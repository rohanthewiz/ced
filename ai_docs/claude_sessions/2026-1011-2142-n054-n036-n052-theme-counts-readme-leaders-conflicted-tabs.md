# 2026-10-11 21:42 — Next-list run: theme counts, README leaders, conflicted tabs

Session: `9b98e626-fd19-476b-bfc8-32905ef1963e`

## Request

`/work-next-list` — work every Open/Validate item that doesn't need the
user. This doc covers the first batch (three items); the run continues.

Triage skipped as needing the user: N-005, N-006 (cats-side product
decisions), N-043 (F-key vs ⌘ chord — "decide which", no recommendation),
N-016 (contingent on N-013), and the hand checks N-010, N-026, N-038,
N-040, N-047, N-057 (real terminals, cats, Finder, tmux).

## What changed

**N-054 — theme comments** (`internal/theme/`)
- The header comments claimed "the other twenty-nine" derived keys and
  "thirty-five" in all; `derivations()` has 33 (41 total). Rather than
  update numbers that drift with every new key, reworded count-free:
  "every other key", "the full palette", "forty-odd" (theme.go,
  palette.go, builtin.go, palette_test.go, load_test.go).

**N-036 (+ N-003) — README leader table** (`README.md`,
`internal/app/leader_test.go`)
- Added the 20 top-level leaders missing from the hotkey table:
  `Esc z Z T , . b o O` backtick `~ v h H A g L S`, plus rows for the
  `Esc a` / `Esc x` / `Esc C` prefixes.
- New `TestREADME_LeaderTableListsEveryBinding`: parses README table rows
  (first cell, closing backtick required so `Esc a` isn't satisfied by
  `Esc a c`; the backtick key is written ``` `` Esc ` `` ```) and fails for
  any `leaderBindings()` key or static AI sub-binding without a row.
  Dynamic namespaces (plugins, cats) are checked at the prefix only.
  Verified red before the README change (exactly the 20 rows), green after.
- N-003 stays Open, updated: leaders are guarded now; prose sections and
  ≡ rows still aren't.

**N-052 — conflicted tabs** (`internal/app/tabbar.go`)
- An unmerged file's tab LABEL draws in `theme.Error`, the tree's rule
  (`filetree.drawNodeRow`), active or not. New helper
  `tabGitConflicted(tab)` reads `a.gitConflicted` (absolute paths, same
  as `Tab.Path`).
- Design choice: colour on the label, not the marker slot — the slot
  carries disk state (⊘ / ⚠ / ●), which is orthogonal to a merge
  conflict.
- Test: `TestDrawTabBar_ConflictedTabLabelIsError`.

## Commits

- `44ca86e` Theme comments: drop drifting key counts (N-054)
- `e3afce9` README: every leader key in the hotkey table, pinned by a test (N-036)
- `5f5ce5d` Tab bar: an unmerged file's tab label is red, like its tree row (N-052)

## Notes

- `gofmt -l internal/` flags `internal/userconfig/userconfig_test.go` —
  pre-existing, not touched here.

## Next

Closed: N-054, N-036, N-052. Declined: None. Raised: None.
Deferred: None. Promoted: None. Moved: None.
Updated: N-003. Full list: `ai_docs/todo/next-list.md`.
