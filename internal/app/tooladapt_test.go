// =============================================================================
// File: internal/app/tooladapt_test.go
// Author: Rohan Allison <rohanthewiz@gmail.com>
// Created: 2026-09-10
// Copyright: 2026 Rohan Allison. All rights reserved.
// =============================================================================

package app

import "testing"

// TestOpenGitPanelTool_RefreshesAndStopsTheWalk covers both halves of
// the git panel's adapter: opening refreshes so the list reflects this
// instant rather than the last 10-second tick, and closing stops a
// survey — a walk cannot outlive the surface it walks.
func TestOpenGitPanelTool_RefreshesAndStopsTheWalk(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	a.gitIsRepo = true

	a.openGitPanelTool()
	if !a.gitPanel.open {
		t.Fatal("the panel should be showing")
	}

	a.gitPanel.walk = true
	a.closeGitPanelTool()
	if a.gitPanel.open {
		t.Error("the panel should be closed")
	}
	if a.gitPanel.walk {
		t.Error("a survey must not outlive the panel it walks")
	}
}

// TestCloseGitLogTool_DropsTheSearchFocus pins the reason the log's
// close verb is not just a flag write: a focused search field nobody can
// see would still be eating keystrokes meant for the editor.
func TestCloseGitLogTool_DropsTheSearchFocus(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	a.gitIsRepo = true
	a.openGitLogTool()
	a.gitLog.filter.focused = true

	a.closeGitLogTool()
	if a.gitLog.open {
		t.Error("the panel should be closed")
	}
	if a.gitLog.filter.focused {
		t.Error("the search field kept focus after the panel closed")
	}
}

// TestShowCompareTool_AsksWhenThereIsNothingToCompare pins the one tool
// whose "show" is a QUESTION: a diff has two named sides, so there is no
// such thing as opening this panel empty. An unprimed panel sends the
// user to the picker and reports false, because nothing is on screen.
func TestShowCompareTool_AsksWhenThereIsNothingToCompare(t *testing.T) {
	a := newTestApp(t, t.TempDir())

	if a.showCompareTool() {
		t.Error("an unprimed compare must not report itself as shown")
	}
	if a.compare.open {
		t.Error("nothing should be on screen yet")
	}
	if _, ok := a.modal.(*paletteModal); !ok {
		t.Errorf("modal = %T, want the source picker", a.modal)
	}
	a.closeAllModals()

	// A panel that already holds a comparison re-opens with it.
	a.compare.oldLabel = "main.go (saved)"
	a.compare.newLabel = "main.go"
	if !a.showCompareTool() {
		t.Error("a primed compare should open")
	}
	if !a.compare.open {
		t.Error("the panel should be showing")
	}
}

// TestShowChatTool_RefusesWithoutAnAgent pins the menuCopilotAuth rule
// at the tool layer: unavailability is explained with a flash rather
// than a silent dead end, and the verb reports false so no stripe button
// latches on over a panel that never opened.
func TestShowChatTool_RefusesWithoutAnAgent(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	a.chat.dead = true

	if a.showChatTool() {
		t.Error("a chat with no agent must report failure")
	}
	if a.chat.open {
		t.Error("nothing should be on screen")
	}
	if a.statusMsg == "" {
		t.Error("the refusal should say why")
	}
}

// TestChatClosePanel_DropsFocusAndSelection pins what a chat close takes
// with it: the keyboard, and the transcript selection — whose row
// numbers are measured in DERIVED rows that are about to stop being
// drawn. The transcript, attachments and prompt history all survive.
func TestChatClosePanel_DropsFocusAndSelection(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	wireChat(a)
	a.menuToggleChat()
	if !a.chat.open {
		t.Fatal("setup: chat should be open")
	}
	a.chat.msgs = []chatMsg{{role: chatRoleUser, text: "hello"}}
	a.chat.selActive = true
	a.chat.history = []string{"hello"}

	a.chatClosePanel()
	if a.chat.open || a.chat.focused {
		t.Errorf("open=%v focused=%v, want both false", a.chat.open, a.chat.focused)
	}
	if a.chat.selActive {
		t.Error("the selection's rows are about to stop existing — it should be cleared")
	}
	if len(a.chat.msgs) != 1 || len(a.chat.history) != 1 {
		t.Error("closing the panel must not lose the transcript or the prompt history")
	}
}

// TestHideTerminalTool_KeepsTheSession pins what makes hiding the
// terminal cheap enough to do reflexively: the shell keeps running and
// the scrollback is intact, so Esc-` brings it straight back.
func TestHideTerminalTool_KeepsTheSession(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	a.showTerminalTool()
	if !a.term.open || !a.term.focused {
		t.Fatalf("open=%v focused=%v, want an open, focused terminal", a.term.open, a.term.focused)
	}
	sess := a.term.sess
	if sess == nil {
		t.Fatal("showing the terminal should start a session")
	}

	a.hideTerminalTool()
	if a.term.open || a.term.focused {
		t.Error("the panel should be closed and unfocused")
	}
	if a.term.sess != sess {
		t.Error("hiding must not discard the shell session")
	}
}

// TestShowTool_RunsTheAdapterNotJustTheFlag is the seam's contract in
// one assertion: showTool reaches the panel's real open verb, so the
// machinery around the flag (a refresh, a session, a selection) happens.
func TestShowTool_RunsTheAdapterNotJustTheFlag(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	if !a.showTool(toolTerminal) {
		t.Fatal("the terminal should open")
	}
	if a.term.sess == nil {
		t.Error("showTool bypassed the adapter — no shell session was started")
	}
}
