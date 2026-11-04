# N-067 checked for real (sql-formatter, in-process); N-087 drag-select everywhere

Session: `5448be56-e10b-47e5-87c5-e082f76486a3`

## Ask

`/work-next-list`, fifth and last batch (triage in
`2026-1104-1308-n080-n098-n089-label-plural-cmd-o`).

## What changed

### N-067 — PostgreSQL formatting against real tools (Validate, `000f0ae`)

- sql-formatter 15.9.0 installed with npm into the scratchpad (not
  globally). ced's argv as `sqlFormatterArgv` builds it works: `--fix
  -c '<inline JSON>'` rewrites in place and keeps lower case with tabs
  (`useTabs`) or 4 spaces (`tabWidth`); `-c <root>/.sql-formatter.json`
  wins over the file's own style (upper case, 3 spaces on a lower-case
  file).
- Real binary (run-ced, sql-formatter on PATH, palette → "Format
  file"): a lower-case 4-space file came back clause-per-line, lower
  case, 4 spaces, on disk and in the tab.
- With nothing on PATH the in-process rung formatted the same way and
  ONE `Esc u` restored the original line.
- **pg_format not run:** downloading pgFormatter's release tarball from
  GitHub was refused by the session's auto-mode check (code from an
  external source). The item stays in Validate, narrowed to pg_format,
  with the steps (`brew install pgformatter`, then the two indent cases
  and a root `.pg_format`).

### N-013 — skipped

rust-analyzer and zls are still unrun: the remaining step is
`rustup component add rust-analyzer` (it changes the user's toolchain)
and a zls install, the same kind of external install the pgFormatter
fetch was refused for. Left for the user.

### N-087 — drag-select in the other diff panes (`b1d70d5`)

- `diffSelSurfaces` (diffselect.go): a registry of tool-window diff
  panes that select — compare (`"comparesel"`) and the git panel's diff
  (`"gitpanelsel"`) — each naming its pane, lines, rows and paint.
  app.go's hooks are one loop each: `diffSelDrag` (drag continuation),
  `diffSelRelease` (copy on release), `diffSelClearAll` (Esc),
  `diffSelClearOutside` (a press elsewhere), `diffSelLive` + `diffSelCopy`
  (⌘C). **Decision (minor):** the git log keeps its own, older wiring;
  folding it in would change a working path for nothing the user sees.
- One paint geometry per pane, used by draw and hit-test alike:
  `comparePaint`, `gitPanelDiffPaint` (`drawGitPanelDiff` now takes no
  arguments and reads it).
- Chips keep winning: compare's `[↺]` already returned before the arm;
  the git panel asks the new `gitPanelHunkChipHit` BEFORE its click runs
  (a verb may replace the diff) and arms nothing on a chip. Arming is
  after the double-click record in both, so the jump still works.
- Cherry-pick preview: a modal owns its mouse, so the drag is the
  dialog's own (`selecting`; press/motion/release edges from `lastBtn`),
  with `diffPaint`, `startDiffSelection`, `extendDiffSelection`,
  `copyDiffSelection`; a press elsewhere in the dialog drops the
  highlight; ⌘C reaches the modal's handleKey (modal dispatch precedes
  the ⌘ block) and copies it again.
- Real binary: a scratch repo with one modified file, `Esc g`, a
  scripted SGR press/motion/release across ` one` → `+TWO` flashed "3
  lines copied".
- Tests: `TestCompareSel_DragCopiesOnRelease` (incl. Esc),
  `TestGitPanelSel_DragCopiesAndChipsStillWin`,
  `TestDiffSel_PressElsewhereAndCmdC`,
  `TestCherryPickDiffSel_DragCopiesInsideTheDialog`. The selection head
  column is exclusive — the same reading as the git log's tests.
- The real-mouse / OSC 52 / chips-by-hand check was added to N-086
  rather than raising a twin item. CLAUDE.md's drag-select rule now
  names every pane.

## Tests

`go test ./...` + `go vet` before each commit; run-ced for both items.

## Next

Closed: N-087. Declined: None. Raised: None.
Deferred: None. Promoted: None. Moved: None.
Updated: N-067, N-086. Full list: `ai_docs/todo/next-list.md`.
