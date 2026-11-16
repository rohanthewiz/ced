// =============================================================================
// File: internal/spell/ident.go
// Author: Rohan Allison <rohanthewiz@gmail.com>
// Created: 2026-11-16
// Copyright: 2026 Rohan Allison. All rights reserved.
// =============================================================================

// ident.go decides which parts of an IDENTIFIER are words, and asks the
// dictionary about those. A struct-tag name is an identifier written in
// one of half a dozen conventions — userName, user_name, user-name,
// UserName, USER_NAME, username — and a checker that looked the whole
// thing up would flag every one of them. So the name is cut into pieces,
// and each piece is either looked up or deliberately left alone.
//
//	"userIDsByHTTPServer_v2"
//	 user│IDs│By│HTTP│Server│_│v│2
//	 ✓    ·   ·  ·    ✓            ·  (· = skipped, ✓ = looked up)
//
// The rules err towards SILENCE, as cats-todo's prose rules do: a typo
// that goes unflagged costs a glance, a convention flagged on every struct
// costs the feature. So a piece is skipped, not checked, when it is:
//
//   - one or two letters (id, db, ok, v) — abbreviations outnumber typos
//     at that length and no dictionary settles the difference;
//   - an acronym: all capitals (HTTP, URL), or capitals plus a plural s
//     (IDs, URLs) — Go's initialism style puts these everywhere;
//   - letters touching a digit (utf8, sha256, ipv4, oauth2, s3) — a
//     version or an algorithm name, never a word to correct;
//   - letters touching a non-ASCII rune — a name or another language; the
//     list is English.
//
// One exception to the acronym rule: in a name with NO lowercase letter
// at all (SCREAMING_SNAKE, the env-var convention), capitals are the only
// way to write a word, so pieces of five letters or more are checked —
// "DATABSE" in DATABSE_URL is a typo, while "URL" and "KEY" stay acronyms.
// Five because the common acronyms (API, URL, HTTP, JSON, UUID) are four
// or fewer.
//
// # Run-together names
//
// "createdat", "firstname" and "apikey" are not words, but they are not
// typos either — a convention some APIs use. A piece the list does not
// know is accepted when it splits into known words (compound, below),
// under the two-tier rule of the package comment: each part a list word of
// four letters or more, or a trusted word of any length. That rule is what
// keeps "pasword" (pas+word) and "existant" (exist+ant) flagged while
// "createdat" (created+at, "at" trusted) passes.
package spell

import (
	"slices"
	"sort"
	"strings"
	"unicode"
)

// Span is one piece of a checked identifier the dictionary did not know,
// as rune offsets [Start, End) into that identifier. Rune offsets because
// the editor addresses its buffer in runes; the caller adds the
// identifier's own column to place it on a line.
type Span struct {
	Start, End int
}

// maxCompoundParts bounds how many known words a run-together piece may be
// made of. Real names run to three ("lastloginat"); past four, almost any
// string of letters can be tiled by short trusted words, and acceptance
// would stop meaning anything.
const maxCompoundParts = 4

// minCompoundWord is the shortest LIST word that may be a compound part.
// See the package comment: SCOWL's three-letter words (pas, con, sep, rec,
// com, ant) are exactly the fragments that let a typo split into "words".
const minCompoundWord = 4

// CheckIdent returns the pieces of name that are words the dictionary does
// not know, in order. Pieces the splitter skips (see the file comment) are
// never reported, so an empty result means "nothing worth flagging", not
// "every piece was looked up".
//
// It keeps no state and does no allocation-heavy work, so calling it for
// every visible tag on every frame is fine — a tag name is a handful of
// map lookups.
func (d *Dictionary) CheckIdent(name string) []Span {
	rs := []rune(name)
	var out []Span
	for _, p := range identPieces(rs) {
		if !d.knownPiece(string(rs[p.Start:p.End])) {
			out = append(out, p)
		}
	}
	return out
}

// knownPiece is the lookup for one piece: spelled as listed (case-lenient,
// see Known), or a run-together compound of known words.
func (d *Dictionary) knownPiece(w string) bool {
	return d.Known(w) || d.compound(strings.ToLower(w))
}

// compound reports whether w — lowercase, already known NOT to be a word
// — can be tiled by two to maxCompoundParts acceptable parts.
//
// A word-break DP over prefix lengths: parts[i] is the fewest parts that
// tile w[:i] (anything over the cap means "not reachable"). Pieces are
// short (a tag word is rarely 20 letters), so the O(n²) inner loop is a
// few hundred map lookups at worst, and only for pieces that already
// failed the plain lookup.
//
// A word with a letter tripled ("messsage") is never accepted: it splits
// into "mess" + "sage" perfectly well, and a triple letter is a stuck key
// in every English word that is not a proper noun.
func (d *Dictionary) compound(w string) bool {
	n := len(w)
	if n < 2 || hasTripleLetter(w) {
		return false
	}
	parts := make([]int, n+1)
	for i := range parts {
		parts[i] = maxCompoundParts + 1
	}
	parts[0] = 0
	for i := range n {
		if parts[i] >= maxCompoundParts {
			continue
		}
		for j := i + 1; j <= n; j++ {
			// The whole word as one "part" is the lookup that already
			// failed; a compound has at least two.
			if i == 0 && j == n {
				continue
			}
			if parts[i]+1 < parts[j] && d.compoundPart(w[i:j]) {
				parts[j] = parts[i] + 1
			}
		}
	}
	return parts[n] <= maxCompoundParts
}

// compoundPart is the two-tier rule for one part of a run-together name: a
// trusted word at any length, or a list word of at least minCompoundWord
// letters.
func (d *Dictionary) compoundPart(seg string) bool {
	if d.isTrusted(seg) {
		return true
	}
	return len(seg) >= minCompoundWord && d.has(seg)
}

// identPieces splits an identifier into the pieces worth looking up,
// applying every skip rule in the file comment. The result is in order and
// the pieces never overlap.
func identPieces(rs []rune) []Span {
	screaming := !hasLower(rs)
	var out []Span
	for i := 0; i < len(rs); {
		if !isASCIILetter(rs[i]) {
			i++
			continue
		}
		j := i
		for j < len(rs) && isASCIILetter(rs[j]) {
			j++
		}
		// The run [i, j) of letters, cut at its camelCase boundaries. A
		// PIECE that touches a digit or a non-ASCII rune is skipped: it is
		// a version, an algorithm, a name — not a word to correct. Judged
		// per piece rather than per run, so "lenghtUtf8" still flags
		// "lenght" while "Utf" (touching the 8) is left alone.
		for _, p := range camelPieces(rs, i, j) {
			touching := (p.Start > 0 && touchesWord(rs[p.Start-1])) ||
				(p.End < len(rs) && touchesWord(rs[p.End]))
			if !touching && checkablePiece(rs[p.Start:p.End], screaming) {
				out = append(out, p)
			}
		}
		i = j
	}
	return out
}

// camelPieces cuts the letter run rs[a:b] at its camelCase boundaries:
//
//	userName     → user | Name        (lower → Upper)
//	HTTPServer   → HTTP | Server      (an acronym running into a word:
//	                                   the last capital starts the word)
//	userIDs      → user | IDs         (…unless what follows is the
//	URLsByHost   → URLs | By | Host    acronym's own plural s)
func camelPieces(rs []rune, a, b int) []Span {
	var out []Span
	start := a
	for k := a + 1; k < b; k++ {
		prev, cur := rs[k-1], rs[k]
		cut := false
		switch {
		case isLower(prev) && isUpper(cur):
			cut = true
		case isUpper(prev) && isUpper(cur) && k+1 < b && isLower(rs[k+1]):
			// The plural of an acronym is an s followed by the end of the
			// run or the next word's capital. Anything else ("HTTPServer")
			// is the acronym ending and a word beginning at cur.
			plural := rs[k+1] == 's' && (k+2 == b || isUpper(rs[k+2]))
			cut = !plural
		}
		if cut {
			out = append(out, Span{Start: start, End: k})
			start = k
		}
	}
	return append(out, Span{Start: start, End: b})
}

// checkablePiece applies the per-piece skip rules: too short, or an
// acronym (with the SCREAMING_SNAKE exception — see the file comment).
func checkablePiece(p []rune, screaming bool) bool {
	n := len(p)
	if n <= 2 {
		return false
	}
	if isAcronym(p) {
		return screaming && allUpper(p) && n >= 5
	}
	return true
}

// isAcronym reports whether a piece is all capitals, or all capitals and a
// trailing plural s ("IDs").
func isAcronym(p []rune) bool {
	if allUpper(p) {
		return true
	}
	n := len(p)
	return n >= 3 && p[n-1] == 's' && allUpper(p[:n-1])
}

// SuggestPiece returns up to max corrections for one flagged piece, best
// first, each re-cased to the piece's shape (Recase) so it can be dropped
// straight back into the identifier — "Nmae" in userNmae offers "Name",
// not "name".
//
// Two sources, in this order:
//
//  1. single-word corrections of the whole piece — the dictionary's
//     candidates (Suggest's reach and cost), filtered and re-ranked for
//     identifiers by identRank;
//  2. for a run-together piece, repairs of ONE half: "frstname" has no
//     single word within two edits, but "name" is known and "frst" is
//     one edit from "first". Only reached when (1) left room.
//
// Every candidate must itself pass the check, so the picker never offers
// a fix that would still be underlined.
func (d *Dictionary) SuggestPiece(piece string, max int) []string {
	if max <= 0 || piece == "" {
		return nil
	}
	max = min(max, suggestMax)
	lower := strings.ToLower(piece)
	seen := map[string]bool{}
	var out []string
	add := func(w string) bool {
		if !isASCIIWord(w) || !d.knownPiece(w) {
			return false
		}
		r := Recase(piece, w)
		if seen[r] {
			return false
		}
		seen[r] = true
		out = append(out, r)
		return len(out) >= max
	}
	for _, w := range identRank(lower, d.candidates(lower)) {
		if add(w) {
			return out
		}
	}
	for _, w := range d.halfRepairs(lower) {
		if add(w) {
			return out
		}
	}
	return out
}

// halfRepairWidth is how many corrections each corrected half of a
// run-together piece contributes (see halfRepairs).
const halfRepairWidth = 3

// halfRepairs proposes corrections for a run-together piece by keeping one
// side of a split that is already a good compound part and correcting the
// other side the same way SuggestPiece corrects a whole piece. Splits are
// tried left to right; each corrected side contributes its halfRepairWidth
// best spellings.
// Sides shorter than three letters are not corrected — every short string
// is two edits from dozens of words.
func (d *Dictionary) halfRepairs(w string) []string {
	var out []string
	for k := 3; k+3 <= len(w); k++ {
		left, right := w[:k], w[k:]
		switch {
		case d.compoundPart(right) && !d.Known(left):
			for _, s := range firstN(identRank(left, d.candidates(left)), halfRepairWidth) {
				out = append(out, s+right)
			}
		case d.compoundPart(left) && !d.Known(right):
			for _, s := range firstN(identRank(right, d.candidates(right)), halfRepairWidth) {
				out = append(out, left+s)
			}
		}
	}
	return out
}

// identRank filters and re-orders the dictionary's candidates for an
// identifier piece (candidates, suggest.go — the whole reach, uncapped).
//
// Filtered out, because none of them is a fix for a tag name:
//
//   - entries the list holds capitalized — proper nouns and acronyms
//     ("Severn", "USA"); lowercased into a tag they are just noise;
//   - anything that is not plain ASCII letters ("don't");
//   - words under three letters — a piece that short is never flagged,
//     so offering one is offering a different name, not a spelling;
//   - the typed word itself.
//
// Re-ordered: Suggest's weighted cost stays the first key — a
// transposition ("teh" → "the") still beats any one-letter edit — but
// within a cost, a candidate that CONTAINS the typed letters in order
// ranks first: the typist dropped a letter. Suggest's own tie-break (the
// longest shared prefix) is right for prose and wrong here, where dropped
// letters are the common case — "usr" is one edit from "use" and "user",
// "frst" from "frat" and "first", and prefix order puts the wrong one of
// each first. The sort is stable, so Suggest's order is the last key.
func identRank(typed string, cands []suggestion) []string {
	q := []rune(strings.ToLower(typed))
	type ranked struct {
		word string
		cost int
		sup  bool
	}
	var rs []ranked
	for _, c := range cands {
		if !isASCIIWord(c.word) || !isLower(rune(c.word[0])) || len(c.word) < 3 || c.word == typed {
			continue
		}
		rs = append(rs, ranked{word: c.word, cost: c.cost, sup: isSubsequence(q, []rune(c.word))})
	}
	sort.SliceStable(rs, func(i, j int) bool {
		if rs[i].cost != rs[j].cost {
			return rs[i].cost < rs[j].cost
		}
		return rs[i].sup && !rs[j].sup
	})
	out := make([]string, len(rs))
	for i, r := range rs {
		out[i] = r.word
	}
	return out
}

// isSubsequence reports whether every rune of q appears in w in order —
// w is q with letters inserted.
func isSubsequence(q, w []rune) bool {
	i := 0
	for _, r := range w {
		if i < len(q) && q[i] == r {
			i++
		}
	}
	return i == len(q)
}

// firstN returns at most n leading elements of s.
func firstN(s []string, n int) []string {
	return s[:min(n, len(s))]
}

// Recase spells word in model's case shape — the shape a piece of an
// identifier has, which is the convention's and not the word's:
//
//	model "NMAE" → "NAME"   (SCREAMING; more than one letter, all capitals)
//	model "Nmae" → "Name"   (a camelCase or PascalCase word)
//	model "nmae" → "name"   (everything else)
func Recase(model, word string) string {
	mr := []rune(model)
	if len(mr) == 0 || word == "" {
		return word
	}
	lower := strings.ToLower(word)
	switch {
	case len(mr) > 1 && allUpper(mr):
		return strings.ToUpper(word)
	case isUpper(mr[0]):
		wr := []rune(lower)
		wr[0] = unicode.ToUpper(wr[0])
		return string(wr)
	}
	return lower
}

// touchesWord reports whether a rune next to a letter run disqualifies the
// run: a digit, or a letter outside ASCII. Separators (_ - . and the like)
// do not.
func touchesWord(r rune) bool {
	return (r >= '0' && r <= '9') || r > unicode.MaxASCII
}

// hasLower reports whether rs holds any lowercase ASCII letter — false for
// a SCREAMING_SNAKE name.
func hasLower(rs []rune) bool {
	return slices.ContainsFunc(rs, isLower)
}

// allUpper reports whether every rune of p is an uppercase ASCII letter.
func allUpper(p []rune) bool {
	return len(p) > 0 && !slices.ContainsFunc(p, func(r rune) bool { return !isUpper(r) })
}

// hasTripleLetter reports whether w repeats one letter three times in a
// row.
func hasTripleLetter(w string) bool {
	for i := 2; i < len(w); i++ {
		if w[i] == w[i-1] && w[i] == w[i-2] {
			return true
		}
	}
	return false
}

// isASCIIWord reports whether w is one or more ASCII letters and nothing
// else — what a correction must be to sit inside an identifier.
func isASCIIWord(w string) bool {
	if w == "" {
		return false
	}
	for _, r := range w {
		if !isASCIILetter(r) {
			return false
		}
	}
	return true
}

// isASCIILetter reports whether r is a-z or A-Z. ASCII only on purpose:
// the list is English, and unicode.IsLetter would let "é" into a piece.
func isASCIILetter(r rune) bool { return isLower(r) || isUpper(r) }

// isLower reports whether r is an ASCII lowercase letter.
func isLower(r rune) bool { return r >= 'a' && r <= 'z' }

// isUpper reports whether r is an ASCII uppercase letter.
func isUpper(r rune) bool { return r >= 'A' && r <= 'Z' }
