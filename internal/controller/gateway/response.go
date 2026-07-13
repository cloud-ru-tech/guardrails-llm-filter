package gateway

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"

	"github.com/tidwall/sjson"

	"github.com/cloud-ru-tech/guardrails-llm-filter/internal/guardrails/demask"
	"github.com/cloud-ru-tech/guardrails-llm-filter/internal/logging"
	"github.com/cloud-ru-tech/guardrails-llm-filter/internal/metrics"
	"github.com/cloud-ru-tech/guardrails-llm-filter/internal/models"
	"github.com/cloud-ru-tech/guardrails-llm-filter/internal/sseproc"
	"github.com/cloud-ru-tech/guardrails-llm-filter/internal/sseproc/common"
	"github.com/cloud-ru-tech/guardrails-llm-filter/pkg/llmutils"
	"github.com/cloud-ru-tech/guardrails-llm-filter/pkg/llmutils/chatcompletions"
	"github.com/cloud-ru-tech/guardrails-llm-filter/pkg/llmutils/messages"
	"github.com/cloud-ru-tech/guardrails-llm-filter/pkg/llmutils/responses"
)

// hopByHopHeaders are stripped in both directions per RFC 7230 §6.1 (plus the
// end-to-end headers listed in a Connection header, handled separately).
var hopByHopHeaders = []string{
	"Connection",
	"Proxy-Connection",
	"Keep-Alive",
	"Proxy-Authenticate",
	"Proxy-Authorization",
	"Te",
	"Trailer",
	"Transfer-Encoding",
	"Upgrade",
}

// forward builds and issues the outbound request, then relays or demasks the
// upstream response. When factory is nil the response is relayed verbatim.
func (h *Handler) forward(
	ctx context.Context,
	w http.ResponseWriter,
	r *http.Request,
	body []byte,
	factory *demask.Factory,
	format models.APIFormat,
	streamRequested bool,
	requestID string,
) {
	target := h.upstreamURL(r)
	if target == nil {
		logging.Error(ctx, "no upstream configured for request", nil, "path", r.URL.Path)
		http.Error(w, "upstream not configured", http.StatusBadGateway)
		return
	}

	// The request context is the client's: cancelling it (client disconnect)
	// cancels the in-flight upstream request and its body read.
	outReq, err := http.NewRequestWithContext(ctx, r.Method, target.String(), bytes.NewReader(body))
	if err != nil {
		logging.Error(ctx, "failed to build upstream request", err)
		http.Error(w, "failed to build upstream request", http.StatusBadGateway)
		return
	}
	copyHeaders(outReq.Header, r.Header)
	removeHopByHop(outReq.Header)
	// The override header is consumed by this service; never forward it.
	if h.cfg.Guardrails.OverrideHeader != "" {
		outReq.Header.Del(h.cfg.Guardrails.OverrideHeader)
	}
	outReq.ContentLength = int64(len(body))
	if factory != nil {
		// Force an uncompressed response so the body is demaskable.
		outReq.Header.Set("Accept-Encoding", "identity")
	}

	resp, err := h.client.Do(outReq)
	if err != nil {
		if ctx.Err() != nil {
			// Client disconnected; nothing to write.
			logging.Debug(ctx, "client disconnected before upstream response", "error", err)
			return
		}
		logging.Error(ctx, "upstream request failed", err, "url", target.String())
		http.Error(w, "upstream request failed", http.StatusBadGateway)
		return
	}
	defer func() { _ = resp.Body.Close() }()

	copyHeaders(w.Header(), resp.Header)
	removeHopByHop(w.Header())

	// No demasking: transparent relay of headers, status and body (with
	// flushing, so an SSE passthrough still streams).
	if factory == nil {
		w.WriteHeader(resp.StatusCode)
		relay(w, resp.Body)
		return
	}

	// Demasking will change the body length; drop the upstream Content-Length.
	w.Header().Del("Content-Length")

	// SSE is authoritative from the response Content-Type. As a fallback, honour
	// the client's streaming intent: if it asked to stream and the upstream did
	// not clearly return a JSON body, treat the response as SSE so a mislabeled
	// stream is still demasked token-by-token rather than buffered whole. A JSON
	// error body keeps the full-body path.
	ct := resp.Header.Get(contentTypeHeader)
	isSSE := strings.Contains(ct, sseContentType)
	if !isSSE && streamRequested && !strings.Contains(ct, "application/json") {
		isSSE = true
	}

	capture := h.audit != nil && requestID != ""

	if isSSE {
		w.WriteHeader(resp.StatusCode)
		texts := demaskSSE(ctx, w, resp.Body, factory, format, capture)
		h.recordMaskedResponse(requestID, texts)
		return
	}

	texts := demaskFull(ctx, w, resp, factory, format, capture)
	h.recordMaskedResponse(requestID, texts)
}

// recordMaskedResponse enriches the audit record with the masked model response
// texts. Best-effort and gated: a no-op when audit is off, the request was not
// masked, or nothing was captured.
func (h *Handler) recordMaskedResponse(requestID string, texts []string) {
	if h.audit == nil || requestID == "" || len(texts) == 0 {
		return
	}
	h.audit.RecordResponse(requestID, texts)
}

// demaskFull buffers the whole non-SSE upstream body, demasks it per format,
// and writes it. The body is handled uniformly regardless of status code (an
// error body simply matches no output fields).
// demaskFull returns the masked (pre-demask) text content of the response when
// capture is set (for the audit trail), else nil.
func demaskFull(ctx context.Context, w http.ResponseWriter, resp *http.Response, factory *demask.Factory, format models.APIFormat, capture bool) []string {
	full, err := io.ReadAll(resp.Body)
	if err != nil {
		if ctx.Err() != nil {
			return nil
		}
		logging.Error(ctx, "failed to read upstream response body", err)
		// Headers not yet written; surface a gateway error.
		http.Error(w, "failed to read upstream response", http.StatusBadGateway)
		return nil
	}

	var fields []llmutils.ContentField
	switch format {
	case models.APIFormatChatCompletions:
		fields = chatcompletions.ExtractOutputFields(full)
	case models.APIFormatResponses:
		fields = responses.ExtractOutputFields(full, "")
	case models.APIFormatMessages:
		fields = messages.ExtractResponseFields(full)
	default:
		logging.Warn(ctx, "unknown response format, passing body through unchanged", "format", string(format))
		metrics.IncUnknownFormatPassthrough()
	}

	maskedResponseTexts := collectMaskedResponseTexts(capture, fields)

	patched := demaskAndPatchFields(ctx, full, fields, factory)

	w.Header().Set(contentTypeHeader, resp.Header.Get(contentTypeHeader))
	w.Header().Set("Content-Length", strconv.Itoa(len(patched)))
	w.WriteHeader(resp.StatusCode)
	if _, err := w.Write(patched); err != nil {
		logging.Debug(ctx, "failed to write demasked response body", "error", err)
	}
	return maskedResponseTexts
}

// collectMaskedResponseTexts gathers the masked (placeholder-bearing) text
// content of the response for the audit trail. Structured tool-call payloads
// (paths ending in .arguments/.input) are excluded so the recorded value is the
// model's text output, matching the SSE processors' capture.
func collectMaskedResponseTexts(capture bool, fields []llmutils.ContentField) []string {
	if !capture {
		return nil
	}
	texts := make([]string, 0, len(fields))
	for _, f := range fields {
		if strings.HasSuffix(f.Path, ".arguments") || strings.HasSuffix(f.Path, ".input") {
			continue
		}
		if f.Value != "" {
			texts = append(texts, f.Value)
		}
	}
	return texts
}

// demaskAndPatchFields demasks each extracted field and patches it back in
// place with sjson, preserving every other byte verbatim. Fields whose path
// ends in ".input" (a raw JSON object) or ".arguments" (a JSON string holding
// an object) are demasked structurally so a restored quote/backslash stays
// valid JSON. Fail-open per field.
func demaskAndPatchFields(ctx context.Context, body []byte, fields []llmutils.ContentField, factory *demask.Factory) []byte {
	newDemasker := func() common.Demasker { return factory.Demasker() }
	patched := body
	for _, f := range fields {
		isRawObject := strings.HasSuffix(f.Path, ".input")
		var demasked string
		var ok bool
		if isRawObject || strings.HasSuffix(f.Path, ".arguments") {
			if len(f.Value) == 0 {
				continue
			}
			demasked, ok = common.DemaskJSONArguments(ctx, newDemasker, f.Value)
		} else {
			var derr error
			demasked, derr = factory.Demasker().DemaskChunk(ctx, f.Value, true)
			ok = derr == nil
		}
		if !ok {
			metrics.IncDemaskFullFailed()
			logging.Error(ctx, "failed to demask response field, keeping masked value", nil, "path", f.Path)
			continue
		}
		if demasked == f.Value {
			continue
		}
		var patchErr error
		if isRawObject {
			patched, patchErr = sjson.SetRawBytes(patched, f.Path, []byte(demasked))
		} else {
			patched, patchErr = sjson.SetBytes(patched, f.Path, demasked)
		}
		if patchErr != nil {
			logging.Error(ctx, "failed to patch response field", patchErr, "path", f.Path)
		}
	}
	return patched
}

// demaskSSE streams the upstream SSE response through the per-format processor,
// demasking frame-by-frame and flushing after every write so the client sees
// tokens as they arrive. Fail-open: a processor error forwards the raw chunk.
// demaskSSE returns the accumulated masked (pre-demask) response text when
// capture is set (for the audit trail), else nil.
func demaskSSE(ctx context.Context, w http.ResponseWriter, upstream io.Reader, factory *demask.Factory, format models.APIFormat, capture bool) []string {
	proc := sseproc.NewForFormat(format,
		func() common.Demasker { return factory.Demasker() },
		func() common.Demasker { return factory.JSONDemasker() },
		capture)

	fw := newFlushWriter(w)
	buf := make([]byte, 32*1024)
	for {
		n, readErr := upstream.Read(buf)
		eos := errors.Is(readErr, io.EOF)
		if n > 0 || eos {
			// Copy: the SSE processor may retain the slice across calls.
			chunk := append([]byte(nil), buf[:n]...)
			out, procErr := proc.ProcessChunk(ctx, chunk, eos)
			if procErr != nil {
				metrics.IncDemaskSSEFailed()
				logging.Warn(ctx, "SSE demask failed, forwarding chunk unchanged (fail-open)", "error", procErr)
				out = chunk
			}
			if len(out) > 0 {
				if _, werr := fw.Write(out); werr != nil {
					logging.Debug(ctx, "failed to write SSE chunk to client", "error", werr)
					return nil // stream interrupted; don't record partial text
				}
			}
		}
		if readErr != nil {
			if !eos && ctx.Err() == nil {
				logging.Warn(ctx, "error reading upstream SSE stream", "error", readErr)
				return nil // truncated stream; don't record partial text
			}
			// Clean end-of-stream (or client disconnect): return whatever the
			// processor accumulated (nil unless capture was requested).
			if src, ok := proc.(common.MaskedResponseTextSource); ok {
				return src.MaskedResponseText()
			}
			return nil
		}
	}
}

// relay copies the upstream body to the client, flushing after each read so a
// streamed passthrough (e.g. detect-mode SSE) is not buffered.
func relay(w http.ResponseWriter, upstream io.Reader) {
	fw := newFlushWriter(w)
	_, _ = io.Copy(fw, upstream)
}

// copyHeaders copies every header value from src to dst.
func copyHeaders(dst, src http.Header) {
	for k, vs := range src {
		for _, v := range vs {
			dst.Add(k, v)
		}
	}
}

// removeHopByHop deletes hop-by-hop headers, including any end-to-end header
// named in a Connection header (RFC 7230 §6.1), from h.
func removeHopByHop(h http.Header) {
	for _, name := range h.Values("Connection") {
		for token := range strings.SplitSeq(name, ",") {
			if token = strings.TrimSpace(token); token != "" {
				h.Del(token)
			}
		}
	}
	for _, hh := range hopByHopHeaders {
		h.Del(hh)
	}
}
