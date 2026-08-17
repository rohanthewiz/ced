// =============================================================================
// File: internal/app/gonotes.go
// Author: Rohan Allison <rohanthewiz@gmail.com>
// Created: 2026-08-17
// Copyright: 2026 Rohan Allison. All rights reserved.
// =============================================================================

// gonotes.go captures the selected text — or the whole file, when
// nothing is selected — into GoNotes as a new note. The selection IS the
// body; the title is the one thing that has to come from somewhere else,
// so it is asked for, with the agent available to draft it.
//
// House rules:
//
//   - THE SELECTION IS THE BODY, VERBATIM. Nothing is prepended to it:
//     a note is a document the user will open and edit later, and a
//     header ced invented would be the first thing they had to delete.
//     Provenance goes in the note's DESCRIPTION instead, which is where
//     GoNotes shows a subtitle and where it stays out of the text.
//   - THE TEXT COMES FROM THE BUFFER (attachContent, shared with the
//     chat attachments). You capture what you are looking at, unsaved
//     edits included — saving the stale on-disk copy of a file the user
//     just changed is the one failure this feature could have that
//     nobody would notice until much later.
//   - NOTHING SAVES WITHOUT AN ENTER. The agent's title only ever
//     pre-fills the prompt, the same contract the drafted commit
//     message has (gitcommitmsg.go), and for the same reason: a machine
//     wrote the sentence, so the user gets the last word on it.
//   - THE AGENT IS A REASON, NOT A GATE. The ✦ button sits on the
//     prompt whatever state the agent is in and says why when it can't
//     ask (the menuCopilotAuth rule). The feature itself needs no agent
//     at all — a typed title is the normal path.
//   - AVAILABILITY OF THE SERVER IS DISCOVERED BY TRYING, so the ≡ row
//     never dims on it and no probe runs at startup. GoNotes is a
//     separate process the user starts and stops; a ced that pinged it
//     to decide whether to draw a menu row would be wrong exactly as
//     often as the server is restarted.
//
// The transport, the credentials and why they are environment variables
// rather than a ced config key all live in internal/gonotes.

package app

import (
	"strings"
	"time"

	"github.com/rohanthewiz/ced/internal/gonotes"
)

const (
	// noteBodyMaxBytes caps one captured note. Far above the chat
	// attachment cap (64KB) because the cost model is different: this
	// text goes into a database, not into a prompt turn, so it pays no
	// tokens and no premium multiplier. It is capped at all only so a
	// stray capture of a multi-megabyte generated file can't wedge a
	// single-writer database behind one enormous row.
	noteBodyMaxBytes = 1 << 20

	// noteExcerptMaxBytes is what the AGENT is shown when drafting a
	// title. A title comes from the first screenful — what this text is
	// — so sending the rest buys nothing and costs a premium request.
	noteExcerptMaxBytes = 8 << 10

	// notePrivateChipWidth reserves the row for the WIDER of the chip's
	// two labels, so toggling it never moves the target under the
	// pointer (promptExtra's rule).
	notePrivateChipWidth = 15 // len("[private: off]") + 1
)

// gonotesCreate is the seam every send goes through — and the seam
// tests replace, so no test run can post a developer's fixture text
// into their real notes database (the pluginShell precedent, which
// matters here for the same reason: the side effect is outside ced).
var gonotesCreate = func(n gonotes.Note) (gonotes.Result, error) {
	return gonotes.New().Create(n)
}

// -----------------------------------------------------------------------------
// The gesture
// -----------------------------------------------------------------------------

// noteTarget is selectionOrFileTarget in this verb's words.
func (a *App) noteTarget() (chatAttach, string) {
	return a.selectionOrFileTarget("send to GoNotes")
}

// canSendToNotes gates the ≡ row on the only question it can honestly
// answer: is there text to capture. Whether a GoNotes server is running
// is not asked — see the header's note on discovery-by-trying.
func (a *App) canSendToNotes() bool {
	_, why := a.noteTarget()
	return why == ""
}

// sendToNotesLabel names the ≡ row for what it will actually capture,
// the house dynamic-label idiom.
func (a *App) sendToNotesLabel() string {
	at, why := a.noteTarget()
	if why != "" {
		return "Send to GoNotes…"
	}
	if at.ranged() {
		return "Send selection to GoNotes…"
	}
	return "Send " + a.relativePathFor(at.path) + " to GoNotes…"
}

// menuSendToNotes is the ≡ / palette / Esc-a-n entry point: resolve the
// target, then ask for the title.
func (a *App) menuSendToNotes() {
	a.closeMenu()
	at, why := a.noteTarget()
	if why != "" {
		a.flash(why)
		return
	}
	a.openNotePrompt(at, a.defaultNoteTitle(at), false, false)
}

// defaultNoteTitle is the title the prompt opens holding: the file's
// project-relative path, plus the line range when the capture is a
// selection. It is a real answer rather than a placeholder — Enter on it
// produces a note the user can find again — which is what makes both the
// typed and the drafted paths optional rather than required.
func (a *App) defaultNoteTitle(at chatAttach) string {
	return a.chatAttachLabel(at)
}

// noteDescription is the provenance line stored beside the note. It says
// where the text came from without touching the text itself, and it
// names the PROJECT as well as the path — a bare "internal/app/find.go"
// means nothing in a notes database fed by a dozen repositories.
func (a *App) noteDescription(at chatAttach) string {
	label := a.chatAttachLabel(at)
	if root := projectName(a.rootDir); root != "" {
		return "ced: " + root + "/" + label
	}
	return "ced: " + label
}

// projectName is the last element of the project root's path, "" when
// there isn't one worth naming.
func projectName(root string) string {
	root = strings.TrimRight(root, "/")
	if root == "" || root == "/" {
		return ""
	}
	if i := strings.LastIndexByte(root, '/'); i >= 0 {
		return root[i+1:]
	}
	return root
}

// -----------------------------------------------------------------------------
// The prompt
// -----------------------------------------------------------------------------

// openNotePrompt asks for the title and sends on Enter.
//
// drafted says the title came from the agent, which changes exactly one
// thing: whether the ✦ button re-drafts a suggestion or makes the first
// one. private is the per-invocation privacy decision, carried through
// the round trip (noteTitleReq) rather than re-read — a user who
// switched the chip on and then asked for another title has already
// answered that question.
func (a *App) openNotePrompt(at chatAttach, initial string, drafted, private bool) {
	// Captured by the closures below so the chip, the ✦ re-draft and the
	// send all read ONE value.
	priv := private
	send := func(app *App, title string) {
		app.startNoteSend(at, title, priv)
	}
	extras := []promptExtra{
		// extras[0] holds the right edge (promptExtra), so the ✦ button
		// sits in the same place whether or not the chip moves.
		{
			label: func(*App) string { return "[ ✦ AI ]" },
			width: runeLen("[ ✦ AI ]"),
			key:   'a',
			run: func(app *App) {
				// Checked BEFORE the modal closes: a refusal must not
				// cost the user the title they had already typed.
				if why := app.commitDraftBlockedReason(); why != "" {
					app.flash(why)
					return
				}
				app.closeModal()
				app.noteSuggestTitle(at, priv)
			},
		},
		// Unlike the commit prompt's trailer chip this one appears
		// always, drafted or not: privacy is a statement about the TEXT
		// being captured, which is equally true whoever wrote its title.
		{
			label: func(*App) string { return notePrivateChipLabel(priv) },
			width: notePrivateChipWidth,
			key:   'p',
			run:   func(*App) { priv = !priv },
		},
	}
	title := "New GoNotes note"
	if drafted {
		title = "New GoNotes note (drafted)"
	}
	a.openPromptExtras(title, notePromptHint, initial, extras, send)
}

// notePromptHint is the prompt's subtitle. It names both chords because
// the modal owns the keyboard: the ≡ menu — where every other keyboard
// twin in this editor is discovered — is unreachable from inside a
// prompt, so the hint row is the only place they can be advertised.
const notePromptHint = "title   ·   alt+a = ✦ AI   ·   alt+p = private"

// notePrivateChipLabel spells the state in words rather than a tick: the
// chip is small, and "on"/"off" needs no glyph decoding and no color the
// theme could flatten.
func notePrivateChipLabel(on bool) string {
	if on {
		return "[private: on]"
	}
	return "[private: off]"
}

// -----------------------------------------------------------------------------
// Sending
// -----------------------------------------------------------------------------

// gonotesDoneEvent carries a finished send from the posting goroutine to
// the main loop, which is the only place App state may be touched.
type gonotesDoneEvent struct {
	when  time.Time
	title string
	res   gonotes.Result
	err   error
}

// When satisfies the tcell.Event interface.
func (e *gonotesDoneEvent) When() time.Time { return e.when }

// startNoteSend resolves the body and posts the note off-loop.
//
// The body is resolved HERE, on the main loop, not on the goroutine:
// which files are open and which buffers are dirty is main-loop-only
// state, so reading it across that boundary would re-open the staleness
// window this feature exists to close (planWorkspaceEdit's argument, at
// a much smaller scale).
func (a *App) startNoteSend(at chatAttach, title string, private bool) {
	body, truncated, err := a.attachContent(at, noteBodyMaxBytes)
	if err != nil {
		a.flash("GoNotes: " + err.Error())
		return
	}
	if strings.TrimSpace(body) == "" {
		a.flash("Nothing to send — the selection is empty")
		return
	}
	note := gonotes.Note{
		Title:       title,
		Body:        body,
		Description: a.noteDescription(at),
		// A fixed tag so everything ced captured is findable as a set in
		// GoNotes' own search, without the user having to remember what
		// they called it.
		Tags:    "ced",
		Private: private,
	}
	if truncated {
		// Announced, never silent: a note the user believes holds a
		// whole file must not quietly hold half of it (the project
		// search's cap rule).
		a.flash("Sending to GoNotes (body cut at " + chatByteLabel(noteBodyMaxBytes) + ")…")
	} else {
		a.flash("Sending to GoNotes…")
	}
	scr := a.screen
	if scr == nil {
		return
	}
	go func() {
		res, err := gonotesCreate(note)
		_ = scr.PostEvent(&gonotesDoneEvent{when: time.Now(), title: note.Title, res: res, err: err})
	}()
}

// handleGonotesDone reports the outcome. Success is a flash — the note
// is elsewhere now and there is nothing to act on here. A FAILURE opens
// the info modal instead, because these messages carry the address that
// was dialed and the server's own words, and a flash that scrolls away
// after a few seconds is not enough to act on ("connection refused" is
// only useful once you can read which URL refused). Nothing is lost
// either way: the text is still in the buffer.
func (a *App) handleGonotesDone(e *gonotesDoneEvent) {
	if e == nil {
		return
	}
	if e.err != nil {
		a.openInfo("GoNotes", []string{
			"Could not save the note.",
			"",
			e.err.Error(),
			"",
			"Server: " + gonotes.URL() + "  (override with " + gonotes.EnvURL + ")",
		})
		return
	}
	a.flash("Saved to GoNotes: " + e.title)
}

// -----------------------------------------------------------------------------
// Asking the agent for a title
// -----------------------------------------------------------------------------

// noteTitleReq is the in-flight "suggest a title" turn. It remembers
// what the title is FOR (so the prompt reopens over the same capture),
// which connection generation asked (a torn-down connection's answer is
// not ours), and where the transcript stood at send time — the answer is
// whatever agent prose appears after that mark, never an older message
// that happens to be trailing. The same discipline every other chat
// result follows.
type noteTitleReq struct {
	at      chatAttach
	seq     int
	mark    int
	private bool
}

// noteSuggestTitle asks the current agent to name the capture. Like the
// commit-message draft it is a NORMAL VISIBLE TURN in the chat panel,
// not a hidden side session: the user watches it stream, can stop it,
// and the request stays in the transcript as the record of what the
// title was drafted from.
func (a *App) noteSuggestTitle(at chatAttach, private bool) {
	if a.chat.turnActive {
		a.flash(a.chatAgent().name + " is answering — ⏹ to stop it first")
		return
	}
	a.chatOpenPanel() // flashes its own reason when unavailable
	if !a.chat.open {
		return
	}
	// The excerpt is resolved now so a failure is reported before a turn
	// is spent, and so the agent is shown the same buffer text the send
	// will capture.
	excerpt, _, err := a.attachContent(at, noteExcerptMaxBytes)
	if err != nil {
		a.flash("GoNotes: " + err.Error())
		return
	}
	label := a.chatAttachLabel(at)
	a.chatAppendMsg(chatMsg{role: chatRoleUser, text: "Suggest a note title for " + label})
	prompt := noteTitlePrompt(label, excerpt)
	switch {
	case a.chatReady():
		a.chat.noteTitle = &noteTitleReq{
			at:      at,
			seq:     a.chat.connSeq,
			mark:    len(a.chat.msgs),
			private: private,
		}
		a.chatSendPrompt(prompt)
	case a.chat.starting:
		// The handshake is still in flight. Queue like the composer
		// does — but WITHOUT a pending request, because the queued send
		// happens under a generation we can't name yet; the suggestion
		// then simply lands in the transcript to copy.
		a.chat.queuedPrompt = prompt
		a.chatAppendMsg(chatMsg{role: chatRoleInfo,
			text: "starting " + a.chatAgent().name + " chat — the request will send shortly"})
	default:
		a.chatAppendMsg(chatMsg{role: chatRoleInfo,
			text: a.chatAgent().name + " chat is not running — " + chatAgentRetryHint + " to restart"})
	}
}

// noteTitlePrompt is the wire prompt. It over-specifies the shape for
// commitSuggestPrompt's reason: the answer lands in a SINGLE-LINE input
// field, so asking for one bare line is cheaper and more reliable than
// parsing prose back out of a chatty reply.
//
// The text is inlined rather than attached because it is already
// truncated to an excerpt — an attachment would say "the file" while
// carrying a fragment of it.
func noteTitlePrompt(label, excerpt string) string {
	var b strings.Builder
	b.WriteString("Write a short title for a note holding the text below (from ")
	b.WriteString(label)
	b.WriteString(").\n")
	b.WriteString("Answer with ONLY the title: one line, at most 60 characters, ")
	b.WriteString("no quotes, no code fences, no explanation, no trailing period.\n")
	b.WriteString("Name what the text IS, so it can be found again later.\n\n")
	b.WriteString("Text:\n```\n")
	b.WriteString(excerpt)
	if !strings.HasSuffix(excerpt, "\n") {
		b.WriteString("\n")
	}
	b.WriteString("```\n")
	return b.String()
}

// chatNoteTitleDone is handleChatTurnDone's second tail: if this turn was
// a note-title request, lift the suggestion out of the transcript and
// reopen the note prompt with it. Consumes the request either way — a
// suggestion belongs to the turn that asked, and reusing it on a later
// answer would put an unrelated sentence on a note.
func (a *App) chatNoteTitleDone(e *chatTurnDoneEvent) {
	req := a.chat.noteTitle
	if req == nil || req.seq != a.chat.connSeq {
		return
	}
	a.chat.noteTitle = nil
	if e.err != nil || e.stopReason == "cancelled" {
		return // handleChatTurnDone already wrote the reason
	}
	title := agentOneLine(a.chatAgentTextSince(req.mark), "title:", "note title:")
	if title == "" {
		a.flash("No title suggested")
		return
	}
	// Never steal the modal slot from a permission prompt the agent is
	// blocked on — that request has to be answered for anything to
	// proceed. The suggestion stays readable in the transcript.
	if a.chat.permModal != nil || len(a.chat.permQueue) > 0 {
		a.flash("Note title suggested — see the chat panel")
		return
	}
	a.openNotePrompt(req.at, title, true, req.private)
}
