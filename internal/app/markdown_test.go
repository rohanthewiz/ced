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
