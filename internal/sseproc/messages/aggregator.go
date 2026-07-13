package messages

import (
	"github.com/cloud-ru-tech/guardrails-llm-filter/internal/sseproc/common"
)

// blockType identifies the kind of content block being demasked. It mirrors
// the "type" field that Anthropic sends in content_block_start events.
type blockType string

const (
	blockText             blockType = "text"
	blockThinking         blockType = "thinking"
	blockToolUse          blockType = "tool_use"
	blockRedactedThinking blockType = "redacted_thinking"
)

// fieldType is the demaskable field inside a block. It's a separate
// dimension from blockType so the demasker map key is uniform with sseproc
// even though for Anthropic each block contributes only one field.
type fieldType string

const (
	fieldText      fieldType = "text"
	fieldThinking  fieldType = "thinking"
	fieldToolInput fieldType = "tool_input"
)

// blockAccum tracks per-content-block state. The un-emitted content itself
// lives in the demasker (returned on error as a fail-open fallback); this
// tracks the block type plus, for tool_use input, JSON-object depth: feeding
// partial_json fragments to the demasker is only safe to flush when depth == 0
// and we just saw a closing brace, matching the contract enforced by the
// full-body demask path.
type blockAccum struct {
	typ     blockType
	jsonTrk common.JSONCloseTracker // only meaningful for fieldToolInput
}

// AppendAndCheckClose records a fragment. For fieldToolInput it feeds the
// fragment to the cross-fragment JSON tracker and returns true iff the JSON
// object just closed in this fragment. For other fields it always returns
// false — those fields flush on content_block_stop / [DONE] / EOS.
func (b *blockAccum) AppendAndCheckClose(field fieldType, fragment string) bool {
	if field != fieldToolInput {
		return false
	}
	return b.jsonTrk.Feed(fragment)
}
