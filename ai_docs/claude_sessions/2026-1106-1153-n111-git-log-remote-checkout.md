# N-111: check out a remote-tracking branch from the git log's scope picker

Session: `af42c42e-2a50-46d7-827b-6f0ef7cf385f`

## Ask

N-111 from the cats-todo backlog: the git log's branch picker
(gitlogscope.go) offered **Check out** only for a viewed LOCAL branch.
Add a "Check out as local <name>" row for a viewed remote-tracking
branch, and settle the open question of whether the scope should persist
per project in `session.Entry`.

## What changed

- **`gitLogScopeRemoteCheckout`** (internal/app/gitlogscope.go): the
  picker's first row while viewing `origin/feat`:
  - a local branch already tracks it → "Check out feat (tracks
    origin/feat)" = `git switch <tracker>`. If several track it, the one
    named like the remote branch wins. No row when you're on it.
  - no local `feat` → "Check out as local feat" =
    `git switch --create=feat --track refs/remotes/origin/feat`
    (`gitLogTrackArgs`: name glued to the flag, full refname as start
    point, so a leading `-` can't become an option).
  - an unrelated local `feat` (other upstream, or none) → "Check out as
    local branch…" → a prompt seeded `origin-feat`. git's refusal covers
    a taken or invalid name.
  - Every variant runs through `runGitCmdOK`, and its success hook puts
    the scope back on HEAD, because the remote ref never receives the
    next commits, so a pinned scope would hide them.
- **`loadGitLogRefs`** asks `%(upstream)` too. `gitLogRef.upstream`
  (locals only) is how the tracker is found, with no extra fork.
  `parseGitLogRefs` reads a missing third field as "tracks nothing".
- **`gitLogRemoteLocalName`** strips one segment (`origin/feat/x` →
  `feat/x`), git's default refspec. A remote name containing `/` gets a
  longer suggestion, which is visible in the label before anything runs.
- **Decided: the scope stays session-only.** It is a view, not a setting.
  Restoring `origin/feat` days later opens the panel on a history nobody
  asked for, under a header easy to misread as the checked-out branch.
  The reason is in the file header.
- Checked first in a scratch repo that `git switch --create=x --track
  refs/remotes/origin/x` (full refname) sets the upstream.

## Docs

README "Which branch the git log shows" paragraph, CLAUDE.md git-log
bullet (remote checkout + session-only by decision).

## Tests

`TestGitLogScopeRemoteCheckout_Cases` (all four answers + no-row cases),
`TestParseGitLogRefs_ReadsUpstream`, `TestGitLogRemoteLocalName`,
`TestGitLogTrackArgs`, and two real-git tests on a clone:
`TestGitLogScopeRemoteCheckout_CreatesTrackingBranch` (upstream set,
switched, scope back to HEAD) and
`TestGitLogScopeRemoteCheckout_CollisionPrompts` (prompt seed, submit
creates `origin-side` tracking `origin/side`). `TestGitLogScopeItems`'s
remote case now pins "tracked by the current branch → no row". `make
test` (race) green.

## Next

Closed: N-111. Declined: None. Raised: None.
Deferred: None. Promoted: None. Moved: None.
Updated: None. Full list: `ai_docs/todo/next-list.md`.
