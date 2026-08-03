// =============================================================================
// File: internal/editor/wordhl.go
// Author: Rohan Allison <rohanthewiz@gmail.com>
// Created: 2026-07-31
// Copyright: 2026 Rohan Allison. All rights reserved.
// =============================================================================

// wordhl.go is the "what else is called this?" layer: the word-boundary
// scanner, the occurrence matcher both it and the multi-caret gestures
// use, and the built-in decoration source that tints every other
// instance of the word under the cursor.
//
// Two deliberate differences from find.go, which does the other kind of
// searching:
//
//   - Matching here is CASE-SENSITIVE and (for a bare cursor)
//     whole-word. Find is a reading tool — you type "buffer" and want
//     every spelling of it. This is a code-reading tool: `Cursor` and
//     `cursor` are two different identifiers, and highlighting one when
//     the caret sits in the other is a lie.
//   - The scan is WINDOW-SCOPED. Find caches its match list and
//     recomputes on query change; the word under the cursor changes on
//     every caret move, so there's nothing to cache against and the
//     scan runs inside the frame. Scanning the visible rows keeps that
//     cost proportional to the screen instead of the file — the same
//     reason DecorationSource takes a line window at all. The visible
//     tradeoff: a word with one instance on screen and another
//     scrolled off doesn't light up. That's the honest reading of
//     "quick" here — it marks what you can see.
package editor

import (
	"github.com/gdamore/tcell/v2"

	"github.com/rohanthewiz/ced/internal/theme"
)

// minSelectionHighlight is the shortest selection that will drive the
// highlight. One character is almost always punctuation the user is
// dragging across, and lighting up every "(" in the viewport is noise,
// not information. A one-rune identifier under a bare cursor (Go's `i`,
// `n`, `t`) still highlights — that path is whole-word matched, so it
// can't spray across the screen the way a bare rune search would.
const minSelectionHighlight = 2

// IsWordRune reports whether r can be part of an identifier for the
// purposes of word selection and whole-word matching. Deliberately the
// programmer's definition (letters, digits, underscore) rather than a
// natural-language one: this drives double-click select and the
// occurrence highlight, and both exist to work on code.
func IsWordRune(r rune) bool {
	return r == '_' ||
		(r >= 'a' && r <= 'z') ||
		(r >= 'A' && r <= 'Z') ||
		(r >= '0' && r <= '9') ||
		r > 0x7f // treat non-ASCII as word content — identifiers may be unicode
}

// WordRange returns the [start, end) rune columns of the word touching
// col, or ok=false when col sits in whitespace or punctuation. A caret
// resting immediately after a word counts as being in it (that's where
// the caret lands after typing one), which is what makes "highlight the
// word I just finished typing" work.
func WordRange(runes []rune, col int) (start, end int, ok bool) {
	if col < 0 || len(runes) == 0 {
		return 0, 0, false
	}
	if col > len(runes) {
		col = len(runes)
	}
	start, end = col, col
	for start > 0 && IsWordRune(runes[start-1]) {
		start--
	}
	for end < len(runes) && IsWordRune(runes[end]) {
		end++
	}
	if start == end {
		return 0, 0, false
	}
	return start, end, true
}

// MatchOccurrences returns every case-sensitive hit of text on lines
// [first, last]. wholeWord rejects hits that are part of a longer
// identifier, so searching "err" doesn't light up "errors". Matches
// never overlap — the scanner advances past a hit — and the line window
// is clamped to the buffer, so callers can pass a render window
// straight through.
//
// The scan itself is find.go's matchCols, so this and the find bar agree
// on what a hit is; the window and the always-case-sensitive compare are
// what make this the code-reading variant.
func MatchOccurrences(b *Buffer, text string, wholeWord bool, first, last int) []Match {
	if b == nil || text == "" {
		return nil
	}
	needle := []rune(text)
	if first < 0 {
		first = 0
	}
	if last >= b.LineCount() {
		last = b.LineCount() - 1
	}
	var out []Match
	for line := first; line <= last; line++ {
		for _, col := range matchCols(b.LineRunes(line), needle, wholeWord) {
			out = append(out, Match{Line: line, Col: col, Width: len(needle)})
		}
	}
	return out
}

// wordHighlightSource tints every visible occurrence of the word under
// the cursor (or of a short single-line selection). It runs BEFORE the
// selection and find sources so both keep painting on top of it — this
// is ambient context, not an answer the user asked for.
type wordHighlightSource struct{}

// Decorations returns one span per visible occurrence, or nothing when
// the highlight is disabled, the caret isn't in a word, or the word
// appears only once on screen. That last rule is the whole point of the
// feature: a lone hit is where the caret already is, and tinting it
// tells the user nothing they didn't know.
func (wordHighlightSource) Decorations(t *Tab, th theme.Theme, firstLine, lastLine int) ([]Span, []GutterMark) {
	if t == nil || !t.WordHighlight || t.IsImage() {
		return nil, nil
	}
	// With multiple carets live, the occurrences the user cares about are
	// already marked — by carets they placed deliberately. A second,
	// weaker wash under them would only muddy which is which.
	if t.HasCarets() {
		return nil, nil
	}
	text, wholeWord, ok := t.caretQuery()
	if !ok {
		return nil, nil
	}
	if !wholeWord && len([]rune(text)) < minSelectionHighlight {
		return nil, nil
	}
	matches := MatchOccurrences(t.Buffer, text, wholeWord, firstLine, lastLine)
	if len(matches) < 2 {
		return nil, nil
	}
	spans := make([]Span, 0, len(matches))
	for _, m := range matches {
		spans = append(spans, Span{
			Start: MatchPosition(m),
			End:   MatchEndPosition(m),
			// Bold as well as the box. A background step alone is at the
			// mercy of the terminal's contrast and the user's screen —
			// the first cut of this feature was invisible on a laptop
			// panel. Weight reads at any contrast, and it's an attribute
			// nothing else in the editor body claims (find and selection
			// are fills; diagnostics underline).
			Delta: StyleDelta{SetBG: true, BG: th.WordHL, Bold: true},
		})
	}
	return spans, nil
}

// wordHighlightStyle is the caret cell's paint for a secondary caret —
// kept here beside the highlight color so the two "where else is this"
// affordances pick their colors from one place. See multicaret.go for
// why carets are painted rather than decorated.
func wordHighlightStyle(th theme.Theme, bg tcell.Color) tcell.Style {
	return tcell.StyleDefault.Background(th.Accent).Foreground(bg)
}
