// =============================================================================
// File: internal/app/searchhistory_test.go
// Author: Rohan Allison <rohanthewiz@gmail.com>
// Created: 2026-09-29
// Copyright: 2026 Rohan Allison. All rights reserved.
// =============================================================================

// Tests for the search history dropdown: what records a search (and what
// does not), the find bar's dropdown by key and by mouse, the prompts'
// dropdown, the geometry's flip and shrink, and that the lists reach the
// repository's database.

package app

import (
	"reflect"
	"strings"
	"testing"

	"github.com/gdamore/tcell/v2"

	"github.com/rohanthewiz/ced/internal/history"
)

// seedSearches records texts oldest first, so the last one is the head
// of the list.
func seedSearches(a *App, kind string, texts ...string) {
	for _, s := range texts {
		a.recordSearch(kind, s)
	}
}

// TestFindBar_EscRecordsTheQuery pins the commonest search there is —
// type, read the hits, Esc — as one the history keeps.
func TestFindBar_EscRecordsTheQuery(t *testing.T) {
	a := seedFindApp(t, "foo bar")
	a.openFind()
	for _, r := range "bar" {
		a.handleFindKey(keyEv(tcell.KeyRune, r))
	}
	a.handleFindKey(keyEv(tcell.KeyEsc, 0))
	if got := a.searchHistory(history.SearchFind); !reflect.DeepEqual(got, []string{"bar"}) {
		t.Fatalf("find history = %q, want [bar]", got)
	}
}

// TestFindBar_EnterRecordsBeforeAnEdit pins that a query stepped through
// with Enter survives being edited into another one.
func TestFindBar_EnterRecordsBeforeAnEdit(t *testing.T) {
	a := seedFindApp(t, "foo food")
	a.openFind()
	for _, r := range "foo" {
		a.handleFindKey(keyEv(tcell.KeyRune, r))
	}
	a.handleFindKey(keyEv(tcell.KeyEnter, 0))
	a.handleFindKey(keyEv(tcell.KeyRune, 'd'))
	a.closeFind()
	if got := a.searchHistory(history.SearchFind); !reflect.DeepEqual(got, []string{"food", "foo"}) {
		t.Fatalf("find history = %q, want [food foo]", got)
	}
}

// TestCloseAllModals_RecordsTheFindBar pins that a modal displacing the
// bar does not lose its query.
func TestCloseAllModals_RecordsTheFindBar(t *testing.T) {
	a := seedFindApp(t, "foo")
	a.openFind()
	a.findField = newTextField("foo")
	a.closeAllModals()
	if got := a.searchHistory(history.SearchFind); !reflect.DeepEqual(got, []string{"foo"}) {
		t.Fatalf("find history = %q, want [foo]", got)
	}
}

// TestFindBar_ReplaceRecordsBothHalves pins that a replace that changed
// something records the query and the replacement, each in its list,
// and that a replace that did nothing records neither.
func TestFindBar_ReplaceRecordsBothHalves(t *testing.T) {
	a := seedFindApp(t, "a b a")
	a.openReplace()
	a.findField = newTextField("zzz")
	a.findApplyQuery()
	a.replField = newTextField("q")
	a.replaceAll()
	if got := a.searchHistory(history.SearchReplace); len(got) != 0 {
		t.Fatalf("a no-op replace recorded %q", got)
	}
	a.findField = newTextField("a")
	a.findApplyQuery()
	a.replaceAll()
	if got := a.searchHistory(history.SearchReplace); !reflect.DeepEqual(got, []string{"q"}) {
		t.Fatalf("replace history = %q, want [q]", got)
	}
	if got := a.searchHistory(history.SearchFind); len(got) == 0 || got[0] != "a" {
		t.Fatalf("find history = %q, want head a", got)
	}
}

// TestFindBar_UpOpensUpwardAndWalks pins the key path: Up opens the
// dropdown ABOVE the bar on the newest entry, Up walks to older ones,
// and Down past the newest closes it without touching the field.
func TestFindBar_UpOpensUpwardAndWalks(t *testing.T) {
	a := seedFindApp(t, "one two three")
	seedSearches(a, history.SearchFind, "one", "two", "three")
	a.openFind()
	a.handleFindKey(keyEv(tcell.KeyUp, 0))
	if !a.findHist.open || a.findHist.sel != 0 {
		t.Fatalf("Up: open=%v sel=%d, want open on the newest", a.findHist.open, a.findHist.sel)
	}
	g := a.findHistGeom()
	_, by, _, _ := a.findBarRect()
	if !g.up || g.y+g.h != by {
		t.Fatalf("geometry %+v does not sit directly above the bar at row %d", g, by)
	}
	a.handleFindKey(keyEv(tcell.KeyUp, 0))
	if a.findHist.sel != 1 {
		t.Fatalf("second Up: sel=%d, want 1 (older)", a.findHist.sel)
	}
	a.handleFindKey(keyEv(tcell.KeyDown, 0))
	a.handleFindKey(keyEv(tcell.KeyDown, 0))
	if a.findHist.open {
		t.Fatal("Down past the newest entry left the dropdown open")
	}
	if a.findField.String() != "" {
		t.Fatalf("walking the list changed the field to %q", a.findField.String())
	}
}

// TestFindBar_EnterPicksAndSearches pins that a picked query lands in
// the field AND is searched, like typing it.
func TestFindBar_EnterPicksAndSearches(t *testing.T) {
	a := seedFindApp(t, "one two two")
	seedSearches(a, history.SearchFind, "two", "one")
	a.openFind()
	a.handleFindKey(keyEv(tcell.KeyUp, 0))
	a.handleFindKey(keyEv(tcell.KeyUp, 0)) // "two"
	a.handleFindKey(keyEv(tcell.KeyEnter, 0))
	if a.findHist.open {
		t.Fatal("Enter left the dropdown open")
	}
	if got := a.findField.String(); got != "two" {
		t.Fatalf("field = %q, want two", got)
	}
	if n := len(a.activeTabPtr().FindMatches); n != 2 {
		t.Fatalf("picked query found %d matches, want 2", n)
	}
}

// TestFindBar_OtherKeysCloseAndType pins that typing with the list open
// dismisses it and still types.
func TestFindBar_OtherKeysCloseAndType(t *testing.T) {
	a := seedFindApp(t, "x")
	seedSearches(a, history.SearchFind, "x")
	a.openFind()
	a.handleFindKey(keyEv(tcell.KeyUp, 0))
	a.handleFindKey(keyEv(tcell.KeyRune, 'x'))
	if a.findHist.open || a.findField.String() != "x" {
		t.Fatalf("open=%v field=%q, want closed and typed", a.findHist.open, a.findField.String())
	}
}

// TestFindBar_EscClosesOnlyTheList pins that Esc with the list open
// leaves the bar (and its query) alone.
func TestFindBar_EscClosesOnlyTheList(t *testing.T) {
	a := seedFindApp(t, "x")
	seedSearches(a, history.SearchFind, "x")
	a.openFind()
	a.findField = newTextField("q")
	a.handleFindKey(keyEv(tcell.KeyUp, 0))
	a.handleFindKey(keyEv(tcell.KeyEsc, 0))
	if a.findHist.open || !a.findOpen || a.findField.String() != "q" {
		t.Fatalf("list=%v bar=%v field=%q", a.findHist.open, a.findOpen, a.findField.String())
	}
}

// TestFindBar_EmptyHistoryExplains pins the unavailable-control rule:
// no box opens, and the status bar says why.
func TestFindBar_EmptyHistoryExplains(t *testing.T) {
	a := seedFindApp(t, "x")
	a.openFind()
	a.handleFindKey(keyEv(tcell.KeyUp, 0))
	if a.findHist.open {
		t.Fatal("an empty history opened a box")
	}
	if !strings.Contains(a.statusMsg, "No recent searches") {
		t.Fatalf("flash = %q", a.statusMsg)
	}
}

// TestFindBar_DeleteForgets pins that Delete drops the highlighted entry
// from the list and from the repository's history.
func TestFindBar_DeleteForgets(t *testing.T) {
	a := seedFindApp(t, "x")
	seedSearches(a, history.SearchFind, "keep", "drop")
	a.openFind()
	a.handleFindKey(keyEv(tcell.KeyUp, 0))
	a.handleFindKey(keyEv(tcell.KeyDelete, 0))
	if !reflect.DeepEqual(a.findHist.items, []string{"keep"}) {
		t.Fatalf("dropdown items = %q", a.findHist.items)
	}
	if got := a.searchHistory(history.SearchFind); !reflect.DeepEqual(got, []string{"keep"}) {
		t.Fatalf("history = %q", got)
	}
}

// TestFindBar_LabelClickOpensTheRowsList pins the mouse path: the Repl▾
// label opens the REPLACEMENT list over the replace row and focuses it,
// and a click on a row fills that field.
func TestFindBar_LabelClickOpensTheRowsList(t *testing.T) {
	a := seedFindApp(t, "x")
	seedSearches(a, history.SearchFind, "find-me")
	seedSearches(a, history.SearchReplace, "with-this")
	a.openReplace()
	b := a.findHistBtnRect(findFocusReplace)
	clickAt(a, b.x+1, b.y)
	if !a.findHist.open || a.findHist.kind != history.SearchReplace || a.findFocus != findFocusReplace {
		t.Fatalf("open=%v kind=%q focus=%d", a.findHist.open, a.findHist.kind, a.findFocus)
	}
	g := a.findHistGeom()
	if _, by, _, _ := a.findBarRect(); g.y+g.h != by {
		t.Fatalf("replacement list %+v does not sit above the whole bar (row %d)", g, by)
	}
	pressAt(a, g.x+2, g.y+g.h-2) // the newest row, nearest the bar
	if a.findHist.open || a.replField.String() != "with-this" {
		t.Fatalf("after row click: open=%v repl=%q", a.findHist.open, a.replField.String())
	}
	if a.findField.String() != "" {
		t.Fatalf("a replace pick touched the query: %q", a.findField.String())
	}
}

// TestFindBar_ForgetButton pins the row's × as the mouse twin of Delete.
func TestFindBar_ForgetButton(t *testing.T) {
	a := seedFindApp(t, "x")
	seedSearches(a, history.SearchFind, "a", "b")
	a.openFind()
	a.openFindHist(findFocusQuery)
	g := a.findHistGeom()
	pressAt(a, g.x+g.w-3, g.y+g.h-2) // × on the newest row ("b")
	if !a.findHist.open || !reflect.DeepEqual(a.findHist.items, []string{"a"}) {
		t.Fatalf("after ×: open=%v items=%q", a.findHist.open, a.findHist.items)
	}
}

// TestFindBar_OutsideClickClosesAndFallsThrough pins that a press away
// from the list closes it without being swallowed.
func TestFindBar_OutsideClickClosesAndFallsThrough(t *testing.T) {
	a := seedFindApp(t, "x")
	seedSearches(a, history.SearchFind, "a")
	a.openFind()
	a.openFindHist(findFocusQuery)
	g := a.findHistGeom()
	if a.findHistMouse(g.x+g.w+2, g.y, tcell.Button1) {
		t.Fatal("an outside press was claimed")
	}
	if a.findHist.open {
		t.Fatal("an outside press left the list open")
	}
}

// TestDrawFindHist_PaintsTheList pins that the drawn frame shows the
// list's name and its entries.
func TestDrawFindHist_PaintsTheList(t *testing.T) {
	a := seedFindApp(t, "x")
	seedSearches(a, history.SearchFind, "alpha-query", "beta-query")
	a.openFind()
	a.openFindHist(findFocusQuery)
	a.draw()
	for _, want := range []string{"recent searches", "alpha-query", "beta-query", "Find▾"} {
		if !screenHasText(t, a, want) {
			t.Errorf("screen lacks %q", want)
		}
	}
}

// TestHistDropGeom_FlipsAndShrinks pins the layout rules: the preferred
// side when it fits, the other when it has more room, fewer rows when
// neither holds them all, nothing when there is no room at all.
func TestHistDropGeom_FlipsAndShrinks(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	a.width, a.height = 80, 24
	d := &histDrop{open: true, items: []string{"a", "b", "c"}}

	d.preferUp = true
	if g := a.histDropGeom(d, btnRect{x: 10, y: 20, w: 20}); !g.up || g.y+g.h != 20 || g.rows != 3 {
		t.Fatalf("up with room: %+v", g)
	}
	if g := a.histDropGeom(d, btnRect{x: 10, y: 2, w: 20}); g.up || g.y != 3 {
		t.Fatalf("up without room should flip down: %+v", g)
	}
	d.preferUp = false
	if g := a.histDropGeom(d, btnRect{x: 10, y: 21, w: 20}); !g.up {
		t.Fatalf("down without room should flip up: %+v", g)
	}
	a.height = 8
	if g := a.histDropGeom(d, btnRect{x: 10, y: 2, w: 20}); g.rows != 2 {
		t.Fatalf("cramped: rows=%d, want 2", g.rows)
	}
	a.height = 3
	if g := a.histDropGeom(d, btnRect{x: 10, y: 1, w: 20}); g.ok() {
		t.Fatalf("no room should draw nothing: %+v", g)
	}
	if g := a.histDropGeom(d, btnRect{x: 75, y: 1, w: 20}); g.ok() && g.x+g.w > a.width {
		t.Fatalf("box runs off the right edge: %+v", g)
	}
}

// TestHistDrop_ScrollsToTheSelection pins that walking past the visible
// rows scrolls the list rather than losing the highlight.
func TestHistDrop_ScrollsToTheSelection(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	var items []string
	for i := 0; i < histDropRows+4; i++ {
		items = append(items, strings.Repeat("x", i+1))
	}
	d := &histDrop{open: true, items: items}
	g := a.histDropGeom(d, btnRect{x: 5, y: 5, w: 20})
	for i := 0; i < histDropRows+2; i++ {
		d.step(g, 1)
	}
	if d.sel != histDropRows+2 || d.sel < d.scroll || d.sel >= d.scroll+g.rows {
		t.Fatalf("sel=%d scroll=%d rows=%d", d.sel, d.scroll, g.rows)
	}
}

// TestSearchPrompt_UpPicksWithoutSubmitting pins the prompt path: Up
// opens the list BELOW the field, Enter fills the field and leaves the
// prompt open, and a second Enter submits what was picked.
func TestSearchPrompt_UpPicksWithoutSubmitting(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	seedSearches(a, history.SearchFind, "older", "newer")
	var got string
	a.openSearchPrompt("Find", "h", "", history.SearchFind, func(_ *App, v string) { got = v })
	m := a.modal.(*promptModal)
	m.handleKey(a, keyEv(tcell.KeyUp, 0))
	if g := m.histGeom(a); !m.hist.open || g.up {
		t.Fatalf("open=%v geom=%+v, want open below", m.hist.open, g)
	}
	m.handleKey(a, keyEv(tcell.KeyDown, 0)) // older
	m.handleKey(a, keyEv(tcell.KeyEnter, 0))
	if a.modal == nil || m.field.String() != "older" || got != "" {
		t.Fatalf("modal=%v field=%q submitted=%q", a.modal != nil, m.field.String(), got)
	}
	m.handleKey(a, keyEv(tcell.KeyEnter, 0))
	if got != "older" {
		t.Fatalf("second Enter submitted %q", got)
	}
}

// TestSearchPrompt_ButtonAndRowClick pins the mouse path: the ▾ opens
// the list, and a click on a row that hangs past the prompt's box picks
// it rather than cancelling the prompt.
func TestSearchPrompt_ButtonAndRowClick(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	seedSearches(a, history.SearchSymbol, "Handler", "Server", "Client", "Router", "Store", "Cache")
	a.openSearchPrompt("Sym", "h", "", history.SearchSymbol, func(*App, string) {})
	m := a.modal.(*promptModal)
	b := m.histBtnRect(a)
	clickAt(a, b.x, b.y)
	if !m.hist.open {
		t.Fatal("▾ did not open the list")
	}
	g := m.histGeom(a)
	_, my, _, mh := m.rect(a)
	lastRow := g.y + g.rows
	if lastRow < my+mh {
		t.Fatalf("test needs a row outside the prompt: list ends %d, prompt %d", lastRow, my+mh)
	}
	i := m.hist.itemAtRow(g, g.rows-1)
	want := m.hist.items[i]
	pressAt(a, g.x+2, lastRow)
	if a.modal == nil {
		t.Fatal("a click on a list row outside the box cancelled the prompt")
	}
	if m.field.String() != want {
		t.Fatalf("field = %q, want %q", m.field.String(), want)
	}
}

// TestPlainPrompt_HasNoHistory pins that an ordinary prompt is unchanged:
// no ▾, and Up is not a history key.
func TestPlainPrompt_HasNoHistory(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	seedSearches(a, history.SearchFind, "x")
	a.openPrompt("T", "h", "", nil)
	m := a.modal.(*promptModal)
	m.handleKey(a, keyEv(tcell.KeyUp, 0))
	if m.hist.open || m.histBtnRect(a).w != 0 {
		t.Fatal("a plain prompt grew a history dropdown")
	}
}

// TestFindVerbs_OfferTheirHistory pins which list each find prompt
// offers.
func TestFindVerbs_OfferTheirHistory(t *testing.T) {
	a := seedFindApp(t, "x")
	a.openFindAll()
	if m, ok := a.modal.(*promptModal); !ok || m.histKind != history.SearchFind {
		t.Fatalf("Find all prompt: %#v", a.modal)
	}
	a.closeModal()
	a.menuFindInProject()
	if m, ok := a.modal.(*promptModal); !ok || m.histKind != history.SearchFind {
		t.Fatalf("Find in project prompt: %#v", a.modal)
	}
}

// TestShowFindAll_RecordsEvenAMiss pins that the in-file list remembers
// its query whichever door it came through, hits or not.
func TestShowFindAll_RecordsEvenAMiss(t *testing.T) {
	a := seedFindApp(t, "abc")
	a.showFindAll("zzz")
	a.showFindAll("abc")
	if got := a.searchHistory(history.SearchFind); !reflect.DeepEqual(got, []string{"abc", "zzz"}) {
		t.Fatalf("find history = %q", got)
	}
}

// TestSearchHistory_ReachesTheDatabase pins persistence: what was
// recorded is written by writeHistory and loads back.
func TestSearchHistory_ReachesTheDatabase(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	seedSearches(a, history.SearchFind, "persisted")
	a.writeHistory()
	h, err := history.Load(a.rootDir, historyPathFn(a.rootDir))
	if err != nil {
		t.Fatal(err)
	}
	if got := h.Searches(history.SearchFind); !reflect.DeepEqual(got, []string{"persisted"}) {
		t.Fatalf("loaded = %q", got)
	}
}
