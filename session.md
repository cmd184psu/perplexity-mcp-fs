# mcp-fs Session Log

Running development log. Update this file at the end of each session or when a significant decision is made.

---

## Project Overview

**Repo:** `/opt/perplexity-mcp-fs`  
**Binary:** `/usr/local/bin/mcp-fs` (installed via `make install`)  
**Purpose:** Local MCP filesystem server for Perplexity. Exposes 8 file tools over SSE (preferred) or stdio.

**SSE endpoint:** `http://localhost:8765/sse`  
**GUI:** `http://localhost:8765/`  
**Config:** `~/.mcp-fs-sse.json`  
**LaunchAgent plist:** `~/Library/LaunchAgents/ai.perplexity.mcp-fs.plist`  
**Log:** `/tmp/mcp-fs.log` (when run via LaunchAgent)

---

## Architecture

```
main.go              — Go server: MCP tools, API handlers, HTTP mux, main()
main_test.go         — Table-driven unit tests
Makefile             — build / install / test / cover / vet / check
static/
  index.html         — HTML shell, links to CSS + JS
  css/style.css      — All GUI styles
  js/app.js          — All GUI logic (roots, browse tree, breadcrumb)
README.md
session.md           — This file
```

**Static files** are served from `./static/` relative to the binary location.  
Fallback: if binary is in a temp dir (e.g. `go run`), falls back to `$CWD/static/`.

---

## MCP Tools (all 8 implemented)

| Tool | Description |
|---|---|
| `read_file` | Read complete file contents |
| `write_file` | Write or overwrite a file |
| `patch_file` | Replace first occurrence of string in a file |
| `list_directory` | Non-recursive directory listing (skips noise dirs) |
| `search_files` | Recursive glob search (skips noise dirs) |
| `create_directory` | mkdir -p |
| `delete_file` | Delete a file (not dirs) |
| `get_file_info` | stat: size, mod time, permissions |

---

## Ignored Directories (`skipDirs`)

These are silently skipped in `list_directory` and `search_files` to prevent context overload:

`.git` `.svn` `.hg` `node_modules` `.node_modules` `vendor` `.venv` `venv` `__pycache__` `.pytest_cache` `.mypy_cache` `dist` `build` `.next` `.nuxt` `.turbo` `.cache` `.DS_Store`

---

## Session History

### 2026-04-16 (Thread 1)

**Problems diagnosed:**
- Stdio mode caused Perplexity to spawn a new process per tool call (one-shot XPC helper pattern) — led to context loss, approval loops, and session instability.
- `guiHTML` was a 22KB Go string constant embedded in `main.go` making the file uneditable via `patch_file` (truncation at 15KB).
- The `+ Add` button in the directory browser used inline `onclick` with string-interpolated paths — paths containing slashes broke the HTML attribute, so adding roots never worked.

**Changes made:**
- Switched connector to SSE mode (`http://localhost:8765/sse`) — stable, persistent, no approval loops.
- Added `skipDirs` map; applied to both `list_directory` and `search_files`.
- Moved all GUI out of `main.go` into `static/` (index.html, css/style.css, js/app.js).
- Fixed `+ Add` button: now uses `addEventListener` with proper closure over `e.path` — no inline HTML string interpolation.
- Added `main_test.go` with table-driven tests: `resolvePath`, `setRoots`, `skipDirs`, patch logic, list-directory skip integration.
- Added `make test`, `make cover`, `make vet`, `make check` targets.
- Wrote `README.md` and this `session.md`.

**Current state:** Code written, needs `make build` + restart.

**Next steps:**
- `make check` — run vet + tests, fix any issues
- `make install` — rebuild binary
- Restart LaunchAgent to pick up new binary
- Verify GUI in browser: browse tree, `+ Add` button stages a root, Apply saves it
- Add more test coverage: `handleBrowse`, `handleSetRoots` via `httptest`
- Consider `//go:embed static` so binary is self-contained (no dependency on `./static/` at runtime)

---

## Known Issues / Decisions Pending

| Item | Status |
|---|---|
| `//go:embed static` for self-contained binary | Not yet done — currently requires `./static/` next to binary |
| `make install` copies binary but not `static/` | Need install target to also copy `static/` or use embed |
| httptest coverage for API handlers | Not yet written |
| Config file path is hardcoded to `~/.mcp-fs-sse.json` | Fine for now |

---

## How to Resume in a New Thread

1. Add `/opt/perplexity-mcp-fs` as a root in the GUI or config
2. Ask Perplexity to read `session.md` first
3. Check `main.go`, `main_test.go`, `Makefile`, and `static/` for current state
4. Run `make check` to see test status before making changes

---

### 2026-04-16 23:21 EDT

**Summary**
- Embedded the GUI assets with `//go:embed static`, so the binary is now self-contained.
- Fixed the nil logger panic in tests by giving `logger` a default discard logger before `main()` runs.
- Added three new MCP tools in `main.go`: `make_test`, `make_build`, and `update_session_md`.
- Confirmed `make test` and `make check` pass after wiring in the new tools.
- Verified dynamic roots are working by listing `/opt/fs-tools` through the connector after adding it as a root.
- Added a Diagnostics panel to the GUI to probe tool exposure; browser-side `/message` probing currently returns HTTP 400 and does not yet prove tool discovery.

**Next steps**
- Check the connector dialog to confirm how many tools the client is actually seeing.
- If needed, add a simpler `/api/tools` smoke-test endpoint in the server that returns the registered tool list directly from Go.
- Update the older parts of this file so the project overview and known-issues section reflect the current embedded-static and new-tool state.
- Consider adding `httptest` coverage for API handlers and any future diagnostics endpoint.
