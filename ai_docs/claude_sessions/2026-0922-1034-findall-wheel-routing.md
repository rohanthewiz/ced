# Session: Find-all wheel routing + run-ced mouse scripting

Session ID: 9400c33c-d372-4700-a122-62c8c53d84aa
Date: 2026-09-22

## Report

> In the find all results scroll seems broken once a selection is made

## Diagnosis

- Unit-level repros (in-file and project mode, pinned and unpinned: wheel,
  click a row, wheel again) all scrolled the list correctly — the bug was
  not in the list's own scrolling.
- Drove the real binary with the run-ced capture tool. Mouse input did
  nothing at first: the script parser splits steps on `;`, and SGR mouse
  reports (`\x1b[<65;60;10M`) are full of semicolons. Built a scratch copy
  with a `{semi}` token to get past it.
- With mouse working, the real cause showed up: **unpinned, the Find-all
  list owns the modal slot, so `handleMouse` hands it EVERY wheel event in
  the window, and it answered all of them with `scrollList`.** Before a
  selection the user is wheeling over the list anyway, so nobody noticed.
  Once a row is picked, the preview moves the code into view under the
  strip, and wheeling over that code to read around the hit scrolled the
  list instead — the code never moved. Confirmed live: list scroll went
  0 → 9, editor stayed put. A pinned list never had the bug, because the
  router's `scrollAt` already routes by pointer.

## Fix

- `internal/app/findall.go` — `findAllModal.handleMouse`: the wheel is
  kept only when the pointer is inside the list's frame; outside, it goes
  to `a.wheelOutsideModal`. `m.rect(a)` is now computed before the wheel
  branch.
- `internal/app/app.go` — new `wheelOutsideModal(x, y, btn)`: the same
  targets as handleMouse's own wheel branch (`scrollAt` / `scrollAtH`,
  native WheelLeft/Right, Shift+wheel → horizontal). The modal handler has
  no event modifiers, so Shift is read from `lastShiftAt`, which
  handleMouse stamps BEFORE modal dispatch (covers this event and
  Zellij's split modifier report).
- Tests:
  - `TestFindAll_WheelOverEditorScrollsTheCode` (findall_test.go): after a
    row click, a wheel over the editor scrolls the code, leaves the list
    scroll alone and keeps the modal open; a wheel over the list still
    scrolls the list.
  - `TestWheelOutsideModal_ShiftRotatesToHorizontal` (app_test.go).
- `make test` (race) green. Verified in the real binary: after a click,
  three wheels over the editor scroll the code 9 lines, list unmoved.

## run-ced capture tool

- `.claude/skills/run-ced/capture/main.go`: `{semi}` token → literal `;`;
  `-help-script` documents it with a click (press `M` + release `m`) and
  wheel example.
- `.claude/skills/run-ced/SKILL.md`: `{semi}` + SGR mouse notes
  (1-based col;row, button 0 left / 2 right / 64,65 wheel up/down), a
  click-then-wheel recipe, and the fact that **only the LAST `SNAP` is
  written out** (the driver overwrites its snapshot each time) — compare
  before/after with two runs.

## Notes for next time

- A modal that shares the screen with live content (Find-all strip) must
  not claim input outside its own frame just because it holds the slot —
  the "unpinned = owns every mouse event" shortcut was the whole bug.
- Other unpinned surfaces that absorb the wheel globally might have the
  same shape; not audited this session.
