// Package responses implements SSE stream processing for OpenAI Responses
// API (/v1/responses) response demasking.
//
// The wire format uses named events like the Anthropic Messages stream
// ("event: <name>" + "data: <json>" pairs separated by "\n\n"). Token text
// arrives as response.output_text.delta events; tool-call arguments as
// response.function_call_arguments.delta. Crucially, several events embed
// the FULL accumulated text again — response.output_text.done (text),
// response.output_item.done (item), response.completed/incomplete/failed
// (the whole response object) — and each of those must be demasked with a
// fresh one-shot demasker or placeholders would leak in the final snapshot
// even though every delta was already demasked.
//
// The design mirrors internal/sseproc/messages: ProcessChunk consumes
// arbitrary Envoy body chunks, splits them into complete frames (carrying
// an incomplete tail into the next chunk), and routes each frame by the
// payload's "type" field. Frames we don't recognize pass through unchanged.
package responses

import (
	"context"
	"strconv"
	"strings"

	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"

	"github.com/cloud-ru-tech/guardrails-llm-filter/internal/logging"
	"github.com/cloud-ru-tech/guardrails-llm-filter/internal/metrics"
	"github.com/cloud-ru-tech/guardrails-llm-filter/internal/sseproc/common"
	"github.com/cloud-ru-tech/guardrails-llm-filter/pkg/llmutils"
	llmresp "github.com/cloud-ru-tech/guardrails-llm-filter/pkg/llmutils/responses"
)

// Processor processes one Responses API SSE stream end-to-end.
// Create via New; feed Envoy body chunks via ProcessChunk.
type Processor struct {
	newDemasker     common.DemaskerFactoryFn
	newJSONDemasker common.DemaskerFactoryFn // for function-call argument fields; falls back to newDemasker when unset

	tailBuf   []byte                          // bytes of an incomplete SSE frame carried between chunks
	outputBuf []byte                          // accumulated output for the current ProcessChunk call
	demaskers map[demaskerKey]common.Demasker // streaming demaskers, one per (output, content, field)
	accums    map[demaskerKey]*fieldAccum     // masked-text carry for fallbacks + args JSON depth
	itemIDs   map[demaskerKey]string          // last item_id seen for each key, for synthetic frames
	outSeq        int64                           // last sequence_number emitted; owns the outgoing sequence space
	completed     bool                            // true once a terminal response.* event was seen
	captureMasked bool                            // record pre-demask text for the audit trail
	masked        common.MaskedTextRecorder       // pre-demask text content for the audit trail
}

// MaskedResponseText returns the accumulated masked (pre-demask) output/reasoning
// text of the streamed response for the audit trail (function-call arguments
// excluded). Accumulated from the streaming deltas; the done-event full-text
// re-sends are intentionally not double-counted.
func (p *Processor) MaskedResponseText() []string { return p.masked.Texts() }

// Option configures a Processor.
type Option func(*Processor)

// WithJSONDemaskerFactory supplies the factory used for
// response.function_call_arguments.delta fields. Its demaskers must JSON-escape
// restored originals: those fragments are JSON *fragments* the client
// accumulates into the tool-call arguments, so inserting an original containing
// a quote or backslash verbatim would corrupt the JSON — unrecoverably, since a
// streaming client that builds arguments from the deltas never re-reads the
// authoritative response.function_call_arguments.done value. Mirrors the
// chatcompletions and messages dialects.
func WithJSONDemaskerFactory(factory common.DemaskerFactoryFn) Option {
	return func(p *Processor) {
		p.newJSONDemasker = factory
	}
}

// WithMaskedResponseCapture makes the processor accumulate the pre-demask
// output/reasoning text for the audit trail (via MaskedResponseText at EOS).
func WithMaskedResponseCapture() Option {
	return func(p *Processor) {
		p.captureMasked = true
	}
}

// New creates a Processor for a single Responses API SSE stream.
func New(factory common.DemaskerFactoryFn, opts ...Option) *Processor {
	p := &Processor{
		newDemasker: factory,
		demaskers:   make(map[demaskerKey]common.Demasker),
		accums:      make(map[demaskerKey]*fieldAccum),
		itemIDs:     make(map[demaskerKey]string),
		// -1 means "no frame emitted yet": the real stream opens with
		// response.created carrying sequence_number 0, which must pass through
		// verbatim (nextOutSeq preserves upstream numbering until the first
		// synthetic insertion). Starting at 0 would collide with that first
		// frame and shift the whole stream forward by one.
		outSeq: -1,
	}
	for _, opt := range opts {
		opt(p)
	}
	return p
}

// ProcessChunk consumes one Envoy body chunk and returns bytes ready for the
// client, or nil when all demaskers are still buffering and no completed
// frames are ready (the caller must not send a body mutation).
func (p *Processor) ProcessChunk(ctx context.Context, body []byte, endOfStream bool) ([]byte, error) {
	frames := p.prepareFrames(ctx, body, endOfStream)

	for _, frame := range frames {
		p.processFrame(ctx, frame)
	}

	// Stream ended without a terminal response.* event (upstream died or
	// Envoy half-closed): flush every open demasker so buffered text isn't
	// lost.
	if endOfStream && !p.completed {
		p.flushAll(ctx)
	}

	return p.takeOutput(), nil
}

func (p *Processor) prepareFrames(ctx context.Context, body []byte, endOfStream bool) [][]byte {
	workBuf := p.prependTail(body)
	frames, tail := common.SplitFrames(workBuf)

	if !endOfStream {
		p.tailBuf = tail
		return frames
	}

	if len(tail) > 0 {
		logging.Warn(ctx, "Responses SSE stream ended with incomplete frame", "tailLen", len(tail))
		frames = append(frames, tail)
	}
	return frames
}

func (p *Processor) processFrame(ctx context.Context, frame []byte) {
	pf := common.ClassifyFrame(frame)

	switch pf.Kind {
	case common.FrameDone:
		// The Responses API does not send [DONE]; handle it defensively the
		// same way the other dialects do.
		p.flushAll(ctx)
		p.completed = true
		p.writeOutput(pf.Original)
	case common.FramePassthrough:
		p.writeOutput(pf.Original)
	case common.FrameEvent:
		p.handleEventFrame(ctx, pf)
	}
}

// handleEventFrame dispatches on the payload's "type" field (never trust
// the event: line alone). Unknown events pass through for forward compat:
// refusal/reasoning-summary/annotation deltas carry model-generated text
// where placeholders cannot appear mid-stream, and new event types must not
// be swallowed.
func (p *Processor) handleEventFrame(ctx context.Context, pf common.ParsedFrame) {
	switch gjson.GetBytes(pf.Data, "type").String() {
	case "response.output_text.delta":
		p.handleTextDelta(ctx, pf)
	case "response.output_text.done":
		p.handleTextDone(ctx, pf)
	case "response.content_part.done":
		p.handleContentPartDone(ctx, pf)
	case "response.reasoning_text.delta":
		// Reasoning models echo prompt text into their chain-of-thought; demask
		// it like output_text so placeholders don't leak in the reasoning trace.
		p.handleDelta(ctx, pf, keyFromPayload(pf.Data, fieldReasoningText))
	case "response.reasoning_text.done":
		p.handleReasoningTextDone(ctx, pf)
	case "response.reasoning_part.done":
		// Same shape as content_part.done (part.text snapshot).
		p.handleContentPartDone(ctx, pf)
	case "response.function_call_arguments.delta":
		p.handleArgsDelta(ctx, pf)
	case "response.function_call_arguments.done":
		p.handleArgsDone(ctx, pf)
	case "response.output_item.done":
		p.handleItemDone(ctx, pf)
	case "response.completed", "response.incomplete", "response.failed":
		p.handleTerminal(ctx, pf)
	default:
		p.forwardEventFrame(pf)
	}
}

// nextOutSeq claims the sequence_number the next emitted frame must carry so
// the outgoing stream stays strictly monotonic even after synthetic frames
// are inserted. Upstream numbering is preserved verbatim until the first
// insertion; after it, every subsequent frame shifts forward past the
// inserted ones (the client cares about monotonicity, not about matching the
// upstream numbers we already diverged from).
func (p *Processor) nextOutSeq(upstream int64) int64 {
	if upstream > p.outSeq {
		p.outSeq = upstream
	} else {
		p.outSeq++
	}
	return p.outSeq
}

// forwardEventFrame forwards an event frame we did not otherwise modify,
// renumbering its sequence_number only when an earlier synthetic frame made
// the upstream number collide. While no renumbering is needed the original
// bytes are forwarded verbatim.
func (p *Processor) forwardEventFrame(pf common.ParsedFrame) {
	sn := gjson.GetBytes(pf.Data, "sequence_number")
	if !sn.Exists() {
		p.writeOutput(pf.Original)
		return
	}
	out := p.nextOutSeq(sn.Int())
	if out == sn.Int() {
		p.writeOutput(pf.Original)
		return
	}
	patched, err := sjson.SetBytes(pf.Data, "sequence_number", out)
	if err != nil {
		// Fail-open: better a stale number than a dropped frame.
		p.writeOutput(pf.Original)
		return
	}
	p.writeOutput(common.BuildEventFrame(pf.Event, patched))
}

// emitEventData writes a rebuilt event frame, claiming (and if needed
// rewriting) its sequence_number in the outgoing sequence space.
func (p *Processor) emitEventData(pf common.ParsedFrame, data []byte) {
	if sn := gjson.GetBytes(data, "sequence_number"); sn.Exists() {
		if out := p.nextOutSeq(sn.Int()); out != sn.Int() {
			if renum, err := sjson.SetBytes(data, "sequence_number", out); err == nil {
				data = renum
			}
		}
	}
	p.writeOutput(common.BuildEventFrame(pf.Event, data))
}

func keyFromPayload(data []byte, field fieldType) demaskerKey {
	key := demaskerKey{
		outputIndex: int(gjson.GetBytes(data, "output_index").Int()),
		field:       field,
	}
	if field == fieldOutputText || field == fieldReasoningText {
		key.contentIndex = int(gjson.GetBytes(data, "content_index").Int())
	}
	return key
}

// handleTextDelta demasks one output_text token fragment via the streaming
// demasker for its (output, content) part.
func (p *Processor) handleTextDelta(ctx context.Context, pf common.ParsedFrame) {
	p.handleDelta(ctx, pf, keyFromPayload(pf.Data, fieldOutputText))
}

// handleArgsDelta demasks one function_call arguments fragment; the demasker
// is force-flushed when the argument JSON object closes.
func (p *Processor) handleArgsDelta(ctx context.Context, pf common.ParsedFrame) {
	p.handleDelta(ctx, pf, keyFromPayload(pf.Data, fieldFunctionArgs))
}

func (p *Processor) handleDelta(ctx context.Context, pf common.ParsedFrame, key demaskerKey) {
	// Remember the item_id of this key's real frames so a synthetic delta
	// emitted on flush carries the same id the client already saw.
	if id := gjson.GetBytes(pf.Data, "item_id").String(); id != "" {
		p.itemIDs[key] = id
	}

	fragment := gjson.GetBytes(pf.Data, "delta").String()
	if fragment == "" {
		p.forwardEventFrame(pf)
		return
	}

	// Record masked text/reasoning deltas for the audit trail. Only the
	// streaming deltas are tapped; the *.done events re-send the full text and
	// would double-count. Function-call arguments (JSON) are excluded.
	if p.captureMasked && key.field != fieldFunctionArgs {
		p.masked.Add(strconv.Itoa(key.outputIndex)+"/"+strconv.Itoa(key.contentIndex)+"/"+string(key.field), fragment)
	}

	accum := p.getAccum(key)
	closed := accum.AppendAndCheckClose(key.field, fragment)
	flush := key.field == fieldFunctionArgs && closed

	demasked, err := p.getDemasker(key).DemaskChunk(ctx, fragment, flush)
	if err != nil {
		// Fallback: the demasker hands back its un-emitted content (with any
		// unresolved placeholders intact) so nothing is lost.
		logging.Error(ctx, "Responses SSE demasker failed, using fallback", err,
			"outputIndex", key.outputIndex, "field", string(key.field))
		metrics.IncDemaskSSEFailed()
		if demasked != "" {
			p.emitDeltaFrame(ctx, pf, demasked)
		}
		return
	}

	if demasked == "" {
		// Buffering (possible placeholder split across deltas). Drop the
		// frame; the text re-emerges on a later delta or a flush.
		return
	}

	p.emitDeltaFrame(ctx, pf, demasked)
}

// handleTextDone flushes the part's streaming demasker (any buffered text is
// emitted as a synthetic delta BEFORE the done frame, preserving the
// delta-before-done invariant), then rewrites the event's full "text" with a
// fresh one-shot demasker — the done event repeats all accumulated text and
// must not leak placeholders.
func (p *Processor) handleTextDone(ctx context.Context, pf common.ParsedFrame) {
	p.handleFieldTextDone(ctx, pf, fieldOutputText)
}

// handleReasoningTextDone mirrors handleTextDone for reasoning_text.done: the
// event repeats the full reasoning text, which must be demasked or placeholders
// leak in the client's reasoning trace.
func (p *Processor) handleReasoningTextDone(ctx context.Context, pf common.ParsedFrame) {
	p.handleFieldTextDone(ctx, pf, fieldReasoningText)
}

// handleFieldTextDone flushes the field's streaming demasker (buffered text out
// as a synthetic delta first) then rewrites the event's full "text" with a
// fresh one-shot demasker.
func (p *Processor) handleFieldTextDone(ctx context.Context, pf common.ParsedFrame, field fieldType) {
	key := keyFromPayload(pf.Data, field)
	p.flushKey(ctx, key)

	text := gjson.GetBytes(pf.Data, "text").String()
	if text == "" {
		p.forwardEventFrame(pf)
		return
	}
	demasked, err := p.newDemasker().DemaskChunk(ctx, text, true)
	if err != nil {
		metrics.IncDemaskSSEFailed()
		p.forwardEventFrame(pf)
		return
	}
	p.emitPatchedFrame(ctx, pf, "text", demasked)
}

// handleContentPartDone demasks the full text repeated in a
// response.content_part.done snapshot. The streaming output_text deltas were
// already demasked, but this event re-sends the entire accumulated part text
// (part.text) — without a fresh one-shot demask its placeholders leak to the
// client. Mirrors handleTextDone; the emitted part carries the demasked text.
func (p *Processor) handleContentPartDone(ctx context.Context, pf common.ParsedFrame) {
	text := gjson.GetBytes(pf.Data, "part.text")
	if !text.Exists() || text.String() == "" {
		p.forwardEventFrame(pf)
		return
	}
	demasked, err := p.newDemasker().DemaskChunk(ctx, text.String(), true)
	if err != nil {
		metrics.IncDemaskSSEFailed()
		p.forwardEventFrame(pf)
		return
	}
	p.emitPatchedFrame(ctx, pf, "part.text", demasked)
}

// handleArgsDone mirrors handleTextDone for function_call arguments: the
// arguments are embedded JSON, so the shared structural demask keeps them valid
// and a failed demask keeps the masked value.
func (p *Processor) handleArgsDone(ctx context.Context, pf common.ParsedFrame) {
	key := keyFromPayload(pf.Data, fieldFunctionArgs)
	p.flushKey(ctx, key)

	args := gjson.GetBytes(pf.Data, "arguments").String()
	if args == "" {
		p.forwardEventFrame(pf)
		return
	}
	// Shared with the full-body path: naive demask, then structural fallback so
	// a restored original containing a quote/backslash stays valid JSON instead
	// of leaking a placeholder to the client.
	demasked, ok := common.DemaskJSONArguments(ctx, p.newDemasker, args)
	if !ok {
		metrics.IncDemaskSSEFailed()
		p.forwardEventFrame(pf)
		return
	}
	p.emitPatchedFrame(ctx, pf, "arguments", demasked)
}

// handleItemDone demasks the full output item embedded in the event.
func (p *Processor) handleItemDone(ctx context.Context, pf common.ParsedFrame) {
	item := gjson.GetBytes(pf.Data, "item")
	if !item.Exists() {
		p.forwardEventFrame(pf)
		return
	}
	patched := p.patchEmbeddedFields(ctx, pf.Data, llmresp.ExtractItemFields(item, "item"))
	p.emitEventData(pf, patched)
}

// handleTerminal flushes every open streaming demasker (synthetic deltas go
// out first) and demasks the full response object embedded in the terminal
// event — it repeats every output text and must not leak placeholders.
func (p *Processor) handleTerminal(ctx context.Context, pf common.ParsedFrame) {
	p.flushAll(ctx)
	p.completed = true

	patched := p.patchEmbeddedFields(ctx, pf.Data, llmresp.ExtractOutputFields(pf.Data, "response"))
	p.emitEventData(pf, patched)
}

// patchEmbeddedFields demasks extracted fields of an embedded object with
// fresh one-shot demaskers and patches them back into the payload.
// ".arguments" fields go through the shared structural demask (masked fallback).
func (p *Processor) patchEmbeddedFields(ctx context.Context, data []byte, fields []llmutils.ContentField) []byte {
	patched := data
	for _, f := range fields {
		var demasked string
		if strings.HasSuffix(f.Path, ".arguments") {
			// arguments is embedded JSON — structural fallback keeps it valid
			// (shared with the full-body path); masked fallback on failure.
			d, ok := common.DemaskJSONArguments(ctx, p.newDemasker, f.Value)
			if !ok {
				metrics.IncDemaskSSEFailed()
				logging.Error(ctx, "Responses SSE embedded arguments demask failed, keeping masked value", nil,
					"path", f.Path)
				continue
			}
			demasked = d
		} else {
			d, err := p.newDemasker().DemaskChunk(ctx, f.Value, true)
			if err != nil {
				metrics.IncDemaskSSEFailed()
				logging.Error(ctx, "Responses SSE embedded-object demask failed, keeping masked value", err,
					"path", f.Path)
				continue
			}
			demasked = d
		}
		if demasked == f.Value {
			continue
		}
		var patchErr error
		patched, patchErr = sjson.SetBytes(patched, f.Path, demasked)
		if patchErr != nil {
			logging.Error(ctx, "Failed to patch Responses SSE embedded field", patchErr, "path", f.Path)
		}
	}
	return patched
}

// emitDeltaFrame rebuilds the delta frame with the demasked value swapped
// into the "delta" field.
func (p *Processor) emitDeltaFrame(ctx context.Context, pf common.ParsedFrame, value string) {
	p.emitPatchedFrame(ctx, pf, "delta", value)
}

func (p *Processor) emitPatchedFrame(ctx context.Context, pf common.ParsedFrame, valuePath, value string) {
	patched, err := sjson.SetBytes(pf.Data, valuePath, value)
	if err != nil {
		logging.Error(ctx, "Failed to patch Responses SSE frame", err, "path", valuePath)
		p.forwardEventFrame(pf)
		return
	}
	p.emitEventData(pf, patched)
}

// emitSyntheticDeltaFrame builds a delta frame from scratch for text that
// was buffered by a streaming demasker and only surfaced during a flush.
func (p *Processor) emitSyntheticDeltaFrame(ctx context.Context, key demaskerKey, value string) {
	eventType := "response.output_text.delta"
	switch key.field {
	case fieldFunctionArgs:
		eventType = "response.function_call_arguments.delta"
	case fieldReasoningText:
		eventType = "response.reasoning_text.delta"
	}

	// The real Responses API always sends item_id and sequence_number on delta
	// events; strict SDKs (openai-python pydantic ResponseTextDeltaEvent) reject
	// frames missing them and abort the stream. Carry the item_id we recorded
	// from this key's real deltas and claim the next outgoing sequence_number —
	// frames emitted after this one renumber past it (see nextOutSeq).
	p.outSeq++
	payload := map[string]any{
		"type":            eventType,
		"output_index":    key.outputIndex,
		"sequence_number": p.outSeq,
		"delta":           value,
	}
	if id := p.itemIDs[key]; id != "" {
		payload["item_id"] = id
	}
	if key.field == fieldOutputText || key.field == fieldReasoningText {
		payload["content_index"] = key.contentIndex
	}

	data, err := common.MarshalNoEscape(payload)
	if err != nil {
		logging.Error(ctx, "Failed to marshal synthetic Responses SSE delta frame", err,
			"outputIndex", key.outputIndex, "field", string(key.field))
		return
	}
	p.writeOutput(common.BuildEventFrame([]byte(eventType), data))
}

// flushKey flushes one streaming demasker; buffered text goes out as a
// synthetic delta, or as the masked carry when the flush itself fails.
func (p *Processor) flushKey(ctx context.Context, key demaskerKey) {
	d, ok := p.demaskers[key]
	if !ok {
		return
	}

	demasked, err := d.DemaskChunk(ctx, "", true)
	if err != nil {
		logging.Error(ctx, "Responses SSE flush failed, using fallback", err,
			"outputIndex", key.outputIndex, "field", string(key.field))
		if demasked == "" {
			return
		}
		metrics.IncDemaskSSEFailed()
		p.emitSyntheticDeltaFrame(ctx, key, demasked)
		return
	}

	if demasked == "" {
		return
	}
	p.emitSyntheticDeltaFrame(ctx, key, demasked)
}

// flushAll flushes every active streaming demasker. Used on terminal
// response.* events, [DONE] and EOS.
func (p *Processor) flushAll(ctx context.Context) {
	for key := range p.demaskers {
		p.flushKey(ctx, key)
	}
}

func (p *Processor) getDemasker(key demaskerKey) common.Demasker {
	if d, ok := p.demaskers[key]; ok {
		return d
	}
	// function_call arguments deltas are JSON fragments the client concatenates,
	// so restored originals must be JSON-escaped (matches chatcompletions and
	// messages); other fields (output_text) use the plain demasker.
	var d common.Demasker
	if key.field == fieldFunctionArgs && p.newJSONDemasker != nil {
		d = p.newJSONDemasker()
	} else {
		d = p.newDemasker()
	}
	p.demaskers[key] = d
	return d
}

func (p *Processor) getAccum(key demaskerKey) *fieldAccum {
	if a, ok := p.accums[key]; ok {
		return a
	}
	a := &fieldAccum{}
	p.accums[key] = a
	return a
}

func (p *Processor) writeOutput(b []byte) {
	if len(b) > 0 {
		p.outputBuf = append(p.outputBuf, b...)
	}
}

func (p *Processor) takeOutput() []byte {
	out := p.outputBuf
	p.outputBuf = nil
	return out
}

func (p *Processor) prependTail(b []byte) []byte {
	if len(p.tailBuf) == 0 {
		return b
	}
	p.tailBuf = append(p.tailBuf, b...)
	out := p.tailBuf
	p.tailBuf = nil
	return out
}
