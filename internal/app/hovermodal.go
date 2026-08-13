// =============================================================================
// File: internal/app/hovermodal.go
// Author: Rohan Allison <rohanthewiz@gmail.com>
// Created: 2026-07-09
// Copyright: 2026 Rohan Allison. All rights reserved.
// =============================================================================

// hovermodal.go is the LSP hover popup — a borderless-feeling tooltip
// that anchors to the caret instead of centering like every other
// modal. It still implements the standard single-slot modal interface
// so openModal's mutual-exclusion and the key/mouse routing all apply
// unchanged; only the geometry is special.
//
// Dismissal is deliberately trigger-happy: ANY key and any click
// dismiss it. Hover is a glance, not a workspace — the user's next
// action is always "back to editing", and eating that keystroke to
// make them close a tooltip first would feel broken.

package app

import (
	"github.com/gdamore/tcell/v2"
)

// hoverModalMaxWidth caps the popup so a long single-line signature
// wraps the server's problem, not ours — we truncate with an ellipsis
// instead of wrapping, because hover text is a preview, not a reader.
const hoverModalMaxWidth = 66

// hoverModalTextWidth is how many columns of content fit inside the box
// at its widest: the cap minus the border and one cell of padding on
// each side. A producer that wraps its own text (signature help) wraps
// to this, so the box never has to truncate what it was handed.
const hoverModalTextWidth = hoverModalMaxWidth - 4

// hoverEmph marks a run of one line to paint with emphasis — rune
// offsets, half-open. It exists for signature help, whose whole point is
// saying WHICH parameter you are typing; without it that verb would be
// hover on the enclosing function, which the user could already get.
type hoverEmph struct {
	line  int
	start int
	end   int
}

// hoverModal shows flattened text in a caret-anchored tooltip. Two verbs
// share it — hover and signature help (lspsignature.go) — because the
// geometry, the trigger-happy dismissal and the "this is a glance" framing
// are identical; only the emphasis is new, which is the findAllModal.heading
// arrangement one floor down. Producers hand it finished lines.
type hoverModal struct {
	lines []string
	emph  []hoverEmph
}

// handleKey dismisses on any key — see the file comment for why.
func (m *hoverModal) handleKey(a *App, _ *tcell.EventKey) {
	a.closeModal()
}

// handleMouse dismisses on any button press, inside or out. Wheel and
// pure motion events pass through silently so an accidental scroll
// doesn't close the popup before it's been read.
func (m *hoverModal) handleMouse(a *App, _, _ int, btn tcell.ButtonMask) {
	if btn&(tcell.Button1|tcell.Button2|tcell.Button3) != 0 {
		a.closeModal()
	}
}

// rect computes the popup rectangle anchored to the caret: preferred
// position is one row below the cursor (tooltip convention); when the
// bottom of the window would clip it, it flips above. X follows the
// caret but clamps into the window. A cursor that's scrolled offscreen
// falls back to the centered position every other modal uses.
func (m *hoverModal) rect(a *App) (x, y, w, h int) {
	w, h = tooltipSize(a, m.lines)

	ex, ey, ew, eh := a.editorRect()
	t := a.activeTabPtr()
	if t == nil {
		return a.centeredRect(w, h)
	}
	dx, dy, ok := t.CursorScreenCell(ew, eh)
	if !ok {
		return a.centeredRect(w, h)
	}
	return tooltipPlace(a, w, h, ex+dx, ey+dy)
}

// tooltipSize measures the box a set of finished lines wants: the widest
// line plus border and one cell of padding each side, capped at
// hoverModalMaxWidth and again at the window, and one row per line
// between the two border rows.
//
// Split out of hoverModal.rect so the dwell tooltip (hoverdwell.go) is
// the same box measured the same way. Two hover surfaces that disagreed
// about their own width by a cell would read as two different features.
func tooltipSize(a *App, lines []string) (w, h int) {
	w = 4 // border + one cell padding each side
	for _, ln := range lines {
		if lw := runeLen(ln) + 4; lw > w {
			w = lw
		}
	}
	if w > hoverModalMaxWidth {
		w = hoverModalMaxWidth
	}
	if w > a.width {
		w = a.width
	}
	return w, len(lines) + 2
}

// tooltipPlace positions a w×h tooltip against the anchor cell (cx, cy):
// one row below it by convention, flipping above when the bottom of the
// window would clip it, with x following the anchor and clamped into the
// window. The anchor cell itself is never covered in either direction —
// which is the whole contract for a mouse tooltip, where the anchor is
// the thing the user is pointing at.
func tooltipPlace(a *App, w, h, cx, cy int) (x, y, ww, hh int) {
	x = cx
	if x+w > a.width {
		x = a.width - w
	}
	if x < 0 {
		x = 0
	}
	y = cy + 1 // below the anchor
	if y+h > a.height-1 {
		y = cy - h // flip above
	}
	if y < 0 {
		y = 0
	}
	return x, y, w, h
}

// draw paints the popup: plain bordered box, no title row — a tooltip
// with a "Hover   esc" header would be all chrome and no content.
func (m *hoverModal) draw(a *App) {
	mx, my, mw, mh := m.rect(a)
	drawTooltipBox(a, m.lines, m.emph, mx, my, mw, mh)
	// The modal flavour owns the screen while it is up, so it takes the
	// caret off it too. The dwell flavour deliberately does NOT — the
	// editor still has the keyboard behind an ambient tooltip, and a
	// vanished caret would say otherwise.
	a.screen.HideCursor()
}

// drawTooltipBox paints a bordered box of finished lines with optional
// emphasis runs — the shared painter behind both hover surfaces, for
// the same reason tooltipSize is shared.
func drawTooltipBox(a *App, lines []string, emph []hoverEmph, mx, my, mw, mh int) {
	c := a.chrome()
	fillRect(a.screen, mx, my, mw, mh, c.bgSt)
	drawBorder(a.screen, mx, my, mw, mh, c.border)

	emphSt := tcell.StyleDefault.Background(c.bg).Foreground(a.theme.Accent).Bold(true)
	for i, ln := range lines {
		runes := []rune(ln)
		if len(runes) > mw-4 {
			runes = append(runes[:mw-5:mw-5], '…')
		}
		drawAt(a.screen, mx+2, my+1+i, string(runes), c.body)
		// Emphasis is repainted OVER the plain line rather than the line
		// being drawn in segments: the truncation above can cut a span
		// short (or away entirely), and clamping one range is simpler to
		// get right than three interlocking substrings.
		for _, e := range emph {
			if e.line != i {
				continue
			}
			s, en := max(e.start, 0), min(e.end, len(runes))
			if s >= en {
				continue
			}
			drawAt(a.screen, mx+2+s, my+1+i, string(runes[s:en]), emphSt)
		}
	}
}
