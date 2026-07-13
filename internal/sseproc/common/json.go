package common

import (
	"bytes"
	"encoding/json"
	"fmt"
)

// MarshalNoEscape marshals v to JSON without HTML-escaping <, >, or &.
//
// The standard json.Marshal unconditionally replaces those characters with
// their \uXXXX equivalents "for safety in HTML contexts". That is harmless in a
// browser but wrong here: the output is an API response consumed by a code
// agent, and rewriting e.g. <EMAIL_1> into <EMAIL_1> would stop the
// client from recognising the synthetic placeholders this service emits.
//
// This is the single source of truth for placeholder-safe marshaling shared by
// the response-body handlers and the SSE processors.
func MarshalNoEscape(v any) ([]byte, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		return nil, fmt.Errorf("marshal: %w", err)
	}
	// json.Encoder.Encode appends a trailing newline; strip it so the output is
	// byte-for-byte what json.Marshal would produce, minus the HTML escaping.
	return bytes.TrimRight(buf.Bytes(), "\n"), nil
}
