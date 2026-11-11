// =============================================================================
// File: internal/app/tickpace.go
// Author: Rohan Allison <rohanthewiz@gmail.com>
// Created: 2026-11-10
// Copyright: 2026 Rohan Allison. All rights reserved.
// =============================================================================

// tickpace.go paces the 10-second refresh tick by focus (N-127 part c).
//
// The tick (app.go's startTreeRefresh → refreshTreeNow) re-reads the
// tree, takes a git snapshot, rebuilds the finder index (`git ls-files`),
// reconciles open tabs with disk and asks for gutter diffs. It used to do
// that every 10 seconds forever, focused or not.
//
// WHY SLOWER, NOT STOPPED. A pane that has lost focus is very often still
// on screen: ced in a cats pane or a tmux split beside an agent that is
// writing files, the user's eyes on the agent, ced showing the new files
// appear and the open tab reload. Both hosts report per-pane focus (cats
// forwards CSI I / CSI O to the pane gaining / losing it; tmux does with
// `focus-events on`), so "unfocused" there means "the user is in the next
// pane", not "nobody is looking". Stopping the tick would freeze exactly
// that view, so it slows down instead — treeRefreshUnfocusedInterval —
// and keeps a watched pane at most half a minute behind.
//
// WHY A REFRESH ON RETURN. The common reason to come back is having done
// something elsewhere — `git checkout` in the shell pane, an agent
// finishing — and on return the editor should already show it rather
// than up to a full period later. Focus-in therefore runs refreshTreeNow
// at once and restarts the tick at the normal pace, period counted from
// now (that refresh just happened).
//
// That refresh can raise the dirty-tab conflict prompt (reconcile.go):
// the prompt the tick would have raised anyway, now as you return
// instead of half a minute later or, as before, while you were away.
//
// A KEYSTROKE OR A CLICK COUNTS AS FOCUS-IN when the last report said
// out: keys only reach a focused pane, so a lost or never-sent CSI I must
// not leave the tick on the slow pace for the rest of the session.
//
// Terminals that never report focus (macOS Terminal.app, tmux without
// `focus-events`) never send CSI O, so `unfocused` stays false and the
// tick keeps its 10-second pace — today's behaviour, unchanged.

package app

import "time"

// treeRefreshUnfocusedInterval is the tick's period while the terminal
// reports the pane unfocused. Three normal periods: a watched pane beside
// an agent stays within half a minute of the disk, and an unwatched one
// costs a third of the work.
const treeRefreshUnfocusedInterval = 30 * time.Second

// treeRefreshOnFocusChange is the focus report's second consumer (the
// first is autoSaveOnFocusChange). Out: slow the tick. In, after an out:
// refresh now and resume the normal pace. A report that repeats the
// current state does nothing — cats seeds a pane with its focus state
// when the program turns reporting on, so a startup "in" must not
// trigger a refresh the startup itself just did.
func (a *App) treeRefreshOnFocusChange(focused bool) {
	if !focused {
		if a.unfocused {
			return
		}
		a.unfocused = true
		a.paceTreeRefresh(treeRefreshUnfocusedInterval)
		return
	}
	if !a.unfocused {
		return
	}
	a.unfocused = false
	a.refreshTreeNow()
	a.paceTreeRefresh(treeRefreshInterval)
}

// noteUserPresent treats a keystroke or a click as proof of focus. Called
// from handleEvent before the key or press is handled; a no-op unless the
// last focus report was "out".
func (a *App) noteUserPresent() {
	if a.unfocused {
		a.treeRefreshOnFocusChange(true)
	}
}

// paceTreeRefresh restarts the running tick at a new period. It never
// STARTS one: no tick running means nothing was asked to poll (a directly
// built App in a test, or after Close), and focus is no reason to begin.
func (a *App) paceTreeRefresh(every time.Duration) {
	if a.treeRefreshStop == nil || a.treeRefreshEvery == every {
		return
	}
	a.stopTreeRefresh()
	a.startTreeRefreshEvery(every)
}
