// =============================================================================
// File: internal/app/summarize.go
// Author: Rohan Allison <rohanthewiz@gmail.com>
// Created: 2026-08-17
// Copyright: 2026 Rohan Allison. All rights reserved.
// =============================================================================

// summarize.go is the AI namespace's reading verb: ask the current chat
// agent what the selected text — or the whole file, when nothing is
// selected — actually says.
//
// It owns almost nothing, and that is the point. The three questions a
// feature like this raises were all answered years of house rules ago:
//
//   - WHAT TEXT GOES OUT is a chatAttach (copilot_chat_context.go), so
//     the answer comes from the open BUFFER including unsaved edits, the
//     payload is capped and the cut is announced, and the wire format
//     follows the agent's advertised capabilities. A second content
//     path would be the one place where "summarize this" summarised the
//     stale on-disk copy of the file the user had just changed.
//   - WHERE THE ANSWER GOES is the chat panel, as a normal visible turn
//     (the gitPanelSuggestCommit rule). Not a modal: a summary is prose
//     of unknown length that the user will want to read beside the code,
//     scroll, copy, and ask a follow-up about — which is a transcript,
//     not a dialog. Nothing here claims the answer afterwards.
//   - WHETHER THE AGENT CAN ANSWER is chatUnavailableReason, the one
//     spelling of that sentence, surfaced rather than dimmed away (the
//     menuCopilotAuth rule).
//
// So what is left is the target rule — selection beats file, the same
// narrower-question-wins rule auto-context already follows — and the
// prompt.

package app

import (
	"fmt"
	"strings"
)

// selectionOrFileTarget resolves what a "do this to what I'm looking
// at" gesture covers right now: the selected lines when there is a
// selection, the whole file otherwise. The second return is the reason
// there is nothing to work on, "" when there is — so every surface says
// the same thing about it, and an empty reason is the only "yes".
//
// SELECTION BEATS FILE is the same narrower-question-wins rule
// chatAutoAttachment follows, and sharing this resolver is what keeps
// the two verbs built on it (summarize, capture-to-note) from drifting
// into two different ideas of what "the current text" means. verb names
// the gesture in the refusal, since "open a saved file" alone reads as
// a complaint about the editor rather than about the action.
//
// Selections snap to whole lines for chatAttach's reason — half a line
// of code is not something a model can reason about — which also makes
// the label and the payload describe the same region.
func (a *App) selectionOrFileTarget(verb string) (chatAttach, string) {
	t := a.activeTabPtr()
	if t == nil || t.Path == "" {
		return chatAttach{}, "Open a saved file to " + verb
	}
	if t.IsImage() {
		return chatAttach{}, "Only text files can be " + verb + "d"
	}
	at := chatAttach{path: absolutePathFor(t.Path)}
	if t.HasSelection() {
		at.lineFrom, at.lineTo = chatSelectionLines(t)
	}
	return at, ""
}

// summarizeTarget is selectionOrFileTarget in this verb's words.
func (a *App) summarizeTarget() (chatAttach, string) {
	return a.selectionOrFileTarget("summarize")
}

// canSummarize gates the ≡ row. It asks only whether there is TEXT to
// summarize — agent availability is deliberately absent, for the reason
// commitDraftBlockedReason spells out: a verdict of "not installed" is
// discovered by trying, so a row that vanished on the machine missing
// the binary would hide the feature from the only user who needs to be
// told why.
func (a *App) canSummarize() bool {
	_, why := a.summarizeTarget()
	return why == ""
}

// summarizeLabel names the ≡ row for what it will actually cover, the
// house dynamic-label idiom (codeActionMenuLabel's shape). A user with
// a selection and a user without are asking different questions, and
// the row is where that difference is visible before it costs a turn.
func (a *App) summarizeLabel() string {
	at, why := a.summarizeTarget()
	if why != "" {
		return "Summarize with AI…"
	}
	if at.ranged() {
		return fmt.Sprintf("Summarize selection (%d lines)…", at.lineTo-at.lineFrom+1)
	}
	return "Summarize " + a.relativePathFor(at.path) + "…"
}

// menuSummarize is the ≡ / palette / Esc-a-z entry point.
//
// The panel opens FIRST so the user sees where the answer is coming
// from — a request that streams into a hidden panel looks like a hang —
// and the attachment is added before the send so chatSendPrompt's single
// dispatch point resolves it exactly like a hand-attached file. That
// also means the queued-prompt path (agent still handshaking) carries
// the attachment too, since pending attachments outlive the queue.
func (a *App) menuSummarize() {
	a.closeMenu()
	at, why := a.summarizeTarget()
	if why != "" {
		a.flash(why)
		return
	}
	if a.chat.turnActive {
		a.flash(a.chatAgent().name + " is answering — ⏹ to stop it first")
		return
	}
	a.chatOpenPanel() // flashes its own reason when unavailable
	if !a.chat.open {
		return
	}
	a.chatAttachOnce(at)

	label := a.chatAttachLabel(at)
	a.chatAppendMsg(chatMsg{role: chatRoleUser, text: "Summarize " + label})
	prompt := summarizePrompt(label, at.ranged())
	switch {
	case a.chatReady():
		a.chatSendPrompt(prompt)
	case a.chat.starting:
		// The handshake is still in flight; queue like the composer
		// does. The attachment stays pending and rides the queued send.
		a.chat.queuedPrompt = prompt
		a.chatAppendMsg(chatMsg{role: chatRoleInfo,
			text: "starting " + a.chatAgent().name + " chat — the request will send shortly"})
	default:
		a.chatAppendMsg(chatMsg{role: chatRoleInfo,
			text: a.chatAgent().name + " chat is not running — " + chatAgentRetryHint + " to restart"})
	}
}

// chatAttachOnce adds at to the pending set unless the same ground is
// already covered.
//
// Deliberately NOT chatAddAttachment: that one is the user's own attach
// gesture, so it flashes ("Attached …", or "… is already attached") and
// opens the panel. Here the attachment is machinery in service of a
// verb the user asked for by name, and the duplicate case is the COMMON
// one — with auto-context on, the active file's attachment is already
// synthesized for every turn, so a flash saying so would be noise on
// the default configuration.
func (a *App) chatAttachOnce(at chatAttach) {
	for _, have := range a.chatPendingAttachments() {
		if have.same(at) {
			return
		}
	}
	a.chat.attach = append(a.chat.attach, at)
}

// summarizePrompt is the wire prompt.
//
// It asks for prose rather than a shape, unlike the commit-message
// prompt: that answer had to land in a single-line field, so
// over-specifying was cheaper than parsing what came back. This one
// lands in the transcript, which word-wraps prose and hard-wraps fenced
// code, so the only real constraints are LENGTH (the panel is a narrow
// strip — an essay is unreadable there) and that the answer describe the
// text rather than review it. "What does this do" is the question; a
// list of suggested improvements is a different one the user can ask as
// a follow-up, in the panel that is now open in front of them.
func summarizePrompt(label string, ranged bool) string {
	var b strings.Builder
	what := "file"
	if ranged {
		what = "excerpt"
	}
	fmt.Fprintf(&b, "Summarize the attached %s (%s).\n", what, label)
	b.WriteString("Lead with one sentence saying what it is and what it is for. ")
	b.WriteString("Then, if there is more worth saying, add a short bullet list of ")
	b.WriteString("the key parts — types, functions, sections — and what each does.\n")
	b.WriteString("Describe what the text says; do not review it, do not suggest changes, ")
	b.WriteString("and do not restate it line by line. Keep the whole answer under 200 words.")
	return b.String()
}
