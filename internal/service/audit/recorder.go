// Package audit records per-request masking audit entries.
//
// The recorder is telemetry-grade and strictly off the data path: records
// are written asynchronously with a bounded number of in-flight writes;
// saturation drops the record (metered) instead of blocking, and store
// errors are log-only (fail-open). Original sensitive values are stored only
// when the operator opts in via GUARDRAILS_AUDIT_STORE_ORIGINAL_TEXTS
// (default off), optionally encrypted with the store AES-256-GCM key.
package audit

import (
	"context"
	"errors"
	"time"
	"unicode/utf8"

	"github.com/cloud-ru-tech/guardrails-llm-filter/internal/config"
	"github.com/cloud-ru-tech/guardrails-llm-filter/internal/logging"
	"github.com/cloud-ru-tech/guardrails-llm-filter/internal/metrics"
	"github.com/cloud-ru-tech/guardrails-llm-filter/internal/models"
	"github.com/cloud-ru-tech/guardrails-llm-filter/internal/repository"
	"github.com/cloud-ru-tech/guardrails-llm-filter/internal/repository/statecodec"
	"github.com/cloud-ru-tech/guardrails-llm-filter/pkg/guardrails/regex/rule"
)

const (
	// maxInFlightWrites bounds concurrent async store writes.
	maxInFlightWrites = 128
	// writeTimeout bounds one detached store write.
	writeTimeout = 5 * time.Second
	// maxMaskedTextBytes caps one stored masked text (prompts can be huge).
	maxMaskedTextBytes = 64 * 1024
)

// RuleResolver resolves rules for per-replacement data-type enrichment.
// *registry.Reloadable satisfies it.
type RuleResolver interface {
	GetRulesByIDs(ruleIDs ...string) []rule.Rule
}

// RecordStore is the write slice of repository.AuditStore the recorder needs.
type RecordStore interface {
	PutAuditRecord(ctx context.Context, rec models.AuditRecord) error
	SetAuditResponseTexts(ctx context.Context, requestID string, texts []string) error
}

// Recorder builds and asynchronously persists audit records.
type Recorder struct {
	store                    RecordStore
	rules                    RuleResolver
	storeMaskedTexts         bool
	storeMaskedResponseTexts bool
	// originalsMode is one of config.Originals{Off,Plain,Encrypted}. When not
	// Off, each replacement carries its original value; Encrypted seals it with
	// codec first.
	originalsMode string
	codec         statecodec.Codec
	sem           chan struct{}
}

// New creates a Recorder.
//   - storeMaskedTexts / storeMaskedResponseTexts persist the placeholder-
//     substituted request / response texts.
//   - originalsMode (config.Originals{Off,Plain,Encrypted}) controls whether
//     each replacement carries its original value; codec performs the sealing
//     for Encrypted mode (the store's statecodec.Codec).
func New(st RecordStore, rules RuleResolver, storeMaskedTexts, storeMaskedResponseTexts bool, originalsMode string, codec statecodec.Codec) *Recorder {
	return &Recorder{
		store:                    st,
		rules:                    rules,
		storeMaskedTexts:         storeMaskedTexts,
		storeMaskedResponseTexts: storeMaskedResponseTexts,
		originalsMode:            originalsMode,
		codec:                    codec,
		sem:                      make(chan struct{}, maxInFlightWrites),
	}
}

// Record persists one audit entry for a masked request. The record is built
// synchronously (cheap: slice copies + a registry lookup) so the write
// goroutine never touches per-stream state after the stream is cleared; the
// store write happens in the background. Never blocks and never returns an
// error — audit must not affect the data path.
func (r *Recorder) Record(md models.Metadata, st models.MaskingState, maskedTexts []string) {
	rec := r.buildRecord(md, st, maskedTexts)

	select {
	case r.sem <- struct{}{}:
	default:
		metrics.IncAuditDropped()
		return
	}
	go func() {
		defer func() { <-r.sem }()
		// Detached context: the stream context may already be canceled.
		ctx, cancel := context.WithTimeout(context.Background(), writeTimeout)
		defer cancel()
		if err := r.store.PutAuditRecord(ctx, rec); err != nil {
			metrics.IncAuditStoreFailure("put")
			logging.Warn(ctx, "Failed to persist audit record",
				"request_id", rec.RequestID, "error", err)
		}
	}()
}

// RecordResponse enriches an already-written audit record with the masked
// (placeholder-substituted) model response texts, keyed by request ID. It is
// best-effort: gated by storeMaskedResponseTexts, async/bounded exactly like
// Record, and a no-op when the record is absent (ErrNotFound) — e.g. the
// request-phase write has not landed yet (cross-replica) or the record
// expired. Never blocks and never returns an error.
func (r *Recorder) RecordResponse(requestID string, maskedResponseTexts []string) {
	if !r.storeMaskedResponseTexts || requestID == "" {
		return
	}
	texts := make([]string, len(maskedResponseTexts))
	for i, txt := range maskedResponseTexts {
		texts[i] = truncateUTF8(txt, maxMaskedTextBytes)
	}

	select {
	case r.sem <- struct{}{}:
	default:
		metrics.IncAuditDropped()
		return
	}
	go func() {
		defer func() { <-r.sem }()
		ctx, cancel := context.WithTimeout(context.Background(), writeTimeout)
		defer cancel()
		switch err := r.store.SetAuditResponseTexts(ctx, requestID, texts); {
		case err == nil, errors.Is(err, repository.ErrNotFound):
			// nil: done. ErrNotFound: best-effort no-op.
		default:
			metrics.IncAuditStoreFailure("set_response")
			logging.Warn(ctx, "Failed to persist audit response texts",
				"request_id", requestID, "error", err)
		}
	}()
}

// Drain waits for all in-flight writes to finish by acquiring every semaphore
// slot, then releases them so the recorder is left ready to accept new Record
// calls. It returns when the recorder is quiescent or ctx is done (whichever
// comes first), so a stuck store write cannot block shutdown past its budget.
// Call it during graceful shutdown before closing the store.
func (r *Recorder) Drain(ctx context.Context) {
	acquired := 0
	for range cap(r.sem) {
		select {
		case r.sem <- struct{}{}:
			acquired++
		case <-ctx.Done():
			// Release whatever we took so a canceled Drain doesn't leave the
			// recorder permanently unable to accept writes.
			for range acquired {
				<-r.sem
			}
			return
		}
	}
	// All in-flight writes finished; release the slots we acquired so the
	// semaphore is empty again and Record continues to work after Drain.
	for range acquired {
		<-r.sem
	}
}

func (r *Recorder) buildRecord(md models.Metadata, st models.MaskingState, maskedTexts []string) models.AuditRecord {
	dataTypeByRule := make(map[string]models.DataType, len(st.TriggeredRuleIDs))
	for _, ru := range r.rules.GetRulesByIDs(st.TriggeredRuleIDs...) {
		dataTypeByRule[ru.ID] = models.DataType(ru.DataType)
	}

	storeOriginals := r.originalsMode != "" && r.originalsMode != config.OriginalsOff
	replacements := make([]models.AuditReplacement, len(st.Replacements))
	for i, rep := range st.Replacements {
		replacements[i] = models.AuditReplacement{
			RuleID: rep.RuleID,
			// UNSPECIFIED when the rule was deleted between masking and now.
			DataType:    dataTypeByRule[rep.RuleID],
			Placeholder: rep.Placeholder,
		}
		if storeOriginals {
			replacements[i].Original = r.originalForAudit(rep.Original)
		}
	}

	rec := models.AuditRecord{
		RequestID:          md.RequestID,
		Timestamp:          time.Now().UTC(),
		Model:              md.Model,
		Path:               md.Path,
		TriggeredRuleIDs:   append([]string(nil), st.TriggeredRuleIDs...),
		TriggeredDataTypes: append([]models.DataType(nil), st.TriggeredDataTypes...),
		Replacements:       replacements,
		Mode:               string(md.Mode),
	}
	if r.storeMaskedTexts {
		rec.MaskedTexts = make([]string, len(maskedTexts))
		for i, txt := range maskedTexts {
			rec.MaskedTexts[i] = truncateUTF8(txt, maxMaskedTextBytes)
		}
	}
	return rec
}

// originalForAudit prepares a replacement's original value for storage:
// truncated to the shared cap, then (in encrypted mode) sealed with the store
// codec. An encryption failure drops the value (returns "") rather than risk
// persisting a raw original — the placeholder still identifies the match.
func (r *Recorder) originalForAudit(original string) string {
	original = truncateUTF8(original, maxMaskedTextBytes)
	if r.originalsMode != config.OriginalsEncrypted {
		return original
	}
	sealed, err := r.codec.EncryptString(original)
	if err != nil {
		logging.Warn(context.Background(), "Failed to encrypt audit original; dropping it", "error", err)
		return ""
	}
	return sealed
}

// truncateUTF8 cuts s to at most maxBytes without splitting a rune.
func truncateUTF8(s string, maxBytes int) string {
	if len(s) <= maxBytes {
		return s
	}
	cut := maxBytes
	for cut > 0 && !utf8.RuneStart(s[cut]) {
		cut--
	}
	return s[:cut]
}
