BINARY  := mcp-fs
LDFLAGS := -ldflags "-s -w"

.PHONY: build build-mac build-linux release install clean

build:
	go build $(LDFLAGS) -o $(BINARY) .

build-mac:
	GOOS=darwin GOARCH=arm64 go build $(LDFLAGS) -o $(BINARY)-darwin-arm64 .

build-linux:
	GOOS=linux GOARCH=amd64 go build $(LDFLAGS) -o $(BINARY)-linux-amd64 .

release: build-mac build-linux

install: build
	install -m 0755 $(BINARY) /usr/local/bin/$(BINARY)
	@echo "Installed to /usr/local/bin/$(BINARY)"

clean:
	rm -f $(BINARY) $(BINARY)-darwin-arm64 $(BINARY)-linux-amd64
