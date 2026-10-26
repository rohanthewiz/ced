# Find all in file: a live prompt (search as you type, from the start)

Session: `c310fb94-0d24-41f9-9d6a-032aa57b9f18`

## Ask

From the cats-todo backlog, with
`2026-1025-2232-find-all-esc-revert-and-seam` as context: "in the findall
dialog when find all in file mode, allow immediate find on type on search
popup". Clarified mid-turn: "Essentially allow interactive find from the
start".

Read as: the "Find all in file" PROMPT (the popup asking for the query
when there's no single-line selection) searches live, beginning with
its pre-fill, before Enter opens the list.

## What shipped

### promptModal hooks (modals.go)

Four optional fields, all nil/false on every other prompt:

- `onEdit(app, value)`: runs after a real edit (`textField.handleKey`'s
  `edited`, so caret motion doesn't count), after a history pick (key or
  mouse), and after an extra fires (key or click), because the find
  chips change what the same text means. `edited` skips it once the
  prompt is no longer `a.modal`, since an extra may close it.
- `onCancel(app)`: Esc, the Cancel button and an outside click now share
  `cancel()`, which closes the prompt and runs the hook. Submit doesn't
  run it; the callback owns that exit.
- `status(app) string`: drawn muted on the blank row 5 under the field.
- `bottom`: rect keeps the centered column but rests on the editor's last
  row (`max(centered y, ey+eh-h)`).

`openFindPrompt` now returns the `*promptModal`. The project/folder
callers ignore it.

### findalllive.go (new)

- `findAllLive` captures the active tab's view and find state AFTER
  `openModal`, so the find bar's close is already applied.
- `update(value)`: trims the value the way submit does and matches under
  `a.findOptions()`. Empty / bad regex / no hits → `settle`: view back to
  the origin, `ClearFind`, note set. Hits → `SetFindOptions` +
  `SetFindQuery`, current = `FirstMatchAtOrAfter(ORIGIN)` (showFindAll's
  rule, never the last preview, so narrowing then widening comes back),
  `preview`, note `"k of N · enter lists them"`.
- `preview`: `MoveCursorTo` + `MarkJump`. The hit is centered when
  off-screen OR under the prompt box (`hiddenByPrompt` via
  `PosScreenCell`; a horizontal scroll-off doesn't count).
- `restore`: `RestoreView` if moved, then the prior find fields written
  verbatim (the `restoreFind` rule).
- `openFindAll` wires it in: `bottom`, `onEdit`, `onCancel`, `status`,
  then `update(seed)`, which makes it live from the start. The submit
  callback runs `live.restore` BEFORE `showFindAll`, so the list's origin
  (its Esc / ↺ revert loc) and `priorQuery` are the true ones.

### Why the bottom

The first cut anchored the box at the TOP, next to where the list opens.
The test showed that a hit near the top of a file can't be centered out
from under it, because ScrollY floors at 0. At the bottom, the overscroll
allowance lifts late hits clear. A centered box covers exactly where
CenterOnCursor lands a hit.

### Why not re-search inside the list

The list is a worklist over ONE answer: filter, dismissals, replace and
the stale check all assume a fixed query. The prompt is where the
question is still being asked. Project/folder prompts stay inert because
a per-keystroke walk of the index is too costly.

### Docs

README Find-all paragraph ("the question is live from the start…").
CLAUDE.md: new Find-all bullet on the live prompt, and findalllive.go in
the architecture map.

## Tests

- modals_test.go: `TestPromptLive_OnEditHearsEditsNotCaretMotion`,
  `TestPromptLive_OnCancelRunsOnEveryNonSubmitExit`,
  `TestPromptLive_BottomAnchorsOnTheEditor`.
- findalllive_test.go: seed searches at open; typing re-searches from
  the origin (count → count+ → count); miss / empty / bad regex put the
  view back; Alt+c re-asks; Esc restores view and the prior find state;
  Enter opens the list with the true origin and priorQuery on the
  previewed row; a hit under the box is scrolled clear while a visible
  one leaves scroll alone; the project prompt stays inert.

## Verification

- `make test` (race) green.
- run-ced PTY: `goto.go`, Esc F, typed `strconv.A`. The box sat on the
  editor's bottom rows, line 83's hit was centered above it, and the
  status row read "1 of 1 · enter lists them".

## Next

Closed: None. Declined: None. Raised: N-093, N-094.
Deferred: None. Promoted: None. Moved: None. Updated: None.
Full list: `ai_docs/todo/next-list.md`.
