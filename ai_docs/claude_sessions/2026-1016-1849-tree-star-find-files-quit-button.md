# 2026-10-16 18:49 — Smaller tree star, Find files in folder, ✕ quit button

Session: `d2090c08-a4d9-421a-89b8-6910172713bb`

## Requests

From the cats-todo backlog, then follow-ups in the session:

1. The recent-folders star is too big — bring it down ~40%.
2. A tree context-menu action "find files in folder": search for a
   filename, or a filename with a relative path, inside a folder.
3. (follow-up) Star "a tad larger"; then a 30% cut that turned out to be
   about the OLD binary (below); the user then ran `./bin/ced` in cats and
   called the star "perfect".
4. (follow-up) A closing ✕ just left of the ≡ that quits the editor, with
   a quick confirmation popup.

## What was built

### Recent-folders star (`internal/filetree/filetree.go`)

- A terminal can't scale a glyph: the only sizes are one cell or two. The
  emoji `⭐` (U+2B50, two cells) gave way to a ONE-cell star, bold, in
  `DiagWarning` gold. `★` (U+2605) was tried first, then swapped for
  `⭑` (U+2B51) when asked for "a tad larger".
- How the glyph was chosen: candidates were rendered in bold SF Mono
  (cats' terminal font is `ui-monospace` in WebKit) with a Swift/CoreText
  script, using CoreText fallback, into a scratch PNG. `⭑` came out ~25%
  bigger than `★`, still one cell (a few pixels of overhang).
- Bonus: `⭑` is East Asian NEUTRAL width, so the ambiguous-width caveat
  `★` carried is gone.
- `recentStarWidth` is now 1; `rootStarCol` / `RecentStarAt` comments
  generalized to `recentStarWidth`. Tests re-pinned (layout cases, narrow
  panel at 11 cols, tiny panel at 3 cols);
  `TestRecentFoldersGlyph_IsTwoCellsWide` renamed
  `TestRecentFoldersGlyph_MatchesRecentStarWidth`.
- Gotcha found: the "too big" screenshot was the cats PLUGIN binary
  (`~/.config/cats/plugins/rohanthewiz.ced/bin/ced`, built from `6924dd2`,
  before any of this work), confirmed via `lsof` on the running ced
  processes. The user then ran `./bin/ced` and approved `⭑`.

### Find files in folder (`internal/finder/finder.go`, `internal/app/finder.go`)

- `Finder.SearchUnder(dir, query, limit) ([]Result, total)`: only paths
  under the root-relative folder (slash prefix + "/", so `internal/app`
  never admits `internal/application`), scored and returned RELATIVE to
  the folder; `total` = files in the folder. Search's scoring body
  extracted into `rank`, shared by both.
- `finderModal.scope` / `scopeName` / `scopeTotal`; `openFinderIn(dir)`
  (root folds to the plain finder via `searchScopeDir`; outside the root
  flashes); `rebuildFinderIfStale`. Title "Find file in internal/", the
  count tail uses the folder's total, and an empty (ignored) folder shows
  "No indexed files here (ignored by the project?)". Enter joins
  `rootDir + scope + result`.
- Tree folder row "Find files in folder…" under "Find all in folder…";
  on the root it reads "Find file in project…". ≡ Find twin
  "Find files in folder (sub/)" on the active folder (flashes the how-to
  with none). `findInFolderLabel`'s truncation moved into the shared
  `activeFolderRowLabel(base)`.
- Menu pins: 178 group actions, 195 rows, height 201, dividers
  `[2, 5, 198]`, 204 with custom actions.

### ✕ quit button (`internal/app/quitbutton.go`, new)

- `quitButtonRect` (3 cells, first in the tab bar), `menuButtonRect` now
  starts after it; `tabBarLeadW = quitButtonWidth + menuButtonWidth` is
  what the tab strip measures from (`tabStripRect`,
  `tabOverflowReserve`). Drawn in `DiagError`, bold, so it reads apart
  from the tree header's muted ✕ (hide the panel).
- Click → `requestQuitFromButton`: dirty tabs → `menuQuit`'s Save /
  Discard / Cancel directly (one question); clean → `openConfirm("Quit
  ced", "Close the editor?")`, No default, whose Yes re-enters `menuQuit`.

### Docs

README (star glyph; "In one folder" under Find file in project; the ✕),
CLAUDE.md (star rule, Find files in folder rule, tab-bar lead rule,
architecture map, menu pins).

## Verification

- `make test` (race) passed after the star + find-files work; the full
  `internal/app` suite passed again after the quit button.
- Real binary via run-ced: star drawn as one cell after the project name;
  right-click on `internal/` shows "Find files in folder…"; typing `app/t`
  lists `app/tab.go` with a `1/3` folder count; clicking the ✕ opens the
  "Quit ced / Close the editor?" dialog.

## Next

Closed: None. Declined: None. Raised: N-070.
Deferred: None. Promoted: None. Moved: None.
Updated: N-066. Full list: `ai_docs/todo/next-list.md`.
