// mesh.ts — OpenCode plugin: register this session in the presence mesh and give it a web
// terminal, mirroring the Claude session-start hook (presence/hooks) and the Codex .codex-plugin
// hook. Every agent goes through the same two calls — `presence ttyd spawn` + `presence register`
// — only where they're wired differs; for OpenCode it's this plugin.
//
// Install: copy or symlink to ~/.config/opencode/plugins/mesh.ts (global) or to
// <project>/.opencode/plugins/mesh.ts (per project). OpenCode auto-loads it at startup.
//
// Cleanup on a hard process exit is left to `presence ttyd reap` + the registry TTL prune —
// OpenCode's `session.deleted` does not fire reliably on exit (sst/opencode#14863), so the tmux
// launcher (`mesh opencode`) is the durable teardown: when the pane dies, reap drops the terminal
// and the stale row is pruned.

export const Mesh = async ({ $, directory }) => ({
  event: async ({ event }) => {
    const sid =
      event?.properties?.info?.id ?? event?.properties?.sessionID ?? event?.sessionID;
    if (!sid) return;

    if (event.type === "session.created") {
      // Web terminal: only when running inside tmux — presence derives the tmux socket from $TMUX.
      let addr = "";
      if (globalThis.process?.env?.TMUX) {
        const tsess = (
          await $`tmux display-message -p '#S'`.quiet().nothrow().text()
        ).trim();
        if (tsess) {
          addr = (
            await $`presence ttyd spawn ${sid} ${tsess}`.quiet().nothrow().text()
          ).trim();
        }
      }
      const args = ["register", "--agent", "opencode", "--session-id", sid];
      if (addr) args.push("--attach-addr", addr);
      // cwd = the project dir so presence detects the repo the session actually serves.
      await $`presence ${args}`.cwd(directory).quiet().nothrow();
    } else if (event.type === "session.deleted") {
      await $`presence deregister --session-id ${sid}`.quiet().nothrow();
      await $`presence ttyd kill ${sid}`.quiet().nothrow();
    }
  },
});
