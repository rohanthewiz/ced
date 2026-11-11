// =============================================================================
// File: internal/editor/undo.go
// Author: Rohan Allison <rohanthewiz@gmail.com>
// Created: 2026-04-30
// Copyright: 2026 Rohan Allison. All rights reserved.
// Portions copyright 2026 Cloudmanic, LLC. Original author: Spicer Matthews.
// =============================================================================

// undo.go gives every Tab a per-tab undo / redo history plus a one-shot
// "revert to original" hatch.
//
// EVERY ENTRY IS A DELTA, NEVER A COPY OF THE BUFFER (N-131). An entry
// (undoStep) holds the runs of lines one step replaced — where each run
// starts, the lines it took out, the lines it put in — plus the caret
// before and after. Memory and the budget's accounting are both
// O(change): typing a word costs the same few hundred bytes in a
// 100-line file and in a 400k-line one.
//
// The model this replaced stored a full []string header copy of the
// buffer per entry (16 bytes × lines) and charged it to the budget,
// so a big file's history collapsed to a handful of steps (50k lines →
// 41, 400k → 5), and its line-by-line cost compare charged every
// SHIFTED line after an insert near the top — 61 steps survived edits
// at the top of a 6,000-line file against 330 at the end.
//
// HOW A STEP IS RECORDED. The mutators keep their old shape: call
// pushUndo BEFORE changing the buffer, then change it any way they like
// (single-line patches, slice rebuilds, a whole new Buffer). Nothing is
// told what changed. Instead the step is diffed out afterwards, lazily:
//
//	undoBase ── the buffer as of the last history checkpoint (a private
//	            header copy: ONE per tab, not one per step)
//
//	pushUndo        settle the step still open, then OPEN a new one:
//	                remember the caret; undoBase already equals the buffer
//	(mutations)     the buffer drifts away from undoBase
//	next pushUndo / settle: lineHunks(undoBase, buffer) → the step's
//	Undo / Redo     hunks; push it; undoBase := buffer
//
// Diffing rather than hooking every primitive is the bookmarks' choice
// (bookmark.go) for the same reason: a mutator can't forget to report a
// change it made, and a new one needs no undo code beyond pushUndo.
// Settling also catches a change NO pushUndo announced: it is filed as
// a step of its own (and drops redo, like any new edit), so the history
// can never apply a delta to text it doesn't describe.
//
// lineHunks is cheap where it runs most. Unchanged lines share their
// string data with undoBase, so the common-prefix / common-suffix trim is
// a pointer compare per line; what's left is the edit itself. See
// lineHunks for the scattered cases (multi-caret, Replace all, a
// formatter run).
//
// THE CAP IS BYTES AND ENTRIES. maxUndoBytes bounds what the two stacks
// keep alive; maxUndoEntries bounds the depth. With deltas the entry cap
// is what binds in practice — the byte budget now only bites when the
// steps themselves are large (a multi-megabyte paste, edits to a file of
// very long lines). See stepCost for what an entry is charged.
//
// Coalescing — every typed rune doesn't get its own entry. Consecutive
// inserts of the same kind (typing, backspace, delete) inside a 500ms
// window collapse into a single undo step, so undoing a 50-character
// word is one click, not 50. Anything structural — pasting, hitting
// Enter, deleting a selection, an explicit cursor move — closes the
// current group so the next mutation starts a fresh entry. A coalesced
// push simply leaves the open step open: its delta is measured once,
// when it settles, over the whole burst.

package editor

import (
	"time"

	"github.com/rohanthewiz/ced/internal/diff"
)

// maxUndoEntries caps the per-tab stack depth. 500 is generous enough
// that typical editing sessions never hit the wall; once we do, the
// oldest entry is dropped (FIFO) so the user can still undo a long
// way back without unbounded memory growth. The open step (see
// pushUndo) counts toward it, so UndoDepth never reads more than this.
const maxUndoEntries = 500

// maxUndoBytes is the per-tab budget for the undo and redo stacks
// combined. Entries are deltas, so ordinary editing never comes near it
// — 500 typing steps cost tens of kilobytes on any file size. What it
// still catches is a history of genuinely large steps: repeated
// multi-megabyte pastes, or edits to a file of very long lines (minified
// JS, a data blob), where every step strands a multi-megabyte string
// nothing else references.
//
// The two stacks share one budget because entries MOVE between them:
// Undo pops one side and pushes the other, so budgeting them separately
// would let the total drift to twice the number either one allows.
const maxUndoBytes = 32 << 20

// lineHeaderBytes is the size of a string header (data pointer + length)
// on a 64-bit build — what every line a hunk names costs in its slice,
// whether or not the characters are shared. Hardcoded rather than taken
// from unsafe.Sizeof so this file needs no unsafe import; on a 32-bit
// build it over-counts by 2x, which errs toward a smaller history rather
// than a larger one.
const lineHeaderBytes = 16

// undoStepOverhead is a round figure for the undoStep struct itself —
// four Positions, two line counts, the hunk slice header and the cost —
// so a step that changed nothing (a cursor-only entry) still costs
// something and a pathological stream of them is bounded by bytes too.
const undoStepOverhead = 112

// undoHunkOverhead is one undoHunk: its start line and two slice headers.
const undoHunkOverhead = 56

// undoDiffMin is the size above which a changed middle region whose
// lines no longer pair up index by index is handed to the patience
// differ (internal/diff) to be split into real hunks. Below it, one hunk
// spanning the region costs at most this many lines of headers more than
// the exact answer — not worth a diff's maps. See lineHunks.
const undoDiffMin = 32

// undoCoalesceWindow is the inactivity gap that closes a coalescing
// group. 500ms feels right — pause-and-think between words almost
// always lasts longer than this, while a typing burst lands well inside.
const undoCoalesceWindow = 500 * time.Millisecond

// undoGroup tags each step with the kind of operation that produced
// it so consecutive ops of the same kind can be merged. Anything labelled
// undoGroupStructural never coalesces — it's the explicit "this is its
// own undo step" signal.
type undoGroup int

const (
	undoGroupNone       undoGroup = iota // no recent op — next push always lands.
	undoGroupTyping                      // a printable rune was inserted.
	undoGroupBackspace                   // a single char was removed before the cursor.
	undoGroupDelete                      // a single char was removed after the cursor.
	undoGroupStructural                  // paste, Enter, delete-selection, etc.
	undoGroupLineMove                    // Alt-Up/Down line shifts — a nudge burst is one step.
)

// snapshot is a whole buffer state: lines plus cursor / anchor. History
// entries are NOT snapshots any more (see undoStep); the one snapshot a
// tab keeps is undoOriginal, the on-open text RevertFile rewinds to. We
// don't include scroll position — undo is about *what* was in the
// buffer, not where the user happened to be looking — but cursor +
// anchor are part of the user's state.
type snapshot struct {
	Lines  []string
	Cursor Position
	Anchor Position
}

// undoHunk is one contiguous run of lines a step replaced.
//
// at is the run's first line in the BEFORE state. Hunks within a step
// are sorted by at and never overlap; the after-state position of a hunk
// is at plus the net line change of the hunks above it, which
// applyHunks / hunksFit compute as they walk rather than storing a
// second number that could disagree.
//
// removed and inserted are private copies of the slices (never sub-slices
// of the buffer or undoBase, which are patched in place), though the
// strings in them share their characters with whatever else holds the
// same line — that is what makes stepCost's accounting work.
type undoHunk struct {
	at       int
	removed  []string // the before-state's lines at [at, at+len(removed))
	inserted []string // the after-state's lines that replace them
}

// undoStep is one entry on the undo or redo stack: the delta from a
// BEFORE state to an AFTER state, and the caret at both ends.
//
// The same value moves between the stacks unchanged: Undo applies it
// backwards (after → before) and files it on redo; Redo applies it
// forwards and files it back on undo. Only cost is re-stamped, because
// which side's text the entry alone keeps alive depends on which stack
// it sits on (stepCost).
type undoStep struct {
	hunks []undoHunk

	// The caret where the step began (restored by Undo) and where it
	// ended (restored by Redo).
	cursorBefore, anchorBefore Position
	cursorAfter, anchorAfter   Position

	// beforeLen / afterLen are the buffer's line counts at either end.
	// hunksFit checks them before a step is applied — the cheap half of
	// "is the buffer really the state this delta starts from?".
	beforeLen, afterLen int

	// cost is stepCost's estimate of the memory this entry keeps alive
	// on the stack it sits on, stamped when it is pushed there. Stored
	// rather than recomputed so eviction is O(1).
	cost int
}

// stepCost estimates how much memory keeping s on a history stack
// actually costs. onRedo selects which stack: the answer differs, and
// the difference is the whole idea.
//
// Every hunk pays for its two slices' headers (lineHeaderBytes per line
// named) plus fixed overheads. Then the CHARACTERS: a string is charged
// to exactly one entry — the one that alone keeps it alive.
//
//   - On the UNDO stack that is the REMOVED text. A string a step put in
//     is either still in the buffer (free — the buffer holds it anyway)
//     or was taken out again by a later step, which charges it as its
//     removed text. A string a step took out is held by nothing but that
//     step (and, at most, the entry below that once inserted it, which
//     isn't charged for it).
//   - On the REDO stack it flips: Undo put the removed text back in the
//     buffer, and the INSERTED text is now what only this entry holds.
//
// Charging both sides would count every string of a typing chain twice
// ("ab" is step 1's inserted and step 2's removed) and halve the history
// for nothing. The estimate can still over-count — undoOriginal and
// savedLines may also hold a removed line — which errs toward a shorter
// history, never a longer one.
func stepCost(s undoStep, onRedo bool) int {
	cost := undoStepOverhead
	for _, h := range s.hunks {
		cost += undoHunkOverhead + (len(h.removed)+len(h.inserted))*lineHeaderBytes
		held := h.removed
		if onRedo {
			held = h.inserted
		}
		for _, ln := range held {
			cost += len(ln)
		}
	}
	return cost
}

// lineHunks returns the hunks that turn before into after.
//
//	before  a a a X X X b b        prefix / suffix trimmed by == :
//	after   a a a Y Y b b          a pointer compare per untouched line
//	              └─┬─┘
//	            the middle — what the step actually touched
//
// The middle is then split into hunks three ways, cheapest first:
//
//  1. SAME LINE COUNT on both sides — typing, Replace all, a rename,
//     multi-caret typing, a comment toggle. Lines still pair up index by
//     index, so one walk emits a hunk per run of differing lines. Exact,
//     no allocation beyond the hunks.
//  2. One side is SMALL (≤ undoDiffMin) — Enter, a paste, a deleted
//     block. One hunk spanning the middle is exact or near enough.
//  3. Both sides LARGE with a shifted count — lines added in one place
//     and removed in another (multi-caret Enter, a formatter run,
//     resolving every conflict block). Index pairing breaks after the
//     first shift, so one span would name every line in between; the
//     patience differ (internal/diff, the compare panel's own — never a
//     second algorithm) finds the real runs.
//
// Case 1 falls through to 3 when more than half its middle "differs":
// equal counts can still hide a shift (a line added at the top and one
// removed at the bottom), and an index walk over a shifted region would
// report nearly every line as changed.
//
// Equality is by content (Go's string ==), so lines a whole-buffer
// rewrite re-created with the same text (ReloadAsEdit's formatter
// output) correctly count as unchanged.
func lineHunks(before, after []string) []undoHunk {
	p := 0
	for p < len(before) && p < len(after) && before[p] == after[p] {
		p++
	}
	b1, a1 := len(before), len(after)
	for b1 > p && a1 > p && before[b1-1] == after[a1-1] {
		b1--
		a1--
	}
	oldMid, newMid := before[p:b1], after[p:a1]
	if len(oldMid) == 0 && len(newMid) == 0 {
		return nil
	}
	if len(oldMid) == len(newMid) {
		differing := 0
		for i := range oldMid {
			if oldMid[i] != newMid[i] {
				differing++
			}
		}
		if len(oldMid) <= undoDiffMin || differing*2 <= len(oldMid) {
			return alignedHunks(p, oldMid, newMid)
		}
	}
	if len(oldMid) <= undoDiffMin || len(newMid) <= undoDiffMin {
		return []undoHunk{{at: p, removed: cloneLines(oldMid), inserted: cloneLines(newMid)}}
	}
	return diffHunks(p, oldMid, newMid)
}

// alignedHunks walks two equal-length line slices index by index and
// emits one hunk per maximal run of differing lines. base is the line
// number of a[0] in the full before state.
func alignedHunks(base int, a, b []string) []undoHunk {
	var hunks []undoHunk
	for i := 0; i < len(a); {
		if a[i] == b[i] {
			i++
			continue
		}
		j := i
		for j < len(a) && a[j] != b[j] {
			j++
		}
		hunks = append(hunks, undoHunk{at: base + i, removed: cloneLines(a[i:j]), inserted: cloneLines(b[i:j])})
		i = j
	}
	return hunks
}

// diffHunks splits a shifted middle region into hunks with the patience
// differ. Each maximal run of non-equal edits becomes one hunk; the
// counters walk both sides so the hunk's lines are sliced from a and b
// themselves (the very strings, sharing their characters with the buffer
// and undoBase) rather than taken from Edit.Text.
func diffHunks(base int, a, b []string) []undoHunk {
	edits := diff.Diff(a, b)
	var hunks []undoHunk
	ai, bi := 0, 0
	for k := 0; k < len(edits); {
		if edits[k].Op == diff.OpEqual {
			ai++
			bi++
			k++
			continue
		}
		a0, b0 := ai, bi
		for k < len(edits) && edits[k].Op != diff.OpEqual {
			if edits[k].Op == diff.OpDelete {
				ai++
			} else {
				bi++
			}
			k++
		}
		hunks = append(hunks, undoHunk{at: base + a0, removed: cloneLines(a[a0:ai]), inserted: cloneLines(b[b0:bi])})
	}
	return hunks
}

// cloneLines copies a slice of lines into a fresh array, so a hunk never
// shares storage with the buffer or undoBase (both are patched in place).
// nil for an empty run: a pure insertion removes nothing.
func cloneLines(lines []string) []string {
	if len(lines) == 0 {
		return nil
	}
	return append([]string(nil), lines...)
}

// hunksFit reports whether lines is exactly the state s starts from in
// the given direction — forward (Redo: before → after) or backward
// (Undo: after → before) — by checking the line count and every line the
// step is about to replace. O(change). The settle invariant (undoBase
// always equals the buffer when a step is applied) makes this a
// should-never-fail check; it is here because the failure it would catch
// is silent text corruption, and refusing is always better than that.
func hunksFit(lines []string, s undoStep, forward bool) bool {
	want := s.afterLen
	if forward {
		want = s.beforeLen
	}
	if len(lines) != want {
		return false
	}
	shift := 0
	for _, h := range s.hunks {
		from, at := h.inserted, h.at+shift
		if forward {
			from, at = h.removed, h.at
		}
		if at < 0 || at+len(from) > len(lines) {
			return false
		}
		for i, ln := range from {
			if lines[at+i] != ln {
				return false
			}
		}
		shift += len(h.inserted) - len(h.removed)
	}
	return true
}

// applyHunks applies hunks to lines forward (before → after) or
// backward (after → before) and returns the result.
//
// When no hunk changes the line count — typing, Replace all, a comment
// toggle — every hunk is patched IN PLACE: O(change), and the at of each
// hunk is valid on both sides since nothing above it shifted. Otherwise
// the slice is rebuilt once, top to bottom, into a fresh array. lines
// may be patched in place, so callers pass only arrays they own (the
// buffer's, or undoBase).
func applyHunks(lines []string, hunks []undoHunk, forward bool) []string {
	sameShape := true
	grow := 0
	for _, h := range hunks {
		if len(h.removed) != len(h.inserted) {
			sameShape = false
		}
		grow += len(h.inserted) - len(h.removed)
	}
	if !forward {
		grow = -grow
	}
	if sameShape {
		for _, h := range hunks {
			to := h.inserted
			if !forward {
				to = h.removed
			}
			copy(lines[h.at:], to)
		}
		return lines
	}
	out := make([]string, 0, len(lines)+grow)
	pos, shift := 0, 0
	for _, h := range hunks {
		from, to, at := h.removed, h.inserted, h.at
		if !forward {
			// Backward, the hunk sits at its AFTER-state line: shifted by
			// everything the hunks above it added or removed.
			from, to, at = h.inserted, h.removed, h.at+shift
		}
		out = append(out, lines[pos:at]...)
		out = append(out, to...)
		pos = at + len(from)
		shift += len(h.inserted) - len(h.removed)
	}
	return append(out, lines[pos:]...)
}

// bufferLines is the live buffer's line slice, nil for a tab without one
// (an image tab), so the history code never has to nil-check Buffer.
func (t *Tab) bufferLines() []string {
	if t.Buffer == nil {
		return nil
	}
	return t.Buffer.Lines
}

// setBufferLines installs lines as the buffer's. A buffer-less tab only
// gains a Buffer when there is text to put in it: an empty step undone
// on an image tab must not conjure a zero-line buffer, which nothing
// else in the editor expects.
func (t *Tab) setBufferLines(lines []string) {
	if t.Buffer == nil {
		if len(lines) == 0 {
			return
		}
		t.Buffer = &Buffer{}
	}
	t.Buffer.Lines = lines
}

// rebase makes undoBase a copy of the buffer — the "last checkpoint"
// every settle diffs against. It reuses undoBase's own array (it is
// private to this file, never aliased), so a checkpoint after the first
// is a memmove of headers, not an allocation.
func (t *Tab) rebase() {
	t.undoBase = append(t.undoBase[:0], t.bufferLines()...)
}

// captureSnapshot returns a copy of the current buffer state — lines
// (a fresh header array) plus cursor / anchor. Used for undoOriginal and
// by ReloadUndoable to hold the user's text across a reload.
func (t *Tab) captureSnapshot() snapshot {
	if t.Buffer == nil {
		return snapshot{Cursor: t.Cursor, Anchor: t.Anchor}
	}
	lines := make([]string, len(t.Buffer.Lines))
	copy(lines, t.Buffer.Lines)
	return snapshot{Lines: lines, Cursor: t.Cursor, Anchor: t.Anchor}
}

// applySnapshot replaces the buffer with s. It also re-copies the lines
// so the live buffer doesn't share storage with the snapshot —
// otherwise the next edit would silently rewrite undoOriginal.
func (t *Tab) applySnapshot(s snapshot) {
	lines := make([]string, len(s.Lines))
	copy(lines, s.Lines)
	if t.Buffer == nil {
		t.Buffer = NewBuffer("")
	}
	t.Buffer.Lines = lines
	t.Cursor = s.Cursor
	t.Anchor = s.Anchor
	t.afterHistoryEdit()
}

// afterHistoryEdit is the bookkeeping every history move (Undo, Redo,
// Revert) owes after rewriting the buffer. Secondary carets were
// positioned against a buffer state we just threw away — keeping them
// would leave the next keystroke editing a line that has moved. The
// primary was restored by the caller, so the user still lands where the
// step happened. Styles are invalidated (the structural default) and the
// edit revision bumped like any other mutation.
func (t *Tab) afterHistoryEdit() {
	t.Carets = nil
	t.cursorMoved = true
	t.InvalidateStyles()
	t.EditRev++
}

// initUndo seeds the original-state snapshot used by RevertFile and
// resets the history. Called from NewTab and Reload — both moments where
// the buffer is now "what's on disk" and any prior history is
// meaningless.
func (t *Tab) initUndo() {
	t.undoOriginal = t.captureSnapshot()
	t.undoStack = nil
	t.redoStack = nil
	t.undoBytes = 0
	t.redoBytes = 0
	t.undoOpen = false
	t.lastUndoGroup = undoGroupNone
	// undoBase must be its OWN array — rebase and settle patch it in
	// place — so it is never shared with undoOriginal.
	t.rebase()
	// Both callers just read the file, so the on-open text IS the saved
	// text. Sharing the original's slice costs nothing: snapshots are
	// never written in place (applySnapshot copies out of them), so the
	// two can't drift through aliasing.
	t.savedLines = t.undoOriginal.Lines
	t.savedKnown = true
}

// markSaved records the current buffer as what the file on disk holds.
// Called wherever the two are known to agree after a write or a read that
// is NOT a fresh baseline: Save, and ReloadAsEdit adopting ced's own
// rewrite. A header copy, because Buffer.Lines is rewritten in place by
// some primitives (MoveLines) — aliasing it would let the next edit
// silently move the baseline along with the buffer.
func (t *Tab) markSaved() {
	if t.Buffer == nil {
		t.savedLines, t.savedKnown = nil, true
		return
	}
	lines := make([]string, len(t.Buffer.Lines))
	copy(lines, t.Buffer.Lines)
	t.savedLines, t.savedKnown = lines, true
}

// ForgetSavedText records that the file on disk no longer holds any text
// this tab knows — it was deleted, ced's formatter rewrote it under a
// dirty buffer, or the user chose to keep their edits over someone else's
// write. Until the next Save or reload, every Undo / Redo / Revert leaves
// the tab dirty: no buffer state is "what's on disk", so none may let the
// editor quit without asking.
func (t *Tab) ForgetSavedText() {
	t.savedLines, t.savedKnown = nil, false
}

// matchesSaved reports whether the buffer holds exactly the text the
// file on disk is known to hold. A string compare per line, but every
// line the edits never touched shares its data pointer with the baseline,
// so the common case is a pointer check — the same cost CanRevert pays.
func (t *Tab) matchesSaved() bool {
	if !t.savedKnown || t.Buffer == nil {
		return false
	}
	if len(t.Buffer.Lines) != len(t.savedLines) {
		return false
	}
	for i, ln := range t.Buffer.Lines {
		if ln != t.savedLines[i] {
			return false
		}
	}
	return true
}

// pushUndoEntry files s on the undo stack, stamps and charges its cost,
// and evicts from the bottom until the tab is back inside its limits.
// The single write path for the undo stack — settleUndo, Redo, and
// ReloadUndoable all go through it so none of them can forget the
// bookkeeping.
func (t *Tab) pushUndoEntry(s undoStep) {
	s.cost = stepCost(s, false)
	t.undoStack = append(t.undoStack, s)
	t.undoBytes += s.cost
	t.trimUndo()
}

// pushRedoEntry is pushUndoEntry's twin for the redo stack (only Undo
// files there). The redo stack is never trimmed itself — its entries are
// the steps just undone, and dropping one would break the chain Redo
// walks — but its bytes count against the shared budget, so the undo
// stack is trimmed to make room.
func (t *Tab) pushRedoEntry(s undoStep) {
	s.cost = stepCost(s, true)
	t.redoStack = append(t.redoStack, s)
	t.redoBytes += s.cost
	t.trimUndo()
}

// dropRedo throws the redo stack away and releases its bytes. Any new
// edit does this: replaying an undone step over text it was never
// measured against would be a branched timeline at best and corruption
// at worst.
func (t *Tab) dropRedo() {
	t.redoStack = nil
	t.redoBytes = 0
}

// trimUndo drops the oldest undo entries until the history fits both
// caps. The depth counted is UndoDepth's — the open step included — so
// the entry cap means the same thing to every reader. The last settled
// entry is never evicted: "undo the thing I just did" has to keep
// working even for a single step big enough to blow the whole budget on
// its own (a multi-megabyte paste), and a history of zero is
// indistinguishable from undo being broken.
func (t *Tab) trimUndo() {
	for len(t.undoStack) > 1 &&
		(t.UndoDepth() > maxUndoEntries || t.undoBytes+t.redoBytes > maxUndoBytes) {
		t.undoBytes -= t.undoStack[0].cost
		// Keep the original snapshot intact — it lives in undoOriginal,
		// not the stack, so RevertFile is unaffected by any of this.
		// Zeroed before re-slicing so the evicted hunks' strings are
		// released now rather than when append next reallocates.
		t.undoStack[0] = undoStep{}
		t.undoStack = t.undoStack[1:]
	}
	if t.undoBytes < 0 {
		t.undoBytes = 0
	}
}

// settleUndo closes the open step: it diffs undoBase (the buffer at the
// last checkpoint) against the buffer and files the result, then makes
// the buffer the new checkpoint. Every history operation calls it first
// — pushUndo before opening the next step, Undo / Redo before moving —
// so the stacks are always complete when they're read.
//
// With no step open it is normally a no-op: undoBase already equals the
// buffer, which the prefix trim confirms with a pointer compare per
// line. If the buffer changed anyway — a mutation that never called
// pushUndo — that change is filed as a step of its own and redo is
// dropped, exactly as if it had announced itself. Nothing in ced is
// known to do that; settling is what guarantees the history only ever
// holds deltas measured against the text they start from.
func (t *Tab) settleUndo() {
	cur := t.bufferLines()
	hunks := lineHunks(t.undoBase, cur)
	if !t.undoOpen && len(hunks) == 0 {
		return
	}
	s := undoStep{
		hunks:       hunks,
		cursorAfter: t.Cursor, anchorAfter: t.Anchor,
		beforeLen: len(t.undoBase), afterLen: len(cur),
	}
	if t.undoOpen {
		s.cursorBefore, s.anchorBefore = t.undoOpenCursor, t.undoOpenAnchor
	} else {
		// An unannounced change: nothing recorded where the caret was
		// before it, and the current caret is the best available answer.
		s.cursorBefore, s.anchorBefore = t.Cursor, t.Anchor
		t.dropRedo()
	}
	t.undoOpen = false
	t.pushUndoEntry(s)
	if len(hunks) > 0 {
		t.rebase()
	}
}

// pushUndo marks the start of a step: the caller is about to mutate the
// buffer and wants the mutation undoable. It settles whatever step was
// still open, then opens a new one over the current text — the delta
// itself is measured later (settleUndo), once the step is over. group
// selects coalescing behavior — a typing push within undoCoalesceWindow
// of another typing push is a no-op that leaves the current step open,
// while undoGroupStructural always starts a new entry. Any new edit
// invalidates the redo stack.
func (t *Tab) pushUndo(group undoGroup) {
	// A multi-caret fan-out already opened one step for the whole burst
	// (applyAtCarets); the per-caret primitives running inside it must
	// not each open another, or undoing a five-caret keystroke would take
	// five presses.
	if t.undoSuppress {
		return
	}
	if t.canCoalesce(group) {
		t.lastUndoAt = time.Now() // extend the window
		return
	}
	t.settleUndo()
	t.undoOpen = true
	t.undoOpenCursor, t.undoOpenAnchor = t.Cursor, t.Anchor
	t.dropRedo()
	// The open step counts toward the entry cap, so opening one can be
	// what pushes the oldest settled step out.
	t.trimUndo()
	t.lastUndoGroup = group
	t.lastUndoAt = time.Now()
}

// canCoalesce reports whether a pending push of the given group should
// be skipped because it would collapse into the previous one.
func (t *Tab) canCoalesce(group undoGroup) bool {
	if t.lastUndoGroup == undoGroupNone || group == undoGroupStructural {
		return false
	}
	if t.lastUndoGroup != group {
		return false
	}
	return time.Since(t.lastUndoAt) <= undoCoalesceWindow
}

// breakUndoGroup closes the current coalescing window so the next
// content-changing op starts a fresh entry. Called from cursor-moving
// methods (the user moved the caret, so the next typing burst is
// clearly a separate intent) and from Save (a save is a natural
// "logical step" boundary). The open step stays open — it settles when
// the next step is pushed, or on Undo.
func (t *Tab) breakUndoGroup() {
	t.lastUndoGroup = undoGroupNone
}

// CanUndo reports whether there is anything to roll back. The action
// menu uses this to enable / disable the Undo row.
func (t *Tab) CanUndo() bool {
	return len(t.undoStack) > 0 || t.undoOpen
}

// UndoDepth reports how many steps deep the undo history currently is:
// the settled entries plus the open step, if any — i.e. how many Undo
// presses it would take to reach the bottom.
//
// It exists for the cross-file edit journal (app/workspaceedit.go), which
// has to answer "is the step I filed still the one Undo would pop?"
// before it offers to unwind a whole rename as one gesture. EditRev alone
// can't answer it — trimUndo evicts from the BOTTOM, so a stack can shrink
// without the buffer changing at all — and depth alone can't either, since
// a push plus an eviction nets to zero. The journal checks both, which is
// why this is the only thing about the stack that's exported. Settling
// never moves it: it turns the open step into a settled one, one for one.
func (t *Tab) UndoDepth() int {
	if t.undoOpen {
		return len(t.undoStack) + 1
	}
	return len(t.undoStack)
}

// CanRedo reports whether a previously undone change can be re-applied.
func (t *Tab) CanRedo() bool {
	return len(t.redoStack) > 0
}

// CanRevert reports whether the buffer differs from the original state
// captured at NewTab / Reload time. Used to gate the Revert menu row.
func (t *Tab) CanRevert() bool {
	if t.Buffer == nil {
		return false
	}
	if len(t.Buffer.Lines) != len(t.undoOriginal.Lines) {
		return true
	}
	for i, ln := range t.Buffer.Lines {
		if ln != t.undoOriginal.Lines[i] {
			return true
		}
	}
	return false
}

// dropUndoHistory empties both stacks and re-bases on the buffer as it
// stands. The answer to a step that doesn't fit the buffer (hunksFit):
// every entry below it was measured against the same lost state, so none
// of them can be trusted either. undoOriginal and the saved baseline are
// whole snapshots, not deltas, and survive.
func (t *Tab) dropUndoHistory() {
	t.undoStack = nil
	t.redoStack = nil
	t.undoBytes = 0
	t.redoBytes = 0
	t.undoOpen = false
	t.rebase()
	t.breakUndoGroup()
}

// Undo rolls the most recent step back. Returns true when a step was
// actually undone — false lets the caller flash a "nothing to undo"
// message. The step moves to the redo stack so a subsequent Redo can
// replay it forward.
func (t *Tab) Undo() bool {
	t.settleUndo()
	n := len(t.undoStack)
	if n == 0 {
		return false
	}
	s := t.undoStack[n-1]
	if !hunksFit(t.bufferLines(), s, false) {
		t.dropUndoHistory()
		return false
	}
	t.undoStack[n-1] = undoStep{}
	t.undoStack = t.undoStack[:n-1]
	t.undoBytes -= s.cost

	t.setBufferLines(applyHunks(t.bufferLines(), s.hunks, false))
	t.rebase()
	t.Cursor = t.clampHistoryPos(s.cursorBefore)
	t.Anchor = t.clampHistoryPos(s.anchorBefore)
	t.pushRedoEntry(s)
	t.afterHistoryEdit()
	// Against the SAVED text, never the on-open original: once a save has
	// moved the disk on, undoing back to the opened text must read dirty
	// (the file says something else), and undoing back to the saved text
	// must read clean.
	t.Dirty = !t.matchesSaved()
	t.breakUndoGroup()
	return true
}

// Redo re-applies a step that was just undone. Returns true when a step
// was actually redone.
func (t *Tab) Redo() bool {
	// Settling first: if the buffer changed since the last Undo without
	// announcing itself, that change is filed as a new edit and redo is
	// dropped, so the step below is never replayed over text it doesn't
	// describe.
	t.settleUndo()
	n := len(t.redoStack)
	if n == 0 {
		return false
	}
	s := t.redoStack[n-1]
	if !hunksFit(t.bufferLines(), s, true) {
		t.dropUndoHistory()
		return false
	}
	t.redoStack[n-1] = undoStep{}
	t.redoStack = t.redoStack[:n-1]
	t.redoBytes -= s.cost
	if t.redoBytes < 0 {
		t.redoBytes = 0
	}

	t.setBufferLines(applyHunks(t.bufferLines(), s.hunks, true))
	t.rebase()
	t.Cursor = t.clampHistoryPos(s.cursorAfter)
	t.Anchor = t.clampHistoryPos(s.anchorAfter)
	t.pushUndoEntry(s)
	t.afterHistoryEdit()
	t.Dirty = !t.matchesSaved() // see Undo
	t.breakUndoGroup()
	return true
}

// clampHistoryPos clamps a restored caret to the buffer. A step's carets
// were recorded against the very text it restores, so this only matters
// for the one step whose "before" caret was a guess — an unannounced
// change (settleUndo) — but a caret past the end of the buffer would
// panic the next keystroke, so every restore is clamped.
func (t *Tab) clampHistoryPos(p Position) Position {
	if t.Buffer == nil {
		return p
	}
	return t.Buffer.Clamp(p)
}

// RevertFile rewinds the buffer all the way back to the snapshot
// captured when the file was first opened (or last reloaded). It is an
// ordinary step — pushUndo, then the rewrite — so the user can recover
// from an accidental Revert with one Undo. Returns true when the buffer
// actually changed; false if it was already at the original.
func (t *Tab) RevertFile() bool {
	if !t.CanRevert() {
		return false
	}
	t.pushUndo(undoGroupStructural)
	t.applySnapshot(t.undoOriginal)
	// The buffer now matches the OPENED text, which is only the disk's
	// text if nothing was saved since — so ask the saved baseline, as
	// Undo does. A revert after a save leaves the tab dirty, and the next
	// save writes the opened text back.
	t.Dirty = !t.matchesSaved()
	t.breakUndoGroup()
	return true
}
