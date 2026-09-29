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
	"github.com/rohanthewiz/ced/internal/session"
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

// TestWriteHistory_OnlyInARepository pins N-027 end to end: with the real
// persistence gate, an App rooted at a bare folder writes nothing — no
// database, no .ced/ — and the same history reaches disk once the root
// becomes a repository (the gate is asked at write time, so a `git init`
// mid-session counts).
func TestWriteHistory_OnlyInARepository(t *testing.T) {
	root := t.TempDir()
	if history.Persists(root) {
		t.Skipf("temp dir %s is inside a git work tree; cannot stage a non-repository", root)
	}
	sub := mkdirs(t, root, "pkg")[0]
	a := newTestApp(t, root)
	historyPersistsFn = history.Persists // newTestApp's Cleanup restores it
	useFolder(a, sub, 1)

	a.writeHistory()
	if _, err := os.Stat(historyPathFn(root)); !os.IsNotExist(err) {
		t.Fatalf("a bare folder's history reached disk: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, ".ced")); !os.IsNotExist(err) {
		t.Fatalf("a bare folder grew a .ced/: %v", err)
	}
	if !a.repoHistory().Folders.HasUsesBelow(root) {
		t.Fatal("the session's history was lost along with the write")
	}

	if err := os.Mkdir(filepath.Join(root, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	a.writeHistory()
	back, err := history.Load(root, historyPathFn(root))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if !back.Folders.HasUsesBelow(root) {
		t.Fatal("once a repository, the pending history never reached the database")
	}
}

// readOnlyHistory points the history database into a directory that
// cannot take a .ced/ — the read-only checkout — for one test. newTestApp
// has already pinned historyPersistsFn to "yes".
func readOnlyHistory(t *testing.T) string {
	t.Helper()
	if os.Geteuid() == 0 {
		t.Skip("root ignores directory permissions")
	}
	ro := t.TempDir()
	if err := os.Chmod(ro, 0o555); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(ro, 0o755) })
	prev := historyPathFn
	historyPathFn = func(string) string { return filepath.Join(ro, ".ced", history.DBName) }
	t.Cleanup(func() { historyPathFn = prev })
	return ro
}

// TestHistoryNoSave_WritableSaysNothing pins the ordinary case: a
// writable history leaves both ≡ Nav labels bare and the rows gated on
// having something to list, exactly as before N-028.
func TestHistoryNoSave_WritableSaysNothing(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	if got := a.recentLocationsLabel(); got != "Recent locations…" {
		t.Fatalf("label = %q, want it bare", got)
	}
	if got := a.recentFilesLabel(); got != "Recent files…" {
		t.Fatalf("label = %q, want it bare", got)
	}
	if a.hasRecentLocationsRow() || a.hasRecentFilesRow() {
		t.Fatal("with no history and no problem both rows should be dimmed")
	}
	if msg := a.historyNoSaveMsg(); msg != "" {
		t.Fatalf("msg = %q, want none", msg)
	}
}

// TestHistoryNoSave_ReadOnlyCheckout is N-028: history that cannot reach
// disk says so on both ≡ Nav rows, the rows stay clickable even with
// nothing to list, and clicking one flashes the reason — naming .ced/
// and the OS cause — instead of the "fills in as you open files" line,
// which would be a promise the session cannot keep.
func TestHistoryNoSave_ReadOnlyCheckout(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	readOnlyHistory(t)

	if got := a.recentLocationsLabel(); got != "Recent locations… (not saved)" {
		t.Fatalf("label = %q", got)
	}
	if got := a.recentFilesLabel(); got != "Recent files… (not saved)" {
		t.Fatalf("label = %q", got)
	}
	if !a.hasRecentLocationsRow() || !a.hasRecentFilesRow() {
		t.Fatal("rows must stay clickable to explain themselves")
	}

	a.menuRecentLocations()
	if a.modal != nil {
		t.Fatalf("modal = %T, want only a flash with nothing to list", a.modal)
	}
	for _, want := range []string{"History won't be saved", "can't create", ".ced", "permission denied"} {
		if !strings.Contains(a.statusMsg, want) {
			t.Fatalf("status = %q, want it to contain %q", a.statusMsg, want)
		}
	}
	a.statusMsg = ""
	a.menuRecentFiles()
	if !strings.HasPrefix(a.statusMsg, "History won't be saved") {
		t.Fatalf("status = %q, want the reason from Recent files too", a.statusMsg)
	}
}

// TestHistoryNoSave_ReasonAlsoWhenListing: with history to show, the
// picker opens as usual and the reason rides along in the status bar.
func TestHistoryNoSave_ReasonAlsoWhenListing(t *testing.T) {
	root := t.TempDir()
	a := newTestApp(t, root)
	readOnlyHistory(t)
	sub := filepath.Join(root, "pkg")
	if err := os.Mkdir(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	useFolder(a, session.Normalize(sub), 1)

	a.menuRecentLocations()
	if _, ok := a.modal.(*paletteModal); !ok {
		t.Fatalf("modal = %T, want the picker", a.modal)
	}
	if !strings.HasPrefix(a.statusMsg, "History won't be saved") {
		t.Fatalf("status = %q, want the reason alongside the picker", a.statusMsg)
	}
}

// TestHistoryNoSave_NotARepositoryIsNotAProblem: a root that is not a
// repository keeps history for the session BY DESIGN (N-027), so it is
// never labelled — even where the directory could not be written anyway.
func TestHistoryNoSave_NotARepositoryIsNotAProblem(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	readOnlyHistory(t)
	prev := historyPersistsFn
	historyPersistsFn = func(string) bool { return false }
	t.Cleanup(func() { historyPersistsFn = prev })

	if got := a.recentLocationsLabel(); got != "Recent locations…" {
		t.Fatalf("label = %q, want it bare outside a repository", got)
	}
}
