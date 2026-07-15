# Многодатасетная оценка PII-маскирования

`multibench.py` прогоняет боевой путь маскирования (`POST /v1/scan`) против
**четырёх** независимых русскоязычных PII-датасетов HuggingFace и считает
precision / recall / F1 (span-level и char-level) + specificity.

```sh
pip install datasets
python3 tests/benchmarks/pii_recall/multibench.py 2000   # 2000 = сэмпл на датасет
```

## Датасеты

| Датасет | Формат | Что покрывает |
|---|---|---|
| [hivetrace/pii-bench](https://huggingface.co/datasets/hivetrace/pii-bench) | span-offset, 13 типов | ФИО, паспорт, СНИЛС, ИНН, ОГРН/ОГРНИП, КПП, адрес, телефон, email, карта, CVC, токен |
| [wolframko/russian-pii-66k](https://huggingface.co/datasets/wolframko/russian-pii-66k) | span-offset (ai4privacy) | ФИО, паспорт, СНИЛС, ИНН, адрес, телефон, email, карта, IBAN, ДР |
| [alexen2/pii-ner-ru-benchmark](https://huggingface.co/datasets/alexen2/pii-ner-ru-benchmark) | BIO-токены | ФИО, телефон, email |
| [alrosait/pii-synthetic-ru](https://huggingface.co/datasets/alrosait/pii-synthetic-ru) | span (text+type) | ФИО, адрес + негативы |

## Результаты (span-level, последний прогон)

| Датасет | n | precision | recall | F1 |
|---|---|---|---|---|
| hivetrace/pii-bench | 1810 | **99.9%** | 84.5% | **91.5%** |
| wolframko/russian-pii-66k | 2000 | **99.9%** | 51.3% | 67.8% |
| alexen2/pii-ner-ru-benchmark | 1000 | 98.9% | 82.2% | 89.8% |
| alrosait/pii-synthetic-ru | 2000 | 91.9% | 67.9% | 78.1% |

**Precision стабильно 92–100% на всех датасетах** — маскирование почти не
трогает не-PII. Recall варьируется от качества/разметки датасета.

### Почему recall различается

- **Структурированные PII с контрольными суммами** (ИНН, ОГРН, телефон, email,
  IBAN): 92–100% там, где значения датасета валидны.
- **Checksum-gated типы** (СНИЛС, карта): recall = доля checksum-валидных
  значений. У wolframko синтетические СНИЛС в основном невалидны (3.5%) и карты
  частично (77%) — наши валидаторы их корректно отвергают, реальные PII
  маскируются. Это качество датасета, не пробел детекции.
- **ФИО**: 84% на реалистичных именах (hivetrace) vs 19% на редких/иностранных
  ai4privacy-именах (wolframko) — словарь коротких имён покрывает частотные.
- **Адрес**: 72–76% там, где адрес полный; ниже на покомпонентной разметке
  (wolframko метит CITY/STREET/BUILDINGNUM раздельно).
- **ДР (дата рождения)**: правила нет — 0%.

## ROC-AUC и почему его здесь нет

ROC-AUC/PR-AUC требуют **непрерывного score** классификатора, по которому
двигают порог и строят кривую. Regex-маскер **детерминированно бинарен** — он
либо маскирует значение, либо нет, порога нет. Поэтому у системы одна рабочая
точка (precision/recall), а не кривая, и ROC-AUC для неё не определён.

Вместо кривой отчёт даёт корректные для masking-гейта метрики:
- **recall** (TPR) — защита от утечки: какая доля PII замаскирована;
- **precision** — какая доля масок попала в реальный PII;
- **F1** — их гармоническое среднее (рабочая точка);
- **specificity** (1 − FPR на не-PII символах) — 99.0–99.98% на всех датасетах.
