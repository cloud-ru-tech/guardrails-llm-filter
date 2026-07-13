package chatcompletions

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"

	"github.com/cloud-ru-tech/guardrails-llm-filter/internal/guardrails/demask"
	"github.com/cloud-ru-tech/guardrails-llm-filter/internal/models"
	"github.com/cloud-ru-tech/guardrails-llm-filter/internal/sseproc/common"
	llmchat "github.com/cloud-ru-tech/guardrails-llm-filter/pkg/llmutils/chatcompletions"
)

// splitFrames exercises common.SplitFrames directly from the test suite.
var splitFrames = common.SplitFrames

// mockDemasker is a test double that records calls and returns predefined outputs.
type mockDemasker struct {
	chunks  []string // chunks received (excluding empty)
	handler func(ctx context.Context, chunk string, flush bool) (string, error)
}

func newDemaskerMock(handler func(ctx context.Context, chunk string, flush bool) (string, error)) *mockDemasker {
	return &mockDemasker{
		chunks:  make([]string, 0, 1),
		handler: handler,
	}
}

func (m *mockDemasker) DemaskChunk(ctx context.Context, chunk string, flush bool) (string, error) {
	// Always record non-empty chunks
	if chunk != "" {
		m.chunks = append(m.chunks, chunk)
	}

	if m.handler != nil {
		return m.handler(ctx, chunk, flush)
	}

	// Passthrough by default
	return chunk, nil
}

func passthroughDemasker() common.DemaskerFactoryFn {
	return func() common.Demasker {
		return newDemaskerMock(nil) // Passthrough by default
	}
}

func bufferingDemasker() common.DemaskerFactoryFn {
	return func() common.Demasker {
		buf := ""
		return newDemaskerMock(
			func(ctx context.Context, chunk string, flush bool) (string, error) {
				buf += chunk // Always accumulate the chunk first
				if flush {
					result := buf
					buf = "" // Reset buffer after flush
					return result, nil
				}
				return "", nil
			})
	}
}

func nBufferingDemasker(chunksBeforeFlush int) common.DemaskerFactoryFn {
	return func() common.Demasker {
		buf := ""
		count := 0
		return newDemaskerMock(
			func(ctx context.Context, chunk string, flush bool) (string, error) {
				count++
				buf += chunk // Always accumulate the chunk first
				if flush || count == chunksBeforeFlush {
					result := buf
					buf = ""  // Reset buffer after flush
					count = 0 // Reset count after flush to allow periodic flushing
					return result, nil
				}
				return "", nil
			})
	}
}

// makeFrame constructs an SSE frame from a Chunk.
func makeFrame(chunk llmchat.Chunk) string {
	data, _ := json.Marshal(chunk)
	return "data: " + string(data) + "\n\n"
}

// makeDoneFrame returns the [DONE] sentinel frame.
func makeDoneFrame() string {
	return "data: [DONE]\n\n"
}

// makeContentChunk creates a Chunk with content in choice 0.
func makeContentChunk(content string) llmchat.Chunk {
	return llmchat.Chunk{
		ID:      "chatcmpl-123",
		Object:  "chat.completion.chunk",
		Created: 1234567890,
		Model:   "gpt-4",
		Choices: []llmchat.ChunkChoice{
			{
				Index: 0,
				Delta: llmchat.Delta{
					Content: &content,
				},
			},
		},
	}
}

func parseDataFrame(t *testing.T, frame []byte) (chunk llmchat.Chunk) {
	frame = bytes.TrimPrefix(bytes.TrimSpace(frame), []byte("data: "))
	err := json.Unmarshal(frame, &chunk)
	require.NoError(t, err)
	return
}

// makeMetadataChunk creates a Chunk with usage but no content.
func makeMetadataChunk() llmchat.Chunk {
	return llmchat.Chunk{
		ID:      "chatcmpl-123",
		Object:  "chat.completion.chunk",
		Created: 1234567890,
		Model:   "gpt-4",
		Choices: []llmchat.ChunkChoice{
			{
				Index: 0,
				Delta: llmchat.Delta{
					// Content and Reasoning are nil (not set)
				},
				FinishReason: toPtr("stop"),
			},
		},
		Usage: &llmchat.Usage{
			PromptTokens:     10,
			CompletionTokens: 20,
			TotalTokens:      30,
		},
	}
}

func TestProcessChunk_CombinedContentFinishReasonPreserved(t *testing.T) {
	// A single frame carrying both content and finish_reason (+ usage). The
	// demasked content frame sets finish_reason/usage to nil, so unless a
	// metadata frame is also emitted the client never sees them.
	p := New(passthroughDemasker())
	content := "hi"
	chunk := llmchat.Chunk{
		ID:      "chatcmpl-1",
		Object:  "chat.completion.chunk",
		Created: 1,
		Model:   "gpt-4",
		Choices: []llmchat.ChunkChoice{{
			Index:        0,
			Delta:        llmchat.Delta{Content: &content},
			FinishReason: toPtr("stop"),
		}},
		Usage: &llmchat.Usage{TotalTokens: 5},
	}

	out, err := p.ProcessChunk(context.Background(), []byte(makeFrame(chunk)), true)
	require.NoError(t, err)

	var sawContent, sawFinish, sawUsage bool
	frames, _ := splitFrames(out)
	for _, f := range frames {
		if bytes.Contains(bytes.TrimSpace(f), []byte("[DONE]")) {
			continue
		}
		c := parseDataFrame(t, f)
		for _, ch := range c.Choices {
			if ch.Delta.Content != nil && *ch.Delta.Content == "hi" {
				sawContent = true
			}
			if ch.FinishReason != nil && *ch.FinishReason == "stop" {
				sawFinish = true
			}
		}
		if c.Usage != nil && c.Usage.TotalTokens == 5 {
			sawUsage = true
		}
	}

	assert.True(t, sawContent, "demasked content must be emitted")
	assert.True(t, sawFinish, "finish_reason must be preserved")
	assert.True(t, sawUsage, "usage must be preserved")
}

// A data frame with non-empty choices but nothing demaskable and no metadata
// (here delta.refusal) must be forwarded, not silently dropped.
func TestProcessChunk_ForwardsRefusalDelta(t *testing.T) {
	p := New(passthroughDemasker())
	in := `data: {"choices":[{"index":0,"delta":{"content":"hi"}}]}` + "\n\n" +
		`data: {"choices":[{"index":0,"delta":{"refusal":"I can't help"}}]}` + "\n\n" +
		`data: {"choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}` + "\n\n" +
		"data: [DONE]\n\n"

	out, err := p.ProcessChunk(context.Background(), []byte(in), true)
	require.NoError(t, err)
	assert.Contains(t, string(out), `"refusal":"I can't help"`, "refusal frame must be forwarded")
	assert.Contains(t, string(out), "hi", "content must still be emitted")
}

// A role-only opening delta ({"delta":{"role":"assistant"}}) carries no
// demaskable content and must be forwarded rather than dropped.
func TestProcessChunk_ForwardsRoleOnlyDelta(t *testing.T) {
	p := New(passthroughDemasker())
	in := `data: {"choices":[{"index":0,"delta":{"role":"assistant"}}]}` + "\n\n" +
		"data: [DONE]\n\n"

	out, err := p.ProcessChunk(context.Background(), []byte(in), true)
	require.NoError(t, err)
	assert.Contains(t, string(out), `"role":"assistant"`, "role-only delta must be forwarded")
}

// Demasked output frames must not HTML-escape <, >, & — the SSE path uses
// common.MarshalNoEscape like the other dialects.
func TestProcessChunk_NoHTMLEscape(t *testing.T) {
	p := New(passthroughDemasker())
	out, err := p.ProcessChunk(context.Background(), []byte(makeFrame(makeContentChunk("a<b&c>d"))), true)
	require.NoError(t, err)
	assert.Contains(t, string(out), "a<b&c>d", "delimiters must be literal")
	assert.NotContains(t, string(out), "\\u003c", "must not HTML-escape <")
	assert.NotContains(t, string(out), "\\u003e", "must not HTML-escape >")
	assert.NotContains(t, string(out), "\\u0026", "must not HTML-escape &")
}

func TestProcessChunk_NBufferingDemasker_SimpleContent(t *testing.T) {
	// Simple content test: nBufferingDemasker(2) should return output after every 2 chunks
	// data: {content: "Hello "}
	// data: {content: "World!"} → should flush and return "Hello World!"
	// data: {content: "How "}
	// data: {content: "are "} → should flush and return "How are "
	// data: {content: "you?"} + EOS → should flush and return "you?"

	p := New(nBufferingDemasker(2))
	ctx := context.Background()

	frame1 := makeFrame(makeContentChunk("Hello "))
	frame2 := makeFrame(makeContentChunk("World!"))
	frame3 := makeFrame(makeContentChunk("How "))
	frame4 := makeFrame(makeContentChunk("are "))
	frame5 := makeFrame(makeContentChunk("you?"))

	// First two chunks: should return output after 2nd chunk
	{
		out1, err := p.ProcessChunk(ctx, []byte(frame1), false)
		require.NoError(t, err)
		require.Nil(t, out1, "Expected no output after 1st chunk (buffering)")

		out2, err := p.ProcessChunk(ctx, []byte(frame2), false)
		require.NoError(t, err)
		require.NotNil(t, out2, "Expected output after 2nd chunk")

		frames, _ := splitFrames(out2)
		require.Len(t, frames, 1)
		chunk := parseDataFrame(t, frames[0])
		assert.Equal(t, "Hello World!", *chunk.Choices[0].Delta.Content)
	}

	// Next two chunks: should return output after 4th chunk
	{
		out3, err := p.ProcessChunk(ctx, []byte(frame3), false)
		require.NoError(t, err)
		require.Nil(t, out3, "Expected no output after 3rd chunk (buffering)")

		out4, err := p.ProcessChunk(ctx, []byte(frame4), false)
		require.NoError(t, err)
		require.NotNil(t, out4, "Expected output after 4th chunk")

		frames, _ := splitFrames(out4)
		require.Len(t, frames, 1)
		chunk := parseDataFrame(t, frames[0])
		assert.Equal(t, "How are ", *chunk.Choices[0].Delta.Content)
	}

	// Last chunk with EOS: should flush immediately
	{
		out5, err := p.ProcessChunk(ctx, []byte(frame5), true)
		require.NoError(t, err)
		require.NotNil(t, out5, "Expected output for EOS")

		frames, _ := splitFrames(out5)
		require.Len(t, frames, 1)
		chunk := parseDataFrame(t, frames[0])
		assert.Equal(t, "you?", *chunk.Choices[0].Delta.Content)
	}
}

func TestProcessChunk_Case1_TwoFramesWithEOS(t *testing.T) {
	// Case 1: Without [DONE], two content frames, last one with EOS
	// data: Hello
	// data: World! + EOS - must flush

	p := New(bufferingDemasker())
	ctx := context.Background()

	frame1 := makeFrame(makeContentChunk("Hello "))
	frame2 := makeFrame(makeContentChunk("World!"))

	// First chunk: frame1 (demasker buffers, no output)
	out1, err := p.ProcessChunk(ctx, []byte(frame1), false)
	require.NoError(t, err, "ProcessChunk chunk1 should not error")
	require.Nil(t, out1, "Expected no output for chunk1 (buffering)")

	// Second chunk: frame2 + EOS (flush triggers, output aggregated content)
	out2, err := p.ProcessChunk(ctx, []byte(frame2), true)
	require.NoError(t, err, "ProcessChunk chunk2 should not error")
	require.NotNil(t, out2, "Expected output for chunk2")

	frames, _ := splitFrames(out2)
	require.Len(t, frames, 1, "Expected 1 aggregated frame")

	frameData := parseDataFrame(t, frames[0])
	require.Len(t, frameData.Choices, 1)
	assert.Equal(t, "Hello World!", *frameData.Choices[0].Delta.Content)
}

func TestProcessChunk_Case2_TwoFramesThenEOS(t *testing.T) {
	// Case 2: Without [DONE], two content frames, then separate EOS
	// data: Hello
	// data: World!
	// EOS - must flush

	p := New(bufferingDemasker())
	ctx := context.Background()

	frame1 := makeFrame(makeContentChunk("Hello "))
	frame2 := makeFrame(makeContentChunk("World!"))

	// First chunk: frame1 (demasker buffers, no output)
	out1, err := p.ProcessChunk(ctx, []byte(frame1), false)
	require.NoError(t, err, "ProcessChunk chunk1 should not error")
	require.Nil(t, out1, "Expected no output for chunk1 (buffering)")

	// Second chunk: frame2 (demasker buffers, no output)
	out2, err := p.ProcessChunk(ctx, []byte(frame2), false)
	require.NoError(t, err, "ProcessChunk chunk2 should not error")
	require.Nil(t, out2, "Expected no output for chunk2 (buffering)")

	// Third chunk: EOS only (flush triggers, output aggregated content)
	out3, err := p.ProcessChunk(ctx, []byte{}, true)
	require.NoError(t, err, "ProcessChunk EOS should not error")
	require.NotNil(t, out3, "Expected output for EOS")

	frames, _ := splitFrames(out3)
	require.Len(t, frames, 1, "Expected 1 aggregated frame")

	frameData := parseDataFrame(t, frames[0])
	require.Len(t, frameData.Choices, 1)
	assert.Equal(t, "Hello World!", *frameData.Choices[0].Delta.Content)
}

func TestProcessChunk_Case3_ContentMetadataWithEOS(t *testing.T) {
	// Case 3: Content frames, then metadata frame with EOS
	// data: Hello
	// data: World!
	// data: {usage + finish reason, NOCONTENT!} + EOS - must flush

	p := New(bufferingDemasker())
	ctx := context.Background()

	frame1 := makeFrame(makeContentChunk("Hello "))
	frame2 := makeFrame(makeContentChunk("World!"))
	frame3 := makeFrame(makeMetadataChunk())

	input := frame1 + frame2 + frame3

	// Process all frames with EOS
	out, err := p.ProcessChunk(ctx, []byte(input), true)
	require.NoError(t, err, "ProcessChunk should not error")
	require.NotNil(t, out, "Expected output")

	// Verify output contains both content and metadata frames
	frames, _ := splitFrames(out)
	require.Len(t, frames, 2)

	contentChunk := parseDataFrame(t, frames[0])
	require.Len(t, contentChunk.Choices, 1)
	assert.Nil(t, contentChunk.Usage)
	assert.Equal(t, "Hello World!", *contentChunk.Choices[0].Delta.Content)

	mdChunk := parseDataFrame(t, frames[1])
	require.Len(t, mdChunk.Choices, 1)
	assert.NotNil(t, mdChunk.Usage)
	assert.NotNil(t, mdChunk.Choices[0].FinishReason)
	// Metadata chunk should have no content or reasoning
	assert.Nil(t, mdChunk.Choices[0].Delta.Content)
	assert.Nil(t, mdChunk.Choices[0].Delta.Reasoning)
}

func TestProcessChunk_Case4_ContentMetadataThenEOS(t *testing.T) {
	// Case 4: Content frames, metadata frame, then separate EOS
	// data: Hello
	// data: World!
	// data: {usage + finish reason, NOCONTENT!}
	// EOS
	// finish_reason triggers an immediate flush (needed for multi-choice)
	// Expected output: content frame + metadata frame when finish_reason arrives

	p := New(bufferingDemasker())
	ctx := context.Background()

	frame1 := makeFrame(makeContentChunk("Hello "))
	frame2 := makeFrame(makeContentChunk("World!"))
	frame3 := makeFrame(makeMetadataChunk())

	// First: process content and metadata - finish_reason triggers flush
	out1, err := p.ProcessChunk(ctx, []byte(frame1+frame2+frame3), false)
	require.NoError(t, err, "ProcessChunk chunk1 should not error")
	require.NotNil(t, out1, "Expected output when finish_reason arrives")

	// Verify output contains 2 frames in correct order: content, then metadata
	frames1, _ := splitFrames(out1)
	require.Len(t, frames1, 2, "Expected 2 frames: content and metadata")

	// First frame should be aggregated content
	contentChunk := parseDataFrame(t, frames1[0])
	require.Len(t, contentChunk.Choices, 1)
	assert.Nil(t, contentChunk.Usage)
	assert.Equal(t, "Hello World!", *contentChunk.Choices[0].Delta.Content)

	// Second frame should be metadata
	mdChunk := parseDataFrame(t, frames1[1])
	require.Len(t, mdChunk.Choices, 1)
	assert.NotNil(t, mdChunk.Usage)
	assert.NotNil(t, mdChunk.Choices[0].FinishReason)
	assert.Nil(t, mdChunk.Choices[0].Delta.Content)
	assert.Nil(t, mdChunk.Choices[0].Delta.Reasoning)

	// Then: EOS (should produce no output, already flushed)
	out2, err := p.ProcessChunk(ctx, []byte{}, true)
	require.NoError(t, err, "ProcessChunk EOS should not error")
	assert.Nil(t, out2, "Expected no output for EOS (already flushed)")
}

func TestProcessChunk_Case5_ContentDoneWithEOS(t *testing.T) {
	// Case 5: With [DONE], content frames + [DONE] + EOS
	// data: Hello
	// data: World!
	// data: [DONE] + EOS - must flush and send 2 frames: data and done

	p := New(bufferingDemasker())
	ctx := context.Background()

	frame1 := makeFrame(makeContentChunk("Hello "))
	frame2 := makeFrame(makeContentChunk("World!"))
	frame3 := makeDoneFrame()

	input := frame1 + frame2 + frame3

	// Process all frames with EOS
	out, err := p.ProcessChunk(ctx, []byte(input), true)
	require.NoError(t, err, "ProcessChunk should not error")
	require.NotNil(t, out, "Expected output")

	// Verify output contains both content frame and [DONE] frame
	frames, _ := splitFrames(out)
	require.Len(t, frames, 2, "Expected 2 frames: content and [DONE]")

	// First frame should be aggregated content
	contentChunk := parseDataFrame(t, frames[0])
	require.Len(t, contentChunk.Choices, 1)
	assert.Equal(t, "Hello World!", *contentChunk.Choices[0].Delta.Content)

	// Second frame should be [DONE]
	assert.Equal(t, "data: [DONE]", string(bytes.TrimSpace(frames[1])))
}

func TestProcessChunk_Case6_ContentDoneThenEOS(t *testing.T) {
	// Case 6: Content frames + [DONE], then separate EOS
	// data: Hello
	// data: World!
	// data: [DONE] - must flush
	// EOS - no flush, all buffers closed

	p := New(bufferingDemasker())
	ctx := context.Background()

	frame1 := makeFrame(makeContentChunk("Hello "))
	frame2 := makeFrame(makeContentChunk("World!"))
	frame3 := makeDoneFrame()

	// First: process content and [DONE]
	out1, err := p.ProcessChunk(ctx, []byte(frame1+frame2+frame3), false)
	require.NoError(t, err, "ProcessChunk chunk1 should not error")
	require.NotNil(t, out1, "Expected output for chunk1")

	// Verify output contains both content frame and [DONE] frame
	frames, _ := splitFrames(out1)
	require.Len(t, frames, 2, "Expected 2 frames: content and [DONE]")

	// First frame should be aggregated content
	contentChunk := parseDataFrame(t, frames[0])
	require.Len(t, contentChunk.Choices, 1)
	assert.Equal(t, "Hello World!", *contentChunk.Choices[0].Delta.Content)

	// Second frame should be [DONE]
	assert.Equal(t, "data: [DONE]", string(bytes.TrimSpace(frames[1])))

	// Second: EOS (should produce no output, all buffers closed)
	out2, err := p.ProcessChunk(ctx, []byte{}, true)
	require.NoError(t, err, "ProcessChunk EOS should not error")
	assert.Nil(t, out2, "Expected no output for EOS after [DONE]")
}

func TestProcessChunk_Case7_ContentMetadataDoneWithEOS(t *testing.T) {
	// Case 7: Content, metadata, [DONE], all with EOS
	// data: Hello
	// data: World!
	// data: {usage + finish reason, NOCONTENT!}
	// data: [DONE] + EOS - must flush and send 3 frames: content, metadata, [DONE]
	// Expected output order: content, metadata, [DONE]

	p := New(bufferingDemasker())
	ctx := context.Background()

	frame1 := makeFrame(makeContentChunk("Hello "))
	frame2 := makeFrame(makeContentChunk("World!"))
	frame3 := makeFrame(makeMetadataChunk())
	frame4 := makeDoneFrame()

	input := frame1 + frame2 + frame3 + frame4

	// Process all frames with EOS
	out, err := p.ProcessChunk(ctx, []byte(input), true)
	require.NoError(t, err, "ProcessChunk should not error")
	require.NotNil(t, out, "Expected output")

	// Verify output contains 3 frames in correct order: content, metadata, [DONE]
	frames, _ := splitFrames(out)
	require.Len(t, frames, 3, "Expected 3 frames: content, metadata, and [DONE]")

	// First frame is aggregated content (flushed before [DONE])
	contentChunk := parseDataFrame(t, frames[0])
	require.Len(t, contentChunk.Choices, 1)
	assert.Equal(t, "Hello World!", *contentChunk.Choices[0].Delta.Content)
	assert.Nil(t, contentChunk.Usage)

	// Second frame is metadata
	mdChunk := parseDataFrame(t, frames[1])
	require.Len(t, mdChunk.Choices, 1)
	assert.NotNil(t, mdChunk.Choices[0].FinishReason)
	assert.NotNil(t, mdChunk.Usage)
	assert.Equal(t, 10, mdChunk.Usage.PromptTokens)
	assert.Equal(t, 20, mdChunk.Usage.CompletionTokens)
	assert.Nil(t, mdChunk.Choices[0].Delta.Content)
	assert.Nil(t, mdChunk.Choices[0].Delta.Reasoning)

	// Third frame should be [DONE]
	assert.Equal(t, "data: [DONE]", string(bytes.TrimSpace(frames[2])))
}

func TestProcessChunk_Case8_ContentMetadataDoneThenEOS(t *testing.T) {
	// Case 8: Content, metadata, [DONE], then separate EOS
	// data: Hello
	// data: World!
	// data: {usage + finish reason, NOCONTENT!}
	// data: [DONE] - must flush
	// EOS - no flush, all buffers closed
	// Expected output order: content, metadata, [DONE]

	p := New(bufferingDemasker())
	ctx := context.Background()

	frame1 := makeFrame(makeContentChunk("Hello "))
	frame2 := makeFrame(makeContentChunk("World!"))
	frame3 := makeFrame(makeMetadataChunk())
	frame4 := makeDoneFrame()

	// First: process all frames without EOS
	out1, err := p.ProcessChunk(ctx, []byte(frame1+frame2+frame3+frame4), false)
	require.NoError(t, err, "ProcessChunk chunk1 should not error")
	require.NotNil(t, out1, "Expected output for chunk1")

	// Verify output contains 3 frames in correct order: content, metadata, [DONE]
	frames, _ := splitFrames(out1)
	require.Len(t, frames, 3, "Expected 3 frames: content, metadata, and [DONE]")

	// First frame is aggregated content (flushed before [DONE])
	contentChunk := parseDataFrame(t, frames[0])
	require.Len(t, contentChunk.Choices, 1)
	assert.Equal(t, "Hello World!", *contentChunk.Choices[0].Delta.Content)
	assert.Nil(t, contentChunk.Usage)

	// Second frame is metadata
	mdChunk := parseDataFrame(t, frames[1])
	require.Len(t, mdChunk.Choices, 1)
	assert.NotNil(t, mdChunk.Choices[0].FinishReason)
	assert.NotNil(t, mdChunk.Usage)
	assert.Equal(t, 10, mdChunk.Usage.PromptTokens)
	assert.Equal(t, 20, mdChunk.Usage.CompletionTokens)
	assert.Nil(t, mdChunk.Choices[0].Delta.Content)
	assert.Nil(t, mdChunk.Choices[0].Delta.Reasoning)

	// Third frame should be [DONE]
	assert.Equal(t, "data: [DONE]", string(bytes.TrimSpace(frames[2])))

	// Second: EOS (should produce no output, all buffers closed)
	out2, err := p.ProcessChunk(ctx, []byte{}, true)
	require.NoError(t, err, "ProcessChunk EOS should not error")
	assert.Nil(t, out2, "Expected no output for EOS after [DONE]")
}

// ============================================================================
// REASONING FIELD TESTS
// ============================================================================

// makeReasoningChunk creates a Chunk with reasoning in choice 0.
func makeReasoningChunk(reasoning string) llmchat.Chunk {
	return llmchat.Chunk{
		ID:      "chatcmpl-123",
		Object:  "chat.completion.chunk",
		Created: 1234567890,
		Model:   "gpt-4",
		Choices: []llmchat.ChunkChoice{
			{
				Index: 0,
				Delta: llmchat.Delta{
					Reasoning: &reasoning,
				},
			},
		},
	}
}

// makeReasoningAndContentChunk creates a chunk with both reasoning and content.
func makeReasoningAndContentChunk(reasoning, content string) llmchat.Chunk {
	return llmchat.Chunk{
		ID:      "chatcmpl-123",
		Object:  "chat.completion.chunk",
		Created: 1234567890,
		Model:   "gpt-4",
		Choices: []llmchat.ChunkChoice{
			{
				Index: 0,
				Delta: llmchat.Delta{
					Reasoning: &reasoning,
					Content:   &content,
				},
			},
		},
	}
}

func TestProcessChunk_NBufferingDemasker_SimpleReasoning(t *testing.T) {
	// Simple reasoning test: nBufferingDemasker(2) should return output after every 2 chunks
	// data: {reasoning: "Let "}
	// data: {reasoning: "me "} → should flush and return "Let me "
	// data: {reasoning: "think "}
	// data: {reasoning: "about "} → should flush and return "think about "
	// data: {reasoning: "it..."} + EOS → should flush and return "it..."

	p := New(nBufferingDemasker(2))
	ctx := context.Background()

	frame1 := makeFrame(makeReasoningChunk("Let "))
	frame2 := makeFrame(makeReasoningChunk("me "))
	frame3 := makeFrame(makeReasoningChunk("think "))
	frame4 := makeFrame(makeReasoningChunk("about "))
	frame5 := makeFrame(makeReasoningChunk("it..."))

	// First two chunks: should return output after 2nd chunk
	{
		out1, err := p.ProcessChunk(ctx, []byte(frame1), false)
		require.NoError(t, err)
		require.Nil(t, out1, "Expected no output after 1st chunk (buffering)")

		out2, err := p.ProcessChunk(ctx, []byte(frame2), false)
		require.NoError(t, err)
		require.NotNil(t, out2, "Expected output after 2nd chunk")

		frames, _ := splitFrames(out2)
		require.Len(t, frames, 1)
		chunk := parseDataFrame(t, frames[0])
		assert.Equal(t, "Let me ", *chunk.Choices[0].Delta.Reasoning)
		assert.Nil(t, chunk.Choices[0].Delta.Content)
	}

	// Next two chunks: should return output after 4th chunk
	{
		out3, err := p.ProcessChunk(ctx, []byte(frame3), false)
		require.NoError(t, err)
		require.Nil(t, out3, "Expected no output after 3rd chunk (buffering)")

		out4, err := p.ProcessChunk(ctx, []byte(frame4), false)
		require.NoError(t, err)
		require.NotNil(t, out4, "Expected output after 4th chunk")

		frames, _ := splitFrames(out4)
		require.Len(t, frames, 1)
		chunk := parseDataFrame(t, frames[0])
		assert.Equal(t, "think about ", *chunk.Choices[0].Delta.Reasoning)
		assert.Nil(t, chunk.Choices[0].Delta.Content)
	}

	// Last chunk with EOS: should flush immediately
	{
		out5, err := p.ProcessChunk(ctx, []byte(frame5), true)
		require.NoError(t, err)
		require.NotNil(t, out5, "Expected output for EOS")

		frames, _ := splitFrames(out5)
		require.Len(t, frames, 1)
		chunk := parseDataFrame(t, frames[0])
		assert.Equal(t, "it...", *chunk.Choices[0].Delta.Reasoning)
		assert.Nil(t, chunk.Choices[0].Delta.Content)
	}
}

func TestProcessChunk_Reasoning_OnlyReasoningWithEOS(t *testing.T) {
	// Only reasoning chunks, then EOS - should flush reasoning
	// data: {reasoning: "Let me think..."}
	// data: {reasoning: " about this"} + EOS
	// Expected: 1 reasoning frame

	p := New(bufferingDemasker())
	ctx := context.Background()

	frame1 := makeFrame(makeReasoningChunk("Let me think..."))
	frame2 := makeFrame(makeReasoningChunk(" about this"))

	// First chunk: reasoning (demasker buffers, no output)
	out1, err := p.ProcessChunk(ctx, []byte(frame1), false)
	require.NoError(t, err)
	require.Nil(t, out1, "Expected no output for chunk1 (buffering)")

	// Second chunk: reasoning + EOS (flush triggers)
	out2, err := p.ProcessChunk(ctx, []byte(frame2), true)
	require.NoError(t, err)
	require.NotNil(t, out2, "Expected output for chunk2")

	frames, _ := splitFrames(out2)
	require.Len(t, frames, 1, "Expected 1 aggregated reasoning frame")

	frameData := parseDataFrame(t, frames[0])
	require.Len(t, frameData.Choices, 1)
	assert.Equal(t, "Let me think... about this", *frameData.Choices[0].Delta.Reasoning)
	assert.Nil(t, frameData.Choices[0].Delta.Content)
}

func TestProcessChunk_Reasoning_OnlyReasoningThenEOS(t *testing.T) {
	// Only reasoning chunks, then separate EOS
	// data: {reasoning: "Step 1"}
	// data: {reasoning: ", Step 2"}
	// EOS
	// Expected: 1 reasoning frame

	p := New(bufferingDemasker())
	ctx := context.Background()

	frame1 := makeFrame(makeReasoningChunk("Step 1"))
	frame2 := makeFrame(makeReasoningChunk(", Step 2"))

	out1, err := p.ProcessChunk(ctx, []byte(frame1), false)
	require.NoError(t, err)
	require.Nil(t, out1)

	out2, err := p.ProcessChunk(ctx, []byte(frame2), false)
	require.NoError(t, err)
	require.Nil(t, out2)

	// EOS triggers flush
	out3, err := p.ProcessChunk(ctx, []byte{}, true)
	require.NoError(t, err)
	require.NotNil(t, out3)

	frames, _ := splitFrames(out3)
	require.Len(t, frames, 1)

	frameData := parseDataFrame(t, frames[0])
	require.Len(t, frameData.Choices, 1)
	assert.Equal(t, "Step 1, Step 2", *frameData.Choices[0].Delta.Reasoning)
	assert.Nil(t, frameData.Choices[0].Delta.Content)
}

func TestProcessChunk_Reasoning_OnlyReasoningWithDone(t *testing.T) {
	// Only reasoning, then [DONE]
	// data: {reasoning: "Thinking"}
	// data: [DONE] + EOS
	// Expected: reasoning frame, then [DONE]

	p := New(bufferingDemasker())
	ctx := context.Background()

	frame1 := makeFrame(makeReasoningChunk("Thinking"))
	frame2 := makeDoneFrame()

	out, err := p.ProcessChunk(ctx, []byte(frame1+frame2), true)
	require.NoError(t, err)
	require.NotNil(t, out)

	frames, _ := splitFrames(out)
	require.Len(t, frames, 2, "Expected reasoning frame and [DONE]")

	// First frame: reasoning
	reasoningChunk := parseDataFrame(t, frames[0])
	require.Len(t, reasoningChunk.Choices, 1)
	assert.Equal(t, "Thinking", *reasoningChunk.Choices[0].Delta.Reasoning)
	assert.Nil(t, reasoningChunk.Choices[0].Delta.Content)

	// Second frame: [DONE]
	assert.Equal(t, "data: [DONE]", string(bytes.TrimSpace(frames[1])))
}

func TestProcessChunk_Reasoning_OnlyReasoningWithUsage(t *testing.T) {
	// Only reasoning, then usage metadata
	// data: {reasoning: "Analysis"}
	// data: {usage, finish_reason, NO reasoning, NO content} + EOS
	// Expected: reasoning frame, then metadata frame

	p := New(bufferingDemasker())
	ctx := context.Background()

	frame1 := makeFrame(makeReasoningChunk("Analysis"))
	frame2 := makeFrame(makeMetadataChunk())

	out, err := p.ProcessChunk(ctx, []byte(frame1+frame2), true)
	require.NoError(t, err)
	require.NotNil(t, out)

	frames, _ := splitFrames(out)
	require.Len(t, frames, 2, "Expected reasoning and metadata frames")

	// First frame: reasoning
	reasoningChunk := parseDataFrame(t, frames[0])
	require.Len(t, reasoningChunk.Choices, 1)
	assert.Equal(t, "Analysis", *reasoningChunk.Choices[0].Delta.Reasoning)
	assert.Nil(t, reasoningChunk.Choices[0].Delta.Content)
	assert.Nil(t, reasoningChunk.Usage)

	// Second frame: metadata
	mdChunk := parseDataFrame(t, frames[1])
	assert.NotNil(t, mdChunk.Usage)
	assert.NotNil(t, mdChunk.Choices[0].FinishReason)
}

func TestProcessChunk_Reasoning_ThenContentSameChunk(t *testing.T) {
	// Reasoning frames, then content starts in same ProcessChunk call
	// Reasoning must flush BEFORE content is processed
	// data: {reasoning: "First"}
	// data: {reasoning: " Second"}
	// data: {content: "Hello"} + EOS
	// Expected: reasoning frame, then content frame

	p := New(bufferingDemasker())
	ctx := context.Background()

	frame1 := makeFrame(makeReasoningChunk("First"))
	frame2 := makeFrame(makeReasoningChunk(" Second"))
	frame3 := makeFrame(makeContentChunk("Hello"))

	out, err := p.ProcessChunk(ctx, []byte(frame1+frame2+frame3), true)
	require.NoError(t, err)
	require.NotNil(t, out)

	frames, _ := splitFrames(out)
	require.Len(t, frames, 2, "Expected reasoning frame, then content frame")

	// First frame: reasoning (flushed before content started)
	reasoningChunk := parseDataFrame(t, frames[0])
	require.Len(t, reasoningChunk.Choices, 1)
	assert.Equal(t, "First Second", *reasoningChunk.Choices[0].Delta.Reasoning)
	assert.Nil(t, reasoningChunk.Choices[0].Delta.Content)

	// Second frame: content
	contentChunk := parseDataFrame(t, frames[1])
	require.Len(t, contentChunk.Choices, 1)
	assert.Equal(t, "Hello", *contentChunk.Choices[0].Delta.Content)
	assert.Nil(t, contentChunk.Choices[0].Delta.Reasoning)
}

func TestProcessChunk_Reasoning_ThenContentDifferentChunks(t *testing.T) {
	// Reasoning frames in one call, content starts in next call
	// Reasoning must flush when content arrives
	// Call 1: data: {reasoning: "Analyzing"}
	// Call 2: data: {reasoning: " more"}, data: {content: "Result"}
	// Expected: reasoning frame, then content frame

	p := New(bufferingDemasker())
	ctx := context.Background()

	frame1 := makeFrame(makeReasoningChunk("Analyzing"))
	frame2 := makeFrame(makeReasoningChunk(" more"))
	frame3 := makeFrame(makeContentChunk("Result"))

	// First call: only reasoning (buffers)
	out1, err := p.ProcessChunk(ctx, []byte(frame1), false)
	require.NoError(t, err)
	require.Nil(t, out1, "Expected no output (buffering reasoning)")

	// Second call: more reasoning + content (reasoning flushes, then content)
	out2, err := p.ProcessChunk(ctx, []byte(frame2+frame3), true)
	require.NoError(t, err)
	require.NotNil(t, out2)

	frames, _ := splitFrames(out2)
	require.Len(t, frames, 2, "Expected reasoning frame, then content frame")

	// First frame: reasoning
	reasoningChunk := parseDataFrame(t, frames[0])
	require.Len(t, reasoningChunk.Choices, 1)
	assert.Equal(t, "Analyzing more", *reasoningChunk.Choices[0].Delta.Reasoning)
	assert.Nil(t, reasoningChunk.Choices[0].Delta.Content)

	// Second frame: content
	contentChunk := parseDataFrame(t, frames[1])
	require.Len(t, contentChunk.Choices, 1)
	assert.Equal(t, "Result", *contentChunk.Choices[0].Delta.Content)
	assert.Nil(t, contentChunk.Choices[0].Delta.Reasoning)
}

func TestProcessChunk_Reasoning_ThenContentWithUsageAndDone(t *testing.T) {
	// Full flow: reasoning, content, usage, [DONE]
	// data: {reasoning: "Think"}
	// data: {reasoning: " more"}
	// data: {reasoning: " about "}
	// data: {reasoning: "it"}
	// data: {content: "Got "}
	// data: {content: "the "}
	// data: {content: "answer"}
	// data: {usage + finish_reason}
	// data: [DONE] + EOS
	// Expected: reasoning, content, metadata, [DONE]

	p := New(bufferingDemasker())
	ctx := context.Background()

	frame1 := makeFrame(makeReasoningChunk("Think"))
	frame1 += makeFrame(makeReasoningChunk(" more"))
	frame1 += makeFrame(makeReasoningChunk(" about "))
	frame1 += makeFrame(makeReasoningChunk("it"))
	frame2 := makeFrame(makeContentChunk("Got "))
	frame2 += makeFrame(makeContentChunk("the "))
	frame2 += makeFrame(makeContentChunk("answer"))
	frame3 := makeFrame(makeMetadataChunk())
	frame4 := makeDoneFrame()

	out, err := p.ProcessChunk(ctx, []byte(frame1+frame2+frame3+frame4), true)
	require.NoError(t, err)
	require.NotNil(t, out)

	frames, _ := splitFrames(out)
	require.Len(t, frames, 4, "Expected reasoning, content, metadata, [DONE]")

	// Frame 1: reasoning
	reasoningChunk := parseDataFrame(t, frames[0])
	assert.Equal(t, "Think more about it", *reasoningChunk.Choices[0].Delta.Reasoning)
	assert.Nil(t, reasoningChunk.Choices[0].Delta.Content)
	assert.Nil(t, reasoningChunk.Usage)

	// Frame 2: content
	contentChunk := parseDataFrame(t, frames[1])
	assert.Equal(t, "Got the answer", *contentChunk.Choices[0].Delta.Content)
	assert.Nil(t, contentChunk.Choices[0].Delta.Reasoning)
	assert.Nil(t, contentChunk.Usage)

	// Frame 3: metadata
	mdChunk := parseDataFrame(t, frames[2])
	assert.NotNil(t, mdChunk.Usage)
	assert.NotNil(t, mdChunk.Choices[0].FinishReason)
	assert.Nil(t, mdChunk.Choices[0].Delta.Content)
	assert.Nil(t, mdChunk.Choices[0].Delta.Reasoning)

	// Frame 4: [DONE]
	assert.Equal(t, "data: [DONE]", string(bytes.TrimSpace(frames[3])))
}

func TestProcessChunk_Reasoning_BothFieldsInSameFrame(t *testing.T) {
	// A frame with BOTH reasoning and content
	// This signals transition: flush reasoning, then process content
	// data: {reasoning: "Pre", content: "Post"} + EOS
	// Expected: reasoning frame (just "Pre"), then content frame (just "Post")

	p := New(bufferingDemasker())
	ctx := context.Background()

	frame1 := makeFrame(makeReasoningAndContentChunk("Pre", "Post"))

	out, err := p.ProcessChunk(ctx, []byte(frame1), true)
	require.NoError(t, err)
	require.NotNil(t, out)

	frames, _ := splitFrames(out)
	require.Len(t, frames, 2, "Expected reasoning frame, then content frame")

	// Frame 1: reasoning only
	reasoningChunk := parseDataFrame(t, frames[0])
	assert.Equal(t, "Pre", *reasoningChunk.Choices[0].Delta.Reasoning)
	assert.Nil(t, reasoningChunk.Choices[0].Delta.Content)

	// Frame 2: content only
	contentChunk := parseDataFrame(t, frames[1])
	assert.Equal(t, "Post", *contentChunk.Choices[0].Delta.Content)
	assert.Nil(t, contentChunk.Choices[0].Delta.Reasoning)
}

func TestProcessChunk_Reasoning_MultipleReasoningThenBothFieldsThenContent(t *testing.T) {
	// Complex case: multiple reasoning, then a frame with both, then more content
	// data: {reasoning: "A"}
	// data: {reasoning: "B"}
	// data: {reasoning: "C", content: "D"}
	// data: {content: "E"} + EOS
	// Expected: reasoning "ABC", content "DE"

	p := New(bufferingDemasker())
	ctx := context.Background()

	frame1 := makeFrame(makeReasoningChunk("A"))
	frame2 := makeFrame(makeReasoningChunk("B"))
	frame3 := makeFrame(makeReasoningAndContentChunk("C", "D"))
	frame4 := makeFrame(makeContentChunk("E"))

	out, err := p.ProcessChunk(ctx, []byte(frame1+frame2+frame3+frame4), true)
	require.NoError(t, err)
	require.NotNil(t, out)

	frames, _ := splitFrames(out)
	require.Len(t, frames, 2, "Expected reasoning, then content")

	// Frame 1: all reasoning accumulated
	reasoningChunk := parseDataFrame(t, frames[0])
	assert.Equal(t, "ABC", *reasoningChunk.Choices[0].Delta.Reasoning)
	assert.Nil(t, reasoningChunk.Choices[0].Delta.Content)

	// Frame 2: all content accumulated
	contentChunk := parseDataFrame(t, frames[1])
	assert.Equal(t, "DE", *contentChunk.Choices[0].Delta.Content)
	assert.Nil(t, contentChunk.Choices[0].Delta.Reasoning)
}

func TestProcessChunk_Reasoning_MultipleReasoningThenContent_NBufferingDemasker(t *testing.T) {
	// Most common case: multiple reasoning, then a lot of content. Use hasn't to wait until EOS to get completed reasoning
	// data: {reasoning: "Hello "}
	// data: {reasoning: "I'm "}
	// data: {reasoning: "thinking..."}
	// data: {content: "Oh, "}
	// data: {content: "now "}
	// data: {content: "I know"}
	// data: [DONE]
	// EOS
	// Expected: reasoning immediately, content later

	p := New(nBufferingDemasker(2))
	ctx := context.Background()

	frame1 := makeFrame(makeReasoningChunk("Hello "))
	frame1 += makeFrame(makeReasoningChunk("I'm "))
	frame1 += makeFrame(makeReasoningChunk("thinking..."))
	frame2 := makeFrame(makeContentChunk("Oh, "))
	frame2 += makeFrame(makeContentChunk("now "))
	frame3 := makeFrame(makeContentChunk("I know"))
	frame4 := makeDoneFrame()

	{
		out1, err := p.ProcessChunk(ctx, []byte(frame1), false)
		require.NoError(t, err)
		require.NotNil(t, out1)

		// N-buffering demasker buffers first 2 chunks and return them, then it buffers the last one
		// and waits for flush, which should happen immediately when content arrives
		frames1, _ := splitFrames(out1)
		require.Len(t, frames1, 1, "Expected first 2 reasoning chunks")
		chunk1 := parseDataFrame(t, frames1[0])
		assert.Equal(t, "Hello I'm ", *chunk1.Choices[0].Delta.Reasoning)
		assert.Nil(t, chunk1.Choices[0].Delta.Content)
	}

	{
		out2, err := p.ProcessChunk(ctx, []byte(frame2), false)
		require.NoError(t, err)
		require.NotNil(t, out2)

		// The processor have received first 2 content chunks, so it flushed last reasoning.
		// Also N-buffering demasker has processed first 2 content chunks and returned them
		frames2, _ := splitFrames(out2)
		require.Len(t, frames2, 2, "Expected last reasoning chunks and first 2 content chunks")
		// Parse reasoning chunk
		chunk2 := parseDataFrame(t, frames2[0])
		assert.Equal(t, "thinking...", *chunk2.Choices[0].Delta.Reasoning)
		assert.Nil(t, chunk2.Choices[0].Delta.Content)
		// Parse content
		chunk3 := parseDataFrame(t, frames2[1])
		assert.Equal(t, "Oh, now ", *chunk3.Choices[0].Delta.Content)
		assert.Nil(t, chunk3.Choices[0].Delta.Reasoning)
	}

	{
		// Sending last content chunk and [DONE] + EOS
		out3, err := p.ProcessChunk(ctx, []byte(frame3+frame4), true)
		require.NoError(t, err)
		require.NotNil(t, out3)

		// The processor have received first 2 content chunks, so it flushed last reasoning.
		// Also N-buffering demasker has processed first 2 content chunks and returned them
		frames3, _ := splitFrames(out3)
		require.Len(t, frames3, 2, "Expected last content chunk and [DONE]")
		// Parse content chunk
		chunk4 := parseDataFrame(t, frames3[0])
		assert.Equal(t, "I know", *chunk4.Choices[0].Delta.Content)
		assert.Nil(t, chunk4.Choices[0].Delta.Reasoning)
		// Parse content
		assert.Equal(t, "data: [DONE]", strings.TrimSpace(string(frames3[1])))
	}
}

// ============================================================================
// MULTIPLE CHOICES TESTS
// ============================================================================

// makeMultiChoiceContentChunk creates a chunk with content for multiple choices.
func makeMultiChoiceContentChunk(contents ...string) llmchat.Chunk {
	choices := make([]llmchat.ChunkChoice, len(contents))
	for i, content := range contents {
		c := content
		choices[i] = llmchat.ChunkChoice{
			Index: i,
			Delta: llmchat.Delta{
				Content: &c,
			},
		}
	}
	return llmchat.Chunk{
		ID:      "chatcmpl-123",
		Object:  "chat.completion.chunk",
		Created: 1234567890,
		Model:   "gpt-4",
		Choices: choices,
	}
}

// makeMultiChoiceReasoningChunk creates a chunk with reasoning for multiple choices.
func makeMultiChoiceReasoningChunk(reasonings ...string) llmchat.Chunk {
	choices := make([]llmchat.ChunkChoice, len(reasonings))
	for i, reasoning := range reasonings {
		r := reasoning
		choices[i] = llmchat.ChunkChoice{
			Index: i,
			Delta: llmchat.Delta{
				Reasoning: &r,
			},
		}
	}
	return llmchat.Chunk{
		ID:      "chatcmpl-123",
		Object:  "chat.completion.chunk",
		Created: 1234567890,
		Model:   "gpt-4",
		Choices: choices,
	}
}

// makeChoiceWithIndex creates a single-choice chunk for a specific index.
func makeChoiceWithIndex(idx int, content, reasoning string) llmchat.Chunk {
	choice := llmchat.ChunkChoice{
		Index: idx,
		Delta: llmchat.Delta{},
	}
	if content != "" {
		choice.Delta.Content = &content
	}
	if reasoning != "" {
		choice.Delta.Reasoning = &reasoning
	}
	return llmchat.Chunk{
		ID:      "chatcmpl-123",
		Object:  "chat.completion.chunk",
		Created: 1234567890,
		Model:   "gpt-4",
		Choices: []llmchat.ChunkChoice{choice},
	}
}

// makeMetadataChunkForChoice creates a metadata chunk with finish_reason "stop" for specific choice.
func makeMetadataChunkForChoice(choiceIdx int, includeUsage bool) llmchat.Chunk {
	finishReason := "stop"
	chunk := llmchat.Chunk{
		ID:      "chatcmpl-123",
		Object:  "chat.completion.chunk",
		Created: 1234567890,
		Model:   "gpt-4",
		Choices: []llmchat.ChunkChoice{
			{
				Index:        choiceIdx,
				Delta:        llmchat.Delta{},
				FinishReason: &finishReason,
			},
		},
	}
	if includeUsage {
		chunk.Usage = &llmchat.Usage{
			PromptTokens:     10,
			CompletionTokens: 20,
			TotalTokens:      30,
		}
	}
	return chunk
}

func TestProcessChunk_MultiChoice_TwoChoicesContentSimultaneous(t *testing.T) {
	// Two choices streaming simultaneously in the same frames
	// data: {choice0: "Hello ", choice1: "Bonjour "}
	// data: {choice0: "World!", choice1: "Monde!"}
	// data: {choice0: finish_reason, choice1: finish_reason, usage} + EOS
	// Expected: 2 content frames (one per choice), then metadata frame

	p := New(bufferingDemasker())
	ctx := context.Background()

	frame1 := makeFrame(makeMultiChoiceContentChunk("Hello ", "Bonjour "))
	frame2 := makeFrame(makeMultiChoiceContentChunk("World!", "Monde!"))

	// Create metadata chunk with both choices finishing
	metadataChunk := llmchat.Chunk{
		ID:      "chatcmpl-123",
		Object:  "chat.completion.chunk",
		Created: 1234567890,
		Model:   "gpt-4",
		Choices: []llmchat.ChunkChoice{
			{Index: 0, FinishReason: toPtr("stop")},
			{Index: 1, FinishReason: toPtr("stop")},
		},
		Usage: &llmchat.Usage{PromptTokens: 10, CompletionTokens: 20, TotalTokens: 30},
	}
	frame3 := makeFrame(metadataChunk)

	out, err := p.ProcessChunk(ctx, []byte(frame1+frame2+frame3), true)
	require.NoError(t, err)
	require.NotNil(t, out)

	frames, _ := splitFrames(out)
	// Expect: choice 0 content, choice 1 content, metadata (with both finish_reason + usage)
	require.Len(t, frames, 3, "Expected 2 content frames + 1 metadata frame")

	// Choice 0 content
	chunk0 := parseDataFrame(t, frames[0])
	require.Len(t, chunk0.Choices, 1)
	assert.Equal(t, 0, chunk0.Choices[0].Index)
	assert.Equal(t, "Hello World!", *chunk0.Choices[0].Delta.Content)
	assert.Nil(t, chunk0.Usage)

	// Choice 1 content
	chunk1 := parseDataFrame(t, frames[1])
	require.Len(t, chunk1.Choices, 1)
	assert.Equal(t, 1, chunk1.Choices[0].Index)
	assert.Equal(t, "Bonjour Monde!", *chunk1.Choices[0].Delta.Content)
	assert.Nil(t, chunk1.Usage)

	// Metadata
	mdChunk := parseDataFrame(t, frames[2])
	assert.NotNil(t, mdChunk.Usage)
	require.Len(t, mdChunk.Choices, 2)
	assert.NotNil(t, mdChunk.Choices[0].FinishReason)
	assert.NotNil(t, mdChunk.Choices[1].FinishReason)
}

func TestProcessChunk_MultiChoice_TwoChoicesContentSeparate(t *testing.T) {
	// Two choices streaming separately (one frame per choice)
	// data: {choice0: "Hello "}
	// data: {choice1: "Bonjour "}
	// data: {choice0: "World!"}
	// data: {choice1: "Monde!"}
	// data: {choice0: finish_reason}
	// data: {choice1: finish_reason, usage} + EOS
	// Expected: choice0 content, choice1 content, metadata

	p := New(bufferingDemasker())
	ctx := context.Background()

	frame1 := makeFrame(makeChoiceWithIndex(0, "Hello ", ""))
	frame2 := makeFrame(makeChoiceWithIndex(1, "Bonjour ", ""))
	frame3 := makeFrame(makeChoiceWithIndex(0, "World!", ""))
	frame4 := makeFrame(makeChoiceWithIndex(1, "Monde!", ""))
	frame5 := makeFrame(makeMetadataChunkForChoice(0, false))
	frame6 := makeFrame(makeMetadataChunkForChoice(1, true))

	out, err := p.ProcessChunk(ctx, []byte(frame1+frame2+frame3+frame4+frame5+frame6), true)
	require.NoError(t, err)
	require.NotNil(t, out)

	frames, _ := splitFrames(out)
	// When choices finish separately, output is:
	// choice 0 content, choice 0 metadata, choice 1 content, choice 1 metadata
	require.Len(t, frames, 4, "Expected at least 4 frames")

	// Verify both choices have content frames (they may not be in first 2 frames)
	foundChoice0 := false
	foundChoice1 := false
	for _, frame := range frames {
		chunk := parseDataFrame(t, frame)
		if len(chunk.Choices) > 0 {
			if chunk.Choices[0].Index == 0 && chunk.Choices[0].Delta.Content != nil {
				assert.Equal(t, "Hello World!", *chunk.Choices[0].Delta.Content)
				foundChoice0 = true
			}
			if chunk.Choices[0].Index == 1 && chunk.Choices[0].Delta.Content != nil {
				assert.Equal(t, "Bonjour Monde!", *chunk.Choices[0].Delta.Content)
				foundChoice1 = true
			}
		}
	}
	assert.True(t, foundChoice0, "Expected choice 0 content frame")
	assert.True(t, foundChoice1, "Expected choice 1 content frame")
}

func TestProcessChunk_MultiChoice_ZeroFinishesFirst(t *testing.T) {
	// Choice 0 finishes before choice 1 - should flush choice 0 immediately
	// data: {choice0: "Quick ", choice1: "This "}
	// data: {choice0: "answer", choice1: "is "}
	// data: {choice0: finish_reason} ← choice 0 finishes, should flush immediately
	// data: {choice1: "longer "} ← choice 1 should flush, because N-buffering demasker
	// data: {choice1: "text"}
	// data: {choice1: finish_reason, usage} + EOS
	// Expected: choice0 content (immediate), then choice1 content, then metadata

	p := New(nBufferingDemasker(3))
	ctx := context.Background()

	frame1 := makeFrame(makeMultiChoiceContentChunk("Quick ", "This "))
	frame2 := makeFrame(makeMultiChoiceContentChunk("answer ", "is "))
	frame3 := makeFrame(makeMetadataChunkForChoice(0, false))
	frame4 := makeFrame(makeChoiceWithIndex(1, "longer ", ""))
	frame5 := makeFrame(makeChoiceWithIndex(1, "text", ""))
	frame6 := makeFrame(makeMetadataChunkForChoice(1, true))

	// Process in steps to verify immediate flushing
	{
		// First chunk: both choices get content
		out1, err := p.ProcessChunk(ctx, []byte(frame1), false)
		require.NoError(t, err)
		assert.Nil(t, out1, "Expected buffering, no output yet")
	}

	{
		// Second chunk: both choices get content
		out2, err := p.ProcessChunk(ctx, []byte(frame2), false)
		require.NoError(t, err)
		assert.Nil(t, out2, "Expected buffering, no output yet")
	}

	{
		// Third chunk: choice 0 finishes - should flush choice 0 immediately
		out3, err := p.ProcessChunk(ctx, []byte(frame3), false)
		require.NoError(t, err)
		require.NotNil(t, out3, "Expected choice 0 to flush on finish_reason")

		frames, _ := splitFrames(out3)
		require.Len(t, frames, 2, "Expected choice 0 content + metadata")

		// First should be choice 0 content
		chunk0 := parseDataFrame(t, frames[0])
		require.Len(t, chunk0.Choices, 1)
		assert.Equal(t, 0, chunk0.Choices[0].Index)
		assert.Equal(t, "Quick answer ", *chunk0.Choices[0].Delta.Content)

		// Second should be choice 0 finish reason
		chunk1 := parseDataFrame(t, frames[1])
		require.Len(t, chunk1.Choices, 1)
		assert.Equal(t, 0, chunk1.Choices[0].Index)
		assert.Nil(t, chunk1.Choices[0].Delta.Content)
		assert.NotNil(t, chunk1.Choices[0].FinishReason)
	}

	{
		// Forth chunk: choice 1 flushes because of the demasker
		out4, err := p.ProcessChunk(ctx, []byte(frame4), false)
		require.NoError(t, err)
		require.NotNil(t, out4, "Expected choice 1 to flush because of the demasker")

		frames, _ := splitFrames(out4)
		require.Len(t, frames, 1, "Expected choice 1 content")

		// First should be choice 0 content
		chunk0 := parseDataFrame(t, frames[0])
		require.Len(t, chunk0.Choices, 1)
		assert.Equal(t, 1, chunk0.Choices[0].Index)
		assert.Equal(t, "This is longer ", *chunk0.Choices[0].Delta.Content)
	}

	{
		// Continue with choice 1
		out, err := p.ProcessChunk(ctx, []byte(frame5+frame6), true)
		require.NoError(t, err)
		require.NotNil(t, out, "Expected choice 1 content and metadata")

		frames, _ := splitFrames(out)
		require.Len(t, frames, 2, "Expected choice 1 content + metadata")

		// Should have choice 1 content
		chunk1 := parseDataFrame(t, frames[0])
		require.Len(t, chunk1.Choices, 1)
		assert.Equal(t, 1, chunk1.Choices[0].Index)
		assert.Equal(t, "text", *chunk1.Choices[0].Delta.Content)

		// Should have choice 1 metadata (finish reason + usage)
		dataChunk := parseDataFrame(t, frames[1])
		require.Len(t, dataChunk.Choices, 1)
		assert.Equal(t, 1, dataChunk.Choices[0].Index)
		assert.NotNil(t, dataChunk.Choices[0].FinishReason)
		assert.NotNil(t, dataChunk.Usage)
	}
}

func TestProcessChunk_MultiChoice_WithReasoning(t *testing.T) {
	// Two choices, both with reasoning then content
	// Choice 0: reasoning → content → finish
	// Choice 1: reasoning → content → finish
	// Both stream simultaneously
	// data: {choice0: reasoning:"Think0", choice1: reasoning:"Think1"}
	// data: {choice0: content:"Answer0", choice1: content:"Answer1"}
	// data: {choice0: finish, choice1: finish, usage} + EOS
	// Expected: choice0 reasoning, choice1 reasoning, choice0 content, choice1 content, metadata

	p := New(bufferingDemasker())
	ctx := context.Background()

	frame1 := makeFrame(makeMultiChoiceReasoningChunk("Think0", "Think1"))

	chunk2 := llmchat.Chunk{
		ID:      "chatcmpl-123",
		Object:  "chat.completion.chunk",
		Created: 1234567890,
		Model:   "gpt-4",
		Choices: []llmchat.ChunkChoice{
			{Index: 0, Delta: llmchat.Delta{Content: toPtr("Answer0")}},
			{Index: 1, Delta: llmchat.Delta{Content: toPtr("Answer1")}},
		},
	}
	frame2 := makeFrame(chunk2)

	metadataChunk := llmchat.Chunk{
		ID:      "chatcmpl-123",
		Object:  "chat.completion.chunk",
		Created: 1234567890,
		Model:   "gpt-4",
		Choices: []llmchat.ChunkChoice{
			{Index: 0, Delta: llmchat.Delta{}, FinishReason: toPtr("stop")},
			{Index: 1, Delta: llmchat.Delta{}, FinishReason: toPtr("stop")},
		},
		Usage: &llmchat.Usage{PromptTokens: 10, CompletionTokens: 20, TotalTokens: 30},
	}
	frame3 := makeFrame(metadataChunk)

	out, err := p.ProcessChunk(ctx, []byte(frame1+frame2+frame3), true)
	require.NoError(t, err)
	require.NotNil(t, out)

	frames, _ := splitFrames(out)
	// Expected: 2 reasoning frames + 2 content frames + 1 metadata = 5 frames
	require.Len(t, frames, 5, "Expected 2 reasoning + 2 content + 1 metadata")

	// Reasoning frames
	r0 := parseDataFrame(t, frames[0])
	assert.Equal(t, 0, r0.Choices[0].Index)
	assert.Equal(t, "Think0", *r0.Choices[0].Delta.Reasoning)
	assert.Nil(t, r0.Choices[0].Delta.Content)

	r1 := parseDataFrame(t, frames[1])
	assert.Equal(t, 1, r1.Choices[0].Index)
	assert.Equal(t, "Think1", *r1.Choices[0].Delta.Reasoning)
	assert.Nil(t, r1.Choices[0].Delta.Content)

	// Content frames
	c0 := parseDataFrame(t, frames[2])
	assert.Equal(t, 0, c0.Choices[0].Index)
	assert.Equal(t, "Answer0", *c0.Choices[0].Delta.Content)
	assert.Nil(t, c0.Choices[0].Delta.Reasoning)

	c1 := parseDataFrame(t, frames[3])
	assert.Equal(t, 1, c1.Choices[0].Index)
	assert.Equal(t, "Answer1", *c1.Choices[0].Delta.Content)
	assert.Nil(t, c1.Choices[0].Delta.Reasoning)

	// Metadata
	md := parseDataFrame(t, frames[4])
	assert.NotNil(t, md.Usage)
}

func TestProcessChunk_MultiChoice_ThreeChoicesDifferentFinishTimes(t *testing.T) {
	// Three choices finishing at different times
	// Choice 0: 1 chunk, finishes first
	// Choice 1: 2 chunks, finishes second
	// Choice 2: 3 chunks, finishes last with usage

	p := New(bufferingDemasker())
	ctx := context.Background()

	// All start together
	frame1 := makeFrame(makeMultiChoiceContentChunk("A", "X", "One"))

	// Choice 0 finishes
	frame2 := makeFrame(makeMetadataChunkForChoice(0, false))

	// Choices 1 and 2 continue
	chunk3 := llmchat.Chunk{
		ID:      "chatcmpl-123",
		Object:  "chat.completion.chunk",
		Created: 1234567890,
		Model:   "gpt-4",
		Choices: []llmchat.ChunkChoice{
			{Index: 1, Delta: llmchat.Delta{Content: toPtr("Y")}},
			{Index: 2, Delta: llmchat.Delta{Content: toPtr("Two")}},
		},
	}
	frame3 := makeFrame(chunk3)

	// Choice 1 finishes
	frame4 := makeFrame(makeMetadataChunkForChoice(1, false))

	// Choice 2 continues and finishes
	frame5 := makeFrame(makeChoiceWithIndex(2, "Three", ""))
	frame6 := makeFrame(makeMetadataChunkForChoice(2, true))

	{
		// Frame 1: all buffering
		out1, err := p.ProcessChunk(ctx, []byte(frame1), false)
		require.NoError(t, err)
		assert.Nil(t, out1, "Buffering")
	}

	{
		// Frame 2: choice 0 finishes - should flush
		out2, err := p.ProcessChunk(ctx, []byte(frame2), false)
		require.NoError(t, err)
		require.NotNil(t, out2, "Choice 0 should flush")

		frames, _ := splitFrames(out2)
		require.Len(t, frames, 2)

		chunk := parseDataFrame(t, frames[0])
		require.Len(t, chunk.Choices, 1)
		assert.Equal(t, 0, chunk.Choices[0].Index)
		assert.Equal(t, "A", *chunk.Choices[0].Delta.Content)

		mdChunk := parseDataFrame(t, frames[1])
		require.Len(t, mdChunk.Choices, 1)
		assert.Equal(t, 0, mdChunk.Choices[0].Index)
		assert.NotNil(t, mdChunk.Choices[0].FinishReason)
		assert.Nil(t, mdChunk.Usage) // No usage in this chunk
	}

	{
		// Frame 3: choices 1 and 2 continue
		out, err := p.ProcessChunk(ctx, []byte(frame3), false)
		require.NoError(t, err)
		// Should not have output with buffering demasker behavior
		require.Nil(t, out)
	}

	{
		// Frame 4: choice 1 finishes - should flush
		out4, err := p.ProcessChunk(ctx, []byte(frame4), false)
		require.NoError(t, err)
		require.NotNil(t, out4, "Choice 1 should flush")

		frames, _ := splitFrames(out4)
		chunk := parseDataFrame(t, frames[0])
		assert.Equal(t, 1, chunk.Choices[0].Index)
		assert.Equal(t, "XY", *chunk.Choices[0].Delta.Content)
	}

	{
		// Frames 5-6: choice 2 continues and finishes
		out5, err := p.ProcessChunk(ctx, []byte(frame5+frame6), true)
		require.NoError(t, err)
		require.NotNil(t, out5, "Choice 2 should flush")

		frames, _ := splitFrames(out5)
		chunk := parseDataFrame(t, frames[0])
		assert.Equal(t, 2, chunk.Choices[0].Index)
		assert.Equal(t, "OneTwoThree", *chunk.Choices[0].Delta.Content)
	}
}

func TestProcessChunk_MultiChoice_MixedReasoningOneChoiceOnly(t *testing.T) {
	// Only choice 0 has reasoning, choice 1 has only content
	// data: {choice0: reasoning:"Think"}
	// data: {choice0: content:"Answer0", choice1: content:"Answer1"}
	// data: {choice0: finish, choice1: finish} + EOS
	// Expected: choice0 reasoning, choice0 content, choice1 content, metadata

	p := New(bufferingDemasker())
	ctx := context.Background()

	frame1 := makeFrame(makeChoiceWithIndex(0, "", "Think"))

	chunk2 := llmchat.Chunk{
		ID:      "chatcmpl-123",
		Object:  "chat.completion.chunk",
		Created: 1234567890,
		Model:   "gpt-4",
		Choices: []llmchat.ChunkChoice{
			{Index: 0, Delta: llmchat.Delta{Content: toPtr("Answer0")}},
			{Index: 1, Delta: llmchat.Delta{Content: toPtr("Answer1")}},
		},
	}
	frame2 := makeFrame(chunk2)

	metadataChunk := llmchat.Chunk{
		ID:      "chatcmpl-123",
		Object:  "chat.completion.chunk",
		Created: 1234567890,
		Model:   "gpt-4",
		Choices: []llmchat.ChunkChoice{
			{Index: 0, Delta: llmchat.Delta{}, FinishReason: toPtr("stop")},
			{Index: 1, Delta: llmchat.Delta{}, FinishReason: toPtr("stop")},
		},
		Usage: &llmchat.Usage{PromptTokens: 10, CompletionTokens: 20, TotalTokens: 30},
	}
	frame3 := makeFrame(metadataChunk)

	out, err := p.ProcessChunk(ctx, []byte(frame1+frame2+frame3), true)
	require.NoError(t, err)
	require.NotNil(t, out)

	frames, _ := splitFrames(out)
	// Expected: choice0 reasoning, choice0 content, choice1 content, metadata
	require.Len(t, frames, 4)

	// Choice 0 reasoning
	r0 := parseDataFrame(t, frames[0])
	assert.Equal(t, 0, r0.Choices[0].Index)
	assert.Equal(t, "Think", *r0.Choices[0].Delta.Reasoning)

	// Choice 0 content
	c0 := parseDataFrame(t, frames[1])
	assert.Equal(t, 0, c0.Choices[0].Index)
	assert.Equal(t, "Answer0", *c0.Choices[0].Delta.Content)

	// Choice 1 content
	c1 := parseDataFrame(t, frames[2])
	assert.Equal(t, 1, c1.Choices[0].Index)
	assert.Equal(t, "Answer1", *c1.Choices[0].Delta.Content)

	// Metadata
	md := parseDataFrame(t, frames[3])
	require.Len(t, md.Choices, 2)
	assert.NotNil(t, md.Choices[0].FinishReason)
	assert.NotNil(t, md.Choices[1].FinishReason)
	assert.NotNil(t, md.Usage)
}

// ============================================================================
// DEMASKER ERROR BACKOFF TESTS
// ============================================================================

// errorDemasker returns a demasker that always fails.
func errorDemasker() common.DemaskerFactoryFn {
	return func() common.Demasker {
		return newDemaskerMock(
			func(ctx context.Context, chunk string, flush bool) (string, error) {
				// New Demasker contract: on error, hand back the un-emitted
				// content (here just the current chunk — this mock never buffers).
				return chunk, fmt.Errorf("demasker error: simulated failure")
			})
	}
}

// partialErrorDemasker returns chunks successfully N times, then errors.
// It flushes every N chunks, and errors after flushCount flushes.
func partialErrorDemasker(flushAfter, flushCount int) common.DemaskerFactoryFn {
	return func() common.Demasker {
		count := 0
		flushes := 0
		buf := ""
		return newDemaskerMock(
			func(ctx context.Context, chunk string, flush bool) (string, error) {
				count++
				buf += chunk

				// Check if we should error. Per the Demasker contract, hand back
				// the un-emitted buffered content so the caller can emit it.
				if flushes >= flushCount {
					lost := buf
					buf = ""
					return lost, fmt.Errorf("demasker error: failed after %d flushes", flushCount)
				}

				// Flush every N chunks or on explicit flush
				if count%flushAfter == 0 || flush {
					result := buf
					buf = ""
					flushes++
					return result, nil
				}

				// Buffer
				return "", nil
			})
	}
}

// bufferingThenErrorDemasker simulates a demasker that returns partial buffered content, then errors.
// It buffers chunks and returns only the first chunk on the returnTrigger-th call, then errors on errorTrigger-th call.
func bufferingThenErrorDemasker(returnTrigger, errorTrigger int) common.DemaskerFactoryFn {
	return func() common.Demasker {
		count := 0
		chunks := []string{}
		return newDemaskerMock(
			func(ctx context.Context, chunk string, flush bool) (string, error) {
				if chunk != "" {
					count++
					chunks = append(chunks, chunk)
				}

				// Error on errorTrigger. Per the Demasker contract, hand back the
				// un-emitted buffered content (everything not yet returned).
				if count >= errorTrigger {
					lost := strings.Join(chunks, "")
					chunks = nil
					return lost, fmt.Errorf("demasker error: at chunk %d", count)
				}

				// Return first chunk on returnTrigger
				if count == returnTrigger {
					// Return only the first chunk, keep the rest in buffer
					result := chunks[0]
					chunks = chunks[1:] // Keep remaining in buffer (will be lost on error)
					return result, nil
				}

				// Flush all on explicit flush
				if flush && len(chunks) > 0 {
					result := ""
					for _, c := range chunks {
						result += c
					}
					chunks = nil
					return result, nil
				}

				return "", nil
			})
	}
}

// TestProcessChunk_DemaskerError_AfterLengthChangingEmitIsLossless is the
// regression for the masked-accumulator trim bug: after a successful
// length-changing emit (placeholder shorter than its value), a later demask
// error must still emit the remaining content losslessly — no dropped or
// duplicated bytes. The demasker returns its un-emitted content on error.
func TestProcessChunk_DemaskerError_AfterLengthChangingEmitIsLossless(t *testing.T) {
	// First content chunk demasks "<P>"(3) -> "REALVALUE"(9) successfully;
	// the second errors and must be emitted verbatim as the fallback.
	calls := 0
	lengthChangingThenError := func() common.Demasker {
		return newDemaskerMock(func(_ context.Context, chunk string, _ bool) (string, error) {
			calls++
			if calls > 1 {
				return chunk, fmt.Errorf("demasker error")
			}
			return strings.ReplaceAll(chunk, "<P>", "REALVALUE"), nil
		})
	}

	p := New(lengthChangingThenError)
	ctx := context.Background()

	out1, err := p.ProcessChunk(ctx, []byte(makeFrame(makeContentChunk("x<P>y"))), false)
	require.NoError(t, err)
	require.NotNil(t, out1)
	frames1, _ := splitFrames(out1)
	require.Len(t, frames1, 1)
	assert.Equal(t, "xREALVALUEy", *parseDataFrame(t, frames1[0]).Choices[0].Delta.Content,
		"first (successful) delta demasked")

	out2, err := p.ProcessChunk(ctx, []byte(makeFrame(makeContentChunk("TAILDATA"))), false)
	require.NoError(t, err)
	require.NotNil(t, out2)
	frames2, _ := splitFrames(out2)
	require.Len(t, frames2, 1)
	assert.Equal(t, "TAILDATA", *parseDataFrame(t, frames2[0]).Choices[0].Delta.Content,
		"second (errored) delta emitted losslessly, no trim corruption")
}

func TestProcessChunk_DemaskerError_FailsOnEverything(t *testing.T) {
	// Scenario 1: Demasker fails on every chunk
	// Expected: Should fall back to the demasker's un-emitted content
	// data: {content: "Hello "}
	// data: {content: "World!"}
	// EOS
	// Demasker errors on all chunks
	// Expected: Output frame with "Hello World!" from fallback

	p := New(errorDemasker())
	ctx := context.Background()

	frame1 := makeFrame(makeContentChunk("Hello "))
	frame2 := makeFrame(makeContentChunk("World!"))

	{
		// First chunk: demasker errors, should output immediate fallback
		out1, err := p.ProcessChunk(ctx, []byte(frame1), false)
		require.NoError(t, err, "ProcessChunk should not error even if demasker errors")
		require.NotNil(t, out1, "Expected output on first chunk, demasker has failed")

		frames, _ := splitFrames(out1)
		require.Len(t, frames, 1)
		chunk := parseDataFrame(t, frames[0])

		require.Len(t, chunk.Choices, 1)
		assert.Equal(t, "Hello ", *chunk.Choices[0].Delta.Content)
	}

	{
		// Second chunk: demasker errors again, outputs immediate fallback
		out2, err := p.ProcessChunk(ctx, []byte(frame2), false)
		require.NoError(t, err)
		require.NotNil(t, out2, "Expected output on second chunk, demasker has failed")

		frames, _ := splitFrames(out2)
		require.Len(t, frames, 1)
		chunk := parseDataFrame(t, frames[0])

		require.Len(t, chunk.Choices, 1)
		assert.Equal(t, "World!", *chunk.Choices[0].Delta.Content)
	}

	// EOS: no output (all chunks already sent as fallback)
	out3, err := p.ProcessChunk(ctx, []byte{}, true)
	require.NoError(t, err)
	require.Nil(t, out3, "Expected no output on EOS (all chunks already sent)")
}

func TestProcessChunk_DemaskerError_SomeContentThenFails(t *testing.T) {
	// Scenario 2: Demasker successfully returns content, then fails
	// data: {content: "A"}
	// data: {content: "B"}  -> demasker flushes "AB"
	// data: {content: "C"}
	// data: {content: "D"}  -> demasker errors
	// EOS -> should flush "CD" from fallback
	// Expected: First output "AB" (demasked), then "CD" (fallback)

	p := New(partialErrorDemasker(2, 1)) // Flush every 2 chunks, allow 1 flush, then error
	ctx := context.Background()

	frame1 := makeFrame(makeContentChunk("A"))
	frame2 := makeFrame(makeContentChunk("B"))
	frame3 := makeFrame(makeContentChunk("C"))
	frame4 := makeFrame(makeContentChunk("D"))

	// First chunk: buffering
	out1, err := p.ProcessChunk(ctx, []byte(frame1), false)
	require.NoError(t, err)
	require.Nil(t, out1, "Buffering first chunk")

	// Second chunk: demasker flushes "AB"
	out2, err := p.ProcessChunk(ctx, []byte(frame2), false)
	require.NoError(t, err)
	require.NotNil(t, out2, "Demasker should flush AB after 2 chunks")

	frames2, _ := splitFrames(out2)
	require.Len(t, frames2, 1)
	chunk2 := parseDataFrame(t, frames2[0])
	assert.Equal(t, "AB", *chunk2.Choices[0].Delta.Content, "Should have demasked AB")

	// Third chunk: demasker starts erroring (flushCount exceeded) - IMMEDIATE FALLBACK
	out3, err := p.ProcessChunk(ctx, []byte(frame3), false)
	require.NoError(t, err)
	require.NotNil(t, out3, "Should output fallback immediately")

	frames3, _ := splitFrames(out3)
	require.Len(t, frames3, 1)
	chunk3 := parseDataFrame(t, frames3[0])
	assert.Equal(t, "C", *chunk3.Choices[0].Delta.Content, "Should output C immediately as fallback")

	// Fourth chunk: demasker still errors - IMMEDIATE FALLBACK
	out4, err := p.ProcessChunk(ctx, []byte(frame4), false)
	require.NoError(t, err)
	require.NotNil(t, out4, "Should output fallback immediately")

	frames4, _ := splitFrames(out4)
	require.Len(t, frames4, 1)
	chunk4 := parseDataFrame(t, frames4[0])
	assert.Equal(t, "D", *chunk4.Choices[0].Delta.Content, "Should output D immediately as fallback")

	// EOS: no more output (all chunks already sent)
	outEOS, err := p.ProcessChunk(ctx, []byte{}, true)
	require.NoError(t, err)
	require.Nil(t, outEOS, "Expected no output on EOS (all chunks already sent)")
}

func TestProcessChunk_DemaskerError_BufferLostOnError(t *testing.T) {
	// Scenario 3: Demasker consumes 2 chunks, returns first one, then fails on third
	// Chunks 1 and 2 are consumed but demasker only returned chunk 1
	// Chunk 3 arrives and demasker fails (clears its buffer, losing chunk 2)
	// data: {content: "A"}
	// data: {content: "B"} -> demasker returns "A" (chunk B still in demasker buffer)
	// data: {content: "C"} -> demasker errors and clears buffer (chunk B lost in demasker)
	// EOS -> should flush "BC" from fallback (choicesAccum has all "ABC", we already sent "A")
	// Expected: "A" (demasked), then "BC" (fallback for what demasker didn't flush)

	p := New(bufferingThenErrorDemasker(2, 3)) // Return on 2nd chunk, error on 3rd
	ctx := context.Background()

	frame1 := makeFrame(makeContentChunk("A"))
	frame2 := makeFrame(makeContentChunk("B"))
	frame3 := makeFrame(makeContentChunk("C"))

	// First chunk: buffering
	out1, err := p.ProcessChunk(ctx, []byte(frame1), false)
	require.NoError(t, err)
	require.Nil(t, out1, "Buffering first chunk")

	// Second chunk: demasker returns "A" (keeps "B" in buffer)
	out2, err := p.ProcessChunk(ctx, []byte(frame2), false)
	require.NoError(t, err)
	require.NotNil(t, out2, "Demasker returns first chunk")

	frames2, _ := splitFrames(out2)
	require.Len(t, frames2, 1)
	chunk2 := parseDataFrame(t, frames2[0])
	assert.Equal(t, "A", *chunk2.Choices[0].Delta.Content, "Demasker returned first chunk only")

	// Third chunk: demasker errors (loses "B" from its buffer)
	// IMMEDIATE FALLBACK: outputs all unsent content from choicesAccum ("BC")
	out3, err := p.ProcessChunk(ctx, []byte(frame3), false)
	require.NoError(t, err)
	require.NotNil(t, out3, "Demasker errored, should output immediate fallback")

	frames3, _ := splitFrames(out3)
	require.Len(t, frames3, 1, "Expected 1 immediate fallback frame")

	// Fallback should contain "BC" (all unsent data: "B" lost in demasker buffer + "C" from current chunk)
	chunk3 := parseDataFrame(t, frames3[0])
	assert.Equal(t, "BC", *chunk3.Choices[0].Delta.Content, "Fallback should output all unsent data including demasker's lost buffer")

	// EOS: no more output (all chunks already sent)
	outEOS, err := p.ProcessChunk(ctx, []byte{}, true)
	require.NoError(t, err)
	require.Nil(t, outEOS, "Expected no output on EOS (all chunks already sent)")
}

// ============================================================================
// TOOL CALLS TESTS
// ============================================================================

// makeToolCallChunk creates a Chunk with a tool call delta.
func makeToolCallChunk(toolIndex int, id, typ, name, arguments string) llmchat.Chunk {
	toolCall := llmchat.ToolCallDelta{
		Index: toolIndex,
	}
	if id != "" {
		toolCall.ID = id
	}
	if typ != "" {
		toolCall.Type = typ
	}
	if name != "" || arguments != "" {
		toolCall.Function = &llmchat.FunctionCallDelta{
			Name:      name,
			Arguments: arguments,
		}
	}

	return llmchat.Chunk{
		ID:      "chatcmpl-123",
		Object:  "chat.completion.chunk",
		Created: 1234567890,
		Model:   "gpt-4",
		Choices: []llmchat.ChunkChoice{
			{
				Index: 0,
				Delta: llmchat.Delta{
					ToolCalls: []llmchat.ToolCallDelta{toolCall},
				},
			},
		},
	}
}

// makeToolCallChunkWithContent creates a chunk with both tool call and content.
func makeToolCallChunkWithContent(toolIndex int, id, typ, name, arguments, content string) llmchat.Chunk {
	chunk := makeToolCallChunk(toolIndex, id, typ, name, arguments)
	if content != "" {
		chunk.Choices[0].Delta.Content = &content
	}
	return chunk
}

// makeMultiToolCallChunk creates a chunk with multiple tool calls.
func makeMultiToolCallChunk(calls ...struct {
	toolIndex int
	id        string
	typ       string
	name      string
	arguments string
}) llmchat.Chunk {
	toolCalls := make([]llmchat.ToolCallDelta, len(calls))
	for i, call := range calls {
		toolCalls[i] = llmchat.ToolCallDelta{
			Index: call.toolIndex,
		}
		if call.id != "" {
			toolCalls[i].ID = call.id
		}
		if call.typ != "" {
			toolCalls[i].Type = call.typ
		}
		if call.name != "" || call.arguments != "" {
			toolCalls[i].Function = &llmchat.FunctionCallDelta{
				Name:      call.name,
				Arguments: call.arguments,
			}
		}
	}

	return llmchat.Chunk{
		ID:      "chatcmpl-123",
		Object:  "chat.completion.chunk",
		Created: 1234567890,
		Model:   "gpt-4",
		Choices: []llmchat.ChunkChoice{
			{
				Index: 0,
				Delta: llmchat.Delta{
					ToolCalls: toolCalls,
				},
			},
		},
	}
}

// TestProcessChunk_ToolCalls_SingleToolCallIncrementalArguments tests a single
// tool call with arguments spread across multiple chunks.
func TestProcessChunk_ToolCalls_SingleToolCallIncrementalArguments(t *testing.T) {
	// Scenario: Tool call with ID and type in first chunk, then incremental arguments
	// Chunk 1: {tool_calls: [{index: 0, id: "call_123", type: "function", function: {name: "get_weather", arguments: "{\"loc"}}]}
	// Chunk 2: {tool_calls: [{index: 0, function: {arguments: "ation\":"}}]}
	// Chunk 3: {tool_calls: [{index: 0, function: {arguments: "\"NYC\"}"}}]}
	// EOS
	// Expected: All chunks pass through with tool_calls preserved and aggregated correctly

	p := New(passthroughDemasker())
	ctx := context.Background()

	frame1 := makeFrame(makeToolCallChunk(0, "call_123", "function", "get_weather", `{"loc`))
	frame2 := makeFrame(makeToolCallChunk(0, "", "", "", `ation":`))
	frame3 := makeFrame(makeToolCallChunk(0, "", "", "", `"NYC"}`))

	// Process first chunk
	out1, err := p.ProcessChunk(ctx, []byte(frame1), false)
	require.NoError(t, err)
	require.NotNil(t, out1)

	frames1, _ := splitFrames(out1)
	require.Len(t, frames1, 1)
	chunk1 := parseDataFrame(t, frames1[0])
	require.Len(t, chunk1.Choices[0].Delta.ToolCalls, 1)
	assert.Equal(t, "call_123", chunk1.Choices[0].Delta.ToolCalls[0].ID)
	assert.Equal(t, "function", chunk1.Choices[0].Delta.ToolCalls[0].Type)
	assert.Equal(t, "get_weather", chunk1.Choices[0].Delta.ToolCalls[0].Function.Name)
	assert.Equal(t, `{"loc`, chunk1.Choices[0].Delta.ToolCalls[0].Function.Arguments)

	// Process second chunk
	out2, err := p.ProcessChunk(ctx, []byte(frame2), false)
	require.NoError(t, err)
	require.NotNil(t, out2)

	frames2, _ := splitFrames(out2)
	require.Len(t, frames2, 1)
	chunk2 := parseDataFrame(t, frames2[0])
	require.Len(t, chunk2.Choices[0].Delta.ToolCalls, 1)
	assert.Equal(t, `ation":`, chunk2.Choices[0].Delta.ToolCalls[0].Function.Arguments)

	// Process third chunk with EOS
	out3, err := p.ProcessChunk(ctx, []byte(frame3), true)
	require.NoError(t, err)
	require.NotNil(t, out3)

	frames3, _ := splitFrames(out3)
	require.Len(t, frames3, 1)
	chunk3 := parseDataFrame(t, frames3[0])
	require.Len(t, chunk3.Choices[0].Delta.ToolCalls, 1)
	assert.Equal(t, `"NYC"}`, chunk3.Choices[0].Delta.ToolCalls[0].Function.Arguments)

	// Verify aggregator has accumulated tool call metadata (not arguments - they're in choicesAccum)
	assert.Equal(t, "call_123", p.aggr.merged.Choices[0].Delta.ToolCalls[0].ID)
	assert.Equal(t, "function", p.aggr.merged.Choices[0].Delta.ToolCalls[0].Type)
	assert.Equal(t, "get_weather", p.aggr.merged.Choices[0].Delta.ToolCalls[0].Function.Name)
}

// Unchecked
// TestProcessChunk_ToolCalls_MultipleParallelToolCalls tests multiple tool calls
// being invoked in parallel.
func TestProcessChunk_ToolCalls_MultipleParallelToolCalls(t *testing.T) {
	// Scenario: Two tool calls in parallel
	// Chunk 1: {tool_calls: [{index: 0, id: "call_1", type: "function", function: {name: "get_weather", arguments: "{\"city\":"}}]}
	// Chunk 2: {tool_calls: [{index: 1, id: "call_2", type: "function", function: {name: "get_time", arguments: "{\"tz\":"}}]}
	// Chunk 3: {tool_calls: [{index: 0, function: {arguments: "\"NYC\"}"}}, {index: 1, function: {arguments: "\"EST\"}"}}]}
	// EOS
	// Expected: Both tool calls preserved with correct indices

	p := New(passthroughDemasker())
	ctx := context.Background()

	frame1 := makeFrame(makeToolCallChunk(0, "call_1", "function", "get_weather", `{"city":`))
	frame2 := makeFrame(makeToolCallChunk(1, "call_2", "function", "get_time", `{"tz":`))
	frame3 := makeFrame(makeMultiToolCallChunk(
		struct {
			toolIndex int
			id        string
			typ       string
			name      string
			arguments string
		}{0, "", "", "", `"NYC"}`},
		struct {
			toolIndex int
			id        string
			typ       string
			name      string
			arguments string
		}{1, "", "", "", `"EST"}`},
	))

	// Process chunks
	out1, err := p.ProcessChunk(ctx, []byte(frame1), false)
	require.NoError(t, err)
	require.NotNil(t, out1)

	out2, err := p.ProcessChunk(ctx, []byte(frame2), false)
	require.NoError(t, err)
	require.NotNil(t, out2)

	out3, err := p.ProcessChunk(ctx, []byte(frame3), true)
	require.NoError(t, err)
	require.NotNil(t, out3)

	// Verify aggregator has both tool calls with metadata (not arguments)
	require.Len(t, p.aggr.merged.Choices[0].Delta.ToolCalls, 2)

	// Tool call 0
	assert.Equal(t, 0, p.aggr.merged.Choices[0].Delta.ToolCalls[0].Index)
	assert.Equal(t, "call_1", p.aggr.merged.Choices[0].Delta.ToolCalls[0].ID)
	assert.Equal(t, "function", p.aggr.merged.Choices[0].Delta.ToolCalls[0].Type)
	assert.Equal(t, "get_weather", p.aggr.merged.Choices[0].Delta.ToolCalls[0].Function.Name)

	// Tool call 1
	assert.Equal(t, 1, p.aggr.merged.Choices[0].Delta.ToolCalls[1].Index)
	assert.Equal(t, "call_2", p.aggr.merged.Choices[0].Delta.ToolCalls[1].ID)
	assert.Equal(t, "function", p.aggr.merged.Choices[0].Delta.ToolCalls[1].Type)
	assert.Equal(t, "get_time", p.aggr.merged.Choices[0].Delta.ToolCalls[1].Function.Name)
}

// Unchecked
// TestProcessChunk_ToolCalls_ToolCallWithContent tests tool call appearing
// alongside content in the same chunk.
func TestProcessChunk_ToolCalls_ToolCallWithContent(t *testing.T) {
	// Scenario: Tool call and content in same chunk (unusual but valid)
	// Chunk 1: {delta: {content: "Let me check...", tool_calls: [{index: 0, id: "call_1", type: "function", function: {name: "search"}}]}}
	// Chunk 2: {tool_calls: [{index: 0, function: {arguments: "{\"q\":\"test\"}"}}]}
	// EOS
	// Expected: Content passes through demasker, tool call preserved

	p := New(bufferingDemasker())
	ctx := context.Background()

	chunk1 := makeToolCallChunkWithContent(0, "call_1", "function", "search", "", "Let me check...")
	chunk2 := makeToolCallChunk(0, "", "", "", `{"q":"test"}`)

	frame1 := makeFrame(chunk1)
	frame2 := makeFrame(chunk2)

	// Process first chunk - content should buffer, tool call has no arguments
	// yet (just ID/type/name) and is forwarded as-is so it can't be dropped.
	out1, err := p.ProcessChunk(ctx, []byte(frame1), false)
	require.NoError(t, err)
	frames1, _ := splitFrames(out1)
	require.Len(t, frames1, 1, "id/name tool call announcement must be forwarded")
	parsedTC := parseDataFrame(t, frames1[0])
	assert.Equal(t, "call_1", parsedTC.Choices[0].Delta.ToolCalls[0].ID)
	assert.Nil(t, parsedTC.Choices[0].Delta.Content, "buffered content must not leak into the announcement frame")

	// Process second chunk - tool arguments should pass through
	out2, err := p.ProcessChunk(ctx, []byte(frame2), false)
	require.NoError(t, err)
	require.NotNil(t, out2)

	// Process EOS - content should flush
	outEOS, err := p.ProcessChunk(ctx, []byte{}, true)
	require.NoError(t, err)
	require.NotNil(t, outEOS)

	framesEOS, _ := splitFrames(outEOS)
	require.Len(t, framesEOS, 1)
	parsedEOS := parseDataFrame(t, framesEOS[0])
	assert.Equal(t, "Let me check...", *parsedEOS.Choices[0].Delta.Content)
	assert.Empty(t, parsedEOS.Choices[0].Delta.ToolCalls)

	// Verify aggregator has tool call metadata (arguments live in the demasker, not merged)
	require.Len(t, p.aggr.merged.Choices[0].Delta.ToolCalls, 1)
}

// Unchecked
// TestProcessChunk_ToolCalls_WithFinishReasonAndUsage tests tool call completing
// with finish_reason and usage metadata.
func TestProcessChunk_ToolCalls_WithFinishReasonAndUsage(t *testing.T) {
	// Scenario: Tool call followed by finish_reason="tool_calls" and usage
	// Chunk 1: {tool_calls: [{index: 0, id: "call_1", type: "function", function: {name: "calc", arguments: "{}"}}]}
	// Chunk 2: {finish_reason: "tool_calls", usage: {...}}
	// EOS
	// Expected: Tool call frame, then metadata frame

	p := New(passthroughDemasker())
	ctx := context.Background()

	chunk1 := makeToolCallChunk(0, "call_1", "function", "calculator", `{}`)
	frame1 := makeFrame(chunk1)

	// Create metadata chunk with finish_reason
	metadataChunk := llmchat.Chunk{
		ID:      "chatcmpl-123",
		Object:  "chat.completion.chunk",
		Created: 1234567890,
		Model:   "gpt-4",
		Choices: []llmchat.ChunkChoice{
			{
				Index:        0,
				Delta:        llmchat.Delta{},
				FinishReason: toPtr("tool_calls"),
			},
		},
		Usage: &llmchat.Usage{
			PromptTokens:     15,
			CompletionTokens: 10,
			TotalTokens:      25,
		},
	}
	frame2 := makeFrame(metadataChunk)

	// Process tool call
	out1, err := p.ProcessChunk(ctx, []byte(frame1), false)
	require.NoError(t, err)
	require.NotNil(t, out1)

	frames1, _ := splitFrames(out1)
	require.Len(t, frames1, 1)
	chunk := parseDataFrame(t, frames1[0])
	require.Len(t, chunk.Choices[0].Delta.ToolCalls, 1)
	assert.Equal(t, "call_1", chunk.Choices[0].Delta.ToolCalls[0].ID)

	// Process metadata - should flush any buffered content then output metadata
	out2, err := p.ProcessChunk(ctx, []byte(frame2), true)
	require.NoError(t, err)
	require.NotNil(t, out2)

	frames2, _ := splitFrames(out2)
	require.Len(t, frames2, 1)
	metadata := parseDataFrame(t, frames2[0])
	assert.Equal(t, "tool_calls", *metadata.Choices[0].FinishReason)
	assert.NotNil(t, metadata.Usage)
	assert.Equal(t, 25, metadata.Usage.TotalTokens)
}

// Unchecked
// TestProcessChunk_ToolCalls_MultipleChoicesDifferentTools tests different
// choices calling different tools.
func TestProcessChunk_ToolCalls_MultipleChoicesDifferentTools(t *testing.T) {
	// Scenario: Two choices, each calling a different tool
	// Chunk 1: {choices: [{index: 0, tool_calls: [{index: 0, id: "call_1", type: "function", function: {name: "tool_a"}}]}]}
	// Chunk 2: {choices: [{index: 1, tool_calls: [{index: 0, id: "call_2", type: "function", function: {name: "tool_b"}}]}]}
	// Chunk 3: {choices: [{index: 0, tool_calls: [{index: 0, function: {arguments: "{\"a\":1}"}}]}]}
	// Chunk 4: {choices: [{index: 1, tool_calls: [{index: 0, function: {arguments: "{\"b\":2}"}}]}]}
	// EOS
	// Expected: Both choices have their respective tool calls preserved

	p := New(passthroughDemasker())
	ctx := context.Background()

	// Create chunks for choice 0
	chunk1 := makeToolCallChunk(0, "call_1", "function", "tool_a", "")
	chunk1.Choices[0].Index = 0

	// Create chunks for choice 1
	chunk2 := makeToolCallChunk(0, "call_2", "function", "tool_b", "")
	chunk2.Choices[0].Index = 1

	// Arguments for choice 0
	chunk3 := makeToolCallChunk(0, "", "", "", `{"a":1}`)
	chunk3.Choices[0].Index = 0

	// Arguments for choice 1
	chunk4 := makeToolCallChunk(0, "", "", "", `{"b":2}`)
	chunk4.Choices[0].Index = 1

	frame1 := makeFrame(chunk1)
	frame2 := makeFrame(chunk2)
	frame3 := makeFrame(chunk3)
	frame4 := makeFrame(chunk4)

	// Process all chunks
	_, err := p.ProcessChunk(ctx, []byte(frame1), false)
	require.NoError(t, err)

	_, err = p.ProcessChunk(ctx, []byte(frame2), false)
	require.NoError(t, err)

	_, err = p.ProcessChunk(ctx, []byte(frame3), false)
	require.NoError(t, err)

	_, err = p.ProcessChunk(ctx, []byte(frame4), true)
	require.NoError(t, err)

	// Verify aggregator has both choices with their tool calls metadata
	require.Len(t, p.aggr.merged.Choices, 2)

	// Choice 0
	assert.Equal(t, 0, p.aggr.merged.Choices[0].Index)
	require.Len(t, p.aggr.merged.Choices[0].Delta.ToolCalls, 1)
	assert.Equal(t, "call_1", p.aggr.merged.Choices[0].Delta.ToolCalls[0].ID)
	assert.Equal(t, "tool_a", p.aggr.merged.Choices[0].Delta.ToolCalls[0].Function.Name)

	// Choice 1
	assert.Equal(t, 1, p.aggr.merged.Choices[1].Index)
	require.Len(t, p.aggr.merged.Choices[1].Delta.ToolCalls, 1)
	assert.Equal(t, "call_2", p.aggr.merged.Choices[1].Delta.ToolCalls[0].ID)
	assert.Equal(t, "tool_b", p.aggr.merged.Choices[1].Delta.ToolCalls[0].Function.Name)
}

// Unchecked
// TestProcessChunk_ToolCalls_WithContentAndReasoning tests tool call with both
// reasoning and content fields.
func TestProcessChunk_ToolCalls_WithContentAndReasoning(t *testing.T) {
	// Scenario: Reasoning, then content, then tool call
	// Chunk 1: {delta: {reasoning: "I need to use a tool"}}
	// Chunk 2: {delta: {content: "Let me help"}}
	// Chunk 3: {tool_calls: [{index: 0, id: "call_1", type: "function", function: {name: "helper", arguments: "{}"}}]}
	// EOS
	// Expected: Reasoning frame, content frame, tool call frame (all preserved)

	p := New(bufferingDemasker())
	ctx := context.Background()

	reasoning := "I need to use a tool"
	content := "Let me help"

	chunk1 := llmchat.Chunk{
		ID:      "chatcmpl-123",
		Object:  "chat.completion.chunk",
		Created: 1234567890,
		Model:   "gpt-4",
		Choices: []llmchat.ChunkChoice{
			{
				Index: 0,
				Delta: llmchat.Delta{
					Reasoning: &reasoning,
				},
			},
		},
	}

	chunk2 := llmchat.Chunk{
		ID:      "chatcmpl-123",
		Object:  "chat.completion.chunk",
		Created: 1234567890,
		Model:   "gpt-4",
		Choices: []llmchat.ChunkChoice{
			{
				Index: 0,
				Delta: llmchat.Delta{
					Content: &content,
				},
			},
		},
	}

	chunk3 := makeToolCallChunk(0, "call_1", "function", "helper", `{}`)

	frame1 := makeFrame(chunk1)
	frame2 := makeFrame(chunk2)
	frame3 := makeFrame(chunk3)

	// Process reasoning - buffers
	out1, err := p.ProcessChunk(ctx, []byte(frame1), false)
	require.NoError(t, err)
	require.Nil(t, out1, "Reasoning should buffer")

	// Process content - should flush reasoning, buffer content
	out2, err := p.ProcessChunk(ctx, []byte(frame2), false)
	require.NoError(t, err)
	require.NotNil(t, out2, "Should flush reasoning when content starts")

	frames2, _ := splitFrames(out2)
	require.Len(t, frames2, 1)
	parsedReasoning := parseDataFrame(t, frames2[0])
	assert.Equal(t, "I need to use a tool", *parsedReasoning.Choices[0].Delta.Reasoning)
	assert.Nil(t, parsedReasoning.Choices[0].Delta.Content)

	// Process tool call - should pass through
	out3, err := p.ProcessChunk(ctx, []byte(frame3), false)
	require.NoError(t, err)
	require.NotNil(t, out3)

	frames3, _ := splitFrames(out3)
	require.Len(t, frames3, 1)
	parsedTool := parseDataFrame(t, frames3[0])
	require.Len(t, parsedTool.Choices[0].Delta.ToolCalls, 1)
	assert.Equal(t, "call_1", parsedTool.Choices[0].Delta.ToolCalls[0].ID)

	// Process EOS - should flush content
	outEOS, err := p.ProcessChunk(ctx, []byte{}, true)
	require.NoError(t, err)
	require.NotNil(t, outEOS)

	framesEOS, _ := splitFrames(outEOS)
	require.Len(t, framesEOS, 1)
	parsedContent := parseDataFrame(t, framesEOS[0])
	assert.Equal(t, "Let me help", *parsedContent.Choices[0].Delta.Content)
	assert.Nil(t, parsedContent.Choices[0].Delta.Reasoning)

	// Verify aggregator has tool call metadata.
	require.Len(t, p.aggr.merged.Choices[0].Delta.ToolCalls, 1)
	assert.Equal(t, "helper", p.aggr.merged.Choices[0].Delta.ToolCalls[0].Function.Name)
}

// Unchecked
// TestProcessChunk_ToolCalls_EmptyFunction tests tool call with nil function.
func TestProcessChunk_ToolCalls_EmptyFunction(t *testing.T) {
	// Scenario: Tool call delta with no function field (edge case)
	// Chunk 1: {tool_calls: [{index: 0, id: "call_1", type: "function"}]}
	// Chunk 2: {tool_calls: [{index: 0, function: {name: "test", arguments: "{}"}}]}
	// EOS
	// Expected: Tool call aggregated correctly despite initial nil function

	p := New(passthroughDemasker())
	ctx := context.Background()

	// First chunk with only id and type
	chunk1 := llmchat.Chunk{
		ID:      "chatcmpl-123",
		Object:  "chat.completion.chunk",
		Created: 1234567890,
		Model:   "gpt-4",
		Choices: []llmchat.ChunkChoice{
			{
				Index: 0,
				Delta: llmchat.Delta{
					ToolCalls: []llmchat.ToolCallDelta{
						{
							Index: 0,
							ID:    "call_1",
							Type:  "function",
							// Function is nil
						},
					},
				},
			},
		},
	}

	chunk2 := makeToolCallChunk(0, "", "", "test", `{}`)

	frame1 := makeFrame(chunk1)
	frame2 := makeFrame(chunk2)

	// Process first chunk - no function yet, so no arguments to demask; the
	// bare id/type announcement is forwarded as-is.
	out1, err := p.ProcessChunk(ctx, []byte(frame1), false)
	require.NoError(t, err)
	frames1, _ := splitFrames(out1)
	require.Len(t, frames1, 1, "bare id/type tool call announcement must be forwarded")
	parsedTC := parseDataFrame(t, frames1[0])
	assert.Equal(t, "call_1", parsedTC.Choices[0].Delta.ToolCalls[0].ID)

	// Process second chunk with EOS
	out2, err := p.ProcessChunk(ctx, []byte(frame2), true)
	require.NoError(t, err)
	require.NotNil(t, out2)

	// Verify aggregator merged correctly (metadata only, not arguments)
	require.Len(t, p.aggr.merged.Choices[0].Delta.ToolCalls, 1)
	assert.Equal(t, "call_1", p.aggr.merged.Choices[0].Delta.ToolCalls[0].ID)
	assert.Equal(t, "function", p.aggr.merged.Choices[0].Delta.ToolCalls[0].Type)
	assert.NotNil(t, p.aggr.merged.Choices[0].Delta.ToolCalls[0].Function)
	assert.Equal(t, "test", p.aggr.merged.Choices[0].Delta.ToolCalls[0].Function.Name)
}

// Unchecked
// TestProcessChunk_ToolCalls_NotLostOnPassthrough tests that tool calls
// are never lost even with passthrough demasker.
func TestProcessChunk_ToolCalls_NotLostOnPassthrough(t *testing.T) {
	// Scenario: Simple passthrough with tool calls - ensure nothing is lost
	// Chunk 1: {tool_calls: [{index: 0, id: "call_1", type: "function", function: {name: "func1", arguments: "arg1"}}]}
	// Chunk 2: {tool_calls: [{index: 0, function: {arguments: "arg2"}}]}
	// Chunk 3: {tool_calls: [{index: 0, function: {arguments: "arg3"}}]}
	// EOS
	// Expected: All 3 chunks output, aggregator has concatenated arguments

	p := New(passthroughDemasker())
	ctx := context.Background()

	frame1 := makeFrame(makeToolCallChunk(0, "call_1", "function", "func1", "arg1"))
	frame2 := makeFrame(makeToolCallChunk(0, "", "", "", "arg2"))
	frame3 := makeFrame(makeToolCallChunk(0, "", "", "", "arg3"))

	var allOutput []byte

	// Process all chunks and collect output
	out1, err := p.ProcessChunk(ctx, []byte(frame1), false)
	require.NoError(t, err)
	require.NotNil(t, out1, "Chunk 1 should not be lost")
	allOutput = append(allOutput, out1...)

	out2, err := p.ProcessChunk(ctx, []byte(frame2), false)
	require.NoError(t, err)
	require.NotNil(t, out2, "Chunk 2 should not be lost")
	allOutput = append(allOutput, out2...)

	out3, err := p.ProcessChunk(ctx, []byte(frame3), true)
	require.NoError(t, err)
	require.NotNil(t, out3, "Chunk 3 should not be lost")
	allOutput = append(allOutput, out3...)

	// Verify all 3 frames were output
	frames, _ := splitFrames(allOutput)
	require.Len(t, frames, 3, "All 3 tool call chunks should be in output")

	// Verify each output frame has tool calls
	for i, frame := range frames {
		chunk := parseDataFrame(t, frame)
		require.Len(t, chunk.Choices[0].Delta.ToolCalls, 1, "Frame %d should have tool_calls", i)
	}

	// Verify aggregator has metadata (arguments are not concatenated in merged state)
	require.Len(t, p.aggr.merged.Choices[0].Delta.ToolCalls, 1)
	assert.Equal(t, "call_1", p.aggr.merged.Choices[0].Delta.ToolCalls[0].ID)
	assert.Equal(t, "func1", p.aggr.merged.Choices[0].Delta.ToolCalls[0].Function.Name)
}

// Unchecked
// TestProcessChunk_ToolCalls_WithDone tests tool calls finishing with [DONE] marker.
func TestProcessChunk_ToolCalls_WithDone(t *testing.T) {
	// Scenario: Tool call followed by [DONE]
	// Chunk 1: {tool_calls: [{index: 0, id: "call_1", type: "function", function: {name: "test", arguments: "{}"}}]}
	// Chunk 2: [DONE]
	// Expected: Tool call frame, then [DONE] frame

	p := New(passthroughDemasker())
	ctx := context.Background()

	frame1 := makeFrame(makeToolCallChunk(0, "call_1", "function", "test", `{}`))
	frame2 := makeDoneFrame()

	// Process tool call
	out1, err := p.ProcessChunk(ctx, []byte(frame1), false)
	require.NoError(t, err)
	require.NotNil(t, out1)

	// Process [DONE]
	out2, err := p.ProcessChunk(ctx, []byte(frame2), true)
	require.NoError(t, err)
	require.NotNil(t, out2)

	frames2, _ := splitFrames(out2)
	require.Len(t, frames2, 1)
	assert.Equal(t, "data: [DONE]", strings.TrimSpace(string(frames2[0])))

	// Verify tool call was preserved in aggregator
	require.Len(t, p.aggr.merged.Choices[0].Delta.ToolCalls, 1)
	assert.Equal(t, "call_1", p.aggr.merged.Choices[0].Delta.ToolCalls[0].ID)
}

// ============================================================================
// TOOL CALLS DEMASKING TESTS
// ============================================================================

// bufferingDemaskingDemasker creates a demasker that replaces [MASKED_X] tokens with real values on flush.
func bufferingDemaskingDemasker() common.DemaskerFactoryFn {
	return func() common.Demasker {
		buf := ""
		return newDemaskerMock(
			func(ctx context.Context, chunk string, flush bool) (string, error) {
				buf += chunk
				if flush {
					result := strings.ReplaceAll(buf, "[MASKED_CITY]", "San Francisco")
					result = strings.ReplaceAll(result, "[MASKED_NAME]", "Alice")
					result = strings.ReplaceAll(result, "[MASKED_TOKEN]", "secret123")
					result = strings.ReplaceAll(result, "[MASKED_QUERY]", "test query")
					result = strings.ReplaceAll(result, "[MASKED]", "test value")
					buf = ""
					return result, nil
				}
				return "", nil
			})
	}
}

// passthroughDemaskingDemasker creates a demasker that demasks and returns immediately without buffering.
func passthroughDemaskingDemasker() common.DemaskerFactoryFn {
	return func() common.Demasker {
		return newDemaskerMock(
			func(ctx context.Context, chunk string, flush bool) (string, error) {
				if chunk == "" {
					return "", nil
				}
				// Demask and return immediately
				result := strings.ReplaceAll(chunk, "[MASKED_CITY]", "San Francisco")
				result = strings.ReplaceAll(result, "[MASKED_NAME]", "Alice")
				result = strings.ReplaceAll(result, "[MASKED]", "test value")
				return result, nil
			})
	}
}

// TestProcessChunk_ToolCallDemasking_SingleToolWithMask tests demasking of a single
// tool call with masked data in arguments across multiple chunks, flushing when JSON closes.
func TestProcessChunk_ToolCallDemasking_SingleToolWithMask(t *testing.T) {
	// Scenario: Tool call arguments split across 3 chunks with masked city name
	// Chunk 1: {tool_calls: [{index: 0, id: "call_1", type: "function", function: {name: "get_weather", arguments: "{\"city\":"}}]}
	// Chunk 2: {tool_calls: [{index: 0, function: {arguments: "\"[MASKED_CITY]\""}}]}
	// Chunk 3: {tool_calls: [{index: 0, function: {arguments: "}"}}]} → JSON closes, should flush and demask
	// EOS
	// Expected: Demasker buffers chunks 1-2, flushes on chunk 3 (JSON closed), outputs demasked arguments

	p := New(bufferingDemaskingDemasker())
	ctx := context.Background()

	frame1 := makeFrame(makeToolCallChunk(0, "call_1", "function", "get_weather", `{"city":`))
	frame2 := makeFrame(makeToolCallChunk(0, "", "", "", `"[MASKED_CITY]"`))
	frame3 := makeFrame(makeToolCallChunk(0, "", "", "", `}`))

	// Process first chunk - demasker buffers, no output
	out1, err := p.ProcessChunk(ctx, []byte(frame1), false)
	require.NoError(t, err)
	// Demasker buffers, so no tool call output yet
	if out1 != nil {
		frames1, _ := splitFrames(out1)
		require.Len(t, frames1, 0, "Expected no output (demasker buffering)")
	}

	// Process second chunk - demasker still buffering
	out2, err := p.ProcessChunk(ctx, []byte(frame2), false)
	require.NoError(t, err)
	if out2 != nil {
		frames2, _ := splitFrames(out2)
		require.Len(t, frames2, 0, "Expected no output (demasker still buffering)")
	}

	// Process third chunk - JSON closes (depth=0), should flush demasker
	out3, err := p.ProcessChunk(ctx, []byte(frame3), false)
	require.NoError(t, err)
	require.NotNil(t, out3, "Expected output when JSON closes")

	frames3, _ := splitFrames(out3)
	require.Len(t, frames3, 1, "Expected 1 tool call frame with demasked arguments")
	chunk3 := parseDataFrame(t, frames3[0])
	require.Len(t, chunk3.Choices[0].Delta.ToolCalls, 1)
	// Should have demasked arguments
	assert.Equal(t, `{"city":"San Francisco"}`, chunk3.Choices[0].Delta.ToolCalls[0].Function.Arguments,
		"Arguments should be fully demasked when JSON closed")

	// Process EOS - no additional output needed
	outEOS, err := p.ProcessChunk(ctx, []byte{}, true)
	require.NoError(t, err)
	// All tool calls already flushed, so no output on EOS
	if outEOS != nil {
		framesEOS, _ := splitFrames(outEOS)
		require.Len(t, framesEOS, 0, "No output expected on EOS (already flushed)")
	}

	// The tool-call accumulator still tracks JSON-close state for the choice.
	acc := p.aggr.choicesAccum[0]
	require.NotNil(t, acc)
	_, ok := acc.toolArguments[0]
	require.True(t, ok, "Tool call 0 should be tracked in the accumulator")
}

// TestProcessChunk_ToolCallDemasking_JSONClosedFlush tests that flush=true is passed
// to the demasker when JSON arguments are closed.
func TestProcessChunk_ToolCallDemasking_JSONClosedFlush(t *testing.T) {
	// Scenario: Verify flush flag is passed correctly when JSON closes
	// Chunk 1: {tool_calls: [{index: 0, id: "call_1", function: {arguments: "{\"key\":"}}]}
	// Chunk 2: {tool_calls: [{index: 0, function: {arguments: "\"value\"}"}}]} → JSON closes
	// Expected: DemaskChunk called with flush=true on chunk 2

	var flushCalls []bool
	customDemasker := func() common.Demasker {
		buf := ""
		return newDemaskerMock(
			func(ctx context.Context, chunk string, flush bool) (string, error) {
				flushCalls = append(flushCalls, flush)
				buf += chunk
				if flush {
					result := buf
					buf = ""
					return result, nil
				}
				return "", nil
			})
	}

	p := New(customDemasker)
	ctx := context.Background()

	frame1 := makeFrame(makeToolCallChunk(0, "call_1", "function", "test", `{"key":`))
	frame2 := makeFrame(makeToolCallChunk(0, "", "", "", `"value"}`))

	// Process first chunk
	_, err := p.ProcessChunk(ctx, []byte(frame1), false)
	require.NoError(t, err)

	// Process second chunk - JSON closes
	_, err = p.ProcessChunk(ctx, []byte(frame2), false)
	require.NoError(t, err)

	// Verify flush calls
	require.Len(t, flushCalls, 2, "Expected 2 DemaskChunk calls")
	assert.False(t, flushCalls[0], "First call should have flush=false")
	assert.True(t, flushCalls[1], "Second call should have flush=true (JSON closed)")
}

// TestProcessChunk_ToolCallDemasking_MultipleToolCallsParallel tests multiple parallel
// tool calls, each flushing independently when its JSON closes.
func TestProcessChunk_ToolCallDemasking_MultipleToolCallsParallel(t *testing.T) {
	// Scenario: 3 parallel tool calls, each completes at different times
	// Chunk 1: {tool_calls: [{index: 0, id: "call_0", function: {name: "tool_a", arguments: "{\"a\":"}}]}
	// Chunk 2: {tool_calls: [{index: 1, id: "call_1", function: {name: "tool_b", arguments: "{\"b\":"}}]}
	// Chunk 3: {tool_calls: [{index: 2, id: "call_2", function: {name: "tool_c", arguments: "{\"c\":"}}]}
	// Chunk 4: {tool_calls: [{index: 0, function: {arguments: "1}"}}]} → tool 0 JSON closes, flushes
	// Chunk 5: {tool_calls: [{index: 1, function: {arguments: "2}"}}]} → tool 1 JSON closes, flushes
	// Chunk 6: {tool_calls: [{index: 2, function: {arguments: "3}"}}]} → tool 2 JSON closes, flushes
	// Expected: Each tool call flushes independently when its JSON closes

	p := New(bufferingDemaskingDemasker())
	ctx := context.Background()

	frame1 := makeFrame(makeToolCallChunk(0, "call_0", "function", "tool_a", `{"a":`))
	frame2 := makeFrame(makeToolCallChunk(1, "call_1", "function", "tool_b", `{"b":`))
	frame3 := makeFrame(makeToolCallChunk(2, "call_2", "function", "tool_c", `{"c":`))
	frame4 := makeFrame(makeToolCallChunk(0, "", "", "", `1}`)) // Tool 0 closes
	frame5 := makeFrame(makeToolCallChunk(1, "", "", "", `2}`)) // Tool 1 closes
	frame6 := makeFrame(makeToolCallChunk(2, "", "", "", `3}`)) // Tool 2 closes

	// Process chunks 1-3: all buffer
	out1, err := p.ProcessChunk(ctx, []byte(frame1), false)
	require.NoError(t, err)
	if out1 != nil {
		frames, _ := splitFrames(out1)
		require.Len(t, frames, 0)
	}

	out2, err := p.ProcessChunk(ctx, []byte(frame2), false)
	require.NoError(t, err)
	if out2 != nil {
		frames, _ := splitFrames(out2)
		require.Len(t, frames, 0)
	}

	out3, err := p.ProcessChunk(ctx, []byte(frame3), false)
	require.NoError(t, err)
	if out3 != nil {
		frames, _ := splitFrames(out3)
		require.Len(t, frames, 0)
	}

	// Process chunk 4: tool 0 JSON closes, should flush
	out4, err := p.ProcessChunk(ctx, []byte(frame4), false)
	require.NoError(t, err)
	require.NotNil(t, out4, "Tool 0 should flush when JSON closes")
	frames4, _ := splitFrames(out4)
	require.Len(t, frames4, 1)
	chunk4 := parseDataFrame(t, frames4[0])
	require.Len(t, chunk4.Choices[0].Delta.ToolCalls, 1)
	assert.Equal(t, 0, chunk4.Choices[0].Delta.ToolCalls[0].Index, "Should be tool 0")
	assert.Equal(t, `{"a":1}`, chunk4.Choices[0].Delta.ToolCalls[0].Function.Arguments)

	// Process chunk 5: tool 1 JSON closes, should flush
	out5, err := p.ProcessChunk(ctx, []byte(frame5), false)
	require.NoError(t, err)
	require.NotNil(t, out5, "Tool 1 should flush when JSON closes")
	frames5, _ := splitFrames(out5)
	require.Len(t, frames5, 1)
	chunk5 := parseDataFrame(t, frames5[0])
	require.Len(t, chunk5.Choices[0].Delta.ToolCalls, 1)
	assert.Equal(t, 1, chunk5.Choices[0].Delta.ToolCalls[0].Index, "Should be tool 1")
	assert.Equal(t, `{"b":2}`, chunk5.Choices[0].Delta.ToolCalls[0].Function.Arguments)

	// Process chunk 6: tool 2 JSON closes, should flush
	out6, err := p.ProcessChunk(ctx, []byte(frame6), false)
	require.NoError(t, err)
	require.NotNil(t, out6, "Tool 2 should flush when JSON closes")
	frames6, _ := splitFrames(out6)
	require.Len(t, frames6, 1)
	chunk6 := parseDataFrame(t, frames6[0])
	require.Len(t, chunk6.Choices[0].Delta.ToolCalls, 1)
	assert.Equal(t, 2, chunk6.Choices[0].Delta.ToolCalls[0].Index, "Should be tool 2")
	assert.Equal(t, `{"c":3}`, chunk6.Choices[0].Delta.ToolCalls[0].Function.Arguments)

	// All tool calls should be tracked in the accumulator.
	acc := p.aggr.choicesAccum[0]
	require.NotNil(t, acc)
	for i := 0; i < 3; i++ {
		_, ok := acc.toolArguments[i]
		require.True(t, ok, "Tool %d should be tracked", i)
	}
}

// TestProcessChunk_ToolCallDemasking_BufferingDemasker tests tool call demasking
// with a demasker that buffers all chunks and only outputs on flush.
func TestProcessChunk_ToolCallDemasking_BufferingDemasker(t *testing.T) {
	// Scenario: Demasker buffers everything, outputs only when flush=true
	// Chunk 1: {tool_calls: [{index: 0, id: "call_1", function: {name: "func", arguments: "{\"token\":"}}]}
	// Chunk 2: {tool_calls: [{index: 0, function: {arguments: "\"[MASKED_TOKEN]\""}}]}
	// Chunk 3: {tool_calls: [{index: 0, function: {arguments: "}"}}]} → JSON closes, flush
	// Expected: No output until chunk 3, then all demasked arguments output at once

	p := New(bufferingDemaskingDemasker())
	ctx := context.Background()

	frame1 := makeFrame(makeToolCallChunk(0, "call_1", "function", "auth", `{"token":`))
	frame2 := makeFrame(makeToolCallChunk(0, "", "", "", `"[MASKED_TOKEN]"`))
	frame3 := makeFrame(makeToolCallChunk(0, "", "", "", `}`))

	// Chunks 1-2: demasker buffers, no output
	out1, err := p.ProcessChunk(ctx, []byte(frame1), false)
	require.NoError(t, err)
	if out1 != nil {
		frames, _ := splitFrames(out1)
		require.Len(t, frames, 0)
	}

	out2, err := p.ProcessChunk(ctx, []byte(frame2), false)
	require.NoError(t, err)
	if out2 != nil {
		frames, _ := splitFrames(out2)
		require.Len(t, frames, 0)
	}

	// Chunk 3: JSON closes, demasker flushes everything
	out3, err := p.ProcessChunk(ctx, []byte(frame3), false)
	require.NoError(t, err)
	require.NotNil(t, out3)

	frames3, _ := splitFrames(out3)
	require.Len(t, frames3, 1)
	chunk3 := parseDataFrame(t, frames3[0])
	require.Len(t, chunk3.Choices[0].Delta.ToolCalls, 1)
	// Should have fully demasked arguments
	assert.Equal(t, `{"token":"secret123"}`, chunk3.Choices[0].Delta.ToolCalls[0].Function.Arguments)
}

// TestProcessChunk_ToolCallDemasking_FlushOnFinishReason tests that incomplete
// JSON arguments are flushed when finish_reason arrives.
func TestProcessChunk_ToolCallDemasking_FlushOnFinishReason(t *testing.T) {
	// Scenario: Tool call with incomplete JSON, finish_reason="tool_calls" arrives
	// Chunk 1: {tool_calls: [{index: 0, id: "call_1", function: {name: "func", arguments: "{\"incomplete\":"}}]}
	// Chunk 2: {finish_reason: "tool_calls", usage: {...}}
	// Expected: Tool call flushed on finish_reason even though JSON not closed

	p := New(bufferingDemaskingDemasker())
	ctx := context.Background()

	frame1 := makeFrame(makeToolCallChunk(0, "call_1", "function", "test", `{"incomplete":`))

	// Metadata chunk with finish_reason
	metadataChunk := llmchat.Chunk{
		ID:      "chatcmpl-123",
		Object:  "chat.completion.chunk",
		Created: 1234567890,
		Model:   "gpt-4",
		Choices: []llmchat.ChunkChoice{
			{
				Index:        0,
				Delta:        llmchat.Delta{},
				FinishReason: toPtr("tool_calls"),
			},
		},
		Usage: &llmchat.Usage{
			PromptTokens:     10,
			CompletionTokens: 20,
			TotalTokens:      30,
		},
	}
	frame2 := makeFrame(metadataChunk)

	// Process first chunk - demasker buffers
	out1, err := p.ProcessChunk(ctx, []byte(frame1), false)
	require.NoError(t, err)
	if out1 != nil {
		frames, _ := splitFrames(out1)
		require.Len(t, frames, 0, "Demasker should buffer")
	}

	// Process metadata chunk - should flush tool call, then output metadata
	out2, err := p.ProcessChunk(ctx, []byte(frame2), false)
	require.NoError(t, err)
	require.NotNil(t, out2)

	frames2, _ := splitFrames(out2)
	require.Len(t, frames2, 2, "Expected tool call frame + metadata frame")

	// First frame: flushed tool call
	chunk1 := parseDataFrame(t, frames2[0])
	require.Len(t, chunk1.Choices[0].Delta.ToolCalls, 1)
	assert.Equal(t, `{"incomplete":`, chunk1.Choices[0].Delta.ToolCalls[0].Function.Arguments,
		"Incomplete JSON should be flushed as-is")

	// Second frame: metadata
	chunk2 := parseDataFrame(t, frames2[1])
	assert.Equal(t, "tool_calls", *chunk2.Choices[0].FinishReason)
	assert.NotNil(t, chunk2.Usage)
}

// TestProcessChunk_ToolCallDemasking_DemaskerError tests fallback to accumulated
// arguments when demasker fails.
func TestProcessChunk_ToolCallDemasking_DemaskerError(t *testing.T) {
	// Scenario: Demasker fails, should fallback to choicesAccum (masked arguments)
	// Chunk 1: {tool_calls: [{index: 0, id: "call_1", function: {name: "func", arguments: "{\"city\":"}}]}
	// Chunk 2: {tool_calls: [{index: 0, function: {arguments: "\"[MASKED_CITY]\""}}]}
	// Chunk 3: {tool_calls: [{index: 0, function: {arguments: "}"}}]} → demasker errors
	// Expected: Fallback to masked arguments from accumulator

	errorDemasker := func() common.Demasker {
		buf := ""
		return newDemaskerMock(
			func(ctx context.Context, chunk string, flush bool) (string, error) {
				buf += chunk
				if flush {
					// Per the Demasker contract, hand back the un-emitted
					// buffered content on error.
					lost := buf
					buf = ""
					return lost, fmt.Errorf("demasker error")
				}
				return "", nil
			})
	}

	p := New(errorDemasker)
	ctx := context.Background()

	frame1 := makeFrame(makeToolCallChunk(0, "call_1", "function", "get_weather", `{"city":`))
	frame2 := makeFrame(makeToolCallChunk(0, "", "", "", `"[MASKED_CITY]"`))
	frame3 := makeFrame(makeToolCallChunk(0, "", "", "", `}`))

	// Process chunks
	_, _ = p.ProcessChunk(ctx, []byte(frame1), false)
	_, _ = p.ProcessChunk(ctx, []byte(frame2), false)

	// Chunk 3: JSON closes, demasker errors, should fallback
	out3, err := p.ProcessChunk(ctx, []byte(frame3), false)
	require.NoError(t, err)
	require.NotNil(t, out3, "Should output fallback even on error")

	frames3, _ := splitFrames(out3)
	require.Len(t, frames3, 1)
	chunk3 := parseDataFrame(t, frames3[0])
	require.Len(t, chunk3.Choices[0].Delta.ToolCalls, 1)
	// Should have masked arguments (fallback from the demasker's un-emitted content)
	assert.Equal(t, `{"city":"[MASKED_CITY]"}`, chunk3.Choices[0].Delta.ToolCalls[0].Function.Arguments,
		"Should fall back to the demasker's un-emitted masked arguments")
}

// TestProcessChunk_ToolCallDemasking_NestedJSON tests JSON depth tracking with
// nested objects.
func TestProcessChunk_ToolCallDemasking_NestedJSON(t *testing.T) {
	// Scenario: Nested JSON object in arguments
	// Chunk 1: {tool_calls: [{index: 0, id: "call_1", function: {name: "func", arguments: "{\"outer\":{"}}]}
	// Chunk 2: {tool_calls: [{index: 0, function: {arguments: "\"inner\":\"[MASKED_NAME]\""}}]}
	// Chunk 3: {tool_calls: [{index: 0, function: {arguments: "}}"}}]} → JSON closes (depth 2→1→0)
	// Expected: Flush only when outermost JSON closes

	p := New(bufferingDemaskingDemasker())
	ctx := context.Background()

	frame1 := makeFrame(makeToolCallChunk(0, "call_1", "function", "complex", `{"outer":{`))
	frame2 := makeFrame(makeToolCallChunk(0, "", "", "", `"inner":"[MASKED_NAME]"`))
	frame3 := makeFrame(makeToolCallChunk(0, "", "", "", `}}`))

	// Chunks 1-2: depth > 0, no flush
	out1, _ := p.ProcessChunk(ctx, []byte(frame1), false)
	if out1 != nil {
		frames, _ := splitFrames(out1)
		require.Len(t, frames, 0)
	}

	out2, _ := p.ProcessChunk(ctx, []byte(frame2), false)
	if out2 != nil {
		frames, _ := splitFrames(out2)
		require.Len(t, frames, 0)
	}

	// Chunk 3: depth reaches 0, flush
	out3, err := p.ProcessChunk(ctx, []byte(frame3), false)
	require.NoError(t, err)
	require.NotNil(t, out3)

	frames3, _ := splitFrames(out3)
	require.Len(t, frames3, 1)
	chunk3 := parseDataFrame(t, frames3[0])
	assert.Equal(t, `{"outer":{"inner":"Alice"}}`, chunk3.Choices[0].Delta.ToolCalls[0].Function.Arguments,
		"Nested JSON should be demasked correctly")
}

// TestProcessChunk_ToolCallDemasking_PartialDemaskerOutput tests demasker returning
// partial results before flush.
func TestProcessChunk_ToolCallDemasking_PartialDemaskerOutput(t *testing.T) {
	// Scenario: Demasker returns partial output on chunk 2, final output on flush
	// Chunk 1: {tool_calls: [{index: 0, id: "call_1", function: {arguments: "{\"a\":"}}]}
	// Chunk 2: {tool_calls: [{index: 0, function: {arguments: "1,"}}]} → demasker returns partial
	// Chunk 3: {tool_calls: [{index: 0, function: {arguments: "\"b\":2}"}}]} → JSON closes, flush
	// Expected: Partial output on chunk 2, final output on chunk 3

	partialDemasker := func() common.Demasker {
		buf := ""
		count := 0
		return newDemaskerMock(
			func(ctx context.Context, chunk string, flush bool) (string, error) {
				count++
				buf += chunk
				// Return partial result on 2nd call
				if count == 2 && !flush {
					partial := buf
					buf = ""
					return partial, nil
				}
				if flush {
					result := buf
					buf = ""
					return result, nil
				}
				return "", nil
			})
	}

	p := New(partialDemasker)
	ctx := context.Background()

	frame1 := makeFrame(makeToolCallChunk(0, "call_1", "function", "test", `{"a":`))
	frame2 := makeFrame(makeToolCallChunk(0, "", "", "", `1,`))
	frame3 := makeFrame(makeToolCallChunk(0, "", "", "", `"b":2}`))

	// Chunk 1: buffering
	out1, _ := p.ProcessChunk(ctx, []byte(frame1), false)
	if out1 != nil {
		frames, _ := splitFrames(out1)
		require.Len(t, frames, 0)
	}

	// Chunk 2: demasker returns partial output
	out2, err := p.ProcessChunk(ctx, []byte(frame2), false)
	require.NoError(t, err)
	require.NotNil(t, out2, "Demasker returned partial output")
	frames2, _ := splitFrames(out2)
	require.Len(t, frames2, 1)
	chunk2 := parseDataFrame(t, frames2[0])
	assert.Equal(t, `{"a":1,`, chunk2.Choices[0].Delta.ToolCalls[0].Function.Arguments)

	// Chunk 3: JSON closes, flush remaining
	out3, err := p.ProcessChunk(ctx, []byte(frame3), false)
	require.NoError(t, err)
	require.NotNil(t, out3)
	frames3, _ := splitFrames(out3)
	require.Len(t, frames3, 1)
	chunk3 := parseDataFrame(t, frames3[0])
	assert.Equal(t, `"b":2}`, chunk3.Choices[0].Delta.ToolCalls[0].Function.Arguments)
}

// TestProcessChunk_ToolCallDemasking_WithContentAndReasoning tests tool calls
// mixed with content and reasoning in the same stream.
func TestProcessChunk_ToolCallDemasking_WithContentAndReasoning(t *testing.T) {
	// Scenario: Content, reasoning, and tool call in same stream
	// Chunk 1: {delta: {reasoning: "Let me search..."}}
	// Chunk 2: {delta: {content: "I'll look that up."}}
	// Chunk 3: {tool_calls: [{index: 0, id: "call_1", function: {name: "search", arguments: "{\"q\":"}}]}
	// Chunk 4: {tool_calls: [{index: 0, function: {arguments: "\"[MASKED_NAME]\"}"}}]} → JSON closes
	// Expected: Reasoning flushes when content starts, tool call flushes when JSON closes

	p := New(bufferingDemaskingDemasker())
	ctx := context.Background()

	chunk1 := llmchat.Chunk{
		ID:      "chatcmpl-123",
		Object:  "chat.completion.chunk",
		Created: 1234567890,
		Model:   "gpt-4",
		Choices: []llmchat.ChunkChoice{
			{
				Index: 0,
				Delta: llmchat.Delta{
					Reasoning: toPtr("Let me search..."),
				},
			},
		},
	}

	chunk2 := llmchat.Chunk{
		ID:      "chatcmpl-123",
		Object:  "chat.completion.chunk",
		Created: 1234567890,
		Model:   "gpt-4",
		Choices: []llmchat.ChunkChoice{
			{
				Index: 0,
				Delta: llmchat.Delta{
					Content: toPtr("I'll look that up."),
				},
			},
		},
	}

	frame1 := makeFrame(chunk1)
	frame2 := makeFrame(chunk2)
	frame3 := makeFrame(makeToolCallChunk(0, "call_1", "function", "search", `{"q":`))
	frame4 := makeFrame(makeToolCallChunk(0, "", "", "", `"[MASKED_NAME]"}`))

	var allOutput []byte

	// Process all chunks
	out1, _ := p.ProcessChunk(ctx, []byte(frame1), false)
	allOutput = append(allOutput, out1...)

	out2, _ := p.ProcessChunk(ctx, []byte(frame2), false)
	allOutput = append(allOutput, out2...)

	out3, _ := p.ProcessChunk(ctx, []byte(frame3), false)
	allOutput = append(allOutput, out3...)

	out4, _ := p.ProcessChunk(ctx, []byte(frame4), false)
	allOutput = append(allOutput, out4...)

	// Process EOS to flush remaining content
	outEOS, _ := p.ProcessChunk(ctx, []byte{}, true)
	allOutput = append(allOutput, outEOS...)

	// Parse all output frames
	frames, _ := splitFrames(allOutput)
	require.GreaterOrEqual(t, len(frames), 3, "Expected at least reasoning, content, and tool call frames")

	// Verify we have reasoning, content, and tool call frames
	hasReasoning := false
	hasContent := false
	hasToolCall := false

	for _, frame := range frames {
		chunk := parseDataFrame(t, frame)
		if len(chunk.Choices) > 0 {
			if chunk.Choices[0].Delta.Reasoning != nil && *chunk.Choices[0].Delta.Reasoning != "" {
				hasReasoning = true
			}
			if chunk.Choices[0].Delta.Content != nil && *chunk.Choices[0].Delta.Content != "" {
				hasContent = true
			}
			if len(chunk.Choices[0].Delta.ToolCalls) > 0 {
				hasToolCall = true
				// Tool call should be demasked
				assert.Equal(t, `{"q":"Alice"}`, chunk.Choices[0].Delta.ToolCalls[0].Function.Arguments)
			}
		}
	}

	assert.True(t, hasReasoning, "Should have reasoning frame")
	assert.True(t, hasContent, "Should have content frame")
	assert.True(t, hasToolCall, "Should have tool call frame with demasked arguments")
}

// TestProcessChunk_ToolCallDemasking_MultipleChoices tests tool calls across
// multiple choices with independent demasking.
func TestProcessChunk_ToolCallDemasking_MultipleChoices(t *testing.T) {
	// Scenario: Two choices, each with different tool call
	// Choice 0: {tool_calls: [{index: 0, function: {arguments: "{\"city\":\"[MASKED_CITY]\"}"}}]}
	// Choice 1: {tool_calls: [{index: 0, function: {arguments: "{\"name\":\"[MASKED_NAME]\"}"}}]}
	// Expected: Each choice has separate demasker, outputs independently

	p := New(bufferingDemaskingDemasker())
	ctx := context.Background()

	// Create chunk with choice 0 tool call
	chunk1 := makeToolCallChunk(0, "call_0", "function", "tool_a", `{"city":"[MASKED_CITY]"}`)
	chunk1.Choices[0].Index = 0

	// Create chunk with choice 1 tool call
	chunk2 := makeToolCallChunk(0, "call_1", "function", "tool_b", `{"name":"[MASKED_NAME]"}`)
	chunk2.Choices[0].Index = 1

	frame1 := makeFrame(chunk1)
	frame2 := makeFrame(chunk2)

	// Process both chunks
	out1, err := p.ProcessChunk(ctx, []byte(frame1), false)
	require.NoError(t, err)
	require.NotNil(t, out1)

	out2, err := p.ProcessChunk(ctx, []byte(frame2), false)
	require.NoError(t, err)
	require.NotNil(t, out2)

	// Verify choice 0 output
	frames1, _ := splitFrames(out1)
	require.Len(t, frames1, 1)
	parsed1 := parseDataFrame(t, frames1[0])
	assert.Equal(t, 0, parsed1.Choices[0].Index)
	assert.Equal(t, `{"city":"San Francisco"}`, parsed1.Choices[0].Delta.ToolCalls[0].Function.Arguments)

	// Verify choice 1 output
	frames2, _ := splitFrames(out2)
	require.Len(t, frames2, 1)
	parsed2 := parseDataFrame(t, frames2[0])
	assert.Equal(t, 1, parsed2.Choices[0].Index)
	assert.Equal(t, `{"name":"Alice"}`, parsed2.Choices[0].Delta.ToolCalls[0].Function.Arguments)
}

// TestProcessChunk_ToolCallDemasking_EmptyArguments tests tool call with no arguments.
func TestProcessChunk_ToolCallDemasking_EmptyArguments(t *testing.T) {
	// Scenario: Tool call with empty arguments string (a parameterless call
	// whose arguments never arrive).
	// Chunk 1: {tool_calls: [{index: 0, id: "call_1", function: {name: "no_args", arguments: ""}}]}
	// Expected: no demasking, but the id/name announcement is forwarded so the
	// tool call isn't dropped from the stream.

	p := New(bufferingDemaskingDemasker())
	ctx := context.Background()

	frame1 := makeFrame(makeToolCallChunk(0, "call_1", "function", "no_args", ``))

	out1, err := p.ProcessChunk(ctx, []byte(frame1), false)
	require.NoError(t, err)
	frames, _ := splitFrames(out1)
	require.Len(t, frames, 1, "parameterless tool call must be forwarded")
	parsed := parseDataFrame(t, frames[0])
	assert.Equal(t, "call_1", parsed.Choices[0].Delta.ToolCalls[0].ID)
	assert.Equal(t, "no_args", parsed.Choices[0].Delta.ToolCalls[0].Function.Name)
}

// ============================================================================
// LEGACY FUNCTION_CALL TESTS
// ============================================================================

// makeFunctionCallChunk creates a Chunk with a legacy function_call delta.
func makeFunctionCallChunk(name, arguments string) llmchat.Chunk {
	var functionCall *llmchat.FunctionCallDelta
	if name != "" || arguments != "" {
		functionCall = &llmchat.FunctionCallDelta{
			Name:      name,
			Arguments: arguments,
		}
	}

	return llmchat.Chunk{
		ID:      "chatcmpl-123",
		Object:  "chat.completion.chunk",
		Created: 1234567890,
		Model:   "gpt-3.5-turbo",
		Choices: []llmchat.ChunkChoice{
			{
				Index: 0,
				Delta: llmchat.Delta{
					FunctionCall: functionCall,
				},
			},
		},
	}
}

// TestProcessChunk_FunctionCall_IncrementalArguments tests legacy function_call
// with arguments spread across multiple chunks.
func TestProcessChunk_FunctionCall_IncrementalArguments(t *testing.T) {
	// Scenario: function_call with name in first chunk, then incremental arguments
	// Chunk 1: {delta: {function_call: {name: "get_weather", arguments: "{\"loc"}}}
	// Chunk 2: {delta: {function_call: {arguments: "ation\":"}}}
	// Chunk 3: {delta: {function_call: {arguments: "\"[MASKED_CITY]\"}"}}}
	// Expected: Demasked function call output with full arguments

	p := New(bufferingDemaskingDemasker())
	ctx := context.Background()

	frame1 := makeFrame(makeFunctionCallChunk("get_weather", `{"loc`))
	frame2 := makeFrame(makeFunctionCallChunk("", `ation":`))
	frame3 := makeFrame(makeFunctionCallChunk("", `"[MASKED_CITY]"}`))

	// Process chunks
	out1, err := p.ProcessChunk(ctx, []byte(frame1), false)
	require.NoError(t, err)
	// Demasker buffers, so no output yet
	if out1 != nil {
		frames1, _ := splitFrames(out1)
		require.Len(t, frames1, 0, "Expected no output (demasker buffering)")
	}

	out2, err := p.ProcessChunk(ctx, []byte(frame2), false)
	require.NoError(t, err)
	// Still buffering
	if out2 != nil {
		frames2, _ := splitFrames(out2)
		require.Len(t, frames2, 0, "Expected no output (demasker buffering)")
	}

	out3, err := p.ProcessChunk(ctx, []byte(frame3), false)
	require.NoError(t, err)

	// JSON should close on chunk 3, triggering flush
	require.NotNil(t, out3, "Expected output when JSON closes")

	frames, _ := splitFrames(out3)
	require.Len(t, frames, 1, "Expected 1 frame on JSON close")

	parsed := parseDataFrame(t, frames[0])
	require.NotNil(t, parsed.Choices[0].Delta.FunctionCall)
	assert.Equal(t, "get_weather", parsed.Choices[0].Delta.FunctionCall.Name)
	assert.Equal(t, `{"location":"San Francisco"}`, parsed.Choices[0].Delta.FunctionCall.Arguments)
}

// TestProcessChunk_FunctionCall_WithFinishReason tests function_call followed by finish_reason.
func TestProcessChunk_FunctionCall_WithFinishReason(t *testing.T) {
	// Scenario: function_call completes, then metadata with finish_reason="function_call"
	// Chunk 1: {delta: {function_call: {name: "search", arguments: "{\"query\":\"[MASKED_QUERY]\"}"}}}
	// Chunk 2: {choices: [{finish_reason: "function_call"}], usage: {...}}
	// Expected: Demasked function call, then metadata frame

	p := New(bufferingDemaskingDemasker())
	ctx := context.Background()

	frame1 := makeFrame(makeFunctionCallChunk("search", `{"query":"[MASKED_QUERY]"}`))

	// Create metadata frame
	metadataChunk := llmchat.Chunk{
		ID:      "chatcmpl-123",
		Object:  "chat.completion.chunk",
		Created: 1234567890,
		Model:   "gpt-3.5-turbo",
		Choices: []llmchat.ChunkChoice{
			{
				Index:        0,
				Delta:        llmchat.Delta{},
				FinishReason: toPtr("function_call"),
			},
		},
		Usage: &llmchat.Usage{
			PromptTokens:     10,
			CompletionTokens: 5,
			TotalTokens:      15,
		},
	}
	frame2 := makeFrame(metadataChunk)

	// Process chunks
	out1, err := p.ProcessChunk(ctx, []byte(frame1), false)
	require.NoError(t, err)
	require.NotNil(t, out1, "Expected output on JSON close")

	out2, err := p.ProcessChunk(ctx, []byte(frame2), false)
	require.NoError(t, err)
	require.NotNil(t, out2, "Expected metadata output on finish_reason")

	// Verify function call output
	frames1, _ := splitFrames(out1)
	require.Len(t, frames1, 1)
	parsed1 := parseDataFrame(t, frames1[0])
	require.NotNil(t, parsed1.Choices[0].Delta.FunctionCall)
	assert.Equal(t, "search", parsed1.Choices[0].Delta.FunctionCall.Name)
	assert.Equal(t, `{"query":"test query"}`, parsed1.Choices[0].Delta.FunctionCall.Arguments)

	// Verify metadata output
	frames2, _ := splitFrames(out2)
	require.Len(t, frames2, 1)
	parsed2 := parseDataFrame(t, frames2[0])
	assert.Equal(t, "function_call", *parsed2.Choices[0].FinishReason)
	assert.NotNil(t, parsed2.Usage)
}

// TestProcessChunk_FunctionCall_BufferingDemasker tests function_call with buffering demasker.
func TestProcessChunk_FunctionCall_BufferingDemasker(t *testing.T) {
	// Scenario: Demasker buffers all chunks, returns on flush
	// Chunk 1-3: Incremental arguments
	// Expected: Single output when JSON closes on chunk 3

	p := New(bufferingDemaskingDemasker())
	ctx := context.Background()

	frame1 := makeFrame(makeFunctionCallChunk("calculate", `{"op`))
	frame2 := makeFrame(makeFunctionCallChunk("", `":"add"`))
	frame3 := makeFrame(makeFunctionCallChunk("", `}`))

	out1, err := p.ProcessChunk(ctx, []byte(frame1), false)
	require.NoError(t, err)
	// Buffering
	if out1 != nil {
		frames1, _ := splitFrames(out1)
		require.Len(t, frames1, 0)
	}

	out2, err := p.ProcessChunk(ctx, []byte(frame2), false)
	require.NoError(t, err)
	// Still buffering
	if out2 != nil {
		frames2, _ := splitFrames(out2)
		require.Len(t, frames2, 0)
	}

	out3, err := p.ProcessChunk(ctx, []byte(frame3), false)
	require.NoError(t, err)
	require.NotNil(t, out3, "Expected output on JSON close")

	// Verify output
	frames, _ := splitFrames(out3)
	require.Len(t, frames, 1)
	parsed := parseDataFrame(t, frames[0])
	require.NotNil(t, parsed.Choices[0].Delta.FunctionCall)
	assert.Equal(t, `{"op":"add"}`, parsed.Choices[0].Delta.FunctionCall.Arguments)
}

// TestProcessChunk_FunctionCall_DemaskerError tests fallback when demasker fails.
func TestProcessChunk_FunctionCall_DemaskerError(t *testing.T) {
	// Scenario: Demasker errors, processor falls back to masked arguments
	// Chunk 1: {function_call: {name: "test", arguments: "{\"key\":\"[MASKED]\"}"}}
	// Expected: Fallback to masked arguments from choicesAccum

	p := New(errorDemasker())
	ctx := context.Background()

	frame1 := makeFrame(makeFunctionCallChunk("test", `{"key":"[MASKED]"}`))

	out1, err := p.ProcessChunk(ctx, []byte(frame1), false)
	require.NoError(t, err)
	require.NotNil(t, out1, "Expected fallback output on demasker error")

	// Verify fallback output (should be masked)
	frames, _ := splitFrames(out1)
	require.Len(t, frames, 1)
	parsed := parseDataFrame(t, frames[0])
	require.NotNil(t, parsed.Choices[0].Delta.FunctionCall)
	assert.Equal(t, `{"key":"[MASKED]"}`, parsed.Choices[0].Delta.FunctionCall.Arguments)
}

// TestProcessChunk_FunctionCall_WithContent tests function_call alongside content.
func TestProcessChunk_FunctionCall_WithContent(t *testing.T) {
	// Scenario: Some chunks have content, some have function_call
	// Chunk 1: {delta: {content: "Let me search for that."}}
	// Chunk 2: {delta: {function_call: {name: "search", arguments: "{\"q\":\"[MASKED]\"}"}}}
	// Expected: Content output on EOS, function_call output on JSON close (separate frames)

	p := New(bufferingDemaskingDemasker())
	ctx := context.Background()

	frame1 := makeFrame(makeContentChunk("Let me search."))
	frame2 := makeFrame(makeFunctionCallChunk("search", `{"q":"[MASKED]"}`))

	out1, err := p.ProcessChunk(ctx, []byte(frame1), false)
	require.NoError(t, err)
	// Content buffers
	if out1 != nil {
		frames1, _ := splitFrames(out1)
		require.Len(t, frames1, 0, "Content should buffer")
	}

	out2, err := p.ProcessChunk(ctx, []byte(frame2), false)
	require.NoError(t, err)
	require.NotNil(t, out2, "Expected function_call output on JSON close")

	// Verify function_call output
	frames2, _ := splitFrames(out2)
	require.Len(t, frames2, 1)
	parsed2 := parseDataFrame(t, frames2[0])
	require.NotNil(t, parsed2.Choices[0].Delta.FunctionCall)
	assert.Nil(t, parsed2.Choices[0].Delta.Content, "Function call frame should not have content")

	// Flush remaining content on EOS
	outEOS, err := p.ProcessChunk(ctx, nil, true)
	require.NoError(t, err)
	require.NotNil(t, outEOS, "Expected content output on EOS")

	framesEOS, _ := splitFrames(outEOS)
	require.Len(t, framesEOS, 1)
	parsedEOS := parseDataFrame(t, framesEOS[0])
	assert.Equal(t, "Let me search.", *parsedEOS.Choices[0].Delta.Content)
	assert.Nil(t, parsedEOS.Choices[0].Delta.FunctionCall, "Content frame should not have function_call")
}

// TestProcessChunk_FunctionCall_PartialDemaskerOutput tests demasker returning partial results.
func TestProcessChunk_FunctionCall_PartialDemaskerOutput(t *testing.T) {
	// Scenario: Demasker returns partial results before flush
	// Uses passthroughDemaskingDemasker which returns immediately
	// Chunk 1: {function_call: {name: "fn", arguments: "{\"a\":"}}
	// Chunk 2: {function_call: {arguments: "\"[MASKED]\""}}
	// Chunk 3: {function_call: {arguments: "}"}}
	// Expected: Multiple output frames with deltas

	p := New(passthroughDemaskingDemasker())
	ctx := context.Background()

	frame1 := makeFrame(makeFunctionCallChunk("fn", `{"a":`))
	frame2 := makeFrame(makeFunctionCallChunk("", `"[MASKED]"`))
	frame3 := makeFrame(makeFunctionCallChunk("", `}`))

	out1, err := p.ProcessChunk(ctx, []byte(frame1), false)
	require.NoError(t, err)
	require.NotNil(t, out1, "Passthrough demasker should return immediately")

	out2, err := p.ProcessChunk(ctx, []byte(frame2), false)
	require.NoError(t, err)
	require.NotNil(t, out2)

	out3, err := p.ProcessChunk(ctx, []byte(frame3), false)
	require.NoError(t, err)
	require.NotNil(t, out3)

	// Verify we got multiple frames with deltas
	frames1, _ := splitFrames(out1)
	frames2, _ := splitFrames(out2)
	frames3, _ := splitFrames(out3)

	require.Len(t, frames1, 1)
	require.Len(t, frames2, 1)
	require.Len(t, frames3, 1)

	// Concatenate all arguments to verify full demasked output
	parsed1 := parseDataFrame(t, frames1[0])
	parsed2 := parseDataFrame(t, frames2[0])
	parsed3 := parseDataFrame(t, frames3[0])

	args1 := parsed1.Choices[0].Delta.FunctionCall.Arguments
	args2 := parsed2.Choices[0].Delta.FunctionCall.Arguments
	args3 := parsed3.Choices[0].Delta.FunctionCall.Arguments

	fullArgs := args1 + args2 + args3
	assert.Equal(t, `{"a":"test value"}`, fullArgs)
}

// TestProcessChunk_FunctionCall_WithDone tests function_call followed by [DONE].
func TestProcessChunk_FunctionCall_WithDone(t *testing.T) {
	// Scenario: function_call completes, then [DONE] marker
	// Chunk 1: {function_call: {name: "test", arguments: "{}"}}
	// Chunk 2: [DONE]
	// Expected: function_call frame, then [DONE] frame

	p := New(passthroughDemaskingDemasker())
	ctx := context.Background()

	frame1 := makeFrame(makeFunctionCallChunk("test", `{}`))
	doneFr := makeDoneFrame()

	out1, err := p.ProcessChunk(ctx, []byte(frame1), false)
	require.NoError(t, err)
	require.NotNil(t, out1)

	outDone, err := p.ProcessChunk(ctx, []byte(doneFr), true)
	require.NoError(t, err)
	require.NotNil(t, outDone)

	// Verify [DONE] is in output
	assert.Contains(t, string(outDone), "[DONE]")
}

// TestProcessChunk_FunctionCall_NestedJSON tests function_call with nested JSON.
func TestProcessChunk_FunctionCall_NestedJSON(t *testing.T) {
	// Scenario: function_call with nested JSON arguments
	// Chunk 1: {function_call: {name: "complex", arguments: "{\"outer\":{"}}
	// Chunk 2: {function_call: {arguments: "\"inner\":\"[MASKED]\""}}
	// Chunk 3: {function_call: {arguments: "}}"}}
	// Expected: Correct depth tracking, flush on final }

	p := New(bufferingDemaskingDemasker())
	ctx := context.Background()

	frame1 := makeFrame(makeFunctionCallChunk("complex", `{"outer":{`))
	frame2 := makeFrame(makeFunctionCallChunk("", `"inner":"[MASKED]"`))
	frame3 := makeFrame(makeFunctionCallChunk("", `}}`))

	out1, err := p.ProcessChunk(ctx, []byte(frame1), false)
	require.NoError(t, err)
	// Should buffer (JSON not closed)
	if out1 != nil {
		frames1, _ := splitFrames(out1)
		require.Len(t, frames1, 0, "JSON not closed, should buffer")
	}

	out2, err := p.ProcessChunk(ctx, []byte(frame2), false)
	require.NoError(t, err)
	// Still buffering
	if out2 != nil {
		frames2, _ := splitFrames(out2)
		require.Len(t, frames2, 0, "JSON still not closed, should buffer")
	}

	out3, err := p.ProcessChunk(ctx, []byte(frame3), false)
	require.NoError(t, err)
	require.NotNil(t, out3, "Expected output when nested JSON closes")

	// Verify output
	frames, _ := splitFrames(out3)
	require.Len(t, frames, 1)
	parsed := parseDataFrame(t, frames[0])
	require.NotNil(t, parsed.Choices[0].Delta.FunctionCall)
	assert.Equal(t, `{"outer":{"inner":"test value"}}`, parsed.Choices[0].Delta.FunctionCall.Arguments)
}

// ============================================================================
// REGRESSION TESTS: passthrough frames must not break placeholder demasking
// ============================================================================

// placeholderDemasker emulates the real demasker's pending-buffer behavior for
// a single placeholder: complete occurrences are replaced, a trailing partial
// prefix is withheld until it is completed by a later chunk or flushed.
func placeholderDemasker(placeholder, original string) common.DemaskerFactoryFn {
	return func() common.Demasker {
		pending := ""
		return newDemaskerMock(func(_ context.Context, chunk string, flush bool) (string, error) {
			text := pending + chunk
			pending = ""
			text = strings.ReplaceAll(text, placeholder, original)
			if flush {
				return text, nil
			}
			for l := len(placeholder) - 1; l > 0; l-- {
				if strings.HasSuffix(text, placeholder[:l]) {
					pending = placeholder[:l]
					return text[:len(text)-l], nil
				}
			}
			return text, nil
		})
	}
}

// TestProcessChunk_PassthroughFrameDoesNotBreakSplitPlaceholder covers the
// regression where the non-demaskable passthrough branch force-flushed the
// choice demaskers, emitting a partially buffered placeholder ("<EMA") that
// the following content delta could then never complete.
func TestProcessChunk_PassthroughFrameDoesNotBreakSplitPlaceholder(t *testing.T) {
	p := New(placeholderDemasker("<EMAIL_1>", "user@example.com"))
	ctx := context.Background()

	var out []byte
	feed := func(frame string, eos bool) {
		got, err := p.ProcessChunk(ctx, []byte(frame), eos)
		require.NoError(t, err)
		out = append(out, got...)
	}

	feed(makeFrame(makeContentChunk("contact <EMA")), false)
	// Empty keepalive delta between the two halves of the placeholder: must be
	// forwarded, and must NOT flush the pending "<EMA" tail.
	feed(makeFrame(makeContentChunk("")), false)
	feed(makeFrame(makeContentChunk("IL_1> now")), false)
	feed(makeDoneFrame(), true)

	// The client concatenates content deltas; the joined text must carry the
	// restored original and no placeholder fragment.
	frames, _ := splitFrames(out)
	var joined strings.Builder
	for _, f := range frames {
		if bytes.Contains(f, []byte("[DONE]")) {
			continue
		}
		parsed := parseDataFrame(t, f)
		if len(parsed.Choices) > 0 && parsed.Choices[0].Delta.Content != nil {
			joined.WriteString(*parsed.Choices[0].Delta.Content)
		}
	}
	assert.Equal(t, "contact user@example.com now", joined.String())
	assert.NotContains(t, string(out), "<EMA")
}

// TestProcessChunk_PerChunkUsagePreservesCadence covers the regression where
// providers attaching usage to every content chunk (vLLM continuous usage
// stats) had their per-chunk usage snapshots buffered — either dumped as a
// stale batch at stream end (unbounded growth) or collapsed to one. usage now
// rides the stream in position, so the client sees the same per-chunk cadence
// it requested and the buffer never grows.
func TestProcessChunk_PerChunkUsagePreservesCadence(t *testing.T) {
	p := New(passthroughDemasker())
	ctx := context.Background()

	makeContentUsageChunk := func(content string, totalTokens int) llmchat.Chunk {
		chunk := makeContentChunk(content)
		chunk.Usage = &llmchat.Usage{TotalTokens: totalTokens}
		return chunk
	}

	var out []byte
	for i := 1; i <= 5; i++ {
		got, err := p.ProcessChunk(ctx, []byte(makeFrame(makeContentUsageChunk(fmt.Sprintf("tok%d ", i), i))), false)
		require.NoError(t, err)
		out = append(out, got...)
	}

	got, err := p.ProcessChunk(ctx, []byte(makeDoneFrame()), true)
	require.NoError(t, err)
	out = append(out, got...)

	frames, _ := splitFrames(out)
	var usageTotals []int
	for _, f := range frames {
		if bytes.Contains(f, []byte(`"usage"`)) && bytes.Contains(f, []byte(`"total_tokens"`)) {
			parsed := parseDataFrame(t, f)
			require.NotNil(t, parsed.Usage)
			usageTotals = append(usageTotals, parsed.Usage.TotalTokens)
		}
	}
	// One usage frame per content chunk, each carrying its own running total,
	// in order — not batched at the end, not collapsed to the last.
	assert.Equal(t, []int{1, 2, 3, 4, 5}, usageTotals,
		"each per-chunk usage snapshot must reach the client in position")
}

// ============================================================================
// Regression: tool-call / function_call argument fragments must be JSON-safe
// ============================================================================

// fakeToolArgsRegistry lets a real demask.Factory run with only its
// max-placeholder-length dependency satisfied.
type fakeToolArgsRegistry struct{}

func (fakeToolArgsRegistry) GetMaxPlaceholderLenByRuleIDs(...string) int { return 32 }

// collectToolArgs reassembles function.arguments across every emitted
// tool_calls frame exactly as a client accumulating the stream would.
func collectToolArgs(t *testing.T, out []byte) string {
	t.Helper()
	var b strings.Builder
	frames, _ := splitFrames(out)
	for _, f := range frames {
		if bytes.Contains(bytes.TrimSpace(f), []byte("[DONE]")) {
			continue
		}
		c := parseDataFrame(t, f)
		for _, ch := range c.Choices {
			for _, tc := range ch.Delta.ToolCalls {
				if tc.Function != nil {
					b.WriteString(tc.Function.Arguments)
				}
			}
		}
	}
	return b.String()
}

// collectFunctionCallArgs reassembles legacy function_call.arguments across
// every emitted frame.
func collectFunctionCallArgs(t *testing.T, out []byte) string {
	t.Helper()
	var b strings.Builder
	frames, _ := splitFrames(out)
	for _, f := range frames {
		if bytes.Contains(bytes.TrimSpace(f), []byte("[DONE]")) {
			continue
		}
		c := parseDataFrame(t, f)
		for _, ch := range c.Choices {
			if ch.Delta.FunctionCall != nil {
				b.WriteString(ch.Delta.FunctionCall.Arguments)
			}
		}
	}
	return b.String()
}

// A restored original containing JSON metacharacters must be JSON-escaped when
// inserted into tool_calls[].function.arguments fragments: the client
// accumulates the fragments into a JSON object, and a verbatim quote/backslash
// would corrupt it unrecoverably (chat-completions never re-sends the full
// arguments in a later event).
func TestProcessChunk_ToolCallArgs_EscapesRestoredOriginals(t *testing.T) {
	provider := demask.NewProvider(fakeToolArgsRegistry{}, nil)
	factory := provider.NewFactory(models.MaskingState{Replacements: []models.Replacement{
		{RuleID: "secret", Original: `C:\Users\x say "hi"`, Placeholder: "<SECRET_1>"},
	}})
	p := New(
		func() common.Demasker { return factory.Demasker() },
		WithJSONDemaskerFactory(func() common.Demasker { return factory.JSONDemasker() }),
	)

	ctx := context.Background()
	var out []byte
	feed := func(chunk llmchat.Chunk) {
		got, err := p.ProcessChunk(ctx, []byte(makeFrame(chunk)), false)
		require.NoError(t, err)
		out = append(out, got...)
	}
	// arguments = {"path":"<SECRET_1>"} arriving in fragments.
	feed(makeToolCallChunk(0, "call_1", "function", "save", `{"path":"`))
	feed(makeToolCallChunk(0, "", "", "", `<SECRET_1>`))
	feed(makeToolCallChunk(0, "", "", "", `"}`))
	got, err := p.ProcessChunk(ctx, nil, true) // EOS flush
	require.NoError(t, err)
	out = append(out, got...)

	args := collectToolArgs(t, out)
	require.True(t, json.Valid([]byte(args)), "accumulated tool arguments must be valid JSON: %q", args)
	assert.Equal(t, `C:\Users\x say "hi"`, gjson.Get(args, "path").String())
	assert.NotContains(t, args, "<SECRET_1>", "placeholder must be resolved")
}

// Same guarantee for the legacy function_call.arguments path (index -1).
func TestProcessChunk_FunctionCallArgs_EscapesRestoredOriginals(t *testing.T) {
	provider := demask.NewProvider(fakeToolArgsRegistry{}, nil)
	factory := provider.NewFactory(models.MaskingState{Replacements: []models.Replacement{
		{RuleID: "secret", Original: `back\slash and "quote"`, Placeholder: "<SECRET_1>"},
	}})
	p := New(
		func() common.Demasker { return factory.Demasker() },
		WithJSONDemaskerFactory(func() common.Demasker { return factory.JSONDemasker() }),
	)

	ctx := context.Background()
	var out []byte
	feed := func(chunk llmchat.Chunk) {
		got, err := p.ProcessChunk(ctx, []byte(makeFrame(chunk)), false)
		require.NoError(t, err)
		out = append(out, got...)
	}
	feed(makeFunctionCallChunk("save", `{"note":"`))
	feed(makeFunctionCallChunk("", `<SECRET_1>`))
	feed(makeFunctionCallChunk("", `"}`))
	got, err := p.ProcessChunk(ctx, nil, true)
	require.NoError(t, err)
	out = append(out, got...)

	args := collectFunctionCallArgs(t, out)
	require.True(t, json.Valid([]byte(args)), "accumulated function_call arguments must be valid JSON: %q", args)
	assert.Equal(t, `back\slash and "quote"`, gjson.Get(args, "note").String())
	assert.NotContains(t, args, "<SECRET_1>", "placeholder must be resolved")
}
