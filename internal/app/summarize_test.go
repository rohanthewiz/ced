// =============================================================================
// File: internal/app/summarize_test.go
// Author: Rohan Allison <rohanthewiz@gmail.com>
// Created: 2026-08-17
// Copyright: 2026 Rohan Allison. All rights reserved.
// =============================================================================

// Tests for the AI summarize verb (summarize.go). No real agent is ever
// spawned — newTestApp marks the chat dead and these tests inject the
// shared fakeCopilotConn through wireChat, the same seam every other
// chat test uses.

package app

import (
	"strings"
	"testing"
	"time"

	"github.com/rohanthewiz/ced/internal/editor"
)

// TestSelectionOrFileTarget_SelectionBeatsFile pins the shared target
// rule both AI verbs are built on: no file is a refusal that names the
// verb, a plain tab yields the whole file, and a selection narrows it —
// a highlighted region is a narrower question than the file around it.
func TestSelectionOrFileTarget_SelectionBeatsFile(t *testing.T) {
	a := newTestApp(t, t.TempDir())

	if _, why := a.summarizeTarget(); why == "" {
		t.Fatal("no open file should refuse")
	} else if !strings.Contains(why, "summarize") {
		t.Errorf("refusal %q does not name the verb — it reads as a complaint about the editor", why)
	}
	if a.canSummarize() {
		t.Error("canSummarize with no file")
	}

	seedChatFile(t, a, "main.go", "package main\n\nfunc main() {}\n")
	at, why := a.summarizeTarget()
	if why != "" || at.ranged() {
		t.Fatalf("whole-file target = %+v (why=%q)", at, why)
	}
	if !a.canSummarize() {
		t.Error("canSummarize with a file open")
	}

	tab := a.activeTabPtr()
	tab.Anchor = editor.Position{Line: 0, Col: 0}
	tab.Cursor = editor.Position{Line: 2, Col: 3}
	at, why = a.summarizeTarget()
	if why != "" || at.lineFrom != 1 || at.lineTo != 3 {
		t.Fatalf("selection target = %+v (why=%q)", at, why)
	}
}

// TestSummarizeLabel_NamesWhatItCovers pins the dynamic ≡ label. The
// difference between "the file" and "these twelve lines" has to be
// visible BEFORE the row is clicked, because clicking it spends a turn.
func TestSummarizeLabel_NamesWhatItCovers(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	if got := a.summarizeLabel(); got != "Summarize with AI…" {
		t.Errorf("no-file label = %q", got)
	}

	seedChatFile(t, a, "main.go", "package main\n\nfunc main() {}\n")
	if got := a.summarizeLabel(); got != "Summarize main.go…" {
		t.Errorf("file label = %q", got)
	}

	tab := a.activeTabPtr()
	tab.Anchor = editor.Position{Line: 0, Col: 0}
	tab.Cursor = editor.Position{Line: 2, Col: 3}
	if got := a.summarizeLabel(); got != "Summarize selection (3 lines)…" {
		t.Errorf("selection label = %q", got)
	}
}

// TestMenuSummarize_SendsTheTurnCarryingTheText is the whole feature end
// to end: the panel opens, the transcript records what was asked, and
// the turn goes out carrying the file's actual text — not a reference to
// it, which is the failure mode a "summarize this" that summarised
// nothing would have.
func TestMenuSummarize_SendsTheTurnCarryingTheText(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	fake := wireChat(a)
	a.chat.embeddedContext = true
	seedChatFile(t, a, "main.go", "package main\n\nfunc main() { println(\"hi\") }\n")

	a.menuSummarize()

	if !a.chat.open {
		t.Fatal("summarize must open the panel — a request streaming into a hidden panel reads as a hang")
	}
	waitForCopilot(t, "session/prompt call", func() bool { return fake.called("session/prompt") })

	if len(a.chat.msgs) == 0 || a.chat.msgs[0].role != chatRoleUser ||
		!strings.Contains(a.chat.msgs[0].text, "Summarize main.go") {
		t.Errorf("transcript = %+v, want the ask recorded", a.chat.msgs)
	}
	// Per-turn attachments: chatSendPrompt consumes the pending set.
	if len(a.chat.attach) != 0 {
		t.Errorf("attachments not consumed: %+v", a.chat.attach)
	}

	var sawText, sawResource bool
	for _, b := range promptBlocks(t, fake) {
		switch b["type"] {
		case "text":
			if s, _ := b["text"].(string); strings.Contains(s, "Summarize the attached") {
				sawText = true
			}
		case "resource":
			raw, _ := b["resource"].(map[string]any)
			if s, _ := raw["text"].(string); strings.Contains(s, "func main()") {
				sawResource = true
			}
		}
	}
	if !sawText {
		t.Error("no instruction block in the prompt")
	}
	if !sawResource {
		t.Error("the file's text never reached the agent")
	}
}

// TestMenuSummarize_SelectionNarrowsThePayload: with a selection, the
// prompt must carry those lines and say it is an excerpt — a summary of
// the whole file when the user highlighted three lines is a wrong answer
// that looks like a right one.
func TestMenuSummarize_SelectionNarrowsThePayload(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	fake := wireChat(a)
	a.chat.embeddedContext = true
	seedChatFile(t, a, "main.go", "alpha\nbravo\ncharlie\ndelta\n")

	tab := a.activeTabPtr()
	tab.Anchor = editor.Position{Line: 1, Col: 0}
	tab.Cursor = editor.Position{Line: 2, Col: 3}
	a.menuSummarize()
	waitForCopilot(t, "session/prompt call", func() bool { return fake.called("session/prompt") })

	var payload, instruction string
	for _, b := range promptBlocks(t, fake) {
		switch b["type"] {
		case "text":
			instruction, _ = b["text"].(string)
		case "resource":
			raw, _ := b["resource"].(map[string]any)
			payload, _ = raw["text"].(string)
		}
	}
	if strings.Contains(payload, "alpha") || strings.Contains(payload, "delta") {
		t.Errorf("payload %q leaked lines outside the selection", payload)
	}
	if !strings.Contains(payload, "bravo") || !strings.Contains(payload, "charlie") {
		t.Errorf("payload %q is missing the selected lines", payload)
	}
	if !strings.Contains(instruction, "excerpt") || !strings.Contains(instruction, "main.go:2-3") {
		t.Errorf("instruction %q should name the excerpt and its range", instruction)
	}
}

// TestMenuSummarize_QueuesWhileStarting pins the first-gesture race: a
// summarize asked for mid-handshake is queued with its attachment
// intact, and lands the moment the session exists.
func TestMenuSummarize_QueuesWhileStarting(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	a.copilot.enabled = true
	a.chat.dead = false
	a.chat.starting = true
	seedChatFile(t, a, "main.go", "package main\n")

	a.menuSummarize()
	if a.chat.queuedPrompt == "" {
		t.Fatal("the request was dropped instead of queued")
	}
	if len(a.chat.attach) != 1 {
		t.Fatalf("attachments = %+v, want the target held for the queued send", a.chat.attach)
	}

	fake := &fakeCopilotConn{}
	a.handleChatReady(&chatReadyEvent{when: time.Now(), client: fake, sessionID: "sess-2"})
	waitForCopilot(t, "queued prompt sent", func() bool { return fake.called("session/prompt") })
	if a.chat.queuedPrompt != "" {
		t.Errorf("queue not drained: %q", a.chat.queuedPrompt)
	}
}

// TestMenuSummarize_RefusesMidTurn: the agent answers one thing at a
// time, so a second ask must say so rather than queue silently behind
// the first.
func TestMenuSummarize_RefusesMidTurn(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	fake := wireChat(a)
	seedChatFile(t, a, "main.go", "package main\n")
	a.chat.turnActive = true

	a.menuSummarize()
	if fake.called("session/prompt") {
		t.Error("a second turn was started while one was in flight")
	}
	if !strings.Contains(a.statusMsg, "answering") {
		t.Errorf("statusMsg = %q, want the busy explanation", a.statusMsg)
	}
}

// TestMenuSummarize_NoFileFlashesTheReason rather than doing nothing —
// a row that silently no-ops reads as broken.
func TestMenuSummarize_NoFileFlashesTheReason(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	wireChat(a)
	a.menuSummarize()
	if a.chat.open {
		t.Error("nothing to summarize should not open the panel")
	}
	if !strings.Contains(a.statusMsg, "summarize") {
		t.Errorf("statusMsg = %q", a.statusMsg)
	}
}

// TestChatAttachOnce_LeavesTheAutoAttachmentAlone: with auto-context on
// (the shipped default) the active file is already synthesized for every
// turn, so summarize must not add a duplicate — the agent would be
// handed the same file twice, and paid for twice.
func TestChatAttachOnce_LeavesTheAutoAttachmentAlone(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	wireChat(a)
	seedChatFile(t, a, "main.go", "package main\n")
	a.chat.autoContext = true

	at, _ := a.summarizeTarget()
	a.chatAttachOnce(at)
	if len(a.chat.attach) != 0 {
		t.Fatalf("attach = %+v, want the synthesized entry left to stand", a.chat.attach)
	}
	if got := len(a.chatPendingAttachments()); got != 1 {
		t.Errorf("pending = %d, want exactly one copy of the file", got)
	}

	// A different range is a different question and does get added.
	a.chatAttachOnce(chatAttach{path: at.path, lineFrom: 1, lineTo: 1})
	if len(a.chat.attach) != 1 {
		t.Errorf("a narrower range should attach: %+v", a.chat.attach)
	}
}

// TestSummarizePrompt_AsksForADescription: the prompt has to forbid a
// review, or an agent answers "here are eight things you should change"
// to a question about what the code says.
func TestSummarizePrompt_AsksForADescription(t *testing.T) {
	p := summarizePrompt("main.go", false)
	for _, want := range []string{"Summarize the attached file", "main.go", "do not review it", "200 words"} {
		if !strings.Contains(p, want) {
			t.Errorf("prompt is missing %q:\n%s", want, p)
		}
	}
	if got := summarizePrompt("main.go:2-9", true); !strings.Contains(got, "excerpt") {
		t.Errorf("a ranged prompt should say excerpt:\n%s", got)
	}
}
