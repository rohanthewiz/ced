# 2026-10-08 17:50 — Tab menu "Copy to…" row (N-055)

Session: `797e751b-0955-45cf-ac31-53f1db6a6e5c`

## Request

From the cats-todo backlog, next-list item **N-055**: the tab right-click
menu had no "Copy to…" row (left out because the menu's row order is
pinned); ≡ File "Copy file to…" only reaches the ACTIVE tab. Add it so a
non-active tab can be copied elsewhere.

## What changed

- `internal/app/tabcontext.go` — new row **Copy to…** directly after
  *Zip file* (the "copy it out" group). It calls the shared
  `promptCopyTo([]string{t.Path})` with the CLICKED tab's path and acts
  IN PLACE (no `onTab`): its answer is a prompt and then a file elsewhere
  on disk, so a background tab is never dragged forward. Enabled on
  `hasPath` (untitled tabs dim). Header diagram, the "acts in place" note
  and the ≡-twin list updated (twin = ≡ File "Copy file to…").
- `internal/app/copyto.go` — door diagram gains `tab right-click "Copy to…"`.
- `internal/app/tabcontext_test.go` — row-order pin in
  `TestTabContext_RightClickOpensTheTabMenu` updated; new
  `TestTabContext_CopyToCopiesTheClickedTabInPlace`: a dirty background
  tab's BUFFER lands in the destination, the active tab stays in front,
  and the active tab's file is not what got copied.
  `TestTabContext_RowsDimWithoutAPath` already covers the dim predicate.
- `CLAUDE.md` — tab menu order, ≡-twin list, and Copy to…'s door list.
- `README.md` — tab-menu bullet and the "Copy files and folders anywhere"
  feature bullet mention the tab door.

## Design notes

- No new verb: every door into Copy to… shares `promptCopyTo` →
  `copyToTyped` → copypaste.go's engine, so the unsaved-buffer overlay
  (copy the buffer as shown, never save) comes free.
- Placement after Zip keeps the menu's look → change → place → copy →
  close grouping; only the one order pin moved.

## Verification

`make test` (race) — all packages pass.

## Next

Closed: N-055. Declined: None. Raised: None.
Deferred: None. Promoted: None. Moved: None.
Updated: None. Full list: `ai_docs/todo/next-list.md`.
