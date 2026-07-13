#!/usr/bin/env python3
"""Real-model integration: route through Envoy -> guardrails -> REAL model
(localhost:8881 via upstream proxy mode). Verifies the full pipeline survives a
live LLM. Hard checks: masking occurred on the request (capture), valid response
framing, and NO service placeholder leaks to the client (demask covered whatever
the model emitted, incl. reasoning). Round-trip (original present) is best-effort
since the model is non-deterministic."""
import os
import json, subprocess, re, sys, hashlib, string

GW=os.environ.get("GW","http://localhost:8080")
CAP=os.path.join(os.path.dirname(os.path.abspath(__file__)), "capture", "requests.jsonl")
PH=re.compile(r"<[A-Z][A-Z0-9]*(?:_[A-Z0-9]+)*_\d+>")
PATHS={"chat":"/v1/chat/completions","responses":"/v1/responses","messages":"/v1/messages"}

def b62(seed,n):
    a=string.ascii_letters+string.digits; out=[]; h=hashlib.sha256(seed.encode()).digest(); i=0
    while len(out)<n:
        if i>=len(h): h=hashlib.sha256(h).digest(); i=0
        out.append(a[h[i]%62]); i+=1
    return "".join(out)

MODEL="MiniMaxAI/MiniMax-M3"
SECRETS={"email":"neo.trinity@example.com","gcp":"AIza"+b62("rmgcp",35)}

def instr(content):
    return ("Repeat the following text back to me EXACTLY and VERBATIM, character for "
            f"character, with no changes, no commentary, nothing else:\n{content}")

def body(ep, content, stream):
    p=instr(content)
    if ep=="chat":
        return {"model":MODEL,"stream":stream,"max_tokens":400,"messages":[{"role":"user","content":p}]}
    if ep=="messages":
        return {"model":MODEL,"stream":stream,"max_tokens":400,"messages":[{"role":"user","content":p}]}
    if ep=="responses":
        return {"model":MODEL,"stream":stream,"max_output_tokens":400,"input":p}

def send(ep, b, stream, tid):
    cmd=["curl","-sS","-N","-m","90","-D","-",f"{GW}{PATHS[ep]}",
         "-H","Content-Type: application/json","-H","Expect:",
         "-H","X-Upstream-Mode: proxy","-H",f"X-Test-Id: {tid}","--data-binary","@-"]
    p=subprocess.run(cmd,input=json.dumps(b).encode(),stdout=subprocess.PIPE,stderr=subprocess.PIPE)
    raw=p.stdout
    while raw[:5]==b"HTTP/" and b"\r\n\r\n" in raw and b" 1" in raw.split(b"\r\n",1)[0][:12]:
        raw=raw.split(b"\r\n\r\n",1)[1]
    hdr,_,bod=raw.partition(b"\r\n\r\n")
    status="?"; ctype=""
    for line in hdr.split(b"\r\n"):
        if line.startswith(b"HTTP/"): status=line.split(b" ")[1].decode()
        if line.lower().startswith(b"content-type:"): ctype=line.split(b":",1)[1].decode().strip()
    return status, ctype, bod.decode("utf-8","replace"), p.returncode

def cap_masked(tid, secret):
    last=None
    for l in open(CAP,encoding="utf-8"):
        l=l.strip()
        if not l: continue
        o=json.loads(l)
        if o.get("test_id")==tid: last=o
    if not last: return None,None
    cb=json.dumps(last.get("body"),ensure_ascii=False)
    return (secret not in cb), bool(PH.search(cb) or "<" in cb)

def valid_framing(ctype, body):
    if "text/event-stream" in ctype:
        # at least one data: frame parses
        frames=[f for f in re.split(r"\n\n",body) if "data:" in f]
        if not frames: return False
        for f in frames:
            for line in f.splitlines():
                if line.startswith("data:"):
                    d=line[5:].strip()
                    if d and d!="[DONE]":
                        try: json.loads(d)
                        except: return False
        return True
    try:
        json.loads(body); return True
    except: return False

fails=0; total=0
for skind,secret in SECRETS.items():
    content=f"здесь секрет {secret} конец"
    for ep in ("chat","responses","messages"):
        for stream in (False,True):
            total+=1
            tid=f"rm.{skind}.{ep}.{'s' if stream else 'n'}"
            status,ctype,resp,rc=send(ep,body(ep,content,stream),stream,tid)
            masked,has_ph=cap_masked(tid,secret)
            framing=valid_framing(ctype,resp)
            leak=bool(PH.search(resp))              # service placeholder leaked to client?
            roundtrip=(secret in resp)               # best-effort
            hard_ok = (status=="200") and (masked is True) and framing and (not leak)
            if not hard_ok: fails+=1
            print(f"  {'PASS' if hard_ok else 'FAIL'} {tid} status={status} masked={masked} framing={framing} "
                  f"placeholder_leak={leak} roundtrip(best-effort)={roundtrip}")
print(f"\nREAL-MODEL: {total-fails}/{total} passed hard checks (status/masked/framing/no-leak)")
sys.exit(1 if fails else 0)
