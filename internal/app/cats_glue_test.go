// =============================================================================
// File: internal/app/cats_glue_test.go
// Author: Rohan Allison <rohanthewiz@gmail.com>
// Created: 2026-08-12
// Copyright: 2026 Rohan Allison. All rights reserved.
// =============================================================================

package app

import (
	"bufio"
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/rohanthewiz/ced/internal/cats"
)

// -----------------------------------------------------------------------------
// Scaffolding
// -----------------------------------------------------------------------------

// hookSpy is a fake cats hook socket that records every state report the
// editor sends. Reports are what this file is about — the App's job is to
// decide WHICH transitions are worth a badge, so the assertions are on the
// sequence that reaches the socket, not on internal fields.
type hookSpy struct {
	t    *testing.T
	path string
	got  chan hookReport
}

// hookReport is the slice of a report these tests assert on.
type hookReport struct {
	Method string `json:"method"`
	Params struct {
		State        string `json:"state"`
		CustomStatus string `json:"custom_status"`
		PaneID       string `json:"pane_id"`
		Source       string `json:"source"`
	} `json:"params"`
}

// startHookSpy binds a fake hook socket. The path is kept short because
// unix socket paths are capped at ~104 bytes by the kernel and macOS's
// t.TempDir() spends most of that budget before we get to the file name.
func startHookSpy(t *testing.T) *hookSpy {
	t.Helper()
	base := os.TempDir()
	if fi, err := os.Stat("/tmp"); err == nil && fi.IsDir() {
		base = "/tmp"
	}
	dir, err := os.MkdirTemp(base, "cedhook")
	if err != nil {
		t.Fatalf("temp dir: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })

	s := &hookSpy{t: t, path: filepath.Join(dir, "h"), got: make(chan hookReport, 32)}
	ln, err := net.Listen("unix", s.path)
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go func() {
				defer conn.Close()
				line, err := bufio.NewReader(conn).ReadBytes('\n')
				if err != nil && len(line) == 0 {
					return
				}
				var r hookReport
				if err := json.Unmarshal(line, &r); err != nil {
					return
				}
				select {
				case s.got <- r:
				default:
				}
			}()
		}
	}()
	return s
}

// next returns the next report, failing the test if none arrives.
func (s *hookSpy) next() hookReport {
	s.t.Helper()
	select {
	case r := <-s.got:
		return r
	case <-time.After(2 * time.Second):
		s.t.Fatal("no state report reached the hook socket")
		return hookReport{}
	}
}

// quiet asserts that nothing more was reported — the property that keeps a
// notification channel usable.
func (s *hookSpy) quiet(t *testing.T, why string) {
	t.Helper()
	select {
	case r := <-s.got:
		t.Fatalf("unexpected report (%s): state=%q status=%q", why, r.Params.State, r.Params.CustomStatus)
	case <-time.After(120 * time.Millisecond):
	}
}

// withHookSpy arms an App's reporter against a fake hook socket, as if the
// editor had started inside a cats pane.
func withHookSpy(t *testing.T, a *App) *hookSpy {
	t.Helper()
	s := startHookSpy(t)
	a.cats.caps = cats.Caps{InCats: true, Hooks: true, PaneHandle: "w1:p3", HookSocket: s.path}
	a.cats.hooks = cats.NewReporter(s.path, "w1:p3")
	if a.cats.hooks == nil {
		t.Fatal("reporter should exist with both halves of the address")
	}
	return s
}

// -----------------------------------------------------------------------------
// State reporting
// -----------------------------------------------------------------------------

// The editor's state is reported on TRANSITIONS only. Every report is a
// potential toast or phone push, so a second pass with nothing changed must
// send nothing at all.
func TestCatsReportsOnlyOnChange(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	s := withHookSpy(t, a)

	a.catsAfterEvent()
	if got := s.next(); got.Params.State != cats.StateIdle {
		t.Fatalf("first report = %q, want idle", got.Params.State)
	}
	a.catsAfterEvent()
	a.catsAfterEvent()
	s.quiet(t, "nothing changed")
}

// A question the user did NOT ask for blocks the editor and says so in one
// phrase. This is the payload of the whole feature: the phrase is what
// shows up on a phone.
func TestCatsReportsBlockedOnUnpromptedQuestion(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	s := withHookSpy(t, a)
	a.catsAfterEvent()
	s.next() // the initial idle

	a.openConfirm("Trust this project's formatter?", "…", func(app *App) {})
	a.catsAsking("formatter trust")
	a.catsAfterEvent()

	got := s.next()
	if got.Params.State != cats.StateBlocked || got.Params.CustomStatus != "formatter trust" {
		t.Fatalf("got state=%q status=%q", got.Params.State, got.Params.CustomStatus)
	}
	if got.Params.PaneID != "w1:p3" || got.Params.Source != "ced" {
		t.Fatalf("identity = %+v", got.Params)
	}

	// Answering it returns the editor to idle — the transition that stops
	// the badge, and the reason the mark clears itself.
	a.closeModal()
	a.catsAfterEvent()
	if got := s.next(); got.Params.State != cats.StateIdle {
		t.Fatalf("after the answer: %q", got.Params.State)
	}
	if a.cats.asking != "" {
		t.Fatalf("the mark is stuck on: %q", a.cats.asking)
	}
}

// A modal the user asked for is NOT blocked. They pressed Rename; they know
// there is a prompt on screen. Paging them about it is the noise that gets
// a notification channel muted.
func TestCatsUserAskedModalIsNotBlocked(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	s := withHookSpy(t, a)
	a.catsAfterEvent()
	s.next()

	a.openPrompt("Rename", "", "x.go", func(app *App, v string) {})
	a.catsAfterEvent()
	s.quiet(t, "a user-initiated prompt is not an alarm")

	if state, _ := a.catsSelfState(); state != cats.StateIdle {
		t.Fatalf("state = %q", state)
	}
}

// Long work reports "working" so a user who walked away learns when it
// ends, and the two sources of it are ranked below a waiting question —
// which is the only state that will not resolve itself.
func TestCatsWorkingStates(t *testing.T) {
	a := newTestApp(t, t.TempDir())

	a.projectSearchActive = 1
	state, status := a.catsSelfState()
	if state != cats.StateWorking || status != "searching the project" {
		t.Fatalf("search: %q / %q", state, status)
	}

	a.chat.turnActive = true
	if state, status = a.catsSelfState(); state != cats.StateWorking || status == "searching the project" {
		t.Fatalf("a chat turn should outrank the search: %q / %q", state, status)
	}

	// A waiting question outranks both.
	a.openConfirm("Disk conflict", "…", func(app *App) {})
	a.catsAsking("file changed on disk")
	if state, status = a.catsSelfState(); state != cats.StateBlocked || status != "file changed on disk" {
		t.Fatalf("blocked should win: %q / %q", state, status)
	}
}

// Idle carries no phrase: the pane title already names the file, and a
// badge repeating it would be a second copy to keep in sync.
func TestCatsIdleHasNoPhrase(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	state, status := a.catsSelfState()
	if state != cats.StateIdle || status != "" {
		t.Fatalf("got %q / %q", state, status)
	}
}

// A mark left behind by a modal that has since closed must not stick: a
// permanent "blocked" would train the user to ignore the real one.
func TestCatsAskingClearsWithTheModal(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	a.cats.hooks = cats.NewReporter("/nonexistent-cats-hook.sock", "p_1")
	a.catsAsking("file changed on disk")
	if a.modal != nil {
		t.Fatal("no modal expected")
	}
	a.catsAfterEvent()
	if a.cats.asking != "" {
		t.Fatalf("stale mark survived: %q", a.cats.asking)
	}
}

// Outside cats there is no reporter, and the after-event hook is a single
// nil check — the Tier-0 path taken by every editor in every other
// terminal.
func TestCatsAfterEventIsANoopAtTier0(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	if a.cats.hooks != nil {
		t.Fatal("a hand-built App must not have a reporter")
	}
	a.catsAfterEvent() // must not panic
	if a.catsTier1() {
		t.Fatal("Tier 1 without a client")
	}
}

// -----------------------------------------------------------------------------
// Detection and the event stream
// -----------------------------------------------------------------------------

// catsInit outside a cats pane does nothing at all: no reporter, no probe,
// no goroutine.
func TestCatsInitOutsideCats(t *testing.T) {
	for _, k := range []string{cats.EnvMarker, cats.EnvPaneID, cats.EnvControlSocket, cats.EnvHookSocket} {
		t.Setenv(k, "")
	}
	a := newTestApp(t, t.TempDir())
	a.catsInit()
	if a.cats.hooks != nil || a.cats.client != nil || a.cats.caps.InCats {
		t.Fatalf("expected Tier 0, got %+v", a.cats.caps)
	}
}

// Inside a pane whose control socket is unreachable, the HOOK reporter is
// still armed. The two sockets are separate addresses, and the attention
// story is the half that matters when you are not looking at the screen.
func TestCatsInitArmsHooksWithoutControl(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	s := startHookSpy(t)
	t.Setenv(cats.EnvMarker, "1")
	t.Setenv(cats.EnvPaneID, "w1:p3")
	t.Setenv(cats.EnvControlSocket, "")
	t.Setenv(cats.EnvHookSocket, s.path)

	a.catsInit()
	if a.cats.hooks == nil {
		t.Fatal("no reporter, but the hook socket is right there")
	}
	if a.catsTier1() {
		t.Fatal("no control socket must not reach Tier 1")
	}
	a.catsAfterEvent()
	if got := s.next(); got.Params.State != cats.StateIdle {
		t.Fatalf("state = %q", got.Params.State)
	}
}

// A probe that came back below Tier 1 installs no client and subscribes to
// nothing — the silent-degradation contract.
func TestCatsReadyBelowTier1(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	a.handleCatsEvent(&catsEvent{
		kind: catsKindReady,
		caps: cats.Caps{InCats: true, Reason: "control socket not answering"},
	})
	if a.cats.client != nil || a.cats.stream != nil || a.catsTier1() {
		t.Fatal("a failed probe must leave the integration off")
	}
}

// A dropped stream is how ced learns cats died AFTER startup said Tier 1 —
// the only way it can learn that at all. Tier-1 paths stop being offered
// until the stream's own reconnect brings it back.
func TestCatsLinkDropLeavesTier1(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	a.cats.caps = cats.Caps{InCats: true, Control: true, ControlSocket: "/x.sock", PaneHandle: "p_1"}
	a.cats.client = cats.NewClient("/x.sock")
	if !a.catsTier1() {
		t.Fatal("setup should be Tier 1")
	}

	a.handleCatsEvent(&catsEvent{kind: catsKindLink, up: false})
	if a.catsTier1() {
		t.Fatal("a dropped link must drop Tier 1")
	}
	// The client survives: a call against a dead socket fails harmlessly,
	// and rebuilding one on every blip would be churn for no gain.
	if a.cats.client == nil {
		t.Fatal("the client should not be torn down by a blip")
	}
	a.handleCatsEvent(&catsEvent{kind: catsKindLink, up: true})
	if !a.catsTier1() {
		t.Fatal("a recovered link must restore Tier 1")
	}
}

// A sibling agent that needs attention is said in ced's status bar. The
// exact reciprocal of the hook reporter: an alarm you can only see by
// leaving the editor is one you will miss.
func TestCatsNotifyFlashesForSiblingPane(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	a.cats.self, a.cats.selfOK = 3, true

	raw, err := json.Marshal(cats.PaneNotifyEvent{Pane: 7, Agent: "claude", Kind: "attention"})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	a.handleCatsEvent(&catsEvent{
		kind:  catsKindFrame,
		frame: cats.Event{Name: cats.EventPaneNotify, Data: raw},
	})
	if a.statusMsg == "" {
		t.Fatal("a sibling agent needing attention should reach the status bar")
	}
	want := "claude needs attention"
	if a.statusMsg != want {
		t.Fatalf("status = %q, want %q", a.statusMsg, want)
	}
}

// Our OWN report coming back around the stream is dropped. Without the
// self-check, ced blocking on a prompt would notify ced about ced.
func TestCatsNotifyIgnoresOwnPane(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	a.cats.self, a.cats.selfOK = 7, true
	a.statusMsg = ""

	raw, err := json.Marshal(cats.PaneNotifyEvent{Pane: 7, Agent: "ced", Kind: "attention"})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	a.handleCatsEvent(&catsEvent{
		kind:  catsKindFrame,
		frame: cats.Event{Name: cats.EventPaneNotify, Data: raw},
	})
	if a.statusMsg != "" {
		t.Fatalf("echoed our own notification: %q", a.statusMsg)
	}
}

// cats' own message wins when it sent one — it is written for a human and
// knows more about the agent than we do — and each kind has a fallback.
func TestCatsNotifyMessage(t *testing.T) {
	cases := []struct {
		in   cats.PaneNotifyEvent
		want string
	}{
		{cats.PaneNotifyEvent{Agent: "claude", Kind: "attention", Message: "needs your input"}, "claude: needs your input"},
		{cats.PaneNotifyEvent{Agent: "codex", Kind: "attention"}, "codex needs attention"},
		{cats.PaneNotifyEvent{Agent: "codex", Kind: "finished"}, "codex finished"},
		{cats.PaneNotifyEvent{Kind: "attention"}, "an agent needs attention"},
		{cats.PaneNotifyEvent{Kind: "something-new"}, ""},
	}
	for _, c := range cases {
		if got := catsNotifyMessage(c.in); got != c.want {
			t.Errorf("catsNotifyMessage(%+v) = %q, want %q", c.in, got, c.want)
		}
	}
}

// An unknown event name is ignored rather than mishandled: the server's
// vocabulary can grow past this build's.
func TestCatsUnknownFrameIgnored(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	a.statusMsg = ""
	a.handleCatsEvent(&catsEvent{
		kind:  catsKindFrame,
		frame: cats.Event{Name: "some_future_event", Data: json.RawMessage(`{"pane":1}`)},
	})
	if a.statusMsg != "" {
		t.Fatalf("acted on an unknown event: %q", a.statusMsg)
	}
}

// Shutdown hands the pane back, so it stops being labeled "ced" the moment
// the editor exits instead of wearing a stale badge until something else
// claims it. Nil-safe, since Close runs on the Tier-0 path too.
func TestCatsCloseReleasesThePane(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	s := withHookSpy(t, a)
	a.catsClose()
	if got := s.next(); got.Method != "pane.release_agent" {
		t.Fatalf("method = %q", got.Method)
	}

	b := newTestApp(t, t.TempDir())
	b.catsClose() // no reporter, no stream: must not panic
}
