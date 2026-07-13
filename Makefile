NAME  := guardrails-llm-filter
GOBIN := $(shell pwd)/bin
IMAGE := $(NAME):local

# Build identity injected into internal/version via -ldflags (see /v1/version).
VERSION_PKG := github.com/cloud-ru-tech/guardrails-llm-filter/internal/version
VERSION     ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
COMMIT      ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo none)
DATE        ?= $(shell date -u +%Y-%m-%dT%H:%M:%SZ)
LDFLAGS     := -s -w \
	-X $(VERSION_PKG).Version=$(VERSION) \
	-X $(VERSION_PKG).Commit=$(COMMIT) \
	-X $(VERSION_PKG).Date=$(DATE)

.PHONY: build
build:
	go build -trimpath -ldflags="$(LDFLAGS)" -o $(GOBIN)/$(NAME) ./cmd/$(NAME)

# Build the management console SPA into frontend/dist, where //go:embed picks it
# up so `make build` embeds it in the binary. Run this before `build` for a
# console-enabled local binary; `docker-build` builds the UI internally.
.PHONY: frontend
frontend:
	cd frontend && npm ci && npm run build

.PHONY: run
run:
	go run ./cmd/$(NAME)

.PHONY: test
test:
	go test -race ./...

# Skips tests that need Docker (postgres store conformance).
.PHONY: test-short
test-short:
	go test -race -short ./...

.PHONY: lint
lint:
	golangci-lint run

.PHONY: generate
generate:
	go generate ./...

# Regenerate the protobuf contract (gRPC server, grpc-gateway REST, OpenAPI)
# for the management API from api/proto into pkg/. Requires the `easyp` binary
# (go install github.com/easyp-tech/easyp/cmd/easyp@latest) plus protoc-gen-go,
# protoc-gen-go-grpc, protoc-gen-grpc-gateway and protoc-gen-openapiv2 on PATH.
.PHONY: gen-proto
gen-proto:
	easyp mod download
	easyp generate

# Regenerate the gitleaks-derived rule file from configs/gitleaks.toml.
.PHONY: rules-gen
rules-gen:
	go run ./cmd/rulesgen import-gitleaks \
		--in configs/gitleaks.toml \
		--out configs/guardrails_regex_rules.gitleaks.generated.yaml

.PHONY: docker-build
docker-build:
	docker build -t $(IMAGE) .

.PHONY: demo-up
demo-up:
	docker compose -f examples/quickstart/docker-compose.yml up --build

.PHONY: demo-down
demo-down:
	docker compose -f examples/quickstart/docker-compose.yml down -v
