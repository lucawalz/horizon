VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS := -X github.com/lucawalz/horizon/internal/version.version=$(VERSION)
PREFIX ?= $(HOME)/.local/bin
CONTROLLER_GEN := go tool controller-gen
SETUP_ENVTEST := go tool setup-envtest
GOFUMPT := go tool gofumpt
GOLANGCI_LINT_VERSION ?= v2.12.2
GOLANGCI_LINT := go run github.com/golangci/golangci-lint/v2/cmd/golangci-lint@$(GOLANGCI_LINT_VERSION)
GORELEASER_VERSION ?= v2.17.0
GORELEASER := go run github.com/goreleaser/goreleaser/v2@$(GORELEASER_VERSION)
API_PATHS := ./api/...
CRD_DIR := config/crd/bases
CHART_DIR := charts/horizon
CHART_CRD_DIR := $(CHART_DIR)/crds
GENERATED_PATHS := '*zz_generated.deepcopy.go' $(CRD_DIR) $(CHART_CRD_DIR)
SITE_DIR := internal/web/site
SITE_DIST := $(SITE_DIR)/dist
IN_SITE := cd $(SITE_DIR) &&
IMAGE ?= ghcr.io/lucawalz/horizon
ENVTEST_K8S_VERSION ?= 1.36.2
ENVTEST_BIN_DIR := $(CURDIR)/bin
KUBEBUILDER_ASSETS = $(shell $(SETUP_ENVTEST) use $(ENVTEST_K8S_VERSION) --bin-dir $(ENVTEST_BIN_DIR) -p path)

define assert_unchanged
@if [ -n "$$(git status --porcelain -uall -- $(1))" ]; then \
		git status --porcelain -uall -- $(1) >&2; \
		echo "$(2)" >&2; \
		exit 1; \
	fi
endef

.PHONY: build test test-race vet fmt install uninstall manifests generate image chart-lint envtest lint tidy-check adr-check release-check site manifests-check verify

verify: tidy-check adr-check vet build manifests-check chart-lint lint release-check test test-race site

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

lint:
	$(GOLANGCI_LINT) run ./...

tidy-check:
	go mod tidy -diff

adr-check:
	bash scripts/check-adr-index.sh

release-check:
	$(GORELEASER) check

manifests:
	$(CONTROLLER_GEN) crd paths=$(API_PATHS) output:crd:artifacts:config=$(CRD_DIR)
	cp $(CRD_DIR)/*.yaml $(CHART_CRD_DIR)/

generate:
	$(CONTROLLER_GEN) object paths=$(API_PATHS)

manifests-check: manifests generate
	$(call assert_unchanged,$(GENERATED_PATHS),generated manifests are stale; run make manifests generate and commit the result)

image:
	docker build --build-arg VERSION=$(VERSION) -t $(IMAGE):$(VERSION) .

chart-lint:
	diff -r $(CRD_DIR) $(CHART_CRD_DIR)
	@app_version="$$(awk '$$1 == "appVersion:" { gsub(/"/, "", $$2); print $$2; exit }' $(CHART_DIR)/Chart.yaml)"; \
	repository="$$(awk '$$1 == "repository:" { print $$2; exit }' $(CHART_DIR)/values.yaml)"; \
	resolved="$$(helm template horizon $(CHART_DIR) | awk '$$1 == "image:" { print $$2; exit }')"; \
	if [ "$$resolved" != "$$repository:$$app_version" ]; then \
		echo "chart resolves $$resolved but appVersion declares $$repository:$$app_version" >&2; \
		exit 1; \
	fi
	helm lint $(CHART_DIR)
	helm template horizon $(CHART_DIR) --include-crds >/dev/null

site:
	bash scripts/check-vite-loopback.sh
	$(IN_SITE) npm ci
# the root tsconfig carries project references only, so a plain tsc --noEmit checks nothing
	$(IN_SITE) npx tsc -b --noEmit
	$(IN_SITE) npm test
	$(IN_SITE) npm run lint
	$(IN_SITE) npm run build
	$(call assert_unchanged,$(SITE_DIST),$(SITE_DIST) is stale; rebuild it with npm run build and commit the result)

install: build
	mkdir -p $(PREFIX)
	rm -f $(PREFIX)/horizon
	cp horizon $(PREFIX)/horizon
	@command -v codesign >/dev/null 2>&1 && codesign --force --sign - $(PREFIX)/horizon || true

uninstall:
	rm -f $(PREFIX)/horizon
