// Package gateway is the standalone data-plane HTTP handler. Clients point at
// it directly (on Config.ListenAddr) instead of the upstream LLM provider: for
// a guarded request path it masks PII/secrets in the request body, forwards the
// masked request to the configured upstream itself, then demasks the upstream
// response — full-body or token-by-token SSE — before returning it to the
// client. There is no Envoy hop.
//
// One ServeHTTP call performs mask→forward→demask on a single replica, so the
// masking state lives in-process for the request lifetime and no masking-state
// store round-trip is needed on the data path.
//
// The verdict model is mask/pass — never block — and the whole path is
// fail-open: any internal error forwards/relays traffic unchanged rather than
// breaking it. A guarded request whose body cannot be masked (parse failure,
// mask error, detect mode, no findings) is forwarded verbatim and its response
// relayed verbatim.
package gateway

import (
	"context"
	"crypto/tls"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/tidwall/gjson"

	"github.com/cloud-ru-tech/guardrails-llm-filter/internal/config"
	"github.com/cloud-ru-tech/guardrails-llm-filter/internal/guardrails/demask"
	"github.com/cloud-ru-tech/guardrails-llm-filter/internal/metrics"
	"github.com/cloud-ru-tech/guardrails-llm-filter/internal/models"
	"github.com/cloud-ru-tech/guardrails-llm-filter/internal/service/settings"
	"github.com/cloud-ru-tech/guardrails-llm-filter/internal/usecases/guardrails/mask"
)

const (
	sseContentType    = "text/event-stream"
	contentTypeHeader = "Content-Type"
	requestIDHeader   = "X-Request-Id"
)

// Masker masks request texts using guardrails regex rules.
type Masker interface {
	Handle(ctx context.Context, cmd mask.Command) (mask.CommandResponse, error)
}

// SettingsProvider supplies the current global guardrails settings.
type SettingsProvider interface {
	Global() models.GuardrailsSettings
}

// DemaskerProvider creates request-scoped demasker factories from the
// per-request masking state.
type DemaskerProvider interface {
	NewFactory(state models.MaskingState) *demask.Factory
}

// AuditRecorder persists per-request masking audit entries. Implementations
// must be non-blocking and fail-open (see internal/service/audit). nil when
// the audit trail is disabled.
type AuditRecorder interface {
	Record(md models.Metadata, st models.MaskingState, maskedTexts []string)
	// RecordResponse enriches an existing record (by request ID) with the
	// masked model response texts; best-effort, gated, no-op if absent.
	RecordResponse(requestID string, maskedResponseTexts []string)
}

// Handler is the standalone gateway http.Handler.
type Handler struct {
	cfg              *config.Config
	masker           Masker
	settingsProvider SettingsProvider
	demaskerProvider DemaskerProvider
	audit            AuditRecorder // nil disables the audit trail
	pathResolver     *models.PathResolver

	client       *http.Client
	upstreamBase *url.URL            // nil when GUARDRAILS_UPSTREAM_BASE_URL is unset
	pathBaseURLs map[string]*url.URL // per-path base URL overrides
}

// New builds a gateway Handler. audit may be nil (audit trail disabled). The
// path map (GUARDRAILS_PATHS) and upstream URLs are validated here; invalid
// input is a boot-time error.
func New(
	cfg *config.Config,
	masker Masker,
	settingsProvider SettingsProvider,
	demaskerProvider DemaskerProvider,
	audit AuditRecorder,
) (*Handler, error) {
	resolver, err := models.NewPathResolver(cfg.Guardrails.Paths)
	if err != nil {
		return nil, fmt.Errorf("build path resolver: %w", err)
	}

	var base *url.URL
	if cfg.Upstream.BaseURL != "" {
		base, err = url.Parse(cfg.Upstream.BaseURL)
		if err != nil {
			return nil, fmt.Errorf("parse upstream base URL: %w", err)
		}
	}

	pathBases := make(map[string]*url.URL, len(cfg.Upstream.PathBaseURLs))
	for path, raw := range cfg.Upstream.PathBaseURLs {
		u, perr := url.Parse(raw)
		if perr != nil {
			return nil, fmt.Errorf("parse upstream override URL for %q: %w", path, perr)
		}
		pathBases[path] = u
	}

	transport := &http.Transport{
		Proxy: http.ProxyFromEnvironment,
		DialContext: (&net.Dialer{
			Timeout:   30 * time.Second,
			KeepAlive: 30 * time.Second,
		}).DialContext,
		ForceAttemptHTTP2:     true,
		MaxIdleConns:          cfg.Upstream.MaxIdleConns,
		MaxIdleConnsPerHost:   cfg.Upstream.MaxIdleConnsPerHost,
		IdleConnTimeout:       cfg.Upstream.IdleConnTimeout,
		TLSHandshakeTimeout:   10 * time.Second,
		ExpectContinueTimeout: 1 * time.Second,
		// Time to first response byte. Deliberately not a whole-request timeout:
		// a streaming (SSE) response must be able to run past it; its lifetime is
		// governed by the client request context instead (see forward).
		ResponseHeaderTimeout: cfg.Upstream.Timeout,
		//nolint:gosec // InsecureSkipVerify is an explicit, documented opt-in for local testing.
		TLSClientConfig: &tls.Config{InsecureSkipVerify: cfg.Upstream.InsecureSkipVerify},
	}

	return &Handler{
		cfg:              cfg,
		masker:           masker,
		settingsProvider: settingsProvider,
		demaskerProvider: demaskerProvider,
		audit:            audit,
		pathResolver:     resolver,
		client:           &http.Client{Transport: transport},
		upstreamBase:     base,
		pathBaseURLs:     pathBases,
	}, nil
}

// ServeHTTP masks a guarded request, forwards it to the upstream, and demasks
// the response back to the client.
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	format, guarded := h.pathResolver.Resolve(r.URL.Path)
	eff := h.effectiveSettings(r)

	// Decide whether this request is a candidate for masking. Everything else
	// (unguarded path, non-body method, disabled) is a transparent passthrough.
	maskable := guarded && requestHasBody(r.Method) && eff.Enabled && len(eff.DataTypes) > 0

	body, ok := readBody(ctx, w, r, h.cfg.MaxRequestBytes)
	if !ok {
		return
	}

	// The client's streaming intent lives in the top-level "stream" boolean of
	// the request body — the same field for all three supported formats. It
	// drives the audit IsStreaming flag and lets the response phase treat an
	// upstream stream as SSE even when the upstream mislabels its Content-Type.
	streamRequested := gjson.GetBytes(body, "stream").Bool()

	outBody := body
	var factory *demask.Factory // non-nil ⇒ demask the response
	var requestID string        // keys the audit record for response-phase enrichment

	switch {
	case maskable && len(body) > 0:
		masked, state, rid, demaskResp := h.maskRequest(ctx, r, body, format, eff, streamRequested)
		if demaskResp {
			outBody = masked
			requestID = rid
			factory = h.demaskerProvider.NewFactory(state)
		}
	case !guarded:
		metrics.IncUnguardedPathPassthrough()
	}

	h.forward(ctx, w, r, outBody, factory, format, streamRequested, requestID)
}

// effectiveSettings resolves the global policy, optionally narrowed by the
// trusted override header. No network I/O.
func (h *Handler) effectiveSettings(r *http.Request) models.EffectiveSettings {
	var overrideValue string
	if h.cfg.Guardrails.OverrideHeader != "" {
		overrideValue = r.Header.Get(h.cfg.Guardrails.OverrideHeader)
	}
	return settings.Effective(h.settingsProvider.Global(), overrideValue)
}

// upstreamURL resolves the outbound URL: the per-path override base if set,
// else the global base, with the original request path and query appended.
// Returns nil when no upstream base is configured for the request.
func (h *Handler) upstreamURL(r *http.Request) *url.URL {
	base := h.upstreamBase
	if override, ok := h.pathBaseURLs[r.URL.Path]; ok {
		base = override
	}
	if base == nil {
		return nil
	}
	out := *base
	out.Path = joinPaths(base.Path, r.URL.Path)
	out.RawQuery = r.URL.RawQuery
	return &out
}

// joinPaths appends the request path to an optional upstream base path prefix
// without introducing or dropping slashes.
func joinPaths(basePath, reqPath string) string {
	basePath = strings.TrimSuffix(basePath, "/")
	if basePath == "" {
		return reqPath
	}
	if !strings.HasPrefix(reqPath, "/") {
		return basePath + "/" + reqPath
	}
	return basePath + reqPath
}

// requestHasBody reports whether a request method carries a body worth masking.
func requestHasBody(method string) bool {
	switch method {
	case http.MethodPost, http.MethodPut, http.MethodPatch:
		return true
	default:
		return false
	}
}
