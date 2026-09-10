# Session: Every resizable seam is findable and grabbable

- Date: 2026-09-10
- Branch: `main`
- Repo: ced (`~/projs/go/ced`)
- Session id: `0d86d3a6-d2c7-4121-a249-9d7a199e007c`
- Predecessor: `2026-0910-1330-markdown-preview-context-menus.md`
- Commit: `3247532`

## What was asked

> In the recent commit `bc4ab0f` we improved the splitter within the git
> panel. Apply the same fix if you think it is appropriate to all
> splitters in CEd. From the user perspective, the ergonomics is way
> better.

One prompt, with the judgment call handed over explicitly ("if you think
it is appropriate"). So the work was as much about deciding what *not*
to port as about porting.

## What bc4ab0f had fixed, and which thirds travelled

`bc4ab0f` fixed the git panels' list/diff seam three ways at once:

| the fix | did it apply elsewhere? |
|---|---|
| ceiling stated as the neighbour's reserve, not a `MaxW` constant | **no** — already true everywhere |
| grab zone widened from 1 column to 3 | **yes**, but narrowed to 2 |
| a grip glyph on the middle rows | **yes**, verbatim |

The ceiling third had nothing to do. All three window seams already
clamp with `a.width - minEditorAfterDrag` (minus whatever strip owns the
other edge) — a *reserve*, not a constant of their own. There was no
`gitPanelMaxListW` equivalent to remove.

## The seams in question

Five, of which three were untouched by the earlier commit:

| seam | drag mode | `x` |
|---|---|---|
| sidebar / editor | `sidebar` | `splitterX()` |
| left-docked terminal / editor | `termsplit` | `termSplitterX()` |
| chat strip / editor | `chatsplit` | `chatSplitterX()` |
| git changes panel, list / diff | `gitlistdiv` | already fixed |
| git log panel, list / detail | `gitlogdiv` | already fixed |

The horizontal seams (each bottom panel's header rule: `gitpanel`,
`gitlog`, `comparepanel`, `problems`, `termpanel`) were deliberately left
alone — a full-width row is an easy target already, and unlike the
vertical case both of its neighbours are content rather than margin.

## The zone is two columns, not three, and the asymmetry is the point

First cut took the git seam's symmetric `dx-1 … dx+1`. It went red on
`TestGitPanelReviewColumn`, which was the whole lesson:

- the sidebar seam sits at `sidebarWidth-1`
- the editor band therefore starts at `sidebarWidth` — i.e. `dx+1`
- and when the git panel is open, that column is its **review column**,
  a one-cell click target that ticks a row for the survey

Same shape in the flipped layout (tree docked right, whenever a chat or
terminal strip owns the left edge): there `dx+1` is the file tree's
**mark gutter**, `x-sx == 0` in treemarks.go — the multi-selection's
tick, again one cell, again with no second mouse path.

So the rule became: **a window seam borrows the column on its LEFT and
never the one on its right.**

- Left of a window seam is the panel it resizes, and every one of them
  stops a column short of the rule: `chatPanelRect` / `termPanelRect`
  return `stripW-1`, and `chatActionRect`'s ⧉ buttons stop a further
  column short of *that*. Classic-layout sidebar borrows the tree's row
  tail, which is text but whose whole row is the click target anyway.
- Right of it is the editor band, whose first column belongs to whoever
  is docked there — and two of those owners put a deliberate one-cell
  control in exactly that cell.

The git panels' *internal* seams keep their symmetric three columns,
because that commit had verified both neighbours were blank there (the
list truncates a column short, the diff pane leaves a gutter). A window
seam cannot make that claim, so it doesn't. Two columns still doubles
the target, which is the ergonomic win the user was after.

## The side effect that forced `dragSplitOffset`

Widening the zone broke a guard that had been correct for years.

`handleMouse`'s sidebar drag glued the rule to the cursor (`want = x+1`),
and gated `lockTreeAutoFit` on `want != a.sidebarWidth` — "a press with
a pixel of jitter isn't a statement about anything, and this writes a
preference to disk."

That gate only worked because the *only* way to start the drag was to
press the rule itself, so jitter inside one cell produced
`want == sidebarWidth`. Grab the seam one column off and the very first
motion event computes a different width — which resizes the sidebar *and*
turns auto-fit off, silently, on what the user experienced as a click.

Fix: `App.dragSplitOffset`, stamped at press time as `x - dividerX`, and
subtracted on every motion. The seam now tracks the pointer from where it
was actually seized. Applied to all five seams (the git ones too) so they
all feel the same, and reset on release and in `closeAllModals`.

`TestDragSplitOffset_AnUnmovedGrabChangesNothing` pins it: seize by the
borrowed column, emit a motion at the same column, assert the width is
unchanged *and* `treeAutoFit` is still on.

## The grip

Straight port. `gitDividerGrip` / `gitDividerIsGrip` moved to
`splitter.go` as `splitterGrip` / `splitterIsGrip` — same helper, five
callers now instead of two. The middle three rows carry `┃` in `Muted`
against the rule's `│` in `Subtle`: the difference is in **weight**
rather than only in hue, because a border and a handle have to be told
apart on a terminal whose contrast ced cannot vouch for. A live drag
lights the whole rule `Accent`, at which point the grip has nothing left
to say and steps back down to the plain glyph.

The three window seams had each been painting an identical
`for y := 0; y < a.height-1; y++` loop; those collapsed into
`drawVSplitter(x, active)`. A grip appearing on only two of three would
have read as a difference in kind between panels that resize identically.

## Files

| file | what |
|---|---|
| `internal/app/splitter.go` | **new** — the rules, `splitterGrip`, `splitterIsGrip`, `splitterHit`, `drawVSplitter`, the three per-seam hit predicates |
| `internal/app/splitter_test.go` | **new** — 7 tests, incl. the moved `TestSplitterIsGrip` |
| `internal/app/app.go` | press switch → the hit predicates; `dragSplitOffset` field + drag math; `drawSplitter` / `drawTermSplitter` collapsed |
| `internal/app/copilot_chat.go` | `drawChatSplitter` collapsed |
| `internal/app/gitpanel.go` | grip helpers moved out; offset recorded at press, applied in `dragGitListDivTo` |
| `internal/app/gitlog.go` | same |
| `internal/app/modals.go` | `closeAllModals` resets the offset |
| `CLAUDE.md` | "Sidebar splitter drag" → "The resizable seams (app/splitter.go)" |

## What a future seam inherits

Stated in `splitter.go`'s header and in CLAUDE.md, so a fourth one gets
it for free:

1. A one-column grab zone is a coin flip with a mouse.
2. The extra column comes from the side that is margin by construction —
   never from a neighbour carrying a one-cell control of its own.
3. A drag carries the offset it was seized at.
4. A plain rule reads as a border; the grip is what says "seize me", and
   the difference has to be in weight, not only in hue.
5. A pane's ceiling is its neighbour's reserve, never a constant.

## Verification

`make test` (race detector) green across all packages. `go vet` clean,
`gofmt` clean.
