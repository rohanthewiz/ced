// =============================================================================
// File: internal/editor/highlight_test.go
// Author: Spicer Matthews <spicer@cloudmanic.com>
// Created: 2026-04-30
// Copyright: 2026 Cloudmanic, LLC. All rights reserved.
// =============================================================================

// Tests for the syntax-highlight grid generator. Mostly shape invariants
// the renderer relies on — one row per source line, each row long enough
// to cover its line's runes, a graceful fallback for unknown or missing
// lexers — since chroma's own token assignments are an upstream concern.
//
// The one place colors ARE pinned is the Literal family, where ced has to
// take a position chroma's category numbering won't give it for free.
// See TestStyleForToken_LiteralFamilySplitsStringsFromNumbers.

package editor

import (
	"strings"
	"testing"

	"github.com/alecthomas/chroma/v2"

	"github.com/gdamore/tcell/v2"

	"github.com/rohanthewiz/ced/internal/theme"
)

// TestHighlight_GoSourceShapesGrid verifies that highlighting a snippet of
// Go produces a grid whose row count and per-row length match the source
// lines and rune counts — that's what the renderer indexes into.
func TestHighlight_GoSourceShapesGrid(t *testing.T) {
	src := "package main\n\nfunc main() {}\n"
	th := theme.Default()

	got := Highlight("main.go", src, th)
	lines := strings.Split(src, "\n")
	if len(got) != len(lines) {
		t.Fatalf("rows = %d, want %d", len(got), len(lines))
	}
	for i, ln := range lines {
		if len(got[i]) != len([]rune(ln)) {
			t.Errorf("row %d len = %d, want %d", i, len(got[i]), len([]rune(ln)))
		}
	}
}

// TestHighlight_GoKeywordIsColored confirms at least one rune in a Go source
// gets a non-default foreground — proves the lexer ran and produced styles.
func TestHighlight_GoKeywordIsColored(t *testing.T) {
	src := "package main\nfunc f() {}\n"
	th := theme.Default()

	got := Highlight("main.go", src, th)
	base := tcell.StyleDefault.Background(th.BG).Foreground(th.Text)

	differs := false
	for _, row := range got {
		for _, st := range row {
			if st != base {
				differs = true
				break
			}
		}
		if differs {
			break
		}
	}
	if !differs {
		t.Fatal("expected at least one non-base styled rune in highlighted Go source")
	}
}

// TestHighlight_UnknownExtension gracefully falls back to the plain-text
// (or analyzed) lexer instead of panicking. The grid must still match the
// source shape.
func TestHighlight_UnknownExtension(t *testing.T) {
	src := "anything goes here\nsecond line"
	th := theme.Default()

	got := Highlight("file.totallymadeup", src, th)
	lines := strings.Split(src, "\n")
	if len(got) != len(lines) {
		t.Fatalf("rows = %d, want %d", len(got), len(lines))
	}
	for i, ln := range lines {
		if len(got[i]) != len([]rune(ln)) {
			t.Errorf("row %d len = %d, want %d", i, len(got[i]), len([]rune(ln)))
		}
	}
}

// TestHighlight_EmptyInput returns a single empty row, mirroring NewBuffer's
// behaviour — strings.Split("", "\n") yields [""], so the grid has one row
// of zero runes.
func TestHighlight_EmptyInput(t *testing.T) {
	th := theme.Default()
	got := Highlight("file.go", "", th)
	if len(got) != 1 {
		t.Fatalf("expected 1 row, got %d", len(got))
	}
	if len(got[0]) != 0 {
		t.Fatalf("expected empty row, got len=%d", len(got[0]))
	}
}

// TestHighlight_MultibyteRunes makes sure rune-indexed columns are handled
// when the source contains multi-byte characters — each row must have one
// style entry per rune (not per byte).
func TestHighlight_MultibyteRunes(t *testing.T) {
	src := "// héllo\nx := 1\n"
	th := theme.Default()

	got := Highlight("main.go", src, th)
	lines := strings.Split(src, "\n")
	for i, ln := range lines {
		if len(got[i]) != len([]rune(ln)) {
			t.Errorf("row %d: style len = %d, rune len = %d", i, len(got[i]), len([]rune(ln)))
		}
	}
}

// TestHighlight_NoFilenameAnalyses lets Chroma analyse the content when no
// filename is provided. It should still return a properly shaped grid.
func TestHighlight_NoFilenameAnalyses(t *testing.T) {
	src := "package main\nfunc main() {}\n"
	th := theme.Default()

	got := Highlight("", src, th)
	lines := strings.Split(src, "\n")
	if len(got) != len(lines) {
		t.Fatalf("rows = %d, want %d", len(got), len(lines))
	}
}

// TestHighlight_DiverseTokens exercises the styleForToken switch by feeding
// it source containing keywords, strings, numbers, comments, and names, then
// confirms the grid is well-formed. We don't assert on specific colors —
// only that the function ran across the diverse token types without panic
// and produced a parallel grid.
func TestHighlight_DiverseTokens(t *testing.T) {
	src := `// comment line
package main

import "fmt"

const Answer = 42

type Foo struct{}

func (f *Foo) Bar() string {
	return "hello"
}
`
	th := theme.Default()
	got := Highlight("main.go", src, th)
	lines := strings.Split(src, "\n")
	if len(got) != len(lines) {
		t.Fatalf("rows = %d, want %d", len(got), len(lines))
	}
	for i, ln := range lines {
		if len(got[i]) != len([]rune(ln)) {
			t.Errorf("row %d len = %d, want %d", i, len(got[i]), len([]rune(ln)))
		}
	}
}

// TestStyleForToken_LiteralFamilySplitsStringsFromNumbers is the
// regression for a category bug that shipped for a long time: Chroma
// numbers its token types so LiteralString (3100) and LiteralNumber
// (3200) both divide down to Literal (3000), which meant the
// `case chroma.LiteralString` / `case chroma.LiteralNumber` arms beside
// the other Category() cases were unreachable and every string and
// number in the editor was painted with the CONSTANT color instead.
//
// It is pinned here rather than left to the eye because the wrong colors
// were perfectly plausible-looking, and because the bracket matcher now
// reads these two off the grid to decide which brackets are real code
// (bracket.go) — a regression would quietly take that with it.
func TestStyleForToken_LiteralFamilySplitsStringsFromNumbers(t *testing.T) {
	th := theme.Default()
	// Distinct sentinels: the shipped palette gives numbers and constants
	// the same orange, so the real theme could not tell this test whether
	// the fix worked.
	th.SynString = tcell.NewRGBColor(0x11, 0x11, 0x11)
	th.SynNumber = tcell.NewRGBColor(0x22, 0x22, 0x22)
	th.SynConstant = tcell.NewRGBColor(0x33, 0x33, 0x33)
	base := tcell.StyleDefault.Background(th.BG).Foreground(th.Text)

	cases := []struct {
		tt   chroma.TokenType
		want tcell.Color
		name string
	}{
		{chroma.LiteralString, th.SynString, "LiteralString"},
		{chroma.LiteralStringDouble, th.SynString, "LiteralStringDouble"},
		{chroma.LiteralStringChar, th.SynString, "LiteralStringChar"},
		{chroma.LiteralNumber, th.SynNumber, "LiteralNumber"},
		{chroma.LiteralNumberInteger, th.SynNumber, "LiteralNumberInteger"},
		{chroma.LiteralNumberFloat, th.SynNumber, "LiteralNumberFloat"},
		// A bare Literal (and its non-string, non-number siblings) still
		// lands on the constant color, which is what that arm is for.
		{chroma.Literal, th.SynConstant, "Literal"},
		{chroma.LiteralDate, th.SynConstant, "LiteralDate"},
	}
	for _, c := range cases {
		fg, _, _ := styleForToken(c.tt, th, base).Decompose()
		if fg != c.want {
			t.Errorf("%s → %v, want %v", c.name, fg, c.want)
		}
	}
}

// TestHighlight_StringContentIsStringColored proves the fix above
// reaches the grid the renderer actually paints from — the level the
// bracket matcher's classifier reads.
func TestHighlight_StringContentIsStringColored(t *testing.T) {
	th := theme.Default()
	th.SynString = tcell.NewRGBColor(0x11, 0x11, 0x11)
	const src = "x := \"ab\"\n"
	got := Highlight("f.go", src, th)
	for _, col := range []int{5, 6, 7, 8} { // the quotes and their contents
		fg, _, _ := got[0][col].Decompose()
		if fg != th.SynString {
			t.Errorf("col %d (%q) fg = %v, want SynString", col, []rune(src)[col], fg)
		}
	}
}
