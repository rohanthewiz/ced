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
// then arrows walk the rows, →/← expand and collapse, Enter opens,
// plain letters typeahead-jump, and n/N/d/r run the same New file/
// New folder/Delete/Rename verbs the right-click menu offers — one
// vocabulary, third door (context menu, ≡ menu, now keys).
//
// Focus discipline mirrors the terminal and chat panels: the branch in
// handleKey sits AFTER the Esc/leader/menu blocks, so every global
// gesture keeps working from inside the tree (Esc-s still saves,
// Esc-Esc still opens the menu). All OTHER keys are claimed while the
// tree has focus — a keystroke aimed at the tree must never leak into
// the buffer as an edit. Clicking anywhere outside the sidebar (or
// opening a file) hands focus back to the editor, the same
// click-where-you-want-to-type model the other panels follow.
//
// The n/N/d/r verbs shadow typeahead for those letters — the
// deliberate cost of having verbs at all. Every shadowed name is still
// reachable: one arrow key, or the finder, which is better at names
// anyway. ('N' costs nothing extra: typeahead lowercases, so the names
// it would have reached were already claimed by 'n'.)

package app

import (
	"path/filepath"
	"strings"

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
		a.sidebarShown = true
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
	case tcell.KeyRune:
		a.treeNavRune(ev.Rune(), sel)
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

// treeNavRune handles the letter layer: the three file-management verbs
// first, any other rune as typeahead.
func (a *App) treeNavRune(r rune, sel *filetree.Node) {
	switch r {
	case 'n':
		// New file — in the selected folder, or the selected file's
		// folder, or the root when nothing is selected: the same target
		// resolution the ≡ menu's New File uses via activeFolder.
		target := a.tree.Root
		switch {
		case sel != nil && sel.IsDir:
			target = sel
		case sel != nil:
			if p := a.tree.ParentOf(sel); p != nil {
				target = p
			}
		}
		a.setActiveFolder(target.Path)
		ctxNewFile(a, target)
	case 'N':
		// New folder — the shifted twin of 'n', resolving its target the
		// same way. Safe here where esc-N is not: this is a bare rune the
		// focused tree claims, not an ESC pair the terminal can swallow.
		// It costs typeahead nothing that 'n' hadn't already cost.
		target := a.tree.Root
		switch {
		case sel != nil && sel.IsDir:
			target = sel
		case sel != nil:
			if p := a.tree.ParentOf(sel); p != nil {
				target = p
			}
		}
		a.setActiveFolder(target.Path)
		ctxNewFolder(a, target)
	case 'd':
		if sel != nil {
			ctxDelete(a, sel)
		}
	case 'r':
		if sel != nil {
			ctxRename(a, sel)
		}
	default:
		a.treeTypeahead(r)
	}
}

// treeTypeahead jumps the cursor to the next visible row whose name
// starts with r, wrapping past the end — press again to cycle through
// same-letter siblings. Single-rune matching on purpose: a stateful
// multi-rune buffer needs a timeout, a timeout needs a visible state,
// and the finder already answers "jump to a name I can spell".
func (a *App) treeTypeahead(r rune) {
	rows := a.tree.VisibleNodes()
	if len(rows) == 0 {
		return
	}
	prefix := strings.ToLower(string(r))
	start := a.tree.SelectedIndex(rows) + 1
	for i := 0; i < len(rows); i++ {
		n := rows[(start+i)%len(rows)]
		if strings.HasPrefix(strings.ToLower(n.Name), prefix) {
			a.tree.Selected = n
			return
		}
	}
}
