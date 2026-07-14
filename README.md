<div align="center">

<img src="docs/images/logo.svg" width="84" alt="Cloud.ru Guardrails" />

# guardrails-llm-filter

**Маскирование PII и секретов в трафике к LLM — как самостоятельный HTTP-сервис.**

Прозрачный обратный прокси между вашими клиентами и LLM-провайдером: убирает
чувствительные данные из запросов к модели и восстанавливает их в ответах.

[![License](https://img.shields.io/badge/license-Apache--2.0-blue.svg)](LICENSE)
[![Go](https://img.shields.io/badge/Go-1.26-00ADD8?logo=go&logoColor=white)](go.mod)
[![Release](https://img.shields.io/badge/release-v0.1.0-2ea44f)](CHANGELOG.md)
[![PRs welcome](https://img.shields.io/badge/PRs-welcome-brightgreen.svg)](CONTRIBUTING.md)
[![Made by Cloud.ru](https://img.shields.io/badge/made%20by-Cloud.ru-26D07C)](https://cloud.ru)

</div>

---

`guardrails-llm-filter` стоит между вашими клиентами и LLM-провайдером. Клиенты шлют
запросы ему, а не провайдеру напрямую.

По пути к модели он ищет в теле запроса чувствительные данные — по набору regex-правил
(~260 встроенных: учётные данные, API-ключи, access-токены, IP-адреса, персональные
данные) — и заменяет найденное безопасными заглушками-плейсхолдерами вида `<EMAIL_1>`.
Затем сам пересылает уже очищенный запрос провайдеру, а в ответе возвращает оригиналы на
место. Для клиента это незаметно; потоковые ответы (SSE, токен за токеном) тоже работают.

```
клиент ──► guardrails-llm-filter ──[маскирование]──► LLM-провайдер
   ▲                 │
   └──[восстановление]┘
```

Провайдер никогда не видит чувствительные значения, а клиент никогда не видит заглушки.
Отдельный прокси (Envoy) или сайдкар не нужны — `guardrails-llm-filter` сам пропускает
через себя трафик. (Если у вас уже есть свой LLM-шлюз на Envoy, тот же движок можно
подключить к нему сбоку — см. родственный проект
[`guardrails-llm-filter-extproc`](https://github.com/cloud-ru-tech/guardrails-llm-filter-extproc).)

## Возможности

- 🛡️ **~260 встроенных правил**: учётные данные, API-ключи, access-токены, IP-адреса,
  персональные данные (российские PII, карты, IBAN, СНИЛС/ИНН/ОГРН — с проверкой
  контрольных сумм).
- 🔄 **Оригиналы возвращаются в ответ автоматически** — включая потоковые ответы и
  аргументы вызова инструментов (tool-call). Клиент видит настоящие данные, модель —
  только заглушки.
- 🔌 **OpenAI и Anthropic из коробки**: `/v1/chat/completions`, `/v1/responses`,
  `/v1/messages` — обычные и потоковые ответы.
- 🟢 **Не мешает работе при сбое**: если внутри что-то пошло не так, запрос проходит как
  есть, а не обрывается.
- 👁️ **Режим наблюдения (detect)**: посмотрите, *что* было бы замаскировано, не трогая сам
  трафик, и переключитесь на боевой режим (`enforce`) через API без перезапуска.
- 🎛️ **Встроенная веб-консоль**: обзор срабатываний, правила, песочница, настройки и
  журнал аудита.
- 📊 **Наблюдаемость**: метрики Prometheus, дашборд Grafana, журнал аудита.

## Веб-консоль

Управление правилами, настройками и журналом аудита **встроено прямо в бинарь** и
открывается на `/` на management-порту (`:9080`) — тот же образ, без отдельного
веб-сервера и без возни с CORS.

<div align="center">

### Обзор — сводка срабатываний по журналу аудита
<img src="docs/images/overview.png" width="900" alt="Дашборд «Обзор»" />

</div>

| Песочница — прогон текста через боевой путь | Журнал аудита — деталь записи |
|:--:|:--:|
| [<img src="docs/images/tester.png" alt="Песочница" />](docs/images/tester.png) | [<img src="docs/images/audit-detail.png" alt="Деталь аудита" />](docs/images/audit-detail.png) |
| **Правила детекции** — 258 встроенных + пользовательские | **Настройки** — единая политика инстанса |
| [<img src="docs/images/rules.png" alt="Правила" />](docs/images/rules.png) | [<img src="docs/images/settings.png" alt="Настройки" />](docs/images/settings.png) |

> Консоль включена по умолчанию (`GUARDRAILS_UI_ENABLED=false` отключает) и находится за
> той же границей доверия, что и config API: держите её **внутри кластера, не выставляйте
> в интернет**. Есть переключатель светлой и тёмной темы.

## Быстрый старт

Укажите `guardrails-llm-filter` адрес вашего LLM-провайдера, запустите его и шлите запросы
на `:8080` вместо провайдера:

```sh
make build
# адрес провайдера: https://foundation-models.api.cloud.ru/v1/chat/completions
GUARDRAILS_UPSTREAM_BASE_URL=https://foundation-models.api.cloud.ru \
  ./bin/guardrails-llm-filter

# в другом терминале — тот же запрос, что вы бы послали провайдеру, но другой хост:
curl -sS http://localhost:8080/v1/chat/completions \
  -H 'Content-Type: application/json' \
  -H "Authorization: Bearer $CLOUDRU_API_KEY" \
  -d '{"model":"ai-sage/GigaChat3-10B-A1.8B","messages":[{"role":"user","content":"email me at a@b.com"}]}'
```

Провайдер получает `<EMAIL_1>` вместо адреса; в ответе, который получаете вы, оригинал
восстановлен.

> Ключ `CLOUDRU_API_KEY` и список доступных моделей — в
> [документации Cloud.ru Foundation Models](https://cloud.ru/docs/foundation-models/ug/topics/quickstart).

### Запускаемое демо (реальный провайдер не нужен)

```sh
cd examples/quickstart
docker compose up --build      # guardrails-llm-filter + фейковый LLM, который отвечает эхом
bash demo.sh                   # шлёт промпты с выдуманными email и картой; показывает,
                               # что ушло провайдеру (замаскировано) и что вернулось клиенту
```

В демо включён аудит с сохранением текстов запроса и ответа — откройте консоль на
<http://localhost:9080> и посмотрите страницы «Обзор» и «Аудит» (как на скриншотах выше).

## Как это работает

Весь путь запроса проходит в одном обработчике на одной реплике: прочитать запрос клиента
→ замаскировать → переслать провайдеру → получить ответ и восстановить в нём оригиналы
(целиком, а для потоковых ответов — по мере поступления) → отдать клиенту. Запрос и ответ
на него обрабатываются вместе, поэтому таблица «заглушка → оригинал» просто хранится в
памяти процесса, пока идёт запрос. Отдельное хранилище для прохождения трафика не нужно.

- **Поддерживаемые API**: OpenAI `/v1/chat/completions`, `/v1/responses`, Anthropic
  `/v1/messages` — обычные и потоковые ответы, включая аргументы вызова инструментов.
  Пути сравниваются по окончанию, поэтому вложенные варианты (`/openai/v1/chat/completions`)
  работают сразу; свои пути задаются через `GUARDRAILS_PATHS`. Если тело запроса не удаётся
  разобрать, он проходит без маскирования (безопасное поведение при сбое) и увеличивает
  счётчик `extproc_guardrails_unsupported_body_schema_total`. Любой другой путь просто
  проксируется провайдеру без изменений.
- **Что делает сервис с находкой**: только заменяет значение на заглушку. Запросы он
  никогда не блокирует.

Полный разбор пути запроса, движок правил и устройство хранилища — в
[`docs/`](docs/README.md).

## Конфигурация

Все переменные с префиксом `GUARDRAILS_`.

| Переменная | По умолчанию | Описание |
|---|---|---|
| `GUARDRAILS_LISTEN_ADDR` | `:8080` | data-plane HTTP-адрес (сюда обращаются клиенты) |
| `GUARDRAILS_UPSTREAM_BASE_URL` | — | **обязательно**: базовый URL upstream LLM-провайдера; путь запроса дописывается к нему |
| `GUARDRAILS_UPSTREAM_TIMEOUT` | `120s` | таймаут заголовков ответа upstream (время до первого байта); не ограничивает стриминговое тело, чья жизнь следует за соединением клиента |
| `GUARDRAILS_UPSTREAM_MAX_IDLE_CONNS` | `100` | пул соединений upstream: максимум idle-соединений |
| `GUARDRAILS_UPSTREAM_MAX_IDLE_CONNS_PER_HOST` | `100` | пул соединений upstream: максимум idle на хост |
| `GUARDRAILS_UPSTREAM_IDLE_CONN_TIMEOUT` | `90s` | пул соединений upstream: таймаут idle-соединения |
| `GUARDRAILS_UPSTREAM_PATH_BASE_URLS` | — | per-path переопределения базового URL как пары `path=url` через запятую (например, `/v1/messages=https://api.anthropic.com`); путь не из списка использует `UPSTREAM_BASE_URL` |
| `GUARDRAILS_UPSTREAM_INSECURE_SKIP_VERIFY` | `false` | ⚠️ отключает проверку TLS upstream — только для локального тестирования |
| `GUARDRAILS_MAX_REQUEST_BYTES` | `33554432` (32 MiB) | лимит тела запроса до маскирования; превышение → 413; `0` отключает |
| `GUARDRAILS_LOG_LEVEL` | `info` | `debug` \| `info` \| `warn` \| `error` |
| `GUARDRAILS_LOG_FORMAT` | `json` | `json` \| `text` |
| `GUARDRAILS_METRICS_PORT` | `9090` | порт метрик Prometheus |
| `GUARDRAILS_GRPC_ADDR` | `:9000` | management gRPC-адрес (`GuardrailsApi`); REST-API проксирует на него |
| `GUARDRAILS_GRPC_SECURE` | `false` | self-signed TLS на management gRPC-listener; по умолчанию выкл (API рассчитан на работу внутри кластера) |
| `GUARDRAILS_ENABLED` | `true` | глобальный вкл/выкл (seed-значение) |
| `GUARDRAILS_MODE` | `enforce` | `detect` = shadow-режим: скан + метрики/аудит, трафик не тронут (seed) |
| `GUARDRAILS_DATA_TYPES` | `1,2,3,4,5,6` | включённые типы данных, числа или имена (`6`/CUSTOM включает кастомные правила из API) |
| `GUARDRAILS_KEYWORD_PREFILTER_ENABLED` | `false` | сохраняющий полноту keyword-пре-фильтр (ускоряет скан) |
| `GUARDRAILS_MASK_PARALLEL_MIN_BYTES` | `8192` | суммарный размер текстов (байты), с которого скан распараллеливается по полям (нужно ≥2 поля); `0` — встроенное значение |
| `GUARDRAILS_PATHS` | 3 стандартных пути | пары `path:format` (`chat_completions`, `messages`, `responses`); суффиксный матчинг, подмешиваются поверх дефолтов |
| `GUARDRAILS_OVERRIDE_HEADER` | `x-guardrails-data-types` | per-request заголовок сужения (потребляется, не форвардится); пусто отключает |
| `GUARDRAILS_SETTINGS_REFRESH_INTERVAL` | `30s` | интервал перечитывания настроек (сходимость реплик); `0` отключает |
| `GUARDRAILS_RULES_REFRESH_INTERVAL` | `30s` | интервал перечитывания кастомных правил; `0` отключает |
| `GUARDRAILS_RULES_REGEX_RULES_FILE` | `./configs/guardrails_regex_rules.yaml` | ручной файл правил |
| `GUARDRAILS_RULES_GITLEAKS_REGEX_RULES_FILE` | `./configs/guardrails_regex_rules.gitleaks.generated.yaml` | генерируемый файл правил |
| `GUARDRAILS_HEADERS_DATA_TYPES_HEADER` | `x-guardrails-data-types-triggered` | заголовок ответа со сработавшими типами данных |
| `GUARDRAILS_HEADERS_TRIGGERED_RULES_HEADER` | `x-guardrails-triggered-rules` | заголовок ответа со сработавшими ID правил |
| `GUARDRAILS_HEADERS_EXPOSE_TRIGGERED_RULES` | `false` | эмитить заголовок сработавших правил |
| `GUARDRAILS_STORE_BACKEND` | `in_memory` | `in_memory` \| `redis` \| `postgres` — хранит кастомные правила, настройки и аудит (не masking state data-path, который в процессе) |
| `GUARDRAILS_STORE_MASKING_TTL` | `15m` | страховочный TTL masking state (для межрепличного fallback); должен превышать самый длинный стриминговый ответ |
| `GUARDRAILS_STORE_REDIS_ADDR` | `redis:6379` | адрес redis-бэкенда |
| `GUARDRAILS_STORE_REDIS_PASSWORD` | — | пароль redis |
| `GUARDRAILS_STORE_REDIS_DB` | `0` | база redis |
| `GUARDRAILS_STORE_POSTGRES_DSN` | — | DSN postgres |
| `GUARDRAILS_STORE_ENCRYPTION_ENABLED` | `false` | AES-256-GCM-шифрование masking state в redis/postgres на месте (no-op для in_memory) |
| `GUARDRAILS_STORE_ENCRYPTION_KEY` | — | base64 32-байтный ключ (`openssl rand -base64 32`); обязателен при включённом шифровании |
| `GUARDRAILS_API_ADDR` | `:9080` | адрес config API; пусто отключает API |
| `GUARDRAILS_UI_ENABLED` | `true` | отдавать встроенную веб-консоль на `/` на порту API (no-op, если бинарь собран без UI) |
| `GUARDRAILS_AUDIT_ENABLED` | `false` | аудит-трейл маскирования + эндпоинты `/v1/audit` |
| `GUARDRAILS_AUDIT_STORE_MASKED_TEXTS` | `false` | дополнительно хранить маскированные тексты запроса (пользовательский контент — см. [SECURITY.md](SECURITY.md)) |
| `GUARDRAILS_AUDIT_STORE_MASKED_RESPONSE_TEXTS` | `false` | дополнительно хранить маскированные тексты ответа модели (тот же класс чувствительности) |
| `GUARDRAILS_AUDIT_STORE_ORIGINAL_TEXTS` | `off` | хранить оригинал за каждым плейсхолдером для «показа по наведению» в UI: `off` \| `plain` \| `encrypted`. `encrypted` переиспользует ключ AES-256-GCM хранилища и требует `GUARDRAILS_STORE_ENCRYPTION_ENABLED`. БЕЗОПАСНОСТЬ: `plain`/`encrypted` сохраняют сырые чувствительные данные — ограничьте доступ к хранилищу |
| `GUARDRAILS_AUDIT_RETENTION` | `24h` | сколько хранятся аудит-записи |
| `GUARDRAILS_AUDIT_MAX_ENTRIES` | `10000` | только для `in_memory`: лимит аудит-записей (вытесняется старейшее) |

Типы данных: `1 CREDENTIALS`, `2 API_KEYS`, `3 ACCESS_TOKENS`, `4 IP_ADDRESSES`,
`5 PERSONAL_DATA`, `6 CUSTOM`. Имена принимаются регистронезависимо всюду, где принимаются
числа.

Переменные окружения задают глобальные настройки **только при первом старте**. Дальше
главный источник — config API (`GET/PUT /v1/settings`); настройки перечитываются каждые
`SETTINGS_REFRESH_INTERVAL`. Заголовок в отдельном запросе умеет **только сужать** набор
проверок: он может выбрать подмножество включённых типов данных, но не добавить
выключенные. Значение `none` отключает маскирование для этого запроса, а нераспознанное
значение просто игнорируется (в пользу защиты).

## Config API

Отдельный HTTP API (`GUARDRAILS_API_ADDR`, по умолчанию `:9080`) управляет своими
правилами и настройками и отдаёт журнал аудита: `GET/PUT /v1/settings`,
`GET/POST/DELETE /v1/rules[...]`, `PATCH /v1/rules/{id}` (включить/выключить),
`GET /v1/audit/records`. API описан в proto-контракте (сервис `GuardrailsApi`): из него
генерируются gRPC-сервер на `GUARDRAILS_GRPC_ADDR` (`:9000`) и REST-обёртка над ним на
`GUARDRAILS_API_ADDR` (`:9080`). Общая спецификация OpenAPI v2 лежит в
[`service.swagger.json`](service.swagger.json). У API **нет своей аутентификации** —
закрывайте его на уровне сети: держите внутри кластера и не выставляйте наружу.

Пример — добавить своё правило (оно проверяется так же, как встроенные, и применяется сразу,
без перезапуска):

```sh
curl -X POST localhost:9080/v1/rules -H 'Content-Type: application/json' -d '{
  "rule_id": "acme_token",
  "name": "ACME internal token",
  "data_type": 6,
  "regex": "\\bacme-[0-9a-f]{8}\\b",
  "masking": {"placeholder": "ACME_TOKEN"}
}'
```

Не забудьте включить тип данных 6 (`custom`) в настройках, иначе такие правила работать не
будут.

## Наблюдаемость

- **Проверки состояния**: `GET /healthz` (жив ли процесс) и `GET /readyz` (готов ли
  принимать трафик) на порту, куда обращаются клиенты.
- **Метрики**: Prometheus на `GUARDRAILS_METRICS_PORT` (`/metrics`), имена начинаются с
  `extproc_guardrails`. Правила алертов и дашборд Grafana — в [`deploy/`](deploy/).
- **Журнал аудита** (`GUARDRAILS_AUDIT_ENABLED`): по одной записи на каждый замаскированный
  запрос — какие правила и типы данных сработали и какими заглушками заменены значения
  (страница «Аудит» в консоли).

## Деплой

Манифесты для Kubernetes (Kustomize) — в [`deploy/kubernetes/`](deploy/kubernetes/):
Deployment с проверками `/healthz` и `/readyz`, Service, ConfigMap и Secret. Укажите
`GUARDRAILS_UPSTREAM_BASE_URL` в ConfigMap. Образ (минимальный, distroless) собирается по
[`Dockerfile`](Dockerfile).

## Разработка

```sh
make build       # бинарь -> ./bin/guardrails-llm-filter
make frontend    # собрать SPA-консоль в frontend/dist (вшивается в make build)
make test        # go test -race ./...  (postgres-хранилище нужен Docker; авто-скип)
make test-short  # без Docker
make lint        # golangci-lint run
make rules-gen   # перегенерировать gitleaks-файл правил из configs/gitleaks.toml
make demo-up     # сквозное демо: guardrails-llm-filter + mock LLM (examples/quickstart)
```

См. [CONTRIBUTING.md](CONTRIBUTING.md) и [`docs/`](docs/README.md).

## Лицензия

Apache-2.0 — см. [LICENSE](LICENSE). Включает правила, производные от
[gitleaks](https://github.com/gitleaks/gitleaks) (MIT), см. [NOTICE](NOTICE).
