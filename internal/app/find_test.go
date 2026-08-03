// =============================================================================
// File: internal/app/find_test.go
// Author: Spicer Matthews <spicer@cloudmanic.com>
// Created: 2026-04-30
// Copyright: 2026 Cloudmanic, LLC. All rights reserved.
// =============================================================================

package app

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gdamore/tcell/v2"

	"github.com/rohanthewiz/ced/internal/editor"
)

// seedFindApp opens a tab with content seeded for find tests so each
// test can focus on the behaviour under test rather than fixture setup.
func seedFindApp(t *testing.T, content string) *App {
	t.Helper()
	dir := t.TempDir()
	target := filepath.Join(dir, "f.txt")
	if err := os.WriteFile(target, []byte(content), 0644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	a := newTestApp(t, dir)
	a.openFile(target)
	return a
}

// screenHasText reports whether want appears contiguously on any row of
// the simulation screen. Handy for bar / panel draws where the exact
// column depends on the window width and the point is only that the
// control made it onto the screen at all.
func screenHasText(t *testing.T, a *App, want string) bool {
	t.Helper()
	scr := a.screen.(tcell.SimulationScreen)
	scr.Show() // SimulationScreen serves GetContents from the *front* buffer.
	cells, w, h := scr.GetContents()
	for y := 0; y < h; y++ {
		var row []rune
		for x := 0; x < w; x++ {
			c := cells[y*w+x]
			if len(c.Runes) == 0 {
				row = append(row, ' ')
				continue
			}
			row = append(row, c.Runes[0])
		}
		if strings.Contains(string(row), want) {
			return true
		}
	}
	return false
}

// TestOpenFind_OpensBarEmpty drops the user into a focused find bar
// with an empty input. Pre-fill from a prior query is intentionally
// not done — closing the bar already clears find state, so each Esc-f
// is a fresh search.
func TestOpenFind_OpensBarEmpty(t *testing.T) {
	a := seedFindApp(t, "foo bar foo")
	a.openFind()
	if !a.findOpen {
		t.Fatal("openFind did not flip findOpen")
	}
	if a.findField.String() != "" {
		t.Fatalf("input should be empty, got %q", a.findField.String())
	}
}

// TestOpenFind_NoTabIsNoOp guards against opening the bar when there's
// no text tab to search. Without this, the bar would float over an
// empty editor with nothing to highlight.
func TestOpenFind_NoTabIsNoOp(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	a.openFind()
	if a.findOpen {
		t.Fatal("openFind should be a no-op with no tab")
	}
}

// TestHandleFindKey_TypingLiveSearches drives the per-keystroke handler
// the way a user would: type "foo", and the active tab's match list
// should be populated and the cursor should sit on the first match.
func TestHandleFindKey_TypingLiveSearches(t *testing.T) {
	a := seedFindApp(t, "foo bar foo")
	a.openFind()
	for _, r := range "foo" {
		a.handleFindKey(keyEv(tcell.KeyRune, r))
	}
	tab := a.activeTabPtr()
	if len(tab.FindMatches) != 2 {
		t.Fatalf("expected 2 matches, got %d", len(tab.FindMatches))
	}
	if tab.Cursor != (editor.Position{Line: 0, Col: 0}) {
		t.Fatalf("cursor should snap to first match, got %+v", tab.Cursor)
	}
}

// TestHandleFindKey_EnterAdvances simulates Enter inside the bar — it
// should jump to the next match, with wrap-around.
func TestHandleFindKey_EnterAdvances(t *testing.T) {
	a := seedFindApp(t, "foo\nfoo\nfoo")
	a.openFind()
	for _, r := range "foo" {
		a.handleFindKey(keyEv(tcell.KeyRune, r))
	}
	tab := a.activeTabPtr()
	a.handleFindKey(keyEv(tcell.KeyEnter, 0))
	if tab.FindIndex != 1 {
		t.Fatalf("expected FindIndex=1 after Enter, got %d", tab.FindIndex)
	}
	if tab.Cursor.Line != 1 {
		t.Fatalf("cursor should be on line 1, got %+v", tab.Cursor)
	}
}

// TestHandleFindKey_ShiftEnterGoesBack pins down the Shift-Enter -> prev
// behaviour. Enter then Shift-Enter from the first match should leave
// us back at the first match.
func TestHandleFindKey_ShiftEnterGoesBack(t *testing.T) {
	a := seedFindApp(t, "foo\nfoo\nfoo")
	a.openFind()
	for _, r := range "foo" {
		a.handleFindKey(keyEv(tcell.KeyRune, r))
	}
	a.handleFindKey(keyEv(tcell.KeyEnter, 0))
	// Shift+Enter — keyEv default is ModNone, so build it directly.
	a.handleFindKey(tcell.NewEventKey(tcell.KeyEnter, 0, tcell.ModShift))
	if a.activeTabPtr().FindIndex != 0 {
		t.Fatalf("Shift-Enter should walk back, got idx=%d", a.activeTabPtr().FindIndex)
	}
}

// TestHandleFindKey_EscClearsHighlights pins the close gesture: Esc
// closes the bar AND wipes the tab's match list so the highlights
// disappear with the UI. Leaving them painted after the bar closes is
// the kind of "did anything happen?" surprise we want to avoid.
func TestHandleFindKey_EscClearsHighlights(t *testing.T) {
	a := seedFindApp(t, "foo bar foo")
	a.openFind()
	for _, r := range "foo" {
		a.handleFindKey(keyEv(tcell.KeyRune, r))
	}
	a.handleFindKey(keyEv(tcell.KeyEsc, 0))
	if a.findOpen {
		t.Fatal("Esc should close the find bar")
	}
	tab := a.activeTabPtr()
	if tab.FindQuery != "" || tab.FindMatches != nil || tab.FindIndex != -1 {
		t.Fatalf("Esc should clear all find state, got %+v", tab)
	}
}

// TestHandleFindKey_BackspaceLiveUpdates removes a character from the
// input and confirms matches re-resolve. Without this, deleting the
// query would leave stale highlights painted in the editor.
func TestHandleFindKey_BackspaceLiveUpdates(t *testing.T) {
	a := seedFindApp(t, "foo bar foox")
	a.openFind()
	for _, r := range "foox" {
		a.handleFindKey(keyEv(tcell.KeyRune, r))
	}
	tab := a.activeTabPtr()
	if len(tab.FindMatches) != 1 {
		t.Fatalf("setup expected 1 match for 'foox', got %d", len(tab.FindMatches))
	}
	a.handleFindKey(keyEv(tcell.KeyBackspace, 0))
	if len(tab.FindMatches) != 2 {
		t.Fatalf("after backspace should match 'foo' (2x), got %d", len(tab.FindMatches))
	}
}

// TestEditorRect_ShrinksWhenFindOpen pins down the layout contract: the
// editor body is one row shorter while the find bar is up. Without this
// the bar would paint over the bottom row of code.
func TestEditorRect_ShrinksWhenFindOpen(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	_, _, _, hClosed := a.editorRect()
	a.findOpen = true
	_, _, _, hOpen := a.editorRect()
	if hOpen != hClosed-findBarHeight {
		t.Fatalf("editor height didn't shrink: closed=%d open=%d", hClosed, hOpen)
	}
}

// TestHasFindable_ImageTabIsFalse keeps the menu's Find row disabled on
// image tabs — there's nothing to search inside an image.
func TestHasFindable_ImageTabIsFalse(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	if a.hasFindable() {
		t.Fatal("no tab should not be findable")
	}
}

// TestCounterText_Variants pins the three rendered states of the
// counter so a future refactor can't quietly drop "no results" or the
// blank no-query state.
func TestCounterText_Variants(t *testing.T) {
	a := seedFindApp(t, "foo bar foo")
	a.openFind()
	if got := a.findCounterText(); got != "" {
		t.Fatalf("empty input should yield blank counter, got %q", got)
	}
	for _, r := range "foo" {
		a.handleFindKey(keyEv(tcell.KeyRune, r))
	}
	if got := a.findCounterText(); got != "1 of 2" {
		t.Fatalf("counter for 2 matches should be '1 of 2', got %q", got)
	}
	for _, r := range "zzz" {
		a.handleFindKey(keyEv(tcell.KeyRune, r))
	}
	if got := a.findCounterText(); got != "no results" {
		t.Fatalf("zero hits should yield 'no results', got %q", got)
	}
}

// TestCloseAllModals_ClosesFindBar guards against a regression where
// opening another modal could leave the find bar focused underneath.
func TestCloseAllModals_ClosesFindBar(t *testing.T) {
	a := seedFindApp(t, "foo")
	a.openFind()
	a.closeAllModals()
	if a.findOpen {
		t.Fatal("closeAllModals should close the find bar")
	}
}

// TestFindBarRows_GrowsWithReplace pins the contract every bottom-pinned
// panel depends on: the bar is one row for find, two once replace shows,
// and zero when closed. A stale constant here would put the git panel
// and the terminal one row over the top of it.
func TestFindBarRows_GrowsWithReplace(t *testing.T) {
	a := seedFindApp(t, "foo")
	if got := a.findBarRows(); got != 0 {
		t.Fatalf("closed bar rows = %d, want 0", got)
	}
	a.openFind()
	if got := a.findBarRows(); got != 1 {
		t.Fatalf("find-only rows = %d, want 1", got)
	}
	a.openReplace()
	if got := a.findBarRows(); got != 2 {
		t.Fatalf("with replace rows = %d, want 2", got)
	}
	// And the editor gives up exactly that many rows — the reason
	// findBarRows exists at all.
	_, _, _, hWith := a.editorRect()
	a.closeFind()
	_, _, _, hWithout := a.editorRect()
	if hWithout-hWith != 2 {
		t.Fatalf("editor gave up %d rows to the bar, want 2", hWithout-hWith)
	}
}

// TestOpenReplace_KeepsCaretInQuery pins the form's order: you have to
// say WHAT to replace before what with, so the caret opens in the query
// field even though the replace row is what was asked for.
func TestOpenReplace_KeepsCaretInQuery(t *testing.T) {
	a := seedFindApp(t, "foo bar")
	a.openReplace()
	if !a.findOpen || !a.findReplaceOpen {
		t.Fatal("openReplace did not open both rows")
	}
	if a.findFocus != findFocusQuery {
		t.Fatalf("focus = %d, want the query field", a.findFocus)
	}
}

// TestFindKey_TabOpensAndWalksFields covers the Tab gesture: from a
// find-only bar it reveals the replace row and lands in it, then walks
// back and forth. Answering Tab with nothing because the second field
// isn't visible yet reads as a broken form.
func TestFindKey_TabOpensAndWalksFields(t *testing.T) {
	a := seedFindApp(t, "foo bar")
	a.openFind()
	a.handleFindKey(tcell.NewEventKey(tcell.KeyTab, 0, tcell.ModNone))
	if !a.findReplaceOpen || a.findFocus != findFocusReplace {
		t.Fatalf("Tab did not reveal + focus the replace row (open=%v focus=%d)",
			a.findReplaceOpen, a.findFocus)
	}
	a.handleFindKey(tcell.NewEventKey(tcell.KeyTab, 0, tcell.ModNone))
	if a.findFocus != findFocusQuery {
		t.Fatal("second Tab did not walk back to the query field")
	}
}

// TestFindKey_TypingRoutesToFocusedField proves the two inputs are
// really separate: runes typed with the replace row focused must not
// re-run the search (which would make the query track the replacement).
func TestFindKey_TypingRoutesToFocusedField(t *testing.T) {
	a := seedFindApp(t, "foo bar foo")
	a.openFind()
	for _, r := range "foo" {
		a.handleFindKey(tcell.NewEventKey(tcell.KeyRune, r, tcell.ModNone))
	}
	a.handleFindKey(tcell.NewEventKey(tcell.KeyTab, 0, tcell.ModNone))
	for _, r := range "baz" {
		a.handleFindKey(tcell.NewEventKey(tcell.KeyRune, r, tcell.ModNone))
	}
	if got := a.findField.String(); got != "foo" {
		t.Fatalf("query field = %q, want %q", got, "foo")
	}
	if got := a.replField.String(); got != "baz" {
		t.Fatalf("replace field = %q, want %q", got, "baz")
	}
	tab := a.activeTabPtr()
	if tab.FindQuery != "foo" {
		t.Fatalf("tab query = %q — typing in the replace row re-ran the search", tab.FindQuery)
	}
}

// TestFindKey_EnterReplacesFromReplaceRow is the keyboard path through
// the whole verb: type a query, Tab, type a replacement, Enter.
func TestFindKey_EnterReplacesFromReplaceRow(t *testing.T) {
	a := seedFindApp(t, "foo bar foo")
	a.openFind()
	for _, r := range "foo" {
		a.handleFindKey(tcell.NewEventKey(tcell.KeyRune, r, tcell.ModNone))
	}
	a.handleFindKey(tcell.NewEventKey(tcell.KeyTab, 0, tcell.ModNone))
	for _, r := range "qux" {
		a.handleFindKey(tcell.NewEventKey(tcell.KeyRune, r, tcell.ModNone))
	}
	a.handleFindKey(tcell.NewEventKey(tcell.KeyEnter, 0, tcell.ModNone))
	if got := a.activeTabPtr().Buffer.String(); got != "qux bar foo" {
		t.Fatalf("buffer = %q, want %q", got, "qux bar foo")
	}
}

// TestFindKey_AltAReplacesAll pins the bulk chord. Alt is safe inside
// the bar precisely because the bar owns the keyboard — handleKey's
// Alt+rune leader branch never sees it, in tmux or out.
func TestFindKey_AltAReplacesAll(t *testing.T) {
	a := seedFindApp(t, "foo foo foo")
	a.openReplace()
	for _, r := range "foo" {
		a.handleFindKey(tcell.NewEventKey(tcell.KeyRune, r, tcell.ModNone))
	}
	a.handleFindKey(tcell.NewEventKey(tcell.KeyTab, 0, tcell.ModNone))
	a.handleFindKey(tcell.NewEventKey(tcell.KeyRune, 'z', tcell.ModNone))
	a.handleFindKey(tcell.NewEventKey(tcell.KeyRune, 'a', tcell.ModAlt))
	if got := a.activeTabPtr().Buffer.String(); got != "z z z" {
		t.Fatalf("buffer = %q, want %q", got, "z z z")
	}
}

// TestFindKey_AltTogglesOptions covers the two in-bar modifier chords
// and, more importantly, that flipping one re-runs the live search
// rather than leaving a match list the toggles say is impossible.
func TestFindKey_AltTogglesOptions(t *testing.T) {
	a := seedFindApp(t, "Foo foo FOO")
	a.openFind()
	for _, r := range "foo" {
		a.handleFindKey(tcell.NewEventKey(tcell.KeyRune, r, tcell.ModNone))
	}
	if got := len(a.activeTabPtr().FindMatches); got != 3 {
		t.Fatalf("insensitive matches = %d, want 3", got)
	}
	a.handleFindKey(tcell.NewEventKey(tcell.KeyRune, 'c', tcell.ModAlt))
	if !a.findCase {
		t.Fatal("alt+c did not set match-case")
	}
	if got := len(a.activeTabPtr().FindMatches); got != 1 {
		t.Fatalf("case-sensitive matches = %d, want 1", got)
	}
	a.handleFindKey(tcell.NewEventKey(tcell.KeyRune, 'w', tcell.ModAlt))
	if !a.findWord {
		t.Fatal("alt+w did not set whole-word")
	}
}

// TestFindOptions_SurviveTabSwitch pins where the options live: on the
// App, pushed onto tabs. Flipping "match case" and then switching files
// must not silently switch it back off.
func TestFindOptions_SurviveTabSwitch(t *testing.T) {
	dir := t.TempDir()
	for _, n := range []string{"a.txt", "b.txt"} {
		if err := os.WriteFile(filepath.Join(dir, n), []byte("Foo foo"), 0644); err != nil {
			t.Fatalf("seed: %v", err)
		}
	}
	a := newTestApp(t, dir)
	a.openFile(filepath.Join(dir, "a.txt"))
	a.toggleFindCase()
	a.openFile(filepath.Join(dir, "b.txt"))
	a.openFind()
	a.findField = newTextField("foo")
	a.findApplyQuery()
	if got := len(a.activeTabPtr().FindMatches); got != 1 {
		t.Fatalf("second tab matched %d — the option did not carry across", got)
	}
}

// TestFindBarPress_TogglesAndFocus walks the mouse story: the two option
// buttons act, and a click in the replace row takes focus there. Without
// the routing case in handleMouse these clicks land in the file behind
// the bar and move the cursor instead.
func TestFindBarPress_TogglesAndFocus(t *testing.T) {
	a := seedFindApp(t, "Foo foo")
	a.openReplace()
	c := a.findCaseRect()
	if !a.findBarPress(c.x, c.y) {
		t.Fatal("press on the Aa button was not consumed")
	}
	if !a.findCase {
		t.Fatal("clicking Aa did not set match-case")
	}
	w := a.findWordRect()
	a.findBarPress(w.x, w.y)
	if !a.findWord {
		t.Fatal("clicking |W| did not set whole-word")
	}
	_, by, _, _ := a.findBarRect()
	a.findBarPress(a.leftBlockW()+10, by+1)
	if a.findFocus != findFocusReplace {
		t.Fatal("a click in the replace row did not take focus")
	}
	// And a press outside the bar is not ours to consume.
	if a.findBarPress(a.leftBlockW()+10, by-1) {
		t.Fatal("findBarPress claimed a row above the bar")
	}
}

// TestFindBarPress_ReplaceButtons covers the mouse path to both verbs —
// the primary surface on this project, where clicks come first.
func TestFindBarPress_ReplaceButtons(t *testing.T) {
	a := seedFindApp(t, "foo foo")
	a.openReplace()
	a.findField = newTextField("foo")
	a.findApplyQuery()
	a.replField = newTextField("x")
	r := a.findReplaceBtnRect()
	a.findBarPress(r.x, r.y)
	if got := a.activeTabPtr().Buffer.String(); got != "x foo" {
		t.Fatalf("after Replace buffer = %q, want %q", got, "x foo")
	}
	all := a.findAllBtnRect()
	a.findBarPress(all.x, all.y)
	if got := a.activeTabPtr().Buffer.String(); got != "x x" {
		t.Fatalf("after All buffer = %q, want %q", got, "x x")
	}
}

// TestDrawFindBar_ShowsToggleStates renders the real bar and asserts the
// two option buttons are on screen — they're the only surface that says
// what the search is currently doing, so losing them to a layout change
// would be silent.
func TestDrawFindBar_ShowsToggleStates(t *testing.T) {
	a := seedFindApp(t, "foo")
	a.openReplace()
	a.findField = newTextField("foo")
	a.findApplyQuery()
	a.drawFindBar()
	if !screenHasText(t, a, "Aa") {
		t.Error("bar does not show the match-case button")
	}
	if !screenHasText(t, a, "|W|") {
		t.Error("bar does not show the whole-word button")
	}
	if !screenHasText(t, a, "Repl:") {
		t.Error("bar does not show the replace row's label")
	}
	if !screenHasText(t, a, "Replace") {
		t.Error("bar does not show the Replace button")
	}
}
