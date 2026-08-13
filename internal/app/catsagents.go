// =============================================================================
// File: internal/app/catsagents.go
// Author: Rohan Allison <rohanthewiz@gmail.com>
// Created: 2026-08-12
// Copyright: 2026 Rohan Allison. All rights reserved.
// =============================================================================

// Agents as collaborators: the coding agents running in the OTHER panes of
// this cats session become things the editor can see and talk to.
//
// cats already arbitrates agent identity per pane — it knows that pane 26 is
// claude and that claude is currently blocked — and pane.list hands that over
// for free. So three things become possible without ced knowing anything
// about any agent's protocol:
//
//  1. **See.** A status-bar segment names the sibling that most wants
//     attention ("claude: blocked"), and clicking it focuses that pane. The
//     editor stops being a place you have to leave to find out what the
//     agent is doing.
//  2. **Quote.** "Send selection to ⟨pane⟩" types the selection into the
//     agent's prompt with a file:line reference in front of it — STAGED, not
//     submitted. Putting words in an agent's mouth is one act; pressing
//     Enter for it is a different one, and the second is the user's.
//  3. **Ask.** "Ask cats chat about selection" sends the same quote to the
//     HOST's own chat panel instead, which is the right door when the
//     question is about the code rather than for a particular agent.
//
// WHY THE PANE LIST IS CACHED, AND WHEN IT REFRESHES
//
// Everything here runs on the main loop — a status-bar segment is built
// inside a draw call — and pane.list is a socket round trip. So the list is
// polled on a goroutine and read from a cache, refreshed at the three
// moments it can go stale: Tier 1 coming up, a pane_notify (which IS an
// agent state change), and focus moving. A cache that is a second behind is
// invisible; a draw call that dials a socket is not.
//
// MULTI-LINE INPUT IS SAFE HERE, which is not obvious. cats paste-encodes
// send_input against the pane's live mode state, so a TUI with bracketed
// paste on (which every agent has) receives a twenty-line selection as one
// pasted block instead of executing it line by line. That is what makes
// "send the selection" a sane verb rather than a way to fire nineteen
// accidental prompts.

package app

import (
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/rohanthewiz/ced/internal/cats"
	"github.com/rohanthewiz/ced/internal/editor"
)

// catsPanePollMin floors the gap between two pane.list reads, for the same
// reason the theme poll has one: focus moves far more often than the pane
// list changes, and the poll is cheap but not free.
const catsPanePollMin = 2 * time.Second

// catsAgentRank orders agent states by how much they want a human. It is
// the whole editorial judgment of the status segment: with three agents
// running, the one worth a strip of the status bar is the one that has
// STOPPED and is waiting, not the one making progress.
var catsAgentRank = map[string]int{
	cats.StateBlocked: 3,
	cats.StateWorking: 2,
	cats.StateIdle:    1,
}

// catsPollPanes refreshes the cached pane list off the main loop.
// Rate-limited and Tier-1 gated, so call sites can fire it at any moment
// that might have changed something.
func (a *App) catsPollPanes() {
	if !a.catsTier1() || a.screen == nil {
		return
	}
	now := time.Now()
	if !a.cats.panesPolledAt.IsZero() && now.Sub(a.cats.panesPolledAt) < catsPanePollMin {
		return
	}
	a.cats.panesPolledAt = now
	client, scr := a.cats.client, a.screen
	go func() {
		panes, err := client.PaneList()
		if err != nil {
			return // keep whatever we have; a stale name beats a blank bar
		}
		_ = scr.PostEvent(&catsEvent{when: time.Now(), kind: catsKindPanes, panes: panes})
	}()
}

// catsAgentPanes returns the sibling panes running an agent, most
// attention-worthy first.
//
// Our own pane is excluded, and not only for tidiness: ced reports itself as
// the agent "ced" through the hook socket (cats_glue.go), so without the
// self-check the editor would offer to send the user's selection to itself.
func (a *App) catsAgentPanes() []cats.PaneInfo {
	out := make([]cats.PaneInfo, 0, len(a.cats.panes))
	for _, p := range a.cats.panes {
		if p.Agent == "" {
			continue
		}
		if a.cats.selfOK && p.Pane == a.cats.self {
			continue
		}
		out = append(out, p)
	}
	// Stable ordering: state first, then pane id. The id tiebreak is what
	// keeps a status segment from flickering between two equally busy
	// agents on every poll — the pane list's own order is not promised.
	sort.SliceStable(out, func(i, j int) bool {
		ri, rj := catsAgentRank[out[i].AgentState], catsAgentRank[out[j].AgentState]
		if ri != rj {
			return ri > rj
		}
		return out[i].Pane < out[j].Pane
	})
	return out
}

// catsAgentStatusSegment is the status bar's sibling-agent text ("" when
// there is nothing to say). One agent gets a segment, not all of them: the
// bar is a strip, not a dashboard, and the ranked first is the one the user
// would have gone looking for.
func (a *App) catsAgentStatusSegment() string {
	panes := a.catsAgentPanes()
	if len(panes) == 0 {
		return ""
	}
	p := panes[0]
	label := p.Agent
	if p.AgentState != "" && p.AgentState != cats.StateIdle {
		// Idle carries no state word: "claude" says everything an idle
		// agent has to say, and "claude: idle" is two words for it.
		label += ": " + p.AgentState
	}
	if len(panes) > 1 {
		// The others are reachable through the click; the count is what
		// says they exist without naming three agents in a status bar.
		label += fmt.Sprintf(" +%d", len(panes)-1)
	}
	return label
}

// catsFocusAgent is the segment's click: jump to the agent's pane. With one
// sibling it goes straight there; with several it asks which, because
// guessing wrong means the user's screen just moved somewhere they did not
// ask to go.
func (a *App) catsFocusAgent() {
	panes := a.catsAgentPanes()
	switch len(panes) {
	case 0:
		a.flash("No agent panes in this cats session")
	case 1:
		a.catsFocusPane(panes[0])
	default:
		a.catsPickAgentPane("Focus agent pane", (*App).catsFocusPane)
	}
}

// catsFocusPane focuses one pane, off the main loop.
func (a *App) catsFocusPane(p cats.PaneInfo) {
	if !a.catsTier1() {
		return
	}
	client, scr := a.cats.client, a.screen
	id := p.Pane
	go func() {
		if err := client.PaneFocus(id); err != nil {
			catsPostNotice(scr, "Focus failed: "+err.Error())
		}
		// Success says nothing: the user is now looking at the pane they
		// asked for, and a status line about it would be on a screen they
		// have already left.
	}()
}

// catsPickAgentPane opens the agent panes as a picker and runs act on the
// chosen one. The house rule for every choose-one-from-a-list surface.
func (a *App) catsPickAgentPane(title string, act func(*App, cats.PaneInfo)) {
	panes := a.catsAgentPanes()
	if len(panes) == 0 {
		a.flash("No agent panes in this cats session")
		return
	}
	items := make([]paletteItem, 0, len(panes))
	for _, p := range panes {
		p := p
		items = append(items, paletteItem{
			label: catsPaneLabel(p),
			run:   func(app *App) { act(app, p) },
		})
	}
	a.openPicker(title, items)
}

// catsPaneWho names an agent pane in a sentence ("claude in w1:p3").
func catsPaneWho(p cats.PaneInfo) string {
	who := p.Agent
	if who == "" {
		who = "a pane"
	}
	if p.Handle != "" {
		who += " in " + p.Handle
	}
	return who
}

// catsPaneLabel renders one picker row: who, where, and what it is doing.
// The state is included because it is the fact that decides which agent a
// user wants to talk to.
func catsPaneLabel(p cats.PaneInfo) string {
	label := catsPaneWho(p)
	if p.AgentState != "" {
		label += " — " + p.AgentState
	}
	if p.Title != "" {
		label += "  (" + p.Title + ")"
	}
	return label
}

// -----------------------------------------------------------------------------
// Quoting the selection
// -----------------------------------------------------------------------------

// catsSelectionQuote builds the message both send verbs carry: a file:line
// reference, a blank line, then the selected text.
//
// The reference is what makes the quote actionable — an agent handed twenty
// lines of code with no location can only guess where they came from, and
// the first thing it would do is search for them. The path is relative to
// the project root, which is the form every agent already prints and the
// only form that means anything in a pane whose cwd is that root.
func (a *App) catsSelectionQuote() string {
	tab := a.activeTabPtr()
	if tab == nil || !tab.HasSelection() {
		return ""
	}
	text := tab.SelectionText()
	if strings.TrimSpace(text) == "" {
		return ""
	}
	ref := a.catsSelectionRef(tab)
	if ref == "" {
		return text
	}
	return ref + "\n\n" + text
}

// catsSelectionRef is the "path:12-40" (or "path:12") half of the quote. An
// untitled buffer has no path to name, and returns "" — the quote is still
// worth sending, it just arrives without a location.
func (a *App) catsSelectionRef(tab *editor.Tab) string {
	if tab.Path == "" {
		return ""
	}
	name := tab.Path
	if rel, err := filepath.Rel(a.rootDir, tab.Path); err == nil && !strings.HasPrefix(rel, "..") {
		name = rel
	}
	start, end := tab.Anchor, tab.Cursor
	if end.Line < start.Line || (end.Line == start.Line && end.Col < start.Col) {
		start, end = end, start
	}
	// End is exclusive at column 0 — a selection dragged to the start of
	// the next line covers the lines above it, not that one. Reporting the
	// line the caret sits on would overstate the range by one on the most
	// common multi-line gesture there is.
	last := end.Line
	if end.Col == 0 && last > start.Line {
		last--
	}
	if last == start.Line {
		return fmt.Sprintf("%s:%d", name, start.Line+1)
	}
	return fmt.Sprintf("%s:%d-%d", name, start.Line+1, last+1)
}

// hasCatsAgents gates the focus row: Tier 1 and a sibling to jump to.
func (a *App) hasCatsAgents() bool { return a.catsTier1() && len(a.catsAgentPanes()) > 0 }

// hasCatsSelectionSend gates both send rows: Tier 1, and something selected.
// The agent-pane row needs a sibling too, but that is checked at the moment
// of use — a row that vanishes because an agent went idle for a second is a
// row the user cannot learn.
func (a *App) hasCatsSelectionSend() bool {
	return a.catsTier1() && a.hasSelection()
}

// menuCatsSendSelection stages the quoted selection in an agent's pane.
//
// submit is FALSE, deliberately and permanently: the user gets to read what
// their editor is about to say on their behalf and press Enter themselves.
// An editor that could silently prompt an agent is an editor that can spend
// somebody's tokens and change their files without a keystroke.
func (a *App) menuCatsSendSelection() {
	a.closeMenu()
	if !a.catsTier1() {
		a.flash("Sending to an agent needs cats — ced is running in a plain terminal")
		return
	}
	quote := a.catsSelectionQuote()
	if quote == "" {
		a.flash("Select some text first")
		return
	}
	panes := a.catsAgentPanes()
	if len(panes) == 1 {
		a.catsSendQuoteTo(panes[0], quote)
		return
	}
	a.catsPickAgentPane("Send selection to", func(app *App, p cats.PaneInfo) {
		app.catsSendQuoteTo(p, quote)
	})
}

// catsSendQuoteTo types the quote into a pane, off the main loop.
func (a *App) catsSendQuoteTo(p cats.PaneInfo, quote string) {
	client, scr := a.cats.client, a.screen
	id, who := p.Pane, catsPaneWho(p)
	go func() {
		if err := client.PaneSendInput(id, quote, false); err != nil {
			catsPostNotice(scr, "Send failed: "+err.Error())
			return
		}
		// The phrasing says the ball is in the user's court, because it
		// is: the text is sitting in the agent's prompt, unsent.
		catsPostNotice(scr, "Staged the selection for "+who+" — press Enter there to send")
	}()
}

// menuCatsAskChat sends the same quote to the HOST's chat panel. The right
// door when the question is about the code rather than for one agent —
// and the one that needs no pane to be running at all.
func (a *App) menuCatsAskChat() {
	a.closeMenu()
	if !a.catsTier1() {
		a.flash("Asking cats chat needs cats — ced is running in a plain terminal")
		return
	}
	quote := a.catsSelectionQuote()
	if quote == "" {
		a.flash("Select some text first")
		return
	}
	client, scr := a.cats.client, a.screen
	go func() {
		if err := client.ChatSend(quote); err != nil {
			catsPostNotice(scr, "Chat send failed: "+err.Error())
			return
		}
		catsPostNotice(scr, "Sent the selection to cats chat")
	}()
}
