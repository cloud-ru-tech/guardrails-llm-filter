package gateway_test

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/cloud-ru-tech/guardrails-llm-filter/internal/controller/gateway"
	"github.com/cloud-ru-tech/guardrails-llm-filter/internal/guardrails/demask"
	"github.com/cloud-ru-tech/guardrails-llm-filter/internal/usecases/guardrails/mask"
	"github.com/cloud-ru-tech/guardrails-llm-filter/pkg/guardrails/regex/registry"
	"github.com/cloud-ru-tech/guardrails-llm-filter/pkg/guardrails/regex/rule"
	"github.com/cloud-ru-tech/guardrails-llm-filter/pkg/guardrails/regex/scanners/placeholder"
	"github.com/cloud-ru-tech/guardrails-llm-filter/pkg/guardrails/regex/scanners/sensitive"
	"github.com/cloud-ru-tech/guardrails-llm-filter/tests/testutil"
)

// This file is an end-to-end round-trip test: it wires the REAL masker and
// demasker (shipped rules) into the data-plane gateway, points it at a mock
// upstream that echoes "You said: <content>", and asserts that masking never
// corrupts the JSON body or the SSE stream. Because the upstream echoes, a
// lossless mask -> forward -> demask cycle must return the EXACT original text.

// capture is a race-safe holder for the content the mock upstream received (i.e.
// what the "LLM" sees), so a test can assert masking actually happened.
type capture struct {
	mu   sync.Mutex
	last string
}

func (c *capture) set(s string) { c.mu.Lock(); c.last = s; c.mu.Unlock() }
func (c *capture) get() string  { c.mu.Lock(); defer c.mu.Unlock(); return c.last }

type upstreamReq struct {
	Stream   bool `json:"stream"`
	Messages []struct {
		Role    string          `json:"role"`
		Content json.RawMessage `json:"content"`
	} `json:"messages"`
}

func lastUserContent(req upstreamReq) string {
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

// echoUpstream is an OpenAI-compatible mock that echoes "You said: <content>",
// in JSON or SSE per the request's stream flag, and records what it received.
func echoUpstream(t *testing.T, seen *capture) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req upstreamReq
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		content := lastUserContent(req)
		seen.set(content)
		reply := "You said: " + content

		if req.Stream {
			w.Header().Set("Content-Type", "text/event-stream")
			fl, _ := w.(http.Flusher)
			for _, word := range strings.SplitAfter(reply, " ") {
				chunk, _ := json.Marshal(map[string]any{
					"id": "chatcmpl-mock", "object": "chat.completion.chunk",
					"choices": []map[string]any{{"index": 0, "delta": map[string]any{"content": word}}},
				})
				_, _ = io.WriteString(w, "data: "+string(chunk)+"\n\n")
				if fl != nil {
					fl.Flush()
				}
			}
			_, _ = io.WriteString(w, "data: [DONE]\n\n")
			if fl != nil {
				fl.Flush()
			}
			return
		}

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"id": "chatcmpl-mock", "object": "chat.completion",
			"choices": []map[string]any{{
				"index": 0, "finish_reason": "stop",
				"message": map[string]any{"role": "assistant", "content": reply},
			}},
		})
	}))
}

func loadRealRegistryGW(t *testing.T) *registry.Registry {
	t.Helper()
	root := testutil.RepoRoot(t)
	_, rules, err := rule.LoadAllFromFiles(
		filepath.Join(root, "configs/guardrails_regex_rules.gitleaks.generated.yaml"),
		filepath.Join(root, "configs/guardrails_regex_rules.yaml"),
	)
	require.NoError(t, err)
	reg := registry.NewRegistry()
	reg.Register(rules...)
	return reg
}

// realGatewayHandler wires the shipped rules into a gateway Handler pointed at
// upstreamURL, with the real masker and demasker (no fakes).
func realGatewayHandler(t *testing.T, upstreamURL string) *gateway.Handler {
	t.Helper()
	reg := loadRealRegistryGW(t)
	masker := mask.New(mask.Deps{Registry: reg, Scanner: sensitive.New(reg)})
	provider := demask.NewProvider(reg, placeholder.New(reg))
	h, err := gateway.New(testConfig(upstreamURL), masker, &fakeSettings{global: enforceSettings()}, provider, &fakeAudit{})
	require.NoError(t, err)
	return h
}

func chatBody(t *testing.T, text string, stream bool) string {
	t.Helper()
	b, err := json.Marshal(map[string]any{
		"model":    "gpt",
		"stream":   stream,
		"messages": []map[string]any{{"role": "user", "content": text}},
	})
	require.NoError(t, err)
	return string(b)
}

// roundTripCases stress the JSON/SSE encoding boundaries the dataset alone may
// not hit: escaping, a literal placeholder collision, and a no-PII passthrough.
var roundTripCases = []struct{ name, text string }{
	{"pii-rich", "Иванов Иван Петрович, тел +7 900 123-45-67, ИНН 500100732259, ул. Тверская 15"},
	{"escaped-quotes", `он сказал "Иванов Иван Петрович" и "Петров Пётр Сергеевич"`},
	{"newlines-tabs", "строка1\tИванов Иван Петрович\nстрока2 тел +7 900 123-45-67"},
	{"json-in-json", `{"fio":"Иванов Иван Петрович","inn":"500100732259"}`},
	{"emoji", "🔥 Иванов Иван Петрович 😀 ✅"},
	{"placeholder-collision", "в тексте уже есть <FIO_1> и звонил Иванов Иван Петрович"},
	{"no-pii", "просто обычный текст без персональных данных"},
}

func TestE2E_NonStreaming_RoundTrip(t *testing.T) {
	t.Parallel()
	seen := &capture{}
	up := echoUpstream(t, seen)
	defer up.Close()
	h := realGatewayHandler(t, up.URL)
	jsonHdr := map[string]string{"Content-Type": "application/json"}

	for _, tc := range roundTripCases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			resp := doPost(t, h, "/v1/chat/completions", chatBody(t, tc.text, false), jsonHdr)
			defer resp.Body.Close()
			require.Equal(t, http.StatusOK, resp.StatusCode)

			var out struct {
				Choices []struct {
					Message struct{ Content string } `json:"message"`
				} `json:"choices"`
			}
			body, _ := io.ReadAll(resp.Body)
			require.NoError(t, json.Unmarshal(body, &out), "response must be valid JSON: %s", body)
			require.Len(t, out.Choices, 1)
			// Lossless round-trip: demasked echo equals the original input exactly.
			assert.Equal(t, "You said: "+tc.text, out.Choices[0].Message.Content)
		})
	}
}

func TestE2E_SSE_RoundTrip(t *testing.T) {
	t.Parallel()
	seen := &capture{}
	up := echoUpstream(t, seen)
	defer up.Close()
	h := realGatewayHandler(t, up.URL)
	jsonHdr := map[string]string{"Content-Type": "application/json"}

	for _, tc := range roundTripCases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			resp := doPost(t, h, "/v1/chat/completions", chatBody(t, tc.text, true), jsonHdr)
			defer resp.Body.Close()
			require.Equal(t, http.StatusOK, resp.StatusCode)
			assert.Contains(t, resp.Header.Get("Content-Type"), "text/event-stream")

			body, _ := io.ReadAll(resp.Body)
			assembled, done := parseSSE(t, body)
			assert.True(t, done, "SSE stream must terminate with [DONE]")
			// Deltas re-assemble to the exact original despite proxy re-chunking.
			assert.Equal(t, "You said: "+tc.text, assembled)
		})
	}
}

// parseSSE splits an SSE body into frames, asserts each data: payload (other than
// [DONE]) is valid JSON, and returns the concatenated delta content plus whether
// the [DONE] terminator was seen.
func parseSSE(t *testing.T, body []byte) (assembled string, done bool) {
	t.Helper()
	var sb strings.Builder
	for _, frame := range strings.Split(string(body), "\n\n") {
		frame = strings.TrimSpace(frame)
		if frame == "" {
			continue
		}
		require.True(t, strings.HasPrefix(frame, "data: "), "frame must start with 'data: ': %q", frame)
		payload := strings.TrimPrefix(frame, "data: ")
		if payload == "[DONE]" {
			done = true
			continue
		}
		var chunk struct {
			Choices []struct {
				Delta struct{ Content string } `json:"delta"`
			} `json:"choices"`
		}
		require.NoError(t, json.Unmarshal([]byte(payload), &chunk), "SSE data must be valid JSON: %q", payload)
		if len(chunk.Choices) > 0 {
			sb.WriteString(chunk.Choices[0].Delta.Content)
		}
	}
	return sb.String(), done
}

// TestE2E_MaskingActuallyHappens proves the pipeline is not a passthrough: the
// upstream (the "LLM") must see placeholders, not the raw PII — while the client
// still gets the original back (verified by the round-trip tests above).
func TestE2E_MaskingActuallyHappens(t *testing.T) {
	t.Parallel()
	seen := &capture{}
	up := echoUpstream(t, seen)
	defer up.Close()
	h := realGatewayHandler(t, up.URL)
	jsonHdr := map[string]string{"Content-Type": "application/json"}

	text := "Иванов Иван Петрович, ИНН 500100732259"
	resp := doPost(t, h, "/v1/chat/completions", chatBody(t, text, false), jsonHdr)
	resp.Body.Close()

	llmSaw := seen.get()
	assert.NotContains(t, llmSaw, "Иванов Иван Петрович", "the LLM must not see the raw name")
	assert.NotContains(t, llmSaw, "500100732259", "the LLM must not see the raw INN")
	assert.Contains(t, llmSaw, "<FIO_1>", "the LLM must see the FIO placeholder")
	assert.Contains(t, llmSaw, "<INN_PERSON_1>", "the LLM must see the INN placeholder")
}
