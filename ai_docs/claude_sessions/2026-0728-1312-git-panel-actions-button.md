# Session: Git panel — checkbox becomes multi-select + Actions ▾ button

Session ID: d8d8524a-11f3-4f5e-b0e5-7718279ed686
Date: 2026-07-28

### Ask

> "I want more options on the Git Panel. Maybe we can still use the
> checkbox, but then select what action to perform with like an actions
> button above the list"

The panel shipped with exactly one verb: the checkbox WAS the stage
state (`[x]` / `[~]` / `[ ]`) and clicking it staged or unstaged. One
gesture can't carry a dozen verbs, so the tick had to become a
selection and the verbs had to move somewhere with room for them.

### Design decisions (asked up front, both material)

Two readings of the ask would have produced very different work, so
both went to the owner before any code:

1. **Checkbox semantics** → *multi-select; the porcelain code shows
   stage state.* (Alternatives offered: keep the checkbox as a stage
   toggle and have Actions act on the highlighted row only; or a dual
   gutter with a separate tick + staged glyph, rejected as ~3 more
   columns out of an already-narrow 24–40 col list.)
2. **Which actions** → all four groups: Stage/Unstage,
   Discard/Delete, Commit staged…, Open/Copy path.

### What shipped

**Header** (`gitpanel.go`) — the rule now carries an `Actions ▾` chip on
the left, the title, a right-aligned tick count, and the ✕:

```
─ Actions ▾  Git changes · 4 files ──────────────── 2 selected  ✕ ─
 [x]  M internal/app/gitpane… │ @@ -1,3 +1,4 @@
 [ ] MM internal/app/gitcmd.… │  ctx
 [x] ?? notes.md              │ -old
 [ ] M  main.go               │ +new
```

Only the two buttons are mandatory; on a narrow panel the title and the
count drop out rather than overlapping — a control you can't click is
worse than a label you can't read.

**Stage state didn't get lost, it moved.** The XY code column is now
drawn VERBATIM instead of `strings.TrimSpace`d — exactly as
`git status -s` prints it (`" M"` unstaged, `"M "` staged, `"MM"`
both) — with the index char overdrawn bold when non-space. The old
trim collapsed staged and unstaged into the same single glyph, which
was survivable while the tri-state checkbox carried staging and is not
survivable now that the column IS the staged indicator.

**New file `internal/app/gitpanelactions.go`** — the panel's verb
surface. Rows offered: Stage, Unstage, Open, Copy path, Discard
changes…, Delete from disk…, Commit staged…, Select all, Clear
selection.

### Non-obvious choices worth keeping

- **Picker, not a bespoke dropdown.** `Actions ▾` calls `openPicker`,
  per the modal house rule that every choose-one-from-a-list UI reuses
  the palette. Free fuzzy filtering, free keyboard drive, zero new
  hit-testing.
- **Rows are omitted, not dimmed**, when they'd no-op — Stage needs an
  unstaged change in the set, Unstage a staged one, Discard a tracked
  file, Commit a non-empty index. Same reasoning as
  `paletteActionItems`: a palette offering "Undo" with nothing to undo
  teaches the user that Enter sometimes does nothing. A `"MM"` file
  correctly offers BOTH staging verbs.
- **Targets fall back to the highlighted row** when nothing is ticked
  (`gitPanelTargets`), so the first click on Actions is already useful.
  Iterating `files` rather than the map keeps results in list order, so
  confirm bodies read top-down.
- **Ticks are keyed by absolute path and pruned on every refresh**
  (`gitPanelPruneChecked`, called from `refreshGitPanelFiles`). A tick
  whose file left the change list — committed, discarded — would
  otherwise sit invisibly in the set and get swept into a later bulk
  action the user never saw it in. Unticking `delete`s the key rather
  than storing `false`, so `len()` IS the count.
- **Discard is `git checkout HEAD -- <paths>`**, which clears the
  staged copy as well as the work-tree edit — "make this look like the
  last commit", which is what a reviewer means. Untracked files are
  filtered out (no HEAD version; `git checkout` on one only errors with
  "pathspec did not match"); Delete is their verb.
- **Delete is plain `os.Remove`, not `git rm`.** The set can mix
  tracked and untracked and `git rm` refuses the latter, whereas a disk
  delete is uniform — tracked entries simply reappear in the panel as
  deletions, which is exactly the reviewable state. A failure mid-set
  does NOT abort the rest (`gitPanelDoDelete`): the user asked for the
  whole set, and stopping halfway leaves a state harder to reason about
  than "these N went, that one didn't". The first error goes in the
  flash.
- **Copy path is relative, newline-joined** — the shape you paste
  straight into a shell, and the form that survives an SSH hop.
- All writes go through `runGitCmd` — one fork for the whole set,
  failures into the info modal with git's own words, redraw off the
  done-event's refresh rather than optimistically.

### Geometry gotcha

The header rule is still the height-drag handle, so `gitPanelPress` has
to carve out `gitPanelActionsRect` (and the pre-existing
`gitPanelCloseRect`) BEFORE returning `"gitpanel"`. Both rects are the
single source draw and hit-test share, per the btnRect house rule.

This bit the test first: `TestHandleMouse_GitPanelHeaderDrag` pressed at
`px+5`, which now lands inside the Actions chip, and later re-used a
`btnRect` captured before a drag had moved the header row. Both fixed —
the rect is re-read after the drag.

### ≡ menu

Added `{label: "Git panel actions", enabled: hasGitPanelOpen}` to the
Git group — the keyboard/palette twin of the header button. The panel
is mouse-driven by design, but macOS Terminal + tmux can swallow
clicks, so its verbs must be menu-reachable (the project's standing
rule).

Menu geometry pins moved: 52 → 53 group actions, height 70 → 71,
dividers `[2, 5, 67]` → `[2, 5, 68]`, Git section 10 → 11 rows. Three
other tests hard-coded `a.height = 70` / `h != 73` against the old
baseline and were bumped to 80 / 74.

### Tests

- New `gitpanelactions_test.go` — 12 tests: targeting (ticks beat
  highlight, list order, empty panel), label naming, the two filters
  (`gitPanelTracked` / `gitPanelOnDisk`), row gating across
  unstaged/staged/partial/untracked, the selection rows, the empty-panel
  flash, the menu predicate, plus e2e bulk stage→unstage and
  discard-to-HEAD against real repos, delete-closes-tabs, partial-delete
  failure reporting, open-as-tabs, and the copy flash.
- `gitpanel_test.go` — `TestGitPanelCheckboxClick_StagesAndUnstages`
  replaced by `..._TogglesSelection`; new `TestGitPanelPruneChecked`,
  `TestGitPanelSelectAllAndClear`,
  `TestDrawGitPanelHeader_ShowsTickCount` (which pins that `" M"` keeps
  its leading space in the drawn output).

E2E pump note: pump on the PANEL's view of the world, not git's. The
command finishes before the done-event's refresh lands, so a condition
reading `git diff --cached` directly goes true while
`gitPanel.files[].Code` is still stale — and the next set of action
rows is built from those codes.

`make test` (race) green across all packages; `gofmt` and `go vet`
clean.

### CLAUDE.md

New section "Git panel checkboxes + Actions" under the design patterns,
leading with **don't put staging back on the checkbox** and **don't trim
the porcelain code** — the two changes most likely to be innocently
reverted by someone reading the row-draw code in isolation.

### Not done / follow-ups

- Not run in a live terminal. The render above is from a tcell
  simulation screen; worth a `make run` against a dirty repo to confirm
  the `Actions ▾` chip reads as clickable at real terminal colors.
- No Esc-leader for the actions picker — the button, the ≡ row, and the
  palette were judged sufficient; the leader table stays uncrowded.
- Stage/Unstage deliberately do NOT clear the tick set, so
  "Stage" → "Commit staged…" flows without re-ticking. Pruning handles
  the files that leave the list.
