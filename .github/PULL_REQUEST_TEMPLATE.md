<!-- Thanks for contributing! Please keep PRs focused and describe the change. -->

## What & why

<!-- What does this change do, and what problem does it solve? -->

## How

<!-- Notable implementation details, trade-offs, alternatives considered. -->

## Checklist

- [ ] `make test` passes (`go test -race ./...`; postgres tests need Docker)
- [ ] `make lint` passes (`golangci-lint run`)
- [ ] Preserves the core invariants: **mask/pass only** (never block traffic)
      and **fail-open** on the data path (internal errors pass traffic through)
- [ ] No secrets, real tokens, or personal data added to code, tests, or docs
- [ ] Docs updated if behavior/config/API changed (README, `docs/`,
      `api/openapi.yaml`)
- [ ] Generated files regenerated if their source changed
      (`make rules-gen`, `go generate ./internal/models/`)
