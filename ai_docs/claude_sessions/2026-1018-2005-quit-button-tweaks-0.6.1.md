# Quit button tweaks + ced 0.6.1

Session: `932bc4ac-e617-4902-8540-9e0cd88e7374`

## Ask

From the cats-todo backlog:

- Desaturate the red of the editor close button.
- On the confirmation dialog make the OK the default, not the cancel.

Then: cut a patch release, and wrap the session.

## What was built

All in `internal/app/quitbutton.go` (+ its test file).

- **Desaturated ✕.** `drawQuitButton` now paints
  `liftToContrast(desaturateColor(DiagError, quitButtonDesat), SidebarBG)`.
  `desaturateColor` (new) blends a colour toward the grey of its own
  Rec. 601 luma via the existing `blendColor`, so the lightness and the
  contrast stay about where the theme put them and only the chroma drops.
  `quitButtonDesat = 0.5` is the one knob. `liftToContrast` is the
  backstop. Every built-in theme clears 4.5:1:

  | theme | DiagError | ✕ now | contrast |
  |---|---|---|---|
  | tokyo-night | #f7768e | #cc8b97 | 6.60 |
  | darcula | #ff6b68 | #cc8180 | 5.25 |
  | gruvbox-dark | #fb4934 | #c37369 | 4.67 |
  | solarized-dark | #dc322f | #c79392 | 4.96 (lifted) |
  | cool-blue | #bf616a | #b28d90 | 4.74 |
  | super-warm | #ff6f5e | #cc847b | 5.99 |
  | dark-game | #ff3d71 | #bf5d77 | 4.72 |
  | dark-city | #d9737a | #b68386 | 5.59 |
  | solarized-light | #dc322f | #a14b4a | 4.73 |
  | corporate | #c53030 | #924747 | 5.98 |

- **Yes is the default on the quit confirm.** `requestQuitFromButton`
  sets `hover = 1` on the modal `openConfirm` returns. The scope is
  deliberate: `openConfirm`'s shared No default stays for Delete, Discard
  and the rest (pinned by `TestOpenConfirm_DefaultStaysNo`). The dirty-tabs
  path (Save / Discard / Cancel) has no "OK" and still defaults to Cancel.
  Offered to the user as a separate choice, not changed.

Tests: `TestQuitButton_CleanTabsConfirmFirst` now expects Yes. New:
`TestQuitButton_EnterConfirmsQuit`, `TestOpenConfirm_DefaultStaysNo`,
`TestDrawQuitButton_RedIsDesaturated` (lower chroma, still red-cast,
≥4.5:1) and `TestDesaturateColor`. It allows ±1 at t=1 because
`blendColor`'s `+0.5` rounds a downward step toward the source.

CLAUDE.md's "Tab bar lead" rule was updated for both changes.

## Release

`v0.6.1`: `version.go` and `cats-plugin.toml` bumped from 0.6.0,
`make test` green, `Release ced 0.6.1` on main, annotated tag pushed.

## Notes

- `gofmt -l internal/app` lists `buttontip.go`. That predates this session
  and was left alone.

## Next

Closed: None. Declined: None. Raised: None.
Deferred: None. Promoted: None. Moved: None.
Updated: N-070. Full list: `ai_docs/todo/next-list.md`.
