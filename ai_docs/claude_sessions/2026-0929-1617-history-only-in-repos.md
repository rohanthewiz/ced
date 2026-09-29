# Session: Persist the history database only in repositories

Session ID: 808dfe36-b169-4425-b86b-4e996ceb0245
Date: 2026-09-29

## 1. The prompt

From the cats-todo backlog — next-list item N-027:

> `.ced/history.bytdb` is created in ANY folder ced opened a file in, not
> only git repositories (`ced ~` leaves `~/.ced/`). Option: persist only
> when the root holds `.git` (or already has `.ced/`), at the cost of no
> history elsewhere. Owner's call.

The owner dropping the item in was taken as the call: implement the gate.

## 2. What already existed

- `history.Load` never creates anything; `History.Write` creates `.ced/`,
  the database and `.ced/.gitignore` on the first write with something
  pending (`internal/history/db.go`).
- The App writes through `writeHistory` (recentlocations.go) — on Close
  and wherever else it flushes — via the `historyPathFn` seam, which
  `newTestApp` points at a temp file.
- `.ced/` is also where `format.json` lives (`format.ConfigDir`).

## 3. The gate — `history.Persists(root)`

```
root inside a git work tree (.git at root or any ancestor) → persist
root already holding a .ced/ DIRECTORY                     → persist
anything else                                              → memory only
```

Decisions:

- **Walks up for `.git`** rather than the item's literal "root holds
  `.git`": opening `ced internal/` in a repo is still working in that
  repo; the `.ced/` it gets is self-gitignored, so harmless.
- **`.git` file counts** (worktrees, submodules).
- **Existing `.ced/` counts** — a project that already has format.json or
  earlier history has opted into the directory. A stray `.ced` FILE does
  not.
- **Filesystem walk, not `git rev-parse`** — it runs on Close; spawning
  git there would make quitting wait on a process.
- **Asked at write time**, not at load, so a mid-session `git init`
  persists the session's pending history.
- Not a repository → `writeHistory` returns early; the in-memory history
  still serves the session (Recent locations, recent files, search
  history) and is dropped with the App.

## 4. Where it sits

- `internal/history/db.go`: `Persists` + a header paragraph ("AND ONLY IN
  A REPOSITORY") next to "Loading never CREATES anything". `Write` itself
  is unchanged — the gate is the caller's, so the history package's own
  tests keep writing into bare temp dirs.
- `internal/app/recentlocations.go`: `historyPersistsFn = history.Persists`
  seam; `writeHistory` checks it.
- `internal/app/app_test.go`: `newTestApp` pins `historyPersistsFn` to
  true (temp roots have no `.git`; the path seam already keeps the DB out
  of the root).

## 5. Tests

- `TestPersists_OnlyRepositoriesAndExistingCed` — bare / .git dir / .git
  file / repo subfolder / existing .ced/.
- `TestPersists_CedFileIsNotADirectory`.
- `TestWriteHistory_OnlyInARepository` — real gate restored: a bare root
  writes nothing and grows no `.ced/`, the session history survives in
  memory, then `mkdir .git` and the same pending history reaches the DB.
- The non-repo halves `t.Skip` if TMPDIR itself sits in a git work tree
  (unsatisfiable environment, not flakiness).
- `make test` (race) green across all packages.

## 6. Docs

- CLAUDE.md → Recent locations bullet names the gate and the seam.
- README → the search-history sentence notes history is written only for
  git repositories (or an existing `.ced/`).
- next-list: N-027 moved to Closed.

## Next

Closed: N-027. Declined: None. Raised: None.
Deferred: None. Promoted: None.
Updated: None. Full list: `ai_docs/todo/next-list.md`.
