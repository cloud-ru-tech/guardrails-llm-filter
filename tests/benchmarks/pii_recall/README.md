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
| INN | 98% | | TOKEN | 67% |
| KPP | 98% | | CVC | 62% |
| OGRNIP | 98% | | ADDRESS | 30% |
| SNILS | 96% | | BANK_CARD | 4%* |
| PHONE | 95% | | | |

**Micro-recall 79.1%, precision 99.8%.**

Пояснения:
- **BANK_CARD 4%** — артефакт датасета: 88/92 карт Luhn-невалидны, наше правило
  корректно их отвергает; реальная (Luhn-валидная) карта маскируется.
- **ADDRESS 30%** — regex ловит адреса с маркером улицы (ул./пр./…); свободные
  адреса без маркера и строчные — территория ML-NER.
- **NAME 84%** — промахи на одиночных/строчных/иностранных именах (regex vs NER).
