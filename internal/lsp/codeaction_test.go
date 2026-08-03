// =============================================================================
// File: internal/lsp/codeaction_test.go
// Author: Rohan Allison <rohanthewiz@gmail.com>
// Created: 2026-08-03
// Copyright: 2026 Rohan Allison. All rights reserved.
// =============================================================================

package lsp

import (
	"encoding/json"
	"testing"
)

// TestParseCodeActions_LiteralShape pins the modern answer: a CodeAction
// object carrying its own ready-made edit. This is the shape ced asks for by
// declaring codeActionLiteralSupport, and the one where a picked row can
// apply immediately without a second round trip.
func TestParseCodeActions_LiteralShape(t *testing.T) {
	raw := json.RawMessage(`[
		{"title":"Organize imports","kind":"source.organizeImports","isPreferred":true,
		 "edit":{"documentChanges":[
			{"textDocument":{"uri":"file:///p/a.go","version":2},"edits":[
				{"range":{"start":{"line":1,"character":0},"end":{"line":2,"character":0}},"newText":""}]}]}}
	]`)
	acts := ParseCodeActions(raw)
	if len(acts) != 1 {
		t.Fatalf("actions = %d, want 1", len(acts))
	}
	a := acts[0]
	if a.Title != "Organize imports" || a.Kind != "source.organizeImports" || !a.IsPreferred {
		t.Errorf("action = %+v, want the literal's own fields", a)
	}
	if a.Edit == nil || len(a.Edit.Documents) != 1 {
		t.Fatalf("edit = %+v, want one document", a.Edit)
	}
	if a.Command != nil {
		t.Errorf("command = %+v, want nil — this action carries an edit", a.Command)
	}
}

// TestParseCodeActions_BareCommandShape pins the LSP 3.8 answer, where the
// element IS a Command. The discriminator is the JSON TYPE of the `command`
// field — a string here, an object on a literal — because a bare Command
// decodes cleanly as a CodeAction with everything zeroed. An error-based
// sniff would "succeed" and produce a row that does nothing at all.
func TestParseCodeActions_BareCommandShape(t *testing.T) {
	raw := json.RawMessage(`[
		{"title":"Upgrade dependency","command":"gopls.upgrade_dependency",
		 "arguments":[{"URIArg":{"URI":"file:///p"},"GoCmdArgs":["-u"]}]}
	]`)
	acts := ParseCodeActions(raw)
	if len(acts) != 1 {
		t.Fatalf("actions = %d, want 1", len(acts))
	}
	a := acts[0]
	if a.Title != "Upgrade dependency" {
		t.Errorf("title = %q", a.Title)
	}
	if a.Edit != nil {
		t.Errorf("edit = %+v, want nil — a bare Command carries none", a.Edit)
	}
	if a.Command == nil || a.Command.Command != "gopls.upgrade_dependency" {
		t.Fatalf("command = %+v, want the command name", a.Command)
	}
	if len(a.Command.Arguments) != 1 {
		t.Fatalf("arguments = %d, want 1", len(a.Command.Arguments))
	}
	// The arguments are the server's private payload and must survive
	// byte-for-byte — they go straight back out in executeCommand.
	if got := string(a.Command.Arguments[0]); got != `{"URIArg":{"URI":"file:///p"},"GoCmdArgs":["-u"]}` {
		t.Errorf("argument = %s, want the raw object verbatim", got)
	}
}

// TestParseCodeActions_LiteralWithCommand pins that edit and command are not
// exclusive: the spec allows both, meaning "apply the edit, then run the
// command", and gopls uses it for fixes that also need a follow-up.
func TestParseCodeActions_LiteralWithCommand(t *testing.T) {
	raw := json.RawMessage(`[
		{"title":"Fill struct","kind":"refactor.rewrite",
		 "edit":{"changes":{"file:///p/a.go":[
			{"range":{"start":{"line":0,"character":0},"end":{"line":0,"character":1}},"newText":"x"}]}},
		 "command":{"title":"Reload","command":"gopls.reload","arguments":[1]}}
	]`)
	acts := ParseCodeActions(raw)
	if len(acts) != 1 {
		t.Fatalf("actions = %d, want 1", len(acts))
	}
	if acts[0].Edit == nil {
		t.Error("edit dropped — the literal carried one")
	}
	if acts[0].Command == nil || acts[0].Command.Command != "gopls.reload" {
		t.Errorf("command = %+v, want gopls.reload alongside the edit", acts[0].Command)
	}
}

// TestParseCodeActions_DisabledDropped pins the one thing this parser throws
// away on purpose. ced's surface for choosing an action is the fuzzy picker,
// in which every row is a verb that runs; a row answering Enter with "you
// can't do that here" is worse than a row that was never offered.
func TestParseCodeActions_DisabledDropped(t *testing.T) {
	raw := json.RawMessage(`[
		{"title":"Extract function","kind":"refactor.extract",
		 "disabled":{"reason":"selection is not a statement list"},
		 "edit":{"changes":{}}},
		{"title":"Organize imports","kind":"source.organizeImports",
		 "edit":{"changes":{"file:///p/a.go":[
			{"range":{"start":{"line":0,"character":0},"end":{"line":0,"character":0}},"newText":"x"}]}}}
	]`)
	acts := ParseCodeActions(raw)
	if len(acts) != 1 || acts[0].Title != "Organize imports" {
		t.Fatalf("actions = %+v, want only the enabled one", acts)
	}
}

// TestParseCodeActions_EmptyActionDropped pins the shape an unresolved
// action arrives in — no edit, no command, waiting for codeAction/resolve.
// ced deliberately doesn't declare resolveSupport, so a server sending one
// anyway has offered a row that could only ever do nothing.
func TestParseCodeActions_EmptyActionDropped(t *testing.T) {
	raw := json.RawMessage(`[
		{"title":"Extract to variable","kind":"refactor.extract","data":{"id":7}}
	]`)
	if acts := ParseCodeActions(raw); len(acts) != 0 {
		t.Errorf("actions = %+v, want none — nothing to apply and nothing to run", acts)
	}
}

// TestParseCodeActions_Degenerate pins that the parser answers "nothing"
// rather than panicking on the payloads a server may legitimately send, and
// that a titleless element is unusable rather than merely unfamiliar.
func TestParseCodeActions_Degenerate(t *testing.T) {
	for name, raw := range map[string]string{
		"null":       `null`,
		"empty":      `[]`,
		"not array":  `{"title":"x"}`,
		"no title":   `[{"kind":"quickfix","edit":{"changes":{}}}]`,
		"junk entry": `[7]`,
	} {
		if acts := ParseCodeActions(json.RawMessage(raw)); len(acts) != 0 {
			t.Errorf("%s: actions = %+v, want none", name, acts)
		}
	}
}

// TestCodeActionKindFamily pins what a picker row shows beside a title: the
// family, not the whole dotted kind. The title already names the specific
// transformation, so "refactor.extract" after "Extract function" is noise —
// the family is the axis a user filters on.
func TestCodeActionKindFamily(t *testing.T) {
	cases := map[string]string{
		"source.organizeImports": "source",
		"refactor.extract":       "refactor",
		"quickfix":               "quickfix",
		"":                       "",
	}
	for kind, want := range cases {
		if got := (CodeAction{Kind: kind}).KindFamily(); got != want {
			t.Errorf("KindFamily(%q) = %q, want %q", kind, got, want)
		}
	}
}

// TestParseApplyEditRequest pins the server-initiated route's payload: the
// label becomes the editor's name for the whole gesture (confirmation title,
// flash, undo row, receipt heading), and the edit goes through the one
// parser both routes share.
func TestParseApplyEditRequest(t *testing.T) {
	raw := json.RawMessage(`{"label":"Extract function","edit":{"documentChanges":[
		{"textDocument":{"uri":"file:///p/a.go","version":null},"edits":[
			{"range":{"start":{"line":3,"character":0},"end":{"line":3,"character":4}},"newText":"f()"}]}]}}`)
	label, edit := ParseApplyEditRequest(raw)
	if label != "Extract function" {
		t.Errorf("label = %q, want the server's own name for the change", label)
	}
	if edit == nil || len(edit.Documents) != 1 || edit.Documents[0].Path != "/p/a.go" {
		t.Fatalf("edit = %+v, want one document for a.go", edit)
	}
	if edit.Documents[0].Version != nil {
		t.Error("a null version must stay nil — absent and zero are different claims")
	}
}

// TestParseApplyEditRequest_Degenerate pins that a malformed or edit-less
// request produces a nil edit rather than an error, which the app layer
// already reports as "nothing to change".
func TestParseApplyEditRequest_Degenerate(t *testing.T) {
	for name, raw := range map[string]string{
		"empty":     ``,
		"junk":      `not json`,
		"no edit":   `{"label":"x"}`,
		"null edit": `{"label":"x","edit":null}`,
	} {
		label, edit := ParseApplyEditRequest(json.RawMessage(raw))
		if edit != nil {
			t.Errorf("%s: edit = %+v, want nil", name, edit)
		}
		if name == "no edit" && label != "x" {
			t.Errorf("%s: label = %q, want it kept even with no edit", name, label)
		}
	}
}
