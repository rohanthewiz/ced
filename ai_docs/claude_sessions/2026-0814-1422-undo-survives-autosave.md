# Session: undo survives auto-save (and leaving now saves)

- Date: 2026-08-14
- Branch: `main`
- Repo: ced (`~/projs/go/ced`)
- Session id: `531d4d6d-7db9-4f5f-afb8-4014b7bf73ee`
- Predecessor: `2026-0813-1822-cats-native-phase6.md`

## What was asked

> Auto-save is happening too quickly so that I am unable to undo. Or
> should we allow undo beyond the save?

The second half of that question already had an answer, and it is worth
recording because it framed the whole session: **undo has always gone
beyond the save.** `Tab.Save()` (tab.go) writes the file, clears Dirty,
stamps Mtime and calls `breakUndoGroup` — that last one only closes the
typing-coalesce window so the next burst starts a fresh entry. Nothing
about writing to disk trims a stack. So "allow undo beyond the save" was
not the fix, and neither was slowing auto-save down.

## The actual bug

Format-on-save, not auto-save's speed:

```
2s idle → handleAutoSave → tab.Save()          (tab is now CLEAN)
                         → runFormatOnSave(quiet)
                         → goimports/gofmt -w   (rewrites the file IN PLACE)
                         → formatDoneEvent
                         → handleFormatDone: tab is clean → tab.Reload()
                         → Reload ends at initUndo()
                         → undoStack = nil, redoStack = nil, undoOriginal re-seeded
```

`internal/app/format.go:504` → `internal/editor/tab.go:367` →
`internal/editor/undo.go:175-182`. **The entire history was destroyed
roughly every idle pause in a Go file** — and unconditionally, because
`execFormatterChain` only reports process success, so a `gofmt` run that
changed nothing reloaded (and wiped) just as hard.

Reproduced end-to-end against both binaries with the `run-ced` capture
tool. Old build, after an auto-save + gofmt, two `Esc u` presses: screen
unchanged, nothing happens. New build: first press takes back the
reformat, second takes back the typing.

## Decisions the owner made

1. A reformat is **one structural undo step on top of the preserved
   history** (not invisible, not the existing one-step `ReloadUndoable`).
2. Plugin in-place rewrites get the same treatment.
3. Delay to 5s **and** configurable.
4. Plus: "what would it take to do a save on focus change" — which turned
   out to be cheap, because tcell already reports focus.

## What landed

```
internal/editor/tab.go        + test   ReloadAsEdit, readDiskState (shared guarded read)
internal/app/format.go        + test   ReloadAsEdit call site, changed-gated flash,
                                       formatRunBegin/End/Running
internal/app/plugincmd.go     + test   reloadPluginTarget → ReloadAsEdit
internal/app/reconcile.go     + test   skip a path with a formatter run in flight
internal/app/autosave.go      + test   autoSaveInterval, autoSaveTabIfEligible,
                                       autoSaveOnFocusChange, autoSaveDepartingTab
internal/app/tabbar.go        + test   switchToTab flushes the departing tab
internal/app/app.go           + test   EnableFocus, *tcell.EventFocus case,
                                       autoSaveDelay + formatRuns fields, welcomeIfQuiet
internal/userconfig/*.go      + test   "autosavedelay" key, parse + clamp + constants
CLAUDE.md, README.md                   the house rules and the user-facing note
```

18 files, ~1200 insertions. `make test` (race) green, `go vet` clean.

### `Tab.ReloadAsEdit` — the third Reload

The family now reads as three answers to "who wrote this file?":

| method | who wrote it | what happens to history |
|---|---|---|
| `Reload` | the USER asked for the disk copy | reset (initUndo) |
| `ReloadUndoable` | a FOREIGN writer, taken on the user's behalf | reset, mine one Undo away |
| `ReloadAsEdit` | **ced itself** (format-on-save, a plugin) | preserved, one step on top |

Shape is `pushUndo(undoGroupStructural)` + a direct Buffer write — the
`ApplyMultiEdit`/`ReplaceAll` route, not `SelectAll` + `InsertString`.
That keeps it to one undo step **without** touching `undoSuppress` (which
belongs to the multi-caret fan-out alone), and avoids bumping `EditRev`
twice and running a whole-file replacement through the deferred style
patcher only for `InvalidateStyles` to throw it away.

Details worth keeping:

- **Identical content is a true no-op** — no undo entry, no `EditRev`
  bump, no style invalidation. This is what stops the background cadence
  filling the stack, since gofmt on already-formatted code is the common
  case. The mtime is still adopted (the write was ours either way).
- **`Dirty = false` is load-bearing, not cosmetic.** A tab left dirty
  would bump EditRev → re-arm auto-save → save → format → reload, forever.
- **View via `RestoreView`**, which clamps and leaves `cursorMoved` false
  so the next Render can't yank a reader back to their caret.
- Carets drop on the changed path (columns moved), survive on the
  unchanged one (dropping a column every 5s would be its own bug).
- `undoOriginal` is NOT re-seeded, so `RevertFile` still means "the file
  as I opened it" and rewinds past the formatting.
- `readDiskState` was factored out of `Reload` so both paths share one
  copy of the size/binary/BOM/line-ending guards.

### The reconcile race (found during design, worth the detour)

`Save()` stamps Mtime *before* the formatter writes, so between the `-w`
write and `formatDoneEvent` the tab is clean with a stale mtime. If the
10s tree tick lands there, `reconcileOpenTabsWithDisk` either takes the
disk copy via `ReloadUndoable` (costing the history, clean tab) or
records a **conflict about ced's own write** — ⚠ in the strip, auto-save
suspended, a modal — on a dirty one.

Fixed with `formatRuns map[string]int` on App: a per-path COUNT (a chain
plus a re-save overlap), incremented before the goroutine starts,
decremented as `handleFormatDone`'s first act on every exit path. The
save guards are deliberately NOT taught about it — same window, but
hitting it needs a keypress inside ~100ms and the failure mode is a
prompt, not lost work.

### Delay: 5s, configurable

`"autosavedelay"` in config.json — a duration string (`"5s"`, `"800ms"`)
or a bare number of seconds, clamped to `[500ms, 5m]`. A **string**, not
a JSON number, because `saveKey` rewrites the file through a
`map[string]any`: a number would be the one key that couldn't survive a ≡
toggle. Floor and ceiling are argued in the constants — every auto-save
also execs a formatter, fires didSave and refreshes git status, so
sub-half-second is a fork bomb wearing a preference's clothes.

**No ≡ row**: the menu is for verbs, and a duration would need a picker
of canned values to have any menu shape at all. (Also avoids re-pinning
`TestMenuLayout_NoCustomActions`' geometry.)

Two different failure answers, deliberately: a **typo is loud** (the rule
every other key follows — a silently ignored value is one the user
believes is in effect), a value merely **out of range is clamped in
silence**. All reads go through `autoSaveInterval()`, which maps the zero
value to the default — tests build `App` as a struct literal and
`time.AfterFunc(0, …)` fires immediately.

### Save on focus change

- **Terminal focus out** — `scr.EnableFocus()` beside `EnablePaste`, one
  `case *tcell.EventFocus` in the dispatch switch, and
  `autoSaveOnFocusChange` which stops the timer and calls
  `handleAutoSave`. Best-effort by construction: Terminal.app never
  reports focus, tmux needs `focus-events on`, so the timer stays the
  backstop and this must never be the only path to disk.
- **Leaving a tab** — `autoSaveDepartingTab` from `switchToTab`, the
  single funnel, so clicks, `Esc ,`/`Esc .`, the switcher and the
  overflow button all get it.
- Neither owns any save logic; both go through `autoSaveTabIfEligible`
  (factored out of `handleAutoSave`'s loop), so there is still exactly
  one answer to "is this tab safe to write in the background", including
  the modal deferral. Both gated on the ≡ toggle — a focus-out write IS
  an auto-save.
- Focus-IN does nothing: the countdown is armed by edits, not attention.

**Trap worth remembering:** `tcell.NewEventFocus` leaves the embedded
`*EventTime` nil (unlike the key/mouse constructors), so calling `When()`
on a focus event panics. Read only `.Focused`. Tests build
`&tcell.EventFocus{Focused: false}` directly — `simscreen.EnableFocus` is
an empty no-op and there is no `InjectFocus`.

### Drive-by

The startup greeting was overwriting config-error flashes, which made
userconfig's whole "report a typo rather than ignore it" contract a
no-op — the user never saw the message and their misspelled key stayed
quietly inactive. Extracted as `welcomeIfQuiet()` (testable) and it now
yields to anything already pending. Verified live: a bad `autosavedelay`
now names itself in the status bar.

## Behavioral note for the release

With auto-save on, undoing a reformat is **transient**: the tab goes
dirty, so ~5s later it saves and reformats again, adding a step. That's
inherent to running both features together rather than a regression, but
it's the first thing anyone will notice after the fix.

## Process note

Ran an Explore agent to trace the wipe and a Plan agent to design the
primitive. The Plan agent's report landed after the plan was already
approved and implementation had started, and it was still worth reading —
it caught the nil `EventTime` panic, corrected my "silent degradation"
framing for config typos (userconfig is deliberately loud), flagged the
`autoSaveDelay` const-vs-field shadowing, and surfaced the dirty-tab half
of the reconcile race. All four were folded in.

## Verification

- `make test` (race) — all packages green.
- `go vet ./...` — clean.
- Real binary via the `run-ced` capture tool, on a scratch project with
  deliberately unindented Go:
  - after auto-save + gofmt: formatted, typing present
  - one `Esc u`: reformat undone, typing still there
  - two `Esc u`: typing undone as well
  - **old binary, same script: two presses do nothing at all**
  - `"autosavedelay"` at `1s` saved within 4s, at `30s` did not, garbage
    fell back to the 5s default and flashed the parse error.
