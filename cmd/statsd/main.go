// Command statsd consumes milestone events from Kafka and keeps each
// engineer's track record in DynamoDB, which the matcher ranks on.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/AlfawakhryDev/hyrde-matcher/internal/env"
	"github.com/AlfawakhryDev/hyrde-matcher/internal/events"
	"github.com/AlfawakhryDev/hyrde-matcher/internal/store"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/twmb/franz-go/pkg/kgo"
)

var (
	applied    = prometheus.NewCounter(prometheus.CounterOpts{Name: "hyrde_stats_events_applied_total", Help: "Events that changed an engineer's record."})
	duplicates = prometheus.NewCounter(prometheus.CounterOpts{Name: "hyrde_stats_events_duplicate_total", Help: "Redelivered events skipped."})
	rejected   = prometheus.NewCounterVec(prometheus.CounterOpts{Name: "hyrde_stats_events_rejected_total", Help: "Events that can never apply."}, []string{"reason"})
)

func main() {
	slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stdout, nil)))
	if err := run(); err != nil {
		slog.Error("statsd stopped", "err", err)
		os.Exit(1)
	}
}

func run() error {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	db, err := store.New(ctx, env.String("AWS_REGION", "us-east-1"), env.String("DYNAMODB_ENDPOINT", ""), env.String("TABLE_PREFIX", "hyrde_"))
	if err != nil {
		return err
	}
	client, err := kgo.NewClient(
		kgo.SeedBrokers(env.List("KAFKA_BROKERS", "localhost:9092")...),
		kgo.ConsumerGroup(env.String("CONSUMER_GROUP", "hyrde-statsd")), // replicas share partitions
		kgo.ConsumeTopics(env.String("EVENTS_TOPIC", "hyrde.milestone-events")),
		kgo.DisableAutoCommit(), // commit only after DynamoDB has the change
		kgo.AllowAutoTopicCreation(),
	)
	if err != nil {
		return err
	}
	defer client.Close()

	reg := prometheus.NewRegistry()
	reg.MustRegister(applied, duplicates, rejected)
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })
	mux.Handle("GET /metrics", promhttp.HandlerFor(reg, promhttp.HandlerOpts{}))
	go func() { _ = http.ListenAndServe(":"+env.String("PORT", "9091"), mux) }()

	slog.Info("statsd consuming")
	for {
		fetches := client.PollFetches(ctx)
		if ctx.Err() != nil {
			slog.Info("statsd stopped cleanly")
			return nil
		}
		fetches.EachError(func(topic string, p int32, err error) {
			slog.Warn("fetch error", "topic", topic, "partition", p, "err", err)
		})
		fetches.EachRecord(func(r *kgo.Record) { handle(ctx, db, r) })
		// At-least-once: offsets move only after every record above was applied,
		// so a crash replays them and Apply's idempotency absorbs the repeats.
		if err := client.CommitUncommittedOffsets(ctx); err != nil && ctx.Err() == nil {
			slog.Warn("commit failed; records will be redelivered", "err", err)
		}
	}
}

func handle(ctx context.Context, db *store.Store, r *kgo.Record) {
	var e events.Event
	if err := json.Unmarshal(r.Value, &e); err != nil {
		rejected.WithLabelValues("bad_json").Inc()
		slog.Error("skipping undecodable event", "offset", r.Offset, "err", err)
		return
	}
	if err := e.Validate(); err != nil {
		rejected.WithLabelValues("invalid").Inc()
		slog.Error("skipping invalid event", "id", e.ID, "err", err)
		return
	}
	// Retry a transient failure (DynamoDB throttling, a network blip) with
	// backoff rather than skip it: skipping would lose a count forever.
	for wait := 100 * time.Millisecond; ; wait = min(wait*2, 10*time.Second) {
		ok, err := db.Apply(ctx, e)
		switch {
		case err == nil && ok:
			applied.Inc()
			return
		case err == nil:
			duplicates.Inc()
			return
		case errors.Is(err, store.ErrUnknownEngineer):
			rejected.WithLabelValues("unknown_engineer").Inc()
			slog.Error("event names an unknown engineer", "id", e.ID, "engineer", e.EngineerID)
			return
		}
		slog.Warn("apply failed; retrying", "id", e.ID, "err", err, "wait", wait)
		select {
		case <-ctx.Done():
			return
		case <-time.After(wait):
		}
	}
}
