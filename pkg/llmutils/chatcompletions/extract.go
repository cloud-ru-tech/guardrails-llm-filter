package chatcompletions

import (
	"strconv"

	"github.com/tidwall/gjson"

	"github.com/cloud-ru-tech/guardrails-llm-filter/pkg/llmutils"
)

// ExtractRequestContent extracts scannable text fields from an OpenAI-compatible
// request body using gjson. Returns fields with sjson-patchable paths that preserve
// the real message index in the JSON array (not the index within the returned slice).
//
// Handles:
//   - /v1/chat/completions: messages[i].content (string)
//   - /v1/chat/completions: messages[i].content[j].text for text content parts
//   - /v1/chat/completions: messages[i].tool_calls[j].function.arguments
//   - /v1/chat/completions: messages[i].function_call.arguments
//
// The extractor is role-agnostic for chat messages. If a payload field is sent to the
// model as text, it is considered scannable regardless of the message role.
func ExtractRequestContent(body []byte) ([]llmutils.ContentField, error) {
	result := gjson.ParseBytes(body)

	// /v1/chat/completions: messages[].payload fields
	messages := result.Get("messages")
	if !messages.Exists() || !messages.IsArray() {
		return nil, llmutils.ErrUnsupportedBodySchema
	}

	fields := []llmutils.ContentField{}
	for i, msg := range messages.Array() {
		basePath := "messages." + strconv.Itoa(i)

		fields = append(
			fields,
			collectMessageContentFields(msg.Get("content"), basePath+".content")...,
		)
		fields = append(fields, collectFunctionCallFields(msg, basePath)...)
		fields = append(fields, collectToolCallArgumentFields(msg, basePath)...)
	}

	if len(fields) == 0 {
		return nil, nil
	}

	return fields, nil
}

func collectMessageContentFields(content gjson.Result, basePath string) []llmutils.ContentField {
	switch {
	case content.Type == gjson.String && content.String() != "":
		return []llmutils.ContentField{{
			Path:  basePath,
			Value: content.String(),
		}}

	case content.IsArray():
		fields := []llmutils.ContentField{}
		for i, part := range content.Array() {
			if part.Get("type").String() != "text" {
				continue
			}

			text := part.Get("text")
			if text.Type != gjson.String || text.String() == "" {
				continue
			}

			fields = append(fields, llmutils.ContentField{
				Path:  basePath + "." + strconv.Itoa(i) + ".text",
				Value: text.String(),
			})
		}

		return fields

	default:
		return nil
	}
}

func collectFunctionCallFields(msg gjson.Result, basePath string) []llmutils.ContentField {
	args := msg.Get("function_call.arguments")
	if args.Type != gjson.String || args.String() == "" {
		return nil
	}

	return []llmutils.ContentField{{
		Path:  basePath + ".function_call.arguments",
		Value: args.String(),
	}}
}

func collectToolCallArgumentFields(msg gjson.Result, basePath string) []llmutils.ContentField {
	toolCalls := msg.Get("tool_calls")
	if !toolCalls.Exists() || !toolCalls.IsArray() {
		return nil
	}

	fields := []llmutils.ContentField{}
	for i, toolCall := range toolCalls.Array() {
		args := toolCall.Get("function.arguments")
		if args.Type != gjson.String || args.String() == "" {
			continue
		}

		fields = append(fields, llmutils.ContentField{
			Path:  basePath + ".tool_calls." + strconv.Itoa(i) + ".function.arguments",
			Value: args.String(),
		})
	}

	return fields
}
