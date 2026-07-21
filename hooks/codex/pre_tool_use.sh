#!/usr/bin/env bash
# edc Codex PreToolUse hook: heartbeat presence (state=busy — the session is actively working).
# Silent, fail-soft, and MUST NOT block the tool call: no stdout, always exit 0.

INPUT="$(cat)"
sid=$(printf '%s' "$INPUT" | python3 -c "import sys,json;print(json.load(sys.stdin).get('session_id',''))" 2>/dev/null || echo "")

if [ -n "$sid" ] && command -v presence >/dev/null 2>&1; then
  presence heartbeat --session-id "$sid" --state busy >/dev/null 2>&1 || true
fi
exit 0
