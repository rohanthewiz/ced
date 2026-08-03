// =============================================================================
// File: internal/lsp/workspaceedit_test.go
// Author: Rohan Allison <rohanthewiz@gmail.com>
// Created: 2026-08-03
// Copyright: 2026 Rohan Allison. All rights reserved.
// =============================================================================

package lsp

import (
	"encoding/json"
	"strings"
	"testing"
)

// TestParseWorkspaceEdit_LegacyChangesAreSortedByPath pins the ordering
// imposed on the map shape. Go randomises map iteration deliberately, so an
// unsorted walk would let two runs of the same rename produce a different
// confirmation prompt, a different receipt, and a different order of disk
// writes each time. The loop runs the parse repeatedly because a single pass
// can pass by luck.
func TestParseWorkspaceEdit_LegacyChangesAreSortedByPath(t *testing.T) {
	raw := json.RawMessage(`{"changes":{
		"file:///z/last.go":[{"range":{"start":{"line":0,"character":0},"end":{"line":0,"character":1}},"newText":"z"}],
		"file:///a/first.go":[{"range":{"start":{"line":0,"character":0},"end":{"line":0,"character":1}},"newText":"a"}],
		"file:///m/mid.go":[{"range":{"start":{"line":0,"character":0},"end":{"line":0,"character":1}},"newText":"m"}]
	}}`)
	for i := 0; i < 20; i++ {
		we := ParseWorkspaceEdit(raw)
		if we == nil || len(we.Documents) != 3 {
			t.Fatalf("parsed %v documents, want 3", we)
		}
		got := []string{we.Documents[0].Path, we.Documents[1].Path, we.Documents[2].Path}
		want := []string{"/a/first.go", "/m/mid.go", "/z/last.go"}
		for j := range want {
			if got[j] != want[j] {
				t.Fatalf("run %d: documents = %v, want %v", i, got, want)
			}
		}
	}
}

// TestParseWorkspaceEdit_DocumentChangesKeepArrayOrder pins the opposite rule
// for the modern shape: the spec makes this array's order meaningful, so
// imposing our own would discard information the server took care to send.
func TestParseWorkspaceEdit_DocumentChangesKeepArrayOrder(t *testing.T) {
	raw := json.RawMessage(`{"documentChanges":[
		{"textDocument":{"uri":"file:///z/last.go","version":7},"edits":[{"range":{"start":{"line":1,"character":2},"end":{"line":1,"character":3}},"newText":"Z"}]},
		{"textDocument":{"uri":"file:///a/first.go","version":null},"edits":[{"range":{"start":{"line":0,"character":0},"end":{"line":0,"character":1}},"newText":"A"}]}
	]}`)
	we := ParseWorkspaceEdit(raw)
	if we == nil || len(we.Documents) != 2 {
		t.Fatalf("parsed %v, want 2 documents", we)
	}
	if we.Documents[0].Path != "/z/last.go" || we.Documents[1].Path != "/a/first.go" {
		t.Errorf("order = %s, %s — array order was not preserved",
			we.Documents[0].Path, we.Documents[1].Path)
	}
	// Version is a POINTER because absent and zero are different answers: a
	// server sends null for files it read off disk, and 0 is a real revision.
	if we.Documents[0].Version == nil || *we.Documents[0].Version != 7 {
		t.Errorf("version = %v, want 7", we.Documents[0].Version)
	}
	if we.Documents[1].Version != nil {
		t.Errorf("null version = %v, want nil (no claim)", *we.Documents[1].Version)
	}
}

// TestParseWorkspaceEdit_PrefersDocumentChangesOverChanges pins spec
// precedence. It is the right call regardless of the spec: documentChanges is
// the shape carrying versions and resource operations, so preferring it never
// loses information while preferring changes could.
func TestParseWorkspaceEdit_PrefersDocumentChangesOverChanges(t *testing.T) {
	raw := json.RawMessage(`{
		"changes":{"file:///legacy.go":[{"range":{"start":{"line":0,"character":0},"end":{"line":0,"character":1}},"newText":"old"}]},
		"documentChanges":[{"textDocument":{"uri":"file:///modern.go","version":1},"edits":[{"range":{"start":{"line":0,"character":0},"end":{"line":0,"character":1}},"newText":"new"}]}]
	}`)
	we := ParseWorkspaceEdit(raw)
	if we == nil || len(we.Documents) != 1 {
		t.Fatalf("parsed %v, want 1 document", we)
	}
	if we.Documents[0].Path != "/modern.go" {
		t.Errorf("chose %s, want /modern.go", we.Documents[0].Path)
	}
}

// TestParseWorkspaceEdit_SniffsOnKindNotOnUnmarshalError is the trap
// ParseDocumentSymbols documents, one level down. A CreateFile element
// decodes cleanly into a TextDocumentEdit struct — every field simply missing
// and left zero — so an error-based sniff would "succeed" and silently turn a
// request to create a file into a document with no edits. The kind field is
// the spec's own discriminator and is what must decide.
func TestParseWorkspaceEdit_SniffsOnKindNotOnUnmarshalError(t *testing.T) {
	raw := json.RawMessage(`{"documentChanges":[
		{"kind":"create","uri":"file:///brand/new.go"},
		{"textDocument":{"uri":"file:///real.go"},"edits":[{"range":{"start":{"line":0,"character":0},"end":{"line":0,"character":1}},"newText":"x"}]}
	]}`)
	we := ParseWorkspaceEdit(raw)
	if we == nil {
		t.Fatal("parse returned nil")
	}
	if len(we.Documents) != 1 || we.Documents[0].Path != "/real.go" {
		t.Errorf("documents = %v, want only /real.go — a create op was read as a document", we.Documents)
	}
	if len(we.Resources) != 1 || we.Resources[0].Kind != ResourceCreate {
		t.Fatalf("resources = %v, want one create", we.Resources)
	}
	if we.Resources[0].Path != "/brand/new.go" {
		t.Errorf("create path = %q, want /brand/new.go", we.Resources[0].Path)
	}
}

// TestParseWorkspaceEdit_ResourceOpsSurvive pins that all three filesystem
// operations reach the caller. Nothing applies one yet, and that is exactly
// why they must be parsed: a client that silently drops half of a package
// rename leaves a tree that no longer builds and never says why. Parsing them
// is what lets the app layer refuse BY NAME.
func TestParseWorkspaceEdit_ResourceOpsSurvive(t *testing.T) {
	raw := json.RawMessage(`{"documentChanges":[
		{"kind":"create","uri":"file:///a.go"},
		{"kind":"rename","oldUri":"file:///old.go","newUri":"file:///new.go"},
		{"kind":"delete","uri":"file:///gone.go"},
		{"kind":"teleport","uri":"file:///what.go"}
	]}`)
	we := ParseWorkspaceEdit(raw)
	if we == nil || len(we.Resources) != 3 {
		t.Fatalf("resources = %v, want 3 (the unknown kind dropped)", we.Resources)
	}
	if we.Resources[1].Kind != ResourceRename ||
		we.Resources[1].Path != "/old.go" || we.Resources[1].NewPath != "/new.go" {
		t.Errorf("rename = %+v, want /old.go -> /new.go", we.Resources[1])
	}
	if we.IsEmpty() {
		t.Error("IsEmpty() = true with resource ops present")
	}
}

// TestParseWorkspaceEdit_NonFileURIKeepsURI pins that a URI naming no file
// still arrives with its text intact, so a refusal can quote what it saw
// rather than reporting an empty path.
func TestParseWorkspaceEdit_NonFileURIKeepsURI(t *testing.T) {
	raw := json.RawMessage(`{"documentChanges":[
		{"textDocument":{"uri":"jdt://contents/rt.jar"},"edits":[{"range":{"start":{"line":0,"character":0},"end":{"line":0,"character":1}},"newText":"x"}]}
	]}`)
	we := ParseWorkspaceEdit(raw)
	if we == nil || len(we.Documents) != 1 {
		t.Fatalf("parsed %v, want 1 document", we)
	}
	if we.Documents[0].Path != "" {
		t.Errorf("path = %q, want empty for a non-file URI", we.Documents[0].Path)
	}
	if !strings.HasPrefix(we.Documents[0].URI, "jdt://") {
		t.Errorf("URI = %q, want the original preserved", we.Documents[0].URI)
	}
}

// TestParseWorkspaceEdit_AnnotatedEditsStillParse pins that an
// AnnotatedTextEdit — a TextEdit carrying an extra annotationId — is read as
// the edit it is. The annotation is a review label, not part of the change;
// ignoring it costs nothing, failing to parse would cost the edit.
func TestParseWorkspaceEdit_AnnotatedEditsStillParse(t *testing.T) {
	raw := json.RawMessage(`{"documentChanges":[
		{"textDocument":{"uri":"file:///a.go","version":1},"edits":[
			{"range":{"start":{"line":0,"character":0},"end":{"line":0,"character":3}},"newText":"hi","annotationId":"rename-1"}
		]}
	]}`)
	we := ParseWorkspaceEdit(raw)
	if we == nil || we.TotalEdits() != 1 {
		t.Fatalf("TotalEdits = %d, want 1", we.TotalEdits())
	}
	if we.Documents[0].Edits[0].NewText != "hi" {
		t.Errorf("newText = %q, want hi", we.Documents[0].Edits[0].NewText)
	}
}

// TestParseWorkspaceEdit_NullAndEmpty pins the answers that mean "nothing to
// do" — which is a different thing from an error, and the caller says so
// differently.
func TestParseWorkspaceEdit_NullAndEmpty(t *testing.T) {
	for _, raw := range []string{``, `null`, `{}`, `{"changes":null}`, `{"documentChanges":null}`} {
		if we := ParseWorkspaceEdit(json.RawMessage(raw)); we != nil && !we.IsEmpty() {
			t.Errorf("ParseWorkspaceEdit(%q) = %+v, want nil or empty", raw, we)
		}
	}
	// A document entry with no edits is something servers send for a file
	// they considered and left alone. It is still "nothing to do".
	we := ParseWorkspaceEdit(json.RawMessage(`{"changes":{"file:///a.go":[]}}`))
	if we == nil || !we.IsEmpty() {
		t.Errorf("a document with zero edits: IsEmpty() = false, want true")
	}
	var nilEdit *WorkspaceEdit
	if !nilEdit.IsEmpty() || nilEdit.TotalEdits() != 0 {
		t.Error("nil WorkspaceEdit is not treated as empty")
	}
}

// TestParseWorkspaceEdit_TotalEditsCountsEverything pins the number the
// confirmation prompt and the summary flash both report.
func TestParseWorkspaceEdit_TotalEditsCountsEverything(t *testing.T) {
	raw := json.RawMessage(`{"changes":{
		"file:///a.go":[
			{"range":{"start":{"line":0,"character":0},"end":{"line":0,"character":1}},"newText":"1"},
			{"range":{"start":{"line":1,"character":0},"end":{"line":1,"character":1}},"newText":"2"}],
		"file:///b.go":[
			{"range":{"start":{"line":0,"character":0},"end":{"line":0,"character":1}},"newText":"3"}]
	}}`)
	if got := ParseWorkspaceEdit(raw).TotalEdits(); got != 3 {
		t.Errorf("TotalEdits = %d, want 3", got)
	}
}
