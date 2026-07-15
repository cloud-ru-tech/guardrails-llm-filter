#!/usr/bin/env python3
"""Benchmark guardrails-llm-filter masking against hivetrace/pii-bench (RU 152-ФЗ).

Recall is the compliance-critical metric: did the PII value get masked at all
(leak prevention), regardless of which rule fired. Type-correct recall additionally
requires the firing rule to map to the gold entity type. Precision measures how
many of our masks land on a labelled PII span.
"""
import json, urllib.request
from collections import defaultdict
from datasets import load_dataset

API = 'http://localhost:9080/v1/scan'
BATCH = 40

# gold hivetrace type -> set of our rule_id prefixes that count as "correct type"
TYPE_MAP = {
    'NAME':            ('pii.fio-ru',),
    'PHONE_NUMBER':    ('pii.phone-ru',),
    'EMAIL':           ('pii.email',),
    'SNILS':           ('pii.docs.snils',),
    'INN':             ('pii.docs.inn-person', 'pii.docs.inn-org'),
    'OGRN':            ('pii.docs.ogrn',),
    'OGRNIP':          ('pii.docs.ogrnip',),
    'PASSPORT_NUMBER': ('pii.docs.passport',),
    'BANK_CARD_NUMBER':('pii.fin.credit-card',),
    'TOKEN':           ('access_tokens.', 'api_keys.', 'credentials.'),
    'ADDRESS':         ('pii.docs.address',),
    'KPP':             ('pii.docs.kpp',),
    'CVC':             ('pii.fin.cvc',),
}

def scan_batch(texts):
    req = urllib.request.Request(API, data=json.dumps({'texts': texts}).encode(),
                                 headers={'Content-Type': 'application/json'})
    with urllib.request.urlopen(req, timeout=60) as r:
        return json.loads(r.read())

def find_spans(text, value):
    """All non-overlapping [start,end) of value in text."""
    spans, i = [], text.find(value)
    while i != -1 and value:
        spans.append((i, i + len(value)))
        i = text.find(value, i + len(value))
    return spans

def overlaps(a, b):
    return a[0] < b[1] and b[0] < a[1]

ds = load_dataset("hivetrace/pii-bench", "default")
rows = []
for split in ds:
    for ex in ds[split]:
        rows.append((split, ex['text'], ex['entities']))

# per-type counters
gold_total = defaultdict(int)      # gold spans of this type
det_any    = defaultdict(int)      # masked by ANY rule (recall)
det_type   = defaultdict(int)      # masked by correct-type rule
pred_spans_total = 0               # all predicted masked spans
pred_spans_hit   = 0               # predicted spans overlapping ANY gold span

# batch scan
texts = [r[1] for r in rows]
results = []
for i in range(0, len(texts), BATCH):
    d = scan_batch(texts[i:i+BATCH])
    # scan returns per-batch placeholders WITHOUT per-text attribution, so scan
    # one text at a time when the batch has PII to keep spans aligned.
    results.extend([None]*len(texts[i:i+BATCH]))

# The batch API pools placeholders across texts; to keep offsets aligned we scan
# per-text (fast enough: sub-ms each).
for idx, (split, text, entities) in enumerate(rows):
    d = scan_batch([text])
    placeholders = d.get('placeholders', [])
    # predicted spans with mapped rule prefix
    preds = []
    for p in placeholders:
        val, rid = p.get('original'), p.get('rule_id', '')
        if not val:
            continue
        for sp in find_spans(text, val):
            preds.append((sp, rid))
    pred_spans_total += len(preds)
    for sp, rid in preds:
        if any(overlaps(sp, (e['start'], e['end'])) for e in entities):
            pred_spans_hit += 1
    # score each gold span
    for e in entities:
        g = (e['start'], e['end']); t = e['type']
        gold_total[t] += 1
        hit_any = any(overlaps(sp, g) for sp, _ in preds)
        if hit_any:
            det_any[t] += 1
        correct_prefixes = TYPE_MAP.get(t, ())
        hit_type = any(overlaps(sp, g) and rid.startswith(correct_prefixes)
                       for sp, rid in preds) if correct_prefixes else False
        if hit_type:
            det_type[t] += 1

print(f"Датасет: hivetrace/pii-bench  ({len(rows)} примеров, {sum(gold_total.values())} PII-спанов)\n")
print(f"{'Тип (152-ФЗ)':20s} {'gold':>5s} {'recall*':>8s} {'type-recall':>12s}  правило")
print('-'*72)
order = sorted(gold_total, key=lambda t: -gold_total[t])
macro_r=[]; macro_tr=[]
for t in order:
    g = gold_total[t]
    r = det_any[t]/g if g else 0
    tr = det_type[t]/g if g else 0
    macro_r.append(r); macro_tr.append(tr)
    rule = TYPE_MAP.get(t) or ('— нет правила —',)
    print(f"{t:20s} {g:5d} {r*100:7.1f}% {tr*100:11.1f}%  {rule[0]}")
tot_g = sum(gold_total.values())
tot_any = sum(det_any.values()); tot_type = sum(det_type.values())
print('-'*72)
print(f"{'ВСЕГО (micro)':20s} {tot_g:5d} {tot_any/tot_g*100:7.1f}% {tot_type/tot_g*100:11.1f}%")
print(f"{'macro-avg':20s} {'':5s} {sum(macro_r)/len(macro_r)*100:7.1f}% {sum(macro_tr)/len(macro_tr)*100:11.1f}%")
prec = pred_spans_hit/pred_spans_total if pred_spans_total else 0
print(f"\nprecision масок по PII-спанам: {pred_spans_hit}/{pred_spans_total} = {prec*100:.1f}%")
print("* recall = значение замаскировано любым правилом (защита от утечки); type-recall = замаскировано правилом правильной категории")
