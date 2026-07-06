# Changelog

## v0.1.0

Initial release.

- Transport-agnostic Claude Code channel: an MCP stdio server that declares the
  `claude/channel` capability and runs a local `/inject` HTTP endpoint, so any session can be
  woken by external events (crons, watchers, daemons) instead of only by a human typing.
- Security: fail-closed (no listener without a port **and** a secret), Bearer auth with a
  constant-time compare, `meta.source` always `system` (no impersonation), caller `context`
  namespaced under `ctx_*`, bounded request body + HTTP timeouts.
- Ships the plugin manifest (`.mcp.json`, `.claude-plugin/marketplace.json`) and prebuilt
  binaries (`edc`) for macOS/Linux/Windows.
