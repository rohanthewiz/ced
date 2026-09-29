// =============================================================================
// File: internal/app/hovermodal_test.go
// Author: Rohan Allison <rohanthewiz@gmail.com>
// Created: 2026-07-09
// Copyright: 2026 Rohan Allison. All rights reserved.
// =============================================================================

package app

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gdamore/tcell/v2"

	"github.com/rohanthewiz/ced/internal/editor"
)

// hoverTestApp opens a tall Go file so cursor-anchoring tests can park
// the caret anywhere in the viewport.
func hoverTestApp(t *testing.T) *App {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "main.go")
	content := "package main\n" + strings.Repeat("// filler line\n", 60)
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	a := newTestApp(t, dir)
	a.openFile(path)
	a.draw() // resolve scroll/EnsureVisible so CursorScreenCell is live
	return a
}

// TestHoverModalRectBelowCursor pins the tooltip convention: with room
// underneath, the popup's top row sits directly below the caret's row.
func TestHoverModalRectBelowCursor(t *testing.T) {
	a := hoverTestApp(t)
	a.activeTabPtr().MoveCursorTo(editor.Position{Line: 3, Col: 0}, false)
	a.draw()

	m := &hoverModal{lines: []string{"one", "two"}}
	_, y, _, h := m.rect(a)

	ex, ey, ew, eh := a.editorRect()
	_ = ex
	dx, dy, ok := a.activeTabPtr().CursorScreenCell(ew, eh)
	_ = dx
	if !ok {
		t.Fatal("cursor should be on screen")
	}
	if want := ey + dy + 1; y != want {
		t.Errorf("popup y = %d, want %d (one row below caret)", y, want)
	}
	if h != 4 { // 2 lines + 2 border rows
		t.Errorf("popup h = %d, want 4", h)
	}
}

// TestHoverModalRectFlipsAbove pins the bottom-edge behavior: when the
// popup would clip the status bar it flips to sit above the caret.
func TestHoverModalRectFlipsAbove(t *testing.T) {
	a := hoverTestApp(t)
	tab := a.activeTabPtr()
	// Park the caret on the last visible row.
	_, _, _, eh := a.editorRect()
	tab.MoveCursorTo(editor.Position{Line: eh - 1, Col: 0}, false)
	a.draw()

	m := &hoverModal{lines: []string{"a", "b", "c"}}
	_, y, _, h := m.rect(a)

	ex, ey, ew, ehh := a.editorRect()
	_ = ex
	_, dy, ok := tab.CursorScreenCell(ew, ehh)
	if !ok {
		t.Fatal("cursor should be on screen")
	}
	cy := ey + dy
	if y+h > cy+1 {
		t.Errorf("popup [%d,%d) overlaps or passes the caret row %d — should flip above", y, y+h, cy)
	}
}

// TestHoverModalRectCenteredFallback pins the degenerate case: with the
// caret scrolled off-screen the popup falls back to center rather than
// anchoring to a stale cell.
func TestHoverModalRectCenteredFallback(t *testing.T) {
	a := hoverTestApp(t)
	a.activeTabPtr().Scroll(40) // caret at top, viewport far below
	a.draw()

	m := &hoverModal{lines: []string{"x"}}
	x, y, w, h := m.rect(a)
	cx, cy, _, _ := a.centeredRect(w, h)
	if x != cx || y != cy {
		t.Errorf("popup at (%d,%d), want centered (%d,%d)", x, y, cx, cy)
	}
}

// TestHoverModalDismissal pins the trigger-happy close contract: any
// key closes, any button click closes, wheel and motion do not.
func TestHoverModalDismissal(t *testing.T) {
	a := hoverTestApp(t)

	a.openModal(&hoverModal{lines: []string{"x"}})
	a.modal.handleKey(a, tcell.NewEventKey(tcell.KeyRune, 'z', tcell.ModNone))
	if a.modal != nil {
		t.Error("any key should dismiss the hover popup")
	}

	a.openModal(&hoverModal{lines: []string{"x"}})
	a.modal.handleMouse(a, 0, 0, tcell.WheelDown)
	if a.modal == nil {
		t.Error("wheel must not dismiss — the user may be reading")
	}
	a.modal.handleMouse(a, 0, 0, tcell.Button1)
	if a.modal != nil {
		t.Error("click should dismiss")
	}
}

// TestHoverModalDrawContent renders onto the sim screen and asserts
// the text lands inside the popup, with an over-wide line WRAPPED onto
// further rows (the box grows down) instead of bleeding through the
// border or being cut with an ellipsis.
func TestHoverModalDrawContent(t *testing.T) {
	a := hoverTestApp(t)
	long := strings.Repeat("w", hoverModalMaxWidth*2)
	m := &hoverModal{lines: []string{"short line", long}}
	a.openModal(m)
	a.draw()
	a.screen.Show()

	mx, my, mw, mh := m.rect(a)
	scr := a.screen.(tcell.SimulationScreen)
	cells, w, _ := scr.GetContents()

	row := func(y int) string {
		var b strings.Builder
		for x := mx; x < mx+mw; x++ {
			c := cells[y*w+x]
			if len(c.Runes) > 0 {
				b.WriteRune(c.Runes[0])
			}
		}
		return b.String()
	}
	if !strings.Contains(row(my+1), "short line") {
		t.Errorf("first body row = %q, want the hover text", row(my+1))
	}
	// 132 runes at 62 per row = 3 rows, plus the short line and borders.
	if mh != 1+3+2 {
		t.Errorf("popup h = %d, want 6 (the long line wrapped onto 3 rows)", mh)
	}
	total := 0
	for y := my + 2; y < my+mh-1; y++ {
		r := row(y)
		if strings.Contains(r, "…") {
			t.Errorf("row %d truncated instead of wrapped: %q", y, r)
		}
		total += strings.Count(r, "w")
	}
	if total != len(long) {
		t.Errorf("wrapped rows show %d of %d runes — text lost", total, len(long))
	}
	if strings.Contains(row(my+2), strings.Repeat("w", mw)) {
		t.Error("over-wide line bled past the popup border")
	}
}

// TestHoverModalWrapsTheReportedDocComment is the backlog screenshot as a
// test: a Go signature and doc paragraph wider than the box used to lose
// their tails to "…" — every word must now reach the screen.
func TestHoverModalWrapsTheReportedDocComment(t *testing.T) {
	a := hoverTestApp(t)
	lines := []string{
		"func (g *Generator) resolveRunDagTarget(dagMetadata map[string][]*task.Task) (string, error)",
		"resolveRunDagTarget decides which generated DAG carries the RunDag final dag id (empty when the run asked for no follow-up).",
	}
	m := &hoverModal{lines: lines}
	a.openModal(m)
	a.draw()
	a.screen.Show()

	mx, my, mw, mh := m.rect(a)
	scr := a.screen.(tcell.SimulationScreen)
	cells, w, _ := scr.GetContents()
	var body strings.Builder
	for y := my + 1; y < my+mh-1; y++ {
		for x := mx + 2; x < mx+mw-2; x++ {
			if c := cells[y*w+x]; len(c.Runes) > 0 {
				body.WriteRune(c.Runes[0])
			}
		}
		body.WriteByte(' ')
	}
	got := strings.Join(strings.Fields(body.String()), " ")
	for _, word := range strings.Fields(strings.Join(lines, " ")) {
		if !strings.Contains(got, word) {
			t.Errorf("word %q missing from the popup: %q", word, got)
		}
	}
	if strings.Contains(got, "…") {
		t.Errorf("popup still truncates: %q", got)
	}
}

// TestWrapTooltipLine pins the wrapper's mechanics: short lines pass
// through, breaks land on spaces (which are dropped), a spaceless run
// hard-breaks, and continuation rows hang at the line's own indent.
func TestWrapTooltipLine(t *testing.T) {
	render := func(ln string, w int) []string {
		r := []rune(ln)
		var out []string
		for _, sg := range wrapTooltipLine(r, w) {
			out = append(out, strings.Repeat(" ", sg.indent)+string(r[sg.start:sg.end]))
		}
		return out
	}
	cases := []struct {
		name string
		in   string
		w    int
		want []string
	}{
		{"fits", "hello", 10, []string{"hello"}},
		{"empty", "", 10, []string{""}},
		{"word break", "aaa bbb ccc", 7, []string{"aaa bbb", "ccc"}},
		{"break on the first rune past", "aaaa bbbb", 4, []string{"aaaa", "bbbb"}},
		{"space run collapses", "aa    bb", 4, []string{"aa", "bb"}},
		{"hard break", "abcdefghij", 4, []string{"abcd", "efgh", "ij"}},
		{"hanging indent", "  aa bb cc", 6, []string{"  aa", "  bb", "  cc"}},
		{"indent never a blank row", "    abcdefgh", 6, []string{"    ab", "cdefgh"}},
	}
	for _, c := range cases {
		got := render(c.in, c.w)
		if strings.Join(got, "|") != strings.Join(c.want, "|") {
			t.Errorf("%s: wrap(%q, %d) = %q, want %q", c.name, c.in, c.w, got, c.want)
		}
		for _, row := range got {
			if runeLen(row) > c.w {
				t.Errorf("%s: row %q wider than %d", c.name, row, c.w)
			}
		}
	}
}

// TestTooltipLayout_EmphasisFollowsTheWrap pins the offset bookkeeping
// signature help relies on in a narrow window: a run split by a wrap is
// emphasised on both rows, re-based to each row's own columns.
func TestTooltipLayout_EmphasisFollowsTheWrap(t *testing.T) {
	rows, emph := tooltipLayout(
		[]string{"f(a int, bb string)"},
		[]hoverEmph{{line: 0, start: 9, end: 18}}, // "bb string"
		12,
	)
	if strings.Join(rows, "|") != "f(a int, bb|string)" {
		t.Fatalf("rows = %q", rows)
	}
	want := []hoverEmph{{line: 0, start: 9, end: 11}, {line: 1, start: 0, end: 6}}
	if len(emph) != len(want) {
		t.Fatalf("emph = %+v, want %+v", emph, want)
	}
	for i := range want {
		if emph[i] != want[i] {
			t.Errorf("emph[%d] = %+v, want %+v", i, emph[i], want[i])
		}
		if got := string([]rune(rows[emph[i].line])[emph[i].start:emph[i].end]); !strings.Contains("bb string", got) {
			t.Errorf("emph[%d] covers %q — not part of the parameter", i, got)
		}
	}
}

// TestTooltipPlace_ShortensATooTallBox pins the growth limit: a box
// taller than the room on either side of the anchor takes the roomier
// side, shrinks to it, and still leaves the anchor row uncovered.
func TestTooltipPlace_ShortensATooTallBox(t *testing.T) {
	a := hoverTestApp(t)
	cy := a.height / 3 // more room below than above
	_, y, _, h := tooltipPlace(a, 20, a.height*2, 0, cy)
	if y != cy+1 {
		t.Errorf("y = %d, want %d (below, the roomier side)", y, cy+1)
	}
	if y+h > a.height-1 {
		t.Errorf("box [%d,%d) runs into the status bar at %d", y, y+h, a.height-1)
	}

	cy = a.height * 2 / 3 // more room above
	_, y, _, h = tooltipPlace(a, 20, a.height*2, 0, cy)
	if y+h > cy {
		t.Errorf("box [%d,%d) covers the anchor row %d", y, y+h, cy)
	}
	if y != 0 {
		t.Errorf("y = %d, want 0 (all the room above)", y)
	}
}

// TestDrawTooltipBox_MarksAShortenedBox pins the cut marker: rows that
// don't fit a shortened box end in "…", so a clipped tooltip never reads
// as the whole answer.
func TestDrawTooltipBox_MarksAShortenedBox(t *testing.T) {
	a := hoverTestApp(t)
	lines := []string{"one", "two", "three", "four", "five"}
	drawTooltipBox(a, lines, []hoverEmph{{line: 4, start: 0, end: 4}}, 0, 0, 20, 5) // 3 body rows
	a.screen.Show()
	scr := a.screen.(tcell.SimulationScreen)
	cells, w, _ := scr.GetContents()
	at := func(y int) string {
		var b strings.Builder
		for x := 2; x < 18; x++ {
			if c := cells[y*w+x]; len(c.Runes) > 0 {
				b.WriteRune(c.Runes[0])
			}
		}
		return strings.TrimSpace(b.String())
	}
	if at(1) != "one" || at(2) != "two" || at(3) != "…" {
		t.Errorf("body rows = %q %q %q, want one / two / …", at(1), at(2), at(3))
	}
}

// TestHoverModalDrawEmphasis pins signature help's whole contribution to
// this modal: the marked run is painted in the accent, bold, while the
// rest of the line stays body-styled. A run that rendered identically to
// its surroundings would make the verb indistinguishable from hover.
func TestHoverModalDrawEmphasis(t *testing.T) {
	a := hoverTestApp(t)
	m := &hoverModal{
		lines: []string{"f(a int, b string)"},
		emph:  []hoverEmph{{line: 0, start: 9, end: 17}}, // "b string"
	}
	a.openModal(m)
	a.draw()
	a.screen.Show()

	mx, my, _, _ := m.rect(a)
	scr := a.screen.(tcell.SimulationScreen)
	cells, w, _ := scr.GetContents()
	at := func(col int) tcell.Style { return cells[(my+1)*w+mx+2+col].Style }

	plainFG, _, _ := at(0).Decompose()
	emphFG, _, emphAttrs := at(9).Decompose()
	if emphFG == plainFG {
		t.Errorf("emphasised cell has the body foreground %v — nothing marks the parameter", emphFG)
	}
	if emphAttrs&tcell.AttrBold == 0 {
		t.Error("emphasised cell is not bold")
	}
	// The cell just past the run must be back to normal, or the emphasis
	// would run to end of line and mark the wrong parameter too.
	if afterFG, _, _ := at(17).Decompose(); afterFG == emphFG {
		t.Error("emphasis leaked past the parameter's end")
	}
}

// TestHoverModalEmphasisClampsToTruncation pins the interaction between
// the two: a line wrapped by the width cap must carry its emphasis onto
// the rows it lands on rather than indexing past any one row's runes.
func TestHoverModalEmphasisClampsToTruncation(t *testing.T) {
	a := hoverTestApp(t)
	long := strings.Repeat("w", hoverModalMaxWidth*2)
	m := &hoverModal{
		lines: []string{long},
		emph:  []hoverEmph{{line: 0, start: 0, end: len(long)}},
	}
	a.openModal(m)
	a.draw() // must not panic
	a.screen.Show()
}
