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
//
// It is also where a BIG buffer's re-lex runs (N-128). Render lexes only
// buffers up to editor.InlineLexBytes; above that the whole-file Chroma
// pass (~0.5-0.9 ms per KB, 113 ms per Enter on a 256KB file) would
// freeze the loop, so it runs on a goroutine:
//
//	afterEvent ──tab.BeginAsyncLex()──► go HighlightClasses(snapshot)
//	                                        └─ syntaxLexedEvent{tab, rev, grid}
//	handleSyntaxLexed ──tab.AdoptClasses(rev, grid)── stale rev? dropped,
//	                                                   and asked again
//
// One lex per tab at a time; the frame meanwhile paints the grid the tab
// has (patched rows, or the previous grid after an edit no patch covers).
package app

import (
	"time"

	"github.com/gdamore/tcell/v2"
	"github.com/rohanthewiz/ced/internal/crash"
	"github.com/rohanthewiz/ced/internal/editor"
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
	a.syntaxLexAsync(tab)
}

// syntaxLexedEvent carries an off-loop lex's grid back to the main loop,
// stamped with the tab and the revision of the text it lexed.
type syntaxLexedEvent struct {
	when time.Time
	tab  *editor.Tab
	rev  int
	grid [][]editor.SynClass
}

// When satisfies the tcell.Event interface.
func (e *syntaxLexedEvent) When() time.Time { return e.when }

// syntaxLexAsync starts tab's re-lex on a goroutine when one is due and
// the buffer is too big to lex inside Render (editor.Tab.BeginAsyncLex
// decides both, and refuses while one is already out).
func (a *App) syntaxLexAsync(tab *editor.Tab) {
	if a.screen == nil {
		return
	}
	path, src, rev, ok := tab.BeginAsyncLex()
	if !ok {
		return
	}
	scr := a.screen
	go func() {
		defer crash.Recover()
		grid := editor.HighlightClasses(path, src)
		postResult(scr, &syntaxLexedEvent{when: time.Now(), tab: tab, rev: rev, grid: grid})
	}()
}

// handleSyntaxLexed adopts an off-loop lex if its tab still holds the
// text it lexed, and asks again for the active tab either way — a stale
// answer means the buffer moved, and BeginAsyncLex knows whether another
// pass is due yet (it waits out the settle window like Render does).
func (a *App) handleSyntaxLexed(e *syntaxLexedEvent) {
	e.tab.AdoptClasses(e.rev, e.grid)
	if tab := a.activeTabPtr(); tab != nil {
		if d := tab.SyntaxSettleWait(); d > 0 {
			a.armSyntaxSettle(d)
		}
		a.syntaxLexAsync(tab)
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
		postResult(scr, &syntaxSettleEvent{when: time.Now()})
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

// Compile-time check that the syntax events really are tcell.Events.
var (
	_ tcell.Event = (*syntaxSettleEvent)(nil)
	_ tcell.Event = (*syntaxLexedEvent)(nil)
)
