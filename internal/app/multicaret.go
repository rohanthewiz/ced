// =============================================================================
// File: internal/app/multicaret.go
// Author: Rohan Allison <rohanthewiz@gmail.com>
// Created: 2026-07-31
// Copyright: 2026 Rohan Allison. All rights reserved.
// =============================================================================

// multicaret.go is the UI half of multi-line editing: the ≡ rows, the
// Esc-leader twins, the Alt+click gesture, and the status readout. The
// model — where carets live, how an edit fans out over them, what drops
// them — is internal/editor/multicaret.go.
//
// Gesture choices, and why:
//
//   - **Alt+click** adds or removes a caret. Alt is the one modifier the
//     house rules already bless (it never fights tmux), and a plain
//     click has to keep meaning "put the caret here" — the mouse-first
//     model depends on it.
//   - **Esc drops every extra caret**, as a side effect, alongside the
//     ghost suggestion and the chat highlight. Esc is the editor's
//     universal "drop that" and a stray column of carets is exactly the
//     state a user needs one key to escape.
//   - **Esc-m / Esc-M** grow the column down / up, following the h/H and
//     o/O convention that shift means the other direction. Both repeat,
//     so "Esc m m m" builds a four-line column without re-arming.
//   - **Esc-*** adds the next occurrence — vim's word-under-cursor key,
//     which is the muscle memory this gesture actually competes with
//     (Cmd+D never reaches a terminal app).
package app

import "github.com/rohanthewiz/ced/internal/editor"

// menuAddCaretBelow drops a caret one line below the lowest one, at the
// current column. The workhorse gesture: three presses and a typed
// string edits four lines at once.
func (a *App) menuAddCaretBelow() {
	a.closeMenu()
	a.addCaretLine(1)
}

// menuAddCaretAbove is the same gesture upward, from the topmost caret.
func (a *App) menuAddCaretAbove() {
	a.closeMenu()
	a.addCaretLine(-1)
}

// addCaretLine is the shared body of the two directional rows. A refusal
// means the file ran out in that direction, which is worth saying — the
// user pressed a key and nothing visible happened.
func (a *App) addCaretLine(delta int) {
	t := a.activeTabPtr()
	if t == nil || t.IsImage() {
		return
	}
	if !t.AddCaretLine(delta) {
		a.flash("No line to add a caret on")
		return
	}
	a.flashCaretCount(t)
}

// menuAddNextOccurrence selects the word under the cursor, then adds a
// caret on each further occurrence — one per press. See
// editor.AddNextOccurrence for why the first press only selects.
func (a *App) menuAddNextOccurrence() {
	a.closeMenu()
	t := a.activeTabPtr()
	if t == nil || t.IsImage() {
		return
	}
	if !t.AddNextOccurrence() {
		a.flash("No further occurrence")
		return
	}
	a.flashCaretCount(t)
}

// menuSelectAllOccurrences puts a caret on every occurrence of the word
// under the cursor at once — the "rename this local" gesture.
func (a *App) menuSelectAllOccurrences() {
	a.closeMenu()
	t := a.activeTabPtr()
	if t == nil || t.IsImage() {
		return
	}
	n := t.SelectAllOccurrences()
	if n == 0 {
		a.flash("Nothing under the cursor to match")
		return
	}
	a.flash(plural(n, "occurrence", "occurrences") + " selected")
}

// menuClearCarets collapses back to a single caret — the menu twin of
// Esc, for the times the menu is already open.
func (a *App) menuClearCarets() {
	a.closeMenu()
	t := a.activeTabPtr()
	if t == nil {
		return
	}
	if t.ClearCarets() {
		a.flash("Back to one caret")
	}
}

// clearCarets drops the extra carets on the active tab, reporting
// whether it changed anything. Called from the Esc branch of handleKey,
// which must not consume the keystroke — Esc still arms the leader and
// opens the menu on a double tap.
func (a *App) clearCarets() bool {
	t := a.activeTabPtr()
	return t != nil && t.ClearCarets()
}

// hasCarets reports whether the active tab has secondary carets — the
// enable predicate for the Clear row.
func (a *App) hasCarets() bool {
	t := a.activeTabPtr()
	return t != nil && t.HasCarets()
}

// hasMultiCaretTarget reports whether the occurrence gestures have
// anything to work on. Deliberately the same predicate as the other
// editing rows rather than "is the cursor in a word": probing the word
// under the cursor on every menu draw would make the row flicker between
// enabled and dim as the caret moves, and the action already flashes
// when it finds nothing.
func (a *App) hasMultiCaretTarget() bool { return a.hasEditableTab() }

// flashCaretCount reports the caret total after a gesture that changed
// it. The status bar carries the same number continuously (see
// caretStatusSuffix); this is the immediate confirmation that the key
// press landed, which matters most for the second caret — the point
// where a user is still deciding whether the gesture works at all.
func (a *App) flashCaretCount(t *editor.Tab) {
	a.flash(plural(t.CaretCount(), "caret", "carets"))
}

// caretStatusSuffix is the status-bar fragment naming how many carets
// are live, or "" for the ordinary single-caret case. Multi-caret is a
// mode with no other visual anchor once the extra carets scroll out of
// view — this is what keeps it from being a mystery.
func (a *App) caretStatusSuffix() string {
	t := a.activeTabPtr()
	if t == nil || !t.HasCarets() {
		return ""
	}
	return " · " + plural(t.CaretCount(), "caret", "carets")
}

// editorAltPress handles an Alt+click in the editor body: toggle a caret
// at the clicked position. Reports false when the click landed outside
// any line so the caller can fall back to the normal press path.
func (a *App) editorAltPress(x, y int) bool {
	tab := a.activeTabPtr()
	if tab == nil || tab.IsImage() {
		return false
	}
	ex, ey, ew, eh := a.editorRect()
	pos, ok := tab.HitTest(x-ex, y-ey, ew, eh)
	if !ok {
		return false
	}
	if !tab.AddCaretAt(pos) {
		return false
	}
	a.flashCaretCount(tab)
	return true
}

// plural renders a count with the right noun — used by every caret
// message so "1 caret" never reads as "1 carets".
func plural(n int, one, many string) string {
	word := many
	if n == 1 {
		word = one
	}
	return itoa(n) + " " + word
}
