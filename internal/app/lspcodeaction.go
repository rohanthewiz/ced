// =============================================================================
// File: internal/app/lspcodeaction.go
// Author: Rohan Allison <rohanthewiz@gmail.com>
// Created: 2026-08-03
// Copyright: 2026 Rohan Allison. All rights reserved.
// =============================================================================

// lspcodeaction.go is "Code actions" — what the server can do to the cursor
// or the selection: fix this error, organize these imports, extract that
// block into a function.
//
// It is the second verb built on the workspace-edit primitive, and the one
// that was queued to PROVE the primitive rather than merely use it. Rename
// asks a question and applies the answer; this arrives by two different
// routes, one of which isn't a response at all:
//
//	Esc c / ≡ Code ──► menuCodeActions ──goroutine──► textDocument/codeAction
//	                                                          │
//	              openPicker("Code action") ◄── lspCodeActionsEvent
//	                          │
//	         ┌────────────────┴────────────────┐
//	         │ act.Edit                        │ act.Command only
//	         ▼                                 ▼
//	   applyServerEdit             workspace/executeCommand ──goroutine
//	                                           │  (server runs it)
//	                          applyServerEditWith ◄── lspApplyEditEvent
//	                                                  ▲
//	                                    server ──workspace/applyEdit
//
// THE SECOND ROUTE IS A SERVER REQUEST, and everything awkward about this
// file follows from that. The edit arrives unprompted, on the client's read
// loop, with a JSON-RPC id waiting for an answer that says whether it was
// applied — so this is the first edit in the codebase that ced must REPORT
// ON rather than merely perform. That is what applyServerEditWith exists
// for, and it was the one addition the primitive needed (its doc comment
// carries the argument).
//
// The rest reuses what was already there, which was the point of the
// exercise: the empty wsRequest makes no origin claim, because a
// server-initiated edit has no origin position to have gone stale; the
// per-participant sync checks still run and are the ones that matter. The
// handler blocks the serving goroutine while the main loop decides — the ACP
// permission-request shape, for the same reason (a decision the loop makes,
// possibly with a confirmation dialog in the middle of it).
//
// The PICKER sits between the response and the apply the way rename's prompt
// sits between the ask and the request, and it raises the same staleness
// question: the ranges were measured against the document as it was when the
// question went out. So the contract is captured at ask time (after the
// flush) and validated when a row FIRES, not when the list arrives.

package app

import (
	"encoding/json"
	"time"

	"github.com/gdamore/tcell/v2"

	"github.com/rohanthewiz/ced/internal/editor"
	"github.com/rohanthewiz/ced/internal/lsp"
)

// wsApplyTimeout bounds how long the goroutine serving a
// workspace/applyEdit waits for the main loop's verdict.
//
// It is deliberately SHORTER than the client's executeCommandTimeout, so a
// user who walks away from the confirmation releases the server rather than
// the server timing out on ced. The cost of that ordering is a narrow
// window: a confirmation answered after the deadline still applies the edit,
// having already reported that it didn't. Which is the right way round — the
// report is a courtesy to the server's log, the edit is what the user asked
// for.
const wsApplyTimeout = 90 * time.Second

// lspCodeActionsEvent carries a codeAction response back to the main loop.
//
// It ferries the wsRequest along for rename's reason: the contract describes
// what was true when the question was ASKED, and the picker this opens may
// sit on screen for a while before a row fires.
type lspCodeActionsEvent struct {
	when    time.Time
	seq     int
	path    string
	actions []lsp.CodeAction
	req     wsRequest
	err     error
}

// When satisfies the tcell.Event interface.
func (e *lspCodeActionsEvent) When() time.Time { return e.when }

// lspApplyEditEvent is a server-initiated workspace/applyEdit crossing from
// the client's request goroutine to the main loop.
//
// reply is buffered, so the main loop can never block handing back an answer
// even if the serving goroutine has already given up and gone home.
type lspApplyEditEvent struct {
	when  time.Time
	label string
	edit  *lsp.WorkspaceEdit
	reply chan lsp.ApplyEditResult
}

// When satisfies the tcell.Event interface.
func (e *lspApplyEditEvent) When() time.Time { return e.when }

// lspCommandEvent reports that an executeCommand finished. Its only payload
// is a failure — whatever the command CHANGED came back separately as an
// applyEdit, which has already announced itself.
type lspCommandEvent struct {
	when  time.Time
	title string
	err   error
}

// When satisfies the tcell.Event interface.
func (e *lspCommandEvent) When() time.Time { return e.when }

// -----------------------------------------------------------------------------
// Asking
// -----------------------------------------------------------------------------

// menuCodeActions is the ≡ Code row and the Esc-c leader: ask the server
// what it can do here.
//
// "Here" is the SELECTION when there is one and the cursor otherwise, and
// that distinction is the whole interface. A refactoring like "extract to
// function" only exists for a range, so a verb that always asked about a
// point would silently never offer half of what the server has; a quick fix
// only exists where a diagnostic is, so a verb that always asked about a
// selection would need one before it could offer anything.
func (a *App) menuCodeActions() {
	a.closeMenu()
	t := a.activeTabPtr()
	if t == nil || !a.hasLSPActions() {
		return
	}
	client, scr := a.lsp.client, a.screen
	if client == nil || scr == nil {
		return
	}
	path := t.Path

	// Flush FIRST, then capture, then convert — rename's ordering, and
	// load-bearing for the same reason. The server has to answer from the
	// text on screen (an action for a line the user just typed is the common
	// case), and a contract captured before the flush would record the
	// pre-sync revision and make the plan refuse its own request.
	a.lspFlushChange(t)
	req := a.captureWSRequest(t)
	rng := codeActionRange(t)
	diags := a.diagsForRange(path, rng)

	a.lsp.actionSeq++
	seq := a.lsp.actionSeq
	go func() {
		acts, err := client.CodeActions(path, rng, diags)
		_ = scr.PostEvent(&lspCodeActionsEvent{
			when: time.Now(), seq: seq, path: path,
			actions: acts, req: req, err: err,
		})
	}()
}

// codeActionRange is the span the question is asked about: the selection in
// document order, or a zero-width range at the cursor.
func codeActionRange(t *editor.Tab) lsp.Range {
	if !t.HasSelection() {
		p := lspPosFor(t, t.Cursor)
		return lsp.Range{Start: p, End: p}
	}
	from, to := editor.PosOrdered(t.Buffer.Clamp(t.Anchor), t.Buffer.Clamp(t.Cursor))
	return lsp.Range{Start: lspPosFor(t, from), End: lspPosFor(t, to)}
}

// diagsForRange collects the diagnostics that touch rng, to be echoed back
// in the request's context.
//
// This is how a quick fix finds the problem it fixes, so the objects go back
// VERBATIM (lsp.Diagnostic round-trips its raw JSON) — the fields that do
// the matching are server-private ones this client never modelled. Overlap
// is deliberately generous: a zero-width range at the cursor has to match a
// diagnostic that merely CONTAINS it, or asking for a fix while standing on
// the error would find nothing.
func (a *App) diagsForRange(path string, rng lsp.Range) []lsp.Diagnostic {
	all := a.lsp.diags[path]
	if len(all) == 0 {
		return nil
	}
	out := make([]lsp.Diagnostic, 0, len(all))
	for _, d := range all {
		if lspRangesOverlap(d.Range, rng) {
			out = append(out, d)
		}
	}
	return out
}

// lspRangesOverlap reports whether two LSP ranges share any position,
// treating a zero-width range as touching whatever contains it.
func lspRangesOverlap(x, y lsp.Range) bool {
	return !lspPosBefore(x.End, y.Start) && !lspPosBefore(y.End, x.Start)
}

// lspPosBefore reports whether p sorts strictly before q.
func lspPosBefore(p, q lsp.Position) bool {
	if p.Line != q.Line {
		return p.Line < q.Line
	}
	return p.Character < q.Character
}

// -----------------------------------------------------------------------------
// Choosing
// -----------------------------------------------------------------------------

// handleLSPCodeActions lands a codeAction response and opens the picker.
//
// The generation check is rename's, for rename's reason: this answer can
// WRITE FILES, so an older list must not outlive a newer one and hand back
// ranges measured against a document that has since been rewritten. The path
// check is the symbols verb's — a list of things to do to a file the user has
// left is a list of rows that all do the wrong thing.
func (a *App) handleLSPCodeActions(e *lspCodeActionsEvent) {
	if e.seq != a.lsp.actionSeq {
		return // superseded by a later ask
	}
	t := a.activeTabPtr()
	if t == nil || t.Path != e.path {
		return
	}
	if e.err != nil {
		a.flash("Code actions: " + e.err.Error())
		return
	}
	if len(e.actions) == 0 {
		// A real answer, and the usual cause is the position rather than the
		// server — so the message names it, the way signature help's does.
		a.flash("No code actions here")
		return
	}
	items := make([]paletteItem, 0, len(e.actions))
	for _, act := range e.actions {
		items = append(items, paletteItem{
			label: codeActionRowLabel(act),
			run:   func(app *App) { app.runCodeAction(act, e.req) },
		})
	}
	a.openPicker("Code action", items)
}

// codeActionRowLabel renders one picker row: the server's title, then the
// kind's family.
//
// The family goes LAST for symbolLabel's reason — the fuzzy scorer rewards
// early matches, so a leading "quickfix " would make every row score alike
// on the first letters typed. Trailing, it still lets a user narrow to the
// fixes while leaving the title in the position that ranks.
func codeActionRowLabel(act lsp.CodeAction) string {
	if fam := act.KindFamily(); fam != "" {
		return act.Title + "  " + fam
	}
	return act.Title
}

// -----------------------------------------------------------------------------
// Running
// -----------------------------------------------------------------------------

// runCodeAction is what a picked row does. An action may carry an edit, a
// command, or both — and when it carries both the spec's order is edit
// first, which is also the only order that makes sense: the command
// generally exists to react to what the edit did.
func (a *App) runCodeAction(act lsp.CodeAction, req wsRequest) {
	label := codeActionLabel(act.Title)
	if act.Edit != nil {
		// Everything hard belongs to the primitive: planning, the
		// confirmation when the edit reaches files the user never opened,
		// the rollback, the one-press cross-file undo, the receipt. A refusal
		// has already said why, so a failed apply must not also fire the
		// command — that would run the follow-up to an edit that never
		// happened.
		if !a.applyServerEdit(act.Edit, label, req) {
			return
		}
	}
	if act.Command == nil {
		return
	}
	a.runServerCommand(act.Command)
}

// runServerCommand executes one of the server's own commands off-loop.
//
// Whatever it CHANGES arrives separately, as a workspace/applyEdit request
// landing while this call is still open — which is why the goroutine can sit
// here for minutes without anything being wrong, and why the client gives
// this call its own long budget (lsp.executeCommandTimeout). The response
// itself is server-private and discarded.
func (a *App) runServerCommand(cmd *lsp.Command) {
	if !a.lspReady() || a.screen == nil {
		a.flash("Code action: no language server")
		return
	}
	client, scr := a.lsp.client, a.screen
	title := cmd.Title
	if title == "" {
		title = cmd.Command
	}
	a.flash(title + "…")
	go func() {
		err := client.ExecuteCommand(cmd.Command, cmd.Arguments)
		_ = scr.PostEvent(&lspCommandEvent{when: time.Now(), title: title, err: err})
	}()
}

// handleLSPCommand reports a failed command and stays quiet about a
// successful one — success already announced itself through whatever
// applyEdit the command sent, and a second "done" would claim credit for
// changes that may have been refused.
func (a *App) handleLSPCommand(e *lspCommandEvent) {
	if e.err != nil {
		a.flash(e.title + ": " + e.err.Error())
	}
}

// codeActionLabel is the one spelling of a code action, so the confirmation
// title, the flash, the ≡ undo row and the receipt heading can never drift —
// renameLabel's rule, and the only thing this verb owes the primitive.
func codeActionLabel(title string) string {
	if title == "" {
		return "Code action"
	}
	return title
}

// -----------------------------------------------------------------------------
// The server-initiated route
// -----------------------------------------------------------------------------

// lspServeApplyEdit answers a workspace/applyEdit request. It runs on the
// client's own per-request goroutine (never the read loop), which is what
// lets it BLOCK here while the main loop plans the edit and possibly asks
// the user to confirm it — the ACP permission-request shape, for the same
// reason.
//
// A timeout answers "not applied" rather than leaving the server hanging: a
// wedged editor and a slow user look identical from the server's side, and
// the honest thing to tell it is that nothing happened yet.
//
// It takes the SCREEN rather than the App, and is a free function for that
// reason: this runs off the main loop, where the App is off limits, and the
// screen is the whole of what posting an event needs.
func lspServeApplyEdit(scr tcell.Screen, params json.RawMessage) (any, error) {
	if scr == nil {
		return lsp.ApplyEditResult{FailureReason: "the editor is not running"}, nil
	}
	label, edit := lsp.ParseApplyEditRequest(params)
	reply := make(chan lsp.ApplyEditResult, 1)
	if err := scr.PostEvent(&lspApplyEditEvent{
		when: time.Now(), label: label, edit: edit, reply: reply,
	}); err != nil {
		return lsp.ApplyEditResult{FailureReason: "the editor is busy"}, nil
	}
	select {
	case res := <-reply:
		return res, nil
	case <-time.After(wsApplyTimeout):
		return lsp.ApplyEditResult{FailureReason: "the editor did not answer in time"}, nil
	}
}

// handleLSPApplyEdit applies a server-initiated edit on the main loop and
// answers the request that carried it.
//
// The wsRequest is EMPTY on purpose. A server-initiated edit has no origin
// position that could have gone stale — nobody asked a question — so there
// is no revision to claim. What still runs, and what actually matters here,
// is the per-participant check: every open file the edit touches must be at
// the revision the server has been told about, or the coordinates were
// measured against text nobody has.
func (a *App) handleLSPApplyEdit(e *lspApplyEditEvent) {
	// answered guards the "exactly once" contract the reply channel needs —
	// the chat permission layer's flag, for the same reason. The channel is
	// buffered, so this never blocks the loop.
	answered := false
	done := func(_ *App, ok bool, reason string) {
		if answered {
			return
		}
		answered = true
		e.reply <- lsp.ApplyEditResult{Applied: ok, FailureReason: reason}
	}
	// This edit is UNPROMPTED — it can land at any moment, including while
	// the user is reading a dialog or typing into a prompt. openModal
	// replaces rather than refuses, so applying now could pop a confirmation
	// over something the user is mid-answer on and drop that modal's own
	// pending reply. Refusing is the honest move and costs nothing: unlike a
	// chat permission request (which queues, because an agent is stuck), a
	// server can simply be told no and the user can run the action again.
	if a.modal != nil || a.menuOpen {
		done(a, false, "the editor is busy with a dialog")
		return
	}
	label := e.label
	if label == "" {
		// Servers may omit it. "Server edit" is vague but honest, and the
		// receipt lists exactly which lines moved.
		label = "Server edit"
	}
	a.applyServerEditWith(e.edit, label, wsRequest{}, done)
}

// hasCodeActions gates the ≡ row. Identical to the other code-intelligence
// rows: a server, and an active document it understands.
func (a *App) hasCodeActions() bool { return a.hasLSPActions() }

// codeActionMenuLabel names the row for what it will ask about, so the menu
// says whether the question covers the selection or just the cursor —
// the one thing about this verb a user has to know before pressing it.
func (a *App) codeActionMenuLabel() string {
	if t := a.activeTabPtr(); t != nil && t.HasSelection() {
		return "Code actions for selection…"
	}
	return "Code actions…"
}
