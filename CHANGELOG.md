# Changelog

All notable changes to Roach Code are recorded here.

## [1.0.0] — unreleased

First stable release.

### Highlights

- **Go kernel**: a single static binary (CGO-free), cross-compiled for
  darwin/linux/windows on amd64 + arm64.
- **Agent core**: the loop, built-in tools (read/write/edit/multi_edit/glob/grep/
  ls/bash/web_fetch/todo_write), permission gate, sandboxed bash, and
  prefix-cache–oriented design.
- **Subagents**: `task` plus explore/research/review/security_review skill agents.
- **Skills & hooks**: skills (`internal/skill`) and hooks
  (`internal/hook`), symlink-aware and slash-integrated.
- **MCP client**: connect external servers over stdio / Streamable HTTP; reads
  `[[plugins]]` and a project `.mcp.json`.
- **Code intelligence via CodeGraph**: a tree-sitter symbol/call graph
  (`codegraph_*` tools) — no embedding service or API cost. Fetched into a
  local cache on first use (or `roach-code codegraph install`) and indexed in
  the background, so installs and startup stay fast.
- **Memory**: `ROACH-CODE.md` hierarchy + auto-memory, folded into the cache-stable
  prefix.
- **ACP** (`roach-code acp`) and an HTTP/SSE server frontend; desktop app (Wails).

### Fixed

- **File encoding support** — GBK/GB18030 (and other non-UTF-8) files
  can now be read, edited, and grepped correctly. The read/edit/write round-trip
  preserves the original file encoding.

### Notes

- Release archives ship a bare binary; CodeGraph is fetched on first use. Windows
  support for the fetched runtime is unverified — install `codegraph` on PATH if
  the auto-fetch doesn't resolve there.

[1.0.0]: https://github.com/tmdgusya/roach-code/releases/tag/v1.0.0
