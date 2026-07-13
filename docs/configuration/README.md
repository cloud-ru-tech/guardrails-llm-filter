# Конфигурация

Только окружение (`internal/config/config.go`, `caarlos0/env`), единый префикс
`GUARDRAILS_` через `env.ParseWithOptions(..., Options{Prefix})`. Вложенные структуры
добавляют свои под-префиксы (`STORE_`, `API_`, `UI_`, `AUDIT_`, `UPSTREAM_`, `RULES_`,
`HEADERS_`; у policy-структуры `Guardrails` под-префикс намеренно **пустой**, поэтому её
поля читаются как `GUARDRAILS_ENABLED` и т. п.).

Семантика резолюции настроек, модель доверия к заголовкам и логирование — в
[settings.md](settings.md).

## Справочник

| Переменная | По умолчанию | Примечания |
|---|---|---|
| `GUARDRAILS_LOG_LEVEL` | `info` | debug/info/warn/error |
| `GUARDRAILS_LOG_FORMAT` | `json` | `text` для локальной разработки |
| `GUARDRAILS_LISTEN_ADDR` | `:8080` | data-plane HTTP-адрес (сюда обращаются клиенты) |
| `GUARDRAILS_MAX_REQUEST_BYTES` | `33554432` (32 MiB) | лимит тела запроса, читаемого в память до маскирования; превышение → 413; `0` отключает лимит |
| `GUARDRAILS_METRICS_PORT` | `9090` | порт метрик Prometheus |
| `GUARDRAILS_GRPC_ADDR` | `:9000` | management gRPC-адрес (`GuardrailsApi`); REST-API проксирует на него |
| `GUARDRAILS_GRPC_SECURE` | `false` | self-signed TLS на management gRPC-listener (`pkg/tlsutils`); по умолчанию выкл — API рассчитан на работу внутри кластера |
| `GUARDRAILS_UPSTREAM_BASE_URL` | — | **обязательно**: базовый URL upstream LLM-провайдера; путь запроса дописывается к нему |
| `GUARDRAILS_UPSTREAM_TIMEOUT` | `120s` | таймаут заголовков ответа upstream (время до первого байта); не ограничивает стриминговое тело; `0` отключает |
| `GUARDRAILS_UPSTREAM_MAX_IDLE_CONNS` | `100` | пул соединений upstream: максимум idle-соединений |
| `GUARDRAILS_UPSTREAM_MAX_IDLE_CONNS_PER_HOST` | `100` | пул соединений upstream: максимум idle на хост |
| `GUARDRAILS_UPSTREAM_IDLE_CONN_TIMEOUT` | `90s` | пул соединений upstream: таймаут idle-соединения |
| `GUARDRAILS_UPSTREAM_PATH_BASE_URLS` | — | per-path переопределения базового URL как пары `path=url` через запятую (например, `/v1/messages=https://api.anthropic.com`); путь не из списка использует `UPSTREAM_BASE_URL` |
| `GUARDRAILS_UPSTREAM_INSECURE_SKIP_VERIFY` | `false` | ⚠️ отключает проверку TLS-сертификата upstream — только для локального тестирования |
| `GUARDRAILS_ENABLED` | `true` | глобальный вкл/выкл (seed-значение — см. settings.md) |
| `GUARDRAILS_MODE` | `enforce` | `detect` = shadow-режим: скан + метрики/аудит, трафик не тронут (seed-значение) |
| `GUARDRAILS_DATA_TYPES` | `1,2,3,4,5,6` | включённые типы данных, числа или имена; `6`/CUSTOM включает кастомные правила из API — без него они молча не сканируются |
| `GUARDRAILS_KEYWORD_PREFILTER_ENABLED` | `false` | сохраняющий полноту keyword-пре-фильтр (ускоряет скан) — см. [../rules-engine/](../rules-engine/) |
| `GUARDRAILS_MASK_PARALLEL_MIN_BYTES` | `8192` | суммарный размер текстов (байты), с которого скан распараллеливается по полям (нужно ≥2 поля); `0` — встроенное значение |
| `GUARDRAILS_PATHS` | 3 стандартных пути | пары `path:format` (`chat_completions`, `messages`, `responses`); суффиксный матчинг, подмешиваются поверх дефолтов |
| `GUARDRAILS_OVERRIDE_HEADER` | `x-guardrails-data-types` | per-request заголовок сужения (потребляется, не форвардится); пусто отключает |
| `GUARDRAILS_SETTINGS_REFRESH_INTERVAL` | `30s` | интервал перечитывания настроек (сходимость реплик); `0` отключает |
| `GUARDRAILS_RULES_REFRESH_INTERVAL` | `30s` | интервал перечитывания кастомных правил; `0` отключает |
| `GUARDRAILS_RULES_REGEX_RULES_FILE` | `./configs/guardrails_regex_rules.yaml` | ручной файл правил |
| `GUARDRAILS_RULES_GITLEAKS_REGEX_RULES_FILE` | `./configs/guardrails_regex_rules.gitleaks.generated.yaml` | генерируемый файл правил |
| `GUARDRAILS_RULES_MAX_CUSTOM` | `500` | максимум кастомных правил через API; `0` = без лимита; превышение → 409 |
| `GUARDRAILS_RULES_MAX_PATTERN_LEN` | `4096` | максимум длины regex кастомного правила в байтах; `0` = без лимита; превышение → 400 |
| `GUARDRAILS_HEADERS_DATA_TYPES_HEADER` | `x-guardrails-data-types-triggered` | заголовок ответа со сработавшими типами данных |
| `GUARDRAILS_HEADERS_TRIGGERED_RULES_HEADER` | `x-guardrails-triggered-rules` | заголовок ответа со сработавшими ID правил |
| `GUARDRAILS_HEADERS_EXPOSE_TRIGGERED_RULES` | `false` | эмитить заголовок сработавших правил (раскрывает детекторы → opt-in) |
| `GUARDRAILS_STORE_BACKEND` | `in_memory` | `in_memory` \| `redis` \| `postgres` — хранит кастомные правила, настройки и аудит (не masking state data-path, который в процессе) |
| `GUARDRAILS_STORE_MASKING_TTL` | `15m` | страховочный TTL masking state (для межрепличного fallback); должен превышать самый длинный стриминговый ответ |
| `GUARDRAILS_STORE_REDIS_ADDR` | `redis:6379` | адрес redis-бэкенда |
| `GUARDRAILS_STORE_REDIS_PASSWORD` | — | пароль redis |
| `GUARDRAILS_STORE_REDIS_DB` | `0` | база redis |
| `GUARDRAILS_STORE_POSTGRES_DSN` | — | DSN postgres |
| `GUARDRAILS_STORE_ENCRYPTION_ENABLED` | `false` | AES-256-GCM-шифрование masking state в redis/postgres на месте (no-op для in_memory) — см. [../storage/](../storage/) |
| `GUARDRAILS_STORE_ENCRYPTION_KEY` | — | base64 32-байтный ключ (`openssl rand -base64 32`); обязателен при включённом шифровании; не логируется |
| `GUARDRAILS_API_ADDR` | `:9080` | адрес config API (grpc-gateway REST); пусто отключает API |
| `GUARDRAILS_UI_ENABLED` | `true` | отдавать встроенную веб-консоль на `/` на порту API (no-op, если бинарь собран без UI) |
| `GUARDRAILS_AUDIT_ENABLED` | `false` | пофазовый аудит маскирования + эндпоинты `/v1/audit` |
| `GUARDRAILS_AUDIT_STORE_MASKED_TEXTS` | `false` | дополнительно хранить маскированные тексты запроса (пользовательский контент — см. [SECURITY.md](../../SECURITY.md)) |
| `GUARDRAILS_AUDIT_STORE_MASKED_RESPONSE_TEXTS` | `false` | дополнительно хранить маскированные тексты ответа модели (тот же класс чувствительности) |
| `GUARDRAILS_AUDIT_STORE_ORIGINAL_TEXTS` | `off` | хранить оригинал за каждым плейсхолдером для «показа по наведению» в UI: `off` \| `plain` \| `encrypted`. `encrypted` переиспользует ключ AES-256-GCM хранилища и требует `GUARDRAILS_STORE_ENCRYPTION_ENABLED`. БЕЗОПАСНОСТЬ: `plain`/`encrypted` сохраняют сырые чувствительные данные — ограничьте доступ к хранилищу |
| `GUARDRAILS_AUDIT_RETENTION` | `24h` | сколько хранятся аудит-записи |
| `GUARDRAILS_AUDIT_MAX_ENTRIES` | `10000` | только для in_memory: лимит аудит-записей (вытесняется старейшее) |

## Матчинг путей (`GUARDRAILS_PATHS`)

По умолчанию: `/v1/chat/completions:chat_completions,/v1/messages:messages,/v1/responses:responses`.

- Синтаксис значения: пары `path:format` через запятую. Допустимые форматы:
  `chat_completions`, `messages`, `responses`. Путь не может содержать литеральный `:`.
- Матчинг (`models.PathResolver`): query-строка отбрасывается, затем точное совпадение,
  затем самый **длинный** сконфигурированный ключ, являющийся суффиксом пути. Ключи должны
  начинаться с `/`, что якорит суффиксный матч на границе сегмента: `/openai/v1/messages`
  совпадает с ключом `/v1/messages`, а `/xv1/messages` и `/v1/messages/foo` — нет.
- Сконфигурированные записи **подмешиваются поверх карты по умолчанию** (запись
  пользователя для того же пути побеждает), поэтому ключевые эндпоинты остаются под
  защитой даже при частичном `GUARDRAILS_PATHS`.
- Валидация на старте (`config.Load`): неизвестный формат, ключ без ведущего `/` или
  пустая карта ломают старт.

Тело запроса, чей формат не удалось разобрать, пересылается без маскирования (fail-open)
и увеличивает `extproc_guardrails_unsupported_body_schema_total`.
