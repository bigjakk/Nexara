.PHONY: build test lint generate migrate-up migrate-down docker-build docker-up docker-down clean

# Go parameters
GOCMD=go
GOBUILD=$(GOCMD) build
GOTEST=$(GOCMD) test
GOVET=$(GOCMD) vet
BINARY_API=proxdash-api
BINARY_WS=proxdash-ws
BINARY_COLLECTOR=proxdash-collector
BINARY_SCHEDULER=proxdash-scheduler

# Version injection
VERSION?=$(shell git describe --tags --always --dirty 2>/dev/null || echo "dev")
COMMIT?=$(shell git rev-parse --short HEAD 2>/dev/null || echo "none")
BUILD_TIME?=$(shell date -u '+%Y-%m-%dT%H:%M:%SZ')
LDFLAGS=-s -w \
	-X github.com/proxdash/proxdash/internal/api.Version=$(VERSION) \
	-X github.com/proxdash/proxdash/internal/api.Commit=$(COMMIT) \
	-X github.com/proxdash/proxdash/internal/api.BuildTime=$(BUILD_TIME)

# Database
MIGRATIONS_DIR=migrations
DATABASE_URL?=postgres://proxdash:proxdash@localhost:5432/proxdash?sslmode=disable

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

## help: Show this help
help:
	@echo "Available targets:"
	@grep -E '^## ' $(MAKEFILE_LIST) | sed 's/## /  /'
