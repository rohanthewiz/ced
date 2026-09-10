// =============================================================================
// File: internal/app/favorites_test.go
// Author: Rohan Allison <rohanthewiz@gmail.com>
// Created: 2026-09-10
// Copyright: 2026 Rohan Allison. All rights reserved.
// =============================================================================

package app

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// revealProject seeds a project with the layout the favorites examples
// assume: a nested docs folder nobody has clicked towards, plus a file
// inside it.
func revealProject(t *testing.T) (root, folder, file string) {
	t.Helper()
	root = t.TempDir()
	folder = filepath.Join(root, "ai_docs", "plans")
	if err := os.MkdirAll(folder, 0o755); err != nil {
		t.Fatal(err)
	}
	file = filepath.Join(folder, "a.md")
	if err := os.WriteFile(file, []byte("# plan\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return root, folder, file
}

// TestRevealPath_FolderExpandsFocusesAndKeepsTheRoot is the gesture
// `ced fav plans` performs. The root assertion is the load-bearing one:
// a favorite must NOT re-root the editor — `ced ai_docs/plans` already
// does that, and pays for it by throwing away the project every other
// subsystem is derived from.
func TestRevealPath_FolderExpandsFocusesAndKeepsTheRoot(t *testing.T) {
	root, folder, _ := revealProject(t)
	a := newTestApp(t, root)

	a.RevealPath(folder)

	if a.rootDir != root {
		t.Fatalf("rootDir = %q, want %q — revealing must not re-root", a.rootDir, root)
	}
	n := a.tree.Selected
	if n == nil || n.Path != folder {
		t.Fatalf("tree cursor = %+v, want the revealed folder %q", n, folder)
	}
	// Arriving by name means "show me what is in here" — the tree's own
	// Reveal deliberately leaves the target alone, so this is the app's
	// call and worth pinning.
	if !n.Expanded {
		t.Fatal("a revealed folder should be expanded")
	}
	// The selection highlight only renders while the tree is Focused, so
	// a reveal that skipped this would leave the cursor somewhere
	// invisible: the arrows would work and nothing on screen would say so.
	if !a.treeFocus {
		t.Fatal("revealing a folder should hand the tree the keyboard")
	}
	if len(a.tabs) != 0 {
		t.Fatalf("a folder favorite opened %d tabs, want none", len(a.tabs))
	}
}

// TestRevealPath_ShowsAHiddenSidebar covers the case where the gesture
// would otherwise be invisible: a user who works with the sidebar hidden
// still asked to be shown a location.
func TestRevealPath_ShowsAHiddenSidebar(t *testing.T) {
	root, folder, _ := revealProject(t)
	a := newTestApp(t, root)
	a.sidebarShown = false

	a.RevealPath(folder)

	if !a.sidebarShown {
		t.Fatal("revealing should bring the sidebar back")
	}
}

// TestRevealPath_FileOpensATabAndLeavesTheKeyboardInTheEditor keeps a
// favorite from being a folders-only idea. For a file, "go there" means
// a tab — and the user's next keystroke belongs in the buffer, which is
// the opposite of the folder case above.
func TestRevealPath_FileOpensATabAndLeavesTheKeyboardInTheEditor(t *testing.T) {
	root, _, file := revealProject(t)
	a := newTestApp(t, root)

	a.RevealPath(file)

	if len(a.tabs) != 1 || a.tabs[0].Path != file {
		t.Fatalf("tabs = %+v, want one tab on %q", a.tabs, file)
	}
	if a.treeFocus {
		t.Fatal("opening a file should leave the keyboard in the editor")
	}
	// The sidebar still agrees with the editor about where you are.
	if a.tree.Selected == nil || a.tree.Selected.Path != file {
		t.Fatalf("tree cursor = %+v, want the opened file", a.tree.Selected)
	}
}

// TestRevealPath_MissingAndOutsideAreFlashed pins the degradation. The
// editor is already up and rooted at the right project, so a favorite
// pointing at something that has since been deleted costs the jump, not
// the session — but the user typed a name, so unlike the silent
// integrations this one says what happened.
func TestRevealPath_MissingAndOutsideAreFlashed(t *testing.T) {
	root, _, _ := revealProject(t)

	for _, c := range []struct{ name, path, want string }{
		{"missing", filepath.Join(root, "ai_docs", "gone"), "Cannot reveal"},
		{"outside the root", t.TempDir(), "not in this project"},
	} {
		t.Run(c.name, func(t *testing.T) {
			a := newTestApp(t, root)
			a.RevealPath(c.path)
			if !strings.Contains(a.statusMsg, c.want) {
				t.Fatalf("statusMsg = %q, want it to mention %q", a.statusMsg, c.want)
			}
		})
	}

	// An empty path is a no-op rather than a complaint: main only calls
	// this when the command line actually named a favorite.
	a := newTestApp(t, root)
	a.RevealPath("")
	if a.statusMsg != "" {
		t.Fatalf("an empty reveal flashed %q", a.statusMsg)
	}
}
