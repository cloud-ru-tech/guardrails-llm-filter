package app

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/cloud-ru-tech/guardrails-llm-filter/internal/config"
	"github.com/cloud-ru-tech/guardrails-llm-filter/internal/metrics"
	"github.com/cloud-ru-tech/guardrails-llm-filter/internal/repository"
)

// sentinelCount is far above anything the local Prometheus gatherer can hold in
// a fresh test binary, so finding it in the response proves the value came from
// the shared store rather than from the per-replica gatherer.
const sentinelCount = 424242

// newTestApp builds an App on the in_memory backend with the given summary
// source, drains any counters other tests queued, and seeds one distinctive
// series in the store.
func newTestApp(t *testing.T, source string) *App {
	t.Helper()
	// The pending queue is process-wide by design; clear it so this test only
	// observes its own seed.
	metrics.TakePendingCounters()

	cfg := &config.Config{}
	cfg.Store.Backend = "in_memory"
	cfg.Store.MaskingTTL = time.Minute
	cfg.Metrics.SummarySource = source

	a := New(cfg)
	require.NoError(t, a.Store().IncrCounters(context.Background(), repository.Counters{
		repository.CounterRequestsMasked: {"enforce": sentinelCount},
		repository.CounterPassthrough:    {metrics.PassthroughUnguardedPath: sentinelCount},
	}))
	t.Cleanup(func() { _ = a.Store().Close() })
	return a
}

func summaryOf(t *testing.T, a *App) metricsSummary {
	t.Helper()
	rec := httptest.NewRecorder()
	a.handleMetricsSummary(rec, httptest.NewRequest(http.MethodGet, "/v1/metrics/summary", nil), nil)
	require.Equal(t, http.StatusOK, rec.Code)

	var got metricsSummary
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &got))
	return got
}

func TestMetricsSummaryServesStoredCounters(t *testing.T) {
	a := newTestApp(t, config.MetricsSummaryStore)

	got := summaryOf(t, a)

	assert.Equal(t, float64(sentinelCount), got.RequestsMaskedTotal["enforce"])
	assert.Equal(t, float64(sentinelCount), got.PassthroughTotal[metrics.PassthroughUnguardedPath])
	// Latency stays per-replica and must still be present.
	assert.Contains(t, got.LatencySeconds, "pipeline")
}

func TestMetricsSummaryLocalSourceIgnoresStore(t *testing.T) {
	a := newTestApp(t, config.MetricsSummaryLocal)

	got := summaryOf(t, a)

	assert.NotEqual(t, float64(sentinelCount), got.RequestsMaskedTotal["enforce"],
		"local source must read the per-replica gatherer, not the shared store")
	assert.NotEqual(t, float64(sentinelCount), got.PassthroughTotal[metrics.PassthroughUnguardedPath])
}

func TestCountersUseStoreResolution(t *testing.T) {
	cases := []struct {
		name    string
		source  string
		backend string
		want    bool
	}{
		{"auto on in_memory stays local", config.MetricsSummaryAuto, "in_memory", false},
		{"auto on redis distributes", config.MetricsSummaryAuto, "redis", true},
		{"auto on postgres distributes", config.MetricsSummaryAuto, "postgres", true},
		{"store forces distribution on in_memory", config.MetricsSummaryStore, "in_memory", true},
		{"local wins over a shared backend", config.MetricsSummaryLocal, "redis", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg := &config.Config{}
			cfg.Store.Backend = tc.backend
			cfg.Metrics.SummarySource = tc.source

			a := New(cfg)
			assert.Equal(t, tc.want, a.countersUseStore())
		})
	}
}
