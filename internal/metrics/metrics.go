package metrics

import (
	"sync"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"

	"github.com/cloud-ru-tech/guardrails-llm-filter/internal/repository"
)

const namespace = "extproc_guardrails"

const (
	modeFull = "full"
	modeSSE  = "sse"
)

// Passthrough labels of the passthrough counter family. They are shared by the
// local mirror below and by /v1/metrics/summary, which reports the same three
// series, so the two can never drift apart.
const (
	PassthroughUnknownFormat     = "unknown_format"
	PassthroughUnguardedPath     = "unguarded_path"
	PassthroughUnsupportedSchema = "unsupported_schema"
)

// maxPendingCounterSeries bounds the pending distributed-counter map. Series are
// (kind, label) pairs drawn from a bounded set — four families, at most
// GUARDRAILS_RULES_MAX_CUSTOM rule IDs, a fixed data-type list — so the cap is
// a backstop against a misbehaving caller, not a routine limit. Hitting it
// drops the new series (metered) instead of growing without bound.
const maxPendingCounterSeries = 10000

// CounterKey identifies one distributed counter series: a family plus its label
// value.
type CounterKey struct {
	Kind  repository.CounterKind
	Label string
}

var (
	pendingCountersMu sync.Mutex
	pendingCounters   = make(map[CounterKey]int64)
)

var (
	latencyBuckets = []float64{0.001, 0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2, 5, 10, 20, 60, 120}
	countBuckets   = []float64{0, 5, 10, 20, 50, 100, 150, 200, 300}
	// UTF-8 byte length; upper bounds through multi‑MB prompts / aggregated fields.
	textByteBuckets = []float64{0, 256, 1024, 4096, 16384, 65536, 262144, 1048576, 4194304, 16777216, 67108864}

	pipelineDuration = promauto.NewHistogram(
		prometheus.HistogramOpts{
			Namespace: namespace,
			Name:      "pipeline_duration_seconds",
			Help:      "Total time spent in the guardrails pipeline for one request.",
			Buckets:   latencyBuckets,
		},
	)

	maskDuration = promauto.NewHistogram(
		prometheus.HistogramOpts{
			Namespace: namespace,
			Name:      "mask_duration_seconds",
			Help:      "Time spent masking and mutating request content.",
			Buckets:   latencyBuckets,
		},
	)

	maskScanDuration = promauto.NewHistogram(
		prometheus.HistogramOpts{
			Namespace: namespace,
			Name:      "mask_scan_duration_seconds",
			Help:      "Wall-clock time spent in the scan phase of one mask invocation (all text fields; lower than the sum of per-text times when the scan fans out concurrently).",
			Buckets:   latencyBuckets,
		},
	)

	scanDuration = promauto.NewHistogram(
		prometheus.HistogramOpts{
			Namespace: namespace,
			Name:      "scan_duration_seconds",
			Help:      "Time spent scanning one text field from request content.",
			Buckets:   latencyBuckets,
		},
	)

	maskTextsCount = promauto.NewHistogram(
		prometheus.HistogramOpts{
			Namespace: namespace,
			Name:      "mask_texts_count",
			Help:      "Number of request text fields scanned in one mask invocation.",
			Buckets:   countBuckets,
		},
	)

	maskScanTextBytes = promauto.NewHistogram(
		prometheus.HistogramOpts{
			Namespace: namespace,
			Name:      "mask_scan_text_bytes",
			Help:      "UTF-8 byte length of one request text field scanned for masking.",
			Buckets:   textByteBuckets,
		},
	)

	maskScanTotalBytes = promauto.NewHistogram(
		prometheus.HistogramOpts{
			Namespace: namespace,
			Name:      "mask_scan_total_bytes",
			Help:      "Sum of UTF-8 byte lengths of all text fields in one successful mask invocation.",
			Buckets:   textByteBuckets,
		},
	)

	demaskDuration = promauto.NewHistogram(
		prometheus.HistogramOpts{
			Namespace: namespace,
			Name:      "demask_duration_seconds",
			Help:      "Time spent demasking a full response body.",
			Buckets:   latencyBuckets,
		},
	)

	sseChunkDemaskDuration = promauto.NewHistogram(
		prometheus.HistogramOpts{
			Namespace: namespace,
			Name:      "sse_chunk_demask_duration_seconds",
			Help:      "Time spent demasking one SSE response body chunk.",
			Buckets:   latencyBuckets,
		},
	)

	triggeredRules = promauto.NewHistogram(
		prometheus.HistogramOpts{
			Namespace: namespace,
			Name:      "triggered_rules_count",
			Help:      "Number of distinct guardrails rules triggered by one scanned request.",
			Buckets:   countBuckets,
		},
	)

	ruleTriggers = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: namespace,
			Name:      "rule_triggers_total",
			Help:      "Total number of requests where a guardrails rule was triggered.",
		},
		[]string{"rule_id"},
	)

	dataTypeTriggers = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: namespace,
			Name:      "data_type_triggers_total",
			Help:      "Total number of requests where a guardrails data type was triggered.",
		},
		[]string{"data_type"},
	)

	maskFailures = promauto.NewCounter(
		prometheus.CounterOpts{
			Namespace: namespace,
			Name:      "mask_failures_total",
			Help:      "Total number of request masking failures.",
		},
	)

	demaskFailures = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: namespace,
			Name:      "demask_failures_total",
			Help:      "Total number of response demasking failures and fallbacks.",
		},
		[]string{"mode"},
	)

	maskingStateStoreFailures = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: namespace,
			Name:      "masking_state_store_failures_total",
			Help:      "Total number of masking-state store failures by operation (fail-open).",
		},
		[]string{"op"},
	)

	requestsMasked = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: namespace,
			Name:      "requests_masked_total",
			Help: "Total number of requests where at least one value was masked " +
				"(mode=enforce) or would have been masked (mode=detect).",
		},
		[]string{"mode"},
	)

	auditStoreFailures = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: namespace,
			Name:      "audit_store_failures_total",
			Help:      "Total number of audit store failures by operation (fail-open on write).",
		},
		[]string{"op"},
	)

	auditRecordsDropped = promauto.NewCounter(
		prometheus.CounterOpts{
			Namespace: namespace,
			Name:      "audit_records_dropped_total",
			Help:      "Total number of audit records dropped because the async write queue was full.",
		},
	)

	unknownFormatPassthrough = promauto.NewCounter(
		prometheus.CounterOpts{
			Namespace: namespace,
			Name:      "unknown_format_passthrough_total",
			Help:      "Total number of response bodies passed through unchanged because their API format was unknown (fail-open).",
		},
	)

	unguardedPathPassthrough = promauto.NewCounter(
		prometheus.CounterOpts{
			Namespace: namespace,
			Name:      "unguarded_path_passthrough_total",
			Help:      "Total number of requests passed through unmasked because their path matched no guarded LLM path (mask/pass — never block). A non-zero rate usually means the service is receiving requests on paths outside GUARDRAILS_PATHS.",
		},
	)

	unsupportedBodySchema = promauto.NewCounter(
		prometheus.CounterOpts{
			Namespace: namespace,
			Name:      "unsupported_body_schema_total",
			Help:      "Total number of requests on a guarded path whose body schema the resolved API format could not extract, so they passed through unmasked (fail-open). A non-zero rate usually means a path is mapped to the wrong format in GUARDRAILS_PATHS (e.g. legacy /v1/completions, which is unsupported).",
		},
	)

	counterMirrorDropped = promauto.NewCounter(
		prometheus.CounterOpts{
			Namespace: namespace,
			Name:      "counter_mirror_dropped_total",
			Help:      "Total number of distributed-counter series dropped because the pending buffer was full. Non-zero means the shared store is unreachable or far slower than the traffic.",
		},
	)
)

// mirrorIncr queues one increment for the shared-store counters. It is the only
// thing the hot path pays for the distributed summary: an in-process map update,
// no store round trip. internal/service/counters drains the queue on an interval.
func mirrorIncr(kind repository.CounterKind, label string) {
	key := CounterKey{Kind: kind, Label: label}

	pendingCountersMu.Lock()
	defer pendingCountersMu.Unlock()
	if _, queued := pendingCounters[key]; !queued && len(pendingCounters) >= maxPendingCounterSeries {
		counterMirrorDropped.Inc()
		return
	}
	pendingCounters[key]++
}

// TakePendingCounters atomically swaps out and returns the counter deltas
// queued since the previous call, or nil when nothing is pending. The caller
// owns the result and must account for it: on a failed store write it should
// hand the deltas back with RestorePendingCounters rather than drop them.
func TakePendingCounters() map[CounterKey]int64 {
	pendingCountersMu.Lock()
	defer pendingCountersMu.Unlock()
	if len(pendingCounters) == 0 {
		return nil
	}
	taken := pendingCounters
	pendingCounters = make(map[CounterKey]int64)
	return taken
}

// RestorePendingCounters merges deltas from a failed flush back into the pending
// queue. Series that no longer fit the cap are dropped (metered).
func RestorePendingCounters(deltas map[CounterKey]int64) {
	pendingCountersMu.Lock()
	defer pendingCountersMu.Unlock()
	for key, delta := range deltas {
		if _, queued := pendingCounters[key]; !queued && len(pendingCounters) >= maxPendingCounterSeries {
			counterMirrorDropped.Inc()
			continue
		}
		pendingCounters[key] += delta
	}
}

// ObservePipelineDuration records total guardrails pipeline duration.
func ObservePipelineDuration(duration time.Duration) {
	pipelineDuration.Observe(duration.Seconds())
}

// ObserveMaskDuration records request masking duration.
func ObserveMaskDuration(duration time.Duration) {
	maskDuration.Observe(duration.Seconds())
}

// ObserveMaskScanDuration records the wall-clock duration of the whole scan
// phase (all texts) in one mask invocation. Under concurrent fan-out this is
// lower than the sum of the per-text ObserveScanDuration samples.
func ObserveMaskScanDuration(duration time.Duration) {
	maskScanDuration.Observe(duration.Seconds())
}

// ObserveScanDuration records the scan duration of one text field. Under
// concurrent fan-out this per-text wall-clock includes scheduler wait, so it
// reflects per-text cost rather than the request's scan latency (that is
// ObserveMaskScanDuration).
func ObserveScanDuration(duration time.Duration) {
	scanDuration.Observe(duration.Seconds())
}

// ObserveMaskTextsCount records how many texts were scanned in one mask invocation.
func ObserveMaskTextsCount(count int) {
	maskTextsCount.Observe(float64(count))
}

// ObserveMaskScanTextBytes records UTF-8 byte length of one scanned text field.
func ObserveMaskScanTextBytes(byteLen int) {
	maskScanTextBytes.Observe(float64(byteLen))
}

// ObserveMaskScanTotalBytes records total UTF-8 bytes processed in one mask invocation.
func ObserveMaskScanTotalBytes(totalByteLen int) {
	maskScanTotalBytes.Observe(float64(totalByteLen))
}

// ObserveDemaskDuration records full response demasking duration.
func ObserveDemaskDuration(duration time.Duration) {
	demaskDuration.Observe(duration.Seconds())
}

// ObserveSSEChunkDemaskDuration records SSE chunk demasking duration.
func ObserveSSEChunkDemaskDuration(duration time.Duration) {
	sseChunkDemaskDuration.Observe(duration.Seconds())
}

// ObserveTriggeredRules records the number of distinct rules triggered by one request.
func ObserveTriggeredRules(count int) {
	triggeredRules.Observe(float64(count))
}

// IncRuleTrigger records one request-level trigger for a rule.
func IncRuleTrigger(ruleID string) {
	ruleTriggers.With(prometheus.Labels{"rule_id": ruleID}).Inc()
	mirrorIncr(repository.CounterRuleTriggers, ruleID)
}

// IncDataTypeTrigger records one request-level trigger for a data type.
func IncDataTypeTrigger(dataType string) {
	dataTypeTriggers.With(prometheus.Labels{"data_type": dataType}).Inc()
	mirrorIncr(repository.CounterDataTypeTriggers, dataType)
}

// IncMaskFailed records a request masking failure.
func IncMaskFailed() {
	maskFailures.Inc()
}

func incDemaskFailure(mode string) {
	demaskFailures.With(prometheus.Labels{"mode": mode}).Inc()
}

// IncDemaskFullFailed records a full response demasking failure or fallback.
func IncDemaskFullFailed() {
	incDemaskFailure(modeFull)
}

// IncDemaskSSEFailed records an SSE response demasking failure or fallback.
func IncDemaskSSEFailed() {
	incDemaskFailure(modeSSE)
}

// IncMaskingStateStoreFailure records a fail-open masking-state store error.
// op is one of: put, get, delete, decrypt.
func IncMaskingStateStoreFailure(op string) {
	maskingStateStoreFailures.With(prometheus.Labels{"op": op}).Inc()
}

// IncRequestMasked records one request where masking replaced at least one
// value (enforce) or would have (detect). mode is "enforce" or "detect".
func IncRequestMasked(mode string) {
	requestsMasked.With(prometheus.Labels{"mode": mode}).Inc()
	mirrorIncr(repository.CounterRequestsMasked, mode)
}

// IncAuditStoreFailure records an audit store error by operation (put|get|list).
func IncAuditStoreFailure(op string) {
	auditStoreFailures.With(prometheus.Labels{"op": op}).Inc()
}

// IncUnknownFormatPassthrough records a response body passed through unchanged
// because its API format was unknown (fail-open).
func IncUnknownFormatPassthrough() {
	unknownFormatPassthrough.Inc()
	mirrorIncr(repository.CounterPassthrough, PassthroughUnknownFormat)
}

// IncUnguardedPathPassthrough records a request passed through unmasked
// because its path matched no guarded LLM path.
func IncUnguardedPathPassthrough() {
	unguardedPathPassthrough.Inc()
	mirrorIncr(repository.CounterPassthrough, PassthroughUnguardedPath)
}

// IncUnsupportedBodySchema records a request on a guarded path whose body the
// resolved API format could not extract, so it passed through unmasked.
func IncUnsupportedBodySchema() {
	unsupportedBodySchema.Inc()
	mirrorIncr(repository.CounterPassthrough, PassthroughUnsupportedSchema)
}

// IncAuditDropped records an audit record dropped due to write-queue saturation.
func IncAuditDropped() {
	auditRecordsDropped.Inc()
}
