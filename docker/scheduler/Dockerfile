# Build stage
FROM golang:1.24-alpine AS builder

RUN apk add --no-cache git ca-certificates

WORKDIR /build

COPY go.mod go.sum ./
RUN go mod download

COPY . .

RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 \
    go build -ldflags="-s -w" -o /build/proxdash-scheduler ./cmd/scheduler

# Runtime stage
FROM gcr.io/distroless/static-debian12

COPY --from=builder /build/proxdash-scheduler /proxdash-scheduler

USER nonroot:nonroot

HEALTHCHECK --interval=10s --timeout=3s --start-period=5s --retries=3 \
    CMD ["/proxdash-scheduler", "healthcheck"]

ENTRYPOINT ["/proxdash-scheduler"]
