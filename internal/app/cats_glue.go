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
	"github.com/rohanthewiz/ced/internal/theme"
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
	// catsKindTheme carries the host's palette back from a config.get
	// poll (catstheme.go), which is IO and so cannot run on the loop.
	catsKindTheme
	// catsKindRecents carries the host's frecency-ranked directory list
	// back from a path.list poll (catsfrecency.go).
	catsKindRecents
	// catsKindPanes carries the session's pane list back from a pane.list
	// poll — who the sibling agents are and what they are doing
	// (catsagents.go).
	catsKindPanes
	// catsKindNotice is the generic "a background cats call finished"
	// message: a phrase for the status bar and nothing else. Every
	// multi-step control-socket action (a split, a spawn) is a goroutine,
	// and this is how its outcome — success or the server's own error —
	// gets in front of the user without each one inventing an event kind.
	catsKindNotice
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

	theme   cats.ConfigTheme // catsKindTheme
	recents []string         // catsKindRecents
	panes   []cats.PaneInfo  // catsKindPanes
	notice  string           // catsKindNotice
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

	// hostTheme is the palette synthesized from the host's own theme, and
	// hostThemeOK says whether there is one. themePolledAt rate-limits the
	// config.get poll that produces it. See catstheme.go.
	hostTheme     theme.Spec
	hostThemeOK   bool
	themePolledAt time.Time

	// recents is the host's frecency-ranked directory list, cached
	// because the picker that shows it runs on the main loop and a
	// socket round trip does not. See catsfrecency.go.
	recents []string

	// panes is the session's pane list — who the sibling agents are and
	// what they are doing — cached for the same reason recents is, and
	// read by a DRAW call (the status-bar segment), which makes the cache
	// non-negotiable rather than merely tidy. See catsagents.go.
	panes         []cats.PaneInfo
	panesPolledAt time.Time

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
	case catsKindTheme:
		a.catsThemeArrived(e.theme)
	case catsKindRecents:
		a.cats.recents = e.recents
	case catsKindPanes:
		a.cats.panes = e.panes
	case catsKindNotice:
		if e.notice != "" {
			a.flash(e.notice)
		}
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
	// Read the host's palette straight away: theme unity that only arrives
	// after the first focus change would look like a bug on the frame the
	// user is actually looking at (catstheme.go).
	a.catsPollTheme()
	// And prime the frecency list, so the first "Recent folders…" the user
	// opens already carries the host's places rather than acquiring them
	// on the second try (catsfrecency.go).
	a.catsPollRecents()
	// Same for the sibling agents: the status bar draws from that cache,
	// so an empty one is a bar that says nothing about the agent running
	// two panes over (catsagents.go).
	a.catsPollPanes()
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
		// Two names, two consumers: pane_notify is a sibling agent asking
		// for a human, focus_changed is the stand-in for the theme_changed
		// event cats does not have yet (roadmap §5 item 3). Nothing else is
		// subscribed — an idle editor should not be woken by every title
		// change in the session.
		cats.SubscribeFilter{Events: []string{cats.EventPaneNotify, cats.EventFocusChanged}},
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
	if ev.Name == cats.EventFocusChanged {
		// The theme poll's trigger. Focus moving is the cheapest available
		// signal that a human has been driving the host, which is when a
		// theme switch happens; catsPollTheme's own rate limit is what
		// keeps a busy session from turning that into a poll loop.
		a.catsPollTheme()
		// Focus moving is also the cheapest hint that the session's shape
		// changed — a new pane, a closed one — which is what the agent
		// segment reads (catsagents.go).
		a.catsPollPanes()
		return
	}
	if ev.Name != cats.EventPaneNotify {
		return
	}
	// A notify IS an agent state change, so the pane cache behind the
	// status segment is stale by definition at this moment.
	a.catsPollPanes()
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

// -----------------------------------------------------------------------------
// The ≡ group and the leader namespace
//
// Both surfaces are DYNAMIC — empty outside cats — for the same reason:
// every row here addresses a program that is not running in a plain
// terminal, so a permanently-dimmed "Cats" section would be noise in the
// menu of every ced user who has never heard of it. Inside cats the rows
// appear, and they dim individually the ordinary way when their
// precondition (Tier 1, a saved file, a sibling agent) is missing.
// -----------------------------------------------------------------------------

// catsMenuItems is the ≡ menu's "Cats" group, spliced in by
// visibleMenuGroups. Returning nil is what hides the whole group.
func (a *App) catsMenuItems() []menuItemDef {
	if !a.cats.caps.InCats {
		return nil
	}
	return []menuItemDef{
		// The splits (catssplit.go). Arrows rather than "right"/"down"
		// because the menu is scanned, not read, and the arrow says which
		// half of the screen the second editor lands in.
		{label: "Open in split →", shortcut: "esc C r", action: (*App).catsSplitRight, enabled: (*App).hasCatsSplit},
		{label: "Open in split ↓", shortcut: "esc C d", action: (*App).catsSplitDown, enabled: (*App).hasCatsSplit},
		// The frecency story's second half (catsfrecency.go). Its first
		// half needs no row of its own: the host's places are merged into
		// ≡ → File → Recent folders…, where a user already looks for them.
		{label: "Open project in new cats tab…", shortcut: "esc C p", action: (*App).menuCatsOpenProject, enabled: (*App).hasCatsProjects},
		// The sibling agents (catsagents.go). Focus first — it is the row
		// with no precondition beyond an agent existing, and the one the
		// status-bar segment's click duplicates.
		{label: "Focus agent pane…", shortcut: "esc C a", action: (*App).catsFocusAgent, enabled: (*App).hasCatsAgents},
		{label: "Send selection to agent…", shortcut: "esc C s", action: (*App).menuCatsSendSelection, enabled: (*App).hasCatsSelectionSend},
		{label: "Ask cats chat about selection", shortcut: "esc C k", action: (*App).menuCatsAskChat, enabled: (*App).hasCatsSelectionSend},
	}
}

// catsLeaderBindings is the Esc-C namespace's contents — the same
// vocabulary as the ≡ group, since a leader key that does something the
// menu cannot is a leader key nobody discovers.
//
// Dynamic (subFor) rather than static, so that outside cats the namespace
// arms NOTHING and the next keystroke reaches the editor untouched.
func catsLeaderBindings(a *App) []leaderBinding {
	if !a.cats.caps.InCats {
		return nil
	}
	return []leaderBinding{
		{key: 'r', action: (*App).catsSplitRight, label: "Split right"},
		{key: 'd', action: (*App).catsSplitDown, label: "Split down"},
		{key: 'p', action: (*App).menuCatsOpenProject, label: "Project in new tab"},
		{key: 'a', action: (*App).catsFocusAgent, label: "Focus agent pane"},
		{key: 's', action: (*App).menuCatsSendSelection, label: "Send selection to agent"},
		// 'k' rather than 'c' for chat: 'c' would be the obvious letter,
		// and it is exactly the letter a user who meant Esc-c (code
		// actions) would hit after a stray Shift. 'k' is what the flat
		// table already uses for "talk to something" (the palette).
		{key: 'k', action: (*App).menuCatsAskChat, label: "Ask cats chat"},
	}
}

// catsLeaderHint is the one-line menu flashed when Esc-C arms. Outside
// cats it is the namespace's only explanation of why it is empty — which
// is the whole reason an empty namespace still flashes something.
func catsLeaderHint(a *App) string {
	if !a.cats.caps.InCats {
		return "Cats — ced is not running in a cats pane"
	}
	if !a.catsTier1() {
		return "Cats — the host's control socket is not answering"
	}
	return "Cats  r split → · d split ↓ · p project tab · a focus agent · s send selection · k ask chat"
}

// catsPostNotice hands one phrase from a background cats goroutine to the
// status bar. It is the ONLY thing such a goroutine may do with a result —
// the events-only rule — so every multi-step control-socket action ends in
// a call to this rather than in a flash it is not on the right thread to
// perform.
func catsPostNotice(scr tcell.Screen, msg string) {
	if scr == nil || msg == "" {
		return
	}
	_ = scr.PostEvent(&catsEvent{when: time.Now(), kind: catsKindNotice, notice: msg})
}

// catsTier1 reports whether the full integration is available right now.
// The single question every future call site asks before choosing between a
// cats path and its Tier-0 fallback.
func (a *App) catsTier1() bool { return a.cats.client != nil && a.cats.caps.Tier1() }

// compile-time check that catsEvent is a tcell event, so a missing When()
// fails here rather than at the PostEvent call site.
var _ tcell.Event = (*catsEvent)(nil)
