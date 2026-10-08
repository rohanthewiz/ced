// =============================================================================
// File: internal/app/contextmenu_test.go
// Author: Rohan Allison
// =============================================================================

// Tests for the editor's right-click context menu: open/decline routing,
// the caret-placement contract (click sets the caret unless it lands in
// the selection), row gating, the word-under-caret seed, and the armed
// selection-vs-paste compare flow.

package app

import (
	"strings"
	"testing"

	"github.com/gdamore/tcell/v2"

	"github.com/rohanthewiz/ced/internal/editor"
	"github.com/rohanthewiz/ced/internal/filetree"
)

// openEditorContextAt right-clicks the editor at buffer-ish local
// coordinates and returns the opened modal, failing the test when the
// click was declined.
func openEditorContextAt(t *testing.T, a *App, lx, ly int) *editorContextModal {
	t.Helper()
	ex, ey, _, _ := a.editorRect()
	if !a.tryEditorContextClick(ex+lx, ey+ly) {
		t.Fatalf("context click at local (%d,%d) declined", lx, ly)
	}
	m, ok := a.modal.(*editorContextModal)
	if !ok {
		t.Fatalf("expected editorContextModal, got %T", a.modal)
	}
	return m
}

// contextRowIndex finds the row whose label starts with prefix, or -1.
func contextRowIndex(m *editorContextModal, prefix string) int {
	for i, it := range m.items {
		if strings.HasPrefix(it.label, prefix) {
			return i
		}
	}
	return -1
}

func TestEditorContextClickOpensAndPlacesCaret(t *testing.T) {
	root := t.TempDir()
	p := writeStatusTestFile(t, root, "ctx.go", "package main\n\nfunc hello() {}\n")
	a := newTestApp(t, root)
	a.openFile(p)
	tab := a.activeTabPtr()

	ex, ey, ew, eh := a.editorRect()
	want, ok := tab.HitTest(8, 2, ew, eh)
	if !ok {
		t.Fatal("hit test failed on known content")
	}
	m := openEditorContextAt(t, a, 8, 2)
	if tab.Cursor != want {
		t.Fatalf("caret should follow the click: got %+v want %+v", tab.Cursor, want)
	}
	// The popup anchors at (or near) the click point.
	mx, my, _, _ := m.rect(a)
	if mx < 0 || my < 0 || mx >= ex+ew || my >= ey+eh+2 {
		t.Fatalf("popup anchored off-screen: (%d,%d)", mx, my)
	}
}

func TestEditorContextClickDeclinedWithoutTab(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	ex, ey, _, _ := a.editorRect()
	if a.tryEditorContextClick(ex+2, ey+2) {
		t.Fatal("context click should decline with no tab open")
	}
}

func TestEditorContextClickInsideSelectionKeepsIt(t *testing.T) {
	root := t.TempDir()
	p := writeStatusTestFile(t, root, "sel.txt", "alpha beta gamma\nsecond line\n")
	a := newTestApp(t, root)
	a.openFile(p)
	tab := a.activeTabPtr()

	// Select "beta" by hand and right-click inside it.
	tab.Anchor = editor.Position{Line: 0, Col: 6}
	tab.Cursor = editor.Position{Line: 0, Col: 10}
	_, _, ew, eh := a.editorRect()
	// Find the local x that maps to buffer col 8 (inside the selection).
	lx := -1
	for x := 0; x < ew; x++ {
		if pos, ok := tab.HitTest(x, 0, ew, eh); ok && pos.Line == 0 && pos.Col == 8 {
			lx = x
			break
		}
	}
	if lx < 0 {
		t.Fatal("could not locate buffer col 8 on screen")
	}
	openEditorContextAt(t, a, lx, 0)
	if !tab.HasSelection() || tab.SelectionText() != "beta" {
		t.Fatalf("selection should survive a right-click inside it; got %q", tab.SelectionText())
	}
}

func TestEditorContextCopyAndSearchRows(t *testing.T) {
	root := t.TempDir()
	p := writeStatusTestFile(t, root, "copy.txt", "alpha beta gamma\n")
	a := newTestApp(t, root)
	a.openFile(p)
	tab := a.activeTabPtr()
	tab.Anchor = editor.Position{Line: 0, Col: 6}
	tab.Cursor = editor.Position{Line: 0, Col: 10}

	m := openEditorContextAt(t, a, 2, 0) // outside the selection…
	// …which moved the caret and dropped it. Re-select for the row test.
	if tab.HasSelection() {
		t.Fatal("click outside the selection should have collapsed it")
	}
	a.closeModal()
	tab.Anchor = editor.Position{Line: 0, Col: 6}
	tab.Cursor = editor.Position{Line: 0, Col: 10}
	// Find the local x that maps to buffer col 8 — inside the selection
	// (the gutter shifts screen x against buffer col).
	_, _, ew, eh := a.editorRect()
	lx := -1
	for x := 0; x < ew; x++ {
		if pos, ok := tab.HitTest(x, 0, ew, eh); ok && pos.Line == 0 && pos.Col == 8 {
			lx = x
			break
		}
	}
	if lx < 0 {
		t.Fatal("could not locate buffer col 8 on screen")
	}
	m = openEditorContextAt(t, a, lx, 0) // inside this time

	// The search row is seeded from the selection and says so.
	si := contextRowIndex(m, "Search project for")
	if si < 0 {
		t.Fatalf("no search row; rows: %v", labelsOf(m))
	}
	if !strings.Contains(m.items[si].label, `"beta"`) {
		t.Fatalf("search row should quote the selection: %q", m.items[si].label)
	}

	ci := contextRowIndex(m, "Copy")
	if ci < 0 {
		t.Fatal("no Copy row")
	}
	m.hover = ci
	m.activate(a)
	if a.clipBuf != "beta" {
		t.Fatalf("Copy row should copy the selection; clip = %q", a.clipBuf)
	}
	if a.modal != nil {
		t.Fatal("activating a row should close the popup")
	}
}

// TestEditorContextSelectAllRow pins the right-click Select all row: it is
// in the fixed vocabulary, enabled, and selects the whole buffer — even
// though the right-click itself just collapsed any selection by moving
// the caret to the click point.
func TestEditorContextSelectAllRow(t *testing.T) {
	root := t.TempDir()
	p := writeStatusTestFile(t, root, "all.txt", "alpha beta\ngamma\n")
	a := newTestApp(t, root)
	a.openFile(p)
	tab := a.activeTabPtr()

	m := openEditorContextAt(t, a, 2, 0)
	i := contextRowIndex(m, "Select all")
	if i < 0 {
		t.Fatalf("no Select all row; rows: %v", labelsOf(m))
	}
	if !m.items[i].enabled(a) {
		t.Fatal("Select all row should be enabled on a text tab")
	}
	m.hover = i
	m.activate(a)
	if a.modal != nil {
		t.Fatal("activating Select all should close the popup")
	}
	if got := tab.SelectionText(); got != "alpha beta\ngamma\n" {
		t.Fatalf("selection = %q, want the whole buffer", got)
	}
}

// labelsOf lists the popup's row labels for failure messages.
func labelsOf(m *editorContextModal) []string {
	out := make([]string, len(m.items))
	for i, it := range m.items {
		out[i] = it.label
	}
	return out
}

func TestEditorContextDisabledRowSwallowsActivate(t *testing.T) {
	root := t.TempDir()
	p := writeStatusTestFile(t, root, "dim.txt", "words here\n")
	a := newTestApp(t, root)
	a.openFile(p) // lsp.dead in tests → LSP rows disabled

	m := openEditorContextAt(t, a, 1, 0)
	gi := contextRowIndex(m, "Go to definition")
	if gi < 0 {
		t.Fatal("no Go to definition row")
	}
	if m.items[gi].enabled(a) {
		t.Fatal("LSP row should be disabled with no server")
	}
	m.hover = gi
	m.activate(a)
	if a.modal == nil {
		t.Fatal("activating a disabled row should keep the popup open")
	}
}

func TestWordAt(t *testing.T) {
	root := t.TempDir()
	p := writeStatusTestFile(t, root, "w.txt", "foo bar_baz  qux\n")
	a := newTestApp(t, root)
	a.openFile(p)
	tab := a.activeTabPtr()

	cases := []struct {
		col  int
		want string
	}{
		{0, "foo"}, {2, "foo"}, {3, "foo"}, // end-of-word col still finds it
		{5, "bar_baz"}, {11, "bar_baz"},
		{12, ""}, // whitespace gap
		{14, "qux"},
	}
	for _, c := range cases {
		got := wordAt(tab, editor.Position{Line: 0, Col: c.col})
		if got != c.want {
			t.Errorf("wordAt col %d = %q, want %q", c.col, got, c.want)
		}
	}
}

func TestPosInSelection(t *testing.T) {
	root := t.TempDir()
	p := writeStatusTestFile(t, root, "ps.txt", "0123456789\nabcdefghij\n")
	a := newTestApp(t, root)
	a.openFile(p)
	tab := a.activeTabPtr()

	// Reversed anchor/cursor must behave identically to forward.
	tab.Anchor = editor.Position{Line: 1, Col: 4}
	tab.Cursor = editor.Position{Line: 0, Col: 2}

	in := []editor.Position{{Line: 0, Col: 2}, {Line: 0, Col: 9}, {Line: 1, Col: 0}, {Line: 1, Col: 3}}
	out := []editor.Position{{Line: 0, Col: 1}, {Line: 1, Col: 4}, {Line: 1, Col: 9}}
	for _, pos := range in {
		if !posInSelection(tab, pos) {
			t.Errorf("%+v should be inside the selection", pos)
		}
	}
	for _, pos := range out {
		if posInSelection(tab, pos) {
			t.Errorf("%+v should be outside the selection", pos)
		}
	}
}

func TestCompareSelectionWithPaste(t *testing.T) {
	root := t.TempDir()
	p := writeStatusTestFile(t, root, "cmp.txt", "one\ntwo\nthree\n")
	a := newTestApp(t, root)
	a.openFile(p)
	tab := a.activeTabPtr()
	tab.Anchor = editor.Position{Line: 0, Col: 0}
	tab.Cursor = editor.Position{Line: 1, Col: 3} // "one\ntwo"

	a.compareSelectionWithPaste()
	if !a.compare.open || !a.compare.awaitPaste || a.compare.selPending == nil {
		t.Fatal("compare panel should be open and armed for the selection")
	}

	a.compareInsertPaste("one\nTWO")
	if a.compare.awaitPaste || a.compare.selPending != nil {
		t.Fatal("paste arrival should disarm the snapshot")
	}
	if a.compare.newLabel != "selection" {
		t.Fatalf("new side should be labeled selection, got %q", a.compare.newLabel)
	}
	if a.compare.identical {
		t.Fatal("differing texts should not report identical")
	}
	joined := strings.Join(a.compare.lines, "\n")
	if !strings.Contains(joined, "-TWO") || !strings.Contains(joined, "+two") {
		t.Fatalf("diff should show the changed line; got:\n%s", joined)
	}
	// A snapshot compare has nothing to refresh — ⟳ must be a no-op.
	before := strings.Join(a.compare.lines, "\n")
	tab.InsertString("mutate the buffer")
	a.compareRefresh()
	if strings.Join(a.compare.lines, "\n") != before {
		t.Fatal("refresh must not recompute a snapshot-vs-snapshot diff")
	}
}

func TestCompareSelectionIdenticalPaste(t *testing.T) {
	root := t.TempDir()
	p := writeStatusTestFile(t, root, "same.txt", "same text\n")
	a := newTestApp(t, root)
	a.openFile(p)
	tab := a.activeTabPtr()
	tab.Anchor = editor.Position{Line: 0, Col: 0}
	tab.Cursor = editor.Position{Line: 0, Col: 9}

	a.compareSelectionWithPaste()
	a.compareInsertPaste("same text")
	if !a.compare.identical {
		t.Fatal("identical selection and paste should report identical")
	}
}

func TestCloseComparePanelDisarmsSelection(t *testing.T) {
	root := t.TempDir()
	p := writeStatusTestFile(t, root, "dis.txt", "abc\n")
	a := newTestApp(t, root)
	a.openFile(p)
	tab := a.activeTabPtr()
	tab.Anchor = editor.Position{Line: 0, Col: 0}
	tab.Cursor = editor.Position{Line: 0, Col: 3}

	a.compareSelectionWithPaste()
	a.closeComparePanel()
	if a.compare.selPending != nil {
		t.Fatal("closing the panel must drop the armed selection snapshot")
	}
}

func TestPlaceContextSizedFlips(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	// Bottom-right corner: the popup must flip up and left to stay on.
	cx, cy := a.placeContextSized(a.width-1, a.height-1, 10, 30)
	if cx+30 > a.width || cy+12 > a.height {
		t.Fatalf("popup runs off screen: origin (%d,%d)", cx, cy)
	}
	// Top-left corner: no flip, clamped at zero.
	cx, cy = a.placeContextSized(0, 0, 10, 30)
	if cx != 0 || cy != 0 {
		t.Fatalf("expected origin (0,0), got (%d,%d)", cx, cy)
	}
}

// TestEditorContext_PreviewRowOnMarkdownOnly pins the conditional
// append: a document offers Preview, a source file does not carry a
// permanently dead row for a viewer that could never render it.
func TestEditorContext_PreviewRowOnMarkdownOnly(t *testing.T) {
	root := t.TempDir()
	md := writeStatusTestFile(t, root, "doc.md", "# Title\n\nbody text\n")
	code := writeStatusTestFile(t, root, "code.go", "package main\n")
	a := newTestApp(t, root)

	a.openFile(md)
	m := openEditorContextAt(t, a, 1, 0)
	if contextRowIndex(m, "Preview") < 0 {
		t.Errorf("markdown file has no Preview row: %v", labelsOf(m))
	}
	a.closeModal()

	a.openFile(code)
	m = openEditorContextAt(t, a, 1, 0)
	if contextRowIndex(m, "Preview") >= 0 {
		t.Errorf("Preview offered on a .go file: %v", labelsOf(m))
	}
}

// TestEditorContext_InsidePreviewOffersOnlyTheWayOut pins the one-row
// popup: a rendered document has no caret for the code verbs to aim at,
// and Stop Preview is the row a reader who arrived from the tree needs.
func TestEditorContext_InsidePreviewOffersOnlyTheWayOut(t *testing.T) {
	root := t.TempDir()
	md := writeStatusTestFile(t, root, "doc.md", "# Title\n\nbody text\n")
	a := newTestApp(t, root)
	a.openFile(md)
	a.toggleMarkdownView()

	m := openEditorContextAt(t, a, 1, 0)
	if got := labelsOf(m); len(got) != 1 || got[0] != "Stop Preview" {
		t.Fatalf("preview popup should be exactly [Stop Preview], got %v", got)
	}
	m.activate(a)
	if a.markdownTab() != nil {
		t.Error("Stop Preview left the tab in preview")
	}
}

// TestEditorContext_PreviewRowTogglesIn pins the other direction: the
// row on a source view turns the preview on, so the pair is reachable
// from the editor body alone.
func TestEditorContext_PreviewRowTogglesIn(t *testing.T) {
	root := t.TempDir()
	md := writeStatusTestFile(t, root, "doc.md", "# Title\n\nbody text\n")
	a := newTestApp(t, root)
	a.openFile(md)

	m := openEditorContextAt(t, a, 1, 0)
	i := contextRowIndex(m, "Preview")
	if i < 0 {
		t.Fatalf("no Preview row: %v", labelsOf(m))
	}
	m.hover = i
	m.activate(a)
	if a.markdownTab() == nil {
		t.Error("the Preview row did not turn the preview on")
	}
}

// TestEditorContextLeadsWithConflictRows pins the conflict rows' place:
// a right-click inside a live conflict opens with the ways to settle it,
// ahead of the code vocabulary; outside one they are absent.
func TestEditorContextLeadsWithConflictRows(t *testing.T) {
	a, _ := conflictTestApp(t, "a.go")
	m := openEditorContextAt(t, a, 8, 2) // inside the block (line 2: current side)
	if m.items[0].label != "Accept current" || m.items[1].label != "Accept incoming" {
		t.Errorf("first rows = %q, %q", m.items[0].label, m.items[1].label)
	}
	a.closeModal()
	m = openEditorContextAt(t, a, 8, 0) // line 0: outside
	if contextRowIndex(m, "Accept") >= 0 {
		t.Error("conflict rows offered outside a conflict")
	}
}

// TestEditorContextMenuWidth_SharesTheTreeRule pins every editor-chassis
// popup (editor, preview, tab, group, problems, git log, conflicts) to
// the tree popup's sizing: the classic width as a floor, grown to the
// widest label, clamped to a measured screen — so no row can run over
// the right border and the menus can't drift apart again.
func TestEditorContextMenuWidth_SharesTheTreeRule(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	if got := a.editorContextMenuWidth([]editorContextItem{{label: "Stop Preview"}}); got != contextMenuWidth {
		t.Fatalf("short label: width %d, want the floor %d", got, contextMenuWidth)
	}
	long := "Delete (both sides deleted it)"
	items := []editorContextItem{{label: "Copy path"}, {label: long}}
	if got := a.editorContextMenuWidth(items); got != runeLen(long)+6 {
		t.Fatalf("long label: width %d, want %d", got, runeLen(long)+6)
	}
	if got, want := a.editorContextMenuWidth(items), a.contextMenuWidthFor([]contextItem{{label: long}}); got != want {
		t.Fatalf("editor chassis %d != tree chassis %d for the same label", got, want)
	}
	a.width = 15
	if got := a.editorContextMenuWidth(items); got != 15 {
		t.Fatalf("narrow screen: width %d, want 15", got)
	}
}

// TestCtxScroll_OffsetRevealAndScrollBy pins the clipped-popup window
// arithmetic: the stored offset is re-clamped on every read, reveal moves
// it only as far as the target row needs, and scrollBy never runs past
// either end.
func TestCtxScroll_OffsetRevealAndScrollBy(t *testing.T) {
	var s ctxScroll
	// 20 items, 6 visible: valid offsets are 0..14.
	s.top = 99
	if got := s.offset(20, 6); got != 14 {
		t.Fatalf("offset should clamp to count-rows: got %d want 14", got)
	}
	s.top = -3
	if got := s.offset(20, 6); got != 0 {
		t.Fatalf("offset should floor at 0: got %d", got)
	}
	// Everything fits: always 0, whatever is stored.
	s.top = 4
	if got := s.offset(5, 9); got != 0 {
		t.Fatalf("a popup that fits never scrolls: got %d", got)
	}

	s.top = 0
	s.reveal(3, 20, 6) // already on screen
	if s.top != 0 {
		t.Fatalf("reveal of a visible row moved the window to %d", s.top)
	}
	s.reveal(10, 20, 6) // below: lands as the last visible row
	if s.top != 5 {
		t.Fatalf("reveal below: top = %d, want 5", s.top)
	}
	s.reveal(2, 20, 6) // above: lands as the first visible row
	if s.top != 2 {
		t.Fatalf("reveal above: top = %d, want 2", s.top)
	}

	s.scrollBy(100, 20, 6)
	if s.top != 14 {
		t.Fatalf("scrollBy past the end: top = %d, want 14", s.top)
	}
	s.scrollBy(-100, 20, 6)
	if s.top != 0 {
		t.Fatalf("scrollBy past the start: top = %d, want 0", s.top)
	}
}

// TestCtxMenuRows_ClipsToWindow pins the visible-row count: all items
// when they fit below the anchor, the window's remainder when they do
// not, and never less than one row.
func TestCtxMenuRows_ClipsToWindow(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	a.height = 12
	if got := a.ctxMenuRows(0, 5); got != 5 {
		t.Fatalf("fits: got %d want 5", got)
	}
	if got := a.ctxMenuRows(0, 18); got != 10 {
		t.Fatalf("clipped at row 0: got %d want 10", got)
	}
	if got := a.ctxMenuRows(4, 18); got != 6 {
		t.Fatalf("clipped at row 4: got %d want 6", got)
	}
	a.height = 2
	if got := a.ctxMenuRows(0, 18); got != 1 {
		t.Fatalf("degenerate window should still show one row: got %d", got)
	}
}

// TestPlaceContextSized_TallMenuStaysOnScreen is the N-039 regression: a
// menu taller than the window used to be placed by its FULL height, clamp
// to row 0 and run off the bottom. Placed by the clipped height, its box
// now ends inside the window wherever the click landed.
func TestPlaceContextSized_TallMenuStaysOnScreen(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	a.height = 14
	for _, y := range []int{0, 5, 13} {
		_, cy := a.placeContextSized(10, y, 18, 30)
		h := a.ctxMenuRows(cy, 18) + 2
		if cy < 0 || cy+h > a.height {
			t.Fatalf("click row %d: popup rows %d..%d leave a %d-row window", y, cy, cy+h-1, a.height)
		}
	}
}

// longEditorMenu opens a synthetic editorContextModal of n enabled rows
// anchored at (x, y) through the real placement, so the popup is clipped
// exactly as a real overlong menu would be. Each row records its label in
// *ran when it runs.
func longEditorMenu(a *App, n, x, y int, ran *string) *editorContextModal {
	items := make([]editorContextItem, n)
	for i := range items {
		label := "row " + itoa(i)
		items[i] = editorContextItem{label: label, enabled: alwaysTrue,
			action: func(*App) { *ran = label }}
	}
	w := a.editorContextMenuWidth(items)
	cx, cy := a.placeContextSized(x, y, n, w)
	m := &editorContextModal{x: cx, y: cy, w: w, items: items}
	a.openModal(m)
	return m
}

// TestEditorContextModal_ScrollsWhenTallerThanTheWindow drives the
// editor chassis at the smallest window ced draws (minHeight) with more
// rows than fit — reachable today by the editor menu on a conflict inside
// cats, with a bookmark and a search word. The box stays on screen, ▼
// announces the rest, the arrows reach the last row with the window
// following, and ▲ then says the top has scrolled away.
func TestEditorContextModal_ScrollsWhenTallerThanTheWindow(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	a.height = minHeight
	ran := ""
	m := longEditorMenu(a, 30, 10, 10, &ran)
	mx, my, mw, mh := m.rect(a)
	if my < 0 || my+mh > a.height {
		t.Fatalf("menu rows %d..%d leave a %d-row window", my, my+mh-1, a.height)
	}
	if mh-2 >= len(m.items) {
		t.Fatalf("test needs a clipped menu: %d rows shown of %d", mh-2, len(m.items))
	}
	a.draw()
	if top := screenRow(t, a, my, mx, mw); strings.Contains(top, "▲") {
		t.Fatalf("nothing is hidden above yet, top border = %q", top)
	}
	if bottom := screenRow(t, a, my+mh-1, mx, mw); !strings.Contains(bottom, "▼") {
		t.Fatalf("bottom border should announce hidden rows, got %q", bottom)
	}

	// Walk to the end with the keyboard; the window must follow.
	for range m.items {
		m.handleKey(a, tcell.NewEventKey(tcell.KeyDown, 0, tcell.ModNone))
	}
	if m.hover != len(m.items)-1 {
		t.Fatalf("Down should reach the last row, hover = %d", m.hover)
	}
	a.draw()
	if got := screenRow(t, a, my+mh-2, mx, mw); !strings.Contains(got, "row 29") {
		t.Fatalf("hovered last row should sit on the popup's last line, got %q", got)
	}
	if top := screenRow(t, a, my, mx, mw); !strings.Contains(top, "▲") {
		t.Fatalf("top border should announce rows scrolled away, got %q", top)
	}
	if bottom := screenRow(t, a, my+mh-1, mx, mw); strings.Contains(bottom, "▼") {
		t.Fatalf("nothing is hidden below any more, bottom border = %q", bottom)
	}
	m.handleKey(a, tcell.NewEventKey(tcell.KeyEnter, 0, tcell.ModNone))
	if ran != "row 29" {
		t.Fatalf("Enter on the scrolled-to row ran %q", ran)
	}
}

// TestEditorContextModal_WheelAndBorderPage pins the two pointer doors
// into a clipped popup: the wheel scrolls it with the hover re-read under
// the pointer, and a press on a ▼/▲ border turns a page without closing
// the menu or running a row.
func TestEditorContextModal_WheelAndBorderPage(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	a.height = minHeight
	ran := ""
	m := longEditorMenu(a, 60, 0, 0, &ran)
	mx, my, mw, mh := m.rect(a)
	rows := mh - 2

	m.handleMouse(a, mx+3, my+1, tcell.WheelDown)
	if got := m.scroll.offset(60, rows); got != wheelLines {
		t.Fatalf("wheel down: offset %d, want %d", got, wheelLines)
	}
	if m.hover != wheelLines {
		t.Fatalf("hover should follow the pointer's new row: got %d", m.hover)
	}
	// The wheel outside the popup leaves it alone.
	m.handleMouse(a, mx+mw+5, my+1, tcell.WheelDown)
	if got := m.scroll.offset(60, rows); got != wheelLines {
		t.Fatalf("wheel outside moved the popup to %d", got)
	}

	m.handleMouse(a, mx+mw/2, my+mh-1, tcell.Button1) // ▼ border
	if a.modal != m || ran != "" {
		t.Fatalf("border press must page, not close or run (modal %T, ran %q)", a.modal, ran)
	}
	if got := m.scroll.offset(60, rows); got != wheelLines+rows-1 {
		t.Fatalf("border page down: offset %d, want %d", got, wheelLines+rows-1)
	}
	m.handleMouse(a, mx+mw/2, my, tcell.Button1) // ▲ border
	if got := m.scroll.offset(60, rows); got != wheelLines {
		t.Fatalf("border page up: offset %d, want %d", got, wheelLines)
	}

	// A press on a body row runs the item drawn THERE, not items[row].
	m.handleMouse(a, mx+3, my+2, tcell.Button1)
	if want := "row " + itoa(wheelLines+1); ran != want {
		t.Fatalf("click ran %q, want %q", ran, want)
	}
}

// TestTreeContext_ScrollsWhenTallerThanTheWindow pins the tree's menu,
// which shares ctxScroll: an overlong row list clips to the window, the
// arrows keep the hover visible, ▲ appears once the top scrolls away,
// and Enter runs the row the user scrolled to.
func TestTreeContext_ScrollsWhenTallerThanTheWindow(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	a.height = minHeight
	ran := ""
	items := make([]contextItem, 30)
	for i := range items {
		label := "row " + itoa(i)
		items[i] = contextItem{label: label, action: func(*App, *filetree.Node) { ran = label }}
	}
	cx, cy := a.placeContextSized(5, 5, len(items), 30)
	m := &contextModal{x: cx, y: cy, w: 30, node: a.tree.Root, items: items}
	a.openModal(m)
	mx, my, mw, mh := m.rect(a)
	if my+mh > a.height || mh-2 >= len(m.items) {
		t.Fatalf("want a clipped, on-screen popup: rows %d..%d of %d, %d items",
			my, my+mh-1, a.height, len(m.items))
	}
	for range m.items {
		m.handleKey(a, tcell.NewEventKey(tcell.KeyDown, 0, tcell.ModNone))
	}
	a.draw()
	if got := screenRow(t, a, my+mh-2, mx, mw); !strings.Contains(got, "row 29") {
		t.Fatalf("last row should be drawn on the popup's last line, got %q", got)
	}
	if top := screenRow(t, a, my, mx, mw); !strings.Contains(top, "▲") {
		t.Fatalf("top border should announce rows scrolled away, got %q", top)
	}
	m.handleKey(a, tcell.NewEventKey(tcell.KeyEnter, 0, tcell.ModNone))
	if ran != "row 29" {
		t.Fatalf("Enter on the scrolled-to row ran %q", ran)
	}
}
