# mcp-fs — Stable Go Filesystem MCP Server

A zero-dependency, single-binary MCP filesystem server in Go.
Replaces `npx @modelcontextprotocol/server-filesystem` with a compiled binary
that never makes network calls and never suffers Node.js pipe/memory issues.

## Tools

| Tool              | Description                                      |
|-------------------|--------------------------------------------------|
| `read_file`       | Read full file contents                          |
| `write_file`      | Write/overwrite a file (auto-creates parent dirs)|
| `patch_file`      | Replace first occurrence of a string in a file   |
| `list_directory`  | Non-recursive directory listing                  |
| `search_files`    | Recursive glob search (e.g. `*.go`)              |
| `create_directory`| mkdir -p                                         |
| `delete_file`     | Delete a single file (refuses directories)       |
| `get_file_info`   | Stat: size, mod time, permissions                |
| `ping`            | Ping                                             |

## Security

Every operation validates the resolved absolute path is inside one of the
declared roots. Path traversal attempts are rejected before touching disk.
The binary opens zero network sockets.

## Requirements

- Go 1.23+
- Internet access only for the initial `go mod tidy` (fetches mark3labs/mcp-go)
- After that: fully air-gapped / dark-site compatible

## Quick Install

```bash
./install.sh
```

## Cross-Compile for Both Machines

```bash
make release
# Produces:
#   mcp-fs-darwin-arm64   → Mac M4
#   mcp-fs-linux-amd64    → x8664 Linux
```

Copy to Linux:
```bash
scp mcp-fs-linux-amd64 user@linux-host:/usr/local/bin/mcp-fs
```

## Perplexity Mac App — Connector Config

### Local Projects
```
Command:   /usr/local/bin/mcp-fs
Arguments: /project1 /project2
```

### Optional: file logging
```
Arguments: -log /tmp/mcp-fs.log /project1
```
Then: `tail -f /tmp/mcp-fs.log`
