package main

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// --- per-session state file ---------------------------------------------------------------
//
// edc runs ONE process per Claude Code session. With "auto" ports the kernel picks the port,
// so emitters can no longer read it from a fixed config — discovery happens through a state
// file at ~/.local/state/edc/<session_id>.json holding {"port":N,"pid":N,"bind":"..."}.
// Hooks/emitters trust the file only while its pid is alive; the file is removed on clean
// exit and orphans (pid dead) are reaped at the next edc startup.

type stateFile struct {
	Port int    `json:"port"`
	PID  int    `json:"pid"`
	Bind string `json:"bind"`
}

// stateDir is $XDG_STATE_HOME/edc, defaulting to ~/.local/state/edc.
func stateDir() string {
	if d := os.Getenv("XDG_STATE_HOME"); d != "" {
		return filepath.Join(d, "edc")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".local", "state", "edc")
}

// sessionID names the state file. The MCP handshake carries no session id (initialize params
// only bring protocolVersion/capabilities/clientInfo, and no notification delivers one), so
// the launch environment is the source: CLAUDE_SESSION_ID when the host exports it, else a
// pid-scoped fallback that is at least unique per live process.
func sessionID() string {
	if v := sanitizeID(envClean("CLAUDE_SESSION_ID")); v != "" {
		return v
	}
	return "pid-" + strconv.Itoa(os.Getpid())
}

// sanitizeID keeps the session id filesystem-safe: it becomes a filename, so path separators
// or anything exotic must never survive (a hostile CLAUDE_SESSION_ID cannot escape stateDir).
func sanitizeID(s string) string {
	var b strings.Builder
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '.', r == '_', r == '-':
			b.WriteRune(r)
		default:
			b.WriteRune('-')
		}
	}
	return strings.Trim(b.String(), ".")
}

func writeStateFile(dir, sid string, st stateFile) (string, error) {
	if dir == "" || sid == "" {
		return "", errors.New("no state dir or session id")
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	b, err := json.Marshal(st)
	if err != nil {
		return "", err
	}
	path := filepath.Join(dir, sid+".json")
	if err := os.WriteFile(path, b, 0o600); err != nil {
		return "", err
	}
	return path, nil
}

// cleanOrphanStateFiles reaps state files whose process died without cleanup (kill -9, crash,
// machine reboot). Runs once at startup, before this process writes its own file.
func cleanOrphanStateFiles(dir string) {
	if dir == "" {
		return
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	self := os.Getpid()
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		path := filepath.Join(dir, e.Name())
		b, err := os.ReadFile(path) //nolint:gosec // G304: files under the user's own state dir
		if err != nil {
			continue
		}
		var st stateFile
		if json.Unmarshal(b, &st) != nil || st.PID <= 0 || (st.PID != self && !pidAlive(st.PID)) {
			_ = os.Remove(path)
		}
	}
}
