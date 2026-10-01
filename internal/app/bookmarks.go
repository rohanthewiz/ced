// =============================================================================
// File: internal/app/bookmarks.go
// Author: Rohan Allison <rohanthewiz@gmail.com>
// Created: 2026-09-30
// Copyright: 2026 Rohan Allison. All rights reserved.
// =============================================================================

// bookmarks.go is LINE BOOKMARKS across the project: mark a line, walk
// the marks with Next / Previous, list them, jump back. The per-tab half
// — keeping a bookmark on its line while the buffer changes, and the
// gutter flag — lives in editor/bookmark.go; this file owns where the
// bookmarks are when their file is NOT open, the verbs, and persistence.
//
// # Two homes, never both
//
//	open tab ──closeTab──▶ a.bookmarks[path]  (parked: path → line + text)
//	   ▲                        │
//	   └──────wireTab───────────┘  (adopted: re-anchored by text)
//
// While a file is open its tab is the ONLY authority: the tab follows
// every edit, and a second copy kept beside it would be wrong after the
// first keystroke. When the tab closes, its bookmarks are parked here by
// path; when the file opens again, wireTab hands them back and the tab
// re-anchors each one by its text (the file may have changed on disk in
// between — a formatter, a pull, a discarded edit). The project-wide
// list is the union of the two, built on demand (allBookmarks).
//
// # Doors
//
// Every verb is a ≡ Nav row (palette included). There is no leader: the
// flat table is out of mnemonic letters, and the project's rule for a
// new verb is a ≡ row. The mouse doors are the editor's right-click menu
// (Add / Remove bookmark on the clicked line) and a DOUBLE-click on a
// line number. Double rather than single, because a single click in the
// gutter already places the caret (and opens a diagnostic's message on
// a marked line); a double-click there used to select the line's first
// word, which nobody aims at the line numbers to do.
//
// # Persistence
//
// In the repository's own history database (<repo>/.ced/history.bytdb,
// internal/history/bookmarks.go), beside the recent files and searches —
// what you were looking at in THIS checkout. One row per file, written
// with the same open-briefly, add-don't-overwrite discipline, so two
// editors on one repository keep each other's bookmarks. Written THROUGH
// on every change (saveBookmarks) as well as at Close, so a crash costs
// nothing, and independent of the "session" toggle: that switch is about
// reopening the tabs you left, while a bookmark is a note you made on
// purpose. Like the rest of the history it reaches disk only inside a git
// work tree (history.Persists); elsewhere bookmarks last for the session.
//
// Untitled buffers refuse a bookmark: a bookmark is kept by path, and a
// flag that vanished at the first restart would teach that bookmarks
// are unreliable.

package app

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/rohanthewiz/ced/internal/editor"
	"github.com/rohanthewiz/ced/internal/history"
)

// bookmarkLabelText is how much of a bookmarked line the picker shows.
const bookmarkLabelText = 60

// bookmarkRef is one bookmark in the project-wide view. tab is the open
// tab that owns it, nil for a parked one.
type bookmarkRef struct {
	tab  *editor.Tab
	path string
	line int
	text string
}

// allBookmarks is every bookmark in the project — live ones from open
// tabs, parked ones for closed files — sorted by path, then line. That
// order is what Next / Previous walk and what the picker lists: files
// read top to bottom, a project read file by file.
func (a *App) allBookmarks() []bookmarkRef {
	var out []bookmarkRef
	for _, t := range a.tabs {
		if t == nil || t.Path == "" {
			continue
		}
		for _, b := range t.Bookmarks() {
			out = append(out, bookmarkRef{tab: t, path: t.Path, line: b.Line, text: b.Text})
		}
	}
	for path, bms := range a.bookmarks {
		for _, b := range bms {
			out = append(out, bookmarkRef{path: path, line: b.Line, text: b.Text})
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].path != out[j].path {
			return out[i].path < out[j].path
		}
		return out[i].line < out[j].line
	})
	return out
}

// bookmarkTotal counts every bookmark in the project.
func (a *App) bookmarkTotal() int {
	n := 0
	for _, t := range a.tabs {
		if t != nil {
			n += t.BookmarkCount()
		}
	}
	for _, bms := range a.bookmarks {
		n += len(bms)
	}
	return n
}

// adoptBookmarks hands a newly opened tab the bookmarks parked for its
// path. Called from wireTab, the one place every user-opened tab passes
// through (openFile and session restore). The detached tabs a
// workspace edit or a project replace builds for files that are not
// open never get here, which is right: they edit the file on disk, and
// the parked bookmarks re-anchor against that when it is next opened.
func (a *App) adoptBookmarks(t *editor.Tab) {
	if t == nil || t.Path == "" {
		return
	}
	if bms, ok := a.bookmarks[t.Path]; ok {
		t.SetBookmarks(bms)
		delete(a.bookmarks, t.Path)
	}
}

// parkBookmarks takes a closing tab's bookmarks back into the parked
// map. Nothing is saved: the set did not change, only where it lives.
func (a *App) parkBookmarks(t *editor.Tab) {
	if t == nil || t.Path == "" || t.BookmarkCount() == 0 {
		return
	}
	if a.bookmarks == nil {
		a.bookmarks = map[string][]editor.Bookmark{}
	}
	a.bookmarks[t.Path] = t.Bookmarks()
}

// bookmarksRenamed re-keys parked bookmarks after a file or folder
// rename (fileops.go). Open tabs carry their own, and their Path was
// already rewritten by the caller. dir says oldPath was a folder, so
// every parked path under it moves too.
func (a *App) bookmarksRenamed(oldPath, newPath string, dir bool) {
	moved := false
	prefix := oldPath + string(filepath.Separator)
	// Collected first, applied after: inserting into a map mid-range may
	// or may not visit the new key, and a re-keyed entry must move once.
	renames := map[string]string{}
	for path := range a.bookmarks {
		switch {
		case path == oldPath:
			renames[path] = newPath
		case dir && strings.HasPrefix(path, prefix):
			renames[path] = filepath.Join(newPath, path[len(prefix):])
		}
	}
	for from, to := range renames {
		a.bookmarks[to] = a.bookmarks[from]
		delete(a.bookmarks, from)
		moved = true
	}
	// An open tab's bookmarks moved with its Path; they are only on
	// disk under the old one, so write whenever any tab under the
	// renamed path holds one.
	for _, t := range a.tabs {
		if t != nil && (t.Path == newPath || strings.HasPrefix(t.Path, newPath+string(filepath.Separator))) && t.BookmarkCount() > 0 {
			moved = true
		}
	}
	if moved {
		a.saveBookmarks()
	}
}

// -----------------------------------------------------------------------------
// Verbs
// -----------------------------------------------------------------------------

// menuToggleBookmark is the ≡ Nav row and the editor menu's row: toggle
// the bookmark on the caret's line. The editor menu placed the caret on
// the clicked line before running it, so both doors aim the same way.
func (a *App) menuToggleBookmark() {
	t := a.activeTabPtr()
	switch {
	case t == nil || t.IsImage():
		a.flash("Open a file to bookmark a line in it")
		return
	case t.Path == "":
		a.flash("Save the file first — bookmarks are kept by file path")
		return
	}
	a.toggleBookmarkAt(t, t.Cursor.Line)
}

// toggleBookmarkAt toggles the bookmark on one line of t, enforcing the
// cap, saying what happened, and writing the set through.
func (a *App) toggleBookmarkAt(t *editor.Tab, line int) {
	if !t.HasBookmark(line) && a.bookmarkTotal() >= history.MaxBookmarks {
		a.flash(fmt.Sprintf("Bookmark limit reached (%d) — clear some from ≡ Nav → Bookmarks…", history.MaxBookmarks))
		return
	}
	if t.ToggleBookmark(line) {
		a.flash(fmt.Sprintf("Bookmarked line %d", line+1))
	} else {
		a.flash(fmt.Sprintf("Removed the bookmark on line %d", line+1))
	}
	a.saveBookmarks()
}

// menuNextBookmark / menuPrevBookmark walk the project's bookmarks from
// the caret, wrapping at either end.
func (a *App) menuNextBookmark() { a.stepBookmark(1) }

// menuPrevBookmark is menuNextBookmark's mirror.
func (a *App) menuPrevBookmark() { a.stepBookmark(-1) }

// stepBookmark jumps to the next (dir > 0) or previous bookmark after
// the caret in (path, line) order. A caret ON a bookmark moves past it
// — "next" from a bookmark is never itself. Bookmarks in files that are
// gone from disk are skipped, not dropped: a file missing because a
// branch switch took it away comes back with its bookmarks when the
// branch does (the picker is where a missing one is said and removed).
func (a *App) stepBookmark(dir int) {
	var list []bookmarkRef
	for _, r := range a.allBookmarks() {
		if r.tab != nil || fileExists(r.path) {
			list = append(list, r)
		}
	}
	if len(list) == 0 {
		a.flash(bookmarksEmptyHint)
		return
	}
	curPath, curLine := "", -1
	if t := a.activeTabPtr(); t != nil {
		curPath, curLine = t.Path, t.Cursor.Line
	}
	// after reports whether r sorts strictly after the caret.
	after := func(r bookmarkRef) bool {
		if r.path != curPath {
			return r.path > curPath
		}
		return r.line > curLine
	}
	before := func(r bookmarkRef) bool {
		if r.path != curPath {
			return r.path < curPath
		}
		return r.line < curLine
	}
	idx := -1
	if dir > 0 {
		for i, r := range list {
			if after(r) {
				idx = i
				break
			}
		}
		if idx < 0 {
			idx = 0 // wrap to the first
		}
	} else {
		for i := len(list) - 1; i >= 0; i-- {
			if before(list[i]) {
				idx = i
				break
			}
		}
		if idx < 0 {
			idx = len(list) - 1 // wrap to the last
		}
	}
	r := list[idx]
	if a.jumpToBookmark(r) {
		a.flash(fmt.Sprintf("Bookmark %d/%d · %s:%d", idx+1, len(list), a.relativePathFor(r.path), r.line+1))
	}
}

// jumpToBookmark puts the caret on a bookmark's line, opening its file
// when it is parked, and records the departure in the nav history so
// Go back returns. Reports success.
func (a *App) jumpToBookmark(r bookmarkRef) bool {
	from, hasFrom := a.currentNavLoc()
	if r.tab != nil {
		idx := a.tabIndexOf(r.tab)
		if idx < 0 {
			return false
		}
		if idx != a.activeTab {
			// switchToTab records the departure itself (and flushes
			// auto-save) — the single place a switch does.
			a.switchToTab(idx)
		} else if hasFrom {
			a.recordNav(from)
		}
		a.goToLine(r.line, 0)
		return true
	}
	if !fileExists(r.path) {
		a.flash(fmt.Sprintf("%s no longer exists", a.relativePathFor(r.path)))
		return false
	}
	// openFile records the departure for a new tab, and wireTab adopts
	// the parked bookmarks — re-anchored against the file as it is now,
	// so the line to land on is read back from the TAB rather than taken
	// from the parked copy, which may be stale.
	a.openFile(r.path)
	t := a.activeTabPtr()
	if t == nil || t.Path != r.path {
		return false
	}
	a.goToLine(closestBookmarkLine(t.Bookmarks(), r), 0)
	return true
}

// closestBookmarkLine finds the adopted bookmark that a parked ref
// became: the one with its text nearest its old line, else simply the
// nearest. The ref's own line when the tab somehow has none.
func closestBookmarkLine(bms []editor.Bookmark, r bookmarkRef) int {
	best, bestD, bestText := r.line, -1, false
	for _, b := range bms {
		d := b.Line - r.line
		if d < 0 {
			d = -d
		}
		text := b.Text == r.text
		switch {
		case bestD < 0, text && !bestText, text == bestText && d < bestD:
			best, bestD, bestText = b.Line, d, text
		}
	}
	return best
}

// bookmarksEmptyHint is what every verb says when there is nothing to
// walk — the way to make one, not just the fact.
const bookmarksEmptyHint = "No bookmarks yet — ≡ Nav → Toggle bookmark, or double-click a line number"

// menuBookmarks lists every bookmark in a picker; picking one jumps to
// it. Below a divider sit the clear rows: this file's (when it has any)
// and the whole project's, which confirms.
func (a *App) menuBookmarks() {
	list := a.allBookmarks()
	if len(list) == 0 {
		if !a.flashHistoryNoSave() {
			a.flash(bookmarksEmptyHint)
		}
		return
	}
	items := make([]paletteItem, 0, len(list)+3)
	for _, r := range list {
		label := fmt.Sprintf("%s:%d", a.relativePathFor(r.path), r.line+1)
		missing := r.tab == nil && !fileExists(r.path)
		if missing {
			label += "  (missing — pick to remove)"
		} else if txt := strings.TrimSpace(r.text); txt != "" {
			label += "  " + elide(strings.ReplaceAll(txt, "\t", " "), bookmarkLabelText)
		}
		items = append(items, paletteItem{label: label, run: func(app *App) {
			if missing {
				app.removeParkedBookmark(r)
				return
			}
			app.jumpToBookmark(r)
		}})
	}
	items = append(items, paletteSpacer())
	if t := a.activeTabPtr(); t != nil && t.Path != "" {
		if n := t.BookmarkCount(); n > 0 {
			items = append(items, paletteItem{
				label: fmt.Sprintf("Clear bookmarks in %s (%d)", filepath.Base(t.Path), n),
				run:   func(app *App) { app.clearTabBookmarks(t) },
			})
		}
	}
	items = append(items, paletteItem{
		label: fmt.Sprintf("Clear all bookmarks (%d)", len(list)),
		run:   (*App).confirmClearAllBookmarks,
	})
	a.openPicker(fmt.Sprintf("Bookmarks (%d)", len(list)), items)
	// Said as the list opens, like the recent-files picker: this is the
	// moment someone is looking at what the history holds.
	a.flashHistoryNoSave()
}

// bookmarksLabel is the ≡ Nav "Bookmarks…" label. Bookmarks live in the
// same per-repo database as recent files, so they carry the same
// "(not saved)" notice (historyRowLabel).
func (a *App) bookmarksLabel() string {
	return a.historyRowLabel("Bookmarks…")
}

// removeParkedBookmark drops one parked bookmark — the picker's answer
// to a row whose file is gone.
func (a *App) removeParkedBookmark(r bookmarkRef) {
	bms := a.bookmarks[r.path]
	for i, b := range bms {
		if b.Line == r.line {
			bms = append(bms[:i], bms[i+1:]...)
			break
		}
	}
	if len(bms) == 0 {
		delete(a.bookmarks, r.path)
	} else {
		a.bookmarks[r.path] = bms
	}
	a.saveBookmarks()
	a.flash(fmt.Sprintf("Removed the bookmark on %s:%d (file is gone)", a.relativePathFor(r.path), r.line+1))
}

// clearTabBookmarks removes every bookmark in one open file. No
// confirmation: it is one file's worth, and re-marking is a click.
func (a *App) clearTabBookmarks(t *editor.Tab) {
	if a.tabIndexOf(t) < 0 {
		return
	}
	n := t.ClearBookmarks()
	a.saveBookmarks()
	a.flash(fmt.Sprintf("Cleared %d bookmark(s) in %s", n, filepath.Base(t.Path)))
}

// confirmClearAllBookmarks asks before wiping the project's bookmarks —
// the one bookmark verb that cannot be undone a click at a time.
func (a *App) confirmClearAllBookmarks() {
	n := a.bookmarkTotal()
	a.openConfirm("Clear bookmarks", fmt.Sprintf("Remove all %d bookmarks in this project?", n), func(app *App) {
		app.clearAllBookmarks()
	})
}

// clearAllBookmarks removes every bookmark, open and parked.
func (a *App) clearAllBookmarks() {
	n := 0
	for _, t := range a.tabs {
		if t != nil {
			n += t.ClearBookmarks()
		}
	}
	for _, bms := range a.bookmarks {
		n += len(bms)
	}
	a.bookmarks = nil
	a.saveBookmarks()
	a.flash(fmt.Sprintf("Cleared %d bookmark(s)", n))
}

// bookmarkContextLabel is the editor menu row's label for the line the
// right-click landed on: the state the click would produce.
func bookmarkContextLabel(t *editor.Tab) string {
	if t.HasBookmark(t.Cursor.Line) {
		return "Remove bookmark"
	}
	return "Add bookmark"
}

// bookmarkGutterPress is the mouse door: a double-click on a line number
// toggles that line's bookmark. The first press of the pair is only
// stamped (and handed on — it still places the caret, or opens a
// diagnostic's message), using a record of its own: sharing lastClick
// with editorPress would make editorPress read the very press that
// stamped it as the second half of a word-select double-click.
func (a *App) bookmarkGutterPress(x, y int) bool {
	t := a.activeTabPtr()
	if t == nil || t.IsImage() || t.IsMarkdownView() || t.Path == "" {
		return false
	}
	ex, ey, ew, eh := a.editorRect()
	lx, ly := x-ex, y-ey
	// The line numbers are everything left of the annotation column
	// (which starts right after them); the mark cell and the blame
	// column have verbs of their own.
	numEnd, _ := t.AnnotationCols()
	if lx < 0 || lx >= numEnd || ly < 0 || ly >= eh {
		a.bookmarkClick = clickRecord{}
		return false
	}
	now := time.Now()
	prev := a.bookmarkClick
	if prev.x == x && prev.y == y && now.Sub(prev.when) < doubleClickMs {
		a.bookmarkClick = clickRecord{}
		// HitTest, never ScrollY+row: under soft wrap a line spans rows.
		pos, ok := t.HitTest(lx, ly, ew, eh)
		if !ok {
			return true
		}
		a.toggleBookmarkAt(t, pos.Line)
		return true
	}
	a.bookmarkClick = clickRecord{x: x, y: y, when: now}
	return false
}

// -----------------------------------------------------------------------------
// Persistence
// -----------------------------------------------------------------------------

// loadBookmarks parks the repository's stored bookmarks, before any tab
// is restored or opened so wireTab can hand each file its own. The
// history is loaded here if nothing loaded it yet (it normally has:
// loadSessionStore seeds the recent-file ring from it), and regardless
// of the session toggle (see the file header).
func (a *App) loadBookmarks() {
	stored := a.repoHistory().Bookmarks()
	if len(stored) == 0 {
		return
	}
	a.bookmarks = make(map[string][]editor.Bookmark, len(stored))
	for path, bms := range stored {
		for _, b := range bms {
			a.bookmarks[path] = append(a.bookmarks[path], editor.Bookmark{Line: b.Line, Text: b.Text})
		}
	}
}

// bookmarkSnapshot is every bookmark in the project, by path, in the
// history package's form — what writeHistory hands over before each
// write, so the edits that moved bookmarks since the last toggle reach
// disk too.
func (a *App) bookmarkSnapshot() map[string][]history.Bookmark {
	out := map[string][]history.Bookmark{}
	for _, r := range a.allBookmarks() {
		out[r.path] = append(out[r.path], history.Bookmark{Line: r.line, Text: r.text})
	}
	return out
}

// saveBookmarks writes the bookmarks through to the history database
// now. writeHistory carries everything else pending with them (recent
// files, searches) — harmless, the history's writes are deltas built to
// be issued any number of times — and stays silent on failure, keeping
// the change pending for the next write.
func (a *App) saveBookmarks() {
	a.repoHistory()
	a.writeHistory()
}

// fileExists reports whether path names an existing regular file.
func fileExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}
