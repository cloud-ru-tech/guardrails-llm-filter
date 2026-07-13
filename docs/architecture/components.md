# Карта пакетов

| Пакет | Зона ответственности |
|---|---|
| `cmd/guardrails-llm-filter` | main: `signal.NotifyContext`, загрузка конфига, `logging.Setup`, `app.Start/Stop` |
| `cmd/rulesgen` | офлайн-CLI: `import-gitleaks` конвертирует gitleaks TOML → YAML правил |
| `internal/app` | DI-контейнер из ленивых геттеров (`app.go`) + разводка серверов (`servers.go`); владеет остановкой |
| `internal/config` | конфигурация только из env, префикс `GUARDRAILS_` (`env.ParseWithOptions`) |
| `internal/logging` | настройка slog + тонкие хелперы (`Error(ctx,msg,err,kv...)` — err позиционный, nil-safe) |
| `internal/controller/gateway` | **data-path**: `http.Handler` (`gateway.go`), маскирование запроса (`request.go`), форвард в upstream и демаскирование ответа (`response.go`), стриминговая запись (`flushwriter.go`) |
| `internal/controller/api` | management API: gRPC-реализация `GuardrailsApi` за REST-прокси grpc-gateway; DTO отвязаны от типов движка через `converter.go` |
| `internal/models` | `Metadata`, `MaskingState`/`Replacement`, `GuardrailsSettings`/`EffectiveSettings`, enum `DataType` (сгенерирован go-enum) |
| `internal/service/settings` | сервис глобальных настроек (atomic-кэш над SettingsStore) + резолюция override через `Effective()` |
| `internal/usecases/rules` | сценарии управления правилами, по пакету на сценарий (create/update/delete/setenabled/get/list) за координатором: валидация → запись → триггер перезагрузки реестра |
| `internal/service/rulesreload` | merge-and-swap реестра: builtin ∪ custom ∖ disabled → `Build` → атомарная замена; загрузка на старте + тикер обновления |
| `internal/usecases/guardrails/mask` | сценарий маскирования: типы данных → ID правил → скан → плейсхолдеры → `MaskingState` |
| `internal/guardrails/demask` | цепочка демаскирования Provider (приложение) → Factory (запрос) → Demasker (безопасен в потоке) |
| `internal/service/audit` | аудит `Recorder`: одна `AuditRecord` на маскированный запрос, асинхронно, неблокирующе, fail-open |
| `internal/sseproc` | процессоры SSE-потока (chatcompletions / messages / responses, выбор через `NewForFormat`) с буферизацией по границам UTF-8/плейсхолдеров |
| `internal/repository` | интерфейсы хранилища + `ErrNotFound`; реализации в `memory/`, `redis/`, `postgres/`; `factory/` строит по конфигу; `repositorytest/` — набор conformance-тестов |
| `internal/metrics` | Prometheus, namespace `extproc_guardrails` |
| `frontend` | встроенная веб-консоль (React-SPA), вшитая через `//go:embed all:dist` (`embed.go`); исходники и dev-инструкции — в самом каталоге |
| `pkg/tlsutils` | self-signed сертификат для опционального режима `GRPC_SECURE=true` (management gRPC) |
| `pkg/guardrails/regex` | **переиспользуемый движок** (публичная библиотека): `rule` (схема + YAML-загрузчик), `registry` (скомпилированные правила + `Reloadable`), `scanners/{sensitive,placeholder}`, `validation`, `placeholderfmt` |
| `pkg/llmutils` | извлечение/патч контентных полей OpenAI и Anthropic, формы запроса и ответа (публичная библиотека) |
| `pkg/gitleaksgen` | конвертер gitleaks TOML → YAML правил, лежит в основе `cmd/rulesgen` |
