// =============================================================================
// File: internal/crash/guard.go
// Author: Rohan Allison <rohanthewiz@gmail.com>
// Created: 2026-11-13
// Copyright: 2026 Rohan Allison. All rights reserved.
// =============================================================================

// Package crash is what stands between a bug and the user's unsaved work.
//
// A Go panic that nobody recovers kills the whole process from whichever
// goroutine it happened on. For a terminal editor that is the worst exit
// there is: the deferred Close never runs, so the dirty buffers, the tab
// list and the history are gone, and the terminal is left in raw mode on
// the alternate screen with mouse reporting on and the trace printed into
// a screen that is about to be thrown away.
//
// The editor's main loop recovers its own panics (app/crashsafety.go). A
// background goroutine cannot be recovered from the outside — recover()
// only works in a deferred call on the panicking goroutine itself — so
// every goroutine ced starts opens with
//
//	defer crash.Recover()
//
// and TestEveryGoroutineRecovers walks the source tree to keep that true.
// Recover does not SWALLOW the panic. A panic is a bug, and carrying on
// after one (a lock still held, a reply channel never answered) trades a
// loud crash for a quiet hang. It hands the panic to the installed
// Handler instead, which moves it onto the main loop so the editor dies
// there, through the same rescue as a main-loop panic:
//
//	worker goroutine                    main loop (App.Run)
//	────────────────                    ───────────────────
//	panic(v)
//	  └─ defer crash.Recover()
//	       └─ Handler(&Panic{v, stack}) ──PostEvent──► panic(*Panic)
//	            waits; the process ends                  └─ Run's recover:
//	            before it wakes                              rescue buffers,
//	                                                          Fini, re-panic
//
// With no Handler installed (a test, a CLI path that never built an
// editor) Recover re-panics on the spot — exactly the crash Go would
// have produced without it.
//
// Timer callbacks (time.AfterFunc) are not covered: they are one-line
// PostEvent calls throughout, and wrapping them all would cost more
// reading than the panic it guards against.
package crash

import (
	"fmt"
	"runtime/debug"
	"strings"
	"sync/atomic"
)

// Panic is a recovered panic from a background goroutine, carried to the
// goroutine that will act on it. Stack is captured at the recover, while
// the panicking frames are still on the stack — once Recover returns
// they are gone, and a trace taken on the main loop would show only the
// event dispatch that re-raised it.
type Panic struct {
	Value any
	Stack []byte
}

// Error makes a re-raised Panic print usefully. Go prints a panic value
// that is an error through Error(), so the original goroutine's trace
// rides along in the final crash output instead of being lost behind
// the main loop's own (uninformative) trace.
func (p *Panic) Error() string {
	return fmt.Sprintf("%v [in a background goroutine]\n\n%s", p.Value, p.Stack)
}

// Summary is the one-line form for a log or an index file: the panic
// value's first line, without the stack.
func (p *Panic) Summary() string {
	return FirstLine(p.Value)
}

// FirstLine renders any panic value as its first line of text. A panic
// carrying a *Panic is unwrapped first, so a re-raised goroutine panic
// summarizes as what actually went wrong rather than as the wrapper.
func FirstLine(v any) string {
	if p, ok := v.(*Panic); ok {
		v = p.Value
	}
	s := fmt.Sprint(v)
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[:i]
	}
	return s
}

// Handler receives a background goroutine's panic, ON that goroutine.
// The editor's handler hands it to the main loop and waits for the
// process to end; it is not expected to return, but if it does the
// goroutine simply ends.
type Handler func(*Panic)

// handler is process-wide because goroutines outlive the code that
// started them: the LSP read loop or a git fork started by one App can
// still be running after a folder switch built the next one, and its
// panic must reach whichever editor is live NOW. An atomic pointer keeps
// the read in Recover lock-free — it runs on arbitrary goroutines at the
// worst possible moment.
var handler atomic.Pointer[Handler]

// SetHandler installs h as the process-wide panic handler and returns a
// function that puts back whatever was installed before. The restore is
// conditional — it only uninstalls h if h is still the one installed —
// so an App closing after its successor already installed a handler
// cannot pull the successor's out from under it.
func SetHandler(h Handler) (restore func()) {
	mine := &h
	prev := handler.Swap(mine)
	return func() {
		handler.CompareAndSwap(mine, prev)
	}
}

// Recover is deferred FIRST in every goroutine ced starts. It must be
// called directly by the defer statement (`defer crash.Recover()`), not
// from inside another deferred closure: recover() only stops a panic
// when the deferred function itself calls it.
//
// A nil recover() is the normal goroutine exit and costs nothing beyond
// the call itself — the handler is read only after a panic.
func Recover() {
	r := recover()
	if r == nil {
		return
	}
	p := &Panic{Value: r, Stack: debug.Stack()}
	if h := handler.Load(); h != nil && *h != nil {
		(*h)(p)
		return
	}
	// No editor to rescue for. Re-raise the ORIGINAL value, not the
	// wrapper: we are still inside the deferred call, so the panicking
	// frames are still on the stack and the trace Go prints for this
	// panic includes them, exactly as if Recover had never run.
	panic(r)
}
