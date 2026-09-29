// =============================================================================
// File: internal/app/find.go
// Author: Rohan Allison <rohanthewiz@gmail.com>
// Created: 2026-04-30
// Copyright: 2026 Rohan Allison. All rights reserved.
// Portions copyright 2026 Cloudmanic, LLC. Original author: Spicer Matthews.
// =============================================================================

// find.go owns the in-file search UI: the strip that lives directly above
// the status bar, the keystroke dispatch while it's focused, and the
// Esc-f / Esc-e / Esc-g leader entry points.
//
// The matching logic itself lives on Tab (see internal/editor/find.go and
// replace.go) so each tab carries its own query, options, match list, and
// current-index. This file only handles UI: the two input fields, the
// option toggles, rendering, and the mouse.
//
// The bar is ONE row for find and TWO once replace is open, which is why
// every panel that pins itself above the status bar asks findBarRows()
// rather than reading a constant. A replace row that floated over the
// editor would cover the line it's about to rewrite — the same argument
// the Find-all list makes for displacing instead of overlaying.
//
// Both inputs are the shared `textField` (modal.go), per the house rule
// that every single-line input in the editor is one: it already knows
// caret motion, horizontal scroll, click-to-position, and paste, and two
// hand-rolled copies of that in one bar would drift apart the first time
// one of them was fixed.

package app

import (
	"fmt"
	"strings"

	"github.com/gdamore/tcell/v2"
	"github.com/rohanthewiz/ced/internal/editor"
	"github.com/rohanthewiz/ced/internal/history"
)

// findBarHeight is the cell height of ONE row of the find bar. Layout
// code wants findBarRows() (which counts the replace row); this constant
// is the per-row unit those calculations are built from.
const findBarHeight = 1

// Field identifiers for findFocus. The bar has at most two inputs and
// Tab walks between them, so a small enum beats a bool — a third field
// (a filter, a scope) would otherwise mean rewriting every comparison.
const (
	findFocusQuery = iota
	findFocusReplace
)

// Button labels in the bar. Every glyph is single-width on purpose (the
// runeLen house rule — a double-width one would skew every rect to its
// right):
//
//	Aa   match case          |W|  whole word (the bars are the boundaries)
//	Replace  swap this hit   All  swap every hit
const (
	findCaseLabel    = " Aa "
	findWordLabel    = " |W| "
	findReplaceLabel = " Replace "
	findAllLabel     = " All "
)

// openFind shows the find bar, seeded with the SELECTION when there is
// one and empty otherwise.
//
// Both halves are the same rule findAllSelectionQuery states one floor
// down: a single-line selection is the user pointing at the exact text,
// so searching for it costs a keystroke nobody would rather spend
// retyping it, and it cannot be a wrong guess. Everything the context
// merely IMPLIES stays out — in particular the last query, because
// closing the bar already clears find state and Esc means "I'm done
// searching", so each Esc-f is still a fresh search. (A multi-line
// selection is not a search term — FindAll matches within a line — and
// findAllSelectionQuery refuses it, which leaves the bar empty.)
//
// A seeded bar searches immediately: the highlights are painted and the
// hit is focused before the user touches another key, which is the
// whole point of not having to type it.
func (a *App) openFind() {
	tab := a.activeTabPtr()
	if tab == nil || tab.IsImage() {
		return
	}
	// Read the selection BEFORE anything can disturb it — findApplyQuery
	// collapses it onto the match it focuses, so by then it is gone.
	seed := a.findAllSelectionQuery()
	selStart, _ := editor.PosOrdered(tab.Anchor, tab.Cursor)

	a.closeAllModals() // a modal would otherwise eat our keystrokes
	a.findOpen = true
	a.findReplaceOpen = false
	a.findFocus = findFocusQuery
	a.findField = newTextField(seed)
	a.replField = newTextField("")
	a.applyFindOptions()
	if seed != "" {
		// SetFindQuery picks the current hit with FirstMatchAtOrAfter
		// (the cursor), and a left-to-right selection leaves the cursor
		// at its END — past the very occurrence the query came from. So
		// collapse to the selection's START first: the highlighted text
		// becomes the current match and Enter moves on to the next one,
		// instead of the bar opening by jumping the view somewhere else.
		tab.MoveCursorTo(selStart, false)
		a.findApplyQuery()
	}
}

// openReplace opens the bar with the replace row showing and the caret in
// the query field — you have to say WHAT to replace before you can say
// what with, and openFind may already have answered that from the
// selection. Reopening an already-open bar just reveals the row (and so
// never clobbers a query the user typed), which is what makes Esc-e
// "I've found it, now let me change it" mid-search.
func (a *App) openReplace() {
	tab := a.activeTabPtr()
	if tab == nil || tab.IsImage() {
		return
	}
	if !a.findOpen {
		a.openFind()
	}
	if !a.findOpen {
		return // openFind refused (no editable tab)
	}
	a.findReplaceOpen = true
}

// closeFind hides the bar AND clears the active tab's find state so the
// highlights disappear with it. Leaving them painted after close is
// surprising — users expect Esc to mean "I'm done searching." Esc-g
// after a closed bar simply re-opens it so the user can type a fresh
// query.
func (a *App) closeFind() {
	a.rememberFindBar()
	a.findHist = histDrop{}
	a.findOpen = false
	a.findReplaceOpen = false
	a.findFocus = findFocusQuery
	a.findField = textField{}
	a.replField = textField{}
	if tab := a.activeTabPtr(); tab != nil {
		tab.ClearFind()
	}
}

// rememberFindBar records the bar's query in the find history as the bar
// goes away. Closing is the one moment every use of the bar passes
// through — typing a query, reading the hits and pressing Esc is the
// commonest search there is, and it never presses Enter — so recording
// here is what makes the history hold what was actually searched. Enter
// records too (handleFindKey), so a query stepped through and then
// edited is not lost to the edit.
func (a *App) rememberFindBar() {
	if !a.findOpen {
		return
	}
	a.recordSearch(history.SearchFind, a.findField.String())
}

// findBarRows is the bar's total height: one row for find, two once the
// replace row is showing, zero while it's closed. Every surface pinned
// above the status bar subtracts this rather than findBarHeight, so
// opening the replace row pushes them all up by exactly one row.
func (a *App) findBarRows() int {
	if !a.findOpen {
		return 0
	}
	if a.findReplaceOpen {
		return 2 * findBarHeight
	}
	return findBarHeight
}

// findApplyQuery pushes the current input text into the active tab's
// find state and snaps the cursor to the new "current" match (so the
// user can see their result while still typing). Called on every input
// change so the highlights track the query live.
func (a *App) findApplyQuery() {
	tab := a.activeTabPtr()
	if tab == nil {
		return
	}
	tab.SetFindQuery(a.findField.String())
	tab.FocusCurrentMatch()
}

// applyFindOptions pushes the app-level toggles onto the active tab and
// re-runs its query under them. The App holds the authoritative copy —
// the toggles are a property of how the USER searches, not of one file,
// so they carry across tabs within the session — and this is the single
// write path, the same shape as applyWordHighlight.
//
// Deliberately NOT persisted to config.json. A saved "match case" would
// silently narrow the first search of every future session, with no bar
// on screen to explain why the hit the user expected didn't appear;
// re-flipping it is one click and it's visible while it matters.
func (a *App) applyFindOptions() {
	tab := a.activeTabPtr()
	if tab == nil {
		return
	}
	tab.SetFindOptions(a.findOptions())
}

// findOptions is the App's toggles as the matcher's struct — the ONE
// place Aa / |W| turn into editor.FindOptions. The find bar, the Find-all
// list and Find in project all read it, so a search narrowed in the bar
// stays narrowed when it becomes a list; before they shared it, the
// lists quietly matched a case-insensitive substring whatever the
// toggles said.
func (a *App) findOptions() editor.FindOptions {
	return editor.FindOptions{CaseSensitive: a.findCase, WholeWord: a.findWord}
}

// findOptionsNote names the non-default options as a parenthetical —
// " (match case, whole word)" — or "" under the defaults. The lists and
// their "no occurrences" flashes carry it because they are the surfaces
// that show a search WITHOUT the bar's lit toggles beside it: a list
// opened from Esc-F, the ≡ menu or a right-click would otherwise be
// narrower than the user thinks, with nothing on screen saying why.
// Words rather than the Aa / |W| glyphs because it sits in prose.
func findOptionsNote(opts editor.FindOptions) string {
	var parts []string
	if opts.CaseSensitive {
		parts = append(parts, "match case")
	}
	if opts.WholeWord {
		parts = append(parts, "whole word")
	}
	if len(parts) == 0 {
		return ""
	}
	return " (" + strings.Join(parts, ", ") + ")"
}

// toggleFindCase flips case sensitivity and re-runs the search.
func (a *App) toggleFindCase() {
	a.findCase = !a.findCase
	a.applyFindOptions()
}

// toggleFindWord flips whole-word matching and re-runs the search.
func (a *App) toggleFindWord() {
	a.findWord = !a.findWord
	a.applyFindOptions()
}

// findNext is the Enter-in-the-bar action: jump to the next match (with
// wrap). Also reachable from the Esc-g leader.
func (a *App) findNext() {
	if tab := a.activeTabPtr(); tab != nil {
		tab.FindNext()
	}
}

// findPrev is the Shift-Enter action: jump to the previous match.
func (a *App) findPrev() {
	if tab := a.activeTabPtr(); tab != nil {
		tab.FindPrev()
	}
}

// replaceCurrent swaps the highlighted hit and advances to the next one.
// A bar with no query (or no hits) flashes rather than silently doing
// nothing — the user pressed a button and is owed an answer.
func (a *App) replaceCurrent() {
	tab := a.activeTabPtr()
	if tab == nil {
		return
	}
	if !tab.ReplaceCurrent(a.replField.String()) {
		a.flash("Nothing to replace")
		return
	}
	a.rememberReplace()
}

// rememberReplace records the pair a replace just used: the query in the
// find history and the replacement in its own. Only a replace that DID
// something records — a button pressed on a bar with no hits changed
// nothing worth recalling. An empty replacement (delete every hit) is
// refused by the history itself: a blank row cannot be picked by eye.
func (a *App) rememberReplace() {
	a.recordSearch(history.SearchFind, a.findField.String())
	a.recordSearch(history.SearchReplace, a.replField.String())
}

// replaceAll swaps every hit in one undo step and reports the count —
// the count is the whole feedback for a bulk edit that may have happened
// entirely off-screen.
func (a *App) replaceAll() {
	tab := a.activeTabPtr()
	if tab == nil {
		return
	}
	n := tab.ReplaceAll(a.replField.String())
	if n == 0 {
		a.flash("Nothing to replace")
		return
	}
	a.rememberReplace()
	a.flash("Replaced " + plural(n, "occurrence", "occurrences"))
}

// menuFind is the action menu entry point. Behaves identically to the
// Esc-f leader — opens the bar against the active tab.
func (a *App) menuFind() {
	a.closeMenu()
	a.openFind()
}

// menuReplace is the ≡ / Esc-e entry point for the replace row.
func (a *App) menuReplace() {
	a.closeMenu()
	a.openReplace()
}

// menuToggleFindCase / menuToggleFindWord are the ≡ twins of the bar's
// two toggle buttons. They exist because the bar OWNS the keyboard while
// it's open, so the menu can only be reached before a search starts —
// which is exactly when a user wants to say "this time, match case".
func (a *App) menuToggleFindCase() {
	a.closeMenu()
	a.toggleFindCase()
}

// menuToggleFindWord flips whole-word matching from the ≡ menu.
func (a *App) menuToggleFindWord() {
	a.closeMenu()
	a.toggleFindWord()
}

// findCaseToggleLabel / findWordToggleLabel render as the state, not the
// action, unlike most toggle rows: "Match case: on" answers "how am I
// searching?" at a glance, which is the question a search modifier
// actually raises.
func (a *App) findCaseToggleLabel() string {
	return "Match case: " + onOff(a.findCase)
}

// findWordToggleLabel names the whole-word state; see findCaseToggleLabel.
func (a *App) findWordToggleLabel() string {
	return "Whole word: " + onOff(a.findWord)
}

// onOff renders a boolean the way the menu's state-labelled rows do.
func onOff(b bool) string {
	if b {
		return "on"
	}
	return "off"
}

// hasFindable reports whether the active tab is a text tab — used to
// gray out the menu row on image tabs / no-tab states.
func (a *App) hasFindable() bool {
	t := a.activeTabPtr()
	return t != nil && !t.IsImage()
}

// hasReplaceable is find's precondition plus "the buffer can be edited".
// Today that's the same predicate; it's named separately so a future
// read-only tab mode dims Replace without dimming Find.
func (a *App) hasReplaceable() bool {
	t := a.activeTabPtr()
	return t != nil && !t.IsImage()
}

// -----------------------------------------------------------------------------
// Geometry — one source for draw AND mouse routing
// -----------------------------------------------------------------------------

// findBarRect returns the on-screen rectangle of the whole bar (both rows
// when replace is open), spanning the editor's column band. Caller is
// expected to check a.findOpen before drawing.
func (a *App) findBarRect() (x, y, w, h int) {
	// Directly above the BOTTOM DOCK, in the editor's column band. The
	// bar is about the file in front of you rather than about the
	// workspace, so it hugs the editor on both axes: it stops where the
	// side docks stop, and it sits above the bottom dock rather than
	// below it — a bar pinned under a git panel would be a long way from
	// the line it is searching.
	lw := a.leftBlockW()
	h = a.findBarRows()
	return lw, a.bottomDockTop() - h, a.width - lw - a.rightBlockW(), h
}

// findBarContains reports whether (x, y) falls on the open bar.
func (a *App) findBarContains(x, y int) bool {
	if !a.findOpen {
		return false
	}
	bx, by, bw, bh := a.findBarRect()
	return x >= bx && x < bx+bw && y >= by && y < by+bh
}

// findCaseRect / findWordRect return the two option buttons, right-
// aligned on the query row just left of the counter. Both are btnRects
// so draw and hit-test read the same geometry (the btnRect house rule).
func (a *App) findCaseRect() btnRect {
	w := a.findWordRect()
	return btnRect{x: w.x - runeLen(findCaseLabel), y: w.y, w: runeLen(findCaseLabel)}
}

// findWordRect is the whole-word button, the rightmost control on the
// query row — every other right-hand item (counter, hint) is a label and
// yields to it on a narrow window.
func (a *App) findWordRect() btnRect {
	bx, by, bw, _ := a.findBarRect()
	return btnRect{x: bx + bw - runeLen(findWordLabel) - 1, y: by, w: runeLen(findWordLabel)}
}

// findReplaceBtnRect / findAllBtnRect return the replace row's two
// buttons, right-aligned under the option toggles so the row reads
// "input … then the two things you can do with it".
func (a *App) findReplaceBtnRect() btnRect {
	all := a.findAllBtnRect()
	return btnRect{x: all.x - runeLen(findReplaceLabel) - 1, y: all.y, w: runeLen(findReplaceLabel)}
}

// findAllBtnRect is the "All" button on the replace row.
func (a *App) findAllBtnRect() btnRect {
	bx, by, bw, _ := a.findBarRect()
	return btnRect{x: bx + bw - runeLen(findAllLabel) - 1, y: by + findBarHeight, w: runeLen(findAllLabel)}
}

// findFieldSpan returns the row and the [start, end) columns of one of
// the bar's inputs. It is the single source the draw path and the click
// path both use to place a caret, so a click can't land on a different
// rune than the one it appears to.
//
// The query row's field stops short of the option buttons; the replace
// row's stops short of its two action buttons.
func (a *App) findFieldSpan(field int) (y, start, end int) {
	bx, by, bw, _ := a.findBarRect()
	labelW := runeLen(findBarLabel(field))
	if field == findFocusReplace {
		return by + findBarHeight, bx + labelW, a.findReplaceBtnRect().x - 1
	}
	// Fall back to the bar's own right edge when the toggles were
	// dropped for width — the input is the one thing that must survive.
	end = a.findCaseRect().x - 1
	if end <= bx+labelW {
		end = bx + bw - 1
	}
	return by, bx + labelW, end
}

// findBarLabel names a row. Same rune width for both so the two inputs
// start in the same column and the bar reads as a form. The ▾ in place of
// the colon marks the label as the row's history button
// (findHistBtnRect): the label is the one part of the row that is not
// already something else, so the button costs the input no width.
func findBarLabel(field int) string {
	if field == findFocusReplace {
		return " Repl▾ "
	}
	return " Find▾ "
}

// findHistBtnRect is a row's history button — its whole label, so the
// target is the width of a word rather than the one ▾ cell.
func (a *App) findHistBtnRect(field int) btnRect {
	bx, by, _, _ := a.findBarRect()
	if field == findFocusReplace {
		by += findBarHeight
	}
	return btnRect{x: bx, y: by, w: runeLen(findBarLabel(field))}
}

// findHistField names the row the open dropdown belongs to, from the
// list it shows.
func (a *App) findHistField() int {
	if a.findHist.kind == history.SearchReplace {
		return findFocusReplace
	}
	return findFocusQuery
}

// findHistGeom lays the bar's dropdown out against its row's input — the
// one geometry the draw, the keys and the mouse all read. The anchor row
// is the bar's TOP whichever row the list belongs to, so the replacement
// list opens above the Find row instead of covering the query it is
// about to be paired with.
func (a *App) findHistGeom() histGeom {
	_, start, end := a.findFieldSpan(a.findHistField())
	_, by, _, _ := a.findBarRect()
	return a.histDropGeom(&a.findHist, btnRect{x: start, y: by, w: end - start})
}

// openFindHist opens the dropdown over field's row and gives that row the
// keyboard — the pick lands in it, so it should be where typing goes
// after. It opens UPWARD by preference: the bar hugs the bottom of the
// editor, and below it is the status bar or a docked panel.
func (a *App) openFindHist(field int) {
	a.findFocus = field
	kind, cur := history.SearchFind, a.findField.String()
	if field == findFocusReplace {
		kind, cur = history.SearchReplace, a.replField.String()
	}
	a.openHistDrop(&a.findHist, kind, cur, true)
}

// findHistPick puts a picked entry in field's input and gives that row the
// keyboard. field is passed in rather than read from the dropdown because
// a pick has already closed (and so zeroed) it. A picked query is searched
// at once, exactly as if it had been typed — the bar's contract is that
// what is in the Find box is what is highlighted.
func (a *App) findHistPick(field int, v string) {
	a.findFocus = field
	if field == findFocusReplace {
		a.replField = newTextField(v)
		return
	}
	a.findField = newTextField(v)
	a.findApplyQuery()
}

// findHistMouse routes a mouse event to the bar's open dropdown, reporting
// whether it claimed it. The router asks this before any panel: the list
// is drawn over the editor and whatever is docked beside it.
func (a *App) findHistMouse(x, y int, btn tcell.ButtonMask) bool {
	if !a.findOpen || !a.findHist.open {
		return false
	}
	g := a.findHistGeom()
	field := a.findHistField()
	consumed, v, picked := a.histDropMouse(&a.findHist, g, a.findHistBtnRect(field), x, y, btn)
	if picked {
		a.findHistPick(field, v)
	}
	return consumed
}

// drawFindHist paints the bar's dropdown. Called from the overlay layer,
// not from drawFindBar: the list covers panels drawn after the bar.
func (a *App) drawFindHist() {
	if a.findOpen && a.findHist.open {
		a.drawHistDrop(&a.findHist, a.findHistGeom())
	}
}

// -----------------------------------------------------------------------------
// Keyboard
// -----------------------------------------------------------------------------

// handleFindKey dispatches a keystroke while the bar is focused.
// Behavior:
//
//	Esc                     close the bar
//	Enter (query row)       jump to the next match
//	Shift+Enter             jump to the previous match
//	Enter (replace row)     replace this hit and advance
//	Tab / Shift+Tab         move between the query and replace inputs
//	Up                      recent searches / replacements for the
//	                        focused row (searchhistory.go)
//	Down                    list every occurrence (findall.go)
//	Alt+c / Alt+w           toggle match case / whole word
//	Alt+a                   replace all
//	everything else         standard single-line editing in the focused
//	                        field (live re-search on the query row)
//
// The Alt chords are the bar's only modifier keys and they're safe for
// the same reason the leader table's Alt path is: the bar owns the
// keyboard, so handleKey's Alt+rune leader branch never sees them —
// including inside tmux, where "Esc c" arrives folded as Alt+c.
func (a *App) handleFindKey(ev *tcell.EventKey) {
	// An open history dropdown hears the key first; one it hands back
	// (it has closed) carries on through the bar as if it never opened.
	if a.findHist.open {
		field := a.findHistField()
		consumed, v, picked := a.histDropKey(&a.findHist, a.findHistGeom(), ev)
		if picked {
			a.findHistPick(field, v)
		}
		if consumed {
			return
		}
	}
	if ev.Modifiers()&tcell.ModAlt != 0 && ev.Key() == tcell.KeyRune {
		switch ev.Rune() {
		case 'c':
			a.toggleFindCase()
			return
		case 'w':
			a.toggleFindWord()
			return
		case 'a':
			if a.findReplaceOpen {
				a.replaceAll()
			}
			return
		}
	}
	switch ev.Key() {
	case tcell.KeyEsc:
		a.closeFind()
		return
	case tcell.KeyEnter:
		if a.findFocus == findFocusReplace {
			a.replaceCurrent()
			return
		}
		if ev.Modifiers()&tcell.ModShift != 0 {
			a.findPrev()
		} else {
			a.findNext()
		}
		a.recordSearch(history.SearchFind, a.findField.String())
		return
	case tcell.KeyTab, tcell.KeyBacktab:
		a.findSwitchField()
		return
	case tcell.KeyUp:
		a.openFindHist(a.findFocus)
		return
	case tcell.KeyDown:
		// Down out of a one-line input means "show me the rest" — the
		// same reflex a browser's search field trains. The bar closes as
		// the list opens: they're one search in two shapes, not two.
		a.openFindAllFromBar()
		return
	}
	if a.findFocus == findFocusReplace {
		a.replField.handleKey(ev)
		return
	}
	if _, edited := a.findField.handleKey(ev); edited {
		a.findApplyQuery()
	}
}

// findSwitchField moves the caret between the two inputs, opening the
// replace row if it isn't showing — Tab is the gesture a form teaches,
// and answering it with nothing when the second field merely isn't
// visible yet reads as a broken bar.
func (a *App) findSwitchField() {
	if !a.findReplaceOpen {
		a.findReplaceOpen = true
		a.findFocus = findFocusReplace
		return
	}
	if a.findFocus == findFocusQuery {
		a.findFocus = findFocusReplace
	} else {
		a.findFocus = findFocusQuery
	}
}

// -----------------------------------------------------------------------------
// Mouse
// -----------------------------------------------------------------------------

// findBarPress routes a left press on the bar, reporting whether it was
// consumed. Buttons act; a click in a field moves the caret there AND
// takes focus (the mouse-first focus model — you click where you want to
// type); anything else on the bar is inert rather than falling through
// to the editor underneath.
func (a *App) findBarPress(x, y int) bool {
	if !a.findBarContains(x, y) {
		return false
	}
	switch {
	case a.findWordRect().contains(x, y):
		a.toggleFindWord()
		return true
	case a.findCaseRect().contains(x, y):
		a.toggleFindCase()
		return true
	}
	for _, field := range []int{findFocusQuery, findFocusReplace} {
		if field == findFocusReplace && !a.findReplaceOpen {
			continue
		}
		if a.findHistBtnRect(field).contains(x, y) {
			// A second click on the same label (the list's own toggle)
			// never gets here — findHistMouse closes the list and claims
			// the press. Reaching this means the list is shut, or open
			// on the OTHER row, which this click switches to.
			a.openFindHist(field)
			return true
		}
	}
	if a.findReplaceOpen {
		switch {
		case a.findAllBtnRect().contains(x, y):
			a.replaceAll()
			return true
		case a.findReplaceBtnRect().contains(x, y):
			a.replaceCurrent()
			return true
		}
	}
	_, by, _, _ := a.findBarRect()
	field := findFocusQuery
	if a.findReplaceOpen && y >= by+findBarHeight {
		field = findFocusReplace
	}
	a.findFocus = field
	_, start, end := a.findFieldSpan(field)
	if field == findFocusReplace {
		a.replField.clickAt(start, end, x)
	} else {
		a.findField.clickAt(start, end, x)
	}
	return true
}

// -----------------------------------------------------------------------------
// Drawing
// -----------------------------------------------------------------------------

// drawFindBar renders the bar at the bottom of the editor area:
//
//	Find: <input>            Aa  |W|   3 of 12   Enter: next · Esc: close
//	Repl: <input>                            Replace   All
//
// The hint on the right is dropped first when the window is too narrow to
// fit it and the match counter is dropped next; the input and the toggle
// buttons always stay, because a control you can't reach is worse than a
// label you can't read (the git panel header's rule).
func (a *App) drawFindBar() {
	if !a.findOpen {
		return
	}
	bx, by, bw, _ := a.findBarRect()

	bg := a.theme.LineHL
	barStyle := tcell.StyleDefault.Background(bg).Foreground(a.theme.Text)
	labelStyle := tcell.StyleDefault.Background(bg).Foreground(a.theme.Accent).Bold(true)
	mutedStyle := tcell.StyleDefault.Background(bg).Foreground(a.theme.Muted)
	emptyStyle := tcell.StyleDefault.Background(bg).Foreground(a.theme.Error).Bold(true)

	// Clear both rows.
	for row := 0; row < a.findBarRows(); row++ {
		for cx := bx; cx < bx+bw; cx++ {
			a.screen.SetContent(cx, by+row, ' ', nil, barStyle)
		}
	}

	// Row labels. The focused one is accented so the bar says where the
	// next keystroke lands without the user hunting for the caret.
	queryLabelSt, replLabelSt := labelStyle, mutedStyle
	if a.findFocus == findFocusReplace {
		queryLabelSt, replLabelSt = mutedStyle, labelStyle
	}
	drawAt(a.screen, bx, by, findBarLabel(findFocusQuery), queryLabelSt)

	// Option toggles: lit when active, muted when not.
	a.drawFindToggle(a.findCaseRect(), findCaseLabel, a.findCase)
	a.drawFindToggle(a.findWordRect(), findWordLabel, a.findWord)

	// Counter, then hint — each drawn only if what's left of the row can
	// hold it, walking leftwards from the toggles.
	rightEdge := a.findCaseRect().x
	counter := a.findCounterText()
	hint := " Enter: next · ⇧Enter: prev · ↑: recent · ↓: list all · Esc: close "
	if a.findReplaceOpen {
		hint = " Enter: replace · alt+a: all · ↑: recent · tab: field · Esc: close "
	}
	inputStart := bx + runeLen(findBarLabel(findFocusQuery))
	if counter != "" && rightEdge-runeLen(counter)-2 > inputStart+8 {
		rightEdge -= runeLen(counter) + 2
		// Red when the query has no matches, so the user gets immediate
		// negative feedback without having to read the digits.
		st := mutedStyle
		if a.findHasNoMatches() {
			st = emptyStyle
		}
		drawAt(a.screen, rightEdge, by, counter, st)
	}
	if rightEdge-runeLen(hint) > inputStart+8 {
		rightEdge -= runeLen(hint)
		drawAt(a.screen, rightEdge, by, hint, mutedStyle)
	}

	// Inputs. The focused field owns the terminal caret.
	qy, qStart, qEnd := a.findFieldSpan(findFocusQuery)
	if e := rightEdge - 1; e < qEnd {
		qEnd = e
	}
	a.findField.draw(a.screen, qy, qStart, qEnd, barStyle, a.findFocus == findFocusQuery)

	if !a.findReplaceOpen {
		return
	}
	ry := by + findBarHeight
	drawAt(a.screen, bx, ry, findBarLabel(findFocusReplace), replLabelSt)
	btnSt := tcell.StyleDefault.Background(a.theme.Selection).Foreground(a.theme.Accent).Bold(true)
	rb, ab := a.findReplaceBtnRect(), a.findAllBtnRect()
	drawAt(a.screen, rb.x, rb.y, findReplaceLabel, btnSt)
	drawAt(a.screen, ab.x, ab.y, findAllLabel, btnSt)
	_, rStart, rEnd := a.findFieldSpan(findFocusReplace)
	a.replField.draw(a.screen, ry, rStart, rEnd, barStyle, a.findFocus == findFocusReplace)
}

// drawFindToggle paints one option button in its on/off state. Active is
// the same accent-on-selection treatment every other pressed control in
// the editor uses; inactive is muted rather than hidden, because a
// toggle you can't see is a toggle nobody finds.
func (a *App) drawFindToggle(r btnRect, label string, on bool) {
	st := tcell.StyleDefault.Background(a.theme.LineHL).Foreground(a.theme.Muted)
	if on {
		st = tcell.StyleDefault.Background(a.theme.Selection).
			Foreground(a.theme.Accent).Bold(true)
	}
	drawAt(a.screen, r.x, r.y, label, st)
}

// findCounterText renders the "N of M" indicator. Returns "" when there
// is no query so the renderer can skip drawing the field entirely.
func (a *App) findCounterText() string {
	if len(a.findField.value) == 0 {
		return ""
	}
	tab := a.activeTabPtr()
	if tab == nil {
		return ""
	}
	if len(tab.FindMatches) == 0 {
		return "no results"
	}
	return fmt.Sprintf("%d of %d", tab.FindIndex+1, len(tab.FindMatches))
}

// findHasNoMatches reports whether the user has typed a query that
// returned zero hits, so the counter can flip color.
func (a *App) findHasNoMatches() bool {
	if len(a.findField.value) == 0 {
		return false
	}
	tab := a.activeTabPtr()
	return tab != nil && len(tab.FindMatches) == 0
}
