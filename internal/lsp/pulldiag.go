// =============================================================================
// File: internal/lsp/pulldiag.go
// Author: Rohan Allison <rohanthewiz@gmail.com>
// Created: 2026-11-17
// Copyright: 2026 Rohan Allison. All rights reserved.
// =============================================================================

// pulldiag.go holds the two requests a PULL-model server needs that a
// push-model one doesn't (N-101): asking for a document's diagnostics,
// and handing the server its settings after the handshake.
//
// TypeScript 7's own server (`tsc --lsp --stdio`) is the case. Probed on
// 2026-11-06 with ced's handshake: it publishes ONE empty
// publishDiagnostics on open and nothing after an edit or a save, while
// `textDocument/diagnostic` answers with the real list; and its inlay
// hints ignore initializationOptions but follow VS Code-shaped settings
// pushed with workspace/didChangeConfiguration.
//
// The client capability `textDocument.diagnostic` is deliberately NOT
// declared in initialize. tsc answers the request without it, and a
// server that speaks both models may take the declaration as "this
// client pulls" and stop pushing — which every other server ced runs
// relies on. Pulling stays a per-server choice the app layer makes.

package lsp

import "encoding/json"

// documentDiagnosticReport is the response to textDocument/diagnostic.
// kind is "full" (items is the whole list) or "unchanged" (only when the
// request carried a previousResultId, which ced never sends).
type documentDiagnosticReport struct {
	Kind  string       `json:"kind"`
	Items []Diagnostic `json:"items"`
}

// PullDiagnostics asks the server for path's current diagnostics. An
// empty, non-nil answer means "no problems" and retracts, exactly as an
// empty publishDiagnostics does. Times out like any Call.
func (c *Client) PullDiagnostics(path string) ([]Diagnostic, error) {
	params := map[string]any{"textDocument": TextDocumentIdentifier{URI: PathToURI(path)}}
	var raw json.RawMessage
	if err := c.Call("textDocument/diagnostic", params, &raw); err != nil {
		return nil, err
	}
	var rep documentDiagnosticReport
	if len(raw) > 0 && string(raw) != "null" {
		if err := json.Unmarshal(raw, &rep); err != nil {
			return nil, err
		}
	}
	if rep.Items == nil {
		rep.Items = []Diagnostic{}
	}
	return rep.Items, nil
}

// DidChangeConfiguration pushes settings to the server
// (workspace/didChangeConfiguration). settings is the whole settings
// object, keyed by section ("typescript", "javascript", …).
func (c *Client) DidChangeConfiguration(settings any) error {
	return c.Notify("workspace/didChangeConfiguration", map[string]any{"settings": settings})
}
