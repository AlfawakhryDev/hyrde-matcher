// Package api is the matcher's HTTP surface.
package api

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"time"

	"github.com/AlfawakhryDev/hyrde-api/internal/events"
	"github.com/AlfawakhryDev/hyrde-api/internal/match"
)

// Publisher is the slice of the Kafka producer the API needs. Defining the
// interface where it is used (not where it is implemented) is idiomatic Go,
// and it lets tests swap in a fake.
type Publisher interface {
	PublishAsync(topic string, e events.Event)
	PublishSync(ctx context.Context, topic string, e events.Event) error
}

type Server struct {
	Index         func() *match.Index // where the current snapshot comes from
	Events        Publisher
	EventsTopic   string // milestone lifecycle, consumed by statsd
	DecisionTopic string // match.served audit trail
	Token         string // shared bearer token; empty refuses every /v1 call
	MaxIndexAge   time.Duration
	Metrics       *Metrics
	Now           func() time.Time
}

// Routes uses the standard library router: since Go 1.22 it matches the
// method and path pattern itself, so no framework is needed.
func (s *Server) Routes(metrics http.Handler) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })
	mux.HandleFunc("GET /readyz", s.ready)
	mux.Handle("GET /metrics", metrics)
	mux.Handle("POST /v1/match", s.auth(http.HandlerFunc(s.match)))
	mux.Handle("POST /v1/events", s.auth(http.HandlerFunc(s.event)))
	return mux
}

// auth is middleware: a function that wraps a handler and decides whether to
// call it. Constant-time comparison so the token can't be guessed by timing.
func (s *Server) auth(next http.Handler) http.Handler {
	want := []byte("Bearer " + s.Token)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got := []byte(r.Header.Get("Authorization"))
		if s.Token == "" || subtle.ConstantTimeCompare(got, want) != 1 {
			writeError(w, http.StatusUnauthorized, "missing or wrong bearer token")
			return
		}
		next.ServeHTTP(w, r)
	})
}

// ready reports 200 only with a snapshot fresh enough to trust, so
// Kubernetes stops sending traffic to a pod whose data has gone stale.
func (s *Server) ready(w http.ResponseWriter, _ *http.Request) {
	ix := s.Index()
	if ix == nil {
		writeError(w, http.StatusServiceUnavailable, "engineer index not loaded yet")
		return
	}
	if age := s.Now().Sub(ix.BuiltAt); age > s.MaxIndexAge {
		writeError(w, http.StatusServiceUnavailable, "engineer index is stale: "+age.Round(time.Second).String())
		return
	}
	w.WriteHeader(http.StatusOK)
}

type matchResponse struct {
	MilestoneID string            `json:"milestoneId"`
	Candidates  []match.Candidate `json:"candidates"`
	IndexAgeMs  int64             `json:"indexAgeMs"`
}

func (s *Server) match(w http.ResponseWriter, r *http.Request) {
	start := s.Now()
	defer func() { s.Metrics.MatchLatency.Observe(s.Now().Sub(start).Seconds()) }() // runs when match returns

	var req match.Request
	if err := decode(w, r, &req); err != nil {
		s.Metrics.Matches.WithLabelValues("bad_request").Inc()
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	ix := s.Index()
	if ix == nil {
		s.Metrics.Matches.WithLabelValues("not_ready").Inc()
		writeError(w, http.StatusServiceUnavailable, "engineer index not loaded yet")
		return
	}

	candidates := ix.Rank(req)
	outcome := "matched"
	if len(candidates) == 0 {
		outcome = "no_candidates"
	}
	s.Metrics.Matches.WithLabelValues(outcome).Inc()

	// The audit event never slows the response down: queue and move on.
	ids := make([]string, len(candidates))
	for i, c := range candidates {
		ids[i] = c.EngineerID
	}
	s.Events.PublishAsync(s.DecisionTopic, events.Event{
		ID: req.MilestoneID + ":" + start.Format(time.RFC3339Nano), Type: events.MatchServed,
		MilestoneID: req.MilestoneID, Candidates: ids, At: start,
	})

	writeJSON(w, http.StatusOK, matchResponse{MilestoneID: req.MilestoneID, Candidates: candidates, IndexAgeMs: s.Now().Sub(ix.BuiltAt).Milliseconds()})
}

// event accepts a lifecycle event and waits for Kafka to acknowledge it, so a
// 202 means the event will be counted. 503 tells the caller to retry.
func (s *Server) event(w http.ResponseWriter, r *http.Request) {
	var e events.Event
	if err := decode(w, r, &e); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if e.Type == events.MatchServed {
		writeError(w, http.StatusBadRequest, "match.served is written by the matcher itself")
		return
	}
	if err := e.Validate(); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if e.At.IsZero() {
		e.At = s.Now()
	}
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	if err := s.Events.PublishSync(ctx, s.EventsTopic, e); err != nil {
		slog.Error("publish event", "id", e.ID, "err", err)
		writeError(w, http.StatusServiceUnavailable, "could not record the event; retry")
		return
	}
	s.Metrics.EventsAccepted.Inc()
	w.WriteHeader(http.StatusAccepted)
}

const maxBody = 64 << 10 // 64 KiB: a milestone request is a few hundred bytes

func decode(w http.ResponseWriter, r *http.Request, v any) error {
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxBody))
	dec.DisallowUnknownFields() // a typo in a field name is an error, not silently ignored
	if err := dec.Decode(v); err != nil {
		var tooBig *http.MaxBytesError
		if errors.As(err, &tooBig) {
			return errors.New("request body too large")
		}
		return errors.New("invalid JSON: " + err.Error())
	}
	return nil
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}
