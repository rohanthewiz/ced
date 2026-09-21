// =============================================================================
// File: internal/app/lsphighlight_test.go
// Author: Rohan Allison <rohanthewiz@gmail.com>
// Created: 2026-09-21
// Copyright: 2026 Rohan Allison. All rights reserved.
// =============================================================================

package app

import (
	"strings"
	"testing"

	"github.com/gdamore/tcell/v2"

	"github.com/rohanthewiz/ced/internal/editor"
	"github.com/rohanthewiz/ced/internal/lsp"
)

// hlRange builds a single-line highlight.
func hlRange(line, from, to, kind int) lsp.DocumentHighlight {
	return lsp.DocumentHighlight{Kind: kind, Range: lsp.Range{
		Start: lsp.Position{Line: line, Character: from},
		End:   lsp.Position{Line: line, Character: to},
	}}
}

// TestHighlightSymbol_InstallsUses drives the verb end to end: the
// answer lands on the tab as rune-coordinate uses with the write flagged,
// and the flash reports the split.
func TestHighlightSymbol_InstallsUses(t *testing.T) {
	a, fake, goPath := newLSPTestApp(t)
	a.openFile(goPath)
	tab := a.activeTabPtr()
	tab.MoveCursorTo(editor.Position{Line: 2, Col: 6}, false) // on `main`
	fake.hlUses = []lsp.DocumentHighlight{
		hlRange(2, 5, 9, lsp.HighlightWrite),
		hlRange(0, 8, 12, lsp.HighlightRead),
	}

	a.menuHighlightSymbol()
	pumpAppEvents(t, a, func() bool { return len(tab.LiveSymbolUses()) > 0 })

	uses := tab.LiveSymbolUses()
	if len(uses) != 2 || !uses[0].Write || uses[1].Write {
		t.Errorf("uses = %+v", uses)
	}
	if !strings.Contains(a.statusMsg, "2 uses") || !strings.Contains(a.statusMsg, "1 of them writes") {
		t.Errorf("flash = %q", a.statusMsg)
	}
}

// TestHandleLSPHighlight_DropsAMovedBuffer pins the staleness rule: an
// answer measured against a revision that no longer exists paints
// nothing — a box on the wrong word is the one thing this must not show.
func TestHandleLSPHighlight_DropsAMovedBuffer(t *testing.T) {
	a, _, goPath := newLSPTestApp(t)
	a.openFile(goPath)
	tab := a.activeTabPtr()
	rev := tab.EditRev
	tab.InsertRune('x')

	a.handleLSPHighlight(&lspHighlightEvent{path: goPath, rev: rev, word: "main",
		uses: []lsp.DocumentHighlight{hlRange(2, 5, 9, lsp.HighlightRead)}})

	if tab.LiveSymbolUses() != nil {
		t.Error("a stale answer must not install")
	}
}

// TestHighlightSymbol_EscClearsWithoutConsuming pins the side effect:
// Esc drops the set, and still arms the leader like any other Esc.
func TestHighlightSymbol_EscClearsWithoutConsuming(t *testing.T) {
	a, _, goPath := newLSPTestApp(t)
	a.openFile(goPath)
	tab := a.activeTabPtr()
	tab.SetSymbolUses([]editor.SymbolUse{{Start: editor.Position{Line: 2, Col: 5}, End: editor.Position{Line: 2, Col: 9}}})

	a.handleKey(tcell.NewEventKey(tcell.KeyEsc, 0, tcell.ModNone))

	if tab.LiveSymbolUses() != nil {
		t.Error("Esc should clear the semantic highlight")
	}
}

// TestHandleLSPHighlight_EmptyAnswerFlashes pins the no-result path: it
// says so, and clears any older set rather than leaving it to mislead.
func TestHandleLSPHighlight_EmptyAnswerFlashes(t *testing.T) {
	a, _, goPath := newLSPTestApp(t)
	a.openFile(goPath)
	tab := a.activeTabPtr()
	tab.SetSymbolUses([]editor.SymbolUse{{Start: editor.Position{Line: 2, Col: 5}, End: editor.Position{Line: 2, Col: 9}}})

	a.handleLSPHighlight(&lspHighlightEvent{path: goPath, rev: tab.EditRev, word: "main"})

	if tab.LiveSymbolUses() != nil || !strings.Contains(a.statusMsg, "No uses") {
		t.Errorf("uses=%v flash=%q", tab.LiveSymbolUses(), a.statusMsg)
	}
}
