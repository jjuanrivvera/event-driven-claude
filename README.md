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
go build -o ~/bin/edc .        # or drop a release binary on your PATH
```

## Load it into a session
The listener is **opt-in and fails closed** — it binds only when `EDC_INJECT_PORT` **and**
`EDC_INJECT_SECRET` are both set.

```sh
export EDC_INJECT_PORT=8790
export EDC_INJECT_SECRET="$(openssl rand -hex 24)"   # emitters need this exact value
# export EDC_INJECT_BIND=100.x.y.z                    # optional: a Tailscale/LAN IP (default 127.0.0.1)

claude --dangerously-load-development-channels server:event-driven-claude
```
`server:event-driven-claude` resolves from a `.mcp.json` that lists it (this repo ships one).
On a fresh session confirm the "local development" prompt; a `Channels (experimental) … inject
directly` line means the capability registered.

**Or as a plugin.** Because a plugin-loaded server doesn't inherit the shell env, put the port
and secret in `~/.config/edc/config.json` (see [Config](#config)) instead of exporting them:

```sh
claude plugin marketplace add jjuanrivvera/event-driven-claude
claude plugin install event-driven-claude@jjuanrivvera-edc
claude --dangerously-load-development-channels plugin:event-driven-claude@jjuanrivvera-edc
```

## Inject an event
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

- **Fail closed.** No listener unless both the port **and** a secret are set. A port with no
  secret logs an error and never binds — there is no such thing as an unauthenticated listener.
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
- **One listener per session.** Each injectable session needs its own `EDC_INJECT_PORT`; sessions
  can't share a port. Fan-out to several sessions is the emitter's job (post to each port).
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
{ "inject_port": "8790", "inject_secret": "a-long-random-secret", "inject_bind": "127.0.0.1" }
```

| Env | Config file | Meaning |
|---|---|---|
| `EDC_INJECT_PORT` | `inject_port` | listener port; unset ⇒ listener off |
| `EDC_INJECT_SECRET` | `inject_secret` | required Bearer secret; unset ⇒ refuses to bind (fail closed) |
| `EDC_INJECT_BIND` | `inject_bind` | bind address, default `127.0.0.1` (a Tailscale/LAN IP for remote emitters) |
