FROM golang:1.26-alpine AS builder
WORKDIR /src
COPY main.go .
RUN go mod init mock-llm && CGO_ENABLED=0 go build -o /out/mock-llm .

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=builder /out/mock-llm /mock-llm
EXPOSE 8000
ENTRYPOINT ["/mock-llm"]
