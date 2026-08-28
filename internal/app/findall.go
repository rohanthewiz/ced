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
//
// …until it is PINNED. The interactive layer at the bottom of this file
// grew the popup into an optional results PANEL: pin it (◇ / p) and the
// same object moves from the modal slot into App.findAllPin, where it
// survives editor clicks and edits like the git panels do — plus a
// filter box narrowing the view (pre-filled with the search expression,
// so the question carries on from where it was asked), per-row dismissal
// (a worklist you burn down), replace-across-survivors through the
// workspace-edit machinery
// (one undo gesture), a re-run button, and stale-row dimming when the
// buffer moves under the list. Unpinned, the peek contract above is
// untouched.

package app

import (
	"fmt"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/gdamore/tcell/v2"

	"github.com/rohanthewiz/ced/internal/editor"
	"github.com/rohanthewiz/ced/internal/userconfig"
)

const (
	// findAllChromeRows is the non-row overhead of the popup: top
	// border, title, the filter/replace fields row, divider, bottom
	// border.
	findAllChromeRows = 5
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

	// dismissed marks a row the user struck off (the ✕ / Delete
	// gesture): it leaves the view but not the slice, so indices held
	// elsewhere stay valid and a re-run resets the worklist cleanly.
	dismissed bool
}

// Field focus for the list's two inputs. The list itself is the default
// owner; a field only holds the keyboard while the user is in it.
const (
	findAllFocusList = iota
	findAllFocusFilter
	findAllFocusReplace
)

// findAllModal is one Find-all session: the resolved result list, where
// the highlight sits, and — the part that makes Esc mean something —
// the exact view state the popup was opened from.
type findAllModal struct {
	query  string
	tabIdx int
	rows   []findAllRow

	// view is the display order: indices into rows that survive the
	// filter and the dismissals, rebuilt by rebuildView. selected and
	// scroll are VIEW positions — everything the user sees, points at,
	// or replaces goes through this indirection, so "the results" and
	// "the results still standing" can never disagree.
	view []int

	selected int
	scroll   int

	// pinned turns the popup into a docked panel (held in
	// App.findAllPin, not the modal slot): the editor gets its keyboard
	// and clicks back, and the list stays put as a worklist. Unpinned
	// keeps the original PEEK contract to the letter.
	pinned bool
	// focus names which input owns keys: the list (default), the
	// filter box, or the replace box.
	focus int
	// filter narrows the DISPLAYED rows (never re-runs the search) —
	// a second, cheaper question asked of results already in hand.
	filter textField
	// seed is what the filter box was PRE-FILLED with at open: the search
	// expression itself, so the list arrives holding the question it just
	// answered and typing on carries straight on narrowing it, rather than
	// starting from an empty box that first has to be re-typed.
	//
	// While the box still holds exactly the seed it narrows NOTHING — see
	// filterNeedle. Producers whose query is not text the rows contain
	// (the workspace-edit receipt, whose query is a LABEL) leave it empty.
	seed string
	// replace holds the replacement text for "Replace in N results".
	replace textField

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

// menuFindAll is the ≡ / Esc-F entry point: list every occurrence of a
// selected region, or ask what to look for (see openFindAll).
func (a *App) menuFindAll() {
	a.closeMenu()
	a.openFindAll()
}

// openFindAll resolves a query and shows the list. A single-line
// selection runs straight away; everything else goes through the
// prompt, pre-filled with the best guess the context offers. The prompt
// path is why this is split from showFindAll: promptModal closes itself
// before running its callback, so the callback lands in an empty modal
// slot.
func (a *App) openFindAll() {
	tab := a.activeTabPtr()
	if tab == nil || tab.IsImage() {
		return
	}
	if q := a.findAllSelectionQuery(); q != "" {
		a.showFindAll(q)
		return
	}
	// Read the seed BEFORE opening the prompt: openModal → closeAllModals
	// wipes the find bar's field, which is one of the things seeding it.
	// (Argument evaluation order already guarantees this; the local makes
	// the dependency visible so a later refactor can't quietly break it.)
	seed := a.findAllPromptSeed()
	a.openPrompt("Find all in file", "lists every occurrence", seed, func(app *App, v string) {
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

// findAllSelectionQuery is the ONE thing a find verb searches for
// without asking: a single-line selection. A highlighted region is an
// explicit gesture — the user pointed at the exact text — so a prompt
// there could only ever be answered "yes, that", which makes it a
// keystroke charged for nothing.
//
// Everything else the context merely IMPLIES (what's left in the find
// bar, the word the cursor happens to be sitting in), and an implication
// is a guess. Guesses belong in the prompt as a pre-fill
// (findAllPromptSeed), where Enter accepts one and typing replaces it —
// a search that silently answers a question the user didn't ask costs
// more than the key it saved, because the result list is indistinguish-
// able from a correct answer to the wrong query.
//
// A multi-line selection is deliberately NOT one of these: FindAll
// matches within a line, so a blob with newlines in it is not a search
// term. It falls through to the prompt like every other unclear case.
func (a *App) findAllSelectionQuery() string {
	tab := a.activeTabPtr()
	if tab == nil || tab.Buffer == nil || !tab.HasSelection() {
		return ""
	}
	sel := tab.SelectionText()
	if sel == "" || strings.ContainsRune(sel, '\n') {
		return ""
	}
	return sel
}

// findAllPromptSeed is what pre-fills the "what am I looking for?"
// prompt, in priority order: what's in the find bar (the user typed it,
// so it beats anything inferred), then the word under the cursor. Empty
// means the prompt opens blank — a cursor in whitespace has nothing to
// suggest.
func (a *App) findAllPromptSeed() string {
	if a.findOpen && len(a.findField.value) > 0 {
		return a.findField.String()
	}
	tab := a.activeTabPtr()
	if tab == nil || tab.Buffer == nil {
		return ""
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
		query:  query,
		tabIdx: a.activeTab,
		rows:   findAllRowsFor(tab.Buffer, matches),
		// The filter box opens holding the search expression, caret at the
		// end, so "/" and another keystroke keeps narrowing the same
		// question instead of restating it. Inert until edited — see seed.
		filter:        newTextField(query),
		seed:          query,
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
	m.rebuildView()
	// Start on the hit at or after the cursor — the same "nearest, not
	// first" rule the find bar uses, so opening the list from where you
	// are doesn't jump you to the top of the file. Fresh list: view
	// position and match index still coincide.
	if idx := editor.FirstMatchAtOrAfter(matches, tab.Cursor); idx > 0 {
		m.selected = idx
	}
	// A fresh list replaces any pinned survivor from an earlier search —
	// two lists answering different questions would fight over the band.
	a.dropFindAllPin()
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

// rebuildView recomputes the display order from the filter and the
// dismissals, keeping the highlight on the same underlying row when it
// survives and clamping it when it doesn't. Document order is
// preserved on purpose — the filter narrows the list, it does not
// re-rank it, so row N is always above row N+1 the way the file reads.
func (m *findAllModal) rebuildView() {
	prev := -1
	if m.selected >= 0 && m.selected < len(m.view) {
		prev = m.view[m.selected]
	}
	needle := m.filterNeedle()
	m.view = m.view[:0]
	for i := range m.rows {
		if m.rows[i].dismissed {
			continue
		}
		if needle != "" &&
			!strings.Contains(strings.ToLower(m.rows[i].text), needle) &&
			!strings.Contains(strings.ToLower(m.rows[i].label), needle) {
			continue
		}
		m.view = append(m.view, i)
	}
	m.selected = 0
	for vi, ri := range m.view {
		if ri >= prev && prev >= 0 {
			m.selected = vi
			break
		}
	}
	if m.selected >= len(m.view) {
		m.selected = len(m.view) - 1
	}
	if m.selected < 0 {
		m.selected = 0
	}
}

// filterNeedle is the text the view is currently narrowed by, folded for
// a case-insensitive match, or "" when the box is doing nothing.
//
// The box does nothing in two cases, and the second is the reason this is
// a method rather than an inline TrimSpace. An empty box is the obvious
// one. The other is a box still holding exactly the SEED — the query the
// search was run with — because a display row is compacted (leading
// indentation stripped, interior tabs rendered as one space), so a query
// carrying a tab, or one that matched inside the indentation, is not
// literally present in its own results. Filtering by it would open the
// list empty on the very search that produced every row in it. Editing
// the box by a single rune hands filtering over to the ordinary contains
// rule; in the overwhelmingly common case the two agree anyway, since a
// row's text does contain the query that found it.
func (m *findAllModal) filterNeedle() string {
	s := m.filter.String()
	if s == m.seed { // covers the empty box when nothing was seeded
		return ""
	}
	return strings.ToLower(strings.TrimSpace(s))
}

// viewRow returns the row behind view position vi, or nil.
func (m *findAllModal) viewRow(vi int) *findAllRow {
	if vi < 0 || vi >= len(m.view) {
		return nil
	}
	return &m.rows[m.view[vi]]
}

// selectedRow is the row under the highlight, or nil on an empty view.
func (m *findAllModal) selectedRow() *findAllRow { return m.viewRow(m.selected) }

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
	r := m.selectedRow()
	if tab == nil || r == nil {
		return
	}
	if ri := m.view[m.selected]; ri < len(tab.FindMatches) {
		tab.FindIndex = ri // paint this hit as the current one
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
// reads as "this is the place I wanted". A PINNED list commits the jump
// but stays put: it is a worklist, and accepting one item doesn't
// finish the list.
func (m *findAllModal) accept(a *App) {
	if m.project {
		m.openSelected(a)
		return
	}
	if m.pinned {
		return // the preview already parked the cursor; the panel stays
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

// selectRow highlights view row idx and previews it. The single write
// path for the selection, so keyboard and mouse can't disagree about
// what "selected" implies.
func (m *findAllModal) selectRow(a *App, idx int) {
	if len(m.view) == 0 {
		return
	}
	if idx < 0 {
		idx = 0
	}
	if idx >= len(m.view) {
		idx = len(m.view) - 1
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
	max := len(m.view) - m.visibleRows(a)
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
	m := a.findAllVisible()
	if m == nil || a.findAllDockRight {
		return 0
	}
	return m.height(a)
}

// findAllPanelWidth is the columns the right-docked list takes out of
// the editor band — 0 in the default top dock. The exact mirror of
// findAllPanelHeight, and exactly one of the two is ever non-zero.
func (a *App) findAllPanelWidth() int {
	m := a.findAllVisible()
	if m == nil || !a.findAllDockRight {
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

// pinRect is the ◇ / ◆ pin button, immediately left of the dock button.
// Same one-source-of-geometry rule as every button here.
func (m *findAllModal) pinRect(a *App) btnRect {
	d := m.dockRect(a)
	return btnRect{x: d.x - findAllDockBtnW, y: d.y, w: findAllDockBtnW}
}

// rerunRect is the ⟳ re-run button, left of the pin.
func (m *findAllModal) rerunRect(a *App) btnRect {
	p := m.pinRect(a)
	return btnRect{x: p.x - findAllDockBtnW, y: p.y, w: findAllDockBtnW}
}

// closePinRect is the pinned panel's ✕, drawn over the spot the "esc"
// hint occupies unpinned — Esc is not how a panel closes, and the cell
// should say what the truth is.
func (m *findAllModal) closePinRect(a *App) btnRect {
	mx, my, mw, _ := m.rect(a)
	return btnRect{x: mx + mw - 5, y: my + 1, w: 4}
}

// pinGlyph names the state the button switches TO (the ≡ toggle-row
// convention): ◇ pins, ◆ unpins. Single-width per the marker rule — the
// plan's 📌 is double-width and would overrun the row.
func (m *findAllModal) pinGlyph() string {
	if m.pinned {
		return " ◆ "
	}
	return " ◇ "
}

// findAllFields is the computed geometry of the filter/replace row:
// where each box and the replace button sit. Zero-width rects mean the
// row is too narrow for that piece (the right dock at minimum width) —
// the filter survives longest, being the cheaper, more-used question.
type findAllFields struct {
	y       int
	filter  btnRect
	replace btnRect
	button  btnRect
}

// fieldsLayout computes the row. One geometry source for draw AND the
// mouse (the btnRect rule), which matters more than usual here — a
// caret placed by clickAt against a rect the draw didn't use would put
// the cursor in the wrong cell.
func (m *findAllModal) fieldsLayout(a *App) findAllFields {
	mx, my, mw, _ := m.rect(a)
	f := findAllFields{y: my + 2}
	inner := mw - 4 // [mx+2, mx+mw-2)
	if inner < 16 {
		return f
	}
	btnLabel := m.replaceBtnLabel()
	btnW := runeLen(btnLabel)
	// Narrow row: the filter takes everything, replace waits for the
	// top dock (or a wider window).
	if inner < btnW+26 {
		f.filter = btnRect{x: mx + 4, y: f.y, w: inner - 2}
		return f
	}
	avail := inner - btnW - 7 // two 2-cell labels + gaps + button gap
	fw := avail * 2 / 5
	if fw < 8 {
		fw = 8
	}
	rw := avail - fw
	f.filter = btnRect{x: mx + 4, y: f.y, w: fw}
	f.replace = btnRect{x: f.filter.x + fw + 3, y: f.y, w: rw}
	f.button = btnRect{x: mx + mw - 2 - btnW, y: f.y, w: btnW}
	return f
}

// replaceBtnLabel counts what the button would actually touch — the
// surviving rows — so the label is the confirmation's first draft.
func (m *findAllModal) replaceBtnLabel() string {
	return fmt.Sprintf("[ Replace in %d ]", len(m.view))
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
	if x < mx || x >= mx+mw || y < my+4 || y > my+mh-2 {
		return -1
	}
	idx := m.scroll + (y - (my + 4))
	if idx < 0 || idx >= len(m.view) {
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

// handleKey routes a keystroke while the list owns the keyboard —
// the whole time unpinned, and only while a field is focused when
// pinned (the pinned panel's list is mouse-driven; the editor keeps
// its keys). List focus:
//
//	Esc              abort — put the cursor and viewport back
//	Enter            accept — keep the previewed position
//	Up/Down          preview the neighbouring hit
//	PgUp/PgDn        preview a page away
//	Home/End         preview the first / last hit
//	Delete           dismiss the highlighted row (the ✕ twin)
//	d                flip the dock (top strip ⇄ right column)
//	p                pin / unpin
//	/                jump to the filter box
//
// Field focus hands ordinary editing keys to the field; Enter and Esc
// return to the list (Enter in the replace box runs the replace).
// Everything else is dropped: letting keys fall through to the buffer
// behind a popup that owns the screen would type into code the user
// can't see. The letter keys exist because the ≡ menu — the usual
// keyboard twin of a click — can't be opened while a modal holds the
// keyboard, which would leave the buttons as the ONLY way in on a
// terminal that eats clicks.
func (m *findAllModal) handleKey(a *App, ev *tcell.EventKey) {
	if m.focus != findAllFocusList {
		m.handleFieldKey(a, ev)
		return
	}
	switch ev.Key() {
	case tcell.KeyRune:
		switch r := ev.Rune(); r {
		case 'd', 'D':
			a.toggleFindAllDock()
			m.preview(a) // the band it was centered against just changed
		case 'p', 'P':
			m.togglePin(a)
		case '/':
			m.focus = findAllFocusFilter
		}
	case tcell.KeyEsc:
		m.abort(a)
	case tcell.KeyEnter:
		m.accept(a)
	case tcell.KeyDelete, tcell.KeyBackspace, tcell.KeyBackspace2:
		m.dismissRow(a, m.selected)
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
		m.selectRow(a, len(m.view)-1)
	}
}

// handleFieldKey is the focused filter/replace box's share of the
// keyboard. Esc backs out to the list; Enter backs out too, and in the
// replace box it is the do-it gesture. Every edit to the filter
// re-narrows the view live — that immediacy is the whole point of a
// filter over a re-search.
func (m *findAllModal) handleFieldKey(a *App, ev *tcell.EventKey) {
	switch ev.Key() {
	case tcell.KeyEsc:
		m.focus = findAllFocusList
		return
	case tcell.KeyEnter:
		if m.focus == findAllFocusReplace {
			m.focus = findAllFocusList
			m.confirmReplace(a)
			return
		}
		m.focus = findAllFocusList
		return
	case tcell.KeyTab:
		// Tab hops between the two boxes — they're one row of form.
		if m.focus == findAllFocusFilter {
			m.focus = findAllFocusReplace
		} else {
			m.focus = findAllFocusFilter
		}
		return
	}
	field := &m.filter
	if m.focus == findAllFocusReplace {
		field = &m.replace
	}
	if _, edited := field.handleKey(ev); edited && m.focus == findAllFocusFilter {
		m.rebuildView()
		m.clampScroll(a)
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
		// Pinned, the panel is furniture: an outside click belongs to
		// whatever it landed on (the router only sends the panel its
		// own clicks, but belt and braces). Unpinned keeps the original
		// contract — the click landed where the user wants to work.
		if m.pinned {
			m.focus = findAllFocusList
			return
		}
		m.accept(a)
		return
	}
	// Buttons are checked before the rows and return without touching
	// lastClick — flipping a control is not half a double-click on
	// whatever row lands under the pointer afterwards.
	if m.pinned && m.closePinRect(a).contains(x, y) {
		m.closePin(a)
		return
	}
	if m.rerunRect(a).contains(x, y) {
		m.rerun(a)
		return
	}
	if m.pinRect(a).contains(x, y) {
		m.togglePin(a)
		return
	}
	if m.dockRect(a).contains(x, y) {
		a.toggleFindAllDock()
		m.preview(a)
		return
	}
	// The filter/replace row: clicking a box focuses it and places the
	// caret; clicking the button runs the replace.
	if f := m.fieldsLayout(a); y == f.y {
		switch {
		case f.filter.w > 0 && x >= f.filter.x-2 && x < f.filter.x+f.filter.w:
			m.focus = findAllFocusFilter
			m.filter.clickAt(f.filter.x, f.filter.x+f.filter.w, x)
		case f.replace.w > 0 && x >= f.replace.x-2 && x < f.replace.x+f.replace.w:
			m.focus = findAllFocusReplace
			m.replace.clickAt(f.replace.x, f.replace.x+f.replace.w, x)
		case f.button.contains(x, y):
			m.focus = findAllFocusList
			m.confirmReplace(a)
		}
		return
	}
	idx := m.rowIndexAt(a, x, y)
	if idx < 0 {
		return
	}
	m.focus = findAllFocusList
	// The ✕ zone at the row's right edge dismisses instead of selecting
	// — the worklist gesture.
	if x >= mx+mw-3 {
		m.dismissRow(a, idx)
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
//	1        title — Find all "query"   3/57 ⟳ ◇ ◨ esc|✕
//	2        fields — ⌕ filter…   ⇄ replace…   [ Replace in N ]
//	3        divider
//	4..N     result rows —  123 │ compacted line text          ✕
//	N+1      bottom border, carrying the key hint
func (m *findAllModal) draw(a *App) {
	mx, my, mw, mh := m.rect(a)
	if mw < 12 || mh < findAllMinHeight {
		return
	}
	c := a.chrome()

	rerun := m.rerunRect(a)
	count := fmt.Sprintf("%d/%d ", m.selected+1, len(m.view))
	if len(m.view) == 0 {
		count = "0/0 "
	}
	countX := rerun.x - runeLen(count)
	title := m.titleText()
	// Clip the title against everything to its right — the count, the
	// buttons, and drawFrame's "esc" — so a long query on a narrow
	// column can never overwrite them.
	if room := countX - (mx + 2); room > 0 && runeLen(title) > room {
		title = string([]rune(title)[:room-1]) + "…"
	}
	c.drawFrame(a.screen, mx, my, mw, mh, title)
	if countX > mx+1 {
		drawAt(a.screen, countX, my+1, count, c.muted)
	}
	// The buttons read as controls, not as chrome — accent, like the
	// panels' header buttons. Re-run, pin, dock, right to left toward
	// the corner.
	drawAt(a.screen, rerun.x, rerun.y, " ⟳ ", c.title)
	pin := m.pinRect(a)
	drawAt(a.screen, pin.x, pin.y, m.pinGlyph(), c.title)
	dock := m.dockRect(a)
	drawAt(a.screen, dock.x, dock.y, a.findAllDockGlyph(), c.title)
	if m.pinned {
		// A pinned panel doesn't answer to Esc — replace drawFrame's
		// hint with the ✕ that actually closes it.
		cl := m.closePinRect(a)
		drawAt(a.screen, cl.x, cl.y, " ✕  ", c.title)
	}

	m.drawFields(a, c)
	drawHDivider(a.screen, mx, my+3, mw, c.border)

	vis := m.visibleRows(a)
	digits := m.labelWidth(a)
	for i := 0; i < vis; i++ {
		ry := my + 4 + i
		idx := m.scroll + i
		if idx >= len(m.view) {
			// Clear the tail so a shorter scroll window doesn't leave
			// the previous frame's rows behind.
			for cx := mx + 1; cx < mx+mw-1; cx++ {
				a.screen.SetContent(cx, ry, ' ', nil, c.bgSt)
			}
			continue
		}
		r := m.rows[m.view[idx]]
		m.drawRow(a, mx, ry, mw, digits, r, idx == m.selected, m.rowStale(a, r))
	}
	if len(m.view) == 0 && vis > 0 {
		msg := "no rows match — edit the filter, or ⟳ to re-run"
		if m.filterNeedle() == "" {
			msg = "every row dismissed — ⟳ re-runs the search"
		}
		drawStatusText(a.screen, mx+2, my+4, mw-4, msg, c.muted)
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

// drawFields paints the filter/replace row over the divider drawFrame
// stamped at my+2 (the divider moves down one row — see draw's layout
// map). The boxes render on the editor BG so they read as inputs
// against the modal chrome; the focused one owns the terminal caret.
func (m *findAllModal) drawFields(a *App, c modalChrome) {
	mx, my, mw, _ := m.rect(a)
	// Reclaim the divider row: interior back to chrome bg, border cells
	// back to plain verticals.
	for cx := mx + 1; cx < mx+mw-1; cx++ {
		a.screen.SetContent(cx, my+2, ' ', nil, c.bgSt)
	}
	a.screen.SetContent(mx, my+2, '│', nil, c.border)
	a.screen.SetContent(mx+mw-1, my+2, '│', nil, c.border)

	f := m.fieldsLayout(a)
	if f.filter.w <= 0 {
		return
	}
	fieldSt := tcell.StyleDefault.Background(a.theme.BG).Foreground(a.theme.Text)
	drawAt(a.screen, f.filter.x-2, f.y, "⌕ ", c.muted)
	m.filter.draw(a.screen, f.y, f.filter.x, f.filter.x+f.filter.w, fieldSt, m.focus == findAllFocusFilter)
	if m.filter.String() == "" && m.focus != findAllFocusFilter {
		drawAt(a.screen, f.filter.x, f.y, "filter…", tcell.StyleDefault.Background(a.theme.BG).Foreground(a.theme.Muted))
	}
	if f.replace.w > 0 {
		drawAt(a.screen, f.replace.x-2, f.y, "⇄ ", c.muted)
		m.replace.draw(a.screen, f.y, f.replace.x, f.replace.x+f.replace.w, fieldSt, m.focus == findAllFocusReplace)
		if m.replace.String() == "" && m.focus != findAllFocusReplace {
			drawAt(a.screen, f.replace.x, f.y, "replace…", tcell.StyleDefault.Background(a.theme.BG).Foreground(a.theme.Muted))
		}
	}
	if f.button.w > 0 {
		drawAt(a.screen, f.button.x, f.y, m.replaceBtnLabel(), c.title)
	}
}

// drawRow paints one result: right-aligned line number, a rule, then the
// compacted text with the matched runes lit. The selected row flips its
// background to the editor's BG — the same block highlight the palette
// and finder use, so "which one am I on" reads the same everywhere.
func (m *findAllModal) drawRow(a *App, mx, ry, mw, digits int, r findAllRow, selected, stale bool) {
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
	if stale {
		// The buffer moved under this row: its text and coordinates are
		// history. Dimming says "roughly here, re-run for the truth"
		// without pretending the row can still be trusted.
		rowStyle = tcell.StyleDefault.Background(rowBG).Foreground(a.theme.Muted)
		hitStyle = rowStyle.Bold(true)
	}

	for cx := mx + 1; cx < mx+mw-1; cx++ {
		a.screen.SetContent(cx, ry, ' ', nil, rowStyle)
	}
	// The per-row ✕ — the dismiss target the mouse router honors for
	// the row's last three cells. Muted until the row is selected, so a
	// hundred ✕s don't shout over the code they sit beside.
	xFG := a.theme.Muted
	if selected {
		xFG = a.theme.Accent
	}
	a.screen.SetContent(mx+mw-2, ry, '✕', nil,
		tcell.StyleDefault.Background(rowBG).Foreground(xFG))

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
	// Two cells short of the border: the last interior cell is the ✕.
	maxCols := (mx + mw - 3) - textStart
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

// -----------------------------------------------------------------------------
// Interactive layer: pin, filter, dismiss, re-run, replace
// -----------------------------------------------------------------------------
//
// The list grew from a peek popup into a results PANEL (the plan's
// "fully interactive find-all"): pin it and it survives editor clicks
// and edits like the git panels do; filter it without re-searching;
// strike rows off and burn the remainder down; replace across what
// survives as one undoable gesture; re-run when the buffer has moved
// under it. Unpinned, the original peek contract is untouched.

// findAllVisible returns whichever find-all list is on screen — the
// modal-slot one or the pinned panel — or nil. The geometry helpers
// (findAllPanelHeight/Width) and the draw/mouse/key hooks all go
// through this, so the two homes share every behavior.
func (a *App) findAllVisible() *findAllModal {
	if m, ok := a.modal.(*findAllModal); ok {
		return m
	}
	return a.findAllPin
}

// dropFindAllPin closes a pinned panel outright — a fresh search
// replaces it, and closing the app-side pointer must also hand back the
// borrowed find tint.
func (a *App) dropFindAllPin() {
	if a.findAllPin == nil {
		return
	}
	m := a.findAllPin
	a.findAllPin = nil
	m.restoreFind(a)
}

// togglePin moves the list between the modal slot and the pinned-panel
// slot. Pinning frees the modal slot (the editor gets its keyboard and
// clicks back); unpinning re-enters it, restoring the peek contract.
// The borrowed find tint travels with the list either way — it lives
// exactly as long as the list that explains it.
func (m *findAllModal) togglePin(a *App) {
	if m.pinned {
		m.pinned = false
		a.findAllPin = nil
		a.openModal(m) // closeAllModals inside is a no-op here: the slot is free
		return
	}
	m.pinned = true
	m.focus = findAllFocusList
	a.modal = nil // deliberate: closeModal semantics without the name saying "dismiss"
	a.findAllPin = m
	a.flash("Find all pinned — it stays while you edit · ◆ or p unpins")
}

// closePin dismisses the pinned panel — its ✕ button. No view restore:
// every jump a pinned panel made was a committed one.
func (m *findAllModal) closePin(a *App) {
	a.findAllPin = nil
	m.restoreFind(a)
}

// dismissRow strikes view row vi off the worklist. The row is marked,
// not removed, so a re-run resurrects it cleanly.
func (m *findAllModal) dismissRow(a *App, vi int) {
	r := m.viewRow(vi)
	if r == nil {
		return
	}
	r.dismissed = true
	m.rebuildView()
	m.clampScroll(a)
	if len(m.view) == 0 {
		a.flash("All results dismissed — ⟳ re-runs the search")
	}
}

// rerun executes the same question again. In-file it is synchronous —
// fresh matches against the buffer as it is now, dismissals reset,
// filter kept (it narrows the new answer the way it narrowed the old).
// A project search re-runs through the async pipeline, whose arrival
// replaces this list wholesale; a producer that is not a text search
// (heading != "") has nothing to re-run and says so.
func (m *findAllModal) rerun(a *App) {
	if m.project {
		if m.heading != "" {
			a.flash("This list came from the language server — re-run it from the menu")
			return
		}
		a.startProjectSearch(m.query)
		return
	}
	tab := m.tab(a)
	if tab == nil || tab.Buffer == nil {
		return
	}
	matches := editor.FindAll(tab.Buffer, m.query)
	if len(matches) == 0 {
		a.flash(fmt.Sprintf("Find all: no occurrences of %q anymore", m.query))
		if m.pinned {
			m.closePin(a)
		} else {
			m.abort(a)
		}
		return
	}
	m.rows = findAllRowsFor(tab.Buffer, matches)
	tab.SetFindQuery(m.query)
	m.rebuildView()
	m.scroll = 0
	m.selectRow(a, editor.FirstMatchAtOrAfter(matches, tab.Cursor))
}

// rowStale reports whether the buffer has moved under an in-file row:
// the match range no longer holds the text the row was built from. A
// stale row still answers "roughly where", so it dims instead of
// vanishing — and replace skips it, because its coordinates are lies.
// Project rows never report stale; there is no buffer behind them to
// check against, and reading files per frame is not a price a draw
// may pay.
func (m *findAllModal) rowStale(a *App, r findAllRow) bool {
	if m.project {
		return false
	}
	tab := m.tab(a)
	if tab == nil || tab.Buffer == nil {
		return true
	}
	if r.line < 0 || r.line >= tab.Buffer.LineCount() {
		return true
	}
	line := tab.Buffer.LineRunes(r.line)
	if r.col < 0 || r.col+r.width > len(line) {
		return true
	}
	return string(line[r.col:r.col+r.width]) != m.query
}

// confirmReplace is the "Replace in N results" gesture: plan the edit
// over every SURVIVING, non-stale row, say exactly what will happen
// (file and match counts, where the bytes land), and only then commit —
// through the workspace-edit machinery, so the whole thing is one undo
// gesture with the same journal, rollback, and report every server
// edit gets.
func (m *findAllModal) confirmReplace(a *App) {
	replacement := m.replace.String()
	if replacement == m.query {
		a.flash("Replace: the replacement is the search text")
		return
	}
	plan, skipped, err := m.buildReplacePlan(a, replacement)
	if err != nil {
		a.flash("Replace: " + err.Error())
		return
	}
	if plan == nil || plan.count == 0 {
		a.flash("Replace: no live results to change")
		return
	}
	msg := fmt.Sprintf("%q becomes %q — %s in %s.",
		m.query, replacement,
		plural(plan.count, "occurrence", "occurrences"),
		plural(len(plan.files), "file", "files"))
	if plan.toDisk > 0 {
		msg += fmt.Sprintf(" %s not open in a tab will be written to disk now.",
			plural(plan.toDisk, "file", "files"))
	} else {
		msg += " Buffers are edited unsaved."
	}
	if skipped > 0 {
		msg += fmt.Sprintf(" %s skipped (the text changed under them).",
			plural(skipped, "stale row", "stale rows"))
	}
	wasPinned := m.pinned
	a.openConfirm("Replace in results", msg, func(app *App) {
		if ok, _ := app.commitWorkspaceEdit(plan); ok {
			// The rows this list held now describe text that no longer
			// exists. Re-running is the honest refresh — and for the
			// unpinned modal the confirm displaced, reopening it on
			// stale rows would be worse than closing.
			if wasPinned {
				m.rerun(app)
			} else {
				m.restoreFind(app)
			}
		}
	})
	// The confirm modal displaced an unpinned list (single slot); its
	// borrowed tint must not outlive it if the user cancels. A pinned
	// panel is untouched — it lives outside the slot.
	if !wasPinned {
		m.restoreFind(a)
	}
}

// buildReplacePlan turns the surviving view rows into a validated
// wsEditPlan: rune-coordinate edits grouped per file, open tabs edited
// in the buffer, everything else loaded through the same guards a real
// open gets (planDocument's contract, minus the LSP coordinate round
// trip — these positions were measured against these buffers directly).
// Returns how many stale rows were skipped alongside the plan.
func (m *findAllModal) buildReplacePlan(a *App, replacement string) (*wsEditPlan, int, error) {
	type fileEdits struct {
		tab   *editor.Tab
		open  bool
		edits []editor.Edit
	}
	perFile := map[string]*fileEdits{}
	order := []string{}
	skipped := 0

	for _, ri := range m.view {
		r := m.rows[ri]
		var (
			path string
			tab  *editor.Tab
			open bool
		)
		if m.project {
			path = r.path
			if t := a.tabByPath(path); t != nil {
				tab, open = t, true
			}
		} else {
			tab, open = m.tab(a), true
			if tab == nil {
				return nil, 0, fmt.Errorf("the tab this list was opened against is gone")
			}
			if tab.Path == "" {
				return nil, 0, fmt.Errorf("save the buffer first — replace needs a file behind it")
			}
			path = tab.Path
		}
		fe, ok := perFile[path]
		if !ok {
			if tab == nil {
				t, err := editor.NewTab(path)
				if err != nil {
					return nil, 0, fmt.Errorf("%s: %w", filepath.Base(path), err)
				}
				tab = t
			}
			fe = &fileEdits{tab: tab, open: open}
			perFile[path] = fe
			order = append(order, path)
		}
		// Staleness against the buffer that will actually be edited —
		// same check rowStale draws with, generalized to any file.
		line := []rune{}
		if r.line >= 0 && r.line < fe.tab.Buffer.LineCount() {
			line = fe.tab.Buffer.LineRunes(r.line)
		}
		if r.col < 0 || r.col+r.width > len(line) ||
			string(line[r.col:r.col+r.width]) != m.query {
			skipped++
			continue
		}
		fe.edits = append(fe.edits, editor.Edit{
			Start:   editor.Position{Line: r.line, Col: r.col},
			End:     editor.Position{Line: r.line, Col: r.col + r.width},
			NewText: replacement,
		})
	}

	plan := &wsEditPlan{label: fmt.Sprintf("Replace %q", m.query)}
	for _, path := range order {
		fe := perFile[path]
		if len(fe.edits) == 0 {
			continue
		}
		editor.NormalizeEdits(fe.edits)
		if i := editor.OverlapIndex(fe.edits); i >= 0 {
			return nil, 0, fmt.Errorf("overlapping matches at line %d", fe.edits[i].Start.Line+1)
		}
		plan.files = append(plan.files, wsFilePlan{
			path: path, tab: fe.tab, open: fe.open, edits: fe.edits,
		})
		plan.count += len(fe.edits)
		if !fe.open {
			plan.toDisk++
		}
	}
	return plan, skipped, nil
}

// findAllPinContains reports whether (x, y) falls on the pinned panel —
// the mouse router's gate, mirroring gitPanelContains and friends.
func (a *App) findAllPinContains(x, y int) bool {
	if a.findAllPin == nil {
		return false
	}
	mx, my, mw, mh := a.findAllPin.rect(a)
	return x >= mx && x < mx+mw && y >= my && y < my+mh
}
