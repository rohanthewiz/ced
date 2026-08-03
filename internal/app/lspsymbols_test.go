// =============================================================================
// File: internal/app/lspsymbols_test.go
// Author: Rohan Allison <rohanthewiz@gmail.com>
// Created: 2026-08-03
// Copyright: 2026 Rohan Allison. All rights reserved.
// =============================================================================

package app

import (
	"strings"
	"testing"
	"time"

	"github.com/rohanthewiz/ced/internal/editor"
	"github.com/rohanthewiz/ced/internal/lsp"
)

// symbolTestApp seeds a Go file long enough to scroll, opens it, and
// returns the app plus its fake connection. Every symbol test needs the
// same three things.
func symbolTestApp(t *testing.T) (*App, *fakeLSPConn, string) {
	t.Helper()
	a, fake, goPath := newLSPTestApp(t)
	// A tall buffer so "was the jump centered?" is a real question.
	var b strings.Builder
	b.WriteString("package main\n")
	for i := range 200 {
		b.WriteString("// filler ")
		b.WriteString(itoa(i))
		b.WriteString("\n")
	}
	a.openFile(goPath)
	a.activeTabPtr().Buffer = editor.NewBuffer(b.String())
	return a, fake, goPath
}

// TestMenuGoToSymbol_FlushesAndRequests pins the request side: the
// outline must describe what is on screen, so a pending edit is flushed
// before the question is asked — an unsynced new function simply
// wouldn't be in the list, which reads as the feature being broken.
func TestMenuGoToSymbol_FlushesAndRequests(t *testing.T) {
	a, fake, goPath := newLSPTestApp(t)
	a.openFile(goPath)
	tab := a.activeTabPtr()
	tab.InsertRune('x')

	a.menuGoToSymbol()
	// The request runs on a goroutine; wait for its call to be recorded.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		calls := fake.callLog()
		if len(calls) > 0 && calls[len(calls)-1] == "documentSymbol:main.go" {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}

	calls := fake.callLog()
	if len(calls) < 3 {
		t.Fatalf("calls = %v, want didOpen + didChange + documentSymbol", calls)
	}
	if calls[1] != "didChange:main.go:2" {
		t.Errorf("calls[1] = %q, want the pre-request flush", calls[1])
	}
	if calls[2] != "documentSymbol:main.go" {
		t.Errorf("calls[2] = %q, want documentSymbol", calls[2])
	}
}

// TestHandleLSPSymbols_OpensPicker pins the surface: the response opens
// the palette as a picker (the house rule for choose-one-from-a-list),
// one row per symbol, in the server's document order.
func TestHandleLSPSymbols_OpensPicker(t *testing.T) {
	a, _, goPath := symbolTestApp(t)
	a.handleLSPSymbols(&lspSymbolsEvent{
		when: time.Now(), path: goPath,
		syms: []lsp.Symbol{
			{Name: "Tab", Kind: 23, Pos: lsp.Position{Line: 10}},
			{Name: "Render", Kind: 6, Depth: 1, Pos: lsp.Position{Line: 20}},
		},
	})

	m, ok := a.modal.(*paletteModal)
	if !ok {
		t.Fatalf("modal = %T, want the picker", a.modal)
	}
	if m.title != "Go to symbol" {
		t.Errorf("title = %q, want %q", m.title, "Go to symbol")
	}
	if len(m.items) != 2 {
		t.Fatalf("items = %d, want 2", len(m.items))
	}
	if m.items[0].label != "Tab  struct" {
		t.Errorf("row 0 = %q, want %q", m.items[0].label, "Tab  struct")
	}
	if m.items[1].label != "  Render  method" {
		t.Errorf("row 1 = %q, want a nested method row", m.items[1].label)
	}
}

// TestHandleLSPSymbols_DropsStaleAndEmpty pins the two ways the response
// declines to open anything: a list for a document the user has already
// left would jump every row somewhere unrelated, and an empty list has
// nothing to show — both flash instead.
func TestHandleLSPSymbols_DropsStaleAndEmpty(t *testing.T) {
	a, _, goPath := symbolTestApp(t)

	a.handleLSPSymbols(&lspSymbolsEvent{
		when: time.Now(), path: goPath + ".other",
		syms: []lsp.Symbol{{Name: "X", Kind: 12}},
	})
	if a.modal != nil {
		t.Fatal("a response for another document must not open a picker")
	}

	a.handleLSPSymbols(&lspSymbolsEvent{when: time.Now(), path: goPath})
	if a.modal != nil {
		t.Fatal("an empty symbol list must not open a picker")
	}
	if !strings.Contains(a.statusMsg, "No symbols") {
		t.Errorf("status = %q, want the empty-list flash", a.statusMsg)
	}
}

// TestGoToSymbol_JumpsCentersAndRecordsNav pins what picking a row does:
// the cursor lands on the symbol's NAME, an off-screen landing is
// centered rather than minimally scrolled (goToLine's policy — the body
// is the reason you jumped), and the departure point goes on the
// navigation stack, because a same-file jump is invisible to openFile's
// path-change recording.
func TestGoToSymbol_JumpsCentersAndRecordsNav(t *testing.T) {
	a, _, goPath := symbolTestApp(t)
	tab := a.activeTabPtr()
	tab.MoveCursorTo(editor.Position{Line: 0, Col: 0}, false)
	before := len(a.nav.back)

	a.goToSymbol(goPath, lsp.Symbol{Name: "Deep", Kind: 12, Pos: lsp.Position{Line: 150, Character: 5}})

	if tab.Cursor.Line != 150 || tab.Cursor.Col != 5 {
		t.Errorf("cursor = %v, want line 150 col 5", tab.Cursor)
	}
	if len(a.nav.back) != before+1 {
		t.Fatalf("nav stack = %d entries, want %d", len(a.nav.back), before+1)
	}
	if got := a.nav.back[len(a.nav.back)-1]; got.path != goPath || got.pos.Line != 0 {
		t.Errorf("recorded departure = %+v, want line 0 of the same file", got)
	}
	_, _, _, eh := a.editorRect()
	if tab.ScrollY == 0 || tab.ScrollY >= 150 {
		t.Errorf("ScrollY = %d, want the line centered in a %d-row view", tab.ScrollY, eh)
	}
}

// TestGoToSymbol_RefusesWrongTab pins the re-check: a picker owns the
// keyboard but not the world, so a row built for one document must
// never land its position in whatever is open when it fires.
func TestGoToSymbol_RefusesWrongTab(t *testing.T) {
	a, _, goPath := symbolTestApp(t)
	tab := a.activeTabPtr()
	tab.MoveCursorTo(editor.Position{Line: 3, Col: 0}, false)

	a.goToSymbol(goPath+".gone", lsp.Symbol{Name: "X", Kind: 12, Pos: lsp.Position{Line: 100}})

	if tab.Cursor.Line != 3 {
		t.Errorf("cursor moved to %v; a stale row must be refused", tab.Cursor)
	}
}

// TestSymbolLabel pins the row format: the kind goes LAST so the fuzzy
// scorer still ranks on the name the user types, and nesting shows as
// two spaces per level so an unfiltered list reads as the file's outline.
// An unknown kind costs a blank column, not a stray separator.
func TestSymbolLabel(t *testing.T) {
	cases := []struct {
		sym  lsp.Symbol
		want string
	}{
		{lsp.Symbol{Name: "Foo", Kind: 12}, "Foo  function"},
		{lsp.Symbol{Name: "Bar", Kind: 6, Depth: 2}, "    Bar  method"},
		{lsp.Symbol{Name: "Odd", Kind: 99}, "Odd"},
	}
	for _, tc := range cases {
		if got := symbolLabel(tc.sym); got != tc.want {
			t.Errorf("symbolLabel(%+v) = %q, want %q", tc.sym, got, tc.want)
		}
	}
}
