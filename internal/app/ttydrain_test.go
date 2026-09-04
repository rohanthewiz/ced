// =============================================================================
// File: internal/app/ttydrain_test.go
// Author: Rohan Allison <rohanthewiz@gmail.com>
// Created: 2026-09-04
// Copyright: 2026 Rohan Allison. All rights reserved.
// =============================================================================

package app

import (
	"testing"
	"time"

	"github.com/gdamore/tcell/v2"
)

// withShortDrain collapses the drain windows for a test, the way the
// syntax tests collapse SyntaxSettle: the behavior under test is "the
// queue is empty afterwards", not how many milliseconds it waited.
func withShortDrain(t *testing.T, window, release time.Duration) {
	t.Helper()
	prevWindow, prevRelease, prevPoll := ttyDrainWindow, ttyDrainReleaseWindow, ttyDrainPoll
	ttyDrainWindow, ttyDrainReleaseWindow, ttyDrainPoll = window, release, time.Millisecond
	t.Cleanup(func() {
		ttyDrainWindow, ttyDrainReleaseWindow, ttyDrainPoll = prevWindow, prevRelease, prevPoll
	})
}

// TestDrainPendingInput_ConsumesQueuedEvents is the bug this file exists
// for: input the terminal already delivered — the release of the click
// that quit the editor — must be swallowed by ced rather than left in
// the tty for the shell to echo as `0;163;54m`.
func TestDrainPendingInput_ConsumesQueuedEvents(t *testing.T) {
	withShortDrain(t, 30*time.Millisecond, 30*time.Millisecond)
	a := newTestApp(t, t.TempDir())

	scr := a.screen.(tcell.SimulationScreen)
	// The tail of a mouse gesture: a release plus the motion reports
	// mode 1003 keeps producing while the pointer is still moving.
	scr.InjectMouse(163, 54, tcell.ButtonNone, tcell.ModNone)
	scr.InjectMouse(164, 54, tcell.ButtonNone, tcell.ModNone)
	scr.InjectKey(tcell.KeyRune, 'q', tcell.ModNone)

	a.drainPendingInput()

	if a.screen.HasPendingEvent() {
		t.Fatal("drainPendingInput left terminal input queued; it would reach the shell")
	}
}

// TestDrainPendingInput_HonorsItsWindow pins the other half of the
// contract: the drain is bounded. An editor that hung on the way out
// waiting for input that never comes would be a worse bug than the
// stray characters it exists to swallow.
func TestDrainPendingInput_HonorsItsWindow(t *testing.T) {
	withShortDrain(t, 20*time.Millisecond, 20*time.Millisecond)
	a := newTestApp(t, t.TempDir())

	start := time.Now()
	a.drainPendingInput()
	elapsed := time.Since(start)

	if elapsed < 20*time.Millisecond {
		t.Fatalf("returned after %v, before its own window elapsed", elapsed)
	}
	if elapsed > 2*time.Second {
		t.Fatalf("returned after %v — the drain is not bounded", elapsed)
	}
}

// TestDrainPendingInput_NilScreen covers the App that never got a
// screen: Close runs on paths a test or a failed startup can reach, and
// the drain must not be the thing that panics there.
func TestDrainPendingInput_NilScreen(t *testing.T) {
	withShortDrain(t, time.Millisecond, time.Millisecond)
	a := &App{}
	a.drainPendingInput() // must not panic
}

// TestClose_DrainsBeforeFini pins the WIRING, which is the part that can
// silently regress: draining after Fini would be too late (tcell stops
// reading), and not draining at all is the original bug. Asserting on
// Close rather than on the helper is what makes a reordering visible.
func TestClose_DrainsBeforeFini(t *testing.T) {
	withShortDrain(t, 30*time.Millisecond, 30*time.Millisecond)
	a := newTestApp(t, t.TempDir())

	scr := a.screen.(tcell.SimulationScreen)
	scr.InjectMouse(163, 54, tcell.ButtonNone, tcell.ModNone)

	a.Close()

	if a.screen.HasPendingEvent() {
		t.Fatal("Close finalized the screen with input still queued")
	}
}

// TestDrainPendingInput_WaitsForTheReleaseOfAHeldButton covers the exit
// gesture itself: a menu row fires on the press, so at Close the release
// has not been written yet and the baseline window would be over before
// it arrived. The drain must still be running when it lands.
func TestDrainPendingInput_WaitsForTheReleaseOfAHeldButton(t *testing.T) {
	withShortDrain(t, 5*time.Millisecond, 500*time.Millisecond)
	a := newTestApp(t, t.TempDir())
	a.mouseHeld = true

	scr := a.screen.(tcell.SimulationScreen)
	// The terminal writes the release well after the baseline window
	// would have closed.
	go func() {
		time.Sleep(60 * time.Millisecond)
		scr.InjectMouse(163, 54, tcell.ButtonNone, tcell.ModNone)
	}()

	a.drainPendingInput()

	if a.screen.HasPendingEvent() {
		t.Fatal("the release arrived after the drain gave up; it would reach the shell")
	}
}

// TestDrainPendingInput_StopsAtTheRelease pins the other side of that
// budget: it is a ceiling, not a delay every mouse-driven quit pays.
// Once the release is in hand there is nothing left to wait for.
func TestDrainPendingInput_StopsAtTheRelease(t *testing.T) {
	withShortDrain(t, 5*time.Millisecond, 2*time.Second)
	a := newTestApp(t, t.TempDir())
	a.mouseHeld = true

	scr := a.screen.(tcell.SimulationScreen)
	scr.InjectMouse(163, 54, tcell.ButtonNone, tcell.ModNone)

	start := time.Now()
	a.drainPendingInput()

	if elapsed := time.Since(start); elapsed > time.Second {
		t.Fatalf("waited %v after the release had already landed", elapsed)
	}
}

// TestDrainPendingInput_HeldButtonWindowIsBounded covers the case where
// no release ever comes — the terminal read the disable before the
// user's finger lifted, so there was never anything to strand. The exit
// must not hang on a byte that will not be written.
func TestDrainPendingInput_HeldButtonWindowIsBounded(t *testing.T) {
	withShortDrain(t, 5*time.Millisecond, 40*time.Millisecond)
	a := newTestApp(t, t.TempDir())
	a.mouseHeld = true

	start := time.Now()
	a.drainPendingInput()
	elapsed := time.Since(start)

	if elapsed < 40*time.Millisecond {
		t.Fatalf("returned after %v, before its own ceiling", elapsed)
	}
	if elapsed > 2*time.Second {
		t.Fatalf("returned after %v — the held-button drain is not bounded", elapsed)
	}
}

// TestHandleMouse_TracksHeldButton pins the flag the drain reads. A
// press that a surface handles and returns early on must still have been
// recorded, which is why handleMouse latches it before any routing.
func TestHandleMouse_TracksHeldButton(t *testing.T) {
	a := newTestApp(t, t.TempDir())

	a.handleMouse(tcell.NewEventMouse(5, 5, tcell.Button1, tcell.ModNone))
	if !a.mouseHeld {
		t.Fatal("a press did not latch mouseHeld")
	}

	a.handleMouse(tcell.NewEventMouse(5, 5, tcell.ButtonNone, tcell.ModNone))
	if a.mouseHeld {
		t.Fatal("a release did not clear mouseHeld")
	}

	// A wheel notch has no release to wait for and must not look like a
	// held button.
	a.handleMouse(tcell.NewEventMouse(5, 5, tcell.WheelUp, tcell.ModNone))
	if a.mouseHeld {
		t.Fatal("a wheel notch latched mouseHeld")
	}
}
