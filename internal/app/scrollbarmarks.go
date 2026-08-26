// =============================================================================
// File: internal/app/scrollbarmarks.go
// Author: Rohan Allison <rohanthewiz@gmail.com>
// Created: 2026-08-26
// Copyright: 2026 Rohan Allison. All rights reserved.
// =============================================================================

// What the scrollbar's rail says besides "where am I": the caret's
// position when you have scrolled away from it, and a colored cell for
// every diagnostic and find hit that is OFF SCREEN.
//
// The bar without this answers one question — how much of the file is in
// front of me, and where in it. The rail is a scale model of the whole
// document sitting right there, and the editor already knows three things
// worth plotting on it that the viewport cannot show you: where your
// cursor went, where the errors are, and where the other hits are. This
// is that plot. It is a minimap of positions, not of content: one cell
// per rail row, color only, no glyph vocabulary to learn.
//
//	▕   ← rail: nothing out there
//	▐   ← error, off screen above
//	▕
//	━   ← the caret, left behind by a scroll
//	▐▐  ← two rows carrying find hits
//	▐   ← the thumb (marks under it are suppressed — see below)
//	▐
//	▕
//
// Two suppression rules do most of the design work:
//
//   - A mark is drawn ONLY for a line the viewport does not already
//     show. On screen, the gutter dot, the underline and the find tint
//     are all right there against the code, saying it better and in
//     place. The rail's job is what you CANNOT see, which is also what
//     makes it quiet on a small file and informative on a large one.
//   - Nothing paints over the thumb. The thumb is "here", and a rail
//     chewed up by marks stops reading as a position at all.
package app

import (
	"github.com/gdamore/tcell/v2"

	"github.com/rohanthewiz/ced/internal/editor"
	"github.com/rohanthewiz/ced/internal/lsp"
	"github.com/rohanthewiz/ced/internal/plugins"
	"github.com/rohanthewiz/ced/internal/theme"
)

// railKind is what has claimed one row of the rail. The values are
// ordered by PRECEDENCE — a higher one wins the cell — because a rail
// row stands for many lines at once (a 5,000-line file through a 40-row
// track is 125 lines per cell), so collisions are the normal case rather
// than the exception, and the cell has to answer with the loudest thing
// in its range.
//
// The caret outranks everything, including an error. It is the one mark
// that is unique — there is exactly one caret, and if its cell is taken
// the feature has silently failed — while a diagnostic is redundantly
// reported by the status bar's counts, the Problems panel and its own
// gutter dot. Find outranks the diagnostics for the reason the
// decoration merge puts findSource last: an active search is a question
// the user asked, and ambient annotation loses to it.
type railKind uint8

const (
	railNone railKind = iota
	railInfo
	railWarn
	railError
	railFind
	railCaret
)

// railCaretRune is the caret tick. It is deliberately NOT from the block
// family the rail and thumb are drawn in: a horizontal stroke across the
// column reads as a pointer at a POSITION, where one more vertical
// segment would read as another piece of rail — and the tick would then
// be a second accent-colored block on a bar whose thumb turns accent the
// moment it is dragged.
const railCaretRune = '━'

// railRow maps a document line to its row on the rail — the ONE mapping,
// shared by the caret tick and every mark, so two things that sit on the
// same line can never be drawn on different rows.
//
// It is proportional over the whole FILE, which is the same measure
// scrollbarMetrics gives the thumb's height. It is deliberately not the
// thumb's POSITION formula (which is measured against MaxScroll, so that
// the end of travel parks the thumb flush at the bottom): that maps
// scroll positions, and a mark is a line. The two agree to within a row
// at the extremes, and marks are suppressed under the thumb anyway.
//
// Returns -1 when there is no rail to place anything on.
func railRow(line, total, trackH int) int {
	if trackH <= 0 {
		return -1
	}
	if total < 1 {
		total = 1
	}
	if line < 0 {
		line = 0
	}
	if line >= total {
		line = total - 1
	}
	row := line * trackH / total
	if row >= trackH {
		row = trackH - 1
	}
	return row
}

// railMarks builds the rail's overlay for one tab: a slice of trackH
// entries, each holding whatever won that row. nil means the rail has
// nothing to say, which is the common case and costs one comparison to
// discover.
//
// [firstLine, lastLine] is the viewport, inclusive. Everything inside it
// is skipped — see the file comment: the rail speaks for what is out of
// sight, and repeating the gutter would only make the bar noisy exactly
// when the user is already looking at the answer.
//
// Sources are read from their caches, NOT through DecorationSource.
// Sources are asked per visible window by contract, and the rail's whole
// subject is the rest of the file; asking each one for the entire buffer
// would turn a per-frame read into a whole-file walk for the word
// highlighter and the git differ, neither of which has anything to say
// here.
func (a *App) railMarks(t *editor.Tab, trackH, firstLine, lastLine int) []railKind {
	if t == nil || trackH <= 0 {
		return nil
	}
	total := t.Buffer.LineCount()
	var marks []railKind

	// claim writes kind at line's row if nothing louder is already there.
	// It allocates on first use so a clean file with no search running
	// never pays for the slice.
	claim := func(line int, kind railKind) {
		if line >= firstLine && line <= lastLine {
			return // on screen: the gutter and the tint already say it
		}
		row := railRow(line, total, trackH)
		if row < 0 {
			return
		}
		if marks == nil {
			marks = make([]railKind, trackH)
		}
		if kind > marks[row] {
			marks[row] = kind
		}
	}

	// Language-server diagnostics. Lines are validated against the live
	// buffer for lspDiagSource's reason: diagnostics lag edits by a
	// debounce plus a type-check, so a stale range past EOF must cull.
	for _, d := range a.lsp.diags[t.Path] {
		if d.Range.Start.Line >= total {
			continue
		}
		claim(d.Range.Start.Line, lspRailKind(d.Severity))
	}

	// Plugin diagnostics, gated at the read the way every other plugin
	// surface is — the kill switch has to be honored at EVERY surface,
	// not only at load.
	if a.plugins.enabled {
		for _, diags := range a.plugins.decos[t.Path] {
			for _, d := range diags {
				if d.Line >= total {
					continue
				}
				claim(d.Line, pluginRailKind(d.Severity))
			}
		}
	}

	// Find hits. The match list is already whole-buffer (the find bar
	// scans the file, not the window), so this is a walk of data that
	// exists rather than a search.
	for _, m := range t.FindMatches {
		claim(m.Line, railFind)
	}

	// The caret last, so it takes whichever row it lands on. Only when
	// it is off screen: on screen the hardware cursor is the answer, and
	// a tick would be a second cursor to explain.
	if cur := t.Cursor.Line; cur < firstLine || cur > lastLine {
		row := railRow(cur, total, trackH)
		if row >= 0 {
			if marks == nil {
				marks = make([]railKind, trackH)
			}
			marks[row] = railCaret
		}
	}
	return marks
}

// lspRailKind maps a language-server severity onto the rail's ranking.
// Hint folds into info the way diagSeverityColor folds it — one cell has
// no room for a distinction the user cannot see.
func lspRailKind(sev int) railKind {
	switch sev {
	case lsp.SeverityWarning:
		return railWarn
	case lsp.SeverityInfo, lsp.SeverityHint:
		return railInfo
	default:
		return railError
	}
}

// pluginRailKind maps a plugin severity onto the same ranking, so a
// `go vet` finding and a gopls one are the same color at the same rank.
func pluginRailKind(sev plugins.Severity) railKind {
	switch sev {
	case plugins.SevError:
		return railError
	case plugins.SevWarn:
		return railWarn
	default:
		return railInfo
	}
}

// railMarkColor picks the cell's color. Diagnostics reuse the theme's
// three Diag* colors — the same ones the gutter dot and the underline
// wear, so a red cell on the rail and a red dot in the gutter are
// obviously the same fact seen from two distances.
//
// Find uses FindCurrent, not FindMatch: FindMatch is a background TINT
// (a dark amber wash meant to sit under text) and would be all but
// invisible as a foreground on the editor's own background.
func railMarkColor(th theme.Theme, k railKind) tcell.Color {
	switch k {
	case railCaret:
		return th.Accent
	case railFind:
		return th.FindCurrent
	case railError:
		return th.DiagError
	case railWarn:
		return th.DiagWarning
	default:
		return th.DiagInfo
	}
}
