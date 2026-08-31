# Session: brace matching, and the bug it fell over on the way in

- Date: 2026-08-31
- Branch: `main`
- Repo: ced (`~/projs/go/ced`)
- Session id: `01HQWSnqh1Bfsuhws5XYHLud`
- Predecessor: `2026-0828-1713-chat-archive.md`

## What was asked

> need brace matching

Three words. Two scope questions worth asking before writing anything —
how far it goes, and whether it understands strings — both answered
toward the fuller version:

- **Highlight + jump.** A decoration source boxing the pair, plus `Esc %`
  and a ≡ Code row.
- **String-aware, by reading the syntax grid.**

That second answer is the one that turned the session.

## What shipped

### `internal/editor/bracket.go` — the matcher and the paint

```go
type BracketPair struct {
    At         Position  // the bracket the caret is on, or just after
    Rune       rune
    Partner    Position
    Matched    bool      // a partner was found
    Conclusive bool      // the scan reached the buffer's edge, not its budget
}

func (t *Tab) MatchingBracket(th theme.Theme) (BracketPair, bool)
```

`bracketSource{}` sits among the ambient built-ins, registered between
`wordHighlightSource` and `selectionSource`, so precedence is now:

```
syntax < external annotations < word highlight < bracket pair < selection < find
```

Matched paints both cells with a bold `bracket-match` fill; a *provably*
unmatched bracket gets the `err` color as a **foreground**; a scan that
merely ran out of budget paints nothing at all.

### `internal/app/bracket.go` — the verb

`menuGoToMatchingBracket` — 66 lines, and most of them are the three
refusals:

| state | flash |
|---|---|
| caret not on a bracket | `Put the cursor on a bracket first` |
| unmatched, conclusive | `No match for (` |
| budget exhausted | `No match for ( within range` |

Landing goes through `goToLine`, so an off-screen partner is *centered*
rather than parked on the last row.

### Two theme keys

```go
{"bracket-match",     mix(bg, fg, 0.42)}   // neutral, bold on the span
{"bracket-unmatched", p["err"]}            // foreground, not a fill
```

Eight core keys now derive twenty-nine.

## The design arguments worth keeping

- **Always on.** No config key, no ≡ toggle — the overflow-markers rule.
  The word highlight has a toggle because it is a *search* the caret
  keeps re-running; this is a fact about the two characters under it,
  and there is nothing a preference could usefully say no to.

- **Loudness is the inverse of cell count.** `word-highlight` is a 26%
  neutral wash because it can cover dozens of cells; `bracket-match` is
  the same neutral idea at 42% plus bold because it covers exactly two,
  and finding the second one from across the screen is the whole
  feature. Neutral for the word highlight's own reason — the blue fill
  stays the selection's alone.

- **`Matched` and `Conclusive` are two facts, deliberately.** The scan
  cannot be window-scoped the way the word highlighter is (a function's
  closing brace is usually off screen, and the jump has to reach it), so
  `bracketScanLines = 2000` bounds it instead. Hitting that bound must
  never be reported as "unmatched" — that would tint a perfectly
  balanced brace red purely for living in a big file.

- **Only `()[]{}`.** `<` and `>` are comparisons far more often than
  pairs; matching them would light up two unrelated inequalities on most
  lines of code. Quotes too — the grid already colors a string end to
  end, which says it better than two boxed cells would.

- **ON beats BEHIND, and that is what makes the jump round-trip.** The
  caret lands *on* the partner, so a second `%` comes straight back.
  Hence `repeat: true` on the binding.

- **The grid read is the LAST FRAME's**, not a fresh lex. Forcing an
  O(file) Chroma pass out of a keystroke handler is exactly what
  syntax.go's settle policy exists to stop — and it is the more honest
  answer besides, since the frame the user is looking at is where their
  own sense of "that's inside a string" came from.

## The bug this fell over

The string classifier reads `Tab.Styles` and compares the per-rune
foreground against `th.SynString` / `th.SynComment`. It never fired. The
grid was painting string content `#FF9E64` — the *constant* color.

Chroma numbers its token types so `Category()` is the **thousand** block:

```
Literal              val=3000  cat=3000  sub=3000
LiteralString        val=3100  cat=3000  sub=3100
LiteralStringDouble  val=3108  cat=3000  sub=3100
LiteralNumber        val=3200  cat=3000  sub=3200
```

So in `styleForToken`, sitting beside the other `Category()` arms:

```go
case chroma.LiteralString:  // 3100 — Category() is 3000. UNREACHABLE.
case chroma.LiteralNumber:  // 3200 — same. UNREACHABLE.
case chroma.Literal:        // catches both. → SynConstant
```

**Every string and every number in ced has been painted the constant
color.** Both arms were dead from the day they were written. Fixed by
asking `SubCategory()` for the Literal family — the one place the
category is too coarse — and pinned by
`TestStyleForToken_LiteralFamilySplitsStringsFromNumbers` with sentinel
colors, since the shipped palette gives numbers and constants the same
orange and so could not have told the test whether the fix worked.

Visible consequence: strings are green now, not orange, in every theme.

## Verification

PTY capture against the real binary, `-out` HTML parsed cell by cell.

Caret on `func main() {` at 5:13 —

```
5  func main() {          ← bg #6c726c, bold
6      fmt.Printf("{%d} )", 42)
7      if true {
9      }
10 }                      ← bg #6c726c, bold
```

Caret on `Printf(` at 6:12 — pairs with the trailing `)` at col 64; the
`)` at col 58 *inside* the literal stays plain string-green, along with
the `{` and `}` at 53 and 56. That is the whole string-awareness
argument, photographed.

An unclosed `(` renders `#e57373` + bold.

`gofmt -l`, `go vet ./...`, `make test` (race) all clean.

## Files

```
internal/editor/bracket.go        NEW — matcher + source (343 lines)
internal/editor/bracket_test.go   NEW — 12 tests (358 lines)
internal/app/bracket.go           NEW — the verb (66 lines)
internal/app/bracket_test.go      NEW — 6 tests (170 lines)
internal/editor/decoration.go     bracketSource registered; precedence comment
internal/editor/highlight.go      the Literal-family fix
internal/editor/highlight_test.go 2 regression tests
internal/theme/theme.go           BracketMatch / BracketUnmatched + Default()
internal/theme/palette.go         two derivations + ToTheme
internal/app/app.go               ≡ Code row
internal/app/leader.go            Esc-%
internal/app/app_test.go          menu pins 145 / 151 / [2, 5, 148] / 154
CLAUDE.md                         map entries + a house-rules section
```

Net: **+1181 / −33**.

## Notes for next time

- **The menu geometry pins moved to 145 rows / height 151 / dividers
  `[2, 5, 148]`** (custom-actions height 154). Anything adding a ≡ row
  updates those four numbers.
- **A test that opens a file is not a test that has a syntax grid.**
  `Tab.Styles` is populated by `Render`, so `seedBracketApp` calls
  `a.draw()` once. Without it the string test exercised the naive path
  and passed for the wrong reason on the editor side, then failed on the
  app side — which is how the gap got noticed.
- **Sentinel colors, not the shipped palette, when a test is about which
  color was chosen.** tokyo-night paints numbers and constants the same
  orange; the first cut of the highlight regression test would have gone
  green against the bug.
- **`cd` inside a `&&` chain persists across Bash tool calls.** Cost a
  couple of round trips with "no such file or directory" on paths that
  were fine. Absolute paths, or `cd` back.
- Chroma's `SubCategory()` exists precisely for the Literal family — it
  is worth checking whether anything else in `styleForToken` is relying
  on a category that is coarser than it looks. `Name` is handled by exact
  token type already, so it is fine; the rest are their own thousand
  blocks.
- The matcher deliberately does **not** highlight quotes or angle
  brackets. If either is ever wanted, the argument against is written
  down in both the file header and the CLAUDE.md section — start there.
