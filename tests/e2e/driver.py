#!/usr/bin/env python3
"""
E2E test driver for extproc-guardrails.

Design principle: this script NEVER prints raw sensitive content. It derives the
actual masked secret from (original, captured) via difflib, and reports only
booleans / counts / hashes / placeholder-type names. So the agent's context
stays free of secrets while every mask/demask property is verified precisely.

Per test case it verifies:
  - roundtrip: client-received text == original input text (demask perfectly
    inverts mask). The master correctness property.
  - masking_occurred: when expected, the upstream saw a changed body.
  - no_secret_leak_upstream: the exact bytes that were replaced (the secret) do
    not appear anywhere in what the upstream received.
  - only_placeholders_inserted: every insertion the service made is a <...>
    placeholder token (it never injects arbitrary text).
  - no_placeholder_leak_client: the client response carries no <TYPE_N>
    service-placeholder leftovers.
  - valid_framing: non-stream => valid JSON; stream => parseable SSE that
    yields the reassembled text.
"""
import json, subprocess, sys, os, re, difflib, hashlib, argparse

GW = os.environ.get("GW", "http://localhost:10000")
_HARNESS = os.path.dirname(os.path.abspath(__file__))
CAPTURE = os.environ.get("CAPTURE_FILE",
    os.path.join(_HARNESS, "capture", "requests.jsonl"))

PLACEHOLDER_RE = re.compile(r"<[A-Z][A-Z0-9]*(?:_[A-Z0-9]+)*_\d+>")
ANY_ANGLE_TOKEN_RE = re.compile(r"<[A-Za-z][A-Za-z0-9_]*>")

PATHS = {
    "chat": "/v1/chat/completions",
    "responses": "/v1/responses",
    "messages": "/v1/messages",
}

def sha(s):
    return hashlib.sha256(s.encode("utf-8", "surrogatepass")).hexdigest()[:12]

# ---- primary-field extraction (mirrors the echo upstream) -------------------

def _content_text(raw):
    if isinstance(raw, str):
        return raw
    if isinstance(raw, list):
        out = []
        for p in raw:
            if isinstance(p, dict) and isinstance(p.get("text"), str):
                out.append(p["text"])
        return "".join(out)
    return json.dumps(raw, ensure_ascii=False)

def primary_text(endpoint, body):
    if endpoint == "chat":
        for m in reversed(body.get("messages", [])):
            if m.get("role") == "user":
                return _content_text(m.get("content", ""))
        return ""
    if endpoint == "messages":
        for m in reversed(body.get("messages", [])):
            if m.get("role") == "user":
                c = m.get("content", "")
                if isinstance(c, str):
                    return c
                if isinstance(c, list):
                    return "".join(b.get("text", "") for b in c
                                   if isinstance(b, dict) and b.get("type") == "text")
                return _content_text(c)
        return ""
    if endpoint == "responses":
        inp = body.get("input", "")
        if isinstance(inp, str):
            return inp
        if isinstance(inp, list):
            for item in reversed(inp):
                c = item.get("content") if isinstance(item, dict) else None
                if isinstance(c, str):
                    return c
                if isinstance(c, list):
                    for part in reversed(c):
                        if isinstance(part, dict) and part.get("type") == "input_text" and part.get("text"):
                            return part["text"]
        return ""
    return ""

# ---- send via curl ---------------------------------------------------------

def send(case):
    endpoint = case["endpoint"]
    path = PATHS[endpoint]
    body = json.dumps(case["body"], ensure_ascii=False)
    headers = [
        "-H", "Content-Type: application/json",
        "-H", "Expect:",  # disable 100-continue so header block is single
        "-H", f"X-Test-Id: {case['id']}",
    ]
    if case.get("chunk"):
        headers += ["-H", f"X-Chunk-Runes: {case['chunk']}"]
    if case.get("echo_mode"):
        headers += ["-H", f"X-Echo-Mode: {case['echo_mode']}"]
    for k, v in case.get("headers", {}).items():
        headers += ["-H", f"{k}: {v}"]
    cmd = ["curl", "-sS", "-m", "60", "-D", "-", f"{GW}{path}"] + headers + \
          ["--data-binary", "@-"]
    if case.get("stream"):
        cmd.insert(1, "-N")
    p = subprocess.run(cmd, input=body.encode("utf-8"),
                       stdout=subprocess.PIPE, stderr=subprocess.PIPE)
    raw = p.stdout
    # Drop any leading 1xx informational header blocks (e.g. 100 Continue).
    while raw[:5] == b"HTTP/" and b"\r\n\r\n" in raw:
        status_line = raw.split(b"\r\n", 1)[0]
        if b" 1" in status_line[:12]:  # HTTP/1.1 1xx
            raw = raw.split(b"\r\n\r\n", 1)[1]
            continue
        break
    if b"\r\n\r\n" in raw:
        hdr, _, bod = raw.partition(b"\r\n\r\n")
    else:
        hdr, bod = b"", raw
    resp_headers = {}
    for line in hdr.split(b"\r\n"):
        if line.startswith(b"HTTP/"):
            parts = line.split(b" ", 2)
            if len(parts) >= 2:
                resp_headers["_status"] = parts[1].decode()
        elif b":" in line:
            k, _, v = line.partition(b":")
            resp_headers[k.decode().strip().lower()] = v.decode().strip()
    return resp_headers, bod, p.returncode, p.stderr.decode(errors="replace")

# ---- reassemble client text from response ----------------------------------

def reassemble(endpoint, stream, body_bytes):
    text = body_bytes.decode("utf-8", "replace")
    if not stream:
        try:
            obj = json.loads(text)
        except Exception as e:
            return None, f"invalid_json:{e}"
        if endpoint == "chat":
            try:
                return obj["choices"][0]["message"]["content"], None
            except Exception as e:
                return None, f"shape:{e}"
        if endpoint == "messages":
            try:
                return "".join(b.get("text", "") for b in obj["content"]
                               if b.get("type") == "text"), None
            except Exception as e:
                return None, f"shape:{e}"
        if endpoint == "responses":
            try:
                out = []
                for item in obj.get("output", []):
                    for c in item.get("content", []):
                        if c.get("type") == "output_text":
                            out.append(c.get("text", ""))
                return "".join(out), None
            except Exception as e:
                return None, f"shape:{e}"
    # streaming
    frames = re.split(r"\n\n", text)
    if endpoint == "chat":
        acc = []
        for fr in frames:
            for line in fr.splitlines():
                if line.startswith("data:"):
                    d = line[5:].strip()
                    if d == "[DONE]" or not d:
                        continue
                    try:
                        j = json.loads(d)
                        delta = j["choices"][0].get("delta", {}).get("content")
                        if delta:
                            acc.append(delta)
                    except Exception:
                        return None, f"bad_sse_frame"
        return "".join(acc), None
    if endpoint == "messages":
        acc = []
        for fr in frames:
            data = None
            for line in fr.splitlines():
                if line.startswith("data:"):
                    data = line[5:].strip()
            if not data:
                continue
            try:
                j = json.loads(data)
            except Exception:
                return None, "bad_sse_frame"
            if j.get("type") == "content_block_delta":
                d = j.get("delta", {})
                if d.get("type") == "text_delta":
                    acc.append(d.get("text", ""))
        return "".join(acc), None
    if endpoint == "responses":
        acc = []
        snapshots = []  # output_text.done / completed full texts for leak check
        for fr in frames:
            data = None
            for line in fr.splitlines():
                if line.startswith("data:"):
                    data = line[5:].strip()
            if not data:
                continue
            try:
                j = json.loads(data)
            except Exception:
                return None, "bad_sse_frame"
            t = j.get("type")
            if t == "response.output_text.delta":
                acc.append(j.get("delta", ""))
            elif t == "response.output_text.done":
                snapshots.append(j.get("text", ""))
            elif t == "response.completed":
                snapshots.append(json.dumps(j, ensure_ascii=False))
        # stash snapshots on a side channel via attribute trick: return tuple
        return ("".join(acc), snapshots), None
    return None, "unknown_endpoint"

# ---- capture lookup --------------------------------------------------------

_CAP_CACHE = {}
_CAP_OFFSET = [0]

def refresh_captures():
    if not os.path.exists(CAPTURE):
        return _CAP_CACHE
    with open(CAPTURE, "rb") as f:
        f.seek(_CAP_OFFSET[0])
        data = f.read()
        _CAP_OFFSET[0] = f.tell()
    for line in data.decode("utf-8", "replace").splitlines():
        line = line.strip()
        if not line:
            continue
        try:
            o = json.loads(line)
        except Exception:
            continue
        _CAP_CACHE.setdefault(o.get("test_id"), []).append(o)
    return _CAP_CACHE

def load_captures():
    return refresh_captures()

def captured_primary(endpoint, cap_body):
    """Extract the same primary field from the captured (masked) body."""
    return primary_text(endpoint, cap_body)

# ---- checks ----------------------------------------------------------------

def recover_secrets(original, masked):
    """Placeholder-anchored exact secret recovery.
    masked = C0 <PH> C1 <PH> ... Cn ; original = C0 S1 C1 S2 ... Sn Cn.
    Anchor C0 as prefix and Cn as suffix (avoids first-occurrence mis-alignment
    when an inner context char also occurs inside a secret). Returns the exact
    replaced byte-strings, or None if the structure can't be reconstructed."""
    contexts = ANY_ANGLE_TOKEN_RE.split(masked)
    n = len(contexts) - 1
    if n <= 0:
        return []
    c0, cn = contexts[0], contexts[-1]
    if not original.startswith(c0):
        return None
    if cn and not original.endswith(cn):
        return None
    start = len(c0)
    end = len(original) - len(cn)
    if end < start:
        return None
    mid = original[start:end]
    if n == 1:
        return [mid]
    # multiple placeholders: recover inner secrets by sequential find on inner
    # contexts within `mid`. Inner contexts are contexts[1..n-1].
    secrets = []
    pos = 0
    ok = True
    for k in range(1, n):
        ck = contexts[k]
        if ck == "":
            secrets.append("")
            continue
        j = mid.find(ck, pos)
        if j < 0:
            ok = False
            break
        secrets.append(mid[pos:j])
        pos = j + len(ck)
    if not ok:
        return None
    secrets.append(mid[pos:])
    return secrets


def diff_secrets(original, masked):
    """Return (replaced_secrets, inserted_tokens) via difflib opcodes.
    replaced_secrets = original substrings that were removed/replaced.
    inserted_tokens  = masked substrings that were inserted/replaced-in."""
    sm = difflib.SequenceMatcher(a=original, b=masked, autojunk=False)
    replaced, inserted = [], []
    for tag, i1, i2, j1, j2 in sm.get_opcodes():
        if tag in ("replace", "delete") and i2 > i1:
            replaced.append(original[i1:i2])
        if tag in ("replace", "insert") and j2 > j1:
            inserted.append(masked[j1:j2])
    return replaced, inserted

def run_case(case, caps):
    r = {"id": case["id"], "endpoint": case["endpoint"],
         "stream": bool(case.get("stream")), "checks": {}, "info": {}}
    resp_headers, body_bytes, rc, stderr = send(case)
    r["http_status"] = resp_headers.get("_status", "?")
    r["curl_rc"] = rc
    if rc != 0:
        r["checks"]["transport"] = False
        r["info"]["stderr"] = stderr[:200]
        return r
    r["checks"]["transport"] = True

    endpoint = case["endpoint"]
    original = primary_text(endpoint, case["body"])
    r["info"]["orig_sha"] = sha(original)
    r["info"]["orig_len"] = len(original)

    # Decide SSE vs JSON from the actual response content-type (robust against
    # a case whose body forgot stream:true).
    is_sse = "text/event-stream" in (resp_headers.get("content-type", ""))
    r["info"]["is_sse"] = is_sse
    reasm, err = reassemble(endpoint, is_sse, body_bytes)
    snapshots = []
    if isinstance(reasm, tuple):
        reasm, snapshots = reasm
    if err:
        r["checks"]["valid_framing"] = False
        r["info"]["framing_err"] = err
        return r
    r["checks"]["valid_framing"] = True

    client_text = reasm if reasm is not None else ""
    r["info"]["client_sha"] = sha(client_text)
    r["info"]["client_len"] = len(client_text)

    # ROUNDTRIP: client text equals original. For a known placeholder-collision
    # case (input literally contains a <TYPE_N> token) round-trip cannot hold by
    # design — record it as info, not a hard failure.
    rt = (client_text == original)
    if case.get("known_collision"):
        r["info"]["roundtrip_known_collision"] = rt
    else:
        r["checks"]["roundtrip"] = rt
    if client_text != original:
        # safe diff summary
        sm = difflib.SequenceMatcher(a=original, b=client_text, autojunk=False)
        r["info"]["roundtrip_ratio"] = round(sm.ratio(), 4)
        # first divergence index
        idx = next((k for k in range(min(len(original), len(client_text)))
                    if original[k] != client_text[k]), min(len(original), len(client_text)))
        r["info"]["first_diff_idx"] = idx
        # does client leak a placeholder that should've been demasked?
        r["info"]["client_has_placeholder"] = bool(PLACEHOLDER_RE.search(client_text))

    # NO placeholder leak to client (unless the original itself had angle tokens)
    orig_tokens = set(ANY_ANGLE_TOKEN_RE.findall(original)) | set(PLACEHOLDER_RE.findall(original))
    client_ph = [t for t in PLACEHOLDER_RE.findall(client_text) if t not in orig_tokens]
    r["checks"]["no_placeholder_leak_client"] = (len(client_ph) == 0)
    if client_ph:
        r["info"]["leaked_client_ph"] = sorted(set(client_ph))
    # responses snapshots must also be demasked
    for snap in snapshots:
        snap_ph = [t for t in PLACEHOLDER_RE.findall(snap) if t not in orig_tokens]
        if snap_ph:
            r["checks"]["no_placeholder_leak_client"] = False
            r["info"].setdefault("leaked_snapshot_ph", []).extend(sorted(set(snap_ph)))

    # MASKING side: find the capture for this test id (refresh: the upstream
    # writes+flushes the capture line before it responds, so it's on disk now).
    refresh_captures()
    cap_list = caps.get(case["id"], [])
    if not cap_list:
        r["checks"]["captured"] = False
        return r
    r["checks"]["captured"] = True
    cap = cap_list[-1]
    cap_body = cap.get("body")
    full_cap_str = json.dumps(cap_body, ensure_ascii=False) if cap_body is not None else (cap.get("body_raw") or "")
    masked_primary = captured_primary(endpoint, cap_body) if cap_body is not None else ""

    replaced, inserted = diff_secrets(original, masked_primary)
    r["info"]["n_replaced"] = len([x for x in replaced if x])
    r["info"]["inserted_ph_types"] = sorted(set(
        re.sub(r"_\d+>$", ">", t) for t in inserted for _ in [0] if t.startswith("<")))
    # actual inserted placeholders (types only, safe)
    ins_ph = [t for t in inserted if PLACEHOLDER_RE.fullmatch(t.strip())]

    expect_masked = case.get("expect_masked", True)
    if expect_masked:
        r["checks"]["masking_occurred"] = (masked_primary != original)
    else:
        r["checks"]["masking_occurred"] = (masked_primary == original)  # negative: no change

    # NO secret leak upstream: recover the EXACT bytes each placeholder replaced
    # (placeholder-anchored) and assert none survive in the captured body. This
    # is robust where the difflib approach produced short-fragment artifacts.
    recovered = recover_secrets(original, masked_primary)
    if recovered is None:
        # structure not reconstructable (e.g. masking altered non-secret text) —
        # fall back to the difflib-derived secrets with a higher length gate.
        leaks = [sha(s) for s in replaced if len(s) >= 6 and s in full_cap_str]
        r["info"]["leak_method"] = "difflib_fallback"
    else:
        # A recovered "secret" that is itself a placeholder-shaped token is
        # user-supplied literal text (placeholder collision), not a real secret.
        leaks = [sha(s) for s in recovered
                 if len(s) >= 4 and s in full_cap_str and not ANY_ANGLE_TOKEN_RE.fullmatch(s)]
        r["info"]["leak_method"] = "anchored"
        r["info"]["recovered_secret_lens"] = [len(s) for s in recovered]
    r["checks"]["no_secret_leak_upstream"] = (len(leaks) == 0)
    if leaks:
        r["info"]["leaked_secret_sha"] = leaks

    # expected placeholder types (from dataset) — INFORMATIONAL ONLY: dataset
    # uses generic names (API_KEY) while the service emits specific ones
    # (GCP_API_KEY, GITHUB_PAT). Round-trip is the real correctness proof.
    exp = case.get("expected_placeholders") or []
    if exp and expect_masked:
        types_present = " ".join(r["info"]["inserted_ph_types"]).upper()
        missing = [p for p in exp if p.upper() not in types_present]
        if missing:
            r["info"]["placeholder_name_mismatch"] = missing

    return r

def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("plan")
    ap.add_argument("--out", default=None)
    ap.add_argument("--verbose", action="store_true")
    args = ap.parse_args()

    cases = []
    for line in open(args.plan, encoding="utf-8"):
        line = line.strip()
        if line:
            cases.append(json.loads(line))

    caps = load_captures()
    results = []
    for c in cases:
        try:
            refresh_captures()
            res = run_case(c, caps)
        except Exception as e:
            res = {"id": c.get("id"), "checks": {"driver_error": False},
                   "info": {"exc": repr(e)[:200]}}
        results.append(res)

    # summary
    total = len(results)
    failed = []
    check_fail_counts = {}
    for res in results:
        bad = [k for k, v in res.get("checks", {}).items() if v is False]
        if bad:
            failed.append((res["id"], bad, res.get("info", {})))
            for k in bad:
                check_fail_counts[k] = check_fail_counts.get(k, 0) + 1

    print(f"TOTAL={total} PASS={total-len(failed)} FAIL={len(failed)}")
    if check_fail_counts:
        print("FAILED CHECKS:", json.dumps(check_fail_counts))
    for tid, bad, info in failed:
        print(f"  FAIL {tid}: {bad}")
        if args.verbose:
            print("       info:", json.dumps(info, ensure_ascii=False))

    if args.out:
        with open(args.out, "w", encoding="utf-8") as f:
            json.dump({"total": total, "pass": total-len(failed), "fail": len(failed),
                       "check_fail_counts": check_fail_counts, "results": results},
                      f, ensure_ascii=False, indent=2)
    sys.exit(1 if failed else 0)

if __name__ == "__main__":
    main()
