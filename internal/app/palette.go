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
// items. Two sources exist today: the action menu inventory (built-ins
// + custom actions, via menuLayout) and the finder's file index, so
// one Esc-a surface fuzzy-searches actions and project files together.
// Later sources (LSP symbols, …) merge into the same ranked list
// without touching the modal.
//
// The modal itself doubles as a generic picker: openPicker shows a
// caller-supplied item list under its own title (the branch switcher
// uses this), reusing the palette's whole interaction grammar for free.

package app

import (
	"path/filepath"
	"sort"
	"time"

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
}

// paletteSpacer is the one spelling of a divider row.
func paletteSpacer() paletteItem { return paletteItem{spacer: true} }

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
// Actions come first: on an empty query the palette reads top-down as
// the action menu, and refresh's stable sort breaks score ties in
// source order, so an action never hides below a same-scored file.
func paletteSources() []paletteSource {
	return []paletteSource{paletteActionItems, paletteFileItems}
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
			if label == paletteMenuLabel {
				continue
			}
			out = append(out, paletteItem{label: label, run: it.action})
		}
	}
	return out
}

// paletteFileItems adapts the finder's file index into palette items,
// so the palette fuzzy-searches project files alongside actions —
// same index, same scorer, same open-on-Enter behavior as the
// dedicated finder modal. Returns nil while the index is idle or
// building (mirroring Finder.Search's contract); the rebuilt event
// re-collects once paths exist.
func paletteFileItems(a *App) []paletteItem {
	if a.finder == nil {
		return nil
	}
	paths := a.finder.Paths()
	out := make([]paletteItem, 0, len(paths))
	for _, rel := range paths {
		rel := rel // capture per-iteration for the closure
		out = append(out, paletteItem{
			label: rel,
			run: func(app *App) {
				app.openFile(filepath.Join(app.rootDir, rel))
			},
		})
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
	// sourced marks a modal whose items came from paletteSources, so
	// the finder-rebuilt event knows to re-collect them (file items may
	// have just become available). Picker item lists are caller-owned
	// and must never be clobbered by a background index build.
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
}

// openPalette gathers items from every source and shows the palette.
// A stale file index kicks off a background rebuild (same contract as
// openFinder) — the rebuilt event re-collects sources so file rows
// stream in without the user reopening the modal.
func (a *App) openPalette() {
	m := &paletteModal{title: paletteMenuLabel, sourced: true}
	a.openModal(m)
	m.collectItems(a)
	if a.finder != nil && a.finder.State() != finder.StateReady {
		scr := a.screen
		a.finder.Rebuild(func() {
			_ = scr.PostEvent(&finderRebuiltEvent{when: time.Now()})
		})
	}
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

// collectItems (re)gathers the inventory from every registered source.
// Called at open and again when the finder index finishes a rebuild,
// so it must be idempotent — hence the slice reset.
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
		if m.selected > 0 {
			m.selected = m.choosable(m.selected-1, -1)
		}
	case tcell.KeyDown:
		if m.selected < len(m.matches)-1 {
			m.selected = m.choosable(m.selected+1, 1)
		}
	default:
		if _, edited := m.field.handleKey(ev); edited {
			m.selected = 0
			m.refresh()
		}
	}
}

// handleMouse mirrors the finder's mouse contract: hover highlights
// the row under the cursor, click runs it, click outside dismisses.
func (m *paletteModal) handleMouse(a *App, x, y int, btn tcell.ButtonMask) {
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
	c.drawFrame(a.screen, mx, my, mw, mh, m.title)

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
		if visible[i].item.spacer {
			m.drawSpacer(a, mx, ry, mw)
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
