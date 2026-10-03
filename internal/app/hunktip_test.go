// =============================================================================
// File: internal/app/hunktip_test.go
// Author: Rohan Allison <rohanthewiz@gmail.com>
// Created: 2026-10-03
// Copyright: 2026 Rohan Allison. All rights reserved.
// =============================================================================

// Tests for the change popup: its text (title, rows, clipping), the
// cell → hunk resolution and its refusals, the hover lifecycle including
// entering and scrolling the box, the click door, and what is painted.

package app

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gdamore/tcell/v2"

	"github.com/rohanthewiz/ced/internal/editor"
	"github.com/rohanthewiz/ced/internal/lsp"
)

// hunkTipTestApp opens a 40-line file whose lines 3–4 (0-based 2–3) are
// marked changed by a hunk with a 20-row body — taller than the popup,
// so it scrolls — and draws once to settle the geometry. Returns the app
// and the screen cell of line 2's change bar.
func hunkTipTestApp(t *testing.T) (a *App, barX, barY int) {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "f.go")
	var src strings.Builder
	for i := 0; i < 40; i++ {
		fmt.Fprintf(&src, "line %d\n", i+1)
	}
	writeFileT(t, path, src.String())

	a = newTestApp(t, dir)
	a.width, a.height = 120, 40
	a.openFile(path)
	var body []string
	for i := 0; i < 10; i++ {
		body = append(body, fmt.Sprintf("-old %d", i))
	}
	for i := 0; i < 10; i++ {
		body = append(body, fmt.Sprintf("+\tnew %d", i))
	}
	a.fileDiffs = map[string][]diffHunk{path: {{Start: 2, End: 3, Kind: diffModified, Body: body}}}
	a.draw()

	tab := a.activeTabPtr()
	ex, ey, ew, eh := a.editorRect()
	_, dy, ok := tab.PosScreenCell(editor.Position{Line: 2}, ew, eh)
	if !ok {
		t.Fatal("line 2 is not on screen")
	}
	_, annEnd := tab.AnnotationCols()
	return a, ex + annEnd, ey + dy
}

// TestHunkTipTitle names each kind in the gutter's terms, with 1-based
// line numbers and the −/+ size.
func TestHunkTipTitle(t *testing.T) {
	cases := []struct {
		h    diffHunk
		want string
	}{
		{diffHunk{Start: 4, End: 4, Kind: diffAdded, Body: []string{"+a"}}, "Added · line 5  +1"},
		{diffHunk{Start: 4, End: 6, Kind: diffModified, Body: []string{"-a", "+b", "+c", "+d"}}, "Changed · lines 5–7  −1 +3"},
		{diffHunk{Start: 9, End: 9, Kind: diffDeleted, Body: []string{"-a", "-b"}}, "Removed · below line 10  −2"},
		{diffHunk{Start: 0, End: 0, Kind: diffDeleted, Top: true, Body: []string{"-a"}}, "Removed · above line 1  −1"},
	}
	for _, c := range cases {
		if got := hunkTipTitle(c.h); got != c.want {
			t.Errorf("title = %q, want %q", got, c.want)
		}
	}
}

// TestHunkTipLines keeps the prefixes, expands tabs and drops CRs.
func TestHunkTipLines(t *testing.T) {
	got := hunkTipLines(diffHunk{Body: []string{"-\tx\r", "+y"}})
	if len(got) != 2 || got[0] != "-    x" || got[1] != "+y" {
		t.Fatalf("rows = %q", got)
	}
}

// TestClipRunesAndScrollClamp pins the two small helpers the painter
// leans on: clipping marks its cut, the scroll stays inside the content.
func TestClipRunesAndScrollClamp(t *testing.T) {
	if got := clipRunes("abcdef", 4); got != "abc…" {
		t.Errorf("clip = %q", got)
	}
	if got := clipRunes("abc", 4); got != "abc" {
		t.Errorf("short clip = %q", got)
	}
	if got := clampHunkTipScroll(50, 20, 16); got != 4 {
		t.Errorf("clamp high = %d, want 4", got)
	}
	if got := clampHunkTipScroll(-3, 20, 16); got != 0 {
		t.Errorf("clamp low = %d, want 0", got)
	}
	if got := clampHunkTipScroll(2, 5, 16); got != 0 {
		t.Errorf("content that fits must not scroll, got %d", got)
	}
}

// TestHunkAtCell answers only on the bar itself: the mark cell of a
// changed line. The code beside it, an unchanged line's mark cell, and a
// changed line whose mark cell shows a diagnostic dot are all refused.
func TestHunkAtCell(t *testing.T) {
	a, barX, barY := hunkTipTestApp(t)
	if h, ok := a.hunkAtCell(barX, barY); !ok || h.Start != 2 {
		t.Fatalf("bar cell → %+v, %v", h, ok)
	}
	if _, ok := a.hunkAtCell(barX, barY+1); !ok {
		t.Error("the hunk's second line has a bar too")
	}
	if _, ok := a.hunkAtCell(barX+1, barY); ok {
		t.Error("the code beside the bar answered")
	}
	if _, ok := a.hunkAtCell(barX-1, barY); ok {
		t.Error("the line number answered")
	}
	if _, ok := a.hunkAtCell(barX, barY+5); ok {
		t.Error("an unchanged line's mark cell answered")
	}

	path := a.activeTabPtr().Path
	a.lsp.diags = map[string][]lsp.Diagnostic{path: {{
		Range:    lsp.Range{Start: lsp.Position{Line: 2}, End: lsp.Position{Line: 2, Character: 1}},
		Severity: lsp.SeverityError, Message: "boom",
	}}}
	if _, ok := a.hunkAtCell(barX, barY); ok {
		t.Error("a line whose mark cell shows a dot answered with the change")
	}
}

// TestHunkTip_Lifecycle walks the hover path: a rest on the bar arms and
// the tick opens; the box hangs off the anchor without covering it;
// motion INTO the box keeps it and is claimed; the wheel inside scrolls
// (clamped) and is claimed; the wheel outside closes and falls through.
func TestHunkTip_Lifecycle(t *testing.T) {
	a, barX, barY := hunkTipTestApp(t)

	before := a.hunkTip.seq
	a.noteHunkTipPointer(barX+3, barY, tcell.ButtonNone)
	if a.hunkTip.seq != before {
		t.Error("a pointer over code armed the popup")
	}
	a.noteHunkTipPointer(barX, barY, tcell.ButtonNone)
	if a.hunkTip.seq == before {
		t.Fatal("a pointer on the bar armed nothing")
	}
	stale := a.hunkTip.seq - 1
	a.handleHunkTipTick(&hunkTipEvent{seq: stale})
	if a.hunkTip.open {
		t.Fatal("a stale tick opened the popup")
	}
	a.handleHunkTipTick(&hunkTipEvent{seq: a.hunkTip.seq})
	if !a.hunkTip.open || len(a.hunkTip.lines) != 20 {
		t.Fatalf("tick left the popup %+v", a.hunkTip)
	}

	a.draw()
	b := a.hunkTip.box
	if b.w == 0 || b.h != hunkTipMaxRows+2 {
		t.Fatalf("box = %+v, want %d rows tall", b, hunkTipMaxRows+2)
	}
	if a.hunkTipContains(barX, barY) {
		t.Error("the popup covers the bar it describes")
	}

	// The trip from the bar into the box: the cell below the anchor.
	if !a.noteHunkTipPointer(barX, barY+1, tcell.ButtonNone) || !a.hunkTip.open {
		t.Fatal("moving into the box should keep it open and be claimed")
	}
	if !a.noteHunkTipPointer(barX+5, barY+4, tcell.WheelDown) {
		t.Fatal("the wheel inside the box was not claimed")
	}
	if a.hunkTip.scroll != wheelLines {
		t.Errorf("scroll = %d, want %d", a.hunkTip.scroll, wheelLines)
	}
	for i := 0; i < 10; i++ {
		a.noteHunkTipPointer(barX+5, barY+4, tcell.WheelDown)
	}
	if want := 20 - hunkTipMaxRows; a.hunkTip.scroll != want {
		t.Errorf("scroll ran to %d, want clamp at %d", a.hunkTip.scroll, want)
	}

	if a.noteHunkTipPointer(b.x+b.w+2, barY, tcell.WheelDown) {
		t.Error("the wheel outside the box was claimed")
	}
	if a.hunkTip.open {
		t.Error("the wheel outside the box left it open")
	}
}

// TestHunkTip_MotionOffCloses: leaving both the bar and the box closes
// the popup at once, without claiming the motion.
func TestHunkTip_MotionOffCloses(t *testing.T) {
	a, barX, barY := hunkTipTestApp(t)
	a.openHunkTipAt(barX, barY)
	a.draw()
	if a.noteHunkTipPointer(barX+3, barY, tcell.ButtonNone) {
		t.Error("motion off the popup was claimed")
	}
	if a.hunkTip.open {
		t.Error("motion off the bar and box left it open")
	}
}

// TestHunkGutterPress pins the click door: a press on the bar opens the
// popup without moving the caret, the release keeps it, a second click
// closes it, and a press on code is left to the caret.
func TestHunkGutterPress(t *testing.T) {
	a, barX, barY := hunkTipTestApp(t)
	tab := a.activeTabPtr()
	start := tab.Cursor
	press := func(x, y int) bool {
		a.noteHunkTipPointer(x, y, tcell.Button1) // the router's order
		return a.hunkGutterPress(x, y)
	}
	if press(barX+3, barY) {
		t.Error("a press on code was claimed")
	}
	if !press(barX, barY) || !a.hunkTip.open {
		t.Fatalf("bar press left the popup %+v", a.hunkTip)
	}
	if tab.Cursor != start {
		t.Errorf("cursor moved to %+v", tab.Cursor)
	}
	a.noteHunkTipPointer(barX, barY, tcell.ButtonNone) // the release
	if !a.hunkTip.open {
		t.Error("the release closed the popup it opened")
	}
	if !press(barX, barY) || a.hunkTip.open {
		t.Error("a second click on the bar should be claimed and close it")
	}
}

// TestHunkTip_DrawsDiffAndPosition reads the painted popup: the title in
// the top border, colored -/+ rows, the ▾ marker, and the "a–b of n"
// position in the bottom border; scrolling moves both.
func TestHunkTip_DrawsDiffAndPosition(t *testing.T) {
	a, barX, barY := hunkTipTestApp(t)
	a.openHunkTipAt(barX, barY)
	a.draw()
	b := a.hunkTip.box

	top := screenRow(t, a, b.y, b.x, b.w)
	if !strings.Contains(top, "Changed · lines 3–4  −10 +10") {
		t.Errorf("top border = %q", top)
	}
	first := screenRow(t, a, b.y+1, b.x, b.w)
	if !strings.Contains(first, "-old 0") {
		t.Errorf("first row = %q", first)
	}
	_, _, st, _ := a.screen.GetContent(b.x+2, b.y+1)
	if fg, _, _ := st.Decompose(); fg != a.theme.GitDeleted {
		t.Errorf("a removed row is not drawn in GitDeleted")
	}
	if r, _, _, _ := a.screen.GetContent(b.x+b.w-1, b.y+b.h-2); r != overflowDownRune {
		t.Errorf("no ▾ on the border with rows below (got %q)", r)
	}
	if bottom := screenRow(t, a, b.y+b.h-1, b.x, b.w); !strings.Contains(bottom, "1–16 of 20") {
		t.Errorf("bottom border = %q", bottom)
	}

	a.scrollHunkTip(100)
	a.draw()
	if bottom := screenRow(t, a, b.y+b.h-1, b.x, b.w); !strings.Contains(bottom, "5–20 of 20") {
		t.Errorf("scrolled bottom border = %q", bottom)
	}
	last := screenRow(t, a, b.y+b.h-2, b.x, b.w)
	if !strings.Contains(last, "+    new 9") {
		t.Errorf("last row = %q (tabs should expand)", last)
	}
	if r, _, _, _ := a.screen.GetContent(b.x+b.w-1, b.y+1); r != overflowUpRune {
		t.Errorf("no ▴ on the border with rows above (got %q)", r)
	}
}

// TestHunkTip_KeyAndTabSwitchHide: a key closes the popup (the passive
// contract), and a popup for another tab is never drawn over this one.
func TestHunkTip_KeyAndTabSwitchHide(t *testing.T) {
	a, barX, barY := hunkTipTestApp(t)
	a.openHunkTipAt(barX, barY)
	a.handleEvent(tcell.NewEventKey(tcell.KeyRight, 0, tcell.ModNone))
	if a.hunkTip.open {
		t.Error("a key left the popup open")
	}

	a.openHunkTipAt(barX, barY)
	a.hunkTip.path = "/somewhere/else.go"
	if a.hunkTipVisible() {
		t.Error("a popup for another file is visible over this tab")
	}
}

// TestLoadFileDiff_CarriesHunkBodies runs real git: the hunks the gutter
// gets now carry the text the popup shows.
func TestLoadFileDiff_CarriesHunkBodies(t *testing.T) {
	if !gitAvailable() {
		t.Skip("git not on PATH")
	}
	dir, file := initTestRepo(t)
	if err := os.WriteFile(file, []byte("one\nTWO\nthree\nfour\n"), 0644); err != nil {
		t.Fatal(err)
	}
	hunks := loadFileDiff(dir, file)
	if len(hunks) != 1 || strings.Join(hunks[0].Body, "|") != "-two|+TWO" {
		t.Fatalf("hunks = %+v", hunks)
	}
}
