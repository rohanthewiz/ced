// =============================================================================
// File: internal/app/syntax.go
// Author: Rohan Allison <rohanthewiz@gmail.com>
// Created: 2026-08-03
// Copyright: 2026 Rohan Allison. All rights reserved.
// =============================================================================

// syntax.go is the wake-up half of the deferred-highlight policy that
// lives in editor/syntax.go. The editor decides WHETHER a re-lex is due;
// this decides that the editor gets asked again once the typing pause has
// elapsed, because the main loop is event-driven and would otherwise sit
// on PollEvent with the last keystroke's colors on screen:
//
//	edit → styles deferred → syntaxAfterEvent arms timer
//	                          └─ idle syntaxSettle ─► syntaxSettleEvent
//	                                                  └─ redraw → re-lex
//
// Same mechanism as the LSP didChange debounce and auto-save, and the
// same constraint as the caret blink: the timer is armed ONLY while a tab
// is actually waiting on one. A standing timer would wake an idle editor
// forever, which on a laptop is a battery bug and over SSH is traffic.
package app

import (
	"time"

	"github.com/gdamore/tcell/v2"
)

// syntaxSettleEvent is posted when the typing pause elapses. It carries
// no payload — the handler re-derives everything from live state, so a
// stale event (the user resumed typing before it landed) is a no-op.
type syntaxSettleEvent struct {
	when time.Time
}

// When satisfies the tcell.Event interface.
func (e *syntaxSettleEvent) When() time.Time { return e.when }

// syntaxAfterEvent runs after every event dispatch (the same slot as
// lspAfterEvent) and arms the settle timer when the active tab is
// painting from a deferred style grid.
//
// Only the ACTIVE tab is consulted: it's the only one being rendered, and
// a background tab's staleness resolves on the render that follows the
// switch to it. Nothing is armed when the wait is zero — either nothing
// is pending, or it's already due and the redraw that follows this
// dispatch will handle it.
func (a *App) syntaxAfterEvent() {
	tab := a.activeTabPtr()
	if tab == nil {
		return
	}
	if d := tab.SyntaxSettleWait(); d > 0 {
		a.armSyntaxSettle(d)
	}
}

// armSyntaxSettle (re)starts the countdown. Restarting on every further
// edit is what makes it a debounce — the re-lex lands after a genuine
// pause, never mid-burst.
func (a *App) armSyntaxSettle(d time.Duration) {
	if a.syntaxTimer != nil {
		a.syntaxTimer.Stop()
	}
	scr := a.screen
	if scr == nil {
		return
	}
	a.syntaxTimer = time.AfterFunc(d, func() {
		_ = scr.PostEvent(&syntaxSettleEvent{when: time.Now()})
	})
}

// stopSyntaxSettle cancels any pending countdown. Safe with none armed.
func (a *App) stopSyntaxSettle() {
	if a.syntaxTimer != nil {
		a.syntaxTimer.Stop()
		a.syntaxTimer = nil
	}
}

// handleSyntaxSettle is the countdown firing on the main loop. It does no
// work of its own by design: the redraw that follows every dispatch is
// the point — Tab.Render finds the settle window has passed and re-lexes
// there. Dropping the timer reference just keeps the bookkeeping honest.
func (a *App) handleSyntaxSettle() {
	a.syntaxTimer = nil
}

// syntaxStatusSuffix is the status-bar note for a tab opened with
// highlighting off. Files big enough to trip MaxHighlightBytes are rare,
// and a user who hits one deserves to know the missing colors are a
// deliberate size decision rather than a broken lexer.
func (a *App) syntaxStatusSuffix() string {
	if tab := a.activeTabPtr(); tab != nil && tab.SyntaxOff {
		return " · no syntax (large file)"
	}
	return ""
}

// Compile-time check that syntaxSettleEvent really is a tcell.Event.
var _ tcell.Event = (*syntaxSettleEvent)(nil)
