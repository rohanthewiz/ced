// =============================================================================
// File: internal/app/mcp_test.go
// Author: Rohan Allison <rohanthewiz@gmail.com>
// Created: 2026-07-31
// Copyright: 2026 Rohan Allison. All rights reserved.
// =============================================================================

package app

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/rohanthewiz/ced/internal/mcp"
)

// fakeMCPClient stands in for a live server connection so the app layer
// can be driven without spawning anything.
type fakeMCPClient struct {
	mu      sync.Mutex
	info    mcp.ServerInfo
	tools   []mcp.Tool
	listErr error
	result  mcp.ToolResult
	callErr error

	calls  []string
	args   []map[string]any
	closed int
}

// Info reports the handshake identity.
func (f *fakeMCPClient) Info() mcp.ServerInfo { return f.info }

// ListTools returns the canned inventory (or the canned failure).
func (f *fakeMCPClient) ListTools() ([]mcp.Tool, error) { return f.tools, f.listErr }

// CallTool records the invocation and returns the canned answer.
func (f *fakeMCPClient) CallTool(name string, args map[string]any) (mcp.ToolResult, error) {
	f.mu.Lock()
	f.calls = append(f.calls, name)
	f.args = append(f.args, args)
	f.mu.Unlock()
	return f.result, f.callErr
}

// Close counts teardowns so leak assertions have something to check.
func (f *fakeMCPClient) Close() {
	f.mu.Lock()
	f.closed++
	f.mu.Unlock()
}

// closeCount reads the teardown counter under the lock.
func (f *fakeMCPClient) closeCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.closed
}

// stubMCPConnect points the connection constructor at a fake for the
// duration of the test.
func stubMCPConnect(t *testing.T, fn func(mcp.Server, func(error)) (mcpClient, error)) {
	t.Helper()
	prev := mcpConnect
	mcpConnect = func(_ string, s mcp.Server, _ string, onExit func(error)) (mcpClient, error) {
		return fn(s, onExit)
	}
	t.Cleanup(func() { mcpConnect = prev })
}

// seedMCPConfig writes an mcp.json into a temp config home and points
// the resolver at it, so loadMCPConfig reads the fixture instead of the
// developer's real inventory.
func seedMCPConfig(t *testing.T, body string) {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)
	if err := os.MkdirAll(filepath.Join(dir, "ced"), 0755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "ced", "mcp.json"), []byte(body), 0644); err != nil {
		t.Fatalf("write mcp.json: %v", err)
	}
}

// declaredServer is the fixture inventory most lifecycle tests use.
func declaredServer() mcp.Server {
	return mcp.Server{Name: "gh", Transport: mcp.TransportStdio, Command: "npx",
		Args: []string{"-y", "server-github"}}
}

// TestLoadMCPConfig_ReadsInventory pins the startup read: declared
// servers land in the inventory in name order, and NOTHING is spawned —
// declaring a server must not mean the editor quietly launched it.
func TestLoadMCPConfig_ReadsInventory(t *testing.T) {
	seedMCPConfig(t, `{"mcpServers": {
	  "zeta": {"command": "z-server"},
	  "alpha": {"command": "a-server", "disabled": true}
	}}`)
	a := newTestApp(t, t.TempDir())
	spawned := false
	stubMCPConnect(t, func(mcp.Server, func(error)) (mcpClient, error) {
		spawned = true
		return nil, errors.New("should not be reached")
	})

	a.loadMCPConfig()
	if a.mcp.loadErr != "" {
		t.Fatalf("loadErr = %q, want none", a.mcp.loadErr)
	}
	if len(a.mcp.servers) != 2 || a.mcp.servers[0].Name != "alpha" {
		t.Fatalf("servers = %+v, want alpha then zeta", a.mcp.servers)
	}
	if got := a.mcpEnabledServers(); len(got) != 1 || got[0].Name != "zeta" {
		t.Errorf("enabled = %+v, want only zeta (alpha is disabled)", got)
	}
	if spawned || len(a.mcp.conns) != 0 {
		t.Error("loading the inventory must not connect to anything")
	}
}

// TestLoadMCPConfig_RemembersTheError pins the degradation path: a
// broken config is remembered (and surfaced by the ≡ row's label and the
// setup help), not flashed at startup and not fatal.
func TestLoadMCPConfig_RemembersTheError(t *testing.T) {
	seedMCPConfig(t, `{"mcpServers": {"broken": {"args": ["no command"]}}}`)
	a := newTestApp(t, t.TempDir())
	a.loadMCPConfig()

	if a.mcp.loadErr == "" {
		t.Fatal("a malformed inventory should be remembered")
	}
	if !strings.Contains(a.mcpServersLabel(), "config error") {
		t.Errorf("menu label = %q, want it to say there's a config error", a.mcpServersLabel())
	}
	help := strings.Join(a.mcpSetupHelp(), "\n")
	if !strings.Contains(help, "broken") || !strings.Contains(help, "mcp.json") {
		t.Errorf("setup help should name the bad entry and the file, got:\n%s", help)
	}
}

// TestMenuMCPServers_EmptyOpensSetupHelp pins the empty case: the row
// stays clickable and answers "where do I declare one?" instead of
// opening a picker with no rows, which answers nothing.
func TestMenuMCPServers_EmptyOpensSetupHelp(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	a.menuMCPServers()

	m, ok := a.modal.(*confirmModal)
	if !ok || !m.info {
		t.Fatalf("modal = %T, want the info modal", a.modal)
	}
	if !strings.Contains(strings.Join(m.lines, "\n"), "mcpServers") {
		t.Error("setup help should show a config template")
	}
	if !strings.Contains(a.mcpServersLabel(), "none configured") {
		t.Errorf("menu label = %q", a.mcpServersLabel())
	}
}

// TestMCPStartConnect_ReadyInstallsAndOpensToolPicker pins the whole
// user-facing connect flow: "Tools…" on an idle server connects, and the
// picker opens by itself when the handshake lands — a request that
// silently did nothing while a server started would read as a broken
// menu row.
func TestMCPStartConnect_ReadyInstallsAndOpensToolPicker(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	a.mcp.servers = []mcp.Server{declaredServer()}
	fake := &fakeMCPClient{
		info:  mcp.ServerInfo{Name: "GitHub", Version: "1.0", HasTools: true},
		tools: []mcp.Tool{{Name: "search", Description: "find things"}},
	}
	stubMCPConnect(t, func(mcp.Server, func(error)) (mcpClient, error) { return fake, nil })

	a.mcpOpenTools("gh")
	if c := a.mcpConnFor("gh"); c == nil || !c.starting {
		t.Fatal("asking for tools should arm a connect")
	}
	pumpAppEvents(t, a, func() bool { return a.modal != nil })

	c := a.mcpConnFor("gh")
	if c.client == nil || len(c.tools) != 1 || c.info.Name != "GitHub" {
		t.Fatalf("connection not installed: %+v", c)
	}
	if _, ok := a.modal.(*paletteModal); !ok {
		t.Fatalf("modal = %T, want the tool picker", a.modal)
	}
	if a.mcp.toolsWanted != "" {
		t.Error("the queued tool-picker request should be consumed")
	}
	if !strings.Contains(a.mcpServerRowLabel(declaredServer()), "1 tools") {
		t.Errorf("row label = %q, want the tool count", a.mcpServerRowLabel(declaredServer()))
	}
}

// TestMCPReady_StaleGenerationIsClosed pins the staleness discipline: a
// handshake that lands after its connection was torn down is dropped AND
// its process closed, or a disconnect would leak the server it just
// disowned.
func TestMCPReady_StaleGenerationIsClosed(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	a.mcp.servers = []mcp.Server{declaredServer()}
	fake := &fakeMCPClient{}
	a.mcp.conns = map[string]*mcpConn{"gh": {seq: 7, starting: true}}

	a.handleMCPReady(&mcpReadyEvent{name: "gh", seq: 6, client: fake})

	if fake.closeCount() != 1 {
		t.Error("a stale connection must be closed, not leaked")
	}
	if c := a.mcpConnFor("gh"); c.client != nil {
		t.Error("a stale ready event must not install a client")
	}
}

// TestMCPFailure_IsSilentUnlessSomeoneIsWaiting pins the degradation
// rule: a failed connect leaves its reason in the picker row, and only
// flashes when the user is standing there waiting for a tool list that
// is now never going to open.
func TestMCPFailure_IsSilentUnlessSomeoneIsWaiting(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	a.mcp.servers = []mcp.Server{declaredServer()}

	a.mcp.conns = map[string]*mcpConn{"gh": {seq: 1, starting: true}}
	a.handleMCPFailed(&mcpFailedEvent{name: "gh", seq: 1, err: errors.New("npx not found")})
	if a.statusMsg != "" {
		t.Errorf("an unrequested failure should not flash, got %q", a.statusMsg)
	}
	if got := a.mcpServerRowLabel(declaredServer()); !strings.Contains(got, "npx not found") {
		t.Errorf("row label = %q, want the failure reason", got)
	}

	a.mcp.conns = map[string]*mcpConn{"gh": {seq: 2, starting: true}}
	a.mcp.toolsWanted = "gh"
	a.handleMCPFailed(&mcpFailedEvent{name: "gh", seq: 2, err: errors.New("npx not found")})
	if !strings.Contains(a.statusMsg, "npx not found") {
		t.Errorf("a waiting user should be told why, got %q", a.statusMsg)
	}
	if a.mcp.toolsWanted != "" {
		t.Error("the queued request should be dropped after a failure")
	}
}

// TestMCPExit_NoAutoRestart pins the crash contract shared with the LSP
// and chat integrations: a dead server settles into a failed row and
// stays there. Reconnect is the user's gesture, so a server that crashes
// on start can't spin behind their back.
func TestMCPExit_NoAutoRestart(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	a.mcp.servers = []mcp.Server{declaredServer()}
	fake := &fakeMCPClient{}
	a.mcp.conns = map[string]*mcpConn{"gh": {seq: 3, client: fake, tools: []mcp.Tool{{Name: "x"}}}}
	connects := 0
	stubMCPConnect(t, func(mcp.Server, func(error)) (mcpClient, error) {
		connects++
		return fake, nil
	})

	a.handleMCPExit(&mcpExitEvent{name: "gh", seq: 3})

	c := a.mcpConnFor("gh")
	if c.client != nil || c.tools != nil {
		t.Error("a dead server should drop its client and inventory")
	}
	if c.err == "" {
		t.Error("the exit should be recorded as the row's reason")
	}
	if connects != 0 {
		t.Error("a crashed server must not restart itself")
	}
}

// TestMCPRunTool_ShowsResultAndKeepsFullText pins the call round trip:
// the arguments reach the server, the answer opens in the info modal,
// and the FULL text is kept for the copy row — the preview is capped, so
// losing the rest would make long results unreachable.
func TestMCPRunTool_ShowsResultAndKeepsFullText(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	a.mcp.servers = []mcp.Server{declaredServer()}
	long := strings.Repeat("line\n", 40)
	fake := &fakeMCPClient{result: mcp.ToolResult{Text: long}}
	a.mcp.conns = map[string]*mcpConn{"gh": {seq: 1, client: fake}}

	a.mcpRunTool("gh", "search", map[string]any{"query": "cats"})
	pumpAppEvents(t, a, func() bool { return a.modal != nil })

	if len(fake.calls) != 1 || fake.calls[0] != "search" {
		t.Fatalf("calls = %v", fake.calls)
	}
	if fake.args[0]["query"] != "cats" {
		t.Errorf("arguments = %v", fake.args[0])
	}
	m, ok := a.modal.(*confirmModal)
	if !ok || !m.info {
		t.Fatalf("modal = %T, want the info modal", a.modal)
	}
	if len(m.lines) > mcpResultMaxLines+1 {
		t.Errorf("preview is %d lines, want it capped at %d + a footer",
			len(m.lines), mcpResultMaxLines)
	}
	if !strings.Contains(m.lines[len(m.lines)-1], "more lines") {
		t.Error("a truncated preview must say so")
	}
	if a.mcp.lastResult != long {
		t.Error("the full result should survive for the copy row")
	}

	// The copy row hands over the whole thing, not the preview.
	var copied string
	prev := mcpCopyResult
	mcpCopyResult = func(s string) error { copied = s; return nil }
	t.Cleanup(func() { mcpCopyResult = prev })
	if !a.hasMCPResult() {
		t.Fatal("hasMCPResult should be true after a call")
	}
	a.menuMCPCopyResult()
	if copied != long {
		t.Error("the copy row should copy the full result")
	}
}

// TestMCPToolResult_ToolErrorStillShowsOutput pins the distinction the
// modal title carries: a tool that ran and failed shows its message —
// that message is the answer — while the title says it failed.
func TestMCPToolResult_ToolErrorStillShowsOutput(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	a.mcp.conns = map[string]*mcpConn{"gh": {seq: 1, client: &fakeMCPClient{}}}

	a.handleMCPToolResult(&mcpToolResultEvent{name: "gh", tool: "search", seq: 1,
		result: mcp.ToolResult{Text: "rate limited", IsError: true}})

	m, ok := a.modal.(*confirmModal)
	if !ok || !strings.Contains(m.title, "tool error") {
		t.Fatalf("modal = %+v, want a tool-error info modal", a.modal)
	}
	if len(m.lines) != 1 || m.lines[0] != "rate limited" {
		t.Errorf("lines = %v, want the tool's own message", m.lines)
	}
}

// TestMCPToolResult_StaleConnectionIsDropped pins that an answer from a
// connection the user has since torn down never opens a modal over
// whatever they are doing now.
func TestMCPToolResult_StaleConnectionIsDropped(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	a.mcp.conns = map[string]*mcpConn{"gh": {seq: 9, client: &fakeMCPClient{}}}

	a.handleMCPToolResult(&mcpToolResultEvent{name: "gh", tool: "search", seq: 8,
		result: mcp.ToolResult{Text: "late"}})

	if a.modal != nil {
		t.Errorf("a stale result opened %T", a.modal)
	}
	if a.mcp.lastResult != "" {
		t.Error("a stale result must not become the copyable one")
	}
}

// TestMCPDisconnectAndShutdown_CloseClients pins process hygiene: both
// paths close the connection, and disconnect bumps the generation so
// anything still in flight lands stale.
func TestMCPDisconnectAndShutdown_CloseClients(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	one, two := &fakeMCPClient{}, &fakeMCPClient{}
	a.mcp.conns = map[string]*mcpConn{
		"one": {seq: 1, client: one},
		"two": {seq: 2, client: two},
	}
	a.mcp.seq = 2

	a.mcpDisconnect("one")
	if one.closeCount() != 1 || a.mcpConnFor("one") != nil {
		t.Error("disconnect should close and forget the connection")
	}
	if a.mcp.seq != 3 {
		t.Errorf("seq = %d, want a bump so in-flight results land stale", a.mcp.seq)
	}

	a.mcpShutdown()
	if two.closeCount() != 1 {
		t.Error("shutdown should close every live connection")
	}
	if a.mcp.conns != nil {
		t.Error("shutdown should clear the connection map")
	}
}

// TestMCPDeclarations pins the ACP payload ced hands the chat agent:
// stdio entries carry command/args plus env as name/value objects,
// disabled entries never leave, an unsupported remote transport is
// dropped and NAMED, and an empty inventory says nothing at all.
func TestMCPDeclarations(t *testing.T) {
	servers := []mcp.Server{
		{Name: "off", Transport: mcp.TransportStdio, Command: "x", Disabled: true},
		{Name: "gh", Transport: mcp.TransportStdio, Command: "npx",
			Env: map[string]string{"B": "2", "A": "1"}},
		{Name: "docs", Transport: mcp.TransportHTTP, URL: "https://example.com/mcp"},
	}

	entries, note := mcpDeclarations(servers, false, false)
	if len(entries) != 1 {
		t.Fatalf("entries = %v, want only the enabled stdio server", entries)
	}
	e := entries[0].(map[string]any)
	if e["name"] != "gh" || e["command"] != "npx" {
		t.Errorf("entry = %v", e)
	}
	env := e["env"].([]map[string]string)
	if len(env) != 2 || env[0]["name"] != "A" || env[1]["name"] != "B" {
		t.Errorf("env should be sorted name/value objects, got %v", env)
	}
	if !strings.Contains(note, "gh") {
		t.Errorf("note should name what was declared, got %q", note)
	}
	if !strings.Contains(note, "docs") || !strings.Contains(note, "unsupported") {
		t.Errorf("note should name the dropped remote server, got %q", note)
	}
	if strings.Contains(note, "off") {
		t.Errorf("a disabled server is not news, got %q", note)
	}

	// An http-capable agent gets the remote entry in ACP's tagged shape.
	entries, _ = mcpDeclarations(servers, true, false)
	if len(entries) != 2 {
		t.Fatalf("http-capable agent got %v", entries)
	}
	remote := entries[1].(map[string]any)
	if remote["type"] != "http" || remote["url"] != "https://example.com/mcp" {
		t.Errorf("remote entry = %v", remote)
	}

	if entries, note := mcpDeclarations(nil, true, true); len(entries) != 0 || note != "" {
		t.Errorf("empty inventory = %v, %q; want nothing to say", entries, note)
	}
}

// TestMCPArgSkeleton pins the pre-filled prompt text: required
// properties appear with an empty value of their declared type, and
// anything ced can't read yields "{}" — which the caller reads as "just
// run it", so a schema-less tool never demands ceremony.
func TestMCPArgSkeleton(t *testing.T) {
	tool := mcp.Tool{InputSchema: []byte(`{
	  "type": "object",
	  "properties": {"q": {"type": "string"}, "n": {"type": "integer"}, "deep": {"type": "boolean"}},
	  "required": ["q", "n", "deep"]
	}`)}
	if got := mcpArgSkeleton(tool); got != `{"q": "", "n": 0, "deep": false}` {
		t.Errorf("skeleton = %q", got)
	}

	optional := mcp.Tool{InputSchema: []byte(`{"type":"object","properties":{"q":{"type":"string"}}}`)}
	if got := mcpArgSkeleton(optional); got != "{}" {
		t.Errorf("no required properties should yield {}, got %q", got)
	}
	if got := mcpArgSkeleton(mcp.Tool{InputSchema: []byte("not json")}); got != "{}" {
		t.Errorf("an unreadable schema should yield {}, got %q", got)
	}
	if got := mcpArgSkeleton(mcp.Tool{}); got != "{}" {
		t.Errorf("a missing schema should yield {}, got %q", got)
	}
}

// TestMCPPromptToolArgs_NoArgsRunsImmediately pins the shortcut: a tool
// that needs nothing runs on selection instead of opening a prompt that
// only has "{}" to confirm.
func TestMCPPromptToolArgs_NoArgsRunsImmediately(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	fake := &fakeMCPClient{result: mcp.ToolResult{Text: "ok"}}
	a.mcp.conns = map[string]*mcpConn{"gh": {seq: 1, client: fake}}

	a.mcpPromptToolArgs("gh", mcp.Tool{Name: "ping"}, "{}")
	pumpAppEvents(t, a, func() bool { return a.modal != nil })

	if len(fake.calls) != 1 || fake.calls[0] != "ping" {
		t.Fatalf("calls = %v, want the tool to have run straight away", fake.calls)
	}
	if _, ok := a.modal.(*confirmModal); !ok {
		t.Errorf("modal = %T, want the result modal (not a prompt)", a.modal)
	}
}

// TestMCPPromptToolArgs_BadJSONReopensWithTheText pins the typo path:
// unparseable arguments flash the parse error and hand the text BACK, so
// a missing brace costs one keystroke instead of retyping the call.
func TestMCPPromptToolArgs_BadJSONReopensWithTheText(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	fake := &fakeMCPClient{}
	a.mcp.conns = map[string]*mcpConn{"gh": {seq: 1, client: fake}}

	a.mcpPromptToolArgs("gh", mcp.Tool{Name: "search"}, `{"q": ""}`)
	m, ok := a.modal.(*promptModal)
	if !ok {
		t.Fatalf("modal = %T, want a prompt", a.modal)
	}
	m.callback(a, `{"q": broken}`)

	if len(fake.calls) != 0 {
		t.Error("unparseable arguments must not reach the server")
	}
	again, ok := a.modal.(*promptModal)
	if !ok {
		t.Fatalf("modal = %T, want the prompt back", a.modal)
	}
	if again.field.String() != `{"q": broken}` {
		t.Errorf("the user's text should come back, got %q", again.field.String())
	}
	if !strings.Contains(a.statusMsg, "MCP arguments") {
		t.Errorf("status = %q, want the parse error", a.statusMsg)
	}
}

// TestMCPResultLines pins the preview's shape: empty output says so,
// long lines are cut, and truncation is announced with where to get the
// rest — a UI that silently truncates reads as one that showed you
// everything.
func TestMCPResultLines(t *testing.T) {
	if got := mcpResultLines("  \n "); len(got) != 1 || !strings.Contains(got[0], "no content") {
		t.Errorf("empty result = %v", got)
	}
	// Nearly every real tool result ends in a newline; that must not
	// become a blank row in a modal that sizes itself to its line count.
	// Interior blanks are content and stay.
	if got := mcpResultLines("one\n\ntwo\n\n"); len(got) != 3 || got[1] != "" || got[2] != "two" {
		t.Errorf("trailing newlines = %q, want [one, \"\", two]", got)
	}
	long := strings.Repeat("x", mcpResultMaxWidth+40)
	got := mcpResultLines(long)
	if len([]rune(got[0])) != mcpResultMaxWidth {
		t.Errorf("long line = %d runes, want %d", len([]rune(got[0])), mcpResultMaxWidth)
	}
	if !strings.HasSuffix(got[0], "…") {
		t.Error("a cut line should be marked")
	}
	many := mcpResultLines(strings.Repeat("a\n", mcpResultMaxLines+5))
	if len(many) != mcpResultMaxLines+1 {
		t.Fatalf("capped preview = %d lines, want %d + footer", len(many), mcpResultMaxLines)
	}
	if !strings.Contains(many[len(many)-1], "Copy last result") {
		t.Errorf("footer should point at the copy row, got %q", many[len(many)-1])
	}
}

// TestMCPServerRowLabel_Status pins the picker's status glyphs — the
// only place a user sees whether a declared server is idle, connecting,
// live, failed, or parked.
func TestMCPServerRowLabel_Status(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	s := declaredServer()

	if got := a.mcpServerRowLabel(s); !strings.HasPrefix(got, "○ gh") {
		t.Errorf("idle = %q", got)
	}
	a.mcp.conns = map[string]*mcpConn{"gh": {starting: true}}
	if got := a.mcpServerRowLabel(s); !strings.HasPrefix(got, "◌ gh") {
		t.Errorf("connecting = %q", got)
	}
	a.mcp.conns["gh"] = &mcpConn{client: &fakeMCPClient{}, tools: []mcp.Tool{{Name: "a"}, {Name: "b"}}}
	if got := a.mcpServerRowLabel(s); !strings.HasPrefix(got, "● gh") || !strings.Contains(got, "2 tools") {
		t.Errorf("connected = %q", got)
	}
	a.mcp.conns["gh"] = &mcpConn{err: "boom\nsecond line"}
	got := a.mcpServerRowLabel(s)
	if !strings.HasPrefix(got, "✕ gh") || strings.Contains(got, "second line") {
		t.Errorf("failed = %q, want the first error line only", got)
	}

	off := s
	off.Disabled = true
	if got := a.mcpServerRowLabel(off); !strings.HasPrefix(got, "· gh") {
		t.Errorf("disabled = %q", got)
	}
}

// TestMCPServerActions_OmitsImpossibleRows pins the picker rule shared
// with the git panels: rows that would no-op are left out, not dimmed.
// A remote server keeps only the row ced can honour.
func TestMCPServerActions_OmitsImpossibleRows(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	a.mcp.servers = []mcp.Server{
		declaredServer(),
		{Name: "docs", Transport: mcp.TransportHTTP, URL: "https://example.com/mcp"},
	}

	a.mcpOpenServerActions("gh")
	labels := pickerLabels(t, a)
	if len(labels) != 3 || labels[0] != "Tools…" || labels[1] != "Connect" {
		t.Fatalf("idle server rows = %v, want Tools…/Connect/Server info", labels)
	}

	a.mcp.conns = map[string]*mcpConn{"gh": {client: &fakeMCPClient{}}}
	a.mcpOpenServerActions("gh")
	labels = pickerLabels(t, a)
	if len(labels) != 4 || labels[1] != "Reconnect" || labels[2] != "Disconnect" {
		t.Fatalf("connected server rows = %v", labels)
	}

	a.mcpOpenServerActions("docs")
	labels = pickerLabels(t, a)
	if len(labels) != 1 || labels[0] != "Server info" {
		t.Fatalf("remote server rows = %v, want only Server info", labels)
	}
}

// pickerLabels reads the open palette's row labels.
func pickerLabels(t *testing.T, a *App) []string {
	t.Helper()
	m, ok := a.modal.(*paletteModal)
	if !ok {
		t.Fatalf("modal = %T, want a picker", a.modal)
	}
	out := make([]string, 0, len(m.matches))
	for _, mt := range m.matches {
		out = append(out, mt.item.label)
	}
	return out
}
