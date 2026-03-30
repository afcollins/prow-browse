BINARY  := prow-status
LDFLAGS := -s -w

.PHONY: build clean

build:
	go build -ldflags="$(LDFLAGS)" -o $(BINARY) .

clean:
	rm -f $(BINARY)
