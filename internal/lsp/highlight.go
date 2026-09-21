// =============================================================================
// File: internal/lsp/highlight.go
// Author: Rohan Allison <rohanthewiz@gmail.com>
// Created: 2026-09-21
// Copyright: 2026 Rohan Allison. All rights reserved.
// =============================================================================

// highlight.go is textDocument/documentHighlight — every use of the
// symbol at a position WITHIN ONE DOCUMENT, each tagged as a read or a
// write. It is references scoped to the file and answered from the
// file's own package, which is why it keeps the default 5s budget where
// references needs thirty.

package lsp

import "encoding/json"

// DocumentHighlightKind values. Text (1) is what a server sends when it
// cannot classify a use; it is treated as a read.
const (
	HighlightText  = 1
	HighlightRead  = 2
	HighlightWrite = 3
)

// DocumentHighlight is one use of the symbol.
type DocumentHighlight struct {
	Range Range `json:"range"`
	Kind  int   `json:"kind,omitempty"`
}

// DocumentHighlights asks for the uses of the symbol at pos in path.
// nil, nil means the position is not on a symbol.
func (c *Client) DocumentHighlights(path string, pos Position) ([]DocumentHighlight, error) {
	params := TextDocumentPositionParams{
		TextDocument: TextDocumentIdentifier{URI: PathToURI(path)},
		Position:     pos,
	}
	var raw json.RawMessage
	if err := c.Call("textDocument/documentHighlight", params, &raw); err != nil {
		return nil, err
	}
	if len(raw) == 0 || string(raw) == "null" {
		return nil, nil
	}
	var out []DocumentHighlight
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, err
	}
	return out, nil
}
