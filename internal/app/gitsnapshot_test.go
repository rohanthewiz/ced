// =============================================================================
// File: internal/app/gitsnapshot_test.go
// Author: Rohan Allison <rohanthewiz@gmail.com>
// Created: 2026-11-10
// Copyright: 2026 Rohan Allison. All rights reserved.
// =============================================================================

// Tests for gitsnapshot.go: the async git status snapshot. Most of the
// suite runs on a directly built App, which keeps snapshots inline; these
// tests turn async on (as New does) and pump the event queue the way the
// real loop does, with gitcmd_test.go's pumpAppEvents.

package app

import (
	"path/filepath"
	"testing"
	"time"
)

// asyncGitApp is a newTestApp over a fresh repo with one committed file
// and one untracked one, with the async snapshot switched on.
func asyncGitApp(t *testing.T) (*App, string) {
	t.Helper()
	requireGit(t)
	repo := initRepo(t)
	writeCommit(t, repo, "f.txt", "one\n", "init")
	writeFileT(t, filepath.Join(repo, "new.txt"), "n\n")
	a := newTestApp(t, repo)
	a.gitSnap.async = true
	return a, repo
}

// TestRefreshGitStatus_InlineWithoutAsync pins the contract the rest of
// the suite leans on: a directly built App (async off) applies the
// snapshot before refreshGitStatus returns.
func TestRefreshGitStatus_InlineWithoutAsync(t *testing.T) {
	requireGit(t)
	repo := initRepo(t)
	writeCommit(t, repo, "f.txt", "one\n", "init")
	writeFileT(t, filepath.Join(repo, "new.txt"), "n\n")
	a := newTestApp(t, repo)

	a.refreshGitStatus()
	if !a.gitIsRepo || !a.tree.DirtyFiles[filepath.Join(repo, "new.txt")] {
		t.Fatalf("inline snapshot not applied: repo=%v dirty=%v", a.gitIsRepo, sortedKeys(a.tree.DirtyFiles))
	}
}

// TestRefreshGitStatus_AsyncAppliesOnEvent: with async on, the call
// returns with nothing applied (the loop never waited on git), and the
// gitStatusEvent brings the snapshot in.
func TestRefreshGitStatus_AsyncAppliesOnEvent(t *testing.T) {
	a, repo := asyncGitApp(t)

	a.refreshGitStatus()
	if a.gitIsRepo {
		t.Fatal("async refresh applied before its event arrived")
	}
	if a.gitSnap.flightSeq == 0 {
		t.Fatal("no scan recorded in flight")
	}
	pumpAppEvents(t, a, func() bool { return a.gitIsRepo })
	if !a.tree.DirtyFiles[filepath.Join(repo, "new.txt")] {
		t.Errorf("dirty set = %v, want new.txt", sortedKeys(a.tree.DirtyFiles))
	}
	if a.gitBranch != "main" {
		t.Errorf("branch = %q, want main", a.gitBranch)
	}
	if a.gitSnap.flightSeq != 0 {
		t.Errorf("flightSeq = %d after the answer, want 0", a.gitSnap.flightSeq)
	}
}

// TestRefreshGitStatus_CoalescesWhileInFlight: three requests while one
// scan runs make exactly ONE more scan, started when the first answers —
// and the rerun sees a change made after the first scan began.
func TestRefreshGitStatus_CoalescesWhileInFlight(t *testing.T) {
	a, repo := asyncGitApp(t)

	a.refreshGitStatus()
	writeFileT(t, filepath.Join(repo, "later.txt"), "l\n")
	a.refreshGitStatus()
	a.refreshGitStatus()
	if a.gitSnap.seq != 1 || !a.gitSnap.rerun {
		t.Fatalf("after three requests: seq=%d rerun=%v, want 1 / true", a.gitSnap.seq, a.gitSnap.rerun)
	}
	pumpAppEvents(t, a, func() bool { return a.gitSnap.seq == 2 && a.gitSnap.flightSeq == 0 })
	if a.gitSnap.rerun {
		t.Error("rerun still pending after the rerun ran")
	}
	if !a.tree.DirtyFiles[filepath.Join(repo, "later.txt")] {
		t.Errorf("rerun missed the later change: %v", sortedKeys(a.tree.DirtyFiles))
	}
	// Nothing more was launched.
	time.Sleep(20 * time.Millisecond)
	if a.screen.HasPendingEvent() || a.gitSnap.seq != 2 {
		t.Errorf("an extra scan ran: seq=%d", a.gitSnap.seq)
	}
}

// TestHandleGitStatus_StaleAnswerAfterInlineIsDropped: an async scan
// overtaken by an inline snapshot (a git command finished meanwhile) must
// not overwrite it — but its arrival still means nothing is in flight,
// or every later request would wait on a scan that is over.
func TestHandleGitStatus_StaleAnswerAfterInlineIsDropped(t *testing.T) {
	a, _ := asyncGitApp(t)
	a.gitSnap.seq, a.gitSnap.flightSeq, a.gitSnap.startedAt = 4, 4, time.Now()

	a.refreshGitStatusNow()
	if !a.gitIsRepo || a.gitSnap.seq != 5 {
		t.Fatalf("inline snapshot: repo=%v seq=%d, want true / 5", a.gitIsRepo, a.gitSnap.seq)
	}
	a.handleGitStatus(&gitStatusEvent{seq: 4, root: a.rootDir, st: gitStatus{}})
	if !a.gitIsRepo {
		t.Error("stale answer overwrote the inline snapshot")
	}
	if a.gitSnap.flightSeq != 0 {
		t.Errorf("flightSeq = %d after the stale answer, want 0", a.gitSnap.flightSeq)
	}
}

// TestRefreshGitStatusNow_SatisfiesAPendingRerun: an inline snapshot
// describes the repo after every request that set the rerun, so the
// in-flight answer must not start another scan for them.
func TestRefreshGitStatusNow_SatisfiesAPendingRerun(t *testing.T) {
	a, _ := asyncGitApp(t)
	a.gitSnap.seq, a.gitSnap.flightSeq, a.gitSnap.startedAt = 1, 1, time.Now()
	a.refreshGitStatus() // in flight → rerun
	if !a.gitSnap.rerun {
		t.Fatal("request during a scan did not set rerun")
	}
	a.refreshGitStatusNow()
	a.handleGitStatus(&gitStatusEvent{seq: 1, root: a.rootDir})
	if a.gitSnap.seq != 2 || a.gitSnap.flightSeq != 0 {
		t.Errorf("seq=%d flightSeq=%d, want 2 / 0 (no rerun launched)", a.gitSnap.seq, a.gitSnap.flightSeq)
	}
}

// TestRefreshGitStatus_LostAnswerRelaunches: a scan in flight longer than
// gitSnapshotStale is presumed dropped (N-124), so a request launches a
// fresh one instead of waiting forever; the lost one, arriving late,
// neither applies nor clears the new flight.
func TestRefreshGitStatus_LostAnswerRelaunches(t *testing.T) {
	a, _ := asyncGitApp(t)
	a.gitSnap.seq, a.gitSnap.flightSeq = 1, 1
	a.gitSnap.startedAt = time.Now().Add(-gitSnapshotStale - time.Second)

	a.refreshGitStatus()
	if a.gitSnap.seq != 2 || a.gitSnap.flightSeq != 2 || a.gitSnap.rerun {
		t.Fatalf("seq=%d flight=%d rerun=%v, want a relaunch (2 / 2 / false)",
			a.gitSnap.seq, a.gitSnap.flightSeq, a.gitSnap.rerun)
	}
	a.handleGitStatus(&gitStatusEvent{seq: 1, root: a.rootDir})
	if a.gitSnap.flightSeq != 2 || a.gitIsRepo {
		t.Errorf("late lost answer: flight=%d applied=%v, want 2 / false", a.gitSnap.flightSeq, a.gitIsRepo)
	}
	pumpAppEvents(t, a, func() bool { return a.gitIsRepo && a.gitSnap.flightSeq == 0 })
}

// TestHandleGitStatus_RerunWaitsForTheNewerFlight: when a relaunch is
// running, a late answer from the scan it replaced must not start the
// rerun — the running scan's own answer will.
func TestHandleGitStatus_RerunWaitsForTheNewerFlight(t *testing.T) {
	a, _ := asyncGitApp(t)
	a.gitSnap.seq, a.gitSnap.flightSeq, a.gitSnap.startedAt = 6, 6, time.Now()
	a.gitSnap.rerun = true

	a.handleGitStatus(&gitStatusEvent{seq: 5, root: a.rootDir})
	if a.gitSnap.seq != 6 || a.gitSnap.flightSeq != 6 || !a.gitSnap.rerun {
		t.Errorf("seq=%d flight=%d rerun=%v, want 6 / 6 / true", a.gitSnap.seq, a.gitSnap.flightSeq, a.gitSnap.rerun)
	}
}

// TestHandleGitStatus_WrongRootIgnored: an answer taken for another
// folder never paints this one.
func TestHandleGitStatus_WrongRootIgnored(t *testing.T) {
	a, _ := asyncGitApp(t)
	a.gitSnap.seq, a.gitSnap.flightSeq = 1, 1
	a.handleGitStatus(&gitStatusEvent{seq: 1, root: "/elsewhere", st: gitStatus{IsRepo: true, Branch: "x"}})
	if a.gitIsRepo || a.gitBranch != "" {
		t.Errorf("foreign snapshot applied: repo=%v branch=%q", a.gitIsRepo, a.gitBranch)
	}
	if a.gitSnap.flightSeq != 0 {
		t.Errorf("flightSeq = %d, want 0", a.gitSnap.flightSeq)
	}
}

// TestGitCmdDone_HooksSeeTheCommandsResultWhileAsync: with snapshots off
// the loop, a finished git command still refreshes INLINE, so its onOK
// hook reads the repo as the command left it — here, the file it staged.
// An async refresh would hand the hook the pre-command snapshot.
func TestGitCmdDone_HooksSeeTheCommandsResultWhileAsync(t *testing.T) {
	a, _ := asyncGitApp(t)
	sawStaged, ran := false, false
	a.runGitCmdOK("Stage", func(app *App) {
		ran = true
		sawStaged = app.gitHasStaged
	}, "add", "new.txt")
	pumpAppEvents(t, a, func() bool { return ran })
	if !sawStaged {
		t.Error("onOK hook read a stale snapshot: gitHasStaged false after `git add`")
	}
}

// TestGitConflictFailHook_RaisesThePanelWhileAsync: the conflict stop
// hook decides from the unmerged set, so it too refreshes inline — with
// async on, a cherry-pick that stops on a conflict still claims the
// failure and lists the file.
func TestGitConflictFailHook_RaisesThePanelWhileAsync(t *testing.T) {
	repo := cherryPickConflictRepo(t)
	a := newTestApp(t, repo)
	a.gitSnap.async = true
	if !gitConflictFailHook(a, &gitCmdDoneEvent{}) {
		t.Fatal("stop hook declined a conflicted cherry-pick")
	}
	if len(a.gitConflicted) == 0 {
		t.Error("hook ran on a snapshot without the unmerged file")
	}
}

// TestApplyGitStatus_FillsTheOpenPanelFromTheSnapshot: an open git panel
// takes its rows from the snapshot rather than forking its own status —
// here the root is not even a repo, so a fork would have listed nothing.
// A shut panel is left alone.
func TestApplyGitStatus_FillsTheOpenPanelFromTheSnapshot(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	rows := []gitPanelFile{{Path: filepath.Join(a.rootDir, "x.txt"), Rel: "x.txt", Code: " M"}}

	a.applyGitStatus(gitStatus{IsRepo: true, PanelFiles: rows})
	if a.gitPanel.files != nil {
		t.Fatalf("a shut panel was filled: %+v", a.gitPanel.files)
	}
	a.gitPanel.open = true
	a.applyGitStatus(gitStatus{IsRepo: true, PanelFiles: rows})
	if len(a.gitPanel.files) != 1 || a.gitPanel.files[0] != rows[0] {
		t.Fatalf("panel rows = %+v, want the snapshot's %+v", a.gitPanel.files, rows)
	}
}
