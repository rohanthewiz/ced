// =============================================================================
// File: internal/app/diagtip.go
// Author: Rohan Allison <rohanthewiz@gmail.com>
// Created: 2026-09-21
// Copyright: 2026 Rohan Allison. All rights reserved.
// =============================================================================

// diagtip.go puts a diagnostic's MESSAGE where its mark is. The gutter
// dot and the underline say "this is broken" and never say WHAT is
// broken; until now that answer lived only in the Problems panel, which
// is a worklist for the whole project rather than an answer about the
// line you are looking at. Three doors, one text:
//
//   - REST THE POINTER on the gutter of a diagnosed line, or on the
//     underlined span itself, and a tooltip lists the messages.
//   - CLICK the gutter of a diagnosed line and the same tooltip opens at
//     once; click it again and it closes. This is the mouse path for a
//     terminal that reports presses but no MOTION (macOS Terminal.app),
//     where the dwell above can never fire. Gutter only: a click in the
//     code is the caret's, and always will be.
//   - Esc-i (hover info) leads with the diagnostics under the CARET, and
//     still answers when the server has no hover text for that spot —
//     the keyboard path, for a terminal that reports no motion.
//   - Next / previous problem flash the message of the one they land on.
//
// The pointer tooltip is the overflow popup's shape, not the LSP dwell
// tooltip's, and for the overflow popup's reason: the answer is already
// in hand (App.lsp.diags is a cache the gutter paints from), so there is
// no round trip to protect and it runs on EVERY host rather than only
// inside cats. It is also why nothing is scheduled over ordinary code —
// armDiagTip asks the cache first and arms only when there is something
// to say.
//
// Passive, like every tooltip here: it never takes the modal slot, any
// button or keystroke dismisses it, and a press inside its box is
// swallowed because the box covers code the user cannot see.
//
//	  12 ● │ x := doThing(y)
//	       │      ~~~~~~~        ← pointer rests here, or on the ●
//	       │ ┌───────────────────────────────┐
//	       │ │ ✗ undefined: doThing (compiler)│
//	       │ └───────────────────────────────┘

package app

import (
	"sort"
	"strings"
	"time"

	"github.com/gdamore/tcell/v2"

	"github.com/rohanthewiz/ced/internal/editor"
	"github.com/rohanthewiz/ced/internal/lsp"
)

const (
	// diagTipDelay matches the overflow popup's: long enough that a
	// pointer sweeping down the gutter does not flash a box on every red
	// line it crosses, short enough to read as an answer.
	diagTipDelay = 250 * time.Millisecond

	// diagTipMaxLines caps the tooltip. A line with a dozen diagnostics
	// (a cascade from one missing import) would otherwise cover the
	// screen; the Problems panel is where the full list lives.
	diagTipMaxLines = 12
)

// diagTipEvent is the dwell timer's tick; seq pins it to the pointer
// position that scheduled it.
type diagTipEvent struct {
	when time.Time
	seq  int
}

// When satisfies the tcell.Event interface.
func (e *diagTipEvent) When() time.Time { return e.when }

// Compile-time check that diagTipEvent really is a tcell.Event.
var _ tcell.Event = (*diagTipEvent)(nil)

// diagTipState is the pointer tooltip's whole state. Kept apart from
// hoverDwellState for the reason overflowTipState is: that layer is
// Tier-1-only and asks a server, this one reads a cache.
type diagTipState struct {
	seq    int
	x, y   int // the cell the pointer was last seen on
	open   bool
	lines  []string
	ax, ay int // the anchor cell the tooltip describes
	box    struct{ x, y, w, h int }
	// pressClosed records that the press being dispatched RIGHT NOW just
	// dismissed a tip anchored on the very cell it landed on. The gutter
	// click reads it to make a second click a close rather than a
	// close-and-reopen; noteDiagPointer rewrites it on every press, so it
	// never outlives the event that set it.
	pressClosed bool
}

// noteDiagPointer is handleMouse's per-event hook, beside notePointer
// and noteOverflowPointer. It reports whether the event was consumed,
// which is true only for a press inside the drawn box.
func (a *App) noteDiagPointer(x, y int, btn tcell.ButtonMask) bool {
	if btn != tcell.ButtonNone {
		hit := a.diagTip.open &&
			btn&(tcell.Button1|tcell.Button2|tcell.Button3) != 0 &&
			a.diagTipContains(x, y)
		a.diagTip.pressClosed = a.diagTip.open && x == a.diagTip.ax && y == a.diagTip.ay
		a.closeDiagTip()
		return hit
	}
	if x == a.diagTip.x && y == a.diagTip.y {
		return false // same cell: some hosts repeat motion reports
	}
	if a.diagTip.open {
		a.closeDiagTip()
	}
	a.diagTip.x, a.diagTip.y = x, y
	a.armDiagTip()
	return false
}

// armDiagTip schedules a tick only when the cell under the pointer
// actually carries a diagnostic, so crossing clean code costs nothing.
func (a *App) armDiagTip() {
	if a.screen == nil {
		return
	}
	if len(a.diagsAtCell(a.diagTip.x, a.diagTip.y)) == 0 {
		return
	}
	a.diagTip.seq++
	seq := a.diagTip.seq
	scr := a.screen
	time.AfterFunc(diagTipDelay, func() {
		// Goroutine territory: post, never mutate.
		_ = scr.PostEvent(&diagTipEvent{when: time.Now(), seq: seq})
	})
}

// closeDiagTip hides the tooltip and invalidates a pending tick. Cheap
// and safe when nothing is open, so callers fire it blind.
func (a *App) closeDiagTip() {
	a.diagTip.open = false
	a.diagTip.lines = nil
	a.diagTip.box = struct{ x, y, w, h int }{}
	a.diagTip.seq++
}

// handleDiagTipTick opens the tooltip if the tick is current and the
// pointer still rests on a diagnosed cell. The diagnostics are looked up
// AGAIN rather than remembered: the server may have republished (the fix
// just landed) or the view scrolled under a stationary pointer since the
// tick was armed.
func (a *App) handleDiagTipTick(e *diagTipEvent) {
	if e.seq != a.diagTip.seq || a.modal != nil || a.menuOpen {
		return
	}
	if a.completion.open || a.whichKey.open || a.dragMode != "" {
		return
	}
	a.openDiagTipAt(a.diagTip.x, a.diagTip.y)
}

// openDiagTipAt shows the tooltip for a screen cell, reporting whether
// the cell had anything to say. The one opener the dwell tick and the
// gutter click share, so the two doors cannot show different text.
func (a *App) openDiagTipAt(x, y int) bool {
	diags := a.diagsAtCell(x, y)
	if len(diags) == 0 {
		return false
	}
	a.diagTip.open = true
	a.diagTip.lines = diagTipLines(diags)
	a.diagTip.ax, a.diagTip.ay = x, y
	// The LSP dwell tooltip may be about to answer the same cell with the
	// symbol's docs; two boxes stacked over one identifier is noise, and
	// "this is broken" is the more urgent of the two things to say.
	a.closeHoverDwell()
	return true
}

// diagGutterPress is the click door: a press in the gutter of a diagnosed
// line opens the tooltip immediately instead of parking the caret at
// column 0. It reports whether the press was claimed; a claimed press
// moves no caret and starts no drag (the blame column's rule — the
// gesture was aimed at the margin, and a wiggle afterwards must not
// select the code beside it). A clean line's gutter is NOT claimed, so
// clicking a line number still places the caret there as it always has.
//
// It runs after noteDiagPointer, which has already dismissed any open
// tip on this same press — hence pressClosed, the toggle's memory.
func (a *App) diagGutterPress(x, y int) bool {
	if !a.diagCellInGutter(x, y) || len(a.diagsAtCell(x, y)) == 0 {
		return false
	}
	if a.diagTip.pressClosed {
		return true // second click on the same dot: it closed, leave it closed
	}
	// Stamp the pointer cell too: the button RELEASE arrives next as a
	// motion report on this cell, and noteDiagPointer treats a report on
	// the remembered cell as a repeat rather than as travel that should
	// close the tip it just opened.
	a.diagTip.x, a.diagTip.y = x, y
	return a.openDiagTipAt(x, y)
}

// diagCellInGutter reports whether a screen cell is in the active tab's
// gutter band — line numbers, annotation column and mark cell — using
// the same boundary diagsAtCell answers "by line" inside of.
func (a *App) diagCellInGutter(x, y int) bool {
	t := a.activeTabPtr()
	if t == nil || t.IsImage() || t.IsMarkdownView() {
		return false
	}
	ex, ey, ew, eh := a.editorRect()
	if x < ex || x >= ex+ew || y < ey || y >= ey+eh {
		return false
	}
	_, annEnd := t.AnnotationCols()
	return x-ex < annEnd+1
}

// diagsAtCell resolves a screen cell to the diagnostics it stands for:
// every diagnostic STARTING on that line when the cell is in the gutter
// (where the dot is drawn — the dot marks a diagnostic's first line), or
// every diagnostic whose range covers the rune under the pointer when the
// cell is in the code.
//
// "Under the pointer" is checked by round-tripping through the renderer
// (PosScreenCell), hoverdwell's rule: HitTest answers NEAREST column,
// which past the end of a line is the end of the line, and a tooltip
// about an underline twenty cells to the left of the pointer is wrong.
func (a *App) diagsAtCell(x, y int) []lsp.Diagnostic {
	t := a.activeTabPtr()
	if t == nil || t.IsImage() || t.Path == "" || t.IsMarkdownView() {
		return nil
	}
	// diagsFor, not a.lsp.diags: a plugin's finding and one of ced's own
	// are underlined in this pane exactly like a server's, so hovering
	// one must answer. See diagmerge.go.
	all := a.diagsFor(t.Path)
	if len(all) == 0 {
		return nil
	}
	ex, ey, ew, eh := a.editorRect()
	if x < ex || x >= ex+ew || y < ey || y >= ey+eh {
		return nil
	}
	lx, ly := x-ex, y-ey
	pos, ok := t.HitTest(lx, ly, ew, eh)
	if !ok || pos.Line >= t.Buffer.LineCount() {
		return nil
	}

	// The code starts one cell past the gutter and annotation columns
	// (Tab.gutterCols()+1, spelled through the exported AnnotationCols).
	_, annEnd := t.AnnotationCols()
	if lx < annEnd+1 {
		var out []lsp.Diagnostic
		for _, d := range all {
			if d.Range.Start.Line == pos.Line {
				out = append(out, d)
			}
		}
		return out
	}

	// Code cell: the pointer must really be ON the rune HitTest named.
	if pos.Col >= len(t.Buffer.LineRunes(pos.Line)) {
		return nil
	}
	dx, dy, ok := t.PosScreenCell(pos, ew, eh)
	if !ok || dy != ly {
		return nil
	}
	w := 1
	if nx, _, ok := t.PosScreenCell(editor.Position{Line: pos.Line, Col: pos.Col + 1}, ew, eh); ok && nx > dx {
		w = nx - dx // a tab or wide rune spans several cells
	}
	if lx < dx || lx >= dx+w {
		return nil
	}
	return diagsCovering(t, all, pos)
}

// diagsCovering returns the diagnostics whose range contains pos, using
// the same stretch the underline uses: a zero-width range is painted as
// one cell, so it must be hoverable as one cell too.
func diagsCovering(t *editor.Tab, all []lsp.Diagnostic, pos editor.Position) []lsp.Diagnostic {
	var out []lsp.Diagnostic
	for _, d := range all {
		if d.Range.Start.Line >= t.Buffer.LineCount() {
			continue // stale: the line it named has since been deleted
		}
		start := editorPosFor(t, d.Range.Start)
		end := editorPosFor(t, d.Range.End)
		if start == end {
			end.Col++
		}
		if !posLess(pos, start) && posLess(pos, end) {
			out = append(out, d)
		}
	}
	return out
}

// posLess orders two buffer positions in document order.
func posLess(a, b editor.Position) bool {
	if a.Line != b.Line {
		return a.Line < b.Line
	}
	return a.Col < b.Col
}

// diagTipLines renders diagnostics as tooltip rows: worst severity
// first, each message word-wrapped under a severity glyph (the status
// bar's and the Problems panel's ✗ ⚠ ℹ, so all three surfaces speak one
// vocabulary) with the source named at the end, since "which tool
// said this" is often the first question about a warning.
//
// Messages are WRAPPED, not truncated: the tooltip exists to show the
// text the gutter could not, and drawTooltipBox's ellipsis would cut the
// clause that says what to do. Capped, with the cut marked.
func diagTipLines(diags []lsp.Diagnostic) []string {
	ds := append([]lsp.Diagnostic(nil), diags...)
	sort.SliceStable(ds, func(i, j int) bool {
		si, sj := problemSeverity(ds[i].Severity), problemSeverity(ds[j].Severity)
		if si != sj {
			return si < sj // LSP severities: 1 = error is the worst
		}
		if ds[i].Range.Start.Line != ds[j].Range.Start.Line {
			return ds[i].Range.Start.Line < ds[j].Range.Start.Line
		}
		return ds[i].Range.Start.Character < ds[j].Range.Start.Character
	})

	// Border + padding take 4 cells, the glyph and its space two more.
	textW := hoverModalMaxWidth - 4 - 2
	var out []string
	for _, d := range ds {
		msg := flattenProblemMsg(d.Message)
		if d.Source != "" {
			msg += " (" + d.Source + ")"
		}
		glyph := string(problemGlyph(problemSeverity(d.Severity)))
		for i, row := range wrapChatText(msg, textW) {
			if i == 0 {
				out = append(out, glyph+" "+row)
			} else {
				out = append(out, "  "+row)
			}
		}
	}
	if len(out) > diagTipMaxLines {
		out = append(out[:diagTipMaxLines-1], "… (see the Problems panel)")
	}
	return out
}

// diagLinesAtCaret is the keyboard door: the tooltip rows for whatever
// diagnostics cover the caret, or — when the caret is on a diagnosed
// line but not inside any range (it usually sits at a line's start after
// a Problems jump or a gutter click) — every diagnostic on that line.
// Asking "what's wrong here?" from anywhere on a red line must answer.
func (a *App) diagLinesAtCaret() []string {
	t := a.activeTabPtr()
	if t == nil || t.Path == "" {
		return nil
	}
	all := a.diagsFor(t.Path)
	if len(all) == 0 {
		return nil
	}
	ds := diagsCovering(t, all, t.Cursor)
	if len(ds) == 0 {
		for _, d := range all {
			if d.Range.Start.Line == t.Cursor.Line {
				ds = append(ds, d)
			}
		}
	}
	if len(ds) == 0 {
		return nil
	}
	return diagTipLines(ds)
}

// diagTipVisible asks at DRAW time, like every passive layer: a modal
// opened by something this file never hears about still suppresses it.
func (a *App) diagTipVisible() bool {
	return a.diagTip.open && a.modal == nil && !a.menuOpen && len(a.diagTip.lines) > 0
}

// diagTipContains reports whether a cell is inside the drawn tooltip,
// using the rect the last draw stamped.
func (a *App) diagTipContains(x, y int) bool {
	b := a.diagTip.box
	return b.w > 0 && b.h > 0 && x >= b.x && x < b.x+b.w && y >= b.y && y < b.y+b.h
}

// drawDiagTip paints the tooltip through the shared tooltip measurer
// and painter — one tooltip look in the editor. Always called, because
// the call is also what clears the stamped rect when nothing is shown.
func (a *App) drawDiagTip() {
	if !a.diagTipVisible() {
		a.diagTip.box = struct{ x, y, w, h int }{}
		return
	}
	w, h := tooltipSize(a, a.diagTip.lines)
	mx, my, mw, mh := tooltipPlace(a, w, h, a.diagTip.ax, a.diagTip.ay)
	a.diagTip.box = struct{ x, y, w, h int }{mx, my, mw, mh}
	drawTooltipBox(a, a.diagTip.lines, nil, mx, my, mw, mh)
}

// diagFlashText is the one-line form for the flash bar: the problem's
// glyph and message. Used by the next/previous-problem verbs, which move
// the caret onto a problem and should say what it is — a jump that lands
// on a red dot and says nothing leaves the user one more gesture away
// from the answer.
func diagFlashText(r problemRow) string {
	return string(problemGlyph(r.sev)) + " " + strings.TrimSpace(r.msg)
}
