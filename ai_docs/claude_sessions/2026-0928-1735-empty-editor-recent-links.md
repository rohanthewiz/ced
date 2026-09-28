# Session: "No file open" — Recent files · Recent locations links

Session ID: 1dc2f396-9b50-4b2e-bff0-b8b6e6ad2bf4
Date: 2026-09-28

## 1. The prompt

> On the "No File Open" page add Recent Files and Recent Locations links

(From the cats-todo backlog.)

## 2. What shipped

The empty-editor placeholder (`drawEmptyEditor`, app.go) now draws a
third line two rows under its hint:

```
            No file open

Click a file in the tree, or  ≡  for the menu

      Recent files   ·   Recent locations
```

- New `internal/app/emptyeditor.go`:
  - `emptyEditorLinks()` — the ONE geometry for draw and hit-test
    (`btnRect`s, same contract as modals). Returns nil while a tab is
    active, so a click in code can never run a link. One shared row
    centred as a whole when it fits the editor band; stacked one per row
    when narrow; rects clipped to the band; a row below the band is
    dropped rather than painted over a panel/status bar.
  - `drawEmptyEditorLinks()` — Accent + underline (the terminal panel's
    link look), muted `·` separator.
  - `emptyEditorPress(x, y)` — runs the link's action.
  - Helpers `centredRect` (mirrors `drawCentered`'s arithmetic, floors at
    the left edge) and `clipRect`.
- Links run exactly the ≡ verbs `menuRecentFiles` / `menuRecentLocations`
  — a second door, never the only one.
- ALWAYS drawn, even with no history: the verbs already flash why the
  list is empty (the "unavailable row explains itself" rule).
- `handleMouse`: `case a.emptyEditorPress(x, y):` sits after the status
  bar case and before the editor catch-all, so a link press never starts
  an editor drag.

## 3. Tests

`internal/app/emptyeditor_test.go` (9 tests): both links drawn on one
row, drawn text sits exactly in the hit rect and is underlined; no links
under an open tab (draw and hit-test); Recent files click after closing
all tabs opens the picker with the last active file first and no drag;
Recent locations click opens its picker; empty history flashes the
reason; separator press is a miss; narrow band (minWidth window) stacks
and stays inside the band; `clipRect`, `centredRect`.

Gotchas hit while writing them:
- `screenText` needs `a.screen.Show()` after `a.draw()` — without it the
  simulation screen reads blank (and a "not drawn" assertion passes
  vacuously).
- Shrinking the band by growing `sidebarWidth` in a loop never
  terminates — the width is clamped. Use `minWidth` window instead.

`make test` (race) green. Verified in the real binary with the run-ced
capture driver: links drawn; clicking Recent locations with no history
flashes "No recent locations yet…"; open a.txt, `Esc w`, click Recent
files → picker lists `a.txt  sub`.

Commit `4edf5b7`, pushed to `origin main`. README untouched (it never
described the empty screen or these pickers — folded into N-029).

## Next

Closed: None. Declined: None. Raised: None.
Deferred: None. Promoted: None.
Updated: N-029 (README gap now also covers the placeholder links).
Full list: `ai_docs/todo/next-list.md`.
