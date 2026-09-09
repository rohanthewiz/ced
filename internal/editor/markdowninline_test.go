// =============================================================================
// File: internal/editor/markdowninline_test.go
// Author: Rohan Allison
// Created: 2026-09-09
// Copyright: 2026 Cloudmanic, LLC. All rights reserved.
// =============================================================================

package editor

import (
	"strings"
	"testing"

	"github.com/gdamore/tcell/v2"

	"github.com/rohanthewiz/ced/internal/theme"
)

// inlineText joins the spans back into the text the reader will see. The
// gap between it and the source is the point of most of these tests:
// markup goes in, prose comes out.
func inlineText(spans []mdSpan) string {
	var b strings.Builder
	for _, s := range spans {
		b.WriteString(s.text)
	}
	return b.String()
}

func inlinePalette() mdPalette { return mdPaletteFor(theme.Default()) }

func parseFixture(s string) []mdSpan {
	p := inlinePalette()
	return parseInline(s, p.base, p, 0)
}

// TestParseInline_StripsEmphasisMarkers pins the reader's basic promise:
// the delimiters are formatting instructions, not text, so none of them
// survive into what is drawn.
func TestParseInline_StripsEmphasisMarkers(t *testing.T) {
	cases := map[string]string{
		"**bold**":          "bold",
		"*em*":              "em",
		"_em_":              "em",
		"***both***":        "both",
		"~~gone~~":          "gone",
		"a **b** c *d* e":   "a b c d e",
		"`code`":            "code",
		"plain text":        "plain text",
		"trailing **bold**": "trailing bold",
	}
	for src, want := range cases {
		if got := inlineText(parseFixture(src)); got != want {
			t.Errorf("parseInline(%q) = %q, want %q", src, got, want)
		}
	}
}

// TestParseInline_BoldIsBold pins that the emphasis actually reaches the
// style, not just that the markers vanished — a renderer that dropped
// the delimiters and styled nothing would pass the test above.
func TestParseInline_BoldIsBold(t *testing.T) {
	spans := parseFixture("a **loud** b")
	var found bool
	for _, s := range spans {
		if s.text != "loud" {
			continue
		}
		found = true
		if _, _, attr := s.style.Decompose(); attr&tcell.AttrBold == 0 {
			t.Errorf("**loud** did not come back bold (attr=%v)", attr)
		}
	}
	if !found {
		t.Fatalf("no span carried the bold text: %#v", spans)
	}
}

// TestParseInline_UnderscoreInsideWordIsLiteral pins the single most
// visible way this renderer could be wrong in a code-adjacent document:
// snake_case identifiers must not turn into italics.
func TestParseInline_UnderscoreInsideWordIsLiteral(t *testing.T) {
	const src = "call some_long_name(x) now"
	if got := inlineText(parseFixture(src)); got != src {
		t.Errorf("parseInline(%q) = %q, want it unchanged", src, got)
	}
}

// TestParseInline_CodeSpanWinsOverEmphasis pins the precedence a reader
// of a document ABOUT markdown depends on: delimiters inside backticks
// are literal text.
func TestParseInline_CodeSpanWinsOverEmphasis(t *testing.T) {
	spans := parseFixture("use `**stars**` here")
	if got, want := inlineText(spans), "use **stars** here"; got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

// TestParseInline_EscapedDelimiterIsLiteral pins backslash escapes.
func TestParseInline_EscapedDelimiterIsLiteral(t *testing.T) {
	if got, want := inlineText(parseFixture(`\*not em\*`)), "*not em*"; got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

// TestParseInline_LinkRendersTextNotURL pins the deliberate divergence:
// a terminal has nothing to click, so the href is not repeated inline.
func TestParseInline_LinkRendersTextNotURL(t *testing.T) {
	spans := parseFixture("see [the docs](https://example.com/x) now")
	got := inlineText(spans)
	if want := "see the docs now"; got != want {
		t.Errorf("got %q, want %q", got, want)
	}
	if strings.Contains(got, "example.com") {
		t.Errorf("the URL leaked into the rendered text: %q", got)
	}
}

// TestParseInline_AutolinkAndBareURLSurvive pins the other half of that
// rule: when the URL IS the text, hiding it would lose content.
func TestParseInline_AutolinkAndBareURLSurvive(t *testing.T) {
	for _, src := range []string{"<https://example.com>", "go to https://example.com now"} {
		if !strings.Contains(inlineText(parseFixture(src)), "https://example.com") {
			t.Errorf("parseInline(%q) dropped the URL", src)
		}
	}
	// Trailing sentence punctuation is not part of the link.
	spans := parseFixture("see https://example.com.")
	for _, s := range spans {
		if strings.HasSuffix(s.text, "com.") {
			t.Errorf("the full stop was swallowed into the URL: %q", s.text)
		}
	}
}

// TestParseInline_ImageIsMarkedAsAbsent pins that an image announces
// itself rather than passing its alt text off as prose.
func TestParseInline_ImageIsMarkedAsAbsent(t *testing.T) {
	got := inlineText(parseFixture("![a diagram](x.png)"))
	if !strings.Contains(got, "▣") || !strings.Contains(got, "a diagram") {
		t.Errorf("got %q, want a marker plus the alt text", got)
	}
}

// TestParseInline_UnclosedDelimiterIsLiteral pins the failure mode that
// matters most: half-typed markup must not eat the rest of the line.
func TestParseInline_UnclosedDelimiterIsLiteral(t *testing.T) {
	for _, src := range []string{"a **b", "a `b", "a [b", "a ~~b"} {
		if got := inlineText(parseFixture(src)); got != src {
			t.Errorf("parseInline(%q) = %q, want it unchanged", src, got)
		}
	}
}

// TestParseLinkAt_NestedBrackets pins that the label's own brackets do
// not end it early.
func TestParseLinkAt_NestedBrackets(t *testing.T) {
	rs := []rune("[see [1] here](u)")
	text, dest, next, ok := parseLinkAt(rs, 0)
	if !ok {
		t.Fatal("parseLinkAt did not match")
	}
	if text != "see [1] here" || dest != "u" || next != len(rs) {
		t.Errorf("got (%q, %q, %d), want (%q, %q, %d)", text, dest, next, "see [1] here", "u", len(rs))
	}
}

// TestParseLinkAt_ReferenceStyleIsNotMatched pins the documented gap:
// "[text][ref]" needs a definition table this reader does not build, so
// it stays literal rather than rendering as a broken link.
func TestParseLinkAt_ReferenceStyleIsNotMatched(t *testing.T) {
	if _, _, _, ok := parseLinkAt([]rune("[text][ref]"), 0); ok {
		t.Error("reference-style link matched; it should fall through to literal text")
	}
}

// TestFindRun_MatchesExactLength pins that a closing "**" is not found
// inside a longer run of asterisks further down the line.
func TestFindRun_MatchesExactLength(t *testing.T) {
	rs := []rune("a***b**")
	if got := findRun(rs, 0, '*', 2); got != 5 {
		t.Errorf("findRun = %d, want 5 (the exact-length run, not the triple)", got)
	}
}
