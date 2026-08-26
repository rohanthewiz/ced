// =============================================================================
// File: internal/app/scrollbarmarks_test.go
// Author: Rohan Allison <rohanthewiz@gmail.com>
// Created: 2026-08-26
// Copyright: 2026 Rohan Allison. All rights reserved.
// =============================================================================

package app

import (
	"testing"

	"github.com/gdamore/tcell/v2"

	"github.com/rohanthewiz/ced/internal/editor"
	"github.com/rohanthewiz/ced/internal/lsp"
	"github.com/rohanthewiz/ced/internal/plugins"
)

// TestRailRow pins the one line→row mapping every mark shares, including
// the ends: line 0 is the top row and the last line is the last row, or a
// mark on a file's final line would be placed off the rail entirely.
func TestRailRow(t *testing.T) {
	cases := []struct {
		name              string
		line, total, trak int
		want              int
	}{
		{"first line", 0, 400, 40, 0},
		{"last line", 399, 400, 40, 39},
		{"midway", 200, 400, 40, 20},
		{"line past EOF clamps", 9999, 400, 40, 39},
		{"negative line clamps", -3, 400, 40, 0},
		{"empty buffer", 0, 0, 40, 0},
		{"file shorter than the track", 3, 5, 40, 24},
		{"no rail", 10, 400, 0, -1},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := railRow(c.line, c.total, c.trak); got != c.want {
				t.Fatalf("railRow(%d, %d, %d) = %d, want %d", c.line, c.total, c.trak, got, c.want)
			}
		})
	}
}

// TestRailMarks_OnlyWhatIsOffScreen is the feature's defining rule: the
// rail speaks for the rest of the file. A diagnostic the user is already
// looking at is said better by the gutter dot beside it, and repeating it
// here would make the bar loudest exactly when it has least to add.
func TestRailMarks_OnlyWhatIsOffScreen(t *testing.T) {
	a, path := scrollbarApp(t, 400)
	tab := a.activeTabPtr()
	a.lsp.diags = map[string][]lsp.Diagnostic{path: {
		diagAt(5, 0, lsp.SeverityError, "on screen", "test"),
		diagAt(300, 0, lsp.SeverityError, "far below", "test"),
	}}

	marks := a.railMarks(tab, 40, 0, 39)
	if got := marks[railRow(300, 400, 40)]; got != railError {
		t.Fatalf("off-screen error = %v, want railError", got)
	}
	if got := marks[railRow(5, 400, 40)]; got != railNone {
		t.Errorf("on-screen error claimed a rail row (%v); the gutter already says it", got)
	}
}

// TestRailMarks_Precedence pins the ranking on a row several things land
// on at once — the normal case, since one rail row stands for many lines.
// The caret wins outright: it is the only mark that is unique, so a lost
// cell is the whole feature failing, while a diagnostic is also in the
// status bar, the Problems panel and its own gutter.
func TestRailMarks_Precedence(t *testing.T) {
	a, path := scrollbarApp(t, 400)
	tab := a.activeTabPtr()
	const trackH = 40
	// Rows are 10 lines each here, so all of these share one rail row.
	a.lsp.diags = map[string][]lsp.Diagnostic{path: {
		diagAt(300, 0, lsp.SeverityInfo, "info", "test"),
		diagAt(301, 0, lsp.SeverityWarning, "warn", "test"),
	}}
	row := railRow(300, 400, trackH)

	if got := a.railMarks(tab, trackH, 0, 39)[row]; got != railWarn {
		t.Fatalf("warn over info = %v, want railWarn", got)
	}

	a.lsp.diags[path] = append(a.lsp.diags[path], diagAt(302, 0, lsp.SeverityError, "err", "test"))
	if got := a.railMarks(tab, trackH, 0, 39)[row]; got != railError {
		t.Fatalf("error over warn = %v, want railError", got)
	}

	tab.SetFindQuery("line")
	if got := a.railMarks(tab, trackH, 0, 39)[row]; got != railFind {
		t.Fatalf("find over error = %v, want railFind — an asked question outranks ambient annotation", got)
	}

	tab.MoveCursorTo(editor.Position{Line: 303}, false)
	if got := a.railMarks(tab, trackH, 0, 39)[row]; got != railCaret {
		t.Fatalf("caret over find = %v, want railCaret", got)
	}
}

// TestRailMarks_CaretTickOnlyWhenScrolledAway pins the tick's whole
// reason for existing: it says where the cursor went. While the cursor is
// on screen the hardware cursor is the answer, and a tick would be a
// second cursor to explain.
func TestRailMarks_CaretTickOnlyWhenScrolledAway(t *testing.T) {
	a, _ := scrollbarApp(t, 400)
	tab := a.activeTabPtr()
	tab.MoveCursorTo(editor.Position{Line: 10}, false)

	if marks := a.railMarks(tab, 40, 0, 39); marks != nil {
		t.Fatalf("a caret inside the viewport drew marks %v, want none at all", marks)
	}

	marks := a.railMarks(tab, 40, 200, 239)
	if marks == nil {
		t.Fatal("caret scrolled off screen drew no tick")
	}
	if got := marks[railRow(10, 400, 40)]; got != railCaret {
		t.Fatalf("tick row = %v, want railCaret", got)
	}
}

// TestRailMarks_PluginsHonourTheKillSwitch pins that the plugin cache is
// gated at the READ, like every other plugin surface — the toggle has to
// mean "off" everywhere, not just at load.
func TestRailMarks_PluginsHonourTheKillSwitch(t *testing.T) {
	a, path := scrollbarApp(t, 400)
	tab := a.activeTabPtr()
	a.plugins.decos = map[string]map[string][]plugins.Diagnostic{
		path: {"vet": {{Line: 300, Col: 0, Severity: plugins.SevError, Message: "bad"}}},
	}
	row := railRow(300, 400, 40)

	a.plugins.enabled = true
	if got := a.railMarks(tab, 40, 0, 39)[row]; got != railError {
		t.Fatalf("plugin finding = %v, want railError", got)
	}

	a.plugins.enabled = false
	if marks := a.railMarks(tab, 40, 0, 39); marks != nil && marks[row] != railNone {
		t.Errorf("plugin finding survived the kill switch: %v", marks[row])
	}
}

// TestRailMarks_QuietWhenThereIsNothingToSay pins the cheap common case:
// a clean file with no search running allocates nothing, so the rail
// stays a plain rail and the per-frame cost is a handful of lookups.
func TestRailMarks_QuietWhenThereIsNothingToSay(t *testing.T) {
	a, _ := scrollbarApp(t, 400)
	if marks := a.railMarks(a.activeTabPtr(), 40, 0, 399); marks != nil {
		t.Fatalf("clean file produced %v, want nil", marks)
	}
}

// TestScrollbarDraw_MarksAndTick paints a real frame and reads the column
// back: the marks are the only affordance the feature has, and the thumb
// must survive them — it is the one thing on the column that has to keep
// reading as a shape.
func TestScrollbarDraw_MarksAndTick(t *testing.T) {
	a, path := scrollbarApp(t, 2000)
	tab := a.activeTabPtr()
	a.lsp.diags = map[string][]lsp.Diagnostic{path: {
		diagAt(1500, 0, lsp.SeverityError, "far below", "test"),
	}}
	// Through RestoreView, not MoveCursorTo: every ordinary cursor write
	// sets cursorMoved, and Render would then scroll the caret straight
	// back on screen — where the tick deliberately does not draw.
	tab.RestoreView(editor.Position{Line: 1000}, editor.Position{Line: 1000}, 0, 0)
	a.draw()
	a.screen.Show()

	sx, sy, _, sh := a.scrollbarRect()
	cells, w, _ := a.screen.(tcell.SimulationScreen).GetContents()
	var ticks, errs, thumbs int
	for row := 0; row < sh; row++ {
		c := cells[(sy+row)*w+sx]
		switch {
		case c.Runes[0] == railCaretRune:
			ticks++
		case c.Runes[0] == scrollbarThumbRune && c.Style == tcell.StyleDefault.
			Background(a.theme.BG).Foreground(a.theme.DiagError):
			errs++
		case c.Runes[0] == scrollbarThumbRune:
			thumbs++
		}
	}
	if ticks != 1 {
		t.Errorf("drew %d caret ticks, want exactly 1", ticks)
	}
	if errs != 1 {
		t.Errorf("drew %d error marks, want exactly 1", errs)
	}
	if thumbs < scrollbarMinThumb {
		t.Errorf("thumb drew %d rows, want at least %d — marks must not eat it", thumbs, scrollbarMinThumb)
	}
}
