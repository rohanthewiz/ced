// =============================================================================
// File: internal/app/crashsafety.go
// Author: Rohan Allison <rohanthewiz@gmail.com>
// Created: 2026-11-13
// Copyright: 2026 Rohan Allison. All rights reserved.
// =============================================================================

// Crash safety: the exits nobody asked for (N-118).
//
// Close is the one path that records the session, writes history and
// hands the terminal back — and until this file, only a plain quit or a
// folder switch reached it. Three other exits skipped it entirely:
//
//	exit                     before                        now
//	───────────────────────  ────────────────────────────  ──────────────────────────────
//	panic on the main loop   process dies, tty left raw    Run's recover: rescue, Fini,
//	                         (alt screen, mouse 1003,      re-panic (the trace prints on
//	                         kitty keys), edits lost       the normal screen)
//	panic in a goroutine     same                          crash.Recover → crashEvent →
//	                                                       the main-loop path above
//	SIGHUP / SIGTERM         same, minus the trace         quit event → auto-save flush,
//	(tmux kill-pane, kill)                                 rescue the rest, normal Close
//	tty read error           editor ignored it and sat     treated as a hangup
//	                         with no input, forever
//
// "Rescue" writes every dirty buffer to a folder under
// ~/.config/ced/recover/ (internal/crash/rescue.go), never over the
// user's file. After a panic the buffer may be what is broken; on a
// signal nobody is there to answer "save or discard?". The one exception
// is the signal path with auto-save ON: a killed pane is the clearest
// "I left" there is, so the focus-out flush's rule applies first, and
// only what it would not write is rescued.
//
// The rescue runs at most once per App (crashState.once), whichever path
// gets there first — the main loop's recover and a goroutine whose
// hand-off timed out can race for it.

package app

import (
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/gdamore/tcell/v2"

	"github.com/rohanthewiz/ced/internal/crash"
	"github.com/rohanthewiz/ced/internal/userconfig"
)

// Seams. Package vars for the SyntaxSettle reason: tests point the
// rescue folder at a temp dir, collapse the hand-off wait, capture the
// exit note, and never register a real signal handler or exit the test
// binary.
var (
	// recoverDirFn is where rescue folders go (~/.config/ced/recover).
	recoverDirFn = userconfig.RecoverDir
	// crashHandoffWait is how long a panicking goroutine waits for the
	// main loop to take its panic over before rescuing on its own. The
	// loop answers in microseconds when it is healthy; the wait only
	// runs out when it is stuck — inside a blocking call, or the very
	// thing that hung.
	crashHandoffWait = 3 * time.Second
	// goroutineCrash ends the process from a goroutine whose hand-off
	// timed out. A re-panic, not os.Exit: we are still inside the
	// deferred crash.Recover, so the panicking frames are on the stack
	// and Go's trace for this panic shows where it really happened.
	goroutineCrash = func(p *crash.Panic) { panic(p) }
	// exitNoteOut receives the line(s) printed after the screen is
	// finalized — where the rescue went, why the editor stopped.
	exitNoteOut io.Writer = os.Stderr
	// signalNotify / signalStop are os/signal's pair.
	signalNotify = signal.Notify
	signalStop   = signal.Stop
)

// crashState groups the crash-safety bookkeeping on the App.
type crashState struct {
	// once guards the rescue: it runs at most once, from whichever path
	// reaches it first.
	once sync.Once
	// restore uninstalls this App's goroutine-panic handler, if it is
	// still the installed one (crash.SetHandler).
	restore func()
	// sigCh / sigStop are the signal watcher's channel and its stop.
	sigCh   chan os.Signal
	sigStop chan struct{}
	// unattended is set once quitUnattended ran: a SIGHUP and the tty
	// read error it causes both arrive, and the second must be a no-op.
	unattended bool
	// exitNote is printed by Close after Fini — the only time a line on
	// stderr lands on a screen the user can read.
	exitNote string
}

// crashEvent carries a background goroutine's panic onto the main loop.
type crashEvent struct {
	when time.Time
	p    *crash.Panic
}

// When satisfies tcell.Event.
func (e *crashEvent) When() time.Time { return e.when }

// signalQuitEvent is a SIGHUP or SIGTERM, posted by the signal watcher.
type signalQuitEvent struct {
	when time.Time
	sig  os.Signal
}

// When satisfies tcell.Event.
func (e *signalQuitEvent) When() time.Time { return e.when }

// installCrashSafety is called from New, before any goroutine starts:
// the goroutine-panic handler and the signal watcher. Tests build App
// directly and never call it — they drive the handlers instead.
func (a *App) installCrashSafety() {
	a.crash.restore = crash.SetHandler(a.onGoroutinePanic)
	a.startSignalWatch()
}

// teardownCrashSafety undoes installCrashSafety. Called LAST in Close:
// a SIGTERM that lands while Close is still restoring the terminal must
// keep being caught — with the default disposition back in place it
// would kill the process half-way through Fini, the exact mess this file
// exists to prevent.
func (a *App) teardownCrashSafety() {
	if a.crash.sigCh != nil {
		signalStop(a.crash.sigCh)
		close(a.crash.sigStop)
		a.crash.sigCh, a.crash.sigStop = nil, nil
	}
	if a.crash.restore != nil {
		a.crash.restore()
		a.crash.restore = nil
	}
}

// startSignalWatch asks for SIGHUP and SIGTERM and forwards each onto
// the main loop as a signalQuitEvent. Only those two: SIGINT can't come
// from the keyboard in raw mode, and SIGQUIT's goroutine dump is a
// debugging tool worth keeping.
//
// A full event queue (a wheel flood at the moment of the signal) must
// not lose the quit, so the post is retried until it lands or the
// watcher is stopped.
func (a *App) startSignalWatch() {
	if a.screen == nil {
		return
	}
	ch := make(chan os.Signal, 4)
	signalNotify(ch, syscall.SIGHUP, syscall.SIGTERM)
	stop := make(chan struct{})
	a.crash.sigCh, a.crash.sigStop = ch, stop
	scr := a.screen
	go func() {
		defer crash.Recover()
		for {
			select {
			case <-stop:
				return
			case sig := <-ch:
				ev := &signalQuitEvent{when: time.Now(), sig: sig}
				for scr.PostEvent(ev) != nil {
					select {
					case <-stop:
						return
					case <-time.After(20 * time.Millisecond):
					}
				}
			}
		}
	}()
}

// recoverRun is deferred by Run (and by the entry points main calls
// around it). It must be the deferred function itself — recover() only
// stops a panic when called directly by one. Rescue, hand the terminal
// back, then re-panic with the same value so the process still dies
// loudly with its trace, now on the normal screen.
func (a *App) recoverRun() {
	r := recover()
	if r == nil {
		return
	}
	a.crashRescue(r)
	panic(r)
}

// onGoroutinePanic is the crash.Handler: it runs ON the panicking
// goroutine. The happy path posts the panic to the main loop, which
// re-raises it inside Run where every tab read is on the right
// goroutine; this goroutine just waits for the process to end.
//
// If the loop never takes it (the queue is full, or the loop is blocked
// — possibly on this very goroutine), the rescue runs from here. That
// reads tabs off the main loop, which the events-only rule forbids for
// good reason; a process seconds from death with the loop wedged is the
// one case where a racy read beats losing the edits outright.
func (a *App) onGoroutinePanic(p *crash.Panic) {
	if scr := a.screen; scr != nil && scr.PostEvent(&crashEvent{when: time.Now(), p: p}) == nil {
		time.Sleep(crashHandoffWait)
	}
	a.crashRescue(p)
	goroutineCrash(p)
}

// handleCrashEvent re-raises a goroutine's panic on the main loop, where
// Run's recover takes it from there.
func (a *App) handleCrashEvent(e *crashEvent) {
	panic(e.p)
}

// crashRescue is the panic exit: rescue the dirty buffers, record what
// Close would have recorded, hand the terminal back, and say where the
// edits went. Each step is guarded on its own — state that has just
// panicked may panic again, and a failure in one step must not cost the
// next (above all, never the Fini).
func (a *App) crashRescue(v any) {
	a.crash.once.Do(func() {
		note := a.rescueDirtyTabs("ced crashed: " + crash.FirstLine(v))
		// The tab list is only recorded once startup has finished
		// restoring it: a panic half-way through restoreSession would
		// otherwise write the half it got through over the whole list.
		if a.sessionTabs.armed {
			crashStep(a.recordSession)
		}
		crashStep(a.writeHistory)
		crashStep(func() {
			if a.screen != nil {
				a.screen.Fini()
			}
		})
		fmt.Fprintln(exitNoteOut, "ced crashed: "+crash.FirstLine(v))
		if note != "" {
			fmt.Fprintln(exitNoteOut, note)
		}
	})
}

// crashStep runs one rescue step, swallowing a panic from it.
func crashStep(fn func()) {
	defer func() { _ = recover() }()
	fn()
}

// handleSignalQuit is the main-loop end of the signal watcher.
func (a *App) handleSignalQuit(e *signalQuitEvent) {
	a.quitUnattended("received " + signalLabel(e.sig))
}

// handleTTYError is tcell's input loop reporting that reading the
// terminal failed. It posts that once and stops reading for good, so an
// editor that carried on would never see another key — the hangup a
// SIGHUP announces, arriving by the other door (or alone, when the
// terminal vanished without one).
func (a *App) handleTTYError(e *tcell.EventError) {
	a.quitUnattended("the terminal stopped answering (" + e.Error() + ")")
}

// quitUnattended ends the editor with nobody at the keyboard: the dirty
// buffers auto-save would write are written, the rest are rescued, and
// the loop exits into the ordinary Close — session, history, LSP
// shutdown, terminal restore all run as on a plain quit.
//
// No question is asked and any open one is abandoned: there is nobody
// to answer it.
func (a *App) quitUnattended(reason string) {
	if a.crash.unattended {
		return
	}
	a.crash.unattended = true
	a.flushAutoSaveForExit()
	if note := a.rescueDirtyTabs(reason); note != "" {
		a.crash.exitNote = note
	}
	// A folder switch in flight is abandoned with everything else.
	a.nextRoot = ""
	a.quit = true
}

// flushAutoSaveForExit writes what the focus-out flush would write:
// auto-save on, no modal or menu up (a save under "Save / Discard /
// Cancel?" would decide the question for the user), and each tab passing
// autoSavable + autoSaveGuard. Not autoSaveTab itself: its follow-ups
// (git diff, LSP didSave, format-on-save) start work this exit would
// abandon half-way. The cost is a Go file left as typed, unformatted,
// until its next save.
func (a *App) flushAutoSaveForExit() {
	if !a.autoSaveEnabled || a.modal != nil || a.menuOpen {
		return
	}
	for _, t := range a.tabs {
		if autoSavable(t) && a.autoSaveGuard(t) {
			_ = t.Save() // a failure leaves it dirty, so the rescue takes it
		}
	}
}

// rescueDirtyTabs writes every dirty text buffer to a rescue folder and
// returns the line telling the user where — or "" when nothing was
// dirty. Guarded: it runs during a panic, and must report rather than
// raise.
func (a *App) rescueDirtyTabs(reason string) (note string) {
	defer func() {
		if r := recover(); r != nil {
			note = "ced could not rescue unsaved edits: " + crash.FirstLine(r)
		}
	}()
	bufs := a.dirtyBuffers()
	if len(bufs) == 0 {
		return ""
	}
	folder, out, err := crash.Rescue(recoverDirFn(), a.rootDir, reason, time.Now(), bufs)
	return rescueNote(folder, len(out), len(bufs), err)
}

// dirtyBuffers collects the bytes of every dirty text tab, encoded as
// Save would write them. A Windows-1252 tab holding a character the code
// page can't is rescued as UTF-8 with a note — a rescue never refuses;
// the user can convert or re-type it, but only if the bytes exist.
func (a *App) dirtyBuffers() []crash.Buffer {
	var bufs []crash.Buffer
	for _, t := range a.tabs {
		if t == nil || !t.Dirty || t.IsImage() || t.Buffer == nil {
			continue
		}
		b := crash.Buffer{Path: t.Path}
		data, err := t.EncodedBytes()
		if err != nil {
			data = []byte(t.Buffer.String())
			b.Note = "written as UTF-8 with LF line endings — " + err.Error()
		}
		b.Data = data
		bufs = append(bufs, b)
	}
	return bufs
}

// rescueNote is the exit line for a rescue of want buffers of which got
// landed in folder.
func rescueNote(folder string, got, want int, err error) string {
	if got == 0 {
		return fmt.Sprintf("ced could not rescue %d unsaved buffer(s): %v", want, err)
	}
	var b strings.Builder
	fmt.Fprintf(&b, "Unsaved edits in %d file(s) were written to %s (see %s).", got, folder, crash.IndexName)
	if err != nil {
		fmt.Fprintf(&b, "\n%d could not be: %v", want-got, err)
	}
	return b.String()
}

// signalLabel names the two signals ced catches the way a user knows
// them; anything else falls back to Go's spelling.
func signalLabel(sig os.Signal) string {
	switch sig {
	case syscall.SIGHUP:
		return "SIGHUP (the terminal hung up)"
	case syscall.SIGTERM:
		return "SIGTERM"
	}
	if sig == nil {
		return "a signal"
	}
	return sig.String()
}

// Compile-time checks that the events really are tcell.Events.
var (
	_ tcell.Event = (*crashEvent)(nil)
	_ tcell.Event = (*signalQuitEvent)(nil)
)
