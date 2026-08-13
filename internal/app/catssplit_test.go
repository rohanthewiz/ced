// =============================================================================
// File: internal/app/catssplit_test.go
// Author: Rohan Allison <rohanthewiz@gmail.com>
// Created: 2026-08-12
// Copyright: 2026 Rohan Allison. All rights reserved.
// =============================================================================

package app

import (
	"bufio"
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/rohanthewiz/ced/internal/cats"
)

// -----------------------------------------------------------------------------
// A fake control socket, app-side
//
// internal/cats has one of these for testing the transport; this one exists
// to test POLICY — which calls the app makes, in which order, with what
// arguments. It answers the handful of verbs the split sequence uses and
// records every request for assertions.
// -----------------------------------------------------------------------------

// ctlSpy is a stand-in cats control socket.
type ctlSpy struct {
	t    *testing.T
	path string

	mu    sync.Mutex
	calls []ctlCall
	panes []uint32 // the pane ids pane.list reports, grown by pane.split
	// splitFails makes pane.split answer ok:false, the "host refused"
	// path every Tier-1 call site has to survive.
	splitFails bool
	// splitAdds is how many panes a split conjures — 1 in reality, 0 or 2
	// in the tests that pin the ambiguous-diff refusal.
	splitAdds int
	done      chan struct{}
}

// ctlCall is one request the spy saw, with its params left raw so a test
// can assert on the exact JSON a wrapper produced.
type ctlCall struct {
	Method string          `json:"method"`
	Params json.RawMessage `json:"params,omitempty"`
}

// startCtlSpy binds a fake control socket. The path is built under /tmp
// because unix socket paths are capped at ~104 bytes and macOS's TempDir
// spends most of that budget before the file name (same note as the hook
// spy above it).
func startCtlSpy(t *testing.T) *ctlSpy {
	t.Helper()
	base := os.TempDir()
	if fi, err := os.Stat("/tmp"); err == nil && fi.IsDir() {
		base = "/tmp"
	}
	dir, err := os.MkdirTemp(base, "cedctl")
	if err != nil {
		t.Fatalf("temp dir: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })

	s := &ctlSpy{
		t: t, path: filepath.Join(dir, "c"),
		panes: []uint32{7, 9}, splitAdds: 1, done: make(chan struct{}, 8),
	}
	ln, err := net.Listen("unix", s.path)
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
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

// serve answers one request per connection, the shape the real control API
// has.
func (s *ctlSpy) serve(conn net.Conn) {
	defer conn.Close()
	line, err := bufio.NewReader(conn).ReadBytes('\n')
	if err != nil && len(line) == 0 {
		return
	}
	var req ctlCall
	if err := json.Unmarshal(line, &req); err != nil {
		return
	}
	s.mu.Lock()
	s.calls = append(s.calls, req)
	reply := map[string]any{"ok": true}
	switch req.Method {
	case cats.MethodPaneList:
		rows := make([]map[string]any, 0, len(s.panes))
		for _, p := range s.panes {
			rows = append(rows, map[string]any{"pane": p})
		}
		reply["data"] = map[string]any{"panes": rows}
	case cats.MethodPaneSplit:
		if s.splitFails {
			reply = map[string]any{"ok": false, "error": "no room to split"}
			break
		}
		for i := 0; i < s.splitAdds; i++ {
			s.panes = append(s.panes, uint32(100+i))
		}
	}
	last := req.Method
	s.mu.Unlock()

	out, _ := json.Marshal(reply)
	_, _ = conn.Write(append(out, '\n'))
	// send_input is the sequence's last call, and so the one an async test
	// waits on. The refusal paths are exercised synchronously (they call
	// catsSpawnSibling directly), so they need no signal.
	if last == cats.MethodPaneSendIn {
		select {
		case s.done <- struct{}{}:
		default:
		}
	}
}

// wait blocks until the sequence reaches its last call.
func (s *ctlSpy) wait(t *testing.T) {
	t.Helper()
	select {
	case <-s.done:
	case <-time.After(2 * time.Second):
		t.Fatal("the split sequence never reached the socket")
	}
}

// call returns the first recorded request for a method.
func (s *ctlSpy) call(method string) (ctlCall, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, c := range s.calls {
		if c.Method == method {
			return c, true
		}
	}
	return ctlCall{}, false
}

// methods returns every method the spy saw, in order.
func (s *ctlSpy) methods() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]string, 0, len(s.calls))
	for _, c := range s.calls {
		out = append(out, c.Method)
	}
	return out
}

// withCtlSpy puts an App at Tier 1 against a fake control socket, as if the
// startup probe had come back positive.
func withCtlSpy(t *testing.T, a *App) *ctlSpy {
	t.Helper()
	s := startCtlSpy(t)
	a.cats.caps = cats.Caps{
		InCats: true, Control: true, Hooks: false,
		PaneHandle: "w1:p9", ControlSocket: s.path,
	}
	a.cats.client = cats.NewClient(s.path)
	a.cats.self, a.cats.selfOK = 9, true
	if !a.catsTier1() {
		t.Fatal("setup should be Tier 1")
	}
	return s
}

// openTestFileTab writes a file under the app's root and opens it, so the
// split has something to point a sibling editor at.
func openTestFileTab(t *testing.T, a *App, name string) string {
	t.Helper()
	path := filepath.Join(a.rootDir, name)
	if err := os.WriteFile(path, []byte("package main\n"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	a.openFile(path)
	return path
}

// -----------------------------------------------------------------------------
// The spawn sequence
// -----------------------------------------------------------------------------

// The whole feature, end to end: list, split, list again, and type the
// editor command into the pane that appeared.
func TestCatsSplitSpawnsASiblingEditor(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	s := withCtlSpy(t, a)
	path := openTestFileTab(t, a, "main.go")
	catsExecutable = func() (string, error) { return "/opt/bin/ced", nil }
	t.Cleanup(func() { catsExecutable = os.Executable })

	a.catsSplitRight()
	s.wait(t)

	if got, want := strings.Join(s.methods(), " "),
		"pane.list pane.split pane.list pane.send_input"; got != want {
		t.Fatalf("call sequence = %q, want %q", got, want)
	}

	// The split names OUR pane, so the sibling lands beside this editor
	// rather than beside whatever happens to be focused.
	split, _ := s.call(cats.MethodPaneSplit)
	var sp struct {
		Pane      *uint32 `json:"pane"`
		Direction string  `json:"direction"`
	}
	if err := json.Unmarshal(split.Params, &sp); err != nil {
		t.Fatalf("split params: %v", err)
	}
	if sp.Pane == nil || *sp.Pane != 9 || sp.Direction != cats.SplitHorizontal {
		t.Fatalf("split params = %+v", sp)
	}

	// And the command goes to the pane that APPEARED, carrying --root so
	// the sibling opens this project rather than the file's parent.
	sent, _ := s.call(cats.MethodPaneSendIn)
	var in struct {
		Pane   uint32 `json:"pane"`
		Text   string `json:"text"`
		Submit bool   `json:"submit"`
	}
	if err := json.Unmarshal(sent.Params, &in); err != nil {
		t.Fatalf("send_input params: %v", err)
	}
	if in.Pane != 100 {
		t.Fatalf("input went to pane %d, want the new one (100)", in.Pane)
	}
	if !in.Submit {
		t.Fatal("the shell needs the newline pressed, not just typed")
	}
	want := "exec '/opt/bin/ced' --root '" + a.rootDir + "' '" + path + "'\n"
	if in.Text != want {
		t.Fatalf("command = %q, want %q", in.Text, want)
	}
}

// A split whose new pane cannot be identified types NOTHING. Sending a
// command to the wrong pane is a far worse outcome than not sending it —
// and the message has to say the split itself worked, because there is now
// an empty shell on screen the user can drive by hand.
func TestCatsSplitRefusesAnAmbiguousPane(t *testing.T) {
	for _, adds := range []int{0, 2} {
		a := newTestApp(t, t.TempDir())
		s := withCtlSpy(t, a)
		s.splitAdds = adds
		openTestFileTab(t, a, "main.go")

		if err := catsSpawnSibling(a.cats.client, nil, cats.SplitVertical, "x\n"); err == nil {
			t.Fatalf("adds=%d: an ambiguous diff should refuse", adds)
		}
		for _, m := range s.methods() {
			if m == cats.MethodPaneSendIn {
				t.Fatalf("adds=%d: typed into a pane it could not identify", adds)
			}
		}
	}
}

// The host refusing the split is reported as the host's own words. Every
// Tier-1 path has a failure that is not ced's fault, and echoing the
// server's message is what makes it actionable.
func TestCatsSplitReportsAHostRefusal(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	s := withCtlSpy(t, a)
	s.splitFails = true
	openTestFileTab(t, a, "main.go")

	err := catsSpawnSibling(a.cats.client, nil, cats.SplitHorizontal, "x\n")
	if err == nil || !strings.Contains(err.Error(), "no room to split") {
		t.Fatalf("err = %v, want the server's own message", err)
	}
}

// catsNewPane is the diff, pinned directly: exactly one new id, or nothing.
func TestCatsNewPane(t *testing.T) {
	before := []cats.PaneInfo{{Pane: 1}, {Pane: 2}}
	if got, ok := catsNewPane(before, []cats.PaneInfo{{Pane: 1}, {Pane: 2}, {Pane: 5}}); !ok || got != 5 {
		t.Fatalf("one new pane: got %d, ok=%v", got, ok)
	}
	if _, ok := catsNewPane(before, []cats.PaneInfo{{Pane: 1}, {Pane: 2}}); ok {
		t.Fatal("no new pane should not resolve")
	}
	if _, ok := catsNewPane(before, []cats.PaneInfo{{Pane: 1}, {Pane: 2}, {Pane: 5}, {Pane: 6}}); ok {
		t.Fatal("two new panes are ambiguous — a race, not an answer")
	}
	// A pane that vanished alongside the new one must not confuse it.
	if got, ok := catsNewPane(before, []cats.PaneInfo{{Pane: 2}, {Pane: 5}}); !ok || got != 5 {
		t.Fatalf("with a closed pane: got %d, ok=%v", got, ok)
	}
}

// -----------------------------------------------------------------------------
// The command line
// -----------------------------------------------------------------------------

// Everything typed at the shell is quoted, because a path with a space in
// it is a path — and this is the one place ced hands a string it did not
// author to a shell.
func TestCatsSpawnLineQuotesEverything(t *testing.T) {
	line := catsSpawnLine("/usr/local/bin/ced", "/tmp/my project", "/tmp/my project/a b.go")
	want := "exec '/usr/local/bin/ced' --root '/tmp/my project' '/tmp/my project/a b.go'\n"
	if line != want {
		t.Fatalf("line = %q, want %q", line, want)
	}
	// exec, so quitting the editor closes the pane instead of dropping the
	// user at a prompt they never asked for.
	if !strings.HasPrefix(line, "exec ") {
		t.Fatal("the sibling must replace its shell")
	}
	// A single quote in a file name is the case that breaks naive quoting.
	if got, want := catsShellQuote(`it's.go`), `'it'\''s.go'`; got != want {
		t.Fatalf("quote = %s, want %s", got, want)
	}
}

// -----------------------------------------------------------------------------
// Refusals on the main loop
// -----------------------------------------------------------------------------

// Outside cats the row explains itself rather than doing nothing. A menu
// row that silently no-ops is worse than one that is dimmed, and one that
// says why is better than both.
func TestCatsSplitOutsideCatsExplainsItself(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	openTestFileTab(t, a, "main.go")

	a.catsSplitRight()

	if !strings.Contains(a.statusMsg, "cats") {
		t.Fatalf("status = %q, want an explanation", a.statusMsg)
	}
	if a.hasCatsSplit() {
		t.Fatal("the row should be dim outside cats")
	}
}

// An untitled buffer has no file a second process could open.
func TestCatsSplitNeedsASavedFile(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	withCtlSpy(t, a)
	a.tabs = append(a.tabs, newUntitledDirtyTab())
	a.activeTab = len(a.tabs) - 1

	if a.catsSplitTarget() != "" || a.hasCatsSplit() {
		t.Fatal("an untitled buffer is not a split target")
	}
	a.catsSplitRight()
	if !strings.Contains(a.statusMsg, "save this tab") {
		t.Fatalf("status = %q", a.statusMsg)
	}
}

// -----------------------------------------------------------------------------
// The surfaces
// -----------------------------------------------------------------------------

// The ≡ group, the leader namespace, and the context-menu rows are all
// absent outside cats — one program's vocabulary must not clutter every
// other terminal's menu — and all present inside it.
func TestCatsSurfacesAppearOnlyInsideCats(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	if len(a.catsMenuItems()) != 0 || len(catsLeaderBindings(a)) != 0 {
		t.Fatal("cats surfaces leaked into a plain terminal")
	}
	if !strings.Contains(catsLeaderHint(a), "not running in a cats pane") {
		t.Fatalf("hint = %q — an empty namespace still has to say why", catsLeaderHint(a))
	}
	for _, g := range a.visibleMenuGroups() {
		if g.title == "Cats" {
			t.Fatal("the Cats group appeared outside cats")
		}
	}

	withCtlSpy(t, a)
	openTestFileTab(t, a, "main.go")
	if len(a.catsMenuItems()) != 6 || len(catsLeaderBindings(a)) != 6 {
		t.Fatalf("rows=%d keys=%d", len(a.catsMenuItems()), len(catsLeaderBindings(a)))
	}
	found := false
	for _, g := range a.visibleMenuGroups() {
		if g.title == "Cats" {
			found = true
			for _, it := range g.items {
				// The split rows are live at Tier 1 on a saved file; the
				// project row has its own precondition (somewhere to go)
				// and is allowed to be dim here.
				if strings.HasPrefix(it.label, "Open in split") && !it.enabled(a) {
					t.Fatalf("row %q is dim at Tier 1 on a saved file", it.label)
				}
			}
		}
	}
	if !found {
		t.Fatal("no Cats group inside cats")
	}
	// And the right-click menu carries the same two verbs.
	n := 0
	for _, it := range a.editorContextItems(a.activeTabPtr()) {
		if strings.HasPrefix(it.label, "Open in split") {
			n++
		}
	}
	if n != 2 {
		t.Fatalf("%d split rows in the context menu, want 2", n)
	}
}
