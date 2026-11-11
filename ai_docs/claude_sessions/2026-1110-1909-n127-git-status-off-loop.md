# N-127: the git status snapshot off the main loop

Session: `21bd5ee1-850c-40c6-9515-1b64e97f4048`

## Ask

Load the last session (`/sl`), then "Work on N-127", then this wrap.

## The decision

N-127 had four parts, filed by the audit: (a) run the snapshot on a
coalesced goroutine, (b) fold its six forks into one porcelain v2 call,
(c) reconcile on focus-in and STOP polling while unfocused, (d) skip a
tab's tick diff when nothing moved.

(c) was put to the user before building, because cats forwards per-pane
focus (its `focus_test.go`: the pane losing focus hears CSI O) and tmux
does with `focus-events on`. "Unfocused" there means "in the next pane",
often watching an agent write files beside ced, so stopping the tick
would freeze exactly that view. Options: keep polling + focus-in refresh;
poll slower when unfocused; stop (as filed). **The user chose slower**:
30s while unfocused, an immediate refresh and 10s on return.

Design calls made while building:

- **Inline stays where the next line reads the snapshot.** A finished
  git command's onOK hooks (gitOp, the conflict set, the upstream) and
  the conflict stop hook would read the pre-command state from an async
  refresh; they use `refreshTreeAfterGit` (inline, two forks after a
  command the user already waited for). Everything else is async.
- **The stash rides rev-parse**, not `status --show-stash`: porcelain v2
  prints `# stash N` only from git 2.35, and Ubuntu 22.04 ships 2.34.
  `--glob=refs/stas[h]` — the bracket stops git anchoring the pattern
  with `/*`.
- **The git panel reads the snapshot's rows** instead of forking its own
  status on every apply; its direct read (`loadGitStatusFiles`) shares
  `parsePorcelainV2`. XY codes stay in v1's spelling (`.` → space, `??`).

## What changed

- **`internal/app/gitsnapshot.go`** (new): `gitStatusEvent`,
  `gitSnapState` (`async`, `seq`, `flightSeq`, `startedAt`, `rerun`),
  `refreshGitStatus` (async, coalesced), `refreshGitStatusNow` (inline,
  clears a pending rerun), `launchGitStatus`, `handleGitStatus`,
  `applyGitStatus` (the old body, moved from app.go). Only the latest
  launch applies; a scan in flight past `gitSnapshotStale` (30s) is
  presumed lost (N-124) and relaunched. `New` sets `async`; a directly
  built App runs inline, so the existing tests are unchanged.
- **`internal/app/gitstatus.go`** (reworked, header flipped):
  `loadGitStatus` = `rev-parse --show-toplevel --absolute-git-dir
  --glob=…` + `status --porcelain=v2 --branch -z`; `parsePorcelainV2`,
  `porcelainEntry`, `pathSets`, `parseBranchAB`, `revParseHasStash`,
  `loadGitShortHead`; `gitStatus.PanelFiles`. Upstream kept only when
  `# branch.ab` is present (a gone upstream reads as none, as before).
  Retired, unused after the switch: `loadGitTracking`,
  `parseTrackingCounts`, `loadGitHasStash`, `parsePorcelain`,
  `hasStagedPorcelain`, `stagedPorcelainSet`, `conflictPorcelainSet`,
  `unquotePath` (tests ported).
- **`internal/app/tickpace.go`** (new): `treeRefreshUnfocusedInterval`
  (30s), `treeRefreshOnFocusChange`, `noteUserPresent` (a key or button
  press while "out" counts as focus-in), `paceTreeRefresh` (restarts a
  running tick, never starts one).
- **app.go**: `startTreeRefreshEvery`, `treeRefreshEvery`, `unfocused`,
  `gitSnap`, `fileDiffStamps` fields; `workspaceChangedGit`,
  `refreshTreeAfterGit`, `refreshTreePipeline`; dispatch for
  `gitStatusEvent`, focus pacing, `noteUserPresent` on keys and presses.
- **gitdiff.go**: `diffStamp` (file + index `editor.DiskStamp`),
  `diffStampFor`, `requestFileDiffStamped`; the tick skips a path whose
  stamps match; stamps recorded on the event's arrival.
  `editor.RacyGranule` exported for the same "too fresh to trust" rule.
- **gitpanel.go**: `setGitPanelFiles`, `gitPanelFilesFrom`;
  `loadGitStatusFiles` on v2. gitcmd.go / gitconflict.go use
  `refreshTreeAfterGit`. autosave.go's focus comment rewritten.

## Found on the way

- My first `handleGitStatus` used an `inFlight` bool: an inline snapshot
  overtaking an async scan left it set after the stale answer arrived,
  blocking every refresh until the 30s relaunch. Replaced by `flightSeq`
  ("is this the scan in flight?" vs "is it the latest launch?").
- The first diff-skip test backdated `.git/index` to before its own
  entries — git reads that as racy and rewrites the index on the next
  status, moving the stamp under test. The fixture now ages files the
  way a settled repo does (entry oldest, index newest).
- Eight test files already fail `gofmt -l` at HEAD (contextmenu_test.go,
  diffselect_test.go, …), untouched here.

## Docs

README: the Live file tree bullet (30s while unfocused, catch-up on
return, background git status). CLAUDE.md: three rules (tick pace,
async snapshot, stamp-gated gutter diffs) and the two new files in the
architecture map.

## Tests

`gitsnapshot_test.go`: inline without async, async applies on the event,
coalescing (three requests → two scans, the rerun sees a later file),
stale answer after inline, inline satisfies a rerun, lost-answer
relaunch, rerun waits for the newer flight, wrong root ignored, git
command hooks see the command's result while async (seen FAILING with
`refreshTreeNow` there), conflict stop hook while async, panel filled
from the snapshot. `tickpace_test.go`: pace out/in and repeats, focus-in
refreshes (and a startup "in" doesn't), key and click count as focus,
motion doesn't, no tick ever started. `gitstatus_test.go`: v2 entries,
malformed rename consumes its source, junk, branch headers (gone
upstream), `parseBranchAB`, `pathSets`, `revParseHasStash`, real-repo
stash (a branch named `stash` doesn't count), detached short SHA, and
`TestLoadGitStatus_PanelCodesMatchPorcelainV1` (v1 parity on a real
repo). `gitpush_test.go`: tracking end to end incl. behind and gone
upstream. `gitdiff_test.go`: skip when unchanged / re-diff on file or
index change (seen FAILING with the gate off), trust rules, record or
forget, closed-tab pruning. `make test` (race) green, 26 packages.

Measured on ced's own repo (throwaway test, HEAD in a scratch worktree
vs. this tree): `loadGitStatus` 190 → 69 ms; `refreshTreeNow`'s
main-loop time 192 ms → 0.29 ms. Single git forks cost 35–50 ms to start
on this Mac.

Real binary (run-ced, scratch repo with upstream): status bar `main ↑1`
and git panel ` M main.go` / `?? untracked.txt` from the async startup
snapshot; a file created while unfocused (CSI O) appeared on CSI I at
~4s, and not in the control run without CSI I; gutter `▎` on open.

## Next

Closed: N-127. Declined: None. Raised: N-140, N-141. Deferred: None.
Promoted: None. Moved: None. Updated: N-122, N-124.
Full list: `ai_docs/todo/next-list.md`.
