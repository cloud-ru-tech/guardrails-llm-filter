# PII-recall benchmark (152-ФЗ)

Прогоняет боевой путь маскирования (`POST /v1/scan` на management-порту)
против русскоязычного датасета [`hivetrace/pii-bench`](https://huggingface.co/datasets/hivetrace/pii-bench)
(1810 примеров, span-разметка 13 типов ПДн) и считает per-type recall/precision.

Recall — метрика защиты от утечки: было ли значение замаскировано ЛЮБЫМ
правилом. Precision — доля наших масок, попавших в размеченный PII-спан.

## Запуск

```sh
pip install datasets
# поднимите сервис (management API на :9080), затем:
python3 tests/benchmarks/pii_recall/bench.py
```

## Результат (последний прогон)

| Тип | recall | | Тип | recall |
|---|---|---|---|---|
| OGRN | 100% | | PASSPORT | 87% |
| EMAIL | 99% | | NAME (ФИО) | 84% |
| INN | 98% | | TOKEN | 76% |
| KPP | 98% | | ADDRESS | 72% |
| OGRNIP | 98% | | CVC | 71% |
| SNILS | 96% | | BANK_CARD | 54%* |
| PHONE | 95% | | | |

**Micro-recall 87.2%, precision 99.9%.**

Пояснения:
- **BANK_CARD 54%** — артефакт датасета: 88/92 карт Luhn-невалидны, строгое
  правило корректно их отвергает (защита от FP на реальном трафике). Keyword-гейтовый
  fallback (`pii.fin.credit-card.context`) добирает карты вплотную к слову
  «карта/card», подняв recall с 4% до 54%; реальная (Luhn-валидная) карта — ~100%.
- **ADDRESS 72%** — regex ловит адреса с маркером улицы (ул./пр./…), маркером
  после названия («Невский проспект 88») и с якорем кв/подъезд; строчные адреса
  без заглавных — территория ML-NER.
- **TOKEN 76%** — keyword/prefix/OTP + keyword-free для mixed-case; строчные
  hex-токены неотличимы от git-хешей и намеренно не маскируются.
- **NAME 84%** — промахи на одиночных/строчных/иностранных именах.
