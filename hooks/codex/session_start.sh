#!/usr/bin/env bash
# edc Codex SessionStart hook: register this Codex session in presence, tagged agent=codex.
# Silent and fail-soft — a missing/unconfigured presence must never block or break the session.
#
# Interactive Codex sessions carry no /inject listener, so they register with inject_port=0:
# discoverable in the mesh (dedup, awareness) but not an injection target. The always-on
# injectable Codex session is the `edc codex serve` daemon, which self-registers with a real port.

INPUT="$(cat)"
sid=$(printf '%s' "$INPUT" | python3 -c "import sys,json;print(json.load(sys.stdin).get('session_id',''))" 2>/dev/null || echo "")
cwd=$(printf '%s' "$INPUT" | python3 -c "import sys,json;print(json.load(sys.stdin).get('cwd',''))" 2>/dev/null || echo "")

# cd into the session's cwd so presence detects the repo it is working in.
[ -n "$cwd" ] && [ -d "$cwd" ] && cd "$cwd" 2>/dev/null

if command -v presence >/dev/null 2>&1; then
  presence register --agent codex ${sid:+--session-id "$sid"} >/dev/null 2>&1 || true
fi
exit 0
