# Build stage
FROM golang:1.22-alpine AS builder

RUN apk add --no-cache git ca-certificates

WORKDIR /build

COPY go.mod go.sum ./
RUN go mod download

COPY . .

RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 \
    go build -ldflags="-s -w" -o /build/proxdash-ws ./cmd/ws

# Runtime stage
FROM gcr.io/distroless/static-debian12

COPY --from=builder /build/proxdash-ws /proxdash-ws

USER nonroot:nonroot

EXPOSE 8081

HEALTHCHECK --interval=10s --timeout=3s --start-period=5s --retries=3 \
    CMD ["/proxdash-ws", "healthcheck"]

ENTRYPOINT ["/proxdash-ws"]
