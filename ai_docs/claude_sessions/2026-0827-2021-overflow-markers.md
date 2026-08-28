# Session: the rail becomes one marker, and it says how much

- Date: 2026-08-27
- Branch: `main`
- Repo: ced (`~/projs/go/ced`)
- Session id: `fa087811-72f8-46f5-b122-20abaf4b654f`
- Predecessor: `2026-0826-1044-scrollbar-rail-marks.md`

## What was asked

> Now add the rail marks to the git panel's diff view, but also move the
> main editor to the rail marks also.
> One thing I'd like is a popup with "+n lines" for the top and bottom
> rail marks.

That first sentence was read two wrong ways before the owner corrected
it, and the correction is the whole session:

> I mean to move everything to what we have for the filetree, but add a
> title popup of how many lines remain in that direction. The rails are
> not pleasing to the eye.

So: **delete the scrollbar** and give every scrolling surface the
sidebar's marker instead, with a hover popup carrying the number a
thumb's height used to carry.

Two decisions were put back before any code was written, because both
were deletions of shipped behavior:

- **The rail's minimap** (off-screen diagnostics, find hits, the caret
  tick) → folded into the marker's COLOR, counts named in the popup.
- **The `≡` View row and the `"scrollbar"` config key** → both dropped.
  The key bought exactly one thing, "give me the column back", and there
  is no column to give back.

Then, in two follow-ups:

> commit then add the markers to the git panel's file list too
> now add them to the git log panel's two panes

## What shipped

Three commits.

### `ff487ba` — the rail becomes one marker

`internal/app/overflow.go` (+ `_test.go`) replaces `scrollbar.go` and
`scrollbarmarks.go`, both deleted along with their tests. A `▴` / `▾` in
the LAST column of a viewport's first and last row:

```
  func handleKey(...) {                    ▴   ← 412 lines above
      switch {
      ...
  }                                        ▾   ← 1,842 lines below
```

Hover one for 250ms:

```
  }                                                    ▾
                                    ┌───────────────────┐
                                    │ 1842 lines below  │
                                    │ 5 hits · 2 errors │
                                    └───────────────────┘
```

Gone with the bar: `scrollbarRect` / `Cols` / `Contains` / `Metrics` /
`Thumb` / `Press`, `dragScrollbarTo`, `drawScrollbar`, the `"scrollbar"`
drag mode and its two mouse-router cases, `scrollbarGrab`,
`scrollbarShown`, `menuToggleScrollbar`, `scrollbarToggleLabel`,
`userconfig.Scrollbar` + `SaveScrollbar`, `editorRect`'s subtraction,
`findAllModal.rect`'s `+ scrollbarCols()`, and — the one that made the
feature one mechanism instead of two — `filetree.drawMoreMarker`.

### `e5b6d94` — the git panel's file list

### `e0a4c03` — the git log's two panes, and a `pane` helper

Six surfaces in total: editor, tree, changes-panel list + diff, git-log
list + detail.

## The decisions worth keeping

**1. The marker SHARES the last column; it reserves nothing.** That is
what lets it come and go with the content, which the bar structurally
could not do: a bar that appeared when a file grew past the fold would
move the editor's right edge, re-flowing everything the user was
reading, on an edit that had nothing to do with layout. That is exactly
why the old bar had to keep its column even in a file that fit.

Where the cell comes from differs by surface, and it is worth knowing
which:

| surface | column | costs |
|---|---|---|
| editor | last content column | one cell of code, two rows |
| changes/log LIST | last cell of the row | one cell of a label already ellipsised |
| changes/log RIGHT pane | the pane's blank margin | nothing |
| tree | last cell of the row | one cell of a name |

**2. It is drawn unconditionally.** The ≡ menu's clipped-content arrows'
rule: content the user cannot see and has not been told about is the one
thing a viewport must never do. Nothing to plumb, nothing to persist. An
old `"scrollbar"` key in config.json is IGNORED rather than rejected —
`TestLoadRetiredScrollbarKey` pins that, because a startup error over a
preference that no longer means anything is the one way this could bite
an existing user.

**3. The color is what is out there.** `railKind` became
`offscreenKind`, same ranking: **caret > find > error > warn > info**. A
marker stands for the whole rest of the document in its direction, so
collisions are the normal case; the caret wins outright because it is
the only UNIQUE mark, while a diagnostic is redundantly carried by the
status bar, the Problems panel and its own gutter dot. Only what is OFF
SCREEN counts — on screen the gutter dot and the find tint are already
saying it, in place. Verified live: the `▾` came up amber (`#e0b64e`,
`FindCurrent`) with find hits below, while the `▴` stayed muted.

**4. `overflowMarkers()` is the ONE enumerator** draw, hit-testing and
the popup all read (the btnRect rule). That is why the tree stopped
painting its own: two implementations of "is there more?" would drift,
and only a shared enumerator could give the tree the popup and an
up-marker for free. The app derives the sidebar's pair from `RowCount` /
`ScrollY` / `ListRows`, all already exported — the tree's old
self-containment was the thing traded away, deliberately.

**5. The marker keeps its cell's BACKGROUND** (`GetContent` +
`Decompose`). It is an annotation on a row somebody else drew, so only
the foreground is its own; setting a background would punch a hole in
the current-line highlight, the tree's selection bar or a panel's fill.
The draw test asserts the marker's bg equals its left neighbour's.

**6. The popup is passive, and NOT folded into `hoverDwellState`.**
That layer is armed only inside cats (`hoverDwellArmed` → Tier 1)
because its answer costs a round trip to a language server over a link
ced cannot vouch for. This answer is a count the draw already had in
hand, so `overflowTipState` is separate and arms on every host — motion
reporting is enabled unconditionally (`app.go`'s `EnableMouse` with
`MouseMotionEvents`). `armOverflowTip` also arms NOTHING unless the cell
really carries a marker, unlike the dwell layer, which has to ask before
it knows. The 250ms delay exists only to stop a pointer sweeping the
window's edge flashing a box on its way past. It reuses `tooltipSize` /
`tooltipPlace` / `drawTooltipBox`, so there is one tooltip look rather
than three.

**7. Line counts floor at zero.** `clampScroll`'s overscroll pad lets
the last line come up to mid-viewport, so `total - (last+1)` goes
negative there. A marker for lines that do not exist is worse than none.

**8. The `pane` helper, once there were four of them.** Both git panels
are a list and a document, same columns, independent scrolls. Spelling
`total - (scroll + visible)` out four times is how one of them ends up
off by a row.

**9. The log's pair rides `gitLogBodyTop` / `gitLogBodyRows`, not the
panel rect.** The search bar takes a row off the body's TOP, so opening
it moves the up-marker DOWN while the down-marker stays where it is. The
first version of that test asserted both moved and failed — worth
keeping as the reason the two helpers exist.

**10. Units follow the surface**: lines (editor, diffs), rows (tree —
it holds folders too), files (changes list), commits (log). The log
counts the LOADED list — capped at 400, narrowed by any search — which
is what the panel is a viewport onto; the title's `+` is what says the
repository holds more.

## Two things deliberately NOT built

- **A click target.** A marker is a report, not a verb. Nothing about
  "there are 1,842 lines below" says which of them you wanted.
- **Git changes as a marker color.** The rail refused them last session
  for a reason that survives the redesign: they would be on nearly every
  file you actually work in, and a change bar is state you already own,
  not somewhere you were about to go.

## Files

```
internal/app/overflow.go            NEW — kinds, counting, the enumerator,
                                    the draw, the popup
internal/app/overflow_test.go       NEW — 11 tests
internal/app/scrollbar.go           DELETED
internal/app/scrollbarmarks.go      DELETED
internal/app/scrollbar_test.go      DELETED
internal/app/scrollbarmarks_test.go DELETED
internal/app/app.go                 editorRect +1 col back, two mouse cases and
                                    one drag branch gone, the ≡ View row gone,
                                    two draw calls + one event case + the
                                    pointer hook added
internal/app/app_test.go            menu pins: 142 rows, height 148,
                                    dividers [2, 5, 145]; custom-actions 151
internal/app/findall.go             right dock back to a plain ex+ew
internal/app/hoverdwell.go          a stale "on its way to the scrollbar"
internal/app/treeautofit.go         the marker is now two rows, not one
internal/editor/tab.go              MaxScroll's doc; the '›' arrow now notes
                                    that the vertical marker outranks it
internal/filetree/filetree.go       drawMoreMarker + treeMoreRune deleted;
                                    three doc comments re-aimed
internal/filetree/filetree_test.go  TestRender_MoreMarker → _OverflowInputs
internal/userconfig/userconfig.go   the "scrollbar" key removed entirely
internal/userconfig/userconfig_test.go  four tests → TestLoadRetiredScrollbarKey
README.md                           "Scrollbars" → "Overflow markers" (the old
                                    section still described the tree bar that
                                    was dropped LAST session)
CLAUDE.md                           three sections → one; architecture map
```

Net: **+236 / −1549** on the first commit alone.

## Verification

- `go test ./... -race` and `go vet ./...` green after every commit;
  `gofmt -l` empty across the repo.
- Driven live through `.claude/skills/run-ced`'s PTY capture, all six
  surfaces: both editor markers in `app.go`, the amber `▾` under an open
  find, the tree's on `internal/app`, the changes panel's diff margin,
  its file list against a throwaway 30-file repo built in the
  scratchpad, and the git log's list + detail.

## Notes for next time

- **The popup cannot be exercised by the capture tool** — its script
  syntax sends keystrokes only, and this is a hover. `TestOverflowTip_*`
  is the coverage; it asserts the stamped box does not cover the marker
  it describes.
- **The capture tool refuses windows below ~22 rows** ("Window too small
  — please resize"), so forcing a panel to overflow by shrinking the
  terminal does not work. Build a fixture repo with enough content
  instead — a `git init` in the scratchpad with 30 modified files took
  one line and photographed the list marker immediately.
- `Tab.MaxScroll` and `Tree.MaxScroll` are now called only by their own
  `clampScroll` and by tests. They were kept (and their doc comments
  re-aimed) rather than inlined: they are the one spelling of the
  ceiling the wheel can reach, and the overscroll pad is the part an
  outside caller always forgets.
- If a seventh surface ever wants markers, it needs three numbers —
  scroll offset, visible rows, total — and one line of `pane(...)`. The
  question to ask it first is the one the tree answered: is this a body
  of text asking "how far in am I?", or a list asking "have I seen
  everything?" Both get the same marker now; only the unit differs.
