// Package engine implements the engine-only API: a small, token-authenticated
// HTTP surface that exposes the masking engine to a caller that already owns
// authentication, routing, budgets and logging.
//
// It exists because the data-plane proxy is the wrong shape for a component
// user: a second proxy in front of (or behind) their gateway either stops their
// gateway from seeing the real text, or lands unmasked data in its logs — and it
// needs one instance per upstream. Instead, the caller's gateway asks this API
// to mask, forwards the masked text itself, and later asks it to unmask the
// response.
//
// The contract is documented and versioned in docs/api/engine.md and is
// deliberately separate from the management API (which is unauthenticated and
// carries the mutating rules/settings endpoints).
package engine

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/cloud-ru-tech/guardrails-llm-filter/internal/guardrails/demask"
	"github.com/cloud-ru-tech/guardrails-llm-filter/internal/logging"
	"github.com/cloud-ru-tech/guardrails-llm-filter/internal/models"
	"github.com/cloud-ru-tech/guardrails-llm-filter/internal/repository"
	maskuc "github.com/cloud-ru-tech/guardrails-llm-filter/internal/usecases/guardrails/mask"
	"github.com/cloud-ru-tech/guardrails-llm-filter/internal/version"
	"github.com/cloud-ru-tech/guardrails-llm-filter/pkg/guardrails/regex/rule"
)

// APIVersion is the engine API contract version. Additive changes keep it; a
// breaking change needs a new path prefix (see docs/api/engine.md).
const APIVersion = "v1"

// Routes on the engine listener.
const (
	PathMask    = "/v1/mask"
	PathUnmask  = "/v1/unmask"
	PathVersion = "/v1/engine/version"
)

// Error codes returned in the `code` field of an error body. They are part of
// the contract: a caller can branch on them without parsing messages.
const (
	CodeInvalidRequest       = "invalid_request"
	CodeUnauthorized         = "unauthorized"
	CodeUnknownCorrelationID = "unknown_correlation_id"
	CodeStoreUnavailable     = "store_unavailable"
	CodeRequestTooLarge      = "request_too_large"
)

// maxCorrelationIDLen bounds the caller-supplied state key: it is part of a
// store key, so an unbounded value would let a caller inflate the keyspace.
const maxCorrelationIDLen = 200

// Masker masks a batch of texts. *maskuc.UseCase satisfies it.
type Masker interface {
	Handle(ctx context.Context, cmd maskuc.Command) (maskuc.CommandResponse, error)
}

// StateStore persists masking state between /v1/mask and /v1/unmask.
// repository.MaskingStateStore satisfies it.
type StateStore interface {
	PutMaskingState(ctx context.Context, requestID string, st models.MaskingState) error
	GetMaskingState(ctx context.Context, requestID string) (models.MaskingState, error)
}

// DemaskerProvider builds a demasker factory from masking state.
// *demask.Provider satisfies it.
type DemaskerProvider interface {
	NewFactory(state models.MaskingState) *demask.Factory
}

// RuleResolver resolves rule IDs to their rules, so each placeholder can report
// the data type it belongs to (masking state stores only the rule ID).
// *registry.Reloadable satisfies it.
type RuleResolver interface {
	GetRulesByIDs(ruleIDs ...string) []rule.Rule
}

// Deps are the handler dependencies.
type Deps struct {
	// Masker is the production mask use case (same rules and placeholder policy
	// as the data plane). Required.
	Masker Masker
	// Demasker builds demaskers from persisted state. Required.
	Demasker DemaskerProvider
	// Rules resolves a placeholder's rule to its data type. Required.
	Rules RuleResolver
	// Store persists masking state. Required.
	Store StateStore
	// DefaultDataTypes is the scan scope when a request omits data_types.
	DefaultDataTypes []models.DataType
	// Token is the bearer token required on every request. Required (the
	// configuration layer refuses to start without it).
	Token string
	// MaxRequestBytes caps the request body, mirroring the data-plane limit.
	// 0 disables the cap.
	MaxRequestBytes int64
}

// Handler serves the engine API.
type Handler struct {
	deps Deps
}

// New creates the engine API handler.
func New(d Deps) *Handler { return &Handler{deps: d} }

// ServeHTTP routes the engine API. Unknown paths and methods get 404/405 with
// the same JSON error shape as everything else.
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	switch {
	case r.URL.Path == PathVersion && r.Method == http.MethodGet:
		// Unauthenticated on purpose: it advertises the contract version and the
		// build, never any data. A caller should be able to probe it to decide
		// whether this engine speaks a version it understands.
		writeJSON(w, http.StatusOK, versionResponse{
			APIVersion: APIVersion,
			Version:    version.Version,
			Commit:     version.Commit,
			Date:       version.Date,
		})
		return

	case r.URL.Path == PathMask || r.URL.Path == PathUnmask:
		if r.Method != http.MethodPost {
			w.Header().Set("Allow", http.MethodPost)
			writeError(w, http.StatusMethodNotAllowed, CodeInvalidRequest, "method not allowed")
			return
		}
	default:
		writeError(w, http.StatusNotFound, CodeInvalidRequest, "not found")
		return
	}

	if !h.authorized(r) {
		// No realm/challenge detail beyond the header name: the caller either
		// has the token or does not.
		w.Header().Set("WWW-Authenticate", "Bearer")
		writeError(w, http.StatusUnauthorized, CodeUnauthorized, "missing or invalid bearer token")
		return
	}

	if r.URL.Path == PathMask {
		h.handleMask(w, r)
		return
	}
	h.handleUnmask(w, r)
}

// authorized compares the presented bearer token in constant time.
func (h *Handler) authorized(r *http.Request) bool {
	const prefix = "Bearer "
	header := r.Header.Get("Authorization")
	if len(header) <= len(prefix) || !strings.EqualFold(header[:len(prefix)], prefix) {
		return false
	}
	presented := strings.TrimSpace(header[len(prefix):])
	return subtle.ConstantTimeCompare([]byte(presented), []byte(h.deps.Token)) == 1
}

// maskRequest is the POST /v1/mask body.
type maskRequest struct {
	Texts         []string       `json:"texts"`
	CorrelationID string         `json:"correlation_id"`
	DataTypes     dataTypesField `json:"data_types"`
}

// unmaskRequest is the POST /v1/unmask body.
type unmaskRequest struct {
	Texts         []string `json:"texts"`
	CorrelationID string   `json:"correlation_id"`
}

// placeholderInfo describes one masked value without revealing it. The original
// is deliberately absent: it is reachable only through /v1/unmask, so a mask
// response cannot be turned into a plaintext dump.
type placeholderInfo struct {
	Placeholder string `json:"placeholder"`
	RuleID      string `json:"rule_id"`
	DataType    string `json:"data_type"`
}

type maskResponse struct {
	CorrelationID      string            `json:"correlation_id"`
	MaskedTexts        []string          `json:"masked_texts"`
	Placeholders       []placeholderInfo `json:"placeholders"`
	TriggeredRuleIDs   []string          `json:"triggered_rule_ids"`
	TriggeredDataTypes []string          `json:"triggered_data_types"`
}

type unmaskResponse struct {
	CorrelationID string   `json:"correlation_id"`
	Texts         []string `json:"texts"`
}

type versionResponse struct {
	APIVersion string `json:"api_version"`
	Version    string `json:"version"`
	Commit     string `json:"commit"`
	Date       string `json:"date"`
}

type errorResponse struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

func (h *Handler) handleMask(w http.ResponseWriter, r *http.Request) {
	var req maskRequest
	if !h.decode(w, r, &req) {
		return
	}
	if err := validateCorrelationID(req.CorrelationID); err != nil {
		writeError(w, http.StatusBadRequest, CodeInvalidRequest, err.Error())
		return
	}

	dataTypes := req.DataTypes
	if len(dataTypes) == 0 {
		dataTypes = h.deps.DefaultDataTypes
	}

	resp, err := h.deps.Masker.Handle(r.Context(), maskuc.Command{
		DataTypes: dataTypes,
		Texts:     req.Texts,
	})
	if err != nil {
		logging.Error(r.Context(), "engine API: mask failed", err)
		writeError(w, http.StatusInternalServerError, CodeInvalidRequest, "masking failed")
		return
	}

	// The mask use case returns no texts when nothing triggered; echo the input
	// so the caller always gets exactly one text back per text sent.
	masked := resp.MaskedTexts
	if masked == nil {
		masked = req.Texts
	}

	// Persist the state under the caller's correlation id even when nothing was
	// masked, so a later /v1/unmask finds the id instead of reporting it unknown.
	// A failed write is fatal for this call: the caller would otherwise hold
	// masked text that can never be restored.
	if err := h.deps.Store.PutMaskingState(r.Context(), req.CorrelationID, resp.MaskingState); err != nil {
		logging.Error(r.Context(), "engine API: failed to persist masking state", err)
		writeError(w, http.StatusServiceUnavailable, CodeStoreUnavailable,
			"masking state could not be persisted; the request was not masked")
		return
	}

	writeJSON(w, http.StatusOK, maskResponse{
		CorrelationID:      req.CorrelationID,
		MaskedTexts:        masked,
		Placeholders:       h.placeholdersOf(resp.MaskingState.Replacements),
		TriggeredRuleIDs:   nonNilStrings(resp.MaskingState.TriggeredRuleIDs),
		TriggeredDataTypes: dataTypeNames(resp.MaskingState.TriggeredDataTypes),
	})
}

func (h *Handler) handleUnmask(w http.ResponseWriter, r *http.Request) {
	var req unmaskRequest
	if !h.decode(w, r, &req) {
		return
	}
	if err := validateCorrelationID(req.CorrelationID); err != nil {
		writeError(w, http.StatusBadRequest, CodeInvalidRequest, err.Error())
		return
	}

	state, err := h.deps.Store.GetMaskingState(r.Context(), req.CorrelationID)
	switch {
	case errors.Is(err, repository.ErrNotFound):
		// Fail closed with a distinct code instead of echoing the texts: a
		// caller that silently received placeholders back would ship them to its
		// own users.
		writeError(w, http.StatusNotFound, CodeUnknownCorrelationID,
			"no masking state for this correlation_id (unknown, already expired, or never masked)")
		return
	case err != nil:
		logging.Error(r.Context(), "engine API: failed to load masking state", err)
		writeError(w, http.StatusServiceUnavailable, CodeStoreUnavailable, "masking state is unavailable")
		return
	}

	factory := h.deps.Demasker.NewFactory(state)
	texts := make([]string, len(req.Texts))
	for i, text := range req.Texts {
		// flush=true: the whole text is available, so the demasker may emit
		// trailing text it would otherwise hold back for a streaming boundary.
		restored, derr := factory.Demasker().DemaskChunk(r.Context(), text, true)
		if derr != nil {
			// Leave the text untouched rather than half-restored; the caller sees
			// placeholders (safe) instead of a partially restored value.
			logging.Error(r.Context(), "engine API: demask failed", derr)
			texts[i] = text
			continue
		}
		texts[i] = restored
	}

	writeJSON(w, http.StatusOK, unmaskResponse{CorrelationID: req.CorrelationID, Texts: texts})
}

// decode reads and unmarshals the JSON body. It writes the error response
// itself and reports whether the caller may continue.
func (h *Handler) decode(w http.ResponseWriter, r *http.Request, dst any) bool {
	if h.deps.MaxRequestBytes > 0 {
		r.Body = http.MaxBytesReader(w, r.Body, h.deps.MaxRequestBytes)
	}
	err := json.NewDecoder(r.Body).Decode(dst)
	if err == nil {
		return true
	}

	var maxErr *http.MaxBytesError
	if errors.As(err, &maxErr) {
		writeError(w, http.StatusRequestEntityTooLarge, CodeRequestTooLarge, "request body too large")
		return false
	}
	if errors.Is(err, io.EOF) {
		writeError(w, http.StatusBadRequest, CodeInvalidRequest, "empty request body")
		return false
	}
	// Unknown fields are accepted on purpose: the engine API is called by a
	// generic gateway whose payload may carry more than this contract defines.
	writeError(w, http.StatusBadRequest, CodeInvalidRequest, "malformed JSON body")
	return false
}

// dataTypesField accepts `data_types` as an array of numbers or names, matching
// the flexibility of GUARDRAILS_DATA_TYPES.
type dataTypesField []models.DataType

func (d *dataTypesField) UnmarshalJSON(raw []byte) error {
	if string(raw) == "null" {
		return nil
	}
	var items []json.RawMessage
	if err := json.Unmarshal(raw, &items); err != nil {
		return fmt.Errorf("data_types must be an array of numbers or names: %w", err)
	}

	out := make([]models.DataType, 0, len(items))
	for _, item := range items {
		var asString string
		if err := json.Unmarshal(item, &asString); err == nil {
			parsed, err := parseDataTypeName(asString)
			if err != nil {
				return err
			}
			out = append(out, parsed)
			continue
		}
		var asNumber uint32
		if err := json.Unmarshal(item, &asNumber); err != nil {
			return fmt.Errorf("data_types entry %s must be a number or a name", item)
		}
		dt := models.DataType(asNumber)
		if !dt.IsValid() || dt == models.DataTypeUNSPECIFIED {
			return fmt.Errorf("unknown data type %d", asNumber)
		}
		out = append(out, dt)
	}
	*d = out
	return nil
}

// parseDataTypeName resolves a data-type name (case-insensitive) to its number.
// It reuses the models enum so the engine API cannot drift from the rest of the
// service.
func parseDataTypeName(name string) (models.DataType, error) {
	parsed, err := models.ParseDataType(strings.ToUpper(strings.TrimSpace(name)))
	if err != nil || parsed == models.DataTypeUNSPECIFIED {
		return models.DataTypeUNSPECIFIED, fmt.Errorf("unknown data type %q", name)
	}
	return parsed, nil
}

func validateCorrelationID(id string) error {
	if strings.TrimSpace(id) == "" {
		return errors.New("correlation_id is required: it ties /v1/mask to the later /v1/unmask")
	}
	if len(id) > maxCorrelationIDLen {
		return fmt.Errorf("correlation_id must be at most %d bytes", maxCorrelationIDLen)
	}
	return nil
}

// placeholdersOf lists each masked value's placeholder, rule and data type. The
// data type is resolved from the rule registry because masking state records
// only the rule ID; a rule deleted between masking and this call reports
// UNSPECIFIED rather than failing the response.
func (h *Handler) placeholdersOf(replacements []models.Replacement) []placeholderInfo {
	ruleIDs := make([]string, 0, len(replacements))
	for _, rep := range replacements {
		ruleIDs = append(ruleIDs, rep.RuleID)
	}
	dataTypeByRule := make(map[string]models.DataType, len(ruleIDs))
	for _, r := range h.deps.Rules.GetRulesByIDs(ruleIDs...) {
		dataTypeByRule[r.ID] = models.DataType(r.DataType)
	}

	out := make([]placeholderInfo, 0, len(replacements))
	for _, rep := range replacements {
		out = append(out, placeholderInfo{
			Placeholder: rep.Placeholder,
			RuleID:      rep.RuleID,
			DataType:    dataTypeByRule[rep.RuleID].String(),
		})
	}
	return out
}

func dataTypeNames(types []models.DataType) []string {
	out := make([]string, 0, len(types))
	for _, dt := range types {
		out = append(out, dt.String())
	}
	return out
}

// nonNilStrings keeps JSON arrays as [] rather than null for an empty result.
func nonNilStrings(in []string) []string {
	if in == nil {
		return []string{}
	}
	return in
}

func writeJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(payload)
}

func writeError(w http.ResponseWriter, status int, code, message string) {
	writeJSON(w, status, errorResponse{Code: code, Message: message})
}

// ReadTimeout and WriteTimeout for the engine listener. Masking is CPU-bound on
// the request and the store write is bounded by the store's own timeouts, so a
// generous but finite budget is enough.
const (
	ReadHeaderTimeout = 5 * time.Second
	ReadTimeout       = 30 * time.Second
	WriteTimeout      = 30 * time.Second
)
