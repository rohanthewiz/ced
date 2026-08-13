// =============================================================================
// File: internal/cats/client.go
// Author: Rohan Allison <rohanthewiz@gmail.com>
// Created: 2026-08-12
// Copyright: 2026 Rohan Allison. All rights reserved.
// =============================================================================

// The control-socket client: one connection per call, one newline-framed
// JSON request out, one response back, close. That is the whole transport —
// cats' control API is deliberately connectionless per command, so there is
// no session to keep alive, nothing to reconnect, and a dead server costs a
// failed dial rather than a wedged client. (The one exception, the event
// stream, upgrades a connection instead of closing it and lives in
// events.go.)
//
// Every wrapper is a thin shell over Call: build the params struct, name the
// verb, decode Data into the result. The value of the wrappers is that the
// verb names and wire shapes are spelled out ONCE here — mirrored from cats'
// internal/app/command_vocab.go — instead of at each call site in the app.
//
// Errors are plain: a transport failure, or the server's own message when it
// answers ok:false. Callers at Tier 0 never reach this code at all; callers
// at Tier 1 treat any error as "fall back to the Tier-0 path", which is why
// nothing here is typed beyond error.

package cats

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

// Control-API verb names (cats internal/app/command_vocab.go §7 table).
// Only the ones ced uses are mirrored; the table is much larger.
const (
	MethodPing         = "ping"
	MethodPaneSplit    = "pane.split"
	MethodPaneFocus    = "pane.focus"
	MethodPaneList     = "pane.list"
	MethodPaneSendIn   = "pane.send_input"
	MethodRead         = "read"
	MethodCapture      = "capture"
	MethodWaitOutput   = "pane.wait_for_output"
	MethodTabCreate    = "tab.create"
	MethodChatSend     = "chat.send"
	MethodConfigGet    = "config.get"
	MethodPathList     = "path.list"
	MethodSubscribe    = "events.subscribe"
	SplitHorizontal    = "h" // side by side  (cats SplitH)
	SplitVertical      = "v" // stacked       (cats SplitV)
	defaultCallTimeout = 3 * time.Second

	// MethodClipboardRead is NOT on the §7 table: it is a control-transport
	// method, answered by the server before its dispatcher ever sees the name,
	// so that the browser front end cannot ask for it. From this side the
	// difference is invisible — same envelope, same socket — and it is noted
	// only because it is the reason this verb exists for a pane program and not
	// for a phone paired to the same catway.
	MethodClipboardRead = "clipboard.read"
)

// request is the outbound envelope. ID is echoed back; ced sends one request
// per connection, so the id is only ever used to sanity-check the reply.
type request struct {
	ID     string `json:"id,omitempty"`
	Method string `json:"method"`
	Params any    `json:"params,omitempty"`
}

// response is the inbound envelope. Data is left raw so each wrapper decodes
// its own result shape (or ignores it, for the verbs that return nothing).
type response struct {
	ID    string          `json:"id,omitempty"`
	OK    bool            `json:"ok"`
	Error string          `json:"error,omitempty"`
	Data  json.RawMessage `json:"data,omitempty"`
}

// Client is a control socket address plus the timeout its calls run under.
// It holds no connection and no lock: every call dials fresh, so a Client is
// safe to share across goroutines and cheap to keep on the App forever.
type Client struct {
	Socket string

	// Timeout bounds one whole call (dial + write + read). Zero means
	// defaultCallTimeout. It is a field rather than a constant because
	// pane.wait_for_output rides the same unary envelope but resolves only
	// when the pane produces matching output — a caller for that verb hands
	// out a Client copy with a longer bound.
	Timeout time.Duration
}

// NewClient returns a client for the given control socket path. It performs
// no IO — an unreachable socket is discovered by the first call, which is
// also the only place a caller can do anything about it.
func NewClient(socket string) *Client { return &Client{Socket: socket} }

// Call runs one control command. out may be nil for verbs whose reply
// carries no data; when non-nil it is json.Unmarshal'd from the response's
// data field.
func (c *Client) Call(method string, params, out any) error {
	if c == nil || c.Socket == "" {
		return errors.New("cats: no control socket")
	}
	timeout := c.Timeout
	if timeout <= 0 {
		timeout = defaultCallTimeout
	}
	conn, err := dial(c.Socket, timeout)
	if err != nil {
		return fmt.Errorf("cats: dial %s: %w", c.Socket, err)
	}
	defer conn.Close()

	line, err := json.Marshal(request{ID: "ced", Method: method, Params: params})
	if err != nil {
		return fmt.Errorf("cats: encode %s: %w", method, err)
	}
	if _, err := conn.Write(append(line, '\n')); err != nil {
		return fmt.Errorf("cats: send %s: %w", method, err)
	}

	var resp response
	// A response can be longer than bufio's default 4KB line (a capture, a
	// large config.get), so decode a stream rather than reading a line: the
	// JSON decoder stops at the closing brace and the newline framing is
	// just what lets the SERVER find the end of our request.
	if err := json.NewDecoder(bufio.NewReader(conn)).Decode(&resp); err != nil {
		return fmt.Errorf("cats: read %s: %w", method, err)
	}
	if !resp.OK {
		if resp.Error == "" {
			return fmt.Errorf("cats: %s failed", method)
		}
		return fmt.Errorf("cats: %s: %s", method, resp.Error)
	}
	if out == nil || len(resp.Data) == 0 {
		return nil
	}
	if err := json.Unmarshal(resp.Data, out); err != nil {
		return fmt.Errorf("cats: decode %s result: %w", method, err)
	}
	return nil
}

// -----------------------------------------------------------------------------
// Wire shapes — minimal mirrors of cats' internal/app/command_vocab.go.
// Fields ced neither sends nor reads are omitted on purpose: a mirror that
// tried to be complete would be a second copy of a file we do not own.
// -----------------------------------------------------------------------------

// Pong is the ping reply: how the server identifies itself.
type Pong struct {
	Protocol int    `json:"protocol"`
	Service  string `json:"service"`
}

// PaneInfo is one row of pane.list. Pane is the internal id every other
// command addresses a pane by; Handle is the public "w1:p3" label the pane
// environment carries — the pair is what makes ResolvePane possible. The
// agent fields are the arbitrated identity cats' own sidebar shows, which is
// what a sibling-agent status segment reads.
type PaneInfo struct {
	Pane       uint32 `json:"pane"`
	Handle     string `json:"handle,omitempty"`
	Name       string `json:"name,omitempty"`
	Focused    bool   `json:"focused"`
	Visible    bool   `json:"visible"`
	Agent      string `json:"agent,omitempty"`
	AgentState string `json:"agent_state,omitempty"`
	Title      string `json:"title,omitempty"`
	Cwd        string `json:"cwd,omitempty"`
}

// PaneListResult is pane.list's payload.
type PaneListResult struct {
	Panes []PaneInfo `json:"panes"`
}

// TabCreateParams opens a tab. The zero value is a default shell inheriting
// the neighbouring tab's cwd; Command makes the tab run a program directly
// (its exit closes the pane), which is how ced spawns a sibling editor.
type TabCreateParams struct {
	Title   string            `json:"title,omitempty"`
	Cwd     string            `json:"cwd,omitempty"`
	Command []string          `json:"command,omitempty"`
	Env     map[string]string `json:"env,omitempty"`
}

// TabCreateResult names the new tab and its root pane, so a caller can drive
// the fresh pane without a follow-up pane.list.
type TabCreateResult struct {
	Num  int    `json:"num"`
	Pane uint32 `json:"pane"`
}

// PathListResult is path.list's payload. Recents is the cdx-ranked frecency
// list (opt-in per request); Dirs is an unranked listing of Dir.
type PathListResult struct {
	Dir       string   `json:"dir"`
	Cwd       string   `json:"cwd"`
	Home      string   `json:"home"`
	Exists    bool     `json:"exists"`
	Error     string   `json:"error,omitempty"`
	Dirs      []string `json:"dirs,omitempty"`
	Truncated bool     `json:"truncated,omitempty"`
	Recents   []string `json:"recents,omitempty"`
}

// ConfigTheme is the appearance section of config.get: the active theme name
// and its color map, which is what a synthesized "Cats (host)" ced theme is
// built from.
type ConfigTheme struct {
	Name   string            `json:"name,omitempty"`
	Colors map[string]string `json:"colors,omitempty"`
	Font   string            `json:"font,omitempty"`
}

// ConfigGetResult is config.get's payload, trimmed to the appearance fields.
// Theme is the EFFECTIVE appearance (overrides already folded in), which is
// the one a host-matching editor theme should follow.
type ConfigGetResult struct {
	Path  string      `json:"path"`
	Theme ConfigTheme `json:"theme"`
}

// -----------------------------------------------------------------------------
// Typed verbs
// -----------------------------------------------------------------------------

// Ping asks the server to identify itself. It is the Tier-1 gate (see
// Caps.Probe) and the cheapest possible liveness check.
func (c *Client) Ping() (Pong, error) {
	var p Pong
	// The probe budget, not the call budget: this runs during startup
	// detection, where the whole point is to give up quickly.
	pc := *c
	if pc.Timeout <= 0 {
		pc.Timeout = ProbeTimeout
	}
	err := pc.Call(MethodPing, nil, &p)
	return p, err
}

// PaneList returns every pane in the session, with the agent/title/cwd
// metadata cats' own sidebar shows.
func (c *Client) PaneList() ([]PaneInfo, error) {
	var r PaneListResult
	if err := c.Call(MethodPaneList, nil, &r); err != nil {
		return nil, err
	}
	return r.Panes, nil
}

// ResolvePane turns the public pane handle from the environment ("w1:p3")
// into the internal id control commands take. The "p_<n>" form is decoded
// locally with no round trip; anything else costs one pane.list.
//
// Its real job is answering "which pane am I?", which every self-addressing
// command (split from here, focus me) needs and which nothing in the pane
// environment tells us directly.
func (c *Client) ResolvePane(handle string) (uint32, error) {
	if id, ok := ParsePaneHandle(handle); ok {
		return id, nil
	}
	panes, err := c.PaneList()
	if err != nil {
		return 0, err
	}
	for _, p := range panes {
		if p.Handle == handle {
			return p.Pane, nil
		}
	}
	return 0, fmt.Errorf("cats: pane %q not found", handle)
}

// SplitParams shapes a pane.split. Pane nil splits whichever pane is focused;
// SplitHorizontal puts the panes side by side, SplitVertical stacks them.
//
// Cwd/Command/Env are the same spawn trio tab.create takes: with a Command the
// host execs that argv AS the new pane's process, so quitting it closes the
// pane and there is no shell in the middle — no quoting, no bracketed-paste
// assumption, no race against a prompt. Without one the pane is a shell in the
// split pane's live directory, which is the historical behaviour.
type SplitParams struct {
	Pane      *uint32           `json:"pane,omitempty"`
	Direction string            `json:"direction"`
	Cwd       string            `json:"cwd,omitempty"`
	Command   []string          `json:"command,omitempty"`
	Env       map[string]string `json:"env,omitempty"`
}

// SplitResult is pane.split's payload: the id of the pane it created, which it
// also focuses.
//
// Pane 0 is the CAPABILITY SIGNAL, not an error. A cats older than the commit
// that added this result answers `ok` with no data at all, which decodes to the
// zero value — and the same commit added the Command parameter, so a zero here
// also means the argv was ignored and the new pane is a plain shell. One field
// therefore answers both questions a caller has, with no extra round trip and
// nothing to probe at startup (see catsSpawnSibling, which falls back to typing
// at that shell).
type SplitResult struct {
	Pane uint32 `json:"pane"`
}

// PaneSplit splits a pane and returns the pane it created. See SplitResult for
// what a zero pane means.
func (c *Client) PaneSplit(p SplitParams) (SplitResult, error) {
	var r SplitResult
	err := c.Call(MethodPaneSplit, p, &r)
	return r, err
}

// PaneFocus focuses a pane within its tab — the click-through for a status
// segment naming a sibling agent.
func (c *Client) PaneFocus(pane uint32) error {
	return c.Call(MethodPaneFocus, struct {
		Pane uint32 `json:"pane"`
	}{pane}, nil)
}

// PaneSendInput injects text into a pane's PTY as though typed there.
// submit false stages the text for the user to read and press Enter on
// themselves — which is the setting ced uses for "send selection to the
// agent pane", because putting words in an agent's mouth and running them
// is a different act from handing it a quote.
func (c *Client) PaneSendInput(pane uint32, text string, submit bool) error {
	return c.Call(MethodPaneSendIn, struct {
		Pane   uint32 `json:"pane"`
		Text   string `json:"text,omitempty"`
		Submit bool   `json:"submit,omitempty"`
	}{pane, text, submit}, nil)
}

// Read extracts the text of a selection in another pane. Anchor and cursor
// are [row, col] in absolute screen-buffer coordinates (row measured from
// the top of scrollback); rect selects a block instead of a reading-order
// range.
func (c *Client) Read(pane uint32, anchor, cursor [2]uint32, rect bool) (string, error) {
	var r struct {
		Text string `json:"text"`
	}
	err := c.Call(MethodRead, struct {
		Pane   uint32    `json:"pane"`
		Anchor [2]uint32 `json:"anchor"`
		Cursor [2]uint32 `json:"cursor"`
		Rect   bool      `json:"rect,omitempty"`
	}{pane, anchor, cursor, rect}, &r)
	return r.Text, err
}

// Capture scopes (cats CaptureParams.Scope). Visible is the on-screen
// viewport; Recent is the last Lines rows of scrollback plus the active
// screen, with Lines 0 meaning the whole buffer.
const (
	CaptureVisible uint8 = 0
	CaptureRecent  uint8 = 1
)

// Capture extracts a pane's buffer text — whole rows, no coordinates, which
// is what "show me what that pane says" wants and what `read` (a selection
// between two points) cannot answer.
//
// ansi is deliberately not exposed: every consumer here puts the text in a
// ced buffer, and VT escapes in a diff would be noise the user cannot see
// past. unwrap rejoins soft-wrapped lines, which is what makes a captured
// 200-column agent transcript diffable against a source file.
func (c *Client) Capture(pane uint32, scope uint8, lines uint32, unwrap bool) (string, error) {
	var r struct {
		Text string `json:"text"`
	}
	err := c.Call(MethodCapture, struct {
		Pane   uint32 `json:"pane"`
		Scope  uint8  `json:"scope,omitempty"`
		Lines  uint32 `json:"lines,omitempty"`
		Unwrap bool   `json:"unwrap,omitempty"`
	}{pane, scope, lines, unwrap}, &r)
	return r.Text, err
}

// MaxWaitTimeout mirrors cats' own cap on a single wait (command_vocab.go).
// A longer request is clamped server-side, so asking for more only misleads
// the caller sizing its own round-trip budget.
const MaxWaitTimeout = 10 * time.Minute

// waitSlack is how much longer the CALL is allowed to take than the wait it
// carries. pane.wait_for_output rides the ordinary unary envelope but resolves
// only when the pane's output matches — so the client's own timeout has to sit
// above the server's, or every wait would end as a client-side timeout with no
// idea whether the thing being waited on ever happened.
const waitSlack = 5 * time.Second

// WaitForOutput blocks until the pane's output matches pattern, the timeout
// elapses, or the pane exits. matched false is not an error: it is the honest
// answer "that never appeared", which a caller reports differently from "the
// host refused to look".
//
// The wait is matched against the pane's LIVE output as it streams, seeded
// once with what is already on screen — so a marker printed by a command that
// scrolls away in the same instant is still caught. That property is the whole
// reason this is a control-API verb rather than a capture-and-poll loop here.
func (c *Client) WaitForOutput(pane uint32, pattern string, regex bool, timeout time.Duration) (matched bool, text string, err error) {
	if c == nil {
		// Every other verb reaches this check inside Call; this one dials
		// through a copy of the client, so it has to ask first.
		return false, "", errors.New("cats: no control socket")
	}
	if timeout <= 0 || timeout > MaxWaitTimeout {
		timeout = MaxWaitTimeout
	}
	// A COPY of the client with a wider budget, never a mutation: a Client is
	// shared across goroutines by design (see the type comment), and widening
	// the shared one would leave every unrelated call waiting minutes for a
	// dead socket.
	waiter := &Client{Socket: c.Socket, Timeout: timeout + waitSlack}
	var r struct {
		Matched bool   `json:"matched"`
		Text    string `json:"text,omitempty"`
	}
	err = waiter.Call(MethodWaitOutput, struct {
		Pane      uint32 `json:"pane"`
		Pattern   string `json:"pattern"`
		Regex     bool   `json:"regex,omitempty"`
		TimeoutMs uint32 `json:"timeout_ms,omitempty"`
	}{pane, pattern, regex, uint32(timeout / time.Millisecond)}, &r)
	return r.Matched, r.Text, err
}

// TabCreate opens a new tab and returns its number and root pane id.
func (c *Client) TabCreate(p TabCreateParams) (TabCreateResult, error) {
	var r TabCreateResult
	err := c.Call(MethodTabCreate, p, &r)
	return r, err
}

// ChatSend posts one user turn to cats' own chat panel — the "ask cats about
// this selection" path, which reaches the host's agent rather than ced's.
func (c *Client) ChatSend(text string) error {
	return c.Call(MethodChatSend, struct {
		Text string `json:"text"`
	}{text}, nil)
}

// ConfigGet reads the host's configuration; ced uses the theme section.
func (c *Client) ConfigGet() (ConfigGetResult, error) {
	var r ConfigGetResult
	err := c.Call(MethodConfigGet, nil, &r)
	return r, err
}

// ClipboardData is clipboard.read's payload. Truncated marks a clipboard the
// host cut at its own size cap (4 MiB), on a whole-rune boundary — the text is
// usable, it is just not all of it.
type ClipboardData struct {
	Text      string `json:"text"`
	Truncated bool   `json:"truncated,omitempty"`
}

// ClipboardRead returns the HOST machine's system clipboard — the box catway
// runs on, read there with pbpaste/wl-paste/xclip.
//
// It is the one thing in this package with no Tier-0 equivalent whatsoever.
// OSC 52 lets a pane program WRITE the clipboard and is write-only by design
// (a terminal that answered reads would let anything that can print bytes
// exfiltrate it), so until cats grew this verb an editor inside a pane could
// put text on the clipboard and never, by any route, read it back.
//
// THE HOST CLIPBOARD IS NOT ALWAYS THE USER'S CLIPBOARD. ced's own copy goes
// out over OSC 52, which cats delivers to the BROWSER's clipboard — the same
// machine as the server in the mac app and in any local session, but a
// different machine entirely when the browser is on a laptop and catway is on a
// server. Callers must therefore treat this as an explicitly-requested read of
// a named place, never as an ambient "what did the user copy?" — which is why
// nothing here refreshes ced's internal clipboard behind the user's back.
//
// An empty string with no error is an empty clipboard, which is an ordinary
// answer rather than a failure.
func (c *Client) ClipboardRead() (ClipboardData, error) {
	var d ClipboardData
	err := c.Call(MethodClipboardRead, nil, &d)
	return d, err
}

// PathList lists a directory server-side and, when recents is set, returns
// the host's frecency-ranked recent directories — the list ced merges into
// its own recent-folders picker. dir "" means the addressed pane's live cwd
// (nil pane = the focused pane), which is the same neighbour inheritance new
// tabs and splits use.
func (c *Client) PathList(dir string, pane *uint32, recents bool) (PathListResult, error) {
	var r PathListResult
	err := c.Call(MethodPathList, struct {
		Dir     string  `json:"dir,omitempty"`
		Pane    *uint32 `json:"pane,omitempty"`
		Recents bool    `json:"recents,omitempty"`
	}{dir, pane, recents}, &r)
	return r, err
}
