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
internal/editor/fileio.go     Open guards, line-ending/BOM round-trip, atomic save
internal/editor/highlight.go  Chroma → []tcell.Style per line
internal/editor/syntax.go     Re-lex settle policy + the style-grid patch
internal/app/syntax.go        The settle timer that wakes the loop for the re-lex
internal/app/tabbar.go        Tab strip: scroll, overflow button, switching
internal/diff/diff.go         Patience line differ + unified-diff rendering (pure Go)
internal/app/compare.go       Compare panel: buffer ↔ file / saved copy / pasted text
internal/editor/find.go       Match model + the one scanner (options: case, whole word)
internal/editor/replace.go    Replace current / replace all — one undo step, bottom-up
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
internal/app/lsp.go           gopls lifecycle, doc sync, diagnostics, definition, hover
internal/app/lspsymbols.go    Document symbols → the "go to symbol in file" picker
internal/app/termdiag.go      Terminal output → clickable path:line:col jumps
internal/app/copilot.go       GitHub Copilot sidecar: lifecycle + device-flow sign-in
internal/app/copilot_ghost.go Copilot phase 2: doc sync + inline completions (ghost text)
internal/app/copilot_chat.go  Copilot phase 3: ACP chat panel (left strip, streaming turns)
internal/app/chatagent.go     Chat backend registry + ≡ picker (Copilot / Claude Code / Gemini)
internal/app/copilot_chat_context.go  Chat context: file / selection attachments
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
internal/app/gitcommitmsg.go  Commit the panel's selection + agent-drafted messages
internal/app/gitlog.go        Git log panel: commit list + `git show` detail (Esc-L)
internal/app/gitlogactions.go Git log verbs: cherry-pick, revert, reset, branch/tag, copies
internal/app/terminal.go      Embedded grsh terminal panel (REPL strip, not a PTY)
internal/format/              format.json load, trust store, builtin goimports / gopls imports / gofmt
internal/filetree/filetree.go Lazy tree, identity-preserving refresh, hit-test, render
internal/clipboard/clipboard.go OSC 52 to /dev/tty with tmux passthrough wrap
internal/userconfig/userconfig.go ~/.config/ced/config.json loader/writer (icons, autosave, termdock, execmarks, chat*, session, theme) + mcp.json / state.json / themes / skills dir paths
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
- Seeding is a ladder: find bar → single-line selection → word under the
  cursor → a prompt. No match flashes rather than opening an empty box.

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
  or if a modal/menu took the slot meanwhile. Seeding reuses
  `findAllSeedQuery` exactly: the two features are one question at two
  scopes, and seeding them differently would be a trap. Leader is
  `Esc P`, the shifted twin of `Esc p` (names vs. contents).

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
- Leaders: Esc-d definition, Esc-i hover, Esc-D the file's symbol
  outline. Definition jumps record into the app-wide navigation
  history (nav.go) — there is no LSP-private jump stack anymore.
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
2 top-zone rows + 100 group actions + 14 headers (116), height 122,
dividers `[2, 5, 119]`. **Adding a menu row means updating those pins**
(and `TestMenuLayout_WithCustomActions` / the two tall-window heights in
`TestMenuModalRect_*`).

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
