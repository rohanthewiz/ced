// =============================================================================
// File: internal/app/palette.go
// Author: Rohan Allison <rohanthewiz@gmail.com>
// Created: 2026-07-08
// Copyright: 2026 Rohan Allison. All rights reserved.
// =============================================================================

// palette.go implements the command palette — a fuzzy-searchable list
// of every action the editor can run right now. It reuses the finder's
// fzy scorer (internal/finder) over action labels instead of paths, so
// the interaction grammar is identical to "Find file in project": type
// to filter, ↑/↓ to move, Enter to run, Esc to dismiss.
//
// The palette is deliberately built on a pluggable-source seam:
// paletteSources() returns a list of functions that each contribute
// items. One source exists today: the action menu inventory (built-ins
// + custom actions, via menuLayout). The finder's file index used to be
// a second source, but in a large repo tens of thousands of path rows
// drowned the few dozen actions the palette exists to reach — any query
// short enough to type fuzzily matched hundreds of paths. Files belong
// to "Find file in project" (the finder modal), which is built for that
// scale. Later sources (LSP symbols, …) can still merge into the same
// ranked list without touching the modal, provided they stay small.
//
// The modal itself doubles as a generic picker: openPicker shows a
// caller-supplied item list under its own title (the branch switcher
// uses this), reusing the palette's whole interaction grammar for free.

package app

import (
	"fmt"
	"sort"

	"github.com/gdamore/tcell/v2"
	"github.com/rohanthewiz/ced/internal/finder"
)

const (
	// paletteMaxWidth caps the modal width. Action labels are far
	// shorter than file paths, so the palette sits narrower than the
	// finder (80) — 60 keeps long custom-action labels comfortable
	// without a strip of dead space.
	paletteMaxWidth = 60
	// paletteResultsVisible mirrors finderResultsVisible so the two
	// fuzzy modals feel like the same instrument in different keys.
	paletteResultsVisible = 10
	// paletteMenuLabel is the palette's own row in the action menu.
	// The action source skips this label so the palette can't list
	// itself — "Command palette → Command palette" is a hall of
	// mirrors nobody needs.
	paletteMenuLabel = "Command palette"
)

// paletteItem is one runnable palette entry: the label the user
// searches against and the action that fires on Enter/click.
type paletteItem struct {
	label string
	run   func(*App)
	// spacer marks a thin divider row between sections of a picker (the
	// Recent folders picker's recent / frequent halves). It is not a
	// choice: it is never selected, never run, never counted, and it
	// only appears while the query is empty — once the user is
	// filtering, the list is ranked by score and "sections" no longer
	// describe it.
	spacer bool
	// heading, on a spacer row, turns the dotted rule into a SECTION
	// HEADING: the name (and count) of the rows below it, then the rule
	// (Recent folders' "MRU 5 ┄┄┄ ⧉ copy alt+r"). Everything a spacer
	// is still holds — never selected, never counted, gone while
	// filtering — so a heading is a labelled divider, not a new row kind
	// every list walk has to learn about.
	heading string
	// copy, on a heading row, is the section's ⧉ button: a click on it
	// (headingCopyRect) or copyKey with Alt runs it. The picker stays
	// open — copying a list is a side errand, not a pick. Alt because a
	// modal owns the keyboard (plain runes edit the query) and can't
	// reach the ≡ menu, so the button needs an in-surface key twin.
	copy    func(*App)
	copyKey rune
	// swatch, when set, is drawn as a two-cell block of that colour in
	// front of the label: a row that offers a COLOUR (the tab-group
	// colour picker) shows it instead of only naming it. It's not part
	// of the label, so fuzzy matching and hit indexes are unchanged.
	// tcell.ColorDefault (the zero value) means no swatch.
	swatch tcell.Color
}

// paletteSwatchW is the cells a swatch takes: the block plus a gap.
const paletteSwatchW = 3

// paletteSpacer is the one spelling of a divider row.
func paletteSpacer() paletteItem { return paletteItem{spacer: true} }

// paletteHeading is a divider row that names its section, with an
// optional ⧉ copy button (copy nil = no button) and its Alt key twin.
func paletteHeading(heading string, copyKey rune, copy func(*App)) paletteItem {
	return paletteItem{spacer: true, heading: heading, copy: copy, copyKey: copyKey}
}

// paletteCopyGlyph is the heading's copy button glyph — statusCopyGlyph,
// the editor's one symbol for "take this with you".
const paletteCopyGlyph = statusCopyGlyph

// paletteMatch pairs an item with its fuzzy score and matched rune
// indexes for the current query, so the renderer can highlight which
// characters lined up — same scannability trick as the finder.
type paletteMatch struct {
	item  paletteItem
	score int
	hits  []int
}

// paletteSource contributes items to the palette. Sources are invoked
// once per open (not per keystroke) — the inventory is small and
// stable while a modal owns the input, so there's nothing to gain
// from re-collecting on every edit.
type paletteSource func(a *App) []paletteItem

// paletteSources returns the registered item sources in merge order.
// On an empty query the palette reads top-down as the action menu, and
// refresh's stable sort breaks score ties in source order. Project
// files are deliberately NOT a source (see the file header): an
// unbounded source would bury the actions in a large repo.
func paletteSources() []paletteSource {
	return []paletteSource{paletteActionItems}
}

// paletteActionItems adapts the action-menu inventory (built-in groups
// + custom actions) into palette items. It flattens visibleMenuGroups
// rather than menuLayout on purpose: the palette must list every enabled
// action regardless of which ≡-menu sections are folded, and must never
// surface the synthetic section-header rows menuLayout stamps in. Only
// actions whose enabled predicate passes right now are listed — a
// palette that offers "Undo" with nothing to undo just teaches the user
// that Enter sometimes does nothing. Dynamic labels (labelFor) are
// resolved here so toggles read correctly ("Hide file explorer" vs
// "Show file explorer").
func paletteActionItems(a *App) []paletteItem {
	var out []paletteItem
	for _, g := range a.visibleMenuGroups() {
		for _, it := range g.items {
			if !it.enabled(a) {
				continue
			}
			label := it.label
			if it.labelFor != nil {
				label = it.labelFor(a)
			}
			// A ≡ twin (the File copy of Edit's Select all) repeats a row
			// listed elsewhere; two identical palette rows read as a bug.
			if label == paletteMenuLabel || it.paletteTwin {
				continue
			}
			out = append(out, paletteItem{label: label, run: it.action})
		}
	}
	return out
}

// paletteModal is the transient UI state of one palette session: the
// query field, the highlighted row, the gathered inventory, and the
// scored view of it for the current query.
type paletteModal struct {
	// title is the frame heading — paletteMenuLabel for the real
	// palette, the caller's choice for pickers ("Switch branch").
	title string
	// sourced marks a modal whose items came from paletteSources (the
	// real palette) rather than a caller-owned picker list. Picker items
	// must never be re-collected from the sources.
	sourced  bool
	field    textField
	selected int
	items    []paletteItem
	matches  []paletteMatch

	// top is the first match drawn. The palette used to show only the
	// first rows and ask the user to refine the query; a picker whose
	// sections run past the fold (5 recent + a spacer + 10 frequent)
	// needs the arrow keys to reach its tail, so the window follows the
	// selection instead.
	top int
	// rows, when non-zero, asks for that many visible rows instead of
	// paletteResultsVisible — for a picker whose whole list is meant to
	// be read at a glance. Still clamped to the window.
	rows int

	// cancel, when set, runs after the modal is dismissed WITHOUT
	// running an item — Esc or a click outside. Pickers whose caller
	// must always receive an answer (the chat permission prompt has an
	// agent blocked on the reply) set it via openPickerWithCancel;
	// ordinary pickers leave it nil and dismissal stays a plain no-op.
	cancel func(*App)

	// panes, when it holds two or more lists, splits a picker into named
	// lists shown ONE AT A TIME behind a switcher on the title row (Recent
	// folders: Local / Global). Stacking two lists in one scroll buried
	// the second below the fold whenever the first was long. m.items is
	// always the ACTIVE pane's list, so filtering, counting, scrolling,
	// drawing and hit-testing stay pane-blind; only the switcher knows.
	panes []palettePane
	// pane indexes panes: the list m.items currently holds.
	pane int
}

// palettePane is one named list of a multi-pane picker.
type palettePane struct {
	name  string
	items []paletteItem
}

// openPalette gathers items from every source and shows the palette.
// It no longer touches the file index: with files out of the palette,
// kicking a background walk of the whole project on every open would
// be pure cost.
func (a *App) openPalette() {
	m := &paletteModal{title: paletteMenuLabel, sourced: true}
	a.openModal(m)
	m.collectItems(a)
	m.refresh()
}

// openPicker shows a caller-supplied item list under its own title —
// the palette modal reused as a generic fuzzy chooser. Items are taken
// as-is; sources are not consulted.
func (a *App) openPicker(title string, items []paletteItem) {
	a.openPickerWithCancel(title, items, nil)
}

// openPickerWithCancel is openPicker for callers that must hear about a
// dismissal: cancel runs when the picker is closed without a pick (Esc,
// click outside). The chat permission prompt uses this — an agent is
// blocked on the answer, so "no pick" still has to send one. Returns
// the modal so the caller can recognise it later (e.g. to close it when
// the underlying request dies).
func (a *App) openPickerWithCancel(title string, items []paletteItem, cancel func(*App)) *paletteModal {
	return a.openPickerRows(title, items, 0, cancel)
}

// openPickerRows is openPickerWithCancel with a requested row count (0 =
// the palette's usual ten), for a picker meant to show its whole list.
func (a *App) openPickerRows(title string, items []paletteItem, rows int, cancel func(*App)) *paletteModal {
	m := &paletteModal{title: title, items: items, cancel: cancel, rows: rows}
	a.openModal(m)
	m.refresh()
	return m
}

// openPickerPanes opens a picker over several named lists, showing
// panes[start] first. Tab / Shift-Tab or a click on a pane's tab switches
// lists. rows is fixed across every pane (callers size it for the
// longest) so switching never moves the frame, and with it the tab you
// just clicked, out from under the pointer.
func (a *App) openPickerPanes(title string, panes []palettePane, start, rows int) *paletteModal {
	m := &paletteModal{title: title, panes: panes, rows: rows}
	if start >= 0 && start < len(panes) {
		m.pane = start
	}
	if len(panes) > 0 {
		m.items = panes[m.pane].items
	}
	a.openModal(m)
	m.refresh()
	return m
}

// switchPane shows pane i (wrapping either way, so Tab and Shift-Tab
// cycle). The query is KEPT: the usual reason to switch is "not in this
// list — is it in the other one?", and retyping would punish that. The
// highlight restarts at the top since the old index means nothing here.
func (m *paletteModal) switchPane(i int) {
	n := len(m.panes)
	if n < 2 {
		return
	}
	i = ((i % n) + n) % n
	if i == m.pane {
		return
	}
	m.pane = i
	m.items = m.panes[i].items
	m.selected, m.top = 0, 0
	m.refresh()
}

// paneCount is how many rows pane i would show for the current query —
// the number on its tab. The active pane's count comes from the live
// matches; another pane is scored on the spot (these lists are a few
// dozen rows, and a draw is far rarer than that is costly). Counting the
// inactive panes under the query is what makes the tabs useful while
// filtering: "Global 2" says the folder is one Tab away.
func (m *paletteModal) paneCount(i int) int {
	if i == m.pane {
		return m.countMatches()
	}
	query := m.field.String()
	if query == "" {
		return countItems(m.panes[i].items)
	}
	// Counted the way refresh lists: the contiguous hits when there are
	// any, else every match — so the number on an inactive tab is the
	// row count that tab would show after a switch.
	all, contiguous := 0, 0
	for _, it := range m.panes[i].items {
		if it.spacer {
			continue
		}
		score, _ := finder.Score(query, it.label)
		if score == 0 {
			continue
		}
		all++
		if finder.IsContiguous(query, score) {
			contiguous++
		}
	}
	if contiguous > 0 {
		return contiguous
	}
	return all
}

// paneTabs lays out the pane switcher on the title row — the ONE geometry
// both draw and handleMouse read. Each tab is " name count ", one cell
// apart, starting two cells after the title:
//
//	┌──────────────────────────────────────────────────────┐
//	│ Recent folders   Local 15  Global 31            esc │
//	├──────────────────────────────────────────────────────┤
//
// When the tabs and the title don't both fit before "esc", the TITLE gives
// way (showTitle false) — the tabs are the controls. A tab that still
// overruns is clipped, and any after it get no rect; Tab still reaches
// them. Returns nothing for a single-list picker.
func (m *paletteModal) paneTabs(a *App) (labels []string, rects []btnRect, showTitle bool) {
	showTitle = true
	if len(m.panes) < 2 {
		return nil, nil, true
	}
	mx, my, mw, _ := m.rect(a)
	labels = make([]string, len(m.panes))
	total := len(m.panes) - 1 // the one-cell gaps
	for i, p := range m.panes {
		labels[i] = fmt.Sprintf(" %s %d ", p.name, m.paneCount(i))
		total += runeLen(labels[i])
	}
	// right is exclusive: one blank cell before drawFrame's "esc " hint.
	right := mx + mw - 1 - runeLen("esc ") - 1
	x := mx + 2 + runeLen(m.title) + 2
	if x+total > right {
		x = mx + 2
		showTitle = false
	}
	for _, l := range labels {
		w := min(runeLen(l), right-x)
		if w <= 0 {
			break
		}
		rects = append(rects, btnRect{x: x, y: my + 1, w: w})
		x += w + 1
	}
	return labels, rects, showTitle
}

// drawPaneTabs paints the switcher. The active tab takes the selected
// row's block (theme BG under the title colour, bold) — the one "this is
// the chosen one" look the modal already has, tuned per theme to stand off
// the modal background. Inactive tabs are muted like the esc hint: still
// readable, clearly not where the list below came from.
func (m *paletteModal) drawPaneTabs(a *App, labels []string, rects []btnRect) {
	c := a.chrome()
	active := c.title.Background(a.theme.BG).Bold(true)
	for i, r := range rects {
		st := c.muted
		if i == m.pane {
			st = active
		}
		drawAt(a.screen, r.x, r.y, string([]rune(labels[i])[:r.w]), st)
	}
}

// collectItems (re)gathers the inventory from every registered source.
// Kept idempotent (hence the slice reset) so a future source that
// streams in late can re-collect without duplicating rows.
func (m *paletteModal) collectItems(a *App) {
	m.items = m.items[:0]
	for _, src := range paletteSources() {
		m.items = append(m.items, src(a)...)
	}
}

// menuCommandPalette is the ≡ menu entry that opens the palette —
// every action stays reachable from the main menu per the project's
// mouse-first rule; Esc-a is the shortcut, not the primary surface.
func (a *App) menuCommandPalette() {
	a.closeMenu()
	a.openPalette()
}

// menuSearchFrom is what typing does while the ≡ menu is open: the menu
// switches into fuzzy-search mode, seeded with the rune that was just
// typed so the keystroke is never lost. It IS the palette's action
// source under a different title — every group's rows regardless of
// fold state, minus the file index, because the user was just LOOKING
// at the menu and the question being answered is "where did that row
// go", not "open me a file". The title says how the mode was entered so
// the discovery teaches itself.
func (a *App) menuSearchFrom(r rune) {
	a.closeMenu()
	m := a.openPickerWithCancel("Search the menu", paletteActionItems(a), nil)
	m.field = newTextField(string(r))
	m.refresh()
}

// refresh re-scores the inventory against the current query. An empty
// query lists everything in source order (which mirrors the action
// menu's own order, so the palette doubles as a menu you can read);
// otherwise items are ranked by fzy score, with source order breaking
// ties via the stable sort.
//
// Like the file finder (finder.rank), a query that some row holds WHOLE
// hides the rows that only match it scattered. Measured on the ≡ menu's
// 178 labels, the scattered tail under a real word is unrelated rows
// ("ref" → Find references… plus 20 like "Recent folders…" and "Remote
// open"; "git" → 7 git rows plus 16 others). Abbreviations with no
// contiguous hit ("rcf" → Recent files…) still get the scattered list.
// LSP completion shares Score but deliberately keeps its scattered rows:
// servers return camel-hump matches on purpose ("nt" → NewTab), and a
// stray contiguous "nt" in Count would hide the one the user meant.
func (m *paletteModal) refresh() {
	query := m.field.String()
	m.matches = m.matches[:0]
	for _, it := range m.items {
		if it.spacer {
			// Kept in place on an empty query (the sections are the
			// point), dropped once filtering ranks by score.
			if query == "" {
				m.matches = append(m.matches, paletteMatch{item: it})
			}
			continue
		}
		score, hits := finder.Score(query, it.label)
		if score == 0 {
			continue
		}
		m.matches = append(m.matches, paletteMatch{item: it, score: score, hits: hits})
	}
	if query != "" {
		m.matches = dropScatteredMatches(m.matches, query)
		sort.SliceStable(m.matches, func(i, j int) bool {
			return m.matches[i].score > m.matches[j].score
		})
	}
	if m.selected >= len(m.matches) {
		m.selected = len(m.matches) - 1
	}
	if m.selected < 0 {
		m.selected = 0
	}
	m.selected = m.choosable(m.selected, 1)
}

// dropScatteredMatches is finder.dropScattered over palette rows: only
// the contiguous hits when there is at least one, else matches
// unchanged. Filters in place — matches is the modal's own scratch
// slice, rebuilt by every refresh. Only called with a non-empty query,
// so no spacer rows are present to keep or drop.
func dropScatteredMatches(matches []paletteMatch, query string) []paletteMatch {
	hasContiguous := false
	for _, mt := range matches {
		if finder.IsContiguous(query, mt.score) {
			hasContiguous = true
			break
		}
	}
	if !hasContiguous {
		return matches
	}
	kept := matches[:0]
	for _, mt := range matches {
		if finder.IsContiguous(query, mt.score) {
			kept = append(kept, mt)
		}
	}
	return kept
}

// choosable returns the nearest non-spacer match at or after i walking
// in dir (+1 / -1), or i itself when there is none that way — so the
// highlight can never rest on a divider, and pressing past the last real
// row leaves it where it was.
func (m *paletteModal) choosable(i, dir int) int {
	for j := i; j >= 0 && j < len(m.matches); j += dir {
		if !m.matches[j].item.spacer {
			return j
		}
	}
	return i
}

// countItems is how many real (non-spacer) entries a list holds — what
// the shown/total tail reports, since a divider is not a result.
func countItems(items []paletteItem) int {
	n := 0
	for _, it := range items {
		if !it.spacer {
			n++
		}
	}
	return n
}

// countMatches is countItems over the current matches.
func (m *paletteModal) countMatches() int {
	n := 0
	for _, mt := range m.matches {
		if !mt.item.spacer {
			n++
		}
	}
	return n
}

// handleKey routes keyboard input while the palette is open: arrows
// navigate, Enter runs, Esc dismisses, everything else edits the
// query via the shared textField.
func (m *paletteModal) handleKey(a *App, ev *tcell.EventKey) {
	switch ev.Key() {
	case tcell.KeyEsc:
		a.closeModal()
		if m.cancel != nil {
			m.cancel(a)
		}
	case tcell.KeyEnter:
		m.runSelected(a)
	case tcell.KeyUp:
		// choosable answers its own start when nothing is choosable that
		// way, which for a list that LEADS with a heading is the heading
		// itself — so only move onto a real row.
		if m.selected > 0 {
			if j := m.choosable(m.selected-1, -1); !m.matches[j].item.spacer {
				m.selected = j
			}
		}
	case tcell.KeyDown:
		if m.selected < len(m.matches)-1 {
			m.selected = m.choosable(m.selected+1, 1)
		}
	case tcell.KeyTab:
		// The pane switcher's keyboard twin. A single-list picker has no
		// use for Tab, and the text field ignores it, so it stays inert.
		m.switchPane(m.pane + 1)
	case tcell.KeyBacktab:
		m.switchPane(m.pane - 1)
	case tcell.KeyRune:
		if ev.Modifiers()&tcell.ModAlt != 0 && m.runHeadingCopy(a, ev.Rune()) {
			return
		}
		if _, edited := m.field.handleKey(ev); edited {
			m.selected = 0
			m.refresh()
		}
	default:
		if _, edited := m.field.handleKey(ev); edited {
			m.selected = 0
			m.refresh()
		}
	}
}

// runHeadingCopy runs the copy of the heading bound to Alt+r, reporting
// whether one was. It walks m.items, not the matches: a query hides the
// headings but the sections are still there, and "copy the MRU list"
// means the whole list whatever is typed.
func (m *paletteModal) runHeadingCopy(a *App, r rune) bool {
	for _, it := range m.items {
		if it.copy != nil && it.copyKey == r {
			it.copy(a)
			return true
		}
	}
	return false
}

// handleMouse mirrors the finder's mouse contract: hover highlights
// the row under the cursor, click runs it, click outside dismisses.
func (m *paletteModal) handleMouse(a *App, x, y int, btn tcell.ButtonMask) {
	// A press on a pane tab switches lists. Checked first: the tabs sit
	// on the title row, which the row hit-test below never claims, and a
	// press there must not fall through to "outside the rows".
	if btn&tcell.Button1 != 0 {
		_, tabs, _ := m.paneTabs(a)
		for i, r := range tabs {
			if r.contains(x, y) {
				m.switchPane(i)
				return
			}
		}
	}
	mx, my, mw, mh := m.rect(a)
	rowsStart := my + 4
	row := y - rowsStart
	// Only rows actually drawn are targets: past the visible window the
	// frame's bottom border sits where a match index would otherwise
	// still resolve.
	if row >= m.visibleRows(mh) {
		row = -1
	}
	if row >= 0 {
		row += m.top
	}
	// A press on a heading's ⧉ copies its section — checked before the
	// spacer veto below, which would otherwise swallow the press as a
	// click on a divider.
	if btn&tcell.Button1 != 0 && row >= 0 && row < len(m.matches) {
		if it := m.matches[row].item; it.copy != nil {
			if r, ok := headingCopyRect(mx, y, mw, it); ok && r.contains(x, y) {
				it.copy(a)
				return
			}
		}
	}
	if row >= len(m.matches) || (row >= 0 && m.matches[row].item.spacer) {
		row = -1
	}
	if row >= 0 && x >= mx && x < mx+mw {
		m.selected = row
	}
	if btn&tcell.Button1 == 0 {
		return
	}
	if x < mx || x >= mx+mw || y < my || y >= my+mh {
		a.closeModal()
		if m.cancel != nil {
			m.cancel(a)
		}
		return
	}
	if row >= 0 && row < len(m.matches) {
		m.selected = row
		m.runSelected(a)
	}
}

// runSelected fires the highlighted action. The modal closes first so
// actions that open their own modal (rename's prompt, delete's
// confirm) land in an empty slot rather than fighting the palette for
// it. Silent no-op on an empty match list (Enter mashed on a
// no-match query).
func (m *paletteModal) runSelected(a *App) {
	if m.selected < 0 || m.selected >= len(m.matches) || m.matches[m.selected].item.spacer {
		return
	}
	run := m.matches[m.selected].item.run
	a.closeModal()
	run(a)
}

// rect returns the palette's on-screen rectangle — same upper-third
// anchor as the finder so switching between the two fuzzy modals
// doesn't make the eye hunt.
func (m *paletteModal) rect(a *App) (x, y, w, h int) {
	w = paletteMaxWidth
	if w > a.width-4 {
		w = a.width - 4
	}
	if w < 30 {
		w = 30
	}
	// Layout: border + title + divider + input + N rows + border.
	h = m.wantRows() + 6
	if h > a.height-2 {
		h = a.height - 2
	}
	x = (a.width - w) / 2
	y = (a.height - h) / 3
	if x < 0 {
		x = 0
	}
	if y < 0 {
		y = 0
	}
	return
}

// wantRows is the row count this palette asks for before the window
// clamps it.
func (m *paletteModal) wantRows() int {
	if m.rows > 0 {
		return m.rows
	}
	return paletteResultsVisible
}

// visibleRows is how many match rows fit in a modal mh tall — the one
// number draw, scrolling and hit-testing all share.
func (m *paletteModal) visibleRows(mh int) int {
	n := min(mh-5, m.wantRows())
	return max(n, 0)
}

// scrollToSelection moves the window just enough to show the highlight.
func (m *paletteModal) scrollToSelection(visible int) {
	if visible <= 0 {
		m.top = 0
		return
	}
	if m.selected < m.top {
		m.top = m.selected
	}
	if m.selected >= m.top+visible {
		m.top = m.selected - visible + 1
	}
	// Pull in a heading sitting just above the window when the selection
	// leaves room for it: arrowing back up to a section's first row should
	// show the section's name, not stop one row short of it (the top row
	// of a list that leads with a heading is never selectable, so without
	// this ↑ could never scroll it back into view).
	for m.top > 0 && m.matches[m.top-1].item.spacer && m.selected-(m.top-1) < visible {
		m.top--
	}
	if maxTop := max(len(m.matches)-visible, 0); m.top > maxTop {
		m.top = maxTop
	}
	if m.top < 0 {
		m.top = 0
	}
}

// draw paints the modal: standard chrome, the query field with a
// shown/total count tail, then the ranked action rows with matched
// runes highlighted.
//
// Layout (relY):
//
//	0     top border
//	1     title — m.title + "    esc"
//	2     divider
//	3     input          [ query…              12/34 ]
//	4..N  action rows
//	N+1   bottom border
func (m *paletteModal) draw(a *App) {
	mx, my, mw, mh := m.rect(a)
	c := a.chrome()
	hitStyle := tcell.StyleDefault.Background(c.bg).Foreground(a.theme.FindCurrent).Bold(true)
	labels, tabs, showTitle := m.paneTabs(a)
	title := m.title
	if !showTitle {
		title = ""
	}
	c.drawFrame(a.screen, mx, my, mw, mh, title)
	m.drawPaneTabs(a, labels, tabs)

	// Input row — the right side leaves room for the count tail.
	inputStyle := tcell.StyleDefault.Background(a.theme.BG).Foreground(a.theme.Text)
	m.field.draw(a.screen, my+3, mx+3, mx+mw-10, inputStyle, true)

	tail := countLabel(m.countMatches(), countItems(m.items)) + " "
	drawAt(a.screen, mx+mw-1-runeLen(tail), my+3, tail, c.muted)

	// Action rows — visible window only, capped like the finder; when
	// more actions match than fit, the user refines the query.
	rowsStart := my + 4
	rowsCap := m.visibleRows(mh)
	m.scrollToSelection(rowsCap)
	visible := m.matches[m.top:]
	if len(visible) > rowsCap {
		visible = visible[:rowsCap]
	}
	for i := 0; i < rowsCap; i++ {
		ry := rowsStart + i
		if i >= len(visible) {
			// Clear unused rows so a previous query's tail doesn't
			// linger when the match list shrinks.
			for cx := mx + 1; cx < mx+mw-1; cx++ {
				a.screen.SetContent(cx, ry, ' ', nil, c.bgSt)
			}
			continue
		}
		if it := visible[i].item; it.spacer {
			if it.heading != "" {
				m.drawHeading(a, mx, ry, mw, it)
			} else {
				m.drawSpacer(a, mx, ry, mw)
			}
			continue
		}
		m.drawRow(a, mx, ry, mw, visible[i], m.top+i == m.selected, hitStyle, c.bg)
	}
}

// drawRow paints one action line with matched runes highlighted and
// the row background flipped when selected — the same block-highlight
// the finder uses so selection reads instantly.
func (m *paletteModal) drawRow(a *App, mx, ry, mw int, match paletteMatch, selected bool, hitStyle tcell.Style, modalBG tcell.Color) {
	rowBG := modalBG
	if selected {
		rowBG = a.theme.BG
	}
	rowStyle := tcell.StyleDefault.Background(rowBG).Foreground(a.theme.Text)
	hitOnRow := hitStyle.Background(rowBG)

	for cx := mx + 1; cx < mx+mw-1; cx++ {
		a.screen.SetContent(cx, ry, ' ', nil, rowStyle)
	}

	matchSet := map[int]bool{}
	for _, hit := range match.hits {
		matchSet[hit] = true
	}
	startCol := mx + 2
	maxCols := mw - 4
	if sw := match.item.swatch; sw != tcell.ColorDefault {
		// Painted as BACKGROUND on spaces rather than as a glyph such
		// as █: it shows in any font and fills the cell exactly.
		swSt := tcell.StyleDefault.Background(sw)
		a.screen.SetContent(startCol, ry, ' ', nil, swSt)
		a.screen.SetContent(startCol+1, ry, ' ', nil, swSt)
		startCol += paletteSwatchW
		maxCols -= paletteSwatchW
	}
	for i, ch := range []rune(match.item.label) {
		if i >= maxCols {
			break
		}
		st := rowStyle
		if matchSet[i] {
			st = hitOnRow
		}
		a.screen.SetContent(startCol+i, ry, ch, nil, st)
	}
}

// drawSpacer paints a divider row: a dotted rule inset from the frame, in
// the border's own color. Dotted and inset so it reads as a pause between
// two lists rather than as the modal's structure (the solid divider under
// the title), and in the border color because that is already the one
// tone every theme tunes to be visible against the modal background
// without competing with the rows.
func (m *paletteModal) drawSpacer(a *App, mx, ry, mw int) {
	c := a.chrome()
	for cx := mx + 1; cx < mx+mw-1; cx++ {
		a.screen.SetContent(cx, ry, ' ', nil, c.bgSt)
	}
	for cx := mx + 3; cx < mx+mw-3; cx++ {
		a.screen.SetContent(cx, ry, '┄', nil, c.border)
	}
}

// headingCopyLabel is the text of a heading's copy button: the glyph, the
// verb (⧉ alone is a house symbol, but a picker is where a first-timer
// meets it) and the Alt key twin, muted like the frame's esc hint.
func headingCopyLabel(it paletteItem) (button, hint string) {
	return paletteCopyGlyph + " copy", "alt+" + string(it.copyKey)
}

// headingCopyRect is the ⧉ button's cell span on a heading drawn at row
// ry — the ONE geometry drawHeading and handleMouse share. Right-aligned
// to the dotted rule's end (two cells in from the frame), the hint
// included so the whole "⧉ copy alt+r" is one target. ok is false for a
// heading with no copy, or a frame too narrow to fit the button beside
// the heading's name.
//
//	│ MRU 5 ┄┄┄┄┄┄┄┄┄┄┄┄┄┄┄┄┄┄┄┄┄┄┄┄┄┄┄┄┄┄┄┄┄┄┄┄┄ ⧉ copy  alt+r  │
//	  ^mx+2                                     ^r.x         ^mx+mw-3
func headingCopyRect(mx, ry, mw int, it paletteItem) (btnRect, bool) {
	if it.copy == nil {
		return btnRect{}, false
	}
	button, hint := headingCopyLabel(it)
	w := runeLen(button) + 2 + runeLen(hint)
	x := mx + mw - 3 - w
	// Leave the name and one cell of rule either side of it.
	if x < mx+2+runeLen(it.heading)+2 {
		return btnRect{}, false
	}
	return btnRect{x: x, y: ry, w: w}, true
}

// drawHeading paints a section heading: the name in the title colour, the
// spacer's dotted rule filling the gap, and the copy button at the right.
// The name takes the accent so it reads as a label for what follows, not
// as one more row; the rule keeps the border tone, exactly as drawSpacer,
// so a heading still reads as a divider. The button's glyph and verb are
// body text (it is a control, and the muted tone sits too close to the
// rule beside it); only the key hint is muted, like the frame's "esc".
func (m *paletteModal) drawHeading(a *App, mx, ry, mw int, it paletteItem) {
	c := a.chrome()
	for cx := mx + 1; cx < mx+mw-1; cx++ {
		a.screen.SetContent(cx, ry, ' ', nil, c.bgSt)
	}
	ruleEnd := mx + mw - 3 // exclusive, as in drawSpacer
	name := clipRunes(it.heading, max(ruleEnd-mx-2, 0))
	drawAt(a.screen, mx+2, ry, name, c.title)
	x := mx + 2 + runeLen(name)
	if r, ok := headingCopyRect(mx, ry, mw, it); ok {
		button, hint := headingCopyLabel(it)
		drawAt(a.screen, r.x, ry, button, c.body)
		drawAt(a.screen, r.x+runeLen(button)+2, ry, hint, c.muted)
		ruleEnd = r.x - 1
	}
	for cx := x + 1; cx < ruleEnd; cx++ {
		a.screen.SetContent(cx, ry, '┄', nil, c.border)
	}
}
