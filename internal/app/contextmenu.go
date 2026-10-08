// =============================================================================
// File: internal/app/contextmenu.go
// Author: Rohan Allison
// =============================================================================

// The editor's right-click context menu.
//
// The file tree has had a right-click menu since the beginning; the
// editor body — where the user actually lives — answered right-click by
// opening the full ≡ menu, a context switch masquerading as a shortcut.
// This file gives the editor its own menu on the contextModal chassis
// (modals.go): anchored at the click point, flipping to stay on screen,
// hover + click or arrows + Enter.
//
// Differences from the tree's menu, and why it isn't the same type:
//
//   - Actions are plain func(*App) — they act on the caret/selection,
//     not on a tree node, so reusing contextItem would mean threading a
//     meaningless *filetree.Node through every row.
//   - Rows carry enabled predicates and DIM instead of disappearing
//     (the ≡-menu convention): the menu is a fixed vocabulary the user
//     learns positions in, and rows that come and go defeat that. The
//     tree menu omits rows instead because its list changes by node
//     KIND (file vs folder vs root) — different rows for different
//     nouns — whereas here the noun is always "this spot in the text".
//   - Width is computed from the labels: "Search project for …" carries
//     the quoted word, so a fixed width would truncate the one row
//     whose label is its argument.
//
// The click placed the caret before the menu opened (unless it landed
// inside the current selection — right-clicking your own selection to
// act on it must not destroy it), so every LSP verb already aims at the
// right spot and needs no new position plumbing.
//
// Both anchored menus — this chassis and the tree's contextModal — SCROLL
// when they are taller than the window (ctxScroll, below). Rows are
// appended per state — a conflict's resolvers, the cats rows, bookmark
// and search rows — so this menu can outgrow even the smallest window
// ced draws in (minHeight), and the tab menu is close behind at 18 rows.
// Placed by its full height, an overlong popup clamped to row 0 and
// dropped its bottom rows off the screen, unreachable by mouse or
// keyboard. Clipped, the popup keeps the ≡ menu's contract:
// ▲/▼ on the border says there is more that way, the wheel scrolls, the
// arrows keep the hovered row in view, and — the mouse-first door, for a
// terminal that drops wheel reports — a click on a marked border turns a
// page.

package app

import (
	"github.com/gdamore/tcell/v2"

	"github.com/rohanthewiz/ced/internal/editor"
)

// editorContextItem is one row of the editor's context menu.
type editorContextItem struct {
	label   string
	action  func(*App)
	enabled func(*App) bool
}

// editorContextModal is the popup itself: anchor, computed width, rows,
// the hovered row index, and how far the rows are scrolled when the
// popup is clipped to the window.
type editorContextModal struct {
	x, y   int
	w      int
	items  []editorContextItem
	hover  int
	scroll ctxScroll
}

// editorContextMenuWidth sizes an editorContextModal to its widest label
// via the shared rule (contextMenuWidthForLabel, modals.go). Every opener
// of this chassis — editor, preview, tab, tab group, problems, git log,
// conflicts — goes through it, including the one-row preview menu whose
// label fits the floor today: a fixed width is only right until someone
// renames the row.
func (a *App) editorContextMenuWidth(items []editorContextItem) int {
	widest := 0
	for _, it := range items {
		widest = max(widest, runeLen(it.label))
	}
	return a.contextMenuWidthForLabel(widest)
}

// tryEditorContextClick opens the context menu when (x, y) lands in the
// editor body over a text tab. Returns true when it consumed the event,
// so the caller knows not to fall back to the ≡ menu. Points inside any
// panel that overlays the editor band (git, compare, terminal, chat,
// find bar) are declined — those surfaces own their own gestures.
func (a *App) tryEditorContextClick(x, y int) bool {
	tab := a.activeTabPtr()
	if tab == nil || tab.IsImage() || tab.Buffer == nil {
		return false
	}
	if (a.gitPanel.open && a.gitPanelContains(x, y)) ||
		(a.gitLog.open && a.gitLogContains(x, y)) ||
		a.problemsContains(x, y) ||
		a.conflictPanelContains(x, y) ||
		(a.compare.open && a.comparePanelContains(x, y)) ||
		(a.term.open && a.termPanelContains(x, y)) ||
		(a.chat.open && a.chatPanelContains(x, y)) ||
		a.findBarContains(x, y) {
		return false
	}
	ex, ey, ew, eh := a.editorRect()
	if x < ex || x >= ex+ew || y < ey || y >= ey+eh {
		return false
	}
	// A preview has no caret to place and no selection to act on, so it
	// gets its own one-row popup instead of the code vocabulary aimed at
	// a cursor the reader cannot see. It is the one surface where the
	// menu MUST work: a preview swallows the keyboard except for
	// navigation, so a user who arrived here from the tree's Preview row
	// and never learned Esc-v would otherwise have only the ≡ menu.
	if tab.IsMarkdownView() {
		a.openPreviewContext(x, y)
		return true
	}

	pos, ok := tab.HitTest(x-ex, y-ey, ew, eh)
	if !ok {
		return false
	}

	// Set the caret at the click point — the GUI-editor contract that
	// makes "Rename symbol" rename the symbol you clicked, not the one
	// you happened to be parked on. The exception: a click inside the
	// current selection keeps it, because the selection is the argument
	// of Copy/Cut/Compare and right-clicking it is how you act on it.
	if !(tab.HasSelection() && posInSelection(tab, pos)) {
		tab.MoveCursorTo(pos, false)
	}

	items := a.editorContextItems(tab)
	w := a.editorContextMenuWidth(items)
	cx, cy := a.placeContextSized(x, y, len(items), w)
	a.openModal(&editorContextModal{x: cx, y: cy, w: w, items: items})
	return true
}

// editorContextItems builds the row list for the current caret spot.
// Built fresh on every open: the search row's label embeds the word it
// would search for, and the predicates read live state anyway.
func (a *App) editorContextItems(tab *editor.Tab) []editorContextItem {
	items := []editorContextItem{
		// The LSP verbs, in the ≡ Code group's order — same vocabulary,
		// new door. All already aim at the caret the click just placed.
		{label: "Completions", action: (*App).menuCompletion, enabled: (*App).hasLSPActions},
		{label: "Go to definition", action: (*App).menuGoToDefinition, enabled: (*App).hasLSPActions},
		{label: "Find references", action: (*App).menuFindReferences, enabled: (*App).hasLSPActions},
		{label: "Rename symbol…", action: (*App).menuRenameSymbol, enabled: (*App).hasLSPActions},
		{label: "Hover info", action: (*App).menuHoverInfo, enabled: (*App).hasLSPActions},
		{label: "Code actions…", action: (*App).menuCodeActions, enabled: (*App).hasCodeActions},
		{label: "Copy", action: (*App).copySelection, enabled: (*App).hasSelection},
		{label: "Cut", action: (*App).cutSelection, enabled: (*App).hasSelection},
		// Paste stays enabled with an empty internal clipboard —
		// pasteClipboard's hint flash explains the OSC 52 read gap,
		// which a dimmed row never could.
		{label: "Paste", action: (*App).pasteClipboard, enabled: alwaysTrue},
		// Part of the fixed vocabulary: every text file can be selected
		// whole, and the popup only opens over a source-view text tab (a
		// preview gets openPreviewContext), so the row never needs dimming.
		{label: "Select all", action: (*App).selectAllInFile, enabled: alwaysTrue},
		{label: "Compare selection with paste…", action: (*App).compareSelectionWithPaste, enabled: (*App).hasSelection},
	}
	// The cats split (catssplit.go), appended conditionally like the search
	// row below rather than listed above with the fixed vocabulary: a row
	// that would be dimmed in every terminal but one does not earn a
	// permanent place in a popup this small — and inside cats, "put a
	// second editor beside this one" is exactly what a right-click on the
	// file is reaching for.
	if a.hasCatsSplit() {
		items = append(items,
			editorContextItem{label: "Open in split →", action: (*App).catsSplitRight, enabled: alwaysTrue},
			editorContextItem{label: "Open in split ↓", action: (*App).catsSplitDown, enabled: alwaysTrue},
		)
	}
	// Handing the selection to a sibling agent (catsagents.go). Gated on
	// there being a selection as well as a host, because "send the
	// selection" with nothing selected is not a row, it is a question.
	if a.hasCatsSelectionSend() {
		if a.hasCatsAgents() {
			items = append(items, editorContextItem{
				label: "Send selection to agent…", action: (*App).menuCatsSendSelection, enabled: alwaysTrue,
			})
		}
		items = append(items, editorContextItem{
			label: "Ask cats chat about selection", action: (*App).menuCatsAskChat, enabled: alwaysTrue,
		})
	}
	// The markdown viewer (markdown.go). Appended conditionally like the
	// cats rows rather than joining the dimmed fixed vocabulary above:
	// that vocabulary is the things you do to a SPOT IN TEXT, all of
	// which exist on every file, while this one exists on a handful of
	// extensions and would otherwise be a permanently dead row in every
	// source file the user right-clicks. The other half of the pair
	// (Stop Preview) lives in openPreviewContext — inside a preview the
	// code rows have nothing to aim at, so the two never share a popup.
	if tab.MarkdownCapable() {
		items = append(items, editorContextItem{
			label: "Preview", action: (*App).toggleMarkdownView, enabled: alwaysTrue,
		})
	}
	// Soft wrap (softwrap.go), beside Preview for the tree menu's reason:
	// both are about how the file is drawn. Unconditional, unlike Preview —
	// every text file can have a long line — and labelled by the state the
	// click produces, so a wrapped file offers the way back out.
	items = append(items, editorContextItem{
		label: softWrapContextLabel(tab.IsSoftWrap()), action: (*App).toggleSoftWrap, enabled: alwaysTrue,
	})
	// Bookmarks (bookmarks.go): the click placed the caret on the clicked
	// line, so the row aims there; labelled by the state it produces.
	// Only on a file with a path — an untitled buffer refuses one.
	// "Label bookmark…" follows it only on a line that already has one:
	// naming an unmarked line is the ≡ row's job, and two bookmark rows
	// on every right-click would crowd a menu that is about the text.
	if tab.Path != "" {
		items = append(items, editorContextItem{
			label: bookmarkContextLabel(tab), action: (*App).menuToggleBookmark, enabled: alwaysTrue,
		})
		if tab.HasBookmark(tab.Cursor.Line) {
			items = append(items, editorContextItem{
				label: "Label bookmark…", action: (*App).menuLabelBookmark, enabled: alwaysTrue,
			})
		}
	}
	if word := a.contextSearchWord(tab); word != "" {
		items = append(items, editorContextItem{
			label:   "Search project for \"" + word + "\"",
			action:  func(app *App) { app.startProjectSearch(word, app.findOptions()) },
			enabled: (*App).hasProjectSearch,
		})
	}
	// A click inside a live merge conflict leads with the ways to settle
	// it (conflictview.go) — the right-click twin of the lens, and the one
	// door to "both, incoming first" / "neither", which the lens has no
	// room for. Prepended rather than appended: on a conflict, choosing a
	// side is what the right-click is for.
	if rows := a.conflictContextItems(); len(rows) > 0 {
		items = append(rows, items...)
	}
	return items
}

// contextSearchWord is what the search row would look for: a single-line
// selection verbatim (the findAllSelectionQuery contract), else the word
// under the caret. Long candidates are declined rather than elided — a
// row can't say what it will do if its label ends in "…".
func (a *App) contextSearchWord(tab *editor.Tab) string {
	q := a.findAllSelectionQuery()
	if q == "" {
		q = wordAt(tab, tab.Cursor)
	}
	if runeLen(q) > 24 {
		return ""
	}
	return q
}

// wordAt returns the word under buffer position p, or "" when p sits in
// whitespace or punctuation. Same boundary scan as double-click select
// (selectWordAt) without the side effect of selecting.
func wordAt(tab *editor.Tab, p editor.Position) string {
	line := tab.Buffer.LineRunes(p.Line)
	if len(line) == 0 {
		return ""
	}
	start := p.Col
	if start > len(line) {
		start = len(line)
	}
	for start > 0 && isWordChar(line[start-1]) {
		start--
	}
	end := p.Col
	for end < len(line) && isWordChar(line[end]) {
		end++
	}
	return string(line[start:end])
}

// posInSelection reports whether p falls inside the tab's selection,
// treating the range as half-open [start, end) in document order.
func posInSelection(tab *editor.Tab, p editor.Position) bool {
	start, end := tab.Anchor, tab.Cursor
	if end.Line < start.Line || (end.Line == start.Line && end.Col < start.Col) {
		start, end = end, start
	}
	after := p.Line > start.Line || (p.Line == start.Line && p.Col >= start.Col)
	before := p.Line < end.Line || (p.Line == end.Line && p.Col < end.Col)
	return after && before
}

// placeContextSized is placeContext for a caller-chosen width: anchor on
// the click point, flip left/up when the popup would run off screen.
//
// The height it places is the CLIPPED one (at most the window), the same
// figure ctxMenuRows derives from the origin this returns: a popup taller
// than the window lands at row 0 and scrolls, rather than being placed by
// its full height and hanging off the bottom edge.
func (a *App) placeContextSized(x, y, count, w int) (int, int) {
	h := min(count, max(a.height-2, 1)) + 2
	cx, cy := x, y
	if cx+w > a.width {
		cx = x - w + 1
	}
	if cy+h > a.height {
		cy = y - h + 1
	}
	if cx < 0 {
		cx = 0
	}
	if cy < 0 {
		cy = 0
	}
	return cx, cy
}

// -----------------------------------------------------------------------------
// Scrolling, shared by both anchored menus
// -----------------------------------------------------------------------------

// ctxScroll is the scroll state of an anchored context menu: the index of
// the first item drawn. It is stored raw and always READ through offset,
// which re-clamps it against the current geometry — menuScrollOffset's
// rule, so a window resized under an open popup can never leave draw and
// hit-testing disagreeing about which row is which.
//
//	┌─── ▲ ───┐  top > 0: items hidden above
//	│ ▸ …     │  ┐
//	│ ▸ …     │  ├ rows = ctxMenuRows(y, count), showing items[top : top+rows]
//	│ ▸ …     │  ┘
//	└─── ▼ ───┘  top+rows < count: items hidden below
type ctxScroll struct {
	top int
}

// ctxMenuRows is how many item rows an anchored popup with count items
// shows when its top border sits on screen row y: all of them, unless the
// window ends first. Floored at one so a degenerate window still gets a
// row to hover rather than a zero-height box with nothing to click.
func (a *App) ctxMenuRows(y, count int) int {
	return max(min(count, a.height-y-2), min(count, 1))
}

// offset is the effective first visible item, clamped to [0, count-rows].
func (s *ctxScroll) offset(count, rows int) int {
	return max(min(s.top, count-rows), 0)
}

// scrollBy moves the window by delta items, clamped.
func (s *ctxScroll) scrollBy(delta, count, rows int) {
	s.top = s.offset(count, rows) + delta
	s.top = s.offset(count, rows)
}

// reveal scrolls just enough to bring item i on screen. Called only when
// the keyboard moves the hover — never from draw — so a wheel that scrolled
// away from the highlight is not yanked back (the ≡ menu's rule).
func (s *ctxScroll) reveal(i, count, rows int) {
	top := s.offset(count, rows)
	switch {
	case i < top:
		s.top = i
	case i >= top+rows:
		s.top = i - rows + 1
	}
}

// pointer folds one mouse event for a popup at (mx, my, mw, mh) holding
// count items into the scroll state, and reports what the modal should do
// with it. row is the item under the pointer (-1 for a border or the world
// outside), which the caller adopts as its hover; activate means a left
// press landed on that row; dismiss means a left press landed outside.
//
// The one place both menus map a screen row to an item, so the hover a
// modal paints and the item a click runs cannot drift apart under scroll.
//
// Two gestures belong to the scroll alone and are consumed here:
//   - the wheel inside the popup scrolls it (wheelLines per notch, the ≡
//     menu's step), and the pointer's row is re-read AFTER the move so the
//     highlight tracks what is under the pointer now;
//   - a left press on a border that carries ▲/▼ turns a page that way.
//     A border press with nothing to reveal stays inert, as before.
func (s *ctxScroll) pointer(x, y int, btn tcell.ButtonMask, mx, my, mw, mh, count int) (row int, activate, dismiss bool) {
	rows := mh - 2
	inside := x >= mx && x < mx+mw && y >= my && y < my+mh
	if inside && btn&(tcell.WheelUp|tcell.WheelDown) != 0 {
		delta := wheelLines
		if btn&tcell.WheelUp != 0 {
			delta = -wheelLines
		}
		s.scrollBy(delta, count, rows)
	}
	top := s.offset(count, rows)
	row = -1
	if inside && y > my && y < my+mh-1 {
		if i := top + (y - my - 1); i < count {
			row = i
		}
	}
	if btn&tcell.Button1 == 0 {
		return row, false, false
	}
	if !inside {
		return -1, false, true
	}
	page := max(rows-1, 1)
	switch {
	case y == my && top > 0:
		s.scrollBy(-page, count, rows)
	case y == my+mh-1 && top+rows < count:
		s.scrollBy(page, count, rows)
	}
	return row, row >= 0, false
}

// drawCtxScrollMarks puts the ≡ menu's clipped-content markers on a
// popup's borders: " ▲ " on the top border while items are hidden above,
// " ▼ " on the bottom one while items are hidden below. Without them a
// clipped menu reads as the whole menu (the "caps are announced" rule).
func (a *App) drawCtxScrollMarks(mx, my, mw, mh, top, count int, st tcell.Style) {
	if mw < 5 {
		return
	}
	mid := mx + (mw-3)/2
	if top > 0 {
		drawAt(a.screen, mid, my, " ▲ ", st)
	}
	if top+(mh-2) < count {
		drawAt(a.screen, mid, my+mh-1, " ▼ ", st)
	}
}

// rect returns the popup's on-screen rectangle, clipped to the window.
func (m *editorContextModal) rect(a *App) (x, y, w, h int) {
	return m.x, m.y, m.w, a.ctxMenuRows(m.y, len(m.items)) + 2
}

// handleKey mirrors the tree menu's bindings, with one addition: the
// arrows skip disabled rows, since Enter on one would be a dead end.
func (m *editorContextModal) handleKey(a *App, ev *tcell.EventKey) {
	switch ev.Key() {
	case tcell.KeyEsc:
		a.closeModal()
	case tcell.KeyDown:
		m.moveHover(a, 1)
	case tcell.KeyUp:
		m.moveHover(a, -1)
	case tcell.KeyEnter:
		m.activate(a)
	}
}

// moveHover advances the hover to the next enabled row in dir, stopping
// at the ends (no wrap — the ends are where the ▲/▼ markers say "no
// more"), and scrolls a clipped popup to keep the new row in view.
func (m *editorContextModal) moveHover(a *App, dir int) {
	for i := m.hover + dir; i >= 0 && i < len(m.items); i += dir {
		if m.items[i].enabled(a) {
			m.hover = i
			_, _, _, mh := m.rect(a)
			m.scroll.reveal(i, len(m.items), mh-2)
			return
		}
	}
}

// handleMouse: hover follows the pointer, click activates, click outside
// dismisses — the contextModal contract. Row mapping, the wheel and the
// border paging live in ctxScroll.pointer, shared with the tree's menu.
func (m *editorContextModal) handleMouse(a *App, x, y int, btn tcell.ButtonMask) {
	mx, my, mw, mh := m.rect(a)
	row, act, dismiss := m.scroll.pointer(x, y, btn, mx, my, mw, mh, len(m.items))
	if dismiss {
		a.closeModal()
		return
	}
	if row >= 0 {
		m.hover = row
	}
	if act {
		m.activate(a)
	}
}

// activate runs the hovered row if it is enabled. Disabled rows swallow
// the click silently — they are visibly dimmed, and a flash on top of
// that would nag.
func (m *editorContextModal) activate(a *App) {
	if m.hover < 0 || m.hover >= len(m.items) {
		return
	}
	item := m.items[m.hover]
	if !item.enabled(a) {
		return
	}
	a.closeModal()
	if item.action != nil {
		item.action(a)
	}
}

// draw renders the popup: the tree menu's look, plus muted text for
// disabled rows.
func (m *editorContextModal) draw(a *App) {
	mx, my, mw, mh := m.rect(a)

	bg := a.theme.LineHL
	bgStyle := tcell.StyleDefault.Background(bg).Foreground(a.theme.Text)
	borderStyle := tcell.StyleDefault.Background(bg).Foreground(a.theme.Subtle)
	mutedStyle := tcell.StyleDefault.Background(bg).Foreground(a.theme.Muted)
	hoverBg := a.theme.Selection
	hoverStyle := tcell.StyleDefault.Background(hoverBg).Foreground(a.theme.Text).Bold(true)
	hoverChevStyle := tcell.StyleDefault.Background(hoverBg).Foreground(a.theme.AccentSoft).Bold(true)
	chevStyle := tcell.StyleDefault.Background(bg).Foreground(a.theme.AccentSoft)

	fillRect(a.screen, mx, my, mw, mh, bgStyle)
	drawBorder(a.screen, mx, my, mw, mh, borderStyle)

	// Only the window [top, top+rows) is painted; rows scrolled out are
	// announced by the border markers drawn after the loop.
	rows := mh - 2
	top := m.scroll.offset(len(m.items), rows)
	for i := top; i < top+rows && i < len(m.items); i++ {
		item := m.items[i]
		cy := my + 1 + (i - top)
		enabled := item.enabled(a)
		switch {
		case i == m.hover && enabled:
			for cx := mx + 1; cx < mx+mw-1; cx++ {
				a.screen.SetContent(cx, cy, ' ', nil, hoverStyle)
			}
			drawAt(a.screen, mx+2, cy, "▸", hoverChevStyle)
			drawAt(a.screen, mx+4, cy, item.label, hoverStyle)
		case enabled:
			drawAt(a.screen, mx+2, cy, "▸", chevStyle)
			drawAt(a.screen, mx+4, cy, item.label, bgStyle)
		default:
			drawAt(a.screen, mx+4, cy, item.label, mutedStyle)
		}
	}
	a.drawCtxScrollMarks(mx, my, mw, mh, top, len(m.items), chevStyle)

	a.screen.HideCursor()
}

// openPreviewContext is the editor menu's shape while a markdown preview
// owns the pane: one row, because a rendered document is a reading
// surface and the only verb it owes is the way out. Built here rather
// than as a branch inside editorContextItems so no caller can end up
// asking a previewed tab for LSP rows.
func (a *App) openPreviewContext(x, y int) {
	items := []editorContextItem{
		{label: "Stop Preview", action: (*App).toggleMarkdownView, enabled: alwaysTrue},
	}
	w := a.editorContextMenuWidth(items)
	cx, cy := a.placeContextSized(x, y, len(items), w)
	a.openModal(&editorContextModal{x: cx, y: cy, w: w, items: items})
}
