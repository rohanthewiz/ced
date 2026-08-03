// =============================================================================
// File: internal/app/folder.go
// Author: Rohan Allison <rohanthewiz@gmail.com>
// Created: 2026-08-03
// Copyright: 2026 Rohan Allison. All rights reserved.
// =============================================================================

// folder.go is the workspace layer: opening a different project folder,
// the recent-folders list, and restoring a folder's tabs and cursor
// positions when you come back to it.
//
// WHY A ROOT SWITCH IS A RESTART, NOT A FIELD ASSIGNMENT. rootDir itself
// is touched in a handful of places, but everything DERIVED from it is
// the actual cost: the file tree, the finder's index, git status, the
// git panels, gopls's rootUri (fixed at initialize — it needs a server
// restart), the ACP session cwd, MCP's roots/list, plugin working
// directories, and the compare panel's two sides. Reassigning the field
// would leave every one of those pointing at the old project, and
// re-deriving them one by one is the same work as New() with a bug
// budget attached. So a switch parks the new root on App.nextRoot,
// asks the loop to exit, and main tears the App down and calls
// New(newRoot). One code path builds a workspace, and it's the one that
// runs at startup — the path that gets exercised every single launch.
//
// The screen blinks once on the way through. That is the whole price.

package app

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/rohanthewiz/ced/internal/editor"
	"github.com/rohanthewiz/ced/internal/session"
	"github.com/rohanthewiz/ced/internal/userconfig"
)

// sessionStatePathFn and sessionConfigPathFn resolve the two files this
// feature writes. They're package vars purely so tests can point them at
// a temp directory: the state file is rewritten on every Close and every
// folder switch, so without the seam a test run would scribble over the
// developer's real recent-folders list.
var (
	sessionStatePathFn  = userconfig.StatePath
	sessionConfigPathFn = userconfig.DefaultPath
)

// NextRoot reports the folder the user asked to switch to, or "" when
// the editor exited for good. main reads it after Run returns and, when
// set, builds a fresh App rooted there. Exported because the restart
// lives outside the package — the App cannot rebuild itself, which is
// the entire point of doing it this way.
func (a *App) NextRoot() string { return a.nextRoot }

// -----------------------------------------------------------------------------
// The store
// -----------------------------------------------------------------------------

// loadSessionStore reads state.json and marks the current root as the
// most recently opened folder, persisting that immediately.
//
// Recording the visit at STARTUP rather than only at exit is deliberate:
// it's what makes `ced --last` and the recent list correct even if this
// run ends in a crash or a kill -9. A session that dies unexpectedly
// then costs its tab list — which was never written — but not the fact
// that the folder was opened at all.
//
// A broken state file flashes and is otherwise ignored: it holds
// convenience, never the user's work, so it must never stop a start.
func (a *App) loadSessionStore() {
	path := sessionStatePathFn()
	st, err := session.Load(path)
	if err != nil {
		a.flash("session: " + err.Error())
	}
	a.sessionStore = st
	st.Touch(a.rootDir)
	a.saveSessionStore()
}

// saveSessionStore writes the store back to disk, flashing on failure.
// Errors here are worth saying out loud — silently failing to remember a
// folder looks like the feature doesn't work rather than like the config
// directory is read-only.
func (a *App) saveSessionStore() {
	if a.sessionStore == nil {
		return
	}
	if err := a.sessionStore.Save(sessionStatePathFn()); err != nil {
		a.flash("session: " + err.Error())
	}
}

// recordSession captures the open tabs and their cursors into the store
// and writes it out. Called from Close, so it runs on both exits — a
// plain quit and a folder switch — without either path having to
// remember.
//
// Untitled tabs and image previews are skipped by trimTabs / this loop
// respectively: neither has a file to reopen, and an image tab restored
// into a text session would be a surprise, not a convenience.
func (a *App) recordSession() {
	if a.sessionStore == nil {
		return
	}
	e := session.Entry{Root: a.rootDir}
	active := 0
	for i, t := range a.tabs {
		if t == nil || t.Path == "" {
			continue
		}
		if i == a.activeTab {
			active = len(e.Tabs)
		}
		e.Tabs = append(e.Tabs, session.TabState{
			Path:    t.Path,
			Line:    t.Cursor.Line,
			Col:     t.Cursor.Col,
			ScrollY: t.ScrollY,
			ScrollX: t.ScrollX,
		})
	}
	e.Active = active
	a.sessionStore.Record(e)
	a.saveSessionStore()
}

// restoreSession reopens the tabs this folder had when it was last
// closed, with their cursors and scroll offsets. Called from New, before
// any file named on the command line is opened, so an explicit `ced
// foo.go` still lands on foo.go as the active tab.
//
// Everything here degrades quietly. A file that has since been deleted,
// grown past the open guards, or turned binary is skipped without a
// word: the user asked to open a FOLDER, and a wall of "could not
// reopen" messages about files they may not even remember having open is
// noise, not information. What they see instead is the tabs that did
// come back.
func (a *App) restoreSession() {
	if !a.sessionEnabled || a.sessionStore == nil {
		return
	}
	e, ok := a.sessionStore.Find(a.rootDir)
	if !ok || len(e.Tabs) == 0 {
		return
	}
	active, restored := -1, 0
	for i, ts := range e.Tabs {
		// Existence is checked HERE rather than left to NewTab, which
		// deliberately succeeds on a missing path — that's the `ced
		// foo.go` new-file intent, and it's right for an explicit open.
		// A restore is the opposite gesture: nobody asked to resurrect a
		// file they deleted, and an empty buffer wearing its name is the
		// worst possible way to say it's gone.
		if info, err := os.Stat(ts.Path); err != nil || info.IsDir() {
			continue
		}
		t, err := editor.NewTab(ts.Path)
		if err != nil {
			continue
		}
		a.wireTab(t)
		// RestoreView, not MoveCursorTo: the stored SCROLL is part of
		// what's being put back, and every other cursor write sets
		// cursorMoved so the next Render scrolls the cursor into view
		// instead. Same argument the Find-all popup's Esc path makes.
		pos := editor.Position{Line: ts.Line, Col: ts.Col}
		t.RestoreView(pos, pos, ts.ScrollY, ts.ScrollX)
		a.tabs = append(a.tabs, t)
		restored++
		// The active index is captured AFTER the append, against the tab
		// list rather than the stored one: files that failed to reopen
		// left gaps, so the two lists no longer line up.
		if i == e.Active {
			active = len(a.tabs) - 1
		}
		a.announceTab(t)
	}
	if restored == 0 {
		return
	}
	// The recorded active tab may itself be one of the files that didn't
	// come back; the last one that did is the closest thing to where the
	// user was.
	if active < 0 {
		active = len(a.tabs) - 1
	}
	a.activeTab = active
	// The active folder drives where "New file" lands, so point it at the
	// file the user was last looking at rather than leaving it on the
	// project root.
	a.setActiveFolder(filepath.Dir(a.tabs[active].Path))
	// One quiet line, and no advice about turning it off: this fires on
	// every launch, and a flash that explains itself every time is the
	// nagging the rest of the editor deliberately avoids.
	noun := "tabs"
	if restored == 1 {
		noun = "tab"
	}
	a.flash(fmt.Sprintf("Restored %d %s", restored, noun))
}

// -----------------------------------------------------------------------------
// Switching folders
// -----------------------------------------------------------------------------

// requestOpenFolder validates a folder path and asks the process to
// restart rooted there. Dirty buffers gate the switch through the same
// unsaved-changes modal quitting uses — a folder switch discards the
// whole workspace, so it owes the user exactly the warning an exit does.
func (a *App) requestOpenFolder(path string) {
	root, err := a.resolveFolder(path)
	if err != nil {
		a.flash(err.Error())
		return
	}
	// Compare through the store's normaliser, not raw strings: rootDir
	// is whatever spelling ced was launched with, and /tmp/proj vs
	// /private/tmp/proj is the same folder wearing two names.
	if session.Normalize(root) == session.Normalize(a.rootDir) {
		a.flash("Already in " + displayPath(root))
		return
	}
	switchTo := func(app *App) {
		app.nextRoot = root
		app.quit = true
	}
	dirty := a.dirtyTabCount()
	if dirty == 0 {
		switchTo(a)
		return
	}
	msg := fmt.Sprintf("%d files have unsaved changes. Save before opening %s?",
		dirty, filepath.Base(root))
	if dirty == 1 {
		msg = "1 file has unsaved changes. Save before opening " +
			filepath.Base(root) + "?"
	}
	a.openDirtyClose(
		"Unsaved changes",
		msg,
		func(app *App) {
			// Only switch when every save landed — a half-saved switch
			// would throw away work on whichever tab failed, and the
			// workspace it belonged to goes with it.
			if app.saveAllDirty() {
				switchTo(app)
			}
		},
		switchTo,
	)
}

// resolveFolder turns whatever the user typed into an absolute directory
// path, or explains why it isn't one.
//
// Relative paths resolve against rootDir, NOT the process working
// directory. That looks pedantic until you remember the embedded grsh
// terminal's `cd` chdirs the whole editor process by design — so "the
// current directory" is a moving target the user can't see, while the
// project root is on screen in the title bar.
func (a *App) resolveFolder(path string) (string, error) {
	p := strings.TrimSpace(path)
	if p == "" {
		return "", fmt.Errorf("no folder given")
	}
	p = expandHome(p)
	if !filepath.IsAbs(p) {
		p = filepath.Join(a.rootDir, p)
	}
	p = filepath.Clean(p)
	info, err := os.Stat(p)
	if err != nil {
		return "", fmt.Errorf("cannot open %s: %v", displayPath(p), err)
	}
	if !info.IsDir() {
		return "", fmt.Errorf("%s is a file, not a folder", displayPath(p))
	}
	return p, nil
}

// expandHome rewrites a leading ~ (bare, or ~/…) to the user's home
// directory. Only the leading form — "~user" is a shell feature that
// needs a passwd lookup, and getting it half-right would be worse than
// not offering it.
func expandHome(p string) string {
	if p != "~" && !strings.HasPrefix(p, "~/") {
		return p
	}
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return p
	}
	if p == "~" {
		return home
	}
	return filepath.Join(home, p[2:])
}

// displayPath is expandHome's inverse for UI text: a path under the home
// directory renders with a leading ~. Picker rows and flashes are read at
// a glance, and "/Users/somebody/projs/go/ced" spends a third of its
// width saying something the user already knows.
func displayPath(p string) string {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return p
	}
	if p == home {
		return "~"
	}
	if strings.HasPrefix(p, home+string(filepath.Separator)) {
		return "~" + p[len(home):]
	}
	return p
}

// -----------------------------------------------------------------------------
// Menu surfaces
// -----------------------------------------------------------------------------

// menuOpenFolder prompts for a folder to open. The field is pre-filled
// with the current root so the common case — a sibling project — is an
// edit rather than a retype.
func (a *App) menuOpenFolder() {
	a.closeMenu()
	a.openPrompt(
		"Open folder",
		// Kept under 50 columns: the prompt modal is 54 wide and draws
		// its hint at +2 without wrapping or clipping, so a longer
		// string paints straight through the right border.
		"absolute, ~/path, or relative to the root",
		displayPath(a.rootDir),
		func(app *App, value string) { app.requestOpenFolder(value) },
	)
}

// menuRecentFolders offers the folders this editor has been opened in,
// most recent first, as a fuzzy picker (the house rule for every
// choose-one-from-a-list UI).
//
// The current root is excluded rather than annotated, unlike the theme
// picker: re-picking a theme is how a user reverts a preview, but
// re-picking the folder you're already in would tear the workspace down
// and rebuild it identically. There is nothing to revert to.
//
// Folders that have since been deleted are PRUNED here, not dimmed. A
// list of places you can't go is worse than a shorter list, and the walk
// is the only moment ced is in a position to notice.
func (a *App) menuRecentFolders() {
	a.closeMenu()
	if a.sessionStore == nil {
		a.flash("No recent folders recorded yet")
		return
	}
	var items []paletteItem
	pruned := false
	cur := session.Normalize(a.rootDir)
	for _, root := range a.sessionStore.Recent() {
		info, err := os.Stat(root)
		if err != nil || !info.IsDir() {
			a.sessionStore.Remove(root)
			pruned = true
			continue
		}
		if root == cur {
			continue
		}
		root := root // capture per-iteration for the closure
		label := displayPath(root)
		if e, ok := a.sessionStore.Find(root); ok && len(e.Tabs) > 0 {
			label = fmt.Sprintf("%s  (%d tabs)", label, len(e.Tabs))
		}
		items = append(items, paletteItem{
			label: label,
			run:   func(app *App) { app.requestOpenFolder(root) },
		})
	}
	if pruned {
		a.saveSessionStore()
	}
	if len(items) == 0 {
		a.flash("No other recent folders — use ≡ → File → Open folder…")
		return
	}
	a.openPicker("Recent folders", items)
}

// menuToggleSession flips whether opening a folder reopens its tabs, and
// persists the choice. The folder is still RECORDED with the preference
// off — the recent-folders list reads the same file, and a user who
// wants a blank editor hasn't asked to forget where they've been.
func (a *App) menuToggleSession() {
	a.closeMenu()
	a.sessionEnabled = !a.sessionEnabled
	if a.sessionEnabled {
		a.flash("Session restore on — reopening a folder restores its tabs")
	} else {
		a.flash("Session restore off — folders open with no tabs")
	}
	if err := userconfig.SaveSession(sessionConfigPathFn(), a.sessionEnabled); err != nil {
		a.flash("config: " + err.Error())
	}
}

// sessionToggleLabel is the dynamic menu label for the restore row —
// the toggle-in-place pattern, so the row always names the action it
// will perform rather than the state it's in.
func (a *App) sessionToggleLabel() string {
	if a.sessionEnabled {
		return "Disable session restore"
	}
	return "Enable session restore"
}

// hasRecentFolders gates the Recent folders row: with nothing but the
// current folder on record there is no list to show.
func (a *App) hasRecentFolders() bool {
	if a.sessionStore == nil {
		return false
	}
	cur := session.Normalize(a.rootDir)
	for _, root := range a.sessionStore.Recent() {
		if root != cur {
			return true
		}
	}
	return false
}
