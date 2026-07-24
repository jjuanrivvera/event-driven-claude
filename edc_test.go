package main

import (
	"bytes"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestInitializeDeclaresChannelCapability(t *testing.T) {
	res := initializeResult([]byte(`{"protocolVersion":"2025-06-18"}`))
	caps, ok := res["capabilities"].(map[string]any)
	if !ok {
		t.Fatal("no capabilities")
	}
	exp, ok := caps["experimental"].(map[string]any)
	if !ok {
		t.Fatal("no experimental capabilities")
	}
	if _, ok := exp[channelCapability]; !ok {
		t.Fatalf("must declare %q so the session is a channel", channelCapability)
	}
	if got := res["protocolVersion"]; got != "2025-06-18" {
		t.Fatalf("protocolVersion echo: got %v", got)
	}
}

func TestBuildInjectNotification_TrustBoundary(t *testing.T) {
	// A caller trying to impersonate a human or shadow reserved fields must not succeed.
	req := injectRequest{
		Source:  "CRON",
		Event:   "build_failed",
		Text:    "hi",
		Context: map[string]string{"source": "telegram", "user_id": "999", "date": "2026-07-14"},
	}
	n := buildInjectNotification(req, time.Unix(0, 0).UTC()).(map[string]any)
	params := n["params"].(map[string]any)
	meta := params["meta"].(map[string]string)

	if meta["source"] != "system" {
		t.Fatalf("meta.source must always be system, got %q", meta["source"])
	}
	if meta["injected_by"] != "CRON" {
		t.Fatalf("injected_by: %q", meta["injected_by"])
	}
	// The caller's "source"/"user_id" land under ctx_* — they cannot shadow the real meta.
	if meta["ctx_source"] != "telegram" || meta["ctx_user_id"] != "999" {
		t.Fatalf("context must be namespaced under ctx_*, got %v", meta)
	}
	if _, leaked := meta["user_id"]; leaked {
		t.Fatal("caller managed to set a top-level user_id — impersonation hole")
	}
}

func TestInjectAuthorized(t *testing.T) {
	cases := []struct {
		header string
		want   bool
	}{
		{"Bearer sekret", true},
		{"Bearer wrong", false},
		{"sekret", false},
		{"", false},
		{"Bearer ", false},
	}
	for _, c := range cases {
		if got := injectAuthorized(c.header, "sekret"); got != c.want {
			t.Errorf("injectAuthorized(%q) = %v, want %v", c.header, got, c.want)
		}
	}
}

func TestHandleInject(t *testing.T) {
	newSrv := func() (*server, *bytes.Buffer) {
		var buf bytes.Buffer
		return &server{out: newOut(&buf), cfg: Config{InjectSecret: "sekret"}}, &buf
	}

	t.Run("authorized emits a channel turn", func(t *testing.T) {
		s, buf := newSrv()
		req := httptest.NewRequest(http.MethodPost, "/inject", strings.NewReader(`{"source":"CRON","text":"hello"}`))
		req.Header.Set("Authorization", "Bearer sekret")
		rec := httptest.NewRecorder()
		s.handleInject(rec, req)
		if rec.Code != http.StatusAccepted {
			t.Fatalf("want 202, got %d", rec.Code)
		}
		var n map[string]any
		if err := json.Unmarshal(bytes.TrimSpace(buf.Bytes()), &n); err != nil {
			t.Fatalf("emitted frame is not JSON: %v", err)
		}
		if n["method"] != "notifications/claude/channel" {
			t.Fatalf("wrong method: %v", n["method"])
		}
	})

	t.Run("unauthorized is rejected and emits nothing", func(t *testing.T) {
		s, buf := newSrv()
		req := httptest.NewRequest(http.MethodPost, "/inject", strings.NewReader(`{"text":"x"}`))
		rec := httptest.NewRecorder()
		s.handleInject(rec, req)
		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("want 401, got %d", rec.Code)
		}
		if buf.Len() != 0 {
			t.Fatalf("a rejected request must not emit a turn, got %q", buf.String())
		}
	})

	t.Run("empty text is a bad request", func(t *testing.T) {
		s, _ := newSrv()
		req := httptest.NewRequest(http.MethodPost, "/inject", strings.NewReader(`{"text":"  "}`))
		req.Header.Set("Authorization", "Bearer sekret")
		rec := httptest.NewRecorder()
		s.handleInject(rec, req)
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("want 400, got %d", rec.Code)
		}
	})

	t.Run("GET is not allowed", func(t *testing.T) {
		s, _ := newSrv()
		req := httptest.NewRequest(http.MethodGet, "/inject", nil)
		rec := httptest.NewRecorder()
		s.handleInject(rec, req)
		if rec.Code != http.StatusMethodNotAllowed {
			t.Fatalf("want 405, got %d", rec.Code)
		}
	})
}

func TestLoadConfig_EnvOverridesFileOverridesDefault(t *testing.T) {
	cfg := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(cfg, []byte(`{"inject_port":"9000","inject_secret":"fromfile","inject_bind":"1.2.3.4"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("EDC_CONFIG", cfg)

	// Nothing in the env: the file wins (this is the plugin path, where the shell env
	// never reaches the process).
	t.Setenv("EDC_INJECT_PORT", "")
	t.Setenv("EDC_INJECT_SECRET", "")
	t.Setenv("EDC_INJECT_BIND", "")
	c := loadConfig()
	if c.InjectPort != "9000" || c.InjectSecret != "fromfile" || c.InjectBind != "1.2.3.4" {
		t.Fatalf("config file not applied: %+v", c)
	}

	// An env var beats the file.
	t.Setenv("EDC_INJECT_PORT", "8790")
	t.Setenv("EDC_INJECT_SECRET", "fromenv")
	c = loadConfig()
	if c.InjectPort != "8790" || c.InjectSecret != "fromenv" {
		t.Fatalf("env must override the file: %+v", c)
	}
}

func TestLoadConfig_IgnoresUnexpandedEnvPlaceholder(t *testing.T) {
	// The plugin load path: Claude Code passes the manifest's "${EDC_INJECT_PORT}" through
	// verbatim when the var isn't set. That literal must NOT win over the config file, or the
	// listener tries to bind a garbage port and never comes up.
	cfg := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(cfg, []byte(`{"inject_port":"8794","inject_secret":"fromfile"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("EDC_CONFIG", cfg)
	t.Setenv("EDC_INJECT_PORT", "${EDC_INJECT_PORT}")
	t.Setenv("EDC_INJECT_SECRET", "${EDC_INJECT_SECRET}")
	t.Setenv("EDC_INJECT_BIND", "${EDC_INJECT_BIND}")

	c := loadConfig()
	if c.InjectPort != "8794" || c.InjectSecret != "fromfile" {
		t.Fatalf("unexpanded ${...} placeholder must be treated as unset so the file wins: %+v", c)
	}
	if c.InjectBind != "127.0.0.1" {
		t.Fatalf("placeholder bind must fall back to loopback default, got %q", c.InjectBind)
	}
}

func TestLoadConfig_MissingFileFailsClosedWithBindDefault(t *testing.T) {
	t.Setenv("EDC_CONFIG", filepath.Join(t.TempDir(), "does-not-exist.json"))
	t.Setenv("EDC_INJECT_PORT", "")
	t.Setenv("EDC_INJECT_SECRET", "")
	t.Setenv("EDC_INJECT_BIND", "")
	c := loadConfig()
	if c.InjectPort != "" || c.InjectSecret != "" {
		t.Fatalf("no config anywhere should leave port/secret empty (listener stays off): %+v", c)
	}
	if c.InjectBind != "127.0.0.1" {
		t.Fatalf("bind should default to loopback, got %q", c.InjectBind)
	}
}

func TestStartInject_AutoPortTwoInstancesDoNotCollide(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	t.Setenv("CLAUDE_SESSION_ID", "")

	newAuto := func(sid string) *server {
		t.Setenv("CLAUDE_SESSION_ID", sid)
		return &server{out: newOut(&bytes.Buffer{}), cfg: Config{
			InjectPort: "auto", InjectSecret: "sekret", InjectBind: "127.0.0.1",
		}}
	}
	s1 := newAuto("sess-1")
	ln1 := s1.startInject()
	if ln1 == nil {
		t.Fatal("first auto listener failed to bind")
	}
	defer ln1.Close()

	s2 := newAuto("sess-2")
	ln2 := s2.startInject()
	if ln2 == nil {
		t.Fatal("second auto listener failed to bind — auto ports must not collide")
	}
	defer ln2.Close()

	p1 := ln1.Addr().(*net.TCPAddr).Port
	p2 := ln2.Addr().(*net.TCPAddr).Port
	if p1 == 0 || p2 == 0 || p1 == p2 {
		t.Fatalf("expected two distinct kernel-assigned ports, got %d and %d", p1, p2)
	}
}

func TestStartInject_ExplicitPortStillWorks(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	// Grab a free port, release it, then ask edc to bind it explicitly.
	probe, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := probe.Addr().(*net.TCPAddr).Port
	probe.Close()

	s := &server{out: newOut(&bytes.Buffer{}), cfg: Config{
		InjectPort: strconv.Itoa(port), InjectSecret: "sekret", InjectBind: "127.0.0.1",
	}}
	ln := s.startInject()
	if ln == nil {
		t.Fatal("explicit port failed to bind")
	}
	defer ln.Close()
	if got := ln.Addr().(*net.TCPAddr).Port; got != port {
		t.Fatalf("explicit port not honored: want %d, got %d", port, got)
	}
}

func TestStartInject_NoSecretFailsClosed(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	s := &server{out: newOut(&bytes.Buffer{}), cfg: Config{InjectPort: "auto", InjectBind: "127.0.0.1"}}
	if ln := s.startInject(); ln != nil {
		ln.Close()
		t.Fatal("a listener without a secret must never bind")
	}
}

func TestStateFileWrittenAndRemoved(t *testing.T) {
	stateHome := t.TempDir()
	t.Setenv("XDG_STATE_HOME", stateHome)
	t.Setenv("CLAUDE_SESSION_ID", "test-session-abc")

	s := &server{out: newOut(&bytes.Buffer{}), cfg: Config{
		InjectPort: "auto", InjectSecret: "sekret", InjectBind: "127.0.0.1",
	}}
	ln := s.startInject()
	if ln == nil {
		t.Fatal("listener failed to bind")
	}
	defer ln.Close()

	path := filepath.Join(stateHome, "edc", "test-session-abc.json")
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("state file not written: %v", err)
	}
	fi, _ := os.Stat(path)
	if fi.Mode().Perm() != 0o600 {
		t.Fatalf("state file must be 0600, got %v", fi.Mode().Perm())
	}
	var st stateFile
	if err := json.Unmarshal(b, &st); err != nil {
		t.Fatalf("state file is not JSON: %v", err)
	}
	if want := ln.Addr().(*net.TCPAddr).Port; st.Port != want {
		t.Fatalf("state file port %d != bound port %d", st.Port, want)
	}
	if st.PID != os.Getpid() {
		t.Fatalf("state file pid %d != own pid %d", st.PID, os.Getpid())
	}
	if st.Bind != "127.0.0.1" {
		t.Fatalf("state file bind: %q", st.Bind)
	}

	s.removeStateFile()
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatal("state file must be removed on shutdown")
	}
	s.removeStateFile() // idempotent
}

func TestCleanOrphanStateFiles(t *testing.T) {
	dir := t.TempDir()

	// A pid that existed and is now dead.
	cmd := exec.Command("true")
	if err := cmd.Run(); err != nil {
		t.Fatal(err)
	}
	deadPID := cmd.Process.Pid

	write := func(name, content string) string {
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
		return p
	}
	live := write("live.json", `{"port":1234,"pid":`+strconv.Itoa(os.Getpid())+`,"bind":"127.0.0.1"}`)
	dead := write("dead.json", `{"port":5678,"pid":`+strconv.Itoa(deadPID)+`,"bind":"127.0.0.1"}`)
	garbage := write("garbage.json", `not json`)
	other := write("notes.txt", `not a state file`)

	cleanOrphanStateFiles(dir)

	if _, err := os.Stat(live); err != nil {
		t.Fatal("state file of a live pid must survive cleanup")
	}
	if _, err := os.Stat(dead); !os.IsNotExist(err) {
		t.Fatal("state file of a dead pid must be reaped")
	}
	if _, err := os.Stat(garbage); !os.IsNotExist(err) {
		t.Fatal("unparseable state file must be reaped")
	}
	if _, err := os.Stat(other); err != nil {
		t.Fatal("non-JSON files must be left alone")
	}
}

func TestSessionID(t *testing.T) {
	t.Setenv("CLAUDE_SESSION_ID", "abc-123")
	if got := sessionID(); got != "abc-123" {
		t.Fatalf("sessionID: %q", got)
	}
	// A hostile session id must never escape the state dir.
	t.Setenv("CLAUDE_SESSION_ID", "../../etc/passwd")
	if got := sessionID(); strings.ContainsAny(got, "/\\") || strings.HasPrefix(got, ".") {
		t.Fatalf("sessionID must be filesystem-safe, got %q", got)
	}
	// Unset (and the plugin path's unexpanded placeholder) falls back to a pid-scoped id.
	t.Setenv("CLAUDE_SESSION_ID", "")
	if got := sessionID(); got != "pid-"+strconv.Itoa(os.Getpid()) {
		t.Fatalf("pid fallback: %q", got)
	}
	t.Setenv("CLAUDE_SESSION_ID", "${CLAUDE_SESSION_ID}")
	if got := sessionID(); got != "pid-"+strconv.Itoa(os.Getpid()) {
		t.Fatalf("placeholder must fall back to pid id: %q", got)
	}
}

func TestParseInjectRequest_Tolerant(t *testing.T) {
	// Campos top-level desconocidos sobreviven como context (Plexus round-trip spec).
	req, errMsg := parseInjectRequest([]byte(`{"text":"hi","source":"hub","reply_to":"http://x/inject","correlation_id":"abc","expects_reply":true}`))
	if errMsg != "" {
		t.Fatalf("unknown fields must not fail: %s", errMsg)
	}
	if req.Context["reply_to"] != "http://x/inject" || req.Context["correlation_id"] != "abc" {
		t.Fatalf("unknown fields must land in context: %v", req.Context)
	}
	if req.Context["expects_reply"] != "true" {
		t.Fatalf("non-string extras keep their JSON encoding, got %q", req.Context["expects_reply"])
	}

	// Valores no-string dentro de context ya no rompen el decode (el bug del issue #1).
	req, errMsg = parseInjectRequest([]byte(`{"text":"x","context":{"user_id":999,"tags":["a","b"],"none":null}}`))
	if errMsg != "" {
		t.Fatalf("non-string context values must not fail: %s", errMsg)
	}
	if req.Context["user_id"] != "999" || req.Context["tags"] != `["a","b"]` || req.Context["none"] != "" {
		t.Fatalf("context flattening wrong: %v", req.Context)
	}
}

func TestParseInjectRequest_Errors(t *testing.T) {
	cases := []struct {
		body string
		want string
	}{
		{`{"text":`, "invalid JSON"},
		{`{"text":123}`, `field "text" must be a string`},
		{`{"text":"x","context":"nope"}`, `field "context" must be an object`},
	}
	for _, c := range cases {
		_, errMsg := parseInjectRequest([]byte(c.body))
		if errMsg == "" || !strings.Contains(errMsg, c.want) {
			t.Errorf("parse(%s): got %q, want containing %q", c.body, errMsg, c.want)
		}
	}
}
