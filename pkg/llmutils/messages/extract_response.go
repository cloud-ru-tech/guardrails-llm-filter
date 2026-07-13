package messages

import (
	"strconv"

	"github.com/tidwall/gjson"

	"github.com/cloud-ru-tech/guardrails-llm-filter/pkg/llmutils"
)

// ExtractResponseFields returns the demaskable fields of a full
// (non-streamed) Anthropic Messages API response object, with sjson-patchable
// paths. The caller demasks-and-patches the body in place instead of
// round-tripping it through the SDK's typed Message: the SDK types have no
// omitempty and no custom marshaler, so a re-marshal would fabricate fields
// the response never had (stop_details, container, union zero values) and
// drop fields the pinned SDK version does not model.
//
// Per content[i] block, by type:
//   - "text": the text field
//   - "thinking": the thinking field; the signature field is encrypted and
//     left alone
//   - "tool_use": the input field — a JSON *object*; its raw form is returned
//     with a path ending in ".input" and must be demasked structurally and
//     patched raw (sjson.SetRawBytes) so it stays an object
//   - "redacted_thinking" (encrypted) and unknown types: skipped, passed
//     through byte-identical (forward compatibility)
func ExtractResponseFields(body []byte) []llmutils.ContentField {
	var fields []llmutils.ContentField
	for i, block := range gjson.GetBytes(body, "content").Array() {
		base := "content." + strconv.Itoa(i)
		switch block.Get("type").String() {
		case "text":
			if v := block.Get("text"); v.Type == gjson.String && v.String() != "" {
				fields = append(fields, llmutils.ContentField{Path: base + ".text", Value: v.String()})
			}
		case "thinking":
			if v := block.Get("thinking"); v.Type == gjson.String && v.String() != "" {
				fields = append(fields, llmutils.ContentField{Path: base + ".thinking", Value: v.String()})
			}
		case "tool_use":
			if v := block.Get("input"); v.IsObject() && v.Raw != "" && v.Raw != "{}" {
				fields = append(fields, llmutils.ContentField{Path: base + ".input", Value: v.Raw})
			}
		}
	}
	return fields
}
