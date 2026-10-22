# Editor right-click rows + the change view (diff, original, Revert)

Session: `e475f892-5a43-4157-82e2-1a90ccdc665f`

## Asks

1. (Context: `e6bf2d5`, the Switch branch picker's "+ New branch…" row.)
   Give the editor pane's right-click menu a way to create a new branch.
2. Add a git status row there too.
3. Suggest more rows for that menu; then build suggestions 1, 2 and 4
   (Blame this line, Toggle line comment, selection hand-off rows).
4. A git option that shows one hunk's diff, the original below it, and a
   way to revert it.
5. Commit, `/next-list`, `/sess-wrap`, then cut a minor tag-only release.

## 1–3. Editor right-click rows (`668df6a`)

All in `editorContextItems` (`internal/app/contextmenu.go`). Each is the twin
of an existing ≡ row (same verb), APPENDED only where it applies, the cats
rows' rule: no permanently dimmed rows in a small popup.

- **Toggle line comment** (`menuToggleLineComment`) — only when
  `hasCommentableTab` (the language has a line-comment prefix).
- **Summarize selection… / Send selection to GoNotes…** (`menuSummarize`,
  `menuSendToNotes`) — only while the click KEPT a selection (it landed
  inside it); labels from the ≡ rows' `summarizeLabel` / `sendToNotesLabel`.
  The whole-file forms stay ≡ AI rows.
- **Git rows, last, inside a work tree** (`hasGitRepo`): Blame this line…
  (saved files), Git status…, New branch….
- "Pane" was read as the EDITOR pane; the tree's menu was offered and not
  taken up.
- Tests: `TestEditorContextNewBranchRow`, `TestEditorContextGitStatusRow`
  (real git), `TestEditorContextToggleCommentRow`, `TestEditorContextBlameRow`,
  `TestEditorContextSelectionHandOffRows`, helper `localXForCol`.
- Suggested, not picked: more LSP jumps on right-click (→ N-081). Noticed:
  `summarizeLabel`'s "(1 lines)" (→ N-080).

## 4. The change view (`01c35ff`)

New `internal/app/hunkview.go` (+ test). A modal for ONE hunk: the diff as
the gutter has it (`hunkTipLines`), then "Original — what Revert puts back":
the old lines framed by ±2 lines of buffer context, `[ Revert change ]`
(alt+r) and `[ Close ]` (default). Scrolls with the change popup's ▴/▾ and
"a–b of n".

- **Doors**: ≡ Git "Show change at caret…" (`menuShowHunkAtCaret`, gated
  `hasDiffHunks`, flashes "No change on this line — Next change jumps to one"),
  editor right-click "Show change…" (changed line only, first git row), and a
  LEFT press inside the change-bar popup (hunktip.go now keeps `hunkTip.hunk`;
  bottom border hint "click: revert…").
- **Baseline** is the gutter's: `git diff -U0` = working tree vs INDEX.
- **Revert** = `ApplyMultiEdit` with one Edit → one undo step, never saves, no
  confirm; caret lands on the restored text with `MarkJump`.
- **Verification** (disk-measured hunk, buffer edit): added/modified must
  still hold the "+" lines at Start..End, else `hunkStaleReason`. A pure
  removal has nothing to check, so it needs `hunksMatchBuffer`: no stamp and
  a clean buffer, or `App.fileDiffsRev[path] == EditRev`. A revert on exact
  hunks stamps the new EditRev; on inexact ones it stamps -1 (so an Undo back
  to clean can't read as exact); `handleGitDiff` clears it. That is what lets
  "revert this, then the removal below" work without a save in between.
- **EOF newlines**: `splitHunkSides` credits git's "\ No newline" to the side
  it follows; `hunkRevertEdit` bends the region end / trailing "\n" so the
  restored file ends exactly as the original did. CRs are trimmed (buffer is
  bare LF).
- **Gutter** catches up at once: `dropRevertedHunk` removes the hunk and
  shifts later ones by `len(old) - len(new)`.
- Tests: `TestRevertHunk_RoundTripsRealDiffs` reverts every hunk of real
  `git diff -U0` output top-down for 12 shapes (each kind, top of file,
  several hunks, emptied file, CRLF, all EOF combinations) and requires the
  committed text back. Plus refusals, one-undo-step, the stamp rules,
  `dropRevertedHunk`, rows, keys/default, click Revert, doors.
- Menu pins: 199 rows / height 205 / dividers `[2, 5, 202]`, Git fold 35,
  custom-actions 208; CLAUDE.md map, pins and a Change-bar rules bullet.
- Real binary (run-ced, scratch repo): view drew as designed; `{esc}r`
  reverted the buffer, the bar went, the tab went dirty, disk untouched. A
  too-small first capture still processed keys and SAVED on quit — the
  second run had nothing to show until the change was re-made.

## 5. Wrap

- `/next-list` (living list): no lapses, IDs unique. Premises re-checked for
  N-016, N-043, N-068 (still true). N-003's premise hit again: the change view
  had no README sentence — added to the Change bars bullet in this session.
- Release: minor, tag only (see the release commit after this doc).

## Next

Closed: None. Declined: None. Raised: N-079, N-080, N-081.
Deferred: None. Promoted: None. Moved: None.
Updated: N-003. Full list: `ai_docs/todo/next-list.md`.
