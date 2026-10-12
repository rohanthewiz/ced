# 2026-10-11 21:07 — Commit receipt holds its expiry under the pointer

Session: `7dc4d923-2d2b-485b-8afd-878aa3fc75f3`

## Request

N-061 from the next list: the commit receipt's 7s expiry kept running
while the pointer rested on the panel, so a slow reach for its
`[ Push <branch>… ]` button could lose the race. Hold the expiry while
the pointer is inside the box, re-arm on leave.

## What changed

**Hold / re-arm** (`internal/app/gitcommitreceipt.go`)
- New `commitReceiptState.held`: true while the pointer is inside the
  drawn box and NO timer for this receipt is live.
- New `noteCommitReceiptPointer(x, y, btn)`:
  - enter (motion inside, not held) → `held = true`, `seq++` — the bump
    is the cancel: the already-scheduled tick carries the old seq and
    `handleCommitReceiptExpire` drops it.
  - leave (motion outside, held) → `held = false`,
    `armCommitReceiptExpiry()` (a full 7s window).
  - everything else → nothing. Only the inside→outside TRANSITION
    re-arms; re-arming on every outside motion would push the expiry back
    forever for someone just moving the mouse over code.
  - Presses are ignored here (handleMouse's receipt branch closes the
    panel anyway). The wheel counts as a pointer report.
- `openGitReceipt` and `closeCommitReceipt` reset `held`, so a receipt
  replacing a held one (a push landing over a commit receipt) starts with
  its own live timer; the next motion inside re-holds it.
- File header gained a paragraph on why the window is held.

**Routing** (`internal/app/app.go`, `handleMouse`)
- `a.noteCommitReceiptPointer(x, y, btn)` runs right after the modal and
  menu early-returns and BEFORE every layer that can return early
  (find-hist dropdowns, hunk tip, dwell…). It never claims an event, so
  motion still reaches the layers beneath — it just has to see every
  pointer report. Receipt press-branch comment notes motion now holds the
  expiry.

**CLAUDE.md**: git "Commit receipt" bullet notes the hold/re-arm.

## Design notes

- Known limit, accepted: the re-arm needs the terminal to REPORT the
  leave. Motion tracking reports each cell crossed and the panel is
  centered with window around it, so leaving crosses outside cells
  first; a pointer that jumps out in one report leaves the receipt held
  until any key/click (which still close it). A terminal with no motion
  reports never holds at all — same behaviour as before.
- Window resize while held: the box re-centers; the next motion
  re-derives inside/outside. No special handling.

## Tests (`gitcommitreceipt_test.go`)

- `TestCommitReceipt_PointerInsideHoldsExpiry`: motion outside before
  entering changes nothing; entering holds and strands the open-time
  tick; motion + wheel inside keep the hold without re-arming; leaving
  re-arms, a pre-hold tick can't close it, the new tick does.
- `TestCommitReceipt_ReplacementIsNotHeld`: a push receipt replacing a
  held commit receipt is not held and its own tick closes it.

`make test` (race) green across all packages. Not driven in the real
binary → N-061 moved to Validate.

## Next

Closed: None. Declined: None. Raised: None.
Deferred: None. Promoted: None. Moved: N-061 → Validate.
Updated: None. Full list: `ai_docs/todo/next-list.md`.
