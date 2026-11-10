# Audit wave 1: data safety (N-113, N-115 – N-117, N-119, N-120, N-125)

Session: `4e4c6a59-bdb9-45cf-ad09-70519bddac23`

## Ask

"Start on the plan spanning next-list items N-113 to N-139" (the
reliability / performance / usability audit filed in `bf7db72`). Then:
one commit, and this wrap.

## The plan

The 27 items, ordered into waves so each lands on what it depends on:

1. **Data safety** — N-113, N-115, N-116, N-117, N-119, N-120 (done
   here), N-121 a–h (next), N-114 and N-118 (wait on decisions below).
2. **Main-loop hangs** — N-124 (`postResult` for completion events),
   N-122 (one git launcher: Setsid, `GIT_TERMINAL_PROMPT=0`, context
   kill, `GIT_OPTIONAL_LOCKS=0`), then N-127 on top of it (git status off
   the loop, folded forks, focus-gated polling), N-123 (LSP writer
   goroutine), N-126 (bounds; part d, the reproduced panic, first).
3. **Performance** — N-138a benchmarks FIRST so each fix is measured,
   then N-128, N-129, N-130, N-131, N-132.
4. **Usability** — N-133, N-134, N-135, N-136.
5. **Validate** — N-137 (user enables Actions), N-138b fuzz targets
   beside N-126d / N-114, N-139 (Docker 29.9 is running on this machine;
   no tmux installed, so the harness goes in a container).

N-125 was pulled forward into wave 1: high value, a one-line fix, and it
is the easiest way to fill the event queue N-124 is about.

## What changed (`ec635e7`)

- **N-125** — `deferFrame` (inputburst.go) skips the frame for any KEY
  handled while `pasting`; the start / end markers are EventPaste and
  still paint, as does any non-key event mid-paste.
- **N-113** — `savedLines` / `savedKnown` on Tab (undo.go): the text the
  disk is KNOWN to hold. Set by `initUndo` (shares the original's slice),
  `markSaved` in Save and both ReloadAsEdit paths (a header copy, since
  MoveLines rewrites `Buffer.Lines` in place). Undo / Redo / RevertFile
  set `Dirty = !matchesSaved()`. `ForgetSavedText` (no undo state can
  read clean) is called by DiskGone, the formatter's dirty branch, and
  `conflictKeepMine`.
- **N-115** — `WriteFileAtomic` falls back in place only when
  `tempRefusedByPermission` (fs.ErrPermission or EROFS); otherwise the
  CreateTemp error is returned. `writeInPlace` = WriteAt, Truncate to
  length, Sync (no O_TRUNC). `createTemp` is a test seam.
- **N-116** — `formatClaim` (format.go) is the only launch path for
  `execFormatterChain` / `execInProcessFormat`: one `formatRun` per
  path in `App.formatActive`, a mid-run request folds into `rerun`
  (`rerunQuiet` false if any folded request was explicit). The event
  carries `seq` + claim-time `rev`. `handleFormatDone` drops a
  non-matching seq, then `settleFormatRun`: dirty → keep edits +
  `AdoptDisk` + ForgetSavedText; clean but EditRev moved → `tab.Save()`
  the buffer back over the formatter's stale write and force a rerun;
  else ReloadAsEdit. `formatRerunIfQueued` skips a dirty tab. Found
  while writing it: a lost done-event (N-124) would now queue a file's
  formatting forever, so a run older than `formatRunStale` (2m) is
  superseded and its marks released; `formatRunEnd` moved after the seq
  check so a late event can't double-decrement.
- **N-117** — new `internal/editor/diskstamp.go`: `DiskStamp` (mtime,
  size, inode via reflection on `Sys()`, so the package needs no
  `syscall.Stat_t`), `Same` = equality not After. `Tab.DiskChanged`:
  stamp same → unchanged, unless RACY (mtime within `racyGranule` 2s of
  `diskSeenAt`), then hash-check; stamp differs → read + sha256, same
  bytes → adopt stamp (a touch is not a change). `readTextFile` returns
  a `diskRead` with the pre-read stat's stamp + raw-bytes sum;
  `writeFileAtomic` returns the temp handle's stat (rename keeps mtime /
  size / inode) for Save to record. `AdoptDisk`, `ForgetDiskStamp`,
  `recordDisk` are the only writers; `Tab.Mtime` is a read-only mirror.
  App: `diskChangedUnderTab` replaces `diskNewerThanTab` (both guards,
  workspace edits), `diskConflict.stamp` + `!Same` for "new news",
  `renamedTabDisk` keeps the stamp across a rename (re-reading would
  adopt an external write), `diskChangedSinceLoad` updated.
- **N-119** — `session.Update(path, fn)`: Load, apply, Save. A parse
  failure (`ErrCorrupt`, wrapped with `%w: %w`) renames the file to
  `state.json.bad` first; a read error writes nothing. `Save` uses
  `os.CreateTemp` + chmod 0644. App `updateSessionStore` runs fn on the
  in-memory store (this run's view, which tests share between Apps) AND
  through Update; Touch, Record, and the recent-folder prune use it;
  `saveToolLayout` calls Update directly (still silent).
- **N-120** — `Load` unmarshals into `map[string]json.RawMessage`;
  `decodeFileFormat` walks `fileFormat`'s tags (exact, then
  case-insensitive), `configValueString` accepts string / bool (→ on /
  off) / number / null. Every `return Defaults(), …` became an append;
  `joinConfigErrors` makes one "N bad values: a; b" line. A file that is
  not JSON still loads as defaults.

## Docs

CLAUDE.md: undo baseline, fileio fallback rule, new **Disk versions**
bullet, state.json merge + config-per-key bullets, one-formatter-run
bullet, `diskstamp.go` in the architecture map. README config section:
booleans + per-key errors.

## Tests

Each fix's reproduction was checked to FAIL on the old code (by
temporarily reverting the decisive line) and pass after:
`TestUndo_AfterSaveBackToOpenedTextIsDirty`, `TestRedo_BackToSavedTextIsClean`,
`TestRevertFile_AfterSaveIsDirty`, `TestForgetSavedText_*`,
`TestReloadAsEdit_FormattedTextIsTheNewBaseline`, the app twins
`TestReconcile_DeletedFileStaysDirtyThroughUndo`,
`TestConflictKeepMine_UndoAfterwardStaysDirty`,
`TestFormatOnSave_KeptEditsUndoStaysDirty`;
`TestDeferFrame_PasteContentNeverPaints` (402 → 2 frames);
`TestWriteFileAtomic_FullDiskLeavesTheOriginal`,
`TestSave_FullDiskKeepsTheTabDirty`;
`TestHandleFormatDone_StaleRunDoesNotSwapANewerSave` (real in-process
JSON rerun), `TestFormatClaim_*`, `TestFormatRerun_*`;
`TestDiskChanged_OlderMtimeIsSeen`, `…_SameGranuleRewriteIsSeen`,
`TestReconcile_BackdatedWriteReloadsCleanTab`,
`TestSaveGuard_BlocksOverABackdatedWrite`;
`TestRecordSession_TwoInstancesKeepEachOthersEntries`,
`TestLoadSessionStore_CorruptFileIsSetAsideNotOverwritten`;
`TestLoad_OneBadValueKeepsTheRest`, `…_AcceptsJSONBooleans`.

Changed, on purpose: `TestHandleAutoSave_SkipsExternallyChangedFile`
faked an external edit with a bare touch, which is no longer a change;
it now writes real bytes, and `TestHandleAutoSave_TouchIsNotAConflict`
pins the new rule. Hand-built `formatDoneEvent`s in tests go through
`deliverFormatDone` (claims first); `TestFormatRunTracking_ClearedOnDone`
claims instead of calling `formatRunBegin`.

`go vet ./...` and `go test ./... -race` green (26 packages, no races).
`gofmt -l` flags seven test files, all unformatted before this session
(alignment from a newer gofmt), left alone.

## Waiting on the user

- **N-114** — policy: read-only / auto-save-off stopgap first, or
  Latin-1 decode + re-encode?
- **N-118** — where recovery files live outside a repository
  (`~/.config/ced/recover/` would be a new config-dir entry).
- **N-133** — root `ced path/to/file` at the repository (a documented
  behaviour change)?
- **N-137** — enable Actions in the fork's Actions tab.

Next up without a decision: N-121, then N-124 (more urgent now that
formatter runs lean on their done-event).

## Next

Closed: N-113, N-115, N-116, N-117, N-119, N-120, N-125. Declined: None.
Raised: None. Deferred: None. Promoted: None. Moved: None.
Updated: N-121, N-124, N-135. Full list: `ai_docs/todo/next-list.md`.
