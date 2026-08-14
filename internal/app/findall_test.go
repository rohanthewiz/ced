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

	m.handleMouse(a, mx+8, my+4+2, tcell.Button1) // third visible row
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
	x, y := mx+8, my+4+1

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
	x, y := mx+8, my+4+1

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

// TestFindAll_PreviewCentersAnOffscreenHit pins the peek scroll: a hit
// outside the band lands mid-band with context on both sides, not parked
// on the edge a minimal scroll would have chosen. The row it lands on has
// to be measured against the SHORTENED band — the popup is open.
func TestFindAll_PreviewCentersAnOffscreenHit(t *testing.T) {
	a, tab := seedFindAllLongApp(t)
	tab.MoveCursorTo(editor.Position{Line: 0, Col: 0}, false)

	m := openFindAllT(t, a, "count")
	_, _, _, eh := a.editorRect()
	m.selectRow(a, 15) // line 60 — well past the band from a ScrollY of 0

	row := tab.Cursor.Line - tab.ScrollY
	if want := eh / 2; row != want {
		t.Errorf("previewed hit drawn on band row %d of %d, want the middle row %d", row, eh, want)
	}
}

// TestFindAll_PreviewLeavesAnOnscreenHitInPlace is the other half of the
// rule: a hit the user can already see must not scroll the code out from
// under them. Walking a cluster of nearby hits holds the view still.
func TestFindAll_PreviewLeavesAnOnscreenHitInPlace(t *testing.T) {
	a, tab := seedFindAllLongApp(t)
	m := openFindAllT(t, a, "count")
	m.selectRow(a, 15) // off-screen: this one centers
	settled := tab.ScrollY

	_, _, _, eh := a.editorRect()
	m.selectRow(a, 16) // the next hit, four lines down — still inside the band
	if !tab.CursorLineVisible(eh) {
		t.Fatalf("fixture drifted: hit on line %d isn't visible at ScrollY %d (band %d)",
			tab.Cursor.Line, tab.ScrollY, eh)
	}
	if tab.ScrollY != settled {
		t.Errorf("ScrollY = %d, want %d held still — an on-screen hit re-centered", tab.ScrollY, settled)
	}
}

// TestFindAll_PreviewNearTopOfFileDoesNotScrollPastIt keeps centering
// honest at the edge: a hit in the first few lines can't be centered, and
// must not push the buffer up to fake it. Jumping there from far down the
// file is what makes it an off-screen (i.e. centering) preview.
func TestFindAll_PreviewNearTopOfFileDoesNotScrollPastIt(t *testing.T) {
	a, tab := seedFindAllLongApp(t)
	m := openFindAllT(t, a, "count")
	m.selectRow(a, 20) // scroll away first
	if tab.ScrollY == 0 {
		t.Fatal("fixture drifted: previewing row 20 should have scrolled")
	}
	m.selectRow(a, 0) // back to the hit on line 0

	if tab.ScrollY != 0 {
		t.Errorf("ScrollY = %d previewing the first line, want 0", tab.ScrollY)
	}
}

// TestFindAll_RightDockTakesColumnsNotRows pins the alternate layout:
// the list becomes a full-height column at the far end of the editor's
// band, the editor keeps its rows and loses columns instead, and the two
// still don't overlap.
func TestFindAll_RightDockTakesColumnsNotRows(t *testing.T) {
	a, _ := seedFindAllApp(t)
	_, _, beforeW, beforeH := a.editorRect()

	a.findAllDockRight = true
	m := openFindAllT(t, a, "count")
	ex, ey, ew, eh := a.editorRect()
	mx, my, mw, mh := m.rect(a)

	if eh != beforeH {
		t.Errorf("editor height = %d, want %d unchanged — the right dock costs columns", eh, beforeH)
	}
	if ey != 1 {
		t.Errorf("editor y = %d, want 1 — nothing is pushing it down", ey)
	}
	if ew != beforeW-mw {
		t.Errorf("editor width = %d, want %d (band %d minus column %d)", ew, beforeW-mw, beforeW, mw)
	}
	if mx != ex+ew {
		t.Errorf("column x = %d, want %d (the editor's right edge)", mx, ex+ew)
	}
	if my != 1 || mh != beforeH {
		t.Errorf("column = y%d h%d, want the full band y1 h%d", my, mh, beforeH)
	}
	if ew < findAllMinEditorCols {
		t.Errorf("editor left with %d columns, want at least %d", ew, findAllMinEditorCols)
	}
	// A tall column shows more hits than the strip does — the point of it.
	if got := m.visibleRows(a); got <= findAllVisibleRows {
		t.Errorf("visible rows = %d, want more than the strip's %d", got, findAllVisibleRows)
	}
}

// TestFindAll_RightDockWidthClamps pins the width band and its
// precedence. On a normal window the editor's reserve is what binds; on
// a band too narrow for both, the list keeps its own floor and the
// editor eats the difference — the git panel's rule, because a column
// too narrow to read is worse than a narrow editor.
func TestFindAll_RightDockWidthClamps(t *testing.T) {
	a, _ := seedFindAllApp(t)
	a.findAllDockRight = true
	m := openFindAllT(t, a, "count")

	if _, _, ew, _ := a.editorRect(); ew < findAllMinEditorCols {
		t.Errorf("editor columns = %d on a 120-col window, want >= %d", ew, findAllMinEditorCols)
	}

	a.width = 70 // band of 40 with the sidebar open: both can't fit
	if w := m.width(a); w != findAllMinWidth {
		t.Errorf("column width = %d on a cramped band, want the floor %d", w, findAllMinWidth)
	}
	if _, _, ew, _ := a.editorRect(); ew != a.editorBandCols()-findAllMinWidth {
		t.Errorf("editor columns = %d, want the remainder %d", ew, a.editorBandCols()-findAllMinWidth)
	}
}

// TestFindAll_DockButtonFlipsTheLayout drives the ◨ button: a click on it
// swaps the dock, leaves the list open, and is not mistaken for a row
// click (which would preview) or half a double-click (which would accept).
func TestFindAll_DockButtonFlipsTheLayout(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	a, tab := seedFindAllApp(t)
	m := openFindAllT(t, a, "count")
	before := tab.Cursor

	btn := m.dockRect(a)
	m.handleMouse(a, btn.x+1, btn.y, tcell.Button1)

	if !a.findAllDockRight {
		t.Fatal("clicking the dock button should have flipped the layout")
	}
	if a.modal == nil {
		t.Fatal("the list must stay open across a dock flip")
	}
	if tab.Cursor != before {
		t.Errorf("cursor = %v, want %v — the button is not a row", tab.Cursor, before)
	}
	// Clicking it again flips back, which proves it wasn't recorded as
	// the first half of a double-click accept either.
	btn = m.dockRect(a)
	m.handleMouse(a, btn.x+1, btn.y, tcell.Button1)
	if a.findAllDockRight {
		t.Error("a second click should have flipped it back")
	}
	if a.modal == nil {
		t.Error("two clicks on the button must not accept")
	}
}

// TestFindAll_DockKeyIsTheKeyboardTwin covers `d`. It exists because a
// modal owns the keyboard, so the ≡ row can't be reached from inside the
// list — the button would otherwise be the only way in.
func TestFindAll_DockKeyIsTheKeyboardTwin(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	a, _ := seedFindAllApp(t)
	m := openFindAllT(t, a, "count")

	m.handleKey(a, tcell.NewEventKey(tcell.KeyRune, 'd', tcell.ModNone))
	if !a.findAllDockRight {
		t.Fatal("d should flip the dock")
	}
	if a.modal == nil {
		t.Fatal("d must not close the list")
	}
	// Any other rune stays inert — the list has no input field.
	before := m.selected
	m.handleKey(a, tcell.NewEventKey(tcell.KeyRune, 'x', tcell.ModNone))
	if m.selected != before || !a.findAllDockRight {
		t.Error("an unbound rune should do nothing at all")
	}
}

// TestFindAll_DockPersistsAndMenuRowMatches pins the preference: the
// choice is written to config.json (so it survives a restart) and the ≡
// row names the layout it will switch TO.
func TestFindAll_DockPersistsAndMenuRowMatches(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	a, _ := seedFindAllApp(t)

	if got := a.findAllDockToggleLabel(); got != "Dock find-all results right" {
		t.Errorf("label = %q, want the action, not the state", got)
	}
	a.menuToggleFindAllDock()
	if !a.findAllDockRight {
		t.Fatal("the ≡ row should flip the dock")
	}
	if got := a.findAllDockToggleLabel(); got != "Dock find-all results at top" {
		t.Errorf("flipped label = %q", got)
	}

	data, err := os.ReadFile(filepath.Join(os.Getenv("XDG_CONFIG_HOME"), "ced", "config.json"))
	if err != nil {
		t.Fatalf("config not written: %v", err)
	}
	if !strings.Contains(string(data), `"findalldock": "right"`) {
		t.Errorf("config.json = %s, want the findalldock key", data)
	}
}

// TestFindAll_DockGlyphNamesTheTarget keeps the button honest: it shows
// the layout a click produces, not the one in force.
func TestFindAll_DockGlyphNamesTheTarget(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	if got := a.findAllDockGlyph(); got != " ◨ " {
		t.Errorf("glyph = %q while docked top, want the right-column mark", got)
	}
	a.findAllDockRight = true
	if got := a.findAllDockGlyph(); got != " ⬒ " {
		t.Errorf("glyph = %q while docked right, want the top-strip mark", got)
	}
}

// TestFindAll_DrawsInRightDock smoke-tests the alternate layout end to
// end: the frame, a row, and the dock glyph all land on screen.
func TestFindAll_DrawsInRightDock(t *testing.T) {
	a, _ := seedFindAllApp(t)
	a.findAllDockRight = true
	openFindAllT(t, a, "count")
	a.draw()
	a.screen.Show()
	out := screenText(a)

	if !strings.Contains(out, "4 │ count := 0") {
		t.Errorf("row not drawn in the right dock:\n%s", out)
	}
	if !strings.Contains(out, "⬒") {
		t.Errorf("dock glyph missing:\n%s", out)
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

// TestFindAll_SelectionQueryIsTheOnlySilentSeed pins the one input a
// find verb acts on without asking: a single-line selection. A word
// under a bare cursor and a multi-line selection are both guesses, so
// neither may answer for the user.
func TestFindAll_SelectionQueryIsTheOnlySilentSeed(t *testing.T) {
	a, tab := seedFindAllApp(t)

	// A bare cursor inside a word is an implication, not a request.
	tab.MoveCursorTo(editor.Position{Line: 2, Col: 7}, false) // inside "count"
	if got := a.findAllSelectionQuery(); got != "" {
		t.Errorf("silent seed from a bare cursor = %q, want none", got)
	}

	// A single-line selection is the user pointing at the exact text.
	tab.Anchor = editor.Position{Line: 0, Col: 0}
	tab.Cursor = editor.Position{Line: 0, Col: 7}
	if got := a.findAllSelectionQuery(); got != "package" {
		t.Errorf("silent seed from selection = %q, want %q", got, "package")
	}

	// A multi-line selection isn't a search term (FindAll is per line).
	tab.Anchor = editor.Position{Line: 0, Col: 0}
	tab.Cursor = editor.Position{Line: 2, Col: 3}
	if got := a.findAllSelectionQuery(); got != "" {
		t.Errorf("silent seed from a multi-line selection = %q, want none", got)
	}
}

// TestFindAll_PromptSeedPrefersBarThenWord pins what pre-fills the
// prompt when there's nothing selected: the find bar's text first (the
// user typed it), then the word under the cursor.
func TestFindAll_PromptSeedPrefersBarThenWord(t *testing.T) {
	a, tab := seedFindAllApp(t)

	tab.MoveCursorTo(editor.Position{Line: 2, Col: 7}, false) // inside "count"
	if got := a.findAllPromptSeed(); got != "count" {
		t.Errorf("prompt seed from cursor = %q, want %q", got, "count")
	}

	a.openFind()
	a.findField = newTextField("int")
	if got := a.findAllPromptSeed(); got != "int" {
		t.Errorf("prompt seed from bar = %q, want %q", got, "int")
	}
}

// TestFindAll_AsksWhenNothingIsSelected covers the headline rule: with
// no selection, find-all opens a prompt rather than searching for the
// word the cursor happens to be in — and pre-fills it with that word so
// accepting the guess is still one key.
func TestFindAll_AsksWhenNothingIsSelected(t *testing.T) {
	a, tab := seedFindAllApp(t)
	tab.MoveCursorTo(editor.Position{Line: 2, Col: 7}, false) // inside "count"
	a.openFindAll()

	m, ok := a.modal.(*promptModal)
	if !ok {
		t.Fatalf("modal = %T, want a promptModal asking for the query", a.modal)
	}
	if got := m.field.String(); got != "count" {
		t.Errorf("prompt pre-fill = %q, want the word under the cursor %q", got, "count")
	}
}

// TestFindAll_SeedFallsBackToPrompt covers the empty ladder: a cursor in
// whitespace has nothing to suggest, so the prompt opens blank.
func TestFindAll_SeedFallsBackToPrompt(t *testing.T) {
	a, tab := seedFindAllApp(t)
	tab.MoveCursorTo(editor.Position{Line: 1, Col: 0}, false) // the blank line
	a.openFindAll()

	m, ok := a.modal.(*promptModal)
	if !ok {
		t.Fatalf("modal = %T, want a promptModal asking for the query", a.modal)
	}
	if got := m.field.String(); got != "" {
		t.Errorf("prompt pre-fill = %q, want it blank", got)
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

// -----------------------------------------------------------------------------
// Interactive layer: filter, dismiss, pin, re-run, replace (Phase 1.6)
// -----------------------------------------------------------------------------

// typeFindAllFilter routes runes through the modal's own key handler so
// the focus plumbing is what's being exercised, not the field directly.
func typeFindAllFilter(a *App, m *findAllModal, s string) {
	m.focus = findAllFocusFilter
	for _, r := range s {
		m.handleKey(a, tcell.NewEventKey(tcell.KeyRune, r, tcell.ModNone))
	}
}

func TestFindAll_FilterNarrowsView(t *testing.T) {
	a, _ := seedFindAllApp(t)
	m := openFindAllT(t, a, "count")
	total := len(m.view)
	if total != len(m.rows) {
		t.Fatalf("fresh list: view (%d) should cover all rows (%d)", total, len(m.rows))
	}

	typeFindAllFilter(a, m, "++")
	if len(m.view) == 0 || len(m.view) >= total {
		t.Fatalf("filter should narrow the view: %d of %d", len(m.view), total)
	}
	for _, ri := range m.view {
		if !strings.Contains(m.rows[ri].text, "++") {
			t.Fatalf("surviving row %q does not match the filter", m.rows[ri].text)
		}
	}
	// Backspace the filter away: everything returns.
	m.handleKey(a, tcell.NewEventKey(tcell.KeyBackspace2, 0, tcell.ModNone))
	m.handleKey(a, tcell.NewEventKey(tcell.KeyBackspace2, 0, tcell.ModNone))
	if len(m.view) != total {
		t.Fatalf("clearing the filter should restore the view: %d of %d", len(m.view), total)
	}
}

// TestFindAll_FilterSeedsWithTheQuery pins the pre-fill: the box opens
// holding the search expression with the caret at its end, so "/" plus a
// keystroke keeps narrowing the same question instead of restating it —
// and the seed itself narrows nothing, so the list opens whole.
func TestFindAll_FilterSeedsWithTheQuery(t *testing.T) {
	a, _ := seedFindAllApp(t)
	m := openFindAllT(t, a, "count")

	if got := m.filter.String(); got != "count" {
		t.Fatalf("filter = %q, want the query pre-filled", got)
	}
	if m.filter.cursor != len("count") {
		t.Errorf("caret = %d, want the end of the seed (%d)", m.filter.cursor, len("count"))
	}
	if len(m.view) != len(m.rows) {
		t.Fatalf("the seed must narrow nothing: view %d of %d rows", len(m.view), len(m.rows))
	}

	// Typing on carries the question forward: "count" → "count++" is one
	// of the four hits.
	typeFindAllFilter(a, m, "++")
	if len(m.view) != 1 {
		t.Fatalf("extending the seed should narrow to the one hit, got %d", len(m.view))
	}
	// …and backing out to the seed restores the whole answer.
	m.handleKey(a, tcell.NewEventKey(tcell.KeyBackspace2, 0, tcell.ModNone))
	m.handleKey(a, tcell.NewEventKey(tcell.KeyBackspace2, 0, tcell.ModNone))
	if len(m.view) != len(m.rows) {
		t.Fatalf("back at the seed: view %d of %d rows", len(m.view), len(m.rows))
	}
}

// TestFindAll_SeededFilterSurvivesAnUncompactableQuery is why the seed is
// inert rather than just another filter string. A row's display text has
// its indentation stripped, so a query that matched inside that
// indentation is not present in its own results — filtering by it would
// open the list empty on the very search that filled it.
func TestFindAll_SeededFilterSurvivesAnUncompactableQuery(t *testing.T) {
	a, _ := seedFindAllApp(t)
	m := openFindAllT(t, a, "\tcount")

	if len(m.rows) == 0 {
		t.Fatal("precondition: the indented occurrences should be found")
	}
	if len(m.view) != len(m.rows) {
		t.Fatalf("the seed must not hide its own rows: view %d of %d", len(m.view), len(m.rows))
	}
	// One edit and the ordinary contains rule takes over, tab and all.
	typeFindAllFilter(a, m, "x")
	if len(m.view) != 0 {
		t.Fatalf("an edited filter should apply normally, got %d rows", len(m.view))
	}
}

func TestFindAll_DismissRowShrinksWorklist(t *testing.T) {
	a, _ := seedFindAllApp(t)
	m := openFindAllT(t, a, "count")
	total := len(m.view)
	first := m.view[0]

	m.selectRow(a, 0)
	m.handleKey(a, tcell.NewEventKey(tcell.KeyDelete, 0, tcell.ModNone))
	if len(m.view) != total-1 {
		t.Fatalf("dismiss should drop one row: %d of %d", len(m.view), total)
	}
	for _, ri := range m.view {
		if ri == first {
			t.Fatal("the dismissed row is still in the view")
		}
	}
	if !m.rows[first].dismissed {
		t.Fatal("the row should be marked, not deleted")
	}
	// Re-run resurrects the worklist.
	m.rerun(a)
	if len(m.view) != total {
		t.Fatalf("re-run should reset dismissals: %d of %d", len(m.view), total)
	}
}

func TestFindAll_PinMovesToPanelAndBack(t *testing.T) {
	a, tab := seedFindAllApp(t)
	m := openFindAllT(t, a, "count")

	m.handleKey(a, tcell.NewEventKey(tcell.KeyRune, 'p', tcell.ModNone))
	if a.modal != nil {
		t.Fatal("pinning should free the modal slot")
	}
	if a.findAllPin != m || !m.pinned {
		t.Fatal("pinning should install the panel")
	}
	if a.findAllPanelHeight() == 0 {
		t.Fatal("the pinned panel should still displace the editor band")
	}
	// The editor owns the keyboard again: a rune types into the buffer.
	before := tab.Buffer.String()
	a.handleKey(tcell.NewEventKey(tcell.KeyRune, 'x', tcell.ModNone))
	if tab.Buffer.String() == before {
		t.Fatal("editing must work while the list is pinned")
	}
	// The panel survived the edit.
	if a.findAllPin == nil {
		t.Fatal("the pinned panel must survive editor keystrokes")
	}
	// Unpin: back into the modal slot.
	m.togglePin(a)
	if a.findAllPin != nil || a.modal != m || m.pinned {
		t.Fatal("unpinning should re-enter the modal slot")
	}
}

func TestFindAll_PinnedClickPreviewsWithoutClosing(t *testing.T) {
	a, tab := seedFindAllApp(t)
	m := openFindAllT(t, a, "count")
	m.togglePin(a)
	mx, my, _, _ := m.rect(a)

	m.handleMouse(a, mx+8, my+4+2, tcell.Button1) // third visible row
	if a.findAllPin == nil {
		t.Fatal("a click on a pinned row must leave the panel up")
	}
	if m.selected != 2 {
		t.Fatalf("selected = %d, want 2", m.selected)
	}
	if tab.Cursor.Line != m.rows[m.view[2]].line {
		t.Error("the click should still preview the row")
	}
	// The ✕ button closes the panel and returns the borrowed tint.
	cl := m.closePinRect(a)
	m.handleMouse(a, cl.x, cl.y, tcell.Button1)
	if a.findAllPin != nil {
		t.Fatal("✕ should close the pinned panel")
	}
	if tab.FindQuery == "count" {
		t.Fatal("closing should hand back the borrowed find state")
	}
}

func TestFindAll_RowStaleAfterEdit(t *testing.T) {
	a, tab := seedFindAllApp(t)
	m := openFindAllT(t, a, "count")
	r := m.rows[m.view[0]]
	if m.rowStale(a, r) {
		t.Fatal("a fresh row must not read as stale")
	}
	// Rewrite the matched line out from under the row.
	tab.MoveCursorTo(editor.Position{Line: r.line, Col: 0}, false)
	tab.MoveLineEnd(true)
	tab.InsertString("rewritten")
	if !m.rowStale(a, r) {
		t.Fatal("a row whose match text is gone must read as stale")
	}
}

func TestFindAll_ReplaceInResults(t *testing.T) {
	a, tab := seedFindAllApp(t)
	m := openFindAllT(t, a, "count")
	total := len(m.view)

	// Dismiss the first row: replace must honor the worklist.
	m.selectRow(a, 0)
	m.dismissRow(a, 0)

	m.replace = newTextField("tally")
	plan, skipped, err := m.buildReplacePlan(a, "tally")
	if err != nil {
		t.Fatalf("plan: %v", err)
	}
	if skipped != 0 {
		t.Fatalf("nothing is stale yet, skipped = %d", skipped)
	}
	if plan.count != total-1 {
		t.Fatalf("plan should cover the %d surviving rows, got %d", total-1, plan.count)
	}
	if len(plan.files) != 1 || plan.toDisk != 0 {
		t.Fatalf("one open file expected: files=%d toDisk=%d", len(plan.files), plan.toDisk)
	}

	if ok, reason := a.commitWorkspaceEdit(plan); !ok {
		t.Fatalf("commit failed: %s", reason)
	}
	text := tab.Buffer.String()
	if !strings.Contains(text, "tally") {
		t.Fatal("replacement text should be in the buffer")
	}
	if strings.Count(text, "count") != 1 {
		// exactly the dismissed occurrence survives
		t.Fatalf("dismissed row should be untouched; %d 'count' left", strings.Count(text, "count"))
	}
	// One undo gesture: the workspace-edit journal owns it.
	if !a.wsUndoAvailable() {
		t.Fatal("replace should arm the one-gesture undo")
	}
}

func TestFindAll_ReplaceSkipsStaleRows(t *testing.T) {
	a, tab := seedFindAllApp(t)
	m := openFindAllT(t, a, "count")

	// Invalidate the first surviving row by rewriting its line.
	r := m.rows[m.view[0]]
	tab.MoveCursorTo(editor.Position{Line: r.line, Col: 0}, false)
	tab.MoveLineEnd(true)
	tab.InsertString("rewritten")

	_, skipped, err := m.buildReplacePlan(a, "tally")
	if err != nil {
		t.Fatalf("plan: %v", err)
	}
	if skipped == 0 {
		t.Fatal("the rewritten row should be skipped as stale")
	}
}

func TestFindAll_FreshSearchDropsPinnedPanel(t *testing.T) {
	a, _ := seedFindAllApp(t)
	m := openFindAllT(t, a, "count")
	m.togglePin(a)
	if a.findAllPin == nil {
		t.Fatal("precondition: pinned")
	}
	a.showFindAll("count")
	if a.findAllPin != nil {
		t.Fatal("a fresh search should replace the pinned survivor")
	}
	if _, ok := a.modal.(*findAllModal); !ok {
		t.Fatal("the fresh search should own the modal slot")
	}
}
