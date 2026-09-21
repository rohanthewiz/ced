// =============================================================================
// File: internal/app/lspgoto.go
// Author: Rohan Allison <rohanthewiz@gmail.com>
// Created: 2026-09-21
// Copyright: 2026 Rohan Allison. All rights reserved.
// =============================================================================

// lspgoto.go is "Go to implementation" and "Go to type definition" — the
// two siblings of go-to-definition that share its wire shape exactly and
// differ from it in one way that matters: THE ANSWER IS OFTEN PLURAL.
//
// A symbol has one definition, so that verb jumps. An interface has as
// many implementations as the project cares to write, so this one has to
// decide what to do with a list — and both halves of the answer already
// existed:
//
//	≡ Code ──► lspGoTo(method) ──goroutine──► client.Locations
//	                                               │
//	        one location ◄── lspLocationsEvent ◄───┘── several
//	             │                                        │
//	        lspJumpTo (definition's landing)     openLocationsPanel
//	                                             (references' list)
//
// So this file owns a request, a fork, and two labels. Everything about
// landing a jump (nav history, the suppressed open) is lsp.go's, and
// everything about a cross-file list (context lines, the open-buffer
// reconciliation, the dock, the Esc contract) is lspreferences.go's.
//
// For Go, implementation is the verb worth having: on an interface or one
// of its methods it lists the concrete types, and on a concrete type it
// lists the interfaces it satisfies — the one relationship in the
// language that is written down nowhere in the source.
//
// "Find incoming calls" is the third verb on the same request-and-fork.
// Its fetch is two round trips instead of one (lsp/callhierarchy.go) and
// it ALWAYS lists, because its question is references' — "who?" — and a
// who-question answered by silently teleporting to the only caller would
// hide the fact that there is only one. It differs from references in
// what it leaves out: the declaration, a callback passed by value, a doc
// link — everything that mentions the function without calling it.
//
// No leader keys: the flat table is out of mnemonic letters, and all
// three rows get the command palette for free.

package app

import (
	"fmt"
	"time"

	"github.com/rohanthewiz/ced/internal/editor"
	"github.com/rohanthewiz/ced/internal/lsp"
)

// lspLocationsEvent carries a finished go-to lookup back to the main
// loop. It holds BOTH forms of the answer because which one is used is
// decided on arrival: a single location jumps from the raw Location
// (exact UTF-16 position, converted against the buffer it lands in), a
// list renders from the context-carrying refLocs.
type lspLocationsEvent struct {
	when time.Time
	// seq shares references' generation (lsp.refSeq): all of these can
	// open the same panel, so the newest question of any kind wins it.
	seq      int
	fromPath string
	fromPos  editor.Position
	// noun names what was asked for in the singular ("implementation"),
	// heading titles the panel ("Implementations of"), query is the word
	// under the cursor.
	noun, heading, query string
	// alwaysList keeps a single answer in the panel instead of jumping.
	alwaysList bool

	locs      []lsp.Location
	refs      []refLoc
	truncated bool
	err       error
}

// When satisfies the tcell.Event interface.
func (e *lspLocationsEvent) When() time.Time { return e.when }

// menuGoToImplementation is the ≡ Code row: who implements this
// interface (or which interfaces this type satisfies).
func (a *App) menuGoToImplementation() {
	a.lspGoTo(locationsFetch(lsp.MethodImplementation), "implementation", "Implementations of", false)
}

// menuIncomingCalls is the ≡ Code row: every place the function under
// the cursor is CALLED.
func (a *App) menuIncomingCalls() {
	fetch := func(c lspConn, path string, pos lsp.Position) ([]lsp.Location, error) {
		return c.IncomingCalls(path, pos)
	}
	a.lspGoTo(fetch, "incoming call", "Calls to", true)
}

// lspLocFetch is the request half of a go-to verb: anything that turns
// a position into locations.
type lspLocFetch func(c lspConn, path string, pos lsp.Position) ([]lsp.Location, error)

// locationsFetch adapts one of definition's same-shaped methods.
func locationsFetch(method string) lspLocFetch {
	return func(c lspConn, path string, pos lsp.Position) ([]lsp.Location, error) {
		return c.Locations(method, path, pos)
	}
}

// menuGoToTypeDefinition is the ≡ Code row: jump to the declaration of
// the TYPE of the symbol under the cursor — from a variable straight to
// its struct, skipping the line that merely declares the variable.
func (a *App) menuGoToTypeDefinition() {
	a.lspGoTo(locationsFetch(lsp.MethodTypeDefinition), "type definition", "Type definitions of", false)
}

// lspGoTo fires one position → locations request. Same contracts as the
// verbs beside it: refuse a cursor on nothing before spending a round
// trip, flush so the server answers from the text on screen, and stamp
// the generation because the answer may open a panel.
func (a *App) lspGoTo(fetch lspLocFetch, noun, heading string, alwaysList bool) {
	a.closeMenu()
	t := a.activeTabPtr()
	if t == nil || !a.hasLSPActions() {
		return
	}
	word := cursorWord(t)
	if word == "" {
		a.flash(capitalize(noun) + ": put the cursor on a symbol first")
		return
	}
	client, scr := a.lspClientFor(t.Path), a.screen
	if client == nil || scr == nil {
		return
	}
	path, from := t.Path, t.Cursor
	pos := lspPosFor(t, from)
	a.lspFlushChange(t)

	a.lsp.refSeq++
	seq := a.lsp.refSeq
	go func() {
		locs, err := fetch(client, path, pos)
		// Context lines are only needed for a list, and reading them is
		// file IO, so it happens here — but only when there is a list.
		var refs []refLoc
		var truncated bool
		if len(locs) > 1 || (alwaysList && len(locs) == 1) {
			refs, truncated = collectRefLines(locs)
		}
		_ = scr.PostEvent(&lspLocationsEvent{
			when: time.Now(), seq: seq, fromPath: path, fromPos: from,
			noun: noun, heading: heading, query: word, alwaysList: alwaysList,
			locs: locs, refs: refs, truncated: truncated, err: err,
		})
	}()
}

// handleLSPLocations lands a go-to answer: nothing flashes, one jumps,
// several list. The guards are references' — a superseded generation is
// dropped, and a list arriving while a modal or the menu owns the screen
// reports its count rather than stealing the slot.
func (a *App) handleLSPLocations(e *lspLocationsEvent) {
	if e.seq != a.lsp.refSeq {
		return
	}
	if e.err != nil {
		a.flash(capitalize(e.noun) + ": " + e.err.Error())
		return
	}
	if len(e.locs) == 0 {
		a.flash(fmt.Sprintf("No %s for %q", e.noun, e.query) + a.lspLoadingNote(e.fromPath))
		return
	}
	if len(e.locs) == 1 && !e.alwaysList {
		target := lsp.URIToPath(e.locs[0].URI)
		if target == "" {
			a.flash("The " + e.noun + " is not in a plain file")
			return
		}
		a.lspJumpTo(e.fromPath, e.fromPos, target, e.locs[0].Range.Start)
		return
	}
	if len(e.refs) == 0 {
		// Several locations, none of them a plain file the editor could
		// open (collectRefLines drops non-file URIs).
		a.flash("The " + e.noun + "s are not in plain files")
		return
	}
	if a.modal != nil || a.menuOpen {
		a.flash(fmt.Sprintf("%s: %d for %q — run it again", capitalize(e.noun), len(e.refs), e.query))
		return
	}
	a.openLocationsPanel(e.heading, e.query, e.refs, e.truncated)
}

// capitalize upper-cases a noun's first letter for the head of a flash.
// ASCII only — every noun here is one this file wrote.
func capitalize(s string) string {
	if s == "" || s[0] < 'a' || s[0] > 'z' {
		return s
	}
	return string(s[0]-'a'+'A') + s[1:]
}
