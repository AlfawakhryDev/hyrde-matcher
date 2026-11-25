package match

import (
	"context"
	"log/slog"
	"sync/atomic"
	"time"
)

// Source is anything that can list engineers. An interface here means this
// package never imports the database package: tests pass a fake, production
// passes the DynamoDB store. Go interfaces are satisfied implicitly — the
// store never says "implements Source", it just has the method.
type Source interface {
	Engineers(ctx context.Context) ([]Engineer, error)
}

// Refresher rebuilds the Index on a timer and publishes it atomically.
type Refresher struct {
	src      Source
	interval time.Duration
	current  atomic.Pointer[Index]
	failures atomic.Int64
}

func NewRefresher(src Source, interval time.Duration) *Refresher {
	return &Refresher{src: src, interval: interval}
}

// Index returns the latest snapshot, or nil before the first load.
func (r *Refresher) Index() *Index { return r.current.Load() }

// Failures counts refreshes that failed; the previous snapshot stays in use.
func (r *Refresher) Failures() int64 { return r.failures.Load() }

// Load builds a fresh snapshot now.
func (r *Refresher) Load(ctx context.Context) error {
	all, err := r.src.Engineers(ctx)
	if err != nil {
		r.failures.Add(1)
		return err
	}
	r.current.Store(NewIndex(all, time.Now()))
	return nil
}

// Run refreshes until ctx is cancelled. A failed refresh keeps serving the
// last good snapshot: stale-but-available beats unavailable for matching.
func (r *Refresher) Run(ctx context.Context) {
	t := time.NewTicker(r.interval)
	defer t.Stop()
	for {
		select { // select waits on several channels; whichever is ready first wins
		case <-ctx.Done():
			return
		case <-t.C:
			loadCtx, cancel := context.WithTimeout(ctx, r.interval)
			if err := r.Load(loadCtx); err != nil {
				slog.Warn("engineer index refresh failed; serving previous snapshot", "err", err)
			}
			cancel()
		}
	}
}
