// =============================================================================
// File: internal/app/goto_test.go
// Author: Rohan Allison <rohanthewiz@gmail.com>
// Created: 2026-08-03
// Copyright: 2026 Rohan Allison. All rights reserved.
// =============================================================================

package app

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// seedGotoApp opens a tab of numbered lines so a jump's destination is
// readable from the line's own text.
func seedGotoApp(t *testing.T, lines int) *App {
	t.Helper()
	dir := t.TempDir()
	body := make([]string, lines)
	for i := range body {
		body[i] = "line" + itoa(i+1)
	}
	target := filepath.Join(dir, "n.txt")
	if err := os.WriteFile(target, []byte(strings.Join(body, "\n")), 0644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	a := newTestApp(t, dir)
	a.openFile(target)
	return a
}

// TestParseLineSpec covers the grammar, including the shapes a user gets
// by pasting a compiler's own output rather than typing a number.
func TestParseLineSpec(t *testing.T) {
	cases := []struct {
		in        string
		line, col int
		ok        bool
	}{
		{"42", 41, 0, true},
		{" 42 ", 41, 0, true},
		{"42:10", 41, 9, true},
		{"42,10", 41, 9, true},
		{"42:", 41, 0, true},
		{"42:1", 41, 0, true}, // column 1 is the start of the line
		{"1", 0, 0, true},     // 1-based in, 0-based out
		{"app.go:314:22", 313, 21, true},
		{"", 0, 0, false},
		{"abc", 0, 0, false},
		{"0", 0, 0, false}, // there is no line 0 in a 1-based world
		{"-5", 0, 0, false},
	}
	for _, c := range cases {
		line, col, ok := parseLineSpec(c.in)
		if ok != c.ok || (ok && (line != c.line || col != c.col)) {
			t.Errorf("parseLineSpec(%q) = (%d,%d,%v), want (%d,%d,%v)",
				c.in, line, col, ok, c.line, c.col, c.ok)
		}
	}
}

// TestGoToLine_MovesAndCenters pins the jump itself plus the
// center-if-off-screen policy: landing on the last visible row answers
// "where is it" but not "what is it doing".
func TestGoToLine_MovesAndCenters(t *testing.T) {
	a := seedGotoApp(t, 200)
	a.goToLine(149, 0)
	tab := a.activeTabPtr()
	if tab.Cursor.Line != 149 {
		t.Fatalf("cursor on line %d, want 149", tab.Cursor.Line)
	}
	_, _, _, eh := a.editorRect()
	if tab.ScrollY == 0 || tab.ScrollY >= 149 {
		t.Fatalf("scroll = %d — an off-screen jump should have centered", tab.ScrollY)
	}
	if !tab.CursorLineVisible(eh) {
		t.Fatal("destination line is not visible after the jump")
	}
}

// TestGoToLine_ClampsPastEOF is the stale-build-log case: a line number
// from an older version of the file lands on the last line rather than
// being refused.
func TestGoToLine_ClampsPastEOF(t *testing.T) {
	a := seedGotoApp(t, 10)
	a.goToLine(999, 0)
	if got := a.activeTabPtr().Cursor.Line; got != 9 {
		t.Fatalf("cursor on line %d, want the last line (9)", got)
	}
}

// TestGoToLine_ClampsColumnToLineLength — a column from a tab-expanded
// compiler message can exceed the line's rune count; the caret must not
// land past the end.
func TestGoToLine_ClampsColumnToLineLength(t *testing.T) {
	a := seedGotoApp(t, 3)
	a.goToLine(0, 500)
	tab := a.activeTabPtr()
	if got := tab.Cursor.Col; got != len([]rune(tab.Buffer.Lines[0])) {
		t.Fatalf("cursor col = %d, want the line's end (%d)",
			got, len([]rune(tab.Buffer.Lines[0])))
	}
}

// TestGoToLineSpec_BadInputFlashes: the user typed something and pressed
// Enter, so silence would leave them wondering whether the jump or the
// editor failed.
func TestGoToLineSpec_BadInputFlashes(t *testing.T) {
	a := seedGotoApp(t, 5)
	a.goToLineSpec("nope")
	if !strings.Contains(a.statusMsg, "nope") {
		t.Fatalf("status = %q, want a complaint naming the input", a.statusMsg)
	}
}

// TestOpenGoToLine_HintNamesTheRange pins the one piece of context that
// makes the question answerable without scrolling first.
func TestOpenGoToLine_HintNamesTheRange(t *testing.T) {
	a := seedGotoApp(t, 37)
	a.openGoToLine()
	m, ok := a.modal.(*promptModal)
	if !ok {
		t.Fatalf("modal = %T, want the shared prompt", a.modal)
	}
	if !strings.Contains(m.hint, "37") {
		t.Fatalf("hint = %q, want it to name the last line", m.hint)
	}
}
