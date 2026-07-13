package gateway

import (
	"context"
	"errors"
	"io"
	"net/http"

	"github.com/google/uuid"
	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"

	"github.com/cloud-ru-tech/guardrails-llm-filter/internal/logging"
	"github.com/cloud-ru-tech/guardrails-llm-filter/internal/metrics"
	"github.com/cloud-ru-tech/guardrails-llm-filter/internal/models"
	"github.com/cloud-ru-tech/guardrails-llm-filter/internal/usecases/guardrails/mask"
	"github.com/cloud-ru-tech/guardrails-llm-filter/pkg/llmutils"
	"github.com/cloud-ru-tech/guardrails-llm-filter/pkg/llmutils/chatcompletions"
	"github.com/cloud-ru-tech/guardrails-llm-filter/pkg/llmutils/messages"
	"github.com/cloud-ru-tech/guardrails-llm-filter/pkg/llmutils/responses"
)

// readBody reads the full client request body. When maxBytes > 0 the body is
// capped via http.MaxBytesReader and an over-limit request is rejected with 413
// (protecting the data-plane from a memory-exhausting body). On any other read
// error it responds 400 and returns ok=false; the caller must stop. An empty
// body reads as an empty slice with ok=true (forwarded verbatim, nothing to mask).
func readBody(ctx context.Context, w http.ResponseWriter, r *http.Request, maxBytes int64) ([]byte, bool) {
	if maxBytes > 0 {
		r.Body = http.MaxBytesReader(w, r.Body, maxBytes)
	}
	body, err := io.ReadAll(r.Body)
	if err != nil {
		var maxErr *http.MaxBytesError
		if errors.As(err, &maxErr) {
			logging.Debug(ctx, "request body exceeds limit", "limit", maxBytes)
			http.Error(w, "request body too large", http.StatusRequestEntityTooLarge)
			return nil, false
		}
		// The client failed to deliver its own body — nothing to forward.
		logging.Debug(ctx, "failed to read request body", "error", err)
		http.Error(w, "failed to read request body", http.StatusBadRequest)
		return nil, false
	}
	return body, true
}

// maskRequest scans and masks the request body. It returns the (possibly)
// patched body, the resulting masking state, the request ID that keys the
// audit record (non-empty only when demaskResp is true), and demaskResp=true
// only when the response must be demasked — i.e. enforce mode produced at least
// one replacement. Every other outcome (parse failure, mask error, no findings,
// detect mode) returns demaskResp=false so the request is forwarded verbatim
// and its response relayed unchanged (fail-open, mask/pass — never block).
func (h *Handler) maskRequest(
	ctx context.Context,
	r *http.Request,
	body []byte,
	format models.APIFormat,
	eff models.EffectiveSettings,
	streamRequested bool,
) ([]byte, models.MaskingState, string, bool) {
	var empty models.MaskingState

	// Extract scannable request payload fields per API format.
	var fields []llmutils.ContentField
	var err error
	switch format {
	case models.APIFormatResponses:
		fields, err = responses.ExtractRequestContent(body)
	case models.APIFormatMessages:
		fields, err = messages.ExtractRequestContent(body)
	default:
		fields, err = chatcompletions.ExtractRequestContent(body)
	}
	if err != nil {
		// Guarded path but the resolved format could not parse this body; a
		// benign empty body is handled by the caller, so this usually signals a
		// GUARDRAILS_PATHS misconfig. Fail open.
		metrics.IncUnsupportedBodySchema()
		logging.Debug(ctx, "unsupported request body schema, forwarding unchanged", "format", string(format))
		return nil, empty, "", false
	}
	if len(fields) == 0 {
		return nil, empty, "", false
	}

	texts := make([]string, len(fields))
	for i, f := range fields {
		texts[i] = f.Value
	}

	result, err := h.masker.Handle(ctx, mask.Command{
		DataTypes: eff.DataTypes,
		Texts:     texts,
	})
	if err != nil {
		metrics.IncMaskFailed()
		logging.Error(ctx, "mask use case error, forwarding unchanged", err)
		return nil, empty, "", false
	}

	metrics.ObserveTriggeredRules(len(result.MaskingState.TriggeredRuleIDs))
	for _, ruleID := range result.MaskingState.TriggeredRuleIDs {
		metrics.IncRuleTrigger(ruleID)
	}
	for _, dataType := range result.MaskingState.TriggeredDataTypes {
		metrics.IncDataTypeTrigger(dataType.String())
	}

	if len(result.MaskingState.Replacements) == 0 {
		return nil, empty, "", false
	}

	md := h.requestMetadata(r, body, format, eff.Mode, streamRequested)

	// Detect (shadow) mode: record what would have been masked, forward the
	// request unchanged, and — with no masking state returned — leave the
	// response relayed verbatim.
	if eff.Mode == models.ModeDetect {
		metrics.IncRequestMasked(string(models.ModeDetect))
		if h.audit != nil {
			h.audit.Record(md, result.MaskingState, result.MaskedTexts)
		}
		return nil, empty, "", false
	}

	// Record the resolved wire format so the demasker/SSE processor picks the
	// right dialect for the response.
	result.MaskingState.Format = format

	// Patch each extracted field back in place. Every field is a decoded string
	// value, so setting it on its own path cannot break the JSON.
	patched := body
	for i, f := range fields {
		if result.MaskedTexts[i] == f.Value {
			continue
		}
		var patchErr error
		patched, patchErr = sjson.SetBytes(patched, f.Path, result.MaskedTexts[i])
		if patchErr != nil {
			logging.Warn(ctx, "failed to patch field", "path", f.Path, "error", patchErr)
		}
	}

	metrics.IncRequestMasked(string(models.ModeEnforce))
	if h.audit != nil {
		h.audit.Record(md, result.MaskingState, result.MaskedTexts)
	}

	return patched, result.MaskingState, md.RequestID, true
}

// requestMetadata builds the per-request metadata used for the audit record
// and log correlation. The request ID comes from the client's X-Request-Id
// header, falling back to a fresh UUID.
func (h *Handler) requestMetadata(r *http.Request, body []byte, format models.APIFormat, mode models.GuardrailsMode, streamRequested bool) models.Metadata {
	requestID := r.Header.Get(requestIDHeader)
	if requestID == "" {
		requestID = uuid.NewString()
	}
	// The model name lives in the request body (top-level "model" field) for
	// every supported format. Fall back to the X-Gateway-Model-Name header for
	// deployments that inject it upstream (e.g. behind Envoy).
	model := gjson.GetBytes(body, "model").String()
	if model == "" {
		model = r.Header.Get("X-Gateway-Model-Name")
	}
	return models.Metadata{
		RequestID:   requestID,
		Model:       model,
		Path:        r.URL.Path,
		Format:      format,
		Mode:        mode,
		IsStreaming: streamRequested,
	}
}
