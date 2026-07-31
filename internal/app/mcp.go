// =============================================================================
// File: internal/app/mcp.go
// Author: Rohan Allison <rohanthewiz@gmail.com>
// Created: 2026-07-31
// Copyright: 2026 Rohan Allison. All rights reserved.
// =============================================================================

// mcp.go is the editor half of the Model Context Protocol integration:
// the inventory read from ~/.config/ced/mcp.json, ced's own connections
// to those servers, and the ≡ surfaces that browse and run their tools.
//
// There are TWO consumers of one inventory, and keeping them straight
// is the thing to hold onto while reading this file:
//
//  1. The chat agent. Whatever backend the chat panel is running gets
//     the enabled servers declared to it in ACP's session/new (see
//     mcpDeclarations, consumed by copilot_chat.go). The AGENT spawns
//     its own copies and calls the tools as part of a turn — that is the
//     path that makes MCP useful day to day, and it needs no connection
//     from ced at all.
//  2. ced itself, through this file. Connecting here is what lets a user
//     see that a server actually works, read its tool list, and run one
//     by hand. It is a deliberate, user-initiated act: nothing is ever
//     spawned at startup, because "I declared a server" must not mean
//     "the editor launched three node processes while I wasn't looking".
//
// House rules, inherited from every other integration:
//
//   - Silent degradation, per server. One server that won't start marks
//     that entry failed with a readable reason in the picker; the editor
//     and every other server carry on. No modal, no nagging.
//   - Events only. Connects, tool lists, and tool calls run on
//     goroutines and post mcp*Events; only main-loop handlers touch
//     App.mcp.
//   - Generation-checked results. Every connection carries a seq;
//     results from a connection that was torn down (disconnect, reload,
//     reconnect) are dropped rather than installed over the live one —
//     the same staleness discipline as the chat layer.
//   - Every choose-one-from-a-list UI is the palette (openPicker), and
//     the ≡ rows are the keyboard twins of a mouse-driven flow.

package app

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/rohanthewiz/ced/internal/clipboard"
	"github.com/rohanthewiz/ced/internal/mcp"
	"github.com/rohanthewiz/ced/internal/userconfig"
)

// mcpResultMaxLines / mcpResultMaxWidth cap the tool-result preview. The
// info modal grows a row per line and does not scroll, so an unbounded
// result would push its own OK button off a short window. The full text
// is still reachable — mcp.lastResult keeps it for the copy row.
const (
	mcpResultMaxLines = 22
	mcpResultMaxWidth = 78
)

// mcpClient is the surface App needs from a live MCP connection. An
// interface (not *mcp.Client) for the same reason copilotConn is one:
// tests drive the app layer with a fake instead of a real server.
type mcpClient interface {
	Info() mcp.ServerInfo
	ListTools() ([]mcp.Tool, error)
	CallTool(name string, args map[string]any) (mcp.ToolResult, error)
	Close()
}

// mcpConnect is the connection constructor, a package var so tests can
// substitute a fake. The explicit nil check keeps a failed connect from
// becoming a non-nil interface wrapping a nil pointer.
var mcpConnect = func(dir string, s mcp.Server, root string, onExit func(error)) (mcpClient, error) {
	c, err := mcp.Connect(dir, s, root, onExit)
	if err != nil {
		return nil, err
	}
	return c, nil
}

// mcpCopyResult puts a tool result on the host clipboard. A var for the
// same reason copilotCopyCode is one: no test may write the developer's
// clipboard.
var mcpCopyResult = clipboard.CopyToSystem

// mcpConn is one server's connection state, keyed by server name in
// mcpState.conns. A conn exists from the moment a connect is armed, so
// the picker can show "connecting…" rather than nothing.
type mcpConn struct {
	seq      int // generation; results from an older seq are dropped
	starting bool
	client   mcpClient
	info     mcp.ServerInfo
	tools    []mcp.Tool
	// err is the last failure for this server, kept after teardown so
	// the picker can say WHY instead of showing an idle-looking row.
	err string
}

// mcpState is the whole integration's state, owned by App and mutated
// only on the main loop.
type mcpState struct {
	// servers is the declared inventory in name order, reloaded from
	// mcp.json by loadMCPConfig. loadErr is the parse/read failure, kept
	// so the ≡ row can report it instead of showing an empty list that
	// looks like "nothing configured".
	servers []mcp.Server
	loadErr string

	conns map[string]*mcpConn

	// seq is the global connection generation, bumped by every teardown
	// and every fresh connect so in-flight goroutines can be identified
	// as stale on arrival.
	seq int

	// toolsWanted names the server whose tool picker should open as soon
	// as its connect completes — the queuedPrompt pattern, so "show me
	// this server's tools" doesn't silently do nothing while the
	// handshake runs.
	toolsWanted string

	// lastResult is the full text of the most recent tool call (the info
	// modal only shows a capped preview) and lastLabel names it for the
	// copy row's flash.
	lastResult string
	lastLabel  string
}

// -----------------------------------------------------------------------------
// Custom tcell events — the goroutine → main-loop bridge
// -----------------------------------------------------------------------------

// mcpReadyEvent lands a completed connect + tools/list.
type mcpReadyEvent struct {
	when   time.Time
	name   string
	seq    int
	client mcpClient
	info   mcp.ServerInfo
	tools  []mcp.Tool
}

// When satisfies the tcell.Event interface.
func (e *mcpReadyEvent) When() time.Time { return e.when }

// mcpFailedEvent lands a connect (or first tools/list) failure. The
// server stays in the inventory — only this attempt failed.
type mcpFailedEvent struct {
	when time.Time
	name string
	seq  int
	err  error
}

// When satisfies the tcell.Event interface.
func (e *mcpFailedEvent) When() time.Time { return e.when }

// mcpExitEvent lands a server process dying under a live connection.
type mcpExitEvent struct {
	when time.Time
	name string
	seq  int
}

// When satisfies the tcell.Event interface.
func (e *mcpExitEvent) When() time.Time { return e.when }

// mcpToolResultEvent lands one tools/call.
type mcpToolResultEvent struct {
	when   time.Time
	name   string
	tool   string
	seq    int
	result mcp.ToolResult
	err    error
}

// When satisfies the tcell.Event interface.
func (e *mcpToolResultEvent) When() time.Time { return e.when }

// -----------------------------------------------------------------------------
// Inventory
// -----------------------------------------------------------------------------

// loadMCPConfig reads (or re-reads) mcp.json into the inventory. A parse
// error is remembered rather than flashed: startup already has enough
// noise, and the ≡ row reports it in full the moment the user goes
// looking. Existing connections are left alone — they belong to a
// server the user connected to deliberately, and dropping them because
// an unrelated entry was edited would be a surprise.
func (a *App) loadMCPConfig() {
	servers, err := mcp.Load(userconfig.MCPPath())
	a.mcp.servers = servers
	if err != nil {
		a.mcp.loadErr = err.Error()
		return
	}
	a.mcp.loadErr = ""
}

// mcpServerByName finds a declared server by name.
func (a *App) mcpServerByName(name string) (mcp.Server, bool) {
	for _, s := range a.mcp.servers {
		if s.Name == name {
			return s, true
		}
	}
	return mcp.Server{}, false
}

// mcpEnabledServers returns the declared servers that are not parked
// behind "disabled" — the set both consumers act on.
func (a *App) mcpEnabledServers() []mcp.Server {
	out := make([]mcp.Server, 0, len(a.mcp.servers))
	for _, s := range a.mcp.servers {
		if s.Enabled() {
			out = append(out, s)
		}
	}
	return out
}

// mcpConnFor returns the tracked connection state for a server, or nil.
func (a *App) mcpConnFor(name string) *mcpConn {
	if a.mcp.conns == nil {
		return nil
	}
	return a.mcp.conns[name]
}

// -----------------------------------------------------------------------------
// Connection lifecycle
// -----------------------------------------------------------------------------

// mcpStartConnect spawns and handshakes a server off-loop, then fetches
// its tool list in the same goroutine — a connection with no inventory
// is nothing the UI can offer, so the two succeed or fail together.
// Idempotent: an already-live or already-connecting server is a no-op.
func (a *App) mcpStartConnect(name string) {
	s, ok := a.mcpServerByName(name)
	if !ok || !s.Enabled() {
		return
	}
	if c := a.mcpConnFor(name); c != nil && (c.starting || c.client != nil) {
		return
	}
	if a.screen == nil {
		return
	}
	a.mcp.seq++
	seq := a.mcp.seq
	if a.mcp.conns == nil {
		a.mcp.conns = map[string]*mcpConn{}
	}
	a.mcp.conns[name] = &mcpConn{seq: seq, starting: true}

	scr := a.screen
	root := a.rootDir
	go func() {
		onExit := func(error) {
			_ = scr.PostEvent(&mcpExitEvent{when: time.Now(), name: s.Name, seq: seq})
		}
		client, err := mcpConnect(root, s, root, onExit)
		if err != nil {
			_ = scr.PostEvent(&mcpFailedEvent{when: time.Now(), name: s.Name, seq: seq, err: err})
			return
		}
		tools, err := client.ListTools()
		if err != nil {
			client.Close()
			_ = scr.PostEvent(&mcpFailedEvent{when: time.Now(), name: s.Name, seq: seq, err: err})
			return
		}
		_ = scr.PostEvent(&mcpReadyEvent{when: time.Now(), name: s.Name, seq: seq,
			client: client, info: client.Info(), tools: tools})
	}()
}

// handleMCPReady installs a live connection and opens the tool picker if
// the user asked for it while the handshake was running.
func (a *App) handleMCPReady(e *mcpReadyEvent) {
	c := a.mcpConnFor(e.name)
	if c == nil || c.seq != e.seq {
		// The connection this belongs to was torn down (disconnect,
		// reconnect, config reload) while it was in flight.
		e.client.Close()
		return
	}
	c.starting = false
	c.client = e.client
	c.info = e.info
	c.tools = e.tools
	c.err = ""
	if a.mcp.toolsWanted == e.name {
		a.mcp.toolsWanted = ""
		a.mcpOpenToolPicker(e.name)
	}
}

// handleMCPFailed records why a server didn't come up. No modal — the
// reason waits in the picker row, which is where the user will look.
// The one exception is a user who is standing there waiting for the tool
// list: they get a flash, because the picker they asked for is never
// going to open.
func (a *App) handleMCPFailed(e *mcpFailedEvent) {
	c := a.mcpConnFor(e.name)
	if c == nil || c.seq != e.seq {
		return
	}
	c.starting = false
	c.client = nil
	c.tools = nil
	if e.err != nil {
		c.err = e.err.Error()
	}
	if a.mcp.toolsWanted == e.name {
		a.mcp.toolsWanted = ""
		a.flash("MCP " + e.name + ": " + c.err)
	}
}

// handleMCPExit marks a server whose process died. Deliberately no
// auto-restart — same contract as the LSP and chat integrations: the
// Connect row is the user's retry gesture, so a server that crashes in a
// loop can't spin forever behind the user's back.
func (a *App) handleMCPExit(e *mcpExitEvent) {
	c := a.mcpConnFor(e.name)
	if c == nil || c.seq != e.seq {
		return
	}
	if c.client == nil && !c.starting {
		return // already settled by a failure event
	}
	c.starting = false
	c.client = nil
	c.tools = nil
	if c.err == "" {
		c.err = "server exited"
	}
	if a.mcp.toolsWanted == e.name {
		a.mcp.toolsWanted = ""
		a.flash("MCP " + e.name + ": " + c.err)
	}
}

// handleMCPToolResult shows one tool call's answer. A tool that ran and
// reported failure still shows its output — the message is the point.
func (a *App) handleMCPToolResult(e *mcpToolResultEvent) {
	if c := a.mcpConnFor(e.name); c == nil || c.seq != e.seq {
		return // the connection it ran under is gone; its answer is moot
	}
	label := e.name + " · " + e.tool
	if e.err != nil {
		a.mcp.lastResult = e.err.Error()
		a.mcp.lastLabel = label
		a.openInfo("MCP "+label+" — failed", mcpResultLines(e.err.Error()))
		return
	}
	a.mcp.lastResult = e.result.Text
	a.mcp.lastLabel = label
	title := "MCP " + label
	if e.result.IsError {
		title += " — tool error"
	}
	a.openInfo(title, mcpResultLines(e.result.Text))
}

// mcpResultLines renders a tool result for the info modal: tabs expanded
// (the modal draws them as one cell), long lines cut, and the whole
// thing capped with a footer naming what was left out. The footer is the
// rule from the git-panel work — a UI that silently truncates reads as a
// UI that showed you everything.
func mcpResultLines(text string) []string {
	if strings.TrimSpace(text) == "" {
		return []string{"(the tool returned no content)"}
	}
	raw := strings.Split(strings.ReplaceAll(text, "\t", "    "), "\n")
	out := make([]string, 0, mcpResultMaxLines+1)
	for i, line := range raw {
		if i >= mcpResultMaxLines {
			out = append(out, fmt.Sprintf("… %d more lines — ≡ → MCP → Copy last result",
				len(raw)-mcpResultMaxLines))
			break
		}
		if len([]rune(line)) > mcpResultMaxWidth {
			line = string([]rune(line)[:mcpResultMaxWidth-1]) + "…"
		}
		out = append(out, line)
	}
	return out
}

// mcpDisconnect tears one server's connection down and bumps its
// generation so anything still in flight lands stale.
func (a *App) mcpDisconnect(name string) {
	c := a.mcpConnFor(name)
	if c == nil {
		return
	}
	if c.client != nil {
		c.client.Close()
	}
	a.mcp.seq++
	delete(a.mcp.conns, name)
}

// mcpShutdown closes every live MCP connection. Called from App.Close so
// child processes don't outlive the editor.
func (a *App) mcpShutdown() {
	for name := range a.mcp.conns {
		if c := a.mcp.conns[name]; c != nil && c.client != nil {
			c.client.Close()
		}
	}
	a.mcp.conns = nil
	a.mcp.toolsWanted = ""
}

// -----------------------------------------------------------------------------
// Declaration to the chat agent
// -----------------------------------------------------------------------------

// mcpDeclarations renders the enabled inventory as ACP session/new
// mcpServers entries, plus a note naming anything left out. httpOK and
// sseOK come from the agent's own advertised mcpCapabilities: declaring
// a transport an agent can't reach fails the whole session/new call on
// some agents, so an unsupported server is dropped and named instead.
//
// Returns (entries, note). The note is transcript text, empty when
// there's nothing to say.
func mcpDeclarations(servers []mcp.Server, httpOK, sseOK bool) ([]any, string) {
	var out []any
	var declared, skipped []string
	for _, s := range servers {
		if !s.Enabled() {
			continue
		}
		switch s.Transport {
		case mcp.TransportStdio:
			entry := map[string]any{
				"name":    s.Name,
				"command": s.Command,
				"args":    s.Args,
			}
			if entry["args"] == nil {
				entry["args"] = []string{}
			}
			entry["env"] = mcpEnvList(s.Env)
			out = append(out, entry)
			declared = append(declared, s.Name)
		case mcp.TransportHTTP, mcp.TransportSSE:
			ok := httpOK
			if s.Transport == mcp.TransportSSE {
				ok = sseOK
			}
			if !ok {
				skipped = append(skipped, s.Name+" ("+string(s.Transport)+" unsupported by this agent)")
				continue
			}
			entry := map[string]any{
				"type":    string(s.Transport),
				"name":    s.Name,
				"url":     s.URL,
				"headers": mcpEnvList(s.Headers),
			}
			out = append(out, entry)
			declared = append(declared, s.Name)
		}
	}
	if len(declared) == 0 && len(skipped) == 0 {
		return out, ""
	}
	note := ""
	if len(declared) > 0 {
		note = "MCP servers: " + strings.Join(declared, ", ")
	}
	if len(skipped) > 0 {
		if note != "" {
			note += "\n"
		}
		note += "MCP skipped: " + strings.Join(skipped, ", ")
	}
	return out, note
}

// mcpEnvList renders a name→value map as ACP's array-of-objects form
// (the shape both env variables and http headers use there), sorted so
// the payload is reproducible. Always returns a non-nil slice: agents
// that validate the field strictly reject a null.
func mcpEnvList(m map[string]string) []map[string]string {
	out := make([]map[string]string, 0, len(m))
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		out = append(out, map[string]string{"name": k, "value": m[k]})
	}
	return out
}

// -----------------------------------------------------------------------------
// ≡ menu surfaces
// -----------------------------------------------------------------------------

// mcpServersLabel names the ≡ row, carrying the inventory count so the
// menu says whether anything is configured before you open it.
func (a *App) mcpServersLabel() string {
	switch {
	case a.mcp.loadErr != "":
		return "MCP servers… (config error)"
	case len(a.mcp.servers) == 0:
		return "MCP servers… (none configured)"
	}
	live := 0
	for _, s := range a.mcp.servers {
		if c := a.mcpConnFor(s.Name); c != nil && c.client != nil {
			live++
		}
	}
	if live > 0 {
		return fmt.Sprintf("MCP servers… (%d, %d connected)", len(a.mcp.servers), live)
	}
	return fmt.Sprintf("MCP servers… (%d)", len(a.mcp.servers))
}

// menuMCPServers opens the server list. A config error or an empty
// inventory opens the setup help instead — a picker with no rows would
// answer the user's question with silence, and "where do I declare
// one?" is the whole question at that point.
func (a *App) menuMCPServers() {
	a.closeMenu()
	if a.mcp.loadErr != "" || len(a.mcp.servers) == 0 {
		a.openInfo("MCP servers", a.mcpSetupHelp())
		return
	}
	items := make([]paletteItem, 0, len(a.mcp.servers))
	for _, s := range a.mcp.servers {
		s := s
		items = append(items, paletteItem{
			label: a.mcpServerRowLabel(s),
			run:   func(app *App) { app.mcpOpenServerActions(s.Name) },
		})
	}
	a.openPicker("MCP servers", items)
}

// mcpServerRowLabel is one server's picker row: a status glyph, the
// name, and enough of the declaration to tell two entries apart.
// Glyphs, deliberately single-width (the chat panel's marker rule):
// ● connected, ◌ connecting, ✕ failed, ○ idle, · disabled.
func (a *App) mcpServerRowLabel(s mcp.Server) string {
	glyph, suffix := "○", ""
	c := a.mcpConnFor(s.Name)
	switch {
	case !s.Enabled():
		glyph, suffix = "·", "  (disabled)"
	case c == nil:
		// idle
	case c.starting:
		glyph, suffix = "◌", "  (connecting…)"
	case c.client != nil:
		glyph = "●"
		suffix = fmt.Sprintf("  (%d tools)", len(c.tools))
	case c.err != "":
		glyph, suffix = "✕", "  ("+firstLine(c.err)+")"
	}
	return glyph + " " + s.Name + suffix + " — " + s.Describe()
}

// firstLine keeps a multi-line error readable in a one-line row.
func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}

// mcpSetupHelp is the "you have no servers" body: the config path, the
// parse error when there is one, and a template short enough to retype.
func (a *App) mcpSetupHelp() []string {
	path := userconfig.MCPPath()
	if path == "" {
		path = "~/.config/ced/mcp.json"
	}
	lines := []string{"Declare MCP servers in " + path}
	if a.mcp.loadErr != "" {
		lines = append(lines, "", "Config error:")
		for _, l := range strings.Split(a.mcp.loadErr, "\n") {
			lines = append(lines, "  "+l)
		}
	}
	return append(lines, "",
		"{",
		`  "mcpServers": {`,
		`    "github": {"command": "npx",`,
		`               "args": ["-y", "@modelcontextprotocol/server-github"],`,
		`               "env": {"GITHUB_TOKEN": "…"}}`,
		"  }",
		"}",
		"",
		"Enabled servers are declared to the chat agent on its next",
		"start, and can be browsed and run from here.")
}

// mcpOpenServerActions is the per-server verb list — the second level of
// the picker, same shape as the git panel's Actions ▾. Rows that would
// no-op are omitted rather than dimmed.
func (a *App) mcpOpenServerActions(name string) {
	s, ok := a.mcpServerByName(name)
	if !ok {
		return
	}
	c := a.mcpConnFor(name)
	connected := c != nil && c.client != nil
	var items []paletteItem

	if s.Enabled() && s.Transport == mcp.TransportStdio {
		items = append(items, paletteItem{
			label: "Tools…",
			run:   func(app *App) { app.mcpOpenTools(name) },
		})
		label := "Connect"
		if connected || (c != nil && c.starting) {
			label = "Reconnect"
		}
		items = append(items, paletteItem{
			label: label,
			run: func(app *App) {
				app.mcpDisconnect(name)
				app.mcpStartConnect(name)
				app.flash("MCP " + name + ": connecting…")
			},
		})
		if connected {
			items = append(items, paletteItem{
				label: "Disconnect",
				run: func(app *App) {
					app.mcpDisconnect(name)
					app.flash("MCP " + name + ": disconnected")
				},
			})
		}
	}
	items = append(items, paletteItem{
		label: "Server info",
		run:   func(app *App) { app.mcpShowServerInfo(name) },
	})
	a.openPicker("MCP: "+name, items)
}

// mcpShowServerInfo reports what ced knows about one server: what was
// declared, what the server said about itself, and why the last attempt
// failed if it did.
func (a *App) mcpShowServerInfo(name string) {
	s, ok := a.mcpServerByName(name)
	if !ok {
		return
	}
	lines := []string{
		"name:      " + s.Name,
		"transport: " + string(s.Transport),
		"declared:  " + s.Describe(),
	}
	if !s.Enabled() {
		lines = append(lines, "status:    disabled in mcp.json")
	}
	if s.Transport != mcp.TransportStdio {
		lines = append(lines, "",
			"ced connects to stdio servers only; this one is declared",
			"to the chat agent, which connects to it itself.")
	}
	c := a.mcpConnFor(name)
	switch {
	case c == nil:
		lines = append(lines, "status:    not connected")
	case c.starting:
		lines = append(lines, "status:    connecting…")
	case c.client != nil:
		lines = append(lines,
			"status:    connected",
			fmt.Sprintf("server:    %s %s (protocol %s)", c.info.Name, c.info.Version, c.info.Protocol),
			fmt.Sprintf("tools:     %d", len(c.tools)))
		if c.info.Instructions != "" {
			lines = append(lines, "", "instructions:")
			for _, l := range strings.Split(c.info.Instructions, "\n") {
				lines = append(lines, "  "+l)
			}
		}
	case c.err != "":
		lines = append(lines, "status:    failed", "error:     "+c.err)
	}
	a.openInfo("MCP server", lines)
}

// mcpOpenTools shows a server's tools, connecting first when it isn't up
// yet — the picker then opens from the ready event (toolsWanted), so
// asking for the tool list never silently does nothing while a handshake
// runs.
func (a *App) mcpOpenTools(name string) {
	c := a.mcpConnFor(name)
	if c != nil && c.client != nil {
		a.mcpOpenToolPicker(name)
		return
	}
	a.mcp.toolsWanted = name
	a.mcpStartConnect(name)
	a.flash("MCP " + name + ": connecting…")
}

// mcpOpenToolPicker lists one connected server's tools.
func (a *App) mcpOpenToolPicker(name string) {
	c := a.mcpConnFor(name)
	if c == nil || c.client == nil {
		return
	}
	if len(c.tools) == 0 {
		a.openInfo("MCP "+name, []string{"This server offers no tools."})
		return
	}
	items := make([]paletteItem, 0, len(c.tools))
	for _, t := range c.tools {
		t := t
		label := t.Label()
		if d := firstLine(t.Description); d != "" {
			label += " — " + d
		}
		items = append(items, paletteItem{
			label: label,
			run:   func(app *App) { app.mcpPromptToolArgs(name, t, mcpArgSkeleton(t)) },
		})
	}
	a.openPicker("MCP "+name+" tools", items)
}

// mcpPromptToolArgs asks for the tool's arguments as JSON, pre-filled
// with a skeleton built from its schema. A tool that takes none runs
// straight away — making the user confirm an empty object would be
// ceremony.
//
// initial carries the previous attempt's text back in when the JSON
// didn't parse, so a typo costs one keystroke to fix instead of retyping
// the whole call.
func (a *App) mcpPromptToolArgs(server string, tool mcp.Tool, initial string) {
	if initial == "{}" {
		a.mcpRunTool(server, tool.Name, nil)
		return
	}
	hint := firstLine(tool.Description)
	if hint == "" {
		hint = "JSON arguments"
	}
	a.openPrompt("Run "+tool.Name, hint, initial, func(app *App, val string) {
		args := map[string]any{}
		if err := json.Unmarshal([]byte(val), &args); err != nil {
			app.flash("MCP arguments: " + err.Error())
			app.mcpPromptToolArgs(server, tool, val)
			return
		}
		app.mcpRunTool(server, tool.Name, args)
	})
}

// mcpArgSkeleton builds the pre-filled argument text from a tool's JSON
// Schema: every required property with an empty value of its declared
// type. "{}" means the tool takes nothing required — the caller reads
// that as "just run it". An unreadable schema also yields "{}" rather
// than a guess; the server's own error is a better teacher than ced
// inventing fields.
func mcpArgSkeleton(t mcp.Tool) string {
	var schema struct {
		Properties map[string]struct {
			Type string `json:"type"`
		} `json:"properties"`
		Required []string `json:"required"`
	}
	if len(t.InputSchema) == 0 || json.Unmarshal(t.InputSchema, &schema) != nil {
		return "{}"
	}
	if len(schema.Required) == 0 {
		return "{}"
	}
	parts := make([]string, 0, len(schema.Required))
	for _, name := range schema.Required {
		val := `""`
		switch schema.Properties[name].Type {
		case "number", "integer":
			val = "0"
		case "boolean":
			val = "false"
		case "array":
			val = "[]"
		case "object":
			val = "{}"
		}
		parts = append(parts, `"`+name+`": `+val)
	}
	return "{" + strings.Join(parts, ", ") + "}"
}

// mcpRunTool calls one tool off-loop and posts the answer back.
func (a *App) mcpRunTool(server, tool string, args map[string]any) {
	c := a.mcpConnFor(server)
	if c == nil || c.client == nil {
		a.flash("MCP " + server + ": not connected")
		return
	}
	if a.screen == nil {
		return
	}
	client, seq, scr := c.client, c.seq, a.screen
	a.flash("MCP " + server + " · " + tool + ": running…")
	go func() {
		res, err := client.CallTool(tool, args)
		_ = scr.PostEvent(&mcpToolResultEvent{when: time.Now(), name: server, tool: tool,
			seq: seq, result: res, err: err})
	}()
}

// menuMCPReload re-reads mcp.json — the escape hatch for editing the
// file with ced itself, which is exactly what a user will do.
func (a *App) menuMCPReload() {
	a.closeMenu()
	a.loadMCPConfig()
	switch {
	case a.mcp.loadErr != "":
		a.openInfo("MCP config", a.mcpSetupHelp())
	case len(a.mcp.servers) == 0:
		a.flash("MCP: no servers declared in " + userconfig.MCPPath())
	default:
		a.flash(fmt.Sprintf("MCP: %d server(s) loaded", len(a.mcp.servers)))
	}
}

// hasMCPResult reports whether a tool result is available to copy.
func (a *App) hasMCPResult() bool { return a.mcp.lastResult != "" }

// mcpCopyResultLabel names the copy row after the call it would copy, so
// the menu says what's on offer without opening it.
func (a *App) mcpCopyResultLabel() string {
	if a.mcp.lastLabel == "" {
		return "Copy last result"
	}
	return "Copy last result (" + a.mcp.lastLabel + ")"
}

// menuMCPCopyResult puts the FULL last tool result on the clipboard —
// the counterpart to the info modal's capped preview.
func (a *App) menuMCPCopyResult() {
	a.closeMenu()
	if a.mcp.lastResult == "" {
		return
	}
	if err := mcpCopyResult(a.mcp.lastResult); err != nil {
		a.flash("copy: " + err.Error())
		return
	}
	a.flash("Copied MCP result (" + a.mcp.lastLabel + ")")
}
