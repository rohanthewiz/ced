// =============================================================================
// File: internal/editor/undo_test.go
// Author: Spicer Matthews <spicer@cloudmanic.com>
// Created: 2026-04-30
// Copyright: 2026 Cloudmanic, LLC. All rights reserved.
// =============================================================================

// Tests for undo.go — the per-Tab snapshot stack, the typing/backspace/
// delete coalescing, redo invalidation, and the one-shot RevertFile.
//
// The fixture is intentionally a pure in-memory Tab built without going
// through NewTab so we don't have to touch disk. initUndo is called
// explicitly to mirror what NewTab does.

package editor

import (
	"fmt"
	"math/rand"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

// newScratchTab builds a Tab whose buffer is initialised from the given
// string, without doing any disk IO. The on-open snapshot is captured
// just like NewTab would do.
func newScratchTab(initial string) *Tab {
	t := &Tab{Buffer: NewBuffer(initial), StyleStale: true}
	t.initUndo()
	return t
}

// TestNewTabCapturesOriginal proves that calling initUndo at construction
// time stamps the on-open snapshot. RevertFile relies on this — without
// it, a freshly-opened tab would have nothing to rewind to.
func TestNewTabCapturesOriginal(t *testing.T) {
	tab := newScratchTab("hello\nworld")
	if got := tab.undoOriginal.Lines; len(got) != 2 || got[0] != "hello" || got[1] != "world" {
		t.Fatalf("undoOriginal not captured: %v", got)
	}
	if tab.CanRevert() {
		t.Fatal("CanRevert should be false for a freshly captured original")
	}
}

// TestInsertRune_CoalescesIntoSingleStep types five characters in quick
// succession and asserts there is exactly one undo entry — the burst.
// One Undo should restore the empty buffer rather than removing chars
// one at a time.
func TestInsertRune_CoalescesIntoSingleStep(t *testing.T) {
	tab := newScratchTab("")
	for _, r := range []rune{'h', 'e', 'l', 'l', 'o'} {
		tab.InsertRune(r)
	}
	if got := tab.Buffer.String(); got != "hello" {
		t.Fatalf("buffer = %q, want hello", got)
	}
	if !tab.CanUndo() {
		t.Fatal("expected something to undo")
	}
	if got := tab.UndoDepth(); got != 1 {
		t.Fatalf("expected 1 undo entry after a typing burst, got %d", got)
	}
	tab.Undo()
	if got := tab.Buffer.String(); got != "" {
		t.Fatalf("after undo buffer = %q, want empty", got)
	}
}

// TestInsertRune_BreakOnCursorMove ensures a manual cursor move closes
// the current typing group: undo should now peel off only the second
// burst, leaving the first intact.
func TestInsertRune_BreakOnCursorMove(t *testing.T) {
	tab := newScratchTab("")
	tab.InsertRune('a')
	tab.InsertRune('b')
	tab.MoveCursorTo(Position{Line: 0, Col: 0}, false)
	tab.InsertRune('X')
	tab.InsertRune('Y')

	if got := tab.Buffer.String(); got != "XYab" {
		t.Fatalf("buffer = %q, want XYab", got)
	}
	tab.Undo()
	if got := tab.Buffer.String(); got != "ab" {
		t.Fatalf("after first undo = %q, want ab", got)
	}
	tab.Undo()
	if got := tab.Buffer.String(); got != "" {
		t.Fatalf("after second undo = %q, want empty", got)
	}
}

// TestInsertRune_BreakAfterTimeout verifies a 500ms idle gap closes the
// group even with no other intervening op. Forces lastUndoAt into the
// past instead of sleeping so the suite stays fast.
func TestInsertRune_BreakAfterTimeout(t *testing.T) {
	tab := newScratchTab("")
	tab.InsertRune('a')
	tab.lastUndoAt = time.Now().Add(-2 * undoCoalesceWindow)
	tab.InsertRune('b')

	if got := tab.UndoDepth(); got != 2 {
		t.Fatalf("expected two undo entries after timeout gap, got %d", got)
	}
	tab.Undo()
	if got := tab.Buffer.String(); got != "a" {
		t.Fatalf("after undo = %q, want a", got)
	}
}

// TestInsertString_AlwaysStructural confirms that explicit InsertString
// calls (paste, indentation insert, "\n" on Enter) never coalesce with
// the surrounding typing burst.
func TestInsertString_AlwaysStructural(t *testing.T) {
	tab := newScratchTab("")
	tab.InsertRune('a')
	tab.InsertString("PASTE")
	tab.InsertRune('b')

	if got := tab.Buffer.String(); got != "aPASTEb" {
		t.Fatalf("buffer = %q, want aPASTEb", got)
	}
	if got := tab.UndoDepth(); got != 3 {
		t.Fatalf("expected 3 distinct undo entries, got %d", got)
	}
	tab.Undo()
	if got := tab.Buffer.String(); got != "aPASTE" {
		t.Fatalf("undo 1: got %q, want aPASTE", got)
	}
	tab.Undo()
	if got := tab.Buffer.String(); got != "a" {
		t.Fatalf("undo 2: got %q, want a", got)
	}
	tab.Undo()
	if got := tab.Buffer.String(); got != "" {
		t.Fatalf("undo 3: got %q, want empty", got)
	}
}

// TestBackspace_CoalescesWithItself verifies repeated backspaces inside
// the window produce a single undo step but never merge into a typing
// step on either side.
func TestBackspace_CoalescesWithItself(t *testing.T) {
	tab := newScratchTab("hello")
	tab.MoveCursorTo(Position{Line: 0, Col: 5}, false)
	tab.Backspace()
	tab.Backspace()
	tab.Backspace()
	if got := tab.Buffer.String(); got != "he" {
		t.Fatalf("after 3 backspaces = %q, want he", got)
	}
	if got := tab.UndoDepth(); got != 1 {
		t.Fatalf("expected 1 entry from backspace burst, got %d", got)
	}
	tab.Undo()
	if got := tab.Buffer.String(); got != "hello" {
		t.Fatalf("after undo = %q, want hello", got)
	}
}

// TestBackspace_BreaksTypingGroup makes sure switching from typing to
// backspace mid-burst opens a new group. Otherwise undoing the backspace
// would also undo the typing — which would be very confusing.
func TestBackspace_BreaksTypingGroup(t *testing.T) {
	tab := newScratchTab("")
	tab.InsertRune('h')
	tab.InsertRune('i')
	tab.Backspace()

	if got := tab.Buffer.String(); got != "h" {
		t.Fatalf("buffer = %q, want h", got)
	}
	tab.Undo()
	if got := tab.Buffer.String(); got != "hi" {
		t.Fatalf("after undo = %q, want hi (only the backspace), got %q", got, got)
	}
}

// TestDelete_CoalescesAndBreaksProperly mirrors the backspace check for
// the forward-delete group.
func TestDelete_CoalescesAndBreaksProperly(t *testing.T) {
	tab := newScratchTab("abcdef")
	tab.MoveCursorTo(Position{Line: 0, Col: 0}, false)
	tab.Delete()
	tab.Delete()
	if got := tab.Buffer.String(); got != "cdef" {
		t.Fatalf("after 2 deletes = %q, want cdef", got)
	}
	if got := tab.UndoDepth(); got != 1 {
		t.Fatalf("expected 1 entry from delete burst, got %d", got)
	}
}

// TestDeleteSelection_IsItsOwnStep proves selection deletes always get
// their own entry — coalescing them would let an Undo recover content
// the user clearly wants gone-then-undoable.
func TestDeleteSelection_IsItsOwnStep(t *testing.T) {
	tab := newScratchTab("hello world")
	tab.Anchor = Position{Line: 0, Col: 6}
	tab.Cursor = Position{Line: 0, Col: 11}
	tab.DeleteSelection()
	if got := tab.Buffer.String(); got != "hello " {
		t.Fatalf("buffer = %q, want 'hello '", got)
	}
	tab.Undo()
	if got := tab.Buffer.String(); got != "hello world" {
		t.Fatalf("after undo = %q, want 'hello world'", got)
	}
}

// TestInsertRune_OverSelectionPushesOnce verifies that typing a char
// while a selection is active pushes a single undo step (the
// DeleteSelection one) and then replaces — so undoing once gives back
// the full pre-replace state, not the empty selection.
func TestInsertRune_OverSelectionPushesOnce(t *testing.T) {
	tab := newScratchTab("hello world")
	tab.Anchor = Position{Line: 0, Col: 6}
	tab.Cursor = Position{Line: 0, Col: 11}
	tab.InsertRune('X')
	if got := tab.Buffer.String(); got != "hello X" {
		t.Fatalf("buffer = %q, want 'hello X'", got)
	}
	if got := tab.UndoDepth(); got != 1 {
		t.Fatalf("expected 1 entry, got %d", got)
	}
	tab.Undo()
	if got := tab.Buffer.String(); got != "hello world" {
		t.Fatalf("after undo = %q, want 'hello world'", got)
	}
}

// TestRedo_RoundTrips writes some text, undoes it, redoes it, and
// confirms the buffer comes back identical.
func TestRedo_RoundTrips(t *testing.T) {
	tab := newScratchTab("")
	tab.InsertString("alpha")
	tab.InsertString("beta")
	if !tab.Undo() {
		t.Fatal("expected first undo to succeed")
	}
	if got := tab.Buffer.String(); got != "alpha" {
		t.Fatalf("after undo = %q, want alpha", got)
	}
	if !tab.Redo() {
		t.Fatal("expected redo to succeed")
	}
	if got := tab.Buffer.String(); got != "alphabeta" {
		t.Fatalf("after redo = %q, want alphabeta", got)
	}
}

// TestRedo_InvalidatedByNewEdit asserts that any new mutation after an
// undo wipes the redo stack — otherwise the user could stumble into a
// branched timeline they can't easily reason about.
func TestRedo_InvalidatedByNewEdit(t *testing.T) {
	tab := newScratchTab("")
	tab.InsertString("a")
	tab.InsertString("b")
	tab.Undo()
	if !tab.CanRedo() {
		t.Fatal("expected CanRedo before new edit")
	}
	tab.InsertRune('X')
	if tab.CanRedo() {
		t.Fatal("new edit should have wiped the redo stack")
	}
}

// TestUndo_NoopOnEmptyStack returns false and leaves state untouched
// when there's nothing to undo.
func TestUndo_NoopOnEmptyStack(t *testing.T) {
	tab := newScratchTab("hi")
	if tab.Undo() {
		t.Fatal("Undo on empty stack should return false")
	}
	if got := tab.Buffer.String(); got != "hi" {
		t.Fatalf("buffer changed: %q", got)
	}
}

// TestRedo_NoopOnEmptyStack same idea for Redo.
func TestRedo_NoopOnEmptyStack(t *testing.T) {
	tab := newScratchTab("hi")
	if tab.Redo() {
		t.Fatal("Redo on empty stack should return false")
	}
}

// TestRevertFile_GoesAllTheWayBack types a long edit history then
// reverts in one step. The buffer should equal the on-open snapshot
// and a single Undo should bring back the pre-revert state.
func TestRevertFile_GoesAllTheWayBack(t *testing.T) {
	tab := newScratchTab("original")
	tab.MoveCursorTo(Position{Line: 0, Col: 8}, false)
	for _, r := range " edits" {
		tab.InsertRune(r)
	}
	tab.InsertString("\nmore")
	if got := tab.Buffer.String(); got != "original edits\nmore" {
		t.Fatalf("setup buffer = %q", got)
	}

	if !tab.RevertFile() {
		t.Fatal("expected RevertFile to report a change")
	}
	if got := tab.Buffer.String(); got != "original" {
		t.Fatalf("after revert = %q, want original", got)
	}
	if tab.CanRevert() {
		t.Fatal("CanRevert should be false right after Revert")
	}

	if !tab.Undo() {
		t.Fatal("expected Undo to be able to recover from a Revert")
	}
	if got := tab.Buffer.String(); got != "original edits\nmore" {
		t.Fatalf("after undo of revert = %q, want pre-revert state", got)
	}
}

// TestRevertFile_NoopWhenAlreadyOriginal returns false and doesn't
// pollute the undo stack.
func TestRevertFile_NoopWhenAlreadyOriginal(t *testing.T) {
	tab := newScratchTab("clean")
	before := tab.UndoDepth()
	if tab.RevertFile() {
		t.Fatal("RevertFile on a clean buffer should return false")
	}
	if tab.UndoDepth() != before {
		t.Fatal("undo stack should not have grown")
	}
}

// TestUndoStack_CapsAtMaxEntries forces the stack past its FIFO cap and
// verifies it stops growing rather than running away. The cap is small
// enough (500) that we can hit it in a fast test.
func TestUndoStack_CapsAtMaxEntries(t *testing.T) {
	tab := newScratchTab("")
	// Each iteration: structural push (no coalescing).
	for i := 0; i < maxUndoEntries+50; i++ {
		tab.InsertString(".")
	}
	if got := tab.UndoDepth(); got != maxUndoEntries {
		t.Fatalf("undo stack length = %d, want capped at %d", got, maxUndoEntries)
	}
}

// TestSave_BreaksTypingGroup confirms a save closes the current
// coalescing window: the next typed char should produce a fresh undo
// entry rather than merging with what came before.
func TestSave_BreaksTypingGroup(t *testing.T) {
	tab := newScratchTab("")
	tab.InsertRune('a')
	// Manually trigger the break behavior — we can't actually call
	// Save() without a path, but it just calls breakUndoGroup at the
	// end which is what we're testing the effect of.
	tab.breakUndoGroup()
	tab.InsertRune('b')

	if got := tab.UndoDepth(); got != 2 {
		t.Fatalf("expected 2 entries after group break, got %d", got)
	}
}

// TestReload_ResetsUndoBaseline verifies that Reload picks up the new
// on-disk content as the new "original" — Revert after a Reload should
// rewind to that, not to the pre-reload state.
func TestReload_ResetsUndoBaseline(t *testing.T) {
	tab := newScratchTab("v1")
	tab.InsertString("xyz")
	if !tab.CanRevert() {
		t.Fatal("expected revert available before reload")
	}

	// Simulate Reload's effect without actually doing disk IO.
	tab.Buffer = NewBuffer("disk-version")
	tab.Cursor = Position{}
	tab.Anchor = Position{}
	tab.initUndo()

	if tab.CanUndo() {
		t.Fatal("undo stack should be empty after baseline reset")
	}
	if tab.CanRevert() {
		t.Fatal("freshly reset baseline should have nothing to revert")
	}

	tab.InsertString("more")
	if !tab.RevertFile() {
		t.Fatal("expected revert to fire after a fresh edit")
	}
	if got := tab.Buffer.String(); got != "disk-version" {
		t.Fatalf("after revert = %q, want disk-version", got)
	}
}

// TestUndo_RestoresCursorAndAnchor checks that the snapshot mechanism
// also restores selection state — a user undoing a change shouldn't
// lose their cursor position or active selection.
func TestUndo_RestoresCursorAndAnchor(t *testing.T) {
	tab := newScratchTab("hello")
	tab.MoveCursorTo(Position{Line: 0, Col: 2}, false)
	tab.Anchor = Position{Line: 0, Col: 5}
	tab.InsertRune('Z') // replaces "llo" with "Z" → "heZ"

	if got := tab.Buffer.String(); got != "heZ" {
		t.Fatalf("buffer = %q, want heZ", got)
	}
	tab.Undo()
	if got := tab.Buffer.String(); got != "hello" {
		t.Fatalf("after undo = %q, want hello", got)
	}
	if tab.Cursor != (Position{Line: 0, Col: 2}) {
		t.Errorf("cursor after undo = %+v, want {0,2}", tab.Cursor)
	}
	if tab.Anchor != (Position{Line: 0, Col: 5}) {
		t.Errorf("anchor after undo = %+v, want {0,5}", tab.Anchor)
	}
}

// TestApplySnapshot_DoesNotShareLineStorage protects against an aliasing
// bug: if the live buffer were to share the snapshot's slice header,
// the next edit would silently corrupt history. We mutate a restored
// line and verify the original snapshot still has the old contents.
func TestApplySnapshot_DoesNotShareLineStorage(t *testing.T) {
	tab := newScratchTab("alpha\nbeta")
	snap := tab.captureSnapshot()
	tab.Buffer.Lines[0] = "MUTATED"
	if snap.Lines[0] != "alpha" {
		t.Fatalf("snapshot was clobbered: %v", snap.Lines)
	}
	tab.applySnapshot(snap)
	tab.Buffer.Lines[0] = "MUTATED-AGAIN"
	if snap.Lines[0] != "alpha" {
		t.Fatalf("apply leaked storage: %v", snap.Lines)
	}
}

// TestCanRevert_DetectsLineCountAndContent covers both branches of the
// CanRevert comparison — adding a line, and editing one in place.
func TestCanRevert_DetectsLineCountAndContent(t *testing.T) {
	tab := newScratchTab("one\ntwo")
	if tab.CanRevert() {
		t.Fatal("brand new tab should not report revert needed")
	}

	// Same line count, different content.
	tab.Buffer.Lines[0] = "ONE"
	if !tab.CanRevert() {
		t.Fatal("content change should trip CanRevert")
	}

	// Restore content, change line count.
	tab.Buffer.Lines[0] = "one"
	tab.Buffer.Lines = append(tab.Buffer.Lines, "extra")
	if !tab.CanRevert() {
		t.Fatal("line count change should trip CanRevert")
	}
}

// TestEmptyBuffer_HandledGracefully — the helpers should not panic on a
// zero-state Tab without a buffer (e.g. one constructed without
// NewBuffer for some reason). Captures + applies still produce
// sensible cursor/anchor copies.
func TestEmptyBuffer_HandledGracefully(t *testing.T) {
	tab := &Tab{Cursor: Position{Line: 1, Col: 2}, Anchor: Position{Line: 3, Col: 4}}
	snap := tab.captureSnapshot()
	if snap.Cursor != tab.Cursor || snap.Anchor != tab.Anchor {
		t.Fatal("captureSnapshot dropped cursor/anchor on a buffer-less tab")
	}
	tab.applySnapshot(snap)
	if tab.Buffer == nil {
		t.Fatal("applySnapshot should have produced a buffer")
	}
}

// TestBufferContentsAfterMixedHistory drives a longer realistic
// workflow — type, paste, type, save-marker, type, backspace bursts —
// and walks Undo all the way back to confirm the timeline is sane at
// every step. The biggest concern this catches is silent state drift:
// not what one undo does in isolation, but whether N undos compose to
// "exactly the on-open buffer."
func TestBufferContentsAfterMixedHistory(t *testing.T) {
	tab := newScratchTab("seed")
	tab.MoveCursorTo(Position{Line: 0, Col: 4}, false)

	tab.InsertRune(' ')         // typing-1
	tab.InsertRune('h')         // typing-1
	tab.InsertRune('i')         // typing-1
	tab.InsertString("[paste]") // structural
	tab.MoveCursorTo(Position{Line: 0, Col: len(tab.Buffer.LineRunes(0))}, false)
	tab.InsertRune('!') // typing-2
	tab.Backspace()     // backspace-1

	want := "seed hi[paste]"
	if got := tab.Buffer.String(); got != want {
		t.Fatalf("after edits = %q, want %q", got, want)
	}

	// Walk every Undo and stop on no-op.
	steps := 0
	for tab.Undo() {
		steps++
		if steps > 50 {
			t.Fatal("undo did not terminate")
		}
	}
	if got := tab.Buffer.String(); got != "seed" {
		t.Fatalf("after fully unwinding (%d steps) = %q, want seed", steps, got)
	}
	// Trailing newlines / whitespace shouldn't sneak in via snapshot copies.
	if strings.TrimSpace(tab.Buffer.String()) != "seed" {
		t.Fatal("trailing junk crept into restored buffer")
	}
}

// -----------------------------------------------------------------------------
// The byte budget (maxUndoBytes)
// -----------------------------------------------------------------------------

// TestStepCost_ChargesTheTextOnlyThisEntryHolds pins the central claim of
// the cost model: a step is charged its hunks' headers plus the text that
// nothing but it keeps alive — the REMOVED lines on the undo stack, the
// INSERTED lines on the redo stack — and never the untouched rest of the
// file, however big.
func TestStepCost_ChargesTheTextOnlyThisEntryHolds(t *testing.T) {
	s := undoStep{hunks: []undoHunk{{
		at:       7,
		removed:  []string{strings.Repeat("r", 100)},
		inserted: []string{strings.Repeat("i", 30), strings.Repeat("j", 40)},
	}}}
	headers := undoStepOverhead + undoHunkOverhead + 3*lineHeaderBytes
	if got, want := stepCost(s, false), headers+100; got != want {
		t.Fatalf("undo-side cost = %d, want %d (headers + removed text)", got, want)
	}
	if got, want := stepCost(s, true), headers+70; got != want {
		t.Fatalf("redo-side cost = %d, want %d (headers + inserted text)", got, want)
	}
}

// TestStepCost_IndependentOfFileSizeAndPosition is the N-131 claim in
// miniature: one typed line costs the same in a 10-line file and a
// 100k-line one, at the top or at the end. The snapshot model charged
// 16 bytes per line of the file, plus every line an insert shifted.
func TestStepCost_IndependentOfFileSizeAndPosition(t *testing.T) {
	cost := func(lines, at int) int {
		tab := newScratchTab(strings.Repeat("some line of text\n", lines))
		tab.Cursor = Position{Line: at, Col: 0}
		tab.Anchor = tab.Cursor
		tab.InsertString("new line\n")
		tab.settleUndo()
		return tab.undoBytes
	}
	small := cost(10, 0)
	for _, c := range []struct{ lines, at int }{{100_000, 0}, {100_000, 99_990}, {10, 9}} {
		if got := cost(c.lines, c.at); got != small {
			t.Errorf("%d lines, insert at %d: cost %d, want %d (same as a 10-line file)", c.lines, c.at, got, small)
		}
	}
	if small > 512 {
		t.Errorf("one inserted line costs %d bytes — expected a few hundred at most", small)
	}
}

// TestUndoDepth_BigFileKeepsTheWholeHistory is the N-131 reproduction:
// 300 line inserts near the TOP of a 20k-line file. The snapshot model
// charged each step 320KB of headers plus every shifted line and kept
// well under 100 of them; with deltas all 300 survive, and unwinding them
// gives back the file exactly.
func TestUndoDepth_BigFileKeepsTheWholeHistory(t *testing.T) {
	var b strings.Builder
	for i := 0; i < 20_000; i++ {
		fmt.Fprintf(&b, "line %05d of an ordinary source file, typical width\n", i)
	}
	original := b.String()
	for _, where := range []string{"top", "end"} {
		tab := newScratchTab(original)
		for i := 0; i < 300; i++ {
			line := 3
			if where == "end" {
				line = len(tab.Buffer.Lines) - 3
			}
			tab.Cursor = Position{Line: line, Col: 0}
			tab.Anchor = tab.Cursor
			tab.InsertString(fmt.Sprintf("inserted %d\n", i))
		}
		if got := tab.UndoDepth(); got != 300 {
			t.Fatalf("%s: UndoDepth = %d after 300 edits, want all 300 kept", where, got)
		}
		for tab.Undo() {
		}
		if tab.Buffer.String() != original {
			t.Fatalf("%s: unwinding every step did not restore the file", where)
		}
	}
}

// TestUndoStack_CapsAtMaxBytes edits a file whose lines are large enough
// that the byte budget binds long before the 500-entry cap does. Without
// the budget this stack would grow to 500 entries holding ~500MB; with
// it, the history is trimmed to whatever fits.
func TestUndoStack_CapsAtMaxBytes(t *testing.T) {
	// One 4MB line: every structural edit strands the previous copy, so
	// each entry costs ~4MB and only a handful fit in the 32MB budget.
	tab := newScratchTab(strings.Repeat("z", 4<<20))
	for i := 0; i < 40; i++ {
		tab.Cursor = Position{Line: 0, Col: 0}
		tab.Anchor = tab.Cursor
		tab.InsertString("a") // structural — never coalesces
	}
	if tab.UndoDepth() >= maxUndoEntries {
		t.Fatalf("undo stack reached the entry cap (%d) — the byte budget never bound",
			tab.UndoDepth())
	}
	if tab.undoBytes+tab.redoBytes > maxUndoBytes {
		t.Fatalf("undo budget exceeded: %d bytes > %d", tab.undoBytes+tab.redoBytes, maxUndoBytes)
	}
	if tab.UndoDepth() == 0 {
		t.Fatal("trimming must never empty the stack — the last step has to stay undoable")
	}
}

// TestTrimUndo_KeepsTheLastStep pins the floor: a single edit big enough
// to blow the entire budget on its own still leaves one undoable step.
// A history of zero is indistinguishable from undo being broken.
func TestTrimUndo_KeepsTheLastStep(t *testing.T) {
	tab := newScratchTab("seed")
	tab.undoStack = []undoStep{{cost: maxUndoBytes * 4}}
	tab.undoBytes = maxUndoBytes * 4
	tab.trimUndo()
	if tab.UndoDepth() != 1 {
		t.Fatalf("stack length after trim = %d, want the single entry kept", tab.UndoDepth())
	}
}

// TestUndoBytes_ReturnToZeroWhenTheHistoryUnwinds proves the running
// sums are maintained on every path, not just on push: undoing back to
// the start and redoing forward again must leave the accounting where it
// began rather than drifting up until the budget falsely binds.
func TestUndoBytes_ReturnToZeroWhenTheHistoryUnwinds(t *testing.T) {
	tab := newScratchTab("one\ntwo")
	for i := 0; i < 5; i++ {
		tab.InsertString("x")
	}
	if tab.undoBytes == 0 {
		t.Fatal("undoBytes should have accumulated after five structural edits")
	}
	for tab.Undo() {
	}
	if tab.undoBytes != 0 {
		t.Fatalf("undoBytes = %d after fully unwinding, want 0", tab.undoBytes)
	}
	for tab.Redo() {
	}
	if tab.redoBytes != 0 {
		t.Fatalf("redoBytes = %d after fully replaying, want 0", tab.redoBytes)
	}
	if tab.undoBytes <= 0 {
		t.Fatalf("undoBytes = %d after replaying, want the history charged again", tab.undoBytes)
	}
}

// TestUndoBytes_ClearedByRedoInvalidation confirms a fresh edit that
// throws the redo stack away also releases its bytes — otherwise the
// budget would leak on every "undo a few steps, then type".
func TestUndoBytes_ClearedByRedoInvalidation(t *testing.T) {
	tab := newScratchTab("one\ntwo")
	tab.InsertString("x")
	tab.InsertString("y")
	tab.Undo()
	if tab.redoBytes == 0 {
		t.Fatal("redoBytes should be non-zero with an entry on the redo stack")
	}
	tab.InsertString("z") // invalidates redo
	if tab.redoBytes != 0 {
		t.Fatalf("redoBytes = %d after a new edit, want 0", tab.redoBytes)
	}
}

// TestInitUndo_ResetsTheBudget pins that a reload zeroes the accounting
// along with the stacks — a stale non-zero sum would silently shrink the
// history of every file opened into a reused Tab.
func TestInitUndo_ResetsTheBudget(t *testing.T) {
	tab := newScratchTab("one")
	tab.InsertString("x")
	tab.initUndo()
	if tab.undoBytes != 0 || tab.redoBytes != 0 {
		t.Fatalf("budget after initUndo = %d/%d, want 0/0", tab.undoBytes, tab.redoBytes)
	}
}

// TestUndoDepth_TracksTheStack pins the accessor the cross-file edit journal
// validates with. It has to move with pushes and pops, and — the case that
// makes it necessary alongside EditRev — it has to reflect an eviction from
// the bottom, which shrinks the stack without changing the buffer at all.
func TestUndoDepth_TracksTheStack(t *testing.T) {
	tab := &Tab{Buffer: NewBuffer("a")}
	tab.initUndo()
	if got := tab.UndoDepth(); got != 0 {
		t.Fatalf("fresh tab UndoDepth = %d, want 0", got)
	}

	for i := 0; i < 3; i++ {
		tab.pushUndo(undoGroupStructural)
	}
	if got := tab.UndoDepth(); got != 3 {
		t.Errorf("after 3 pushes UndoDepth = %d, want 3", got)
	}
	tab.Undo()
	if got := tab.UndoDepth(); got != 2 {
		t.Errorf("after an undo UndoDepth = %d, want 2", got)
	}

	// Eviction: overshoot the entry cap and the bottom is dropped, so the
	// depth stops growing even though the buffer keeps changing. This is
	// why the journal checks depth AND EditRev rather than either alone.
	for i := 0; i < maxUndoEntries+10; i++ {
		tab.pushUndo(undoGroupStructural)
	}
	if got := tab.UndoDepth(); got != maxUndoEntries {
		t.Errorf("past the cap UndoDepth = %d, want %d", got, maxUndoEntries)
	}
}

// openSavedTab writes body to a file in a temp dir and opens it with
// NewTab, so Save / ReloadAsEdit exercise the real disk paths.
func openSavedTab(t *testing.T, body string) (*Tab, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "f.txt")
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	tab, err := NewTab(path)
	if err != nil {
		t.Fatal(err)
	}
	return tab, path
}

// readDisk returns the file's bytes as a string, failing the test on error.
func readDisk(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// TestUndo_AfterSaveBackToOpenedTextIsDirty is the N-113 reproduction:
// type, save (auto-save does the same), undo back to the OPENED text. The
// disk holds the saved text, so the tab must read dirty — before the fix
// Dirty was measured against the on-open snapshot, the tab read clean,
// and quit asked nothing while the file kept the undone edit.
func TestUndo_AfterSaveBackToOpenedTextIsDirty(t *testing.T) {
	tab, path := openSavedTab(t, "original")
	tab.Cursor = Position{Line: 0, Col: len("original")}
	tab.Anchor = tab.Cursor
	tab.InsertString(" EXPERIMENT")
	if err := tab.Save(); err != nil {
		t.Fatal(err)
	}
	if !tab.Undo() {
		t.Fatal("expected an undo step")
	}
	if got := tab.Buffer.String(); got != "original" {
		t.Fatalf("buffer after undo = %q", got)
	}
	if got := readDisk(t, path); got != "original EXPERIMENT" {
		t.Fatalf("disk = %q, want the saved text", got)
	}
	if !tab.Dirty {
		t.Fatal("buffer differs from disk after undo but the tab reads clean")
	}
}

// TestRedo_BackToSavedTextIsClean is the reverse half of N-113: after an
// undo past a save, redoing back to exactly the saved text reads clean —
// before the fix it read dirty because it differed from the opened text.
func TestRedo_BackToSavedTextIsClean(t *testing.T) {
	tab, _ := openSavedTab(t, "a")
	tab.Cursor = Position{Line: 0, Col: 1}
	tab.Anchor = tab.Cursor
	tab.InsertString("b")
	if err := tab.Save(); err != nil {
		t.Fatal(err)
	}
	tab.Undo()
	if !tab.Dirty {
		t.Fatal("undo past the save should read dirty")
	}
	tab.Redo()
	if tab.Dirty {
		t.Fatal("redo back to the saved text should read clean")
	}
}

// TestRevertFile_AfterSaveIsDirty pins ≡ "Revert file" against the saved
// baseline: it rewinds to the OPENED text, which a save has since moved
// the disk away from, so the tab must stay dirty.
func TestRevertFile_AfterSaveIsDirty(t *testing.T) {
	tab, _ := openSavedTab(t, "one")
	tab.Cursor = Position{Line: 0, Col: 3}
	tab.Anchor = tab.Cursor
	tab.InsertString(" two")
	if err := tab.Save(); err != nil {
		t.Fatal(err)
	}
	if !tab.RevertFile() {
		t.Fatal("expected revert to fire")
	}
	if got := tab.Buffer.String(); got != "one" {
		t.Fatalf("buffer after revert = %q", got)
	}
	if !tab.Dirty {
		t.Fatal("reverted to the opened text after a save, but the tab reads clean")
	}
}

// TestUndo_WithoutSaveStillCleanAtOpenedText guards the old behaviour
// that must survive: with no save in between, undoing back to the opened
// text is clean (the disk still holds it).
func TestUndo_WithoutSaveStillCleanAtOpenedText(t *testing.T) {
	tab, _ := openSavedTab(t, "x")
	tab.InsertString("y")
	tab.Dirty = true
	tab.Undo()
	if tab.Dirty {
		t.Fatal("back at the opened, never-saved-over text should read clean")
	}
}

// TestForgetSavedText_KeepsEveryStateDirty pins the "disk holds text the
// tab never saw" state (deleted file, formatter under a dirty buffer, keep
// mine): no undo or revert may read clean until the next save.
func TestForgetSavedText_KeepsEveryStateDirty(t *testing.T) {
	tab, _ := openSavedTab(t, "base")
	tab.InsertString("edit ")
	tab.Dirty = true
	tab.ForgetSavedText()
	tab.Undo()
	if !tab.Dirty {
		t.Fatal("undo read clean with the saved text unknown")
	}
	tab.Redo()
	tab.RevertFile()
	if !tab.Dirty {
		t.Fatal("revert read clean with the saved text unknown")
	}
	if err := tab.Save(); err != nil {
		t.Fatal(err)
	}
	tab.InsertString("z")
	tab.Undo()
	if tab.Dirty {
		t.Fatal("a save must re-establish the baseline")
	}
}

// TestReloadAsEdit_FormattedTextIsTheNewBaseline: ced's own rewrite
// (format-on-save) adopted through ReloadAsEdit becomes the saved text, so
// undoing the reformat reads dirty and redoing it reads clean.
func TestReloadAsEdit_FormattedTextIsTheNewBaseline(t *testing.T) {
	tab, path := openSavedTab(t, "a  b")
	if err := os.WriteFile(path, []byte("a b"), 0o644); err != nil {
		t.Fatal(err)
	}
	changed, err := tab.ReloadAsEdit()
	if err != nil || !changed {
		t.Fatalf("ReloadAsEdit = %v, %v", changed, err)
	}
	tab.Undo()
	if !tab.Dirty {
		t.Fatal("undoing the adopted reformat should read dirty")
	}
	tab.Redo()
	if tab.Dirty {
		t.Fatal("redoing back to the disk's text should read clean")
	}
}

// TestMarkSaved_CopiesTheLineHeaders: the baseline must not alias
// Buffer.Lines, which some primitives rewrite in place.
func TestMarkSaved_CopiesTheLineHeaders(t *testing.T) {
	tab := newScratchTab("a\nb")
	tab.markSaved()
	tab.Buffer.Lines[0] = "changed"
	if tab.savedLines[0] != "a" {
		t.Fatal("savedLines aliases the live buffer")
	}
}

// -----------------------------------------------------------------------------
// Deltas: lineHunks, applyHunks, settling (N-131)
// -----------------------------------------------------------------------------

// TestLineHunks_TrimsToTheEditedMiddle covers the common shapes: an edit
// inside a line, an inserted line, a deleted line, and no change at all.
// Only the touched lines appear in a hunk; at is the before-state line.
func TestLineHunks_TrimsToTheEditedMiddle(t *testing.T) {
	before := []string{"a", "b", "c", "d"}
	cases := []struct {
		name  string
		after []string
		want  []undoHunk
	}{
		{"same", []string{"a", "b", "c", "d"}, nil},
		{"edit", []string{"a", "B", "c", "d"}, []undoHunk{{at: 1, removed: []string{"b"}, inserted: []string{"B"}}}},
		{"insert", []string{"a", "b", "new", "c", "d"}, []undoHunk{{at: 2, inserted: []string{"new"}}}},
		{"delete", []string{"a", "c", "d"}, []undoHunk{{at: 1, removed: []string{"b"}}}},
		{"two runs", []string{"A", "b", "C", "D"}, []undoHunk{
			{at: 0, removed: []string{"a"}, inserted: []string{"A"}},
			{at: 2, removed: []string{"c", "d"}, inserted: []string{"C", "D"}},
		}},
	}
	for _, c := range cases {
		got := lineHunks(before, c.after)
		if !reflect.DeepEqual(got, c.want) {
			t.Errorf("%s: lineHunks = %#v, want %#v", c.name, got, c.want)
		}
	}
}

// TestLineHunks_ShiftedMiddleUsesTheDiffer pins case 3: a line added near
// the top and another near the bottom of a large region. Index pairing
// would report every line in between; the patience differ gives the two
// real hunks, and the step's cost is a few lines, not the span.
func TestLineHunks_ShiftedMiddleUsesTheDiffer(t *testing.T) {
	before := make([]string, 2000)
	for i := range before {
		before[i] = fmt.Sprintf("line %d", i)
	}
	after := append([]string(nil), before[:10]...)
	after = append(after, "added at the top")
	after = append(after, before[10:1990]...)
	after = append(after, "added at the bottom", "and another")
	after = append(after, before[1990:]...)

	got := lineHunks(before, after)
	want := []undoHunk{
		{at: 10, inserted: []string{"added at the top"}},
		{at: 1990, inserted: []string{"added at the bottom", "and another"}},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("lineHunks = %#v, want %#v", got, want)
	}

	// Same count, still shifted: one line added at the top, one removed at
	// the bottom. Index pairing would call ~2000 lines changed.
	shifted := append([]string{"new first"}, before[:1999]...)
	got = lineHunks(before, shifted)
	if len(got) != 2 {
		t.Fatalf("equal-count shift: %d hunks, want 2 (one insert, one delete)", len(got))
	}
	if n := len(got[0].removed) + len(got[0].inserted) + len(got[1].removed) + len(got[1].inserted); n != 2 {
		t.Fatalf("equal-count shift names %d lines, want 2", n)
	}
}

// TestApplyHunks_RoundTripsRandomEdits is the property every Undo and
// Redo leans on: for any before/after pair, lineHunks' hunks applied
// forward to before give after, and applied backward to after give
// before — through both the in-place path (no line-count change) and
// the rebuild path, and through the differ for large shifted middles.
// The inputs are copied first because applyHunks may patch in place.
func TestApplyHunks_RoundTripsRandomEdits(t *testing.T) {
	rng := rand.New(rand.NewSource(131))
	word := func() string { return fmt.Sprintf("w%d", rng.Intn(40)) }
	for iter := 0; iter < 2000; iter++ {
		n := rng.Intn(120)
		before := make([]string, n)
		for i := range before {
			before[i] = word()
		}
		after := append([]string(nil), before...)
		for k := rng.Intn(6); k >= 0; k-- {
			at := 0
			if len(after) > 0 {
				at = rng.Intn(len(after) + 1)
			}
			switch rng.Intn(3) {
			case 0: // replace in place
				if at < len(after) {
					after[at] = word()
				}
			case 1: // insert a run
				run := make([]string, rng.Intn(40)+1)
				for i := range run {
					run[i] = word()
				}
				after = append(after[:at], append(run, after[at:]...)...)
			case 2: // delete a run
				end := at + rng.Intn(40) + 1
				if end > len(after) {
					end = len(after)
				}
				after = append(after[:at], after[end:]...)
			}
		}
		hunks := lineHunks(before, after)
		s := undoStep{hunks: hunks, beforeLen: len(before), afterLen: len(after)}
		if !hunksFit(before, s, true) || !hunksFit(after, s, false) {
			t.Fatalf("iter %d: hunks don't fit their own sides", iter)
		}
		fwd := applyHunks(append([]string(nil), before...), hunks, true)
		if !reflect.DeepEqual(nonNil(fwd), nonNil(after)) {
			t.Fatalf("iter %d: forward gave %v, want %v (hunks %#v)", iter, fwd, after, hunks)
		}
		back := applyHunks(append([]string(nil), after...), hunks, false)
		if !reflect.DeepEqual(nonNil(back), nonNil(before)) {
			t.Fatalf("iter %d: backward gave %v, want %v (hunks %#v)", iter, back, before, hunks)
		}
	}
}

// nonNil maps a nil slice to an empty one so DeepEqual compares contents.
func nonNil(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}

// TestUndoHunks_DoNotAliasTheBuffer protects against the aliasing bug
// the delta model is most exposed to: the buffer and undoBase are patched
// in place, so a hunk sharing their array would have its history
// rewritten by the next edit.
func TestUndoHunks_DoNotAliasTheBuffer(t *testing.T) {
	tab := newScratchTab("alpha\nbeta\ngamma")
	tab.MoveCursorTo(Position{Line: 1, Col: 0}, false)
	tab.InsertRune('X')
	tab.settleUndo()
	h := tab.undoStack[0].hunks[0]
	tab.Buffer.Lines[1] = "MUTATED"
	tab.undoBase[1] = "MUTATED"
	if h.removed[0] != "beta" || h.inserted[0] != "Xbeta" {
		t.Fatalf("hunk was rewritten through the buffer: %#v", h)
	}
}

// TestUndoDepth_SettlingDoesNotMoveIt: the open step is counted, so turning
// it into a settled entry leaves the depth where it was — the workspace
// edit journal compares depths across exactly such moments.
func TestUndoDepth_SettlingDoesNotMoveIt(t *testing.T) {
	tab := newScratchTab("a")
	tab.InsertString("b")
	if got := tab.UndoDepth(); got != 1 || !tab.undoOpen {
		t.Fatalf("after one edit: depth %d open %v, want 1 and open", got, tab.undoOpen)
	}
	tab.settleUndo()
	if got := tab.UndoDepth(); got != 1 || tab.undoOpen {
		t.Fatalf("after settling: depth %d open %v, want 1 and settled", got, tab.undoOpen)
	}
	if !tab.CanUndo() {
		t.Fatal("CanUndo false with a settled step on the stack")
	}
}

// TestSettle_UnannouncedChangeIsItsOwnStep: a buffer change no pushUndo
// announced is filed as a step when the history next moves, so Undo
// takes it back rather than applying an older delta to text it doesn't
// describe — and it drops redo, like any new edit.
func TestSettle_UnannouncedChangeIsItsOwnStep(t *testing.T) {
	tab := newScratchTab("one\ntwo\nthree")
	tab.InsertString("x")
	tab.Undo()
	if !tab.CanRedo() {
		t.Fatal("setup: expected a redo step")
	}
	tab.Buffer.Lines = []string{"one", "TWO", "three", "four"} // nobody called pushUndo
	if tab.Redo() {
		t.Fatal("Redo replayed a step over text that changed underneath it")
	}
	if got := tab.Buffer.String(); got != "one\nTWO\nthree\nfour" {
		t.Fatalf("buffer after the refused redo = %q", got)
	}
	if !tab.Undo() {
		t.Fatal("expected the unannounced change to be undoable")
	}
	if got := tab.Buffer.String(); got != "one\ntwo\nthree" {
		t.Fatalf("after undo = %q, want the text before the unannounced change", got)
	}
}

// TestUndo_RefusesAStepThatDoesNotFit: should the history ever disagree
// with the buffer, Undo refuses and drops the history rather than writing
// a delta over the wrong lines. The buffer is left exactly as it was.
func TestUndo_RefusesAStepThatDoesNotFit(t *testing.T) {
	tab := newScratchTab("a\nb")
	tab.undoStack = []undoStep{{
		hunks:     []undoHunk{{at: 0, removed: []string{"zzz"}, inserted: []string{"not here"}}},
		beforeLen: 2, afterLen: 2,
	}}
	if tab.Undo() {
		t.Fatal("Undo applied a step whose lines aren't in the buffer")
	}
	if got := tab.Buffer.String(); got != "a\nb" {
		t.Fatalf("buffer = %q, want it untouched", got)
	}
	if tab.CanUndo() || tab.CanRedo() {
		t.Fatal("the untrustworthy history should have been dropped")
	}
}

// TestUndo_ScatteredMultiEditIsOneCheapStep: a multi-range edit that adds
// lines at both ends of a large file (the multi-caret Enter / resolve-all
// shape) is still ONE undo step, round-trips exactly, and is charged for
// what it changed — not for the thousands of lines between its hunks.
func TestUndo_ScatteredMultiEditIsOneCheapStep(t *testing.T) {
	var b strings.Builder
	for i := 0; i < 5000; i++ {
		fmt.Fprintf(&b, "line %d\n", i)
	}
	original := b.String()
	tab := newScratchTab(original)
	_, ok := tab.ApplyMultiEdit([]Edit{
		{Start: Position{Line: 10, Col: 0}, End: Position{Line: 10, Col: 0}, NewText: "top\n"},
		{Start: Position{Line: 4990, Col: 0}, End: Position{Line: 4990, Col: 0}, NewText: "bottom\nmore\n"},
	})
	if !ok {
		t.Fatal("ApplyMultiEdit refused")
	}
	edited := tab.Buffer.String()
	tab.settleUndo()
	if got := len(tab.undoStack[0].hunks); got != 2 {
		t.Fatalf("step has %d hunks, want 2", got)
	}
	if tab.undoBytes > 1024 {
		t.Fatalf("step costs %d bytes — charged for the span, not the change", tab.undoBytes)
	}
	tab.Undo()
	if tab.Buffer.String() != original {
		t.Fatal("undo did not restore the original")
	}
	tab.Redo()
	if tab.Buffer.String() != edited {
		t.Fatal("redo did not restore the edit")
	}
}

// TestUndo_StepRestoresBothCarets: Undo lands on the caret where the step
// began, Redo on the caret where it ended — the two ends a delta records.
func TestUndo_StepRestoresBothCarets(t *testing.T) {
	tab := newScratchTab("hello\nworld")
	tab.MoveCursorTo(Position{Line: 1, Col: 5}, false)
	tab.InsertString("!\nnext")
	end := tab.Cursor
	tab.Undo()
	if tab.Cursor != (Position{Line: 1, Col: 5}) {
		t.Fatalf("cursor after undo = %+v, want {1 5}", tab.Cursor)
	}
	tab.Redo()
	if tab.Cursor != end {
		t.Fatalf("cursor after redo = %+v, want %+v", tab.Cursor, end)
	}
}
