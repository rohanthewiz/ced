// =============================================================================
// File: internal/app/termdiag_test.go
// Author: Rohan Allison <rohanthewiz@gmail.com>
// Created: 2026-08-03
// Copyright: 2026 Rohan Allison. All rights reserved.
// =============================================================================

package app

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// termDiagTestApp opens the terminal panel over a project holding one
// real Go file, and seeds the scrollback with the given rows. Locations
// only exist when the file behind them does, so every test here needs a
// file on disk.
func termDiagTestApp(t *testing.T, rows ...termLine) (*App, string) {
	t.Helper()
	dir := t.TempDir()
	src := filepath.Join(dir, "main.go")
	if err := os.WriteFile(src, []byte("package main\n\nfunc main() {\n}\n"), 0644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	a := newTestApp(t, dir)
	f := openTestTerm(t, a)
	f.cwd = dir // grsh's cwd is what relative paths resolve against
	a.term.lines = rows
	return a, src
}

// TestTermDiagAt_ResolvesRelativeToShellCwd pins the happy path and the
// base it resolves against: `go build` prints paths relative to where it
// ran, and grsh's `cd` moves that — so the shell's cwd, not the project
// root, is what turns the printed path into a file.
func TestTermDiagAt_ResolvesRelativeToShellCwd(t *testing.T) {
	a, src := termDiagTestApp(t, termLine{text: "main.go:3:6: undefined: foo"})

	loc, ok := a.termDiagAt(0)
	if !ok {
		t.Fatal("a compiler line naming a real file should be a location")
	}
	if loc.path != src {
		t.Errorf("path = %q, want %q", loc.path, src)
	}
	if loc.line != 2 || loc.col != 5 {
		t.Errorf("target = %d:%d, want 0-based 2:5", loc.line, loc.col)
	}
	if loc.start != 0 || loc.width != len("main.go:3:6") {
		t.Errorf("underline span = %d+%d, want 0+%d", loc.start, loc.width, len("main.go:3:6"))
	}
}

// TestTermDiagAt_RejectsWhatIsNotThere pins the guard that makes this
// safe on output nobody owns: the permissive shared parser is happy with
// a bare "12:30", so the row must ALSO name a path, that path must
// resolve to a regular file, and the file must sit inside the project.
func TestTermDiagAt_RejectsWhatIsNotThere(t *testing.T) {
	a, _ := termDiagTestApp(t,
		termLine{text: "12:30: still building"},           // no path
		termLine{text: "nosuch.go:3:6: undefined: foo"},   // path doesn't exist
		termLine{text: "/etc/hosts:1:1: outside"},         // outside the root
		termLine{text: "go: downloading example.com/m"},   // prose
		termLine{text: ".:1:1: a directory is no target"}, // not a regular file
	)
	for idx := range a.term.lines {
		if _, ok := a.termDiagAt(idx); ok {
			t.Errorf("row %d (%q) must not be a location", idx, a.term.lines[idx].text)
		}
	}
}

// TestTermLocSpan pins the underline measurement, which is deliberately
// separate from the parse: the parse is lossy (a printed column of 0
// clamps), so reconstructing the text from parsed values would match
// nothing. Measuring the raw string can't disagree with itself.
func TestTermLocSpan(t *testing.T) {
	cases := []struct {
		in           string
		start, width int
		ok           bool
	}{
		{"main.go:3:6: undefined: foo", 0, len("main.go:3:6"), true},
		{"  indented.go:12: note", 2, len("indented.go:12"), true},
		// A colon that isn't a column separator must stay in the message.
		{"pkg/a.go:7:foo := bar", 0, len("pkg/a.go:7"), true},
		{"a printed column of zero:4:0: x", 0, len("a printed column of zero:4:0"), true},
		{"no colon here", 0, 0, false},
		{"label: not a location", 0, 0, false},
		{":5: empty path", 0, 0, false},
	}
	for _, tc := range cases {
		start, width, ok := termLocSpan(tc.in)
		if ok != tc.ok || (ok && (start != tc.start || width != tc.width)) {
			t.Errorf("termLocSpan(%q) = (%d,%d,%v), want (%d,%d,%v)",
				tc.in, start, width, ok, tc.start, tc.width, tc.ok)
		}
	}
}

// TestTermResolveDiagPath_CachesPerCwd pins the memo: drawing asks per
// visible row per frame, so the answer is cached — and keyed by cwd,
// because a relative path means a different file after a `cd`.
func TestTermResolveDiagPath_CachesPerCwd(t *testing.T) {
	a, src := termDiagTestApp(t)
	f := a.term.sess.(*fakeTermEval)

	if got := a.termResolveDiagPath("main.go"); got != src {
		t.Fatalf("resolve = %q, want %q", got, src)
	}
	if len(a.term.diagCache) != 1 {
		t.Errorf("cache holds %d entries, want 1", len(a.term.diagCache))
	}
	// A second ask is a map hit, not a second stat.
	if got := a.termResolveDiagPath("main.go"); got != src {
		t.Errorf("cached resolve = %q, want %q", got, src)
	}

	// Moving the shell must not serve the old answer for the same text.
	sub := filepath.Join(a.rootDir, "sub")
	if err := os.Mkdir(sub, 0755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	f.cwd = sub
	if got := a.termResolveDiagPath("main.go"); got != "" {
		t.Errorf("after cd, resolve = %q, want \"\" (no main.go in sub/)", got)
	}
}

// TestTermLocations_NewestCommandFirst pins the list's ordering, which
// is the whole design of it: plain document order buries the build you
// just ran, plain reverse order shows one build's errors backwards. The
// echoed command rows mark the boundaries, so blocks go newest-first
// while the rows inside one stay in printed order.
func TestTermLocations_NewestCommandFirst(t *testing.T) {
	a, _ := termDiagTestApp(t,
		termLine{text: "❯ go build ./...", kind: termCmd},
		termLine{text: "main.go:1:1: old first"},
		termLine{text: "main.go:2:1: old second"},
		termLine{text: "❯ go vet ./...", kind: termCmd},
		termLine{text: "main.go:3:1: new first"},
		termLine{text: "main.go:4:1: new second"},
	)

	locs := a.termLocations()
	if len(locs) != 4 {
		t.Fatalf("collected %d locations, want 4: %+v", len(locs), locs)
	}
	wantLines := []int{2, 3, 0, 1} // newest command's rows, in printed order
	for i, want := range wantLines {
		if locs[i].line != want {
			t.Errorf("location %d targets line %d, want %d", i, locs[i].line, want)
		}
	}
}

// TestTermJumpToRow_OpensAndClamps pins the jump: it opens the named
// file at the named position, and a line past today's EOF clamps rather
// than refusing — the output may be minutes old and the file edited
// since, so landing near beats not landing.
func TestTermJumpToRow_OpensAndClamps(t *testing.T) {
	a, src := termDiagTestApp(t,
		termLine{text: "main.go:3:6: undefined: foo"},
		termLine{text: "main.go:9000:1: history moved on"},
		termLine{text: "not a location at all"},
	)

	a.termJumpToRow(0)
	tab := a.activeTabPtr()
	if tab == nil || tab.Path != src {
		t.Fatalf("jump did not open %s", src)
	}
	if tab.Cursor.Line != 2 || tab.Cursor.Col != 5 {
		t.Errorf("cursor = %v, want line 2 col 5", tab.Cursor)
	}

	a.termJumpToRow(1)
	last := tab.Buffer.LineCount() - 1
	if got := a.activeTabPtr().Cursor.Line; got != last {
		t.Errorf("out-of-range line landed at %d, want the last line (%d)", got, last)
	}

	// A miss is a miss, not an error — the cursor simply stays put.
	before := a.activeTabPtr().Cursor
	a.termJumpToRow(2)
	if a.activeTabPtr().Cursor != before {
		t.Error("a non-location row must not move the cursor")
	}
}

// TestMenuTermLocations_PicksAndFlashes pins the keyboard twin: it opens
// the palette as a picker (the house rule), rows carry the compacted
// output line, and an empty scan says so rather than opening an empty
// box — the panel is mouse-driven, but macOS Terminal eats clicks, so
// this path has to work on its own.
func TestMenuTermLocations_PicksAndFlashes(t *testing.T) {
	a, src := termDiagTestApp(t,
		termLine{text: "  main.go:3:6: undefined: foo"},
	)

	a.menuTermLocations()
	m, ok := a.modal.(*paletteModal)
	if !ok {
		t.Fatalf("modal = %T, want the picker", a.modal)
	}
	if len(m.items) != 1 {
		t.Fatalf("items = %d, want 1", len(m.items))
	}
	if m.items[0].label != "main.go:3:6: undefined: foo" {
		t.Errorf("row = %q, want the compacted output line", m.items[0].label)
	}
	m.items[0].run(a)
	if tab := a.activeTabPtr(); tab == nil || tab.Path != src {
		t.Error("picking a row should open the file it names")
	}

	a.modal = nil
	a.term.lines = []termLine{{text: "nothing to see"}}
	a.term.diagCache = nil
	a.menuTermLocations()
	if a.modal != nil {
		t.Error("an empty scan must not open a picker")
	}
	if !strings.Contains(a.statusMsg, "No file locations") {
		t.Errorf("status = %q, want the empty-scan flash", a.statusMsg)
	}
}

// TestHasTermOutput pins the menu predicate's deliberate approximation:
// menuLayout runs every frame the menu is open, so the honest question
// ("does any row resolve?") — a scrollback walk with a stat behind it —
// is traded for a cheap gate plus an honest flash.
func TestHasTermOutput(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	if a.hasTermOutput() {
		t.Error("a terminal that has printed nothing has no locations")
	}
	a.term.lines = []termLine{{text: "anything"}}
	if !a.hasTermOutput() {
		t.Error("scrollback present should enable the row")
	}
}
