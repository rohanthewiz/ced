# N-136, N-140, N-144 — the undo-trim notice, git panels off the loop, recovered edits

Session: `bf5fffcd-9ebf-4213-a664-3cfa9fde36c5`

## Ask

Seventh batch of the `/work-next-list` run (previous:
`2026-1117-2352-n132-n148-n134-n135-find-fastpath-help-menu`).

## N-136 — the undo-trim notice (`352dfef`)

editor/undo.go: `trimUndo` counts dropped steps (`Tab.undoTrimmed`);
`TakeUndoTrimNotice` answers true once per tab; `UndoCaps` exposes the
caps for the message. app.go: `undoTrimAfterEvent` in the after-event
hook list (after `syntaxAfterEvent`), asks every tab (a workspace edit
can fill a background one), flashes "<file>: undo history is full — its
oldest steps are now dropped (keeps 500 steps, 32MB)", one tab per
event. No status-bar segment by decision (rare since N-131; the flash is
kept in Messages…).

## N-140 — nothing forks on the loop when the snapshot lands (`4f64bec`)

- gitpaneltick.go (new): in an async App (`gitSnap.async`, set by
  `New`), `applyGitStatus` calls `gitPanelsReloadAsync` instead of the
  inline `refreshGitLogCommits` / `refreshConflictPanel*`.
  `gitLogLoadedEvent` (seq, scope, toplevel, commits, truncated,
  scopeGone) and `conflictLoadedEvent` (seq, full, data); handlers drop a
  stale seq, a closed panel, an applied filter or a changed scope; a gone
  scope runs `gitLogScopeGone` on the loop and reloads. Splits:
  `gitLogRefGone` (pure), `loadConflictPanelData` /
  `applyConflictPanelData` (refreshConflictPanel is now the two). Inline
  paths (open, ⟳, post-command hooks, directly built test Apps) unchanged.
- Finder: `filetree.Tree.Refresh` returns whether any loaded folder's
  listing changed (`reloadChanged`: new name / kind, or a survivor count
  below the old child count). `refreshTreePipeline(false)` (tick,
  custom-action done, focus-in) → `finderTickRebuild(changed)`: rebuild
  only on a change or when the index is older than `finderMaxAge` (60s,
  the backstop for collapsed folders in a non-git project). The snapshot
  carries `FilesSig` (new `# branch.oid` parse + every entry's XY and
  path); `finderAfterGitStatus` rebuilds when it moves (the first one only
  records). `invalidateFinder` stamps `finderBuiltAt`.
- Tests: the finder ones needed a real finder (`withFinder`) —
  `newTestApp` has none, so `invalidateFinder` returned before stamping —
  and baselines in the past (two `time.Now()` readings can be equal).

## N-144 — Recovered edits (`e6afaf9`)

crash/rescue.go: `Folder`, `List(dir, root)` (skips folders without an
INDEX or for another project; newest first), `parseIndex` (the When / Why
/ Project header and the `  <rel>  <-  <orig>` lines; "(untitled)" →
""). app/recovered.go (new): ≡ File row after the session toggle,
`recoveredEditsLabel` "Recovered edits (N)…" via a 5s cache
(`recoveredCache`), `menuRecoveredEdits` (fresh read; none → a flash
naming the recover dir), `recoveredFileActions` (open the copy / Compare
with <file> when the original exists — opens the original, then
`compareWithFile(rescued)` so `[↺]` restores rescued hunks / read the
INDEX). README File table and the pins (+1 row → 218 rows, height 260,
dividers `[2, 5, 257]`, custom 263) and CLAUDE.md's copy updated.

## What's left on the list's doable side

N-149 (realign after unpatched edits on big files) and N-150 (revisit
`MaxHighlightBytes`) — both low, raised by this run.

## Next

Closed: N-136, N-140, N-144. Declined: None. Raised: None.
Deferred: None. Promoted: None. Moved: None. Updated: None.
Full list: `ai_docs/todo/next-list.md`.
