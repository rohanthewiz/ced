# Session: Clickable overflow markers — click to page, double-click to the end

- Date: 2026-09-10
- Branch: `main`
- Repo: ced (`~/projs/go/ced`)
- Session id: `01EZ7QsbPQUKFLDxTwEVibjJ`
- Predecessor: `2026-0910-1814-favorites-management-and-open-in-editor.md`
- Commits: `2dfba24` (code), `1d49ab3` (this doc)

## What was asked

One prompt:

> Single click on a scroll indicator arrow should page up or down
> respectively. Also double-click should take us to the top or bottom of
> the doc respectively.

The "scroll indicators" are the `▴` / `▾` overflow markers (overflow.go)
— what ced has instead of a scrollbar. Until now they were pure report:
drawn in the last column of a viewport's first and last row, colored by
the loudest thing off-screen, with a dwell popup naming the counts. They
had no gesture at all.

## The shape of the change

The marker becomes a **target**, which is the one thing the retired rail
could do that a glyph could not — a scrollbar's trough is clickable. It
is worth more on the marker, because the glyph is drawn at the very edge
the reader is already looking at rather than off in a column of chrome.

It is still not a *verb* in the sense the Find-all rule means: the only
state a press may change is which part of the surface is on screen. Never
the document, the selection, or the worklist.

### One gesture, every surface

`overflowMarkerPress(x, y, scroll func(int)) bool` is the whole gesture,
and it takes the surface's **own mover**:

- the router (`App.overflowMarkerClick`) hands it `scrollAt`, which
  already knows how to move whichever panel a cell belongs to — editor,
  markdown preview, file tree, both git panels' independently-scrolling
  panes, the pinned Find-all list;
- an **unpinned** Find-all list owns the modal slot, so the router never
  reaches the panel dispatch `scrollAt` is built on. It calls in from
  `findAllModal.handleMouse` with `scrollList` instead
  (`findAllModal.overflowClick`).

Two call sites, one gesture — the same split `drawOverflowMarkers` /
`drawOverflowMarkersOverlay` already makes for the paint.

### Both distances are COUNTED, not assumed

`overflowMarker` gained a `page` field (its viewport's height), stamped
by the enumerator alongside `off`, so the click can be exact:

- **A page** is the viewport less one row of overlap (the line you
  stopped reading on stays on screen), floored at 1, and **capped at
  `off.lines`**. Unclamped, a page down with three lines left would run
  into `clampScroll`'s overscroll pad and answer an arrow that said
  "3 lines below" with half a screen of blank rows. That is a fine thing
  for a wheel to do on purpose and a poor answer to a click.
- **The end** is `off.lines` exactly, so the last line lands on the last
  row rather than in the middle of the pad. The marker's own count is the
  distance to the edge on every surface here — the one number nobody has
  to re-derive.

### The press is claimed even when the marker has just gone

This is the non-obvious half. Double-clicking `▴` with one page left
above scrolls to the top on the **first** press — which is exactly the
moment the marker stops being drawn. The second press, finding nothing
there, would fall through to the surface underneath and move the caret
(or open the file in the tree).

So `App.overflowClick` (`overflowClickRecord`) remembers the cell **and
the direction**, which is why it is not `App.lastClick`. A second press
at the same cell inside `doubleClickMs` is read as the rest of the
gesture: it runs to the edge if a marker is still there, and is simply
swallowed if the first press already got there. A triple click clears the
record — there is nowhere further to go, and re-arming would make a
fourth press page back.

### Where it sits in `handleMouse`

After the dwell popup, the overflow popup, the commit receipt, which-key
and the completion popup — all of which are drawn **over** the editor, so
they outrank an annotation on the text below them. Before the right-click
branch (no conflict; ours is Button1 only) and before the drag branches.

Being ahead of the drag branches is what forces the `dragMode == ""`
guard: it has to run before them, or a press on a marker would start a
selection before anyone asked whether it was a marker — but without the
guard a splitter drag whose pointer swept through the editor's last
column would page the file it passed over.

## The one contract that changed

`findAllModal.handleMouse`'s marker carve-out used to end "the press
falls through to plain selection". It now goes to the marker's gesture.
`TestFindAllRowClick_MarkerIsNotADismiss` was updated to match: it still
pins that the press is **not** a dismissal (striking a row off because
the user pointed at "20 results below" is the one way this annotation
could cost them something), and now also pins that the selection stays
put while the list pages.

## Tests

Five new in `overflow_test.go`, driving real `tcell.EventMouse` presses
through `handleMouse` — the router is the thing being pinned, since the
same event would otherwise have moved the caret:

- `TestOverflowClick_PagesTheEditor` — a page each way, caret untouched.
- `TestOverflowClick_PageStopsAtTheEdge` — three lines left travels three
  lines, and the marker leaves.
- `TestOverflowDoubleClick_RunsToTheEnd` — the jump, plus the
  vanished-marker case (second press swallowed, caret unmoved).
- `TestOverflowClick_LeavesOrdinaryCellsAlone` — the cell beside the
  marker, a right press, and a press mid-drag are all unclaimed.
- `TestOverflowClick_PagesTheTree` — the same gesture on the sidebar
  moves the tree and does **not** open the file whose row the glyph
  shares a cell with.

`go test ./...` green.

## Files touched

```
internal/app/overflow.go       the gesture, the record, page/end math, header rules
internal/app/app.go            App.overflowClick + the hook in handleMouse
internal/app/findall.go        the modal-slot call site (scrollList as the mover)
internal/app/overflow_test.go  five new tests
internal/app/findall_test.go   the changed contract
CLAUDE.md                      the overflow-markers section
```

## For next time

- The unpinned Find-all list is now clickable but still has no **popup**
  (it paints below the overlay layer). That gap is unchanged and still
  documented; pinning (◇) restores it.
- Surfaces with no markers at all — compare panel, problems panel, chat,
  terminal — get nothing from this. If markers are ever extended there,
  the click comes along for free via `scrollAt`.
