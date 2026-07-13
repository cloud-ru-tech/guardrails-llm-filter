package chatcompletions

import (
	"github.com/cloud-ru-tech/guardrails-llm-filter/internal/sseproc/common"
)

// frameKind classifies a single SSE frame.
type frameKind int

const (
	frameData        frameKind = iota // data: {...}  — contains JSON payload
	frameDone                         // data: [DONE]
	framePassthrough                  // comment, empty line, or non-data line
)

// classifyFrame inspects a raw SSE frame (including trailing \n\n) and returns
// its kind plus the JSON payload bytes (only for frameData). It delegates to
// common.ClassifyFrame so that events with a leading comment/event/id line
// before the data line, or a multi-line data field, are classified line-by-line
// exactly like the messages/responses dialects. A whole-frame "data:" prefix
// check would misclassify such an event as passthrough and forward it verbatim,
// leaking the placeholder to the client undemasked.
func classifyFrame(frame []byte) (kind frameKind, jsonPayload []byte) {
	pf := common.ClassifyFrame(frame)
	switch pf.Kind {
	case common.FrameDone:
		return frameDone, nil
	case common.FrameEvent:
		return frameData, pf.Data
	default:
		return framePassthrough, nil
	}
}
