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
	"strings"

	"github.com/gdamore/tcell/v2"
)

// hoverModalMaxWidth caps the popup's width. A line longer than the box
// is WRAPPED onto more rows (tooltipLayout) rather than cut with an
// ellipsis: a doc comment's point is usually in the clause past the
// cap, and a truncated signature hides exactly the parameter or return
// type the user asked about. The box grows down, not across — a wide
// tooltip over code covers more of what the user is reading than a
// tall, narrow one.
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
// caret but clamps into the window; a box too tall for either side is
// shortened (tooltipPlace). A cursor that's scrolled offscreen falls
// back to the centered position every other modal uses.
func (m *hoverModal) rect(a *App) (x, y, w, h int) {
	w, h = tooltipSize(a, m.lines)

	ex, ey, ew, eh := a.editorRect()
	t := a.activeTabPtr()
	if t == nil {
		return a.centeredRect(w, h)
	}
	dx, dy, ok := t.CursorScreenCell(ew, eh)
	if !ok {
		// No anchor to shorten against, so cap at the window; the painter
		// marks the cut the same way tooltipPlace's shortening does.
		return a.centeredRect(w, min(h, max(a.height-1, 3)))
	}
	return tooltipPlace(a, w, h, ex+dx, ey+dy)
}

// tooltipSize measures the box a set of finished lines wants: the widest
// line plus border and one cell of padding each side, capped at
// hoverModalMaxWidth and again at the window, and one row per WRAPPED
// row between the two border rows. The height is the box's natural
// height; tooltipPlace trims it to the room beside the anchor.
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
	// Measured by the same layout drawTooltipBox paints, at the same
	// text width, so the box is exactly as tall as what lands in it.
	rows, _ := tooltipLayout(lines, nil, w-4)
	return w, len(rows) + 2
}

// tooltipLayout wraps finished lines to textW columns and carries any
// emphasis runs along onto the rows they land on. It is the one layout
// shared by measuring (tooltipSize) and painting (drawTooltipBox) — a
// measurer that wrapped differently from the painter would leave blank
// rows or clip the last ones.
//
// Wrapping is greedy at spaces, hard-breaking a run with no space in
// reach (a long identifier, a URL). A continuation row repeats the
// line's leading indentation, so an indented doc example or a
// diagnostic's under-the-glyph continuation stays hung where it was.
// Lines that already fit — signature help pre-wraps to
// hoverModalTextWidth — come back untouched, emphasis and all.
//
//	"func (g *Generator) resolveRunDagTarget(dagMetadata map[string]…"
//	         │ textW = 20
//	         ▼
//	"func (g *Generator)"      seg [0,19)   break at a space, space dropped
//	"resolveRunDagTarget("     seg [20,40)  no space in reach: hard break
//	"dagMetadata map[string]…" seg [40,…)   and so on
//
// Emphasis offsets are rune offsets into the ORIGINAL line; each is
// clipped to every segment it overlaps and re-based onto that row
// (plus the hanging indent), so a run split by a wrap is painted on
// both rows and a run falling in a dropped break space vanishes.
func tooltipLayout(lines []string, emph []hoverEmph, textW int) ([]string, []hoverEmph) {
	if textW < 1 {
		textW = 1
	}
	var rows []string
	var rowEmph []hoverEmph
	for li, ln := range lines {
		runes := []rune(ln)
		for _, sg := range wrapTooltipLine(runes, textW) {
			row := len(rows)
			rows = append(rows, strings.Repeat(" ", sg.indent)+string(runes[sg.start:sg.end]))
			for _, e := range emph {
				if e.line != li {
					continue
				}
				s, en := max(e.start, sg.start), min(e.end, sg.end)
				if s >= en {
					continue
				}
				rowEmph = append(rowEmph, hoverEmph{
					line:  row,
					start: sg.indent + s - sg.start,
					end:   sg.indent + en - sg.start,
				})
			}
		}
	}
	return rows, rowEmph
}

// tooltipSeg is one wrapped row of a tooltip line: the half-open rune
// range [start, end) of the source line it shows, drawn after indent
// spaces of hanging indentation (zero on a line's first row).
type tooltipSeg struct {
	start, end, indent int
}

// wrapTooltipLine splits one line into rows of at most w cells. See
// tooltipLayout for the policy; this is the mechanics, kept separate so
// the offset bookkeeping emphasis depends on is testable on its own.
func wrapTooltipLine(r []rune, w int) []tooltipSeg {
	if len(r) <= w {
		return []tooltipSeg{{start: 0, end: len(r)}}
	}
	// Hanging indent = the line's own leading whitespace, dropped when it
	// would eat more than half the row: a deeply indented line wrapping
	// into a sliver is worse than one that wraps flush left.
	lead := 0
	for lead < len(r) && (r[lead] == ' ' || r[lead] == '\t') {
		lead++
	}
	hang := lead
	if hang > w/2 {
		hang = 0
	}

	var out []tooltipSeg
	pos, first := 0, true
	for pos < len(r) {
		indent := 0
		if !first {
			indent = hang
		}
		avail := w - indent
		if len(r)-pos <= avail {
			out = append(out, tooltipSeg{start: pos, end: len(r), indent: indent})
			break
		}
		// The row's content starts past the leading indentation on the
		// first row; a break inside that run would emit a row of blanks.
		content := pos
		if first {
			content = lead
		}
		// Latest space that still leaves content on this row. r[pos+avail]
		// is the first rune that does NOT fit, so a space there is a clean
		// break with the row full.
		brk := -1
		for b := pos + avail; b > content; b-- {
			if r[b] == ' ' {
				brk = b
				break
			}
		}
		if brk < 0 {
			// No space in reach: hard-break so nothing overflows the box.
			out = append(out, tooltipSeg{start: pos, end: pos + avail, indent: indent})
			pos += avail
		} else {
			// Walk back over a run of spaces so the row doesn't end in
			// padding, then skip the whole run: the break is the space.
			end := brk
			for end > content && r[end-1] == ' ' {
				end--
			}
			out = append(out, tooltipSeg{start: pos, end: end, indent: indent})
			pos = brk
			for pos < len(r) && r[pos] == ' ' {
				pos++
			}
		}
		first = false
	}
	return out
}

// tooltipPlace positions a w×h tooltip against the anchor cell (cx, cy):
// one row below it by convention, flipping above when the bottom of the
// window would clip it, with x following the anchor and clamped into the
// window. The anchor cell itself is never covered in either direction —
// which is the whole contract for a mouse tooltip, where the anchor is
// the thing the user is pointing at.
//
// A box too tall for EITHER side (wrapping lets hover text grow down)
// takes the roomier side and is shortened to fit it; drawTooltipBox marks
// the cut. The returned hh is therefore authoritative — callers paint
// and hit-test with it, not with the h they asked for.
//
//	    room above = cy rows         ┐
//	  ── anchor row cy ──            │ a.height-1 (status bar excluded)
//	    room below = height-2-cy     ┘
func tooltipPlace(a *App, w, h, cx, cy int) (x, y, ww, hh int) {
	x = cx
	if x+w > a.width {
		x = a.width - w
	}
	if x < 0 {
		x = 0
	}
	below := a.height - 1 - (cy + 1)
	above := cy
	switch {
	case h <= below:
		y = cy + 1 // below the anchor
	case h <= above:
		y = cy - h // flip above
	case max(below, above) >= 3:
		// Neither side holds it all: shorten into the bigger one. Three
		// rows (border, one body row, border) is the least worth drawing.
		if below >= above {
			h, y = below, cy+1
		} else {
			h, y = above, 0
		}
	default:
		// A window too short to honour the anchor at all: the old
		// behaviour — flip above, clamp to the top, cap at the window.
		y = cy - h
		h = min(h, a.height-1)
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
// the same reason tooltipSize is shared. Lines are wrapped to the box
// (tooltipLayout); a box shorter than its rows (tooltipPlace shortened
// it, or the window is tiny) ends in a "…" row so the cut is visible.
func drawTooltipBox(a *App, lines []string, emph []hoverEmph, mx, my, mw, mh int) {
	c := a.chrome()
	fillRect(a.screen, mx, my, mw, mh, c.bgSt)
	drawBorder(a.screen, mx, my, mw, mh, c.border)

	rows, emph := tooltipLayout(lines, emph, mw-4)
	if body := mh - 2; body >= 1 && len(rows) > body {
		// An unmarked cut reads as the server having said less than it did.
		rows = append(rows[:body-1:body-1], "…")
		// Emphasis aimed at the cut rows would otherwise land on the "…".
		kept := emph[:0:0]
		for _, e := range emph {
			if e.line < body-1 {
				kept = append(kept, e)
			}
		}
		emph = kept
	}

	emphSt := tcell.StyleDefault.Background(c.bg).Foreground(a.theme.Accent).Bold(true)
	for i, ln := range rows {
		if i >= mh-2 {
			break
		}
		runes := []rune(ln)
		// Rows are wrapped to mw-4 so this never fires at a sane width;
		// it stays as the guard against a box narrower than 5 cells,
		// where even the wrap floor of one cell can't fit.
		if mw >= 5 && len(runes) > mw-4 {
			runes = append(runes[:mw-5:mw-5], '…')
		}
		drawAt(a.screen, mx+2, my+1+i, string(runes), c.body)
		// Emphasis is repainted OVER the plain line rather than the line
		// being drawn in segments: the guard above can cut a span short
		// (or away entirely), and clamping one range is simpler to get
		// right than three interlocking substrings. Rows past the "…"
		// cut have no line to land on, so their emphasis is never drawn.
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
