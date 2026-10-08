# ced — next list

The living list of open follow-ups. Sessions edit this file IN PLACE
(`/sess-save`, `/next-list`); nothing is copied forward from one session
doc to the next. A session doc's `## Next` records only what that session
changed here, as `Closed: … Raised: …`.

Seeded 2026-09-21 by `/next-list seed` from the last 15 session docs
(`2026-0828-1713-chat-archive` … `2026-0921-0801-lsp-diagnostic-messages`)
plus the LSP work done in the seeding session itself
(`2026-0921-0906-lsp-experience`).

## Conventions

- **IDs are permanent and never reused.** `N-001`… Refer to items by ID.
- **`raised`** is the stem of the session doc the item FIRST appeared in,
  even when that is older than any window a rebuild looked through.
- **Age is computed, never stored**: the number of session docs since
  `raised`.
- **Value** is the payoff of doing it, not the effort:
  - `high` — something is being worked around today, or a second
    independent consumer has arrived
  - `medium` — it blocks one named thing, or it is a visible defect nobody
    has to route around yet
  - `low` — a gap nobody has bumped into, or contingent on something that
    does not exist
- **Open** is what we intend to pick up next. **Roadmap** is wanted but not
  soon — parked, not declined. **Non-goals** is what we are likely never to
  do, kept so it stays visibly declined.
- **Validate** sits directly below Open: items whose remaining work is purely
  testing (a hand check in a real terminal or app, a run on real data or
  hardware, or a test to write or repair), with no product change planned. A
  check that finds a defect raises it as a new Open item; an Open item whose
  fix has landed but is unchecked moves to Validate.
- **Nothing leaves Open, Validate or Roadmap without a line in another
  section.** Done → Closed. Declined → Non-goals. Merged → Closed as
  `merged into N-xxx`. Moving among Open, Validate and Roadmap is fine.
- Open, Validate and Roadmap stay in **ID order**. Never renumber, never delete.

**Next ID: N-059**

## Open

- **N-003** · raised `2026-0910-2016-tool-windows` · value low
  **The README goes stale and no test catches it.** Known spots for
  tool-window changes: `### Tool windows`, the Features list, the chat
  section, the hotkey table. Caught up on 2026-09-21: it had NO language
  server coverage at all, and now has a `## Code intelligence` section
  (the server table, every ≡ Code verb, inlay hints, the restart row,
  the gutter click), a Features bullet, the LSP hotkey rows and Select
  all. What stays open is the standing problem — nothing fails when a
  feature lands without its README paragraph. (Value lowered from medium
  with the backlog cleared.)

- **N-005** · raised `2026-0913-1919-cats-plugin` · value low
  cats-side decision: should two ced launch paths share ONE sidebar group
  (map the shell-launched fallback id to the configured plugin id)? Rare in
  practice; left as-is.

- **N-006** · raised `2026-0913-1919-cats-plugin` · value low
  cats-side decision: should a BLOCKED editor count toward the AGENTS
  attention tally? Currently it does not.

- **N-016** · raised `2026-0921-0906-lsp-experience` · value low
  Inlay hints for servers that take their hint settings through
  `workspace/configuration` rather than `initializationOptions` (pyright).
  The auto-responder answers every configuration request with `{}`, so
  those servers never switch hints on. Contingent on N-013 showing it
  matters.

- **N-036** · raised `2026-0929-1929-readme-recent-files-and-locations` · value low
  The README's leader-key table is missing top-level leaders that are
  bound in leader.go: `Esc b` (switch tab), `Esc ,` / `Esc .`,
  `Esc o` / `Esc O` (back / forward), `Esc T` (focus tree), `Esc Z`
  (redo), `Esc v` (markdown preview), `Esc h` / `Esc H` (next / previous
  change), `Esc A` (blame), `Esc g` (git panel), `Esc L` (git log),
  `Esc S` (git log search), `` Esc ` `` (terminal) and `Esc ~` (terminal
  locations). Some may be documented in their own sections; the table
  is what's short. An instance of N-003.

- **N-042** · raised `2026-0930-2025-tab-groups` · value low
  Tab group extras left out of the first cut:
  - choosing a group's colour (groups get distinct ones automatically)
  - creating a folder group from the file tree's folder context menu
    (today it starts from a tab in that folder)
  - a COLOURED member underline, if a capability check can tell which
    terminals take the colon-form SGR 58

- **N-043** · raised `2026-0930-2056-bookmarks` · value medium
  Bookmarks have no keyboard door: the leader table is out of letters,
  so Toggle / Next / Previous are ≡ Nav rows reached by the palette
  (`Esc k` + typing). A function key (F2 / F11-style, nothing in ced
  binds F-keys yet) or a ⌘ chord (must be pressed in a real browser
  first, per the ⌘ allowlist rule) would make stepping through them
  practical. Decide which, then add it as a second door.

- **N-048** · raised `2026-1003-2042-cherry-pick-and-conflicts-ui` · value low
  The git log panel still cherry-picks ONE commit (Actions ▾). Ticking
  several commits in the log and picking them together would be the
  log-side twin of the cherry-pick dialog, which only lists one branch
  against HEAD.

- **N-049** · raised `2026-1003-2042-cherry-pick-and-conflicts-ui` · value low
  No side-by-side view of a conflict block (VS Code's "Compare changes",
  a 3-way merge editor). The washes show both sides in place; comparing
  current vs incoming vs base for one block would need the compare panel
  to take two arbitrary texts instead of "buffer vs something".

- **N-050** · raised `2026-1003-2042-cherry-pick-and-conflicts-ui` · value low
  The marker grammar (editor/conflict.go) matches only git's default
  conflict-marker-size of 7. A repo setting the `conflict-marker-size`
  gitattribute gets no washes or lens, and its files never read as
  "no markers left" (the stage rows' disk scan has the same rule).

- **N-051** · raised `2026-1003-2042-cherry-pick-and-conflicts-ui` · value low
  Cherry-pick dialog extras left out: a filter field for long lists
  (capped at 300, announced), picking a MERGE commit (needs `-m <parent>`;
  merges are hidden and the summary says so), and a per-commit diff
  preview (today: the files it touches, and which overlap HEAD's side).

- **N-052** · raised `2026-1003-2042-cherry-pick-and-conflicts-ui` · value low
  Conflicted files are red in the tree and counted in the status bar's ⚠,
  but the TAB BAR doesn't mark a conflicted tab. Left over from the 4.3
  follow-ups (`2026-0812-2058-cats-native-phase4-3`), of which the
  `--skip` row and the tree marking are now done.

- **N-053** · raised `2026-1003-2042-cherry-pick-and-conflicts-ui` · value low
  The Conflicts panel rescans every unmerged file WITHOUT an open tab
  from disk on each refresh (open, the 10s tick while it's up, every
  finished git command). Fine for dozens of files; a rebase leaving
  hundreds unmerged would pay for it. A per-file mtime cache would fix it.

- **N-054** · raised `2026-1003-2042-cherry-pick-and-conflicts-ui` · value low
  The theme package's header comments say Normalize derives "the other
  twenty-nine" keys (theme.go, palette.go, load_test.go); the table has
  33 derived keys now (31 before this session's two conflict washes). The
  totals drifted too: "thirty-five" (palette.go, builtin.go,
  palette_test.go, load_test.go) is 41 now (8 core + 33). Re-checked
  2026-10-08 by counting `derivations()`. An instance of N-003's doc
  drift.

## Validate

Items whose remaining work is purely testing: hand checks in a real terminal
or app, runs on real data or hardware, and tests to write or repair. No
product change is planned unless a check finds a defect, which is then raised
as a new Open item. Split out of Open on 2026-10-07; each item kept its ID
and `raised`.

- **N-010** · raised `2026-0921-0801-lsp-diagnostic-messages` · value low
  Try the diagnostic pointer tooltip in a REAL terminal (plain tmux, cats,
  macOS Terminal.app): do motion events reach it, does 250ms feel right.
  `run-ced` CAN send motion (SGR button 35, `{esc}[<35{semi}X{semi}YM`)
  and the tooltip opens on the underline in the real binary
  (2026-09-30); what stays is a person's hand check. Value lowered from
  medium: the caret-line note (`diagnote.go`) now answers in a terminal
  with no motion at all, so the tooltip is no longer the only mouse door.

- **N-013** · raised `2026-0921-0906-lsp-experience` · value medium
  **Most non-Go language servers have never been run.** clangd WAS, on
  2026-09-21 through the real binary (`run-ced`, a three-file C project):
  diagnostics, go to definition (into the header), hover, completion and
  inlay notes (`» n: 3 · by: 4`, on by default) all work. Still
  registered from documentation only: typescript-language-server,
  rust-analyzer, pyright/basedpyright/pylsp and zls — none RUNS on the
  dev machine. (2026-10-08: `~/.cargo/bin/rust-analyzer` exists but is
  rustup's proxy with the component missing — it exits "Unknown binary
  'rust-analyzer' in official toolchain". `rustup component add
  rust-analyzer` would make it the cheapest server to check next. Until
  then ced's look-up finds it and the server dies on start, which should
  degrade silently; worth confirming on the way.) The TypeScript inlay-hint `preferences` in
  `initOptions` are the least certain part — typescript-language-server
  may want them via `workspace/didChangeConfiguration` instead.

- **N-026** · raised `2026-0921-0932-next-list-run` · value low
  cats-side: `"KeyA"` is now in `CMD_TO_PANE` (cats `bd18905`,
  `cmd/catway/web/js/20-keys.js`; the cost to a shell was checked first
  and is nil: a legacy pane fails the kitty gate). What's left is a HAND
  CHECK that ⌘A actually reaches a ced pane in Chrome and in the cats mac
  app, the lesson ⌘E taught (`ed4962c`; Chrome resolved its ⌘E as a menu
  item and never dispatched it). Both are expected to deliver it; close
  on confirmation.

- **N-038** · raised `2026-0930-2002-tab-menu-file-verbs` · value low
  Tab menu "Move to split →" / "Copy to split →" have only run against
  the test's fake control socket (`withCtlSpy`). The real binary drew the
  rows inside cats, but no hand check has clicked one and watched the
  pane appear and the tab close. Also open: whether a `↓` pair is
  wanted. It was left out to keep the menu at 15 rows (≡ Cats still has
  "Open in split ↓").

- **N-040** · raised `2026-0930-2025-tab-groups` · value low
  Tab groups have never been checked by hand in a real terminal. The
  run-ced emulator draws no underlines, so the member underline (plain,
  `tabbar.go`) has only been seen in tests. Look at it in tmux, cats and
  macOS Terminal.app, and check that the chip reads well on a light
  theme (`tabGroupChipFG` picks BG or Text by contrast).

- **N-047** · raised `2026-1003-2042-cherry-pick-and-conflicts-ui` · value low
  The conflict UI has only been driven through `run-ced`'s emulator. Hand
  check in tmux, cats and macOS Terminal.app: the side washes' contrast
  (light themes especially), a real mouse click on the `<<<<<<<` lens,
  and the cats "blocked" badge firing on a stop (`conflictPanel.unseen`).

- **N-056** · raised `2026-1007-1604-copy-to-folder` · value low
  Copy to… was driven in the real binary only in a NON-repo scratch
  project (history memory-only). Hand check in a git repo: the field is
  seeded with the last destination after a restart, and the ▾ / Up
  dropdown lists and forgets destinations (`history.CopyDestinations`).

- **N-057** · raised `2026-1008-1724-tree-open-finder-terminal-shell-cmd` · value low
  The new tree rows' host branches have only run behind the `hostRun`
  seam. Hand check: "Open in Finder" really reveals a file (`open -R`)
  and opens a folder; "Open in terminal" with `"terminal": "tmux"` (or
  auto) inside real tmux splits a pane in the folder (no tmux on the dev
  machine); a custom command (`open -a Ghostty {{DIR}}`) opens there.
  The cats branch and ced's panel branch DID run in the real binary.

## Roadmap

Wanted, but not next. Parked, not declined.

- **N-001** · raised `2026-0910-2016-tool-windows` · value low
  **The GitHub Release pipeline is parked.** Moved from Open on
  2026-09-21: the owner does not need the CI/CD flow for now; releases
  are a hand bump of `version.go` + `cats-plugin.toml` and a pushed tag,
  which is all the cats plugin needs. What stays unshipped: `origin/release`
  is still at `0409317 Release ced 0.2.0`, so `install.sh` and the GitHub
  Releases page still say 0.2.0, and tags `v0.3.0`–`v0.3.7` have no
  artifacts. If revived: the fork suppresses push triggers, so fast-forward
  `release` and `gh workflow run release.yml --repo rohanthewiz/ced --ref
  release`; bump past the latest tag first. The `cats-plugin.toml` sed in
  `release.yml` (N-004) is still unrun.

- **N-009** · raised `2026-0914-1114-homebrew-removed` · value low
  `.claude/commands/summary-of-downloads.md` says download counts include
  Homebrew installs. True up to v0.3.0, false for anything released after
  the tap was removed; tweak if N-001 (now Roadmap) ever ships a release.

- **N-017** · raised `2026-0909-1845-tree-multi-select` · value low
  Tree marks: a cross-folder "select all matching" (tick every
  `*_test.go`). The finder answers the question differently today.

- **N-019** · raised `2026-0828-1713-chat-archive` · value low
  Real chat RESUME via ACP `session/load`. Contingent on an agent
  advertising `loadSession` and on solving the replay-doubles-the-transcript
  problem; the archive deliberately does not store the agent session id.

## Non-goals

- **N-020** · declined `2026-0910-2016-tool-windows` — polishing a bottom-docked
  Explorer / narrow-docked git panels. Usable but cramped on purpose; the
  internal seams are tuned for a wide strip and nobody has asked.
- **N-021** · declined `2026-0910-2016-tool-windows` — making the Find-all list a tool
  window. Live preview, an Esc that restores the view, and a TOP dock no
  tool window has. (Related, from `2026-0910-1826-clickable-overflow-markers`:
  the UNPINNED list's markers have no popup; pinning restores it.)
- **N-022** · declined `2026-0913-2142-select-all` — a leader key for Select all. The
  flat Esc table is out of mnemonic letters.
- **N-023** · declined `2026-0909-1845-tree-multi-select` — persisted tree marks, and
  Rename as a set verb (a bulk rename needs a pattern language).
- **N-024** · declined `2026-0828-1713-chat-archive` — a delete row in the Recent
  chats picker. The retention cap and `rm` cover it; `chatstore.Remove`
  exists if it is ever wanted.
- **N-025** · declined `2026-0921-0906-lsp-experience` — semantic tokens (would fight
  the Chroma grid brace matching reads), LSP formatting (format.go covers
  it), code lens (needs virtual rows), folding (no fold model), and
  IN-LINE inlay hints (linenote.go's header has the bill). Since
  `2026-1003-2042-cherry-pick-and-conflicts-ui`, editor/lens.go offers a
  clickable END-OF-LINE lens (`LensSource`), so an LSP code lens would no
  longer need virtual rows — still declined, but that reason is gone.

## Closed

Newest first. Closures before 2026-09-21 live in the session docs.

- closed 2026-10-08, no session doc (commit "run-ced: palette is Esc k, not Esc a (N-041)") —
  **N-041** the run-ced recipes open the palette with `{esc}k`, in
  SKILL.md and in the capture tool's `-help-script` text. "Leaders worth
  knowing" now says `k` palette, adds `?`, and warns that `a` is the AI
  namespace. Found while checking with the real binary: the theme-picker
  recipe's filter `theme` ranks "Reload themes" first, so its `{enter}`
  reloaded themes — now `theme:`. The `-help-script` ≡ menu example sent
  `{esc}{esc}` in one write (folds into Alt+Esc, opens nothing); it now
  splits the two Escs like SKILL.md's recipe. All three recipes re-run
  against `bin/ced`.

- closed 2026-10-08, `2026-1008-1815-context-menus-scroll` —
  **N-039** anchored context menus scroll when taller than the window.
  `editorContextModal` (editor, tab, tab-group, problems, git log,
  conflicts) and the tree's `contextModal` share `ctxScroll`
  (contextmenu.go): `placeContextSized` places by the CLIPPED height,
  ▲/▼ on the borders, wheel scrolls, arrows reveal, a press on a marked
  border pages. Premise corrected: ced draws nothing below `minHeight`
  (24), so the tab/tree menus fit today; the editor menu (conflict + cats
  + bookmark + search rows) is what actually overflowed.

- closed 2026-10-08, `2026-1008-1803-diag-note-off-switch` —
  **N-037** the caret-line diagnostic note has an off switch: ≡ View
  "Hide / Show diagnostic note" beside the inlay-hints row, persisted as
  `"diagnote"` (default on; `userconfig.SaveDiagNote`). Held inverted as
  `App.diagNoteOff` so tests that build App directly keep the note. The
  nit is fixed too: while the note is on, a press stamps the diag-tip
  pointer cell, so the click's release (a same-cell motion report) no
  longer arms a tooltip saying the same thing; while off, it still arms
  (the click's only answer). Menu pins re-pinned (174 actions, 197).
  Pinned by `TestDiagNote_OffSwitch`,
  `TestDiagNote_ClickReleaseArmsNoTooltipWhileOn`, `TestDiagNoteKey`.

- closed 2026-10-08, `2026-1008-1750-tab-menu-copy-to` —
  **N-055** the tab right-click menu has a "Copy to…" row, after Zip
  file (copy-it-out group). It acts IN PLACE on the clicked tab
  (`promptCopyTo` with that tab's path), so a background tab is copied
  without coming forward; an unsaved buffer is copied as shown, never
  saved. ≡ File "Copy file to…" is its twin. Order pin updated
  (`TestTabContext_RightClickOpensTheTabMenu`, CLAUDE.md); pinned by
  `TestTabContext_CopyToCopiesTheClickedTabInPlace`.

- closed 2026-10-08, `2026-1008-1737-popup-width-rule` —
  **N-058** checked: nothing overflowed. problems.go, gitlogactions.go,
  conflictpanel.go (and tabcontext.go, tabgroups.go, the editor menu)
  already grew to their widest label — `contextMenuWidth` was only the
  loop's floor; the one truly fixed popup, the preview's "Stop Preview"
  (contextmenu.go), fits it (12 + 6 ≤ 19). The seven hand copies of the
  sizing loop now share `contextMenuWidthForLabel` (modals.go) via
  `contextMenuWidthFor` / `editorContextMenuWidth`, the preview menu
  included, so they can't drift.

- closed 2026-10-03, `2026-1003-1533-change-bar-popup` —
  **N-046** CLAUDE.md's Commits rule now allows `Co-Authored-By: Claude`
  trailers (user's call); the "Generated with Claude Code" footer stays
  banned.

- closed 2026-10-03, `2026-1003-1222-wrapped-scroll-ceiling` —
  **N-045** wrapped scroll ceiling in rows: `MaxScroll` hands a wrapped
  tab to `maxScrollWrapped`, which walks up from EOF until
  `viewH - overscroll` display rows are counted (one row per line gives
  the old formula). Pinned by
  `TestSoftWrap_CaretBelowLineCeilingStaysOnScreen`.

- closed 2026-10-01, `2026-1001-0055-bookmark-labels` —
  **N-044** the four bookmark extras:
  - labels: `editor.Bookmark.Label` rides remap / restore / merge; ≡ Nav
    "Label bookmark…" (+ editor right-click twin on a bookmarked line),
    `[Clear name]` alt+c because a prompt drops an empty submit; shown in
    brackets in the picker and in Next/Prev's flash; stored as an
    omitempty `label` key (`history.MaxBookmarkLabel` 60 runes).
  - overflow: `offscreen.bookmarks`, POPUP ONLY — no colour (Accent
    already means the caret; any rank demotes a diagnostic).
  - "tree cut/paste MOVE": premise lapsed — ced has no move verb; the
    file clipboard only COPIES (`startPaste` → `copyTree`), and a copy
    rightly gets no bookmarks. Renames are the only in-editor move and
    were already re-keyed. A move made outside ced (shell `mv`,
    `git mv`) still leaves "(missing)" rows — unchanged by design.
  - `⚑` width: U+2691 is East-Asian-Width Neutral, not
    Emoji_Presentation; macOS libc `wcwidth` = 1, uniseg = 1 (2 only
    with VS16, which ced never emits). The real binary in a PTY (run-ced
    capture) lines `⚑   7` up with `    8`. Not checked: a GUI terminal
    whose font fallback draws the glyph wider than its cell — tmux and
    tcell lay out by wcwidth, so that would be a font overdraw, not a
    column shift.

- closed 2026-09-30, `2026-0930-1910-diagnostic-caret-note` —
  **N-012** the caret line's diagnostic is echoed as the caret moves,
  but END-OF-LINE (the inlay-note slot, in severity colour) rather than
  in the width-budgeted status bar: `diagnote.go`, `Tab.SetCaretNote`.
  Worst diagnostic + `(+N more)`, caret line only, shares
  `diagsAtCaret` with Esc-i.

- closed 2026-09-29, `2026-0929-1929-readme-recent-files-and-locations` —
  **N-029** README gains `### Recent files and recent locations`: both
  ≡ Nav pickers (the 5 + 10 lists, the `›` drill-in, reveal not re-root,
  what counts as a use), the "No file open" links, and where the history
  lives (`.ced/history.bytdb`, gitignored, repositories only, "(not
  saved)"). Plus a Features bullet and the `Esc B` hotkey row.
- closed 2026-09-29, no session doc (commit "Hover asks for markdown first (N-030)") —
  **N-030** hover now asks for MARKDOWN first. Checked against real
  gopls v0.21 on ced's own sources before deciding: the plaintext form
  was worse than the heuristic's known costs. A type's method list
  arrives as unindented `func (c *Client) …` lines, which `hoverReflow`
  could only read as prose and joined into one paragraph. Every doc link
  also trails a `[Name]: file:///…` definition line, visible in any
  short tooltip. Markdown fences the signature and the method list, splits sections
  with `---`, sends each paragraph on one line, and resolves doc links.
  `lsp.Hover.Markup` keeps the kind (bare MarkedString = markdown,
  `{language, value}` fenced, array elements blank-separated); it rides
  `lspHoverEvent.markdown` for both the Esc-i modal and the dwell
  tooltip. In markdown, non-code lines go through the preview's own
  inline scanner (`editor.MarkdownInlineText`, plus `MarkdownLinkOnly`
  for the pkg.go.dev footer, shown as its URL). The plaintext guesses
  stay for servers that ignore the preference. Signature help and
  completion docs stay plaintext first. Known wart, gopls-side: its
  markdown linkifies the `[string]` in `map[string]any`, so that one
  reads "mapstringany". Pinned by
  `TestHoverLines_MarkdownReadsGoplsStructure`,
  `TestHoverLines_MarkdownDropsThePlaintextGuesses`,
  `TestHandleLSPHover_RendersMarkdown`, `TestHoverMarkup`,
  `TestInitialize_PrefersMarkdownHover`,
  `TestMarkdownInlineText_ReadsLikeThePreview`, `TestMarkdownLinkOnly`.
- closed 2026-09-29, no session doc (commit "gofmt drift (N-034)") —
  **N-034** `gofmt -w` on `internal/app/hovermodal.go` (the
  `tooltipPlace` diagram's leading indent), `internal/editor/tab.go`
  (`mdView`/`MDScroll` alignment) and `internal/lsp/inlayhint_test.go`
  (a long one-line goroutine split). Formatting only; `gofmt -l` is now
  empty.
- closed 2026-09-29, `2026-0929-1855-find-options-reach-the-lists` — **N-035** the bar's `Aa` / `|W|` now reach both
  lists. `App.findOptions()` is the one conversion; `showFindAll`
  snapshots it onto `findAllModal.opts` (⟳, the stale check and the
  replace plan keep asking that question if a toggle flips under a pinned
  list), and borrows/returns the tab's `FindOpts` with its query.
  `search.Options` embeds `editor.FindOptions`; the event carries them;
  `startProjectSearch(query, opts)`; project tints install the list's
  options and restore the tab's. Titles and "no occurrences" flashes name
  non-default options ("(match case, whole word)", `findOptionsNote`).
  Found on the way: `rowStale` / `buildReplacePlan` compared the row to
  the query byte-for-byte, so a case-insensitive list dimmed its
  "Count" hit for "count" as stale and Replace skipped it — now
  `editor.MatchesAt`, which shares `matchCols`' fold and boundary rule.
  Pinned by `TestFindAll_HonoursTheBarsOptions`,
  `TestFindAll_CaseVariantRowsAreNotStale`,
  `TestProjectSearch_CarriesTheOptionsThrough`,
  `TestProject_HonoursMatchCaseAndWholeWord`,
  `TestMatchesAt_AgreesWithTheScanner`.
- closed 2026-09-29, `2026-0929-1808-find-docs-blame-names-replace-history` — **N-031** the Find-all list's replace
  box has its own dropdown on the replacement list it already recorded
  into: `Up` in the box or a click on its `⇄▾` label, opens below, fills
  and never runs "Replace in N". Unpinned it rides the modal slot;
  pinned, `findAllPinHistMouse` asks it before the router's panel
  dispatch (rows hang over the editor) and `drawFindAllPinHist` paints it
  from the overlay pass; `closeAllModals` shuts it. The filter boxes
  (Find-all, git log, references) stay history-less — filters, not
  searches. Found on the way: a dropdown's own button could never close
  it (the outside-press rule closed it and handed the press to the button,
  which reopened it) — the find bar's promised second-click close and the
  prompt `▾` both; `histDropMouse` now takes the owner's `toggle` rect and
  consumes a press on it. Pinned by `TestFindAll_Replace*`,
  `TestFindAll_FilterBoxHasNoHistory`,
  `TestFindAll_PinnedReplaceHistHangsOverTheEditor`,
  `TestCloseAllModals_ClosesThePinnedReplaceHist`,
  `TestFindBar_SecondLabelClickClosesTheList`,
  `TestSearchPrompt_SecondButtonClickClosesTheList`.
- closed 2026-09-29, `2026-0929-1808-find-docs-blame-names-replace-history` — **N-033** neither switch nor choice:
  blame keeps the given name and adds surname letters only where two
  different authors in the same file share it —
  `disambiguateBlameAuthors`, run at the end of `parseBlamePorcelain`
  over the whole file so labels (and the column width) never depend on
  what is scrolled into view. "Rohan A." / "Rohan B.", the prefix grows
  on a shared initial ("Rohan Al." / "Rohan Ad."), a whole surname drops
  the dot, the given name is what gets elided to fit `blameAuthorMax`.
  No config key, no ≡ row. Pinned by `TestDisambiguateBlameAuthors_*`
  and `TestParseBlamePorcelain_DisambiguatesSharedGivenNames`.
- closed 2026-09-29, `2026-0929-1808-find-docs-blame-names-replace-history` — **N-032** README `### Find in file`
  rewritten against the code: `Aa` / `|W|` (click, `Alt+c` / `Alt+w`, the
  ≡ Find rows), selection seeding, the replace row (`Esc e`, Tab, `Enter`
  / `Alt+a`, one undo step), the Find-all list (`Esc F` / ↓: preview,
  Esc restores, `d` dock, `p` pin with filter / dismiss / ⟳ / replace),
  Find in project (`Esc P`, click keeps the list, 10,000 cap) and Go to
  line. The hotkey table gains `Esc F` / `Esc e` / `Esc P` / `Esc j`.
  Raised N-035 for the toggles not reaching the lists.
- closed 2026-09-29, `2026-0929-1722-history-not-saved-notice` — **N-028** neither option as written: no
  fallback location (a new dotfile, which CLAUDE.md rules out) and no
  startup flash (startup errors are HELD for a ≡ label). Instead
  `history.WriteProblem` probes by doing — temp dir beside a missing
  `.ced/`, temp file in an existing one, write-open of the database, each
  undone — on the first menu/palette ask, and the answer is held: ≡ Nav
  "Recent files…" / "Recent locations…" read "(not saved)", stay
  clickable, and flash "History won't be saved this session: can't create
  .ced (permission denied)". A non-repository root is memory-only by
  design and never labelled. Pinned by `TestWriteProblem_*` and
  `TestHistoryNoSave_*`.
- closed 2026-09-29, `2026-0929-1710-select-all-file-row` — **N-008** ≡ **File** now carries a "Select all"
  row too (under the Copy-path rows), same action and predicate as the
  Edit row. It is flagged `paletteTwin` so the command palette still
  lists the verb once — an explicit flag, not dedupe by label + func
  pointer, because plugin/custom-action rows are closures from one func
  literal and would merge. Pinned by `TestMenuSelectAll_InFileAndEdit`
  and `TestPalette_TwinRowListedOnce`; menu pins now 150 actions / 167
  rows / height 173.
- closed 2026-09-29, `2026-0929-1617-history-only-in-repos` — **N-027** `.ced/history.bytdb` is written only when
  `history.Persists(root)`: the root is inside a git work tree (walks up,
  so a repo subfolder counts; a `.git` FILE counts, for worktrees and
  submodules) or already holds a `.ced/` (earlier history, format.json).
  Anywhere else the history is memory-only for the session — `ced ~` no
  longer leaves `~/.ced/`. Checked at write time, so a mid-session
  `git init` persists. Filesystem walk, not `git rev-parse`, so Close
  never waits on a process. Pinned by `TestPersists_*` and
  `TestWriteHistory_OnlyInARepository`.
- closed 2026-09-29, `2026-0929-1552-overflow-markers-panels` — **N-018** overflow markers on the compare panel
  (lines), Problems (problems, over the filtered view), chat (wrapped
  transcript rows, band stops above chips/composer) and terminal
  (scrollback lines). All sit in each panel's blank right margin. Problems
  is the only list COLORED by what's hidden (severity, since rows sort by
  path); the terminal stays plain over stderr on purpose. The click routes
  through `scrollAt` unchanged. Seen in the real binary (terminal ▴/▾).
- closed 2026-09-21, `2026-0921-0932-next-list-run` — **N-004** `release.yml`'s auto-bump now rewrites
  `cats-plugin.toml`'s version in the same commit, and
  `TestVersion_MatchesCatsManifest` catches a manual bump that forgot it.
  The workflow edit is UNRUN — first exercised by N-001's release.
- closed 2026-09-21, `2026-0921-0932-next-list-run` — **N-002** `toolLayoutSummary()` has a reader: the
  ≡ "Reset tool window layout" flash now carries it, since most of what
  a reset changes is off screen. Kept rather than deleted.
- closed 2026-09-21, `2026-0921-0932-next-list-run` — **N-007** ⌘A selects all (`metaAccels`, pinned by
  `TestMetaAccelSelectsAll`; the never-the-only-path test passes via the
  ≡ Edit row). Not live in browser-cats until N-026.
- closed 2026-09-21, `2026-0921-0932-next-list-run` — **N-015** `lspLookPath` pinned at "never found" in
  `newTestApp`; the two real-gopls tests opt back in with
  `useRealLSPBinaries` (confirmed they still RUN, not skip).
- closed 2026-09-21, `2026-0921-0932-next-list-run` — **N-011** gutter click opens the diagnostic tooltip
  (`diagGutterPress`, diagtip.go). Diagnosed lines only; a second click
  closes it; no caret move, no drag. Like N-010, only exercised on the
  simulation screen — `run-ced` cannot send mouse events.
- closed 2026-09-21, `2026-0921-0932-next-list-run` — **N-014** "Restart language server" ≡ Code row.
  `internal/app/lsprestart.go`; acts on the active file's server, never
  dimmed, names the missing binaries. Needed a slot generation
  (`lspServer.gen`) so the replaced process's late exit event cannot kill
  its successor. The spawn path itself is untested (it would start a real
  server); try it once by hand: `kill` gopls, then use the row.
- closed 2026-09-21 — **"a `bin` entry in ced's manifest" (was a Non-goal
  in `2026-0913-1919-cats-plugin`) — OVERTURNED, not done.** The reason
  was that it would shadow the Homebrew `ced`; the Homebrew removal
  (`2026-0914-1114-homebrew-removed`) reversed that, and
  `cats-plugin.toml` now has `bin = ["./bin/ced"]` as the official install.
- closed 2026-09-21 — commit and push the cats changes (`catway.go`,
  `wire/down.go`, `pluginpane_test.go`). `~/projs/go/cats` is clean and
  `50e06e3 agents: an editor is a tool row, not an agent row` holds them.
- closed 2026-09-21 — rebuild and restart catway for the editor
  reclassification. Not verifiable from here (a running process); closed on
  the grounds that any catway built since `50e06e3` includes it. Reopen if
  ced still shows among the coding agents.
- closed 2026-09-21 — `catctl plugin install rohanthewiz/ced` from GitHub.
  Its only blocker was the manifest reaching `main`; `cats-plugin.toml` is
  on `origin/main`.
