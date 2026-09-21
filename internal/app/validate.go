// =============================================================================
// File: internal/app/validate.go
// Author: Rohan Allison <rohanthewiz@gmail.com>
// Created: 2026-09-21
// Copyright: 2026 Rohan Allison. All rights reserved.
// =============================================================================

// validate.go is ced's own syntax checker for the data formats it
// parses in-process — JSON today, and whatever joins format.kindFor
// later. It turns "this file does not parse" into a mark on the line
// that broke, which is the only form of that answer worth having.
//
// It is the ONLY diagnostic producer in this editor that needs nothing
// installed: no language server, no linter binary, no project config,
// no network. A JSON file with a trailing comma is underlined on a
// freshly-unpacked machine, which is exactly the property that made
// the in-process formatter worth building too.
//
// House rules:
//
//   - **A DecorationSource, like every other overlay.** The paint goes
//     through the one merge path (editor/decoration.go); this file adds
//     no branch to Render. It is registered in wireTab between the git
//     and plugin sources — see "Precedence" below.
//   - **THE SET DIES WITH THE REVISION** (symbolhl's rule, and here it
//     earns its keep twice). A Problem's column is a coordinate into the
//     text that was parsed; one keystroke later it may point at a
//     perfectly good character. A stale underline on the wrong rune is
//     the single worst thing this feature could show, because it is
//     indistinguishable from a correct answer.
//   - **DEBOUNCED, and NOT for the usual reason.** The LSP debounces to
//     spare a round trip and the plugin layer to spare a process; this
//     parse is free. It waits because JSON is transiently broken on
//     almost every keystroke — the instant after you type `{` the file
//     does not parse — and a mark that flashed red through every edit
//     would be noise the eye learns to ignore. Waiting means the mark
//     says "you stopped, and it still doesn't parse", which is the only
//     moment the answer is useful.
//   - **Armed only while something listens.** The event loop is
//     idle-driven, so a standing timer would wake a resting editor
//     forever — the caret-blink constraint. The timer is armed only when
//     the active tab is a kind ced validates.
//   - **It reports; it never rewrites.** Formatting happens on save
//     (format.go). Keeping them apart is what lets ced complain about a
//     half-typed file without touching it.
//
// # Precedence
//
// Registered between git and plugin, so the single gutter cell runs:
//
//	git < validate < plugin < LSP
//
// Above git because a syntax error is a fact about the code, not an
// ambient note about which lines you touched. Below the other two
// because both are more specific: a plugin mark was installed
// deliberately by the user, and a language server that has type-checked
// the file knows strictly more than a parser that only checked its
// punctuation. In practice a collision is rare — no language server
// ced ships a mapping for handles .json.

package app

import (
	"time"

	"github.com/rohanthewiz/ced/internal/editor"
	"github.com/rohanthewiz/ced/internal/format"
	"github.com/rohanthewiz/ced/internal/theme"
)

// validateDebounce is how long the editor waits after the last buffer
// change before re-parsing.
//
// Shorter than the plugin layer's 800ms (that one spawns a process)
// and a little longer than the LSP's 300ms. The tuning target is not
// cost — the parse is microseconds — but the flicker described in the
// house rules: long enough that typing a value straight through never
// paints a mark, short enough that the answer still feels attached to
// the keystroke that caused it.
const validateDebounce = 400 * time.Millisecond

// validateMark is the gutter glyph for a ced syntax finding.
// Deliberately distinct from the LSP's ● and the plugin layer's ◆: when
// two of them have something to say about one file, being able to tell
// which is which at a glance is the whole reason they differ.
const validateMark = '◇'

// validateState is the per-app cache of ced's own findings.
//
// Both maps are keyed by tab PATH, because that is what a
// DecorationSource is handed and what survives a tab being reordered or
// closed and reopened. They are written together and read together; rev
// is what makes probs safe to paint.
type validateState struct {
	// probs holds the findings for each validated file.
	probs map[string][]format.Problem

	// rev records the Tab.EditRev each entry in probs was computed
	// against. A mismatch means the buffer has moved and the columns no
	// longer describe it, so the entry is not painted. This is the
	// whole staleness contract — see the house rules.
	rev map[string]int

	// editSig is the summed EditRev of every tab at the last dispatch,
	// the autoSaveAfterEvent signature. Edits arrive through far too
	// many paths (keys, paste, undo, a plugin's rewrite, a workspace
	// edit) to hook each one, and this is a handful of integer adds
	// when idle.
	editSig int

	// timer is the live debounce, nil when nothing is pending.
	timer *time.Timer

	// seq invalidates a timer armed before a teardown. A folder switch
	// rebuilds the App, but a pending tick posted under the old one
	// must not be honoured by the new.
	seq int
}

// validateEvent fires when the debounce expires.
type validateEvent struct {
	when time.Time
	seq  int
}

// When implements tcell.Event.
func (e *validateEvent) When() time.Time { return e.when }

// -----------------------------------------------------------------------------
// Producing the findings
// -----------------------------------------------------------------------------

// validateTab parses one tab's buffer and files the result, stamped
// with the revision it was computed against.
//
// It reads the BUFFER, never the disk copy: the marks follow what is on
// screen, so an unsaved edit is checked as typed. A checker reading
// from disk would underline text the user had already fixed — and would
// say nothing at all about a brand-new file that has never been saved.
//
// An empty result DELETES the entry rather than storing an empty slice.
// That is how marks disappear once the user fixes the file, and it
// keeps the common case (a project with no broken JSON in it) costing
// no map entries at all.
func (a *App) validateTab(t *editor.Tab) {
	if t == nil || t.Path == "" || t.IsImage() || !format.Validates(t.Path) {
		return
	}
	if a.validate.probs == nil {
		a.validate.probs = make(map[string][]format.Problem)
		a.validate.rev = make(map[string]int)
	}
	// The revision is recorded whether or not anything was found, which
	// is what makes "has this buffer been checked yet?" answerable —
	// validateAfterEvent asks it to catch a tab switched to while its
	// findings were stale. A rev map holding only BROKEN files could not
	// tell a clean file from an unexamined one, and would re-arm the
	// timer forever on every clean file (the caret-blink constraint).
	a.validate.rev[t.Path] = t.EditRev
	probs := format.Validate(t.Path, []byte(t.Buffer.String()))
	if len(probs) == 0 {
		// The findings go even though the revision stays: this is how
		// marks disappear once the user fixes the file.
		delete(a.validate.probs, t.Path)
		return
	}
	a.validate.probs[t.Path] = probs
}

// validateForget drops a file's findings. Called when a tab closes, so
// a reopened file is re-parsed rather than painted from a cache whose
// revision happens to line up again.
func (a *App) validateForget(path string) {
	delete(a.validate.probs, path)
	delete(a.validate.rev, path)
}

// liveProblems returns the findings for a tab, but ONLY when they were
// computed against the buffer as it stands now.
//
// This is the single read path, so the staleness rule cannot be
// forgotten by a future consumer: everything that wants ced's findings
// — the decoration source, the merge seam feeding the tooltip and the
// Problems panel — comes through here.
func (a *App) liveProblems(t *editor.Tab) []format.Problem {
	if t == nil || t.Path == "" {
		return nil
	}
	probs := a.validate.probs[t.Path]
	if len(probs) == 0 {
		return nil
	}
	if a.validate.rev[t.Path] != t.EditRev {
		return nil
	}
	return probs
}

// -----------------------------------------------------------------------------
// The debounce
// -----------------------------------------------------------------------------

// validateAfterEvent runs on the dispatch tail and re-arms the parse
// whenever a buffer actually changed. Mirrors pluginsAfterEvent's
// summed-EditRev signature for the same reason.
//
// The arm is gated on the ACTIVE tab being a validated kind: a user
// editing Go all afternoon must not have the editor waking itself 400ms
// after every keystroke to discover it has nothing to parse.
func (a *App) validateAfterEvent() {
	t := a.activeTabPtr()
	if t == nil || t.Path == "" || !format.Validates(t.Path) {
		return
	}
	sig := 0
	for _, tab := range a.tabs {
		sig += tab.EditRev
	}
	if sig != a.validate.editSig {
		a.validate.editSig = sig
		a.armValidateTimer()
		return
	}
	// No edit — but the active tab may have just BECOME active while its
	// findings were stale. That happens when a file is edited and the
	// user switches away inside the debounce window: the tick fires
	// against whatever tab is in front by then, and the edited one is
	// left with findings pinned to a revision it has moved past, so its
	// marks and its Problems rows would both stay gone until it was
	// typed in again. Re-arming here re-parses it on the way back.
	//
	// Safe to run on every dispatch because the comparison is against a
	// revision recorded for EVERY validated buffer, clean ones included
	// — see validateTab. A file already checked at this revision arms
	// nothing.
	if a.validate.timer == nil && a.validate.rev[t.Path] != t.EditRev {
		a.armValidateTimer()
	}
}

// armValidateTimer restarts the debounce countdown.
func (a *App) armValidateTimer() {
	a.stopValidateTimer()
	scr := a.screen
	if scr == nil {
		return
	}
	seq := a.validate.seq
	a.validate.timer = time.AfterFunc(validateDebounce, func() {
		_ = scr.PostEvent(&validateEvent{when: time.Now(), seq: seq})
	})
}

// stopValidateTimer disarms a pending countdown. Called on teardown so
// a timer armed under the old App can't fire into the new one.
func (a *App) stopValidateTimer() {
	if a.validate.timer != nil {
		a.validate.timer.Stop()
		a.validate.timer = nil
	}
}

// handleValidateTick re-parses the active tab once typing has settled.
func (a *App) handleValidateTick(e *validateEvent) {
	if e == nil || e.seq != a.validate.seq {
		return
	}
	a.validate.timer = nil
	a.validateTab(a.activeTabPtr())
}

// -----------------------------------------------------------------------------
// The decoration source
// -----------------------------------------------------------------------------

// validateSource paints ced's own syntax findings. A pure read of the
// cache, per the DecorationSource contract — the parse runs on the
// debounce, not in the frame.
type validateSource struct{ app *App }

// Decorations converts the live findings into one underline and one
// gutter mark, culled to the visible window.
//
// The underline covers the offending character and, when that character
// sits inside a word, the rest of it: a single cell is genuinely hard
// to see, and the token is what a reader needs to look at. This is
// pluginDiagRange's argument, applied to a producer that always has a
// column.
func (s validateSource) Decorations(t *editor.Tab, th theme.Theme, firstLine, lastLine int) ([]editor.Span, []editor.GutterMark) {
	if s.app == nil {
		return nil, nil
	}
	probs := s.app.liveProblems(t)
	if len(probs) == 0 {
		return nil, nil
	}
	var spans []editor.Span
	var marks []editor.GutterMark
	for _, p := range probs {
		if p.Line < firstLine || p.Line > lastLine || p.Line >= t.Buffer.LineCount() {
			continue
		}
		start, end := validateProblemRange(t, p)
		if start.Col < end.Col {
			spans = append(spans, editor.Span{
				Start: start,
				End:   end,
				Delta: editor.StyleDelta{Underline: true, SetFG: true, FG: th.DiagError},
			})
		}
		marks = append(marks, editor.GutterMark{
			Line:  p.Line,
			Glyph: validateMark,
			FG:    th.DiagError,
		})
	}
	return spans, marks
}

// validateProblemRange decides what a finding underlines: the token at
// the column when there is one, otherwise the single cell there.
//
// A problem at or past end of line — which is what an unterminated
// document produces — is stretched one cell so the mark has somewhere
// to live. An empty line leaves the span empty and only the gutter
// speaks, which is the honest answer: there is no cell to point at.
func validateProblemRange(t *editor.Tab, p format.Problem) (start, end editor.Position) {
	runes := t.Buffer.LineRunes(p.Line)
	col := p.Col
	if col < 0 {
		col = 0
	}
	if col > len(runes) {
		col = len(runes)
	}
	stop := col
	for stop < len(runes) && isWordChar(runes[stop]) {
		stop++
	}
	if stop == col {
		stop = min(col+1, len(runes))
	}
	return editor.Position{Line: p.Line, Col: col}, editor.Position{Line: p.Line, Col: stop}
}
