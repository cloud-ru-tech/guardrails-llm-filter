package counters

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/cloud-ru-tech/guardrails-llm-filter/internal/metrics"
	"github.com/cloud-ru-tech/guardrails-llm-filter/internal/repository"
)

// fakeStore records every batch it is handed, optionally failing.
type fakeStore struct {
	mu      sync.Mutex
	batches []repository.Counters
	err     error
}

func (f *fakeStore) IncrCounters(_ context.Context, deltas repository.Counters) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.err != nil {
		return f.err
	}
	f.batches = append(f.batches, deltas)
	return nil
}

func (f *fakeStore) snapshot() []repository.Counters {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]repository.Counters(nil), f.batches...)
}

// resetPending clears the package-level queue between tests, since
// internal/metrics keeps it in a process-wide map by design.
func resetPending() { metrics.TakePendingCounters() }

func TestFlushBatchesQueuedDeltas(t *testing.T) {
	resetPending()
	store := &fakeStore{}
	m := New(store, time.Hour)

	// Two increments of the same series collapse into one delta; distinct
	// series stay distinct.
	metrics.IncRequestMasked("enforce")
	metrics.IncRequestMasked("enforce")
	metrics.IncRequestMasked("detect")
	metrics.IncRuleTrigger("pii.email")
	metrics.IncUnguardedPathPassthrough()

	m.Flush(context.Background())

	batches := store.snapshot()
	require.Len(t, batches, 1)
	assert.Equal(t, repository.Counters{
		repository.CounterRequestsMasked: {"enforce": 2, "detect": 1},
		repository.CounterRuleTriggers:   {"pii.email": 1},
		repository.CounterPassthrough:    {"unguarded_path": 1},
	}, batches[0])
}

func TestFlushWithNothingPendingIsNoOp(t *testing.T) {
	resetPending()
	store := &fakeStore{}
	m := New(store, time.Hour)

	m.Flush(context.Background())

	assert.Empty(t, store.snapshot())
}

func TestFlushFailureRequeuesDeltas(t *testing.T) {
	resetPending()
	store := &fakeStore{err: errors.New("store down")}
	m := New(store, time.Hour)

	metrics.IncRequestMasked("enforce")
	m.Flush(context.Background())
	assert.Empty(t, store.snapshot(), "failed write must not be recorded")

	// The deltas survive the failure: a later successful flush carries the
	// original increment plus anything queued in between.
	store.mu.Lock()
	store.err = nil
	store.mu.Unlock()
	metrics.IncRequestMasked("enforce")

	m.Flush(context.Background())

	batches := store.snapshot()
	require.Len(t, batches, 1)
	assert.Equal(t, int64(2), batches[0][repository.CounterRequestsMasked]["enforce"])
}

func TestRunFlushesOnIntervalThenStops(t *testing.T) {
	resetPending()
	store := &fakeStore{}
	m := New(store, 5*time.Millisecond)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		m.Run(ctx)
		close(done)
	}()

	metrics.IncRequestMasked("enforce")
	require.Eventually(t, func() bool { return len(store.snapshot()) > 0 }, time.Second, time.Millisecond)

	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("Run did not stop after context cancellation")
	}
}

func TestNewFallsBackToDefaultInterval(t *testing.T) {
	assert.Equal(t, DefaultFlushInterval, New(&fakeStore{}, 0).interval)
	assert.Equal(t, DefaultFlushInterval, New(&fakeStore{}, -time.Second).interval)
	assert.Equal(t, time.Second, New(&fakeStore{}, time.Second).interval)
}
