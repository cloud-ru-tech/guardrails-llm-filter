package common

import "context"

// Demasker replaces synthetic tokens with original values in streaming chunks.
//
// DemaskChunk returns the demasked safe prefix and buffers a tail internally
// until it can be resolved (returning "" while buffering). On error it returns
// a non-nil error together with the un-emitted content it was holding
// (previously-buffered tail + the current chunk, with any unresolved
// placeholders left intact): callers MUST emit that string as a fail-open,
// lossless fallback so the stream tail is never dropped, then stop demasking
// that field stream.
type Demasker interface {
	DemaskChunk(ctx context.Context, chunk string, flush bool) (string, error)
}

// DemaskerFactoryFn creates a fresh Demasker instance for a single field stream.
type DemaskerFactoryFn func() Demasker
