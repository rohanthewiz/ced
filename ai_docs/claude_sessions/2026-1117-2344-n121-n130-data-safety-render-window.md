# N-121, N-130 — eight data-safety fixes, and Render styles only what it shows

Session: `bf5fffcd-9ebf-4213-a664-3cfa9fde36c5`

## Ask

Fifth batch of the `/work-next-list` run (earlier: `2026-1117-2243-…`,
`…2301-…`, `…2313-…`, `…2326-…`). N-121 was worked part by part, a commit
per part or pair, the item closing when the last landed.

## N-121 — data safety (`59483fd`, `065fb31`, `c575ae6`, `d8538d8`, `d75f511`)

Every part's regression test was checked to FAIL with its fix stashed.

- **(f)** `requestCloseTab`'s Save/Discard resolve the `*editor.Tab` via
  `tabIndexOf` instead of a captured index
  (`TestRequestCloseTab_DiscardClosesTheSameTabAfterAShift`).
- **(e)** copypaste.go: `notCreatedError` marks a failure creating the
  destination ITSELF (O_EXCL taken, source unreadable, Mkdir/Symlink
  refused); `ownedErr` strips it once a parent the copy created is above;
  `runCopyPlan` removes the destination only if `copyCreatedDest`
  (`TestRunCopyPlan_FailureKeepsADestinationItDidNotCreate`,
  `…_FailureRemovesItsOwnPartialFolder`).
- **(b)** autosave.go: skip `runFormatOnSave` while `tab.CanRedo()` — the
  save still happens (`TestAutoSaveTab_KeepsRedoByNotFormatting`; first
  fixture undid back to the saved text → clean; fixed with two edits,
  one undo).
- **(c)** `Tab.DiskGoneClean` / `DiskGoneRev` set at the vanish; on return
  an untouched clean tab has Dirty cleared and takes the existing clean
  path (`ReloadUndoable`) — `TestReconcile_CleanTabWhoseFileReturnsReloads`,
  `TestReconcile_EditedWhileGoneStillConflicts`.
- **(d)** editor: `encodeText` (shared with `Tab.encode`),
  `EncodeLikeTab`, `EncodeLikeFile` (folds CRLF first; missing/unopenable
  file → verbatim). `chatFSWrite` uses them + `WriteFileAtomic`; an
  unencodable write is refused back to the agent
  (`TestHandleChatFSRequest_WriteKeepsTheFilesFormat`,
  `TestEncodeLikeFile_KeepsTheFilesFormat`).
- **(g)** fileops.go: `unsavedUnder`, `unsavedDeleteLines` (one name as a
  sentence, else count + three names + "…and N more"),
  `openDeleteConfirm` used by ≡ Delete file / folder and the tree's
  `ctxDelete`; the marked-set confirm appends the same lines
  (`TestMenuDeleteFolder_NamesUnsavedTabsInside`).
- **(a)** new editor/savemeta.go: `readTargetMeta` (mode incl.
  setuid/setgid/sticky, uid/gid, nlink from `syscall.Stat_t`),
  `keepIdentity` (Chown the temp when owner/group differ, copy xattrs via
  `x/sys/unix` Listxattr/Getxattr/Fsetxattr), `syncDir`, `*ReadOnlyError`
  / `IsReadOnlyError`. writeFileAtomic: refuse a target without owner
  write; nlink > 1 → in place; keepIdentity or a chmod failure → in
  place; fsync the dir after the rename. Auto-save skips a read-only
  refusal silently (like an unencodable one). `golang.org/x/sys` became a
  direct dependency (`go mod tidy`). Four tests, run on macOS AND on Linux
  (golang:1.26.1 container, host module cache mounted read-only,
  `GOFLAGS=-mod=mod GOPROXY=off`) — none skipped on either.
- **(h)** kept as is by decision (recorded in the closure): a minority CR
  stays a buffer character because that preserves the file's bytes, and
  it shows as `␍` since N-114.

## N-130 — Render styles only the visible runes (`a35fdce`)

tab.go: the per-line block now picks one of three paths — unwrapped
without ghost (`lineWindow` walks the string: scroll column, first rune
past the edge, overflow; `lineStylesFor` resolves `[ScrollX, hi)`; the
paint walk uses `runeWalker` over the string and stops at `paintEnd`;
on overflow `visualCol` is bumped past the pane so the `›` arrow and the
note slot behave as before), wrapped (`[]rune` for `wrapStarts`, styles
per painted row), ghost line (the old whole-line path). A memprofile
then showed the last 8MB/frame was two caret helpers converting the
caret's line: `PosScreenCell` now uses `LineVisualColString`,
`bracketAtCaret` uses `runesAroundCol`.

`BenchmarkTabRender_OneLongLine`: 22 ms / 85MB → 0.74 ms / 64KB.
`TestRender_LongLineFrameStaysSmall` (≤ 1MB), plus equivalence tests for
the walkers (`TestLineVisualColString_AgreesWithTheRuneSlice`, a
horizontally scrolled line with tabs and a wide rune — the leftmost
content cell is the `‹` arrow's, so it isn't probed). Real binary: a
1MB one-line bundle.js scrolled to its end draws normally (a 120×12
window is below ced's minimum — "Window too small").

## Next

Closed: N-121, N-130. Declined: None. Raised: None.
Deferred: None. Promoted: None. Moved: None. Updated: None.
Full list: `ai_docs/todo/next-list.md`.
