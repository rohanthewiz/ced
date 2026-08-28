// =============================================================================
// File: internal/app/chatarchive_test.go
// Author: Rohan Allison <rohanthewiz@gmail.com>
// Created: 2026-08-28
// Copyright: 2026 Rohan Allison. All rights reserved.
// =============================================================================

// Tests for the chat archive (chatarchive.go). newTestApp points
// chatArchiveDirFn at a temp directory, so nothing here can touch the
// developer's real ~/.config/ced/chats.

package app

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/rohanthewiz/ced/internal/chatstore"
)

// seedChat fills the panel with one exchange so the archive has
// something worth keeping.
func seedChat(a *App, prompt, answer string) {
	a.chatAppendMsg(chatMsg{role: chatRoleUser, text: prompt})
	a.chatAppendMsg(chatMsg{role: chatRoleAgent, text: answer})
}

// TestChatArchiveSaveRoundTrip pins the save path end to end: roles are
// stored as strings, the title comes off the user's prompt, and the
// project root travels with the entry so a picker row can name it.
func TestChatArchiveSaveRoundTrip(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	seedChat(a, "why does the caret drift?", "because caretGoalCol")
	a.chatArchiveSave()

	metas, err := chatstore.List(chatArchiveDirFn())
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(metas) != 1 {
		t.Fatalf("archived %d conversations, want 1", len(metas))
	}
	if metas[0].Title != "why does the caret drift?" {
		t.Errorf("title = %q", metas[0].Title)
	}
	c, err := chatstore.Load(chatArchiveDirFn(), metas[0].ID)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(c.Msgs) != 2 || c.Msgs[0].Role != "user" || c.Msgs[1].Role != "agent" {
		t.Fatalf("stored messages = %+v", c.Msgs)
	}
	if c.Root == "" {
		t.Error("conversation should record the project root it belonged to")
	}
}

// TestChatArchiveSaveReusesID pins the one-file-per-conversation rule:
// saving after every turn must overwrite the same entry, or a long chat
// would fill the Recent list with a row per exchange.
func TestChatArchiveSaveReusesID(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	seedChat(a, "first question", "first answer")
	a.chatArchiveSave()
	first := a.chat.archiveID
	if first == "" {
		t.Fatal("save should mint an archive id")
	}
	seedChat(a, "second question", "second answer")
	a.chatArchiveSave()
	if a.chat.archiveID != first {
		t.Errorf("archive id changed mid-conversation: %q → %q", first, a.chat.archiveID)
	}
	metas, _ := chatstore.List(chatArchiveDirFn())
	if len(metas) != 1 {
		t.Fatalf("archived %d conversations, want 1", len(metas))
	}
	if metas[0].Count != 4 {
		t.Errorf("message count = %d, want 4", metas[0].Count)
	}
}

// TestChatArchiveSkipsPromptlessPanel pins the worth test: a panel that
// only ever printed editor-side status is not a conversation, and
// archiving those would fill the picker with rows nobody can tell apart.
func TestChatArchiveSkipsPromptlessPanel(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	a.chatAppendMsg(chatMsg{role: chatRoleInfo, text: "starting Copilot chat"})
	a.chatAppendMsg(chatMsg{role: chatRoleTool, text: "▤ attached main.go"})
	a.chatArchiveSave()
	if metas, _ := chatstore.List(chatArchiveDirFn()); len(metas) != 0 {
		t.Fatalf("archived %d conversations, want 0", len(metas))
	}
	if a.chat.archiveID != "" {
		t.Error("a promptless panel should not claim an archive id")
	}
}

// TestChatNewChatArchivesAndClears is the headline behavior: the panel
// empties, and what was in it is retrievable rather than gone.
func TestChatNewChatArchivesAndClears(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	seedChat(a, "why does the caret drift?", "because caretGoalCol")
	a.chat.scroll = 3
	a.chat.selActive = true

	a.chatNewChat()

	if len(a.chat.msgs) != 0 {
		t.Fatalf("transcript still holds %d messages", len(a.chat.msgs))
	}
	if a.chat.scroll != 0 || a.chat.selActive {
		t.Error("clearing should reset the scroll and drop the selection")
	}
	if a.chat.archiveID != "" {
		t.Error("a fresh conversation should start with no archive identity")
	}
	metas, _ := chatstore.List(chatArchiveDirFn())
	if len(metas) != 1 || metas[0].Title != "why does the caret drift?" {
		t.Fatalf("cleared conversation was not archived: %+v", metas)
	}
	if !strings.Contains(a.statusMsg, "Recent chats") {
		t.Errorf("flash = %q, should point at where the conversation went", a.statusMsg)
	}
}

// TestChatNewChatKeepsPromptHistoryAndAttachments pins what survives a
// clear: Up-arrow recall is a typing convenience that spans
// conversations (the shell-history rule), and a pending attachment
// describes the message the user has not sent yet.
func TestChatNewChatKeepsPromptHistoryAndAttachments(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	seedChat(a, "an earlier question", "an answer")
	a.chat.history = []string{"an earlier question"}
	a.chat.attach = []chatAttach{{path: "main.go"}}

	a.chatNewChat()

	if len(a.chat.history) != 1 {
		t.Errorf("prompt history = %v, should survive a clear", a.chat.history)
	}
	if len(a.chat.attach) != 1 {
		t.Errorf("pending attachments = %v, should survive a clear", a.chat.attach)
	}
}

// TestChatNewChatRestartsTheSession is the honesty guarantee: clearing
// the panel while the agent kept every prior turn in its context would
// answer the next question against a conversation the user can no longer
// see — and bill for it.
func TestChatNewChatRestartsTheSession(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	fake := wireChat(a)
	seedChat(a, "why does the caret drift?", "because caretGoalCol")

	a.chatNewChat()

	if !fake.isClosed() {
		t.Error("clearing should tear the ACP connection down")
	}
	if a.chat.client != nil || a.chat.sessionID != "" {
		t.Error("clearing should leave no live session behind")
	}
}

// TestChatRestartSessionIsNoOpWhenDetached pins that "new chat" on an
// unopened or dead panel does not become a spawn attempt — reconnecting
// is a side effect here, not the user asking to retry a crashed agent.
func TestChatRestartSessionIsNoOpWhenDetached(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	a.chat.dead = true
	seedChat(a, "a question", "an answer")
	before := a.chat.connSeq

	a.chatNewChat()

	if a.chat.connSeq != before {
		t.Error("a detached panel should not bump the connection generation")
	}
	if !a.chat.dead {
		t.Error("clearing must not clear the dead verdict — that is the backend picker's job")
	}
}

// TestChatNewChatRefusesMidTurn pins the refusal: the agent is
// mid-sentence and the user is watching it arrive.
func TestChatNewChatRefusesMidTurn(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	wireChat(a)
	seedChat(a, "a question", "a partial answer")
	a.chat.turnActive = true

	a.chatNewChat()

	if len(a.chat.msgs) != 2 {
		t.Fatalf("transcript was cleared mid-turn (%d messages left)", len(a.chat.msgs))
	}
	if !strings.Contains(a.statusMsg, "⏹") {
		t.Errorf("flash = %q, should say how to stop the answer", a.statusMsg)
	}
}

// TestChatClearLabelNamesTheConsequence pins that the row says it saves
// — "new" beside an hour-old transcript otherwise reads as a threat.
func TestChatClearLabelNamesTheConsequence(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	if got := a.chatClearLabel(); got != "New chat" {
		t.Errorf("empty-panel label = %q", got)
	}
	seedChat(a, "a question", "an answer")
	if got := a.chatClearLabel(); !strings.Contains(got, "saves") {
		t.Errorf("label = %q, should say the conversation is kept", got)
	}
}

// TestMenuChatRecentEmptyFlashes pins the empty case: the row stays
// clickable (the MCP/Skills rule), so the sentence has to come from
// somewhere — and it must not open a picker with no rows.
func TestMenuChatRecentEmptyFlashes(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	a.menuChatRecent()
	if a.modal != nil {
		t.Fatalf("modal = %T, want none for an empty archive", a.modal)
	}
	if !strings.Contains(a.statusMsg, "no saved chats") {
		t.Errorf("flash = %q", a.statusMsg)
	}
}

// TestMenuChatRecentExcludesLiveConversation pins the recent-folders
// rule applied here: the conversation already on screen is not an
// "open" candidate, because picking it would reload the panel from a
// copy of itself.
func TestMenuChatRecentExcludesLiveConversation(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	seedChat(a, "older question", "older answer")
	a.chatArchiveSave()
	a.chatNewChat() // archives the first, starts a second
	seedChat(a, "live question", "live answer")
	a.chatArchiveSave()

	a.menuChatRecent()
	pm, ok := a.modal.(*paletteModal)
	if !ok {
		t.Fatalf("modal = %T, want picker", a.modal)
	}
	joined := ""
	for _, it := range pm.items {
		joined += it.label + "|"
	}
	if strings.Contains(joined, "live question") {
		t.Errorf("picker lists the live conversation: %q", joined)
	}
	if !strings.Contains(joined, "older question") {
		t.Errorf("picker missing the archived conversation: %q", joined)
	}
}

// TestChatOpenArchivedRestoresAndWarns pins the restore contract: the
// text comes back, the live conversation is kept on the way past, and
// the panel SAYS the agent does not remember any of it — the gap is
// invisible otherwise and shows up three messages later as a confidently
// unrelated answer.
func TestChatOpenArchivedRestoresAndWarns(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	seedChat(a, "an old question", "an old answer")
	a.chatArchiveSave()
	oldID := a.chat.archiveID
	a.chatNewChat()
	seedChat(a, "a newer question", "a newer answer")

	a.chatOpenArchived(oldID)

	if a.chat.archiveID != oldID {
		t.Errorf("archive id = %q, want the restored conversation's %q", a.chat.archiveID, oldID)
	}
	if !a.chat.restored {
		t.Error("restored flag should be set")
	}
	if len(a.chat.msgs) != 3 { // the two restored messages plus the note
		t.Fatalf("transcript = %d messages, want 3", len(a.chat.msgs))
	}
	if a.chat.msgs[0].text != "an old question" {
		t.Errorf("first restored message = %q", a.chat.msgs[0].text)
	}
	note := a.chat.msgs[2]
	if note.role != chatRoleInfo || !strings.Contains(note.text, "no memory") {
		t.Errorf("restore note = %+v, should say the agent does not remember it", note)
	}
	if !a.chat.open {
		t.Error("restoring should open the panel — a conversation you can't see is not restored")
	}
	// The conversation that was live when the restore ran is archived
	// too, so the gesture is reversible from the same picker.
	metas, _ := chatstore.List(chatArchiveDirFn())
	if len(metas) != 2 {
		t.Fatalf("archive holds %d conversations, want 2", len(metas))
	}
}

// TestChatOpenArchivedLeavesTheTranscriptOnScreen pins the ordering bug
// that shipped a blank panel: chatMaxScroll is derived from the strip's
// WIDTH, and a closed panel has none — every message wrapped to one rune
// per row, so a scroll pinned before the reveal landed far past the end
// and the restored conversation opened empty, with nothing on screen to
// say why. A conversation this short must rest at the top.
func TestChatOpenArchivedLeavesTheTranscriptOnScreen(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	seedChat(a, "a short question", "a short answer")
	a.chatArchiveSave()
	id := a.chat.archiveID
	a.chatNewChat()

	a.chatOpenArchived(id)

	if a.chat.scroll != 0 {
		t.Errorf("scroll = %d, want 0 — the whole conversation fits", a.chat.scroll)
	}
	if got := a.chatMaxScroll(); got != 0 {
		t.Errorf("chatMaxScroll = %d, want 0 at the real panel width", got)
	}
	// The pin must land on a width the panel actually has, so the rows
	// the scroll was measured against are the rows that get drawn.
	if a.chatRowWidth() <= 1 {
		t.Fatalf("chatRowWidth = %d — the panel was measured while closed", a.chatRowWidth())
	}
	rows := a.chatRows(a.chatRowWidth())
	if len(rows) == 0 || len(rows) > a.chatVisibleRows() {
		t.Fatalf("derived %d rows for %d visible", len(rows), a.chatVisibleRows())
	}
}

// TestChatOpenArchivedRefusesMidTurn mirrors the clear's refusal.
func TestChatOpenArchivedRefusesMidTurn(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	seedChat(a, "an old question", "an old answer")
	a.chatArchiveSave()
	id := a.chat.archiveID
	a.chat.turnActive = true

	a.chatOpenArchived(id)

	if a.chat.restored {
		t.Error("a restore should not run mid-turn")
	}
	if !strings.Contains(a.statusMsg, "⏹") {
		t.Errorf("flash = %q", a.statusMsg)
	}
}

// TestChatArchiveRoleRoundTrip pins the stored spelling of the role
// enum: the archive outlives the constant block, so a reordered enum
// must not restyle conversations already on disk. An unknown role reads
// as info, the neutral one — text intact, styling degraded.
func TestChatArchiveRoleRoundTrip(t *testing.T) {
	for _, r := range []chatMsgRole{chatRoleUser, chatRoleAgent, chatRoleTool, chatRoleInfo} {
		if got := chatRoleFromArchive(chatArchiveRole(r)); got != r {
			t.Errorf("role %v round-tripped to %v", r, got)
		}
	}
	if got := chatRoleFromArchive("something-new"); got != chatRoleInfo {
		t.Errorf("unknown role = %v, want chatRoleInfo", got)
	}
}

// TestChatArchiveRowLabel pins what a picker row says — and what it
// leaves out: the project marker appears only when the conversation
// belongs somewhere other than the folder you are standing in.
func TestChatArchiveRowLabel(t *testing.T) {
	root := t.TempDir()
	a := newTestApp(t, root)
	now := time.Now()
	prevNow := chatArchiveNow
	chatArchiveNow = func() time.Time { return now }
	t.Cleanup(func() { chatArchiveNow = prevNow })

	here := a.chatArchiveRowLabel(chatstore.Meta{
		Title: "why does the caret drift?", Count: 4,
		Root: a.rootDir, Updated: now,
	})
	if !strings.Contains(here, "why does the caret drift?") || !strings.Contains(here, "4 msgs") {
		t.Errorf("row = %q", here)
	}
	if strings.Contains(here, "  · "+chatArchiveProject(a.rootDir)) {
		t.Errorf("row names the current project for no reason: %q", here)
	}
	away := a.chatArchiveRowLabel(chatstore.Meta{
		Title: "an older thread", Count: 1,
		Root: "/somewhere/else/otherproj", Updated: now,
	})
	if !strings.Contains(away, "otherproj") {
		t.Errorf("row for another project should name it: %q", away)
	}
	if !strings.Contains(away, "1 msg") || strings.Contains(away, "1 msgs") {
		t.Errorf("singular message count mis-rendered: %q", away)
	}
	untitled := a.chatArchiveRowLabel(chatstore.Meta{Updated: now})
	if !strings.Contains(untitled, "untitled") {
		t.Errorf("untitled row = %q", untitled)
	}
}

// TestChatArchiveWhen pins the three timestamp forms. The row is
// scanned, not read: "this morning" and "last week" are the two
// questions it answers.
func TestChatArchiveWhen(t *testing.T) {
	now := time.Date(2026, 8, 28, 18, 0, 0, 0, time.Local)
	cases := []struct {
		name string
		when time.Time
		want string
	}{
		{"today", time.Date(2026, 8, 28, 9, 5, 0, 0, time.Local), "09:05"},
		{"this year", time.Date(2026, 3, 4, 9, 5, 0, 0, time.Local), "Mar  4"},
		{"older", time.Date(2025, 3, 4, 9, 5, 0, 0, time.Local), "Mar  4 25"},
	}
	for _, c := range cases {
		got := chatArchiveWhen(c.when, now)
		if !strings.Contains(got, c.want) {
			t.Errorf("%s: chatArchiveWhen = %q, want it to contain %q", c.name, got, c.want)
		}
	}
	// Every form is the same width, so titles line up down the picker.
	width := len(chatArchiveWhen(cases[0].when, now))
	for _, c := range cases[1:] {
		if got := len(chatArchiveWhen(c.when, now)); got != width {
			t.Errorf("%s: stamp width %d, want %d", c.name, got, width)
		}
	}
	if got := chatArchiveWhen(time.Time{}, now); len(got) != width {
		t.Errorf("zero stamp width %d, want %d", len(got), width)
	}
}

// TestChatShutdownArchives pins the exit path: whatever was appended
// since the last turn (a prompt, an editor-side note) still lands, and
// so does a conversation whose editor was simply quit.
func TestChatShutdownArchives(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	seedChat(a, "a question at quitting time", "an answer")
	a.chatShutdown()
	metas, _ := chatstore.List(chatArchiveDirFn())
	if len(metas) != 1 || metas[0].Title != "a question at quitting time" {
		t.Fatalf("shutdown did not archive the conversation: %+v", metas)
	}
}

// TestHandleChatTurnDoneArchives pins the after-every-turn save, which
// is what makes the archive survive a crash rather than only a clean
// exit — the transcript is the one thing in the panel that cannot be
// reconstructed.
func TestHandleChatTurnDoneArchives(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	wireChat(a)
	seedChat(a, "a question", "an answer")
	a.handleChatTurnDone(&chatTurnDoneEvent{when: time.Now(), seq: a.chat.connSeq})
	if metas, _ := chatstore.List(chatArchiveDirFn()); len(metas) != 1 {
		t.Fatalf("archived %d conversations after a turn, want 1", len(metas))
	}
}

// TestHandleChatTurnDoneArchivesOnError pins the same save on the
// failure path: what streamed in before the turn broke is still the
// conversation, and a broken turn is when a session is most likely to
// be lost next.
func TestHandleChatTurnDoneArchivesOnError(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	wireChat(a)
	seedChat(a, "a question", "a partial answer")
	a.handleChatTurnDone(&chatTurnDoneEvent{
		when: time.Now(), seq: a.chat.connSeq, err: errors.New("stream broke"),
	})
	if metas, _ := chatstore.List(chatArchiveDirFn()); len(metas) != 1 {
		t.Fatalf("archived %d conversations after a failed turn, want 1", len(metas))
	}
}
