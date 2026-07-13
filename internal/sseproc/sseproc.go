// Package sseproc is the top-level facade for SSE response demasking.
//
// Each supported upstream wire format has its own subpackage:
//   - chatcompletions: OpenAI /v1/chat/completions
//   - messages:        Anthropic /v1/messages
//   - responses:       OpenAI /v1/responses
//
// Construct a processor for one stream via NewChatCompletions, NewMessages,
// NewResponses, or — given the request's resolved API format — NewForFormat.
// All constructors return a common.Processor so callers can hold a single
// interface field.
//
// Shared building blocks (Demasker interface, frame splitter/classifier,
// JSON depth helpers) live in the internal/sseproc/common subpackage so each
// format implementation has a single source of truth to depend on.
package sseproc

import (
	"log/slog"

	"github.com/cloud-ru-tech/guardrails-llm-filter/internal/metrics"
	"github.com/cloud-ru-tech/guardrails-llm-filter/internal/models"
	"github.com/cloud-ru-tech/guardrails-llm-filter/internal/sseproc/chatcompletions"
	"github.com/cloud-ru-tech/guardrails-llm-filter/internal/sseproc/common"
	"github.com/cloud-ru-tech/guardrails-llm-filter/internal/sseproc/messages"
	"github.com/cloud-ru-tech/guardrails-llm-filter/internal/sseproc/responses"
)

// NewChatCompletions returns a Processor for OpenAI-style streaming
// (data: {chunk}\n\n frames with optional [DONE] sentinel). jsonFactory
// produces demaskers whose restored originals are JSON-escaped; it is used for
// tool-call and legacy function_call argument fragments, which the client
// accumulates as JSON.
func NewChatCompletions(factory, jsonFactory common.DemaskerFactoryFn, captureMasked bool) common.Processor {
	opts := []chatcompletions.Option{chatcompletions.WithJSONDemaskerFactory(jsonFactory)}
	if captureMasked {
		opts = append(opts, chatcompletions.WithMaskedResponseCapture())
	}
	return chatcompletions.New(factory, opts...)
}

// NewMessages returns a Processor for Anthropic Messages streaming
// (event: <name>\ndata: <json>\n\n pairs). jsonFactory produces demaskers
// whose restored originals are JSON-escaped; it is used for tool-input
// (input_json_delta) fragments, which the client accumulates as JSON.
func NewMessages(factory, jsonFactory common.DemaskerFactoryFn, captureMasked bool) common.Processor {
	opts := []messages.Option{messages.WithJSONDemaskerFactory(jsonFactory)}
	if captureMasked {
		opts = append(opts, messages.WithMaskedResponseCapture())
	}
	return messages.New(factory, opts...)
}

// NewResponses returns a Processor for OpenAI Responses API streaming
// (event: response.output_text.delta\ndata: <json>\n\n pairs). jsonFactory
// produces demaskers whose restored originals are JSON-escaped; it is used for
// response.function_call_arguments.delta fragments, which the client
// accumulates as JSON.
func NewResponses(factory, jsonFactory common.DemaskerFactoryFn, captureMasked bool) common.Processor {
	opts := []responses.Option{responses.WithJSONDemaskerFactory(jsonFactory)}
	if captureMasked {
		opts = append(opts, responses.WithMaskedResponseCapture())
	}
	return responses.New(factory, opts...)
}

// NewForFormat returns the Processor for a resolved API format. An unknown or
// empty format (reachable when masking state persisted by an older version
// lacks the Format field — the response side restores it from the store on a
// cross-replica deployment) returns a passthrough processor: the same
// fail-open policy the full-body path applies to the same condition, instead
// of routing the stream into a dialect processor that would mishandle it.
func NewForFormat(format models.APIFormat, factory, jsonFactory common.DemaskerFactoryFn, captureMasked bool) common.Processor {
	switch format {
	case models.APIFormatMessages:
		return NewMessages(factory, jsonFactory, captureMasked)
	case models.APIFormatResponses:
		return NewResponses(factory, jsonFactory, captureMasked)
	case models.APIFormatChatCompletions:
		return NewChatCompletions(factory, jsonFactory, captureMasked)
	default:
		slog.Warn("unknown api format for SSE processing, passing stream through unchanged",
			"format", string(format))
		metrics.IncUnknownFormatPassthrough()
		return passthroughProcessor{}
	}
}
