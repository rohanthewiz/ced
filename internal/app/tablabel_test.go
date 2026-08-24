// =============================================================================
// File: internal/app/tablabel_test.go
// Author: Rohan Allison <rohanthewiz@gmail.com>
// Created: 2026-08-24
// Copyright: 2026 Rohan Allison. All rights reserved.
// =============================================================================

// Tests for the tab strip's disambiguating labels: when a directory is
// added, when it isn't, how far the growth goes, and the path-list cache
// that stands in for an invalidation flag.

package app

import (
	"os"
	"path/filepath"
	"testing"
)

// openTabsAt opens each of rel under a fresh root and returns the app,
// so a test can state a tab set as a list of project-relative paths.
func openTabsAt(t *testing.T, rel ...string) *App {
	t.Helper()
	root := t.TempDir()
	a := newTestApp(t, root)
	for _, r := range rel {
		p := filepath.Join(root, filepath.FromSlash(r))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte("x\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		a.openFile(p)
	}
	return a
}

// tabLabelsOf reads every open tab's drawn label.
func tabLabelsOf(a *App) []string {
	out := make([]string, len(a.tabs))
	for i := range a.tabs {
		out[i] = a.tabLabel(i)
	}
	return out
}

// TestTabLabels_UniqueNamesStayBare is the common case and the reason the
// growth is per colliding group: a strip of distinct names must cost no
// directory columns at all, since the strip is the editor's scarcest
// horizontal space.
func TestTabLabels_UniqueNamesStayBare(t *testing.T) {
	a := openTabsAt(t, "main.go", "internal/app/tabbar.go", "internal/editor/tab.go")
	want := []string{"main.go", "tabbar.go", "tab.go"}
	for i, w := range want {
		if got := a.tabLabel(i); got != w {
			t.Errorf("tab %d label = %q, want %q", i, got, w)
		}
	}
}

// TestTabLabels_SameNameGainsOneDirectory is the feature: two files whose
// basenames collide are drawn with the directory that tells them apart,
// and only those two.
func TestTabLabels_SameNameGainsOneDirectory(t *testing.T) {
	a := openTabsAt(t, "cmd/main.go", "web/main.go", "internal/util.go")
	got := tabLabelsOf(a)
	want := []string{"cmd/main.go", "web/main.go", "util.go"}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("labels = %v, want %v", got, want)
		}
	}
}

// TestTabLabels_GrowsUntilDistinct covers the tie that one directory
// can't break: the loop must take another segment rather than settle for
// two identical labels, which is the whole failure it exists to prevent.
func TestTabLabels_GrowsUntilDistinct(t *testing.T) {
	a := openTabsAt(t, "a/x/dup.go", "b/x/dup.go")
	got := tabLabelsOf(a)
	want := []string{"a/x/dup.go", "b/x/dup.go"}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("labels = %v, want %v", got, want)
		}
	}
}

// TestTabLabels_PartialCollisionLeavesTheThirdAlone pins the re-grouping
// rule: once cmd/main.go and web/main.go are distinct, a third file that
// was never ambiguous must not have been widened along with them.
func TestTabLabels_PartialCollisionLeavesTheThirdAlone(t *testing.T) {
	a := openTabsAt(t, "cmd/main.go", "web/main.go", "cmd/serve.go")
	if got := a.tabLabel(2); got != "serve.go" {
		t.Errorf("uncontested tab label = %q, want %q", got, "serve.go")
	}
}

// TestDisambiguateTabLabels_SaturatesWithoutSpinning is the termination
// proof for the pathological inputs — two unsaved buffers have no
// directories to take, so the loop must give up rather than hang.
func TestDisambiguateTabLabels_SaturatesWithoutSpinning(t *testing.T) {
	got := disambiguateTabLabels([]string{"", ""}, []string{"untitled", "untitled"})
	if len(got) != 2 || got[0] != "untitled" || got[1] != "untitled" {
		t.Fatalf("labels = %v, want two bare untitled", got)
	}
}

// TestTabLabels_CacheFollowsTheOpenPaths is the invalidation contract:
// the cache is keyed by the list of open paths, so closing one of a
// colliding pair must narrow the survivor back to its basename with no
// mutation site having to say so.
func TestTabLabels_CacheFollowsTheOpenPaths(t *testing.T) {
	a := openTabsAt(t, "cmd/main.go", "web/main.go")
	if got := a.tabLabel(0); got != "cmd/main.go" {
		t.Fatalf("pre-close label = %q, want cmd/main.go", got)
	}
	a.tabs = a.tabs[:1] // the close path, minus its unrelated bookkeeping.
	a.activeTab = 0
	if got := a.tabLabel(0); got != "main.go" {
		t.Errorf("post-close label = %q, want main.go", got)
	}
}

// TestTabWidth_CountsTheDisambiguatedLabel is why tabWidth measures the
// label: a width taken from the basename would clip exactly the directory
// that carries the information.
func TestTabWidth_CountsTheDisambiguatedLabel(t *testing.T) {
	a := openTabsAt(t, "cmd/main.go", "web/main.go")
	bare := openTabsAt(t, "cmd/main.go")
	if a.tabWidth(0) <= bare.tabWidth(0) {
		t.Errorf("disambiguated tab width %d not wider than bare %d",
			a.tabWidth(0), bare.tabWidth(0))
	}
	if want := bare.tabWidth(0) + len("cmd/"); a.tabWidth(0) != want {
		t.Errorf("tabWidth = %d, want %d", a.tabWidth(0), want)
	}
}

// TestPathSegments_DropsRootAndDot guards the segment splitter against
// the two inputs that would otherwise contribute an empty directory
// component to a label.
func TestPathSegments_DropsRootAndDot(t *testing.T) {
	if got := pathSegments("/main.go"); len(got) != 0 {
		t.Errorf("root-level segments = %v, want none", got)
	}
	if got := pathSegments("main.go"); len(got) != 0 {
		t.Errorf("bare-name segments = %v, want none", got)
	}
	got := pathSegments("/a/b/c.go")
	if len(got) != 2 || got[0] != "a" || got[1] != "b" {
		t.Errorf("segments = %v, want [a b]", got)
	}
}
