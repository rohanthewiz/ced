// =============================================================================
// File: internal/app/tooladapt.go
// Author: Rohan Allison <rohanthewiz@gmail.com>
// Created: 2026-09-10
// Copyright: 2026 Rohan Allison. All rights reserved.
// =============================================================================

// tooladapt.go is the seam between the tool-window layer and the panels
// themselves: for each tool, the pair of verbs the registry calls to put
// it on screen and take it off again.
//
// WHY THESE ARE THEIR OWN FILE. Every one of them is an EXTRACTION — the
// body of a `menuToggleX` minus the toggle and minus the "close all the
// others" block, which is now claimDock's job. Keeping the extractions
// together makes the pattern visible: a panel's show verb does its own
// machinery (refresh the list, start the shell, connect the agent) and
// nothing about the layout, because the layout is decided one floor up.
// Scattered back into six files, the next panel would inevitably grow a
// seventh copy of the exclusivity block that this whole change removes.
//
// EVERY SHOW REPORTS WHETHER IT WORKED. Three of them can genuinely
// refuse — a git panel outside a repository, a chat with no agent
// binary, a compare with nothing to compare — and a caller that assumed
// success would leave a stripe button lit over a panel that never
// opened. The ones that cannot fail still return true rather than
// nothing, so the registry has one signature and no special cases.

package app

// -----------------------------------------------------------------------------
// Project (the file tree)
// -----------------------------------------------------------------------------
//
// The project tool has no adapter of its own: showing it is
// `sidebarShown = true` and there is no machinery around that, so the
// registry states both hooks inline. Its WIDTH is the one thing about a
// tool window that does not live in the layout's size map — App.
// sidebarWidth stays where it is because auto-fit (treeautofit.go)
// re-derives it on every frame and 40-odd tests pin it. The layout's
// serialiser reads and writes that field instead of shadowing it, which
// is one honest special case with a real reason rather than two numbers
// that can disagree.

// -----------------------------------------------------------------------------
// Git changes panel
// -----------------------------------------------------------------------------

// openGitPanelTool puts the changes panel up and refreshes it
// immediately, so the list reflects this instant rather than the last
// 10-second tick.
func (a *App) openGitPanelTool() {
	a.gitPanel.open = true
	a.refreshGitPanelFiles()
}

// closeGitPanelTool collapses the panel. A survey cannot outlive the
// surface it walks, so it stops here; the reviewed marks do survive, and
// reopening offers "Resume 3/7 ▶".
func (a *App) closeGitPanelTool() {
	a.gitPanel.open = false
	a.stopGitPanelWalk()
}

// -----------------------------------------------------------------------------
// Git log
// -----------------------------------------------------------------------------

// openGitLogTool puts the history browser up. The refresh is the
// EXPLICIT one, not the pipeline's: re-opening the panel with a filter
// still applied must re-run the search, which the pipeline refresh
// deliberately does not do (see refreshGitLogCommits).
func (a *App) openGitLogTool() {
	a.gitLog.open = true
	a.gitLogRefreshNow()
}

// closeGitLogTool collapses the history browser and drops any focused
// search field with it — a field nobody can see must not still be
// eating keystrokes.
func (a *App) closeGitLogTool() {
	a.gitLog.open = false
	a.gitLog.filter.focused = false
}

// -----------------------------------------------------------------------------
// Problems
// -----------------------------------------------------------------------------

// openProblemsTool puts the worklist up, refreshes it so it reflects
// this instant rather than whenever the server last spoke, and lands on
// the ACTIVE file's first problem — the panel lists the whole project,
// but the user arrived from a specific file and the row they meant is
// almost never row one of some other file.
func (a *App) openProblemsTool() {
	a.problems.open = true
	a.refreshProblems()
	a.problemsSelectActiveFile()
}

// closeProblemsTool collapses the worklist. Its rows and selection stay
// on the state: the next/previous-problem verbs walk the same list
// without needing it on screen.
func (a *App) closeProblemsTool() {
	a.problems.open = false
}

// -----------------------------------------------------------------------------
// Compare
// -----------------------------------------------------------------------------

// showCompareTool is what a stripe button or a ≡ show row does for the
// compare panel, and it is the one tool whose "show" is a QUESTION.
//
// A diff is of two named sides, so there is no such thing as opening
// this panel empty — a box saying nothing is worse than a row that
// explains itself. So a panel that already has a comparison re-opens
// with it; one that does not sends the user to the same picker the ≡
// Compare group's rows use, and reports false, because nothing is on
// screen yet.
func (a *App) showCompareTool() bool {
	if a.compare.oldLabel != "" || a.compare.newLabel != "" {
		a.openComparePanel()
		return true
	}
	a.openCompareSourcePicker()
	return false
}

// openCompareSourcePicker asks WHICH comparison, using the palette (the
// house rule that every choose-one-from-a-list UI reuses it). It exists
// for the stripe button and the ≡ show row, which name the PANEL rather
// than a comparison — the three ≡ Compare rows still go straight to
// their own verb, since a user who picked "Compare with pasted text" has
// already answered this question.
func (a *App) openCompareSourcePicker() {
	a.openPicker("Compare current file with", []paletteItem{
		{label: "Another file…", run: func(app *App) { app.menuCompareFile() }},
		{label: "Its saved copy on disk", run: func(app *App) { app.menuCompareSaved() }},
		{label: "Pasted text", run: func(app *App) { app.menuComparePaste() }},
	})
}

// -----------------------------------------------------------------------------
// Terminal
// -----------------------------------------------------------------------------

// showTerminalTool puts the grsh strip up, starts a session if this is
// the first time, and gives it the keyboard — the terminal is the one
// tool you open in order to type into it.
func (a *App) showTerminalTool() bool {
	a.term.open = true
	a.term.focused = true
	a.ensureTermSession()
	return true
}

// hideTerminalTool collapses the strip. The SESSION survives — a running
// command keeps running and the scrollback is intact — which is what
// makes hiding cheap enough to do reflexively.
func (a *App) hideTerminalTool() {
	a.term.open = false
	a.term.focused = false
}

// -----------------------------------------------------------------------------
// Chat
// -----------------------------------------------------------------------------

// showChatTool opens the chat strip and starts the agent, explaining
// unavailability with a flash instead of a dimmed row (the
// menuCopilotAuth rule: a first-touch surface must never be a silent
// dead end). It reports false when the agent could not be reached, so
// the stripe button does not latch on over a panel that never opened.
func (a *App) showChatTool() bool {
	if why := a.chatUnavailableReason(); why != "" {
		a.flash(why)
		return false
	}
	a.chatEnsureStarted()
	// Asked again: starting is what DISCOVERS a missing binary, so the
	// first attempt on a machine without the agent installed only has an
	// honest answer after it.
	if why := a.chatUnavailableReason(); why != "" {
		a.flash(why)
		return false
	}
	a.chatRevealPanel()
	return true
}

// chatClosePanel collapses the chat strip and drops its keyboard focus.
// The transcript, the pending attachments and the prompt history all
// survive — see chatarchive.go for what actually ends a conversation.
func (a *App) chatClosePanel() {
	a.chat.open = false
	a.chat.focused = false
	// The transcript selection goes too: it is measured in DERIVED-row
	// coordinates, and the rows it named are about to stop being drawn.
	a.chatClearSelection()
}
