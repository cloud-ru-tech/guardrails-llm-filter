package sseproc

import (
	"context"
)

// passthroughProcessor forwards every chunk unchanged. It is the fail-open
// processor for streams whose API format is unknown: routing such a stream
// into a dialect processor risks worse than not demasking — e.g. the
// chat-completions processor treats named-event frames as passthrough
// per-frame but still buffers and reorders, and a future dialect could be
// mangled outright. Unknown format means we cannot demask safely, so the
// stream passes through byte-identical (same policy as the full-body path).
type passthroughProcessor struct{}

func (passthroughProcessor) ProcessChunk(_ context.Context, body []byte, _ bool) ([]byte, error) {
	return body, nil
}
