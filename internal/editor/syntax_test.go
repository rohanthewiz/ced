// =============================================================================
// File: internal/editor/syntax_test.go
// Author: Rohan Allison <rohanthewiz@gmail.com>
// Created: 2026-08-03
// Copyright: 2026 Rohan Allison. All rights reserved.
// =============================================================================

package editor

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gdamore/tcell/v2"

	"github.com/rohanthewiz/ced/internal/theme"
)

// syntaxTab builds a tab over content, gives it a real highlighted grid,
// and parks the cursor at the origin — the shared fixture for the settle
// tests, which all need a grid to defer against.
func syntaxTab(t *testing.T, name, content string) *Tab {
	t.Helper()
	tab := &Tab{Path: name, Buffer: NewBuffer(content), StyleStale: true}
	tab.initUndo()
	tab.Styles = Highlight(name, content, theme.Default())
	tab.StyleStale = false
	return tab
}

// TestSyntaxDefer_TypingDoesNotRelexPerKeystroke is the whole point of
// the settle policy: an intra-line edit leaves the grid stale but still
// paintable, so Render skips the O(file) Chroma pass while the user is
// mid-burst.
func TestSyntaxDefer_TypingDoesNotRelexPerKeystroke(t *testing.T) {
	tab := syntaxTab(t, "x.go", "package main\n\nfunc main() {}\n")
	tab.Cursor = Position{Line: 2, Col: 5}
	tab.Anchor = tab.Cursor

	tab.InsertRune('X')
	if !tab.StyleStale {
		t.Fatal("an edit must mark the grid stale")
	}
	if tab.needsRelex() {
		t.Fatal("a fresh intra-line edit must defer the re-lex, not demand one")
	}

	// Once the buffer has been quiet for the settle window, it comes due.
	tab.lastEditAt = time.Now().Add(-2 * SyntaxSettle)
	if !tab.needsRelex() {
		t.Fatal("re-lex must come due after the settle window elapses")
	}
}

// TestSyntaxDefer_StructuralEditRelexesNow pins the boundary the deferral
// stops at. Anything that changes the line structure would leave the
// grid's rows misaligned with the buffer's, which repaints everything
// below the edit in the wrong colors — so it re-lexes immediately.
func TestSyntaxDefer_StructuralEditRelexesNow(t *testing.T) {
	cases := []struct {
		name string
		edit func(*Tab)
	}{
		{"newline", func(tb *Tab) { tb.InsertString("\n") }},
		{"multi-line paste", func(tb *Tab) { tb.InsertString("a\nb") }},
		{"join lines", func(tb *Tab) {
			tb.Cursor = Position{Line: 2, Col: 0}
			tb.Anchor = tb.Cursor
			tb.Backspace()
		}},
		{"undo", func(tb *Tab) { tb.InsertRune('X'); tb.Undo() }},
		{"line duplicate", func(tb *Tab) { tb.DuplicateLines() }},
		{"comment toggle", func(tb *Tab) { tb.ToggleLineComment() }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tab := syntaxTab(t, "x.go", "package main\n\nfunc main() {}\n")
			tab.Cursor = Position{Line: 2, Col: 5}
			tab.Anchor = tab.Cursor

			tc.edit(tab)
			if !tab.needsRelex() {
				t.Fatalf("%s must force an immediate re-lex", tc.name)
			}
			if tab.styleDefer {
				t.Fatalf("%s must clear the defer flag", tc.name)
			}
		})
	}
}

// TestSyntaxPatch_InsertKeepsTailAligned covers the artifact the patch
// exists to prevent: without it, everything to the right of the caret
// would wear its neighbour's color for the length of the settle window.
// The inserted rune inherits the style to its left; the tail is untouched.
func TestSyntaxPatch_InsertKeepsTailAligned(t *testing.T) {
	tab := syntaxTab(t, "x.go", `x := "hello world"`)
	before := append([]tcell.Style(nil), tab.Styles[0]...)
	tab.Cursor = Position{Line: 0, Col: 7} // inside the string literal
	tab.Anchor = tab.Cursor

	tab.InsertRune('Z')

	row := tab.Styles[0]
	if len(row) != len(before)+1 {
		t.Fatalf("row should grow by one style: got %d want %d", len(row), len(before)+1)
	}
	if row[7] != before[6] {
		t.Fatal("an inserted rune must inherit the style of its left neighbour")
	}
	for i := 8; i < len(row); i++ {
		if row[i] != before[i-1] {
			t.Fatalf("tail style at %d shifted; the patch failed to keep it aligned", i)
		}
	}
}

// TestSyntaxPatch_DeleteKeepsTailAligned is the delete-side twin.
func TestSyntaxPatch_DeleteKeepsTailAligned(t *testing.T) {
	tab := syntaxTab(t, "x.go", `x := "hello world"`)
	before := append([]tcell.Style(nil), tab.Styles[0]...)
	tab.Cursor = Position{Line: 0, Col: 8}
	tab.Anchor = tab.Cursor

	tab.Backspace()

	row := tab.Styles[0]
	if len(row) != len(before)-1 {
		t.Fatalf("row should shrink by one style: got %d want %d", len(row), len(before)-1)
	}
	for i := 7; i < len(row); i++ {
		if row[i] != before[i+1] {
			t.Fatalf("tail style at %d shifted after delete", i)
		}
	}
}

// TestSyntaxPatch_SelectionDeleteOnOneLine pins that a selection delete
// within a single line stays on the cheap path — it's the common
// select-a-word-and-retype gesture, which would otherwise re-lex twice
// (once for the delete, once for the first replacement rune).
func TestSyntaxPatch_SelectionDeleteOnOneLine(t *testing.T) {
	tab := syntaxTab(t, "x.go", "alpha bravo charlie")
	tab.Anchor = Position{Line: 0, Col: 6}
	tab.Cursor = Position{Line: 0, Col: 11}

	tab.DeleteSelection()

	if tab.Buffer.Lines[0] != "alpha  charlie" {
		t.Fatalf("unexpected buffer: %q", tab.Buffer.Lines[0])
	}
	if tab.needsRelex() {
		t.Fatal("a single-line selection delete should defer, not force")
	}
	if got, want := len(tab.Styles[0]), len("alpha  charlie"); got != want {
		t.Fatalf("style row out of step with the line: got %d want %d", got, want)
	}
}

// TestSyntaxPatch_BackwardSelectionDelete guards the ordering: a
// selection dragged right-to-left arrives with Anchor after Cursor, and
// an unordered range would compute a negative width and silently throw
// the grid away.
func TestSyntaxPatch_BackwardSelectionDelete(t *testing.T) {
	tab := syntaxTab(t, "x.go", "alpha bravo charlie")
	tab.Anchor = Position{Line: 0, Col: 11}
	tab.Cursor = Position{Line: 0, Col: 6}

	tab.DeleteSelection()

	if tab.needsRelex() {
		t.Fatal("a backward single-line selection delete should still defer")
	}
	if got, want := len(tab.Styles[0]), len("alpha  charlie"); got != want {
		t.Fatalf("style row out of step with the line: got %d want %d", got, want)
	}
}

// TestSyntaxOff_LargeFileNeverHighlights pins the size threshold: past
// MaxHighlightBytes the tab opens with highlighting off and never asks
// for a re-lex, however much it is edited.
func TestSyntaxOff_LargeFileNeverHighlights(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "big.go")
	big := strings.Repeat("// filler line to push this file over the limit\n", (MaxHighlightBytes/47)+64)
	if err := os.WriteFile(path, []byte(big), 0o644); err != nil {
		t.Fatal(err)
	}

	tab, err := NewTab(path)
	if err != nil {
		t.Fatal(err)
	}
	if !tab.SyntaxOff {
		t.Fatalf("a %d-byte file must open with highlighting off", len(big))
	}
	tab.InsertRune('x')
	if tab.needsRelex() {
		t.Fatal("a syntax-off tab must never re-lex")
	}
	if tab.SyntaxSettleWait() != 0 {
		t.Fatal("a syntax-off tab must never ask the app for a settle timer")
	}
}

// TestSyntaxSettleWait_OnlyWhileDeferred pins the timer contract the app
// side depends on: a wake-up is requested only while a deferred re-lex is
// genuinely waiting on the clock. Anything else returns zero, because an
// event-driven loop must not hold timers it has no use for.
func TestSyntaxSettleWait_OnlyWhileDeferred(t *testing.T) {
	tab := syntaxTab(t, "x.go", "package main\n")
	if got := tab.SyntaxSettleWait(); got != 0 {
		t.Fatalf("a clean tab must want no timer, got %v", got)
	}

	tab.InsertRune('X')
	if got := tab.SyntaxSettleWait(); got <= 0 || got > SyntaxSettle {
		t.Fatalf("a fresh edit should want a wake-up inside the settle window, got %v", got)
	}

	// Already due: the redraw that follows this dispatch will re-lex, so
	// arming a timer for it would be a wasted wake-up.
	tab.lastEditAt = time.Now().Add(-2 * SyntaxSettle)
	if got := tab.SyntaxSettleWait(); got != 0 {
		t.Fatalf("an already-due re-lex needs no timer, got %v", got)
	}

	tab.InvalidateStyles()
	if got := tab.SyntaxSettleWait(); got != 0 {
		t.Fatalf("an invalidated grid re-lexes on the next render, got %v", got)
	}
}

// TestSyntaxDefer_RenderPaintsPatchedGrid is the end-to-end check: a
// character typed mid-burst renders in the color of what it was typed
// into, straight from the patched grid, with no re-lex in between.
func TestSyntaxDefer_RenderPaintsPatchedGrid(t *testing.T) {
	scr := tcell.NewSimulationScreen("UTF-8")
	if err := scr.Init(); err != nil {
		t.Fatal(err)
	}
	defer scr.Fini()
	scr.SetSize(60, 6)

	th := theme.Default()
	tab := syntaxTab(t, "x.go", "x := \"hello\"\n")
	tab.Cursor = Position{Line: 0, Col: 9} // inside the string
	tab.Anchor = tab.Cursor
	tab.InsertRune('Z')
	if tab.needsRelex() {
		t.Fatal("precondition: the edit should be deferred")
	}
	tab.Render(scr, th, 0, 0, 60, 5)
	scr.Show()

	// The typed rune must carry the string literal's color, not the
	// editor's plain foreground.
	cells, _, _ := scr.GetContents()
	const contentX = gutterWidth + 1
	got := cells[contentX+9]
	if got.Runes[0] != 'Z' {
		t.Fatalf("expected the typed rune at the caret, got %q", got.Runes[0])
	}
	// Compared against the neighbour actually on screen rather than a
	// theme constant: the contract is "inherits the color of what it was
	// typed into", and pinning a literal color would just re-assert
	// Chroma's classification of the fixture.
	fg, _, _ := got.Style.Decompose()
	neighbourFG, _, _ := cells[contentX+8].Style.Decompose()
	if fg != neighbourFG {
		t.Fatalf("typed rune should inherit its left neighbour's color %v, got %v", neighbourFG, fg)
	}
	if plain, _, _ := cells[contentX].Style.Decompose(); fg == plain {
		t.Fatal("fixture is not actually highlighted — the assertion above proves nothing")
	}
	if !tab.StyleStale {
		t.Fatal("render must not have consumed the staleness before the settle window")
	}
}
