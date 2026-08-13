// =============================================================================
// File: internal/app/cats_glue.go
// Author: Rohan Allison <rohanthewiz@gmail.com>
// Created: 2026-08-12
// Copyright: 2026 Rohan Allison. All rights reserved.
// =============================================================================

// The cats integration's app-side seam: everything that knows both about
// internal/cats (sockets, wire shapes) and about App (tabs, modals, the
// status bar). The transport lives in the package; the policy lives here, so
// internal/app gains one file rather than a protocol.
//
// THREE THINGS THIS FILE OWNS
//
//  1. Tier detection. The env sniff is free and runs inline at startup; the
//     socket probe is IO and runs on a goroutine that posts its answer back
//     as a catsEvent. Until that lands the editor is at Tier 0 — which is
//     not a special case, it is what ced is in every other terminal.
//
//  2. The hook reporter (the "editor can page you" half). ced reports idle /
//     working / blocked for its own pane, and cats turns that into a sidebar
//     badge, a toast, a native notification, or a phone push with no further
//     work here. The reporting rule is one line long: report on CHANGE, from
//     the after-event slot, so the transitions are exactly the ones a human
//     would describe.
//
//  3. The event stream in the other direction — today, one consumer: a
//     sibling agent in another pane that needs attention gets said in ced's
//     status bar. The exact reciprocal of (2), and the reason both exist: an
//     editor you can't see is useless as an alarm, and an alarm you can only
//     see by leaving the editor is one you'll miss.
//
// WHAT "BLOCKED" MEANS, AND WHY IT IS MARKED BY HAND
//
// Not "a modal is open". The user who just pressed Rename knows there is a
// prompt on screen; paging them about it would be noise, and noise is how a
// notification channel gets muted. Blocked means the editor raised a
// question the user did NOT ask for — a file changed underneath them, an
// agent wants permission, a formatter wants trust, a cherry-pick stopped.
// Those sites call catsAsking with a phrase; nothing else does. The mark
// clears itself when the modal slot empties (catsAfterEvent), so no site has
// to remember the other half.

package app

import (
	"encoding/json"
	"time"

	"github.com/gdamore/tcell/v2"

	"github.com/rohanthewiz/ced/internal/cats"
)

// catsEventKind discriminates the payload of a catsEvent. One event type for
// the whole integration keeps app.go's switch to a single case; the dispatch
// on kind happens here, where the integration already lives.
type catsEventKind int

const (
	// catsKindReady carries the completed capability probe (plus the
	// resolved id of our own pane, when it could be resolved).
	catsKindReady catsEventKind = iota
	// catsKindLink reports the event stream connecting or dropping — the
	// only way to learn that cats died AFTER startup said Tier 1.
	catsKindLink
	// catsKindFrame carries one subscribed event from the stream.
	catsKindFrame
)

// catsEvent is the goroutine → main-loop message for everything cats. The
// background goroutines in internal/cats hand their results to callbacks
// whose entire body is one PostEvent of this; the main loop is still the
// only thing that touches App.
type catsEvent struct {
	when time.Time
	kind catsEventKind

	caps   cats.Caps // catsKindReady
	self   uint32    // catsKindReady: our own pane's internal id
	selfOK bool

	up bool // catsKindLink

	frame cats.Event // catsKindFrame
}

// When satisfies the tcell.Event interface.
func (e *catsEvent) When() time.Time { return e.when }

// catsState is the App's whole cats-integration state. The zero value is
// Tier 0 with no reporter, which is what an editor outside cats has and what
// every failure degrades to.
type catsState struct {
	// caps is the last known capability set. Control is refreshed by link
	// events, so it stays honest about a cats that went away mid-session.
	caps cats.Caps

	// client is the control-socket client, nil below Tier 1. Every call
	// site reads it as `if a.cats.client != nil { … } else { fallback }`.
	client *cats.Client

	// stream is the live event subscription, nil below Tier 1.
	stream *cats.Stream

	// hooks is the state reporter. It is deliberately independent of
	// client: the hook socket is a different address, so the editor can
	// still raise an alarm on a host whose control API is unreachable.
	hooks *cats.Reporter

	// self is our own pane's internal id. Its job is telling our own
	// reports apart from other panes' when they come back around the event
	// stream — without it, ced blocking on a prompt would notify ced about
	// ced.
	self   uint32
	selfOK bool

	// asking is why an unprompted question is on screen ("" when none).
	// See the file comment.
	asking string

	// lastState / lastStatus are what the reporter last sent. Reporting on
	// change is not just economy: every report is a potential toast or
	// phone push, and re-sending "working" on each keystroke would turn the
	// channel into a firehose nobody keeps enabled.
	lastState  string
	lastStatus string
}

// catsInit runs at startup: sniff the environment, arm the hook reporter if
// the address is there, and schedule the socket probe. Returns immediately —
// nothing here dials.
func (a *App) catsInit() {
	a.cats.caps = cats.DetectEnv()
	if !a.cats.caps.InCats {
		return
	}
	// The reporter needs only the hook socket and the pane handle, both of
	// which came from the environment, so it is live before the probe —
	// and stays live even if the probe says Tier 0.
	a.cats.hooks = cats.NewReporter(a.cats.caps.HookSocket, a.cats.caps.PaneHandle)
	if a.cats.caps.ControlSocket == "" || a.screen == nil {
		return
	}
	caps, scr := a.cats.caps, a.screen
	go func() {
		caps = caps.Probe()
		var self uint32
		selfOK := false
		if caps.Tier1() {
			// Resolve which pane we are while we are already off the main
			// loop: the public handle in the environment ("w1:p3") is not
			// the id control commands take, and every self-addressing
			// command needs the translation.
			if id, err := cats.NewClient(caps.ControlSocket).ResolvePane(caps.PaneHandle); err == nil {
				self, selfOK = id, true
			}
		}
		_ = scr.PostEvent(&catsEvent{
			when: time.Now(), kind: catsKindReady,
			caps: caps, self: self, selfOK: selfOK,
		})
	}()
}

// handleCatsEvent is the main-loop side of the integration: the one case
// app.go's switch gains.
func (a *App) handleCatsEvent(e *catsEvent) {
	switch e.kind {
	case catsKindReady:
		a.catsReady(e)
	case catsKindLink:
		// A dropped stream means the host is gone for now, so Tier-1 paths
		// stop being offered; the stream's own reconnect loop flips it back
		// when cats returns. The client is NOT torn down — a call against a
		// dead socket fails harmlessly, and rebuilding one on every blip
		// would be churn for no gain.
		a.cats.caps.Control = e.up
	case catsKindFrame:
		a.catsFrame(e.frame)
	}
}

// catsReady installs the probe's verdict. At Tier 1 it builds the control
// client and subscribes; below it, nothing happens at all — which is the
// entire Tier-0 code path.
func (a *App) catsReady(e *catsEvent) {
	a.cats.caps = e.caps
	a.cats.self, a.cats.selfOK = e.self, e.selfOK
	if !e.caps.Tier1() {
		return
	}
	a.cats.client = cats.NewClient(e.caps.ControlSocket)
	a.catsSubscribe()
}

// catsSubscribe starts the event stream, filtered to the events ced acts on.
// The filter is applied server-side, so an editor that cares about one event
// is not woken by every title change in the session.
func (a *App) catsSubscribe() {
	if a.cats.stream != nil || a.screen == nil {
		return
	}
	scr := a.screen
	a.cats.stream = cats.Subscribe(
		a.cats.caps.ControlSocket,
		cats.SubscribeFilter{Events: []string{cats.EventPaneNotify}},
		func(ev cats.Event) {
			_ = scr.PostEvent(&catsEvent{when: time.Now(), kind: catsKindFrame, frame: ev})
		},
		func(up bool, _ error) {
			_ = scr.PostEvent(&catsEvent{when: time.Now(), kind: catsKindLink, up: up})
		},
	)
}

// catsFrame handles one streamed event. Unknown names are ignored: the
// server's vocabulary can grow past this build's, and the filter means
// anything arriving here was asked for by an older or newer version of this
// same list.
func (a *App) catsFrame(ev cats.Event) {
	if ev.Name != cats.EventPaneNotify {
		return
	}
	var p cats.PaneNotifyEvent
	if err := json.Unmarshal(ev.Data, &p); err != nil {
		return
	}
	if a.cats.selfOK && p.Pane == a.cats.self {
		return // our own report, come back around the loop
	}
	if msg := catsNotifyMessage(p); msg != "" {
		a.flash(msg)
	}
}

// catsNotifyMessage phrases a sibling pane's notification for ced's status
// bar. cats' own message is preferred when it sent one — it is written for a
// human and knows more about the agent than we do; the fallbacks cover the
// two kinds it can send.
func catsNotifyMessage(p cats.PaneNotifyEvent) string {
	who := p.Agent
	if who == "" {
		who = "an agent"
	}
	switch {
	case p.Message != "":
		return who + ": " + p.Message
	case p.Kind == "attention":
		return who + " needs attention"
	case p.Kind == "finished":
		return who + " finished"
	}
	return ""
}

// catsAsking records why the editor is raising a question the user did not
// ask for. Call it AFTER opening the modal — openModal dismisses whatever
// was up first, and that path clears this mark.
//
// The phrase is what shows on the phone, so write it as one: "disk conflict",
// not "modal open".
func (a *App) catsAsking(reason string) { a.cats.asking = reason }

// catsAfterEvent is the reporting pump, run in the after-event slot beside
// the other reconcilers. Two compares when nothing changed.
func (a *App) catsAfterEvent() {
	if a.cats.hooks == nil {
		return
	}
	// The mark's other half: an empty modal slot means whatever we were
	// asking about has been answered or dismissed. Clearing here rather
	// than at each dismissal path is what keeps the mark from getting stuck
	// on — a stale "blocked" would train the user to ignore the real one.
	if a.modal == nil {
		a.cats.asking = ""
	}
	state, status := a.catsSelfState()
	if state == a.cats.lastState && status == a.cats.lastStatus {
		return
	}
	a.cats.lastState, a.cats.lastStatus = state, status
	a.cats.hooks.ReportState(state, status)
}

// catsSelfState describes what the editor is doing right now, in the hook
// API's vocabulary, with a short phrase for the badge.
//
// Order is priority order: a question waiting on a human outranks work in
// flight, because it is the only state that will not resolve itself.
func (a *App) catsSelfState() (state, status string) {
	if a.modal != nil && a.cats.asking != "" {
		return cats.StateBlocked, a.cats.asking
	}
	if a.chat.turnActive {
		return cats.StateWorking, a.chatAgent().name + " is working"
	}
	if a.projectSearchActive > 0 {
		return cats.StateWorking, "searching the project"
	}
	// Idle carries no phrase: the pane's title (OSC 2, hostident.go)
	// already names the file, and a badge repeating it would be one more
	// thing to keep in sync for no added information.
	return cats.StateIdle, ""
}

// catsClose tears the integration down on the way out: stop the stream
// first, so no callback can post onto a screen being finalized, then hand
// the pane back so it stops being labeled "ced" the moment we exit.
//
// Both calls are nil-safe, which is the Tier-0 path.
func (a *App) catsClose() {
	a.cats.stream.Close()
	a.cats.stream = nil
	a.cats.hooks.Release()
}

// catsTier1 reports whether the full integration is available right now.
// The single question every future call site asks before choosing between a
// cats path and its Tier-0 fallback.
func (a *App) catsTier1() bool { return a.cats.client != nil && a.cats.caps.Tier1() }

// compile-time check that catsEvent is a tcell event, so a missing When()
// fails here rather than at the PostEvent call site.
var _ tcell.Event = (*catsEvent)(nil)
