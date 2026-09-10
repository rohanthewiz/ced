// =============================================================================
// File: internal/app/favmanage_test.go
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

	"github.com/rohanthewiz/ced/internal/favorites"
	"github.com/rohanthewiz/ced/internal/filetree"
)

// manageProject seeds a project with both a global-shaped folder and a
// project-shaped one, so the two scopes can be told apart in a listing.
func manageProject(t *testing.T) (root string, a *App) {
	t.Helper()
	root = t.TempDir()
	for _, d := range []string{"ai_docs/plans", "ai_docs/claude_sessions", "docs/plans"} {
		if err := os.MkdirAll(filepath.Join(root, filepath.FromSlash(d)), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	return root, newTestApp(t, root)
}

// favFile reads back what the management verbs wrote — the assertions
// that matter are about the FILE, not about the picker that produced it.
func favFile(t *testing.T) *favorites.Set {
	t.Helper()
	set, err := favorites.Load(favoritesPathFn())
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	return set
}

// runRow finds a picker row by label prefix and fires it.
func runRow(t *testing.T, a *App, prefix string) {
	t.Helper()
	pm, ok := a.modal.(*paletteModal)
	if !ok {
		t.Fatalf("modal = %T, want a picker", a.modal)
	}
	for _, it := range pm.items {
		if strings.HasPrefix(it.label, prefix) {
			it.run(a)
			return
		}
	}
	labels := make([]string, 0, len(pm.items))
	for _, it := range pm.items {
		labels = append(labels, it.label)
	}
	t.Fatalf("no row starting %q in %v", prefix, labels)
}

// answerPrompt types a value into whatever prompt is open and confirms
// it. It delegates to lsprename_test.go's submitPrompt rather than
// calling the callback directly, so these tests go through the prompt's
// own trim and empty-submit rules — one spelling of "the user pressed
// Enter", the same argument the production code makes about its own
// single write paths.
func answerPrompt(t *testing.T, a *App, value string) {
	t.Helper()
	pm, ok := a.modal.(*promptModal)
	if !ok {
		t.Fatalf("modal = %T, want a prompt", a.modal)
	}
	submitPrompt(t, a, pm, value)
}

// TestManageFavorites_ProjectListUnderAGlobalDrillIn pins the layout the
// whole surface turns on. The global map is written once and shared by
// every project, so editing it from inside one of them is the rarer act
// and belongs a gesture deeper; the override list for the repo in front
// of you is what you maintain, so that is what the row opens on.
func TestManageFavorites_ProjectListUnderAGlobalDrillIn(t *testing.T) {
	root, a := manageProject(t)
	set := &favorites.Set{}
	set.Add("", "plans", "ai_docs/plans")
	set.Add("", "clsess", "ai_docs/claude_sessions")
	set.Add(root, "local", "docs/plans")
	if err := set.Save(favoritesPathFn()); err != nil {
		t.Fatal(err)
	}

	a.menuManageFavorites()
	rows := pickerRows(t, a)
	if len(rows) != 3 {
		t.Fatalf("rows = %v, want the drill-in, one project entry, and add", rows)
	}
	if !strings.HasPrefix(rows[0], "Global favorites…") {
		t.Fatalf("first row = %q, want the global drill-in", rows[0])
	}
	// The count on the drill-in is what tells you there is anything
	// behind it without spending a gesture to find out.
	if !strings.Contains(rows[0], "(2)") {
		t.Fatalf("drill-in should carry the global count, got %q", rows[0])
	}
	if !strings.HasPrefix(rows[1], "local") {
		t.Fatalf("second row = %q, want this project's entry", rows[1])
	}
	// Global entries are NOT repeated at the top level — that is what
	// makes the drill-in a level rather than a duplicate.
	if strings.Contains(strings.Join(rows, "|"), "clsess") {
		t.Fatalf("a global entry leaked into the project list: %v", rows)
	}

	runRow(t, a, "Global favorites…")
	global := pickerRows(t, a)
	if len(global) != 3 || !strings.HasPrefix(global[0], "clsess") {
		t.Fatalf("global list = %v, want both entries sorted plus add", global)
	}
}

// TestManageFavorites_ListsWhatDoesNotResolve is the report/verb split
// one floor down from the "Go to" picker. You cannot go to a folder that
// isn't there — but a broken entry is exactly the one you came here to
// fix, so a management list that hid it would be a list you could not use.
func TestManageFavorites_ListsWhatDoesNotResolve(t *testing.T) {
	root, a := manageProject(t)
	set := &favorites.Set{}
	set.Add(root, "gone", "docs/nowhere")
	if err := set.Save(favoritesPathFn()); err != nil {
		t.Fatal(err)
	}

	a.menuManageFavorites()
	joined := strings.Join(pickerRows(t, a), "|")
	if !strings.Contains(joined, "gone") {
		t.Fatalf("a broken entry must still be listed: %q", joined)
	}
	if !strings.Contains(joined, "missing here") {
		t.Fatalf("a broken PROJECT entry should be marked: %q", joined)
	}
}

// TestManageFavorites_GlobalRowsAreNotMarkedMissing is the other half of
// that rule. A global default this project doesn't follow is the NORMAL
// case — that is what a default across many projects means — so marking
// those would put a warning on nearly every row and teach the user to
// ignore it.
func TestManageFavorites_GlobalRowsAreNotMarkedMissing(t *testing.T) {
	_, a := manageProject(t)
	set := &favorites.Set{}
	set.Add("", "cosess", "ai_docs/copilot_sessions")
	if err := set.Save(favoritesPathFn()); err != nil {
		t.Fatal(err)
	}

	a.openManageGlobalFavorites()
	joined := strings.Join(pickerRows(t, a), "|")
	if !strings.Contains(joined, "cosess") {
		t.Fatalf("the global entry is missing: %q", joined)
	}
	if strings.Contains(joined, "missing here") {
		t.Fatalf("a global row should not be marked missing: %q", joined)
	}
}

// TestManageFavorites_AddRowSeedsItsOwnScope pins what makes the scope
// obvious without a flag: each list's add row is seeded to that list's
// scope, so where you asked decides what you get.
func TestManageFavorites_AddRowSeedsItsOwnScope(t *testing.T) {
	root, a := manageProject(t)

	a.menuManageFavorites()
	runRow(t, a, "Add a favorite…")
	answerPrompt(t, a, "docs/plans") // the path prompt comes FIRST
	answerPrompt(t, a, "here")       // then the name, defaulted from it

	if rel, sc, ok := favFile(t).Lookup(root, "here"); !ok || sc != favorites.ScopeProject || rel != "docs/plans" {
		t.Fatalf("project add wrote %q/%q/%v, want a project override", rel, sc, ok)
	}

	a.openManageGlobalFavorites()
	runRow(t, a, "Add a global favorite…")
	answerPrompt(t, a, "ai_docs/plans")
	answerPrompt(t, a, "plans")

	set := favFile(t)
	if rel, ok := set.Favorites["plans"]; !ok || rel != "ai_docs/plans" {
		t.Fatalf("global add wrote %q/%v, want the global map", rel, ok)
	}
}

// TestPromptFavoriteName_ScopeChipFlipsWhereItLands is the chip's whole
// contract: it states what OK will do, and alt+s changes it. A closure
// rather than an App field (the commit prompt's rule) — the value belongs
// to one invocation and must not be readable by the next.
func TestPromptFavoriteName_ScopeChipFlipsWhereItLands(t *testing.T) {
	root, a := manageProject(t)

	a.promptFavoriteName("ai_docs/plans", favorites.ScopeGlobal, nil)
	pm, ok := a.modal.(*promptModal)
	if !ok {
		t.Fatalf("modal = %T, want a prompt", a.modal)
	}
	if len(pm.extras) != 1 {
		t.Fatalf("extras = %d, want the scope chip", len(pm.extras))
	}
	if got := pm.extras[0].label(a); got != "[scope: global]" {
		t.Fatalf("chip = %q, want it to start global", got)
	}
	// The chord is what makes the chip reachable at all: a modal owns the
	// keyboard, so the ≡ menu is unreachable from inside a prompt.
	if pm.extras[0].key != 's' {
		t.Fatalf("chip key = %q, want 's'", pm.extras[0].key)
	}
	if !strings.Contains(pm.hint, "alt+s") {
		t.Fatalf("hint = %q, must name the chord — it is its only discovery surface", pm.hint)
	}

	pm.extras[0].run(a)
	if got := pm.extras[0].label(a); got != "[scope: project]" {
		t.Fatalf("chip after alt+s = %q, want project", got)
	}
	answerPrompt(t, a, "plans")

	if _, sc, ok := favFile(t).Lookup(root, "plans"); !ok || sc != favorites.ScopeProject {
		t.Fatalf("the flipped chip did not land in the project scope (%q, %v)", sc, ok)
	}
}

// TestCtxAddFavorite_SeedsNameFromTheClickedFolder pins the tree gesture.
// The click WAS the path answer, so asking for it again would be the
// editor forgetting what the user just pointed at — it goes straight to
// the name prompt, pre-filled with the folder's own name.
func TestCtxAddFavorite_SeedsNameFromTheClickedFolder(t *testing.T) {
	root, a := manageProject(t)
	var docs *filetree.Node
	for _, c := range a.tree.Root.Children {
		if c.Name == "ai_docs" {
			docs = c
		}
	}
	if docs == nil {
		t.Fatal("ai_docs not in the tree")
	}

	ctxAddFavorite(a, docs)
	pm, ok := a.modal.(*promptModal)
	if !ok {
		t.Fatalf("modal = %T, want the name prompt", a.modal)
	}
	if got := pm.field.String(); got != "ai_docs" {
		t.Fatalf("name seed = %q, want the folder's own name", got)
	}
	// Global by default: a favorite earns its name by repeating across
	// projects, and one chord says otherwise.
	if got := pm.extras[0].label(a); got != "[scope: global]" {
		t.Fatalf("chip = %q, want global by default", got)
	}
	answerPrompt(t, a, "docs")

	if rel, ok := favFile(t).Favorites["docs"]; !ok || rel != "ai_docs" {
		t.Fatalf("wrote %q/%v, want the clicked folder bound globally", rel, ok)
	}
	_ = root
}

// TestFavoriteActions_RenameKeepsThePathAndScope pins the edit verb that
// is easiest to get wrong: rename is remove-then-add, and the ADD has to
// land before the remove or a rejected name would lose the entry.
func TestFavoriteActions_RenameKeepsThePathAndScope(t *testing.T) {
	root, a := manageProject(t)
	set := &favorites.Set{}
	set.Add(root, "old", "docs/plans")
	if err := set.Save(favoritesPathFn()); err != nil {
		t.Fatal(err)
	}

	a.menuManageFavorites()
	runRow(t, a, "old")
	runRow(t, a, "Rename…")
	answerPrompt(t, a, "new")

	back := favFile(t)
	if rel, sc, ok := back.Lookup(root, "new"); !ok || rel != "docs/plans" || sc != favorites.ScopeProject {
		t.Fatalf("renamed entry = %q/%q/%v", rel, sc, ok)
	}
	if _, _, ok := back.Lookup(root, "old"); ok {
		t.Fatal("the old name survived the rename")
	}

	// A rejected name must leave the original alone.
	a.menuManageFavorites()
	runRow(t, a, "new")
	runRow(t, a, "Rename…")
	answerPrompt(t, a, "a/b")
	if _, _, ok := favFile(t).Lookup(root, "new"); !ok {
		t.Fatal("a refused rename destroyed the entry")
	}
}

// TestFavoriteActions_OverrideThenRemoveUncoversTheGlobal walks the
// two-scope model through the UI, including the report a removal owes:
// unbinding an override usually UNCOVERS a default rather than removing
// the name, and silence there reads as the removal having failed.
func TestFavoriteActions_OverrideThenRemoveUncoversTheGlobal(t *testing.T) {
	root, a := manageProject(t)
	set := &favorites.Set{}
	set.Add("", "plans", "ai_docs/plans")
	if err := set.Save(favoritesPathFn()); err != nil {
		t.Fatal(err)
	}

	a.openManageGlobalFavorites()
	runRow(t, a, "plans")
	runRow(t, a, "Override in this project…")
	// Pre-filled with the global path, so the common edit is a few
	// keystrokes rather than a retype.
	if got := a.modal.(*promptModal).field.String(); got != "ai_docs/plans" {
		t.Fatalf("override seed = %q, want the global path", got)
	}
	answerPrompt(t, a, "docs/plans")

	if rel, sc, ok := favFile(t).Lookup(root, "plans"); !ok || sc != favorites.ScopeProject || rel != "docs/plans" {
		t.Fatalf("override = %q/%q/%v", rel, sc, ok)
	}

	// The row now offers no second override — that case is an edit.
	a.menuManageFavorites()
	runRow(t, a, "plans")
	joined := strings.Join(pickerRows(t, a), "|")
	if strings.Contains(joined, "Override in this project") {
		t.Fatalf("a project row should not offer to override itself: %q", joined)
	}

	runRow(t, a, "Remove (project)")
	if !strings.Contains(a.statusMsg, "falls back to ai_docs/plans") {
		t.Fatalf("statusMsg = %q, want the uncovered global named", a.statusMsg)
	}
	if _, sc, ok := favFile(t).Lookup(root, "plans"); !ok || sc != favorites.ScopeGlobal {
		t.Fatalf("after removing the override, plans = %q/%v, want the global", sc, ok)
	}
}

// TestFavoriteActions_GoToOnlyWhenThePathResolves pins the picker rule
// applied to the per-entry list: a row answering Enter with "that isn't
// here" is worse than one never offered, while the EDIT verbs must stay
// — a broken entry is precisely what they exist to repair.
func TestFavoriteActions_GoToOnlyWhenThePathResolves(t *testing.T) {
	root, a := manageProject(t)
	set := &favorites.Set{}
	set.Add(root, "good", "docs/plans")
	set.Add(root, "bad", "docs/nowhere")
	if err := set.Save(favoritesPathFn()); err != nil {
		t.Fatal(err)
	}

	a.menuManageFavorites()
	runRow(t, a, "good")
	if joined := strings.Join(pickerRows(t, a), "|"); !strings.Contains(joined, "Go to") {
		t.Fatalf("a resolvable entry should offer Go to: %q", joined)
	}

	a.menuManageFavorites()
	runRow(t, a, "bad")
	joined := strings.Join(pickerRows(t, a), "|")
	if strings.Contains(joined, "Go to") {
		t.Fatalf("a broken entry must not offer Go to: %q", joined)
	}
	for _, want := range []string{"Rename…", "Change path…", "Remove"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("a broken entry lost its repair verb %q: %q", want, joined)
		}
	}
}

// TestManageFavorites_MalformedFileRefusesToWrite is the one refusal
// that protects data. Reading a syntax error as "you have no favorites"
// on a surface the user is about to WRITE to would mean saving over
// whatever the file actually held.
func TestManageFavorites_MalformedFileRefusesToWrite(t *testing.T) {
	_, a := manageProject(t)
	if err := os.WriteFile(favoritesPathFn(), []byte("{{{"), 0o644); err != nil {
		t.Fatal(err)
	}

	a.menuManageFavorites()
	if a.modal != nil {
		t.Fatalf("modal = %T, want no picker over an unreadable file", a.modal)
	}
	if !strings.Contains(a.statusMsg, "Favorites:") {
		t.Fatalf("statusMsg = %q, want the parse error", a.statusMsg)
	}
	// And the file is untouched.
	data, err := os.ReadFile(favoritesPathFn())
	if err != nil || string(data) != "{{{" {
		t.Fatalf("the malformed file was rewritten: %q, %v", data, err)
	}
}
