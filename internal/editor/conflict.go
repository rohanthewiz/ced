// =============================================================================
// File: internal/editor/conflict.go
// Author: Rohan Allison <rohanthewiz@gmail.com>
// Created: 2026-10-03
// Copyright: 2026 Rohan Allison. All rights reserved.
// =============================================================================

// conflict.go is the buffer-level model of git's CONFLICT MARKERS: it
// finds the blocks a stopped merge / cherry-pick / revert / rebase wrote
// into a file, and it rewrites one block (or all of them) as a single
// undoable step. Everything about git itself — which operation is in
// progress, which files are unmerged, staging the result — lives in the
// app package; this file only knows what the text looks like.
//
// The shape git writes, with the optional diff3 / zdiff3 base section:
//
//	<<<<<<< HEAD                     ← Start   (CurrentLabel = "HEAD")
//	current side                       "ours": the branch you are on
//	||||||| parent of 2a0370b (…)    ← Base    (-1 when absent)
//	common ancestor                    only with merge.conflictStyle=diff3
//	=======                          ← Mid
//	incoming side                      "theirs": the commit being applied
//	>>>>>>> 2a0370b (t1 change b)    ← End     (IncomingLabel)
//
// Four decisions worth knowing:
//
//   - THE GRAMMAR IS STRICT. A marker is exactly seven marker characters
//     at the start of a line, followed by end-of-line or a space (and the
//     separator is exactly seven `=` and nothing else). That is what git
//     writes with the default conflict-marker-size, and strictness is what
//     keeps a Markdown setext underline (`=======` under a heading) or a
//     `<<<<<<<<` in a test fixture from being mistaken for a conflict. A
//     block must also CLOSE, in order: an opener with no `=======` and
//     `>>>>>>>` after it is not a conflict, it is text, and the scan
//     resumes on the line after it.
//   - "CURRENT" AND "INCOMING", NOT "OURS" AND "THEIRS". In a rebase git
//     swaps the meanings of ours/theirs (HEAD is the upstream being
//     replayed onto), so the two git words describe the opposite sides
//     depending on the operation. The positional words do not lie in any
//     operation: current is above the separator, incoming below — the
//     vocabulary VS Code taught a generation of users for the same reason.
//   - PARSED ON DEMAND, MEMOIZED BY REVISION. Conflicts() is asked by the
//     decoration source every frame, by the lens hit-test, by the menu
//     predicates; scanning a file per ask would be a per-frame O(file)
//     cost. One scan per EditRev is the bracket matcher's and the markdown
//     cache's rule, and an edit is the only thing that can move a marker.
//   - ONE UNDO STEP PER GESTURE. "Accept incoming" is one thought, so it
//     undoes in one hop — and "accept all incoming" in a file of twelve
//     conflicts is still one thought. Both follow ApplyMultiEdit's rules:
//     one structural snapshot up front, then direct Buffer edits applied
//     bottom-up so every block still waiting keeps its line numbers.

package editor

import "strings"

// conflictMarkerLen is git's default conflict-marker-size. A repository
// can change it per path with a gitattribute, which is rare enough that
// matching only the default is the right trade: a looser match would
// start finding "conflicts" in ordinary files, and that failure is
// louder than the one it avoids.
const conflictMarkerLen = 7

// ConflictBlock is one conflict region, by buffer LINE index. Base is -1
// for the plain two-way style; every other field is always set for a
// block the parser returns.
type ConflictBlock struct {
	Start int // the `<<<<<<<` line
	Base  int // the `|||||||` line, or -1
	Mid   int // the `=======` line
	End   int // the `>>>>>>>` line

	// The text after each marker, trimmed — "HEAD", "2a0370b (t1 change
	// b)", "parent of …". Shown by the UI to say which side is which; never
	// used to decide anything.
	CurrentLabel  string
	BaseLabel     string
	IncomingLabel string
}

// HasBase reports whether the block carries a diff3 common-ancestor
// section — the one case where "accept base" means something.
func (b ConflictBlock) HasBase() bool { return b.Base >= 0 }

// currentEnd is the line just past the current side: the base marker
// when there is one, the separator otherwise.
func (b ConflictBlock) currentEnd() int {
	if b.Base >= 0 {
		return b.Base
	}
	return b.Mid
}

// CurrentLines returns the current ("ours") side's lines.
func (b ConflictBlock) CurrentLines(lines []string) []string {
	return sliceLines(lines, b.Start+1, b.currentEnd())
}

// BaseLines returns the common-ancestor section, empty without diff3.
func (b ConflictBlock) BaseLines(lines []string) []string {
	if b.Base < 0 {
		return nil
	}
	return sliceLines(lines, b.Base+1, b.Mid)
}

// IncomingLines returns the incoming ("theirs") side's lines.
func (b ConflictBlock) IncomingLines(lines []string) []string {
	return sliceLines(lines, b.Mid+1, b.End)
}

// sliceLines copies lines[from:to], clamped. A copy because the caller
// will splice the result back into the same backing array it came from.
func sliceLines(lines []string, from, to int) []string {
	if from < 0 {
		from = 0
	}
	if to > len(lines) {
		to = len(lines)
	}
	if from >= to {
		return nil
	}
	out := make([]string, to-from)
	copy(out, lines[from:to])
	return out
}

// Contains reports whether buffer line `line` is inside the block,
// markers included.
func (b ConflictBlock) Contains(line int) bool { return line >= b.Start && line <= b.End }

// ConflictRegion names which part of a block a line belongs to — what the
// decoration source tints it as.
type ConflictRegion int

const (
	RegionNone        ConflictRegion = iota
	RegionStartMarker                // `<<<<<<< …`
	RegionCurrent                    // the current side's body
	RegionBaseMarker                 // `||||||| …`
	RegionBase                       // the common ancestor's body
	RegionMidMarker                  // `=======`
	RegionIncoming                   // the incoming side's body
	RegionEndMarker                  // `>>>>>>> …`
)

// RegionOf classifies buffer line `line` against the block.
func (b ConflictBlock) RegionOf(line int) ConflictRegion {
	switch {
	case !b.Contains(line):
		return RegionNone
	case line == b.Start:
		return RegionStartMarker
	case line == b.End:
		return RegionEndMarker
	case line == b.Mid:
		return RegionMidMarker
	case b.Base >= 0 && line == b.Base:
		return RegionBaseMarker
	case line < b.currentEnd():
		return RegionCurrent
	case b.Base >= 0 && line < b.Mid:
		return RegionBase
	default:
		return RegionIncoming
	}
}

// ConflictChoice is one way to settle a block.
type ConflictChoice int

const (
	// ChooseCurrent keeps the current side — the branch you are on.
	ChooseCurrent ConflictChoice = iota
	// ChooseIncoming keeps the incoming side — the commit being applied.
	ChooseIncoming
	// ChooseBoth keeps both, current first: the common answer when two
	// people added different things at the same spot (two imports, two
	// cases in a switch).
	ChooseBoth
	// ChooseBothIncomingFirst keeps both, incoming first — the same
	// answer when order matters and the incoming lines belong above.
	ChooseBothIncomingFirst
	// ChooseBase keeps the common ancestor, discarding both edits. Only
	// meaningful for a diff3 block; refused otherwise.
	ChooseBase
	// ChooseNeither drops the whole block — both sides added something
	// and neither survives.
	ChooseNeither
)

// Label is the choice's human name, shared by the lens, the context
// menu and the pickers so every door names a verb the same way.
func (c ConflictChoice) Label() string {
	switch c {
	case ChooseCurrent:
		return "Accept current"
	case ChooseIncoming:
		return "Accept incoming"
	case ChooseBoth:
		return "Accept both"
	case ChooseBothIncomingFirst:
		return "Accept both, incoming first"
	case ChooseBase:
		return "Accept base"
	case ChooseNeither:
		return "Accept neither"
	}
	return "Resolve"
}

// Resolution returns the lines a block becomes under choice c, and false
// for a choice the block cannot answer (ChooseBase without a base
// section).
func (b ConflictBlock) Resolution(lines []string, c ConflictChoice) ([]string, bool) {
	cur, inc := b.CurrentLines(lines), b.IncomingLines(lines)
	switch c {
	case ChooseCurrent:
		return cur, true
	case ChooseIncoming:
		return inc, true
	case ChooseBoth:
		return append(cur, inc...), true
	case ChooseBothIncomingFirst:
		return append(inc, cur...), true
	case ChooseBase:
		if !b.HasBase() {
			return nil, false
		}
		return b.BaseLines(lines), true
	case ChooseNeither:
		return nil, true
	}
	return nil, false
}

// markerTail reports whether line opens with exactly conflictMarkerLen
// copies of ch followed by end-of-line or a space, and returns the
// trimmed text after the marker (the label).
func markerTail(line string, ch byte) (label string, ok bool) {
	if len(line) < conflictMarkerLen {
		return "", false
	}
	for i := 0; i < conflictMarkerLen; i++ {
		if line[i] != ch {
			return "", false
		}
	}
	rest := line[conflictMarkerLen:]
	if rest == "" {
		return "", true
	}
	// The space requirement is what rejects an 8-character run: git never
	// glues a label to the marker, so `<<<<<<<<` is content.
	if rest[0] != ' ' {
		return "", false
	}
	return strings.TrimSpace(rest), true
}

// isMidMarker reports whether line is the `=======` separator. Stricter
// than the other markers — exactly seven `=` and NOTHING after, not even
// a label — because a run of `=` is ordinary Markdown (a setext heading
// underline) and the separator is the marker most likely to collide.
func isMidMarker(line string) bool {
	return line == "======="
}

// ParseConflicts scans lines for well-formed conflict blocks, in order.
// Malformed openers (no closing `>>>>>>>`, markers out of order) are not
// blocks: the scan moves on from the line after them, so one stray
// `<<<<<<<` in a fixture cannot swallow a real block further down.
func ParseConflicts(lines []string) []ConflictBlock {
	var out []ConflictBlock
	for i := 0; i < len(lines); i++ {
		label, ok := markerTail(lines[i], '<')
		if !ok {
			continue
		}
		if b, ok := parseConflictAt(lines, i, label); ok {
			out = append(out, b)
			i = b.End // resume after the block; the loop's i++ steps past it
		}
	}
	return out
}

// parseConflictAt tries to read one block whose opener is lines[start].
//
// The walk is a three-state machine — current side, base side, incoming
// side — and it bails on the first marker that arrives out of turn. A
// second `<<<<<<<` inside the block (nested markers from a recursive
// merge, or a broken file) ends the attempt: the outer opener is then
// reported as text and the scan picks the inner one up as a block of
// its own, which is the most useful reading of a malformed file.
func parseConflictAt(lines []string, start int, currentLabel string) (ConflictBlock, bool) {
	b := ConflictBlock{Start: start, Base: -1, Mid: -1, End: -1, CurrentLabel: currentLabel}
	for j := start + 1; j < len(lines); j++ {
		line := lines[j]
		if _, ok := markerTail(line, '<'); ok {
			return ConflictBlock{}, false
		}
		if b.Mid < 0 {
			if label, ok := markerTail(line, '|'); ok {
				if b.Base >= 0 {
					return ConflictBlock{}, false // two base sections
				}
				b.Base, b.BaseLabel = j, label
				continue
			}
			if isMidMarker(line) {
				b.Mid = j
				continue
			}
			if _, ok := markerTail(line, '>'); ok {
				return ConflictBlock{}, false // closed before the separator
			}
			continue
		}
		if label, ok := markerTail(line, '>'); ok {
			b.End, b.IncomingLabel = j, label
			return b, true
		}
		if isMidMarker(line) {
			return ConflictBlock{}, false // a second separator
		}
	}
	return ConflictBlock{}, false
}

// conflictCache memoizes ParseConflicts against the revision it scanned.
type conflictCache struct {
	rev    int
	valid  bool
	blocks []ConflictBlock
}

// Conflicts returns the tab's conflict blocks as of its current revision,
// scanning at most once per EditRev. The slice is shared — callers read
// it, never write it.
func (t *Tab) Conflicts() []ConflictBlock {
	if t == nil || t.Buffer == nil || t.IsImage() {
		return nil
	}
	if t.conflictScan.valid && t.conflictScan.rev == t.EditRev {
		return t.conflictScan.blocks
	}
	t.conflictScan = conflictCache{rev: t.EditRev, valid: true, blocks: ParseConflicts(t.Buffer.Lines)}
	return t.conflictScan.blocks
}

// ConflictAt returns the index of the block containing buffer line
// `line`, markers included.
func (t *Tab) ConflictAt(line int) (int, bool) {
	for i, b := range t.Conflicts() {
		if b.Contains(line) {
			return i, true
		}
		if b.Start > line {
			break // blocks are in document order
		}
	}
	return -1, false
}

// NextConflict returns the index of the first block starting after line
// `from` (dir > 0) or the last block starting before it (dir < 0). When
// the caret is INSIDE a block, "previous" means the one before that
// block, not the block itself — the same strictness stepProblem has,
// because landing back where you stand reads as a dead key.
func (t *Tab) NextConflict(from, dir int) (int, bool) {
	blocks := t.Conflicts()
	if dir > 0 {
		for i, b := range blocks {
			if b.Start > from {
				return i, true
			}
		}
		return -1, false
	}
	// A block ENDING above the caret is wholly behind it; a block that
	// merely starts above it may be the one the caret is standing in.
	for i := len(blocks) - 1; i >= 0; i-- {
		if blocks[i].End < from {
			return i, true
		}
	}
	return -1, false
}

// ResolveConflict settles block idx with choice c as one undo step and
// parks the caret at the first line of what replaced it. Returns false
// (buffer untouched, no snapshot) for an index out of range or a choice
// the block cannot answer.
func (t *Tab) ResolveConflict(idx int, c ConflictChoice) bool {
	blocks := t.Conflicts()
	if idx < 0 || idx >= len(blocks) {
		return false
	}
	b := blocks[idx]
	repl, ok := b.Resolution(t.Buffer.Lines, c)
	if !ok {
		return false
	}
	t.pushUndo(undoGroupStructural)
	t.spliceLines(b.Start, b.End, repl)
	t.afterConflictEdit(Position{Line: b.Start})
	return true
}

// ResolveAllConflicts settles every block with choice c as ONE undo step
// and reports how many it rewrote. Blocks that cannot answer the choice
// (ChooseBase on a two-way block) are left in place and not counted, so
// "accept base" over a mixed file does what it can and says how much.
func (t *Tab) ResolveAllConflicts(c ConflictChoice) int {
	blocks := t.Conflicts()
	type plan struct {
		b    ConflictBlock
		repl []string
	}
	var plans []plan
	for _, b := range blocks {
		if repl, ok := b.Resolution(t.Buffer.Lines, c); ok {
			plans = append(plans, plan{b, repl})
		}
	}
	if len(plans) == 0 {
		return 0
	}
	t.pushUndo(undoGroupStructural)
	// Bottom-up: a splice only moves lines BELOW it, so every block still
	// waiting above keeps the line numbers the scan gave it.
	for i := len(plans) - 1; i >= 0; i-- {
		t.spliceLines(plans[i].b.Start, plans[i].b.End, plans[i].repl)
	}
	t.afterConflictEdit(Position{Line: plans[0].b.Start})
	return len(plans)
}

// spliceLines replaces buffer lines [first, last] with repl. Records no
// history — the callers above file one snapshot for the whole gesture.
// A buffer is never left with zero lines (the Buffer invariant every
// cursor clamp relies on): dropping a block that was the whole file
// leaves one empty line, which is what deleting all text does too.
func (t *Tab) spliceLines(first, last int, repl []string) {
	lines := t.Buffer.Lines
	if first < 0 || last >= len(lines) || first > last {
		return
	}
	out := make([]string, 0, len(lines)-(last-first+1)+len(repl))
	out = append(out, lines[:first]...)
	out = append(out, repl...)
	out = append(out, lines[last+1:]...)
	if len(out) == 0 {
		out = []string{""}
	}
	t.Buffer.Lines = out
}

// afterConflictEdit is the shared tail of both resolvers — ApplyMultiEdit's
// tail, for its reasons: carets and selection were measured against text
// that moved, the edit is structural (InvalidateStyles, not a grid patch),
// and the caret lands where the conflict was so the reader sees what the
// choice produced.
func (t *Tab) afterConflictEdit(at Position) {
	t.Carets = nil
	t.Dirty = true
	t.EditRev++
	t.InvalidateStyles()
	t.Cursor = t.Buffer.Clamp(at)
	t.Anchor = t.Cursor
	t.cursorMoved = true
}
