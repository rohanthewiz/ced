// =============================================================================
// File: internal/editor/conflict_test.go
// Author: Rohan Allison <rohanthewiz@gmail.com>
// Created: 2026-10-03
// Copyright: 2026 Rohan Allison. All rights reserved.
// =============================================================================

package editor

import (
	"reflect"
	"strings"
	"testing"
)

// twoWay is a file with one plain two-way conflict between unrelated lines,
// shaped exactly as git writes a stopped cherry-pick.
const twoWay = "a\n" +
	"<<<<<<< HEAD\n" +
	"B-main\n" +
	"=======\n" +
	"B-topic\n" +
	">>>>>>> 2a0370b (t1 change b)\n" +
	"c"

// threeWay is the same conflict in git's diff3 / zdiff3 style, with the
// common ancestor between the two sides.
const threeWay = "a\n" +
	"<<<<<<< HEAD\n" +
	"B-main\n" +
	"||||||| parent of 2a0370b (t1 change b)\n" +
	"b\n" +
	"=======\n" +
	"B-topic\n" +
	">>>>>>> 2a0370b (t1 change b)\n" +
	"c"

// TestParseConflicts_TwoWay pins the block's line indexes and labels for
// the shape git writes by default.
func TestParseConflicts_TwoWay(t *testing.T) {
	got := ParseConflicts(strings.Split(twoWay, "\n"))
	want := []ConflictBlock{{
		Start: 1, Base: -1, Mid: 3, End: 5,
		CurrentLabel: "HEAD", IncomingLabel: "2a0370b (t1 change b)",
	}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %+v\nwant %+v", got, want)
	}
}

// TestParseConflicts_ThreeWay pins the diff3 base section: its marker line,
// its label, and that the current side stops at the base marker.
func TestParseConflicts_ThreeWay(t *testing.T) {
	lines := strings.Split(threeWay, "\n")
	got := ParseConflicts(lines)
	if len(got) != 1 {
		t.Fatalf("got %d blocks, want 1", len(got))
	}
	b := got[0]
	if b.Base != 3 || b.Mid != 5 || b.End != 7 || b.BaseLabel != "parent of 2a0370b (t1 change b)" {
		t.Fatalf("block = %+v", b)
	}
	if cur := b.CurrentLines(lines); !reflect.DeepEqual(cur, []string{"B-main"}) {
		t.Errorf("current = %q", cur)
	}
	if base := b.BaseLines(lines); !reflect.DeepEqual(base, []string{"b"}) {
		t.Errorf("base = %q", base)
	}
	if inc := b.IncomingLines(lines); !reflect.DeepEqual(inc, []string{"B-topic"}) {
		t.Errorf("incoming = %q", inc)
	}
}

// TestParseConflicts_RejectsLookalikes pins the strict grammar: a setext
// heading underline, an eight-character run, a glued label and an opener
// that never closes are all ordinary text.
func TestParseConflicts_RejectsLookalikes(t *testing.T) {
	cases := map[string]string{
		"setext heading":   "Title\n=======\nbody",
		"eight-char run":   "<<<<<<<< HEAD\nx\n=======\ny\n>>>>>>>> z",
		"glued label":      "<<<<<<<HEAD\nx\n=======\ny\n>>>>>>> z",
		"never closes":     "<<<<<<< HEAD\nx\n=======\ny",
		"no separator":     "<<<<<<< HEAD\nx\n>>>>>>> z",
		"labelled sep":     "<<<<<<< HEAD\nx\n======= y\ny\n>>>>>>> z",
		"indented markers": "  <<<<<<< HEAD\nx\n=======\ny\n  >>>>>>> z",
	}
	for name, text := range cases {
		if got := ParseConflicts(strings.Split(text, "\n")); len(got) != 0 {
			t.Errorf("%s: parsed %+v", name, got)
		}
	}
}

// TestParseConflicts_StrayOpenerDoesNotSwallowARealBlock pins the resume
// rule: an opener with no close is skipped, and the real block below it
// is still found.
func TestParseConflicts_StrayOpenerDoesNotSwallowARealBlock(t *testing.T) {
	text := "<<<<<<< stray\nx\n" + twoWay
	got := ParseConflicts(strings.Split(text, "\n"))
	if len(got) != 1 || got[0].Start != 3 {
		t.Fatalf("got %+v, want one block starting at line 3", got)
	}
}

// TestParseConflicts_SeveralBlocks pins document order and that the scan
// resumes after each block.
func TestParseConflicts_SeveralBlocks(t *testing.T) {
	text := twoWay + "\n" + twoWay
	got := ParseConflicts(strings.Split(text, "\n"))
	if len(got) != 2 || got[0].Start != 1 || got[1].Start != 8 {
		t.Fatalf("got %+v", got)
	}
}

// TestConflictBlock_RegionOf pins every region of a diff3 block, plus the
// lines outside it.
func TestConflictBlock_RegionOf(t *testing.T) {
	b := ParseConflicts(strings.Split(threeWay, "\n"))[0]
	want := []ConflictRegion{
		RegionNone, RegionStartMarker, RegionCurrent, RegionBaseMarker,
		RegionBase, RegionMidMarker, RegionIncoming, RegionEndMarker, RegionNone,
	}
	for line, w := range want {
		if got := b.RegionOf(line); got != w {
			t.Errorf("line %d: region %d, want %d", line, got, w)
		}
	}
}

// TestConflictBlock_Resolution pins what every choice produces, and that
// "base" is refused on a two-way block.
func TestConflictBlock_Resolution(t *testing.T) {
	lines := strings.Split(threeWay, "\n")
	b := ParseConflicts(lines)[0]
	cases := map[ConflictChoice][]string{
		ChooseCurrent:           {"B-main"},
		ChooseIncoming:          {"B-topic"},
		ChooseBoth:              {"B-main", "B-topic"},
		ChooseBothIncomingFirst: {"B-topic", "B-main"},
		ChooseBase:              {"b"},
		ChooseNeither:           nil,
	}
	for c, want := range cases {
		got, ok := b.Resolution(lines, c)
		if !ok || !reflect.DeepEqual(got, want) {
			t.Errorf("%s: got %q ok=%v, want %q", c.Label(), got, ok, want)
		}
	}
	two := strings.Split(twoWay, "\n")
	if _, ok := ParseConflicts(two)[0].Resolution(two, ChooseBase); ok {
		t.Error("ChooseBase accepted on a block with no base section")
	}
}

// TestResolveConflict_OneUndoStep pins the gesture contract: the block is
// rewritten, the tab is dirty and on a new revision, the caret sits where
// the conflict was, and one Undo restores the markers exactly.
func TestResolveConflict_OneUndoStep(t *testing.T) {
	tab := tabWith(twoWay)
	rev := tab.EditRev
	if !tab.ResolveConflict(0, ChooseIncoming) {
		t.Fatal("ResolveConflict refused a valid block")
	}
	if got := tab.Buffer.String(); got != "a\nB-topic\nc" {
		t.Fatalf("buffer = %q", got)
	}
	if !tab.Dirty || tab.EditRev == rev {
		t.Errorf("dirty=%v rev %d→%d: an edit must mark and bump", tab.Dirty, rev, tab.EditRev)
	}
	if tab.Cursor != (Position{Line: 1}) {
		t.Errorf("caret = %+v, want line 1 col 0", tab.Cursor)
	}
	if len(tab.Conflicts()) != 0 {
		t.Error("the scan still reports a conflict after resolving the only one")
	}
	if !tab.Undo() {
		t.Fatal("Undo refused")
	}
	if got := tab.Buffer.String(); got != twoWay {
		t.Fatalf("after undo = %q", got)
	}
	if len(tab.Conflicts()) != 1 {
		t.Error("undo did not bring the conflict back into the scan")
	}
}

// TestResolveConflict_RefusesWithoutTouching pins the refusal paths: a bad
// index and an impossible choice leave the buffer and history alone.
func TestResolveConflict_RefusesWithoutTouching(t *testing.T) {
	tab := tabWith(twoWay)
	depth := tab.UndoDepth()
	if tab.ResolveConflict(3, ChooseCurrent) {
		t.Error("accepted an index out of range")
	}
	if tab.ResolveConflict(0, ChooseBase) {
		t.Error("accepted ChooseBase on a two-way block")
	}
	if tab.Buffer.String() != twoWay || tab.Dirty || tab.UndoDepth() != depth {
		t.Error("a refused resolve touched the buffer or the history")
	}
}

// TestResolveAllConflicts_BottomUpOneStep is the test a top-down
// implementation fails: resolving the first block shrinks the file, and
// the second block must still be found at its original lines.
func TestResolveAllConflicts_BottomUpOneStep(t *testing.T) {
	text := twoWay + "\n" + twoWay
	tab := tabWith(text)
	depth := tab.UndoDepth()
	if n := tab.ResolveAllConflicts(ChooseCurrent); n != 2 {
		t.Fatalf("resolved %d, want 2", n)
	}
	if got := tab.Buffer.String(); got != "a\nB-main\nc\na\nB-main\nc" {
		t.Fatalf("buffer = %q", got)
	}
	if tab.UndoDepth() != depth+1 {
		t.Errorf("undo depth %d → %d, want exactly one step", depth, tab.UndoDepth())
	}
	tab.Undo()
	if tab.Buffer.String() != text {
		t.Error("one undo did not restore both blocks")
	}
}

// TestResolveConflict_WholeFileLeavesOneLine pins the Buffer invariant: a
// file that was nothing but a conflict, resolved to "neither", keeps one
// empty line rather than zero.
func TestResolveConflict_WholeFileLeavesOneLine(t *testing.T) {
	tab := tabWith("<<<<<<< HEAD\nx\n=======\ny\n>>>>>>> z")
	tab.ResolveConflict(0, ChooseNeither)
	if tab.Buffer.LineCount() != 1 || tab.Buffer.Lines[0] != "" {
		t.Fatalf("lines = %q, want one empty line", tab.Buffer.Lines)
	}
}

// TestConflicts_MemoizedByRevision pins the cache: the same revision
// returns the same scan, and an edit re-scans.
func TestConflicts_MemoizedByRevision(t *testing.T) {
	tab := tabWith(twoWay)
	first := tab.Conflicts()
	tab.Buffer.Lines = []string{"plain"} // a write behind the tab's back…
	if len(tab.Conflicts()) != len(first) {
		t.Error("same revision re-scanned instead of using the cache")
	}
	tab.EditRev++ // …only becomes visible once the revision moves
	if len(tab.Conflicts()) != 0 {
		t.Error("a new revision did not re-scan")
	}
}

// TestNextConflict pins forward/backward stepping, including the rule that
// "previous" from inside a block skips the block you are standing in.
func TestNextConflict(t *testing.T) {
	tab := tabWith(twoWay + "\n" + twoWay) // blocks at lines 1–5 and 8–12
	cases := []struct {
		from, dir, want int
		ok              bool
	}{
		{0, 1, 0, true},
		{1, 1, 1, true},   // standing on block 0's opener: next is block 1
		{9, 1, -1, false}, // inside the last block
		{9, -1, 0, true},  // inside block 1: previous is block 0
		{3, -1, -1, false},
		{13, -1, 1, true},
	}
	for _, c := range cases {
		got, ok := tab.NextConflict(c.from, c.dir)
		if got != c.want || ok != c.ok {
			t.Errorf("NextConflict(%d, %d) = %d,%v want %d,%v", c.from, c.dir, got, ok, c.want, c.ok)
		}
	}
}

// TestConflictAt pins which block a line belongs to, markers included.
func TestConflictAt(t *testing.T) {
	tab := tabWith(twoWay)
	for _, line := range []int{1, 3, 5} {
		if i, ok := tab.ConflictAt(line); !ok || i != 0 {
			t.Errorf("line %d: got %d,%v", line, i, ok)
		}
	}
	for _, line := range []int{0, 6} {
		if _, ok := tab.ConflictAt(line); ok {
			t.Errorf("line %d reported inside a block", line)
		}
	}
}

// TestConflictChoice_Label pins the shared verb names every door uses.
func TestConflictChoice_Label(t *testing.T) {
	if ChooseCurrent.Label() != "Accept current" || ChooseIncoming.Label() != "Accept incoming" ||
		ChooseBoth.Label() != "Accept both" {
		t.Error("the three lens verbs changed name")
	}
}
