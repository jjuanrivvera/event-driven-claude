#!/usr/bin/env bash
# edc Codex Stop hook: heartbeat presence (state=idle — the turn finished). Codex has no reliable
# session-end hook, so the row is not explicitly deregistered here: once heartbeats stop, presence's
# TTL auto-prune removes it — the same path used for any session that dies without cleanup.
# Silent and fail-soft.

INPUT="$(cat)"
sid=$(printf '%s' "$INPUT" | python3 -c "import sys,json;print(json.load(sys.stdin).get('session_id',''))" 2>/dev/null || echo "")

if [ -n "$sid" ] && command -v presence >/dev/null 2>&1; then
  presence heartbeat --session-id "$sid" --state idle >/dev/null 2>&1 || true
fi
exit 0
