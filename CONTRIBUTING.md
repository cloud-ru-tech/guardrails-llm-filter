# Участие в разработке

Спасибо за интерес к проекту!

## Окружение разработки

- Go (версия из `go.mod`)
- Docker (для тестов postgres-хранилища и демо quickstart)
- [golangci-lint](https://golangci-lint.run/)

```sh
make build       # собрать бинарь в ./bin
make test        # полный набор тестов (нужен Docker для postgres-хранилища)
make test-short  # пропускает тесты, зависящие от Docker
make lint
make demo-up     # сквозное демо: guardrails-llm-filter + mock LLM
```

## Pull request'ы

- Держите изменения сфокусированными; одно логическое изменение на PR.
- Добавляйте тесты на новое поведение. Бэкенды хранилища обязаны проходить общий
  conformance-набор в `internal/repository/repositorytest`.
- Перед отправкой запускайте `make test lint`.
- Новые правила детекции идут в `configs/guardrails_regex_rules.yaml` с тест-кейсом в
  `tests/rules`.

## Сообщения о проблемах

Используйте GitHub issues для багов и предложений. По вопросам безопасности см.
[SECURITY.md](SECURITY.md).
