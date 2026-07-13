package responses

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"

	"github.com/cloud-ru-tech/guardrails-llm-filter/internal/sseproc/common"
)

// mockDemasker mirrors the test double used by the messages processor tests.
type mockDemasker struct {
	handler func(ctx context.Context, chunk string, flush bool) (string, error)
}

func (m *mockDemasker) DemaskChunk(ctx context.Context, chunk string, flush bool) (string, error) {
	if m.handler != nil {
		return m.handler(ctx, chunk, flush)
	}
	return chunk, nil
}

func passthrough() common.DemaskerFactoryFn {
	return func() common.Demasker { return &mockDemasker{} }
}

// replacing replaces `from` with `to` in every chunk immediately.
func replacing(from, to string) common.DemaskerFactoryFn {
	return func() common.Demasker {
		return &mockDemasker{handler: func(_ context.Context, chunk string, _ bool) (string, error) {
			return strings.ReplaceAll(chunk, from, to), nil
		}}
	}
}

// bufferingReplacing buffers all chunks and emits the replaced buffer only
// on flush — the adversarial case of a placeholder split across deltas. It
// demasks the fixed test placeholder <EMAIL_1> back to a sample original.
func bufferingReplacing() common.DemaskerFactoryFn {
	const (
		from = "<EMAIL_1>"
		to   = "user@example.com"
	)
	return func() common.Demasker {
		buf := ""
		return &mockDemasker{handler: func(_ context.Context, chunk string, flush bool) (string, error) {
			buf += chunk
			if flush {
				out := strings.ReplaceAll(buf, from, to)
				buf = ""
				return out, nil
			}
			return "", nil
		}}
	}
}

// jsonReplacing mimics the production JSON-escaping demasker: it replaces the
// placeholder with the JSON-escaped form of the original (no surrounding
// quotes), so a value containing a quote/backslash stays valid inside a JSON
// string context. Used to exercise the args-delta JSON factory path.
func jsonReplacing(from, orig string) common.DemaskerFactoryFn {
	esc, _ := json.Marshal(orig)
	escaped := string(esc[1 : len(esc)-1]) // strip the surrounding quotes
	return func() common.Demasker {
		return &mockDemasker{handler: func(_ context.Context, chunk string, _ bool) (string, error) {
			return strings.ReplaceAll(chunk, from, escaped), nil
		}}
	}
}

// erroring always fails, returning its un-emitted content (the chunk) with the
// error, per the Demasker contract: callers emit that as a lossless fallback.
func erroring() common.DemaskerFactoryFn {
	return func() common.Demasker {
		return &mockDemasker{handler: func(_ context.Context, chunk string, _ bool) (string, error) {
			return chunk, errors.New("demasker boom")
		}}
	}
}

// erroringAfter demasks (length-changingly) for the first n successful chunks,
// then errors on every later chunk, returning its buffered-and-current
// un-emitted content per the Demasker contract. Exercises the fallback after a
// length-changing emit — the scenario the old masked-accumulator trim corrupted.
func erroringAfter(n int, from, to string) common.DemaskerFactoryFn {
	return func() common.Demasker {
		calls := 0
		return &mockDemasker{handler: func(_ context.Context, chunk string, _ bool) (string, error) {
			calls++
			if calls > n {
				return chunk, errors.New("demasker boom")
			}
			return strings.ReplaceAll(chunk, from, to), nil
		}}
	}
}

// --- frame helpers ---

func frame(eventName, dataJSON string) string {
	return "event: " + eventName + "\ndata: " + dataJSON + "\n\n"
}

func textDelta(outIdx int, delta string) string {
	return frame("response.output_text.delta",
		fmt.Sprintf(`{"type":"response.output_text.delta","output_index":%d,"content_index":0,"delta":%q}`,
			outIdx, delta))
}

func textDone(outIdx int, text string) string {
	return frame("response.output_text.done",
		fmt.Sprintf(`{"type":"response.output_text.done","output_index":%d,"content_index":0,"text":%q}`,
			outIdx, text))
}

func argsDelta(outIdx int, delta string) string {
	return frame("response.function_call_arguments.delta",
		fmt.Sprintf(`{"type":"response.function_call_arguments.delta","output_index":%d,"delta":%q}`,
			outIdx, delta))
}

func argsDone(outIdx int, args string) string {
	return frame("response.function_call_arguments.done",
		fmt.Sprintf(`{"type":"response.function_call_arguments.done","output_index":%d,"arguments":%q}`,
			outIdx, args))
}

func createdFrame() string {
	return frame("response.created", `{"type":"response.created","response":{"id":"resp_1","status":"in_progress"}}`)
}

func itemDone(outIdx int, text string) string {
	return frame("response.output_item.done",
		fmt.Sprintf(`{"type":"response.output_item.done","output_index":%d,"item":{"type":"message","content":[{"type":"output_text","text":%q}]}}`,
			outIdx, text))
}

func completedFrame(text string) string {
	return frame("response.completed",
		fmt.Sprintf(`{"type":"response.completed","response":{"id":"resp_1","status":"completed","output":[{"type":"message","content":[{"type":"output_text","text":%q}]}]}}`,
			text))
}

func process(t *testing.T, p *Processor, chunks []string, eosOnLast bool) string {
	t.Helper()
	var out strings.Builder
	for i, c := range chunks {
		eos := eosOnLast && i == len(chunks)-1
		res, err := p.ProcessChunk(context.Background(), []byte(c), eos)
		require.NoError(t, err)
		out.Write(res)
	}
	return out.String()
}

// --- tests ---

func TestPassthroughEvents(t *testing.T) {
	t.Parallel()
	p := New(passthrough())

	in := createdFrame() +
		frame("response.output_item.added", `{"type":"response.output_item.added","output_index":0,"item":{"type":"message"}}`) +
		frame("response.content_part.added", `{"type":"response.content_part.added","output_index":0,"content_index":0}`) +
		": comment\n\n" +
		frame("response.unknown.future", `{"type":"response.unknown.future"}`)

	out := process(t, p, []string{in}, false)
	assert.Equal(t, in, out, "non-text events must pass through byte-for-byte")
}

func TestTextDeltaDemasked(t *testing.T) {
	t.Parallel()
	p := New(replacing("<EMAIL_1>", "user@example.com"))

	out := process(t, p, []string{
		textDelta(0, "write to <EMAIL_1> now") + textDone(0, "write to <EMAIL_1> now"),
	}, false)

	assert.Contains(t, out, `"delta":"write to user@example.com now"`)
	assert.Contains(t, out, `"text":"write to user@example.com now"`)
	assert.NotContains(t, out, "<EMAIL_1>")
}

func TestPlaceholderSplitAcrossDeltas(t *testing.T) {
	t.Parallel()
	p := New(bufferingReplacing())

	// The placeholder is split across two delta frames; the buffering
	// demasker returns "" for both, so both frames are dropped, and the
	// text surfaces as a synthetic delta on the flush at output_text.done.
	out := process(t, p, []string{
		textDelta(0, "mail: <EMA"),
		textDelta(0, "IL_1> ok"),
		textDone(0, "mail: <EMAIL_1> ok"),
	}, false)

	assert.NotContains(t, out, "<EMAIL_1>")
	assert.NotContains(t, out, "<EMA")
	// Synthetic delta carries the merged demasked text before the done frame.
	deltaPos := strings.Index(out, `"delta":"mail: user@example.com ok"`)
	donePos := strings.Index(out, `"text":"mail: user@example.com ok"`)
	require.GreaterOrEqual(t, deltaPos, 0)
	require.GreaterOrEqual(t, donePos, 0)
	assert.Less(t, deltaPos, donePos, "flushed delta must precede the done frame")
}

func TestFrameSplitAcrossEnvoyChunks(t *testing.T) {
	t.Parallel()
	p := New(replacing("<EMAIL_1>", "user@example.com"))

	full := textDelta(0, "hi <EMAIL_1>")
	out := process(t, p, []string{full[:25], full[25:]}, false)

	assert.Contains(t, out, "user@example.com")
	assert.NotContains(t, out, "<EMAIL_1>")
}

func TestCompletedEmbeddedResponseDemasked(t *testing.T) {
	t.Parallel()
	p := New(replacing("<EMAIL_1>", "user@example.com"))

	out := process(t, p, []string{
		textDelta(0, "hi <EMAIL_1>") + completedFrame("hi <EMAIL_1>"),
	}, false)

	assert.NotContains(t, out, "<EMAIL_1>",
		"the full response object in response.completed must be demasked")
	// The completed frame's embedded output text is patched.
	var completedData string
	for _, fr := range strings.Split(out, "\n\n") {
		if strings.Contains(fr, "response.completed") {
			completedData = fr
		}
	}
	require.NotEmpty(t, completedData)
	dataLine := completedData[strings.Index(completedData, "data: ")+len("data: "):]
	assert.Equal(t, "hi user@example.com",
		gjson.Get(dataLine, "response.output.0.content.0.text").String())
}

func TestOutputItemDoneDemasked(t *testing.T) {
	t.Parallel()
	p := New(replacing("<EMAIL_1>", "user@example.com"))

	out := process(t, p, []string{itemDone(0, "reach me at <EMAIL_1>")}, false)
	assert.Contains(t, out, "reach me at user@example.com")
	assert.NotContains(t, out, "<EMAIL_1>")
}

func TestFunctionCallArgumentsFlushOnJsonClose(t *testing.T) {
	t.Parallel()
	p := New(bufferingReplacing())

	// Fragments split at JSON token boundaries: the shared depth tracker is
	// stateless per fragment (same contract as the messages processor).
	out := process(t, p, []string{
		argsDelta(1, `{"to":`),
		argsDelta(1, `"<EMAIL_1>"}`),
	}, false)

	// The closing brace triggers the flush: the merged demasked arguments
	// come out as a synthetic delta without waiting for the done event.
	assert.Contains(t, out, `user@example.com`)
	assert.NotContains(t, out, "<EMAIL_1>")
}

func TestArgsDoneStructuralFallbackKeepsValidJSON(t *testing.T) {
	t.Parallel()
	// A restored original containing a JSON metacharacter (") makes a naive
	// substitution produce invalid JSON. The shared structural fallback
	// (common.DemaskJSONArguments) re-escapes it so the arguments stay valid
	// JSON and no placeholder leaks — identical to the full-body path.
	p := New(replacing("<SECRET_1>", `he said "hi"`))

	in := argsDone(0, `{"note":"<SECRET_1>"}`)
	out := process(t, p, []string{in}, false)

	frames, _ := common.SplitFrames([]byte(out))
	require.Len(t, frames, 1)
	pf := common.ClassifyFrame(frames[0])
	args := gjson.GetBytes(pf.Data, "arguments").String()
	require.True(t, json.Valid([]byte(args)), "arguments must stay valid JSON: %q", args)
	assert.NotContains(t, string(pf.Data), "<SECRET_1>", "placeholder must not leak")
	assert.Equal(t, `he said "hi"`, gjson.GetBytes([]byte(args), "note").String())
}

// Regression: streaming function_call_arguments.delta fragments are JSON the
// client concatenates into the tool-call arguments. A restored original with a
// quote must be JSON-escaped in the deltas (matching chatcompletions/messages),
// or the accumulated arguments become invalid JSON. Before the fix the delta
// path used the plain demasker and this reassembly was invalid JSON.
func TestArgsDeltaUsesJSONEscapingDemasker(t *testing.T) {
	t.Parallel()
	p := New(
		replacing("<SECRET_1>", `he said "hi"`), // plain (used for output_text)
		WithJSONDemaskerFactory(jsonReplacing("<SECRET_1>", `he said "hi"`)),
	)

	out := process(t, p, []string{
		argsDelta(0, `{"note":`),
		argsDelta(0, `"<SECRET_1>"}`),
	}, false)

	// Concatenate the delta fragments exactly as a streaming client would.
	frames, _ := common.SplitFrames([]byte(out))
	var args strings.Builder
	for _, f := range frames {
		pf := common.ClassifyFrame(f)
		if gjson.GetBytes(pf.Data, "type").String() == "response.function_call_arguments.delta" {
			args.WriteString(gjson.GetBytes(pf.Data, "delta").String())
		}
	}
	require.True(t, json.Valid([]byte(args.String())),
		"reassembled arguments must be valid JSON: %q", args.String())
	assert.Equal(t, `he said "hi"`, gjson.GetBytes([]byte(args.String()), "note").String())
	assert.NotContains(t, args.String(), "<SECRET_1>", "placeholder must not leak")
}

// Regression: the real OpenAI Responses API emits response.content_part.done
// carrying the full accumulated part.text. Without a fresh one-shot demask its
// placeholders leak to the client (found via real-model e2e; the mock upstream
// never emitted this event).
func TestContentPartDoneDemasked(t *testing.T) {
	t.Parallel()
	p := New(replacing("<EMAIL_1>", "user@example.com"))
	in := frame("response.content_part.done",
		`{"type":"response.content_part.done","output_index":0,"content_index":0,"part":{"type":"output_text","text":"reach <EMAIL_1> now","annotations":[]}}`)
	out := process(t, p, []string{in}, false)

	frames, _ := common.SplitFrames([]byte(out))
	require.Len(t, frames, 1)
	pf := common.ClassifyFrame(frames[0])
	assert.Equal(t, "reach user@example.com now", gjson.GetBytes(pf.Data, "part.text").String())
	assert.NotContains(t, string(pf.Data), "<EMAIL_1>", "placeholder must not leak in content_part.done")
}

// Reasoning models (e.g. served via vLLM) echo prompt text into their
// chain-of-thought, streamed as response.reasoning_text.delta /
// reasoning_text.done / reasoning_part.done. All must be demasked or the client
// sees placeholders in the reasoning trace (found via real-model e2e).
func TestReasoningTextDeltaDemasked(t *testing.T) {
	t.Parallel()
	p := New(replacing("<EMAIL_1>", "user@example.com"))
	in := frame("response.reasoning_text.delta",
		`{"type":"response.reasoning_text.delta","output_index":0,"content_index":0,"item_id":"rs_1","delta":"think <EMAIL_1>"}`)
	out := process(t, p, []string{in}, false)
	assert.Contains(t, out, "user@example.com")
	assert.NotContains(t, out, "<EMAIL_1>")
}

func TestReasoningTextDoneDemasked(t *testing.T) {
	t.Parallel()
	p := New(replacing("<EMAIL_1>", "user@example.com"))
	in := frame("response.reasoning_text.done",
		`{"type":"response.reasoning_text.done","output_index":0,"content_index":0,"item_id":"rs_1","text":"final <EMAIL_1>"}`)
	out := process(t, p, []string{in}, false)
	frames, _ := common.SplitFrames([]byte(out))
	require.Len(t, frames, 1)
	pf := common.ClassifyFrame(frames[0])
	assert.Equal(t, "final user@example.com", gjson.GetBytes(pf.Data, "text").String())
	assert.NotContains(t, string(pf.Data), "<EMAIL_1>")
}

func TestReasoningPartDoneDemasked(t *testing.T) {
	t.Parallel()
	p := New(replacing("<EMAIL_1>", "user@example.com"))
	in := frame("response.reasoning_part.done",
		`{"type":"response.reasoning_part.done","output_index":0,"content_index":0,"item_id":"rs_1","part":{"type":"reasoning_text","text":"part <EMAIL_1>"}}`)
	out := process(t, p, []string{in}, false)
	frames, _ := common.SplitFrames([]byte(out))
	require.Len(t, frames, 1)
	pf := common.ClassifyFrame(frames[0])
	assert.Equal(t, "part user@example.com", gjson.GetBytes(pf.Data, "part.text").String())
	assert.NotContains(t, string(pf.Data), "<EMAIL_1>")
}

// Reasoning text buffered across split deltas must flush as a synthetic
// reasoning_text.delta (correct event type) on reasoning_text.done.
func TestReasoningSplitPlaceholderFlushes(t *testing.T) {
	t.Parallel()
	p := New(bufferingReplacing())
	out := process(t, p, []string{
		frame("response.reasoning_text.delta", `{"type":"response.reasoning_text.delta","output_index":0,"content_index":0,"item_id":"rs_1","delta":"<EMAIL"}`),
		frame("response.reasoning_text.delta", `{"type":"response.reasoning_text.delta","output_index":0,"content_index":0,"item_id":"rs_1","delta":"_1>"}`),
		frame("response.reasoning_text.done", `{"type":"response.reasoning_text.done","output_index":0,"content_index":0,"item_id":"rs_1","text":"<EMAIL_1>"}`),
	}, false)
	assert.Contains(t, out, "user@example.com")
	assert.NotContains(t, out, "<EMAIL_1>")
	assert.Contains(t, out, "response.reasoning_text.delta")
}

// A synthetic delta frame (emitted when a split placeholder's tail surfaces
// only on flush) must carry item_id and sequence_number, which strict SDKs
// require. The item_id is taken from the key's real deltas; sequence_number is
// monotonically increasing.
func TestResponses_SyntheticDeltaHasRequiredFields(t *testing.T) {
	t.Parallel()
	p := New(bufferingReplacing()) // buffers, flushes <EMAIL_1> -> user@example.com

	frames := []string{
		// A real stream opens with response.created (sequence_number 0), which
		// advances the outgoing cursor before any output_text.delta arrives.
		frame("response.created",
			`{"type":"response.created","sequence_number":0,"response":{"id":"r1"}}`),
		frame("response.output_text.delta",
			`{"type":"response.output_text.delta","output_index":0,"content_index":0,"item_id":"item_123","sequence_number":5,"delta":"a: <EMA"}`),
		frame("response.output_text.delta",
			`{"type":"response.output_text.delta","output_index":0,"content_index":0,"item_id":"item_123","sequence_number":6,"delta":"IL_1>"}`),
		frame("response.output_text.done",
			`{"type":"response.output_text.done","output_index":0,"content_index":0,"item_id":"item_123","sequence_number":7,"text":"a: <EMAIL_1>"}`),
	}
	out := process(t, p, frames, false)

	// Find the synthetic delta frame carrying the demasked tail.
	outFrames, _ := common.SplitFrames([]byte(out))
	var synth []byte
	for _, f := range outFrames {
		pf := common.ClassifyFrame(f)
		if gjson.GetBytes(pf.Data, "type").String() == "response.output_text.delta" &&
			strings.Contains(gjson.GetBytes(pf.Data, "delta").String(), "user@example.com") {
			synth = pf.Data
		}
	}
	require.NotNil(t, synth, "expected a synthetic output_text.delta with the demasked tail")
	assert.Equal(t, "item_123", gjson.GetBytes(synth, "item_id").String(), "item_id must be carried")
	assert.True(t, gjson.GetBytes(synth, "sequence_number").Exists(), "sequence_number must be present")
	assert.Greater(t, gjson.GetBytes(synth, "sequence_number").Int(), int64(0))
}

func TestArgsDoneDemaskerErrorKeepsMasked(t *testing.T) {
	t.Parallel()
	// When even the structural fallback fails (the demasker errors), the
	// original masked frame is forwarded unchanged (fail-open on content we
	// cannot safely rewrite).
	p := New(erroring())

	in := argsDone(0, `{"to":"<EMAIL_1>"}`)
	out := process(t, p, []string{in}, false)

	assert.Contains(t, out, "<EMAIL_1>")
}

func TestParallelOutputItemsIndependentState(t *testing.T) {
	t.Parallel()
	p := New(bufferingReplacing())

	out := process(t, p, []string{
		textDelta(0, "a: <EMA"),
		textDelta(1, "b: plain"),
		textDelta(0, "IL_1>"),
		textDone(0, "a: <EMAIL_1>"),
		textDone(1, "b: plain"),
	}, false)

	assert.Contains(t, out, "a: user@example.com")
	assert.Contains(t, out, "b: plain")
	assert.NotContains(t, out, "<EMAIL_1>")
}

func TestDemaskerErrorFallsBackToMasked(t *testing.T) {
	t.Parallel()
	p := New(erroring())

	out := process(t, p, []string{textDelta(0, "keep <EMAIL_1> masked")}, false)

	// Content is not lost: the demasker hands back its un-emitted content and
	// the processor emits it (placeholders intact) as a fail-open fallback.
	assert.Contains(t, out, "keep <EMAIL_1> masked")
}

// TestDemaskerErrorAfterLengthChangingEmitIsLossless is the regression for the
// masked-accumulator trim bug: after a successful length-changing emit
// (placeholder shorter than its value), a later demask error must still emit
// the remaining content losslessly — no dropped or duplicated bytes.
func TestDemaskerErrorAfterLengthChangingEmitIsLossless(t *testing.T) {
	t.Parallel()
	// First delta demasks "<E>"(3) -> "aaaaaaaa"(8) successfully; second delta
	// errors and must be emitted verbatim as the fallback.
	p := New(erroringAfter(1, "<E>", "aaaaaaaa"))

	out := process(t, p, []string{
		textDelta(0, "x<E>y"),
		textDelta(0, "TAILDATA"),
	}, false)

	assert.Contains(t, out, "xaaaaaaaay", "first (successful) delta demasked")
	assert.Contains(t, out, "TAILDATA", "second (errored) delta emitted losslessly")
}

func TestEosWithoutCompletedFlushes(t *testing.T) {
	t.Parallel()
	p := New(bufferingReplacing())

	out := process(t, p, []string{textDelta(0, "bye <EMAIL_1>")}, true)

	assert.Contains(t, out, "bye user@example.com", "EOS must flush buffered demaskers")
}

func TestCrlfFrames(t *testing.T) {
	t.Parallel()
	p := New(replacing("<EMAIL_1>", "user@example.com"))

	in := "event: response.output_text.delta\r\n" +
		`data: {"type":"response.output_text.delta","output_index":0,"content_index":0,"delta":"hi <EMAIL_1>"}` +
		"\r\n\r\n"
	out := process(t, p, []string{in}, false)
	assert.Contains(t, out, "user@example.com")
}

func TestDeltaAndDoneConsistency(t *testing.T) {
	t.Parallel()
	p := New(replacing("<EMAIL_1>", "user@example.com"))

	out := process(t, p, []string{
		textDelta(0, "x <EMAIL_1> y"),
		textDone(0, "x <EMAIL_1> y"),
	}, false)

	// The demasked delta stream and the rewritten done text must agree.
	assert.Contains(t, out, `"delta":"x user@example.com y"`)
	assert.Contains(t, out, `"text":"x user@example.com y"`)
}

// The outgoing stream must be strictly monotonic in sequence_number even when
// a synthetic flush delta is inserted mid-stream: the synthetic claims the
// next number and every subsequent real frame is renumbered past it.
// Regression for the synthetic frame taking maxSeq+1 while the triggering
// done frame kept its original (now colliding) number.
func TestResponses_SequenceMonotonicAcrossSyntheticFlush(t *testing.T) {
	t.Parallel()
	p := New(bufferingReplacing()) // buffers, flushes <EMAIL_1> -> user@example.com

	frames := []string{
		frame("response.output_text.delta",
			`{"type":"response.output_text.delta","output_index":0,"content_index":0,"item_id":"i1","sequence_number":5,"delta":"a: <EMA"}`),
		frame("response.output_text.delta",
			`{"type":"response.output_text.delta","output_index":0,"content_index":0,"item_id":"i1","sequence_number":6,"delta":"IL_1>"}`),
		frame("response.output_text.done",
			`{"type":"response.output_text.done","output_index":0,"content_index":0,"item_id":"i1","sequence_number":7,"text":"a: <EMAIL_1>"}`),
		frame("response.content_part.done",
			`{"type":"response.content_part.done","output_index":0,"content_index":0,"item_id":"i1","sequence_number":8,"part":{"type":"output_text","text":"a: user@example.com"}}`),
	}
	out := process(t, p, frames, false)

	outFrames, _ := common.SplitFrames([]byte(out))
	var seqs []int64
	for _, f := range outFrames {
		pf := common.ClassifyFrame(f)
		if sn := gjson.GetBytes(pf.Data, "sequence_number"); sn.Exists() {
			seqs = append(seqs, sn.Int())
		}
	}
	require.GreaterOrEqual(t, len(seqs), 3, "expected sequenced frames in output: %s", out)
	for i := 1; i < len(seqs); i++ {
		assert.Greater(t, seqs[i], seqs[i-1],
			"sequence_number must strictly increase, got %v", seqs)
	}
}

// The real /v1/responses stream opens with response.created carrying
// sequence_number 0. Until the first synthetic insertion, upstream numbering
// must be preserved verbatim — starting the outgoing cursor at 0 (instead of
// -1) would collide with this first frame and shift the whole stream by +1.
// Regression for the off-by-one in nextOutSeq's initial state.
func TestResponses_FirstFrameSequenceZeroPreservedVerbatim(t *testing.T) {
	t.Parallel()
	p := New(passthrough()) // no insertions, so nothing should be renumbered

	frames := []string{
		frame("response.created",
			`{"type":"response.created","sequence_number":0,"response":{"id":"resp_1"}}`),
		frame("response.in_progress",
			`{"type":"response.in_progress","sequence_number":1,"response":{"id":"resp_1"}}`),
		frame("response.output_item.added",
			`{"type":"response.output_item.added","sequence_number":2,"output_index":0,"item":{"type":"message"}}`),
	}
	out := process(t, p, frames, false)

	// Every input frame must be forwarded byte-for-byte (no renumbering, no
	// re-serialization) when no synthetic frame was inserted.
	for _, f := range frames {
		assert.Contains(t, out, f, "frame must pass through verbatim")
	}

	outFrames, _ := common.SplitFrames([]byte(out))
	var seqs []int64
	for _, f := range outFrames {
		pf := common.ClassifyFrame(f)
		if sn := gjson.GetBytes(pf.Data, "sequence_number"); sn.Exists() {
			seqs = append(seqs, sn.Int())
		}
	}
	assert.Equal(t, []int64{0, 1, 2}, seqs, "upstream numbering must be preserved verbatim")
}
