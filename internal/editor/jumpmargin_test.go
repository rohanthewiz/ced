// =============================================================================
// File: internal/editor/jumpmargin_test.go
// Author: Rohan Allison <rohanthewiz@gmail.com>
// Created: 2026-10-03
// Copyright: 2026 Rohan Allison. All rights reserved.
// =============================================================================

package editor

import (
	"fmt"
	"strings"
	"testing"

	"github.com/rohanthewiz/ced/internal/theme"
)

// jmViewW / jmViewH are the render size the unwrapped tests use; at 20
// rows the full JumpMargin (5) fits under the quarter-of-view cap.
const (
	jmViewW = 40
	jmViewH = 20
)

// jmTab builds an unwrapped tab of n numbered lines.
func jmTab(n int) *Tab {
	lines := make([]string, n)
	for i := range lines {
		lines[i] = fmt.Sprintf("line %d", i)
	}
	return &Tab{Buffer: NewBuffer(strings.Join(lines, "\n")), StyleStale: true}
}

// jmRender renders tab once into a w×h simulation screen, which is where
// the cursorMoved / jumpReveal flags are consumed.
func jmRender(t *testing.T, tab *Tab, w, h int) {
	t.Helper()
	scr := newSimScreen(t, w, h)
	defer scr.Fini()
	tab.Render(scr, theme.Default(), 0, 0, w, h)
}

// TestJumpMargin_JumpBelowViewLandsWithContext pins the bug this file
// fixes: a jump to a line below the view used to park it on the last row;
// marked as a jump it lands JumpMargin rows above the bottom edge.
func TestJumpMargin_JumpBelowViewLandsWithContext(t *testing.T) {
	tab := jmTab(100)
	tab.MoveCursorTo(Position{Line: 50}, false)
	tab.MarkJump()
	jmRender(t, tab, jmViewW, jmViewH)

	if want := 50 + JumpMargin - jmViewH + 1; tab.ScrollY != want {
		t.Fatalf("ScrollY = %d, want %d (caret %d rows above the bottom edge)", tab.ScrollY, want, JumpMargin)
	}
}

// TestJumpMargin_PlainMoveStaysMinimal keeps ordinary motion on the
// minimal-scroll rule: an unmarked move below the view lands on the last
// row, so arrows and clicks never make the view lurch.
func TestJumpMargin_PlainMoveStaysMinimal(t *testing.T) {
	tab := jmTab(100)
	tab.MoveCursorTo(Position{Line: 50}, false)
	jmRender(t, tab, jmViewW, jmViewH)

	if want := 50 - jmViewH + 1; tab.ScrollY != want {
		t.Fatalf("ScrollY = %d, want %d (minimal scroll)", tab.ScrollY, want)
	}
}

// TestJumpMargin_OnScreenEdgeRowsNudged covers a hit that is already on
// screen but on the bottom or top row: the margin nudges the view so it
// sits JumpMargin rows in from that edge.
func TestJumpMargin_OnScreenEdgeRowsNudged(t *testing.T) {
	tab := jmTab(100)
	tab.MoveCursorTo(Position{Line: jmViewH - 1}, false) // bottom row of a ScrollY-0 view
	tab.MarkJump()
	jmRender(t, tab, jmViewW, jmViewH)
	if tab.ScrollY != JumpMargin {
		t.Fatalf("bottom row: ScrollY = %d, want %d", tab.ScrollY, JumpMargin)
	}

	tab.ScrollY = 40
	tab.MoveCursorTo(Position{Line: 40}, false) // top row
	tab.MarkJump()
	jmRender(t, tab, jmViewW, jmViewH)
	if want := 40 - JumpMargin; tab.ScrollY != want {
		t.Fatalf("top row: ScrollY = %d, want %d", tab.ScrollY, want)
	}
}

// TestJumpMargin_ComfortableHitDoesNotScroll checks a jump that already
// has its margin on both sides leaves the view alone — the margin is a
// minimum, not a recentering.
func TestJumpMargin_ComfortableHitDoesNotScroll(t *testing.T) {
	tab := jmTab(100)
	tab.ScrollY = 30
	tab.MoveCursorTo(Position{Line: 40}, false)
	tab.MarkJump()
	jmRender(t, tab, jmViewW, jmViewH)
	if tab.ScrollY != 30 {
		t.Fatalf("ScrollY = %d, want 30 (unchanged)", tab.ScrollY)
	}
}

// TestJumpMargin_FileEdgesAreTheLimit checks the margin never invents
// rows: a jump to line 1 stays at ScrollY 0, and a jump to the last line
// leaves it on the bottom row rather than scrolling into blank overscroll.
func TestJumpMargin_FileEdgesAreTheLimit(t *testing.T) {
	tab := jmTab(100)
	tab.ScrollY = 50
	tab.MoveCursorTo(Position{Line: 1}, false)
	tab.MarkJump()
	jmRender(t, tab, jmViewW, jmViewH)
	if tab.ScrollY != 0 {
		t.Fatalf("top of file: ScrollY = %d, want 0", tab.ScrollY)
	}

	tab.MoveCursorTo(Position{Line: 99}, false)
	tab.MarkJump()
	jmRender(t, tab, jmViewW, jmViewH)
	if want := 100 - jmViewH; tab.ScrollY != want {
		t.Fatalf("end of file: ScrollY = %d, want %d (no blank overscroll)", tab.ScrollY, want)
	}

	// Two lines from the end: only two rows of context exist below it.
	tab.ScrollY = 0
	tab.MoveCursorTo(Position{Line: 97}, false)
	tab.MarkJump()
	jmRender(t, tab, jmViewW, jmViewH)
	if want := 100 - jmViewH; tab.ScrollY != want {
		t.Fatalf("near end: ScrollY = %d, want %d", tab.ScrollY, want)
	}
}

// TestJumpMargin_SmallPaneScalesDown checks a short pane gets a quarter
// of its height as margin, not the full JumpMargin.
func TestJumpMargin_SmallPaneScalesDown(t *testing.T) {
	if got := jumpMarginFor(JumpMargin, 8); got != 2 {
		t.Fatalf("jumpMarginFor(8 rows) = %d, want 2", got)
	}
	if got := jumpMarginFor(JumpMargin, 2); got != 0 {
		t.Fatalf("jumpMarginFor(2 rows) = %d, want 0", got)
	}
	tab := jmTab(100)
	tab.MoveCursorTo(Position{Line: 50}, false)
	tab.MarkJump()
	jmRender(t, tab, jmViewW, 8)
	if want := 50 + 2 - 8 + 1; tab.ScrollY != want {
		t.Fatalf("ScrollY = %d, want %d", tab.ScrollY, want)
	}
}

// TestJumpMargin_FindHitIsAJump checks the find bar's landing
// (FocusCurrentMatch) marks itself, so Enter in the bar never parks the
// match on the view's last row.
func TestJumpMargin_FindHitIsAJump(t *testing.T) {
	tab := jmTab(100)
	tab.SetFindQuery("line 60")
	tab.FocusCurrentMatch()
	jmRender(t, tab, jmViewW, jmViewH)
	if tab.Cursor.Line != 60 {
		t.Fatalf("cursor on line %d, want 60", tab.Cursor.Line)
	}
	if want := 60 + JumpMargin - jmViewH + 1; tab.ScrollY != want {
		t.Fatalf("ScrollY = %d, want %d", tab.ScrollY, want)
	}
}

// TestJumpMargin_IsOneShot checks the mark dies with the Render that
// consumed it, and that RestoreView / CenterOnCursor drop it: a later
// plain move must scroll minimally again.
func TestJumpMargin_IsOneShot(t *testing.T) {
	tab := jmTab(100)
	tab.MoveCursorTo(Position{Line: 50}, false)
	tab.MarkJump()
	jmRender(t, tab, jmViewW, jmViewH)
	if tab.jumpReveal {
		t.Fatal("jumpReveal survived the Render that consumed it")
	}

	tab.MarkJump()
	tab.RestoreView(Position{Line: 3}, Position{Line: 3}, 0, 0)
	if tab.jumpReveal {
		t.Fatal("RestoreView left jumpReveal set")
	}
	tab.MarkJump()
	tab.CenterOnCursor(jmViewW, jmViewH)
	if tab.jumpReveal {
		t.Fatal("CenterOnCursor left jumpReveal set")
	}
}

// TestJumpMargin_WrappedCountsRows checks the soft-wrapped rule works in
// display rows: 50-rune lines are three rows each at the test wrap width,
// so a jump to line 30 drops whole top lines until five rows (the rest of
// line 30 and line 31) show below the caret's row.
func TestJumpMargin_WrappedCountsRows(t *testing.T) {
	text := strings.TrimSuffix(strings.Repeat(strings.Repeat("x", 50)+"\n", 40), "\n")
	tab, scr := newWrappedTab(t, text, jmViewH)
	tab.MoveCursorTo(Position{Line: 30, Col: 0}, false)
	tab.MarkJump()
	tab.Render(scr, theme.Default(), 0, 0, wrapTestW, jmViewH)

	// Minimal would be ScrollY 24 (18 rows above the caret's row, which
	// sits on the last row); with the margin it is 26 (12 rows above, 7
	// below).
	if tab.ScrollY != 26 {
		t.Fatalf("ScrollY = %d, want 26", tab.ScrollY)
	}
}
