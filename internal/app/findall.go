// =============================================================================
// File: internal/app/findall.go
// Author: Rohan Allison <rohanthewiz@gmail.com>
// Created: 2026-08-02
// Copyright: 2026 Rohan Allison. All rights reserved.
// =============================================================================

// findall.go implements "Find all in file": every occurrence of a query
// listed as one compacted row each — line number, then the line's text
// with the hit lit up — in a scrollable popup pinned above the editor,
// directly under the tab bar.
//
// Why this isn't a palette picker. The house rule is that any
// choose-one-from-a-list UI reuses openPicker, and nearly every list in
// ced does. This one can't, for three reasons that are all the point of
// the feature:
//
//   - The grammar is PEEK, not pick. Moving the highlight moves the
//     editor's cursor live, and Esc puts it back. The palette's contract
//     is the opposite — nothing happens until you commit, and dismissal
//     is a no-op.
//   - A click PREVIEWS instead of dismissing. That's what "non-modal"
//     means here: the list keeps the keyboard (Enter accepts, Esc
//     aborts), but clicking a row leaves it open so you can walk the
//     hits one by one.
//   - A row is two columns (line number ┃ code), not a label, and the
//     popup must not cover what it's previewing.
//
// That last one drives the geometry: the popup takes its rows OUT of the
// editor band (editorBandRows / editorRect) rather than floating over
// it, so the shortened editor scrolls the previewed line into what's
// left. It takes them off the TOP — list first, then the code it's
// pointing at. Height is fixed, so unlike the resizable bottom panels it
// needs no clamp negotiation with them; it just displaces, the way the
// find bar does at the other end.
//
// It still lives in App.modal: the single slot is what makes it mutually
// exclusive with every other overlay and gets it keyboard/mouse routing
// and draw ordering for free.

package app

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/gdamore/tcell/v2"

	"github.com/rohanthewiz/ced/internal/editor"
	"github.com/rohanthewiz/ced/internal/userconfig"
)

const (
	// findAllChromeRows is the non-row overhead of the popup: top
	// border, title, divider under it, bottom border.
	findAllChromeRows = 4
	// findAllVisibleRows is how many result rows the popup shows when
	// the window can afford them. Ten matches the palette/finder so the
	// three list surfaces feel like one instrument.
	findAllVisibleRows = 10
	// findAllMinEditorRows is what the editor keeps no matter what. The
	// popup is a preview surface — squeezing the code it's previewing
	// down to nothing defeats it.
	findAllMinEditorRows = 3
	// findAllMinHeight is the floor: chrome plus a single result row.
	findAllMinHeight = findAllChromeRows + 1
	// findAllMinLineDigits keeps the number column from twitching
	// between widths on files under 100 lines.
	findAllMinLineDigits = 3

	// findAllRightCols is the width of the right-docked column. Wide
	// enough for a line number plus a readable slice of code; anything
	// wider stops being a column and starts being a second editor.
	findAllRightCols = 62
	// findAllMinEditorCols is what the editor keeps when the list docks
	// right — the column twin of findAllMinEditorRows.
	findAllMinEditorCols = 34
	// findAllMinWidth is the narrowest the column may be squeezed to
	// before it stops being able to say anything.
	findAllMinWidth = 24
	// findAllDockBtnW is the dock button's cell width: glyph plus a
	// pad either side, so it's a comfortable click target.
	findAllDockBtnW = 3
)

// findAllRow is one result: where the hit is in the buffer, plus the
// compacted text to paint for it and where the hit falls inside that
// text. The display fields are derived once at open — the list is
// re-drawn on every event and re-deriving them per frame would scan the
// whole match set for nothing.
type findAllRow struct {
	line  int // 0-based buffer line
	col   int // rune column of the match start in the buffer
	width int // rune width of the match
	text  string
	hit   int // rune index of the match within text
	hitW  int // runes of text the match covers (0 = nothing to light up)

	// path is the file the hit lives in, set only in project mode
	// (projectsearch.go). Empty means "the tab this list was opened
	// against", which is every row of an in-file search. label is the
	// left column's text — the line number alone in file mode, the
	// relative path and line together in project mode.
	path  string
	label string
}

// findAllModal is one Find-all session: the resolved result list, where
// the highlight sits, and — the part that makes Esc mean something —
// the exact view state the popup was opened from.
type findAllModal struct {
	query  string
	tabIdx int
	rows   []findAllRow

	selected int
	scroll   int

	// Origin view state, restored verbatim by abort. Cursor and Anchor
	// are written back as plain fields (not MoveCursorTo) precisely so
	// cursorMoved stays clear and Render's EnsureVisible doesn't
	// second-guess the restored scroll — see the note in abort.
	origin        editor.Position
	originAnchor  editor.Position
	originScrollY int
	originScrollX int

	// Find state the tab carried before the popup borrowed it to paint
	// every occurrence. Restored on both exits, so the highlight set
	// lives exactly as long as the list that explains it.
	priorQuery   string
	priorMatches []editor.Match
	priorIndex   int

	// previewed records whether we ever moved the cursor. Nothing to
	// put back if we didn't, and restoring anyway would fight a
	// background reload that clamped the cursor for a good reason.
	previewed bool

	// project switches the list from "occurrences in this buffer" to
	// "occurrences in the project" (projectsearch.go). It changes three
	// things and nothing else: rows carry a path, walking the list does
	// NOT preview, and accept opens the file the row names. See the
	// project-mode note at the top of projectsearch.go for why the peek
	// contract deliberately stops at the file boundary.
	project bool
	// truncated marks a project search that hit the result cap, so the
	// title can say so — a silently short list reads as "that's all of
	// them", the one wrong answer a search can give.
	truncated bool
	// heading renames the list for a project-mode producer that is not a
	// text search — "References to" (lspreferences.go). It is the ONLY
	// thing such a producer is allowed to change: a second cross-file
	// list that behaved differently row-for-row would be a second feature
	// wearing this one's face. Empty means "Find in project".
	heading string
}

// -----------------------------------------------------------------------------
// Entry points
// -----------------------------------------------------------------------------

// menuFindAll is the ≡ / Esc-F entry point: list every occurrence of the
// query the context implies (see findAllSeedQuery), asking for one when
// context offers nothing.
func (a *App) menuFindAll() {
	a.closeMenu()
	a.openFindAll()
}

// openFindAll resolves a query and shows the list, falling back to a
// prompt when there's nothing to seed from. The prompt path is why this
// is split from showFindAll: promptModal closes itself before running
// its callback, so the callback lands in an empty modal slot.
func (a *App) openFindAll() {
	tab := a.activeTabPtr()
	if tab == nil || tab.IsImage() {
		return
	}
	if q := a.findAllSeedQuery(); q != "" {
		a.showFindAll(q)
		return
	}
	a.openPrompt("Find all in file", "lists every occurrence", "", func(app *App, v string) {
		app.showFindAll(v)
	})
}

// openFindAllFromBar is the find bar's Down-arrow gesture: turn what's
// typed into the full list. The bar closes on the way (openModal's
// closeAllModals does it), which is why the query is captured first —
// the list is the same search in another shape, not a second one.
func (a *App) openFindAllFromBar() {
	if len(a.findField.value) == 0 {
		return
	}
	a.showFindAll(a.findField.String())
}

// findAllSeedQuery is what "find all" means with no query typed, in
// priority order: what's in the find bar, then a single-line selection
// (a highlighted region is a narrower question — the same rule the chat
// attachments follow), then the word under the cursor. Empty means the
// caller should ask.
func (a *App) findAllSeedQuery() string {
	if a.findOpen && len(a.findField.value) > 0 {
		return a.findField.String()
	}
	tab := a.activeTabPtr()
	if tab == nil || tab.Buffer == nil {
		return ""
	}
	if tab.HasSelection() {
		// A multi-line selection isn't a search term; fall through to
		// the word under the cursor rather than searching for a blob
		// with newlines in it (FindAll matches within a line).
		if sel := tab.SelectionText(); sel != "" && !strings.ContainsRune(sel, '\n') {
			return sel
		}
	}
	if tab.Cursor.Line < 0 || tab.Cursor.Line >= len(tab.Buffer.Lines) {
		return ""
	}
	runes := []rune(tab.Buffer.Lines[tab.Cursor.Line])
	if start, end, ok := editor.WordRange(runes, tab.Cursor.Col); ok {
		return string(runes[start:end])
	}
	return ""
}

// showFindAll runs the search and opens the popup on its results. A miss
// flashes instead of opening an empty list — a popup with nothing in it
// makes the user dismiss a box to learn "no".
func (a *App) showFindAll(query string) {
	tab := a.activeTabPtr()
	if tab == nil || tab.IsImage() || tab.Buffer == nil || query == "" {
		return
	}
	matches := editor.FindAll(tab.Buffer, query)
	if len(matches) == 0 {
		a.flash(fmt.Sprintf("Find all: no occurrences of %q in this file", query))
		return
	}

	m := &findAllModal{
		query:         query,
		tabIdx:        a.activeTab,
		rows:          findAllRowsFor(tab.Buffer, matches),
		origin:        tab.Cursor,
		originAnchor:  tab.Anchor,
		originScrollY: tab.ScrollY,
		originScrollX: tab.ScrollX,
		priorQuery:    tab.FindQuery,
		priorMatches:  tab.FindMatches,
		priorIndex:    tab.FindIndex,
	}
	// Borrow the tab's find state so the editor tints every occurrence
	// while the list is up: the popup and the code behind it then agree
	// about what "all of them" means, and the previewed row paints as
	// the current match for free (findSource reads FindIndex).
	tab.SetFindQuery(query)
	// Start on the hit at or after the cursor — the same "nearest, not
	// first" rule the find bar uses, so opening the list from where you
	// are doesn't jump you to the top of the file.
	if idx := editor.FirstMatchAtOrAfter(matches, tab.Cursor); idx > 0 {
		m.selected = idx
	}
	a.openModal(m)
	m.preview(a)
}

// hasFindAll reports whether the active tab can be searched — the ≡ row's
// enable predicate, shared in spirit with hasFindable.
func (a *App) hasFindAll() bool { return a.hasFindable() }

// -----------------------------------------------------------------------------
// Rows
// -----------------------------------------------------------------------------

// findAllRowsFor compacts each match's line into one display row.
func findAllRowsFor(buf *editor.Buffer, matches []editor.Match) []findAllRow {
	rows := make([]findAllRow, 0, len(matches))
	for _, mt := range matches {
		if mt.Line < 0 || mt.Line >= len(buf.Lines) {
			continue
		}
		text, trimmed := compactLine(buf.Lines[mt.Line])
		n := runeLen(text)
		hit, hitW := mt.Col-trimmed, mt.Width
		if hit < 0 {
			// The hit starts inside the indentation we trimmed off (a
			// whitespace query). Clip it rather than drop the row — the
			// row still answers "which line", which is the job.
			hitW += hit
			hit = 0
		}
		if hit > n {
			hit, hitW = n, 0
		}
		if hit+hitW > n {
			hitW = n - hit
		}
		if hitW < 0 {
			hitW = 0
		}
		rows = append(rows, findAllRow{
			line: mt.Line, col: mt.Col, width: mt.Width,
			text: text, hit: hit, hitW: hitW,
			label: strconv.Itoa(mt.Line + 1),
		})
	}
	return rows
}

// compactLine strips a line's leading indentation and renders interior
// tabs as a single space, returning the display text and how many runes
// came off the front.
//
// A tab becomes ONE space rather than an IndentUnit-width expansion on
// purpose: it keeps the display text aligned rune-for-rune with the
// buffer's columns, so a match column maps to a display column by
// subtracting the trim. No width table, nothing to drift.
func compactLine(s string) (string, int) {
	runes := []rune(s)
	i := 0
	for i < len(runes) && (runes[i] == ' ' || runes[i] == '\t') {
		i++
	}
	out := make([]rune, 0, len(runes)-i)
	for _, r := range runes[i:] {
		if r == '\t' {
			r = ' '
		}
		out = append(out, r)
	}
	return string(out), i
}

// -----------------------------------------------------------------------------
// Preview / accept / abort
// -----------------------------------------------------------------------------

// tab returns the tab the popup was opened against, or nil if it's gone.
// Indexed rather than held by pointer so a closed tab can't be resurrected
// by this modal — nothing closes tabs while a modal owns the input, but
// the nil check keeps that from being load-bearing.
func (m *findAllModal) tab(a *App) *editor.Tab {
	if m.tabIdx < 0 || m.tabIdx >= len(a.tabs) {
		return nil
	}
	return a.tabs[m.tabIdx]
}

// preview moves the editor onto the highlighted row WITHOUT committing to
// it. MoveCursorTo (not FocusCurrentMatch) because it clamps: a
// background reload can shrink the buffer under an open popup, and a
// cursor past the end of a line is not a state the editor should have to
// survive. Dropping secondary carets is the documented cost of any
// explicit jump.
//
// An OFF-SCREEN hit is CENTERED rather than merely scrolled into view:
// EnsureVisible's minimal scroll parks it on the last row, which shows
// the lines before it and nothing after — no use when the question is
// "what is this line doing?". A hit already on screen is left exactly
// where it is, because scrolling the code out from under a line the
// user can already see is motion that buys nothing. So walking a
// cluster of nearby hits holds the view still, and falling out of the
// band re-centers once. The band is read AFTER the popup is installed,
// so it's the shortened one.
func (m *findAllModal) preview(a *App) {
	// Project mode walks rows without touching any buffer — the row
	// itself is the preview. See projectsearch.go for why.
	if m.project {
		m.ensureRowVisible(a)
		return
	}
	tab := m.tab(a)
	if tab == nil || m.selected < 0 || m.selected >= len(m.rows) {
		return
	}
	r := m.rows[m.selected]
	if m.selected < len(tab.FindMatches) {
		tab.FindIndex = m.selected // paint this hit as the current one
	}
	tab.MoveCursorTo(editor.Position{Line: r.line, Col: r.col}, false)
	// Left alone, the on-screen case still gets Render's EnsureVisible
	// off the cursorMoved flag MoveCursorTo just set — a vertical no-op
	// that keeps a long line's column in view.
	if _, _, ew, eh := a.editorRect(); !tab.CursorLineVisible(eh) {
		tab.CenterOnCursor(ew, eh)
	}
	m.previewed = true
	m.ensureRowVisible(a)
}

// accept settles where the preview left the cursor and closes. Every
// dismissal except Esc lands here — a click in the editor, like an Enter,
// reads as "this is the place I wanted".
func (m *findAllModal) accept(a *App) {
	if m.project {
		m.openSelected(a)
		return
	}
	m.restoreFind(a)
	a.closeModal()
}

// abort puts the view back exactly as it was and closes.
//
// RestoreView rather than MoveCursorTo: the captured SCROLL is part of
// what's being restored, and MoveCursorTo would arm the next Render's
// EnsureVisible to scroll over the top of it (see the editor-side note).
// Nothing to put back if we never previewed.
func (m *findAllModal) abort(a *App) {
	if tab := m.tab(a); tab != nil && m.previewed {
		tab.RestoreView(m.origin, m.originAnchor, m.originScrollY, m.originScrollX)
	}
	m.restoreFind(a)
	a.closeModal()
}

// restoreFind hands the tab's find state back. The popup only borrowed it
// to tint the occurrences it was listing, so the tint has to leave with
// the list — same contract as closing the find bar.
func (m *findAllModal) restoreFind(a *App) {
	// Project mode never borrowed anything: it has no single tab behind
	// it to tint, and writing these fields would clobber whatever find
	// state the active tab legitimately holds.
	if m.project {
		return
	}
	tab := m.tab(a)
	if tab == nil {
		return
	}
	tab.FindQuery = m.priorQuery
	tab.FindMatches = m.priorMatches
	tab.FindIndex = m.priorIndex
}

// -----------------------------------------------------------------------------
// Selection & scrolling
// -----------------------------------------------------------------------------

// moveSelection walks the highlight by delta rows (clamped at the ends —
// the list is a document position, not a carousel) and previews it.
func (m *findAllModal) moveSelection(a *App, delta int) {
	m.selectRow(a, m.selected+delta)
}

// selectRow highlights row idx and previews it. The single write path for
// the selection, so keyboard and mouse can't disagree about what
// "selected" implies.
func (m *findAllModal) selectRow(a *App, idx int) {
	if len(m.rows) == 0 {
		return
	}
	if idx < 0 {
		idx = 0
	}
	if idx >= len(m.rows) {
		idx = len(m.rows) - 1
	}
	m.selected = idx
	m.preview(a)
}

// ensureRowVisible scrolls the list just enough to bring the highlight
// into view. Called only when the selection moves — never from draw — so
// wheel-scrolling away from the highlight doesn't snap back (the same
// rule as the editor's cursorMoved and the menu's hovered row).
func (m *findAllModal) ensureRowVisible(a *App) {
	vis := m.visibleRows(a)
	if vis <= 0 {
		return
	}
	if m.selected < m.scroll {
		m.scroll = m.selected
	}
	if m.selected >= m.scroll+vis {
		m.scroll = m.selected - vis + 1
	}
	m.clampScroll(a)
}

// scrollList moves the visible window by delta rows without touching the
// selection — the wheel reads the list, it doesn't drive the editor.
func (m *findAllModal) scrollList(a *App, delta int) {
	m.scroll += delta
	m.clampScroll(a)
}

// clampScroll keeps the visible window inside the list.
func (m *findAllModal) clampScroll(a *App) {
	max := len(m.rows) - m.visibleRows(a)
	if max < 0 {
		max = 0
	}
	if m.scroll > max {
		m.scroll = max
	}
	if m.scroll < 0 {
		m.scroll = 0
	}
}

// -----------------------------------------------------------------------------
// Geometry
// -----------------------------------------------------------------------------

// findAllPanelHeight is the rows the open Find-all popup takes out of the
// editor band — 0 when it isn't up, and 0 when it's docked right (that
// mode costs columns instead). Lives on App (not the modal) because
// editorRect needs it without knowing what's in the modal slot.
func (a *App) findAllPanelHeight() int {
	m, ok := a.modal.(*findAllModal)
	if !ok || a.findAllDockRight {
		return 0
	}
	return m.height(a)
}

// findAllPanelWidth is the columns the right-docked list takes out of
// the editor band — 0 in the default top dock. The exact mirror of
// findAllPanelHeight, and exactly one of the two is ever non-zero.
func (a *App) findAllPanelWidth() int {
	m, ok := a.modal.(*findAllModal)
	if !ok || !a.findAllDockRight {
		return 0
	}
	return m.width(a)
}

// height is the popup's cell height. Docked right it's the full editor
// band — a tall column is the whole point of that mode, and it costs the
// editor no rows. Docked top it's the desired size capped so the editor
// keeps its minimum working rows.
//
// Both read the band directly (never editorRect, which subtracts these
// very values) — that's the whole reason editorBandRows/Cols exist as
// separate helpers.
func (m *findAllModal) height(a *App) int {
	band := a.editorBandRows()
	if a.findAllDockRight {
		if band < 0 {
			return 0
		}
		return band
	}
	h := findAllVisibleRows + findAllChromeRows
	if max := band - findAllMinEditorRows; h > max {
		h = max
	}
	if h < findAllMinHeight {
		h = findAllMinHeight
	}
	if h > band {
		h = band // absurdly short window: take what there is
	}
	if h < 0 {
		h = 0
	}
	return h
}

// width is the popup's cell width: the editor's full column band in the
// top dock, or the fixed column width (capped so the editor keeps its
// minimum working columns) when docked right.
func (m *findAllModal) width(a *App) int {
	band := a.editorBandCols()
	if !a.findAllDockRight {
		if band < 0 {
			return 0
		}
		return band
	}
	w := findAllRightCols
	if max := band - findAllMinEditorCols; w > max {
		w = max
	}
	// The floor is applied AFTER the editor's reserve, so on a band too
	// narrow for both the list keeps its minimum and the editor eats the
	// difference — the same precedence gitPanelHeight uses. A column too
	// narrow to read is worse than a narrow editor, and the real fix on
	// a window that small is hiding the sidebar.
	if w < findAllMinWidth {
		w = findAllMinWidth
	}
	if w > band {
		w = band // narrow window: take what there is
	}
	if w < 0 {
		w = 0
	}
	return w
}

// visibleRows is how many result rows fit in the current height.
func (m *findAllModal) visibleRows(a *App) int {
	h := m.height(a) - findAllChromeRows
	if h < 0 {
		return 0
	}
	return h
}

// rect is the popup's screen rectangle, in whichever dock is in force.
//
// TOP (default): the editor's full column band, directly under the tab
// bar, editor pushed down beneath it. Above rather than below because
// the list is what the user is reading and the code is the reference
// under it — and it's where the eye already is after the tab bar.
//
// RIGHT: a full-height column at the far end of the editor's band,
// editor narrowed to its left. `ex + ew` IS that edge, because
// editorRect has already subtracted the column's width.
func (m *findAllModal) rect(a *App) (x, y, w, h int) {
	ex, _, ew, _ := a.editorRect()
	if a.findAllDockRight {
		return ex + ew, 1, m.width(a), m.height(a)
	}
	return ex, 1, ew, m.height(a)
}

// findAllDockRect is the ◨ / ⬒ dock button in the title row, parked left
// of the "esc" hint drawFrame pins to the right edge. One source for
// draw and hit-test (the btnRect house rule), and the count and title
// are positioned off it so the three can't overlap.
func (m *findAllModal) dockRect(a *App) btnRect {
	mx, my, mw, _ := m.rect(a)
	// drawFrame's "esc " occupies the four columns ending at mx+mw-2.
	return btnRect{x: mx + mw - 5 - findAllDockBtnW, y: my + 1, w: findAllDockBtnW}
}

// findAllDockGlyph names the layout the button will switch TO, not the
// one in force — the action-not-state convention the ≡ toggle rows use.
// Half-filled squares because the glyph IS the layout: ◨ is a column on
// the right, ⬒ a strip across the top. Single-width, per the marker rule
// (a double-width emoji would overrun the row).
func (a *App) findAllDockGlyph() string {
	if a.findAllDockRight {
		return " ⬒ "
	}
	return " ◨ "
}

// setFindAllDock installs the dock preference and persists it. Same
// silent-degradation contract as every other ≡ toggle: an unwritable
// config flashes and the session keeps the new layout anyway.
func (a *App) setFindAllDock(right bool) {
	a.findAllDockRight = right
	dock := userconfig.FindAllDockTop
	if right {
		dock = userconfig.FindAllDockRight
	}
	if a.findAllDockRight {
		a.flash("Find all docks right — ⬒ or d for the top strip")
	} else {
		a.flash("Find all docks at the top — ◨ or d for the right column")
	}
	if err := userconfig.SaveFindAllDock(userconfig.DefaultPath(), dock); err != nil {
		a.flash("config: " + err.Error())
	}
}

// toggleFindAllDock flips the dock — the button, the `d` key, and the ≡
// row all land here.
func (a *App) toggleFindAllDock() { a.setFindAllDock(!a.findAllDockRight) }

// menuToggleFindAllDock is the ≡ View row. It exists because the ≡ menu
// can't be opened while the popup owns the keyboard: without it, a user
// whose terminal swallows clicks would have no way to reach the layout
// at all (macOS Terminal + tmux, the reason every action has a menu
// twin). Flipping it with the list open reflows on the next draw.
func (a *App) menuToggleFindAllDock() {
	a.closeMenu()
	a.toggleFindAllDock()
	// A dock change moves the band the preview was centered against.
	if m, ok := a.modal.(*findAllModal); ok {
		m.preview(a)
	}
}

// findAllDockToggleLabel names the layout the row will switch TO.
func (a *App) findAllDockToggleLabel() string {
	if a.findAllDockRight {
		return "Dock find-all results at top"
	}
	return "Dock find-all results right"
}

// rowIndexAt maps a screen cell to the result index drawn there, or -1
// for anything that isn't a row. The one geometry source for both the
// click and the hover, so they can't disagree about which row is which.
func (m *findAllModal) rowIndexAt(a *App, x, y int) int {
	mx, my, mw, mh := m.rect(a)
	if x < mx || x >= mx+mw || y < my+3 || y > my+mh-2 {
		return -1
	}
	idx := m.scroll + (y - (my + 3))
	if idx < 0 || idx >= len(m.rows) {
		return -1
	}
	return idx
}

// labelWidth is the width of the left column: enough for the widest row
// label in the list, floored so short files don't get a cramped gutter
// and capped in project mode, where a "path:line" label can otherwise run
// longer than the code it is supposed to be introducing.
//
// The cap is a share of the panel rather than a constant because the two
// docks differ by a factor of three in width — a column that leaves the
// top strip readable would leave the right dock with nothing but paths.
func (m *findAllModal) labelWidth(a *App) int {
	widest := 0
	for _, r := range m.rows {
		widest = max(widest, runeLen(r.label))
	}
	w := max(widest, findAllMinLineDigits)
	if m.project {
		w = min(w, max(m.width(a)*2/5, findAllMinLineDigits))
	}
	return w
}

// rowLabelText renders a row's left column, truncated to width. Project
// labels are cut from the FRONT (with a leading ellipsis) because the
// distinguishing part of a path is its tail — twenty rows all reading
// "internal/app/…" would say nothing at all.
func rowLabelText(label string, width int) string {
	runes := []rune(label)
	if len(runes) <= width {
		return label
	}
	if width <= 1 {
		return "…"
	}
	return "…" + string(runes[len(runes)-(width-1):])
}

// -----------------------------------------------------------------------------
// Input
// -----------------------------------------------------------------------------

// handleKey routes a keystroke while the list is up:
//
//	Esc              abort — put the cursor and viewport back
//	Enter            accept — keep the previewed position
//	Up/Down          preview the neighbouring hit
//	PgUp/PgDn        preview a page away
//	Home/End         preview the first / last hit
//	d                flip the dock (top strip ⇄ right column)
//
// Everything else is dropped: the list has no input field, and letting
// keys fall through to the buffer behind a popup that owns the screen
// would type into code the user can't see. `d` is the exception because
// the ≡ menu — the usual keyboard twin of a click — can't be opened
// while a modal holds the keyboard, which would leave the button as the
// ONLY way in on a terminal that eats clicks.
func (m *findAllModal) handleKey(a *App, ev *tcell.EventKey) {
	switch ev.Key() {
	case tcell.KeyRune:
		if r := ev.Rune(); r == 'd' || r == 'D' {
			a.toggleFindAllDock()
			m.preview(a) // the band it was centered against just changed
		}
	case tcell.KeyEsc:
		m.abort(a)
	case tcell.KeyEnter:
		m.accept(a)
	case tcell.KeyUp:
		m.moveSelection(a, -1)
	case tcell.KeyDown:
		m.moveSelection(a, 1)
	case tcell.KeyPgUp:
		m.moveSelection(a, -m.visibleRows(a))
	case tcell.KeyPgDn:
		m.moveSelection(a, m.visibleRows(a))
	case tcell.KeyHome:
		m.selectRow(a, 0)
	case tcell.KeyEnd:
		m.selectRow(a, len(m.rows)-1)
	}
}

// handleMouse implements the popup's non-modal half: a click on a row
// PREVIEWS it and leaves the list open, a second click on the same row
// within the double-click window accepts it, the wheel scrolls the list,
// and a click outside accepts (the click landed where the user wants to
// work, so settling there is the honest reading).
//
// The double-click record is the editor's own lastClick, reused so the
// two gestures share one window — the same trick the git panel plays.
func (m *findAllModal) handleMouse(a *App, x, y int, btn tcell.ButtonMask) {
	if btn&tcell.WheelUp != 0 {
		m.scrollList(a, -wheelLines)
		return
	}
	if btn&tcell.WheelDown != 0 {
		m.scrollList(a, wheelLines)
		return
	}
	if btn&tcell.Button1 == 0 {
		return
	}
	mx, my, mw, mh := m.rect(a)
	if x < mx || x >= mx+mw || y < my || y >= my+mh {
		m.accept(a)
		return
	}
	// The dock button is checked before the rows and returns without
	// touching lastClick — flipping the layout is not half a
	// double-click on whatever row lands under the pointer afterwards.
	if m.dockRect(a).contains(x, y) {
		a.toggleFindAllDock()
		m.preview(a)
		return
	}
	idx := m.rowIndexAt(a, x, y)
	if idx < 0 {
		return
	}
	now := time.Now()
	double := a.lastClick.x == x && a.lastClick.y == y && now.Sub(a.lastClick.when) < doubleClickMs
	if double {
		a.lastClick = clickRecord{} // a triple click isn't a second accept
	} else {
		a.lastClick = clickRecord{x: x, y: y, when: now}
	}
	m.selectRow(a, idx)
	if double {
		m.accept(a)
	}
}

// -----------------------------------------------------------------------------
// Drawing
// -----------------------------------------------------------------------------

// draw paints the popup.
//
// Layout (relative to the frame's top-left):
//
//	0        top border
//	1        title — Find all "query"    3/57  ◨  esc
//	2        divider
//	3..N     result rows —  123 │ compacted line text
//	N+1      bottom border, carrying the key hint
func (m *findAllModal) draw(a *App) {
	mx, my, mw, mh := m.rect(a)
	if mw < 12 || mh < findAllMinHeight {
		return
	}
	c := a.chrome()

	dock := m.dockRect(a)
	count := fmt.Sprintf("%d/%d ", m.selected+1, len(m.rows))
	countX := dock.x - runeLen(count)
	title := m.titleText()
	// Clip the title against everything to its right — the count, the
	// dock button, and drawFrame's "esc" — so a long query on a narrow
	// column can never overwrite them.
	if room := countX - (mx + 2); room > 0 && runeLen(title) > room {
		title = string([]rune(title)[:room-1]) + "…"
	}
	c.drawFrame(a.screen, mx, my, mw, mh, title)
	if countX > mx+1 {
		drawAt(a.screen, countX, my+1, count, c.muted)
	}
	// The dock button reads as a control, not as chrome — accent, like
	// the panels' header buttons.
	drawAt(a.screen, dock.x, dock.y, a.findAllDockGlyph(), c.title)

	vis := m.visibleRows(a)
	digits := m.labelWidth(a)
	for i := 0; i < vis; i++ {
		ry := my + 3 + i
		idx := m.scroll + i
		if idx >= len(m.rows) {
			// Clear the tail so a shorter scroll window doesn't leave
			// the previous frame's rows behind.
			for cx := mx + 1; cx < mx+mw-1; cx++ {
				a.screen.SetContent(cx, ry, ' ', nil, c.bgSt)
			}
			continue
		}
		m.drawRow(a, mx, ry, mw, digits, m.rows[idx], idx == m.selected)
	}

	// Footer hint, widest form that fits — the right-docked column has
	// less than half the room the top strip does, and a hint clipped
	// mid-word reads worse than a shorter one.
	for _, hint := range m.footerHints() {
		if mw > runeLen(hint)+6 {
			drawAt(a.screen, mx+2, my+mh-1, hint, c.muted)
			break
		}
	}
}

// drawRow paints one result: right-aligned line number, a rule, then the
// compacted text with the matched runes lit. The selected row flips its
// background to the editor's BG — the same block highlight the palette
// and finder use, so "which one am I on" reads the same everywhere.
func (m *findAllModal) drawRow(a *App, mx, ry, mw, digits int, r findAllRow, selected bool) {
	c := a.chrome()
	rowBG := c.bg
	numFG := a.theme.Muted
	if selected {
		rowBG = a.theme.BG
		numFG = a.theme.AccentSoft
	}
	rowStyle := tcell.StyleDefault.Background(rowBG).Foreground(a.theme.Text)
	numStyle := tcell.StyleDefault.Background(rowBG).Foreground(numFG)
	ruleStyle := tcell.StyleDefault.Background(rowBG).Foreground(a.theme.Subtle)
	hitStyle := tcell.StyleDefault.Background(rowBG).Foreground(a.theme.FindCurrent).Bold(true)

	for cx := mx + 1; cx < mx+mw-1; cx++ {
		a.screen.SetContent(cx, ry, ' ', nil, rowStyle)
	}

	// Right-aligned in file mode (numbers line up on their units digit),
	// LEFT-aligned in project mode: the paths are already truncated to a
	// common width from the front, and right-aligning them would put the
	// ellipses in a ragged column.
	num := rowLabelText(r.label, digits)
	numStart := mx + 2
	if !m.project {
		numStart += digits - runeLen(num)
	}
	drawAt(a.screen, numStart, ry, num, numStyle)
	a.screen.SetContent(mx+2+digits+1, ry, '│', nil, ruleStyle)

	textStart := mx + 2 + digits + 3
	maxCols := (mx + mw - 1) - textStart
	if maxCols <= 0 {
		return
	}
	for i, ch := range []rune(r.text) {
		if i >= maxCols {
			break
		}
		st := rowStyle
		if r.hitW > 0 && i >= r.hit && i < r.hit+r.hitW {
			st = hitStyle
		}
		a.screen.SetContent(textStart+i, ry, ch, nil, st)
	}
}
