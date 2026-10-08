# 2026-10-08 18:37 — Compare a conflict block's sides (N-049)

Session: `a6fdccf0-5e92-4b28-9c4a-91feb4a214ce`

## Request

From the cats-todo backlog, next-list item **N-049**: there was no
side-by-side view of a conflict block (VS Code's "Compare changes", a
3-way merge editor). The washes show both sides in place; comparing
current vs incoming vs base for one block needed the compare panel to take
two arbitrary texts instead of "buffer vs something".

## What changed

**Compare panel takes two texts** (`internal/app/compare.go`)
- New general form `compareTexts(oldLabel, oldLines, newLabel, newLines)`:
  the one place a diff is computed and installed. It RESETS the source
  fields (`oldPath`, `oldLines`, `newPath`, new `newLineBase`, new
  `conflict`); callers with a source to remember set them afterwards.
- `runCompare` (whole buffer as new side) and `compareTextWithSelection`
  now go through it — both lost their duplicated install blocks.
- `newLineBase`: the buffer line the "+" side starts on, added in
  `compareJumpToRow` so a slice from mid-file jumps to the right line.
- `compareRefresh` checks `conflict` first and re-reads the block.
- Header comment amended: "active buffer is the new side" now names the
  two two-slice sources (selection vs paste, conflict sides).

**Compare sides** (new `internal/app/conflictcompare.go` + test)
- A two-way block opens straight into current → incoming. A diff3 block
  opens a picker: current ↔ incoming, base ↔ current ("what HEAD
  changed"), base ↔ incoming. The applied side is NEW; base is old on both
  base pairs.
- Labels `current (HEAD)` / `incoming (feat)` / `base (…)`, marker labels
  elided to 24.
- The block is remembered by its OPENER LINE (the lens's re-check rule).
  ⟳ re-reads it from the buffer; if it's gone (resolved, or lines added
  above) it flashes and keeps the old diff on screen.
- Doors: a **Compare sides** lens button LAST on the `<<<<<<<` line (shed
  first on narrow panes; lens nibble `conflictCompareLens` = 0xf, never a
  real `ConflictChoice`, routed in `conflictLensPress` before the
  resolver), a right-click row ("Compare sides", with "…" on a diff3
  block), and ≡ Git **Compare conflict sides…** under "Resolve conflict
  at caret…".
- Lens colour: started as `th.Subtle` — #32344A, a border grey,
  invisible as text — switched to `th.Text`, same as *Accept base*.

**Tests**
- `conflictcompare_test.go`: two-way door (outside-a-block flash, no
  one-row picker, labels, stats, jump base), diff3 picker (all three
  pairs' direction and jump base), ⟳ re-read + gone-block flash keeping
  the diff, double-click lands on the buffer line, painted lens click
  opens the panel without editing, label helper.
- `compare_test.go`: `TestCompareTexts_ResetsTheSource`.
- Updated pins: menu 175 actions / 192 rows / height 198 / dividers
  `[2, 5, 195]`, Git section 32 rows, custom-actions height 201, the two
  tall-window heights; lens set and context rows in
  `conflictview_test.go`.
- `make test` (race) green.

**Real binary** (run-ced capture): made a real `git merge` conflict in a
scratch repo, clicked the painted Compare sides lens — the panel opened
`Compare · current (HEAD) ↔ incoming (feat)` with `+3 −3`. The capture
also showed tabs drawn as one cell in the diff (raised as N-059).

**Docs**: README conflicts section (new Compare sides bullet + keyboard
door), CLAUDE.md (menu pins, compare-panel rule, conflicts rule).

## Next

Closed: N-049. Declined: None. Raised: N-059. Deferred: None.
Promoted: None. Moved: None. Updated: None.
Full list: `ai_docs/todo/next-list.md`.
