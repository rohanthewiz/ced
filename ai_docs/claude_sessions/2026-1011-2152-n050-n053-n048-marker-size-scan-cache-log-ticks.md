# 2026-10-11 21:52 — Next-list run: conflict marker size, panel scan cache, git log ticks

Session: `9b98e626-fd19-476b-bfc8-32905ef1963e`

## Request

`/work-next-list`, second batch (the first is
`2026-1011-2142-n054-n036-n052-theme-counts-readme-leaders-conflicted-tabs`).

## What changed

**N-050 — `conflict-marker-size` gitattribute**
- `internal/editor/conflict.go`: marker length is a parameter.
  `DefaultConflictMarkerSize` (7), `ParseConflictsSized(lines, n)`
  (ParseConflicts = default), `markerTail(line, ch, n)`,
  `isMidMarker(line, n)`. `Tab.SetConflictMarkerSize` /
  `ConflictMarkerSize`; the memo is keyed on (EditRev, size) so learning
  the size after a first scan rescans without an edit.
- `internal/app/gitopstate.go`: `loadConflictMarkerSizes(toplevel, abs)`
  — one `git check-attr --stdin -z conflict-marker-size` for the set;
  answers mapped back by INPUT ORDER (no path-spelling question);
  `parseConflictMarkerSizes` keeps only real non-default sizes.
- `gitstatus.go`: snapshot field `ConflictMarkerSizes`, asked only when
  something is unmerged (no fork on the steady-state tick).
- `app.go`: `a.gitConflictMarkerSizes`; refreshGitStatus calls
  `applyConflictMarkerSizes` (conflictview.go); wireTab sets the size.
- Disk count (`conflictCountFor`) scans at the file's size; the picker's
  stage-row probe `fileHasConflictMarkers(path, size)` (prefix of `size`
  `<`) asks git live in `gitConflictReady` — a 10s-old snapshot must not
  decide what is safe to `git add`.
- Tests: editor sized parsing + memo; `TestConflictMarkerSize_RealRepo`
  (real repo with `*.txt conflict-marker-size=10`, a parked
  cherry-pick); `TestParseConflictMarkerSizes`; the probe test gained a
  five-character case.

**N-053 — Conflicts panel disk-scan cache** (`conflictpanel.go`)
- `conflictPanelState.diskScans` (abs → size, mtime ns, marker size,
  count), rebuilt from the unmerged set on every refresh;
  `conflictCountCached` reuses a scan when the stat matches.
- Design choice: only the REFRESH reads through the cache. The verbs
  that gate `git add` (Mark resolved, Stage all & continue) still call
  `conflictCountFor` fresh — a same-size write inside one mtime tick
  could fool the cache, and there a wrong "no markers" commits markers.
- Test: `TestConflictPanel_DiskScanCachedByStat`.

**N-048 — git log multi-commit cherry-pick** (new `gitlogticks.go`)
- `gitLogState.ticked map[hash]gitLogCommit`. The row's leading blank
  cell is the tick (✓, Accent) and the click zone (toggle, highlight
  stays) — the tree marks' trick. Title gains "· N ticked".
- Actions ▾ (and so right-click, ≡, palette): "Tick <hash> for a
  multi-commit cherry-pick" / "Untick" after the apply pair; with ticks,
  "Cherry-pick N ticked commits onto <branch>…" and "Clear ticks (N)"
  lead the list.
- Ticks are never pruned against the visible list (it can be a search
  result); they clear on a successful pick or by hand.
- Order: a streamed `git rev-list <ticked>` walk, stopped once every
  tick is seen. The default walk never lists a parent before its child,
  so same-second commits keep ancestry — `log --no-walk=sorted` (pure
  committer date) was the first draft and was dropped for that reason.
- Confirm lists the set (capped at `treeMarkConfirmList`, announced),
  oldest first; refused while an op is parked (Conflicts panel raised).
- Tests in `gitlogticks_test.go`; `TestGitLogActionItems_Inventory`
  gained the Tick row. README cherry-pick section + CLAUDE.md git log
  rule updated.

## Commits

- `57d6445` Conflicts: honour the conflict-marker-size gitattribute (N-050)
- `6b8c689` Conflicts panel: cache disk scans by stat (N-053)
- `388127b` Git log: tick several commits and cherry-pick them together (N-048)

## Next

Closed: N-050, N-053, N-048. Declined: None. Raised: None.
Deferred: None. Promoted: None. Moved: None.
Updated: None. Full list: `ai_docs/todo/next-list.md`.
