// =============================================================================
// File: internal/app/termdiag.go
// Author: Rohan Allison <rohanthewiz@gmail.com>
// Created: 2026-08-03
// Copyright: 2026 Rohan Allison. All rights reserved.
// =============================================================================

// termdiag.go makes the terminal panel's output clickable: any row that
// names a file and a line — a compiler error, a `go vet` finding, a
// `grep -n` hit — becomes a jump into the editor. It closes the
// build→fix loop inside one tmux pane, which is the whole reason the
// panel exists.
//
//	go build ./...
//	internal/app/lsp.go:314:22: undefined: foo     ← underlined, double-click
//	                                                  or ≡ / Esc ~ to pick
//
// House rules:
//
//   - ONE parser decides what a location is. `plugins.ParseDiagnostic`
//     already speaks the compiler/grep convention the decoration layer
//     is built on (internal/plugins/diag.go); a second implementation
//     here would drift, and the user would have no way to tell which
//     one decided a row wasn't a link.
//   - A LOCATION IS ONLY REAL IF THE FILE IS. The parser is deliberately
//     permissive — it has to be, since its usual caller already knows
//     which file the output is about — so "12:30" in some tool's
//     progress line parses fine. Terminal output belongs to nobody, so
//     the guard here is stricter: the row must name a PATH, that path
//     must resolve to a regular file, and that file must be inside the
//     project root (the confinement rule the git-log jump and the chat
//     filesystem both apply). Everything else is prose.
//   - Relative paths resolve against the SHELL's cwd, not the project
//     root: `go build` prints paths relative to where it ran, and grsh's
//     `cd` moves that. A user who cd'd away since the output was printed
//     gets rows that no longer resolve — inherent to a scrollback, and
//     the same thing a real terminal's file links do.
//   - Resolution is CACHED, because drawing asks per visible row per
//     frame. Uncached, every repaint would cost a stat syscall per row
//     of terminal output — fine locally, a storm over a slow filesystem.
//     The cache keys on the cwd too, so a `cd` invalidates it exactly
//     when the answers change.
//   - The keyboard twin is a PICKER (openPicker — the house rule for
//     every choose-one-from-a-list UI). Double-click is the primary
//     gesture, but macOS Terminal swallows clicks, so a mouse-only path
//     to a feature is no path at all.

package app

import (
	"os"
	"path/filepath"

	"github.com/rohanthewiz/ced/internal/plugins"
)

const (
	// termLocationsMax caps the picker. A scrollback holds 5000 rows and
	// a noisy build can make most of them locations; a picker that long
	// is slower to scan than scrolling the panel. The list is collected
	// newest-command-first, so the cap drops the OLDEST — and says so in
	// the title, because a silently short list reads as "that's all of
	// them" (the project-search rule).
	termLocationsMax = 200

	// termDiagCacheMax bounds the path-resolution cache. Reached only by
	// a session that has cd'd through many directories or printed many
	// distinct paths; dropping the whole map costs one stat per row on
	// the next repaint, which is cheaper than tracking eviction order.
	termDiagCacheMax = 512
)

// termLocation is one jumpable reference found in the scrollback.
// start/width describe the "path:line[:col]" text WITHIN the row, in
// runes, so the renderer can underline exactly the part that is a link.
type termLocation struct {
	row   int    // scrollback row index
	path  string // absolute, verified to exist inside the project root
	line  int    // zero-based
	col   int    // zero-based; -1 when the tool printed no column
	start int    // rune offset of the location text in the row
	width int    // rune length of the location text
	text  string // the whole row, for the picker label
}

// termDiagAt reports the jumpable location on scrollback row idx, if
// any. The parser decides the SHAPE; termLocSpan measures the same
// prefix for the underline, and the filesystem has the last word on
// whether the row is a link at all.
func (a *App) termDiagAt(idx int) (termLocation, bool) {
	ln, ok := a.termRowLine(idx)
	if !ok {
		return termLocation{}, false
	}
	d, ok := plugins.ParseDiagnostic(ln.text)
	if !ok || d.Path == "" {
		return termLocation{}, false
	}
	start, width, ok := termLocSpan(ln.text)
	if !ok {
		return termLocation{}, false
	}
	abs := a.termResolveDiagPath(d.Path)
	if abs == "" {
		return termLocation{}, false
	}
	return termLocation{
		row: idx, path: abs, line: d.Line, col: d.Col,
		start: start, width: width, text: ln.text,
	}, true
}

// termLocSpan measures the leading "path:line[:col]" run of a row and
// returns its rune offset and length.
//
// It exists rather than reconstructing the text from the parsed values
// because the parse is LOSSY on purpose: a printed column of 0 clamps to
// zero-based 0, so re-rendering it would produce ":1" and match nothing.
// Measuring the raw text can't disagree with itself. It is not a second
// parser — it never decides whether a row is a location, only how much
// of it to underline once ParseDiagnostic has said that it is.
func termLocSpan(raw string) (start, width int, ok bool) {
	r := []rune(raw)
	i := 0
	for i < len(r) && (r[i] == ' ' || r[i] == '\t') {
		i++
	}
	start = i
	for i < len(r) && r[i] != ':' {
		i++
	}
	if i >= len(r) || i == start {
		return 0, 0, false // no colon, or an empty path field
	}
	i++ // step past the path's colon
	digits := i
	for i < len(r) && r[i] >= '0' && r[i] <= '9' {
		i++
	}
	if i == digits {
		return 0, 0, false // "foo: bar" — a label, not a location
	}
	end := i
	// Optional ":col", claimed only when digits actually follow — the
	// same claim-only-if-it-matches rule the parser uses, so `12:foo`
	// doesn't swallow the message.
	if i < len(r) && r[i] == ':' {
		j := i + 1
		for j < len(r) && r[j] >= '0' && r[j] <= '9' {
			j++
		}
		if j > i+1 {
			end = j
		}
	}
	return start, end - start, true
}

// termResolveDiagPath turns a printed path into an absolute one the
// editor will open, or "" when it names nothing openable. See the file
// header for why the base is the shell's cwd and why the answer is
// cached.
func (a *App) termResolveDiagPath(raw string) string {
	if raw == "" {
		return ""
	}
	base := a.rootDir
	if a.term.sess != nil {
		if cwd := a.term.sess.Cwd(); cwd != "" {
			base = cwd
		}
	}
	key := base + "\x00" + raw
	if abs, hit := a.term.diagCache[key]; hit {
		return abs
	}
	abs := raw
	if !filepath.IsAbs(abs) {
		abs = filepath.Join(base, abs)
	}
	resolved := ""
	// A directory is not a jump target, and a path outside the tree is
	// somebody else's file — a stdlib frame in a stack trace, say.
	if fi, err := os.Stat(abs); err == nil && fi.Mode().IsRegular() && pathInside(abs, a.rootDir) {
		resolved = abs
	}
	if a.term.diagCache == nil || len(a.term.diagCache) >= termDiagCacheMax {
		a.term.diagCache = map[string]string{}
	}
	a.term.diagCache[key] = resolved
	return resolved
}

// termLocations collects every jumpable location in the scrollback,
// NEWEST COMMAND FIRST but in printed order within each command's
// output.
//
// That ordering is the whole design of this list. Plain document order
// buries the build you just ran under every build before it; plain
// reverse order puts a single build's three errors on screen backwards.
// The echoed command rows (kind termCmd) already mark where one
// command's output ends and the next begins, so grouping by them costs
// nothing and gets both halves right.
func (a *App) termLocations() []termLocation {
	var blocks [][]termLocation
	var cur []termLocation
	for idx := range a.termContentRows() {
		if ln, ok := a.termRowLine(idx); ok && ln.kind == termCmd {
			blocks = append(blocks, cur)
			cur = nil
		}
		if loc, ok := a.termDiagAt(idx); ok {
			cur = append(cur, loc)
		}
	}
	blocks = append(blocks, cur)

	var out []termLocation
	for i := len(blocks) - 1; i >= 0; i-- {
		out = append(out, blocks[i]...)
		if len(out) >= termLocationsMax {
			return out[:termLocationsMax]
		}
	}
	return out
}

// termJumpToRow opens the file named on scrollback row idx at the line
// (and column) it reported. Rows that aren't locations do nothing —
// a double-click on ordinary output is not an error, it's a miss.
func (a *App) termJumpToRow(idx int) {
	loc, ok := a.termDiagAt(idx)
	if !ok {
		return
	}
	a.termJumpTo(loc)
}

// termJumpTo opens one collected location. Best-effort on the line
// number, like the git-log jump: the output may be minutes old and the
// file edited since, so a line past today's EOF clamps rather than
// refusing — landing near beats not landing.
func (a *App) termJumpTo(loc termLocation) {
	a.openFile(loc.path)
	t := a.activeTabPtr()
	if t == nil || t.Path != loc.path || t.Buffer == nil {
		return // openFile failed and flashed its own reason
	}
	col := loc.col
	if col < 0 {
		col = 0
	}
	a.goToLine(loc.line, col)
}

// hasTermOutput is the menu predicate for the locations picker.
// Deliberately approximate: menuLayout runs on every frame the menu is
// open, and the honest predicate — "does any row resolve to a real
// file?" — is a scrollback walk with a stat behind it. A cheap gate
// plus an honest flash when the scan finds nothing is the right trade
// (the same shape hasGitPanelOpen takes).
func (a *App) hasTermOutput() bool {
	return len(a.term.lines) > 0
}

// menuTermLocations is the keyboard twin of double-clicking a row: every
// location in the scrollback, offered as a fuzzy picker.
func (a *App) menuTermLocations() {
	a.closeMenu()
	locs := a.termLocations()
	if len(locs) == 0 {
		a.flash("No file locations in the terminal output")
		return
	}
	items := make([]paletteItem, 0, len(locs))
	for _, loc := range locs {
		items = append(items, paletteItem{
			label: termLocationLabel(loc),
			run:   func(app *App) { app.termJumpTo(loc) },
		})
	}
	title := "Terminal output locations"
	if len(locs) == termLocationsMax {
		title += " (newest " + itoa(termLocationsMax) + ")"
	}
	a.openPicker(title, items)
}

// termLocationLabel renders one picker row: the output line itself,
// compacted the way the Find-all list compacts a code line (leading
// indentation off, interior tabs to one space). The row already names
// its file and line, so nothing needs to be prepended — and reusing
// compactLine keeps the two list surfaces reading alike.
func termLocationLabel(loc termLocation) string {
	label, _ := compactLine(loc.text)
	return label
}
