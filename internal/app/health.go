package app

import (
	"net/http"

	"github.com/cloud-ru-tech/guardrails-llm-filter/internal/health"
)

// livenessHandler reports process liveness for the /healthz probe.
func livenessHandler(w http.ResponseWriter, _ *http.Request) {
	writeHealth(w, health.GetLiveness())
}

// readinessHandler reports readiness for the /readyz probe. Readiness is
// process-liveness once the servers are up; the gateway does not proactively
// probe the upstream (a per-request forward failure is surfaced as 502
// instead).
func readinessHandler(w http.ResponseWriter, _ *http.Request) {
	writeHealth(w, health.GetReadiness())
}

func writeHealth(w http.ResponseWriter, ok bool) {
	if ok {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
		return
	}
	w.WriteHeader(http.StatusServiceUnavailable)
	_, _ = w.Write([]byte("unavailable"))
}
