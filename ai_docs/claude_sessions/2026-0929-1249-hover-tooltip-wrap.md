# Session: Hover tooltip wraps and grows down instead of truncating

Session ID: b429bd1a-70e4-42ba-b497-b2872bce26b1
Date: 2026-09-29

## 1. The prompt

From the cats-todo backlog, with a screenshot of a gopls hover whose
signature and doc lines were cut with `…` at the box's right edge:

> Go info dialog text is being cut off. I'm thinking grow the dialog
> vertically instead.

## 2. Where the cut happened

Every tooltip box shares two helpers in `internal/app/hovermodal.go`:
`tooltipSize` (widest line, capped at `hoverModalMaxWidth` = 66, one row
per line) and `drawTooltipBox` (truncates each line past `mw-4` with
`…`). Four surfaces use them: the Esc-i hover modal (+ signature help),
the mouse-dwell hover tooltip, the diagnostic tooltip, and the overflow
marker popup. Fixing the shared layout fixes all four.

## 3. The change

### Wrap, don't truncate (hovermodal.go)
- `tooltipLayout(lines, emph, textW)` — ONE layout shared by measuring
  (`tooltipSize`) and painting (`drawTooltipBox`), so the box is exactly
  as tall as what lands in it.
- `wrapTooltipLine` — greedy at spaces (break space dropped, space runs
  collapsed), hard-break when no space is in reach, continuation rows
  hang at the line's own leading indentation (dropped if > half the
  row; never a row of blanks).
- Emphasis (signature help's active parameter) is rune offsets into the
  ORIGINAL line; each run is clipped to every segment it overlaps and
  re-based onto that row, so a run split by a wrap paints on both rows.
  Signature help already pre-wraps to `hoverModalTextWidth`, so this only
  matters in windows narrower than the cap.
- Width is still capped at 66 — the box grows DOWN, per the ask.

### Height limits (tooltipPlace)
- Fits below → below; fits above → flip above (unchanged).
- Fits neither → take the roomier side and SHORTEN to it (min 3 rows);
  the anchor row is never covered. The returned `hh` is authoritative —
  all callers already paint/hit-test with it.
- `drawTooltipBox` cuts rows that don't fit with a final `…` row and
  drops emphasis aimed at the cut rows.
- Hover modal's centered fallback (caret off-screen) caps at the window.

### Prose reflow (lsp.go)
Running the real binary with gopls showed wrapping alone left ragged
orphan rows: gopls sends doc comments with the SOURCE's ~70-col line
breaks, and re-wrapping each at 62 leaves "carries any" / "layout" rows.
- `hoverReflow` re-joins soft line breaks of markdown prose; keeps fenced
  code, indented code, list items, headings, quotes, tables, bare URLs,
  and markdown hard breaks (two trailing spaces / backslash).
- ced asks for PLAINTEXT hover first, and gopls' plaintext has no fence
  and no blank line between signature and doc — the first capture joined
  the signature into the doc. Guards: the text's first line (the header,
  when not fenced) never absorbs the next; a line ending in `{` `}` `;`
  (a type's declaration body) never absorbs one either.
- `hoverLines` cap is now in WRAPPED ROWS at `hoverModalTextWidth` (16),
  not source lines; a paragraph straddling the cap is cut at a row
  boundary with one row reserved for `…`.

### Docs
- `diagtip.go`: comment no longer mentions drawTooltipBox's ellipsis; it
  pre-wraps under the glyph for the hanging indent.
- CLAUDE.md: a "Hover / every tooltip box" rule under LSP verb specifics.

## 4. Tests

- `hovermodal_test.go`: `TestHoverModalDrawContent` now expects the long
  line wrapped onto 3 rows with no text lost; new
  `TestHoverModalWrapsTheReportedDocComment` (the screenshot's text),
  `TestWrapTooltipLine` (table), `TestTooltipLayout_EmphasisFollowsTheWrap`,
  `TestTooltipPlace_ShortensATooTallBox`,
  `TestDrawTooltipBox_MarksAShortenedBox`.
- `lsp_test.go`: `TestHoverLines` cap uses blank-separated paragraphs
  (row cap 16 incl. marker); new `TestHoverLines_ReflowsProseParagraphs`,
  `TestHoverLines_PlaintextKeepsTheSignatureApart`,
  `TestHoverLines_CutsAnOverlongParagraphAtARow`.
- `make test` (race) green.
- Verified in the real binary via `run-ced` at 130×44 with live gopls:
  Esc-i on `tooltipLayout` in hovermodal.go — signature wraps to two rows,
  doc paragraphs flow, box shortened above the caret ends in `…`.

## 5. Files

- internal/app/hovermodal.go, hovermodal_test.go
- internal/app/lsp.go, lsp_test.go
- internal/app/diagtip.go
- CLAUDE.md

## Next

Closed: None. Declined: None. Raised: N-030.
Deferred: None. Promoted: None.
Updated: None. Full list: `ai_docs/todo/next-list.md`.
