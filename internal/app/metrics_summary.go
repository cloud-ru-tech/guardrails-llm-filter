package app

import (
	"math"
	"net/http"
	"sort"

	promdto "github.com/prometheus/client_model/go"

	"github.com/cloud-ru-tech/guardrails-llm-filter/internal/logging"
	"github.com/cloud-ru-tech/guardrails-llm-filter/internal/metrics"
	"github.com/cloud-ru-tech/guardrails-llm-filter/internal/repository"
)

// metricsNamespace is the Prometheus name prefix shared by both services
// (see internal/metrics). Family names below are namespace + metric name.
const metricsNamespace = "extproc_guardrails_"

// maxTopLabels caps the per-label breakdowns (rule_id, data_type) so a large
// custom-rule set cannot bloat the summary response.
const maxTopLabels = 20

// metricsSummary is the GET /v1/metrics/summary response: JSON aggregates of
// the registered Prometheus collectors, so a console shows lifetime numbers
// without a Prometheus/Grafana stack.
type metricsSummary struct {
	RequestsMaskedTotal   map[string]float64 `json:"requests_masked_total"`
	RuleTriggersTotal     []labelCount       `json:"rule_triggers_total"`
	DataTypeTriggersTotal []labelCount       `json:"data_type_triggers_total"`
	PassthroughTotal      map[string]float64 `json:"passthrough_total"`
	LatencySeconds        map[string]latency `json:"latency_seconds"`
}

type labelCount struct {
	Label string  `json:"label"`
	Count float64 `json:"count"`
}

type latency struct {
	Count uint64  `json:"count"`
	P50   float64 `json:"p50"`
	P95   float64 `json:"p95"`
}

// handleMetricsSummary aggregates the default Prometheus gatherer into a compact
// JSON summary. When the distributed counters are enabled, the monotonic
// families are overridden with the shared-store totals so every replica reports
// the same numbers; latency percentiles stay per-replica.
func (e *App) handleMetricsSummary(w http.ResponseWriter, r *http.Request, _ map[string]string) {
	families, err := e.metricsGatherer.Gather()
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to gather metrics"})
		return
	}
	byName := make(map[string]*promdto.MetricFamily, len(families))
	for _, f := range families {
		byName[f.GetName()] = f
	}

	summary := metricsSummary{
		RequestsMaskedTotal:   counterByLabel(byName[metricsNamespace+"requests_masked_total"], "mode"),
		RuleTriggersTotal:     topCountersByLabel(byName[metricsNamespace+"rule_triggers_total"], "rule_id"),
		DataTypeTriggersTotal: topCountersByLabel(byName[metricsNamespace+"data_type_triggers_total"], "data_type"),
		PassthroughTotal: map[string]float64{
			metrics.PassthroughUnknownFormat:     counterTotal(byName[metricsNamespace+"unknown_format_passthrough_total"]),
			metrics.PassthroughUnguardedPath:     counterTotal(byName[metricsNamespace+"unguarded_path_passthrough_total"]),
			metrics.PassthroughUnsupportedSchema: counterTotal(byName[metricsNamespace+"unsupported_body_schema_total"]),
		},
		LatencySeconds: map[string]latency{
			"pipeline": histogramLatency(byName[metricsNamespace+"pipeline_duration_seconds"]),
			"mask":     histogramLatency(byName[metricsNamespace+"mask_duration_seconds"]),
			"demask":   histogramLatency(byName[metricsNamespace+"demask_duration_seconds"]),
		},
	}

	// Flush first so this replica's own pending deltas are part of the total it
	// is about to report; otherwise a burst served by this pod would be missing
	// from a summary read taken moments later.
	if mirror := e.CountersMirror(); mirror != nil {
		mirror.Flush(r.Context())
		stored, readErr := e.Store().ReadCounters(r.Context())
		if readErr != nil {
			// Fail open to the per-replica reading: a console that shows a
			// partial number beats one that shows an error.
			logging.Warn(r.Context(), "Failed to read distributed counters; serving per-replica values", "error", readErr)
		} else {
			applyStoredCounters(&summary, stored)
		}
	}

	writeJSON(w, http.StatusOK, summary)
}

// applyStoredCounters overrides the shared-store families in a summary. The
// latency maps are deliberately left alone: percentiles do not aggregate by
// summing, so a cross-replica percentile needs an HDR histogram or bucket sums
// in the store — a separate concern from these lifetime counters.
func applyStoredCounters(s *metricsSummary, stored repository.Counters) {
	s.RequestsMaskedTotal = toFloatCounts(stored[repository.CounterRequestsMasked])
	s.RuleTriggersTotal = topLabelCounts(toFloatCounts(stored[repository.CounterRuleTriggers]))
	s.DataTypeTriggersTotal = topLabelCounts(toFloatCounts(stored[repository.CounterDataTypeTriggers]))
	s.PassthroughTotal = map[string]float64{
		metrics.PassthroughUnknownFormat:     float64(stored[repository.CounterPassthrough][metrics.PassthroughUnknownFormat]),
		metrics.PassthroughUnguardedPath:     float64(stored[repository.CounterPassthrough][metrics.PassthroughUnguardedPath]),
		metrics.PassthroughUnsupportedSchema: float64(stored[repository.CounterPassthrough][metrics.PassthroughUnsupportedSchema]),
	}
}

// toFloatCounts widens an int64 counter series for the JSON summary, whose
// numbers are float64 to match the Prometheus-derived shape.
func toFloatCounts(series map[string]int64) map[string]float64 {
	out := make(map[string]float64, len(series))
	for label, value := range series {
		out[label] = float64(value)
	}
	return out
}

func labelValue(m *promdto.Metric, name string) string {
	for _, l := range m.GetLabel() {
		if l.GetName() == name {
			return l.GetValue()
		}
	}
	return ""
}

func counterByLabel(f *promdto.MetricFamily, label string) map[string]float64 {
	out := make(map[string]float64)
	if f == nil {
		return out
	}
	for _, m := range f.GetMetric() {
		out[labelValue(m, label)] += m.GetCounter().GetValue()
	}
	return out
}

func topCountersByLabel(f *promdto.MetricFamily, label string) []labelCount {
	return topLabelCounts(counterByLabel(f, label))
}

// topLabelCounts orders a label→count series by descending count, ties broken by
// label so the output is deterministic, and caps it at maxTopLabels.
func topLabelCounts(agg map[string]float64) []labelCount {
	out := make([]labelCount, 0, len(agg))
	for k, v := range agg {
		out = append(out, labelCount{Label: k, Count: v})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Count != out[j].Count {
			return out[i].Count > out[j].Count
		}
		return out[i].Label < out[j].Label
	})
	if len(out) > maxTopLabels {
		out = out[:maxTopLabels]
	}
	return out
}

func counterTotal(f *promdto.MetricFamily) float64 {
	var total float64
	if f == nil {
		return 0
	}
	for _, m := range f.GetMetric() {
		total += m.GetCounter().GetValue()
	}
	return total
}

func histogramLatency(f *promdto.MetricFamily) latency {
	if f == nil || len(f.GetMetric()) == 0 {
		return latency{}
	}
	h := f.GetMetric()[0].GetHistogram()
	if h == nil {
		return latency{}
	}
	return latency{
		Count: h.GetSampleCount(),
		P50:   approxQuantile(h, 0.50),
		P95:   approxQuantile(h, 0.95),
	}
}

// approxQuantile estimates a quantile from a Prometheus histogram by linear
// interpolation within the bucket the target rank falls into.
func approxQuantile(h *promdto.Histogram, q float64) float64 {
	count := h.GetSampleCount()
	if count == 0 {
		return 0
	}
	rank := q * float64(count)
	var prevBound, prevCount float64
	for _, b := range h.GetBucket() {
		cum := float64(b.GetCumulativeCount())
		if cum >= rank {
			upper := b.GetUpperBound()
			if math.IsInf(upper, 1) {
				return prevBound
			}
			if cum == prevCount {
				return upper
			}
			frac := (rank - prevCount) / (cum - prevCount)
			return prevBound + frac*(upper-prevBound)
		}
		prevBound = b.GetUpperBound()
		prevCount = cum
	}
	return prevBound
}
