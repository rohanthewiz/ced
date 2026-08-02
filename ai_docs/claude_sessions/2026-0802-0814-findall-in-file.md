# Session: Find all in file — a peek list with live preview

Session ID: b4d8ef0b-0df3-4af8-905a-4a7c54800f48
Date: 2026-08-02

### Ask

> "Add a find all (in current file) feature that will popup all
> occurrences in compacted rows of scrollable results. Rows should
> include the line number as the first col. Clicking a row should
> temporarily move to that row (keep the popup non-modal). Dbl click or
> enter to accept the new position, Esc to abort and return to the
> original position."

Then three refinements, each of which changed a design decision rather
than adding surface:

> "Position the find all popup vertically before the main editor window
> if possible"

> "center the previewed line in the band" … "make the centering
> conditional - only if off-screen"

> "Also give me an icon to optionally position the findall pane to right
> / or top (default)"

### The house rule it had to break, and why

`CLAUDE.md` says every choose-one-from-a-list UI reuses `openPicker`.
This one can't, and the reason is worth keeping: the palette's grammar
is **pick → run → close**, with dismissal as a no-op. The ask describes
the opposite on all three counts.

- Moving the highlight moves the editor's cursor **live**; Esc puts it
  back. The palette commits nothing until Enter and undoes nothing on
  Esc.
- A click **previews** instead of dismissing. That's what "non-modal"
  means here — the list keeps the keyboard (Enter accepts, Esc aborts),
  but a click leaves it open so you can walk the hits one at a time.
- A row is two columns (number ┃ code), not a label.

Bending the picker into that shape means giving it a preview hook, a
cancel that undoes, and a two-column row — at which point it isn't the
picker any more. So `findall.go` is a dedicated modal, and the exception
is documented in `CLAUDE.md` next to the rule it breaks. It still lives
in the single `App.modal` slot, which is what buys mutual exclusivity,
key/mouse routing, and draw ordering for free.

### Geometry: it displaces the editor, never covers it

A floating popup would hide the line it was previewing. So the popup
takes its rows **out of the editor band**:

```
editorBandRows()  →  what the editor has once the pinned strips take theirs
editorRect()      =  editorBandRows() - findAllPanelHeight(), y = 1 + that
```

`editorBandRows` had to be split out of `editorRect` because every panel
(and now the popup) needs to ask *"what would the editor have left?"* —
a question `editorRect` can't answer, since it already subtracts them.

Height is **fixed**, which is what lets it stack rather than negotiate:
the resizable bottom panels need circular clamp math against each other,
and a fixed-height strip needs none. `findAllMinEditorRows` is the floor
it never eats through.

**The rows come off the TOP** (the first refinement): pinned under the
tab bar, editor pushed down. The list is what you're reading; the code
is the reference under it. That flip cost exactly two lines —
`editorRect` returns `y = 1 + findAllPanelHeight()` instead of a
constant `1`, and the popup's `rect` returns `y = 1` — because every
consumer already read `ey` from `editorRect`: hit-testing, drag
auto-scroll, the hover modal, Alt+click, the tab renderer. That's now
load-bearing: **no call site may go back to assuming the editor starts
at row 1.**

### Two new editor primitives, both about `cursorMoved`

The `cursorMoved` flag (Render consumes it to decide whether to
`EnsureVisible`) turned out to be the crux of both refinements. Neither
could be done from the app layer — the flag is unexported, deliberately.

**`Tab.RestoreView(cursor, anchor, scrollY, scrollX)`** — the abort-a-peek
primitive. Not `MoveCursorTo`, because the captured **scroll** is part of
what's being restored: `MoveCursorTo` sets `cursorMoved`, and the next
Render would `EnsureVisible` over the top of it. A user who wheeled away
from their own cursor before peeking would get a viewport yanked to the
cursor instead of their view back. Clearing the flag also swallows the
pending move the peek itself armed.

**`Tab.CenterOnCursor(viewW, viewH)`** — the second refinement.
`EnsureVisible`'s minimal scroll parks a hit below the viewport on the
**last row**: every line before it, none after, which is useless when
the question is "what is this line doing?". Centering also fixes where
the eye looks — walking the list keeps every hit on the same screen row.
Two details it must keep: horizontal scroll delegates to
`EnsureVisible` (centering is a vertical idea, and a match far out on a
long line still has to come into view), and it clears `cursorMoved` for
the same reason `RestoreView` does.

Both edges stay honest. Near the top of a file the line can't be
centered, so `ScrollY` clamps at 0 rather than pushing the buffer up to
fake it; near the bottom, `clampScroll`'s existing overscroll allowance
(half a view — the deliberate design already in `CLAUDE.md`) is exactly
enough to keep the final line centerable.

Centering is **conditional** — it fires only when the hit is off-screen
(`Tab.CursorLineVisible`). The first cut re-centered unconditionally, on
the theory that a fixed reading position beats a still background; in
practice it scrolls the code out from under a line the user can already
see. So walking a cluster of nearby hits now holds the view still, and
only falling out of the band re-centers. The primitive itself stays
unconditional: that policy belongs to the peek UI, not to the Tab.

### Two docks, one displacement rule

The third refinement:

> "Also give me an icon to optionally position the findall pane to right
> / or top (default)"

Because the popup already **displaces** the editor rather than floating
over it, a second dock was mostly a matter of choosing which axis it
takes from: TOP costs rows, RIGHT costs columns and runs full height.
`editorRect` grew a width term next to its height term
(`editorBandCols() - findAllPanelWidth()`), with exactly one of the two
ever non-zero — and again nothing downstream changed, because every
consumer was already reading the returned rect rather than assuming the
editor's bounds.

The trade between them is real: the strip shows long lines, the column
shows three times as many hits (30 vs 10 on a 44-row terminal).

Width precedence in the right dock copies `gitPanelHeight`: the editor's
reserve caps the column, but the column's own floor is applied **last**
and wins on a band too narrow for both. A list too narrow to read is
worse than a narrow editor, and the real fix at that size is hiding the
sidebar.

**Three surfaces for one preference** (`"findalldock"`, persisted like
`termdock`): the ◨/⬒ button in the title row, `d` inside the popup, and
a ≡ View row. The `d` key isn't redundant — a modal owns the keyboard,
so the ≡ menu is *unreachable from inside the list*, which would have
left a mouse-only path on exactly the terminal (macOS Terminal + tmux)
the whole no-mouse-only rule exists for. The glyph names the layout it
switches TO, not the one in force, matching the toggle-row convention;
both halves are single-width per the marker rule.

Two details the button needed: it's hit-tested **before** the rows and
returns without touching `lastClick`, so a flip is neither a row preview
nor half a double-click accept; and a flip re-runs `preview`, because
the band the hit was centered against just changed.

### The find state is borrowed, not set

The popup calls `SetFindQuery` so the editor tints **every** occurrence
while the list explains them, and tracks `FindIndex` so the previewed
hit paints as the current match for free (`findSource` already reads
it). Both are restored on both exits — the tint has to leave with the
list, same contract as closing the find bar. The prior query is saved
and handed back, so opening the list from an active find bar doesn't
lose the bar's search.

### Rows: compacted once, at open

Leading indentation comes off, and interior tabs render as **one space**.
That second part is the trick: it keeps the display text aligned
rune-for-rune with the buffer, so a match column maps to a display
column by subtracting the trim. No width table, nothing to drift.

Two hits on one line are two rows — the list is occurrences, not lines
(visible in the screenshot: line 63 twice, with a different word lit
each time).

### Ways in

- `Esc F` — the shifted twin of `Esc f`, following the `h/H`, `o/O`
  convention.
- ≡ → Search → "Find all in file".
- **`↓` from the find bar** — the reflex a browser search field trains.
  The bar closes as the list opens (`openModal` → `closeAllModals`), so
  the query is captured first. The bar's hint says so now.

Seeding is a ladder: find bar → single-line selection → word under the
cursor → a prompt. No match **flashes** rather than opening an empty box
the user has to dismiss to be told "no".

### Files

| File | What |
|---|---|
| `internal/app/findall.go` | new — the modal, rows, preview/accept/abort, geometry, draw |
| `internal/app/findall_test.go` | new — 32 cases |
| `internal/editor/tab.go` | `RestoreView`, `CenterOnCursor`, `CursorLineVisible` |
| `internal/app/app.go` | `editorBandRows`/`Cols` split out; `editorRect` offsets; 2 ≡ rows |
| `internal/userconfig/` | the `findalldock` key + `SaveFindAllDock` |
| `internal/app/leader.go` | `Esc F` |
| `internal/app/find.go` | `↓` opens the list; hint text |
| `CLAUDE.md` | architecture map + a house-rules section |
| `internal/*/[*]_test.go` | menu geometry pins; editor + userconfig cases |

### Verification

Driven through the real binary with the `run-ced` PTY capture (not just
`SimulationScreen`), because none of this is visible to a
SimulationScreen assertion alone:

- Esc restores to `Ln 31, Col 1`; Enter lands on the previewed hit.
- Centering: previewing line 63 in a 28-row band scrolls to 49–76, hit
  mid-band. Walking hits at lines 22 → 27 → 31 → 63 held the top line at
  1, then 1, then 17, then 49 — i.e. the two in-band hits moved nothing
  and only the ones that fell out re-centered.
- Both docks draw: `◨`/`⬒` land single-width in the title row, and the
  right column shows 30 rows where the strip shows 10.

`make test`, `go vet`, `gofmt` clean. One pre-existing flake worth
knowing about: `internal/mcp`'s `TestConnect_Handshake` fails under
`-count=3` on a **clean tree** too — not related to this work, but it
will bite the next person who runs the suite with `-count`.
