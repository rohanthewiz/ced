// =============================================================================
// File: internal/editor/lens.go
// Author: Rohan Allison <rohanthewiz@gmail.com>
// Created: 2026-10-03
// Copyright: 2026 Rohan Allison. All rights reserved.
// =============================================================================

// lens.go is END-OF-LINE LENSES: short clickable labels painted in the
// empty cells after a line's last character — `<<<<<<< HEAD  Accept
// current · Accept incoming · Accept both`. It is the CodeLens idea, and
// the first user is the merge-conflict resolver (app/conflictview.go),
// but nothing here knows about conflicts: a LensSource hands the editor
// labels and an opaque ID per line, the editor paints them and remembers
// where, and a click comes back as "line 12, lens ID 3".
//
// It is linenote.go's placement, made clickable, and it inherits that
// file's whole argument: cells past a line's end belong to no rune, so a
// button there costs the layout nothing — no column mapping between the
// buffer and the screen, nothing for HitTest, soft wrap, secondary carets
// or the horizontal scroll to learn. An IDE draws its lens on a phantom
// row ABOVE the line; that would be a row the buffer does not own, which
// is exactly the splice ghost.go explains the price of.
//
//	<<<<<<< HEAD  Accept current · Accept incoming · Accept both
//	└─ buffer ─┘  └──────────── cells nobody owns ─────────────┘
//
// The rules, all inherited from notes or from btnRect:
//
//   - DROPPED, NEVER SQUEEZED. A lens set first tries its full labels,
//     then its short ones, then sheds whole buttons from the RIGHT (the
//     source orders them most-wanted first). A button is never cut
//     mid-word: a truncated verb is a verb you cannot read before you
//     click it. Whatever does not fit is the source's job to offer
//     elsewhere — the conflict verbs all have menu twins.
//   - A LENS OUTRANKS A NOTE on its line. Both want the same cells, and
//     an action you can take beats a remark you can read (the caret note
//     on a marker line is a compiler complaining about the marker itself).
//   - ONE GEOMETRY SOURCE. Render stamps every button it paints into
//     lensHits; LensAt reads that slice. The draw and the hit-test cannot
//     disagree about where a button is because the hit-test never
//     recomputes it — the status bar's stamped-segment rule.

package editor

import (
	"github.com/gdamore/tcell/v2"

	"github.com/rohanthewiz/ced/internal/theme"
)

// lensSep separates two buttons. Wide enough to aim between, muted so
// it reads as punctuation rather than a fourth button.
const lensSep = " · "

// Lens is one end-of-line button.
type Lens struct {
	Label string      // the full label, e.g. "Accept incoming"
	Short string      // compact form for a tight line; "" means Label
	FG    tcell.Color // the label's colour
	ID    int         // the source's action key — opaque to the editor
}

// short returns the compact label, falling back to the full one.
func (l Lens) short() string {
	if l.Short != "" {
		return l.Short
	}
	return l.Label
}

// LensSource is a DecorationSource that also offers lenses, keyed by
// buffer line, for the visible window [firstLine, lastLine]. Sources are
// asked once per render; a line answered by several sources shows the
// LATER source's set (the precedence rule spans and marks follow).
type LensSource interface {
	DecorationSource
	Lenses(t *Tab, th theme.Theme, firstLine, lastLine int) map[int][]Lens
}

// LensHit is one painted button: the line it belongs to, the lens, and
// its one-row cell rect relative to the Render origin.
type LensHit struct {
	Line    int
	Lens    Lens
	X, Y, W int
}

// collectLenses gathers every source's lenses for the visible window.
// nil when no source offers any — the common case, which then costs the
// row loop one map lookup per line.
func (t *Tab) collectLenses(th theme.Theme, firstLine, lastLine int) map[int][]Lens {
	var out map[int][]Lens
	for _, src := range t.DecoSources {
		ls, ok := src.(LensSource)
		if !ok {
			continue
		}
		for line, set := range ls.Lenses(t, th, firstLine, lastLine) {
			if len(set) == 0 {
				continue
			}
			if out == nil {
				out = make(map[int][]Lens)
			}
			out[line] = set
		}
	}
	return out
}

// LensAt returns the button painted at render-relative cell (dx, dy) in
// the last frame, if any.
func (t *Tab) LensAt(dx, dy int) (LensHit, bool) {
	for _, h := range t.lensHits {
		if dy == h.Y && dx >= h.X && dx < h.X+h.W {
			return h, true
		}
	}
	return LensHit{}, false
}

// LensHits returns the buttons painted in the last frame — for tests and
// for the app's hover affordances. The slice is shared; read only.
func (t *Tab) LensHits() []LensHit { return t.lensHits }

// layoutLens decides which labels a set shows in `room` cells: all full
// labels if they fit, else all short labels, else as many short labels as
// fit counting from the left. Returns the labels to draw (possibly none).
func layoutLens(set []Lens, room int) []string {
	width := func(labels []string) int {
		n := 0
		for i, l := range labels {
			if i > 0 {
				n += runeCount(lensSep)
			}
			n += runeCount(l)
		}
		return n
	}
	full := make([]string, len(set))
	short := make([]string, len(set))
	for i, l := range set {
		full[i], short[i] = l.Label, l.short()
	}
	if width(full) <= room {
		return full
	}
	for n := len(short); n > 0; n-- {
		if width(short[:n]) <= room {
			return short[:n]
		}
	}
	return nil
}

// runeCount is the cell width of a lens label — one cell per rune, the
// marker rule every chrome label in the editor follows.
func runeCount(s string) int {
	n := 0
	for range s {
		n++
	}
	return n
}

// paintLens draws one line's lens set after the line's text on its last
// screen row and stamps the hit rects. usedCells is how many content
// cells the row's text occupies; (originX, originY) is the Render origin
// the stamped rects are relative to. Reports whether anything was drawn,
// so the caller knows the note slot is taken.
func (t *Tab) paintLens(scr tcell.Screen, th theme.Theme, lineIdx int, set []Lens, cy, contentX, contentW, usedCells int, lineBg tcell.Color, originX, originY int) bool {
	start := usedCells + lineNoteGap
	limit := contentW - 1 // the last column is the overflow markers'
	labels := layoutLens(set, limit-start)
	if len(labels) == 0 {
		return false
	}
	sepSt := tcell.StyleDefault.Background(lineBg).Foreground(th.Muted)
	x := contentX + start
	for i, label := range labels {
		if i > 0 {
			for _, r := range lensSep {
				scr.SetContent(x, cy, r, nil, sepSt)
				x++
			}
		}
		st := tcell.StyleDefault.Background(lineBg).Foreground(set[i].FG).Bold(true)
		w := 0
		for _, r := range label {
			scr.SetContent(x+w, cy, r, nil, st)
			w++
		}
		t.lensHits = append(t.lensHits, LensHit{
			Line: lineIdx, Lens: set[i],
			X: x - originX, Y: cy - originY, W: w,
		})
		x += w
	}
	return true
}
