# 2026-10-17 20:29 — Finder: favor contiguous over fuzzy matches

Session: `f0a02b7b-e70d-439f-9459-8a47191e3412`

## Requests

From the cats-todo backlog (with a screenshot):

1. "Favor a contiguous search rather than a fuzzy search." In the
   folder-scoped finder, typing `pobc_kahua_canada/prod.json` highlighted
   the `po` of `custom_dm_re**po**rts/` and then the rest — the greedy
   fuzzy walk grabbed the first stray letters it met.
2. (follow-up) Hide scattered matches entirely when a contiguous one
   exists.

## What was built

### Contiguous tier in the scorer — `internal/finder/score.go` (commit `8db341c`)

- `Score` first tries a case-insensitive substring (`bestContiguous`):
  every occurrence is scored with the existing heuristics and the best
  kept (earliest on a tie), so "app" highlights the basename of
  `internal/app/app.go`, not the directory.
- A contiguous hit is lifted by `contiguousFloor(n) = n*maxRuneBonus + 1`
  — one above the ceiling of ANY scattered score for an n-rune query.
  A fixed bonus could not separate the tiers: scattered letters on word
  boundaries can out-earn a mid-word substring per rune.
- The greedy walk remains the fallback (abbreviations like `apgo`).
- Per-rune heuristics factored into `scoreAlignment`, shared by both
  passes.
- Position 0 now counts as a word boundary. Without it, root-level
  `tab.go` lost to `internal/app/tabbar.go` for "tab" once both were
  contiguous (`TestFinder_SearchRanks` caught it).
- Shared scorer → the palette and LSP completion rank contiguous-first
  too.

### Hide scattered when contiguous exists — `internal/finder/finder.go`

- `rank` (Search + SearchUnder) calls `dropScattered`: if any result
  scores ≥ `contiguousFloor`, everything below is dropped, BEFORE the
  limit cut. No contiguous hit → the scattered list is kept.
- Palette/completion deliberately unchanged (raised as N-072).

## Tests

- score_test.go: substring highlighted as one run (the screenshot case),
  contiguous outranks scattered, best occurrence chosen, case-folded
  substring, floor bounds the scattered ceiling.
- finder_test.go: rank hides scattered when contiguous exists, keeps
  scattered without one, `dropScattered` on nil.
- `go test ./internal/finder/ ./internal/app/ -race` green.

## Notes

- Correction made mid-session: the finder's `1/1810` tail is
  shown/total-files-in-scope (`countLabel`), not a match count — the
  screenshot only ever had one result; the bug there was the highlight.

## Next

Closed: None. Declined: None. Raised: N-072. Deferred: None.
Promoted: None. Moved: None. Updated: None.
Full list: `ai_docs/todo/next-list.md`.
