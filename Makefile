VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS := -X github.com/lucawalz/horizon/internal/version.version=$(VERSION)

.PHONY: build test vet

build:
	go build -ldflags "$(LDFLAGS)" -o horizon ./cmd/horizon

test:
	go test ./...

vet:
	go vet ./...
