// =============================================================================
// File: internal/app/tabbar_test.go
// Author: Rohan Allison <rohanthewiz@gmail.com>
// Created: 2026-08-03
// Copyright: 2026 Rohan Allison. All rights reserved.
// =============================================================================

package app

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// tabBarApp opens n files named tab0.go … tab<n-1>.go in a throwaway root
// and returns the app. The names are deliberately uniform so tab widths
// are predictable in the scroll assertions.
func tabBarApp(t *testing.T, n int) *App {
	t.Helper()
	dir := t.TempDir()
	var paths []string
	for i := range n {
		p := filepath.Join(dir, fmt.Sprintf("tab%d.go", i))
		if err := os.WriteFile(p, []byte("package main\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		paths = append(paths, p)
	}
	a := newTestApp(t, dir)
	for _, p := range paths {
		a.openFile(p)
	}
	if len(a.tabs) != n {
		t.Fatalf("expected %d tabs, got %d", n, len(a.tabs))
	}
	return a
}

// drawnTabIndexes returns which tabs the strip actually laid out.
func drawnTabIndexes(a *App) []int {
	var out []int
	for _, r := range a.layoutTabs() {
		out = append(out, r.Index)
	}
	return out
}

// TestTabStrip_ActiveTabAlwaysDrawn is the bug this stage exists for:
// tabs used to be laid out left to right without bound and clipped at the
// band edge, so with enough files open the ACTIVE tab could be off-screen
// — invisible and unclickable, with no key binding to reach it either.
func TestTabStrip_ActiveTabAlwaysDrawn(t *testing.T) {
	a := tabBarApp(t, 20)
	// Narrow enough that only a handful of tabs fit at a time.
	a.width = 60
	for idx := range a.tabs {
		a.activeTab = idx
		drawn := drawnTabIndexes(a)
		found := false
		for _, i := range drawn {
			if i == idx {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("active tab %d not drawn; strip showed %v", idx, drawn)
		}
	}
}

// TestTabStrip_NoTabOverrunsTheStrip pins that the layout stops at the
// edge rather than relying on the paint loop to clip. lastTabRects is
// what hit-testing consumes, so a rect past the edge would make a click
// land on a tab the user cannot see.
func TestTabStrip_NoTabOverrunsTheStrip(t *testing.T) {
	a := tabBarApp(t, 20)
	a.width = 80
	a.activeTab = 10
	sx, _, sw := a.tabStripRect()
	for _, r := range a.layoutTabs() {
		if r.X < sx || r.X+r.Width > sx+sw {
			t.Fatalf("tab %d at [%d,%d) escapes the strip [%d,%d)",
				r.Index, r.X, r.X+r.Width, sx, sx+sw)
		}
	}
}

// TestTabStrip_ScrollPullsBackWhenSpaceFrees: closing tabs while scrolled
// must not leave the strip parked to the right with dead space trailing
// it. The backwards half of ensureActiveTabVisible is easy to omit and
// invisible in tests that only ever open files.
func TestTabStrip_ScrollPullsBackWhenSpaceFrees(t *testing.T) {
	a := tabBarApp(t, 20)
	a.width = 120
	a.activeTab = 19
	a.layoutTabs()
	if a.tabScroll == 0 {
		t.Fatal("precondition: the strip should be scrolled with 20 tabs at width 120")
	}

	// Everything but the first three closes.
	a.tabs = a.tabs[:3]
	a.activeTab = 2
	a.layoutTabs()
	if a.tabScroll != 0 {
		t.Fatalf("scroll should have returned to 0 once the tabs fit, got %d", a.tabScroll)
	}
}

// TestTabOverflow_CountsHiddenTabsAndOpensThePicker covers the affordance:
// with tabs off-screen the button says how many, and clicking it opens
// the switcher — the only mouse path to a tab that isn't drawn.
func TestTabOverflow_CountsHiddenTabsAndOpensThePicker(t *testing.T) {
	a := tabBarApp(t, 20)
	a.width = 60
	a.activeTab = 0
	a.draw() // populates lastTabRects, which the overflow count reads

	x, _, w, hidden, ok := a.tabOverflowRect()
	if !ok {
		t.Fatal("20 tabs at width 60 must show the overflow button")
	}
	if hidden != len(a.tabs)-len(a.lastTabRects) {
		t.Fatalf("hidden = %d, want %d", hidden, len(a.tabs)-len(a.lastTabRects))
	}
	if w != len([]rune(a.tabOverflowLabel(hidden))) {
		t.Fatalf("button width %d does not match its label", w)
	}

	a.tabBarClick(x, 0)
	if _, isPicker := a.modal.(*paletteModal); !isPicker {
		t.Fatalf("clicking the overflow button should open the switcher, got %T", a.modal)
	}
}

// TestTabOverflow_HiddenWhenEverythingFits keeps the button from becoming
// permanent chrome — with a handful of files it must take no columns at
// all, or it would eat width the tabs could have used.
func TestTabOverflow_HiddenWhenEverythingFits(t *testing.T) {
	a := tabBarApp(t, 3)
	a.width = 200
	a.draw()
	if _, _, _, _, ok := a.tabOverflowRect(); ok {
		t.Fatal("three tabs on a wide window must not show an overflow button")
	}
	if got := a.tabOverflowReserve(); got != 0 {
		t.Fatalf("no overflow button should reserve no columns, got %d", got)
	}
}

// TestSwitchToTab_RecordsNavigationOnce pins the one place tab switching
// touches the history. Every surface routes through switchToTab, and a
// switch to the tab already showing is not navigation — recording it
// would spend Go back's first press on a no-op.
func TestSwitchToTab_RecordsNavigationOnce(t *testing.T) {
	a := tabBarApp(t, 3)
	a.activeTab = 0
	before := len(a.nav.back)

	a.switchToTab(2)
	if a.activeTab != 2 {
		t.Fatalf("switch did not take effect, active = %d", a.activeTab)
	}
	if len(a.nav.back) != before+1 {
		t.Fatalf("switch should record one history entry, got %d", len(a.nav.back)-before)
	}

	a.switchToTab(2)
	if len(a.nav.back) != before+1 {
		t.Fatal("switching to the active tab must record nothing")
	}
	a.switchToTab(99)
	if a.activeTab != 2 {
		t.Fatal("an out-of-range index must be ignored")
	}
}

// TestSwitchToTab_FlushesDepartingTab pins the in-editor twin of losing
// window focus: a file you can no longer see should not still own a
// countdown. Being the single funnel is what lets this live in one
// place and cover every switching surface.
func TestSwitchToTab_FlushesDepartingTab(t *testing.T) {
	useTestTrustFile(t)
	a := newTestApp(t, t.TempDir())
	a.autoSaveEnabled = true
	t.Cleanup(a.stopAutoSave)
	leaving := openScratch(t, a, "a.txt", "a\n")
	openScratch(t, a, "b.txt", "b\n")

	a.activeTab = 0
	leaving.InsertString("edited ")
	want := leaving.Buffer.String()

	a.switchToTab(1)

	if leaving.Dirty {
		t.Fatal("the departing tab should have been flushed")
	}
	got, _ := os.ReadFile(leaving.Path)
	if string(got) != want {
		t.Fatalf("disk = %q, want the departing buffer %q", got, want)
	}
}

// TestSwitchToTab_NoFlushWhenAutoSaveOff pins that the ≡ toggle wins
// here too — the flush is auto-save, not a separate feature.
func TestSwitchToTab_NoFlushWhenAutoSaveOff(t *testing.T) {
	useTestTrustFile(t)
	a := newTestApp(t, t.TempDir())
	a.autoSaveEnabled = false
	leaving := openScratch(t, a, "a.txt", "a\n")
	openScratch(t, a, "b.txt", "b\n")

	a.activeTab = 0
	leaving.InsertString("edited ")

	a.switchToTab(1)

	if !leaving.Dirty {
		t.Fatal("with auto-save off, switching tabs must write nothing")
	}
	got, _ := os.ReadFile(leaving.Path)
	if string(got) != "a\n" {
		t.Fatalf("disk changed with auto-save off: %q", got)
	}
}

// TestNextPrevTab_Wrap pins the wrap. With the strip scrolled, next/prev
// is a gesture repeated without looking, and stopping dead at the end
// reads as a stuck key.
func TestNextPrevTab_Wrap(t *testing.T) {
	a := tabBarApp(t, 3)
	a.activeTab = 2

	a.menuNextTab()
	if a.activeTab != 0 {
		t.Fatalf("next from the last tab should wrap to 0, got %d", a.activeTab)
	}
	a.menuPrevTab()
	if a.activeTab != 2 {
		t.Fatalf("prev from the first tab should wrap to the last, got %d", a.activeTab)
	}
}

// TestNextPrevTab_SingleTabIsANoOp — with one file open there is nowhere
// to go, and the wrap arithmetic must not land back on itself noisily.
func TestNextPrevTab_SingleTabIsANoOp(t *testing.T) {
	a := tabBarApp(t, 1)
	before := len(a.nav.back)
	a.menuNextTab()
	a.menuPrevTab()
	if a.activeTab != 0 || len(a.nav.back) != before {
		t.Fatal("switching with one tab open must do nothing at all")
	}
}

// TestSwitchTabPicker_ExcludesCurrentAndNamesDirectories: picking the
// current tab is the one choice guaranteed to do nothing, and the
// directory is what tells four open main.go files apart.
func TestSwitchTabPicker_ExcludesCurrentAndNamesDirectories(t *testing.T) {
	dir := t.TempDir()
	for _, sub := range []string{"alpha", "bravo"} {
		if err := os.MkdirAll(filepath.Join(dir, sub), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, sub, "main.go"), []byte("package main\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	a := newTestApp(t, dir)
	a.openFile(filepath.Join(dir, "alpha", "main.go"))
	a.openFile(filepath.Join(dir, "bravo", "main.go"))
	a.activeTab = 0

	a.menuSwitchTab()
	m, ok := a.modal.(*paletteModal)
	if !ok {
		t.Fatalf("expected the picker, got %T", a.modal)
	}
	if len(m.items) != 1 {
		t.Fatalf("the current tab should be excluded, got %d rows", len(m.items))
	}
	if !strings.Contains(m.items[0].label, "bravo") {
		t.Fatalf("row should name the directory that disambiguates it: %q", m.items[0].label)
	}
	if !strings.Contains(m.title, "alpha") && !strings.Contains(m.title, "main.go") {
		t.Fatalf("title should name the current tab: %q", m.title)
	}
}

// TestSwitchTabPicker_MarksDirtyTabs — the switcher is where a user with
// a dozen files open looks for the one they haven't saved.
func TestSwitchTabPicker_MarksDirtyTabs(t *testing.T) {
	a := tabBarApp(t, 3)
	a.activeTab = 0
	a.tabs[2].InsertRune('x')

	a.menuSwitchTab()
	m := a.modal.(*paletteModal)
	found := false
	for _, it := range m.items {
		if strings.HasPrefix(it.label, "●") {
			found = true
		}
	}
	if !found {
		t.Fatal("a dirty tab should carry the dirty marker in the switcher")
	}
}
