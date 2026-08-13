// =============================================================================
// File: internal/app/recentfiles.go
// Author: Rohan Allison <rohanthewiz@gmail.com>
// Created: 2026-08-13
// Copyright: 2026 Rohan Allison. All rights reserved.
// =============================================================================

// The recent-files picker (ai_docs/cats-native-plan.md Phase 3.4): an MRU
// ring of every file this editor made active in this folder, offered as a
// fuzzy picker on Esc-B, ≡ → Tab → Recent files…, and ⌘E at Tier 1.
//
// WHY THE TAB SWITCHER IS NOT ENOUGH. Esc-b already lists the open tabs,
// and this list deliberately overlaps it — but the rows worth having are
// the ones the switcher CANNOT show: the file you closed twenty minutes
// ago, and the file you were in before this root was reopened. Those are
// the only ones that would otherwise cost a fuzzy-find through the whole
// project to get back to.
//
// THE ORDER IS THE FEATURE, and it is why this is not just "files sorted
// by mtime". The head of the ring is the file you are looking at, so the
// FIRST row of the picker is the one you were in before it — press the
// chord, press Enter, and you are back. That two-file flip is what ⌘E
// means to a hand trained in VS Code or GoLand, and it falls out of the
// ring for free as long as every activation touches it.
//
// WHERE IT IS TOUCHED, and where it deliberately is not:
//
//	openFile      both branches — a new tab, and a click onto one already open
//	switchToTab   the single funnel every tab-switching surface goes through
//	restoreSession the tab session restore lands on
//	closeTab      NO. Closing a tab makes a neighbour active without anyone
//	              having navigated to it, and quitting closes every tab in
//	              turn — hooking it would rewrite the ring in reverse close
//	              order at the exact moment recordSession writes it to disk,
//	              so the list you'd get back is the one nobody visited.
//
// The ring is PER FOLDER and lives in state.json beside that folder's tab
// list (internal/session), for the same reason the tab list is per folder:
// "the last file I had open" is a question about a project, and a global
// list would answer it with somebody else's project. It rides the same
// durability story too — written on Close, so a kill -9 costs the ring
// exactly as it costs the tab list.
//
// It is NOT gated on the session-restore preference. That toggle governs
// whether a folder reopens its tabs; a user who wants a blank editor has
// not asked the editor to forget where they have been — the same line
// folder.go's recent-folders list already draws.

package app

import (
	"os"
	"path/filepath"

	"github.com/rohanthewiz/ced/internal/session"
)

// touchRecentFile moves path to the head of the ring. Called wherever a
// tab becomes the active one; untitled tabs pass "" and are ignored,
// since there is nothing for a picker row to reopen.
func (a *App) touchRecentFile(path string) {
	a.recentFiles = session.TouchRecent(a.recentFiles, path, session.MaxRecentFiles)
}

// loadRecentFiles seeds the ring from this folder's stored entry. Called
// from loadSessionStore, i.e. before restoreSession — so the tabs coming
// back touch the ring on TOP of what was remembered, and a restored
// session's active file is the head exactly as it was at exit.
//
// The stored slice is copied rather than aliased: the store's entry is
// handed out by value but its slice header is not, and the ring is
// rewritten in place on every prune.
func (a *App) loadRecentFiles() {
	if a.sessionStore == nil {
		return
	}
	e, ok := a.sessionStore.Find(a.rootDir)
	if !ok || len(e.Recent) == 0 {
		return
	}
	a.recentFiles = append([]string(nil), e.Recent...)
}

// menuRecentFiles opens the picker.
//
// The current file is excluded rather than listed and skipped over: it is
// the ring's head, so it would otherwise be row one — the row a hand
// reaching for the two-file flip lands on — and picking it does nothing.
// This is the same call the tab switcher makes for the same reason.
//
// Files that have since been deleted or moved are PRUNED, not dimmed
// (folder.go's rule for recent folders, and the picker is the only moment
// ced is in a position to notice). The prune is up to MaxRecentFiles
// stats on a keystroke — sub-millisecond against a warm filesystem, and
// the alternative is a picker that offers rows that flash an error. It
// mutates only the in-memory ring: the write happens at Close like every
// other session change, and a ring pruned twice costs nothing.
func (a *App) menuRecentFiles() {
	a.closeMenu()
	cur := ""
	if t := a.activeTabPtr(); t != nil {
		cur = t.Path
	}
	items := make([]paletteItem, 0, len(a.recentFiles))
	kept := a.recentFiles[:0]
	for _, p := range a.recentFiles {
		if info, err := os.Stat(p); err != nil || info.IsDir() {
			continue
		}
		kept = append(kept, p)
		if p == cur {
			continue
		}
		p := p // capture per-iteration for the closure
		items = append(items, paletteItem{
			label: a.recentFileLabel(p),
			// openFile, not switchToTab: it switches to the tab when the
			// file is already open and opens one when it isn't, which is
			// the whole difference between this picker and the switcher.
			run: func(app *App) { app.openFile(p) },
		})
	}
	a.recentFiles = kept
	if len(items) == 0 {
		a.flash("No other recent files yet — this list fills in as you open them")
		return
	}
	title := "Recent files"
	if t := a.activeTabPtr(); t != nil {
		title += " (current: " + t.DisplayName() + ")"
	}
	a.openPicker(title, items)
}

// recentFileLabel renders one row. Open files are rendered by exactly the
// switcher's rules — dirty dot, name, directory relative to the root —
// because they ARE switcher rows here, and two pickers that disagree
// about how a file looks would read as two different lists of files.
//
// A file outside the root gets its directory spelled out instead. The
// switcher can leave that blank (a "../../.." chain is noise for a file
// you deliberately opened) but this list is walked rather than read, and
// the entries that get here from outside the project — a jump into a
// dependency via go-to-definition — are exactly the ones whose basename
// is `client.go` for the fourth time.
func (a *App) recentFileLabel(path string) string {
	name, dirty := filepath.Base(path), false
	if t := a.tabForPath(path); t != nil {
		name, dirty = t.DisplayName(), t.Dirty
	}
	label := a.tabPickerLabel(name, path, dirty)
	if a.tabPickerDir(path) == "" {
		if dir := filepath.Dir(path); dir != a.rootDir {
			label += "  " + displayPath(dir)
		}
	}
	return label
}

// hasRecentFiles gates the ≡ row: with nothing on record but the file
// already on screen there is no list to show.
//
// It deliberately does NOT stat, unlike the picker it gates: menu
// predicates run on every draw of an open menu, and fifty stats per frame
// to grey out one row is not a trade worth making. The cost is that the
// row can be enabled when every remaining entry has been deleted since —
// in which case the picker says so in a flash, which is where that
// sentence belongs anyway.
func (a *App) hasRecentFiles() bool {
	cur := ""
	if t := a.activeTabPtr(); t != nil {
		cur = t.Path
	}
	for _, p := range a.recentFiles {
		if p != cur {
			return true
		}
	}
	return false
}
