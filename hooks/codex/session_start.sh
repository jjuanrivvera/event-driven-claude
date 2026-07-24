#!/usr/bin/env bash
# edc Codex SessionStart hook: register this Codex session in presence (agent=codex) and,
# if it runs inside tmux, spawn a per-session web terminal so the cockpit can attach it —
# the same wiring as the Claude session-start hook, via `presence ttyd`.
# Silent and fail-soft — a missing/unconfigured presence must never block or break the session.
#
# Interactive Codex sessions carry no /inject listener, so they register with inject_port=0:
# discoverable in Plexus (dedup, awareness) but not an injection target. The always-on
# injectable Codex session is the `edc codex serve` daemon, which self-registers with a real port.
#
# Cleanup of the ttyd is left to `presence ttyd reap` (it runs on every spawn and drops
# terminals whose tmux session is gone): Codex has no SessionEnd hook to kill it explicitly.

INPUT="$(cat)"
sid=$(printf '%s' "$INPUT" | python3 -c "import sys,json;print(json.load(sys.stdin).get('session_id',''))" 2>/dev/null || echo "")
cwd=$(printf '%s' "$INPUT" | python3 -c "import sys,json;print(json.load(sys.stdin).get('cwd',''))" 2>/dev/null || echo "")

# cd into the session's cwd so presence detects the repo it is working in.
[ -n "$cwd" ] && [ -d "$cwd" ] && cd "$cwd" 2>/dev/null

PBIN="$HOME/.local/bin/presence"; [ -x "$PBIN" ] || PBIN="$(command -v presence 2>/dev/null)"
[ -n "$PBIN" ] || exit 0

# Web-terminal attach: if inside tmux, spawn a per-session ttyd (presence derives the tmux
# socket from $TMUX) and advertise its address; register then picks it up via $PRESENCE_ATTACH_ADDR.
if [ -n "${TMUX:-}" ] && [ -n "$sid" ] && command -v tmux >/dev/null 2>&1; then
  TSESS=$(tmux display-message -p '#S' 2>/dev/null)
  if [ -n "$TSESS" ]; then
    ADDR=$("$PBIN" ttyd spawn "$sid" "$TSESS" 2>/dev/null)
    [ -n "$ADDR" ] && export PRESENCE_ATTACH_ADDR="$ADDR"
  fi
fi

"$PBIN" register --agent codex ${sid:+--session-id "$sid"} >/dev/null 2>&1 || true
exit 0
