package engine_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/cloud-ru-tech/guardrails-llm-filter/internal/controller/engine"
	"github.com/cloud-ru-tech/guardrails-llm-filter/internal/guardrails/demask"
	"github.com/cloud-ru-tech/guardrails-llm-filter/internal/models"
	"github.com/cloud-ru-tech/guardrails-llm-filter/internal/repository"
	maskuc "github.com/cloud-ru-tech/guardrails-llm-filter/internal/usecases/guardrails/mask"
	"github.com/cloud-ru-tech/guardrails-llm-filter/pkg/guardrails/regex/rule"
	"github.com/cloud-ru-tech/guardrails-llm-filter/pkg/guardrails/regex/scanners/placeholder"
)

const testToken = "s3cr3t-token"

// fakeMasker returns a canned mask result and records the command it was given.
type fakeMasker struct {
	mu   sync.Mutex
	last maskuc.Command
	resp maskuc.CommandResponse
	err  error
}

func (f *fakeMasker) Handle(_ context.Context, cmd maskuc.Command) (maskuc.CommandResponse, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.last = cmd
	return f.resp, f.err
}

// fakeStore is an in-memory masking-state store.
type fakeStore struct {
	mu     sync.Mutex
	states map[string]models.MaskingState
	putErr error
	getErr error
}

func newFakeStore() *fakeStore {
	return &fakeStore{states: map[string]models.MaskingState{}}
}

func (f *fakeStore) PutMaskingState(_ context.Context, requestID string, st models.MaskingState) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.putErr != nil {
		return f.putErr
	}
	f.states[requestID] = st
	return nil
}

func (f *fakeStore) GetMaskingState(_ context.Context, requestID string) (models.MaskingState, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.getErr != nil {
		return models.MaskingState{}, f.getErr
	}
	st, ok := f.states[requestID]
	if !ok {
		return models.MaskingState{}, repository.ErrNotFound
	}
	return st, nil
}

// fakeRules resolves rule IDs to rules so a placeholder can report its data type.
type fakeRules struct{}

func (fakeRules) GetRulesByIDs(ruleIDs ...string) []rule.Rule {
	out := make([]rule.Rule, 0, len(ruleIDs))
	for _, id := range ruleIDs {
		dt := int(models.DataTypePERSONALDATA)
		if strings.Contains(id, "token") {
			dt = int(models.DataTypeACCESSTOKENS)
		}
		out = append(out, rule.Rule{ID: id, DataType: dt})
	}
	return out
}

// noopRegistry/noopScanner satisfy demask.Provider's inputs: the exact-replacer
// index built from the masking state is what restores placeholders, so the
// regex scan path is not exercised here.
type noopRegistry struct{}

func (noopRegistry) GetMaxPlaceholderLenByRuleIDs(...string) int { return 0 }

type noopScanner struct{}

func (noopScanner) Scan(string, []string) ([]placeholder.Match, error) { return nil, nil }

func newHandler(t *testing.T, masker engine.Masker, store engine.StateStore) *engine.Handler {
	t.Helper()
	return engine.New(engine.Deps{
		Masker:           masker,
		Demasker:         demask.NewProvider(noopRegistry{}, noopScanner{}),
		Rules:            fakeRules{},
		Store:            store,
		DefaultDataTypes: []models.DataType{models.DataTypePERSONALDATA, models.DataTypeCUSTOM},
		Token:            testToken,
		MaxRequestBytes:  4096,
	})
}

func do(t *testing.T, h http.Handler, method, path, body string, token string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func decode[T any](t *testing.T, rec *httptest.ResponseRecorder) T {
	t.Helper()
	var out T
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &out), "body=%s", rec.Body.String())
	return out
}

func maskedResponse() maskuc.CommandResponse {
	return maskuc.CommandResponse{
		MaskedTexts: []string{"write to <EMAIL_1> about <PHONE_1>"},
		MaskingState: models.MaskingState{
			TriggeredRuleIDs:   []string{"pii.email", "pii.phone-ru"},
			TriggeredDataTypes: []models.DataType{models.DataTypePERSONALDATA},
			Replacements: []models.Replacement{
				{RuleID: "pii.email", Original: "a@b.c", Placeholder: "<EMAIL_1>"},
				{RuleID: "pii.phone-ru", Original: "+79990000000", Placeholder: "<PHONE_1>"},
			},
		},
	}
}

func TestMaskThenUnmaskRoundTrip(t *testing.T) {
	store := newFakeStore()
	masker := &fakeMasker{resp: maskedResponse()}
	h := newHandler(t, masker, store)

	rec := do(t, h, http.MethodPost, engine.PathMask,
		`{"texts":["write to a@b.c about +79990000000"],"correlation_id":"conv-1"}`, testToken)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())

	got := decode[map[string]any](t, rec)
	assert.Equal(t, "conv-1", got["correlation_id"])
	assert.Equal(t, []any{"write to <EMAIL_1> about <PHONE_1>"}, got["masked_texts"])

	placeholders := got["placeholders"].([]any)
	require.Len(t, placeholders, 2)
	first := placeholders[0].(map[string]any)
	assert.Equal(t, "<EMAIL_1>", first["placeholder"])
	assert.Equal(t, "pii.email", first["rule_id"])
	assert.Equal(t, "PERSONAL_DATA", first["data_type"])
	// The mask response must never carry the original value; only /v1/unmask does.
	assert.NotContains(t, rec.Body.String(), "a@b.c")

	// The state is keyed by the caller's correlation id, so a later call restores.
	rec = do(t, h, http.MethodPost, engine.PathUnmask,
		`{"texts":["reply to <EMAIL_1>"],"correlation_id":"conv-1"}`, testToken)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	unmasked := decode[map[string]any](t, rec)
	assert.Equal(t, []any{"reply to a@b.c"}, unmasked["texts"])
}

func TestMaskWithoutFindingsStillPersistsState(t *testing.T) {
	store := newFakeStore()
	// Nothing triggered: the mask use case returns no texts and empty state.
	masker := &fakeMasker{}
	h := newHandler(t, masker, store)

	rec := do(t, h, http.MethodPost, engine.PathMask,
		`{"texts":["nothing sensitive"],"correlation_id":"conv-empty"}`, testToken)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	got := decode[map[string]any](t, rec)
	assert.Equal(t, []any{"nothing sensitive"}, got["masked_texts"], "input must be echoed back")

	// A later unmask must find the id rather than report it unknown.
	rec = do(t, h, http.MethodPost, engine.PathUnmask,
		`{"texts":["nothing sensitive"],"correlation_id":"conv-empty"}`, testToken)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
}

func TestMaskPersistsStateBeforeResponding(t *testing.T) {
	store := newFakeStore()
	masker := &fakeMasker{resp: maskedResponse()}
	h := newHandler(t, masker, store)

	do(t, h, http.MethodPost, engine.PathMask,
		`{"texts":["x"],"correlation_id":"conv-store"}`, testToken)

	require.Contains(t, store.states, "conv-store")
	assert.Equal(t, []string{"pii.email", "pii.phone-ru"}, store.states["conv-store"].TriggeredRuleIDs)
}

func TestMaskFailsClosedWhenStateCannotBePersisted(t *testing.T) {
	store := newFakeStore()
	store.putErr = errors.New("redis down")
	h := newHandler(t, &fakeMasker{resp: maskedResponse()}, store)

	rec := do(t, h, http.MethodPost, engine.PathMask,
		`{"texts":["x"],"correlation_id":"conv-1"}`, testToken)

	// Returning masked text whose state was never stored would hand the caller
	// a value it can never unmask.
	require.Equal(t, http.StatusServiceUnavailable, rec.Code, rec.Body.String())
	assert.Equal(t, engine.CodeStoreUnavailable, decode[map[string]string](t, rec)["code"])
	assert.NotContains(t, rec.Body.String(), "<EMAIL_1>")
}

func TestUnmaskUnknownCorrelationID(t *testing.T) {
	h := newHandler(t, &fakeMasker{}, newFakeStore())

	rec := do(t, h, http.MethodPost, engine.PathUnmask,
		`{"texts":["<EMAIL_1>"],"correlation_id":"nope"}`, testToken)

	require.Equal(t, http.StatusNotFound, rec.Code, rec.Body.String())
	assert.Equal(t, engine.CodeUnknownCorrelationID, decode[map[string]string](t, rec)["code"])
	// Fail closed: the placeholder is not echoed back as if it were restored.
	assert.NotContains(t, rec.Body.String(), "<EMAIL_1>")
}

func TestUnmaskStoreFailureIsServiceUnavailable(t *testing.T) {
	store := newFakeStore()
	store.getErr = errors.New("redis down")
	h := newHandler(t, &fakeMasker{}, store)

	rec := do(t, h, http.MethodPost, engine.PathUnmask,
		`{"texts":["x"],"correlation_id":"conv-1"}`, testToken)

	require.Equal(t, http.StatusServiceUnavailable, rec.Code, rec.Body.String())
	assert.Equal(t, engine.CodeStoreUnavailable, decode[map[string]string](t, rec)["code"])
}

func TestEngineRequiresBearerToken(t *testing.T) {
	h := newHandler(t, &fakeMasker{resp: maskedResponse()}, newFakeStore())
	body := `{"texts":["a@b.c"],"correlation_id":"conv-1"}`

	for _, tc := range []struct {
		name  string
		token string
	}{
		{"absent", ""},
		{"wrong", "not-the-token"},
		{"prefix-only", "s3cr3t"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rec := do(t, h, http.MethodPost, engine.PathMask, body, tc.token)
			require.Equal(t, http.StatusUnauthorized, rec.Code, rec.Body.String())
			assert.Equal(t, engine.CodeUnauthorized, decode[map[string]string](t, rec)["code"])
		})
	}
}

func TestVersionEndpointNeedsNoToken(t *testing.T) {
	h := newHandler(t, &fakeMasker{}, newFakeStore())

	rec := do(t, h, http.MethodGet, engine.PathVersion, "", "")

	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	got := decode[map[string]string](t, rec)
	assert.Equal(t, engine.APIVersion, got["api_version"])
}

func TestDataTypesAcceptNamesAndNumbers(t *testing.T) {
	masker := &fakeMasker{}
	h := newHandler(t, masker, newFakeStore())

	rec := do(t, h, http.MethodPost, engine.PathMask,
		`{"texts":["x"],"correlation_id":"c","data_types":["credentials",2]}`, testToken)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	assert.Equal(t, []models.DataType{models.DataTypeCREDENTIALS, models.DataTypeAPIKEYS}, masker.last.DataTypes)

	rec = do(t, h, http.MethodPost, engine.PathMask,
		`{"texts":["x"],"correlation_id":"c","data_types":["nonsense"]}`, testToken)
	require.Equal(t, http.StatusBadRequest, rec.Code, rec.Body.String())

	// An omitted scope falls back to the configured default.
	rec = do(t, h, http.MethodPost, engine.PathMask, `{"texts":["x"],"correlation_id":"c"}`, testToken)
	require.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, []models.DataType{models.DataTypePERSONALDATA, models.DataTypeCUSTOM}, masker.last.DataTypes)
}

func TestRequestValidation(t *testing.T) {
	h := newHandler(t, &fakeMasker{}, newFakeStore())

	for _, tc := range []struct {
		name   string
		method string
		path   string
		body   string
		want   int
	}{
		{"mask without correlation_id", http.MethodPost, engine.PathMask, `{"texts":["x"]}`, http.StatusBadRequest},
		{"unmask without correlation_id", http.MethodPost, engine.PathUnmask, `{"texts":["x"]}`, http.StatusBadRequest},
		{"over-long correlation_id", http.MethodPost, engine.PathMask,
			`{"texts":["x"],"correlation_id":"` + strings.Repeat("a", 201) + `"}`, http.StatusBadRequest},
		{"malformed json", http.MethodPost, engine.PathMask, `{"texts":`, http.StatusBadRequest},
		{"empty body", http.MethodPost, engine.PathMask, ``, http.StatusBadRequest},
		{"method not allowed", http.MethodGet, engine.PathMask, ``, http.StatusMethodNotAllowed},
		{"unknown path", http.MethodPost, "/v1/nope", `{}`, http.StatusNotFound},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rec := do(t, h, tc.method, tc.path, tc.body, testToken)
			require.Equal(t, tc.want, rec.Code, rec.Body.String())
			assert.NotEmpty(t, decode[map[string]string](t, rec)["code"])
		})
	}
}

func TestUnknownFieldsAreTolerated(t *testing.T) {
	// The engine API is called by a generic gateway whose payload may carry more
	// than this contract defines; unknown fields must not fail the request.
	h := newHandler(t, &fakeMasker{resp: maskedResponse()}, newFakeStore())

	rec := do(t, h, http.MethodPost, engine.PathMask,
		`{"texts":["a@b.c"],"correlation_id":"c","tenant":"acme","trace":{"id":"t1"}}`, testToken)

	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
}

func TestBodySizeCap(t *testing.T) {
	h := newHandler(t, &fakeMasker{}, newFakeStore())

	rec := do(t, h, http.MethodPost, engine.PathMask,
		`{"texts":["`+strings.Repeat("x", 8192)+`"],"correlation_id":"c"}`, testToken)

	require.Equal(t, http.StatusRequestEntityTooLarge, rec.Code, rec.Body.String())
	assert.Equal(t, engine.CodeRequestTooLarge, decode[map[string]string](t, rec)["code"])
}

func TestMaskErrorIsReportedWithoutLeakingInput(t *testing.T) {
	h := newHandler(t, &fakeMasker{err: errors.New("boom")}, newFakeStore())

	rec := do(t, h, http.MethodPost, engine.PathMask,
		`{"texts":["secret@example.com"],"correlation_id":"c"}`, testToken)

	require.Equal(t, http.StatusInternalServerError, rec.Code)
	assert.NotContains(t, rec.Body.String(), "secret@example.com")
}
