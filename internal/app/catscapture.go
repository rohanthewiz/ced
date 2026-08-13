// =============================================================================
// File: internal/app/catscapture.go
// Author: Rohan Allison <rohanthewiz@gmail.com>
// Created: 2026-08-13
// Copyright: 2026 Rohan Allison. All rights reserved.
// =============================================================================

// Reading another pane: the agent two panes over just printed a version of
// the function you are looking at, and the question "what did it actually
// change?" is currently answered by squinting between two halves of a screen.
//
// cats' `capture` verb returns a pane's buffer as text — whole rows, no
// coordinates, which is the difference between it and `read` (a selection
// between two points, which needs a selection nobody made). Handing that text
// to the compare panel (compare.go) turns the squinting into a unified diff
// against the buffer, computed by the same code that diffs against a file.
//
// THE CAPTURE IS THE OLD SIDE, ALWAYS. compare.go's house rule is that the
// active buffer is the "new" side, which makes "+" lines the ones that exist
// in the file and lets a double-click jump to them. A proposal read out of a
// terminal is by definition the thing being compared AGAINST what you have.
//
// WHAT COMES BACK IS A TERMINAL, NOT A FILE, and the trimming reflects that:
//
//   - unwrap, so a 200-column agent transcript in an 80-column pane is not
//     diffed as line-broken nonsense;
//   - no ansi, because escape sequences in a diff are noise the user cannot
//     see past;
//   - the trailing blank rows of the pane's empty screen are dropped, or
//     every capture would report a few hundred deletions of nothing.
//
// It is deliberately NOT smart beyond that: no attempt to find the code block
// inside the transcript, no stripping of prompts. A capture that quietly
// discarded the wrong half would be worse than one the user can see all of
// and scroll.

package app

import (
	"strings"
	"time"

	"github.com/gdamore/tcell/v2"

	"github.com/rohanthewiz/ced/internal/cats"
)

// catsCaptureLines bounds how much scrollback is asked for. Enough to hold a
// long agent turn, small enough that the diff of it stays a diff rather than
// a second document — and bounded at all because scope "recent" with 0 lines
// means the WHOLE buffer, which on a pane that has been running for a day is
// megabytes crossing a unix socket to be diffed against a 300-line file.
const catsCaptureLines = 2000

// menuCatsComparePane picks a pane and diffs it against the active buffer.
//
// Agent panes are offered first and alone when there are any: "compare with
// the agent" is the question this verb exists for. Everything else in the
// session is reachable through the same picker when there is no agent — a
// pane running a test suite or a `git show` is just as comparable, and the
// alternative is a row that dims for a reason the user cannot see.
func (a *App) menuCatsComparePane() {
	a.closeMenu()
	if !a.catsTier1() {
		a.flash("Reading another pane needs cats — ced is running in a plain terminal")
		return
	}
	if a.activeTabPtr() == nil {
		a.flash("Open a file to compare the pane against")
		return
	}
	panes := a.catsComparablePanes()
	switch len(panes) {
	case 0:
		a.flash("No other panes in this cats session")
	case 1:
		a.catsCapturePane(panes[0])
	default:
		items := make([]paletteItem, 0, len(panes))
		for _, p := range panes {
			p := p
			items = append(items, paletteItem{
				label: catsPaneLabel(p),
				run:   func(app *App) { app.catsCapturePane(p) },
			})
		}
		a.openPicker("Compare buffer with pane", items)
	}
}

// catsComparablePanes is the picker's contents: the agent panes when there
// are any, otherwise every pane but our own.
//
// Our own is excluded in both branches for the same reason it is excluded
// from the agent list — ced reports itself to cats as the agent "ced", so
// without the check the editor would offer to diff the buffer against a
// picture of itself.
func (a *App) catsComparablePanes() []cats.PaneInfo {
	if agents := a.catsAgentPanes(); len(agents) > 0 {
		return agents
	}
	out := make([]cats.PaneInfo, 0, len(a.cats.panes))
	for _, p := range a.cats.panes {
		if a.cats.selfOK && p.Pane == a.cats.self {
			continue
		}
		out = append(out, p)
	}
	return out
}

// catsCapturePane reads one pane off the main loop and posts the text back.
// The compare itself runs on the loop, in catsCaptureArrived — a diff mutates
// panel state and opens a panel, neither of which a goroutine may do.
func (a *App) catsCapturePane(p cats.PaneInfo) {
	client, scr := a.cats.client, a.screen
	id, label := p.Pane, catsPaneCompareLabel(p)
	go func() {
		text, err := client.Capture(id, cats.CaptureRecent, catsCaptureLines, true)
		if err != nil {
			catsPostNotice(scr, "Capture failed: "+err.Error())
			return
		}
		text = catsTrimCapture(text)
		if text == "" {
			catsPostNotice(scr, "That pane has no output to compare")
			return
		}
		_ = scr.PostEvent(&catsEvent{
			when: time.Now(), kind: catsKindCapture,
			text: text, label: label,
		})
	}()
}

// catsCaptureArrived installs the captured text as the compare panel's old
// side. The active tab may have changed while the capture was in flight —
// that is fine and needs no guard: the diff is against whatever the user is
// looking at NOW, which is the buffer they would have picked if asked again.
func (a *App) catsCaptureArrived(e *catsEvent) {
	if e.text == "" || a.activeTabPtr() == nil {
		return
	}
	a.compareWithText(e.label, e.text)
}

// catsPaneCompareLabel names the captured side in the diff header. It has to
// read as a place ("claude in w1:p3 · cats pane"), because the other side of
// the header is a file path and a bare agent name beside one would look like
// a file that had gone missing.
func catsPaneCompareLabel(p cats.PaneInfo) string {
	return catsPaneWho(p) + " · cats pane"
}

// catsTrimCapture drops the trailing empty rows a captured screen always
// carries and normalises line endings.
//
// Only the TAIL is trimmed: blank lines inside a transcript are part of it,
// and a diff whose old side had its interior whitespace silently removed
// would report changes the user never made.
func catsTrimCapture(text string) string {
	text = strings.ReplaceAll(text, "\r\n", "\n")
	return strings.TrimRight(text, " \t\n")
}

// hasCatsComparePane gates the row: Tier 1, a buffer to be the new side, and
// somebody else in the session to read.
func (a *App) hasCatsComparePane() bool {
	return a.catsTier1() && a.activeTabPtr() != nil && len(a.catsComparablePanes()) > 0
}

// compile-time reminder that the capture path reports through the same notice
// channel as every other background cats call.
var _ func(tcell.Screen, string) = catsPostNotice
