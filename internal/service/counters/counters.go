// Package counters mirrors the in-process masking counters into the shared
// store so /v1/metrics/summary reports the same lifetime numbers on every
// replica.
//
// The data path never talks to the store: internal/metrics queues increments in
// memory, and Mirror drains that queue on a fixed interval with a single batched
// write per flush. A failed flush hands its deltas back to the queue, so an
// unreachable store costs bounded memory rather than a stalled request, and the
// counters catch up once it recovers.
package counters

import (
	"context"
	"time"

	"github.com/cloud-ru-tech/guardrails-llm-filter/internal/logging"
	"github.com/cloud-ru-tech/guardrails-llm-filter/internal/metrics"
	"github.com/cloud-ru-tech/guardrails-llm-filter/internal/repository"
)

// DefaultFlushInterval is how often queued deltas reach the store. It bounds
// both how stale /v1/metrics/summary can be and how much is lost if the process
// exits without a final flush.
const DefaultFlushInterval = 5 * time.Second

// writeTimeout bounds one flush write.
const writeTimeout = 5 * time.Second

// CounterStore is the write slice of repository.CounterStore the mirror needs.
type CounterStore interface {
	IncrCounters(ctx context.Context, deltas repository.Counters) error
}

// Mirror drains metrics' pending counter deltas into the store.
type Mirror struct {
	store    CounterStore
	interval time.Duration
}

// New creates a Mirror. A non-positive interval falls back to
// DefaultFlushInterval.
func New(store CounterStore, interval time.Duration) *Mirror {
	if interval <= 0 {
		interval = DefaultFlushInterval
	}
	return &Mirror{store: store, interval: interval}
}

// Run flushes on every tick until ctx is done. It blocks, so callers run it in
// a goroutine.
func (m *Mirror) Run(ctx context.Context) {
	ticker := time.NewTicker(m.interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			m.Flush(ctx)
		}
	}
}

// Flush writes every queued delta in one batch. It never reports an error: a
// failed write is logged and its deltas are returned to the queue, because
// counters are telemetry and must not affect the data path. ctx bounds the
// write — a caller on a request path should pass the request context so a slow
// store cannot outlive the request.
func (m *Mirror) Flush(ctx context.Context) {
	pending := metrics.TakePendingCounters()
	if len(pending) == 0 {
		return
	}

	writeCtx, cancel := context.WithTimeout(ctx, writeTimeout)
	defer cancel()

	if err := m.store.IncrCounters(writeCtx, groupByKind(pending)); err != nil {
		metrics.RestorePendingCounters(pending)
		logging.Warn(ctx, "Failed to persist distributed masking counters; will retry",
			"series", len(pending), "error", err)
	}
}

// groupByKind reshapes metrics' flat pending map into the store's
// kind → label → delta form.
func groupByKind(pending map[metrics.CounterKey]int64) repository.Counters {
	out := make(repository.Counters, len(repository.CounterKinds))
	for key, delta := range pending {
		if out[key.Kind] == nil {
			out[key.Kind] = make(map[string]int64)
		}
		out[key.Kind][key.Label] += delta
	}
	return out
}
