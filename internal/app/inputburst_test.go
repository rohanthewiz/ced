// =============================================================================
// File: internal/app/inputburst_test.go
// Author: Rohan Allison <rohanthewiz@gmail.com>
// Created: 2026-09-22
// Copyright: 2026 Rohan Allison. All rights reserved.
// =============================================================================

package app

import (
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
