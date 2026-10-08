// =============================================================================
// File: internal/app/tabcontext_test.go
// Author: Rohan Allison <rohanthewiz@gmail.com>
// Created: 2026-09-30
// Copyright: 2026 Rohan Allison. All rights reserved.
// =============================================================================

package app

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gdamore/tcell/v2"

	"github.com/rohanthewiz/ced/internal/editor"
)

// tabMenuTestApp opens three files — a.go, b.go and sub/deep/c.go — with
// a.go active and draws once so the tab strip's rects are stamped.
func tabMenuTestApp(t *testing.T) (*App, []string) {
	t.Helper()
	root := t.TempDir()
	paths := []string{
		filepath.Join(root, "a.go"),
		filepath.Join(root, "b.go"),
		filepath.Join(root, "sub", "deep", "c.go"),
	}
	for _, p := range paths {
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte("package x\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	a := newTestApp(t, root)
	a.width, a.height = 160, 40
	for _, p := range paths {
		a.openFile(p)
	}
	a.switchToTab(0)
	a.draw()
	return a, paths
}

// tabRectFor returns the stamped rect of the tab showing path.
func tabRectFor(t *testing.T, a *App, path string) tabRect {
	t.Helper()
	for _, r := range a.lastTabRects {
		if a.tabs[r.Index].Path == path {
			return r
		}
	}
	t.Fatalf("no drawn tab for %s", path)
	return tabRect{}
}

// rightClick sends a secondary-button press at (x, y).
func rightClick(a *App, x, y int) {
	a.handleMouse(tcell.NewEventMouse(x, y, tcell.ButtonSecondary, tcell.ModNone))
}

// runTabMenuRow activates the open tab menu's row with the given label.
func runTabMenuRow(t *testing.T, a *App, label string) {
	t.Helper()
	m, ok := a.modal.(*editorContextModal)
	if !ok {
		t.Fatalf("modal = %T, want the tab menu", a.modal)
	}
	for i, it := range m.items {
		if it.label == label {
			m.hover = i
			m.activate(a)
			return
		}
	}
	t.Fatalf("tab menu has no %q row", label)
}

// TestTabContext_RightClickOpensTheTabMenu pins the gesture: a right-click
// on a tab opens that tab's menu rather than the ≡ menu, and the clicked
// tab is not brought to the front by opening it.
func TestTabContext_RightClickOpensTheTabMenu(t *testing.T) {
	a, paths := tabMenuTestApp(t)
	r := tabRectFor(t, a, paths[1])
	_, sy, _ := a.tabStripRect()

	rightClick(a, r.X+2, sy)

	m, ok := a.modal.(*editorContextModal)
	if !ok {
		t.Fatalf("modal = %T (menuOpen=%v), want the tab menu", a.modal, a.menuOpen)
	}
	var labels []string
	for _, it := range m.items {
		labels = append(labels, it.label)
	}
	want := "Reveal in file tree|Show uncommitted changes|Show git history|Compare with clipboard|" +
		"Format file|Validate file|Restore (discard changes)…|Add to group…|Zip file|Copy to…|Copy relative path|Copy absolute path|Close tab|Close other tabs"
	if got := strings.Join(labels, "|"); got != want {
		t.Errorf("rows = %s, want %s", got, want)
	}
	if a.activeTabPtr().Path != paths[0] {
		t.Errorf("active tab = %s, want a.go still (the menu acts in place)", a.activeTabPtr().Path)
	}
}

// TestTabContext_MissFallsThroughToMenu pins that only a drawn tab claims
// the gesture: a right-click on the empty strip still opens ≡.
func TestTabContext_MissFallsThroughToMenu(t *testing.T) {
	a, _ := tabMenuTestApp(t)
	_, sy, _ := a.tabStripRect()
	last := a.lastTabRects[len(a.lastTabRects)-1]
	if a.tryTabContextClick(last.X+last.Width+3, sy) {
		t.Fatal("empty strip claimed the right-click")
	}
	if a.tryTabContextClick(last.X+1, sy+1) {
		t.Fatal("a row below the strip claimed the right-click")
	}
}

// TestTabContext_CloseBackgroundTabKeepsTheView pins that "Close tab" on a
// background tab closes THAT tab and leaves the active one in front.
func TestTabContext_CloseBackgroundTabKeepsTheView(t *testing.T) {
	a, paths := tabMenuTestApp(t)
	a.switchToTab(2) // c.go active; close a.go to its left
	a.draw()
	_, sy, _ := a.tabStripRect()
	rightClick(a, tabRectFor(t, a, paths[0]).X+2, sy)
	runTabMenuRow(t, a, "Close tab")

	if len(a.tabs) != 2 {
		t.Fatalf("tabs = %d, want 2", len(a.tabs))
	}
	if got := a.activeTabPtr().Path; got != paths[2] {
		t.Errorf("active = %s, want c.go still in front", got)
	}
}

// TestCloseTab_LeftOfActiveKeepsActive is the bug the tab menu exposed:
// closing a tab to the left of the active one used to leave activeTab
// pointing one slot too far, switching the view to a neighbour. Fails
// against the old closeTab.
func TestCloseTab_LeftOfActiveKeepsActive(t *testing.T) {
	a, paths := tabMenuTestApp(t)
	a.switchToTab(1) // b.go
	a.closeTab(0)
	if got := a.activeTabPtr().Path; got != paths[1] {
		t.Errorf("active = %s after closing a tab to its left, want b.go", got)
	}
	// Closing the active tab still hands over to its right neighbour.
	a.closeTab(a.activeTab)
	if got := a.activeTabPtr().Path; got != paths[2] {
		t.Errorf("active = %s after closing the active tab, want c.go", got)
	}
}

// TestTabContext_RevealSelectsTheFileInTheTree pins the original ask: the
// tab's file is selected in the tree with its folders expanded, the
// sidebar is shown, and the keyboard stays with the editor.
func TestTabContext_RevealSelectsTheFileInTheTree(t *testing.T) {
	a, paths := tabMenuTestApp(t)
	a.sidebarShown = false
	for _, n := range a.tree.Root.Children {
		n.Expanded = false
	}

	_, sy, _ := a.tabStripRect()
	rightClick(a, tabRectFor(t, a, paths[2]).X+2, sy)
	runTabMenuRow(t, a, "Reveal in file tree")

	if !a.sidebarShown {
		t.Error("sidebar still hidden after a reveal")
	}
	if a.tree.Selected == nil || a.tree.Selected.Path != paths[2] {
		t.Errorf("tree selection = %v, want %s", a.tree.Selected, paths[2])
	}
	if a.treeFocus {
		t.Error("reveal took keyboard focus; the next keystroke would filter the tree")
	}
	if got := a.activeTabPtr().Path; got != paths[2] {
		t.Errorf("active = %s, want the revealed file", got)
	}
}

// TestMenuRevealActiveFile pins the ≡ Nav twin: it reveals the ACTIVE tab.
func TestMenuRevealActiveFile(t *testing.T) {
	a, paths := tabMenuTestApp(t)
	a.switchToTab(2)
	a.menuRevealActiveFile()
	if a.tree.Selected == nil || a.tree.Selected.Path != paths[2] {
		t.Errorf("tree selection = %v, want %s", a.tree.Selected, paths[2])
	}
}

// TestCloseOtherTabs_KeepsUnsavedAndSaysSo pins the sweep: clean tabs go,
// dirty ones stay and are counted in the flash, and the kept tab ends up
// active.
func TestCloseOtherTabs_KeepsUnsavedAndSaysSo(t *testing.T) {
	a, paths := tabMenuTestApp(t)
	a.tabs[2].Dirty = true
	keep := a.tabs[1]

	a.closeOtherTabs(keep)

	var left []string
	for _, tab := range a.tabs {
		left = append(left, filepath.Base(tab.Path))
	}
	if got := strings.Join(left, ","); got != "b.go,c.go" {
		t.Errorf("tabs left = %s, want b.go,c.go (dirty c.go kept)", got)
	}
	if a.activeTabPtr() != keep {
		t.Errorf("active = %s, want %s", a.activeTabPtr().Path, paths[1])
	}
	if !strings.Contains(a.statusMsg, "Kept 1 unsaved tab") {
		t.Errorf("flash = %q, want the kept count", a.statusMsg)
	}
}

// TestTabContext_RowsDimWithoutAPath pins the enabled predicates: an
// untitled tab has no path to reveal, check, zip or copy, and a lone tab
// has no others to close. Comparing needs only a buffer, so it stays.
func TestTabContext_RowsDimWithoutAPath(t *testing.T) {
	a, _ := tabMenuTestApp(t)
	a.closeOtherTabs(a.tabs[0])
	a.gitIsRepo = true // the git rows must dim on the path, not the repo
	tab := a.tabs[0]
	tab.Path = ""
	live := map[string]bool{"Close tab": true, "Compare with clipboard": true, "Add to group…": true}
	for _, it := range a.tabContextItems(tab) {
		if on := it.enabled(a); live[it.label] != on {
			t.Errorf("%q enabled = %v", it.label, on)
		}
	}
}

// tabMenuLabels lists the rows tabContextItems builds for t.
func tabMenuLabels(a *App, t *editor.Tab) []string {
	var out []string
	for _, it := range a.tabContextItems(t) {
		out = append(out, it.label)
	}
	return out
}

// TestTabContext_GitRowsDimOutsideARepo pins the git rows' predicate: a
// saved file outside a repository has no changes or history to show.
func TestTabContext_GitRowsDimOutsideARepo(t *testing.T) {
	a, _ := tabMenuTestApp(t)
	for _, it := range a.tabContextItems(a.tabs[1]) {
		if strings.HasPrefix(it.label, "Show ") && it.enabled(a) {
			t.Errorf("%q enabled outside a repo", it.label)
		}
	}
}

// TestTabContext_PreviewOnlyOnMarkdown pins the Preview row: absent on a
// source file, present on markdown, and run from a BACKGROUND tab it
// brings that tab forward and previews it — the label then offers the
// way back.
func TestTabContext_PreviewOnlyOnMarkdown(t *testing.T) {
	a, paths := tabMenuTestApp(t)
	if strings.Contains(strings.Join(tabMenuLabels(a, a.tabs[1]), "|"), "Preview") {
		t.Error("a .go tab offered Preview")
	}
	md := filepath.Join(filepath.Dir(paths[0]), "README.md")
	if err := os.WriteFile(md, []byte("# hi\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	a.openFile(md)
	mdTab := a.activeTabPtr()
	a.switchToTab(0)
	a.draw()

	_, sy, _ := a.tabStripRect()
	rightClick(a, tabRectFor(t, a, md).X+2, sy)
	runTabMenuRow(t, a, "Preview")

	if a.activeTabPtr() != mdTab || !mdTab.IsMarkdownView() {
		t.Fatalf("active=%s preview=%v, want README.md previewed in front",
			a.activeTabPtr().Path, mdTab.IsMarkdownView())
	}
	if !strings.Contains(strings.Join(tabMenuLabels(a, mdTab), "|"), "Stop preview") {
		t.Error("a previewed tab should offer Stop preview")
	}
}

// TestTabContext_ValidateRunsOnTheClickedTab pins onTab: a verb that
// works on the active tab is run on the CLICKED one, which is brought to
// the front first so its answer (here, a caret on the problem) is seen.
func TestTabContext_ValidateRunsOnTheClickedTab(t *testing.T) {
	a, paths := tabMenuTestApp(t)
	bad := filepath.Join(filepath.Dir(paths[0]), "conf.json")
	if err := os.WriteFile(bad, []byte("{\n  \"a\": 1,\n}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	a.openFile(bad)
	a.switchToTab(0)
	a.draw()

	_, sy, _ := a.tabStripRect()
	rightClick(a, tabRectFor(t, a, bad).X+2, sy)
	runTabMenuRow(t, a, "Validate file")

	if a.activeTabPtr().Path != bad {
		t.Fatalf("active = %s, want conf.json brought forward", a.activeTabPtr().Path)
	}
	if !strings.HasPrefix(a.statusMsg, "conf.json: 1 error") {
		t.Errorf("flash = %q", a.statusMsg)
	}
}

// TestTabContext_ZipActsInPlace pins that Zip, whose answer is a file on
// disk, does not drag the clicked tab to the front.
func TestTabContext_ZipActsInPlace(t *testing.T) {
	a, paths := tabMenuTestApp(t)
	_, sy, _ := a.tabStripRect()
	rightClick(a, tabRectFor(t, a, paths[1]).X+2, sy)
	runTabMenuRow(t, a, "Zip file")

	if a.activeTabPtr().Path != paths[0] {
		t.Errorf("active = %s, want a.go still", a.activeTabPtr().Path)
	}
	e := waitForZipEvent(t, a)
	a.handleEvent(e)
	if _, err := os.Stat(paths[1] + ".zip"); err != nil {
		t.Errorf("no b.go.zip beside b.go: %v", err)
	}
}

// TestTabContext_CopyToCopiesTheClickedTabInPlace pins N-055's row: Copy
// to… on a BACKGROUND tab copies that tab's file (its unsaved buffer, as
// the ≡ twin would) and leaves the active tab in front.
func TestTabContext_CopyToCopiesTheClickedTabInPlace(t *testing.T) {
	a, paths := tabMenuTestApp(t)
	elsewhere := t.TempDir()
	a.tabs[1].InsertString("// edited\n")

	_, sy, _ := a.tabStripRect()
	rightClick(a, tabRectFor(t, a, paths[1]).X+2, sy)
	runTabMenuRow(t, a, "Copy to…")
	submitCopyTo(t, a, elsewhere)
	a.handlePasteDone(waitForPasteEvent(t, a))

	if a.activeTabPtr().Path != paths[0] {
		t.Errorf("active = %s, want a.go still", a.activeTabPtr().Path)
	}
	if got := readString(t, filepath.Join(elsewhere, "b.go")); got != "// edited\npackage x\n" {
		t.Errorf("copy = %q, want b.go's buffer", got)
	}
	if _, err := os.Stat(filepath.Join(elsewhere, "a.go")); err == nil {
		t.Error("copied the active tab instead of the clicked one")
	}
}

// TestTabContext_SplitRowsOnlyInsideCats pins the split rows' gate: absent
// in a plain terminal (no host can split), present inside cats, and Copy
// leaves the tab open here.
func TestTabContext_SplitRowsOnlyInsideCats(t *testing.T) {
	a, paths := tabMenuTestApp(t)
	if strings.Contains(strings.Join(tabMenuLabels(a, a.tabs[1]), "|"), "split") {
		t.Fatal("split rows outside cats")
	}
	s := withCtlSpy(t, a)
	catsExecutable = func() (string, error) { return "/opt/bin/ced", nil }
	t.Cleanup(func() { catsExecutable = os.Executable })
	labels := strings.Join(tabMenuLabels(a, a.tabs[1]), "|")
	if !strings.Contains(labels, "Move to split →|Copy to split →") {
		t.Fatalf("rows = %s, want the split pair", labels)
	}

	_, sy, _ := a.tabStripRect()
	rightClick(a, tabRectFor(t, a, paths[1]).X+2, sy)
	runTabMenuRow(t, a, "Copy to split →")
	s.wait(t)
	pumpAppEvents(t, a, func() bool { return strings.Contains(a.statusMsg, "Opened b.go in a split") })
	if len(a.tabs) != 3 || a.activeTabPtr().Path != paths[0] {
		t.Errorf("tabs=%d active=%s, want all three kept and a.go in front", len(a.tabs), a.activeTabPtr().Path)
	}
}
