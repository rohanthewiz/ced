# Session: a resize seam under the Find-all strip

Session ID: `04a997f6-faf8-4f07-9ad6-e0774d067acc`
Date: 2026-09-21
Branch: `main`

## Ask

> Give me a splitter below the find-all section and the editor below

One sentence, one feature, and the interesting part is that the feature
had been explicitly ruled out: findall.go's header comment read "Height
is fixed, so unlike the resizable bottom panels it needs no clamp
negotiation with them", and CLAUDE.md repeated it as a house rule. So
the job was as much about retiring that claim honestly as about adding
the drag.

## What shipped

### The strip's own bottom border IS the handle

No new row is drawn. The top-docked Find-all panel already paints a
bottom border; that row became the seam.

```
┌──────────────────────────────────────────────────┐
│ Find all "rows"              1/107  ⟳  ◇  ◨  esc │
│ ⌕ rows          ⇄ replace…     [ Replace in 107 ]│
├──────────────────────────────────────────────────┤
│   29 │ // That last one drives the geometry…    ✕│
│   30 │ // editor band (editorBandRows …)        ✕│
└─ ↑↓ preview · enter accept · … ─────────━━━──────┘   ← drag this row
   16  // the feature:
   17  //
   18  //   - The grammar is PEEK, not pick. …         ← editor keeps the rest
```

This is the git panels' header rule turned upside down — both sit on the
edge the panel shares with the editor, which is the edge being traded.
Those panels hang off the bottom of the window so their rule is on top;
this one hangs off the top so its rule is on the bottom.

It costs **no rows**, which is the whole reason the border is the handle
rather than a rule drawn beside it — the same shared-cell trade the
overflow markers make for their column. A seam that added a row would
take it from the code the list exists to point at.

No borrowed second row either: splitter.go's two-column grab zone
answers a problem the horizontal seams don't have (a row is a much
easier target than a column), and the row above this one carries a
result the user clicks to preview.

### The stored size is rows, not cells, and is NOT persisted

`App.findAllRows` — `0` means auto (`findAllVisibleRows`), the tool
layer's convention: a strip nobody has dragged re-derives rather than
restoring a number chosen for another window.

Stated in RESULT ROWS rather than cells so the number keeps meaning the
same thing if the chrome ever gains or loses a row.

It lives on `App` beside `findAllDockRight` for the dock's reason — the
popup is transient and the size the user chose isn't, so it survives
closing and reopening the list. But unlike the dock it is deliberately
**not persisted to config**:

> The dock says which SHAPE of answer the user wants and is worth
> carrying between sessions; a height is a moment-to-moment trade
> against the code underneath, re-made every time the strip is opened
> over a different file.

### Both ends clamp at the STORE, not just at the draw

`height()` already clamped what is drawn (`band - findAllMinEditorRows`,
floor at `findAllMinHeight`). `dragFindAllTo` clamps what is *stored* as
well, and that is not belt-and-braces:

> Without it, a drag that swept far below the editor's reserve banks a
> number the user then has to drag back up through before the strip
> moves at all — dead space under the pointer that reads as the seam
> having come unstuck.

### The drag is continued in TWO places, on purpose

The one genuinely non-obvious thing in the change, and the reason
`findAllDragMode` is a named constant rather than a string spelled twice.

- **Unpinned**, the list owns the modal slot. `App.handleMouse`'s
  single-slot absorb (`if a.modal != nil { … return }`) sits *above* the
  whole drag-continuation chain, so a gesture handled only in that chain
  would freeze on the first motion event.
- **Pinned**, the panel is furniture and the router's own branch answers
  first; `findAllModal.handleMouse` never sees those events.

So both carry the same two lines, and both call one mover
(`dragFindAllTo`). The modal's copy also clears `dragMode` on the
release, since the router's release tail is likewise unreachable behind
the absorb.

### No handle in the right dock

`findAllSplitterY()` returns `-1` there — splitterX's own "no seam"
convention, so a hidden or wrong-dock panel can never claim a press.
Docked right the strip is full height by design (a tall column is the
point of that mode) and its bottom edge is the band's own.

### The grip is centred in the rule's FREE span

First cut centred it on the rule and it never appeared — caught by the
test, then confirmed in the real binary. The footer hint
(`↑↓ preview · enter accept · del dismiss · / filter · p pin · d dock ·
esc back`) is left-aligned and is the wider of the two on any strip
narrower than ~120 columns, so a centred grip is the half that never
gets drawn. It now centres in what the hint leaves, and stands down
entirely when even that doesn't fit — the hint is the row's meaning, the
grip only its affordance, and the seam is still findable by a press.

Weight, not hue alone: `━` against the border's own rule, one step up in
color, per splitter.go's rule about terminals whose contrast ced cannot
vouch for. The whole rule lights Accent while the drag is live, at which
point the grip has nothing left to say.

## Tests

Five new ones in `internal/app/findall_test.go`:

| Test | Pins |
|---|---|
| `…_BottomRuleDragsTheHeight` | the full press → drag → release gesture, and that the editor gives up exactly those rows |
| `…_SurvivesReopening` | the size belongs to the strip, not to one popup |
| `…_ClampsBothEnds` | the stored overshoot, and the one-row floor |
| `…_RightDockHasNoHandle` | the one place the seam does not exist |
| `…_GripMarksTheRule` | the affordance actually paints |

`make test` green (race detector, whole tree).

Two test-authoring notes worth remembering: the modal's bounds check
means a synthetic press has to be at `mx+10`, not `x=10` (the sidebar
puts the strip's left edge around column 30, so a bare `10` falls
outside the rect and is routed as accept-and-close); and the grip test is
what caught the centring bug that a passing build would not have.

## Verified in the real binary

`run-ced` capture, `-text`, opening `internal/app/findall.go` and running
Find-all for "rows": the `━━━` grip renders on the bottom rule right of
the hint, with the editor picking up directly underneath.

## Docs retired

The "Height is FIXED" claim was wrong in two places and both were fixed
rather than left to drift:

- `internal/app/findall.go`'s header comment — now says the bottom
  border is the handle, and that it *still* needs no clamp negotiation
  with the bottom panels, for a narrower reason: it is alone on its
  edge, so the trade is with the editor alone.
- `CLAUDE.md`'s Find-all section — same correction, plus a new bullet
  recording the costs-no-rows trade, the rows-not-cells store, the
  not-persisted decision, the two-route drag, and the grip placement.

## Files touched

```
internal/app/findall.go        seam section, press/drag/release, grip paint
internal/app/app.go            App.findAllRows, the router's drag branch
internal/app/findall_test.go   five tests
CLAUDE.md                      the retired house rule + the new one
```
