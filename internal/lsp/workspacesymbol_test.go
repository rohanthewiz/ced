// =============================================================================
// File: internal/lsp/workspacesymbol_test.go
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

// TestParseWorkspaceSymbols pins the normal form: server order kept,
// container and start position carried, and hits the editor could not
// open (a non-file URI, a nameless entry) dropped.
func TestParseWorkspaceSymbols(t *testing.T) {
	raw := json.RawMessage(`[
	 {"name":"Zed","kind":12,"containerName":"pkg","location":{"uri":"file:///p/z.go","range":{"start":{"line":9,"character":5},"end":{"line":9,"character":8}}}},
	 {"name":"Alpha","kind":5,"location":{"uri":"file:///p/a.go"}},
	 {"name":"Jar","kind":5,"location":{"uri":"jdt://contents/x.class","range":{"start":{"line":1,"character":0},"end":{"line":1,"character":1}}}},
	 {"name":"","kind":5,"location":{"uri":"file:///p/b.go"}}
	]`)
	got := ParseWorkspaceSymbols(raw)
	if len(got) != 2 {
		t.Fatalf("got %d symbols, want 2: %+v", len(got), got)
	}
	if got[0].Name != "Zed" || got[0].Container != "pkg" || got[0].Path != "/p/z.go" ||
		got[0].Pos != (Position{Line: 9, Character: 5}) {
		t.Errorf("first = %+v", got[0])
	}
	// A range-less location still lands in the right file.
	if got[1].Name != "Alpha" || got[1].Pos != (Position{}) {
		t.Errorf("second = %+v", got[1])
	}
	if ParseWorkspaceSymbols(json.RawMessage(`null`)) != nil {
		t.Error("null should parse to nothing")
	}
}

// TestWorkspaceSymbols_WireFormat pins the request: the method and the
// query string are the whole of it.
func TestWorkspaceSymbols_WireFormat(t *testing.T) {
	c, srv, done := pipeClient(t, nil, nil)
	defer done()

	ch := make(chan []WorkspaceSymbol, 1)
	go func() {
		syms, _ := c.WorkspaceSymbols("handleKey")
		ch <- syms
	}()
	m := srv.read(t)
	if m.Method != "workspace/symbol" {
		t.Fatalf("method = %q", m.Method)
	}
	var p struct {
		Query string `json:"query"`
	}
	if err := json.Unmarshal(m.Params, &p); err != nil || p.Query != "handleKey" {
		t.Errorf("params = %s", m.Params)
	}
	srv.write(t, fmt.Sprintf(`{"jsonrpc":"2.0","id":%d,"result":[{"name":"handleKey","kind":6,"location":{"uri":"file:///p/app.go","range":{"start":{"line":3,"character":1},"end":{"line":3,"character":2}}}}]}`, *m.ID))
	if syms := <-ch; len(syms) != 1 || syms[0].Path != "/p/app.go" {
		t.Errorf("symbols = %+v", syms)
	}
}
