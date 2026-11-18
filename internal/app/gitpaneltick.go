// =============================================================================
// File: internal/app/gitpaneltick.go
// Author: Rohan Allison <rohanthewiz@gmail.com>
// Created: 2026-11-17
// Copyright: 2026 Rohan Allison. All rights reserved.
// =============================================================================

// gitpaneltick.go takes the git log's and the Conflicts panel's
// tick-time reloads OFF the main loop (N-140). N-127 moved the status
// snapshot to a goroutine, but its apply (applyGitStatus) still forked
// inline for these two while their panel was up — the log's toplevel +
// `git log` (~40 ms each to start on the dev Mac), the op record and the
// unmerged list for Conflicts — on every 10s tick.
//
//	applyGitStatus ──gitPanelsReloadAsync──► go: gather (forks)
//	                                            ├─ gitLogLoadedEvent{seq, scope, …}
//	                                            └─ conflictLoadedEvent{seq, full, …}
//	handle*: newest seq only, panel still in the state it was asked for,
//	then the same apply the inline refresh uses.
//
// Only the TICK path is async. Opening a panel, its ⟳, and the hooks
// after a git command ced ran keep the inline refresh: the user is
// waiting on those, and the hooks read the result on the next line.
package app

import (
	"time"

	"github.com/rohanthewiz/ced/internal/crash"
)

// gitLogLoadedEvent carries an off-loop git log reload.
type gitLogLoadedEvent struct {
	when      time.Time
	seq       int
	scope     string
	toplevel  string
	commits   []gitLogCommit
	truncated bool
	// scopeGone: the list was empty because the viewed ref no longer
	// exists; the handler falls back to the current branch (on the loop,
	// where the flash and the scope change belong) and asks again.
	scopeGone bool
}

// When satisfies the tcell.Event interface.
func (e *gitLogLoadedEvent) When() time.Time { return e.when }

// conflictLoadedEvent carries an off-loop Conflicts reload: the whole
// panel's data when it was open (full), else just the operation record.
type conflictLoadedEvent struct {
	when time.Time
	seq  int
	full bool
	data conflictPanelData
}

// When satisfies the tcell.Event interface.
func (e *conflictLoadedEvent) When() time.Time { return e.when }

// gitPanelsReloadAsync launches the tick's reloads for whichever of the
// two panels needs one — the conditions refreshGitLogCommits /
// refreshConflictPanel* check inline.
func (a *App) gitPanelsReloadAsync() {
	a.gitLogReloadAsync()
	switch {
	case a.conflictPanel.open:
		a.conflictReloadAsync(true)
	case a.gitOp != "" || a.conflictPanel.info.op != "":
		a.conflictReloadAsync(false)
	}
}

// gitLogReloadAsync is refreshGitLogCommits on a goroutine. Skipped, as
// inline, while the panel is shut or a filter's results are showing.
func (a *App) gitLogReloadAsync() {
	if !a.gitLog.open || a.gitLog.filter.applied != "" {
		return
	}
	a.gitLog.loadSeq++
	seq, root, scope, scr := a.gitLog.loadSeq, a.rootDir, a.gitLog.scope, a.screen
	go func() {
		defer crash.Recover()
		e := &gitLogLoadedEvent{when: time.Now(), seq: seq, scope: scope, toplevel: gitToplevel(root)}
		e.commits, e.truncated = loadGitLogCommits(root, scope)
		if len(e.commits) == 0 && gitLogRefGone(root, scope) {
			e.scopeGone = true
		}
		postResult(scr, e)
	}()
}

// handleGitLogLoaded applies an off-loop log reload if it is still the
// newest and the panel still shows what it was asked for.
func (a *App) handleGitLogLoaded(e *gitLogLoadedEvent) {
	if e.seq != a.gitLog.loadSeq || !a.gitLog.open || a.gitLog.filter.applied != "" || e.scope != a.gitLog.scope {
		return
	}
	if e.scopeGone {
		// gitLogScopeGone's answer, spoken on the loop: back to the
		// current branch, say so, and load that.
		if a.gitLogScopeGone() {
			a.gitLogReloadAsync()
		}
		return
	}
	a.gitLog.toplevel = e.toplevel
	a.applyGitLogCommits(e.commits, e.truncated)
}

// conflictReloadAsync is refreshConflictPanel (full) or
// refreshConflictPanelInfo on a goroutine.
func (a *App) conflictReloadAsync(full bool) {
	a.conflictPanel.loadSeq++
	seq, root, gitDir, prev, scr := a.conflictPanel.loadSeq, a.rootDir, a.gitDir, a.conflictPanel.info, a.screen
	go func() {
		defer crash.Recover()
		var d conflictPanelData
		if full {
			d = loadConflictPanelData(root, gitDir, prev)
		} else {
			if gitDir == "" {
				gitDir = gitAbsoluteDir(root)
			}
			d.info = reloadGitOpInfo(root, gitDir, gitOpFromGitDir(gitDir), prev)
		}
		postResult(scr, &conflictLoadedEvent{when: time.Now(), seq: seq, full: full, data: d})
	}()
}

// handleConflictLoaded applies an off-loop Conflicts reload if it is the
// newest. A full reload whose panel has closed meanwhile keeps only the
// operation record, which is what a shut panel maintains anyway.
func (a *App) handleConflictLoaded(e *conflictLoadedEvent) {
	if e.seq != a.conflictPanel.loadSeq {
		return
	}
	if e.full && a.conflictPanel.open {
		a.applyConflictPanelData(e.data)
		return
	}
	a.conflictPanel.info = e.data.info
}
