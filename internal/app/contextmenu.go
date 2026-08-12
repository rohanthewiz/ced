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
// and the hovered row index.
type editorContextModal struct {
	x, y  int
	w     int
	items []editorContextItem
	hover int
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
	w := contextMenuWidth
	for _, it := range items {
		if lw := runeLen(it.label) + 6; lw > w { // border+chevron+padding
			w = lw
		}
	}
	if w > a.width {
		w = a.width
	}
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
		{label: "Compare selection with paste…", action: (*App).compareSelectionWithPaste, enabled: (*App).hasSelection},
	}
	if word := a.contextSearchWord(tab); word != "" {
		items = append(items, editorContextItem{
			label:   "Search project for \"" + word + "\"",
			action:  func(app *App) { app.startProjectSearch(word) },
			enabled: (*App).hasProjectSearch,
		})
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
func (a *App) placeContextSized(x, y, count, w int) (int, int) {
	h := count + 2
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

// rect returns the popup's on-screen rectangle.
func (m *editorContextModal) rect(a *App) (x, y, w, h int) {
	return m.x, m.y, m.w, len(m.items) + 2
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
// at the ends (no wrap — the popup is small enough to see whole).
func (m *editorContextModal) moveHover(a *App, dir int) {
	for i := m.hover + dir; i >= 0 && i < len(m.items); i += dir {
		if m.items[i].enabled(a) {
			m.hover = i
			return
		}
	}
}

// handleMouse: hover follows the pointer, click activates, click outside
// dismisses — the contextModal contract.
func (m *editorContextModal) handleMouse(a *App, x, y int, btn tcell.ButtonMask) {
	mx, my, mw, mh := m.rect(a)
	if x >= mx && x < mx+mw && y > my && y < my+mh-1 {
		m.hover = y - my - 1
	}
	if btn&tcell.Button1 == 0 {
		return
	}
	if x < mx || x >= mx+mw || y < my || y >= my+mh {
		a.closeModal()
		return
	}
	if y > my && y < my+mh-1 {
		m.hover = y - my - 1
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

	for i, item := range m.items {
		cy := my + 1 + i
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

	a.screen.HideCursor()
}
