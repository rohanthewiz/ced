// =============================================================================
// File: internal/lsp/callhierarchy_test.go
// Author: Rohan Allison <rohanthewiz@gmail.com>
// Created: 2026-09-21
// Copyright: 2026 Rohan Allison. All rights reserved.
// =============================================================================

package lsp

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

// TestParseIncomingCalls pins the flattening: one Location per call
// RANGE in the caller's file, and a caller with no ranges still yields
// a row at its own name.
func TestParseIncomingCalls(t *testing.T) {
	raw := json.RawMessage(`[
	 {"from":{"name":"run","uri":"file:///p/a.go","selectionRange":{"start":{"line":1,"character":5},"end":{"line":1,"character":8}}},
	  "fromRanges":[{"start":{"line":3,"character":2},"end":{"line":3,"character":6}},{"start":{"line":9,"character":2},"end":{"line":9,"character":6}}]},
	 {"from":{"name":"init","uri":"file:///p/b.go","selectionRange":{"start":{"line":7,"character":5},"end":{"line":7,"character":9}}},"fromRanges":[]}
	]`)
	got := ParseIncomingCalls(raw)
	if len(got) != 3 {
		t.Fatalf("got %d call sites, want 3: %+v", len(got), got)
	}
	if got[1].URI != "file:///p/a.go" || got[1].Range.Start.Line != 9 {
		t.Errorf("second site = %+v", got[1])
	}
	if got[2].URI != "file:///p/b.go" || got[2].Range.Start.Line != 7 {
		t.Errorf("range-less caller = %+v, want its own name's position", got[2])
	}
}

// TestIncomingCalls_EchoesTheItemVerbatim pins the two-step exchange: the
// prepared item goes back to the server byte-for-byte, server-private
// `data` included — that field is how the server knows which symbol the
// second request is about.
func TestIncomingCalls_EchoesTheItemVerbatim(t *testing.T) {
	c, srv, done := pipeClient(t, nil, nil)
	defer done()

	ch := make(chan []Location, 1)
	go func() {
		locs, _ := c.IncomingCalls("/p/x.go", Position{Line: 4, Character: 6})
		ch <- locs
	}()
	m := srv.read(t)
	if m.Method != "textDocument/prepareCallHierarchy" {
		t.Fatalf("first method = %q", m.Method)
	}
	srv.write(t, fmt.Sprintf(`{"jsonrpc":"2.0","id":%d,"result":[{"name":"f","kind":12,"uri":"file:///p/x.go","data":{"secret":42}}]}`, *m.ID))

	m = srv.read(t)
	if m.Method != "callHierarchy/incomingCalls" {
		t.Fatalf("second method = %q", m.Method)
	}
	if !strings.Contains(string(m.Params), `"secret":42`) {
		t.Errorf("item not echoed verbatim: %s", m.Params)
	}
	srv.write(t, fmt.Sprintf(`{"jsonrpc":"2.0","id":%d,"result":[{"from":{"uri":"file:///p/a.go"},"fromRanges":[{"start":{"line":3,"character":2},"end":{"line":3,"character":3}}]}]}`, *m.ID))
	if locs := <-ch; len(locs) != 1 || locs[0].URI != "file:///p/a.go" {
		t.Errorf("locations = %+v", locs)
	}
}
