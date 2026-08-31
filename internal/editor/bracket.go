// =============================================================================
// File: internal/editor/bracket.go
// Author: Rohan Allison <rohanthewiz@gmail.com>
// Created: 2026-08-31
// Copyright: 2026 Rohan Allison. All rights reserved.
// =============================================================================

// bracket.go answers one question: what does the bracket under the caret
// pair with? It carries the matcher, the "is this bracket real code?"
// classifier, and the decoration source that boxes the pair. The jump
// verb built on it lives in app/bracket.go.
//
// Three decisions shape everything here.
//
//  1. **The scan reads the SYNTAX GRID, not just the characters.** A
//     naive counter pairs the braces inside `fmt.Printf("{%d}", n)` and
//     then reports every `{` after it one level deep — which is not a
//     cosmetic error, it is the feature confidently pointing at the
//     wrong line. Tab.Styles already holds a per-rune foreground that
//     Chroma assigned, so a bracket painted syn-string or syn-comment
//     can be skipped for free: the grid is computed for the render
//     anyway, and this costs one color compare per bracket. Where there
//     is no grid — a file over MaxHighlightBytes opens with SyntaxOff —
//     the classifier says "code" for everything and the match degrades
//     to the naive one, which is the honest fallback: those files are
//     the ones a real lexer would cost the most on.
//
//  2. **The scan is BUDGETED, and running out is not the same as
//     finding nothing.** A whole-buffer walk per frame is what the
//     word highlighter's window-scoping rule exists to avoid; this one
//     cannot be window-scoped (a function's closing brace is usually
//     off screen, and the jump verb has to reach it), so it is bounded
//     by bracketScanLines instead. Hitting that bound leaves the answer
//     UNKNOWN — reported as Conclusive=false — and an unknown answer
//     paints nothing. Reporting it as "unmatched" would put an error
//     tint on a perfectly balanced brace purely because its partner was
//     far away.
//
//  3. **Only `()[]{}`.** Angle brackets are excluded deliberately: `<`
//     and `>` are comparison operators far more often than they are a
//     pair, so matching them would mean the caret lighting up two
//     unrelated inequalities on most lines of code. Quotes are excluded
//     for the same class of reason — the grid already colors a string
//     end to end, which says it better than two boxed cells would.

package editor

import "github.com/rohanthewiz/ced/internal/theme"

// bracketScanLines caps how far the matcher walks in either direction
// before giving up. Generous enough that no realistic top-level
// declaration exceeds it (a 2000-line function is its own problem) and
// small enough that the walk stays a fraction of a millisecond on the
// 512KB files the editor will still open. See the file comment for why
// exhausting it is reported rather than swallowed.
const bracketScanLines = 2000

// bracketPartner maps each bracket to its opposite. It is a single map
// in both directions so the two scan helpers can share one lookup and
// no code path can disagree about what closes a `[`.
var bracketPartner = map[rune]rune{
	'(': ')', '[': ']', '{': '}',
	')': '(', ']': '[', '}': '{',
}

// isOpenBracket reports whether r opens a pair (scan forward from it).
func isOpenBracket(r rune) bool { return r == '(' || r == '[' || r == '{' }

// isCloseBracket reports whether r closes a pair (scan backward from it).
func isCloseBracket(r rune) bool { return r == ')' || r == ']' || r == '}' }

// isBracket reports whether r is either half of a pair.
func isBracket(r rune) bool { return isOpenBracket(r) || isCloseBracket(r) }

// BracketPair is the matcher's whole answer. Matched and Conclusive are
// two separate facts on purpose: Matched says a partner was found,
// Conclusive says the search was allowed to finish. The combination
// (false, false) means "the budget ran out" and is the one state that
// must render as nothing at all — see the file comment.
type BracketPair struct {
	// At is the bracket the caret is on, or immediately after.
	At Position
	// Rune is the bracket character at At, kept so callers can name it
	// in a message without re-reading the buffer.
	Rune rune
	// Partner is where its opposite sits. Meaningful only when Matched.
	Partner Position
	// Matched is true when Partner holds a real position.
	Matched bool
	// Conclusive is true when the scan reached the edge of the buffer
	// rather than the end of its budget, i.e. when "no partner" is an
	// answer about the FILE rather than about how far we were willing
	// to look.
	Conclusive bool
}

// MatchingBracket resolves the pair for the primary cursor. ok=false
// means the caret isn't on a bracket at all, which is a different
// (and far more common) thing than a bracket with no partner — callers
// that flash a message need to tell those two apart.
//
// The theme comes in because the string/comment classifier reads colors
// out of the style grid, and a Tab holds no theme of its own; Render
// receives one per frame for the same reason.
func (t *Tab) MatchingBracket(th theme.Theme) (BracketPair, bool) {
	if t == nil || t.Buffer == nil || t.IsImage() {
		return BracketPair{}, false
	}
	at, r, ok := t.bracketAtCaret(th)
	if !ok {
		return BracketPair{}, false
	}
	bp := BracketPair{At: at, Rune: r}
	other := bracketPartner[r]
	if isOpenBracket(r) {
		bp.Partner, bp.Matched, bp.Conclusive = t.scanForward(th, at, r, other)
	} else {
		bp.Partner, bp.Matched, bp.Conclusive = t.scanBackward(th, at, other, r)
	}
	return bp, true
}

// bracketAtCaret returns the bracket the caret is sitting on, falling
// back to the one immediately behind it.
//
// The fallback is the same courtesy WordRange extends to a word: the
// caret lands AFTER a character you just typed, so without it the pair
// would refuse to light up at the exact moment you closed it. On wins
// over behind because the caret-on-a-bracket case is the one a
// deliberate cursor move produces, and it is the one the jump verb
// needs to round-trip (landing on a partner must find its way back).
//
// A bracket inside a string or comment is not a candidate, so each
// position is tested independently — `f("(")` with the caret at the
// end of the literal skips the quoted paren rather than pairing it.
func (t *Tab) bracketAtCaret(th theme.Theme) (Position, rune, bool) {
	p := t.Cursor
	if p.Line < 0 || p.Line >= t.Buffer.LineCount() {
		return Position{}, 0, false
	}
	runes := t.Buffer.LineRunes(p.Line)
	for _, col := range []int{p.Col, p.Col - 1} {
		if col < 0 || col >= len(runes) {
			continue
		}
		if r := runes[col]; isBracket(r) && !t.inStringOrComment(th, p.Line, col) {
			return Position{Line: p.Line, Col: col}, r, true
		}
	}
	return Position{}, 0, false
}

// scanForward walks from an opening bracket toward the end of the
// buffer, tracking nesting depth, and returns the position that closes
// it. conclusive is false when the walk stopped at bracketScanLines
// instead of at the last line.
//
// Depth starts at zero and the opener at `from` is counted by the loop
// itself, so the partner is the close that brings depth back to zero —
// no special-casing of the first character, and no way for the two
// halves of the count to drift apart.
func (t *Tab) scanForward(th theme.Theme, from Position, open, close rune) (Position, bool, bool) {
	last, conclusive := t.scanLimit(from.Line, +1)
	depth := 0
	for line := from.Line; line <= last; line++ {
		runes := t.Buffer.LineRunes(line)
		col := 0
		if line == from.Line {
			col = from.Col
		}
		for ; col < len(runes); col++ {
			r := runes[col]
			if r != open && r != close {
				continue
			}
			if t.inStringOrComment(th, line, col) {
				continue
			}
			if r == open {
				depth++
				continue
			}
			if depth--; depth == 0 {
				return Position{Line: line, Col: col}, true, true
			}
		}
	}
	return Position{}, false, conclusive
}

// scanBackward is scanForward's mirror: it walks from a closing bracket
// toward the start of the buffer and returns the opener that balances
// it. Kept as a separate function rather than a direction flag because
// the column bounds differ at both ends of every line, and the flagged
// version of this was harder to read than two short loops.
func (t *Tab) scanBackward(th theme.Theme, from Position, open, close rune) (Position, bool, bool) {
	first, conclusive := t.scanLimit(from.Line, -1)
	depth := 0
	for line := from.Line; line >= first; line-- {
		runes := t.Buffer.LineRunes(line)
		col := len(runes) - 1
		if line == from.Line {
			col = from.Col
		}
		for ; col >= 0; col-- {
			r := runes[col]
			if r != open && r != close {
				continue
			}
			if t.inStringOrComment(th, line, col) {
				continue
			}
			if r == close {
				depth++
				continue
			}
			if depth--; depth == 0 {
				return Position{Line: line, Col: col}, true, true
			}
		}
	}
	return Position{}, false, conclusive
}

// scanLimit returns the last line a scan starting at line may touch in
// the given direction, and whether that limit is the buffer's own edge
// (conclusive) or the budget (not). One helper for both directions so
// the two loops cannot disagree about how far "far enough" is.
func (t *Tab) scanLimit(line, dir int) (limit int, conclusive bool) {
	if dir > 0 {
		limit = t.Buffer.LineCount() - 1
		if budget := line + bracketScanLines; budget < limit {
			return budget, false
		}
		return limit, true
	}
	if budget := line - bracketScanLines; budget > 0 {
		return budget, false
	}
	return 0, true
}

// inStringOrComment reports whether the rune at (line, col) was painted
// as string or comment content by the last syntax pass.
//
// This is a read of the render's own grid rather than a second lexer,
// which is the whole reason the feature can afford to be string-aware
// (see the file comment). Three ways it deliberately says "code":
//
//   - No grid at all (SyntaxOff, or a row the last pass hasn't reached).
//     Degrading to naive matching beats refusing to match.
//   - The color equals the plain text color. A theme is free to state
//     syn-string as its foreground; when it does, this classifier can no
//     longer tell content from code, and answering "string" would make
//     it skip every real bracket in the file.
//   - Anything else. The grid is patched, not re-lexed, during a typing
//     burst (see syntax.go), so it can be a keystroke stale — which for
//     an ambient highlight is invisible, and which is why this reads the
//     cheap cached answer instead of forcing a re-lex.
func (t *Tab) inStringOrComment(th theme.Theme, line, col int) bool {
	if t.SyntaxOff || line < 0 || line >= len(t.Styles) {
		return false
	}
	row := t.Styles[line]
	if col < 0 || col >= len(row) {
		return false
	}
	fg, _, _ := row[col].Decompose()
	if fg == th.Text {
		return false
	}
	return fg == th.SynString || fg == th.SynComment
}

// bracketSource boxes the bracket under the caret and its partner.
//
// It runs among the ambient built-ins, after the word highlight and
// before selection and find, so an answer the user actually asked for
// always paints over it. The overlap with the word highlight is
// theoretical anyway — a bracket is not a word rune — but the order
// makes the intent explicit rather than accidental.
type bracketSource struct{}

// Decorations returns the pair's two spans, or the single error-tinted
// span of a bracket that provably has no partner, culled to the visible
// window.
//
// Loudness here is the inverse of the word highlight's: that feature can
// paint dozens of cells so it has to stay quiet, while this one paints
// exactly two, so a full box plus bold is affordable and is what makes
// the partner findable from across a screen. Matched uses a FILL and
// unmatched a foreground tint — different in kind, not just in hue, so
// the two states can never be mistaken for each other at a glance.
func (bracketSource) Decorations(t *Tab, th theme.Theme, firstLine, lastLine int) ([]Span, []GutterMark) {
	if t == nil || t.IsImage() {
		return nil, nil
	}
	// With a caret column live, every position the user cares about is
	// already marked by a caret they placed. A pair box under them would
	// only muddy which mark is which — the same argument the word
	// highlight makes for standing down here.
	if t.HasCarets() {
		return nil, nil
	}
	bp, ok := t.MatchingBracket(th)
	if !ok {
		return nil, nil
	}
	if !bp.Matched {
		// Silent when the budget ran out: "no partner" is only worth
		// saying when the scan was allowed to prove it.
		if !bp.Conclusive {
			return nil, nil
		}
		return bracketSpans(StyleDelta{SetFG: true, FG: th.BracketUnmatched, Bold: true},
			firstLine, lastLine, bp.At), nil
	}
	return bracketSpans(StyleDelta{SetBG: true, BG: th.BracketMatch, Bold: true},
		firstLine, lastLine, bp.At, bp.Partner), nil
}

// bracketSpans turns bracket positions into one-cell spans, dropping any
// that fall outside the visible window. A partner scrolled off screen
// simply contributes nothing — the caret's own bracket still lights up,
// which is the honest report ("there is a pair, and its other half is
// not here").
func bracketSpans(delta StyleDelta, firstLine, lastLine int, at ...Position) []Span {
	spans := make([]Span, 0, len(at))
	for _, p := range at {
		if p.Line < firstLine || p.Line > lastLine {
			continue
		}
		spans = append(spans, Span{
			Start: p,
			End:   Position{Line: p.Line, Col: p.Col + 1},
			Delta: delta,
		})
	}
	if len(spans) == 0 {
		return nil
	}
	return spans
}
