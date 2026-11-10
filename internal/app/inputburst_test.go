// =============================================================================
// File: internal/app/inputburst_test.go
// Author: Rohan Allison <rohanthewiz@gmail.com>
// Created: 2026-09-22
// Copyright: 2026 Rohan Allison. All rights reserved.
// =============================================================================

package app

import (
	"strings"
	"testing"
	"time"

	"github.com/gdamore/tcell/v2"
)

// TestIsBurstMouseEvent_WheelAndMotionOnly pins which events may defer a
// frame: wheel notches on either axis and buttonless motion flood; a
// press, a drag and a key are gestures whose every step must paint.
func TestIsBurstMouseEvent_WheelAndMotionOnly(t *testing.T) {
	cases := []struct {
		name string
		ev   tcell.Event
		want bool
	}{
		{"wheel down", tcell.NewEventMouse(5, 5, tcell.WheelDown, 0), true},
		{"wheel up", tcell.NewEventMouse(5, 5, tcell.WheelUp, 0), true},
		{"wheel right", tcell.NewEventMouse(5, 5, tcell.WheelRight, 0), true},
		{"motion", tcell.NewEventMouse(5, 5, tcell.ButtonNone, 0), true},
		{"press", tcell.NewEventMouse(5, 5, tcell.Button1, 0), false},
		{"right press", tcell.NewEventMouse(5, 5, tcell.ButtonSecondary, 0), false},
		{"key", tcell.NewEventKey(tcell.KeyRune, 'x', 0), false},
	}
	for _, c := range cases {
		if got := isBurstMouseEvent(c.ev); got != c.want {
			t.Errorf("%s: got %v, want %v", c.name, got, c.want)
		}
	}
}

// TestDeferFrame_OnlyWhileMoreInputIsQueued is the no-unpainted-screen
// guarantee: with nothing queued behind it, even a wheel event paints,
// so Run can never block in PollEvent over a stale frame.
func TestDeferFrame_OnlyWhileMoreInputIsQueued(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	now := time.Now()
	a.lastFrameAt = now
	wheel := tcell.NewEventMouse(5, 5, tcell.WheelDown, 0)

	if a.deferFrame(wheel, now) {
		t.Fatal("deferred with an empty queue — the burst's last event would never paint")
	}
	a.screen.(tcell.SimulationScreen).InjectMouse(5, 5, tcell.WheelDown, 0)
	if !a.deferFrame(wheel, now) {
		t.Fatal("did not defer a wheel event with more input queued")
	}
}

// TestDeferFrame_CappedSoASpinStillScrollsVisibly: a sustained spin never
// empties the queue, so past burstFrameMax a frame must paint anyway or
// the file would scroll invisibly and then jump.
func TestDeferFrame_CappedSoASpinStillScrollsVisibly(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	now := time.Now()
	a.lastFrameAt = now.Add(-burstFrameMax)
	a.screen.(tcell.SimulationScreen).InjectMouse(5, 5, tcell.WheelDown, 0)

	if a.deferFrame(tcell.NewEventMouse(5, 5, tcell.WheelDown, 0), now) {
		t.Fatal("deferred past the frame cap")
	}
}

// TestDeferFrame_KeysAlwaysPaint: a keystroke's effect is seen one at a
// time, so it never defers even behind a queue.
func TestDeferFrame_KeysAlwaysPaint(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	now := time.Now()
	a.lastFrameAt = now
	a.screen.(tcell.SimulationScreen).InjectMouse(5, 5, tcell.WheelDown, 0)

	if a.deferFrame(tcell.NewEventKey(tcell.KeyRune, 'x', 0), now) {
		t.Fatal("a key event deferred its frame")
	}
}

// loopStep replays one iteration of Run's loop body — handle the event,
// then ask deferFrame — and reports whether that iteration would paint.
// Run itself blocks in PollEvent, so this is how a test counts frames.
func loopStep(a *App, ev tcell.Event) bool {
	a.handleEvent(ev)
	return !a.deferFrame(ev, time.Now())
}

// TestDeferFrame_PasteContentNeverPaints is the N-125 regression: a
// bracketed paste arrives as one EventKey per rune, and before the fix
// each one paid a full frame (1.7ms a rune at 200×60; 9s for a 125-line
// paste) though handleKey only appended it to pasteBuf. Now the start
// marker and the end marker paint and nothing in between does — and the
// text still lands whole.
func TestDeferFrame_PasteContentNeverPaints(t *testing.T) {
	a := openBlankTab(t)
	text := strings.Repeat("abc\tdef\n", 50)

	frames := 0
	if loopStep(a, tcell.NewEventPaste(true)) {
		frames++
	}
	if !a.pasting {
		t.Fatal("the start marker did not arm accumulation on an editable tab")
	}
	for _, r := range text {
		var ev *tcell.EventKey
		switch r {
		case '\n':
			ev = tcell.NewEventKey(tcell.KeyEnter, '\r', tcell.ModNone)
		case '\t':
			ev = tcell.NewEventKey(tcell.KeyTab, '\t', tcell.ModNone)
		default:
			ev = tcell.NewEventKey(tcell.KeyRune, r, tcell.ModNone)
		}
		if loopStep(a, ev) {
			frames++
		}
	}
	if loopStep(a, tcell.NewEventPaste(false)) {
		frames++
	}

	if frames != 2 {
		t.Fatalf("a %d-rune paste painted %d frames, want 2 (start + end marker)",
			len([]rune(text)), frames)
	}
	if got := a.activeTabPtr().Buffer.String(); got != text {
		t.Fatalf("pasted text did not land whole: %q", got)
	}
}

// TestDeferFrame_NonKeyEventsMidPastePaint: only the pasted KEYS skip
// their frame — a timer or background result that lands between the
// markers still paints, so nothing it changed waits on the end marker.
func TestDeferFrame_NonKeyEventsMidPastePaint(t *testing.T) {
	a := openBlankTab(t)
	a.handlePaste(tcell.NewEventPaste(true))
	if !a.pasting {
		t.Fatal("paste not armed")
	}
	if a.deferFrame(tcell.NewEventInterrupt(nil), time.Now()) {
		t.Fatal("a non-key event mid-paste deferred its frame")
	}
}
