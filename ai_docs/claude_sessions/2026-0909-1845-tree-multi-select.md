# Session: select and act on multiple files in the file tree

- Date: 2026-09-09
- Branch: `main`
- Repo: ced (`~/projs/go/ced`)
- Session id: `01RSt1WvNtftfgibb8zuUBnN`
- Predecessor: `2026-0909-1341-markdown-viewer.md`

## What was asked

> In the filetree I need a way to select and act on multiple files

Two halves, and the second one is the load-bearing half. A tick nobody
can spend is decoration; the deliverable is the verb list.

## The precedent, and why it settled almost every question

The editor already had this feature — in the git panel. Its checkbox
used to be a stage toggle, which capped the panel at exactly one verb;
moving stage/unstage into an `Actions ▾` picker is what turned the tick
into a general multi-selection.

So this is that idea in a panel with no room for a four-cell gutter.
Inheriting the pattern brought its answers with it:

| question | the git panel's answer, reused |
|---|---|
| where do the verbs live | `openPicker`, never a bespoke dropdown |
| what if nothing is ticked | fall back to the row under the cursor |
| rows that would no-op | omitted, not dimmed |
| stale ticks | pruned on every refresh |
| labels | one helper, so "Delete main.go" / "Delete 4 items" can't drift |

The split of labour mirrors it too: `internal/filetree` holds the set
and paints the tick, `internal/app/treemarks.go` owns what the marks are
FOR. gitpanel.go / gitpanelactions.go, one floor down.

## The one cell

The tick had to cost **no layout**, and that is not a style point.
`nodeRowSegments` is the single construction of a row's text and it is
also `ContentWidth`'s measurer — which the sidebar's auto-fit sizes
itself from. A wider prefix would therefore tie the sidebar's width to
the multi-selection: ticking a file would shift the editor's columns and
re-flow whatever the user was reading.

Every row already opens with a blank cell (`prefix` starts with a
space), so `paintMark` stamps the `✓` there *after* the row's own text.
The overflow markers' shared-column argument, and
`TestMarks_TickCostsNoWidth` is what pins it.

Glyph is `✓` — the git panel's *review* mark rather than its `[x]`
checkbox. One cell is all there is, and the tree has no competing "I
have read this" notion for it to collide with.

## Keyed by path, not by `*Node`

The tree's refresh is identity-preserving — but only for **survivors**.
A folder rewritten on disk hands its rows fresh `*Node` pointers, and a
set keyed on the old ones would empty itself silently. `Marked` is
`map[string]bool`, nil in the common case, so every reader tolerates a
nil map.

Three consequences fell out of that choice:

- **`Refresh` prunes**, not the app's call sites. It is the single funnel
  every tree reload comes through, and a forgotten prune is invisible: a
  mark for a file that left the tree would quietly widen the next bulk
  action.
- **`MarkedNodes` walks the loaded tree, in tree order.** Map order is
  randomised, so two runs of one delete would produce two different
  confirmation bodies. Walking the *loaded* tree rather than the visible
  rows is also what makes a mark survive folding a branch.
- **Bulk marking stays scoped to what is visible** (`MarkVisible`,
  `MarkChildren`, both shallow; the "select contents of…" row refuses an
  unexpanded folder). A set nobody can see is a set nobody can check
  before deleting it.

That last rule is also why the multi-delete confirmation **lists the
names** instead of only counting them — the one place it differs from
the single-file dialog it grew out of. A mark outlives scrolling and
folding, so a count alone would ask the user to approve a deletion they
cannot see the contents of.

## Four surfaces

Not gold-plating — each closes a hole one of the others leaves:

- **Gutter click** (the row's first column) — primary, mouse-first.
- **`Space` / `*` / `A`** in the focused tree — the keyboard twins.
  Space costs typeahead nothing (no filename starts with one) and `A` is
  shifted, so it costs nothing either (typeahead lowercases).
- **Right-click → Select** — the *discovery* surface. A one-cell tick is
  close to invisible as an affordance; without a named row a mouse user
  would have no way to learn the gutter is clickable at all.
- **≡ File ▸ Selected items…** — the path that survives a terminal which
  swallows right-click, and where the count is legible while the sidebar
  is hidden.

**Shift-click is a bonus layer**, in metakeys.go's sense: several
terminals keep it for their own text selection, so an unreported shift
degrades to a plain toggle and every set reachable with it is reachable
without it. `MarkRange` only ever *adds* — a second extension that
unmarked what it swept over would destroy the set the first one built.

## The verbs widened existing code rather than reimplementing it

None of them owns an implementation:

- **`doDeletePaths`** is `deletePath` plus one collected report. A loop
  over `doDeletePath` would spend a workspace re-sync per file and leave
  the user reading whichever flash landed last — and *which* file failed
  is the whole content of the answer.
- **`createZipMulti`** runs `addZipSource` (the spine factored out of
  `createZip`) over several sources. Entries are rooted at the set's
  **common parent**, not each source's basename: a set can hold
  `app/main.go` beside `cmd/main.go`, and rooted at basenames both would
  be stored as `main.go` and extraction would clobber one with the other.
  `commonParentDir` works on path *segments*, because a common string
  prefix is not a common directory (`/a/foo` vs `/a/foobar`).
- **Copy arms the same file clipboard** `≡ Paste` and Cmd+V already read
  — which is why there is no paste verb in the picker at all. The
  clipboard grew from one path to a slice; `copyToFileClip` is now a
  wrapper, so every existing surface reads unchanged.

### The bug the tests were written for

A set paste is **planned before it copies**, and the plan has to
*reserve* names. "Is this name free?" is answered against the
filesystem, and nothing is written until the plan is complete — so two
sources called `same.txt` each found the destination unoccupied, both
claimed it, and the second copy failed on `O_EXCL` after the first had
already landed. `TestStartPaste_SetReservesNamesInOrder` caught it on
its first run; `uniquePastePathExcept` is the fix.

## Two deliberate refusals

- **Discard is not a row.** Staging from the tree is two lines through
  `runGitCmd` and genuinely useful; reverting a file's *contents* is a
  loss the git panel shows you the diff of first, and offering it from a
  surface that cannot render what would be lost is the one git verb this
  picker should not carry.
- **Partial sets are reported, never silently narrowed.** A set ticked
  minutes ago can legitimately have lost a file to a git checkout, so
  Copy drops the missing and says how many, Delete names what failed,
  and Open counts what actually landed *from the tab list* rather than
  from the loop (openFile refuses a binary or oversized file with its
  own flash). The one all-or-nothing refusal is the archive: a zip
  quietly missing a file it was asked to hold is the single wrong answer
  a backup can give.

A delete also clears the set **explicitly** rather than leaving it to
pruning — a path that *failed* to delete is still in the tree, so it
would stay ticked and ride along into the next action.

## Files

New:

```
internal/app/treemarks.go              + _test.go   (20 tests)
```

Touched: `internal/filetree/filetree.go` (the set, `paintMark`, the
header count, pruning in `Refresh`) + `_test.go` (12 tests),
`internal/app/copypaste.go` (set-shaped clipboard, planned paste,
`uniquePastePathExcept`), `internal/app/zipops.go` (`addZipSource`,
`createZipMulti`, `startZipSet`, `commonParentDir`),
`internal/app/fileops.go` (`doDeletePaths`), `internal/app/app.go`
(`sidebarPress`, the ≡ File row), `internal/app/modals.go` (the two
context rows), `internal/app/treenav.go` (Space / `*` / `A`),
`CLAUDE.md`, `README.md`.

Menu-layout pins moved as the house rules require: 147→148 rows,
153→154 height, dividers `[2, 5, 151]`.

## Verification

`go test ./...` green, and green again under `-race`; `go vet` and
`gofmt` clean.

Beyond the suite, every claim above was driven through the real binary
with `run-ced` over a PTY:

- ticks land in the rows' first cells, `2 ✓` on the EXPLORER header
- `A` opens "Selected items (2)" with the eight expected rows
- the multi-delete dialog lists both names and keeps the recursive
  warning when the set holds a folder (`No` still the default focus)
- zipping two files produced `marksdemo-selection.zip` holding both, and
  it appeared in the tree immediately
- copy-for-paste → `≡ Paste 2 items` produced `alpha copy.txt` and
  `beta copy.txt`

One polish came out of that pass: the confirm drawer *centers* every
line it is handed, so the two-space indent on the listed names was
shifting them off-centre. Dropped.

## Known gaps / possible follow-ups

- **No cross-folder "select all matching"** — e.g. tick every `*_test.go`
  in the tree. The finder answers the question differently today.
- **Marks are session-only** and die with a folder switch (the App is
  rebuilt). That seems right; a persisted selection would be a set
  nobody remembers making.
- **Rename is not a set verb**, deliberately: a bulk rename needs a
  pattern language, which is a different feature.
