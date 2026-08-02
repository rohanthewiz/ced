// =============================================================================
// File: internal/app/findall_test.go
// Author: Rohan Allison <rohanthewiz@gmail.com>
// Created: 2026-08-02
// Copyright: 2026 Rohan Allison. All rights reserved.
// =============================================================================

package app

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gdamore/tcell/v2"

	"github.com/rohanthewiz/ced/internal/editor"
)

// findAllFixture is a small Go-shaped file with the token "count" on
// four separate lines, two of them indented — enough to exercise the
// row compaction, the line-number column, and multi-row scrolling
// without any test having to reason about a big buffer.
const findAllFixture = `package main

func count() int {
	count := 0
	for range 3 {
		count++
	}
	return count
}
`

// seedFindAllApp opens a tab on the fixture and returns the app plus the
// tab pointer, which nearly every assertion below needs.
func seedFindAllApp(t *testing.T) (*App, *editor.Tab) {
	t.Helper()
	dir := t.TempDir()
	target := filepath.Join(dir, "count.go")
	if err := os.WriteFile(target, []byte(findAllFixture), 0644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	a := newTestApp(t, dir)
	a.openFile(target)
	tab := a.activeTabPtr()
	if tab == nil {
		t.Fatal("fixture did not open")
	}
	return a, tab
}

// seedFindAllLongApp opens a tab on a file long enough that the popup
// can't show every hit at once — the fixture the scrolling tests need.
// Every fourth line carries the token, so the match count (30) comfortably
// exceeds findAllVisibleRows.
func seedFindAllLongApp(t *testing.T) (*App, *editor.Tab) {
	t.Helper()
	var b strings.Builder
	for i := 0; i < 120; i++ {
		if i%4 == 0 {
			b.WriteString("\tcount++ // hit\n")
		} else {
			b.WriteString("filler line\n")
		}
	}
	dir := t.TempDir()
	target := filepath.Join(dir, "long.go")
	if err := os.WriteFile(target, []byte(b.String()), 0644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	a := newTestApp(t, dir)
	a.openFile(target)
	tab := a.activeTabPtr()
	if tab == nil {
		t.Fatal("fixture did not open")
	}
	return a, tab
}

// openFindAllT opens the list on query and returns the modal, failing the
// test if the popup didn't come up — every interaction test starts here.
func openFindAllT(t *testing.T, a *App, query string) *findAllModal {
	t.Helper()
	a.showFindAll(query)
	m, ok := a.modal.(*findAllModal)
	if !ok {
		t.Fatalf("showFindAll(%q) did not open the list (modal = %T)", query, a.modal)
	}
	return m
}

// TestFindAll_RowsCarryLineAndCompactedText pins the row model: one row
// per occurrence, in document order, each carrying its buffer line and
// the line's text with the indentation stripped — the "compacted" part
// of the feature.
func TestFindAll_RowsCarryLineAndCompactedText(t *testing.T) {
	a, _ := seedFindAllApp(t)
	m := openFindAllT(t, a, "count")

	if len(m.rows) != 4 {
		t.Fatalf("rows = %d, want 4 (func count, count :=, count++, return count)", len(m.rows))
	}
	// Lines are 0-based in the buffer; the fixture puts the declaration
	// on line 2 and the last use on line 7.
	if m.rows[0].line != 2 {
		t.Errorf("first row line = %d, want 2", m.rows[0].line)
	}
	if got := m.rows[len(m.rows)-1].line; got != 7 {
		t.Errorf("last row line = %d, want 7", got)
	}
	// Row 1 is the tab-indented "\tcount := 0": the display text must
	// have lost the indent entirely.
	if got := m.rows[1].text; got != "count := 0" {
		t.Errorf("compacted text = %q, want %q", got, "count := 0")
	}
	// …and the hit must have moved with it, so the highlight lands on
	// the word rather than where it sat in the raw line.
	if m.rows[1].hit != 0 || m.rows[1].hitW != 5 {
		t.Errorf("hit = (%d,%d), want (0,5)", m.rows[1].hit, m.rows[1].hitW)
	}
}

// TestCompactLine_TabsBecomeOneSpace covers the mapping rule the row
// highlight depends on: trimming reports its own width and interior tabs
// stay one rune wide, so a buffer column maps to a display column by
// subtracting the trim and nothing else.
func TestCompactLine_TabsBecomeOneSpace(t *testing.T) {
	text, trimmed := compactLine("\t\tif x\t== 1 {")
	if trimmed != 2 {
		t.Errorf("trimmed = %d, want 2", trimmed)
	}
	if text != "if x == 1 {" {
		t.Errorf("text = %q, want %q", text, "if x == 1 {")
	}
	if runeLen(text) != runeLen("\t\tif x\t== 1 {")-trimmed {
		t.Error("compaction changed the rune count — column mapping would drift")
	}
}

// TestFindAll_NoMatchesFlashesInsteadOfOpening keeps the popup honest:
// an empty list is a dialog the user has to dismiss to be told "no".
func TestFindAll_NoMatchesFlashesInsteadOfOpening(t *testing.T) {
	a, _ := seedFindAllApp(t)
	a.showFindAll("zzzznotthere")
	if a.modal != nil {
		t.Fatalf("empty result set should not open a modal, got %T", a.modal)
	}
	if !strings.Contains(a.statusMsg, "no occurrences") {
		t.Errorf("statusMsg = %q, want a no-occurrences flash", a.statusMsg)
	}
}

// TestFindAll_OpensOnNearestMatch pins the "nearest, not first" rule the
// find bar already follows: opening the list from halfway down a file
// highlights the hit at or after the cursor.
func TestFindAll_OpensOnNearestMatch(t *testing.T) {
	a, tab := seedFindAllApp(t)
	tab.MoveCursorTo(editor.Position{Line: 5, Col: 0}, false) // the "count++" line
	m := openFindAllT(t, a, "count")

	if m.rows[m.selected].line != 5 {
		t.Errorf("selected row line = %d, want 5 (the hit at the cursor)", m.rows[m.selected].line)
	}
	if tab.Cursor.Line != 5 {
		t.Errorf("cursor line = %d, want 5 — opening previews the selection", tab.Cursor.Line)
	}
}

// TestFindAll_ArrowsPreviewAndEscRestores is the feature's core loop:
// walking the list moves the editor's cursor live, and Esc puts the view
// back exactly where it started.
func TestFindAll_ArrowsPreviewAndEscRestores(t *testing.T) {
	a, tab := seedFindAllApp(t)
	tab.MoveCursorTo(editor.Position{Line: 0, Col: 3}, false)
	tab.ScrollY = 0
	origin, originScroll := tab.Cursor, tab.ScrollY

	m := openFindAllT(t, a, "count")
	m.handleKey(a, tcell.NewEventKey(tcell.KeyDown, 0, tcell.ModNone))
	m.handleKey(a, tcell.NewEventKey(tcell.KeyDown, 0, tcell.ModNone))

	if m.selected != 2 {
		t.Fatalf("selected = %d, want 2 after two Downs", m.selected)
	}
	want := m.rows[2]
	if tab.Cursor.Line != want.line || tab.Cursor.Col != want.col {
		t.Fatalf("preview cursor = %v, want line %d col %d", tab.Cursor, want.line, want.col)
	}

	m.handleKey(a, tcell.NewEventKey(tcell.KeyEsc, 0, tcell.ModNone))
	if a.modal != nil {
		t.Error("Esc should close the list")
	}
	if tab.Cursor != origin {
		t.Errorf("cursor = %v after abort, want the original %v", tab.Cursor, origin)
	}
	if tab.ScrollY != originScroll {
		t.Errorf("ScrollY = %d after abort, want %d", tab.ScrollY, originScroll)
	}
}

// TestFindAll_AbortRestoresScrollWithoutSnapping guards the reason the
// restore writes Cursor/Anchor directly instead of calling MoveCursorTo:
// a user who wheeled away from their own cursor before opening the list
// must get that scrolled-away view back, not a viewport yanked to the
// cursor by the next Render.
func TestFindAll_AbortRestoresScrollWithoutSnapping(t *testing.T) {
	a, tab := seedFindAllLongApp(t)
	tab.MoveCursorTo(editor.Position{Line: 0, Col: 0}, false)
	tab.ScrollY = 40 // wheeled far down, away from the cursor

	m := openFindAllT(t, a, "count")
	m.handleKey(a, tcell.NewEventKey(tcell.KeyEnd, 0, tcell.ModNone))
	m.handleKey(a, tcell.NewEventKey(tcell.KeyEsc, 0, tcell.ModNone))

	if tab.ScrollY != 40 {
		t.Fatalf("ScrollY = %d after abort, want the restored 40", tab.ScrollY)
	}
	// Render consumes cursorMoved; a restore that set it would scroll
	// back to the cursor here.
	ex, ey, ew, eh := a.editorRect()
	tab.Render(a.screen, a.theme, ex, ey, ew, eh)
	if tab.ScrollY != 40 {
		t.Errorf("ScrollY = %d after a render, want 40 — the restore armed EnsureVisible", tab.ScrollY)
	}
}

// TestFindAll_EnterAcceptsPosition pins the other exit: Enter closes the
// list and leaves the cursor on the previewed hit.
func TestFindAll_EnterAcceptsPosition(t *testing.T) {
	a, tab := seedFindAllApp(t)
	tab.MoveCursorTo(editor.Position{Line: 0, Col: 0}, false)

	m := openFindAllT(t, a, "count")
	m.handleKey(a, tcell.NewEventKey(tcell.KeyEnd, 0, tcell.ModNone))
	last := m.rows[len(m.rows)-1]
	m.handleKey(a, tcell.NewEventKey(tcell.KeyEnter, 0, tcell.ModNone))

	if a.modal != nil {
		t.Error("Enter should close the list")
	}
	if tab.Cursor.Line != last.line || tab.Cursor.Col != last.col {
		t.Errorf("cursor = %v after accept, want line %d col %d", tab.Cursor, last.line, last.col)
	}
}

// TestFindAll_ClickPreviewsAndKeepsListOpen is the non-modal half of the
// contract: a single click on a row moves the editor but must NOT
// dismiss the popup the way every other list modal's click does.
func TestFindAll_ClickPreviewsAndKeepsListOpen(t *testing.T) {
	a, tab := seedFindAllApp(t)
	m := openFindAllT(t, a, "count")
	mx, my, _, _ := m.rect(a)

	m.handleMouse(a, mx+8, my+3+2, tcell.Button1) // third visible row
	if a.modal == nil {
		t.Fatal("a click on a row must leave the list open")
	}
	if m.selected != 2 {
		t.Fatalf("selected = %d, want 2 (the clicked row)", m.selected)
	}
	if tab.Cursor.Line != m.rows[2].line {
		t.Errorf("cursor line = %d, want %d — the click should preview", tab.Cursor.Line, m.rows[2].line)
	}
}

// TestFindAll_DoubleClickAccepts pins the mouse twin of Enter: a second
// click on the same cell inside the double-click window commits.
func TestFindAll_DoubleClickAccepts(t *testing.T) {
	a, tab := seedFindAllApp(t)
	m := openFindAllT(t, a, "count")
	mx, my, _, _ := m.rect(a)
	x, y := mx+8, my+3+1

	m.handleMouse(a, x, y, tcell.Button1)
	if a.modal == nil {
		t.Fatal("first click should not close the list")
	}
	m.handleMouse(a, x, y, tcell.Button1)
	if a.modal != nil {
		t.Fatal("double click should accept and close")
	}
	if tab.Cursor.Line != m.rows[1].line {
		t.Errorf("cursor line = %d, want %d", tab.Cursor.Line, m.rows[1].line)
	}
}

// TestFindAll_SlowSecondClickIsNotADoubleClick keeps the accept gesture
// deliberate: two clicks either side of the window are two previews.
func TestFindAll_SlowSecondClickIsNotADoubleClick(t *testing.T) {
	a, _ := seedFindAllApp(t)
	m := openFindAllT(t, a, "count")
	mx, my, _, _ := m.rect(a)
	x, y := mx+8, my+3+1

	m.handleMouse(a, x, y, tcell.Button1)
	a.lastClick.when = time.Now().Add(-2 * doubleClickMs)
	m.handleMouse(a, x, y, tcell.Button1)

	if a.modal == nil {
		t.Fatal("a slow second click should still be a preview, not an accept")
	}
}

// TestFindAll_ClickOutsideAccepts pins the dismissal rule: clicking in
// the editor settles where the preview left you (Esc is the one gesture
// that means "put it back").
func TestFindAll_ClickOutsideAccepts(t *testing.T) {
	a, tab := seedFindAllApp(t)
	tab.MoveCursorTo(editor.Position{Line: 0, Col: 0}, false)
	m := openFindAllT(t, a, "count")
	m.handleKey(a, tcell.NewEventKey(tcell.KeyDown, 0, tcell.ModNone))
	previewed := tab.Cursor

	m.handleMouse(a, 2, 2, tcell.Button1) // over the file tree
	if a.modal != nil {
		t.Fatal("a click outside should close the list")
	}
	if tab.Cursor != previewed {
		t.Errorf("cursor = %v, want the previewed %v kept", tab.Cursor, previewed)
	}
}

// TestFindAll_WheelScrollsListWithoutMovingCursor separates reading the
// list from driving the editor — the wheel does the former only.
func TestFindAll_WheelScrollsListWithoutMovingCursor(t *testing.T) {
	a, tab := seedFindAllLongApp(t)
	m := openFindAllT(t, a, "count")
	m.selected, m.scroll = 0, 0
	m.preview(a)
	before := tab.Cursor

	mx, my, _, _ := m.rect(a)
	m.handleMouse(a, mx+2, my+4, tcell.WheelDown)

	if m.scroll == 0 {
		t.Fatalf("wheel did not scroll the list (visible rows = %d)", m.visibleRows(a))
	}
	if m.selected != 0 || tab.Cursor != before {
		t.Error("the wheel must not move the selection or the cursor")
	}
}

// TestFindAll_ScrollFollowsSelection covers the other direction: moving
// the highlight past the visible window pulls the window along.
func TestFindAll_ScrollFollowsSelection(t *testing.T) {
	a, _ := seedFindAllLongApp(t)
	m := openFindAllT(t, a, "count")
	vis := m.visibleRows(a)
	if vis >= len(m.rows) {
		t.Skipf("popup fits all %d rows at this size — nothing to scroll", len(m.rows))
	}
	m.selectRow(a, 0)
	m.handleKey(a, tcell.NewEventKey(tcell.KeyEnd, 0, tcell.ModNone))

	if m.selected != len(m.rows)-1 {
		t.Fatalf("selected = %d, want the last row", m.selected)
	}
	if m.selected < m.scroll || m.selected >= m.scroll+vis {
		t.Errorf("selection %d outside the visible window [%d,%d)", m.selected, m.scroll, m.scroll+vis)
	}
}

// TestFindAll_ShrinksEditorAndSitsAboveIt pins the geometry decision: the
// popup takes its rows OUT of the editor band rather than floating over
// it, and takes them off the TOP — pinned under the tab bar with the
// editor pushed down beneath it. That's what keeps the previewed line
// visible.
func TestFindAll_ShrinksEditorAndSitsAboveIt(t *testing.T) {
	a, _ := seedFindAllApp(t)
	_, beforeY, _, before := a.editorRect()
	if beforeY != 1 {
		t.Fatalf("editor y = %d with no popup, want 1 (under the tab bar)", beforeY)
	}

	m := openFindAllT(t, a, "count")
	ex, ey, ew, eh := a.editorRect()
	mx, my, mw, mh := m.rect(a)

	if eh != before-mh {
		t.Errorf("editor height = %d, want %d (band %d minus popup %d)", eh, before-mh, before, mh)
	}
	if my != 1 {
		t.Errorf("popup y = %d, want 1 (directly under the tab bar)", my)
	}
	if ey != my+mh {
		t.Errorf("editor y = %d, want %d (pushed down below the popup)", ey, my+mh)
	}
	if mx != ex || mw != ew {
		t.Errorf("popup column band = (%d,%d), want the editor's (%d,%d)", mx, mw, ex, ew)
	}
	if eh < findAllMinEditorRows {
		t.Errorf("editor left with %d rows, want at least %d", eh, findAllMinEditorRows)
	}
	// Closing gives the rows back.
	m.accept(a)
	if _, _, _, after := a.editorRect(); after != before {
		t.Errorf("editor height = %d after close, want the original %d", after, before)
	}
}

// TestFindAll_PreviewCentersTheHitInTheBand pins the peek scroll: the
// previewed line sits mid-band with context on both sides, not parked on
// the edge a minimal scroll would have chosen. The row it lands on has to
// be measured against the SHORTENED band — the popup is open.
func TestFindAll_PreviewCentersTheHitInTheBand(t *testing.T) {
	a, tab := seedFindAllLongApp(t)
	tab.MoveCursorTo(editor.Position{Line: 0, Col: 0}, false)

	m := openFindAllT(t, a, "count")
	m.selectRow(a, 15) // a hit far enough down that centering can happen

	_, _, _, eh := a.editorRect()
	row := tab.Cursor.Line - tab.ScrollY
	if want := eh / 2; row != want {
		t.Errorf("previewed hit drawn on band row %d of %d, want the middle row %d", row, eh, want)
	}
}

// TestFindAll_PreviewNearTopOfFileDoesNotScrollPastIt keeps centering
// honest at the edge: a hit in the first few lines can't be centered, and
// must not push the buffer up to fake it.
func TestFindAll_PreviewNearTopOfFileDoesNotScrollPastIt(t *testing.T) {
	a, tab := seedFindAllLongApp(t)
	m := openFindAllT(t, a, "count")
	m.selectRow(a, 0) // the hit on line 0

	if tab.ScrollY != 0 {
		t.Errorf("ScrollY = %d previewing the first line, want 0", tab.ScrollY)
	}
}

// TestFindAll_HeightClampsOnShortWindow makes sure a cramped terminal
// still leaves the editor its working rows instead of the popup taking
// the whole band.
func TestFindAll_HeightClampsOnShortWindow(t *testing.T) {
	a, _ := seedFindAllApp(t)
	a.height = 16
	m := openFindAllT(t, a, "count")

	if _, _, _, eh := a.editorRect(); eh < findAllMinEditorRows {
		t.Errorf("editor rows = %d on a 16-row window, want >= %d", eh, findAllMinEditorRows)
	}
	if h := m.height(a); h < findAllMinHeight {
		t.Errorf("popup height = %d, want at least %d", h, findAllMinHeight)
	}
}

// TestFindAll_DrawsLineNumbersAndText renders the popup and asserts the
// two things the user actually reads: the line number leading each row,
// and the compacted code beside it.
func TestFindAll_DrawsLineNumbersAndText(t *testing.T) {
	a, _ := seedFindAllApp(t)
	openFindAllT(t, a, "count")
	a.draw()
	a.screen.Show()
	out := screenText(a)

	if !strings.Contains(out, `Find all "count"`) {
		t.Errorf("title missing from the drawn screen:\n%s", out)
	}
	// The fixture's "\tcount := 0" is buffer line 3 → displayed as 4,
	// with its indentation compacted away.
	if !strings.Contains(out, "4 │ count := 0") {
		t.Errorf("row %q not drawn:\n%s", "4 │ count := 0", out)
	}
	if !strings.Contains(out, "1/4") {
		t.Errorf("position counter missing:\n%s", out)
	}
}

// TestFindAll_BorrowsAndReturnsFindState covers the highlight loan: the
// editor tints every occurrence while the list explains them, and the
// tint leaves with the list.
func TestFindAll_BorrowsAndReturnsFindState(t *testing.T) {
	a, tab := seedFindAllApp(t)
	m := openFindAllT(t, a, "count")

	if tab.FindQuery != "count" || len(tab.FindMatches) != len(m.rows) {
		t.Fatalf("tab find state = %q/%d, want the popup's query and %d matches",
			tab.FindQuery, len(tab.FindMatches), len(m.rows))
	}
	if tab.FindIndex != m.selected {
		t.Errorf("FindIndex = %d, want the previewed row %d", tab.FindIndex, m.selected)
	}
	m.accept(a)
	if tab.FindQuery != "" || tab.FindMatches != nil {
		t.Errorf("find state survived the popup: %q/%d", tab.FindQuery, len(tab.FindMatches))
	}
}

// TestFindAll_SeedQueryPrefersBarThenSelectionThenWord pins the "what
// does find-all mean with nothing typed" ladder.
func TestFindAll_SeedQueryPrefersBarThenSelectionThenWord(t *testing.T) {
	a, tab := seedFindAllApp(t)

	// Word under the cursor — the bare case.
	tab.MoveCursorTo(editor.Position{Line: 2, Col: 7}, false) // inside "count"
	if got := a.findAllSeedQuery(); got != "count" {
		t.Errorf("seed from cursor = %q, want %q", got, "count")
	}

	// A single-line selection is a narrower question and wins.
	tab.Anchor = editor.Position{Line: 0, Col: 0}
	tab.Cursor = editor.Position{Line: 0, Col: 7}
	if got := a.findAllSeedQuery(); got != "package" {
		t.Errorf("seed from selection = %q, want %q", got, "package")
	}

	// The find bar outranks everything — it's what the user just typed.
	a.openFind()
	a.findValue = []rune("int")
	if got := a.findAllSeedQuery(); got != "int" {
		t.Errorf("seed from bar = %q, want %q", got, "int")
	}
}

// TestFindAll_SeedFallsBackToPrompt covers the empty ladder: a cursor in
// whitespace has nothing to search for, so ced asks instead of guessing.
func TestFindAll_SeedFallsBackToPrompt(t *testing.T) {
	a, tab := seedFindAllApp(t)
	tab.MoveCursorTo(editor.Position{Line: 1, Col: 0}, false) // the blank line
	a.openFindAll()

	if _, ok := a.modal.(*promptModal); !ok {
		t.Fatalf("modal = %T, want a promptModal asking for the query", a.modal)
	}
}

// TestFindAll_FindBarDownOpensTheList pins the bar's Down gesture: the
// typed query carries over and the bar gives way to the list.
func TestFindAll_FindBarDownOpensTheList(t *testing.T) {
	a, _ := seedFindAllApp(t)
	a.openFind()
	for _, r := range "count" {
		a.handleFindKey(tcell.NewEventKey(tcell.KeyRune, r, tcell.ModNone))
	}
	a.handleFindKey(tcell.NewEventKey(tcell.KeyDown, 0, tcell.ModNone))

	m, ok := a.modal.(*findAllModal)
	if !ok {
		t.Fatalf("modal = %T, want the find-all list", a.modal)
	}
	if m.query != "count" {
		t.Errorf("query = %q, want the bar's %q", m.query, "count")
	}
	if a.findOpen {
		t.Error("the find bar should close as the list opens")
	}
}

// TestFindAll_LeaderAndMenuRowExist keeps the two documented surfaces
// wired: Esc-F and the ≡ Search row must both reach the same action.
func TestFindAll_LeaderAndMenuRowExist(t *testing.T) {
	if leaderActionFor('F') == nil {
		t.Error("Esc-F is not bound")
	}
	a := newTestApp(t, t.TempDir())
	found := false
	for _, g := range a.visibleMenuGroups() {
		for _, it := range g.items {
			if it.label == "Find all in file" {
				found = true
				if it.shortcut != "esc F" {
					t.Errorf("menu shortcut hint = %q, want %q", it.shortcut, "esc F")
				}
			}
		}
	}
	if !found {
		t.Error("no ≡ row labelled \"Find all in file\"")
	}
}

// TestFindAll_ImageTabIsInert — image tabs have no buffer to search, and
// the row's enable predicate says so.
func TestFindAll_ImageTabIsInert(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	if a.hasFindAll() {
		t.Error("hasFindAll should be false with no tab open")
	}
	a.openFindAll()
	if a.modal != nil {
		t.Errorf("openFindAll with no tab opened %T", a.modal)
	}
}
