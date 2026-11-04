// =============================================================================
// File: internal/readme/readme_test.go
// Author: Rohan Allison <rohanthewiz@gmail.com>
// Created: 2026-11-03
// Copyright: 2026 Rohan Allison. All rights reserved.
// =============================================================================

package readme

import (
	"path/filepath"
	"reflect"
	"testing"
)

// sample is a small README with every construct the parser has an
// opinion about: nested headings, a heading and a table inside a fence,
// a pipe inside a code span, ASCII art with pipes, and a ragged row.
const sample = "# Top\n" +
	"\n" +
	"## Usage\n" +
	"\n" +
	"### Tool windows\n" +
	"\n" +
	"| Tool | Edge |\n" +
	"| --- | :-: |\n" +
	"| `Explorer` | left |\n" +
	"| `|W|` chip | right |\n" +
	"| ragged |\n" +
	"\n" +
	"```\n" +
	"## not a heading\n" +
	"| not | a |\n" +
	"| --- | - |\n" +
	"```\n" +
	"\n" +
	" ─ art │ x │\n" +
	"\n" +
	"#### Sub one\n" +
	"one\n" +
	"#### Sub two\n" +
	"##### deeper\n" +
	"two\n" +
	"### Next\n" +
	"tail\n"

// TestSection_EndsAtSameOrHigherLevel pins the section boundary: a
// section owns its deeper headings and stops at the next heading of its
// own level or higher — and a heading inside a fence is not one.
func TestSection_EndsAtSameOrHigherLevel(t *testing.T) {
	d := Parse(sample)
	s, ok := d.Section(3, "Tool windows")
	if !ok {
		t.Fatal("### Tool windows not found")
	}
	if s.Line != 5 {
		t.Errorf("Line = %d, want 5", s.Line)
	}
	subs := s.Subsections()
	var titles []string
	for _, sub := range subs {
		titles = append(titles, sub.Title)
	}
	if want := []string{"Sub one", "Sub two"}; !reflect.DeepEqual(titles, want) {
		t.Errorf("subsections = %q, want %q (the fenced ## and the ##### are not)", titles, want)
	}
	if _, ok := d.Section(2, "not a heading"); ok {
		t.Error("a heading inside a fence must not be found")
	}
	if _, ok := d.Section(3, "Missing"); ok {
		t.Error("a missing heading must report !ok")
	}
}

// TestTables_SkipsFencesAndArt pins what counts as a table: header +
// separator, outside a fence. The fenced example and the box-drawing art
// must not turn up as tables, and a ragged row reads as short, not as a
// crash.
func TestTables_SkipsFencesAndArt(t *testing.T) {
	s, _ := Parse(sample).Section(3, "Tool windows")
	tables := s.Tables()
	if len(tables) != 1 {
		t.Fatalf("got %d tables, want 1", len(tables))
	}
	tb := tables[0]
	if want := []string{"Tool", "Edge"}; !reflect.DeepEqual(tb.Header, want) {
		t.Errorf("header = %q, want %q", tb.Header, want)
	}
	if len(tb.Rows) != 3 {
		t.Fatalf("got %d rows, want 3", len(tb.Rows))
	}
	if got := tb.Rows[1].Cell(0); got != "`|W|` chip" {
		t.Errorf("code-span pipe split the cell: %q", got)
	}
	if got := tb.Rows[2].Cell(1); got != "" {
		t.Errorf("ragged row's missing cell = %q, want empty", got)
	}
	if tb.Rows[0].Line != 9 {
		t.Errorf("first row Line = %d, want 9", tb.Rows[0].Line)
	}
}

// TestCodeSpans_CommonMarkPadding pins span extraction: single and
// double backtick spans, the one-space padding rule that lets a span
// hold a backtick, and an unclosed run read as literal text.
func TestCodeSpans_CommonMarkPadding(t *testing.T) {
	cases := map[string][]string{
		"`a` / `b c`":    {"a", "b c"},
		"`` Esc ` ``":    {"Esc `"},
		"`` a`b ``":      {"a`b"},
		"`unclosed":      nil,
		"no spans":       nil,
		"`<name>…` here": {"<name>…"},
	}
	for in, want := range cases {
		if got := CodeSpans(in); !reflect.DeepEqual(got, want) {
			t.Errorf("CodeSpans(%q) = %q, want %q", in, got, want)
		}
	}
}

// TestSplitRow_EscapedPipe pins the \| escape: GitHub renders it as a
// literal pipe inside the cell, so it must not split one.
func TestSplitRow_EscapedPipe(t *testing.T) {
	got := SplitRow(`| a \| b | c |`)
	if want := []string{`a \| b`, "c"}; !reflect.DeepEqual(got, want) {
		t.Errorf("SplitRow = %q, want %q", got, want)
	}
	// No closing pipe: the trailing text is still a cell.
	if got := SplitRow("| a | b"); !reflect.DeepEqual(got, []string{"a", "b"}) {
		t.Errorf("open row = %q", got)
	}
}

// TestParse_UnclosedFenceRunsToEnd pins the renderer-compatible reading
// of a fence nobody closed: everything after it is example text.
func TestParse_UnclosedFenceRunsToEnd(t *testing.T) {
	d := Parse("## A\n```\n## B\n")
	if _, ok := d.Section(2, "B"); ok {
		t.Error("a heading after an unclosed fence must not be found")
	}
	// A ~~~ line does not close a ``` block.
	d = Parse("```\n~~~\n## C\n```\n## D\n")
	if _, ok := d.Section(2, "C"); ok {
		t.Error("~~~ closed a ``` fence")
	}
	if _, ok := d.Section(2, "D"); !ok {
		t.Error("heading after the real close not found")
	}
}

// TestLoad_MissingFile is the obvious failure: a path that isn't there
// is an error, not an empty document a pin would pass against.
func TestLoad_MissingFile(t *testing.T) {
	if _, err := Load(filepath.Join(t.TempDir(), "README.md")); err == nil {
		t.Error("Load of a missing file returned no error")
	}
}

// TestLoadRepo_FindsTheModuleREADME pins the walk-up: from this
// package's directory it must land on the repository's own README.
func TestLoadRepo_FindsTheModuleREADME(t *testing.T) {
	d, err := LoadRepo()
	if err != nil {
		t.Fatalf("LoadRepo: %v", err)
	}
	if _, ok := d.Section(1, "ced — Cats Editor"); !ok {
		t.Error("LoadRepo did not load ced's README")
	}
}

// TestLoadRepo_NoModule pins the failure: a directory with no go.mod
// above it is an error rather than a search that wanders off.
func TestLoadRepo_NoModule(t *testing.T) {
	// t.Chdir restores the working directory when the test ends.
	t.Chdir(t.TempDir())
	if _, err := LoadRepo(); err == nil {
		t.Error("LoadRepo with no go.mod above returned no error")
	}
}
