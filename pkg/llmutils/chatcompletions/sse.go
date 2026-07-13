package chatcompletions

// ──────────────────────────────────────────────────────────────────────────────
// /v1/chat/completions  STREAMING RESPONSE  (SSE chunks)
// ──────────────────────────────────────────────────────────────────────────────
//
// When stream == true, the API sends a series of SSE lines:
//
//   data: {"id":"chatcmpl-...","object":"chat.completion.chunk",...}
//   data: {"id":"chatcmpl-...","object":"chat.completion.chunk",...}
//   data: [DONE]
//
// Parse by stripping the "data: " prefix from each line, then unmarshalling
// into Chunk.  Stop when you receive "data: [DONE]".

// Chunk is one SSE event from a streaming chat completion.
type Chunk struct {
	// ID matches the ID of every chunk in the same response stream.
	ID string `json:"id"`

	// Object is always "chat.completion.chunk".
	Object string `json:"object"`

	// Created is the Unix timestamp of stream start (same across all chunks).
	Created int64 `json:"created"`

	// Model echoes the model used.
	Model string `json:"model"`

	// ServiceTier echoes the service tier.
	ServiceTier string `json:"service_tier,omitempty"`

	// SystemFingerprint echoes the backend fingerprint.
	SystemFingerprint string `json:"system_fingerprint,omitempty"`

	// Choices carries the incremental delta(s). Empty in the final usage chunk.
	Choices []ChunkChoice `json:"choices"`

	// Usage is only present in the last chunk before [DONE] when
	// stream_options.include_usage is true. All other chunks have null here.
	Usage *Usage `json:"usage,omitempty"`
}

// ChunkChoice is one incremental update inside a streaming chunk.
type ChunkChoice struct {
	// Index identifies which completion choice this delta belongs to.
	Index int `json:"index"`

	// Delta carries the incremental content update for this chunk.
	Delta Delta `json:"delta"`

	// FinishReason is null on intermediate chunks and populated on the final
	// chunk for this choice with the same values as in non-streaming responses.
	FinishReason *string `json:"finish_reason"`
}

// Delta is the partial message content in a streaming chunk.
// On the very first chunk the Role is sent; subsequent chunks typically have
// only Content (and leave Role empty).
type Delta struct {
	// Role is "assistant" on the first chunk for each choice, empty thereafter.
	Role *string `json:"role,omitempty"`

	// Content is the incremental text fragment. Empty string means no text in
	// this delta; null means streaming has concluded for this field.
	Content *string `json:"content,omitempty"`

	Reasoning *string `json:"reasoning,omitempty"`

	// Refusal is an incremental refusal text fragment.
	// Refusal *string `json:"refusal,omitempty"`

	// ToolCalls carries incremental tool-call updates.
	// Each entry uses the same Index to identify which parallel call it extends.
	ToolCalls []ToolCallDelta `json:"tool_calls,omitempty"`

	// FunctionCall is the deprecated streaming equivalent of ToolCalls.
	FunctionCall *FunctionCallDelta `json:"function_call,omitempty"`
}

// ToolCallDelta is the streaming analogue of ToolCall; fields are built up
// across multiple chunks.
type ToolCallDelta struct {
	// Index identifies which concurrent tool call this delta belongs to.
	Index int `json:"index"`

	// ID is set only on the first delta for this tool call.
	ID string `json:"id,omitempty"`

	// Type is set only on the first delta: "function".
	Type string `json:"type,omitempty"`

	// Function carries incremental function name / arguments fragments.
	Function *FunctionCallDelta `json:"function,omitempty"`
}

// FunctionCallDelta is an incremental fragment of a function / tool call.
type FunctionCallDelta struct {
	// Name is the function name (sent in the first chunk only).
	Name string `json:"name,omitempty"`

	// Arguments is an incremental JSON fragment. Concatenate across all chunks
	// to obtain the complete argument JSON string.
	Arguments string `json:"arguments,omitempty"`
}

// Usage contains token-consumption statistics returned on every response.
type Usage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	TotalTokens      int `json:"total_tokens"`
}

// Copy returns a deep copy of Usage.
func (u Usage) Copy() Usage {
	return u
}

func (u *Usage) CopyPtr() *Usage {
	if u == nil {
		return nil
	}
	newU := u.Copy()
	return &newU
}

// Copy returns a deep copy of FunctionCallDelta.
func (f FunctionCallDelta) Copy() FunctionCallDelta {
	return f
}

func (f *FunctionCallDelta) CopyPtr() *FunctionCallDelta {
	if f == nil {
		return nil
	}
	newF := f.Copy()
	return &newF
}

// Copy returns a deep copy of ToolCallDelta.
func (t ToolCallDelta) Copy() ToolCallDelta {
	t.Function = t.Function.CopyPtr()
	return t
}

func (t *ToolCallDelta) CopyPtr() *ToolCallDelta {
	if t == nil {
		return nil
	}
	newT := t.Copy()
	return &newT
}

// Copy returns a deep copy of Delta.
func (c Delta) Copy() Delta {
	toolCalls := make([]ToolCallDelta, len(c.ToolCalls))
	for i, tc := range c.ToolCalls {
		toolCalls[i] = tc.Copy()
	}
	return Delta{
		Role:         copyString(c.Role),
		Content:      copyString(c.Content),
		Reasoning:    copyString(c.Reasoning),
		ToolCalls:    toolCalls,
		FunctionCall: c.FunctionCall.CopyPtr(),
	}
}

func (c *Delta) CopyPtr() *Delta {
	if c == nil {
		return nil
	}
	newC := c.Copy()
	return &newC
}

// Copy returns a deep copy of ChunkChoice.
func (c ChunkChoice) Copy() ChunkChoice {
	return ChunkChoice{
		Index:        c.Index,
		Delta:        c.Delta.Copy(),
		FinishReason: copyString(c.FinishReason),
	}
}

func (c *ChunkChoice) CopyPtr() *ChunkChoice {
	if c == nil {
		return nil
	}
	newC := c.Copy()
	return &newC
}

// Copy returns a deep copy of Chunk.
func (c Chunk) Copy() Chunk {
	choices := make([]ChunkChoice, len(c.Choices))
	for i, choice := range c.Choices {
		choices[i] = choice.Copy()
	}

	c.Choices = choices
	c.Usage = c.Usage.CopyPtr()

	return c
}

func (c *Chunk) CopyPtr() *Chunk {
	if c == nil {
		return nil
	}
	newC := c.Copy()
	return &newC
}

func copyString(s *string) *string {
	if s == nil {
		return nil
	}
	copied := *s
	return &copied
}
