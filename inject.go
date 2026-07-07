package main

import (
	"crypto/subtle"
	"encoding/json"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"strings"
	"time"
)

// --- local event injection --------------------------------------------------------------
//
// The inject listener lets LOCAL systems (cron jobs, daemons, home automation, watchers)
// deliver an event into the Claude session as a channel turn, with meta.source "system" so
// the model can tell events from people. This is the entire point of the plugin: any session
// that loads it becomes injectable.
//
// Fails closed: without a secret it never binds, no matter what the port says. With a secret,
// the port may be explicit, or "auto"/empty for a kernel-assigned per-session port ("127.0.0.1:0")
// published through the state file (state.go) so emitters can find it. The server always
// overwrites meta.source — a caller can declare where an event came from (injected_by) but can
// never impersonate an authenticated sender.

const injectMaxBody = 64 << 10 // 64 KiB is plenty for an event; refuse anything bigger

type injectRequest struct {
	Source  string            `json:"source"`  // caller-declared origin, e.g. "CRON", "HA"
	Event   string            `json:"event"`   // optional machine-readable key, e.g. "build_failed"
	Text    string            `json:"text"`    // required human-readable event description
	Context map[string]string `json:"context"` // optional extra key/values, relayed verbatim
}

// runInject binds the local listener, publishes the state file, and serves for the life of
// the process. No-op when the feature is not configured.
func (s *server) runInject() {
	ln := s.startInject()
	if ln == nil {
		return
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/inject", s.handleInject)
	srv := &http.Server{
		Handler: mux,
		// Bounds slow/Slowloris clients on a listener that can bind a Tailscale/LAN
		// address (EDC_INJECT_BIND), not just loopback.
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       10 * time.Second,
		WriteTimeout:      10 * time.Second,
		IdleTimeout:       60 * time.Second,
	}
	log.Printf("inject: listening on %s", ln.Addr())
	if err := srv.Serve(ln); err != nil && err != http.ErrServerClosed {
		log.Printf("inject: listener stopped: %v", err)
	}
}

// startInject binds the listener and writes the per-session state file. Returns nil when the
// feature is off (no secret ⇒ fail closed) or the bind fails.
func (s *server) startInject() net.Listener {
	if s.cfg.InjectSecret == "" {
		if s.cfg.InjectPort != "" {
			log.Printf("inject: a port is configured but no secret (EDC_INJECT_SECRET); refusing to start an unauthenticated listener")
		}
		return nil
	}
	port := s.cfg.InjectPort
	if port == "" || port == "auto" {
		// One edc runs per session: an explicit port lets only the first session bind.
		// Port 0 hands each session its own kernel-assigned port instead.
		port = "0"
	}
	addr := net.JoinHostPort(s.cfg.InjectBind, port)
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		log.Printf("inject: cannot bind %s: %v", addr, err)
		return nil
	}
	boundPort := ln.Addr().(*net.TCPAddr).Port
	st := stateFile{Port: boundPort, PID: os.Getpid(), Bind: s.cfg.InjectBind}
	if path, err := writeStateFile(stateDir(), sessionID(), st); err != nil {
		// Discovery degrades (emitters that rely on the state file won't find us) but the
		// listener itself still works for anyone who knows the port.
		log.Printf("inject: cannot write state file: %v", err)
	} else {
		s.setStatePath(path)
	}
	return ln
}

func (s *server) handleInject(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if !injectAuthorized(r.Header.Get("Authorization"), s.cfg.InjectSecret) {
		log.Printf("inject: rejected unauthenticated request from %s", r.RemoteAddr)
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, injectMaxBody+1))
	if err != nil || len(body) > injectMaxBody {
		http.Error(w, "body too large or unreadable", http.StatusRequestEntityTooLarge)
		return
	}
	var req injectRequest
	if err := json.Unmarshal(body, &req); err != nil {
		http.Error(w, "invalid JSON", http.StatusBadRequest)
		return
	}
	if strings.TrimSpace(req.Text) == "" {
		http.Error(w, "text is required", http.StatusBadRequest)
		return
	}
	s.out.send(buildInjectNotification(req, time.Now().UTC()))
	log.Printf("inject: accepted source=%q event=%q bytes=%d", req.Source, req.Event, len(body))
	w.WriteHeader(http.StatusAccepted)
	_, _ = w.Write([]byte(`{"ok":true}`))
}

// injectAuthorized expects "Bearer <secret>" and compares in constant time.
func injectAuthorized(header, secret string) bool {
	const prefix = "Bearer "
	if !strings.HasPrefix(header, prefix) {
		return false
	}
	got := strings.TrimPrefix(header, prefix)
	return subtle.ConstantTimeCompare([]byte(got), []byte(secret)) == 1
}

// buildInjectNotification renders an injected event as a channel turn. meta.source is ALWAYS
// "system" — the trust boundary between machine events and authenticated human senders.
func buildInjectNotification(req injectRequest, now time.Time) any {
	meta := map[string]string{
		"source": "system",
		"ts":     now.Format(time.RFC3339),
	}
	if req.Source != "" {
		meta["injected_by"] = req.Source
	}
	if req.Event != "" {
		meta["event"] = req.Event
	}
	for k, v := range req.Context {
		// Context keys are namespaced so a caller can never shadow the reserved meta fields
		// (source, injected_by, event, ts).
		meta["ctx_"+k] = v
	}
	return notification("notifications/claude/channel", map[string]any{
		"content": req.Text,
		"meta":    meta,
	})
}
