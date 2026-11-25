// Command matcher answers "who should take this milestone?" in milliseconds.
package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/AlfawakhryDev/hyrde-api/internal/api"
	"github.com/AlfawakhryDev/hyrde-api/internal/env"
	"github.com/AlfawakhryDev/hyrde-api/internal/events"
	"github.com/AlfawakhryDev/hyrde-api/internal/match"
	"github.com/AlfawakhryDev/hyrde-api/internal/store"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

func main() {
	slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stdout, nil))) // JSON logs for the log pipeline
	if err := run(); err != nil {
		slog.Error("matcher stopped", "err", err)
		os.Exit(1)
	}
}

func run() error {
	// The context is cancelled on SIGTERM, which is how Kubernetes asks a pod to stop.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	interval := env.Duration("REFRESH_INTERVAL", 5*time.Second)
	db, err := store.New(ctx, env.String("AWS_REGION", "us-east-1"), env.String("DYNAMODB_ENDPOINT", ""), env.String("TABLE_PREFIX", "hyrde_"))
	if err != nil {
		return err
	}
	producer, err := events.NewProducer(env.List("KAFKA_BROKERS", "localhost:9092"))
	if err != nil {
		return err
	}
	defer producer.Close(context.Background())

	refresher := match.NewRefresher(db, interval)
	if err := refresher.Load(ctx); err != nil {
		slog.Warn("first index load failed; not ready until a refresh succeeds", "err", err)
	}
	go refresher.Run(ctx) // `go` starts a goroutine: a function running concurrently

	reg := prometheus.NewRegistry()
	reg.MustRegister(collectors.NewGoCollector(), collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}))
	metrics := api.NewMetrics(reg)
	reg.MustRegister(
		prometheus.NewCounterFunc(prometheus.CounterOpts{Name: "hyrde_events_dropped_total", Help: "Audit events dropped because the Kafka buffer was full."},
			func() float64 { return float64(producer.Dropped()) }),
		prometheus.NewCounterFunc(prometheus.CounterOpts{Name: "hyrde_index_refresh_failures_total", Help: "Index refreshes that failed."},
			func() float64 { return float64(refresher.Failures()) }),
	)
	go func() { // keep the index gauges current
		for range time.Tick(time.Second) {
			if ix := refresher.Index(); ix != nil {
				metrics.IndexSize.Set(float64(ix.Len()))
				metrics.IndexAge.Set(time.Since(ix.BuiltAt).Seconds())
			}
		}
	}()

	srv := &api.Server{
		Index: refresher.Index, Events: producer, Metrics: metrics, Now: time.Now,
		EventsTopic:   env.String("EVENTS_TOPIC", "hyrde.milestone-events"),
		DecisionTopic: env.String("DECISIONS_TOPIC", "hyrde.match-decisions"),
		Token:         env.String("API_TOKEN", ""),
		MaxIndexAge:   3 * interval,
	}
	if srv.Token == "" {
		slog.Warn("API_TOKEN is empty: every /v1 call will be refused")
	}
	httpServer := &http.Server{
		Addr:              ":" + env.String("PORT", "8080"),
		Handler:           srv.Routes(promhttp.HandlerFor(reg, promhttp.HandlerOpts{})),
		ReadHeaderTimeout: 2 * time.Second, // slow clients can't hold connections open
		ReadTimeout:       5 * time.Second,
		WriteTimeout:      5 * time.Second,
	}

	errs := make(chan error, 1)
	go func() { errs <- httpServer.ListenAndServe() }()
	slog.Info("matcher listening", "addr", httpServer.Addr)

	select {
	case err := <-errs:
		return err
	case <-ctx.Done():
	}
	// Finish in-flight requests, then exit: a rolling deploy drops nothing.
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := httpServer.Shutdown(shutdownCtx); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	slog.Info("matcher stopped cleanly")
	return nil
}
