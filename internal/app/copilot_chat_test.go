// =============================================================================
// File: internal/app/copilot_chat_test.go
// Author: Rohan Allison <rohanthewiz@gmail.com>
// Created: 2026-07-22
// Copyright: 2026 Rohan Allison. All rights reserved.
// =============================================================================

// Tests for the Copilot chat panel (copilot_chat.go). A real
// copilot-language-server --acp is never spawned — newTestApp marks the
// integration dead, and these tests inject fakeCopilotConn (the chat
// layer deliberately shares the sidecar's conn interface). Prompt turns
// that hop through a goroutine are asserted with the same bounded wait
// the sidecar tests use.

package app

import (
	"bufio"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gdamore/tcell/v2"

	"github.com/rohanthewiz/r-ed/internal/lsp"
)

// wireChat installs a live fake chat connection, bypassing the async
// start — the injection twin of the sidecar tests' handleCopilotReady
// path.
func wireChat(a *App) *fakeCopilotConn {
	fake := &fakeCopilotConn{}
	a.copilot.enabled = true
	a.chat.dead = false
	a.chat.client = fake
	a.chat.sessionID = "sess-1"
	return fake
}

// TestChatToggleLabel pins the flip-in-place menu label.
func TestChatToggleLabel(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	if got := a.chatToggleLabel(); got != "Show Copilot chat" {
		t.Errorf("closed label = %q", got)
	}
	a.chat.open = true
	if got := a.chatToggleLabel(); got != "Hide Copilot chat" {
		t.Errorf("open label = %q", got)
	}
}

// TestMenuToggleChat_OpensAndExplains drives the three open outcomes:
// Copilot disabled flashes the dependency, a dead agent flashes the
// install hint, and a healthy state opens focused — never a silently
// dimmed dead end (the menuCopilotAuth rule).
func TestMenuToggleChat_OpensAndExplains(t *testing.T) {
	a := newTestApp(t, t.TempDir())

	a.copilot.enabled = false
	a.menuToggleChat()
	if a.chat.open || !strings.Contains(a.statusMsg, "Copilot is disabled") {
		t.Fatalf("disabled: open=%v flash=%q", a.chat.open, a.statusMsg)
	}

	a.copilot.enabled = true
	a.chat.dead = true // newTestApp default, restated for clarity
	a.menuToggleChat()
	if a.chat.open || !strings.Contains(a.statusMsg, "unavailable") {
		t.Fatalf("dead: open=%v flash=%q", a.chat.open, a.statusMsg)
	}

	wireChat(a)
	a.menuToggleChat()
	if !a.chat.open || !a.chat.focused {
		t.Fatalf("healthy: open=%v focused=%v", a.chat.open, a.chat.focused)
	}
	// Toggling again hides without tearing the session down.
	a.menuToggleChat()
	if a.chat.open || a.chat.client == nil {
		t.Fatalf("hide: open=%v client=%v", a.chat.open, a.chat.client)
	}
}

// TestChatLeftEdgeSingleOccupancy pins the eviction in both directions:
// opening chat closes a LEFT-docked terminal (bottom-docked coexists),
// and re-opening the left-docked terminal evicts the chat.
func TestChatLeftEdgeSingleOccupancy(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	wireChat(a)

	// Chat evicts the left-docked terminal…
	a.termDockLeft = true
	a.term.open = true
	a.menuToggleChat()
	if !a.chat.open || a.term.open {
		t.Fatalf("chat open should evict left terminal: chat=%v term=%v", a.chat.open, a.term.open)
	}

	// …and the terminal takes the edge back.
	a.menuToggleTerminal()
	if !a.term.open || a.chat.open {
		t.Fatalf("left terminal should evict chat: chat=%v term=%v", a.chat.open, a.term.open)
	}

	// A bottom-docked terminal coexists with the chat strip.
	a.term.open = false
	a.termDockLeft = false
	a.menuToggleChat()
	a.menuToggleTerminal()
	if !a.chat.open || !a.term.open {
		t.Fatalf("bottom terminal should coexist: chat=%v term=%v", a.chat.open, a.term.open)
	}
}

// TestChatDockFlipEvictsChat pins the other reclaim path: flipping the
// terminal dock to the left (which force-opens the terminal there)
// takes the edge from an open chat panel.
func TestChatDockFlipEvictsChat(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	wireChat(a)
	a.menuToggleChat()
	if !a.chat.open {
		t.Fatal("setup: chat should be open")
	}
	a.menuToggleTermDock() // bottom → left, opens the terminal there
	if !a.termDockLeft || !a.term.open || a.chat.open {
		t.Fatalf("dock flip: dockLeft=%v term=%v chat=%v", a.termDockLeft, a.term.open, a.chat.open)
	}
}

// TestChatLayoutFlipsTreeRight pins the geometry contract: an open chat
// strip owns the left block, the sidebar flips to the right edge, and
// the strip's splitter sits on its rightmost column.
func TestChatLayoutFlipsTreeRight(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	wireChat(a)
	if a.treeOnRight() {
		t.Fatal("classic layout should keep the tree left")
	}
	a.menuToggleChat()

	if !a.treeOnRight() {
		t.Fatal("open chat should flip the tree right")
	}
	if got := a.leftBlockW(); got != a.chatStripW() {
		t.Errorf("leftBlockW = %d, want chat strip %d", got, a.chatStripW())
	}
	if got := a.rightBlockW(); got != a.sidebarW() {
		t.Errorf("rightBlockW = %d, want sidebar %d", got, a.sidebarW())
	}
	if got := a.chatSplitterX(); got != a.chatStripW()-1 {
		t.Errorf("chatSplitterX = %d, want %d", got, a.chatStripW()-1)
	}
	sx, _, _, _ := a.sidebarRect()
	if sx != a.width-a.sidebarW()+1 {
		t.Errorf("sidebar x = %d, want right-docked %d", sx, a.width-a.sidebarW()+1)
	}
}

// TestResizeChatPanelWidth_Clamps pins the resize band: the strip can't
// shrink below the minimum or starve the editor next to the sidebar.
func TestResizeChatPanelWidth_Clamps(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	wireChat(a)
	a.chat.open = true

	a.resizeChatPanelWidth(1)
	if a.chat.width != chatPanelMinWidth {
		t.Errorf("tiny target: width = %d, want %d", a.chat.width, chatPanelMinWidth)
	}
	a.resizeChatPanelWidth(9999)
	if a.chat.width != a.maxChatPanelWidth() {
		t.Errorf("huge target: width = %d, want %d", a.chat.width, a.maxChatPanelWidth())
	}
}

// TestWrapChatText pins the word-wrapper: greedy wrapping at width,
// paragraph blanks preserved, and over-long words hard-broken instead
// of overflowing the strip.
func TestWrapChatText(t *testing.T) {
	got := wrapChatText("alpha beta gamma", 11)
	want := []string{"alpha beta", "gamma"}
	if len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
		t.Errorf("wrap = %q, want %q", got, want)
	}
	if got := wrapChatText("", 10); len(got) != 1 || got[0] != "" {
		t.Errorf("empty line = %q, want one blank row", got)
	}
	got = wrapChatText("supercalifragilistic", 6)
	if len(got) != 4 || got[0] != "superc" {
		t.Errorf("long word = %q, want 6-cell chunks", got)
	}
}

// TestChatRows_RolesAndFences pins the transcript row derivation: the
// user prompt gets its ❯ gutter, messages are separated by a blank
// row, and fenced blocks in agent prose come back flagged as code with
// their interior hard-wrapped, not word-wrapped.
func TestChatRows_RolesAndFences(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	a.chat.msgs = []chatMsg{
		{role: chatRoleUser, text: "hi"},
		{role: chatRoleAgent, text: "Sure:\n```go\nfmt.Println(1)\n```"},
	}
	rows := a.chatRows(40)

	if rows[0].text != "❯ hi" || rows[0].role != chatRoleUser {
		t.Errorf("row 0 = %+v, want the gutter-prefixed prompt", rows[0])
	}
	if rows[1].text != "" {
		t.Errorf("row 1 = %+v, want the blank separator", rows[1])
	}
	var codeRows []string
	for _, r := range rows {
		if r.code {
			codeRows = append(codeRows, r.text)
		}
	}
	want := []string{"```go", "fmt.Println(1)", "```"}
	if len(codeRows) != len(want) {
		t.Fatalf("code rows = %q, want %q", codeRows, want)
	}
	for i := range want {
		if codeRows[i] != want[i] {
			t.Errorf("code row %d = %q, want %q", i, codeRows[i], want[i])
		}
	}
}

// TestChatSend_WireShape drives Enter through the real key path and
// asserts the session/prompt payload: the session id and the typed
// text as one ACP text block.
func TestChatSend_WireShape(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	fake := wireChat(a)
	a.chat.open = true
	a.chat.focused = true
	typeChatText(a, "hello agent")
	a.handleChatKey(enterKey())

	waitForCopilot(t, "session/prompt call", func() bool { return fake.called("session/prompt") })
	if !a.chat.turnActive {
		t.Error("turnActive should be set while the prompt is in flight")
	}
	params := fake.paramsFor("session/prompt")
	var p struct {
		SessionID string `json:"sessionId"`
		Prompt    []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"prompt"`
	}
	if err := json.Unmarshal(params[len(params)-1], &p); err != nil {
		t.Fatalf("params: %v", err)
	}
	if p.SessionID != "sess-1" || len(p.Prompt) != 1 || p.Prompt[0].Type != "text" || p.Prompt[0].Text != "hello agent" {
		t.Errorf("prompt params = %+v", p)
	}
	// The prompt is echoed into the transcript and the input cleared.
	if len(a.chat.msgs) == 0 || a.chat.msgs[0].text != "hello agent" || a.chat.msgs[0].role != chatRoleUser {
		t.Errorf("transcript echo missing: %+v", a.chat.msgs)
	}
	if a.chat.input.String() != "" {
		t.Errorf("input not cleared: %q", a.chat.input.String())
	}
}

// TestChatSend_QueuesWhileStarting pins the first-Enter race: a prompt
// submitted mid-handshake is queued, and handleChatReady sends it the
// moment the session exists — never silently dropped.
func TestChatSend_QueuesWhileStarting(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	a.copilot.enabled = true
	a.chat.dead = false
	a.chat.starting = true
	a.chat.open = true
	typeChatText(a, "early bird")
	a.handleChatKey(enterKey())

	if a.chat.queuedPrompt != "early bird" {
		t.Fatalf("queuedPrompt = %q", a.chat.queuedPrompt)
	}
	fake := &fakeCopilotConn{}
	a.handleChatReady(&chatReadyEvent{when: time.Now(), client: fake, sessionID: "sess-2"})
	waitForCopilot(t, "queued prompt sent", func() bool { return fake.called("session/prompt") })
	if a.chat.queuedPrompt != "" {
		t.Errorf("queue not drained: %q", a.chat.queuedPrompt)
	}
}

// TestChatUpdate_StreamsChunks pins the streaming merge: consecutive
// agent_message_chunks extend ONE transcript message, a tool_call adds
// its muted one-liner, and updates for a stale session are dropped.
func TestChatUpdate_StreamsChunks(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	wireChat(a)

	update := func(sid, body string) *chatUpdateEvent {
		return &chatUpdateEvent{when: time.Now(), sessionID: sid, update: json.RawMessage(body)}
	}
	a.handleChatUpdate(update("sess-1", `{"sessionUpdate":"agent_message_chunk","content":{"type":"text","text":"Hel"}}`))
	a.handleChatUpdate(update("sess-1", `{"sessionUpdate":"agent_message_chunk","content":{"type":"text","text":"lo"}}`))
	if len(a.chat.msgs) != 1 || a.chat.msgs[0].text != "Hello" || a.chat.msgs[0].role != chatRoleAgent {
		t.Fatalf("chunk merge: %+v", a.chat.msgs)
	}

	a.handleChatUpdate(update("sess-1", `{"sessionUpdate":"tool_call","title":"Search the web"}`))
	if len(a.chat.msgs) != 2 || a.chat.msgs[1].text != "⚙ Search the web" || a.chat.msgs[1].role != chatRoleTool {
		t.Fatalf("tool line: %+v", a.chat.msgs)
	}

	a.handleChatUpdate(update("old-session", `{"sessionUpdate":"agent_message_chunk","content":{"type":"text","text":"ghost"}}`))
	if len(a.chat.msgs) != 2 {
		t.Fatalf("stale-session update should be dropped: %+v", a.chat.msgs)
	}
}

// TestChatTurnDone pins the turn endings: an error surfaces as an info
// line, a cancel gets its marker, and a clean end adds nothing — the
// streamed answer is the feedback.
func TestChatTurnDone(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	wireChat(a)

	a.chat.turnActive = true
	a.handleChatTurnDone(&chatTurnDoneEvent{when: time.Now(), stopReason: "end_turn"})
	if a.chat.turnActive || len(a.chat.msgs) != 0 {
		t.Fatalf("clean end: active=%v msgs=%+v", a.chat.turnActive, a.chat.msgs)
	}

	a.chat.turnActive = true
	a.handleChatTurnDone(&chatTurnDoneEvent{when: time.Now(), stopReason: "cancelled"})
	if len(a.chat.msgs) != 1 || a.chat.msgs[0].text != "— stopped" {
		t.Fatalf("cancel marker: %+v", a.chat.msgs)
	}

	a.chat.turnActive = true
	a.handleChatTurnDone(&chatTurnDoneEvent{when: time.Now(), err: errors.New("boom")})
	if len(a.chat.msgs) != 2 || !strings.Contains(a.chat.msgs[1].text, "boom") {
		t.Fatalf("error line: %+v", a.chat.msgs)
	}
}

// TestChatInterrupt pins the ⏹ contract: one session/cancel per turn,
// carrying the session id, and a no-op while idle.
func TestChatInterrupt(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	fake := wireChat(a)

	a.chatInterrupt() // idle — nothing to cancel
	if fake.notified("session/cancel") {
		t.Fatal("idle interrupt should not notify")
	}

	a.chat.turnActive = true
	a.chatInterrupt()
	a.chatInterrupt() // second press within the same turn is dropped
	if got := len(fake.paramsFor("session/cancel")); got != 1 {
		t.Fatalf("cancel notifications = %d, want 1", got)
	}
	var p struct {
		SessionID string `json:"sessionId"`
	}
	_ = json.Unmarshal(fake.paramsFor("session/cancel")[0], &p)
	if p.SessionID != "sess-1" {
		t.Errorf("cancel sessionId = %q", p.SessionID)
	}
}

// TestChatExit_SurfacesReason pins the failure transparency rule: a
// handshake error lands in the transcript with the sign-in hint when
// the user isn't signed in, and the integration is dead afterwards.
func TestChatExit_SurfacesReason(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	wireChat(a)
	a.copilot.signedIn = false

	a.handleChatExit(&chatExitEvent{when: time.Now(), err: errors.New("auth required")})
	if !a.chat.dead || a.chat.client != nil {
		t.Fatalf("exit should kill the integration: dead=%v client=%v", a.chat.dead, a.chat.client)
	}
	if len(a.chat.msgs) != 2 ||
		!strings.Contains(a.chat.msgs[0].text, "auth required") ||
		!strings.Contains(a.chat.msgs[1].text, "Sign in") {
		t.Fatalf("transcript = %+v, want error + sign-in hint", a.chat.msgs)
	}
}

// TestChatAutoRejectPermission pins the phase-3 scope guard: the
// agent's own reject_once option is chosen when present, and the
// cancelled outcome is the fallback when it isn't.
func TestChatAutoRejectPermission(t *testing.T) {
	params := json.RawMessage(`{
		"toolCall": {"title": "Edit main.go"},
		"options": [
			{"optionId": "ok", "kind": "allow_once"},
			{"optionId": "no", "kind": "reject_once"}
		]}`)
	res, title := chatAutoRejectPermission(params)
	if title != "Edit main.go" {
		t.Errorf("title = %q", title)
	}
	b, _ := json.Marshal(res)
	if !strings.Contains(string(b), `"optionId":"no"`) {
		t.Errorf("outcome = %s, want the reject_once option", b)
	}

	res, _ = chatAutoRejectPermission(json.RawMessage(`{"options":[{"optionId":"ok","kind":"allow_once"}]}`))
	b, _ = json.Marshal(res)
	if !strings.Contains(string(b), `"cancelled"`) {
		t.Errorf("no-reject fallback = %s, want cancelled", b)
	}
}

// TestChatHistoryMove pins the readline behavior on the composer: Up
// recalls the previous prompt, Down walks back to the stashed draft.
func TestChatHistoryMove(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	a.chat.history = []string{"first", "second"}
	a.chat.histIdx = 2
	typeChatText(a, "draft")

	a.chatHistoryMove(-1)
	if a.chat.input.String() != "second" {
		t.Errorf("up = %q, want second", a.chat.input.String())
	}
	a.chatHistoryMove(-1)
	if a.chat.input.String() != "first" {
		t.Errorf("up up = %q, want first", a.chat.input.String())
	}
	a.chatHistoryMove(1)
	a.chatHistoryMove(1)
	if a.chat.input.String() != "draft" {
		t.Errorf("back down = %q, want the stashed draft", a.chat.input.String())
	}
}

// TestChatPanelPress pins the mouse contract: ✕ closes, a body click
// focuses the composer and steals focus from the terminal.
func TestChatPanelPress(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	wireChat(a)
	a.chat.open = true
	a.term.focused = true

	px, py, pw, ph := a.chatPanelRect()
	a.chatPanelPress(px+1, py+ph/2)
	if !a.chat.focused || a.term.focused {
		t.Fatalf("body click: chat=%v term=%v", a.chat.focused, a.term.focused)
	}

	c := a.chatCloseRect()
	a.chatPanelPress(c.x+1, c.y)
	if a.chat.open {
		t.Fatal("✕ should close the panel")
	}
	_ = pw
}

// TestMenuToggleCopilot_TearsDownChat pins the shared kill switch:
// disabling Copilot closes the chat connection AND the panel, and
// re-enabling clears the chat's dead verdict for a fresh start.
func TestMenuToggleCopilot_TearsDownChat(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	fake := wireChat(a)
	a.chat.open = true
	a.copilot.dead = true // keep copilotEnsureStarted from spawning anything

	a.menuToggleCopilot() // on → off
	if a.chat.open || a.chat.client != nil || !fake.closed {
		t.Fatalf("disable: open=%v client=%v closed=%v", a.chat.open, a.chat.client, fake.closed)
	}

	a.chat.dead = true
	a.menuToggleCopilot() // off → on
	if a.chat.dead {
		t.Error("re-enable should clear the chat dead verdict")
	}
	// The eager start marked copilot dead again (no binary lookup in
	// tests matters here); the point is chat.dead cleared before it.
	a.copilot.enabled = false
}

// TestDrawChatPanel_Smoke renders the open panel on the simulation
// screen and checks the header title landed on row 0 — the whole
// paint path (header, transcript wrap, input row, splitter) at once.
func TestDrawChatPanel_Smoke(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	wireChat(a)
	a.menuToggleChat()
	a.chat.msgs = []chatMsg{{role: chatRoleAgent, text: "Hello from Copilot"}}
	a.draw()
	scr := a.screen.(tcell.SimulationScreen)
	scr.Show()

	cells, w, _ := scr.GetContents()
	var header strings.Builder
	for x := 0; x < a.chatStripW(); x++ {
		c := cells[0*w+x]
		if len(c.Runes) > 0 {
			header.WriteRune(c.Runes[0])
		}
	}
	if !strings.Contains(header.String(), "Copilot chat") {
		t.Errorf("header row = %q, want the panel title", header.String())
	}

	// Once a session names its model, the header carries it.
	a.chat.models = chatTestModels()
	a.chat.modelID = "gpt-5.5"
	a.draw()
	scr.Show()
	cells, w, _ = scr.GetContents()
	header.Reset()
	for x := 0; x < a.chatStripW(); x++ {
		c := cells[0*w+x]
		if len(c.Runes) > 0 {
			header.WriteRune(c.Runes[0])
		}
	}
	if !strings.Contains(header.String(), "Copilot - GPT-5.5") {
		t.Errorf("header row = %q, want the current model named", header.String())
	}
}

// TestChatHeaderTitle pins the header's two states: the current model
// once the session reports one, the neutral title otherwise (including
// when the id isn't in the roster — never a raw wire id).
func TestChatHeaderTitle(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	if got := a.chatHeaderTitle(); got != " Copilot chat " {
		t.Errorf("no-session title = %q", got)
	}
	a.chat.models = chatTestModels()
	a.chat.modelID = "claude-sonnet-4.6"
	if got := a.chatHeaderTitle(); got != " Copilot - Claude Sonnet 4.6 " {
		t.Errorf("resolved title = %q", got)
	}
	a.chat.modelID = "gone"
	if got := a.chatHeaderTitle(); got != " Copilot chat " {
		t.Errorf("unknown-id title = %q", got)
	}
}

// TestChatFitHeader pins the clip: text that fits is untouched, an
// overlong title is ellipsized to exactly the budget, and a budget too
// small to say anything yields "" rather than spilling under the
// ⏹/✕ buttons.
func TestChatFitHeader(t *testing.T) {
	if got := chatFitHeader(" Copilot ", 20); got != " Copilot " {
		t.Errorf("fitting text = %q", got)
	}
	got := chatFitHeader(" Copilot - Claude Sonnet 4.6 ", 12)
	if runeLen(got) != 12 || !strings.HasSuffix(got, "…") {
		t.Errorf("clipped title = %q (len %d)", got, runeLen(got))
	}
	if got := chatFitHeader(" Copilot ", 1); got != "" {
		t.Errorf("no-room title = %q, want empty", got)
	}
}

// typeChatText feeds runes through the composer's real key handler.
func typeChatText(a *App, s string) {
	for _, r := range s {
		a.chat.input.handleKey(tcell.NewEventKey(tcell.KeyRune, r, 0))
	}
}

// enterKey builds the Enter keystroke used by the send tests.
func enterKey() *tcell.EventKey {
	return tcell.NewEventKey(tcell.KeyEnter, 0, 0)
}

// chatTestModels seeds a small roster for the model-selection tests.
func chatTestModels() []chatModel {
	return []chatModel{
		{id: "auto", name: "Auto"},
		{id: "claude-sonnet-4.6", name: "Claude Sonnet 4.6", usage: "1x"},
		{id: "gpt-5.5", name: "GPT-5.5", usage: "7.5x"},
	}
}

// TestChatModelLabel pins the ≡ row's two states: the resolved model
// name while a session knows it, a neutral opener otherwise — the
// label must never show a raw wire id.
func TestChatModelLabel(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	if got := a.chatModelLabel(); got != "Chat model…" {
		t.Errorf("no-session label = %q", got)
	}
	a.chat.models = chatTestModels()
	a.chat.modelID = "claude-sonnet-4.6"
	if got := a.chatModelLabel(); got != "Chat model: Claude Sonnet 4.6" {
		t.Errorf("resolved label = %q", got)
	}
	// An id missing from the roster falls back to the opener rather
	// than showing a stale or empty name.
	a.chat.modelID = "gone"
	if got := a.chatModelLabel(); got != "Chat model…" {
		t.Errorf("unknown-id label = %q", got)
	}
}

// TestMenuChatModel_ExplainsUnavailability pins the always-clickable
// contract: disabled and dead states flash WHY instead of silently
// doing nothing.
func TestMenuChatModel_ExplainsUnavailability(t *testing.T) {
	a := newTestApp(t, t.TempDir())

	a.copilot.enabled = false
	a.menuChatModel()
	if !strings.Contains(a.statusMsg, "Copilot is disabled") {
		t.Errorf("disabled flash = %q", a.statusMsg)
	}

	a.copilot.enabled = true
	a.chat.dead = true
	a.menuChatModel()
	if !strings.Contains(a.statusMsg, "unavailable") {
		t.Errorf("dead flash = %q", a.statusMsg)
	}
}

// TestMenuChatModel_QueuesWhileStarting pins the queued-intent path:
// a click before the agent is up must not vanish — it arms
// modelPickWanted so handleChatReady opens the picker.
func TestMenuChatModel_QueuesWhileStarting(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	a.copilot.enabled = true
	a.chat.dead = false
	a.chat.starting = true // async start in flight — ensureStarted no-ops

	a.menuChatModel()
	if !a.chat.modelPickWanted {
		t.Fatal("click while starting should queue the picker")
	}
	if !strings.Contains(a.statusMsg, "starting") {
		t.Errorf("queue flash = %q", a.statusMsg)
	}
}

// TestHandleChatReady_StoresModelsAndOpensQueuedPicker pins the ready
// handler's model leg: the roster and current id land on chat state,
// and a queued picker request opens the picker exactly once.
func TestHandleChatReady_StoresModelsAndOpensQueuedPicker(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	a.copilot.enabled = true
	a.chat.dead = false
	a.chat.modelPickWanted = true

	fake := &fakeCopilotConn{}
	a.handleChatReady(&chatReadyEvent{when: time.Now(), client: fake,
		sessionID: "sess-1", models: chatTestModels(), modelID: "auto"})

	if len(a.chat.models) != 3 || a.chat.modelID != "auto" {
		t.Fatalf("model state not installed: %v / %q", a.chat.models, a.chat.modelID)
	}
	if a.chat.modelPickWanted {
		t.Error("modelPickWanted should be consumed")
	}
	pm, ok := a.modal.(*paletteModal)
	if !ok {
		t.Fatalf("queued pick should open the picker, modal = %T", a.modal)
	}
	if !strings.Contains(pm.title, "Chat model") {
		t.Errorf("picker title = %q", pm.title)
	}
}

// TestOpenChatModelPicker_ExcludesCurrentAndShowsUsage pins the picker
// rows: the current model is left out (menuGitSwitchBranch's no-op-row
// rule) and each row carries the premium-request multiplier so a pick
// is an informed spend.
func TestOpenChatModelPicker_ExcludesCurrentAndShowsUsage(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	wireChat(a)
	a.chat.models = chatTestModels()
	a.chat.modelID = "auto"

	a.openChatModelPicker()
	pm, ok := a.modal.(*paletteModal)
	if !ok {
		t.Fatalf("modal = %T, want picker", a.modal)
	}
	labels := make([]string, 0, len(pm.items))
	for _, it := range pm.items {
		labels = append(labels, it.label)
	}
	joined := strings.Join(labels, "|")
	if strings.Contains(joined, "Auto") {
		t.Errorf("current model should be excluded, rows = %q", joined)
	}
	if !strings.Contains(joined, "GPT-5.5  (7.5x)") {
		t.Errorf("usage multiplier missing, rows = %q", joined)
	}
	if !strings.Contains(pm.title, "current: Auto") {
		t.Errorf("title should name the current model, got %q", pm.title)
	}
}

// TestChatSetModel_SendsSetModel pins the async wire leg: the picked
// model goes out as session/set_model with the live session's id.
func TestChatSetModel_SendsSetModel(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	fake := wireChat(a)

	a.chatSetModel(chatModel{id: "gpt-5.5", name: "GPT-5.5"})
	waitForCopilot(t, "session/set_model call", func() bool { return fake.called("session/set_model") })

	fake.mu.Lock()
	raw := fake.params["session/set_model"]
	fake.mu.Unlock()
	if len(raw) != 1 {
		t.Fatalf("set_model sent %d times", len(raw))
	}
	var p struct {
		SessionID string `json:"sessionId"`
		ModelID   string `json:"modelId"`
	}
	if err := json.Unmarshal(raw[0], &p); err != nil {
		t.Fatalf("params: %v", err)
	}
	if p.SessionID != "sess-1" || p.ModelID != "gpt-5.5" {
		t.Errorf("wire params = %+v", p)
	}
}

// TestHandleChatModelSet covers both endings: success records the new
// model, notes it in the transcript, and persists the preference;
// failure only flashes — the session keeps its previous model.
func TestHandleChatModelSet(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	a := newTestApp(t, t.TempDir())
	wireChat(a)
	a.chat.models = chatTestModels()
	a.chat.modelID = "auto"

	a.handleChatModelSet(&chatModelSetEvent{when: time.Now(),
		model: chatModel{id: "gpt-5.5", name: "GPT-5.5"}})
	if a.chat.modelID != "gpt-5.5" || a.chat.modelPref != "gpt-5.5" {
		t.Fatalf("state after set: id %q pref %q", a.chat.modelID, a.chat.modelPref)
	}
	if len(a.chat.msgs) == 0 || !strings.Contains(a.chat.msgs[len(a.chat.msgs)-1].text, "GPT-5.5") {
		t.Error("transcript should note the switch")
	}
	data, err := os.ReadFile(filepath.Join(os.Getenv("XDG_CONFIG_HOME"), "r-ed", "config.json"))
	if err != nil {
		t.Fatalf("config not written: %v", err)
	}
	if !strings.Contains(string(data), `"chatmodel": "gpt-5.5"`) {
		t.Errorf("persisted config = %s", data)
	}

	msgsBefore := len(a.chat.msgs)
	a.handleChatModelSet(&chatModelSetEvent{when: time.Now(),
		model: chatModel{id: "auto", name: "Auto"}, err: errors.New("nope")})
	if a.chat.modelID != "gpt-5.5" {
		t.Error("failed set must not change the model")
	}
	if len(a.chat.msgs) != msgsBefore {
		t.Error("failed set should not write the transcript")
	}
	if !strings.Contains(a.statusMsg, "failed") {
		t.Errorf("failure flash = %q", a.statusMsg)
	}
}

// TestChatDisconnect_ClearsModelStateKeepsPref pins the reset scope:
// roster and session model die with the connection, but the persisted
// preference survives so the next handshake re-applies it.
func TestChatDisconnect_ClearsModelStateKeepsPref(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	wireChat(a)
	a.chat.models = chatTestModels()
	a.chat.modelID = "gpt-5.5"
	a.chat.modelPref = "gpt-5.5"
	a.chat.modelPickWanted = true

	a.chatDisconnect()
	if a.chat.models != nil || a.chat.modelID != "" || a.chat.modelPickWanted {
		t.Error("attached-only model state should be cleared")
	}
	if a.chat.modelPref != "gpt-5.5" {
		t.Error("modelPref must survive a disconnect")
	}
}

// fakeACPAgent speaks just enough ndjson ACP over pipes to satisfy
// chatInitialize: initialize, session/new (with a fixed roster), and
// session/set_model (recorded; optionally failed). It exists so the
// pref-apply branch is tested against real framing, not the fake conn.
type fakeACPAgent struct {
	mu      sync.Mutex
	setIDs  []string
	failSet bool
}

// serve runs the agent's read loop until the client side closes.
func (f *fakeACPAgent) serve(r io.Reader, w io.Writer) {
	sc := bufio.NewScanner(r)
	for sc.Scan() {
		var req struct {
			ID     int64           `json:"id"`
			Method string          `json:"method"`
			Params json.RawMessage `json:"params"`
		}
		if json.Unmarshal(sc.Bytes(), &req) != nil {
			continue
		}
		var result any
		switch req.Method {
		case "initialize":
			result = map[string]any{"protocolVersion": 1}
		case "session/new":
			result = map[string]any{
				"sessionId": "sess-acp",
				"models": map[string]any{
					"availableModels": []map[string]any{
						{"modelId": "auto", "name": "Auto"},
						{"modelId": "gpt-5.5", "name": "GPT-5.5",
							"_meta": map[string]any{"copilotUsage": "7.5x"}},
					},
					"currentModelId": "auto",
				},
			}
		case "session/set_model":
			var p struct {
				ModelID string `json:"modelId"`
			}
			_ = json.Unmarshal(req.Params, &p)
			f.mu.Lock()
			f.setIDs = append(f.setIDs, p.ModelID)
			fail := f.failSet
			f.mu.Unlock()
			if fail {
				resp, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": req.ID,
					"error": map[string]any{"code": -32000, "message": "nope"}})
				_, _ = w.Write(append(resp, '\n'))
				continue
			}
			result = map[string]any{}
		default:
			continue
		}
		resp, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": req.ID, "result": result})
		_, _ = w.Write(append(resp, '\n'))
	}
}

// startFakeACPAgent wires a fakeACPAgent to a real ndjson lsp.Client.
func startFakeACPAgent(t *testing.T, agent *fakeACPAgent) *lsp.Client {
	t.Helper()
	agentR, cliW := io.Pipe()
	cliR, agentW := io.Pipe()
	go agent.serve(agentR, agentW)
	c := lsp.NewClientACP(cliR, cliW, func(string, json.RawMessage) {},
		func(string, json.RawMessage) (any, error) { return nil, errors.New("unexpected") },
		func(error) {})
	t.Cleanup(c.Close)
	return c
}

// TestChatInitialize_ModelRoster pins the handshake's model handling
// against real ndjson framing: the roster and current id are decoded,
// a saved preference present in the roster is applied via
// session/set_model, and a stale preference is silently skipped.
func TestChatInitialize_ModelRoster(t *testing.T) {
	// No preference: roster decoded, agent default kept, no set call.
	agent := &fakeACPAgent{}
	sess, err := chatInitialize(startFakeACPAgent(t, agent), "/tmp/x", "")
	if err != nil {
		t.Fatalf("handshake: %v", err)
	}
	if sess.id != "sess-acp" || sess.modelID != "auto" || len(sess.models) != 2 {
		t.Fatalf("session = %+v", sess)
	}
	if sess.models[1].usage != "7.5x" {
		t.Errorf("usage multiplier lost: %+v", sess.models[1])
	}
	if len(agent.setIDs) != 0 {
		t.Errorf("no-pref handshake sent set_model %v", agent.setIDs)
	}

	// Saved preference in the roster: applied, session reports it.
	agent = &fakeACPAgent{}
	sess, err = chatInitialize(startFakeACPAgent(t, agent), "/tmp/x", "gpt-5.5")
	if err != nil {
		t.Fatalf("pref handshake: %v", err)
	}
	if sess.modelID != "gpt-5.5" || len(agent.setIDs) != 1 || agent.setIDs[0] != "gpt-5.5" {
		t.Errorf("pref not applied: modelID %q, set calls %v", sess.modelID, agent.setIDs)
	}

	// Stale preference (not in roster): no call, default kept.
	agent = &fakeACPAgent{}
	sess, err = chatInitialize(startFakeACPAgent(t, agent), "/tmp/x", "retired-model")
	if err != nil {
		t.Fatalf("stale-pref handshake: %v", err)
	}
	if sess.modelID != "auto" || len(agent.setIDs) != 0 {
		t.Errorf("stale pref should be skipped: modelID %q, set calls %v", sess.modelID, agent.setIDs)
	}

	// set_model failure: swallowed, agent default kept — a broken pref
	// must never break the handshake.
	agent = &fakeACPAgent{failSet: true}
	sess, err = chatInitialize(startFakeACPAgent(t, agent), "/tmp/x", "gpt-5.5")
	if err != nil {
		t.Fatalf("failing-set handshake: %v", err)
	}
	if sess.modelID != "auto" {
		t.Errorf("failed set should keep the default, got %q", sess.modelID)
	}
}
