// =============================================================================
// File: internal/app/overflow.go
// Author: Rohan Allison <rohanthewiz@gmail.com>
// Created: 2026-08-27
// Copyright: 2026 Rohan Allison. All rights reserved.
// =============================================================================

// "There is more that way", said once per direction: a single '▴' or
// '▾' in the LAST column of a viewport's first and last row, on every
// surface that scrolls — the editor body, the git panel's diff pane,
// and the file tree — plus a popup on hover saying how much more.
//
//	func handleKey(...) {                    ▴   ← 412 lines above
//	    switch {
//	    ...
//	}                                        ▾   ← 1,842 lines below
//
// This is what the editor has INSTEAD of a scrollbar, and the tree's
// marker is where the shape came from. A rail with a thumb answers "how
// far into this file am I?" — but it answers it with a column of chrome
// running the whole height of the window, permanently, in every file,
// and the owner's verdict on that column was that it is not pleasing to
// look at. The question a reader actually asks at the edge of a
// viewport is narrower: is there more, and how much? A marker answers
// the first half in one cell of one row, and the popup answers the
// second half only when asked.
//
// Three rules carry the design:
//
//   - THE MARKER SHARES THE LAST COLUMN; it reserves nothing. That is
//     what lets it come and go with the content: a marker that cost
//     layout would move the editor's right edge — re-flowing everything
//     the user is reading — on an edit that had nothing to do with
//     layout, which is exactly why the old bar had to keep its column
//     even in a file that fit on screen. Sharing costs the last cell of
//     two rows, and only while there is something to say.
//
//   - IT IS DRAWN UNCONDITIONALLY. No preference gates it, for the
//     reason the ≡ menu's clipped-content arrows aren't gated either:
//     content the user cannot see and has not been told about is the one
//     thing a viewport must never do. The old "scrollbar" config key
//     bought exactly one thing — give me that column back — and there is
//     no column to give back any more.
//
//   - THE MARKER'S COLOR IS WHAT IS OUT THERE. The rail used to plot
//     every off-screen diagnostic and find hit as its own cell, a
//     minimap of positions. With no rail to plot on, that information
//     folds into the marker: it takes the color of the LOUDEST thing in
//     that direction (offscreenKind's precedence), and the popup names
//     the counts. One cell can carry "there is more, and there is an
//     error in it" — which is the part of the minimap that was worth
//     keeping.
package app

import (
	"time"

	"github.com/gdamore/tcell/v2"

	"github.com/rohanthewiz/ced/internal/editor"
	"github.com/rohanthewiz/ced/internal/lsp"
	"github.com/rohanthewiz/ced/internal/plugins"
	"github.com/rohanthewiz/ced/internal/theme"
)

const (
	// overflowUpRune / overflowDownRune are the markers. Both are
	// single-width per the marker rule — the cell is one column, and a
	// double-width glyph would spill into whatever is drawn beside it
	// (the sidebar's splitter, the git panel's frame).
	//
	// '▾' is also the tree's own expand chevron, and position is what
	// tells the two apart: a chevron sits at the HEAD of a row against
	// the indent, this sits at the row's tail. The same distinction
	// holds in the editor, where the only other thing in that column is
	// the horizontal-overflow '›' — and on the one row where both would
	// land, this wins, because "the file runs on" outranks "this line
	// runs on" (the line's own arrow is repeated on every other long
	// row; this marker has exactly one place to be).
	overflowUpRune   = '▴'
	overflowDownRune = '▾'

	// overflowTipDelay is how long the pointer must rest on a marker
	// before the popup opens. Short — this answer is local (a count the
	// draw already computed), so unlike hoverdwell's 400ms there is no
	// round trip to wait out and nothing to cancel. It exists only to
	// keep a pointer SWEEPING along the window's right edge from
	// flashing a box on its way past.
	overflowTipDelay = 250 * time.Millisecond
)

// offscreenKind is what a marker has to report about the direction it
// points in. The values are ordered by PRECEDENCE — a higher one owns
// the marker's color — because one cell stands for the whole rest of
// the document that way, so collisions are the normal case rather than
// the exception and the cell must answer with the loudest thing in
// range.
//
// The caret outranks everything, including an error: it is the one mark
// that is unique (there is exactly one cursor, and if it goes unreported
// the marker has silently failed), while a diagnostic is redundantly
// carried by the status bar's counts, the Problems panel and its own
// gutter dot. Find outranks the diagnostics for the reason the
// decoration merge puts findSource last — an active search is a question
// the user asked, and ambient annotation loses to it.
type offscreenKind uint8

const (
	offNone offscreenKind = iota
	offInfo
	offWarn
	offError
	offFind
	offCaret
)

// offscreen summarises what lies beyond ONE edge of a viewport: how many
// lines (or list rows), and what is in them worth knowing about.
//
// It is the whole payload of a marker — the glyph reads its `lines` to
// decide whether to appear at all, the color reads kind(), and the popup
// reads the rest. Surfaces that have nothing but a line count (the git
// panel's diff pane, the file tree) simply leave the counters at zero.
type offscreen struct {
	lines  int // lines/rows hidden in this direction
	errors int
	warns  int
	infos  int
	hits   int // find matches
	caret  bool
}

// kind reports the loudest thing off-screen this way, which is what the
// marker is colored by. offNone means "just more text", the common case,
// and is drawn in the same muted tone the tree's marker has always used.
func (o offscreen) kind() offscreenKind {
	switch {
	case o.caret:
		return offCaret
	case o.hits > 0:
		return offFind
	case o.errors > 0:
		return offError
	case o.warns > 0:
		return offWarn
	case o.infos > 0:
		return offInfo
	}
	return offNone
}

// detail renders the counters as one line for the popup, loudest first
// so the reason the marker changed color is the first thing read. Empty
// when there is nothing but text out there — the popup then has one line
// and says only how much.
func (o offscreen) detail() string {
	parts := make([]string, 0, 5)
	if o.caret {
		parts = append(parts, "cursor")
	}
	if o.hits > 0 {
		parts = append(parts, plural(o.hits, "hit", "hits"))
	}
	if o.errors > 0 {
		parts = append(parts, plural(o.errors, "error", "errors"))
	}
	if o.warns > 0 {
		parts = append(parts, plural(o.warns, "warning", "warnings"))
	}
	if o.infos > 0 {
		parts = append(parts, plural(o.infos, "note", "notes"))
	}
	return joinMid(parts, " · ")
}

// joinMid is strings.Join with the separator this file uses, kept local
// so a UI file that needs nothing else from strings doesn't import it —
// the same trade itoa makes one package over.
func joinMid(parts []string, sep string) string {
	out := ""
	for i, p := range parts {
		if i > 0 {
			out += sep
		}
		out += p
	}
	return out
}

// offscreenColor picks the marker's foreground. Diagnostics reuse the
// theme's three Diag* colors — the same ones the gutter dot and the
// underline wear — so a red marker at the bottom of the screen and a red
// dot in the gutter are obviously the same fact seen from two distances.
//
// Find uses FindCurrent, not FindMatch: FindMatch is a background TINT (a
// dark amber wash meant to sit under text) and would be all but invisible
// as a foreground.
//
// offNone is Muted rather than Subtle: the marker has something to say,
// and it is competing with whatever text sits in the cells beside it.
func offscreenColor(th theme.Theme, k offscreenKind) tcell.Color {
	switch k {
	case offCaret:
		return th.Accent
	case offFind:
		return th.FindCurrent
	case offError:
		return th.DiagError
	case offWarn:
		return th.DiagWarning
	case offInfo:
		return th.DiagInfo
	}
	return th.Muted
}

// -----------------------------------------------------------------------------
// What is off-screen, per surface
// -----------------------------------------------------------------------------

// editorOffscreen summarises what lies above and below the tab's
// viewport, [firstLine, lastLine] inclusive.
//
// Everything INSIDE the viewport is skipped, which is the rule that
// keeps the markers quiet on a file that fits and informative on a long
// one: on screen the gutter dot, the underline and the find tint are
// already there against the code, saying it better and in place. Same
// argument for the caret — while the cursor is visible the hardware
// cursor is the answer, and a marker for it would be a second cursor to
// explain.
//
// Sources are read from their CACHES, not through DecorationSource.
// Sources are asked per visible window by contract, and this function's
// whole subject is the rest of the file; asking each one for the entire
// buffer would turn a per-frame read into a whole-file walk for the word
// highlighter and the git differ, neither of which has anything to say
// here.
func (a *App) editorOffscreen(t *editor.Tab, firstLine, lastLine int) (above, below offscreen) {
	if t == nil {
		return above, below
	}
	total := t.Buffer.LineCount()
	above.lines = firstLine
	if above.lines < 0 {
		above.lines = 0
	}
	below.lines = total - (lastLine + 1)
	if below.lines < 0 {
		below.lines = 0 // scrolled into clampScroll's overscroll pad
	}

	// side picks which summary a line belongs to, or nil when the line is
	// on screen (or off the end of a buffer a stale diagnostic still
	// refers to — lspDiagSource's rule: diagnostics lag edits by a
	// debounce plus a type-check, so a range past EOF must cull).
	side := func(line int) *offscreen {
		if line < 0 || line >= total {
			return nil
		}
		if line < firstLine {
			return &above
		}
		if line > lastLine {
			return &below
		}
		return nil
	}

	for _, d := range a.lsp.diags[t.Path] {
		if o := side(d.Range.Start.Line); o != nil {
			o.countDiag(lspOffscreenKind(d.Severity))
		}
	}

	// Plugin diagnostics, gated at the READ the way every other plugin
	// surface is — the kill switch has to be honored at every surface,
	// not only at load.
	if a.plugins.enabled {
		for _, diags := range a.plugins.decos[t.Path] {
			for _, d := range diags {
				if o := side(d.Line); o != nil {
					o.countDiag(pluginOffscreenKind(d.Severity))
				}
			}
		}
	}

	// Find hits. The match list is already whole-buffer (the find bar
	// scans the file, not the window), so this is a walk of data that
	// exists rather than a search.
	for _, m := range t.FindMatches {
		if o := side(m.Line); o != nil {
			o.hits++
		}
	}

	if o := side(t.Cursor.Line); o != nil {
		o.caret = true
	}
	return above, below
}

// countDiag adds one diagnostic to the right counter. A method rather
// than three branches at each call site because both diagnostic sources
// (gopls and the plugins) feed it and must land in the same buckets — a
// `go vet` finding and a gopls one are the same color at the same rank.
func (o *offscreen) countDiag(k offscreenKind) {
	switch k {
	case offError:
		o.errors++
	case offWarn:
		o.warns++
	default:
		o.infos++
	}
}

// lspOffscreenKind maps a language-server severity onto the ranking.
// Hint folds into info the way diagSeverityColor folds it — one cell has
// no room for a distinction the user cannot see.
func lspOffscreenKind(sev int) offscreenKind {
	switch sev {
	case lsp.SeverityWarning:
		return offWarn
	case lsp.SeverityInfo, lsp.SeverityHint:
		return offInfo
	}
	return offError
}

// pluginOffscreenKind maps a plugin severity onto the same ranking.
func pluginOffscreenKind(sev plugins.Severity) offscreenKind {
	switch sev {
	case plugins.SevError:
		return offError
	case plugins.SevWarn:
		return offWarn
	}
	return offInfo
}

// -----------------------------------------------------------------------------
// Where the markers are
// -----------------------------------------------------------------------------

// overflowMarker is one drawn marker: the cell it occupies, which way it
// points, the noun its popup counts in, and what is out there.
type overflowMarker struct {
	x, y int
	down bool
	unit string // "line" for a body of text, "row" for a list
	off  offscreen
}

// overflowMarkers enumerates every marker that should be on screen right
// now — the ONE source draw, hit-testing and the popup all read, so a
// marker can never be painted where it cannot be pointed at (the btnRect
// house rule).
//
// Order matters only where two surfaces could claim one cell, which they
// cannot: each marker sits in its own panel's band.
func (a *App) overflowMarkers() []overflowMarker {
	out := make([]overflowMarker, 0, 6)

	add := func(x, y int, down bool, unit string, o offscreen) {
		if o.lines <= 0 || x < 0 || y < 0 || x >= a.width || y >= a.height {
			return
		}
		out = append(out, overflowMarker{x: x, y: y, down: down, unit: unit, off: o})
	}

	// The editor body. The marker shares the last CONTENT column, which
	// is the column Tab.Render paints code into — nothing is reserved,
	// so the glyph simply covers one cell of two rows.
	if t := a.activeTabPtr(); t != nil {
		ex, ey, ew, eh := a.editorRect()
		if ew > 0 && eh > 0 {
			above, below := a.editorOffscreen(t, t.ScrollY, t.ScrollY+eh-1)
			add(ex+ew-1, ey, false, "line", above)
			add(ex+ew-1, ey+eh-1, true, "line", below)
		}
	}

	// The git panel's two panes, which scroll independently and so each
	// carry their own pair. Both bands are the same rows (the panel's
	// body under its header rule); only the column differs.
	//
	// The DIFF pane's markers land in its blank right margin —
	// drawGitPanelDiffRow stops one column short of the panel's edge, and
	// so do the hunk chips — so there they cover nothing at all. The FILE
	// LIST has no such margin, so its pair shares the last cell of a
	// filename: the tree's trade, and the reason paths in that column are
	// already truncated with an ellipsis rather than run to the edge.
	if a.gitPanel.open {
		px, py, pw, ph := a.gitPanelRect()
		visible := ph - 1 // the header rule
		if pw > 0 && visible > 0 {
			diffTop := a.gitPanel.diffScroll
			add(px+pw-1, py+1, false, "line", offscreen{lines: diffTop})
			add(px+pw-1, py+ph-1, true, "line",
				offscreen{lines: len(a.gitPanel.diffLines) - (diffTop + visible)})

			// Counted in FILES, not rows: every row of this list is one,
			// and "9 files below" is the answer somebody about to commit
			// is actually asking for.
			if listW := a.gitPanelListW(pw); listW > 0 {
				listTop := a.gitPanel.listScroll
				add(px+listW-1, py+1, false, "file", offscreen{lines: listTop})
				add(px+listW-1, py+ph-1, true, "file",
					offscreen{lines: len(a.gitPanel.files) - (listTop + visible)})
			}
		}
	}

	// The file tree. Counted in rows, not lines: a tree is a list of
	// names, and "12 more rows" is the honest unit for it.
	if a.sidebarShown {
		sx, sy, sw, sh := a.sidebarRect()
		off, listH := a.tree.ListRows(sh)
		if sw > 0 && listH > 0 {
			top := a.tree.ScrollY
			add(sx+sw-1, sy+off, false, "row", offscreen{lines: top})
			add(sx+sw-1, sy+off+listH-1, true, "row",
				offscreen{lines: a.tree.RowCount() - (top + listH)})
		}
	}
	return out
}

// drawOverflowMarkers paints them all, keeping each cell's existing
// BACKGROUND. That last part is what lets the marker sit inside the
// editor's current-line highlight, the tree's selection bar or the git
// panel's fill without punching a hole in any of them — the glyph is an
// annotation on a row somebody else drew, so only the foreground is
// this feature's to set.
//
// Called from draw() after every panel has rendered, deliberately: the
// editor's Render is where EnsureVisible and clampScroll settle ScrollY,
// so a marker placed before it would report the previous frame's
// viewport.
func (a *App) drawOverflowMarkers() {
	for _, m := range a.overflowMarkers() {
		r := overflowUpRune
		if m.down {
			r = overflowDownRune
		}
		_, _, st, _ := a.screen.GetContent(m.x, m.y)
		_, bg, _ := st.Decompose()
		a.screen.SetContent(m.x, m.y, r, nil,
			tcell.StyleDefault.Background(bg).
				Foreground(offscreenColor(a.theme, m.off.kind())).Bold(true))
	}
}

// -----------------------------------------------------------------------------
// The popup
// -----------------------------------------------------------------------------

// overflowTipEvent is the dwell timer's tick. seq pins it to the pointer
// position that scheduled it, so a pointer that has moved on invalidates
// it without any timer having to be cancelled.
type overflowTipEvent struct {
	when time.Time
	seq  int
}

// When satisfies the tcell.Event interface.
func (e *overflowTipEvent) When() time.Time { return e.when }

// Compile-time check that overflowTipEvent really is a tcell.Event.
var _ tcell.Event = (*overflowTipEvent)(nil)

// overflowTipState is the popup's whole state. It is deliberately NOT
// folded into hoverDwellState: that one is armed only inside cats
// (Tier 1) because its answer costs a round trip to a language server
// over a link ced cannot vouch for, while this answer is a count the
// draw pass already had in hand. Free everywhere, so it runs everywhere.
type overflowTipState struct {
	seq    int
	x, y   int // the cell the pointer was last seen on
	open   bool
	lines  []string
	ax, ay int // the anchor: the marker cell this describes
	box    struct{ x, y, w, h int }
}

// noteOverflowPointer is the hook handleMouse calls for every mouse
// event, beside notePointer and for the same reason: it is about the
// POINTER rather than about what the pointer is over.
//
// It reports whether the event was consumed, which is true in exactly
// one case — a button press inside the drawn popup, which covers content
// the user cannot see and so must not fall through to it (the completion
// popup's contract). Anything with a button down dismisses first: a
// press is an action, not a rest.
func (a *App) noteOverflowPointer(x, y int, btn tcell.ButtonMask) bool {
	if btn != tcell.ButtonNone {
		hit := a.overflowTip.open &&
			btn&(tcell.Button1|tcell.Button2|tcell.Button3) != 0 &&
			a.overflowTipContains(x, y)
		a.closeOverflowTip()
		return hit
	}
	if x == a.overflowTip.x && y == a.overflowTip.y {
		return false // same cell: don't restart the clock (some hosts repeat)
	}
	if a.overflowTip.open {
		a.closeOverflowTip()
	}
	a.overflowTip.x, a.overflowTip.y = x, y
	a.armOverflowTip()
	return false
}

// armOverflowTip schedules a tick for the cell the pointer is on now,
// but only when that cell actually carries a marker — unlike the dwell
// tooltip, which has to ask a server before it knows whether there is an
// answer. Here the answer is already in hand, so a pointer crossing
// ordinary code schedules nothing at all.
func (a *App) armOverflowTip() {
	if a.screen == nil {
		return
	}
	if _, ok := a.overflowMarkerAt(a.overflowTip.x, a.overflowTip.y); !ok {
		return
	}
	a.overflowTip.seq++
	seq := a.overflowTip.seq
	scr := a.screen
	time.AfterFunc(overflowTipDelay, func() {
		// Goroutine territory: post, never mutate (the iron rule).
		_ = scr.PostEvent(&overflowTipEvent{when: time.Now(), seq: seq})
	})
}

// closeOverflowTip hides the popup and invalidates anything in flight —
// the seq bump is what makes a pending tick arrive dead. Cheap and safe
// when nothing is open, which is what lets callers fire it blind.
func (a *App) closeOverflowTip() {
	a.overflowTip.open = false
	a.overflowTip.lines = nil
	a.overflowTip.box = struct{ x, y, w, h int }{}
	a.overflowTip.seq++
}

// handleOverflowTipTick opens the popup if the tick is still current and
// the pointer is still resting on a marker. The marker is re-resolved
// rather than remembered: the editor may have scrolled under a stationary
// pointer (a wheel event is a rest, not a move), and the count is the
// whole content of the box.
func (a *App) handleOverflowTipTick(e *overflowTipEvent) {
	if e.seq != a.overflowTip.seq || a.modal != nil || a.menuOpen {
		return
	}
	m, ok := a.overflowMarkerAt(a.overflowTip.x, a.overflowTip.y)
	if !ok {
		return
	}
	a.overflowTip.open = true
	a.overflowTip.lines = overflowTipLines(m)
	a.overflowTip.ax, a.overflowTip.ay = m.x, m.y
}

// overflowMarkerAt reports the marker drawn on a cell, if any — the
// hit-test half of overflowMarkers, so the popup can only ever appear
// for a glyph that is really on screen.
func (a *App) overflowMarkerAt(x, y int) (overflowMarker, bool) {
	for _, m := range a.overflowMarkers() {
		if m.x == x && m.y == y {
			return m, true
		}
	}
	return overflowMarker{}, false
}

// overflowTipLines is the popup's text: the count on the first line,
// what is in it on the second.
//
// The count is the whole reason the popup exists. A marker says "there
// is more", which is the yes/no a viewport owes its reader; the number
// is what a scrollbar's thumb used to say with its HEIGHT, and it is
// worth a hover but not a permanent column.
func overflowTipLines(m overflowMarker) []string {
	dir := " above"
	if m.down {
		dir = " below"
	}
	lines := []string{plural(m.off.lines, m.unit, m.unit+"s") + dir}
	if d := m.off.detail(); d != "" {
		lines = append(lines, d)
	}
	return lines
}

// overflowTipVisible asks the question at DRAW time rather than trusting
// every opener to have dismissed the popup: being passive, it survives
// events it never hears about (a modal opened by a plugin, say).
func (a *App) overflowTipVisible() bool {
	return a.overflowTip.open && a.modal == nil && !a.menuOpen && len(a.overflowTip.lines) > 0
}

// overflowTipContains reports whether a cell is inside the drawn popup,
// using the rect the last draw stamped.
func (a *App) overflowTipContains(x, y int) bool {
	b := a.overflowTip.box
	return b.w > 0 && b.h > 0 && x >= b.x && x < b.x+b.w && y >= b.y && y < b.y+b.h
}

// drawOverflowTip paints the popup through the same measurer and painter
// the two hover surfaces use — one tooltip look in the editor, not
// three.
func (a *App) drawOverflowTip() {
	if !a.overflowTipVisible() {
		// Nothing painted, so nothing may be hit: a stale box would
		// swallow presses over a region that no longer has a popup in it.
		a.overflowTip.box = struct{ x, y, w, h int }{}
		return
	}
	w, h := tooltipSize(a, a.overflowTip.lines)
	mx, my, mw, mh := tooltipPlace(a, w, h, a.overflowTip.ax, a.overflowTip.ay)
	a.overflowTip.box = struct{ x, y, w, h int }{mx, my, mw, mh}
	drawTooltipBox(a, a.overflowTip.lines, nil, mx, my, mw, mh)
}
