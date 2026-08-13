// =============================================================================
// File: internal/app/catsagents_test.go
// Author: Rohan Allison <rohanthewiz@gmail.com>
// Created: 2026-08-12
// Copyright: 2026 Rohan Allison. All rights reserved.
// =============================================================================

package app

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rohanthewiz/ced/internal/cats"
	"github.com/rohanthewiz/ced/internal/editor"
)

// siblingPanes is a stand-in pane.list: two agents, a plain shell, and this
// editor's own pane reporting itself as the agent "ced" (which is exactly
// what the hook reporter makes cats believe).
func siblingPanes() []cats.PaneInfo {
	return []cats.PaneInfo{
		{Pane: 3, Handle: "w1:p1", Agent: "codex", AgentState: cats.StateWorking},
		{Pane: 5, Handle: "w1:p2"}, // a plain shell
		{Pane: 9, Handle: "w1:p9", Agent: "ced", AgentState: cats.StateIdle},
		{Pane: 11, Handle: "w1:p4", Agent: "claude", AgentState: cats.StateBlocked},
	}
}

// The list is filtered and ranked, not forwarded: plain shells are not
// agents, we are not our own collaborator, and the agent worth naming is the
// one that has STOPPED and is waiting for a human.
func TestCatsAgentPanesRanksTheWaitingOneFirst(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	a.cats.self, a.cats.selfOK = 9, true
	a.cats.panes = siblingPanes()

	got := a.catsAgentPanes()

	if len(got) != 2 {
		t.Fatalf("panes = %+v, want the two sibling agents", got)
	}
	if got[0].Agent != "claude" || got[1].Agent != "codex" {
		t.Fatalf("order = %s, %s — blocked outranks working", got[0].Agent, got[1].Agent)
	}
	for _, p := range got {
		if p.Pane == 9 {
			t.Fatal("the editor offered to collaborate with itself")
		}
	}
}

// The status segment names one agent and counts the rest — the bar is a
// strip, not a dashboard — and says nothing at all outside cats.
func TestCatsAgentStatusSegment(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	if a.catsAgentStatusSegment() != "" {
		t.Fatal("a plain terminal has no sibling agents")
	}

	a.cats.self, a.cats.selfOK = 9, true
	a.cats.panes = siblingPanes()
	if got, want := a.catsAgentStatusSegment(), "claude: blocked +1"; got != want {
		t.Fatalf("segment = %q, want %q", got, want)
	}

	// An idle lone agent is just its name: "claude: idle" spends four
	// columns saying nothing.
	a.cats.panes = []cats.PaneInfo{{Pane: 3, Agent: "claude", AgentState: cats.StateIdle}}
	if got := a.catsAgentStatusSegment(); got != "claude" {
		t.Fatalf("segment = %q, want the bare name", got)
	}
}

// And it reaches the drawn bar as a click target, because the fact and the
// verb it suggests ("take me there") are one thing.
func TestCatsAgentSegmentIsClickable(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	withCtlSpy(t, a)
	a.cats.panes = []cats.PaneInfo{{Pane: 3, Agent: "claude", AgentState: cats.StateWorking}}

	found := false
	for _, s := range a.statusRightSegments() {
		if strings.Contains(s.text, "claude") {
			found = true
			if s.onClick == nil {
				t.Fatal("the agent segment is not clickable")
			}
		}
	}
	if !found {
		t.Fatal("no agent segment in the status bar")
	}
}

// The quote carries a location, because an agent handed twenty lines with no
// file name can only guess where they came from.
func TestCatsSelectionQuoteCarriesAReference(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	sub := filepath.Join(a.rootDir, "internal")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(sub, "x.go")
	if err := os.WriteFile(path, []byte("one\ntwo\nthree\nfour\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	a.openFile(path)
	tab := a.activeTabPtr()

	// Lines 2–3, selected the way a drag does it: anchor at the start of
	// line 2, cursor at the start of line 4.
	tab.Anchor = editor.Position{Line: 1, Col: 0}
	tab.Cursor = editor.Position{Line: 3, Col: 0}

	quote := a.catsSelectionQuote()
	if !strings.HasPrefix(quote, filepath.Join("internal", "x.go")+":2-3\n\n") {
		t.Fatalf("quote = %q — want a project-relative ref, end line NOT overstated", quote)
	}
	if !strings.Contains(quote, "two\nthree\n") {
		t.Fatalf("quote = %q — the selected text is missing", quote)
	}

	// A single-line selection names one line, not a range of one.
	tab.Anchor = editor.Position{Line: 0, Col: 0}
	tab.Cursor = editor.Position{Line: 0, Col: 3}
	if got := a.catsSelectionRef(tab); got != filepath.Join("internal", "x.go")+":1" {
		t.Fatalf("single-line ref = %q", got)
	}
}

// Sending stages the quote — submit false, permanently. An editor that could
// silently prompt an agent is an editor that can spend somebody's tokens and
// change their files with no keystroke.
func TestCatsSendSelectionStagesRatherThanSubmits(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	s := withCtlSpy(t, a)
	a.cats.panes = []cats.PaneInfo{{Pane: 3, Handle: "w1:p1", Agent: "claude", AgentState: cats.StateIdle}}
	path := openTestFileTab(t, a, "main.go")
	tab := a.activeTabPtr()
	tab.Anchor = editor.Position{Line: 0, Col: 0}
	tab.Cursor = editor.Position{Line: 0, Col: 7}

	a.menuCatsSendSelection()

	call := waitForCall(t, s, cats.MethodPaneSendIn)
	var in struct {
		Pane   uint32 `json:"pane"`
		Text   string `json:"text"`
		Submit bool   `json:"submit"`
	}
	if err := json.Unmarshal(call.Params, &in); err != nil {
		t.Fatalf("params: %v", err)
	}
	if in.Submit {
		t.Fatal("the selection was SUBMITTED — pressing Enter is the user's act")
	}
	if in.Pane != 3 {
		t.Fatalf("sent to pane %d, want the lone agent (3)", in.Pane)
	}
	if !strings.HasPrefix(in.Text, filepath.Base(path)+":1\n\n") {
		t.Fatalf("text = %q, want the file:line reference first", in.Text)
	}
}

// With several agents it asks which, rather than guessing — a wrong guess
// puts the user's code in front of the wrong program.
func TestCatsSendSelectionAsksWhichAgent(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	withCtlSpy(t, a)
	a.cats.panes = siblingPanes()
	a.cats.self, a.cats.selfOK = 9, true
	openTestFileTab(t, a, "main.go")
	tab := a.activeTabPtr()
	tab.Anchor = editor.Position{Line: 0, Col: 0}
	tab.Cursor = editor.Position{Line: 0, Col: 7}

	a.menuCatsSendSelection()

	labels := pickerLabels(t, a)
	if len(labels) != 2 {
		t.Fatalf("rows = %v, want one per sibling agent", labels)
	}
	if !strings.Contains(labels[0], "claude in w1:p4") || !strings.Contains(labels[0], "blocked") {
		t.Fatalf("row 0 = %q — it must say who, where, and what they are doing", labels[0])
	}
}

// The chat verb reaches the HOST's chat panel, and needs no agent pane at
// all — it is the door for a question about the code rather than for one
// program.
func TestCatsAskChatSendsTheQuote(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	s := withCtlSpy(t, a)
	openTestFileTab(t, a, "main.go")
	tab := a.activeTabPtr()
	tab.Anchor = editor.Position{Line: 0, Col: 0}
	tab.Cursor = editor.Position{Line: 0, Col: 7}

	a.menuCatsAskChat()

	call := waitForCall(t, s, cats.MethodChatSend)
	var p struct {
		Text string `json:"text"`
	}
	if err := json.Unmarshal(call.Params, &p); err != nil {
		t.Fatalf("params: %v", err)
	}
	if !strings.Contains(p.Text, "main.go:1") {
		t.Fatalf("chat text = %q, want the location", p.Text)
	}
}

// Both send rows decline with nothing selected, and say so: "send the
// selection" with no selection is a question, not a row.
func TestCatsSendNeedsASelection(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	withCtlSpy(t, a)
	openTestFileTab(t, a, "main.go")

	if a.hasCatsSelectionSend() {
		t.Fatal("the rows should be dim with nothing selected")
	}
	a.menuCatsSendSelection()
	if !strings.Contains(a.statusMsg, "Select some text") {
		t.Fatalf("status = %q", a.statusMsg)
	}
	a.menuCatsAskChat()
	if !strings.Contains(a.statusMsg, "Select some text") {
		t.Fatalf("status = %q", a.statusMsg)
	}
}

// Below Tier 1 every path here is inert: no dial, no panic, an explanation.
func TestCatsAgentsAreANoopAtTier0(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	a.catsPollPanes()
	if !a.cats.panesPolledAt.IsZero() {
		t.Fatal("Tier 0 should not even start the rate-limit clock")
	}
	if a.hasCatsAgents() || a.hasCatsSelectionSend() {
		t.Fatal("agent rows live in a plain terminal")
	}
	a.catsFocusAgent()
	if !strings.Contains(a.statusMsg, "No agent panes") {
		t.Fatalf("status = %q", a.statusMsg)
	}
}
