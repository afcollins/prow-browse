BINARY  := prow-status
LDFLAGS := -s -w
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
