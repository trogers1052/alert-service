# Build stage
FROM golang:1.25-alpine AS builder

RUN apk add --no-cache git

WORKDIR /app

COPY go.mod go.sum ./
RUN sed -i '/replace.*trading-testkit/d' go.mod && \
    GOPRIVATE=github.com/trogers1052/* go get github.com/trogers1052/trading-testkit@v0.3.0 && \
    go mod download

COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -o /alert-service ./cmd/alerts
RUN CGO_ENABLED=0 GOOS=linux go build -o /healthcheck ./cmd/healthcheck

# Run stage
FROM gcr.io/distroless/static-debian12

COPY --from=builder /alert-service /alert-service
COPY --from=builder /healthcheck /healthcheck

USER nonroot:nonroot

HEALTHCHECK --interval=30s --timeout=5s --retries=3 --start-period=15s \
    CMD ["/healthcheck"]

ENTRYPOINT ["/alert-service"]
