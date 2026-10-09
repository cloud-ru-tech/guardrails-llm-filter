package gateway_test

import (
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/cloud-ru-tech/guardrails-llm-filter/internal/controller/gateway"
	"github.com/cloud-ru-tech/guardrails-llm-filter/internal/guardrails/demask"
	"github.com/cloud-ru-tech/guardrails-llm-filter/internal/models"
)

// newDecisionsHandler is newHandler with the decisions path mapped the way a
// deployment maps it: one suffix pair, GUARDRAILS_PATHS=/decisions:decisions.
func newDecisionsHandler(t *testing.T, upstreamURL string, masker gateway.Masker, audit gateway.AuditRecorder) *gateway.Handler {
	t.Helper()
	cfg := testConfig(upstreamURL)
	cfg.Guardrails.Paths["/decisions"] = "decisions"
	provider := demask.NewProvider(fakeDemaskReg{}, fakeScanner{})
	h, err := gateway.New(cfg, masker, &fakeSettings{global: enforceSettings()}, provider, audit)
	require.NoError(t, err)
	return h
}

const decisionsRequest = `{"model":"typesafe/jev","state":{"context":"ctx alice@example.com",` +
	`"history":[{"role":"user","text":"mail alice@example.com",` +
	`"tool_calls":[{"id":"t1","tool":"bash","input":"echo alice@example.com"}]}]},` +
	`"questions":{"q1":{"type":"noul","instructions":"is alice@example.com valid?"}}}`

func TestDecisionsRequestIsMasked(t *testing.T) {
	var gotBody string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		gotBody = string(b)
		_, _ = w.Write([]byte(`{"answers":{"q1":0.9}}`))
	}))
	defer upstream.Close()

	masker := &fakeMasker{reps: []models.Replacement{emailRep()}, ruleIDs: []string{"pii.email"}}
	h := newDecisionsHandler(t, upstream.URL, masker, nil)

	// Both the TypeSafe and the OpenRouter path resolve through the one suffix key.
	for _, path := range []string{"/v1/decisions", "/api/alpha/decisions"} {
		resp := doPost(t, h, path, decisionsRequest, nil)
		_ = resp.Body.Close()

		assert.NotContains(t, gotBody, "alice@example.com", "%s: original must not reach upstream", path)
		assert.Contains(t, gotBody, `"context":"ctx <EMAIL_1>"`, path)
		assert.Contains(t, gotBody, `"text":"mail <EMAIL_1>"`, path)
		assert.Contains(t, gotBody, `"input":"echo <EMAIL_1>"`, path)
		assert.Contains(t, gotBody, `"instructions":"is <EMAIL_1> valid?"`, path)
		assert.Contains(t, gotBody, `"model":"typesafe/jev"`, "%s: unscanned fields stay as sent", path)
	}
}

// One-way: a placeholder-shaped string in the answer is the upstream's own
// text, not something the filter inserted, so it must reach the client as is.
func TestDecisionsResponseUntouched(t *testing.T) {
	const answer = `{"answers":{"q1":0.9},"note":"<EMAIL_1>"}`
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("X-Upstream", "kept")
		_, _ = w.Write([]byte(answer))
	}))
	defer upstream.Close()

	masker := &fakeMasker{reps: []models.Replacement{emailRep()}, ruleIDs: []string{"pii.email"}}
	h := newDecisionsHandler(t, upstream.URL, masker, nil)

	resp := doPost(t, h, "/v1/decisions", decisionsRequest, nil)
	defer func() { _ = resp.Body.Close() }()
	body, _ := io.ReadAll(resp.Body)

	assert.Equal(t, 1, masker.calls)
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Equal(t, answer, string(body), "response must not be touched")
	assert.Equal(t, "kept", resp.Header.Get("X-Upstream"))
}

// The request is audited, but there is no masked response text to enrich the
// record with: nothing is kept for the response phase.
func TestDecisionsAuditsRequestOnly(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"answers":{"q1":0.9}}`))
	}))
	defer upstream.Close()

	masker := &fakeMasker{reps: []models.Replacement{emailRep()}, ruleIDs: []string{"pii.email"}}
	audit := &fakeAudit{}
	h := newDecisionsHandler(t, upstream.URL, masker, audit)

	resp := doPost(t, h, "/v1/decisions", decisionsRequest, nil)
	_ = resp.Body.Close()

	require.Len(t, audit.calls, 1, "the masked request is audited")
	assert.Equal(t, models.APIFormatDecisions, audit.calls[0].md.Format)
	assert.Empty(t, audit.responseTexts, "no response enrichment for a one-way format")
}

func TestDecisionsNon2xxIsRelayed(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error":"bad key"}`))
	}))
	defer upstream.Close()

	masker := &fakeMasker{reps: []models.Replacement{emailRep()}, ruleIDs: []string{"pii.email"}}
	h := newDecisionsHandler(t, upstream.URL, masker, nil)

	resp := doPost(t, h, "/v1/decisions", decisionsRequest, nil)
	defer func() { _ = resp.Body.Close() }()
	body, _ := io.ReadAll(resp.Body)

	assert.Equal(t, http.StatusUnauthorized, resp.StatusCode)
	assert.JSONEq(t, `{"error":"bad key"}`, string(body))
}

// Fail open, as for every other format: a body the decisions extractor cannot
// read is forwarded unchanged and the masker is not asked.
func TestDecisionsBadSchemaFailsOpen(t *testing.T) {
	var gotBody string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		gotBody = string(b)
		_, _ = w.Write([]byte(`{}`))
	}))
	defer upstream.Close()

	masker := &fakeMasker{reps: []models.Replacement{emailRep()}}
	h := newDecisionsHandler(t, upstream.URL, masker, nil)

	const chatBody = `{"messages":[{"role":"user","content":"alice@example.com"}]}`
	resp := doPost(t, h, "/v1/decisions", chatBody, nil)
	_ = resp.Body.Close()

	assert.Equal(t, 0, masker.calls)
	assert.Equal(t, chatBody, gotBody)
}
