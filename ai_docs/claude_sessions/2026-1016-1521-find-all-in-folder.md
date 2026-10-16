# 2026-10-16 15:21 — Find all in folder (tree right-click + ≡ Find)

Session: `c082b7b9-1054-4de3-b457-11eabc364c30`

## Request

From the cats-todo backlog: "Add a context menu item on the filetree of
'find all in folder'".

## What was built

- **Scoped search pipeline** (`internal/app/projectsearch.go`):
  `startProjectSearch` now delegates to `startFolderSearch(query, opts,
  dir)`; `dir == ""` (or the project root / tree root, folded by
  `searchScopeDir`) is the whole project. `scopeSearchPaths` narrows the
  finder index (root-relative) to the folder by a slash-normalized
  `rel + "/"` prefix, so `sub/` never admits `subway/`. A folder with no
  indexed files flashes "no indexed files there (ignored by the
  project?)" instead of "no occurrences". The scope rides
  `projectSearchEvent.dir`; flashes and the title use
  `projectSearchVerb` ("Find in project" / "Find in internal/app/").
  Header comment gained a doors → pipeline diagram.
- **Results list** (`internal/app/findall.go`): `findAllModal.scopeDir`
  (snapshotted like `opts`) and `scopeName` (display form). `titleText`
  names the folder; ⟳ re-runs `startFolderSearch` with the list's own
  scope.
- **Tree right-click** (`internal/app/modals.go`): folders only, right
  under New File / New Folder — "Find all in folder…"; on the root it is
  labelled "Find in project…". `ctxFindInFolder` → `promptFindInFolder`
  (same seed ranking and `history.SearchFind` kind as Find in project).
- **≡ Find twin** (`internal/app/app.go`): `labelFor` row
  "Find all in folder (sub/)" on the active folder (`findInFolderLabel`,
  front-truncated to `maxLabelSuffix`). With no subfolder active it stays
  clickable and flashes how to pick one rather than becoming a second
  Find in project.
- **README**: an "In one folder" paragraph under Find → Across the
  project.

## Tests

- `projectsearch_test.go`: `TestScopeSearchPaths_KeepsOnlyTheFolder`
  (prefix trap, root, outside root), `TestFindInFolder_SearchesOnlyThatFolder`
  (end to end with a real finder index; title; ⟳ keeps the scope),
  `TestFindInFolder_RootIsAProjectSearch`, `TestFindInFolder_IgnoredFolderSaysSo`,
  `TestTreeContext_FindAllInFolderOnFoldersOnly`,
  `TestMenuFindInFolder_NamesTheActiveFolderOrExplains`,
  `TestCtxFindInFolder_IgnoresFiles`.
- `modals_test.go`: folder and root tree-menu label lists updated.
- Menu pins bumped for the new ≡ row: 177 group actions, 194 rows, height
  200, dividers `[2, 5, 197]`, 203 with custom actions, tall-window
  heights 200 in `TestMenuModalRect_*`. CLAUDE.md's pin note updated.

`make test` (race) passes.

## Next

Closed: None. Declined: None. Raised: None.
Deferred: None. Promoted: None. Moved: None.
Updated: None. Full list: `ai_docs/todo/next-list.md`.
