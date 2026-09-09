// =============================================================================
// File: internal/editor/markdown_test.go
// Author: Rohan Allison
// Created: 2026-09-09
// Copyright: 2026 Cloudmanic, LLC. All rights reserved.
// =============================================================================

package editor

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gdamore/tcell/v2"

	"github.com/rohanthewiz/ced/internal/theme"
)

// mdTab builds a saved .md tab holding src.
func mdTab(t *testing.T, src string) *Tab {
	t.Helper()
	path := filepath.Join(t.TempDir(), "doc.md")
	if err := os.WriteFile(path, []byte(src), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	tab, err := NewTab(path)
	if err != nil {
		t.Fatalf("NewTab: %v", err)
	}
	return tab
}

// TestIsMarkdownPath pins the extension set, including the negative that
// matters — a file merely CONTAINING markdown is not offered the view,
// because guessing wrong shows a preview where source was expected.
func TestIsMarkdownPath(t *testing.T) {
	for _, p := range []string{"a.md", "A.MD", "notes.markdown", "x.mkd"} {
		if !IsMarkdownPath(p) {
			t.Errorf("IsMarkdownPath(%q) = false, want true", p)
		}
	}
	for _, p := range []string{"a.go", "README", "a.md.go", "", "a.txt"} {
		if IsMarkdownPath(p) {
			t.Errorf("IsMarkdownPath(%q) = true, want false", p)
		}
	}
}

// TestMarkdownCapable_RejectsImageAndUntitled pins the two tabs that
// have nothing to preview: an image has no text, an untitled buffer has
// no name to judge by.
func TestMarkdownCapable_RejectsImageAndUntitled(t *testing.T) {
	if (&Tab{Mode: imageMode, Path: "a.md"}).MarkdownCapable() {
		t.Error("an image tab claimed to be previewable")
	}
	if (&Tab{Buffer: NewBuffer("")}).MarkdownCapable() {
		t.Error("an untitled tab claimed to be previewable")
	}
	if !mdTab(t, "# hi").MarkdownCapable() {
		t.Error("a real .md tab was refused")
	}
}

// TestSetMarkdownView_LeavesTheBufferAlone is the feature's central
// promise: the preview is a way of LOOKING at the file, so toggling it
// must not touch the text, the dirty flag, the undo history or the
// caret. A view that quietly edited would be the worst possible bug
// here, and it is one a rendering change could introduce silently.
func TestSetMarkdownView_LeavesTheBufferAlone(t *testing.T) {
	tab := mdTab(t, "# Title\n\nbody\n")
	tab.InsertRune('x')
	before := tab.Buffer.String()
	rev, dirty, cur := tab.EditRev, tab.Dirty, tab.Cursor
	depth := tab.UndoDepth()

	tab.SetMarkdownView(true)
	tab.SetMarkdownView(false)

	if tab.Buffer.String() != before {
		t.Errorf("the buffer changed:\n got %q\nwant %q", tab.Buffer.String(), before)
	}
	if tab.EditRev != rev || tab.Dirty != dirty || tab.Cursor != cur {
		t.Errorf("tab state moved: rev %d→%d dirty %v→%v cursor %v→%v",
			rev, tab.EditRev, dirty, tab.Dirty, cur, tab.Cursor)
	}
	if tab.UndoDepth() != depth {
		t.Errorf("undo depth moved: %d → %d", depth, tab.UndoDepth())
	}
}

// TestSetMarkdownView_PreservesTheEditViewport pins why MDScroll is a
// separate counter: scrolling the preview must not rewrite the position
// the source view is restored to.
func TestSetMarkdownView_PreservesTheEditViewport(t *testing.T) {
	tab := mdTab(t, strings.Repeat("a line of prose\n", 200))
	tab.ScrollY = 57

	tab.SetMarkdownView(true)
	tab.MDScroll = 3
	tab.MDScrollBy(40, 500, 20)
	tab.SetMarkdownView(false)

	if tab.ScrollY != 57 {
		t.Errorf("ScrollY = %d, want 57 — the preview's scroll leaked into the source view", tab.ScrollY)
	}
}

// TestMarkdownRows_CachedUntilTheContentOrWidthMoves pins the memo. A
// re-render per frame would put an O(document) Chroma pass in the draw,
// which is the exact cost syntax.go's settle policy exists to avoid.
func TestMarkdownRows_CachedUntilTheContentOrWidthMoves(t *testing.T) {
	tab := mdTab(t, "# Title\n\nbody text\n")
	th := theme.Default()

	first := tab.MarkdownRows(th, 40)
	if &first[0] != &tab.MarkdownRows(th, 40)[0] {
		t.Error("a second call at the same width re-rendered")
	}
	if &first[0] == &tab.MarkdownRows(th, 30)[0] {
		t.Error("a width change did not re-flow")
	}
	tab.InsertRune('z')
	if &first[0] == &tab.MarkdownRows(th, 40)[0] {
		t.Error("an edit did not invalidate the cache")
	}
	tab.InvalidateMarkdown()
	if tab.mdValid {
		t.Error("InvalidateMarkdown left the cache marked valid")
	}
}

// TestMarkdownRows_RefusesAWindowTooNarrow pins the degradation: prose
// wrapped into a six-column column is unreadable, so the view declines
// and Render falls back to the source rather than painting a mess.
func TestMarkdownRows_RefusesAWindowTooNarrow(t *testing.T) {
	tab := mdTab(t, "# Title\n\nbody\n")
	if rows := tab.MarkdownRows(theme.Default(), 4); rows != nil {
		t.Errorf("got %d rows at width 4, want none", len(rows))
	}
}

// TestMDMaxScroll_KeepsTheOverscrollPad pins that the preview scrolls
// the way the source view does — the last row can be pulled toward the
// middle rather than stopping dead at the bottom.
func TestMDMaxScroll_KeepsTheOverscrollPad(t *testing.T) {
	tab := &Tab{}
	if got, want := tab.MDMaxScroll(100, 20), 90; got != want {
		t.Errorf("MDMaxScroll = %d, want %d", got, want)
	}
	// A document shorter than the viewport cannot scroll at all.
	if got := tab.MDMaxScroll(5, 20); got != 0 {
		t.Errorf("MDMaxScroll on a short document = %d, want 0", got)
	}
}

// TestMDScrollBy_Clamps pins both ends of the range.
func TestMDScrollBy_Clamps(t *testing.T) {
	tab := &Tab{}
	tab.MDScrollBy(-50, 100, 20)
	if tab.MDScroll != 0 {
		t.Errorf("scrolled above the top: %d", tab.MDScroll)
	}
	tab.MDScrollBy(9999, 100, 20)
	if want := tab.MDMaxScroll(100, 20); tab.MDScroll != want {
		t.Errorf("MDScroll = %d, want the clamp %d", tab.MDScroll, want)
	}
}

// TestRender_MarkdownViewDrawsTheDocumentNotTheSource is the end-to-end
// pin: with the view on, Tab.Render must paint prose — no hashes, no
// line-number gutter.
func TestRender_MarkdownViewDrawsTheDocumentNotTheSource(t *testing.T) {
	scr := tcell.NewSimulationScreen("UTF-8")
	if err := scr.Init(); err != nil {
		t.Fatalf("init: %v", err)
	}
	defer scr.Fini()
	scr.SetSize(60, 20)

	tab := mdTab(t, "# Heading\n\nsome body text\n")
	tab.SetMarkdownView(true)
	tab.Render(scr, theme.Default(), 0, 0, 60, 20)

	screen := simText(scr, 60, 20)
	if strings.Contains(screen, "#") {
		t.Errorf("the source's hash was painted:\n%s", screen)
	}
	if !strings.Contains(screen, "Heading") || !strings.Contains(screen, "some body text") {
		t.Errorf("the document did not render:\n%s", screen)
	}
	if strings.Contains(screen, "  1 ") {
		t.Errorf("the line-number gutter was drawn in the preview:\n%s", screen)
	}
}

// TestRender_MarkdownViewOpensNearTheCursor pins the toggle's courtesy:
// the preview shows the passage that was being edited, not the top of a
// long file.
func TestRender_MarkdownViewOpensNearTheCursor(t *testing.T) {
	scr := tcell.NewSimulationScreen("UTF-8")
	if err := scr.Init(); err != nil {
		t.Fatalf("init: %v", err)
	}
	defer scr.Fini()
	scr.SetSize(60, 20)

	// Separate paragraphs, not one wall of text: consecutive non-blank
	// lines are ONE markdown paragraph, so a wall would render as a
	// single wrapped block whose rows all trace back to its first line.
	var b strings.Builder
	for i := 0; i < 300; i++ {
		b.WriteString("filler line\n\n")
	}
	b.WriteString("THE MARKER LINE\n")
	tab := mdTab(t, b.String())
	tab.MoveCursorTo(Position{Line: 600}, false)
	tab.SetMarkdownView(true)
	tab.Render(scr, theme.Default(), 0, 0, 60, 20)

	if !strings.Contains(simText(scr, 60, 20), "THE MARKER LINE") {
		t.Error("the preview opened at the top instead of near the cursor")
	}
}

// simText reads the simulation screen back as text, one line per row.
func simText(scr tcell.Screen, w, h int) string {
	var b strings.Builder
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			r, _, _, _ := scr.GetContent(x, y)
			b.WriteRune(r)
		}
		b.WriteByte('\n')
	}
	return b.String()
}
