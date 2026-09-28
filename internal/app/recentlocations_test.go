// =============================================================================
// File: internal/app/recentlocations_test.go
// Author: Rohan Allison <rohanthewiz@gmail.com>
// Created: 2026-09-28
// Copyright: 2026 Rohan Allison. All rights reserved.
// =============================================================================

// Tests for the Recent locations picker and the App's side of the
// per-repository history. newTestApp points historyPathFn at a temp dir,
// so nothing here writes a .ced/ into the test's root.

package app

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rohanthewiz/ced/internal/history"
)

// useFolder records n uses of dir, the way n file opens there would.
func useFolder(a *App, dir string, n int) {
	for i := 0; i < n; i++ {
		a.noteFolderUse(dir)
	}
}

// TestRecentLocations_RecentThenSpacerThenFrequent pins the top level's
// shape: the repository's five most recent folders, a spacer, then the
// most frequent of the rest — relative to the root, never a folder twice.
func TestRecentLocations_RecentThenSpacerThenFrequent(t *testing.T) {
	root := t.TempDir()
	var names []string
	for i := 0; i < 8; i++ {
		names = append(names, fmt.Sprintf("r%d", i))
	}
	dirs := mkdirs(t, root, names...)
	a := newTestApp(t, root)
	useFolder(a, dirs[0], 3)
	useFolder(a, dirs[1], 2)
	for _, d := range dirs[2:] {
		useFolder(a, d, 1)
	}

	a.menuRecentLocations()
	m := paletteOf(a)
	if m == nil {
		t.Fatalf("modal = %T, want the picker", a.modal)
	}
	var got []string
	for _, mt := range m.matches {
		if mt.item.spacer {
			got = append(got, "--")
			continue
		}
		got = append(got, mt.item.label)
	}
	if want := "r7,r6,r5,r4,r3,--,r0,r1,r2"; strings.Join(got, ",") != want {
		t.Fatalf("rows = %v, want %s", got, want)
	}
}

// TestRecentLocations_DrillThenReveal pins the path through the levels: a
// folder with used subfolders drills (marked ›), the drill-in leads with
// a row revealing the folder itself, and a leaf is revealed in the tree —
// never re-rooted.
func TestRecentLocations_DrillThenReveal(t *testing.T) {
	root := t.TempDir()
	app := mkdirs(t, root, "internal/app")[0]
	internal := filepath.Dir(app)
	a := newTestApp(t, root)
	useFolder(a, internal, 1)
	useFolder(a, app, 1)

	a.menuRecentLocations()
	labels := pickerLabels(t, a)
	if len(labels) != 2 || labels[0] != filepath.Join("internal", "app") || labels[1] != "internal  ›" {
		t.Fatalf("top rows = %v, want [internal/app, internal  ›]", labels)
	}
	paletteOf(a).selected = 1
	paletteOf(a).runSelected(a)

	m := paletteOf(a)
	if m == nil || m.title != "Folders in internal" {
		t.Fatalf("drilling should open the drill-in, got %T", a.modal)
	}
	labels = pickerLabels(t, a)
	if len(labels) != 2 || labels[0] != "Reveal internal in tree" || labels[1] != "app" {
		t.Fatalf("drill rows = %v, want [Reveal internal in tree, app]", labels)
	}
	m.selected = 1
	m.runSelected(a)
	if a.quit || a.nextRoot != "" {
		t.Fatal("a location must be revealed, never re-rooted")
	}
	if !strings.Contains(a.statusMsg, "Revealed") {
		t.Fatalf("status = %q, want the reveal's flash", a.statusMsg)
	}
}

// TestNoteFolderUse_OnlyInsideTheRepo pins what counts: a folder outside
// the workspace belongs to another repository's history, and the root is
// the top of every list rather than an entry in one.
func TestNoteFolderUse_OnlyInsideTheRepo(t *testing.T) {
	root := t.TempDir()
	a := newTestApp(t, root)
	a.noteFolderUse(root)
	a.noteFolderUse(t.TempDir())
	if a.repoHistory().Folders.Len() != 0 {
		t.Fatalf("recorded %d folders, want none", a.repoHistory().Folders.Len())
	}
	if a.hasRecentLocations() {
		t.Fatal("the row should be dimmed with nothing recorded")
	}
	a.menuRecentLocations()
	if a.modal != nil || !strings.Contains(a.statusMsg, "No recent locations") {
		t.Fatalf("modal = %T status = %q, want a flash and no picker", a.modal, a.statusMsg)
	}
}

// TestRecentLocations_PrunesGoneFolders pins that a folder deleted since
// it was used is dropped from the index rather than listed.
func TestRecentLocations_PrunesGoneFolders(t *testing.T) {
	root := t.TempDir()
	dirs := mkdirs(t, root, "live", "gone")
	a := newTestApp(t, root)
	useFolder(a, dirs[0], 1)
	useFolder(a, dirs[1], 1)
	if err := os.Remove(dirs[1]); err != nil {
		t.Fatal(err)
	}

	a.menuRecentLocations()
	if labels := pickerLabels(t, a); len(labels) != 1 || labels[0] != "live" {
		t.Fatalf("rows = %v, want only live", labels)
	}
	if a.repoHistory().Folders.Len() != 1 {
		t.Fatal("the missing folder was not pruned from the index")
	}
}

// TestOpenFile_RecordsItsFolder pins the source of the data: a file
// opened in a new tab is a use of its folder, while switching back to an
// already-open tab is not.
func TestOpenFile_RecordsItsFolder(t *testing.T) {
	root := t.TempDir()
	dirs := mkdirs(t, root, "pkg", "busy")
	file := filepath.Join(dirs[0], "a.go")
	if err := os.WriteFile(file, []byte("package pkg\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	a := newTestApp(t, root)
	useFolder(a, dirs[1], 2)

	a.openFile(file)
	a.openFile(file) // already open: a switch, not a use

	idx := a.repoHistory().Folders
	if r := idx.Recent(root, 1, nil); len(r) != 1 || filepath.Base(r[0]) != "pkg" {
		t.Fatalf("most recent = %v, want pkg", r)
	}
	// busy has 2 uses. Had the switch counted, pkg would tie it at 2 and
	// win on recency; with one use it must rank second.
	if f := idx.Frequent(root, 1, nil); filepath.Base(f[0]) != "busy" {
		t.Fatalf("most frequent = %v, want busy — the tab switch was counted as a use", f)
	}
}

// TestWriteHistory_ReachesTheRepoDatabase pins the App's half of the
// persistence: what the editor recorded is in the database after the write
// Close performs, and a load — the next startup — sees it.
func TestWriteHistory_ReachesTheRepoDatabase(t *testing.T) {
	root := t.TempDir()
	sub := mkdirs(t, root, "pkg")[0]
	a := newTestApp(t, root)
	useFolder(a, sub, 1)

	a.writeHistory()

	back, err := history.Load(root, historyPathFn(root))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if !back.Folders.HasUsesBelow(root) {
		t.Fatal("the recorded folder never reached the database")
	}
}
