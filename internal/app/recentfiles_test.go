// =============================================================================
// File: internal/app/recentfiles_test.go
// Author: Rohan Allison <rohanthewiz@gmail.com>
// Created: 2026-08-13
// Copyright: 2026 Rohan Allison. All rights reserved.
// =============================================================================

package app

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rohanthewiz/ced/internal/session"
)

// recentFilesApp seeds n files in a temp root and returns an App with a
// tab open on each, in order — so tabs[n-1] is active and the ring reads
// most-recent-first from it backwards.
func recentFilesApp(t *testing.T, n int) (*App, string, []string) {
	t.Helper()
	root := t.TempDir()
	paths := make([]string, 0, n)
	for i := 0; i < n; i++ {
		p := filepath.Join(root, string(rune('a'+i))+".txt")
		if err := os.WriteFile(p, []byte("x\n"), 0o644); err != nil {
			t.Fatalf("seed: %v", err)
		}
		paths = append(paths, p)
	}
	a := newTestApp(t, root)
	for _, p := range paths {
		a.openFile(p)
	}
	return a, root, paths
}

// The gesture the feature exists for: with three files visited, the
// picker's FIRST row is the one visited before the current — so ⌘E,
// Enter is a flip back to where you just were. Everything else about the
// ring is bookkeeping in service of this row.
func TestRecentFiles_FirstRowIsThePreviousFile(t *testing.T) {
	a, _, paths := recentFilesApp(t, 3)

	a.menuRecentFiles()

	labels := pickerLabels(t, a)
	if len(labels) != 2 {
		t.Fatalf("want 2 rows (3 visited, current excluded), got %d: %v", len(labels), labels)
	}
	if want := filepath.Base(paths[1]); !strings.Contains(labels[0], want) {
		t.Fatalf("first row should be the previously visited file %q, got %q", want, labels[0])
	}
	if want := filepath.Base(paths[0]); !strings.Contains(labels[1], want) {
		t.Fatalf("second row should be the one before that (%q), got %q", want, labels[1])
	}
	// And picking it lands there.
	m := a.modal.(*paletteModal)
	m.items[0].run(a)
	if got := a.activeTabPtr().Path; got != paths[1] {
		t.Fatalf("picking the first row went to %q, want %q", got, paths[1])
	}
}

// The ring's head must be the file ON SCREEN, however the user got
// there. A tab switched to by click, by Esc-. or by the switcher all
// funnel through switchToTab, so this is the invariant that keeps the
// row above honest after any of them.
func TestRecentFiles_TabSwitchReordersTheRing(t *testing.T) {
	a, _, paths := recentFilesApp(t, 3)

	a.switchToTab(0) // back to the first file

	if got := a.recentFiles[0]; got != paths[0] {
		t.Fatalf("ring head is %q, want the file on screen %q", got, paths[0])
	}
	a.menuRecentFiles()
	labels := pickerLabels(t, a)
	if want := filepath.Base(paths[2]); !strings.Contains(labels[0], want) {
		t.Fatalf("first row should be the file we just left (%q), got %q", want, labels[0])
	}
}

// The rows the tab switcher cannot show are the point of the list: a
// closed file stays offered, and picking it opens it again.
func TestRecentFiles_KeepsClosedFiles(t *testing.T) {
	a, _, paths := recentFilesApp(t, 2)
	a.closeTab(1) // close the file we are looking at
	if len(a.tabs) != 1 {
		t.Fatalf("precondition: %d tabs left, want 1", len(a.tabs))
	}

	a.menuRecentFiles()

	labels := pickerLabels(t, a)
	if len(labels) != 1 {
		t.Fatalf("want the closed file offered, got %v", labels)
	}
	if want := filepath.Base(paths[1]); !strings.Contains(labels[0], want) {
		t.Fatalf("row should be the closed file %q, got %q", want, labels[0])
	}
	m := a.modal.(*paletteModal)
	m.items[0].run(a)
	if got := a.activeTabPtr().Path; got != paths[1] {
		t.Fatalf("picking a closed file opened %q, want %q", got, paths[1])
	}
	if len(a.tabs) != 2 {
		t.Fatalf("picking a closed file should open a tab, have %d", len(a.tabs))
	}
}

// Closing tabs must NOT touch the ring. Quit closes every tab in turn
// right before the session is written, so a closeTab hook would persist
// the reverse close order as though the user had visited each one — the
// list would come back wrong on the next launch, which is the failure
// this test exists to prevent regressing.
func TestRecentFiles_ClosingTabsDoesNotReorderTheRing(t *testing.T) {
	a, _, paths := recentFilesApp(t, 3)
	before := append([]string(nil), a.recentFiles...)

	for len(a.tabs) > 0 {
		a.closeTab(len(a.tabs) - 1)
	}

	if len(a.recentFiles) != len(before) {
		t.Fatalf("ring changed length on close: %v → %v", before, a.recentFiles)
	}
	for i := range before {
		if a.recentFiles[i] != before[i] {
			t.Fatalf("ring reordered on close: %v → %v", before, a.recentFiles)
		}
	}
	if a.recentFiles[0] != paths[2] {
		t.Fatalf("head should still be the last file visited, got %q", a.recentFiles[0])
	}
}

// A file deleted since it was visited is dropped from the ring, not
// offered and then flashed at. The prune is in-memory; the write happens
// at Close like every other session change.
func TestRecentFiles_PrunesVanishedFiles(t *testing.T) {
	a, _, paths := recentFilesApp(t, 3)
	if err := os.Remove(paths[0]); err != nil {
		t.Fatalf("remove: %v", err)
	}

	a.menuRecentFiles()

	for _, p := range a.recentFiles {
		if p == paths[0] {
			t.Fatalf("deleted file survived the prune: %v", a.recentFiles)
		}
	}
	labels := pickerLabels(t, a)
	if len(labels) != 1 {
		t.Fatalf("want only the surviving non-current file, got %v", labels)
	}
}

// With nothing but the file already on screen there is no list — the row
// is disabled and the picker says so rather than opening empty.
func TestRecentFiles_SingleFileHasNothingToOffer(t *testing.T) {
	a, _, _ := recentFilesApp(t, 1)

	if a.hasRecentFiles() {
		t.Fatal("the only visited file is the one on screen — the row should be disabled")
	}
	a.menuRecentFiles()
	if _, ok := a.modal.(*paletteModal); ok {
		t.Fatal("picker opened with no rows to show")
	}
}

// The ring is per folder and rides the session entry: recorded on Close,
// seeded at load, in order. Two Apps rather than one, because the
// question is whether the list survives the editor.
func TestRecentFiles_SurviveTheSessionEntry(t *testing.T) {
	a, root, paths := recentFilesApp(t, 3)
	store := &session.Store{}
	a.sessionStore = store
	a.recordSession()

	e, ok := store.Find(root)
	if !ok {
		t.Fatal("no entry recorded for the root")
	}
	if len(e.Recent) != 3 || e.Recent[0] != paths[2] {
		t.Fatalf("recorded ring is %v, want most-recent-first from %q", e.Recent, paths[2])
	}

	b := newTestApp(t, root)
	b.sessionStore = store
	b.loadRecentFiles()

	if len(b.recentFiles) != 3 {
		t.Fatalf("seeded ring is %v, want 3 entries", b.recentFiles)
	}
	for i := range e.Recent {
		if b.recentFiles[i] != e.Recent[i] {
			t.Fatalf("seeded ring %v does not match the stored one %v", b.recentFiles, e.Recent)
		}
	}
	// And it is a COPY: pruning the fresh App's ring must not reach back
	// into the store's entry.
	b.recentFiles = b.recentFiles[:1]
	if again, _ := store.Find(root); len(again.Recent) != 3 {
		t.Fatalf("the store's entry was aliased by the seed: %v", again.Recent)
	}
}

// A restored session lands on one tab, and that tab is the ring's head —
// the restored tabs keep the order they were remembered in rather than
// being re-touched in tab-index order.
func TestRecentFiles_RestoredSessionKeepsItsOrder(t *testing.T) {
	root := t.TempDir()
	one := filepath.Join(root, "one.txt")
	two := filepath.Join(root, "two.txt")
	for _, p := range []string{one, two} {
		if err := os.WriteFile(p, []byte("x\n"), 0o644); err != nil {
			t.Fatalf("seed: %v", err)
		}
	}
	store := &session.Store{}
	store.Record(session.Entry{
		Root:   root,
		Active: 1,
		Tabs:   []session.TabState{{Path: one}, {Path: two}},
		// Remembered order says the user was in two.txt last, having been
		// in one.txt before it — the opposite of tab order.
		Recent: []string{two, one},
	})

	a := newTestApp(t, root)
	a.sessionStore = store
	a.loadRecentFiles()
	a.restoreSession()

	if got := a.activeTabPtr().Path; got != two {
		t.Fatalf("restored onto %q, want %q", got, two)
	}
	if a.recentFiles[0] != two || a.recentFiles[1] != one {
		t.Fatalf("ring is %v, want [two one]", a.recentFiles)
	}
}

// A file outside the project root gets its directory spelled out. The
// tab switcher leaves that blank on purpose; here it is the only thing
// distinguishing a dependency's client.go from the project's own.
func TestRecentFileLabel_NamesOutsideDirectories(t *testing.T) {
	a, root, _ := recentFilesApp(t, 1)
	outside := filepath.Join(filepath.Dir(root), "elsewhere.txt")
	if err := os.WriteFile(outside, []byte("x\n"), 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	t.Cleanup(func() { os.Remove(outside) })

	label := a.recentFileLabel(outside)

	if !strings.Contains(label, "elsewhere.txt") {
		t.Fatalf("label %q should name the file", label)
	}
	if !strings.Contains(label, displayPath(filepath.Dir(outside))) {
		t.Fatalf("label %q should name the directory of an outside file", label)
	}
}

// An untitled buffer has nothing a picker row could reopen, so it never
// enters the ring — and must not enter it as a blank row either.
func TestRecentFiles_IgnoresUntitledBuffers(t *testing.T) {
	a, _, _ := recentFilesApp(t, 1)
	before := len(a.recentFiles)

	a.touchRecentFile("")

	if len(a.recentFiles) != before {
		t.Fatalf("an untitled buffer entered the ring: %v", a.recentFiles)
	}
}
