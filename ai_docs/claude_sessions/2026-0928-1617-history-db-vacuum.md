# Session: History db VACUUM (bytdb v0.19.0), release 0.3.7

Session ID: c9aeabe7-4638-4ef8-8a86-de281ae92f68
Date: 2026-09-28

## 1. The prompt

> Bytdb v0.19.0 now has a VACUUM feature if you want to use that.

bytdb v0.19.0 added `Engine.Compact` and SQL `VACUUM` (whole-file log
compaction; refuses inside a transaction block).

## 2. Why ced needed it

bytdb's storage (btypedb) is an append-only log. Its background
auto-compact fires only once the log is past 32MB (`compactMinSize`) and
100% growth, and only in a long-lived engine. ced opens
`<repo>/.ced/history.bytdb` for moments (load at startup, write on
Close), so it never compacted.

Measured with a throwaway test (deep paths, caps full):

| state                             | file size |
|-----------------------------------|-----------|
| live set at the caps (400 + 50)   | ~44 KB    |
| after 200 simulated sessions      | 563 KB    |
| after one VACUUM                  | 44 KB     |

≈ 2.6KB of dead records per session → 32MB would take ~12,000 sessions;
in practice the file just grew forever.

## 3. What shipped

- `go.mod`: bytdb v0.18.0 → v0.19.0 (btypedb stays v0.8.0).
- `internal/history/db.go`: `compactIfLarge(db, dbPath)` runs at the end
  of a successful `Write`, after the commit (VACUUM refuses inside a
  transaction). It stats the file and VACUUMs past `compactAbove`
  (package var, 256KB ≈ 6× the largest live set the caps allow → roughly
  one compaction per ~80 sessions, a sawtooth between live set and
  ceiling). Best-effort, errors ignored: the write already landed, and
  btypedb's rename swap leaves the old log intact on failure. A fixed
  ceiling rather than a growth ratio because the live set is bounded by
  the caps. Header comment gained a "COMPACTION IS OURS TO ASK FOR"
  section.
- The compaction temp file `history.bytdb.compact` is already covered by
  the `history.bytdb*` line in `.ced/.gitignore`.
- `TestWrite_CompactsPastTheCeiling`: ceiling lowered to 2× the live
  set; sizes climb, the crossing write shrinks the file back under the
  ceiling, and all 20 folders with 41 hits each survive. Verified it
  FAILS with the `compactIfLarge` call removed.
- CLAUDE.md: one sentence in the Recent locations rule.

Commits: `4c37e23` (feature), `43e7495` Release ced 0.3.7 (version.go +
cats-plugin.toml), tag `v0.3.7`, pushed to `origin main`. `make test`
green, `CGO_ENABLED=0` build OK. 0.3.7 also carries the unreleased
recent-locations work (`57bed9f`).

## Next

Closed: None. Declined: None. Raised: None.
Deferred: None. Promoted: None.
Updated: N-001 (tag range without artifacts now `v0.3.0`–`v0.3.7`).
Full list: `ai_docs/todo/next-list.md`.
