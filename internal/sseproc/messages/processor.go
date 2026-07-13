// Package messages implements SSE stream processing for the Anthropic
// Messages API (/v1/messages) response demasking.
//
// The Anthropic wire format differs from OpenAI's chat-completions stream:
// frames are pairs of "event: <name>" + "data: <json>" lines separated by
// "\n\n", and content is streamed as a sequence of content_block_delta
// events whose `delta.type` selects which field carries demaskable text
// (text_delta.text, thinking_delta.thinking, input_json_delta.partial_json).
//
// This package mirrors the design of internal/sseproc: a Processor consumes
// arbitrary Envoy body chunks via ProcessChunk, splits them into complete
// frames (carrying any incomplete tail to the next chunk), and routes each
// frame to a per-block, per-field Demasker created lazily via a factory.
// Frames that don't need demasking (message_start, ping, comments, etc.)
// pass through byte-for-byte.
package messages

import (
	"context"
	"strconv"

	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"

	"github.com/cloud-ru-tech/guardrails-llm-filter/internal/logging"
	"github.com/cloud-ru-tech/guardrails-llm-filter/internal/metrics"
	"github.com/cloud-ru-tech/guardrails-llm-filter/internal/sseproc/common"
)

// demaskerKey uniquely identifies one Demasker instance: per content block,
// per field. Anthropic streams blocks sequentially, but the keying matches
// sseproc's pattern so a block's lifecycle stays isolated even if blocks
// were ever interleaved.
type demaskerKey struct {
	blockIndex int
	field      fieldType
}

// Processor processes one Anthropic SSE response stream end-to-end.
// Create via New; feed Envoy body chunks via ProcessChunk.
type Processor struct {
	newDemasker     common.DemaskerFactoryFn
	newJSONDemasker common.DemaskerFactoryFn // for tool-input fields; falls back to newDemasker when unset

	tailBuf   []byte                          // bytes of an incomplete SSE frame carried between chunks
	outputBuf []byte                          // accumulated output for the current ProcessChunk call
	demaskers map[demaskerKey]common.Demasker // one Demasker per (blockIndex, field)
	blocks        map[int]*blockAccum             // per-block accumulator and state (block type, raw text, JSON depth)
	doneSent      bool                            // true if we've already forwarded the [DONE] frame
	captureMasked bool                            // record pre-demask text for the audit trail
	masked        common.MaskedTextRecorder       // pre-demask text content for the audit trail
}

// MaskedResponseText returns the accumulated masked (pre-demask) text/thinking
// content of the streamed response for the audit trail (tool input excluded).
func (p *Processor) MaskedResponseText() []string { return p.masked.Texts() }

// Option configures a Processor.
type Option func(*Processor)

// WithJSONDemaskerFactory supplies the factory used for input_json_delta
// fields. Its demaskers must JSON-escape restored originals: the fragments
// are JSON *fragments* accumulated by the client, so inserting an original
// containing a quote or backslash verbatim would corrupt the tool input the
// client assembles — unrecoverably, since Anthropic never re-sends the full
// input in a later event.
func WithJSONDemaskerFactory(factory common.DemaskerFactoryFn) Option {
	return func(p *Processor) {
		p.newJSONDemasker = factory
	}
}

// WithMaskedResponseCapture makes the processor accumulate the pre-demask
// text/thinking content for the audit trail (via MaskedResponseText at EOS).
func WithMaskedResponseCapture() Option {
	return func(p *Processor) {
		p.captureMasked = true
	}
}

// New creates a Processor for a single SSE response stream.
func New(factory common.DemaskerFactoryFn, opts ...Option) *Processor {
	p := &Processor{
		newDemasker: factory,
		demaskers:   make(map[demaskerKey]common.Demasker),
		blocks:      make(map[int]*blockAccum),
	}
	for _, opt := range opts {
		opt(p)
	}
	return p
}

// ProcessChunk consumes one Envoy body chunk and returns bytes ready for the
// client, or nil when all Demaskers are still buffering and we have no
// completed frames to forward yet (caller must not send a body mutation).
func (p *Processor) ProcessChunk(ctx context.Context, body []byte, endOfStream bool) ([]byte, error) {
	frames := p.prepareFrames(ctx, body, endOfStream)

	for _, frame := range frames {
		p.processFrame(ctx, frame)
	}

	// If the stream ended without a [DONE] sentinel (Envoy half-closed the
	// upstream, or the upstream simply omitted [DONE]), flush every open
	// demasker so no buffered text is lost.
	if endOfStream && !p.doneSent {
		p.flushAllBlocks(ctx)
	}

	return p.takeOutput(), nil
}

// prepareFrames assembles frames from the current chunk plus any leftover
// tail from the previous chunk. On end-of-stream, any partial frame still
// in the tail is logged and treated as a complete frame so we don't lose
// content silently.
func (p *Processor) prepareFrames(ctx context.Context, body []byte, endOfStream bool) [][]byte {
	workBuf := p.prependTail(body)
	frames, tail := common.SplitFrames(workBuf)

	if !endOfStream {
		p.tailBuf = tail
		return frames
	}

	if len(tail) > 0 {
		// Never log the tail bytes: they are response-body content.
		logging.Warn(ctx, "Anthropic SSE stream ended with incomplete frame", "tailLen", len(tail))
		frames = append(frames, tail)
	}
	return frames
}

// processFrame routes a single SSE frame.
func (p *Processor) processFrame(ctx context.Context, frame []byte) {
	pf := common.ClassifyFrame(frame)

	switch pf.Kind {
	case common.FrameDone:
		p.handleDoneFrame(ctx, pf.Original)
	case common.FramePassthrough:
		p.writeOutput(pf.Original)
	case common.FrameEvent:
		p.handleEventFrame(ctx, pf)
	}
}

// handleDoneFrame flushes all open demaskers, emits any pending demasked
// output, then forwards the [DONE] sentinel.
func (p *Processor) handleDoneFrame(ctx context.Context, frame []byte) {
	p.flushAllBlocks(ctx)
	p.doneSent = true
	p.writeOutput(frame)
}

// handleEventFrame dispatches a parsed event frame based on its data
// payload's "type" field. Unknown or unparseable events are passed through.
func (p *Processor) handleEventFrame(ctx context.Context, pf common.ParsedFrame) {
	eventType := gjson.GetBytes(pf.Data, "type").String()

	switch eventType {
	case "content_block_start":
		p.handleContentBlockStart(ctx, pf)
	case "content_block_delta":
		p.handleContentBlockDelta(ctx, pf)
	case "content_block_stop":
		p.handleContentBlockStop(ctx, pf)
	default:
		// message_start, message_delta, message_stop, ping, error, and any
		// event we don't know about: forward unchanged. The Anthropic API
		// may add new event types in future and we don't want to swallow
		// them.
		p.writeOutput(pf.Original)
	}
}

// handleContentBlockStart records the block's type from content_block.type
// so subsequent deltas know whether to demask and which field to use.
func (p *Processor) handleContentBlockStart(_ context.Context, pf common.ParsedFrame) {
	idx := int(gjson.GetBytes(pf.Data, "index").Int())
	typStr := gjson.GetBytes(pf.Data, "content_block.type").String()
	p.blocks[idx] = &blockAccum{typ: blockType(typStr)}
	p.writeOutput(pf.Original)
}

// handleContentBlockDelta is the only routing arm that mutates frame
// contents. We branch on `delta.type`: text_delta / thinking_delta /
// input_json_delta carry demaskable text in known string fields;
// signature_delta and citations_delta (and anything else) are passed
// through unchanged.
func (p *Processor) handleContentBlockDelta(ctx context.Context, pf common.ParsedFrame) {
	idx := int(gjson.GetBytes(pf.Data, "index").Int())
	deltaType := gjson.GetBytes(pf.Data, "delta.type").String()

	var (
		field        fieldType
		valuePath    string
		fragment     string
		flushOnClose bool
	)
	switch deltaType {
	case "text_delta":
		field = fieldText
		valuePath = "delta.text"
		fragment = gjson.GetBytes(pf.Data, valuePath).String()
	case "thinking_delta":
		field = fieldThinking
		valuePath = "delta.thinking"
		fragment = gjson.GetBytes(pf.Data, valuePath).String()
	case "input_json_delta":
		field = fieldToolInput
		valuePath = "delta.partial_json"
		fragment = gjson.GetBytes(pf.Data, valuePath).String()
		flushOnClose = true
	default:
		// signature_delta, citations_delta, anything unknown: passthrough.
		p.writeOutput(pf.Original)
		return
	}

	// Empty fragment: nothing to demask, but keep the frame in the stream
	// so we don't reorder events.
	if fragment == "" {
		p.writeOutput(pf.Original)
		return
	}

	block, ok := p.blocks[idx]
	if !ok {
		// Delta arrived without a prior content_block_start. Synthesize a
		// minimal block state so we don't crash, but log it — this is an
		// upstream protocol violation.
		logging.Warn(ctx, "Anthropic SSE content_block_delta before content_block_start", "index", idx)
		block = &blockAccum{}
		p.blocks[idx] = block
	}

	// Redacted thinking blocks carry only encrypted bytes; never feed those
	// to the demasker. The same intent applies to the full-body demask path.
	if block.typ == blockRedactedThinking {
		p.writeOutput(pf.Original)
		return
	}

	closed := block.AppendAndCheckClose(field, fragment)
	flush := flushOnClose && closed

	if p.captureMasked && (field == fieldText || field == fieldThinking) {
		p.masked.Add(strconv.Itoa(idx)+"/"+string(field), fragment)
	}
	key := demaskerKey{blockIndex: idx, field: field}
	d := p.getDemasker(key)
	demasked, err := d.DemaskChunk(ctx, fragment, flush)
	if err != nil {
		p.fallbackBlock(ctx, idx, field, pf, valuePath, demasked)
		return
	}

	if demasked == "" {
		// Demasker is buffering. Drop this frame — it will be re-emitted
		// (in aggregated form) when the demasker eventually returns text
		// or when we force a flush on content_block_stop / [DONE] / EOS.
		return
	}

	p.emitDeltaFrame(ctx, pf, valuePath, demasked)
}

// handleContentBlockStop flushes the block's demasker so any buffered text
// is emitted as a synthetic content_block_delta BEFORE the stop event,
// preserving the invariant that deltas arrive between start and stop.
func (p *Processor) handleContentBlockStop(ctx context.Context, pf common.ParsedFrame) {
	idx := int(gjson.GetBytes(pf.Data, "index").Int())
	p.flushBlock(ctx, idx)
	p.writeOutput(pf.Original)
	// We deliberately keep the block entry around: a stop event ends the
	// block but doesn't release the accumulator memory until ProcessChunk
	// returns. The demasker map entry stays too — Anthropic doesn't reuse
	// indices within a single message, so this can't cause leakage across
	// blocks. The whole Processor is garbage-collected at end of stream.
}

// emitDeltaFrame rebuilds a content_block_delta frame from the original
// data with the demasked value swapped in at valuePath.
func (p *Processor) emitDeltaFrame(ctx context.Context, pf common.ParsedFrame, valuePath, value string) {
	patched, err := sjson.SetBytes(pf.Data, valuePath, value)
	if err != nil {
		logging.Error(ctx, "Failed to patch Anthropic SSE delta frame", err, "path", valuePath)
		p.writeOutput(pf.Original)
		return
	}
	p.writeOutput(common.BuildEventFrame(pf.Event, patched))
}

// emitSyntheticDeltaFrame builds a content_block_delta frame from scratch
// when flushing a block whose buffered text didn't get a frame to attach
// to (the demasker returned "" inline and finally returned content during
// a flush on content_block_stop / [DONE] / EOS).
func (p *Processor) emitSyntheticDeltaFrame(ctx context.Context, blockIdx int, field fieldType, value string) {
	deltaType, valueKey, ok := deltaWireForField(field)
	if !ok {
		return
	}

	payload := map[string]any{
		"type":  "content_block_delta",
		"index": blockIdx,
		"delta": map[string]any{
			"type":   deltaType,
			valueKey: value,
		},
	}

	data, err := common.MarshalNoEscape(payload)
	if err != nil {
		logging.Error(ctx, "Failed to marshal synthetic Anthropic SSE delta frame", err,
			"blockIndex", blockIdx, "field", string(field))
		return
	}
	p.writeOutput(common.BuildEventFrame([]byte("content_block_delta"), data))
}

// deltaWireForField maps a fieldType back to the Anthropic wire-format
// delta type and value key.
func deltaWireForField(field fieldType) (deltaType, valueKey string, ok bool) {
	switch field {
	case fieldText:
		return "text_delta", "text", true
	case fieldThinking:
		return "thinking_delta", "thinking", true
	case fieldToolInput:
		return "input_json_delta", "partial_json", true
	}
	return "", "", false
}

// fallbackBlock emits the un-emitted content the demasker handed back when it
// errored (with any unresolved placeholders left intact), using the original
// frame as a template so nothing is lost. Mirrors the "immediate fallback"
// behaviour in sseproc's processTextField.
func (p *Processor) fallbackBlock(ctx context.Context, blockIdx int, field fieldType, pf common.ParsedFrame, valuePath, content string) {
	logging.Error(ctx, "Anthropic SSE demasker failed, using fallback", nil,
		"blockIndex", blockIdx, "field", string(field))

	if content == "" {
		return
	}

	metrics.IncDemaskSSEFailed()

	p.emitDeltaFrame(ctx, pf, valuePath, content)
}

// flushBlock flushes every demasker that actually exists for one block and
// emits any pending demasked text as a synthetic content_block_delta. Used by
// handleContentBlockStop and the all-blocks flush paths.
//
// It iterates the demasker map (keyed by the field derived from delta.type at
// creation time) rather than deriving the field from the block's recorded type.
// The two can disagree — a text_delta may arrive before content_block_start
// (block synthesized with an empty type), or the delta/block types may desync —
// and keying the flush off the block type would then skip a live demasker,
// silently dropping its buffered tail (data loss, not fail-open).
func (p *Processor) flushBlock(ctx context.Context, blockIdx int) {
	for key, d := range p.demaskers {
		if key.blockIndex != blockIdx {
			continue
		}

		demasked, err := d.DemaskChunk(ctx, "", true)
		if err != nil {
			logging.Error(ctx, "Anthropic SSE flush failed, using fallback", err,
				"blockIndex", blockIdx, "field", string(key.field))
			if demasked == "" {
				continue
			}
			metrics.IncDemaskSSEFailed()
			p.emitSyntheticDeltaFrame(ctx, blockIdx, key.field, demasked)
			continue
		}

		if demasked == "" {
			continue
		}

		p.emitSyntheticDeltaFrame(ctx, blockIdx, key.field, demasked)
	}
}

// flushAllBlocks flushes every active demasker. Used on [DONE] and on EOS.
func (p *Processor) flushAllBlocks(ctx context.Context) {
	// Walk demaskers (not blocks) so we don't flush a block that never had
	// a demasker created.
	seen := make(map[int]bool, len(p.demaskers))
	for key := range p.demaskers {
		if seen[key.blockIndex] {
			continue
		}
		seen[key.blockIndex] = true
		p.flushBlock(ctx, key.blockIndex)
	}
}

// getDemasker returns the existing Demasker for the key or creates a new one
// and stores it. Tool-input fields get the JSON-escaping factory (when wired)
// because their fragments live inside a JSON string context.
func (p *Processor) getDemasker(key demaskerKey) common.Demasker {
	if d, ok := p.demaskers[key]; ok {
		return d
	}
	var d common.Demasker
	if key.field == fieldToolInput && p.newJSONDemasker != nil {
		d = p.newJSONDemasker()
	} else {
		d = p.newDemasker()
	}
	p.demaskers[key] = d
	return d
}

// writeOutput appends to the output buffer.
func (p *Processor) writeOutput(b []byte) {
	if len(b) > 0 {
		p.outputBuf = append(p.outputBuf, b...)
	}
}

// takeOutput returns the accumulated output and clears the buffer.
func (p *Processor) takeOutput() []byte {
	out := p.outputBuf
	p.outputBuf = nil
	return out
}

// prependTail prepends the leftover bytes from the previous chunk.
func (p *Processor) prependTail(b []byte) []byte {
	if len(p.tailBuf) == 0 {
		return b
	}
	p.tailBuf = append(p.tailBuf, b...)
	out := p.tailBuf
	p.tailBuf = nil
	return out
}
