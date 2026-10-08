// =============================================================================
// File: internal/app/diagnote_test.go
// Author: Rohan Allison <rohanthewiz@gmail.com>
// Created: 2026-09-30
// Copyright: 2026 Rohan Allison. All rights reserved.
// =============================================================================

package app

import (
	"strings"
	"testing"

	"github.com/gdamore/tcell/v2"

	"github.com/rohanthewiz/ced/internal/editor"
	"github.com/rohanthewiz/ced/internal/lsp"
	"github.com/rohanthewiz/ced/internal/userconfig"
)

// editorRowText returns the drawn screen row holding buffer line `line`
// of the active tab.
func editorRowText(t *testing.T, a *App, line int) string {
	t.Helper()
	tab := a.activeTabPtr()
	ex, ey, ew, eh := a.editorRect()
	_, dy, ok := tab.PosScreenCell(editor.Position{Line: line}, ew, eh)
	if !ok {
		t.Fatalf("line %d is not on screen", line)
	}
	return screenRow(t, a, ey+dy, ex, ew)
}

// TestDiagNote_ShowsTheMessageOnTheCaretLine pins the feature: with the
// caret on a diagnosed line the message is painted after it, with no
// hover, click or chord — and only on that line.
func TestDiagNote_ShowsTheMessageOnTheCaretLine(t *testing.T) {
	a, _, _, _ := diagTipTestApp(t)
	tab := a.activeTabPtr()

	tab.MoveCursorTo(editor.Position{Line: 0, Col: 0}, false)
	a.draw()
	if row := editorRowText(t, a, 2); strings.Contains(row, "redeclared") {
		t.Errorf("message shown with the caret elsewhere: %q", row)
	}

	// Line start, outside the range: the gutter-click / Problems-jump spot.
	tab.MoveCursorTo(editor.Position{Line: 2, Col: 0}, false)
	a.draw()
	if row := editorRowText(t, a, 2); !strings.Contains(row, "✗ main redeclared in this block") {
		t.Errorf("caret on the diagnosed line, row = %q, want the message after it", row)
	}
}

// TestDiagNote_JSONTrailingCommaEndToEnd is the reported bug end to end:
// a JSON file with a trailing comma, validated by ced's own parser,
// says what is wrong beside the mark once the caret is on that line —
// and the mark is on the comma, not the blameless ']'.
func TestDiagNote_JSONTrailingCommaEndToEnd(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	a.width, a.height = 120, 30
	tab := openScratch(t, a, "conf.json", "{\n  \"tags\": [\"a\", \"b\",],\n  \"n\": 3\n}\n")
	a.validateTab(tab)

	ds := a.diagsFor(tab.Path)
	if len(ds) != 1 || ds[0].Range.Start.Line != 1 || ds[0].Range.Start.Character != 19 {
		t.Fatalf("diagsFor = %+v, want one finding on the ',' at line 1 col 19", ds)
	}

	tab.MoveCursorTo(editor.Position{Line: 1, Col: 0}, false)
	a.draw()
	if row := editorRowText(t, a, 1); !strings.Contains(row, "✗ trailing comma") {
		t.Errorf("row = %q, want the trailing-comma message beside the line", row)
	}
}

// TestDiagCaretNoteText pins the note's text: the worst diagnostic
// leads regardless of order, multi-line messages are flattened, and the
// rest are counted rather than dropped silently.
func TestDiagCaretNoteText(t *testing.T) {
	text, sev := diagCaretNoteText([]lsp.Diagnostic{
		{Severity: lsp.SeverityWarning, Message: "unused"},
		{Severity: lsp.SeverityError, Message: "undefined:\n\tx"},
		{Severity: lsp.SeverityHint, Message: "hint"},
	})
	if text != "✗ undefined: x  (+2 more)" || sev != lsp.SeverityError {
		t.Errorf("note = %q sev %d, want the error first with +2 more", text, sev)
	}
	if text, _ := diagCaretNoteText(nil); text != "" {
		t.Errorf("no diagnostics gave %q, want empty", text)
	}
}

// TestDiagsAtCaret pins the selection rule shared with Esc-i: ranges
// covering the caret win; failing that, diagnostics starting on the
// caret's line; failing that, nothing.
func TestDiagsAtCaret(t *testing.T) {
	tab := &editor.Tab{Buffer: editor.NewBuffer("abc def\nxyz\n")}
	all := []lsp.Diagnostic{
		{Range: lsp.Range{Start: lsp.Position{Line: 0, Character: 0}, End: lsp.Position{Line: 0, Character: 3}}, Message: "A"},
		{Range: lsp.Range{Start: lsp.Position{Line: 0, Character: 4}, End: lsp.Position{Line: 0, Character: 7}}, Message: "B"},
	}
	tab.Cursor = editor.Position{Line: 0, Col: 5}
	if got := diagsAtCaret(tab, all); len(got) != 1 || got[0].Message != "B" {
		t.Errorf("inside B = %v, want just B", got)
	}
	tab.Cursor = editor.Position{Line: 0, Col: 7}
	if got := diagsAtCaret(tab, all); len(got) != 2 {
		t.Errorf("outside both ranges on the line = %v, want both", got)
	}
	tab.Cursor = editor.Position{Line: 1, Col: 0}
	if got := diagsAtCaret(tab, all); len(got) != 0 {
		t.Errorf("clean line = %v, want none", got)
	}
}

// TestDiagNote_OffSwitch pins N-037's toggle: off takes the note off the
// caret line on the very next frame, the row's label offers the way
// back, the choice is persisted, and on brings the note back.
func TestDiagNote_OffSwitch(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	a, _, _, _ := diagTipTestApp(t)
	tab := a.activeTabPtr()
	tab.MoveCursorTo(editor.Position{Line: 2, Col: 0}, false)
	a.draw()
	if a.diagNoteToggleLabel() != "Hide diagnostic note" {
		t.Errorf("default label = %q", a.diagNoteToggleLabel())
	}

	a.setDiagNote(false)
	a.draw()
	if row := editorRowText(t, a, 2); strings.Contains(row, "redeclared") {
		t.Errorf("note still painted while off: %q", row)
	}
	if a.diagNoteToggleLabel() != "Show diagnostic note" {
		t.Errorf("off label = %q", a.diagNoteToggleLabel())
	}
	if cfg, err := userconfig.Load(userconfig.DefaultPath()); err != nil || cfg.DiagNote {
		t.Errorf("persisted DiagNote = %v, %v; want off", cfg.DiagNote, err)
	}

	a.setDiagNote(true)
	a.draw()
	if row := editorRowText(t, a, 2); !strings.Contains(row, "main redeclared") {
		t.Errorf("note not back after on: %q", row)
	}
}

// TestDiagNote_ClickReleaseArmsNoTooltipWhileOn pins N-037's nit: the
// release of a click on an underline arrives as a motion report on the
// same cell. With the note on, the click is already answered beside the
// caret, so that release must arm no tooltip; with the note off it is
// the click's only answer and arms as before.
func TestDiagNote_ClickReleaseArmsNoTooltipWhileOn(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	a, _, codeX, rowY := diagTipTestApp(t)

	a.noteDiagPointer(codeX, rowY, tcell.Button1)
	before := a.diagTip.seq
	a.noteDiagPointer(codeX, rowY, tcell.ButtonNone)
	if a.diagTip.seq != before {
		t.Error("note on: the click's release armed the tooltip")
	}
	// Real travel still arms: the stamp covers the release, nothing more.
	a.noteDiagPointer(codeX+1, rowY, tcell.ButtonNone)
	if a.diagTip.seq == before {
		t.Error("note on: moving along the underline armed nothing")
	}

	a.setDiagNote(false)
	a.noteDiagPointer(codeX, rowY, tcell.Button1)
	before = a.diagTip.seq
	a.noteDiagPointer(codeX, rowY, tcell.ButtonNone)
	if a.diagTip.seq == before {
		t.Error("note off: the click's release should arm the tooltip")
	}
}
