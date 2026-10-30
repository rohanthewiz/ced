# Markdown preview: text lifted to readable contrast

Session: `90816c18-20ff-4aa8-97ad-a1cd1ecb6f38`

## Ask

1. "The contrast on this markdown preview is not great", with a
   screenshot of a preview on Darcula (inline code spans, h3/bullets).
2. `/sw`

## What was wrong

Measured every preview style in every built-in (WCAG ratio, against the
surface each style actually sits on):

- **Inline code** is `syn-constant` on `md-code-bg`. On Darcula that is
  `#9876aa` on `#3d3d3d`: **2.85:1**. Also under 4.5 on gruvbox-dark (4.10),
  solarized-dark (2.75), cool-blue (3.91), solarized-light (3.42),
  corporate (2.90).
- **h3 / bullets** (`accent-soft`): Darcula 3.71, solarized-dark 3.43.
- **Quotes / front matter / image alt** (`muted`): under 4.5 in eight of
  ten themes (cool-blue 2.43, tokyo-night 2.76).
- Headings/links (`accent`) under 4.5 on the two Solarizeds.

The preview borrows colours the theme tuned for other jobs (a syntax
class, the quiet grey, the keyword tint), and nothing promised those
would read as prose, least of all on the code slab.

## What changed

- **`internal/theme/contrast.go`** (new): `RelLuminance`, `ContrastRatio`,
  `LiftToContrast(fg, bg, target)` and `MinTextContrast = 4.5`. This is
  app's existing blame/conflict lift, moved so the editor package can
  reach it, with the target made a parameter. `liftToContrast`,
  `relLuminance` and `contrastRatio` in app/gitblame.go stay as thin
  delegates (fixed 4.5), so every existing caller behaves identically.
- **`mdPaletteFor`** (editor/markdown.go): every TEXT role goes through
  `ink`, a lift to `inkFloor` against its own background, hue kept.
  Already-readable colours are untouched (Tokyo Night headings keep the
  exact accent). Heading rules and bullets wear their tier's LIFTED colour
  so an underline never drifts a shade from its word. Body text and pure
  decoration (hr, table rules, the quote bar) are not lifted.
- **`inkFloor`**: 4.5, or the theme's own body-text contrast when LOWER.
  Solarized Light's fg is 4.13 on purpose; aiming headings and quotes
  past the prose would invert the hierarchy. The lift moves in tenth
  steps, so it can overshoot by up to one step (Solarized Light quotes
  land ~4.7). Documented in the comment rather than solving exactly, so
  one lift stays shared with blame, conflicts and diff emphasis.
- **`slabInk`** (fenced code, markdownblocks.go `codeRows`): syntax colours
  are lifted only to their PAGE contrast, capped at the floor. The slab is
  ced's addition, so ced returns what it took and no more. Lifting all
  tokens to 4.5 would put Darcula's `#808080` comments (3.6:1 on the page)
  level with the code and flatten the theme's comment/code split. Cached
  per block by raw colour.

Results (before → after): Darcula inline 2.85 → 4.53 (`#9876aa` →
`#b79fc3`), h3 3.71 → 5.09, quotes 3.59 → 4.97; solarized-dark inline
2.75 → 5.00; corporate inline 2.90 → 5.18; tokyo-night quotes 2.76 →
5.36. Values already ≥ 4.5 unchanged.

A before/after page with the run-ced captures and the full table was
published as a private artifact during the session.

## Tests

- `theme/contrast_test.go`: WCAG endpoints and symmetry; non-RGB counts
  as black; the Darcula purple reaches 4.5 and stays purple; a light
  surface deepens rather than brightens; readable / default-bg / non-RGB
  inputs come back unchanged; a lower target lifts less.
- `TestMDPalette_TextRolesReadOnEveryBuiltin`: every text role in every
  built-in reaches `inkFloor`. Fails without the fix (Darcula inline
  2.85, etc.).
- `TestMDPalette_ReadableColoursUntouched`: Tokyo Night h1 keeps its exact
  accent.
- `TestInkFloor_FollowsALowContrastTheme`: Solarized Light floor = its
  body contrast; Tokyo Night floor = 4.5.
- `TestRenderMarkdown_FencedTokensGetBackWhatTheSlabTook`: on Darcula a
  fenced comment reads on the slab at least as well as on the page, yet
  below the code beside it. Fails without `slabInk` (2.75 vs 3.59).

Verified: `make test` (race) passes. Real binary via run-ced
(`-theme darcula`, a doc with inline code, a table, a quote and a Go
fence, Esc-v): inline code renders `#b79fc3` on `#3d3d3d`, was `#9876aa`.

CLAUDE.md: the Markdown preview section states the lift rule, and the
architecture map names `theme/contrast.go`.

## Next

Closed: None. Declined: None. Raised: N-105.
Deferred: None. Promoted: None. Moved: None.
Updated: None. Full list: `ai_docs/todo/next-list.md`.
