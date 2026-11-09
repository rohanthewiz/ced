# Markdown preview: no magenta, higher contrast

Session: `7e33f58f-8bc7-4390-b795-8359fbf7376b`

## Ask

"My default theme is Cats green. I don't like the general magenta color of
the markdown preview. And can we increase the contrast there too?" (with a
screenshot of a preview table full of inline code).

## Finding: the screenshot was Darcula, not Cats green

`~/.config/ced/config.json` has `"theme": "darcula"`. A pinned theme beats
the host palette (`catsThemeArrived` returns when `themePinned` and the
name isn't `cats-host`), so ced ignored cats' `cats-green`. The magenta was
Darcula's `#9876aa`, which is both `accent-soft` and `syn-constant`. The
user's config was left alone; they were told how to change it (pick
"Cats (host: cats-green)" in the theme picker, or delete the `theme` line).

## What changed

All in `mdPaletteFor` and friends (internal/editor/markdown.go), so it
applies to every theme with no per-theme fork.

- **Prose takes no syntax hue.** The preview borrowed colours tuned for
  keywords and constants, which are purple in most palettes (and olive on
  a green accent, where accent-soft is a blend toward err).
  - inline code: `syn-constant` → the code slab's neutral ink (`pal.inline`
    is `pal.code`). The slab alone says "this is code".
  - h3, h2 rule, bullets: `accent-soft` → the lifted accent.
  - quote bar: raw `accent-soft` → raw accent (decoration, not lifted).
  - The heading tiers stay apart by their rules (heavy, light, none).
  - Fenced blocks keep their syntax colours: those are real syntax.
- **Higher contrast targets.** `mdBodyContrast` 9 (new: body text is now
  lifted, via `mdBodyInk`) and `mdInkContrast` 7 for every other text role
  (was 4.5). Body sits a tier above so a lifted quote or heading can't
  out-shout the prose.
- **`inkFloor`** is now `min(mdInkContrast, what the body achieves)`. The
  old Solarized carve-out (floor drops to the theme's own body contrast)
  no longer applies since the body is lifted too; the min only bites on a
  mid-tone background where even white can't reach the target.
- `pal.check` (the `x` in `[x]`) now goes through `ink` as well; it was
  the one text role left unlifted.
- CLAUDE.md's markdown rule updated to match.

## Measured in the real binary (run-ced capture)

| Text | Darcula before | Darcula after | Cats green before | Cats green after |
|---|---|---|---|---|
| Body | 6.93 | 9.44 | 11.40 | 11.40 (unchanged) |
| Inline code | 4.53 purple `#b79fc3` | 7.24 neutral `#cbd4dd` | 6.75 yellow | 9.34 neutral |
| Headings | 4.54 | 7.54 | 6.06 | 7.42 |

"Before" was the installed `~/.cats/bin/ced` 0.7.1. Cats green was a
seeded theme file holding cats' palette through the same mapping as
`catsHostSpec`, not a live cats pane. No purple left in the Darcula
capture.

## Tests

- New: `TestMDPalette_BodyTextReachesItsTierOnEveryBuiltin`,
  `TestMDPalette_ProseWearsNoSyntaxHue`, `TestInkFloor_FollowsAnUnreachableBody`
  (replaces `TestInkFloor_FollowsALowContrastTheme`, whose premise is gone).
- `TestMDPalette_TextRolesReadOnEveryBuiltin` also covers `check`.
- `TestMDPalette_ReadableColoursUntouched` now pins Tokyo Night's body
  text; its accent (~6.8:1) sits under the new floor.
- `make test` passes.

## Noticed, not changed

`pal.quote` (muted italic) is only reached at `mdMaxQuoteDepth`: ordinary
blockquotes recurse through a sub-renderer whose paragraphs use `pal.base`,
so they render as plain body text beside the bar.

## Next

Closed: None. Declined: None. Raised: None.
Deferred: None. Promoted: None. Moved: None.
Updated: N-105. Full list: `ai_docs/todo/next-list.md`.
