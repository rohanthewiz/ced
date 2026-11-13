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
	"path/filepath"

	"github.com/gdamore/tcell/v2"

	"github.com/rohanthewiz/ced/internal/editor"
)

// editorContextItem is one row of the editor's context menu.
//
// split marks a SEPARATOR row instead: a dotted rule between two runs of
// related rows (N-108), the right-click twin of the ≡ menu's splits. It
// is a row KIND here rather than the ≡ menu's splitAbove flag on the next
// row, on purpose: ctxScroll maps one screen row to one item, and a flag
// would need a second display-row index threaded through pointer, reveal,
// offset and every opener's height. A split row is simply never usable —
// the arrows skip it, the pointer lands on it without highlighting it, a
// click on it does nothing (the popup stays) — and trimCtxSplits drops the
// ones a conditional row left leading, trailing or doubled.
type editorContextItem struct {
	label   string
	action  func(*App)
	enabled func(*App) bool
	split   bool
}

// editorCtxSplit is a separator row for an editorContextModal list.
func editorCtxSplit() editorContextItem { return editorContextItem{split: true} }

// usable reports whether the row can be hovered and run: never a split,
// else its enabled predicate.
func (it editorContextItem) usable(a *App) bool {
	return !it.split && it.enabled != nil && it.enabled(a)
}

// trimCtxSplits drops the separators a menu builder's conditional rows
// left meaningless: any before the first real row, after the last, or
// directly after another split. Builders therefore place a split at every
// group boundary unconditionally and let this tidy up, rather than each
// branch asking "is there a group above me?".
func trimCtxSplits[T any](items []T, isSplit func(T) bool) []T {
	out := make([]T, 0, len(items))
	for _, it := range items {
		if isSplit(it) && (len(out) == 0 || isSplit(out[len(out)-1])) {
			continue
		}
		out = append(out, it)
	}
	for len(out) > 0 && isSplit(out[len(out)-1]) {
		out = out[:len(out)-1]
	}
	return out
}

// drawCtxSplit paints a separator row on screen row cy of a popup at
// (mx, mw): the ≡ menu's dotted rule (app.go, drawMenu's splits) — inset
// from the label column to two cells short of the frame, in Subtle lifted
// to menuSplitContrast, so a pause inside the list never reads as the
// popup's border. Shared by the tree's menu and this chassis.
func (a *App) drawCtxSplit(mx, mw, cy int, bg tcell.Color) {
	st := tcell.StyleDefault.Background(bg).Foreground(menuSplitColor(a.theme.Subtle, bg))
	for cx := mx + 4; cx < mx+mw-3; cx++ {
		a.screen.SetContent(cx, cy, '┄', nil, st)
	}
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
		// The LSP verbs — same vocabulary as ≡ Code, new door. All already
		// aim at the caret the click just placed. The question-asking rows
		// keep ≡ Code's relative order (definition, references, symbol
		// uses, then definition's two siblings) so a position learned in
		// one list holds in the other; the three that answer or change
		// in place (rename, hover, code actions) close the run as before.
		//
		// "Highlight symbol uses", "Go to implementation" and "Go to type
		// definition" (N-081) were ≡ Code rows only, though they aim at
		// the clicked symbol exactly as definition does. Their three rows
		// are mostly paid for by folding the git rows into one "Git…" row
		// at the foot of the menu: in a repository the popup is the length
		// it was on a changed line and one row longer elsewhere.
		// "Find incoming calls…" stays a ≡ row: it is references with the
		// non-calls taken out, a refinement rather than a new question.
		{label: "Completions", action: (*App).menuCompletion, enabled: (*App).hasLSPActions},
		{label: "Go to definition", action: (*App).menuGoToDefinition, enabled: (*App).hasLSPActions},
		{label: "Find references", action: (*App).menuFindReferences, enabled: (*App).hasLSPActions},
		{label: "Highlight symbol uses", action: (*App).menuHighlightSymbol, enabled: (*App).hasLSPActions},
		{label: "Go to implementation", action: (*App).menuGoToImplementation, enabled: (*App).hasLSPActions},
		{label: "Go to type definition", action: (*App).menuGoToTypeDefinition, enabled: (*App).hasLSPActions},
		{label: "Rename symbol…", action: (*App).menuRenameSymbol, enabled: (*App).hasLSPActions},
		{label: "Hover info", action: (*App).menuHoverInfo, enabled: (*App).hasLSPActions},
		{label: "Code actions…", action: (*App).menuCodeActions, enabled: (*App).hasCodeActions},
		// Splits (N-108) divide the menu into what each run acts ON: the
		// symbol (LSP), the text (clipboard, compare, comment), where the
		// file goes (cats splits), who gets the selection (agents, chat,
		// notes), how the file is drawn, places to come back to, and the
		// repo. Placed at every boundary; trimCtxSplits drops the empties.
		editorCtxSplit(),
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
	// Toggle line comment, the ≡ Edit row's twin (Esc /). It aims at the
	// clicked line, or every line of a selection the click landed inside.
	// Appended rather than joining the fixed vocabulary above because it
	// needs a known comment prefix for the file's language: on a .txt or
	// an unknown extension it would be a permanently dimmed row.
	if a.hasCommentableTab() {
		items = append(items, editorContextItem{
			label: "Toggle line comment", action: (*App).menuToggleLineComment, enabled: alwaysTrue,
		})
	}
	// The cats split (catssplit.go), appended conditionally like the search
	// row below rather than listed above with the fixed vocabulary: a row
	// that would be dimmed in every terminal but one does not earn a
	// permanent place in a popup this small — and inside cats, "put a
	// second editor beside this one" is exactly what a right-click on the
	// file is reaching for.
	items = append(items, editorCtxSplit())
	if a.hasCatsSplit() {
		items = append(items,
			editorContextItem{label: "Open in split →", action: (*App).catsSplitRight, enabled: alwaysTrue},
			editorContextItem{label: "Open in split ↓", action: (*App).catsSplitDown, enabled: alwaysTrue},
		)
	}
	// Handing the selection to a sibling agent (catsagents.go). Gated on
	// there being a selection as well as a host, because "send the
	// selection" with nothing selected is not a row, it is a question.
	// One run with the chat / GoNotes rows below: all hand the selection
	// to someone else.
	items = append(items, editorCtxSplit())
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
	// Handing the selection to ced's own chat (summarize.go, Esc a z) and
	// to GoNotes (gonotes.go, Esc a n). Both verbs fall back to the WHOLE
	// FILE when nothing is selected, but here they appear only WITH a
	// selection: a right-click is about a spot in the text, and "summarize
	// this file" is a ≡ AI question, not a pointer one. The selection
	// survives the click only when it landed inside it (see the caret
	// rule above), which is exactly when these rows are about what the
	// user is pointing at. Labels come from the ≡ rows' own labelFor
	// ("Summarize selection (N lines)…"), resolved now — the popup is
	// rebuilt on every open. Neither asks whether the agent / server is
	// up: both verbs discover that by trying and explain the refusal.
	if tab.HasSelection() {
		if a.canSummarize() {
			items = append(items, editorContextItem{
				label: a.summarizeLabel(), action: (*App).menuSummarize, enabled: alwaysTrue,
			})
		}
		if a.canSendToNotes() {
			items = append(items, editorContextItem{
				label: a.sendToNotesLabel(), action: (*App).menuSendToNotes, enabled: alwaysTrue,
			})
		}
	}
	// The markdown viewer (markdown.go). Appended conditionally like the
	// cats rows rather than joining the dimmed fixed vocabulary above:
	// that vocabulary is the things you do to a SPOT IN TEXT, all of
	// which exist on every file, while this one exists on a handful of
	// extensions and would otherwise be a permanently dead row in every
	// source file the user right-clicks. The other half of the pair
	// (Stop Preview) lives in openPreviewContext — inside a preview the
	// code rows have nothing to aim at, so the two never share a popup.
	items = append(items, editorCtxSplit())
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
	// The search row shares their run: both are ways to get back to, or
	// on to, other places from this word.
	items = append(items, editorCtxSplit())
	if tab.Path != "" {
		items = append(items, editorContextItem{
			label: bookmarkContextLabel(tab), action: (*App).menuToggleBookmark, enabled: alwaysTrue,
		})
		if tab.HasBookmark(tab.Cursor.Line) {
			items = append(items, editorContextItem{
				label: "Label bookmark…", action: (*App).menuLabelBookmark, enabled: alwaysTrue,
			})
		}
		// "<absolute path>:<line>" of the clicked line (copyref.go) — a
		// bookmark you hand to someone else, so it shares this run. The
		// click already moved the caret there, so the verb reads the
		// caret. Saved files only, like the bookmark row: an untitled
		// buffer has no path to name.
		items = append(items, editorContextItem{
			label: copyReferenceLabel, action: func(app *App) { app.copyReferenceFor(tab) }, enabled: alwaysTrue,
		})
	}
	if word := a.contextSearchWord(tab); word != "" {
		items = append(items, editorContextItem{
			label:   "Search project for \"" + word + "\"",
			action:  func(app *App) { app.startProjectSearch(word, app.findOptions()) },
			enabled: (*App).hasProjectSearch,
		})
	}
	// The repo rows, folded into ONE "Git…" row that opens them as a
	// picker (editorGitPicker, below). Appended only inside a work tree,
	// for the cats rows' reason: a row that is dead in every non-repo
	// folder doesn't earn a slot in a popup this small. Last, because what
	// it leads to is mostly about the repo rather than the spot in the
	// text every row above aims at.
	//
	// Folded (N-081) to pay for the three LSP rows added at the top: the
	// git rows were up to four (Show change, Blame, Git status, New
	// branch), and the symbol verbs are what a right-click on code is
	// reaching for more often. A picker rather than a nested popup: it is
	// the sanctioned choose-one-from-a-list surface (openPicker), it needs
	// no new chassis, and on a changed line its first — highlighted — row
	// is Show change, so the one git question that IS about the clicked
	// line costs a click (or Enter) more than before, nothing else.
	items = append(items, editorCtxSplit())
	if a.hasGitRepo() {
		items = append(items, editorContextItem{
			label: "Git…", action: func(app *App) { app.editorGitPicker(tab) }, enabled: alwaysTrue,
		})
	}
	// A click inside a live merge conflict leads with the ways to settle
	// it (conflictview.go) — the right-click twin of the lens, and the one
	// door to "both, incoming first" / "neither", which the lens has no
	// room for. Prepended rather than appended: on a conflict, choosing a
	// side is what the right-click is for.
	if rows := a.conflictContextItems(); len(rows) > 0 {
		items = append(append(rows, editorCtxSplit()), items...)
	}
	return trimCtxSplits(items, func(it editorContextItem) bool { return it.split })
}

// editorGitPickerItems are the rows behind the right-click "Git…" row
// (N-081 folded the popup's git rows here): the clicked line's change,
// the file's blame, then the two about the repo.
//
//   - "Show change…" (hunkview.go) — only when the clicked line is part
//     of a change: the question the change bar answers, asked of the line
//     rather than of a one-cell gutter target. Leading, so on a changed
//     line the picker opens with it highlighted.
//   - "Blame this file" / "Hide blame" (gitblame.go) — the blame column,
//     the Esc A / ≡ Git "Show blame" toggle, labelled by what the pick
//     does. It replaced "Blame this line…": one line's commit is a click
//     on the column once it is up, and the column answers every other
//     line too, which a right-click on code is as likely to be after. A
//     very long file comes back screen-first (blameWindowAbove), so
//     asking for the whole file costs no wait. The layer is global like
//     its other doors: it stays on across tab switches. A saved file
//     only; an untitled buffer has no history to blame. ≡ Git keeps
//     "Blame this line…" as the keyboard twin of clicking the column.
//   - "Git status…" (gitstatusreport.go) and "New branch…" (gitcmd.go) —
//     twins of the ≡ Git rows of the same names (New branch is also the
//     Switch branch picker's "+ New branch…"): the read-only report
//     first, the one that changes HEAD last.
//
// Every row calls the ≡ row's own verb, so the report, the prompt and
// git's refusals are identical at every door. Rows are omitted rather
// than dimmed — a picker has no dim state (tabGroupActionsPicker's rule).
// The picker closes before a row runs (runSelected), so the New branch
// prompt opens in a free slot and the status report, which declines an
// occupied one, finds it empty. The caret is still where the right-click
// put it, so Show change aims at the clicked line.
func (a *App) editorGitPickerItems(tab *editor.Tab) []paletteItem {
	var items []paletteItem
	if a.hasHunkAtCaret() {
		items = append(items, paletteItem{label: "Show change…", run: (*App).menuShowHunkAtCaret})
	}
	if tab != nil && tab.Path != "" {
		items = append(items, paletteItem{label: blameFileLabel(a.blameOn), run: (*App).menuToggleBlame})
	}
	return append(items,
		paletteItem{label: "Git status…", run: (*App).menuGitStatus},
		paletteItem{label: "New branch…", run: (*App).menuGitNewBranch},
	)
}

// blameFileLabel is the Git… picker's blame row, read as the action the
// pick performs (the toggle convention of blameToggleLabel). "Hide
// blame" matches the ≡ Git row's wording for the same state, so the two
// doors agree once the column is up.
func blameFileLabel(on bool) string {
	if on {
		return "Hide blame"
	}
	return "Blame this file"
}

// editorGitPicker opens editorGitPickerItems. The title names the clicked
// line ("Git · main.go:42"): the palette is centred, away from the code
// the popup was anchored on, and Show change needs a referent for "the
// line". An untitled buffer has no name to give, so it gets plain "Git".
func (a *App) editorGitPicker(tab *editor.Tab) {
	title := "Git"
	if tab != nil && tab.Path != "" {
		title += " · " + filepath.Base(tab.Path) + ":" + itoa(tab.Cursor.Line+1)
	}
	a.openPicker(title, a.editorGitPickerItems(tab))
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
		if m.items[i].usable(a) {
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
	if !item.usable(a) {
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
		if item.split {
			a.drawCtxSplit(mx, mw, cy, bg)
			continue
		}
		enabled := item.usable(a)
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
