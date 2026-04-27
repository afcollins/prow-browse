BINARY  := prow-status
LDFLAGS := -s -w
PREFIX  := $(HOME)/.local

.PHONY: build test clean install

build:
	go build -ldflags="$(LDFLAGS)" -o $(BINARY) .

test:
	go test -v ./...

install: build
	mkdir -p $(PREFIX)/bin
	cp $(BINARY) $(PREFIX)/bin/$(BINARY)

clean:
	rm -f $(BINARY)
