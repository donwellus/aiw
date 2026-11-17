SHELL := /bin/bash
BIN := bin/aiw
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS := -s -w -X github.com/donwellus/aiw/internal/cli.Version=$(VERSION)
LANGFUSE_DIR := infra/langfuse
COMPOSE := docker compose -f $(LANGFUSE_DIR)/compose.yaml --env-file $(LANGFUSE_DIR)/.env

.PHONY: build install test vet lint fmt check verify eval-smoke telemetry-init telemetry-up telemetry-down telemetry-reset telemetry-logs telemetry-env smoke clean

build:
	mkdir -p bin
	go build -trimpath -ldflags "$(LDFLAGS)" -o $(BIN) ./cmd/aiw

install:
	go install -ldflags "$(LDFLAGS)" ./cmd/aiw

test:
	go test ./...

vet:
	go vet ./...

lint:
	golangci-lint run ./...

fmt:
	gofmt -w .

check: vet test

telemetry-init:
	@$(LANGFUSE_DIR)/init-env.sh

telemetry-up: telemetry-init
	$(COMPOSE) up -d
	@echo "Langfuse: http://localhost:3000 (aguarde 1-2 min até o ready)"
	@echo "Depois carregue as chaves no shell: source $(LANGFUSE_DIR)/env.sh"

telemetry-down:
	$(COMPOSE) down

telemetry-reset:
	$(COMPOSE) down -v

telemetry-logs:
	$(COMPOSE) logs -f --tail=100

telemetry-env:
	@echo "source $(LANGFUSE_DIR)/env.sh"

smoke: build
	@./scripts/smoke.sh

eval-smoke: build
	$(BIN) --root $(CURDIR) eval --suite $(CURDIR)/evals/smoke/suite.yaml --engine fake

verify: eval-smoke
	@./scripts/verify-parallel.sh

clean:
	rm -rf bin
