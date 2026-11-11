// =============================================================================
// File: internal/app/gitsnapshot.go
// Author: Rohan Allison <rohanthewiz@gmail.com>
// Created: 2026-11-10
// Copyright: 2026 Rohan Allison. All rights reserved.
// =============================================================================

// gitsnapshot.go takes the git status snapshot (gitstatus.go's
// loadGitStatus) OFF the main loop (N-127).
//
// The snapshot runs every 10 seconds on the tree tick, after every save,
// after every file operation, and once at startup. It used to run INLINE
// on the event loop, so the editor froze for the length of six git forks
// (190–265 ms measured on ced's own repo) every ten seconds whether you
// were typing or not, and app.New spent most of its time there. Now
// refreshGitStatus starts it on a goroutine and the answer comes back as
// a gitStatusEvent; the loop never waits on git for it.
//
// ONE IN FLIGHT. Requests arrive in bursts (a save, then the tick, then a
// tree op), and each snapshot is a full repo scan, so at most one runs at
// a time. A request that arrives while one is running sets `rerun`, and
// the arrival of the running one starts exactly one more:
//
//	request ─▶ launch #1 ───────────────▶ answer #1: apply, rerun? ─▶ launch #2
//	request ─▶ (in flight: rerun = true)
//	request ─▶ (in flight: rerun = true)          ← three requests, two scans
//
// The rerun is needed, not just tidy: a request made while #1 runs may
// describe a change #1 already missed (a save that landed after git read
// that file), so dropping it would leave the tree stale until the next
// tick.
//
// INLINE STILL EXISTS, ON PURPOSE. refreshGitStatusNow takes the snapshot
// on the loop, for the few callers that READ it in the very next line:
// a finished git command (its onOK hooks ask gitOp, the upstream, the
// conflict set — "did the cherry-pick finish?") and the conflict stop
// hook. Those follow a git command the user just waited for, and the
// snapshot is two forks now, not six (gitstatus.go). Making them wait for
// an event instead would turn every hook into a continuation for a few
// tens of milliseconds saved where nobody is typing.
//
// THE SEQUENCE NUMBER keeps the two paths from lying to each other. Every
// launch, async or inline, takes the next seq; an async answer is applied
// only if its seq is still the latest. So an async scan started before a
// git command can't overwrite the fresher inline one taken after it:
//
//	async launch seq=4 ─────────────────────────────▶ answer seq=4: DROPPED
//	        git checkout done ─▶ inline seq=5, applied
//
// A LOST ANSWER can't wedge it. tcell drops PostEvent when its queue is
// full (N-124), and a dropped answer would leave a scan "in flight"
// forever —
// every later request would just set rerun, and the snapshot would never
// run again. An in-flight scan older than gitSnapshotStale is presumed
// lost; the next request launches a fresh one (whose seq makes the lost
// one stale if it ever does arrive). Same rule as format.go's
// formatRunStale.
//
// A DIRECTLY BUILT App runs inline. `async` is off in the zero value and
// New() turns it on, so tests that build App by hand (newTestApp) keep
// the snapshot synchronous — they assert on the tree's dirty set in the
// line after a refresh — while the tests in gitsnapshot_test.go turn it on
// and pump the event like the real loop does.

package app

import (
	"time"
)

// gitSnapshotStale is how long an in-flight async snapshot may run before
// a new request stops waiting for it and launches another. Far above any
// real scan (two forks; seconds even on a huge cold repo), so it only
// ever fires for an answer that was dropped on the way back.
const gitSnapshotStale = 30 * time.Second

// gitStatusEvent carries a finished async snapshot to the main loop.
// root is the project root it was taken for: the App's root never changes
// (a folder switch builds a new App on a new screen), so a mismatch can
// only be a bug, but checking costs nothing and stops a snapshot of one
// folder from ever painting another.
type gitStatusEvent struct {
	when time.Time
	seq  int
	root string
	st   gitStatus
}

// When satisfies the tcell.Event interface.
func (e *gitStatusEvent) When() time.Time { return e.when }

// gitSnapState is the async snapshot's bookkeeping, all main-loop state.
type gitSnapState struct {
	// async runs snapshots on a goroutine. Off in a directly built App
	// (see the file header); New sets it.
	async bool
	// seq numbers launches, async and inline alike; only the latest
	// launch's answer is applied.
	seq int
	// flightSeq is the seq of the async scan now running, 0 for none;
	// startedAt dates it for the lost-answer rule. A seq rather than a
	// bool because "is this answer the one in flight?" and "is this
	// answer the latest?" are different questions once an inline
	// snapshot has overtaken the scan: its answer is stale (not applied)
	// yet still means the goroutine is done.
	flightSeq int
	startedAt time.Time
	// rerun records a request that arrived while a scan was in flight.
	rerun bool
}

// refreshGitStatus brings the git-derived state (the tree's dirty and
// conflict colours, the branch label, the tracking counts, the menu
// predicates' flags, the git panels) up to date with the repo — WITHOUT
// blocking the main loop: the snapshot is taken on a goroutine and
// applied when its gitStatusEvent arrives (handleGitStatus). Use it
// wherever nothing reads the snapshot in the next line: the tick, saves,
// file operations, startup.
//
// Falls back to the inline refreshGitStatusNow when async is off (a
// directly built App) or there is no screen to post the answer to.
func (a *App) refreshGitStatus() {
	if a.tree == nil {
		return
	}
	if !a.gitSnap.async || a.screen == nil {
		a.refreshGitStatusNow()
		return
	}
	if a.gitSnap.flightSeq != 0 && time.Since(a.gitSnap.startedAt) < gitSnapshotStale {
		a.gitSnap.rerun = true
		return
	}
	a.launchGitStatus()
}

// refreshGitStatusNow takes the snapshot INLINE and applies it before
// returning. For callers that read the snapshot right after refreshing
// it (handleGitCmdDone's hooks, the conflict stop hook); everything else
// calls refreshGitStatus.
//
// Taking a seq makes any async answer still in flight stale, since this
// one describes a later repo. It also satisfies any pending rerun: every
// request that set it was made before this scan started.
func (a *App) refreshGitStatusNow() {
	if a.tree == nil {
		return
	}
	a.gitSnap.seq++
	a.gitSnap.rerun = false
	a.applyGitStatus(loadGitStatus(a.rootDir))
}

// launchGitStatus starts one async snapshot. Everything the goroutine
// needs is copied out first; it touches no App state.
func (a *App) launchGitStatus() {
	a.gitSnap.seq++
	a.gitSnap.flightSeq = a.gitSnap.seq
	a.gitSnap.startedAt = time.Now()
	a.gitSnap.rerun = false
	seq, root, scr := a.gitSnap.seq, a.rootDir, a.screen
	go func() {
		st := loadGitStatus(root)
		_ = scr.PostEvent(&gitStatusEvent{when: time.Now(), seq: seq, root: root, st: st})
	}()
}

// handleGitStatus lands an async snapshot. Three separate questions:
//
//   - Is this the scan in flight? Then nothing is running any more —
//     even if its answer is about to be dropped.
//   - Is it the LATEST launch? Only then is it applied; an inline
//     snapshot or a relaunch that came after it knows better.
//   - Was a rerun asked for while it ran? Then start one, but only
//     once nothing is in flight (a newer relaunch may still be running,
//     and its own answer will start the rerun).
func (a *App) handleGitStatus(e *gitStatusEvent) {
	if e.seq == a.gitSnap.flightSeq {
		a.gitSnap.flightSeq = 0
	}
	if e.seq == a.gitSnap.seq && e.root == a.rootDir {
		a.applyGitStatus(e.st)
	}
	if a.gitSnap.rerun && a.gitSnap.flightSeq == 0 {
		a.refreshGitStatus()
	}
}

// applyGitStatus stamps a snapshot onto the App and the file tree, so
// changed files render in the Modified color on the next draw, and
// refreshes the panels that mirror it. A non-git project leaves the
// tree's dirty maps empty, which the renderer treats as "everything
// clean". Main loop only.
func (a *App) applyGitStatus(st gitStatus) {
	if a.tree == nil {
		return
	}
	a.gitIsRepo = st.IsRepo
	a.gitHasStaged = st.HasStaged
	a.gitHasStash = st.HasStash
	a.gitStagedFiles = st.StagedFiles
	a.gitConflicted = st.ConflictedFiles
	a.gitConflictMarkerSizes = st.ConflictMarkerSizes
	a.applyConflictMarkerSizes()
	a.gitOp, a.gitDir = st.InProgress, st.GitDir
	// The tracking facts follow the same snapshot, including the reset
	// on a non-repo: a stale "↑3" outliving the folder it described
	// would be a status bar lying about a repo the user has left.
	a.gitUpstream = st.Upstream
	a.gitAhead = st.Ahead
	a.gitBehind = st.Behind
	a.gitHasRemote = st.HasRemote
	if !st.IsRepo {
		a.tree.DirtyFiles = nil
		a.tree.DirtyFolders = nil
		a.tree.ConflictFiles, a.tree.ConflictFolders = nil, nil
		a.gitBranch = ""
	} else {
		a.tree.DirtyFiles = st.DirtyFiles
		a.tree.DirtyFolders = dirtyFolderSet(st.DirtyFiles, a.rootDir)
		// Unmerged files in the error colour, folders rolled up the same
		// way dirty ones are (conflictpanel.go's "parked repo" signal).
		a.tree.ConflictFiles = st.ConflictedFiles
		a.tree.ConflictFolders = dirtyFolderSet(st.ConflictedFiles, a.rootDir)
		a.gitBranch = st.Branch
	}
	// The git panel mirrors the same status snapshot — refreshing it
	// here (a no-op while collapsed) means every path that keeps the
	// tree's dirty colors honest keeps the panel honest too: the 10s
	// tick, saves, file ops, and finished git commands. It takes the
	// snapshot's own list rather than forking `git status` again.
	a.setGitPanelFiles(st.PanelFiles)
	// The log panel rides the same pipeline (no-op while collapsed), so
	// a commit, cherry-pick, or outside-the-editor fetch shows up on the
	// next tick without a dedicated refresh path.
	a.refreshGitLogCommits()
	// The Conflicts panel too, for the same reason: a resolve staged from
	// a terminal, or a continue run there, shows up on the next tick.
	// Shut, it still keeps the operation's record fresh while there is an
	// operation (or a record of one to retire): the side names on every
	// lens and separator label read it (conflictsides.go).
	if a.conflictPanel.open {
		a.refreshConflictPanel()
	} else if a.gitOp != "" || a.conflictPanel.info.op != "" {
		a.refreshConflictPanelInfo()
	}
}
