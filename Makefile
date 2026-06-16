VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS := -X github.com/lucawalz/horizon/internal/version.version=$(VERSION)
PREFIX ?= $(HOME)/.local/bin

.PHONY: build test vet install uninstall

build:
	go build -ldflags "$(LDFLAGS)" -o horizon ./cmd/horizon

test:
	go test ./...

vet:
	go vet ./...

install: build
	mkdir -p $(PREFIX)
	rm -f $(PREFIX)/horizon
	cp horizon $(PREFIX)/horizon
	@command -v codesign >/dev/null 2>&1 && codesign --force --sign - $(PREFIX)/horizon || true

uninstall:
	rm -f $(PREFIX)/horizon
