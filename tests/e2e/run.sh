#!/usr/bin/env bash
#
# Local e2e harness orchestrator for the standalone guardrails service.
#
#   ./run.sh up            build binaries + start upstream + guardrails
#   ./run.sh restart       rebuild + restart guardrails only (fix->retest loop)
#   ./run.sh seed          (re)create the custom rule + enable all data types
#   ./run.sh test          run the deterministic suites (base, expanded, tool, struct, config)
#   ./run.sh test-real     run the real-model suite (needs a model at :8881)
#   ./run.sh all           up + seed + test (+ test-real if :8881 is reachable)
#   ./run.sh status        show what is running
#   ./run.sh down          stop upstream + guardrails
#
# Topology (no Envoy):
#   driver -> guardrails(:8080) -> upstream(:8890, capture/echo/tool/proxy->:8881)
#
# Env overrides: REAL_MODEL_URL (default http://localhost:8881).
# The management API (:9080) is unauthenticated by design (protected at the
# network layer in real deployments) -- no token is needed or sent here.
set -euo pipefail

HARNESS="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO="$(cd "$HARNESS/../.." && pwd)"
REAL_MODEL_URL="${REAL_MODEL_URL:-http://localhost:8881}"
CAP="$HARNESS/capture/requests.jsonl"
API="http://localhost:9080"
GW="http://localhost:8080"
export GW API CAPTURE_FILE="$CAP"

mkdir -p "$HARNESS/capture" "$HARNESS/results"
bold(){ printf '\n\033[1m%s\033[0m\n' "$*"; }

build(){
  bold "Building guardrails + upstream"
  ( cd "$REPO" && make build >/dev/null )
  ( cd "$HARNESS/upstream" && go build -o "$HARNESS/upstream-bin" . )
  echo "  ok"
}

free_port(){ # kill whatever listens on the given TCP port (path-agnostic)
  local pids; pids="$(lsof -ti "tcp:$1" -sTCP:LISTEN 2>/dev/null || true)"
  [ -n "$pids" ] && kill $pids 2>/dev/null || true
}

start_upstream(){
  pkill -f 'upstream-bin' 2>/dev/null || true   # kill any prior upstream, whatever its path
  free_port 8890; sleep 1
  CAPTURE_FILE="$CAP" REAL_MODEL_URL="$REAL_MODEL_URL" LISTEN_ADDR=":8890" \
    nohup "$HARNESS/upstream-bin" > "$HARNESS/upstream.log" 2>&1 &
  sleep 1
  curl -fsS -m3 http://localhost:8890/healthz >/dev/null && echo "  upstream :8890 up (capture -> $CAP)"
}

start_guardrails(){
  pkill -f 'bin/guardrails-llm-filter' 2>/dev/null || true
  free_port 8080; sleep 1
  nohup bash "$HARNESS/start-guardrails.sh" > "$HARNESS/guardrails.log" 2>&1 &
  for _ in $(seq 1 20); do
    curl -fsS -m2 "$API/v1/settings" >/dev/null 2>&1 && { echo "  guardrails :8080/:9080 up"; return; }
    sleep 0.5
  done
  echo "  ERROR: guardrails did not become ready; see $HARNESS/guardrails.log"; exit 1
}

seed(){
  bold "Seeding custom rule + enabling all data types"
  curl -fsS -m10 -X POST "$API/v1/rules" -H 'Content-Type: application/json' \
    -d '{"rule_id":"test.quote_token","name":"Quote token","data_type":6,"regex":"QSECRET\\S+","masking":{"placeholder":"QSECRET"}}' \
    -o /dev/null -w "  create rule: HTTP %{http_code}\n" || true
  curl -fsS -m10 -X PUT "$API/v1/settings" -H 'Content-Type: application/json' \
    -d '{"enabled":true,"data_types":[1,2,3,4,5,6],"mode":"enforce"}' \
    -o /dev/null -w "  settings: HTTP %{http_code}\n" || true
}

gen(){
  python3 "$HARNESS/gen_base_plan.py" >/dev/null
  python3 "$HARNESS/gen_expanded.py" >/dev/null
}

run_suite(){ # name script args...
  local name="$1"; shift
  bold "[$name]"
  : > "$CAP"
  python3 "$@" || true
}

case "${1:-all}" in
  up)        build; start_upstream; start_guardrails; seed ;;
  restart)   build; start_guardrails; seed ;;
  seed)      seed ;;
  gen)       gen ;;
  test)
    seed; gen
    run_suite "base (1782)"      "$HARNESS/driver.py" "$HARNESS/plan_base.jsonl"     --out "$HARNESS/results/base.json"
    run_suite "expanded"         "$HARNESS/driver.py" "$HARNESS/plan_expanded.jsonl" --out "$HARNESS/results/expanded.json"
    run_suite "tool-call"        "$HARNESS/tool_test.py"
    run_suite "structured"       "$HARNESS/struct_test.py"
    run_suite "config/behavior"  "$HARNESS/config_test.py"
    run_suite "stream/limit edge" "$HARNESS/stream_edge_test.py"
    ;;
  test-real)
    run_suite "real-model"       "$HARNESS/real_model_test.py"
    ;;
  all)
    build; start_upstream; start_guardrails; seed; gen
    run_suite "base (1782)"      "$HARNESS/driver.py" "$HARNESS/plan_base.jsonl"     --out "$HARNESS/results/base.json"
    run_suite "expanded"         "$HARNESS/driver.py" "$HARNESS/plan_expanded.jsonl" --out "$HARNESS/results/expanded.json"
    run_suite "tool-call"        "$HARNESS/tool_test.py"
    run_suite "structured"       "$HARNESS/struct_test.py"
    run_suite "config/behavior"  "$HARNESS/config_test.py"
    run_suite "stream/limit edge" "$HARNESS/stream_edge_test.py"
    if curl -fsS -m3 "$REAL_MODEL_URL/v1/models" >/dev/null 2>&1; then
      run_suite "real-model"     "$HARNESS/real_model_test.py"
    else
      echo; echo "(skipping real-model suite: $REAL_MODEL_URL not reachable)"
    fi
    ;;
  status)
    bold "Status"
    for p in 8080 9080 9090 8890; do printf "  port %-5s: " "$p"; (lsof -nP -iTCP:$p -sTCP:LISTEN 2>/dev/null | tail -n +2 | awk '{print $1}' | head -1) || echo "-"; echo; done
    curl -fsS -m3 "$REAL_MODEL_URL/v1/models" >/dev/null 2>&1 && echo "  real model $REAL_MODEL_URL: reachable" || echo "  real model $REAL_MODEL_URL: NOT reachable"
    ;;
  down)
    bold "Stopping"
    pkill -f 'upstream-bin' 2>/dev/null || true
    pkill -f 'bin/guardrails-llm-filter' 2>/dev/null || true
    echo "  stopped"
    ;;
  *)
    grep '^#' "${BASH_SOURCE[0]}" | sed 's/^# \{0,1\}//' | head -24 ;;
esac
