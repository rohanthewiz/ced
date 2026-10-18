// =============================================================================
// File: internal/editor/linenote.go
// Author: Rohan Allison <rohanthewiz@gmail.com>
// Created: 2026-09-21
// Copyright: 2026 Rohan Allison. All rights reserved.
// =============================================================================

// linenote.go is END-OF-LINE NOTES: a short muted remark painted in the
// empty cells after a line's last character. It is how the editor shows a
// language server's inlay hints (app/lspinlay.go), and the placement is
// the whole design.
//
// An IDE draws an inlay hint INSIDE the line, at the position it
// annotates — `f(‹level:› 3)`. Doing that here means adding cells the
// buffer does not own to every visible row, and ghost.go already spells
// out what one such splice costs: it is tolerable there only because the
// ghost sits at the caret on a single row. Mid-line hints on every row
// would put a column mapping between the buffer and the screen, and
// HitTest, PosScreenCell, the soft-wrap layout, secondary carets, the
// horizontal scroll and every tooltip that round-trips a cell would each
// have to learn it — with a click landing one word off as the failure
// mode. So the hints move to where no mapping is needed:
//
//	x := compute(cfg, 3)      » x: int · level: 3
//	└──── buffer cells ────┘  └── cells nobody owns ──┘
//
// Cells past a line's end belong to no rune, so nothing about geometry
// changes: a click there already clamps to end-of-line, the caret never
// sits there, and wrapping never reaches them. It is the "virtual text"
// form terminal editors settled on for the same reason.
//
// The rules that follow from it:
//
//   - A NOTE IS DROPPED, NEVER SQUEEZED. It is drawn only in room the line
//     left over; a line that runs to the edge shows none. It stops one
//     cell short of the pane, because the last column is where the
//     overflow markers and the `›` arrow live.
//   - THE SET DIES WITH THE REVISION, symbolhl.go's rule for its reason:
//     a note describes the text the server saw. Because notes occupy no
//     layout, their vanishing while you type and returning when you pause
//     moves nothing on screen.
//   - Painted in Render, not decorated: a Span can only restyle cells the
//     buffer owns (the ghost-text and secondary-caret exception again).

package editor

import (
	"github.com/gdamore/tcell/v2"

	"github.com/rohanthewiz/ced/internal/theme"
)

// lineNoteGap is the blank run between a line's last cell and its note.
const lineNoteGap = 2

// lineNoteMinCells is the least room worth drawing into; below it the
// note would be a lead glyph and an ellipsis.
const lineNoteMinCells = 6

// SetLineNotes installs end-of-line notes keyed by buffer line, stamped
// with the current EditRev. nil or empty clears.
func (t *Tab) SetLineNotes(notes map[int]string) {
	t.lineNotes = notes
	t.lineNotesRev = t.EditRev
}

// LineNotesRev reports the revision the installed notes describe, and
// whether any are installed — what the app needs to decide a refresh.
func (t *Tab) LineNotesRev() (rev int, have bool) {
	return t.lineNotesRev, t.lineNotes != nil
}

// LiveLineNotes returns the notes while they still describe the buffer.
func (t *Tab) LiveLineNotes() map[int]string {
	if t == nil || len(t.lineNotes) == 0 || t.lineNotesRev != t.EditRev {
		return nil
	}
	return t.lineNotes
}

// caretNote is the one end-of-line note the app pins to the CARET's line
// — the message of a diagnostic on that line (app/diagnote.go). It is a
// separate slot from lineNotes rather than an entry in that map for two
// reasons: the map belongs to the inlay-hint pipeline, which replaces it
// wholesale whenever the server answers, and a diagnostic speaks in its
// SEVERITY's colour where an inlay hint is deliberately muted.
//
// On the caret's line it takes the place of any inlay note. Both cannot
// fit in the room a line leaves, and "this line is broken" outranks
// "this variable is an int".
type caretNote struct {
	line int
	rev  int
	text string
	fg   tcell.Color
}

// SetCaretNote pins a note to the end of buffer line `line`, painted in
// fg, and stamped with the current EditRev. An empty text clears it.
//
// The app re-stamps it before every frame, so the stamp is a backstop,
// not the mechanism: a frame drawn by anything that does not re-stamp
// (a test, a future second pane) still cannot show a note that
// describes an older buffer.
func (t *Tab) SetCaretNote(line int, text string, fg tcell.Color) {
	t.caretNote = caretNote{line: line, rev: t.EditRev, text: text, fg: fg}
}

// liveCaretNote returns the caret note for lineIdx, if one is installed
// for that line, still describes the buffer, and the caret is on it.
// Keyed to the caret at PAINT time as well as at stamp time: a caret
// moved by the same event that drew the frame must not leave the note
// hanging on the line it left.
func (t *Tab) liveCaretNote(lineIdx int) (caretNote, bool) {
	n := t.caretNote
	if n.text == "" || n.line != lineIdx || n.rev != t.EditRev || t.Cursor.Line != lineIdx {
		return caretNote{}, false
	}
	return n, true
}

// paintLineNote draws one line's note on its LAST screen row. usedCells
// is how many content cells that row's text occupies; contentX/contentW
// are the code area. Reports nothing: a note that does not fit is simply
// not there.
func paintLineNote(scr tcell.Screen, th theme.Theme, note string, cy, contentX, contentW, usedCells int, lineBg tcell.Color) {
	if note == "" {
		return
	}
	st := tcell.StyleDefault.Background(lineBg).Foreground(th.Muted).Attributes(tcell.AttrItalic)
	paintNoteText(scr, "» "+note, st, cy, contentX, contentW, usedCells)
}

// paintCaretNote draws the caret note in its own colour. Not italic and
// with no `»` lead: the text already opens with a severity glyph, and
// the upright face is what separates "the editor is warning you" from
// the muted aside an inlay hint is.
func paintCaretNote(scr tcell.Screen, n caretNote, cy, contentX, contentW, usedCells int, lineBg tcell.Color) {
	st := tcell.StyleDefault.Background(lineBg).Foreground(n.fg)
	paintNoteText(scr, n.text, st, cy, contentX, contentW, usedCells)
}

// paintNoteText is the placement every note kind shares: after the gap,
// one cell short of the pane, cut with `…`, dropped when the room left
// is not worth drawing into.
func paintNoteText(scr tcell.Screen, text string, st tcell.Style, cy, contentX, contentW, usedCells int) {
	runes := []rune(text)
	styles := make([]tcell.Style, len(runes))
	for i := range styles {
		styles[i] = st
	}
	placeNoteCells(scr, runes, styles, cy, contentX, contentW, usedCells)
}

// placeNoteCells lays out a note given cell by cell — one style per rune,
// so a source note can colour its runs — under paintNoteText's rules. The
// `…` that marks a cut takes the style of the cell it replaces.
func placeNoteCells(scr tcell.Screen, runes []rune, styles []tcell.Style, cy, contentX, contentW, usedCells int) {
	start := usedCells + lineNoteGap
	limit := contentW - 1 // the last column is the overflow markers'
	if len(runes) == 0 || limit-start < lineNoteMinCells {
		return
	}
	room := limit - start
	if len(runes) > room {
		runes = append(runes[:room-1:room-1], '…')
	}
	for i, r := range runes {
		scr.SetContent(contentX+start+i, cy, r, nil, styles[i])
	}
}

// -----------------------------------------------------------------------------
// Source notes
// -----------------------------------------------------------------------------

// NoteRun is one coloured stretch of a source note.
type NoteRun struct {
	Text string
	FG   tcell.Color
}

// NoteSource is a DecorationSource that also LABELS lines: an end-of-line
// note per buffer line for the visible window [firstLine, lastLine], made
// of coloured runs. The third optional extension after LineWashSource and
// LensSource, for the same reason they exist — a Span can only restyle
// cells the buffer owns, and a label belongs past the text.
//
// Merge conflicts are the first user: the `=======` line says which side
// is above it and which below ("▲ yours (main)  ▼ picked (2a0370b)"),
// where git's own markers only say HEAD and a hash.
//
// Precedence in the note slot (Render): a lens set first (an action beats
// a remark), then a source note, then the caret's diagnostic note, then
// an inlay hint. A source note outranks the caret note because the only
// lines a source labels are structure the user is in the middle of —
// a conflict marker, whose "diagnostic" is just a server choking on the
// marker itself. A line answered by several sources shows the LATER
// source's note (the spans' and lenses' rule).
type NoteSource interface {
	DecorationSource
	Notes(t *Tab, th theme.Theme, firstLine, lastLine int) map[int][]NoteRun
}

// collectNotes gathers every source's notes for the visible window. nil
// when no source labels anything — every tab not mid-conflict.
func (t *Tab) collectNotes(th theme.Theme, firstLine, lastLine int) map[int][]NoteRun {
	var out map[int][]NoteRun
	for _, src := range t.DecoSources {
		ns, ok := src.(NoteSource)
		if !ok {
			continue
		}
		for line, runs := range ns.Notes(t, th, firstLine, lastLine) {
			if len(runs) == 0 {
				continue
			}
			if out == nil {
				out = make(map[int][]NoteRun)
			}
			out[line] = runs
		}
	}
	return out
}

// paintSourceNote draws a source note's runs, upright like the caret
// note: it is a statement about the line, not the muted aside an inlay
// hint is, so it gets neither the italic face nor the `»` lead.
func paintSourceNote(scr tcell.Screen, runs []NoteRun, cy, contentX, contentW, usedCells int, lineBg tcell.Color) {
	var runes []rune
	var styles []tcell.Style
	for _, r := range runs {
		st := tcell.StyleDefault.Background(lineBg).Foreground(r.FG)
		for _, c := range r.Text {
			runes = append(runes, c)
			styles = append(styles, st)
		}
	}
	placeNoteCells(scr, runes, styles, cy, contentX, contentW, usedCells)
}
