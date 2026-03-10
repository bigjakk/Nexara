# Build stage
FROM golang:1.24-alpine AS builder

ARG VERSION=dev
ARG COMMIT=none
ARG BUILD_TIME=unknown

RUN apk add --no-cache git ca-certificates

WORKDIR /build

COPY go.mod go.sum ./
RUN go mod download

COPY . .

RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 \
    go build -ldflags="-s -w \
    -X github.com/bigjakk/nexara/internal/api.Version=${VERSION} \
    -X github.com/bigjakk/nexara/internal/api.Commit=${COMMIT} \
    -X github.com/bigjakk/nexara/internal/api.BuildTime=${BUILD_TIME}" \
    -o /build/nexara-ws ./cmd/ws

# Runtime stage
FROM gcr.io/distroless/static-debian12

COPY --from=builder /build/nexara-ws /nexara-ws

USER nonroot:nonroot

EXPOSE 8081

HEALTHCHECK --interval=10s --timeout=3s --start-period=5s --retries=3 \
    CMD ["/nexara-ws", "healthcheck"]

ENTRYPOINT ["/nexara-ws"]
