package llmutils

import (
	"strconv"
	"strings"

	"github.com/tidwall/gjson"
)

// CollectToolDefinitionFields returns the scannable text of a single tool (a.k.a.
// function) definition: its `description` plus the natural-language and example
// text inside its JSON Schema. schemaKey names that schema in the caller's
// dialect — "input_schema" for Anthropic Messages, "parameters" for the OpenAI
// chat/completions and Responses APIs.
//
// Tool definitions are part of the prompt: the model reads every description and
// example, so a default e-mail, an enum of real client names or a webhook URL
// with credentials reaches the provider exactly like message text does.
//
// Deliberately skipped:
//   - `name`: the model echoes it back in tool_use.name / tool_calls[].function.name,
//     which are never demasked (the client matches the call by name) — masking it
//     would break tool calling.
//   - operational fields of server-side tools (`server_url`, `headers`,
//     `user_location`, `allowed_tools`, `container`, ...): the provider consumes
//     them to actually perform the call, so a placeholder would break the tool
//     rather than protect it. They are the client's deliberate disclosure.
//   - machine-facing schema keywords (`type`, `format`, `pattern`, `$ref`,
//     `required`, ...): not natural language, and rewriting them would change
//     the contract the model is asked to satisfy.
func CollectToolDefinitionFields(tool gjson.Result, base, schemaKey string) []ContentField {
	var fields []ContentField

	if d := tool.Get("description"); d.Type == gjson.String && d.String() != "" {
		fields = append(fields, ContentField{Path: base + ".description", Value: d.String()})
	}
	if schema := tool.Get(schemaKey); schema.IsObject() {
		fields = append(fields, collectSchemaTextFields(schema, base+"."+schemaKey)...)
	}

	return fields
}

// schemaTextKeywords are the JSON Schema keywords whose value is human-readable
// text or example data. Masking them is safe: the only way the model can hand
// such a value back is inside tool-call arguments, which are demasked (request
// side: tool_use.input / tool_calls[].function.arguments; response side: the
// same paths, full-body and streaming).
var schemaTextKeywords = map[string]struct{}{
	"description": {},
	"title":       {},
	"default":     {},
	"const":       {},
	"enum":        {},
	"examples":    {},
}

// schemaMapKeywords are the keywords whose value maps arbitrary names to
// subschemas. Their keys are property names, so they must never be interpreted
// as keywords themselves — otherwise a property literally named "description"
// would be treated as a text field instead of a schema.
var schemaMapKeywords = map[string]struct{}{
	"properties":        {},
	"patternProperties": {},
	"dependentSchemas":  {},
	"$defs":             {},
	"definitions":       {},
}

// collectSchemaTextFields walks a JSON Schema and collects the text keywords'
// values. Anything else is traversed as a subschema (`items`, `allOf`, `if` and
// the rest), so nesting depth and vendor extensions need no special casing.
func collectSchemaTextFields(node gjson.Result, base string) []ContentField {
	if node.IsArray() {
		// A list of subschemas: allOf/anyOf/oneOf/prefixItems.
		var fields []ContentField
		for i, item := range node.Array() {
			fields = append(fields, collectSchemaTextFields(item, base+"."+strconv.Itoa(i))...)
		}
		return fields
	}
	if !node.IsObject() {
		return nil
	}

	var fields []ContentField
	node.ForEach(func(key, value gjson.Result) bool {
		k := key.String()
		path := base + "." + EscapePathKey(k)
		switch {
		case hasKeyword(schemaTextKeywords, k):
			fields = append(fields, collectTextValues(value, path)...)
		case hasKeyword(schemaMapKeywords, k):
			value.ForEach(func(name, sub gjson.Result) bool {
				fields = append(fields, collectSchemaTextFields(sub, path+"."+EscapePathKey(name.String()))...)
				return true
			})
		default:
			fields = append(fields, collectSchemaTextFields(value, path)...)
		}
		return true
	})
	return fields
}

// collectTextValues collects every non-empty string leaf of a text keyword's
// value: a plain string (description/title), an array (enum/examples) or an
// object (an object-shaped default or example). Non-string leaves are left
// alone — substituting a string placeholder would change their JSON type.
func collectTextValues(value gjson.Result, base string) []ContentField {
	switch {
	case value.Type == gjson.String:
		if value.String() == "" {
			return nil
		}
		return []ContentField{{Path: base, Value: value.String()}}

	case value.IsArray():
		var fields []ContentField
		for i, item := range value.Array() {
			fields = append(fields, collectTextValues(item, base+"."+strconv.Itoa(i))...)
		}
		return fields

	case value.IsObject():
		var fields []ContentField
		value.ForEach(func(key, item gjson.Result) bool {
			fields = append(fields, collectTextValues(item, base+"."+EscapePathKey(key.String()))...)
			return true
		})
		return fields
	}
	return nil
}

func hasKeyword(set map[string]struct{}, key string) bool {
	_, ok := set[key]
	return ok
}

// EscapePathKey escapes gjson/sjson path metacharacters in an object key so a
// key containing dots or wildcards addresses the intended element.
func EscapePathKey(k string) string {
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
