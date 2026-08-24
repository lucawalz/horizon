VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS := -X github.com/lucawalz/horizon/internal/version.version=$(VERSION)
PREFIX ?= $(HOME)/.local/bin
CONTROLLER_GEN := go tool controller-gen
SETUP_ENVTEST := go tool setup-envtest
GOFUMPT := go tool gofumpt
API_PATHS := ./api/...
CRD_DIR := config/crd/bases
CHART_DIR := charts/horizon
IMAGE ?= ghcr.io/lucawalz/horizon
ENVTEST_K8S_VERSION ?= 1.36.2
ENVTEST_BIN_DIR := $(CURDIR)/bin
KUBEBUILDER_ASSETS = $(shell $(SETUP_ENVTEST) use $(ENVTEST_K8S_VERSION) --bin-dir $(ENVTEST_BIN_DIR) -p path)

.PHONY: build test test-race vet fmt install uninstall manifests generate image chart-lint envtest

build:
	go build -trimpath -ldflags "$(LDFLAGS)" -o horizon ./cmd/horizon

envtest:
	$(SETUP_ENVTEST) use $(ENVTEST_K8S_VERSION) --bin-dir $(ENVTEST_BIN_DIR)

test: envtest
	KUBEBUILDER_ASSETS="$(KUBEBUILDER_ASSETS)" go test ./...

test-race: envtest
	KUBEBUILDER_ASSETS="$(KUBEBUILDER_ASSETS)" go test -race ./...

vet:
	go vet ./...

fmt:
	$(GOFUMPT) -w .

manifests:
	$(CONTROLLER_GEN) crd paths=$(API_PATHS) output:crd:artifacts:config=$(CRD_DIR)
	cp $(CRD_DIR)/*.yaml $(CHART_DIR)/crds/

generate:
	$(CONTROLLER_GEN) object paths=$(API_PATHS)

image:
	docker build --build-arg VERSION=$(VERSION) -t $(IMAGE):$(VERSION) .

chart-lint:
	diff -r $(CRD_DIR) $(CHART_DIR)/crds
	helm lint $(CHART_DIR)
	helm template horizon $(CHART_DIR) --include-crds >/dev/null

install: build
	mkdir -p $(PREFIX)
	rm -f $(PREFIX)/horizon
	cp horizon $(PREFIX)/horizon
	@command -v codesign >/dev/null 2>&1 && codesign --force --sign - $(PREFIX)/horizon || true

uninstall:
	rm -f $(PREFIX)/horizon
