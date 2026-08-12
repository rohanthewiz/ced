// =============================================================================
// File: internal/app/problems.go
// Author: Rohan Allison <rohanthewiz@gmail.com>
// Created: 2026-08-12
// Copyright: 2026 Rohan Allison. All rights reserved.
// =============================================================================

// problems.go is the Problems panel: every diagnostic the language
// server has published, across every file it has published for, as one
// clickable worklist.
//
// Until now diagnostics were only ever visible where they happened — an
// underline in the buffer, a dot in the gutter, a count in the status
// bar. That answers "is this line broken?" and never answers "what is
// broken?", which is the question you ask before a commit and the one a
// status bar showing `✗ 2 ⚠ 5` most provokes. Phase 1 stamped that
// status segment as a click target and deliberately left it inert; this
// file is what claims the click.
//
// Where it lives, and why not with find-all. The plan called for
// find-all's dock machinery, and the interaction model is indeed
// find-all's — a list of positions you walk, a selection that moves the
// editor, filters that narrow the view without re-running anything. But
// find-all's TWO DOCKS both exist to keep a list near the code it points
// at inside ONE file: the top strip is short and full-width, the right
// column is tall and 62 cells wide. A problem row is `path:line │
// message`, which is neither — it is wide (the message is prose, the
// path is long) and it belongs to no particular file. So the panel takes
// the bottom strip, where gitpanel/gitlog/compare/terminal already live,
// and mirrors gitlog.go's shape rather than sharing its code (the same
// choice gitlog itself made against gitpanel, and for the same reason:
// panels evolve apart, and the house patterns are the shared part).
//
// What it is NOT: it never takes the modal slot. Diagnostics are
// ambient — you did not ask a question, so nothing here may own the
// keyboard. The panel is furniture from birth, which is the one real
// difference from find-all, whose list starts life as a peek popup and
// only becomes furniture when pinned.
//
// Layout:
//
//	├──[ ✗ 3 ][ ⚠ 5 ][ ℹ 1 ][ all files ] Problems · 9 ──── ⟳ ─ ✕ ─┤
//	│ ✗ internal/app/foo.go:42 │ undefined: doThing      compiler   │
//	│ ⚠ internal/app/bar.go:8  │ unused variable x       staticcheck│
//
// The chips are the severity filter (lit = shown) plus the scope
// toggle; the rows are the view. One geometry source per control
// (btnRect), stamped by the same function draw and hit-testing both
// call — the house rule that keeps a button's drawn cells and its
// clickable cells from drifting.

package app

import (
	"fmt"
	"path/filepath"
	"sort"
	"strings"

	"github.com/gdamore/tcell/v2"

	"github.com/rohanthewiz/ced/internal/clipboard"
	"github.com/rohanthewiz/ced/internal/lsp"
)

const (
	// Same vertical band as the other bottom-strip panels — they swap in
	// one slot, so inheriting the floor/cap keeps the swap seamless.
	problemsMinHeight = 6
	problemsMaxHeight = 18

	// problemsMinEditorRows / problemsResizeStep mirror the git panels'
	// resize contract: the strip is a viewer, the editor stays primary.
	problemsMinEditorRows = 5
	problemsResizeStep    = 2

	// problemsMinLabelW keeps the `path:line` column readable on a narrow
	// window; below this it says nothing that could identify a file.
	problemsMinLabelW = 14

	// problemsHeaderReserve is the cells the header keeps clear at its
	// right end for the ⟳ and ✕ buttons, so a long chip run can never
	// draw over a control.
	problemsHeaderReserve = 10
)

// Chip kinds. The three severities double as their own kind (the LSP
// severity numbers), so a chip can carry the bucket it filters; the
// scope chip needs a value outside that space.
const problemsChipScope = 0

// problemRow is one diagnostic, flattened for display and for the verbs
// that act on it. The display fields are derived once per refresh —
// the panel redraws on every event, and re-deriving a relative path per
// frame would fork nothing but garbage.
type problemRow struct {
	path  string // absolute file path, as the server published it
	label string // left column: "internal/app/foo.go:42"
	msg   string // message, flattened to one line
	src   string // the server's "source" field ("compiler", "go vet"), often empty
	sev   int    // normalized bucket: SeverityError / Warning / Info

	// start is the diagnostic's start position in LSP coordinates
	// (UTF-16), kept verbatim so the jump can convert it against the
	// buffer it actually lands in — the row is built before the file is
	// necessarily open, so there is no buffer to convert against yet.
	start lsp.Position

	// diag is the server's object, kept whole because a quick fix is
	// matched by fields this client never modelled (diagsForRange's
	// note): the diagnostic goes BACK to the server verbatim.
	diag lsp.Diagnostic
}

// problemsState is the panel's whole state, mutated only on the main
// loop. rows/view follow find-all's indirection exactly: rows is
// everything the server said, view is the subset the filters admit, and
// selected/scroll are VIEW positions — so "the problems" and "the
// problems you are looking at" can never disagree.
type problemsState struct {
	open bool
	// height is the user-chosen row count from a drag or the resize
	// leaders; 0 means "auto". Session-only, like every other panel's —
	// ced deliberately has no layout config.
	height int

	rows []problemRow
	view []int

	selected int
	scroll   int

	// hidden is the severity filter, indexed by bucket (the array is
	// four wide so the LSP severity number indexes it directly). A
	// hidden severity leaves the view but never the counts — the chip
	// keeps telling the truth about what exists, which is the whole
	// point of hiding it behind a toggle rather than a scroll.
	hidden [4]bool

	// thisFile narrows the list to the active tab's file. Off by
	// default: the panel exists to show what the editor's per-file
	// surfaces cannot. It matters because the status-bar segment that
	// opens this panel counts the ACTIVE FILE only, so a user who clicks
	// "✗ 2" and sees eleven rows needs one click to reconcile the two.
	thisFile bool
}

// -----------------------------------------------------------------------------
// Toggle + refresh
// -----------------------------------------------------------------------------

// menuToggleProblems is the ≡ Code row, the Esc-! leader, and the
// status-bar diagnostic segment's click. Opening claims the bottom
// strip (the git panels, the compare view and a bottom-docked terminal
// yield — single occupancy, the rule they already enforce against each
// other) and refreshes immediately, so the list reflects this instant
// rather than whenever the server last spoke.
func (a *App) menuToggleProblems() {
	a.closeMenu()
	a.problems.open = !a.problems.open
	if !a.problems.open {
		return
	}
	a.gitPanel.open = false
	a.gitLog.open = false
	a.closeComparePanel()
	if !a.termDockLeft {
		a.term.open = false
		a.term.focused = false
	}
	a.refreshProblems()
	// Land on the active file's first problem. The panel lists the whole
	// project, but the user arrived here from a specific file (the status
	// segment, or the file they were reading) and the row they meant is
	// almost never row one of some other file.
	a.problemsSelectActiveFile()
}

// problemsToggleLabel names the action the row will perform — the
// dynamic-label convention every toggle row in the ≡ menu follows.
func (a *App) problemsToggleLabel() string {
	if a.problems.open {
		return "Hide problems"
	}
	return "Show problems"
}

// hasAnyDiagnostics gates the next/previous rows. It reads the
// diagnostics map rather than the panel's rows on purpose: a menu
// predicate runs on every frame the menu is drawn, and rebuilding the
// row list there would sort the whole project's problems to answer a
// yes/no question.
func (a *App) hasAnyDiagnostics() bool { return len(a.lsp.diags) > 0 }

// refreshProblems rebuilds the row list from the diagnostics map and
// re-derives the view, keeping the selection on the same underlying
// problem when it survives.
//
// Called on open, from the ⟳ button, from a publish or a server exit
// while the panel is on screen, and from the next/previous verbs —
// which walk this same list whether or not the panel is visible, and so
// are the reason a closed panel's rows are allowed to go stale: nobody
// reads them without refreshing first.
func (a *App) refreshProblems() {
	prev := ""
	if r := a.problemRow(a.problems.selected); r != nil {
		prev = problemKey(*r)
	}
	a.problems.rows = a.buildProblemRows()
	a.rebuildProblemsView()
	// Re-find the row the user was on. Identity, not index: a publish
	// can insert problems above the selection, and an index-preserving
	// refresh would silently move the highlight to a different problem.
	if prev != "" {
		for vi, ri := range a.problems.view {
			if problemKey(a.problems.rows[ri]) == prev {
				a.problems.selected = vi
				break
			}
		}
	}
	a.problemsClampScroll()
	a.problemsEnsureSelectedVisible()
}

// problemKey identifies a problem across refreshes: where it is and what
// it says. Deliberately NOT the column — a diagnostic's range shifts by
// a character as you type on its line, and a selection that jumped every
// time you pressed a key would be no selection at all.
func problemKey(r problemRow) string {
	return fmt.Sprintf("%s:%d:%s", r.path, r.start.Line, r.msg)
}

// buildProblemRows flattens the diagnostics map into a sorted slice.
//
// Sorted by (path, line, character) with the paths in lexical order:
// a map iterates randomly, and a list whose rows shuffle between frames
// is unusable. The order also makes each file's problems contiguous,
// which is what lets the next/previous verbs treat the whole project as
// one document (see problemsSeek).
func (a *App) buildProblemRows() []problemRow {
	rows := make([]problemRow, 0, 16)
	for path, diags := range a.lsp.diags {
		for _, d := range diags {
			rows = append(rows, problemRow{
				path:  path,
				label: fmt.Sprintf("%s:%d", a.problemRelPath(path), d.Range.Start.Line+1),
				msg:   flattenProblemMsg(d.Message),
				src:   d.Source,
				sev:   problemSeverity(d.Severity),
				start: d.Range.Start,
				diag:  d,
			})
		}
	}
	sort.SliceStable(rows, func(i, j int) bool {
		if rows[i].path != rows[j].path {
			return rows[i].path < rows[j].path
		}
		if rows[i].start.Line != rows[j].start.Line {
			return rows[i].start.Line < rows[j].start.Line
		}
		return rows[i].start.Character < rows[j].start.Character
	})
	return rows
}

// problemRelPath renders a path for the left column: relative to the
// project root when it is inside it, absolute otherwise. gopls publishes
// for anything in its workspace, which can include files above the
// editor's root — a ../../.. chain would be longer than the absolute
// path and read worse, so the outside case keeps the real thing and lets
// the column's front-truncation do the shortening.
func (a *App) problemRelPath(path string) string {
	if a.rootDir == "" {
		return path
	}
	rel, err := filepath.Rel(a.rootDir, path)
	if err != nil || strings.HasPrefix(rel, "..") {
		return path
	}
	return rel
}

// flattenProblemMsg puts a multi-line diagnostic on one line. gopls
// wraps type errors across several lines and pads the continuations;
// a row is one line tall, so the alternative to flattening is silently
// showing the first fragment and hiding the part that explains it.
func flattenProblemMsg(msg string) string {
	if !strings.ContainsAny(msg, "\n\r\t") {
		return strings.TrimSpace(msg)
	}
	fields := strings.FieldsFunc(msg, func(r rune) bool {
		return r == '\n' || r == '\r' || r == '\t'
	})
	for i := range fields {
		fields[i] = strings.TrimSpace(fields[i])
	}
	return strings.Join(fields, " ")
}

// problemSeverity normalizes an LSP severity into the three buckets the
// editor paints and counts: hints ride with information (they are the
// same colour in the gutter), and an omitted severity is an error per
// the spec — the same folding diagCounts and diagSeverityColor use, so
// the panel, the status bar and the underlines can never disagree about
// what counts as a warning.
func problemSeverity(sev int) int {
	switch sev {
	case lsp.SeverityWarning:
		return lsp.SeverityWarning
	case lsp.SeverityInfo, lsp.SeverityHint:
		return lsp.SeverityInfo
	default:
		return lsp.SeverityError
	}
}

// rebuildProblemsView recomputes the display order from the severity
// and scope filters, clamping the selection into the survivors.
// Document order is preserved: the filters narrow the list, they never
// re-rank it.
func (a *App) rebuildProblemsView() {
	p := &a.problems
	scope := ""
	if p.thisFile {
		if t := a.activeTabPtr(); t != nil {
			scope = t.Path
		}
	}
	p.view = p.view[:0]
	for i := range p.rows {
		if p.hidden[p.rows[i].sev] {
			continue
		}
		if p.thisFile && p.rows[i].path != scope {
			continue
		}
		p.view = append(p.view, i)
	}
	if p.selected >= len(p.view) {
		p.selected = len(p.view) - 1
	}
	if p.selected < 0 {
		p.selected = 0
	}
}

// problemRow returns the row behind view position vi, or nil.
func (a *App) problemRow(vi int) *problemRow {
	if vi < 0 || vi >= len(a.problems.view) {
		return nil
	}
	return &a.problems.rows[a.problems.view[vi]]
}

// problemsCounts tallies the rows the SCOPE admits, by severity bucket.
// Scope but not severity: the chips describe the set they filter, so a
// hidden severity still shows its count (that count is the argument for
// unhiding it), while a chip counting files the scope excludes would be
// counting rows the panel can't show at all.
func (a *App) problemsCounts() (errs, warns, infos int) {
	scope := ""
	if a.problems.thisFile {
		if t := a.activeTabPtr(); t != nil {
			scope = t.Path
		}
	}
	for _, r := range a.problems.rows {
		if a.problems.thisFile && r.path != scope {
			continue
		}
		switch r.sev {
		case lsp.SeverityWarning:
			warns++
		case lsp.SeverityInfo:
			infos++
		default:
			errs++
		}
	}
	return errs, warns, infos
}

// problemsSelectActiveFile puts the highlight on the first row belonging
// to the file on screen, leaving it alone when that file is clean. Used
// on open only — afterwards the selection is the user's.
func (a *App) problemsSelectActiveFile() {
	t := a.activeTabPtr()
	if t == nil || t.Path == "" {
		return
	}
	for vi, ri := range a.problems.view {
		if a.problems.rows[ri].path == t.Path {
			a.problems.selected = vi
			a.problemsEnsureSelectedVisible()
			return
		}
	}
}

// -----------------------------------------------------------------------------
// Geometry
// -----------------------------------------------------------------------------

// problemsHeight returns the panel's row count for the current window —
// user choice wins, auto mode takes a third of the screen, both
// re-clamped against the live window. Mirrors gitLogHeight.
func (a *App) problemsHeight() int {
	h := a.problems.height
	if h == 0 {
		h = a.height / 3
		if h > problemsMaxHeight {
			h = problemsMaxHeight
		}
	}
	if h < problemsMinHeight {
		h = problemsMinHeight
	}
	if max := a.maxProblemsHeight(); h > max {
		h = max
	}
	return h
}

// maxProblemsHeight is the tallest the panel may grow while leaving the
// editor its minimum working rows — the hard limit even explicit drags
// respect.
func (a *App) maxProblemsHeight() int {
	max := a.height - 2 - problemsMinEditorRows
	max -= a.findBarRows()
	if max < problemsMinHeight {
		max = problemsMinHeight
	}
	return max
}

// resizeProblems records a user-chosen height, clamped to the legal
// band, and re-clamps the scroll against the new viewport.
func (a *App) resizeProblems(target int) {
	if target < problemsMinHeight {
		target = problemsMinHeight
	}
	if max := a.maxProblemsHeight(); target > max {
		target = max
	}
	a.problems.height = target
	a.problemsClampScroll()
}

// dragProblemsPanelTo resizes the panel so its header rule tracks the
// mouse row during a drag.
func (a *App) dragProblemsPanelTo(y int) {
	bottom := a.height - 1
	bottom -= a.findBarRows()
	a.resizeProblems(bottom - y)
}

// growProblems / shrinkProblems are the Esc-= / Esc-- targets while the
// panel owns the bottom strip. Silent no-ops while collapsed, per the
// leader contract; single occupancy guarantees at most one panel acts.
func (a *App) growProblems() {
	if !a.problems.open {
		return
	}
	a.resizeProblems(a.problemsHeight() + problemsResizeStep)
}

// shrinkProblems steps the panel shorter; see growProblems.
func (a *App) shrinkProblems() {
	if !a.problems.open {
		return
	}
	a.resizeProblems(a.problemsHeight() - problemsResizeStep)
}

// problemsRect returns the panel's on-screen rectangle — the same slot
// the git panels occupy (they swap, never stack).
func (a *App) problemsRect() (x, y, w, h int) {
	lw := a.leftBlockW()
	h = a.problemsHeight()
	y = a.height - 1 - h
	y -= a.findBarRows()
	return lw, y, a.width - lw - a.rightBlockW(), h
}

// problemsContains reports whether (x, y) falls inside the open panel.
func (a *App) problemsContains(x, y int) bool {
	if !a.problems.open {
		return false
	}
	px, py, pw, ph := a.problemsRect()
	return x >= px && x < px+pw && y >= py && y < py+ph
}

// problemsVisibleRows is how many result rows fit below the header.
func (a *App) problemsVisibleRows() int {
	_, _, _, ph := a.problemsRect()
	if ph < 1 {
		return 0
	}
	return ph - 1
}

// problemsClampScroll pins the scroll offset into range. Hard clamp, no
// overscroll — it is a viewer, same rationale as the git panels.
func (a *App) problemsClampScroll() {
	max := len(a.problems.view) - a.problemsVisibleRows()
	if max < 0 {
		max = 0
	}
	if a.problems.scroll > max {
		a.problems.scroll = max
	}
	if a.problems.scroll < 0 {
		a.problems.scroll = 0
	}
}

// problemsEnsureSelectedVisible scrolls the list just enough to bring
// the highlight into view. Called only when the selection moves — never
// from generic clamping, so wheel-scrolling away from the highlight
// doesn't snap back (the cursorMoved rule, shared with every list here).
func (a *App) problemsEnsureSelectedVisible() {
	vis := a.problemsVisibleRows()
	if vis <= 0 {
		return
	}
	if a.problems.selected < a.problems.scroll {
		a.problems.scroll = a.problems.selected
	}
	if a.problems.selected >= a.problems.scroll+vis {
		a.problems.scroll = a.problems.selected - vis + 1
	}
	a.problemsClampScroll()
}

// problemsCloseRect is the ✕ collapse button in the header row.
func (a *App) problemsCloseRect() btnRect {
	px, py, pw, _ := a.problemsRect()
	return btnRect{x: px + pw - 4, y: py, w: 3}
}

// problemsRefreshRect is the ⟳ button. The list already follows the
// server, so this is for the other direction: a manual rebuild after a
// file was fixed outside the editor, and a visible way to confirm the
// panel is telling the truth.
func (a *App) problemsRefreshRect() btnRect {
	c := a.problemsCloseRect()
	return btnRect{x: c.x - 4, y: c.y, w: 3}
}

// problemsChip is one header filter button: where it is, what it says,
// which bucket it filters (problemsChipScope for the scope toggle), and
// whether its filter currently ADMITS rows — "lit" means "these are in
// the list".
type problemsChip struct {
	rect  btnRect
	label string
	kind  int
	on    bool
}

// problemsChips lays the header's filter run out left to right, and is
// the single geometry source draw and hit-testing both read.
//
// Every chip is drawn even at zero — the vocabulary is fixed so the user
// learns where each one is, and a chip that vanished when its count hit
// zero would be a chip you can't click to bring its rows back. Chips
// that would collide with the ⟳ / ✕ buttons are dropped from the RIGHT
// (the scope toggle goes first), because a control you can't reach is
// worse than a filter you have to open the menu for.
func (a *App) problemsChips() []problemsChip {
	px, py, pw, _ := a.problemsRect()
	errs, warns, infos := a.problemsCounts()
	scopeLabel := " all files "
	if a.problems.thisFile {
		scopeLabel = " this file "
	}
	want := []problemsChip{
		{label: fmt.Sprintf(" ✗ %d ", errs), kind: lsp.SeverityError, on: !a.problems.hidden[lsp.SeverityError]},
		{label: fmt.Sprintf(" ⚠ %d ", warns), kind: lsp.SeverityWarning, on: !a.problems.hidden[lsp.SeverityWarning]},
		{label: fmt.Sprintf(" ℹ %d ", infos), kind: lsp.SeverityInfo, on: !a.problems.hidden[lsp.SeverityInfo]},
		{label: scopeLabel, kind: problemsChipScope, on: a.problems.thisFile},
	}
	chips := make([]problemsChip, 0, len(want))
	x := px + 1
	for _, c := range want {
		w := runeLen(c.label)
		if x+w > px+pw-problemsHeaderReserve {
			break
		}
		c.rect = btnRect{x: x, y: py, w: w}
		chips = append(chips, c)
		x += w + 1
	}
	return chips
}

// problemsLabelWidth is the width of the `path:line` column: enough for
// the widest label in the VIEW, floored so a narrow panel still says
// something, and capped at two fifths of the panel so the paths can
// never crowd out the messages they introduce.
func (a *App) problemsLabelWidth() int {
	_, _, pw, _ := a.problemsRect()
	widest := 0
	for _, ri := range a.problems.view {
		widest = max(widest, runeLen(a.problems.rows[ri].label))
	}
	// The cap is a SHARE of the panel rather than a constant (find-all's
	// reasoning): the strip is as wide as the window, and a fixed column
	// that reads well at 200 cells eats a narrow one alive.
	return min(widest, max(pw*2/5, problemsMinLabelW))
}

// problemsRowIndexAt maps a screen cell to the view index drawn there,
// or -1 for anything that isn't a row.
func (a *App) problemsRowIndexAt(x, y int) int {
	px, py, pw, ph := a.problemsRect()
	if x < px || x >= px+pw || y <= py || y >= py+ph {
		return -1
	}
	idx := a.problems.scroll + (y - py - 1)
	if idx < 0 || idx >= len(a.problems.view) {
		return -1
	}
	return idx
}

// -----------------------------------------------------------------------------
// Mouse
// -----------------------------------------------------------------------------

// problemsPress routes an initial left press inside the panel and
// reports the drag it started ("" for none): the header rule outside the
// buttons is the height handle, the chips toggle their filter, the two
// header buttons do their thing, and a body row selects and jumps.
func (a *App) problemsPress(x, y int) (dragMode string) {
	_, py, _, _ := a.problemsRect()
	if y == py {
		for _, c := range a.problemsChips() {
			if c.rect.contains(x, y) {
				a.toggleProblemsChip(c.kind)
				return ""
			}
		}
		switch {
		case a.problemsRefreshRect().contains(x, y):
			a.refreshProblems()
			a.flash("Problems refreshed")
		case a.problemsCloseRect().contains(x, y):
			a.problems.open = false
		default:
			return "problems"
		}
		return ""
	}
	if idx := a.problemsRowIndexAt(x, y); idx >= 0 {
		a.problemsSelectRow(idx, true)
	}
	return ""
}

// toggleProblemsChip flips one filter and re-derives the view. The
// selection is preserved by identity across the rebuild — hiding
// warnings while parked on an error must not move you off the error.
func (a *App) toggleProblemsChip(kind int) {
	prev := ""
	if r := a.problemRow(a.problems.selected); r != nil {
		prev = problemKey(*r)
	}
	if kind == problemsChipScope {
		a.problems.thisFile = !a.problems.thisFile
	} else {
		a.problems.hidden[kind] = !a.problems.hidden[kind]
	}
	a.rebuildProblemsView()
	if prev != "" {
		for vi, ri := range a.problems.view {
			if problemKey(a.problems.rows[ri]) == prev {
				a.problems.selected = vi
				break
			}
		}
	}
	a.problemsClampScroll()
	a.problemsEnsureSelectedVisible()
}

// problemsScroll wheels the list without touching the selection — the
// wheel reads the panel, it doesn't drive the editor.
func (a *App) problemsScroll(delta int) {
	a.problems.scroll += delta
	a.problemsClampScroll()
}

// problemsSelectRow highlights view row idx and, when jump is set, takes
// the editor there. The single write path for the selection, so the
// mouse, the context menu and the next/previous verbs can't disagree
// about what selecting implies.
func (a *App) problemsSelectRow(idx int, jump bool) {
	if len(a.problems.view) == 0 {
		return
	}
	if idx < 0 {
		idx = 0
	}
	if idx >= len(a.problems.view) {
		idx = len(a.problems.view) - 1
	}
	a.problems.selected = idx
	a.problemsEnsureSelectedVisible()
	if jump {
		a.problemsJump(idx)
	}
}

// problemsJump opens the row's file and parks the caret on the problem.
//
// A single click jumps, where find-all's project list waits for a
// double: there, a row is a search HIT and walking the list is how you
// read the results; here a row is a defect and the only thing you can do
// about it is go and look at it. The panel stays open either way — it is
// a worklist you burn down, and the editor keeps the keyboard, so the
// jump costs nothing to undo (Esc-o retraces it).
func (a *App) problemsJump(idx int) {
	r := a.problemRow(idx)
	if r == nil {
		return
	}
	a.openFile(r.path)
	t := a.activeTabPtr()
	if t == nil || t.Path != r.path {
		return // openFile failed and flashed its own reason
	}
	// editorPosFor converts UTF-16 → runes and clamps: a diagnostic can
	// outlive the line it describes (the server lags the buffer by a
	// debounce plus a type-check), and landing near beats not landing.
	t.MoveCursorTo(editorPosFor(t, r.start), false)
	if _, _, ew, eh := a.editorRect(); !t.CursorLineVisible(eh) {
		t.CenterOnCursor(ew, eh)
	}
}

// -----------------------------------------------------------------------------
// Right-click: the quick fix
// -----------------------------------------------------------------------------

// tryProblemsContextClick opens the panel's row menu at the pointer,
// reporting whether it consumed the event (the tree/editor menus'
// contract). It reuses editorContextModal rather than growing a third
// anchored-menu chassis: that type's rows are already plain func(*App)
// with enable predicates, which is exactly this menu's shape.
func (a *App) tryProblemsContextClick(x, y int) bool {
	if !a.problemsContains(x, y) {
		return false
	}
	idx := a.problemsRowIndexAt(x, y)
	if idx < 0 {
		return true // a right-click on the header is swallowed, not escalated
	}
	// Select WITHOUT jumping: the menu is a question about this row, and
	// moving the editor before the user has answered it would be acting
	// on a choice they haven't made.
	a.problemsSelectRow(idx, false)

	items := a.problemsContextItems()
	w := contextMenuWidth
	for _, it := range items {
		if lw := runeLen(it.label) + 6; lw > w {
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

// problemsContextItems builds the row menu. Quick fix leads: it is the
// verb the panel exists to make reachable, and the reason the plan
// wanted a right-click here at all.
func (a *App) problemsContextItems() []editorContextItem {
	return []editorContextItem{
		{label: "Quick fix…", action: (*App).problemsQuickFix, enabled: (*App).hasProblemsQuickFix},
		{label: "Go to problem", action: (*App).problemsGoToSelected, enabled: (*App).hasProblemsSelection},
		{label: "Copy message", action: (*App).problemsCopyMessage, enabled: (*App).hasProblemsSelection},
	}
}

// hasProblemsSelection reports whether a row is under the highlight.
func (a *App) hasProblemsSelection() bool { return a.problemRow(a.problems.selected) != nil }

// hasProblemsQuickFix additionally wants a live server — the fix comes
// from it. The predicate deliberately does NOT ask hasLSPActions: that
// one tests the ACTIVE tab, and the whole point of the row is that the
// problem is somewhere else.
func (a *App) hasProblemsQuickFix() bool {
	return a.hasProblemsSelection() && a.lspReady()
}

// problemsGoToSelected is the menu twin of the row click.
func (a *App) problemsGoToSelected() { a.problemsJump(a.problems.selected) }

// problemsQuickFix jumps to the problem and then asks the server what it
// can do about it.
//
// The jump is not a convenience, it is the mechanism: codeAction is
// asked about a RANGE IN THE ACTIVE DOCUMENT, and its response handler
// drops answers whose path is no longer the active tab (an old list of
// things to do to a file you have left is a list of rows that all do the
// wrong thing). Moving the caret onto the diagnostic first also makes
// diagsForRange find this exact diagnostic and echo it back in the
// request context, which is how a quick fix identifies the problem it
// fixes.
func (a *App) problemsQuickFix() {
	r := a.problemRow(a.problems.selected)
	if r == nil {
		return
	}
	a.problemsJump(a.problems.selected)
	t := a.activeTabPtr()
	if t == nil || t.Path != r.path {
		return
	}
	if !a.hasLSPActions() {
		// The file opened but the server doesn't handle it — possible for
		// a diagnostic published against a non-Go file by a future server.
		a.flash("No code actions for this file")
		return
	}
	a.menuCodeActions()
}

// problemsCopyMessage puts the selected diagnostic's text on the system
// clipboard — the row is often the thing you paste into a search or a
// message, and the panel truncates it on screen.
func (a *App) problemsCopyMessage() {
	r := a.problemRow(a.problems.selected)
	if r == nil {
		return
	}
	text := r.label + ": " + r.msg
	if err := clipboard.CopyToSystem(text); err != nil {
		a.flash(fmt.Sprintf("Copy failed: %v", err))
		return
	}
	a.flash("Copied problem")
}

// -----------------------------------------------------------------------------
// Keyboard: next / previous problem
// -----------------------------------------------------------------------------

// menuNextProblem / menuPrevProblem walk the panel's list from wherever
// the caret is. They are the keyboard twin of clicking a row — the panel
// is mouse-driven furniture (it never takes the keyboard), so without
// them a terminal that eats clicks would have a Problems panel it could
// only look at.
func (a *App) menuNextProblem() { a.stepProblem(1) }

// menuPrevProblem steps the other way; see menuNextProblem.
func (a *App) menuPrevProblem() { a.stepProblem(-1) }

// stepProblem moves to the neighbouring problem and jumps to it.
//
// It works whether or not the panel is open, because the list is state,
// not a view: refreshProblems maintains rows/view regardless, so these
// verbs are "next problem in the project" for everyone and "move the
// panel's highlight" as a side effect for whoever has it open. The
// filters apply either way — a user who hid warnings said so.
func (a *App) stepProblem(delta int) {
	a.closeMenu()
	a.refreshProblems()
	if len(a.problems.view) == 0 {
		a.flash("No problems")
		return
	}
	idx := a.problemsSeek(delta)
	if idx < 0 || idx >= len(a.problems.view) {
		if delta > 0 {
			a.flash("No further problems")
		} else {
			a.flash("No earlier problems")
		}
		return
	}
	a.problemsSelectRow(idx, true)
}

// problemsSeek finds the view index of the problem after (delta > 0) or
// before (delta < 0) the caret, treating the whole project as one
// document ordered exactly the way the list is.
//
// The caret is converted INTO LSP coordinates rather than the rows out
// of them: lspPosFor already exists and needs only the buffer the caret
// is in, whereas converting every row would need each row's file open.
// Returns an out-of-range index when there is nothing further, which
// stepProblem reports rather than clamping — silently landing back on
// the row you were on reads as a broken key.
func (a *App) problemsSeek(delta int) int {
	t := a.activeTabPtr()
	if t == nil || t.Path == "" {
		// No file on screen to be positioned in; fall back to walking
		// from the current highlight.
		return a.problems.selected + delta
	}
	path := t.Path
	caret := lspPosFor(t, t.Cursor)
	after := func(r problemRow) bool {
		if r.path != path {
			return r.path > path
		}
		if r.start.Line != caret.Line {
			return r.start.Line > caret.Line
		}
		return r.start.Character > caret.Character
	}
	if delta > 0 {
		for vi, ri := range a.problems.view {
			if after(a.problems.rows[ri]) {
				return vi
			}
		}
		return len(a.problems.view) // past the last problem
	}
	for vi := len(a.problems.view) - 1; vi >= 0; vi-- {
		r := a.problems.rows[a.problems.view[vi]]
		if !after(r) && !problemAtCaret(r, path, caret) {
			return vi
		}
	}
	return -1 // before the first problem
}

// problemAtCaret reports whether a row starts exactly where the caret
// is. "Previous" means strictly before, so the problem you are already
// standing on must not answer it.
func problemAtCaret(r problemRow, path string, caret lsp.Position) bool {
	return r.path == path && r.start.Line == caret.Line && r.start.Character == caret.Character
}

// -----------------------------------------------------------------------------
// Drawing
// -----------------------------------------------------------------------------

// drawProblems paints the panel: the header rule with the filter chips,
// the title, and the ⟳ / ✕ buttons; then the rows.
func (a *App) drawProblems() {
	px, py, pw, _ := a.problemsRect()
	th := a.theme

	headerSt := tcell.StyleDefault.Background(th.SidebarBG).Foreground(th.Subtle)
	titleSt := tcell.StyleDefault.Background(th.SidebarBG).Foreground(th.Accent).Bold(true)
	listBG := tcell.StyleDefault.Background(th.SidebarBG)

	// The grab handle brightens while seized — the sidebar splitter's
	// grab-handle language, shared by every resizable strip.
	ruleSt := headerSt
	if a.dragMode == "problems" {
		ruleSt = tcell.StyleDefault.Background(th.SidebarBG).Foreground(th.Accent)
	}
	for cx := px; cx < px+pw; cx++ {
		a.screen.SetContent(cx, py, '─', nil, ruleSt)
	}

	closeBtn := a.problemsCloseRect()
	drawAt(a.screen, closeBtn.x, closeBtn.y, " ✕ ", titleSt)
	refreshBtn := a.problemsRefreshRect()
	drawAt(a.screen, refreshBtn.x, refreshBtn.y, " ⟳ ", titleSt)

	chips := a.problemsChips()
	rightMost := px
	for _, c := range chips {
		drawAt(a.screen, c.rect.x, c.rect.y, c.label, a.problemsChipStyle(c))
		rightMost = c.rect.x + c.rect.w
	}

	// Title after the chips, dropped rather than overlapped when the
	// window can't hold both — the chips ARE controls, the title is a
	// label.
	title := " Problems · " + itoa(len(a.problems.view))
	if n := len(a.problems.rows); n != len(a.problems.view) {
		title += " of " + itoa(n)
	}
	title += " "
	if tx := rightMost + 1; tx+runeLen(title) <= refreshBtn.x {
		drawAt(a.screen, tx, py, title, titleSt)
	}

	// Body.
	vis := a.problemsVisibleRows()
	labelW := a.problemsLabelWidth()
	for row := 0; row < vis; row++ {
		ry := py + 1 + row
		for cx := px; cx < px+pw; cx++ {
			a.screen.SetContent(cx, ry, ' ', nil, listBG)
		}
		idx := a.problems.scroll + row
		if idx >= len(a.problems.view) {
			continue
		}
		a.drawProblemRow(idx, px, ry, pw, labelW)
	}
	if len(a.problems.view) == 0 && vis > 0 {
		drawStatusText(a.screen, px+2, py+1, pw-4, a.problemsEmptyText(),
			tcell.StyleDefault.Background(th.SidebarBG).Foreground(th.Muted))
	}
}

// problemsEmptyText explains an empty list, which has four quite
// different causes — and "no problems" would be a lie for three of them.
func (a *App) problemsEmptyText() string {
	switch {
	case len(a.problems.rows) > 0:
		return fmt.Sprintf("%d problems hidden by the filters — click a chip to bring them back",
			len(a.problems.rows))
	case a.lsp.dead:
		return "No language server — nothing is reporting problems"
	case !a.lspReady():
		return "Language server starting…"
	default:
		return "No problems"
	}
}

// problemsChipStyle paints a chip by state: lit (its rows are in the
// list) in its own severity colour on the button background, dimmed
// against the header when its filter is excluding rows — so the header
// reads as a row of switches at a glance, without a legend.
//
// The scope chip has no severity colour to wear, so it borrows the
// accent; lit there means "narrowed to this file", which is the state
// worth noticing because it is the one that hides rows silently.
func (a *App) problemsChipStyle(c problemsChip) tcell.Style {
	th := a.theme
	if !c.on {
		return tcell.StyleDefault.Background(th.SidebarBG).Foreground(th.Subtle)
	}
	fg := th.Accent
	if c.kind != problemsChipScope {
		fg = diagSeverityColor(th, c.kind)
	}
	return tcell.StyleDefault.Background(th.Selection).Foreground(fg).Bold(true)
}

// drawProblemRow paints one problem: severity glyph, the `path:line`
// label, a rule, the message, and — when the row is wide enough — the
// server's source name right-aligned. Mirrors find-all's row so the two
// worklists read as one instrument.
func (a *App) drawProblemRow(idx, px, ry, pw, labelW int) {
	th := a.theme
	r := a.problems.rows[a.problems.view[idx]]

	bg := th.SidebarBG
	if idx == a.problems.selected {
		bg = th.Selection
		for cx := px; cx < px+pw; cx++ {
			a.screen.SetContent(cx, ry, ' ', nil, tcell.StyleDefault.Background(bg))
		}
	}
	textSt := tcell.StyleDefault.Background(bg).Foreground(th.Text)
	mutedSt := tcell.StyleDefault.Background(bg).Foreground(th.Muted)
	ruleSt := tcell.StyleDefault.Background(bg).Foreground(th.Subtle)
	sevSt := tcell.StyleDefault.Background(bg).Foreground(diagSeverityColor(th, r.sev))

	a.screen.SetContent(px+1, ry, problemGlyph(r.sev), nil, sevSt)

	// The label is cut from the FRONT (rowLabelText, shared with find-all's
	// project rows) because the distinguishing part of a path is its tail.
	labelSt := mutedSt
	if idx == a.problems.selected {
		labelSt = tcell.StyleDefault.Background(bg).Foreground(th.AccentSoft)
	}
	drawAt(a.screen, px+3, ry, rowLabelText(r.label, labelW), labelSt)
	a.screen.SetContent(px+3+labelW+1, ry, '│', nil, ruleSt)

	msgStart := px + 3 + labelW + 3
	msgEnd := px + pw - 1
	// The source tail claims the right edge only when the message keeps a
	// readable share — otherwise the message wins (gitlog's rule).
	if r.src != "" {
		tail := " " + r.src + " "
		if tw := runeLen(tail); msgStart+24+tw < msgEnd {
			drawAt(a.screen, msgEnd-tw, ry, tail, mutedSt)
			msgEnd -= tw
		}
	}
	if msgEnd > msgStart {
		drawStatusText(a.screen, msgStart, ry, msgEnd-msgStart, r.msg, textSt)
	}
}

// problemGlyph is the row's severity marker, matching the status bar's
// vocabulary exactly (✗ ⚠ ℹ) so the count you clicked and the rows you
// landed on are visibly the same thing. All three are single-width, per
// the marker rule.
func problemGlyph(sev int) rune {
	switch sev {
	case lsp.SeverityWarning:
		return '⚠'
	case lsp.SeverityInfo:
		return 'ℹ'
	default:
		return '✗'
	}
}
