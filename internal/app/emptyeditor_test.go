// =============================================================================
// File: internal/app/emptyeditor_test.go
// Author: Rohan Allison <rohanthewiz@gmail.com>
// Created: 2026-09-28
// Copyright: 2026 Rohan Allison. All rights reserved.
// =============================================================================

package app

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/gdamore/tcell/v2"
)

// linkNamed finds the placeholder link with the given label, failing the
// test when it is not laid out.
func linkNamed(t *testing.T, a *App, label string) emptyLink {
	t.Helper()
	for _, l := range a.emptyEditorLinks() {
		if l.label == label {
			return l
		}
	}
	t.Fatalf("no %q link laid out; got %v", label, a.emptyEditorLinks())
	return emptyLink{}
}

// clickAt sends a left press and release at (x, y) through the real
// mouse router.
func clickAt(a *App, x, y int) {
	a.handleMouse(tcell.NewEventMouse(x, y, tcell.Button1, 0))
	a.handleMouse(tcell.NewEventMouse(x, y, tcell.ButtonNone, 0))
}

// TestEmptyEditor_DrawsBothLinks checks the placeholder paints the two
// links, on one row, below the existing hint, with the link styling.
func TestEmptyEditor_DrawsBothLinks(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	a.draw()
	a.screen.Show()

	text := screenText(a)
	for _, want := range []string{"No file open", "Recent files", "Recent locations"} {
		if !strings.Contains(text, want) {
			t.Fatalf("placeholder missing %q:\n%s", want, text)
		}
	}
	files, locs := linkNamed(t, a, "Recent files"), linkNamed(t, a, "Recent locations")
	if files.rect.y != locs.rect.y {
		t.Fatalf("a wide band should put both links on one row: %v vs %v", files.rect, locs.rect)
	}
	// The drawn text sits exactly in the rect the hit-test answers for.
	scr := a.screen.(tcell.SimulationScreen)
	cells, w, _ := scr.GetContents()
	var got []rune
	for x := files.rect.x; x < files.rect.x+files.rect.w; x++ {
		c := cells[files.rect.y*w+x]
		got = append(got, c.Runes[0])
		if _, _, attr := c.Style.Decompose(); attr&tcell.AttrUnderline == 0 {
			t.Fatalf("link cell at x=%d is not underlined", x)
		}
	}
	if string(got) != "Recent files" {
		t.Fatalf("rect covers %q, want %q", string(got), "Recent files")
	}
}

// TestEmptyEditor_NoLinksWhileATabIsOpen checks an open file hides the
// links from BOTH draw and hit-test, so a click in the code never runs one.
func TestEmptyEditor_NoLinksWhileATabIsOpen(t *testing.T) {
	a, _, _ := recentFilesApp(t, 1)
	if links := a.emptyEditorLinks(); links != nil {
		t.Fatalf("links laid out under an open tab: %v", links)
	}
	a.draw()
	a.screen.Show()
	if strings.Contains(screenText(a), "Recent locations") {
		t.Fatal("placeholder links drawn over an open file")
	}
}

// TestEmptyEditor_RecentFilesLinkOpensThePicker checks a click on the
// link runs the ≡ verb: after the last tab closes, the picker lists the
// files that were open.
func TestEmptyEditor_RecentFilesLinkOpensThePicker(t *testing.T) {
	a, _, paths := recentFilesApp(t, 2)
	for len(a.tabs) > 0 {
		a.closeTab(0)
	}
	a.draw()

	l := linkNamed(t, a, "Recent files")
	clickAt(a, l.rect.x+l.rect.w/2, l.rect.y)

	labels := pickerLabels(t, a)
	if len(labels) != 2 {
		t.Fatalf("want both closed files listed, got %v", labels)
	}
	if !strings.Contains(labels[0], filepath.Base(paths[1])) {
		t.Fatalf("first row = %q, want the last active file %q", labels[0], filepath.Base(paths[1]))
	}
	if a.dragMode != "" {
		t.Fatalf("a link press must not start a drag, dragMode = %q", a.dragMode)
	}
}

// TestEmptyEditor_RecentLocationsLinkOpensThePicker checks the second
// link runs the Recent locations verb.
func TestEmptyEditor_RecentLocationsLinkOpensThePicker(t *testing.T) {
	root := t.TempDir()
	dirs := mkdirs(t, root, "alpha")
	a := newTestApp(t, root)
	useFolder(a, dirs[0], 1)
	a.draw()

	l := linkNamed(t, a, "Recent locations")
	clickAt(a, l.rect.x, l.rect.y)

	m := paletteOf(a)
	if m == nil {
		t.Fatalf("modal = %T, want the locations picker", a.modal)
	}
	if !strings.HasPrefix(m.title, "Recent locations") {
		t.Fatalf("picker title = %q, want Recent locations", m.title)
	}
}

// TestEmptyEditor_EmptyHistoryFlashesWhy checks a link with nothing
// behind it explains itself instead of doing nothing.
func TestEmptyEditor_EmptyHistoryFlashesWhy(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	a.draw()

	l := linkNamed(t, a, "Recent files")
	clickAt(a, l.rect.x, l.rect.y)
	if a.modal != nil {
		t.Fatalf("no history should open nothing, got %T", a.modal)
	}
	if !strings.Contains(a.statusMsg, "recent files") {
		t.Fatalf("status = %q, want the empty-list explanation", a.statusMsg)
	}
}

// TestEmptyEditor_MissOpensNothing checks a click beside the links (the
// separator) is not a link press.
func TestEmptyEditor_MissOpensNothing(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	a.draw()
	files := linkNamed(t, a, "Recent files")
	if a.emptyEditorPress(files.rect.x+files.rect.w+1, files.rect.y) {
		t.Fatal("a press on the separator ran a link")
	}
}

// TestEmptyEditor_NarrowBandStacksAndClips checks a band too narrow for
// one row stacks the links and keeps every rect inside the band.
func TestEmptyEditor_NarrowBandStacksAndClips(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	scr := a.screen.(tcell.SimulationScreen)
	// The minimum window beside the default tree leaves a band narrower
	// than the one-row layout (12 + 7 + 16 = 35 cells) but wider than
	// either label (16). The width is clamped, so assert rather than hunt.
	scr.SetSize(minWidth, 30)
	a.width, a.height = minWidth, 30
	ex, _, ew, _ := a.editorRect()
	if ew >= 35 || ew < 16 {
		t.Fatalf("fixture band is %d cells, want [16, 35)", ew)
	}
	links := a.emptyEditorLinks()
	if len(links) != 2 {
		t.Fatalf("want both links stacked, got %v", links)
	}
	if links[0].rect.y == links[1].rect.y {
		t.Fatalf("narrow band should stack the links: %v", links)
	}
	for _, l := range links {
		if l.rect.x < ex || l.rect.x+l.rect.w > ex+ew {
			t.Fatalf("link %q rect %v outside band [%d, %d)", l.label, l.rect, ex, ex+ew)
		}
	}
}

// TestClipRect checks both edges trim and a disjoint rect comes back
// empty.
func TestClipRect(t *testing.T) {
	cases := []struct {
		in   btnRect
		want btnRect
	}{
		{btnRect{x: 5, y: 1, w: 4}, btnRect{x: 5, y: 1, w: 4}},   // inside
		{btnRect{x: 2, y: 1, w: 6}, btnRect{x: 4, y: 1, w: 4}},   // left edge
		{btnRect{x: 8, y: 1, w: 6}, btnRect{x: 8, y: 1, w: 4}},   // right edge
		{btnRect{x: 20, y: 1, w: 3}, btnRect{x: 20, y: 1, w: 0}}, // outside
	}
	for _, c := range cases {
		if got := clipRect(c.in, 4, 8); got != c.want {
			t.Errorf("clipRect(%v) = %v, want %v", c.in, got, c.want)
		}
	}
}

// TestCentredRect checks centring and the left-edge floor for a label
// wider than the band.
func TestCentredRect(t *testing.T) {
	if got := centredRect(10, 20, 3, 6); got != (btnRect{x: 17, y: 3, w: 6}) {
		t.Errorf("centred = %v", got)
	}
	if got := centredRect(10, 4, 3, 6); got.x != 10 {
		t.Errorf("wide label should floor at the band edge, got %v", got)
	}
}
