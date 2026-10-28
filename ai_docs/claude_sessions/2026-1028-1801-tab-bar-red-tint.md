# Tab bar: a faint red tint

Session: `4cbfdc09-8bf7-4eac-998a-9edb3d02ebf7`

## Ask

1. "Add a slight red tint to the background color of the tabs bar", with a
   screenshot of the strip on Darcula.
2. `/sw`

## What changed

The strip and its inactive tabs used to wear `SidebarBG`, the same surface
as the tree beside them and every tool-window header. Tinting `SidebarBG`
would have moved all of those too, so the strip got a colour key of its own.

- **`tabbar-bg`** (theme/palette.go): a new DERIVED key, placed right after
  `sidebar-bg` in the derivation table:
  `mix(sidebar-bg, err, tabBarTint)` with `tabBarTint = 0.08`. It uses the
  theme's own `err` rather than a fixed red, so the cast stays in each
  palette's hue family. Because it derives, every built-in, every
  hand-written eight-key theme and the cats host theme (which goes through
  `Normalize`) pick it up with no file changes. A theme may state it.
- **`Theme.TabBarBG`** (theme/theme.go) with `ToTheme` mapping it.
  `Default()` gains the Tokyo Night literal `#281e27`, so
  `TestBuiltin_TokyoNightMatchesDefault` still holds.
- **Painters** switched from `SidebarBG` to `TabBarBG`: the strip fill and
  inactive tabs (`drawTabBar`), `+N` (`drawTabOverflow`), ≡
  (`drawMenuButton`, app.go) and ✕ (`drawQuitButton`). The ✕'s
  `liftToContrast` now measures against the tinted ground, since a red-cast
  button now sits on a red-cast strip. The active tab keeps `BG`.

### Choosing 8%

Each built-in was measured at 6/8/10/12% for its channel distance from
`sidebar-bg` (the tree beside the strip) and from `bg` (the active tab and
the editor below), and for Muted's contrast on the result:

- At 8% the strip is 12–19 units off the tree in every theme and at
  least 6 off `bg` (cool-blue is the closest, and there the hues differ
  too: mauve against blue-grey). Inactive labels lose about 0.1–0.2 of
  contrast ratio.
- Past about 10% a dark strip starts to read as an error banner.
- A variant that capped the tint's luminance at the sidebar's was tried
  and dropped: on the darkest themes it squeezed the cast to 4–6 units,
  which you can't see.

On Darcula the strip is `#352929` against the tree's `#232323` and the
active tab's `#2b2b2b`. On the light themes it comes out pinkish beige
(`#edd9c8` solarized-light, `#efe5e7` corporate).

## Tests

- `TestDerive_TabBarBGIsAFaintRedCast` (palette_test.go): in every
  built-in, redder than `sidebar-bg`, 8–24 units off it, at least 6 off
  `bg`.
- `TestDerive_TabBarBGStatedValueWins`: a stated `tabbar-bg` is kept.
- `TestDrawTabBar_StripWearsTabBarBG` (tabbar_test.go): ✕, ≡, an inactive
  tab and the empty run are on `TabBarBG`, and the active tab is on `BG`.
- `TestDefault_AllColorsSet` lists `TabBarBG`. The ✕ contrast test
  measures against `TabBarBG`.

Verified: `go test -race ./...` passes. In the real binary (run-ced PTY,
`-theme darcula`, two tabs open), row 1's background runs are: tree
`#232323`, then ✕ / ≡ / inactive README.md / empty strip `#352929`, then
the active version.go tab `#2b2b2b`.

CLAUDE.md's Tabs section now says every cell on the tab row wears
`TabBarBG`, never `SidebarBG`.

## Next

Closed: None. Declined: None. Raised: N-104.
Deferred: None. Promoted: None. Moved: None.
Updated: N-065. Full list: `ai_docs/todo/next-list.md`.
