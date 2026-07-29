// =============================================================================
// File: internal/app/chatagent_test.go
// Author: Rohan Allison <rohanthewiz@gmail.com>
// Created: 2026-07-29
// Copyright: 2026 Rohan Allison. All rights reserved.
// =============================================================================

// Tests for the chat-backend registry and switcher (chatagent.go): the
// resolve/fallback rules, the per-backend "copilot" gate, the switch's
// teardown-and-restart semantics, and the connection-generation guard
// that keeps a torn-down agent's stragglers out of the fresh one's
// state. Same injection setup as copilot_chat_test.go — newTestApp pins
// chatLookPath to "never found", so nothing here can spawn a process.

package app

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestChatAgents_RegistryShape smoke-tests the registry: Copilot is the
// first (default) entry — zero-value Apps depend on that — and every
// entry has the fields the UI and spawner require, with no duplicate
// ids that would make the config value ambiguous.
func TestChatAgents_RegistryShape(t *testing.T) {
	agents := chatAgents()
	if len(agents) == 0 || agents[0].id != chatAgentCopilotID {
		t.Fatalf("first registry entry must be %q, got %+v", chatAgentCopilotID, agents)
	}
	seen := map[string]bool{}
	for _, def := range agents {
		if def.id == "" || def.name == "" || def.binary == "" {
			t.Errorf("registry entry missing required fields: %+v", def)
		}
		if seen[def.id] {
			t.Errorf("duplicate agent id %q", def.id)
		}
		seen[def.id] = true
	}
}

// TestChatAgentByID_FallsBackToDefault pins the stale-preference rule:
// a known id resolves to its entry, while "" or an id from another ced
// version quietly lands on the default backend instead of breaking
// startup.
func TestChatAgentByID_FallsBackToDefault(t *testing.T) {
	if got := chatAgentByID("claude"); got.name != "Claude Code" {
		t.Errorf("claude resolved to %+v", got)
	}
	for _, id := range []string{"", "no-such-agent"} {
		if got := chatAgentByID(id); got.id != chatAgentCopilotID {
			t.Errorf("chatAgentByID(%q) = %q, want default", id, got.id)
		}
	}
}

// TestChatAgent_ZeroValueIsCopilot pins the read-path mapping: an App
// built by hand (every test, before any config seed) reads the default
// backend, so pre-registry behavior is unchanged.
func TestChatAgent_ZeroValueIsCopilot(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	if got := a.chatAgent(); got.id != chatAgentCopilotID || got.name != "Copilot" {
		t.Errorf("zero-value agent = %+v, want Copilot default", got)
	}
}

// TestChatAgentEnabled_GatesOnlyCopilot pins the kill-switch scope: the
// "copilot" config key gates the Copilot backend and nothing else — for
// any other agent the binary on PATH is the whole opt-in.
func TestChatAgentEnabled_GatesOnlyCopilot(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	a.copilot.enabled = false
	if a.chatAgentEnabled() {
		t.Error("Copilot backend must honor the disabled kill switch")
	}
	a.chat.agent = chatAgentByID("claude")
	if !a.chatAgentEnabled() {
		t.Error("a non-Copilot backend must ignore the Copilot kill switch")
	}
}

// TestMenuChatAgent_ListsAllWithRestartRow pins the picker contents:
// every registry entry is a row, and — unlike the model picker — the
// CURRENT agent stays listed, annotated as a restart, because re-picking
// it is the deliberate crash-retry gesture for non-Copilot backends.
func TestMenuChatAgent_ListsAllWithRestartRow(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	a.menuChatAgent()
	pm, ok := a.modal.(*paletteModal)
	if !ok {
		t.Fatalf("modal = %T, want picker", a.modal)
	}
	if len(pm.items) != len(chatAgents()) {
		t.Fatalf("picker rows = %d, want %d", len(pm.items), len(chatAgents()))
	}
	labels := make([]string, 0, len(pm.items))
	for _, it := range pm.items {
		labels = append(labels, it.label)
	}
	joined := strings.Join(labels, "|")
	if !strings.Contains(joined, "Copilot  (current — restart)") {
		t.Errorf("current agent should carry the restart annotation, rows = %q", joined)
	}
	if !strings.Contains(joined, "Claude Code") {
		t.Errorf("registry entries missing from picker, rows = %q", joined)
	}
	if !strings.Contains(pm.title, "current: Copilot") {
		t.Errorf("title should name the current agent, got %q", pm.title)
	}
}

// TestChatSetAgent_SwitchTearsDownAndPersists pins the switch: the old
// connection closes, the dead verdict clears (picking IS the retry
// gesture), the transcript notes the new backend, and the choice lands
// in config.json. The transcript itself must survive — same contract as
// any disconnect.
func TestChatSetAgent_SwitchTearsDownAndPersists(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	a := newTestApp(t, t.TempDir())
	fake := wireChat(a)
	a.chat.dead = true // a stale verdict the switch must clear
	a.chatAppendMsg(chatMsg{role: chatRoleUser, text: "earlier conversation"})

	a.chatSetAgent(chatAgentByID("claude"))
	if a.chat.client != nil || !fake.closed {
		t.Fatalf("switch must close the old connection: client=%v closed=%v", a.chat.client, fake.closed)
	}
	if a.chat.dead {
		t.Error("switching agents must clear the dead verdict")
	}
	if a.chatAgent().name != "Claude Code" {
		t.Errorf("active agent = %q", a.chatAgent().name)
	}
	if len(a.chat.msgs) < 2 || !strings.Contains(a.chat.msgs[len(a.chat.msgs)-1].text, "Claude Code") {
		t.Error("transcript should note the new backend")
	}
	if a.chat.msgs[0].text != "earlier conversation" {
		t.Error("transcript must survive the switch")
	}
	data, err := os.ReadFile(filepath.Join(os.Getenv("XDG_CONFIG_HOME"), "ced", "config.json"))
	if err != nil {
		t.Fatalf("config not written: %v", err)
	}
	if !strings.Contains(string(data), `"chatagent": "claude"`) {
		t.Errorf("persisted config = %s", data)
	}
}

// TestChatSetAgent_RestartDoesNotPersist pins the same-agent pick: it
// restarts (teardown + dead cleared + transcript note) but writes no
// config — re-picking what's already chosen is a retry, not a change.
func TestChatSetAgent_RestartDoesNotPersist(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	a := newTestApp(t, t.TempDir())
	fake := wireChat(a)
	a.chat.dead = true

	a.chatSetAgent(a.chatAgent())
	if !fake.closed || a.chat.dead {
		t.Fatalf("restart must close the connection and clear dead: closed=%v dead=%v", fake.closed, a.chat.dead)
	}
	if len(a.chat.msgs) == 0 || !strings.Contains(a.chat.msgs[len(a.chat.msgs)-1].text, "Restarting") {
		t.Error("transcript should note the restart")
	}
	if _, err := os.Stat(filepath.Join(os.Getenv("XDG_CONFIG_HOME"), "ced", "config.json")); !os.IsNotExist(err) {
		t.Errorf("a restart must not write config, stat err = %v", err)
	}
}

// TestChatEnsureStarted_MissingBinaryMarksDead pins the silent-
// degradation path through the stubbed resolver: with the binary
// unresolvable (newTestApp's chatLookPath), a start attempt settles
// into dead without spawning anything or writing the transcript.
func TestChatEnsureStarted_MissingBinaryMarksDead(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	a.copilot.enabled = true
	a.chat.dead = false

	a.chatEnsureStarted()
	if !a.chat.dead || a.chat.starting {
		t.Fatalf("missing binary: dead=%v starting=%v, want dead", a.chat.dead, a.chat.starting)
	}
	if len(a.chat.msgs) != 0 {
		t.Error("silent degradation must not write the transcript")
	}
}

// TestChatConnSeq_DropsStaleEvents pins the generation guard: after a
// switch (which bumps connSeq via disconnect), the old connection's
// ready, exit, and turn-done events must all be ignored — a stale ready
// would install the OLD agent's client under the new agent's name, and
// a stale exit would mark the fresh agent dead.
func TestChatConnSeq_DropsStaleEvents(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	wireChat(a)
	staleSeq := a.chat.connSeq
	a.chatSetAgent(chatAgentByID("claude")) // bumps connSeq

	stale := &fakeCopilotConn{}
	a.handleChatReady(&chatReadyEvent{when: time.Now(), seq: staleSeq, client: stale, sessionID: "old"})
	if a.chat.client != nil || a.chat.sessionID == "old" {
		t.Fatal("stale ready must not install its connection")
	}
	if !stale.closed {
		t.Error("stale ready's client must be closed, not leaked")
	}

	msgsBefore := len(a.chat.msgs)
	a.handleChatExit(&chatExitEvent{when: time.Now(), seq: staleSeq, err: errors.New("old agent died")})
	if a.chat.dead {
		t.Error("stale exit must not mark the new agent dead")
	}
	a.handleChatTurnDone(&chatTurnDoneEvent{when: time.Now(), seq: staleSeq, err: errors.New("connection closed")})
	if len(a.chat.msgs) != msgsBefore {
		t.Error("stale events must not write the transcript")
	}
}

// TestHandleChatExit_AgentNamedReasonAndAuthHint pins the per-backend
// failure story: the transcript names the active agent, and a
// non-Copilot backend always gets its auth hint — its sign-in state is
// invisible to ced, and auth is the overwhelmingly likely handshake
// failure — while Copilot's hint stays suppressed once signed in.
func TestHandleChatExit_AgentNamedReasonAndAuthHint(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	a.chat.agent = chatAgentByID("claude")
	a.copilot.signedIn = true // Copilot auth state must not gate other agents

	a.handleChatExit(&chatExitEvent{when: time.Now(), seq: a.chat.connSeq, err: errors.New("boom")})
	if !a.chat.dead {
		t.Fatal("a current-generation exit must mark the chat dead")
	}
	joined := ""
	for _, m := range a.chat.msgs {
		joined += m.text + "\n"
	}
	if !strings.Contains(joined, "Claude Code chat failed: boom") {
		t.Errorf("failure line should name the agent, transcript = %q", joined)
	}
	if !strings.Contains(joined, "ANTHROPIC_API_KEY") {
		t.Errorf("auth hint missing, transcript = %q", joined)
	}
}

// TestMenuToggleCopilot_LeavesOtherBackendAlone pins the kill-switch
// scope on the toggle itself: with a non-Copilot backend active,
// disabling Copilot tears down the completion sidecar but must not
// touch the chat panel — that agent is none of the toggle's business.
func TestMenuToggleCopilot_LeavesOtherBackendAlone(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	a := newTestApp(t, t.TempDir())
	fake := wireChat(a)
	a.chat.agent = chatAgentByID("claude")
	a.chat.open = true
	a.copilot.dead = true // keep copilotEnsureStarted from spawning anything

	a.menuToggleCopilot() // on → off
	if !a.chat.open || a.chat.client == nil || fake.closed {
		t.Fatalf("non-Copilot chat must survive the toggle: open=%v client=%v closed=%v",
			a.chat.open, a.chat.client, fake.closed)
	}

	a.chat.dead = true
	a.menuToggleCopilot() // off → on
	if !a.chat.dead {
		t.Error("re-enabling Copilot must not clear a non-Copilot backend's dead verdict")
	}
}
