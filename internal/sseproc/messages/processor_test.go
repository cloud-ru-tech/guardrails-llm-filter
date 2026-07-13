package messages

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cloud-ru-tech/guardrails-llm-filter/internal/guardrails/demask"
	"github.com/cloud-ru-tech/guardrails-llm-filter/internal/models"
	"github.com/cloud-ru-tech/guardrails-llm-filter/internal/sseproc/common"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

// mockDemasker is a configurable test double. The handler decides what to
// return for each chunk; chunks (excluding the empty flush sentinel) are
// recorded for assertions.
type mockDemasker struct {
	chunks  []string
	flushes int
	handler func(ctx context.Context, chunk string, flush bool) (string, error)
}

func newDemasker(handler func(ctx context.Context, chunk string, flush bool) (string, error)) *mockDemasker {
	return &mockDemasker{handler: handler}
}

func (m *mockDemasker) DemaskChunk(ctx context.Context, chunk string, flush bool) (string, error) {
	if chunk != "" {
		m.chunks = append(m.chunks, chunk)
	}
	if flush {
		m.flushes++
	}
	if m.handler != nil {
		return m.handler(ctx, chunk, flush)
	}
	return chunk, nil
}

// passthrough returns each chunk immediately, unchanged.
func passthrough() common.DemaskerFactoryFn {
	return func() common.Demasker {
		return newDemasker(nil)
	}
}

// buffering accumulates every chunk and only emits the buffer on flush.
// This is the classic adversarial demasker that forces the processor to
// hold frames until the flush boundary.
func buffering() common.DemaskerFactoryFn {
	return func() common.Demasker {
		buf := ""
		return newDemasker(func(_ context.Context, chunk string, flush bool) (string, error) {
			buf += chunk
			if flush {
				out := buf
				buf = ""
				return out, nil
			}
			return "", nil
		})
	}
}

// replacing replaces every occurrence of `from` with `to` and emits the
// result of *each* incoming chunk immediately. This simulates a real
// demasker that doesn't buffer.
func replacing(from, to string) common.DemaskerFactoryFn {
	return func() common.Demasker {
		return newDemasker(func(_ context.Context, chunk string, _ bool) (string, error) {
			return strings.ReplaceAll(chunk, from, to), nil
		})
	}
}

// bufferingReplacing buffers every chunk then on flush replaces tokens.
func bufferingReplacing(from, to string) common.DemaskerFactoryFn {
	return func() common.Demasker {
		buf := ""
		return newDemasker(func(_ context.Context, chunk string, flush bool) (string, error) {
			buf += chunk
			if flush {
				out := strings.ReplaceAll(buf, from, to)
				buf = ""
				return out, nil
			}
			return "", nil
		})
	}
}

// erroring always fails, returning its un-emitted content (the chunk) with the
// error, per the Demasker contract: callers emit that as a lossless fallback.
func erroring() common.DemaskerFactoryFn {
	return func() common.Demasker {
		return newDemasker(func(_ context.Context, chunk string, _ bool) (string, error) {
			return chunk, errors.New("demasker boom")
		})
	}
}

// --- Frame helpers ---

func frame(eventName, dataJSON string) string {
	return "event: " + eventName + "\ndata: " + dataJSON + "\n\n"
}

func doneFrame() string {
	return "event: data\ndata: [DONE]\n\n"
}

func textDelta(text string) string {
	return fmt.Sprintf(`{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":%q}}`, text)
}

func thinkingDelta(idx int, thinking string) string {
	return fmt.Sprintf(`{"type":"content_block_delta","index":%d,"delta":{"type":"thinking_delta","thinking":%q}}`, idx, thinking)
}

func inputJSONDelta(idx int, partial string) string {
	return fmt.Sprintf(`{"type":"content_block_delta","index":%d,"delta":{"type":"input_json_delta","partial_json":%q}}`, idx, partial)
}

func signatureDelta(idx int, sig string) string {
	return fmt.Sprintf(`{"type":"content_block_delta","index":%d,"delta":{"type":"signature_delta","signature":%q}}`, idx, sig)
}

func blockStart(idx int, typ string) string {
	return fmt.Sprintf(`{"type":"content_block_start","index":%d,"content_block":{"type":%q,"text":"","citations":null}}`, idx, typ)
}

func blockStop(idx int) string {
	return fmt.Sprintf(`{"type":"content_block_stop","index":%d}`, idx)
}

func messageStart() string {
	return `{"type":"message_start","message":{"id":"msg_1","role":"assistant","model":"claude"}}`
}

func messageDelta() string {
	return `{"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"input_tokens":3,"output_tokens":5}}`
}

func messageStop() string {
	return `{"type":"message_stop"}`
}

func processAll(t *testing.T, p *Processor, chunks []string) []byte {
	t.Helper()
	var out []byte
	for i, c := range chunks {
		eos := i == len(chunks)-1
		o, err := p.ProcessChunk(context.Background(), []byte(c), eos)
		require.NoError(t, err)
		out = append(out, o...)
	}
	return out
}

// dataPayloads extracts every "data: <payload>" line from raw SSE output,
// preserving order. [DONE] sentinels are included verbatim.
func dataPayloads(t *testing.T, raw []byte) []string {
	t.Helper()
	var out []string
	for _, line := range strings.Split(string(raw), "\n") {
		if strings.HasPrefix(line, "data: ") {
			out = append(out, strings.TrimPrefix(line, "data: "))
		}
	}
	return out
}

func eventNames(t *testing.T, raw []byte) []string {
	t.Helper()
	var out []string
	for _, line := range strings.Split(string(raw), "\n") {
		if strings.HasPrefix(line, "event: ") {
			out = append(out, strings.TrimPrefix(line, "event: "))
		}
	}
	return out
}

// --- Tests ---

func TestProcessor_TextBlock_PassthroughDemasker(t *testing.T) {
	p := New(passthrough())
	body := frame("message_start", messageStart()) +
		frame("content_block_start", blockStart(0, "text")) +
		frame("content_block_delta", textDelta("Hello ")) +
		frame("content_block_delta", textDelta("world")) +
		frame("content_block_stop", blockStop(0)) +
		frame("message_delta", messageDelta()) +
		frame("message_stop", messageStop()) +
		doneFrame()

	out := processAll(t, p, []string{body})

	// Every event must come through in order.
	assert.Equal(t,
		[]string{"message_start", "content_block_start", "content_block_delta", "content_block_delta", "content_block_stop", "message_delta", "message_stop", "data"},
		eventNames(t, out),
	)

	// Combined text across both content_block_delta payloads equals input.
	payloads := dataPayloads(t, out)
	var combined string
	for _, pld := range payloads {
		if gjson.Get(pld, "type").String() == "content_block_delta" {
			combined += gjson.Get(pld, "delta.text").String()
		}
	}
	assert.Equal(t, "Hello world", combined)
}

func TestProcessor_TextBlock_BufferingDemasker_FlushOnBlockStop(t *testing.T) {
	p := New(buffering())

	body := frame("content_block_start", blockStart(0, "text")) +
		frame("content_block_delta", textDelta("Hello ")) +
		frame("content_block_delta", textDelta("world")) +
		frame("content_block_stop", blockStop(0)) +
		doneFrame()

	out := processAll(t, p, []string{body})

	// Buffering demasker swallows the two delta frames; on content_block_stop
	// we synthesize a single delta carrying the full aggregated text, then
	// emit the stop, then [DONE].
	payloads := dataPayloads(t, out)
	require.Len(t, payloads, 4, "start, synthetic delta, stop, [DONE]; got: %v", payloads)
	assert.Equal(t, "content_block_start", gjson.Get(payloads[0], "type").String())
	assert.Equal(t, "content_block_delta", gjson.Get(payloads[1], "type").String())
	assert.Equal(t, "Hello world", gjson.Get(payloads[1], "delta.text").String())
	assert.Equal(t, "content_block_stop", gjson.Get(payloads[2], "type").String())
	assert.Equal(t, "[DONE]", payloads[3])
}

func TestProcessor_TextBlock_Demasking(t *testing.T) {
	p := New(bufferingReplacing("<EMAIL_1>", "alice@example.com"))

	body := frame("content_block_start", blockStart(0, "text")) +
		frame("content_block_delta", textDelta("Contact me at ")) +
		frame("content_block_delta", textDelta("<EMAIL_1>")) +
		frame("content_block_delta", textDelta(" for details.")) +
		frame("content_block_stop", blockStop(0)) +
		doneFrame()

	out := processAll(t, p, []string{body})
	payloads := dataPayloads(t, out)

	require.Len(t, payloads, 4)
	assert.Equal(t, "Contact me at alice@example.com for details.", gjson.Get(payloads[1], "delta.text").String())
}

func TestProcessor_ToolUse_JSONDepthFlush(t *testing.T) {
	// Build a tool_use block whose partial_json arrives in fragments that
	// only complete the JSON object on the last fragment. The demasker
	// should be told flush=true only when the object closes.
	d := newDemasker(nil)
	calls := []struct {
		chunk string
		flush bool
	}{}
	d.handler = func(_ context.Context, chunk string, flush bool) (string, error) {
		calls = append(calls, struct {
			chunk string
			flush bool
		}{chunk, flush})
		return chunk, nil
	}
	p := New(func() common.Demasker { return d })

	body := frame("content_block_start", blockStart(0, "tool_use")) +
		frame("content_block_delta", inputJSONDelta(0, `{"city":`)) +
		frame("content_block_delta", inputJSONDelta(0, `"Paris"`)) +
		frame("content_block_delta", inputJSONDelta(0, `}`)) +
		frame("content_block_stop", blockStop(0)) +
		doneFrame()

	_ = processAll(t, p, []string{body})

	// Three partial_json fragments hit the demasker. Only the last one
	// closes the JSON object (depth → 0), so flush must be true only on
	// that call. (The content_block_stop flush is a final empty-chunk
	// call to the same demasker — sseproc behaviour parity.)
	require.GreaterOrEqual(t, len(calls), 3)
	assert.False(t, calls[0].flush, "first partial_json should not flush")
	assert.False(t, calls[1].flush, "second partial_json should not flush")
	assert.True(t, calls[2].flush, "JSON-closing partial_json must flush")
}

func TestProcessor_ToolUse_JSONDepthFlush_ValueContainsBrace(t *testing.T) {
	// A tool_use argument value that itself contains a '}' inside the string,
	// with the closing quote+brace arriving in a later fragment. A stateless
	// depth tracker resets its in-string state each fragment, so it would read
	// the in-string '}' of fragment 2 as a real object close and flush early —
	// which can split a placeholder straddling that boundary. The tracker must
	// carry string state across fragments and flush only on the true close.
	d := newDemasker(nil)
	var calls []struct {
		chunk string
		flush bool
	}
	d.handler = func(_ context.Context, chunk string, flush bool) (string, error) {
		calls = append(calls, struct {
			chunk string
			flush bool
		}{chunk, flush})
		return chunk, nil
	}
	p := New(func() common.Demasker { return d })

	body := frame("content_block_start", blockStart(0, "tool_use")) +
		frame("content_block_delta", inputJSONDelta(0, `{"note":"start`)) +
		frame("content_block_delta", inputJSONDelta(0, `end } more`)) +
		frame("content_block_delta", inputJSONDelta(0, `"}`)) +
		frame("content_block_stop", blockStop(0)) +
		doneFrame()

	_ = processAll(t, p, []string{body})

	require.GreaterOrEqual(t, len(calls), 3)
	assert.False(t, calls[0].flush, "fragment 1 opens the object")
	assert.False(t, calls[1].flush, "fragment 2 is still inside the string value (in-string '}')")
	assert.True(t, calls[2].flush, "fragment 3 closes the object")
}

func TestProcessor_ToolUse_DemaskingAcrossManyChunks(t *testing.T) {
	p := New(bufferingReplacing("<EMAIL_1>", "alice@example.com"))

	// Split the same JSON across many tiny fragments, including one that
	// puts '<EMAIL_1>' across a chunk boundary.
	body := frame("content_block_start", blockStart(0, "tool_use")) +
		frame("content_block_delta", inputJSONDelta(0, `{"to":"`)) +
		frame("content_block_delta", inputJSONDelta(0, `<EMA`)) +
		frame("content_block_delta", inputJSONDelta(0, `IL_1>`)) +
		frame("content_block_delta", inputJSONDelta(0, `"}`)) +
		frame("content_block_stop", blockStop(0)) +
		doneFrame()

	out := processAll(t, p, []string{body})
	payloads := dataPayloads(t, out)

	// Buffering demasker → exactly one synthetic delta before stop.
	var deltas []string
	for _, pld := range payloads {
		if gjson.Get(pld, "type").String() == "content_block_delta" {
			deltas = append(deltas, gjson.Get(pld, "delta.partial_json").String())
		}
	}
	require.Len(t, deltas, 1)
	assert.Equal(t, `{"to":"alice@example.com"}`, deltas[0])
}

func TestProcessor_ThinkingBlock_DemaskedAndSignaturePassthrough(t *testing.T) {
	p := New(replacing("<NAME_1>", "Alice"))

	body := frame("content_block_start", blockStart(0, "thinking")) +
		frame("content_block_delta", thinkingDelta(0, "Considering <NAME_1>'s request")) +
		frame("content_block_delta", signatureDelta(0, "encrypted-signature-bytes")) +
		frame("content_block_stop", blockStop(0)) +
		doneFrame()

	out := processAll(t, p, []string{body})
	payloads := dataPayloads(t, out)

	var thinkingFound, sigFound bool
	for _, pld := range payloads {
		switch gjson.Get(pld, "delta.type").String() {
		case "thinking_delta":
			assert.Equal(t, "Considering Alice's request", gjson.Get(pld, "delta.thinking").String())
			thinkingFound = true
		case "signature_delta":
			assert.Equal(t, "encrypted-signature-bytes", gjson.Get(pld, "delta.signature").String())
			sigFound = true
		}
	}
	assert.True(t, thinkingFound, "thinking delta should be present")
	assert.True(t, sigFound, "signature delta should pass through")
}

func TestProcessor_PartialFrameAcrossChunks(t *testing.T) {
	p := New(passthrough())

	full := frame("content_block_start", blockStart(0, "text")) +
		frame("content_block_delta", textDelta("split")) +
		frame("content_block_stop", blockStop(0)) +
		doneFrame()

	// Cut in the middle of the delta data: line.
	cut := strings.Index(full, `"text":"split"`)
	require.Greater(t, cut, 0)
	first, second := full[:cut+5], full[cut+5:]

	out := processAll(t, p, []string{first, second})
	assert.Equal(t,
		[]string{"content_block_start", "content_block_delta", "content_block_stop", "data"},
		eventNames(t, out),
	)
	payloads := dataPayloads(t, out)
	var combined string
	for _, pld := range payloads {
		if gjson.Get(pld, "type").String() == "content_block_delta" {
			combined += gjson.Get(pld, "delta.text").String()
		}
	}
	assert.Equal(t, "split", combined)
}

func TestProcessor_CommentAndPingPassthrough(t *testing.T) {
	p := New(passthrough())
	body := ": PROCESSING\n\n" +
		frame("ping", `{"type":"ping"}`) +
		frame("content_block_start", blockStart(0, "text")) +
		frame("content_block_delta", textDelta("x")) +
		frame("content_block_stop", blockStop(0)) +
		doneFrame()

	out := processAll(t, p, []string{body})
	s := string(out)
	assert.Contains(t, s, ": PROCESSING")
	assert.Contains(t, s, "event: ping")
}

func TestProcessor_RedactedThinkingPassthrough(t *testing.T) {
	// A redacted_thinking block has an opaque encrypted `data` field on the
	// content_block_start; some upstreams may also emit deltas. Either way,
	// the demasker must NOT see those bytes.
	d := newDemasker(nil)
	d.handler = func(_ context.Context, chunk string, _ bool) (string, error) {
		t.Errorf("demasker must not be called for redacted_thinking, got chunk %q", chunk)
		return chunk, nil
	}
	p := New(func() common.Demasker { return d })

	body := frame("content_block_start", `{"type":"content_block_start","index":0,"content_block":{"type":"redacted_thinking","data":"encrypted-bytes"}}`) +
		frame("content_block_delta", textDelta("should-pass-through")) +
		frame("content_block_stop", blockStop(0)) +
		doneFrame()

	out := processAll(t, p, []string{body})
	assert.Contains(t, string(out), "should-pass-through")
}

// A text_delta arriving without a preceding content_block_start synthesizes a
// block with an empty type. The buffered tail (a split placeholder) must still
// be flushed and demasked on EOS — flushing keys off the live demasker, not the
// (empty) block type. Regression for held-tail loss.
func TestProcessor_FlushesTailWithoutContentBlockStart(t *testing.T) {
	p := New(bufferingReplacing("<EMAIL_1>", "a@b.com"))

	// No content_block_start; placeholder split across two text deltas; the
	// stream ends with message_stop (no [DONE]) so EOS drives the flush.
	out := processAll(t, p, []string{
		frame("content_block_delta", textDelta("call <EMA")),
		frame("content_block_delta", textDelta("IL_1>")),
		frame("message_stop", messageStop()),
	})

	s := string(out)
	assert.Contains(t, s, "a@b.com", "held tail must be demasked and emitted")
	assert.NotContains(t, s, "<EMAIL_1>", "placeholder must not leak")
}

func TestProcessor_DemaskerError_TextFallback(t *testing.T) {
	p := New(erroring())

	body := frame("content_block_start", blockStart(0, "text")) +
		frame("content_block_delta", textDelta("Hello <EMAIL_1>")) +
		frame("content_block_stop", blockStop(0)) +
		doneFrame()

	out := processAll(t, p, []string{body})
	payloads := dataPayloads(t, out)

	// On demasker error we emit its un-emitted content (placeholders intact) so
	// the client still gets the content — better masked than missing.
	var sawDelta bool
	for _, pld := range payloads {
		if gjson.Get(pld, "type").String() == "content_block_delta" {
			assert.Equal(t, "Hello <EMAIL_1>", gjson.Get(pld, "delta.text").String())
			sawDelta = true
		}
	}
	assert.True(t, sawDelta, "fallback delta must be emitted")
}

func TestProcessor_DemaskerError_InputJSONFallback(t *testing.T) {
	p := New(erroring())

	body := frame("content_block_start", blockStart(0, "tool_use")) +
		frame("content_block_delta", inputJSONDelta(0, `{"a":"<EMAIL_1>"}`)) +
		frame("content_block_stop", blockStop(0)) +
		doneFrame()

	out := processAll(t, p, []string{body})
	payloads := dataPayloads(t, out)

	var sawDelta bool
	for _, pld := range payloads {
		if gjson.Get(pld, "type").String() == "content_block_delta" {
			assert.Equal(t, `{"a":"<EMAIL_1>"}`, gjson.Get(pld, "delta.partial_json").String())
			sawDelta = true
		}
	}
	assert.True(t, sawDelta, "fallback partial_json delta must be emitted")
}

func TestProcessor_EOSWithoutDone_FlushesBuffer(t *testing.T) {
	p := New(buffering())
	body := frame("content_block_start", blockStart(0, "text")) +
		frame("content_block_delta", textDelta("Hi"))
	// No content_block_stop, no [DONE] — just EOS.

	out := processAll(t, p, []string{body})
	payloads := dataPayloads(t, out)

	var sawFlushedText bool
	for _, pld := range payloads {
		if gjson.Get(pld, "delta.text").String() == "Hi" {
			sawFlushedText = true
		}
	}
	assert.True(t, sawFlushedText, "buffered text must flush on EOS")
}

func TestProcessor_MultiBlock_NoDemaskerBleed(t *testing.T) {
	// Block 0 (text) uses one demasker instance; block 1 (tool_use) gets a
	// fresh one. The factory below returns distinct mocks so we can confirm
	// independence.
	var made []*mockDemasker
	factory := func() common.Demasker {
		buf := ""
		m := newDemasker(func(_ context.Context, chunk string, flush bool) (string, error) {
			buf += chunk
			if flush {
				out := buf
				buf = ""
				return out, nil
			}
			return "", nil
		})
		made = append(made, m)
		return m
	}
	p := New(factory)

	body := frame("content_block_start", blockStart(0, "text")) +
		frame("content_block_delta", textDelta("first")) +
		frame("content_block_stop", blockStop(0)) +
		frame("content_block_start", blockStart(1, "tool_use")) +
		frame("content_block_delta", inputJSONDelta(1, `{"x":1}`)) +
		frame("content_block_stop", blockStop(1)) +
		doneFrame()

	out := processAll(t, p, []string{body})
	payloads := dataPayloads(t, out)

	assert.Len(t, made, 2, "two blocks → two demasker instances")
	assert.Equal(t, []string{"first"}, made[0].chunks)
	assert.Equal(t, []string{`{"x":1}`}, made[1].chunks)

	var text, partial string
	for _, pld := range payloads {
		if gjson.Get(pld, "delta.type").String() == "text_delta" {
			text = gjson.Get(pld, "delta.text").String()
		}
		if gjson.Get(pld, "delta.type").String() == "input_json_delta" {
			partial = gjson.Get(pld, "delta.partial_json").String()
		}
	}
	assert.Equal(t, "first", text)
	assert.Equal(t, `{"x":1}`, partial)
}

// TestProcessor_FixtureReplay_Passthrough feeds a captured real Anthropic
// streaming response through the processor in randomised chunk sizes with
// a passthrough demasker. The output must contain exactly the same event
// names and the same combined text across all content_block_delta frames
// as the input — i.e. the wire format is preserved end-to-end.
func TestProcessor_FixtureReplay_Passthrough(t *testing.T) {
	fixturePath := filepath.Join("..", "..", "llm_responses", "messages_stream", "anthropic__claude-sonnet-4-5.json")
	raw, err := os.ReadFile(fixturePath)
	if err != nil {
		t.Skipf("fixture not available: %v", err)
	}

	p := New(passthrough())

	// Feed in 256-byte chunks to exercise tail-buffering across frames.
	const chunkSize = 256
	var chunks []string
	for i := 0; i < len(raw); i += chunkSize {
		end := i + chunkSize
		if end > len(raw) {
			end = len(raw)
		}
		chunks = append(chunks, string(raw[i:end]))
	}

	out := processAll(t, p, chunks)

	gotEvents := eventNames(t, out)
	wantEvents := eventNames(t, raw)
	assert.Equal(t, wantEvents, gotEvents, "event sequence must be preserved")

	combinedDelta := func(b []byte) string {
		var s string
		for _, pld := range dataPayloads(t, b) {
			if gjson.Get(pld, "type").String() == "content_block_delta" &&
				gjson.Get(pld, "delta.type").String() == "text_delta" {
				s += gjson.Get(pld, "delta.text").String()
			}
		}
		return s
	}
	assert.Equal(t, combinedDelta(raw), combinedDelta(out), "concatenated text_delta payloads must match")
}

// TestProcessor_FixtureReplay_Opus46 exercises the Opus 4.6 streaming
// capture under three demasker strategies and several chunk sizes. The
// fixture starts with a `: PROCESSING` SSE comment, then message_start →
// content_block_start(text) → ~40 content_block_delta frames →
// content_block_stop → message_delta(usage) → message_stop → [DONE], which
// is the canonical happy path the production stream follows. Each
// sub-test asserts the property the corresponding demasker should
// guarantee.
func TestProcessor_FixtureReplay_Opus46(t *testing.T) {
	fixturePath := filepath.Join("..", "..", "llm_responses", "messages_stream", "anthropic__claude-opus-4-6.json")
	raw, err := os.ReadFile(fixturePath)
	if err != nil {
		t.Skipf("fixture not available: %v", err)
	}

	wantEvents := eventNames(t, raw)
	wantCombined := func() string {
		var s string
		for _, pld := range dataPayloads(t, raw) {
			if gjson.Get(pld, "delta.type").String() == "text_delta" {
				s += gjson.Get(pld, "delta.text").String()
			}
		}
		return s
	}()

	chunkInto := func(b []byte, size int) []string {
		var chunks []string
		for i := 0; i < len(b); i += size {
			end := i + size
			if end > len(b) {
				end = len(b)
			}
			chunks = append(chunks, string(b[i:end]))
		}
		return chunks
	}

	t.Run("passthrough preserves event sequence across many chunk sizes", func(t *testing.T) {
		// 1, 17, and 4096 stress different tail-buffering regimes: byte-at-
		// a-time, mid-token boundaries, and "single chunk" delivery.
		for _, size := range []int{1, 17, 256, 4096} {
			size := size
			t.Run(fmt.Sprintf("chunkSize=%d", size), func(t *testing.T) {
				p := New(passthrough())
				out := processAll(t, p, chunkInto(raw, size))

				assert.Equal(t, wantEvents, eventNames(t, out), "event sequence must be preserved at chunkSize=%d", size)

				var combined string
				for _, pld := range dataPayloads(t, out) {
					if gjson.Get(pld, "delta.type").String() == "text_delta" {
						combined += gjson.Get(pld, "delta.text").String()
					}
				}
				assert.Equal(t, wantCombined, combined, "concatenated text must round-trip at chunkSize=%d", size)

				// The leading ": PROCESSING" SSE comment line must survive.
				assert.Contains(t, string(out), ": PROCESSING")
				// And the terminal [DONE] must be present exactly once.
				assert.Equal(t, 1, strings.Count(string(out), "data: [DONE]"))
			})
		}
	})

	t.Run("buffering demasker aggregates the whole block into one synthetic delta", func(t *testing.T) {
		p := New(buffering())
		out := processAll(t, p, []string{string(raw)})

		// With a buffering demasker, every individual text_delta arrives
		// returning "" and is dropped; on content_block_stop we synthesize
		// exactly one content_block_delta carrying the full aggregated text.
		var textDeltaCount int
		var aggregated string
		for _, pld := range dataPayloads(t, out) {
			if gjson.Get(pld, "delta.type").String() == "text_delta" {
				textDeltaCount++
				aggregated += gjson.Get(pld, "delta.text").String()
			}
		}
		assert.Equal(t, 1, textDeltaCount, "buffering demasker must collapse all text_deltas into one synthetic frame")
		assert.Equal(t, wantCombined, aggregated, "aggregated text must equal the upstream's full content")

		// Framing invariants: surrounding events still arrive in order.
		got := eventNames(t, out)
		// We expect every non-content_block_delta event to be untouched in
		// order, and exactly one content_block_delta in between start/stop.
		var startIdx, stopIdx, deltaCount int
		for i, ev := range got {
			switch ev {
			case "content_block_start":
				startIdx = i
			case "content_block_stop":
				stopIdx = i
			case "content_block_delta":
				deltaCount++
			}
		}
		assert.Equal(t, 1, deltaCount, "exactly one delta event between start and stop")
		assert.Less(t, startIdx, stopIdx, "block_start precedes block_stop")
		assert.Contains(t, got, "message_delta")
		assert.Contains(t, got, "message_stop")
		assert.Contains(t, got, "data") // the [DONE] sentinel's event name
	})

	t.Run("inline demasker substitutes a substring that crosses delta boundaries", func(t *testing.T) {
		// The fixture's text contains the Russian word "Сказка" in the very
		// first delta. Replace it with "STORY" to confirm we can patch real
		// upstream content end-to-end. Use a buffering demasker so the
		// substitution applies to the *combined* text — the equivalent of
		// what a real demasker would do when a placeholder spans multiple
		// upstream chunks.
		const needle = "Сказка"
		const replacement = "STORY"
		require.Contains(t, wantCombined, needle, "fixture must contain the needle we're about to replace")

		p := New(bufferingReplacing(needle, replacement))
		out := processAll(t, p, []string{string(raw)})

		var got string
		for _, pld := range dataPayloads(t, out) {
			if gjson.Get(pld, "delta.type").String() == "text_delta" {
				got += gjson.Get(pld, "delta.text").String()
			}
		}
		assert.Equal(t, strings.ReplaceAll(wantCombined, needle, replacement), got)
		assert.NotContains(t, got, needle, "placeholder should not survive demasking")
	})
}

// fakeDemaskRegistry lets a real demask.Factory run with only its
// exact-replacer path (no placeholder-regex rules).
type fakeDemaskRegistry struct{}

func (fakeDemaskRegistry) GetMaxPlaceholderLenByRuleIDs(...string) int { return 32 }

// A restored original containing JSON metacharacters must be JSON-escaped
// when inserted into input_json_delta fragments: the client accumulates the
// fragments as JSON, and a verbatim quote/backslash would corrupt the tool
// input unrecoverably (Anthropic never re-sends the full input later).
// Regression: stream/non-stream divergence for tool_use input.
func TestInputJSONDeltaEscapesRestoredOriginals(t *testing.T) {
	provider := demask.NewProvider(fakeDemaskRegistry{}, nil)
	factory := provider.NewFactory(models.MaskingState{Replacements: []models.Replacement{
		{RuleID: "secret", Original: `say "hi" C:\tmp`, Placeholder: "<SECRET_1>"},
	}})
	p := New(
		func() common.Demasker { return factory.Demasker() },
		WithJSONDemaskerFactory(func() common.Demasker { return factory.JSONDemasker() }),
	)

	chunks := []string{
		frame("content_block_start", `{"type":"content_block_start","index":0,"content_block":{"type":"tool_use","id":"t1","name":"f"}}`),
		frame("content_block_delta", `{"type":"content_block_delta","index":0,"delta":{"type":"input_json_delta","partial_json":"{\"note\":\""}}`),
		frame("content_block_delta", `{"type":"content_block_delta","index":0,"delta":{"type":"input_json_delta","partial_json":"<SECRET_1>\"}"}}`),
		frame("content_block_stop", `{"type":"content_block_stop","index":0}`),
	}
	out := processAll(t, p, chunks)

	// Reassemble the partial_json fragments exactly as a client would.
	var assembled strings.Builder
	frames, _ := common.SplitFrames(out)
	for _, f := range frames {
		pf := common.ClassifyFrame(f)
		if gjson.GetBytes(pf.Data, "delta.type").String() == "input_json_delta" {
			assembled.WriteString(gjson.GetBytes(pf.Data, "delta.partial_json").String())
		}
	}
	input := assembled.String()
	require.True(t, json.Valid([]byte(input)), "accumulated tool input must be valid JSON: %q", input)
	assert.Equal(t, `say "hi" C:\tmp`, gjson.Get(input, "note").String())
	assert.NotContains(t, input, "<SECRET_1>")
}
