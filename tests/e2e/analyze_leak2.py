#!/usr/bin/env python3
"""Definitive leak analysis via placeholder-anchored secret recovery.

masked = C0 <PH> C1 <PH> C2 ... Cn   (Ck = shared non-secret context)
original = C0 S1 C1 S2 C2 ... Sn Cn   (Sk = the exact bytes replaced by PH k)

Recover each Sk by sequentially locating C0..Cn in original; the gaps are the
true secrets. Then a leak = any Sk (len>=1) that still appears in the FULL
captured upstream body. Prints only lengths / char-classes / booleans / sha.
"""
import os
import json, re, hashlib, sys
from collections import Counter

H = os.path.dirname(os.path.abspath(__file__))
PH = re.compile(r"<[A-Za-z][A-Za-z0-9_]*>")

def sha(s): return hashlib.sha256(s.encode('utf-8','surrogatepass')).hexdigest()[:10]

def primary(endpoint, body):
    if endpoint in ("chat","messages"):
        for m in reversed(body.get("messages",[])):
            if m.get("role")=="user":
                c=m.get("content","")
                if isinstance(c,str): return c
                if isinstance(c,list):
                    return "".join(b.get("text","") for b in c if isinstance(b,dict) and (b.get("type")=="text"))
        return ""
    if endpoint=="responses":
        inp=body.get("input","")
        return inp if isinstance(inp,str) else ""
    return ""

def recover_secrets(original, masked):
    """Return list of exact secret strings replaced by placeholders, or None if
    the context anchoring fails (structure not reconstructable)."""
    contexts = PH.split(masked)            # n+1 context segments
    n_ph = len(contexts) - 1
    if n_ph <= 0:
        return []
    secrets = []
    pos = 0
    # anchor C0
    c0 = contexts[0]
    if c0:
        i = original.find(c0, 0)
        if i != 0 and i < 0:
            return None
        # allow C0 not at 0 only if it's truly a prefix; require prefix
        if not original.startswith(c0):
            return None
        pos = len(c0)
    for k in range(1, n_ph+1):
        ck = contexts[k]
        if ck == "":
            # next placeholder immediately follows; secret is up to the next
            # context anchor — defer by treating empty context as zero-length
            # match at current pos (secret between consecutive placeholders is
            # ambiguous; mark as unknown by taking nothing)
            secrets.append("")  # cannot delimit; treat as empty
            continue
        j = original.find(ck, pos)
        if j < 0:
            return None
        secrets.append(original[pos:j])
        pos = j + len(ck)
    return secrets

def analyze(test_id, plan, caps, verbose=True):
    case=plan[test_id]; endpoint=case["endpoint"]
    orig=primary(endpoint, case["body"])
    cap=caps.get(test_id,[])
    if not cap: return ("nocap", test_id)
    masked=primary(endpoint, cap[-1]["body"])
    full_cap=json.dumps(cap[-1]["body"], ensure_ascii=False)
    secrets=recover_secrets(orig, masked)
    if secrets is None:
        if verbose: print(f"{test_id}: ANCHOR-FAIL (cannot reconstruct)")
        return ("anchorfail", test_id)
    leaks=[s for s in secrets if len(s)>=1 and s in full_cap]
    real=[s for s in leaks if len(s)>=4]  # meaningful leak threshold
    if verbose:
        print(f"{test_id} ({endpoint}): {len(secrets)} secret(s) recovered; "
              f"lens={[len(s) for s in secrets]}; "
              f"leaks(>=4)={[ (len(s),sha(s)) for s in real]}; "
              f"minor_leaks(<4)={[len(s) for s in leaks if len(s)<4]}")
    return ("real" if real else ("minor" if leaks else "clean"), test_id)

if __name__=="__main__":
    plan={json.loads(l)["id"]:json.loads(l) for l in open(f"{H}/plan_base.jsonl",encoding="utf-8") if l.strip()}
    caps={}
    for l in open(f"{H}/capture/requests.jsonl",encoding="utf-8"):
        l=l.strip()
        if not l: continue
        try:o=json.loads(l)
        except:continue
        caps.setdefault(o.get("test_id"),[]).append(o)

    # analyze ALL cases that the driver flagged as leak
    base=json.load(open(f"{H}/results/base.json"))
    leak_ids=[r["id"] for r in base["results"]
              if r.get("checks",{}).get("no_secret_leak_upstream") is False]
    print(f"driver-flagged leak cases: {len(leak_ids)}")
    verdicts=Counter()
    reals=[]
    for tid in leak_ids:
        v,_=analyze(tid, plan, caps, verbose=(len(leak_ids)<=40))
        verdicts[v]+=1
        if v=="real": reals.append(tid)
    print("VERDICTS:", dict(verdicts))
    if reals:
        print("REAL LEAK CASES:", reals)
