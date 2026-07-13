package responses

import (
	"github.com/cloud-ru-tech/guardrails-llm-filter/internal/sseproc/common"
)

// fieldType is the demaskable streamed field of a Responses API output item.
type fieldType string

const (
	fieldOutputText    fieldType = "output_text"
	fieldFunctionArgs  fieldType = "function_args"
	fieldReasoningText fieldType = "reasoning_text"
)

// demaskerKey uniquely identifies one Demasker instance: per output item,
// per content part, per field. output_text and reasoning_text deltas carry both
// output_index and content_index; function_call arguments carry only
// output_index (their contentIndex is always 0). The fields never share a
// demasker — they have different flush disciplines.
type demaskerKey struct {
	outputIndex  int
	contentIndex int
	field        fieldType
}

// fieldAccum tracks close detection for one streamed field. The un-emitted
// content itself lives in the demasker (returned on error as a fail-open
// fallback); this only tracks brace depth for function_call arguments to
// decide when the argument object closed and the demasker can be flushed.
type fieldAccum struct {
	jsonTrk common.JSONCloseTracker
}

// AppendAndCheckClose records a fragment. For fieldFunctionArgs it feeds the
// cross-fragment JSON tracker and reports whether the argument object just
// closed; for output_text it always returns false — text flushes on
// output_text.done, response.completed or EOS.
func (a *fieldAccum) AppendAndCheckClose(field fieldType, fragment string) bool {
	if field != fieldFunctionArgs {
		return false
	}
	return a.jsonTrk.Feed(fragment)
}
