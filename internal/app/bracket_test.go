// =============================================================================
// File: internal/app/bracket_test.go
// Author: Rohan Allison <rohanthewiz@gmail.com>
// Created: 2026-08-31
// Copyright: 2026 Rohan Allison. All rights reserved.
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

// seedBracketApp opens src as a Go file so the tab carries a real syntax
// grid — the matcher's string/comment classifier reads it, so a test that
// skipped it would be exercising the naive path by accident.
func seedBracketApp(t *testing.T, src string) *App {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "b.go")
	if err := os.WriteFile(path, []byte(src), 0644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	a := newTestApp(t, dir)
	a.openFile(path)
	// One frame, because the matcher's string/comment classifier reads
	// the grid Render populates. That is not a test artifact: the verb
	// deliberately reads the LAST frame's grid rather than forcing a
	// re-lex from a keystroke handler (syntax.go's whole settle policy),
	// so "after a draw" is the only state it ever runs in for real.
	a.draw()
	return a
}

const bracketSrc = "package main\n\nfunc main() {\n\tif x {\n\t\ty()\n\t}\n}\n"

// TestGoToMatchingBracket_JumpsAndReturns is the verb's whole contract:
// the caret lands ON the partner, so the repeatable key round-trips.
func TestGoToMatchingBracket_JumpsAndReturns(t *testing.T) {
	a := seedBracketApp(t, bracketSrc)
	tab := a.activeTabPtr()

	// The '{' that opens func main's body, on line 2.
	open := editor.Position{Line: 2, Col: strings.Index("func main() {", "{")}
	tab.MoveCursorTo(open, false)
	a.menuGoToMatchingBracket()

	if tab.Cursor != (editor.Position{Line: 6, Col: 0}) {
		t.Fatalf("cursor = %v, want the closing '}' at {6 0}", tab.Cursor)
	}

	a.menuGoToMatchingBracket()
	if tab.Cursor != open {
		t.Errorf("round trip landed at %v, want %v", tab.Cursor, open)
	}
}

// TestGoToMatchingBracket_FlashesInsteadOfMoving pins the three refusals
// as three DIFFERENT messages. "no bracket here" and "this one has no
// partner" are separate facts, and one message for both would leave a
// user staring at a brace they can see is balanced.
func TestGoToMatchingBracket_FlashesInsteadOfMoving(t *testing.T) {
	a := seedBracketApp(t, "package main\n\nfunc f( {\n")
	tab := a.activeTabPtr()

	// A caret in a word names no bracket at all.
	tab.MoveCursorTo(editor.Position{Line: 0, Col: 3}, false)
	a.menuGoToMatchingBracket()
	if tab.Cursor != (editor.Position{Line: 0, Col: 3}) {
		t.Errorf("cursor moved to %v with no bracket under it", tab.Cursor)
	}
	if !strings.Contains(a.statusMsg, "cursor on a bracket") {
		t.Errorf("flash = %q, want the 'no bracket here' message", a.statusMsg)
	}

	// An unclosed '(' is a different answer, and says so.
	openCol := strings.Index("func f( {", "(")
	tab.MoveCursorTo(editor.Position{Line: 2, Col: openCol}, false)
	a.menuGoToMatchingBracket()
	if tab.Cursor.Col != openCol || tab.Cursor.Line != 2 {
		t.Errorf("cursor = %v, want to have stayed on the '('", tab.Cursor)
	}
	if !strings.Contains(a.statusMsg, "No match for (") {
		t.Errorf("flash = %q, want it to name the unmatched bracket", a.statusMsg)
	}
}

// TestGoToMatchingBracket_SkipsStringLiterals is the app-level proof that
// the classifier is wired through: the jump must clear the braces inside
// the format string rather than stopping at one.
func TestGoToMatchingBracket_SkipsStringLiterals(t *testing.T) {
	src := "package main\n\nfunc main() {\n\tprintln(\"}\")\n}\n"
	a := seedBracketApp(t, src)
	tab := a.activeTabPtr()

	tab.MoveCursorTo(editor.Position{Line: 2, Col: strings.Index("func main() {", "{")}, false)
	a.menuGoToMatchingBracket()
	if tab.Cursor != (editor.Position{Line: 4, Col: 0}) {
		t.Errorf("cursor = %v, want the real '}' at {4 0} — the one in the literal was matched",
			tab.Cursor)
	}
}

// TestGoToMatchingBracket_LeaderKey drives the real keystroke, so the
// binding can't be dropped from the table without a test noticing.
func TestGoToMatchingBracket_LeaderKey(t *testing.T) {
	a := seedBracketApp(t, bracketSrc)
	tab := a.activeTabPtr()
	tab.MoveCursorTo(editor.Position{Line: 2, Col: strings.Index("func main() {", "{")}, false)

	a.handleKey(keyEv(tcell.KeyEsc, 0))
	a.handleKey(keyEv(tcell.KeyRune, '%'))

	if tab.Cursor.Line != 6 {
		t.Errorf("Esc-%% left the cursor at %v, want line 6", tab.Cursor)
	}
}

// TestGoToMatchingBracket_MenuRow keeps the ≡ twin present and pointed at
// the same verb — macOS Terminal swallows enough input that a
// keyboard-only path is not a path.
func TestGoToMatchingBracket_MenuRow(t *testing.T) {
	a := seedBracketApp(t, bracketSrc)
	for _, g := range a.visibleMenuGroups() {
		if g.title != "Code" {
			continue
		}
		for _, it := range g.items {
			if it.label == "Go to matching bracket" {
				if it.shortcut != "esc %" {
					t.Errorf("shortcut = %q, want %q", it.shortcut, "esc %")
				}
				if it.enabled == nil || !it.enabled(a) {
					t.Error("the row should be enabled on an open text tab")
				}
				return
			}
		}
	}
	t.Fatal("no 'Go to matching bracket' row in the Code group")
}

// TestGoToMatchingBracket_CentersOffScreenPartner pins the goToLine
// policy reaching this verb: a partner far below is centered, not parked
// on the viewport's last row where its surroundings are invisible.
func TestGoToMatchingBracket_CentersOffScreenPartner(t *testing.T) {
	body := strings.Repeat("\tx()\n", 200)
	a := seedBracketApp(t, "package main\n\nfunc main() {\n"+body+"}\n")
	tab := a.activeTabPtr()
	tab.MoveCursorTo(editor.Position{Line: 2, Col: strings.Index("func main() {", "{")}, false)

	a.menuGoToMatchingBracket()
	if tab.Cursor.Line != 203 {
		t.Fatalf("cursor = %v, want the '}' on line 203", tab.Cursor)
	}
	_, _, _, eh := a.editorRect()
	if !tab.CursorLineVisible(eh) {
		t.Error("the partner should be on screen after the jump")
	}
	if tab.ScrollY >= tab.Cursor.Line {
		t.Errorf("scroll %d parked the partner on the first row, not centered", tab.ScrollY)
	}
}
