# mcp-fs

A lightweight MCP (Model Context Protocol) filesystem server written in Go. Exposes a set of file tools to AI assistants like Perplexity via either **stdio** or **SSE (Server-Sent Events)** transport.

---

## Installation

```bash
./install.sh
```

This builds the binary and installs it to `/usr/local/bin/mcp-fs`.

---

## Modes

### Stdio Mode (default)

Used for direct process-based MCP clients. The client spawns `mcp-fs` as a subprocess and communicates over stdin/stdout.

```bash
mcp-fs -log /tmp/mcp-fs.log /your/project /another/dir
```

> **Note:** Perplexity's XPC helper uses stdio mode but spawns a fresh process per tool call (one-shot). This causes repeated approval prompts and unstable sessions. **SSE mode is strongly recommended for Perplexity.**

---

### SSE Mode

Runs a persistent HTTP server on localhost. The AI client connects once and reuses the connection for all tool calls — no per-call process spawning, no approval loops, no context cancellation churn.

```bash
mcp-fs -sse -log /tmp/mcp-fs.log
```

Roots are read from `~/.mcp-fs-sse.json` automatically. You can also pass roots as CLI arguments to override the config:

```bash
mcp-fs -sse /your/project /another/dir
```

Two endpoints are served on the same port:

| Endpoint | Purpose |
|---|---|
| `http://localhost:8765/sse` | MCP SSE endpoint — point Perplexity here |
| `http://localhost:8765/` | Web GUI — manage roots in your browser |

---

## Perplexity Connector Setup

In the Perplexity app, open **Settings → Connectors**, add a new connector, and choose **Server-Sent Events**:

![Edit Connector dialog](edit%20connector.png)

- **Server Name:** `mcp-fs-sse` (or any label you prefer)
- **SSE Endpoint URL:** `http://localhost:8765/sse`
- No headers required

---

## Config File

`~/.mcp-fs-sse.json` stores your persistent configuration:

```json
{
  "port": 8765,
  "roots": [
    "/opt/my-project",
    "/Users/you/projects"
  ]
}
```

| Key | Default | Description |
|---|---|---|
| `port` | `8765` | HTTP port to listen on |
| `roots` | `[]` | Allowed directory paths |

- CLI arguments override the config file for that session only
- Changes made via the web GUI are written back to the config file immediately and take effect without a restart

---

## Web GUI

Open `http://localhost:8765/` in your browser while the server is running.

**Active Roots (left panel)**

- Shows all currently allowed roots
- Click **×** next to a root to remove it from the pending list
- Click **Apply Changes** to commit — roots update live with no restart, and `~/.mcp-fs-sse.json` is saved automatically

**Browse & Add (right panel)**

- Navigates your full local filesystem
- Noisy directories (`.git`, `node_modules`, etc.) are hidden automatically
- Click any folder to descend into it; click `..` to go up
- Click **+** next to any folder to add it to the active roots list

---

## LaunchAgent

To start `mcp-fs` automatically at login, install the provided plist:

```bash
cp ai.perplexity.mcp-fs.plist ~/Library/LaunchAgents/
launchctl load ~/Library/LaunchAgents/ai.perplexity.mcp-fs.plist
```

The plist contents:

```xml
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
    <key>Label</key>
    <string>ai.perplexity.mcp-fs</string>
    <key>Program</key>
    <string>/usr/local/bin/mcp-fs</string>
    <key>ProgramArguments</key>
    <array>
        <string>/usr/local/bin/mcp-fs</string>
        <string>-sse</string>
        <string>-log</string>
        <string>/tmp/mcp-fs.log</string>
    </array>
    <key>RunAtLoad</key>
    <true/>
    <key>KeepAlive</key>
    <true/>
    <key>StandardErrorPath</key>
    <string>/tmp/mcp-fs-stderr.log</string>
</dict>
</plist>
```

> No root paths are listed in `ProgramArguments` — the LaunchAgent reads roots from `~/.mcp-fs-sse.json`. Add or remove roots at any time via the web GUI without restarting.

**Stop / restart the agent:**

```bash
launchctl unload ~/Library/LaunchAgents/ai.perplexity.mcp-fs.plist
launchctl load   ~/Library/LaunchAgents/ai.perplexity.mcp-fs.plist
```

---

## Available Tools

| Tool | Description |
|---|---|
| `read_file` | Read complete file contents |
| `write_file` | Write or overwrite a file |
| `patch_file` | Replace first occurrence of a string in a file |
| `list_directory` | List files and directories at a path (non-recursive) |
| `search_files` | Recursively find files matching a glob pattern |
| `create_directory` | Create a directory and any necessary parents |
| `delete_file` | Delete a file (not directories) |
| `get_file_info` | Return size, mod time, and permissions for a path |

All tools enforce the allowed roots — any path outside them returns an error immediately.

---

## Ignored Directories

The following directory names are automatically hidden from `list_directory` and skipped entirely during `search_files`:

`.git` · `.svn` · `.hg` · `node_modules` · `vendor` · `.venv` · `venv` · `__pycache__` · `.pytest_cache` · `.mypy_cache` · `dist` · `build` · `.next` · `.nuxt` · `.turbo` · `.cache` · `.DS_Store`

---

## Flags

| Flag | Default | Description |
|---|---|---|
| `-sse` | `false` | Run in SSE/HTTP mode instead of stdio |
| `-port` | `8765` | HTTP port (SSE mode only, overrides config) |
| `-log` | stderr | Path to append log output to |

---

## License

Apache 2.0
