# 2026-10-15 16:40 — File tree: ★ after the project name opens Recent folders

Session: `efcf8b15-cac4-4c67-9fa2-25f070bfa3b5`

## Request

From the cats-todo backlog: "Next to the repo name in the file tree
Explorer give me a star icon which when clicked will popup the list of
recent folders". Then: wrap the session and do a minor release.

## What was built

- `internal/filetree/filetree.go`
  - `RecentFoldersGlyph` (`★`). Render draws it one space after the
    project name on the root row, in Muted bold. The root name is
    usually Accent, and a star in the same colour would read as part of
    the name. Muted already clears SidebarBG: the EXPLORER label uses it.
  - `rootStarCol(name, w)`: where the star goes. Normally one space past
    the name. On a narrow panel it pins to the last column and the NAME
    is truncated in front of it, so the button never disappears. Under 4
    columns there is no star.
  - Render stamps `recentStarCol`, and `RecentStarAt(localX, localY)`
    reads it. This is the same draw-then-hit-test handshake as
    `t.visible`, and `HideLabel` is respected. Column 0 is the "absent"
    value because the star never lands in the gutter.
  - `ContentWidth` measures the project row with the star (+2), so
    auto-fit never sizes the panel onto it.
- `internal/app/app.go`: `sidebarClick` checks `RecentStarAt` BEFORE
  `HitTest` (which answers root for the whole row and would reset the
  active folder), then calls `menuRecentFolders`. That is the same verb
  as ≡ File "Recent folders…", so an empty list flashes its reason.
- README (session/folders paragraph) and CLAUDE.md (File tree section)
  mention the star.

## Tests

- filetree: `TestRootStarCol_Layout`,
  `TestRender_DrawsRecentStarAfterProjectName`,
  `TestRender_RecentStarSurvivesANarrowPanel`,
  `TestRecentStarAt_NothingBeforeRender`,
  `TestRender_NoRecentStarOnATinyPanel`. Updated
  `TestContentWidth_MeasuresHeaderBlock` and
  `TestHideLabel_ShiftsTheWholeRowMap` for the new row text.
- app: `TestSidebarClick_RecentStarOpensRecentFolders` (it finds the star
  on the simulated screen, clicks it, and checks that the picker opened
  and the active folder was untouched) and
  `TestSidebarClick_RecentStarExplainsAnEmptyList`.

## Verification

- `make test` (race) passes across all packages.
- In the real binary via run-ced, with a seeded `state.json`, the tree's
  first row reads ` ced ★`. An SGR click at (6,2) opened "Recent folders"
  listing the two other seeded folders, with the current one excluded.

## Release

Minor release requested: 0.4.4 → 0.5.0 (version.go + cats-plugin.toml,
tag `v0.5.0`).

## Next

Closed: None. Declined: None. Raised: N-066.
Deferred: None. Promoted: None. Moved: None.
Updated: None. Full list: `ai_docs/todo/next-list.md`.
