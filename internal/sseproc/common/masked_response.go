package common

import "strings"

// MaskedResponseTextSource is optionally implemented by an SSE Processor to
// expose the accumulated pre-demask (placeholder-bearing) model response text
// at end-of-stream, for the audit trail. Callers type-assert it; processors
// that do not track it (e.g. the passthrough) simply do not implement it.
type MaskedResponseTextSource interface {
	MaskedResponseText() []string
}

// MaskedTextRecorder accumulates the masked (pre-demask) text of an SSE
// response, grouped by stream key and concatenated in first-seen order, so the
// full masked text of each content field can be recorded once at stream end.
// It tracks only what the caller feeds it — tool-call argument fragments are
// deliberately not recorded (masked_response_texts is the model's text output).
// The zero value is ready to use; it is single-goroutine (SSE processing runs
// on one goroutine per stream).
type MaskedTextRecorder struct {
	byKey map[string]*strings.Builder
	order []string
}

// Add appends a pre-demask text fragment for the given stream key. Empty
// fragments are ignored so buffering/flush no-ops do not create empty entries.
func (r *MaskedTextRecorder) Add(key, text string) {
	if text == "" {
		return
	}
	if r.byKey == nil {
		r.byKey = make(map[string]*strings.Builder)
	}
	b, ok := r.byKey[key]
	if !ok {
		b = &strings.Builder{}
		r.byKey[key] = b
		r.order = append(r.order, key)
	}
	b.WriteString(text)
}

// Texts returns the accumulated masked text of each stream in first-seen order.
// Returns nil when nothing was recorded.
func (r *MaskedTextRecorder) Texts() []string {
	if len(r.order) == 0 {
		return nil
	}
	out := make([]string, 0, len(r.order))
	for _, k := range r.order {
		if s := r.byKey[k].String(); s != "" {
			out = append(out, s)
		}
	}
	return out
}
