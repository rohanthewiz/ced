# Git log defaults to the current branch; branch picker in its header

Session: `2261278e-0ecb-4632-a006-9bd2e63a9c59`

## Ask

"In the Git log tool window, shouldn't it default to the current branch?
Also, a branch switcher would be nice."

## What changed

### Scope (new `internal/app/gitlogscope.go` + `gitlogscope_test.go`)

- `gitLogState.scope`: `""` = current branch (no revision → `git log`
  walks HEAD, so it follows a branch switch for free), `gitLogScopeAll`
  (`"*"`, unrepresentable as a ref name) = `--all`, else a FULL refname
  (`refs/heads/x`, `refs/remotes/origin/x` — never starts with `-`,
  never collides with a tag/file). Session-only.
- `gitLogFilterArgs(q, scope)` is now the one argv builder: the plain
  list (`loadGitLogCommits(root, scope)`) is its empty query, so list
  and search can't cover different histories. The scope rides before
  any `--`. `c:` commit mode ignores the scope (a blame click must find
  a commit off the branch).
- Header button `⑂ <name> ▾` after ⌕ search on the left chain (name
  elided at 24); toolbar and title now start after it. Press → picker
  "Show in git log": Current branch (name), All branches, other local
  branches, then remote-tracking (symrefs like origin/HEAD dropped);
  the shown one says "(viewing)". While viewing a local branch you're
  not on, the first row is **Check out <branch>** (`git switch`).
- ≡ Git row **Git log branch…** (opens the log first, like Search
  history…) + button tip "Choose which branch the log shows…".
- A scoped branch that vanishes (deleted/pruned) → on an empty refresh,
  `rev-parse --verify` confirms it's gone, scope falls back to current,
  flash "<name> is gone — showing the current branch".
- Comments in gitlogactions.go / gitlogticks.go that said "the log
  lists --all" updated: ticks survive a scope change, so a cross-branch
  set is built under All or by switching branches.

### Tests / pins

- New: scope revs, ref before the fence, commit mode ignores scope,
  ref short names, ref parsing (locals first, symrefs dropped), real
  repo default-vs-all-vs-named, picker rows, set-scope reload/no-op,
  gone-branch fallback, Check out against real git, header button draw
  + press, label elision, ≡ door.
- Existing loader tests pass an explicit scope (All where the fixture's
  commits live off HEAD — merges on topic, the tick fixture, which now
  also sets `gitLog.scope = All` so `showTool`'s refresh keeps them).
- Menu pins +1 row: 208 rows (191 actions), height 250, dividers
  `[2, 5, 247]`, Git fold 36 rows / 44 height, custom actions 253,
  tall-window heights 250.
- README: Git menu-reference row, "Show git log" description, and a
  "Which branch the git log shows" paragraph before the tick-set one.
  CLAUDE.md Git log rule rewritten for the scope.
- `go test -race ./...` green. Real binary (run-ced): header reads
  `⑂ main ▾`, click opens the picker with Current (viewing) / All /
  local / origin/main.

## Next

Closed: None. Declined: None. Raised: N-111.
Deferred: None. Promoted: None. Moved: None.
Updated: None. Full list: `ai_docs/todo/next-list.md`.
