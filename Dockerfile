# Build stage
FROM golang:1.21-alpine AS builder

WORKDIR /app

COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -o /alert-service ./cmd/alerts

# Run stage
FROM gcr.io/distroless/static-debian12

COPY --from=builder /alert-service /alert-service

USER nonroot:nonroot

ENTRYPOINT ["/alert-service"]
