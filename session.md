# MCP‑fs Vibe‑Coding Session

**Repo:** `/opt/perplexity-mcp-fs`
**Binary:** `/usr/local/bin/mcp-fs`
**SSE:** `http://localhost:8765/sse`
**GUI:** `http://localhost:8765/`

## Current State
- GUI assets embedded with `//go:embed static`.
- New tools: `make_test`, `make_build`, `update_session_md`.
- Tests pass (`make check`).
- Dynamic roots working.

## Current task
Task: Add /api/tools endpoint handler.
File(s): /opt/perplexity-mcp-fs/main.go
Goal: Expose all available MCP tools for discovery.
Change: Add the smallest safe handler and route registration only.
Stop when: /api/tools returns the tool list and tests pass.
Blocked if: main.go cannot access the tool registry.

## Next steps
- [ ] Verify tool discovery in the UI.
- [ ] Fix `/message` probing.
- [ ] Add `httptest` coverage.
- [ ] Fix `make install`.

## Resume Instructions
Say to the agent: "Read session.md, fix the next thing in the list."