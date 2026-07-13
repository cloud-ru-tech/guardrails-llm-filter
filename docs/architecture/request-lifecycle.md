# Жизненный цикл запроса

Один HTTP-запрос к data-plane (`:8080`) = один цикл mask→forward→demask в
`internal/controller/gateway`. Обработчик смонтирован на `POST /v1/chat/completions`,
`/v1/messages`, `/v1/responses`; путь резолвится в API-формат через `models.PathResolver`
(из `GUARDRAILS_PATHS`, суффиксный матчинг — проксирующие монтирования вроде
`/openai/v1/chat/completions` работают без настройки). Любой другой путь проксируется на
upstream без изменений.

## Фаза 1 — маскирование запроса (`request.go`)

1. Читается тело клиента (ограничено `GUARDRAILS_MAX_REQUEST_BYTES`, по умолчанию 32 MiB;
   превышение → 413).
2. Валидируется путь/формат; берётся/выводится `x-request-id` (ключ masking state).
3. `p.Settings = settings.Effective(глобальные настройки, значение override-заголовка)` —
   чистое in-memory-вычисление; настройки берутся из in-process `atomic.Pointer`-кэша, без
   сетевого I/O.
4. Извлекаются текстовые поля (`pkg/llmutils`), сканируются
   (`pkg/guardrails/regex/scanners/sensitive`), маскируются
   (`internal/usecases/guardrails/mask`), JSON патчится через sjson.
5. `MaskingState` пишется в хранилище best-effort (для межрепличного fallback; ошибки
   только логируются).
6. Строится исходящий `*http.Request` на `Upstream.BaseURL + path` (копируются
   метод/заголовки, вырезаются hop-by-hop-заголовки по RFC 7230 §6.1; per-path override
   через `GUARDRAILS_UPSTREAM_PATH_BASE_URLS`), и маскированное тело пересылается upstream.

**Detect (shadow) режим** (`mode: detect`): пайплайн сканирует, пишет метрики
срабатываний и аудит, но тело не изменяется и masking state не сохраняется — трафик идёт
без модификации.

## Фаза 2 — демаскирование ответа (`response.go`)

- **Non-SSE**: полный JSON буферизуется до конца потока, затем демаскируется через
  `internal/guardrails/demask` (Provider→Factory→Demasker), диспетчеризация по
  `Metadata.Format`. Плейсхолдеры вроде `<EMAIL_1>` должны сохраниться байт-в-байт (без
  HTML-экранирования при маршалинге).
- **SSE** (`Content-Type: text/event-stream` от upstream либо стриминговый запрос): кадры
  идут через `internal/sseproc` (варианты chatcompletions/messages/responses, выбор по
  `Metadata.Format` через `NewForFormat`) и сбрасываются по кадрам через `http.Flusher`.
  Per-field-демаскеры буферизуют суффикс длиной с максимально возможный плейсхолдер, чтобы
  плейсхолдеры, разбитые между кусками, всё равно демаскировались; UTF-8-безопасно.

Если in-process `MaskingState` пуст, фаза ответа откатывается к хранилищу (межрепличный
случай — см. [../storage/](../storage/)). Отключение клиента отменяет контекст запроса к
upstream.

## Заголовки ответа

- `x-guardrails-data-types-triggered: 5,2` — когда маскирование сработало.
- `x-guardrails-triggered-rules: pii.email,...` — только с
  `GUARDRAILS_HEADERS_EXPOSE_TRIGGERED_RULES=true` (ID правил раскрывают детекторы → opt-in).

## Тайминги upstream

`GUARDRAILS_UPSTREAM_TIMEOUT` (по умолчанию 120s) ограничивает ожидание заголовков ответа
upstream (время до первого байта), но намеренно **не** ограничивает время жизни
стримингового (SSE) тела — оно управляется контекстом запроса клиента, поэтому длинные
потоки не обрываются на полуслове.
