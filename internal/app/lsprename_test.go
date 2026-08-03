// =============================================================================
// File: internal/app/lsprename_test.go
// Author: Rohan Allison <rohanthewiz@gmail.com>
// Created: 2026-08-03
// Copyright: 2026 Rohan Allison. All rights reserved.
// =============================================================================

package app

import (
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/rohanthewiz/ced/internal/editor"
	"github.com/rohanthewiz/ced/internal/lsp"
)

// renameTestApp builds an app with a ready fake server, one Go file open at a
// cursor sitting on the symbol `foo`, and the LSP sync bookkeeping seeded so
// the staleness guards see a synced document.
func renameTestApp(t *testing.T) (*App, *fakeLSPConn, string, *editor.Tab) {
	t.Helper()
	a := newTestApp(t, t.TempDir())
	fake := &fakeLSPConn{}
	a.lsp.dead = false
	a.lsp.client = fake
	path := wsTestFile(t, a, "main.go", "package main\n\nvar foo int\n")
	tab := wsOpenTab(t, a, path)
	tab.MoveCursorTo(editor.Position{Line: 2, Col: 5}, false) // inside "foo"
	return a, fake, path, tab
}

// renamePrompt opens the rename prompt and returns it, failing the test if
// the verb refused instead.
func renamePrompt(t *testing.T, a *App) *promptModal {
	t.Helper()
	a.menuRenameSymbol()
	pm, ok := a.modal.(*promptModal)
	if !ok {
		t.Fatalf("modal = %T (status %q), want *promptModal", a.modal, a.statusMsg)
	}
	return pm
}

// submitPrompt types a value into the open prompt and confirms it, the way a
// user does — so the test exercises the prompt's own trim and empty-submit
// rules rather than calling the callback directly.
func submitPrompt(t *testing.T, a *App, pm *promptModal, value string) {
	t.Helper()
	pm.field = newTextField(value)
	pm.submit(a)
}

// TestRenameSymbol_PromptSeedsFromTheWordUnderTheCursor pins the affordance
// that makes a small correction one keystroke instead of a retype. The old
// name never goes on the wire — the position is the symbol's identity — so
// pre-filling is the only thing it is for, and losing it would be invisible
// to every other test.
func TestRenameSymbol_PromptSeedsFromTheWordUnderTheCursor(t *testing.T) {
	a, _, _, _ := renameTestApp(t)
	pm := renamePrompt(t, a)

	if got := pm.field.String(); got != "foo" {
		t.Errorf("prompt field = %q, want the word under the cursor", got)
	}
	if !strings.Contains(pm.title, "foo") {
		t.Errorf("prompt title = %q, want it to name the symbol", pm.title)
	}
}

// TestRenameSymbol_RefusesWithoutASymbol pins the refusal that happens BEFORE
// the user is asked to think of a name: a cursor in whitespace has nothing to
// rename, and a round trip about it is guaranteed to come back empty.
func TestRenameSymbol_RefusesWithoutASymbol(t *testing.T) {
	a, fake, _, tab := renameTestApp(t)
	tab.MoveCursorTo(editor.Position{Line: 1, Col: 0}, false) // the blank line

	a.menuRenameSymbol()
	if a.modal != nil {
		t.Fatalf("modal = %T, want no prompt for a cursor on nothing", a.modal)
	}
	if !strings.Contains(a.statusMsg, "cursor on a symbol") {
		t.Errorf("status = %q, want the no-symbol explanation", a.statusMsg)
	}
	assertNoRenameCall(t, fake, "the refusal is client-side")
}

// TestRenameSymbol_SendsTheNewNameAndAppliesTheEdit is the happy path end to
// end: the name the prompt collected reaches the wire, and the edit that
// comes back is applied through the workspace-edit primitive.
func TestRenameSymbol_SendsTheNewNameAndAppliesTheEdit(t *testing.T) {
	a, fake, path, tab := renameTestApp(t)
	fake.renameEdit = wsEdit(path, 2, 4, 7, "bar")

	submitPrompt(t, a, renamePrompt(t, a), "bar")
	a.handleLSPRename(waitRename(t, a, fake))

	if fake.renameName != "bar" {
		t.Errorf("newName on the wire = %q, want %q", fake.renameName, "bar")
	}
	if got, want := tab.Buffer.Lines[2], "var bar int"; got != want {
		t.Errorf("buffer line = %q, want %q", got, want)
	}
	if a.wsGroup == nil {
		t.Fatal("no undo group armed — a rename must come back with one press")
	}
	if got, want := a.wsGroup.label, "Rename foo → bar"; got != want {
		t.Errorf("group label = %q, want %q", got, want)
	}
}

// TestRenameSymbol_LabelIsTheSameEverywhere pins the one spelling. The label
// becomes the confirmation title, the flash, the ≡ undo row and the receipt
// heading; four hand-built strings would drift, and the undo row is the one
// place a user reads it cold.
func TestRenameSymbol_LabelIsTheSameEverywhere(t *testing.T) {
	a, fake, path, _ := renameTestApp(t)
	fake.renameEdit = wsEdit(path, 2, 4, 7, "bar")

	submitPrompt(t, a, renamePrompt(t, a), "bar")
	a.handleLSPRename(waitRename(t, a, fake))

	want := renameLabel("foo", "bar")
	if a.wsGroup == nil || a.wsGroup.label != want {
		t.Fatalf("group label = %+v, want %q", a.wsGroup, want)
	}
	if !strings.Contains(a.wsEditUndoLabel(), want) {
		t.Errorf("undo row = %q, want it to carry %q", a.wsEditUndoLabel(), want)
	}
	if !strings.Contains(a.statusMsg, want) {
		t.Errorf("flash = %q, want it to carry %q", a.statusMsg, want)
	}
}

// TestRenameSymbol_RefusesAnUnchangedName pins a client-side refusal whose
// only cost is a round trip: the server would answer either nothing or a
// no-op edit, and "nothing to change" is a confusing way to say "you didn't
// change it".
func TestRenameSymbol_RefusesAnUnchangedName(t *testing.T) {
	a, fake, _, _ := renameTestApp(t)

	submitPrompt(t, a, renamePrompt(t, a), "foo")
	assertNoRenameCall(t, fake, "an unchanged name is refused before the wire")
	if !strings.Contains(a.statusMsg, "already its name") {
		t.Errorf("status = %q, want the unchanged-name explanation", a.statusMsg)
	}
}

// TestRenameSymbol_RefusesWhitespaceInTheName pins the other client-side
// refusal. No language ced will speak allows a space in an identifier, so the
// server's error would only be a slower way to say the same thing — and the
// user has already typed the name by then.
func TestRenameSymbol_RefusesWhitespaceInTheName(t *testing.T) {
	a, fake, _, _ := renameTestApp(t)

	submitPrompt(t, a, renamePrompt(t, a), "new name")
	assertNoRenameCall(t, fake, "a name with a space is refused before the wire")
	if !strings.Contains(a.statusMsg, "spaces") {
		t.Errorf("status = %q, want the whitespace explanation", a.statusMsg)
	}
}

// TestRenameSymbol_RefusesWhenTheBufferMovedUnderThePrompt is the reason
// startRename is split out of menuRenameSymbol. The modal owns the keyboard,
// but the debounce timer, auto-save, a chat agent's write and the disk
// reconciliation do not stop — and a captured position measured against text
// that has since moved would rename whatever now sits under it.
func TestRenameSymbol_RefusesWhenTheBufferMovedUnderThePrompt(t *testing.T) {
	a, fake, _, tab := renameTestApp(t)
	pm := renamePrompt(t, a)

	// Something that is not the keyboard edits the document — a chat agent's
	// write and the disk reconciliation both land exactly like this.
	tab.Buffer.InsertString(editor.Position{}, "// touched\n")
	tab.EditRev++

	submitPrompt(t, a, pm, "bar")
	assertNoRenameCall(t, fake, "the ask no longer describes the file")
	if !strings.Contains(a.statusMsg, "changed") {
		t.Errorf("status = %q, want the moved-file explanation", a.statusMsg)
	}
}

// TestRenameSymbol_FlushesBeforeAsking pins that the server is asked about
// what is on screen. A rename resolved against text one keystroke stale would
// rewrite the OLD symbol's bindings under the new name — the worst answer
// this verb can give, because it looks like it worked.
func TestRenameSymbol_FlushesBeforeAsking(t *testing.T) {
	a, fake, path, tab := renameTestApp(t)
	fake.renameEdit = wsEdit(path, 2, 4, 7, "bar")

	// An unsynced edit, as a pending debounce would leave one.
	tab.MoveCursorTo(editor.Position{Line: 0, Col: 0}, false)
	tab.InsertString("// note\n")
	tab.MoveCursorTo(editor.Position{Line: 3, Col: 5}, false)

	pm := renamePrompt(t, a)
	submitPrompt(t, a, pm, "bar")

	var sawChange bool
	for _, c := range fake.callLog() {
		if strings.HasPrefix(c, "didChange:") {
			sawChange = true
		}
		if strings.HasPrefix(c, "rename:") && !sawChange {
			t.Fatal("rename went out before the pending change was flushed")
		}
	}
	if !sawChange {
		t.Error("no didChange — the unsynced buffer was never flushed")
	}
}

// TestRenameSymbol_StaleAnswerIsDropped pins the generation guard. Unlike
// hover, this answer WRITES FILES: an older rename's edit planned against a
// buffer the newer one already rewrote would corrupt it plausibly enough that
// nobody notices until it doesn't compile.
func TestRenameSymbol_StaleAnswerIsDropped(t *testing.T) {
	a, _, path, tab := renameTestApp(t)
	before := tab.Buffer.Lines[2]

	a.lsp.renameSeq = 7
	a.handleLSPRename(&lspRenameEvent{
		when: time.Now(), seq: 6, // one generation behind
		oldName: "foo", newName: "bar",
		edit: wsEdit(path, 2, 4, 7, "bar"),
	})

	if tab.Buffer.Lines[2] != before {
		t.Errorf("buffer = %q, want %q — a superseded rename was applied", tab.Buffer.Lines[2], before)
	}
	if a.wsGroup != nil {
		t.Error("a superseded rename armed the undo group")
	}
}

// TestRenameSymbol_ServerRefusalCarriesItsReason pins that gopls's own
// message reaches the user. It names the rule that was broken ("cannot rename
// package"), which is more than ced could work out — a generic "rename
// failed" would leave the user with no next move.
func TestRenameSymbol_ServerRefusalCarriesItsReason(t *testing.T) {
	a, fake, _, _ := renameTestApp(t)
	fake.renameErr = errors.New("can't rename package: not supported")

	submitPrompt(t, a, renamePrompt(t, a), "bar")
	a.handleLSPRename(waitRename(t, a, fake))

	if !strings.Contains(a.statusMsg, "can't rename package") {
		t.Errorf("status = %q, want the server's own reason", a.statusMsg)
	}
	if !strings.Contains(a.statusMsg, "Rename foo → bar") {
		t.Errorf("status = %q, want it to name the rename that failed", a.statusMsg)
	}
}

// TestRenameSymbol_EmptyEditSaysNothingToChange pins that a legal rename that
// changes nothing reads as an answer, not a failure — the distinction the
// client preserves by returning (nil, nil).
func TestRenameSymbol_EmptyEditSaysNothingToChange(t *testing.T) {
	a, fake, _, _ := renameTestApp(t)
	fake.renameEdit = nil

	submitPrompt(t, a, renamePrompt(t, a), "bar")
	a.handleLSPRename(waitRename(t, a, fake))

	if !strings.Contains(a.statusMsg, "nothing to change") {
		t.Errorf("status = %q, want the empty-edit answer", a.statusMsg)
	}
}

// TestRenameSymbol_ReachesFilesWithNoTab is the whole point of the verb: a
// rename crosses files, and the ones nobody has open are written on the
// user's behalf after a confirmation — without opening a tab for any of them.
func TestRenameSymbol_ReachesFilesWithNoTab(t *testing.T) {
	a, fake, path, tab := renameTestApp(t)
	other := wsTestFile(t, a, "other.go", "package main\n\nvar _ = foo\n")
	fake.renameEdit = &lsp.WorkspaceEdit{Documents: []lsp.DocumentEdit{
		wsEdit(path, 2, 4, 7, "bar").Documents[0],
		wsEdit(other, 2, 8, 11, "bar").Documents[0],
	}}
	tabsBefore := len(a.tabs)

	submitPrompt(t, a, renamePrompt(t, a), "bar")
	a.handleLSPRename(waitRename(t, a, fake))

	cm, ok := a.modal.(*confirmModal)
	if !ok {
		t.Fatalf("modal = %T, want a confirmation before writing an unopened file", a.modal)
	}
	cm.yes(a)

	if len(a.tabs) != tabsBefore {
		t.Errorf("tab count = %d, want %d — the rename opened tabs", len(a.tabs), tabsBefore)
	}
	if got, want := tab.Buffer.Lines[2], "var bar int"; got != want {
		t.Errorf("open buffer = %q, want %q", got, want)
	}
	data, _ := os.ReadFile(other)
	if got, want := string(data), "package main\n\nvar _ = bar\n"; got != want {
		t.Errorf("unopened file = %q, want %q", got, want)
	}
}

// TestRenameSymbol_UndoneWithOnePress pins the payoff of building the
// primitive first: the whole cross-file rename comes back with a single plain
// undo pressed in one of the touched files.
func TestRenameSymbol_UndoneWithOnePress(t *testing.T) {
	a, fake, path, tab := renameTestApp(t)
	other := wsTestFile(t, a, "other.go", "package main\n\nvar _ = foo\n")
	fake.renameEdit = &lsp.WorkspaceEdit{Documents: []lsp.DocumentEdit{
		wsEdit(path, 2, 4, 7, "bar").Documents[0],
		wsEdit(other, 2, 8, 11, "bar").Documents[0],
	}}

	submitPrompt(t, a, renamePrompt(t, a), "bar")
	a.handleLSPRename(waitRename(t, a, fake))
	if cm, ok := a.modal.(*confirmModal); ok {
		cm.yes(a)
	}
	a.closeModal() // dismiss the receipt

	a.menuUndo()

	if got, want := tab.Buffer.Lines[2], "var foo int"; got != want {
		t.Errorf("open buffer after undo = %q, want %q", got, want)
	}
	data, _ := os.ReadFile(other)
	if !strings.Contains(string(data), "var _ = foo") {
		t.Errorf("unopened file after undo = %q, want the original name back", data)
	}
}

// TestRenameSymbol_MenuAndLeaderAgree pins that the ≡ row and Esc-E run the
// same verb, and that the row's advertised shortcut matches the binding —
// menuItemDef.shortcut is display-only, so the two can silently disagree.
func TestRenameSymbol_MenuAndLeaderAgree(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	if sc := menuItemByLabel(t, a, "Rename symbol…").shortcut; sc != "esc E" {
		t.Errorf("menu shortcut = %q, want %q", sc, "esc E")
	}
	found := false
	for _, b := range leaderBindings() {
		if b.key == 'E' {
			found = true
			if b.sub != nil || b.subFor != nil {
				t.Error("'E' must be an action, not a namespace prefix")
			}
			if b.repeat {
				t.Error("'E' must not repeat — it opens a prompt")
			}
		}
	}
	if !found {
		t.Error("no leader binding on 'E'")
	}
}

// assertNoRenameCall proves the verb refused before reaching the server. It
// looks for the rename call specifically rather than an empty log, because
// opening the test file already put a didOpen on it.
func assertNoRenameCall(t *testing.T, fake *fakeLSPConn, why string) {
	t.Helper()
	for _, c := range fake.callLog() {
		if strings.HasPrefix(c, "rename:") {
			t.Errorf("a rename request went out; %s", why)
		}
	}
}

// waitRename drains the simulation screen's queue until the rename event the
// request goroutine posts shows up. The request runs off-loop like every
// other LSP verb, so the test has to meet it at the queue rather than assume
// it has already landed. Capped at 2s so a hung goroutine fails instead of
// hanging CI.
func waitRename(t *testing.T, a *App, fake *fakeLSPConn) *lspRenameEvent {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		ev := a.screen.PollEvent()
		if ev == nil {
			t.Fatal("screen returned nil event")
		}
		if re, ok := ev.(*lspRenameEvent); ok {
			return re
		}
	}
	t.Fatalf("no rename event arrived (calls: %v)", fake.callLog())
	return nil
}
