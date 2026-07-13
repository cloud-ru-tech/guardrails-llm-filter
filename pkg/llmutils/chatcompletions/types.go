// Package chatcompletions models the OpenAI /v1/chat/completions wire format
// (non-streaming and SSE) and extracts the scannable/demaskable text fields
// from its request and response bodies.
package chatcompletions

// ──────────────────────────────────────────────────────────────────────────────
// /v1/chat/completions  NON-STREAMING RESPONSE
// ──────────────────────────────────────────────────────────────────────────────

// Response is the JSON response for a non-streaming
// POST /v1/chat/completions call.
type Response struct {
	// ID is the unique identifier for this completion, e.g. "chatcmpl-abc123".
	ID string `json:"id"`

	// Object is always "chat.completion".
	Object string `json:"object"`

	// Created is the Unix timestamp (seconds) when the completion was created.
	Created int64 `json:"created"`

	// Model is the exact model snapshot that served the request,
	// e.g. "gpt-4o-2024-08-06".
	Model string `json:"model"`

	// Choices is the list of generated completions. Has N elements where N is
	// the n request parameter (usually 1).
	Choices []Choice `json:"choices"`

	// Usage contains token consumption statistics for this request.
	Usage Usage `json:"usage"`
}

// Choice is one generated completion option.
type Choice struct {
	// Index is the 0-based position of this choice in the choices array.
	Index int `json:"index"`

	// Message is the generated assistant message for this choice.
	Message Message `json:"message"`

	// FinishReason explains why generation stopped:
	//   "stop"           – natural stop or stop sequence reached
	//   "length"         – max_tokens limit reached
	//   "tool_calls"     – model wants to call a tool
	//   "content_filter" – output omitted by content policy
	//   "function_call"  – deprecated; model called a function
	FinishReason string `json:"finish_reason"`
}

// Message is the assistant turn returned inside a Choice.
type Message struct {
	// Role is always "assistant" on output.
	Role string `json:"role"`

	// Content is the text of the reply. Null when the model issued tool_calls
	// with no accompanying text.
	Content *string `json:"content"`

	ReasoningContent *string `json:"reasoning_content,omitempty"`

	Reasoning *string `json:"reasoning,omitempty"`

	// ToolCalls is a list of tool calls the model wants to make.
	ToolCalls []ToolCall `json:"tool_calls,omitempty"`

	// FunctionCall is the deprecated equivalent of ToolCalls.
	FunctionCall *Function `json:"function_call,omitempty"`
}

// ToolCall represents a tool call in a chat completion response.
type ToolCall struct {
	// Index is meaningful only in streaming (SSE) delta chunks. Non-streaming
	// responses often omit it entirely, so we use a pointer with omitempty to
	// avoid injecting "index":0 into responses that never had the field.
	Index *int `json:"index,omitempty"`

	// ID is the unique identifier for this tool call.
	ID string `json:"id"`

	// Type is the type of the tool call, typically "function".
	Type string `json:"type"`

	// Function contains the function name and arguments.
	Function Function `json:"function"`
}

// Function represents a function call within a tool call.
type Function struct {
	// Name is the name of the function to call.
	Name string `json:"name"`

	// Arguments is kept as json.RawMessage for a precise reason: the OpenAI
	// wire format encodes this field as a JSON string whose *content* is a
	// JSON object. If we decode it into a plain Go string and then re-encode
	// it, json.Marshal will HTML-escape angle brackets and ampersands, silently
	// corrupting synthetic placeholders like <EMAIL_1>. RawMessage tells the
	// encoder to write the bytes back verbatim, no re-interpretation.
	Arguments string `json:"arguments"`
}
