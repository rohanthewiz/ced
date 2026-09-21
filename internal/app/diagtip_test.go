// =============================================================================
// File: internal/app/diagtip_test.go
// Author: Rohan Allison <rohanthewiz@gmail.com>
// Created: 2026-09-21
// Copyright: 2026 Rohan Allison. All rights reserved.
// =============================================================================

// Tests for the diagnostic message surfaces: the tooltip rows, the
// cell → diagnostics resolution (gutter, underline, and the refusals),
// the pointer tooltip's lifecycle, Esc-i leading with the diagnostic,
// and next/previous problem saying what they landed on.

package app

import (
	"strings"
	"testing"

	"github.com/gdamore/tcell/v2"

	"github.com/rohanthewiz/ced/internal/editor"
	"github.com/rohanthewiz/ced/internal/lsp"
)

// diagTipTestApp opens main.go ("package main\n\nfunc main() {\n}\n")
// with one error underlining `main` on line 2 (cols 5–9), drawn once so
// the viewport geometry is settled. Returns the app and the screen cell
// of that line's gutter and of the `m` in `main`.
func diagTipTestApp(t *testing.T) (a *App, gutterX, codeX, rowY int) {
	t.Helper()
	a, _, goPath := newLSPTestApp(t)
	a.width, a.height = 120, 40
	a.openFile(goPath)
	a.lsp.diags = map[string][]lsp.Diagnostic{
		goPath: {{
			Range:    lsp.Range{Start: lsp.Position{Line: 2, Character: 5}, End: lsp.Position{Line: 2, Character: 9}},
			Severity: lsp.SeverityError,
			Message:  "main redeclared in this block",
			Source:   "compiler",
		}},
	}
	a.draw()
	tab := a.activeTabPtr()
	ex, ey, ew, eh := a.editorRect()
	dx, dy, ok := tab.PosScreenCell(editor.Position{Line: 2, Col: 5}, ew, eh)
	if !ok {
		t.Fatal("line 2 is not on screen")
	}
	return a, ex, ex + dx, ey + dy
}

// TestDiagTipLines pins the tooltip text: worst severity first, the
// status bar's glyphs, the source named, long messages WRAPPED rather
// than cut (the tooltip exists for the text the gutter can't show), and
// a cap that points at the Problems panel.
func TestDiagTipLines(t *testing.T) {
	got := diagTipLines([]lsp.Diagnostic{
		{Severity: lsp.SeverityWarning, Message: "unused"},
		{Severity: lsp.SeverityError, Message: "undefined: x", Source: "compiler"},
	})
	if len(got) != 2 || got[0] != "✗ undefined: x (compiler)" || got[1] != "⚠ unused" {
		t.Fatalf("rows = %q", got)
	}

	long := strings.Repeat("word ", 40)
	got = diagTipLines([]lsp.Diagnostic{{Severity: lsp.SeverityError, Message: long}})
	if len(got) < 2 {
		t.Fatalf("a long message was not wrapped: %q", got)
	}
	for _, row := range got {
		if runeLen(row)+4 > hoverModalMaxWidth {
			t.Errorf("row %q is wider than the tooltip", row)
		}
	}
	if !strings.HasPrefix(got[1], "  ") {
		t.Errorf("continuation row %q is not indented under the glyph", got[1])
	}

	many := make([]lsp.Diagnostic, 30)
	for i := range many {
		many[i] = lsp.Diagnostic{Message: "e"}
	}
	got = diagTipLines(many)
	if len(got) != diagTipMaxLines || !strings.Contains(got[len(got)-1], "Problems") {
		t.Errorf("cap: %d rows, last %q", len(got), got[len(got)-1])
	}
}

// TestDiagsAtCell pins what a pointer cell stands for: the gutter of a
// diagnosed line and the underlined runes answer; the rest of the line,
// the air past its end, and a clean line's gutter do not.
func TestDiagsAtCell(t *testing.T) {
	a, gutterX, codeX, rowY := diagTipTestApp(t)

	if d := a.diagsAtCell(gutterX, rowY); len(d) != 1 {
		t.Errorf("gutter of the red line: %d diagnostics, want 1", len(d))
	}
	if d := a.diagsAtCell(codeX, rowY); len(d) != 1 {
		t.Errorf("on the underline: %d diagnostics, want 1", len(d))
	}
	if d := a.diagsAtCell(codeX+3, rowY); len(d) != 1 {
		t.Errorf("last underlined rune: %d diagnostics, want 1", len(d))
	}
	if d := a.diagsAtCell(codeX+4, rowY); len(d) != 0 {
		t.Error("the rune after the range answered")
	}
	if d := a.diagsAtCell(codeX-2, rowY); len(d) != 0 {
		t.Error("the `func` keyword before the range answered")
	}
	if d := a.diagsAtCell(codeX+40, rowY); len(d) != 0 {
		t.Error("the air past the end of the line answered")
	}
	if d := a.diagsAtCell(gutterX, rowY-1); len(d) != 0 {
		t.Error("a clean line's gutter answered")
	}
}

// TestDiagTip_Lifecycle pins the pointer tooltip: motion over clean code
// arms nothing, a rest on the underline opens after the tick, a stale
// tick opens nothing, draw stamps a box that does not cover its anchor,
// and a press dismisses without being consumed outside the box.
func TestDiagTip_Lifecycle(t *testing.T) {
	a, _, codeX, rowY := diagTipTestApp(t)

	before := a.diagTip.seq
	a.noteDiagPointer(codeX, rowY-1, tcell.ButtonNone)
	if a.diagTip.seq != before {
		t.Error("a pointer over clean code armed the tip")
	}

	a.noteDiagPointer(codeX, rowY, tcell.ButtonNone)
	stale := a.diagTip.seq
	if stale == before {
		t.Fatal("a pointer on the underline armed nothing")
	}
	a.noteDiagPointer(codeX+1, rowY, tcell.ButtonNone)
	a.handleDiagTipTick(&diagTipEvent{seq: stale})
	if a.diagTip.open {
		t.Fatal("a stale tick opened the tip")
	}

	a.handleDiagTipTick(&diagTipEvent{seq: a.diagTip.seq})
	if !a.diagTip.open || len(a.diagTip.lines) == 0 ||
		!strings.Contains(a.diagTip.lines[0], "main redeclared") {
		t.Fatalf("tick left the tip %+v", a.diagTip)
	}

	a.draw()
	if b := a.diagTip.box; b.w == 0 || b.h == 0 {
		t.Fatalf("draw stamped no box: %+v", b)
	}
	if a.diagTipContains(a.diagTip.ax, a.diagTip.ay) {
		t.Error("the tooltip covers the cell it describes")
	}

	if a.noteDiagPointer(0, 0, tcell.Button1) {
		t.Error("a press away from the tooltip was consumed")
	}
	if a.diagTip.open {
		t.Error("a press left the tooltip open")
	}
}

// TestDiagGutterPress pins the click door: a press on a diagnosed line's
// gutter opens the tip without moving the caret, survives the release
// that follows, and a second click closes it; a clean line's gutter and
// the code itself are left to the caret.
func TestDiagGutterPress(t *testing.T) {
	a, gutterX, codeX, rowY := diagTipTestApp(t)
	tab := a.activeTabPtr()
	start := tab.Cursor

	press := func(x, y int) bool {
		a.noteDiagPointer(x, y, tcell.Button1) // the router's order
		return a.diagGutterPress(x, y)
	}

	if press(gutterX, rowY-1) {
		t.Error("a clean line's gutter was claimed")
	}
	if press(codeX, rowY) {
		t.Error("a press in the code was claimed — that click is the caret's")
	}

	if !press(gutterX, rowY) || !a.diagTip.open {
		t.Fatalf("gutter press left the tip %+v", a.diagTip)
	}
	if tab.Cursor != start {
		t.Errorf("cursor moved to %+v", tab.Cursor)
	}
	a.noteDiagPointer(gutterX, rowY, tcell.ButtonNone) // the release
	if !a.diagTip.open {
		t.Error("the button release closed the tip it opened")
	}

	if !press(gutterX, rowY) {
		t.Error("the closing click should still be claimed")
	}
	if a.diagTip.open {
		t.Error("a second click on the dot should close the tip")
	}
	if !press(gutterX, rowY) || !a.diagTip.open {
		t.Error("a third click should open it again")
	}
}

// TestHoverInfo_LeadsWithDiagnostics pins the keyboard door: Esc-i on a
// red line shows the message even when the server has no hover text,
// puts it ABOVE the hover text when there is some, and still flashes
// "No hover info" on a clean spot with no answer.
func TestHoverInfo_LeadsWithDiagnostics(t *testing.T) {
	a, _, _, _ := diagTipTestApp(t)
	tab := a.activeTabPtr()

	// Column 0 of the red line: outside the range, still on the line.
	tab.MoveCursorTo(editor.Position{Line: 2, Col: 0}, false)
	a.handleLSPHover(&lspHoverEvent{path: tab.Path, text: ""})
	m, ok := a.modal.(*hoverModal)
	if !ok || len(m.lines) == 0 || !strings.Contains(m.lines[0], "main redeclared") {
		t.Fatalf("no diagnostic tooltip: modal=%T %+v", a.modal, a.modal)
	}
	a.closeAllModals()

	tab.MoveCursorTo(editor.Position{Line: 2, Col: 6}, false)
	a.handleLSPHover(&lspHoverEvent{path: tab.Path, text: "func main()"})
	m, ok = a.modal.(*hoverModal)
	if !ok || len(m.lines) < 3 || !strings.HasPrefix(m.lines[0], "✗") || m.lines[len(m.lines)-1] != "func main()" {
		t.Fatalf("diagnostic did not lead the hover text: %q", m.lines)
	}
	a.closeAllModals()

	tab.MoveCursorTo(editor.Position{Line: 0, Col: 0}, false)
	a.handleLSPHover(&lspHoverEvent{path: tab.Path, text: ""})
	if a.modal != nil || !strings.Contains(a.statusMsg, "No hover info") {
		t.Errorf("clean spot: modal=%T flash=%q", a.modal, a.statusMsg)
	}
}

// TestStepProblem_FlashesMessage pins that the keyboard walk says what it
// landed on — the caret arriving on a red dot is not the answer.
func TestStepProblem_FlashesMessage(t *testing.T) {
	a, aPath, _ := newProblemsTestApp(t)
	a.openFile(aPath)
	a.activeTabPtr().MoveCursorTo(editor.Position{Line: 0, Col: 0}, false)
	a.menuNextProblem()
	if !strings.Contains(a.statusMsg, "syntax error") || !strings.HasPrefix(a.statusMsg, "✗") {
		t.Errorf("flash = %q, want the landed-on problem's message", a.statusMsg)
	}
}
