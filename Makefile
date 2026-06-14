# Potluck — build & release. The runner is a single static, stdlib-only Go binary.
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS := -s -w -X main.version=$(VERSION)
PKG     := ./cmd/potluck
DIST    := dist
# os/arch pairs to ship. Static (CGO_ENABLED=0) so the binaries run anywhere.
PLATFORMS := darwin/amd64 darwin/arm64 linux/amd64 linux/arm64 windows/amd64

.PHONY: build build-all checksums test clean help

help: ## list targets
	@grep -E '^[a-zA-Z_-]+:.*?## .*$$' $(MAKEFILE_LIST) | awk 'BEGIN{FS=":.*?## "}{printf "  %-12s %s\n",$$1,$$2}'

build: ## build for the host machine -> bin/potluck
	cd client && go build -ldflags "$(LDFLAGS)" -o ../bin/potluck $(PKG)
	@echo "built bin/potluck ($(VERSION))"

build-all: ## cross-compile release binaries -> dist/
	@rm -rf $(DIST) && mkdir -p $(DIST)
	@for p in $(PLATFORMS); do \
	  os=$${p%/*}; arch=$${p#*/}; ext=; [ "$$os" = windows ] && ext=.exe; \
	  out=$(DIST)/potluck-$$os-$$arch$$ext; \
	  echo "  -> $$out"; \
	  (cd client && GOOS=$$os GOARCH=$$arch CGO_ENABLED=0 go build -ldflags "$(LDFLAGS)" -o ../$$out $(PKG)) || exit 1; \
	done
	@echo "built $(VERSION)"

checksums: ## write dist/SHA256SUMS over the release binaries
	@cd $(DIST) && ( command -v sha256sum >/dev/null && sha256sum potluck-* || shasum -a 256 potluck-* ) > SHA256SUMS
	@cat $(DIST)/SHA256SUMS

test: ## vet + unit tests
	cd client && go vet ./... && go test ./...

clean:
	rm -rf bin $(DIST)
