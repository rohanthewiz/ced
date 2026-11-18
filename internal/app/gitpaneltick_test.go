// =============================================================================
// File: internal/app/gitpaneltick_test.go
// Author: Rohan Allison <rohanthewiz@gmail.com>
// Created: 2026-11-17
// Copyright: 2026 Rohan Allison. All rights reserved.
// =============================================================================

package app

import (
	"path/filepath"
	"testing"
	"time"
)

// TestGitLogReloadAsync_LoadsOffTheLoopAndApplies is N-140's log half: the
// tick's reload runs on a goroutine and its event installs the commits.
func TestGitLogReloadAsync_LoadsOffTheLoopAndApplies(t *testing.T) {
	if !gitAvailable() {
		t.Skip("git not on PATH")
	}
	repo := initRepo(t)
	writeFileT(t, filepath.Join(repo, "f.txt"), "x\n")
	gitRun(t, repo, "add", "f.txt")
	gitRun(t, repo, "commit", "-q", "-m", "first")
	a := newTestApp(t, repo)
	a.gitLog.open = true
	a.gitLogReloadAsync()
	if len(a.gitLog.commits) != 0 {
		t.Fatal("the reload ran on the loop")
	}
	pumpAppEvents(t, a, func() bool { return len(a.gitLog.commits) == 1 })
	if a.gitLog.toplevel == "" {
		t.Error("toplevel not installed")
	}
}

// TestHandleGitLogLoaded_DropsStaleAndMisdirected: an older reload's
// answer, one for a scope the panel has left, and one for a closed panel
// are all dropped.
func TestHandleGitLogLoaded_DropsStaleAndMisdirected(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	a.gitLog.open = true
	a.gitLog.loadSeq = 3
	commits := []gitLogCommit{{Hash: "abc"}}
	a.handleGitLogLoaded(&gitLogLoadedEvent{seq: 2, scope: a.gitLog.scope, commits: commits})
	a.handleGitLogLoaded(&gitLogLoadedEvent{seq: 3, scope: "refs/heads/elsewhere", commits: commits})
	if len(a.gitLog.commits) != 0 {
		t.Fatal("a stale or misdirected reload was applied")
	}
	a.gitLog.open = false
	a.handleGitLogLoaded(&gitLogLoadedEvent{seq: 3, scope: a.gitLog.scope, commits: commits})
	if len(a.gitLog.commits) != 0 {
		t.Fatal("a reload landed in a closed panel")
	}
}

// TestHandleConflictLoaded_NewestOnlyAndInfoWhenShut is the Conflicts half:
// only the newest reload applies, and a full reload whose panel closed
// meanwhile keeps just the operation record.
func TestHandleConflictLoaded_NewestOnlyAndInfoWhenShut(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	a.conflictPanel.loadSeq = 2
	a.handleConflictLoaded(&conflictLoadedEvent{seq: 1, data: conflictPanelData{info: gitOpInfo{op: "merge"}}})
	if a.conflictPanel.info.op != "" {
		t.Fatal("a stale reload was applied")
	}
	a.handleConflictLoaded(&conflictLoadedEvent{seq: 2, full: true,
		data: conflictPanelData{info: gitOpInfo{op: "rebase"}, files: []conflictFile{{rel: "x"}}}})
	if a.conflictPanel.info.op != "rebase" || len(a.conflictPanel.files) != 0 {
		t.Errorf("shut panel: info=%q files=%d, want the record only", a.conflictPanel.info.op, len(a.conflictPanel.files))
	}
}

// TestFinderTickRebuild_OnlyWhenSomethingMayHaveChanged pins N-140's
// finder half: the tick rebuilds the index when the tree's listing
// changed or the index is older than finderMaxAge — not every period.
func TestFinderTickRebuild_OnlyWhenSomethingMayHaveChanged(t *testing.T) {
	a, _ := withFinder(t)
	// Baselines sit in the past so a rebuild's time.Now() is strictly
	// after them even at the clock's resolution.
	a.finderBuiltAt = time.Now().Add(-time.Second)
	built := a.finderBuiltAt
	a.finderTickRebuild(false)
	if a.finderBuiltAt != built {
		t.Error("an unchanged tick rebuilt the index")
	}
	a.finderTickRebuild(true)
	if !a.finderBuiltAt.After(built) {
		t.Error("a changed tree did not rebuild the index")
	}
	built = time.Now().Add(-finderMaxAge - time.Second)
	a.finderBuiltAt = built
	a.finderTickRebuild(false)
	if !a.finderBuiltAt.After(built) {
		t.Error("an index older than finderMaxAge was not rebuilt")
	}
}

// TestFinderAfterGitStatus_RebuildsOnAChangedFingerprint: the first
// snapshot only records the fingerprint; a different one rebuilds; the
// same one again does not.
func TestFinderAfterGitStatus_RebuildsOnAChangedFingerprint(t *testing.T) {
	a, _ := withFinder(t)
	a.finderStatusSig = ""
	a.finderBuiltAt = time.Now().Add(-time.Second)
	built := a.finderBuiltAt
	a.finderAfterGitStatus("oid1")
	if a.finderBuiltAt != built {
		t.Error("the first snapshot rebuilt the index")
	}
	a.finderAfterGitStatus("oid1")
	if a.finderBuiltAt != built {
		t.Error("an unchanged fingerprint rebuilt the index")
	}
	a.finderAfterGitStatus("oid2\x00?? new.go")
	if !a.finderBuiltAt.After(built) {
		t.Error("a changed fingerprint did not rebuild the index")
	}
}

// TestFilesSig_TracksHeadAndEntries: the fingerprint moves with HEAD and
// with the changed-file list.
func TestFilesSig_TracksHeadAndEntries(t *testing.T) {
	a := parsePorcelainV2([]byte("# branch.oid abc\x00# branch.head main\x00? new.go\x00"))
	b := parsePorcelainV2([]byte("# branch.oid def\x00# branch.head main\x00? new.go\x00"))
	c := parsePorcelainV2([]byte("# branch.oid abc\x00# branch.head main\x00"))
	if a.filesSig() == b.filesSig() || a.filesSig() == c.filesSig() {
		t.Errorf("fingerprints collide: %q %q %q", a.filesSig(), b.filesSig(), c.filesSig())
	}
}
