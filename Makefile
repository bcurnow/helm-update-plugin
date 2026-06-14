.PHONY: all build test bench clean tidy fmt vet lint check generate install-dev help release prepare-release version coverage coverage-html _check-plugin-version

BINARY_NAME=helm-upgrade-check
BIN_DIR=bin
CMD_DIR=cmd/$(BINARY_NAME)
VERSION=$(shell git describe --tags --abbrev=0 2>/dev/null | sed 's/^v//' || echo "dev")

all: tidy fmt vet test build

tidy:
	go mod tidy

fmt:
	go fmt ./...

vet: tidy
	go vet ./...

lint:
	@command -v golangci-lint >/dev/null 2>&1 || (echo "Error: golangci-lint is not installed. See https://golangci-lint.run/welcome/install/"; exit 1)
	golangci-lint run

# CI gate: static analysis + lint + tests without auto-modifying source files.
check: vet lint test

generate:
	go generate ./...

build: tidy
	mkdir -p $(BIN_DIR)
	go build -o $(BIN_DIR)/$(BINARY_NAME) ./$(CMD_DIR)

test: tidy
	go test -v ./...

bench: tidy
	go test -bench=. -benchmem ./...

coverage: tidy
	go test -v -coverprofile=coverage.out ./...
	@echo "\nCoverage report generated: coverage.out"
	@go tool cover -func=coverage.out | tail -1

coverage-html: coverage
	go tool cover -html=coverage.out -o coverage.html
	@echo "HTML coverage report generated: coverage.html"

# Build and (re)install the plugin locally for development.
install-dev: build
	helm plugin remove upgrade-check 2>/dev/null || true
	helm plugin install .

clean:
	rm -rf $(BIN_DIR)
	rm -rf dist/
	rm -f coverage.out coverage.html
	go clean

help:
	@echo "Available targets:"
	@echo "  all           - tidy, fmt, vet, test, and build"
	@echo "  build         - build the plugin binary"
	@echo "  test          - run all tests"
	@echo "  bench         - run benchmarks"
	@echo "  coverage      - run tests with coverage reporting"
	@echo "  coverage-html - generate HTML coverage report (coverage.html)"
	@echo "  fmt           - format all Go source files with go fmt"
	@echo "  vet           - run go vet static analysis"
	@echo "  lint          - run golangci-lint (must be installed)"
	@echo "  check         - vet + lint + test (CI gate, no auto-formatting)"
	@echo "  generate      - run go generate"
	@echo "  install-dev   - build and (re)install the plugin locally"
	@echo "  tidy          - download and tidy Go module dependencies"
	@echo "  clean         - remove build artifacts"
	@echo "  version       - print current version"
	@echo "  prepare-release TAG=X.Y.Z - update plugin.yaml version before tagging"
	@echo "  release       - publish a release via goreleaser (tag must exist, plugin.yaml must match)"
	@echo "  help          - show this help message"

version:
	@echo "$(VERSION)"

prepare-release:
	@test -n "$(TAG)" || (echo "Usage: make prepare-release TAG=X.Y.Z"; exit 1)
	@sed -i 's/^version:.*/version: "$(TAG)"/' plugin.yaml
	@echo "Updated plugin.yaml to $(TAG) — commit it, then tag v$(TAG) and run make release"

_check-plugin-version:
	@PLUGIN_VER=$$(grep '^version:' plugin.yaml | sed 's/version: *"\(.*\)"/\1/'); \
	if [ "$$PLUGIN_VER" != "$(VERSION)" ]; then \
		echo "Error: plugin.yaml version ($$PLUGIN_VER) does not match release tag ($(VERSION))"; \
		echo "Run: make prepare-release TAG=$(VERSION)  then commit and re-tag"; \
		exit 1; \
	fi

release: tidy test _check-plugin-version
	@command -v goreleaser >/dev/null 2>&1 || (echo "Error: goreleaser is not installed. Install from https://goreleaser.com"; exit 1)
	@echo "Building release $(VERSION) with goreleaser..."
	goreleaser release --clean
