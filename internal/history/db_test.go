// =============================================================================
// File: internal/history/db_test.go
// Author: Rohan Allison <rohanthewiz@gmail.com>
// Created: 2026-09-28
// Copyright: 2026 Rohan Allison. All rights reserved.
// =============================================================================

// Tests for the bytdb persistence. The claims worth pinning are the ones
// two editors on one repository depend on: a write ADDS to what another
// instance stored, a contended file defers rather than loses the write,
// and a pruned folder's rows go with it — without taking a sibling whose
// name merely starts the same way. Plus the repository-facing promises:
// loading creates nothing, paths are stored relative, and the database is
// gitignored.

package history

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rohanthewiz/bytdb"
	"github.com/rohanthewiz/ced/internal/session"
)

// repo is a fresh repository directory, normalized so folder paths built
// from it match what the index records (macOS's /var → /private/var).
func repo(t *testing.T) string {
	return session.Normalize(t.TempDir())
}

// mustLoad loads or fails the test.
func mustLoad(t *testing.T, root string) *History {
	t.Helper()
	h, err := Load(root, DBPath(root))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	return h
}

// mustWrite writes or fails the test.
func mustWrite(t *testing.T, h *History, root string, ring []string) {
	t.Helper()
	if err := h.Write(DBPath(root), ring); err != nil {
		t.Fatalf("Write: %v", err)
	}
}

// TestLoad_CreatesNothing pins that opening ced on a folder leaves no
// trace: with no database, a load is an empty history and no .ced/
// appears.
func TestLoad_CreatesNothing(t *testing.T) {
	root := repo(t)
	h := mustLoad(t, root)
	if h.Folders.Len() != 0 || len(h.Files()) != 0 || h.Dirty() {
		t.Fatal("a load with no database produced history")
	}
	if _, err := os.Stat(filepath.Join(root, ".ced")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("load created .ced/: %v", err)
	}
	if err := h.Write(DBPath(root), nil); err != nil {
		t.Fatalf("clean write: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, ".ced")); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("a write with nothing pending created .ced/")
	}
}

// TestWrite_RoundTripsFoldersAndFiles pins that a written history loads
// back ranking identically, with the file ring in its order.
func TestWrite_RoundTripsFoldersAndFiles(t *testing.T) {
	root := repo(t)
	h := mustLoad(t, root)
	app, ed := filepath.Join(root, "internal", "app"), filepath.Join(root, "internal", "editor")
	h.Folders.Record(app)
	h.Folders.Record(app)
	h.Folders.Record(ed)
	ring := []string{filepath.Join(root, "b.go"), filepath.Join(root, "a.go")}
	for _, f := range ring {
		h.TouchFile(f)
	}
	mustWrite(t, h, root, ring)
	if h.Dirty() {
		t.Fatal("a successful write left changes pending")
	}

	back := mustLoad(t, root)
	for _, byFreq := range []bool{false, true} {
		a := strings.Join(h.Folders.top(root, byFreq, 10, nil), ",")
		b := strings.Join(back.Folders.top(root, byFreq, 10, nil), ",")
		if a != b {
			t.Fatalf("freq %v: %s != %s after round trip", byFreq, a, b)
		}
	}
	if got := back.Files(); strings.Join(got, ",") != strings.Join(ring, ",") {
		t.Fatalf("files = %v, want %v", got, ring)
	}
	// A use after the reload is newer than everything stored.
	late := filepath.Join(root, "late")
	back.Folders.Record(late)
	if got := back.Folders.Recent(root, 1, nil); got[0] != late {
		t.Fatalf("head after reload = %v, want the new use", got)
	}
}

// TestWrite_PathsAreRelativeAndIgnored pins the repository-facing half:
// rows inside the repo are stored relative (a moved checkout keeps its
// history), a file outside it absolute, and the database is gitignored.
func TestWrite_PathsAreRelativeAndIgnored(t *testing.T) {
	root := repo(t)
	h := mustLoad(t, root)
	h.Folders.Record(filepath.Join(root, "pkg"))
	outside := filepath.Join(t.TempDir(), "dep.go")
	ring := []string{filepath.Join(root, "pkg", "x.go"), outside}
	for _, f := range ring {
		h.TouchFile(f)
	}
	mustWrite(t, h, root, ring)

	ign, err := os.ReadFile(filepath.Join(root, ".ced", ".gitignore"))
	if err != nil || !strings.Contains(string(ign), ignoreLine) {
		t.Fatalf(".ced/.gitignore = %q, %v — want it to ignore %s", ign, err, ignoreLine)
	}

	// Move the checkout: relative rows follow it, the absolute one stays.
	moved := filepath.Join(filepath.Dir(root), filepath.Base(root)+"-moved")
	if err := os.Rename(root, moved); err != nil {
		t.Fatal(err)
	}
	back := mustLoad(t, moved)
	if !back.Folders.HasUsesBelow(moved) {
		t.Fatal("folder history did not follow the moved checkout")
	}
	want := []string{filepath.Join(moved, "pkg", "x.go"), outside}
	if got := back.Files(); strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("files after move = %v, want %v", got, want)
	}
}

// TestEnsureIgnored_AppendsOnce pins the .gitignore edit: an existing
// file gains the line (after a newline it was missing) exactly once.
func TestEnsureIgnored_AppendsOnce(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ".gitignore")
	if err := os.WriteFile(path, []byte("keep-me"), 0o644); err != nil {
		t.Fatal(err)
	}
	ensureIgnored(dir)
	ensureIgnored(dir)
	data, _ := os.ReadFile(path)
	if string(data) != "keep-me\n"+ignoreLine+"\n" {
		t.Fatalf(".gitignore = %q", data)
	}
}

// TestWrite_TwoInstancesAdd pins the reason writes are deltas: two
// editors loaded the same repository's history, both used the same
// folder and touched files, and both wrote — the folder count is the sum,
// and the file ring interleaves them with the later writer's on top.
func TestWrite_TwoInstancesAdd(t *testing.T) {
	root := repo(t)
	shared := filepath.Join(root, "shared")
	seed := mustLoad(t, root)
	seed.Folders.Record(shared)
	mustWrite(t, seed, root, nil)

	a := mustLoad(t, root)
	b := mustLoad(t, root)
	a.Folders.Record(shared)
	a.Folders.Record(shared)
	a.Folders.Record(filepath.Join(root, "only-a"))
	b.Folders.Record(filepath.Join(root, "only-b"))
	b.Folders.Record(shared)
	fa, fb := filepath.Join(root, "a.go"), filepath.Join(root, "b.go")
	a.TouchFile(fa)
	b.TouchFile(fb)
	mustWrite(t, a, root, []string{fa})
	mustWrite(t, b, root, []string{fb})

	got := mustLoad(t, root)
	if n := got.Folders.find(shared); n == nil || n.self.hits != 4 {
		t.Fatalf("shared = %+v, want 1 + 2 + 1 hits", n)
	}
	if got.Folders.find(filepath.Join(root, "only-a")) == nil || got.Folders.find(filepath.Join(root, "only-b")) == nil {
		t.Fatal("one instance's folder was overwritten by the other's write")
	}
	// b wrote last, so its uses were re-issued after a's.
	want := []string{shared, filepath.Join(root, "only-b"), filepath.Join(root, "only-a")}
	if r := got.Folders.Recent(root, 3, nil); strings.Join(r, ",") != strings.Join(want, ",") {
		t.Fatalf("recent folders = %v, want %v", r, want)
	}
	if files := got.Files(); strings.Join(files, ",") != fb+","+fa {
		t.Fatalf("files = %v, want b.go then a.go", files)
	}
}

// TestWrite_RemoveDeletesTheSubtreeOnly pins the range delete's boundary:
// pruning proj takes proj/sub with it and leaves proj2 and proj-x — names
// that share the prefix but not the folder.
func TestWrite_RemoveDeletesTheSubtreeOnly(t *testing.T) {
	root := repo(t)
	h := mustLoad(t, root)
	dirs := []string{"proj", filepath.Join("proj", "sub"), "proj2", "proj-x"}
	for _, d := range dirs {
		h.Folders.Record(filepath.Join(root, d))
	}
	mustWrite(t, h, root, nil)

	h2 := mustLoad(t, root)
	h2.Folders.Remove(filepath.Join(root, "proj"))
	mustWrite(t, h2, root, nil)

	got := mustLoad(t, root)
	if got.Folders.find(filepath.Join(root, "proj")) != nil || got.Folders.find(filepath.Join(root, "proj", "sub")) != nil {
		t.Fatal("the pruned folder or its subfolder survived the write")
	}
	if got.Folders.find(filepath.Join(root, "proj2")) == nil || got.Folders.find(filepath.Join(root, "proj-x")) == nil {
		t.Fatal("a sibling sharing the prefix was deleted with it")
	}
}

// TestWrite_RemovedFileLeavesTheRing pins that a file pruned from the ring
// (deleted on disk) loses its row too.
func TestWrite_RemovedFileLeavesTheRing(t *testing.T) {
	root := repo(t)
	h := mustLoad(t, root)
	fa, fb := filepath.Join(root, "a.go"), filepath.Join(root, "b.go")
	h.TouchFile(fa)
	h.TouchFile(fb)
	mustWrite(t, h, root, []string{fb, fa})

	h2 := mustLoad(t, root)
	h2.RemoveFile(fa)
	mustWrite(t, h2, root, []string{fb})
	if got := mustLoad(t, root).Files(); len(got) != 1 || got[0] != fb {
		t.Fatalf("files = %v, want only b.go", got)
	}
}

// TestWrite_FilesCappedAcrossInstances pins the trim: two instances each
// under the cap can together exceed it, and the table keeps only the
// newest MaxRecentFiles.
func TestWrite_FilesCappedAcrossInstances(t *testing.T) {
	root := repo(t)
	for inst := 0; inst < 2; inst++ {
		h := mustLoad(t, root)
		var ring []string
		for i := 0; i < MaxRecentFiles-10; i++ {
			f := filepath.Join(root, fmt.Sprintf("i%d-f%02d.go", inst, i))
			ring = append([]string{f}, ring...)
			h.TouchFile(f)
		}
		mustWrite(t, h, root, ring)
	}
	got := mustLoad(t, root).Files()
	if len(got) != MaxRecentFiles {
		t.Fatalf("kept %d files, want the cap %d", len(got), MaxRecentFiles)
	}
	if !strings.Contains(got[0], "i1-f39") {
		t.Fatalf("head = %s, want the second instance's newest", got[0])
	}
}

// TestWrite_LockedDefersTheChanges pins the degradation path: while
// another engine holds the file, a write fails with ErrLocked after its
// retries and keeps everything pending; once the file is free, the next
// write carries it all.
func TestWrite_LockedDefersTheChanges(t *testing.T) {
	root := repo(t)
	h := mustLoad(t, root)
	h.Folders.Record(filepath.Join(root, "first"))
	mustWrite(t, h, root, nil) // creates the file

	h.Folders.Record(filepath.Join(root, "later"))
	// A separate engine, not sql.Open: the driver shares one engine per
	// path within a process, so it would not be a rival at all.
	eng, err := bytdb.Open(DBPath(root))
	if err != nil {
		t.Fatalf("engine open: %v", err)
	}
	err = h.Write(DBPath(root), nil)
	if !errors.Is(err, bytdb.ErrLocked) {
		t.Fatalf("Write under a held lock = %v, want ErrLocked", err)
	}
	if !h.Dirty() {
		t.Fatal("a failed write dropped the pending changes")
	}
	if err := eng.Close(); err != nil {
		t.Fatal(err)
	}
	mustWrite(t, h, root, nil)
	if mustLoad(t, root).Folders.find(filepath.Join(root, "later")) == nil {
		t.Fatal("the deferred change never reached the database")
	}
}

// TestLoad_TrimsAnOversizedIndex pins that folder rows past the cap —
// which several instances can produce between them — are evicted at load
// and the deletions reach the database on the next write.
func TestLoad_TrimsAnOversizedIndex(t *testing.T) {
	root := repo(t)
	a := mustLoad(t, root)
	b := mustLoad(t, root)
	for i := 0; i < MaxFolders-10; i++ {
		a.Folders.Record(filepath.Join(root, "a", fmt.Sprintf("d%03d", i)))
		b.Folders.Record(filepath.Join(root, "b", fmt.Sprintf("d%03d", i)))
	}
	mustWrite(t, a, root, nil)
	mustWrite(t, b, root, nil)

	h := mustLoad(t, root)
	if h.Folders.Len() > MaxFolders {
		t.Fatalf("loaded %d folders, over the cap", h.Folders.Len())
	}
	mustWrite(t, h, root, nil)
	if n := mustLoad(t, root).Folders.Len(); n > MaxFolders {
		t.Fatalf("database still holds %d folders after the trim was written", n)
	}
}

// TestLoad_SkipsRowsOutsideTheRepo pins decodeFolder's refusal: a row
// that is absolute or climbs out (a hand-edit, a copied database) is
// skipped, not planted outside the repository.
func TestLoad_SkipsRowsOutsideTheRepo(t *testing.T) {
	h := New(repo(t))
	for _, bad := range []string{"", "/abs/path", "../escape", "a/../../escape"} {
		if _, ok := h.decodeFolder(bad); ok {
			t.Fatalf("decodeFolder(%q) accepted a path outside the repo", bad)
		}
	}
	if _, ok := h.decodeFolder("internal/app"); !ok {
		t.Fatal("decodeFolder refused an ordinary relative folder")
	}
}

// TestWrite_CompactsPastTheCeiling pins that the log does not grow by the
// session forever: sizes climb while under compactAbove, and the write
// that crosses it VACUUMs the file back down to its live set — with every
// row still there afterwards.
func TestWrite_CompactsPastTheCeiling(t *testing.T) {
	root := repo(t)
	size := func() int64 {
		fi, err := os.Stat(DBPath(root))
		if err != nil {
			t.Fatalf("stat: %v", err)
		}
		return fi.Size()
	}
	folder := func(i int) string { return filepath.Join(root, "pkg", fmt.Sprintf("d%02d", i)) }

	h := mustLoad(t, root)
	for i := 0; i < 20; i++ {
		h.Folders.Record(folder(i))
	}
	mustWrite(t, h, root, nil)
	live := size()

	// A ceiling a few writes away; restored so other tests keep the real one.
	old := compactAbove
	compactAbove = live * 2
	t.Cleanup(func() { compactAbove = old })

	prev, dropped := live, false
	for s := 0; s < 40; s++ {
		h := mustLoad(t, root)
		for i := 0; i < 20; i++ {
			h.Folders.Record(folder(i)) // every row rewritten: pure garbage growth
		}
		mustWrite(t, h, root, nil)
		cur := size()
		if cur > compactAbove*2 {
			t.Fatalf("session %d: file %d bytes, ceiling %d never compacted", s, cur, compactAbove)
		}
		if cur < prev {
			dropped = true
			if cur > compactAbove {
				t.Errorf("session %d: compacted to %d, still over the ceiling %d", s, cur, compactAbove)
			}
		}
		prev = cur
	}
	if !dropped {
		t.Fatal("the file never shrank: no compaction ran")
	}

	// Compaction drops dead records only: all 20 folders, each with its
	// 41 hits, survive it.
	h = mustLoad(t, root)
	if n := h.Folders.Len(); n != 20 {
		t.Fatalf("folders after compaction = %d, want 20", n)
	}
	if got := h.Folders.find(folder(7)).self.hits; got != 41 {
		t.Errorf("hits after compaction = %d, want 41", got)
	}
}
