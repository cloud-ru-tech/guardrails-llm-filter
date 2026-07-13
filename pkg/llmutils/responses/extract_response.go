package responses

import (
	"strconv"

	"github.com/tidwall/gjson"

	"github.com/cloud-ru-tech/guardrails-llm-filter/pkg/llmutils"
)

// ExtractOutputFields returns the demaskable text fields of a full
// OpenAI Responses API response object, with sjson-patchable paths rooted at
// basePath (pass "" for a top-level response body; the SSE processor passes
// "response" for the object embedded in response.completed events).
//
// Per output[i] item:
//   - type "message": content[j].text where content[j].type == "output_text"
//   - type "function_call": arguments (a JSON string — the caller must guard
//     the demasked value with json.Valid before patching)
//   - type "reasoning": summary[j].text where summary[j].type == "summary_text",
//     and content[j].text where content[j].type == "reasoning_text" (reasoning
//     models echo prompt text into their chain-of-thought). encrypted_content
//     is never touched.
//
// Everything else (web_search_call, refusal parts, annotations, ...) is left
// untouched.
func ExtractOutputFields(body []byte, basePath string) []llmutils.ContentField {
	root := gjson.ParseBytes(body)
	if basePath != "" {
		root = root.Get(basePath)
		basePath += "."
	}

	output := root.Get("output")
	if !output.IsArray() {
		return nil
	}

	fields := []llmutils.ContentField{}
	for i, item := range output.Array() {
		itemPath := basePath + "output." + strconv.Itoa(i)
		fields = append(fields, ExtractItemFields(item, itemPath)...)
	}
	return fields
}

// ExtractItemFields returns the demaskable text fields of a single
// Responses API output item (as embedded in response.output_item.done SSE
// events), with paths rooted at basePath.
func ExtractItemFields(item gjson.Result, basePath string) []llmutils.ContentField {
	switch item.Get("type").String() {
	case "message":
		fields := []llmutils.ContentField{}
		for j, part := range item.Get("content").Array() {
			if part.Get("type").String() != "output_text" {
				continue
			}
			text := part.Get("text")
			if text.Type != gjson.String || text.String() == "" {
				continue
			}
			fields = append(fields, llmutils.ContentField{
				Path:  basePath + ".content." + strconv.Itoa(j) + ".text",
				Value: text.String(),
			})
		}
		return fields

	case "function_call":
		args := item.Get("arguments")
		if args.Type != gjson.String || args.String() == "" {
			return nil
		}
		return []llmutils.ContentField{{Path: basePath + ".arguments", Value: args.String()}}

	case "reasoning":
		fields := []llmutils.ContentField{}
		for j, part := range item.Get("summary").Array() {
			if part.Get("type").String() != "summary_text" {
				continue
			}
			text := part.Get("text")
			if text.Type != gjson.String || text.String() == "" {
				continue
			}
			fields = append(fields, llmutils.ContentField{
				Path:  basePath + ".summary." + strconv.Itoa(j) + ".text",
				Value: text.String(),
			})
		}
		for j, part := range item.Get("content").Array() {
			if part.Get("type").String() != "reasoning_text" {
				continue
			}
			text := part.Get("text")
			if text.Type != gjson.String || text.String() == "" {
				continue
			}
			fields = append(fields, llmutils.ContentField{
				Path:  basePath + ".content." + strconv.Itoa(j) + ".text",
				Value: text.String(),
			})
		}
		return fields

	default:
		return nil
	}
}
