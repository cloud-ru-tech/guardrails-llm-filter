package chatcompletions

import (
	"testing"

	"github.com/cloud-ru-tech/guardrails-llm-filter/pkg/llmutils"
)

func TestExtractResponseContent(t *testing.T) {
	tests := []struct {
		name  string
		input []byte
		want  []llmutils.ContentField
	}{
		// /v1/chat/completions — message.content
		{
			name: "chat completion single choice",
			input: []byte(`{
				"id": "chatcmpl-123",
				"object": "chat.completion",
				"choices": [{
					"index": 0,
					"message": {"role": "assistant", "content": "Hello, how can I help?"},
					"finish_reason": "stop"
				}]
			}`),
			want: []llmutils.ContentField{{Path: "choices.0.message.content", Value: "Hello, how can I help?"}},
		},
		{
			name: "chat completion multiple choices",
			input: []byte(`{
				"choices": [
					{"message": {"content": "First response"}},
					{"message": {"content": "Second response"}}
				]
			}`),
			want: []llmutils.ContentField{
				{Path: "choices.0.message.content", Value: "First response"},
				{Path: "choices.1.message.content", Value: "Second response"},
			},
		},
		{
			name: "chat completion null content skipped",
			input: []byte(`{
				"choices": [{"message": {"role": "assistant", "content": null}}]
			}`),
			want: nil,
		},
		{
			name: "chat completion empty content skipped",
			input: []byte(`{
				"choices": [{"message": {"content": ""}}]
			}`),
			want: nil,
		},

		// /v1/chat/completions — reasoning_content (CoT models)
		{
			name: "reasoning only",
			input: []byte(`{
				"choices": [{
					"message": {
						"content": null,
						"reasoning": "The user is asking for a password. Must refuse."
					}
				}]
			}`),
			want: []llmutils.ContentField{
				{Path: "choices.0.message.reasoning", Value: "The user is asking for a password. Must refuse."},
			},
		},
		{
			name: "reasoning_content only",
			input: []byte(`{
				"choices": [{
					"message": {
						"content": null,
						"reasoning_content": "Let me think about this step by step..."
					}
				}]
			}`),
			want: []llmutils.ContentField{
				{Path: "choices.0.message.reasoning_content", Value: "Let me think about this step by step..."},
			},
		},
		{
			name: "content and reasoning_content both present",
			input: []byte(`{
				"choices": [{
					"message": {
						"content": "The answer is 42.",
						"reasoning_content": "I calculated this by..."
					}
				}]
			}`),
			want: []llmutils.ContentField{
				{Path: "choices.0.message.content", Value: "The answer is 42."},
				{Path: "choices.0.message.reasoning_content", Value: "I calculated this by..."},
			},
		},
		{
			name: "empty reasoning_content skipped",
			input: []byte(`{
				"choices": [{"message": {"content": "Hello", "reasoning_content": ""}}]
			}`),
			want: []llmutils.ContentField{
				{Path: "choices.0.message.content", Value: "Hello"},
			},
		},

		// Edge cases
		{
			name:  "empty choices array",
			input: []byte(`{"choices": []}`),
			want:  nil,
		},
		{
			name:  "no choices field",
			input: []byte(`{"id": "123", "model": "gpt-4"}`),
			want:  nil,
		},
		{
			name:  "empty JSON object",
			input: []byte(`{}`),
			want:  nil,
		},
		{
			name: "real chat completions example from GLM-4.6",
			input: []byte(`{
				"id": "chatcmpl-18240ca2",
				"object": "chat.completion",
				"choices": [{
					"index": 0,
					"message": {
						"role": "assistant",
						"content": "Я языковая модель GLM.",
						"reasoning_content": null
					},
					"finish_reason": "stop"
				}],
				"usage": {"prompt_tokens": 14, "completion_tokens": 95}
			}`),
			want: []llmutils.ContentField{{Path: "choices.0.message.content", Value: "Я языковая модель GLM."}},
		},
		{
			name: "SSE-style chunk with delta — not extracted (delta field ignored)",
			input: []byte(`{
				"object": "chat.completion.chunk",
				"choices": [{"index": 0, "delta": {"content": "1"}}]
			}`),
			// delta.content is intentionally not extracted — use DemaskBuffer for SSE
			want: nil,
		},
		{
			name: "unicode content preserved",
			input: []byte(`{
				"choices": [{"message": {"content": "Hello 世界 🌍"}}]
			}`),
			want: []llmutils.ContentField{{Path: "choices.0.message.content", Value: "Hello 世界 🌍"}},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ExtractResponseContent(tt.input)

			if len(got) != len(tt.want) {
				t.Errorf("ExtractResponseContent() returned %d fields, want %d\ngot:  %+v\nwant: %+v",
					len(got), len(tt.want), got, tt.want)
				return
			}

			for i, f := range got {
				if f.Path != tt.want[i].Path {
					t.Errorf("field[%d].Path = %q, want %q", i, f.Path, tt.want[i].Path)
				}
				if f.Value != tt.want[i].Value {
					t.Errorf("field[%d].Value = %q, want %q", i, f.Value, tt.want[i].Value)
				}
			}
		})
	}
}
