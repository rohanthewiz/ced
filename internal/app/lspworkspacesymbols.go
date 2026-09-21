// =============================================================================
// File: internal/app/lspworkspacesymbols.go
// Author: Rohan Allison <rohanthewiz@gmail.com>
// Created: 2026-09-21
// Copyright: 2026 Rohan Allison. All rights reserved.
// =============================================================================

// lspworkspacesymbols.go is "Go to symbol in project" — find a
// declaration by NAME, in any file, open or not. It is the p/P widening
// of "Go to symbol in file": that one lists this file's outline, this one
// asks the server's project-wide index.
//
//	≡ Code ──► prompt (seeded with the cursor word) ──Enter──┐
//	                                                         ▼
//	   picker ◄── lspWorkspaceSymbolsEvent ◄──goroutine── workspace/symbol
//	     │                                               (every ready server)
//	   Enter ──► lspJumpTo (definition's landing)
//
// The decisions worth spelling out:
//
//   - IT PROMPTS FIRST, unlike the file outline, because the request is
//     QUERY-DRIVEN. A server answers workspace/symbol("") with nothing, or
//     with an arbitrary slice of a very large set; there is no "whole
//     list" to fetch and filter locally. So the question is asked once,
//     and the picker then narrows the ANSWER with the palette's own
//     scorer. A picker that re-queried on every keystroke would need a
//     hook the palette doesn't have and would reorder under the user's
//     fingers — the argument that kept symbols out of the palette's
//     sources.
//   - EVERY READY SERVER IS ASKED, because the question is about the
//     project, not the file in front of you: a Go backend with a
//     TypeScript front end has two indexes and one prompt. Only servers
//     that are already UP take part — this verb must not be the thing
//     that spawns a language server for a language nobody has opened.
//   - The name leads the row and the path trails it (symbolLabel's rule):
//     the fuzzy scorer rewards early matches, and the name is what was
//     asked for.
//
// No leader key — the flat table is out of mnemonic letters.

package app

import (
	"fmt"
	"sort"
	"time"

	"github.com/rohanthewiz/ced/internal/lsp"
)

// maxWorkspaceSymbols caps the picker. A query loose enough to match
// more than this is a query to refine, and the cap is reported in the
// picker's title so a short list never passes for a complete one.
const maxWorkspaceSymbols = 500

// lspWorkspaceSymbolsEvent carries the merged answer to the main loop.
type lspWorkspaceSymbolsEvent struct {
	when      time.Time
	seq       int
	query     string
	syms      []lsp.WorkspaceSymbol
	truncated bool
	err       error
}

// When satisfies the tcell.Event interface.
func (e *lspWorkspaceSymbolsEvent) When() time.Time { return e.when }

// lspReadyClients returns every live connection in a stable (id) order,
// so two runs of one query merge their answers the same way.
func (a *App) lspReadyClients() []lspConn {
	if a.lsp.dead {
		return nil
	}
	ids := make([]string, 0, len(a.lsp.servers))
	for id, sv := range a.lsp.servers {
		if sv.client != nil && !sv.dead {
			ids = append(ids, id)
		}
	}
	sort.Strings(ids)
	out := make([]lspConn, 0, len(ids))
	for _, id := range ids {
		out = append(out, a.lsp.servers[id].client)
	}
	return out
}

// hasWorkspaceSymbols is the ≡ predicate: some server is up. Not
// hasLSPActions — the active tab is irrelevant to a project-wide
// question, and the row must work from a README.
func (a *App) hasWorkspaceSymbols() bool { return a.lspAnyReady() }

// menuGoToWorkspaceSymbol opens the query prompt, seeded with the word
// under the cursor: the common case is "where is THIS declared, by
// name", and Enter on the seed is one keystroke.
func (a *App) menuGoToWorkspaceSymbol() {
	a.closeMenu()
	if !a.lspAnyReady() {
		a.flash("Go to symbol in project: no language server is running — open a source file first")
		return
	}
	seed := cursorWord(a.activeTabPtr())
	a.openPrompt("Go to symbol in project", "a name, or part of one", seed, func(app *App, query string) {
		app.startWorkspaceSymbols(query)
	})
}

// startWorkspaceSymbols fires the query at every ready server and merges
// what comes back. Pending edits are flushed first — a function typed a
// moment ago should be findable by name.
func (a *App) startWorkspaceSymbols(query string) {
	clients, scr := a.lspReadyClients(), a.screen
	if len(clients) == 0 || scr == nil {
		a.flash("Go to symbol in project: no language server")
		return
	}
	for _, t := range a.tabs {
		a.lspFlushChange(t)
	}
	a.lsp.symSeq++
	seq := a.lsp.symSeq
	a.flash(fmt.Sprintf("Finding symbols matching %q…", query))
	go func() {
		var all []lsp.WorkspaceSymbol
		var firstErr error
		for _, c := range clients {
			syms, err := c.WorkspaceSymbols(query)
			if err != nil {
				// One server failing must not cost the others' answers;
				// the error only surfaces if NOBODY had anything.
				if firstErr == nil {
					firstErr = err
				}
				continue
			}
			all = append(all, syms...)
		}
		// A single server's ranking is kept as it came. Merged answers
		// have no shared ranking, so they fall back to name order.
		if len(clients) > 1 {
			lsp.SortWorkspaceSymbols(all)
		}
		truncated := false
		if len(all) > maxWorkspaceSymbols {
			all, truncated = all[:maxWorkspaceSymbols], true
		}
		if len(all) > 0 {
			firstErr = nil
		}
		_ = scr.PostEvent(&lspWorkspaceSymbolsEvent{
			when: time.Now(), seq: seq, query: query,
			syms: all, truncated: truncated, err: firstErr,
		})
	}()
}

// handleLSPWorkspaceSymbols opens the result picker. Generation-checked
// because it opens a modal, and polite for the references reason: a list
// landing while something else owns the screen reports its count instead
// of stealing the slot.
func (a *App) handleLSPWorkspaceSymbols(e *lspWorkspaceSymbolsEvent) {
	if e.seq != a.lsp.symSeq {
		return
	}
	if e.err != nil {
		a.flash("Go to symbol in project: " + e.err.Error())
		return
	}
	if len(e.syms) == 0 {
		a.flash(fmt.Sprintf("No symbols matching %q", e.query))
		return
	}
	if a.modal != nil || a.menuOpen {
		a.flash(fmt.Sprintf("Go to symbol in project: %d for %q — run it again", len(e.syms), e.query))
		return
	}
	items := make([]paletteItem, 0, len(e.syms))
	for _, s := range e.syms {
		items = append(items, paletteItem{
			label: a.workspaceSymbolLabel(s),
			run:   func(app *App) { app.goToWorkspaceSymbol(s) },
		})
	}
	title := fmt.Sprintf("Symbols matching %q", e.query)
	if e.truncated {
		title += fmt.Sprintf(" (first %d)", maxWorkspaceSymbols)
	}
	a.openPicker(title, items)
}

// workspaceSymbolLabel renders one row: name, kind, container, then
// where it lives. Name first for the scorer; the location last because
// it is what tells two same-named declarations apart once the eye has
// found the name.
func (a *App) workspaceSymbolLabel(s lsp.WorkspaceSymbol) string {
	label := s.Name
	if kind := lsp.SymbolKindName(s.Kind); kind != "" {
		label += "  " + kind
	}
	if s.Container != "" {
		label += "  " + s.Container
	}
	return fmt.Sprintf("%s  —  %s:%d", label, a.relativePathFor(s.Path), s.Pos.Line+1)
}

// goToWorkspaceSymbol jumps to a picked declaration through the landing
// every go-to verb shares, then centers it — the body is why you came.
func (a *App) goToWorkspaceSymbol(s lsp.WorkspaceSymbol) {
	from, ok := a.currentNavLoc()
	if !ok {
		from = navLoc{}
	}
	if !a.lspJumpTo(from.path, from.pos, s.Path, s.Pos) {
		return
	}
	if t := a.activeTabPtr(); t != nil {
		if _, _, ew, eh := a.editorRect(); !t.CursorLineVisible(eh) {
			t.CenterOnCursor(ew, eh)
		}
	}
}
