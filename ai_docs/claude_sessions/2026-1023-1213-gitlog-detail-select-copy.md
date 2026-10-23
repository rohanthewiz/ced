# Git log detail: drag-select + copy

Session: `64bee60f-c1e0-44f7-8c8f-98b9f2a0a529`

## Ask

"I can't seem to select text for copy-paste on the right side of the git
log panel" (screenshot of the commit detail pane). Then `/sw` and a
tag-only patch release.

## Cause

ced captures the mouse, so the terminal's own drag-select never reaches
any panel. Only the chat transcript had its own selection; the diff
panes (git log detail included) had none.

## What shipped

### diffselect.go (new) — selection on the shared diff layer

- State `diffSel` lives on `diffPane` in DISPLAY-row space (row index,
  rune column of the row's display text). `diffPane.layout` clears it on
  rebuild, so new commit text or a split toggle never inherits a stale
  highlight.
- Split mode: ONE side per drag, fixed at the press; holes on that side
  skipped; full rows (headers, @@, metadata) copied whole.
- Text = the display form the painter draws (tabs expanded), so a cell
  column IS a rune offset. Unified rows keep their +/-/space prefix;
  split sides drop prefix and gutter.
- Clipped rows: a row passed through copies whole; a pointer on the `…`
  cell means "to end of line" (the pane has no horizontal scroll).
- Drags off the pane clamp to the VISIBLE rows (above → start of top
  row, below → end of bottom row).
- `diffSelTextRect` is the one geometry for hit-test + painter, mirroring
  drawDiffRows / drawDiffSide. `paintDiffSelection` is an overlay at the
  end of `drawDiffRows` (re-backgrounds cells with `th.Selection`, the
  overflow.go `GetContent` idiom), so emphasis / ▸ are untouched.

### Git log wiring (gitlog.go, app.go)

- `gitLogDetailPaint()` — the ONE paint geometry for draw and hit-test;
  `drawGitLogDetail` lost its now-redundant params.
- `gitLogPress` arms `"gitlogsel"` AFTER `gitLogClick`, so the
  double-click jump still works (its second press arms an empty
  selection that copies nothing).
- COPY ON RELEASE (`gitLogSelFinish`) — tmux copy-mode's habit, and the
  only door every host delivers (⌘C arrives only via the kitty protocol,
  which tmux usually doesn't forward). Goes through `chatCopy` (clipBuf
  + OSC 52 via the `copilotCopyCode` seam), flash "N lines copied".
  Plain click: no copy, no flash.
- Esc side effect clears it; a press outside the panel clears it (so ⌘C
  after an editor selection copies the editor's text).
- ⌘C with a live log selection re-copies it instead of falling through
  to `cmdCopy`, which would overwrite the clipboard with the file path.

### Docs

README "Diff views" gains a *Copying from the git log* paragraph;
CLAUDE.md Diff views gains the drag-select rule bullet.

## Tests

- diffselect_test.go (8): unified partial-row drag text, plain click
  selects nothing, press outside arms nothing, split side lock + hole
  skip + below-pane clamp, … takes the tail, above-pane clamp, layout
  rebuild clears, rect matches painted text (SimulationScreen), overlay
  backgrounds only selected cells.
- gitlog_test.go (4): real `handleMouse` press/motion/release copies +
  flashes "3 lines copied" and keeps the highlight; plain click copies
  nothing; Esc and outside press clear; ⌘C copies the selection.
- `make test` (race) green.
- run-ced: real binary, `Esc L`, scripted SGR press (0) / drag (32) /
  release (m) across the commit message → status bar "4 lines copied",
  selection fill painted.

## Release

Tag-only patch release **0.7.1** (version.go + cats-plugin.toml bumped,
`Release ced 0.7.1`, annotated tag `v0.7.1`).

## Next

Closed: None. Declined: None. Raised: N-086, N-087. Deferred: None.
Promoted: None. Moved: None. Updated: None.
Full list: `ai_docs/todo/next-list.md`.
