// =============================================================================
// File: internal/app/finder.go
// Author: Rohan Allison <rohanthewiz@gmail.com>
// Created: 2026-04-30
// Copyright: 2026 Rohan Allison. All rights reserved.
// Portions copyright 2026 Cloudmanic, LLC. Original author: Spicer Matthews.
// =============================================================================

package app

// Project-wide file finder UI — a centered modal with a search
// input on top and ~10 result rows below. Type to filter, ↑/↓ to
// navigate, Enter to open, Esc to dismiss. Mouse hover highlights;
// click opens.
//
// All the index/scoring lifting lives in internal/finder; this
// file is just the wiring between user input and the buffer/tree
// that already exist. The cached index (App.finder) outlives the
// modal — finderModal is only the transient UI state of one open.
//
// "Find files in folder…" is the same modal with a SCOPE: the index is
// narrowed to one directory and every path is matched and shown
// relative to it, so a query can be a bare filename or a path inside the
// folder ("app/tab" typed in internal/). The scope lives on the modal,
// never on the shared index, so the plain finder is untouched:
//
//	tree folder ▸ "Find files in folder…" ─┐
//	≡ Find "Find files in folder (sub/)" ──┴─▶ openFinderIn(dir)
//	                                              │ root → "" (whole project)
//	≡ Find "Find file in project" / Esc-p ──▶ openFinder()
//	                                              ▼
//	        refresh: scope "" → Finder.Search, else Finder.SearchUnder
//	        openSelected: rootDir + scope + folder-relative result path

import (
	"path/filepath"
	"strings"
	"time"

	"github.com/gdamore/tcell/v2"
	"github.com/rohanthewiz/ced/internal/filetree"
	"github.com/rohanthewiz/ced/internal/finder"
)

const (
	// finderModalMaxWidth caps the modal so very wide terminals
	// don't get a full-width strip that's awkward to scan. 80 is
	// the comfortable reading width every IDE settles on.
	finderModalMaxWidth = 80
	// finderResultsVisible is how many result rows we render at
	// once. 10 is the floor for "feels useful" without dominating
	// the screen on small terminals.
	finderResultsVisible = 10
	// finderSearchLimit is how deep we look — we ask for one full
	// "page" extra so users can scroll past the initial chunk.
	finderSearchLimit = 50
)

// finderRebuiltEvent is posted by the background indexer goroutine
// when it finishes a rebuild. The main loop reacts by re-running
// the current query so the visible results refresh — the user
// gets to see "Indexing…" replaced with real matches without
// having to type or wait for the next keystroke.
type finderRebuiltEvent struct {
	when time.Time
}

// When satisfies the tcell.Event interface.
func (e *finderRebuiltEvent) When() time.Time { return e.when }

// finderModal is the transient UI state of one finder session: the
// query field, the highlighted row, and the current result page.
type finderModal struct {
	field    textField
	selected int
	results  []finder.Result

	// scope is the folder this finder is narrowed to, ROOT-RELATIVE and
	// "/"-separated ("internal/app"), or "" for the whole project. When
	// set, results[].Path is relative to the folder, not the root.
	// scopeName is its display form for the title, and scopeTotal how
	// many indexed files the folder holds (the count tail's total, and
	// the "nothing indexed here" note when it is 0).
	scope      string
	scopeName  string
	scopeTotal int
}

// openFinder shows the project-wide file finder. Triggers a
// background rebuild on every open so external file changes are
// reflected even if the periodic invalidation tick hasn't fired.
// (Building when the index is already StateReady is a no-op
// inside the orchestrator's coalesce gate.)
func (a *App) openFinder() {
	m := &finderModal{}
	a.openModal(m)
	scr := a.screen
	if a.finder != nil {
		// Rebuild only when the cache is genuinely stale. A re-open
		// during a still-warm session shouldn't pay for a refresh.
		if a.finder.State() != finder.StateReady {
			a.finder.Rebuild(func() {
				_ = scr.PostEvent(&finderRebuiltEvent{when: time.Now()})
			})
		}
	}
	m.refresh(a)
}

// openFinderIn opens the finder narrowed to the files under dir
// (absolute). The project root — what the tree's root row hands over —
// folds into the plain finder (searchScopeDir), so "Find file in
// project…" on the root is exactly Esc-p.
//
// A folder outside the root (a stale node after an external move)
// flashes instead of opening an empty modal: the index only knows the
// project, so there is nothing it could ever list.
func (a *App) openFinderIn(dir string) {
	dir = a.searchScopeDir(dir)
	if dir == "" {
		a.openFinder()
		return
	}
	rel, err := filepath.Rel(a.rootDir, dir)
	if err != nil || rel == "." || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		a.flash("Find files in folder: that folder is outside the project")
		return
	}
	m := &finderModal{scope: filepath.ToSlash(rel), scopeName: a.relativeFolderLabel(dir)}
	a.openModal(m)
	a.rebuildFinderIfStale()
	m.refresh(a)
}

// rebuildFinderIfStale kicks a background index build when the cache is
// not ready, posting finderRebuiltEvent so an open modal refreshes. A
// warm index costs nothing — the finder's coalesce gate also makes a
// redundant call a no-op.
func (a *App) rebuildFinderIfStale() {
	if a.finder == nil || a.finder.State() == finder.StateReady {
		return
	}
	scr := a.screen
	a.finder.Rebuild(func() {
		_ = scr.PostEvent(&finderRebuiltEvent{when: time.Now()})
	})
}

// ctxFindFilesInFolder is the tree's folder row "Find files in folder…"
// (the root's "Find file in project…"): the file finder scoped to the
// clicked directory. Folders only — openTreeContext never offers it on
// a file.
func ctxFindFilesInFolder(a *App, n *filetree.Node) {
	if !n.IsDir {
		return
	}
	a.openFinderIn(n.Path)
}

// findFilesInFolderContextLabel names the tree row for n: the root's row
// is the whole project and says so (it opens the plain finder), every
// other folder gets the folder verb.
func (a *App) findFilesInFolderContextLabel(n *filetree.Node) string {
	if a.tree != nil && n == a.tree.Root {
		return "Find file in project…"
	}
	return "Find files in folder…"
}

// menuFindFilesInFolder is the ≡ Find twin of the tree's folder row
// (every file action also lives in ≡ — right-click is not reliable
// under macOS Terminal + tmux). It acts on the ACTIVE folder, like the
// other ≡ folder rows. With no subfolder active it explains how to pick
// one rather than quietly becoming the plain finder.
func (a *App) menuFindFilesInFolder() {
	a.closeMenu()
	if !a.hasActiveSubfolder() {
		a.flash("Find files in folder: click a folder in the tree first — Find file in project searches everything")
		return
	}
	a.openFinderIn(a.activeFolder)
}

// findFilesInFolderLabel is the ≡ row's dynamic label, naming the folder
// it will search (activeFolderRowLabel).
func (a *App) findFilesInFolderLabel() string {
	return a.activeFolderRowLabel("Find files in folder")
}

// menuFindFile is the ≡ menu entry that opens the finder. Lives
// alongside menuFind (which is the in-file find bar) — they share
// vocabulary but search different scopes.
func (a *App) menuFindFile() {
	a.closeMenu()
	a.openFinder()
}

// hasFinder is the menu predicate. Always true once the finder
// has been wired in App.New — the row stays enabled even before
// the first index lands so the user can pop the modal and watch
// "Indexing…" tick over.
func (a *App) hasFinder() bool {
	return a.finder != nil
}

// refresh re-runs the current query against the index. Called on
// every edit and on the rebuilt event so a slow first index doesn't
// leave the modal showing stale "Indexing…" forever.
func (m *finderModal) refresh(a *App) {
	if a.finder == nil {
		m.results = nil
		return
	}
	if m.scope == "" {
		m.results = a.finder.Search(m.field.String(), finderSearchLimit)
	} else {
		m.results, m.scopeTotal = a.finder.SearchUnder(m.scope, m.field.String(), finderSearchLimit)
	}
	if m.selected >= len(m.results) {
		m.selected = len(m.results) - 1
	}
	if m.selected < 0 {
		m.selected = 0
	}
}

// invalidateFinder marks the index stale and kicks off a rebuild.
// Called by the file-tree refresh tick and by every fileops
// mutation (create / rename / delete) so the finder doesn't show
// ghost paths or miss freshly-created files.
func (a *App) invalidateFinder() {
	if a.finder == nil {
		return
	}
	a.finder.Invalidate()
	scr := a.screen
	a.finder.Rebuild(func() {
		_ = scr.PostEvent(&finderRebuiltEvent{when: time.Now()})
	})
}

// handleKey routes keyboard input while the finder modal is open.
// The finder-specific bits are arrow-key navigation through results
// and Enter-to-open; text editing is the shared textField.
func (m *finderModal) handleKey(a *App, ev *tcell.EventKey) {
	switch ev.Key() {
	case tcell.KeyEsc:
		a.closeModal()
	case tcell.KeyEnter:
		m.openSelected(a)
	case tcell.KeyUp:
		if m.selected > 0 {
			m.selected--
		}
	case tcell.KeyDown:
		if m.selected < len(m.results)-1 {
			m.selected++
		}
	default:
		if _, edited := m.field.handleKey(ev); edited {
			m.selected = 0
			m.refresh(a)
		}
	}
}

// handleMouse handles mouse input while the modal is open. Hover
// highlights the row under the cursor; click opens it. Click outside
// the modal dismisses.
func (m *finderModal) handleMouse(a *App, x, y int, btn tcell.ButtonMask) {
	mx, my, mw, mh := m.rect(a)
	rowsStart := my + 4
	row := y - rowsStart
	if row >= 0 && row < len(m.results) && x >= mx && x < mx+mw {
		// Hover highlight always tracks the mouse — same behaviour
		// the action menu uses, so users can scrub through results
		// without clicking.
		m.selected = row
	}
	if btn&tcell.Button1 == 0 {
		return
	}
	if x < mx || x >= mx+mw || y < my || y >= my+mh {
		a.closeModal()
		return
	}
	if row >= 0 && row < len(m.results) {
		m.selected = row
		m.openSelected(a)
	}
}

// openSelected opens the highlighted result. Closes the modal, then
// resolves the relative path against the project root and opens the
// file. Silent no-op when the result list is empty (e.g. the user
// mashed Enter on a no-match query).
func (m *finderModal) openSelected(a *App) {
	if m.selected < 0 || m.selected >= len(m.results) {
		return
	}
	rel := m.results[m.selected].Path
	a.closeModal()
	// A scoped result is folder-relative; Join drops the "" of an
	// unscoped one.
	abs := filepath.Join(a.rootDir, filepath.FromSlash(m.scope), rel)
	a.openFile(abs)
}

// rect returns the on-screen rectangle of the finder modal. Sized to
// fit a healthy result list while leaving margins on small terminals.
func (m *finderModal) rect(a *App) (x, y, w, h int) {
	w = finderModalMaxWidth
	if w > a.width-4 {
		w = a.width - 4
	}
	if w < 30 {
		w = 30
	}
	// Layout: 1 border + 1 title + 1 divider + 1 input + N results
	// + 1 status + 1 border = N+6 rows.
	h = finderResultsVisible + 6
	if h > a.height-2 {
		h = a.height - 2
	}
	x = (a.width - w) / 2
	y = (a.height - h) / 3 // anchor in upper third — matches VS Code's feel
	if x < 0 {
		x = 0
	}
	if y < 0 {
		y = 0
	}
	return
}

// draw paints the modal: title + Esc hint, input field with
// match-count tail, then either a "Indexing…" line or the result
// rows with highlighted match characters.
//
// Layout (relY):
//
//	0     top border
//	1     title — "Find file in project    esc"
//	2     divider
//	3     input          [ query…           42/12345 ]
//	4..N  result rows
//	N+1   bottom border
func (m *finderModal) draw(a *App) {
	mx, my, mw, mh := m.rect(a)
	c := a.chrome()
	hitStyle := tcell.StyleDefault.Background(c.bg).Foreground(a.theme.FindCurrent).Bold(true)
	title := "Find file"
	if m.scope != "" {
		title = "Find file in " + m.scopeName
	}
	c.drawFrame(a.screen, mx, my, mw, mh, title)

	// Input row — the right side leaves room for the count tail. The
	// text starts at mx+2, the result rows' column, so the query lines
	// up with the paths it filters (same as the palette).
	inputStyle := tcell.StyleDefault.Background(a.theme.BG).Foreground(a.theme.Text)
	m.field.draw(a.screen, my+3, mx+2, mx+mw-12, inputStyle, true)

	// Match count tail.
	state, total, viaGit := finder.StateIdle, 0, false
	if a.finder != nil {
		state, total, viaGit = a.finder.Stats()
	}
	_ = viaGit // reserved for a future "via git" badge — keeping the
	// triple-return so callers don't have to plumb a new method later.
	tail := ""
	switch state {
	case finder.StateBuilding, finder.StateIdle:
		tail = "indexing… "
	case finder.StateErrored:
		tail = "index err "
	case finder.StateReady:
		if m.scope != "" {
			// The folder's own file count, not the project's: "3/12"
			// in a folder of 12 files.
			total = m.scopeTotal
		}
		tail = countLabel(len(m.results), total) + " "
	}
	drawAt(a.screen, mx+mw-1-runeLen(tail), my+3, tail, c.muted)

	// Result rows. We paint the visible window (no scrolling support
	// in v1 — we just cap at finderResultsVisible). When the query
	// has more matches than fit, the user can refine.
	rowsStart := my + 4
	rowsCap := mh - 5 // borders + title + divider + input
	if rowsCap > finderResultsVisible {
		rowsCap = finderResultsVisible
	}
	visible := m.results
	if len(visible) > rowsCap {
		visible = visible[:rowsCap]
	}
	// A scoped finder over a folder the index skips entirely (build
	// output, vendor, anything gitignored) would otherwise read as "no
	// file matched"; name the likelier cause on the first row instead.
	emptyScope := m.scope != "" && state == finder.StateReady && m.scopeTotal == 0
	for i := 0; i < rowsCap; i++ {
		ry := rowsStart + i
		if i == 0 && emptyScope {
			for cx := mx + 1; cx < mx+mw-1; cx++ {
				a.screen.SetContent(cx, ry, ' ', nil, c.bgSt)
			}
			drawAt(a.screen, mx+2, ry, clipRunes("No indexed files here (ignored by the project?)", mw-4), c.muted)
			continue
		}
		if i >= len(visible) {
			// Clear unused rows so a previous query's tail doesn't
			// linger when results shrink.
			for cx := mx + 1; cx < mx+mw-1; cx++ {
				a.screen.SetContent(cx, ry, ' ', nil, c.bgSt)
			}
			continue
		}
		m.drawRow(a, mx, ry, mw, visible[i], i == m.selected, hitStyle, c.muted, c.bg)
	}
}

// drawRow paints one result line: the path, with matched runes
// highlighted via hitStyle, and the row background flipped when it's
// the selected row. The dirname is dimmed so the basename pops — same
// trick the editor's tab bar uses to make the file name the visual
// anchor.
func (m *finderModal) drawRow(a *App, mx, ry, mw int, r finder.Result, selected bool, hitStyle, mutedStyle tcell.Style, modalBG tcell.Color) {
	rowBG := modalBG
	if selected {
		rowBG = a.theme.BG
	}
	rowStyle := tcell.StyleDefault.Background(rowBG).Foreground(a.theme.Text)
	hitOnRow := hitStyle.Background(rowBG)
	mutedOnRow := mutedStyle.Background(rowBG)

	// Background fill so the selected row reads as a single block.
	for cx := mx + 1; cx < mx+mw-1; cx++ {
		a.screen.SetContent(cx, ry, ' ', nil, rowStyle)
	}

	pathRunes := []rune(r.Path)
	// Find the basename split so we can dim the directory part.
	sepIdx := -1
	for i := len(pathRunes) - 1; i >= 0; i-- {
		if pathRunes[i] == '/' {
			sepIdx = i
			break
		}
	}
	matchSet := map[int]bool{}
	for _, hit := range r.MatchedIndexes {
		matchSet[hit] = true
	}

	startCol := mx + 2
	maxCols := mw - 4
	for i, ch := range pathRunes {
		if i >= maxCols {
			break
		}
		st := rowStyle
		if i <= sepIdx {
			st = mutedOnRow
		}
		if matchSet[i] {
			st = hitOnRow
		}
		a.screen.SetContent(startCol+i, ry, ch, nil, st)
	}
}

// countLabel formats the "shown/total" tail. Pulled into its own
// helper so future tweaks (commas, abbreviation past 1k) live in
// one spot. We don't bother showing "shown" when it equals total
// — the count would be redundant and the eye should focus on the
// total.
func countLabel(shown, total int) string {
	if total == 0 {
		return "0"
	}
	if shown == total {
		return itoa(total)
	}
	return itoa(shown) + "/" + itoa(total)
}

// itoa is a tiny non-allocating-ish int→string for the count
// tail. strconv would work fine; keeping a local helper avoids
// pulling strconv into a UI file that otherwise doesn't need it.
func itoa(n int) string {
	if n <= 0 {
		return "0"
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	return string(buf[i:])
}
