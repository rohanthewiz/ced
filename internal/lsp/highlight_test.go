// =============================================================================
// File: internal/lsp/highlight_test.go
// Author: Rohan Allison <rohanthewiz@gmail.com>
// Created: 2026-09-21
// Copyright: 2026 Rohan Allison. All rights reserved.
// =============================================================================

package lsp

import (
	"fmt"
	"testing"
)

// TestDocumentHighlights pins the request's method and the decode of the
// read/write kind, including a use the server left unclassified.
func TestDocumentHighlights(t *testing.T) {
	c, srv, done := pipeClient(t, nil, nil)
	defer done()

	ch := make(chan []DocumentHighlight, 1)
	go func() {
		hl, _ := c.DocumentHighlights("/p/x.go", Position{Line: 1, Character: 2})
		ch <- hl
	}()
	m := srv.read(t)
	if m.Method != "textDocument/documentHighlight" {
		t.Fatalf("method = %q", m.Method)
	}
	srv.write(t, fmt.Sprintf(`{"jsonrpc":"2.0","id":%d,"result":[{"range":{"start":{"line":1,"character":0},"end":{"line":1,"character":3}},"kind":3},{"range":{"start":{"line":4,"character":2},"end":{"line":4,"character":5}}}]}`, *m.ID))
	hl := <-ch
	if len(hl) != 2 || hl[0].Kind != HighlightWrite || hl[1].Kind != 0 || hl[1].Range.Start.Line != 4 {
		t.Errorf("highlights = %+v", hl)
	}
}
