// =============================================================================
// File: internal/app/toolmenu_test.go
// Author: Rohan Allison <rohanthewiz@gmail.com>
// Created: 2026-09-10
// Copyright: 2026 Rohan Allison. All rights reserved.
// =============================================================================

package app

import (
	"strings"
	"testing"
)

// TestMenuToolWindows_ListsEveryToolAndToggles covers the first row: one
// picker entry per registered tool, and picking one toggles it. A tool
// added to the registry therefore appears here with no menu change at
// all, which is the reason the group is three rows rather than twenty.
func TestMenuToolWindows_ListsEveryTool(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	a.gitIsRepo = true
	a.menuToolWindows()

	p, ok := a.modal.(*paletteModal)
	if !ok {
		t.Fatalf("modal = %T, want the tool picker", a.modal)
	}
	if len(p.items) != len(toolDefs) {
		t.Fatalf("%d rows for %d tools", len(p.items), len(toolDefs))
	}
	for _, d := range toolDefs {
		found := false
		for _, it := range p.items {
			if strings.HasPrefix(it.label, d.title+" ") {
				found = true
			}
		}
		if !found {
			t.Errorf("no row for %q", d.title)
		}
	}

	// Picking the git row shows it.
	for _, it := range p.items {
		if strings.HasPrefix(it.label, "Git ·") {
			it.run(a)
		}
	}
	if !a.gitPanel.open {
		t.Error("the picked row should have shown the git panel")
	}
}

// TestToolPickerLabel_NameFirst pins the go-to-symbol rule as it applies
// here: the fuzzy scorer rewards early matches, so the annotations trail
// the name — a leading "shown" or "left" would make every row score
// alike on the first letters typed.
func TestToolPickerLabel_NameFirst(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	label := a.toolPickerLabel(toolProject)
	if !strings.HasPrefix(label, "Project") {
		t.Errorf("label = %q, want it to start with the tool's name", label)
	}
	if !strings.Contains(label, "shown") || !strings.Contains(label, "left") {
		t.Errorf("label = %q, want it to carry the state and the edge", label)
	}
	a.hideTool(toolProject)
	if got := a.toolPickerLabel(toolProject); !strings.Contains(got, "hidden") {
		t.Errorf("label = %q after hiding, want it to say so", got)
	}
}

// TestOpenToolDockPicker_OmitsTheCurrentEdge pins the code-actions rule:
// the palette has no disabled state to borrow, so a row that would
// answer Enter with "it is already there" is not offered at all.
func TestOpenToolDockPicker_OmitsTheCurrentEdge(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	a.openToolDockPicker(toolProject) // currently left

	p, ok := a.modal.(*paletteModal)
	if !ok {
		t.Fatalf("modal = %T, want the edge picker", a.modal)
	}
	if len(p.items) != len(dockSides)-1 {
		t.Fatalf("%d rows, want %d (every edge but the current one)", len(p.items), len(dockSides)-1)
	}
	for _, it := range p.items {
		if strings.Contains(it.label, "to the left") {
			t.Errorf("the current edge was offered: %q", it.label)
		}
	}

	// Picking one moves AND shows — a "put it THERE" gesture must not
	// move something invisible and read as doing nothing.
	a.hideTool(toolProject)
	for _, it := range p.items {
		if strings.Contains(it.label, "to the right") {
			it.run(a)
		}
	}
	if got := a.toolDock(toolProject); got != dockRight {
		t.Errorf("dock = %q after the pick, want right", got)
	}
	if !a.sidebarShown {
		t.Error("picking an edge should have shown the tool there")
	}
}

// TestMenuResetToolLayout_NoConfirmation pins the favorites rule: reset
// destroys nothing — the tabs, the shell session, the transcript and the
// panels' own state all survive a re-arrangement — and a dialog in front
// of a reversible action trains people to dismiss dialogs.
func TestMenuResetToolLayout_NoConfirmation(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	a.gitIsRepo = true
	a.moveTool(toolGit, dockRight)
	a.showTool(toolGit)

	a.menuResetToolLayout()

	if a.modal != nil {
		t.Errorf("reset opened %T, want no dialog", a.modal)
	}
	if got := a.toolDock(toolGit); got != dockBottom {
		t.Errorf("git docks %q after reset, want its default", got)
	}
	if a.statusMsg == "" {
		t.Error("reset should say what it did")
	}
}

// TestToolWindowsLabel_CountsWhatIsShowing pins the theme row's trick:
// the row answers "where is everything?" without being clicked, and it
// counts what is SHOWING because that is the half the user can already
// see and is checking their memory against.
func TestToolWindowsLabel_CountsWhatIsShowing(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	a.gitIsRepo = true

	if got := a.toolWindowsLabel(); !strings.Contains(got, "1 shown") {
		t.Errorf("label = %q with only the tree up, want \"1 shown\"", got)
	}
	a.showTool(toolGit)
	if got := a.toolWindowsLabel(); !strings.Contains(got, "2 shown") {
		t.Errorf("label = %q with two tools up", got)
	}
	a.hideTool(toolProject)
	a.hideTool(toolGit)
	if got := a.toolWindowsLabel(); !strings.Contains(got, "none shown") {
		t.Errorf("label = %q with nothing up", got)
	}
}

// TestToolLayoutSummary_SortedByEdge pins the reporting helper's
// stability: edges in the dockSides order, tools in registry order, so
// two runs of one arrangement produce the same string.
func TestToolLayoutSummary_SortedByEdge(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	first := a.toolLayoutSummary()
	if first != a.toolLayoutSummary() {
		t.Fatal("the summary is not stable across calls")
	}
	if !strings.HasPrefix(first, "left: Project*") {
		t.Errorf("summary = %q, want it to open with the showing left-edge tool", first)
	}
	if strings.Index(first, "left:") > strings.Index(first, "bottom:") {
		t.Errorf("summary = %q, want the edges in dockSides order", first)
	}
}

// TestSortedToolIDs_IsStable pins the ordering the persisted layout is
// written in: a map would round-trip in whatever order Go felt like, and
// a state file that changed shape on every save is one nobody can diff.
func TestSortedToolIDs_IsStable(t *testing.T) {
	a, b := sortedToolIDs(), sortedToolIDs()
	if len(a) != len(toolDefs) {
		t.Fatalf("%d ids for %d tools", len(a), len(toolDefs))
	}
	for i := range a {
		if a[i] != b[i] {
			t.Fatalf("unstable order at %d: %q vs %q", i, a[i], b[i])
		}
		if i > 0 && a[i-1] >= a[i] {
			t.Errorf("not sorted: %q before %q", a[i-1], a[i])
		}
	}
}
