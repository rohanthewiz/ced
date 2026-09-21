// =============================================================================
// File: internal/lsp/inlayhint_test.go
// Author: Rohan Allison <rohanthewiz@gmail.com>
// Created: 2026-09-21
// Copyright: 2026 Rohan Allison. All rights reserved.
// =============================================================================

package lsp

import (
	"encoding/json"
	"fmt"
	"testing"
)

// TestParseInlayHints pins the label union: a plain string and an array
// of parts both come out as joined text, and an empty label is dropped.
func TestParseInlayHints(t *testing.T) {
	raw := json.RawMessage(`[
	 {"position":{"line":1,"character":4},"label":": int","kind":1},
	 {"position":{"line":2,"character":9},"label":[{"value":"level"},{"value":":"}],"kind":2},
	 {"position":{"line":3,"character":0},"label":""}
	]`)
	got := ParseInlayHints(raw)
	if len(got) != 2 {
		t.Fatalf("got %d hints, want 2: %+v", len(got), got)
	}
	if got[0].Label != ": int" || got[0].Kind != InlayType || got[0].Pos.Line != 1 {
		t.Errorf("first = %+v", got[0])
	}
	if got[1].Label != "level:" || got[1].Kind != InlayParameter {
		t.Errorf("parts label = %+v", got[1])
	}
}

// TestInlayHints_WireFormat pins the request: method, document, range.
func TestInlayHints_WireFormat(t *testing.T) {
	c, srv, done := pipeClient(t, nil, nil)
	defer done()

	ch := make(chan []InlayHint, 1)
	go func() {
		h, _ := c.InlayHints("/p/x.go", Range{End: Position{Line: 40}})
		ch <- h
	}()
	m := srv.read(t)
	if m.Method != "textDocument/inlayHint" {
		t.Fatalf("method = %q", m.Method)
	}
	var p struct {
		TextDocument TextDocumentIdentifier `json:"textDocument"`
		Range        Range                  `json:"range"`
	}
	if err := json.Unmarshal(m.Params, &p); err != nil || p.TextDocument.URI != "file:///p/x.go" || p.Range.End.Line != 40 {
		t.Errorf("params = %s", m.Params)
	}
	srv.write(t, fmt.Sprintf(`{"jsonrpc":"2.0","id":%d,"result":null}`, *m.ID))
	if h := <-ch; h != nil {
		t.Errorf("null should be no hints, got %+v", h)
	}
}

// TestInitializeWithOptions_SendsThem pins the options' place on the
// wire — gopls ships with every hint switched off, and this field is the
// only way to switch them on.
func TestInitializeWithOptions_SendsThem(t *testing.T) {
	c, srv, done := pipeClient(t, nil, nil)
	defer done()

	go func() { _ = c.InitializeWithOptions("/p", map[string]any{"hints": map[string]any{"parameterNames": true}}) }()
	m := srv.read(t)
	var p struct {
		Opts struct {
			Hints map[string]bool `json:"hints"`
		} `json:"initializationOptions"`
	}
	if err := json.Unmarshal(m.Params, &p); err != nil || !p.Opts.Hints["parameterNames"] {
		t.Errorf("initializationOptions missing: %s", m.Params)
	}
	srv.write(t, fmt.Sprintf(`{"jsonrpc":"2.0","id":%d,"result":{"capabilities":{}}}`, *m.ID))
	srv.read(t) // the `initialized` notification
}
