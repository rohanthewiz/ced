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
//   - Only wheel and motion defer. Every other event — keys, presses,
//     releases, pastes, timers, background results — paints as before.
//     Those are events a user expects to SEE the effect of one by one, and
//     none of them arrives in the volumes that caused the backlog.
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
// skipped: it was a flood-kind mouse event, more input is already queued
// behind it, and the screen was painted recently enough. now is passed in
// so tests can pin the clock.
func (a *App) deferFrame(ev tcell.Event, now time.Time) bool {
	if a.screen == nil || !isBurstMouseEvent(ev) {
		return false
	}
	if !a.screen.HasPendingEvent() {
		return false
	}
	return now.Sub(a.lastFrameAt) < burstFrameMax
}
