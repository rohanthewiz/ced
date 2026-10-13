# 2026-10-13 12:42 — N-051: cherry-pick merges (-m 1) and a diff preview

Session: `3a4c5613-2655-4efc-bdbb-0afee553bee7`

## Request

Next-list item N-051 (pasted from the cats-todo backlog): the cherry-pick
dialog's remaining extras — picking a MERGE commit (which parent to offer,
and whether `-m` may ride along with non-merge picks in one command), and
a per-commit diff preview.

## Open questions answered

- **Which parent:** always the first (`-m 1`), no picker. Parent 1 is the
  branch the merge was made on, so `-m 1` replays what the merge brought
  into the source branch — the only reading of "bring that merge over" in
  a branch-to-branch pick. `-m 2` replays the source branch's own side
  against the merged-in one; left to the terminal.
- **One command:** yes. Since git 2.21 `-m 1` on a non-merge picks it
  normally. Checked by hand on git 2.54 in a scratch repo
  (`cherry-pick -m 1 c1 <merge> c2` landed all three) and pinned by
  `TestCherryPick_MergeEndToEnd`. `-m 1` is passed only when a merge is
  among the picks, so merge-free argv is unchanged.
- **Found on the way:** "merge main into feature" (the commonest merge on
  a feature branch) is listed by `--cherry-mark` as new (merges have no
  patch id), and picking it stops the sequence as an empty pick
  (reproduced). Such merges — every non-first parent already an ancestor
  of HEAD — are now `synced`: shown `[=]`, refused with the reason.

## What changed

**Merges** (`cherrypick.go`)
- Listing: `--diff-merges=first-parent` instead of `--no-merges`, `%P` in
  the format (parsed with `SplitN` so a subject may hold a US byte);
  `merge` = more than one parent. `--name-only` then lists a merge's
  first-parent files, so `⚠` overlap works for merges too.
- `loadMergeParentsNotOnHead`: one `git rev-list <non-first parents…>
  --not HEAD`; `markCherryPickSynced` flags merges none of whose other
  parents appear. `cherryPickCommit.pickable()` (= not applied, not
  synced) replaces the `applied` checks in tick / select-all / refilter /
  first highlight / Enter.
- Old git fallback: if the merge-aware log fails, rerun with the legacy
  args (`cherryPickLogArgs(…, legacy=true)`); `mergesHidden` → summary
  "merges hidden (git < 2.31)".
- `cherryPickArgs` gained `mainline` (`-m 1`); `picksMerge(hashes)`
  decides it. The git log ticks pass false (raised N-064).
- UI: `⑂` in a second marker cell after `⚠`; synced rows say "(nothing
  main lacks)"; detail row "merge, picked as -m 1 · touches …"; summary
  "merges as -m 1" only when a pickable merge is listed.

**Diff preview** (new `cherrypickdiff.go`)
- `alt+d` or the detail row's `▸ diff alt+d` toggle. A pane INSIDE the
  dialog: the dialog grows to the window (width ≤ 160), the list keeps
  ≤ a third (`cherryPickSplitRows`), the detail row follows the list, the
  pane (header rule + body on the editor background) fills to the
  options row.
- `git show --no-color --format=%B --stat --patch
  --diff-merges=first-parent` (retried without the flag on old git);
  capped at 4000 lines with the cut announced.
- Load asked for from the pane's draw (every highlight move ends in a
  frame), cached by hash (immutable, bounded at 64, dropped whole), the
  event carries its dialog pointer so an answer that lands while the
  branch picker holds the slot still fills it.
- Alt/Shift + Up/Down/PgUp/PgDn/Home/End and the wheel over the pane
  scroll it; bare keys still walk the list; scroll resets on another
  commit. Header: "<short> [vs its first parent]" and "a–b of n · alt+↑↓".

**Checked in the real binary** (`run-ced`, 120×36, a demo repo with a
side merge and a merge of main): list shows the synced merge `[=]` with
`⑂`, the side merge pickable, summary "merges as -m 1"; alt+d on the
side merge shows its first-parent diff (eof.go only).

**Tests**: updated `TestParseCherryPickLog`, `TestCherryPickArgs`,
`TestListCherryPickCommits_RealRepo`; new `TestCherryPickLogArgs`,
`TestMarkCherryPickSynced`, `TestListCherryPickCommits_Merges`,
`TestCherryPickModal_SyncedMergeRefusesAndSays`,
`TestCherryPick_MergeEndToEnd`, and in `cherrypickdiff_test.go`
`TestCherryPickSplitRows`, `TestCapCherryPickDiff`,
`TestCherryPickDiff_ToggleSplitsTheDialog`,
`TestCherryPickDiff_DrawsAndScrolls`, `TestCherryPickDiff_LoadsRealCommits`,
`TestHandleCherryPickDiff_LandsInItsOwnDialog`. `make test` (race) green.

**Docs**: README cherry-pick section (diagram, `⑂`, alt+d); CLAUDE.md
cherry-pick dialog rule.

Known limit, not raised: ticking a merge AND a commit it brought in (both
are listed) applies that change twice; the dialog does not warn.

## Next

Closed: N-051. Declined: None. Raised: N-064.
Deferred: None. Promoted: None. Moved: None.
Updated: None. Full list: `ai_docs/todo/next-list.md`.
