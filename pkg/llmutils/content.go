// Package llmutils holds the shared extraction contract consumed by its
// format-specific subpackages (chatcompletions, messages, responses): the
// ContentField addressing type and the ErrUnsupportedBodySchema sentinel.
package llmutils

import "errors"

var ErrUnsupportedBodySchema = errors.New("unsupported request body schema")

// ContentField is a mutable text field in an OpenAI-compatible JSON body.
// Path is a sjson-compatible dotted path for patching back into the body.
// Used for both request extraction (ExtractRequestContent) and response extraction (ExtractResponseContent).
type ContentField struct {
	Path  string // e.g. "messages.1.content", "prompt", "choices.0.message.content"
	Value string
}
