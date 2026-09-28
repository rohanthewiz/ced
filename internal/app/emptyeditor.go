// =============================================================================
// File: internal/app/emptyeditor.go
// Author: Rohan Allison <rohanthewiz@gmail.com>
// Created: 2026-09-28
// Copyright: 2026 Rohan Allison. All rights reserved.
// =============================================================================

// Links on the "No file open" placeholder: Recent files and Recent
// locations, one click each from an empty editor.
//
// WHY THESE TWO. An empty editor is almost always a moment of "where was
// I": a fresh start in a folder whose session restore is off, or the
// last tab just closed. Both questions already have a picker (Esc-B and
// ≡ Nav → Recent locations…) — the placeholder is simply the one screen
// that has room to offer them without being asked, to a mouse-first user
// who is already looking at the middle of the window.
//
// THE LINKS ARE A SECOND DOOR, never the only one: each runs exactly the
// ≡ row's verb (menuRecentFiles / menuRecentLocations), so a link can
// never disagree with its row about what the list holds.
//
// ALWAYS DRAWN, even with no history yet. The verbs already flash why the
// list is empty ("this list fills in as you open files"), which is the
// "an unavailable row explains itself" rule; a link that appeared only
// after the first file was opened would be a feature nobody ever saw on
// the screen that most needs it.
//
// ONE GEOMETRY for draw and hit-test (emptyEditorLinks), the same
// btnRect contract the modals use — a link painted one cell off from
// where it answers is the classic way for these to rot.
//
//	            No file open
//
//	Click a file in the tree, or  ≡  for the menu
//
//	      Recent files   ·   Recent locations      ← one row when it fits
//
// On a band too narrow for the one row they stack, one link per row, and
// a row that would fall below the editor band is dropped rather than
// painted over the panel or status bar beneath it.

package app

import "github.com/gdamore/tcell/v2"

// emptyLinkSep sits between the two links when they share a row. Wide
// enough that the two underlines read as two targets, not one phrase.
const emptyLinkSep = "   ·   "

// emptyLink is one clickable label on the placeholder: what it says,
// where it is, and which ≡ verb it runs.
type emptyLink struct {
	label  string
	rect   btnRect
	action func(*App)
}

// emptyEditorLinks lays the links out in the current editor band. It
// returns nil whenever the placeholder is not what the band shows (a tab
// is active), so the hit-test can never answer for an invisible link.
//
// Rects are CLIPPED to the band: a label wider than a very narrow band
// keeps only its visible cells clickable, exactly the cells drawCentered
// paints.
func (a *App) emptyEditorLinks() []emptyLink {
	if a.activeTabPtr() != nil {
		return nil
	}
	ex, ey, ew, eh := a.editorRect()
	if ew <= 0 || eh <= 0 {
		return nil
	}
	links := []emptyLink{
		{label: "Recent files", action: (*App).menuRecentFiles},
		{label: "Recent locations", action: (*App).menuRecentLocations},
	}
	// Two rows below the hint line (drawn at cy+1), leaving one blank row
	// between them so the links read as a separate offer, not more hint.
	row := ey + eh/2 + 3

	total := 0
	for i, l := range links {
		if i > 0 {
			total += len([]rune(emptyLinkSep))
		}
		total += len([]rune(l.label))
	}

	out := links[:0]
	if total <= ew {
		// One shared row, centred as a whole — the same arithmetic
		// drawCentered uses, so the links line up under the hint.
		if row >= ey+eh {
			return nil
		}
		x := ex + (ew-total)/2
		for _, l := range links {
			n := len([]rune(l.label))
			l.rect = btnRect{x: x, y: row, w: n}
			out = append(out, l)
			x += n + len([]rune(emptyLinkSep))
		}
		return out
	}
	// Too narrow for one row: stack, each link centred on its own row.
	for i, l := range links {
		y := row + i
		if y >= ey+eh {
			break
		}
		l.rect = clipRect(centredRect(ex, ew, y, len([]rune(l.label))), ex, ew)
		if l.rect.w > 0 {
			out = append(out, l)
		}
	}
	return out
}

// centredRect places an n-cell label centred in the w-column band at x,
// flooring at the band's left edge the way drawCentered does.
func centredRect(x, w, y, n int) btnRect {
	start := x + (w-n)/2
	if start < x {
		start = x
	}
	return btnRect{x: start, y: y, w: n}
}

// clipRect trims r to the columns [x, x+w). A rect wholly outside comes
// back with w == 0.
func clipRect(r btnRect, x, w int) btnRect {
	if r.x < x {
		r.w -= x - r.x
		r.x = x
	}
	if r.x+r.w > x+w {
		r.w = x + w - r.x
	}
	if r.w < 0 {
		r.w = 0
	}
	return r
}

// drawEmptyEditorLinks paints the links (and the separator between them
// when they share a row). Accent + underline is the terminal's link look
// — the same styling the terminal panel gives its clickable locations —
// so no extra legend is needed to say "these are clickable".
func (a *App) drawEmptyEditorLinks() {
	links := a.emptyEditorLinks()
	bg := a.theme.BG
	link := tcell.StyleDefault.Background(bg).Foreground(a.theme.Accent).Underline(true)
	muted := tcell.StyleDefault.Background(bg).Foreground(a.theme.Muted)
	for i, l := range links {
		// centredRect floors at the band's left edge, so a clipped rect
		// only ever lost cells on the RIGHT: its w cells are the label's
		// first w runes, exactly what drawCentered would show.
		runes := []rune(l.label)
		for j := 0; j < l.rect.w && j < len(runes); j++ {
			a.screen.SetContent(l.rect.x+j, l.rect.y, runes[j], nil, link)
		}
		// Separator only between two links on the same row.
		if i > 0 && links[i-1].rect.y == l.rect.y {
			sx := links[i-1].rect.x + links[i-1].rect.w
			for j, r := range []rune(emptyLinkSep) {
				a.screen.SetContent(sx+j, l.rect.y, r, nil, muted)
			}
		}
	}
}

// emptyEditorPress runs the link under (x, y), if any. Returns true when
// it consumed the press, so handleMouse does not also start an editor
// drag on a screen that has no document to select in.
func (a *App) emptyEditorPress(x, y int) bool {
	for _, l := range a.emptyEditorLinks() {
		if l.rect.contains(x, y) {
			l.action(a)
			return true
		}
	}
	return false
}
