package chatcompletions

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/cloud-ru-tech/guardrails-llm-filter/pkg/llmutils"
)

func TestExtractRequestContent(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		body      string
		want      []llmutils.ContentField
		wantError error
	}{
		{
			name: "chat extracts all message roles with string content",
			body: `{
				"messages":[
					{"role":"system","content":"system secret"},
					{"role":"user","content":"user secret"},
					{"role":"assistant","content":"assistant secret"},
					{"role":"tool","content":"tool secret"},
					{"role":"function","content":"function secret"}
				]
			}`,
			want: []llmutils.ContentField{
				{Path: "messages.0.content", Value: "system secret"},
				{Path: "messages.1.content", Value: "user secret"},
				{Path: "messages.2.content", Value: "assistant secret"},
				{Path: "messages.3.content", Value: "tool secret"},
				{Path: "messages.4.content", Value: "function secret"},
			},
		},
		{
			name: "chat extracts text parts only",
			body: `{
				"messages":[
					{
						"role":"user",
						"content":[
							{"type":"text","text":"part one"},
							{"type":"input_image","image_url":"https://example.com/x.png"},
							{"type":"text","text":"part two"}
						]
					}
				]
			}`,
			want: []llmutils.ContentField{
				{Path: "messages.0.content.0.text", Value: "part one"},
				{Path: "messages.0.content.2.text", Value: "part two"},
			},
		},
		{
			name: "chat extracts tool call arguments",
			body: `{
				"messages":[
					{
						"role":"assistant",
						"tool_calls":[
							{
								"id":"call_1",
								"type":"function",
								"function":{
									"name":"read_file",
									"arguments":"{\"path\":\"/tmp/.env\"}"
								}
							},
							{
								"id":"call_2",
								"type":"function",
								"function":{
									"name":"run_cmd",
									"arguments":"{\"cmd\":\"cat ~/.npmrc\"}"
								}
							}
						]
					}
				]
			}`,
			want: []llmutils.ContentField{
				{Path: "messages.0.tool_calls.0.function.arguments", Value: `{"path":"/tmp/.env"}`},
				{Path: "messages.0.tool_calls.1.function.arguments", Value: `{"cmd":"cat ~/.npmrc"}`},
			},
		},
		{
			name: "chat extracts deprecated function call arguments",
			body: `{
				"messages":[
					{
						"role":"assistant",
						"function_call":{
							"name":"read_file",
							"arguments":"{\"path\":\"/tmp/.env\"}"
						}
					}
				]
			}`,
			want: []llmutils.ContentField{{
				Path:  "messages.0.function_call.arguments",
				Value: `{"path":"/tmp/.env"}`,
			}},
		},
		{
			name: "chat preserves real message indexes",
			body: `{
				"messages":[
					{"role":"user","content":null},
					{"role":"assistant","tool_calls":[{"id":"call_1","type":"function","function":{"name":"read_file","arguments":"{\"path\":\"/tmp/a\"}"}}]},
					{"role":"tool","content":"tool output"}
				]
			}`,
			want: []llmutils.ContentField{
				{Path: "messages.1.tool_calls.0.function.arguments", Value: `{"path":"/tmp/a"}`},
				{Path: "messages.2.content", Value: "tool output"},
			},
		},
		{
			name:      "unsupported schema",
			body:      `{"model":"gpt-4o-mini"}`,
			wantError: llmutils.ErrUnsupportedBodySchema,
		},
		{
			// Legacy /v1/completions prompt-style body is unsupported: no
			// messages array → llmutils.ErrUnsupportedBodySchema (fail-open passthrough).
			name:      "legacy completions prompt body unsupported",
			body:      `{"model":"gpt-3.5-turbo-instruct","prompt":"my email is a@b.com"}`,
			wantError: llmutils.ErrUnsupportedBodySchema,
		},
		{
			name: "chat extracts tool definitions before messages",
			// A tool definition is prompt text the model reads on every turn.
			// Extracting it first keeps its placeholder numbers independent of
			// the conversation length.
			body: `{
				"messages":[{"role":"user","content":"user secret"}],
				"tools":[
					{
						"type":"function",
						"function":{
							"name":"notify",
							"description":"Notify a@b.com",
							"parameters":{
								"type":"object",
								"properties":{
									"to":{"type":"string","default":"a@b.com","description":"recipient"}
								},
								"required":["to"]
							}
						}
					}
				]
			}`,
			want: []llmutils.ContentField{
				{Path: "tools.0.function.description", Value: "Notify a@b.com"},
				{Path: "tools.0.function.parameters.properties.to.default", Value: "a@b.com"},
				{Path: "tools.0.function.parameters.properties.to.description", Value: "recipient"},
				{Path: "messages.0.content", Value: "user secret"},
			},
		},
		{
			name: "chat extracts legacy top-level function definitions",
			body: `{
				"messages":[{"role":"user","content":"hi"}],
				"functions":[{"name":"notify","description":"Notify a@b.com"}]
			}`,
			want: []llmutils.ContentField{
				{Path: "functions.0.description", Value: "Notify a@b.com"},
				{Path: "messages.0.content", Value: "hi"},
			},
		},
		{
			name: "chat never extracts a tool name",
			// The model echoes the name back in tool_calls[].function.name, which
			// is not demasked — masking it would break tool calling.
			body: `{
				"messages":[{"role":"user","content":"hi"}],
				"tools":[{"type":"function","function":{"name":"a@b.com"}}]
			}`,
			want: []llmutils.ContentField{
				{Path: "messages.0.content", Value: "hi"},
			},
		},
		{
			name: "no extractable fields returns nil",
			body: `{
				"messages":[
					{"role":"user","content":null},
					{"role":"assistant","tool_calls":[{"id":"call_1","type":"function","function":{"name":"read_file","arguments":""}}]}
				]
			}`,
			want: nil,
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, err := ExtractRequestContent([]byte(tt.body))
			if tt.wantError != nil {
				require.ErrorIs(t, err, tt.wantError)
				return
			}

			require.NoError(t, err)
			require.Equal(t, tt.want, got)
		})
	}
}
