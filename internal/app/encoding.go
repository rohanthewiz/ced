// =============================================================================
// File: internal/app/encoding.go
// Author: Rohan Allison <rohanthewiz@gmail.com>
// Created: 2026-11-10
// Copyright: 2026 Rohan Allison. All rights reserved.
// =============================================================================

// encoding.go is the app's side of N-114: files whose bytes are not UTF-8
// are held as Windows-1252 (editor/encoding.go has the why), and this
// file is where that becomes visible and decidable.
//
// THE ONE QUESTION. A Windows-1252 file can only lose data one way: the
// user types a character the code page has no byte for, and a save
// would have nothing to write. Save refuses with an
// *editor.UnencodableError and the answer is always the same question —
// "convert the file to UTF-8?" — asked from three doors:
//
//	explicit save (saveTabAt, Save & close) ── refused ──► confirm ─ Yes ─► convert + save
//	≡ File "Convert file to UTF-8…"  ────────────────────► confirm ─ Yes ─► convert
//	status bar "cp1252" segment (click) ─────────────────► confirm ─ Yes ─► convert
//
// Every door confirms, No by default: converting rewrites the bytes of
// every non-ASCII character in the file, which no Undo reaches (the
// TEXT does not change, so there is no edit to undo). Auto-save never
// asks — a background save that opened a dialog would be the editor
// interrupting itself — it skips the tab silently and the status
// segment, red while the buffer holds such a character, is the signal.
//
// THE OTHER READERS. Anything that compares the buffer against bytes
// from disk or git, or writes such bytes INTO the buffer, has to read
// them the way the tab did, or a Windows-1252 file differs from itself
// on every non-ASCII line: blame's --contents (gitblame.go encodes the
// buffer back), the compare panel's disk side and the agent's reads
// (editor.DecodeBytes), and the gutter diff's hunk lines (decodeDiffLine,
// below) — the last one matters most, because a hunk revert writes the
// "-" lines into the buffer, and raw bytes there are exactly the bug.
package app

import (
	"errors"
	"fmt"
	"path/filepath"
	"unicode/utf8"

	"github.com/gdamore/tcell/v2"

	"github.com/rohanthewiz/ced/internal/editor"
	"github.com/rohanthewiz/ced/internal/theme"
)

// offerUTF8Save is the explicit save's answer to a refusal: when err is
// an *editor.UnencodableError it opens the conversion question for tab
// and reports true (the caller stops — the question IS its reply). Yes
// converts and runs resume, which is the caller's own gesture again, so
// "save" and "save & close" each finish what the user asked for. Any
// other error reports false and the caller flashes it as before.
func (a *App) offerUTF8Save(tab *editor.Tab, err error, resume func(*App)) bool {
	var uerr *editor.UnencodableError
	if tab == nil || !errors.As(err, &uerr) {
		return false
	}
	name := filepath.Base(tab.Path)
	enc := editor.EncodingLabel(uerr.Encoding)
	// Three lines, each a fact the decision needs: which file and how
	// it was read, what exactly can't be written (the line is what lets
	// them go and delete it instead), and the offer. Each fits the
	// confirm's fixed width for an ordinary name — the drawer ELIDES a
	// long line, and the first draft lost "hold." off the second one
	// (TestUTF8Questions_FitTheConfirm).
	a.openConfirmLines("Save as UTF-8?", []string{
		fmt.Sprintf("%s was read as %s.", name, enc),
		fmt.Sprintf("Line %d has %s, which it can't hold.", uerr.Line+1, editor.RuneLabel(uerr.Rune)),
		"Convert the file to UTF-8 and save it?",
	}, func(app *App) {
		// The tab is resolved by pointer at answer time, never by an index
		// captured at ask time: tabs can close or reorder under a dialog
		// (N-121f).
		if app.tabIndexOf(tab) < 0 {
			app.flash(name + " was closed — nothing converted")
			return
		}
		tab.ConvertToUTF8()
		resume(app)
	})
	return true
}

// menuConvertToUTF8 is the ≡ File row: ask, for the active tab.
func (a *App) menuConvertToUTF8() {
	a.closeMenu()
	a.askConvertToUTF8(a.activeTabPtr())
}

// askConvertToUTF8 is the question behind the ≡ row and the status
// segment. Unavailable cases explain themselves (the menu rule) rather
// than leaving a row that silently does nothing.
func (a *App) askConvertToUTF8(tab *editor.Tab) {
	switch {
	case tab == nil:
		a.flash("No file open")
		return
	case tab.IsImage():
		a.flash("Images have no text encoding")
		return
	case tab.Encoding == "":
		a.flash(tab.DisplayName() + " is already UTF-8")
		return
	}
	name := tab.DisplayName()
	a.openConfirmLines("Convert to UTF-8?", []string{
		fmt.Sprintf("%s was read as %s.", name, editor.EncodingLabel(tab.Encoding)),
		"From its next save it is written as UTF-8,",
		"changing the bytes of each non-ASCII character.",
	}, func(app *App) {
		if app.tabIndexOf(tab) < 0 {
			app.flash(name + " was closed — nothing converted")
			return
		}
		tab.ConvertToUTF8()
		// The conversion leaves the tab dirty without moving EditRev,
		// so auto-save's edit-driven countdown would not notice it.
		// Armed here, a converted tab is saved the way an edited one is.
		if app.autoSaveEnabled {
			app.armAutoSave()
		}
		app.flash(name + " will be saved as UTF-8")
	})
}

// isUnencodableErr reports whether err is a save refused for its
// encoding — auto-save's cue to skip quietly rather than flash.
func isUnencodableErr(err error) bool {
	var uerr *editor.UnencodableError
	return errors.As(err, &uerr)
}

// encodingStatusSegments is the status bar's encoding note: nothing for
// UTF-8 (the default goes unsaid), " · cp1252" for a Windows-1252 file,
// and a red "cp1252 ✗" badge while the buffer holds a character no save
// could write — the one signal auto-save's silent skip leaves. Both are
// doors onto the conversion question.
func (a *App) encodingStatusSegments(tab *editor.Tab) []statusSegment {
	if tab == nil || tab.Encoding == "" {
		return nil
	}
	ask := func(app *App) { app.askConvertToUTF8(app.activeTabPtr()) }
	enc := editor.EncodingLabel(tab.Encoding)
	if uerr := tab.Unencodable(); uerr != nil {
		return []statusSegment{
			{text: " · "},
			{text: tab.Encoding + " ✗", alert: true, onClick: ask,
				tip: fmt.Sprintf("Can't save: line %d has %s, which %s can't hold — convert to UTF-8…",
					uerr.Line+1, editor.RuneLabel(uerr.Rune), enc)},
		}
	}
	return []statusSegment{{text: " · " + tab.Encoding, onClick: ask,
		tip: "Read as " + enc + " (not UTF-8) — convert to UTF-8…"}}
}

// statusAlertStyle is the red badge an alert segment wears: the error
// colour as the fill, and whichever of the theme's two inks (background,
// text) reads better on it — tabGroupChipFG's choice, the same problem.
//
// Not liftToContrast: an error red is a MID tone (gruvbox-dark's is
// luminance ~0.2), which the lift reads as "dark" and pushes toward
// white, topping out near 3.4:1 when black would give ~5.8:1. When
// neither theme ink reaches the text floor, plain black or white —
// whichever is further from the fill — does.
// TestStatusAlertStyle_ReadsOnEveryBuiltin is the check.
func (a *App) statusAlertStyle() tcell.Style {
	bg := a.theme.DiagError
	ink := a.tabGroupChipFG(bg)
	if contrastRatio(ink, bg) < theme.MinTextContrast {
		ink = tcell.NewRGBColor(0, 0, 0)
		if white := tcell.NewRGBColor(255, 255, 255); contrastRatio(white, bg) > contrastRatio(ink, bg) {
			ink = white
		}
	}
	return tcell.StyleDefault.Background(bg).Foreground(ink).Bold(true)
}

// decodeDiffLine is how a gutter-diff line (git's bytes) becomes text
// the buffer can hold. A Windows-1252 tab's lines decode as Windows-1252
// whatever they look like (C3 A9 there is "Ã©"). For any other tab a line
// that is not valid UTF-8 can only be legacy bytes — the index still
// holding a file's Windows-1252 form after it was converted — so it
// decodes the same way. Either way the buffer never receives a raw byte
// from a hunk revert.
func decodeDiffLine(enc, line string) string {
	if enc == editor.EncodingCP1252 || !utf8.ValidString(line) {
		return editor.DecodeAs(editor.EncodingCP1252, []byte(line))
	}
	return line
}

// decodeHunkBodies rewrites every hunk's Body through decodeDiffLine, in
// place. The "-" / "+" / "\" prefixes are ASCII and survive as-is.
func decodeHunkBodies(enc string, hunks []diffHunk) {
	for i := range hunks {
		for j, ln := range hunks[i].Body {
			hunks[i].Body[j] = decodeDiffLine(enc, ln)
		}
	}
}
