// Package sseproc implements SSE stream processing for LLM response
// demasking. It handles frame splitting across arbitrary Envoy chunk
// boundaries, delta aggregation while the Demasker buffers, and frame
// re-assembly using canonical OpenAI-compatible llmutils types.
package chatcompletions

import (
	"context"
	"encoding/json"
	"strconv"

	"github.com/cloud-ru-tech/guardrails-llm-filter/internal/logging"
	"github.com/cloud-ru-tech/guardrails-llm-filter/internal/metrics"
	"github.com/cloud-ru-tech/guardrails-llm-filter/internal/sseproc/common"
	llmchat "github.com/cloud-ru-tech/guardrails-llm-filter/pkg/llmutils/chatcompletions"
)

// fieldType identifies the type of text field being processed.
type fieldType string

const (
	fieldContent       fieldType = "content"
	fieldReasoning     fieldType = "reasoning"
	fieldToolArguments fieldType = "tool_arguments"
)

// demaskerKey uniquely identifies a Demasker for one (choiceIndex, toolCallIndex, field) tuple.
// For content/reasoning fields, toolCallIndex is 0 (unused).
type demaskerKey struct {
	choiceIndex   int
	toolCallIndex int // 0 for content/reasoning, actual index for tool_calls
	field         fieldType
}

// Processor processes one SSE response stream end-to-end.
// Create via New; feed Envoy body chunks via ProcessChunk.
type Processor struct {
	newDemasker     common.DemaskerFactoryFn
	newJSONDemasker common.DemaskerFactoryFn // for tool-argument fields; falls back to newDemasker when unset

	tailBuf        []byte                          // bytes of an incomplete SSE frame carried between chunks
	outputBuf      []byte                          // accumulated output for the current ProcessChunk call
	aggr           *aggregator                     // accumulates metadata and non-text deltas while Demaskers buffer
	demaskers      map[demaskerKey]common.Demasker // one Demasker per (choiceIndex, field)
	doneSent       bool                            // true if we've already sent [DONE] frame
	contentStarted map[int]bool                    // tracks whether content has started for each choice index
	captureMasked  bool                            // record pre-demask text for the audit trail
	masked         common.MaskedTextRecorder       // pre-demask text content for the audit trail
}

// MaskedResponseText returns the accumulated masked (pre-demask) text content
// of the streamed response for the audit trail (tool-call arguments excluded).
func (p *Processor) MaskedResponseText() []string { return p.masked.Texts() }

// Option configures a Processor.
type Option func(*Processor)

// WithJSONDemaskerFactory supplies the factory used for tool-call and legacy
// function_call argument fields. Its demaskers must JSON-escape restored
// originals: the arguments arrive as JSON *fragments* that the client
// accumulates into a JSON object, so inserting an original containing a quote
// or backslash verbatim would corrupt that object — unrecoverably, since the
// chat-completions stream never re-sends the full arguments in a later event.
func WithJSONDemaskerFactory(factory common.DemaskerFactoryFn) Option {
	return func(p *Processor) {
		p.newJSONDemasker = factory
	}
}

// WithMaskedResponseCapture makes the processor accumulate the pre-demask text
// content for the audit trail (exposed via MaskedResponseText at stream end).
func WithMaskedResponseCapture() Option {
	return func(p *Processor) {
		p.captureMasked = true
	}
}

// New creates a Processor for a single SSE response stream.
func New(factory common.DemaskerFactoryFn, opts ...Option) *Processor {
	p := &Processor{
		newDemasker:    factory,
		demaskers:      make(map[demaskerKey]common.Demasker),
		aggr:           newAggregator(),
		contentStarted: make(map[int]bool),
	}
	for _, opt := range opts {
		opt(p)
	}
	return p
}

// ProcessChunk consumes one Envoy body chunk and returns bytes ready for the
// client, or nil when Demaskers are still buffering (caller must not send).
func (p *Processor) ProcessChunk(ctx context.Context, body []byte, endOfStream bool) ([]byte, error) {
	frames := p.prepareFrames(ctx, body, endOfStream)

	for _, frame := range frames {
		p.processFrame(ctx, frame)
	}

	// Flush any demaskers still buffering when the stream ends without a
	// [DONE] sentinel (upstream died or Envoy half-closed).
	if endOfStream && !p.doneSent && len(p.demaskers) > 0 {
		p.flushAllDemaskers(ctx)
	}

	return p.takeOutput(), nil
}

// prepareFrames splits the body into frames, handling tail buffer and EOS.
func (p *Processor) prepareFrames(ctx context.Context, body []byte, endOfStream bool) [][]byte {
	workBuf := p.prependTail(body)
	frames, tail := common.SplitFrames(workBuf)

	if !endOfStream {
		p.tailBuf = tail
		return frames
	}

	if len(tail) > 0 {
		// Never log the tail bytes: they are response-body content.
		logging.Warn(ctx, "SSE stream ended with incomplete frame", "tailLen", len(tail))
		frames = append(frames, tail)
	}
	return frames
}

// chunkHasText returns true if any choice has content or reasoning.
func chunkHasText(chunk llmchat.Chunk) bool {
	for _, choice := range chunk.Choices {
		if ptrVal(choice.Delta.Content) != "" || ptrVal(choice.Delta.Reasoning) != "" {
			return true
		}
	}
	return false
}

// chunkHasToolCalls returns true if any choice has tool_calls.
func chunkHasToolCalls(chunk llmchat.Chunk) bool {
	for _, choice := range chunk.Choices {
		if len(choice.Delta.ToolCalls) > 0 {
			return true
		}
	}
	return false
}

// chunkHasFunctionCall returns true if any choice has function_call.
func chunkHasFunctionCall(chunk llmchat.Chunk) bool {
	for _, choice := range chunk.Choices {
		if choice.Delta.FunctionCall != nil && choice.Delta.FunctionCall.Arguments != "" {
			return true
		}
	}
	return false
}

// processFrame routes a frame to the appropriate handler based on its kind.
func (p *Processor) processFrame(ctx context.Context, frame []byte) {
	kind, jsonPayload := classifyFrame(frame)

	switch kind {
	case frameDone:
		p.handleDoneFrame(ctx, frame)
	case framePassthrough:
		p.writeOutput(frame)
	case frameData:
		p.handleDataFrame(ctx, frame, jsonPayload)
	}
}

// handleDoneFrame processes the [DONE] sentinel frame.
func (p *Processor) handleDoneFrame(ctx context.Context, frame []byte) {
	p.flushAllDemaskers(ctx)
	p.doneSent = true
	p.writeOutput(frame)
}

// handleDataFrame processes a data frame containing a JSON chunk.
func (p *Processor) handleDataFrame(ctx context.Context, frame, jsonPayload []byte) {
	var chunk llmchat.Chunk
	err := json.Unmarshal(jsonPayload, &chunk)
	if err != nil {
		logging.Error(ctx, "Failed to unmarshal data chunk", err)
		p.writeOutput(frame)
		return
	}

	if len(chunk.Choices) == 0 {
		p.writeOutput(frame)
		return
	}

	p.aggr.merge(chunk)

	hasData := chunkHasText(chunk) || chunkHasToolCalls(chunk) || chunkHasFunctionCall(chunk)
	if !hasData && isMetadataChunk(chunk) {
		p.handleMetadataFrame(ctx, chunk, frame)
		return
	}

	if !hasData {
		// Non-empty choices, but nothing we demask and no metadata: a
		// delta.refusal / delta.audio, a role-only opening delta
		// ({"delta":{"role":"assistant"}}) or an empty keepalive delta. These
		// carry no placeholders, so forward the frame unchanged (fail-open on
		// content we don't touch) rather than dropping it. Demaskers must NOT
		// be flushed here: a flush would emit a partially buffered placeholder
		// (e.g. "<EMA") that the next content delta could then never complete,
		// leaking the placeholder to the client. The withheld tail is delivered
		// with the next content delta or at stream end; this frame arriving
		// ahead of that tail is harmless cross-field reordering.
		p.writeOutput(frame)
		return
	}

	// Process tool calls through demaskers (arguments need demasking)
	if chunkHasToolCalls(chunk) {
		p.processToolCalls(ctx, chunk)
	}

	// Process legacy function_call through demaskers
	if chunkHasFunctionCall(chunk) {
		p.processFunctionCall(ctx, chunk)
	}

	// Process content/reasoning through demaskers if present
	if chunkHasText(chunk) {
		p.processChoices(ctx, chunk)
	}

	// A combined frame carries finish_reason and/or usage alongside the data
	// deltas. The demasked frames emitted above set FinishReason and Usage to
	// nil, so emit a separate metadata-only frame to preserve them for clients
	// that terminate on finish_reason or read the final usage. (Pure-metadata
	// frames are handled by the early return above.)
	if isMetadataChunk(chunk) {
		metaFrame, err := metadataOnlyFrame(chunk)
		if err != nil {
			logging.Error(ctx, "Failed to build metadata frame from combined chunk", err)
			return
		}
		p.handleMetadataFrame(ctx, chunk, metaFrame)
	}
}

// metadataOnlyFrame returns an SSE frame for chunk with every choice's Delta
// cleared, keeping the choice Index, finish_reason and the top-level usage. It
// carries the trailing metadata of a combined content+finish frame without
// duplicating the (already-demasked-and-emitted) content deltas.
func metadataOnlyFrame(chunk llmchat.Chunk) ([]byte, error) {
	out := chunk.Copy()
	for i := range out.Choices {
		out.Choices[i].Delta = llmchat.Delta{}
	}
	return marshalSseFrame(out)
}

// handleMetadataFrame outputs a metadata-only frame (usage and/or
// finish_reason) in its stream position.
func (p *Processor) handleMetadataFrame(ctx context.Context, chunk llmchat.Chunk, frame []byte) {
	// A finish_reason ends its choice: flush that choice's demaskers first so
	// the buffered demasked content is emitted before this terminal frame.
	for _, choice := range chunk.Choices {
		if choice.FinishReason != nil && ptrVal(choice.FinishReason) != "" {
			p.flushChoice(ctx, choice.Index)
		}
	}

	// Emit in position rather than buffering to stream end. usage rides the
	// stream where upstream placed it — per content chunk for continuous usage
	// stats, or once in the trailing chunk for include_usage — so the client
	// sees the same usage cadence it requested, with no unbounded buffer. The
	// content-frame emitters strip usage, so it is never duplicated. Ordering
	// against a demasker still holding a split placeholder is at most one frame
	// early and self-corrects on the next delta.
	p.writeOutput(frame)
}

// processChoices processes all choices in a chunk.
func (p *Processor) processChoices(ctx context.Context, chunk llmchat.Chunk) {
	for _, choice := range chunk.Choices {
		p.processChoice(ctx, choice)
	}
}

// processChoice processes a single choice's text fields and finish reason.
func (p *Processor) processChoice(ctx context.Context, choice llmchat.ChunkChoice) {
	content := ptrVal(choice.Delta.Content)
	reasoning := ptrVal(choice.Delta.Reasoning)

	if content == "" && reasoning == "" {
		return
	}

	if reasoning != "" {
		p.processTextField(ctx, choice.Index, fieldReasoning, reasoning)
	}

	if content != "" && !p.contentStarted[choice.Index] {
		p.flushFieldForChoice(ctx, choice.Index, fieldReasoning)
		p.contentStarted[choice.Index] = true
	}

	if content != "" {
		p.processTextField(ctx, choice.Index, fieldContent, content)
	}

	if choice.FinishReason != nil && ptrVal(choice.FinishReason) != "" {
		p.flushChoice(ctx, choice.Index)
	}
}

// processTextField demasks a text field and outputs it or falls back on error.
func (p *Processor) processTextField(ctx context.Context, choiceIdx int, field fieldType, text string) {
	key := demaskerKey{choiceIdx, 0, field}
	if p.captureMasked {
		p.masked.Add(strconv.Itoa(choiceIdx)+"/"+string(field), text)
	}
	d := p.getDemasker(key)
	demasked, err := d.DemaskChunk(ctx, text, false)
	if err != nil {
		p.fallbackTextField(ctx, choiceIdx, field, demasked)
		return
	}

	if demasked != "" {
		p.outputTextFrame(ctx, choiceIdx, field, demasked)
	}
}

// fallbackTextField outputs the un-emitted content the demasker handed back
// when it errored (with any unresolved placeholders intact) so nothing is lost.
func (p *Processor) fallbackTextField(ctx context.Context, choiceIdx int, field fieldType, content string) {
	logging.Error(ctx, "Failed to demask content chunk, using fallback", nil, "choiceIdx", choiceIdx, "field", field)
	if content != "" {
		metrics.IncDemaskSSEFailed()
		p.outputTextFrame(ctx, choiceIdx, field, content)
	}
}

// takeOutput returns the accumulated output and clears the buffer.
func (p *Processor) takeOutput() []byte {
	out := p.outputBuf
	p.outputBuf = nil
	return out
}

// getDemasker returns the existing Demasker for the key, or creates and stores
// a fresh one via the factory. tool-argument fields get the JSON-escaping
// factory (when wired) because their fragments live inside a JSON string that
// the client reassembles.
func (p *Processor) getDemasker(key demaskerKey) common.Demasker {
	if d, ok := p.demaskers[key]; ok {
		return d
	}
	var d common.Demasker
	if key.field == fieldToolArguments && p.newJSONDemasker != nil {
		d = p.newJSONDemasker()
	} else {
		d = p.newDemasker()
	}
	p.demaskers[key] = d
	return d
}

func (p *Processor) writeOutput(b []byte) {
	if len(b) > 0 {
		p.outputBuf = append(p.outputBuf, b...)
	}
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

func ptrVal[T any](ptr *T) T {
	if ptr == nil {
		var empty T
		return empty
	}
	return *ptr
}

func toPtr[T any](v T) *T {
	return &v
}

// isMetadataChunk returns true if chunk has usage or finish_reason but no content.
// These frames should be buffered and output after content frames.
func isMetadataChunk(chunk llmchat.Chunk) bool {
	if chunk.Usage != nil {
		return true
	}
	for _, choice := range chunk.Choices {
		if choice.FinishReason != nil && ptrVal(choice.FinishReason) != "" {
			return true
		}
	}
	return false
}

// outputTextFrame outputs a text frame (content or reasoning) for a specific choice.
func (p *Processor) outputTextFrame(ctx context.Context, choiceIdx int, field fieldType, text string) {
	if text == "" {
		return
	}

	chunk := p.aggr.merged
	merged, ok := p.aggr.findChoice(choiceIdx)
	if !ok {
		return
	}

	choice := merged.Copy()
	choice.FinishReason = nil

	if field == fieldContent {
		choice.Delta.Content = &text
		choice.Delta.Reasoning = nil
	} else {
		choice.Delta.Reasoning = &text
		choice.Delta.Content = nil
	}

	// Text frames should not include tool_calls or function_call (they're output separately)
	choice.Delta.ToolCalls = nil
	choice.Delta.FunctionCall = nil

	chunk.Choices = []llmchat.ChunkChoice{choice}
	chunk.Usage = nil

	frame, err := marshalSseFrame(chunk)
	if err != nil {
		logging.Error(ctx, "Failed to marshal text frame", err)
		return
	}
	p.writeOutput(frame)
}

// flushFieldForChoice flushes a specific field's demasker for a choice.
func (p *Processor) flushFieldForChoice(ctx context.Context, choiceIdx int, field fieldType) {
	key := demaskerKey{choiceIdx, 0, field}
	d, ok := p.demaskers[key]
	if !ok {
		return
	}

	demasked, err := d.DemaskChunk(ctx, "", true)
	if err != nil {
		logging.Error(ctx, "Error flushing content demasker, using fallback", err, "key", key)
		if demasked != "" {
			metrics.IncDemaskSSEFailed()
			p.outputTextFrame(ctx, choiceIdx, field, demasked)
		}
		return
	}

	if demasked != "" {
		p.outputTextFrame(ctx, choiceIdx, field, demasked)
	}
}

// flushChoice flushes reasoning, content, function_call, and tool call demaskers for a specific choice.
func (p *Processor) flushChoice(ctx context.Context, choiceIdx int) {
	p.flushFieldForChoice(ctx, choiceIdx, fieldReasoning)
	p.flushFieldForChoice(ctx, choiceIdx, fieldContent)
	p.flushFunctionCallForChoice(ctx, choiceIdx)
	p.flushAllToolCallsForChoice(ctx, choiceIdx)
}

// processToolCalls processes tool calls through demaskers for all choices in a chunk.
func (p *Processor) processToolCalls(ctx context.Context, chunk llmchat.Chunk) {
	for _, choice := range chunk.Choices {
		for _, tc := range choice.Delta.ToolCalls {
			if tc.Function == nil || tc.Function.Arguments == "" {
				// An id/name announcement with no arguments payload (e.g. a
				// parameterless call whose arguments stay empty). Nothing to
				// demask — forward it as-is so the tool call isn't dropped
				// when no arguments delta ever follows. Merging clients treat
				// a repeated id/name (when arguments do follow) as idempotent.
				p.outputToolCallPassthrough(ctx, choice.Index, tc)
				continue
			}

			args := tc.Function.Arguments

			// Accumulate in choicesAccum and check if JSON closed
			acc := p.aggr.getAccum(choice.Index)
			jsonClosed := acc.AppendToolArguments(tc.Index, args)

			// Process through demasker
			key := demaskerKey{choice.Index, tc.Index, fieldToolArguments}
			d := p.getDemasker(key)

			demasked, err := d.DemaskChunk(ctx, args, jsonClosed)
			if err != nil {
				logging.Error(ctx, "Failed to demask tool call arguments, using fallback", err,
					"choiceIdx", choice.Index, "toolCallIdx", tc.Index)
				p.fallbackToolCall(ctx, choice.Index, tc.Index, demasked)
				continue
			}

			if demasked != "" {
				p.outputToolCallFrame(ctx, choice.Index, tc.Index, demasked)
			}
		}
	}
}

// outputToolCallFrame outputs a single tool call frame with demasked arguments.
func (p *Processor) outputToolCallFrame(ctx context.Context, choiceIdx, toolCallIdx int, demaskedArgs string) {
	if demaskedArgs == "" {
		return
	}

	chunk := p.aggr.merged
	merged, ok := p.aggr.findChoice(choiceIdx)
	if !ok {
		return
	}

	// Find the tool call in merged state
	mergedChoice := *merged
	var foundToolCall *llmchat.ToolCallDelta
	for i := range mergedChoice.Delta.ToolCalls {
		if mergedChoice.Delta.ToolCalls[i].Index == toolCallIdx {
			foundToolCall = &mergedChoice.Delta.ToolCalls[i]
			break
		}
	}

	if foundToolCall == nil {
		return
	}

	// Create output choice with only this tool call
	choice := llmchat.ChunkChoice{
		Index:        choiceIdx,
		Delta:        llmchat.Delta{},
		FinishReason: nil,
	}

	// Create tool call delta with demasked arguments
	toolCall := llmchat.ToolCallDelta{
		Index: toolCallIdx,
		ID:    foundToolCall.ID,
		Type:  foundToolCall.Type,
	}

	if foundToolCall.Function != nil {
		toolCall.Function = &llmchat.FunctionCallDelta{
			Name:      foundToolCall.Function.Name,
			Arguments: demaskedArgs, // Only the demasked delta
		}
	}

	choice.Delta.ToolCalls = []llmchat.ToolCallDelta{toolCall}

	// Create output chunk
	outChunk := chunk.Copy()
	outChunk.Choices = []llmchat.ChunkChoice{choice}
	outChunk.Usage = nil

	frame, err := marshalSseFrame(outChunk)
	if err != nil {
		logging.Error(ctx, "Failed to marshal tool call frame", err)
		return
	}
	p.writeOutput(frame)
}

// outputToolCallPassthrough forwards a tool-call delta that carries nothing to
// demask (no/empty arguments) as its own frame, preserving id/type/name.
func (p *Processor) outputToolCallPassthrough(ctx context.Context, choiceIdx int, tc llmchat.ToolCallDelta) {
	outChunk := p.aggr.merged.Copy()
	outChunk.Choices = []llmchat.ChunkChoice{{
		Index: choiceIdx,
		Delta: llmchat.Delta{ToolCalls: []llmchat.ToolCallDelta{tc}},
	}}
	outChunk.Usage = nil

	frame, err := marshalSseFrame(outChunk)
	if err != nil {
		logging.Error(ctx, "Failed to marshal tool call passthrough frame", err)
		return
	}
	p.writeOutput(frame)
}

// fallbackToolCall outputs the un-emitted tool-call arguments the demasker
// handed back when it errored (placeholders intact) so nothing is lost.
func (p *Processor) fallbackToolCall(ctx context.Context, choiceIdx, toolCallIdx int, content string) {
	if content != "" {
		metrics.IncDemaskSSEFailed()
		p.outputToolCallFrame(ctx, choiceIdx, toolCallIdx, content)
	}
}

// flushToolCallForChoice flushes a specific tool call's demasker for a choice.
func (p *Processor) flushToolCallForChoice(ctx context.Context, choiceIdx, toolCallIdx int) {
	key := demaskerKey{choiceIdx, toolCallIdx, fieldToolArguments}
	d, ok := p.demaskers[key]
	if !ok {
		return
	}

	demasked, err := d.DemaskChunk(ctx, "", true)
	if err != nil {
		logging.Error(ctx, "Error flushing tool call demasker, using fallback", err, "key", key)
		p.fallbackToolCall(ctx, choiceIdx, toolCallIdx, demasked)
		return
	}

	if demasked != "" {
		p.outputToolCallFrame(ctx, choiceIdx, toolCallIdx, demasked)
	}
}

// flushAllToolCallsForChoice flushes all tool call demaskers for a specific choice.
func (p *Processor) flushAllToolCallsForChoice(ctx context.Context, choiceIdx int) {
	// Collect all tool call indices for this choice
	toolCallIndices := make(map[int]bool)
	for key := range p.demaskers {
		if key.choiceIndex == choiceIdx && key.field == fieldToolArguments {
			toolCallIndices[key.toolCallIndex] = true
		}
	}

	// Flush each tool call
	for toolCallIdx := range toolCallIndices {
		p.flushToolCallForChoice(ctx, choiceIdx, toolCallIdx)
	}
}

// processFunctionCall processes legacy function_call through demaskers for all choices in a chunk.
// function_call uses toolCallIndex -1 to distinguish it from tool_calls array.
func (p *Processor) processFunctionCall(ctx context.Context, chunk llmchat.Chunk) {
	for _, choice := range chunk.Choices {
		if choice.Delta.FunctionCall == nil || choice.Delta.FunctionCall.Arguments == "" {
			continue
		}

		args := choice.Delta.FunctionCall.Arguments

		// Accumulate in choicesAccum and check if JSON closed
		// Use index -1 for legacy function_call to distinguish from tool_calls
		acc := p.aggr.getAccum(choice.Index)
		jsonClosed := acc.AppendToolArguments(-1, args)

		// Process through demasker
		key := demaskerKey{choice.Index, -1, fieldToolArguments}
		d := p.getDemasker(key)

		demasked, err := d.DemaskChunk(ctx, args, jsonClosed)
		if err != nil {
			logging.Error(ctx, "Failed to demask function_call arguments, using fallback", err,
				"choiceIdx", choice.Index)
			p.fallbackFunctionCall(ctx, choice.Index, demasked)
			continue
		}

		if demasked != "" {
			p.outputFunctionCallFrame(ctx, choice.Index, demasked)
		}
	}
}

// outputFunctionCallFrame outputs a legacy function_call frame with demasked arguments.
func (p *Processor) outputFunctionCallFrame(ctx context.Context, choiceIdx int, demaskedArgs string) {
	if demaskedArgs == "" {
		return
	}

	chunk := p.aggr.merged
	merged, ok := p.aggr.findChoice(choiceIdx)
	if !ok {
		return
	}

	mergedChoice := *merged
	if mergedChoice.Delta.FunctionCall == nil {
		return
	}

	// Create output choice with only function_call
	choice := llmchat.ChunkChoice{
		Index:        choiceIdx,
		Delta:        llmchat.Delta{},
		FinishReason: nil,
	}

	// Create function call delta with demasked arguments
	choice.Delta.FunctionCall = &llmchat.FunctionCallDelta{
		Name:      mergedChoice.Delta.FunctionCall.Name,
		Arguments: demaskedArgs, // Only the demasked delta
	}

	// Create output chunk
	outChunk := chunk.Copy()
	outChunk.Choices = []llmchat.ChunkChoice{choice}
	outChunk.Usage = nil

	frame, err := marshalSseFrame(outChunk)
	if err != nil {
		logging.Error(ctx, "Failed to marshal function_call frame", err)
		return
	}
	p.writeOutput(frame)
}

// fallbackFunctionCall outputs the un-emitted function_call arguments the
// demasker handed back when it errored (placeholders intact) so nothing is lost.
func (p *Processor) fallbackFunctionCall(ctx context.Context, choiceIdx int, content string) {
	if content != "" {
		metrics.IncDemaskSSEFailed()
		p.outputFunctionCallFrame(ctx, choiceIdx, content)
	}
}

// flushFunctionCallForChoice flushes the function_call demasker for a specific choice.
func (p *Processor) flushFunctionCallForChoice(ctx context.Context, choiceIdx int) {
	key := demaskerKey{choiceIdx, -1, fieldToolArguments}
	d, ok := p.demaskers[key]
	if !ok {
		return
	}

	demasked, err := d.DemaskChunk(ctx, "", true)
	if err != nil {
		logging.Error(ctx, "Error flushing function_call demasker, using fallback", err, "key", key)
		p.fallbackFunctionCall(ctx, choiceIdx, demasked)
		return
	}

	if demasked != "" {
		p.outputFunctionCallFrame(ctx, choiceIdx, demasked)
	}
}

// flushAllDemaskers flushes all demaskers and outputs frames, then metadata frames.
// Reasoning demaskers are flushed first, then content demaskers.
func (p *Processor) flushAllDemaskers(ctx context.Context) {
	// Collect all unique choice indices
	choiceIndices := make(map[int]bool)
	for key := range p.demaskers {
		choiceIndices[key.choiceIndex] = true
	}

	// Flush each choice (reasoning first, then content)
	for choiceIdx := range choiceIndices {
		p.flushChoice(ctx, choiceIdx)
	}
}
