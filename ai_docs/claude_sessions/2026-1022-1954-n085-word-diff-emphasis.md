# N-085: changed-word emphasis in side-by-side diffs

Session: `45e45c33-f52c-4fad-a1f8-bbcf4615a1d4`

## Ask

From the cats-todo backlog, next-list item **N-085**: side-by-side diffs
paired removed/added lines positionally with no intra-line emphasis, so a
one-word change in a long line read as two whole lines. Add word-level
highlighting of the differing span on both sides. Then `/sw`.

## What shipped

### `diff.Words` — internal/diff/words.go (new)

```
old line ─tokenize─▶ [return][ ][fmt][.][Errorf][(]…[open]…   ─┐
new line ─tokenize─▶ [return][ ][fmt][.][Errorf][(]…[read]…   ─┴▶ DiffWith (patience) ─▶ spans per side
```

- NOT a second differ: the token lists go through the package's own
  `DiffWith` (same algorithm, LCS budget, whitespace option). That answers
  the first cut's objection ("a cleverer pairing would be a second differ
  to get wrong").
- Tokens: word runs (letters/digits/`_`), whitespace runs, every other
  rune alone — punctuation anchors (`a.b(c)` vs `a.d(c)` → `b`/`d`).
- Dissimilar pairs get NOTHING: shared non-blank runes < half the longer
  line's → nil (positional pairing often zips unrelated lines).
- Spans separated only by whitespace merge (`onlySpaceBefore` walks back
  from the token, cost = the gap).
- Under `IgnoreWhitespace`, a whitespace token is never a span on its own.
- Lines over 2000 runes are not looked inside.

### Pane side — internal/app/diffview.go

- `diffPane.emphasisAt(lines, k, ws)`: lazy per row on first paint,
  cached in `p.emph` (parallel to rows, reset by `layout`, recomputed when
  `diffIgnoreWS` flips). `diffRow` stays comparable — tests compare rows
  with `!=`, so no slices on it.
- `diffPairEmphasis` compares the RAW body (prefix + CR dropped) so a
  tab→spaces change still shows, then `diffSpansToDisplay` maps rune spans
  through the painter's tab expansion (`hunkTipTabWidth` stops from 0).
- `drawDiffSide` gained an `emph` param; with spans it paints rune by
  rune (cells == display runes, drawAt's rule).
- `diffEmphasisStyle(bg, fg)`: fill = side colour at `diffEmphasisMix`
  (0.35) over bg, text `liftToContrast`ed to 4.5:1. 0.30 left
  solarized-dark's red fill at 1.19:1 to its bg, so it went to 0.35
  (min 1.25 across builtins). No RGB bg → reverse video.
- Split mode only; unified keeps each surface's palette. Covers compare,
  git panel, git log, cherry-pick preview (all share `drawDiffRows`). The
  change view (hunkview.go) has its own painter — not covered.

### Docs

- CLAUDE.md "Diff views": new intra-line emphasis rule.
- README "split" bullet: changed words tinted; dissimilar pairs plain.
- next-list: N-085 → Closed.

## Tests

- internal/diff/words_test.go: one word in a long line, punctuation
  anchors, pure insertion, gap merge, dissimilar → nil, equal/over-long →
  nil, whitespace counted vs ignored, tokenizer rune offsets.
- diffview_test.go: `TestDiffPairEmphasis_MapsTabsToDisplay`,
  `TestDiffPane_EmphasisOnlyOnPairs`,
  `TestDrawDiffRows_SplitEmphasisesChangedWord` (simulation screen: fill
  under the changed word, plain bg under `return`, both sides),
  `TestDiffEmphasisStyle_VisibleOnEveryBuiltin`.
- `make test` (race) green. Real binary via run-ced capture: compare of
  two Go files in split mode painted `path`/`open` on `#64403d` and
  `name`/`read` on `#395c3f`; the dissimilar `defer f.Close()` /
  `log.Println(...)` pair stayed plain.

## Next

Closed: N-085. Declined: None. Raised: None. Deferred: None.
Promoted: None. Moved: None. Updated: None.
Full list: `ai_docs/todo/next-list.md`.
