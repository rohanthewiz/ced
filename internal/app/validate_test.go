// =============================================================================
// File: internal/app/validate_test.go
// Author: Rohan Allison <rohanthewiz@gmail.com>
// Created: 2026-09-21
// Copyright: 2026 Rohan Allison. All rights reserved.
// =============================================================================

package app

import (
	"testing"

	"github.com/rohanthewiz/ced/internal/editor"
	"github.com/rohanthewiz/ced/internal/theme"
)

// TestValidate_OpeningABrokenFileMarksIt pins the case the feature
// exists for. The debounce only ever fires after an EDIT, so a file
// that is already malformed when it opens has to be parsed by wireTab
// or the user could read it and never be told.
func TestValidate_OpeningABrokenFileMarksIt(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	tab := openScratch(t, a, "conf.json", "{\n  \"a\": 1,\n}\n")

	probs := a.liveProblems(tab)
	if len(probs) != 1 {
		t.Fatalf("liveProblems = %v, want one finding on open", probs)
	}
	if probs[0].Line != 2 {
		t.Errorf("finding on line %d, want 2 (the '}')", probs[0].Line)
	}

	spans, marks := validateSource{app: a}.Decorations(tab, theme.Default(), 0, 3)
	if len(marks) != 1 {
		t.Fatalf("marks = %d, want 1", len(marks))
	}
	if marks[0].Glyph != validateMark {
		t.Errorf("glyph = %q, want %q — it must be tellable from ● and ◆",
			marks[0].Glyph, validateMark)
	}
	if len(spans) != 1 || !spans[0].Delta.Underline {
		t.Errorf("spans = %v, want one underline", spans)
	}
}

// TestValidate_ValidFileSaysNothing pins the quiet case, which is
// almost every file almost all the time.
func TestValidate_ValidFileSaysNothing(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	tab := openScratch(t, a, "conf.json", "{\n  \"a\": 1\n}\n")

	if probs := a.liveProblems(tab); probs != nil {
		t.Fatalf("liveProblems = %v, want nil", probs)
	}
	spans, marks := validateSource{app: a}.Decorations(tab, theme.Default(), 0, 3)
	if len(spans) != 0 || len(marks) != 0 {
		t.Errorf("spans/marks = %d/%d, want 0/0", len(spans), len(marks))
	}
	// And nothing is cached, so a clean project costs no map entries.
	if len(a.validate.probs) != 0 {
		t.Errorf("probs cache = %v, want empty", a.validate.probs)
	}
}

// TestValidate_FindingsDieWithTheRevision is the staleness contract,
// and the single most important test in this file. A Problem's column
// is a coordinate into the text that was parsed; one keystroke later it
// may point at a perfectly good character. A stale underline on the
// wrong rune is indistinguishable from a correct answer, which makes it
// the worst thing this feature could show.
func TestValidate_FindingsDieWithTheRevision(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	tab := openScratch(t, a, "conf.json", "{\n  \"a\": 1,\n}\n")
	if len(a.liveProblems(tab)) != 1 {
		t.Fatal("expected a finding before the edit")
	}

	// Any edit at all moves EditRev, and the finding must stand down
	// until the debounce re-parses.
	tab.MoveCursorTo(editor.Position{Line: 0, Col: 0}, false)
	tab.InsertRune('x')

	if probs := a.liveProblems(tab); probs != nil {
		t.Errorf("liveProblems = %v after an edit, want nil until re-parsed", probs)
	}
	spans, marks := validateSource{app: a}.Decorations(tab, theme.Default(), 0, 4)
	if len(spans) != 0 || len(marks) != 0 {
		t.Errorf("painted %d spans / %d marks against a moved buffer, want none", len(spans), len(marks))
	}
}

// TestValidate_ReparseRestoresTheFinding is the other half: once the
// tick runs, a file that is still broken is marked again — at the
// position it now occupies. Without this the previous test would also
// pass for a feature that simply stopped working after one keystroke.
func TestValidate_ReparseRestoresTheFinding(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	tab := openScratch(t, a, "conf.json", "{\n  \"a\": 1,\n}\n")

	// Push the whole document down one line; the broken '}' moves with
	// it, so a cached position would now be wrong.
	tab.MoveCursorTo(editor.Position{Line: 0, Col: 0}, false)
	tab.InsertRune('\n')
	a.validateTab(tab)

	probs := a.liveProblems(tab)
	if len(probs) != 1 {
		t.Fatalf("liveProblems = %v, want the finding back after a re-parse", probs)
	}
	if probs[0].Line != 3 {
		t.Errorf("finding on line %d, want 3 — it must track the text it moved with", probs[0].Line)
	}
}

// TestValidate_FixingTheFileClearsTheMark pins that marks DISAPPEAR.
// An empty result has to delete the entry rather than store an empty
// slice, or a fixed file keeps its gutter dot forever.
func TestValidate_FixingTheFileClearsTheMark(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	tab := openScratch(t, a, "conf.json", "{\n  \"a\": 1,\n}\n")
	if len(a.liveProblems(tab)) != 1 {
		t.Fatal("expected a finding before the fix")
	}

	// Remove the trailing comma.
	tab.MoveCursorTo(editor.Position{Line: 1, Col: 9}, false)
	tab.Backspace()
	a.validateTab(tab)

	if probs := a.liveProblems(tab); probs != nil {
		t.Errorf("liveProblems = %v after fixing the file, want nil", probs)
	}
	if _, still := a.validate.probs[tab.Path]; still {
		t.Error("cache still holds an entry for a file that now parses")
	}
}

// TestValidate_IgnoresFilesItDoesNotOwn pins that nothing is parsed,
// cached or painted for a file ced has no checker for — including the
// JSONC names, which are the carve-out most likely to be noticed.
func TestValidate_IgnoresFilesItDoesNotOwn(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	for _, name := range []string{"main.go", "notes.txt", "tsconfig.json"} {
		tab := openScratch(t, a, name, "{ this is not json at all")
		if probs := a.liveProblems(tab); probs != nil {
			t.Errorf("%s: liveProblems = %v, want nil", name, probs)
		}
		spans, marks := validateSource{app: a}.Decorations(tab, theme.Default(), 0, 3)
		if len(spans) != 0 || len(marks) != 0 {
			t.Errorf("%s: painted %d spans / %d marks, want none", name, len(spans), len(marks))
		}
	}
}

// TestValidateAfterEvent_ArmsOnlyForValidatedFiles pins the
// caret-blink constraint. The loop is idle-driven, so a timer armed
// while the user edits Go would wake a resting editor every 400ms
// forever to discover it has nothing to parse.
func TestValidateAfterEvent_ArmsOnlyForValidatedFiles(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	tab := openScratch(t, a, "main.go", "package main\n")
	a.stopValidateTimer()

	tab.InsertRune('x')
	a.validateAfterEvent()
	if a.validate.timer != nil {
		t.Error("timer armed while editing a .go file; it has nothing to parse")
	}
	t.Cleanup(a.stopValidateTimer)

	// The same edit on a JSON file must arm it.
	jsonTab := openScratch(t, a, "conf.json", "{}\n")
	jsonTab.InsertRune('x')
	a.validateAfterEvent()
	if a.validate.timer == nil {
		t.Error("timer not armed while editing a .json file")
	}
}

// TestValidateAfterEvent_IgnoresCursorTravel pins that only real edits
// re-arm. Moving the caret changes no text, so re-parsing on it would
// be work nobody asked for — the ghost-text rule.
func TestValidateAfterEvent_IgnoresCursorTravel(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	tab := openScratch(t, a, "conf.json", "{\n  \"a\": 1\n}\n")
	a.validateAfterEvent() // absorb the open
	a.stopValidateTimer()
	t.Cleanup(a.stopValidateTimer)

	tab.MoveCursorTo(editor.Position{Line: 1, Col: 3}, false)
	a.validateAfterEvent()
	if a.validate.timer != nil {
		t.Error("timer armed by cursor movement alone")
	}
}

// TestValidate_ClosingATabForgetsIt pins the cleanup. The cache is
// keyed by path, so without this a reopened file would be painted
// straight from a stale entry whose revision happened to line up.
func TestValidate_ClosingATabForgetsIt(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	tab := openScratch(t, a, "conf.json", "{\n  \"a\": 1,\n}\n")
	path := tab.Path
	if len(a.validate.probs) != 1 {
		t.Fatal("expected a cached finding before the close")
	}

	a.closeTab(a.activeTab)
	if _, still := a.validate.probs[path]; still {
		t.Error("findings survived the tab that produced them")
	}
	if _, still := a.validate.rev[path]; still {
		t.Error("revision stamp survived the tab that produced them")
	}
}

// TestValidateProblemRange_UnderlinesTheToken pins what a finding
// covers. A single cell is genuinely hard to see, so a problem landing
// inside a word underlines the whole word; one past the end of a line
// still gets a cell so the mark has somewhere to live.
func TestValidateProblemRange_UnderlinesTheToken(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	tab := openScratch(t, a, "conf.json", "{\n  \"a\": bad\n}\n")

	probs := a.liveProblems(tab)
	if len(probs) != 1 {
		t.Fatalf("liveProblems = %v, want one finding", probs)
	}
	start, end := validateProblemRange(tab, probs[0])
	if start.Line != 1 {
		t.Fatalf("start line = %d, want 1", start.Line)
	}
	// `  "a": bad` — the b is rune 7, and the word runs to 10.
	if start.Col != 7 || end.Col != 10 {
		t.Errorf("range = [%d,%d), want [7,10) — the whole token", start.Col, end.Col)
	}
}

// TestValidate_TabSwitchInsideTheDebounceIsRecovered pins the gap that
// the revision-for-every-buffer record exists to close.
//
// Edit a JSON file and switch away before the tick fires: the tick then
// runs against whatever tab is in front, and the edited one is left
// with findings pinned to a revision it has moved past — so its marks
// AND its Problems rows stay gone until it is typed in again. Coming
// back to it must re-arm the parse.
func TestValidate_TabSwitchInsideTheDebounceIsRecovered(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	broken := openScratch(t, a, "conf.json", "{\n  \"a\": 1,\n}\n")
	brokenIdx := a.activeTab

	// An edit the tick never gets to run for.
	broken.InsertRune(' ')
	if a.liveProblems(broken) != nil {
		t.Fatal("findings should have stood down after the edit")
	}

	// Switch away; the tick fires against the other tab.
	openScratch(t, a, "other.go", "package main\n")
	a.stopValidateTimer()
	a.handleValidateTick(&validateEvent{seq: a.validate.seq})

	// Come back. The dispatch tail must notice the stale buffer.
	a.switchToTab(brokenIdx)
	a.validateAfterEvent()
	if a.validate.timer == nil {
		t.Fatal("no re-parse armed for a tab returned to with stale findings")
	}
	t.Cleanup(a.stopValidateTimer)

	a.stopValidateTimer()
	a.handleValidateTick(&validateEvent{seq: a.validate.seq})
	if probs := a.liveProblems(broken); len(probs) != 1 {
		t.Errorf("liveProblems = %v, want the finding back", probs)
	}
}

// TestValidateAfterEvent_CleanFileArmsNothingRepeatedly is the guard on
// the fix above. The recovery check runs on EVERY dispatch, so a file
// already parsed at its current revision must arm nothing — otherwise
// the editor would wake itself every 400ms forever on a perfectly good
// JSON file (the caret-blink constraint).
func TestValidateAfterEvent_CleanFileArmsNothingRepeatedly(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	openScratch(t, a, "conf.json", "{\n  \"a\": 1\n}\n")
	a.stopValidateTimer()
	t.Cleanup(a.stopValidateTimer)

	for i := 0; i < 5; i++ {
		a.validateAfterEvent()
		if a.validate.timer != nil {
			t.Fatalf("dispatch %d armed a timer on an already-parsed clean file", i)
		}
	}
}
