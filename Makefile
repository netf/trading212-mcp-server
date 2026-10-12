BINARY       := trading212-mcp-server
PKG          := ./cmd/$(BINARY)
VERSION      ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS      := -s -w -X main.version=$(VERSION)
GOLANGCI_LINT ?= golangci-lint
IMAGE        ?= $(BINARY):$(VERSION)
SPEC_URL     := https://docs.trading212.com/_bundle/api.yaml
SPEC_FILE    := internal/trading212/testdata/openapi.yaml

.PHONY: all build install run test test-race cover vet lint fmt tidy check docker clean update-spec check-spec

all: check build

build:
	CGO_ENABLED=0 go build -trimpath -ldflags "$(LDFLAGS)" -o bin/$(BINARY) $(PKG)

install:
	CGO_ENABLED=0 go install -trimpath -ldflags "$(LDFLAGS)" $(PKG)

run: build
	./bin/$(BINARY)

test:
	go test ./...

test-race:
	go test -race ./...

cover:
	go test -race -coverprofile=coverage.out ./...
	go tool cover -func=coverage.out | tail -n 1

vet:
	go vet ./...

lint:
	$(GOLANGCI_LINT) run ./...

fmt:
	gofmt -w .
	$(GOLANGCI_LINT) fmt ./...

tidy:
	go mod tidy

check: vet lint test-race

docker:
	docker build --build-arg VERSION=$(VERSION) -t $(IMAGE) .

# Refresh the Trading 212 OpenAPI snapshot used by the contract tests.
update-spec:
	curl -fsSL $(SPEC_URL) -o $(SPEC_FILE)
	go test ./internal/trading212/ -run 'Spec'

# Fail if the published Trading 212 OpenAPI document differs from the snapshot.
check-spec:
	curl -fsSL $(SPEC_URL) | diff -u $(SPEC_FILE) - && echo "OpenAPI snapshot is current"

clean:
	rm -rf bin coverage.out
