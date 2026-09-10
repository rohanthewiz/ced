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

// -----------------------------------------------------------------------------
// The ≡ Navigation row and its picker
// -----------------------------------------------------------------------------

// seedFavorites writes a favorites.json into the throwaway config
// directory newTestApp already pinned, so a test can state what is bound
// without going near the developer's real file.
func seedFavorites(t *testing.T, a *App, body string) {
	t.Helper()
	if err := os.WriteFile(favoritesPathFn(), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

// pickerRows is the picker's labels, joined — the shape every assertion
// below wants.
func pickerRows(t *testing.T, a *App) []string {
	t.Helper()
	pm, ok := a.modal.(*paletteModal)
	if !ok {
		t.Fatalf("modal = %T, want a picker", a.modal)
	}
	rows := make([]string, 0, len(pm.items))
	for _, it := range pm.items {
		rows = append(rows, it.label)
	}
	return rows
}

// TestMenuGoToFavorite_ListsAndReveals is the row's happy path: the
// bound favorites that exist here become rows, and running one lands the
// tree cursor on the folder — the same gesture `ced fav plans` performs,
// mid-session.
func TestMenuGoToFavorite_ListsAndReveals(t *testing.T) {
	root, folder, _ := revealProject(t)
	a := newTestApp(t, root)
	seedFavorites(t, a, `{"favorites":{"plans":"ai_docs/plans"}}`)

	a.menuGoToFavorite()
	rows := pickerRows(t, a)
	if len(rows) != 1 {
		t.Fatalf("picker rows = %v, want one", rows)
	}
	// Name first, path trailing: the fuzzy scorer rewards early matches
	// and the name is what the user types (the symbol picker's rule).
	if !strings.HasPrefix(rows[0], "plans") || !strings.Contains(rows[0], "ai_docs/plans") {
		t.Fatalf("row = %q, want the name first and the path as context", rows[0])
	}

	pm := a.modal.(*paletteModal)
	pm.items[0].run(a)
	if a.tree.Selected == nil || a.tree.Selected.Path != folder {
		t.Fatalf("tree cursor = %+v, want %q", a.tree.Selected, folder)
	}
	if a.rootDir != root {
		t.Fatalf("rootDir = %q, want %q — the picker must not re-root", a.rootDir, root)
	}
}

// TestMenuGoToFavorite_OffersOnlyWhatResolvesHere pins the difference
// between this picker and the CLI's `fav list`. That one is a REPORT, so
// it shows a global default this project doesn't follow, marked "missing
// here". A picker is a list of VERBS and the palette has no disabled
// state to borrow, so a row answering Enter with "that isn't here" is
// worse than one never offered (the code-actions rule).
func TestMenuGoToFavorite_OffersOnlyWhatResolvesHere(t *testing.T) {
	root, _, _ := revealProject(t)
	a := newTestApp(t, root)
	seedFavorites(t, a, `{"favorites":{
		"plans":"ai_docs/plans",
		"cosess":"ai_docs/copilot_sessions"
	}}`)

	a.menuGoToFavorite()
	rows := pickerRows(t, a)
	joined := strings.Join(rows, "|")
	if !strings.Contains(joined, "plans") {
		t.Fatalf("the resolvable favorite is missing: %q", joined)
	}
	if strings.Contains(joined, "cosess") {
		t.Fatalf("a favorite with no directory here was offered: %q", joined)
	}
}

// TestMenuGoToFavorite_ResolvesStrictlyInTheOpenRoot is the one place
// the editor deliberately differs from the CLI. `ced fav` walks UP
// because it is still choosing which project to open; a running editor
// already has one, and a walk here would resolve a favorite in the
// PARENT of the workspace — a path outside the file tree, which the tree
// would then refuse to reveal, having been handed somewhere the user
// cannot see.
func TestMenuGoToFavorite_ResolvesStrictlyInTheOpenRoot(t *testing.T) {
	parent := t.TempDir()
	// The favorite exists in the PARENT of the workspace, not in it.
	if err := os.MkdirAll(filepath.Join(parent, "ai_docs", "plans"), 0o755); err != nil {
		t.Fatal(err)
	}
	root := filepath.Join(parent, "child")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}

	a := newTestApp(t, root)
	seedFavorites(t, a, `{"favorites":{"plans":"ai_docs/plans"}}`)

	a.menuGoToFavorite()
	if _, ok := a.modal.(*paletteModal); ok {
		t.Fatalf("a favorite outside the open root was offered: %v", pickerRows(t, a))
	}
	if !strings.Contains(a.statusMsg, "exist in this project") {
		t.Fatalf("statusMsg = %q, want it to say the favorites aren't here", a.statusMsg)
	}
}

// TestMenuGoToFavorite_EmptyStatesTeachRatherThanDim covers both ways
// the picker comes up with nothing, and why the row is never dimmed. A
// dimmed row on a machine with no favorites.json is a dead end that
// cannot explain itself; these two messages have different fixes, so
// they are different messages (the CLI's unbound / bound-but-missing
// split, one floor up).
func TestMenuGoToFavorite_EmptyStatesTeachRatherThanDim(t *testing.T) {
	root, _, _ := revealProject(t)

	// Nothing bound at all: name the verb that binds one.
	a := newTestApp(t, root)
	a.menuGoToFavorite()
	if a.modal != nil {
		t.Fatalf("modal = %T, want no picker", a.modal)
	}
	if !strings.Contains(a.statusMsg, "ced fav add") {
		t.Fatalf("statusMsg = %q, want it to teach the add verb", a.statusMsg)
	}

	// Bound, but none of them here: say THAT instead, so the user
	// doesn't go hunting for a file they know they wrote.
	b := newTestApp(t, root)
	seedFavorites(t, b, `{"favorites":{"cosess":"ai_docs/copilot_sessions"}}`)
	b.menuGoToFavorite()
	if b.modal != nil {
		t.Fatalf("modal = %T, want no picker", b.modal)
	}
	if !strings.Contains(b.statusMsg, "exist in this project") {
		t.Fatalf("statusMsg = %q, want the not-here message", b.statusMsg)
	}
}

// TestMenuGoToFavorite_MalformedFileIsReported keeps the file's one
// error path honest. The user wrote favorites.json, so reading a syntax
// error as "you have no favorites" would leave them believing the names
// were bound.
func TestMenuGoToFavorite_MalformedFileIsReported(t *testing.T) {
	root, _, _ := revealProject(t)
	a := newTestApp(t, root)
	seedFavorites(t, a, `{{{`)

	a.menuGoToFavorite()
	if a.modal != nil {
		t.Fatalf("modal = %T, want no picker", a.modal)
	}
	if !strings.Contains(a.statusMsg, "Favorites:") {
		t.Fatalf("statusMsg = %q, want the parse error reported", a.statusMsg)
	}
}

// TestMenuGoToFavorite_ProjectOverrideWins pins that the picker reads
// the same two scopes the CLI does — the override is what a user who set
// one expects to see, and offering the global path instead would reveal
// the wrong folder.
func TestMenuGoToFavorite_ProjectOverrideWins(t *testing.T) {
	root, _, _ := revealProject(t)
	other := filepath.Join(root, "docs", "plans")
	if err := os.MkdirAll(other, 0o755); err != nil {
		t.Fatal(err)
	}
	a := newTestApp(t, root)
	seedFavorites(t, a, `{
		"favorites":{"plans":"ai_docs/plans"},
		"projects":{"`+root+`":{"plans":"docs/plans"}}
	}`)

	a.menuGoToFavorite()
	rows := pickerRows(t, a)
	if len(rows) != 1 || !strings.Contains(rows[0], "docs/plans") {
		t.Fatalf("rows = %v, want the project override", rows)
	}
	a.modal.(*paletteModal).items[0].run(a)
	if a.tree.Selected == nil || a.tree.Selected.Path != other {
		t.Fatalf("revealed %+v, want the override's %q", a.tree.Selected, other)
	}
}
