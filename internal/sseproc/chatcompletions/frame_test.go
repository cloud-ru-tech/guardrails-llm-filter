package chatcompletions

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestClassifyFrame(t *testing.T) {
	tests := []struct {
		name        string
		frame       string
		wantKind    frameKind
		wantPayload string
	}{
		{"data with space", "data: {\"a\":1}\n\n", frameData, `{"a":1}`},
		// Regression: SSE spec makes the space after "data:" optional; some
		// OpenAI-compatible backends omit it. Without optional-space handling the
		// frame classified as passthrough and its placeholders leaked undemasked.
		{"data without space", "data:{\"a\":1}\n\n", frameData, `{"a":1}`},
		{"done with space", "data: [DONE]\n\n", frameDone, ""},
		{"done without space", "data:[DONE]\n\n", frameDone, ""},
		{"crlf terminated", "data: {\"a\":1}\r\n\r\n", frameData, `{"a":1}`},
		{"comment", ": keep-alive\n\n", framePassthrough, ""},
		{"empty", "\n\n", framePassthrough, ""},
		{"non-data line", "event: ping\n\n", framePassthrough, ""},
		// Regression: a data line preceded by a comment or event line in the SAME
		// event (no blank line between) must still be classified as data. A
		// whole-frame prefix check would misclassify these as passthrough and leak
		// the placeholders to the client undemasked.
		{"comment then data same frame", ": keep-alive\ndata: {\"a\":1}\n\n", frameData, `{"a":1}`},
		{"event then data same frame", "event: message\ndata: {\"a\":1}\n\n", frameData, `{"a":1}`},
		{"crlf comment then data", ": ka\r\ndata: {\"a\":1}\r\n\r\n", frameData, `{"a":1}`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			kind, payload := classifyFrame([]byte(tt.frame))
			assert.Equal(t, tt.wantKind, kind)
			assert.Equal(t, tt.wantPayload, string(payload))
		})
	}
}
