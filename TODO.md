# TODO

## Adapt qrush to manage coding agents

qrush already has the primitives: daemon-hosted persistent PTY panes, sessions/
groups, a queue with states + slots + timeouts/retries, job dependencies, a
git-branch-aware status bar, and OSC 133 prompt-marker parsing (`atPrompt`). The
gap from "shell session manager" to "agent manager" is mostly **attention,
isolation, and lifecycle**.

### Priority 1 — attention column + `ru mark` hook (start here)
The smallest change with the biggest payoff; builds directly on what exists.
- [ ] Add a per-pane state: `working` / `waiting-for-input` / `idle` / `errored`.
      Store it on `TerminalPTY` (server) and surface it in the TUI job table as a
      column, sorted so "needs you" floats to the top (inbox view).
- [ ] Add `ru mark <working|waiting|done|error>` (and/or `ru notify "<msg>"`) that
      a pane process calls back into the daemon. Panes already get `QRUSH_SESSION`
      in their env, so the agent can self-report over the existing client protocol.
- [ ] Wire it to Claude Code's Stop / Notification hooks (and aider, etc.) so the
      agent reports its own state instead of qrush guessing from output.

### Priority 2 — notifications
- [ ] Fire on attention transitions (`working → waiting`, finished). Extend the
      existing `QRUSH_ONFINISH` job hook to panes. Desktop/bell notification.

### Priority 3 — worktree-per-agent isolation
- [ ] Session option to spawn in a fresh `git worktree add` on its own branch;
      show the branch (already rendered), auto-clean the worktree on session delete.
      Groups → projects, sessions → agent runs.

### Priority 4 — reuse existing mechanisms
- [ ] Slots as a concurrency/cost governor: cap N agents running at once
      (API rate limits / cost), queue the rest — same mechanism as job slots.
- [ ] Chaining via existing job dependencies: on agent finish, auto-enqueue a
      follow-up (run tests → if green, `gh pr create`).

### Detection notes
- Full-screen agents live in the alt-screen, so output-pattern detection is
  unreliable — prefer the explicit `ru mark` self-report. Fallbacks: alt-screen +
  no output for N seconds ⇒ idle/waiting; prompt marker ⇒ back at a shell prompt.
