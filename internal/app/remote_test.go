// =============================================================================
// File: internal/app/remote_test.go
// Author: Rohan Allison <rohanthewiz@gmail.com>
// Created: 2026-08-03
// Copyright: 2026 Rohan Allison. All rights reserved.
// =============================================================================

// Tests for remote.go — the main-loop half of `ced --remote` / `ced
// --wait`: opening the handed-over file, the root guard, the waiter
// registry, and the ≡ toggle.
//
// The listener itself is stubbed through remoteListenFn so no test binds
// a socket a real client could find. The transport is covered in
// internal/remote.

package app

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/rohanthewiz/ced/internal/remote"
)

// remoteOpen drives one request through the main-loop handler and hands
// back what the connection goroutine would have received.
func remoteOpen(a *App, path string, wait bool) remoteOpenReply {
	ev := &remoteOpenEvent{
		when:  time.Now(),
		path:  path,
		wait:  wait,
		reply: make(chan remoteOpenReply, 1),
	}
	a.handleRemoteOpen(ev)
	return <-ev.reply
}

// TestHandleRemoteOpen_OpensTheFile pins the basic delivery: the file
// lands in a tab and becomes active, which is the whole point — the user
// is looking at pane 1 and expects the file they just asked for.
func TestHandleRemoteOpen_OpensTheFile(t *testing.T) {
	root := t.TempDir()
	file := filepath.Join(root, "note.txt")
	if err := os.WriteFile(file, []byte("hello"), 0o644); err != nil {
		t.Fatal(err)
	}
	a := newTestApp(t, root)

	if got := remoteOpen(a, file, false); got.err != nil {
		t.Fatalf("reply err = %v, want nil", got.err)
	}
	tab := a.activeTabPtr()
	if tab == nil || filepath.Base(tab.Path) != "note.txt" {
		t.Fatalf("active tab = %v, want note.txt", tab)
	}
}

// TestHandleRemoteOpen_RefusesOutsideTheRoot pins the guard. A client
// already declines to pick a mismatched instance, so this only fires for
// a request that didn't come from ced's CLI — and the answer has to be
// the chat filesystem's answer: a readable error, never a file opened
// outside the workspace.
func TestHandleRemoteOpen_RefusesOutsideTheRoot(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	outside := filepath.Join(t.TempDir(), "elsewhere.txt")
	if err := os.WriteFile(outside, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	got := remoteOpen(a, outside, false)
	if got.err == nil {
		t.Fatal("a file outside the project must be refused")
	}
	if len(a.tabs) != 0 {
		t.Fatalf("tab count = %d, want 0 — the refused file was opened anyway", len(a.tabs))
	}
}

// TestHandleRemoteOpen_WaitReleasesWhenTheTabCloses is the --wait
// contract seen from the editor: closing the tab is what tells the
// waiting shell it can go.
func TestHandleRemoteOpen_WaitReleasesWhenTheTabCloses(t *testing.T) {
	root := t.TempDir()
	file := filepath.Join(root, "COMMIT_EDITMSG")
	if err := os.WriteFile(file, []byte("msg"), 0o644); err != nil {
		t.Fatal(err)
	}
	a := newTestApp(t, root)

	reply := remoteOpen(a, file, true)
	if reply.err != nil {
		t.Fatalf("reply err = %v, want nil", reply.err)
	}
	if reply.done == nil {
		t.Fatal("a wait request must come back with a channel to wait on")
	}
	select {
	case <-reply.done:
		t.Fatal("released before the tab was closed")
	default:
	}

	a.closeTab(a.activeTab)
	select {
	case <-reply.done:
	default:
		t.Fatal("closing the tab did not release the waiting client")
	}
}

// TestHandleRemoteOpen_NoWaitReturnsNoChannel proves a plain --remote
// open registers nothing: a waiter nobody will ever release is a shell
// prompt that never comes back.
func TestHandleRemoteOpen_NoWaitReturnsNoChannel(t *testing.T) {
	root := t.TempDir()
	file := filepath.Join(root, "a.txt")
	if err := os.WriteFile(file, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	a := newTestApp(t, root)

	if got := remoteOpen(a, file, false); got.done != nil {
		t.Fatal("a non-wait request must not register a waiter")
	}
	if len(a.remote.waiters) != 0 {
		t.Fatalf("waiters = %d, want none", len(a.remote.waiters))
	}
}

// TestStopRemote_ReleasesEveryWaiter pins the exit path: quitting the
// editor (or switching folders, which runs the same teardown) must
// unblock every client rather than leaving shells hanging in other
// panes.
func TestStopRemote_ReleasesEveryWaiter(t *testing.T) {
	root := t.TempDir()
	a := newTestApp(t, root)
	one := a.addRemoteWaiter(filepath.Join(root, "one.txt"))
	two := a.addRemoteWaiter(filepath.Join(root, "two.txt"))

	a.stopRemote()

	for i, ch := range []<-chan struct{}{one, two} {
		select {
		case <-ch:
		default:
			t.Fatalf("waiter %d was not released by stopRemote", i)
		}
	}
	if a.remote.waiters != nil {
		t.Fatal("stopRemote should forget the waiter registry")
	}
}

// TestReleaseRemote_IsIdempotent guards the one bug this shape exists to
// prevent: two releases of the same path would close a channel twice and
// panic, taking the editor down when a client hangs up at the wrong
// moment.
func TestReleaseRemote_IsIdempotent(t *testing.T) {
	root := t.TempDir()
	a := newTestApp(t, root)
	path := filepath.Join(root, "x.txt")
	ch := a.addRemoteWaiter(path)

	a.releaseRemote(path)
	a.releaseRemote(path) // must not panic
	a.releaseRemote(filepath.Join(root, "never-registered.txt"))

	select {
	case <-ch:
	default:
		t.Fatal("the waiter was not released")
	}
}

// TestAddRemoteWaiter_WakesEveryClientOnOnePath covers two panes waiting
// on the same file — unusual, but a slice of channels is only correct if
// one release wakes all of them.
func TestAddRemoteWaiter_WakesEveryClientOnOnePath(t *testing.T) {
	root := t.TempDir()
	a := newTestApp(t, root)
	path := filepath.Join(root, "shared.txt")
	first := a.addRemoteWaiter(path)
	second := a.addRemoteWaiter(path)

	a.releaseRemote(path)
	for i, ch := range []<-chan struct{}{first, second} {
		select {
		case <-ch:
		default:
			t.Fatalf("client %d was left waiting", i)
		}
	}
}

// TestServeRemoteOpen_PostsAndWaits pins the goroutine contract: the
// handler posts an event and blocks for the loop's answer rather than
// touching App from the connection's goroutine.
func TestServeRemoteOpen_PostsAndWaits(t *testing.T) {
	root := t.TempDir()
	file := filepath.Join(root, "a.txt")
	if err := os.WriteFile(file, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	a := newTestApp(t, root)

	result := make(chan error, 1)
	go func() {
		_, err := a.serveRemoteOpen(file, false)
		result <- err
	}()

	// Pump the queue the way Run would.
	deadline := time.After(2 * time.Second)
	for {
		ev := a.screen.PollEvent()
		if ev == nil {
			t.Fatal("screen closed before the request arrived")
		}
		if ro, ok := ev.(*remoteOpenEvent); ok {
			a.handleRemoteOpen(ro)
			break
		}
		select {
		case <-deadline:
			t.Fatal("the remote request never reached the loop")
		default:
		}
	}
	select {
	case err := <-result:
		if err != nil {
			t.Fatalf("serveRemoteOpen = %v, want nil", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("serveRemoteOpen did not return after the loop answered")
	}
}

// TestStartRemote_HonoursThePreference pins the kill switch at the one
// place that matters: with the preference off, nothing binds at all.
func TestStartRemote_HonoursThePreference(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	called := false
	prev := remoteListenFn
	remoteListenFn = func(root string, h remote.Handler) (*remote.Server, error) {
		called = true
		return nil, errors.New("should not be reached")
	}
	t.Cleanup(func() { remoteListenFn = prev })

	a.remote.enabled = false
	a.startRemote()
	if called {
		t.Fatal("startRemote bound a socket while the preference was off")
	}
}

// TestStartRemote_RecordsTheReasonItFailed pins the silent-degradation
// contract: a socket that can't be created costs the handoff, not the
// editor — and the reason has to survive for the ≡ row, since "off" and
// "broken" are different answers to the user's question.
func TestStartRemote_RecordsTheReasonItFailed(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	prev := remoteListenFn
	remoteListenFn = func(root string, h remote.Handler) (*remote.Server, error) {
		return nil, errors.New("read-only runtime dir")
	}
	t.Cleanup(func() { remoteListenFn = prev })

	a.remote.enabled = true
	a.startRemote()
	if a.remoteActive() {
		t.Fatal("remoteActive should be false when the listener failed")
	}
	if a.remote.err != "read-only runtime dir" {
		t.Fatalf("remote.err = %q, want the listener's reason", a.remote.err)
	}
	if got := a.remoteToggleLabel(); got != "Remote open: unavailable" {
		t.Fatalf("label = %q, want the unavailable form", got)
	}
}

// TestRemoteToggleLabel_DistinguishesOffFromUnavailable pins the three
// states apart. Collapsing "off" and "unavailable" would leave a user
// toggling a preference that was already on.
func TestRemoteToggleLabel_DistinguishesOffFromUnavailable(t *testing.T) {
	a := newTestApp(t, t.TempDir())

	a.remote.enabled = false
	if got := a.remoteToggleLabel(); got != "Remote open: off" {
		t.Errorf("disabled label = %q", got)
	}
	a.remote.enabled = true
	if got := a.remoteToggleLabel(); got != "Remote open: unavailable" {
		t.Errorf("enabled-but-unbound label = %q", got)
	}
	a.remote.srv = &remote.Server{}
	if got := a.remoteToggleLabel(); got != "Remote open: on" {
		t.Errorf("running label = %q", got)
	}
	a.remote.srv = nil
}

// TestMenuToggleRemote_PersistsAndReleases proves the ≡ row does all
// three things it owes: flip the preference, tear the listener down, and
// write the choice back — and that turning it off doesn't strand a
// client that was waiting on the instance.
func TestMenuToggleRemote_PersistsAndReleases(t *testing.T) {
	root := t.TempDir()
	a := newTestApp(t, root)
	prev := remoteListenFn
	remoteListenFn = func(string, remote.Handler) (*remote.Server, error) {
		return nil, errors.New("stubbed")
	}
	t.Cleanup(func() { remoteListenFn = prev })

	a.remote.enabled = true
	ch := a.addRemoteWaiter(filepath.Join(root, "x.txt"))

	a.menuToggleRemote()
	if a.remote.enabled {
		t.Fatal("the toggle did not flip the preference")
	}
	select {
	case <-ch:
	default:
		t.Fatal("turning remote open off left a client waiting")
	}

	cfg, err := os.ReadFile(sessionConfigPathFn())
	if err != nil {
		t.Fatalf("config not written: %v", err)
	}
	if !strings.Contains(string(cfg), `"remote"`) || !strings.Contains(string(cfg), `"off"`) {
		t.Fatalf("config.json = %s, want the remote preference persisted as off", cfg)
	}
}
