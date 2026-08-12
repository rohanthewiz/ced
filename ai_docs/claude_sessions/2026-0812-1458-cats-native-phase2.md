# Session: cats-native plan, Phase 2 — never clobber, never be clobbered

- Session id: `42c8c156-d73b-4502-bda4-94d26aeb29d1`
- Date: 2026-08-12
- Branch: `main`, commits `a46e322..ad3ae5f` (2 commits, pushed)
- Plan: `ai_docs/cats-native-plan.md` §Phase 2 — now marked ✅ done
- Predecessor: `2026-0812-1429-cats-native-phase1.md`

## What was asked

Load the last session, then continue with Phase 2 of the ced × cats
plan: extract `reconcileOpenTabsWithDisk` into `reconcile.go` and build
the two interception points — the watcher's conflict matrix and a save
guard that aborts instead of overwriting — plus autosave suspension.

## What landed

One feature commit (Phase 2 is a single unit in the plan, unlike Phase
1's six shippable items) plus the plan update. `make test` (race) green.

### New: `internal/app/reconcile.go` + `reconcile_test.go`

**The state that ties both halves together** — `diskConflict{tab, path,
mtime, resume, prompted}` in `App.conflicts map[*editor.Tab]*diskConflict`
(keyed by pointer: the tab IS the thing at risk, and a rename would
orphan a path key). While a record stands: `⚠` in the tab strip,
auto-save suspended for that tab, every save path refusing.
`closeTab` drops it beside `wsForgetTab`.

**A. Watcher tick** — the matrix:

| disk | buffer | behaviour |
|---|---|---|
| newer | clean | `ReloadUndoable` + "↺ — undo to get yours back" |
| newer | dirty | record a conflict (no flash-and-forget), defer the prompt |
| deleted | any | keep the buffer, `⊘` marker, save recreates |

The load-bearing change is a *deletion*: the old code adopted the disk
mtime on the dirty branch purely to stop re-flashing every tick, which
silently told the (future) save guard "this tab is based on the current
file". The conflict record suppresses repeats instead; a test pins that
`tab.Mtime` is untouched.

**B. Save guard** — `saveGuard(tab, resume) bool` stats first and
**aborts** the write on a newer disk mtime (before: ced warned and wrote
anyway). Wired into `saveTabAt`, `menuSaveAndClose`, autosave
(`autoSaveGuard`, the quiet flavour), plus two paths beyond the spec:

- `applyWorkspaceEdit` — a detached participant that moved between plan
  and apply aborts the whole group through the existing rollback. This
  is the one write path that cannot stop to ask; half a rename is worse
  than none.
- `handleFormatDone`'s kept-edits branch now adopts the formatter's
  mtime. Without it, ced's own write reads back as a foreign one and
  blocks the user's next save behind a question about something the
  editor did. (New test: `TestFormatOnSave_KeptEditsAreNotAConflict`.)

Stat-error asymmetry is deliberate: `saveGuard` passes (a missing file
means the save is a *recreate*; other errors are better reported by the
write itself), `autoSaveGuard` refuses (a background write must not
guess).

**The prompt** — `conflictModal`, four clickable choices driven off one
`conflictChoices` table (label + hover hint + resolution), so draw,
hit-test, and behaviour can't drift:

- **Compare** (default focus — the only choice that changes nothing)
  → `compareWithFile(tab.Path)`, which already means buffer-vs-disk with
  a "(saved)" label. Deliberately does NOT resolve.
- **Keep mine** → re-stat and adopt the mtime (not the recorded one —
  the user may have spent a minute reading the diff), then run `resume`.
- **Take disk** → `ReloadUndoable`; drops the resume (writing the disk
  copy back is a no-op that only muddies mtimes).
- **Later** → marker stays, saves stay guarded, resume dropped.

Esc / click-away = Later: dismissing a question is deferring it.

**Deferral rule** — `conflictAfterEvent()` in the after-event slot
(beside `autoSaveAfterEvent`) raises the *frontmost* tab's unanswered
conflict once the modal slot is free. Detection can happen on a
background tick for any tab; the interruption only ever arrives for the
file the user is looking at. `noteConflict` clears `prompted` when the
disk moves again — new news deserves a new question.

### New editor primitive: `Tab.ReloadUndoable` (`internal/editor/tab.go`)

Reload (whose `initUndo` makes the disk copy the revert anchor), *then*
`pushUndoEntry(before, undoTop())`. Stack reads baseline=disk,
one-step-back=mine; Undo restores the user's text and `Dirty =
CanRevert()` correctly re-marks it dirty; Redo returns to disk. Failure
leaves the buffer untouched. Used by both the clean-tab reload and Take
disk — it is what makes "the editor replaced my text" a reversible act.

### Markers

`tabMarker(tab)` — worst news wins in the strip's single status slot:
`⊘` (Error) > `⚠` (DiagWarning) > `●` (Modified). Both new glyphs are
single-width per the runeLen house rule; `⊘` was already in the
codebase's established set (chat panel).

## Conventions / gotchas worth remembering

- Anything that writes a file ced holds open must either go through
  `saveGuard` or adopt the mtime it just created — otherwise ced files a
  conflict against itself. format-on-save was the first casualty found;
  check plugin writes (`plugincmd.go:327` reloads, so it's fine) and any
  future writer.
- A conflict raised during save-all-then-quit blocks the quit (nothing
  is lost; the user re-quits after answering). Same for the
  folder-switch save-all.
- `diskChangedSinceLoad` (autosave.go) is kept as the side-effect-free
  question; `autoSaveGuard` is the recording successor the loop calls.
- Test fixtures push mtimes a fixed number of *minutes* ahead
  (`externalWrite(..., minutesAhead)`) so two staged external writes can
  read as successively newer on a coarse filesystem.
- gopls still flags `../ced` files as "not in workspace" from the cats
  session — noise; `go build ./...` + `make test` in ced are the real
  checks.

## State / next steps

- Phase 2 marked done in `ai_docs/cats-native-plan.md`, with the three
  spec deviations recorded there.
- **Follow-up left on the table (noted in the plan):** after "Decide
  later" the only route back to the prompt is an explicit save. Clicking
  the tab's `⚠` marker should re-raise it — a small tab-bar hit-test in
  the Phase-1 stamped-rect idiom.
- Next per §7: **Phase 3.1 — the LSP completion popup** (~800–1000 LOC,
  the roadmap's centerpiece). Phase 5.3's `blocked` hook report will
  consume the conflict record as-is; nothing in reconcile.go needs to
  change for it.
