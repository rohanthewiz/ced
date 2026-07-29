# Session: Chat permission UI + agent fs (Phase B of the AI agent window)

Session ID: 1a74ea83-ff6e-47f9-8457-6a22a6aadb40
Date: 2026-07-29

### Ask

> "Pls continue with phase b"

Phase B as scoped in the previous session
(2026-0729-0234-switchable-chat-agent-backend.md): surface
`session/request_permission` in a real modal, declare fs capabilities,
and let the existing three-way reconciliation absorb agent edits. All
delivered; the panel is now a full ACP client on every backend
(Copilot / Claude Code / Gemini).

### What was built

**`internal/app/copilot_chat_perm.go`** (+ test, NEW) — phase 4 of the
chat integration:

- **Permission prompts.** `session/request_permission` opens the
  agent's own options (Allow / Always Allow / Reject…) as a picker —
  the openPicker house rule. Requests queue (`chat.permQueue`); the
  prompt never steals the modal slot from an open modal or the menu,
  and a dispatch-tail hook (`chatPermAfterEvent`, the lspAfterEvent
  pattern) resurfaces the head when the slot frees.
- **Answered exactly once** (the `answered` flag): pick → the pick;
  Esc/click-outside → the agent's own reject option (once-scoped
  preferred); teardown / ⏹ / turn-end → the cancelled outcome, which
  the ACP spec REQUIRES for permissions pending when a turn dies
  (`chatFlushPermissions`, called from chatDisconnect, chatInterrupt,
  and handleChatTurnDone). The serve goroutine's chatTurnTimeout is
  the walk-away backstop. Decisions echo into the transcript
  (`✓ allowed:` / `⊘ rejected:`) — the agent's next answer references
  them.
- **Client-side fs.** `fs/read_text_file` serves the open tab's BUFFER
  (unsaved edits — the attachment rationale) before disk, with ACP's
  1-based line/limit windowing. `fs/write_text_file` writes (creating
  parent dirs), drops a `✎ wrote <rel>` transcript receipt, and runs
  `refreshTreeNow()` so the normal reconciliation absorbs the edit:
  clean tabs reload silently, dirty tabs get the overwrite warning.
- **Root confinement.** `chatFSResolve` confines every path to rootDir
  lexically (Clean + Rel); escapes get a readable error, not silence.
  Stated in the header: aimed at keeping a confused agent inside the
  project, not defeating a hostile one.

### The two enabling changes

- **`lsp.Client.onRequest` now runs on its OWN goroutine per request**
  (was: the read loop). A permission handler legitimately blocks for
  minutes on the user while `session/update` chunks must keep
  streaming; JSON-RPC correlates by id so out-of-order replies are
  legal, and `send` was already writeMu-serialised. The serve helpers
  (`chatServePermission` / `chatServeFS`) post an event carrying a
  buffered reply channel and block until the main loop answers — only
  main-loop handlers touch App, as everywhere.
  `TestACPOnRequestDoesNotBlockReadLoop` pins the contract.
- **The palette grew a cancel hook** (`openPickerWithCancel`): Esc and
  click-outside now run an optional `cancel func(*App)` after
  dismissal. The permission picker needs it because a dismissal the
  agent never hears about blocks the turn until timeout. Ordinary
  pickers pass nil and behave exactly as before.

### Threading / lifecycle guards

- Permission and fs events carry `connSeq` + sessionId; stale ones are
  answered (rejected / errored) immediately so no serve goroutine ever
  blocks to timeout on a torn-down connection.
- Reply channels are buffered(1): a main-loop answer racing a
  timed-out serve goroutine lands in a channel nobody reads — harmless
  — and a send never blocks the main loop.
- `chat.permModal` tracks the open picker so teardown can recognise
  and close it without adding per-modal fields to App.

### Retired

`chatAutoRejectPermission` + `chatPermissionEvent` + its handler (the
phase-3 auto-decline) are gone; the reject-preference logic lives on as
the pure `chatPermRejectResult`. The handshake now declares
`fs: {readTextFile: true, writeTextFile: true}`.

### Docs

- copilot_chat.go's scope-guard header rewritten for phase 4.
- copilot_chat_context.go's "pushed, never fetched" rationale
  re-argued honestly: attachments stay pushed because they must reach
  the model in THAT turn — no fetch round trip, no permission prompt
  mid-question — not because fetching is impossible anymore.
- CLAUDE.md: new "Chat permissions + agent fs" section, updated chat
  panel bullet + attachments intro, architecture-map row.

No menu rows added, so the menu geometry pins are untouched.

### Files touched

- `internal/app/copilot_chat_perm.go` + `_test.go` — NEW
- `internal/app/copilot_chat.go` — capability flip, request routing,
  flush call sites, permQueue/permModal state, auto-decline removal
- `internal/app/app.go` — event dispatch + chatPermAfterEvent tail
- `internal/app/palette.go` — cancel hook + openPickerWithCancel
- `internal/lsp/client.go` — per-request onRequest goroutines
- `internal/lsp/acp_test.go` — async-hook test
- `internal/app/copilot_chat_test.go` — auto-reject test removed
  (superseded by TestChatPermRejectResult)
- `internal/app/copilot_chat_context.go`, `CLAUDE.md` — comments/docs

`make test` (race) green; vet + gofmt clean.

### To try it

Open the chat panel, pick an agent (≡ → Copilot → Chat agent), and ask
it to edit a file — a permission prompt opens, and an allowed edit
lands in the open tab immediately.

### Possible follow-ups (not started)

- Diff preview inside the permission prompt (the request's toolCall
  often carries the proposed content).
- Per-path or per-session "always allow" remembered client-side (today
  allow_always is the agent's memory, not ced's).
- `tool_call_update` progress lines in the transcript.

### Loose end

The stale pre-rebrand `r-ed` binary is still untracked in the repo
root — safe to delete.
