// =============================================================================
// File: internal/app/lsprename.go
// Author: Rohan Allison <rohanthewiz@gmail.com>
// Created: 2026-08-03
// Copyright: 2026 Rohan Allison. All rights reserved.
// =============================================================================

// lsprename.go is "Rename symbol" — change a declaration's name and every
// use of it across the project, in one undoable gesture.
//
// It is the thinnest file in the code-intelligence group, and deliberately
// so: the workspace-edit primitive was built one session before this one on
// exactly that argument. Every hard question a cross-file rewrite raises —
// which files to open (none), which text the coordinates were measured
// against (the buffer, never the disk copy behind it), what happens when a
// write fails halfway (rollback), how a dozen files come back with one press
// (the undo journal), how the user sees what changed (the Find-all panel) —
// is answered in workspaceedit.go. All this file does is ask the question
// and hand the answer over.
//
//	Esc E / ≡ Code ──► menuRenameSymbol ──► prompt ──► startRename
//	                          │                            │
//	                   word under cursor            flush + capture
//	                                                       │ goroutine
//	                                                       ▼
//	   applyServerEdit ◄── handleLSPRename ◄── lspRenameEvent
//
// THE PROMPT SITS BETWEEN THE ASK AND THE REQUEST, which is the one genuine
// complication and the reason startRename is split out of menuRenameSymbol.
// The modal owns the keyboard, so the user cannot type into the buffer while
// it is up — but the LSP debounce timer, auto-save, a chat agent's write and
// the external-change reconciliation are all still running, and any of them
// can move the document under the prompt. So the position the word was read
// at is captured when the prompt OPENS, re-verified when it SUBMITS, and the
// staleness contract (captureWSRequest) is taken at submit time, after the
// flush, so it describes the text the server is about to answer from rather
// than the text that was on screen when the user reached for the key.
//
// The generation counter is not optional here the way it is for hover. This
// verb's answer WRITES FILES, so a second rename fired while the first was
// in flight must not have its answer applied out of order — a stale edit
// would be planned against coordinates from a buffer two renames ago. The
// plan's own staleness checks would catch most of it; the seq catches the
// rest, and it is one integer.

package app

import (
	"fmt"
	"strings"
	"time"

	"github.com/rohanthewiz/ced/internal/editor"
	"github.com/rohanthewiz/ced/internal/lsp"
)

// lspRenameEvent carries a finished rename request back to the main loop.
//
// It carries the wsRequest it was launched with rather than letting the
// handler rebuild one: the contract is what was true when the question was
// ASKED, and by the time this lands the answer to "what revision is that
// document at?" may legitimately have changed — which is precisely what the
// plan is supposed to notice.
type lspRenameEvent struct {
	when    time.Time
	seq     int
	oldName string
	newName string
	edit    *lsp.WorkspaceEdit
	req     wsRequest
	err     error
}

// When satisfies the tcell.Event interface.
func (e *lspRenameEvent) When() time.Time { return e.when }

// menuRenameSymbol is the ≡ Code row and the Esc-E leader: rename the symbol
// under the cursor everywhere it is used.
//
// The word is resolved BEFORE the prompt for two reasons. It seeds the input
// field, so a small correction (fooBar → fooBaz) is a keystroke rather than a
// retype; and a cursor that isn't on a symbol refuses HERE, before the user
// has been asked to think of a name for nothing. The server needs only the
// position — the old name never goes on the wire (see lsp.RenameParams) — so
// this is a UI affordance, not part of the request.
func (a *App) menuRenameSymbol() {
	a.closeMenu()
	t := a.activeTabPtr()
	if t == nil || !a.hasLSPActions() {
		return
	}
	old := cursorWord(t)
	if old == "" {
		a.flash("Rename: put the cursor on a symbol first")
		return
	}
	// Everything needed to re-establish the ask is captured now: the modal
	// is about to own the keyboard, and the tab list, the active tab and the
	// buffer can all move before the callback runs.
	path, pos, rev := t.Path, t.Cursor, t.EditRev
	a.openPrompt("Rename "+old, "every use across the project", old,
		func(a *App, newName string) {
			a.startRename(path, pos, rev, old, newName)
		})
}

// startRename validates the new name, proves the document hasn't moved since
// the prompt opened, and fires the request.
//
// The two client-side refusals are deliberately narrow. An unchanged name is
// refused because the round trip is guaranteed to come back either empty or
// with a no-op edit, and "nothing to change" is a confusing way to answer
// "you didn't change it". Whitespace is refused because no language this
// editor will ever speak allows it in an identifier, so the server's error
// would only be a slower way to say the same thing. Everything else — a
// leading digit, a keyword, a name that collides — is the SERVER's judgment
// to make, and its message names the actual rule.
func (a *App) startRename(path string, pos editor.Position, askRev int, oldName, newName string) {
	if newName == oldName {
		a.flash(fmt.Sprintf("Rename: %q is already its name", oldName))
		return
	}
	if strings.ContainsAny(newName, " \t") {
		a.flash("Rename: a name can't contain spaces")
		return
	}
	// The prompt was open, not the editor — but the debounce timer, auto-save
	// and the disk reconciliation never stopped. A buffer that moved means the
	// captured position may no longer be on the symbol the user was looking
	// at, and renaming whatever is there now is the wrong kind of surprise.
	t := a.tabByPath(path)
	if t == nil || t.EditRev != askRev {
		a.flash("Rename: the file changed — put the cursor on the symbol again")
		return
	}
	if !a.lspReady() || a.screen == nil {
		a.flash("Rename: no language server")
		return
	}
	client, scr := a.lsp.client, a.screen

	// Flush FIRST, then capture: the contract has to describe the text the
	// server is about to answer from, and the flush is what makes those the
	// same thing. Capturing before the flush would record the pre-sync
	// revision and make the plan refuse its own request.
	a.lspFlushChange(t)
	req := a.captureWSRequest(t)
	lspPos := lspPosFor(t, pos)

	a.lsp.renameSeq++
	seq := a.lsp.renameSeq
	a.flash(fmt.Sprintf("Renaming %s → %s…", oldName, newName))
	go func() {
		edit, err := client.Rename(path, lspPos, newName)
		_ = scr.PostEvent(&lspRenameEvent{
			when: time.Now(), seq: seq,
			oldName: oldName, newName: newName,
			edit: edit, req: req, err: err,
		})
	}()
}

// handleLSPRename lands a rename response on the main loop and hands it to
// the primitive.
//
// There is almost nothing here, and that is the point: applyServerEdit
// already refuses an empty edit, refuses resource operations by name, plans
// every file, confirms when the edit reaches files the user never opened,
// rolls back a failed write, arms the one-gesture undo and opens the
// receipt. The only thing this verb owes on top is the LABEL — which becomes
// the confirmation's title, the flash, the undo row's text and the receipt's
// heading, so it has to read as a sentence in all four places.
func (a *App) handleLSPRename(e *lspRenameEvent) {
	if e.seq != a.lsp.renameSeq {
		return // superseded by a later rename
	}
	label := renameLabel(e.oldName, e.newName)
	if e.err != nil {
		// The server's own message is the useful part — it names the rule
		// that was broken ("cannot rename package foo"), which is more than
		// ced could work out for itself.
		a.flash(label + ": " + e.err.Error())
		return
	}
	a.applyServerEdit(e.edit, label, e.req)
}

// renameLabel is the one spelling of a rename, so the confirmation, the
// flash, the ≡ undo row and the receipt heading can never drift apart.
func renameLabel(oldName, newName string) string {
	return fmt.Sprintf("Rename %s → %s", oldName, newName)
}
