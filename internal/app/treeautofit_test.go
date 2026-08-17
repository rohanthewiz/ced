// =============================================================================
// File: internal/app/treeautofit_test.go
// Author: Rohan Allison <rohanthewiz@gmail.com>
// Created: 2026-08-17
// Copyright: 2026 Rohan Allison. All rights reserved.
// =============================================================================

package app

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gdamore/tcell/v2"

	"github.com/rohanthewiz/ced/internal/userconfig"
)

// autoFitApp builds an App whose tree carries one deeply nested, long
// name — the case auto-fit exists for — with the config redirected at a
// temp dir so the drag/menu paths can persist without touching the
// developer's real config.json.
func autoFitApp(t *testing.T) *App {
	t.Helper()
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	root := t.TempDir()
	deep := filepath.Join(root, "internal", "app")
	if err := os.MkdirAll(deep, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(deep, "a-quite-long-file-name.go"), []byte("package app"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	a := newTestApp(t, root)
	a.treeAutoFit = true
	// Expand down to the long row so the tree actually wants the columns.
	internal := a.tree.Root.Children[0]
	a.tree.Toggle(internal)
	a.tree.Toggle(internal.Children[0])
	return a
}

// TestAutoFitSidebar_WidensToFitTheTree is the feature: an expanded deep
// folder stops truncating names, and the width it lands on is exactly the
// tree's content plus the splitter column.
func TestAutoFitSidebar_WidensToFitTheTree(t *testing.T) {
	a := autoFitApp(t)
	want := a.tree.ContentWidth() + 1
	if want <= defaultSidebarWidth {
		t.Fatalf("fixture is not wide enough to test growth (%d)", want)
	}
	a.autoFitSidebar()
	if a.sidebarWidth != want {
		t.Fatalf("sidebarWidth = %d, want %d", a.sidebarWidth, want)
	}
	// And the tree's own render rect is wide enough for the content, which
	// is what the +1 is for.
	if _, _, w, _ := a.sidebarRect(); w < a.tree.ContentWidth() {
		t.Fatalf("tree rect %d narrower than its content %d", w, a.tree.ContentWidth())
	}
}

// TestAutoFitSidebar_KeepsEditorRoom pins the "reasonable room" half: the
// editor never drops below autoFitMinEditor, and the panel never takes
// more than its share of a wide window even when the tree wants more.
func TestAutoFitSidebar_KeepsEditorRoom(t *testing.T) {
	a := autoFitApp(t)
	// A tree that wants far more than any window here can spare: the
	// project row alone is 200 columns wide.
	a.tree.Root.Name = strings.Repeat("x", 200)

	a.width = 120
	a.autoFitSidebar()
	if got := a.width - a.sidebarWidth; got < autoFitMinEditor {
		t.Fatalf("editor left with %d columns, want at least %d", got, autoFitMinEditor)
	}

	// A very wide window: the editor floor alone would allow a 160-column
	// sidebar, so the share cap is what has to bind.
	a.width = 240
	a.autoFitSidebar()
	if got, max := a.sidebarWidth, 240/autoFitMaxShareDen; got > max {
		t.Fatalf("sidebarWidth = %d, want at most a %d-column share", got, max)
	}
}

// TestAutoFitSidebar_NoRoomLeavesWidthAlone covers the narrow terminal:
// the editor can't have its comfortable floor at even the default sidebar
// width, so auto-fit declines rather than fighting for columns that
// aren't there.
func TestAutoFitSidebar_NoRoomLeavesWidthAlone(t *testing.T) {
	a := autoFitApp(t)
	a.width = defaultSidebarWidth + autoFitMinEditor - 1
	a.sidebarWidth = 22
	a.autoFitSidebar()
	if a.sidebarWidth != 22 {
		t.Fatalf("sidebarWidth = %d, want the untouched 22", a.sidebarWidth)
	}
}

// TestAutoFitSidebar_FloorIsTheDefaultWidth documents that auto-fit only
// ever grows past the shipped width: a shallow tree with short names
// leaves the panel at its default rather than shrink-wrapping it, which
// would make every expand/collapse move the editor twice.
func TestAutoFitSidebar_FloorIsTheDefaultWidth(t *testing.T) {
	a := newTestApp(t, t.TempDir()) // empty tree, nothing to fit
	a.treeAutoFit = true
	a.sidebarWidth = 60
	a.autoFitSidebar()
	if a.sidebarWidth != defaultSidebarWidth {
		t.Fatalf("sidebarWidth = %d, want the default %d", a.sidebarWidth, defaultSidebarWidth)
	}
}

// TestAutoFitSidebar_OffOrHiddenIsANoop keeps the preference and the
// hidden-sidebar case honest — a width nobody can see must not be
// rewritten under a user who turned the panel off.
func TestAutoFitSidebar_OffOrHiddenIsANoop(t *testing.T) {
	a := autoFitApp(t)
	a.treeAutoFit = false
	a.sidebarWidth = 25
	a.autoFitSidebar()
	if a.sidebarWidth != 25 {
		t.Fatalf("auto-fit off: sidebarWidth = %d, want 25", a.sidebarWidth)
	}

	a.treeAutoFit = true
	a.sidebarShown = false
	a.autoFitSidebar()
	if a.sidebarWidth != 25 {
		t.Fatalf("sidebar hidden: sidebarWidth = %d, want 25", a.sidebarWidth)
	}
}

// TestAutoFitSidebar_FlippedLayoutLeavesTheLeftStrip checks the layout
// the chat/terminal panels create: the tree is on the right, so the cap
// has to be measured against the columns it actually shares with the
// editor, not the whole window.
func TestAutoFitSidebar_FlippedLayoutLeavesTheLeftStrip(t *testing.T) {
	a := autoFitApp(t)
	a.tree.Root.Name = "a-really-quite-long-project-directory-name-here"
	a.width = 200
	a.termDockLeft = true
	a.term.open = true
	a.autoFitSidebar()

	left := a.leftBlockW()
	if left <= 0 {
		t.Fatal("fixture should have a left-docked strip")
	}
	if got := a.width - left - a.sidebarWidth; got < autoFitMinEditor {
		t.Fatalf("editor left with %d columns beside the strip, want %d+", got, autoFitMinEditor)
	}
}

// TestAutoFitSidebar_DrawAppliesIt wires the feature to the frame: the
// width is derived before any rect is read, so the editor's own rect
// already reflects it on the very first draw after an expand.
func TestAutoFitSidebar_DrawAppliesIt(t *testing.T) {
	a := autoFitApp(t)
	want := a.tree.ContentWidth() + 1
	a.draw()
	if a.sidebarWidth != want {
		t.Fatalf("draw did not auto-fit: sidebarWidth = %d, want %d", a.sidebarWidth, want)
	}
	if ex, _, _, _ := a.editorRect(); ex != want {
		t.Fatalf("editor starts at %d, want %d — geometry read a stale width", ex, want)
	}
}

// TestSidebarDrag_LocksAutoFit pins the ownership handoff: a drag that
// MOVES the splitter turns auto-fit off and persists it (otherwise the
// next expanded folder would silently undo the drag), while a press that
// moves nothing leaves the preference alone.
func TestSidebarDrag_LocksAutoFit(t *testing.T) {
	a := autoFitApp(t)
	a.autoFitSidebar() // settle at the fitted width first

	// Press on the splitter, then a motion at the same column: nothing
	// moved, so nothing is decided.
	splitX := a.splitterX()
	a.handleMouse(tcell.NewEventMouse(splitX, 5, tcell.Button1, tcell.ModNone))
	a.handleMouse(tcell.NewEventMouse(splitX, 5, tcell.Button1, tcell.ModNone))
	if !a.treeAutoFit {
		t.Fatal("a jitter-free press should not turn auto-fit off")
	}

	// Now actually drag.
	a.handleMouse(tcell.NewEventMouse(splitX-6, 5, tcell.Button1, tcell.ModNone))
	if a.treeAutoFit {
		t.Fatal("dragging the splitter should turn auto-fit off")
	}
	if a.sidebarWidth != splitX-5 {
		t.Fatalf("sidebarWidth = %d, want the dragged %d", a.sidebarWidth, splitX-5)
	}
	cfg, err := userconfig.Load(userconfig.DefaultPath())
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.TreeAutoFit {
		t.Fatal("the drag should have persisted treeautofit off")
	}

	// And the dragged width survives the next frame — the whole point of
	// locking it.
	a.draw()
	if a.sidebarWidth != splitX-5 {
		t.Fatalf("draw overwrote the locked width: %d", a.sidebarWidth)
	}
}

// TestMenuToggleTreeAutoFit_PersistsAndPreservesConfig drives the ≡ row
// end to end: the flag flips, the choice lands in config.json, and keys
// the toggle doesn't own survive the rewrite.
func TestMenuToggleTreeAutoFit_PersistsAndPreservesConfig(t *testing.T) {
	cfgDir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", cfgDir)
	cfgPath := filepath.Join(cfgDir, "ced", "config.json")
	if err := os.MkdirAll(filepath.Dir(cfgPath), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(cfgPath, []byte(`{"icons":"off"}`+"\n"), 0o644); err != nil {
		t.Fatalf("seed config: %v", err)
	}
	a := newTestApp(t, t.TempDir())
	a.treeAutoFit = true

	a.menuToggleTreeAutoFit()
	if a.treeAutoFit {
		t.Fatal("toggle should flip auto-fit off")
	}
	cfg, err := userconfig.Load(cfgPath)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.TreeAutoFit {
		t.Fatal("config.json should carry treeautofit off")
	}
	if cfg.Icons != userconfig.IconsOff {
		t.Fatalf("hand-set icons key lost: %q", cfg.Icons)
	}

	a.menuToggleTreeAutoFit()
	if !a.treeAutoFit {
		t.Fatal("second toggle should flip auto-fit back on")
	}
}

// TestTreeAutoFitToggleLabel names the ACTION, not the state — the same
// toggle-in-place rule the rest of the ≡ View rows follow.
func TestTreeAutoFitToggleLabel(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	a.treeAutoFit = true
	if got := a.treeAutoFitToggleLabel(); got != "Lock file tree width" {
		t.Fatalf("label with auto-fit on = %q", got)
	}
	a.treeAutoFit = false
	if got := a.treeAutoFitToggleLabel(); got != "Auto-fit file tree width" {
		t.Fatalf("label with auto-fit off = %q", got)
	}
}

// TestLoadUserConfig_AppliesTreeAutoFit pins the startup path: config.json
// is what decides, so a user who locked their width keeps it across
// sessions.
func TestLoadUserConfig_AppliesTreeAutoFit(t *testing.T) {
	cfgDir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", cfgDir)
	cfgPath := filepath.Join(cfgDir, "ced", "config.json")
	if err := os.MkdirAll(filepath.Dir(cfgPath), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(cfgPath, []byte(`{"treeautofit":"off"}`+"\n"), 0o644); err != nil {
		t.Fatalf("seed config: %v", err)
	}
	a := newTestApp(t, t.TempDir())
	a.treeAutoFit = true
	a.loadUserConfig()
	if a.treeAutoFit {
		t.Fatal("loadUserConfig should apply treeautofit:off")
	}

	// And the absent key keeps the shipped default on — the state the
	// product actually ships in.
	if err := os.WriteFile(cfgPath, []byte(`{}`+"\n"), 0o644); err != nil {
		t.Fatalf("reseed config: %v", err)
	}
	a.loadUserConfig()
	if !a.treeAutoFit {
		t.Fatal("an absent treeautofit key should leave auto-fit on")
	}
}
