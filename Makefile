# Shellforge build tooling.
#
# On Windows, `make` is usually not installed. Use .\make.ps1 <target>, which runs
# the same commands. Keep the two files in sync.

BINARY      := shellforge
PKG         := ./cmd/shellforge
BIN_DIR     := bin
IMAGE_NAME  := shellforge-sandbox
IMAGE_TAG   := dev
LEVEL       ?= nav-01

VERSION     := $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
COMMIT      := $(shell git rev-parse --short HEAD 2>/dev/null || echo unknown)
DATE        := $(shell date -u +%Y-%m-%dT%H:%M:%SZ)
LDFLAGS     := -s -w \
	-X main.version=$(VERSION) \
	-X main.commit=$(COMMIT) \
	-X main.buildDate=$(DATE)

# Detect the container engine. Docker first, Podman as a local convenience.
CONTAINER_ENGINE := $(shell command -v docker 2>/dev/null || command -v podman 2>/dev/null || echo docker)

.DEFAULT_GOAL := help
.PHONY: help build install test race fuzz cover lint fmt vet punct allowlist links arch \
        cli labels sec vuln gosec image rootfs run golden validate clean tools ci

## help: Show this help.
help:
	@echo "Shellforge make targets:"
	@echo
	@grep -E '^## ' $(MAKEFILE_LIST) | sed 's/## /  /' | awk -F': ' '{printf "  %-12s %s\n", $$1, $$2}'
	@echo
	@echo "On Windows use: .\\make.ps1 <target>"

## build: Compile the binary into bin/.
build:
	@mkdir -p $(BIN_DIR)
	go build -trimpath -ldflags "$(LDFLAGS)" -o $(BIN_DIR)/$(BINARY) $(PKG)
	@echo "built $(BIN_DIR)/$(BINARY) $(VERSION)"

## install: Install the binary into GOBIN.
install:
	go install -trimpath -ldflags "$(LDFLAGS)" $(PKG)

## test: Run unit tests.
test:
	go test ./...

## race: Run tests under the race detector.
race:
	go test -race ./...

## fuzz: Fuzz the OSC parser for 60s.
fuzz:
	go test -run '^FuzzParser$$' -fuzz '^FuzzParser$$' -fuzztime 60s ./internal/pty

## cover: Run tests and open a coverage report.
cover:
	go test -coverprofile=coverage.out ./...
	go tool cover -func=coverage.out | tail -1

## fmt: Format all Go source in place.
fmt:
	gofmt -s -w .

## vet: Run go vet.
vet:
	go vet ./...

## punct: Fail on em dashes, en dashes, and smart quotes. See Rule 0 in CLAUDE.md.
punct:
	@bash scripts/check-punctuation.sh

## allowlist: Fail if the argv identifier allowlist regexp drifts or a loose copy returns.
allowlist:
	@bash scripts/check-allowlist-regexp.sh

## links: Fail on broken relative links in Markdown.
links:
	@bash scripts/check-links.sh

## arch: Enforce the layer dependency rule.
arch:
	go test ./internal/archtest/...

## cli: Assert the CLI entry point package is present, tracked, and buildable.
cli:
	@bash scripts/check-cli-package.sh

## gates: Assert no CI job can fail without blocking a merge.
gates:
	@python3 scripts/check-ci-gates.py 2>/dev/null || python scripts/check-ci-gates.py

## labels: Sync GitHub issue labels from .github/labels.yml.
labels:
	@bash scripts/sync-labels.sh

## lint: gofmt check, go vet, punctuation gate, allowlist gate, link check, layer test.
lint: vet punct allowlist links arch cli
	@unformatted="$$(gofmt -s -l . )"; \
	if [ -n "$$unformatted" ]; then \
		echo "FAIL: these files are not gofmt -s clean:"; \
		echo "$$unformatted"; \
		exit 1; \
	fi
	@echo "OK: lint clean"

## vuln: Scan dependencies and the toolchain for known vulnerabilities.
vuln:
	go run golang.org/x/vuln/cmd/govulncheck@latest ./...

## gosec: Static security analysis.
gosec:
	go run github.com/securego/gosec/v2/cmd/gosec@latest -quiet ./...

## sec: All security checks.
sec: vuln gosec

## image: Build the sandbox container image.
image:
	$(CONTAINER_ENGINE) build -f images/Containerfile -t $(IMAGE_NAME):$(IMAGE_TAG) images/

## rootfs: Export the WSL rootfs tarball from the container image.
rootfs: image
	@mkdir -p images/out
	$(CONTAINER_ENGINE) create --name $(IMAGE_NAME)-export $(IMAGE_NAME):$(IMAGE_TAG) /bin/true
	$(CONTAINER_ENGINE) export $(IMAGE_NAME)-export | gzip -9 > images/out/rootfs.tar.gz
	$(CONTAINER_ENGINE) rm -f $(IMAGE_NAME)-export
	@cd images/out && sha256sum rootfs.tar.gz > rootfs.tar.gz.sha256
	@echo "exported images/out/rootfs.tar.gz"
	@cat images/out/rootfs.tar.gz.sha256

## run: Play one level. Override with LEVEL=<id>.
run: build
	./$(BIN_DIR)/$(BINARY) run $(LEVEL)

## validate: Validate the content pack.
validate: build
	./$(BIN_DIR)/$(BINARY) author validate packs/core-linux-basics

## golden: Run the golden test for every level.
golden: build
	./$(BIN_DIR)/$(BINARY) author test --all

## tools: Report the toolchain versions this repo expects.
tools:
	@go version
	@echo "container engine: $(CONTAINER_ENGINE)"
	@$(CONTAINER_ENGINE) --version 2>/dev/null || echo "  not available"

## clean: Remove build output.
clean:
	rm -rf $(BIN_DIR) images/out coverage.out coverage.html
	go clean -testcache

## ci: Everything CI runs.
ci: lint test race sec
	@echo "OK: ci clean"
