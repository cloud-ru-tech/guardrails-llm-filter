// Command e2e-upstream is the test "LLM" behind Envoy for e2e verification of
// extproc-guardrails. It has three jobs:
//
//  1. CAPTURE: every incoming request body (what the guardrails service
//     forwarded upstream, i.e. POST-masking) is appended as one JSON line to
//     $CAPTURE_FILE, keyed by the X-Test-Id header. This is the oracle for
//     masking verification.
//  2. ECHO (default): reflect the extracted user text back verbatim as the
//     assistant output, in the correct wire format for the path
//     (/v1/chat/completions, /v1/responses, /v1/messages), streaming or not.
//     Because the reflected text is the MASKED text (placeholders), the
//     guardrails demask path should restore it to the ORIGINAL — so the client
//     must receive byte-exact originals. This is the demask round-trip oracle.
//  3. PROXY (X-Upstream-Mode: proxy): reverse-proxy to the real model at
//     $REAL_MODEL_URL and stream the response back, for integration-reality
//     checks with a live LLM.
//
// Streaming echo fragments the reply into small rune-safe chunks so that
// placeholders like <EMAIL_1> are split across SSE frames — stressing the
// demasker's cross-chunk pending-tail buffer.
package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

var (
	captureFile  = env("CAPTURE_FILE", "./capture/requests.jsonl")
	realModelURL = env("REAL_MODEL_URL", "http://localhost:8881")
	listenAddr   = env("LISTEN_ADDR", ":8890")

	captureMu sync.Mutex
	seq       atomic.Int64
)

func env(k, d string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return d
}

// jmarshal marshals v to JSON WITHOUT HTML-escaping (< > & stay literal), which
// is what real OpenAI/Anthropic APIs emit — Go's default json.Marshal would
// turn placeholders like <EMAIL_1> into \u003cEMAIL_1\u003e and defeat demask.
func jmarshal(v any) []byte {
	var b bytes.Buffer
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)
	_ = enc.Encode(v)
	return bytes.TrimRight(b.Bytes(), "\n")
}

func jencode(w http.ResponseWriter, v any) {
	_, _ = w.Write(jmarshal(v))
}

type captureRecord struct {
	Seq       int64             `json:"seq"`
	TestID    string            `json:"test_id"`
	RequestID string            `json:"request_id"`
	Path      string            `json:"path"`
	Mode      string            `json:"mode"`
	Headers   map[string]string `json:"headers"`
	Body      json.RawMessage   `json:"body"`
	BodyRaw   string            `json:"body_raw,omitempty"` // when body is not valid JSON
}

func capture(r *http.Request, body []byte, mode string) {
	rec := captureRecord{
		Seq:       seq.Add(1),
		TestID:    r.Header.Get("X-Test-Id"),
		RequestID: r.Header.Get("X-Request-Id"),
		Path:      r.URL.Path,
		Mode:      mode,
		Headers: map[string]string{
			"content-type":              r.Header.Get("Content-Type"),
			"x-guardrails-data-types":   r.Header.Get("X-Guardrails-Data-Types"),
			"x-guardrails-triggered":    r.Header.Get("X-Guardrails-Data-Types-Triggered"),
			"x-guardrails-rules":        r.Header.Get("X-Guardrails-Triggered-Rules"),
		},
	}
	if json.Valid(body) {
		rec.Body = json.RawMessage(body)
	} else {
		rec.BodyRaw = string(body)
	}
	line := jmarshal(rec)

	captureMu.Lock()
	defer captureMu.Unlock()
	f, err := os.OpenFile(captureFile, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		log.Printf("capture open error: %v", err)
		return
	}
	defer f.Close()
	_, _ = f.Write(append(line, '\n'))
}

func main() {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /v1/chat/completions", handleChat)
	mux.HandleFunc("POST /v1/responses", handleResponses)
	mux.HandleFunc("POST /v1/messages", handleMessages)
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte("ok")) })

	log.Printf("e2e-upstream listening on %s (capture=%s real=%s)", listenAddr, captureFile, realModelURL)
	log.Fatal(http.ListenAndServe(listenAddr, mux))
}

// ---- chunking for streaming echo -------------------------------------------

// chunkRunes splits s into pieces of at most n runes each (rune-safe, so we
// never emit invalid UTF-8 inside a JSON string). Small n fragments
// placeholders across frames.
func chunkRunes(s string, n int) []string {
	if n <= 0 {
		n = 3
	}
	runes := []rune(s)
	var out []string
	for i := 0; i < len(runes); i += n {
		end := i + n
		if end > len(runes) {
			end = len(runes)
		}
		out = append(out, string(runes[i:end]))
	}
	if len(out) == 0 {
		out = []string{""}
	}
	return out
}

// sseContentType returns the Content-Type an SSE handler should send. When
// the client sets X-Mislabel-Content-Type: true, it deliberately returns a
// non-SSE type (as a real upstream that mislabels its stream would) to
// exercise the gateway's request-body-driven stream detection fallback.
func sseContentType(r *http.Request) string {
	if r.Header.Get("X-Mislabel-Content-Type") == "true" {
		return "text/plain"
	}
	return "text/event-stream"
}

func chunkSize(r *http.Request) int {
	if v := r.Header.Get("X-Chunk-Runes"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			return n
		}
	}
	if v := r.URL.Query().Get("chunk"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			return n
		}
	}
	return 3
}

// ---- proxy mode ------------------------------------------------------------

func maybeProxy(w http.ResponseWriter, r *http.Request, body []byte) bool {
	if r.Header.Get("X-Upstream-Mode") != "proxy" {
		return false
	}
	req, err := http.NewRequestWithContext(r.Context(), http.MethodPost, realModelURL+r.URL.Path, strings.NewReader(string(body)))
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return true
	}
	req.Header.Set("Content-Type", "application/json")
	if accept := r.Header.Get("Accept"); accept != "" {
		req.Header.Set("Accept", accept)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return true
	}
	defer resp.Body.Close()
	for k, vs := range resp.Header {
		for _, v := range vs {
			w.Header().Add(k, v)
		}
	}
	w.WriteHeader(resp.StatusCode)
	flusher, _ := w.(http.Flusher)
	buf := make([]byte, 4096)
	for {
		n, rerr := resp.Body.Read(buf)
		if n > 0 {
			_, _ = w.Write(buf[:n])
			if flusher != nil {
				flusher.Flush()
			}
		}
		if rerr != nil {
			break
		}
	}
	return true
}

// ---- /v1/chat/completions --------------------------------------------------

type chatRequest struct {
	Model    string `json:"model"`
	Stream   bool   `json:"stream"`
	Messages []struct {
		Role    string          `json:"role"`
		Content json.RawMessage `json:"content"`
	} `json:"messages"`
}

func lastUserContent(req chatRequest) string {
	for i := len(req.Messages) - 1; i >= 0; i-- {
		if req.Messages[i].Role != "user" {
			continue
		}
		return contentText(req.Messages[i].Content)
	}
	return ""
}

// contentText extracts text from a content field that is either a JSON string
// or an array of {type:"text"|"input_text",text:...} parts.
func contentText(raw json.RawMessage) string {
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		return s
	}
	var parts []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	if err := json.Unmarshal(raw, &parts); err == nil {
		var b strings.Builder
		for _, p := range parts {
			if p.Text != "" {
				b.WriteString(p.Text)
			}
		}
		return b.String()
	}
	return string(raw)
}

func handleChat(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(r.Body)
	mode := r.Header.Get("X-Upstream-Mode")
	if mode == "" {
		mode = "echo"
	}
	capture(r, body, mode)
	if maybeProxy(w, r, body) {
		return
	}

	var req chatRequest
	if err := json.Unmarshal(body, &req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	reply := lastUserContent(req)

	if r.Header.Get("X-Echo-Mode") == "tool" {
		serveChatTool(w, reply, req.Stream, chunkSize(r))
		return
	}
	if req.Stream {
		serveChatSSE(w, reply, chunkSize(r), sseContentType(r))
		return
	}
	w.Header().Set("Content-Type", "application/json")
	jencode(w, map[string]any{
		"id":      "chatcmpl-e2e",
		"object":  "chat.completion",
		"created": 1700000000,
		"model":   req.Model,
		"choices": []map[string]any{{
			"index":         0,
			"message":       map[string]any{"role": "assistant", "content": reply},
			"finish_reason": "stop",
		}},
	})
}

func serveChatSSE(w http.ResponseWriter, reply string, n int, contentType string) {
	w.Header().Set("Content-Type", contentType)
	w.Header().Set("Cache-Control", "no-cache")
	flusher, _ := w.(http.Flusher)
	for _, piece := range chunkRunes(reply, n) {
		chunk := map[string]any{
			"id":      "chatcmpl-e2e",
			"object":  "chat.completion.chunk",
			"created": 1700000000,
			"choices": []map[string]any{{"index": 0, "delta": map[string]any{"content": piece}}},
		}
		payload := jmarshal(chunk)
		fmt.Fprintf(w, "data: %s\n\n", payload)
		if flusher != nil {
			flusher.Flush()
		}
		time.Sleep(2 * time.Millisecond)
	}
	fmt.Fprint(w, "data: [DONE]\n\n")
	if flusher != nil {
		flusher.Flush()
	}
}

// ---- /v1/responses ---------------------------------------------------------

type responsesRequest struct {
	Model        string          `json:"model"`
	Stream       bool            `json:"stream"`
	Instructions string          `json:"instructions"`
	Input        json.RawMessage `json:"input"`
}

func responsesInputText(req responsesRequest) string {
	var s string
	if err := json.Unmarshal(req.Input, &s); err == nil {
		return s
	}
	var items []struct {
		Content json.RawMessage `json:"content"`
	}
	if err := json.Unmarshal(req.Input, &items); err != nil {
		return string(req.Input)
	}
	for i := len(items) - 1; i >= 0; i-- {
		var contentStr string
		if err := json.Unmarshal(items[i].Content, &contentStr); err == nil {
			return contentStr
		}
		var parts []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		}
		if err := json.Unmarshal(items[i].Content, &parts); err != nil {
			continue
		}
		for j := len(parts) - 1; j >= 0; j-- {
			if parts[j].Type == "input_text" && parts[j].Text != "" {
				return parts[j].Text
			}
		}
	}
	return ""
}

func handleResponses(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(r.Body)
	mode := r.Header.Get("X-Upstream-Mode")
	if mode == "" {
		mode = "echo"
	}
	capture(r, body, mode)
	if maybeProxy(w, r, body) {
		return
	}

	var req responsesRequest
	if err := json.Unmarshal(body, &req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	reply := responsesInputText(req)

	if r.Header.Get("X-Echo-Mode") == "tool" {
		serveResponsesTool(w, reply, req.Stream, chunkSize(r))
		return
	}
	if req.Stream {
		serveResponsesSSE(w, reply, chunkSize(r), sseContentType(r))
		return
	}
	w.Header().Set("Content-Type", "application/json")
	jencode(w, map[string]any{
		"id":         "resp_e2e",
		"object":     "response",
		"created_at": 1700000000,
		"status":     "completed",
		"model":      req.Model,
		"output": []map[string]any{{
			"type":   "message",
			"id":     "msg_e2e",
			"status": "completed",
			"role":   "assistant",
			"content": []map[string]any{{
				"type":        "output_text",
				"text":        reply,
				"annotations": []any{},
			}},
		}},
	})
}

func serveResponsesSSE(w http.ResponseWriter, reply string, n int, contentType string) {
	w.Header().Set("Content-Type", contentType)
	w.Header().Set("Cache-Control", "no-cache")
	flusher, _ := w.(http.Flusher)
	emit := func(event string, payload map[string]any) {
		data := jmarshal(payload)
		fmt.Fprintf(w, "event: %s\ndata: %s\n\n", event, data)
		if flusher != nil {
			flusher.Flush()
		}
	}
	item := map[string]any{"type": "message", "id": "msg_e2e", "status": "in_progress",
		"role": "assistant", "content": []any{}}
	emit("response.created", map[string]any{"type": "response.created",
		"response": map[string]any{"id": "resp_e2e", "status": "in_progress"}})
	emit("response.output_item.added", map[string]any{"type": "response.output_item.added",
		"output_index": 0, "item": item})
	emit("response.content_part.added", map[string]any{"type": "response.content_part.added",
		"output_index": 0, "content_index": 0,
		"part": map[string]any{"type": "output_text", "text": ""}})
	for _, piece := range chunkRunes(reply, n) {
		emit("response.output_text.delta", map[string]any{"type": "response.output_text.delta",
			"output_index": 0, "content_index": 0, "delta": piece})
		time.Sleep(2 * time.Millisecond)
	}
	emit("response.output_text.done", map[string]any{"type": "response.output_text.done",
		"output_index": 0, "content_index": 0, "text": reply})
	// The real Responses API repeats the full text here too — must be demasked.
	emit("response.content_part.done", map[string]any{"type": "response.content_part.done",
		"output_index": 0, "content_index": 0,
		"part": map[string]any{"type": "output_text", "text": reply, "annotations": []any{}}})
	doneItem := map[string]any{"type": "message", "id": "msg_e2e", "status": "completed",
		"role":    "assistant",
		"content": []map[string]any{{"type": "output_text", "text": reply, "annotations": []any{}}}}
	emit("response.output_item.done", map[string]any{"type": "response.output_item.done",
		"output_index": 0, "item": doneItem})
	emit("response.completed", map[string]any{"type": "response.completed",
		"response": map[string]any{"id": "resp_e2e", "status": "completed",
			"output": []any{doneItem}}})
}

// ---- /v1/messages (Anthropic) ----------------------------------------------

type messagesRequest struct {
	Model    string          `json:"model"`
	Stream   bool            `json:"stream"`
	System   json.RawMessage `json:"system"`
	Messages []struct {
		Role    string          `json:"role"`
		Content json.RawMessage `json:"content"`
	} `json:"messages"`
}

// messagesText extracts the text of the last user message (string form, or the
// concatenation of text blocks in the array form).
func messagesText(req messagesRequest) string {
	for i := len(req.Messages) - 1; i >= 0; i-- {
		if req.Messages[i].Role != "user" {
			continue
		}
		var s string
		if err := json.Unmarshal(req.Messages[i].Content, &s); err == nil {
			return s
		}
		var blocks []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		}
		if err := json.Unmarshal(req.Messages[i].Content, &blocks); err == nil {
			var b strings.Builder
			for _, bl := range blocks {
				if bl.Type == "text" && bl.Text != "" {
					b.WriteString(bl.Text)
				}
			}
			return b.String()
		}
		return string(req.Messages[i].Content)
	}
	return ""
}

func handleMessages(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(r.Body)
	mode := r.Header.Get("X-Upstream-Mode")
	if mode == "" {
		mode = "echo"
	}
	capture(r, body, mode)
	if maybeProxy(w, r, body) {
		return
	}

	var req messagesRequest
	if err := json.Unmarshal(body, &req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	reply := messagesText(req)

	if r.Header.Get("X-Echo-Mode") == "tool" {
		serveMessagesTool(w, reply, req.Stream, chunkSize(r))
		return
	}
	if req.Stream {
		serveMessagesSSE(w, reply, chunkSize(r), sseContentType(r))
		return
	}
	w.Header().Set("Content-Type", "application/json")
	jencode(w, map[string]any{
		"id":            "msg_e2e",
		"type":          "message",
		"role":          "assistant",
		"model":         req.Model,
		"stop_reason":   "end_turn",
		"stop_sequence": nil,
		"content":       []map[string]any{{"type": "text", "text": reply}},
		"usage":         map[string]any{"input_tokens": 10, "output_tokens": 10},
	})
}

func serveMessagesSSE(w http.ResponseWriter, reply string, n int, contentType string) {
	w.Header().Set("Content-Type", contentType)
	w.Header().Set("Cache-Control", "no-cache")
	flusher, _ := w.(http.Flusher)
	emit := func(event string, payload map[string]any) {
		data := jmarshal(payload)
		fmt.Fprintf(w, "event: %s\ndata: %s\n\n", event, data)
		if flusher != nil {
			flusher.Flush()
		}
	}
	emit("message_start", map[string]any{"type": "message_start",
		"message": map[string]any{"id": "msg_e2e", "type": "message", "role": "assistant",
			"content": []any{}, "model": "e2e", "stop_reason": nil,
			"usage": map[string]any{"input_tokens": 10, "output_tokens": 0}}})
	emit("content_block_start", map[string]any{"type": "content_block_start",
		"index": 0, "content_block": map[string]any{"type": "text", "text": ""}})
	for _, piece := range chunkRunes(reply, n) {
		emit("content_block_delta", map[string]any{"type": "content_block_delta",
			"index": 0, "delta": map[string]any{"type": "text_delta", "text": piece}})
		time.Sleep(2 * time.Millisecond)
	}
	emit("content_block_stop", map[string]any{"type": "content_block_stop", "index": 0})
	emit("message_delta", map[string]any{"type": "message_delta",
		"delta": map[string]any{"stop_reason": "end_turn", "stop_sequence": nil},
		"usage": map[string]any{"output_tokens": 10}})
	emit("message_stop", map[string]any{"type": "message_stop"})
}

// ---- tool-call echo mode --------------------------------------------------
// Reflects the extracted user text into a tool call's arguments as {"q": <text>}.
// The arguments JSON string is fragmented across deltas (streaming) so that both
// the JSON structure AND embedded placeholders split across frames — exercising
// the JSON-fragment demask path (the function_call_arguments.delta escaping fix).

func toolArgsJSON(text string) string {
	b := jmarshal(map[string]string{"q": text})
	return string(b) // {"q":"...escaped text..."}
}

// serveChatTool emits an OpenAI chat tool_calls response.
func serveChatTool(w http.ResponseWriter, text string, stream bool, n int) {
	args := toolArgsJSON(text)
	if !stream {
		w.Header().Set("Content-Type", "application/json")
		jencode(w, map[string]any{
			"id": "chatcmpl-e2e", "object": "chat.completion", "created": 1700000000, "model": "demo",
			"choices": []map[string]any{{
				"index": 0, "finish_reason": "tool_calls",
				"message": map[string]any{"role": "assistant", "content": nil,
					"tool_calls": []map[string]any{{"id": "call_1", "type": "function", "index": 0,
						"function": map[string]any{"name": "echo", "arguments": args}}}},
			}},
		})
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	flusher, _ := w.(http.Flusher)
	send := func(delta map[string]any) {
		chunk := map[string]any{"id": "chatcmpl-e2e", "object": "chat.completion.chunk",
			"created": 1700000000, "choices": []map[string]any{{"index": 0, "delta": delta}}}
		b := jmarshal(chunk)
		fmt.Fprintf(w, "data: %s\n\n", b)
		if flusher != nil {
			flusher.Flush()
		}
	}
	send(map[string]any{"role": "assistant", "tool_calls": []map[string]any{
		{"index": 0, "id": "call_1", "type": "function",
			"function": map[string]any{"name": "echo", "arguments": ""}}}})
	for _, piece := range chunkRunes(args, n) {
		send(map[string]any{"tool_calls": []map[string]any{
			{"index": 0, "function": map[string]any{"arguments": piece}}}})
		time.Sleep(2 * time.Millisecond)
	}
	fmt.Fprint(w, "data: [DONE]\n\n")
	if flusher != nil {
		flusher.Flush()
	}
}

// serveResponsesTool emits an OpenAI Responses function_call.
func serveResponsesTool(w http.ResponseWriter, text string, stream bool, n int) {
	args := toolArgsJSON(text)
	if !stream {
		w.Header().Set("Content-Type", "application/json")
		jencode(w, map[string]any{
			"id": "resp_e2e", "object": "response", "created_at": 1700000000, "status": "completed", "model": "demo",
			"output": []map[string]any{{"type": "function_call", "id": "fc_1", "call_id": "call_1",
				"name": "echo", "arguments": args, "status": "completed"}},
		})
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	flusher, _ := w.(http.Flusher)
	seq := 0
	emit := func(event string, payload map[string]any) {
		payload["sequence_number"] = seq
		seq++
		data := jmarshal(payload)
		fmt.Fprintf(w, "event: %s\ndata: %s\n\n", event, data)
		if flusher != nil {
			flusher.Flush()
		}
	}
	fcItem := map[string]any{"type": "function_call", "id": "fc_1", "call_id": "call_1",
		"name": "echo", "arguments": "", "status": "in_progress"}
	emit("response.created", map[string]any{"type": "response.created",
		"response": map[string]any{"id": "resp_e2e", "status": "in_progress"}})
	emit("response.output_item.added", map[string]any{"type": "response.output_item.added",
		"output_index": 0, "item": fcItem})
	for _, piece := range chunkRunes(args, n) {
		emit("response.function_call_arguments.delta", map[string]any{
			"type": "response.function_call_arguments.delta",
			"output_index": 0, "item_id": "fc_1", "delta": piece})
		time.Sleep(2 * time.Millisecond)
	}
	emit("response.function_call_arguments.done", map[string]any{
		"type": "response.function_call_arguments.done",
		"output_index": 0, "item_id": "fc_1", "arguments": args})
	doneItem := map[string]any{"type": "function_call", "id": "fc_1", "call_id": "call_1",
		"name": "echo", "arguments": args, "status": "completed"}
	emit("response.output_item.done", map[string]any{"type": "response.output_item.done",
		"output_index": 0, "item": doneItem})
	emit("response.completed", map[string]any{"type": "response.completed",
		"response": map[string]any{"id": "resp_e2e", "status": "completed", "output": []any{doneItem}}})
}

// serveMessagesTool emits an Anthropic tool_use block.
func serveMessagesTool(w http.ResponseWriter, text string, stream bool, n int) {
	args := toolArgsJSON(text)
	if !stream {
		var input any
		_ = json.Unmarshal([]byte(args), &input)
		w.Header().Set("Content-Type", "application/json")
		jencode(w, map[string]any{
			"id": "msg_e2e", "type": "message", "role": "assistant", "model": "demo",
			"stop_reason": "tool_use", "stop_sequence": nil,
			"content": []map[string]any{{"type": "tool_use", "id": "toolu_1", "name": "echo", "input": input}},
			"usage":   map[string]any{"input_tokens": 10, "output_tokens": 10},
		})
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	flusher, _ := w.(http.Flusher)
	emit := func(event string, payload map[string]any) {
		data := jmarshal(payload)
		fmt.Fprintf(w, "event: %s\ndata: %s\n\n", event, data)
		if flusher != nil {
			flusher.Flush()
		}
	}
	emit("message_start", map[string]any{"type": "message_start",
		"message": map[string]any{"id": "msg_e2e", "type": "message", "role": "assistant",
			"content": []any{}, "model": "e2e", "stop_reason": nil,
			"usage": map[string]any{"input_tokens": 10, "output_tokens": 0}}})
	emit("content_block_start", map[string]any{"type": "content_block_start", "index": 0,
		"content_block": map[string]any{"type": "tool_use", "id": "toolu_1", "name": "echo", "input": map[string]any{}}})
	for _, piece := range chunkRunes(args, n) {
		emit("content_block_delta", map[string]any{"type": "content_block_delta", "index": 0,
			"delta": map[string]any{"type": "input_json_delta", "partial_json": piece}})
		time.Sleep(2 * time.Millisecond)
	}
	emit("content_block_stop", map[string]any{"type": "content_block_stop", "index": 0})
	emit("message_delta", map[string]any{"type": "message_delta",
		"delta": map[string]any{"stop_reason": "tool_use", "stop_sequence": nil},
		"usage": map[string]any{"output_tokens": 10}})
	emit("message_stop", map[string]any{"type": "message_stop"})
}
