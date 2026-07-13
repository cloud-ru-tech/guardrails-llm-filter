#!/usr/bin/env python3
"""Generate an EXPANDED test dataset (valid-format vectors + demask edge cases)
and a driver plan. Writes:
  - tests/dataset/guardrails_dataset_expanded.jsonl  (flat, same schema as base)
  - harness/plan_expanded.jsonl                       (driver plan, all 3 APIs x modes)
Values are synthetic (high-entropy, valid-format / valid-checksum) so they
actually exercise detection, or intentional negatives that must NOT mask."""
import json, hashlib, string, os

REPO = os.path.dirname(os.path.dirname(os.path.dirname(os.path.abspath(__file__))))
H = os.path.dirname(os.path.abspath(__file__))

def b62(seed, n):
    a = string.ascii_letters + string.digits
    out=[]; h=hashlib.sha256(seed.encode()).digest(); i=0
    while len(out)<n:
        if i>=len(h): h=hashlib.sha256(h).digest(); i=0
        out.append(a[h[i]%62]); i+=1
    return "".join(out)
def hexs(seed,n):
    out=""
    h=seed
    while len(out)<n:
        h=hashlib.sha256(h.encode()).hexdigest(); out+=h
    return out[:n]
def luhn(pref):
    d=[int(c) for c in pref]; s=0
    for i,x in enumerate(reversed(d)):
        x=x*2 if i%2==0 else x
        if x>9: x-=9
        s+=x
    return pref+str((10-s%10)%10)
def inn12(p10):
    d=[int(c) for c in p10]; c1=[7,2,4,10,3,5,9,4,6,8]; c2=[3,7,2,4,10,3,5,9,4,6,8]
    n11=sum(c1[i]*d[i] for i in range(10))%11%10; d11=d+[n11]
    n12=sum(c2[i]*d11[i] for i in range(11))%11%10; return p10+str(n11)+str(n12)
def ogrn13(p12): return p12+str(int(p12)%11%10)
def snils(p9):
    d=[int(c) for c in p9]; s=sum(d[i]*(9-i) for i in range(9)); c=s%101
    if c==100: c=0
    return f"{p9}{c:02d}"

# valid-format positive vectors
gcp = "AIza"+b62("gcpX",35)
openai_legacy = "sk-"+b62("oaL",20)+"T3BlbkFJ"+b62("oaL2",20)
openai_proj = "sk-proj-"+b62("oaP",74)+"T3BlbkFJ"+b62("oaP2",74)
pplx = "pplx-"+b62("pplxX",48)
pmak = "PMAK-"+hexs("pmak1",24)+"-"+hexs("pmak2",34)
slack = "xoxb-"+"123456789012"+"-"+"210987654321"+"-"+b62("slk",24)
aws = "AKIA"+ "".join(c for c in b62("aws",16).upper() if c.isalnum())[:16]
card = luhn("453212345678901")  # 16-digit visa-ish, valid Luhn
inn = inn12("7712345678")
ogrn = ogrn13("115774612345")
sn = snils("112233445")
email = "team.qa+eu@example.com"

POS = [
 ("exp.email","personal_data",["EMAIL"], f"пришли отчёт на {email} до пятницы"),
 ("exp.gcp","api_keys",["GCP_API_KEY"], f"в конфиге ключ {gcp} используется"),
 ("exp.openai.legacy","api_keys",["OPENAI"], f"старый ключ {openai_legacy} в .env"),
 ("exp.openai.proj","api_keys",["OPENAI"], f"новый ключ {openai_proj} в секрете"),
 ("exp.pplx","api_keys",["PERPLEXITY"], f"токен {pplx} для perplexity"),
 ("exp.pmak","api_keys",["POSTMAN"], f"в коллекции токен {pmak}"),
 ("exp.slack","access_tokens",["SLACK"], f"бот-токен {slack} в чате"),
 ("exp.card","personal_data",["CREDIT_CARD"], f"оплата картой {card} прошла"),
 ("exp.inn","personal_data",["INN"], f"ИНН {inn} в договоре"),
 ("exp.ogrn","personal_data",["OGRN"], f"ОГРН {ogrn} в реестре"),
 ("exp.snils","personal_data",["SNILS"], f"СНИЛС {sn} сотрудника"),
 # dedup: same email twice -> same placeholder
 ("exp.dedup","personal_data",["EMAIL"], f"с {email} и снова {email} свяжись"),
 # multiple distinct secrets in one text
 ("exp.multi","mixed",["EMAIL","GCP_API_KEY","CREDIT_CARD"], f"почта {email}, ключ {gcp}, карта {card}"),
 # unicode/emoji adjacent to a secret
 ("exp.unicode","personal_data",["EMAIL"], f"контакт 👉{email}👈 ✅ спасибо, ёжик"),
 # adversarial: input already contains a literal placeholder-looking token + a real secret
 ("exp.literalph","personal_data",["EMAIL"], f"шаблон <EMAIL_1> и реальный {email}"),
 # secret at very start and very end
 ("exp.edges","personal_data",["EMAIL"], f"{email} в начале и в конце {email}"),
]
# negative cases (must NOT mask): invalid checksums / non-secret text
NEG = [
 ("exp.neg.plain","none",[], "просто обычное сообщение без секретов, всё хорошо"),
 ("exp.neg.badluhn","none",[], "номер 4532123456789010 не проходит проверку"),  # invalid Luhn
 ("exp.neg.shortkey","none",[], f"строка AIza{b62('short',10)} слишком короткая"),
]

def write_dataset():
    os.makedirs(f"{REPO}/tests/dataset", exist_ok=True)
    with open(f"{REPO}/tests/dataset/guardrails_dataset_expanded.jsonl","w",encoding="utf-8") as f:
        for sid,cat,exp,content in POS:
            f.write(json.dumps({"id":sid,"category":cat,"rule_ids":[],"content":content,
                                "expected_placeholders":exp,"expect_masked":True},ensure_ascii=False)+"\n")
        for sid,cat,exp,content in NEG:
            f.write(json.dumps({"id":sid,"category":cat,"rule_ids":[],"content":content,
                                "expected_placeholders":exp,"expect_masked":False},ensure_ascii=False)+"\n")

def body_for(ep, content, stream):
    if ep=="chat": return {"model":"demo","stream":stream,"messages":[{"role":"user","content":content}]}
    if ep=="messages": return {"model":"demo","stream":stream,"max_tokens":256,"messages":[{"role":"user","content":content}]}
    if ep=="responses": return {"model":"demo","stream":stream,"input":content}

def write_plan():
    cases=[]
    allrows = [(s,c,e,ct,True) for s,c,e,ct in POS] + [(s,c,e,ct,False) for s,c,e,ct in NEG]
    for sid,cat,exp,content,expect in allrows:
        for ep in ("chat","messages","responses"):
            for stream in (False,True):
                case={"id":f"{sid}|{ep}|{'s' if stream else 'n'}","endpoint":ep,"stream":stream,
                      "body":body_for(ep,content,stream),"expected_placeholders":exp,
                      "expect_masked":expect,"category":cat}
                if stream: case["chunk"]=1   # extreme fragmentation: 1 rune per SSE frame
                if sid=="exp.literalph": case["known_collision"]=True
                cases.append(case)
    with open(f"{H}/plan_expanded.jsonl","w",encoding="utf-8") as f:
        for c in cases: f.write(json.dumps(c,ensure_ascii=False)+"\n")
    return len(cases)

write_dataset()
n=write_plan()
print(f"wrote expanded dataset ({len(POS)+len(NEG)} entries) and plan ({n} cases, streaming chunk=1)")
