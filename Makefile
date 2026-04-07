BINARY  := prow-status
LDFLAGS := -s -w

.PHONY: build test clean

build:
	go build -ldflags="$(LDFLAGS)" -o $(BINARY) .

test:
	go test -v ./...

clean:
	rm -f $(BINARY)
