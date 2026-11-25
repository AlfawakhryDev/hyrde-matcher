package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/AlfawakhryDev/hyrde-matcher/internal/events"
	"github.com/AlfawakhryDev/hyrde-matcher/internal/match"
	"github.com/prometheus/client_golang/prometheus"
)

type fakePublisher struct {
	mu      sync.Mutex
	async   []events.Event
	sync    []events.Event
	failing bool
}

func (f *fakePublisher) PublishAsync(_ string, e events.Event) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.async = append(f.async, e)
}

func (f *fakePublisher) PublishSync(_ context.Context, _ string, e events.Event) error {
	if f.failing {
		return errors.New("broker down")
	}
	f.sync = append(f.sync, e)
	return nil
}

func newServer(ix *match.Index, pub *fakePublisher) http.Handler {
	s := &Server{
		Index: func() *match.Index { return ix }, Events: pub, EventsTopic: "e", DecisionTopic: "d",
		Token: "secret", MaxIndexAge: time.Minute, Metrics: NewMetrics(prometheus.NewRegistry()), Now: time.Now,
	}
	return s.Routes(http.NotFoundHandler())
}

func do(h http.Handler, method, path, body, token string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

var engineers = []match.Engineer{
	{ID: "ana", Name: "Ana", Skills: []string{"Procore"}, Status: "approved", MaxConcurrent: 2},
	{ID: "ben", Name: "Ben", Skills: []string{"Yardi"}, Status: "approved", MaxConcurrent: 2},
}

func TestMatchRequiresToken(t *testing.T) {
	h := newServer(match.NewIndex(engineers, time.Now()), &fakePublisher{})
	for _, token := range []string{"", "wrong"} {
		if rec := do(h, "POST", "/v1/match", `{}`, token); rec.Code != http.StatusUnauthorized {
			t.Errorf("token %q: status %d, want 401", token, rec.Code)
		}
	}
}

func TestMatchRanksAndAudits(t *testing.T) {
	pub := &fakePublisher{}
	h := newServer(match.NewIndex(engineers, time.Now()), pub)
	rec := do(h, "POST", "/v1/match", `{"milestoneId":"m1","integrations":["procore"]}`, "secret")
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body)
	}
	var got matchResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if len(got.Candidates) != 1 || got.Candidates[0].EngineerID != "ana" {
		t.Fatalf("candidates = %+v", got.Candidates)
	}
	if len(pub.async) != 1 || pub.async[0].Type != events.MatchServed || pub.async[0].Candidates[0] != "ana" {
		t.Fatalf("audit event = %+v", pub.async)
	}
}

func TestMatchRejectsUnknownFields(t *testing.T) {
	h := newServer(match.NewIndex(engineers, time.Now()), &fakePublisher{})
	if rec := do(h, "POST", "/v1/match", `{"integrationz":["procore"]}`, "secret"); rec.Code != http.StatusBadRequest {
		t.Fatalf("status %d, want 400", rec.Code)
	}
}

func TestNotReadyWithoutIndex(t *testing.T) {
	h := newServer(nil, &fakePublisher{})
	if rec := do(h, "GET", "/readyz", "", ""); rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("readyz %d, want 503", rec.Code)
	}
	if rec := do(h, "POST", "/v1/match", `{}`, "secret"); rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("match %d, want 503", rec.Code)
	}
}

func TestStaleIndexIsNotReady(t *testing.T) {
	h := newServer(match.NewIndex(engineers, time.Now().Add(-time.Hour)), &fakePublisher{})
	if rec := do(h, "GET", "/readyz", "", ""); rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("readyz %d, want 503", rec.Code)
	}
}

func TestEventIsAcceptedOnlyOnceKafkaHasIt(t *testing.T) {
	ok := `{"id":"ev1","type":"milestone.delivered","milestoneId":"m1","engineerId":"ana","onTime":true}`
	pub := &fakePublisher{}
	h := newServer(match.NewIndex(engineers, time.Now()), pub)
	if rec := do(h, "POST", "/v1/events", ok, "secret"); rec.Code != http.StatusAccepted || len(pub.sync) != 1 {
		t.Fatalf("status %d, published %d", rec.Code, len(pub.sync))
	}
	if rec := do(h, "POST", "/v1/events", `{"id":"ev2","type":"milestone.delivered","milestoneId":"m1","engineerId":"ana"}`, "secret"); rec.Code != http.StatusBadRequest {
		t.Fatalf("missing onTime: status %d, want 400", rec.Code)
	}
	down := newServer(match.NewIndex(engineers, time.Now()), &fakePublisher{failing: true})
	if rec := do(down, "POST", "/v1/events", ok, "secret"); rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("broker down: status %d, want 503", rec.Code)
	}
}
