#!/usr/bin/env python3
"""Turn the guardrails dataset into e2e test cases across all 3 endpoints x
{stream, non-stream}. Each dataset entry's `content` becomes a single user
message; the driver verifies mask (upstream) + demask (client round-trip)."""
import os
import json, sys

_H = os.path.dirname(os.path.abspath(__file__))
_REPO = os.path.dirname(os.path.dirname(_H))
DATASET = sys.argv[1] if len(sys.argv) > 1 else \
    os.path.join(_REPO, "tests", "dataset", "guardrails_dataset.jsonl")
OUT = sys.argv[2] if len(sys.argv) > 2 else os.path.join(_H, "plan_base.jsonl")

def body_for(endpoint, content, stream):
    if endpoint == "chat":
        return {"model": "demo", "stream": stream, "messages": [{"role": "user", "content": content}]}
    if endpoint == "messages":
        return {"model": "demo", "stream": stream, "max_tokens": 256, "messages": [{"role": "user", "content": content}]}
    if endpoint == "responses":
        return {"model": "demo", "stream": stream, "input": content}

entries = []
with open(DATASET, encoding="utf-8") as f:
    for line in f:
        line = line.strip()
        if line:
            entries.append(json.loads(line))

n = 0
with open(OUT, "w", encoding="utf-8") as out:
    for e in entries:
        content = e["content"]
        eid = e["id"]
        exp = e.get("expected_placeholders", [])
        for endpoint in ("chat", "messages", "responses"):
            for stream in (False, True):
                case = {
                    "id": f"{eid}|{endpoint}|{'s' if stream else 'n'}",
                    "endpoint": endpoint,
                    "stream": stream,
                    "body": body_for(endpoint, content, stream),
                    "expected_placeholders": exp,
                    "category": e.get("category"),
                    "expect_masked": True,
                }
                out.write(json.dumps(case, ensure_ascii=False) + "\n")
                n += 1
print(f"wrote {n} cases from {len(entries)} entries to {OUT}")
