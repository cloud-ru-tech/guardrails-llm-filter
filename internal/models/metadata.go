package models

// Metadata carries per-request context extracted from Envoy headers.
type Metadata struct {
	// RequestID is the x-request-id header value (or a generated fallback);
	// it is the audit-record identity and tags log lines. It is NOT used as
	// the masking-state store key — see StateKey.
	RequestID string
	// StateKey keys the masking state in external stores. It is derived from
	// RequestID (HMAC-SHA256(salt, request_id)) so a client-supplied or
	// predictable x-request-id cannot be used to guess or collide with
	// another request's stored state.
	StateKey    string
	Model       string
	IsStreaming bool
	Path        string
	// Mode is the guardrails mode this request was processed under
	// (resolved once in the request-headers phase).
	Mode GuardrailsMode
	// Format is the API wire format resolved from Path in the
	// request-headers phase; it drives extraction and demask dispatch.
	Format APIFormat
}
