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

// paintLineNote draws one line's note on its LAST screen row. usedCells
// is how many content cells that row's text occupies; contentX/contentW
// are the code area. Reports nothing: a note that does not fit is simply
// not there.
func paintLineNote(scr tcell.Screen, th theme.Theme, note string, cy, contentX, contentW, usedCells int, lineBg tcell.Color) {
	start := usedCells + lineNoteGap
	limit := contentW - 1 // the last column is the overflow markers'
	if note == "" || limit-start < lineNoteMinCells {
		return
	}
	st := tcell.StyleDefault.Background(lineBg).Foreground(th.Muted).Attributes(tcell.AttrItalic)
	runes := []rune("» " + note)
	room := limit - start
	if len(runes) > room {
		runes = append(runes[:room-1:room-1], '…')
	}
	for i, r := range runes {
		scr.SetContent(contentX+start+i, cy, r, nil, st)
	}
}
