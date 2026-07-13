#!/usr/bin/env python3
"""Edge-case coverage for the two behaviors introduced by commit 87efa9a
("fix sse"): (1) the gateway derives streaming intent from the request body's
"stream" boolean (gjson), so a response still gets demasked frame-by-frame
even when the upstream mislabels its Content-Type (not text/event-stream);
(2) GUARDRAILS_MAX_REQUEST_BYTES rejects an over-limit request body with 413
before it ever reaches the masker or upstream."""
import json, os, re, subprocess, sys

GW = os.environ.get("GW", "http://localhost:8080")
PATHS = {"chat": "/v1/chat/completions", "responses": "/v1/responses", "messages": "/v1/messages"}
PLACEHOLDER_RE = re.compile(r"<[A-Z][A-Z0-9]*(?:_[A-Z0-9]+)*_\d+>")
EMAIL = "mislabel@example.com"

results = []
def check(name, ok, note=""):
    results.append((name, ok, note))
    print(f"  {'PASS' if ok else 'FAIL'} {name}" + (f"  {note}" if note and not ok else ""))

def body_for(endpoint, text):
    if endpoint == "chat":
        return {"model": "demo", "stream": True, "messages": [{"role": "user", "content": text}]}
    if endpoint == "messages":
        return {"model": "demo", "stream": True, "max_tokens": 128, "messages": [{"role": "user", "content": text}]}
    if endpoint == "responses":
        return {"model": "demo", "stream": True, "input": text}

def send(endpoint, body, tid, chunk=1):
    cmd = ["curl", "-sS", "-N", "-m", "30", "-D", "-", f"{GW}{PATHS[endpoint]}",
           "-H", "Content-Type: application/json", "-H", "Expect:",
           "-H", f"X-Test-Id: {tid}", "-H", f"X-Chunk-Runes: {chunk}",
           "-H", "X-Mislabel-Content-Type: true", "--data-binary", "@-"]
    p = subprocess.run(cmd, input=json.dumps(body, ensure_ascii=False).encode(),
                        stdout=subprocess.PIPE, stderr=subprocess.PIPE)
    raw = p.stdout
    while raw[:5] == b"HTTP/" and b"\r\n\r\n" in raw and b" 1" in raw.split(b"\r\n", 1)[0][:12]:
        raw = raw.split(b"\r\n\r\n", 1)[1]
    hdr, _, bod = raw.partition(b"\r\n\r\n")
    status = "?"; ctype = ""
    for line in hdr.split(b"\r\n"):
        if line.startswith(b"HTTP/"):
            status = line.split(b" ")[1].decode()
        if line.lower().startswith(b"content-type:"):
            ctype = line.split(b":", 1)[1].decode().strip()
    return status, ctype, bod.decode("utf-8", "replace")

def reassemble(endpoint, text):
    acc = []
    for fr in re.split(r"\n\n", text):
        data = None
        for line in fr.splitlines():
            if line.startswith("data:"):
                data = line[5:].strip()
        if not data or data == "[DONE]":
            continue
        try:
            j = json.loads(data)
        except Exception:
            return None, "bad_sse_frame"
        if endpoint == "chat":
            d = j.get("choices", [{}])[0].get("delta", {}).get("content")
            if d:
                acc.append(d)
        elif endpoint == "messages":
            d = j.get("delta", {})
            if j.get("type") == "content_block_delta" and d.get("type") == "text_delta":
                acc.append(d.get("text", ""))
        elif endpoint == "responses":
            if j.get("type") == "response.output_text.delta":
                acc.append(j.get("delta", ""))
    return "".join(acc), None

print("A. Mislabeled-SSE stream detection (streamRequested from request body)")
for endpoint in ("chat", "responses", "messages"):
    tid = f"mislabel.{endpoint}"
    status, ctype, body = send(endpoint, body_for(endpoint, EMAIL), tid)
    text, err = (reassemble(endpoint, body) if status == "200" else (None, f"http_{status}"))
    ok_framing = err is None
    roundtrip = (text == EMAIL) if ok_framing else False
    leaked = bool(ok_framing and PLACEHOLDER_RE.search(text or ""))
    ok = (status == "200") and ok_framing and roundtrip and not leaked
    check(f"{endpoint}: mislabeled Content-Type ({ctype!r}) still demasked as a stream", ok,
          f"status={status} err={err} roundtrip={roundtrip} leak={leaked}")

print("B. Request body size limit (GUARDRAILS_MAX_REQUEST_BYTES -> 413)")
# Comfortably over the default 32 MiB cap; filler has no sensitive content.
oversized = json.dumps({"model": "demo", "messages": [{"role": "user", "content": "A" * (34 * 1024 * 1024)}]})
p = subprocess.run(["curl", "-sS", "-m", "60", "-o", "/dev/null", "-w", "%{http_code}",
                     f"{GW}{PATHS['chat']}", "-H", "Content-Type: application/json",
                     "-H", "Expect:", "--data-binary", "@-"],
                    input=oversized.encode(), stdout=subprocess.PIPE, stderr=subprocess.PIPE)
status = p.stdout.decode()
check("over-limit body (34MiB) -> 413", status == "413", f"got {status}")

small = json.dumps({"model": "demo", "messages": [{"role": "user", "content": "under the limit"}]})
p = subprocess.run(["curl", "-sS", "-m", "30", "-o", "/dev/null", "-w", "%{http_code}",
                     f"{GW}{PATHS['chat']}", "-H", "Content-Type: application/json",
                     "-H", "Expect:", "--data-binary", "@-"],
                    input=small.encode(), stdout=subprocess.PIPE, stderr=subprocess.PIPE)
status = p.stdout.decode()
check("under-limit body -> 200 (proxies normally)", status == "200", f"got {status}")

npass = sum(1 for _, ok, _ in results if ok)
print(f"\nSTREAM/LIMIT EDGE TESTS: {npass}/{len(results)} passed")
sys.exit(0 if npass == len(results) else 1)
