// =============================================================================
// File: internal/editor/symbolhl_test.go
// Author: Rohan Allison <rohanthewiz@gmail.com>
// Created: 2026-09-21
// Copyright: 2026 Rohan Allison. All rights reserved.
// =============================================================================

package editor

import (
	"testing"

	"github.com/rohanthewiz/ced/internal/theme"
)

// symbolTab builds a tab with `err` three times and the caret on the
// first, so the textual highlight has something to say by default.
func symbolTab() *Tab {
	t := &Tab{Buffer: NewBuffer("err := f()\nerr = g()\n// err\n"), WordHighlight: true}
	t.Cursor, t.Anchor = Position{Line: 0, Col: 1}, Position{Line: 0, Col: 1}
	return t
}

// TestSymbolUses_PaintWritesUnderlined pins the paint: every use gets the
// word highlight's fill, and only writes carry the underline.
func TestSymbolUses_PaintWritesUnderlined(t *testing.T) {
	tab := symbolTab()
	tab.SetSymbolUses([]SymbolUse{
		{Start: Position{0, 0}, End: Position{0, 3}, Write: true},
		{Start: Position{1, 0}, End: Position{1, 3}},
	})
	th := theme.Default()
	spans, _ := symbolUseSource{}.Decorations(tab, th, 0, 2)
	if len(spans) != 2 {
		t.Fatalf("spans = %d, want 2", len(spans))
	}
	if !spans[0].Delta.Underline || spans[1].Delta.Underline {
		t.Errorf("underline = %v/%v, want write only", spans[0].Delta.Underline, spans[1].Delta.Underline)
	}
	if !spans[0].Delta.SetBG || spans[0].Delta.BG != th.WordHL {
		t.Error("uses should take the word highlight's fill")
	}
	// Window culling: a set entirely above the window paints nothing.
	if spans, _ := (symbolUseSource{}).Decorations(tab, th, 2, 2); len(spans) != 0 {
		t.Errorf("off-window spans = %d", len(spans))
	}
}

// TestSymbolUses_ReplaceTheWordHighlight pins the stand-down: while a
// semantic set is live the textual wash is silent — it would tint the
// `err` in the comment, which is exactly what the server ruled out.
func TestSymbolUses_ReplaceTheWordHighlight(t *testing.T) {
	tab := symbolTab()
	th := theme.Default()
	if spans, _ := (wordHighlightSource{}).Decorations(tab, th, 0, 2); len(spans) != 3 {
		t.Fatalf("textual highlight = %d spans, want 3 (the precondition)", len(spans))
	}
	tab.SetSymbolUses([]SymbolUse{{Start: Position{0, 0}, End: Position{0, 3}}})
	if spans, _ := (wordHighlightSource{}).Decorations(tab, th, 0, 2); len(spans) != 0 {
		t.Errorf("textual highlight still painting %d spans under a live set", len(spans))
	}
}

// TestSymbolUses_DieWithTheRevision pins staleness: one edit and the set
// is gone — its columns were measured against text that no longer
// exists — and the textual highlight takes over again.
func TestSymbolUses_DieWithTheRevision(t *testing.T) {
	tab := symbolTab()
	tab.SetSymbolUses([]SymbolUse{{Start: Position{0, 0}, End: Position{0, 3}}})
	tab.InsertRune('x')
	if tab.LiveSymbolUses() != nil {
		t.Error("an edit should retire the set")
	}
	if spans, _ := (symbolUseSource{}).Decorations(tab, theme.Default(), 0, 2); len(spans) != 0 {
		t.Error("a stale set must not paint")
	}
	if !tab.ClearSymbolUses() || tab.ClearSymbolUses() {
		t.Error("ClearSymbolUses should report true once, then false")
	}
}
