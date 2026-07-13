// Command mock-llm is a tiny OpenAI-compatible echo server for the
// quickstart demo. It answers /v1/chat/completions and /v1/responses by
// echoing the user input back — so whatever the guardrails processor masked
// is clearly visible in the "LLM" response (and gets demasked on the way
// back to the client). Supports both JSON and SSE (stream: true) modes.
package main

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strings"
	"time"
)

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
		var s string
		if err := json.Unmarshal(req.Messages[i].Content, &s); err == nil {
			return s
		}
		return string(req.Messages[i].Content)
	}
	return ""
}

func main() {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /v1/chat/completions", func(w http.ResponseWriter, r *http.Request) {
		var req chatRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}

		content := lastUserContent(req)
		log.Printf("mock-llm received (as the LLM sees it): %q", content)
		reply := "You said: " + content

		if req.Stream {
			serveSSE(w, reply)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"id":      "chatcmpl-mock",
			"object":  "chat.completion",
			"created": time.Now().Unix(),
			"model":   req.Model,
			"choices": []map[string]any{{
				"index":         0,
				"message":       map[string]any{"role": "assistant", "content": reply},
				"finish_reason": "stop",
			}},
		})
	})

	mux.HandleFunc("POST /v1/responses", func(w http.ResponseWriter, r *http.Request) {
		var req responsesRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}

		content := responsesInputText(req)
		log.Printf("mock-llm /v1/responses received (as the LLM sees it): %q", content)
		reply := "You said: " + content

		if req.Stream {
			serveResponsesSSE(w, reply)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"id":         "resp_mock",
			"object":     "response",
			"created_at": time.Now().Unix(),
			"status":     "completed",
			"model":      req.Model,
			"output": []map[string]any{{
				"type":   "message",
				"id":     "msg_mock",
				"status": "completed",
				"role":   "assistant",
				"content": []map[string]any{{
					"type":        "output_text",
					"text":        reply,
					"annotations": []any{},
				}},
			}},
		})
	})

	log.Println("mock-llm listening on :8000")
	log.Fatal(http.ListenAndServe(":8000", mux))
}

type responsesRequest struct {
	Model        string          `json:"model"`
	Stream       bool            `json:"stream"`
	Instructions string          `json:"instructions"`
	Input        json.RawMessage `json:"input"`
}

// responsesInputText extracts the echoable text from a Responses API input:
// either the plain string form or the last input_text part of the items form.
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

// serveResponsesSSE streams a Responses API event sequence: created →
// output_item.added → content_part.added → output_text.delta* →
// output_text.done → output_item.done → completed.
func serveResponsesSSE(w http.ResponseWriter, reply string) {
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	flusher, _ := w.(http.Flusher)

	emit := func(event string, payload map[string]any) {
		data, _ := json.Marshal(payload)
		_, _ = fmt.Fprintf(w, "event: %s\ndata: %s\n\n", event, data)
		if flusher != nil {
			flusher.Flush()
		}
	}

	item := map[string]any{"type": "message", "id": "msg_mock", "status": "in_progress",
		"role": "assistant", "content": []any{}}
	emit("response.created", map[string]any{"type": "response.created",
		"response": map[string]any{"id": "resp_mock", "status": "in_progress"}})
	emit("response.output_item.added", map[string]any{"type": "response.output_item.added",
		"output_index": 0, "item": item})
	emit("response.content_part.added", map[string]any{"type": "response.content_part.added",
		"output_index": 0, "content_index": 0,
		"part": map[string]any{"type": "output_text", "text": ""}})

	for _, word := range strings.SplitAfter(reply, " ") {
		emit("response.output_text.delta", map[string]any{"type": "response.output_text.delta",
			"output_index": 0, "content_index": 0, "delta": word})
		time.Sleep(50 * time.Millisecond)
	}

	emit("response.output_text.done", map[string]any{"type": "response.output_text.done",
		"output_index": 0, "content_index": 0, "text": reply})
	doneItem := map[string]any{"type": "message", "id": "msg_mock", "status": "completed",
		"role":    "assistant",
		"content": []map[string]any{{"type": "output_text", "text": reply, "annotations": []any{}}}}
	emit("response.output_item.done", map[string]any{"type": "response.output_item.done",
		"output_index": 0, "item": doneItem})
	emit("response.completed", map[string]any{"type": "response.completed",
		"response": map[string]any{"id": "resp_mock", "status": "completed",
			"output": []any{doneItem}}})
}

func serveSSE(w http.ResponseWriter, reply string) {
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	flusher, _ := w.(http.Flusher)

	words := strings.SplitAfter(reply, " ")
	for _, word := range words {
		chunk := map[string]any{
			"id":      "chatcmpl-mock",
			"object":  "chat.completion.chunk",
			"created": time.Now().Unix(),
			"choices": []map[string]any{{
				"index": 0,
				"delta": map[string]any{"content": word},
			}},
		}
		payload, _ := json.Marshal(chunk)
		_, _ = fmt.Fprintf(w, "data: %s\n\n", payload)
		if flusher != nil {
			flusher.Flush()
		}
		time.Sleep(50 * time.Millisecond)
	}
	_, _ = fmt.Fprint(w, "data: [DONE]\n\n")
	if flusher != nil {
		flusher.Flush()
	}
}
