// =============================================================================
// File: internal/app/lspcodeaction_test.go
// Author: Rohan Allison <rohanthewiz@gmail.com>
// Created: 2026-08-03
// Copyright: 2026 Rohan Allison. All rights reserved.
// =============================================================================

package app

import (
	"encoding/json"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/rohanthewiz/ced/internal/editor"
	"github.com/rohanthewiz/ced/internal/lsp"
)

// actionTestApp builds an app with a ready fake server and one Go file open,
// the LSP sync bookkeeping seeded so the staleness guards see a synced
// document. Mirrors renameTestApp — the two verbs share every contract that
// isn't about the picker.
func actionTestApp(t *testing.T) (*App, *fakeLSPConn, string, *editor.Tab) {
	t.Helper()
	a := newTestApp(t, t.TempDir())
	fake := &fakeLSPConn{}
	a.lsp.dead = false
	a.lsp.client = fake
	path := wsTestFile(t, a, "main.go", "package main\n\nvar foo int\n")
	tab := wsOpenTab(t, a, path)
	tab.MoveCursorTo(editor.Position{Line: 2, Col: 5}, false)
	return a, fake, path, tab
}

// waitCodeActions drains the simulation screen's queue until the request
// goroutine's event shows up. The request runs off-loop like every other LSP
// verb, so the test meets it at the queue rather than assuming it landed.
func waitCodeActions(t *testing.T, a *App, fake *fakeLSPConn) *lspCodeActionsEvent {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		ev := a.screen.PollEvent()
		if ev == nil {
			t.Fatal("screen returned nil event")
		}
		if ce, ok := ev.(*lspCodeActionsEvent); ok {
			return ce
		}
	}
	t.Fatalf("no codeAction event arrived (calls: %v)", fake.callLog())
	return nil
}

// actionPicker asks for code actions, lands the response, and returns the
// picker it opened — failing the test if the verb refused instead.
func actionPicker(t *testing.T, a *App, fake *fakeLSPConn) *paletteModal {
	t.Helper()
	a.menuCodeActions()
	a.handleLSPCodeActions(waitCodeActions(t, a, fake))
	pm, ok := a.modal.(*paletteModal)
	if !ok {
		t.Fatalf("modal = %T (status %q), want the code-action picker", a.modal, a.statusMsg)
	}
	return pm
}

// pickRow runs the picker's nth row the way Enter does.
func pickRow(t *testing.T, a *App, m *paletteModal, n int) {
	t.Helper()
	if n >= len(m.matches) {
		t.Fatalf("picker has %d rows, wanted row %d", len(m.matches), n)
	}
	m.selected = n
	m.runSelected(a)
}

// editAction builds a code action carrying a ready-made edit.
func editAction(title, kind, path string, line, start, end int, newText string) lsp.CodeAction {
	return lsp.CodeAction{
		Title: title, Kind: kind,
		Edit: wsEdit(path, line, start, end, newText),
	}
}

// -----------------------------------------------------------------------------
// Asking
// -----------------------------------------------------------------------------

// TestCodeActions_RangeIsTheCursorWhenNothingIsSelected pins half of the
// verb's whole interface. A quick fix exists where a diagnostic is, so a
// bare cursor has to be a legal question — a zero-width range at the caret.
func TestCodeActions_RangeIsTheCursorWhenNothingIsSelected(t *testing.T) {
	a, fake, _, _ := actionTestApp(t)

	a.menuCodeActions()
	waitCodeActions(t, a, fake)

	want := lsp.Range{
		Start: lsp.Position{Line: 2, Character: 5},
		End:   lsp.Position{Line: 2, Character: 5},
	}
	if fake.actionRange != want {
		t.Errorf("range = %+v, want a zero-width range at the cursor %+v", fake.actionRange, want)
	}
}

// TestCodeActions_RangeIsTheSelection pins the other half: a refactoring
// like "extract to function" only exists for a span, so a verb that always
// asked about a point would silently never offer one. Document order, not
// gesture order — a selection dragged upward asks the same question.
func TestCodeActions_RangeIsTheSelection(t *testing.T) {
	a, fake, _, tab := actionTestApp(t)
	// Anchor after the cursor: the drag went backwards.
	tab.Anchor = editor.Position{Line: 2, Col: 7}
	tab.Cursor = editor.Position{Line: 2, Col: 4}

	a.menuCodeActions()
	waitCodeActions(t, a, fake)

	want := lsp.Range{
		Start: lsp.Position{Line: 2, Character: 4},
		End:   lsp.Position{Line: 2, Character: 7},
	}
	if fake.actionRange != want {
		t.Errorf("range = %+v, want the selection in document order %+v", fake.actionRange, want)
	}
}

// TestCodeActions_EchoesTouchingDiagnostics pins how a quick fix finds the
// problem it fixes. The diagnostics go back VERBATIM — the fields doing the
// matching are server-private ones this client never modelled — and a
// zero-width cursor range has to match a diagnostic that merely CONTAINS it,
// or asking for a fix while standing on the error would find nothing.
func TestCodeActions_EchoesTouchingDiagnostics(t *testing.T) {
	a, fake, path, _ := actionTestApp(t)
	var onTheLine lsp.Diagnostic
	if err := json.Unmarshal([]byte(
		`{"range":{"start":{"line":2,"character":4},"end":{"line":2,"character":7}},`+
			`"severity":1,"message":"undefined: foo","data":{"fix":"declare"}}`), &onTheLine); err != nil {
		t.Fatalf("seed: %v", err)
	}
	elsewhere := lsp.Diagnostic{
		Range:   lsp.Range{Start: lsp.Position{Line: 0}, End: lsp.Position{Line: 0, Character: 4}},
		Message: "unrelated",
	}
	a.lsp.diags = map[string][]lsp.Diagnostic{path: {onTheLine, elsewhere}}

	a.menuCodeActions()
	waitCodeActions(t, a, fake)

	if len(fake.actionDiags) != 1 {
		t.Fatalf("echoed %d diagnostics, want only the one touching the cursor", len(fake.actionDiags))
	}
	if !strings.Contains(string(fake.actionDiags[0].Raw), `"data":{"fix":"declare"}`) {
		t.Errorf("diagnostic = %s, want the server's own object kept whole", fake.actionDiags[0].Raw)
	}
}

// TestCodeActions_FlushesBeforeAsking pins that the server answers about
// what is on screen. An action offered for text one keystroke stale would
// carry ranges measured against a document that no longer exists, and the
// plan would refuse it — a feature that fails for a reason reading like a
// race.
func TestCodeActions_FlushesBeforeAsking(t *testing.T) {
	a, fake, _, tab := actionTestApp(t)
	tab.MoveCursorTo(editor.Position{Line: 0, Col: 0}, false)
	tab.InsertString("// note\n") // unsynced, as a pending debounce leaves it

	a.menuCodeActions()
	waitCodeActions(t, a, fake)

	var sawChange, sawAction bool
	for _, c := range fake.callLog() {
		switch {
		case strings.HasPrefix(c, "didChange:"):
			sawChange = true
		case strings.HasPrefix(c, "codeAction:"):
			if !sawChange {
				t.Error("the codeAction request went out before the didChange that syncs it")
			}
			sawAction = true
		}
	}
	if !sawAction {
		t.Fatalf("no codeAction request (calls: %v)", fake.callLog())
	}
}

// -----------------------------------------------------------------------------
// The response
// -----------------------------------------------------------------------------

// TestCodeActions_EmptyListFlashes pins that "nothing here" is a real answer
// rather than an empty picker. The usual cause is the position, so the
// message names it — signature help's rule.
func TestCodeActions_EmptyListFlashes(t *testing.T) {
	a, fake, _, _ := actionTestApp(t)

	a.menuCodeActions()
	a.handleLSPCodeActions(waitCodeActions(t, a, fake))

	if a.modal != nil {
		t.Errorf("modal = %T, want no picker for an empty answer", a.modal)
	}
	if !strings.Contains(a.statusMsg, "No code actions here") {
		t.Errorf("status = %q, want the nothing-here message", a.statusMsg)
	}
}

// TestCodeActions_ServerErrorFlashesItsReason pins that the server's own
// message survives the hop — it names the rule it enforced, which is more
// than ced could work out for itself.
func TestCodeActions_ServerErrorFlashesItsReason(t *testing.T) {
	a, fake, _, _ := actionTestApp(t)
	fake.actionErr = errors.New("no code action provider")

	a.menuCodeActions()
	a.handleLSPCodeActions(waitCodeActions(t, a, fake))

	if !strings.Contains(a.statusMsg, "no code action provider") {
		t.Errorf("status = %q, want the server's own reason", a.statusMsg)
	}
}

// TestCodeActions_StaleGenerationDropped pins why this verb is
// generation-checked at all: a picked row WRITES FILES, so an older list
// must not outlive a newer one and hand back ranges measured against a
// document the newer ask has already been answered about.
func TestCodeActions_StaleGenerationDropped(t *testing.T) {
	a, fake, path, _ := actionTestApp(t)
	fake.actions = []lsp.CodeAction{editAction("Fix", "quickfix", path, 2, 4, 7, "bar")}

	a.menuCodeActions()
	e := waitCodeActions(t, a, fake)
	a.lsp.actionSeq++ // a second ask went out while this answer was in flight

	a.handleLSPCodeActions(e)
	if a.modal != nil {
		t.Errorf("modal = %T, want the superseded answer dropped", a.modal)
	}
}

// TestCodeActions_ResponseForAnotherFileDropped pins the symbols verb's
// rule: a list of things to do to a file the user has left is a list of rows
// that all do the wrong thing.
func TestCodeActions_ResponseForAnotherFileDropped(t *testing.T) {
	a, fake, path, _ := actionTestApp(t)
	fake.actions = []lsp.CodeAction{editAction("Fix", "quickfix", path, 2, 4, 7, "bar")}

	a.menuCodeActions()
	e := waitCodeActions(t, a, fake)
	other := wsTestFile(t, a, "other.go", "package main\n")
	wsOpenTab(t, a, other)

	a.handleLSPCodeActions(e)
	if a.modal != nil {
		t.Errorf("modal = %T, want the answer for the departed file dropped", a.modal)
	}
}

// TestCodeActions_RowLabelPutsTheFamilyLast pins symbolLabel's rule. The
// fuzzy scorer rewards early matches, so a leading "quickfix " would make
// every row score alike on the first letters typed.
func TestCodeActions_RowLabelPutsTheFamilyLast(t *testing.T) {
	got := codeActionRowLabel(lsp.CodeAction{Title: "Organize imports", Kind: "source.organizeImports"})
	if !strings.HasPrefix(got, "Organize imports") {
		t.Errorf("label = %q, want the title first", got)
	}
	if !strings.HasSuffix(got, "source") {
		t.Errorf("label = %q, want the kind family trailing", got)
	}
	if bare := codeActionRowLabel(lsp.CodeAction{Title: "Upgrade"}); bare != "Upgrade" {
		t.Errorf("kindless label = %q, want no trailing separator", bare)
	}
}

// -----------------------------------------------------------------------------
// Running a picked action
// -----------------------------------------------------------------------------

// TestCodeActions_AppliesAnActionsEdit is the happy path: the picked row's
// edit goes through the workspace-edit primitive, buffer and all, and comes
// back with one press.
func TestCodeActions_AppliesAnActionsEdit(t *testing.T) {
	a, fake, path, tab := actionTestApp(t)
	fake.actions = []lsp.CodeAction{editAction("Rename to bar", "quickfix", path, 2, 4, 7, "bar")}

	pickRow(t, a, actionPicker(t, a, fake), 0)

	if got, want := tab.Buffer.Lines[2], "var bar int"; got != want {
		t.Errorf("buffer line = %q, want %q", got, want)
	}
	if a.wsGroup == nil {
		t.Fatal("no undo group armed — a code action must come back with one press")
	}
	if a.wsGroup.label != "Rename to bar" {
		t.Errorf("group label = %q, want the action's own title", a.wsGroup.label)
	}
}

// TestCodeActions_LabelIsTheActionTitleEverywhere pins renameLabel's rule
// for this verb: one spelling reaching the flash, the ≡ undo row and the
// receipt heading, so the three can never drift.
func TestCodeActions_LabelIsTheActionTitleEverywhere(t *testing.T) {
	a, fake, path, _ := actionTestApp(t)
	fake.actions = []lsp.CodeAction{editAction("Fill struct", "refactor.rewrite", path, 2, 4, 7, "bar")}

	pickRow(t, a, actionPicker(t, a, fake), 0)

	want := codeActionLabel("Fill struct")
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

// TestCodeActions_CommandOnlyActionExecutes pins the second route. A bare
// Command has nothing to apply — what it changes comes back later as a
// workspace/applyEdit — so the row's whole job is putting the command on the
// wire with its arguments intact.
func TestCodeActions_CommandOnlyActionExecutes(t *testing.T) {
	a, fake, _, _ := actionTestApp(t)
	args := []json.RawMessage{json.RawMessage(`{"URI":"file:///p","Fix":"stubMethods"}`)}
	fake.actions = []lsp.CodeAction{{
		Title:   "Implement interface",
		Command: &lsp.Command{Title: "Implement interface", Command: "gopls.apply_fix", Arguments: args},
	}}

	pickRow(t, a, actionPicker(t, a, fake), 0)
	waitCommandDone(t, a)

	if fake.execCmd != "gopls.apply_fix" {
		t.Errorf("command on the wire = %q, want gopls.apply_fix", fake.execCmd)
	}
	if len(fake.execArgs) != 1 || string(fake.execArgs[0]) != string(args[0]) {
		t.Errorf("arguments = %v, want the server's payload verbatim", fake.execArgs)
	}
}

// TestCodeActions_EditRunsBeforeItsCommand pins the spec's order for an
// action carrying both, which is also the only order that makes sense: the
// command generally exists to react to what the edit did.
func TestCodeActions_EditRunsBeforeItsCommand(t *testing.T) {
	a, fake, path, tab := actionTestApp(t)
	fake.actions = []lsp.CodeAction{{
		Title:   "Fix and reload",
		Edit:    wsEdit(path, 2, 4, 7, "bar"),
		Command: &lsp.Command{Title: "Reload", Command: "gopls.reload"},
	}}

	pickRow(t, a, actionPicker(t, a, fake), 0)
	waitCommandDone(t, a)

	if got := tab.Buffer.Lines[2]; got != "var bar int" {
		t.Errorf("buffer line = %q, want the edit applied", got)
	}
	if fake.execCmd != "gopls.reload" {
		t.Errorf("command = %q, want the follow-up to have run too", fake.execCmd)
	}
}

// TestCodeActions_RefusedEditSkipsTheCommand pins the guard that keeps a
// half-done action from happening: if the edit was refused, running its
// follow-up would react to something that never occurred.
func TestCodeActions_RefusedEditSkipsTheCommand(t *testing.T) {
	a, fake, _, _ := actionTestApp(t)
	fake.actions = []lsp.CodeAction{{
		Title: "Fix and reload",
		// A resource operation ced declared it cannot perform: refused by
		// name, before anything is applied.
		Edit: &lsp.WorkspaceEdit{Resources: []lsp.ResourceOp{
			{Kind: lsp.ResourceCreate, Path: "/tmp/new.go", URI: "file:///tmp/new.go"},
		}},
		Command: &lsp.Command{Title: "Reload", Command: "gopls.reload"},
	}}

	pickRow(t, a, actionPicker(t, a, fake), 0)

	if fake.execCmd != "" {
		t.Errorf("command %q ran after the edit was refused", fake.execCmd)
	}
	if !strings.Contains(a.statusMsg, "can't do that") {
		t.Errorf("status = %q, want the resource-op refusal", a.statusMsg)
	}
}

// TestCodeActions_CommandFailureFlashes pins that a failed command says so.
// A successful one stays quiet — it already announced itself through
// whatever applyEdit it sent, and a second "done" would claim credit for
// changes that may have been refused.
func TestCodeActions_CommandFailureFlashes(t *testing.T) {
	a, _, _, _ := actionTestApp(t)

	a.handleLSPCommand(&lspCommandEvent{title: "Upgrade", err: errors.New("network is down")})
	if !strings.Contains(a.statusMsg, "network is down") {
		t.Errorf("status = %q, want the command's failure", a.statusMsg)
	}

	a.statusMsg = ""
	a.handleLSPCommand(&lspCommandEvent{title: "Upgrade"})
	if a.statusMsg != "" {
		t.Errorf("status = %q, want silence on success", a.statusMsg)
	}
}

// -----------------------------------------------------------------------------
// The server-initiated route
// -----------------------------------------------------------------------------

// applyEditReply drives a server-initiated edit on the main loop and returns
// the answer that went back on the wire.
func applyEditReply(t *testing.T, a *App, label string, edit *lsp.WorkspaceEdit) lsp.ApplyEditResult {
	t.Helper()
	ev := &lspApplyEditEvent{
		when: time.Now(), label: label, edit: edit,
		reply: make(chan lsp.ApplyEditResult, 1),
	}
	a.handleLSPApplyEdit(ev)
	select {
	case res := <-ev.reply:
		return res
	default:
		t.Fatal("applyEdit was never answered — the request goroutine would hang")
		return lsp.ApplyEditResult{}
	}
}

// awaitReply takes the answer a deferred applyEdit eventually sends. It is a
// timed receive rather than a bare one on purpose: the failure this guards —
// a path that never answers — would otherwise hang CI instead of failing it,
// which is exactly what a missing cancel hook looks like.
func awaitReply(t *testing.T, ch chan lsp.ApplyEditResult) lsp.ApplyEditResult {
	t.Helper()
	select {
	case res := <-ch:
		return res
	case <-time.After(2 * time.Second):
		t.Fatal("applyEdit was never answered — the request goroutine would hang")
		return lsp.ApplyEditResult{}
	}
}

// TestApplyEdit_AppliesAndReportsApplied pins the whole reason the primitive
// grew an outcome callback. This edit arrives as a REQUEST with a JSON-RPC
// id waiting on a field literally called `applied`, so it is the first edit
// in the codebase ced must report on rather than merely perform.
func TestApplyEdit_AppliesAndReportsApplied(t *testing.T) {
	a, _, path, tab := actionTestApp(t)

	res := applyEditReply(t, a, "Extract function", wsEdit(path, 2, 4, 7, "bar"))

	if !res.Applied {
		t.Errorf("result = %+v, want applied", res)
	}
	if got := tab.Buffer.Lines[2]; got != "var bar int" {
		t.Errorf("buffer line = %q, want the edit applied", got)
	}
	if a.wsGroup == nil || a.wsGroup.label != "Extract function" {
		t.Errorf("group = %+v, want the server's own label", a.wsGroup)
	}
}

// TestApplyEdit_RefusalReportsAReason pins that a "no" carries a sentence.
// failureReason is what the server shows the user, so an empty one turns a
// refusal ced made deliberately into an unexplained failure.
func TestApplyEdit_RefusalReportsAReason(t *testing.T) {
	a, _, _, _ := actionTestApp(t)

	res := applyEditReply(t, a, "Move file", &lsp.WorkspaceEdit{Resources: []lsp.ResourceOp{
		{Kind: lsp.ResourceRename, Path: "/a.go", NewPath: "/b.go"},
	}})

	if res.Applied {
		t.Fatal("a refused edit reported itself as applied")
	}
	if !strings.Contains(res.FailureReason, "rename") {
		t.Errorf("failureReason = %q, want it to name what ced can't do", res.FailureReason)
	}
}

// TestApplyEdit_EmptyEditReportsNothingToChange pins that "nothing to
// change" is a real answer rather than a failure — the same distinction
// rename's null result makes.
func TestApplyEdit_EmptyEditReportsNothingToChange(t *testing.T) {
	a, _, _, _ := actionTestApp(t)

	res := applyEditReply(t, a, "No-op", &lsp.WorkspaceEdit{})
	if res.Applied || !strings.Contains(res.FailureReason, "nothing to change") {
		t.Errorf("result = %+v, want a reasoned refusal", res)
	}
}

// TestApplyEdit_DeclinedConfirmationReportsNotApplied is the case acceptance
// and outcome come apart, and the reason applyServerEditWith exists at all.
// An edit reaching files the user never opened asks first; a "no" there has
// to reach the server as applied:false, not as the acceptance that opened
// the dialog.
func TestApplyEdit_DeclinedConfirmationReportsNotApplied(t *testing.T) {
	a, _, _, _ := actionTestApp(t)
	// A file with no tab: the primitive confirms before writing it.
	unopened := wsTestFile(t, a, "other.go", "package main\n\nvar foo int\n")

	ev := &lspApplyEditEvent{
		when: time.Now(), label: "Extract function",
		edit:  wsEdit(unopened, 2, 4, 7, "bar"),
		reply: make(chan lsp.ApplyEditResult, 1),
	}
	a.handleLSPApplyEdit(ev)

	cm, ok := a.modal.(*confirmModal)
	if !ok {
		t.Fatalf("modal = %T, want a confirmation before writing an unopened file", a.modal)
	}
	select {
	case res := <-ev.reply:
		t.Fatalf("answered %+v while the confirmation was still open", res)
	default:
	}

	cm.cancel(a)

	res := awaitReply(t, ev.reply)
	if res.Applied {
		t.Fatal("a declined edit reported itself as applied")
	}
	if !strings.Contains(res.FailureReason, "declined") {
		t.Errorf("failureReason = %q, want it to say the user declined", res.FailureReason)
	}
	if body, err := os.ReadFile(unopened); err != nil || strings.Contains(string(body), "bar") {
		t.Errorf("file = %q, want it untouched after a decline", body)
	}
}

// TestApplyEdit_AcceptedConfirmationReportsApplied is the other half: the
// answer is deferred until the user says yes, and then it is true.
func TestApplyEdit_AcceptedConfirmationReportsApplied(t *testing.T) {
	a, _, _, _ := actionTestApp(t)
	unopened := wsTestFile(t, a, "other.go", "package main\n\nvar foo int\n")

	ev := &lspApplyEditEvent{
		when: time.Now(), label: "Extract function",
		edit:  wsEdit(unopened, 2, 4, 7, "bar"),
		reply: make(chan lsp.ApplyEditResult, 1),
	}
	a.handleLSPApplyEdit(ev)

	cm, ok := a.modal.(*confirmModal)
	if !ok {
		t.Fatalf("modal = %T, want a confirmation", a.modal)
	}
	cm.yes(a)

	res := awaitReply(t, ev.reply)
	if !res.Applied {
		t.Errorf("result = %+v, want applied after the user agreed", res)
	}
	body, err := os.ReadFile(unopened)
	if err != nil || !strings.Contains(string(body), "var bar int") {
		t.Errorf("file = %q (%v), want the edit on disk", body, err)
	}
}

// TestApplyEdit_AnsweredExactlyOnce pins the contract the reply channel
// needs. The channel is buffered for one, so a second send would either
// block the main loop or panic on a closed channel — the flag is what stops
// a confirmation that somehow ran both hooks from doing either.
func TestApplyEdit_AnsweredExactlyOnce(t *testing.T) {
	a, _, _, _ := actionTestApp(t)
	unopened := wsTestFile(t, a, "other.go", "package main\n\nvar foo int\n")

	ev := &lspApplyEditEvent{
		when: time.Now(), label: "Extract function",
		edit:  wsEdit(unopened, 2, 4, 7, "bar"),
		reply: make(chan lsp.ApplyEditResult, 1),
	}
	a.handleLSPApplyEdit(ev)
	cm := a.modal.(*confirmModal)

	cm.cancel(a)
	// A second dismissal (a click landing after the Esc, say) must not
	// answer again.
	if cm.cancelHook != nil {
		cm.cancelHook(a)
	}

	if len(ev.reply) != 1 {
		t.Fatalf("reply channel holds %d answers, want exactly 1", len(ev.reply))
	}
}

// TestApplyEdit_LabelFallsBackWhenTheServerOmitsIt pins that the gesture
// always has a name: the label becomes the ≡ undo row, which a user reads
// cold, and an empty one would read as a bug.
func TestApplyEdit_LabelFallsBackWhenTheServerOmitsIt(t *testing.T) {
	a, _, path, _ := actionTestApp(t)

	applyEditReply(t, a, "", wsEdit(path, 2, 4, 7, "bar"))

	if a.wsGroup == nil || a.wsGroup.label != "Server edit" {
		t.Errorf("group = %+v, want the fallback label", a.wsGroup)
	}
}

// TestServeApplyEdit_PostsAndWaits pins the transport half: the serving
// goroutine posts an event carrying its reply channel and blocks for the
// main loop's verdict — the ACP permission-request shape, for the same
// reason. It takes the SCREEN rather than the App because it runs off-loop.
func TestServeApplyEdit_PostsAndWaits(t *testing.T) {
	a, _, path, _ := actionTestApp(t)

	resCh := make(chan lsp.ApplyEditResult, 1)
	go func() {
		res, _ := lspServeApplyEdit(a.screen, json.RawMessage(
			`{"label":"Extract","edit":{"changes":{"`+lsp.PathToURI(path)+`":[`+
				`{"range":{"start":{"line":2,"character":4},"end":{"line":2,"character":7}},"newText":"bar"}]}}}`))
		r, _ := res.(lsp.ApplyEditResult)
		resCh <- r
	}()

	deadline := time.Now().Add(2 * time.Second)
	for a.modal == nil && time.Now().Before(deadline) {
		if ev, ok := a.screen.PollEvent().(*lspApplyEditEvent); ok {
			a.handleLSPApplyEdit(ev)
		}
	}
	select {
	case res := <-resCh:
		if !res.Applied {
			t.Errorf("result = %+v, want applied", res)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("the serving goroutine never got its answer")
	}
}

// TestServeApplyEdit_NoScreenRefuses pins the degenerate case: a torn-down
// editor answers rather than blocking a server that is waiting on it.
func TestServeApplyEdit_NoScreenRefuses(t *testing.T) {
	res, err := lspServeApplyEdit(nil, json.RawMessage(`{"label":"x"}`))
	if err != nil {
		t.Fatalf("err = %v, want a result rather than a protocol error", err)
	}
	r, _ := res.(lsp.ApplyEditResult)
	if r.Applied || r.FailureReason == "" {
		t.Errorf("result = %+v, want a reasoned refusal", r)
	}
}

// -----------------------------------------------------------------------------
// Surfaces
// -----------------------------------------------------------------------------

// TestCodeActions_MenuAndLeaderAgree pins that both keyboard paths run the
// same verb — the menu's hint column lies the moment they diverge.
func TestCodeActions_MenuAndLeaderAgree(t *testing.T) {
	var row *menuItemDef
	for _, g := range builtinMenuGroups() {
		if g.title != "Code" {
			continue
		}
		for i := range g.items {
			if g.items[i].shortcut == "esc c" {
				row = &g.items[i]
			}
		}
	}
	if row == nil {
		t.Fatal("no ≡ Code row advertising esc c")
	}
	if row.labelFor == nil {
		t.Error("the code-action row needs a dynamic label — it names the span it will ask about")
	}
	found := false
	for _, b := range leaderBindings() {
		if b.key == 'c' {
			found = true
			if b.sub != nil || b.subFor != nil {
				t.Error("'c' is bound as a prefix, not the code-action verb")
			}
		}
	}
	if !found {
		t.Error("no leader binding on 'c'")
	}
}

// TestCodeActionMenuLabel_NamesTheSpan pins the one thing a user has to know
// before pressing this: a selection changes the answer completely, so the
// row says which span the question will cover.
func TestCodeActionMenuLabel_NamesTheSpan(t *testing.T) {
	a, _, _, tab := actionTestApp(t)

	if got := a.codeActionMenuLabel(); strings.Contains(got, "selection") {
		t.Errorf("label = %q, want no mention of a selection when there is none", got)
	}
	tab.Anchor = editor.Position{Line: 2, Col: 4}
	tab.Cursor = editor.Position{Line: 2, Col: 7}
	if got := a.codeActionMenuLabel(); !strings.Contains(got, "selection") {
		t.Errorf("label = %q, want it to say the question covers the selection", got)
	}
}

// TestCodeActions_RangeOverlap pins the predicate that decides which
// diagnostics get echoed. A zero-width range has to count as touching what
// contains it, or a cursor sitting on an error would carry none of it.
func TestCodeActions_RangeOverlap(t *testing.T) {
	span := func(l1, c1, l2, c2 int) lsp.Range {
		return lsp.Range{Start: lsp.Position{Line: l1, Character: c1}, End: lsp.Position{Line: l2, Character: c2}}
	}
	cases := []struct {
		name string
		x, y lsp.Range
		want bool
	}{
		{"cursor inside", span(2, 0, 2, 9), span(2, 4, 2, 4), true},
		{"cursor at the start edge", span(2, 4, 2, 9), span(2, 4, 2, 4), true},
		{"cursor at the end edge", span(2, 4, 2, 9), span(2, 9, 2, 9), true},
		{"before", span(2, 0, 2, 3), span(2, 4, 2, 6), false},
		{"after", span(3, 0, 3, 3), span(2, 4, 2, 6), false},
		{"multi-line straddle", span(1, 0, 4, 0), span(2, 4, 2, 6), true},
	}
	for _, c := range cases {
		if got := lspRangesOverlap(c.x, c.y); got != c.want {
			t.Errorf("%s: overlap = %v, want %v", c.name, got, c.want)
		}
	}
}

// waitCommandDone drains the queue until the executeCommand goroutine
// reports back, so the fake's record is settled before the test reads it.
func waitCommandDone(t *testing.T, a *App) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if _, ok := a.screen.PollEvent().(*lspCommandEvent); ok {
			return
		}
	}
	t.Fatal("no command-done event arrived")
}

// TestApplyEdit_RefusesWhileADialogIsOpen pins the guard that keeps an
// UNPROMPTED edit from stealing the modal slot. openModal replaces rather
// than refuses, so applying here would pop a confirmation over something the
// user is mid-answer on and silently drop that modal's own pending reply.
// Unlike a chat permission request — where an agent is stuck and the prompt
// queues — a server can simply be told no.
func TestApplyEdit_RefusesWhileADialogIsOpen(t *testing.T) {
	a, _, path, tab := actionTestApp(t)
	before := tab.Buffer.Lines[2]
	a.openPrompt("Busy", "", "", func(*App, string) {})
	held := a.modal

	res := applyEditReply(t, a, "Extract function", wsEdit(path, 2, 4, 7, "bar"))

	if res.Applied {
		t.Error("an edit landed while a dialog owned the screen")
	}
	if !strings.Contains(res.FailureReason, "busy") {
		t.Errorf("failureReason = %q, want it to say why", res.FailureReason)
	}
	if a.modal != held {
		t.Errorf("modal = %T, want the user's own dialog left in place", a.modal)
	}
	if tab.Buffer.Lines[2] != before {
		t.Errorf("buffer line = %q, want it untouched", tab.Buffer.Lines[2])
	}
}
