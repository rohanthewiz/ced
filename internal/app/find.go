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

	"github.com/gdamore/tcell/v2"
	"github.com/rohanthewiz/ced/internal/editor"
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

// openFind shows the find bar with an empty input. We don't pre-fill
// the user's last query because closing the bar already clears find
// state — Esc means "I'm done searching." Each Esc-f opens a fresh
// search.
func (a *App) openFind() {
	tab := a.activeTabPtr()
	if tab == nil || tab.IsImage() {
		return
	}
	a.closeAllModals() // a modal would otherwise eat our keystrokes
	a.findOpen = true
	a.findReplaceOpen = false
	a.findFocus = findFocusQuery
	a.findField = newTextField("")
	a.replField = newTextField("")
	a.applyFindOptions()
}

// openReplace opens the bar with the replace row showing and the caret in
// the query field — you have to say WHAT to replace before you can say
// what with. Reopening an already-open bar just reveals the row, so
// Esc-e is also "I've found it, now let me change it" mid-search.
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
	a.findOpen = false
	a.findReplaceOpen = false
	a.findFocus = findFocusQuery
	a.findField = textField{}
	a.replField = textField{}
	if tab := a.activeTabPtr(); tab != nil {
		tab.ClearFind()
	}
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
	tab.SetFindOptions(editor.FindOptions{
		CaseSensitive: a.findCase,
		WholeWord:     a.findWord,
	})
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
	}
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
// when replace is open), spanning the editor's column band and pinned
// directly above the status bar. Caller is expected to check a.findOpen
// before drawing.
func (a *App) findBarRect() (x, y, w, h int) {
	lw := a.leftBlockW()
	h = a.findBarRows()
	return lw, a.height - 1 - h, a.width - lw - a.rightBlockW(), h
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
// start in the same column and the bar reads as a form.
func findBarLabel(field int) string {
	if field == findFocusReplace {
		return " Repl: "
	}
	return " Find: "
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
		return
	case tcell.KeyTab, tcell.KeyBacktab:
		a.findSwitchField()
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
	hint := " Enter: next · ⇧Enter: prev · ↓: list all · Esc: close "
	if a.findReplaceOpen {
		hint = " Enter: replace · alt+a: all · tab: field · Esc: close "
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
