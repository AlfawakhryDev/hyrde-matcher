//go:build integration

// The end-to-end test runs against the real stack (docker compose up), not
// fakes: it proves Kafka, DynamoDB and both services work together.
//
//	go test -tags integration ./e2e
package e2e

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/AlfawakhryDev/hyrde-matcher/internal/match"
	"github.com/AlfawakhryDev/hyrde-matcher/internal/store"
)

const (
	matcher = "http://localhost:8080"
	token   = "dev-token"
)

func post(t *testing.T, path string, body any) *http.Response {
	t.Helper() // failures point at the caller's line, not this one
	b, _ := json.Marshal(body)
	req, _ := http.NewRequest("POST", matcher+path, bytes.NewReader(b))
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	return resp
}

// topCandidate returns the best match, or "" while the matcher is still
// catching up: 503 until its first snapshot loads, no candidates until the
// snapshot includes the engineer we just wrote.
func topCandidate(t *testing.T, skill string) (string, float64) {
	resp := post(t, "/v1/match", map[string]any{"milestoneId": "e2e", "integrations": []string{skill}, "limit": 1})
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusServiceUnavailable {
		return "", 0
	}
	var out struct {
		Candidates []match.Candidate `json:"candidates"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil || resp.StatusCode != http.StatusOK {
		t.Fatalf("match: status %d, err %v", resp.StatusCode, err)
	}
	if len(out.Candidates) == 0 {
		return "", 0
	}
	return out.Candidates[0].EngineerID, out.Candidates[0].Reliability
}

// eventually retries a check until it passes or the deadline hits: the
// system is asynchronous, so the answer arrives "soon", not "now".
func eventually(t *testing.T, within time.Duration, check func() bool) {
	t.Helper()
	deadline := time.Now().Add(within)
	for !check() {
		if time.Now().After(deadline) {
			t.Fatal("condition not met in time")
		}
		time.Sleep(250 * time.Millisecond)
	}
}

func TestTrackRecordFlowsBackIntoMatching(t *testing.T) {
	ctx := context.Background()
	db, err := store.New(ctx, "us-east-1", "http://localhost:8000", "hyrde_")
	if err != nil {
		t.Fatal(err)
	}
	if err := db.EnsureTables(ctx); err != nil {
		t.Fatal(err)
	}

	// A skill nobody else has, so this engineer is the only candidate.
	run := time.Now().UnixNano()
	skill := fmt.Sprintf("e2e-skill-%d", run)
	id := fmt.Sprintf("e2e-eng-%d", run)
	if err := db.PutEngineer(ctx, match.Engineer{ID: id, Name: "E2E", Skills: []string{skill}, Status: "approved", MaxConcurrent: 2}); err != nil {
		t.Fatal(err)
	}

	eventually(t, 15*time.Second, func() bool { got, _ := topCandidate(t, skill); return got == id })
	if _, rel := topCandidate(t, skill); rel != 0.5 {
		t.Fatalf("a newcomer starts at reliability 0.5, got %v", rel)
	}

	yes := true
	evs := []map[string]any{
		{"id": id + "-a", "type": "milestone.assigned", "milestoneId": "m", "engineerId": id},
		{"id": id + "-d", "type": "milestone.delivered", "milestoneId": "m", "engineerId": id, "onTime": yes},
		{"id": id + "-r", "type": "milestone.reviewed", "milestoneId": "m", "engineerId": id, "approved": yes},
	}
	for range 2 { // every event twice: redelivery must not double count
		for _, ev := range evs {
			if resp := post(t, "/v1/events", ev); resp.StatusCode != http.StatusAccepted {
				t.Fatalf("event %v: status %d", ev["id"], resp.StatusCode)
			}
		}
	}

	// Counted once: (1+1)/(1+2) on-time and approval → 2/3. Twice would be 3/4.
	eventually(t, 20*time.Second, func() bool {
		_, rel := topCandidate(t, skill)
		return rel > 0.66 && rel < 0.67
	})
}
