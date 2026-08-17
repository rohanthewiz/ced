// =============================================================================
// File: internal/app/gonotes_test.go
// Author: Rohan Allison <rohanthewiz@gmail.com>
// Created: 2026-08-17
// Copyright: 2026 Rohan Allison. All rights reserved.
// =============================================================================

// Tests for the GoNotes capture (gonotes.go). Nothing here reaches a
// GoNotes server: newTestApp pins gonotesCreate at a refusal, and the
// tests that need to see what was sent replace it with their own
// recorder. The chat half injects the shared fakeCopilotConn through
// wireChat, like every other agent-backed feature.

package app

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/gdamore/tcell/v2"

	"github.com/rohanthewiz/ced/internal/editor"
	"github.com/rohanthewiz/ced/internal/gonotes"
)

// captureNotes replaces the send seam with a recorder, returning a
// channel the test reads the note off. The send runs on a goroutine, so
// a channel is what makes the assertion deterministic.
func captureNotes(t *testing.T, res gonotes.Result, err error) chan gonotes.Note {
	t.Helper()
	sent := make(chan gonotes.Note, 4)
	prev := gonotesCreate
	gonotesCreate = func(n gonotes.Note) (gonotes.Result, error) {
		sent <- n
		return res, err
	}
	t.Cleanup(func() { gonotesCreate = prev })
	return sent
}

// awaitNote reads one recorded note or fails.
func awaitNote(t *testing.T, sent chan gonotes.Note) gonotes.Note {
	t.Helper()
	select {
	case n := <-sent:
		return n
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for the note to be sent")
		return gonotes.Note{}
	}
}

// TestSendToNotesLabel_NamesWhatItCaptures pins the dynamic ≡ label —
// the difference between capturing a file and capturing a highlighted
// region has to be visible before the row is clicked.
func TestSendToNotesLabel_NamesWhatItCaptures(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	if got := a.sendToNotesLabel(); got != "Send to GoNotes…" {
		t.Errorf("no-file label = %q", got)
	}
	if a.canSendToNotes() {
		t.Error("canSendToNotes with no file")
	}

	seedChatFile(t, a, "notes.md", "one\ntwo\nthree\n")
	if got := a.sendToNotesLabel(); got != "Send notes.md to GoNotes…" {
		t.Errorf("file label = %q", got)
	}
	if !a.canSendToNotes() {
		t.Error("canSendToNotes with a file open")
	}

	tab := a.activeTabPtr()
	tab.Anchor = editor.Position{Line: 0, Col: 0}
	tab.Cursor = editor.Position{Line: 1, Col: 3}
	if got := a.sendToNotesLabel(); got != "Send selection to GoNotes…" {
		t.Errorf("selection label = %q", got)
	}
}

// TestDefaultNoteTitle_IsAFindableAnswer: the prompt opens holding a
// real title, not a placeholder — Enter straight away has to produce a
// note the user can find again, which is what makes both the typed and
// the drafted paths optional.
func TestDefaultNoteTitle_IsAFindableAnswer(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	seedChatFile(t, a, "main.go", "a\nb\nc\n")
	at, _ := a.noteTarget()
	if got := a.defaultNoteTitle(at); got != "main.go" {
		t.Errorf("default title = %q", got)
	}

	tab := a.activeTabPtr()
	tab.Anchor = editor.Position{Line: 1, Col: 0}
	tab.Cursor = editor.Position{Line: 2, Col: 1}
	at, _ = a.noteTarget()
	if got := a.defaultNoteTitle(at); got != "main.go:2-3" {
		t.Errorf("ranged default title = %q", got)
	}
}

// TestNoteDescription_NamesTheProject: a notes database fed by a dozen
// repositories learns nothing from a bare "internal/app/find.go", so the
// provenance line carries the project too.
func TestNoteDescription_NamesTheProject(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	seedChatFile(t, a, "main.go", "x\n")
	at, _ := a.noteTarget()
	got := a.noteDescription(at)
	if !strings.HasPrefix(got, "ced: ") || !strings.HasSuffix(got, "/main.go") {
		t.Errorf("description = %q", got)
	}
	if projectName("/a/b/proj") != "proj" || projectName("/") != "" || projectName("proj") != "proj" {
		t.Error("projectName mishandles an edge case")
	}
}

// TestStartNoteSend_CapturesTheBufferVerbatim is the core contract: the
// body is the text on screen INCLUDING unsaved edits (the stale on-disk
// copy is the one failure nobody would notice until much later), it is
// sent verbatim with nothing prepended, and the provenance rides in the
// description instead.
func TestStartNoteSend_CapturesTheBufferVerbatim(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	sent := captureNotes(t, gonotes.Result{ID: 5, Title: "T"}, nil)
	seedChatFile(t, a, "main.go", "on disk\n")

	tab := a.activeTabPtr()
	tab.Buffer.Lines = []string{"unsaved one", "unsaved two"}

	at, _ := a.noteTarget()
	a.startNoteSend(at, "My title", false)

	n := awaitNote(t, sent)
	if n.Body != "unsaved one\nunsaved two" {
		t.Errorf("body = %q, want the buffer's text verbatim", n.Body)
	}
	if n.Title != "My title" {
		t.Errorf("title = %q", n.Title)
	}
	if n.Tags != "ced" {
		t.Errorf("tags = %q, want the fixed capture tag", n.Tags)
	}
	if !strings.Contains(n.Description, "main.go") {
		t.Errorf("description = %q", n.Description)
	}
	if n.Private {
		t.Error("private should default off")
	}
}

// TestStartNoteSend_SelectionIsTheBody: with a selection the note holds
// those lines and only those — the whole file would be a wrong answer
// that looks like a right one.
func TestStartNoteSend_SelectionIsTheBody(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	sent := captureNotes(t, gonotes.Result{ID: 1}, nil)
	seedChatFile(t, a, "main.go", "alpha\nbravo\ncharlie\ndelta\n")

	tab := a.activeTabPtr()
	tab.Anchor = editor.Position{Line: 1, Col: 0}
	tab.Cursor = editor.Position{Line: 2, Col: 3}
	at, _ := a.noteTarget()
	a.startNoteSend(at, "Middle", true)

	n := awaitNote(t, sent)
	if n.Body != "bravo\ncharlie" {
		t.Errorf("body = %q, want only the selected lines", n.Body)
	}
	if !n.Private {
		t.Error("the private decision did not reach the send")
	}
}

// TestStartNoteSend_RefusesEmptyText rather than creating a titled note
// with nothing in it.
func TestStartNoteSend_RefusesEmptyText(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	sent := captureNotes(t, gonotes.Result{}, nil)
	seedChatFile(t, a, "blank.txt", "   \n\n")

	at, _ := a.noteTarget()
	a.startNoteSend(at, "Nothing", false)

	select {
	case n := <-sent:
		t.Fatalf("an empty capture was sent anyway: %+v", n)
	case <-time.After(100 * time.Millisecond):
	}
	if !strings.Contains(a.statusMsg, "Nothing to send") {
		t.Errorf("statusMsg = %q", a.statusMsg)
	}
}

// TestHandleGonotesDone_ReportsBothOutcomes: success is a flash (the
// note is elsewhere now and there is nothing to act on), while a failure
// opens the info modal — those messages carry the address that was
// dialed, and a flash that scrolls away is not enough to act on.
func TestHandleGonotesDone_ReportsBothOutcomes(t *testing.T) {
	a := newTestApp(t, t.TempDir())

	a.handleGonotesDone(&gonotesDoneEvent{when: time.Now(), title: "Some note", res: gonotes.Result{ID: 3}})
	if a.modal != nil {
		t.Error("success should not open a modal")
	}
	if !strings.Contains(a.statusMsg, "Some note") {
		t.Errorf("statusMsg = %q", a.statusMsg)
	}

	a.handleGonotesDone(&gonotesDoneEvent{when: time.Now(), title: "Some note",
		err: errors.New("connection refused")})
	m, ok := a.modal.(*confirmModal)
	if !ok {
		t.Fatalf("failure modal = %T, want the info modal", a.modal)
	}
	body := strings.Join(m.lines, "\n")
	if !strings.Contains(body, "connection refused") {
		t.Errorf("modal omits the cause:\n%s", body)
	}
	if !strings.Contains(body, gonotes.URL()) || !strings.Contains(body, gonotes.EnvURL) {
		t.Errorf("modal should name the address tried and how to change it:\n%s", body)
	}
}

// TestOpenNotePrompt_PrivateChipQualifiesTheSend pins the chip: it is on
// every note prompt (privacy is a statement about the text, true
// whoever wrote the title), and flipping it changes what the send
// carries — not a preference somewhere else.
func TestOpenNotePrompt_PrivateChipQualifiesTheSend(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	sent := captureNotes(t, gonotes.Result{ID: 1}, nil)
	seedChatFile(t, a, "main.go", "body text\n")
	at, _ := a.noteTarget()

	a.openNotePrompt(at, "Title", false, false)
	pm, ok := a.modal.(*promptModal)
	if !ok {
		t.Fatalf("modal = %T, want a prompt", a.modal)
	}
	if len(pm.extras) != 2 {
		t.Fatalf("extras = %d, want the ✦ button and the privacy chip", len(pm.extras))
	}
	chip := pm.extras[1]
	if got := chip.label(a); got != "[private: off]" {
		t.Fatalf("chip label = %q", got)
	}
	if chip.width < runeLen("[private: off]") {
		t.Errorf("chip width %d must cover its widest label", chip.width)
	}
	chip.run(a)
	if got := chip.label(a); got != "[private: on]" {
		t.Fatalf("after the click chip label = %q", got)
	}

	pm.submit(a)
	if n := awaitNote(t, sent); !n.Private {
		t.Error("the chip's decision never reached the note")
	}
}

// TestOpenNotePrompt_ChipIsClickable drives the real mouse path through
// extraRects, which is the geometry draw and hit-testing share.
func TestOpenNotePrompt_ChipIsClickable(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	seedChatFile(t, a, "main.go", "body\n")
	at, _ := a.noteTarget()

	a.openNotePrompt(at, "Title", false, false)
	pm := a.modal.(*promptModal)
	chip := pm.extraRects(a)[1]
	pm.handleMouse(a, chip.x+1, chip.y, tcell.Button1)
	if got := pm.extras[1].label(a); got != "[private: on]" {
		t.Fatalf("the click missed the chip: label %q", got)
	}
}

// TestNotePromptHint_FitsTheBox: the hint is the prompt's only discovery
// surface for its two chords (a modal owns the keyboard, so the ≡ menu
// is unreachable from inside one), and it is drawn before extras decide
// whether the box widens.
func TestNotePromptHint_FitsTheBox(t *testing.T) {
	if n := runeLen(notePromptHint); n > promptModalWidth-4 {
		t.Errorf("hint is %d cells wide, past the %d the box has", n, promptModalWidth-4)
	}
	for _, want := range []string{"alt+a", "alt+p"} {
		if !strings.Contains(notePromptHint, want) {
			t.Errorf("hint %q omits %s", notePromptHint, want)
		}
	}
}

// TestNoteSuggestTitle_SendsTheExcerptAndArmsTheRequest: the ask is a
// normal visible turn, the agent is shown the text (capped), and the
// pending request remembers what the title is FOR.
func TestNoteSuggestTitle_SendsTheExcerptAndArmsTheRequest(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	fake := wireChat(a)
	seedChatFile(t, a, "main.go", "func Widget() {}\n")
	at, _ := a.noteTarget()

	a.noteSuggestTitle(at, true)
	if !a.chat.open {
		t.Fatal("the panel must open — a request streaming into a hidden panel reads as a hang")
	}
	waitForCopilot(t, "session/prompt call", func() bool { return fake.called("session/prompt") })

	req := a.chat.noteTitle
	if req == nil {
		t.Fatal("no pending request armed — the answer would have nothing to attach to")
	}
	if req.at.path != at.path || !req.private {
		t.Errorf("request = %+v, want the capture and its privacy decision carried", req)
	}
	var text string
	for _, b := range promptBlocks(t, fake) {
		if b["type"] == "text" {
			text, _ = b["text"].(string)
		}
	}
	if !strings.Contains(text, "func Widget()") || !strings.Contains(text, "ONLY the title") {
		t.Errorf("prompt = %q, want the excerpt and a one-line instruction", text)
	}
}

// TestNoteSuggestTitle_RefusesMidTurn — one thing at a time, said out
// loud rather than queued silently.
func TestNoteSuggestTitle_RefusesMidTurn(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	fake := wireChat(a)
	seedChatFile(t, a, "main.go", "x\n")
	at, _ := a.noteTarget()
	a.chat.turnActive = true

	a.noteSuggestTitle(at, false)
	if fake.called("session/prompt") {
		t.Error("a second turn was started while one was in flight")
	}
	if a.chat.noteTitle != nil {
		t.Error("a refused ask must not arm a request")
	}
}

// TestChatNoteTitleDone_ReopensThePromptWithTheDraft: the suggestion
// only ever PRE-FILLS the prompt — nothing saves without an Enter — and
// the request is consumed so a later answer can't be mistaken for it.
func TestChatNoteTitleDone_ReopensThePromptWithTheDraft(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	wireChat(a)
	seedChatFile(t, a, "main.go", "x\n")
	at, _ := a.noteTarget()

	a.chat.noteTitle = &noteTitleReq{at: at, seq: a.chat.connSeq, mark: len(a.chat.msgs), private: true}
	a.chatAppendMsg(chatMsg{role: chatRoleAgent, text: "```\nTitle: The widget constructor\n```"})
	a.chatNoteTitleDone(&chatTurnDoneEvent{when: time.Now(), seq: a.chat.connSeq})

	if a.chat.noteTitle != nil {
		t.Error("the request survived its own turn")
	}
	pm, ok := a.modal.(*promptModal)
	if !ok {
		t.Fatalf("modal = %T, want the note prompt reopened", a.modal)
	}
	if got := pm.field.String(); got != "The widget constructor" {
		t.Errorf("draft = %q — the fence and the label should be stripped", got)
	}
	if got := pm.extras[1].label(a); got != "[private: on]" {
		t.Errorf("chip = %q, want the privacy decision carried through the round trip", got)
	}
}

// TestChatNoteTitleDone_DropsAStaleGeneration: an answer that outlived
// its connection belongs to nothing, and applying it would put an
// unrelated sentence on a note.
func TestChatNoteTitleDone_DropsAStaleGeneration(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	wireChat(a)
	seedChatFile(t, a, "main.go", "x\n")
	at, _ := a.noteTarget()

	a.chat.noteTitle = &noteTitleReq{at: at, seq: a.chat.connSeq - 1, mark: 0}
	a.chatAppendMsg(chatMsg{role: chatRoleAgent, text: "Some title"})
	a.chatNoteTitleDone(&chatTurnDoneEvent{when: time.Now(), seq: a.chat.connSeq})

	if a.modal != nil {
		t.Errorf("a stale answer opened %T", a.modal)
	}
	if a.chat.noteTitle == nil {
		t.Error("a stale request belongs to the old generation and must be left alone, not consumed")
	}
}

// TestChatNoteTitleDone_YieldsToAPendingPermission: an agent blocked on
// a permission request has to be answered for anything to proceed, so
// the draft waits in the transcript rather than stealing the slot.
func TestChatNoteTitleDone_YieldsToAPendingPermission(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	wireChat(a)
	seedChatFile(t, a, "main.go", "x\n")
	at, _ := a.noteTarget()

	a.chat.noteTitle = &noteTitleReq{at: at, seq: a.chat.connSeq, mark: len(a.chat.msgs)}
	a.chatAppendMsg(chatMsg{role: chatRoleAgent, text: "A title"})
	a.chat.permQueue = []*chatPermRequest{{}}
	a.chatNoteTitleDone(&chatTurnDoneEvent{when: time.Now(), seq: a.chat.connSeq})

	if a.modal != nil {
		t.Errorf("the draft stole the modal slot: %T", a.modal)
	}
	if !strings.Contains(a.statusMsg, "chat panel") {
		t.Errorf("statusMsg = %q, want the user pointed at the transcript", a.statusMsg)
	}
}

// TestAgentOneLine_StripsWhatAnAgentAdds pins the shared reduction both
// drafted-text features depend on: the field is single-line, and this is
// the difference between saving a title and saving a markdown fence.
func TestAgentOneLine_StripsWhatAnAgentAdds(t *testing.T) {
	cases := []struct{ in, want string }{
		{"```\nTitle: Widget setup\n```", "Widget setup"},
		{"- \"Widget setup\"", "Widget setup"},
		{"Note title: Widget setup", "Widget setup"},
		{"\n\nWidget setup\nand more prose", "Widget setup"},
		{"", ""},
	}
	for _, c := range cases {
		if got := agentOneLine(c.in, "title:", "note title:"); got != c.want {
			t.Errorf("agentOneLine(%q) = %q, want %q", c.in, got, c.want)
		}
	}
	// The commit wrapper still behaves as it always did.
	if got := commitSubject("Commit message: Add the thing"); got != "Add the thing" {
		t.Errorf("commitSubject = %q", got)
	}
}
