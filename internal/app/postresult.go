// =============================================================================
// File: internal/app/postresult.go
// Author: Rohan Allison <rohanthewiz@gmail.com>
// Created: 2026-11-17
// Copyright: 2026 Rohan Allison. All rights reserved.
// =============================================================================

// postresult.go delivers COMPLETION events to the main loop reliably
// (N-124).
//
// tcell's PostEvent never blocks: when its 256-slot queue is full it
// DROPS the event and returns ErrEventQFull. The queue fills whenever
// input outruns the loop — a big paste, or a stall while the pointer
// moves (all-motion mouse reporting is on) — because tcell's own input
// enqueue blocks while ours doesn't. For a tick that is harmless: the
// next one comes. For a completion it is permanent stuck state, because
// the event is the only thing that clears what its launch set:
//
//	formatDoneEvent      formatRuns[path] up → reconcile off for the file
//	termDoneEvent        term.running stays → the panel "busy" for good
//	chatTurnDoneEvent    chat.turnActive stays → no new turn
//	…ReadyEvent          `starting` stays, the child leaks
//	autoSave / validate  one-shot timers never re-armed until the next edit
//
// postResult is PostEvent for those: it waits for room instead of
// dropping. It must only ever run OFF the main loop (a goroutine, an
// AfterFunc, a client's read loop) — on the loop it would wait for the
// loop to drain itself. Ticks (caret blink, dwell timers, the tree tick,
// tooltips' expiry) keep plain PostEvent: dropping one of those costs a
// frame, and making them wait would only add to a flood.
package app

import (
	"time"

	"github.com/gdamore/tcell/v2"
)

// postResultGiveUp bounds the wait. A queue that stays full this long
// belongs to a loop that is gone — an App closed (Fini) or switched away
// from by Open folder, whose screen nobody drains any more — or wedged
// for good; waiting on would only leak the goroutine. In a live editor
// the queue drains within a frame.
var postResultGiveUp = time.Minute // a var only so a test can shorten it

// postResultMaxDelay caps the retry backoff: the wait after a refusal
// doubles from 1ms to this, so a burst that clears in a millisecond is
// not slept through, and a long one costs a wake-up every 20ms.
const postResultMaxDelay = 20 * time.Millisecond

// postResult posts ev, retrying while tcell's queue is full until it is
// taken or postResultGiveUp passes. Returns whether ev was delivered.
// Never call it from the main loop.
//
// A retry loop rather than tcell's PostEventWait: that one is deprecated
// (for exactly the main-loop deadlock the rule above avoids) and has no
// deadline; the channels it waits on are not on the Screen interface.
func postResult(scr tcell.Screen, ev tcell.Event) bool {
	if scr == nil {
		return false
	}
	// The common case costs exactly what PostEvent did.
	if scr.PostEvent(ev) == nil {
		return true
	}
	deadline := time.Now().Add(postResultGiveUp)
	delay := time.Millisecond
	for time.Now().Before(deadline) {
		time.Sleep(delay)
		if scr.PostEvent(ev) == nil {
			return true
		}
		delay = min(delay*2, postResultMaxDelay)
	}
	return false
}
