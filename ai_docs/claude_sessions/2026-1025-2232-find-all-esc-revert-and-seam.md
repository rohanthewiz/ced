# Find all: Esc keeps a clicked row, ↺ revert loc, right-dock seam

Session: `4b05934c-1702-4a67-b6a7-b2109ca1a26b`

## Ask

From the cats-todo backlog, for the Find-all list:

- Once a row has been picked with a single click, Esc should close the
  list and stay at the current location. Double-click keeps dismissing to
  the current location.
- Keep the original location when no row was picked, or when a "revert
  location" ("revert loc") button is pressed at any point.

Then: commit, and "put a splitter between the editor section and the
find section". Asked which layout was meant (the top strip already has a
draggable bottom border with a ━━━ grip); the user chose the RIGHT dock.

## What shipped — `6571d23` Find all: Esc keeps a clicked row; ↺ revert loc

- `findAllModal.clicked`: set only by a single ROW click (in-file mode,
  after `selectRow` in `handleMouse`). Title buttons, the ✕ zone,
  overflow markers and arrow keys never set it.
- `escape` (the list's Esc): clicked + in-file → `accept`; otherwise
  `abort`. Project mode always aborts. Its abort restores nothing, which
  already keeps the user in the file a click opened, and `accept` there
  would open the HIGHLIGHTED row's file, which is wrong after arrowing.
- `captureOrigin`: active tab (by POINTER) + cursor/anchor/scroll, called
  by every producer: `showFindAll` (replacing the inline origin fields),
  `handleProjectSearch`, `openLocationsPanel` (references and other
  location lists) and the workspace-edit receipt.
- `revertLocation`: unpinned → hand back the find tint, close, then
  `switchToTab` (the one funnel) + `RestoreView`. Pinned → the same
  restore, but the panel stays (only ✕ closes it). Origin tab closed →
  flash. No tab at open → just closes.
- Button: `revertRect` left of ⟳, label `revertLabel` = " ↺ revert loc "
  at ≥ `findAllRevertFullW` (56) columns, else " ↺ ". Key twin `r` in
  list focus. Button tip "Revert location: back to where the list
  opened" with keys `r` (empty when pinned).
- Footer hints: in-file says `esc back` → `esc stay` once clicked, plus
  `r revert loc` in the wide forms; project mode's widest form gains
  `r revert loc`.
- README Find-all paragraph and CLAUDE.md's Find-all PEEK bullet.
- Tests: findall_test.go (click+Esc stays, click+arrows+Esc stays on the
  walked row, arrows+Esc restores, button clicks don't arm, revert
  button/key, pinned revert switches tab and stays up, closed origin tab
  flashes, label shrinks narrow, hint flips); projectsearch_test.go
  (revert returns to the origin tab, Esc after a jump stays);
  buttontip_test.go (revert label + `r`).

## What shipped — right-dock seam (this wrap's commit)

- The right-docked column now has a real splitter COLUMN to the left of
  its frame. `width()` counts it (`findAllSeamW` = 1), the way a tool
  window's width includes its splitter, so `editorRect`'s subtraction is
  unchanged. `rect` starts at `ex+ew+1` and is one column narrower.
  `findAllSeamX` is −1 with no list, in the top dock, or when the frame
  is too small to draw.
- Drawn by `drawVSplitterSpan(x, top, rows, active)`, factored out of
  `drawVSplitter` (which now calls it over the side-dock rows), so the
  glyph, grip and colours can't drift from the window seams. Painted
  from `m.draw`, so the pinned (body) and unpinned (overlay) passes both
  get it. The rule lights Accent while dragging.
- Grab zone `findAllSeamHit` = the seam + the frame's left border (the
  PANEL side, per splitter.go), on the list's rows only, never the
  editor's last column or the tab bar.
- Drag `findAllColsDragMode`, `dragFindAllColsTo(x)` with
  `dragSplitOffset` (an unmoved grab of the border is a no-op), clamped
  when stored: editor reserve `findAllMinEditorCols` above, floor
  `findAllMinWidth` below. Width in `App.findAllCols` (0 = auto), session
  only like `findAllRows`.
- Routing: unpinned, `handleMouse` claims the seam BEFORE the
  outside-click accept (otherwise grabbing it would close the list) and
  continues/releases the drag. Pinned, the App press dispatch claims it
  right after `dockSplitterAt`, and the drag chain continues it next to
  `findAllDragMode`. Release goes through the generic one.
- Why a column rather than the border-as-handle used by the top strip:
  code ran straight into the frame, and the editor's last-column
  `›`/overflow markers sat against the list's border. A one-column
  border is also the coin-flip target splitter.go was written to fix.
- Tests: `TestFindAll_RightDockTakesColumnsNotRows` updated (editor +
  seam + frame = band). New `TestFindAllSeam_*`: only in the right dock
  + grab zone, unpinned drag end to end (list stays open), unmoved
  border grab is a no-op, both clamps, pinned press through
  `a.handleMouse`, grip drawn (3 ┃ + rule). Doc of
  `TestFindAllResize_RightDockHasNoHandle` now says it's about the ROW
  seam.
- README ("docked right, drag the splitter between it and the editor")
  and CLAUDE.md's Find-all resize bullet.

## Verification

- `make test` (race) green after each part.
- run-ced (PTY): the title row fits in the top strip and in the 62-column
  right dock (`1/33  ↺ revert loc  ⟳  ◇  ⬒ esc`). The seam shows the ┃
  grip, and a scripted SGR press on the seam (column 89, row 21) → motion
  20 columns left → release widened the list by 20 with the list still
  open.

## Next

Closed: None. Declined: None. Raised: N-090, N-091, N-092.
Deferred: None. Promoted: None. Moved: None. Updated: None.
Full list: `ai_docs/todo/next-list.md`.
