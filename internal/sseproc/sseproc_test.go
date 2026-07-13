package sseproc

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/cloud-ru-tech/guardrails-llm-filter/internal/models"
	"github.com/cloud-ru-tech/guardrails-llm-filter/internal/sseproc/chatcompletions"
	"github.com/cloud-ru-tech/guardrails-llm-filter/internal/sseproc/common"
	"github.com/cloud-ru-tech/guardrails-llm-filter/internal/sseproc/messages"
	"github.com/cloud-ru-tech/guardrails-llm-filter/internal/sseproc/responses"
)

// Every APIFormat constant must map to its intended processor: a named-event
// stream silently routed through the chatcompletions default would forward
// placeholders undemasked.
func TestNewForFormatExhaustive(t *testing.T) {
	t.Parallel()
	factory := common.DemaskerFactoryFn(func() common.Demasker { return nil })

	tests := []struct {
		format models.APIFormat
		want   string
	}{
		{models.APIFormatChatCompletions, fmt.Sprintf("%T", chatcompletions.New(factory))},
		{models.APIFormatMessages, fmt.Sprintf("%T", messages.New(factory))},
		{models.APIFormatResponses, fmt.Sprintf("%T", responses.New(factory))},
	}
	for _, tt := range tests {
		got := NewForFormat(tt.format, factory, factory, false)
		assert.Equal(t, tt.want, fmt.Sprintf("%T", got), string(tt.format))
	}

	// Unknown/empty formats (pre-Format persisted state, future dialects) get
	// the fail-open passthrough processor: routing them into a dialect
	// processor risks mangling the stream, and the full-body path passes the
	// same condition through unchanged.
	for _, format := range []models.APIFormat{"bogus", ""} {
		got := NewForFormat(format, factory, factory, false)
		assert.IsType(t, passthroughProcessor{}, got, string(format))

		// The stream must come back byte-identical, chunk by chunk.
		in := []byte("event: content_block_delta\ndata: {\"delta\":{\"text\":\"<EMAIL_1>\"}}\n\n")
		out, err := got.ProcessChunk(t.Context(), in, false)
		assert.NoError(t, err)
		assert.Equal(t, in, out)
	}
}
