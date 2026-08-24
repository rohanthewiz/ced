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
tmux often swallows right-click, so the editor cannot rely on
right-click as the only path to anything. Tree right-click is a redundant
shortcut, not a primary surface — when adding new file-management
features, make sure they're reachable from the main menu first.

**Right-click is `tcell.ButtonSecondary` (Button2), NOT `Button3`.** tcell
v2 reversed v1's X11 numbering: Button2 is the right button and Button3 is
the MIDDLE one. The dispatch in `handleMouse` checked Button3 for a long
time, so real right-clicks did nothing in every terminal (middle-click
opened the menu) while the "Terminal swallows it" lore took the blame.
Tests must send `ButtonSecondary` to simulate a right press — a Button3
test passes against code that never sees a right-click.

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
internal/editor/undo.go       Snapshot stack: coalescing, the byte budget, revert
internal/editor/fileio.go     Open guards, line-ending/BOM round-trip, atomic save
internal/editor/highlight.go  Chroma → []tcell.Style per line
internal/editor/syntax.go     Re-lex settle policy + the style-grid patch
internal/app/syntax.go        The settle timer that wakes the loop for the re-lex
internal/app/tabbar.go        Tab strip: scroll, overflow button, switching
internal/diff/diff.go         Patience line differ + unified-diff rendering (pure Go)
internal/app/compare.go       Compare panel: buffer ↔ file / saved copy / pasted text
internal/editor/find.go       Match model + the one scanner (options: case, whole word)
internal/editor/replace.go    Replace current / replace all — one undo step, bottom-up
internal/editor/multiedit.go  Multi-range edit of one buffer — bottom-up, ONE undo step
internal/app/find.go          The find bar: two rows, option toggles, replace buttons
internal/app/goto.go          Go to line — line, line:col, or a pasted compiler ref
internal/search/search.go     Project-wide text search over the finder's index
internal/app/projectsearch.go Find in project: search → the find-all panel
internal/editor/decoration.go Span/GutterMark overlay system merged in Tab.Render
internal/editor/multicaret.go Secondary carets + the bottom-up edit fan-out
internal/editor/wordhl.go     Word scanner, occurrence matcher, word-highlight source
internal/app/multicaret.go    Multi-caret UI: ≡ rows, Esc-m/M/*, Alt+click, status
internal/app/wordhl.go        Word-highlight ≡ toggle + per-tab flag plumbing
internal/app/findall.go       Find-all peek list: compacted rows, preview, Esc-restore
internal/lsp/client.go        Minimal JSON-RPC-over-stdio LSP client (stdlib only)
internal/lsp/workspaceedit.go WorkspaceEdit's two wire shapes → one normal form
internal/lsp/codeaction.go    Code actions: the response union + applyEdit's params
internal/app/lsp.go           gopls lifecycle, doc sync, diagnostics, definition, hover
internal/app/lspsymbols.go    Document symbols → the "go to symbol in file" picker
internal/app/lspreferences.go References → the Find-all panel's project mode
internal/app/lspsignature.go  Signature help → the hover tooltip, active param lit
internal/app/lsprename.go     Rename symbol: prompt → server edit → the primitive
internal/app/lspcodeaction.go Code actions: picker, executeCommand, server applyEdit
internal/app/hovermodal.go    Caret-anchored tooltip (hover + signature help)
internal/app/workspaceedit.go Cross-file apply: validate, write, one-gesture undo
internal/app/termdiag.go      Terminal output → clickable path:line:col jumps
internal/app/copilot.go       GitHub Copilot sidecar: lifecycle + device-flow sign-in
internal/app/copilot_ghost.go Copilot phase 2: doc sync + inline completions (ghost text)
internal/app/copilot_chat.go  Copilot phase 3: ACP chat panel (left strip, streaming turns)
internal/app/chatagent.go     Chat backend registry + ≡ picker (Copilot / Claude Code / Gemini)
internal/app/copilot_chat_context.go  Chat context: file / selection attachments
internal/app/summarize.go     AI summarize: selection-or-file, one visible chat turn
internal/gonotes/gonotes.go   GoNotes v1 REST client: env credentials, shared token cache
internal/app/gonotes.go       Capture selection/file as a GoNotes note (+ AI-drafted title)
internal/app/copilot_chat_perm.go     Phase 4: permission prompts + agent fs read/write
internal/lsp/acp.go           ACP framing (ndjson) + onRequest hook over the same Client
internal/lsp/ndjson.go        StartNDJSON — generic ndjson process launcher (ACP + MCP), env-aware
internal/mcp/config.go        mcp.json inventory (Claude-Desktop shape): stdio/http/sse entries
internal/mcp/client.go        MCP client: handshake, tools/list, tools/call, roots/list + ping
internal/app/mcp.go           MCP state, ≡ server/tool pickers, and the chat-agent declaration
internal/skills/skills.go     SKILL.md inventory: three dirs scanned, frontmatter, shadowing
internal/app/skills.go        Skills state, ≡ pickers, skill → chat attachment + directive
internal/plugins/plugins.go   plugin.json manifests: commands / hooks / decorations + validation
internal/plugins/diag.go      Compiler-style output → Diagnostic (path:line:col: sev: msg)
internal/app/plugins.go       Plugin state, ≡ group, dynamic command splice, Esc-x namespace
internal/app/plugincmd.go     Command runs: stdin modes, stdout application, the sh -c helper
internal/app/plugindeco.go    Hooks, decoration providers, edit debounce, pluginDecoSource
internal/editor/ghost.go      GhostText display form + the render-row splice overlay
internal/app/autosave.go      Idle-debounced auto-save (EditRev signature → autoSaveEvent)
internal/app/zipops.go        Zip file/folder — stdlib archive/zip, async zipDoneEvent
internal/app/format.go        Format-on-save bridge: project config, builtin Go, prompts
internal/app/nav.go           Back/forward file-navigation history (Esc-o/O, Alt+←/→)
internal/session/session.go   state.json: recent folders + per-folder tab sessions
internal/app/folder.go        Open folder (restart), recent list, session record/restore
internal/remote/remote.go     `ced --remote` / `--wait`: socket, root-based discovery
internal/app/remote.go        The listener, the wait registry, the root guard, the ≡ row
internal/app/hostident.go     OSC 7 cwd + OSC 2 title: the pane learns what you're editing
internal/cats/detect.go       cats capabilities: env sniff (free) + control-socket ping
internal/cats/client.go       Control socket: one call per connection, typed §7 verbs
internal/cats/events.go       events.subscribe stream — reconnects forever, survives restarts
internal/cats/hooks.go        Hook reporter: idle/working/blocked → cats badge/toast/push
internal/app/cats_glue.go     Tier detection, the state reporter, sibling-agent notifications
internal/app/gitcommitmsg.go  Commit the panel's selection + agent-drafted messages
internal/app/gitlog.go        Git log panel: commit list + `git show` detail (Esc-L)
internal/app/gitlogactions.go Git log verbs: cherry-pick, revert, reset, branch/tag, copies
internal/app/gitstatusreport.go git's own `git status` report, on demand, in the info modal
internal/app/terminal.go      Embedded grsh terminal panel (REPL strip, not a PTY)
internal/app/runexec.go       Run an executable: dir picker → staged line in the terminal
internal/format/              format.json load, trust store, builtin goimports / gopls imports / gofmt
internal/filetree/filetree.go Lazy tree, identity-preserving refresh, hit-test, render
internal/app/treeautofit.go   Sidebar auto-fit: width derived from the tree, locked by a drag
internal/app/scrollbar.go     Scrollbars: editor (reserved column) + tree (shared column)
internal/clipboard/clipboard.go OSC 52 to /dev/tty with tmux passthrough wrap
internal/userconfig/userconfig.go ~/.config/ced/config.json loader/writer (icons, autosave, termdock, execmarks, treeautofit, scrollbar, chat*, session, theme) + mcp.json / state.json / themes / skills dir paths
internal/icons/icons.go       Nerd Font detection + per-file glyph mapping
internal/theme/theme.go       Theme struct (tcell colors) + Default() fallback
internal/theme/palette.go     Canonical color keys + the 8-core derivation table
internal/theme/builtin.go     Spec type + the ten shipped themes
internal/theme/load.go        themes/*.json registry, shadowing, encode/save
internal/app/theme.go         Theme state, live switch, ≡ picker, save-to-preview
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

### Deferred syntax highlighting (editor/syntax.go + app/syntax.go)
`Highlight` is O(file) — it tokenises the whole buffer and allocates a
per-rune style grid. Render used to call it whenever `StyleStale` was
set, which every mutation sets, so one typed rune re-lexed the file:
70ms and 36MB of garbage per keystroke in `internal/app/app.go`. Chroma
has no incremental API, so the answer is asking less often. House rules:

- **Intra-line edits DEFER; structural edits re-lex NOW.** Typing,
  backspace, delete and a same-line selection replace patch the edited
  row's style slice and wait out `SyntaxSettle`. Enter, a multi-line
  paste, undo, line ops, comment toggle, reload and a theme switch go
  through `InvalidateStyles` and re-lex on the next render. That boundary
  is free — it's the same "structural" cut the undo grouping makes — and
  it's load-bearing: a grid whose ROWS no longer align with the buffer's
  would repaint the whole screen below the edit in the wrong colors.
- **A deferred edit must PATCH the grid** (`stylesAfterInsert` /
  `stylesAfterDelete`). Without it everything right of the caret smears
  one column for the length of the settle window. Typed runes inherit
  their left neighbour's style, so a character typed inside a string
  stays string-colored until the real lex confirms it.
- **`InvalidateStyles` is the DEFAULT contract** for any new mutation
  path; deferral is the opt-in, and only for edits that provably keep
  the rows aligned. A new mutator that just sets `StyleStale = true`
  inherits whatever `styleDefer` the last edit left — that's the bug
  this shape exists to prevent.
- The settle timer lives in the app (the editor has no loop to wake
  itself from) and is armed **only while a tab is waiting on it** — the
  caret-blink constraint.
- Over `MaxHighlightBytes` (512KB) a tab opens with `SyntaxOff` and says
  so in the status bar. At ~0.5ms/KB even one pass per pause is a freeze.
- `SyntaxSettle` is a package var **only** so tests can collapse the
  window instead of sleeping. It is not a user setting.

### Undo history and its byte budget (editor/undo.go)
Full-buffer snapshots with typing/backspace/delete coalescing, capped by
BYTES rather than by entry count. House rules:

- **The entry cap is the backstop; the byte budget is the real limit.**
  A snapshot of a 25k-line file costs 400KB in slice headers alone, so
  500 of them is ~200MB per tab and a user with eight big files open pays
  it eight times. `maxUndoBytes` (32MB, shared by BOTH stacks because
  entries move between them) is what makes the depth scale to the file.
- **`snapshotCost` charges headers plus only the lines that CHANGED.**
  Copying a `[]string` copies headers, not characters: every untouched
  line is the same string the live buffer and the neighbouring snapshots
  already hold, so charging each entry for the whole file would over-count
  by the depth of the stack and amputate a 1MB file's history for no
  reason. The header term alone would miss the opposite case — editing
  very long lines (minified JS, a data blob), where each step strands a
  multi-megabyte string nothing else references. Both terms are needed.
- **Every entry is measured against the one it will sit ON**
  (`undoTop`, or the popped entry in Undo), because those two are exactly
  one edit apart, which is the comparison the estimate is built for. The
  cost is stamped once and stored, so eviction is O(1) — re-measuring the
  bottom of the stack on every push would make it quadratic in line count.
- **Trimming never empties the stack.** "Undo the thing I just did" has
  to work even for a single step big enough to blow the whole budget (a
  multi-megabyte paste), and a history of zero is indistinguishable from
  undo being broken.
- `pushUndoEntry` is the single write path for the undo stack, and the
  running sums are maintained on every path (push, Undo, Redo, redo
  invalidation, `initUndo`). A sum that drifts up silently shrinks the
  history until the budget falsely binds.
- **The workspace-edit journal is the ONLY thing that sits above this
  stack** (app/workspaceedit.go). It never touches `undoSuppress` — it
  goes through `pushUndo(undoGroupStructural)` plus direct Buffer edits,
  the ReplaceAll route — and it validates a participant with `EditRev`
  AND `UndoDepth`, because trimUndo can shrink a stack without the buffer
  changing. `UndoDepth` is the only thing about the stack that's exported,
  and that is what it's for.
- **`Tab.ReloadAsEdit` is the second thing on the `pushUndo(structural)`
  + direct-Buffer route**, and for the same reason: it files its own
  single snapshot, which is exactly the arrangement that makes touching
  `undoSuppress` unnecessary. It is how a rewrite ced ITSELF caused
  (format-on-save, a plugin) is adopted — one step on top of the history
  rather than `Reload`'s reset of it. See the format-on-save section.

### File IO guards and round-trip (editor/fileio.go)
The two edges of a Tab's life, grouped because they're the same question
asked twice: can we faithfully round-trip this file, and did we?

- **Guards run BEFORE the read.** `MaxOpenBytes` is checked on the stat;
  a limit checked afterwards has already paid for the damage. The binary
  sniff is one NUL in the first 8KB — that catches executables, archives,
  images and UTF-16 without a content-type table, and UTF-16 is a file
  ced genuinely cannot round-trip, so refusing is right rather than
  merely convenient. Both refusals name the reason; the tree opens
  whatever gets clicked, so the message is the whole UI.
- **The buffer always holds bare LF.** `LineEnding` and `BOM` are
  detected on load and re-emitted on write, so nothing above this layer
  thinks about CRLF. Normalisation is whole-file, not per-line: a file's
  ending is a property of the FILE, and re-emitting it uniformly is what
  stops a mixed-ending file getting more mixed on every save. (Classic
  Mac CR-only is deliberately not detected — guessing wrong joins the
  whole file into one line.)
- **Saves are temp-file + rename**, and three details are load-bearing:
  the temp lives in the TARGET's directory (rename is only atomic within
  a filesystem), symlinks are resolved first (renaming onto a link
  replaces the LINK with a regular file), and mode is copied from the
  existing file (`os.WriteFile`'s perm only applies at creation, so the
  old in-place write preserved it for free). A read-only directory falls
  back to an in-place write — degrading beats refusing to save.
- Not preserved: hard links and root-owned ownership. Standard trade for
  atomicity; vim does the same.
- **External formatters still win.** gofmt/goimports always emit LF, so
  format-on-save normalises a CRLF Go file regardless of any of this.

### Tab strip scrolling and switching (app/tabbar.go)
- **`tabScroll` is DERIVED, never a preference.** `layoutTabs` re-derives
  it every frame via `ensureActiveTabVisible`, which pushes forward until
  the active tab fits and **pulls back** when closing tabs leaves dead
  space. Omitting the pull-back is invisible in any test that only opens
  files.
- **A tab that doesn't fit is not laid out at all.** `lastTabRects` is
  what hit-testing reads, so a rect past the edge would make a click land
  on a tab the user can't see. The active tab is the one exception, on a
  strip too narrow for even one.
- **`switchToTab` is the single place a switch records nav history.** The
  click path used to do it inline, so every new surface would have had to
  remember; the keyboard ones would have quietly not.
- The `+N` overflow button counts undrawn tabs and opens the switcher —
  the only mouse path to a hidden tab. One rect for draw and hit-test.
- **`Esc [` and `Esc ]` CANNOT be bound.** `\x1b[` is the CSI introducer
  and `\x1b]` is OSC, so the terminal eats the pair before the leader
  table sees it — the binding tests green and does nothing in a real
  terminal. Same trap for `P`, `N`, `\`, `^`, `_`, `#`. Tab switching is
  `Esc ,` / `Esc .` (`<` and `>` live on those keys) and `Esc b`.

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

### Multi-caret editing (editor/multicaret.go + app/multicaret.go)
Extra editing points that type, delete, and move alongside the primary
one. House rules:

- **Primary + secondaries, NOT a list of equal carets.** `Tab.Cursor` /
  `Tab.Anchor` stay exactly what they were — the caret the hardware
  cursor sits on, the one `EnsureVisible` scrolls to, the one find,
  ghost text, hover, line ops, and the status bar already read.
  Secondaries live in `Tab.Carets`, empty in the common case. Don't
  "clean this up" into one slice; every one of those features would
  need to learn which entry is special.
- **The fan-out reuses the single-caret primitives.** `applyAtCarets`
  swaps each caret into Cursor/Anchor, runs the ORIGINAL code
  (`insertRuneAt`, `backspaceAt`, …), and reads the position back —
  which is why the exported methods are thin wrappers and the cores are
  unexported. A core must never call an exported sibling: that
  re-enters the fan-out and visits every caret once per caret.
- **Bottom-up, always.** Carets are visited in descending document
  order so an edit can only move text AFTER the carets still waiting.
  Top-down invalidates every position below the first edit.
- **One undo step per burst.** The fan-out pushes one structural
  snapshot and sets `undoSuppress`; `pushUndo` returns early while it's
  set. Nothing else may set that flag — an unbalanced true silently
  disables undo. Undo/redo then DROP the carets (`applySnapshot`),
  because their positions were measured against a buffer that's gone.
- **Explicit jumps drop the carets**: `MoveCursorTo` (click, definition
  jump, nav history), `FocusCurrentMatch`, `SelectAll`, `Reload`. Arrow
  keys and Home/End do the opposite and move ALL of them — that's how a
  column gets lined up ("place carets, press End, type"). Alt+click adds
  a caret and deliberately starts no drag.
- **Secondary carets are PAINTED, not decorated** (`paintCarets`, called
  per row from Render). A caret is a zero-width position; the one at
  end-of-line has no cell for a Span to cover, and end-of-line is
  exactly where a column lands after End. Same exception shape as ghost
  text. Their SELECTIONS are decorations, though — `selectionSource`
  iterates `AllCarets`.
- **New carets are promoted to primary** (`promoteCaret`) so the
  viewport follows what the user just created, and "the primary is the
  last caret you placed" stays true. `caretGoalCol` (the widest caret's
  column) is what keeps a column from walking left across short lines —
  don't replace it with `Cursor.Col`, that's the drift bug.
- **Secondary carets blink on ced's own ticker** (`caretBlinkAfterEvent`
  arms it from the dispatch tail, `caretBlinkEvent` toggles
  `Tab.CaretsHidden`). Two constraints: it must be armed ONLY while
  carets exist — the loop is event-driven, so a standing timer would
  wake an idle editor twice a second forever — and `stopCaretBlink` must
  restore the on-phase, or disarming mid-blink strands a caret
  invisible. Not tcell's `AttrBlink`: SGR blink toggles the GLYPH, and
  an end-of-line caret paints a space.
- **Whole-line gestures collapse the set** (`dropCaretsForLineOp` in
  DuplicateLines / MoveLines / ToggleLineComment). Two of them change
  the line count or order, so surviving carets would point at the wrong
  line; fanning them out isn't the fix either (two carets on one line
  would duplicate it twice).
- Leaders: Esc-m below, Esc-M above, Esc-* next occurrence (all
  repeatable), Esc-& every occurrence (not — it has nothing left to
  add). Esc clears carets as a SIDE EFFECT (like the ghost and the
  chat highlight) — it must not consume the keystroke.

### Matching word highlight (editor/wordhl.go + app/wordhl.go)
Every other visible instance of the word under the cursor, tinted.
House rules:

- **A DecorationSource, running FIRST** among the built-ins, so
  selection and find always paint over it. Subordinate in PRECEDENCE,
  not in weight — the first cut derived `word-highlight` as a quiet 18%
  accent wash "because it's ambient", and it was invisible on an
  ordinary terminal. The key is now a NEUTRAL box (26% fg over bg) plus
  bold on the span. Neutral is load-bearing: `selection` is also an
  accent wash, so any accent tint strong enough to see read as "I
  selected that". Keep the blue fill exclusive to the selection, and
  keep the bold — a background step alone is at the mercy of the
  terminal's contrast.
- **Window-scoped by design.** Find caches its match list against a
  query; the word under the cursor changes on every caret move, so
  there's nothing to cache and the scan runs inside the frame.
  Scanning `[firstLine, lastLine]` keeps that proportional to the
  screen. The tradeoff (a match scrolled off-screen doesn't light its
  on-screen twin) is deliberate — don't "fix" it with a whole-buffer
  scan per frame.
- **Case-sensitive, whole-word from a bare cursor** (unlike find, which
  is a reading tool). `caretQuery` decides whole-word from what the
  RANGE is, not from which branch found it — a selection that exactly
  spans an identifier matches whole-word. That's what keeps Esc-* from
  widening `count` to `counter` after its first press turns the word
  into a selection.
- **Quiet unless it has something to say**: a lone on-screen match, a
  caret in punctuation, a one-rune selection, and multi-caret mode all
  produce nothing.
- `MatchOccurrences` and `WordRange` are shared with multicaret.go —
  one scanner, two consumers. `Tab.WordHighlight` gates it per tab
  because sources are asked per-tab; `App.wordHLEnabled` is the
  authoritative copy and `applyWordHighlight` the single write path.

### Find verbs — options, replace, go to line (editor/find.go,
### editor/replace.go, app/find.go, app/goto.go)
The bar grew from "type and jump" into the three verbs a search surface
owes you. House rules:

- **ONE scanner decides what a hit is** (`matchCols` in editor/find.go).
  `FindAllOpts` (whole buffer, optionally case-folded), `FindAll` (the
  zero-options entry point project search and the seeding ladder use),
  and `MatchOccurrences` (the word highlighter's line window) all come
  through it. Two implementations would drift, and the user would have
  no way to tell which one answered. Case folding is per-RUNE
  (`foldRunes`), not `strings.ToLower`: a few runes lowercase to more
  than one rune, and a fold that changes the rune count shifts every
  column reported after it, painting the highlight over the wrong cells.
- **Options live on the App, are PUSHED to the tab, and are not
  persisted.** `App.findCase` / `findWord` are authoritative because
  they describe how the USER searches — flipping "match case" then
  switching tabs must not silently switch it back — and
  `applyFindOptions` is the single write path (the applyWordHighlight
  shape). `Tab.SetFindOptions` re-runs the query, so a match list can
  never outlive the options that produced it. No config key, unlike the
  Find-all dock: a saved "match case" would silently narrow the first
  search of every future session, with no bar on screen to explain the
  hit that didn't appear.
- **The bar is 1 row for find, 2 for replace, and everything pinned
  above the status bar asks `findBarRows()`** — never the
  `findBarHeight` constant, which is now just the per-row unit. A
  replace row that floated over the editor would cover the line it is
  about to rewrite (the Find-all displacement argument).
- **Both inputs are the shared `textField`** (the house rule for every
  single-line input). It already knows caret motion, scroll, click-to-
  position and paste; the hand-rolled copy that used to live here is
  what this replaced.
- **Alt is safe INSIDE the bar** and nowhere else in the editor body:
  the bar owns the keyboard, so handleKey's Alt+rune leader branch never
  sees `alt+c` / `alt+w` / `alt+a` — including in tmux, where "Esc c"
  arrives folded as Alt+c. That's what makes in-bar chords possible at
  all when the ≡ menu is unreachable from a keyboard-owning surface. The
  two toggles ALSO get ≡ rows for the same reason the Find-all dock
  does, and clickable `Aa` / `|W|` buttons because clicks come first here.
- **A replace is ONE undo step, and replace-all goes bottom-up.**
  `ReplaceCurrent` selects the match and calls `InsertString` — that path
  already records exactly one structural step. `ReplaceAll` files one
  snapshot itself and then edits the **Buffer** directly: Buffer
  primitives record no history, which is how it stays one step WITHOUT
  touching `undoSuppress` (that flag belongs to the caret fan-out alone —
  an unbalanced `true` silently disables undo). Descending document
  order is load-bearing: a replacement of a different width shifts every
  later column on the line, so a top-down pass corrupts the tail.
- `ReplaceAll` **re-scans** rather than trusting `FindMatches`, and
  `ReplaceCurrent` advances PAST what it wrote, so `s/a/aa/` terminates.
- **Go to line clamps, and parses a pasted compiler reference**
  (`app.go:314:22` → line 314, col 22). The number usually comes from a
  build log that may be a few edits stale; landing on the last line beats
  a modal that says no. Leader is `Esc j` — not `l`, which is one stray
  Esc away from every word with an L in it (the same argument that put
  the git Log on a shifted `L`). Replace is `Esc e`: `r` is redo, and
  pairing a MUTATING verb with find under the shift convention would say
  the wrong thing about what pressing it does.

### Find all in file (app/findall.go)
Every occurrence of a query listed as one compacted row each — line
number, then the line with the hit lit — under the editor. Esc-F, the ≡
Search group, or ↓ from the find bar. House rules:

- **It's a PEEK, which is why it isn't a palette picker.** The house
  rule is that every choose-one-from-a-list UI reuses `openPicker`, and
  this is the one documented exception. The palette's grammar is
  pick-run-close with a no-op dismissal; here moving the highlight moves
  the editor's cursor LIVE and Esc puts it back, a click PREVIEWS
  instead of dismissing, and a row is two columns (number ┃ code) rather
  than a label. Three different contracts, not a skin. Don't "unify"
  them — the picker would have to grow a preview hook, a cancel that
  undoes, and a two-column row, at which point it isn't the palette.
- **It takes rows OUT of the editor band, never floats over it**
  (`editorBandRows` → `editorRect` → `findAllPanelHeight`). A popup that
  covered the line it was previewing would defeat the feature. Height is
  FIXED, so unlike the resizable bottom panels it needs no clamp
  negotiation with them — it just displaces, and `findAllMinEditorRows`
  is the floor it never eats through. `editorBandRows` exists because
  the popup (like every panel) must ask "what would the editor have
  left?" — a question `editorRect` can't answer, since it already
  subtracts them.
- **Two docks, one displacement rule.** TOP (default) takes rows off the
  top — pinned under the tab bar, editor pushed down: the list is what
  you're reading and the code is the reference under it. RIGHT takes
  COLUMNS off the editor's right edge and runs full height, which trades
  line length for showing three times as many hits. So `editorRect`
  returns `y = 1 + findAllPanelHeight()` and `w = editorBandCols() -
  findAllPanelWidth()`, with exactly one of the two ever non-zero.
  Everything that positions itself inside the editor already reads that
  x/y — hit-testing, drag auto-scroll, the hover modal, Alt+click — so
  nothing else needed to learn about it. Keep it that way: **no call
  site may assume the editor starts at row 1 or runs to the right edge
  of its band.** Width precedence in the right dock follows
  `gitPanelHeight`: the editor's reserve caps the column, but the
  column's own floor is applied last and wins on a band too narrow for
  both — a list too narrow to read is worse than a narrow editor.
- **The dock is a persisted preference with THREE surfaces**
  (`"findalldock"`, default top): the ◨/⬒ button in the popup's title
  row, `d` inside the popup, and the ≡ View row. The `d` key is not
  redundant — a modal owns the keyboard, so the ≡ menu is unreachable
  from inside the list, which would leave a mouse-only path on a
  terminal that eats clicks (the macOS-Terminal rule). The glyph names
  the layout it switches TO, and both halves are single-width per the
  marker rule. A flip re-runs `preview` because the band it centered
  against just changed.
- **A preview CENTERS an off-screen hit** (`Tab.CenterOnCursor`), rather
  than just scrolling it into view: EnsureVisible's minimal scroll parks
  it on the last row — every line before it, none after — which is
  useless when the question is "what is this line doing?". Like
  RestoreView it must CLEAR `cursorMoved`, or the next Render
  minimally-scrolls the line straight back to the edge. A hit ALREADY on
  screen is left exactly where it is (`Tab.CursorLineVisible`), so
  walking a cluster of nearby hits holds the view still and only falling
  out of the band re-centers. The primitive stays unconditional — that
  policy belongs to the caller, not to the Tab.
- **Esc restores through `Tab.RestoreView`, not `MoveCursorTo`.** Every
  other cursor write sets `cursorMoved` so the next Render scrolls the
  cursor into view; a restore wants the opposite, because the captured
  SCROLL is part of what's being put back. Without it, a user who
  wheeled away from their own cursor before peeking gets a viewport
  yanked to the cursor instead of their view. Everything else that
  dismisses (Enter, double-click, a click outside) ACCEPTS — Esc is the
  one gesture that means "put it back".
- **The tab's find state is BORROWED, and returned on both exits.** The
  popup sets `SetFindQuery` so the editor tints every occurrence while
  the list explains them, and `FindIndex` tracks the highlighted row so
  the previewed hit paints as the current match for free. The tint has
  to leave with the list — same contract as closing the find bar.
- **Rows are compacted at open, once.** Leading indentation comes off
  and interior tabs render as ONE space, which keeps the display text
  aligned rune-for-rune with the buffer so a match column maps to a
  display column by subtracting the trim. No width table, nothing to
  drift. Two hits on one line are two rows — the list is occurrences,
  not lines.
- **The filter box is SEEDED with the query and the seed is INERT**
  (`findAllModal.seed`, `filterNeedle`). It opens holding the search
  expression, caret at the end, so `/` plus a keystroke carries the same
  question on instead of restating it — but while the box still holds
  exactly the seed it narrows nothing, because a row's display text is
  compacted (the bullet above) and a query carrying a tab, or one that
  matched inside the indentation, is not literally present in its own
  results. Filtering by it would open the list empty on the very search
  that filled it. One edit hands filtering back to the ordinary contains
  rule. Seeding belongs to producers whose query IS text the rows carry
  (in-file search, project search, references); the workspace-edit
  RECEIPT leaves it empty, since its query is a label ("Rename foo →
  bar") that would narrow to nothing the moment it was touched.
- **ONLY A SELECTION SEARCHES SILENTLY** (`findAllSelectionQuery`);
  everything else asks. A highlighted single-line region is the user
  pointing at the exact text, so a prompt there could only be answered
  "yes, that". The find bar's leftovers and the word the cursor happens
  to sit in are IMPLICATIONS, and a result list is indistinguishable from
  a correct answer to the wrong query — so those go in the prompt as a
  PRE-FILL (`findAllPromptSeed`: bar first, then the word), where Enter
  accepts the guess and any other key replaces it. The old ladder ran the
  guess. A multi-line selection is not a search term (FindAll matches
  within a line) and falls through to the prompt like every other unclear
  case. The seed must be read BEFORE `openPrompt` — `openModal` →
  `closeAllModals` wipes the find bar that may be seeding it. The bar's
  own ↓ gesture (`openFindAllFromBar`) is untouched: the user just typed
  that, so it is not a guess. No match flashes rather than opening an
  empty box.

### Find in project (internal/search + app/projectsearch.go)
The same panel, a second scope: every occurrence across the tree, rows
carrying a path. Extending the list beat building one — it already had
the two-column row, the displacing strip, the right dock, scrolling, the
Esc contract and the mouse story. House rules:

- **Pure Go, not ripgrep.** Neither rg nor grep is on every machine and
  the promise is one static binary that works when it lands. Cost is
  bounded three other ways: the file list is the FINDER'S index (so the
  project's own gitignore rules already excluded node_modules, vendor and
  build output), files too big or binary are skipped, and results are
  capped — **with the cap reported in the title**, because a silently
  short list reads as "that's all of them", the one wrong answer a search
  can give.
- **One matcher.** Matching delegates to `editor.FindAll`, so an in-file
  search and a project search can never disagree about what a hit is. A
  second implementation would drift. Row compaction is shared with
  `compactLine` for the same reason.
- **Project mode does NOT preview.** Walking rows in the in-file list
  moves the cursor live; across files that would mean opening a file per
  keystroke — firing the LSP's didOpen, Copilot's didOpen, every plugin's
  open hook and a syntax pass, and leaving a tab behind for every row
  merely scrolled past. The row IS the preview (it carries the whole line
  with the hit lit); Enter or a double-click opens. Esc therefore
  restores nothing, and `restoreFind` returns early — project mode never
  borrowed the tab's find state and writing it would clobber what the
  active tab legitimately holds. A preview mode, if ever wanted, needs a
  real preview-TAB concept (one reusable slot), not a special case here.
- **Labels truncate from the FRONT.** The distinguishing part of a path
  is its tail; twenty rows reading `internal/app/…` say nothing. The
  column caps at a share of the panel, not a constant — the two docks
  differ by a factor of three in width.
- Results arrive as a generation-stamped event and are dropped if stale
  or if a modal/menu took the slot meanwhile. Seeding reuses the in-file
  rule exactly — a single-line selection runs, anything else prompts
  pre-filled: the two features are one question at two scopes, and
  seeding them differently would be a trap. It matters more here, if
  anything, since a guessed query spends a whole-tree walk before the
  user can correct it. Leader is `Esc P`, the shifted twin of `Esc p`
  (names vs. contents).

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
- The handshake also declares `workspace.workspaceEdit` with
  `documentChanges: true` and an EMPTY `resourceOperations`, which is how
  a server learns ced can apply text edits but cannot create, rename or
  delete files on its behalf — see the workspace-edit section.
- Leaders: Esc-d definition, Esc-i hover, Esc-I signature help, Esc-D
  the file's symbol outline, Esc-R references, Esc-c code actions, Esc-E
  rename. The ≡ Code group also carries the multi-file undo row, which
  has no leader (see that section). Definition jumps record into the
  app-wide navigation history (nav.go) — there is no LSP-private jump
  stack.
- **The handshake's `onRequest` hook is NARROW, and that is enforced by a
  sentinel.** ced's gopls connection is the only LSP client here that
  answers server→client REQUESTS (workspace/applyEdit — see code
  actions), and a hook that owned the whole surface would inherit
  `workspace/configuration`, which gopls BLOCKS on while type-checking.
  `lsp.ErrRequestUnhandled` is how a hook declines one method and leaves
  the built-in auto-responder in charge of the rest. `StartWithRequests`
  is a separate constructor because the hook must be installed BEFORE the
  read loop starts (the NewClientACP rule) — assigning it afterwards
  races the goroutine that reads it.
- **Absolute paths only**: `New()` absolutizes rootDir and `openFile`
  absolutizes tab paths. A relative root produces a malformed rootUri
  and gopls then publishes diagnostics keyed by absolute paths that
  never match the tabs — the "gopls installed but no squiggles" bug.
- Tests kill the integration (`a.lsp.dead = true` in newTestApp) so
  openFile can't spawn a real gopls; LSP tests inject `fakeLSPConn`.

### Go to symbol in file (lsp/types.go + app/lspsymbols.go)
The active document's declarations, listed in the palette, jump on
Enter (Esc-D, ≡ Code). House rules:

- **The protocol's two answers collapse in `internal/lsp`, not in the
  app.** `documentSymbol` returns either the hierarchical
  `DocumentSymbol[]` or the legacy flat `SymbolInformation[]`;
  `ParseDocumentSymbols` normalises both into one document-ordered
  `[]Symbol` carrying a Depth. It tells them apart by a **"location"
  key, not by a failed unmarshal** — both shapes decode cleanly into
  either struct, so an error-based sniff would "succeed" with every
  position at 0:0 and every row jumping to line 1. The jump target is
  `selectionRange` (the NAME); `range` is the whole declaration, and
  landing on a 200-line function's opening brace is what this avoids.
- **It's a PICKER, not a palette source.** palette.go's doc comment
  floats symbols as a merge source, and that's the one place it
  shouldn't go: sources are collected synchronously at open, and this
  costs a round trip to gopls. Feeding it in would either block the
  palette on a cold server or make its contents arrive late and
  reorder under the user's fingers.
- **The kind word goes LAST in a row label.** The fuzzy scorer rewards
  early matches, so a leading "function " would make every row score
  alike on the first letters typed. Trailing, "func" still narrows by
  kind while the name keeps the position that ranks. Indentation is
  two spaces per Depth, pure decoration that survives filtering — it's
  what makes an unfiltered list read as the file's outline.
- Same contracts as the other two verbs: flush before asking (an
  unsynced new function missing from the list reads as the feature
  being broken), drop a response whose document is no longer active,
  re-check the path when a row FIRES (a picker owns the keyboard, not
  the world), record nav explicitly (a same-file jump is invisible to
  openFile's path-change recording), and center an off-screen landing
  per goToLine's policy.
- Leader is **Esc-D**, the shifted twin of definition's Esc-d: same
  verb, wider scope — 'd' goes to the definition of what's under the
  cursor, 'D' lists every definition in the file. The f/F, p/P, h/H
  convention.

### Find references (lsp/client.go + app/lspreferences.go)
Every use of the symbol under the cursor, listed in the Find-all panel's
PROJECT mode (Esc-R, ≡ Code). House rules:

- **It reuses the panel; it does not build one.** findall.go's project
  mode already answers every question a cross-file result list raises —
  path-carrying rows, a strip that displaces the editor, the right dock,
  scrolling, the Esc contract, the mouse story, the truncation notice. A
  second list would drift from this one exactly where a user would
  notice. The ONLY thing a non-search producer may change is
  `findAllModal.heading` (the title verb); if a second thing ever needs
  changing, that's the signal the two really are different features.
- **The result OPENS A PANEL, so it is generation-checked**
  (`lsp.refSeq`) — unlike definition and hover, which are content with a
  path check because they only move a cursor or pop a modal. Same guards
  as project search besides: an empty answer flashes rather than opening
  a blank list, and a result landing while a modal or the menu owns the
  screen reports its count instead of stealing the slot.
- **Context text is fetched off-loop and reconciled ON it.** A Location
  carries no text, so `collectRefLines` (on the request goroutine) reads
  each file ONCE however many hits it holds, and `referenceHits` (main
  loop) then prefers an OPEN TAB's buffer line over the disk copy. That
  order is load-bearing: gopls answered from the text ced synced to it,
  so rendering those columns against a stale on-disk line would light up
  the wrong cells of the wrong text. Which is also why columns stay in
  UTF-16 across the hop and convert once, against the text that will
  actually be drawn (`refEndOfLine` is the multi-line-range sentinel
  that survives it).
- **Sorted before capped**, so the truncation is a prefix of what the
  user sees, in search.Project's order so the two lists read alike. An
  unreadable file costs its rows their CONTEXT, not their existence —
  the location is the answer, the line is decoration.
- **The word under the cursor is resolved first and a miss refuses.**
  It isn't just the title: the panel tints that word in whatever file a
  row opens. `includeDeclaration` is always true — a "who uses this?"
  list that omits the thing being used reads as a hole.
- `referencesTimeout` is 30s, not the client's 5s default: this is the
  one verb whose cost scales with the PROJECT rather than the file.
- Leader is **Esc-R** — the letter References' own name offers once `r`
  is spoken for by redo (the rEplace argument). Deliberately not a
  shifted twin of redo: redo's twin is `Z`, beside its own `z` alias.

### Signature help (lsp/types.go + app/lspsignature.go)
The parameter list of the call the cursor is inside, with the parameter
you're typing lit (Esc-I, ≡ Code). House rules:

- **IT IS MANUAL, AND THAT IS STRUCTURAL.** Every other editor pops this
  automatically on `(` and `,`; ced cannot, because a modal here OWNS
  THE KEYBOARD (the single-slot modal interface) — a tooltip that
  appeared while you typed would swallow the very next keystroke it
  exists to help you write. The live version of this feature needs a
  NON-modal overlay, and ced already has exactly one of those: ghost
  text (editor/ghost.go). If signature help ever goes live-while-typing,
  that is the layer it goes through. Don't bolt an auto-trigger onto
  this one.
- **The emphasis IS the feature.** Without it this is hover on the
  enclosing function, which the user could already get by moving the
  cursor. So `hoverModal` grew one field (`emph []hoverEmph`) — the
  findAllModal.heading arrangement one floor down: two verbs, one
  tooltip, because the geometry, the trigger-happy dismissal and the
  "this is a glance" framing are identical.
- **The label is HARD-wrapped, the prose WORD-wrapped** — the chat
  transcript's code/text split, and here it is load-bearing twice: a
  signature is code, so collapsing its whitespace misrepresents it, and
  only an exact rune-per-column mapping lets an offset become a
  (row, col) by division. A word wrapper drops and merges spaces, so
  every offset past the first break would be a guess. A parameter
  straddling a break gets one emphasis run per row.
- **The protocol's optionals collapse in `internal/lsp`, not the app**
  (the ParseDocumentSymbols rule). `activeSignature`/`activeParameter`
  are POINTERS on the wire because absent must be distinguishable from
  zero — and zero is the first signature and the first parameter, the
  common answer. A signature's own `activeParameter` overrides the
  help's (spec precedence). `paramRange` resolves the label's two shapes,
  offsets FIRST because they're exact; the string form is a substring
  search that can land on the wrong occurrence in `f(int, int)`, which
  costs a few columns of misplaced emphasis, not a wrong tooltip.
- **A cursor outside a call is a real answer**, so nil-with-nil-error
  reads as "nothing here" and the flash says "No signature help **here**"
  — the usual cause is the position, not the server, and a bare "no
  signature help" reads as the latter. (gopls also declines inside a
  string literal argument; that's its call, and the message is right for
  it too.)
- The active parameter's OWN documentation precedes the signature's: it
  answers the question that made the user press the key, and the tooltip
  is capped, so it must not be what gets cut. `firstParagraph` trims a Go
  doc comment to its opening statement rather than truncating
  mid-sentence.
- Leader is **Esc-I**, a true shifted twin of hover's Esc-i: same
  tooltip, same glance, one question over — 'i' describes the symbol
  under the cursor, 'I' describes the call the cursor stands inside.

### Workspace edits — the multi-file primitive
### (lsp/workspaceedit.go, editor/multiedit.go, app/workspaceedit.go)
Applying a server-authored `WorkspaceEdit` — text edits spanning files the
user may never have opened — as ONE gesture they can undo with one press.
It is a PRIMITIVE, not a verb: code actions and rename both reduce to
about thirty lines on top of it, which is the argument for building it
properly rather than special-casing rename. House rules:

- **It opens NO TABS, and the Find-all panel is the receipt.** A rename
  can touch a dozen files; opening each would fire didOpen for the LSP and
  Copilot, every plugin's open hook and a syntax pass, then leave a DIRTY
  tab behind — a dozen modal round-trips at quit, most not even laid out
  on the strip. That is exactly the cost find-in-project refuses to pay.
  Files with no tab are loaded into a DETACHED `editor.Tab`, edited, and
  written through `Tab.Save` (so the open guards, the BOM and the line
  ending all round-trip); files with a tab are edited in their BUFFER.
  Visibility comes from `reportWorkspaceEdit`, which lists every applied
  edit in the Find-all panel's project mode — the `heading` field is still
  the only thing a non-search producer may change.
- **The open buffer outranks disk, always.** The server answered from the
  text ced synced to it, which for an open tab includes unsaved edits.
  Writing that file's disk copy behind a dirty buffer would apply
  coordinates to text they were never measured against — `referenceHits`'
  rule, and here it is the difference between a rename and a corruption.
  **Open participants are NOT saved**: that would also commit whatever
  else the user had unsaved and bypass format-on-save's prompts. Auto-save
  takes them two seconds later, and the flash names the asymmetry.
- **Validate everything, THEN apply.** A half-applied rename does not
  compile and the file that failed is the one nobody notices, so
  `planWorkspaceEdit` does every read, guard and conversion while writing
  nothing, and one refusal kills the whole edit naming the file and the
  reason. Order inside the apply is the rest of the story: buffer edits
  first (pure memory, cannot fail), then disk writes in path order, and a
  failed write rolls back through the same per-tab snapshots — which is
  what those retained detached Tabs are for. A rollback that itself fails
  leaves the journal ARMED rather than clearing it.
- **The undo journal is ONE SLOT sitting above the per-tab stacks**, the
  only thing in this editor that does. Validity is `EditRev` **and**
  `UndoDepth` per participant: EditRev alone can't say the snapshot is
  still on top (`trimUndo` evicts from the BOTTOM, shrinking a stack
  without the buffer changing), and depth alone can't either (a push plus
  an eviction nets to zero). A detached file also checks its mtime — if
  somebody else wrote it since, rewriting would discard their change.
- **Plain undo CLAIMS the group from a participant tab**, and degrades
  LOUDLY when it can't. A reflex Esc-u would otherwise roll back one file
  of a rename and leave the rest, silently. When a participant has moved,
  `menuUndo` says which file broke it, undoes just this tab, and CLEARS
  the slot — falling through beats refusing (an undo that does nothing
  reads as broken), announcing beats silence, and clearing is what stops a
  later press half-applying the rest. `closeTab` drops the journal too: a
  closed tab takes its undo stack with it.
- **The protocol's two shapes collapse in `internal/lsp`** (the
  ParseDocumentSymbols rule). `documentChanges` wins over `changes` — it
  is the shape carrying versions and resource ops, so preferring it never
  loses information. Sniffing is on a FIELD (`kind`, then `textDocument`),
  never a failed unmarshal: a `CreateFile` decodes cleanly as a
  `TextDocumentEdit` with everything zeroed, so an error-based sniff would
  turn "create this file" into a document with no edits. `changes` is a
  MAP, so its documents are sorted by path — Go randomises map order, and
  an unsorted walk would give two runs of one rename two different orders.
  `documentChanges` is an array whose order the spec makes meaningful, so
  it is preserved.
- **Resource ops are declined at the capability and refused BY NAME at the
  parse.** `Initialize` declares `workspaceEdit.resourceOperations: []`,
  so a conforming server refuses a package rename ITSELF with its own
  reason, before anything is applied. `ParseWorkspaceEdit` still parses
  them, and the app refuses the whole edit naming what it saw — applying
  the text edits while dropping the file move would rewrite every
  identifier and leave a tree that no longer builds.
- **Confinement runs AFTER `EvalSymlinks`, on both sides.**
  `writeFileAtomic` resolves symlinks before writing, so a lexical check
  alone is escapable — a link inside the root pointing out of it would
  pass and then be written through. The ROOT is resolved too, or every
  file in a project under `/tmp` reads as outside its own root on macOS.
  `resolveInRoot` is the one implementation (chatFSResolve delegates to
  it); containment itself is gitstatus.go's `pathInside`.
- **`ClampEnd`, not `Clamp`, for an exclusive range end.** Clamp pins the
  line to the last line and THEN the column to that line's length, so
  `{LineCount, 0}` — how the protocol spells "the whole document" — would
  spare the final line's text. **Overlapping edits refuse**: applied
  bottom-up they don't fail, they produce plausible garbage.
- **`ApplyMultiEdit` is the per-tab half, and `ReplaceAll` was refactored
  ONTO it** — that pass was this codebase's first multi-range edit, and
  two copies of "edit a set of ranges as one step" would drift. One
  `pushUndo(undoGroupStructural)` up front then direct Buffer edits, which
  is how it stays one step WITHOUT touching `undoSuppress` (that flag is
  the caret fan-out's alone). Bottom-up, always. `EditResults` derives
  where each edit LANDED analytically rather than recording it during the
  pass, because the pass runs backwards and every recorded position would
  need fixing up as earlier edits arrived.
- **Planning reads files ON THE MAIN LOOP**, deliberately. Which files are
  open, which buffers are dirty and which revisions are synced is
  main-loop-only state, and splitting the read from the validation across
  the loop boundary would re-open the staleness window this exists to
  close. Bounded: tens of small files, once per deliberate gesture. The
  escape hatch if it ever hurts is reading into a map on the request
  goroutine and re-stat'ing on the loop.
- Staleness has three comparisons, each against the thing that produced
  the coordinates: the origin tab's `EditRev` vs. what `captureWSRequest`
  recorded, every open participant's `EditRev`/`syncedRev` pair (which
  catches a keystroke plus a fired debounce that would leave the first
  test true again), and the server's own `textDocument.version` against
  ced's didChange counter when it makes one.
- **`applyServerEdit` reports ACCEPTANCE; `applyServerEditWith` reports
  the OUTCOME**, and the gap between them is the confirmation prompt. The
  callback exists for exactly one caller — a server-initiated
  `workspace/applyEdit` is a REQUEST whose response field is literally
  `applied`, so it is the one edit here ced must report on rather than
  merely perform. Every other verb asks a question and is told the
  answer; "accepted" is all those need. `done` fires EXACTLY ONCE on
  every path (refusal, no-confirm commit, and whichever of the
  confirmation's two hooks runs — which is what `confirmModal.cancelHook`
  is for), because a caller is blocked on it. This was the one addition
  the primitive needed when code actions were built on it, and it is the
  shape a third verb should reach for rather than a new one.
- No leader key — the flat table is out of mnemonic letters and plain undo
  covers the common case. The ≡ **Code** row (`wsEditUndoLabel`, dynamic)
  is the path for the two cases plain undo can't serve: the active tab
  isn't a participant, or every touched file went straight to disk.

### Rename symbol (lsp/client.go + app/lsprename.go)
The first verb built on the workspace-edit primitive, and the proof that
building the primitive first was right: prompt, ask, hand over. Esc-E, ≡
Code. House rules:

- **It owns almost nothing.** Everything a cross-file rewrite raises —
  which files to open (none), which text the coordinates were measured
  against, rollback, the one-gesture undo, the receipt — belongs to
  `applyServerEdit`. What this file owes on top is the LABEL
  (`renameLabel`, one spelling), because it becomes the confirmation
  title, the flash, the ≡ undo row and the receipt heading, and four
  hand-built strings would drift. A future verb that needs more than a
  label from the primitive is the signal something is wrong with the
  primitive, not with the verb.
- **THE PROMPT SITS BETWEEN THE ASK AND THE REQUEST**, which is why
  `startRename` is split out of `menuRenameSymbol`. The modal owns the
  keyboard, so the user can't type into the buffer — but the LSP debounce,
  auto-save, a chat agent's write and the disk reconciliation are all
  still running. So the position is captured when the prompt OPENS,
  re-verified against `EditRev` when it SUBMITS, and `captureWSRequest` is
  taken at submit time AFTER the flush, so the contract describes the text
  the server is about to answer from rather than what was on screen when
  the user reached for the key. Capturing before the flush records the
  pre-sync revision and makes the plan refuse its own request.
- **Generation-checked, because this one WRITES FILES.** Stronger than the
  references rule (that answer only opens a panel) and stronger than
  definition/hover's path check: a superseded rename's edit planned against
  a buffer the newer one already rewrote corrupts it plausibly.
- **The old name never goes on the wire** — the POSITION is the symbol's
  identity, which is exactly what separates this from a textual
  replace-all. `cursorWord` is a UI affordance only: it seeds the prompt so
  a small correction is a keystroke, and it refuses a cursor on nothing
  BEFORE the user is asked to think of a name for it.
- **Two client-side refusals, deliberately narrow**: an unchanged name
  (the round trip is guaranteed to come back with "nothing to change",
  which is a confusing way to say "you didn't change it") and whitespace
  (no language ced will speak allows it). Everything else — a keyword, a
  leading digit, a collision — is the SERVER's judgment, and its message
  names the actual rule. Don't grow this list into an identifier validator.
- **No `prepareSupport`.** That option buys a round trip to validate a
  position and hand back a placeholder ced already has, and gopls refuses
  an illegal rename with a better message than a prepare step's silence.
  `renameTimeout` is 30s for references' reason and then some — a rename
  IS a project-wide reference search plus the edit built from it.
- Leader is **Esc-E**, the shifted twin of rEplace's `Esc e` under the
  f/F, p/P, d/D convention: same verb, wider and smarter scope — 'e'
  replaces text you name in this file by matching characters, 'E' replaces
  a SYMBOL the compiler names, everywhere it is bound. It is not 'n'
  (new file) and deliberately not 'N': `\x1bN` is SS2, one of the ESC
  pairs a terminal can eat before tcell sees it. The ≡ row sits directly
  above the multi-file undo row — the one write in a group of reads, next
  to the thing that takes it back.

### Code actions (lsp/codeaction.go + app/lspcodeaction.go)
What the server can do to the cursor or the selection — fix this error,
organize these imports, extract that block — listed in the palette
(Esc-c, ≡ Code). It is the second verb on the workspace-edit primitive
and the one that was queued to PROVE it: rename asks a question and
applies the answer, while this arrives by two routes, one of which isn't
a response at all. House rules:

- **THE SECOND ROUTE IS A SERVER REQUEST**, and everything unusual here
  follows from it. An action carrying no edit of its own is run through
  `workspace/executeCommand`, and what it CHANGES comes back unprompted
  as `workspace/applyEdit` — with a JSON-RPC id waiting on a field called
  `applied`. Hence `applyServerEditWith` (see the primitive), hence the
  narrow `onRequest` hook plus `lsp.ErrRequestUnhandled` (see the LSP
  section), and hence a `wsRequest` that is EMPTY: nobody asked a
  question, so there is no origin revision to claim. The per-participant
  sync checks still run and are the ones that matter.
- **An unprompted edit REFUSES while a dialog owns the screen.**
  `openModal` replaces rather than refuses, so applying under a prompt
  the user is mid-answer on would pop a confirmation over it and silently
  drop that modal's own pending reply. Unlike a chat permission request —
  where an agent is stuck, so the prompt queues — a server can simply be
  told no, with a reason, and the user re-runs the action.
- **The handler BLOCKS the serving goroutine** — the ACP
  permission-request shape, for the same reason: the answer is a decision
  the main loop makes, possibly with a confirmation dialog in the middle
  of it. `wsApplyTimeout` (90s) is deliberately SHORTER than the client's
  `executeCommandTimeout` (2 min), so a user who walks away releases the
  server rather than the other way round. That budget is the longest in
  the client because THE USER IS INSIDE IT: the server is blocked in
  executeCommand while ced's confirmation sits on screen.
- **The range is the SELECTION when there is one, the cursor otherwise**,
  and that distinction is the whole interface. "Extract to function" only
  exists for a span, so a verb that always asked about a point would
  silently never offer half of what the server has; a quick fix only
  exists where a diagnostic is, so a verb that always asked about a
  selection would need one before it could offer anything. The ≡ row's
  label is dynamic and says which span it will cover.
- **Diagnostics are echoed back VERBATIM**, which is how a quick fix
  finds the problem it fixes. `lsp.Diagnostic` round-trips its raw JSON
  (`UnmarshalJSON`/`MarshalJSON`) because the fields doing the matching —
  `data`, `code`, server-private extensions — are exactly the ones this
  client has no reason to model. Same argument the Copilot layer makes
  for echoing a completion item's raw JSON, and the same reason a
  command's `Arguments` stay `json.RawMessage`. Overlap is generous: a
  zero-width cursor range must match a diagnostic that CONTAINS it.
- **The response union collapses in `internal/lsp`** (the
  ParseDocumentSymbols rule), and the discriminator is the JSON TYPE of
  the `command` field — a string on a bare Command, an object on a
  CodeAction literal. Never a failed unmarshal: a bare Command decodes
  cleanly as a CodeAction with everything zeroed, so an error-based sniff
  would turn "run this command" into a row that does nothing at all.
- **No `resolveSupport`, the `prepareSupport` trade.** Declaring it tells
  the server to send actions with NO edit and wait for a
  `codeAction/resolve` round trip; not declaring it makes the server
  compute edits up front, so a picked row applies immediately. The cost
  is work the server does for actions nobody picks, which is its own
  cheapest work. `codeActionLiteralSupport` IS declared, or a server
  sends only bare Commands the editor could execute blind.
- **DISABLED actions are dropped, not dimmed.** The surface is the fuzzy
  picker, in which every row is a verb that runs; a row answering Enter
  with "you can't do that here" is worse than one never offered, and the
  palette has no disabled state to borrow. An action with neither an edit
  nor a command is dropped for the same reason.
- **Edit before command**, per the spec, and a REFUSED edit skips the
  command — running the follow-up to something that never happened is the
  one way this verb could do half a thing silently.
- Generation-checked (`actionSeq`) for rename's reason — a picked row
  WRITES FILES — plus the symbols verb's path check. The picker sits
  between the response and the apply the way rename's prompt sits between
  the ask and the request, so the contract is captured at ask time (after
  the flush) and validated when a row FIRES.
- Leader is **Esc-c**, the letter the verb's own name offers and the last
  obvious one free in the flat table. It collides with the AI
  namespace's `Esc a c`, which is what a namespace is for. Not VS Code's
  `Ctrl-.` in any form: `.` is next-tab and Ctrl is out by the project's
  founding rule.

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
- **MCP servers are declared, not connected** (mcp.go): `session/new`
  carries ced's configured inventory in `mcpServers`, and the agent
  spawns its own copies. It's agent-agnostic — see the MCP section.
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
  `chatInsertPaste` → `flattenPaste`, so Cmd+V and a terminal paste can
  never drift apart. The composer flattens a paste WHOLE, unlike the
  terminal, which runs it line by line — deliberate, not an
  inconsistency: a break in a prompt implies no "submit", so flattening
  loses nothing and keeps the text editable before it's sent.
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

### Summarize with AI (app/summarize.go)
The AI namespace's one READING verb: what does the selected text — or
the whole file, when nothing is selected — actually say. Esc-a-z, ≡
Copilot. House rules:

- **It owns almost nothing, and that is the point.** What text goes out
  is a `chatAttach`, so the payload comes from the open BUFFER including
  unsaved edits, is capped with the cut announced, and takes whichever
  wire shape the agent advertised. Where the answer goes is the chat
  panel as a normal visible turn (the gitPanelSuggestCommit rule) —
  **not a modal**: a summary is prose of unknown length the user will
  read beside the code, scroll, copy and ask a follow-up about, which is
  a transcript, not a dialog. Whether the agent can answer is
  `chatUnavailableReason`, surfaced rather than dimmed away. Nothing
  here claims the answer afterwards; what is left is the target rule and
  the prompt.
- **`selectionOrFileTarget` is the SHARED target resolver** — selection
  beats file, the same narrower-question-wins rule `chatAutoAttachment`
  follows — and both verbs built on it (this one, the GoNotes capture)
  go through it, so "the current text" can never mean two things. The
  verb is a parameter only because the REFUSAL has to name it: "open a
  saved file" alone reads as a complaint about the editor.
- **`chatAttachOnce`, not `chatAddAttachment`.** The latter is the
  user's own attach gesture, so it flashes and opens the panel; here the
  attachment is machinery serving a verb the user named, and the
  duplicate case is the COMMON one — with auto-context on (the shipped
  default) the active file is already synthesized for every turn, so a
  flash saying so would be noise on the default configuration.
- **The prompt asks for prose, unlike the commit-message prompt.** That
  answer had to land in a single-line field, so over-specifying was
  cheaper than parsing what came back; this one lands in a transcript
  that word-wraps prose and hard-wraps fenced code. The only real
  constraints are LENGTH (the panel is a narrow strip) and that the
  answer DESCRIBE the text rather than review it — "what does this do"
  is the question, and a list of suggested improvements is a different
  one the user can now ask as a follow-up, in the panel already open in
  front of them.
- Agent-agnostic, like the chat toggle beside it. The ≡ row is in the
  Copilot group because that group IS the editor's AI block, and its
  label names what it will cover — a selection changes the question
  completely, and that has to be visible before a click spends a turn.
  Leader is **Esc-a-z**: summariZe, the letter the word offers once 's'
  is skills and 'm' is the model picker.

### GoNotes capture (internal/gonotes + app/gonotes.go)
The selected text — or the whole file — saved as a new note in the
user's running GoNotes server, with the title typed or agent-drafted.
Esc-a-n, ≡ Notes. House rules:

- **THE SELECTION IS THE BODY, VERBATIM.** Nothing is prepended to it: a
  note is a document the user will open and edit later, and a header ced
  invented is the first thing they would have to delete. Provenance goes
  in the note's DESCRIPTION (`ced: <project>/<path>:<lines>`), which is
  where GoNotes shows a subtitle and where it stays out of the text. The
  project name is part of it — a bare path means nothing in a notes
  database fed by a dozen repositories. Every capture is tagged `ced` so
  the set is findable.
- **The text comes from the BUFFER** (`attachContent`, shared with the
  chat attachments — `chatAttachContent` is now the 64KB wrapper over
  it). You capture what you are looking at, unsaved edits included;
  saving the stale on-disk copy of a file the user just changed is the
  one failure here nobody would notice until much later. The note cap is
  far higher than the chat one because the cost model is different: this
  text goes to a database, not into a prompt turn, so it pays no tokens.
- **HTTP, not the database.** GoNotes' bytdb files are SINGLE-WRITER —
  a second writer is not a race to be careful about, it is a file that
  won't open — so the server that owns them is the only safe path to
  them. Same conclusion GoNotes' own TUI reached for its cats-hosted
  mode. Stdlib only, no SDK, no CGO.
- **Every knob is an ENVIRONMENT VARIABLE, and they are GoNotes' own**
  (`GONOTES_URL`, `GONOTES_USER` / `GONOTES_SYNC_USERNAME`,
  `GONOTES_PASSWORD` / `GONOTES_SYNC_PASSWORD_B64`,
  `GONOTES_TOKEN_FILE`). A ced config key would be a second place to say
  the same thing and a second place for it to be wrong, and it would put
  a credential in a file ced writes. The JWT cache is the SHARED
  `~/.gonotes/.api_token`, so a user who signed in through the GoNotes
  TUI is already signed in here; writing it back is best-effort, 0600
  under a 0700 parent.
- **One login, one retry, never a loop.** The cached token is tried
  first (the common case costs no extra round trip) and only a 401
  spends a login. A wrong password re-sent forever is worse than one
  visible failure. No credentials at all is `ErrNoCredentials`, which
  names the variables rather than repeating "unauthorized".
- **AVAILABILITY IS DISCOVERED BY TRYING.** Nothing probes at startup
  and the ≡ row never dims on the server's state: GoNotes is a separate
  process the user starts and stops, so a row that vanished whenever it
  was restarted would be wrong exactly when the user asked. A failure
  therefore opens the INFO MODAL rather than flashing — those messages
  carry the address dialed and the server's own words, and "connection
  refused" is only actionable once you can read which URL refused.
  Success is a flash; nothing is lost either way, since the text is
  still in the buffer.
- **Nothing saves without an Enter, and the agent is a REASON, not a
  gate.** The ✦ button sits on the prompt whatever state the agent is in
  and says why when it can't ask (`commitDraftBlockedReason`, the
  menuCopilotAuth rule); a drafted title only ever pre-fills the field.
  The suggestion is a normal visible chat turn claimed by generation +
  transcript mark (`noteTitleReq`) — the same staleness discipline as
  the commit draft, and it yields the modal slot to a pending permission
  prompt for the same reason.
- **The `[private: off]` chip is on EVERY note prompt**, unlike the
  commit prompt's trailer chip, which appears only on a draft: privacy
  is a statement about the TEXT being captured, equally true whoever
  wrote its title. It is per-invocation (no persisted key) and travels
  with `noteTitleReq`, so a re-draft doesn't re-arm what the user just
  switched off. `alt+p` is its chord, `alt+a` the ✦ button's, both named
  in the hint — a modal owns the keyboard, so the ≡ menu is unreachable
  from inside a prompt.
- **`agentOneLine` is the shared reduction** behind `commitSubject` and
  the note title: a single-line input field is about to receive whatever
  the agent felt like writing, and a fence or a "Title:" prefix would be
  saved verbatim. Its own ≡ group ("Notes") for the reason MCP, Skills
  and Plugins each have one — a separate system ced talks to, not a
  feature of any subsystem here. `gonotesCreate` is a package var;
  newTestApp pins it at a refusal so no test run can write fixture text
  into a developer's real notes database.

### MCP servers (internal/mcp + app/mcp.go)
Model Context Protocol support. **One inventory, two consumers** — hold
onto that and the rest follows:

1. **The chat agent.** Enabled servers are declared in ACP's
   `session/new` `mcpServers` array (`mcpDeclarations`, called from
   `chatInitialize`), and the AGENT spawns its own copies. This is the
   path that makes MCP useful day to day and it needs no connection from
   ced at all. It is agent-agnostic — not a Copilot feature.
2. **ced itself**, so a user can verify a server works, read its tool
   list, and run one by hand from ≡ → MCP.

House rules:

- **The inventory is `~/.config/ced/mcp.json`, in the ecosystem's shape**
  (`{"mcpServers": {name: {command, args, env, …}}}`, VS Code's
  `"servers"` accepted too) so a user pastes the block they already have.
  A separate file from config.json for the actions.json reason: flat
  toggles the ≡ writes back vs. a nested inventory the user hand-edits,
  and a syntax error in one must not disable the other. `userconfig`
  owns only the PATH (`MCPPath`); the parser lives in `internal/mcp`.
- **Nothing spawns at startup.** `New` reads the inventory; ced connects
  only on a deliberate ≡ action. "I declared a server" must never mean
  "the editor launched three node processes while I wasn't looking".
  Silent degradation is PER SERVER: one that won't start gets a `✕` row
  with the reason, no modal, and no auto-restart (Reconnect is the retry
  gesture, same as every other integration).
- **Same transport, not a new one.** MCP stdio framing IS ndjson, so it
  rides `lsp.StartNDJSON` (ndjson.go — `StartACP` is now an alias of it).
  Do NOT add an MCP SDK or a second framing layer. The handshake is
  three steps, not two: `initialize`, then the
  `notifications/initialized` NOTIFICATION. ced answers `ping` and
  `roots/list` (that's how a server scopes itself to the project) and
  honestly declines `sampling`/`elicitation` — they want an LLM and a
  prompt surface this client hasn't got.
- **ced's own client is stdio-only.** http/sse entries still parse and
  are declared to the agent (gated on the agent's advertised
  `mcpCapabilities`; an unsupported one is dropped and NAMED in the
  transcript note, because some agents reject the whole `session/new`
  over one unreachable entry). ced's picker offers such a server only
  "Server info", which says why.
- **Generation-checked, events-only**, like the chat layer: every
  connection carries a seq, teardown bumps it, and a ready/result event
  from an older generation is dropped — and a stale ready event CLOSES
  the client it carried, or a disconnect leaks the process it disowned.
- Surfaces are all palette pickers (the house rule): servers → per-server
  actions → tools → a JSON-arguments prompt pre-filled from the tool's
  schema (`mcpArgSkeleton`; `"{}"` means "just run it"). Bad JSON flashes
  the parse error and hands the text BACK. Results open in the info
  modal, which does not scroll — hence the capped preview plus the
  full text kept on `mcp.lastResult` for the "Copy last result" row.
- `Describe()` shows env KEYS, never values: picker rows end up in
  screenshots and bug reports.

### Agent skills (internal/skills + app/skills.go)
The folders of markdown instructions the coding-agent ecosystem keeps in
`~/.claude/skills` and `<project>/.claude/skills`. ced reads those
directories AS THEY ARE (plus `~/.config/ced/skills` for skills written
for ced itself) — nobody should have to duplicate a folder to use it
here. House rules:

- **A skill is PUSHED, and only on purpose.** ced is not a model; it
  can't read a description and decide a skill applies. So there is no
  auto-selection and no skill index riding along on every prompt — the
  user picks one from ≡ → Skills, it attaches, and it goes out with the
  next message. That also keeps the cost visible: the chip is on screen
  before Enter.
- **Attachment, not a new mechanism.** A skill IS a `chatAttach`
  (copilot_chat_context.go) carrying a `skill`/`skillDir` pair: same
  chips, same ✕, same per-turn consumption, same size cap, same
  embedded-resource / fenced-block wire formats. The additions are the
  label (`skill: <name>` — a personal skill's path is outside the
  project, so `relativePathFor` would render `../../` noise) and
  `chatSkillDirective`, which leads the TEXT block because that is the
  one part of the payload the agent reads in BOTH wire shapes. It says
  the markdown is a procedure to follow and names the skill's DIRECTORY:
  ced ships only the SKILL.md (a skill folder can run to megabytes), so
  naming the directory is what keeps its scripts and references reachable
  by an agent that has fs access.
- **Agent-agnostic**, like MCP: whatever backend the panel is running
  gets the skill. Not a Copilot feature, hence its own ≡ group.
- **Nothing is executed.** A skill is markdown ced hands to an agent,
  never a script ced runs. That's what keeps these directories on the
  right side of the no-plugin-system line — same as themes being data.
- **Precedence is ced < user < project, shadowing by name IN PLACE of a
  second row** (the theme registry's rule): a checked-in skill overriding
  a personal one is the whole reason to scan a project directory, and two
  rows with one name in a picker is a bug report.
- Frontmatter is parsed by hand (`parseFrontmatter`) — quoted scalars,
  block scalars, wrapped continuations, nested structures skipped. Do NOT
  add a YAML dependency for two string keys. A file with no frontmatter
  still loads, named by its directory; only an unreadable file is an
  error, and it costs that one skill.
- Both pickers rescan first, so a skill written moments ago in ced is
  already there — the theme feature's save-to-preview loop, minus the
  save hook. `skillsUserDirFn` / `skillsCedDirFn` are package vars;
  newTestApp pins them at temp dirs so no test reads the developer's real
  skills.
- Leader: **Esc-a-s**, in the AI namespace with the rest of the chat
  surface. (It was briefly a top-level `Esc S` — the flat table having
  nothing better left is what argued for the namespace.)

### Declarative plugins (internal/plugins + app/plugin*.go)
`~/.config/ced/plugins/<name>/plugin.json` — shell commands the user
already had permission to type, bound to menu rows, leader keys, editor
events, and a decoration overlay. **It is actions.json one octave up**,
which is the frame to keep: actions.json answered "give me a menu row
that shells out", and this answers the three things that row could never
do — put output back in the buffer, run without being clicked, and paint
over the code. Prompt collection is imported wholesale from
`customactions` (same schema, same form modal, same env-var contract) so
there is one dialect, not two. House rules:

- **A plugin is DATA, and ced never becomes a host.** Nothing is loaded,
  compiled, or interpreted; a manifest can only say WHEN to run a
  command and WHERE its stdout goes. That is what keeps `plugins/` on
  the themes-and-skills side of the What-NOT-to-add line rather than
  making it the plugin system that entry rules out. The test of a
  proposed field is whether it still reduces to "a command line plus a
  place to put its output" — `input`/`output`/`on`/`glob` do; a callback
  into editor internals would not.
- **Nothing runs at startup.** `New` reads manifests; the first command
  fires on a file open, a save, or a deliberate pick. The MCP promise,
  for the same reason — "I wrote a manifest" must never mean "the editor
  ran three commands while I wasn't looking". The `"plugins"` config key
  (default on, `SavePlugins`, ≡ toggle) is the kill switch, and it is
  honoured at EVERY surface — menu, palette, leader, hooks, and the
  decoration source — not just at load. Off means the marks leave the
  screen too.
- **stdout is the answer, stderr is the complaint**, captured separately
  and never merged on the command path. A formatter that prints a
  deprecation warning to stderr must not have it spliced into the user's
  source — which is exactly what one `CombinedOutput` here would do. The
  DECORATION path deliberately reads both, because `go vet` and half the
  Go toolchain report findings on stderr, and it ignores exit status
  because a linter exits non-zero precisely when it has something to say.
- **Nothing is written back over a buffer that moved.** Every run
  captures (path, EditRev) and the range it may overwrite BEFORE the
  goroutine starts; a mismatch discards the output with a flash. Same
  staleness discipline as ghost text and the chat results, and for the
  same reason: by the time the command exits, the tab, the selection and
  the cursor may all be somewhere else. A replace is ONE undo step
  (`InsertString` over a selection already records exactly one), and a
  whole-file replace captures and restores the view through
  `RestoreView` — which is the right primitive because it does NOT set
  `cursorMoved`.
- **Decorations are a DecorationSource, keyed by (file, provider)**, so
  a re-run replaces its own marks and nobody else's, and an EMPTY result
  still replaces — that's how findings disappear when the user fixes
  them. Precedence is **git < plugin < LSP**: a plugin mark outranks the
  ambient git change bar because the user installed it deliberately, and
  loses to gopls because a compile error is the more urgent thing to say
  in the one gutter cell. Its glyph is `◆`, deliberately not the LSP's
  `●` — when both have something to say, telling them apart is the point.
- **The output format is the compiler/grep convention**
  (`path:line:col: severity: message`), parsed by hand in diag.go. That
  choice is the whole reason decorations are worth having: a useful
  provider is a one-liner the user already knows how to write (`grep -n
  TODO`, `go vet`, `shellcheck -f gcc`, `eslint -f unix`). A format ced
  invented would have made the feature theoretical. Unparseable lines
  are DROPPED, never reported — real tools interleave summaries and
  progress with their findings. Findings naming a DIFFERENT file are
  dropped too: the decoration layer is strictly per-file.
- **Esc-x is the plugin namespace, and the codebase's only DYNAMIC
  prefix** (`leaderBinding.subFor` / `hintFor`, resolved on every arm
  because a table baked at startup goes stale the moment the user hits
  Reload). It clears the second-namespace bar from the opposite
  direction to Esc-a: that one existed because a fixed surface outgrew
  the flat table, this one because plugin keys are UNBOUNDED and belong
  to the user — every letter they took would be one ced could no longer
  bind, and any letter ced later bound would silently break somebody's
  plugin. An EMPTY namespace arms nothing (it would otherwise swallow
  the next keystroke on the overwhelmingly common plugin-free machine).
  Leader collisions are first-declared-wins over the name-sorted
  inventory, and the loser is named in the ≡ Plugins report.
- **The edit event is debounced at 800ms and armed only while something
  listens** — longer than the LSP's 300ms because this spawns a PROCESS,
  and gated because a standing timer in an event-driven loop wakes an
  idle editor forever (the caret-blink constraint).
- **Degradation is per plugin** (the theme registry's rule): one broken
  manifest names itself in the ≡ label and costs that plugin only.
  Startup is silent — load errors are HELD on the state, not flashed,
  because a startup flash scrolls past before anyone looks.
- **The inventory is USER-scoped, deliberately.** There is no
  `<project>/.ced/plugins`: a checked-out repo that could run shell on
  open would be a supply-chain hole, and the honest version of that
  feature is format.json's trust store (`format.LoadTrust` /
  `CheckTrust`, hash-pinned per project). If project plugins are ever
  added, they go through that gate — not on their own.
- `pluginsDirFn` / `pluginConfigPathFn` / `pluginShell` are package vars;
  newTestApp pins the first two at temp dirs and the third at a stub that
  refuses, so no test can read the developer's real plugins or execute
  one. This matters more here than anywhere else in the harness, because
  a plugin IS an arbitrary shell command and open/save fire hooks by
  themselves. `TestRunPluginShell_RealShell` is the one exception and is
  restricted to `cat`/`echo`.

### Compare panel (internal/diff + app/compare.go)
A unified diff of the file you're editing against another file, its own
saved copy, or text you just pasted — the fourth occupant of the bottom
strip. House rules:

- **The active buffer is the NEW side, always.** Not cosmetic: it makes
  the `+` lines the ones that exist in the open file, so `diffTargetLine`
  — written for the git panel — maps a display row straight to a line in
  the tab and the double-click jump costs nothing. It also reads the way
  the question is asked ("what have I got that the saved copy hasn't?").
- **Pure Go (internal/diff), not `git diff --no-index`.** The sources
  here are BUFFERS, so shelling out would mean temp files, and neither
  git nor the repository it wants is guaranteed to be there. Same
  argument the project search made against ripgrep — and it's why
  Compare is its own ≡ group rather than rows under Git: none of it
  needs a repo.
- **The differ is patience, and that's a correctness choice as much as a
  performance one.** Anchoring on lines that appear exactly ONCE on each
  side is what stops "a new function was added" from rendering as "every
  closing brace moved". A full LCS table is the fallback INSIDE small
  unanchorable regions only (`lcsCellBudget`); past that the region is
  reported as a wholesale replace, because an n·m table on two 20k-line
  files is 400M cells. The common-suffix peel is COUNTED, not collected —
  prepending each line made a one-line-edit-at-the-top diff quadratic.
- **`SplitLines` does not invent a trailing empty line.** A file ending
  in `\n` has as many lines as it has newlines; counting a phantom one
  would report an edit on every buffer-vs-file comparison.
- **Both sides come from the BUFFER when the file is open** — you compare
  what you're looking at, including unsaved edits (the chat-attachment
  rule). The one exception is a file compared with ITSELF, which only
  means anything against the saved copy: that side reads from disk and
  the label says `(saved)`, since "t.txt ↔ t.txt" would look like a bug.
- **Pasted text is a first-class source** because ced cannot READ the
  system clipboard (OSC 52 is write-only, and that's correct for an
  SSH-first editor). "Compare with pasted text" ARMS the panel — visibly,
  with the instruction in the body, because a mode you can't see is a
  mode nobody knows they're in — and `comparePasteTarget` then outranks
  the editor, chat and terminal for the next bracketed paste. It can only
  be armed deliberately, which is what makes outranking them safe. Cmd+V
  feeds it from the internal clipboard; Esc disarms it as a side effect,
  like clearing the ghost.
- **⟳ re-reads the file side, and re-diffs a pasted one.** A diff is a
  snapshot and both sides move; `compare.oldPath` is kept for that rather
  than reconstructing a path from `oldLabel`, which is prose (it carries
  "(saved)") and wouldn't survive a path outside the project root.
- Guards mirror fileio's open guards — size checked on the STAT, one NUL
  in the first 8KB is binary — because a file ced won't open has no lines
  worth diffing either.
- Single occupancy with the git panels and a bottom-docked terminal, both
  directions, via `growBottomPanel`/`shrinkBottomPanel` and each opener
  closing the others. No leader key: the three verbs are ≡ / palette rows
  (the flat table is out of mnemonic letters, and this isn't a namespace's
  worth of surface).

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

### Commit rows + agent-drafted messages (app/gitcommitmsg.go)
The panel's Actions list can commit the ticked files themselves, and ask
the CURRENT chat agent (whatever backend is active — this is not a
Copilot feature) to draft the message for exactly those files. House
rules:

- **A commit of the selection stages first.** `gitCommitFiles` runs
  `add --` then `commit -m … --` through `runGitCmdSeq` (one goroutine,
  stop at the first failure, ONE done-event): the panel's tick is a
  work-tree statement, so committing only what happened to be staged
  would silently commit a stale version. The pathspec-limited commit
  leaves anything else already staged in the index — that's what makes
  the row safe on a half-staged tree. "Commit staged…" (no targets) is
  still the plain index commit.
- **A suggestion is a normal, visible chat turn** — never a hidden
  second session. `gitPanelSuggestCommit` opens the panel first (a
  request streaming into a hidden panel reads as a hang), collects the
  diff off-loop (`gitCommitDiffEvent`), and sends through
  `chatSendPrompt`, the single dispatch point. The transcript gets a
  short ask, not the patch; the panel is a narrow strip.
- **The answer is claimed by generation + transcript mark**
  (`commitSuggestReq`), the same staleness discipline as every other
  chat result — never "the last agent message". A cancelled or errored
  turn drafts nothing, a torn-down connection's answer is dropped
  (`chatDisconnect` clears the request), and the draft never steals the
  modal slot from a pending permission prompt.
- **Nothing commits without an Enter.** The draft only PRE-FILLS the
  commit prompt. `commitSubject` strips fences, "Commit message:"
  labels, bullets and quotes and keeps one line, because the prompt
  field is single-line — an unparsed answer would put a markdown fence
  in a commit.
- **The diff is capped and includes untracked contents.** `diff HEAD`
  so a half-staged file arrives as one change; untracked targets have
  no diff at all, so their text is appended under a `new file:` marker
  or a commit of only-new-files would look empty to the agent.
- The ≡ Git group's "Suggest commit message" row is the keyboard twin
  and takes the same targets.
- **A DRAFTED message is attributed; a typed one never is.** Config
  `"commitmsgtrailer"` (default on, `SaveCommitMsgTrailer`, ≡ Git
  toggle) appends `Co-Authored-By: <agent name> <noreply@…>` — the
  address is deliberately non-routing (`chatAgentDef.coAuthorEmail`),
  because the trailer is a RECORD that a machine wrote the sentence,
  not a mention of an account ced never verified. `openCommitPrompt`
  (typed) and `openCommitPromptDraft` (the agent's) are separate entry
  points for exactly this reason, and the `[trailer: on]` chip beside
  the ✦ button appears only on the drafted one — a chip over
  hand-written work would state something the user can't act on. The
  chip is a PER-INVOCATION copy (the ≡ row is what persists), and it
  travels with `commitSuggestReq` so a re-draft doesn't re-arm what the
  user just switched off. `commitMsgWithTrailer` is idempotent.
- **`promptModal.extras` is the generic button row** behind both the ✦
  button and the chip: `extras[0]` holds the modal's right edge and the
  rest lay out leftwards, each with a RESERVED width (a toggle's label
  changes length; the target must not). The modal WIDENS to carry them
  (`promptModal.width`) — and refuses to on a terminal too narrow,
  where `extraRects` sheds extras from the left instead of painting a
  truncated click target. Each extra carries an **Alt chord**
  (`promptExtra.key` → `fireExtraKey`: `alt+a` drafts, `alt+t` flips the
  chip), safe for the find bar's reason — the modal consumes the
  keystroke, so handleKey's Alt+rune leader branch never sees it, tmux's
  folded "Esc a" included. It is not optional garnish: the ≡ menu, where
  every other keyboard twin lives, is unreachable from a surface that
  owns the keyboard, so without the chord these buttons would be
  mouse-only on the one terminal that eats clicks. The chord fires even
  when the button was SHED for width — a key can't lie about its target
  the way a truncated button can — and `commitPromptHint` names it in
  the subtitle, the prompt's only discovery surface.
- **AGENT AVAILABILITY IS A REASON, NOT A GATE.** The ✦ button sits on
  every commit prompt in a repo, and the Suggest rows on every change,
  whatever state the agent is in; `commitDraftBlockedReason` (over
  `chatUnavailableReason`, the one spelling shared with
  `chatOpenPanel`) is what says no. That's the menuCopilotAuth rule, and
  three things force it here: a verdict of "not installed" is only
  DISCOVERED by starting the agent, so hiding the button pre-emptively
  hides it from the very machine that needs the message most; a user
  whose configured backend has no binary on PATH (`"chatagent"` naming
  an agent they never installed) otherwise gets a commit dialog with no
  AI affordance and nothing at all to explain the absence; and the
  reason names the binary, which the absence cannot. The refusal is
  checked BEFORE the modal closes — a no must not cost the user the
  message they had already typed. `canSuggestCommitMsg` is therefore
  down to the one question that makes the ACTION meaningless rather than
  merely unavailable: is this a repository.

### Git log panel (app/gitlog.go + gitlogactions.go)
A JetBrains-style history browser (Esc-L) in the SAME bottom strip as
the changes panel: commits on the left (● marks ref-decorated rows;
`--all` so cherry-pick can reach other branches, capped at 400 with a
"400+" title when truncated), the selected commit's `git show
--pretty=fuller --stat --patch` on the right. It deliberately MIRRORS
gitpanel.go's shape rather than sharing code — the house patterns are
the shared part. House rules:

- **Single-occupancy in every direction**: log, changes panel, compare,
  and a bottom-docked terminal swap, never stack — each opener closes
  the others. `growBottomPanel`/`shrinkBottomPanel` fan out to all
  four; single occupancy guarantees at most one acts.
- **Verbs live behind `Actions ▾`** (openPicker, the house rule):
  cherry-pick, revert, reset, detached checkout, branch/tag creation,
  the two copies. Labels name the branch and hash they'll touch. Reset
  modes are a SECOND picker; only `--hard` confirms — soft/mixed are a
  reflog entry away from undone, and cherry-pick/revert CREATE commits.
  The ≡ "Git log actions" row is the keyboard twin.
- Header buttons are single-width glyphs through btnRects: `⧉ hash`
  (one-click full-hash copy), `⟳` (manual refresh), `✕`. The rule
  outside them is the height handle; the list/detail divider drags too.
- **Refresh rides refreshGitStatus** (no-op while collapsed), so
  finished git commands and the 10s tick keep it honest for free.
  Selection is preserved BY HASH across refreshes; a detail pane
  already showing the selected hash is never refetched (commits are
  immutable). Detail fetches post `gitLogShowEvent`s and stale results
  drop against the current selection.
- Double-click on a detail row jumps to the file/line that diff row
  touched (`gitLogDetailTarget` — diffTargetLine generalized to a
  multi-file patch, paths resolved against the repo toplevel and
  confined to rootDir). Best-effort: history may have moved on, so a
  line past EOF clamps.

### Git status report (app/gitstatusreport.go)
The ≡ Git group's "Git status…" row — and the same row in the changes
panel's `Actions ▾` picker: git's own long-form report, forked on demand
and shown in the info modal. House rules:

- **It exists for what the porcelain snapshot DROPS.** gitstatus.go asks
  `status --porcelain` and keeps two answers — which files changed, and
  how far HEAD is from its upstream — which is what the tree colors, the
  changes panel and the status bar are built from. The narrative header is
  everything else: the sequencer's state (a stopped cherry-pick, a rebase
  in progress, the still-unmerged paths), what HEAD detached from, whether
  there is an upstream at all. Re-deriving that would mean parsing four
  more surfaces. Hence long format, deliberately NOT `--short`: the short
  form IS the panel's list, so a row producing it would say nothing new.
- **Two `-c` overrides, both about the surface it lands on.**
  `color.status=false` because a user with `color.ui = always` would get
  raw SGR drawn as text in a modal that parses no ANSI;
  `advice.statusHints=false` because those `(use "git restore …")` lines
  name shell commands for verbs that are ROWS IN THIS VERY MENU, and the
  info modal doesn't scroll, so every advice line costs a line of report.
- **The body is capped to the WINDOW and the cut is named.** openInfo
  draws every line it is handed and `centeredRect` doesn't clamp, so rows
  past the bottom would be painted off-screen and lost. The remainder goes
  in the last surviving row (the project-search rule): a silently short
  status reads exactly like a clean one, which is the single wrong answer
  a status can give.
- **An arriving report DECLINES an occupied modal slot** and flashes
  instead. The round trip is milliseconds, so what this guards is not a
  dialog the user opened meanwhile but one that arrived unprompted (a chat
  permission request, a disk-conflict warning) — openModal replaces rather
  than refuses, so stealing the slot would silently drop that modal's
  pending reply. A report is re-runnable; a dropped permission answer
  leaves an agent stuck.
- Failures are SURFACED (the write-side contract in gitcmd.go) rather than
  swallowed the way the background snapshot's are — the user asked for
  this one. Enabled on any repo, clean included: "nothing to commit" is a
  real answer, and it is the one a user suspicious of the tree's colors is
  asking for. No leader key (the flat table is out of letters); the ≡ row
  gets the command palette for free.
- **The panel's Actions row reuses `menuGitStatus`**, one spelling of the
  verb (the menuGitCommit precedent). It is the only row in that picker
  about the REPOSITORY rather than the selection, which is exactly why it
  belongs there — the panel draws the change set and structurally cannot
  draw the rest of git's answer. It is the one row NOT subject to the
  picker's omit-when-it-would-no-op rule: a clean panel is the case where
  "nothing to commit, working tree clean" is the answer being asked for,
  so an otherwise-empty Actions list now offers this instead of flashing.

### Remote open (internal/remote + app/remote.go)
`ced --remote <file>` hands a file to an instance already running on that
project; `ced --wait <file>` does the same and blocks until the editor is
finished with it, which is what makes `EDITOR="ced --wait"` work for
`git commit`. Without it, `$EDITOR=ced` in another tmux pane starts a
SECOND full-screen editor nested inside the first one's terminal strip.
House rules:

- **DISCOVERY IS BY PROJECT ROOT, not "the" instance.** Everything here
  is rooted — the tree, the finder index, gopls's rootUri, the terminal's
  cwd — so a file delivered to an instance rooted elsewhere lands in a
  workspace where none of that applies. A client probes every socket,
  picks the instance whose root CONTAINS the file (longest root wins),
  and reports `ErrNoInstance` when none does. `contains` requires a
  separator at the boundary, or `/a/proj` claims `/a/project-notes`.
- **ErrNoInstance is a FALLBACK, not a failure.** Both flags start a
  normal local editor when nothing is listening — a `$EDITOR` that errors
  out is worse than one that opens the wrong window, and a bare `--wait`
  then blocks the way any terminal editor does, so git behaves the same
  either way. A handler REFUSAL is the opposite and must never be
  confused with it: that returns a real error, because falling back there
  would silently start a second editor on a file the first one declined.
- **Sockets are named per PROCESS** (`<root-hash>-<pid>.sock`), not per
  root. A deterministic per-root name forces every second instance on one
  project to decide whether to take the socket over, and the honest
  answer is that it can't — the first one is still running and still
  wants it. Per-process names let both listen; a client unlinks the ones
  nobody answers, so a crashed instance needs no reaper. They live under
  `$XDG_RUNTIME_DIR/ced` (else a per-uid folder in the system temp dir),
  0700 — **never `~/.config/ced`**: a socket is runtime state that must
  not survive a reboot. Keep the names short: the kernel caps a unix
  socket path at ~104 bytes, which is also why the tests can't use
  `t.TempDir()` on macOS.
- **Events only, and every waiter is released exactly once.**
  `serveRemoteOpen` runs on the connection's goroutine, posts an event
  carrying a buffered reply channel and blocks for the main loop — the
  ACP permission-request shape, for the same reason (the handler has to
  block on a decision the loop makes). `releaseRemote` is the single
  write path and deletes as it closes, so a double release can't panic.
  Exactly three things release: the tab closing, `Close` (a quit AND a
  folder switch), and the ≡ toggle going off. A `--wait` client left
  hanging is a shell prompt in another pane that never comes back.
- **Closing the tab is the gesture, not saving.** `$EDITOR` callers
  expect the editor to be FINISHED, not merely to have written once.
- **The root guard is re-checked on arrival.** A client already refuses a
  mismatched instance, so `handleRemoteOpen`'s check only fires for a
  request that didn't come from ced's CLI — and the answer is the chat
  filesystem's: an error the caller can read, never a file opened outside
  the workspace.
- Silent degradation, with one twist: a socket that won't bind costs the
  handoff, not the editor, and the reason is HELD on the state for the ≡
  label rather than flashed (a startup flash scrolls past). The label
  therefore has three states — `on` / `off` / `unavailable` — because a
  choice and a problem are different answers to "will `ced --remote` find
  me?", and collapsing them leaves a user toggling a preference that was
  already on. `"remote"` is the persisted key (default on), the row lives
  with the workspace rows at the top of ≡ **File**, and there is no
  leader key (a once-a-day action, and the flat table is out of letters).
- `remoteListenFn` is a package var; newTestApp leaves `remote.enabled`
  false and the transport tests stub `socketDirFn`, so no test can bind a
  socket a real client would then find.

### The cats host integration (internal/cats + app/cats_glue.go)
ced usually runs inside cats, the terminal multiplexer this editor is named
after. The integration is a client for three of its surfaces: the control
socket (unary JSON commands), the event stream that socket upgrades to, and
the SEPARATE hook socket agents report their state on. Roadmap and phase
plan: `ai_docs/cats-native-plan.md`. House rules:

- **TWO TIERS, AND TIER 0 IS NOT A DEGRADED MODE — it is ced in any other
  terminal.** Detection is `CATS_ENV=1` + `CATS_PANE_ID` + a control socket
  that ANSWERS a ping (a socket file proves nothing; a crashed server leaves
  one behind, and even a successful dial only proves something is
  listening). **No feature may exist only at Tier 1 without a Tier-0 path.**
  Every call site reads `if a.catsTier1() { … } else { fallback }`.
- **The env sniff is free; the probe is IO.** `DetectEnv` runs inline at
  startup, `Caps.Probe` runs on a goroutine and posts a `catsEvent` back. A
  wedged host must never hold up the editor's first frame.
- **Never import the cats module.** The wire structs in internal/cats are
  hand-copied minimal mirrors of cats' `internal/app/command_vocab.go` and
  `events.go`. ced has to stay buildable on a machine that has never heard
  of cats, and a shared type package would make the editor's build depend on
  the multiplexer's.
- **Events only, as everywhere else.** The stream's callbacks run on the
  reader goroutine and do exactly one thing: PostEvent. One event type
  (`catsEvent{kind, …}`) so app.go's switch gains one case.
- **The stream reconnects forever.** A unary call that fails is a feature
  that didn't happen; a subscription that stays dead is a feature that
  stopped working and never said so. Capped backoff, indefinitely, until
  Close — and Close must interrupt a blocked read, which is why the live
  connection is held under a mutex. **One json.Decoder spans the ack AND the
  events**: a second decoder for the pump strands any event the server wrote
  in the same breath as the ack, invisibly and forever.
- **"Blocked" means a question the user did NOT ask for**, not "a modal is
  open". They pressed Rename; they know. Blocked is the file that changed
  underneath them, the agent asking permission, the formatter asking for
  trust, the cherry-pick that stopped. Those sites call `catsAsking(phrase)`
  AFTER opening the modal (openModal clears the mark on its way in), and the
  mark clears itself when the modal slot empties. The phrase is what shows
  up on a phone, so write it as one.
- **Report on CHANGE only.** Every report is a potential toast or push, so
  re-sending "working" per keystroke would get the whole channel muted.
  `catsAfterEvent` runs LAST in the dispatch tail, after the hooks that can
  raise the very question that blocks us.
- **The hook seq is seeded from the clock, not from 1.** The server keeps a
  per-source high-water mark ON THE PANE, which outlives the process; a
  counter starting at 1 has every report from a restarted ced (a folder
  switch alone rebuilds the App) silently dropped as stale. cats' own
  shipped hooks use a wall-clock seq for the same reason. Seq is stamped at
  CALL time, on the main loop, because the sends are fire-and-forget
  goroutines that genuinely do arrive out of order.
- **Source is `"ced"`, never `"cats:ced"`.** The `cats:` prefix names cats'
  own built-in agent integrations, whose state is detection-driven and whose
  hook state reports are deliberately ignored. Verified live: a custom
  source takes real state authority for its pane, and `pane.release_agent`
  hands it back.
- The hook reporter is armed independently of the control client: different
  sockets, and the attention story is the half that matters when you are not
  looking at the screen.

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

### Open folder + session restore (internal/session + app/folder.go)
Switching projects without leaving the editor, a recent-folders list, and
each folder's tabs and cursors coming back when you return. House rules:

- **A ROOT SWITCH IS A RESTART.** `rootDir` itself is touched in a handful
  of places; everything DERIVED from it is the cost — the tree, the
  finder index, git status, both git panels, gopls's `rootUri` (fixed at
  initialize), the ACP session cwd, MCP's `roots/list`, plugin working
  directories, the compare panel's two sides. So `requestOpenFolder`
  parks the new root on `App.nextRoot`, sets `quit`, and **main** tears
  the App down and calls `New(newRoot)` in a loop. One code path builds a
  workspace and it's the one that runs on every launch; a second
  re-derivation path would be exercised by nobody. Close is called
  EXPLICITLY there, not deferred — a deferred one fires when main
  returns, leaving the old screen, goroutines and language servers alive
  under the new App. The screen blinks once; that is the whole price.
- **The state file is `~/.config/ced/state.json`, and it is separate from
  config.json for the INVERSE of mcp.json's reason.** mcp.json is
  separate because the user hand-writes it; this one is separate because
  ced rewrites it on every folder switch and every exit. Machine churn
  has no business in a file somebody hand-edits, and a corrupt state file
  must cost a tab list rather than a settings file. `userconfig` owns
  only the PATH (`StatePath`); the schema lives in `internal/session`.
- **Order IS the recency** — the entry list is stored most-recent-first
  rather than carrying timestamps that would have to be sorted on load.
  Nothing shows "opened 2 hours ago", so a timestamp would be a field
  with no reader and one more thing to get wrong across clock skew.
- **The visit is recorded at STARTUP, the tabs at Close.** That split is
  what makes `--last` and the recent list correct after a crash: a run
  that dies costs its tab list, never the fact that you were there.
- **`session.Normalize` resolves symlinks, and the app compares through
  it.** `ced /tmp/proj` roots at the path as typed; `cd /tmp/proj && ced`
  roots at what the kernel reports, which on macOS is `/private/tmp/proj`.
  Without it one directory keeps two half-sessions that overwrite each
  other in turn. Best-effort: a path that no longer exists keeps its
  absolute form, or `Remove` could never prune it.
- **Restore checks the file EXISTS itself** rather than leaning on
  `editor.NewTab`, which deliberately succeeds on a missing path — that's
  the `ced foo.go` new-file intent, right for an explicit open and wrong
  here. Nobody asked to resurrect a file they deleted, and an empty
  buffer wearing its name is the worst way to say it's gone. Everything
  else degrades in silence too (too big, binary, unreadable): the user
  asked to open a FOLDER, so a wall of messages about files they may not
  remember having open is noise. Cursor and scroll come back through
  `Tab.RestoreView` — the stored scroll is part of what's being put back,
  so this must NOT set `cursorMoved` (the Find-all Esc argument).
- **Tabs are wired by `wireTab` / `announceTab`, shared with openFile.**
  Restore is the second way a tab is born; a second copy of the wiring
  would drift and a restored tab would quietly lack the git gutter, the
  word highlight, or a plugin's marks.
- **Bare `ced` opens the CURRENT directory** and deliberately does not
  reopen the last folder — `cd myproj && ced` is the gesture this editor
  is launched with, and landing somewhere else would make that reflex a
  lie. You get the folder's TABS back instead, and `ced --last` for the
  times you really did mean "wherever I was".
- **A folder switch owes the same unsaved-changes modal an exit does**
  (it discards the whole workspace), and a Save that FAILS must not
  switch — same short-circuit as `menuQuit`.
- The recent picker is `openPicker` (house rule) and EXCLUDES the current
  root rather than annotating it, unlike the theme picker: re-picking a
  theme is how you revert a preview, but re-picking your own folder
  rebuilds an identical workspace. Deleted folders are PRUNED during the
  walk — a row you can't open is worse than a shorter list.
- Rows live at the top of the ≡ **File** group (the File>Open Folder
  convention) with **no leader key**: the flat table is out of mnemonic
  letters and this is a once-an-hour action. `"session"` is the persisted
  toggle; folders are recorded with it off, because the recent list is a
  different feature reading the same file.
- `sessionStatePathFn` / `sessionConfigPathFn` are package vars;
  newTestApp pins both at temp dirs so no test can rewrite the
  developer's real recent-folders list or their restore preference.

### Menu shortcut hints
`menuItemDef.shortcut` is a display-only accelerator column rendered
right-aligned and muted in the ≡ menu ("esc s", "alt+←"). Dispatch
still lives in the leader table / handleKey — when adding or rebinding
a key, update both or the menu lies. Rows without a binding leave it
empty; drawMenu skips the hint when a long label would collide.

### The Esc-a AI namespace (leader.go)
The leader table is flat with TWO exceptions: a `leaderBinding` carrying
a `sub` table (or a `subFor` resolver) is a PREFIX. Firing it runs no
action — it stores the sub-table on `App.leaderChord`, stamps
`leaderChordAt` and `leaderChordName`, and flashes the binding's `hint`;
the next rune resolves against that table in `handleChordKey`, which
handleKey calls before everything else. House rules:

- **It exists because the AI surface outgrew the flat table.** Fifteen
  menu rows, and the letters had run out — skills briefly lived on a
  shifted `Esc S` for exactly that reason. That's the bar for a new
  namespace; don't add one without it. A chord is a real cost, paid by
  everyone who has to remember which letters are prefixes. `Esc x`
  (plugins) is the only other one that has cleared it, and it did so
  from the opposite direction — see the plugin section: its keys belong
  to the USER and are unbounded, so they can't live in the flat table at
  all. That entry is also the only DYNAMIC prefix (`subFor`/`hintFor`),
  and the only one allowed to arm nothing when its table is empty.
- **`Esc a` took the palette's alias.** The palette is `Esc k` (plus the
  ≡ menu's pinned headline row) — owner's call, on the grounds that the
  namespace is the higher-traffic use of the letter.
- **One level only.** A sub-binding with its own `sub` is a bug the
  binding-table test rejects. Sub-bindings collide with the top-level
  table on purpose (`a`, `f`, `m`, `t`) — the prefix already said which
  world you're in.
- **A miss inside a live chord is SWALLOWED with a flash**, deliberately
  unlike the flat table's fall-through. A lone Esc can be a stray tap, so
  the flat table stays harmless to mash; `Esc a` is two deliberate keys,
  and falling through would answer a mistyped chord by dropping a
  character into the user's code.
- **The window is `leaderChordFor` (2s), not `doubleEscMs`.** A chord is
  composed, not reflexive — 500ms isn't long enough to remember which
  letter the model picker is. Esc drops a pending chord (handleChordKey
  disarms on any non-rune and reports false, so the normal Esc handling
  still runs and `Esc a Esc s` saves).
- **tmux comes free.** Both leader entry paths — bare Esc + rune, and the
  folded `Alt+<rune>` tmux delivers — funnel through `fireLeader`, so the
  prefix arms identically either way and the second rune arrives bare.
  `TestLeaderChord_TmuxAltPath` pins it.
- The flashed hint is the namespace's ONLY keyboard discovery surface
  (the flat table gets that from the ≡ hint column), so a test asserts
  every sub-binding appears in it.

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

Two rules about the formatter's output coming back:

- **CED'S OWN WRITE COMING BACK IS AN EDIT, NEVER A NEW BASELINE.**
  `handleFormatDone` adopts it through `Tab.ReloadAsEdit` — ONE
  structural undo step on top of the preserved history, and *nothing at
  all* when the bytes match. `Tab.Reload` re-seeds the baseline
  (`initUndo` nils both stacks) and is reached from the app only via
  `ReloadUndoable`, which is what an EXTERNAL writer earns. That
  distinction is the whole reason there are three Reload methods; do not
  collapse them. It is also not a style point: with a plain `Reload`
  here, auto-save destroyed the user's entire undo history on every idle
  pause in a Go file, which reads as undo simply being broken. Plugin
  in-place rewrites (`reloadPluginTarget`) go the same way.
- **A run in flight suppresses the reconcile tick for that path**
  (`formatRunBegin`/`formatRunEnd`/`formatRunning`, a per-path COUNT
  because a chain and a re-save overlap). The formatter writes from a
  goroutine, so the tick can stat the file in the window before
  `formatDoneEvent` adopts its mtime — where a write WE caused reads as
  somebody else's, costing the history on a clean tab and raising a ⚠
  conflict about ced's own write on a dirty one. The save guards are
  deliberately NOT taught about this: same window, but hitting it needs
  a keypress inside ~100ms and the failure mode is a prompt, not lost
  work.

### Auto-save (app/autosave.go)
Debounce mirrors the LSP didChange pattern: `autoSaveAfterEvent` runs
after every dispatch, compares the sum of all tabs' EditRevs, and
(re)arms a `time.AfterFunc` that posts `autoSaveEvent`. Saves are
silent (no flash), run format-on-save in quiet mode, defer while any
modal/menu is open, and skip tabs whose disk file changed after load
(explicit Save remains the overwrite path). The ≡ toggle persists via
`userconfig.SaveAutoSave`, which round-trips unknown JSON keys — don't
replace that with a struct marshal. Default is ON.

- **The idle window is 5s and configurable** (`"autosavedelay"`, a
  duration string or bare seconds, clamped to `[500ms, 5m]`). It has NO
  ≡ row — the menu is for verbs, and a duration would need a picker of
  canned values to have any menu shape at all. A typo is reported (the
  rule every other key follows: a silently ignored value is one the user
  believes is in effect); a value merely out of range is clamped in
  silence. All reads go through `autoSaveInterval()`, which maps the zero
  value to the default — tests build `App` as a struct literal, and
  `time.AfterFunc(0, …)` fires immediately.
- **LEAVING FLUSHES, which is what lets the window be that long.** The
  terminal losing focus (`autoSaveOnFocusChange`, off `scr.EnableFocus`
  + `*tcell.EventFocus`) and switching tabs (`autoSaveDepartingTab`, in
  `switchToTab` because it is the single funnel) both write immediately.
  Neither owns any save logic — both go through `autoSaveTabIfEligible`
  / `handleAutoSave`, so there is exactly one answer to "is this tab safe
  to write in the background", including the modal deferral. Both are
  gated on the ≡ toggle: a focus-out write IS an auto-save. Focus-IN
  deliberately does nothing (the countdown is armed by edits, not by
  attention). **Focus reporting is best-effort and must never be the only
  path to disk** — macOS Terminal.app never reports it, tmux needs
  `focus-events on` — so the timer stays the backstop. Never call
  `When()` on a `*tcell.EventFocus`: tcell's constructor leaves the
  embedded `*EventTime` nil and it panics.

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
- **Single-occupancy bottom strip**: while BOTTOM-docked, the terminal,
  the git panels and the compare panel swap, never stack (opening one
  collapses the others). Two resizable bottom strips would need circular
  height-clamp math on small windows — keep the exclusivity. A
  LEFT-docked terminal doesn't compete for the bottom, so it coexists
  with them; flipping back to bottom evicts whatever is there.
- **Focus flag, not a modal**: `term.focused` routes plain editing
  keys to the input line; Esc stays global so leaders and the
  double-Esc menu keep working from inside the terminal. Any click
  outside the panel unfocuses. Esc-` is focus-or-toggle.
- **Coalescing writer**: grsh output lands in `termWriter`'s buffer
  with at most one `termOutputEvent` in flight — never post
  per-chunk events (heavy output would overflow tcell's queue).
- **Paste is real-shell paste: a line break means Enter.**
  `termPasteTarget` (textpaste.go) claims bracketed pastes for the panel
  and `termInsertPaste` runs the complete lines in order, parking an
  unterminated tail at the prompt — owner's call, chosen over
  flattening. The paste's first line joins what's already on the input
  line, at the caret. Three invariants hold it together:
  - **One at a time.** Eval is async, so lines can't be looped over:
    `term.pasteQueue` holds the tail and `termRunPasteQueue` submits the
    next line only when the previous Eval reports done (re-entered from
    `handleTermDone`). A loop here would interleave commands or trip
    `submitTermCommand`'s busy guard.
  - **Through `submitTermCommand`, always.** That's what makes a pasted
    block feed grsh's `NeedsMore` continuation as ONE unit and echo each
    line into the scrollback, so a batch reads back as what it ran.
  - **⏹ aborts the remainder**, and drops the queue whether or not
    there's a process to signal — "stop" has to mean the rest never
    runs. `exit` drops it too (that shell is gone). Hiding the panel does
    NOT: a running command already survives hide/show.

  What NOT to do: don't restore per-rune replay (that ran every line but
  the last, through `handleKey`'s shortcut machinery), and don't join
  lines with `; ` (invents separators the user never typed, and a pasted
  `#` comment then swallows the rest of the line).
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

### Clickable terminal output (app/termdiag.go)
Any scrollback row naming a file and a line — a compiler error, a
`go vet` finding, a `grep -n` hit — is a jump into the editor. It
closes the build→fix loop inside one pane, which is the whole reason
the panel exists. House rules:

- **ONE parser decides what a location is.** `plugins.ParseDiagnostic`
  (the exported single-line twin of `ParseDiagnostics`) already speaks
  the compiler/grep convention the decoration layer is built on; a
  second implementation here would drift and the user would have no
  way to tell which one decided a row wasn't a link.
- **A LOCATION IS ONLY REAL IF THE FILE IS.** That parser is
  deliberately permissive — it has to be, since its usual caller
  already knows which file the output describes — so a bare "12:30"
  parses fine. Terminal output belongs to nobody, so the guard here is
  stricter: the row must name a PATH, that path must resolve to a
  regular file, and the file must sit inside rootDir (the git-log
  jump's confinement rule).
- **Relative paths resolve against the SHELL's cwd**, not the project
  root: `go build` prints relative to where it ran, and grsh's `cd`
  moves that. Output that no longer resolves after a `cd` is inherent
  to a scrollback, and is what a real terminal's file links do too.
- **Resolution is CACHED, keyed by cwd + the path as printed.**
  Drawing asks per visible row per frame; uncached that is a stat
  syscall per row of output on every repaint. The cwd in the key means
  a `cd` invalidates exactly the answers that changed.
- **`termLocSpan` measures the underline, it does not re-parse.** The
  parse is lossy on purpose (a printed column of 0 clamps to
  zero-based 0), so rebuilding "path:line:col" from parsed values
  would match nothing. Measuring the raw text can't disagree with
  itself. The underline IS the affordance — without it the feature is
  invisible — so a row is a link only when both agree.
- **The list is NEWEST COMMAND FIRST, printed order within each.**
  Plain document order buries the build you just ran; plain reverse
  order shows one build's three errors backwards. The echoed command
  rows (`termCmd`) already mark the boundaries, so grouping by them
  costs nothing and gets both halves right. Capped, with the cap named
  in the title (the project-search rule).
- **Double-click is primary, the picker is the twin.** macOS Terminal
  swallows clicks, so `Esc ~` / ≡ opens the same locations through
  `openPicker`. `~` is the shifted twin of the terminal's own Esc-`.
  The menu predicate (`hasTermOutput`) is deliberately approximate —
  menuLayout runs every frame the menu is open, and the honest
  question is a scrollback walk with a stat behind it, so it's a cheap
  gate plus an honest flash.
- The row lives in the **Code** group, not with the terminal's View
  toggles: it answers the code-intelligence question ("take me to the
  problem") and is the one row there needing no language server —
  `go build` and `grep -n` are the providers.

### Run an executable (app/runexec.go)
The tree's `*` marker (execmarks.go) and the terminal panel, joined: right-
click an executable → "Run in terminal…", pick a working directory, and the
command lands on the panel's input line. Also the ≡ **File** row (the
right-click-swallowed rule), no leader key. House rules:

- **IT STAGES, IT DOES NOT SUBMIT.** Same rule as catsRunInPanel and as
  handing a selection to an agent — the editor may COMPOSE a command, the
  user presses Enter. Here it is structural rather than merely careful: an
  execute bit says how to START a program and nothing about what to pass
  it, so a row that fired immediately could never run a script that takes
  a flag. Re-running is the panel's own history (Up), which keeps the
  edited line, arguments and all — hence no per-file command memory.
- **THE `cd` IS PART OF THE STAGED LINE, because grsh has no subshell.**
  v1's language has no `( … )` grouping ("there is no subshell to run them
  in") and its `cd` builtin chdirs the WHOLE editor process by design, so a
  scoped cd is not available; wrapping in `sh -c '…'` would bury the
  command inside quotes where arguments cannot be typed. So the cd leads
  the line — visible, editable, and FIRST, which is also what makes typed
  arguments land after the command. Joined with `&&` (catsRunScript's
  argument) and omitted entirely when the shell is already there
  (menuCatsTerminal's pointless-cd rule), which is what stops a second run
  stacking a redundant one.
- **The directory picker widens into the frecency list, it does not build
  one** (the findall-reuse rule). Rows run most-specific-first — the file's
  own directory, the project root, wherever the shell currently is — then
  ced's own recent folders and the host's cdx-ranked history through
  `catsRecentFolders`, so this picker and "Open project" can never disagree
  about which directories exist. Deduped on `session.Normalize` (the folder
  store's key) and pruned to what still exists. ONE candidate is not a
  choice, so a workspace with nothing else to offer stages straight away.
- **The command is relative to the chosen directory when it sits inside
  it**, absolute when it does not (catsRelPath's rule) — and the `./` is
  load-bearing, since a bare `tool.sh` is a PATH lookup. `shellArg` quotes
  only what needs it, unlike `catsShellQuote`'s unconditional form: this
  line is read and edited by a person.
- **The execute bit is re-checked live**, never trusted from the tree node
  (stamped at the last reload) — a `chmod -x` in the very panel this row
  feeds must refuse rather than stage something that only fails oddly.

### Named themes (internal/theme + app/theme.go)
Ten shipped palettes plus `~/.config/ced/themes/*.json`, switchable live
from ≡ → Theme. House rules:

- **Eight core keys, twenty-seven derived.** A theme states `bg fg muted
  line accent ok warn err`; `Normalize` fills the rest from the ordered
  derivation table in palette.go (`selection ← 32% accent over bg`,
  `syn-string ← ok`, `git-deleted ← err`, …). That's what keeps a
  hand-written theme eight lines long, and it means **adding a new color
  key never invalidates a theme file somebody already wrote** — give it a
  derivation and every existing theme gains it. A stated key always wins,
  and later rules see the stated value (syn-operator lightens whatever
  syn-type ended up being), so order the table by dependency.
- **Palettes are `map[string]string`, not a struct of colors.** "Was this
  key stated?" has to be answerable and zero is a real color. Specs hold
  the SPARSE palette — what the author literally wrote — so re-saving an
  eight-line theme can't balloon it to thirty-five.
- **`theme.Default()` stays a hardcoded literal.** It's the floor when a
  file is broken or a saved name is gone, so it must not be able to fail;
  `TestBuiltin_TokyoNightMatchesDefault` pins it against the built-in of
  the same name so the two can't drift.
- **A switch is a live restyle.** `setTheme` assigns `App.theme`,
  repaints the screen default style, and marks every tab `StyleStale`.
  `Tab.Styles` is the ONE cache of theme-derived colors in the editor —
  everything else builds its styles inside its own draw call. Anything
  that starts caching colors must join `restyleTabs` or it keeps painting
  the old palette until the buffer is edited.
- **Same silent-degradation contract as LSP/formatters.** Unknown name,
  broken file, unwritable config → one flash, editor keeps running on the
  default. Per-file degradation in the registry: one bad theme costs that
  theme, never its neighbours. A missing themes directory says nothing at
  all (it's the common case).
- **A user theme shadows a built-in IN PLACE** (same name → same list
  position), so tweaking a shipped theme doesn't produce two identical
  picker rows.
- **The editing loop is the customization UI.** "Customize theme…" writes
  the active palette out FULLY EXPANDED under a `-custom` name (so the
  original stays reachable), switches to it, and opens it as a tab;
  `themeAfterSave` re-reads the registry on any save under the themes
  directory. Don't replace that with a color-picker modal — ced has no
  settings dialog by design.
- The picker is `openPicker` (house rule) and KEEPS the current theme in
  the list, annotated — unlike the chat-model picker — because re-picking
  is how a user reverts after previewing. Rows are in the ≡ **View**
  group for the same above-the-fold reason the terminal rows are.

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
2 top-zone rows + 125 group actions + 15 headers (142), height 148,
dividers `[2, 5, 145]`. **Adding a menu row means updating those pins**
(and `TestMenuLayout_WithCustomActions` / the two tall-window heights in
`TestMenuModalRect_*`).

### Sidebar splitter drag
A drag is detected when a press lands at exactly `x == splitterX()`.
Min widths: `minSidebarWidth = 18`, `minEditorAfterDrag = 40`. Don't
let the editor shrink below that. A drag that MOVES the splitter also
turns auto-fit off — see the next section for why.

### File-tree auto-fit (app/treeautofit.go + filetree's ContentWidth)
The sidebar sizes itself to the tree's longest row, so expanding
`internal/app/` stops truncating the names inside it. House rules:

- **`sidebarWidth` is a preference OR derived, never both.** That's the
  tension the whole feature turns on: auto-fit re-derives the width every
  frame (`autoFitSidebar`, called at the top of `draw` **before any rect
  helper is read** — a width derived afterwards paints the row the user
  just expanded truncated and leaves it that way until the next event),
  and a splitter drag states one. So a drag that actually MOVES the
  splitter calls `lockTreeAutoFit`: auto-fit off, persisted, with the
  flash naming the ≡ row that undoes it. Without that handoff the next
  expanded folder silently overwrites the drag, which reads as the
  splitter being broken rather than as a feature. The lock is gated on
  the width actually changing — a press with a pixel of jitter is not a
  statement about anything, and this writes to disk.
- **Grows only, and only into room the editor won't miss.** The floor is
  `defaultSidebarWidth` (a panel that also shrink-wrapped a shallow tree
  would move the editor twice per expand/collapse, and 30 columns is the
  width ced ships with — nobody wants it back); the cap is
  `autoFitMinEditor` (80 — deliberately far above `minEditorAfterDrag`'s
  40, because a DRAG is the user asking for a narrow editor while this
  happens on its own), further capped to `1/autoFitMaxShareDen` of the
  band, since the editor's floor alone would hand a 240-column terminal's
  tree 160 columns. Too narrow for the editor's floor at the DEFAULT
  sidebar width → auto-fit does nothing at all. That last clause is the
  "if there is reasonable room" half of the feature.
- **The measurement shares the renderer's row construction.**
  `nodeRowSegments` is the ONE place a row's text is built, read by both
  `drawNodeRow` and `Tree.ContentWidth`; a second copy would drift and the
  fitted width would be a column or two wrong in exactly the cases that
  matter (deep nesting, icons on, an executable's `*`). It counts RUNES
  because `drawString` advances one column per rune, so measure and paint
  make the same assumption about glyph width.
- **It measures every expanded row, NOT the scroll window** — the one
  place the word-highlighter's window-scoping rule is deliberately not
  followed. A window-scoped measure makes the panel breathe as the user
  wheels past a long filename, shifting the editor's columns for a gesture
  that changed nothing about the tree. Expanding is deliberate; scrolling
  isn't. The walk is bounded in practice because the tree is lazy — rows
  exist only for folders somebody opened by hand.
- `"treeautofit"` is the persisted key (default on) with a ≡ **View** row
  under the tree rows it governs, and no leader key (a once-a-session
  action, and the flat table is out of letters). `newTestApp` leaves it
  OFF: on, every draw would re-derive `sidebarWidth` under the tests that
  pin sidebar geometry, and a splitter-drag test would persist the lock
  into the developer's real config.json.

### Scrollbars (app/scrollbar.go)

A thumb whose HEIGHT says how much of the content fits on screen and
whose POSITION says where in it you are — and which drags. Two surfaces
on ONE preference, and the interesting part is the single difference
between them: the editor's bar RESERVES its column, the file tree's
SHARES the tree's own last one. Everything below follows from that.

House rules, editor bar first:

- **It DISPLACES the editor, it does not float over it** (the Find-all
  dock's rule). `editorRect` subtracts `scrollbarCols()`, which is what
  keeps every existing call site — hit-testing, the hover tooltip, drag
  auto-scroll, Alt+click, the context menu — ignorant that the bar
  exists. A thumb painted on top of the last column would cover a
  character on every row it crossed, and that column is the one
  `Tab.Render` already uses for the horizontal-overflow arrow.
- **The column is NOT conditioned on the file overflowing.** A bar that
  came and went as the buffer grew past the bottom row would move the
  editor's right edge — re-flowing everything the user was reading — on
  an edit that had nothing to do with layout. A short file gets a
  FULL-HEIGHT thumb instead, which is the honest way to say "this is all
  of it". The two cases that do give the column back are no tab open
  (there is no scroll position to report) and a band too narrow for
  `scrollbarMinEditor` (at that size the code is the scarce thing).
- **It sits at `ex+ew`, INSIDE a right-docked Find-all list.** The bar
  belongs to the editor, so it stays welded to the editor's edge wherever
  that edge moved to — which is why `findAllModal.rect` is the one call
  site that had to learn about it (`ex + ew + scrollbarCols()`). A second
  surface that positions itself against the band's right edge has to do
  the same.
- **`Tab.MaxScroll` is exported so there is ONE ceiling.** The thumb is
  placed against exactly the range the wheel can reach, including
  `clampScroll`'s overscroll pad; a second copy of that arithmetic would
  drift, and the symptom is a thumb that cannot be dragged to the end of
  a file the wheel scrolls to happily. `clampScroll` now calls it.
- **Thumb HEIGHT is measured against the file, thumb POSITION against
  `MaxScroll`.** The pad is blank space below the last line: counting it
  in the height would shrink the thumb to claim there is more file than
  there is, while ignoring it in the position would leave the thumb a few
  rows short of the bottom at the end of travel. `scrollbarMetrics` is a
  pure function for that reason — three degenerate cases (empty buffer,
  file shorter than the window, thumb as tall as the track), each one a
  place a division could panic.
- **Press on the thumb DRAGS, press on the track PAGES.** Paging is the
  reversible answer: a mis-aimed page is one press back, while jumping
  the thumb to the pointer has thrown away the position the user was
  reading from with nothing to restore it. The grab offset
  (`scrollbarGrab`) is taken at press time so the thumb slides with the
  pointer instead of snapping its top edge under it.
- **The hit-test runs BEFORE the editor catch-all** in `handleMouse`, the
  same reason every panel's does: the column is inside the editor's
  y-band, so an unasked press would move the caret to whatever line the
  user grabbed the thumb on. The wheel needs no such case — `scrollAt`'s
  catch-all already scrolls the active tab for that y range.
- **`drawScrollbar` runs AFTER `Tab.Render`**, never before: Render is
  where `EnsureVisible` and `clampScroll` settle `ScrollY`, so a bar
  drawn ahead of it reports the previous frame's position on any tick
  that moved the cursor.
- `"scrollbar"` is the persisted key (default on) with a ≡ **View** row
  and no leader key (the flat table is out of letters, and this is a
  once-a-session decision about how much code width to spend). The row
  sits with the two dock toggles rather than up with the tree rows,
  because the rows above it are pinned above the fold on a 24-row window
  (`TestMenuLayout_TerminalRowsAboveTheFold`) with no slack to push them
  down by. `newTestApp` leaves the bar OFF, the treeAutoFit precedent:
  on, it would shift `editorRect` by a column under every test that pins
  editor geometry.

And the file tree's:

- **IT SHARES THE TREE'S LAST COLUMN**, painted over after `Tree.Render`
  rather than subtracted from `sidebarRect`. The tree keeps its full
  drawing width; the cost is that the longest row's final rune can end up
  under the bar. That is the trade the next rule buys.
- **So it appears only while the list overflows**, which the editor's bar
  structurally cannot do. There, coming and going would move the editor's
  right edge and re-flow the code on an edit that had nothing to do with
  layout; here it costs no layout at all, so a tree with nothing to
  scroll gets its column back instead of wearing a full-height thumb over
  its names. Two bars, opposite answers, one reason.
- **Auto-fit is the ONE thing that compensates** (`treeScrollbarCols`,
  read only by `autoFitSidebar`): it asks for one column more while the
  bar is up, because auto-fit exists precisely to stop the tree
  truncating names and a bar sitting on the last rune of the row it just
  widened to fit would undo that. It cannot oscillate — widening changes
  no ROW count, and the bar's verdict is about rows. With auto-fit off
  (a width the user dragged) nothing compensates and the bar simply
  overlays; dragging one column wider is the out.
- **It spans the LIST band only**, never the two header rows: those
  scroll with nothing, and the project name is itself a click target.
  `Tree.ListRows` is that split's one spelling — `Render` reads it too,
  so the "2" lives in exactly one place.
- **`Tree.MaxScroll` / `Tree.RowCount` mirror the editor's** for the same
  one-formula reason, and there is deliberately NO overscroll pad here: a
  tree has no "read the bottom comfortably" problem, and scrolling into
  blank space would just lose rows.
- **The hit-test runs before `sidebarClick`**, or a press would select
  whatever node the user grabbed the thumb on — and after the splitter
  cases, because the two columns are adjacent and the splitter is the one
  the user aims at by feel.
- Both bars share `scrollbarMetrics` and `scrollbarGrab`, so a thumb of a
  given size can never mean two things, and both go through the same
  `"scrollbar"` key: this is one feature at two surfaces (the
  find-in-file / find-in-project rule), and the single argument for
  turning a bar off — give me the width back — does not even apply to the
  tree's.

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
- A config file / dotfile. ced is opinionated. (The files under
  `~/.config/ced/` are the deliberate exceptions, and each earned it by
  being something ced cannot know for you: which shell aliases you use,
  which formatters your repo trusts, which MCP servers and credentials
  you have, which colors you can actually read. Themes in particular are
  DATA, not code: a theme file can only set colors from a fixed key
  list. Skills sit on the same side of that line for a different reason:
  ced never runs one. A SKILL.md is markdown handed to the chat agent,
  so the skills directories — including the `~/.claude/skills` and
  `<project>/.claude/skills` ced reads but doesn't own — extend the
  AGENT, not the editor. `state.json` is the odd one out and earns its
  place differently again: it holds no preferences at all, only what the
  editor did — which folders you opened and where your cursor was — so
  deleting it costs convenience and changes no behavior.)
- **A HOST plugin system** — anything ced loads and runs *as code*: Go
  `plugin` .so files (they'd cost the static binary and CGO), an
  embedded interpreter, or an editor API that has to stay stable across
  releases. That line held even when `plugins/` landed, because a ced
  plugin is a JSON manifest of SHELL COMMANDS the user already had
  permission to type — the editor's contribution is *when* to run one
  and where the output goes, not a runtime to run it in. See the plugin
  section below; the moment a manifest can express something that isn't
  "a command line plus a place to put its stdout", that line has moved.
- CGO dependencies. The whole point is one static binary.
- Tree-sitter. We use Chroma intentionally — pure Go, no setup.
- A separate `homebrew-tap` repo. The formula lives here under
  `Formula/` and that's deliberate.
