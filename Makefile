.PHONY: build frontend-build test test-quick lint generate migrate-up migrate-down docker-build docker-up docker-down clean audit audit-go audit-npm coverage-html

# Go parameters
GOCMD=go
GOBUILD=$(GOCMD) build
GOTEST=$(GOCMD) test
GOVET=$(GOCMD) vet
BINARY=nexara

# Version injection
VERSION?=$(shell git describe --tags --always --dirty 2>/dev/null || echo "dev")
COMMIT?=$(shell git rev-parse --short HEAD 2>/dev/null || echo "none")
BUILD_TIME?=$(shell date -u '+%Y-%m-%dT%H:%M:%SZ')
LDFLAGS=-s -w \
	-X github.com/bigjakk/nexara/internal/api.Version=$(VERSION) \
	-X github.com/bigjakk/nexara/internal/api.Commit=$(COMMIT) \
	-X github.com/bigjakk/nexara/internal/api.BuildTime=$(BUILD_TIME)

# Database
MIGRATIONS_DIR=migrations
DATABASE_URL?=postgres://nexara:nexara@localhost:5432/nexara?sslmode=disable

## build: Build unified Go binary (copies frontend dist if available)
build:
	@if [ -d frontend/dist ]; then \
		rm -rf cmd/nexara/dist && \
		cp -r frontend/dist cmd/nexara/dist; \
	fi
	$(GOBUILD) -ldflags="$(LDFLAGS)" -o bin/$(BINARY) ./cmd/nexara

## frontend-build: Build frontend and copy to embed directory
frontend-build:
	cd frontend && npm ci && npm run build
	rm -rf cmd/nexara/dist
	cp -r frontend/dist cmd/nexara/dist

## test: Run all tests (race detector + coverage) — the gate before committing
# -timeout 30m, not Go's 10m default. internal/api, the slowest package, runs
# ~3 min under bare -race on a 12-core box; -coverprofile adds 20-40% and a
# 4-vCPU CI runner 1.5-2x on top, and a timeout reads as a hung test rather
# than a slow one.
test:
	$(GOTEST) -race -timeout 30m -coverprofile=coverage.out ./...

## test-quick: Dev-loop run (~1.5 min cold, cached packages skipped) — no race detector, no coverage, -short
test-quick:
	$(GOTEST) -short ./...

## lint: Run golangci-lint
lint:
	golangci-lint run ./...

## generate: Run sqlc and go generate
generate:
	sqlc generate
	$(GOCMD) generate ./...

## migrate-up: Apply all pending migrations
migrate-up:
	migrate -database "$(DATABASE_URL)" -path $(MIGRATIONS_DIR) up

## migrate-down: Rollback the last migration
migrate-down:
	migrate -database "$(DATABASE_URL)" -path $(MIGRATIONS_DIR) down 1

## docker-build: Build Docker image
docker-build:
	docker compose build

## docker-up: Start all services
docker-up:
	docker compose up -d

## docker-down: Stop all services
docker-down:
	docker compose down

## clean: Remove build artifacts
clean:
	rm -rf bin/ coverage.out

## audit-go: Run Go vulnerability check
audit-go:
	govulncheck ./...

## audit-npm: Run npm audit on frontend
audit-npm:
	cd frontend && npm audit

## audit: Run all security audits
audit: audit-go audit-npm

## coverage-html: Generate HTML coverage report
# -timeout 30m for the same reason as `test:` above, and more so: this is the
# same -race sweep plus coverage instrumentation, so it is the slowest path in
# the Makefile.
coverage-html:
	$(GOTEST) -race -timeout 30m -coverprofile=coverage.out ./...
	$(GOCMD) tool cover -html=coverage.out -o coverage.html
	@echo "Coverage report: coverage.html"

## help: Show this help
help:
	@echo "Available targets:"
	@grep -E '^## ' $(MAKEFILE_LIST) | sed 's/## /  /'
