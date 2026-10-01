// =============================================================================
// File: internal/app/tabcontext.go
// Author: Rohan Allison <rohanthewiz@gmail.com>
// Created: 2026-09-30
// Copyright: 2026 Rohan Allison. All rights reserved.
// =============================================================================

// tabcontext.go is the right-click menu on a TAB in the tab strip. Until
// now a right-click there fell through to the full ≡ menu, which answers
// "what can the editor do?" when the gesture asked "what can I do with
// THIS file?".
//
//	 ┌ main.go × ┐┌ app.go × ┐
//	             ┌──────────────────────┐   right-click on app.go
//	             │ ▸ Reveal in file tree│
//	             │ ▸ Close tab          │
//	             │ ▸ Close other tabs   │
//	             │ ▸ Copy relative path │
//	             │ ▸ Copy absolute path │
//	             └──────────────────────┘
//
// Design choices:
//
//   - **It acts on the CLICKED tab, without switching to it first.**
//     Closing or copying the path of a background tab must not drag it
//     to the front. Reveal is the exception by nature: revealing opens
//     the file (RevealPath), because the selected tree row and the
//     active tab are meant to agree.
//   - **Rows capture the *editor.Tab, never its index.** An index goes
//     stale the moment any tab before it closes; the pointer is resolved
//     back to an index when the row runs (tabIndexOf).
//   - **Same chassis as the editor's menu** (editorContextModal): rows
//     with enabled predicates that dim rather than vanish, so the list is
//     a fixed vocabulary whose positions the hand learns.
//   - **Every row has a ≡ twin** (the macOS Terminal + tmux rule — the
//     right button is often swallowed): Reveal is ≡ Nav "Reveal file in
//     tree", Close other tabs sits under Close tab in ≡ File, and the
//     rest were already there. The twins act on the ACTIVE tab.
//   - **Close other tabs keeps unsaved tabs** and says how many. Closing
//     several dirty tabs would mean a queue of save/discard dialogs from
//     one click, and the single modal slot cannot stack them; the dirty
//     ones are exactly the tabs a sweep should not take anyway.

package app

import (
	"github.com/rohanthewiz/ced/internal/editor"
)

// tryTabContextClick opens the tab menu when (x, y) lands on a drawn tab.
// Returns true when it consumed the event. The ≡ button and the `+N`
// overflow button are not tabs and decline, so a right-click on them
// still opens the ≡ menu.
func (a *App) tryTabContextClick(x, y int) bool {
	_, sy, _ := a.tabStripRect()
	if y != sy {
		return false
	}
	for _, r := range a.lastTabRects {
		if x < r.X || x >= r.X+r.Width || r.Index < 0 || r.Index >= len(a.tabs) {
			continue
		}
		items := a.tabContextItems(a.tabs[r.Index])
		w := contextMenuWidth
		for _, it := range items {
			if lw := runeLen(it.label) + 6; lw > w { // border+chevron+padding
				w = lw
			}
		}
		w = min(w, a.width)
		// Anchored one row BELOW the strip so the menu never covers the
		// tab it describes.
		cx, cy := a.placeContextSized(x, y+1, len(items), w)
		a.openModal(&editorContextModal{x: cx, y: cy, w: w, items: items})
		return true
	}
	return false
}

// tabContextItems builds the rows for one tab.
func (a *App) tabContextItems(t *editor.Tab) []editorContextItem {
	hasPath := func(*App) bool { return t.Path != "" }
	return []editorContextItem{
		{label: "Reveal in file tree", action: func(app *App) { app.revealTabInTree(t) }, enabled: hasPath},
		{label: "Close tab", action: func(app *App) { app.requestCloseTab(app.tabIndexOf(t)) }, enabled: alwaysTrue},
		{label: "Close other tabs", action: func(app *App) { app.closeOtherTabs(t) }, enabled: (*App).hasMultipleTabs},
		{label: "Copy relative path", action: func(app *App) {
			app.copyPathToSystemClipboard(app.relativePathFor(t.Path), "relative path")
		}, enabled: hasPath},
		{label: "Copy absolute path", action: func(app *App) {
			app.copyPathToSystemClipboard(absolutePathFor(t.Path), "absolute path")
		}, enabled: hasPath},
	}
}

// tabIndexOf resolves a tab pointer to its current index, or -1 when the
// tab has since closed. Identity, not path: two untitled tabs share "".
func (a *App) tabIndexOf(t *editor.Tab) int {
	for i, tab := range a.tabs {
		if tab == t {
			return i
		}
	}
	return -1
}

// revealTabInTree shows a tab's file in the file tree: the sidebar comes
// up if hidden, its folders expand, the row is selected and scrolled to.
// RevealPath does the work (and its flashes explain a miss — a file
// outside the project, or under a hidden folder).
//
// The keyboard stays with the editor. The reveal answers "where is this
// file?"; taking focus would turn the next keystroke into a tree filter
// the user never asked for. A click on the row, or Esc T, takes it.
func (a *App) revealTabInTree(t *editor.Tab) {
	if t == nil || t.Path == "" || a.tabIndexOf(t) < 0 {
		return
	}
	a.RevealPath(t.Path)
}

// menuRevealActiveFile is the ≡ Nav / palette twin of the tab menu's
// Reveal row, for the active tab.
func (a *App) menuRevealActiveFile() {
	a.closeMenu()
	a.revealTabInTree(a.activeTabPtr())
}

// closeOtherTabs closes every tab but keep, leaving unsaved tabs open
// (see the header) and saying how many were kept. keep ends up active.
//
// Walks right to left so each close leaves the indices still to visit
// untouched.
func (a *App) closeOtherTabs(keep *editor.Tab) {
	if a.tabIndexOf(keep) < 0 {
		return
	}
	dirty := 0
	for i := len(a.tabs) - 1; i >= 0; i-- {
		t := a.tabs[i]
		if t == keep {
			continue
		}
		if t.Dirty {
			dirty++
			continue
		}
		a.closeTab(i)
	}
	a.switchToTab(a.tabIndexOf(keep))
	if dirty > 0 {
		a.flash("Kept " + plural(dirty, "unsaved tab", "unsaved tabs"))
	}
}

// menuCloseOtherTabs is the ≡ File twin of the tab menu's row, keeping
// the active tab.
func (a *App) menuCloseOtherTabs() {
	a.closeMenu()
	a.closeOtherTabs(a.activeTabPtr())
}
