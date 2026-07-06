# Changelog

## v0.1.2

- Fix the plugin load path. Claude Code passes a plugin manifest's `"${EDC_INJECT_PORT}"` through
  **verbatim** (unexpanded) when the var isn't set in the host env, and that literal was winning
  over the config file — the listener tried to bind a garbage port and never came up. `edc` now
  treats an unexpanded `${...}` value as unset, so the config file is the source on the plugin
  path. The shipped `.mcp.json` also drops its `env` block (it only produced those placeholders).
  With this, `claude plugin install` + `~/.config/edc/config.json` brings the `/inject` listener
  up. Verified end-to-end.

## v0.1.1

- Config file support: `edc` reads `inject_port` / `inject_secret` / `inject_bind` from
  `~/.config/edc/config.json` (override with `$EDC_CONFIG`) when the env vars aren't set — env
  still takes precedence. This makes the plugin install path work: a plugin-loaded MCP server
  doesn't inherit the launching shell's env, so the file is how you hand it a port and secret.

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
