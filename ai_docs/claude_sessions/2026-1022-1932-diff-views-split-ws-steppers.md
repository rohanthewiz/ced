# Diff views: side by side, ignore whitespace, next/prev change, hunk revert

Session: `e62c94d8-e36c-49fa-96c5-0fae39be2ecd`

## Ask

From the cats-todo backlog: "Wherever I show a diff view, where it is
practical, also give me the option to show a side-by-side view with
options to revert hunks and the option to ignore whitespace, and buttons
for Next and Previous changes." Then `/sw`.

## What shipped

### One shared layer — internal/app/diffview.go

The unified lines stay the MODEL; side by side is a LAYOUT over them:

```
unified lines ──diffLayout(lines, split)──▶ []diffRow ──drawDiffRows──▶ screen
   (hunk spans, patches,                     full row  = one line, full width
    diffTargetLine untouched)                paired    = left(-) │ right(+)
```

- `classifyDiffLines` tracks hunk state (starts at `@@`, ends at the next
  non-body line) so `git show` metadata / stat lines never read as
  context. Combined diffs (`@@@`) never split. The untracked synthesis
  pairs on the right.
- Pairing is positional per -/+ run; `+a -b` is two runs.
- `diffPane` caches by slice identity + mode, holds change `starts` and
  the cursor `cur`; `switchMode` keeps the top line and cursor across a
  layout flip.
- Steppers walk changes (runs), relative to the current change while it
  is on screen, else the viewport; stop at the ends ("No next change").
  Current change marked `▸` left of the text.
- Toolbar `▲ ▼ split ws` (`diffToolbar`, right-aligned, sheds ws → split
  → the pair), tips via buttontip.go.
- Prefs: `diffSplit` persisted as `"diffview"` (userconfig `DiffView`,
  `SaveDiffView`); `diffIgnoreWS` session-only.
- ≡ View: "Diff view: side by side / unified", "Diff view: ignore
  whitespace / show whitespace changes", "Diff view: next change",
  "Diff view: previous change" (act on `lastDiffSurf`).

### Surfaces

| Surface | split | ws | next/prev | revert |
|---|---|---|---|---|
| Compare panel | ✓ | re-diffs held `sideOld/sideNew` (`diff.DiffWith`) | ✓ | new `[↺]` (comparerevert.go) |
| Git panel diff | ✓ | `git diff -w`; event carries `ws`, mismatch stale; `diffWS` hides hunk verbs | ✓ + walk `]`/`[` | existing chips, keyed by DISPLAY row |
| Git log detail | ✓ | `git show -w` | ✓ | — |
| Cherry-pick preview | alt+v | — (shows what a pick applies) | alt+n / alt+p | — |
| Change view | alt+s (pair rows) | — (-U0 gutter hunk) | ◂ Prev / Next ▸, alt+p / alt+n, nav row (chrome 6→7) | existing |
| Hover popup | not practical — click it for the change view | | | |

### Compare revert — internal/app/comparerevert.go

`compareRevertEdit` verifies the hunk's "+" side (context included)
against the buffer, then replaces it with the "-" side as one
`ApplyMultiEdit` (unsaved, no confirm — hunkview's contract). Handles a
buffer without a final newline. Only when the new side is a whole open
buffer (`compareRevertBlocked` explains selection / conflict refusals).
Switches to the tab, re-runs the comparison keeping scroll. ≡ Compare
"Revert a change in the compare view…" picker. `compareRediff` leaves an
armed paste alone.

### internal/diff

`Options{IgnoreWhitespace}` + `DiffWith`: match on whitespace-stripped
keys, emit original text (equal lines carry the NEW side's spelling, so
the compare revert's verification still matches the buffer).

### Other edits

- Scroll clamps, double-click jumps and overflow-marker totals count
  display rows (`compareRows`, `gitPanelDiffRows`, `gitLogDetailRows`).
- `loadGitPanelDiff` / `loadGitLogDetail` take `ws`.
- Menu pins: 205 rows, height 211, dividers `[2, 5, 208]` (5 new rows).
- `TestDrawGitPanel_HandlesBrightenWhileDragging` probes the rule cell
  left of ✕ (the old cell is under the toolbar).
- README "### Diff views" + Features bullet; CLAUDE.md architecture map,
  pins and a "Diff views" design-rules section.

## Verification

- New tests: diffview_test.go, comparerevert_test.go, plus additions in
  diff, userconfig, compare, gitpanel, gitpanelwalk, gitlog, hunkview,
  cherrypickdiff tests.
- `make test` (race) green across all packages; `go test ./internal/app`
  green after the last edit.
- run-ced captures: git panel side by side with line numbers and chips,
  ws hiding a re-indent with chips gone and "Change 1 of 2" + `▸`,
  compare panel side by side with `[↺]`.

## Next

Closed: None. Declined: None. Raised: N-084, N-085. Deferred: None.
Promoted: None. Moved: None. Updated: None.
Full list: `ai_docs/todo/next-list.md`.
