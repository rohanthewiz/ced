// =============================================================================
// File: internal/app/remote.go
// Author: Rohan Allison <rohanthewiz@gmail.com>
// Created: 2026-08-03
// Copyright: 2026 Rohan Allison. All rights reserved.
// =============================================================================

// remote.go is the editor's half of `ced --remote` / `ced --wait`: the
// listening socket a second ced can hand a file to, and the bookkeeping
// that lets a `--wait` client block until the editor is finished with
// it. The transport, the discovery rules and the wire format all live in
// internal/remote; this file is the bridge between that goroutine world
// and the main loop.
//
// WHY IT EXISTS. The editor's whole premise is one instance per project
// inside tmux. Without this, `EDITOR=ced` in another pane starts a
// SECOND editor nested inside the terminal panel of the first — a
// full-screen TUI inside a REPL strip, which is exactly the thing the
// terminal panel is documented as not being. With it, `git commit` in
// pane 2 opens its message as a tab in pane 1 and blocks until you close
// that tab.
//
// TWO CONTRACTS CARRIED OVER WHOLESALE:
//
//   - Events only. The server calls serveRemoteOpen on its own
//     connection goroutine; that function posts an event carrying a
//     buffered reply channel and waits for the main loop's answer. Only
//     main-loop handlers touch App. This is the ACP permission-request
//     shape (copilot_chat_perm.go), for the same reason: the handler has
//     to BLOCK on a decision the loop makes.
//   - Silent degradation. No runtime directory, an over-long socket
//     path, a read-only /run — the editor starts normally and remote
//     open is simply off. The reason is held on the state for the ≡
//     label rather than flashed, because a startup flash scrolls past
//     before anyone looks (the plugin-loader rule).
//
// EVERY WAITER IS RELEASED EXACTLY ONCE, and there are only three things
// that release one: the tab closing, the editor exiting, and the
// preference being switched off. A `--wait` client that is never
// released is a shell prompt that never comes back, so releaseRemote is
// the single write path and it deletes as it closes.

package app

import (
	"errors"
	"fmt"
	"path/filepath"
	"time"

	"github.com/rohanthewiz/ced/internal/remote"
	"github.com/rohanthewiz/ced/internal/userconfig"
)

// remoteListenFn is the seam tests use to avoid binding a real socket in
// the developer's runtime directory. A package var for the same reason
// pluginsDirFn and sessionStatePathFn are: startRemote is reachable from
// the ≡ toggle, and a test that flips it must not leave a live socket
// behind that a real client could then find.
var remoteListenFn = remote.Listen

// remoteAnswerTimeout bounds how long a connection goroutine waits for
// the main loop to answer its open request. It only expires when the
// loop is genuinely stuck (a modal mid-drag on a wedged terminal), and
// when it does, the client gets a readable refusal and falls back to
// starting its own editor — which is a far better outcome than a git
// commit that hangs.
const remoteAnswerTimeout = 5 * time.Second

// remoteState is everything the feature keeps on the App.
type remoteState struct {
	// enabled mirrors the persisted "remote" preference (default on).
	// Authoritative — the listener is derived from it, never the other
	// way round, so the ≡ toggle has one thing to write.
	enabled bool

	// srv is the live listener, or nil when the feature is off or the
	// socket could not be created. err holds the reason in the second
	// case so the ≡ row can say WHY instead of silently reading "off".
	srv *remote.Server
	err string

	// waiters maps an absolute file path to the channels of every client
	// blocked on it. A slice because two clients can legitimately wait on
	// one file (two panes editing the same commit message is unusual but
	// not wrong), and closing a channel is what wakes them.
	waiters map[string][]chan struct{}
}

// remoteOpenEvent carries a client's open request from the connection
// goroutine to the main loop. reply is buffered so the loop can answer
// and move on even if the waiting goroutine has already timed out.
type remoteOpenEvent struct {
	when  time.Time
	path  string
	wait  bool
	reply chan remoteOpenReply
}

// When satisfies tcell.Event.
func (e *remoteOpenEvent) When() time.Time { return e.when }

// remoteOpenReply is the main loop's answer: the channel to wait on (nil
// when the client did not ask to wait), or the reason the file was
// refused.
type remoteOpenReply struct {
	done <-chan struct{}
	err  error
}

// -----------------------------------------------------------------------------
// Lifecycle
// -----------------------------------------------------------------------------

// startRemote binds this instance's socket. A no-op when the preference
// is off or a listener is already up, so it is safe to call from both
// New and the ≡ toggle.
//
// A failure is recorded, not flashed: the editor works exactly as it
// always did without a listener, and the one place the difference
// matters is the menu row, which reads the reason back.
func (a *App) startRemote() {
	if !a.remote.enabled || a.remote.srv != nil {
		return
	}
	srv, err := remoteListenFn(a.rootDir, a.serveRemoteOpen)
	if err != nil {
		a.remote.err = err.Error()
		return
	}
	a.remote.srv = srv
	a.remote.err = ""
}

// stopRemote tears the listener down and releases every client still
// waiting. Called from Close (so a quit never strands a blocked shell)
// and from the ≡ toggle. Safe to call when nothing is running.
func (a *App) stopRemote() {
	if a.remote.srv != nil {
		_ = a.remote.srv.Close()
		a.remote.srv = nil
	}
	// Release AFTER closing the listener: no new waiter can arrive once
	// the socket is gone, so this can't race with a request that lands
	// between the two statements.
	for path := range a.remote.waiters {
		a.releaseRemote(path)
	}
	a.remote.waiters = nil
}

// remoteActive reports whether this instance is currently reachable by
// `ced --remote`. Used by the menu label — it is the honest question,
// since the preference being on says nothing about whether the socket
// actually bound.
func (a *App) remoteActive() bool { return a.remote.srv != nil }

// -----------------------------------------------------------------------------
// Serving a request
// -----------------------------------------------------------------------------

// serveRemoteOpen is the internal/remote Handler. It runs on the
// connection's own goroutine and must NOT touch App — it posts the
// request and waits for the main loop, which is the only thing allowed
// to open a file.
func (a *App) serveRemoteOpen(path string, wait bool) (<-chan struct{}, error) {
	reply := make(chan remoteOpenReply, 1)
	ev := &remoteOpenEvent{when: time.Now(), path: path, wait: wait, reply: reply}
	if err := a.screen.PostEvent(ev); err != nil {
		return nil, errors.New("the editor's event queue is full")
	}
	select {
	case r := <-reply:
		return r.done, r.err
	case <-time.After(remoteAnswerTimeout):
		return nil, errors.New("the editor did not answer in time")
	}
}

// handleRemoteOpen runs on the main loop: it opens the file, registers a
// waiter when the client asked for one, and answers the connection
// goroutine.
//
// The root check is a guard, not a route. A client already refuses to
// pick an instance whose root doesn't contain the file, so this only
// ever fires on a request that didn't come from ced's own CLI — and the
// answer to that is the same as the chat filesystem's: an error the
// caller can read, never a file opened outside the workspace.
func (a *App) handleRemoteOpen(e *remoteOpenEvent) {
	answer := func(done <-chan struct{}, err error) {
		select {
		case e.reply <- remoteOpenReply{done: done, err: err}:
		default: // buffered; a full channel means the client already gave up
		}
	}
	if e.path == "" {
		answer(nil, errors.New("no path given"))
		return
	}
	if !remote.Owns(a.rootDir, e.path) {
		answer(nil, fmt.Errorf("%s is outside this editor's project", filepath.Base(e.path)))
		return
	}

	before := len(a.tabs)
	a.openFile(e.path)
	// openFile flashes its own error and leaves the tab list alone, so
	// "no new tab and nothing focused on this path" is how a failure is
	// detected without teaching openFile to return one.
	tab := a.activeTabPtr()
	opened := tab != nil && sameRemotePath(tab.Path, e.path)
	if !opened && len(a.tabs) == before {
		answer(nil, errors.New("the editor could not open that file"))
		return
	}

	if !e.wait {
		a.flash("Opened " + filepath.Base(e.path) + " (remote)")
		answer(nil, nil)
		return
	}
	// The wait contract is worth saying out loud in the status bar: the
	// client is a shell prompt that will not come back until this tab is
	// closed, and nothing else on screen would explain that.
	a.flash("Opened " + filepath.Base(e.path) + " — close the tab when you're done")
	answer(a.addRemoteWaiter(tab.Path), nil)
}

// addRemoteWaiter registers a channel against path and returns it. The
// channel is closed (never sent on) so every waiter on a path wakes from
// one release, and a double release can be made impossible by deleting
// the key at the same time.
func (a *App) addRemoteWaiter(path string) <-chan struct{} {
	if a.remote.waiters == nil {
		a.remote.waiters = make(map[string][]chan struct{})
	}
	ch := make(chan struct{})
	a.remote.waiters[path] = append(a.remote.waiters[path], ch)
	return ch
}

// releaseRemote wakes every client waiting on path and forgets them. The
// single write path for finishing a wait — closeTab and stopRemote both
// go through it, so neither can close a channel twice.
func (a *App) releaseRemote(path string) {
	if len(a.remote.waiters) == 0 {
		return
	}
	chans, ok := a.remote.waiters[path]
	if !ok {
		return
	}
	delete(a.remote.waiters, path)
	for _, ch := range chans {
		close(ch)
	}
}

// sameRemotePath compares two paths the way the remote layer does, so a
// tab opened through a symlinked directory still matches the request
// that asked for it.
func sameRemotePath(a, b string) bool {
	return remote.Owns(a, b) && remote.Owns(b, a)
}

// -----------------------------------------------------------------------------
// The ≡ row
// -----------------------------------------------------------------------------

// remoteToggleLabel names the row and answers the only question a user
// has about this feature: will `ced --remote` find me? "Unavailable" is
// deliberately distinct from "off" — one is a choice and the other is a
// problem, and collapsing them would leave a user toggling a preference
// that was already on.
func (a *App) remoteToggleLabel() string {
	switch {
	case !a.remote.enabled:
		return "Remote open: off"
	case a.remote.srv != nil:
		return "Remote open: on"
	default:
		return "Remote open: unavailable"
	}
}

// menuToggleRemote flips the preference, applies it immediately, and
// persists it. Turning it off releases any waiting client rather than
// leaving it blocked on an instance that has stopped listening.
func (a *App) menuToggleRemote() {
	a.remote.enabled = !a.remote.enabled
	if a.remote.enabled {
		a.startRemote()
		if a.remote.srv == nil {
			a.flash("Remote open unavailable: " + a.remote.err)
		} else {
			a.flash("Remote open on — other ced instances can hand files to this one")
		}
	} else {
		a.stopRemote()
		a.flash("Remote open off")
	}
	if path := sessionConfigPathFn(); path != "" {
		if err := userconfig.SaveRemote(path, a.remote.enabled); err != nil {
			a.flash("Could not save the remote setting: " + err.Error())
		}
	}
	a.closeMenu()
}
