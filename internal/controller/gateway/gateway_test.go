package gateway_test

import (
	"context"
	"io"
	"maps"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/cloud-ru-tech/guardrails-llm-filter/internal/config"
	"github.com/cloud-ru-tech/guardrails-llm-filter/internal/controller/gateway"
	"github.com/cloud-ru-tech/guardrails-llm-filter/internal/guardrails/demask"
	"github.com/cloud-ru-tech/guardrails-llm-filter/internal/models"
	"github.com/cloud-ru-tech/guardrails-llm-filter/internal/usecases/guardrails/mask"
	"github.com/cloud-ru-tech/guardrails-llm-filter/pkg/guardrails/regex/scanners/placeholder"
)

// --- fakes ---

type fakeSettings struct{ global models.GuardrailsSettings }

func (f *fakeSettings) Global() models.GuardrailsSettings { return f.global }

// fakeMasker applies configured placeholder substitutions to the texts it is
// given, mirroring the real mask use case closely enough for wiring tests.
type fakeMasker struct {
	calls     int
	reps      []models.Replacement
	ruleIDs   []string
	dataTypes []models.DataType
	err       error
}

func (f *fakeMasker) Handle(_ context.Context, cmd mask.Command) (mask.CommandResponse, error) {
	f.calls++
	if f.err != nil {
		return mask.CommandResponse{}, f.err
	}
	masked := make([]string, len(cmd.Texts))
	triggered := false
	for i, t := range cmd.Texts {
		m := t
		for _, rep := range f.reps {
			if strings.Contains(m, rep.Original) {
				m = strings.ReplaceAll(m, rep.Original, rep.Placeholder)
				triggered = true
			}
		}
		masked[i] = m
	}
	st := models.MaskingState{}
	if triggered {
		st.Replacements = f.reps
		st.TriggeredRuleIDs = f.ruleIDs
		st.TriggeredDataTypes = f.dataTypes
	}
	return mask.CommandResponse{MaskedTexts: masked, MaskingState: st}, nil
}

type auditCall struct {
	md          models.Metadata
	st          models.MaskingState
	maskedTexts []string
}

type fakeAudit struct {
	calls         []auditCall
	responseTexts map[string][]string
}

func (f *fakeAudit) Record(md models.Metadata, st models.MaskingState, maskedTexts []string) {
	f.calls = append(f.calls, auditCall{md, st, maskedTexts})
}

func (f *fakeAudit) RecordResponse(requestID string, maskedResponseTexts []string) {
	if f.responseTexts == nil {
		f.responseTexts = make(map[string][]string)
	}
	f.responseTexts[requestID] = maskedResponseTexts
}

// The demask provider needs a Registry and a PlaceholderScanner. Exact
// placeholder restoration is driven entirely by the masking-state replacements
// (an internal strings.Replacer), so the scanner can be a no-op here.
type fakeDemaskReg struct{}

func (fakeDemaskReg) GetMaxPlaceholderLenByRuleIDs(...string) int { return 64 }

type fakeScanner struct{}

func (fakeScanner) Scan(string, []string) ([]placeholder.Match, error) { return nil, nil }

// --- helpers ---

func emailRep() models.Replacement {
	return models.Replacement{Placeholder: "<EMAIL_1>", Original: "alice@example.com"}
}

func enforceSettings() models.GuardrailsSettings {
	return models.GuardrailsSettings{
		Enabled:   true,
		Mode:      models.ModeEnforce,
		DataTypes: []models.DataType{models.DataTypeCREDENTIALS, models.DataTypePERSONALDATA},
	}
}

func testConfig(upstreamURL string) *config.Config {
	paths := make(map[string]string, len(config.DefaultGuardrailPaths))
	maps.Copy(paths, config.DefaultGuardrailPaths)
	return &config.Config{
		Guardrails: config.Guardrails{
			OverrideHeader: "x-guardrails-data-types",
			Paths:          paths,
		},
		Upstream: config.Upstream{BaseURL: upstreamURL},
	}
}

func newHandler(t *testing.T, upstreamURL string, masker gateway.Masker, global models.GuardrailsSettings, audit gateway.AuditRecorder) *gateway.Handler {
	t.Helper()
	provider := demask.NewProvider(fakeDemaskReg{}, fakeScanner{})
	h, err := gateway.New(testConfig(upstreamURL), masker, &fakeSettings{global: global}, provider, audit)
	require.NoError(t, err)
	return h
}

func doPost(t *testing.T, h http.Handler, path, body string, headers map[string]string) *http.Response {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec.Result()
}

// --- tests ---

func TestMasksRequestBeforeForwarding(t *testing.T) {
	var gotBody string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		gotBody = string(b)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[]}`))
	}))
	defer upstream.Close()

	masker := &fakeMasker{reps: []models.Replacement{emailRep()}, ruleIDs: []string{"pii.email"}}
	h := newHandler(t, upstream.URL, masker, enforceSettings(), nil)

	resp := doPost(t, h, "/v1/chat/completions",
		`{"messages":[{"role":"user","content":"contact alice@example.com"}]}`, nil)
	defer func() { _ = resp.Body.Close() }()

	assert.Equal(t, 1, masker.calls)
	assert.Contains(t, gotBody, "<EMAIL_1>", "upstream must receive the masked placeholder")
	assert.NotContains(t, gotBody, "alice@example.com", "original must not reach upstream")
}

func TestNonStreamingResponseDemasked(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		// Upstream echoes the placeholder back inside the assistant message.
		_, _ = w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"reach <EMAIL_1> now"}}]}`))
	}))
	defer upstream.Close()

	masker := &fakeMasker{reps: []models.Replacement{emailRep()}, ruleIDs: []string{"pii.email"}}
	h := newHandler(t, upstream.URL, masker, enforceSettings(), nil)

	resp := doPost(t, h, "/v1/chat/completions",
		`{"messages":[{"role":"user","content":"alice@example.com"}]}`, nil)
	defer func() { _ = resp.Body.Close() }()
	body, _ := io.ReadAll(resp.Body)

	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Contains(t, string(body), "alice@example.com", "response must be demasked")
	assert.NotContains(t, string(body), "<EMAIL_1>", "no placeholder may leak to the client")
}

func TestNonStreamingResponseCapturesMaskedText(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"reach <EMAIL_1> now"}}]}`))
	}))
	defer upstream.Close()

	masker := &fakeMasker{reps: []models.Replacement{emailRep()}, ruleIDs: []string{"pii.email"}}
	audit := &fakeAudit{}
	h := newHandler(t, upstream.URL, masker, enforceSettings(), audit)

	resp := doPost(t, h, "/v1/chat/completions",
		`{"messages":[{"role":"user","content":"alice@example.com"}]}`, nil)
	defer func() { _ = resp.Body.Close() }()
	_, _ = io.ReadAll(resp.Body)

	require.Len(t, audit.responseTexts, 1, "one record enriched with masked response text")
	for _, texts := range audit.responseTexts {
		assert.Equal(t, []string{"reach <EMAIL_1> now"}, texts, "masked (pre-demask) response text is captured")
	}
}

func TestSSEResponseDemaskedFrameByFrame(t *testing.T) {
	frames := []string{
		`data: {"choices":[{"delta":{"role":"assistant"}}]}`,
		`data: {"choices":[{"delta":{"content":"reach <EMAIL_1>"}}]}`,
		`data: {"choices":[{"delta":{"content":" today"},"finish_reason":"stop"}]}`,
		`data: [DONE]`,
	}
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		fl, _ := w.(http.Flusher)
		for _, f := range frames {
			_, _ = io.WriteString(w, f+"\n\n")
			if fl != nil {
				fl.Flush()
			}
		}
	}))
	defer upstream.Close()

	masker := &fakeMasker{reps: []models.Replacement{emailRep()}, ruleIDs: []string{"pii.email"}}
	h := newHandler(t, upstream.URL, masker, enforceSettings(), nil)

	resp := doPost(t, h, "/v1/chat/completions",
		`{"stream":true,"messages":[{"role":"user","content":"alice@example.com"}]}`, nil)
	defer func() { _ = resp.Body.Close() }()
	body, _ := io.ReadAll(resp.Body)

	assert.Contains(t, resp.Header.Get("Content-Type"), "text/event-stream")
	assert.Contains(t, string(body), "alice@example.com", "streamed content must be demasked")
	assert.NotContains(t, string(body), "<EMAIL_1>", "no placeholder may leak in any SSE frame")
	assert.Contains(t, string(body), "data:", "SSE framing must be preserved")
	assert.Contains(t, string(body), "[DONE]", "stream terminator must be preserved")
}

func TestSSEResponseCapturesMaskedText(t *testing.T) {
	frames := []string{
		`data: {"choices":[{"delta":{"role":"assistant"}}]}`,
		`data: {"choices":[{"delta":{"content":"reach <EMAIL_1>"}}]}`,
		`data: {"choices":[{"delta":{"content":" today"},"finish_reason":"stop"}]}`,
		`data: [DONE]`,
	}
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		fl, _ := w.(http.Flusher)
		for _, f := range frames {
			_, _ = io.WriteString(w, f+"\n\n")
			if fl != nil {
				fl.Flush()
			}
		}
	}))
	defer upstream.Close()

	masker := &fakeMasker{reps: []models.Replacement{emailRep()}, ruleIDs: []string{"pii.email"}}
	audit := &fakeAudit{}
	h := newHandler(t, upstream.URL, masker, enforceSettings(), audit)

	resp := doPost(t, h, "/v1/chat/completions",
		`{"stream":true,"messages":[{"role":"user","content":"alice@example.com"}]}`, nil)
	defer func() { _ = resp.Body.Close() }()
	_, _ = io.ReadAll(resp.Body)

	require.Len(t, audit.responseTexts, 1, "one record enriched at end-of-stream")
	for _, texts := range audit.responseTexts {
		// Deltas of the one content stream are concatenated into the masked text.
		assert.Equal(t, []string{"reach <EMAIL_1> today"}, texts)
	}
}

func TestHopByHopHeadersStripped(t *testing.T) {
	var got http.Header
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = r.Header.Clone()
		_, _ = w.Write([]byte(`{}`))
	}))
	defer upstream.Close()

	h := newHandler(t, upstream.URL, &fakeMasker{}, enforceSettings(), nil)

	resp := doPost(t, h, "/v1/chat/completions", `{"messages":[]}`, map[string]string{
		"Connection":   "X-Drop-Me",
		"X-Drop-Me":    "secret",
		"Keep-Alive":   "timeout=5",
		"X-Keep-Me":    "kept",
		"Content-Type": "application/json",
	})
	defer func() { _ = resp.Body.Close() }()

	assert.Empty(t, got.Get("X-Drop-Me"), "header named in Connection must be stripped")
	assert.Empty(t, got.Get("Keep-Alive"), "hop-by-hop header must be stripped")
	assert.Empty(t, got.Get("Connection"), "Connection header must be stripped")
	assert.Equal(t, "kept", got.Get("X-Keep-Me"), "end-to-end headers must be forwarded")
}

func TestOverrideHeaderNotForwarded(t *testing.T) {
	var got http.Header
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = r.Header.Clone()
		_, _ = w.Write([]byte(`{}`))
	}))
	defer upstream.Close()

	h := newHandler(t, upstream.URL, &fakeMasker{}, enforceSettings(), nil)
	resp := doPost(t, h, "/v1/chat/completions", `{"messages":[]}`, map[string]string{
		"x-guardrails-data-types": "5",
	})
	defer func() { _ = resp.Body.Close() }()

	assert.Empty(t, got.Get("x-guardrails-data-types"), "override header is consumed, not forwarded")
}

func TestClientDisconnectCancelsUpstream(t *testing.T) {
	// The upstream blocks forever without responding. With ResponseHeaderTimeout
	// unset (0), a normal request would hang in client.Do indefinitely; the
	// gateway must instead abort as soon as the client's request context is
	// canceled, so ServeHTTP returns promptly.
	upstreamStarted := make(chan struct{})
	var startOnce sync.Once
	block := make(chan struct{})
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		startOnce.Do(func() { close(upstreamStarted) })
		select {
		case <-r.Context().Done():
		case <-block:
		}
	}))
	defer upstream.Close()
	defer close(block)

	h := newHandler(t, upstream.URL, &fakeMasker{}, enforceSettings(), nil)

	ctx, cancel := context.WithCancel(context.Background())
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{"messages":[]}`)).WithContext(ctx)
	rec := httptest.NewRecorder()

	done := make(chan struct{})
	go func() {
		h.ServeHTTP(rec, req)
		close(done)
	}()

	// Wait until the upstream is actually handling the request before the
	// client disconnects, otherwise cancellation could race the dial.
	select {
	case <-upstreamStarted:
	case <-time.After(3 * time.Second):
		t.Fatal("upstream never received the request")
	}
	cancel() // client disconnects

	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("ServeHTTP did not return after client disconnect — upstream request was not canceled")
	}
}

func TestNon2xxUpstreamPassthrough(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error":{"message":"bad request"}}`))
	}))
	defer upstream.Close()

	masker := &fakeMasker{reps: []models.Replacement{emailRep()}, ruleIDs: []string{"pii.email"}}
	h := newHandler(t, upstream.URL, masker, enforceSettings(), nil)

	resp := doPost(t, h, "/v1/chat/completions",
		`{"messages":[{"role":"user","content":"alice@example.com"}]}`, nil)
	defer func() { _ = resp.Body.Close() }()
	body, _ := io.ReadAll(resp.Body)

	assert.Equal(t, http.StatusBadRequest, resp.StatusCode, "upstream status must be propagated")
	assert.JSONEq(t, `{"error":{"message":"bad request"}}`, string(body), "error body relayed unchanged")
}

func TestUnguardedPathPassthrough(t *testing.T) {
	var gotBody string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		gotBody = string(b)
		_, _ = w.Write([]byte(`{"data":"<EMAIL_1>"}`))
	}))
	defer upstream.Close()

	masker := &fakeMasker{reps: []models.Replacement{emailRep()}}
	h := newHandler(t, upstream.URL, masker, enforceSettings(), nil)

	resp := doPost(t, h, "/v1/embeddings",
		`{"input":"alice@example.com"}`, nil)
	defer func() { _ = resp.Body.Close() }()
	body, _ := io.ReadAll(resp.Body)

	assert.Equal(t, 0, masker.calls, "unguarded path must not invoke the masker")
	assert.Contains(t, gotBody, "alice@example.com", "unguarded request forwarded unchanged")
	// No demasking on an unguarded path: whatever the upstream returns is relayed as-is.
	assert.Contains(t, string(body), "<EMAIL_1>", "unguarded response relayed unchanged")
}

func TestDetectModePassthroughWithAudit(t *testing.T) {
	var gotBody string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		gotBody = string(b)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"<EMAIL_1>"}}]}`))
	}))
	defer upstream.Close()

	global := enforceSettings()
	global.Mode = models.ModeDetect
	masker := &fakeMasker{reps: []models.Replacement{emailRep()}, ruleIDs: []string{"pii.email"}}
	audit := &fakeAudit{}
	h := newHandler(t, upstream.URL, masker, global, audit)

	resp := doPost(t, h, "/v1/chat/completions",
		`{"messages":[{"role":"user","content":"alice@example.com"}]}`, nil)
	defer func() { _ = resp.Body.Close() }()
	body, _ := io.ReadAll(resp.Body)

	assert.Contains(t, gotBody, "alice@example.com", "detect mode must forward the body unchanged")
	assert.NotContains(t, gotBody, "<EMAIL_1>", "detect mode must not mask")
	assert.Len(t, audit.calls, 1, "detect mode still records an audit entry")
	// No masking state ⇒ response relayed verbatim (placeholder stays as-is).
	assert.Contains(t, string(body), "<EMAIL_1>")
}

func TestDisabledGuardrailsPassthrough(t *testing.T) {
	var gotBody string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		gotBody = string(b)
		_, _ = w.Write([]byte(`{}`))
	}))
	defer upstream.Close()

	global := enforceSettings()
	global.Enabled = false
	masker := &fakeMasker{reps: []models.Replacement{emailRep()}}
	h := newHandler(t, upstream.URL, masker, global, nil)

	resp := doPost(t, h, "/v1/chat/completions",
		`{"messages":[{"role":"user","content":"alice@example.com"}]}`, nil)
	defer func() { _ = resp.Body.Close() }()

	assert.Equal(t, 0, masker.calls, "disabled guardrails must not invoke the masker")
	assert.Contains(t, gotBody, "alice@example.com", "disabled guardrails forward unchanged")
}

func TestNoUpstreamConfiguredReturnsBadGateway(t *testing.T) {
	h := newHandler(t, "", &fakeMasker{}, enforceSettings(), nil)
	resp := doPost(t, h, "/v1/chat/completions", `{"messages":[]}`, nil)
	defer func() { _ = resp.Body.Close() }()
	assert.Equal(t, http.StatusBadGateway, resp.StatusCode)
}

// TestStreamRequestDemasksMislabeledSSE proves the client's stream:true intent
// drives SSE routing even when the upstream fails to label the response
// Content-Type: text/event-stream. Without the request-flag fallback the body
// would be buffered whole and demaskFull would fail to parse the multi-frame
// SSE as one JSON object, leaking the placeholder. The audit metadata must also
// carry IsStreaming=true derived from the request body.
func TestStreamRequestDemasksMislabeledSSE(t *testing.T) {
	frames := []string{
		`data: {"choices":[{"delta":{"content":"reach <EMAIL_1>"}}]}`,
		`data: {"choices":[{"delta":{"content":" today"},"finish_reason":"stop"}]}`,
		`data: [DONE]`,
	}
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		// Deliberately NOT text/event-stream — an upstream that mislabels its stream.
		w.Header().Set("Content-Type", "text/plain")
		fl, _ := w.(http.Flusher)
		for _, f := range frames {
			_, _ = io.WriteString(w, f+"\n\n")
			if fl != nil {
				fl.Flush()
			}
		}
	}))
	defer upstream.Close()

	masker := &fakeMasker{reps: []models.Replacement{emailRep()}, ruleIDs: []string{"pii.email"}}
	audit := &fakeAudit{}
	h := newHandler(t, upstream.URL, masker, enforceSettings(), audit)

	resp := doPost(t, h, "/v1/chat/completions",
		`{"stream":true,"messages":[{"role":"user","content":"alice@example.com"}]}`, nil)
	defer func() { _ = resp.Body.Close() }()
	body, _ := io.ReadAll(resp.Body)

	assert.Contains(t, string(body), "alice@example.com", "mislabeled stream must still be demasked frame-by-frame")
	assert.NotContains(t, string(body), "<EMAIL_1>", "no placeholder may leak")
	assert.Contains(t, string(body), "[DONE]", "stream terminator preserved")
	require.Len(t, audit.calls, 1)
	assert.True(t, audit.calls[0].md.IsStreaming, "stream flag from request body must set IsStreaming")
}

// TestNonStreamRequestKeepsFullBodyPath is the counterpart: without stream:true
// in the request and without an SSE Content-Type, the response stays on the
// full-body path and IsStreaming is false.
func TestNonStreamRequestKeepsFullBodyPath(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"<EMAIL_1>"}}]}`))
	}))
	defer upstream.Close()

	masker := &fakeMasker{reps: []models.Replacement{emailRep()}, ruleIDs: []string{"pii.email"}}
	audit := &fakeAudit{}
	h := newHandler(t, upstream.URL, masker, enforceSettings(), audit)

	resp := doPost(t, h, "/v1/chat/completions",
		`{"messages":[{"role":"user","content":"alice@example.com"}]}`, nil)
	defer func() { _ = resp.Body.Close() }()
	body, _ := io.ReadAll(resp.Body)

	assert.Contains(t, string(body), "alice@example.com", "full JSON response demasked")
	require.Len(t, audit.calls, 1)
	assert.False(t, audit.calls[0].md.IsStreaming, "no stream flag ⇒ IsStreaming false")
}

func newHandlerMaxBytes(t *testing.T, upstreamURL string, maxBytes int64) *gateway.Handler {
	t.Helper()
	cfg := testConfig(upstreamURL)
	cfg.MaxRequestBytes = maxBytes
	provider := demask.NewProvider(fakeDemaskReg{}, fakeScanner{})
	h, err := gateway.New(cfg, &fakeMasker{}, &fakeSettings{global: enforceSettings()}, provider, nil)
	require.NoError(t, err)
	return h
}

// TestRequestBodyLimit rejects an over-limit body with 413 and lets a body
// within the limit through unchanged.
func TestRequestBodyLimit(t *testing.T) {
	var reached bool
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		reached = true
		_, _ = w.Write([]byte(`{}`))
	}))
	defer upstream.Close()

	t.Run("over limit returns 413", func(t *testing.T) {
		reached = false
		h := newHandlerMaxBytes(t, upstream.URL, 16)
		resp := doPost(t, h, "/v1/chat/completions",
			`{"messages":[{"role":"user","content":"far larger than sixteen bytes"}]}`, nil)
		defer func() { _ = resp.Body.Close() }()
		assert.Equal(t, http.StatusRequestEntityTooLarge, resp.StatusCode)
		assert.False(t, reached, "over-limit request must not reach the upstream")
	})

	t.Run("within limit proxies", func(t *testing.T) {
		reached = false
		h := newHandlerMaxBytes(t, upstream.URL, 1<<20)
		resp := doPost(t, h, "/v1/chat/completions", `{"messages":[]}`, nil)
		defer func() { _ = resp.Body.Close() }()
		assert.Equal(t, http.StatusOK, resp.StatusCode)
		assert.True(t, reached, "within-limit request must reach the upstream")
	})
}
