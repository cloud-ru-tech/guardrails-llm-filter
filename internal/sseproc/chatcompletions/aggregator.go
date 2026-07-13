package chatcompletions

import (
	"fmt"
	"slices"

	"github.com/cloud-ru-tech/guardrails-llm-filter/internal/sseproc/common"
	llmchat "github.com/cloud-ru-tech/guardrails-llm-filter/pkg/llmutils/chatcompletions"
)

// toolArgsAccum tracks JSON-object depth for one tool call's streamed
// arguments, so we know when the object closes and the demasker can flush.
// The arguments themselves live in the demasker (returned on error).
type toolArgsAccum struct {
	jsonTrk common.JSONCloseTracker // tracks { and } depth across fragments
}

// choiceAccum tracks per-choice close detection for tool-call arguments. The
// un-emitted content itself lives in the demasker (returned on error as a
// fail-open fallback), so no masked text is stored here.
type choiceAccum struct {
	toolArguments map[int]*toolArgsAccum // key = toolCallIndex
}

// AppendToolArguments records a tool call arguments fragment and tracks JSON
// depth. Returns true if JSON just closed (depth is 0 and this chunk had a
// closing brace).
func (ca *choiceAccum) AppendToolArguments(toolCallIdx int, args string) bool {
	if ca.toolArguments == nil {
		ca.toolArguments = make(map[int]*toolArgsAccum)
	}

	acc, ok := ca.toolArguments[toolCallIdx]
	if !ok {
		acc = &toolArgsAccum{}
		ca.toolArguments[toolCallIdx] = acc
	}

	// Feed the fragment to the cross-fragment JSON tracker. It returns true
	// when the object closes: a complete object in one chunk ({"x":"y"}) or the
	// final chunk of a multi-chunk object. String state persists across chunks,
	// so an in-string '}' does not trigger a premature flush.
	return acc.jsonTrk.Feed(args)
}

// aggregator accumulates non-text deltas and metadata across SSE frames
// while Demaskers are buffering text content. The merged field acts as the
// canonical state; choicesAccum holds per-choice tool-args close-detection.
type aggregator struct {
	merged       llmchat.Chunk
	choicesAccum map[int]*choiceAccum // per-choice tool-args JSON-close tracking
}

func newAggregator() *aggregator {
	return &aggregator{
		choicesAccum: make(map[int]*choiceAccum),
	}
}

// findChoice returns a pointer to the merged choice with the given logical
// Index (the value carried in choice.Index on the wire), or (nil, false) if
// none is present. Callers must look choices up by Index rather than slice
// position: merged.Choices is built in arrival order, which only coincides with
// Index for the common single-choice / in-order case.
func (a *aggregator) findChoice(index int) (*llmchat.ChunkChoice, bool) {
	i := slices.IndexFunc(a.merged.Choices, func(c llmchat.ChunkChoice) bool { return c.Index == index })
	if i == -1 {
		return nil, false
	}
	return &a.merged.Choices[i], true
}

// getAccum returns (or lazily creates) the choiceAccum for the given index.
func (a *aggregator) getAccum(idx int) *choiceAccum {
	if acc, ok := a.choicesAccum[idx]; ok {
		return acc
	}
	acc := &choiceAccum{}
	a.choicesAccum[idx] = acc
	return acc
}

// merge integrates one incoming chunk into the aggregator's canonical state.
func (a *aggregator) merge(chunk llmchat.Chunk) {
	a.mergeMetadata(chunk)
	a.mergeUsage(chunk.Usage)
	for _, choice := range chunk.Choices {
		a.mergeChoice(choice)
	}
}

// mergeMetadata applies first-seen-wins for string fields and max for Created.
func (a *aggregator) mergeMetadata(chunk llmchat.Chunk) {
	if a.merged.ID == "" {
		a.merged.ID = chunk.ID
	}
	if a.merged.Object == "" {
		a.merged.Object = chunk.Object
	}
	if a.merged.Model == "" {
		a.merged.Model = chunk.Model
	}
	if a.merged.ServiceTier == "" {
		a.merged.ServiceTier = chunk.ServiceTier
	}
	if a.merged.SystemFingerprint == "" {
		a.merged.SystemFingerprint = chunk.SystemFingerprint
	}

	a.merged.Created = max(a.merged.Created, chunk.Created)
}

// mergeUsage applies per-field max for token counts.
func (a *aggregator) mergeUsage(u *llmchat.Usage) {
	if u == nil {
		return
	}
	if a.merged.Usage == nil {
		copied := *u
		a.merged.Usage = &copied
		return
	}

	a.merged.Usage.PromptTokens = max(a.merged.Usage.PromptTokens, u.PromptTokens)
	a.merged.Usage.CompletionTokens = max(a.merged.Usage.CompletionTokens, u.CompletionTokens)
	a.merged.Usage.TotalTokens = max(a.merged.Usage.TotalTokens, u.TotalTokens)
}

// mergeChoice integrates one ChunkChoice into the aggregator.
func (a *aggregator) mergeChoice(in llmchat.ChunkChoice) {
	choiceIdx := slices.IndexFunc(a.merged.Choices, func(c llmchat.ChunkChoice) bool { return c.Index == in.Index })
	if choiceIdx == -1 {
		// Text is emitted via demasked frames, never carried in the canonical
		// merged state — drop it here.
		in.Delta.Content = nil
		in.Delta.Reasoning = nil
		a.merged.Choices = append(a.merged.Choices, in)
		return
	}

	choice := &a.merged.Choices[choiceIdx]

	if in.Delta.Role != nil && choice.Delta.Role == nil {
		choice.Delta.Role = in.Delta.Role
	}
	if in.FinishReason != nil {
		choice.FinishReason = in.FinishReason
	}

	for _, inTc := range in.Delta.ToolCalls {
		idx := slices.IndexFunc(choice.Delta.ToolCalls, func(tc llmchat.ToolCallDelta) bool { return tc.Index == inTc.Index })
		if idx == -1 {
			// Not found — append a new one.
			choice.Delta.ToolCalls = append(choice.Delta.ToolCalls, inTc)
			continue
		}

		tc := &choice.Delta.ToolCalls[idx]

		if tc.ID == "" {
			tc.ID = inTc.ID
		}
		if tc.Type == "" {
			tc.Type = inTc.Type
		}
		if inTc.Function != nil {
			tc.Function = mergeFunctionCall(tc.Function, inTc.Function)
		}
	}

	if in.Delta.FunctionCall != nil {
		choice.Delta.FunctionCall = mergeFunctionCall(choice.Delta.FunctionCall, in.Delta.FunctionCall)
	}
}

// mergeFunctionCall applies first-seen name. Arguments are NOT concatenated here;
// they are accumulated in choiceAccum.toolArguments instead.
func mergeFunctionCall(dst *llmchat.FunctionCallDelta, in *llmchat.FunctionCallDelta) *llmchat.FunctionCallDelta {
	if dst == nil {
		return in
	}
	if in == nil {
		return dst
	}

	if dst.Name == "" {
		dst.Name = in.Name
	}

	// Do NOT concatenate arguments - they are accumulated in choiceAccum.toolArguments
	return dst
}

// marshalSseFrame encodes chunk as JSON and wraps it in "data: ...\n\n".
//
// It uses common.MarshalNoEscape (not json.Marshal) so that <, > and & are
// emitted literally rather than as \uXXXX escapes. This keeps placeholder-safe
// serialization identical across all three dialects (full-body, messages and
// responses paths all use MarshalNoEscape) and preserves byte-wise placeholder
// markers such as <EMAIL_1> for clients that scan for them.
func marshalSseFrame(chunk llmchat.Chunk) ([]byte, error) {
	data, err := common.MarshalNoEscape(chunk)
	if err != nil {
		return nil, fmt.Errorf("marshal chunk to JSON: %w", err)
	}
	out := make([]byte, 0, len(common.DataPrefix)+len(data)+2)
	out = append(out, common.DataPrefix...)
	out = append(out, data...)
	out = append(out, '\n', '\n')
	return out, nil
}
