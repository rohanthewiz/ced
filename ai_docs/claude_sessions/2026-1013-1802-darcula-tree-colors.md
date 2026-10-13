# 2026-10-13 18:02 — Darcula file tree: darker panel, neutral folders

Session: `016a0bf2-007e-4c94-a5a4-b73356d215c6` (dropped from the
cats-todo backlog with a screenshot)

## Request

"Please fix my filetree colors. I believe I am on the green theme, and the
editor body is on Darcula. Obviously the filetree colors do not match."

## Diagnosis

- Pixels sampled from the screenshot showed the tree was not on a second
  theme. The whole of ced was on Darcula (`~/.config/ced/config.json` has
  `"theme": "darcula"`). The green was the cats host chrome: the tab bar
  and status line above ced.
- The tree area was `#3d3f41`, Darcula's stated `sidebar-bg` `#3c3f41`.
  The editor was `#2b2b2b`.
- Darcula was the only dark built-in whose sidebar steps much LIGHTER
  than the editor. The derivation (`shadePanel`, 18% darker) and every
  other dark theme step it darker. Solarized Dark steps up slightly, but
  that is base02 and intentional.
- Folders were derived from `accent`, and Darcula's accent `#6897bb` is
  its NUMBER color. A tree full of blue clashed with the orange, yellow
  and green code beside it. The owner confirmed this.

## What changed

- `internal/theme/builtin.go`, Darcula:
  - `sidebar-bg` changed from `#3c3f41` to `#232323` (bg darkened 18%,
    the shadePanel value). It also paints the tab strip and tool-window
    headers.
  - `folder` changed from the derived accent to `#bbbbbb`, JetBrains'
    neutral label grey.
  - The tree now has three brightness tiers: folders `#bbbbbb`, then
    files (derived, ≈`#9ba4ad`), then dotfiles (muted `#808080`). The
    ACTIVE folder still paints `Accent`, so it is now the one blue row.
  - Both choices are explained in comments in the palette.
- `internal/theme/builtin_test.go`:
  - `TestBuiltin_DarculaSidebarStepsDown` checks that the sidebar is
    darker than bg. It failed before the fix.
  - `TestBuiltin_DarculaFoldersAreNeutral` checks that folders are not
    the accent, and that folder/file/muted each differ by at least 0.05
    luminance.

## Verification

- `go test` passes for `internal/theme`, `internal/filetree` and
  `internal/app`.
- run-ced captures of the real binary on Darcula show the tree painting
  `#232323` beside the editor's `#2b2b2b`, with grey folders.
- The owner's verdict: "It's much better now!"

## Notes

- To make ced match the cats green, pick the host theme ("Cats (host:
  cats-green)") or `cats-host-custom` in the theme picker. Darcula is a
  separate choice.

## Next

Closed: None. Declined: None. Raised: None.
Deferred: None. Promoted: None. Moved: None.
Updated: None. Full list: `ai_docs/todo/next-list.md`.
