// =============================================================================
// File: internal/readme/readme.go
// Author: Rohan Allison <rohanthewiz@gmail.com>
// Created: 2026-11-03
// Copyright: 2026 Rohan Allison. All rights reserved.
// =============================================================================

// Package readme reads the repository's README.md as structure — headed
// sections, pipe tables, code spans — so tests elsewhere can pin the
// documentation to the code it describes.
//
// Why a package, when nothing in the editor imports it: README.md is the
// project's only documentation, and it went stale every time a feature
// landed without its paragraph. The cure is tests that fail when a ≡ row,
// a config.json key or a tool window exists in code but not in a README
// table (and the reverse, when a table names something that is gone).
// Those tests live beside the code they pin — internal/app for the menu
// and the tool windows, internal/userconfig for the config keys — and
// they must agree on what "a README table row" means. One parser here is
// what keeps a Markdown quirk (a `|` inside a code span, a heading inside
// a fenced example) from being handled in one test and not the other.
//
// Deliberately NOT a Markdown library. It understands exactly the subset
// the README's reference tables use, and a construct outside that subset
// is read the simple way rather than rejected:
//
//	# … ######         ATX headings (setext headings are not used)
//	``` / ~~~ fences   their contents are opaque — no headings, no tables
//	| a | b |          a table row; a row of only -, :, | and spaces is
//	                   the separator; the row above it is the header
//	`x`, `` x ``       code spans (CommonMark: a run of N backticks closes
//	                   on the next run of exactly N; one padding space is
//	                   stripped from each end when both are present)
package readme

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
)

// Doc is a README split into lines with fenced code blocks marked, so
// every later question (where does a section end? is this a table row?)
// can skip example text without re-scanning for fences.
type Doc struct {
	lines []string
	// fenced[i] is true when lines[i] is inside a fenced code block or
	// is one of its fence lines. Kept parallel to lines rather than
	// stripping fenced text out, so line numbers in error messages stay
	// the README's own.
	fenced []bool
}

// Parse splits text into a Doc. It never fails: an unclosed fence runs
// to the end of the document, which is how renderers treat it too.
func Parse(text string) *Doc {
	lines := strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n")
	d := &Doc{lines: lines, fenced: make([]bool, len(lines))}
	// fence is the opening marker while inside a block ("```" or "~~~"
	// repeated). A block closes only on a line that starts with the SAME
	// character repeated at least as many times — CommonMark's rule, and
	// the reason a ``` example containing a ~~~ line stays one block.
	fence := ""
	for i, line := range lines {
		trimmed := strings.TrimLeft(line, " ")
		if fence == "" {
			if m := fenceMarker(trimmed); m != "" {
				fence = m
				d.fenced[i] = true
			}
			continue
		}
		d.fenced[i] = true
		if m := fenceMarker(trimmed); m != "" && m[0] == fence[0] && len(m) >= len(fence) &&
			strings.TrimSpace(trimmed[len(m):]) == "" {
			fence = ""
		}
	}
	return d
}

// fenceMarker returns the run of three or more backticks or tildes that
// opens s, or "" when s does not start a fence.
func fenceMarker(s string) string {
	if s == "" || (s[0] != '`' && s[0] != '~') {
		return ""
	}
	n := 0
	for n < len(s) && s[n] == s[0] {
		n++
	}
	if n < 3 {
		return ""
	}
	return s[:n]
}

// Load reads and parses the README at path.
func Load(path string) (*Doc, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return Parse(string(raw)), nil
}

// LoadRepo finds the module root by walking up from the working
// directory to the first go.mod, then loads README.md beside it. Tests
// run with the package directory as cwd, so this spares every caller a
// "../../" count that breaks the day a package moves.
func LoadRepo() (*Doc, error) {
	dir, err := os.Getwd()
	if err != nil {
		return nil, err
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return Load(filepath.Join(dir, "README.md"))
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return nil, errors.New("readme: no go.mod above the working directory")
		}
		dir = parent
	}
}

// Section is a run of README lines under one heading, up to (not
// including) the next heading of the same or a higher level.
type Section struct {
	// Title is the heading text without its #'s, e.g. "Tool windows".
	Title string
	// Level is the number of #'s (1–6).
	Level int
	// Line is the heading's 1-based line number in the README.
	Line int
	// start/end bound the body: lines[start:end] in the owning Doc.
	start, end int
	doc        *Doc
}

// heading reports the level and text of an ATX heading at line i, or
// level 0 when the line is not one (or sits inside a fence).
func (d *Doc) heading(i int) (int, string) {
	if d.fenced[i] {
		return 0, ""
	}
	line := d.lines[i]
	n := 0
	for n < len(line) && n < 7 && line[n] == '#' {
		n++
	}
	// "#tag" is not a heading; "#" alone is (an empty one).
	if n == 0 || n > 6 || (n < len(line) && line[n] != ' ') {
		return 0, ""
	}
	return n, strings.TrimSpace(strings.TrimRight(strings.TrimSpace(line[n:]), "#"))
}

// Section returns the section headed by exactly title at the given
// level (3 for "### title"); ok is false when the README has no such
// heading. The first match wins — README headings are unique where a
// test cares.
func (d *Doc) Section(level int, title string) (Section, bool) {
	for i := range d.lines {
		if l, text := d.heading(i); l == level && text == title {
			return d.sectionAt(i, l, text), true
		}
	}
	return Section{}, false
}

// sectionAt builds the Section whose heading is line i.
func (d *Doc) sectionAt(i, level int, title string) Section {
	end := len(d.lines)
	for j := i + 1; j < len(d.lines); j++ {
		if l, _ := d.heading(j); l != 0 && l <= level {
			end = j
			break
		}
	}
	return Section{Title: title, Level: level, Line: i + 1, start: i + 1, end: end, doc: d}
}

// Subsections returns the sections one level deeper than s, in order.
// Text between s's heading and its first subsection is not part of any.
// Only the NEXT level counts: a "#####" under a "####" stays inside it.
func (s Section) Subsections() []Section {
	var out []Section
	for i := s.start; i < s.end; i++ {
		if l, text := s.doc.heading(i); l == s.Level+1 {
			sub := s.doc.sectionAt(i, l, text)
			// A subsection never runs past its parent.
			if sub.end > s.end {
				sub.end = s.end
			}
			out = append(out, sub)
		}
	}
	return out
}

// Row is one body row of a pipe table.
type Row struct {
	// Cells are the trimmed cell texts, outer pipes removed.
	Cells []string
	// Line is the row's 1-based line number in the README.
	Line int
}

// Cell returns cell i, or "" when the row is shorter — a ragged row is
// a rendering quirk, not something a pin should crash on.
func (r Row) Cell(i int) string {
	if i < 0 || i >= len(r.Cells) {
		return ""
	}
	return r.Cells[i]
}

// Table is a pipe table: the header row's cells and the body rows.
type Table struct {
	Header []string
	Rows   []Row
	// Line is the header row's 1-based line number.
	Line int
}

// Tables returns every pipe table in the section, nested subsections
// included, in order. A table is a header row followed directly by a
// separator row; body rows run until the first line that is not a
// table row. Pipe lines with no separator under their first line (ASCII
// art, a stray "|") are not tables.
func (s Section) Tables() []Table {
	var out []Table
	d := s.doc
	for i := s.start; i < s.end; i++ {
		if d.fenced[i] || !isTableLine(d.lines[i]) {
			continue
		}
		if i+1 >= s.end || d.fenced[i+1] || !isSeparator(d.lines[i+1]) {
			continue
		}
		t := Table{Header: SplitRow(d.lines[i]), Line: i + 1}
		j := i + 2
		for ; j < s.end && !d.fenced[j] && isTableLine(d.lines[j]); j++ {
			t.Rows = append(t.Rows, Row{Cells: SplitRow(d.lines[j]), Line: j + 1})
		}
		out = append(out, t)
		i = j - 1
	}
	return out
}

// isTableLine reports whether line is written as a table row: it starts
// with a pipe after optional indentation. The README always writes the
// leading pipe, and requiring it keeps prose with a "|" in it out.
func isTableLine(line string) bool {
	return strings.HasPrefix(strings.TrimSpace(line), "|")
}

// isSeparator reports whether line is a table's |---|:--:| row.
func isSeparator(line string) bool {
	t := strings.TrimSpace(line)
	if !strings.HasPrefix(t, "|") || !strings.Contains(t, "-") {
		return false
	}
	return strings.Trim(t, "|-: ") == ""
}

// SplitRow splits a table line into trimmed cells. A pipe inside a code
// span or escaped as \| belongs to the cell (GitHub renders both that
// way), so the README can document a `|W|` chip in a table.
func SplitRow(line string) []string {
	t := strings.TrimSpace(line)
	t = strings.TrimPrefix(t, "|")
	var cells []string
	var cur strings.Builder
	for i := 0; i < len(t); {
		c := t[i]
		switch {
		case c == '\\' && i+1 < len(t) && t[i+1] == '|':
			cur.WriteString(`\|`)
			i += 2
		case c == '`':
			// Copy a whole code span verbatim. An unclosed run of
			// backticks is literal text, the CommonMark reading.
			n := runLen(t, i, '`')
			if end := closingRun(t, i+n, n); end >= 0 {
				cur.WriteString(t[i : end+n])
				i = end + n
			} else {
				cur.WriteString(t[i : i+n])
				i += n
			}
		case c == '|':
			cells = append(cells, strings.TrimSpace(cur.String()))
			cur.Reset()
			i++
		default:
			cur.WriteByte(c)
			i++
		}
	}
	// Text after the last pipe is a cell only when the row omitted its
	// closing pipe; the usual trailing "|" leaves nothing behind.
	if rest := strings.TrimSpace(cur.String()); rest != "" {
		cells = append(cells, rest)
	}
	return cells
}

// runLen counts the run of c starting at s[i].
func runLen(s string, i int, c byte) int {
	n := 0
	for i+n < len(s) && s[i+n] == c {
		n++
	}
	return n
}

// closingRun finds the next run of exactly n backticks at or after from,
// returning its start or -1. A longer or shorter run is part of the
// span's content (that is how a span holds a backtick).
func closingRun(s string, from, n int) int {
	for i := from; i < len(s); {
		if s[i] != '`' {
			i++
			continue
		}
		m := runLen(s, i, '`')
		if m == n {
			return i
		}
		i += m
	}
	return -1
}

// CodeSpans returns the content of every code span in s, in order. One
// space of padding is stripped from each end when BOTH ends have it and
// the content is not all spaces — the CommonMark rule that lets a span
// hold a backtick, as the README's hotkey table does for the terminal key:
//
//	`` Esc ` ``   →   Esc `
func CodeSpans(s string) []string {
	var out []string
	for i := 0; i < len(s); {
		if s[i] != '`' {
			i++
			continue
		}
		n := runLen(s, i, '`')
		end := closingRun(s, i+n, n)
		if end < 0 {
			i += n
			continue
		}
		body := s[i+n : end]
		if len(body) >= 2 && body[0] == ' ' && body[len(body)-1] == ' ' && strings.TrimSpace(body) != "" {
			body = body[1 : len(body)-1]
		}
		out = append(out, body)
		i = end + n
	}
	return out
}
