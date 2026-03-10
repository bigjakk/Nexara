.PHONY: build test lint generate migrate-up migrate-down docker-build docker-up docker-down clean audit audit-go audit-npm coverage-html

# Go parameters
GOCMD=go
GOBUILD=$(GOCMD) build
GOTEST=$(GOCMD) test
GOVET=$(GOCMD) vet
BINARY_API=nexara-api
BINARY_WS=nexara-ws
BINARY_COLLECTOR=nexara-collector
BINARY_SCHEDULER=nexara-scheduler

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

## build: Build all Go binaries
build:
	$(GOBUILD) -ldflags="$(LDFLAGS)" -o bin/$(BINARY_API) ./cmd/api
	$(GOBUILD) -o bin/$(BINARY_WS) ./cmd/ws
	$(GOBUILD) -o bin/$(BINARY_COLLECTOR) ./cmd/collector
	$(GOBUILD) -o bin/$(BINARY_SCHEDULER) ./cmd/scheduler

## test: Run all tests
test:
	$(GOTEST) -race -coverprofile=coverage.out ./...

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

## docker-build: Build all Docker images
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
coverage-html:
	$(GOTEST) -race -coverprofile=coverage.out ./...
	$(GOCMD) tool cover -html=coverage.out -o coverage.html
	@echo "Coverage report: coverage.html"

## help: Show this help
help:
	@echo "Available targets:"
	@grep -E '^## ' $(MAKEFILE_LIST) | sed 's/## /  /'
