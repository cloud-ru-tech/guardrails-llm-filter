package common

import "bytes"

// EventPrefix and DoneSentinel complete the named-event SSE vocabulary
// (`event: <name>\ndata: <json>\n\n`) used by the Anthropic Messages and
// OpenAI Responses streams.
var (
	EventPrefix  = []byte("event: ")
	DoneSentinel = []byte("[DONE]")

	// dataLinePrefix matches a "data:" field line without assuming the leading
	// space. Per the SSE spec the single space after the colon is optional and
	// stripped, so `data:{...}` (emitted by some OpenAI-compatible backends) is
	// as valid as `data: {...}`. Classification matches this bare prefix and
	// trims one optional space; BuildEventFrame still emits canonical DataPrefix.
	dataLinePrefix = []byte("data:")
	dataLineSpace  = []byte(" ")
)

// FrameKind classifies a single named-event SSE frame.
type FrameKind int

const (
	// FrameEvent has an "event: <name>" line and a "data: <json>" line.
	FrameEvent FrameKind = iota
	// FrameDone is the terminal "data: [DONE]" sentinel.
	FrameDone
	// FramePassthrough is any frame we don't need to inspect: SSE comments
	// (lines starting with ":"), empty frames, or anything we can't classify.
	FramePassthrough
)

// ParsedFrame is the result of inspecting a single frame.
type ParsedFrame struct {
	Kind FrameKind
	// Event is the event name without the "event: " prefix; only set for
	// FrameEvent.
	Event []byte
	// Data is the JSON payload without the "data: " prefix; only set for
	// FrameEvent.
	Data []byte
	// Original is the original frame bytes (with trailing separator) for
	// passthrough/done.
	Original []byte
}

// ClassifyFrame inspects an SSE frame (including its trailing \n\n) and
// returns its classification. Frames are parsed line-by-line. A canonical
// named-event frame is:
//
//	event: content_block_delta\ndata: {"type":"content_block_delta",...}\n\n
//
// Processors only mutate the frames whose payload they recognize;
// everything else is passed through unchanged.
func ClassifyFrame(frame []byte) ParsedFrame {
	pf := ParsedFrame{Original: frame}

	// Walk lines until we have both an event: line and a data: line, or run
	// out. We strip the trailing \r before comparing so CRLF is supported.
	var eventName, dataPayload []byte
	rest := frame
	for len(rest) > 0 {
		line, remainder := NextLine(rest)
		rest = remainder

		trimmed := bytes.TrimRight(line, "\r")
		if len(trimmed) == 0 {
			continue
		}

		switch {
		case bytes.HasPrefix(trimmed, EventPrefix):
			eventName = trimmed[len(EventPrefix):]
		case bytes.HasPrefix(trimmed, dataLinePrefix):
			// SSE spec: strip a single optional leading space after "data:".
			payload := bytes.TrimPrefix(trimmed[len(dataLinePrefix):], dataLineSpace)
			if bytes.Equal(payload, DoneSentinel) {
				pf.Kind = FrameDone
				return pf
			}
			// SSE joins multiple data: lines in one event with "\n". Keep the
			// common single-line case zero-copy; only allocate a fresh buffer
			// when a second line appears, so we never append into frame's
			// backing array (frame is also pf.Original, forwarded verbatim on
			// passthrough).
			if dataPayload == nil {
				dataPayload = payload
			} else {
				joined := make([]byte, 0, len(dataPayload)+1+len(payload))
				joined = append(joined, dataPayload...)
				joined = append(joined, '\n')
				joined = append(joined, payload...)
				dataPayload = joined
			}
		}
	}

	if len(dataPayload) > 0 {
		pf.Kind = FrameEvent
		pf.Event = eventName
		pf.Data = dataPayload
		return pf
	}

	pf.Kind = FramePassthrough
	return pf
}

// NextLine returns the next line (without its trailing \n) and the remainder.
// If there is no newline left, the entire input is returned as the line and
// the remainder is empty.
func NextLine(buf []byte) (line, rest []byte) {
	idx := bytes.IndexByte(buf, '\n')
	if idx == -1 {
		return buf, nil
	}
	return buf[:idx], buf[idx+1:]
}

// BuildEventFrame rebuilds a named-event SSE frame from an event name and a
// JSON data payload. The result includes the trailing "\n\n" separator.
func BuildEventFrame(eventName, dataPayload []byte) []byte {
	out := make([]byte, 0, len(EventPrefix)+len(eventName)+1+len(DataPrefix)+len(dataPayload)+2)
	out = append(out, EventPrefix...)
	out = append(out, eventName...)
	out = append(out, '\n')
	out = append(out, DataPrefix...)
	out = append(out, dataPayload...)
	out = append(out, '\n', '\n')
	return out
}
