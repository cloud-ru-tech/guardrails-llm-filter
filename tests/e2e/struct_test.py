#!/usr/bin/env python3
"""Structured multi-field masking tests: verify the upstream receives each
sensitive field MASKED (no raw secret; placeholder present) across the richer
request shapes — system, tool_use.input, tool_result.content, instructions,
input items, function_call_output (string AND array form), chat tool_calls
arguments, multi-message. Uses a safe synthetic email as the probe."""
import os
import json, subprocess, sys

GW = os.environ.get("GW", "http://localhost:8080")
CAP = os.path.join(os.path.dirname(os.path.abspath(__file__)), "capture", "requests.jsonl")
EMAIL = "probe@example.com"

def send(path, body, tid):
    cmd=["curl","-sS","-m","30","-o","/dev/null","-w","%{http_code}",f"{GW}{path}",
         "-H","Content-Type: application/json","-H","Expect:","-H",f"X-Test-Id: {tid}","--data-binary","@-"]
    p=subprocess.run(cmd,input=json.dumps(body,ensure_ascii=False).encode(),stdout=subprocess.PIPE,stderr=subprocess.PIPE)
    return p.stdout.decode()

def cap_body(tid):
    last=None
    for l in open(CAP,encoding="utf-8"):
        l=l.strip()
        if not l: continue
        o=json.loads(l)
        if o.get("test_id")==tid: last=o
    return json.dumps(last["body"],ensure_ascii=False) if last and last.get("body") is not None else ""

CASES = [
 # (id, path, body, expect_masked_field)
 ("st.msg.system.str","/v1/messages",
   {"model":"demo","max_tokens":64,"system":f"инструкция: пиши на {EMAIL}",
    "messages":[{"role":"user","content":"привет"}]}, True),
 ("st.msg.system.arr","/v1/messages",
   {"model":"demo","max_tokens":64,"system":[{"type":"text","text":f"контакт {EMAIL}"}],
    "messages":[{"role":"user","content":"привет"}]}, True),
 ("st.msg.tooluse.input","/v1/messages",
   {"model":"demo","max_tokens":64,"messages":[
     {"role":"assistant","content":[{"type":"tool_use","id":"tu1","name":"send","input":{"to":EMAIL}}]},
     {"role":"user","content":"ок"}]}, True),
 ("st.msg.toolresult","/v1/messages",
   {"model":"demo","max_tokens":64,"messages":[
     {"role":"user","content":[{"type":"tool_result","tool_use_id":"tu1","content":f"found {EMAIL}"}]}]}, True),
 ("st.msg.toolresult.arr","/v1/messages",
   {"model":"demo","max_tokens":64,"messages":[
     {"role":"user","content":[{"type":"tool_result","tool_use_id":"tu1",
        "content":[{"type":"text","text":f"found {EMAIL}"}]}]}]}, True),
 ("st.resp.instructions","/v1/responses",
   {"model":"demo","instructions":f"свяжись с {EMAIL}","input":"привет"}, True),
 ("st.resp.items.input_text","/v1/responses",
   {"model":"demo","input":[{"role":"user","content":[{"type":"input_text","text":f"почта {EMAIL}"}]}]}, True),
 ("st.resp.fco.string","/v1/responses",
   {"model":"demo","input":[{"type":"function_call_output","call_id":"c1","output":f"result {EMAIL}"}]}, True),
 # array-form function_call_output — coverage gap now FIXED, must be masked
 ("st.resp.fco.array","/v1/responses",
   {"model":"demo","input":[{"type":"function_call_output","call_id":"c1",
      "output":[{"type":"output_text","text":f"result {EMAIL}"}]}]}, True),
 ("st.chat.multi","/v1/chat/completions",
   {"model":"demo","messages":[
     {"role":"system","content":f"пиши на {EMAIL}"},
     {"role":"user","content":f"и ещё {EMAIL}"}]}, True),
 ("st.chat.toolcall.args","/v1/chat/completions",
   {"model":"demo","messages":[
     {"role":"assistant","content":None,"tool_calls":[
        {"id":"c1","type":"function","function":{"name":"send","arguments":json.dumps({"to":EMAIL})}}]},
     {"role":"user","content":"ок"}]}, True),
]

fails=0
for tid,path,body,expect in CASES:
    code=send(path,body,tid)
    cb=cap_body(tid)
    masked = (EMAIL not in cb)          # masked => raw email absent upstream
    has_ph = ("<EMAIL_1>" in cb or "<EMAIL" in cb)
    if expect:
        ok = (code=="200") and masked and has_ph
        note = "" if ok else f"(code={code} email_absent={masked} has_ph={has_ph})"
    else:
        # documented coverage gap: field NOT masked -> email present, no ph for it
        ok = (code=="200") and (not masked)
        note = "KNOWN-GAP: field passed through UNMASKED (review finding)" if ok else f"(code={code} unexpectedly masked?)"
    status = "PASS" if ok else "FAIL"
    if not ok: fails+=1
    print(f"  {status} {tid} {note}")
print(f"\nSTRUCT TESTS: {len(CASES)-fails}/{len(CASES)} passed")
sys.exit(1 if fails else 0)
