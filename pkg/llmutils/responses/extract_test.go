package responses_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/sjson"

	"github.com/cloud-ru-tech/guardrails-llm-filter/pkg/llmutils"
	"github.com/cloud-ru-tech/guardrails-llm-filter/pkg/llmutils/responses"
)

func TestExtractRequestContent(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		body    string
		want    []llmutils.ContentField
		wantErr error
	}{
		{
			name: "input string",
			body: `{"model":"gpt-x","input":"my mail is user@example.com"}`,
			want: []llmutils.ContentField{{Path: "input", Value: "my mail is user@example.com"}},
		},
		{
			name: "instructions only",
			body: `{"model":"gpt-x","instructions":"act as user@example.com"}`,
			want: []llmutils.ContentField{{Path: "instructions", Value: "act as user@example.com"}},
		},
		{
			name: "instructions and input string",
			body: `{"instructions":"be helpful","input":"hello"}`,
			want: []llmutils.ContentField{
				{Path: "instructions", Value: "be helpful"},
				{Path: "input", Value: "hello"},
			},
		},
		{
			name: "input array with string content",
			body: `{"input":[{"role":"user","content":"first"},{"role":"assistant","content":"second"}]}`,
			want: []llmutils.ContentField{
				{Path: "input.0.content", Value: "first"},
				{Path: "input.1.content", Value: "second"},
			},
		},
		{
			name: "input_text parts mixed with input_image keep real indices",
			body: `{"input":[{"role":"user","content":[
				{"type":"input_image","image_url":"https://x/img.png"},
				{"type":"input_text","text":"look at user@example.com"}]}]}`,
			want: []llmutils.ContentField{
				{Path: "input.0.content.1.text", Value: "look at user@example.com"},
			},
		},
		{
			name: "function_call_output string output",
			body: `{"input":[{"type":"function_call_output","call_id":"c1","output":"token=abc123"}]}`,
			want: []llmutils.ContentField{{Path: "input.0.output", Value: "token=abc123"}},
		},
		{
			name: "function_call_output content-array output",
			body: `{"input":[{"type":"function_call_output","call_id":"c1","output":[{"type":"output_text","text":"email a@b.com"},{"type":"input_text","text":"card 4111"}]}]}`,
			want: []llmutils.ContentField{
				{Path: "input.0.output.0.text", Value: "email a@b.com"},
				{Path: "input.0.output.1.text", Value: "card 4111"},
			},
		},
		{
			name: "assistant output_text replay is scanned (stateless multi-turn)",
			body: `{"input":[{"role":"assistant","content":[{"type":"output_text","text":"contact user@example.com"}]}]}`,
			want: []llmutils.ContentField{
				{Path: "input.0.content.0.text", Value: "contact user@example.com"},
			},
		},
		{
			name: "function_call arguments are scanned",
			body: `{"input":[{"type":"function_call","call_id":"c1","name":"f","arguments":"{\"email\":\"user@example.com\"}"}]}`,
			want: []llmutils.ContentField{
				{Path: "input.0.arguments", Value: `{"email":"user@example.com"}`},
			},
		},
		{
			name: "function_call empty arguments skipped",
			body: `{"input":[{"type":"function_call","call_id":"c1","name":"f","arguments":""}]}`,
			want: nil,
		},
		{
			name: "empty strings skipped",
			body: `{"instructions":"","input":""}`,
			want: nil,
		},
		{
			name:    "neither input nor instructions",
			body:    `{"model":"gpt-x","messages":[{"role":"user","content":"hi"}]}`,
			wantErr: llmutils.ErrUnsupportedBodySchema,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, err := responses.ExtractRequestContent([]byte(tt.body))
			if tt.wantErr != nil {
				require.ErrorIs(t, err, tt.wantErr)
				return
			}
			require.NoError(t, err)
			if tt.want == nil {
				assert.Empty(t, got)
				return
			}
			assert.Equal(t, tt.want, got)
		})
	}
}

// The returned paths must round-trip through sjson patching.
func TestExtractRequestContentPathsPatchable(t *testing.T) {
	t.Parallel()

	body := []byte(`{"instructions":"i","input":[{"role":"user","content":[{"type":"input_text","text":"secret"}]}]}`)
	fields, err := responses.ExtractRequestContent(body)
	require.NoError(t, err)
	require.Len(t, fields, 2)

	patched := body
	for _, f := range fields {
		patched, err = sjson.SetBytes(patched, f.Path, "<MASKED>")
		require.NoError(t, err)
	}
	assert.JSONEq(t,
		`{"instructions":"<MASKED>","input":[{"role":"user","content":[{"type":"input_text","text":"<MASKED>"}]}]}`,
		string(patched))
}
