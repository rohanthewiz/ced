# N-129, N-128 — a one-byte syntax class grid, and no whole-file lex on the loop

Session: `bf5fffcd-9ebf-4213-a664-3cfa9fde36c5`

## Ask

Third batch of the `/work-next-list` run (earlier batches:
`2026-1117-2243-n147-n122-n123-n126-detached-children-lsp-writer`,
`2026-1117-2301-n124-n101-n138-post-result-tsc-lsp-fuzz`). N-129 first
because N-128's off-loop lex is far simpler over a theme-free grid.

## N-129 — `Tab.Classes [][]SynClass` (`763f90c`)

editor/highlight.go rewritten (header flipped to the maintainer with the
Cloudmanic portions line): `SynClass` (uint8, `SynPlain` = 0 … 11 classes,
one per theme syn-* key), `SynPalette` + `NewSynPalette(theme, base)`,
`EditorBase`, `HighlightClasses(filename, src)` (theme-free),
`classForToken` (the old styleForToken mapping, Literal sub-category arm
kept), `ResolveClasses`. `Highlight` / `HighlightLang` still return
styles (markdown fences + tests) through the palette; `styleForToken`
is a thin wrapper so `TestStyleForToken_LiteralFamilySplitsStringsFromNumbers`
still pins the mapping.

- Render builds the palette once per frame and maps `classes[i]`.
- syntax.go patches class rows; bracket.go's `inStringOrComment(line,
  col)` compares classes (dropped the `th` parameter) — the old
  "syn-string == fg ⇒ give up" degradation is gone
  (`TestInStringOrComment_IsThemeIndependent` replaced that test case).
- app/theme.go `restyleTabs` only drops markdown rows; the theme test
  became `TestApplyThemeName_RepaintsCodeWithoutARelex` (same grid
  pointer after the switch, `package` cell in gruvbox's keyword ink).
- Budget: `TestHighlightClasses_GridCostsOneBytePerRune` (3× the text —
  the synthetic fixture's ~25-byte lines make the 24-byte row headers
  half the cost; the old grid was ~72×). 256KB file: ~0.54MB vs ~19MB.

Slip caught by the new gate: the first commit attempt stopped on
`test -z "$(gofmt -l .)"` (tab.go field alignment) — fixed, then
committed.

## N-128 — structural edits off the main loop (`422e571`)

(a) editor/syntax.go: `patchRowsInsert` (split the row at the caret; head
keeps its classes + the first inserted line's, middle lines get rows,
the tail keeps its classes on the last row; inserted runes take the
class to the left) and `patchRowsDelete` (join head + tail, drop the rows
between, columns clamped), both through `spliceRows` (fresh slice).
Enter, multi-line paste and multi-line delete now DEFER. Undo, line ops,
reload, multi-edits still `InvalidateStyles`.

(b) `InlineLexBytes` (32KB, var): Render lexes only at or under it.
`BeginAsyncLex` (due + too big + none in flight → path, snapshot, rev;
sets `t.lexing`) / `AdoptClasses(rev, grid)` (clears `lexing`, installs
only when rev == EditRev). App (app/syntax.go): `syntaxLexAsync` from
`syntaxAfterEvent`, goroutine → `syntaxLexedEvent` → `handleSyntaxLexed`
(adopt, then re-arm the settle timer / ask again). `Run` calls
`syntaxAfterEvent` before its first frame so a restored big file starts
lexing without waiting for an event.

Numbers: `BenchmarkEnterThenRender` 113 ms / 58MB → 0.96 ms / 0.47MB.
Budget `TestEnterThenRender_StaysOffChromaOnTheLoop` measures bytes
allocated (deterministic) for Enter+frame and for a settled frame on a
256KB buffer. Tests rewritten for the new boundary:
`TestSyntaxDefer_StructuralEditRelexesNow` keeps undo / duplicate /
comment toggle; new `TestSyntaxPatch_LineSplitsAndJoinsDefer`,
`TestSyntaxPatch_SplitKeepsTheTailsClasses`,
`TestAsyncLex_BigBufferIsLexedOffTheLoop`, app
`TestSyntaxLexAsync_BigFileLexedOffTheLoopAndAdopted` (-race ×5), and the
timer test now expects Enter to arm the settle timer.

Real binary (run-ced): a copy of the 261KB app.go, Esc-j to line 2460,
three Enters and typing; the HTML capture of that code region carries 12
distinct syntax inks.

What it leaves: a big file's unpatched edit paints the previous grid
until the async lex lands (N-149), and `MaxHighlightBytes` (512KB) no
longer has its original reasons (N-150).

## Also learned

- Docker is up and `golang:1.26.1` can `apt-get install tmux` (3.5a) — so
  N-139's harness is buildable; npm installs on the host stay refused.
- The run-ced capture driver's PTY code is Darwin-only
  (`TIOCPTYGRANT`/`TIOCPTYUNLK`); running it in a Linux container needs a
  build-tagged Linux `openPTY`.

## Next

Closed: N-129, N-128. Declined: None. Raised: N-149, N-150.
Deferred: None. Promoted: None. Moved: None. Updated: None.
Full list: `ai_docs/todo/next-list.md`.
