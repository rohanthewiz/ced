# 2026-10-11 20:44 — Commit receipt gets a Push button

Session: `8779e0bb-7577-49e9-85f0-affb2000f8a4`

## Request

Expand the "Committed" popup (the commit receipt) with a link to the push
popup, reusing the git panel's Actions "push" action.

## What changed

**Button on the commit receipt** (`internal/app/gitcommitreceipt.go`)
- New `commitReceiptState.pushBranch`: the branch the
  `[ Push <branch>… ]` button names; empty = no button.
- Set in `handleGitCommitReceipt` AFTER `openGitReceipt`, under the same
  condition the git panel's Actions picker lists its "Push <branch>…" row
  (`hasGitPushTarget()` + `a.gitBranch != ""`). `openGitReceipt` and
  `closeCommitReceipt` reset it, so a push receipt replacing a commit
  receipt never inherits the button.
- Captured at OPEN, not read per frame: the post-commit status refresh
  lands while the panel is up, and a button appearing/vanishing would
  resize the box under a pointer already heading for it.
- `commitReceiptRect` grows one row when the button is offered (button
  row directly above the bottom border, right-aligned, 2-cell inset).
- ONE geometry method, `commitReceiptPushBtn()` (btnRect house rule),
  read by `drawCommitReceipt` and `commitReceiptPushHit`. Returns
  `ok=false` when not offered or when the label won't fit inside the
  border (no clipped buttons).
- Drawn with `drawButton`, accent, unfocused — a pointer target, not a
  keyboard default.
- Header comment extended with the rationale + an ASCII diagram.

**Press routing** (`internal/app/app.go`, the receipt branch of
`handleMouse`)
- A LEFT press on the button: close the receipt first (it paints below
  the modal layer), then `a.openGitPush()` — the exact action behind the
  Actions row and ≡ Git, so the dialog, its refusals (detached HEAD, no
  remotes) and the later "Pushed" receipt are all the existing ones.
- Right/middle press: unchanged — dismiss, swallowed inside the box.
- Keys: unchanged — any key dismisses without being consumed. No key is
  bound to the button (it would break that contract); ≡ Git → Push is
  the keyboard door.

**CLAUDE.md**: git "Commit receipt" bullet notes the button, its
condition, capture-at-open, and that it runs `openGitPush`.

## Tests (`gitcommitreceipt_test.go`)

- `TestCommitReceipt_PushButtonOnlyWithATarget`: no remote → no button
  and button-less height; remote + branch → button offered, drawn
  (`[ Push main… ]` on screen), height +1, hit-test matches geometry;
  detached HEAD → none; push receipt replacing a commit receipt → none.
- `TestCommitReceipt_PushButtonOpensPushDialog` (real repo + bare remote
  via `initRepoWithRemote`): right-click on the button only dismisses;
  left click closes the receipt and opens `*gitPushModal` for `main`.

Gotcha hit while writing it: the press-swallowing check reads the rect
stamped by DRAW (`commitReceipt.box`). Without a draw, a right press on
the panel fell through to the ≡ menu. The real loop always draws before
input, so the test now calls `drawCommitReceipt()` first — not a product
bug.

`make test` (race) green across all packages. Not driven in the real
binary this session → N-060 in Validate.

## Next

Closed: None. Declined: None. Raised: N-060, N-061.
Deferred: None. Promoted: None. Moved: None.
Updated: None. Full list: `ai_docs/todo/next-list.md`.
