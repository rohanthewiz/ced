# Session: a markdown viewer mode for .md files

- Date: 2026-09-09
- Branch: `main`
- Repo: ced (`~/projs/go/ced`)
- Session id: `01S5PF5oJJdtnMwhSLvBwYoh`
- Predecessor: `2026-0904-1021-tty-input-drain.md`

## What was asked

> please add a markdown viewer mode for .md files

Six words, and one of them was the trap: **"mode"**. The editor already
has a mode — `imageMode` — and copying it would have been wrong in a way
that only shows up much later. See the first design note below.

A follow-up question, *"Are markdown tables handled?"*, is answered at
the end.

## The shape, and why it isn't the image viewer's

`internal/editor/image.go` was the obvious precedent: a tab that draws
something other than text. But an image tab and a previewed markdown tab
are not the same animal:

| | image tab | markdown preview |
|---|---|---|
| has text underneath | no | **yes, editable** |
| `Save` | refuses | works |
| dirty flag, undo, caret | meaningless | **all live** |
| reversible | no | **that is the whole feature** |

So `Mode` was left alone and the preview is a **flag beside the buffer**,
`Tab.mdView`. The buffer, undo history, dirty flag, caret, find state,
auto-save, the LSP sync and the git gutter all keep running underneath —
only the *drawing* changed.

`TestSetMarkdownView_LeavesTheBufferAlone` is the pin that matters most
in the whole change: a rendering edit that quietly wrote to the buffer
would be invisible until it turned up in somebody's diff.

## What shipped

### `internal/editor/markdown.go` — the view, the cache, the paint

```go
func IsMarkdownPath(path string) bool
func (t *Tab) MarkdownCapable() bool
func (t *Tab) IsMarkdownView() bool
func (t *Tab) SetMarkdownView(on bool)
func (t *Tab) InvalidateMarkdown()
func (t *Tab) MarkdownRows(th theme.Theme, width int) []MDRow
func MDContentWidth(paneW int) int
func (t *Tab) MDMaxScroll(total, viewH int) int
func (t *Tab) MDScrollBy(delta, total, viewH int)
func MDRowForLine(rows []MDRow, line int) int
func MDLineForRow(rows []MDRow, row int) int
```

Three decisions here:

- **`MDScroll` is its own counter, not `ScrollY`.** A display row is not
  a buffer line — a paragraph becomes several, a heading gains a rule, a
  fence loses its markers — so borrowing the field would let scrolling
  the preview silently rewrite the position the *source* view gets
  restored to. Restoring the view the user left is the point of a toggle.

- **Rows are derived and memoized on `(EditRev, width)`** — the chat
  transcript's `chatRows` shape. A resize re-flows for free, an edit
  invalidates by itself. A **theme switch has to invalidate explicitly**
  (`restyleTabs` now calls `InvalidateMarkdown`) because the rows carry
  resolved `tcell.Style`s: they are the second cache of theme-derived
  color in the editor, and `Tab.Styles`' rule applies to them.

- **`MarkdownRows` is exported because draw and hit-testing share it**
  (the btnRect rule). Scrolling, the double-click's row→line mapping and
  the overflow markers' counts all ask through it at
  `MDContentWidth(paneW)`, so none of the four can disagree with what is
  on screen.

### `internal/editor/markdownblocks.go` — blocks → display rows

A single forward walk with lookahead. **No document tree**: a tree would
buy nesting only the block quote needs, and that recurses into a fresh
`mdRenderer` over its own stripped lines instead, prefixing the rows that
come back with `▎ `. The flat walk is what makes **every row able to name
its source line** — the property that turns the preview from a picture
into a place you can click.

```go
type MDRow struct {
    Runes  []rune
    Styles []tcell.Style
    Src    int // buffer line, or -1 for a synthesized row
}
```

Covers ATX and setext headings (rule under the top two levels), fenced
and indented code, block quotes to any depth, ordered / unordered / task
lists, GFM pipe tables, thematic breaks, YAML front matter.

Two rules hold throughout:

- **Prose word-wraps, code hard-wraps** — the chat transcript's split.
  A code line's columns are meaningful; a paragraph's are not.
- **A row is padded to full width only when the block owns a background**
  (code, table header). Padding everything would make a selection-less
  viewer look like it had a selection.

### `internal/editor/markdowninline.go` — one line → styled spans

Hand-written scanner, no dependency — the `internal/skills` frontmatter
argument: the whole surface is a dozen delimiters, and the promise is one
static binary with no CGO. A CommonMark library brings a document model
this viewer never uses (it does not round-trip and never emits HTML).

Documented divergences from the spec, each with a reason in the header:

- Emphasis is matched by scanning forward for a closing run, not by
  CommonMark's left/right-flanking algorithm. On real prose they agree;
  the cost of being wrong is a few cells of italic.
- **An intraword `_` is not emphasis.** `snake_case_names` are far more
  common than mid-word italics in a code-adjacent document, and treating
  them as delimiters is the single most visible way this could be wrong.
- **A link renders its TEXT, not its URL.** A terminal has nothing to
  click, and repeating every href would double a link-dense document to
  say what the source says one keystroke away. Autolinks and bare URLs
  *are* their own text, so those survive.
- Reference-style `[text][ref]` is left literal — resolving it needs a
  definition table this reader does not build.
- Raw HTML passes through muted.

### `internal/app/markdown.go` — the toggle and the pane's four differences

`esc v`, the ≡ **View** row, and the status bar's `preview` segment, all
through one write path (`toggleMarkdownView`, the `setWordHighlight`
shape). The four things the editor pane does differently:

1. **Scrolls by rows** — `scrollAt` routes to `markdownScroll`.
2. **Double-click leaves the preview with the caret on that line**
   (`MDLineForRow`); Enter is its keyboard twin, landing on the row at
   the top of the viewport.
3. **Swallows editing keys with navigation carved out.** Dropping *every*
   key (the image tab's rule) would make the preview unscrollable from a
   keyboard, which on a terminal that eats clicks means unreadable;
   passing them through would type into a buffer with no visible caret.
   Leaders dispatch far above this branch, so `esc v` still gets the
   source back and the ≡ menu stays reachable from inside a preview.
4. **Overflow markers count rows, not lines**, with the unit reading
   "rows" — none of the sources `editorOffscreen` reads has anything to
   say about a display row.

### `HighlightLang` on `internal/editor/highlight.go`

A fence names its language as an info string, not a filename, so
`Highlight` could not serve it. `highlightWith` was extracted as the
shared token walk and both entry points now differ only in how they pick
a lexer and what an unstyled rune falls back to.

**One Chroma pass per block, not per line** — the lexer is stateful (a
string literal spans lines), so per-line calls would restart it and
mis-color every multi-line construct.

The `base` parameter is why the split was needed at all: a code block
paints its own background, so unstyled runes must inherit *that* rather
than the editor's, or every gap between two tokens punches a hole in the
slab.

This is also the reason the renderer lives in package `editor` rather
than in an `internal/markdown` beside `internal/diff`: the highlighter is
right here, and a flat code block is the biggest thing a markdown reader
*for programmers* could get wrong.

## The color bug, caught by a screenshot

First cut painted code blocks on `theme.LineHL`. It built, it tested, it
looked fine in the text capture. Then the HTML capture:

```
tokyo-night     bg #1a1b26   code #1f202e     ← five units. invisible.
solarized-light bg #fdf6e3   code #f2ecd8
```

`line-hl` is the **active-line wash** — a few units off the background
*by design*, which is right for one row under the cursor and useless
across a twenty-row slab. Exactly the failure the standing memory note
warns about ("ambient so make it quiet" ships something invisible).

The fix follows the theme package's own rule — *adding a color key never
invalidates a theme file somebody already wrote, give it a derivation* —
so `md-code-bg` is now a derived key stepping toward `line`, the
separator color:

```go
{"md-code-bg", func(p Palette) string { return mix(p["bg"], p["line"], 0.55) }},
```

```
tokyo-night     bg #1a1b26   code #27293a     ← a real step
solarized-light bg #fdf6e3   code #e7dfca
```

`TestDerive_MDCodeBGIsVisiblyOffTheBackground` holds every shipped theme
to a per-channel distance of ≥8 **and** ≤60 — the upper bound is there so
the slab can never grow into a second selection. `Default()` gained the
literal `#27293a`, and `TestBuiltin_TokyoNightMatchesDefault` confirmed
it against the derivation on the first run.

## Two more things the captures caught

- **Tabs inside a code fence collapsed to one cell.** The source view
  expands a tab by advancing to the next stop and painting spaces
  (`Tab.Render` via `RuneVisualWidth`); the preview draws every cell
  literally, so it has to expand them itself. `codeCells` now does, at
  the same `TabStop`, so a fence and the file it was copied from line up.

- **The table rule used `┼` at both ends**, drawing four stubs poking out
  of the table's sides. Now `├─┼─┤`.

Neither was reachable from a unit test that only asserted content.

## Placement

The ≡ row went **below** the terminal rows in **View** — the first
placement pushed "Dock terminal left" under the fold and
`TestMenuLayout_TerminalRowsAboveTheFold` caught it. It dims on a
non-markdown file rather than flashing a reason: there is nothing to say
beyond "this isn't one", which the filename already says.

Leader is **`esc v`** (View / preView), in the flat table rather than a
namespace because it is a one-key toggle on the file in front of you,
reached for mid-read.

Menu geometry pins moved 129 → 130 group actions (146 → 147 rows, height
152 → 153, dividers `[2, 5, 150]`, custom-actions height 155 → 156).

## No config key

Deliberate, and the one place this diverges from most of the editor's
toggles. The preview answers *"how do I want to look at this file right
now"* — and the same file is a document one minute and something you are
editing the next. A persisted "preview .md by default" would also have to
decide what happens when you type into a preview, and *"you cannot"* is a
bad thing to discover by surprise on a file you opened to fix a typo in.

## The follow-up: are tables handled?

Yes. Verified against a fixture with ragged rows, missing outer pipes, an
escaped pipe, and inline markup in cells:

- Column widths size to the **rendered** cell, so `**Tables**` measures 6
  rather than 10.
- `:---` / `:-:` / `---:` alignment honored.
- Outer pipes optional; short rows pad, excess cells drop (GFM's rule).
- `x \| y` stays one cell holding a literal pipe.
- Narrow windows shrink columns **widest-first** and truncate with `…`,
  so a short numeric column keeps its width while the prose ones give way.

Two refusals worth keeping:

- **A table needs two lines.** A single line with a pipe is
  overwhelmingly likely to be prose or a shell command — `run ls | grep
  go` stays a sentence.
- **The delimiter row must actually be one.** `| a - b | c |` is
  rejected, so a prose row with a dash can't open a table.

**Known gap:** cells do not wrap — a long cell truncates rather than
growing the row to two lines. Wrapping would mean multi-line rows, which
breaks the one-display-row-per-source-row mapping the clickable preview
depends on. Worth revisiting if it bites in practice.

## Files

New:

```
internal/editor/markdown.go            + _test.go
internal/editor/markdownblocks.go      + _test.go
internal/editor/markdowninline.go      + _test.go
internal/app/markdown.go               + _test.go
```

Touched: `internal/editor/tab.go` (the fields + the Render branch),
`internal/editor/highlight.go` (`HighlightLang`, `highlightWith`),
`internal/app/app.go` (key/mouse/scroll routing, the ≡ row),
`internal/app/leader.go`, `internal/app/statusbar.go`,
`internal/app/overflow.go`, `internal/app/theme.go`,
`internal/theme/palette.go`, `internal/theme/theme.go`, `CLAUDE.md`.

## Verification

`make test` green under `-race`, `go vet` and `gofmt` clean. Beyond the
suite, every visual claim in this doc came from `run-ced` driving the
real binary through a PTY — which is what caught all three defects above.

Artifact with the captures:
<https://claude.ai/code/artifact/54847a9f-1c4c-4f7e-80bc-b19e0fa29c1d>
