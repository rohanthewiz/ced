// =============================================================================
// File: internal/app/markdown_test.go
// Author: Rohan Allison
// Created: 2026-09-09
// Copyright: 2026 Cloudmanic, LLC. All rights reserved.
// =============================================================================

package app

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gdamore/tcell/v2"

	"github.com/rohanthewiz/ced/internal/filetree"
)

// openMD writes a markdown file into the app's root and opens it.
func openMD(t *testing.T, a *App, name, src string) string {
	t.Helper()
	path := filepath.Join(a.rootDir, name)
	if err := os.WriteFile(path, []byte(src), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	a.openFile(path)
	return path
}

// TestToggleMarkdownView_OnlyOnMarkdownFiles pins the refusal and that
// it names the file rather than the rule — "not a markdown file" is
// actionable, "unavailable" is not.
func TestToggleMarkdownView_OnlyOnMarkdownFiles(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	openMD(t, a, "code.go", "package main\n")

	a.toggleMarkdownView()
	if a.markdownTab() != nil {
		t.Error("a .go file was put into the markdown preview")
	}
	if !strings.Contains(a.statusMsg, "code.go") {
		t.Errorf("the refusal did not name the file: %q", a.statusMsg)
	}
	if a.hasMarkdownPreview() {
		t.Error("hasMarkdownPreview said yes on a .go file")
	}
}

// TestToggleMarkdownView_RoundTrips pins the toggle and its label, which
// names the state it will switch TO — the sidebar/terminal convention.
func TestToggleMarkdownView_RoundTrips(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	openMD(t, a, "doc.md", "# Title\n\nbody\n")

	if got := a.markdownToggleLabel(); got != "Preview markdown" {
		t.Errorf("label off = %q", got)
	}
	a.toggleMarkdownView()
	if a.markdownTab() == nil {
		t.Fatal("the preview did not turn on")
	}
	if got := a.markdownToggleLabel(); got != "Show markdown source" {
		t.Errorf("label on = %q", got)
	}
	a.toggleMarkdownView()
	if a.markdownTab() != nil {
		t.Error("the preview did not turn off")
	}
}

// TestMarkdownView_SwallowsEditingKeys is the safety pin: there is no
// visible caret in a preview, so a keystroke that reached the buffer
// would be an invisible edit to a file the user believes they are only
// reading.
func TestMarkdownView_SwallowsEditingKeys(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	openMD(t, a, "doc.md", "# Title\n\nbody\n")
	a.toggleMarkdownView()

	tab := a.activeTabPtr()
	before, rev := tab.Buffer.String(), tab.EditRev
	for _, ev := range []*tcell.EventKey{
		tcell.NewEventKey(tcell.KeyRune, 'x', tcell.ModNone),
		tcell.NewEventKey(tcell.KeyBackspace2, 0, tcell.ModNone),
		tcell.NewEventKey(tcell.KeyDelete, 0, tcell.ModNone),
		tcell.NewEventKey(tcell.KeyTab, 0, tcell.ModNone),
		tcell.NewEventKey(tcell.KeyCtrlD, 0, tcell.ModCtrl),
	} {
		// Enter is deliberately absent: it is the keyboard twin of the
		// double-click and legitimately LEAVES the preview, which is
		// pinned by its own test below.
		a.handleKey(ev)
	}
	if tab.Buffer.String() != before || tab.EditRev != rev {
		t.Errorf("a key reached the buffer: %q (rev %d → %d)", tab.Buffer.String(), rev, tab.EditRev)
	}
}

// TestMarkdownView_NavigationKeysScroll pins the carve-out: dropping
// EVERY key would leave the preview unscrollable from a keyboard, which
// on a terminal that eats mouse events means unreadable.
func TestMarkdownView_NavigationKeysScroll(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	openMD(t, a, "doc.md", strings.Repeat("a paragraph of prose\n\n", 200))
	a.toggleMarkdownView()
	tab := a.activeTabPtr()

	a.handleKey(tcell.NewEventKey(tcell.KeyPgDn, 0, tcell.ModNone))
	down := tab.MDScroll
	if down == 0 {
		t.Fatal("PgDn did not scroll the preview")
	}
	a.handleKey(tcell.NewEventKey(tcell.KeyHome, 0, tcell.ModNone))
	if tab.MDScroll != 0 {
		t.Errorf("Home left MDScroll at %d", tab.MDScroll)
	}
	a.handleKey(tcell.NewEventKey(tcell.KeyDown, 0, tcell.ModNone))
	if tab.MDScroll == 0 {
		t.Error("Down did not scroll the preview")
	}
	// Scrolling the preview must never move the edit view's viewport.
	if tab.ScrollY != 0 {
		t.Errorf("ScrollY moved to %d while previewing", tab.ScrollY)
	}
}

// TestMarkdownView_EnterLeavesOnTheVisibleLine pins the preview as a
// NAVIGATION surface: you read until something is wrong, and the gesture
// that acts on it puts the caret there.
func TestMarkdownView_EnterLeavesOnTheVisibleLine(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	openMD(t, a, "doc.md", strings.Repeat("prose line\n\n", 100)+"# Marker\n")
	a.toggleMarkdownView()
	tab := a.activeTabPtr()

	tab.MDScroll = 40
	a.handleKey(tcell.NewEventKey(tcell.KeyEnter, 0, tcell.ModNone))

	if a.markdownTab() != nil {
		t.Fatal("Enter did not leave the preview")
	}
	if tab.Cursor.Line == 0 {
		t.Error("Enter left the preview without landing the caret on what was on screen")
	}
}

// TestMarkdownView_LeaderTogglesFromInsideThePreview pins the reason
// swallowing keys is safe: the leader table is dispatched well above the
// read-only branch, so Esc-v still gets the source back and the whole ≡
// menu stays reachable from a surface that otherwise eats keystrokes.
func TestMarkdownView_LeaderTogglesFromInsideThePreview(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	openMD(t, a, "doc.md", "# Title\n\nbody\n")

	a.handleKey(tcell.NewEventKey(tcell.KeyEsc, 0, tcell.ModNone))
	a.handleKey(tcell.NewEventKey(tcell.KeyRune, 'v', tcell.ModNone))
	if a.markdownTab() == nil {
		t.Fatal("esc v did not open the preview")
	}
	a.handleKey(tcell.NewEventKey(tcell.KeyEsc, 0, tcell.ModNone))
	a.handleKey(tcell.NewEventKey(tcell.KeyRune, 'v', tcell.ModNone))
	if a.markdownTab() != nil {
		t.Error("esc v from inside the preview did not return to the source")
	}
}

// TestMarkdownView_WheelScrollsRowsNotLines pins the routing: a display
// row is not a buffer line, so the wheel has to reach the preview's own
// counter and leave ScrollY alone.
func TestMarkdownView_WheelScrollsRowsNotLines(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	openMD(t, a, "doc.md", strings.Repeat("a paragraph\n\n", 200))
	a.toggleMarkdownView()
	tab := a.activeTabPtr()

	ex, ey, _, _ := a.editorRect()
	a.scrollAt(ex+2, ey+2, 5)
	if tab.MDScroll == 0 {
		t.Error("the wheel did not scroll the preview")
	}
	if tab.ScrollY != 0 {
		t.Errorf("the wheel moved the source viewport to %d", tab.ScrollY)
	}
}

// TestMarkdownView_StatusBarSaysPreview pins that the bar admits what
// the pane is showing — the Ln/Col beside it is a caret in a file the
// user cannot currently see.
func TestMarkdownView_StatusBarSaysPreview(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	openMD(t, a, "doc.md", "# Title\n")
	a.statusUntil = time.Time{} // drop the open flash so the facts show
	a.toggleMarkdownView()
	a.statusUntil = time.Time{}

	var b strings.Builder
	for _, seg := range a.statusLeftSegments() {
		b.WriteString(seg.text)
	}
	if !strings.Contains(b.String(), "preview") {
		t.Errorf("status bar did not mention the preview: %q", b.String())
	}
}

// TestMarkdownView_SurvivesATabSwitch pins that the flag is per TAB —
// previewing a document must not put the Go file beside it into a mode
// it has no rendering for.
func TestMarkdownView_SurvivesATabSwitch(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	doc := openMD(t, a, "doc.md", "# Title\n")
	code := openMD(t, a, "main.go", "package main\n")

	a.openFile(doc)
	a.toggleMarkdownView()
	a.openFile(code)
	if a.markdownTab() != nil {
		t.Error("the preview followed the tab switch onto a .go file")
	}
	a.openFile(doc)
	if a.markdownTab() == nil {
		t.Error("the document lost its preview across a tab switch")
	}
}

// treeNodeFor finds the tree row for a file directly under the root,
// which is the noun openTreeContext acts on.
func treeNodeFor(t *testing.T, a *App, name string) *filetree.Node {
	t.Helper()
	for _, c := range a.tree.Root.Children {
		if filepath.Base(c.Path) == name {
			return c
		}
	}
	t.Fatalf("no tree row for %s", name)
	return nil
}

// TestOpenTreeContext_PreviewOnlyOnMarkdown pins that the row is present
// on a .md file and absent everywhere else — the tree menu omits rows by
// node KIND rather than dimming them, so a Preview on a .go file would
// be a dead end the user still has to read past.
func TestOpenTreeContext_PreviewOnlyOnMarkdown(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	for _, name := range []string{"doc.md", "code.go"} {
		if err := os.WriteFile(filepath.Join(a.rootDir, name), []byte("# hi\n"), 0o644); err != nil {
			t.Fatalf("write: %v", err)
		}
	}
	a.refreshTreeNow()

	has := func(n *filetree.Node) bool {
		a.openTreeContext(n, 5, 5)
		m := contextOf(a)
		if m == nil {
			t.Fatal("context menu should open")
		}
		defer a.closeModal()
		for _, it := range m.items {
			if it.label == "Preview" {
				return true
			}
		}
		return false
	}
	if !has(treeNodeFor(t, a, "doc.md")) {
		t.Error("markdown file has no Preview row")
	}
	if has(treeNodeFor(t, a, "code.go")) {
		t.Error("Preview offered on a file the viewer would refuse")
	}
	if has(a.tree.Root) {
		t.Error("Preview offered on a directory")
	}
}

// TestCtxPreviewMarkdown_OpensAsADocument pins the verb: the clicked
// file becomes the active tab AND is drawn as a preview, so a user who
// never learned Esc-v can still read a document as one.
func TestCtxPreviewMarkdown_OpensAsADocument(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	path := filepath.Join(a.rootDir, "doc.md")
	if err := os.WriteFile(path, []byte("# Title\n\nbody\n"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	a.refreshTreeNow()

	ctxPreviewMarkdown(a, treeNodeFor(t, a, "doc.md"))

	tab := a.activeTabPtr()
	if tab == nil || tab.Path != path {
		t.Fatalf("the clicked file did not become active: %+v", tab)
	}
	if a.markdownTab() == nil {
		t.Error("the file opened as source, not as a preview")
	}
}

// TestCtxPreviewMarkdown_ForcesPreviewOn pins that the row is not a
// toggle: a tab already left in preview stays in preview rather than
// flipping back to source, or the row's outcome would depend on state
// the click cannot see.
func TestCtxPreviewMarkdown_ForcesPreviewOn(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	path := filepath.Join(a.rootDir, "doc.md")
	if err := os.WriteFile(path, []byte("# Title\n"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	a.refreshTreeNow()
	n := treeNodeFor(t, a, "doc.md")

	ctxPreviewMarkdown(a, n)
	ctxPreviewMarkdown(a, n)
	if a.markdownTab() == nil {
		t.Error("a second Preview turned the preview off")
	}
}

// ctxRowLabels opens the tree menu for n and returns its row labels.
func ctxRowLabels(t *testing.T, a *App, n *filetree.Node) []string {
	t.Helper()
	a.openTreeContext(n, 5, 5)
	m := contextOf(a)
	if m == nil {
		t.Fatal("context menu should open")
	}
	defer a.closeModal()
	var out []string
	for _, it := range m.items {
		out = append(out, it.label)
	}
	return out
}

// TestOpenTreeContext_PreviewRowNamesItsOutcome pins the pair: exactly
// one of Preview / Stop Preview is ever on the popup, chosen by the
// CLICKED file's tab rather than the active one — right-clicking a
// document while standing in another file is the normal case.
func TestOpenTreeContext_PreviewRowNamesItsOutcome(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	path := filepath.Join(a.rootDir, "doc.md")
	if err := os.WriteFile(path, []byte("# Title\n"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	if err := os.WriteFile(filepath.Join(a.rootDir, "main.go"), []byte("package main\n"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	a.refreshTreeNow()
	n := treeNodeFor(t, a, "doc.md")

	joined := strings.Join(ctxRowLabels(t, a, n), " | ")
	if !strings.Contains(joined, "Preview") || strings.Contains(joined, "Stop Preview") {
		t.Errorf("a closed document should offer Preview only: %q", joined)
	}

	ctxPreviewMarkdown(a, n)
	a.openFile(filepath.Join(a.rootDir, "main.go")) // stand somewhere else

	joined = strings.Join(ctxRowLabels(t, a, n), " | ")
	if !strings.Contains(joined, "Stop Preview") || strings.Contains(joined, "| Preview") {
		t.Errorf("a previewed document should offer Stop Preview only: %q", joined)
	}
}

// TestCtxStopMarkdownPreview_ReturnsToSourceAndFocuses pins the verb: the
// preview drops and the file comes to the front, which is the whole
// point of reaching for it from a tree row rather than from Esc-v.
func TestCtxStopMarkdownPreview_ReturnsToSourceAndFocuses(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	path := filepath.Join(a.rootDir, "doc.md")
	if err := os.WriteFile(path, []byte("# Title\n"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	if err := os.WriteFile(filepath.Join(a.rootDir, "main.go"), []byte("package main\n"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	a.refreshTreeNow()
	n := treeNodeFor(t, a, "doc.md")

	ctxPreviewMarkdown(a, n)
	a.openFile(filepath.Join(a.rootDir, "main.go"))
	ctxStopMarkdownPreview(a, n)

	tab := a.activeTabPtr()
	if tab == nil || tab.Path != path {
		t.Fatalf("Stop Preview did not focus the document: %+v", tab)
	}
	if a.markdownTab() != nil {
		t.Error("the tab is still being drawn as a preview")
	}
}
