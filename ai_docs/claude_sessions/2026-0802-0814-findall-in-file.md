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

Then two refinements, each of which changed a design decision rather
than adding surface:

> "Position the find all popup vertically before the main editor window
> if possible"

> "center the previewed line in the band"

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

Centering is **unconditional**, including when the hit is already on
screen. Deliberate: a fixed reading position beats a still background.
Easy to gate on "only if off-screen" if it ever feels busy.

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
| `internal/app/findall_test.go` | new — 26 cases |
| `internal/editor/tab.go` | `RestoreView`, `CenterOnCursor` |
| `internal/app/app.go` | `editorBandRows` split out; `editorRect` top offset; ≡ row |
| `internal/app/leader.go` | `Esc F` |
| `internal/app/find.go` | `↓` opens the list; hint text |
| `CLAUDE.md` | architecture map + a house-rules section |

### Verification

Driven through the real binary with the `run-ced` PTY capture (not just
`SimulationScreen`): Esc restores to `Ln 31, Col 1`, Enter lands on the
previewed hit, and after centering, previewing line 63 in a 28-row band
scrolls to 49–76 with the hit mid-band.

`make test`, `go vet`, `gofmt` clean. One pre-existing flake worth
knowing about: `internal/mcp`'s `TestConnect_Handshake` fails under
`-count=3` on a **clean tree** too — not related to this work, but it
will bite the next person who runs the suite with `-count`.
