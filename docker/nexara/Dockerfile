# Stage 1: Build frontend
FROM node:22-alpine AS frontend-builder

WORKDIR /build

COPY frontend/package.json frontend/package-lock.json* ./
RUN npm ci

COPY frontend/ .
RUN npm run build

# Stage 2: Build Go binary
FROM golang:1.24-alpine AS go-builder

ARG VERSION=dev
ARG COMMIT=none
ARG BUILD_TIME=unknown

RUN apk add --no-cache git ca-certificates

WORKDIR /build

COPY go.mod go.sum ./
RUN go mod download

COPY . .

# Copy built frontend into the embed directory
COPY --from=frontend-builder /build/dist cmd/nexara/dist

RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 \
    go build -ldflags="-s -w \
    -X github.com/bigjakk/nexara/internal/api.Version=${VERSION} \
    -X github.com/bigjakk/nexara/internal/api.Commit=${COMMIT} \
    -X github.com/bigjakk/nexara/internal/api.BuildTime=${BUILD_TIME}" \
    -o /build/nexara ./cmd/nexara

# Stage 3: Runtime
FROM gcr.io/distroless/static-debian12

COPY --from=go-builder /build/nexara /nexara

USER nonroot:nonroot

EXPOSE 8080

HEALTHCHECK --interval=10s --timeout=3s --start-period=15s --retries=5 \
    CMD ["/nexara", "healthcheck"]

ENTRYPOINT ["/nexara"]
