// =============================================================================
// File: internal/app/projectsearch.go
// Author: Rohan Allison <rohanthewiz@gmail.com>
// Created: 2026-08-03
// Copyright: 2026 Rohan Allison. All rights reserved.
// =============================================================================

// projectsearch.go is "Find in project": every occurrence of a query
// across the whole tree, listed in the SAME panel Find-all uses.
//
// Reusing that panel rather than building a list surface is the entire
// reason this feature is small. findall.go already had everything a
// cross-file result list needs — a two-column row, a fixed-height strip
// that displaces the editor instead of covering it, a right-dock mode,
// scrolling, an Esc contract, and a mouse story. All this adds is a path
// on each row and a different verb behind Enter.
//
// Where project mode DELIBERATELY differs from the in-file list:
//
//   - Walking rows does not preview. In-file, moving the highlight moves
//     the cursor live, because the buffer is already open and moving the
//     cursor costs nothing. Across files it would mean opening a file per
//     keystroke — and an open is not free: it fires the LSP's didOpen,
//     Copilot's didOpen, every plugin's "open" hook, and a syntax pass,
//     and leaves a tab behind for every row the user merely scrolled
//     past. So the row IS the preview (it carries the whole line with the
//     hit lit), and Enter or a double-click opens. That is what every
//     grep integration does, and it is the honest reading of the cost.
//   - Esc therefore restores nothing, because nothing moved.
//
// The search itself is internal/search: pure Go, over the FINDER'S index
// so the project's own ignore rules apply, run off the main loop and
// delivered as an event like every other background result in this
// codebase.
package app

import (
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/gdamore/tcell/v2"

	"github.com/rohanthewiz/ced/internal/editor"
	"github.com/rohanthewiz/ced/internal/search"
)

// projectSearchEvent carries a finished search back to the main loop.
// Generation-checked like every other async result: a search launched
// before the user changed their mind must not open a list over the top
// of whatever they are doing now.
type projectSearchEvent struct {
	when      time.Time
	seq       int
	query     string
	hits      []search.Hit
	truncated bool
}

// When satisfies the tcell.Event interface.
func (e *projectSearchEvent) When() time.Time { return e.when }

// menuFindInProject is the ≡ Search row and the Esc-P leader: search the
// whole project for the query the context implies, asking for one when
// context offers nothing.
//
// Seeding follows findAllSeedQuery exactly — find bar, then a single-line
// selection, then the word under the cursor — because the two features
// are the same question at two scopes and answering them differently
// would be a trap.
func (a *App) menuFindInProject() {
	a.closeMenu()
	if q := a.findAllSeedQuery(); q != "" {
		a.startProjectSearch(q)
		return
	}
	a.openPrompt("Find in project", "searches every file", "", func(app *App, v string) {
		app.startProjectSearch(v)
	})
}

// hasProjectSearch gates the menu row: the finder's index is the file
// list, so there is nothing to search until it exists.
func (a *App) hasProjectSearch() bool {
	return a.finder != nil && a.rootDir != ""
}

// startProjectSearch kicks the scan off on a goroutine and posts the
// result. Off-loop because a cold cache over a large tree is hundreds of
// milliseconds of IO, and the editor must stay responsive through it —
// the same contract git status, diffs, and plugin commands all keep.
func (a *App) startProjectSearch(query string) {
	if query == "" || a.screen == nil {
		return
	}
	if a.finder == nil {
		a.flash("Find in project: no file index yet")
		return
	}
	paths := a.finder.Paths()
	if len(paths) == 0 {
		// The index builds asynchronously at startup and after workspace
		// mutations. Saying so beats "no results", which would be a lie.
		a.flash("Find in project: still indexing — try again in a moment")
		return
	}

	a.projectSearchSeq++
	seq := a.projectSearchSeq
	scr, root := a.screen, a.rootDir
	a.flash(fmt.Sprintf("Searching %d files for %q…", len(paths), query))
	go func() {
		hits, truncated := search.Project(root, paths, search.Options{Query: query})
		_ = scr.PostEvent(&projectSearchEvent{
			when: time.Now(), seq: seq, query: query,
			hits: hits, truncated: truncated,
		})
	}()
}

// handleProjectSearch opens the result list on the main loop.
//
// A stale generation is dropped, and so is a result that arrives while a
// modal or the menu owns the screen — the user asked for something else
// in the meantime, and stealing the slot from a prompt they are typing
// into would be worse than losing a search they can repeat.
func (a *App) handleProjectSearch(e *projectSearchEvent) {
	if e.seq != a.projectSearchSeq {
		return
	}
	if len(e.hits) == 0 {
		a.flash(fmt.Sprintf("Find in project: no occurrences of %q", e.query))
		return
	}
	if a.modal != nil || a.menuOpen {
		a.flash(fmt.Sprintf("Find in project: %d hits for %q — run it again", len(e.hits), e.query))
		return
	}
	m := &findAllModal{
		query:     e.query,
		tabIdx:    -1, // no single tab behind this list
		project:   true,
		truncated: e.truncated,
		rows:      a.projectSearchRows(e.hits),
	}
	a.openModal(m)
	m.ensureRowVisible(a)
}

// projectSearchRows compacts each hit into a display row, reusing the
// in-file compaction so the two lists render code identically — leading
// indentation off, interior tabs as one space, which keeps the display
// text aligned rune-for-rune with the buffer.
func (a *App) projectSearchRows(hits []search.Hit) []findAllRow {
	rows := make([]findAllRow, 0, len(hits))
	for _, h := range hits {
		text, trimmed := compactLine(h.Text)
		n := runeLen(text)
		hit, hitW := h.Col-trimmed, h.Width
		if hit < 0 {
			hitW += hit
			hit = 0
		}
		if hit > n {
			hit, hitW = n, 0
		}
		if hit+hitW > n {
			hitW = n - hit
		}
		hitW = max(hitW, 0)
		rows = append(rows, findAllRow{
			line: h.Line, col: h.Col, width: h.Width,
			text: text, hit: hit, hitW: hitW,
			path:  h.Path,
			label: fmt.Sprintf("%s:%d", a.projectSearchLabel(h.Path), h.Line+1),
		})
	}
	return rows
}

// projectSearchLabel is the path as the row should name it: relative to
// the project root, since every hit shares that prefix and repeating it
// on every row would push the code off the right edge.
func (a *App) projectSearchLabel(path string) string {
	if rel, err := filepath.Rel(a.rootDir, path); err == nil && !strings.HasPrefix(rel, "..") {
		return rel
	}
	return filepath.Base(path)
}

// openSelected is project mode's accept: open the file the highlighted
// row names, put the cursor on the hit, and close the list.
//
// The list closes FIRST, so the editor band is back to full height before
// the cursor is placed — otherwise the centering below would measure
// against a band the panel is still taking rows out of, and the line
// would land off-centre the moment the panel left.
func (m *findAllModal) openSelected(a *App) {
	if m.selected < 0 || m.selected >= len(m.rows) {
		a.closeModal()
		return
	}
	r := m.rows[m.selected]
	a.closeModal()
	if r.path == "" {
		return
	}
	a.openFile(r.path)
	tab := a.activeTabPtr()
	if tab == nil || tab.Path != r.path {
		return // the open failed and already flashed why
	}
	tab.MoveCursorTo(editor.Position{Line: r.line, Col: r.col}, false)
	// Centered rather than merely scrolled into view, for the reason the
	// in-file preview centers: a minimal scroll parks the line on the
	// last row, showing everything before it and nothing after, which is
	// useless when the question is "what is this line doing?".
	if _, _, ew, eh := a.editorRect(); !tab.CursorLineVisible(eh) {
		tab.CenterOnCursor(ew, eh)
	}
	// The hit is left selected so it is visible the instant the file
	// opens — the same courtesy the find bar's current match gets.
	tab.SetFindQuery(m.query)
}

// titleText names the list and, in project mode, admits when the result
// cap cut it short. A silently truncated list reads as "that's all of
// them", which is the one wrong answer a search tool can give.
//
// The heading is what a project-mode producer that is NOT a text search
// substitutes for "Find in project" — references, today
// (lspreferences.go). The truncation clause stays shared rather than
// duplicated per producer, because the honesty it buys is the part
// neither of them may skip.
func (m *findAllModal) titleText() string {
	if !m.project {
		return fmt.Sprintf("Find all %q", m.query)
	}
	verb := "Find in project"
	if m.heading != "" {
		verb = m.heading
	}
	if m.truncated {
		return fmt.Sprintf("%s %q — first %d", verb, m.query, len(m.rows))
	}
	return fmt.Sprintf("%s %q", verb, m.query)
}

// footerHints returns the key hints in decreasing width; draw takes the
// widest that fits. Project mode says "open" rather than "accept" (that
// press changes which file you are looking at) and drops "preview", which
// it deliberately does not do.
func (m *findAllModal) footerHints() []string {
	if m.project {
		return []string{
			" ↑↓ walk · enter open · esc close · d dock ",
			" ↑↓ walk · enter open · esc close ",
			" enter open · esc close ",
			" esc close ",
		}
	}
	return []string{
		" ↑↓ preview · enter accept · esc back · d dock ",
		" ↑↓ preview · enter accept · esc back ",
		" enter accept · esc back ",
		" esc back ",
	}
}

// Compile-time check that projectSearchEvent really is a tcell.Event.
var _ tcell.Event = (*projectSearchEvent)(nil)
