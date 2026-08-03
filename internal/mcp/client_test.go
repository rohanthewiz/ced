// =============================================================================
// File: internal/mcp/client_test.go
// Author: Rohan Allison <rohanthewiz@gmail.com>
// Created: 2026-07-31
// Copyright: 2026 Rohan Allison. All rights reserved.
// =============================================================================

package mcp

import (
	"bufio"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/rohanthewiz/ced/internal/lsp"
)

// fakeCall records one request the fake server received, so tests can
// assert on what ced actually put on the wire.
type fakeCall struct {
	method string
	params json.RawMessage
}

// fakeServer is a minimal MCP server over in-memory pipes: it answers
// requests from a handler table and remembers what it was asked. Using
// pipes rather than a stub process keeps these tests hermetic (nothing
// on PATH, nothing to install) while still exercising the real framing,
// correlation, and handshake code.
type fakeServer struct {
	mu    sync.Mutex
	calls []fakeCall
	// handle answers one request; returning an error sends a JSON-RPC
	// error object back, which is how a real server reports a refusal.
	handle func(method string, params json.RawMessage) (any, error)
}

// serve drains newline-framed requests until the pipe closes.
func (f *fakeServer) serve(r io.Reader, w io.Writer) {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64*1024), 1<<20)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		var m struct {
			ID     *int64          `json:"id"`
			Method string          `json:"method"`
			Params json.RawMessage `json:"params"`
		}
		if err := json.Unmarshal([]byte(line), &m); err != nil {
			continue
		}
		f.mu.Lock()
		f.calls = append(f.calls, fakeCall{method: m.Method, params: m.Params})
		f.mu.Unlock()
		if m.ID == nil {
			continue // notification — nothing to answer
		}
		res, err := f.handle(m.Method, m.Params)
		out := map[string]any{"jsonrpc": "2.0", "id": *m.ID}
		if err != nil {
			out["error"] = map[string]any{"code": -32000, "message": err.Error()}
		} else {
			out["result"] = res
		}
		body, _ := json.Marshal(out)
		_, _ = w.Write(append(body, '\n'))
	}
}

// methods returns the request methods the fake saw, in order.
func (f *fakeServer) methods() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]string, 0, len(f.calls))
	for _, c := range f.calls {
		out = append(out, c.method)
	}
	return out
}

// waitForMethods blocks until the fake has recorded at least n requests,
// or the test's patience runs out. The initialized NOTIFICATION is
// fire-and-forget — Connect writes it and returns without waiting for a
// reply, because there is no reply — so a test that reads methods()
// immediately is racing the fake's own reader goroutine. Waiting for the
// count is the fix; sleeping would only make the race rarer.
func (f *fakeServer) waitForMethods(t *testing.T, n int) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		f.mu.Lock()
		got := len(f.calls)
		f.mu.Unlock()
		if got >= n {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("timed out waiting for %d requests, saw %v", n, f.methods())
}

// paramsFor returns the params of the first call to method.
func (f *fakeServer) paramsFor(method string) json.RawMessage {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, c := range f.calls {
		if c.method == method {
			return c.params
		}
	}
	return nil
}

// connectFake wires Connect to an in-memory fakeServer for the duration
// of the test.
func connectFake(t *testing.T, handle func(string, json.RawMessage) (any, error)) (*Client, *fakeServer) {
	t.Helper()
	f := &fakeServer{handle: handle}
	prev := dial
	dial = func(dir string, s Server,
		onRequest func(string, json.RawMessage) (any, error),
		onExit func(error)) (*lsp.Client, error) {
		srvIn, cliOut := io.Pipe()
		cliIn, srvOut := io.Pipe()
		go f.serve(srvIn, srvOut)
		return lsp.NewClientACP(cliIn, cliOut, nil, onRequest, onExit), nil
	}
	t.Cleanup(func() { dial = prev })

	// "sh" only has to satisfy Connect's PATH check — dial is stubbed,
	// so nothing is ever spawned.
	c, err := Connect(t.TempDir(), Server{Name: "fake", Transport: TransportStdio, Command: "sh"}, "/tmp/project", nil)
	if err != nil {
		t.Fatalf("Connect: %v", err)
	}
	t.Cleanup(c.Close)
	return c, f
}

// initResult is the standard handshake answer used by most tests.
func initResult(hasTools bool) map[string]any {
	caps := map[string]any{}
	if hasTools {
		caps["tools"] = map[string]any{"listChanged": false}
	}
	return map[string]any{
		"protocolVersion": "2025-06-18",
		"capabilities":    caps,
		"serverInfo":      map[string]any{"name": "Fake Server", "version": "1.2.3"},
		"instructions":    "be helpful",
	}
}

// TestConnect_Handshake pins the three-step MCP handshake: initialize
// carries ced's identity and protocol revision, the initialized
// NOTIFICATION follows it (servers may legally refuse work until it
// lands), and what the server said about itself is recorded.
func TestConnect_Handshake(t *testing.T) {
	c, f := connectFake(t, func(method string, _ json.RawMessage) (any, error) {
		if method == "initialize" {
			return initResult(true), nil
		}
		return map[string]any{}, nil
	})

	f.waitForMethods(t, 2)
	got := f.methods()
	if len(got) < 2 || got[0] != "initialize" || got[1] != "notifications/initialized" {
		t.Fatalf("handshake sequence = %v, want initialize then notifications/initialized", got)
	}
	var params struct {
		ProtocolVersion string `json:"protocolVersion"`
		ClientInfo      struct {
			Name string `json:"name"`
		} `json:"clientInfo"`
		Capabilities struct {
			Roots *struct{} `json:"roots"`
		} `json:"capabilities"`
	}
	if err := json.Unmarshal(f.paramsFor("initialize"), &params); err != nil {
		t.Fatalf("initialize params: %v", err)
	}
	if params.ProtocolVersion != protocolVersion {
		t.Errorf("protocolVersion = %q, want %q", params.ProtocolVersion, protocolVersion)
	}
	if params.ClientInfo.Name != "ced" {
		t.Errorf("clientInfo.name = %q, want ced", params.ClientInfo.Name)
	}
	// Declaring roots is what obliges (and enables) us to answer
	// roots/list — a server scopes itself to the project with it.
	if params.Capabilities.Roots == nil {
		t.Error("initialize did not declare the roots capability")
	}

	info := c.Info()
	if info.Name != "Fake Server" || info.Version != "1.2.3" || !info.HasTools {
		t.Errorf("Info = %+v, want the server's own name/version and tools=true", info)
	}
	if info.Instructions != "be helpful" {
		t.Errorf("instructions = %q", info.Instructions)
	}
}

// TestConnect_HandshakeFailureClosesTheConnection pins the degradation
// path: a server that refuses initialize yields an error naming the
// server, not a half-live client.
func TestConnect_HandshakeFailureClosesTheConnection(t *testing.T) {
	f := &fakeServer{handle: func(string, json.RawMessage) (any, error) {
		return nil, io.ErrUnexpectedEOF
	}}
	prev := dial
	dial = func(dir string, s Server,
		onRequest func(string, json.RawMessage) (any, error), onExit func(error)) (*lsp.Client, error) {
		srvIn, cliOut := io.Pipe()
		cliIn, srvOut := io.Pipe()
		go f.serve(srvIn, srvOut)
		return lsp.NewClientACP(cliIn, cliOut, nil, onRequest, onExit), nil
	}
	t.Cleanup(func() { dial = prev })

	c, err := Connect(t.TempDir(), Server{Name: "grumpy", Transport: TransportStdio, Command: "sh"}, "/tmp/p", nil)
	if err == nil {
		c.Close()
		t.Fatal("expected a handshake error")
	}
	if !strings.Contains(err.Error(), "grumpy") {
		t.Errorf("error should name the server, got %v", err)
	}
}

// TestConnect_RejectsNonStdio pins the scope line: ced's own client is
// stdio-only, and says so in terms that point at the alternative
// (the chat agent gets the declaration) rather than just failing.
func TestConnect_RejectsNonStdio(t *testing.T) {
	_, err := Connect(t.TempDir(),
		Server{Name: "docs", Transport: TransportHTTP, URL: "https://example.com/mcp"}, "/tmp/p", nil)
	if err == nil {
		t.Fatal("expected an error for an http server")
	}
	if !strings.Contains(err.Error(), "docs") || !strings.Contains(err.Error(), "stdio") {
		t.Errorf("error = %v, want it to name the server and the stdio limit", err)
	}
}

// TestListTools_FollowsPagination pins cursor following — a server with
// a long inventory hands it over a page at a time, and a client that
// reads only the first page silently hides tools.
func TestListTools_FollowsPagination(t *testing.T) {
	c, _ := connectFake(t, func(method string, params json.RawMessage) (any, error) {
		switch method {
		case "initialize":
			return initResult(true), nil
		case "tools/list":
			var p struct {
				Cursor string `json:"cursor"`
			}
			_ = json.Unmarshal(params, &p)
			if p.Cursor == "" {
				return map[string]any{
					"tools": []map[string]any{
						{"name": "search", "title": "Search", "description": "find things",
							"inputSchema": map[string]any{"type": "object"}},
					},
					"nextCursor": "page2",
				}, nil
			}
			return map[string]any{
				"tools":      []map[string]any{{"name": "write", "description": "write things"}},
				"nextCursor": "",
			}, nil
		}
		return map[string]any{}, nil
	})

	tools, err := c.ListTools()
	if err != nil {
		t.Fatalf("ListTools: %v", err)
	}
	if len(tools) != 2 || tools[0].Name != "search" || tools[1].Name != "write" {
		t.Fatalf("tools = %+v, want both pages", tools)
	}
	if tools[0].Label() != "Search" {
		t.Errorf("Label should prefer the title, got %q", tools[0].Label())
	}
	if tools[1].Label() != "write" {
		t.Errorf("Label should fall back to the wire name, got %q", tools[1].Label())
	}
	if len(tools[0].InputSchema) == 0 {
		t.Error("input schema was dropped")
	}
}

// TestListTools_NoToolsCapability pins the local short-circuit: a server
// that never advertised tools is answered with an empty list instead of
// a protocol error — "this one has no tools" is a normal answer.
func TestListTools_NoToolsCapability(t *testing.T) {
	c, f := connectFake(t, func(method string, _ json.RawMessage) (any, error) {
		if method == "initialize" {
			return initResult(false), nil
		}
		return nil, io.ErrUnexpectedEOF // any tools/list here is a bug
	})
	tools, err := c.ListTools()
	if err != nil || tools != nil {
		t.Fatalf("ListTools = %v, %v; want nil, nil", tools, err)
	}
	for _, m := range f.methods() {
		if m == "tools/list" {
			t.Fatal("tools/list should not be sent to a server without the capability")
		}
	}
}

// TestCallTool_FlattensContentAndArguments pins both halves of a call:
// arguments go out under the names the server expects, and mixed
// content blocks come back as readable text — non-text blocks summarised
// rather than dropped, so an image-only answer doesn't look empty.
func TestCallTool_FlattensContentAndArguments(t *testing.T) {
	c, f := connectFake(t, func(method string, _ json.RawMessage) (any, error) {
		switch method {
		case "initialize":
			return initResult(true), nil
		case "tools/call":
			return map[string]any{"content": []map[string]any{
				{"type": "text", "text": "first"},
				{"type": "image", "mimeType": "image/png", "data": "…"},
				{"type": "resource", "resource": map[string]any{"uri": "file:///x", "text": "embedded"}},
			}}, nil
		}
		return map[string]any{}, nil
	})

	res, err := c.CallTool("search", map[string]any{"query": "cats"})
	if err != nil {
		t.Fatalf("CallTool: %v", err)
	}
	if res.IsError {
		t.Error("IsError should be false for a successful call")
	}
	if res.Text != "first\n[image image/png]\nembedded" {
		t.Errorf("Text = %q", res.Text)
	}
	var p struct {
		Name      string         `json:"name"`
		Arguments map[string]any `json:"arguments"`
	}
	if err := json.Unmarshal(f.paramsFor("tools/call"), &p); err != nil {
		t.Fatalf("tools/call params: %v", err)
	}
	if p.Name != "search" || p.Arguments["query"] != "cats" {
		t.Errorf("call params = %+v", p)
	}
}

// TestCallTool_ToolReportedError pins the distinction the result modal
// depends on: a tool that ran and failed is a result with IsError set
// (its text explains why), not a Go error — only transport and protocol
// failures are those.
func TestCallTool_ToolReportedError(t *testing.T) {
	c, _ := connectFake(t, func(method string, _ json.RawMessage) (any, error) {
		switch method {
		case "initialize":
			return initResult(true), nil
		case "tools/call":
			return map[string]any{
				"isError": true,
				"content": []map[string]any{{"type": "text", "text": "rate limited"}},
			}, nil
		}
		return map[string]any{}, nil
	})
	res, err := c.CallTool("search", nil)
	if err != nil {
		t.Fatalf("CallTool returned a Go error for a tool-reported failure: %v", err)
	}
	if !res.IsError || res.Text != "rate limited" {
		t.Errorf("res = %+v, want IsError with the server's message", res)
	}
}

// TestCallTool_StructuredOnlyResult pins the fallback for a tool that
// answers only with structured content: it renders as pretty JSON rather
// than an empty box that reads as "nothing happened".
func TestCallTool_StructuredOnlyResult(t *testing.T) {
	c, _ := connectFake(t, func(method string, _ json.RawMessage) (any, error) {
		switch method {
		case "initialize":
			return initResult(true), nil
		case "tools/call":
			return map[string]any{"structuredContent": map[string]any{"count": 3}}, nil
		}
		return map[string]any{}, nil
	})
	res, err := c.CallTool("count", nil)
	if err != nil {
		t.Fatalf("CallTool: %v", err)
	}
	if !strings.Contains(res.Text, "\"count\": 3") {
		t.Errorf("Text = %q, want indented structured content", res.Text)
	}
}

// TestConnect_RealProcess exercises the path the pipe tests stub out:
// an actual child process, resolved on PATH, spawned with the declared
// env and args, framed over real pipes. The stub server is a shell
// script answering the handshake and one tools/list by request order.
// Without this, a break in the spawn/framing seam would only ever show
// up on a user's machine.
func TestConnect_RealProcess(t *testing.T) {
	dir := t.TempDir()
	script := `read line
printf '{"jsonrpc":"2.0","id":1,"result":{"protocolVersion":"2025-06-18","capabilities":{"tools":{}},"serverInfo":{"name":"%s","version":"9.9"}}}\n' "$CED_SERVER_NAME"
read line
read line
printf '{"jsonrpc":"2.0","id":2,"result":{"tools":[{"name":"echo","description":"echoes"}]}}\n'
`
	path := filepath.Join(dir, "server.sh")
	if err := os.WriteFile(path, []byte(script), 0755); err != nil {
		t.Fatalf("write stub: %v", err)
	}

	c, err := Connect(dir, Server{
		Name: "stub", Transport: TransportStdio,
		Command: "/bin/sh", Args: []string{path},
		Env: map[string]string{"CED_SERVER_NAME": "Stub Server"},
	}, dir, nil)
	if err != nil {
		t.Fatalf("Connect: %v", err)
	}
	defer c.Close()

	if got := c.Info().Name; got != "Stub Server" {
		t.Errorf("serverInfo.name = %q, want the env-injected name", got)
	}
	tools, err := c.ListTools()
	if err != nil {
		t.Fatalf("ListTools: %v", err)
	}
	if len(tools) != 1 || tools[0].Name != "echo" {
		t.Fatalf("tools = %+v", tools)
	}
}

// TestServeRequest pins the server→client half: ping is answered
// (liveness), roots/list reports the workspace as a file:// URI (that's
// how a server scopes itself to the open project), and anything needing
// a facility ced hasn't got — sampling, elicitation — is declined
// honestly so the server can fall back.
func TestServeRequest(t *testing.T) {
	h := serveRequest("/home/dev/project")

	if _, err := h("ping", nil); err != nil {
		t.Errorf("ping should be answered, got %v", err)
	}

	res, err := h("roots/list", nil)
	if err != nil {
		t.Fatalf("roots/list: %v", err)
	}
	body, _ := json.Marshal(res)
	var parsed struct {
		Roots []struct {
			URI  string `json:"uri"`
			Name string `json:"name"`
		} `json:"roots"`
	}
	if err := json.Unmarshal(body, &parsed); err != nil {
		t.Fatalf("roots payload: %v", err)
	}
	if len(parsed.Roots) != 1 || parsed.Roots[0].URI != "file:///home/dev/project" {
		t.Fatalf("roots = %+v, want one file:// URI for the workspace", parsed.Roots)
	}
	if parsed.Roots[0].Name != "project" {
		t.Errorf("root name = %q, want the directory's base name", parsed.Roots[0].Name)
	}

	for _, m := range []string{"sampling/createMessage", "elicitation/create"} {
		if _, err := h(m, nil); err == nil {
			t.Errorf("%s should be declined, not answered", m)
		}
	}
}
