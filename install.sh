#!/usr/bin/env bash
set -euo pipefail
cd "$(dirname "$0")"
echo "==> go mod tidy..."
go mod tidy
echo "==> Building for $(uname -s)/$(uname -m)..."
go build -ldflags "-s -w" -o mcp-fs .
echo "==> Installing to /usr/local/bin/mcp-fs ..."
install -m 0755 mcp-fs /usr/local/bin/mcp-fs
echo "Done! Run: mcp-fs --help"
