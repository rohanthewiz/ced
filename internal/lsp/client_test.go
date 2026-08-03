// =============================================================================
// File: internal/lsp/client_test.go
// Author: Rohan Allison <rohanthewiz@gmail.com>
// Created: 2026-07-09
// Copyright: 2026 Rohan Allison. All rights reserved.
// =============================================================================

package lsp

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// fakeServer is an in-memory language server: it reads framed messages
// from the client and lets tests script responses, so the whole
// JSON-RPC layer is exercised without spawning a process.
type fakeServer struct {
	in  *bufio.Reader // client → server
	out io.Writer     // server → client
}

// pipeClient wires a Client to a fakeServer over two in-memory pipes.
func pipeClient(t *testing.T, onNotify func(string, json.RawMessage), onExit func(error)) (*Client, *fakeServer, func()) {
	t.Helper()
	cliR, srvW := io.Pipe() // server writes → client reads
	srvR, cliW := io.Pipe() // client writes → server reads
	c := NewClient(cliR, cliW, onNotify, onExit)
	srv := &fakeServer{in: bufio.NewReader(srvR), out: srvW}
	return c, srv, func() { _ = srvW.Close(); _ = cliW.Close() }
}

// read returns the next message the client sent.
func (s *fakeServer) read(t *testing.T) *message {
	t.Helper()
	m, err := readMessage(s.in)
	if err != nil {
		t.Fatalf("fake server read: %v", err)
	}
	return m
}

// write frames and sends a raw JSON body to the client.
func (s *fakeServer) write(t *testing.T, body string) {
	t.Helper()
	if _, err := fmt.Fprintf(s.out, "Content-Length: %d\r\n\r\n%s", len(body), body); err != nil {
		t.Fatalf("fake server write: %v", err)
	}
}

// TestReadMessageFraming pins the header parser: a valid frame decodes,
// unknown headers are skipped, and a missing Content-Length is a hard
// error because the stream can't be resynchronised.
func TestReadMessageFraming(t *testing.T) {
	body := `{"jsonrpc":"2.0","method":"m"}`
	raw := fmt.Sprintf("Content-Type: application/json\r\nContent-Length: %d\r\n\r\n%s", len(body), body)
	m, err := readMessage(bufio.NewReader(strings.NewReader(raw)))
	if err != nil {
		t.Fatalf("readMessage: %v", err)
	}
	if m.Method != "m" {
		t.Errorf("method = %q, want m", m.Method)
	}

	_, err = readMessage(bufio.NewReader(strings.NewReader("Content-Type: x\r\n\r\n{}")))
	if err == nil {
		t.Error("missing Content-Length should be an error")
	}
}

// TestCallRoundTrip drives a full request/response cycle: the call
// blocks, the fake server answers by id, and the result unmarshals into
// the caller's struct.
func TestCallRoundTrip(t *testing.T) {
	c, srv, done := pipeClient(t, nil, nil)
	defer done()

	type res struct{ OK bool }
	var got res
	errCh := make(chan error, 1)
	go func() { errCh <- c.Call("test/echo", map[string]int{"x": 1}, &got) }()

	m := srv.read(t)
	if m.Method != "test/echo" || m.ID == nil {
		t.Fatalf("server saw %+v, want test/echo request", m)
	}
	srv.write(t, fmt.Sprintf(`{"jsonrpc":"2.0","id":%d,"result":{"OK":true}}`, *m.ID))

	if err := <-errCh; err != nil {
		t.Fatalf("Call: %v", err)
	}
	if !got.OK {
		t.Error("result not unmarshalled")
	}
}

// TestCallWithTimeout pins the two behaviors CallWithTimeout exists
// for: a response that arrives after the caller's deadline fails as a
// timeout (and unblocks — a wedged server can't hang the goroutine),
// while a longer deadline than callTimeout lets a slow-but-legitimate
// exchange (the Copilot device-flow confirmation) complete.
func TestCallWithTimeout(t *testing.T) {
	c, srv, done := pipeClient(t, nil, nil)
	defer done()

	// Short deadline, request delivered but never answered: must fail
	// fast rather than block. The call runs on a goroutine because the
	// in-memory pipe's write blocks until the fake server reads.
	start := time.Now()
	slowCh := make(chan error, 1)
	go func() { slowCh <- c.CallWithTimeout("test/slow", nil, nil, 50*time.Millisecond) }()
	_ = srv.read(t) // deliver it; deliberately no response
	err := <-slowCh
	if err == nil || !strings.Contains(err.Error(), "timed out") {
		t.Fatalf("short-deadline call: err = %v, want timeout", err)
	}
	if time.Since(start) > 2*time.Second {
		t.Fatal("timed-out call blocked far past its deadline")
	}

	// Response after a delay, but within a generous deadline: succeeds.
	errCh := make(chan error, 1)
	go func() { errCh <- c.CallWithTimeout("test/eventually", nil, nil, 5*time.Second) }()
	m := srv.read(t)
	time.Sleep(100 * time.Millisecond)
	srv.write(t, fmt.Sprintf(`{"jsonrpc":"2.0","id":%d,"result":null}`, *m.ID))
	if err := <-errCh; err != nil {
		t.Fatalf("delayed-but-in-deadline call: %v", err)
	}
}

// TestCallServerError pins that a JSON-RPC error response surfaces as a
// Go error naming the method — the caller's only diagnostic.
func TestCallServerError(t *testing.T) {
	c, srv, done := pipeClient(t, nil, nil)
	defer done()

	errCh := make(chan error, 1)
	go func() { errCh <- c.Call("test/fail", nil, nil) }()

	m := srv.read(t)
	srv.write(t, fmt.Sprintf(`{"jsonrpc":"2.0","id":%d,"error":{"code":-32601,"message":"nope"}}`, *m.ID))

	err := <-errCh
	if err == nil || !strings.Contains(err.Error(), "nope") {
		t.Errorf("Call error = %v, want server message surfaced", err)
	}
}

// TestServerNotificationDispatch pins that notifications reach onNotify
// with method and raw params intact — the diagnostics pipeline hangs
// off this path.
func TestServerNotificationDispatch(t *testing.T) {
	type note struct {
		method string
		params json.RawMessage
	}
	ch := make(chan note, 1)
	_, srv, done := pipeClient(t, func(m string, p json.RawMessage) {
		ch <- note{m, p}
	}, nil)
	defer done()

	srv.write(t, `{"jsonrpc":"2.0","method":"textDocument/publishDiagnostics","params":{"uri":"file:///x.go","diagnostics":[]}}`)

	select {
	case n := <-ch:
		if n.method != "textDocument/publishDiagnostics" {
			t.Errorf("method = %q", n.method)
		}
		var p PublishDiagnosticsParams
		if err := json.Unmarshal(n.params, &p); err != nil || p.URI != "file:///x.go" {
			t.Errorf("params = %s (err %v)", n.params, err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("notification never dispatched")
	}
}

// TestServerRequestAutoReply pins the auto-responder: gopls's
// workspace/configuration request must be answered with one element
// per item or the server stalls, and unknown requests get null.
func TestServerRequestAutoReply(t *testing.T) {
	_, srv, done := pipeClient(t, nil, nil)
	defer done()

	srv.write(t, `{"jsonrpc":"2.0","id":7,"method":"workspace/configuration","params":{"items":[{},{}]}}`)
	resp := srv.read(t)
	if resp.ID == nil || *resp.ID != 7 {
		t.Fatalf("reply id = %v, want 7", resp.ID)
	}
	var arr []map[string]any
	if err := json.Unmarshal(resp.Result, &arr); err != nil || len(arr) != 2 {
		t.Errorf("configuration reply = %s, want 2-element array", resp.Result)
	}

	srv.write(t, `{"jsonrpc":"2.0","id":8,"method":"client/registerCapability","params":{}}`)
	resp = srv.read(t)
	if resp.ID == nil || *resp.ID != 8 || string(resp.Result) != "null" {
		t.Errorf("registerCapability reply = id %v result %s, want id 8 null", resp.ID, resp.Result)
	}
}

// TestPipeCloseFailsPendingCalls pins the shutdown contract: when the
// server side dies, in-flight Calls fail promptly (not after the 5s
// timeout) and onExit fires exactly once.
func TestPipeCloseFailsPendingCalls(t *testing.T) {
	exited := make(chan error, 1)
	c, srv, _ := pipeClient(t, nil, func(err error) { exited <- err })

	errCh := make(chan error, 1)
	go func() { errCh <- c.Call("test/hang", nil, nil) }()
	srv.read(t) // make sure the request is in flight

	// Server "crashes".
	_ = srv.out.(io.Closer).Close()

	select {
	case err := <-errCh:
		if err == nil {
			t.Error("pending Call should fail on connection close")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("pending Call still blocked after pipe close")
	}
	select {
	case <-exited:
	case <-time.After(2 * time.Second):
		t.Fatal("onExit never fired")
	}
}

// TestDefinitionShapes pins the response normalisation: single
// Location, array, and null all come back as a slice.
func TestDefinitionShapes(t *testing.T) {
	cases := []struct {
		name, response string
		wantLen        int
	}{
		{"array", `[{"uri":"file:///a.go","range":{"start":{"line":1,"character":2},"end":{"line":1,"character":5}}}]`, 1},
		{"single object", `{"uri":"file:///a.go","range":{"start":{"line":0,"character":0},"end":{"line":0,"character":1}}}`, 1},
		{"null", `null`, 0},
	}
	for _, tc := range cases {
		c, srv, done := pipeClient(t, nil, nil)

		type defRes struct {
			locs []Location
			err  error
		}
		ch := make(chan defRes, 1)
		go func() {
			locs, err := c.Definition("/x.go", Position{Line: 3, Character: 4})
			ch <- defRes{locs, err}
		}()
		m := srv.read(t)
		srv.write(t, fmt.Sprintf(`{"jsonrpc":"2.0","id":%d,"result":%s}`, *m.ID, tc.response))

		r := <-ch
		if r.err != nil {
			t.Errorf("%s: Definition err %v", tc.name, r.err)
		}
		if len(r.locs) != tc.wantLen {
			t.Errorf("%s: got %d locations, want %d", tc.name, len(r.locs), tc.wantLen)
		}
		done()
	}
}

// TestNotifyWireFormat pins what didChange actually puts on the wire —
// full-document sync means exactly one change event with no range.
func TestNotifyWireFormat(t *testing.T) {
	c, srv, done := pipeClient(t, nil, nil)
	defer done()

	go func() { _ = c.DidChange("/tmp/x.go", 4, "package x\n") }()
	m := srv.read(t)
	if m.Method != "textDocument/didChange" {
		t.Fatalf("method = %q", m.Method)
	}
	var p DidChangeParams
	if err := json.Unmarshal(m.Params, &p); err != nil {
		t.Fatalf("params: %v", err)
	}
	if p.TextDocument.Version != 4 || p.TextDocument.URI != "file:///tmp/x.go" {
		t.Errorf("doc id = %+v", p.TextDocument)
	}
	if len(p.ContentChanges) != 1 || p.ContentChanges[0].Text != "package x\n" {
		t.Errorf("content changes = %+v, want one full-text change", p.ContentChanges)
	}
}

// TestGoplsEndToEnd exercises the full stack against a real gopls:
// spawn, initialize, didOpen a file with a type error, and wait for
// publishDiagnostics to report it. Skipped when gopls isn't on PATH
// (CI boxes without Go tooling) — same convention as the git
// integration tests. The lspServerBinary override env var lets local
// runs point at a scratch install.
func TestGoplsEndToEnd(t *testing.T) {
	bin := os.Getenv("CED_TEST_GOPLS")
	if bin == "" {
		bin = "gopls"
	}
	if _, err := exec.LookPath(bin); err != nil {
		t.Skip("gopls not installed")
	}

	dir := t.TempDir()
	// A module root makes gopls treat the dir as a real workspace.
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module e2e\n\ngo 1.21\n"), 0644); err != nil {
		t.Fatalf("seed go.mod: %v", err)
	}
	src := filepath.Join(dir, "main.go")
	// 'undefined: notDefined' — a guaranteed diagnostic.
	code := "package main\n\nfunc main() {\n\tnotDefined()\n}\n"
	if err := os.WriteFile(src, []byte(code), 0644); err != nil {
		t.Fatalf("seed main.go: %v", err)
	}

	diagCh := make(chan PublishDiagnosticsParams, 8)
	onNotify := func(method string, params json.RawMessage) {
		if method != "textDocument/publishDiagnostics" {
			return
		}
		var p PublishDiagnosticsParams
		if json.Unmarshal(params, &p) == nil {
			diagCh <- p
		}
	}
	c, err := Start(dir, bin, nil, onNotify, nil)
	if err != nil {
		t.Fatalf("start gopls: %v", err)
	}
	defer c.Close()
	if err := c.Initialize(dir); err != nil {
		t.Fatalf("initialize: %v", err)
	}
	if err := c.DidOpen(src, "go", 1, code); err != nil {
		t.Fatalf("didOpen: %v", err)
	}

	// gopls type-checks asynchronously; give a cold cache a generous
	// window but return the moment the diagnostic lands.
	deadline := time.After(60 * time.Second)
	for {
		select {
		case p := <-diagCh:
			if URIToPath(p.URI) != src || len(p.Diagnostics) == 0 {
				continue
			}
			msg := p.Diagnostics[0].Message
			if !strings.Contains(msg, "notDefined") {
				t.Errorf("diagnostic = %q, want mention of notDefined", msg)
			}
			// Bonus round-trip: definition from inside main() should
			// resolve — proves requests work after the notification
			// stream is flowing.
			locs, err := c.Definition(src, Position{Line: 2, Character: 6})
			if err != nil {
				t.Errorf("definition: %v", err)
			}
			_ = locs // any non-error answer is fine; main has no def target beyond itself
			return
		case <-deadline:
			t.Fatal("no diagnostics from gopls within 60s")
		}
	}
}

// TestSignatureHelpRequest pins the wrapper end to end: the right
// method and position go out, and the response comes back already
// collapsed — the app layer never learns the protocol had pointer
// optionals, a precedence rule and two label shapes to resolve.
func TestSignatureHelpRequest(t *testing.T) {
	c, srv, done := pipeClient(t, nil, nil)
	defer done()

	type result struct {
		sig *Signature
		err error
	}
	resCh := make(chan result, 1)
	go func() {
		sig, err := c.SignatureHelpAt("/tmp/proj/main.go", Position{Line: 9, Character: 12})
		resCh <- result{sig, err}
	}()

	m := srv.read(t)
	if m.Method != "textDocument/signatureHelp" {
		t.Fatalf("method = %q, want textDocument/signatureHelp", m.Method)
	}
	var params TextDocumentPositionParams
	if err := json.Unmarshal(m.Params, &params); err != nil {
		t.Fatalf("params: %v", err)
	}
	if params.TextDocument.URI != PathToURI("/tmp/proj/main.go") {
		t.Errorf("uri = %q", params.TextDocument.URI)
	}
	if params.Position.Line != 9 || params.Position.Character != 12 {
		t.Errorf("position = %+v, want 9:12", params.Position)
	}
	srv.write(t, fmt.Sprintf(`{"jsonrpc":"2.0","id":%d,"result":{
		"signatures":[{"label":"Join(elems []string, sep string) string",
		               "parameters":[{"label":"elems []string"},{"label":"sep string"}]}],
		"activeSignature":0,"activeParameter":1}}`, *m.ID))

	got := <-resCh
	if got.err != nil {
		t.Fatalf("SignatureHelpAt: %v", got.err)
	}
	if got.sig == nil {
		t.Fatal("SignatureHelpAt returned nil")
	}
	if p := got.sig.Label[got.sig.ParamStart:got.sig.ParamEnd]; p != "sep string" {
		t.Errorf("active parameter = %q, want %q", p, "sep string")
	}
}

// TestSignatureHelpNullResult pins the empty answer: a cursor that isn't
// inside a call is a real answer, not a failure.
func TestSignatureHelpNullResult(t *testing.T) {
	c, srv, done := pipeClient(t, nil, nil)
	defer done()

	type result struct {
		sig *Signature
		err error
	}
	resCh := make(chan result, 1)
	go func() {
		sig, err := c.SignatureHelpAt("/tmp/proj/main.go", Position{})
		resCh <- result{sig, err}
	}()

	m := srv.read(t)
	srv.write(t, fmt.Sprintf(`{"jsonrpc":"2.0","id":%d,"result":null}`, *m.ID))

	got := <-resCh
	if got.err != nil || got.sig != nil {
		t.Errorf("null result = (%+v, %v), want (nil, nil)", got.sig, got.err)
	}
}

// TestReferencesRequest pins the one thing this request has that
// definition and hover don't: the context object. A server given no
// context is entitled to refuse, and includeDeclaration decides whether
// the declaration itself shows up in the list — so both have to be on
// the wire, not merely in the struct.
func TestReferencesRequest(t *testing.T) {
	c, srv, done := pipeClient(t, nil, nil)
	defer done()

	type result struct {
		locs []Location
		err  error
	}
	resCh := make(chan result, 1)
	go func() {
		locs, err := c.References("/tmp/proj/main.go", Position{Line: 3, Character: 5}, true)
		resCh <- result{locs, err}
	}()

	m := srv.read(t)
	if m.Method != "textDocument/references" {
		t.Fatalf("method = %q, want textDocument/references", m.Method)
	}
	var params ReferenceParams
	if err := json.Unmarshal(m.Params, &params); err != nil {
		t.Fatalf("params: %v", err)
	}
	if params.TextDocument.URI != PathToURI("/tmp/proj/main.go") {
		t.Errorf("uri = %q, want %q", params.TextDocument.URI, PathToURI("/tmp/proj/main.go"))
	}
	if params.Position.Line != 3 || params.Position.Character != 5 {
		t.Errorf("position = %+v, want 3:5", params.Position)
	}
	if !params.Context.IncludeDeclaration {
		t.Error("context.includeDeclaration missing from the wire payload")
	}
	srv.write(t, fmt.Sprintf(`{"jsonrpc":"2.0","id":%d,"result":[
		{"uri":"file:///tmp/proj/a.go","range":{"start":{"line":1,"character":2},"end":{"line":1,"character":9}}},
		{"uri":"file:///tmp/proj/b.go","range":{"start":{"line":7,"character":0},"end":{"line":7,"character":7}}}
	]}`, *m.ID))

	got := <-resCh
	if got.err != nil {
		t.Fatalf("References: %v", got.err)
	}
	if len(got.locs) != 2 || got.locs[1].Range.Start.Line != 7 {
		t.Errorf("locations = %+v, want two, the second at line 7", got.locs)
	}
}

// TestReferencesNullResult pins the empty answer: a symbol nothing uses
// is a real answer, not a failure.
func TestReferencesNullResult(t *testing.T) {
	c, srv, done := pipeClient(t, nil, nil)
	defer done()

	type result struct {
		locs []Location
		err  error
	}
	resCh := make(chan result, 1)
	go func() {
		locs, err := c.References("/tmp/proj/main.go", Position{}, false)
		resCh <- result{locs, err}
	}()

	m := srv.read(t)
	srv.write(t, fmt.Sprintf(`{"jsonrpc":"2.0","id":%d,"result":null}`, *m.ID))

	got := <-resCh
	if got.err != nil || got.locs != nil {
		t.Errorf("null result = (%+v, %v), want (nil, nil)", got.locs, got.err)
	}
}

// TestDocumentSymbolsRequest pins the request wrapper end to end: the
// right method and document URI go out, and the response comes back
// already normalised — the app layer never learns that the protocol has
// two shapes for this answer.
func TestDocumentSymbolsRequest(t *testing.T) {
	c, srv, done := pipeClient(t, nil, nil)
	defer done()

	type result struct {
		syms []Symbol
		err  error
	}
	resCh := make(chan result, 1)
	go func() {
		syms, err := c.DocumentSymbols("/tmp/proj/main.go")
		resCh <- result{syms, err}
	}()

	m := srv.read(t)
	if m.Method != "textDocument/documentSymbol" {
		t.Fatalf("method = %q, want textDocument/documentSymbol", m.Method)
	}
	var params DocumentSymbolParams
	if err := json.Unmarshal(m.Params, &params); err != nil {
		t.Fatalf("params: %v", err)
	}
	if params.TextDocument.URI != PathToURI("/tmp/proj/main.go") {
		t.Errorf("uri = %q, want %q", params.TextDocument.URI, PathToURI("/tmp/proj/main.go"))
	}
	srv.write(t, fmt.Sprintf(`{"jsonrpc":"2.0","id":%d,"result":[
		{"name":"main","kind":12,
		 "range":{"start":{"line":3,"character":0},"end":{"line":5,"character":1}},
		 "selectionRange":{"start":{"line":3,"character":5},"end":{"line":3,"character":9}}}
	]}`, *m.ID))

	got := <-resCh
	if got.err != nil {
		t.Fatalf("DocumentSymbols: %v", got.err)
	}
	if len(got.syms) != 1 || got.syms[0].Name != "main" || got.syms[0].Pos.Line != 3 {
		t.Errorf("symbols = %+v, want one 'main' at line 3", got.syms)
	}
}

// TestDocumentSymbolsNullResult pins the empty answer: a server with
// nothing to report sends null, and that must read as "no symbols"
// rather than as a failure — an empty file is a legitimate document.
func TestDocumentSymbolsNullResult(t *testing.T) {
	c, srv, done := pipeClient(t, nil, nil)
	defer done()

	type result struct {
		syms []Symbol
		err  error
	}
	resCh := make(chan result, 1)
	go func() {
		syms, err := c.DocumentSymbols("/tmp/proj/empty.go")
		resCh <- result{syms, err}
	}()

	m := srv.read(t)
	srv.write(t, fmt.Sprintf(`{"jsonrpc":"2.0","id":%d,"result":null}`, *m.ID))

	got := <-resCh
	if got.err != nil || got.syms != nil {
		t.Errorf("null result = (%+v, %v), want (nil, nil)", got.syms, got.err)
	}
}

// renameResult is the (edit, err) pair a Rename call answers with, ferried
// off the request goroutine so the test can drive the fake server in
// between.
type renameResult struct {
	edit *WorkspaceEdit
	err  error
}

// TestRenameRequest pins the wire payload. The old name is deliberately NOT
// on it — the position is the symbol's identity, which is the whole reason
// this differs from a textual replace-all — so the test asserts the three
// fields that are, and that the answer comes back through
// ParseWorkspaceEdit rather than a second decoder.
func TestRenameRequest(t *testing.T) {
	c, srv, done := pipeClient(t, nil, nil)
	defer done()

	resCh := make(chan renameResult, 1)
	go func() {
		edit, err := c.Rename("/tmp/proj/main.go", Position{Line: 2, Character: 8}, "bar")
		resCh <- renameResult{edit, err}
	}()

	m := srv.read(t)
	if m.Method != "textDocument/rename" {
		t.Fatalf("method = %q, want textDocument/rename", m.Method)
	}
	var params RenameParams
	if err := json.Unmarshal(m.Params, &params); err != nil {
		t.Fatalf("params: %v", err)
	}
	if params.TextDocument.URI != PathToURI("/tmp/proj/main.go") {
		t.Errorf("uri = %q, want %q", params.TextDocument.URI, PathToURI("/tmp/proj/main.go"))
	}
	if params.Position.Line != 2 || params.Position.Character != 8 {
		t.Errorf("position = %+v, want 2:8", params.Position)
	}
	if params.NewName != "bar" {
		t.Errorf("newName = %q, want %q", params.NewName, "bar")
	}
	srv.write(t, fmt.Sprintf(`{"jsonrpc":"2.0","id":%d,"result":{"documentChanges":[
		{"textDocument":{"uri":"file:///tmp/proj/a.go","version":3},"edits":[
			{"range":{"start":{"line":1,"character":4},"end":{"line":1,"character":7}},"newText":"bar"}
		]}
	]}}`, *m.ID))

	got := <-resCh
	if got.err != nil {
		t.Fatalf("Rename: %v", got.err)
	}
	if got.edit == nil || len(got.edit.Documents) != 1 {
		t.Fatalf("edit = %+v, want one document", got.edit)
	}
	doc := got.edit.Documents[0]
	if doc.Path != "/tmp/proj/a.go" || len(doc.Edits) != 1 || doc.Edits[0].NewText != "bar" {
		t.Errorf("document = %+v, want a.go with one edit to \"bar\"", doc)
	}
	if doc.Version == nil || *doc.Version != 3 {
		t.Errorf("version = %v, want a claim of 3 — the staleness check needs it", doc.Version)
	}
}

// TestRenameNullResult pins the answer a server gives when the rename is
// legal but changes nothing: nil with a nil error, NOT an error. The app
// layer reports that as "nothing to change", which is a different sentence
// from a refusal and has to stay one.
func TestRenameNullResult(t *testing.T) {
	c, srv, done := pipeClient(t, nil, nil)
	defer done()

	resCh := make(chan renameResult, 1)
	go func() {
		edit, err := c.Rename("/tmp/proj/main.go", Position{}, "bar")
		resCh <- renameResult{edit, err}
	}()

	m := srv.read(t)
	srv.write(t, fmt.Sprintf(`{"jsonrpc":"2.0","id":%d,"result":null}`, *m.ID))

	got := <-resCh
	if got.err != nil || got.edit != nil {
		t.Errorf("null result = (%+v, %v), want (nil, nil)", got.edit, got.err)
	}
}

// TestRenameServerRefusal pins that a server's own reason survives the hop.
// gopls names the rule it enforced ("cannot rename package"), and that
// message is better than anything ced could synthesise — so the client must
// return it rather than flattening every failure to "rename failed".
func TestRenameServerRefusal(t *testing.T) {
	c, srv, done := pipeClient(t, nil, nil)
	defer done()

	resCh := make(chan renameResult, 1)
	go func() {
		edit, err := c.Rename("/tmp/proj/main.go", Position{}, "bar")
		resCh <- renameResult{edit, err}
	}()

	m := srv.read(t)
	srv.write(t, fmt.Sprintf(
		`{"jsonrpc":"2.0","id":%d,"error":{"code":-32602,"message":"can't rename package: not supported"}}`,
		*m.ID))

	got := <-resCh
	if got.err == nil {
		t.Fatal("a server refusal came back as success")
	}
	if !strings.Contains(got.err.Error(), "can't rename package") {
		t.Errorf("error = %v, want the server's own reason", got.err)
	}
}

// TestRenameCapabilityDeclared pins the handshake half. A server that isn't
// told the client speaks textDocument/rename is entitled to answer that it
// has no rename provider, which would make the verb dead on arrival with
// nothing on screen to explain it.
func TestRenameCapabilityDeclared(t *testing.T) {
	c, srv, done := pipeClient(t, nil, nil)
	defer done()

	errCh := make(chan error, 1)
	go func() { errCh <- c.Initialize("/tmp/proj") }()

	m := srv.read(t)
	var params struct {
		Capabilities struct {
			TextDocument map[string]json.RawMessage `json:"textDocument"`
		} `json:"capabilities"`
	}
	if err := json.Unmarshal(m.Params, &params); err != nil {
		t.Fatalf("params: %v", err)
	}
	if _, ok := params.Capabilities.TextDocument["rename"]; !ok {
		t.Error("initialize did not declare textDocument.rename")
	}
	srv.write(t, fmt.Sprintf(`{"jsonrpc":"2.0","id":%d,"result":{"capabilities":{}}}`, *m.ID))
	// The pipes are unbuffered, so the trailing "initialized" notification
	// has to be drained here or Initialize never returns.
	if n := srv.read(t); n.Method != "initialized" {
		t.Errorf("second message = %q, want the initialized notification", n.Method)
	}
	if err := <-errCh; err != nil {
		t.Fatalf("Initialize: %v", err)
	}
}

// codeActionResult pairs the two returns so a goroutine can hand both back.
type codeActionResult struct {
	acts []CodeAction
	err  error
}

// TestCodeActionsRequest pins the wire payload. The range and the echoed
// diagnostics are the whole question — the server matches a quick fix to the
// problem it fixes by the diagnostic it is handed back, so a request that
// dropped them would silently offer refactorings and no fixes.
func TestCodeActionsRequest(t *testing.T) {
	c, srv, done := pipeClient(t, nil, nil)
	defer done()

	// A diagnostic that arrived from a server keeps its raw bytes, which is
	// what has to go back out — `data` here stands for every server-private
	// field this client never modelled.
	var diag Diagnostic
	if err := json.Unmarshal([]byte(
		`{"range":{"start":{"line":4,"character":2},"end":{"line":4,"character":9}},`+
			`"severity":1,"source":"compiler","message":"undefined: foo","data":{"fix":"import"}}`), &diag); err != nil {
		t.Fatalf("seed diagnostic: %v", err)
	}

	resCh := make(chan codeActionResult, 1)
	go func() {
		acts, err := c.CodeActions("/tmp/proj/main.go",
			Range{Start: Position{Line: 4, Character: 0}, End: Position{Line: 6, Character: 1}},
			[]Diagnostic{diag})
		resCh <- codeActionResult{acts, err}
	}()

	m := srv.read(t)
	if m.Method != "textDocument/codeAction" {
		t.Fatalf("method = %q, want textDocument/codeAction", m.Method)
	}
	var params struct {
		TextDocument TextDocumentIdentifier `json:"textDocument"`
		Range        Range                  `json:"range"`
		Context      struct {
			Diagnostics []json.RawMessage `json:"diagnostics"`
		} `json:"context"`
	}
	if err := json.Unmarshal(m.Params, &params); err != nil {
		t.Fatalf("params: %v", err)
	}
	if params.TextDocument.URI != PathToURI("/tmp/proj/main.go") {
		t.Errorf("uri = %q", params.TextDocument.URI)
	}
	if params.Range.Start.Line != 4 || params.Range.End.Line != 6 || params.Range.End.Character != 1 {
		t.Errorf("range = %+v, want 4:0-6:1", params.Range)
	}
	if len(params.Context.Diagnostics) != 1 {
		t.Fatalf("context diagnostics = %d, want 1", len(params.Context.Diagnostics))
	}
	// Byte-for-byte: the fields that match a fix to its problem are exactly
	// the ones a modelled round trip would have dropped.
	if !strings.Contains(string(params.Context.Diagnostics[0]), `"data":{"fix":"import"}`) {
		t.Errorf("diagnostic = %s, want the server's own object verbatim", params.Context.Diagnostics[0])
	}

	srv.write(t, fmt.Sprintf(`{"jsonrpc":"2.0","id":%d,"result":[
		{"title":"Add import","kind":"quickfix","edit":{"changes":{"file:///tmp/proj/main.go":[
			{"range":{"start":{"line":0,"character":0},"end":{"line":0,"character":0}},"newText":"import x\n"}]}}}
	]}`, *m.ID))

	got := <-resCh
	if got.err != nil {
		t.Fatalf("CodeActions: %v", got.err)
	}
	if len(got.acts) != 1 || got.acts[0].Title != "Add import" || got.acts[0].Edit == nil {
		t.Errorf("actions = %+v, want one quickfix carrying an edit", got.acts)
	}
}

// TestCodeActionsEmptyContext pins that "no diagnostics" goes out as an
// empty ARRAY rather than null. The spec makes context.diagnostics required,
// and a server is entitled to refuse a null — which would make every code
// action on a clean file fail for a reason nobody could see.
func TestCodeActionsEmptyContext(t *testing.T) {
	c, srv, done := pipeClient(t, nil, nil)
	defer done()

	go func() { _, _ = c.CodeActions("/tmp/proj/main.go", Range{}, nil) }()

	m := srv.read(t)
	if !strings.Contains(string(m.Params), `"diagnostics":[]`) {
		t.Errorf("params = %s, want an empty diagnostics array", m.Params)
	}
	srv.write(t, fmt.Sprintf(`{"jsonrpc":"2.0","id":%d,"result":null}`, *m.ID))
}

// TestExecuteCommandWireFormat pins that a command's arguments reach the
// server exactly as they arrived. They are the server's own private payload
// — modelling them would drop every field this client doesn't know about,
// and the command would then fail on data it originally sent itself.
func TestExecuteCommandWireFormat(t *testing.T) {
	c, srv, done := pipeClient(t, nil, nil)
	defer done()

	args := []json.RawMessage{json.RawMessage(`{"URI":"file:///p","Fix":"stubMethods","Extra":[1,2]}`)}
	errCh := make(chan error, 1)
	go func() { errCh <- c.ExecuteCommand("gopls.apply_fix", args) }()

	m := srv.read(t)
	if m.Method != "workspace/executeCommand" {
		t.Fatalf("method = %q, want workspace/executeCommand", m.Method)
	}
	var params ExecuteCommandParams
	if err := json.Unmarshal(m.Params, &params); err != nil {
		t.Fatalf("params: %v", err)
	}
	if params.Command != "gopls.apply_fix" {
		t.Errorf("command = %q", params.Command)
	}
	if len(params.Arguments) != 1 || string(params.Arguments[0]) != string(args[0]) {
		t.Errorf("arguments = %v, want the raw payload verbatim", params.Arguments)
	}
	srv.write(t, fmt.Sprintf(`{"jsonrpc":"2.0","id":%d,"result":null}`, *m.ID))
	if err := <-errCh; err != nil {
		t.Fatalf("ExecuteCommand: %v", err)
	}
}

// TestCodeActionCapabilitiesDeclared pins the handshake half. Three claims
// have to be on the wire or the feature is dead in different ways:
// codeActionLiteralSupport (else the server sends only bare Commands),
// workspace.applyEdit (else a command-only action has no route back), and
// workspace.executeCommand (else those commands aren't runnable at all).
func TestCodeActionCapabilitiesDeclared(t *testing.T) {
	c, srv, done := pipeClient(t, nil, nil)
	defer done()

	errCh := make(chan error, 1)
	go func() { errCh <- c.Initialize("/tmp/proj") }()

	m := srv.read(t)
	var params struct {
		Capabilities struct {
			TextDocument map[string]json.RawMessage `json:"textDocument"`
			Workspace    map[string]json.RawMessage `json:"workspace"`
		} `json:"capabilities"`
	}
	if err := json.Unmarshal(m.Params, &params); err != nil {
		t.Fatalf("params: %v", err)
	}
	ca, ok := params.Capabilities.TextDocument["codeAction"]
	if !ok {
		t.Fatal("initialize did not declare textDocument.codeAction")
	}
	if !strings.Contains(string(ca), "codeActionLiteralSupport") {
		t.Errorf("codeAction = %s, want codeActionLiteralSupport", ca)
	}
	// resolveSupport is deliberately absent — declaring it makes the server
	// withhold edits and wait for a second round trip (see Initialize).
	if strings.Contains(string(ca), "resolveSupport") {
		t.Errorf("codeAction = %s, want no resolveSupport", ca)
	}
	if got := string(params.Capabilities.Workspace["applyEdit"]); got != "true" {
		t.Errorf("workspace.applyEdit = %q, want true", got)
	}
	if _, ok := params.Capabilities.Workspace["executeCommand"]; !ok {
		t.Error("initialize did not declare workspace.executeCommand")
	}

	srv.write(t, fmt.Sprintf(`{"jsonrpc":"2.0","id":%d,"result":{"capabilities":{}}}`, *m.ID))
	if n := srv.read(t); n.Method != "initialized" {
		t.Errorf("second message = %q, want the initialized notification", n.Method)
	}
	if err := <-errCh; err != nil {
		t.Fatalf("Initialize: %v", err)
	}
}

// TestRequestHookFallthrough pins the narrow-hook contract. A hook installed
// to answer workspace/applyEdit must NOT inherit workspace/configuration:
// gopls blocks on that one while type-checking, and answering it with the
// honest "I don't handle this" null wedges the server on the first file
// opened. ErrRequestUnhandled is what keeps the auto-responder in charge of
// everything the hook didn't claim.
func TestRequestHookFallthrough(t *testing.T) {
	cliR, srvW := io.Pipe()
	srvR, cliW := io.Pipe()
	var seen []string
	c := &Client{
		w: cliW, r: bufio.NewReader(cliR), pending: map[int64]chan *message{},
		onRequest: func(method string, _ json.RawMessage) (any, error) {
			seen = append(seen, method)
			if method != "workspace/applyEdit" {
				return nil, ErrRequestUnhandled
			}
			return ApplyEditResult{Applied: true}, nil
		},
	}
	go c.readLoop()
	srv := &fakeServer{in: bufio.NewReader(srvR), out: srvW}
	defer func() { _ = srvW.Close(); _ = cliW.Close() }()

	// The declined method still gets the auto-responder's one-per-item echo.
	srv.write(t, `{"jsonrpc":"2.0","id":1,"method":"workspace/configuration","params":{"items":[{},{}]}}`)
	resp := srv.read(t)
	if string(resp.Result) != "[{},{}]" {
		t.Errorf("configuration result = %s, want one empty object per item", resp.Result)
	}

	// The claimed method is answered by the hook.
	srv.write(t, `{"jsonrpc":"2.0","id":2,"method":"workspace/applyEdit","params":{"label":"x","edit":{}}}`)
	resp = srv.read(t)
	var got ApplyEditResult
	if err := json.Unmarshal(resp.Result, &got); err != nil {
		t.Fatalf("applyEdit result: %v", err)
	}
	if !got.Applied {
		t.Errorf("applyEdit result = %+v, want the hook's answer", got)
	}
	if len(seen) != 2 {
		t.Errorf("hook saw %v, want first refusal on both methods", seen)
	}
}
