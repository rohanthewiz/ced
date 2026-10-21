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

**Next ID: N-079**

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
  with the backlog cleared.) Since
  `2026-1011-2142-n054-n036-n052-theme-counts-readme-leaders-conflicted-tabs`
  the LEADER table is pinned (`TestREADME_LeaderTableListsEveryBinding`:
  every top-level binding and every static `Esc a` sub-binding needs a
  row); prose sections and ≡ rows are still unguarded.

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

- **N-043** · raised `2026-0930-2056-bookmarks` · value medium
  Bookmarks have no keyboard door: the leader table is out of letters,
  so Toggle / Next / Previous are ≡ Nav rows reached by the palette
  (`Esc k` + typing). A function key (F2 / F11-style, nothing in ced
  binds F-keys yet) or a ⌘ chord (must be pressed in a real browser
  first, per the ⌘ allowlist rule) would make stepping through them
  practical. Decide which, then add it as a second door.

- **N-068** · raised `2026-1016-1651-postgres-sql-formatting` · value low
  SQL files get no live syntax underline (`kindSQL` is formatted, not
  validated) because .sql is shared with other dialects — MySQL's
  backslash escapes and `#` comments lex as unterminated PostgreSQL
  strings. The formatter's positioned refusal is the only answer today.
  Option: validate only the dialect-unambiguous `.pgsql` / `.psql`
  extensions with the existing lexer + paren matcher (sqllex.go,
  `matchSQLParens`), keeping the three-verb table in agreement.

- **N-069** · raised `2026-1016-1651-postgres-sql-formatting` · value low
  ced's in-process SQL formatter (sqlfmt.go) deliberately does not wrap
  long lines and does not look inside `$$` PL/pgSQL bodies (a second
  language, a string to the SQL grammar). A long DDL line or a messy
  function body stays as written. Revisit only if someone hits it; any
  change must keep the re-lex token check (`sameSQLTokens`).

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
  degrade silently; worth confirming on the way.) 2026-10-11
  (`2026-1011-2210-validate-n059-n056-n060-n061-n013-real-binary`): the proxy is GONE from the dev machine (no
  `~/.cargo/bin/rust-analyzer`; `rust-analyzer` not on PATH), so the
  dying-server case was run with a stand-in on PATH that prints rustup's
  "Unknown binary" error and exits 1: opening a .rs file in the real
  binary shows no modal, no flash, nothing in the status bar; ≡ Code
  offers "Restart language server (rust-analyzer)" as the retry; `Esc d`
  does nothing at all (menuGoToDefinition returns early on
  `!hasLSPActions()` — the same as with no server, by the
  silent-degradation rule). The TypeScript inlay-hint `preferences` in
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
  Since `2026-1017-2208-conflict-side-names` also: the side names
  (conflictsides.go) on a real `git rebase` stop — "yours" on the lower
  side, the `=======` label's ▲/▼ colours readable on the grey wash
  (pinned at 4.5:1 in tests; drawn correctly by the run-ced PTY capture
  on a real rebase stop, but never seen in tmux, cats or Terminal.app),
  and a conflicted `stash pop` keeping the plain current / incoming words.

- **N-057** · raised `2026-1008-1724-tree-open-finder-terminal-shell-cmd` · value low
  The new tree rows' host branches have only run behind the `hostRun`
  seam. Hand check: "Open in Finder" really reveals a file (`open -R`)
  and opens a folder; "Open in terminal" with `"terminal": "tmux"` (or
  auto) inside real tmux splits a pane in the folder (no tmux on the dev
  machine); a custom command (`open -a Ghostty {{DIR}}`) opens there.
  The cats branch and ced's panel branch DID run in the real binary.

- **N-065** · raised `2026-1014-1518-host-theme-tree-colors` · value low
  The host theme's tree corrections (`catsHostTreeColors`: a panel lighter
  than bg is dropped for the derived darker sidebar, folders = fg) have
  only run in tests. run-ced has no cats host, so the synthesized theme
  never appears there. Hand check inside cats on cats-green and one theme
  that keeps its panel (Tokyo Night): does the darker tree/tab strip sit
  well against cats' own sidebar, and do the folder/file tiers read.

- **N-066** · raised `2026-1015-1640-tree-recent-folders-star` · value low
  The tree's recent-folders star has only been seen through run-ced's
  emulator, which doesn't measure glyph width. It went `★` (muted, too
  faint) → emoji `⭐` (two cells, too big) → `★` bold gold (a little
  small) → `⭑` (U+2B51, NEUTRAL width) bold gold, one cell. Size seen
  and approved in cats on 2026-10-16. In SF Mono's fallback ⭑ is a few
  pixels wider than its cell. Still to hand check in macOS Terminal.app,
  tmux and cats: the overhang doesn't clip or smear into the splitter
  when a narrow panel pins it to the last column, and a real click on it
  opens Recent folders.

- **N-067** · raised `2026-1016-1651-postgres-sql-formatting` · value low
  PostgreSQL formatting's external rung has only run with stubbed
  binaries: neither `pg_format` nor `sql-formatter` was installed, so the
  argvs (`pg_format -i --tabs|--spaces N --keyword-case 1|2`, `-c
  <root>/.pg_format`; `sql-formatter --fix -c '<inline JSON>'` / `-c
  <root>/.sql-formatter.json`) were checked against each project's
  README only. Install both and run ≡ File → Format file on a tab- and
  a four-space-indented, lower-case .sql file: each tool rewrites in
  place, keeps the indent and case, and a root config wins. Also confirm
  the in-process rung in the real binary (run-ced): format, then one
  Esc u undoes it.

- **N-070** · raised `2026-1016-1849-tree-star-find-files-quit-button` · value low
  The ✕ quit button left of ≡ (quitbutton.go) has only run in tests and
  run-ced's emulator. Hand check in cats and tmux: a real click on any
  of its three cells opens "Close the editor?" (dirty tabs → Save /
  Discard / Cancel), the red ✕ reads as distinct from the tree header's
  muted ✕ a few cells left, and the ≡ beside it still opens the menu.
  Since `2026-1018-2005-quit-button-tweaks-0.6.1` (ced 0.6.1) the ✕ is a
  DESATURATED red and the clean-tabs confirm opens on Yes: also check the
  dusty red still reads as "close the editor" (not as muted chrome) in a
  couple of themes, and that click + Enter quits.

- **N-071** · raised `2026-1016-2111-recent-folders-local-and-workspace-routing` · value medium
  Recent folders' Tier-1 routing (catsworkspace.go) has only run against
  the control-socket spy. Hand check in a real cats, rebuilt with the
  `workspace.list` `cwd` field (cats 2026-10-16): clicking a project in
  the tree's ⭑ (1) with a ced open on it in another workspace brings
  that workspace, tab and pane into view; (2) with only shells in its
  workspace opens a new ced tab THERE and follows it; (3) with its
  workspace asleep, or none, opens the tab in the current workspace.
  Also with an OLDER cats (no `cwd`): the pane-evidence fallback still
  finds the workspace. And the `./` local half at the bottom reveals,
  never re-roots.

- **N-073** · raised `2026-1017-2044-button-hover-labels` · value medium
  Button hover labels have run in unit tests and in the run-ced PTY
  driver (✕ label drawn over a bare PTY), never through a multiplexer.
  Hand check in plain tmux (`mouse on`), cats, and macOS Terminal + tmux:
  resting on ✕ / ≡ / a tab's × / status ⧉ / a panel ⟳ shows the label
  after ~½s, moving across a multi-cell button (`Actions ▾`) doesn't
  flicker, a click or key dismisses it without being eaten, and the
  unpinned Find-all list's ⟳ ◇ ◨ labels draw ABOVE the list. Where no
  motion is reported, nothing appears and nothing breaks.

- **N-078** · raised `2026-1020-1857-push-receipt-pr-link` · value medium
  The push receipt rework (ced 0.6.5) has run only in unit tests on a
  simulated screen. Hand check a REAL first push of a new branch to
  GitHub from the Push dialog, in tmux and in cats: the whole output is
  shown, the `pull/new/<branch>` URL sits on one row (terminal
  Cmd/Shift-click opens it), `[ Copy link ]` lands it on the Mac
  clipboard over SSH (OSC 52 through tmux), `[ Open link ]` opens the
  browser locally, the box stays ~20s / while hovered, and ≡ Git "Show
  last push output" brings it back. Also try ≡ Git "New branch…" (bad
  name → git's refusal modal; dirty tree → the "changes come along"
  hint).

## Roadmap

Wanted, but not next. Parked, not declined.

- **N-017** · raised `2026-0909-1845-tree-multi-select` · value low
  Tree marks: a cross-folder "select all matching" (tick every
  `*_test.go`). The finder answers the question differently today.

- **N-019** · raised `2026-0828-1713-chat-archive` · value low
  Real chat RESUME via ACP `session/load`. Contingent on an agent
  advertising `loadSession` and on solving the replay-doubles-the-transcript
  problem; the archive deliberately does not store the agent session id.

- **N-074** · raised `2026-1017-2044-button-hover-labels` · value low
  Button hover labels (buttontip.go) skip three spots: buttons inside
  ordinary modals (Yes/No etc. — they already carry words, and the layer
  is silent under every modal but the unpinned Find-all list), the
  commit receipt's `[ Push <branch>… ]`, and the status bar's
  ` · preview` / ` · wrap` pieces, which get no key line because their
  onClick (`toggleMarkdownView`/`toggleSoftWrap`) is not the verb the
  leader binds. Add only if someone misses them.

- **N-075** · raised `2026-1017-2208-conflict-side-names` · value low
  The conflict lens's full labels now carry names ("Accept yours (main) ·
  Accept picked (2a0370b) · Accept both · Compare sides") and no longer
  fit at ~120 columns, so a typical window shows the short roles
  (`Yours · Picked · Both · Compare`); the full names live on the
  `=======` label. If the short form reads as too terse in practice,
  try a middle tier (role + name without "Accept") in `layoutLens`.

- **N-077** · raised `2026-1020-1857-push-receipt-pr-link` · value low
  The push receipt's `[ Open link ]` / `[ Copy link ]` are click-only
  (the receipt lets keys pass through). The keyboard path today is ≡ Git
  "Show last push output" + selecting the URL, which is now on one row.
  If that proves clumsy, add a ≡ Git "Copy last push link" row (dim
  until a push printed a link).

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

- **N-001** · declined `2026-1013-1525-retire-release-pipeline` — reviving the GitHub Release pipeline
  (`release` branch, auto-bump, goreleaser). The owner retired it on
  2026-10-13 (cats N-019) instead of reviving it: `release.yml`,
  `.goreleaser.yml` and `install.sh` are deleted. ced ships as the cats
  plugin, or builds from source. The Releases page stops at v0.2.0, and
  later versions exist only as tags.

## Closed

Newest first. Closures before 2026-09-21 live in the session docs.

- closed 2026-10-20, `2026-1020-1913-n076-new-branch-row` —
  **N-076** the Switch branch picker (≡ Git row and the status bar's
  branch label) now leads with "+ New branch…" (`newBranchRowLabel`,
  runs `menuGitNewBranch`). The picker always opens, so a one-branch
  repo gets that row instead of the "No other branches" flash
  (`TestMenuGitSwitchBranch_OneBranchOffersNewBranch`). The row is the
  initial highlight, so Enter on an unfiltered list now creates rather
  than switches.

- closed 2026-10-18, `2026-1018-2021-n072-palette-hides-scattered` —
  **N-072** palette yes, completion no. Measured over the ≡ menu's 178
  labels: under a real word the scattered tail was unrelated rows ("ref"
  → 1 hit + 20 others, "git" 7 + 16, "line" 7 + 16), so the palette
  (every `openPicker` list) now drops scattered rows when a contiguous
  one exists (`dropScatteredMatches`), and `paneCount` counts the same
  way. New `finder.IsContiguous` is the shared tier check. Completion
  keeps them: servers return camel-hump matches on purpose ("nt" →
  NewTab) that a stray contiguous hit would hide.

- closed 2026-10-14, `2026-1014-1447-n042-tab-group-colours` —
  **N-042** both extras. Group menu (and the ≡
  *Tab groups…* picker, which shows the same rows) gains *Change colour…*
  (`pickTabGroupColor`): the six syntax hues, each row with a swatch
  (new `paletteItem.swatch`), "current" and "used by <group>" marked,
  stored as the index so it follows theme switches and the session.
  Members' underline takes the group colour (SGR 58) only when
  `undercolor.go` vouches for the terminal: an env ALLOWLIST where the
  innermost multiplexer decides (screen → no; tmux only with
  TERM_PROGRAM=tmux; Zellij; cats by CATS_ENV), then TERM_PROGRAM /
  TERM / terminal-specific vars, VTE ≥ 0.52. List from a Sept 2026
  review of terminals' SGR parsers (vim PR #21412). `"underlinecolor"`
  auto/on/off overrides (ssh hides most evidence). Also fixed: the
  member underline broke under the file icon. Real binary checked via
  a raw byte dump of the capture tool: on/auto-in-cats emit
  `58:2::r:g:b` matching the chip, off/auto-without-evidence emit none;
  not checked by eye in other terminals.

- closed 2026-10-13, `2026-1013-1525-retire-release-pipeline` — **N-009** moot. The
  download-summary note about Homebrew installs is true for every GitHub
  Release that exists (all are v0.2.0 or older), and no more will ship
  now that the pipeline is retired (N-001 → Non-goals).

- closed 2026-10-13, `2026-1013-1326-n064-git-log-merge-picks` —
  **N-064** the log rows now carry `Merge` (parent count from `%P` in
  `gitLogFormat`), and both single-commit verbs plus the tick set add
  `-m 1` only when a merge is in the command (cherry-pick through
  `cherryPickArgs`' `mainline`, revert through `gitLogRevertArgs`). The
  confirm gains a line naming the choice (`gitLogMergeNote`: "replays" /
  "undoes what it merged in (-m 1)"). Pinned against real git by
  `TestGitLogActions_MergeEndToEnd` (pick onto main, revert on topic) and
  `TestGitLogTicks_SetWithAMergeEndToEnd`; not driven in the real binary.

- closed 2026-10-13, `2026-1013-1242-n051-cherry-pick-merges-diff-preview` —
  **N-051** both remaining extras. MERGES are
  listed (`⑂`) and picked against their first parent: `-m 1` is always
  the answer (the merge's own contribution to the source branch; `-m 2`
  replays the branch against what it merged and is left to the terminal),
  and it rides in the SAME `git cherry-pick` as non-merge picks — git
  ≥ 2.21 picks a non-merge under `-m 1` unchanged (checked against git
  2.54 by hand and by `TestCherryPick_MergeEndToEnd`). A merge that only
  brings in what HEAD has ("merge main into feature") is shown `[=]` and
  refused — git would stop the sequence on it as an empty pick. Git <
  2.31 falls back to the merge-free listing with "merges hidden" in the
  summary. DIFF PREVIEW: `alt+d` / the detail row's `▸ diff` toggle opens
  a pane inside the dialog (cherrypickdiff.go) that follows the
  highlight — message, stat, patch (first-parent for a merge), cached by
  hash, Alt/Shift+arrows and the wheel scroll it. Checked in the real
  binary (`run-ced`). Raised N-064 (the git log panel's merge picks).

- closed 2026-10-11, `2026-1011-2210-validate-n059-n056-n060-n061-n013-real-binary` —
  **N-061** checked in the real binary (`run-ced`, SGR motion reports):
  with no pointer the receipt is gone by 8.5s; with the pointer resting
  inside it is still up at 9.5s; after leaving at 3s it is still up 5s
  later and gone 8.5s later (a full 7s re-arm on leave).

- closed 2026-10-11, `2026-1011-2210-validate-n059-n056-n060-n061-n013-real-binary` —
  **N-060** checked in the real binary (`run-ced`, a repo with a bare
  remote and upstream): a commit from the git panel's Actions ▾ shows
  the receipt with `[ Push main… ]` under the message; a click on it
  opens the Push dialog for `main → origin/main`; with the receipt up,
  `Esc p` dismisses it AND opens the finder (the key is not eaten).

- closed 2026-10-11, `2026-1011-2210-validate-n059-n056-n060-n061-n013-real-binary` —
  **N-063** (raised and fixed in the same session, found by N-056's
  check) the search-history dropdown clipped Copy to… destinations at
  the END, so two folders under one long parent drew as identical rows.
  `histDropLabel` (searchhistory.go) clips destinations from the front
  (`chatFitPath`), searches and command templates still at the end.
  Pinned by `TestHistDropLabel_DestinationsKeepTheirTail`; re-checked in
  the real binary (`…/v56dest/two`, `…/v56dest/one`).

- closed 2026-10-11, `2026-1011-2210-validate-n059-n056-n060-n061-n013-real-binary` —
  **N-056** checked in the real binary in a git repo (`run-ced`, four
  restarts): a copy records its destination in `.ced/history.bytdb`
  (the database ignored by the `.ced/.gitignore` it writes; only that
  file shows as untracked, by design); the next process seeds the field
  with it; Up opens the dropdown listing both destinations, newest
  first; Delete forgets the highlighted one and the forgetting survives
  a restart (field then seeded with the survivor). The check found
  N-063, fixed alongside.

- closed 2026-10-11, `2026-1011-2210-validate-n059-n056-n060-n061-n013-real-binary` —
  **N-062** (raised and fixed in the same session, found by N-059's
  check) a COMBINED diff — the git panel's view of an unmerged file
  (`diff --cc`), and `git show` of a merge commit in the git log —
  expanded tabs one cell short: its two prefix columns were counted as
  one and code. `diffDisplayLineCols` + `diffPrefixColsAt` (gitpanel.go)
  read the column count off the hunk header's `@` run; both painters use
  them for rows holding a tab. Pinned by
  `TestDiffDisplayLineCols_CombinedDiff`; re-checked in the real binary.

- closed 2026-10-11, `2026-1011-2210-validate-n059-n056-n060-n061-n013-real-binary` —
  **N-059** hand check run in the real binary (`run-ced`, a scratch repo
  with tab-indented Go and a parked cherry-pick): the git panel diff of
  a modified Go file, a git log commit detail, and a Go conflict's
  Compare sides all draw one tab as 4 cells and two as 8. The check found
  N-062 (combined diffs one cell short), fixed alongside.

- closed 2026-10-11, `2026-1011-2152-n050-n053-n048-marker-size-scan-cache-log-ticks` —
  **N-048** the git log panel ticks several commits (leading-cell click,
  or Actions ▾ "Tick …") and cherry-picks them together: "Cherry-pick N
  ticked commits onto <branch>…" leads the Actions picker, confirms with
  the list, runs one `git cherry-pick` oldest first (order from a
  streamed `rev-list --topo-order` — fixed in `ea34914` after the
  end-to-end test caught a plain walk applying a same-second parent
  late). New gitlogticks.go; pinned by `TestGitLogTicks_CherryPickEndToEnd`
  (real repo, ticked newest first, lands oldest first), the parked-op
  refusal, the draw (✓ + "N ticked" title) and the row order tests.

- closed 2026-10-11, `2026-1011-2152-n050-n053-n048-marker-size-scan-cache-log-ticks` —
  **N-053** the Conflicts panel's refresh reuses a file's last disk scan
  while its size, nanosecond mtime and marker length are unchanged
  (`conflictCountCached`, `conflictPanelState.diskScans`, rebuilt from
  the unmerged set each refresh). The verbs that gate `git add` (Mark
  resolved, Stage all & continue) still read fresh via
  `conflictCountFor`. Pinned by `TestConflictPanel_DiskScanCachedByStat`
  (same-stat rewrite stays cached, the gate reads fresh, an mtime move
  rescans).

- closed 2026-10-11, `2026-1011-2152-n050-n053-n048-marker-size-scan-cache-log-ticks` —
  **N-050** the conflict grammar takes a marker length
  (`editor.ParseConflictsSized`, `Tab.SetConflictMarkerSize`; memo keyed
  on revision AND size). The git snapshot asks `git check-attr --stdin -z
  conflict-marker-size` for UNMERGED files only (no fork when nothing is
  conflicted), pushes the sizes onto open tabs and wireTab; the disk
  count and the picker's stage-row probe use the same size (the probe
  asks git live). Pinned by `TestConflictMarkerSize_RealRepo` (a real
  repo with `conflict-marker-size=10`), `TestParseConflictMarkerSizes`,
  and editor tests for sized parsing and the memo.

- closed 2026-10-11, `2026-1011-2142-n054-n036-n052-theme-counts-readme-leaders-conflicted-tabs` —
  **N-052** a conflicted (unmerged) file's TAB label is drawn in
  `theme.Error`, the tree's rule for the same file, active or not
  (`tabGitConflicted`, tabbar.go, reading `a.gitConflicted`). The colour
  sits on the label, not the marker slot, which keeps the disk state
  (⊘ / ⚠ / ●). Pinned by `TestDrawTabBar_ConflictedTabLabelIsError`.

- closed 2026-10-11, `2026-1011-2142-n054-n036-n052-theme-counts-readme-leaders-conflicted-tabs` —
  **N-036** the README hotkey table now lists all 20 missing top-level
  leaders (`Esc z/Z T , . b o O ~ v h H A g L S`, backtick, plus rows for the
  `Esc a` / `Esc x` / `Esc C` prefixes). `TestREADME_LeaderTableListsEveryBinding`
  (leader_test.go) fails when a binding in `leaderBindings()` or the
  static AI table has no row — it reported exactly those 20 before the
  README change.

- closed 2026-10-11, `2026-1011-2142-n054-n036-n052-theme-counts-readme-leaders-conflicted-tabs` —
  **N-054** the theme package's comments no longer state key counts
  ("the other twenty-nine", "thirty-five" — really 33 derived, 41 in
  all, counted from `derivations()`). Reworded count-free ("every other
  key", "the full palette", "forty-odd") in theme.go, palette.go,
  builtin.go, palette_test.go and load_test.go so the next derived key
  can't make them drift again. Theme tests pass.

- closed 2026-10-08, `2026-1008-1837-conflict-compare-sides` —
  **N-049** a conflict block's sides diff in the
  compare panel ("Compare sides": a lens button, a right-click row, ≡ Git
  "Compare conflict sides…"). The panel takes two arbitrary texts now
  (`compareTexts`); a diff3 block offers current ↔ incoming, base ↔
  current and base ↔ incoming. Not a 3-way merge editor — a unified diff
  of one pair at a time, with ⟳ re-reading the block and double-click
  landing on the buffer line.

- closed 2026-10-08, `2026-1008-1825-run-ced-palette-leader` —
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
