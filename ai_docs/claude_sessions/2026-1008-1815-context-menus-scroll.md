# 2026-10-08 18:15 — Anchored context menus scroll (N-039)

Session: `c4f00d54-2cc0-479d-a380-bf8f974bc93d`

## Request

From the cats-todo backlog, next-list item **N-039**: `editorContextModal`
does not scroll; on a short window `placeContextSized` clamps an overlong
menu to row 0 and its bottom rows fall off the screen. The editor
right-click menu has the same limit.

## Findings

- The premise ("a window shorter than about 22 rows") cannot occur:
  `draw` shows the too-small screen below `minHeight` (24), so at the
  smallest drawn window the tab menu (≤ 20 with border) and the tree menu
  (≤ 18 rows + border) fit.
- The editor right-click menu genuinely overflows at 24 rows: conflict
  resolver rows are prepended, and cats split/agent rows, bookmark rows
  and the search row are appended — ~24 rows + border on a conflict in
  cats.
- The tree's `contextModal` had the identical flaw (rect = len(items)+2),
  so it was fixed through the same helper.

## What changed

- **`ctxScroll`** (internal/app/contextmenu.go), shared by both anchored
  menus:
  - `offset` re-clamps the stored top on every read (the
    `menuScrollOffset` rule — draw and hit-test can't disagree after a
    resize).
  - `reveal` scrolls only when the keyboard moves the hover.
  - `pointer` is the single row↔screen mapping: wheel inside the popup
    scrolls (`wheelLines`), hover re-read after the move; a left press on
    a border that carries ▲/▼ pages (`rows-1`); press outside dismisses.
  - `drawCtxScrollMarks` paints the ≡ menu's ` ▲ ` / ` ▼ ` on the borders.
- `App.ctxMenuRows(y, count)` = visible rows for a popup at row y
  (floored at one); both `rect`s use it.
- `placeContextSized` places by the CLIPPED height
  (`min(count, height-2) + 2`), so the box always ends inside the window.
- `editorContextModal` (editor, preview, tab, tab-group, problems, git
  log, conflicts menus) and `contextModal` (tree) draw only the window,
  keep the hover visible on Up/Down, and route the mouse through
  `ctxScroll.pointer`.
- CLAUDE.md: new cross-cutting rule "Anchored context menus scroll".

## Tests (contextmenu_test.go)

- `TestCtxScroll_OffsetRevealAndScrollBy`, `TestCtxMenuRows_ClipsToWindow`
- `TestPlaceContextSized_TallMenuStaysOnScreen` (the regression)
- `TestEditorContextModal_ScrollsWhenTallerThanTheWindow` — at
  `minHeight`: ▼ then ▲, Down reaches the last row, Enter runs it.
- `TestEditorContextModal_WheelAndBorderPage` — wheel, wheel outside is
  inert, border paging both ways, a click after scrolling runs the row
  drawn there.
- `TestTreeContext_ScrollsWhenTallerThanTheWindow`

`make test` (race) green. Not driven in the real binary.

## Next

Closed: N-039. Declined: None. Raised: None. Deferred: None.
Promoted: None. Moved: None. Updated: None.
Full list: `ai_docs/todo/next-list.md`.
