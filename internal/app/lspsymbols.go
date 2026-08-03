// =============================================================================
// File: internal/app/lspsymbols.go
// Author: Rohan Allison <rohanthewiz@gmail.com>
// Created: 2026-08-03
// Copyright: 2026 Rohan Allison. All rights reserved.
// =============================================================================

// lspsymbols.go is "Go to symbol in file" — every declaration in the
// active document, listed in the command palette, jump on Enter.
//
// It is the third code-intelligence verb after definition and hover, and
// it follows both of their contracts exactly: the request runs off-loop
// and lands as a custom tcell event (lspSymbolsEvent), a response for a
// document the user has already left is dropped, and no server means the
// row simply dims. What it adds is a SURFACE, and that surface is the
// palette — the house rule that every choose-one-from-a-list UI reuses
// openPicker rather than growing a list modal of its own.
//
//	Esc D / ≡ Code ──► menuGoToSymbol ──goroutine──► documentSymbol
//	                                                       │
//	     openPicker("Go to symbol") ◄── lspSymbolsEvent ◄───┘
//	                    │
//	          pick ──► recordNav + MoveCursorTo (+ center if off-screen)
//
// Why a dedicated picker rather than a palette SOURCE (palette.go's doc
// comment floats the idea): sources are collected synchronously at open,
// and symbols cost a round trip to gopls. Feeding them in would either
// block the palette on a server that may be cold, or make the palette's
// contents arrive late and reorder under the user's fingers. A verb of
// its own asks the question first and shows the list once it has one.

package app

import (
	"strings"
	"time"

	"github.com/rohanthewiz/ced/internal/lsp"
)

// lspSymbolsEvent carries a documentSymbol response back to the main
// loop. path is the document the request was made against, so a
// response that outlived the user's attention can be dropped.
type lspSymbolsEvent struct {
	when time.Time
	path string
	syms []lsp.Symbol
	err  error
}

// When satisfies the tcell.Event interface.
func (e *lspSymbolsEvent) When() time.Time { return e.when }

// menuGoToSymbol fires an async documentSymbol request for the active
// document; the result lands as an lspSymbolsEvent.
func (a *App) menuGoToSymbol() {
	a.closeMenu()
	t := a.activeTabPtr()
	if t == nil || !a.hasLSPActions() {
		return
	}
	client := a.lsp.client
	scr := a.screen
	path := t.Path
	// The outline must describe what is on screen, not what the server
	// was last told — the same flush definition and hover do, and it
	// matters more here: an unsynced new function simply wouldn't be in
	// the list, which reads as the feature being broken.
	a.lspFlushChange(t)
	go func() {
		syms, err := client.DocumentSymbols(path)
		_ = scr.PostEvent(&lspSymbolsEvent{
			when: time.Now(), path: path, syms: syms, err: err,
		})
	}()
}

// handleLSPSymbols lands a documentSymbol response and opens the picker.
// A response for a tab the user has already left is dropped — a symbol
// list for a different file would be worse than none, because every row
// in it would jump somewhere unrelated.
func (a *App) handleLSPSymbols(e *lspSymbolsEvent) {
	t := a.activeTabPtr()
	if t == nil || t.Path != e.path {
		return
	}
	if e.err != nil || len(e.syms) == 0 {
		a.flash("No symbols in this file")
		return
	}
	items := make([]paletteItem, 0, len(e.syms))
	for _, s := range e.syms {
		items = append(items, paletteItem{
			label: symbolLabel(s),
			run:   func(app *App) { app.goToSymbol(e.path, s) },
		})
	}
	a.openPicker("Go to symbol", items)
}

// symbolLabel renders one picker row: two spaces of indent per nesting
// level, the name, then the kind word.
//
// The kind goes LAST on purpose. The fuzzy scorer rewards early matches,
// so a leading "function " would make every row score alike on the first
// letters the user types; trailing, it still lets "handlekeyfunc" narrow
// to functions while leaving the name in the position that ranks. The
// indent survives filtering as pure decoration — harmless, and it's what
// makes an unfiltered list read as the file's outline.
func symbolLabel(s lsp.Symbol) string {
	label := strings.Repeat("  ", s.Depth) + s.Name
	if kind := lsp.SymbolKindName(s.Kind); kind != "" {
		label += "  " + kind
	}
	return label
}

// goToSymbol jumps the cursor to a picked symbol, recording the
// departure in the navigation history so Go back retraces it.
//
// The nav record is explicit for the same reason the definition jump's
// is: this is (almost always) a SAME-FILE move, and openFile's
// path-change recording never sees it. The path is re-checked because a
// picker owns the keyboard but not the world — the response's tab is
// still the one this row was built for, or the jump is refused rather
// than landing a stale position in whatever is open now.
func (a *App) goToSymbol(path string, s lsp.Symbol) {
	t := a.activeTabPtr()
	if t == nil || t.Path != path || t.Buffer == nil {
		return
	}
	from, ok := a.currentNavLoc()
	if ok {
		a.recordNav(from)
	}
	t.MoveCursorTo(editorPosFor(t, s.Pos), false)
	// Center an off-screen landing, leave an on-screen one alone — the
	// goToLine / find-all policy. A minimal scroll parks a declaration on
	// the last row, which answers "where is it?" but not "what does it
	// look like?", and the body is the whole reason you jumped.
	if _, _, ew, eh := a.editorRect(); !t.CursorLineVisible(eh) {
		t.CenterOnCursor(ew, eh)
	}
}
