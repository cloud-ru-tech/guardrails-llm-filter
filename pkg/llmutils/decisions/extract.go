// Package decisions extracts the scannable text fields from Decisions API
// request bodies (TypeSafe Jev, OpenRouter /api/alpha/decisions).
//
// A Decisions answer holds scores and flags, never the source text, so the
// format is one-way: there is no response extractor and nothing to demask.
package decisions

import (
	"strconv"
	"strings"

	"github.com/tidwall/gjson"

	"github.com/cloud-ru-tech/guardrails-llm-filter/pkg/llmutils"
)

// ExtractRequestContent extracts scannable text fields from a Decisions API
// request body.
//
// Handles:
//   - state: every string leaf at any depth — context, goal, history[].text,
//     history[].tool_calls[] in whichever form the caller sends it (objects
//     with input/result, or plain strings)
//   - questions.<key>.instructions: a string
//
// State is walked rather than decoded into a struct because callers differ in
// how they shape history entries; a struct breaks on the first unexpected form
// and the request would be forwarded unmasked. Fields outside these two nodes
// (model, question types, ids) are not scanned and stay untouched.
//
// Returns llmutils.ErrUnsupportedBodySchema when the body has neither `state`
// nor `questions` (a path mapped to the wrong format), and (nil, nil) when
// there is nothing to scan.
func ExtractRequestContent(body []byte) ([]llmutils.ContentField, error) {
	root := gjson.ParseBytes(body)

	state := root.Get("state")
	questions := root.Get("questions")
	if !state.Exists() && !questions.Exists() {
		return nil, llmutils.ErrUnsupportedBodySchema
	}

	fields := collectJSONStringLeaves(state, "state")

	questions.ForEach(func(key, q gjson.Result) bool {
		instr := q.Get("instructions")
		if instr.Type == gjson.String && instr.String() != "" {
			fields = append(fields, llmutils.ContentField{
				Path:  "questions." + escapePathKey(key.String()) + ".instructions",
				Value: instr.String(),
			})
		}
		return true
	})

	if len(fields) == 0 {
		return nil, nil
	}
	return fields, nil
}

// collectJSONStringLeaves returns every non-empty string leaf under v as its
// own decoded field, with a path rooted at base. Patching a leaf back as a
// string keeps the JSON valid by construction.
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
