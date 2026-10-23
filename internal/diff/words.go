// =============================================================================
// File: internal/diff/words.go
// Author: Rohan Allison <rohanthewiz@gmail.com>
// Created: 2026-10-22
// Copyright: 2026 Rohan Allison. All rights reserved.
// =============================================================================

// words.go finds WHAT changed inside a changed line: given an old line
// and the new line it was replaced by, the rune spans on each side that
// differ. A side-by-side diff pane uses it to emphasise a one-word edit
// in a long line instead of leaving the reader to spot it.
//
//	old: return fmt.Errorf("open %s: %w", path, err)
//	                                  ^   ^^^^
//	new: return fmt.Errorf("open %s: %v", name, err)
//	                                  ^   ^^^^
//
// Design choices:
//
//   - NOT A SECOND DIFFER. The lines are cut into tokens and the token
//     lists go through this package's own patience diff (DiffWith) —
//     the same algorithm, budget and whitespace option the line diff
//     uses. A line is just a short document whose "lines" are words.
//   - TOKENS ARE WORDS, NOT RUNES. A rune-level diff of "count" vs
//     "amount" finds the shared "ount" and paints c|ount vs am|ount,
//     which reads as noise. A word (letters, digits, underscore) is the
//     unit a reader edits; every other rune is a token on its own so
//     punctuation still anchors ("a.b(c)" vs "a.d(c)" emphasises b/d
//     only); a whitespace run is one token.
//   - DISSIMILAR LINES GET NOTHING. Positional pairing (the pane zips a
//     removed run against an added run) often puts two unrelated lines
//     side by side. Emphasising nearly every word of both says nothing
//     the line colour didn't already, so when the unchanged part is less
//     than half of the longer line (non-blank runes) the answer is
//     "no spans" and the pane draws the pair plainly.
//   - SPANS SEPARATED ONLY BY WHITESPACE MERGE. "foo bar" → "baz qux"
//     emphasises one run per side, not two with a hole where the shared
//     space is — a striped run is harder to read than a solid one.
//   - UNDER IgnoreWhitespace a whitespace token is never emphasised on
//     its own: the user asked for spacing not to count, and DiffWith's
//     normalised keys already let any two whitespace runs match.
package diff

import "unicode"

// Span is a half-open rune range [From, To) within one line.
type Span struct{ From, To int }

// maxWordLineRunes caps the lines Words will look inside. Past it the
// answer is "no spans": a line that long is minified or generated text,
// and the pane clips it to its width anyway.
const maxWordLineRunes = 2000

// wordToken is one token of a line and the rune offset it starts at.
type wordToken struct {
	text  string
	from  int // rune offset of the first rune
	to    int // rune offset one past the last rune
	space bool
	solid int // non-whitespace runes in the token
}

// tokenizeWords cuts a line into word, whitespace-run and single-rune
// punctuation tokens, recording each token's rune extent.
func tokenizeWords(s string) []wordToken {
	rs := []rune(s)
	var toks []wordToken
	isWord := func(r rune) bool { return r == '_' || unicode.IsLetter(r) || unicode.IsDigit(r) }
	for i := 0; i < len(rs); {
		j := i + 1
		switch {
		case isWord(rs[i]):
			for j < len(rs) && isWord(rs[j]) {
				j++
			}
		case unicode.IsSpace(rs[i]):
			for j < len(rs) && unicode.IsSpace(rs[j]) {
				j++
			}
		}
		tk := wordToken{text: string(rs[i:j]), from: i, to: j, space: unicode.IsSpace(rs[i])}
		if !tk.space {
			tk.solid = j - i
		}
		toks = append(toks, tk)
		i = j
	}
	return toks
}

// Words returns the rune spans of a and b that differ, in order, or
// nil for both when the lines are equal (under opt), too long to look
// inside, or too different for emphasis to help (see the header).
func Words(a, b string, opt Options) (as, bs []Span) {
	if a == b {
		return nil, nil
	}
	ta, tb := tokenizeWords(a), tokenizeWords(b)
	if (len(ta) > 0 && ta[len(ta)-1].to > maxWordLineRunes) ||
		(len(tb) > 0 && tb[len(tb)-1].to > maxWordLineRunes) {
		return nil, nil
	}
	ka, kb := make([]string, len(ta)), make([]string, len(tb))
	for i, tk := range ta {
		ka[i] = tk.text
	}
	for i, tk := range tb {
		kb[i] = tk.text
	}

	shared, totalA, totalB := 0, 0, 0
	for _, tk := range ta {
		totalA += tk.solid
	}
	for _, tk := range tb {
		totalB += tk.solid
	}
	for _, e := range DiffWith(ka, kb, opt) {
		switch e.Op {
		case OpEqual:
			// Under IgnoreWhitespace an "equal" pair can still differ in
			// spelling only by whitespace, so the NEW side's count is
			// the honest one for both.
			shared += tb[e.NewLine-1].solid
		case OpDelete:
			as = addWordSpan(as, ta, e.OldLine-1, opt)
		case OpInsert:
			bs = addWordSpan(bs, tb, e.NewLine-1, opt)
		}
	}
	if len(as) == 0 && len(bs) == 0 {
		return nil, nil
	}
	if shared*2 < max(totalA, totalB) {
		return nil, nil
	}
	return as, bs
}

// addWordSpan appends token k's extent to spans, extending the last span
// instead when only whitespace lies between them (the gap merge), and
// skipping a whitespace token outright under IgnoreWhitespace.
func addWordSpan(spans []Span, toks []wordToken, k int, opt Options) []Span {
	tk := toks[k]
	if tk.space && opt.IgnoreWhitespace {
		return spans
	}
	if n := len(spans); n > 0 && onlySpaceBefore(toks, k, spans[n-1].To) {
		spans[n-1].To = tk.to
		return spans
	}
	return append(spans, Span{From: tk.from, To: tk.to})
}

// onlySpaceBefore reports whether every token between rune offset from
// and token k is whitespace — true when there are none, so adjacent
// changed tokens always fuse into one span. It walks back from k, so the
// cost is the gap, not the line.
func onlySpaceBefore(toks []wordToken, k, from int) bool {
	for j := k - 1; j >= 0 && toks[j].from >= from; j-- {
		if !toks[j].space {
			return false
		}
	}
	return true
}
