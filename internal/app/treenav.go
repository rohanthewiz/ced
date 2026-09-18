// =============================================================================
// File: internal/app/treenav.go
// Author: Rohan Allison
// =============================================================================

// Keyboard navigation for the file tree.
//
// The tree was mouse-only from birth — the one panel where a keyboard
// user hit a wall. This file closes the gap and makes the tree
// symmetric with the rest of the editor: mouse-first, keys as
// accelerators. Esc-T (or the ≡ View row) moves focus into the tree;
// then arrows walk the rows, →/← expand and collapse, Enter opens, and
// typing finds (treefilter.go: a pattern that lights every row in the
// current folder whose name contains it, jumping to the first, with
// Tab / Shift-Tab cycling the matches).
//
// LETTERS ARE NOT COMMANDS HERE. The tree used to bind n/N/d/r (New
// file, New folder, Delete, Rename) and A (the marks' verb list) as bare
// keys. Once typing became a search that was a trap: the first letter
// of "readme" opened a Rename prompt, and a search could never begin
// with any of five letters. Every one of those verbs already lives in
// the right-click menu and the ≡ File group (and so in the palette),
// which is where the house rule puts a file action first — so the keys
// went, and every letter now starts a search.
//
// Space and * survive as the multi-selection's keyboard half
// (treemarks.go): tick this row, and tick or clear every visible row.
// Neither is a letter a name search starts with, and both extend a
// pattern that is already running like any other rune.
//
// Focus discipline mirrors the terminal and chat panels: the branch in
// handleKey sits AFTER the Esc/leader/menu blocks, so every global
// gesture keeps working from inside the tree (Esc-s still saves,
// Esc-Esc still opens the menu). All OTHER keys are claimed while the
// tree has focus — a keystroke aimed at the tree must never leak into
// the buffer as an edit. Clicking anywhere outside the sidebar (or
// opening a file) hands focus back to the editor, the same
// click-where-you-want-to-type model the other panels follow.

package app

import (
	"path/filepath"

	"github.com/gdamore/tcell/v2"

	"github.com/rohanthewiz/ced/internal/filetree"
)

// menuFocusTree toggles keyboard focus between the tree and the editor
// — the Esc-T leader and the ≡ View row. Focusing an invisible tree
// shows it first; there is nothing to focus otherwise.
func (a *App) menuFocusTree() {
	a.closeMenu()
	if a.treeFocus {
		a.treeFocus = false
		return
	}
	if !a.sidebarShown {
		a.showTool(toolProject)
	}
	a.focusTree()
}

// focusTree gives the tree the keyboard and makes sure the cursor
// points somewhere sensible: the file being edited if its row is
// visible, else wherever the cursor already was, else the first row.
// The other panel focuses yield — one keyboard, one owner.
func (a *App) focusTree() {
	a.treeFocus = true
	a.term.focused = false
	a.chat.focused = false
	rows := a.tree.VisibleNodes()
	if len(rows) == 0 {
		a.tree.Selected = nil
		return
	}
	if a.tree.SelectedIndex(rows) >= 0 {
		return
	}
	if tab := a.activeTabPtr(); tab != nil && tab.Path != "" {
		for _, n := range rows {
			if n.Path == tab.Path {
				a.tree.Selected = n
				a.ensureTreeSelectionVisible()
				return
			}
		}
	}
	a.tree.Selected = rows[0]
	a.ensureTreeSelectionVisible()
}

// ensureTreeSelectionVisible scrolls the sidebar so the cursor row is
// on screen, using the live sidebar height (minus the two header rows,
// matching Render's layout).
func (a *App) ensureTreeSelectionVisible() {
	_, _, _, sh := a.sidebarRect()
	a.tree.EnsureSelectedVisible(sh - 2)
}

// handleTreeNavKey processes a keystroke while the tree has focus.
// Always consumes (the tree owns the keyboard); the caller has already
// given the Esc/leader/menu layers their chance.
func (a *App) handleTreeNavKey(ev *tcell.EventKey) {
	sel := a.treeSelection()
	switch ev.Key() {
	case tcell.KeyDown:
		a.tree.SelectDelta(1)
	case tcell.KeyUp:
		a.tree.SelectDelta(-1)
	case tcell.KeyPgDn:
		_, _, _, sh := a.sidebarRect()
		a.tree.SelectDelta(sh - 2)
	case tcell.KeyPgUp:
		_, _, _, sh := a.sidebarRect()
		a.tree.SelectDelta(-(sh - 2))
	case tcell.KeyRight:
		// A collapsed folder expands; an expanded one steps into its
		// first child — the two-press descent every tree UI teaches.
		if sel == nil || !sel.IsDir {
			return
		}
		if !sel.Expanded {
			a.tree.Toggle(sel)
		} else {
			a.tree.SelectDelta(1)
		}
	case tcell.KeyLeft:
		// An expanded folder folds; anything else jumps to its parent —
		// so held-← walks all the way back up and out of a deep branch.
		if sel != nil && sel.IsDir && sel.Expanded {
			a.tree.Toggle(sel)
			return
		}
		if sel != nil {
			if p := a.tree.ParentOf(sel); p != nil {
				a.tree.Selected = p
			}
		}
	case tcell.KeyEnter:
		if sel == nil {
			return
		}
		if sel.IsDir {
			a.setActiveFolder(sel.Path)
			a.tree.Toggle(sel)
			return
		}
		// Opening a file is a commitment: focus follows the file into
		// the editor, like every tree-and-editor pairing users know.
		a.setActiveFolder(filepath.Dir(sel.Path))
		a.openFile(sel.Path)
		a.treeFocus = false
		return
	case tcell.KeyBackspace, tcell.KeyBackspace2:
		// Trims the type-to-find prefix (treefilter.go); with no prefix
		// there is nothing for Backspace to mean in a tree.
		a.treeFilterBackspace()
	case tcell.KeyTab:
		a.treeFilterStep(1)
	case tcell.KeyBacktab:
		a.treeFilterStep(-1)
	case tcell.KeyRune:
		a.treeNavRune(ev.Rune())
	}
	a.ensureTreeSelectionVisible()
}

// treeSelection returns the cursor's node, re-validated against the
// visible rows — a row folded away since the last keystroke is not a
// selection anymore.
func (a *App) treeSelection() *filetree.Node {
	if a.tree.Selected == nil {
		return nil
	}
	if a.tree.SelectedIndex(a.tree.VisibleNodes()) < 0 {
		a.tree.Selected = nil
	}
	return a.tree.Selected
}

// treeNavRune handles the letter layer: Space and * are the mark keys
// when no search is running, and every other rune — or any rune once a
// pattern is being typed — extends the type-to-find pattern
// (treefilter.go).
func (a *App) treeNavRune(r rune) {
	if a.tree.Filter == "" {
		switch r {
		case ' ':
			// Space ticks the cursor's row — the multi-selection's
			// keyboard twin of a click in the mark gutter (treemarks.go).
			// It arrives as KeyRune ' ' rather than as a key of its own,
			// so it belongs here rather than in handleTreeNavKey's switch.
			a.treeToggleMarkSelected()
			return
		case '*':
			// Mark every visible row, or clear the set when one exists —
			// one key for both directions, because undoing an over-eager
			// select has to be as cheap as making it.
			a.treeMarkAllToggle()
			return
		}
	}
	a.treeFilterType(r)
}
