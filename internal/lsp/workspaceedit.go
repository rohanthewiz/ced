// =============================================================================
// File: internal/lsp/workspaceedit.go
// Author: Rohan Allison <rohanthewiz@gmail.com>
// Created: 2026-08-03
// Copyright: 2026 Rohan Allison. All rights reserved.
// =============================================================================

// workspaceedit.go is the wire half of the workspace-edit primitive: the
// structs a server sends when it wants the editor to change files, and the
// one parser that collapses them into a single normal form.
//
// A WorkspaceEdit is the payload of textDocument/rename, of a code action,
// and of the workspace/applyEdit request a server can send unprompted, so
// it is the shared currency of every "the server rewrites your code" verb.
// It arrives in TWO shapes:
//
//	{"changes": {"file:///a.go": [TextEdit, …], …}}          — legacy
//	{"documentChanges": [TextDocumentEdit | CreateFile | …]} — modern
//
// Collapsing them HERE rather than in the app layer is this package's
// standing rule (see ParseDocumentSymbols and ParseSignatureHelp): the
// consumer should never learn that the protocol has two answers to one
// question, because a consumer that knows will eventually handle only the
// shape its own server happens to send.
//
// The sniff keys on a DISCRIMINATING FIELD, never on a failed unmarshal —
// the same trap ParseDocumentSymbols documents, and it bites twice as hard
// here. `{"changes":…}` and `{"documentChanges":…}` both decode cleanly
// into a struct carrying both fields, and INSIDE documentChanges a
// CreateFile and a TextDocumentEdit both decode into either Go struct with
// every missing field left zero. An error-based sniff would therefore
// "succeed" and turn a request to create a file into a document with no
// edits — a silent no-op where the user asked for something.

package lsp

import (
	"encoding/json"
	"sort"
)

// TextEdit is one replacement inside one document. The range is in the
// protocol's coordinates: zero-based lines, UTF-16 character offsets.
// Converting to the editor's rune columns is the caller's job, and has to
// happen against the text that will actually be edited.
type TextEdit struct {
	Range   Range  `json:"range"`
	NewText string `json:"newText"`
}

// ResourceOpKind names the three filesystem operations documentChanges may
// carry alongside its text edits.
type ResourceOpKind string

const (
	ResourceCreate ResourceOpKind = "create"
	ResourceRename ResourceOpKind = "rename"
	ResourceDelete ResourceOpKind = "delete"
)

// ResourceOp is one create / rename / delete a server asked for.
//
// It is PARSED even though nothing applies one yet, and that is the whole
// point of the type. A client that silently drops half of a package rename
// — rewriting every identifier but never moving the file — leaves a tree
// that no longer builds and never says why. Parsing them is what lets the
// app layer refuse the edit BY NAME instead.
type ResourceOp struct {
	Kind ResourceOpKind
	// Path is the absolute filesystem path, or "" for a URI that names no
	// file. URI is kept verbatim either way so a refusal can quote what it
	// actually saw.
	Path    string
	NewPath string // rename only
	URI     string
	NewURI  string // rename only
}

// DocumentEdit is one document's share of a workspace edit, in the
// editor-facing normal form.
//
// Version is the server's optional claim about which revision of the
// document its ranges were measured against. It is a POINTER because
// absent and zero are different answers: a server reads unopened files off
// disk and sends null for them, while 0 is a real version number. Losing
// that distinction would turn "no claim" into "revision zero" and make
// every staleness check on an unopened file fail.
type DocumentEdit struct {
	Path    string
	URI     string
	Version *int
	// Edits keeps the server's array order. The spec makes it meaningful
	// for edits that share a position, and the multi-edit primitive's
	// stable sort is built to preserve it.
	Edits []TextEdit
}

// WorkspaceEdit is what both wire shapes collapse into.
type WorkspaceEdit struct {
	Documents []DocumentEdit
	Resources []ResourceOp
}

// IsEmpty reports whether the edit asks for nothing at all — which is what
// a server answers when a rename is legal but changes nothing, and is a
// different thing from an error.
func (w *WorkspaceEdit) IsEmpty() bool {
	if w == nil {
		return true
	}
	if len(w.Resources) > 0 {
		return false
	}
	// A document entry with an empty edit list is something servers do send
	// (a file they considered and left alone), so the question is whether
	// any EDIT exists, not whether any document does.
	for _, d := range w.Documents {
		if len(d.Edits) > 0 {
			return false
		}
	}
	return true
}

// TotalEdits counts every text edit across every document — what the
// confirmation prompt and the summary flash report.
func (w *WorkspaceEdit) TotalEdits() int {
	if w == nil {
		return 0
	}
	n := 0
	for _, d := range w.Documents {
		n += len(d.Edits)
	}
	return n
}

// ParseWorkspaceEdit normalises either wire shape into one WorkspaceEdit,
// or nil when the payload is absent, null, or unusable.
//
// documentChanges WINS when both are present — spec precedence, and the
// right call regardless: it is the shape that carries document versions
// and resource operations, so preferring it never loses information while
// preferring changes always could.
func ParseWorkspaceEdit(raw json.RawMessage) *WorkspaceEdit {
	if len(raw) == 0 || string(raw) == "null" {
		return nil
	}
	var probe struct {
		Changes         json.RawMessage `json:"changes"`
		DocumentChanges json.RawMessage `json:"documentChanges"`
	}
	if err := json.Unmarshal(raw, &probe); err != nil {
		return nil
	}

	if len(probe.DocumentChanges) > 0 && string(probe.DocumentChanges) != "null" {
		return parseDocumentChanges(probe.DocumentChanges)
	}
	if len(probe.Changes) > 0 && string(probe.Changes) != "null" {
		return parseChangesMap(probe.Changes)
	}
	return nil
}

// parseChangesMap reads the legacy {uri: TextEdit[]} shape.
//
// The result is SORTED BY PATH, and that is not cosmetic: a Go map's
// iteration order is deliberately randomised, so an unsorted walk would
// let two runs of the same rename produce two different orderings — a
// different confirmation prompt, a different receipt list, and a different
// order of disk writes each time. Order is the one thing a map shape
// cannot tell us, so we impose a stable one.
func parseChangesMap(raw json.RawMessage) *WorkspaceEdit {
	var changes map[string][]TextEdit
	if err := json.Unmarshal(raw, &changes); err != nil {
		return nil
	}
	out := &WorkspaceEdit{}
	for uri, edits := range changes {
		out.Documents = append(out.Documents, DocumentEdit{
			Path:  URIToPath(uri),
			URI:   uri,
			Edits: edits,
		})
	}
	sort.Slice(out.Documents, func(i, j int) bool {
		if out.Documents[i].Path != out.Documents[j].Path {
			return out.Documents[i].Path < out.Documents[j].Path
		}
		return out.Documents[i].URI < out.Documents[j].URI
	})
	return out
}

// parseDocumentChanges reads the modern array shape, whose elements are a
// union of four types.
//
// Element order is PRESERVED — unlike the map shape, the spec says this
// array's order is meaningful, so imposing our own would be discarding
// information the server took care to send.
//
// The discriminator is the spec's own: exactly the three resource
// operations carry a "kind", and only a TextDocumentEdit carries a
// "textDocument". An element with neither is something a later protocol
// revision added, and is skipped rather than guessed at.
func parseDocumentChanges(raw json.RawMessage) *WorkspaceEdit {
	var elems []json.RawMessage
	if err := json.Unmarshal(raw, &elems); err != nil {
		return nil
	}
	out := &WorkspaceEdit{}
	for _, el := range elems {
		var probe struct {
			Kind         string          `json:"kind"`
			TextDocument json.RawMessage `json:"textDocument"`
		}
		if err := json.Unmarshal(el, &probe); err != nil {
			continue
		}
		switch {
		case probe.Kind != "":
			if op, ok := parseResourceOp(probe.Kind, el); ok {
				out.Resources = append(out.Resources, op)
			}
		case len(probe.TextDocument) > 0:
			if de, ok := parseTextDocumentEdit(el); ok {
				out.Documents = append(out.Documents, de)
			}
		}
	}
	return out
}

// parseTextDocumentEdit reads one TextDocumentEdit element.
//
// AnnotatedTextEdit — a TextEdit carrying an extra annotationId — decodes
// into TextEdit unchanged, because the annotation is a LABEL for a change
// the user might want to review, not part of the edit. Ignoring the label
// costs nothing; failing to parse the element would cost the edit.
func parseTextDocumentEdit(raw json.RawMessage) (DocumentEdit, bool) {
	var tde struct {
		TextDocument struct {
			URI     string `json:"uri"`
			Version *int   `json:"version"`
		} `json:"textDocument"`
		Edits []TextEdit `json:"edits"`
	}
	if err := json.Unmarshal(raw, &tde); err != nil {
		return DocumentEdit{}, false
	}
	return DocumentEdit{
		Path:    URIToPath(tde.TextDocument.URI),
		URI:     tde.TextDocument.URI,
		Version: tde.TextDocument.Version,
		Edits:   tde.Edits,
	}, true
}

// parseResourceOp reads one create / rename / delete element. An unknown
// kind is dropped: it names an operation this client could not describe in
// a refusal, let alone perform.
func parseResourceOp(kind string, raw json.RawMessage) (ResourceOp, bool) {
	var op struct {
		URI    string `json:"uri"`
		OldURI string `json:"oldUri"`
		NewURI string `json:"newUri"`
	}
	if err := json.Unmarshal(raw, &op); err != nil {
		return ResourceOp{}, false
	}
	switch ResourceOpKind(kind) {
	case ResourceCreate, ResourceDelete:
		return ResourceOp{
			Kind: ResourceOpKind(kind),
			Path: URIToPath(op.URI),
			URI:  op.URI,
		}, true
	case ResourceRename:
		return ResourceOp{
			Kind:    ResourceRename,
			Path:    URIToPath(op.OldURI),
			NewPath: URIToPath(op.NewURI),
			URI:     op.OldURI,
			NewURI:  op.NewURI,
		}, true
	}
	return ResourceOp{}, false
}
