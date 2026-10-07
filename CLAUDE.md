# CLAUDE.md — ced

Project-specific guidance for Claude Code. This file holds the RULES;
the reasoning behind each one lives in the header comment of the file
named in its section heading — read that before changing the feature.
(The long-form version of this file is in git history:
`git show 57bed9f:CLAUDE.md`.)

## What this project is

ced ("Cats Editor") is an opinionated, **mouse-first** terminal code
editor for SSH-into-tmux workflows: file tree left, tabs on top,
syntax-highlighted editor, status bar. One static Go binary, no CGO.

- The action menu opens via the `≡` icon, right-click, or double-`Esc`.
  **Almost no `Ctrl+` shortcuts** — they fight tmux/terminals. Don't add
  more. Sanctioned exception: `Ctrl-D` (duplicate line). `Alt+Up/Down`
  (move line) is fine.
- **`Cmd+` is a BONUS LAYER** (metakeys.go): every ⌘ chord is a second
  door onto a verb the Esc table or ≡ menu already reaches
  (`TestMetaAccelsAreNeverTheOnlyPath`). Arrives only via the kitty
  protocol as `ModMeta`, gated by `metaAccelArmed`. `⌘←/⌘→` are the one
  pair dispatched from the editing switch, not the rune table.
- **tmux folds Esc sequences into Alt events**: a fast double-Esc arrives
  as `KeyEsc+ModAlt`, "Esc s" as `Alt+s`. handleKey treats Alt+Esc as the
  menu toggle and Alt+<bound rune> as that leader. Keep those branches.
- **Every file action also lives in the main ≡ menu** — macOS Terminal +
  tmux often swallow right-click, so right-click is never the only path.
- **Right-click is `tcell.ButtonSecondary` (Button2), NOT `Button3`**
  (Button3 is middle in tcell v2). Tests must send `ButtonSecondary`.
- **Keyboard-owning surfaces (modals, prompts, the find bar) can't reach
  the ≡ menu**, so any button on them needs an in-surface key/Alt chord
  (Alt is safe inside them — the modal eats it before the leader branch).

## Module / repo

- Module `github.com/rohanthewiz/ced`; binary `ced` (Makefile,
  goreleaser, cats-plugin.toml `bin`, install.sh all assume it).
- Official install: the cats plugin (`cats-plugin.toml` → `~/.cats/bin/ced`).
  **No Homebrew formula/tap**, ever.

## Architecture map

```
main.go                       urfave/cli surface + `ced fav`
internal/version/version.go   const Version — single line
internal/editor/
  buffer.go tab.go            Position/Buffer ([]string); Tab: path, cursor, anchor, scroll, dirty
  undo.go                     Snapshot stack: coalescing, byte budget
  fileio.go                   Open guards, line-ending/BOM round-trip, atomic save
  highlight.go syntax.go      Chroma → style grid; re-lex settle policy + grid patch
  markdown*.go                Markdown preview: flag, row cache, blocks, inline spans
  softwrap.go                 Soft-wrap layout, row-unit scroll/hit-test/Up-Down
  find.go replace.go          The one match scanner; replace current/all
  multiedit.go multicaret.go  Multi-range edit (one undo step); secondary carets
  decoration.go               Span/GutterMark overlay merged in Tab.Render
  wordhl.go symbolhl.go       Word highlight; server-resolved symbol uses
  linenote.go bracket.go      End-of-line notes (inlay hints); brace matcher
  bookmark.go                 Line bookmarks: content-diff re-anchoring, gutter flag
  jumpmargin.go               Context margin a jump lands with (MarkJump)
  ghost.go                    Ghost-text display form + render-row splice
  conflict.go lens.go         Conflict-marker blocks + resolvers; clickable end-of-line lens
internal/diff/diff.go         Patience line differ + unified rendering
internal/search/search.go     Project-wide text search over the finder index
internal/lsp/                 JSON-RPC client (LSP + ACP/ndjson), workspaceedit,
                              codeaction, callhierarchy, progress, types
internal/mcp/                 mcp.json inventory + MCP client
internal/skills/              SKILL.md inventory + frontmatter
internal/plugins/             plugin.json manifests; diag.go compiler-output parser
internal/chatstore/           One JSON file per saved conversation
internal/gonotes/             GoNotes REST client
internal/session/session.go   state.json: recent folders, per-folder tabs, layout
internal/history/             <repo>/.ced/history.bytdb: folder index (trie), file ring, bookmarks
internal/remote/remote.go     `ced --remote/--wait`: sockets, root-based discovery
internal/cats/                cats detection, control socket, event stream, hooks
internal/favorites/           favorites.json: two scopes, walk-up resolver
internal/filetree/            Lazy tree, identity-preserving refresh, marks; filter.go
internal/format/              format.json, trust store, builtin ladder, kinds.go,
                              inprocess.go (JSON), validate.go, jsonexplain.go
internal/theme/               Theme struct, palette derivation, builtins, loader
internal/userconfig/          ~/.config/ced/config.json + other config paths
internal/clipboard/           OSC 52 with tmux passthrough
internal/icons/               Nerd Font detection + glyphs
internal/app/
  app.go                      Event loop, layout, rendering
  inputburst.go               One frame per wheel/motion burst
  modal.go leader.go whichkey.go  Modal slot; leader table + namespaces; which-key band
  metakeys.go nav.go          ⌘ layer; back/forward history
  tabbar.go tablabel.go tabcontext.go tabgroups.go statusbar.go
  toolwindow.go tooladapt.go toolheader.go toollayout.go toolmenu.go  Tool windows
  splitter.go treeautofit.go treefilter.go treemarks.go overflow.go
  find.go findall.go projectsearch.go goto.go bracket.go
  markdown.go softwrap.go wordhl.go multicaret.go
  lsp*.go hovermodal.go diagtip.go diagnote.go diagmerge.go
  workspaceedit.go
  copilot*.go chatcomposer.go chatagent.go chatarchive.go summarize.go gonotes.go
  mcp.go skills.go plugins.go plugincmd.go plugindeco.go
  git*.go compare.go           Git panel/log/commit/receipt/status; compare panel
  gitrestore.go               Restore one file to HEAD (tree/tab/≡ Git rows)
  gitconflict.go gitopstate.go  Conflict picker + stop hook; parked-op / unmerged reads
  conflictview.go conflictpanel.go  In-editor conflict washes/lens/verbs; Conflicts tool window
  cherrypick.go               Multi-commit cherry-pick dialog
  hunktip.go                  Change-bar popup: a hunk's diff, scrollable
  terminal.go termdiag.go runexec.go openineditor.go
  autosave.go format.go validate.go syntax.go zipops.go
  copypaste.go copyto.go      File clipboard Copy/Paste; Copy to… a typed folder (one engine)
  folder.go favorites.go favmanage.go recentlocations.go remote.go
  bookmarks.go                Project-wide bookmarks: park/adopt, verbs, persistence
  cats_glue.go hostident.go theme.go
```

## Conventions

### File headers
Every new source file gets the header block (file name, author, created
date, copyright year — see existing files). Copyright year = current
year (2026). This is a fork of Cloudmanic's SpiceEdit: files still
saying `Author: Spicer Matthews` stay as they are until substantially
reworked (~half the lines or 200+ changed), then flip `Author:` to the
maintainer and add `Portions copyright 2026 Cloudmanic, LLC. Original
author: Spicer Matthews.` New files get a plain maintainer header.
**Never touch `LICENSE`.**

### Comments
- A short doc comment above every function, public and private.
- Favor "why" notes over "what" inside functions.

### Tests — required
- **Every source file gets a `_test.go` in the same package** (not
  `_test`), one test file per source file.
- Exported funcs: happy path + obvious failure. Non-trivial helpers:
  same. Bug fixes: a test that fails before the fix. Pure data: a smoke
  test.
- Each `Test*` gets a short doc comment (style: `internal/app/fileops_test.go`).
- `t.TempDir()` for filesystem state; never write into the repo or /tmp.
- UI code: `tcell.NewSimulationScreen("UTF-8")` + `scr.GetContents()`.
- `t.Skip` only for unsatisfiable environment needs, never for flakiness.
- `make test` (race detector) / `make coverage`. CI does NOT actually run
  on this fork (see Releases) — run tests locally.
- **newTestApp stubs every host side effect** (LSP/Copilot/chat dead,
  `lspLookPath`/`chatLookPath` never-found, builtin formatters nil,
  plugin/skill/theme/chat-archive/history/session/favorites paths at temp
  dirs, `pluginShell` refusing, `editorEnv` empty, `gonotesCreate`
  refusing, rc.grsh disabled, tree auto-fit off). New integrations that
  touch the machine need a package-var seam pinned there. Tests build
  `App` directly, so menu sections start expanded and inlay hints off.

### Commits
- `Co-Authored-By: Claude …` trailers are fine. No "Generated with
  Claude Code" footer.
- Don't ask for commit-message approval — commit directly when asked.

## Cross-cutting rules

- **Events only.** Goroutines never mutate UI state; they post custom
  tcell events handled on the main loop. Blocking server→client requests
  post an event with a buffered reply channel and wait.
- **Generation-check async results** (seq/gen counters) for anything
  that opens a panel or writes files; drop stale answers. Anything that
  merely moves a cursor checks path/EditRev.
- **Silent degradation** for every integration (LSP, formatters,
  Copilot, MCP, plugins, cats, themes): missing binary / crash → the
  editor works, no nagging, no auto-restart; a ≡ row is the retry.
  Startup errors are HELD for a ≡ label, not flashed.
- **Nothing spawns or runs at startup** that the user didn't trigger.
- **A menu row that is unavailable explains itself** (flash the reason)
  rather than dimming or hiding, when the fix is something the user can
  do (install a binary, export $EDITOR, create favorites.json).
- **Choose-one-from-a-list UIs reuse `openPicker`** (palette). Only
  exceptions: the Find-all list (a live-preview peek) and the search
  history dropdown (it fills a field the modal slot would tear down).
- **Single modal slot** (`App.modal`, `openModal`). Implement the
  `modal` interface; button geometry in ONE method returning `btnRect`s
  used by both draw and hit-test; single-line input = `textField`. Don't
  add per-modal fields to App. `openModal` replaces rather than refuses,
  so unprompted arrivals must DECLINE an occupied slot.
- After any workspace mutation call `a.workspaceChanged()`.
- **Read the open tab's BUFFER before disk** whenever text is sent
  somewhere (chat, notes, compare, reference context, workspace edits).
- **Tab mutations must bump `EditRev`** and should go through
  `InvalidateStyles` (see syntax below).
- **Standing timers only while something needs them** (caret blink,
  syntax settle, validate, plugin edit debounce) — the loop is
  event-driven; never wake an idle editor forever.
- **Esc side effects never consume the key** (ghost text, carets, tree
  filter, compare-paste arm, project-find tints) so `Esc s` still saves.
- **Leader keys**: the flat table is out of mnemonic letters — new verbs
  get a ≡ row (palette comes free), no leader. `Esc [`, `Esc ]`, `P`,
  `N`, `\`, `^`, `_`, `#` CANNOT be bound (terminal escape introducers).
  Menu `shortcut` hints are display-only — rebinding a key means updating
  both leader table and hint.
- **Caps are announced** (in the title / last row): a silently short list
  reads as "that's all".
- **Paths**: absolute everywhere (grsh's `cd` moves the process cwd);
  confinement checks run after `EvalSymlinks` on both sides
  (`resolveInRoot`, `pathInside`).

## Design rules by feature

### Editor core
- **`cursorMoved` (tab.go)**: every cursor mutator sets it; Render
  consumes it. Never call `EnsureVisible` unconditionally. Restores
  (`RestoreView`, `CenterOnCursor`) must CLEAR it.
- **Deferred syntax (editor/syntax.go, app/syntax.go)**: intra-line edits
  (typing, backspace, delete, same-line replace) DEFER and patch the grid
  (`stylesAfterInsert/Delete`); structural edits (Enter, multi-line paste,
  undo, line ops, reload, theme switch) call `InvalidateStyles`.
  `InvalidateStyles` is the DEFAULT for any new mutation path. Over
  `MaxHighlightBytes` (512KB) → `SyntaxOff`. `SyntaxSettle` is a package
  var for tests only.
- **Undo (undo.go)**: capped by BYTES (`maxUndoBytes` 32MB, both stacks);
  `snapshotCost` = headers + changed lines, measured against the entry
  below, stamped once. Trimming never empties the stack. `pushUndoEntry`
  is the single write path; keep the running sums exact. Only the
  multi-caret fan-out may set `undoSuppress`. One-step multi-range edits
  use `pushUndo(undoGroupStructural)` + direct Buffer edits (ReplaceAll,
  ApplyMultiEdit, workspace edits, `ReloadAsEdit`).
- **Three Reload methods, don't collapse them**: `Reload` resets history
  (external writer, via `ReloadUndoable`); `ReloadAsEdit` adopts ced's
  own rewrite (formatter, plugin) as one undo step.
- **File IO (fileio.go)**: guards (size on stat, NUL in first 8KB)
  BEFORE reading. Buffer is always bare LF; LineEnding/BOM re-emitted on
  write. Saves: temp file in TARGET dir + rename, symlinks resolved,
  mode copied; read-only dir falls back to in-place. `WriteFileAtomic`
  is exported for other writers — don't copy it.
- **Scroll clamp** allows overscroll (`max(viewH/2, 3)`) — intentional.
- **Jump margin (jumpmargin.go)**: a JUMP (find hit via
  `FocusCurrentMatch`, `lspJumpTo`, nav retrace, goto, the peek/list
  landings) calls `MarkJump`, and Render reveals with `JumpMargin` (5,
  capped at viewH/4) rows of context instead of the minimal scroll.
  Plain motion and clicks stay minimal. One-shot, cleared with
  `cursorMoved`; never pads past EOF into overscroll.
- **One frame per input burst (inputburst.go)**: wheel/motion bursts
  defer frames only while more input is queued, capped at ~30fps. Don't
  restore the unconditional per-event draw.
- **Decoration layer**: anything painted over code is a
  `DecorationSource` (Spans + GutterMarks), never a new branch in
  Render's paint loop. Precedence: syntax < external < word-hl <
  bracket < selection < find. Gutter precedence: git < validate < plugin
  < LSP (glyphs: validate `◇`, plugin `◆`, LSP `●`). Exceptions that are
  PAINTED not decorated: ghost text, secondary carets, end-of-line notes,
  the bookmark line-number flag. Two optional source interfaces extend a
  DecorationSource: `LineWashSource` (whole-row bg, gutter and past-EOL
  included; replaces the caret-line highlight, later source wins) and
  `LensSource` (clickable end-of-line buttons: full → short labels → shed
  from the right, never cut; outranks both notes on its line; Render
  STAMPS `lensHits`, `Tab.LensAt` reads them — reset at Render's top).
- **Identity-preserving tree refresh**: `reload` keeps survivor `*Node`s
  and their `Expanded` state.
- **External-change reconcile** (each tree tick): clean + changed →
  silent reload; dirty + changed → warn; deleted → `DiskGone`.
  A formatter run in flight suppresses it (`formatRunBegin/End`, per-path
  count).

### Tabs, labels, status bar
- `tabScroll` is DERIVED each frame (`ensureActiveTabVisible`, including
  pull-back). A tab that doesn't fit gets no rect (only the active tab on
  a too-narrow strip). `switchToTab` is the single place a switch records
  nav history and flushes auto-save. `+N` button opens the switcher.
- Tab switching leaders: `Esc ,` / `Esc .` / `Esc b`.
- Tab right-click menu (tabcontext.go): `editorContextModal` chassis,
  acts on the CLICKED tab (rows capture `*editor.Tab`, resolved via
  `tabIndexOf`). Rows that answer with a VIEW or a buffer write
  (compare with clipboard, Preview, Format, Validate) go through
  `onTab` (switchToTab first); the rest act in place. Order: Reveal,
  uncommitted changes (`gitPanelRevealFile`), git history
  (`gitLogShowFile`, `p:` filter, field NOT focused), compare, Preview
  (markdown only), Format, Validate (`validateFile`), Restore (`restoreFile`),
  Move/Copy to split → (only when `InCats`, like the ≡ Cats group), Add to group…,
  Remove from group <name> (grouped tabs only), Zip, copy paths,
  Close tab, Close other tabs (KEEPS dirty tabs). Rows call the same
  verbs as their ≡ twins: Nav "Reveal file in tree", File "Validate
  file" / "Close other tabs", Git "Show file's uncommitted changes" /
  "Show file's git history" / "Restore file (discard changes)…", Cats "Move to split →" ("Open in split →"
  is Copy). Splits (`catsSplitTab`) SAVE a dirty tab first; Move closes
  the tab only on the host's answer (`catsKindSplitMoved`) and keeps it
  if edited meanwhile. `closeTab` decrements `activeTab` for a tab
  closed to its left (`TestCloseTab_LeftOfActiveKeepsActive`).
- **Tab groups (tabgroups.go)**: name ≤4 letters/digits, unique
  case-insensitively. FOLDER group = a rule (every open file under it,
  `exclude` for hand removals); AD-HOC = `members`. Membership keyed by
  `*editor.Tab`, resolved by `tabGroupOf` (ad-hoc first, then deepest
  folder), never stored on the tab; `tabGroupsForgetTab` in closeTab
  (an empty ad-hoc group goes with its last tab, folder groups stay).
  Contiguity = `arrangeTabGroups` REORDERS `a.tabs` (cluster at first
  member, stable, no-op with no groups) at the top of `layoutTabs`.
  The chip lives inside its tab's slot (`tabSlotWidth(i, first)`), so
  `tabScroll` stays a tab index; the first DRAWN member carries it.
  Collapse never hides the active tab; folded tabs get zero-width rects
  so `+N` counts only scrolled-off tabs. Members get a PLAIN underline
  (no colour: the colon-form SGR 58 is emitted nowhere in ced). Chip
  colours come from the syntax palette (read at paint time). Chip left
  click folds, right click = group menu (`tabGroupContextItems`, shared
  with the ≡ File "Tab groups…" picker); ≡ File "Add tab to group…" is
  the tab row's twin. Persisted in `session.Entry.Groups` by path;
  `restoreTabGroups` is DEFERRED in `restoreSession` so it runs after
  the tabs, and still runs on the no-tabs return.
- Labels are the basename until another OPEN tab collides, then grow by
  directory segments per colliding group; cache keyed by the list of
  open paths. `tabWidth` measures the label; icon keys off the real name.
- Status bar path: project-relative (absolute outside root), truncated
  from the FRONT, budgeted so Ln/Col survive; `⧉` copies the absolute
  path and is drawn even for root-level files.

### Multi-caret (editor/multicaret.go, app/multicaret.go)
- Primary is `Tab.Cursor/Anchor`; secondaries in `Tab.Carets`. Don't
  merge into one slice.
- Fan-out (`applyAtCarets`) swaps each caret in and runs the unexported
  single-caret core; a core must never call an exported sibling.
  Bottom-up order always. One structural undo snapshot + `undoSuppress`.
  Undo/redo drop carets.
- Explicit jumps drop carets; arrows/Home/End move all. Alt+click adds a
  caret, no drag. New carets are promoted to primary; `caretGoalCol`
  prevents column drift.
- Secondary carets are painted (`paintCarets`) and blink on ced's own
  ticker, armed only while carets exist; `stopCaretBlink` restores
  the on-phase. Not SGR blink.
- Whole-line ops collapse the set (`dropCaretsForLineOp`).
- Leaders: Esc-m / Esc-M / Esc-* / Esc-&.

### Ambient highlights
- **Word highlight (wordhl.go)**: DecorationSource running first;
  `word-highlight` is a NEUTRAL box (26% fg over bg) + bold — the blue
  fill belongs to selection only. Window-scoped scan by design.
  Case-sensitive whole-word; `caretQuery` decides whole-word from the
  range. Quiet for lone matches, punctuation, one-rune selections,
  multi-caret. `applyWordHighlight` is the single write path.
- **Symbol uses (symbolhl.go, lsphighlight.go)**: a VERB (≡ Code), never
  on caret move; replaces the word highlight while live; dies with
  `EditRev`; writes underlined; Esc clears.
- **Brace matching (bracket.go)**: always on, no toggle. Skips brackets
  whose grid color is syn-string/syn-comment (degrades to "code" with no
  grid, uncovered row, or theme where syn-string == fg). Scan is budgeted
  (`bracketScanLines`); running out is `Conclusive=false` and paints
  nothing — distinct from unmatched. `bracket-match` 42% neutral + bold;
  unmatched = `err` FOREGROUND. Only `()[]{}`. Caret ON or just after;
  ON wins. Quiet in multi-caret. Leader `Esc %` (≡ Nav).
  `TestStyleForToken_LiteralFamilySplitsStringsFromNumbers` pins the
  Literal SubCategory fix it depends on.
- **Inlay hints (linenote.go, lspinlay.go)**: END OF LINE ONLY, never
  mid-line (`TestLineNote_CostsNoGeometry`). Note restates its anchor
  (`inlayNote`); dropped not squeezed; one cell short of the pane; dies
  with the revision. No own timer: `inlayAfterEvent` asks once per
  (path, rev), recorded BEFORE the answer. Error → `noInlay`; progress
  end clears the record. gopls needs `initOptions`
  (`TestInlay_EndToEndWithRealGopls`). ≡ View toggle `"inlayhints"`.

### Markdown preview (editor/markdown*.go, app/markdown.go)
- A VIEW flag (`Tab.mdView`), not a Mode: buffer, undo, dirty, LSP etc.
  keep running (`TestSetMarkdownView_LeavesTheBufferAlone`).
- `MDScroll` is its own counter. Rows derived + memoized on
  (EditRev, width); theme switch must `InvalidateMarkdown` (via
  `restyleTabs`). `MarkdownRows` is shared by draw and hit-test; every
  row names its source line (-1 = nearest above).
- Keys swallowed except navigation. Prose word-wraps, code hard-wraps
  with tabs expanded; fences highlighted via one `HighlightLang` pass.
  `md-code-bg` is a derived key (`TestDerive_MDCodeBGIsVisiblyOffTheBackground`).
- No config key. Leader Esc-v; ≡ View row below the terminal rows.
  Each context menu carries only ONE of Preview/Stop Preview (tree:
  keyed by the clicked file via `previewingPath`; previewed editor pane
  gets `openPreviewContext`).

### Soft wrap (editor/softwrap.go, app/softwrap.go)
- Per-tab view flag, rides the session (`TabState.Wrap`); restore calls
  `SetSoftWrap` BEFORE `RestoreView`.
- `ScrollY` stays a LINE index. ONE layout (`wrapLayout`) feeds Render,
  HitTest, PosScreenCell, EnsureVisible, Up/Down, etc. **Any screen-row →
  line mapping must ask `HitTest`**, never `ScrollY + row`.
- `wrapW` cached from last render. Rows wrap one cell short of the pane.
  Up/Down step by screen row when wrapped. No leader.
- The scroll CEILING counts rows when wrapped (`MaxScroll` →
  `maxScrollWrapped`, same overscroll as unwrapped); a line-count
  ceiling clamps ScrollY back above the caret on files of few long lines
  (`TestSoftWrap_CaretBelowLineCeilingStaysOnScreen`).

### Find, replace, go to line (editor/find.go, replace.go, app/find.go, goto.go)
- ONE scanner (`matchCols`); case folding per rune (`foldRunes`).
- Options live on App (`findCase`/`findWord`), pushed via
  `applyFindOptions`, not persisted.
- Bar height: always ask `findBarRows()`. Inputs are `textField`. In-bar
  Alt chords + clickable `Aa` / `|W|` + ≡ rows.
- Replace = one undo step; ReplaceAll re-scans and goes bottom-up via
  `ApplyMultiEdit`; ReplaceCurrent advances past what it wrote.
- Go to line clamps and parses `file:line:col`. Leaders: Esc-j goto,
  Esc-e replace.

### Search history (history/searches.go, app/searchhistory.go)
- Lists per KIND (`find` shared by find bar / Find all / Find in
  project; `replace`; `symbol`; plus `copyto` — Copy to…'s folders, the
  one kind that is not a search), MRU, exact dedupe, `MaxSearches` 25,
  in `.ced/history.bytdb` (`search_history`, same add-don't-overwrite
  write as recent files). Recorded where a search RUNS (`showFindAll`,
  `startProjectSearch`, `startWorkspaceSymbols`, find bar Enter/close,
  successful replaces), never in the prompt.
- ONE dropdown (`histDrop`, geometry only via `histDropGeom`): Up or
  the ▾ opens; arrows move visually; Enter/Tab/click FILLS, never
  submits; Delete/× forgets; any other key closes and is typed. Find bar
  = `Find▾`/`Repl▾` labels, list above the whole bar; prompts =
  `openSearchPrompt`; Find-all replace box = `⇄▾` (`replHist`, list
  below, pinned route via `findAllPinHistMouse` / overlay draw). FILTER
  boxes get no history. A pick must be told its field — the drop has
  already zeroed itself. A press on the owner's button is the toggle
  (`histDropMouse`'s `toggle`, consumed) — handed on, it would reopen.

### Find-all list + project search (app/findall.go, projectsearch.go, internal/search)
- A PEEK, not a picker: moving the highlight moves the cursor live, Esc
  restores via `RestoreView`, other dismissals accept.
- Takes rows/columns OUT of the editor (`editorBandRows` → `editorRect`),
  never floats. TOP dock (default) or RIGHT dock (`"findalldock"`,
  switched by title button, `d`, or ≡ View). **No call site may assume
  the editor starts at row 1 or runs to its band's right edge.**
- Bottom border is its resize handle; `findAllRows` not persisted;
  drag continued in two places (`findAllDragMode`).
- Preview CENTERS an off-screen hit, leaves an on-screen one alone.
  Borrowed find state returned on exit. Rows compacted at open
  (indent trimmed, tabs → one space).
- Filter box seeded with the query; the seed is INERT until edited.
- Opening: only a single-line selection searches silently; otherwise
  prompt pre-filled (`findAllPromptSeed`), seed read BEFORE `openPrompt`.
- Both lists match under the bar's `Aa`/`|W|` (`a.findOptions()`),
  SNAPSHOTTED on `findAllModal.opts` at open (⟳, stale check, replace
  plan, tints reuse it); non-default options named in the title
  (`findOptionsNote`). Staleness = `editor.MatchesAt`, never a byte compare.
- Project search: pure Go over the finder index, capped with the cap in
  the title, matcher = `editor.FindAllOpts` (`search.Options` embeds
  `editor.FindOptions`). Project mode does NOT preview on
  keyboard walk; a single click jumps and keeps the list; tints recorded
  in `projFindTints` and removed by `clearProjectFindTints`. Always
  prompts (seeded). Labels truncate from the front. Leader Esc-P.
- Non-search producers (references, locations, workspace-edit receipt)
  may change ONLY `findAllModal.heading`.

### LSP (internal/lsp, app/lsp*.go)
- Hand-rolled JSON-RPC; no LSP framework dependency.
- Several servers, ONE PER FILE by extension (lspservers.go; extensions
  disjoint). Installing the binary is the opt-in. Every verb gets its
  connection from `a.lspClientFor(path)`. Degradation is per server;
  `lspState.dead` is the global switch.
- ≡ Code "Restart language server" is the retry; `lspServer.gen` drops
  stale ready/exit events; `lspDropServer` is the shared teardown.
- Sync via `EditRev` vs `syncedRev`, 300ms debounce; saves flush before
  didSave. Absolute paths only (root and tab paths).
- Handshake declares workspaceEdit with `documentChanges: true` and EMPTY
  `resourceOperations`, `workDoneProgress`, `codeActionLiteralSupport`;
  deliberately NOT `resolveSupport`, `prepareSupport`, `linkSupport`.
  Hover `contentFormat` is MARKDOWN first; signature help and completion
  docs stay plaintext first (`TestInitialize_PrefersMarkdownHover`).
- `onRequest` hook is narrow: return `lsp.ErrRequestUnhandled` for
  methods it doesn't own (gopls blocks on `workspace/configuration`).
  `StartWithRequests` installs the hook before the read loop.
- Go to definition at the declaration flips to references
  (`definitionIsHere`). ⌘+click = definition (`isMetaClick`: cats sends
  ⌘ on mouse as CTRL+ALT); plain Ctrl+click left unbound.
- Protocol shape unions collapse in `internal/lsp`, discriminated by a
  FIELD/JSON type, never by a failed unmarshal.
- Leaders: Esc-d definition, Esc-i hover, Esc-I signature, Esc-D
  symbols, Esc-R references, Esc-c code actions, Esc-E rename.
  Implementation / type definition / incoming calls / workspace symbols
  are ≡ Code rows only.
- Tests: `a.lsp.dead = true` + `lspLookPath` pinned; inject
  `fakeLSPConn` via `a.lspInstall(lspGoServerID, fake)`;
  `useRealLSPBinaries` for the two real-gopls tests.
- Verb specifics:
  - Symbols (Esc-D): a picker, not a palette source; kind word LAST in
    the label; jump to `selectionRange`.
  - Workspace symbols: prompts first; asks every READY server, never
    spawns; gated on `lspAnyReady`.
  - Implementation/type def/incoming calls share `Locations(method)`;
    one result jumps (`lspJumpTo`), several list
    (`openLocationsPanel`); incoming calls always list.
  - References: generation-checked (`refSeq`); context read off-loop,
    reconciled on-loop preferring open buffers; sorted before capped;
    `includeDeclaration` true; 30s timeout.
  - Hover / every tooltip box: lines WRAP (`tooltipLayout`, shared by
    measure and paint), the box grows DOWN, never wider than
    `hoverModalMaxWidth`; too tall for either side of the anchor →
    `tooltipPlace` shortens it and the painter ends in `…`.
    `hoverReflow` re-joins prose soft breaks. The kind rides the event
    (`lsp.Hover.Markup`): markdown gets `---` → blank, blank runs
    collapsed, non-code lines through `editor.MarkdownInlineText` (the
    preview's scanner, not a second one), a lone http(s) link shown as
    its URL; plaintext keeps the guesses (first line is the header,
    `{`/`}`/`;` ends never join). `hoverLines` caps in wrapped ROWS (16).
  - Signature help: MANUAL only (a modal would eat keystrokes); label
    hard-wrapped for exact offsets; active param's doc first.
  - Progress: status-bar segment, token SET, `lspLoadingNote` on empty
    answers; only error/warning `showMessage` flashes.
  - Rename: `startRename` captures position at prompt open, re-checks
    EditRev at submit, `captureWSRequest` AFTER the flush; old name never
    sent; only refuses unchanged name / whitespace.
  - Code actions: range = selection or cursor; diagnostics echoed
    VERBATIM (raw JSON round-trip); disabled actions dropped; edit before
    command, refused edit skips command. Server `workspace/applyEdit`
    refuses while a dialog owns the screen; handler blocks
    (`wsApplyTimeout` 90s < `executeCommandTimeout` 2m).

### Workspace edits (lsp/workspaceedit.go, editor/multiedit.go, app/workspaceedit.go)
- Opens NO tabs: non-open files go through a detached `editor.Tab` +
  `Tab.Save`; open files are edited in their buffer and NOT saved.
  Receipt = Find-all project mode (`reportWorkspaceEdit`).
- Validate everything (`planWorkspaceEdit`, on the main loop), then
  apply: buffers first, then disk in path order, rollback on failure.
- Undo journal: one slot above per-tab stacks, validated by `EditRev`
  AND `UndoDepth` (+ mtime for detached). Plain undo claims the group,
  or degrades loudly and clears the slot; `closeTab` drops it.
- `documentChanges` wins over `changes`; `changes` sorted by path.
  Resource ops refused by name. `ClampEnd` for exclusive ends;
  overlapping edits refuse.
- `applyServerEdit` = acceptance; `applyServerEditWith` = outcome,
  `done` fires exactly once on every path.

### Diagnostics (app/diagmerge.go, diagtip.go)
- `a.diagsFor(path)` merges LSP → plugins (sorted keys) → ced's own, as
  `lsp.Diagnostic`; synthetic columns re-encoded to UTF-16 (`lspPosFor`).
  Plugin kill switch honoured here. `diagsForRange` stays LSP-only
  (`TestDiagsForRange_StaysOnLSPOnly`). Esc-i answers with diagnostics
  alone when there is no server.
- Tooltip: passive, reads the cache, arms only on a real diagnostic;
  gutter answers by line, code by rune (PosScreenCell round-trip).
  Gutter click toggles it (`diagGutterPress`, after `blameColumnPress`).
  Messages wrapped, capped.
- Caret-line note (diagnote.go): the WORST diagnostic at the caret
  (`diagsAtCaret`, shared with Esc-i) painted end-of-line in severity
  colour, `(+N more)`; caret line ONLY; replaces that line's inlay note;
  stamped every frame before Render (`Tab.SetCaretNote`, rev + caret
  guarded). The no-motion-terminal door: clicking the underline shows it.

### Change-bar popup (app/hunktip.go, gitdiff.go)
- Text = the gutter's own `-U0` diff: `diffHunk.Body` keeps each hunk's
  `-`/`+`/`\` lines (collected only after an accepted header), so no
  fork on hover. `diffHunk` is no longer comparable — tests compare
  `hunkPos`.
- Target = the MARK cell on a line's FIRST screen row; refused where the
  cell shows a diagnostic dot (`diagsAtCell` non-empty). Dwell
  (`diagTipDelay`) + click door (`hunkGutterPress`, after
  `diagGutterPress`; second click closes via `pressClosed`).
- The one ENTERABLE passive popup: `noteHunkTipPointer` runs FIRST among
  the pointer hooks and claims motion/wheel inside its box (keeps the
  dwell layers off the code under it). Wheel inside scrolls the box
  only, even when it fits; wheel elsewhere closes and falls through.
  Keys, presses, resize, `openModal` close it; draw hides it for another
  tab (`hunkTip.path`).
- Code clipped (`clipRunes`), never wrapped; tabs → 4 cells; body capped
  at `hunkTipMaxRows` then scrolls, announced by ▴/▾ on its own border
  and "a–b of n" in the bottom border. NOT an overflow-marker surface.

### Data formats (internal/format/kinds.go, inprocess.go, validate.go, app/validate.go)
- `format.kindFor` is the ONE table for formatter + in-process pass +
  validator (`TestKindFor_AgreesWithTheThreeVerbs`). JSONC carved out by
  name (tsconfig, jsconfig, .eslintrc.json, `.vscode/`).
- Builtin ladder: repo-local tool → global tool → in-process pass.
  External commands must rewrite IN PLACE (no jq). JSON tools don't chain.
- In-process JSON: `json.Indent` on trimmed bytes, never Marshal.
  Unparseable → not rewritten; unchanged → not rewritten; empty file is
  not an error. Adopted via `formatDoneEvent` → `ReloadAsEdit`.
- Validation reads the BUFFER; formatting reads DISK. Findings die with
  the revision (`liveProblems`). 400ms debounce armed only for validated
  kinds; parse once at open. One problem reported; columns in runes,
  offset-1. Go deliberately not validated (gopls does it).
- JSON messages are EXPLAINED (format/jsonexplain.go): keyed on the
  parser's error class + rejected byte + previous non-blank byte, the
  mark MOVES to the mistake (trailing comma → the comma; missing comma
  → end of the previous value; EOF → the unclosed opener). Unrecognised
  shapes keep the parser's wording and position.
- **Only Go formats on save** (`format.FormatsOnSave`). Everything else
  (JSON, non-Go `format.json` entries) runs only via ≡ File → "Format
  file" (`formatActiveFile`: saves a dirty tab first; a Go save already
  formatted, so it stops there). Both doors share `runFormatter`.
- Formatter order: project `format.json` (trust-gated) → builtin
  external (`BuiltinCommandsFor(root, path)`) → in-process → install
  offer. Go: goimports, else `gopls imports -w` + `gofmt -w`, else gofmt.
  `quiet=true` (auto-save) never prompts or flashes.
- Documents keep their indentation (`format.DetectIndent`, indent.go):
  narrowest space run or tabs by vote; no evidence → tool default. JSON
  tools get it as flags unless the repo configures the tool (prettier
  `--config-precedence prefer-file`; biome/deno skip on root config).

### Auto-save (app/autosave.go)
- Debounce on the sum of EditRevs; default ON; silent; quiet format;
  defers while a modal/menu is open; skips tabs changed on disk.
- `"autosavedelay"` (default 5s, clamped [500ms, 5m]), no ≡ row; read
  via `autoSaveInterval()`.
- Focus-out and tab switch flush through `autoSaveTabIfEligible`. Focus
  is best-effort; the timer is the backstop. Never call `When()` on
  `*tcell.EventFocus`.
- `userconfig.Save*` round-trips unknown keys — never replace with a
  struct marshal.

### AI: Copilot, chat, context (app/copilot*.go, chat*.go, lsp/acp.go)
- Copilot sidecar = `copilot-language-server` over the same `internal/lsp`
  client; `"copilot"` opt-out; device-flow sign-in via
  `CallWithTimeout`; `editorInfo`/`editorPluginInfo` required. Host side
  effects via `copilotCopyCode`/`copilotOpenBrowser`. Menu rows stay
  clickable and flash why.
- Ghost text: NOT a DecorationSource — spliced into the cursor row after
  decoration merge; first line inline, `⋯+N` for the rest. Lazy doc
  sync; only EditRev movement arms the 300ms debounce; responses
  validated on (path, EditRev, cursor, reqSeq). Accept = select +
  InsertString of full InsertText. `"suggestions"` separate opt-out.
- Chat = ACP over `internal/lsp` (ndjson); no ACP SDK. Agent registry
  (chatagent.go: Copilot, Claude Code, Gemini), ONE panel, switchable;
  re-picking the agent is the retry. `"chatagent"`, `"chatmodel"`
  persisted, stale ids skipped silently. `connSeq` on every teardown.
- Chat is a TOOL WINDOW, docked RIGHT by default. Turns via
  `session/prompt` with `CallWithTimeout`; queued first prompt.
- Transcript is the model, rows derived (`chatRows`). Composer is the
  multi-line `chatComposer` (chat only; everything else uses
  `textField`): Enter sends, Alt/Shift+Enter breaks; the legacy
  ESC-CR fold ('m' + ModAlt|ModCtrl) is rewritten to Alt+Enter in
  handleKey BEFORE leaders. Composer hard-wraps.
- Focused chat owns paste (`chatPasteTarget` vs `editorPasteTarget`,
  mutually exclusive). Selection/copy in derived-row space; copy buttons
  are derived rows (`chatActionRect`).
- Permissions: hooks run per-request goroutines and block; every request
  answered exactly once (pick / reject / cancelled via
  `chatFlushPermissions`); queued, never steals the modal slot.
  `"chatwrite"` read-only mode enforced at handshake, fs handler, and
  auto-rejecting mutating kinds. fs is root-confined, reads buffers
  first, writes then `refreshTreeNow()`.
- Context attachments: PUSHED as embedded resources (no
  `resource_link`), per-turn not sticky, auto-context toggle
  `"chatcontext"` (selection beats file), buffer content, capped with the
  cut announced, fenced fallback when `embeddedContext` is false.
  Single-width markers.
- Archive (chatstore, chatarchive.go): New chat archives, empties AND
  restarts the session (`chatRestartSession`, never clears `dead`). No
  confirmations; both refuse mid-turn. Saved after every turn; one file
  per conversation (`archiveID`, nanosecond ids). Restored chats say the
  agent memory is gone. `chatRevealPanel` for reading. Leaders Esc-a-x /
  Esc-a-r.
- Summarize (Esc-a-z): `selectionOrFileTarget` (shared), `chatAttachOnce`,
  visible chat turn, prose prompt.
- Tests: `fakeCopilotConn`; `a.copilot.dead` / `a.chat.dead` true.

### MCP, skills, plugins, GoNotes
- **MCP** (internal/mcp, app/mcp.go): `~/.config/ced/mcp.json` in the
  Claude-Desktop shape. Declared to the agent in `session/new`; ced's own
  client (stdio only, over `lsp.StartNDJSON`) connects only on a ≡
  action. Generation-checked; stale ready events close their client.
  All pickers. `Describe()` shows env KEYS only.
- **Skills** (internal/skills, app/skills.go): read `~/.claude/skills`,
  `<project>/.claude/skills`, `~/.config/ced/skills`; precedence ced <
  user < project, shadowing in place. Pushed only on purpose as a
  `chatAttach` + `chatSkillDirective`. Never executed. Hand-parsed
  frontmatter — no YAML dep. Leader Esc-a-s.
- **Plugins** (internal/plugins, app/plugin*.go): JSON manifests of SHELL
  COMMANDS — ced never hosts code. Nothing runs at startup; `"plugins"`
  kill switch honoured at every surface. stdout = answer, stderr kept
  separate (decorations read both, ignore exit status). Output discarded
  if (path, EditRev) moved. Decorations keyed by (file, provider); empty
  result replaces. Compiler/grep output format. Esc-x = dynamic
  namespace (arms nothing when empty). Edit debounce 800ms. User-scoped
  only — project plugins would need format.json's trust store.
- **GoNotes** (internal/gonotes, app/gonotes.go, Esc-a-n): body is the
  selection VERBATIM; provenance in the description; tagged `ced`. HTTP
  only (bytdb is single-writer). Config via GoNotes' own env vars and
  shared token cache. One login retry. Failures open the info modal.
  Privacy chip on every note prompt (`alt+p`); ✦ draft (`alt+a`).
  `agentOneLine` shared with `commitSubject`.

### Git
- **Panel checkbox is a multi-selection tick, NOT staging.** Stage state
  is the XY porcelain code drawn verbatim. Verbs behind `Actions ▾`
  (openPicker); targets fall back to the highlighted row; ticks pruned on
  refresh; no-op rows omitted. Writes via `runGitCmd`; Discard/Delete
  confirm.
- **Restore (gitrestore.go)**: one verb `restoreFile` behind three
  doors — tree "Git restore…" (repo files only), tab menu, ≡ Git
  "Restore file (discard changes)…". `checkout HEAD --` (same as the
  panel's Discard: work tree AND index). Probes synchronously, confirms
  with the loss sized (numstat, staged, unsaved edits); untracked/new
  and clean files flash instead. An open tab adopts via
  `ReloadUndoable` (one Undo back); reconcile is held off the path with
  `formatRunBegin/End`, released on both outcomes.
- **Conflicts (editor/conflict.go, app/conflictview.go, conflictpanel.go,
  gitopstate.go)**: strict markers (exactly 7 chars + space/EOL; `=======`
  alone), unclosed openers are text; scan memoized per EditRev. Sides are
  CURRENT/INCOMING (positional — git's ours/theirs flips in a rebase).
  Resolve = one structural undo step, bottom-up for all-blocks; never
  stages. The view (washes + lens, `conflictSource` in wireTab) is GATED on
  `a.gitConflicted[path]` — marker fixtures stay quiet. Lens IDs carry
  `conflictLensTag`; the block is re-checked by its opener line at click.
  Panel = tool window `toolConflicts` (generic header), furniture: op line
  (Continue/Skip/Abort/⟳, right-aligned via `layoutButtonsRight`), sides
  line, file rows with state buttons. Op + files RE-DERIVED each refresh;
  only the "marked resolved since this stop" list is remembered (keyed by
  op + stopped commit). Open files' counts are read LIVE from the buffer
  (`conflictFileCount`) — a reload/undo/keystroke must never leave a
  stale row. Presence conflicts (DU/UD/AU/UA/DD) get Keep/Delete and never
  count as "ready". Staging saves via `saveForStaging` — NO format-on-save
  or plugin hooks (they'd race add→continue). The stop hook
  (`gitConflictFailHook`) raises the panel + opens the first block, and
  claims the event only when files are unmerged (else git's message
  modal). Continue/skip use the hook too (a continue can stop on the next
  commit); `gitConflictAfterStep` closes the panel when the op ends.
  `conflictPanel.unseen` is the cats "blocked" mark for a stop (the panel
  isn't a modal), cleared by the next key/click in handleEvent. Status bar
  ⚠ segment + tree error colour read the snapshot (`gitOp` rides the
  existing rev-parse via `--absolute-git-dir`). The picker stays as the
  keyboard door.
- **Cherry-pick dialog (cherrypick.go)**: `HEAD...src --right-only
  --cherry-mark --no-merges`, capped (announced); `=` rows shown dimmed and
  unpickable; ⚠ = touches a file HEAD changed since the merge base. Loaded
  off-loop, seq-checked. Applied OLDEST FIRST in one `git cherry-pick`;
  `-x` dropped with `--no-commit`. Refused (panel shown) while an op is
  parked. Alt chords a/x/s/b are the button twins.
- Commit of a selection stages first (`gitCommitFiles`, `runGitCmdSeq`);
  every commit goes through `gitCommitFiles`.
- Agent-drafted messages: a visible chat turn claimed by generation +
  transcript mark (`commitSuggestReq`); draft only pre-fills. Diff is
  `diff HEAD`, capped, includes untracked contents.
  `"commitmsgtrailer"` adds `Co-Authored-By` ONLY to drafted messages
  (`openCommitPromptDraft` vs `openCommitPrompt`). `promptModal.extras`
  = button row with Alt chords. Agent unavailability is a reason
  (`commitDraftBlockedReason`), not a hidden button.
- Commit receipt: passive layer, never takes the modal slot; dismissed by
  anything without consuming the key; reads `git log -1` via
  `gitCmdDoneEvent.onOK`. A successful push shows git's own output in
  the SAME panel ("Pushed", gitpushreceipt.go) via `onOKOutput` /
  `runGitCmdOKOutput`; both open through `openGitReceipt`, one replaces
  the other.
- Blame (gitblame.go, Esc-A): blames the BUFFER (`--contents -`). Two
  styles, `"blamestyle"` + ≡ Git row, both measured on every
  `fileBlame` (`newFileBlame`) so a switch never forks git: `bands`
  (default) = date + author on EVERY line over a per-commit
  `LineAnnotation.BG` band, hue keyed by hash, adjacent runs never share
  a hue, text lifted to 4.5:1 by `blameBandFG`; `compact` = hash · name ·
  age once per run. Switching style never turns the layer on. Author
  label = given name; surname letters only where two authors in the
  FILE share it (`disambiguateBlameAuthors`, in the parser) — no knob.
- Git log (Esc-L): tool window; `--all`, capped 400; Actions picker
  (only `reset --hard` confirms); selection kept by hash; refresh rides
  `refreshGitStatus`.
- Git status report (≡ Git + panel Actions): long format with
  `color.status=false`, `advice.statusHints=false`; capped to the window
  with the cut named; declines an occupied modal slot.

### Compare panel (internal/diff, app/compare.go)
- Active buffer is always the NEW side. Pure-Go patience diff; LCS only
  within `lcsCellBudget`. `SplitLines` adds no phantom line. Buffers over
  disk, except a file vs. itself (saved copy). Pasted text is a source
  (armed paste target). ⟳ re-reads via `compare.oldPath`. Tool window;
  its ≡ show row opens a picker. No leader.

### Tool windows + layout (app/toolwindow.go, tool*.go, splitter.go)
- Every panel (tree, git panels, conflicts, problems, compare, terminal, chat) is a
  tool window on one of three edges; ONE visible per edge
  (`claimDock`). The bottom edge spans the whole width; side docks stop
  above it (`dockSplitterHit` is row-aware).
- No button rail. Find bar hugs the editor, above the bottom dock.
- Tools lacking their own header get the generic one (`toolDef.ownHeader`);
  the tree hides its EXPLORER row (`HideLabel` moves the ROW MAP too).
  `toolRect` = whole dock, `toolBodyRect` = content. Header checked
  before the seam.
- Sizes per tool per axis, 0 = auto; width includes the splitter. Tree
  width reads through to `App.sidebarWidth`. `rawDockCols` avoids clamp
  recursion (`TestOppositeDocksDoNotRecurse`). Hidden tool → zero rect.
  `showTool` can refuse; callers read the answer.
- Nothing about layouts confirms. ≡ Tool windows = three rows + pickers.
  Find-all list and find bar are NOT tool windows.
- Layout persisted per project in `session.Entry.Layout`, sparse;
  restored through `showTool` silently; `clampToolSizes` refuses on a
  zero-size window (`TestApplyToolLayout_SurvivesAnUnsizedWindow`);
  `toolLayoutLoading` latch stops write-back during restore.
- `termDockLeft`, the tree flip, `"termdock"`, `"toolstripes"` are
  retired (still parse, ignored).
- Seams: grab zone = divider + column on its PANEL side (mirrors for
  right docks); drags carry `dragSplitOffset`; grip glyph on middle rows.
  Ceilings are the neighbour's reserve. `minSidebarWidth = 18`,
  `minEditorAfterDrag = 40`. A drag that moves the seam calls
  `lockTreeAutoFit`.

### File tree
- **Auto-fit (treeautofit.go)**: `autoFitSidebar` runs at the top of
  `draw` before any rect is read; grows only, floor
  `defaultSidebarWidth`, cap `autoFitMinEditor` (80) and a share of the
  band. Measurement shares `nodeRowSegments` with the renderer; measures
  all expanded rows. `"treeautofit"` default on (off in newTestApp).
- **Type-to-find (filter.go, treefilter.go)**: substring match per rune
  fold; no timeout; clears on Backspace-to-empty, Esc (side effect),
  focus loss. **No bare letter is a command in the focused tree** —
  only Space and `*` on an empty pattern. Tab/Shift-Tab cycle. Scope =
  expanded active folder else project, captured on first rune. Visible
  rows only. Paint = `FindMatch` + bold after the row.
- **Marks (treemarks.go)**: the tick borrows the row's leading blank cell
  (`TestMarks_TickCostsNoWidth`); keyed by PATH; `MarkedNodes` walks the
  loaded tree in order. Bulk marking only over visible rows;
  `MarkRange` only adds. Verbs reuse single-file paths
  (`doDeletePaths`, `createZipMulti`, file clipboard); archive entries
  relative to the common parent (`commonParentDir` by segment); paste
  plans reserve names (`uniquePastePathExcept`). Partial sets reported.
  No Discard row. Delete clears the set.
- **Copy to… (copyto.go)**: copy into a TYPED folder anywhere on disk
  (Paste only reaches folders the tree shows). Doors: tree right-click
  (root included), ≡ File "Copy file to…" / "Copy folder (…) to…"
  (project when no subfolder), marks picker "Copy N items to…". Shares
  Paste's engine: `planCopyInto` (plan before writing, names reserved,
  folder-into-itself refused after `resolveExisting` on both sides) →
  `runCopyPlan` → `pasteDoneEvent` (`into` set = Copy to… receipt naming
  the destination and any " as <new name>"). The destination is a
  FOLDER, never "copy as": an existing file is refused; a missing folder
  is created only after a confirm, and refusals run BEFORE that confirm.
  Never overwrites. Both verbs copy an open dirty tab's BUFFER
  (`dirtyBufferOverlay` → `Tab.EncodedBytes`), never saving it. Field
  seeded with the last destination (`history.CopyDestinations`, recorded
  when a copy runs, display form).

### Overflow markers (app/overflow.go)
- `▴`/`▾` in the LAST column of a viewport's first/last row on every
  scrolling surface; reserves no column; always drawn, no toggle
  (`"scrollbar"` retired, `TestLoadRetiredScrollbarKey`).
- Color = loudest thing off-screen: caret > find > error > warn > info;
  only OFF-screen items count; read from caches, not DecorationSources.
  Bookmarks are popup-only counts with no rank.
- `overflowMarkers()` is the ONE enumerator for draw, hit-test, popup.
  Keeps the cell's background. Drawn after all surfaces render; the
  unpinned Find-all list paints via `drawOverflowMarkersOverlay`.
- Click pages that way, double-click runs to the end
  (`overflowMarkerPress`; distances counted from `off.lines`;
  `App.overflowClick` claims the second press); gated on
  `dragMode == ""`. Counts floor at zero.
- Popup is passive (`overflowTipState`, not folded into
  `hoverDwellState`), 250ms delay, reuses the tooltip helpers. Units
  follow the surface. Auto-fit does not compensate for it.

### Terminal (app/terminal.go, termdiag.go, runexec.go)
- Embedded grsh REPL strip — NOT a PTY, no VT emulator. POSIX-only;
  don't add `windows` back to goreleaser (build-tag it out instead).
- Tool window, bottom by default; ≡ "Dock terminal left" is a preset
  that also opens it. Keep terminal rows near the top of ≡ View
  (`TestMenuLayout_TerminalRowsAboveTheFold`).
- Focus flag, not a modal; Esc stays global. Coalescing writer (one
  `termOutputEvent` in flight). ⏹ = SIGINT then Kill; aborts the paste
  queue.
- Paste: line break = Enter; lines submitted one at a time through
  `submitTermCommand` via `term.pasteQueue`. No per-rune replay, no `; `
  joining.
- `~/.config/ced/rc.grsh` sourced synchronously (`termRcPath`).
  Tests: `fakeTermEval`; only `TestTermRealGrshIntegration` runs `echo`.
- Clickable output: parsed by `plugins.ParseDiagnostic`; must name an
  existing regular file inside rootDir; relative to the SHELL's cwd;
  cached by cwd+path; `termLocSpan` measures the raw text; list newest
  command first. Esc-~ / ≡ Nav picker twin.
- Run executable: STAGES, never submits; `cd … &&` leads the line (grsh
  has no subshell), omitted when already there; directory picker reuses
  frecency sources; execute bit re-checked live.

### Bookmarks (editor/bookmark.go, app/bookmarks.go)
- Lines are re-derived from CONTENT, never shifted by edit hooks:
  the tab snapshots `Buffer.Lines` (a COPY — MoveLines rewrites in
  place) and on EditRev movement diffs prefix/suffix; inside the region
  the bookmark's text is searched, a net deletion takes an adjacent twin
  (`TestBookmarks_DeletingOneOfTwinLinesKeepsTheSurvivor`), an in-place
  edit never hops (`TestBookmarks_TypingOnABlankLineDoesNotHop`). Don't
  add per-primitive shift hooks.
- Two homes, never both: open tab owns them; `closeTab` parks by path
  (`a.bookmarks`), `wireTab` adopts (`SetBookmarks` re-anchors by text;
  blank lines never searched).
- Painted in the line NUMBER (Accent + bold, `⚑` in the blank leading
  cell), not the mark cell — a diagnostic would hide it.
- Doors: ≡ Nav rows (Toggle / Next / Previous / Bookmarks…), editor
  right-click row, DOUBLE-click on a line number (own `bookmarkClick`
  record, checked before `diagGutterPress`). No leader. Untitled tabs
  refuse. Next/Prev skip missing files; the picker labels and removes
  them. Clear all confirms.
- Labels: `Bookmark.Label` is payload, never a key (text re-anchors);
  every path that rebuilds a bookmark must carry it (remap, SetBookmarks,
  the normalize merge keeps a name). ≡ Nav "Label bookmark…" adds the
  bookmark if missing; clearing is the prompt's `[Clear name]` alt+c
  (prompts drop empty submits). Stored omitempty, 60 runes.
- Off-screen bookmarks are counted in the overflow POPUP only — never a
  marker colour (Accent means the caret).
- Persisted in `.ced/history.bytdb` (history/bookmarks.go), NOT
  state.json: table `bookmarks(path PK, marks JSON)`, ONE ROW PER FILE
  (a line is no stable key). STATE not deltas: `writeHistory` hands the
  whole set over (`SetBookmarks`) and only files whose set differs from
  the loaded/written one are upserted/deleted — unchanged files never
  written, so sibling instances keep each other's. Never-set history
  deletes nothing. Written through (`saveBookmarks` → `writeHistory`)
  and at Close; loaded regardless of the `"session"` toggle; gated by
  `history.Persists` like the rest. ≡ row carries "(not saved)". Cap
  `history.MaxBookmarks` (500) refused with a flash; text over
  `MaxBookmarkText` stored without it.

### Workspace, sessions, navigation
- **Nav history (nav.go)**: recorded centrally by openFile / switchToTab;
  `nav.suppress` during retrace; fresh navigation clears forward. LSP
  jumps record explicitly. Esc-o/O, Alt+←/→.
- **Open folder = restart** (folder.go): `nextRoot` + quit, main loops
  `New(newRoot)`; Close called explicitly. state.json (separate from
  config.json), order = recency, visit recorded at startup, tabs at
  Close, `session.Normalize` resolves symlinks. Restore checks file
  existence itself, degrades silently, uses `RestoreView`. Tabs wired by
  `wireTab`/`announceTab`. Bare `ced` opens cwd; `--last` for the last
  folder. Folder switch owes the unsaved-changes modal. Recent picker
  excludes the current root and prunes gone folders. `"session"` toggle.
- **Recent locations (internal/history, recentlocations.go)**:
  `<repo>/.ced/history.bytdb` (gitignored on first write, loading creates
  nothing), relative paths. Written only when `history.Persists(root)`:
  inside a git work tree (walks up; .git dir or file) or `.ced/` already
  there — else memory-only (`ced ~` leaves no `~/.ced/`). Gate asked at
  write time via `historyPersistsFn` (pinned true in newTestApp). Trie with max-of-subtree bounds, best-first
  top-k (`TestIndex_SearchMatchesBruteForce`). Eviction with hysteresis.
  DB opened briefly (load on first use, write on Close), retries on lock;
  writes ADD via deltas and re-issued sequences
  (`TestWrite_TwoInstancesAdd`). A write past `compactAbove` (256KB)
  ends with a VACUUM — btypedb's own auto-compact never fires on a
  briefly-opened small file. Spacers only in the unfiltered view.
  Unwritable history (read-only checkout, chmodded `.ced/`): probed by
  `history.WriteProblem` (self-undoing, never at startup — first menu /
  palette ask), HELD, shown as "(not saved)" on ≡ Nav Recent files /
  Recent locations; those rows then stay clickable and flash the reason.
  No fallback location (it would be a new dotfile). Not a repository is
  memory-only by design and never labelled.
- **Favorites (internal/favorites, favorites.go, favmanage.go)**:
  `ced fav <name>` REVEALS in the tree, never re-roots. Relative names;
  CLI walks up (each dir asked in full), the ≡ row resolves strictly in
  the open root (`ResolveIn`). Project scope shadows globals per name.
  `Clean` at write and read; `Resolve` re-checks after symlinks.
  Unbound vs bound-but-missing are different errors. Subcommand words
  refused as names. `Tree.Reveal` expands ancestors only; `RevealPath`
  opens the target and focuses the tree. Manage favorites: project list
  with a global drill-in, each list ends with its scoped Add row; scope
  chip is a closure (`alt+s`); rename is add-first; malformed file
  refuses writes.
- **Remote open (internal/remote, remote.go)**: discovery by project root
  (longest containing root wins); `ErrNoInstance` falls back to a local
  editor, a handler refusal is an error. Sockets per process under
  `$XDG_RUNTIME_DIR/ced` (never ~/.config), short paths. Waiters released
  exactly once (`releaseRemote`: tab close, Close, toggle off). Root
  re-checked on arrival. `"remote"` on/off/unavailable.
- **Open in $EDITOR (openineditor.go)**: $VISUAL beats $EDITOR, passed to
  a shell. Tier 1 runs it in a side-by-side cats pane (relative path);
  Tier 0 stages it in the terminal (absolute path). Row names the editor;
  always shown, refusal teaches. Sits above "Run in terminal…"
  (`TestTreeContextRunRowOnlyForExecutables`).
- **CLI (main.go)**: ONE parser — `parseArgs` runs the real `cli.App`
  into a `cliResult`. `actionDone` default. `helpText` is the help
  template (no backticks). Explicit `--version/-v/-V`; `OnUsageError`
  returns the message alone; no-op `ExitErrHandler`.

### cats host integration (internal/cats, app/cats_glue.go)
- Tier 0 is ced in any other terminal, not a degraded mode. **No feature
  may exist only at Tier 1** (`if a.catsTier1() { … } else { … }`).
- Tier 1 = `CATS_ENV=1` + `CATS_PANE_ID` + a socket that answers ping.
  Env sniff inline, probe on a goroutine.
- Never import the cats module — hand-copied wire structs.
- Stream reconnects forever with capped backoff; ONE json.Decoder for
  ack and events; Close interrupts a blocked read.
- "Blocked" = a question the user didn't ask for (`catsAsking(phrase)`
  after opening the modal). Report on CHANGE only; `catsAfterEvent` runs
  last. Hook seq seeded from the clock. Source is `"ced"`, never
  `"cats:ced"`.

### Themes (internal/theme, app/theme.go)
- Eight core keys; the rest derived via the ordered table in palette.go
  (new keys get a derivation). Palettes are `map[string]string`; specs
  keep the sparse palette.
- `theme.Default()` is a hardcoded literal
  (`TestBuiltin_TokyoNightMatchesDefault`).
- Switch = live restyle; `Tab.Styles` (and markdown rows) are the only
  color caches — anything new that caches colors must join
  `restyleTabs`.
- User themes shadow built-ins in place; "Customize theme…" writes a
  fully expanded `-custom` file and opens it — no color-picker modal.
  Picker keeps the current theme. ≡ View rows.
- When adding a color, check it against the background AND its
  neighbours — "ambient, keep it quiet" ships invisible colors.
  `conflict-incoming` is CHOSEN, not aliased (`conflictIncomingHue`): the
  first of accent-soft/warn/err/accent whose wash clears both the current
  wash and the separator grey (`TestDerive_ConflictWashesAreVisibleAndDistinct`).

### Menu (≡) and leaders
- Groups in `builtinMenuGroups`; `menuLayout` recomputes geometry every
  call; scrolled geometry only via `menuItemIndexAt` /
  `menuScrollOffset`.
- Top level: File · Edit · View · Find · Nav · Code · Git · AI · Tools,
  then headerless Quit. Sub-sections nest ONE level via
  `menuGroup.parent` (children follow the parent,
  `TestBuiltinMenuGroups_ChildrenFollowParent`); Tools stays the last
  parent before Quit. `openMenuAtSection` unfolds the parent first.
- Pinned top zone: command palette + expand/collapse-all. Sections
  collapse by default via `seedMenuFoldDefault` (not in tests); new
  sections follow `menuFoldDefault`. Fold state is session-only.
  Headers are selectable but not the initial highlight.
- **Adding a menu row means updating the pins**:
  `TestMenuLayout_NoCustomActions` expects 2 top-zone rows + 170 group
  actions + 15 headers (187), height 193, dividers `[2, 5, 190]`; also
  `TestMenuLayout_WithCustomActions`, the two tall-window heights in
  `TestMenuModalRect_*`, and `TestMenuLayout_TerminalRowsAboveTheFold`.
- Leader namespaces (leader.go): `Esc a` (AI) and `Esc x` (plugins,
  dynamic via `subFor`/`hintFor`). One level only; sub-bindings may
  collide with top-level ones; a miss inside a live chord is swallowed
  with a flash; chord window `leaderChordFor` (2s); both entry paths go
  through `fireLeader` (`TestLeaderChord_TmuxAltPath`); the flashed hint
  must list every sub-binding. Palette is `Esc k`. Don't add a namespace
  without a real need.
- Which-key band opens on `Esc ?`; the hesitation timer only applies to
  namespace chords. Don't bring back the lone-Esc timer.

## Build / run

```sh
make run / build / build-linux / install / tidy / clean
make test          # go test ./... -race
```
No dev server — it's a TUI; use the `run-ced` skill to drive the real
binary.

## Releases (don't break this)

A release is a hand bump and a tag on `main`:

1. Bump `internal/version/version.go` AND `cats-plugin.toml`'s top-level
   `version =` to the same `x.y.z` (`TestVersion_MatchesCatsManifest`).
2. `make test`, commit `Release ced x.y.z` on `main`.
3. `git tag -a vx.y.z -m "ced x.y.z"` and `git push origin main vx.y.z`.

Pick a number past the LATEST TAG, not just `version.go`. Don't touch the
`release` branch or dispatch `release.yml` — that's the parked
goreleaser pipeline (it publishes a GitHub Release). install.sh and the
Releases page still serve 0.2.0 (next-list N-001).

Parked pipeline notes, if revived: this repo is a FORK, so Actions
triggers (release.yml AND test.yml) don't fire until enabled in the
Actions tab — dispatch by hand with
`gh workflow run release.yml --repo rohanthewiz/ced --ref release`.
The workflow auto-bumps unless the TIP commit edits version.go — amend
the version commit, don't stack on it; delete a stale remote tag before
re-dispatching. The version auto-commit must keep `[skip ci]`.

There is no website; README.md is the documentation. Don't restore the
old SpiceEdit `website/`.

## What NOT to add

- `Ctrl+` editor shortcuts.
- A config file / dotfile beyond the existing `~/.config/ced/` files
  (config.json, mcp.json, state.json, favorites.json, themes/, skills/,
  plugins/, chats/, rc.grsh) and `<repo>/.ced/`. Each exists because it
  holds something ced cannot know for you.
- A HOST plugin system (Go .so plugins, embedded interpreter, stable
  editor API). Plugins stay "a command line plus where its stdout goes".
- CGO dependencies. Tree-sitter (Chroma is intentional).
- A Homebrew formula or tap.
- A PTY / VT emulator in the terminal panel.
