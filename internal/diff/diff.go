// =============================================================================
// File: internal/diff/diff.go
// Author: Rohan Allison <rohanthewiz@gmail.com>
// Created: 2026-08-03
// Copyright: 2026 Rohan Allison. All rights reserved.
// =============================================================================

// Package diff is a line-oriented differ that emits unified-diff text.
//
// WHY NOT `git diff --no-index`. The comparison surface has to work on
// BUFFERS — the unsaved text you're looking at, a block you just pasted —
// and handing those to git means writing temp files, which is a lot of
// machinery to reach a program that may not be installed and that would
// refuse the job outside a repository anyway. The same argument the
// project search made against shelling out to ripgrep: the promise is
// one static binary that works when it lands. So the algorithm lives
// here, in Go, with no dependency and nothing to install.
//
// THE ALGORITHM IS PATIENCE DIFF, which is worth naming because the
// obvious alternative (Myers, or a full LCS table) is worse on both axes
// that matter here:
//
//   - Memory. An LCS table is O(n·m) — two 20k-line files would want
//     400M cells. Patience recurses on anchors and only falls back to a
//     table inside small regions.
//   - Readability. Patience anchors on lines that appear exactly ONCE on
//     each side, which in code means signatures, imports and braces with
//     distinct content. That's what stops the classic ugly diff where a
//     new function is reported as "change every closing brace".
//
// The shape:
//
//	trim common prefix/suffix
//	  └─ find lines unique on BOTH sides       → candidate anchors
//	       └─ longest increasing subsequence   → the anchors we keep
//	            └─ recurse between anchors
//	                 └─ no unique lines left?  → small: LCS table
//	                                             big:  replace wholesale
//
// Everything the panel needs comes out of Unified(), whose output is
// deliberately byte-compatible with `git diff` output: the app already
// has a color function (gitPanelDiffStyle) and a row→line mapper
// (diffTargetLine) for that format, and inventing a private one would
// have meant writing both again.
package diff

import (
	"strconv"
	"strings"
)

// Op names what happened to one line.
type Op int

const (
	// OpEqual — the line is present, unchanged, on both sides.
	OpEqual Op = iota
	// OpDelete — present on the old side only.
	OpDelete
	// OpInsert — present on the new side only.
	OpInsert
)

// Edit is one line of the edit script. OldLine / NewLine are 1-based
// line numbers on their respective sides, and 0 where the line doesn't
// exist on that side — which is what the hunk headers are counted from.
type Edit struct {
	Op      Op
	Text    string
	OldLine int
	NewLine int
}

// lcsCellBudget caps the fallback LCS table. A region bigger than this
// (in cells) is reported as a wholesale replace rather than diffed
// line-by-line: past that size the table costs more than the extra
// precision is worth, and a region with NO unique lines on either side
// is usually a block of boilerplate where a line-level diff reads as
// noise anyway. 250k cells is ~1MB of int16-ish state — comfortably
// inside a frame, and reached only by a 500×500 region.
const lcsCellBudget = 250_000

// Diff returns the edit script transforming a into b.
func Diff(a, b []string) []Edit {
	var out []Edit
	emit := func(e Edit) { out = append(out, e) }
	diffRange(a, b, 0, len(a), 0, len(b), emit)
	return out
}

// diffRange diffs a[a0:a1] against b[b0:b1], emitting edits in document
// order. Offsets are carried rather than sub-slicing so every emitted
// line already knows its number on both sides — the recursion would
// otherwise have to renumber on the way back up.
func diffRange(a, b []string, a0, a1, b0, b1 int, emit func(Edit)) {
	// Common prefix. Peeling it here (rather than only at the top level)
	// is what makes the recursion cheap: after an anchor match, the lines
	// either side of it usually agree for a while.
	for a0 < a1 && b0 < b1 && a[a0] == b[b0] {
		emit(Edit{Op: OpEqual, Text: a[a0], OldLine: a0 + 1, NewLine: b0 + 1})
		a0++
		b0++
	}
	// Common suffix — measured now, emitted after the middle. Counted
	// rather than collected: a long identical tail is the COMMON case
	// (one edit near the top of a file), and prepending each line to a
	// slice would make that quadratic.
	suffix := 0
	for a1 > a0 && b1 > b0 && a[a1-1] == b[b1-1] {
		a1--
		b1--
		suffix++
	}
	switch {
	case a0 == a1 && b0 == b1:
		// nothing in the middle
	case a0 == a1:
		emitRange(b, b0, b1, OpInsert, emit)
	case b0 == b1:
		emitRange(a, a0, a1, OpDelete, emit)
	default:
		anchors := uniqueAnchors(a, b, a0, a1, b0, b1)
		if len(anchors) == 0 {
			fallbackDiff(a, b, a0, a1, b0, b1, emit)
			break
		}
		// Walk the anchors, diffing the gaps between them recursively.
		// An anchor is by construction an equal line, so it's emitted
		// directly rather than re-derived.
		pa, pb := a0, b0
		for _, an := range anchors {
			diffRange(a, b, pa, an.ai, pb, an.bi, emit)
			emit(Edit{Op: OpEqual, Text: a[an.ai], OldLine: an.ai + 1, NewLine: an.bi + 1})
			pa, pb = an.ai+1, an.bi+1
		}
		diffRange(a, b, pa, a1, pb, b1, emit)
	}
	for i := 0; i < suffix; i++ {
		emit(Edit{Op: OpEqual, Text: a[a1+i], OldLine: a1 + i + 1, NewLine: b1 + i + 1})
	}
}

// emitRange emits a run of lines as a single-sided op.
func emitRange(lines []string, from, to int, op Op, emit func(Edit)) {
	for i := from; i < to; i++ {
		e := Edit{Op: op, Text: lines[i]}
		if op == OpDelete {
			e.OldLine = i + 1
		} else {
			e.NewLine = i + 1
		}
		emit(e)
	}
}

// anchor is one matched pair of unique lines.
type anchor struct{ ai, bi int }

// uniqueAnchors finds the lines appearing EXACTLY ONCE in both ranges,
// pairs them up, and returns the longest increasing subsequence of those
// pairs by b-index — the largest set of anchors that can all be kept
// without crossing, which is the heart of patience diff.
//
// Uniqueness is the whole trick: a line that appears once on each side
// is almost certainly the SAME line, whereas a repeated `}` matched by
// position is a coin flip that produces the ugly brace-shuffle diff.
func uniqueAnchors(a, b []string, a0, a1, b0, b1 int) []anchor {
	countA := make(map[string]int, a1-a0)
	posA := make(map[string]int, a1-a0)
	for i := a0; i < a1; i++ {
		countA[a[i]]++
		posA[a[i]] = i
	}
	countB := make(map[string]int, b1-b0)
	posB := make(map[string]int, b1-b0)
	for i := b0; i < b1; i++ {
		countB[b[i]]++
		posB[b[i]] = i
	}
	// Candidates in b order, so the LIS below is over a-indices.
	var cands []anchor
	for i := b0; i < b1; i++ {
		line := b[i]
		if countB[line] != 1 || countA[line] != 1 {
			continue
		}
		cands = append(cands, anchor{ai: posA[line], bi: i})
	}
	if len(cands) == 0 {
		return nil
	}
	return longestIncreasing(cands)
}

// longestIncreasing returns the longest subsequence of cands whose ai is
// strictly increasing (bi already is, by construction). Patience sorting:
// piles by tail value, binary search for the pile, back-pointers to
// reconstruct. O(n log n).
func longestIncreasing(cands []anchor) []anchor {
	if len(cands) == 0 {
		return nil
	}
	tails := make([]int, 0, len(cands)) // index into cands of each pile's top
	prev := make([]int, len(cands))
	for i := range prev {
		prev[i] = -1
	}
	for i, c := range cands {
		// First pile whose top is >= this a-index.
		lo, hi := 0, len(tails)
		for lo < hi {
			mid := (lo + hi) / 2
			if cands[tails[mid]].ai < c.ai {
				lo = mid + 1
			} else {
				hi = mid
			}
		}
		if lo > 0 {
			prev[i] = tails[lo-1]
		}
		if lo == len(tails) {
			tails = append(tails, i)
		} else {
			tails[lo] = i
		}
	}
	out := make([]anchor, len(tails))
	k := tails[len(tails)-1]
	for i := len(tails) - 1; i >= 0; i-- {
		out[i] = cands[k]
		k = prev[k]
	}
	return out
}

// fallbackDiff handles a region with no unique lines to anchor on. Small
// regions get a real LCS so the result is still minimal; anything past
// the budget is reported as delete-all-then-insert-all, which is both
// honest and what a human would say about a block that shares no
// distinguishing line with its counterpart.
func fallbackDiff(a, b []string, a0, a1, b0, b1 int, emit func(Edit)) {
	n, m := a1-a0, b1-b0
	if n*m > lcsCellBudget {
		emitRange(a, a0, a1, OpDelete, emit)
		emitRange(b, b0, b1, OpInsert, emit)
		return
	}
	// table[i][j] = LCS length of a[a0+i:a1] and b[b0+j:b1]. Built from
	// the end so the walk below can go forwards, which is what keeps the
	// emitted script in document order without a reversal step.
	table := make([][]int, n+1)
	for i := range table {
		table[i] = make([]int, m+1)
	}
	for i := n - 1; i >= 0; i-- {
		for j := m - 1; j >= 0; j-- {
			if a[a0+i] == b[b0+j] {
				table[i][j] = table[i+1][j+1] + 1
				continue
			}
			if table[i+1][j] >= table[i][j+1] {
				table[i][j] = table[i+1][j]
			} else {
				table[i][j] = table[i][j+1]
			}
		}
	}
	i, j := 0, 0
	for i < n && j < m {
		switch {
		case a[a0+i] == b[b0+j]:
			emit(Edit{Op: OpEqual, Text: a[a0+i], OldLine: a0 + i + 1, NewLine: b0 + j + 1})
			i++
			j++
		case table[i+1][j] >= table[i][j+1]:
			emit(Edit{Op: OpDelete, Text: a[a0+i], OldLine: a0 + i + 1})
			i++
		default:
			emit(Edit{Op: OpInsert, Text: b[b0+j], NewLine: b0 + j + 1})
			j++
		}
	}
	emitRange(a, a0+i, a1, OpDelete, emit)
	emitRange(b, b0+j, b1, OpInsert, emit)
}

// Stats counts the changed lines on each side — what a header says when
// it wants to summarise a comparison in six characters.
func Stats(edits []Edit) (added, removed int) {
	for _, e := range edits {
		switch e.Op {
		case OpInsert:
			added++
		case OpDelete:
			removed++
		}
	}
	return added, removed
}

// Unified renders edits as unified-diff text with `context` lines of
// context around each change, including the `--- a` / `+++ b` header
// pair. The output is the format `git diff` prints, on purpose: the app
// already colors it (gitPanelDiffStyle) and already maps a display row
// back to a file line (diffTargetLine), and a private format would have
// meant writing both of those a second time.
//
// Identical inputs render as an empty slice, not as a header with no
// hunks — the caller says "no differences" better than an empty box does.
func Unified(edits []Edit, labelA, labelB string, context int) []string {
	if context < 0 {
		context = 0
	}
	groups := hunkGroups(edits, context)
	if len(groups) == 0 {
		return nil
	}
	out := []string{"--- " + labelA, "+++ " + labelB}
	for _, g := range groups {
		oldStart, oldCount, newStart, newCount := hunkExtent(edits[g.from:g.to])
		out = append(out, "@@ -"+rangeSpec(oldStart, oldCount)+
			" +"+rangeSpec(newStart, newCount)+" @@")
		for _, e := range edits[g.from:g.to] {
			switch e.Op {
			case OpEqual:
				out = append(out, " "+e.Text)
			case OpDelete:
				out = append(out, "-"+e.Text)
			case OpInsert:
				out = append(out, "+"+e.Text)
			}
		}
	}
	return out
}

// span is a half-open [from, to) range of the edit script.
type span struct{ from, to int }

// hunkGroups slices the edit script into the runs a unified diff prints:
// every changed line, plus up to `context` unchanged lines either side,
// with overlapping runs merged so two nearby changes read as one hunk
// rather than repeating the lines between them.
func hunkGroups(edits []Edit, context int) []span {
	var groups []span
	for i := 0; i < len(edits); i++ {
		if edits[i].Op == OpEqual {
			continue
		}
		// Walk to the end of this change, allowing up to 2*context
		// unchanged lines to bridge into the next one — bridging is what
		// merges neighbouring hunks instead of emitting a header, some
		// context, then the same context again under a new header.
		j := i
		for k := i; k < len(edits); k++ {
			if edits[k].Op != OpEqual {
				j = k
				continue
			}
			if k-j > 2*context {
				break
			}
		}
		from := i - context
		if from < 0 {
			from = 0
		}
		to := j + context + 1
		if to > len(edits) {
			to = len(edits)
		}
		if n := len(groups); n > 0 && from <= groups[n-1].to {
			groups[n-1].to = to
		} else {
			groups = append(groups, span{from: from, to: to})
		}
		i = j
	}
	return groups
}

// hunkExtent computes a hunk's @@ header numbers from the lines it
// carries. A side with no lines at all reports start 0, which is what
// git prints for a pure insertion or deletion.
func hunkExtent(lines []Edit) (oldStart, oldCount, newStart, newCount int) {
	for _, e := range lines {
		if e.OldLine > 0 {
			if oldStart == 0 {
				oldStart = e.OldLine
			}
			oldCount++
		}
		if e.NewLine > 0 {
			if newStart == 0 {
				newStart = e.NewLine
			}
			newCount++
		}
	}
	return oldStart, oldCount, newStart, newCount
}

// rangeSpec formats one side of an @@ header. git omits the count when
// it is exactly 1, and this matches that so the output diffs cleanly
// against real git output in tests and in the user's eye.
func rangeSpec(start, count int) string {
	if count == 1 {
		return strconv.Itoa(start)
	}
	return strconv.Itoa(start) + "," + strconv.Itoa(count)
}

// SplitLines cuts text into the []string this package diffs. A trailing
// newline does NOT produce a final empty line — a file ending in "\n" has
// as many lines as it has newlines, and counting a phantom one would
// report an edit on every comparison between a file and a buffer.
func SplitLines(text string) []string {
	if text == "" {
		return nil
	}
	text = strings.TrimSuffix(text, "\n")
	return strings.Split(text, "\n")
}
