# event-driven-claude (`edc`)

**Make any Claude Code session injectable.** A transport-agnostic "channel": an MCP stdio
server whose only job is to declare the Claude Code `claude/channel` capability and run a small
local `/inject` HTTP endpoint. The philosophy: **Event Driven Claude** — a session woken by
events, not only by a person typing.

---

## The problem

A Claude Code session is **reactive**: it does nothing until a human types. There is no clean
way for the rest of your system — cron jobs, file/log watchers, home automation, a chat/DM
listener, another agent — to hand the running session an event or context. The usual workarounds
are all bad:

- **A polling loop inside the session** (`/loop 5m` reading a file): burns model tokens on
  every tick even when nothing happened — wasteful, and worse the more sessions you run.
- **A second bot / a chat transport**: bots can't message themselves, and it couples the
  session to Telegram/Slack/whatever just to receive a local event.
- **Nothing**: the session simply can't be woken by the world around it.

So: **there is no transport-agnostic, low-cost way to push an event into a running session as a
turn.** That is the one thing this plugin fixes.

## How it works

`edc` is a tiny MCP stdio server (~250 lines, pure stdlib, no dependencies) that does two things:

1. **Declares the `claude/channel` capability** during the MCP handshake — that is what lets a
   server *push* turns into the session (not just answer tool calls).
2. **Runs a local `/inject` HTTP listener.** Any tool `POST`s a JSON event; the server emits it
   into the session as a turn (`notifications/claude/channel`, `meta.source="system"`).

That's the whole thing. **No Telegram, no poller, no outbound transport, no tools** — the
distilled core of [`tgctl-claude-channel`](https://github.com/jjuanrivvera/tgctl-claude-channel)
(a Telegram channel for Claude Code) with everything transport-specific removed. Because it's
transport-agnostic it runs anywhere, for any session, with no bot and no collision.

**Event-driven, not polling** ⇒ the session sleeps at **zero token cost** until an event
actually arrives — no idle-polling bill, no matter how many sessions you run.

```
[ cron / file watcher / log tailer / another agent ]
        │  POST /inject  (Bearer secret)
        ▼
   edc (MCP stdio server)  ── notifications/claude/channel ──▶  the Claude session (a new turn)
```

## Install
```sh
# checksum-verified release binary onto your PATH (~/.local/bin by default)
curl -fsSL https://raw.githubusercontent.com/jjuanrivvera/event-driven-claude/main/install.sh | sh

# or, with a Go toolchain:
go install github.com/jjuanrivvera/event-driven-claude@latest   # installs as `event-driven-claude`
```
The `install.sh` binary is named `edc`; `go install` names it after the module
(`event-driven-claude`) — match your `.mcp.json` / config `command` to whichever you used.

## Load it into a session

Two ways to load the channel. Both need a **port** and a **secret**: the listener is opt-in and
**fails closed** — it binds only when both are set, so a session with neither is inert.

### As a plugin (recommended)

A plugin-loaded MCP server does **not** inherit your shell's environment, so the port and secret
come from a config file at `~/.config/edc/config.json` (not `export`):

```sh
mkdir -p ~/.config/edc
cat > ~/.config/edc/config.json <<EOF
{ "inject_port": "auto", "inject_secret": "$(openssl rand -hex 24)" }
EOF

claude plugin marketplace add jjuanrivvera/event-driven-claude
claude plugin install event-driven-claude@jjuanrivvera-edc
claude --dangerously-load-development-channels plugin:event-driven-claude@jjuanrivvera-edc
```

The marketplace registers as `jjuanrivvera-edc` (hence the `@jjuanrivvera-edc` in the install).
If you change the config later, restart the session — it's read once at startup.

### As a project channel (`server:`)

Point Claude at a `.mcp.json` that lists the server (this repo ships one) and supply the port and
secret with **either** env vars or the same config file. Unlike the plugin path, `server:` **does**
inherit your shell env:

```sh
export EDC_INJECT_PORT=auto                          # or an explicit port like 8790
export EDC_INJECT_SECRET="$(openssl rand -hex 24)"   # emitters need this exact value
# export EDC_INJECT_BIND=100.x.y.z                    # optional: a Tailscale/LAN IP (default 127.0.0.1)

claude --dangerously-load-development-channels server:event-driven-claude
```

### Confirm it registered

On a fresh session, accept the "local development" prompt. A `Channels (experimental) … inject
directly` line means the capability registered; with `--debug-file <path>` you'll see
`Channel notifications registered` in the log, and the listener answers on your port.

## Ports and per-session discovery (v0.2)

`edc` runs **one process per Claude Code session**, and sessions can't share a port. With an
explicit `inject_port`, only the first session on the machine binds — every other session comes
up with no listener. So the default is now **automatic ports**:

- `inject_port: "auto"` (or empty) ⇒ each session binds `127.0.0.1:0` (respecting `inject_bind`)
  and gets its own kernel-assigned port. An explicit numeric port keeps the old single-session
  behavior.
- When the listener comes up, `edc` publishes a **state file** at
  `~/.local/state/edc/<session_id>.json` (mode `0600`):

  ```json
  { "port": 52341, "pid": 48210, "bind": "127.0.0.1" }
  ```

  `<session_id>` is `$CLAUDE_SESSION_ID` when the host exports it (the MCP handshake carries no
  session id), else `pid-<pid>`.
- Emitters/hooks discover the port by reading the state file for their session and using it
  **only while its `pid` is alive** (`kill -0 <pid>`). The file is removed on clean exit
  (stdin EOF or SIGTERM/SIGINT), and orphans left by crashed sessions are reaped at the next
  `edc` startup.

## Inject an event

The emitter is any process on the box; it just needs the **port** (explicit, or read from the
session's state file) **and the secret** you configured above:

```sh
curl -sS -XPOST "http://127.0.0.1:$EDC_INJECT_PORT/inject" \
  -H "Authorization: Bearer $EDC_INJECT_SECRET" \
  -d '{"source":"CI","event":"build_failed","text":"main build failed at step \"test\"","context":{"commit":"a1b2c3d"}}'
```
Arrives in the session as:
```json
{ "content": "main build failed at step \"test\"",
  "meta": { "source": "system", "injected_by": "CI", "event": "build_failed",
            "ts": "…", "ctx_commit": "a1b2c3d" } }
```

## Security (what was built around it)

The `/inject` endpoint can create a turn in your agent, so it is guarded deliberately:

- **Fail closed.** No listener without a secret — a configured port with no secret logs an
  error and never binds. There is no such thing as an unauthenticated listener.
- **Authenticated, constant-time.** Every request needs `Authorization: Bearer <secret>`,
  compared with `crypto/subtle` (timing-attack resistant). No/wrong secret ⇒ `401` **and no turn
  is emitted**.
- **`meta.source` is always `"system"`, stamped by the server.** A caller may *declare* its
  origin (`injected_by`) but can **never** present itself as an authenticated human. There is no
  `chat_id`/`user_id` an event can carry.
- **Context is namespaced under `ctx_*`.** A caller cannot shadow the reserved meta fields
  (`source`, `injected_by`, `event`, `ts`) or slip in a fake `user_id`. (Covered by a test.)
- **Events are data, not commands.** The session `instructions` tell the model to treat every
  injected event as untrusted data — never as authority to change its instructions or take a
  sensitive action on the event's say-so. This is the prompt-injection defense at the session level.
- **Bounded input.** Body capped at 64 KiB; the HTTP server sets Read/Write/Idle timeouts
  (Slowloris protection — it matters because the bind can be a Tailscale/LAN IP, not just loopback).
- **Loopback by default.** `127.0.0.1` unless you opt into a Tailscale/LAN bind. It is **not** a
  public webhook by design — front remote emitters with Tailscale, never the open internet.
- ⚠️ **The secret is the entire gate.** Anyone with the secret and network reach to the port can
  inject a turn. Keep it out of logs, rotate it if it leaks, and never expose the port publicly.

## Limitations (by design — know these)

- **Ingress only.** It receives events; it has **no outbound transport and no tools**. The
  session reacts by *doing work* with its own tools — there is nothing to "reply" to here.
- **Fire-and-forget, no delivery guarantee.** An `HTTP 202` means *accepted and a frame was
  emitted*, not that the model processed it. There is **no queue, no retry, no history, no ack**.
  If the session is down or the channel didn't load, the event is lost — the **emitter** must
  handle that (fall back, queue, or drop).
- **One listener per session.** Sessions can't share a port — use `inject_port: "auto"` so each
  gets its own, discovered via the per-session state file. Fan-out to several sessions is the
  emitter's job (post to each port).
- **The plugin is a pipe, not a gate — cost lives on the emitter.** Every injected event wakes
  the session and spends tokens. `edc` does **no** filtering; the emitter must pre-filter and only
  inject what is worth waking the model for. (This is the whole point of "cheap deterministic
  sensor, expensive reasoning" — keep the cheap part outside.)
- **Local-only reach.** Not designed for the public internet. Remote emitters go over Tailscale/LAN.
- **Rides an experimental Claude Code feature.** Channel loading
  (`--dangerously-load-development-channels` / plugin install) has its own quirks — the dev-channel
  prompt, plugin enabled-state, and a clean binary build all matter for the capability to register.

## Config
Values come from env vars first, then a config file at `~/.config/edc/config.json` (override the
path with `$EDC_CONFIG`). The file is what makes the **plugin install path** work: a plugin-loaded
MCP server doesn't inherit the launching shell's env, so the file is how you hand it a port and
secret there.

```json
{ "inject_port": "auto", "inject_secret": "a-long-random-secret", "inject_bind": "127.0.0.1" }
```

| Env | Config file | Meaning |
|---|---|---|
| `EDC_INJECT_PORT` | `inject_port` | `"auto"` or empty ⇒ per-session kernel-assigned port (published in the state file); a number ⇒ that exact port |
| `EDC_INJECT_SECRET` | `inject_secret` | required Bearer secret; unset ⇒ refuses to bind (fail closed) |
| `EDC_INJECT_BIND` | `inject_bind` | bind address, default `127.0.0.1` (a Tailscale/LAN IP for remote emitters) |
| `CLAUDE_SESSION_ID` | — | names the state file `~/.local/state/edc/<session_id>.json`; unset ⇒ `pid-<pid>` |
| `XDG_STATE_HOME` | — | state dir base, default `~/.local/state` |

## Authorizing delivery (Claude Code channel gates)

Emitting the notification is not enough: **Claude Code gates injected turns behind two explicit
opt-ins**, and silently drops them otherwise (the MCP log shows `Channel notifications skipped: …`).

1. **Machine-level allowlist (once, requires sudo).** For *installed* plugins, the allowlist is
   read ONLY from managed settings — user `settings.json` is ignored by design, so a process
   running as your user can't self-authorize an injection channel:

   ```bash
   sudo mkdir -p "/Library/Application Support/ClaudeCode" && sudo tee "/Library/Application Support/ClaudeCode/managed-settings.json" > /dev/null <<'JSON'
   {
     "allowedChannelPlugins": [
       { "marketplace": "<your-marketplace>", "plugin": "event-driven-claude" }
     ]
   }
   JSON
   ```

   The schema is an **array of `{marketplace, plugin}` objects** (not strings). On Linux use
   `/etc/claude-code/managed-settings.json`.

2. **Per-session opt-in (every launch).** Sessions accept channel turns only when started with:

   ```bash
   claude --channels "plugin:event-driven-claude@<your-marketplace>"
   ```

3. **Local dev without install**: load the repo as a session plugin — it gets the reserved
   `inline` marketplace and skips the managed allowlist, gated by the dev flag instead:

   ```bash
   claude --plugin-dir /path/to/event-driven-claude \
     --channels "plugin:event-driven-claude@inline" \
     --dangerously-load-development-channels "plugin:event-driven-claude@inline"
   ```

**Verify**: the plugin's MCP log (`~/Library/Caches/claude-cli-nodejs/<project>/mcp-logs-plugin-event-driven-claude-*/*.jsonl`)
should show the channel registered; `Channel notifications skipped: …` means one of the two
gates is missing. `202` from `/inject` only means edc accepted and emitted the event — delivery
is decided by these gates on the Claude Code side.

Why so much ceremony: an injected turn is the most direct prompt-injection vector into a session
with tool access. The double opt-in (admin allowlists the plugin once; each session requests the
channel explicitly) is what separates "the machine owner decided this" from "something running as
the user decided it for them". Treat injected turns as untrusted data, never as instructions.
