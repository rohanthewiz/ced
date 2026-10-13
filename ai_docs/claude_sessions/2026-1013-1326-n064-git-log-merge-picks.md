# 2026-10-13 13:26 — N-064: git log panel picks and reverts merges (-m 1)

Session: `2b4017d8-47b2-4807-9b7f-c62d256d8396`

## Request

Next-list item N-064 (pasted from the cats-todo backlog): the git log
panel's cherry-pick and revert of a MERGE commit — Actions ▾
single-commit rows and a tick set holding a merge — ran without `-m`, so
git refused with "is a merge but no -m option was given". Carry over the
cherry-pick dialog's rule: `-m 1`, only when a merge is in the command;
revert of a merge needs the same.

## What changed

- **gitlog.go** — `gitLogCommit.Merge` (more than one parent).
  `gitLogFormat` gained `%P` before the subject (the subject stays last so
  a tab inside it survives `SplitN`, now 7 fields). Only the parent COUNT
  is kept. The path-filtered log (gitlogfilter.go) shares the format and
  parser, so it gets the flag for free.
- **gitlogactions.go** — Cherry-pick row builds its argv with
  `cherryPickArgs([]string{c.Hash}, false, false, c.Merge)` (the dialog's
  function, so both doors share one rule); Revert with the new
  `gitLogRevertArgs` (`revert --no-edit [-m 1] <hash>`). Ordinary commits
  keep the exact argv they had. `confirmGitLogApply` adds a third line for
  a merge via `gitLogMergeNote(verb)`: "Merge: replays what it merged in
  (-m 1)" / "Merge: undoes what it merged in (-m 1)" (fits the 50-cell
  body). File header: new bullet on the first-parent rule; the old line
  claiming "revert refusals on merges land in the info modal" reworded.
- **gitlogticks.go** — the set ORs `Merge` over the ordered commits, passes
  it as `mainline`, and the confirm names the rule above "onto <branch>,
  oldest first?". Header bullet added.
- **CLAUDE.md** — Git log rule records the merge behaviour.

## Checked on the way

- Git 2.54 accepts `revert -m 1` on a NON-merge (scratch repo). The code
  still omits `-m` for ordinary commits so their argv is unchanged; the
  comment says so rather than claiming git would refuse.

## Tests

- `TestParseGitLogCommits` — 7-field fixture; two parents = merge, one or
  none (root) = not; empty parent field doesn't shift the subject.
- `TestLoadGitLogCommits_MarksMerges` — real git, `cherryPickMergeRepo`:
  exactly "merge main" and "merge side" flagged.
- `TestGitLogApplyArgs_MainlineOnlyForMerges` — argv for both verbs,
  plain vs merge.
- `TestConfirmGitLogApply_NamesTheMergeParent` — third line per verb,
  ≤ 50 cells; ordinary commit stays two lines.
- `TestGitLogActions_MergeEndToEnd` — real git: pick "merge side" onto
  main lands s.txt; revert it on topic removes s.txt. (First run caught a
  test bug: the second pump matched the first run's "— done" flash;
  `runGitLogApply` clears `statusMsg` first.)
- `TestGitLogTicks_SetWithAMergeEndToEnd` — c1 + merge ticked, one pick
  onto main, oldest first, merge diff = s.txt, confirm names `-m 1`.
- `TestGitLogTicks_MergeFreeSetHasNoMergeNote`.
- `go test ./... -race`: all 25 packages ok.

Not driven in the real binary (`run-ced`); the end-to-end tests run the
real git commands the rows issue.

## Next

Closed: N-064. Declined: None. Raised: None.
Deferred: None. Promoted: None. Moved: None.
Updated: None. Full list: `ai_docs/todo/next-list.md`.
