// =============================================================================
// File: internal/lsp/codeaction.go
// Author: Rohan Allison <rohanthewiz@gmail.com>
// Created: 2026-08-03
// Copyright: 2026 Rohan Allison. All rights reserved.
// =============================================================================

// codeaction.go is the wire half of code actions — the quick fixes,
// refactorings and source transformations a server offers for a position or
// a selection, plus the two ways one of them can reach the editor.
//
//	textDocument/codeAction ──► (Command | CodeAction)[]
//	                                 │
//	           ┌─────────────────────┴─────────────────────┐
//	           │ has .edit                                 │ has only .command
//	           ▼                                           ▼
//	   apply it directly                    workspace/executeCommand
//	                                                       │
//	                              server ──workspace/applyEdit──► editor
//
// Both paths end in a WorkspaceEdit, which is why this file adds no applying
// of its own: workspaceedit.go already owns that shape and the app layer
// already owns the primitive that applies it. What this file owes is the
// union collapse, and there are TWO of them.
//
// THE RESPONSE UNION. A server may answer with bare Commands (the LSP 3.8
// shape) or CodeAction literals (3.8+, gated on the client declaring
// codeActionLiteralSupport). Both collapse into CodeAction here, per this
// package's standing rule: a consumer that learns the protocol has two
// answers will eventually handle only the one its own server happens to
// send. The discriminator is the SHAPE OF THE `command` FIELD — a string on
// a bare Command, an object on a CodeAction that carries one — and not a
// failed unmarshal, which is the trap ParseDocumentSymbols and
// ParseWorkspaceEdit both document. A bare Command decodes cleanly as a
// CodeAction with everything zeroed, so an error-based sniff would turn
// "run this command" into an action that does nothing at all.
//
// THE ARGUMENTS ARE KEPT RAW. A command's arguments are the server's own
// private payload — gopls packs file URIs, ranges and internal identifiers
// in there — and they have to go back out byte-for-byte in the
// executeCommand that follows. Modelling them would mean losing every field
// this client doesn't know about, which is the same reason the Copilot layer
// echoes a completion item's raw JSON back in its telemetry.

package lsp

import (
	"encoding/json"
	"strings"
)

// Command is a server-defined action the editor asks the server to run.
// It is both a response shape of its own and a field on a CodeAction.
type Command struct {
	Title   string `json:"title"`
	Command string `json:"command"`
	// Arguments stay raw so they round-trip verbatim (see the file comment).
	Arguments []json.RawMessage `json:"arguments,omitempty"`
}

// CodeAction is the editor-facing normal form both response shapes collapse
// into: something with a title that, when chosen, produces either a
// WorkspaceEdit to apply or a Command to run.
//
// Edit and Command are not exclusive — the spec allows an action to carry
// both, meaning "apply the edit, then run the command" (gopls does this for
// fixes that also need a build-cache touch). Callers must honour that order.
type CodeAction struct {
	Title string
	// Kind is the spec's dotted hierarchy ("quickfix",
	// "refactor.extract", "source.organizeImports"), kept whole so a
	// caller can filter on a prefix.
	Kind        string
	IsPreferred bool
	Edit        *WorkspaceEdit
	Command     *Command
}

// KindFamily returns the first segment of an action's kind — "quickfix",
// "refactor", "source" — or "" when the server sent no kind.
//
// The family, not the whole kind, is what a picker row wants beside a title.
// The title already names the specific transformation ("Extract function"),
// so repeating "refactor.extract" after it is noise; the family is the axis
// a user actually filters on ("just show me the quick fixes").
func (a CodeAction) KindFamily() string {
	if i := strings.IndexByte(a.Kind, '.'); i >= 0 {
		return a.Kind[:i]
	}
	return a.Kind
}

// CodeActionContext is the required options object of a codeAction request:
// which diagnostics the client believes apply to the range, and optionally
// which kinds it wants back.
type CodeActionContext struct {
	// Diagnostics carries the server's OWN diagnostic objects back to it,
	// which is how a quick fix is matched to the problem it fixes. They are
	// echoed verbatim (Diagnostic round-trips its raw JSON) because the
	// fields that do the matching — `data`, `code`, server-private
	// extensions — are ones this client deliberately doesn't model.
	Diagnostics []Diagnostic `json:"diagnostics"`
	Only        []string     `json:"only,omitempty"`
}

// CodeActionParams is the payload of textDocument/codeAction: a document, a
// range, and that context.
type CodeActionParams struct {
	TextDocument TextDocumentIdentifier `json:"textDocument"`
	Range        Range                  `json:"range"`
	Context      CodeActionContext      `json:"context"`
}

// ExecuteCommandParams is the payload of workspace/executeCommand.
type ExecuteCommandParams struct {
	Command   string            `json:"command"`
	Arguments []json.RawMessage `json:"arguments,omitempty"`
}

// ApplyEditResult is the client's answer to a workspace/applyEdit request.
// FailureReason is what the server shows the user when Applied is false, so
// it has to read as a sentence rather than an error code.
type ApplyEditResult struct {
	Applied       bool   `json:"applied"`
	FailureReason string `json:"failureReason,omitempty"`
}

// ParseCodeActions normalises a codeAction response into the flat CodeAction
// list, tolerating both shapes the protocol allows.
//
// A DISABLED action is dropped rather than listed. The spec suggests clients
// show them greyed with their reason, to signal that a refactoring exists
// but doesn't apply here — and ced's surface for choosing one is the fuzzy
// picker, in which every row is a verb that runs. A row that answers Enter
// with "you can't do that" is worse than a row that isn't offered, and the
// palette has no disabled state to borrow.
func ParseCodeActions(raw json.RawMessage) []CodeAction {
	if len(raw) == 0 || string(raw) == "null" {
		return nil
	}
	var elems []json.RawMessage
	if err := json.Unmarshal(raw, &elems); err != nil {
		return nil
	}
	out := make([]CodeAction, 0, len(elems))
	for _, el := range elems {
		if act, ok := parseCodeAction(el); ok {
			out = append(out, act)
		}
	}
	return out
}

// parseCodeAction reads one element of the response union.
//
// The `command` field's JSON TYPE is the discriminator: a string means the
// element is itself a bare Command, an object (or absence) means it is a
// CodeAction literal. Nothing else in the two shapes distinguishes them —
// both have a title, and every other CodeAction field is optional.
func parseCodeAction(raw json.RawMessage) (CodeAction, bool) {
	var probe struct {
		Title    string          `json:"title"`
		Command  json.RawMessage `json:"command"`
		Kind     string          `json:"kind"`
		Disabled *struct {
			Reason string `json:"reason"`
		} `json:"disabled"`
		IsPreferred bool            `json:"isPreferred"`
		Edit        json.RawMessage `json:"edit"`
	}
	if err := json.Unmarshal(raw, &probe); err != nil {
		return CodeAction{}, false
	}
	if probe.Title == "" {
		// A title is the one thing a picker row cannot do without, and the
		// spec makes it required — an element without one is unusable
		// rather than merely unfamiliar.
		return CodeAction{}, false
	}
	if probe.Disabled != nil {
		return CodeAction{}, false
	}

	// The bare-Command shape: the whole element IS the command.
	var name string
	if err := json.Unmarshal(probe.Command, &name); err == nil && name != "" {
		var cmd Command
		if err := json.Unmarshal(raw, &cmd); err != nil {
			return CodeAction{}, false
		}
		return CodeAction{Title: cmd.Title, Command: &cmd}, true
	}

	act := CodeAction{
		Title:       probe.Title,
		Kind:        probe.Kind,
		IsPreferred: probe.IsPreferred,
		Edit:        ParseWorkspaceEdit(probe.Edit),
	}
	if len(probe.Command) > 0 && string(probe.Command) != "null" {
		var cmd Command
		if err := json.Unmarshal(probe.Command, &cmd); err == nil && cmd.Command != "" {
			act.Command = &cmd
		}
	}
	if act.Edit == nil && act.Command == nil {
		// Nothing to apply and nothing to run. This is what an action
		// awaiting codeAction/resolve looks like, and ced deliberately does
		// not declare resolveSupport (see Initialize) — so a server sending
		// one anyway has offered a row that could only do nothing.
		return CodeAction{}, false
	}
	return act, true
}

// ParseApplyEditRequest reads the params of a server-initiated
// workspace/applyEdit. The label is the server's own name for the change
// ("Extract function"), which becomes the editor's label for the whole
// gesture — the confirmation title, the flash, and the undo row.
func ParseApplyEditRequest(raw json.RawMessage) (label string, edit *WorkspaceEdit) {
	if len(raw) == 0 {
		return "", nil
	}
	var p struct {
		Label string          `json:"label"`
		Edit  json.RawMessage `json:"edit"`
	}
	if err := json.Unmarshal(raw, &p); err != nil {
		return "", nil
	}
	return p.Label, ParseWorkspaceEdit(p.Edit)
}
