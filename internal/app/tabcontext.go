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
//	             ┌───────────────────────────┐   right-click on app.go
//	             │ ▸ Reveal in file tree     │   look: where is it,
//	             │ ▸ Show uncommitted changes│         what changed,
//	             │ ▸ Show git history        │         how did it get here,
//	             │ ▸ Compare with clipboard  │         how does it differ
//	             │ ▸ Preview                 │   (markdown only)
//	             │ ▸ Format file             │   change / check it
//	             │ ▸ Validate file           │
//	             │ ▸ Move to split →         │   place it (inside cats
//	             │ ▸ Copy to split →         │   only)
//	             │ ▸ Add to group…           │   group it (tabgroups.go)
//	             │ ▸ Remove from group api   │   (grouped tabs only)
//	             │ ▸ Zip file                │   copy it out
//	             │ ▸ Copy relative path      │
//	             │ ▸ Copy absolute path      │
//	             │ ▸ Close tab               │   make it go away
//	             │ ▸ Close other tabs        │
//	             └───────────────────────────┘
//
// Design choices:
//
//   - **It acts on the CLICKED tab.** Whether that tab comes to the
//     front depends on where the answer appears. Rows whose answer is a
//     VIEW of the file or a write to its buffer (compare, preview,
//     format, validate) bring it forward first (onTab) — a diff or a
//     caret on a problem in a tab nobody can see is an answer shown
//     nowhere. Rows whose answer is a flash, a panel or a file on disk
//     (close, zip, copy path, git changes / history, the splits) act in
//     place, so tidying a background tab never drags it forward. Reveal
//     opens the file by nature (RevealPath): the selected tree row and
//     the active tab are meant to agree.
//   - **Every verb is an existing one.** Each row calls the same code
//     as its ≡ row, so the two doors cannot drift; the only new verbs
//     are the three that had no door at all (one file's changes, one
//     file's history, Validate) and Move to split.
//   - **Rows capture the *editor.Tab, never its index.** An index goes
//     stale the moment any tab before it closes; the pointer is resolved
//     back to an index when the row runs (tabIndexOf).
//   - **Same chassis as the editor's menu** (editorContextModal): rows
//     with enabled predicates that dim rather than vanish, so the list is
//     a fixed vocabulary whose positions the hand learns.
//   - **Every row has a ≡ twin** (the macOS Terminal + tmux rule — the
//     right button is often swallowed): Reveal is ≡ Nav "Reveal file in
//     tree"; Close other tabs and Validate file sit in ≡ File; the git
//     pair is ≡ Git "Show file's uncommitted changes" / "Show file's git
//     history"; Move to split is ≡ Cats "Move to split →" and Copy to
//     split is its "Open in split →"; the rest were already there. The
//     twins act on the ACTIVE tab.
//   - **Close other tabs keeps unsaved tabs** and says how many. Closing
//     several dirty tabs would mean a queue of save/discard dialogs from
//     one click, and the single modal slot cannot stack them; the dirty
//     ones are exactly the tabs a sweep should not take anyway.

package app

import (
	"github.com/rohanthewiz/ced/internal/cats"
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
		// A group chip has its own menu: group management, not file
		// verbs (tabgroups.go).
		if r.ChipW > 0 && x >= r.ChipX && x < r.ChipX+r.ChipW {
			a.openTabGroupMenu(r.Group, x, y)
			return true
		}
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

// tabContextItems builds the rows for one tab, grouped look → change →
// place → copy → close: questions about the file first (where is it,
// what changed, how did it get here, how does it differ), then verbs that
// rewrite or check it, then where it lives, the clipboard, and last the
// verbs that make the tab go away.
//
// Rows that need the tab IN FRONT go through onTab: their answer is a
// view of the file (a diff, a preview, a caret on a problem) or a write
// to its buffer, and doing either to a tab the user cannot see would be
// a result shown nowhere. Rows whose answer is a flash or a file on disk
// (zip, copy path, close) act in place.
func (a *App) tabContextItems(t *editor.Tab) []editorContextItem {
	hasPath := func(*App) bool { return t.Path != "" }
	isText := func(*App) bool { return t.Buffer != nil && !t.IsImage() }
	isTextFile := func(*App) bool { return t.Path != "" && !t.IsImage() }
	inRepo := func(app *App) bool { return app.gitIsRepo && t.Path != "" }
	items := []editorContextItem{
		{label: "Reveal in file tree", action: func(app *App) { app.revealTabInTree(t) }, enabled: hasPath},
		{label: "Show uncommitted changes", action: func(app *App) { app.gitPanelRevealFile(t.Path) }, enabled: inRepo},
		{label: "Show git history", action: func(app *App) { app.gitLogShowFile(t.Path) }, enabled: inRepo},
		{label: "Compare with clipboard", action: onTab(t, (*App).menuCatsCompareClipboard), enabled: isText},
	}
	// Preview only on a file that has one — the editor menu's rule
	// (contextmenu.go): a row dead on every source file is noise. Labelled
	// by what the click does, so a previewed tab offers the way back.
	if t.MarkdownCapable() {
		label := "Preview"
		if t.IsMarkdownView() {
			label = "Stop preview"
		}
		items = append(items, editorContextItem{label: label, action: onTab(t, (*App).toggleMarkdownView), enabled: alwaysTrue})
	}
	items = append(items,
		editorContextItem{label: "Format file", action: onTab(t, (*App).formatActiveFile), enabled: isTextFile},
		editorContextItem{label: "Validate file", action: onTab(t, (*App).validateFile), enabled: isTextFile},
	)
	// The splits (catssplit.go) appear only inside cats, like the ≡ Cats
	// group that holds their twins: no plain terminal can split, so the
	// rows would be dead in every ced outside it. Inside cats they dim
	// the ordinary way (Tier 1 down, an untitled tab). They act in
	// place — the file opens in ANOTHER pane, so this one's front tab is
	// beside the point; Move closes the tab once the pane exists.
	if a.cats.caps.InCats {
		canSplit := func(app *App) bool { return app.catsTier1() && catsSplitPath(t) != "" }
		items = append(items,
			editorContextItem{label: "Move to split →", action: func(app *App) {
				app.catsSplitTab(t, cats.SplitHorizontal, "→", true)
			}, enabled: canSplit},
			editorContextItem{label: "Copy to split →", action: func(app *App) {
				app.catsSplitTab(t, cats.SplitHorizontal, "→", false)
			}, enabled: canSplit},
		)
	}
	// Grouping is placement too, so it sits with the splits. "Add to
	// group…" is always there (its picker also starts new groups);
	// "Remove from group" appears only on a grouped tab, named after the
	// group, for Preview's reason: the row says what the click will do,
	// and on an ungrouped tab there is nothing it could do.
	items = append(items, editorContextItem{label: "Add to group…", action: func(app *App) { app.openAddToGroup(t) }, enabled: alwaysTrue})
	if g := a.tabGroupOf(t); g != nil {
		items = append(items, editorContextItem{label: "Remove from group " + g.name, action: func(app *App) { app.removeTabFromGroup(t) }, enabled: alwaysTrue})
	}
	items = append(items,
		editorContextItem{label: "Zip file", action: func(app *App) { app.startZip(t.Path) }, enabled: hasPath},
		editorContextItem{label: "Copy relative path", action: func(app *App) {
			app.copyPathToSystemClipboard(app.relativePathFor(t.Path), "relative path")
		}, enabled: hasPath},
		editorContextItem{label: "Copy absolute path", action: func(app *App) {
			app.copyPathToSystemClipboard(absolutePathFor(t.Path), "absolute path")
		}, enabled: hasPath},
		editorContextItem{label: "Close tab", action: func(app *App) { app.requestCloseTab(app.tabIndexOf(t)) }, enabled: alwaysTrue},
		editorContextItem{label: "Close other tabs", action: func(app *App) { app.closeOtherTabs(t) }, enabled: (*App).hasMultipleTabs},
	)
	return items
}

// onTab wraps an active-tab verb so it runs on t: switch t to the front
// (through switchToTab, the one place a switch records nav history and
// flushes auto-save), then run it. The pointer is resolved when the row
// RUNS, so a tab that closed while the menu was up is simply skipped.
func onTab(t *editor.Tab, verb func(*App)) func(*App) {
	return func(app *App) {
		idx := app.tabIndexOf(t)
		if idx < 0 {
			return
		}
		if idx != app.activeTab {
			app.switchToTab(idx)
		}
		verb(app)
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
