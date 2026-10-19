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
// asks a plain Yes/No whose YES is the default. Nothing is lost on that
// path (no unsaved tabs), so the deliberate click plus Enter should just
// finish the job; the confirm still guards against the stray click, and
// a stray Enter can only follow a stray click. This is the one confirm
// that flips the shared default — Delete and friends keep No.
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

// quitButtonDesat is how far the ✕'s red is pulled toward its own grey
// (0 = the theme's full error red, 1 = grey). The full DiagError shouted
// from the corner of every frame like a live error; half-way keeps it
// unmistakably red-family while letting it sit with the chrome.
const quitButtonDesat = 0.5

// drawQuitButton paints ✕ in a muted error colour on the tab bar's ground.
// Error-derived rather than Accent: the ≡ beside it is Accent, and the
// tree's own ✕ (hide the panel) is Muted — the red cast is what says this
// one closes the whole editor. Desaturated so it reads as chrome, not as
// a diagnostic; liftToContrast is the backstop for a theme whose greyed
// red lands too close to the sidebar ground.
func (a *App) drawQuitButton() {
	qx, qy, qw, _ := a.quitButtonRect()
	fg := liftToContrast(desaturateColor(a.theme.DiagError, quitButtonDesat), a.theme.SidebarBG)
	style := tcell.StyleDefault.Background(a.theme.SidebarBG).Foreground(fg).Bold(true)
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
	m := a.openConfirm("Quit ced", "Close the editor?", (*App).menuQuit)
	m.hover = 1 // Yes — see the file header for why this confirm differs
}

// desaturateColor pulls c toward the grey of the same (Rec. 601) luma by
// t (0 = unchanged, 1 = grey). Blending toward its OWN grey rather than
// toward the background keeps the lightness — and so the contrast against
// the ground — roughly where the theme put it; only the chroma drops. A
// colour with no RGB form comes back unchanged (blendColor's rule).
func desaturateColor(c tcell.Color, t float64) tcell.Color {
	r, g, b := c.RGB()
	if r < 0 {
		return c
	}
	y := int32(0.299*float64(r) + 0.587*float64(g) + 0.114*float64(b) + 0.5)
	return blendColor(c, tcell.NewRGBColor(y, y, y), t)
}
