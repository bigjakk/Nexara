# Build stage
FROM golang:1.24-alpine AS builder

RUN apk add --no-cache git ca-certificates

WORKDIR /build

COPY go.mod go.sum ./
RUN go mod download

COPY . .

RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 \
    go build -ldflags="-s -w" -o /build/proxdash-collector ./cmd/collector

# Runtime stage
FROM gcr.io/distroless/static-debian12

COPY --from=builder /build/proxdash-collector /proxdash-collector

USER nonroot:nonroot

HEALTHCHECK --interval=10s --timeout=3s --start-period=5s --retries=3 \
    CMD ["/proxdash-collector", "healthcheck"]

ENTRYPOINT ["/proxdash-collector"]
