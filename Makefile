.PHONY: up down test test-integration lint proto build smoke tools

GOLANGCI_LINT_VERSION := v2.15.0

up:
	docker compose up --build -d --wait

down:
	docker compose down -v

build:
	go build ./...

test:
	go vet ./...
	go test -race ./...

test-integration:
	go test -tags=integration -count=1 -timeout=10m ./...

lint:
	golangci-lint run ./...
	golangci-lint run --build-tags=integration ./test/...

proto:
	buf lint
	buf generate

smoke:
	./scripts/smoke.sh

tools:
	go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@$(GOLANGCI_LINT_VERSION)
