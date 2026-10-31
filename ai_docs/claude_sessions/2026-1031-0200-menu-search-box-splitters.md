# ≡ menu: a search box at the top, splitters in every section

Session: `90d2a0a6-49c4-4e5c-9866-e8c9bc2ecfe4`

## Ask

1. From the cats-todo backlog (pasted): "Save me another click, can we
   put the input box for the command pallet near the top of the main menu
   pane? And the view menu could use some splitters."
2. Mid-turn: "In fact go thru all the menus and see where splitters make
   sense."
3. `/sw`

## Starting point

Typing while the ≡ menu was open already opened a picker
(`menuSearchFrom`, commit `044cfdf`), titled "Search the menu". Nothing
on screen said so: the top row was a plain "Command palette" label, so
the visible path was click ≡ → click the row → type. The palette and
that picker listed the same items (`paletteActionItems`) under two
names.

Inside a section the menu had no separators at all. View was 25 rows of
toggles in one run, and File was 39.

## What changed

### Search box (app.go, palette.go)

- The pinned palette row is now drawn as an input field
  (`menuItemDef.input`, `drawMenuSearchBox`). It shows the placeholder
  "Type to search commands…" with the terminal caret parked in it, and
  the `esc k` hint on the right. The field covers the palette's own input
  cells (`paletteInputSpan`, now shared by `paletteModal.draw`).
- The whole top zone (box, expand/collapse-all and its divider) is
  pinned: `menuPinnedRows` went from 3 to 6, so the box never scrolls
  away. Row↔screen mapping is now one pure pair, `menuRowY` /
  `menuRelYAt`, and pinned rows ignore the scroll. `menuItemIndexAt`,
  `menuEnsureHoveredVisible` (a pinned row never moves the band) and
  `drawMenu` all read it. ▲ moved to the top zone's divider.
- Typing, a click on the box, and Enter on the box all go through
  `openPaletteFromMenu(seed)`. It opens the real palette (title "Command
  palette", sourced) drawn in the MENU's frame (`paletteModal.fromMenu` →
  `menuRect`): same x, width and top row, and height = the menu's (at
  least the usual ten rows). The first typed rune lands in the cell the
  caret was in, and the frame doesn't jump. "Search the menu" is gone
  as a separate title.
- The box never wears the hover bar, even while it is the selected row.
  The first try painted the bar around the field, which showed as a stray
  blue chip behind `esc k` at ~1.5:1. The caret is now its focus mark.

### Splitters (app.go, cats_glue.go)

- `menuItemDef.splitAbove` is a flag on the first REAL row of a run, not a
  separator item, so the palette and the tests keep walking actions only.
  `menuGeometry` (the old `menuLayout` body; `menuLayout` is now a thin
  wrapper) gives each split its own row and records it in
  `menuGeom.splits` with its depth. A split on a group's first row is
  ignored.
- Each split is drawn as an inset dotted `┄` from the label column to two
  cells short of the frame. It is quieter than the solid `├──┤` section
  dividers, matching the palette's spacer.
- Ink is `menuSplitColor`: Subtle lifted with `theme.LiftToContrast` to
  `menuSplitContrast` = 1.8:1 on LineHL. Measured: Subtle alone is
  1.13–1.49:1 on the built-ins, and **1.00:1 on cool-blue** (Subtle ==
  LineHL there). Lifted, it lands at 1.80–2.37:1, below the Muted hints
  on most themes.
- 38 splits in all: File 8, Compare 1, Edit 3, View 4, Find 2, Nav 4,
  Code 3, Git 8, Copilot 3, Cats 2 (Cats is only present inside cats).
  MCP, Skills, Notes and Plugins have none (three rows or fewer).
- Five rows moved so that no split would isolate a one-row run:
  - File: "Manage favorites…" moved up under Recent folders. "Revert
    file" and the auto-save toggle moved up under Validate, which makes
    the auto-save comment ("sits under [Save]") true again.
  - View: the word-highlight toggle moved down beside inlay hints, and its
    old row pays for the split above Show terminal, so the terminal rows
    sit exactly where they did (TerminalRowsAboveTheFold unchanged). The
    Find-all dock moved up into the arrangement run with the terminal
    dock and tool windows.
  - Code: "Undo workspace edit" moved back directly under Code actions /
    Rename, where its neighbours' comment said it belonged; the Problems
    rows had come in between.
  - The user was told and offered a revert.

Full-expanded menu: 207 rows, height 213 → 249, dividers `[2, 5, 246]`.

## Tests

- New: `TestMenuRowY_PinnedZoneIgnoresScroll` (pure mapping and inverse
  round trip), `TestMenuSearchBox_StaysOnScreenWhenScrolled` (scrolled to
  the bottom, the box is still under its cells and a click opens the
  palette fromMenu with an empty query),
  `TestDrawMenu_SearchBoxPlaceholderAndCaret` (placeholder at the
  palette's input column, caret parked there),
  `TestMenuGeometry_SplitsSitInsideASection` (own row, below an item of
  the same section, never under a header, gone when folded),
  `TestDrawMenu_SplitRuleIsInsetAndDotted`,
  `TestMenuSplitColor_VisibleOnEveryBuiltin` (≥ 1.8 and below the
  labels), `TestPaletteFromMenu_KeepsTheMenusFrame` (rect == the menu's,
  typed rune in the box's cell),
  `TestPaletteFromMenu_ShortMenuGetsTheUsualRows` (a 14-row window, then
  11).
- Updated: `TestMenuTypingOpensSearch` also asserts title / sourced /
  fromMenu. `TestMenuMoveSelection_ScrollsIntoView`: wrapping down to the
  pinned box leaves the scroll alone. Pins: height 249, 36 splits,
  dividers `[2, 5, 246]`, Git fold 43 (35 rows + 8 splits), custom 252,
  the two tall-window heights 249.

Verified: `go test -race ./...` passes (25 packages). Real binary via
run-ced: menu open, File unfolded and View unfolded (text and colour
captures, rendered to PNG with `qlmanage`). Typing "the" / "git" turns
the menu into the palette in place, with the frame unmoved.

Docs: CLAUDE.md (≡ menu section: `menuGeometry` / `menuRowY`, the search
box and `openPaletteFromMenu`, the splits rule, new pins). README
Features bullet for the Action menu.

## Noticed, not done

- Cool-blue: Subtle == LineHL, so every modal's frame border (`chrome().
  border`) is invisible on that theme. This predates the session; raised.
- The field fill (BG) stands off the menu (LineHL) by only 1.06:1 on
  Tokyo Night (1.10–1.24 elsewhere). The palette's input always looked
  like this, and the box matches it deliberately. It goes in the by-eye
  check.
- Right-click menus (tree, tab, editor) have no splitters. They need a
  separator kind in the `ctxScroll` chassis; raised.

## Next

Closed: None. Declined: None. Raised: N-106, N-107, N-108.
Deferred: None. Promoted: None. Moved: None.
Updated: None. Full list: `ai_docs/todo/next-list.md`.
