// =============================================================================
// File: internal/app/quitbutton.go
// Author: Rohan Allison <rohanthewiz@gmail.com>
// Created: 2026-10-16
// Copyright: 2026 Rohan Allison. All rights reserved.
// =============================================================================

// The ✕ quit button: a one-click way to close the editor itself, sitting
// immediately left of the ≡ button at the start of the tab bar.
//
//	│ ✕  ≡ │ main.go × │ app.go × │ …
//	  └┬┘└┬┘
//	   │  menuButtonRect — the action menu
//	   quitButtonRect   — confirm, then quit
//
// Both buttons live in the tab bar's leading block (tabBarLeadW): the tab
// strip starts after them, so a tab can never sit under either.
//
// The click always ASKS — a top-left corner cell is easy to hit by
// accident, and quitting drops every tab's undo history. One question,
// never two: with unsaved tabs the click goes straight to menuQuit's
// Save / Discard / Cancel modal (itself the confirmation); with none it
// asks a plain Yes/No whose No is the default, so a stray Enter is safe.
// Yes re-enters menuQuit rather than setting a.quit directly, so the
// dirty check is made again at the moment of quitting — the single quit
// path stays menuQuit (≡ Quit and Esc-q are the other doors).
package app

import (
	"github.com/gdamore/tcell/v2"
)

// quitButtonWidth is the ✕ button's cell count: the glyph with one
// space of padding either side, so it reads as a button rather than a
// stray character jammed against the sidebar seam.
const quitButtonWidth = 3

// tabBarLeadW is the tab bar's leading block — ✕ then ≡ — that the tab
// strip starts after. Every "where do tabs begin" calculation reads this,
// never menuButtonWidth alone.
const tabBarLeadW = quitButtonWidth + menuButtonWidth

// quitButtonRect is the on-screen rectangle of the ✕ button: the first
// cells of the tab bar, so it follows the bar when the sidebar hides.
// Draw and tabBarClick both read it.
func (a *App) quitButtonRect() (x, y, w, h int) {
	return a.leftBlockW(), 0, quitButtonWidth, 1
}

// drawQuitButton paints ✕ in the error colour on the tab bar's ground.
// Error rather than Accent: the ≡ beside it is Accent, and the tree's own
// ✕ (hide the panel) is Muted — the red is what says this one closes the
// whole editor.
func (a *App) drawQuitButton() {
	qx, qy, qw, _ := a.quitButtonRect()
	style := tcell.StyleDefault.Background(a.theme.SidebarBG).Foreground(a.theme.DiagError).Bold(true)
	for cx := qx; cx < qx+qw; cx++ {
		a.screen.SetContent(cx, qy, ' ', nil, style)
	}
	a.screen.SetContent(qx+qw/2, qy, '✕', nil, style)
}

// requestQuitFromButton is the ✕ click: confirm, then quit through
// menuQuit. With unsaved tabs menuQuit's own Save/Discard/Cancel modal is
// the confirmation, so it is opened directly rather than stacking a
// second question in front of it.
func (a *App) requestQuitFromButton() {
	if a.dirtyTabCount() > 0 {
		a.menuQuit()
		return
	}
	a.openConfirm("Quit ced", "Close the editor?", (*App).menuQuit)
}
