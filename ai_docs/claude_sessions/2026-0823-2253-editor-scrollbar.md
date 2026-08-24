# Session: a scrollbar, and how much of the file is below you

- Date: 2026-08-23 (part two, the file tree, 2026-08-24)
- Branch: `main`
- Repo: ced (`~/projs/go/ced`)
- Session id: `f05eb6db-6e10-43d6-b3f3-56d5e6c19fcd`
- Predecessor: `2026-0819-1426-run-executable-in-terminal-panel.md`

## What was asked

> Need a draggable scrollbar giving an indication of how much is
> off-screen

Two questions were put back before any code was written, because both
answers changed the shape of the work:

- **Scope** → editor only (not the tree, not the bottom panels). One
  surface done properly; the primitive is there to reuse later.
- **Geometry** → reserve a column, don't overlay the last one.

## What shipped

`internal/app/scrollbar.go` — one reserved column down the editor body's
right edge:

```
┌──────────────┬─┐
│ editorRect   │█│ ← thumb: trackH × viewH / lineCount rows,
│              │█│   offset free × ScrollY / MaxScroll
│              │ │
│              │ │ ← track
└──────────────┴─┘
                ^ scrollbarRect: one column, at ex+ew
```

- **Thumb height** answers "how much of this file fits on screen".
- **Thumb position** answers "where in it am I".
- **Drag the thumb** to move; past either end it parks there.
- **Click the track** to page toward the click.
- The wheel already worked over that column (`scrollAt`'s catch-all) and
  still does.

`≡` → **View** → *Hide / Show editor scrollbar*, persisted as
`"scrollbar"` in config.json, default on.

## The decisions worth keeping

**1. It displaces the editor; it does not float over it.**
`editorRect` subtracts `scrollbarCols()`, which is the whole reason
nothing else had to learn about the bar — hit-testing, the hover
tooltip, drag auto-scroll, Alt+click and the editor context menu all
read that rect already. The Find-all dock's rule, and it applies harder
here: the column a thumb would have painted over is the one
`Tab.Render` uses for the horizontal-overflow arrow.

The single exception is `findAllModal.rect`, which used `ex + ew` as
"the right edge of the band" and now needs `+ a.scrollbarCols()`. Layout
is `[editor][scrollbar][find-all]`: the bar belongs to the editor, so it
stays welded to the editor's edge wherever that edge moved to.

**2. The column is NOT conditional on the file overflowing.** The
tempting version hides the bar when everything fits. That moves the
editor's right edge — re-flowing everything the user is reading — on an
edit that had nothing to do with layout. A short file gets a
**full-height thumb** instead, which is the honest way to say "this is
all of it". The two cases that do give the column back are structural,
not content-dependent: no tab open (no scroll position to report) and a
band narrower than `scrollbarMinEditor` (at that size the code is the
scarce thing).

**3. `Tab.MaxScroll` was extracted from `clampScroll` and exported, so
there is ONE ceiling.** `clampScroll` allows overscroll — the last line
can come up to the middle of the viewport — so the reachable range is
`LineCount - viewH + max(viewH/2, 3)`, not `LineCount - viewH`. A second
copy of that arithmetic in the app layer would drift, and the symptom
would be a thumb that cannot be dragged to the end of a file the wheel
scrolls to happily. `clampScroll` now calls it.

**4. Height is measured against the FILE, position against
`MaxScroll`.** The overscroll pad is blank space below the last line.
Counting it in the height shrinks the thumb to claim there is more file
than there is; ignoring it in the position leaves the thumb short of the
bottom at the end of travel. Both halves are needed, and they use
different denominators for that reason. `scrollbarMetrics` is a pure
function precisely because the interesting part is three degenerate
cases — empty buffer, file shorter than the window, thumb as tall as the
track — each one a place a division could panic or a subtraction go
negative.

**5. Press on the thumb drags; press on the track pages.** Paging is the
reversible answer: a mis-aimed page is one press back, while jumping the
thumb to the pointer has thrown away the position the user was reading
from with nothing left to restore it. Dragging is how you go somewhere
specific. The grab offset (`scrollbarGrab`) is taken at press time so
the thumb slides with the pointer instead of snapping its top edge
under it on the first motion.

**6. The hit-test runs before the editor catch-all** in `handleMouse` —
the same reason every panel's does. The column sits inside the editor's
y-band, so an unasked press would move the caret to whatever line the
user happened to grab the thumb on. There is a test for exactly that.

**7. `drawScrollbar` runs AFTER `Tab.Render`.** Render is where
`EnsureVisible` and `clampScroll` settle `ScrollY`; a bar drawn ahead of
it reports the previous frame's position on any tick that moved the
cursor.

## Two placement constraints that bit

- **The ≡ row could not go where it belonged.** Grouping-wise it wanted
  to sit with the tree / word-highlight toggles, but
  `TestMenuLayout_TerminalRowsAboveTheFold` pins *Dock terminal left* at
  relY ≤ 22 on a 24-row window and it was already at 22 — zero slack. The
  row went below the two dock toggles instead, which is defensible on its
  own terms: it is the same *kind* of setting, a column of the editor's
  band spent on chrome.
- **`newTestApp` leaves the bar OFF** (the `treeAutoFit` precedent). On,
  every test that pins editor geometry would shift by a column. The
  feature's own tests turn it on with `XDG_CONFIG_HOME` at a temp dir.

## Files

```
internal/app/scrollbar.go          new — rect, metrics, press/drag, draw, ≡ toggle
internal/app/scrollbar_test.go     new — 10 tests
internal/app/app.go                editorRect -1 col, drag continuation, press
                                   case, draw call, config load, ≡ View row
internal/app/app_test.go           menu geometry pins: 143 rows, height 149,
                                   dividers [2, 5, 146]; custom-actions 152
internal/app/findall.go            right dock starts at ex+ew+scrollbarCols()
internal/editor/tab.go             MaxScroll extracted from clampScroll
internal/editor/tab_test.go        TestTab_MaxScroll_MatchesClampScroll
internal/userconfig/userconfig.go  "scrollbar" key + SaveScrollbar
internal/userconfig/userconfig_test.go  4 tests (default, values, invalid, round-trip)
README.md                          a Scrollbar section
CLAUDE.md                          architecture map + a house-rules section
```

## Verification

`make test` (`-race`, all packages) and `go vet ./...` green.

Live, through the `run-ced` capture tool, three shapes checked against
the real binary:

| file | lines | viewport | thumb | expected |
|---|---|---|---|---|
| `internal/app/app.go` | ~4650 | 42 | 1 row, at top | `42×42/4650 = 0.4` → floored at 1 |
| `capture/main.go` | 745 | 42 | 2 rows | `42×42/746 = 2.4` |
| `internal/version/version.go` | 15 | 42 | 42 rows (fills) | everything fits |

And `Esc j 2300` in app.go put the thumb at row 21 of 42 — halfway down
a file at line 2300 of ~4650.

One near-miss worth recording: the 745-line reading looked wrong against
`main.go` until it turned out the fuzzy finder had opened
`.claude/skills/run-ced/capture/main.go`, not the repo's own. The skill
warns about this ("matches paths, not just filenames") and it still cost
a round trip. Type more of the path.


---

# Part two: the same bar on the file tree, sharing a column

> Now add a similar scrollbar to the file tree, but share the last column

"Share" is the whole brief. The editor's bar reserves its column; this
one is painted **over** the tree's own last column after `Tree.Render`,
so `sidebarRect` is untouched and the tree keeps its full drawing width.

## What sharing bought, and what it cost

**Bought: the bar can come and go.** It appears only while the list
overflows. The editor's bar structurally cannot do that — its column is
reserved, so appearing and disappearing would move the editor's right
edge and re-flow the code on an edit that had nothing to do with layout.
This one costs no layout at all, so a tree with nothing to scroll gets
its column back instead of wearing a full-height thumb over its names.
Two bars, opposite answers to the same question, one reason.

**Cost: the longest row's final rune sits under the bar.** Predicted
while designing it, and the very first live capture caught it in this
repo: auto-fit had widened the sidebar to fit
`copilot_chat_context_test.go`, and the bar then sat on its final `o` —

```
  copilot_chat_context_test.g│
```

Auto-fit exists precisely to stop that truncation, so `autoFitSidebar`
now asks for one column more while the bar is up
(`want = ContentWidth() + 1 + treeScrollbarCols()`). It cannot oscillate:
widening changes no ROW count, and the bar's verdict is about rows. With
auto-fit **off** (a width the user dragged) nothing compensates and the
bar simply overlays — that is the honest price of sharing, and dragging
one column wider is the out.

That `treeScrollbarCols` helper has exactly one caller, and deliberately
is NOT read by `sidebarRect`. If it ever gains a second caller that
subtracts it from a rect, the feature has quietly become "reserved" and
this whole section is wrong.

## Other decisions

- **It spans the LIST band only.** The EXPLORER label and the project
  name scroll with nothing, and the project name is itself a click
  target. `Tree.ListRows` is that split's one spelling — `Render` reads
  it too, so the "2" now lives in exactly one place instead of two.
- **`Tree.MaxScroll` / `Tree.RowCount`** mirror the editor's for the same
  one-formula reason (`clampScroll` calls `maxTreeScroll`). No overscroll
  pad here, deliberately: a tree has no "read the bottom comfortably"
  problem, and scrolling into blank space would just lose rows.
- **The hit-test runs before `sidebarClick`** — a press must not select
  the node under the thumb — and after the splitter cases, because the
  two columns are adjacent and the splitter is the one the user aims at
  by feel.
- **One config key for both bars.** The ≡ row became *Hide scrollbars* /
  *Show scrollbars*. This is one feature at two surfaces (the
  find-in-file / find-in-project rule), and the single argument for
  turning a bar off — give me the width back — does not even apply to the
  tree's. Both bars also share `scrollbarMetrics` and `scrollbarGrab`, so
  a thumb of a given size can never mean two things.

## Files (part two)

```
internal/app/scrollbar.go       + the tree half: rect, thumb, press, drag,
                                  draw, treeScrollbarCols; label → "scrollbars"
internal/app/scrollbar_test.go  + 6 tests
internal/app/treeautofit.go     the one-column allowance
internal/app/app.go             draw call, drag continuation, press case
internal/filetree/filetree.go   MaxScroll, RowCount, ListRows, maxTreeScroll;
                                Render now goes through ListRows
internal/filetree/filetree_test.go  + 2 tests
internal/userconfig/userconfig.go   the key's doc now covers both surfaces
README.md / CLAUDE.md           both sections rewritten for two surfaces
```

## Verification

`make test` (`-race`) and `go vet ./...` green. Live, both bars in one
frame on `internal/app` (200-odd rows, `scrollbar.go` open):

| bar | thumb | why |
|---|---|---|
| tree | rows 2–10 of the list band | ~210 rows through 42 |
| editor | 3 rows | ~470 lines through 42 |

Tree header rows 0–1 stay clean, and
`copilot_chat_context_test.go` now renders in full with the bar in the
column beside it.

## Note for next time

`internal/app/reconcile.go`, `internal/app/tabbar.go` and
`internal/app/hostident_test.go` are gofmt-unclean on `main` and were
before this session — `gofmt -l internal/app` flags them on every run.
Left alone deliberately; worth one tidy-up commit of its own.
