# N-072: palette hides scattered matches

Session: `fce842ae-3d83-4246-9d13-433b3c5c2d57`

## Ask

From the cats-todo backlog, N-072: the file finder hides scattered
matches whenever a contiguous one exists (`dropScattered`). The command
palette and LSP completion share `finder.Score` but still list scattered
hits below. Apply the same filter there only if the scattered tail turns
out to be noise in practice.

## Measurement

A throwaway test (deleted afterwards) scored every ≡ menu label (178,
enabled or not) against everyday queries and split contiguous from
scattered hits:

| query | contiguous | scattered | scattered examples |
|-------|-----------:|----------:|--------------------|
| ref   | 1  | 20 | Recent folders…, Remote open: off, Revert file |
| git   | 7  | 16 | Manage favorites…, Toggle line comment |
| line  | 7  | 16 | Disable session restore, Run file in terminal… |
| rest  | 3  | 8  | Compare with pasted text, Reset tool layout |
| del   | 3  | 9  | Validate file, Duplicate line |
| rcf   | 0  | 11 | Recent folders…, Recent files… (abbreviation — kept) |

Verdict: in the palette, the scattered tail under a real word is noise.
Abbreviations with no contiguous hit still get the scattered list.

Completion was judged by reasoning, not measured: servers (gopls) return
camel-hump matches deliberately (`nt` → `NewTab`), and a stray contiguous
`nt` in `Count`/`printf` would hide the identifier the user meant. Left
as is.

## Changes

- `internal/finder/score.go` — new exported `IsContiguous(query, score)`:
  the tier check by score alone (`score >= contiguousFloor(n)`), so other
  `Score` callers can apply the finder's rule without Score growing a
  flag.
- `internal/finder/finder.go` — `dropScattered` uses `IsContiguous`.
- `internal/app/palette.go`:
  - `refresh` calls new `dropScatteredMatches` on a non-empty query
    before sorting (applies to every `openPicker` list).
  - `paneCount` counts contiguous hits when there are any, else all —
    an inactive pane's tab number equals what a switch would show.
  - `refresh`'s doc comment records the measurement and why completion
    is excluded.
- Tests: `TestIsContiguous_SplitsTheTiers` (finder);
  `TestPaletteRefresh_HidesScatteredWhenContiguousExists`,
  `TestPaletteRefresh_KeepsScatteredWithoutContiguous`,
  `TestPalettePanes_CountsHideScatteredLikeTheList`,
  `TestDropScatteredMatches_NoMatches` (app).

## Trade-off accepted

A two-letter acronym query can lose its target to a contiguous hit:
`gl` now shows only the "Toggle…" rows ("toggle" holds `gl`); "Show git
log" (previously ranked below them) is hidden. The file finder already
accepts the same trade.

## Verification

- `go test ./internal/finder ./internal/app` — pass (full app package
  before the new tests; finder + palette/picker/menu subset after).
- `go vet` on both packages — clean.

## Also in this commit

`next-list.md` carried another session's uncommitted edit: N-074 moved
from Open to Roadmap. Committed along with this session's changes.

## Next

Closed: N-072. Declined: None. Raised: None.
Deferred: None. Promoted: None. Moved: None.
Updated: None. Full list: `ai_docs/todo/next-list.md`.
