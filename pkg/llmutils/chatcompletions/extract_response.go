package chatcompletions

import (
	"strconv"

	"github.com/tidwall/gjson"

	"github.com/cloud-ru-tech/guardrails-llm-filter/pkg/llmutils"
)

// ExtractResponseContent extracts all non-empty text content fields from an
// OpenAI-compatible response body using gjson.
//
// Handles:
//   - /v1/chat/completions: choices[i].message.content, choices[i].message.reasoning_content
//
// For SSE streaming responses use DemaskBuffer on raw bytes instead — JSON parsing
// per-chunk is not viable due to arbitrary Envoy chunk boundaries.
func ExtractResponseContent(body []byte) []llmutils.ContentField {
	var fields []llmutils.ContentField
	for i, choice := range gjson.GetBytes(body, "choices").Array() {
		base := "choices." + strconv.Itoa(i)

		if v := choice.Get("message.content"); v.Type == gjson.String && v.String() != "" {
			fields = append(fields, llmutils.ContentField{Path: base + ".message.content", Value: v.String()})
		}
		// Some OpenAI-compatible providers use message.reasoning (instead of reasoning_content).
		if v := choice.Get("message.reasoning"); v.Type == gjson.String && v.String() != "" {
			fields = append(fields, llmutils.ContentField{Path: base + ".message.reasoning", Value: v.String()})
		}
		if v := choice.Get("message.reasoning_content"); v.Type == gjson.String && v.String() != "" {
			fields = append(fields, llmutils.ContentField{Path: base + ".message.reasoning_content", Value: v.String()})
		}
	}
	return fields
}

// ExtractOutputFields returns the demaskable fields of a full
// (non-streamed) OpenAI chat/completions response object, with sjson-patchable
// paths (no leading dot). Unlike ExtractResponseContent it also covers refusal
// text and tool/function call arguments, so the caller can demask-and-patch the
// body in place instead of round-tripping it through a partial typed struct
// (which would silently drop unmodeled fields such as system_fingerprint,
// service_tier, logprobs and annotations, and inject an empty usage object).
//
// Per choices[i].message:
//   - content, reasoning, reasoning_content, refusal: plain string fields
//   - function_call.arguments and tool_calls[j].function.arguments: JSON strings
//     whose path ends in ".arguments" — the caller must demask them structurally
//     (see DemaskJSONArguments) rather than as plain text.
//
// Everything else in the body is left untouched by the caller.
func ExtractOutputFields(body []byte) []llmutils.ContentField {
	var fields []llmutils.ContentField
	for i, choice := range gjson.GetBytes(body, "choices").Array() {
		base := "choices." + strconv.Itoa(i) + ".message"

		for _, field := range []string{"content", "reasoning", "reasoning_content", "refusal"} {
			if v := choice.Get("message." + field); v.Type == gjson.String && v.String() != "" {
				fields = append(fields, llmutils.ContentField{Path: base + "." + field, Value: v.String()})
			}
		}

		if v := choice.Get("message.function_call.arguments"); v.Type == gjson.String && v.String() != "" {
			fields = append(fields, llmutils.ContentField{Path: base + ".function_call.arguments", Value: v.String()})
		}

		for j, tc := range choice.Get("message.tool_calls").Array() {
			if v := tc.Get("function.arguments"); v.Type == gjson.String && v.String() != "" {
				fields = append(fields, llmutils.ContentField{
					Path:  base + ".tool_calls." + strconv.Itoa(j) + ".function.arguments",
					Value: v.String(),
				})
			}
		}
	}
	return fields
}
