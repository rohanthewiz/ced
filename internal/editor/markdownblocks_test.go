// =============================================================================
// File: internal/editor/markdownblocks_test.go
// Author: Rohan Allison
// Created: 2026-09-09
// Copyright: 2026 Cloudmanic, LLC. All rights reserved.
// =============================================================================

package editor

import (
	"strings"
	"testing"

	"github.com/rohanthewiz/ced/internal/theme"
)

// renderMD is the fixture entry point: markdown source (as one string,
// for readability) laid out at a width, returned as the plain text of
// each display row.
func renderMD(src string, width int) []string {
	rows := RenderMarkdown(strings.Split(src, "\n"), width, theme.Default())
	out := make([]string, len(rows))
	for i, r := range rows {
		out[i] = strings.TrimRight(string(r.Runes), " ")
	}
	return out
}

// renderRows is renderMD when a test needs the Src line numbers too.
func renderRows(src string, width int) []MDRow {
	return RenderMarkdown(strings.Split(src, "\n"), width, theme.Default())
}

// hasRow reports whether any rendered row equals want exactly.
func hasRow(rows []string, want string) bool {
	for _, r := range rows {
		if r == want {
			return true
		}
	}
	return false
}

// TestRenderMarkdown_HeadingLosesItsHashes pins the basic transformation
// and the rule beneath the top two levels — the hierarchy cue that
// survives a terminal profile rendering neither bold nor color.
func TestRenderMarkdown_HeadingLosesItsHashes(t *testing.T) {
	rows := renderMD("# Title\n\nbody\n\n## Section\n\nmore", 40)
	if !hasRow(rows, "Title") {
		t.Errorf("no bare 'Title' row: %q", rows)
	}
	if !hasRow(rows, strings.Repeat("━", 40)) {
		t.Errorf("h1 lost its rule: %q", rows)
	}
	if !hasRow(rows, strings.Repeat("─", 40)) {
		t.Errorf("h2 lost its rule: %q", rows)
	}
	for _, r := range rows {
		if strings.Contains(r, "#") {
			t.Errorf("a hash survived into the output: %q", r)
		}
	}
}

// TestRenderMarkdown_HashtagIsNotAHeading pins the space requirement.
// Without it every "#tag" in a document becomes an H1 with a rule under
// it, which is the loudest possible way to be wrong.
func TestRenderMarkdown_HashtagIsNotAHeading(t *testing.T) {
	rows := renderMD("filed under #urgent today", 40)
	if !hasRow(rows, "filed under #urgent today") {
		t.Errorf("#urgent was treated as a heading: %q", rows)
	}
}

// TestRenderMarkdown_SetextHeading pins the underlined form, including
// the ambiguity it turns on: "---" under a paragraph is a heading, the
// same "---" after a blank is a thematic break.
func TestRenderMarkdown_SetextHeading(t *testing.T) {
	rows := renderMD("Title\n=====\n\nbody", 20)
	if !hasRow(rows, "Title") || !hasRow(rows, strings.Repeat("━", 20)) {
		t.Errorf("setext h1 did not render as a heading: %q", rows)
	}

	rows = renderMD("body\n\n---\n\nmore", 20)
	if hasRow(rows, strings.Repeat("━", 20)) {
		t.Errorf("a standalone --- was read as a setext heading: %q", rows)
	}
	if !hasRow(rows, strings.Repeat("─", 20)) {
		t.Errorf("no thematic break: %q", rows)
	}
}

// TestRenderMarkdown_ParagraphWraps pins that prose is word-wrapped to
// the width and joined across source lines the way markdown defines.
func TestRenderMarkdown_ParagraphWraps(t *testing.T) {
	rows := renderMD("one two three\nfour five six seven", 12)
	for _, r := range rows {
		if len([]rune(r)) > 12 {
			t.Errorf("row wider than the window: %q (%d)", r, len([]rune(r)))
		}
		if strings.HasPrefix(r, " ") {
			t.Errorf("a wrap boundary carried its space onto the next row: %q", r)
		}
	}
	joined := strings.Join(rows, " ")
	if !strings.Contains(joined, "three four") {
		t.Errorf("the two source lines were not joined into one paragraph: %q", rows)
	}
}

// TestRenderMarkdown_LongWordHardBreaks pins the escape hatch: a URL
// longer than the pane must break rather than run off the edge.
func TestRenderMarkdown_LongWordHardBreaks(t *testing.T) {
	rows := renderMD(strings.Repeat("x", 50), 20)
	if len(rows) < 3 {
		t.Fatalf("a 50-rune word did not break across rows: %q", rows)
	}
	for _, r := range rows {
		if len([]rune(r)) > 20 {
			t.Errorf("row overflowed the window: %q", r)
		}
	}
}

// TestRenderMarkdown_FencedCodeKeepsItsColumns pins the hard-wrap half
// of the wrapping rule and that the fence markers themselves are gone.
// The leading space is the block's own margin.
func TestRenderMarkdown_FencedCodeKeepsItsColumns(t *testing.T) {
	rows := renderMD("```go\nif x {\n    y()\n}\n```", 40)
	if !hasRow(rows, " if x {") || !hasRow(rows, "     y()") {
		t.Errorf("code lost its indentation or its content: %q", rows)
	}
	for _, r := range rows {
		if strings.Contains(r, "```") {
			t.Errorf("a fence marker survived: %q", r)
		}
	}
	if !hasRow(rows, "go") {
		t.Errorf("the language chip is missing: %q", rows)
	}
}

// TestRenderMarkdown_UnterminatedFenceDoesNotEatNothing pins the
// degradation: a half-typed fence renders the rest of the file as code
// rather than dropping it, which is what a reader watching a document
// they are editing needs.
func TestRenderMarkdown_UnterminatedFenceDoesNotEatNothing(t *testing.T) {
	rows := renderMD("```\nstill here\nand here", 40)
	if !hasRow(rows, " still here") || !hasRow(rows, " and here") {
		t.Errorf("content after an unterminated fence was lost: %q", rows)
	}
}

// TestRenderMarkdown_ListsGetBulletsAndNesting pins the marker
// substitution and that indent becomes depth.
func TestRenderMarkdown_ListsGetBulletsAndNesting(t *testing.T) {
	rows := renderMD("- one\n- two\n  - nested\n\n1. first\n2. second", 40)
	if !hasRow(rows, "• one") || !hasRow(rows, "• two") {
		t.Errorf("bullets missing: %q", rows)
	}
	if !hasRow(rows, "  ◦ nested") {
		t.Errorf("nested item did not indent or change glyph: %q", rows)
	}
	if !hasRow(rows, "1. first") || !hasRow(rows, "2. second") {
		t.Errorf("ordered markers lost their numbers: %q", rows)
	}
}

// TestRenderMarkdown_TaskBoxes pins the checkbox substitution, and that
// both states are the same width so a mixed list stays aligned.
func TestRenderMarkdown_TaskBoxes(t *testing.T) {
	rows := renderMD("- [ ] todo\n- [x] done", 40)
	if !hasRow(rows, "• [ ] todo") {
		t.Errorf("unchecked box wrong: %q", rows)
	}
	if !hasRow(rows, "• [✓] done") {
		t.Errorf("checked box wrong: %q", rows)
	}
}

// TestRenderMarkdown_BlockQuoteGetsABar pins the quote's marker and the
// recursion that gives a nested quote a second one.
func TestRenderMarkdown_BlockQuoteGetsABar(t *testing.T) {
	rows := renderMD("> quoted\n\n> > deeper", 40)
	if !hasRow(rows, "▎ quoted") {
		t.Errorf("quote bar missing: %q", rows)
	}
	if !hasRow(rows, "▎ ▎ deeper") {
		t.Errorf("nested quote did not gain a second bar: %q", rows)
	}
	for _, r := range rows {
		if strings.Contains(r, ">") {
			t.Errorf("a quote marker survived: %q", r)
		}
	}
}

// TestRenderMarkdown_Table pins the pipe-table layout: a header, a rule
// under it, and box-drawing separators in place of the pipes.
func TestRenderMarkdown_Table(t *testing.T) {
	rows := renderMD("| a | b |\n| --- | ---: |\n| 1 | 2 |", 40)
	var head, rule, body bool
	for _, r := range rows {
		switch {
		case strings.Contains(r, "│ a ") && strings.Contains(r, "│ b "):
			head = true
		case strings.Contains(r, "├─") && strings.HasSuffix(r, "┤"):
			rule = true
		case strings.Contains(r, "│ 1 "):
			body = true
		}
	}
	if !head || !rule || !body {
		t.Errorf("table incomplete (head=%v rule=%v body=%v): %q", head, rule, body, rows)
	}
}

// TestRenderMarkdown_SingleLineWithAPipeIsNotATable pins the two-line
// requirement — otherwise every shell pipeline in a document becomes a
// one-column table.
func TestRenderMarkdown_SingleLineWithAPipeIsNotATable(t *testing.T) {
	rows := renderMD("run ls | grep go to list them", 40)
	for _, r := range rows {
		if strings.Contains(r, "│") {
			t.Errorf("a prose line with a pipe was laid out as a table: %q", rows)
		}
	}
}

// TestRenderMarkdown_FrontMatterOnlyAtTheTop pins that a leading YAML
// block is shown as metadata while an identical "---" later in the file
// is a thematic break — the rule that keeps the renderer from silently
// swallowing a document's second half.
func TestRenderMarkdown_FrontMatterOnlyAtTheTop(t *testing.T) {
	rows := renderMD("---\ntitle: x\n---\n\nbody", 20)
	if !hasRow(rows, "title: x") || !hasRow(rows, "body") {
		t.Errorf("front matter render wrong: %q", rows)
	}
	// An unterminated leading "---" is a break, not a swallowed file.
	rows = renderMD("---\nbody here", 20)
	if !hasRow(rows, "body here") {
		t.Errorf("an unclosed front-matter marker ate the document: %q", rows)
	}
}

// TestRenderMarkdown_EveryRowNamesItsSource pins the property that makes
// the preview clickable: a row either carries the buffer line it came
// from, or is a synthesized row whose meaning is the nearest one above.
func TestRenderMarkdown_EveryRowNamesItsSource(t *testing.T) {
	const src = "# Title\n\nsome prose here\n\n- item one\n- item two"
	rows := renderRows(src, 40)
	lines := strings.Split(src, "\n")
	for i, r := range rows {
		if r.Src < -1 || r.Src >= len(lines) {
			t.Fatalf("row %d has an impossible Src %d", i, r.Src)
		}
	}
	// The heading's own row must point at line 0 and the second list
	// item at line 5, or a click would land somewhere else entirely.
	if got := MDRowForLine(rows, 0); rows[got].Src != 0 {
		t.Errorf("MDRowForLine(0) landed on Src %d", rows[got].Src)
	}
	if got := MDRowForLine(rows, 5); rows[got].Src != 5 {
		t.Errorf("MDRowForLine(5) landed on Src %d", rows[got].Src)
	}
}

// TestMDLineForRow_SynthesizedRowMeansTheOneAbove pins the backwards
// search: clicking a heading's underline rule means the heading.
func TestMDLineForRow_SynthesizedRowMeansTheOneAbove(t *testing.T) {
	rows := renderRows("# Title\n\nbody", 20)
	// Row 0 is the heading, row 1 its rule.
	if rows[1].Src != -1 {
		t.Fatalf("expected row 1 to be the synthesized rule, got Src %d", rows[1].Src)
	}
	if got := MDLineForRow(rows, 1); got != 0 {
		t.Errorf("MDLineForRow(rule) = %d, want 0 (the heading)", got)
	}
}

// TestRenderMarkdown_NoRowExceedsTheWidth is the invariant every block
// has to keep: the pane is what it is, and a row wider than it would be
// clipped mid-glyph by the draw.
func TestRenderMarkdown_NoRowExceedsTheWidth(t *testing.T) {
	const src = "# A fairly long heading that will not fit\n\n" +
		"paragraph with a very long word: " + "z0123456789012345678901234567890\n\n" +
		"| column one | column two | column three |\n| --- | --- | --- |\n| aaaaaaaaaa | bbbbbbbbbb | cccccccccc |\n\n" +
		"> a quoted line that is also quite long indeed\n\n" +
		"- a list item whose text runs on and on and on\n\n" +
		"```go\nfunc main() { fmt.Println(\"a long line of code right here\") }\n```"
	for _, w := range []int{20, 32, 60} {
		for _, r := range renderRows(src, w) {
			if len(r.Runes) > w {
				t.Errorf("width %d: row of %d runes: %q", w, len(r.Runes), string(r.Runes))
			}
		}
	}
}

// TestRenderMarkdown_TrailingBlanksTrimmed pins that a document's
// trailing newlines do not become rows the overflow marker counts.
func TestRenderMarkdown_TrailingBlanksTrimmed(t *testing.T) {
	if n := len(renderRows("body\n\n\n\n", 20)); n != 1 {
		t.Errorf("got %d rows, want 1", n)
	}
}

// TestMDListItem pins the marker classifier's refusals, which are what
// keep ordinary prose from being read as a list.
func TestMDListItem(t *testing.T) {
	yes := []string{"- a", "* a", "+ a", "1. a", "12) a", "  - nested"}
	no := []string{"-a", "*bold*", "1.a", "1234567890. a", "text - dash"}
	for _, s := range yes {
		if _, _, _, ok := mdListItem(s); !ok {
			t.Errorf("mdListItem(%q) = false, want true", s)
		}
	}
	for _, s := range no {
		if _, _, _, ok := mdListItem(s); ok {
			t.Errorf("mdListItem(%q) = true, want false", s)
		}
	}
}

// TestRenderMarkdown_CodeTabsExpand pins that a fence keeps the
// indentation the file it was copied from had. The preview draws every
// cell literally — there is no tab-stop pass under it like the source
// view has — so an unexpanded tab would flatten a Go snippet's structure
// to one blank column.
func TestRenderMarkdown_CodeTabsExpand(t *testing.T) {
	rows := renderMD("```go\nfunc f() {\n\tg()\n}\n```", 40)
	if !hasRow(rows, " "+strings.Repeat(" ", TabStop)+"g()") {
		t.Errorf("the tab did not expand to a tab stop: %q", rows)
	}
	for _, r := range rows {
		if strings.ContainsRune(r, '\t') {
			t.Errorf("a raw tab reached a display row: %q", r)
		}
	}
}

// TestMDFenceOpen pins the info-string handling: the first word is the
// language, the rest is somebody else's directive.
func TestMDFenceOpen(t *testing.T) {
	if _, _, lang, ok := mdFenceOpen("```go run"); !ok || lang != "go" {
		t.Errorf("got (%q, %v), want (go, true)", lang, ok)
	}
	if _, _, lang, ok := mdFenceOpen("~~~"); !ok || lang != "" {
		t.Errorf("got (%q, %v), want ('', true)", lang, ok)
	}
	// A backtick in a backtick fence's info string is inline code in a
	// paragraph, not a fence.
	if _, _, _, ok := mdFenceOpen("a ``code`` line"); ok {
		t.Error("inline code was read as a fence")
	}
}

// TestMDDelimiterRow pins alignment parsing and the refusal that keeps a
// row of prose from opening a table.
func TestMDDelimiterRow(t *testing.T) {
	got, ok := mdDelimiterRow("| :-- | :-: | --: | --- |")
	if !ok {
		t.Fatal("delimiter row not recognised")
	}
	if want := []int{0, 1, 2, 0}; len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	} else {
		for i := range want {
			if got[i] != want[i] {
				t.Errorf("align[%d] = %d, want %d", i, got[i], want[i])
			}
		}
	}
	if _, ok := mdDelimiterRow("| a - b | c |"); ok {
		t.Error("a prose row was accepted as a delimiter row")
	}
}
