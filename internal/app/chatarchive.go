// =============================================================================
// File: internal/app/chatarchive.go
// Author: Rohan Allison <rohanthewiz@gmail.com>
// Created: 2026-08-28
// Copyright: 2026 Rohan Allison. All rights reserved.
// =============================================================================

// chatarchive.go gives the chat panel the two verbs a long-lived
// conversation surface owes you: START A NEW ONE, and GO BACK TO AN OLD
// ONE. Between them they answer the same question from both ends — the
// panel had become a single unbounded transcript that only ever grew,
// with the model's own memory growing along with it, and no way to put
// a finished conversation down without losing it.
//
// House rules:
//
//   - CLEARING RESETS THE AGENT, NOT JUST THE SCREEN. A "clear" that
//     wiped the panel while the ACP session kept every prior turn in its
//     context would be a lie in the direction that costs the user money:
//     the next question would still be answered against a conversation
//     they can no longer see, and still billed for it. So chatNewChat
//     archives, empties the transcript, and tears the connection down so
//     the next turn opens a fresh session/new. That is deliberately the
//     same teardown-and-restart the agent switch performs — one honest
//     path, already tested, rather than a second "reset" verb reaching
//     into the handshake to re-issue session/new by itself.
//
//   - NOTHING IS DESTROYED, WHICH IS WHY NEITHER VERB CONFIRMS. Clearing
//     archives first, and opening a saved conversation archives the live
//     one on its way past, so every gesture here is reversible from the
//     Recent chats picker. A confirmation dialog in front of a
//     reversible action trains people to dismiss dialogs.
//
//   - THE LIVE CONVERSATION IS SAVED AFTER EVERY TURN, not only at
//     teardown. A crash, a `kill`, a closed laptop — the transcript is
//     the one thing in the panel that cannot be reconstructed, and the
//     write is one small file per turn, where a turn already cost
//     seconds of model time. Same reasoning that records a folder VISIT
//     at startup rather than at exit (session.go): a run that dies must
//     cost as little as possible of what actually happened.
//
//   - A RESTORED CONVERSATION IS A READING SURFACE, NOT A RESUMED ONE,
//     and the panel SAYS SO. ced archives the transcript it drew; the
//     agent's memory lives in an ACP session that died with its process.
//     ACP's session/load could in principle resume one, but it is
//     optional, agent-side, and replays the entire history back as
//     session/update notifications — which would double every message
//     against the transcript ced just restored. So restoring loads text
//     and starts a fresh session, and an info line in the transcript
//     names the gap. Silence there would be the worst outcome: a
//     follow-up question typed against a conversation the model has
//     never seen gets a confidently unrelated answer.
//
//   - The archive is USER-scoped, not per project, and a row names the
//     project when it isn't this one. A conversation is remembered by
//     what was ASKED, and the thing a user reaches for it by ("what did
//     I work out about the caret blink?") does not always live in the
//     folder they are standing in now.

package app

import (
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/rohanthewiz/ced/internal/chatstore"
	"github.com/rohanthewiz/ced/internal/session"
	"github.com/rohanthewiz/ced/internal/userconfig"
)

// chatArchiveDirFn resolves the archive directory. A package var for the
// reason every other config seam is one: without it a test run would
// write fixture transcripts into the developer's real ~/.config/ced/chats
// and prune their actual conversations to make room. newTestApp pins it
// at a temp directory.
var chatArchiveDirFn = userconfig.ChatsDir

// chatArchiveNow is the clock the archive stamps ids and Updated times
// with, so a test can pin an ordering instead of racing the wall clock.
var chatArchiveNow = time.Now

// -----------------------------------------------------------------------------
// Saving the live conversation
// -----------------------------------------------------------------------------

// chatArchiveRoles maps the transcript's role enum onto the archive's
// strings. The stored form is deliberately textual: an archive outlives
// the enum, and a reordered constant block must not silently restyle
// every conversation already on disk.
func chatArchiveRole(r chatMsgRole) string {
	switch r {
	case chatRoleUser:
		return "user"
	case chatRoleAgent:
		return "agent"
	case chatRoleTool:
		return "tool"
	default:
		return "info"
	}
}

// chatRoleFromArchive is chatArchiveRole's inverse. An unknown string
// reads as info — the neutral role, so a transcript written by a future
// ced still loads with its text intact and only its styling degraded.
func chatRoleFromArchive(s string) chatMsgRole {
	switch s {
	case "user":
		return chatRoleUser
	case "agent":
		return chatRoleAgent
	case "tool":
		return chatRoleTool
	default:
		return chatRoleInfo
	}
}

// chatArchiveWorth reports whether the live transcript is worth keeping.
// A conversation with no user prompt in it is one the user never had —
// a panel that opened, printed "starting Copilot chat", and was closed —
// and archiving those would fill the Recent list with rows nobody can
// tell apart or remember creating.
func (a *App) chatArchiveWorth() bool {
	for _, m := range a.chat.msgs {
		if m.role == chatRoleUser {
			return true
		}
	}
	return false
}

// chatArchiveSave writes the live conversation to the archive. Called
// after every completed turn, before the transcript is replaced (a
// clear, an open), and at teardown — so the archive is never more than
// one turn behind what is on screen.
//
// Silent on failure, deliberately: this is background bookkeeping the
// user did not ask for, and a flash about a config directory in the
// middle of reading an answer is noise about a problem they cannot act
// on mid-turn. The failure surfaces where it means something — the
// Recent picker, which will not list what was never written.
func (a *App) chatArchiveSave() {
	if !a.chatArchiveWorth() {
		return
	}
	dir := chatArchiveDirFn()
	if dir == "" {
		return
	}
	now := chatArchiveNow()
	if a.chat.archiveID == "" {
		a.chat.archiveID = chatstore.NewID(now)
	}
	if a.chat.archiveStart.IsZero() {
		a.chat.archiveStart = now
	}
	msgs := make([]chatstore.Msg, 0, len(a.chat.msgs))
	for _, m := range a.chat.msgs {
		msgs = append(msgs, chatstore.Msg{Role: chatArchiveRole(m.role), Text: m.text})
	}
	_ = chatstore.Save(dir, chatstore.Conversation{
		ID:      a.chat.archiveID,
		Title:   chatstore.DeriveTitle(msgs),
		Agent:   a.chatAgent().name,
		Model:   a.chatCurrentModelName(),
		Root:    session.Normalize(a.rootDir),
		Started: a.chat.archiveStart,
		Updated: now,
		Msgs:    msgs,
	})
}

// -----------------------------------------------------------------------------
// New chat
// -----------------------------------------------------------------------------

// chatClearLabel names the ≡ row for what it will actually do. It says
// "saves this one" rather than leaving the user to guess, because the
// word "new" next to a transcript they have been building for an hour
// otherwise reads as a threat.
func (a *App) chatClearLabel() string {
	if a.chatArchiveWorth() {
		return "New chat (saves this one)"
	}
	return "New chat"
}

// menuChatNew is the ≡ Copilot row / Esc-a-x: archive the conversation,
// empty the panel, and put the agent back to a fresh session.
func (a *App) menuChatNew() {
	a.closeMenu()
	a.chatNewChat()
}

// chatNewChat is the verb behind the row and the leader key.
//
// It refuses mid-turn rather than tearing the connection out from under
// a running answer: the agent is mid-sentence, the user is watching it
// arrive, and "stop it first" is a shorter sentence than any explanation
// of where the half-answer went. Same refusal shape as every other verb
// that would disturb a live turn.
func (a *App) chatNewChat() {
	if a.chat.turnActive {
		a.flash(a.chatAgent().name + " is answering — ⏹ to stop it first")
		return
	}
	had := a.chatArchiveWorth()
	a.chatArchiveSave()
	a.chatResetConversation()
	a.chatRestartSession()
	if had {
		a.flash("new chat — the previous one is under ≡ → Recent chats")
	} else {
		a.flash("new chat")
	}
}

// chatResetConversation empties everything that belongs to ONE
// conversation and mints a fresh archive identity.
//
// What deliberately survives: the prompt history ring (Up-arrow recall
// is a typing convenience, the shell-history rule — it spans
// conversations the way a shell's spans directories) and the pending
// context attachments (they describe the message the user has not sent
// yet, exactly as chatDisconnect argues). What does not: the selection,
// whose row numbers are about to mean nothing, and the scroll.
func (a *App) chatResetConversation() {
	a.chat.msgs = nil
	a.chat.scroll = 0
	a.chatClearSelection()
	a.chat.archiveID = ""
	a.chat.archiveStart = time.Time{}
	a.chat.restored = false
}

// chatRestartSession tears the ACP connection down and starts a fresh
// one, which is what makes a cleared panel honest: session/new is the
// only thing that empties the agent's own memory of the conversation.
//
// It is a no-op when nothing was attached — an unopened or dead panel
// has no memory to clear — and it deliberately does NOT clear the dead
// verdict. Reconnecting is a side effect here, not the user asking to
// retry a crashed agent; that gesture is re-picking the backend, and
// quietly borrowing it would turn "new chat" into a spawn attempt on a
// machine where the binary is missing.
func (a *App) chatRestartSession() {
	if a.chat.client == nil && !a.chat.starting {
		return
	}
	a.chatDisconnect()
	a.chatEnsureStarted()
}

// -----------------------------------------------------------------------------
// Recent chats
// -----------------------------------------------------------------------------

// chatRecentLabel names the ≡ row. It stays clickable with an empty
// archive (the MCP/Skills rule — a dimmed row is a dead end that
// explains nothing), so the label is where the empty case is announced.
// Deliberately NOT derived from a directory read: menuLayout runs on
// every frame the menu is open, and a stat-per-frame is the cost this
// avoids by simply saying the same words either way.
func (a *App) chatRecentLabel() string { return "Recent chats…" }

// menuChatRecent opens the archive as a fuzzy picker (the house rule:
// every choose-one-from-a-list UI is the palette).
func (a *App) menuChatRecent() {
	a.closeMenu()
	dir := chatArchiveDirFn()
	metas, err := chatstore.List(dir)
	if err != nil {
		a.openInfo("Recent chats", []string{"Could not read the chat archive:", err.Error()})
		return
	}
	// The live conversation is already on screen; offering to "open" it
	// would either be a no-op row or a gesture that reloads the panel
	// from a copy one turn stale. Same exclusion the recent-folders
	// picker makes for the current root.
	items := make([]paletteItem, 0, len(metas))
	for _, m := range metas {
		if m.ID == a.chat.archiveID {
			continue
		}
		id := m.ID
		items = append(items, paletteItem{
			label: a.chatArchiveRowLabel(m),
			run:   func(app *App) { app.chatOpenArchived(id) },
		})
	}
	if len(items) == 0 {
		a.flash("no saved chats yet — they are archived as you use the panel")
		return
	}
	a.openPicker("Recent chats", items)
}

// chatArchiveRowLabel builds one picker row: when, what was asked, how
// big it got, and — only when it differs from the folder you are in —
// which project it belonged to.
//
// The project marker is conditional because the common case is the
// current one, and a label repeating the same folder on every row spends
// the width that the TITLE needs while distinguishing nothing. Same
// argument the status bar makes for an empty directory on a root-level
// file.
func (a *App) chatArchiveRowLabel(m chatstore.Meta) string {
	title := m.Title
	if title == "" {
		title = "(untitled chat)"
	}
	var b strings.Builder
	b.WriteString(chatArchiveWhen(m.Updated, chatArchiveNow()))
	b.WriteString("  ")
	b.WriteString(title)
	if m.Count > 0 {
		fmt.Fprintf(&b, "  · %d msg", m.Count)
		if m.Count != 1 {
			b.WriteString("s")
		}
	}
	// Both sides go through Normalize before they are compared: the
	// stored root is normalized at save time, but a hand-edited file (or
	// one written by an older ced) may not be, and on macOS the same
	// directory spells itself two ways through /var's symlink — which
	// would tag every row with the project it is already in.
	if proj := chatArchiveProject(m.Root); proj != "" &&
		session.Normalize(m.Root) != session.Normalize(a.rootDir) {
		b.WriteString("  · " + proj)
	}
	return b.String()
}

// chatArchiveWhen renders a timestamp for a picker row: a clock time for
// today, a month and day before that, and the year once it is no longer
// this one. The row is scanned, not read — "what did I do this morning"
// and "that thing last week" are the two questions it answers, and a
// full RFC-3339 stamp answers neither faster while costing a third of
// the row's width.
func chatArchiveWhen(t, now time.Time) string {
	if t.IsZero() {
		return "         "
	}
	t = t.Local()
	now = now.Local()
	switch {
	case t.YearDay() == now.YearDay() && t.Year() == now.Year():
		return t.Format("15:04    ")
	case t.Year() == now.Year():
		return t.Format("Jan _2   ")
	default:
		return t.Format("Jan _2 06")
	}
}

// chatArchiveProject names a conversation's project by its folder's base
// name. The full path would swamp the row, and the base name is what a
// user calls the project anyway.
func chatArchiveProject(root string) string {
	if root == "" {
		return ""
	}
	return filepath.Base(root)
}

// chatOpenArchived loads a saved conversation into the panel.
//
// The live conversation is archived on the way past, so this is
// reversible from the same picker — which is what lets it run without a
// confirmation. The agent is restarted for the clear's reason: continuing
// to talk to a session that remembers the conversation being REPLACED is
// the confusing failure, and it is invisible.
func (a *App) chatOpenArchived(id string) {
	if a.chat.turnActive {
		a.flash(a.chatAgent().name + " is answering — ⏹ to stop it first")
		return
	}
	c, err := chatstore.Load(chatArchiveDirFn(), id)
	if err != nil {
		a.openInfo("Recent chats", []string{"Could not open that conversation:", err.Error()})
		return
	}
	a.chatArchiveSave()
	a.chatResetConversation()
	a.chatRestartSession()
	for _, m := range c.Msgs {
		a.chat.msgs = append(a.chat.msgs, chatMsg{role: chatRoleFromArchive(m.Role), text: m.Text})
	}
	// Continue the SAME archive entry rather than forking a copy: a
	// conversation reopened and carried on is one conversation, and two
	// rows differing only by where the user stopped reading is exactly
	// the list nobody can navigate.
	a.chat.archiveID = c.ID
	a.chat.archiveStart = c.Started
	a.chat.restored = true
	a.chatAppendMsg(chatMsg{role: chatRoleInfo, text: chatRestoreNote(c)})
	// THE PANEL IS REVEALED BEFORE THE SCROLL IS PINNED, and the order is
	// load-bearing rather than tidy. chatMaxScroll is derived from the
	// strip's WIDTH — how many rows the transcript wraps to — and a closed
	// panel has none, so a scroll computed first is measured against a
	// wrapping that does not exist. It came out far past the end, and the
	// restored conversation opened as a blank panel: every row scrolled
	// off the top, with nothing on screen to say why.
	//
	// chatRevealPanel, not chatOpenPanel: reading a saved transcript needs
	// no agent at all, and chatOpenPanel refuses (loudly) when there is
	// none — which would hide the archive on exactly the machine where it
	// is all that survives of the conversation. Starting the agent is
	// still attempted, silently, so a follow-up question has somewhere to
	// go; chatEnsureStarted is a no-op when there is nothing to start.
	a.chatEnsureStarted()
	a.chatRevealPanel()
	// The newest end, where the restore note is and where a follow-up
	// would go — the transcript's ordinary resting place.
	a.chat.scroll = a.chatMaxScroll()
}

// chatRestoreNote is the line that tells the user what they just got —
// and, more importantly, what they did not. The gap between "this text
// is back on screen" and "the model remembers this" is invisible and
// only shows up as a strangely uninformed answer three messages later,
// so it is stated once, in the transcript, where it stays scrolled into
// the record rather than flashing past in the status bar.
func chatRestoreNote(c chatstore.Conversation) string {
	when := ""
	if !c.Updated.IsZero() {
		when = " from " + c.Updated.Local().Format("Jan 2 15:04")
	}
	return "— restored transcript" + when + " —\n" +
		"This is the saved conversation, not a resumed session: the agent has no " +
		"memory of it. Quote what matters, or re-attach the files, before asking a follow-up."
}
