// =============================================================================
// File: internal/finder/score.go
// Author: Spicer Matthews <spicer@cloudmanic.com>
// Created: 2026-04-30
// Copyright: 2026 Cloudmanic, LLC. All rights reserved.
// =============================================================================

// Package finder implements project-wide file search: an in-memory
// index of every non-ignored file under the project root, plus a
// fuzzy matcher that ranks paths against a typed query.
//
// The match algorithm is a small fzy-style fuzzy matcher: every
// query character must appear in the path in order (case-insensitive),
// then a positive integer score is computed from a handful of
// heuristics that VS Code's Cmd+P and similar finders use:
//
//   - Matches in the basename outrank matches in the directory.
//   - Consecutive runs of matched characters outrank scattered ones.
//   - Matches at word boundaries (`/`, `_`, `-`, `.`) are favoured
//     so "tab" matches "tab.go" before it matches "stable.go".
//   - Earlier match positions break ties.
//
// Above all of those sits one tier rule: a query that appears in the
// path as a CONTIGUOUS run (case-insensitive substring) always outranks
// one that only matches scattered. Typing "pobc_kahua_canada/prod.json"
// means that folder and file, not any path that happens to hold those
// letters in order — the fuzzy walk is the fallback for abbreviations
// ("apgo" → "app/go"), not the first answer.
//
// The matcher returns both the score and the matched rune indexes so
// the UI can highlight which characters of each result line up with
// the query — the same trick `fzf` uses to make results scannable.
package finder

import "unicode"

// Match is the result of scoring one path against a query. Score
// is positive for any hit (higher is better); MatchedIndexes lists
// the rune indexes inside the path that line up with the query —
// the renderer uses those to highlight matched characters.
type Match struct {
	Score          int
	MatchedIndexes []int
}

// scoring constants. Pulled out so a future tweak (e.g. weighting
// basename hits even harder) is one line, and so the comments below
// can refer to each by name. Magnitudes were tuned to make the
// "basename match wins" rule visible in test ranking — the smallest
// gap between two reasonable matches should still differ by at
// least one full bonus, not a 1-point fudge.
const (
	bonusBasename     = 15 // match falls inside the file's basename
	bonusWordBoundary = 20 // match starts a word (after / _ - .)
	bonusConsecutive  = 30 // match continues an unbroken run
	bonusFirstChar    = 10 // first matched char is at position 0
	penaltyGap        = 1  // each unmatched char between matches

	// maxRuneBonus is the most one matched rune can earn from the
	// bonuses above (first char + consecutive + basename + boundary).
	// contiguousFloor multiplies it by the query length, so it bounds
	// every scattered score for that query from above — see Score.
	maxRuneBonus = bonusFirstChar + bonusConsecutive + bonusBasename + bonusWordBoundary
)

// Why bonusConsecutive (30) > bonusWordBoundary (20): a tight run
// of three matched chars in a row (e.g. "abc" hitting "abc.go")
// must outrank the same query scattered across word boundaries
// (e.g. "abc" hitting "a_b_c.go"). Without this, "type the start
// of the basename" — the most common finder pattern — would lose
// to anything with underscores.

// Score returns (score, matchedIndexes) for query against path.
// score == 0 means "no match" — every query rune must appear in
// path in order (case-insensitive) to score above zero.
//
// Two passes, tiered:
//
//  1. Contiguous: every case-insensitive occurrence of the whole query
//     as a substring is scored with the usual heuristics and the best
//     one kept (earliest on a tie). Scoring each occurrence rather than
//     taking the first is what puts "app" on the basename of
//     "internal/app/app.go" instead of the directory. The result is
//     lifted by contiguousFloor so it outranks ANY scattered match.
//
//  2. Scattered (only when no substring exists): the greedy
//     left-to-right walk — for each query rune take the first matching
//     path rune not yet consumed. Faster than a full DP and good enough
//     for abbreviations; its weakness (grabbing an early stray letter,
//     e.g. the "po" of "reports" for "pobc…") is exactly the case pass
//     1 now catches before it gets here.
//
// Why a floor and not a bigger bonus: a scattered match across word
// boundaries can earn more per rune than a mid-word substring ("abc"
// over "a_b_c" collects three boundary bonuses), so no fixed bonus
// keeps the tiers apart for every query length. n×maxRuneBonus is the
// ceiling of any scattered score for an n-rune query, so floor+1 is a
// strict separation, and the tier stays comparable across paths
// because every path is scored against the same query.
func Score(query, path string) (int, []int) {
	if query == "" {
		// Empty query matches everything, with no highlighting and
		// a tiny score so the caller can keep results sorted by
		// some external order (typically alphabetical).
		return 1, nil
	}
	if path == "" {
		return 0, nil
	}

	pathRunes := []rune(path)
	queryRunes := []rune(query)
	basenameStart := lastSeparatorIndex(pathRunes) + 1

	if idx, s, ok := bestContiguous(queryRunes, pathRunes, basenameStart); ok {
		return contiguousFloor(len(queryRunes)) + s, idx
	}

	matchedIdx := make([]int, 0, len(queryRunes))

	// Greedy walk. Bail as soon as we know the query can't fit —
	// saves us scoring paths that already lost.
	pi := 0
	for qi := 0; qi < len(queryRunes); qi++ {
		want := unicode.ToLower(queryRunes[qi])
		found := false
		for pi < len(pathRunes) {
			got := unicode.ToLower(pathRunes[pi])
			if got == want {
				matchedIdx = append(matchedIdx, pi)
				pi++
				found = true
				break
			}
			pi++
		}
		if !found {
			return 0, nil
		}
	}

	// Floor at 1 — even a "bad" match (lots of gaps) should outrank
	// a non-match (zero) so the result still appears.
	score := scoreAlignment(pathRunes, matchedIdx, basenameStart)
	if score < 1 {
		score = 1
	}
	return score, matchedIdx
}

// contiguousFloor is the score every contiguous match of an n-rune
// query starts from: one above the highest score a scattered match of
// the same query can reach (n × maxRuneBonus; gaps only subtract).
func contiguousFloor(n int) int {
	return n*maxRuneBonus + 1
}

// bestContiguous finds the best-scoring case-insensitive occurrence of
// query as a substring of path. ok is false when there is none. Ties go
// to the earliest occurrence (strict > below) so the choice is stable.
//
// Rune-wise lowering (unicode.ToLower) never changes the rune count, so
// an index into the lowered slices is an index into pathRunes — which
// is what the highlighter needs.
func bestContiguous(query, path []rune, basenameStart int) (idx []int, score int, ok bool) {
	n := len(query)
	if n > len(path) {
		return nil, 0, false
	}
	lq := make([]rune, n)
	for i, r := range query {
		lq[i] = unicode.ToLower(r)
	}
	lp := make([]rune, len(path))
	for i, r := range path {
		lp[i] = unicode.ToLower(r)
	}

	cand := make([]int, n)
	for start := 0; start+n <= len(lp); start++ {
		hit := true
		for k := range n {
			if lp[start+k] != lq[k] {
				hit = false
				break
			}
		}
		if !hit {
			continue
		}
		for k := range cand {
			cand[k] = start + k
		}
		s := scoreAlignment(path, cand, basenameStart)
		if !ok || s > score {
			idx = append(idx[:0], cand...)
			score, ok = s, true
		}
	}
	return idx, score, ok
}

// scoreAlignment applies the per-rune heuristics (first char,
// consecutive run, basename, word boundary, gap penalty) to one set of
// matched indexes. Shared by both passes of Score so a contiguous and
// a scattered alignment are measured by the same rules — the tier
// floor is the only difference between them.
func scoreAlignment(pathRunes []rune, matchedIdx []int, basenameStart int) int {
	score := 0
	prevIdx := -2 // -2 so the first match never counts as consecutive
	for i, idx := range matchedIdx {
		if i == 0 && idx == 0 {
			score += bonusFirstChar
		}
		if idx == prevIdx+1 {
			score += bonusConsecutive
		}
		if idx >= basenameStart {
			score += bonusBasename
		}
		// Position 0 starts a word as surely as the rune after a "/"
		// does. Without that, a root-level "tab.go" (first-char bonus
		// only) lost to "internal/app/tabbar.go" (boundary after "/")
		// once both became contiguous hits for "tab".
		if idx == 0 || isWordBoundary(pathRunes[idx-1]) {
			score += bonusWordBoundary
		}
		gap := idx - (prevIdx + 1)
		if gap > 0 && i > 0 {
			score -= gap * penaltyGap
		}
		prevIdx = idx
	}
	return score
}

// lastSeparatorIndex returns the index of the last `/` (or `\\` on
// Windows-y paths) in r, or -1 when none. Pulled into a helper so
// the score loop reads as the rule it's enforcing rather than path
// arithmetic.
func lastSeparatorIndex(r []rune) int {
	for i := len(r) - 1; i >= 0; i-- {
		if r[i] == '/' || r[i] == '\\' {
			return i
		}
	}
	return -1
}

// isWordBoundary reports whether r is the kind of separator after
// which the *next* character is the start of a "word" — the cue we
// use to upweight `tab` matching `cmd/tab.go` over `command-table.go`.
func isWordBoundary(r rune) bool {
	switch r {
	case '/', '\\', '_', '-', '.', ' ':
		return true
	}
	return false
}
