# Session: tab right-click menu with "Reveal in file tree"

Session ID: d935bafc-2250-498f-b3e8-b031227330f4
Date: 2026-09-30

Second half of the session that produced
`2026-0930-1910-diagnostic-caret-note`.

## 1. The ask

"I need some means of manually navigating the file tree to the file I am
currently on." Refined mid-turn: "When I right click on a file tab, I just
get the main editor menu. A context menu would be great here and one item
could be … 'reveal in file tree'."

Right-click on the tab strip fell through every `try*ContextClick` to
`openMenu()`. `RevealPath` (favorites.go) and `filetree.Reveal` already
existed (for `ced fav`, recent locations) but had no door for the active
file.

## 2. What landed

- **`internal/app/tabcontext.go`** (new): `tryTabContextClick` claims a
  right-click on a DRAWN tab (`lastTabRects`, strip row only; ≡ and `+N`
  fall through to ≡). Reuses `editorContextModal`, anchored one row below
  the strip. Rows act on the CLICKED tab without switching to it; closures
  capture `*editor.Tab`, resolved by `tabIndexOf` at run time.
  - Reveal in file tree → `revealTabInTree` → `RevealPath` (opens the
    file, shows the sidebar, expands ancestors, selects + scrolls,
    flashes "Revealed …"). Keyboard stays in the editor on purpose — tree
    focus would turn the next keystroke into a tree filter.
  - Close tab → `requestCloseTab(tabIndexOf(t))`.
  - Close other tabs → `closeOtherTabs`: right-to-left, KEEPS dirty tabs
    and flashes "Kept N unsaved tab(s)" (the single modal slot can't
    stack save dialogs), kept tab made active via `switchToTab`.
  - Copy relative / absolute path.
- **≡ twins** (right-click is never the only path): Nav "Reveal file in
  tree" (`menuRevealActiveFile`), File "Close other tabs"
  (`menuCloseOtherTabs`). No leader (flat table full).
- **Bug fix in `closeTab`** (app.go): closing a tab LEFT of the active one
  didn't decrement `activeTab`, so the view jumped to the active tab's
  right neighbour (reachable before via a background tab's `×`).
  `TestCloseTab_LeftOfActiveKeepsActive` fails without the fix (checked).
- `app.go` mouse dispatch: `tryTabContextClick` before
  `tryEditorContextClick`.

## 3. Tests

- New `tabcontext_test.go`: menu opens on a tab (rows pinned, no switch),
  misses fall through, Close on a background tab keeps the view, the
  closeTab fix, Reveal selects + expands + shows sidebar + no focus, ≡
  twin, close-others keeps dirty + flash, predicates dim on untitled /
  lone tab.
- Menu pins: 150 → 152 group actions, 167 → 169 rows, height 173 → 175,
  dividers `[2, 5, 172]`, custom-actions height 178;
  `TestHandleMenuMouse_ClicksRowAndOutside` given `a.height = 180`.
- `make test` (race) green; gofmt/vet clean.
- Real binary (`run-ced`): right-click on a background `c.json` tab opens
  the 5-row menu under it; Reveal expands `sub/` → `deep/`, selects
  `c.json`, flashes "Revealed sub/deep/c.json".

## 4. Docs

README tab bullet (right-click a tab + ≡ twins); CLAUDE.md Tabs rule,
menu pins, architecture map (`tabcontext.go`).

## Next

Closed: None. Declined: None. Raised: None.
Deferred: None. Promoted: None.
Updated: None. Full list: `ai_docs/todo/next-list.md`.
