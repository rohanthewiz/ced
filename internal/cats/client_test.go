// =============================================================================
// File: internal/cats/client_test.go
// Author: Rohan Allison <rohanthewiz@gmail.com>
// Created: 2026-08-12
// Copyright: 2026 Rohan Allison. All rights reserved.
// =============================================================================

package cats

import (
	"bufio"
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// -----------------------------------------------------------------------------
// Scaffolding shared by every test in this package
// -----------------------------------------------------------------------------

// sockPath returns a socket path short enough to bind. Unix socket paths
// are capped at ~104 bytes by the kernel and macOS's t.TempDir() burns most
// of that on $TMPDIR plus the test name, so this is a hard requirement of
// the transport rather than tidiness (internal/remote's tests carry the same
// note for the same reason).
func sockPath(t *testing.T) string {
	t.Helper()
	base := os.TempDir()
	if fi, err := os.Stat("/tmp"); err == nil && fi.IsDir() {
		base = "/tmp"
	}
	dir, err := os.MkdirTemp(base, "cats")
	if err != nil {
		t.Fatalf("temp dir: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	return filepath.Join(dir, "s")
}

// fakeServer is a stand-in control socket. Each accepted connection is
// handed to handle, which reads one request and decides what to write back —
// the same one-shot shape the real server has.
type fakeServer struct {
	t      *testing.T
	ln     net.Listener
	path   string
	reqs   chan request // every request the server saw, for assertions
	handle func(req request, conn net.Conn)
}

// rawRequest mirrors request with Params kept raw, so a test can assert on
// the exact JSON a wrapper produced rather than on a round-tripped struct.
type rawRequest struct {
	ID     string          `json:"id,omitempty"`
	Method string          `json:"method"`
	Params json.RawMessage `json:"params,omitempty"`
}

// startFake binds a fake control server and serves until the test ends.
func startFake(t *testing.T, handle func(req request, conn net.Conn)) *fakeServer {
	t.Helper()
	path := sockPath(t)
	ln, err := net.Listen("unix", path)
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	s := &fakeServer{t: t, ln: ln, path: path, reqs: make(chan request, 16), handle: handle}
	t.Cleanup(func() { _ = ln.Close() })
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go s.serve(conn)
		}
	}()
	return s
}

// serve reads one request from a connection and lets the test's handler
// answer it.
func (s *fakeServer) serve(conn net.Conn) {
	defer conn.Close()
	line, err := bufio.NewReader(conn).ReadBytes('\n')
	if err != nil && len(line) == 0 {
		return
	}
	var raw rawRequest
	if err := json.Unmarshal(line, &raw); err != nil {
		return
	}
	req := request{ID: raw.ID, Method: raw.Method, Params: raw.Params}
	select {
	case s.reqs <- req:
	default:
	}
	if s.handle != nil {
		s.handle(req, conn)
	}
}

// reply writes a success response carrying data (nil for none).
func reply(t *testing.T, conn net.Conn, data any) {
	t.Helper()
	var raw json.RawMessage
	if data != nil {
		b, err := json.Marshal(data)
		if err != nil {
			t.Fatalf("marshal reply: %v", err)
		}
		raw = b
	}
	b, _ := json.Marshal(response{ID: "ced", OK: true, Data: raw})
	_, _ = conn.Write(append(b, '\n'))
}

// nextReq returns the request the server saw, failing if none arrives.
func (s *fakeServer) nextReq() request {
	s.t.Helper()
	select {
	case r := <-s.reqs:
		return r
	case <-time.After(2 * time.Second):
		s.t.Fatal("no request reached the server")
		return request{}
	}
}

// params re-marshals a captured request's params for string assertions.
func params(t *testing.T, req request) string {
	t.Helper()
	b, err := json.Marshal(req.Params)
	if err != nil {
		t.Fatalf("marshal params: %v", err)
	}
	return string(b)
}

// -----------------------------------------------------------------------------
// Tests
// -----------------------------------------------------------------------------

// A call round-trips: the method reaches the server and the reply's data
// lands in the caller's struct.
func TestCallRoundTrip(t *testing.T) {
	s := startFake(t, func(req request, conn net.Conn) {
		reply(t, conn, Pong{Protocol: 1, Service: "catway"})
	})
	pong, err := NewClient(s.path).Ping()
	if err != nil {
		t.Fatalf("ping: %v", err)
	}
	if pong.Service != "catway" || pong.Protocol != 1 {
		t.Fatalf("got %+v", pong)
	}
	if got := s.nextReq().Method; got != MethodPing {
		t.Fatalf("method = %q", got)
	}
}

// A server that answers ok:false surfaces its message, not a generic
// failure — the message is the only thing a caller can put in a flash.
func TestCallServerError(t *testing.T) {
	s := startFake(t, func(req request, conn net.Conn) {
		b, _ := json.Marshal(response{ID: "ced", OK: false, Error: "pane 9 not found"})
		_, _ = conn.Write(append(b, '\n'))
	})
	err := NewClient(s.path).PaneFocus(9)
	if err == nil || !strings.Contains(err.Error(), "pane 9 not found") {
		t.Fatalf("err = %v", err)
	}
}

// A missing socket fails rather than hanging or panicking: this is the
// everyday Tier-0 path, reached whenever cats has gone away.
func TestCallDeadSocket(t *testing.T) {
	if err := NewClient(sockPath(t)).PaneFocus(1); err == nil {
		t.Fatal("expected an error dialing a socket that does not exist")
	}
	if err := NewClient("").PaneFocus(1); err == nil {
		t.Fatal("expected an error with no socket configured")
	}
}

// A reply bigger than a bufio line buffer decodes intact. Guards the
// decoder choice in Call: reading a "line" would truncate a large capture
// or config payload.
func TestCallLargeReply(t *testing.T) {
	big := strings.Repeat("x", 64*1024)
	s := startFake(t, func(req request, conn net.Conn) {
		reply(t, conn, struct {
			Text string `json:"text"`
		}{big})
	})
	got, err := NewClient(s.path).Read(1, [2]uint32{0, 0}, [2]uint32{9, 9}, false)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if len(got) != len(big) {
		t.Fatalf("got %d bytes, want %d", len(got), len(big))
	}
}

// ResolvePane decodes the "p_<n>" handle with no round trip, and resolves
// the public "w1:p3" form against pane.list. The translation is what every
// self-addressing command depends on.
func TestResolvePane(t *testing.T) {
	s := startFake(t, func(req request, conn net.Conn) {
		reply(t, conn, PaneListResult{Panes: []PaneInfo{
			{Pane: 4, Handle: "w1:p2"},
			{Pane: 7, Handle: "w1:p3", Agent: "claude", AgentState: "working"},
		}})
	})
	c := NewClient(s.path)

	id, err := c.ResolvePane("p_12")
	if err != nil || id != 12 {
		t.Fatalf("p_12 → %d, %v", id, err)
	}
	select {
	case r := <-s.reqs:
		t.Fatalf("p_ form should need no round trip, saw %q", r.Method)
	default:
	}

	if id, err = c.ResolvePane("w1:p3"); err != nil || id != 7 {
		t.Fatalf("w1:p3 → %d, %v", id, err)
	}
	if _, err = c.ResolvePane("w9:p9"); err == nil {
		t.Fatal("expected an error for a handle no pane claims")
	}
}

// The wrappers put the fields on the wire under the names cats reads them
// by. A silent rename here is the failure mode this whole file exists to
// catch, since the server answers a malformed params block with a plain
// error a caller can only report.
func TestWrapperWireShapes(t *testing.T) {
	s := startFake(t, func(req request, conn net.Conn) {
		switch req.Method {
		case MethodTabCreate:
			reply(t, conn, TabCreateResult{Num: 3, Pane: 11})
		case MethodPathList:
			reply(t, conn, PathListResult{Dir: "/w", Exists: true, Recents: []string{"/a"}})
		case MethodConfigGet:
			reply(t, conn, ConfigGetResult{Theme: ConfigTheme{Name: "dark"}})
		default:
			reply(t, conn, nil)
		}
	})
	c := NewClient(s.path)
	pane := uint32(7)

	if err := c.PaneSplit(&pane, SplitVertical); err != nil {
		t.Fatalf("split: %v", err)
	}
	if got := params(t, s.nextReq()); got != `{"pane":7,"direction":"v"}` {
		t.Fatalf("split params = %s", got)
	}

	// submit=false is the staged form and must be omitted, not sent as
	// true by accident: the difference is whether the agent runs it.
	if err := c.PaneSendInput(7, "hello", false); err != nil {
		t.Fatalf("send_input: %v", err)
	}
	if got := params(t, s.nextReq()); got != `{"pane":7,"text":"hello"}` {
		t.Fatalf("send_input params = %s", got)
	}

	if err := c.ChatSend("what is this"); err != nil {
		t.Fatalf("chat.send: %v", err)
	}
	if got := params(t, s.nextReq()); got != `{"text":"what is this"}` {
		t.Fatalf("chat.send params = %s", got)
	}

	res, err := c.TabCreate(TabCreateParams{Title: "ced", Cwd: "/w", Command: []string{"ced", "/w/x.go"}})
	if err != nil || res.Pane != 11 || res.Num != 3 {
		t.Fatalf("tab.create → %+v, %v", res, err)
	}
	if got := params(t, s.nextReq()); got != `{"title":"ced","cwd":"/w","command":["ced","/w/x.go"]}` {
		t.Fatalf("tab.create params = %s", got)
	}

	pl, err := c.PathList("", nil, true)
	if err != nil || len(pl.Recents) != 1 {
		t.Fatalf("path.list → %+v, %v", pl, err)
	}
	if got := params(t, s.nextReq()); got != `{"recents":true}` {
		t.Fatalf("path.list params = %s", got)
	}

	cfg, err := c.ConfigGet()
	if err != nil || cfg.Theme.Name != "dark" {
		t.Fatalf("config.get → %+v, %v", cfg, err)
	}
}

// pane.list carries the agent metadata a sibling-agent surface reads, so
// the mirror keeps those fields rather than only the identity ones.
func TestPaneListAgentFields(t *testing.T) {
	s := startFake(t, func(req request, conn net.Conn) {
		reply(t, conn, PaneListResult{Panes: []PaneInfo{
			{Pane: 7, Handle: "w1:p3", Agent: "claude", AgentState: "blocked", Cwd: "/w", Focused: true},
		}})
	})
	panes, err := NewClient(s.path).PaneList()
	if err != nil || len(panes) != 1 {
		t.Fatalf("pane.list → %v, %v", panes, err)
	}
	p := panes[0]
	if p.Agent != "claude" || p.AgentState != "blocked" || p.Cwd != "/w" || !p.Focused {
		t.Fatalf("got %+v", p)
	}
}
