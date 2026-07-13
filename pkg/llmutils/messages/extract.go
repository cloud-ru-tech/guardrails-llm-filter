// Package messages extracts the scannable/demaskable text fields from Anthropic
// Messages API (/v1/messages) request and response bodies.
package messages

import (
	"strconv"
	"strings"

	"github.com/tidwall/gjson"

	"github.com/cloud-ru-tech/guardrails-llm-filter/pkg/llmutils"
)

// ExtractRequestContent extracts scannable text fields from an Anthropic
// Messages API (/v1/messages) request body.
//
// Handles:
//   - top-level `system`: a string, or an array of {type:"text", text} blocks
//   - messages[i].content: a string, or an array of blocks:
//   - {type:"text", text}
//   - {type:"tool_use", input} — a JSON object; every string leaf inside it is
//     extracted as its own decoded field, so rules match the actual values
//     (not their JSON-escaped raw form) and patching a leaf back as a string
//     keeps the object valid JSON by construction.
//   - {type:"tool_result", content} — a string, or an array of text blocks
//
// Paths preserve the real array indices so the caller can sjson-patch them back.
// Returns (nil, nil) when there is nothing to scan (fail-open) — never an error,
// so a malformed or unexpected body simply passes through unmasked.
func ExtractRequestContent(body []byte) ([]llmutils.ContentField, error) {
	root := gjson.ParseBytes(body)
	var fields []llmutils.ContentField

	// Top-level system prompt: user-supplied instructions that routinely carry
	// PII/secrets and must be masked before reaching the model.
	if sys := root.Get("system"); sys.Exists() {
		switch {
		case sys.Type == gjson.String && sys.String() != "":
			fields = append(fields, llmutils.ContentField{Path: "system", Value: sys.String()})
		case sys.IsArray():
			fields = append(fields, collectTypedTextParts(sys, "system")...)
		}
	}

	root.Get("messages").ForEach(func(i, msg gjson.Result) bool {
		base := "messages." + i.String() + ".content"
		content := msg.Get("content")
		switch {
		case content.Type == gjson.String && content.String() != "":
			fields = append(fields, llmutils.ContentField{Path: base, Value: content.String()})
		case content.IsArray():
			fields = append(fields, collectMessagesContentBlocks(content, base)...)
		}
		return true
	})

	if len(fields) == 0 {
		return nil, nil
	}
	return fields, nil
}

// collectTypedTextParts collects non-empty {type:"text", text} parts of an
// array, with paths rooted at base ("<base>.<i>.text").
func collectTypedTextParts(arr gjson.Result, base string) []llmutils.ContentField {
	var fields []llmutils.ContentField
	for i, part := range arr.Array() {
		if part.Get("type").String() != "text" {
			continue
		}
		text := part.Get("text")
		if text.Type != gjson.String || text.String() == "" {
			continue
		}
		fields = append(fields, llmutils.ContentField{
			Path:  base + "." + strconv.Itoa(i) + ".text",
			Value: text.String(),
		})
	}
	return fields
}

// collectJSONStringLeaves walks a JSON value depth-first and collects every
// non-empty string leaf as its own field addressed by gjson path. Non-string
// leaves (numbers, booleans) are not scanned: substituting a string
// placeholder for them would change the value's JSON type.
func collectJSONStringLeaves(v gjson.Result, base string) []llmutils.ContentField {
	var fields []llmutils.ContentField
	switch {
	case v.IsObject():
		v.ForEach(func(k, child gjson.Result) bool {
			fields = append(fields, collectJSONStringLeaves(child, base+"."+escapePathKey(k.String()))...)
			return true
		})
	case v.IsArray():
		for i, child := range v.Array() {
			fields = append(fields, collectJSONStringLeaves(child, base+"."+strconv.Itoa(i))...)
		}
	case v.Type == gjson.String && v.String() != "":
		fields = append(fields, llmutils.ContentField{Path: base, Value: v.String()})
	}
	return fields
}

// escapePathKey escapes gjson/sjson path metacharacters in an object key so a
// key containing dots or wildcards addresses the intended element.
func escapePathKey(k string) string {
	if !strings.ContainsAny(k, `\.*?|#@`) {
		return k
	}
	var b strings.Builder
	for _, r := range k {
		switch r {
		case '\\', '.', '*', '?', '|', '#', '@':
			b.WriteByte('\\')
		}
		b.WriteRune(r)
	}
	return b.String()
}

// collectMessagesContentBlocks collects scannable fields from an Anthropic
// messages[i].content block array.
func collectMessagesContentBlocks(content gjson.Result, base string) []llmutils.ContentField {
	var fields []llmutils.ContentField
	for i, part := range content.Array() {
		partPath := base + "." + strconv.Itoa(i)
		switch part.Get("type").String() {
		case "text":
			if text := part.Get("text"); text.Type == gjson.String && text.String() != "" {
				fields = append(fields, llmutils.ContentField{Path: partPath + ".text", Value: text.String()})
			}
		case "tool_use":
			fields = append(fields, collectJSONStringLeaves(part.Get("input"), partPath+".input")...)
		case "tool_result":
			c := part.Get("content")
			switch {
			case c.Type == gjson.String && c.String() != "":
				fields = append(fields, llmutils.ContentField{Path: partPath + ".content", Value: c.String()})
			case c.IsArray():
				fields = append(fields, collectTypedTextParts(c, partPath+".content")...)
			}
		}
	}
	return fields
}
