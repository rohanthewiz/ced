// =============================================================================
// File: internal/editor/tab_test.go
// Author: Spicer Matthews <spicer@cloudmanic.com>
// Created: 2026-04-30
// Copyright: 2026 Cloudmanic, LLC. All rights reserved.
// =============================================================================

// Tests for the Tab type — the per-file owner of buffer + view state. We
// cover the disk I/O (NewTab, Save, Reload), the editing primitives that
// wrap Buffer (InsertString, Backspace, Delete, DeleteSelection), the
// cursor/selection movement helpers, scroll clamping, and the Render and
// HitTest methods using a tcell SimulationScreen.

package editor

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/gdamore/tcell/v2"

	"github.com/rohanthewiz/ced/internal/theme"
)

// newSimScreen builds a SimulationScreen of the given dimensions, ready to
// have a Tab rendered into it. The caller is responsible for Fini.
func newSimScreen(t *testing.T, w, h int) tcell.SimulationScreen {
	t.Helper()
	scr := tcell.NewSimulationScreen("UTF-8")
	if err := scr.Init(); err != nil {
		t.Fatalf("scr.Init: %v", err)
	}
	scr.SetSize(w, h)
	return scr
}

// TestNewTab_ExistingFile loads a real file from disk and confirms the
// buffer matches its contents and Mtime is populated.
func TestNewTab_ExistingFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "f.txt")
	if err := os.WriteFile(path, []byte("hello\nworld"), 0644); err != nil {
		t.Fatalf("seed: %v", err)
	}

	tab, err := NewTab(path)
	if err != nil {
		t.Fatalf("NewTab: %v", err)
	}
	if tab.Buffer.String() != "hello\nworld" {
		t.Fatalf("buffer mismatch: %q", tab.Buffer.String())
	}
	if tab.Mtime.IsZero() {
		t.Fatal("expected mtime to be set")
	}
	if !tab.StyleStale {
		t.Fatal("new tab should mark styles stale")
	}
}

// TestNewTab_MissingFile creates a tab for a nonexistent path with an empty
// buffer — matches editor convention of "open" creating on first save.
func TestNewTab_MissingFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "ghost.txt")

	tab, err := NewTab(path)
	if err != nil {
		t.Fatalf("NewTab: %v", err)
	}
	if tab.Buffer.LineCount() != 1 || tab.Buffer.Lines[0] != "" {
		t.Fatalf("expected empty buffer, got %#v", tab.Buffer.Lines)
	}
	if !tab.Mtime.IsZero() {
		t.Fatal("missing-file tab should have zero mtime")
	}
}

// TestNewTab_EmptyPath produces a scratch tab with an empty buffer and no
// path — the "untitled" case.
func TestNewTab_EmptyPath(t *testing.T) {
	tab, err := NewTab("")
	if err != nil {
		t.Fatalf("NewTab: %v", err)
	}
	if tab.Path != "" {
		t.Fatalf("expected empty path, got %q", tab.Path)
	}
	if tab.DisplayName() != "untitled" {
		t.Fatalf("expected 'untitled', got %q", tab.DisplayName())
	}
}

// TestNewTab_UnreadableFile surfaces non-NotExist errors. We make a file
// unreadable by making its parent directory unsearchable; on Windows the
// mode bits don't behave the same way so we skip there.
func TestNewTab_UnreadableFile(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("permissions test not portable to Windows")
	}
	if os.Geteuid() == 0 {
		t.Skip("root bypasses file-mode checks")
	}
	dir := t.TempDir()
	sub := filepath.Join(dir, "locked")
	if err := os.Mkdir(sub, 0755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	target := filepath.Join(sub, "secret.txt")
	if err := os.WriteFile(target, []byte("nope"), 0644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	if err := os.Chmod(sub, 0000); err != nil {
		t.Fatalf("chmod: %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(sub, 0755) })

	if _, err := NewTab(target); err == nil {
		t.Fatal("expected error opening unreadable file")
	}
}

// TestTab_DisplayName returns the basename of Path for saved tabs.
func TestTab_DisplayName(t *testing.T) {
	tab := &Tab{Path: "/tmp/some/dir/code.go", Buffer: NewBuffer("")}
	if tab.DisplayName() != "code.go" {
		t.Fatalf("got %q", tab.DisplayName())
	}
}

// TestTab_Save_WritesAndClearsDirty round-trips a save to disk and confirms
// Dirty is cleared and Mtime refreshed.
func TestTab_Save_WritesAndClearsDirty(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "out.txt")

	tab, err := NewTab(path)
	if err != nil {
		t.Fatalf("NewTab: %v", err)
	}
	tab.InsertString("payload")
	if !tab.Dirty {
		t.Fatal("expected Dirty after insert")
	}
	if err := tab.Save(); err != nil {
		t.Fatalf("Save: %v", err)
	}
	if tab.Dirty {
		t.Fatal("Dirty should be cleared after save")
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if string(got) != "payload" {
		t.Fatalf("file contents = %q", got)
	}
	if tab.Mtime.IsZero() {
		t.Fatal("expected mtime after save")
	}
}

// TestTab_Save_NoPath rejects saving an untitled tab — caller must prompt.
func TestTab_Save_NoPath(t *testing.T) {
	tab := &Tab{Buffer: NewBuffer("hi")}
	if err := tab.Save(); err == nil {
		t.Fatal("expected error saving tab without a path")
	}
}

// TestTab_Save_WriteError surfaces write errors (e.g. unwritable directory).
func TestTab_Save_WriteError(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("permission semantics differ on Windows")
	}
	if os.Geteuid() == 0 {
		t.Skip("root bypasses file-mode checks")
	}
	dir := t.TempDir()
	if err := os.Chmod(dir, 0500); err != nil {
		t.Fatalf("chmod: %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(dir, 0755) })

	tab := &Tab{Path: filepath.Join(dir, "no.txt"), Buffer: NewBuffer("hi")}
	if err := tab.Save(); err == nil {
		t.Fatal("expected save error in unwritable directory")
	}
}

// TestTab_Reload_RereadsAndClampsCursor confirms that Reload picks up the
// fresh disk content and that the cursor is clamped into the new buffer.
func TestTab_Reload_RereadsAndClampsCursor(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "x.txt")
	if err := os.WriteFile(path, []byte("aaaa\nbbbb\ncccc"), 0644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	tab, err := NewTab(path)
	if err != nil {
		t.Fatalf("NewTab: %v", err)
	}
	// Park cursor far down; will be clamped after reload truncates.
	tab.Cursor = Position{Line: 2, Col: 4}
	tab.Anchor = Position{Line: 1, Col: 0}
	tab.Dirty = true

	if err := os.WriteFile(path, []byte("only one"), 0644); err != nil {
		t.Fatalf("rewrite: %v", err)
	}
	if err := tab.Reload(); err != nil {
		t.Fatalf("Reload: %v", err)
	}
	if tab.Buffer.String() != "only one" {
		t.Fatalf("buffer = %q", tab.Buffer.String())
	}
	if tab.Dirty {
		t.Fatal("Dirty should be cleared")
	}
	if tab.Cursor.Line != 0 || tab.Cursor.Col > len([]rune("only one")) {
		t.Fatalf("cursor not clamped: %+v", tab.Cursor)
	}
	if tab.Anchor != tab.Cursor {
		t.Fatal("Reload should drop selection")
	}
}

// TestTab_ReloadUndoable_UndoRestoresMine is the whole promise of the
// undoable reload: after the editor swaps the buffer for the disk copy
// on its own initiative, ONE undo gets the user's text back — and the
// tab is honestly dirty again, because it now differs from the file.
func TestTab_ReloadUndoable_UndoRestoresMine(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "x.txt")
	if err := os.WriteFile(path, []byte("original\n"), 0644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	tab, err := NewTab(path)
	if err != nil {
		t.Fatalf("NewTab: %v", err)
	}
	tab.InsertString("mine ")
	mine := tab.Buffer.String()

	if err := os.WriteFile(path, []byte("theirs\n"), 0644); err != nil {
		t.Fatalf("rewrite: %v", err)
	}
	if err := tab.ReloadUndoable(); err != nil {
		t.Fatalf("ReloadUndoable: %v", err)
	}
	if got := tab.Buffer.String(); got != "theirs\n" {
		t.Fatalf("after reload buffer = %q, want the disk copy", got)
	}
	if tab.Dirty {
		t.Fatal("a fresh reload matches disk, so the tab must be clean")
	}

	if !tab.Undo() {
		t.Fatal("undo should have a step to take")
	}
	if got := tab.Buffer.String(); got != mine {
		t.Fatalf("after undo buffer = %q, want %q", got, mine)
	}
	if !tab.Dirty {
		t.Fatal("restored edits differ from disk — the tab must be dirty")
	}
	// …and redo returns to the disk copy, so the step is a real
	// two-way history entry rather than a one-shot escape hatch.
	if !tab.Redo() || tab.Buffer.String() != "theirs\n" {
		t.Fatalf("redo should return to the disk copy, got %q", tab.Buffer.String())
	}
}

// TestTab_ReloadUndoable_FailureLeavesBufferAlone pins the error path:
// an unreadable file must not cost the user their edits (the reload
// never happened, so there is nothing to undo either).
func TestTab_ReloadUndoable_FailureLeavesBufferAlone(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "x.txt")
	if err := os.WriteFile(path, []byte("original\n"), 0644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	tab, err := NewTab(path)
	if err != nil {
		t.Fatalf("NewTab: %v", err)
	}
	tab.InsertString("mine ")
	mine := tab.Buffer.String()

	if err := os.Remove(path); err != nil {
		t.Fatalf("remove: %v", err)
	}
	if err := tab.ReloadUndoable(); err == nil {
		t.Fatal("expected an error reloading a deleted file")
	}
	if got := tab.Buffer.String(); got != mine {
		t.Fatalf("failed reload changed the buffer: %q", got)
	}
}

// reloadAsEditFixture opens a real file as a Tab. The shared setup for
// the ReloadAsEdit tests, which all need a path a formatter could
// plausibly have rewritten underneath them.
func reloadAsEditFixture(t *testing.T, initial string) (*Tab, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "main.go")
	if err := os.WriteFile(path, []byte(initial), 0644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	tab, err := NewTab(path)
	if err != nil {
		t.Fatalf("NewTab: %v", err)
	}
	return tab, path
}

// TestTab_ReloadAsEdit_PreservesUndoHistory is the regression this
// method exists for. Format-on-save used to adopt the formatter's
// rewrite with a plain Reload, whose initUndo nils both stacks — so with
// auto-save on, the user's entire history was destroyed on every idle
// pause and undo appeared to be broken. The whole history must survive,
// with the reformat sitting on top of it as one step.
func TestTab_ReloadAsEdit_PreservesUndoHistory(t *testing.T) {
	tab, path := reloadAsEditFixture(t, "one\n")
	tab.InsertString("two\n")
	first := tab.Buffer.String()
	tab.InsertString("three\n")
	typed := tab.Buffer.String()
	depth := tab.UndoDepth()

	if err := os.WriteFile(path, []byte("formatted\n"), 0644); err != nil {
		t.Fatalf("rewrite: %v", err)
	}
	changed, err := tab.ReloadAsEdit()
	if err != nil {
		t.Fatalf("ReloadAsEdit: %v", err)
	}
	if !changed {
		t.Fatal("rewritten content must report changed")
	}
	if got := tab.Buffer.String(); got != "formatted\n" {
		t.Fatalf("buffer = %q, want the formatted text", got)
	}
	if got := tab.UndoDepth(); got != depth+1 {
		t.Fatalf("undo depth = %d, want %d — the reformat is exactly one step", got, depth+1)
	}

	// One press takes back the reformat…
	if !tab.Undo() || tab.Buffer.String() != typed {
		t.Fatalf("first undo = %q, want the typed text %q", tab.Buffer.String(), typed)
	}
	// …and the history underneath it is still there, which is the point.
	if !tab.Undo() || tab.Buffer.String() != first {
		t.Fatalf("second undo = %q, want %q", tab.Buffer.String(), first)
	}
	// The revert anchor was never re-seeded, so Revert still means "the
	// file as I opened it" rather than "before the last reformat".
	if !tab.RevertFile() || tab.Buffer.String() != "one\n" {
		t.Fatalf("revert = %q, want the on-open text", tab.Buffer.String())
	}
}

// TestTab_ReloadAsEdit_IdenticalContentIsNoOp pins the rule that keeps a
// repeating background formatter from filling the undo stack: gofmt on
// already-formatted code rewrites nothing, and a run that moved no bytes
// must cost no undo step and no EditRev bump. The mtime is still adopted
// — the write was ours either way, and leaving it stale would make the
// reconcile tick read it as somebody else's edit.
func TestTab_ReloadAsEdit_IdenticalContentIsNoOp(t *testing.T) {
	tab, path := reloadAsEditFixture(t, "same\n")
	tab.InsertString("mine ")
	if err := tab.Save(); err != nil {
		t.Fatalf("save: %v", err)
	}
	text, depth, rev, stale := tab.Buffer.String(), tab.UndoDepth(), tab.EditRev, tab.StyleStale

	// Rewrite the identical bytes with a newer mtime, exactly as a
	// no-change formatter run leaves things.
	future := time.Now().Add(2 * time.Second)
	if err := os.WriteFile(path, []byte(text), 0644); err != nil {
		t.Fatalf("rewrite: %v", err)
	}
	if err := os.Chtimes(path, future, future); err != nil {
		t.Fatalf("chtimes: %v", err)
	}

	changed, err := tab.ReloadAsEdit()
	if err != nil {
		t.Fatalf("ReloadAsEdit: %v", err)
	}
	if changed {
		t.Fatal("identical content must report unchanged")
	}
	if tab.UndoDepth() != depth {
		t.Fatalf("undo depth moved from %d to %d on a no-op reload", depth, tab.UndoDepth())
	}
	if tab.EditRev != rev {
		t.Fatalf("EditRev moved from %d to %d on a no-op reload", rev, tab.EditRev)
	}
	if tab.StyleStale != stale {
		t.Fatal("a no-op reload must not invalidate the style grid")
	}
	if !tab.Mtime.Equal(future) {
		t.Fatalf("mtime = %v, want the write's %v", tab.Mtime, future)
	}
}

// TestTab_ReloadAsEdit_PreservesView pins that adopting a reformat does
// not move the user's viewport. RestoreView (not a plain cursor write)
// is what makes that true: it clamps into the new buffer, keeps both
// scroll offsets, and leaves cursorMoved false so the next Render can't
// yank a reader back to their caret.
func TestTab_ReloadAsEdit_PreservesView(t *testing.T) {
	tab, path := reloadAsEditFixture(t, "a\nb\nc\nd\ne\nf\n")
	tab.Cursor = Position{Line: 5, Col: 1}
	tab.Anchor = tab.Cursor
	tab.ScrollY, tab.ScrollX = 3, 2
	tab.cursorMoved = false

	// A shorter file, so the cursor has to clamp rather than survive.
	if err := os.WriteFile(path, []byte("a\nb\n"), 0644); err != nil {
		t.Fatalf("rewrite: %v", err)
	}
	if _, err := tab.ReloadAsEdit(); err != nil {
		t.Fatalf("ReloadAsEdit: %v", err)
	}
	if tab.Cursor.Line >= len(tab.Buffer.Lines) {
		t.Fatalf("cursor line %d escaped the shortened buffer (%d lines)",
			tab.Cursor.Line, len(tab.Buffer.Lines))
	}
	if tab.ScrollY != 3 || tab.ScrollX != 2 {
		t.Fatalf("scroll = (%d,%d), want the captured (3,2)", tab.ScrollY, tab.ScrollX)
	}
	if tab.cursorMoved {
		t.Fatal("cursorMoved must stay false — the captured scroll is part of what's preserved")
	}
}

// TestTab_ReloadAsEdit_CaretsFollowTheContent pins both halves of the
// multi-caret rule. A changed file moves every column the carets were
// measured against, so they go; an unchanged one moved nothing, and
// dropping a column of carets every idle pause would be its own bug.
func TestTab_ReloadAsEdit_CaretsFollowTheContent(t *testing.T) {
	tab, path := reloadAsEditFixture(t, "x\ny\n")
	tab.Carets = []Caret{{Cursor: Position{Line: 1}, Anchor: Position{Line: 1}}}
	if _, err := tab.ReloadAsEdit(); err != nil { // identical content
		t.Fatalf("ReloadAsEdit: %v", err)
	}
	if len(tab.Carets) != 1 {
		t.Fatal("an unchanged file moved nothing — the carets must survive")
	}

	if err := os.WriteFile(path, []byte("xx\nyy\n"), 0644); err != nil {
		t.Fatalf("rewrite: %v", err)
	}
	if _, err := tab.ReloadAsEdit(); err != nil {
		t.Fatalf("ReloadAsEdit: %v", err)
	}
	if tab.Carets != nil {
		t.Fatal("changed content invalidates every secondary caret's position")
	}
}

// TestTab_ReloadAsEdit_ClearsDirty pins the flag that stops a feedback
// loop: the buffer now matches disk, and a tab left dirty would bump
// EditRev, re-arm the auto-save timer, save, format, reload — forever.
func TestTab_ReloadAsEdit_ClearsDirty(t *testing.T) {
	tab, path := reloadAsEditFixture(t, "seed\n")
	tab.InsertString("typing ")
	if err := os.WriteFile(path, []byte("formatted\n"), 0644); err != nil {
		t.Fatalf("rewrite: %v", err)
	}
	if _, err := tab.ReloadAsEdit(); err != nil {
		t.Fatalf("ReloadAsEdit: %v", err)
	}
	if tab.Dirty {
		t.Fatal("the buffer matches disk — the tab must be clean")
	}
	// And undoing it correctly goes dirty again: the buffer now holds
	// text the file does not.
	if !tab.Undo() || !tab.Dirty {
		t.Fatal("undoing a reformat must leave the tab needing a save")
	}
}

// TestTab_ReloadAsEdit_GuardsRunOnReRead pins that the formatter's
// output gets no weaker check than the user's: a file that turned binary
// is refused, and a refusal leaves the buffer and the history untouched.
func TestTab_ReloadAsEdit_GuardsRunOnReRead(t *testing.T) {
	tab, path := reloadAsEditFixture(t, "text\n")
	tab.InsertString("mine ")
	mine, depth := tab.Buffer.String(), tab.UndoDepth()

	if err := os.WriteFile(path, []byte("bin\x00ary\n"), 0644); err != nil {
		t.Fatalf("rewrite: %v", err)
	}
	changed, err := tab.ReloadAsEdit()
	if err == nil {
		t.Fatal("expected a refusal for a file that turned binary")
	}
	if changed {
		t.Fatal("a refused reload changed nothing, so it must report unchanged")
	}
	if tab.Buffer.String() != mine || tab.UndoDepth() != depth {
		t.Fatal("a refused reload must leave the buffer and the history alone")
	}
}

// TestTab_ReloadAsEdit_NoPath delegates to Reload for an untitled tab —
// there is no file to re-read, so it must fail the same way.
func TestTab_ReloadAsEdit_NoPath(t *testing.T) {
	tab := &Tab{Buffer: NewBuffer("")}
	if _, err := tab.ReloadAsEdit(); err == nil {
		t.Fatal("expected error reloading untitled tab")
	}
}

// TestTab_Reload_NoPath returns an error for untitled tabs.
func TestTab_Reload_NoPath(t *testing.T) {
	tab := &Tab{Buffer: NewBuffer("")}
	if err := tab.Reload(); err == nil {
		t.Fatal("expected error reloading untitled tab")
	}
}

// TestTab_Reload_VanishedFile returns an error when the file disappears
// between opens.
func TestTab_Reload_VanishedFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "x.txt")
	if err := os.WriteFile(path, []byte("hi"), 0644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	tab, err := NewTab(path)
	if err != nil {
		t.Fatalf("NewTab: %v", err)
	}
	if err := os.Remove(path); err != nil {
		t.Fatalf("remove: %v", err)
	}
	if err := tab.Reload(); err == nil {
		t.Fatal("expected error reloading vanished file")
	}
}

// TestTab_HasSelection_AndSelectionText covers the selection accessors for
// (a) no selection, (b) anchor before cursor, (c) anchor after cursor.
func TestTab_HasSelection_AndSelectionText(t *testing.T) {
	tab := &Tab{Buffer: NewBuffer("hello world")}

	// (a) Empty selection.
	if tab.HasSelection() {
		t.Fatal("fresh tab should have no selection")
	}
	if tab.SelectionText() != "" {
		t.Fatal("expected empty selection text")
	}

	// (b) Anchor before cursor.
	tab.Anchor = Position{Line: 0, Col: 0}
	tab.Cursor = Position{Line: 0, Col: 5}
	if !tab.HasSelection() {
		t.Fatal("expected selection")
	}
	if tab.SelectionText() != "hello" {
		t.Fatalf("got %q", tab.SelectionText())
	}

	// (c) Anchor after cursor — Substring returns document order.
	tab.Anchor = Position{Line: 0, Col: 11}
	tab.Cursor = Position{Line: 0, Col: 6}
	if tab.SelectionText() != "world" {
		t.Fatalf("got %q", tab.SelectionText())
	}
}

// TestTab_DeleteSelection removes the selected range and collapses both
// cursor and anchor to the start.
func TestTab_DeleteSelection(t *testing.T) {
	tab := &Tab{Buffer: NewBuffer("hello world")}
	tab.Anchor = Position{Line: 0, Col: 5}
	tab.Cursor = Position{Line: 0, Col: 11}
	tab.DeleteSelection()
	if tab.Buffer.String() != "hello" {
		t.Fatalf("got %q", tab.Buffer.String())
	}
	if tab.Cursor != tab.Anchor || tab.Cursor.Col != 5 {
		t.Fatalf("cursor not collapsed: %+v / %+v", tab.Cursor, tab.Anchor)
	}
	if !tab.Dirty || !tab.StyleStale {
		t.Fatal("expected Dirty + StyleStale set")
	}
}

// TestTab_DeleteSelection_NoSelection is a no-op when nothing is selected.
func TestTab_DeleteSelection_NoSelection(t *testing.T) {
	tab := &Tab{Buffer: NewBuffer("hello")}
	tab.DeleteSelection()
	if tab.Buffer.String() != "hello" {
		t.Fatalf("buffer changed: %q", tab.Buffer.String())
	}
	if tab.Dirty {
		t.Fatal("should not become dirty")
	}
}

// TestTab_InsertString_ReplacesSelection inserts text after first deleting
// any active selection.
func TestTab_InsertString_ReplacesSelection(t *testing.T) {
	tab := &Tab{Buffer: NewBuffer("hello world")}
	tab.Anchor = Position{Line: 0, Col: 6}
	tab.Cursor = Position{Line: 0, Col: 11}
	tab.InsertString("there")
	if tab.Buffer.String() != "hello there" {
		t.Fatalf("got %q", tab.Buffer.String())
	}
	if tab.Cursor.Col != 11 {
		t.Fatalf("cursor wrong: %+v", tab.Cursor)
	}
}

// TestTab_InsertRune is the one-rune wrapper around InsertString.
func TestTab_InsertRune(t *testing.T) {
	tab := &Tab{Buffer: NewBuffer("ab")}
	tab.Cursor = Position{Line: 0, Col: 1}
	tab.Anchor = tab.Cursor
	tab.InsertRune('X')
	if tab.Buffer.String() != "aXb" {
		t.Fatalf("got %q", tab.Buffer.String())
	}
}

// TestTab_Backspace_MidLine deletes the rune to the left of the cursor.
func TestTab_Backspace_MidLine(t *testing.T) {
	tab := &Tab{Buffer: NewBuffer("hello")}
	tab.Cursor = Position{Line: 0, Col: 5}
	tab.Anchor = tab.Cursor
	tab.Backspace()
	if tab.Buffer.String() != "hell" {
		t.Fatalf("got %q", tab.Buffer.String())
	}
	if tab.Cursor.Col != 4 {
		t.Fatalf("cursor wrong: %+v", tab.Cursor)
	}
}

// TestTab_Backspace_StartOfBuffer is a no-op at line 0 col 0.
func TestTab_Backspace_StartOfBuffer(t *testing.T) {
	tab := &Tab{Buffer: NewBuffer("hi")}
	tab.Cursor = Position{Line: 0, Col: 0}
	tab.Anchor = tab.Cursor
	tab.Backspace()
	if tab.Buffer.String() != "hi" {
		t.Fatalf("buffer changed: %q", tab.Buffer.String())
	}
	if tab.Dirty {
		t.Fatal("should not be dirty")
	}
}

// TestTab_Backspace_JoinsLines deletes the implicit '\n' when at column 0
// of a non-first line.
func TestTab_Backspace_JoinsLines(t *testing.T) {
	tab := &Tab{Buffer: NewBuffer("hello\nworld")}
	tab.Cursor = Position{Line: 1, Col: 0}
	tab.Anchor = tab.Cursor
	tab.Backspace()
	if tab.Buffer.String() != "helloworld" {
		t.Fatalf("got %q", tab.Buffer.String())
	}
	if tab.Cursor != (Position{Line: 0, Col: 5}) {
		t.Fatalf("cursor wrong: %+v", tab.Cursor)
	}
}

// TestTab_Backspace_DeletesSelection prefers the selection over the rune.
func TestTab_Backspace_DeletesSelection(t *testing.T) {
	tab := &Tab{Buffer: NewBuffer("hello")}
	tab.Anchor = Position{Line: 0, Col: 1}
	tab.Cursor = Position{Line: 0, Col: 4}
	tab.Backspace()
	if tab.Buffer.String() != "ho" {
		t.Fatalf("got %q", tab.Buffer.String())
	}
}

// TestTab_Delete_MidLine removes the rune to the right of the cursor.
func TestTab_Delete_MidLine(t *testing.T) {
	tab := &Tab{Buffer: NewBuffer("hello")}
	tab.Cursor = Position{Line: 0, Col: 0}
	tab.Anchor = tab.Cursor
	tab.Delete()
	if tab.Buffer.String() != "ello" {
		t.Fatalf("got %q", tab.Buffer.String())
	}
}

// TestTab_Delete_EndOfBuffer is a no-op when the cursor is past the last
// rune of the document.
func TestTab_Delete_EndOfBuffer(t *testing.T) {
	tab := &Tab{Buffer: NewBuffer("hi")}
	tab.Cursor = Position{Line: 0, Col: 2}
	tab.Anchor = tab.Cursor
	tab.Delete()
	if tab.Buffer.String() != "hi" {
		t.Fatalf("buffer changed: %q", tab.Buffer.String())
	}
}

// TestTab_Delete_JoinsLines deletes the line break at end-of-line.
func TestTab_Delete_JoinsLines(t *testing.T) {
	tab := &Tab{Buffer: NewBuffer("hello\nworld")}
	tab.Cursor = Position{Line: 0, Col: 5}
	tab.Anchor = tab.Cursor
	tab.Delete()
	if tab.Buffer.String() != "helloworld" {
		t.Fatalf("got %q", tab.Buffer.String())
	}
}

// TestTab_Delete_DeletesSelection prefers the selection.
func TestTab_Delete_DeletesSelection(t *testing.T) {
	tab := &Tab{Buffer: NewBuffer("hello")}
	tab.Anchor = Position{Line: 0, Col: 0}
	tab.Cursor = Position{Line: 0, Col: 3}
	tab.Delete()
	if tab.Buffer.String() != "lo" {
		t.Fatalf("got %q", tab.Buffer.String())
	}
}

// TestTab_MoveCursor_Basic walks the cursor across simple line/col deltas.
func TestTab_MoveCursor_Basic(t *testing.T) {
	tab := &Tab{Buffer: NewBuffer("aaa\nbbbb\nc")}
	tab.Cursor = Position{Line: 0, Col: 0}
	tab.Anchor = tab.Cursor

	tab.MoveCursor(0, 2, false)
	if tab.Cursor != (Position{Line: 0, Col: 2}) {
		t.Fatalf("after right: %+v", tab.Cursor)
	}
	tab.MoveCursor(1, 0, false)
	if tab.Cursor.Line != 1 {
		t.Fatalf("after down: %+v", tab.Cursor)
	}
}

// TestTab_MoveCursor_ClampsAtEdges keeps the cursor within bounds when the
// caller asks for a delta past the start or end of the buffer.
func TestTab_MoveCursor_ClampsAtEdges(t *testing.T) {
	tab := &Tab{Buffer: NewBuffer("a\nb\nc")}
	tab.Cursor = Position{Line: 0, Col: 0}
	tab.Anchor = tab.Cursor

	tab.MoveCursor(-99, 0, false)
	if tab.Cursor.Line != 0 {
		t.Fatalf("up clamp: %+v", tab.Cursor)
	}
	tab.MoveCursor(99, 0, false)
	if tab.Cursor.Line != tab.Buffer.LineCount()-1 {
		t.Fatalf("down clamp: %+v", tab.Cursor)
	}
}

// TestTab_MoveCursor_WrapsAtLineEdges wraps to neighbouring lines when col
// goes below zero or past end.
func TestTab_MoveCursor_WrapsAtLineEdges(t *testing.T) {
	tab := &Tab{Buffer: NewBuffer("ab\ncd")}

	// Past end of line 0 wraps to start of line 1.
	tab.Cursor = Position{Line: 0, Col: 2}
	tab.Anchor = tab.Cursor
	tab.MoveCursor(0, 1, false)
	if tab.Cursor != (Position{Line: 1, Col: 0}) {
		t.Fatalf("forward wrap: %+v", tab.Cursor)
	}

	// Before start of line 1 wraps to end of line 0.
	tab.Cursor = Position{Line: 1, Col: 0}
	tab.Anchor = tab.Cursor
	tab.MoveCursor(0, -1, false)
	if tab.Cursor != (Position{Line: 0, Col: 2}) {
		t.Fatalf("backward wrap: %+v", tab.Cursor)
	}

	// At document start, going left clamps to col 0.
	tab.Cursor = Position{Line: 0, Col: 0}
	tab.Anchor = tab.Cursor
	tab.MoveCursor(0, -1, false)
	if tab.Cursor != (Position{Line: 0, Col: 0}) {
		t.Fatalf("left at start: %+v", tab.Cursor)
	}

	// At document end, going right clamps at end of last line.
	tab.Cursor = Position{Line: 1, Col: 2}
	tab.Anchor = tab.Cursor
	tab.MoveCursor(0, 1, false)
	if tab.Cursor != (Position{Line: 1, Col: 2}) {
		t.Fatalf("right at end: %+v", tab.Cursor)
	}
}

// TestTab_MoveCursor_Extend leaves Anchor in place when extend=true.
func TestTab_MoveCursor_Extend(t *testing.T) {
	tab := &Tab{Buffer: NewBuffer("abcdef")}
	tab.Cursor = Position{Line: 0, Col: 1}
	tab.Anchor = tab.Cursor
	tab.MoveCursor(0, 3, true)
	if tab.Anchor != (Position{Line: 0, Col: 1}) {
		t.Fatalf("anchor moved: %+v", tab.Anchor)
	}
	if tab.Cursor != (Position{Line: 0, Col: 4}) {
		t.Fatalf("cursor wrong: %+v", tab.Cursor)
	}
}

// TestTab_MoveCursor_DownAdjustsCol clamps Col when the new line is shorter.
func TestTab_MoveCursor_DownAdjustsCol(t *testing.T) {
	tab := &Tab{Buffer: NewBuffer("longer line\nshort")}
	tab.Cursor = Position{Line: 0, Col: 11}
	tab.Anchor = tab.Cursor
	tab.MoveCursor(1, 0, false)
	if tab.Cursor.Line != 1 || tab.Cursor.Col != 5 {
		t.Fatalf("cursor wrong: %+v", tab.Cursor)
	}
}

// TestTab_MoveCursorTo clamps the target into the buffer.
func TestTab_MoveCursorTo(t *testing.T) {
	tab := &Tab{Buffer: NewBuffer("abc\nde")}
	tab.MoveCursorTo(Position{Line: 99, Col: 99}, false)
	if tab.Cursor != (Position{Line: 1, Col: 2}) {
		t.Fatalf("not clamped: %+v", tab.Cursor)
	}
	if tab.Anchor != tab.Cursor {
		t.Fatal("expected anchor moved with cursor")
	}

	// extend=true keeps anchor.
	tab.Anchor = Position{Line: 0, Col: 0}
	tab.MoveCursorTo(Position{Line: 0, Col: 2}, true)
	if tab.Anchor != (Position{Line: 0, Col: 0}) {
		t.Fatalf("anchor moved: %+v", tab.Anchor)
	}
}

// TestTab_MoveLineHome_End covers both home and end movement.
func TestTab_MoveLineHome_End(t *testing.T) {
	tab := &Tab{Buffer: NewBuffer("hello")}
	tab.Cursor = Position{Line: 0, Col: 3}
	tab.Anchor = tab.Cursor

	tab.MoveLineHome(false)
	if tab.Cursor.Col != 0 {
		t.Fatalf("home: %+v", tab.Cursor)
	}
	tab.MoveLineEnd(false)
	if tab.Cursor.Col != 5 {
		t.Fatalf("end: %+v", tab.Cursor)
	}

	// extend=true preserves anchor.
	tab.Anchor = Position{Line: 0, Col: 2}
	tab.Cursor = Position{Line: 0, Col: 4}
	tab.MoveLineHome(true)
	if tab.Anchor != (Position{Line: 0, Col: 2}) {
		t.Fatal("anchor moved on extend home")
	}
	tab.MoveLineEnd(true)
	if tab.Anchor != (Position{Line: 0, Col: 2}) {
		t.Fatal("anchor moved on extend end")
	}
}

// TestTab_SelectAll spans the whole buffer.
func TestTab_SelectAll(t *testing.T) {
	tab := &Tab{Buffer: NewBuffer("foo\nbar")}
	tab.SelectAll()
	if tab.Anchor != (Position{Line: 0, Col: 0}) {
		t.Fatalf("anchor wrong: %+v", tab.Anchor)
	}
	if tab.Cursor != (Position{Line: 1, Col: 3}) {
		t.Fatalf("cursor wrong: %+v", tab.Cursor)
	}
}

// TestTab_EnsureVisible_Scrolls walks the cursor off-screen in each
// direction and confirms ScrollY/ScrollX move to bring it back.
func TestTab_EnsureVisible_Scrolls(t *testing.T) {
	tab := &Tab{Buffer: NewBuffer(strings.Repeat("xxxxxxxxxxxxxxxxxxxx\n", 50))}

	// Cursor below viewport.
	tab.Cursor = Position{Line: 30, Col: 0}
	tab.EnsureVisible(40, 10)
	if tab.ScrollY != 30-10+1 {
		t.Fatalf("ScrollY = %d", tab.ScrollY)
	}

	// Cursor above viewport.
	tab.ScrollY = 20
	tab.Cursor = Position{Line: 5, Col: 0}
	tab.EnsureVisible(40, 10)
	if tab.ScrollY != 5 {
		t.Fatalf("ScrollY = %d", tab.ScrollY)
	}

	// Cursor right of viewport.
	tab.ScrollX = 0
	tab.Cursor = Position{Line: 5, Col: 18}
	tab.EnsureVisible(20, 10) // contentW = 20-6-1 = 13
	if tab.ScrollX == 0 {
		t.Fatalf("expected ScrollX > 0, got %d", tab.ScrollX)
	}

	// Tiny viewport — contentW clamped to 1.
	tab.EnsureVisible(1, 1)
	if tab.ScrollX < 0 || tab.ScrollY < 0 {
		t.Fatalf("negative scroll: %d %d", tab.ScrollX, tab.ScrollY)
	}
}

// TestTab_Scroll_NeverNegative bounds ScrollY at 0 even with a negative
// delta from cell zero.
func TestTab_Scroll_NeverNegative(t *testing.T) {
	tab := &Tab{Buffer: NewBuffer("a\nb\nc")}
	tab.Scroll(-10)
	if tab.ScrollY != 0 {
		t.Fatalf("ScrollY = %d", tab.ScrollY)
	}
	tab.Scroll(2)
	if tab.ScrollY != 2 {
		t.Fatalf("ScrollY = %d", tab.ScrollY)
	}
}

// TestTab_Render_DrawsLineNumbersAndContent renders into a SimulationScreen
// and reads cells back to confirm the gutter and the first line of content
// are visible. We don't pin colors — only the characters.
func TestTab_Render_DrawsLineNumbersAndContent(t *testing.T) {
	scr := newSimScreen(t, 40, 10)
	defer scr.Fini()

	tab, err := NewTab("")
	if err != nil {
		t.Fatalf("NewTab: %v", err)
	}
	tab.Buffer = NewBuffer("hello\nworld")
	tab.Cursor = Position{Line: 0, Col: 0}
	tab.Anchor = tab.Cursor

	tab.Render(scr, theme.Default(), 0, 0, 40, 10)
	scr.Show()

	cells, w, _ := scr.GetContents()
	if w != 40 {
		t.Fatalf("width = %d", w)
	}
	// Reconstruct the first row.
	var row0 strings.Builder
	for i := 0; i < w; i++ {
		c := cells[i]
		if len(c.Runes) > 0 {
			row0.WriteRune(c.Runes[0])
		} else {
			row0.WriteRune(' ')
		}
	}
	got := row0.String()
	if !strings.Contains(got, "1") {
		t.Errorf("expected line number 1 in row 0, got %q", got)
	}
	if !strings.Contains(got, "hello") {
		t.Errorf("expected 'hello' in row 0, got %q", got)
	}

	// Cursor should be visible somewhere in the rendered area.
	cx, cy, vis := scr.GetCursor()
	if !vis {
		t.Fatal("cursor not visible")
	}
	if cy != 0 {
		t.Errorf("cursor row = %d, want 0", cy)
	}
	if cx < gutterWidth {
		t.Errorf("cursor col %d should be past the gutter", cx)
	}
}

// TestTab_Render_HighlightsSelection draws a selection and confirms Render
// completes cleanly with mid-line cursor visibility — selection bg is theme
// dependent so we just ensure no panic and the cursor lands at col 5.
func TestTab_Render_HighlightsSelection(t *testing.T) {
	scr := newSimScreen(t, 40, 10)
	defer scr.Fini()

	tab, _ := NewTab("")
	tab.Buffer = NewBuffer("hello world")
	tab.Anchor = Position{Line: 0, Col: 0}
	tab.Cursor = Position{Line: 0, Col: 5}

	tab.Render(scr, theme.Default(), 0, 0, 40, 10)
	cx, _, vis := scr.GetCursor()
	if !vis {
		t.Fatal("cursor hidden")
	}
	wantCx := gutterWidth + 1 + 5
	if cx != wantCx {
		t.Errorf("cursor x = %d, want %d", cx, wantCx)
	}
}

// TestTab_Render_HidesCursorWhenOffscreen confirms the cursor is hidden
// when scroll has pushed the cursor's line out of view (cursorMoved=false
// so EnsureVisible doesn't drag it back).
func TestTab_Render_HidesCursorWhenOffscreen(t *testing.T) {
	scr := newSimScreen(t, 40, 5)
	defer scr.Fini()

	tab, _ := NewTab("")
	tab.Buffer = NewBuffer(strings.Repeat("x\n", 50))
	tab.Cursor = Position{Line: 0, Col: 0}
	tab.Anchor = tab.Cursor
	tab.cursorMoved = false
	tab.ScrollY = 20 // far past line 0

	tab.Render(scr, theme.Default(), 0, 0, 40, 5)
	if _, _, vis := scr.GetCursor(); vis {
		t.Fatal("expected cursor to be hidden")
	}
}

// TestTab_HitTest_ContentClick converts a click on a content cell back to
// the matching buffer Position.
func TestTab_HitTest_ContentClick(t *testing.T) {
	tab, _ := NewTab("")
	tab.Buffer = NewBuffer("hello\nworld")

	pos, ok := tab.HitTest(gutterWidth+1+2, 1, 40, 10)
	if !ok {
		t.Fatal("expected ok")
	}
	if pos != (Position{Line: 1, Col: 2}) {
		t.Fatalf("pos wrong: %+v", pos)
	}
}

// TestTab_HitTest_GutterClick treats clicks in the gutter as col 0 of that
// line — convenient for click-to-select-line.
func TestTab_HitTest_GutterClick(t *testing.T) {
	tab, _ := NewTab("")
	tab.Buffer = NewBuffer("hello\nworld")

	pos, ok := tab.HitTest(0, 1, 40, 10)
	if !ok {
		t.Fatal("expected ok")
	}
	if pos != (Position{Line: 1, Col: 0}) {
		t.Fatalf("pos wrong: %+v", pos)
	}
}

// TestTab_HitTest_OutOfBounds returns ok=false when the click is below the
// last line or above the area.
func TestTab_HitTest_OutOfBounds(t *testing.T) {
	tab, _ := NewTab("")
	tab.Buffer = NewBuffer("hi")

	if _, ok := tab.HitTest(10, -1, 40, 10); ok {
		t.Fatal("expected !ok for negative y")
	}
	if _, ok := tab.HitTest(10, 99, 40, 10); ok {
		t.Fatal("expected !ok for huge y")
	}
	// Click on a row that is past the buffer's last line (still within h).
	tab.ScrollY = 0
	if _, ok := tab.HitTest(10, 5, 40, 10); ok {
		t.Fatal("expected !ok past last line")
	}
}

// TestTab_HitTest_ClampsColumnAtLineEnd never returns a Col past the line's
// rune length.
func TestTab_HitTest_ClampsColumnAtLineEnd(t *testing.T) {
	tab, _ := NewTab("")
	tab.Buffer = NewBuffer("ab")

	pos, ok := tab.HitTest(gutterWidth+1+50, 0, 80, 10)
	if !ok {
		t.Fatal("expected ok")
	}
	if pos.Col != 2 {
		t.Fatalf("col = %d, want 2", pos.Col)
	}
}

// TestTab_Render_ExpandsTabsToTabStops confirms that a real \t in the
// buffer paints across multiple cells until the next 4-cell tab stop.
// Without this, the cell directly after a tab character would read as
// ' ' (not 'a'), and indented lines wouldn't line up with each other.
func TestTab_Render_ExpandsTabsToTabStops(t *testing.T) {
	scr := newSimScreen(t, 40, 5)
	defer scr.Fini()

	tab, _ := NewTab("")
	tab.Buffer = NewBuffer("\tabc")
	tab.Cursor = Position{Line: 0, Col: 0}
	tab.Anchor = tab.Cursor

	tab.Render(scr, theme.Default(), 0, 0, 40, 5)
	scr.Show()

	cells, w, _ := scr.GetContents()
	cellRune := func(col int) rune {
		c := cells[col]
		if len(c.Runes) == 0 {
			return ' '
		}
		return c.Runes[0]
	}
	// Content starts at col gutterWidth+1. The tab fills 4 cells, so
	// 'a' lands at content+4, 'b' at +5, 'c' at +6.
	contentCol := gutterWidth + 1
	if w < contentCol+7 {
		t.Fatalf("simulated screen too narrow: w=%d", w)
	}
	if got := cellRune(contentCol + 4); got != 'a' {
		t.Errorf("expected 'a' at content+4, got %q", got)
	}
	if got := cellRune(contentCol + 5); got != 'b' {
		t.Errorf("expected 'b' at content+5, got %q", got)
	}
	if got := cellRune(contentCol + 6); got != 'c' {
		t.Errorf("expected 'c' at content+6, got %q", got)
	}
}

// TestTab_HitTest_InsideTabSnapsToTab proves a click anywhere inside a
// tab's 4-cell visual span returns the tab's rune column. Without this,
// clicks would silently land on phantom positions where there's nothing
// to edit.
func TestTab_HitTest_InsideTabSnapsToTab(t *testing.T) {
	tab, _ := NewTab("")
	tab.Buffer = NewBuffer("\tx")

	contentX := gutterWidth + 1
	for offset := 0; offset < 4; offset++ {
		pos, ok := tab.HitTest(contentX+offset, 0, 40, 10)
		if !ok {
			t.Fatalf("HitTest offset %d returned !ok", offset)
		}
		if pos.Col != 0 {
			t.Errorf("offset %d: col = %d, want 0 (the tab)", offset, pos.Col)
		}
	}
	// Cell 4 is the first cell of 'x' — should land on rune 1.
	pos, _ := tab.HitTest(contentX+4, 0, 40, 10)
	if pos.Col != 1 {
		t.Errorf("cell after tab: col = %d, want 1", pos.Col)
	}
}

// TestTab_NewTab_DetectsIndent ties the editor.Tab type to the
// indent-detection step so opening a tab-indented file makes Tab key
// inserts use a real tab. Pinned at this layer so a future refactor
// can't accidentally drop the call without a test failing.
func TestTab_NewTab_DetectsIndent(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "x.go")
	if err := os.WriteFile(target, []byte("package x\n\nfunc x() {\n\treturn 1\n}\n"), 0644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	tab, err := NewTab(target)
	if err != nil {
		t.Fatalf("NewTab: %v", err)
	}
	if tab.IndentUnit != "\t" {
		t.Fatalf("expected tab IndentUnit, got %q", tab.IndentUnit)
	}
}

// TestTab_clampScroll_BoundsScroll exercises clampScroll via Render: when
// ScrollY is set absurdly high, clampScroll caps it so the file stays
// visible.
func TestTab_clampScroll_BoundsScroll(t *testing.T) {
	scr := newSimScreen(t, 40, 10)
	defer scr.Fini()

	tab, _ := NewTab("")
	tab.Buffer = NewBuffer(strings.Repeat("x\n", 5))
	tab.ScrollY = 9999
	tab.cursorMoved = false

	tab.Render(scr, theme.Default(), 0, 0, 40, 10)
	if tab.ScrollY > tab.Buffer.LineCount() {
		t.Fatalf("ScrollY not clamped: %d", tab.ScrollY)
	}
}

// TestTab_MaxScroll_MatchesClampScroll pins the extracted ceiling against
// the clamp it was pulled out of: the scrollbar places its thumb against
// exactly this number, so a drift between the two would show up as a
// thumb that cannot be dragged to the end of a file the wheel scrolls to
// happily. Also covers the two shapes the formula has to get right — a
// file shorter than the viewport (no travel at all) and the overscroll
// pad that lets the last line come up to the middle of the screen.
func TestTab_MaxScroll_MatchesClampScroll(t *testing.T) {
	cases := []struct {
		name  string
		lines int
		viewH int
		want  int
	}{
		{"file shorter than the viewport", 5, 20, 0},
		{"file exactly the viewport", 20, 20, 10},       // + viewH/2 overscroll
		{"long file", 200, 20, 190},                     // 200 - 20 + 10
		{"tiny viewport keeps a floor of 3", 50, 4, 49}, // 50 - 4 + 3
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			// No trailing newline: NewBuffer keeps the empty line after a
			// final \n, which would make every count here one too many.
			tab := &Tab{Buffer: NewBuffer(strings.TrimSuffix(strings.Repeat("x\n", c.lines), "\n"))}
			if got := tab.Buffer.LineCount(); got != c.lines {
				t.Fatalf("fixture has %d lines, want %d", got, c.lines)
			}
			if got := tab.MaxScroll(c.viewH); got != c.want {
				t.Fatalf("MaxScroll(%d) = %d, want %d", c.viewH, got, c.want)
			}
			// clampScroll must land on the same number from above.
			tab.ScrollY = 99999
			tab.clampScroll(c.viewH)
			if tab.ScrollY != c.want {
				t.Errorf("clampScroll capped at %d, want MaxScroll's %d", tab.ScrollY, c.want)
			}
		})
	}
}

// TestTab_ScrollH_AdjustsAndClamps confirms that ScrollH adds the delta
// and never lets ScrollX go negative — mirroring how Scroll behaves for
// the vertical axis.
func TestTab_ScrollH_AdjustsAndClamps(t *testing.T) {
	tab := &Tab{Buffer: NewBuffer("hello world")}
	tab.ScrollH(5)
	if tab.ScrollX != 5 {
		t.Fatalf("ScrollX = %d, want 5", tab.ScrollX)
	}
	tab.ScrollH(-100)
	if tab.ScrollX != 0 {
		t.Fatalf("ScrollX after negative delta = %d, want 0", tab.ScrollX)
	}
}

// TestTab_Render_OverflowIndicator_Right paints a long line into a narrow
// viewport and confirms a '›' glyph appears at the rightmost content cell,
// signaling that more content exists off-screen. Without this affordance
// the user has no way to discover horizontal scroll is available.
func TestTab_Render_OverflowIndicator_Right(t *testing.T) {
	scr := newSimScreen(t, 20, 5)
	defer scr.Fini()

	tab, _ := NewTab("")
	// 30 chars on one line; viewport content width = 20 - gutterWidth - 1 = 13.
	tab.Buffer = NewBuffer(strings.Repeat("x", 30))
	tab.Cursor = Position{Line: 0, Col: 0}
	tab.Anchor = tab.Cursor

	tab.Render(scr, theme.Default(), 0, 0, 20, 5)
	scr.Show()

	cells, w, _ := scr.GetContents()
	// Last cell of row 0 should be the right-overflow glyph.
	last := cells[w-1]
	if len(last.Runes) == 0 || last.Runes[0] != '›' {
		t.Fatalf("expected '›' at row 0 col %d, got %q", w-1, string(last.Runes))
	}
}

// TestTab_Render_OverflowIndicator_Left scrolls a long line right and
// confirms a '‹' glyph appears at the leftmost content cell to signal
// off-screen content to the left.
func TestTab_Render_OverflowIndicator_Left(t *testing.T) {
	scr := newSimScreen(t, 20, 5)
	defer scr.Fini()

	tab, _ := NewTab("")
	tab.Buffer = NewBuffer(strings.Repeat("x", 30))
	tab.ScrollX = 10
	tab.Cursor = Position{Line: 0, Col: 10}
	tab.Anchor = tab.Cursor

	tab.Render(scr, theme.Default(), 0, 0, 20, 5)
	scr.Show()

	cells, w, _ := scr.GetContents()
	// First content cell is at column gutterWidth + 1.
	left := cells[gutterWidth+1]
	if len(left.Runes) == 0 || left.Runes[0] != '‹' {
		t.Fatalf("expected '‹' at row 0 col %d, got %q", gutterWidth+1, string(left.Runes))
	}
	_ = w
}

// TestTab_Render_NoOverflowIndicator_WhenLineFits is the negative control:
// a line that fits within contentW should NOT get a '›' glyph painted over
// its trailing real content.
func TestTab_Render_NoOverflowIndicator_WhenLineFits(t *testing.T) {
	scr := newSimScreen(t, 40, 5)
	defer scr.Fini()

	tab, _ := NewTab("")
	tab.Buffer = NewBuffer("short")
	tab.Cursor = Position{Line: 0, Col: 0}
	tab.Anchor = tab.Cursor

	tab.Render(scr, theme.Default(), 0, 0, 40, 5)
	scr.Show()

	cells, w, _ := scr.GetContents()
	for i := 0; i < w; i++ {
		if len(cells[i].Runes) > 0 && (cells[i].Runes[0] == '›' || cells[i].Runes[0] == '‹') {
			t.Fatalf("unexpected overflow glyph at col %d", i)
		}
	}
}

// TestTab_EditRev_BumpsOnContentMutations pins the contract the LSP
// didChange debounce relies on: every path that changes the document
// text moves EditRev, and pure cursor motion does not.
func TestTab_EditRev_BumpsOnContentMutations(t *testing.T) {
	tab, _ := NewTab("")
	tab.Buffer = NewBuffer("hello world\nsecond line")

	rev := tab.EditRev
	step := func(name string, mutate func()) {
		t.Helper()
		mutate()
		if tab.EditRev <= rev {
			t.Errorf("%s did not bump EditRev (still %d)", name, tab.EditRev)
		}
		rev = tab.EditRev
	}

	step("InsertRune", func() { tab.InsertRune('x') })
	step("InsertString", func() { tab.InsertString("ab") })
	step("Backspace", func() { tab.Backspace() })
	step("Delete", func() { tab.MoveCursorTo(Position{}, false); tab.Delete() })
	step("Undo", func() {
		if !tab.Undo() {
			t.Fatal("expected undo history")
		}
	})
	step("DeleteSelection", func() {
		tab.Anchor = Position{Line: 0, Col: 0}
		tab.Cursor = Position{Line: 0, Col: 2}
		tab.DeleteSelection()
	})

	// Negative control: motion and selection changes leave EditRev alone.
	before := tab.EditRev
	tab.MoveCursor(1, 0, false)
	tab.MoveLineEnd(true)
	tab.SelectAll()
	if tab.EditRev != before {
		t.Errorf("cursor motion bumped EditRev %d → %d", before, tab.EditRev)
	}
}

// TestTab_CursorScreenCell pins the caret-anchoring math the hover
// popup uses: on-screen cursors resolve to the same cell Render's
// hardware cursor lands on; scrolled-away cursors report ok=false.
func TestTab_CursorScreenCell(t *testing.T) {
	tab, _ := NewTab("")
	tab.Buffer = NewBuffer("zero\none\ntwo\nthree\nfour\nfive\nsix")

	tab.Cursor = Position{Line: 2, Col: 3}
	dx, dy, ok := tab.CursorScreenCell(40, 5)
	if !ok {
		t.Fatal("cursor inside the viewport should resolve")
	}
	// Content starts after the 6-cell gutter + 1-cell mark column.
	if dx != gutterWidth+1+3 || dy != 2 {
		t.Errorf("cell = (%d,%d), want (%d,2)", dx, dy, gutterWidth+1+3)
	}

	// Scroll the cursor's line above the viewport → not visible.
	tab.ScrollY = 5
	if _, _, ok := tab.CursorScreenCell(40, 5); ok {
		t.Error("cursor above the viewport should report ok=false")
	}

	// Horizontal scroll pushing the caret off the left edge. The line
	// must extend past ScrollX — on a shorter line the visual-column
	// clamp parks the caret at the content edge instead (same rule
	// Render's hardware cursor follows).
	tab.Buffer = NewBuffer("this line is comfortably longer than the scroll offset")
	tab.Cursor = Position{Line: 0, Col: 3}
	tab.ScrollY = 0
	tab.ScrollX = 10
	if _, _, ok := tab.CursorScreenCell(40, 5); ok {
		t.Error("cursor left of the h-scroll window should report ok=false")
	}
}

// TestRestoreView_PutsTheViewBackWithoutRescrolling pins the abort-a-peek
// primitive: cursor, anchor, and both scroll offsets come back verbatim,
// and — the part that matters — the next Render must NOT drag the
// viewport to the cursor. Anything that restored via MoveCursorTo would
// arm EnsureVisible and lose a scrolled-away view.
func TestRestoreView_PutsTheViewBackWithoutRescrolling(t *testing.T) {
	lines := make([]string, 0, 200)
	for i := 0; i < 200; i++ {
		lines = append(lines, "line")
	}
	tab := &Tab{Buffer: &Buffer{Lines: lines}, IndentUnit: "\t"}

	// Peek somewhere far away, then put it back.
	tab.MoveCursorTo(Position{Line: 150, Col: 2}, false)
	tab.RestoreView(Position{Line: 3, Col: 1}, Position{Line: 3, Col: 1}, 60, 4)

	if tab.Cursor != (Position{Line: 3, Col: 1}) {
		t.Errorf("Cursor = %v, want line 3 col 1", tab.Cursor)
	}
	if tab.Anchor != (Position{Line: 3, Col: 1}) {
		t.Errorf("Anchor = %v, want line 3 col 1", tab.Anchor)
	}
	if tab.ScrollY != 60 || tab.ScrollX != 4 {
		t.Fatalf("scroll = (%d,%d), want (60,4)", tab.ScrollY, tab.ScrollX)
	}

	scr := tcell.NewSimulationScreen("UTF-8")
	if err := scr.Init(); err != nil {
		t.Fatalf("init: %v", err)
	}
	defer scr.Fini()
	tab.Render(scr, theme.Default(), 0, 0, 40, 20)
	if tab.ScrollY != 60 {
		t.Errorf("ScrollY = %d after render, want 60 — the restore armed EnsureVisible", tab.ScrollY)
	}
}

// TestRestoreView_ClampsToAShrunkenBuffer covers the external-reload
// case: a peek can outlive the lines it was measured against, and the
// restore must land somewhere legal rather than past the end.
func TestRestoreView_ClampsToAShrunkenBuffer(t *testing.T) {
	tab := &Tab{Buffer: &Buffer{Lines: []string{"one", "two"}}}
	tab.RestoreView(Position{Line: 90, Col: 40}, Position{Line: 90, Col: 40}, 0, 0)
	if tab.Cursor.Line != 1 || tab.Cursor.Col != 3 {
		t.Errorf("Cursor = %v, want the clamped end of the buffer", tab.Cursor)
	}
}

// TestCenterOnCursor_PutsTheLineInTheMiddle pins the peek scroll: the
// cursor's line lands mid-view rather than at whichever edge a minimal
// scroll would have parked it, and the next Render must not undo that.
func TestCenterOnCursor_PutsTheLineInTheMiddle(t *testing.T) {
	lines := make([]string, 0, 300)
	for i := 0; i < 300; i++ {
		lines = append(lines, "line")
	}
	tab := &Tab{Buffer: &Buffer{Lines: lines}, IndentUnit: "\t"}

	tab.MoveCursorTo(Position{Line: 200, Col: 0}, false)
	tab.CenterOnCursor(80, 20)

	if want := 200 - 10; tab.ScrollY != want {
		t.Fatalf("ScrollY = %d, want %d (line 200 centered in 20 rows)", tab.ScrollY, want)
	}

	scr := tcell.NewSimulationScreen("UTF-8")
	if err := scr.Init(); err != nil {
		t.Fatalf("init: %v", err)
	}
	defer scr.Fini()
	tab.Render(scr, theme.Default(), 0, 0, 80, 20)
	if tab.ScrollY != 190 {
		t.Errorf("ScrollY = %d after render, want 190 — centering armed EnsureVisible", tab.ScrollY)
	}
}

// TestCenterOnCursor_TopOfFileAndLongLines covers the two cases centering
// can't take literally: a line too near the top to sit in the middle
// clamps at 0, and a cursor off the right edge still gets scrolled into
// view horizontally (centering is a vertical idea only).
func TestCenterOnCursor_TopOfFileAndLongLines(t *testing.T) {
	long := strings.Repeat("x", 400)
	tab := &Tab{Buffer: &Buffer{Lines: []string{"a", long, "c"}}, IndentUnit: "\t"}

	tab.MoveCursorTo(Position{Line: 1, Col: 350}, false)
	tab.CenterOnCursor(80, 20)

	if tab.ScrollY != 0 {
		t.Errorf("ScrollY = %d near the top of the file, want the clamped 0", tab.ScrollY)
	}
	if tab.ScrollX == 0 {
		t.Error("a cursor at column 350 should still have scrolled horizontally")
	}
	if tab.Cursor.Col < tab.ScrollX {
		t.Errorf("cursor col %d left of ScrollX %d — not visible", tab.Cursor.Col, tab.ScrollX)
	}
}

// TestCursorLineVisible_BandEdges pins the visibility test callers use to
// decide whether a scroll is needed at all: inclusive at the top row,
// exclusive one past the bottom, and false for a zero-height view.
func TestCursorLineVisible_BandEdges(t *testing.T) {
	lines := make([]string, 0, 100)
	for i := 0; i < 100; i++ {
		lines = append(lines, "line")
	}
	tab := &Tab{Buffer: &Buffer{Lines: lines}}
	tab.ScrollY = 30

	cases := []struct {
		line int
		want bool
	}{
		{29, false}, // one above the window
		{30, true},  // first visible row
		{49, true},  // last visible row of a 20-row view
		{50, false}, // one past it
	}
	for _, c := range cases {
		tab.Cursor = Position{Line: c.line}
		if got := tab.CursorLineVisible(20); got != c.want {
			t.Errorf("CursorLineVisible(20) at line %d = %v, want %v", c.line, got, c.want)
		}
	}
	tab.Cursor = Position{Line: 30}
	if tab.CursorLineVisible(0) {
		t.Error("a zero-height view can't be showing anything")
	}
}
