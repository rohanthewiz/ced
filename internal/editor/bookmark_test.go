// =============================================================================
// File: internal/editor/bookmark_test.go
// Author: Rohan Allison <rohanthewiz@gmail.com>
// Created: 2026-09-30
// Copyright: 2026 Rohan Allison. All rights reserved.
// =============================================================================

package editor

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/rohanthewiz/ced/internal/theme"
)

// bmTab builds an in-memory tab over lines joined by newlines.
func bmTab(lines ...string) *Tab {
	return &Tab{Buffer: NewBuffer(strings.Join(lines, "\n")), StyleStale: true}
}

// bmLines is the tab's bookmarked line numbers, for compact asserts.
func bmLinesOf(t *Tab) []int {
	var out []int
	for _, b := range t.Bookmarks() {
		out = append(out, b.Line)
	}
	return out
}

// caretAt parks the caret (no selection) at line, col.
func caretAt(t *Tab, line, col int) {
	t.MoveCursorTo(Position{Line: line, Col: col}, false)
}

// selectRange selects from a to b.
func selectRange(t *Tab, a, b Position) {
	t.MoveCursorTo(a, false)
	t.MoveCursorTo(b, true)
}

// TestToggleBookmark_OnOffAndQueries pins the basic toggle contract:
// on, then off, with the queries agreeing at each step.
func TestToggleBookmark_OnOffAndQueries(t *testing.T) {
	tab := bmTab("a", "b", "c")
	if !tab.ToggleBookmark(1) {
		t.Fatal("first toggle should turn the bookmark on")
	}
	if !tab.HasBookmark(1) || tab.BookmarkCount() != 1 {
		t.Fatalf("after on: has=%v count=%d", tab.HasBookmark(1), tab.BookmarkCount())
	}
	if got := tab.Bookmarks(); got[0].Text != "b" {
		t.Errorf("Text = %q, want the line's content", got[0].Text)
	}
	if tab.ToggleBookmark(1) {
		t.Fatal("second toggle should turn it off")
	}
	if tab.HasBookmark(1) || tab.BookmarkCount() != 0 {
		t.Errorf("after off: has=%v count=%d", tab.HasBookmark(1), tab.BookmarkCount())
	}
}

// TestToggleBookmark_ClampsAndRefusesImages pins the edges: an out of
// range line lands on the last line, and an image tab takes none.
func TestToggleBookmark_ClampsAndRefusesImages(t *testing.T) {
	tab := bmTab("a", "b")
	tab.ToggleBookmark(99)
	if got := bmLinesOf(tab); !reflect.DeepEqual(got, []int{1}) {
		t.Errorf("clamped = %v, want [1]", got)
	}
	img := &Tab{Buffer: NewBuffer(""), Mode: imageMode}
	if img.ToggleBookmark(0) || img.BookmarkCount() != 0 {
		t.Error("an image tab must not take a bookmark")
	}
}

// TestBookmarks_ShiftWithInsertAndDeleteAbove pins the common case: a
// change above a bookmark moves it by the line delta, one below leaves
// it alone.
func TestBookmarks_ShiftWithInsertAndDeleteAbove(t *testing.T) {
	tab := bmTab("a", "b", "c", "d", "e")
	tab.ToggleBookmark(3) // "d"

	caretAt(tab, 0, 0)
	tab.InsertString("x\ny\n")
	if got := bmLinesOf(tab); !reflect.DeepEqual(got, []int{5}) {
		t.Fatalf("after inserting two lines above = %v, want [5]", got)
	}
	// Delete "x\n" (line 0) — one line up.
	selectRange(tab, Position{Line: 0, Col: 0}, Position{Line: 1, Col: 0})
	tab.Backspace()
	if got := bmLinesOf(tab); !reflect.DeepEqual(got, []int{4}) {
		t.Fatalf("after deleting a line above = %v, want [4]", got)
	}
	// An edit BELOW changes nothing.
	caretAt(tab, 5, 1)
	tab.InsertString("\nz")
	if got := bmLinesOf(tab); !reflect.DeepEqual(got, []int{4}) {
		t.Errorf("after editing below = %v, want [4]", got)
	}
	if b := tab.Bookmarks()[0]; b.Text != "d" {
		t.Errorf("bookmark drifted onto %q", b.Text)
	}
}

// TestBookmarks_StayOnALineBeingEdited pins that typing on the
// bookmarked line keeps the bookmark there, and that its Text follows.
func TestBookmarks_StayOnALineBeingEdited(t *testing.T) {
	tab := bmTab("a", "b", "c")
	tab.ToggleBookmark(1)
	caretAt(tab, 1, 1)
	tab.InsertRune('!')
	if got := tab.Bookmarks(); len(got) != 1 || got[0].Line != 1 || got[0].Text != "b!" {
		t.Errorf("after typing on the line = %+v", got)
	}
}

// TestBookmarks_EnterSplitsKeepTheHead pins Enter on a bookmarked line:
// at column 0 the line is pushed down and the bookmark goes with it; at
// the end the bookmark stays; mid-line it stays on the head.
func TestBookmarks_EnterSplitsKeepTheHead(t *testing.T) {
	tab := bmTab("a", "foo", "c")
	tab.ToggleBookmark(1)
	caretAt(tab, 1, 0)
	tab.InsertString("\n")
	if got := bmLinesOf(tab); !reflect.DeepEqual(got, []int{2}) {
		t.Fatalf("Enter at col 0 = %v, want [2]", got)
	}
	caretAt(tab, 2, 3)
	tab.InsertString("\n")
	if got := bmLinesOf(tab); !reflect.DeepEqual(got, []int{2}) {
		t.Fatalf("Enter at the end = %v, want [2]", got)
	}
	caretAt(tab, 2, 2)
	tab.InsertString("\n")
	if got := tab.Bookmarks(); got[0].Line != 2 || got[0].Text != "fo" {
		t.Errorf("Enter mid-line = %+v, want line 2 \"fo\"", got)
	}
}

// TestBookmarks_FollowMoveLines pins that a whole-line move, which
// rotates Buffer.Lines in place without any edit primitive, carries the
// bookmark with its line.
func TestBookmarks_FollowMoveLines(t *testing.T) {
	tab := bmTab("a", "B", "c", "d")
	tab.ToggleBookmark(1)
	caretAt(tab, 1, 0)
	tab.MoveLines(1)
	tab.MoveLines(1)
	if got := tab.Bookmarks(); got[0].Line != 3 || got[0].Text != "B" {
		t.Errorf("after moving the line down twice = %+v, want line 3", got)
	}
}

// TestBookmarks_FollowUndoAndRedo pins that undo / redo, which restore
// whole snapshots, put the bookmark back where its line went.
func TestBookmarks_FollowUndoAndRedo(t *testing.T) {
	tab := bmTab("a", "b", "c")
	tab.ToggleBookmark(2) // "c"
	caretAt(tab, 0, 0)
	tab.InsertString("x\ny\n")
	if got := bmLinesOf(tab); !reflect.DeepEqual(got, []int{4}) {
		t.Fatalf("after insert = %v", got)
	}
	tab.Undo()
	if got := bmLinesOf(tab); !reflect.DeepEqual(got, []int{2}) {
		t.Fatalf("after undo = %v, want [2]", got)
	}
	tab.Redo()
	if got := bmLinesOf(tab); !reflect.DeepEqual(got, []int{4}) {
		t.Errorf("after redo = %v, want [4]", got)
	}
}

// TestBookmarks_DeletingOneOfTwinLinesKeepsTheSurvivor pins the
// ambiguity rule: deleting the first of two identical lines reads to
// the comparison as deleting the second, and the bookmark on the second
// must land on the twin that is left, not on the line after it.
func TestBookmarks_DeletingOneOfTwinLinesKeepsTheSurvivor(t *testing.T) {
	tab := bmTab("a", "b", "b", "c")
	tab.ToggleBookmark(2)
	selectRange(tab, Position{Line: 1, Col: 0}, Position{Line: 2, Col: 0})
	tab.Backspace()
	if got := tab.Bookmarks(); len(got) != 1 || got[0].Line != 1 || got[0].Text != "b" {
		t.Errorf("after deleting the first twin = %+v, want line 1 \"b\"", got)
	}
}

// TestBookmarks_TypingOnABlankLineDoesNotHop pins the other side of the
// ambiguity rule: an in-place edit of a blank bookmarked line keeps the
// bookmark there even with a blank twin right above it.
func TestBookmarks_TypingOnABlankLineDoesNotHop(t *testing.T) {
	tab := bmTab("a", "", "", "c")
	tab.ToggleBookmark(2)
	caretAt(tab, 2, 0)
	tab.InsertRune('x')
	if got := tab.Bookmarks(); got[0].Line != 2 || got[0].Text != "x" {
		t.Errorf("after typing on the blank line = %+v, want line 2 \"x\"", got)
	}
}

// TestBookmarks_DeletedLinesMerge pins what happens when the bookmarked
// lines themselves are deleted: they settle on the line that follows,
// and two that land together become one.
func TestBookmarks_DeletedLinesMerge(t *testing.T) {
	tab := bmTab("a", "b", "c", "d")
	tab.ToggleBookmark(1)
	tab.ToggleBookmark(2)
	selectRange(tab, Position{Line: 1, Col: 0}, Position{Line: 3, Col: 0})
	tab.Backspace()
	if got := tab.Bookmarks(); len(got) != 1 || got[0].Line != 1 || got[0].Text != "d" {
		t.Errorf("after deleting both bookmarked lines = %+v, want one on line 1", got)
	}
}

// TestSetBookmarks_ReanchorsByText pins the restore path: a stored
// bookmark whose line moved on disk is found by its text, one whose
// line still reads the same stays, and a blank one is never searched
// for.
func TestSetBookmarks_ReanchorsByText(t *testing.T) {
	tab := bmTab("new", "new", "a", "", "func main() {", "b")
	tab.SetBookmarks([]Bookmark{
		{Line: 2, Text: "func main() {"}, // moved down two lines
		{Line: 5, Text: "b"},             // unchanged
		{Line: 1, Text: ""},              // blank: stays on 1
		{Line: 40, Text: "gone"},         // off the end and missing: clamped
	})
	if got := bmLinesOf(tab); !reflect.DeepEqual(got, []int{1, 4, 5}) {
		t.Errorf("restored = %v, want [1 4 5] (the clamped one merged onto 5)", got)
	}
}

// TestClearBookmarks_ReportsTheCount pins Clear's answer.
func TestClearBookmarks_ReportsTheCount(t *testing.T) {
	tab := bmTab("a", "b", "c")
	tab.ToggleBookmark(0)
	tab.ToggleBookmark(2)
	if n := tab.ClearBookmarks(); n != 2 || tab.BookmarkCount() != 0 {
		t.Errorf("ClearBookmarks = %d, count after = %d", n, tab.BookmarkCount())
	}
}

// TestBookmarks_FollowAnExternalReload pins the wholesale-replacement
// path: a file rewritten on disk with lines added above the bookmark is
// reloaded, and the bookmark follows its line.
func TestBookmarks_FollowAnExternalReload(t *testing.T) {
	path := filepath.Join(t.TempDir(), "f.txt")
	if err := os.WriteFile(path, []byte("a\nb\nc\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	tab, err := NewTab(path)
	if err != nil {
		t.Fatal(err)
	}
	tab.ToggleBookmark(1)
	if err := os.WriteFile(path, []byte("// header\n\na\nb\nc\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := tab.Reload(); err != nil {
		t.Fatal(err)
	}
	if got := tab.Bookmarks(); got[0].Line != 3 || got[0].Text != "b" {
		t.Errorf("after reload = %+v, want line 3 \"b\"", got)
	}
}

// TestRender_BookmarkFlagsTheLineNumber pins the gutter: the flag in the
// number's leading cell, the number in the accent colour, and nothing on
// an unmarked line.
func TestRender_BookmarkFlagsTheLineNumber(t *testing.T) {
	tab := bmTab("a", "b", "c")
	caretAt(tab, 0, 0)
	tab.ToggleBookmark(1)
	scr := noteScreen(t, tab, 30, 4)

	if row := noteRow(scr, 1, 30); !strings.HasPrefix(row, string(BookmarkGlyph)+"   2") {
		t.Errorf("bookmarked row = %q, want the flag before the number", row)
	}
	if row := noteRow(scr, 2, 30); strings.ContainsRune(row, BookmarkGlyph) {
		t.Errorf("unmarked row carries a flag: %q", row)
	}
	_, _, st, _ := scr.GetContent(4, 1)
	if fg, _, _ := st.Decompose(); fg != theme.Default().Accent {
		t.Errorf("bookmarked number fg = %v, want Accent", fg)
	}
}
