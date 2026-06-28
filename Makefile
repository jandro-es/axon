# AXON — common build/dev tasks.
# `make` builds the dashboard SPA and the single binary.

BINARY := axon
PKG    := ./cmd/axon
PREFIX ?= /usr/local
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS := -X main.version=$(VERSION)

.PHONY: all build web binary test race vet fmt fmtcheck cover clean install tidy

all: web binary

## web: build the React/Recharts dashboard (requires Node)
web:
	cd web && npm install && npm run build

## binary: build the Go binary (embeds web/dist)
binary:
	go build -ldflags "$(LDFLAGS)" -o $(BINARY) $(PKG)

## build: alias for binary
build: binary

## test: run the test suite
test:
	go test ./...

## race: run the test suite with the race detector
race:
	go test -race ./...

## cover: print per-package coverage
cover:
	go test -cover ./...

## vet: run go vet
vet:
	go vet ./...

## fmt: format all Go code
fmt:
	gofmt -w .

## fmtcheck: fail if any file is not gofmt-clean
fmtcheck:
	@test -z "$$(gofmt -l .)" || (echo "gofmt needed:"; gofmt -l .; exit 1)

## tidy: tidy module dependencies
tidy:
	go mod tidy

## install: install the binary to $(PREFIX)/bin
install: binary
	install -d $(PREFIX)/bin
	install -m 0755 $(BINARY) $(PREFIX)/bin/$(BINARY)

## clean: remove build artifacts
clean:
	rm -f $(BINARY)
	rm -rf web/node_modules web/dist/assets web/dist/index.html
