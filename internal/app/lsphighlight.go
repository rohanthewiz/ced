// =============================================================================
// File: internal/app/lsphighlight.go
// Author: Rohan Allison <rohanthewiz@gmail.com>
// Created: 2026-09-21
// Copyright: 2026 Rohan Allison. All rights reserved.
// =============================================================================

// lsphighlight.go is "Highlight symbol uses" — the language server's
// answer to the question the matching-word highlight guesses at. The
// display half, and the argument for why a second highlight exists at
// all, live in editor/symbolhl.go; this file is the request.
//
//	≡ Code ──► menuHighlightSymbol ──goroutine──► documentHighlight
//	                                                    │
//	   Tab.SetSymbolUses ◄── lspHighlightEvent ◄────────┘
//	   (dropped by the next edit; Esc clears it as a side effect)
//
// IT IS A VERB, NOT AMBIENT, and that is the word highlighter's own rule
// applied honestly. That source re-runs on every caret move precisely
// because it is a window-scoped text scan that costs nothing; this is a
// round trip to a server, and wiring it to caret travel would spend one
// per arrow key (copilot_ghost.go refuses the same trade: "cursor travel
// never spends a request"). So the ambient layer stays the free guess,
// and this is the deliberate, exact answer — summoned when two `err`s in
// one function make the guess useless.
//
// Validated like ghost text: the answer is pinned to the (path, EditRev)
// it was asked under and dropped if either moved. No leader key.

package app

import (
	"fmt"
	"time"

	"github.com/rohanthewiz/ced/internal/editor"
	"github.com/rohanthewiz/ced/internal/lsp"
)

// lspHighlightEvent carries a documentHighlight answer to the main loop.
type lspHighlightEvent struct {
	when time.Time
	path string
	rev  int // Tab.EditRev at ask time
	word string
	uses []lsp.DocumentHighlight
	err  error
}

// When satisfies the tcell.Event interface.
func (e *lspHighlightEvent) When() time.Time { return e.when }

// menuHighlightSymbol asks the server which occurrences in this file are
// the symbol under the cursor.
func (a *App) menuHighlightSymbol() {
	a.closeMenu()
	t := a.activeTabPtr()
	if t == nil || !a.hasLSPActions() {
		return
	}
	word := cursorWord(t)
	if word == "" {
		a.flash("Highlight symbol uses: put the cursor on a symbol first")
		return
	}
	client, scr := a.lspClientFor(t.Path), a.screen
	if client == nil || scr == nil {
		return
	}
	// Flush first, then stamp: the revision recorded has to be the one the
	// server is about to answer from.
	a.lspFlushChange(t)
	path, rev, pos := t.Path, t.EditRev, lspPosFor(t, t.Cursor)
	go func() {
		uses, err := client.DocumentHighlights(path, pos)
		_ = scr.PostEvent(&lspHighlightEvent{
			when: time.Now(), path: path, rev: rev, word: word, uses: uses, err: err,
		})
	}()
}

// handleLSPHighlight installs the answer on the tab it was asked about —
// found by PATH, not by being active: the highlight is a property of that
// document, and it is still correct if the user glanced at another tab
// during the round trip. A buffer that moved is the one disqualifier.
func (a *App) handleLSPHighlight(e *lspHighlightEvent) {
	t := a.tabByPath(e.path)
	if t == nil || t.EditRev != e.rev {
		return
	}
	if e.err != nil || len(e.uses) == 0 {
		t.ClearSymbolUses()
		a.flash(fmt.Sprintf("No uses of %q found", e.word) + a.lspLoadingNote(e.path))
		return
	}
	uses := make([]editor.SymbolUse, 0, len(e.uses))
	writes := 0
	for _, u := range e.uses {
		// A range past EOF would mean the server and the buffer disagree
		// about the text; editorPosFor clamps, and a clamped-to-empty use
		// is skipped rather than painted somewhere it does not belong.
		start, end := editorPosFor(t, u.Range.Start), editorPosFor(t, u.Range.End)
		if !editor.PosLess(start, end) {
			continue
		}
		w := u.Kind == lsp.HighlightWrite
		if w {
			writes++
		}
		uses = append(uses, editor.SymbolUse{Start: start, End: end, Write: w})
	}
	t.SetSymbolUses(uses)
	a.flash(fmt.Sprintf("%q: %d uses in this file, %d of them writes (underlined) — Esc clears",
		e.word, len(uses), writes))
}

// clearSymbolUses is the Esc side effect: drop the active tab's semantic
// highlight. Never consumes the keystroke.
func (a *App) clearSymbolUses() {
	if t := a.activeTabPtr(); t != nil {
		t.ClearSymbolUses()
	}
}
