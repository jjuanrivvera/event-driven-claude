package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
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
		Event:   "gym_missed",
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
