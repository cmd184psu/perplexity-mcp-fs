BINARY  := mcp-fs
LDFLAGS := -ldflags "-s -w"

.PHONY: build build-mac build-linux release install clean test cover vet check

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

vet:
	go vet ./...

test:
	go test -v -race ./...

cover:
	go test -coverprofile=coverage.out ./...
	go tool cover -html=coverage.out -o coverage.html
	@echo "Coverage report: coverage.html"

check: vet test

clean:
	rm -f $(BINARY) $(BINARY)-darwin-arm64 $(BINARY)-linux-amd64 coverage.out coverage.html
