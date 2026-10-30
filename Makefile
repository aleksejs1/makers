VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
IMAGE   ?= ghcr.io/aleksejs1/makers
PORT    ?= 8080

.PHONY: help run dev test lint fmt tidy build docker docker-test docker-lint docker-run clean

help: ## Show this help
	@grep -E '^[a-z-]+:.*## ' $(MAKEFILE_LIST) | awk 'BEGIN {FS = ":.*## "}; {printf "  \033[36m%-12s\033[0m %s\n", $$1, $$2}'

run: ## Run locally in dev mode (login codes are printed to the log)
	MAKERS_ENV=dev MAKERS_PORT=$(PORT) MAKERS_DB_PATH=./data/makers.db go run ./cmd/makers

dev: ## Run with hot reload and Mailpit in Docker
	docker compose -f compose.dev.yaml up --build

test: ## Run tests
	go vet ./...
	go test -race ./...

lint: ## Run golangci-lint (installed locally)
	golangci-lint run ./...

fmt: ## Format code
	gofmt -w .

tidy: ## Tidy go.mod
	go mod tidy

build: ## Build a static binary into ./bin
	CGO_ENABLED=0 go build -trimpath -ldflags="-s -w -X main.version=$(VERSION)" -o bin/makers ./cmd/makers

docker: ## Build the production image
	docker build --build-arg VERSION=$(VERSION) -t $(IMAGE):$(VERSION) -t $(IMAGE):latest .

docker-test: ## Run tests in Docker
	docker build --target test .

docker-lint: ## Run the linter in Docker
	docker build --target lint .

docker-run: docker ## Run the production image locally
	docker run --rm -p $(PORT):$(PORT) -e MAKERS_PORT=$(PORT) -e MAKERS_ENV=dev -v makers-data:/data $(IMAGE):latest

clean: ## Remove build artifacts
	rm -rf bin tmp
