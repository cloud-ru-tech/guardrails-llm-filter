###############################################################
# UI BUILD (management console SPA)
###############################################################
FROM node:24-alpine AS ui

WORKDIR /ui

# Install deps against the lockfile first for layer caching.
COPY frontend/package.json frontend/package-lock.json ./
RUN npm ci

# Build the SPA -> /ui/dist. `prebuild` regenerates the API types from
# spec/openapi.yaml, so the whole frontend/ tree is needed here.
COPY frontend/ ./
RUN npm run build

###############################################################
# BUILDER
###############################################################
FROM golang:1.26-alpine AS builder

ENV CGO_ENABLED=0

# Build identity, surfaced by GET /v1/version. Pass via --build-arg.
ARG VERSION=dev
ARG COMMIT=none
ARG DATE=unknown

WORKDIR /src

COPY go.mod go.sum ./
RUN go mod download

COPY . .

# Drop the built console into the path //go:embed expects (frontend/dist is
# excluded from the build context via .dockerignore) so it is embedded in the
# binary. This overwrites the committed dist placeholder.
COPY --from=ui /ui/dist ./frontend/dist

RUN go build -trimpath -ldflags="-s -w \
	-X github.com/cloud-ru-tech/guardrails-llm-filter/internal/version.Version=${VERSION} \
	-X github.com/cloud-ru-tech/guardrails-llm-filter/internal/version.Commit=${COMMIT} \
	-X github.com/cloud-ru-tech/guardrails-llm-filter/internal/version.Date=${DATE}" \
	-o /out/guardrails-llm-filter ./cmd/guardrails-llm-filter

###############################################################
# RUNTIME
###############################################################
FROM gcr.io/distroless/static-debian13:nonroot

WORKDIR /app

COPY --from=builder /out/guardrails-llm-filter /app/guardrails-llm-filter
COPY --from=builder /src/configs /app/configs

# data-plane HTTP (clients), metrics HTTP, configuration API HTTP
EXPOSE 8080 9090 9080

ENTRYPOINT ["/app/guardrails-llm-filter"]
