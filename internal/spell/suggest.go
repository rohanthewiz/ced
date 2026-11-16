// =============================================================================
// File: internal/spell/suggest.go
// Author: Rohan Allison <rohanthewiz@gmail.com>
// Created: 2026-11-16
// Copyright: 2026 Rohan Allison. All rights reserved.
// Copied from cats-todo's internal/spell (same author). Suggest's ranking is
// unchanged, so the two editors correct prose words identically; the scan
// is split out as candidates for ident.go's re-ranking.
// =============================================================================

// Corrections for a word the checker flagged: the dictionary entries within a
// couple of typing mistakes of it, best first.
//
// This is the second half of the feature CheckIdent begins. CheckIdent answers
// "is this wrong", which is all an underline needs; Suggest answers "then what
// did they mean", which is what a picker needs, and the two are deliberately
// separate — CheckIdent runs on every redraw and must cost nothing, while
// Suggest runs once, when someone asks for it, and can afford to read the whole
// word list.
//
// The measure is an optimal string alignment distance — Levenshtein plus
// adjacent transposition — with the edits weighted rather than counted, because
// counting them ranks the wrong word first. Every single-edit neighbour of
// "teh" ties at one edit: "the", but also "tea", "ted", "tee" and "Te". What
// separates them is not how many mistakes each implies but which mistake, and
// two letters arriving in the wrong order is a far more likely accident than a
// letter being replaced by a different one that happens to spell another word.
// So a transposition is cheaper than a substitution, an apostrophe left out is
// cheaper still ("dont" was not a typo at all, it was a contraction typed at
// speed), and "teh" suggests "the" while "dont" suggests "don't".
//
// Beyond that there is no word-frequency data to rank with — SCOWL's size 60
// list is a set, not a corpus — so remaining ties break on how much of the
// front of the word survives (a typo lands in the middle or the end far more
// often than on the first letter), then on length, then alphabetically. The
// point of the last two is not that they are right but that they are stable: a
// popup whose rows move between openings is one nobody can build a habit on.
package spell

import (
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"
)

// What each kind of edit costs. The unit is a tenth of an ordinary edit, so
// that "within two mistakes" is still a round number (see suggestLimit) and the
// two discounts have somewhere to sit between the whole numbers.
const (
	costEdit       = 10 // a letter substituted, inserted or missed
	costTranspose  = 8  // two adjacent letters typed in the wrong order
	costApostrophe = 4  // an apostrophe left out, or put in where none belongs
)

// suggestEdits is how many mistakes a word may be away and still be offered, as
// a function of how long the misspelling is. Three- and four-letter words get
// one: at that length two edits reach most of the short words in English ("cat"
// is two from "dog"), and twenty unrelated three-letter words are not a
// suggestion, they are a dictionary. Longer words get two, which is what catches
// a doubled typo ("recieveing") and a word typed from memory.
func suggestEdits(n int) int {
	if n <= 4 {
		return 1
	}
	return 2
}

// suggestion is one candidate mid-ranking: the dictionary's spelling of it,
// what it cost to get there, and how many leading runes it shares with the
// misspelling.
type suggestion struct {
	word   string
	cost   int
	prefix int
}

// suggestMax is how many corrections any caller may ask for. A cap belongs here
// rather than at the call site because it is a property of the measure: past
// the first handful the candidates are all at the far edge of the distance and
// have nothing to recommend them over each other, and a popup that scrolls is
// one the eye has to read rather than glance at.
const suggestMax = 8

// Suggest returns up to max corrections for word, best first, in the case the
// user typed: a misspelling that began with a capital gets capitalized
// suggestions back, so accepting one does not also change the sentence.
//
// The word is matched lowercased, because the list holds proper nouns
// capitalized and a lowercase "wednesday" should still suggest "Wednesday". A
// word the dictionary already knows suggests nothing — there is nothing to
// correct.
//
// Cost is one pass over the word list per call: about 90k length comparisons,
// of which the few thousand that survive pay for a bounded distance matrix over
// two short words. That is a few milliseconds, which is affordable exactly once
// — when the panel opens — and is why nothing calls this per keystroke.
func (d *Dictionary) Suggest(word string, max int) []string {
	if max <= 0 {
		return nil
	}
	max = min(max, suggestMax)
	word = strings.ReplaceAll(word, "’", "'")
	if word == "" || d.Known(word) {
		return nil
	}
	out := d.candidates(word)
	if len(out) > max {
		out = out[:max]
	}
	words := make([]string, 0, len(out))
	for _, s := range out {
		words = append(words, matchCase(word, s.word))
	}
	return words
}

// candidates is every dictionary entry within reach of word, ranked best
// first — Suggest before its cap and its case matching. Split out so the
// identifier side (ident.go) can filter and re-rank the WHOLE reach: a cap
// applied first would hand it eight acronyms ("usr" reaches US, USA, USB…
// before "user"), leaving nothing to choose from.
//
// The scan is one pass over the word list; see Suggest for the cost.
func (d *Dictionary) candidates(word string) []suggestion {
	lower := strings.ToLower(word)
	q := []rune(lower)
	edits := suggestEdits(len(q))
	limit := edits * costEdit

	// The distance rows and the candidate's runes are allocated once and reused
	// across every candidate: the matrix is at most three rows of a dozen ints,
	// and allocating that ninety thousand times is more work than the arithmetic
	// it holds.
	buf := newEditBuf(len(q) + edits + 1)
	cand := make([]rune, 0, len(q)+edits+1)
	var out []suggestion
	for w := range d.words {
		// The cheap filter first, on bytes rather than runes. Reaching a word
		// `edits` mistakes away needs at least one insertion or deletion per
		// character of length difference, whatever those edits are weighted at
		// — so a candidate further than `edits` from the query in length cannot
		// qualify. Byte length only ever over-counts (a multi-byte candidate
		// passes a filter it might not have needed to), so nothing real is lost
		// by measuring it the cheap way.
		if diff := len(w) - len(lower); diff > edits || diff < -edits {
			continue
		}
		cand = cand[:0]
		for _, r := range w {
			cand = append(cand, unicode.ToLower(r))
		}
		cost := buf.cost(q, cand, limit)
		if cost > limit {
			continue
		}
		out = append(out, suggestion{word: w, cost: cost, prefix: commonPrefix(q, cand)})
	}
	rankSuggestions(out)
	return out
}

// rankSuggestions orders candidates best first: cheapest first, then the
// longest surviving prefix, then the closest in length, then alphabetically.
// See the package comment for why the last three are tie-breaks rather than
// judgements.
func rankSuggestions(s []suggestion) {
	sort.Slice(s, func(i, j int) bool {
		a, b := s[i], s[j]
		switch {
		case a.cost != b.cost:
			return a.cost < b.cost
		case a.prefix != b.prefix:
			return a.prefix > b.prefix
		case len(a.word) != len(b.word):
			return len(a.word) < len(b.word)
		}
		return a.word < b.word
	})
}

// matchCase returns fixed in the case typed: capitalized when the misspelling
// was, and otherwise exactly as the dictionary holds it — which is what lets a
// suggestion for "wendesday" arrive as "Wednesday" rather than being flattened
// to lowercase on the way in.
func matchCase(typed, fixed string) string {
	first, _ := utf8.DecodeRuneInString(typed)
	if !unicode.IsUpper(first) {
		return fixed
	}
	r, size := utf8.DecodeRuneInString(fixed)
	if r == utf8.RuneError || unicode.IsUpper(r) {
		return fixed
	}
	return string(unicode.ToUpper(r)) + fixed[size:]
}

// commonPrefix is how many leading runes a and b share.
func commonPrefix(a, b []rune) int {
	n := min(len(a), len(b))
	for i := range n {
		if a[i] != b[i] {
			return i
		}
	}
	return n
}

// indelCost is what it costs to insert or drop one rune — the apostrophe
// discount, and the whole of why "dont" suggests "don't" ahead of the eight
// three- and four-letter words one ordinary edit away from it.
func indelCost(r rune) int {
	if r == '\'' {
		return costApostrophe
	}
	return costEdit
}

// editBuf is the three rows a weighted optimal-string-alignment matrix needs,
// kept so a scan of the whole word list allocates none. Two rows would do for
// plain Levenshtein; the third is what the transposition rule reads back into.
type editBuf struct{ prev2, prev, cur []int }

func newEditBuf(n int) *editBuf {
	return &editBuf{prev2: make([]int, n), prev: make([]int, n), cur: make([]int, n)}
}

// cost is the weighted edit distance from a to b, computed no further than
// limit: the moment a whole row of the matrix is over the limit, no cell below
// it can come back under one (every step down adds at least zero and the row
// minimum never decreases), so the walk stops and answers limit+1. That early
// exit is what makes a scan of ninety thousand words cheap — the great majority
// of them fail on their second or third row.
//
// So any answer above limit means only "further than that", not a distance: a
// walk that aborts says limit+1 and a walk that runs to the end of a short word
// says whatever it really cost. Callers compare against limit and ask no more
// of the number than that.
//
// The alignment is OSA rather than full Damerau-Levenshtein: OSA forbids
// editing a substring it has already transposed, which makes it not quite a
// metric, and true Damerau costs an alphabet-sized table per call. The
// difference only shows on words three or more edits apart — further than
// anything is offered from here at all.
func (e *editBuf) cost(a, b []rune, limit int) int {
	la, lb := len(a), len(b)
	if la == 0 {
		return runsCost(b)
	}
	if lb == 0 {
		return runsCost(a)
	}
	// The matrix is walked a row per rune of a, and each row is as wide as b —
	// so the buffer, sized for the query, is indexed by the candidate and has
	// to be grown for one longer than the length filter expected.
	if len(e.cur) < lb+1 {
		*e = *newEditBuf(lb + 1)
	}
	prev2, prev, cur := e.prev2, e.prev, e.cur
	prev[0] = 0
	for j := 1; j <= lb; j++ {
		prev[j] = prev[j-1] + indelCost(b[j-1]) // the empty query: insert all of b
	}
	for i := 1; i <= la; i++ {
		cur[0] = prev[0] + indelCost(a[i-1]) // the empty candidate: drop all of a
		rowMin := cur[0]
		for j := 1; j <= lb; j++ {
			sub := costEdit
			if a[i-1] == b[j-1] {
				sub = 0
			}
			c := min(prev[j]+indelCost(a[i-1]), cur[j-1]+indelCost(b[j-1]))
			c = min(c, prev[j-1]+sub)
			if i > 1 && j > 1 && a[i-1] == b[j-2] && a[i-2] == b[j-1] {
				c = min(c, prev2[j-2]+costTranspose) // the two letters arrived swapped
			}
			cur[j] = c
			rowMin = min(rowMin, c)
		}
		if rowMin > limit {
			return limit + 1
		}
		prev2, prev, cur = prev, cur, prev2
	}
	// The last row written is prev, the rotation above having moved it there.
	e.prev2, e.prev, e.cur = prev2, prev, cur
	return prev[lb]
}

// runsCost is what dropping (or inserting) every rune of s costs — the answer
// when one of the two words is empty.
func runsCost(s []rune) int {
	total := 0
	for _, r := range s {
		total += indelCost(r)
	}
	return total
}
