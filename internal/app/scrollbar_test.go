// =============================================================================
// File: internal/app/scrollbar_test.go
// Author: Rohan Allison <rohanthewiz@gmail.com>
// Created: 2026-08-23
// Copyright: 2026 Rohan Allison. All rights reserved.
// =============================================================================

package app

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gdamore/tcell/v2"

	"github.com/rohanthewiz/ced/internal/userconfig"
)

// scrollbarApp builds an App with the bar ON (newTestApp deliberately
// leaves it off, the treeAutoFit precedent) and a tab holding `lines`
// numbered rows, so the fixture can say exactly how much is off-screen.
// The config is redirected at a temp dir so the ≡ toggle can persist
// without touching the developer's real config.json.
func scrollbarApp(t *testing.T, lines int) (*App, string) {
	t.Helper()
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	root := t.TempDir()
	body := make([]string, lines)
	for i := range body {
		body[i] = "line"
	}
	target := filepath.Join(root, "long.txt")
	if err := os.WriteFile(target, []byte(strings.Join(body, "\n")), 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	a := newTestApp(t, root)
	a.scrollbarShown = true
	a.openFile(target)
	if a.activeTabPtr() == nil {
		t.Fatalf("fixture failed to open %s", target)
	}
	return a, target
}

// TestScrollbarCols_CostsTheEditorOneColumn is the geometry contract: the
// bar DISPLACES the editor rather than floating over its last column, so
// turning it on must be visible in editorRect and nowhere else.
func TestScrollbarCols_CostsTheEditorOneColumn(t *testing.T) {
	a, _ := scrollbarApp(t, 200)

	_, _, onW, _ := a.editorRect()
	a.scrollbarShown = false
	_, _, offW, _ := a.editorRect()

	if offW-onW != 1 {
		t.Fatalf("editor width with bar = %d, without = %d; want a 1-column difference", onW, offW)
	}
	if got := a.scrollbarCols(); got != 0 {
		t.Errorf("scrollbarCols with the preference off = %d, want 0", got)
	}
}

// TestScrollbarCols_NoTabNoColumn pins the two cases that give the column
// back: nothing open (there is no scroll position to report) and a band
// too narrow to spare it (the code is the scarce thing at that size).
func TestScrollbarCols_NoTabNoColumn(t *testing.T) {
	a, _ := scrollbarApp(t, 200)

	if got := a.scrollbarCols(); got != 1 {
		t.Fatalf("scrollbarCols with a tab open = %d, want 1", got)
	}
	a.closeTab(a.activeTab)
	if got := a.scrollbarCols(); got != 0 {
		t.Errorf("scrollbarCols with no tab = %d, want 0", got)
	}

	// Reopen and starve the band instead.
	a2, _ := scrollbarApp(t, 200)
	a2.sidebarShown = false
	a2.width = scrollbarMinEditor // band is exactly the floor; the bar can't fit under it
	if got := a2.scrollbarCols(); got != 0 {
		t.Errorf("scrollbarCols on a %d-column window = %d, want 0", a2.width, got)
	}
}

// TestScrollbarRect_SitsOnTheEditorsEdge pins where the column lands: at
// ex+ew, spanning exactly the editor's rows. Everything else in the
// feature (draw, hit-test, drag) reads this one rect.
func TestScrollbarRect_SitsOnTheEditorsEdge(t *testing.T) {
	a, _ := scrollbarApp(t, 200)
	ex, ey, ew, eh := a.editorRect()
	sx, sy, sw, sh := a.scrollbarRect()

	if sx != ex+ew || sy != ey || sw != 1 || sh != eh {
		t.Fatalf("scrollbarRect = (%d,%d,%d,%d), want (%d,%d,1,%d)", sx, sy, sw, sh, ex+ew, ey, eh)
	}
	if !a.scrollbarContains(sx, sy) || !a.scrollbarContains(sx, sy+sh-1) {
		t.Error("scrollbarContains missed its own rect")
	}
	if a.scrollbarContains(sx-1, sy) || a.scrollbarContains(sx, sy+sh) {
		t.Error("scrollbarContains claimed a cell outside the bar")
	}
}

// TestScrollbarMetrics covers the arithmetic and each of its degenerate
// cases: a file that fits (full-height thumb — the honest way to say
// "this is all of it"), a file far longer than the window (a thumb no
// shorter than scrollbarMinThumb, because at that point it is a grab
// handle before it is a readout), a track shorter than that floor (where
// the ceiling has to win, or the thumb places off the end of its own
// track), and the two ends of travel.
func TestScrollbarMetrics(t *testing.T) {
	cases := []struct {
		name                              string
		total, viewH, trackH, scrollY, ms int
		wantY, wantH                      int
	}{
		{"fits entirely", 10, 20, 20, 0, 0, 0, 20},
		{"exactly fits", 20, 20, 20, 0, 0, 0, 20},
		{"empty buffer", 0, 20, 20, 0, 0, 0, 20},
		{"top of a long file", 200, 20, 20, 0, 190, 0, 2},
		{"bottom of a long file", 200, 20, 20, 190, 190, 18, 2},
		{"midway", 200, 20, 20, 95, 190, 9, 2},
		{"huge file keeps a grabbable thumb", 100000, 20, 20, 0, 99990, 0, scrollbarMinThumb},
		{"track shorter than the thumb floor", 100000, 1, 1, 0, 99990, 0, 1},
		{"no track", 200, 20, 0, 0, 190, 0, 0},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			y, h := scrollbarMetrics(c.total, c.viewH, c.trackH, c.scrollY, c.ms)
			if y != c.wantY || h != c.wantH {
				t.Fatalf("scrollbarMetrics = (y%d h%d), want (y%d h%d)", y, h, c.wantY, c.wantH)
			}
			if h > 0 && y+h > c.trackH {
				t.Errorf("thumb (y%d h%d) overflows a %d-row track", y, h, c.trackH)
			}
		})
	}
}

// TestScrollbarThumb_TracksTheViewport is the feature as the user sees
// it: the thumb is short when most of the file is off-screen, and it
// walks the track as the buffer scrolls — ending flush at the bottom on
// the last line of travel, which is what makes the drag reach the end.
func TestScrollbarThumb_TracksTheViewport(t *testing.T) {
	a, _ := scrollbarApp(t, 500)
	tab := a.activeTabPtr()
	_, trackH, thumbY, thumbH, maxScroll, ok := a.scrollbarThumb()
	if !ok {
		t.Fatal("scrollbarThumb reported no bar")
	}
	if thumbH >= trackH {
		t.Fatalf("thumb fills a %d-row track for a 500-line file (h=%d)", trackH, thumbH)
	}
	if thumbY != 0 {
		t.Errorf("thumb at y%d with the buffer at the top, want 0", thumbY)
	}

	tab.ScrollY = maxScroll
	_, _, thumbY, thumbH, _, _ = a.scrollbarThumb()
	if got := thumbY + thumbH; got != trackH {
		t.Errorf("thumb bottom = %d at max scroll, want the track's %d", got, trackH)
	}
}

// TestScrollbarDrag_MovesTheViewport pins the gesture end to end through
// the real mouse router: a press on the thumb starts the drag, motion
// scrolls, release clears the mode. Dragging past the bottom parks at the
// last legal scroll rather than doing nothing.
func TestScrollbarDrag_MovesTheViewport(t *testing.T) {
	a, _ := scrollbarApp(t, 500)
	tab := a.activeTabPtr()
	sx, sy, _, sh := a.scrollbarRect()

	a.handleMouse(tcell.NewEventMouse(sx, sy, tcell.Button1, tcell.ModNone))
	if a.dragMode != "scrollbar" {
		t.Fatalf("dragMode = %q after pressing the thumb, want scrollbar", a.dragMode)
	}
	if tab.ScrollY != 0 {
		t.Fatalf("the press itself scrolled to %d, want 0", tab.ScrollY)
	}

	// Halfway down the track lands roughly halfway through the travel.
	a.handleMouse(tcell.NewEventMouse(sx, sy+sh/2, tcell.Button1, tcell.ModNone))
	maxScroll := tab.MaxScroll(sh)
	if tab.ScrollY <= 0 || tab.ScrollY >= maxScroll {
		t.Fatalf("mid-track drag put ScrollY at %d, want strictly inside (0, %d)", tab.ScrollY, maxScroll)
	}

	// Off the bottom of the screen entirely: the clamp is what makes this
	// park at the end instead of being ignored.
	a.handleMouse(tcell.NewEventMouse(sx, a.height+50, tcell.Button1, tcell.ModNone))
	if tab.ScrollY != maxScroll {
		t.Errorf("drag past the bottom put ScrollY at %d, want %d", tab.ScrollY, maxScroll)
	}

	a.handleMouse(tcell.NewEventMouse(sx, sy, tcell.ButtonNone, tcell.ModNone))
	if a.dragMode != "" {
		t.Errorf("dragMode = %q after release, want cleared", a.dragMode)
	}
}

// TestScrollbarPress_TrackPages pins the other half of the press verb: a
// click below the thumb pages down instead of jumping the thumb under the
// pointer, and starts no drag.
func TestScrollbarPress_TrackPages(t *testing.T) {
	a, _ := scrollbarApp(t, 500)
	tab := a.activeTabPtr()
	sx, sy, _, sh := a.scrollbarRect()

	a.handleMouse(tcell.NewEventMouse(sx, sy+sh-1, tcell.Button1, tcell.ModNone))
	if a.dragMode != "" {
		t.Errorf("a track press started drag mode %q, want none", a.dragMode)
	}
	if tab.ScrollY != sh {
		t.Fatalf("ScrollY = %d after paging down, want one viewport (%d)", tab.ScrollY, sh)
	}

	// And back up. The zero floor lives in Tab.Scroll, so a page up from
	// the first page lands exactly at the top rather than going negative.
	a.handleMouse(tcell.NewEventMouse(sx, sy, tcell.Button1, tcell.ModNone))
	if tab.ScrollY != 0 {
		t.Errorf("ScrollY = %d after paging back up, want 0", tab.ScrollY)
	}
}

// TestScrollbarPress_DoesNotMoveTheCaret is why the hit-test has to run
// before the editor catch-all: the bar's column is inside the editor's
// y-band, so an unasked press would move the cursor to whatever line the
// user grabbed the thumb on.
func TestScrollbarPress_DoesNotMoveTheCaret(t *testing.T) {
	a, _ := scrollbarApp(t, 500)
	tab := a.activeTabPtr()
	before := tab.Cursor
	sx, sy, _, sh := a.scrollbarRect()

	a.handleMouse(tcell.NewEventMouse(sx, sy+sh/2, tcell.Button1, tcell.ModNone))
	if tab.Cursor != before {
		t.Errorf("cursor moved to %v on a scrollbar press, want %v", tab.Cursor, before)
	}
}

// TestScrollbarDraw paints a frame and asserts the column carries a thumb
// over a track — the bar is the only affordance the feature has, so a
// silent failure to draw it is the whole feature missing.
func TestScrollbarDraw(t *testing.T) {
	a, _ := scrollbarApp(t, 500)
	a.draw()
	a.screen.Show()

	sx, sy, _, sh := a.scrollbarRect()
	cells, w, _ := a.screen.(tcell.SimulationScreen).GetContents()
	var thumb, track int
	for row := 0; row < sh; row++ {
		switch cells[(sy+row)*w+sx].Runes[0] {
		case scrollbarThumbRune:
			thumb++
		case scrollbarTrackRune:
			track++
		}
	}
	if thumb == 0 || track == 0 {
		t.Fatalf("scrollbar column drew %d thumb / %d track rows in %d, want both", thumb, track, sh)
	}
	if thumb+track != sh {
		t.Errorf("scrollbar column drew %d recognised rows, want all %d", thumb+track, sh)
	}
}

// TestScrollbarToggle_PersistsAndRelabels pins the ≡ row: it names the
// action it will perform (not the state it's in) and writes the choice to
// config.json, since the preference outlives the session.
func TestScrollbarToggle_PersistsAndRelabels(t *testing.T) {
	a, _ := scrollbarApp(t, 200)

	if got := a.scrollbarToggleLabel(); got != "Hide scrollbar" {
		t.Errorf("label with the bar on = %q", got)
	}
	a.menuToggleScrollbar()
	if a.scrollbarShown {
		t.Fatal("toggle left the bar on")
	}
	if got := a.scrollbarToggleLabel(); got != "Show scrollbar" {
		t.Errorf("label with the bar off = %q", got)
	}

	cfg, err := userconfig.Load(userconfig.DefaultPath())
	if err != nil {
		t.Fatalf("reload config: %v", err)
	}
	if cfg.Scrollbar {
		t.Error("config still says scrollbar on after the toggle wrote it off")
	}
}
