// =============================================================================
// File: internal/app/bookmarks_test.go
// Author: Rohan Allison <rohanthewiz@gmail.com>
// Created: 2026-09-30
// Copyright: 2026 Rohan Allison. All rights reserved.
// =============================================================================

package app

import (
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/rohanthewiz/ced/internal/editor"
	"github.com/rohanthewiz/ced/internal/history"
)

// bmTestApp builds an app over a root with two small files and opens
// a.go. Returns the app and the two absolute paths.
func bmTestApp(t *testing.T) (*App, string, string) {
	t.Helper()
	root := t.TempDir()
	a := writeStatusTestFile(t, root, "a.go", "package a\n\nfunc A() {}\n\nfunc A2() {}\n")
	b := writeStatusTestFile(t, root, "b.go", "package b\n\nfunc B() {}\n")
	app := newTestApp(t, root)
	app.openFile(a)
	return app, a, b
}

// bmCaretTo parks the active tab's caret on line (col 0).
func bmCaretTo(a *App, line int) {
	a.activeTabPtr().MoveCursorTo(editor.Position{Line: line}, false)
}

// bookmarkSpots renders allBookmarks as "base:line1" strings.
func bookmarkSpots(a *App) []string {
	var out []string
	for _, r := range a.allBookmarks() {
		out = append(out, filepath.Base(r.path)+":"+strconv.Itoa(r.line+1))
	}
	return out
}

// TestMenuToggleBookmark_MarksTheCaretLineAndFlashes pins the ≡ row:
// on, flash, off, flash.
func TestMenuToggleBookmark_MarksTheCaretLineAndFlashes(t *testing.T) {
	a, _, _ := bmTestApp(t)
	bmCaretTo(a, 2)
	a.menuToggleBookmark()
	if !a.activeTabPtr().HasBookmark(2) || !strings.Contains(a.statusMsg, "Bookmarked line 3") {
		t.Fatalf("after toggle: has=%v flash=%q", a.activeTabPtr().HasBookmark(2), a.statusMsg)
	}
	a.menuToggleBookmark()
	if a.activeTabPtr().HasBookmark(2) || !strings.Contains(a.statusMsg, "Removed") {
		t.Errorf("after second toggle: has=%v flash=%q", a.activeTabPtr().HasBookmark(2), a.statusMsg)
	}
}

// TestMenuToggleBookmark_RefusesUntitledAndNoTab pins the two refusals,
// each of which says why.
func TestMenuToggleBookmark_RefusesUntitledAndNoTab(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	a.menuToggleBookmark()
	if !strings.Contains(a.statusMsg, "Open a file") {
		t.Errorf("no tab flash = %q", a.statusMsg)
	}
	tab, _ := editor.NewTab("")
	a.tabs = append(a.tabs, tab)
	a.activeTab = 0
	a.menuToggleBookmark()
	if tab.BookmarkCount() != 0 || !strings.Contains(a.statusMsg, "Save the file first") {
		t.Errorf("untitled: count=%d flash=%q", tab.BookmarkCount(), a.statusMsg)
	}
}

// TestToggleBookmark_RefusesPastTheCap pins MaxBookmarks: the bookmark
// that would pass it is refused out loud, removing one still works.
func TestToggleBookmark_RefusesPastTheCap(t *testing.T) {
	a, _, _ := bmTestApp(t)
	tab := a.activeTabPtr()
	tab.ToggleBookmark(0)
	parked := make([]editor.Bookmark, history.MaxBookmarks-1)
	for i := range parked {
		parked[i] = editor.Bookmark{Line: i}
	}
	a.bookmarks = map[string][]editor.Bookmark{"/elsewhere.go": parked}
	a.toggleBookmarkAt(tab, 2)
	if tab.HasBookmark(2) || !strings.Contains(a.statusMsg, "limit") {
		t.Errorf("past the cap: has=%v flash=%q", tab.HasBookmark(2), a.statusMsg)
	}
	a.toggleBookmarkAt(tab, 0)
	if tab.HasBookmark(0) {
		t.Error("removing a bookmark at the cap must still work")
	}
}

// TestStepBookmark_WalksTheProjectAndWraps pins Next / Previous: path
// then line order across open and parked files, past the caret's own
// bookmark, wrapping at both ends, with a "k/n" flash.
func TestStepBookmark_WalksTheProjectAndWraps(t *testing.T) {
	a, aPath, bPath := bmTestApp(t)
	a.activeTabPtr().ToggleBookmark(2)
	a.activeTabPtr().ToggleBookmark(4)
	a.bookmarks = map[string][]editor.Bookmark{bPath: {{Line: 2, Text: "func B() {}"}}}

	bmCaretTo(a, 2) // ON the first bookmark: next must move past it
	a.menuNextBookmark()
	if tab := a.activeTabPtr(); tab.Path != aPath || tab.Cursor.Line != 4 {
		t.Fatalf("next = %s:%d, want a.go:4", filepath.Base(tab.Path), tab.Cursor.Line)
	}
	if !strings.Contains(a.statusMsg, "Bookmark 2/3") {
		t.Errorf("flash = %q, want the position in the walk", a.statusMsg)
	}
	a.menuNextBookmark() // into the parked file: opens it
	if tab := a.activeTabPtr(); tab.Path != bPath || tab.Cursor.Line != 2 {
		t.Fatalf("next = %s:%d, want b.go:2", filepath.Base(tab.Path), tab.Cursor.Line)
	}
	if _, parked := a.bookmarks[bPath]; parked {
		t.Error("opening b.go should have handed its bookmarks to the tab")
	}
	a.menuNextBookmark() // wraps
	if tab := a.activeTabPtr(); tab.Path != aPath || tab.Cursor.Line != 2 {
		t.Fatalf("wrap = %s:%d, want a.go:2", filepath.Base(tab.Path), tab.Cursor.Line)
	}
	a.menuPrevBookmark() // wraps backwards
	if tab := a.activeTabPtr(); tab.Path != bPath || tab.Cursor.Line != 2 {
		t.Errorf("prev wrap = %s:%d, want b.go:2", filepath.Base(tab.Path), tab.Cursor.Line)
	}
}

// TestStepBookmark_RecordsNavAndExplainsEmpty pins that a jump within
// the file is a Go back step, and that with no bookmarks the verb says
// how to make one.
func TestStepBookmark_RecordsNavAndExplainsEmpty(t *testing.T) {
	a, _, _ := bmTestApp(t)
	a.menuNextBookmark()
	if !strings.Contains(a.statusMsg, "No bookmarks yet") {
		t.Fatalf("empty flash = %q", a.statusMsg)
	}
	a.activeTabPtr().ToggleBookmark(4)
	bmCaretTo(a, 0)
	a.menuNextBookmark()
	if !a.hasNavBack() {
		t.Fatal("a bookmark jump should be recorded in the nav history")
	}
	a.navBack()
	if line := a.activeTabPtr().Cursor.Line; line != 0 {
		t.Errorf("Go back landed on line %d, want 0", line)
	}
}

// TestStepBookmark_SkipsMissingFiles pins that a parked bookmark whose
// file is gone is stepped over (and kept), not jumped into.
func TestStepBookmark_SkipsMissingFiles(t *testing.T) {
	a, aPath, _ := bmTestApp(t)
	a.activeTabPtr().ToggleBookmark(2)
	gone := filepath.Join(filepath.Dir(aPath), "gone.go")
	a.bookmarks = map[string][]editor.Bookmark{gone: {{Line: 1}}}
	bmCaretTo(a, 2)
	a.menuNextBookmark()
	if tab := a.activeTabPtr(); tab.Path != aPath || tab.Cursor.Line != 2 {
		t.Errorf("next = %s:%d, want to stay on a.go:2 (the only reachable one)", filepath.Base(tab.Path), tab.Cursor.Line)
	}
	if len(a.bookmarks[gone]) != 1 {
		t.Error("a missing file's bookmark must be kept by the walk")
	}
}

// TestCloseAndReopen_ParksAndReanchors pins the two homes: closing the
// tab parks its bookmarks by path, and reopening the file after it
// changed on disk hands them back re-anchored by text.
func TestCloseAndReopen_ParksAndReanchors(t *testing.T) {
	a, aPath, _ := bmTestApp(t)
	a.activeTabPtr().ToggleBookmark(4) // "func A2() {}"
	a.closeTab(a.activeTab)
	if got := a.bookmarks[aPath]; len(got) != 1 || got[0].Text != "func A2() {}" {
		t.Fatalf("parked = %+v", got)
	}
	if err := os.WriteFile(aPath, []byte("package a\n\n// new\n// lines\nfunc A() {}\n\nfunc A2() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	a.openFile(aPath)
	if got := a.activeTabPtr().Bookmarks(); len(got) != 1 || got[0].Line != 6 {
		t.Errorf("adopted = %+v, want line 6 (moved down by two)", got)
	}
	if len(a.bookmarks) != 0 {
		t.Errorf("parked map should be empty once the file is open: %+v", a.bookmarks)
	}
}

// TestMenuBookmarks_ListsJumpsAndClears pins the picker: one row per
// bookmark with its line text, a jump on pick, and the clear rows.
func TestMenuBookmarks_ListsJumpsAndClears(t *testing.T) {
	a, _, bPath := bmTestApp(t)
	a.activeTabPtr().ToggleBookmark(2)
	a.bookmarks = map[string][]editor.Bookmark{bPath: {{Line: 2, Text: "func B() {}"}}}

	a.menuBookmarks()
	rows := pickerLabels(t, a)
	want := []string{"a.go:3  func A() {}", "b.go:3  func B() {}", "Clear bookmarks in a.go (1)", "Clear all bookmarks (2)"}
	if !reflect.DeepEqual(rows, want) {
		t.Fatalf("rows = %q, want %q", rows, want)
	}
	pickByLabel(t, a, "b.go:3")
	if tab := a.activeTabPtr(); tab.Path != bPath || tab.Cursor.Line != 2 {
		t.Fatalf("pick landed on %s:%d", filepath.Base(tab.Path), tab.Cursor.Line)
	}

	a.menuBookmarks()
	pickByLabel(t, a, "Clear all bookmarks")
	c := confirmOf(a)
	if c == nil {
		t.Fatal("Clear all should confirm")
	}
	c.yes(a)
	if n := a.bookmarkTotal(); n != 0 {
		t.Errorf("after Clear all: %d bookmarks left", n)
	}
	a.menuBookmarks()
	if a.modal != nil || !strings.Contains(a.statusMsg, "No bookmarks yet") {
		t.Errorf("empty list should flash, not open: modal=%T flash=%q", a.modal, a.statusMsg)
	}
}

// TestMenuBookmarks_MissingFileRowRemoves pins the picker's answer for
// a parked bookmark whose file is gone: labelled, and picking removes it.
func TestMenuBookmarks_MissingFileRowRemoves(t *testing.T) {
	a, aPath, _ := bmTestApp(t)
	gone := filepath.Join(filepath.Dir(aPath), "gone.go")
	a.bookmarks = map[string][]editor.Bookmark{gone: {{Line: 0, Text: "x"}}}
	a.menuBookmarks()
	pickByLabel(t, a, "gone.go:1  (missing")
	if a.bookmarkTotal() != 0 || !strings.Contains(a.statusMsg, "file is gone") {
		t.Errorf("total=%d flash=%q", a.bookmarkTotal(), a.statusMsg)
	}
}

// TestClearTabBookmarks_OnlyThatFile pins the per-file clear.
func TestClearTabBookmarks_OnlyThatFile(t *testing.T) {
	a, _, bPath := bmTestApp(t)
	a.activeTabPtr().ToggleBookmark(0)
	a.activeTabPtr().ToggleBookmark(2)
	a.bookmarks = map[string][]editor.Bookmark{bPath: {{Line: 1}}}
	a.clearTabBookmarks(a.activeTabPtr())
	if got := bookmarkSpots(a); !reflect.DeepEqual(got, []string{"b.go:2"}) {
		t.Errorf("left = %v, want only b.go's", got)
	}
}

// TestBookmarks_WrittenThroughAndRestored pins persistence: a toggle is
// in the repository's history database at once (no Close needed), and a
// fresh app over the same root gets it back on the reopened tab — with
// the session toggle OFF too.
func TestBookmarks_WrittenThroughAndRestored(t *testing.T) {
	a, aPath, bPath := bmTestApp(t)
	dbPath := historyPathFn(a.rootDir)
	bmCaretTo(a, 2)
	a.menuToggleBookmark()

	h, err := history.Load(a.rootDir, dbPath)
	if err != nil {
		t.Fatal(err)
	}
	if got := h.Bookmarks()[aPath]; !reflect.DeepEqual(got, []history.Bookmark{{Line: 2, Text: "func A() {}"}}) {
		t.Fatalf("on disk after one toggle = %+v", got)
	}
	a.bookmarks = map[string][]editor.Bookmark{bPath: {{Line: 2, Text: "func B() {}"}}}
	a.saveBookmarks()

	b := newTestApp(t, a.rootDir)
	historyPathFn = func(string) string { return dbPath }
	b.sessionEnabled = false
	b.loadBookmarks()
	b.openFile(aPath)
	if !b.activeTabPtr().HasBookmark(2) {
		t.Error("reopened a.go should carry its bookmark")
	}
	if got := bookmarkSpots(b); !reflect.DeepEqual(got, []string{"a.go:3", "b.go:3"}) {
		t.Errorf("restored = %v", got)
	}
}

// TestWriteHistory_CarriesBookmarkMoves pins that Close's history write
// stores where edits moved the bookmarks since the last toggle, and that
// clearing them all deletes the rows.
func TestWriteHistory_CarriesBookmarkMoves(t *testing.T) {
	a, aPath, _ := bmTestApp(t)
	dbPath := historyPathFn(a.rootDir)
	a.activeTabPtr().ToggleBookmark(2)
	bmCaretTo(a, 0)
	a.activeTabPtr().InsertString("// top\n")
	a.writeHistory()
	h, err := history.Load(a.rootDir, dbPath)
	if err != nil {
		t.Fatal(err)
	}
	if got := h.Bookmarks()[aPath]; len(got) != 1 || got[0].Line != 3 {
		t.Fatalf("written = %+v, want line 3", got)
	}
	a.clearAllBookmarks()
	if h, _ = history.Load(a.rootDir, dbPath); len(h.Bookmarks()) != 0 {
		t.Errorf("after Clear all the rows should be gone: %+v", h.Bookmarks())
	}
}

// TestBookmarksLabel_SaysNotSaved pins the ≡ row's held notice: the same
// "(not saved)" the other history rows carry when the database cannot
// be written.
func TestBookmarksLabel_SaysNotSaved(t *testing.T) {
	a, _, _ := bmTestApp(t)
	if got := a.bookmarksLabel(); got != "Bookmarks…" {
		t.Errorf("label = %q", got)
	}
	a.historyProbed, a.historyNoSave = true, os.ErrPermission
	if got := a.bookmarksLabel(); got != "Bookmarks… (not saved)" {
		t.Errorf("label = %q, want the (not saved) suffix", got)
	}
}

// TestBookmarkGutterPress_DoubleClickTogglesSingleDoesNot pins the mouse
// door through the real router: one press on a line number only moves
// the caret, a second on the same cell toggles that line, and a double
// click in the code still selects a word.
func TestBookmarkGutterPress_DoubleClickTogglesSingleDoesNot(t *testing.T) {
	a, _, _ := bmTestApp(t)
	a.draw()
	ex, ey, _, _ := a.editorRect()
	tab := a.activeTabPtr()

	clickAt(a, ex+2, ey+2)
	if tab.HasBookmark(2) {
		t.Fatal("a single click must not bookmark")
	}
	if tab.Cursor.Line != 2 {
		t.Errorf("single click should still place the caret: line %d", tab.Cursor.Line)
	}
	clickAt(a, ex+2, ey+2)
	if !tab.HasBookmark(2) {
		t.Fatal("a double click on the line number should bookmark the line")
	}
	if tab.HasSelection() {
		t.Error("the bookmark double-click must not also select a word")
	}

	// A slow second press is two single clicks.
	a.bookmarkClick.when = time.Now().Add(-time.Second)
	clickAt(a, ex+2, ey+2)
	if !tab.HasBookmark(2) {
		t.Error("a slow second press must not toggle")
	}

	// In the code, the double-click keeps its word-select meaning.
	gx := ex + 10
	clickAt(a, gx, ey+2)
	clickAt(a, gx, ey+2)
	if !tab.HasSelection() {
		t.Error("a double click in the code should still select a word")
	}
}

// TestEditorContext_BookmarkRowFollowsTheClickedLine pins the right-click
// row: labelled by state for the clicked line, and toggling that line.
func TestEditorContext_BookmarkRowFollowsTheClickedLine(t *testing.T) {
	a, _, _ := bmTestApp(t)
	a.draw()
	m := openEditorContextAt(t, a, 9, 2)
	i := contextRowIndex(m, "Add bookmark")
	if i < 0 {
		t.Fatalf("no Add bookmark row in %v", m.items)
	}
	a.closeModal()
	m.items[i].action(a)
	if !a.activeTabPtr().HasBookmark(2) {
		t.Fatal("the row should bookmark the clicked line")
	}
	m = openEditorContextAt(t, a, 9, 2)
	if contextRowIndex(m, "Remove bookmark") < 0 {
		t.Errorf("a bookmarked line should offer Remove bookmark")
	}
}

// TestBookmarksRenamed_RekeysParked pins the rename hook for a file and
// for a folder of parked bookmarks.
func TestBookmarksRenamed_RekeysParked(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	a.bookmarks = map[string][]editor.Bookmark{
		"/r/x.go":     {{Line: 1}},
		"/r/d/y.go":   {{Line: 2}},
		"/r/dz/z.go":  {{Line: 3}},
		"/r/other.go": {{Line: 4}},
	}
	a.bookmarksRenamed("/r/x.go", "/r/x2.go", false)
	a.bookmarksRenamed("/r/d", "/r/e", true)
	var keys []string
	for k := range a.bookmarks {
		keys = append(keys, k)
	}
	want := map[string]bool{"/r/x2.go": true, "/r/e/y.go": true, "/r/dz/z.go": true, "/r/other.go": true}
	if len(keys) != len(want) {
		t.Fatalf("keys = %v", keys)
	}
	for _, k := range keys {
		if !want[k] {
			t.Errorf("unexpected key %q (dz/ is not under d/)", k)
		}
	}
}

// TestRender_BookmarkFlagInTheApp pins that the flag reaches the real
// screen in the editor's gutter.
func TestRender_BookmarkFlagInTheApp(t *testing.T) {
	a, _, _ := bmTestApp(t)
	a.activeTabPtr().ToggleBookmark(2)
	a.draw()
	ex, ey, _, _ := a.editorRect()
	r, _, _, _ := a.screen.GetContent(ex, ey+2)
	if r != editor.BookmarkGlyph {
		t.Errorf("gutter cell = %q, want the bookmark flag", r)
	}
}
