package messages

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/cloud-ru-tech/guardrails-llm-filter/pkg/llmutils"
)

func paths(fields []llmutils.ContentField) []string {
	out := make([]string, len(fields))
	for i, f := range fields {
		out[i] = f.Path
	}
	return out
}

func valueAt(fields []llmutils.ContentField, path string) (string, bool) {
	for _, f := range fields {
		if f.Path == path {
			return f.Value, true
		}
	}
	return "", false
}

func TestExtractRequestContent(t *testing.T) {
	t.Run("system string is extracted", func(t *testing.T) {
		// The top-level system prompt carries user-supplied instructions that
		// routinely include PII/secrets, so it must be masked.
		body := []byte(`{"model":"claude","system":"SSN 123-45-6789","messages":[{"role":"user","content":"hi"}]}`)
		fields, err := ExtractRequestContent(body)
		require.NoError(t, err)
		v, ok := valueAt(fields, "system")
		require.True(t, ok, "system must be extracted")
		assert.Equal(t, "SSN 123-45-6789", v)
		assert.Equal(t, []string{"system", "messages.0.content"}, paths(fields))
	})

	t.Run("system block array is extracted", func(t *testing.T) {
		body := []byte(`{"system":[{"type":"text","text":"first"},{"type":"text","text":"second"}],` +
			`"messages":[]}`)
		fields, err := ExtractRequestContent(body)
		require.NoError(t, err)
		assert.Equal(t, []string{"system.0.text", "system.1.text"}, paths(fields))
		v, ok := valueAt(fields, "system.0.text")
		require.True(t, ok)
		assert.Equal(t, "first", v)
	})

	t.Run("content blocks: text, tool_use, tool_result", func(t *testing.T) {
		body := []byte(`{"messages":[{"role":"user","content":[` +
			`{"type":"text","text":"hello"},` +
			`{"type":"tool_use","id":"t1","name":"f","input":{"q":"secret"}},` +
			`{"type":"tool_result","tool_use_id":"t1","content":"result text"}` +
			`]}]}`)
		fields, err := ExtractRequestContent(body)
		require.NoError(t, err)
		got := paths(fields)
		assert.Contains(t, got, "messages.0.content.0.text")
		assert.Contains(t, got, "messages.0.content.1.input.q")
		assert.Contains(t, got, "messages.0.content.2.content")

		v, _ := valueAt(fields, "messages.0.content.1.input.q")
		assert.Equal(t, "secret", v, "tool_use input string leaves are extracted decoded")
	})

	t.Run("tool_use input string leaves: nested, escaped, non-string skipped", func(t *testing.T) {
		// Scanning decoded leaves lets rules match values that JSON escaping
		// would otherwise hide, and leaf-level patching keeps the object valid.
		body := []byte(`{"messages":[{"role":"user","content":[` +
			`{"type":"tool_use","id":"t1","name":"f","input":` +
			`{"note":"say \"hi\"","nested":{"path":"C:\\tmp\\key"},"list":["x",7],"count":42,"ok":true}}` +
			`]}]}`)
		fields, err := ExtractRequestContent(body)
		require.NoError(t, err)
		base := "messages.0.content.0.input"
		v, ok := valueAt(fields, base+".note")
		require.True(t, ok)
		assert.Equal(t, `say "hi"`, v, "leaf values are decoded, not raw-escaped")
		v, ok = valueAt(fields, base+".nested.path")
		require.True(t, ok)
		assert.Equal(t, `C:\tmp\key`, v)
		_, ok = valueAt(fields, base+".list.0")
		assert.True(t, ok, "array string elements are leaves")
		for _, p := range []string{base + ".count", base + ".ok", base + ".list.1"} {
			_, ok := valueAt(fields, p)
			assert.False(t, ok, "non-string leaf %s must not be extracted", p)
		}
	})

	t.Run("tool_use input keys with path metacharacters are escaped", func(t *testing.T) {
		body := []byte(`{"messages":[{"role":"user","content":[` +
			`{"type":"tool_use","id":"t1","name":"f","input":{"a.b":"dotted","c*d":"starred"}}` +
			`]}]}`)
		fields, err := ExtractRequestContent(body)
		require.NoError(t, err)
		v, ok := valueAt(fields, `messages.0.content.0.input.a\.b`)
		require.True(t, ok, "dotted key must be escaped in the path")
		assert.Equal(t, "dotted", v)
		v, ok = valueAt(fields, `messages.0.content.0.input.c\*d`)
		require.True(t, ok, "wildcard key must be escaped in the path")
		assert.Equal(t, "starred", v)
	})

	t.Run("tool_result content as text blocks", func(t *testing.T) {
		body := []byte(`{"messages":[{"role":"user","content":[` +
			`{"type":"tool_result","content":[{"type":"text","text":"a"},{"type":"text","text":"b"}]}` +
			`]}]}`)
		fields, err := ExtractRequestContent(body)
		require.NoError(t, err)
		assert.Equal(t, []string{
			"messages.0.content.0.content.0.text",
			"messages.0.content.0.content.1.text",
		}, paths(fields))
	})

	t.Run("empty body returns nil,nil", func(t *testing.T) {
		fields, err := ExtractRequestContent([]byte(`{"model":"claude"}`))
		require.NoError(t, err)
		assert.Nil(t, fields)
	})

	t.Run("empty tool_use input is skipped", func(t *testing.T) {
		body := []byte(`{"messages":[{"role":"user","content":[` +
			`{"type":"tool_use","id":"t1","name":"f","input":{}}` +
			`]}]}`)
		fields, err := ExtractRequestContent(body)
		require.NoError(t, err)
		assert.Nil(t, fields)
	})
}
