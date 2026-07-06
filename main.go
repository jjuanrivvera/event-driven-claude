// Command edc (Event Driven Claude) is a transport-agnostic Claude Code "channel": an MCP
// stdio server whose only job is to make ANY session injectable. It declares the
// claude/channel capability and runs a local /inject HTTP listener, so external deterministic
// tools (crons, watchers, daemons) can POST an event that arrives as a session turn.
//
// No Telegram, no outbound transport, no tools — a "fake channel". The philosophy: give any
// Claude session the ability to be woken by events instead of only by a human typing.
package main

import (
	"context"
	"os"
	"os/signal"
	"syscall"
)

var version = "0.1.0"

// Config is the inject listener's env-driven config. The channel capability itself needs
// nothing; the HTTP listener is opt-in via EDC_INJECT_PORT and fails closed without a secret.
type Config struct {
	InjectPort   string // EDC_INJECT_PORT — listener off unless set
	InjectSecret string // EDC_INJECT_SECRET — required to bind (fail closed)
	InjectBind   string // EDC_INJECT_BIND — default 127.0.0.1; set a Tailscale/LAN IP for remote emitters
}

func loadConfig() Config {
	return Config{
		InjectPort:   os.Getenv("EDC_INJECT_PORT"),
		InjectSecret: os.Getenv("EDC_INJECT_SECRET"),
		InjectBind:   envOr("EDC_INJECT_BIND", "127.0.0.1"),
	}
}

func envOr(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

func main() {
	cfg := loadConfig()
	srv := &server{out: newOut(os.Stdout), cfg: cfg}

	// SIGTERM/SIGINT just let the process exit; the listener goroutine dies with it.
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
	defer stop()
	_ = ctx

	go srv.runInject()

	// Blocks until Claude Code closes the MCP pipe (stdin EOF), then we exit.
	srv.serve(os.Stdin)
}
