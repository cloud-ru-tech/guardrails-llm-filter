package decisions

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/sjson"

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
	t.Run("state strings and question instructions are extracted", func(t *testing.T) {
		body := []byte(`{"model":"typesafe/jev",` +
			`"state":{"context":"ctx","goal":"goal","history":[{"role":"user","text":"hello"}]},` +
			`"questions":{"q1":{"type":"noul","instructions":"is it ok?"}}}`)
		fields, err := ExtractRequestContent(body)
		require.NoError(t, err)
		assert.Equal(t, []string{
			"state.context", "state.goal", "state.history.0.role", "state.history.0.text",
			"questions.q1.instructions",
		}, paths(fields))
	})

	t.Run("tool_calls as objects: input and result are extracted", func(t *testing.T) {
		// jev_arm.py sends tool_calls as {id, tool, input, result} objects.
		body := []byte(`{"state":{"history":[{"role":"assistant","text":"",` +
			`"tool_calls":[{"id":"t1","tool":"bash","input":"cat .env","result":"ok 12ch"}]}]}}`)
		fields, err := ExtractRequestContent(body)
		require.NoError(t, err)
		v, ok := valueAt(fields, "state.history.0.tool_calls.0.input")
		require.True(t, ok, "tool call input must be extracted")
		assert.Equal(t, "cat .env", v)
		_, ok = valueAt(fields, "state.history.0.tool_calls.0.result")
		assert.True(t, ok, "tool call result must be extracted")
	})

	t.Run("tool_calls as strings are extracted", func(t *testing.T) {
		// After history compaction jev_arm.py sends tool_calls as plain strings;
		// a typed struct would fail to decode this form and the request would
		// leave unmasked.
		body := []byte(`{"state":{"history":[{"role":"assistant","text":"",` +
			`"tool_calls":["t1 bash token=abc → ok 12ch","t2 read /etc/hosts → ok 3ch"]}]}}`)
		fields, err := ExtractRequestContent(body)
		require.NoError(t, err)
		v, ok := valueAt(fields, "state.history.0.tool_calls.1")
		require.True(t, ok)
		assert.Equal(t, "t2 read /etc/hosts → ok 3ch", v)
	})

	t.Run("tool call input as a JSON object: every string leaf is extracted", func(t *testing.T) {
		body := []byte(`{"state":{"history":[{"tool_calls":[` +
			`{"input":{"cmd":"curl -H 'X: secret'","opts":["a","b"]}}]}]}}`)
		fields, err := ExtractRequestContent(body)
		require.NoError(t, err)
		assert.Equal(t, []string{
			"state.history.0.tool_calls.0.input.cmd",
			"state.history.0.tool_calls.0.input.opts.0",
			"state.history.0.tool_calls.0.input.opts.1",
		}, paths(fields))
	})

	t.Run("state as a plain string is extracted", func(t *testing.T) {
		// The TypeSafe API accepts state as a string, an object or an array.
		body := []byte(`{"model":"jev-latest","state":"mail a@b.ru","questions":{"q":{"instructions":"ok?"}}}`)
		fields, err := ExtractRequestContent(body)
		require.NoError(t, err)
		assert.Equal(t, []string{"state", "questions.q.instructions"}, paths(fields))
	})

	t.Run("state as an array is extracted", func(t *testing.T) {
		body := []byte(`{"state":["one","two"],"questions":{}}`)
		fields, err := ExtractRequestContent(body)
		require.NoError(t, err)
		assert.Equal(t, []string{"state.0", "state.1"}, paths(fields))
	})

	t.Run("fields outside state and instructions are not scanned", func(t *testing.T) {
		body := []byte(`{"model":"typesafe/jev","user":"someone",` +
			`"questions":{"q1":{"type":"noul","instructions":"i"}}}`)
		fields, err := ExtractRequestContent(body)
		require.NoError(t, err)
		assert.Equal(t, []string{"questions.q1.instructions"}, paths(fields))
	})

	t.Run("keys with dots address the intended element", func(t *testing.T) {
		body := []byte(`{"questions":{"q.1":{"instructions":"secret"}}}`)
		fields, err := ExtractRequestContent(body)
		require.NoError(t, err)
		require.Len(t, fields, 1)

		patched, err := sjson.SetBytes(body, fields[0].Path, "<MASKED>")
		require.NoError(t, err)
		assert.JSONEq(t, `{"questions":{"q.1":{"instructions":"<MASKED>"}}}`, string(patched))
	})

	t.Run("patching leaves unknown fields byte-for-byte", func(t *testing.T) {
		body := []byte(`{"model":"m","state":{"context":"secret","extra":{"n":1}},"x":[1,2]}`)
		fields, err := ExtractRequestContent(body)
		require.NoError(t, err)

		patched, err := sjson.SetBytes(body, fields[0].Path, "<MASKED>")
		require.NoError(t, err)
		assert.Equal(t, `{"model":"m","state":{"context":"<MASKED>","extra":{"n":1}},"x":[1,2]}`, string(patched))
	})

	t.Run("empty strings and non-string values are skipped", func(t *testing.T) {
		body := []byte(`{"state":{"context":"","goal":null,"turn":3,"history":[]},` +
			`"questions":{"q1":{"instructions":""}}}`)
		fields, err := ExtractRequestContent(body)
		require.NoError(t, err)
		assert.Nil(t, fields)
	})

	t.Run("body with neither state nor questions is an unsupported schema", func(t *testing.T) {
		// A chat body sent to a path mapped to decisions: surface it as
		// unsupported_body_schema so the misconfiguration is visible.
		_, err := ExtractRequestContent([]byte(`{"messages":[{"role":"user","content":"hi"}]}`))
		assert.ErrorIs(t, err, llmutils.ErrUnsupportedBodySchema)
	})
}
