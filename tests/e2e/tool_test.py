#!/usr/bin/env python3
"""E2E tool-call demask test. The upstream (X-Echo-Mode: tool) reflects the
masked user text into a tool call's arguments {"q": <text>}. We verify the
client receives valid JSON tool arguments whose "q" == the original input
(round-trip), across all 3 APIs, streaming and non-streaming. Includes a value
containing a quote and backslash — the case that broke the Responses SSE
function_call_arguments.delta path before the JSON-escaping fix."""
import os
import json, subprocess, re, sys

GW = os.environ.get("GW", "http://localhost:8080")
PATHS = {"chat": "/v1/chat/completions", "responses": "/v1/responses", "messages": "/v1/messages"}
PH = re.compile(r"<[A-Za-z][A-Za-z0-9_]*_\d+>")

def send(endpoint, body, stream, tid):
    cmd = ["curl", "-sS", "-N", "-m", "60", "-D", "-", f"{GW}{PATHS[endpoint]}",
           "-H", "Content-Type: application/json", "-H", "Expect:",
           "-H", "X-Echo-Mode: tool", "-H", f"X-Test-Id: {tid}", "--data-binary", "@-"]
    p = subprocess.run(cmd, input=json.dumps(body, ensure_ascii=False).encode(), stdout=subprocess.PIPE, stderr=subprocess.PIPE)
    raw = p.stdout
    while raw[:5] == b"HTTP/" and b"\r\n\r\n" in raw and b" 1" in raw.split(b"\r\n",1)[0][:12]:
        raw = raw.split(b"\r\n\r\n", 1)[1]
    hdr, _, bod = raw.partition(b"\r\n\r\n")
    ctype = ""
    for line in hdr.split(b"\r\n"):
        if line.lower().startswith(b"content-type:"):
            ctype = line.split(b":",1)[1].decode().strip()
    return ctype, bod.decode("utf-8", "replace")

def sse_frames(text):
    for fr in re.split(r"\n\n", text):
        data=None
        for line in fr.splitlines():
            if line.startswith("data:"):
                data = line[5:].strip()
        if data and data != "[DONE]":
            yield data

def extract_args(endpoint, ctype, body):
    """Return (arguments_string, source) reassembled as a streaming client would."""
    is_sse = "text/event-stream" in ctype
    if endpoint == "chat":
        if not is_sse:
            o = json.loads(body)
            return o["choices"][0]["message"]["tool_calls"][0]["function"]["arguments"], "nonstream"
        acc=[]
        for d in sse_frames(body):
            j=json.loads(d)
            for tc in j["choices"][0].get("delta",{}).get("tool_calls",[]) or []:
                frag=tc.get("function",{}).get("arguments")
                if frag: acc.append(frag)
        return "".join(acc), "stream-delta"
    if endpoint == "responses":
        if not is_sse:
            o=json.loads(body)
            for item in o["output"]:
                if item.get("type")=="function_call":
                    return item["arguments"], "nonstream"
            return "", "nonstream"
        acc=[]; done=None
        for d in sse_frames(body):
            j=json.loads(d); t=j.get("type")
            if t=="response.function_call_arguments.delta": acc.append(j.get("delta",""))
            elif t=="response.function_call_arguments.done": done=j.get("arguments")
        return "".join(acc), "stream-delta"
    if endpoint == "messages":
        if not is_sse:
            o=json.loads(body)
            for b in o["content"]:
                if b.get("type")=="tool_use":
                    return json.dumps(b["input"], ensure_ascii=False), "nonstream"
            return "", "nonstream"
        acc=[]
        for d in sse_frames(body):
            j=json.loads(d)
            if j.get("type")=="content_block_delta" and j.get("delta",{}).get("type")=="input_json_delta":
                acc.append(j["delta"].get("partial_json",""))
        return "".join(acc), "stream-delta"
    return "", "?"

def body_for(endpoint, text, stream):
    if endpoint=="chat":
        return {"model":"demo","stream":stream,"messages":[{"role":"user","content":text}]}
    if endpoint=="messages":
        return {"model":"demo","stream":stream,"max_tokens":256,"messages":[{"role":"user","content":text}]}
    if endpoint=="responses":
        return {"model":"demo","stream":stream,"input":text}

# test inputs (real bytes on disk via python string literals)
INPUTS = {
    "plain":  "напиши на qa.lead@example.com",
    "quote":  'ключ QSECRET"a\\b/c{}<>&t и телефон 8-999-123-45-67',
}
fails=0; total=0
for name, text in INPUTS.items():
    for endpoint in ("chat","responses","messages"):
        for stream in (False, True):
            total+=1
            tid=f"tool.{name}.{endpoint}.{'s' if stream else 'n'}"
            ctype, body = send(endpoint, body_for(endpoint, text, stream), stream, tid)
            args, src = extract_args(endpoint, ctype, body)
            ok_json = True; qval=None; leftover=None
            try:
                parsed = json.loads(args)
                qval = parsed.get("q")
            except Exception as e:
                ok_json=False
            leftover = bool(PH.search(args))
            roundtrip = (qval == text)
            status = "PASS" if (ok_json and roundtrip and not leftover) else "FAIL"
            if status=="FAIL":
                fails+=1
                print(f"  {status} {tid} [{src}] valid_json={ok_json} roundtrip={roundtrip} placeholder_leftover={leftover} args_len={len(args)}")
            else:
                print(f"  {status} {tid} [{src}]")
print(f"\nTOOL TESTS: {total-fails}/{total} passed")
sys.exit(1 if fails else 0)
