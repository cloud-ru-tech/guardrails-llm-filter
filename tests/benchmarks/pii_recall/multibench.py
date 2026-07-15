#!/usr/bin/env python3
"""Multi-dataset PII-masking metrics for guardrails-llm-filter.

Evaluates the live masking path (POST /v1/scan) against several RU PII
datasets and reports precision / recall / F1 (span-level and char-level),
micro and macro, per dataset.

On ROC-AUC: a deterministic regex masker outputs a binary decision with no
score to threshold, so a single operating point — not a curve — exists.
ROC-AUC/PR-AUC require a continuous confidence; they are not defined for this
system. We report char-level TPR (recall), specificity (1-FPR on non-PII
chars) and the F1 operating point, which is the meaningful evaluation for a
masking gate. See the printed summary.
"""
import ast, json, urllib.request, sys
from collections import defaultdict
from datasets import load_dataset

API = 'http://localhost:9080/v1/scan'

# Common label -> our rule-id prefixes (correct-type credit). Recall (leak
# prevention) counts masking by ANY rule regardless of type.
RULE = {
    'NAME': ('pii.fio-ru',), 'PHONE': ('pii.phone-ru',), 'EMAIL': ('pii.email',),
    'SNILS': ('pii.docs.snils',), 'INN': ('pii.docs.inn-person', 'pii.docs.inn-org'),
    'OGRN': ('pii.docs.ogrn',), 'OGRNIP': ('pii.docs.ogrnip',), 'KPP': ('pii.docs.kpp',),
    'PASSPORT': ('pii.docs.passport',), 'ADDRESS': ('pii.docs.address',),
    'CARD': ('pii.fin.credit-card',), 'CVC': ('pii.fin.cvc',),
    'TOKEN': ('access_tokens.', 'api_keys.', 'credentials.'),
    'DOB': (), 'IBAN': ('pii.fin.iban',), 'IP': ('ip-addrs.',),
}

def scan(text):
    req = urllib.request.Request(API, data=json.dumps({'texts': [text]}).encode(),
                                 headers={'Content-Type': 'application/json'})
    with urllib.request.urlopen(req, timeout=60) as r:
        return json.loads(r.read())

def pred_spans(text, d):
    out = []
    for p in d.get('placeholders', []):
        v, rid = p.get('original'), p.get('rule_id', '')
        if not v:
            continue
        i = text.find(v)
        while i != -1:
            out.append((i, i + len(v), rid)); i = text.find(v, i + len(v))
    return out

def overlap(a, b):
    return a[0] < b[1] and b[0] < a[1]

# ---- dataset loaders -> list of (text, [(start,end,common_type)]) ----
def load_hivetrace(n):
    ds = load_dataset("hivetrace/pii-bench", "default")
    m = {'NAME':'NAME','PHONE_NUMBER':'PHONE','EMAIL':'EMAIL','ADDRESS':'ADDRESS','PASSPORT_NUMBER':'PASSPORT',
         'INN':'INN','SNILS':'SNILS','TOKEN':'TOKEN','KPP':'KPP','OGRN':'OGRN','BANK_CARD_NUMBER':'CARD','OGRNIP':'OGRNIP','CVC':'CVC'}
    rows=[]
    for sp in ds:
        for ex in ds[sp]:
            rows.append((ex['text'], [(e['start'],e['end'],m[e['type']]) for e in ex['entities'] if e['type'] in m]))
    return rows[:n]

def load_alexen(n):
    ds = load_dataset("alexen2/pii-ner-ru-benchmark", split=f"test[:{n}]")
    m={'PER':'NAME','PHONE':'PHONE','EMAIL':'EMAIL'}
    rows=[]
    for ex in ds:
        toks=ex['tokens']; tags=ex['ner_tags']
        if isinstance(toks,str): toks=ast.literal_eval(toks)
        if isinstance(tags,str): tags=ast.literal_eval(tags)
        text=' '.join(toks); spans=[]; pos=0; cur=None
        offs=[]
        for tkn in toks:
            offs.append((pos,pos+len(tkn))); pos+=len(tkn)+1
        i=0
        while i<len(tags):
            tg=tags[i]
            if tg.startswith('B-'):
                typ=tg[2:]; s=offs[i][0]; e=offs[i][1]; j=i+1
                while j<len(tags) and tags[j]=='I-'+typ: e=offs[j][1]; j+=1
                if typ in m: spans.append((s,e,m[typ]))
                i=j
            else: i+=1
        rows.append((text,spans))
    return rows

def load_alrosait(n):
    ds = load_dataset("alrosait/pii-synthetic-ru", split=f"train[:{n}]")
    m={'NAME':'NAME','ADDRESS':'ADDRESS'}
    rows=[]
    for ex in ds:
        ents=ex['entities']
        if isinstance(ents,str): ents=ast.literal_eval(ents)
        sp=[]; text=ex['text']
        for e in (ents or []):
            typ=e.get('type') or e.get('label')
            val=e.get('text') or e.get('value')
            if typ not in m or not val: continue
            i=text.find(val)
            if i!=-1: sp.append((i,i+len(val),m[typ]))
        rows.append((text, sp))
    return rows

def evaluate(name, rows):
    gt=defaultdict(int); tp_r=defaultdict(int); tp_t=defaultdict(int)
    pred_tot=0; pred_hit=0
    # char-level for AUC-style stats
    char_gold=char_pred=char_tp=char_total=0
    for text, gold in rows:
        d=scan(text); preds=pred_spans(text,d)
        pred_tot+=len(preds)
        for ps in preds:
            if any(overlap(ps,(g[0],g[1])) for g in gold): pred_hit+=1
        for (s,e,typ) in gold:
            gt[typ]+=1
            hit=any(overlap((s,e),(p[0],p[1])) for p in preds)
            if hit: tp_r[typ]+=1
            pref=RULE.get(typ,())
            if pref and any(overlap((s,e),(p[0],p[1])) and p[2].startswith(pref) for p in preds): tp_t[typ]+=1
        # char-level
        gset=set()
        for (s,e,_) in gold: gset.update(range(s,e))
        pset=set()
        for (s,e,_) in preds: pset.update(range(s,e))
        char_gold+=len(gset); char_pred+=len(pset); char_tp+=len(gset&pset); char_total+=len(text)
    tg=sum(gt.values()); tr=sum(tp_r.values()); tt=sum(tp_t.values())
    R=tr/tg if tg else 0
    P=pred_hit/pred_tot if pred_tot else 0
    F1=2*P*R/(P+R) if P+R else 0
    # char metrics
    cP=char_tp/char_pred if char_pred else 0
    cR=char_tp/char_gold if char_gold else 0
    cF1=2*cP*cR/(cP+cR) if cP+cR else 0
    spec=1-((char_pred-char_tp)/(char_total-char_gold)) if char_total-char_gold else 1
    print(f"\n### {name}  ({len(rows)} примеров, {tg} PII-спанов)")
    print(f"{'тип':10s} {'gold':>5s} {'recall':>7s} {'type-rec':>9s}")
    for typ in sorted(gt, key=lambda x:-gt[x]):
        print(f"{typ:10s} {gt[typ]:5d} {tp_r[typ]/gt[typ]*100:6.1f}% {tp_t[typ]/gt[typ]*100:8.1f}%")
    print(f"{'—':10s}")
    print(f"span-level:  precision {P*100:.1f}%  recall {R*100:.1f}%  F1 {F1*100:.1f}%")
    print(f"char-level:  precision {cP*100:.1f}%  recall {cR*100:.1f}%  F1 {cF1*100:.1f}%  specificity {spec*100:.2f}%")
    return dict(name=name, n=len(rows), spans=tg, P=P, R=R, F1=F1, cP=cP, cR=cR, cF1=cF1, spec=spec)

if __name__=='__main__':
    N=int(sys.argv[1]) if len(sys.argv)>1 else 2000
    res=[]
    res.append(evaluate("hivetrace/pii-bench", load_hivetrace(10**9)))
    res.append(evaluate("alexen2/pii-ner-ru-benchmark", load_alexen(10**9)))
    res.append(evaluate("alrosait/pii-synthetic-ru", load_alrosait(N)))
    print("\n\n=== СВОДКА (span-level) ===")
    print(f"{'датасет':32s} {'n':>5s} {'prec':>6s} {'rec':>6s} {'F1':>6s}")
    for r in res:
        print(f"{r['name']:32s} {r['n']:5d} {r['P']*100:5.1f}% {r['R']*100:5.1f}% {r['F1']*100:5.1f}%")
    print("\nROC-AUC: не определён — regex-маскер бинарен и не выдаёт score/порог;")
    print("отчёт даёт рабочую точку (precision/recall/F1) и specificity вместо кривой.")
