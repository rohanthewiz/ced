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

	"github.com/rohanthewiz/ced/internal/editor"
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
