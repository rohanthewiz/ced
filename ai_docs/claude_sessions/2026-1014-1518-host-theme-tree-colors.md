# 2026-10-14 15:18 — Host theme: darker tree panel, neutral folders

Session: `c7f20f2d-8a8c-4718-a41e-07335128ae58` (follows on from
`2026-1013-1802-darcula-tree-colors`)

## Request

"Can we make these darcula changes the default for the green theme?"

## Diagnosis

- The "green theme" is the cats HOST theme, "Cats (host: cats-green)".
  ced synthesizes it at runtime from cats' palette (`catsHostSpec`,
  `internal/app/catstheme.go`). No ced built-in is involved.
- `panel` maps to `sidebar-bg`. In cats-green the panel is `#242a25` over
  a `#1f2420` bg, a step LIGHTER: the same problem Darcula had.
- Folders were derived from the accent (`#4db380`). The host accent
  also becomes `syn-function` and `diag-info`.
- The panel is lighter than bg in seven of cats' eight dark themes:
  cats-green, Darcula, Solarized Dark, Super Warm, Cool Blue, Dark Game
  and Dark City. Tokyo Night steps down, and so do both light themes
  (Solarized Light, Corporate).
- The owner chose scope "every host theme" over "only cats-green" or
  "only my custom file".

## What changed

- `internal/app/catstheme.go`: new `catsHostTreeColors(p)`, called from
  `catsHostSpec` after the core-key check.
  - A host panel that is not darker than bg is dropped, so `shadePanel`
    derives the sidebar 18% darker. cats-green's sidebar becomes
    `#191e1a`.
  - `folder` is set to `fg` (cats-green `#d6ddd6`). The tree tiers are
    folders (fg), then files (derived, ≈`#c2cdc4`), then dotfiles (muted).
    The active folder keeps the accent.
  - Both keys are stated in the sparse palette, so "Customize theme…"
    sees them.
  - The file-header diagram and the `catsHostPalette` comment are
    updated.
  - Folder/file luminance gaps were checked for all ten cats palettes:
    each is at least 0.054. On the light themes the gap runs the other
    way, since darker text reads louder there.
- `internal/app/catstheme_test.go`:
  - New `TestCatsHostSpec_TreeStepsDownWithNeutralFolders`, a table over
    cats-green, Darcula, Tokyo Night and Solarized Light, hand-copied from
    cats. It checks that the panel is dropped or kept as expected, that
    the sidebar is darker than bg, that folder = fg ≠ accent, and the
    tier gaps (≥0.05, measured as distance from the panel so light themes
    pass too).
  - `TestCatsHostSpecSynthesizesFromTheCoreEight` no longer expects
    `sidebar-bg` to cross over.
- `CLAUDE.md`, Themes section: one rule line covering host theme panel
  and folders.

## Verification

- `go vet ./internal/app` passes, `gofmt -l` is clean, and
  `go test ./internal/app ./internal/theme -race` passes.
- Not seen in the real binary: run-ced has no cats host, so the
  synthesized theme never appears there. Raised as N-065.

## Notes

- The owner's config pins `"theme": "darcula"`, so to see the change
  they pick "Cats (host: cats-green)" in the picker.
- `~/.config/ced/themes/cats-host-custom.json` is a frozen expanded copy
  that still holds the old sidebar `#242a25` and green folders. Delete it
  and run "Customize theme…" again to refresh it. It was not touched.

## Next

Closed: None. Declined: None. Raised: N-065.
Deferred: None. Promoted: None. Moved: None.
Updated: None. Full list: `ai_docs/todo/next-list.md`.
