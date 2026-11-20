BINARY      := sshm
MODULE      := github.com/master2sid2/sshm
VERSION     := $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS     := -X '$(MODULE)/internal/version.Version=$(VERSION)'
INSTALL_DIR := $(HOME)/.local/bin
DIST_DIR    := dist

PLATFORMS   := linux/amd64 linux/arm64 darwin/amd64 darwin/arm64

.PHONY: all build build-darwin build-darwin-arm64 build-darwin-amd64 dist demo run clean test vet lint fmt install uninstall tidy release

all: build

## build: compile ./sshm with the version baked in
build:
	go build -ldflags "$(LDFLAGS)" -o $(BINARY) .

## build-darwin-arm64: cross-compile for macOS on Apple Silicon -> dist/sshm-darwin-arm64
build-darwin-arm64:
	mkdir -p $(DIST_DIR)
	CGO_ENABLED=0 GOOS=darwin GOARCH=arm64 go build -ldflags "$(LDFLAGS)" -o $(DIST_DIR)/$(BINARY)-darwin-arm64 .

## build-darwin-amd64: cross-compile for macOS on Intel -> dist/sshm-darwin-amd64
build-darwin-amd64:
	mkdir -p $(DIST_DIR)
	CGO_ENABLED=0 GOOS=darwin GOARCH=amd64 go build -ldflags "$(LDFLAGS)" -o $(DIST_DIR)/$(BINARY)-darwin-amd64 .

## build-darwin: build both macOS binaries (Apple Silicon + Intel) into dist/
build-darwin: build-darwin-arm64 build-darwin-amd64

## dist: release binaries for Linux and macOS (amd64 + arm64) with SHA256SUMS in dist/
dist:
	mkdir -p $(DIST_DIR)
	@for p in $(PLATFORMS); do \
		os=$${p%/*}; arch=$${p#*/}; out=$(DIST_DIR)/$(BINARY)-$(VERSION)-$$os-$$arch; echo $$out; \
		CGO_ENABLED=0 GOOS=$$os GOARCH=$$arch go build -trimpath -ldflags "-s -w $(LDFLAGS)" -o $$out . || exit 1; \
	done
	cd $(DIST_DIR) && sha256sum $(BINARY)-$(VERSION)-* > SHA256SUMS

## demo: record docs/demo/sshm.gif with VHS against a throwaway SSH server (needs Docker)
demo: export CGO_ENABLED := 0
demo: build
	@set -e; tmp=$$(mktemp -d); \
	cleanup() { docker rm -f sshm-demo-web >/dev/null 2>&1 || true; \
		docker network rm sshm-demo >/dev/null 2>&1 || true; \
		rm -f "$$tmp/id_ed25519" "$$tmp/id_ed25519.pub"; rmdir "$$tmp"; }; \
	trap cleanup EXIT; \
	ssh-keygen -q -t ed25519 -N '' -C demo -f "$$tmp/id_ed25519"; \
	docker network create sshm-demo >/dev/null; \
	docker run -d --name sshm-demo-web --hostname web-01 --network sshm-demo --network-alias web-01 \
		-v "$$tmp":/keys:ro -v $(CURDIR)/docs/demo/server.sh:/server.sh:ro alpine:3.20 sh /server.sh >/dev/null; \
	for i in $$(seq 60); do docker logs sshm-demo-web 2>&1 | grep -q 'Server listening' && break; sleep 1; done; \
	docker run --rm --shm-size=1g --user $$(id -u):$$(id -g) -e HOME=/tmp --network sshm-demo \
		-v $(CURDIR):/vhs -v "$$tmp":/keys:ro -w /vhs ghcr.io/charmbracelet/vhs:v0.12.1 docs/demo/sshm.tape

## run: build then launch the TUI
run: build
	./$(BINARY)

## test: run the Go test suite
test:
	go test ./...

## vet: run go vet across the module
vet:
	go vet ./...

## lint: run golangci-lint (configuration in .golangci.yml)
lint:
	golangci-lint run

## fmt: gofmt all source files
fmt:
	gofmt -w .

## tidy: sync go.mod/go.sum
tidy:
	go mod tidy

## install: build and copy the binary onto $PATH ($(INSTALL_DIR))
install: build
	mkdir -p $(INSTALL_DIR)
	cp $(BINARY) $(INSTALL_DIR)/$(BINARY)

## uninstall: remove the installed binary
uninstall:
	rm -f $(INSTALL_DIR)/$(BINARY)

## clean: remove local build artifacts (never touches the vault)
clean:
	rm -f $(BINARY) $(BINARY)-v* $(DIST_DIR)/$(BINARY)-* $(DIST_DIR)/SHA256SUMS

## release: build a version-tagged binary, e.g. sshm-v0.2.0
release: build
	cp $(BINARY) $(BINARY)-$(VERSION)
