<!--
  File: CLAUDE.md
  Author: Rohan Allison <rohanthewiz@gmail.com>
  Created: 2026-04-29
  Copyright: 2026 Rohan Allison. All rights reserved.
  Portions copyright 2026 Cloudmanic, LLC. Original author: Spicer Matthews.
-->

# CLAUDE.md — ced

Project-specific guidance for Claude Code. Read this first; it captures
conventions and design decisions that aren't obvious from the code alone.

## What this project is

ced ("Cats Editor") is an opinionated, **mouse-first** terminal code editor aimed at
SSH-into-tmux workflows. It looks and behaves like a tiny VS Code: file
tree on the left, tabs across the top, syntax-highlighted editor in the
middle, status bar at the bottom. It ships as a single static Go binary
with no CGO.

Users open the action menu (Save, Quit, Show/Hide Sidebar, …) by clicking
the `≡` icon, right-clicking, or double-tapping `Esc`. There are
intentionally **almost no `Ctrl+` shortcuts** for editor actions — they
conflict with `tmux` and terminal emulators. Don't add more. The one
sanctioned exception is `Ctrl-D` (duplicate line): it collides with
nothing (not flow control, not the tmux/zellij prefixes), and the owner
approved it explicitly. `Alt+Up/Down` (move line) is fine — Alt never
fights tmux.

**tmux folds Esc sequences into Alt events.** tmux buffers a lone ESC
for its escape-time (500ms default), so a fast double-Esc reaches tcell
as one `\x1b\x1b` write → a single `KeyEsc + ModAlt` event, and "Esc,
s" reaches it as `Alt+s`. handleKey therefore treats Alt+Esc as the
double-Esc menu toggle and Alt+<bound rune> as that leader. Keep those
branches — removing them makes the keyboard menu and every leader
unreachable inside tmux.

**Every file action also lives in the main ≡ menu.** macOS Terminal +
tmux often swallows Button3 (right-click), so the editor cannot rely on
right-click as the only path to anything. Tree right-click is a redundant
shortcut, not a primary surface — when adding new file-management
features, make sure they're reachable from the main menu first.

## Module / repo

- Module: `github.com/rohanthewiz/ced`
- Binary name: `ced` (one word, lowercase — Makefile, goreleaser,
  brew formula all assume this)
- Brew tap: this same repo, `Formula/` directory (no separate tap repo)

## Architecture map

```
main.go                       Entry — parses optional rootDir arg
internal/app/app.go           Event loop, layout, menu modal, splitter, all rendering
internal/editor/buffer.go     Position + Buffer ([]string lines), edit primitives
internal/editor/tab.go        Tab: path, buffer, cursor, anchor, scroll, dirty state
internal/editor/highlight.go  Chroma → []tcell.Style per line
internal/editor/decoration.go Span/GutterMark overlay system merged in Tab.Render
internal/lsp/client.go        Minimal JSON-RPC-over-stdio LSP client (stdlib only)
internal/app/lsp.go           gopls lifecycle, doc sync, diagnostics, definition, hover
internal/app/copilot.go       GitHub Copilot sidecar: lifecycle + device-flow sign-in
internal/app/copilot_ghost.go Copilot phase 2: doc sync + inline completions (ghost text)
internal/app/copilot_chat.go  Copilot phase 3: ACP chat panel (left strip, streaming turns)
internal/app/chatagent.go     Chat backend registry + ≡ picker (Copilot / Claude Code / Gemini)
internal/app/copilot_chat_context.go  Chat context: file / selection attachments
internal/app/copilot_chat_perm.go     Phase 4: permission prompts + agent fs read/write
internal/lsp/acp.go           ACP framing (ndjson) + onRequest hook over the same Client
internal/editor/ghost.go      GhostText display form + the render-row splice overlay
internal/app/autosave.go      Idle-debounced auto-save (EditRev signature → autoSaveEvent)
internal/app/zipops.go        Zip file/folder — stdlib archive/zip, async zipDoneEvent
internal/app/format.go        Format-on-save bridge: project config, builtin Go, prompts
internal/app/nav.go           Back/forward file-navigation history (Esc-o/O, Alt+←/→)
internal/app/terminal.go      Embedded grsh terminal panel (REPL strip, not a PTY)
internal/format/              format.json load, trust store, builtin goimports / gopls imports / gofmt
internal/filetree/filetree.go Lazy tree, identity-preserving refresh, hit-test, render
internal/clipboard/clipboard.go OSC 52 to /dev/tty with tmux passthrough wrap
internal/userconfig/userconfig.go ~/.config/ced/config.json loader/writer (icons, autosave, termdock, execmarks, chat*)
internal/icons/icons.go       Nerd Font detection + per-file glyph mapping
internal/theme/theme.go       Tokyo Night palette + syntax color mapping
internal/version/version.go   const Version = "x.y.z" — single line, CI bumps it
```

## Conventions

### File headers
Every new source file gets the header block (file name, author, created
date, copyright year). See existing files for the exact format. Keep
copyright year matching the **current year** (2026 right now).

**Attribution on inherited files.** This codebase is a fork of Cloudmanic's
SpiceEdit. Files still carrying `Author: Spicer Matthews` are ones we
haven't substantially rewritten — leave them as they are. When a file
crosses into substantial rework (roughly: half its original lines churned,
or 200+ lines changed), flip `Author:` to the current maintainer and add
the `Portions copyright 2026 Cloudmanic, LLC. Original author: Spicer
Matthews.` line under the copyright. Files created after the fork get a
plain maintainer header with no Cloudmanic line. **`LICENSE` is never
touched** — MIT notice retention is a condition of the license, and it's
the file that actually discharges it.

### Comments
- A short doc comment above every function (public **and** private)
  explaining intent. This is a project-wide convention — don't skip it.
- Skip throwaway "what" comments inside functions; favor "why" notes
  for non-obvious decisions.

### Tests — required, not optional
**Every source file gets a corresponding `_test.go` file in the same
package.** New code without tests should not be merged. The bar:

- New exported functions: cover happy path + the obvious failure mode.
- New unexported helpers with non-trivial logic: same.
- Bug fixes: add a test that fails before the fix and passes after.
- Pure data / glue (theme palettes, single-constant files): a smoke
  test that the value is sensible is enough.

Conventions:
- One `_test.go` per source file, in the same package (NOT `_test`),
  so tests can poke unexported helpers directly. Don't split tests
  for one source file across multiple test files.
- Each `Test*` function gets a short doc comment above it explaining
  the behavior it pins down — the same "why over what" rule as
  production code. See `internal/app/fileops_test.go` for the style.
- Use `t.TempDir()` for filesystem state; never write into the repo
  or `/tmp` directly.
- For UI / drawing code that takes a `tcell.Screen`, build one with
  `tcell.NewSimulationScreen("UTF-8")` and assert against
  `scr.GetContents()`.
- Skip a test (`t.Skip`) only when the environment can't satisfy a
  hard requirement (e.g. `/dev/tty` in CI). Don't skip to dodge a
  flaky test — fix it.

Run them locally:
```sh
make test          # go test ./... with race detector
make coverage      # generates coverage.out + an HTML report
```

CI (`.github/workflows/test.yml`) runs `go test ./...` on every push
and every PR; broken tests block merges via the PR's required-checks.

### Commits
- No "Generated with Claude Code" trailers, no Co-Authored-By Claude.
- Don't ask for commit-message approval — commit directly with a good
  message when the user asks you to commit.

## Design patterns to preserve

### `cursorMoved` flag (tab.go)
The cursor only triggers `EnsureVisible` when something actually moved
the cursor. Every cursor mutator sets `t.cursorMoved = true`; `Render`
consumes the flag and clears it. **Do not** call `EnsureVisible`
unconditionally — that re-introduces the "scroll yanks back to cursor
on every tick" bug.

### Scroll clamping with overscroll
`tab.clampScroll(viewH)` allows the last line to scroll roughly to the
middle (`overscroll = max(viewH/2, 3)`). This is intentional — without
it, you can't comfortably read the bottom of a file.

### Custom tcell events for goroutine → main-loop messaging
Background work (auto-scroll during drag, 10s tree refresh) posts custom
events (`autoScrollEvent`, `treeRefreshEvent`) onto the tcell event queue
and the main loop handles them. Don't mutate UI state from goroutines
directly.

### Identity-preserving tree refresh (filetree.go)
`reload` walks the existing children, matches survivors by name, and
keeps their `*Node` pointers (and their `Expanded` state). New entries
get fresh nodes; gone entries are dropped. This is what makes the
10-second auto-refresh feel non-jarring — open folders stay open.

### Decoration layer (editor/decoration.go)
Any "paint something over the code" feature is a `DecorationSource`
producing `Span`s (range + `StyleDelta`) and `GutterMark`s — never a
new branch inside `Tab.Render`'s paint loop. External sources register
via `Tab.DecoSources`; built-ins (selection, find) run last so merge
precedence is: syntax < external annotations < selection < find. The
gutter mark column is the single cell at `x + gutterWidth`, between
the line numbers and the code.

### LSP integration (internal/lsp + app/lsp.go)
The client is a hand-rolled JSON-RPC subset — do NOT add an LSP
framework dependency. House rules it must keep obeying:

- **Silent degradation**: no gopls on PATH / crash / timeout → the
  editor works normally, no nagging. Same contract as formatters.
- **Events only**: the read loop, start handshake, debounce timers,
  and definition/hover requests all run off-loop and post
  `lsp*Event`s; only the main loop touches `App.lsp`.
- **Sync via `Tab.EditRev`**: every content mutation bumps it; the
  post-event check (`lspAfterEvent`) compares against `syncedRev`
  and arms a 300ms debounce. Saves flush pending changes BEFORE
  didSave. New Tab mutation paths must bump `EditRev` or the server
  silently diagnoses stale text.
- Diagnostics are just another `DecorationSource` (registered after
  the git source so the diag gutter dot outranks the git mark).
- Leaders: Esc-d definition, Esc-i hover. Definition jumps record
  into the app-wide navigation history (nav.go) — there is no
  LSP-private jump stack anymore.
- **Absolute paths only**: `New()` absolutizes rootDir and `openFile`
  absolutizes tab paths. A relative root produces a malformed rootUri
  and gopls then publishes diagnostics keyed by absolute paths that
  never match the tabs — the "gopls installed but no squiggles" bug.
- Tests kill the integration (`a.lsp.dead = true` in newTestApp) so
  openFile can't spawn a real gopls; LSP tests inject `fakeLSPConn`.

### Copilot sidecar (app/copilot.go) — phase 1 of the AI integration
Runs GitHub's official `copilot-language-server` (native binary, found
on PATH like gopls) over the SAME `internal/lsp` JSON-RPC client — the
transport is protocol-generic; do not add a second framing layer or an
SDK dependency. House rules:

- **Same contracts as LSP**: silent degradation (no binary → dead, no
  nagging; installing the binary is the opt-in, the `"copilot"` config
  key is the opt-out, default on), events-only (`copilot*Event`s; only
  the main loop touches `App.copilot`), no auto-restart after a crash
  (the ≡ enable/disable toggle is the deliberate retry path — enabling
  clears the `dead` verdict).
- **Auth is the device flow** via the server's custom methods: `signIn`
  returns a user code + confirm command; the confirm
  (`workspace/executeCommand`) BLOCKS until browser auth finishes,
  which is why `lsp.Client` has `CallWithTimeout` — never funnel that
  call through the 5s default. While it's pending the code stays in
  the status bar (`pendingCode`), because the modal that showed it is
  already gone.
- **Menu rows stay clickable when unavailable** — unlike the dimming
  LSP rows, `menuCopilotAuth` flashes WHY (disabled / not installed).
  Sign in is a new user's first touch; a dimmed row is a dead end.
- The handshake must send `initializationOptions.editorInfo` +
  `editorPluginInfo` or the server refuses service.
- Host side-effects (clipboard copy, browser open) go through the
  stubbable vars `copilotCopyCode` / `copilotOpenBrowser`; newTestApp
  neuters both and sets `a.copilot.dead = true` so tests never spawn a
  real sidecar. Copilot tests inject `fakeCopilotConn`.

### Copilot ghost text (app/copilot_ghost.go + editor/ghost.go) — phase 2
Inline completions painted dimmed at the caret, Tab to accept. House
rules:

- **Ghost text is NOT a DecorationSource** — decorations restyle cells
  the buffer owns; a suggestion ADDS cells. `Tab.Render` splices the
  proposal into the cursor row's runes/styles AFTER decoration merge
  (`ghostOverlay`), so the paint walk (tab stops, ScrollX, overflow
  arrows) needs zero ghost awareness. Only the first line renders
  inline; extra lines are summarised by a `⋯+N` marker — no virtual
  rows, ever (they'd ripple through scrolling and hit-testing).
- **Doc sync is lazy**: didOpen/didClose track tab lifecycle (all text
  files, not just Go — `copilotLanguageID` maps ext → languageId), but
  didChange flushes only right before a completion request. The Copilot
  server only answers questions we ask; steady sync would be traffic
  for nobody.
- **Only EditRev movement arms the 300ms debounce** (dispatch-tail
  `copilotAfterEvent`, mirrors `lspAfterEvent`) — cursor travel never
  spends a request. Responses are validated against the request's
  (path, EditRev, cursor) AND a reqSeq before painting; anything stale
  drops silently. `copilotOpenDoc` seeds `armRev` so merely opening a
  file never fires a request.
- **Accept replaces the server's range** (select + InsertString = one
  undo step) with the full InsertText — never the display form.
  Acceptance telemetry = executing the item's command; shown telemetry
  (`didShowCompletion`) echoes the RAW item JSON so correlation fields
  this client doesn't model survive. The Tab key falls through to
  plain indent when no ghost is painted.
- **Separate opt-out**: the `"suggestions"` config key (default on,
  `SaveSuggestions`, ≡ Copilot group toggle) controls ghost text
  independently of `"copilot"` — a user can keep the sidecar for
  sign-in/chat while disabling just the ghost text. Toggling off
  clears any visible ghost immediately.
- Ghost bookkeeping lives on `App.copilot` (ghostPath/Rev/Pos/Item/
  Raw); the Tab only carries the display form. Esc clears the ghost as
  a side effect (never swallowed); `copilotDisconnect` tears down the
  ghost, timer, and doc maps.

### Copilot chat panel (app/copilot_chat.go + lsp/acp.go) — phase 3
A chat panel backed by the Agent Client Protocol: the SAME
`copilot-language-server` binary run as a SECOND process in `--acp`
mode (chat and completions are separate protocols by GitHub's design).
House rules:

- **ACP rides internal/lsp, not a new transport.** ACP is the same
  JSON-RPC 2.0 envelope with two differences, both handled inside the
  existing Client: ndjson framing (`Client.ndjson`, `StartACP` /
  `NewClientACP` in lsp/acp.go) and real agent→client requests
  answered via the `onRequest` hook (runs on the read loop — post
  events, never touch App). Do not add an ACP SDK or a second framing
  package.
- **Same contracts as the sidecar**: silent degradation (no binary →
  dead), events-only (`chat*Event`s; only the main loop touches
  `App.chat`), no auto-restart (re-picking the agent under ≡ Chat
  agent is the retry path; for the Copilot backend the ≡ Copilot
  off/on toggle also works and clears BOTH dead verdicts). The
  `"copilot"` key gates only the Copilot backend (see the agent
  registry below); when Copilot is the active backend, disabling it
  tears down and closes the panel. Copilot's auth is phase 1's device
  flow (the agent reads the same credential store); a failed
  handshake writes WHY into the transcript plus a per-agent auth hint
  — unlike the sidecar, the open panel must never fail silently.
- **Switchable backend (app/chatagent.go)**: the panel is a generic
  ACP client and Copilot is just the default entry in a small agent
  registry (`chatAgents`: Copilot, Claude Code via `claude-code-acp`,
  Gemini via `gemini --experimental-acp`). ONE panel, switchable
  backend — never a second panel; the left edge is single-occupancy.
  The ≡ "Chat agent" row opens the registry as a picker; unlike the
  model picker it KEEPS the current agent, annotated "(current —
  restart)", because re-picking is the deliberate crash-retry gesture
  (it clears the dead verdict) and non-Copilot backends have no other
  one. The choice persists as `"chatagent"` in config.json; an
  unknown saved id resolves to the default silently (the stale-
  chatmodel rule). All reads go through `App.chatAgent()`, which maps
  the zero value to Copilot so hand-built test Apps behave unchanged.
  Every teardown bumps `chatState.connSeq`, and ready/exit/turn-done
  events carry the generation they were launched under — handlers
  drop mismatches, or a switch mid-handshake installs the OLD agent's
  client and the old process's exit marks the fresh agent dead. Tests
  stub `chatLookPath` (newTestApp pins it to "never found") so agent
  switching can never spawn a dev machine's real binaries. Everything
  downstream of the spawn — turns, streaming, the model roster picker,
  context attachments (embedded or fenced-text fallback) — is already
  agent-agnostic; keep it that way.
- **Docked LEFT, tree flips RIGHT** (owner preference). Every layout
  helper pivots on `treeOnRight()` (`termDockLeft || chat.open`) and
  `leftBlockW()` — don't reintroduce per-feature geometry branches.
  The left edge is SINGLE-OCCUPANCY: opening chat closes a
  left-docked terminal and vice versa (same rationale as the bottom
  strip's terminal/git exclusivity); a bottom-docked terminal
  coexists.
- **Turns**: Enter → `session/prompt` (blocks for the whole turn —
  `CallWithTimeout`, never the 5s default) while `session/update`
  notifications stream `agent_message_chunk`s into the transcript;
  chunks merge into ONE trailing agent message. ⏹ sends
  `session/cancel` (once per turn). A prompt typed mid-handshake is
  queued (`queuedPrompt`, the signInWanted pattern) — the first Enter
  must never vanish.
- **Model selection**: `session/new` returns the model roster
  (`availableModels` + `currentModelId`); the ≡ Copilot "Chat model"
  row opens it as a fuzzy picker (openPicker, current model excluded,
  premium multiplier shown) and picks go out as `session/set_model`.
  The choice persists as `"chatmodel"` in config.json and is re-applied
  during every handshake; a stale saved id is silently skipped — it
  must never break the handshake. Roster/current live on `App.chat`
  and die with the connection; `modelPref` survives. All Copilot menu
  rows (auth, chat toggle, model, suggestions, kill switch) live in
  the ≡ Copilot group — owner preference, one block.
- **Permissions + fs are real as of phase 4** (copilot_chat_perm.go):
  the handshake declares fs read/write capabilities, and
  `session/request_permission` opens the agent's own options as a
  picker (the openPicker house rule) instead of auto-declining. See
  the phase-4 section below for the house rules; the retired
  chat-only scope guard should not be reintroduced piecemeal.
- **Transcript is the model, rows are derived**: `chatRows(width)`
  re-wraps `[]chatMsg` on demand (word wrap for prose, hard wrap for
  fenced code, ❯ gutter on user prompts), so resizes re-flow for
  free. Scroll follows the termAtBottom rule. The composer is a
  single-line `textField` (Enter sends, Up/Down history, and both paste
  gestures — Cmd+V from the internal clipboard and a real terminal
  paste — land there with newlines flattened); a multi-line composer is
  a known follow-up, not an accident.
- **A focused chat panel owns the paste.** `chatPasteTarget`
  (textpaste.go) claims bracketed pastes for the composer, and
  `editorPasteTarget` returns nil while the panel has focus — the two
  predicates are mutually exclusive on purpose. Without that gate a
  paste aimed at the prompt resolved through the active tab and landed
  in the FILE behind the panel. Both gestures funnel through
  `chatInsertPaste` → `chatFlattenPaste`, so Cmd+V and a terminal paste
  can never drift apart.
- **Selection + copy live in the panel, not the terminal.** The app
  captures the mouse, so the terminal's own drag-to-select can never
  reach the transcript — the editor provides it. Selection is a
  `chatPos` pair in DERIVED-row space (wrapped row index + rune col),
  which is what the user actually drags across and what scrolling
  leaves alone; press starts the `"chatsel"` drag mode, Cmd+C lifts it
  (`chatCopySelection`), Esc drops it, and a transcript trim clears it
  (row numbers shift). Copy affordances are derived ROWS too
  (`chatRowAction`), never cells squeezed beside prose: one `⧉ copy`
  row after each agent response and one `⧉ copy conversation` row at
  the end, so hit-testing stays "which row was clicked" and geometry
  goes through the single `chatActionRect` (btnRect rule). A ⧉ button
  copies the LOGICAL message (original line breaks); a drag-selection
  copies the wrapped rows the user saw, minus any action-row labels.
  All chat copies route through `copilotCopyCode`, the stubbable var
  tests already neuter. The ≡ Copilot "Copy chat transcript" row is the
  keyboard twin of the trailing button.
- Tests inject `fakeCopilotConn` (the chat layer shares the sidecar's
  conn interface on purpose); newTestApp sets `a.chat.dead = true` so
  nothing ever spawns the real binary.

### Chat permissions + agent fs (app/copilot_chat_perm.go) — phase 4
The permission UI and the client-side filesystem that turned the panel
from chat-only into a full ACP client. House rules:

- **The transport contract is per-request goroutines.** `lsp.Client`
  runs every `onRequest` hook on its OWN goroutine (never the read
  loop) precisely so these handlers can BLOCK: `chatServe*` post an
  event carrying a buffered reply channel, wait for the main loop's
  answer, and hand it back. Only main-loop handlers touch `App` —
  don't move logic into the serve side.
- **Every permission request is answered exactly once** (the
  `answered` flag): a pick answers with the pick, dismissal (Esc /
  click outside — `openPickerWithCancel`, the palette's cancel hook)
  answers with the agent's own reject option, and teardown / turn
  cancellation answers with the cancelled outcome — the ACP-required
  response for permissions pending when a turn dies
  (`chatFlushPermissions`, called from disconnect, ⏹, and turn-done).
  The serve goroutine's `chatTurnTimeout` is the walk-away backstop.
- **Requests queue; the prompt is polite.** `chat.permQueue` holds
  arrivals in order; `chatMaybeOpenPermission` never steals the modal
  slot from an open modal or the menu, and the dispatch-tail hook
  (`chatPermAfterEvent`) resurfaces the head when the slot frees.
  Decisions are echoed into the transcript ("✓ allowed" /
  "⊘ rejected") — the agent's next answer references them.
- **Read-only chat is the coarse switch above the prompts** (config
  `"chatwrite"`, default on, `SaveChatWrite`, ≡ Copilot group toggle).
  Off means three enforcement points, not one: the handshake declares
  `fs.writeTextFile: false` (`chatInitialize` takes `allowWrite` — an
  agent that knows it can't write plans differently), `handleChatFSRequest`
  refuses writes with a readable error, and `handleChatPermRequest`
  auto-rejects any request whose tool kind is in `chatMutatingKinds`
  (edit/delete/move/execute — a shell command is a write with extra
  steps) instead of prompting. UNRECOGNISED and unlabelled kinds still
  prompt: auto-rejecting everything an agent forgot to label would make
  the mode useless rather than safe, and ced's own write path is
  refused regardless. The capability is a handshake artifact, so
  `setChatWrite` says in the transcript when a re-allow needs a restart.
- **fs is root-confined and buffer-fresh.** Reads serve the open tab's
  BUFFER (unsaved edits — the attachment rationale) before disk;
  writes land on disk, then run `refreshTreeNow()` so the normal
  three-way reconciliation absorbs the edit (clean tab reloads, dirty
  tab warns) and the transcript gets a `✎ wrote` receipt. Paths are
  confined to rootDir lexically (`chatFSResolve`); outside paths get
  an error the agent can read, not silence.

### Chat context attachments (app/copilot_chat_context.go)
What the chat panel is allowed to know about your code. Context is
**pushed, not fetched** — even now that phase 4 lets agents read files:
an attachment must reach the model in THAT turn, with no fetch round
trip and no permission prompt in the middle of the user's question, so
ced ships the bytes itself as embedded `resource` blocks in ACP's
ContentBlock array. A `resource_link` block still demotes "here is the
context" to "you could go look"; don't add one. House rules:

- **Per-turn, not sticky.** `chatSendPrompt` (the single dispatch point,
  so the queued-prompt path gets this too) resolves the attachments,
  echoes a `▤` note into the transcript, and clears the list. An ACP
  session keeps history server-side, so a sticky attachment would
  re-send the whole file on every prompt for the rest of the session —
  paid for twice (tokens and the premium multiplier) with nothing on
  screen to explain why.
- **Auto-context is a TOGGLE, not an entry** (`"chatcontext"` config
  key, default on, `SaveChatContext`). It's synthesized at send time
  from the active tab — **selection beats file**, because a highlighted
  region is a narrower question. The chip's ✕ flips the toggle (the
  only thing removing a synthesized entry can mean) and persists it
  through the same `setChatContext` the ≡ row uses, so the setting
  can't mean different things depending on which surface changed it.
- **Content comes from the open Tab's BUFFER**, falling back to disk.
  You attach what you're looking at, including unsaved edits — sending
  the stale on-disk copy of the file you just changed is the one failure
  mode that would make the agent's answers quietly wrong.
- **Attachments are visible and capped.** Chip rows sit between the
  transcript and the composer (`chatAttachRowsView` is the one source
  draw and hit-testing share, and it clamps itself so the transcript
  keeps a row); `chatVisibleRows` subtracts them. Payloads cap at
  `chatAttachMaxBytes`, cut on a line boundary, and the cut is announced
  in the note. A failed attachment is announced too — a prompt the user
  thinks carries a file must never go out pretending it did.
- **`embeddedContext` gates the wire format.** Captured from
  initialize's `agentCapabilities.promptCapabilities`; false folds the
  same text into the prompt as a labelled fenced block. It describes the
  AGENT, so it dies with the connection — pending attachments don't.
- Markers stay single-width (`▤`, not 📎): `runeLen` counts runes, and a
  double-width emoji would overrun the ✕ button.
- The ≡ Copilot group carries the keyboard/menu twins (toggle, attach
  current-or-selection, attach-file picker, clear). Attaching opens the
  panel — context you can't see is context you can't trust.

### Git panel checkboxes + Actions (app/gitpanel.go + gitpanelactions.go)
The panel's checkbox is a **multi-selection tick, not a stage toggle**.
It used to stage/unstage on click, which capped the panel at exactly one
verb; the tick now feeds the header's `Actions ▾` button, and staging is
one row in that list beside unstage, discard, delete, open, copy path,
commit, and the two select-all/clear helpers. House rules:

- **Don't put staging back on the checkbox.** Stage state is carried by
  the porcelain XY code column, drawn VERBATIM (`" M"` unstaged, `"M "`
  staged, `"MM"` both — exactly `git status -s`) with the index char
  bolded. Trimming that code collapses staged and unstaged into one
  glyph and the panel loses its only staged indicator.
- **The picker, not a dropdown**: `Actions ▾` opens `openPicker`, per
  the modal house rule that every choose-one-from-a-list UI reuses the
  palette. Rows are omitted when they'd no-op (no Unstage with an empty
  index) rather than dimmed.
- **Targets fall back to the highlighted row** when nothing is ticked
  (`gitPanelTargets`), so the first click on Actions is already useful.
  Ticks are keyed by absolute path and **pruned on every refresh** — a
  tick for a file that left the change list would silently widen the
  next bulk action.
- The header rule is still the height-drag handle, so `gitPanelPress`
  carves out `gitPanelActionsRect` and `gitPanelCloseRect` before
  starting a drag. Both rects are the single source draw and hit-test
  share (the btnRect rule).
- Writes go through `runGitCmd` (one fork for the whole set, failures in
  the info modal); Discard and Delete confirm first. The ≡ Git group's
  "Git panel actions" row is the keyboard twin of the button — the panel
  is mouse-driven, but macOS Terminal can swallow clicks.

### Navigation history (app/nav.go)
Browser-style Go back / Go forward across files (≡ menu, Esc-o / Esc-O,
Alt+Left / Alt+Right). Recording happens CENTRALLY: openFile records the
departure point on its success paths, and tabBarClick (which bypasses
openFile) records its own switches — new navigation surfaces get history
for free by calling openFile, so don't add per-surface push calls. The
`nav.suppress` flag is set while navBack/navForward retrace so the
retrace itself never records (removing that corrupts the trail into a
two-entry bounce). Any fresh navigation clears the forward stack, same
rule as a browser. LSP definition jumps record explicitly with the
request's origin position (a same-file jump moves only the cursor, which
path-change-only recording would miss) and open with suppress on.

### Menu shortcut hints
`menuItemDef.shortcut` is a display-only accelerator column rendered
right-aligned and muted in the ≡ menu ("esc s", "alt+←"). Dispatch
still lives in the leader table / handleKey — when adding or rebinding
a key, update both or the menu lies. Rows without a binding leave it
empty; drawMenu skips the hint when a long label would collide.

### Format-on-save precedence + builtin Go pass (app/format.go)
`runFormatOnSave(idx, quiet)` routes: project `format.json` entry
(trust-gated) → builtin Go pass (`format.BuiltinCommandsFor`, NO trust
prompt — the argvs are hardcoded, not repo-supplied) → global-defaults
install offer. The builtin pass is a command PIPELINE: goimports alone
if installed, else `gopls imports -w` chained with `gofmt -w` (a
machine with gopls but no goimports must not lose auto-imports), else
gofmt alone. `quiet=true` (auto-save) never opens a modal and never
flashes; an untrusted config is silently skipped until the next
explicit Save. Tests stub the app-level `builtinCommandsFor` var
(newTestApp sets it nil) so saves never exec the dev machine's Go
tools — keep that in place.

### Auto-save (app/autosave.go)
Debounce mirrors the LSP didChange pattern: `autoSaveAfterEvent` runs
after every dispatch, compares the sum of all tabs' EditRevs, and
(re)arms a 2s `time.AfterFunc` that posts `autoSaveEvent`. Saves are
silent (no flash), run format-on-save in quiet mode, defer while any
modal/menu is open, and skip tabs whose disk file changed after load
(explicit Save remains the overwrite path). The ≡ toggle persists via
`userconfig.SaveAutoSave`, which round-trips unknown JSON keys — don't
replace that with a struct marshal. Default is ON.

### Terminal panel (app/terminal.go)
An embedded grsh session (github.com/rohanthewiz/grsh — the module's
only public package; the embedding contract lives in that repo's
docs/EMBEDDING.md), hosted as a REPL strip. NOT a PTY — do not add
one, or a VT emulator; full-screen child apps (vim, htop) are out of
scope by design. House rules:

- **Two dock modes, one toggle**: the terminal is a bottom strip by
  default, or a full-height vertical strip on the LEFT (≡ → "Dock
  terminal left") — that layout also flips the file tree to the RIGHT
  edge. `App.termDockLeft` drives it; `leftBlockW`/`rightBlockW` are
  the geometry pivots every rect helper goes through. Persisted as
  `"termdock"` in config.json. Bottom mode resizes by header-rule
  drag (rows); left mode by its vertical splitter (columns). The dock
  toggle also OPENS a closed terminal — flipping the layout must never
  leave nothing where the terminal should be (that reads as the layout
  breaking, not a mode change). Keep the Show/Hide terminal and dock
  rows in the View-toggles group near the TOP of the ≡ menu — the menu
  scrolls on short windows and these rows must stay above the fold
  (pinned by `TestMenuLayout_TerminalRowsAboveTheFold`).
- **Single-occupancy bottom strip**: while BOTTOM-docked, the terminal
  and the git panel swap, never stack (opening one collapses the
  other). Two resizable bottom strips would need circular height-clamp
  math on small windows — keep the exclusivity. A LEFT-docked terminal
  doesn't compete for the bottom, so it coexists with the git panel;
  flipping back to bottom evicts the git panel.
- **Focus flag, not a modal**: `term.focused` routes plain editing
  keys to the input line; Esc stays global so leaders and the
  double-Esc menu keep working from inside the terminal. Any click
  outside the panel unfocuses. Esc-` is focus-or-toggle.
- **Coalescing writer**: grsh output lands in `termWriter`'s buffer
  with at most one `termOutputEvent` in flight — never post
  per-chunk events (heavy output would overflow tcell's queue).
- **A paste never runs anything.** `termPasteTarget` (textpaste.go)
  claims bracketed pastes for the input line, and `termInsertPaste`
  flattens them via the shared `flattenPaste`. Before that gate a paste
  arrived as raw keys, so every Enter it carried EXECUTED the line
  before it — a pasted three-line snippet ran two commands nobody
  typed. Enter is the only thing that submits; keep it that way. Do not
  "improve" the flattening into `; ` joins (that invents separators the
  user never typed, and a pasted `#` comment then swallows the rest of
  the line) and do not restore per-rune replay. A multi-line paste
  flashes its line count, because that's the case where an unreviewed
  Enter does something unintended.
- **Stop button, not Ctrl+C**: ⏹ sends Interrupt (SIGINT to the
  child's own process group), a second press escalates to Kill.
  grsh's embedded mode guarantees the signal cannot hit the editor.
- Evals run on goroutines; only main-loop handlers mutate term state.
  Each completed command calls `refreshTreeNow()` — shell commands
  create files.
- **POSIX only — this panel is why.** grsh reaches for job-control
  syscalls (`SIGTSTP`, `SIGUSR1/2`, `Setpgid`, `Getpgrp`) that Go's
  `syscall` package doesn't define on Windows, so embedding it makes the
  whole binary POSIX-only and `.goreleaser.yml` ships linux/darwin only.
  Restoring a Windows target means build-tagging this panel out behind
  stubs, NOT adding `windows` back to the goos list — that just breaks
  the release again (it broke ced's first one).
- grsh's `cd` chdirs the whole editor process (grsh's deliberate
  design) — keep ced's own file operations absolute-path based.
- **rc file, the grsh analog of ~/.zshrc**: `ensureTermSession` sources
  `~/.config/ced/rc.grsh` (`userconfig.RcPath`) into each fresh session
  via `sourceTermRc`, so a user's aliases/functions load before the first
  prompt. It embeds grsh, NOT zsh — it never reads any zsh startup file,
  which is the whole reason this file exists, and it must be grsh syntax.
  Same silent-degradation contract as the LSP/formatters: absent rc → no
  eval, broken rc → one termErr scrollback line, never a modal. Sourced
  SYNCHRONOUSLY (a real shell blocks on its rc; this also beats the race
  where a typed command could outrun an async source). `termRcPath` is a
  package var so tests point it at a temp file — newTestApp disables it
  (returns "") so the dev machine's real rc.grsh never enters `evals`.
- Tests inject `fakeTermEval` via the `newTermEvaluator` stub in
  newTestApp. Only TestTermRealGrshIntegration may execute a real
  command, and it is restricted to `echo`.

### Three-way external-change reconciliation (app.go)
On each tree-refresh tick, `reconcileOpenTabsWithDisk` checks each open
tab's mtime: clean buffer + changed file → silent reload; dirty buffer
+ changed file → warning; file deleted → set `DiskGone` once.

### Single-slot modal interface (modal.go)
Every secondary overlay (prompt, confirm, dirty-close, form, tree
context, finder) is a struct implementing the `modal` interface
(`handleKey` / `handleMouse` / `draw`) held in the single `App.modal`
slot — nil means none. `openModal` enforces mutual exclusivity. When
adding a modal: implement the interface, compute button geometry in ONE
method returning `btnRect`s that both draw and mouse hit-testing
consume, and reuse `textField` for any single-line input. For any
"choose one from a list" UI, reuse the palette as a fuzzy picker via
`a.openPicker(title, items)` (the branch switcher does this) — don't
write a new list modal. Do NOT add
per-modal fields back onto App or new branches to handleKey/handleMouse.
After any workspace mutation call `a.workspaceChanged()` — never the
individual tree/git/finder refreshes.

### Modal layout via `relY` and dynamic `labelFor`
The action menu uses named struct literals with an optional `labelFor`
hook so labels like "Show Sidebar" / "Hide Sidebar" toggle in place.
`menuLayout` recomputes every row's `relY`, the divider offsets, and
the modal height on each call — adding a menu item is just adding it
to its group in `builtinMenuGroups` (then updating the geometry pins
in `TestMenuLayout_NoCustomActions`). When the layout is taller than
the window, the modal clamps to the window and scrolls: frame + title
stay pinned, wheel / keyboard selection move the rows, ▲/▼ mark
clipped content. All scrolled geometry flows through
`menuItemIndexAt` / `menuScrollOffset` — don't hand-compute row
positions anywhere else.

**Collapsible sections.** `builtinMenuGroups` returns `[]menuGroup`
(title + `collapsible` + items), and `menuLayout` stamps a fold-header
row (`menuItemDef.header`) above every collapsible group whose action
toggles `App.menuCollapsed[title]`; a collapsed section keeps its header
but drops its item rows from the layout entirely (so they're neither
drawn, hit-tested, nor keyboard-reachable). Headers ARE selectable
(fold via keyboard), but `openMenu` deliberately skips them for the
initial highlight so a reflex Enter runs an action, not a fold. Fold
state is session-only (map on `App`, nil = all expanded, survives
close/reopen — not persisted to config). Quit is the one
non-collapsible group: it renders headerless behind a divider, because
a one-row section you could fold the exit away into reads as a bug.
Folding re-centers the (now shorter) modal — expected, same as any
resize.

**Pinned top zone + collapse-by-default.** `menuLayout` prepends two
rows OUTSIDE every group, above the first section: the **command
palette** (the menu's headline — the fuzzy gateway to every action, so
it must never hide behind a fold) and the **expand/collapse-all toggle**
(`menuToggleAllSections` / `expandAllToggleLabel`, which leaves the menu
open like a header does). A divider sets this zone off from the section
list. On first run `New` calls `seedMenuFoldDefault`, which contracts
every section (via `setAllMenuSections`) UNLESS `menuCollapsed` is
already populated — so the menu opens as a compact index of headers, not
a long scroll, and the palette/expand-all zone keeps everything one click
away. Tests build the App struct directly (not through `New`), so they
still start expanded; opt into the collapsed default with
`seedMenuFoldDefault`. Since headers and the top-zone rows are all rows,
the geometry pins count them: `TestMenuLayout_NoCustomActions` expects
2 top-zone rows + 60 group actions + 10 headers (72), height 78, dividers
`[2, 5, 75]`.

### Sidebar splitter drag
A drag is detected when a press lands at exactly `x == splitterX()`.
Min widths: `minSidebarWidth = 18`, `minEditorAfterDrag = 40`. Don't
let the editor shrink below that.

## Build / run

```sh
make run          # go run . in current dir
make build        # build to ./bin/ced
make build-linux  # cross-compile linux/amd64
make install      # go install to $GOPATH/bin
make tidy         # go mod tidy
make clean        # rm -rf bin
```

There's no `dev server` to run for this project — it's a TUI. To test
UI behavior, build and run it against a real directory.

## Releases (don't break this)

Releases are cut deliberately: push to the **`release` branch** (cut it
from main) and `.github/workflows/release.yml` runs. Ordinary pushes to
`main` no longer ship anything; `workflow_dispatch` is the manual escape
hatch.

> **⚠️ This repo is a FORK, so pushing does NOT trigger anything.**
> `rohanthewiz/ced` is a fork of `cloudmanic/spice-edit`, and GitHub
> suppresses *automatic* workflow triggers on forks until someone opens
> the repo's Actions tab and clicks **"I understand my workflows, go
> ahead and enable them."** Until that happens:
>
> - `git push origin release` cuts **no** release. Dispatch it by hand:
>   `gh workflow run release.yml --repo rohanthewiz/ced --ref release`
> - `test.yml` never runs either — CI is silent on every push to `main`,
>   so a green PR check means nothing yet. Run `make test` locally and
>   don't trust the absence of a red X.
>
> Nothing in the API surface exposes this gate: the repo reports
> `actions/permissions` → `enabled: true`, and both workflows report
> `state: active`. The only symptom is zero runs. Don't go hunting
> through permissions or the workflow YAML — check `.fork` on the repo
> first. Delete this block once Actions are enabled on the fork.

The fork is also why the repo inherited **no tags, releases, or CI
history** — v0.2.0 was the first tag it has ever had.

Once a run does start (pushed or dispatched), the workflow:

1. Reads `internal/version/version.go`.
2. **If that file was edited in the pushed commit**, the version is used
   as-is (manual major/minor bump). **Otherwise** the patch is
   auto-bumped, committed back to `release` with `[skip ci]`, and pushed.
3. Tags `v<x.y.z>`.
4. GoReleaser cross-compiles, attaches archives to a GitHub Release,
   and writes `Formula/ced.rb` back into this repo (using the
   default `GITHUB_TOKEN` — no PAT). The formula commit also carries
   `[skip ci]` to break the loop.

**Step 2 inspects the TIP commit only** (`git diff HEAD~1..HEAD`), which
makes pinning a version fragile in a non-obvious way: stack a follow-up
commit on top of your version bump and the tip no longer touches
`version.go`, so CI silently auto-bumps past the number you chose.
**Amend the version commit, don't stack onto it.** Same trap with a merge
commit — its first-parent diff drags in whatever `main` changed. And if a
run already tagged before failing, delete the remote tag
(`git push origin :refs/tags/vX.Y.Z`) before re-dispatching, or GoReleaser
releases the old tagged tree instead of your fix.

There is no site deploy step. The inherited SpiceEdit marketing site
(`website/`, spice-edit.com) went dormant with the ced rebrand — its
`pages.yml` workflow, the `CNAME` domain binding, and the Makefile
`site-*` targets are all gone. Recover them from git history if a ced
site ever gets built.

`main` is left untouched by a release run — merge `release` back into
main yourself to bring its `version.go` current.

If you're touching the workflow or `.goreleaser.yml`, make sure both
auto-commits keep their `[skip ci]` markers — without them the workflow
loops forever.

## What NOT to add

- `Ctrl+` editor shortcuts (they fight tmux/terminals — that's the
  whole reason the action menu exists).
- A config file / dotfile / plugin system. ced is opinionated.
- CGO dependencies. The whole point is one static binary.
- Tree-sitter. We use Chroma intentionally — pure Go, no setup.
- A separate `homebrew-tap` repo. The formula lives here under
  `Formula/` and that's deliberate.
