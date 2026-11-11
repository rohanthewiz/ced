// =============================================================================
// File: internal/app/tickpace_test.go
// Author: Rohan Allison <rohanthewiz@gmail.com>
// Created: 2026-11-10
// Copyright: 2026 Rohan Allison. All rights reserved.
// =============================================================================

// Tests for tickpace.go: the refresh tick slows while the pane is
// unfocused and refreshes at once on return.

package app

import (
	"path/filepath"
	"testing"

	"github.com/gdamore/tcell/v2"
)

// treeShows reports whether the file tree's visible rows include path.
func treeShows(a *App, path string) bool {
	for _, n := range a.tree.VisibleNodes() {
		if n.Path == path {
			return true
		}
	}
	return false
}

// tickingApp is a newTestApp with the real tick running at the normal
// pace (newTestApp itself starts none), stopped at cleanup.
func tickingApp(t *testing.T, root string) *App {
	t.Helper()
	a := newTestApp(t, root)
	a.startTreeRefresh()
	t.Cleanup(a.stopTreeRefresh)
	return a
}

// TestTreeRefreshOnFocusChange_SlowsAndRestores: focus out moves the tick
// to the unfocused pace, focus in back to the normal one, and a repeated
// report changes nothing.
func TestTreeRefreshOnFocusChange_SlowsAndRestores(t *testing.T) {
	a := tickingApp(t, t.TempDir())
	if a.treeRefreshEvery != treeRefreshInterval {
		t.Fatalf("start pace = %v, want %v", a.treeRefreshEvery, treeRefreshInterval)
	}

	a.handleEvent(tcell.NewEventFocus(false))
	if !a.unfocused || a.treeRefreshEvery != treeRefreshUnfocusedInterval {
		t.Fatalf("after focus out: unfocused=%v pace=%v, want true / %v",
			a.unfocused, a.treeRefreshEvery, treeRefreshUnfocusedInterval)
	}
	stop := a.treeRefreshStop
	a.handleEvent(tcell.NewEventFocus(false))
	if a.treeRefreshStop != stop {
		t.Error("a repeated focus-out restarted the tick")
	}

	a.handleEvent(tcell.NewEventFocus(true))
	if a.unfocused || a.treeRefreshEvery != treeRefreshInterval || a.treeRefreshStop == nil {
		t.Fatalf("after focus in: unfocused=%v pace=%v running=%v, want false / %v / true",
			a.unfocused, a.treeRefreshEvery, a.treeRefreshStop != nil, treeRefreshInterval)
	}
}

// TestTreeRefreshOnFocusChange_FocusInRefreshes: coming back runs the
// tick's pipeline at once — a file made while away is in the tree and in
// the git dirty set without waiting a period. A focus-in with no
// focus-out before it (cats' startup seed) refreshes nothing.
func TestTreeRefreshOnFocusChange_FocusInRefreshes(t *testing.T) {
	requireGit(t)
	repo := initRepo(t)
	writeCommit(t, repo, "f.txt", "one\n", "init")
	a := tickingApp(t, repo)

	seeded := filepath.Join(repo, "seeded.txt")
	writeFileT(t, seeded, "s\n")
	a.handleEvent(tcell.NewEventFocus(true))
	if treeShows(a, seeded) {
		t.Fatal("a focus-in without a focus-out refreshed the tree")
	}

	a.handleEvent(tcell.NewEventFocus(false))
	away := filepath.Join(repo, "made-while-away.txt")
	writeFileT(t, away, "a\n")
	a.handleEvent(tcell.NewEventFocus(true))
	if !treeShows(a, away) {
		t.Error("focus-in did not refresh the tree")
	}
	if !a.tree.DirtyFiles[away] {
		t.Errorf("focus-in did not refresh git status: %v", sortedKeys(a.tree.DirtyFiles))
	}
}

// TestNoteUserPresent_KeystrokeCountsAsFocusIn: keys only reach a focused
// pane, so a key after a focus-out (whose focus-in was lost) restores the
// normal pace and refreshes, rather than leaving the tick slow for good.
func TestNoteUserPresent_KeystrokeCountsAsFocusIn(t *testing.T) {
	root := t.TempDir()
	a := tickingApp(t, root)
	a.handleEvent(tcell.NewEventFocus(false))
	made := filepath.Join(root, "made.txt")
	writeFileT(t, made, "m\n")

	a.handleEvent(tcell.NewEventKey(tcell.KeyRight, 0, tcell.ModNone))
	if a.unfocused || a.treeRefreshEvery != treeRefreshInterval {
		t.Fatalf("after a key: unfocused=%v pace=%v, want false / %v", a.unfocused, a.treeRefreshEvery, treeRefreshInterval)
	}
	if !treeShows(a, made) {
		t.Error("the implied focus-in did not refresh")
	}
}

// TestNoteUserPresent_ClickCountsButMotionDoesNot: a button press is the
// same proof as a key; pointer motion is not (an unfocused window can
// still be hovered).
func TestNoteUserPresent_ClickCountsButMotionDoesNot(t *testing.T) {
	a := tickingApp(t, t.TempDir())
	a.handleEvent(tcell.NewEventFocus(false))

	a.handleEvent(tcell.NewEventMouse(5, 5, tcell.ButtonNone, tcell.ModNone))
	if !a.unfocused {
		t.Fatal("pointer motion counted as focus")
	}
	a.handleEvent(tcell.NewEventMouse(5, 5, tcell.Button1, tcell.ModNone))
	if a.unfocused {
		t.Error("a click did not count as focus")
	}
}

// TestPaceTreeRefresh_NeverStartsATick: with no tick running (a directly
// built App, or after Close) focus changes record state but start
// nothing — focus is no reason to begin polling.
func TestPaceTreeRefresh_NeverStartsATick(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	a.handleEvent(tcell.NewEventFocus(false))
	if a.treeRefreshStop != nil || a.treeRefreshEvery != 0 {
		t.Fatalf("focus-out started a tick: running=%v pace=%v", a.treeRefreshStop != nil, a.treeRefreshEvery)
	}
	if !a.unfocused {
		t.Error("focus-out not recorded")
	}
	a.handleEvent(tcell.NewEventFocus(true))
	if a.treeRefreshStop != nil {
		t.Error("focus-in started a tick")
	}
}

// TestStopTreeRefresh_ClearsThePace: a stopped tick reports no pace, so
// paceTreeRefresh's "already at that pace" check can't mistake a dead
// tick for a running one.
func TestStopTreeRefresh_ClearsThePace(t *testing.T) {
	a := tickingApp(t, t.TempDir())
	a.stopTreeRefresh()
	if a.treeRefreshEvery != 0 {
		t.Errorf("pace after stop = %v, want 0", a.treeRefreshEvery)
	}
}
