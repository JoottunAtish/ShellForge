# Shellforge build tooling.
#
# On Windows, `make` is usually not installed. Use .\make.ps1 <target>, which runs
# the same commands. Keep the two files in sync.

BINARY      := shellforge
PKG         := ./cmd/shellforge
BIN_DIR     := bin
IMAGE_NAME  := shellforge-sandbox
IMAGE_TAG   := dev
# The image `author test` provisions. It is deliberately not IMAGE_NAME: the
# golden harness tears down every level it touches, and doing that to the
# container a learner has a level open in is not acceptable. It must be built
# from the same Containerfile, which is what golden-image below is for.
GOLDEN_IMAGE := shellforge-authortest
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

# The architecture cmd/sf-ptyhost is built for. It runs inside the sandbox,
# so this follows the image, not the host. v0.1's published rootfs is amd64.
PTYHOST_ARCH ?= amd64

.DEFAULT_GOAL := help
.PHONY: help build install test race fuzz cover lint fmt vet punct allowlist links arch \
        cli labels sec vuln gosec ptyhost image rootfs run golden golden-image golden-go validate \
        demo dist clean tools ci

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

# DIST_DIR is a literal, never a `?=` override: an overridable `rm -rf`
# target is exactly the shape the destructive-safety skill forbids. It is
# never derived from git and never empty, because a make variable assigned
# a literal cannot be.
DIST_DIR     := dist
DIST_TARGETS := linux/amd64 linux/arm64 windows/amd64

## dist: Cross-compile the release archives into dist/, the same matrix release.yml builds.
#
# Deliberately does NOT export the WSL rootfs: that needs a Docker daemon,
# `make rootfs` already exists for it, and coupling the two would make
# `make dist` unusable on a machine with no daemon, which is this one.
dist:
	@rm -rf $(DIST_DIR)
	@mkdir -p $(DIST_DIR)
	@for t in $(DIST_TARGETS); do \
	  os=$${t%%/*}; arch=$${t##*/}; \
	  bin=$(BINARY); if [ "$$os" = "windows" ]; then bin=$(BINARY).exe; fi; \
	  CGO_ENABLED=0 GOOS=$$os GOARCH=$$arch go build -trimpath -ldflags "$(LDFLAGS)" \
	    -o $(DIST_DIR)/$$bin $(PKG) || exit 1; \
	  if [ "$$os" = "windows" ]; then \
	    (cd $(DIST_DIR) && zip -q -j shellforge_$(VERSION)_$${os}_$${arch}.zip $$bin); \
	  else \
	    (cd $(DIST_DIR) && tar -czf shellforge_$(VERSION)_$${os}_$${arch}.tar.gz $$bin); \
	  fi; \
	  rm -f $(DIST_DIR)/$$bin; \
	done
	@cd $(DIST_DIR) && sha256sum shellforge_*.tar.gz shellforge_*.zip > SHA256SUMS
	@cd $(DIST_DIR) && sha256sum -c SHA256SUMS
	@ls -l $(DIST_DIR)

## test: Run unit tests.
# Depends on ptyhost because `go test ./...` provisions a real sandbox image
# wherever a docker daemon exists, and images/Containerfile copies the binary
# that target builds. Without it the docker contract suite fails with a docker
# build error about a missing file, which reads as a broken Containerfile.
test: ptyhost
	go test ./...

## race: Run tests under the race detector.
race: ptyhost
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
#
# It tags :latest as well as :dev, because those are two different consumers
# and only one of them is this file. `shellforge run` provisions the untagged
# $(IMAGE_NAME), which docker resolves to :latest, so an image built only as
# :dev left the game running whatever :latest happened to hold. On this
# machine that was three weeks old and predated the logistics group perm-02
# needs, which is the same trap golden-image exists to close for `author
# test`.
## ptyhost: Build the in-sandbox pseudo terminal host into the image context.
#
# cmd/sf-ptyhost runs INSIDE the sandbox, so it is built for linux and for
# the image's architecture, never for whatever the developer is sitting at.
# v0.1's image is amd64 (docs/design/DAY-3-TICKETS.md records the
# multi-architecture rootfs as a deliberate cut), and PTYHOST_ARCH is here so
# that decision has one place to change rather than several.
#
# It lands in images/out/, which .gitignore already covers, rather than in
# the tracked images/bin/ that the Containerfile's `COPY bin/` would sweep
# up: CLAUDE.md forbids committing a binary, and a build artifact sitting in
# a tracked directory is one stray `git add -A` away from being committed.
ptyhost:
	@mkdir -p images/out/bin
	CGO_ENABLED=0 GOOS=linux GOARCH=$(PTYHOST_ARCH) go build -trimpath -ldflags "-s -w" \
	    -o images/out/bin/sf-ptyhost ./cmd/sf-ptyhost
	@echo "built images/out/bin/sf-ptyhost (linux/$(PTYHOST_ARCH))"

image: ptyhost
	$(CONTAINER_ENGINE) build -f images/Containerfile -t $(IMAGE_NAME):$(IMAGE_TAG) images/
	$(CONTAINER_ENGINE) tag $(IMAGE_NAME):$(IMAGE_TAG) $(IMAGE_NAME):latest

## rootfs: Export the WSL rootfs tarball from the container image.
rootfs: image
	@mkdir -p images/out
	$(CONTAINER_ENGINE) create --name $(IMAGE_NAME)-export $(IMAGE_NAME):$(IMAGE_TAG) /bin/true
	@rm -f images/out/rootfs.tar images/out/rootfs.tar.sha256
	# Export to a file first, then gzip the file in place: a Make recipe has
	# no pipefail by default, so `docker export | gzip > file` would let a
	# failed export still report success, since gzip happily compresses
	# whatever partial bytes it received before EOF and exits 0.
	$(CONTAINER_ENGINE) export $(IMAGE_NAME)-export -o images/out/rootfs.tar
	$(CONTAINER_ENGINE) rm -f $(IMAGE_NAME)-export
	gzip -9 -n -f images/out/rootfs.tar
	@cd images/out && sha256sum rootfs.tar.gz > rootfs.tar.gz.sha256
	@echo "exported images/out/rootfs.tar.gz"
	@cat images/out/rootfs.tar.gz.sha256

## run: Play one level. Override with LEVEL=<id>.
run: build
	./$(BIN_DIR)/$(BINARY) run $(LEVEL)

## validate: Validate the content pack.
validate: build
	./$(BIN_DIR)/$(BINARY) author validate packs/core-linux-basics

## golden-image: Tag the sandbox image as the one `author test` provisions.
golden-image: image
	$(CONTAINER_ENGINE) tag $(IMAGE_NAME):$(IMAGE_TAG) $(GOLDEN_IMAGE):latest

## golden: Run the golden test for every level, through the CLI.
#
# It depends on golden-image because nothing else keeps that tag current.
# `shellforge author test` builds $(GOLDEN_IMAGE) from the Containerfile when
# the tag is missing, but not when it merely holds an old build, so a local
# `make golden` used to test whatever image happened to be sitting under that
# name. On the Day 5 content run that was a three week old one, and it
# reported three failures that did not exist.
golden: build golden-image
	./$(BIN_DIR)/$(BINARY) author test --all

## golden-go: The same contract as a Go test. Needs a Linux Docker daemon.
golden-go: golden-image
	SHELLFORGE_GOLDEN=1 go test -run '^TestEveryLevelGoldenPath$$|^TestLevelsRejectNearMisses$$' -timeout 30m ./cmd/shellforge/...

## demo: Re-record the README demo GIF with VHS.
#
# The recording runs against a throwaway XDG_DATA_HOME so it can never show,
# or write to, the recorder's own progress database. Without that the GIF
# carries whoever made it: their XP, their rank, their achievement list.
#
# It needs a working sandbox, so a container engine has to be up. The tape
# provisions one in a hidden block rather than making anyone watch it.
#
# Deliberately not mirrored in make.ps1, which the header above otherwise asks
# to be kept in sync. The demo is recorded once per release by whoever cuts it,
# on the platform the GIF is recorded on, and a Windows twin nobody has run
# would be a target that looks supported and is not.
demo: build
	@command -v vhs >/dev/null 2>&1 || { \
	  echo "FAIL: vhs is not installed, so there is nothing to record with." >&2; \
	  echo "  The demo is a scripted recording rather than a screen capture." >&2; \
	  echo "  Install VHS from https://github.com/charmbracelet/vhs, then run: make demo" >&2; \
	  exit 1; }
	@tmp=$$(mktemp -d) && \
	  echo "recording with XDG_DATA_HOME=$$tmp" && \
	  PATH="$(CURDIR)/$(BIN_DIR):$$PATH" XDG_DATA_HOME="$$tmp" vhs docs/assets/demo.tape; \
	  status=$$?; rm -rf "$$tmp"; exit $$status
	@ls -l docs/assets/demo.gif
	@echo "Target is under 5 MB. If it is over, lower Set Framerate or Set Width in the tape."

## tools: Report the toolchain versions this repo expects.
tools:
	@go version
	@echo "container engine: $(CONTAINER_ENGINE)"
	@$(CONTAINER_ENGINE) --version 2>/dev/null || echo "  not available"

## clean: Remove build output.
clean:
	rm -rf $(BIN_DIR) $(DIST_DIR) images/out coverage.out coverage.html
	go clean -testcache

## ci: Everything CI runs.
ci: lint test race sec
	@echo "OK: ci clean"
