// =============================================================================
// File: internal/app/crashsafety_test.go
// Author: Rohan Allison <rohanthewiz@gmail.com>
// Created: 2026-11-13
// Copyright: 2026 Rohan Allison. All rights reserved.
// =============================================================================

package app

import (
	"bytes"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/gdamore/tcell/v2"

	"github.com/rohanthewiz/ced/internal/crash"
)

// crashFixture is a project with one file open and edited (dirty, not
// saved), auto-save on, and the exit note captured.
func crashFixture(t *testing.T) (a *App, path string, note *bytes.Buffer) {
	t.Helper()
	root := t.TempDir()
	path = filepath.Join(root, "main.go")
	if err := os.WriteFile(path, []byte("package main\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	a = newTestApp(t, root)
	a.autoSaveEnabled = true
	a.openFile(path)
	a.activeTabPtr().InsertString("// unsaved\n")
	note = &bytes.Buffer{}
	exitNoteOut = note // newTestApp's cleanup restores it
	return a, path, note
}

// rescueFolders lists the rescue folders written so far.
func rescueFolders(t *testing.T) []string {
	t.Helper()
	entries, err := os.ReadDir(recoverDirFn())
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, e := range entries {
		if e.IsDir() {
			out = append(out, filepath.Join(recoverDirFn(), e.Name()))
		}
	}
	return out
}

// pollFor drains the screen's queue until an event of type T arrives.
func pollFor[T tcell.Event](t *testing.T, a *App) T {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		ev := a.screen.PollEvent()
		if ev == nil {
			t.Fatal("screen returned nil event")
		}
		if want, ok := ev.(T); ok {
			return want
		}
	}
	var zero T
	t.Fatalf("timed out waiting for %T", zero)
	return zero
}

// catchPanic runs fn and returns what it panicked with (nil if nothing).
func catchPanic(fn func()) (got any) {
	defer func() { got = recover() }()
	fn()
	return nil
}

// TestRecoverRun_RescuesThenRepanics is the main-loop promise: a panic
// in Run writes the dirty buffer to a rescue folder, says where on the
// exit note, and still dies with the ORIGINAL value — the trace must
// not be swallowed.
func TestRecoverRun_RescuesThenRepanics(t *testing.T) {
	a, path, note := crashFixture(t)

	got := catchPanic(func() {
		defer a.recoverRun()
		panic("boom")
	})

	if got != "boom" {
		t.Fatalf("re-panicked with %#v, want the original \"boom\"", got)
	}
	folders := rescueFolders(t)
	if len(folders) != 1 {
		t.Fatalf("rescue folders = %v, want one", folders)
	}
	rescued, err := os.ReadFile(filepath.Join(folders[0], "main.go"))
	if err != nil || !strings.Contains(string(rescued), "// unsaved") {
		t.Fatalf("rescued main.go = %q (%v), want the unsaved buffer", rescued, err)
	}
	if disk, _ := os.ReadFile(path); string(disk) != "package main\n" {
		t.Fatalf("a crash wrote the real file: %q", disk)
	}
	if out := note.String(); !strings.Contains(out, "ced crashed: boom") || !strings.Contains(out, folders[0]) {
		t.Fatalf("exit note = %q, want the panic and the folder", out)
	}
}

// TestRecoverRun_NoPanicIsANoOp pins the quiet path: Run returning
// normally must not rescue or print anything.
func TestRecoverRun_NoPanicIsANoOp(t *testing.T) {
	a, _, note := crashFixture(t)
	func() { defer a.recoverRun() }()
	if len(rescueFolders(t)) != 0 || note.Len() != 0 {
		t.Fatalf("a clean return rescued %v / printed %q", rescueFolders(t), note.String())
	}
}

// TestCrashRescue_RunsOnce pins the once-guard: the main loop's recover
// and a goroutine's timed-out hand-off can both reach the rescue, and
// the second must not write a second folder or finalize twice.
func TestCrashRescue_RunsOnce(t *testing.T) {
	a, _, _ := crashFixture(t)
	a.crashRescue("first")
	a.crashRescue("second")
	if n := len(rescueFolders(t)); n != 1 {
		t.Fatalf("%d rescue folders, want 1", n)
	}
}

// TestCrashRescue_RecordsTheSessionOnlyOnceArmed pins the half-restore
// guard: a panic during startup (before New armed the tab record) must
// not write the half-restored tab list over the real one.
func TestCrashRescue_RecordsTheSessionOnlyOnceArmed(t *testing.T) {
	a, _, _ := crashFixture(t)
	seedSessionStore(a)
	a.crashRescue("during restore")
	if _, err := os.Stat(sessionStatePathFn()); !os.IsNotExist(err) {
		t.Fatalf("an unarmed crash wrote state.json (%v)", err)
	}

	b, _, _ := crashFixture(t)
	seedSessionStore(b)
	b.armSessionTabs()
	b.crashRescue("after startup")
	if _, err := os.Stat(sessionStatePathFn()); err != nil {
		t.Fatalf("an armed crash did not record the session: %v", err)
	}
}

// TestOnGoroutinePanic_HandsOffToTheMainLoop pins the happy hand-off: a
// goroutine's panic is posted as a crashEvent, and dispatching it on the
// main loop re-raises the SAME panic inside Run's recover, which
// rescues there — every tab read on the right goroutine.
func TestOnGoroutinePanic_HandsOffToTheMainLoop(t *testing.T) {
	a, _, _ := crashFixture(t)
	prevWait, prevCrash := crashHandoffWait, goroutineCrash
	crashed := make(chan *crash.Panic, 1)
	crashHandoffWait = 200 * time.Millisecond
	goroutineCrash = func(p *crash.Panic) { crashed <- p }
	t.Cleanup(func() { crashHandoffWait, goroutineCrash = prevWait, prevCrash })

	p := &crash.Panic{Value: "bg", Stack: []byte("worker()")}
	go a.onGoroutinePanic(p)
	ev := pollFor[*crashEvent](t, a)

	got := catchPanic(func() {
		defer a.recoverRun()
		a.handleEvent(ev)
	})
	if got != p {
		t.Fatalf("main loop re-raised %#v, want the goroutine's *crash.Panic", got)
	}
	if n := len(rescueFolders(t)); n != 1 {
		t.Fatalf("%d rescue folders, want 1", n)
	}
	// The goroutine's own wait still ends in its crash call; the process
	// would already be gone, but here we wait so the seam is restored
	// only after it ran.
	select {
	case <-crashed:
	case <-time.After(5 * time.Second):
		t.Fatal("goroutine never finished its hand-off wait")
	}
}

// TestGuardedGoroutinePanic_RescuesWhenTheLoopIsStuck is the end-to-end
// fallback: a guarded goroutine panics, nobody is polling the queue (the
// loop is wedged), so after the hand-off wait the goroutine rescues on
// its own and then crashes with the panic.
func TestGuardedGoroutinePanic_RescuesWhenTheLoopIsStuck(t *testing.T) {
	a, _, note := crashFixture(t)
	prevWait, prevCrash := crashHandoffWait, goroutineCrash
	crashed := make(chan *crash.Panic, 1)
	crashHandoffWait = 20 * time.Millisecond
	goroutineCrash = func(p *crash.Panic) { crashed <- p }
	t.Cleanup(func() { crashHandoffWait, goroutineCrash = prevWait, prevCrash })
	restore := crash.SetHandler(a.onGoroutinePanic)
	defer restore()

	go func() {
		defer crash.Recover()
		panic("worker exploded")
	}()

	select {
	case p := <-crashed:
		if p.Value != "worker exploded" {
			t.Fatalf("crashed with %v", p.Value)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the goroutine's panic never reached the editor")
	}
	if n := len(rescueFolders(t)); n != 1 {
		t.Fatalf("%d rescue folders, want 1", n)
	}
	if !strings.Contains(note.String(), "ced crashed: worker exploded") {
		t.Fatalf("exit note = %q", note.String())
	}
}

// TestHandleSignalQuit_AutoSavesThenRescuesTheRest pins the signal exit
// with auto-save on: a file auto-save may write is written (a killed
// pane is the clearest "I left"), an untitled buffer it can't write is
// rescued, and the loop is asked to exit through the normal Close.
func TestHandleSignalQuit_AutoSavesThenRescuesTheRest(t *testing.T) {
	a, path, _ := crashFixture(t)
	untitled := newUntitledDirtyTab()
	untitled.InsertString("scratch")
	a.tabs = append(a.tabs, untitled)

	a.handleEvent(&signalQuitEvent{when: time.Now(), sig: syscall.SIGHUP})

	if !a.quit {
		t.Fatal("a signal must end the loop")
	}
	if disk, _ := os.ReadFile(path); !strings.Contains(string(disk), "// unsaved") {
		t.Fatalf("auto-save on: the file should have been written, disk = %q", disk)
	}
	folders := rescueFolders(t)
	if len(folders) != 1 {
		t.Fatalf("rescue folders = %v, want one", folders)
	}
	if _, err := os.Stat(filepath.Join(folders[0], "main.go")); !os.IsNotExist(err) {
		t.Fatal("a buffer auto-save wrote was also rescued")
	}
	if got, _ := os.ReadFile(filepath.Join(folders[0], "untitled-1.txt")); !strings.Contains(string(got), "scratch") {
		t.Fatalf("untitled rescue = %q", got)
	}
	if !strings.Contains(a.crash.exitNote, folders[0]) {
		t.Fatalf("exit note %q does not say where", a.crash.exitNote)
	}
}

// TestHandleSignalQuit_AutoSaveOffRescuesInstead pins the off switch:
// "don't write behind my back" holds on a signal too — the edits go to
// the rescue folder and the file is untouched.
func TestHandleSignalQuit_AutoSaveOffRescuesInstead(t *testing.T) {
	a, path, _ := crashFixture(t)
	a.autoSaveEnabled = false

	a.handleEvent(&signalQuitEvent{when: time.Now(), sig: syscall.SIGTERM})

	if disk, _ := os.ReadFile(path); string(disk) != "package main\n" {
		t.Fatalf("auto-save off, but the file was written: %q", disk)
	}
	folders := rescueFolders(t)
	if len(folders) != 1 {
		t.Fatalf("rescue folders = %v, want one", folders)
	}
	index, _ := os.ReadFile(filepath.Join(folders[0], crash.IndexName))
	if !strings.Contains(string(index), "SIGTERM") || !strings.Contains(string(index), path) {
		t.Fatalf("INDEX.txt = %q, want the signal and the original path", index)
	}
}

// TestHandleSignalQuit_OpenModalRescuesInsteadOfSaving pins the modal
// rule auto-save already follows: under "Save / Discard / Cancel?" a
// write would answer the question for the user, so nothing is written
// and the buffer is rescued.
func TestHandleSignalQuit_OpenModalRescuesInsteadOfSaving(t *testing.T) {
	a, path, _ := crashFixture(t)
	a.modal = &confirmModal{}

	a.handleEvent(&signalQuitEvent{when: time.Now(), sig: syscall.SIGHUP})

	if disk, _ := os.ReadFile(path); string(disk) != "package main\n" {
		t.Fatalf("a save landed under an open modal: %q", disk)
	}
	if n := len(rescueFolders(t)); n != 1 {
		t.Fatalf("%d rescue folders, want 1", n)
	}
}

// TestHandleTTYError_QuitsLikeAHangup pins the other door: tcell stops
// reading after a tty read error, so the editor must leave (rescuing on
// the way) rather than sit forever with no input.
func TestHandleTTYError_QuitsLikeAHangup(t *testing.T) {
	a, _, _ := crashFixture(t)
	a.autoSaveEnabled = false

	a.handleEvent(tcell.NewEventError(io.EOF))

	if !a.quit {
		t.Fatal("a dead terminal must end the loop")
	}
	if n := len(rescueFolders(t)); n != 1 {
		t.Fatalf("%d rescue folders, want 1", n)
	}
}

// TestQuitUnattended_IsIdempotentAndDropsAFolderSwitch pins that the
// SIGHUP + tty-error pair rescues once, and that a folder switch in
// flight is abandoned rather than started after the terminal is gone.
func TestQuitUnattended_IsIdempotentAndDropsAFolderSwitch(t *testing.T) {
	a, _, _ := crashFixture(t)
	a.autoSaveEnabled = false
	a.nextRoot = t.TempDir()

	a.quitUnattended("first")
	a.quitUnattended("second")

	if n := len(rescueFolders(t)); n != 1 {
		t.Fatalf("%d rescue folders, want 1", n)
	}
	if a.nextRoot != "" {
		t.Fatalf("nextRoot = %q, want the switch abandoned", a.nextRoot)
	}
}

// TestQuitUnattended_CleanEditorSaysNothing pins that a signal with
// nothing unsaved leaves no folder and no exit note.
func TestQuitUnattended_CleanEditorSaysNothing(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	a.quitUnattended("received SIGTERM")
	if !a.quit || a.crash.exitNote != "" || len(rescueFolders(t)) != 0 {
		t.Fatalf("quit=%v note=%q folders=%v", a.quit, a.crash.exitNote, rescueFolders(t))
	}
}

// TestClose_PrintsTheExitNote pins where the rescue is announced: by
// Close, after Fini, so the line lands on the normal screen.
func TestClose_PrintsTheExitNote(t *testing.T) {
	withShortDrain(t, time.Millisecond, time.Millisecond)
	a := newTestApp(t, t.TempDir())
	var out bytes.Buffer
	exitNoteOut = &out
	a.crash.exitNote = "Unsaved edits were written to /x"

	a.Close()

	if !strings.Contains(out.String(), "Unsaved edits were written to /x") {
		t.Fatalf("Close printed %q", out.String())
	}
}

// TestDirtyBuffers_UnencodableIsRescuedAsUTF8 pins that a rescue never
// refuses: a Windows-1252 file holding a character the code page can't
// is rescued as UTF-8 with a note, where Save would refuse outright.
func TestDirtyBuffers_UnencodableIsRescuedAsUTF8(t *testing.T) {
	a, tab, _ := openLatin1(t)
	typeArrow(tab)

	bufs := a.dirtyBuffers()

	if len(bufs) != 1 {
		t.Fatalf("dirtyBuffers = %d, want 1", len(bufs))
	}
	if !strings.Contains(string(bufs[0].Data), "→") || bufs[0].Note == "" {
		t.Fatalf("buffer = %q note %q, want UTF-8 text and a note", bufs[0].Data, bufs[0].Note)
	}
}

// TestStartSignalWatch_ForwardsSignalsAndStops pins the watcher's
// plumbing: a caught signal becomes a signalQuitEvent on the loop, and
// teardown hands the signals back.
func TestStartSignalWatch_ForwardsSignalsAndStops(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	prevNotify, prevStop := signalNotify, signalStop
	var caught chan<- os.Signal
	var asked []os.Signal
	stopped := false
	signalNotify = func(c chan<- os.Signal, sig ...os.Signal) { caught, asked = c, sig }
	signalStop = func(chan<- os.Signal) { stopped = true }
	t.Cleanup(func() { signalNotify, signalStop = prevNotify, prevStop })

	a.startSignalWatch()
	if len(asked) != 2 || asked[0] != syscall.SIGHUP || asked[1] != syscall.SIGTERM {
		t.Fatalf("asked for %v, want SIGHUP and SIGTERM", asked)
	}
	caught <- syscall.SIGTERM
	if ev := pollFor[*signalQuitEvent](t, a); ev.sig != syscall.SIGTERM {
		t.Fatalf("forwarded %v", ev.sig)
	}

	a.teardownCrashSafety()
	if !stopped || a.crash.sigCh != nil {
		t.Fatal("teardown did not hand the signals back")
	}
}

// TestSignalLabel names the two caught signals the way a user knows them.
func TestSignalLabel(t *testing.T) {
	if got := signalLabel(syscall.SIGHUP); !strings.Contains(got, "SIGHUP") {
		t.Errorf("SIGHUP label = %q", got)
	}
	if got := signalLabel(syscall.SIGTERM); got != "SIGTERM" {
		t.Errorf("SIGTERM label = %q", got)
	}
	if got := signalLabel(nil); got != "a signal" {
		t.Errorf("nil label = %q", got)
	}
}

// TestRescueNote covers the three outcomes the exit note can report.
func TestRescueNote(t *testing.T) {
	if got := rescueNote("/r/f", 2, 2, nil); !strings.Contains(got, "2 file(s)") || !strings.Contains(got, "/r/f") {
		t.Errorf("full rescue note = %q", got)
	}
	if got := rescueNote("/r/f", 1, 2, errors.New("disk full")); !strings.Contains(got, "1 could not be: disk full") {
		t.Errorf("partial rescue note = %q", got)
	}
	if got := rescueNote("", 0, 3, errors.New("read-only")); !strings.Contains(got, "could not rescue 3") {
		t.Errorf("failed rescue note = %q", got)
	}
}
