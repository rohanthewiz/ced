// =============================================================================
// File: internal/app/diagnote.go
// Author: Rohan Allison <rohanthewiz@gmail.com>
// Created: 2026-09-30
// Copyright: 2026 Rohan Allison. All rights reserved.
// =============================================================================

// diagnote.go puts the message of a diagnostic on the caret's line in
// the empty cells AFTER that line — no gesture, no dwell, no chord:
//
//	  3 ◇   "tags": ["a", "b",],  ✗ trailing comma: JSON allows no ',' before ']'
//	                          ~       └──── painted while the caret is on line 3
//
// # Why this door exists when diagtip.go already has four
//
// Every door diagtip.go opens has to be KNOWN to be used. The pointer
// dwell never fires in a terminal that reports presses but no motion
// (macOS Terminal.app, and tmux without motion reporting), and the
// gutter click and Esc-i are invisible until someone tells you about
// them. The result was a red underline that looked mute: a mark you can
// see, with no visible way to ask it what it means.
//
// The one gesture every user makes at a red underline is to CLICK it,
// and in the code that click is the caret's (diagtip's rule — it always
// will be). So the answer rides the caret: clicking the mark, arrowing
// onto the line, or landing there from next-problem all show the
// message beside the thing it describes, on every host, with nothing to
// learn.
//
// # Why the caret line only
//
// Painting every diagnosed line's message would turn a file with thirty
// gopls warnings into a wall of red prose, and the gutter and underlines
// already say WHERE the problems are. The caret line is the one the user
// is asking about.
//
// # Mechanics
//
// Painted in the end-of-line slot inlay hints use (editor/linenote.go),
// so it costs no geometry: dropped, never squeezed, when the line leaves
// no room — and on the caret's line it replaces the inlay note, since
// both cannot fit and "broken" outranks "is an int". Re-stamped before
// every frame from diagsFor, the cache every other diagnostic surface
// reads; nothing here is scheduled, so an idle editor stays idle.
//
// # The off switch
//
// ≡ View "Hide diagnostic note" / config `"diagnote": "off"`. Default
// on: the note is the only door that needs neither motion reports nor
// knowing a chord, and it is quiet by construction (one line, one
// sentence). Off is for people who would rather ask than be told; the
// other doors (Esc-i, the gutter click, the pointer tooltip, Problems)
// keep working, and the caret line gets its inlay note back.
//
// While the note is ON, a click on an underline is already answered
// beside the caret, so the pointer tooltip does not ALSO open on the
// press's release (see noteDiagPointer). While it is OFF that release
// is the click's only answer on a motion-reporting host, so it arms
// the tooltip as before.

package app

import (
	"fmt"

	"github.com/rohanthewiz/ced/internal/editor"
	"github.com/rohanthewiz/ced/internal/lsp"
	"github.com/rohanthewiz/ced/internal/userconfig"
)

// stampDiagCaretNote installs (or clears) the caret-line diagnostic
// note on t just before it renders.
func (a *App) stampDiagCaretNote(t *editor.Tab) {
	if t == nil {
		return
	}
	text, sev := "", 0
	// Off still STAMPS (an empty note): that is what clears a note left
	// by the frame before the switch, with no per-tab sweep to forget.
	if !a.diagNoteOff && t.Path != "" && !t.IsImage() && !t.IsMarkdownView() {
		text, sev = diagCaretNoteText(diagsAtCaret(t, a.diagsFor(t.Path)))
	}
	t.SetCaretNote(t.Cursor.Line, text, diagSeverityColor(a.theme, sev))
}

// diagsAtCaret picks the diagnostics the caret is asking about: those
// whose range covers it, or — when the caret sits on a diagnosed line
// but outside every range, which is where a gutter click or a Problems
// jump leaves it — every diagnostic STARTING on that line (the line the
// gutter mark is drawn on). Shared with Esc-i so the note and the
// keyboard answer can never describe different diagnostics.
func diagsAtCaret(t *editor.Tab, all []lsp.Diagnostic) []lsp.Diagnostic {
	if t == nil || len(all) == 0 {
		return nil
	}
	ds := diagsCovering(t, all, t.Cursor)
	if len(ds) > 0 {
		return ds
	}
	for _, d := range all {
		if d.Range.Start.Line == t.Cursor.Line {
			ds = append(ds, d)
		}
	}
	return ds
}

// diagCaretNoteText renders the note for a set of diagnostics: the
// WORST one's glyph and message on one line, plus a count of the rest
// (the tooltip and Problems panel list them all; an end-of-line note
// has room for one sentence). The severity is returned for the colour.
//
// Ties go to the earliest in the list, which diagsFor orders LSP →
// plugins → ced, so a server's answer beats a guess about the same spot.
func diagCaretNoteText(ds []lsp.Diagnostic) (string, int) {
	if len(ds) == 0 {
		return "", 0
	}
	best := 0
	for i := 1; i < len(ds); i++ {
		// LSP severities count DOWN: 1 is an error, the worst.
		if problemSeverity(ds[i].Severity) < problemSeverity(ds[best].Severity) {
			best = i
		}
	}
	sev := problemSeverity(ds[best].Severity)
	text := string(problemGlyph(sev)) + " " + flattenProblemMsg(ds[best].Message)
	if n := len(ds) - 1; n > 0 {
		text += fmt.Sprintf("  (+%d more)", n)
	}
	return text, sev
}

// menuToggleDiagNote is the ≡ View row.
func (a *App) menuToggleDiagNote() {
	a.closeMenu()
	a.setDiagNote(a.diagNoteOff)
}

// setDiagNote is the single write path for the preference: state, flash,
// config. Nothing else to clear — stampDiagCaretNote runs before every
// frame and stamps an empty note while the switch is off.
func (a *App) setDiagNote(on bool) {
	a.diagNoteOff = !on
	if on {
		a.flash("Diagnostic note on")
	} else {
		a.flash("Diagnostic note off")
	}
	if err := userconfig.SaveDiagNote(userconfig.DefaultPath(), on); err != nil {
		a.flash("config: " + err.Error())
	}
}

// diagNoteToggleLabel names the row by what clicking it does.
func (a *App) diagNoteToggleLabel() string {
	if a.diagNoteOff {
		return "Show diagnostic note"
	}
	return "Hide diagnostic note"
}
