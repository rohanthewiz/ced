# Session: Switchable chat agent backend (Phase A of the AI agent window)

Session ID: 23cb999c-065e-46f5-8677-9ea931281b6e
Date: 2026-07-29

### Ask

> "What would it take to add an ai agent window similar to our Copilot
> chat window?"

Answered as a scoping question first: the chat panel is already ~90% a
generic ACP client — Copilot is just the one agent it spawns. Proposed a
two-phase plan; the owner said *"Yes, let's start with Phase A"* and
added mid-flight: *"Still allow file and selection context."*

- **Phase A (this session)**: ONE panel, switchable backend — an agent
  registry + ≡ picker, chat-only scope unchanged.
- **Phase B (not started)**: the permission UI — surface
  `session/request_permission` options in a modal, declare fs
  capabilities, reconcile agent edits (the tree-refresh tick's
  three-way reconciliation already covers the disk side).

### What was built

**`internal/app/chatagent.go`** — the registry (`chatAgents()`):

| id | name | spawn | auth hint |
|---|---|---|---|
| `copilot` | Copilot | `copilot-language-server --acp` | phase-1 device flow (hint only when not signed in) |
| `claude` | Claude Code | `claude-code-acp` (npm `@zed-industries/claude-code-acp`) | `claude` login / `ANTHROPIC_API_KEY` (always shown on failure) |
| `gemini` | Gemini | `gemini --experimental-acp` | `gemini` login (always shown) |

Key decisions, in the order they mattered:

- **One panel, switchable backend** — the left edge is single-occupancy
  by design; a second chat strip would fight it. Switching = disconnect
  + respawn; the transcript survives (disconnect contract) with a
  "Chat agent: X" info line marking the seam.
- **`chatAgent()` maps the zero value to Copilot**, so hand-built test
  Apps (and any pre-registry code path) behave exactly as before.
- **The `"copilot"` kill switch gates only the Copilot backend**
  (`chatAgentEnabled`). Disabling Copilot tears down chat only when
  Copilot is the active backend; a Claude/Gemini chat stays up. For
  non-Copilot agents the binary on PATH is the entire opt-in.
- **The agent picker KEEPS the current agent**, annotated
  "(current — restart)" — a deliberate deviation from the model
  picker's exclude-current rule, because re-picking is the crash-retry
  gesture (clears the dead verdict) and non-Copilot backends have no
  off/on toggle to fall back on. Retry wording everywhere is the
  shared `chatAgentRetryHint`.
- **`"chatagent"` config key** (userconfig): free string, trimmed +
  lowercased, NOT validated in userconfig (the registry is app-layer
  knowledge); `chatAgentByID` falls back to the default silently — the
  stale-chatmodel rule, so a config from a newer/older ced never breaks
  startup. `SaveChatAgent` round-trips unknown keys like every SaveX.
- **Context attachments carried over for free** (the mid-flight ask):
  `copilot_chat_context.go` has zero Copilot coupling — context is
  pushed as embedded `resource` blocks, the `embeddedContext`
  capability is probed per agent in `chatInitialize`, and the
  fenced-text fallback covers agents that don't take resources.
  Pending attachments survive an agent switch (disconnect contract).

### The bug found along the way: stale connection events

`lsp.Client.Close()` ALWAYS fires `onExit` (read loop ends → callback),
so a deliberately torn-down connection still posts a `chatExitEvent` —
and a handshake in flight during teardown still posts its
`chatReadyEvent`. Before this session that meant:

- Switch agents mid-handshake → the OLD agent's ready event installs
  the OLD client under the NEW agent's name.
- Switch (or any teardown) → the old process's exit event marks the
  fresh agent dead and writes a bogus "chat stopped" line (this one was
  reachable before the feature too, via the disable toggle).

Fix: **`chatState.connSeq`**, bumped in `chatDisconnect`. The start and
turn goroutines capture the generation and stamp it into
`chatReadyEvent` / `chatExitEvent` / `chatTurnDoneEvent`; handlers drop
mismatches (a stale ready also closes its client so nothing leaks).
`session/update` didn't need it — it was already guarded by sessionID.

### Test-harness hardening

Agent switching CLEARS the dead verdict by design, which would have
punched through `newTestApp`'s `a.chat.dead = true` guard and let a
test spawn whatever `claude-code-acp` the dev machine has. New package
var `chatLookPath` (the `builtinCommandsFor` pattern); newTestApp pins
it to "never found" with cleanup restore. `chatEnsureStarted` then
re-marks dead instead of spawning.

### Geometry pins

One new menu row (Chat agent) in the Copilot group →
`TestMenuLayout_NoCustomActions` now expects 2 top-zone + 59 actions +
10 headers (71 rows), height 77, dividers `[2, 5, 74]`; the
custom-actions variant 80. Note: CLAUDE.md's pinned numbers were
already stale before this session (said 54/66/72) — both now updated.

### Files touched

- `internal/app/chatagent.go` + `chatagent_test.go` — NEW (registry,
  picker, switch, gating, seq-guard tests)
- `internal/app/copilot_chat.go` — spawn via descriptor, `connSeq`,
  agent-named strings, gate swap to `chatAgentEnabled`
- `internal/app/copilot.go` — toggle scoped to the Copilot backend
- `internal/app/app.go` — config seed, menu row
- `internal/app/app_test.go` — `chatLookPath` stub, geometry pins
- `internal/userconfig/userconfig.go` + test — `"chatagent"` key
- `CLAUDE.md` — chat contract section rewritten, architecture map row,
  pin numbers corrected

`make test` (race) green; vet + gofmt clean. Untouched by design:
ghost text / completion sidecar (separate protocol, Copilot-only) and
the chat-only permission scope (every backend still auto-declined —
that's Phase B).

### To try it

```sh
npm i -g @zed-industries/claude-code-acp
# in ced: ≡ → Copilot → Chat agent → Claude Code
```

### Loose end

A stale `r-ed` binary (pre-rebrand build, Jul 22) sits untracked in the
repo root — left uncommitted on purpose; safe to delete.
