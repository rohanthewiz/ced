// =============================================================================
// File: internal/diff/words_test.go
// Author: Rohan Allison <rohanthewiz@gmail.com>
// Created: 2026-10-22
// Copyright: 2026 Rohan Allison. All rights reserved.
// =============================================================================

package diff

import (
	"reflect"
	"strings"
	"testing"
)

// spanText renders the spans of s as the substrings they cover, so a
// test states what is emphasised rather than offsets.
func spanText(s string, spans []Span) []string {
	rs := []rune(s)
	var out []string
	for _, sp := range spans {
		out = append(out, string(rs[sp.From:sp.To]))
	}
	return out
}

// TestWords_OneWordInALongLine emphasises only the changed word on each
// side — the case the feature exists for.
func TestWords_OneWordInALongLine(t *testing.T) {
	a := `	return fmt.Errorf("could not open the configuration file: %w", err)`
	b := `	return fmt.Errorf("could not read the configuration file: %w", err)`
	as, bs := Words(a, b, Options{})
	if got := spanText(a, as); !reflect.DeepEqual(got, []string{"open"}) {
		t.Fatalf("old spans = %q", got)
	}
	if got := spanText(b, bs); !reflect.DeepEqual(got, []string{"read"}) {
		t.Fatalf("new spans = %q", got)
	}
}

// TestWords_PunctuationAnchors keeps punctuation as its own token, so a
// changed identifier between dots and parens is all that lights up.
func TestWords_PunctuationAnchors(t *testing.T) {
	as, bs := Words("x := a.b(c)", "x := a.d(c)", Options{})
	if got := spanText("x := a.b(c)", as); !reflect.DeepEqual(got, []string{"b"}) {
		t.Fatalf("old spans = %q", got)
	}
	if got := spanText("x := a.d(c)", bs); !reflect.DeepEqual(got, []string{"d"}) {
		t.Fatalf("new spans = %q", got)
	}
}

// TestWords_PureInsertion leaves the old side unemphasised when the new
// side only adds tokens.
func TestWords_PureInsertion(t *testing.T) {
	a, b := "call(a, b)", "call(a, b, c)"
	as, bs := Words(a, b, Options{})
	if len(as) != 0 {
		t.Fatalf("old spans = %q, want none", spanText(a, as))
	}
	if got := spanText(b, bs); !reflect.DeepEqual(got, []string{", c"}) {
		t.Fatalf("new spans = %q", got)
	}
}

// TestWords_GapOfSpaceMerges fuses two changed words separated only by a
// shared space into one span per side.
func TestWords_GapOfSpaceMerges(t *testing.T) {
	a := "keep these words then foo bar and the end"
	b := "keep these words then baz qux and the end"
	as, bs := Words(a, b, Options{})
	if got := spanText(a, as); !reflect.DeepEqual(got, []string{"foo bar"}) {
		t.Fatalf("old spans = %q", got)
	}
	if got := spanText(b, bs); !reflect.DeepEqual(got, []string{"baz qux"}) {
		t.Fatalf("new spans = %q", got)
	}
}

// TestWords_DissimilarLinesGetNothing declines to emphasise a pair that
// shares too little — painting every word says nothing.
func TestWords_DissimilarLinesGetNothing(t *testing.T) {
	as, bs := Words("x := compute(1)", "return errors.New(msg)", Options{})
	if as != nil || bs != nil {
		t.Fatalf("spans = %v / %v, want none", as, bs)
	}
}

// TestWords_EqualAndOverlong both answer nil: nothing to show, and a
// line past the cap is not looked inside.
func TestWords_EqualAndOverlong(t *testing.T) {
	if as, bs := Words("same", "same", Options{}); as != nil || bs != nil {
		t.Fatalf("equal lines: %v / %v", as, bs)
	}
	long := strings.Repeat("w ", maxWordLineRunes)
	if as, bs := Words(long+"a", long+"b", Options{}); as != nil || bs != nil {
		t.Fatal("an over-long line was looked inside")
	}
}

// TestWords_Whitespace shows a spacing change normally and hides it
// under IgnoreWhitespace, while a real edit still shows under it.
func TestWords_Whitespace(t *testing.T) {
	a, b := "x = a+b + c", "x = a + b + c"
	_, bs := Words(a, b, Options{})
	if len(bs) == 0 {
		t.Fatal("a spacing change shows nothing with whitespace counted")
	}
	as, bs := Words(a, b, Options{IgnoreWhitespace: true})
	if as != nil || bs != nil {
		t.Fatalf("spacing emphasised under IgnoreWhitespace: %v / %v", as, bs)
	}
	a2, b2 := "x = a+b + c", "x = a + d + c"
	_, bs = Words(a2, b2, Options{IgnoreWhitespace: true})
	if got := spanText(b2, bs); !reflect.DeepEqual(got, []string{"d"}) {
		t.Fatalf("new spans under -w = %q", got)
	}
}

// TestTokenizeWords cuts words, whitespace runs and single punctuation
// runes, with rune (not byte) offsets.
func TestTokenizeWords(t *testing.T) {
	toks := tokenizeWords("héllo  wörld_2(x)")
	var got []string
	for _, tk := range toks {
		got = append(got, tk.text)
	}
	want := []string{"héllo", "  ", "wörld_2", "(", "x", ")"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("tokens = %q, want %q", got, want)
	}
	if toks[2].from != 7 || toks[2].to != 14 {
		t.Fatalf("wörld_2 extent = %d..%d, want 7..14 (runes)", toks[2].from, toks[2].to)
	}
}
