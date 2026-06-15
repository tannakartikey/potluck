# Potluck — build helpers. The runner is a single static, stdlib-only Go binary.
# v0 installs from source — no release pipeline, no version tags (see open-questions #18).
# The binary stamps a short commit hash for support only.
VERSION ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo dev)
LDFLAGS := -s -w -X main.version=$(VERSION)
PKG     := ./cmd/potluck

.PHONY: build test clean help anon-gate

help: ## list targets
	@grep -E '^[a-zA-Z_-]+:.*?## .*$$' $(MAKEFILE_LIST) | awk 'BEGIN{FS=":.*?## "}{printf "  %-8s %s\n",$$1,$$2}'

build: ## build the runner -> bin/potluck
	cd client && go build -ldflags "$(LDFLAGS)" -o ../bin/potluck $(PKG)
	@echo "built bin/potluck ($(VERSION))"

test: ## vet + unit tests
	cd client && go vet ./... && go test ./...

anon-gate: ## run the mandatory pre-launch anon-role security gate (hits the live API)
	bash scripts/anon-gate.sh

clean:
	rm -rf bin
