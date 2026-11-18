// =============================================================================
// File: internal/lsp/pulldiag_test.go
// Author: Rohan Allison <rohanthewiz@gmail.com>
// Created: 2026-11-17
// Copyright: 2026 Rohan Allison. All rights reserved.
// =============================================================================

package lsp

import (
	"encoding/json"
	"strings"
	"testing"
)

// TestPullDiagnostics_ReadsAFullReport pins the request and the
// response shape tsc --lsp answers with: the document's URI out, a
// "full" report's items back.
func TestPullDiagnostics_ReadsAFullReport(t *testing.T) {
	c, srv, cleanup := pipeClient(t, nil, nil)
	defer cleanup()
	type result struct {
		diags []Diagnostic
		err   error
	}
	got := make(chan result, 1)
	go func() {
		d, err := c.PullDiagnostics("/p/a.ts")
		got <- result{d, err}
	}()
	req := srv.read(t)
	if req.Method != "textDocument/diagnostic" || !strings.Contains(string(req.Params), `"file:///p/a.ts"`) {
		t.Fatalf("request = %s %s", req.Method, req.Params)
	}
	srv.write(t, `{"jsonrpc":"2.0","id":`+jsonID(req)+`,"result":{"kind":"full","items":[
		{"range":{"start":{"line":2,"character":4},"end":{"line":2,"character":9}},"severity":1,"message":"Type 'string' is not assignable"}]}}`)
	r := <-got
	if r.err != nil || len(r.diags) != 1 || r.diags[0].Range.Start.Line != 2 {
		t.Fatalf("got %+v, %v", r.diags, r.err)
	}
}

// TestPullDiagnostics_EmptyReportRetracts: no items (or a null result)
// is an empty, non-nil list — the "fixed" answer that clears the marks.
func TestPullDiagnostics_EmptyReportRetracts(t *testing.T) {
	for _, body := range []string{`{"kind":"full","items":[]}`, `null`} {
		c, srv, cleanup := pipeClient(t, nil, nil)
		done := make(chan []Diagnostic, 1)
		go func() {
			d, _ := c.PullDiagnostics("/p/a.ts")
			done <- d
		}()
		req := srv.read(t)
		srv.write(t, `{"jsonrpc":"2.0","id":`+jsonID(req)+`,"result":`+body+`}`)
		if d := <-done; d == nil || len(d) != 0 {
			t.Errorf("%s: got %#v, want an empty non-nil list", body, d)
		}
		cleanup()
	}
}

// TestDidChangeConfiguration_WrapsTheSettings pins the notification's
// envelope: the settings object under "settings".
func TestDidChangeConfiguration_WrapsTheSettings(t *testing.T) {
	c, srv, cleanup := pipeClient(t, nil, nil)
	defer cleanup()
	go func() { _ = c.DidChangeConfiguration(map[string]any{"typescript": map[string]any{"x": 1}}) }()
	m := srv.read(t)
	if m.Method != "workspace/didChangeConfiguration" || string(m.Params) != `{"settings":{"typescript":{"x":1}}}` {
		t.Errorf("got %s %s", m.Method, m.Params)
	}
}

// jsonID renders a request's id for a scripted response.
func jsonID(m *message) string {
	b, _ := json.Marshal(m.ID)
	return string(b)
}
