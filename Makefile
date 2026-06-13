BINARY  := pb
BUILD_DATE = $(shell date '+%Y-%m-%d-%H:%M:%S')
VERSION := $(shell git rev-parse --short HEAD)
LDFLAGS := -s -w -X main.Version=$(VERSION) -X main.BuildDate=$(BUILD_DATE) -X main.GitCommit=$(VERSION)
PREFIX  := $(HOME)/.local

.PHONY: build test lint fmt clean install

build:
	go build -ldflags="$(LDFLAGS)" -o $(BINARY) .

test:
	go test -v ./...

lint:
	go run github.com/golangci/golangci-lint/v2/cmd/golangci-lint@latest run ./...

fmt:
	gofmt -w .

install: build
	mkdir -p $(PREFIX)/bin
	cp $(BINARY) $(PREFIX)/bin/$(BINARY)

clean:
	rm -f $(BINARY)
