#!/usr/bin/env bash
# Quickstart demo: shows masking on the request path and demasking on the
# response path. Run `docker compose up --build` in this directory first.
set -euo pipefail

GATEWAY=${GATEWAY:-http://localhost:8080}
API=${API:-http://localhost:9080}

bold() { printf '\n\033[1m%s\033[0m\n' "$*"; }

bold "1. Non-streaming request with an email and a card number in the prompt"
echo "   (the mock LLM echoes what it receives — check 'docker compose logs mock-llm'"
echo "    to see the MASKED text; the response below is already DEMASKED)"
curl -sS -i "$GATEWAY/v1/chat/completions" \
  -H 'Content-Type: application/json' \
  -d '{
    "model": "demo",
    "messages": [{"role": "user", "content": "My email is john.doe@example.com and my card is 4111 1111 1111 1111"}]
  }' | sed -n '1,20p'

bold "2. Same request, streaming (SSE demasking)"
curl -sSN "$GATEWAY/v1/chat/completions" \
  -H 'Content-Type: application/json' \
  -d '{
    "model": "demo",
    "stream": true,
    "messages": [{"role": "user", "content": "Reach me at jane@example.com"}]
  }' | head -20

bold "3. Add a custom rule via the configuration API"
curl -sS -X POST "$API/v1/rules" \
  -H 'Content-Type: application/json' \
  -d '{
    "rule_id": "acme_token",
    "name": "ACME internal token",
    "data_type": 6,
    "regex": "\\bacme-[0-9a-f]{8}\\b",
    "masking": {"placeholder": "ACME_TOKEN"}
  }' | head -5
echo

bold "4. Enable the CUSTOM data type globally"
curl -sS -X PUT "$API/v1/settings" \
  -H 'Content-Type: application/json' \
  -d '{"enabled": true, "data_types": [1,2,3,4,5,"custom"]}'
echo

bold "5. The custom rule now masks matching values"
curl -sS "$GATEWAY/v1/chat/completions" \
  -H 'Content-Type: application/json' \
  -d '{
    "model": "demo",
    "messages": [{"role": "user", "content": "here is my token acme-deadbeef"}]
  }'
echo
echo "(mock-llm logs show <ACME_TOKEN_1>; the response above shows the original)"

bold "6. OpenAI Responses API (/v1/responses), non-streaming"
curl -sS "$GATEWAY/v1/responses" \
  -H 'Content-Type: application/json' \
  -d '{
    "model": "demo",
    "input": "Ping me at john.doe@example.com"
  }'
echo

bold "7. Responses API, streaming (named-event SSE demasking)"
curl -sSN "$GATEWAY/v1/responses" \
  -H 'Content-Type: application/json' \
  -d '{
    "model": "demo",
    "stream": true,
    "input": "Reach me at jane@example.com"
  }' | head -20

bold "8. Audit trail: what was masked, per request"
echo "   (records carry rules/data types/placeholders — never the original values)"
curl -sS "$API/v1/audit/records?limit=5"
echo
