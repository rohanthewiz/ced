// =============================================================================
// File: internal/app/recentlocations.go
// Author: Rohan Allison <rohanthewiz@gmail.com>
// Created: 2026-09-28
// Copyright: 2026 Rohan Allison. All rights reserved.
// =============================================================================

// Recent locations: this repository's folders, by how recently and how
// often you work in them — and the per-repo history store behind them and
// behind the recent-files ring (internal/history).
//
// EVERY LEVEL IS THE SAME TWO SECTIONS: the five most recently used
// folders, a thin spacer, then the ten most frequently used that the first
// section didn't already name. The top level is the repository's own
// folders; picking one that has used subfolders opens the same picker one
// level down, and so on for as many levels as there is history to show.
//
//	Recent locations                  Folders in internal
//	  internal  ›                       Reveal internal in tree
//	  ai_docs/plans                     app
//	  …5 recent                         …5 recent
//	  ┄┄┄┄┄┄┄┄┄┄┄┄┄┄┄┄┄┄┄┄┄┄┄┄┄┄┄       ┄┄┄┄┄┄┄┄┄┄┄┄┄┄┄┄┄┄┄┄┄┄┄┄┄┄┄
//	  …10 frequent                      …10 frequent
//
// A folder with nothing used below it has nothing to drill into, so
// picking it REVEALS it in the tree. The `›` says which rows drill. A
// drill-in's first row reveals the folder itself, so drilling costs
// nothing when the folder was what you wanted after all.
//
// It reveals and never re-roots, the favorites rule: every folder here is
// inside the workspace, and re-rooting at `internal/app` would throw away
// git, gopls and the finder index. Switching PROJECTS is still ≡ File →
// Recent folders…, which this deliberately leaves alone.
//
// A USE is a file opened in a folder in a NEW tab (openFile's new-tab
// branch — tab switching is not work in another place), or a folder picked
// here. The rankings, and why they are cheap, are in
// internal/history/index.go; the database, and why two editors on one
// repository add to each other's counts, in internal/history/db.go.

package app

import (
	"errors"
	"os"
	"path/filepath"

	"github.com/rohanthewiz/bytdb"
	"github.com/rohanthewiz/ced/internal/history"
	"github.com/rohanthewiz/ced/internal/session"
)

const (
	// locationsRecent is the recent section's length at every level.
	locationsRecent = 5
	// locationsFrequent is the frequent section's length at every level.
	locationsFrequent = 10
	// locationsMaxRows caps the picker's requested height: both sections,
	// the spacer, and a drill-in's reveal row.
	locationsMaxRows = 20
)

// historyPathFn resolves a repository's history database. A package var
// for the reason every config seam here is one: newTestApp points it at a
// temp dir, so no test run can write a .ced/ into whatever directory it
// happened to root an App at.
var historyPathFn = history.DBPath

// repoHistory returns this workspace's history, loading it on first use.
// Never nil: a failed load is an empty history (and a flash, unless the
// file was merely locked by a sibling for longer than the retry window —
// a moment's contention, not a problem to report).
func (a *App) repoHistory() *history.History {
	if a.history == nil {
		h, err := history.Load(a.rootDir, historyPathFn(a.rootDir))
		if err != nil && !errors.Is(err, bytdb.ErrLocked) {
			a.flash("history: " + err.Error())
		}
		a.history = h
	}
	return a.history
}

// writeHistory adds the pending history to the repository's database.
// Silent on failure: the changes stay pending for the next write, and the
// one guaranteed write (Close) has no screen left to flash on. Nothing is
// written — and no .ced/ created — while nothing is pending.
func (a *App) writeHistory() {
	if a.history == nil {
		return
	}
	_ = a.history.Write(historyPathFn(a.rootDir), a.recentFiles)
}

// noteFolderUse records a use of dir. Only folders strictly inside the
// workspace are recorded — a folder outside it belongs to some other
// repository's history, and the root itself is the top of every list
// rather than an entry in one. Memory only; the database is written on
// Close, so opening files costs no IO here.
func (a *App) noteFolderUse(dir string) {
	h := a.repoHistory()
	if dir == "" || !h.Inside(dir) {
		return
	}
	h.Folders.Record(dir)
}

// hasRecentLocations gates the ≡ row: is there anywhere to list? A bound
// check on the root's children, cheap enough for a predicate the menu
// runs every frame.
func (a *App) hasRecentLocations() bool {
	return a.repoHistory().Folders.HasUsesBelow(a.rootDir)
}

// menuRecentLocations is the ≡ Nav row: the top level of the picker.
func (a *App) menuRecentLocations() {
	a.closeMenu()
	a.openLocations(session.Normalize(a.rootDir))
}

// openLocations shows the used folders below dir in the two sections —
// the top level when dir is the workspace root, a drill-in otherwise.
func (a *App) openLocations(dir string) {
	idx := a.repoHistory().Folders
	pick := &locationPick{have: map[string]bool{}}
	recent := idx.Recent(dir, locationsRecent, pick.accept)
	frequent := idx.Frequent(dir, locationsFrequent, pick.accept)
	a.pruneGoneLocations(pick.gone)

	root := session.Normalize(a.rootDir)
	sections := a.locationSections(dir, recent, frequent)
	if len(sections) == 0 {
		if dir == root {
			a.flash("No recent locations yet — this list fills in as you open files")
			return
		}
		// The history said drill, the filesystem disagreed: everything
		// below is gone. Fall back to the folder itself.
		a.revealLocation(dir)
		return
	}
	title := "Recent locations"
	var items []paletteItem
	if dir != root {
		title = "Folders in " + a.locationLabel(dir)
		items = append(items, paletteItem{
			label: "Reveal " + a.locationLabel(dir) + " in tree",
			run:   func(app *App) { app.revealLocation(dir) },
		})
	}
	items = append(items, sections...)
	a.openPickerRows(title, items, locationRows(items), nil)
}

// locationPick is the veto both sections of one picker share. It stats
// each candidate (collecting the ones gone from disk, which the caller
// prunes AFTER the search — removing nodes mid-search would pull them out
// from under the heap) and claims what it accepts, so the frequent
// section never repeats a recent row.
type locationPick struct {
	have map[string]bool
	gone []string
}

// accept is the veto itself, the claimed set before the stat.
func (f *locationPick) accept(p string) bool {
	if f.have[p] {
		return false
	}
	if info, err := os.Stat(p); err != nil || !info.IsDir() {
		f.gone = append(f.gone, p)
		return false
	}
	f.have[p] = true
	return true
}

// pruneGoneLocations forgets folders the search found missing from disk.
// Memory only, like every other use; Close writes it.
func (a *App) pruneGoneLocations(gone []string) {
	for _, p := range gone {
		a.repoHistory().Folders.Remove(p)
	}
}

// locationSections builds the two sections as picker rows: recent, a
// spacer (only when both have rows — a divider with nothing on one side
// divides nothing), frequent. Rows are labelled relative to the folder
// being listed: its path is in the title already, and repeating it on
// every row would spend the width the distinguishing tail needs.
func (a *App) locationSections(under string, recent, frequent []string) []paletteItem {
	idx := a.repoHistory().Folders
	row := func(dir string) paletteItem {
		label, err := filepath.Rel(under, dir)
		if err != nil {
			label = dir
		}
		if idx.HasUsesBelow(dir) {
			label += "  ›"
		}
		return paletteItem{label: label, run: func(app *App) { app.pickLocation(dir) }}
	}
	items := make([]paletteItem, 0, len(recent)+len(frequent)+1)
	for _, d := range recent {
		items = append(items, row(d))
	}
	if len(recent) > 0 && len(frequent) > 0 {
		items = append(items, paletteSpacer())
	}
	for _, d := range frequent {
		items = append(items, row(d))
	}
	return items
}

// locationRows is the height to ask for: the whole list when it fits under
// the cap, and never shorter than an ordinary picker.
func locationRows(items []paletteItem) int {
	return max(min(len(items), locationsMaxRows), paletteResultsVisible)
}

// locationLabel is a folder's name relative to the workspace root.
func (a *App) locationLabel(dir string) string {
	if rel, err := filepath.Rel(session.Normalize(a.rootDir), dir); err == nil {
		return rel
	}
	return displayPath(dir)
}

// pickLocation is what choosing a row does: drill in when there is
// something below, reveal otherwise.
func (a *App) pickLocation(dir string) {
	if a.repoHistory().Folders.HasUsesBelow(dir) {
		a.openLocations(dir)
		return
	}
	a.revealLocation(dir)
}

// revealLocation shows dir in the file tree and counts the visit as a use.
// The path is re-spelled under rootDir: the index holds RESOLVED paths
// (/private/var/… on macOS) while the tree is rooted at the path as typed,
// and Reveal refuses anything outside that spelling.
func (a *App) revealLocation(dir string) {
	a.noteFolderUse(dir)
	target := dir
	if rel, err := filepath.Rel(session.Normalize(a.rootDir), dir); err == nil {
		target = filepath.Join(a.rootDir, rel)
	}
	a.RevealPath(target)
}
