// =============================================================================
// File: internal/favorites/favorites_test.go
// Author: Rohan Allison <rohanthewiz@gmail.com>
// Created: 2026-09-10
// Copyright: 2026 Rohan Allison. All rights reserved.
// =============================================================================

package favorites

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// seedProject builds the layout the examples assume — a project root
// with ai_docs/plans inside it and a directory buried a couple of levels
// down — and returns both, because resolving from a subdirectory is the
// case the walk exists for.
func seedProject(t *testing.T) (root, deep string) {
	t.Helper()
	root = t.TempDir()
	deep = filepath.Join(root, "internal", "app")
	for _, d := range []string{filepath.Join(root, "ai_docs", "plans"), deep} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	return root, deep
}

// TestLoad_MissingFileIsNotAnError pins the contract that keeps the
// feature invisible to everyone who doesn't use it: most users have no
// favorites, so the absent file is the common case and must not be
// something every caller has to handle.
func TestLoad_MissingFileIsNotAnError(t *testing.T) {
	set, err := Load(filepath.Join(t.TempDir(), "favorites.json"))
	if err != nil {
		t.Fatalf("Load on a missing file = %v, want nil", err)
	}
	if len(set.List("")) != 0 {
		t.Fatal("a missing file should parse to an empty set")
	}
	// So is no config location at all — the feature is unavailable, not
	// broken.
	if _, err := Load(""); err != nil {
		t.Fatalf(`Load("") = %v, want nil`, err)
	}
}

// TestLoad_MalformedFileIsReported is the other half. A file somebody
// wrote and ced silently ignored is worse than a message: they would go
// on believing the name was bound.
func TestLoad_MalformedFileIsReported(t *testing.T) {
	path := filepath.Join(t.TempDir(), "favorites.json")
	if err := os.WriteFile(path, []byte("{{{"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(path); err == nil {
		t.Fatal("a malformed favorites file should be reported")
	}

	// An empty file is not malformed — `{}` (or nothing) is a
	// legitimate thing to leave behind after removing your last entry.
	if err := os.WriteFile(path, []byte("  \n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(path); err != nil {
		t.Fatalf("an empty file should parse cleanly, got %v", err)
	}
}

// TestSaveLoad_RoundTripsBothScopes pins the file format end to end,
// including the sparse-map rule: a two-entry file must come back as a
// two-entry file rather than gaining empty scaffolding.
func TestSaveLoad_RoundTripsBothScopes(t *testing.T) {
	root, _ := seedProject(t)
	path := filepath.Join(t.TempDir(), "sub", "favorites.json")

	set := &Set{}
	if _, err := set.Add("", "plans", "ai_docs/plans"); err != nil {
		t.Fatal(err)
	}
	if _, err := set.Add(root, "plans", "docs/plans"); err != nil {
		t.Fatal(err)
	}
	// Save creates the config directory: `ced fav add` is often the
	// first thing that ever writes to ~/.config/ced.
	if err := set.Save(path); err != nil {
		t.Fatalf("Save: %v", err)
	}

	back, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if rel, scope, ok := back.Lookup(root, "plans"); !ok || rel != "docs/plans" || scope != ScopeProject {
		t.Fatalf("project lookup = %q/%q/%v", rel, scope, ok)
	}
	if rel, scope, ok := back.Lookup("", "plans"); !ok || rel != "ai_docs/plans" || scope != ScopeGlobal {
		t.Fatalf("global lookup = %q/%q/%v", rel, scope, ok)
	}
}

// TestClean_RefusesAnythingThatLeavesTheProject is the confinement rule
// at its cheapest point. A favorite is a project-RELATIVE location; the
// one thing this feature must not become is a way for a config file to
// point the editor at /etc.
func TestClean_RefusesAnythingThatLeavesTheProject(t *testing.T) {
	for _, bad := range []string{"", "   ", ".", "..", "../etc", "../../etc/passwd", "a/../.."} {
		if got, err := Clean(bad); err == nil {
			t.Errorf("Clean(%q) = %q, want an error", bad, got)
		}
	}
	abs := string(filepath.Separator) + "etc"
	if _, err := Clean(abs); err == nil {
		t.Errorf("Clean(%q) should refuse an absolute path", abs)
	}

	// And normalises the spellings people actually type. Forward slashes
	// are accepted whatever the host separator is: these files get
	// copied between machines.
	for _, c := range []struct{ in, want string }{
		{"ai_docs/plans", filepath.Join("ai_docs", "plans")},
		{"./ai_docs/plans", filepath.Join("ai_docs", "plans")},
		{"  ai_docs/plans/  ", filepath.Join("ai_docs", "plans")},
		{"a/b/../plans", filepath.Join("a", "plans")},
	} {
		got, err := Clean(c.in)
		if err != nil || got != c.want {
			t.Errorf("Clean(%q) = %q, %v; want %q", c.in, got, err, c.want)
		}
	}
}

// TestCleanName_RefusesPathsAndFlags pins the two shapes a name may not
// take. A name with a separator would read as a path in `ced fav <name>`
// and make a typo's error message actively misleading; one starting with
// a dash is a flag as far as any command line is concerned.
func TestCleanName_RefusesPathsAndFlags(t *testing.T) {
	for _, bad := range []string{"", "  ", "ai_docs/plans", `a\b`, "-p"} {
		if _, err := CleanName(bad); err == nil {
			t.Errorf("CleanName(%q) should be refused", bad)
		}
	}
	if got, err := CleanName("  plans "); err != nil || got != "plans" {
		t.Fatalf("CleanName trimmed to %q, %v", got, err)
	}
}

// TestResolve_WalksUpFromASubdirectory is the feature, not a fallback:
// `ced fav plans` is typed from wherever the user is standing, which for
// a project of any size is not the project root.
func TestResolve_WalksUpFromASubdirectory(t *testing.T) {
	root, deep := seedProject(t)
	set := &Set{}
	set.Add("", "plans", "ai_docs/plans")

	got, err := set.Resolve(deep, "plans")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	// The directory that OWNS the favorite is the project — that is what
	// the walk decides, and it is what the editor roots at.
	if got.Root != root {
		t.Fatalf("Root = %q, want %q", got.Root, root)
	}
	if want := filepath.Join(root, "ai_docs", "plans"); got.Abs != want {
		t.Fatalf("Abs = %q, want %q", got.Abs, want)
	}
	if got.Scope != ScopeGlobal {
		t.Fatalf("Scope = %q, want global", got.Scope)
	}
}

// TestResolve_ProjectOverrideIsReachableFromASubdirectory pins the
// subtlety that makes two scopes work at all. The override is keyed by
// the root that owns it, so it is only found if the name is looked up
// per CANDIDATE as the walk climbs — a single lookup against the
// starting directory would make an override invisible from every
// subdirectory of its own project.
func TestResolve_ProjectOverrideIsReachableFromASubdirectory(t *testing.T) {
	root, deep := seedProject(t)
	if err := os.MkdirAll(filepath.Join(root, "docs", "plans"), 0o755); err != nil {
		t.Fatal(err)
	}
	set := &Set{}
	set.Add("", "plans", "ai_docs/plans")
	set.Add(root, "plans", "docs/plans")

	got, err := set.Resolve(deep, "plans")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if want := filepath.Join(root, "docs", "plans"); got.Abs != want {
		t.Fatalf("Abs = %q, want the project's %q", got.Abs, want)
	}
	if got.Scope != ScopeProject {
		t.Fatalf("Scope = %q, want project", got.Scope)
	}
}

// TestResolve_UnboundAndMissingAreDifferentAnswers separates the two
// failures, because they have different fixes: an unbound name is a typo
// or a forgotten word, while a bound name whose directory isn't there is
// a project that doesn't follow the convention. The second must name the
// path so there is something to go and check.
func TestResolve_UnboundAndMissingAreDifferentAnswers(t *testing.T) {
	_, deep := seedProject(t)
	set := &Set{}
	set.Add("", "cosess", "ai_docs/copilot_sessions")

	if _, err := set.Resolve(deep, "nope"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("unbound name gave %v, want ErrNotFound", err)
	}
	_, err := set.Resolve(deep, "cosess")
	if err == nil {
		t.Fatal("a bound name with no directory should be an error")
	}
	if errors.Is(err, ErrNotFound) {
		t.Fatal("bound-but-missing must not report as unbound")
	}
	if got := err.Error(); !strings.Contains(got, "copilot_sessions") {
		t.Fatalf("error should name the path it looked for: %s", got)
	}
}

// TestResolve_HandEditedBadPathIsNamed covers the entry Add would have
// refused but a text editor will happily write. The user is looking at
// the file, so "invalid" alone would not tell them which line.
func TestResolve_HandEditedBadPathIsNamed(t *testing.T) {
	_, deep := seedProject(t)
	set := &Set{Favorites: map[string]string{"escape": "../../../etc"}}

	_, err := set.Resolve(deep, "escape")
	if err == nil {
		t.Fatal("a hand-written escaping path should be refused at resolve time")
	}
	if got := err.Error(); !strings.Contains(got, "escape") || !strings.Contains(got, "climbs outside") {
		t.Fatalf("error should name the entry and the rule: %s", got)
	}
}

// TestList_MergesScopesWithoutDuplicatingNames pins the theme registry's
// shadowing rule applied here: a shadowed global appears ONCE, wearing
// the project scope, because two rows with one name in a listing is a
// bug report — and the value the user needs to see is the one that will
// actually be used.
func TestList_MergesScopesWithoutDuplicatingNames(t *testing.T) {
	root, _ := seedProject(t)
	set := &Set{}
	set.Add("", "plans", "ai_docs/plans")
	set.Add("", "clsess", "ai_docs/claude_sessions")
	set.Add(root, "plans", "docs/plans")

	got := set.List(root)
	if len(got) != 2 {
		t.Fatalf("List returned %d entries, want 2: %+v", len(got), got)
	}
	// Sorted, so two runs of `ced fav` print the same thing.
	if got[0].Name != "clsess" || got[1].Name != "plans" {
		t.Fatalf("List is not sorted by name: %+v", got)
	}
	if got[1].Path != "docs/plans" || got[1].Scope != ScopeProject {
		t.Fatalf("the project override should win in a listing: %+v", got[1])
	}
	// From outside that project, the global value is what shows.
	if outside := set.List(t.TempDir()); outside[1].Path != "ai_docs/plans" {
		t.Fatalf("outside the project, plans = %q, want the global", outside[1].Path)
	}
}

// TestRemove_ProjectFirstThenGlobal pins the default scope of a removal.
// An override is the more specific statement, so it is what `ced fav rm
// plans` typed inside that project almost certainly means — and the
// caller is told which one went, because quietly deleting a default
// shared by every project is a loss with nothing on screen to explain it.
func TestRemove_ProjectFirstThenGlobal(t *testing.T) {
	root, _ := seedProject(t)
	set := &Set{}
	set.Add("", "plans", "ai_docs/plans")
	set.Add(root, "plans", "docs/plans")

	if scope, err := set.Remove(root, "", "plans"); err != nil || scope != ScopeProject {
		t.Fatalf("first Remove = %q, %v; want project", scope, err)
	}
	// The emptied project block is deleted rather than left as `{}` — a
	// file that accumulates empty entries for every project a user ever
	// tried a favorite in is a file nobody can read.
	if _, ok := set.Projects[root]; ok && len(set.Projects[root]) == 0 {
		t.Fatal("an emptied project block should be dropped, not left behind")
	}
	if scope, err := set.Remove(root, "", "plans"); err != nil || scope != ScopeGlobal {
		t.Fatalf("second Remove = %q, %v; want global", scope, err)
	}
	if _, err := set.Remove(root, "", "plans"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("third Remove = %v, want ErrNotFound", err)
	}
}

// TestRemove_ExplicitScopeDoesNotFallThrough is the other half: asking
// for the global entry must never take the project's override instead.
func TestRemove_ExplicitScopeDoesNotFallThrough(t *testing.T) {
	root, _ := seedProject(t)
	set := &Set{}
	set.Add(root, "plans", "docs/plans")

	if _, err := set.Remove(root, ScopeGlobal, "plans"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("--global should not remove a project override, got %v", err)
	}
	if _, _, ok := set.Lookup(root, "plans"); !ok {
		t.Fatal("the override was removed by a --global request")
	}
}

// TestAdd_RefusesWhatWouldNeverResolve keeps an unusable entry out of
// the file entirely. The alternative is a favorites.json that parses,
// lists, and then fails at the one moment the user wanted it.
func TestAdd_RefusesWhatWouldNeverResolve(t *testing.T) {
	set := &Set{}
	if _, err := set.Add("", "plans", "../escape"); err == nil {
		t.Error("Add should refuse an escaping path")
	}
	if _, err := set.Add("", "a/b", "ai_docs/plans"); err == nil {
		t.Error("Add should refuse a name that looks like a path")
	}
	if len(set.Favorites) != 0 {
		t.Fatalf("a refused Add wrote something: %+v", set.Favorites)
	}
}
