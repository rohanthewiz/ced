// =============================================================================
// File: internal/app/hoverdwell.go
// Author: Rohan Allison <rohanthewiz@gmail.com>
// Created: 2026-08-13
// Copyright: 2026 Rohan Allison. All rights reserved.
// =============================================================================

// hoverdwell.go is hover on mouse dwell — rest the pointer on an
// identifier and the LSP's answer appears under it, with no gesture at
// all. It is the mouse half of a verb ced already had: menuHoverInfo
// (lsp.go) asks about the CARET and shows a modal, this asks about the
// POINTER and shows ambient chrome.
//
// Four properties, and each one is the answer to a way an unbidden
// tooltip goes wrong:
//
//   - PASSIVE. It never takes the modal slot. The user asked no
//     question, so nothing here may own the keyboard or block the save
//     dialog that was about to open — the argument the Problems panel
//     makes one floor down, and the reason the keyboard flavour stays a
//     modal: THAT one was asked for.
//   - TIED TO THE POINTER. It opens under the cell it describes and
//     closes the moment the pointer leaves that cell. A tooltip that
//     outlives the thing it points at is just an obstruction, and this
//     one covers the user's code.
//   - QUIET WHEN THERE IS NOTHING. A server with no answer produces
//     nothing at all — no flash, no empty box. menuHoverInfo flashes
//     "No hover info" because someone asked and deserves a reply;
//     nobody asked here.
//   - ARMED ONLY AT TIER 1 (inside cats). The plan's call, and it
//     survives contact: motion reporting is precise and local there,
//     the round trip is a unix socket, and ced knows the host is not
//     re-deriving pointer cells over a slow link. Tier 0 keeps the
//     explicit verb (≡ → LSP → Hover info, and its leader key), so the
//     capability is degraded, not missing — the ladder's own rule. If
//     this should ever light up in bare kitty/Ghostty, hoverDwellArmed
//     is the one line to widen.
//
// Redraw mechanics are the which-key overlay's, for the same reason:
// ced's loop is event-driven, so "the pointer stopped moving" is a
// one-shot time.AfterFunc that posts a hoverDwellEvent and never
// touches App state itself. A generation counter stamped into the event
// invalidates stale ticks — and the same counter rides the LSP request,
// so an answer about a cell the pointer has already left arrives dead
// rather than painting a tooltip about the wrong symbol.

package app

import (
	"time"

	"github.com/gdamore/tcell/v2"

	"github.com/rohanthewiz/ced/internal/editor"
)

// hoverDwellDelay is how long the pointer must sit still before the
// editor asks. Longer than crossing the window (a sweep across the
// screen must not leave a wake of requests behind it), short enough
// that a deliberate rest still feels like an answer rather than a
// timeout. GUI editors sit around 300ms; this adds room for a round
// trip to a language server that may be indexing.
const hoverDwellDelay = 400 * time.Millisecond

// hoverDwellEvent is the posted timer tick. seq pins it to the pointer
// position that scheduled it — see hoverDwellState.seq.
type hoverDwellEvent struct {
	when time.Time
	seq  int
}

// When satisfies the tcell.Event interface.
func (e *hoverDwellEvent) When() time.Time { return e.when }

// Compile-time check that hoverDwellEvent really is a tcell.Event.
var _ tcell.Event = (*hoverDwellEvent)(nil)

// hoverDwellState is the whole feature's state.
type hoverDwellState struct {
	// seq counts pointer rests. A scheduled tick — and the LSP request
	// it goes on to make — carries the seq current at schedule time and
	// is dropped on arrival unless it still matches. That is how "the
	// mouse moved in the meantime" cancels both a pending tooltip and an
	// in-flight request without tracking timers or cancelling calls.
	seq int
	// x, y is the cell the pointer was last seen on, armed or not. Kept
	// so a repeated motion event for the SAME cell (some hosts emit
	// them) doesn't restart the clock and make the tooltip unreachable.
	x, y int

	open bool
	// lines is the flattened hover text, already capped by hoverLines.
	lines []string
	// path pins the tooltip to the document it describes: a response for
	// a tab the user has left is not this tooltip's business, the rule
	// every other async LSP surface follows.
	path string
	// ax, ay is the anchor — the cell the request was made for. The box
	// hangs off it and the pointer leaving it closes the box, so this is
	// both geometry and lifetime.
	ax, ay int
	// box is the full rectangle stamped by the last draw, so a press
	// inside the tooltip is swallowed rather than landing in code the
	// user cannot see. One rect for draw and hit — the status bar and
	// completion popup idiom.
	box struct{ x, y, w, h int }
}

// hoverDwellArmed reports whether the dwell layer is live at all. See
// the file header for why this is Tier 1 and what widening it would
// mean.
func (a *App) hoverDwellArmed() bool { return a.catsTier1() }

// notePointer is the single hook handleMouse calls for every mouse
// event before its own routing, and it does the whole lifetime: any
// button or wheel is an ACTION rather than a rest, so it dismisses and
// disarms; pure motion to a new cell dismisses whatever was up and
// starts the clock again for the cell now under the pointer.
//
// It reports whether the event was consumed, which is true in exactly
// one case: a button press inside the tooltip's own box. The box covers
// code, so a click on it must not reach the code underneath — the same
// contract the completion popup's box has.
func (a *App) notePointer(x, y int, btn tcell.ButtonMask) bool {
	if btn != tcell.ButtonNone {
		hit := a.hoverDwell.open &&
			btn&(tcell.Button1|tcell.Button2|tcell.Button3) != 0 &&
			a.hoverDwellContains(x, y)
		a.closeHoverDwell()
		return hit
	}
	if a.hoverDwell.open && (x != a.hoverDwell.ax || y != a.hoverDwell.ay) {
		// The pointer left the cell this tooltip is about. Note it is the
		// ANCHOR that is compared, not the last-seen cell: the tooltip's
		// claim is "this is what is under the pointer", and it stops
		// being true the instant the pointer is somewhere else.
		a.closeHoverDwell()
	}
	if x == a.hoverDwell.x && y == a.hoverDwell.y {
		return false
	}
	a.hoverDwell.x, a.hoverDwell.y = x, y
	a.armHoverDwell()
	return false
}

// armHoverDwell schedules a dwell tick for the cell the pointer is on
// now. Every motion event schedules one, which is a timer per cell
// crossed during a sweep — cheap (one-shot, 400ms, no goroutine of its
// own until it fires) and the same trade armWhichKey makes on every
// leader press. Only the newest survives the seq check.
func (a *App) armHoverDwell() {
	if !a.hoverDwellArmed() || a.screen == nil {
		return
	}
	a.hoverDwell.seq++
	seq := a.hoverDwell.seq
	scr := a.screen
	time.AfterFunc(hoverDwellDelay, func() {
		// Goroutine territory: post, never mutate (the iron rule).
		_ = scr.PostEvent(&hoverDwellEvent{when: time.Now(), seq: seq})
	})
}

// closeHoverDwell hides the tooltip and invalidates anything in flight —
// a pending tick AND a request already sent, since both carry the seq
// this bumps. Safe and cheap when nothing is open, which is what lets
// callers (any keystroke, closeAllModals) fire it unconditionally.
func (a *App) closeHoverDwell() {
	a.hoverDwell.open = false
	a.hoverDwell.lines = nil
	a.hoverDwell.path = ""
	a.hoverDwell.box = struct{ x, y, w, h int }{}
	a.hoverDwell.seq++
}

// handleHoverDwellTick fires the request when the tick that scheduled it
// is still the current one and the pointer is resting somewhere worth
// asking about.
func (a *App) handleHoverDwellTick(e *hoverDwellEvent) {
	if e.seq != a.hoverDwell.seq || !a.hoverDwellEligible() {
		return
	}
	t := a.activeTabPtr()
	pos, ok := a.hoverDwellPos(t, a.hoverDwell.x, a.hoverDwell.y)
	if !ok {
		return
	}

	client := a.lsp.client
	scr := a.screen
	path := t.Path
	lpos := lspPosFor(t, pos)
	seq := a.hoverDwell.seq
	ax, ay := a.hoverDwell.x, a.hoverDwell.y
	a.lspFlushChange(t) // the answer must describe what is on screen
	go func() {
		h, err := client.HoverAt(path, lpos)
		text := ""
		if h != nil {
			text = h.HoverText()
		}
		_ = scr.PostEvent(&lspHoverEvent{
			when: time.Now(), path: path, text: text, err: err,
			dwell: true, seq: seq, ax: ax, ay: ay,
		})
	}()
}

// hoverDwellEligible reports whether a tooltip may be asked for right
// now. The surfaces listed here don't merely look wrong underneath one —
// each of them means the user is doing something deliberate, and a
// tooltip that appears mid-gesture is an interruption, not a hint.
func (a *App) hoverDwellEligible() bool {
	if !a.hoverDwellArmed() || a.screen == nil {
		return false
	}
	if a.modal != nil || a.menuOpen || a.findOpen {
		return false
	}
	// The completion popup and the which-key band are both anchored INTO
	// the editor body and both mean "mid-keystroke"; the drag means the
	// mouse is working, not resting (its button is down, so this is belt
	// and braces over notePointer's first branch).
	if a.completion.open || a.whichKey.open || a.dragMode != "" {
		return false
	}
	return a.hasLSPActions()
}

// hoverDwellPos converts a screen cell to the buffer position the
// tooltip would describe, refusing everything that isn't an identifier
// rune the pointer is actually ON.
//
// The refusals matter more than the conversion. Tab.HitTest answers
// "which column is NEAREST", which is exactly right for a click — a
// click ten cells past the end of a line means "put the caret at the
// end" — and exactly wrong for a pointer, which is over whitespace and
// means nothing. Same for the gutter, where HitTest reports column 0 and
// would have the editor explain the first token of every line the
// pointer crosses on its way to the window's edge.
//
// The "actually on it" test round-trips through the editor's own
// PosScreenCell rather than re-deriving the gutter offset and tab-stop
// arithmetic here: the position is accepted only if it maps back to a
// cell range containing the pointer. Asking the renderer where it PUT a
// column is the only way to stay honest about tabs and wide runes.
func (a *App) hoverDwellPos(t *editor.Tab, x, y int) (editor.Position, bool) {
	if t == nil || t.IsImage() {
		return editor.Position{}, false
	}
	ex, ey, ew, eh := a.editorRect()
	if x < ex || x >= ex+ew || y < ey || y >= ey+eh {
		return editor.Position{}, false
	}
	lx, ly := x-ex, y-ey
	pos, ok := t.HitTest(lx, ly, ew, eh)
	if !ok {
		return editor.Position{}, false
	}
	runes := t.Buffer.LineRunes(pos.Line)
	if pos.Col >= len(runes) || !editor.IsWordRune(runes[pos.Col]) {
		return editor.Position{}, false
	}
	dx, dy, ok := t.PosScreenCell(pos, ew, eh)
	if !ok || dy != ly {
		return editor.Position{}, false
	}
	// Width of the rune's cell footprint, taken from where the renderer
	// puts the NEXT column. A wide glyph is two cells and the pointer may
	// rest on either of them.
	w := 1
	if nx, _, ok := t.PosScreenCell(editor.Position{Line: pos.Line, Col: pos.Col + 1}, ew, eh); ok && nx > dx {
		w = nx - dx
	}
	if lx < dx || lx >= dx+w {
		return editor.Position{}, false
	}
	return pos, true
}

// handleHoverDwellResult lands a dwell hover response. Everything about
// it is a reason to say nothing: a superseded request, a tab the user
// has left, an error, an empty answer. Only a live request with text
// paints — see the file header on why silence is the right failure here.
func (a *App) handleHoverDwellResult(e *lspHoverEvent) {
	if e.seq != a.hoverDwell.seq {
		return
	}
	t := a.activeTabPtr()
	if t == nil || t.Path != e.path || e.err != nil {
		return
	}
	lines := hoverLines(e.text)
	if len(lines) == 0 {
		return
	}
	a.hoverDwell.open = true
	a.hoverDwell.lines = lines
	a.hoverDwell.path = e.path
	a.hoverDwell.ax, a.hoverDwell.ay = e.ax, e.ay
}

// hoverDwellVisible reports whether the tooltip should be on screen.
// Being passive, it survives events it never hears about (a modal
// opening from a plugin, say), so visibility asks the question at draw
// time rather than trusting every opener to have dismissed it.
func (a *App) hoverDwellVisible() bool {
	return a.hoverDwell.open && a.modal == nil && !a.menuOpen && len(a.hoverDwell.lines) > 0
}

// hoverDwellContains reports whether a screen cell is inside the drawn
// tooltip, using the rect the last draw stamped.
func (a *App) hoverDwellContains(x, y int) bool {
	b := a.hoverDwell.box
	return b.w > 0 && b.h > 0 && x >= b.x && x < b.x+b.w && y >= b.y && y < b.y+b.h
}

// drawHoverDwell paints the tooltip anchored to the cell it describes,
// through the same measurer and painter the modal flavour uses.
func (a *App) drawHoverDwell() {
	if !a.hoverDwellVisible() {
		// Nothing painted, so nothing may be hit: a box left over from
		// the last draw would swallow presses over a region that no
		// longer has a tooltip in it.
		a.hoverDwell.box = struct{ x, y, w, h int }{}
		return
	}
	w, h := tooltipSize(a, a.hoverDwell.lines)
	mx, my, mw, mh := tooltipPlace(a, w, h, a.hoverDwell.ax, a.hoverDwell.ay)
	a.hoverDwell.box = struct{ x, y, w, h int }{mx, my, mw, mh}
	drawTooltipBox(a, a.hoverDwell.lines, nil, mx, my, mw, mh)
}
