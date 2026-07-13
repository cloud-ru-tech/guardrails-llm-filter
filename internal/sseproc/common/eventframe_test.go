package common

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestClassifyFrame(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		frame       string
		wantKind    FrameKind
		wantEvent   string
		wantPayload string
	}{
		{
			name:        "canonical data line with space",
			frame:       "event: x\ndata: {\"a\":1}\n\n",
			wantKind:    FrameEvent,
			wantEvent:   "x",
			wantPayload: `{"a":1}`,
		},
		{
			// SSE-legal frame without the space after "data:" — emitted by some
			// OpenAI-compatible backends. Must still be recognized so placeholders
			// are demasked instead of passing through raw.
			name:        "data line without space",
			frame:       "event: x\ndata:{\"a\":1}\n\n",
			wantKind:    FrameEvent,
			wantEvent:   "x",
			wantPayload: `{"a":1}`,
		},
		{
			name:     "done sentinel with space",
			frame:    "data: [DONE]\n\n",
			wantKind: FrameDone,
		},
		{
			name:     "done sentinel without space",
			frame:    "data:[DONE]\n\n",
			wantKind: FrameDone,
		},
		{
			name:     "comment line passes through",
			frame:    ": keep-alive\n\n",
			wantKind: FramePassthrough,
		},
		{
			// SSE spec: multiple data: lines in one event are joined with "\n"
			// to reconstruct the payload. Keeping only the last line would
			// truncate the JSON and leak placeholders via the passthrough path.
			name:        "multi-line data joined with newline",
			frame:       "event: x\ndata: line1\ndata: line2\n\n",
			wantKind:    FrameEvent,
			wantEvent:   "x",
			wantPayload: "line1\nline2",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			pf := ClassifyFrame([]byte(tt.frame))
			assert.Equal(t, tt.wantKind, pf.Kind)
			if tt.wantKind == FrameEvent {
				assert.Equal(t, tt.wantEvent, string(pf.Event))
				assert.Equal(t, tt.wantPayload, string(pf.Data))
			}
		})
	}
}
