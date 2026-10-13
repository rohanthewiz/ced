// =============================================================================
// File: internal/app/cherrypickdiff_test.go
// Author: Rohan Allison <rohanthewiz@gmail.com>
// Created: 2026-10-13
// Copyright: 2026 Rohan Allison. All rights reserved.
// =============================================================================

package app

import (
	"strings"
	"testing"

	"github.com/gdamore/tcell/v2"
)

// diffTestModal builds a dialog over n synthetic commits h0…h(n-1), with
// h0's diff already cached as lines, opened in a.
func diffTestModal(a *App, n int, lines []string) *cherryPickModal {
	m := &cherryPickModal{target: "main", ticked: map[string]bool{}, lastClickRow: -1}
	for i := 0; i < n; i++ {
		h := "h" + itoa(i)
		m.commits = append(m.commits, cherryPickCommit{hash: h, short: h, subject: "commit " + h})
	}
	m.diff.cache = map[string][]string{"h0": lines}
	a.openModal(m)
	return m
}

// numberedLines returns n lines "+line 1"…"+line n".
func numberedLines(n int) []string {
	out := make([]string, n)
	for i := range out {
		out[i] = "+line " + itoa(i+1)
	}
	return out
}

// TestCherryPickSplitRows pins the list/pane split: the list gets what it
// needs up to a third (floor three), and the pane always keeps two rows.
func TestCherryPickSplitRows(t *testing.T) {
	cases := []struct{ avail, n, want int }{
		{30, 2, 2},   // a short list takes only its rows
		{30, 50, 10}, // a long one stops at a third
		{6, 50, 3},   // the floor of three while it fits
		{4, 50, 2},   // the pane keeps header + one row
		{1, 5, 1},    // never below one row
	}
	for _, c := range cases {
		if got := cherryPickSplitRows(c.avail, c.n); got != c.want {
			t.Errorf("split(%d, %d) = %d, want %d", c.avail, c.n, got, c.want)
		}
	}
}

// TestCapCherryPickDiff pins the cap: a short diff is untouched, a long
// one keeps max lines and ends with a line counting the cut.
func TestCapCherryPickDiff(t *testing.T) {
	if got := capCherryPickDiff(numberedLines(3), 5); len(got) != 3 {
		t.Errorf("short diff changed: %v", got)
	}
	got := capCherryPickDiff(numberedLines(10), 4)
	if len(got) != 5 || got[3] != "+line 4" || !strings.Contains(got[4], "6 more lines not shown") {
		t.Errorf("capped = %v", got)
	}
}

// TestCherryPickDiff_ToggleSplitsTheDialog pins the toggle: alt+d opens
// the pane, the dialog grows to the window, the list keeps its share with
// the detail row right under it, the toggle is drawn on that row, and a
// click on it closes the pane again.
func TestCherryPickDiff_ToggleSplitsTheDialog(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	m := diffTestModal(a, 30, numberedLines(5))
	_, _, _, closedH := m.rect(a)

	m.handleKey(a, tcell.NewEventKey(tcell.KeyRune, 'd', tcell.ModAlt))
	if !m.diff.open || m.filter != "" {
		t.Fatalf("alt+d: open=%v filter=%q", m.diff.open, m.filter)
	}
	mx, _, mw, h := m.rect(a)
	if h != a.height-2 || h <= closedH {
		t.Errorf("open height = %d (closed %d, window %d)", h, closedH, a.height)
	}
	_, _, listTop, dy, _, _, _ := m.rowY(a)
	if rows := m.listRows(a); rows != cherryPickSplitRows(h-cherryPickFixedRows, 30) || dy != listTop+rows {
		t.Errorf("list rows = %d, detail row %d, list top %d", rows, dy, listTop)
	}
	hy, top, n := m.diffPaneRows(a)
	if hy != dy+1 || top != dy+2 || n < 5 {
		t.Errorf("pane rows header=%d top=%d n=%d (detail %d)", hy, top, n, dy)
	}

	a.draw()
	if row := screenRow(t, a, dy, mx, mw); !strings.Contains(row, "▾ diff alt+d") {
		t.Errorf("detail row = %q", row)
	}
	tg := m.diffToggleRect(a)
	m.handleMouse(a, tg.x+1, tg.y, tcell.Button1)
	if m.diff.open {
		t.Error("a click on the toggle did not close the pane")
	}
}

// TestCherryPickDiff_DrawsAndScrolls pins the pane's body: the cached diff
// is drawn with its position in the header, Alt and Shift arrows scroll it
// (bare arrows still walk the list), the wheel over the pane scrolls the
// diff and not the list, and moving to another commit restarts at the top.
func TestCherryPickDiff_DrawsAndScrolls(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	m := diffTestModal(a, 30, numberedLines(200))
	m.toggleDiff(a)
	a.draw()
	mx, _, mw, _ := m.rect(a)
	hy, top, n := m.diffPaneRows(a)
	if row := screenRow(t, a, top, mx, mw); !strings.Contains(row, "+line 1") {
		t.Errorf("first body row = %q", row)
	}
	if hdr := screenRow(t, a, hy, mx, mw); !strings.Contains(hdr, "h0") ||
		!strings.Contains(hdr, "1–"+itoa(n)+" of 200") || !strings.Contains(hdr, "alt+↑↓") {
		t.Errorf("header = %q", hdr)
	}

	m.handleKey(a, tcell.NewEventKey(tcell.KeyDown, 0, tcell.ModAlt))
	m.handleKey(a, tcell.NewEventKey(tcell.KeyDown, 0, tcell.ModShift))
	if m.diff.scroll != 2 || m.cursor != 0 {
		t.Errorf("modified Down: scroll=%d cursor=%d, want 2 / 0", m.diff.scroll, m.cursor)
	}
	m.handleKey(a, tcell.NewEventKey(tcell.KeyEnd, 0, tcell.ModAlt))
	if m.diff.scroll != 200-n {
		t.Errorf("alt+End scroll = %d, want %d", m.diff.scroll, 200-n)
	}
	listScroll := m.scroll
	m.handleMouse(a, mx+5, top+1, tcell.WheelUp)
	if m.diff.scroll != 200-n-wheelLines || m.scroll != listScroll {
		t.Errorf("wheel over the pane: diff scroll=%d list scroll=%d", m.diff.scroll, m.scroll)
	}

	m.handleKey(a, keyEvent(tcell.KeyDown, 0))
	if m.cursor != 1 {
		t.Fatalf("bare Down did not walk the list: cursor=%d", m.cursor)
	}
	m.diff.cache["h1"] = numberedLines(3)
	a.draw()
	if m.diff.scroll != 0 || m.diff.hash != "h1" {
		t.Errorf("another commit kept scroll %d (hash %q)", m.diff.scroll, m.diff.hash)
	}
}

// TestCherryPickDiff_LoadsRealCommits pins the load against real git: the
// pane asks for the highlighted commit when drawn, the answer lands in the
// cache, and a merge's diff is its first-parent diff — the side's file,
// not the plain commits around it.
func TestCherryPickDiff_LoadsRealCommits(t *testing.T) {
	repo := cherryPickMergeRepo(t)
	a := newTestApp(t, repo)
	a.rootDir = repo
	a.refreshGitStatus()
	m := openLoadedCherryPick(t, a, "topic")
	m.toggleDiff(a)
	_, first := cherryPickBySubject(t, m.commits, "c2 plain")
	a.draw()
	if !m.diff.pending[first.hash] {
		t.Fatal("drawing the pane did not ask for the diff")
	}
	pumpAppEvents(t, a, func() bool { _, ok := m.diff.cache[first.hash]; return ok })
	if got := strings.Join(m.diff.cache[first.hash], "\n"); !strings.Contains(got, "c2 plain") || !strings.Contains(got, "+c2") {
		t.Errorf("c2 diff = %q", got)
	}
	if m.diff.pending[first.hash] {
		t.Error("pending not cleared")
	}

	_, merge := cherryPickBySubject(t, m.commits, "merge side")
	got := strings.Join(loadCherryPickDiff(repo, merge.hash), "\n")
	if !strings.Contains(got, "+++ b/s.txt") || strings.Contains(got, "c1.txt") {
		t.Errorf("merge diff = %q, want s.txt against the first parent only", got)
	}
	if got := loadCherryPickDiff(repo, "0000000000000000000000000000000000000000"); !strings.HasPrefix(got[0], "(can't show") {
		t.Errorf("bad hash = %v", got)
	}
}

// TestHandleCherryPickDiff_LandsInItsOwnDialog pins the addressing: a diff
// arriving while another modal holds the slot (the branch picker) still
// fills the dialog that asked, ready for when it comes back.
func TestHandleCherryPickDiff_LandsInItsOwnDialog(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	m := &cherryPickModal{ticked: map[string]bool{}}
	m.diff.pending = map[string]bool{"h": true}
	a.openPicker("other", []paletteItem{{label: "x", run: func(*App) {}}})
	a.handleCherryPickDiff(&cherryPickDiffEvent{m: m, hash: "h", lines: []string{"+x"}})
	if got := m.diff.cache["h"]; len(got) != 1 || m.diff.pending["h"] {
		t.Errorf("cache=%v pending=%v", m.diff.cache, m.diff.pending)
	}
}
