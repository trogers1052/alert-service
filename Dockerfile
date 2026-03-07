# Build stage
FROM golang:1.23-alpine AS builder

WORKDIR /app

COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -o /alert-service ./cmd/alerts
RUN CGO_ENABLED=0 GOOS=linux go build -o /healthcheck ./cmd/healthcheck

# Run stage
FROM gcr.io/distroless/static-debian12

COPY --from=builder /alert-service /alert-service
COPY --from=builder /healthcheck /healthcheck

USER nonroot:nonroot

ENTRYPOINT ["/alert-service"]
