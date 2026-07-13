#!/usr/bin/env bash
# Start the standalone guardrails service for local e2e testing. Runs from the
# repo root so it finds ./configs/*.yaml. Config is via GUARDRAILS_* env
# (overridable). No Envoy: guardrails is the data plane and forwards masked
# requests to the upstream capture layer itself.
set -euo pipefail
HARNESS="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO="$(cd "$HARNESS/../.." && pwd)"
cd "$REPO"

export GUARDRAILS_LOG_LEVEL="${GUARDRAILS_LOG_LEVEL:-debug}"
export GUARDRAILS_LOG_FORMAT="${GUARDRAILS_LOG_FORMAT:-text}"
export GUARDRAILS_STORE_BACKEND="${GUARDRAILS_STORE_BACKEND:-in_memory}"
export GUARDRAILS_AUDIT_ENABLED="${GUARDRAILS_AUDIT_ENABLED:-true}"
export GUARDRAILS_AUDIT_STORE_MASKED_TEXTS="${GUARDRAILS_AUDIT_STORE_MASKED_TEXTS:-true}"
export GUARDRAILS_HEADERS_EXPOSE_TRIGGERED_RULES="${GUARDRAILS_HEADERS_EXPOSE_TRIGGERED_RULES:-true}"

# Data plane: clients hit :8080; masked requests are forwarded to the upstream
# capture layer (:8890), which either echoes (round-trip oracle) or proxies to
# the real model at :8881 (X-Upstream-Mode: proxy).
export GUARDRAILS_LISTEN_ADDR="${GUARDRAILS_LISTEN_ADDR:-:8080}"
export GUARDRAILS_UPSTREAM_BASE_URL="${GUARDRAILS_UPSTREAM_BASE_URL:-http://localhost:8890}"

exec ./bin/guardrails-llm-filter
