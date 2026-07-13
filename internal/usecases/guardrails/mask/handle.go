package mask

import (
	"context"
	"fmt"
	"runtime"
	"sync"
	"time"

	"github.com/cloud-ru-tech/guardrails-llm-filter/internal/metrics"
	"github.com/cloud-ru-tech/guardrails-llm-filter/internal/models"
	"github.com/cloud-ru-tech/guardrails-llm-filter/pkg/guardrails/regex/registry"
	"github.com/cloud-ru-tech/guardrails-llm-filter/pkg/guardrails/regex/scanners/sensitive"
)

// defaultParallelMinBytes is the fallback combined-text-size gate for text-level
// parallel scanning, used when no explicit threshold is configured (direct
// construction, tests, benchmarks). Below this size the goroutine and scheduling
// overhead outweighs the win, so the hot path stays sequential (most requests
// carry a handful of small fields). Mirrors the size-gate precedent in the
// placeholder scanner. In production this is driven by GUARDRAILS_MASK_PARALLEL_
// MIN_BYTES (config default 8192); tuned via tests/benchmarks/mask_parallel.
const defaultParallelMinBytes = 8 * 1024

// Handle masks texts using guardrails rules for the given project/model pair.
func (uc *UseCase) Handle(ctx context.Context, cmd Command) (CommandResponse, error) {
	if len(cmd.DataTypes) == 0 {
		return CommandResponse{}, nil
	}

	// Pin one registry snapshot: resolve the enabled rule IDs and their
	// compiled rules together so a concurrent reload cannot drop a rule
	// mid-request. The compiled set is resolved once and reused for every text.
	ruleIDs, rules := uc.registry.ResolveForDataTypes(dataTypesToUint32s(cmd.DataTypes))
	if len(ruleIDs) == 0 || len(rules) == 0 {
		return CommandResponse{}, nil
	}

	metrics.ObserveMaskTextsCount(len(cmd.Texts))

	// Phase A: scan every text. The scanner is stateless, so texts are scanned
	// concurrently when it pays off; results stay in text order. Time the whole
	// phase once so the aggregate metric reports true wall-clock scan latency
	// (under concurrency it is lower than the sum of per-text durations).
	scanStartedAt := time.Now()
	results := uc.scanTexts(cmd.Texts, rules)
	metrics.ObserveMaskScanDuration(time.Since(scanStartedAt))

	// Surface the first scan failure (lowest text index) and fail open before
	// recording per-text metrics for an aborted scan, preserving the sequential
	// error-wrapping contract.
	for i := range results {
		if results[i].err != nil {
			return CommandResponse{}, fmt.Errorf("scan text[%d]: %w", i, results[i].err)
		}
	}

	// Aggregate per-text scan metrics across all texts. Each per-text duration is
	// wall-clock for that goroutine, so under concurrency it includes scheduler
	// wait — it reflects per-text cost, not the request's scan latency (that is
	// the phase timer above).
	var totalTextBytes int
	for i := range results {
		metrics.ObserveMaskScanTextBytes(results[i].textBytes)
		metrics.ObserveScanDuration(results[i].duration)
		totalTextBytes += results[i].textBytes
	}
	metrics.ObserveMaskScanTotalBytes(totalTextBytes)

	// Phase B: mask sequentially in text order through one masker, so
	// placeholder numbering and cross-text dedup stay deterministic.
	m := newMasker()
	maskedTexts := make([]string, len(cmd.Texts))
	for i, text := range cmd.Texts {
		if len(results[i].matches) == 0 {
			maskedTexts[i] = text
			continue
		}
		maskedTexts[i] = m.maskText(text, results[i].matches)
	}

	triggeredRuleIDs := m.triggeredRules()
	triggeredDataTypes := m.triggeredDataTypes()
	replacements := m.replacements()

	if len(triggeredRuleIDs) == 0 && len(replacements) == 0 {
		return CommandResponse{}, nil
	}

	return CommandResponse{
		MaskedTexts: maskedTexts,
		MaskingState: models.MaskingState{
			TriggeredRuleIDs:   triggeredRuleIDs,
			TriggeredDataTypes: triggeredDataTypes,
			Replacements:       replacements,
		},
	}, nil
}

func dataTypesToUint32s(dataTypes []models.DataType) []uint32 {
	out := make([]uint32, len(dataTypes))
	for i, dataType := range dataTypes {
		out[i] = uint32(dataType)
	}
	return out
}

// scanResult holds one text's scan outcome, kept in a per-index slot so the
// masking phase can consume it deterministically in text order.
type scanResult struct {
	matches   []sensitive.Match
	err       error
	duration  time.Duration
	textBytes int
}

// scanTexts scans every text against the pinned rule set. The scanner is
// stateless, so texts are scanned concurrently when the combined size makes
// concurrency worthwhile (or when an explicit worker count is configured);
// otherwise they are scanned sequentially with zero goroutine overhead. Each
// goroutine writes only its own result slot, so there is no shared-write race,
// and order is preserved for the sequential masking phase that follows.
func (uc *UseCase) scanTexts(texts []string, rules []registry.CompiledRule) []scanResult {
	results := make([]scanResult, len(texts))
	for i := range texts {
		results[i].textBytes = len(texts[i])
	}

	workers := uc.scanWorkers(texts)
	if workers <= 1 {
		// Sequential (the small-body hot path): stop at the first error, since
		// Handle discards the whole result and fails open — no point scanning the
		// remaining texts. The parallel path below cannot cheaply cancel in-flight
		// goroutines, so it lets them finish (scan errors are rare either way).
		for i, text := range texts {
			results[i].matches, results[i].duration, results[i].err = scanOne(uc.scanner, text, rules)
			if results[i].err != nil {
				return results
			}
		}
		return results
	}

	sem := make(chan struct{}, workers)
	var wg sync.WaitGroup
	wg.Add(len(texts))
	for i, text := range texts {
		sem <- struct{}{}
		go func(idx int, t string) {
			defer wg.Done()
			defer func() { <-sem }()
			// Fail-open safety net: a panic in this goroutine (the scanner's own
			// fan-out workers guard themselves, but the sequential path and
			// conflict resolution run here) must not crash the replica. Convert
			// it into a per-text error so Handle discards the result and passes
			// the traffic through unchanged.
			defer func() {
				if r := recover(); r != nil {
					results[idx].err = fmt.Errorf("scan panic: %v", r)
				}
			}()
			results[idx].matches, results[idx].duration, results[idx].err = scanOne(uc.scanner, t, rules)
		}(i, text)
	}
	wg.Wait()
	return results
}

// scanWorkers decides the text-level scan concurrency for this request.
//
//   - a single text (or none) is always scanned inline;
//   - an explicit override (WithScanConcurrency) forces that degree, bypassing
//     the size threshold — used by tuning and benchmarks;
//   - otherwise (auto) parallelize by GOMAXPROCS only when the combined text
//     size clears parallelMaskTotalBytesThreshold.
func (uc *UseCase) scanWorkers(texts []string) int {
	if len(texts) < 2 {
		return 1
	}
	if uc.maxScanWorkers > 0 {
		return min(uc.maxScanWorkers, len(texts))
	}

	minBytes := uc.parallelMinBytes
	if minBytes <= 0 {
		minBytes = defaultParallelMinBytes
	}

	total := 0
	for _, t := range texts {
		total += len(t)
	}
	if total < minBytes {
		return 1
	}
	return min(runtime.GOMAXPROCS(0), len(texts))
}

// scanOne runs the scanner on a single text and times it. Split out so both the
// sequential and parallel paths record the same per-text scan duration.
func scanOne(scanner SensitiveScanner, text string, rules []registry.CompiledRule) ([]sensitive.Match, time.Duration, error) {
	startedAt := time.Now()
	matches, err := scanner.ScanRules(text, rules)
	return matches, time.Since(startedAt), err
}
