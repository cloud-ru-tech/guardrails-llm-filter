// Package responses extracts the scannable/demaskable text fields from OpenAI
// Responses API (/v1/responses) request and response bodies.
package responses

import (
	"strconv"

	"github.com/tidwall/gjson"

	"github.com/cloud-ru-tech/guardrails-llm-filter/pkg/llmutils"
)

// ExtractRequestContent extracts scannable text fields from an
// OpenAI Responses API (/v1/responses) request body. Returns fields with
// sjson-patchable paths preserving real array indices.
//
// Handles:
//   - instructions (string)
//   - input (string form)
//   - input[i].content (string form of a message item)
//   - input[i].content[j].text for input_text/output_text/text content parts
//     (output_text/text appear when a client replays prior assistant turns in
//     stateless multi-turn — those values we demasked back to real originals,
//     so they must be re-masked before reaching the upstream)
//   - input[i].arguments for "function_call" items (a JSON string; an assistant
//     tool call replayed by the client, scanned as text like chat-completions)
//   - input[i].output for "function_call_output" items (string form)
//   - input[i].output[j].text for "function_call_output" items (content-array
//     form: output_text/input_text/text parts — tool results often carry PII)
//
// Deliberately skipped (non-text or opaque content): input_image/input_file
// parts, item_reference items, reasoning items (often encrypted, not replayed
// as plain text).
func ExtractRequestContent(body []byte) ([]llmutils.ContentField, error) {
	result := gjson.ParseBytes(body)

	input := result.Get("input")
	instructions := result.Get("instructions")
	if !input.Exists() && !instructions.Exists() {
		return nil, llmutils.ErrUnsupportedBodySchema
	}

	fields := []llmutils.ContentField{}

	if instructions.Type == gjson.String && instructions.String() != "" {
		fields = append(fields, llmutils.ContentField{Path: "instructions", Value: instructions.String()})
	}

	switch {
	case input.Type == gjson.String:
		if input.String() != "" {
			fields = append(fields, llmutils.ContentField{Path: "input", Value: input.String()})
		}

	case input.IsArray():
		for i, item := range input.Array() {
			basePath := "input." + strconv.Itoa(i)
			fields = append(fields, collectResponsesInputItemFields(item, basePath)...)
		}
	}

	if len(fields) == 0 {
		return nil, nil
	}
	return fields, nil
}

func collectResponsesInputItemFields(item gjson.Result, basePath string) []llmutils.ContentField {
	switch item.Get("type").String() {
	case "function_call_output":
		// Tool results sent back to the model — frequently contain retrieved
		// PII/secrets. Both the string form and the content-array form scanned.
		out := item.Get("output")
		switch {
		case out.Type == gjson.String && out.String() != "":
			return []llmutils.ContentField{{Path: basePath + ".output", Value: out.String()}}
		case out.IsArray():
			return collectResponsesTextParts(out, basePath+".output")
		}
		return nil

	case "function_call":
		// An assistant tool call replayed by the client in stateless multi-turn.
		// arguments is a JSON string that may carry values we demasked back to
		// real originals on a prior turn; scan it as text so they are re-masked
		// before reaching the upstream (mirrors chat-completions, which scans
		// tool_calls[].function.arguments). Placeholders like <EMAIL_1> stay
		// valid inside a JSON string.
		args := item.Get("arguments")
		if args.Type == gjson.String && args.String() != "" {
			return []llmutils.ContentField{{Path: basePath + ".arguments", Value: args.String()}}
		}
		return nil
	}

	// Message-shaped items: content is a string or an array of parts.
	content := item.Get("content")
	switch {
	case content.Type == gjson.String && content.String() != "":
		return []llmutils.ContentField{{Path: basePath + ".content", Value: content.String()}}

	case content.IsArray():
		// output_text/text parts appear when the client replays prior assistant
		// turns (stateless multi-turn); input_text is the user's own text. All
		// carry client-supplied text worth scanning — image/file parts are
		// skipped by collectResponsesTextParts.
		return collectResponsesTextParts(content, basePath+".content")

	default:
		return nil
	}
}

// collectResponsesTextParts extracts text from a Responses content-array (used
// by the content-array form of function_call_output.output): output_text,
// input_text and plain text parts carry client-supplied text worth scanning.
func collectResponsesTextParts(arr gjson.Result, base string) []llmutils.ContentField {
	var fields []llmutils.ContentField
	for j, part := range arr.Array() {
		switch part.Get("type").String() {
		case "output_text", "input_text", "text":
			text := part.Get("text")
			if text.Type != gjson.String || text.String() == "" {
				continue
			}
			fields = append(fields, llmutils.ContentField{
				Path:  base + "." + strconv.Itoa(j) + ".text",
				Value: text.String(),
			})
		}
	}
	return fields
}
