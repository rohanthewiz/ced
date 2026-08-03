// =============================================================================
// File: internal/editor/undo.go
// Author: Spicer Matthews <spicer@cloudmanic.com>
// Created: 2026-04-30
// Copyright: 2026 Cloudmanic, LLC. All rights reserved.
// =============================================================================

// undo.go gives every Tab a per-tab snapshot-based undo history plus a
// one-shot "revert to original" hatch.
//
// The model is intentionally simple: each entry is a full copy of the
// buffer's lines plus the cursor / anchor at the moment the snapshot was
// taken. That uses more memory than an operation log but keeps the code
// small enough to reason about and is plenty fast for the file sizes a
// terminal editor opens. The stack is FIFO-capped so a runaway typing
// session can't grow it forever.
//
// THE CAP IS BYTES, NOT ENTRIES — the entry count is only the backstop.
// A snapshot of a 25k-line file costs 400KB in slice headers alone
// before a single character of content, so 500 of them is ~200MB per
// tab, and a user with eight big files open pays it eight times. Every
// entry therefore carries an estimate of what it actually keeps alive
// and pushUndo evicts from the bottom until the tab is back inside
// maxUndoBytes. See snapshotCost for what that estimate counts and, more
// importantly, what it deliberately doesn't.
//
// Coalescing — every typed rune doesn't get its own entry. Consecutive
// inserts of the same kind (typing, backspace, delete) inside a 500ms
// window collapse into a single undo step, so undoing a 50-character
// word is one click, not 50. Anything structural — pasting, hitting
// Enter, deleting a selection, an explicit cursor move — closes the
// current group so the next mutation starts a fresh entry.

package editor

import "time"

// maxUndoEntries caps the per-tab stack depth. 500 is generous enough
// that typical editing sessions never hit the wall; once we do, the
// oldest entry is dropped (FIFO) so the user can still undo a long
// way back without unbounded memory growth.
//
// On a small file this is the only limit that ever binds — 500 snapshots
// of a 400-line source file is under 4MB. maxUndoBytes is what takes
// over once the file is big enough for the count to be the wrong unit.
const maxUndoEntries = 500

// maxUndoBytes is the per-tab budget for the undo and redo stacks
// combined. 32MB is deliberately generous against real editing costs
// (a typing step on a 25k-line file estimates at ~400KB, so ~80 steps
// survive there; a 400-line file never comes close and keeps all 500),
// while still bounding a session that edits several large files to
// something a laptop doesn't notice.
//
// The two stacks share one budget because entries MOVE between them:
// Undo pops one side and pushes the other, so budgeting them separately
// would let the total drift to twice the number either one allows.
const maxUndoBytes = 32 << 20

// lineHeaderBytes is the size of a string header (data pointer + length)
// on a 64-bit build — the per-line cost of a snapshot's []string that is
// paid whether or not any content is shared. Hardcoded rather than taken
// from unsafe.Sizeof so this file needs no unsafe import; on a 32-bit
// build it over-counts by 2x, which errs toward a smaller history rather
// than a larger one.
const lineHeaderBytes = 16

// undoCoalesceWindow is the inactivity gap that closes a coalescing
// group. 500ms feels right — pause-and-think between words almost
// always lasts longer than this, while a typing burst lands well inside.
const undoCoalesceWindow = 500 * time.Millisecond

// undoGroup tags each snapshot with the kind of operation that produced
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

// snapshot captures everything needed to reproduce the editor's content
// state at a moment in time. We don't include scroll position — undo is
// about *what* was in the buffer, not where the user happened to be
// looking — but cursor + anchor are part of the user's state.
type snapshot struct {
	Lines  []string
	Cursor Position
	Anchor Position

	// cost is snapshotCost's estimate of the memory this entry keeps
	// alive, stamped once when the entry is pushed. Stored rather than
	// recomputed so eviction is O(1) — the byte budget is checked on
	// every push, and re-measuring the bottom of the stack each time
	// would make a capped stack quadratic in the file's line count.
	cost int
}

// snapshotCost estimates how much memory keeping s on a history stack
// actually costs, given that prev is the entry it will sit on top of.
//
// Two terms, and the second one is where the thinking is:
//
//   - The []string header array (len * lineHeaderBytes). This is
//     unshared by construction — captureSnapshot allocates a fresh slice
//     every time — so it is real, per-entry, and on a large file it
//     dominates everything else. It is the 200MB the entry cap missed.
//   - The bytes of the lines that DIFFER from prev. Copying a []string
//     copies headers, not characters: every line the edit didn't touch
//     is the same string the live buffer and the neighbouring snapshots
//     already hold, so charging this entry for it would over-count by
//     the depth of the stack. Counting only what changed is what keeps
//     a 1MB file's 500-step history honest at a few MB instead of
//     reporting 500MB and amputating the history for no reason — while
//     still catching the case the header term alone would miss: editing
//     a file of very long lines (minified JS, a data blob), where each
//     step strands a multi-megabyte string nothing else references.
//
// The comparison walks both slices, but a shared line compares equal on
// its data pointer, so the common case is a pointer check per line —
// cheap next to the header copy captureSnapshot already paid for.
func snapshotCost(s, prev snapshot) int {
	cost := len(s.Lines) * lineHeaderBytes
	for i, ln := range s.Lines {
		if i < len(prev.Lines) && prev.Lines[i] == ln {
			continue
		}
		cost += len(ln)
	}
	return cost
}

// captureSnapshot returns a deep copy of the current buffer state so a
// later mutation can't bleed into history.
func (t *Tab) captureSnapshot() snapshot {
	if t.Buffer == nil {
		return snapshot{Cursor: t.Cursor, Anchor: t.Anchor}
	}
	lines := make([]string, len(t.Buffer.Lines))
	copy(lines, t.Buffer.Lines)
	return snapshot{Lines: lines, Cursor: t.Cursor, Anchor: t.Anchor}
}

// applySnapshot replaces the buffer with s. It also re-copies the lines
// so the live buffer doesn't share storage with the history entry —
// otherwise the next edit would silently rewrite the past.
func (t *Tab) applySnapshot(s snapshot) {
	lines := make([]string, len(s.Lines))
	copy(lines, s.Lines)
	if t.Buffer == nil {
		t.Buffer = NewBuffer("")
	}
	t.Buffer.Lines = lines
	t.Cursor = s.Cursor
	t.Anchor = s.Anchor
	// Secondary carets were positioned against a buffer state we just
	// threw away — keeping them would leave the next keystroke editing a
	// line that has moved. The primary is restored from the snapshot, so
	// the user still lands where the step happened.
	t.Carets = nil
	t.cursorMoved = true
	t.InvalidateStyles()
	t.EditRev++
}

// initUndo seeds the original-state snapshot used by RevertFile. Called
// from NewTab and Reload — both moments where the buffer is now "what's
// on disk" and any prior history is meaningless.
func (t *Tab) initUndo() {
	t.undoOriginal = t.captureSnapshot()
	t.undoStack = nil
	t.redoStack = nil
	t.undoBytes = 0
	t.redoBytes = 0
	t.lastUndoGroup = undoGroupNone
}

// undoTop returns the entry a newly-pushed undo snapshot will sit on
// top of — the current top of the stack, or the on-open original when
// the stack is empty. It is the baseline snapshotCost measures against,
// and the two are always exactly one edit step apart, which is what
// makes "count only the lines that changed" the right estimate.
func (t *Tab) undoTop() snapshot {
	if n := len(t.undoStack); n > 0 {
		return t.undoStack[n-1]
	}
	return t.undoOriginal
}

// pushUndoEntry appends s to the undo stack, charges its cost to the
// tab's budget, and evicts from the bottom until the tab is back inside
// its limits. The single write path for the undo stack — pushUndo,
// Redo, and RevertFile all go through it so none of them can forget the
// bookkeeping.
func (t *Tab) pushUndoEntry(s snapshot, prev snapshot) {
	s.cost = snapshotCost(s, prev)
	t.undoStack = append(t.undoStack, s)
	t.undoBytes += s.cost
	t.trimUndo()
}

// trimUndo drops the oldest undo entries until the stack fits both
// caps. The last entry is never evicted: "undo the thing I just did"
// has to keep working even for a single step big enough to blow the
// whole budget on its own (a multi-megabyte paste), and a history of
// zero is indistinguishable from undo being broken.
func (t *Tab) trimUndo() {
	for len(t.undoStack) > 1 &&
		(len(t.undoStack) > maxUndoEntries || t.undoBytes+t.redoBytes > maxUndoBytes) {
		t.undoBytes -= t.undoStack[0].cost
		// Keep the original snapshot intact — it lives in undoOriginal,
		// not the stack, so RevertFile is unaffected by any of this.
		t.undoStack = t.undoStack[1:]
	}
	if t.undoBytes < 0 {
		t.undoBytes = 0
	}
}

// pushUndo records the *current* state on the undo stack so the caller
// can mutate freely afterwards. group selects coalescing behavior — a
// typing push within undoCoalesceWindow of another typing push is a
// no-op, while undoGroupStructural always creates a new entry. Any new
// edit invalidates the redo stack.
func (t *Tab) pushUndo(group undoGroup) {
	// A multi-caret fan-out already filed one snapshot for the whole
	// burst (applyAtCarets); the per-caret primitives running inside it
	// must not each file another, or undoing a five-caret keystroke
	// would take five presses.
	if t.undoSuppress {
		return
	}
	if t.canCoalesce(group) {
		t.lastUndoAt = time.Now() // extend the window
		return
	}
	t.pushUndoEntry(t.captureSnapshot(), t.undoTop())
	t.redoStack = nil
	t.redoBytes = 0
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
// "logical step" boundary).
func (t *Tab) breakUndoGroup() {
	t.lastUndoGroup = undoGroupNone
}

// CanUndo reports whether there is anything to roll back. The action
// menu uses this to enable / disable the Undo row.
func (t *Tab) CanUndo() bool {
	return len(t.undoStack) > 0
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

// Undo restores the previous snapshot. Returns true when a step was
// actually undone — false lets the caller flash a "nothing to undo"
// message. The current state is moved onto the redo stack first so a
// subsequent Redo can replay it forward.
func (t *Tab) Undo() bool {
	if len(t.undoStack) == 0 {
		return false
	}
	last := len(t.undoStack) - 1
	prev := t.undoStack[last]
	t.undoStack = t.undoStack[:last]
	t.undoBytes -= prev.cost

	// Measure the redo entry against the state we're about to restore:
	// the two are one edit apart, which is the comparison snapshotCost
	// is built for. Popping first is what makes prev available to it.
	current := t.captureSnapshot()
	current.cost = snapshotCost(current, prev)
	t.redoStack = append(t.redoStack, current)
	t.redoBytes += current.cost
	t.trimUndo()

	t.applySnapshot(prev)
	t.Dirty = t.CanRevert()
	t.breakUndoGroup()
	return true
}

// Redo re-applies a step that was just undone. Returns true when a step
// was actually redone.
func (t *Tab) Redo() bool {
	if len(t.redoStack) == 0 {
		return false
	}
	t.pushUndoEntry(t.captureSnapshot(), t.undoTop())

	last := len(t.redoStack) - 1
	next := t.redoStack[last]
	t.redoStack = t.redoStack[:last]
	t.redoBytes -= next.cost
	if t.redoBytes < 0 {
		t.redoBytes = 0
	}

	t.applySnapshot(next)
	t.Dirty = t.CanRevert()
	t.breakUndoGroup()
	return true
}

// RevertFile rewinds the buffer all the way back to the snapshot
// captured when the file was first opened (or last reloaded). The
// current state is pushed onto the undo stack first so the user can
// recover from an accidental Revert with one Undo. Returns true when
// the buffer actually changed; false if it was already at the original.
func (t *Tab) RevertFile() bool {
	if !t.CanRevert() {
		return false
	}
	t.pushUndoEntry(t.captureSnapshot(), t.undoTop())
	t.redoStack = nil
	t.redoBytes = 0
	t.applySnapshot(t.undoOriginal)
	t.Dirty = t.CanRevert() // false now — buffer matches original
	t.breakUndoGroup()
	return true
}
