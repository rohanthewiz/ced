// =============================================================================
// File: internal/app/diffview.go
// Author: Rohan Allison <rohanthewiz@gmail.com>
// Created: 2026-10-22
// Copyright: 2026 Rohan Allison. All rights reserved.
// =============================================================================

// diffview.go is the layer every diff PANE shares — the compare panel,
// the git panel's diff pane, the git log's commit detail, and the
// cherry-pick dialog's preview: a side-by-side layout, an
// ignore-whitespace switch, and stepping from one change to the next.
//
//	unified (the text as git prints it)     side by side (the same text, paired)
//	────────────────────────────────────    ───────────────────────────────────────
//	@@ -10,4 +10,4 @@ func f()             @@ -10,4 +10,4 @@ func f()
//	 a                                      10 a              │10 a
//	-b                                      11 b              │11 B
//	-c                                      12 c              │
//	+B                                      13 d              │12 d
//	 d
//
// Design choices:
//
//   - THE UNIFIED TEXT STAYS THE MODEL. Every surface keeps the lines it
//     always had (git's own output, or internal/diff's byte-compatible
//     rendering), and everything that already reads them — hunk spans
//     and their git-apply patches, diffTargetLine's jump, the log's
//     file/line mapper — keeps working unchanged. Side by side is a
//     LAYOUT over those lines: a list of display rows, each naming the
//     unified line(s) it shows. A surface's scroll counts display rows;
//     anything that needs a line asks the row (diffRow.line), never
//     "scroll + y".
//   - ONE LAYOUT FUNCTION FOR BOTH MODES. Unified is the degenerate
//     layout (one full-width row per line), so draw, hit-test, the
//     overflow markers and the change stepper read the same []diffRow
//     whichever mode is on, and there is no "if split" outside the
//     painter.
//   - PAIRING IS POSITIONAL WITHIN A RUN. A run of "-" lines followed by
//     a run of "+" lines is zipped row by row (the first removed line
//     beside the first added one); the longer run's tail sits against a
//     blank. No intra-line matching: the panes are review strips, and a
//     cleverer pairing would be a second differ to get wrong.
//   - HEADERS STAY FULL WIDTH. File headers, "@@" lines, the git panel's
//     staged/unstaged markers and a commit's metadata are drawn across
//     the whole pane in both modes — which is also what keeps the hunk
//     chips (gitpanelhunks.go, the compare panel's revert) on the row
//     they have always been on.
//   - A COMBINED DIFF (a merge in the log, an unmerged file in the git
//     panel) is never split: its lines carry one column per parent and
//     have no single "old" side to put on the left.
//   - THE PREFERENCES ARE GLOBAL. "Side by side" is one setting for
//     every pane, persisted ("diffview" in config.json) because it is a
//     way of reading that a user picks once. "Ignore whitespace" is a
//     per-session switch, like the find options: it hides real edits,
//     so it should not still be on tomorrow without anybody choosing it.
//   - NEXT / PREVIOUS STEP BY CHANGE, not by hunk: a hunk can hold several
//     separate edits with context between them, and the stepper lands on
//     each. The current change is marked (▸ in the column left of the
//     text) so "next" visibly means "after THIS one".
//
// Doors: a toolbar in each panel's header (▲ ▼ split ws), the ≡ View
// rows for keyboards and terminals that eat clicks, and alt chords inside
// the cherry-pick dialog (a modal owns the keyboard there).

package app

import (
	"strings"

	"github.com/gdamore/tcell/v2"
	"github.com/rohanthewiz/ced/internal/userconfig"
)

// diffNavMargin is how many rows of context a stepped-to change lands
// with above it: enough to see what it follows, not so many that a
// short pane spends itself on lines that didn't change.
const diffNavMargin = 2

// -----------------------------------------------------------------------------
// Layout
// -----------------------------------------------------------------------------

// diffLineKind classifies one unified line for the layout.
type diffLineKind uint8

const (
	diffLineMeta    diffLineKind = iota // header, marker, metadata — full width
	diffLineContext                     // " x" inside a hunk
	diffLineDel                         // "-x" inside a hunk
	diffLineAdd                         // "+x" inside a hunk
	diffLineNote                        // "\ No newline…" — full width, rides with its run
)

// diffRow is one DISPLAY row of a diff pane. A full row shows unified
// line left (== right) across the whole pane; a paired row shows the
// old-side line left on the left and the new-side line right on the
// right, either of which may be -1 for a blank side.
type diffRow struct {
	full        bool
	left, right int
	// oldNo / newNo are the 1-based line numbers the side-by-side
	// gutter prints; 0 where the side is blank.
	oldNo, newNo int
	// change marks a row that shows a changed line on either side — the
	// unit the Next / Previous stepper walks.
	change bool
}

// line is the unified line a row stands for when one line is wanted —
// a jump target, a hunk header. The NEW side wins: a jump lands in the
// file as it is now, which is what diffTargetLine maps from.
func (r diffRow) line() int {
	if r.right >= 0 {
		return r.right
	}
	return r.left
}

// anchor is the FIRST unified line a row shows. Unified lines increase
// down a layout when read this way, which is what lets a mode switch
// find the row that holds the line the old top row held.
func (r diffRow) anchor() int {
	switch {
	case r.left < 0:
		return r.right
	case r.right < 0:
		return r.left
	}
	return min(r.left, r.right)
}

// covers reports whether a row shows unified line i on either side.
func (r diffRow) covers(i int) bool { return i >= 0 && (r.left == i || r.right == i) }

// classifyDiffLines walks a unified document once and says what each
// line is, with the old/new line numbers inside hunks. Hunk state ends
// at the first line that cannot be hunk body — the next file header, a
// marker, a blank line, a commit's metadata — so the stat lines and
// indented message lines of `git show` (which start with a space) are
// never mistaken for context.
func classifyDiffLines(lines []string) (kinds []diffLineKind, oldNos, newNos []int, combined []bool) {
	kinds = make([]diffLineKind, len(lines))
	oldNos = make([]int, len(lines))
	newNos = make([]int, len(lines))
	combined = make([]bool, len(lines))
	inHunk, inCombined := false, false
	oldNo, newNo := 0, 0
	for i, ln := range lines {
		switch {
		case ln == gitPanelUntrackedHeader:
			// The git panel's synthesized all-added view: every line
			// after it is a "+" line numbered from 1.
			inHunk, inCombined = true, false
			oldNo, newNo = 0, 1
			continue
		case isGitPanelMarker(ln):
			inHunk, inCombined = false, false
			continue
		case strings.HasPrefix(ln, "@@@"):
			// Combined diff: never split, but its +/- lines still count
			// as changes for the stepper.
			inHunk, inCombined = false, true
			continue
		case strings.HasPrefix(ln, "@@"):
			inCombined = false
			a, _, c, _, ok := parseHunkHeader(ln)
			inHunk = ok
			oldNo, newNo = a, c
			continue
		}
		if inCombined {
			if ln == "" || !strings.ContainsRune(" +-", rune(ln[0])) {
				inCombined = false
				continue
			}
			combined[i] = true
			continue
		}
		if !inHunk || ln == "" {
			inHunk = false
			continue
		}
		switch ln[0] {
		case ' ':
			kinds[i], oldNos[i], newNos[i] = diffLineContext, oldNo, newNo
			oldNo++
			newNo++
		case '-':
			kinds[i], oldNos[i] = diffLineDel, oldNo
			oldNo++
		case '+':
			kinds[i], newNos[i] = diffLineAdd, newNo
			newNo++
		case '\\':
			kinds[i] = diffLineNote
		default:
			inHunk = false
		}
	}
	return kinds, oldNos, newNos, combined
}

// combinedChange reports whether a combined-diff body line changes
// anything: one of its parent columns carries a + or −. Two columns is
// the ordinary merge; an octopus merge's extra columns are rare enough
// that missing a change in them only costs the stepper a stop.
func combinedChange(ln string) bool {
	for _, c := range ln[:min(len(ln), 2)] {
		if c == '+' || c == '-' {
			return true
		}
	}
	return false
}

// diffLayout builds the display rows for lines. Unified is one full row
// per line; split pairs each hunk's runs of removed and added lines.
func diffLayout(lines []string, split bool) []diffRow {
	kinds, oldNos, newNos, combined := classifyDiffLines(lines)
	rows := make([]diffRow, 0, len(lines))
	if !split {
		for i := range lines {
			ch := kinds[i] == diffLineDel || kinds[i] == diffLineAdd ||
				(combined[i] && combinedChange(lines[i]))
			if kinds[i] == diffLineNote && len(rows) > 0 {
				ch = rows[len(rows)-1].change // rides with its run
			}
			rows = append(rows, diffRow{full: true, left: i, right: i, change: ch})
		}
		return rows
	}

	// Pending run: the removed lines seen since the last context line,
	// then the added lines after them. Zipped when the run ends.
	var dels, adds []int
	flush := func() {
		for k := 0; k < max(len(dels), len(adds)); k++ {
			r := diffRow{left: -1, right: -1, change: true}
			if k < len(dels) {
				r.left, r.oldNo = dels[k], oldNos[dels[k]]
			}
			if k < len(adds) {
				r.right, r.newNo = adds[k], newNos[adds[k]]
			}
			rows = append(rows, r)
		}
		dels, adds = dels[:0], adds[:0]
	}
	for i := range lines {
		switch kinds[i] {
		case diffLineContext:
			flush()
			rows = append(rows, diffRow{left: i, right: i, oldNo: oldNos[i], newNo: newNos[i]})
		case diffLineDel:
			// A removal AFTER additions starts a new run: "+a -b" is two
			// edits, and zipping b beside a would claim one replaced the
			// other in the wrong order.
			if len(adds) > 0 {
				flush()
			}
			dels = append(dels, i)
		case diffLineAdd:
			adds = append(adds, i)
		default:
			ch := combined[i] && combinedChange(lines[i])
			if kinds[i] == diffLineNote {
				ch = len(dels)+len(adds) > 0 || (len(rows) > 0 && rows[len(rows)-1].change)
			}
			flush()
			rows = append(rows, diffRow{full: true, left: i, right: i, change: ch})
		}
	}
	flush()
	return rows
}

// diffChangeStarts lists the display rows where a change begins: a
// changed row whose predecessor is not one.
func diffChangeStarts(rows []diffRow) []int {
	var starts []int
	for i, r := range rows {
		if r.change && (i == 0 || !rows[i-1].change) {
			starts = append(starts, i)
		}
	}
	return starts
}

// diffNumWidth is the side-by-side gutter's width in digits: wide
// enough for the largest line number either side shows.
func diffNumWidth(rows []diffRow) int {
	hi := 0
	for _, r := range rows {
		hi = max(hi, r.oldNo, r.newNo)
	}
	return len(itoa(hi))
}

// -----------------------------------------------------------------------------
// The per-surface cache
// -----------------------------------------------------------------------------

// diffPane is one surface's layout cache plus its change cursor. The
// cache is keyed by the IDENTITY of the lines slice (every surface
// replaces its slice when new text arrives, never edits it in place) and
// the mode, so a frame that changes neither costs nothing — and a new
// diff resets the change cursor with no surface having to remember to.
type diffPane struct {
	key    []string
	split  bool
	built  bool
	rows   []diffRow
	starts []int
	numW   int
	// cur is the index into starts of the change the stepper last landed
	// on; -1 for none.
	cur int
}

// sameSlice reports whether two slices are the same backing array and
// length — the cache's identity test.
func sameSlice(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	return len(a) == 0 || &a[0] == &b[0]
}

// layout returns the rows for lines in the given mode, rebuilding only
// when either changed.
func (p *diffPane) layout(lines []string, split bool) []diffRow {
	if p.built && p.split == split && sameSlice(p.key, lines) {
		return p.rows
	}
	p.key, p.split, p.built = lines, split, true
	p.rows = diffLayout(lines, split)
	p.starts = diffChangeStarts(p.rows)
	p.numW = diffNumWidth(p.rows)
	p.cur = -1
	return p.rows
}

// switchMode re-lays lines in the other mode and returns the scroll
// that keeps the same text at the top. The change cursor is carried
// across too, so stepping continues from where it was.
func (p *diffPane) switchMode(lines []string, from, to bool, scroll int) int {
	old := p.layout(lines, from)
	top, curLine := -1, -1
	if scroll >= 0 && scroll < len(old) {
		top = old[scroll].anchor()
	}
	if p.cur >= 0 && p.cur < len(p.starts) {
		curLine = old[p.starts[p.cur]].anchor()
	}
	rows := p.layout(lines, to)
	if curLine >= 0 {
		for k, s := range p.starts {
			if rows[s].covers(curLine) {
				p.cur = k
				break
			}
		}
	}
	if top < 0 {
		return 0
	}
	for i, r := range rows {
		if r.covers(top) || r.anchor() >= top {
			return i
		}
	}
	return 0
}

// rowOfLine is the display row showing unified line i, or -1.
func (p *diffPane) rowOfLine(i int) int {
	for k, r := range p.rows {
		if r.covers(i) {
			return k
		}
	}
	return -1
}

// lineAt is the unified line behind display row k (new side first), or
// -1 when k is outside the layout.
func (p *diffPane) lineAt(k int) int {
	if k < 0 || k >= len(p.rows) {
		return -1
	}
	return p.rows[k].line()
}

// nextChange picks the change a step lands on, or -1 when there is none
// that way. While the current change is still on screen the step is
// relative to it; once the user has scrolled it away, the step is
// relative to what they are LOOKING at — the first change at or below
// the top row for Next, the last one above it for Previous.
func (p *diffPane) nextChange(dir, scroll, visible int) int {
	n := len(p.starts)
	if p.cur >= 0 && p.cur < n {
		if r := p.starts[p.cur]; r >= scroll && r < scroll+max(visible, 1) {
			if k := p.cur + dir; k >= 0 && k < n {
				return k
			}
			return -1
		}
	}
	if dir > 0 {
		for k, r := range p.starts {
			if r >= scroll {
				return k
			}
		}
		return -1
	}
	for k := n - 1; k >= 0; k-- {
		if p.starts[k] < scroll {
			return k
		}
	}
	return -1
}

// curChangeRows is the inclusive display-row range of the current
// change, ok=false when there is none.
func (p *diffPane) curChangeRows() (from, to int, ok bool) {
	if p.cur < 0 || p.cur >= len(p.starts) {
		return 0, 0, false
	}
	from = p.starts[p.cur]
	to = from
	for to+1 < len(p.rows) && p.rows[to+1].change {
		to++
	}
	return from, to, true
}

// clampDiffScroll pins a viewer scroll into range — hard, no overscroll,
// the rule every diff pane already followed.
func clampDiffScroll(scroll, total, visible int) int {
	return max(min(scroll, total-visible), 0)
}

// -----------------------------------------------------------------------------
// Surfaces
// -----------------------------------------------------------------------------

// diffSurface names a panel that shows a diff through this layer. The
// cherry-pick preview is not one: it lives inside a modal with its own
// keys and is reached directly (cherrypickdiff.go).
type diffSurface uint8

const (
	diffSurfNone diffSurface = iota
	diffSurfCompare
	diffSurfGit
	diffSurfLog
)

// diffView is one open surface's handle: its cache, its text, its
// scroll (in display rows) and how many rows it shows.
type diffView struct {
	pane    *diffPane
	lines   []string
	scroll  *int
	visible int
}

// diffViewOf returns the handle for an OPEN surface.
func (a *App) diffViewOf(s diffSurface) (diffView, bool) {
	switch s {
	case diffSurfCompare:
		if a.compare.open {
			_, _, _, ph := a.comparePanelRect()
			return diffView{&a.compare.pane, a.compare.lines, &a.compare.scroll, ph - 1}, true
		}
	case diffSurfGit:
		if a.gitPanel.open {
			_, _, _, ph := a.gitPanelRect()
			return diffView{&a.gitPanel.pane, a.gitPanel.diffLines, &a.gitPanel.diffScroll, ph - 1}, true
		}
	case diffSurfLog:
		if a.gitLog.open {
			return diffView{&a.gitLog.pane, a.gitLog.detailLines, &a.gitLog.detailScroll, a.gitLogBodyRows()}, true
		}
	}
	return diffView{}, false
}

// rows is the surface's layout in the current mode.
func (v diffView) rows(a *App) []diffRow { return v.pane.layout(v.lines, a.diffSplit) }

// activeDiffSurface is the surface the ≡ rows act on: the one the user
// last touched while it is still open, else the first open one with a
// diff in it. Several can be open at once (they dock to different
// edges), so "the one I was just using" is the only reading that does
// not surprise.
func (a *App) activeDiffSurface() diffSurface {
	if v, ok := a.diffViewOf(a.lastDiffSurf); ok && len(v.lines) > 0 {
		return a.lastDiffSurf
	}
	for _, s := range []diffSurface{diffSurfCompare, diffSurfGit, diffSurfLog} {
		if v, ok := a.diffViewOf(s); ok && len(v.lines) > 0 {
			return s
		}
	}
	return diffSurfNone
}

// noteDiffSurface records the surface a press or wheel landed on.
func (a *App) noteDiffSurface(s diffSurface) { a.lastDiffSurf = s }

// -----------------------------------------------------------------------------
// The two preferences
// -----------------------------------------------------------------------------

// setDiffSplit switches every diff pane between unified and side by
// side, keeping each one's top line in view, and persists the choice.
func (a *App) setDiffSplit(on bool) {
	if a.diffSplit == on {
		return
	}
	from := a.diffSplit
	for _, s := range []diffSurface{diffSurfCompare, diffSurfGit, diffSurfLog} {
		if v, ok := a.diffViewOf(s); ok {
			*v.scroll = v.pane.switchMode(v.lines, from, on, *v.scroll)
		}
	}
	if m, ok := a.modal.(*cherryPickModal); ok {
		if lines, loaded := m.diffLines(); loaded {
			m.diff.scroll = m.diff.pane.switchMode(lines, from, on, m.diff.scroll)
		}
	}
	a.diffSplit = on
	a.clampDiffScrolls()
	if on {
		a.flash("Diffs side by side")
	} else {
		a.flash("Diffs unified")
	}
	style := userconfig.DiffViewUnified
	if on {
		style = userconfig.DiffViewSplit
	}
	if err := userconfig.SaveDiffView(userconfig.DefaultPath(), style); err != nil {
		a.flash("config: " + err.Error())
	}
}

// clampDiffScrolls re-clamps every open surface after its row count
// changed under it (a mode switch).
func (a *App) clampDiffScrolls() {
	if a.compare.open {
		a.compareClampScroll()
	}
	if a.gitPanel.open {
		a.gitPanelClampScrolls()
	}
	if a.gitLog.open {
		a.gitLogClampScrolls()
	}
}

// setDiffIgnoreWS switches whitespace-blind diffs on or off and has every
// open surface ask its question again: the compare panel re-diffs the
// texts it holds; the git panes re-run git with or without -w.
func (a *App) setDiffIgnoreWS(on bool) {
	a.diffIgnoreWS = on
	if a.compare.open || a.compare.sideNew != nil {
		a.compareRediff()
	}
	if a.gitPanel.open {
		if f, ok := a.gitPanelSelectedFile(); ok {
			a.requestGitPanelDiff(f)
		}
	}
	if a.gitLog.open {
		if c, ok := a.gitLogSelectedCommit(); ok {
			a.requestGitLogDetail(c)
		}
	}
	if on {
		a.flash("Diffs ignore whitespace — hunk stage/revert in the git panel is off while they do")
	} else {
		a.flash("Diffs show whitespace changes")
	}
}

// -----------------------------------------------------------------------------
// Stepping
// -----------------------------------------------------------------------------

// diffStep moves surface s to its next (dir > 0) or previous change and
// says where it landed. At either end it stays put and says so — a
// wrap would turn "am I done?" into a question the user has to check.
func (a *App) diffStep(s diffSurface, dir int) {
	v, ok := a.diffViewOf(s)
	if !ok {
		return
	}
	a.noteDiffSurface(s)
	rows := v.rows(a)
	p := v.pane
	n := len(p.starts)
	if n == 0 {
		a.flash("No changes here")
		return
	}
	k := p.nextChange(dir, *v.scroll, v.visible)
	if k < 0 {
		if dir > 0 {
			a.flash("No next change")
		} else {
			a.flash("No previous change")
		}
		return
	}
	p.cur = k
	*v.scroll = clampDiffScroll(p.starts[k]-min(diffNavMargin, v.visible/4), len(rows), v.visible)
	a.flash("Change " + itoa(k+1) + " of " + itoa(n))
}

// -----------------------------------------------------------------------------
// ≡ rows
// -----------------------------------------------------------------------------

// menuToggleDiffSplit is the ≡ View twin of the toolbar's split toggle.
func (a *App) menuToggleDiffSplit() {
	a.closeMenu()
	a.setDiffSplit(!a.diffSplit)
}

// diffSplitToggleLabel reads as what the row will do.
func (a *App) diffSplitToggleLabel() string {
	if a.diffSplit {
		return "Diff view: unified"
	}
	return "Diff view: side by side"
}

// menuToggleDiffIgnoreWS is the ≡ View twin of the toolbar's ws toggle.
func (a *App) menuToggleDiffIgnoreWS() {
	a.closeMenu()
	a.setDiffIgnoreWS(!a.diffIgnoreWS)
}

// diffIgnoreWSToggleLabel reads as what the row will do.
func (a *App) diffIgnoreWSToggleLabel() string {
	if a.diffIgnoreWS {
		return "Diff view: show whitespace changes"
	}
	return "Diff view: ignore whitespace"
}

// menuDiffNextChange / menuDiffPrevChange step the active diff pane.
// Unavailable rows explain themselves rather than dimming: with no diff
// panel open the fix is opening one, and the flash says which.
func (a *App) menuDiffNextChange() { a.menuDiffStep(1) }

// menuDiffPrevChange is menuDiffNextChange's other direction.
func (a *App) menuDiffPrevChange() { a.menuDiffStep(-1) }

// menuDiffStep is the two rows' shared body.
func (a *App) menuDiffStep(dir int) {
	a.closeMenu()
	s := a.activeDiffSurface()
	if s == diffSurfNone {
		a.flash("No diff on screen — open the compare panel, the git panel or the git log")
		return
	}
	a.diffStep(s, dir)
}

// -----------------------------------------------------------------------------
// The header toolbar
// -----------------------------------------------------------------------------

// diffToolKind is one toolbar button.
type diffToolKind uint8

const (
	diffToolPrev diffToolKind = iota
	diffToolNext
	diffToolSplit
	diffToolWS
)

// diffToolBtn is one laid-out toolbar button.
type diffToolBtn struct {
	kind diffToolKind
	rect btnRect
}

// diffToolLabel is a button's text, which is also its click width (the
// btnRect rule). ▲/▼ rather than words: the header is shared with each
// panel's own controls, and the hover label (buttontip.go) spells them.
func diffToolLabel(k diffToolKind) string {
	switch k {
	case diffToolPrev:
		return " ▲ "
	case diffToolNext:
		return " ▼ "
	case diffToolSplit:
		return " split "
	}
	return " ws "
}

// diffToolbar lays the toolbar out right-aligned to end at right
// (exclusive), never starting before minX. On a narrow header the
// toggles are shed first (ws, then split) and the stepper pair last, as
// a pair: the ≡ View rows still reach whatever was shed, and a control
// half-drawn over a title is worse than one that isn't there.
func diffToolbar(minX, right, y int) []diffToolBtn {
	sets := [][]diffToolKind{
		{diffToolPrev, diffToolNext, diffToolSplit, diffToolWS},
		{diffToolPrev, diffToolNext, diffToolSplit},
		{diffToolPrev, diffToolNext},
	}
	for _, set := range sets {
		w := len(set) - 1 // one-cell gaps
		for _, k := range set {
			w += runeLen(diffToolLabel(k))
		}
		x := right - w
		if x < minX {
			continue
		}
		btns := make([]diffToolBtn, 0, len(set))
		for _, k := range set {
			lw := runeLen(diffToolLabel(k))
			btns = append(btns, diffToolBtn{kind: k, rect: btnRect{x: x, y: y, w: lw}})
			x += lw + 1
		}
		return btns
	}
	return nil
}

// diffToolbarLeft is where a laid-out toolbar starts, or right when it
// is empty — the edge the header's other text must stop at.
func diffToolbarLeft(btns []diffToolBtn, right int) int {
	if len(btns) == 0 {
		return right
	}
	return btns[0].rect.x
}

// drawDiffToolbar paints the buttons: the steppers in the flat button
// style, the toggles lit while on and muted while off (the find bar's
// and the log's ⌕ toggle language).
func (a *App) drawDiffToolbar(btns []diffToolBtn) {
	th := a.theme
	on := tcell.StyleDefault.Background(th.Selection).Foreground(th.Accent).Bold(true)
	off := tcell.StyleDefault.Background(th.Selection).Foreground(th.Muted)
	for _, b := range btns {
		st := on
		switch b.kind {
		case diffToolSplit:
			if !a.diffSplit {
				st = off
			}
		case diffToolWS:
			if !a.diffIgnoreWS {
				st = off
			}
		}
		drawAt(a.screen, b.rect.x, b.rect.y, diffToolLabel(b.kind), st)
	}
}

// diffToolbarPress runs the button under (x, y), reporting whether one
// claimed the press.
func (a *App) diffToolbarPress(s diffSurface, btns []diffToolBtn, x, y int) bool {
	for _, b := range btns {
		if !b.rect.contains(x, y) {
			continue
		}
		a.noteDiffSurface(s)
		switch b.kind {
		case diffToolPrev:
			a.diffStep(s, -1)
		case diffToolNext:
			a.diffStep(s, 1)
		case diffToolSplit:
			a.setDiffSplit(!a.diffSplit)
		case diffToolWS:
			a.setDiffIgnoreWS(!a.diffIgnoreWS)
		}
		return true
	}
	return false
}

// diffToolbarTips labels the buttons for buttontip.go.
func (a *App) diffToolbarTips(btns []diffToolBtn) []buttonTip {
	tips := make([]buttonTip, 0, len(btns))
	for _, b := range btns {
		var text []string
		switch b.kind {
		case diffToolPrev:
			text = []string{"Previous change"}
		case diffToolNext:
			text = []string{"Next change"}
		case diffToolSplit:
			if a.diffSplit {
				text = []string{"Side by side — click for unified"}
			} else {
				text = []string{"Unified — click for side by side"}
			}
		case diffToolWS:
			if a.diffIgnoreWS {
				text = []string{"Ignoring whitespace — click to show it"}
			} else {
				text = []string{"Click to ignore whitespace changes"}
			}
		}
		tips = append(tips, buttonTip{rect: b.rect, text: text})
	}
	return tips
}

// -----------------------------------------------------------------------------
// Painting
// -----------------------------------------------------------------------------

// diffPaint is where and how one pane is painted: the text origin and
// width, how many rows, the first display row, the palette for full
// rows, and the background the body sits on.
type diffPaint struct {
	x, y, w, n int
	scroll     int
	style      func(line string) tcell.Style
	bg         tcell.Color
}

// drawDiffRows paints the visible rows of a pane. Full rows go through
// the surface's own palette exactly as the unified panes always drew
// them (tabs expanded at the hunk's prefix width, clipped with …). The
// cell left of the text carries ▸ on the current change's rows.
func (a *App) drawDiffRows(lines []string, p *diffPane, rows []diffRow, d diffPaint) {
	if d.w <= 0 {
		return
	}
	curFrom, curTo, hasCur := p.curChangeRows()
	for row := 0; row < d.n; row++ {
		i := d.scroll + row
		if i < 0 || i >= len(rows) {
			break
		}
		r := rows[i]
		ry := d.y + row
		if hasCur && i >= curFrom && i <= curTo && d.x > 0 {
			a.screen.SetContent(d.x-1, ry, '▸', nil,
				tcell.StyleDefault.Background(d.bg).Foreground(a.theme.Accent).Bold(true))
		}
		if r.full {
			line := lines[r.left]
			st := d.style(line)
			if strings.ContainsRune(line, '\t') {
				line = diffDisplayLineCols(line, diffPrefixColsAt(lines, r.left))
			} else {
				line = diffDisplayLine(line)
			}
			drawAt(a.screen, d.x, ry, clipRunes(line, d.w), st)
			continue
		}
		lw := (d.w - 1) / 2
		a.drawDiffSide(lines, r.left, r.oldNo, p.numW, d.x, ry, lw, d.bg)
		a.screen.SetContent(d.x+lw, ry, '│', nil,
			tcell.StyleDefault.Background(d.bg).Foreground(a.theme.Subtle))
		a.drawDiffSide(lines, r.right, r.newNo, p.numW, d.x+lw+1, ry, d.w-lw-1, d.bg)
	}
}

// drawDiffSide paints one half of a paired row: the line number in a
// muted gutter, then the text without its +/-/space prefix, colored by
// that prefix. A blank side is filled with the sidebar tone so "no line
// here" reads as a hole rather than as an empty line of the file.
func (a *App) drawDiffSide(lines []string, idx, no, numW, x, y, w int, bg tcell.Color) {
	if w <= 0 {
		return
	}
	th := a.theme
	if idx < 0 {
		fillRect(a.screen, x, y, w, 1, tcell.StyleDefault.Background(th.SidebarBG))
		return
	}
	line := diffDisplayLine(lines[idx])
	fg := th.Text
	if line != "" {
		switch line[0] {
		case '-':
			fg = th.GitDeleted
		case '+':
			fg = th.GitAdded
		}
		line = line[1:] // the prefix is one ASCII byte
	}
	text := tcell.StyleDefault.Background(bg).Foreground(fg)
	// The gutter only when there's room for it AND a readable stub of
	// text beside it; on a very narrow half the text wins.
	if w >= numW+6 {
		num := itoa(no)
		num = strings.Repeat(" ", max(numW-len(num), 0)) + num
		drawAt(a.screen, x, y, num, tcell.StyleDefault.Background(bg).Foreground(th.Muted))
		x += numW + 1
		w -= numW + 1
	}
	drawAt(a.screen, x, y, clipRunes(line, w), text)
}
