// Package config holds the env-driven service configuration.
// All variables share the GUARDRAILS_ prefix (e.g. GUARDRAILS_LISTEN_ADDR).
package config

import (
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/caarlos0/env/v11"

	"github.com/cloud-ru-tech/guardrails-llm-filter/internal/models"
)

// EnvPrefix is prepended to every environment variable name.
const EnvPrefix = "GUARDRAILS_"

// DefaultGuardrailPaths are the built-in request paths always guarded.
// GUARDRAILS_PATHS entries are merged on top of these (a user entry for the
// same path wins); core paths cannot be silently dropped by a partial
// override. See Guardrails.Paths.
var DefaultGuardrailPaths = map[string]string{
	"/v1/chat/completions": "chat_completions",
	"/v1/messages":         "messages",
	"/v1/responses":        "responses",
}

type Config struct {
	// Logging
	LogLevel  string `env:"LOG_LEVEL" envDefault:"info"`  // debug|info|warn|error
	LogFormat string `env:"LOG_FORMAT" envDefault:"json"` // json|text

	// Servers
	MetricsPort int `env:"METRICS_PORT" envDefault:"9090"`

	// ListenAddr is the data-plane HTTP listen address clients point at — this
	// is what callers hit instead of the upstream LLM provider directly.
	ListenAddr string `env:"LISTEN_ADDR" envDefault:":8080"`

	// MaxRequestBytes caps the client request body the data-plane will read into
	// memory before masking, protecting against a memory-exhausting body on this
	// public-facing listener. Over-limit requests get 413. The default (32 MiB)
	// is generous enough for typical multimodal/base64 payloads; raise it for
	// vision-heavy workloads, or set 0 to disable the cap entirely.
	MaxRequestBytes int64 `env:"MAX_REQUEST_BYTES" envDefault:"33554432"`

	// GRPCAddr is the management gRPC listen address. The REST management API
	// (config.API.Addr) is served from the same contract via grpc-gateway,
	// which proxies to this address.
	GRPCAddr string `env:"GRPC_ADDR" envDefault:":9000"`

	// GrpcSecure serves the management gRPC listener over TLS using a
	// self-signed certificate. Off by default: the management API is meant to
	// run cluster-internal behind a network boundary.
	GrpcSecure bool `env:"GRPC_SECURE" envDefault:"false"`

	GuardrailsRules   GuardrailsRulesCfg `envPrefix:"RULES_"`
	GuardrailsHeaders GuardrailsHeaders  `envPrefix:"HEADERS_"`

	Guardrails Guardrails `envPrefix:""`
	Store      Store      `envPrefix:"STORE_"`
	API        API        `envPrefix:"API_"`
	UI         UI         `envPrefix:"UI_"`
	Audit      Audit      `envPrefix:"AUDIT_"`
	Upstream   Upstream   `envPrefix:"UPSTREAM_"`
}

// Upstream configures the LLM provider this service forwards masked requests
// to. In standalone mode the service is the data-plane proxy: it dials the
// upstream itself (no Envoy hop), so the request the model receives is masked
// and the response the client receives is demasked.
type Upstream struct {
	// BaseURL is the scheme://host[:port] of the upstream LLM provider; the
	// guarded request path is appended to it. Required once the gateway is the
	// data-plane server; validated (must be an absolute http/https URL) when
	// set. A per-path override in PathBaseURLs takes precedence.
	BaseURL string `env:"BASE_URL"`

	// Timeout bounds how long the gateway waits for the upstream response
	// headers (time to first byte). It deliberately does NOT bound the whole
	// streaming (SSE) response, whose lifetime is governed by the client
	// request context instead, so long streams are not severed mid-flight.
	// 0 disables the header timeout.
	Timeout time.Duration `env:"TIMEOUT" envDefault:"120s"`

	// MaxIdleConns / MaxIdleConnsPerHost / IdleConnTimeout tune the shared
	// upstream HTTP connection pool (http.Transport keep-alive reuse).
	MaxIdleConns        int           `env:"MAX_IDLE_CONNS" envDefault:"100"`
	MaxIdleConnsPerHost int           `env:"MAX_IDLE_CONNS_PER_HOST" envDefault:"100"`
	IdleConnTimeout     time.Duration `env:"IDLE_CONN_TIMEOUT" envDefault:"90s"`

	// PathBaseURLs optionally overrides BaseURL per guarded request path, as
	// comma-separated path=url pairs (the separator is '=' because URLs contain
	// ':'), e.g. "/v1/messages=https://api.anthropic.com,/v1/responses=https://api.openai.com".
	// A path not listed falls back to BaseURL.
	PathBaseURLs map[string]string `env:"PATH_BASE_URLS" envKeyValSeparator:"="`

	// InsecureSkipVerify disables upstream TLS certificate verification.
	// SECURITY: the response this service reads back is demasked to the original
	// secrets, so a MITM on an unverified TLS channel sees them. Only for local
	// testing against self-signed upstreams; never true in production.
	InsecureSkipVerify bool `env:"INSECURE_SKIP_VERIFY" envDefault:"false"`
}

// Audit StoreOriginalTexts modes.
const (
	OriginalsOff       = "off"
	OriginalsPlain     = "plain"
	OriginalsEncrypted = "encrypted"
)

// Audit configures the per-request masking audit trail. Records describe
// what was masked (rules, data types, placeholders); original sensitive
// values are stored only when StoreOriginalTexts opts in (default off).
type Audit struct {
	// Enabled turns on audit-record writing and the /v1/audit API endpoints.
	Enabled bool `env:"ENABLED" envDefault:"false"`

	// StoreMaskedTexts additionally persists the masked (placeholder-
	// substituted) request texts in each record. Still no originals, but
	// prompts are user content: enable only with an access-controlled store
	// and a non-empty API token.
	StoreMaskedTexts bool `env:"STORE_MASKED_TEXTS" envDefault:"false"`

	// StoreMaskedResponseTexts additionally persists the masked (placeholder-
	// substituted) model response texts in each record — the response
	// counterpart of StoreMaskedTexts. Off by default; same sensitivity class.
	StoreMaskedResponseTexts bool `env:"STORE_MASKED_RESPONSE_TEXTS" envDefault:"false"`

	// StoreOriginalTexts controls whether each audit replacement carries the
	// raw pre-masking value behind its placeholder (for the UI "reveal on
	// hover" feature). One of:
	//   - "off"       (default) — never store originals; the invariant holds.
	//   - "plain"     — store originals unencrypted in the record.
	//   - "encrypted" — store originals encrypted with the store AES-256-GCM
	//                   key (GUARDRAILS_STORE_ENCRYPTION_*); requires encryption
	//                   to be enabled or startup fails.
	// SECURITY: "plain" and "encrypted" persist raw sensitive values; use only
	// with an access-controlled store.
	StoreOriginalTexts string `env:"STORE_ORIGINAL_TEXTS" envDefault:"off"`

	// Retention is how long audit records are kept in the repository.
	Retention time.Duration `env:"RETENTION" envDefault:"24h"`

	// MaxEntries caps the audit map of the in_memory backend (oldest record
	// evicted first); ignored by redis/postgres. 0 = unlimited.
	MaxEntries int `env:"MAX_ENTRIES" envDefault:"10000"`
}

// Guardrails is the global policy applied to all traffic. It seeds the
// settings store on first start; afterwards the store (mutable via the
// configuration API) is the source of truth.
type Guardrails struct {
	Enabled bool `env:"ENABLED" envDefault:"true"`

	// Mode selects enforcement: "enforce" masks traffic, "detect" (shadow
	// mode) only scans and records metrics/audit without mutating bodies.
	Mode string `env:"MODE" envDefault:"enforce"`

	// DataTypes is a comma-separated list of enabled data types, by number
	// or name (e.g. "1,2,3" or "credentials,personal_data"). 6 (CUSTOM) is
	// included so custom rules created via the API — which the docs and
	// /v1/data-types steer toward data_type=CUSTOM — actually scan by default;
	// omitting it silently disables every CUSTOM rule regardless of its own
	// enabled flag. No built-in rule uses CUSTOM, so it is inert until a custom
	// rule exists.
	DataTypes string `env:"DATA_TYPES" envDefault:"1,2,3,4,5,6"`

	// KeywordPrefilterEnabled turns on the keyword pre-filter: a rule that
	// declares keywords runs its regex only when at least one keyword is
	// present in the text (case-insensitive). Off by default — enabling it
	// trades detection recall for scan speed and only helps rules whose
	// keyword lists are accurate.
	KeywordPrefilterEnabled bool `env:"KEYWORD_PREFILTER_ENABLED" envDefault:"false"`

	// MaskParallelMinBytes is the combined request-text size (in bytes) at or
	// above which the masking scan fans out across text fields; smaller bodies
	// are scanned sequentially, avoiding goroutine overhead on the latency-
	// sensitive small-request hot path. A request must also carry at least two
	// text fields to parallelize. 0 falls back to the built-in default.
	MaskParallelMinBytes int `env:"MASK_PARALLEL_MIN_BYTES" envDefault:"8192"`

	// Paths maps request paths to API wire formats as comma-separated
	// path:format pairs. Matching is exact first, then longest suffix, so
	// proxy-prefixed mounts (/openai/v1/chat/completions) work without
	// configuration. Entries are MERGED on top of DefaultGuardrailPaths (a
	// user entry for the same path wins), so a partial GUARDRAILS_PATHS can
	// never silently disable masking for a core endpoint. Validated at boot.
	Paths map[string]string `env:"PATHS"`

	// OverrideHeader names the trusted request header that narrows the
	// enabled data types per request. Empty disables the override. The gateway
	// consumes this header and does not forward it upstream; if the service is
	// exposed to untrusted clients, a fronting gateway should strip it so
	// callers cannot narrow their own masking scope.
	OverrideHeader string `env:"OVERRIDE_HEADER" envDefault:"x-guardrails-data-types"`

	// Refresh intervals converge replicas on API changes when the store
	// backend is shared (redis/postgres). 0 disables refreshing.
	SettingsRefreshInterval time.Duration `env:"SETTINGS_REFRESH_INTERVAL" envDefault:"30s"`
	RulesRefreshInterval    time.Duration `env:"RULES_REFRESH_INTERVAL" envDefault:"30s"`
}

// API configures the HTTP configuration API.
type API struct {
	// Addr is the listen address; empty disables the API server.
	//
	// The API is unauthenticated: it exposes mutating endpoints (rules,
	// settings) with no token check, so it must be protected at the network
	// layer (cluster-internal only, never public ingress).
	Addr string `env:"ADDR" envDefault:":9080"`
}

// UI configures serving of the embedded management console (a static SPA) from
// the management API origin (config.API.Addr).
type UI struct {
	// Enabled serves the embedded console at "/" on the management API port,
	// alongside the "/v1/*" API. Default on. Serving still requires a console
	// build to be embedded in the binary (see the frontend package); when a
	// data-plane-only binary was built without the UI, this is a no-op. Turn it
	// off to expose only the API on that port.
	//
	// The console shares the management API's trust boundary: that port is
	// unauthenticated and must stay cluster-internal (never public ingress).
	Enabled bool `env:"ENABLED" envDefault:"true"`
}

// Store selects and configures the persistence backend for masking state,
// custom rules and global settings.
type Store struct {
	// Backend is one of: in_memory (default), redis, postgres.
	Backend string `env:"BACKEND" envDefault:"in_memory"`

	// MaskingTTL is the safety-net TTL for masking-state entries; it must
	// exceed the longest expected streaming response.
	MaskingTTL time.Duration `env:"MASKING_TTL" envDefault:"15m"`

	Redis       StoreRedis `envPrefix:"REDIS_"`
	PostgresDSN string     `env:"POSTGRES_DSN"`

	// EncryptionEnabled turns on AES-256-GCM encryption of masking state at
	// rest for the external backends (redis, postgres); no-op for in_memory.
	EncryptionEnabled bool `env:"ENCRYPTION_ENABLED" envDefault:"false"`

	// EncryptionKey is the standard base64 encoding of a 32-byte key
	// (`openssl rand -base64 32`). Required when EncryptionEnabled; a missing
	// or malformed key fails startup. SECURITY: never log this value.
	EncryptionKey string `env:"ENCRYPTION_KEY"`
}

// StoreRedis holds connection parameters for the redis store backend.
type StoreRedis struct {
	Addr     string `env:"ADDR" envDefault:"redis:6379"`
	Password string `env:"PASSWORD"`
	DB       int    `env:"DB" envDefault:"0"`
}

type GuardrailsHeaders struct {
	DataTypesHeader      string `env:"DATA_TYPES_HEADER" envDefault:"x-guardrails-data-types-triggered"`
	TriggeredRulesHeader string `env:"TRIGGERED_RULES_HEADER" envDefault:"x-guardrails-triggered-rules"`

	// ExposeTriggeredRules controls whether the triggered-rules header is
	// added to responses. Rule IDs reveal which detectors fired, so exposing
	// them to end clients is opt-in.
	ExposeTriggeredRules bool `env:"EXPOSE_TRIGGERED_RULES" envDefault:"false"`
}

type GuardrailsRulesCfg struct {
	RegexRulesFile         string `env:"REGEX_RULES_FILE" envDefault:"./configs/guardrails_regex_rules.yaml"`
	GitleaksRegexRulesFile string `env:"GITLEAKS_REGEX_RULES_FILE" envDefault:"./configs/guardrails_regex_rules.gitleaks.generated.yaml"`

	// MaxCustom bounds the number of custom rules that can be created via the
	// configuration API. Each rule is evaluated against every request on the
	// hot path, so an unbounded count degrades latency/memory. 0 disables the
	// limit.
	MaxCustom int `env:"MAX_CUSTOM" envDefault:"500"`
	// MaxPatternLen bounds a custom rule's regex length. RE2 has no
	// backtracking, but a very long pattern still costs linearly per request.
	// 0 disables the limit.
	MaxPatternLen int `env:"MAX_PATTERN_LEN" envDefault:"4096"`
}

func Load() (*Config, error) {
	cfg := &Config{}
	if err := env.ParseWithOptions(cfg, env.Options{Prefix: EnvPrefix}); err != nil {
		return nil, err
	}
	// Merge the built-in core paths under any user overrides so a partial
	// GUARDRAILS_PATHS can never silently disable masking for a core endpoint.
	if cfg.Guardrails.Paths == nil {
		cfg.Guardrails.Paths = make(map[string]string, len(DefaultGuardrailPaths))
	}
	for path, format := range DefaultGuardrailPaths {
		if _, ok := cfg.Guardrails.Paths[path]; !ok {
			cfg.Guardrails.Paths[path] = format
		}
	}
	// Bad path config must fail the boot, not silently reject traffic.
	if _, err := models.NewPathResolver(cfg.Guardrails.Paths); err != nil {
		return nil, fmt.Errorf("parse %sPATHS: %w", EnvPrefix, err)
	}
	// A negative parallel-scan gate is meaningless and would be silently coerced
	// to the built-in default downstream, masking operator intent. 0 keeps the
	// built-in default; reject anything below it at boot.
	if cfg.Guardrails.MaskParallelMinBytes < 0 {
		return nil, fmt.Errorf("%sMASK_PARALLEL_MIN_BYTES must be >= 0, got %d", EnvPrefix, cfg.Guardrails.MaskParallelMinBytes)
	}
	// Header lookups are case-insensitive (net/http canonicalizes header
	// names). Normalize the configured override header name so a mixed-case
	// value still matches instead of silently never firing.
	cfg.Guardrails.OverrideHeader = strings.ToLower(strings.TrimSpace(cfg.Guardrails.OverrideHeader))
	// The audit originals mode is a small enum; reject unknown values at boot
	// rather than silently treating them as "off". "encrypted" additionally
	// requires store encryption to be configured — fail closed so an operator
	// cannot accidentally persist raw sensitive values in plaintext.
	switch cfg.Audit.StoreOriginalTexts {
	case OriginalsOff, OriginalsPlain:
	case OriginalsEncrypted:
		if !cfg.Store.EncryptionEnabled {
			return nil, fmt.Errorf("%sAUDIT_STORE_ORIGINAL_TEXTS=%q requires %sSTORE_ENCRYPTION_ENABLED=true with a valid %sSTORE_ENCRYPTION_KEY",
				EnvPrefix, OriginalsEncrypted, EnvPrefix, EnvPrefix)
		}
	default:
		return nil, fmt.Errorf("%sAUDIT_STORE_ORIGINAL_TEXTS must be one of %q, %q, %q; got %q",
			EnvPrefix, OriginalsOff, OriginalsPlain, OriginalsEncrypted, cfg.Audit.StoreOriginalTexts)
	}
	// A malformed upstream URL must fail boot rather than surface as a per-
	// request forward error. BaseURL is optional here (it becomes required once
	// the gateway is the data-plane server); validate whatever is set.
	if err := validateUpstreamURL(cfg.Upstream.BaseURL, EnvPrefix+"UPSTREAM_BASE_URL"); err != nil {
		return nil, err
	}
	for path, raw := range cfg.Upstream.PathBaseURLs {
		if err := validateUpstreamURL(raw, fmt.Sprintf("%sUPSTREAM_PATH_BASE_URLS[%s]", EnvPrefix, path)); err != nil {
			return nil, err
		}
	}
	return cfg, nil
}

// validateUpstreamURL rejects a non-empty upstream URL that is not an absolute
// http/https URL with a host. Empty is allowed (caller decides if required).
func validateUpstreamURL(raw, name string) error {
	if raw == "" {
		return nil
	}
	u, err := url.Parse(raw)
	if err != nil {
		return fmt.Errorf("parse %s: %w", name, err)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return fmt.Errorf("%s must be an http(s) URL, got %q", name, raw)
	}
	if u.Host == "" {
		return fmt.Errorf("%s must include a host, got %q", name, raw)
	}
	return nil
}
