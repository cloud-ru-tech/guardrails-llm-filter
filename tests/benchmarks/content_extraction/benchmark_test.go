package contentextraction

import (
	"encoding/json"
	"fmt"
	"strconv"
	"testing"

	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"

	"github.com/cloud-ru-tech/guardrails-llm-filter/pkg/llmutils/chatcompletions"
)

// Test data for various response scenarios
var testCases = map[string][]byte{
	"single_choice_content": []byte(`{
		"id": "chatcmpl-123",
		"object": "chat.completion",
		"created": 1699000000,
		"model": "gpt-4",
		"choices": [{
			"index": 0,
			"message": {
				"role": "assistant",
				"content": "This is a simple response with content only."
			},
			"finish_reason": "stop"
		}],
		"usage": {
			"prompt_tokens": 10,
			"completion_tokens": 20,
			"total_tokens": 30
		}
	}`),

	"single_choice_reasoning_content": []byte(`{
		"id": "chatcmpl-123",
		"object": "chat.completion",
		"created": 1699000000,
		"model": "gpt-4",
		"choices": [{
			"index": 0,
			"message": {
				"role": "assistant",
				"content": "The answer is 42.",
				"reasoning_content": "Let me think about this step by step. First, I need to understand the question..."
			},
			"finish_reason": "stop"
		}],
		"usage": {
			"prompt_tokens": 10,
			"completion_tokens": 50,
			"total_tokens": 60
		}
	}`),

	"single_choice_content_one_tool_call": []byte(`{
		"id": "chatcmpl-123",
		"object": "chat.completion",
		"created": 1699000000,
		"model": "gpt-4",
		"choices": [{
			"index": 0,
			"message": {
				"role": "assistant",
				"content": "I'll help you with that.",
				"tool_calls": [{
					"index": 0,
					"id": "call_abc123",
					"type": "function",
					"function": {
						"name": "get_weather",
						"arguments": "{\"location\": \"New York\", \"unit\": \"celsius\"}"
					}
				}]
			},
			"finish_reason": "tool_calls"
		}],
		"usage": {
			"prompt_tokens": 15,
			"completion_tokens": 30,
			"total_tokens": 45
		}
	}`),

	"single_choice_reasoning_content_multiple_tool_calls": []byte(`{
		"id": "chatcmpl-123",
		"object": "chat.completion",
		"created": 1699000000,
		"model": "gpt-4",
		"choices": [{
			"index": 0,
			"message": {
				"role": "assistant",
				"content": "I need to get some information for you.",
				"reasoning_content": "The user is asking for weather data. I should call the weather API for multiple locations.",
				"tool_calls": [{
					"index": 0,
					"id": "call_abc123",
					"type": "function",
					"function": {
						"name": "get_weather",
						"arguments": "{\"location\": \"New York\", \"unit\": \"celsius\"}"
					}
				}, {
					"index": 1,
					"id": "call_def456",
					"type": "function",
					"function": {
						"name": "get_weather",
						"arguments": "{\"location\": \"London\", \"unit\": \"celsius\"}"
					}
				}, {
					"index": 2,
					"id": "call_ghi789",
					"type": "function",
					"function": {
						"name": "get_weather",
						"arguments": "{\"location\": \"Tokyo\", \"unit\": \"celsius\"}"
					}
				}]
			},
			"finish_reason": "tool_calls"
		}],
		"usage": {
			"prompt_tokens": 20,
			"completion_tokens": 80,
			"total_tokens": 100
		}
	}`),

	"two_choices_content": []byte(`{
		"id": "chatcmpl-123",
		"object": "chat.completion",
		"created": 1699000000,
		"model": "gpt-4",
		"choices": [{
			"index": 0,
			"message": {
				"role": "assistant",
				"content": "This is the first response option."
			},
			"finish_reason": "stop"
		}, {
			"index": 1,
			"message": {
				"role": "assistant",
				"content": "This is the second response option."
			},
			"finish_reason": "stop"
		}],
		"usage": {
			"prompt_tokens": 10,
			"completion_tokens": 40,
			"total_tokens": 50
		}
	}`),

	"two_choices_reasoning_content_tool_calls": []byte(`{
		"id": "chatcmpl-123",
		"object": "chat.completion",
		"created": 1699000000,
		"model": "gpt-4",
		"choices": [{
			"index": 0,
			"message": {
				"role": "assistant",
				"content": "First response with tool call.",
				"reasoning_content": "I need to call the search function.",
				"tool_calls": [{
					"index": 0,
					"id": "call_abc123",
					"type": "function",
					"function": {
						"name": "search",
						"arguments": "{\"query\": \"test\"}"
					}
				}]
			},
			"finish_reason": "tool_calls"
		}, {
			"index": 1,
			"message": {
				"role": "assistant",
				"content": "Second response with different tool call.",
				"reasoning_content": "I need to call the database function.",
				"tool_calls": [{
					"index": 0,
					"id": "call_def456",
					"type": "function",
					"function": {
						"name": "query_db",
						"arguments": "{\"table\": \"users\", \"limit\": 10}"
					}
				}]
			},
			"finish_reason": "tool_calls"
		}],
		"usage": {
			"prompt_tokens": 15,
			"completion_tokens": 100,
			"total_tokens": 115
		}
	}`),

	"large_content": []byte(`{
		"id": "chatcmpl-123",
		"object": "chat.completion",
		"created": 1699000000,
		"model": "gpt-4",
		"choices": [{
			"index": 0,
			"message": {
				"role": "assistant",
				"content": "` + generateLargeContent(5000) + `"
			},
			"finish_reason": "stop"
		}],
		"usage": {
			"prompt_tokens": 10,
			"completion_tokens": 5000,
			"total_tokens": 5010
		}
	}`),

	"large_reasoning_content": []byte(`{
		"id": "chatcmpl-123",
		"object": "chat.completion",
		"created": 1699000000,
		"model": "gpt-4",
		"choices": [{
			"index": 0,
			"message": {
				"role": "assistant",
				"content": "The final answer is 42.",
				"reasoning_content": "` + generateLargeContent(3000) + `"
			},
			"finish_reason": "stop"
		}],
		"usage": {
			"prompt_tokens": 10,
			"completion_tokens": 3000,
			"total_tokens": 3010
		}
	}`),
}

// generateLargeContent generates a large string for testing
func generateLargeContent(chars int) string {
	result := make([]byte, chars)
	for i := 0; i < chars; i++ {
		result[i] = byte('a' + (i % 26))
	}
	return string(result)
}

// demaskContent simulates demasking by replacing content with a modified version
func demaskContent(content string) string {
	// Simulate demasking by adding a prefix
	return "[DEMASKED] " + content
}

// demaskToolCallArguments simulates demasking tool call arguments
func demaskToolCallArguments(args string) string {
	// Simulate demasking by replacing sensitive data
	return `{"location": "[DEMASKED]", "unit": "celsius"}`
}

// ============================================================================
// Approach 1: gjson + sjson (current implementation)
// ============================================================================

// ContentField is a mutable text field in an OpenAI-compatible JSON body.
// Path is a sjson-compatible dotted path for patching back into the body.
// Used for both request extraction (ExtractRequestContent) and response extraction (ExtractResponseContent).
type ContentField struct {
	Path  string // e.g. "messages.1.content", "prompt", "choices.0.message.content"
	Value string
}

// extractResponseGjsonSjson extracts all non-empty text content fields from an
// OpenAI-compatible response body using gjson.
//
// Handles:
//   - /v1/chat/completions: choices[i].message.content, choices[i].message.reasoning_content
//
// For SSE streaming responses use DemaskBuffer on raw bytes instead — JSON parsing
// per-chunk is not viable due to arbitrary Envoy chunk boundaries.
func extractResponseGjsonSjson(body []byte) []ContentField {
	var fields []ContentField
	for i, choice := range gjson.GetBytes(body, "choices").Array() {
		base := "choices." + strconv.Itoa(i)

		if v := choice.Get("message.content"); v.Type == gjson.String && v.String() != "" {
			fields = append(fields, ContentField{Path: base + ".message.content", Value: v.String()})
		}
		if v := choice.Get("message.reasoning_content"); v.Type == gjson.String && v.String() != "" {
			fields = append(fields, ContentField{Path: base + ".message.reasoning_content", Value: v.String()})
		}
	}
	return fields
}

func benchmarkGjsonSjson(_ *testing.B, body []byte) {
	fields := extractResponseGjsonSjson(body)

	// Also extract tool_calls arguments
	for i, choice := range gjson.GetBytes(body, "choices").Array() {
		base := "choices." + fmt.Sprint(i) + ".message.tool_calls"
		for j, tc := range choice.Get("message.tool_calls").Array() {
			argsPath := base + "." + fmt.Sprint(j) + ".function.arguments"
			if args := tc.Get("function.arguments").String(); args != "" {
				fields = append(fields, ContentField{
					Path:  argsPath,
					Value: args,
				})
			}
		}
	}

	// Demask and patch
	for _, f := range fields {
		demasked := demaskContent(f.Value)
		_, _ = sjson.SetBytes(body, f.Path, demasked)
	}
}

// ============================================================================
// Approach 2: json.Unmarshal + json.Marshal
// ============================================================================

func benchmarkStructMarshal(_ *testing.B, body []byte) {
	var resp chatcompletions.Response
	_ = json.Unmarshal(body, &resp)

	// Demask content fields
	for i := range resp.Choices {
		if resp.Choices[i].Message.Content != nil {
			demasked := demaskContent(*resp.Choices[i].Message.Content)
			resp.Choices[i].Message.Content = &demasked
		}
		if resp.Choices[i].Message.ReasoningContent != nil {
			demasked := demaskContent(*resp.Choices[i].Message.ReasoningContent)
			resp.Choices[i].Message.ReasoningContent = &demasked
		}
		// Demask tool_calls arguments
		for j := range resp.Choices[i].Message.ToolCalls {
			demasked := demaskToolCallArguments(resp.Choices[i].Message.ToolCalls[j].Function.Arguments)
			resp.Choices[i].Message.ToolCalls[j].Function.Arguments = demasked
		}
	}

	_, _ = json.Marshal(resp)
}

// ============================================================================
// Benchmarks
// ============================================================================

func BenchmarkGjsonSjson_SingleChoiceContent(b *testing.B) {
	body := testCases["single_choice_content"]
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		benchmarkGjsonSjson(b, body)
	}
}

func BenchmarkStructMarshal_SingleChoiceContent(b *testing.B) {
	body := testCases["single_choice_content"]
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		benchmarkStructMarshal(b, body)
	}
}

func BenchmarkGjsonSjson_SingleChoiceReasoningContent(b *testing.B) {
	body := testCases["single_choice_reasoning_content"]
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		benchmarkGjsonSjson(b, body)
	}
}

func BenchmarkStructMarshal_SingleChoiceReasoningContent(b *testing.B) {
	body := testCases["single_choice_reasoning_content"]
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		benchmarkStructMarshal(b, body)
	}
}

func BenchmarkGjsonSjson_SingleChoiceContentOneToolCall(b *testing.B) {
	body := testCases["single_choice_content_one_tool_call"]
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		benchmarkGjsonSjson(b, body)
	}
}

func BenchmarkStructMarshal_SingleChoiceContentOneToolCall(b *testing.B) {
	body := testCases["single_choice_content_one_tool_call"]
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		benchmarkStructMarshal(b, body)
	}
}

func BenchmarkGjsonSjson_SingleChoiceReasoningContentMultipleToolCalls(b *testing.B) {
	body := testCases["single_choice_reasoning_content_multiple_tool_calls"]
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		benchmarkGjsonSjson(b, body)
	}
}

func BenchmarkStructMarshal_SingleChoiceReasoningContentMultipleToolCalls(b *testing.B) {
	body := testCases["single_choice_reasoning_content_multiple_tool_calls"]
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		benchmarkStructMarshal(b, body)
	}
}

func BenchmarkGjsonSjson_TwoChoicesContent(b *testing.B) {
	body := testCases["two_choices_content"]
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		benchmarkGjsonSjson(b, body)
	}
}

func BenchmarkStructMarshal_TwoChoicesContent(b *testing.B) {
	body := testCases["two_choices_content"]
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		benchmarkStructMarshal(b, body)
	}
}

func BenchmarkGjsonSjson_TwoChoicesReasoningContentToolCalls(b *testing.B) {
	body := testCases["two_choices_reasoning_content_tool_calls"]
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		benchmarkGjsonSjson(b, body)
	}
}

func BenchmarkStructMarshal_TwoChoicesReasoningContentToolCalls(b *testing.B) {
	body := testCases["two_choices_reasoning_content_tool_calls"]
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		benchmarkStructMarshal(b, body)
	}
}

func BenchmarkGjsonSjson_LargeContent(b *testing.B) {
	body := testCases["large_content"]
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		benchmarkGjsonSjson(b, body)
	}
}

func BenchmarkStructMarshal_LargeContent(b *testing.B) {
	body := testCases["large_content"]
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		benchmarkStructMarshal(b, body)
	}
}

func BenchmarkGjsonSjson_LargeReasoningContent(b *testing.B) {
	body := testCases["large_reasoning_content"]
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		benchmarkGjsonSjson(b, body)
	}
}

func BenchmarkStructMarshal_LargeReasoningContent(b *testing.B) {
	body := testCases["large_reasoning_content"]
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		benchmarkStructMarshal(b, body)
	}
}

// ============================================================================
// Memory allocation benchmarks
// ============================================================================

func BenchmarkGjsonSjson_Alloc_SingleChoiceContent(b *testing.B) {
	body := testCases["single_choice_content"]
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		benchmarkGjsonSjson(b, body)
	}
}

func BenchmarkStructMarshal_Alloc_SingleChoiceContent(b *testing.B) {
	body := testCases["single_choice_content"]
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		benchmarkStructMarshal(b, body)
	}
}

func BenchmarkGjsonSjson_Alloc_SingleChoiceReasoningContentMultipleToolCalls(b *testing.B) {
	body := testCases["single_choice_reasoning_content_multiple_tool_calls"]
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		benchmarkGjsonSjson(b, body)
	}
}

func BenchmarkStructMarshal_Alloc_SingleChoiceReasoningContentMultipleToolCalls(b *testing.B) {
	body := testCases["single_choice_reasoning_content_multiple_tool_calls"]
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		benchmarkStructMarshal(b, body)
	}
}

func BenchmarkGjsonSjson_Alloc_LargeContent(b *testing.B) {
	body := testCases["large_content"]
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		benchmarkGjsonSjson(b, body)
	}
}

func BenchmarkStructMarshal_Alloc_LargeContent(b *testing.B) {
	body := testCases["large_content"]
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		benchmarkStructMarshal(b, body)
	}
}
