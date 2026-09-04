// =============================================================================
// File: internal/app/ttydrain.go
// Author: Rohan Allison <rohanthewiz@gmail.com>
// Created: 2026-09-04
// Copyright: 2026 Rohan Allison. All rights reserved.
// =============================================================================

// Draining the terminal's input on the way out.
//
// ced enables full mouse tracking at startup — buttons, drags AND motion
// (see New's EnableMouse call, which is DEC mode 1003) — so the terminal
// reports every pointer movement as an SGR sequence on ced's stdin. That
// is what makes the editor mouse-first, and it costs one thing at exit:
// the reports the terminal has already written are sitting in the tty's
// input buffer, and whoever reads that buffer next gets them.
//
// The common case is the exit gesture itself. A menu row fires on the
// BUTTON PRESS, so clicking ≡ → Quit tears the editor down while the
// button is still down; the release the terminal emits a moment later —
// `ESC [ < 0 ; col ; row m` — has no reader left in ced. The shell that
// gets the pane back swallows the `ESC [ <` as an unbound key sequence
// and echoes the remainder at its prompt, which is where the stray
// `0;163;54m` a user sees after quitting comes from.
//
// tcell cannot close this itself: Fini writes the mouse-off sequences
// LAST and returns immediately, so everything already in flight is
// stranded by construction. The fix is to stop asking for reports and
// then keep reading for a moment before the screen is finalized, which
// is exactly the window in which the release arrives:
//
//	 press ──> ced quits ──> DisableMouse ──> [drain window] ──> Fini
//	                              ^                  ^
//	                   terminal stops reporting   release consumed here
//
// Draining BEFORE Fini rather than reading the raw fd afterwards is what
// keeps this small: the screen is still in raw mode with tcell's input
// goroutine running, so a partial escape sequence is reassembled by the
// parser that already knows how, and events are discarded whole rather
// than a byte count being guessed at.

package app

import (
	"time"

	"github.com/gdamore/tcell/v2"
)

// ttyDrainWindow is the baseline time Close keeps consuming terminal
// input after reporting is switched off. It is a package var ONLY so
// tests can collapse it (the SyntaxSettle precedent) — it is not a user
// setting.
//
// It is sized against what can still be IN FLIGHT rather than against
// anything a user does: the terminal stops reporting as soon as it reads
// the disable, so the only stranded bytes are the ones it wrote before
// that read — one link latency's worth. 50ms covers a laggy SSH hop and
// is not perceptible on a path that is handing the terminal back anyway.
var ttyDrainWindow = 50 * time.Millisecond

// ttyDrainReleaseWindow is the longer budget used when a mouse button
// was still DOWN as the editor quit — the ≡ → Quit click, which a menu
// row fires on the press. There the interesting byte has not been
// written yet: it appears when the user's finger lifts, which is tens to
// a couple of hundred milliseconds later.
//
// The budget is only ever a CEILING. Draining stops the moment the
// release is seen, so the common click pays its own duration and nothing
// more; the full window is spent only when no release ever arrives,
// which is the case where the terminal processed the disable first and
// there was never anything to strand.
var ttyDrainReleaseWindow = 150 * time.Millisecond

// ttyDrainPoll is the idle interval between checks inside the window.
// Short enough that a window is honored to roughly a millisecond, long
// enough that draining is not a spin loop.
var ttyDrainPoll = 2 * time.Millisecond

// drainPendingInput switches off every reporting mode ced turned on and
// then discards whatever the terminal has already said, so no leftover
// escape sequence reaches the shell after ced exits.
//
// The three Disable calls mirror New's three Enable calls one for one.
// Mouse reporting is the one that actually leaks (motion and release
// reports are generated without the user typing anything), but paste and
// focus reporting are switched off here for the same reason and in the
// same breath: a bracketed-paste wrapper or a focus report arriving into
// a shell prompt is the same class of garbage, and leaving them on until
// Fini would mean two shutdown paths where one will do.
//
// PollEvent is only ever called behind HasPendingEvent, so this can not
// block: Run has returned by now, nothing else is polling, and an empty
// queue leaves the loop sleeping rather than waiting on a read.
func (a *App) drainPendingInput() {
	if a.screen == nil {
		return
	}
	a.screen.DisableMouse()
	a.screen.DisablePaste()
	a.screen.DisableFocus()

	// A button still held means the release is the byte we are here for,
	// and it has not been written yet — wait for it, but no longer than
	// it takes to arrive.
	awaitRelease := a.mouseHeld
	deadline := time.Now().Add(ttyDrainWindow)
	if awaitRelease {
		deadline = time.Now().Add(ttyDrainReleaseWindow)
	}

	for time.Now().Before(deadline) {
		if !a.screen.HasPendingEvent() {
			time.Sleep(ttyDrainPoll)
			continue
		}
		// Discarded deliberately: the editor is gone, so an event here
		// is either the tail of the gesture that quit it or noise the
		// shell must not inherit.
		ev := a.screen.PollEvent()
		if !awaitRelease {
			continue
		}
		if me, ok := ev.(*tcell.EventMouse); ok && !mouseButtonHeld(me.Buttons()) {
			// The release landed. Fall back to the baseline window so
			// anything queued behind it is still swallowed, without
			// holding the exit open for the rest of the ceiling.
			awaitRelease = false
			if short := time.Now().Add(ttyDrainWindow); short.Before(deadline) {
				deadline = short
			}
		}
	}
}

// mouseButtonHeld reports whether a mouse event carries a button that is
// physically down. Wheel "buttons" are deliberately excluded: a wheel
// event is an instantaneous notch with no release to wait for, and the
// three-button test matches how handleMouse already reads presses.
func mouseButtonHeld(btn tcell.ButtonMask) bool {
	return btn&(tcell.Button1|tcell.Button2|tcell.Button3) != 0
}
