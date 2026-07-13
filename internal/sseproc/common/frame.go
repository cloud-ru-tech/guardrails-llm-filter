package common

import "bytes"

// Frame separators and the "data: " prefix are shared across every SSE
// dialect we speak (OpenAI chat-completions, OpenAI Responses, Anthropic
// Messages). Keep them exported here so subpackages can refer to a single
// source of truth.
var (
	FrameSepLF   = []byte("\n\n")
	FrameSepCRLF = []byte("\r\n\r\n")
	DataPrefix   = []byte("data: ")
)

// SplitFrames splits raw SSE bytes into complete frames and a leftover tail.
// A frame boundary is "\n\n" (or "\r\n\r\n"). Each returned frame includes
// its trailing separator. tail contains any bytes after the last separator
// (an incomplete frame that the caller must carry into the next chunk).
func SplitFrames(data []byte) (frames [][]byte, tail []byte) {
	for len(data) > 0 {
		sep, idx := FindFrameSeparator(data)
		if idx == -1 {
			return frames, data
		}

		end := idx + len(sep)
		frames = append(frames, data[:end])
		data = data[end:]
	}
	return frames, nil
}

// FindFrameSeparator finds the earliest frame separator (\n\n or \r\n\r\n)
// in data and returns both the matched separator and its index. Returns
// idx == -1 when no separator is present.
func FindFrameSeparator(data []byte) (sep []byte, idx int) {
	lfIdx := bytes.Index(data, FrameSepLF)
	crlfIdx := bytes.Index(data, FrameSepCRLF)

	if crlfIdx != -1 && (lfIdx == -1 || crlfIdx < lfIdx) {
		return FrameSepCRLF, crlfIdx
	}
	return FrameSepLF, lfIdx
}
