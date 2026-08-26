# Session: the tree loses its scrollbar, the editor's rail gains a map

- Date: 2026-08-26
- Branch: `main`
- Repo: ced (`~/projs/go/ced`)
- Session id: `f1e65a9c-d0da-4a4f-9156-1d1942b30cad`
- Predecessor: `2026-0823-2253-editor-scrollbar.md`

## What was asked

> Drop the file tree scrollbar. Just give some kind of indicator at the
> last row that there is more.
> The editor scrollbars are also quite ugly. What improvements can we
> make there?

Then, after the first round shipped and two further ideas were floated:

> add the caret tick on the rail and then take on 2.

("2" being the minimap-lite: diagnostic and find marks on the rail.)

## What shipped

Three changes, in the order they were asked for.

### 1. The tree's scrollbar is gone; one marker replaces it

Everything sidebar-side was deleted: `treeScrollbarRect` / `Cols` /
`Contains` / `Thumb` / `Press`, `dragTreeScrollbarTo`,
`drawTreeScrollbar`, the `"treescroll"` drag mode, both mouse-router
cases in `handleMouse`, the `drawTreeScrollbar()` call in `draw`, and
auto-fit's `+ a.treeScrollbarCols()` allowance for the shared column.

In its place, `filetree.drawMoreMarker` paints a muted bold `▾` in the
**last cell of the last list row** whenever rows sit below the fold:

```
 EXPLORER                    │
 app                         │
   catssplit.go              │
   catssplit_test.go         │
   catstheme.go             ▾│   ← more below
```

The argument, which is the part worth keeping:

- **A list asks a different question than a body of text.** "How far into
  this file am I?" needs a thumb's height and position. "Have I seen
  everything?" is a yes/no. The bar answered the second question with the
  first one's machinery, and charged a column of every name for it.
- It lives inside `Tree.Render`, which already holds the flat row list,
  the band split (`ListRows`) and `ScrollY` — so nothing had to be
  plumbed through the App, and **no preference gates it**. It costs no
  layout, so there is nothing to give back by hiding it, and a list that
  silently runs on past its bottom row is the one thing a list must never
  do (the ≡ menu's clipped-content arrows, same rule).
- **Bottom only.** Content scrolled past announces itself — the user did
  the scrolling. Content below the fold does not.
- Position is what tells it apart from the expand chevrons, which are the
  identical glyph: those sit at a row's HEAD against the indent, this one
  at its tail.

Auto-fit now asks for `ContentWidth() + 1` (the splitter) and nothing
more: an allowance for a **one-row** marker would be a column of blank
air on every other row of the tree. That is the trade a shared-column
scrollbar could not make, and the reason the tree stopped having one.

### 2. The editor bar, restyled

The real problem was two unrelated shapes on one column — a box-drawing
`│` hairline centred in the cell with a solid `█` slab bulging out of it,
so the thumb read as a blot rather than as the rail thickened.

Now it is **one shape at two weights, right-aligned**:

| | glyph | color |
|---|---|---|
| rail | `▕` (U+2595, one-eighth block) | `Subtle` |
| thumb | `▐` (U+2590, half block) | `Muted`, `Accent` while dragging |

Same Block-Elements family the git gutter's `▎` comes from, which is also
the proof the owner's terminal renders it. Right-aligned matters: the ink
sits against the window's edge instead of crowding the code beside it.

Also `scrollbarMinThumb = 2`. A one-cell thumb is legible as a mark but
not as a **handle**, and the arithmetic produced one exactly where
dragging matters most (a file thousands of lines long). Floor before
ceiling, ceiling wins, or on a track shorter than the floor the thumb
places off the end of its own track.

The ≡ row and the flash went singular now that there is one bar:
"Hide scrollbar" / "Scrollbar on".

### 3. The rail became a minimap of positions

New file: `internal/app/scrollbarmarks.go` (+ `_test.go`).

```
▕   ← rail: nothing out there
▐   ← error, off screen above
▕
━   ← the caret, left behind by a scroll
▐▐  ← two rows carrying find hits
▐   ← the thumb (marks under it are suppressed)
▐
▕
```

- **Caret tick**: `━` in `Accent`. Deliberately NOT from the block family
  — a horizontal stroke reads as a pointer at a POSITION, where one more
  vertical segment would read as another piece of rail, and would be a
  second accent-colored block on a bar whose thumb turns accent the
  moment it is dragged.
- **Marks**: one `▐` per off-screen diagnostic (LSP + plugin, the theme's
  three `Diag*` colors) and find hit (`FindCurrent` — `FindMatch` is a
  background TINT and is invisible as a foreground).

House rules, which are the whole design:

- **Only what is OFF SCREEN.** On screen the gutter dot, the underline
  and the find tint are already there against the code, saying it better
  and in place. That also keeps the rail silent on a file that fits and
  informative on a long one. Same argument for the tick: while the cursor
  is visible the hardware cursor IS the answer, and a tick would be a
  second cursor to explain.
- **Nothing paints over the thumb.** It is the one thing on the column
  that has to keep reading as a shape, and it covers the rows whose marks
  are suppressed anyway.
- **`railKind` is ordered by precedence: caret > find > error > warn >
  info.** A rail row stands for many lines (5,000 lines through a 40-row
  track is 125 lines per cell), so collisions are the NORMAL case. The
  caret wins outright because it is the only UNIQUE mark — lose its cell
  and the feature has silently failed — while a diagnostic is redundantly
  reported by the status bar's counts, the Problems panel and its own
  gutter dot. Find over the diagnostics mirrors `collectDecorations`
  putting `findSource` last: a question the user asked outranks ambient
  annotation.
- **`railRow` is the ONE line→row mapping**, shared by the tick and every
  mark, so two things on the same line can never land on different rows.
  Proportional over the whole FILE — the measure that gives the thumb its
  HEIGHT — and deliberately not the thumb's POSITION formula, which is
  measured against `MaxScroll` because it maps scroll offsets, not lines.
- **Sources are read from their CACHES, not through `DecorationSource`.**
  Sources are asked per visible window by contract, and this feature's
  subject is the rest of the file; asking each for the whole buffer would
  turn a per-frame read into a full-file walk for the word highlighter
  and the git differ, neither of which has anything to say here. So:
  `lsp.diags`, `plugins.decos` (gated on the kill switch at the READ,
  like every other plugin surface), and `Tab.FindMatches`, which is
  already whole-buffer.
- `railMarks` returns **nil** — zero allocation — on a clean file with no
  search running, which is the common case.

## Two things deliberately NOT built

- **Git changes on the rail.** They would be on the rail of nearly every
  file you actually work in, which is precisely when the bar most needs
  to read as a position — and a change bar is state you already own, not
  somewhere you were about to jump. The rail is for what you would go and
  look at.
- **A mark as a click target.** A press on the track still PAGES toward
  the pointer, per the bar's own rule: paging is reversible, and a cell
  that jumped somewhere would make one gesture mean two things depending
  on where in the column you happened to land.

## Files touched

```
internal/filetree/filetree.go        drawMoreMarker + treeMoreRune
internal/filetree/filetree_test.go   TestRender_MoreMarker
internal/app/scrollbar.go            tree bar deleted; rail/thumb glyphs;
                                     scrollbarMinThumb; drawScrollbar
                                     paints the marks; singular ≡ label
internal/app/scrollbar_test.go       tree-bar tests dropped, metrics
                                     re-pinned + a new degenerate case
internal/app/scrollbarmarks.go       NEW — railKind, railRow, railMarks
internal/app/scrollbarmarks_test.go  NEW
internal/app/app.go                  two mouse cases + one draw call gone
internal/app/treeautofit.go          the shared-column allowance gone
CLAUDE.md                            "Scrollbars" split into three
                                     sections; architecture map updated
```

## Verification

- `go test ./... -race` green; `gofmt` clean.
- Driven live through `.claude/skills/run-ced`'s PTY capture: the `▾`
  marker on an overflowing tree, the new rail/thumb down `app.go`, and
  the find bar open on `autoScroll` with its off-screen hits amber on the
  rail.

## Notes for next time

- The caret tick cannot be exercised by the capture tool: every scripted
  keystroke moves the caret, and the tick only appears when the VIEW
  moves without it (a wheel scroll). `TestScrollbarDraw_MarksAndTick`
  covers it, and it has to use `Tab.RestoreView` rather than
  `MoveCursorTo` — every ordinary cursor write sets `cursorMoved`, and
  Render would scroll the caret straight back on screen.
- If a third rail source is ever wanted, the question to ask it is the
  one git failed: is this somewhere the user would GO, or state they
  already own?
