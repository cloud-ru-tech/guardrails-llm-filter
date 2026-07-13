package responses_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/tidwall/gjson"

	"github.com/cloud-ru-tech/guardrails-llm-filter/pkg/llmutils"
	"github.com/cloud-ru-tech/guardrails-llm-filter/pkg/llmutils/responses"
)

const responsesBody = `{
	"id": "resp_1",
	"status": "completed",
	"output": [
		{"type": "reasoning", "summary": [{"type": "summary_text", "text": "thinking about <EMAIL_1>"}]},
		{"type": "message", "role": "assistant", "content": [
			{"type": "output_text", "text": "hello <EMAIL_1>", "annotations": []},
			{"type": "refusal", "refusal": "no"}
		]},
		{"type": "function_call", "call_id": "c1", "name": "send", "arguments": "{\"to\":\"<EMAIL_1>\"}"},
		{"type": "web_search_call", "status": "completed"}
	]
}`

func TestExtractOutputFields(t *testing.T) {
	t.Parallel()

	got := responses.ExtractOutputFields([]byte(responsesBody), "")
	assert.Equal(t, []llmutils.ContentField{
		{Path: "output.0.summary.0.text", Value: "thinking about <EMAIL_1>"},
		{Path: "output.1.content.0.text", Value: "hello <EMAIL_1>"},
		{Path: "output.2.arguments", Value: `{"to":"<EMAIL_1>"}`},
	}, got)
}

func TestExtractOutputFieldsWithBasePath(t *testing.T) {
	t.Parallel()

	// The SSE processor extracts from the object embedded in
	// response.completed events.
	embedded := `{"type":"response.completed","response":` + responsesBody + `}`
	got := responses.ExtractOutputFields([]byte(embedded), "response")
	assert.Len(t, got, 3)
	assert.Equal(t, "response.output.1.content.0.text", got[1].Path)
}

func TestExtractOutputFieldsNoOutput(t *testing.T) {
	t.Parallel()
	assert.Empty(t, responses.ExtractOutputFields([]byte(`{"id":"resp_1"}`), ""))
	assert.Empty(t, responses.ExtractOutputFields([]byte(`{"output":"nope"}`), ""))
}

func TestExtractItemFields(t *testing.T) {
	t.Parallel()

	item := gjson.Parse(`{"type":"message","content":[{"type":"output_text","text":"hi <EMAIL_1>"}]}`)
	got := responses.ExtractItemFields(item, "item")
	assert.Equal(t, []llmutils.ContentField{{Path: "item.content.0.text", Value: "hi <EMAIL_1>"}}, got)

	unknown := gjson.Parse(`{"type":"computer_call","action":{}}`)
	assert.Empty(t, responses.ExtractItemFields(unknown, "item"))
}

// Reasoning items may carry chain-of-thought text in content[].reasoning_text
// (in addition to summary[].summary_text); both must be demasked, encrypted
// content never touched. Regression for a placeholder leaking in the reasoning
// trace of a reasoning model.
func TestExtractResponsesReasoningContent(t *testing.T) {
	t.Parallel()
	item := gjson.Parse(`{"type":"reasoning",` +
		`"summary":[{"type":"summary_text","text":"sum <EMAIL_1>"}],` +
		`"content":[{"type":"reasoning_text","text":"cot <EMAIL_1>"}],` +
		`"encrypted_content":"opaque"}`)
	got := responses.ExtractItemFields(item, "item")
	assert.Equal(t, []llmutils.ContentField{
		{Path: "item.summary.0.text", Value: "sum <EMAIL_1>"},
		{Path: "item.content.0.text", Value: "cot <EMAIL_1>"},
	}, got)
}
