// =============================================================================
// File: internal/app/inputburst.go
// Author: Rohan Allison <rohanthewiz@gmail.com>
// Created: 2026-09-22
// Copyright: 2026 Rohan Allison. All rights reserved.
// =============================================================================

// Painting once per input BURST instead of once per event.
//
// Run's loop is PollEvent → handle → draw → Show, and a frame costs about
// a millisecond on an ordinary file. That is invisible for keystrokes and
// ruinous for the mouse. A free-spinning wheel (a Logitech MX Master is
// the one that found this) emits notches far faster than one per
// millisecond and KEEPS emitting them after the view has clamped at the
// end of the file — each one a no-op scroll that still paid for a full
// frame. A few thousand of those queued seconds of painting behind them,
// which reads as the editor freezing at the bottom of a long file and
// then, much later, thawing by itself. Motion reports (DEC mode 1003 is
// on — see ttydrain.go) flood the same way while the pointer sweeps.
//
// So after a wheel or pure-motion event, the frame is SKIPPED while more
// input is already queued, and the burst is painted once, when it drains:
//
//	wheel wheel wheel … wheel │ (queue empty)
//	handle handle handle …    │ handle → draw → Show
//
// Three details make that safe:
//
//   - A frame is deferred only when another event is ALREADY pending, so
//     the loop can never block in PollEvent over an unpainted screen: the
//     event it is waiting for is in the queue, and whichever event turns
//     out to be the last one paints.
//   - The deferral is capped at burstFrameMax. A sustained spin never
//     lets the queue go empty, and without the cap the file would scroll
//     invisibly and then jump — the cap keeps it visibly moving at ~30fps
//     while still collapsing the thousands of frames in between.
//   - Only wheel and motion defer this way. Every other event — keys,
//     presses, releases, timers, background results — paints as before.
//     Those are events a user expects to SEE the effect of one by one, and
//     none of them arrives in the volumes that caused the backlog. (The
//     one exception, keys INSIDE a bracketed paste, is below.)
//
// A BRACKETED PASTE is the other flood, and it needs no pending-input or
// time test at all. Between the start and end markers every rune arrives
// as its own EventKey, and handleKey only appends it to pasteBuf
// (textpaste.go) — nothing on screen changes until the end marker splices
// the whole paste in. Painting each of those frames was pure waste: a
// 125-line paste (5250 runes) measured 9s at 200×60, 1.7ms a rune, every
// frame identical, and the backlog it built is what pins tcell's event
// queue (N-124, N-125). So a KEY handled while `pasting` is set never
// paints; the end marker is an EventPaste, not a key, so it paints the
// result. Anything else that lands mid-paste (a timer, a background
// result) is not a key and still paints as usual.
//
// Deferring leaves draw's per-frame re-syncs (tab rects, tree focus, the
// wrap width) at their last-painted values for the rest of the burst.
// That is the right answer rather than a compromise: a click landing
// mid-burst is hit-tested against the frame that is actually on screen.

package app

import (
	"time"

	"github.com/gdamore/tcell/v2"
)

// burstFrameMax is the longest the screen may go unpainted while a
// wheel/motion burst is being absorbed. ~30fps: smooth enough to watch a
// file scroll, sparse enough that a flood costs a few dozen frames a
// second instead of one per notch.
const burstFrameMax = 33 * time.Millisecond

// isBurstMouseEvent reports whether ev is one of the event kinds that
// arrive in floods: a wheel notch (any axis) or a pointer move with no
// button down. A drag (motion WITH a button) is deliberately excluded —
// it is a gesture the user is steering by what they see, so every step
// of it paints.
func isBurstMouseEvent(ev tcell.Event) bool {
	me, ok := ev.(*tcell.EventMouse)
	if !ok {
		return false
	}
	btn := me.Buttons()
	const wheels = tcell.WheelUp | tcell.WheelDown | tcell.WheelLeft | tcell.WheelRight
	if btn&wheels != 0 {
		return true
	}
	return btn == tcell.ButtonNone
}

// deferFrame reports whether the frame for the event just handled may be
// skipped: it was a key accumulated into a bracketed paste, or it was a
// flood-kind mouse event with more input already queued behind it and the
// screen painted recently enough. now is passed in so tests can pin the
// clock.
func (a *App) deferFrame(ev tcell.Event, now time.Time) bool {
	if a.screen == nil {
		return false
	}
	// Paste content: accumulated, not drawn — see the header. Checked
	// AFTER handleEvent ran, so the end marker (which clears `pasting`)
	// and the start marker (an EventPaste) both paint.
	if _, isKey := ev.(*tcell.EventKey); isKey && a.pasting {
		return true
	}
	if !isBurstMouseEvent(ev) {
		return false
	}
	if !a.screen.HasPendingEvent() {
		return false
	}
	return now.Sub(a.lastFrameAt) < burstFrameMax
}
