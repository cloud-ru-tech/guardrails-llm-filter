package common

import "context"

// Processor is the SSE stream processor contract: each Envoy body chunk
// goes in via ProcessChunk and any bytes ready for the client come out.
// A nil (or empty) result with endOfStream == false means the processor
// is still buffering and the caller should not forward a body mutation.
//
// Both the OpenAI chat-completions and Anthropic Messages processors
// satisfy this interface; the top-level sseproc.NewForPath returns one of
// them based on the request path.
type Processor interface {
	ProcessChunk(ctx context.Context, body []byte, endOfStream bool) ([]byte, error)
}
