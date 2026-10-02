BINARY      := mcp-diary
PKG         := github.com/gogodjzhu/mcp-diary
CMD         := ./cmd/mcp-diary
BIN_DIR     := bin
WEB_DIR     := web
VERSION     ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS     := -s -w -X main.version=$(VERSION)

.PHONY: all build build-go web-install web-build web-clean run dev test test-race vet fmt tidy docker-build clean help

all: build

## build: build the web UI and compile the server binary into bin/
build: web-build
	@mkdir -p $(BIN_DIR)
	go build -trimpath -ldflags "$(LDFLAGS)" -o $(BIN_DIR)/$(BINARY) $(CMD)

## build-go: compile the server binary only (uses the committed web assets)
build-go:
	@mkdir -p $(BIN_DIR)
	go build -trimpath -ldflags "$(LDFLAGS)" -o $(BIN_DIR)/$(BINARY) $(CMD)

## web-install: install the frontend dependencies
web-install:
	cd $(WEB_DIR) && npm install

## web-build: build the frontend into internal/transport/webui/dist
web-build: web-install
	cd $(WEB_DIR) && npm run build

## web-clean: remove the frontend dependencies
web-clean:
	rm -rf $(WEB_DIR)/node_modules

## run: start the server locally with the current directory as workspace
run:
	go run -ldflags "$(LDFLAGS)" $(CMD) serve --root .

## dev: run the Vite dev server (proxying the API to :8080)
dev:
	cd $(WEB_DIR) && npm run dev

## test: run all unit and integration tests
test:
	go test ./...

## test-race: run tests with the race detector
test-race:
	go test -race ./...

## vet: run go vet
vet:
	go vet ./...

## fmt: format all Go source
fmt:
	gofmt -w .

## tidy: synchronize go.mod/go.sum
tidy:
	go mod tidy

## docker-build: build the container image
docker-build:
	docker build --build-arg VERSION=$(VERSION) -t $(BINARY):$(VERSION) .

## clean: remove build artifacts
clean:
	rm -rf $(BIN_DIR)

## help: list available targets
help:
	@grep -E '^## ' $(MAKEFILE_LIST) | sed 's/## //'
