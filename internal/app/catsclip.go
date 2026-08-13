// =============================================================================
// File: internal/app/catsclip.go
// Author: Rohan Allison <rohanthewiz@gmail.com>
// Created: 2026-08-13
// Copyright: 2026 Rohan Allison. All rights reserved.
// =============================================================================

// Reading the host's clipboard — the one thing a program inside a terminal
// pane could not do at all until cats grew a verb for it.
//
// THE ASYMMETRY THIS FIXES. ced can already WRITE the system clipboard: copy
// emits OSC 52 and the terminal delivers it. That sequence is write-only by
// design, and rightly so — a terminal that answered clipboard reads would let
// anything able to print bytes exfiltrate whatever the user last copied. So
// "compare this buffer with what I just copied" has, until now, been a
// two-gesture ritual: arm the compare panel, then paste into it by hand.
// cats' clipboard.read closes the loop over the owner-only control socket,
// where the trust question has a different and much better answer.
//
// IT IS THE HOST'S CLIPBOARD, NOT NECESSARILY THE USER'S. This is the rule
// that shapes the whole file. ced's own copy goes out over OSC 52, which cats
// hands to the BROWSER's clipboard; clipboard.read reads the machine catway
// RUNS on. Those are the same clipboard in the mac app and in any local
// session — and two different machines the moment someone drives a remote
// catway from a laptop.
//
// Everything here therefore follows from one refusal: **nothing reads the host
// clipboard unless the user asked, in that moment, for the host clipboard.**
// In particular ced's internal clipboard is never refreshed in the background.
// An ambient sync would be delightful nine times out of ten and, the tenth,
// would silently replace what the user copied in their browser with whatever
// is sitting on a headless build server — a clobber they did not ask for, of
// state they cannot see, discovered only when they paste.
//
// The two verbs are the two shapes of "I copied something elsewhere":
//
//	Compare with clipboard   the §4 Tier-1 gesture — the panel opens already
//	                         populated, where Tier 0 arms it and waits
//	Paste from host clipboard  the text at the caret, for everything that is
//	                         not a diff
//
// Both are async (a socket round trip off the loop, the answer posted back),
// which for the paste means the caret could have moved in between. It refuses
// to insert into a buffer that moved rather than guessing — see catsClipPaste.

package app

import (
	"strings"
	"time"

	"github.com/gdamore/tcell/v2"

	"github.com/rohanthewiz/ced/internal/editor"
)

// catsClipUse says what a completed clipboard read is for. The read itself is
// one call with one shape of answer; what differs is where the text lands, and
// carrying that on the request is what lets both verbs share one goroutine and
// one arrival path.
type catsClipUse int

const (
	catsClipCompare catsClipUse = iota // the compare panel's old side
	catsClipPasteAt                    // inserted at the caret
)

// -----------------------------------------------------------------------------
// Compare with the host clipboard (§4 Tier 1)
// -----------------------------------------------------------------------------

// menuCatsCompareClipboard diffs the active buffer against the host clipboard,
// with no gestures at all: the panel opens already showing the diff.
//
// Below Tier 1 it does NOT dim. It falls through to the Tier-0 path — arm the
// panel and wait for a paste — because that path is a real feature rather than
// a consolation, and a row that vanishes outside cats would teach the user that
// the editor is less capable than it is. The only difference the user sees is
// whether they have to press ⌘V.
func (a *App) menuCatsCompareClipboard() {
	a.closeMenu()
	if !a.hasComparable() {
		a.flash("Nothing to compare — open a file first")
		return
	}
	if !a.catsTier1() {
		a.catsClipCompareTier0()
		return
	}
	a.catsClipRead(catsClipCompare)
	a.flash("Reading the host clipboard…")
}

// catsClipCompareTier0 is the fallback: ced's own clipboard when it holds
// something, otherwise the armed-paste ritual.
//
// The internal clipboard is tried FIRST because it is the honest answer to
// "compare with what I copied" whenever the copy happened in this editor — it
// needs no host, no gesture and no round trip, and outside cats it is the only
// clipboard ced can see at all.
func (a *App) catsClipCompareTier0() {
	if a.clipBuf != "" {
		if a.compare.selPending != nil {
			a.compareTextWithSelection("clipboard", a.clipBuf)
			return
		}
		a.compareWithText("clipboard", a.clipBuf)
		return
	}
	a.menuComparePaste()
}

// -----------------------------------------------------------------------------
// Paste the host clipboard at the caret
// -----------------------------------------------------------------------------

// menuCatsPasteClipboard inserts the host clipboard's text at the caret.
//
// It is a SEPARATE verb rather than a smarter ⌘V, and that is the deliberate
// half. ⌘V is synchronous, instant and pastes exactly what ced last copied;
// making it dial a socket would put a round trip — and, on a wedged host,
// seconds — inside the most reflexive keystroke in the editor, to fetch a
// clipboard that on a remote catway is not even the one the user copied into.
// This row asks for that specific place, by name, when the user means it.
func (a *App) menuCatsPasteClipboard() {
	a.closeMenu()
	if !a.catsTier1() {
		a.flash("Reading the host clipboard needs cats — use ⌘V for the editor's own")
		return
	}
	if a.activeTabPtr() == nil {
		a.flash("Open a file to paste into")
		return
	}
	a.catsClipRead(catsClipPasteAt)
}

// hasCatsClipboard gates the paste row: Tier 1 and something to paste into.
// The compare row is not gated on Tier 1 at all — it has a Tier-0 path.
func (a *App) hasCatsClipboard() bool {
	return a.catsTier1() && a.activeTabPtr() != nil
}

// -----------------------------------------------------------------------------
// The read itself
// -----------------------------------------------------------------------------

// catsClipRead runs the read off the main loop and posts the text back.
//
// The paste target is snapshotted HERE, on the loop, at the moment the user
// asked: which tab was active and what its edit revision was. Recording it at
// arrival instead would be recording where the caret drifted to, which is the
// bug this exists to avoid.
func (a *App) catsClipRead(use catsClipUse) {
	client, scr := a.cats.client, a.screen
	var tab *editor.Tab
	var rev int
	if t := a.activeTabPtr(); t != nil {
		tab, rev = t, t.EditRev
	}
	go func() {
		d, err := client.ClipboardRead()
		if err != nil {
			catsPostNotice(scr, "Clipboard: "+catsClipError(err))
			return
		}
		_ = scr.PostEvent(&catsEvent{
			when: time.Now(), kind: catsKindClip,
			text: d.Text, clipUse: use, clipTab: tab, clipRev: rev,
			truncated: d.Truncated,
		})
	}()
}

// catsClipError shortens the host's message for a status bar. The server's own
// words are kept — it knows which tool failed and why — minus the transport
// prefix this package puts on every error, which is noise on a line the user
// reads at a glance.
func catsClipError(err error) string {
	msg := err.Error()
	msg = strings.TrimPrefix(msg, "cats: ")
	if i := strings.Index(msg, "clipboard.read: "); i >= 0 {
		msg = msg[i+len("clipboard.read: "):]
	}
	return msg
}

// catsClipArrived is the main-loop side: the text is here, put it where the
// user asked for it.
func (a *App) catsClipArrived(e *catsEvent) {
	if e.text == "" {
		a.flash("The host clipboard is empty")
		return
	}
	switch e.clipUse {
	case catsClipCompare:
		if a.activeTabPtr() == nil {
			return
		}
		// No staleness guard, unlike the paste: a diff is against whatever the
		// user is looking at NOW, which is the buffer they would have picked if
		// asked again (the same rule catscapture.go's arrival keeps).
		label := "host clipboard"
		if e.truncated {
			label += " (truncated)"
		}
		if a.compare.selPending != nil {
			a.compareTextWithSelection(label, e.text)
			return
		}
		a.compareWithText(label, e.text)

	case catsClipPasteAt:
		a.catsClipPaste(e)
	}
}

// catsClipPaste inserts arrived text at the caret, or declines and hands the
// text to the internal clipboard instead.
//
// It declines when the buffer MOVED under the request — a different tab is
// active, or this one has been edited since. A local socket answers in
// microseconds, so this is close to unreachable in practice; the case it
// exists for is a host that has wedged and answers three seconds later, at
// which point the user is mid-word somewhere else and an insertion would land
// in the middle of what they are typing.
//
// The refusal is not a dead end. The text goes into ced's own clipboard and
// says so, which turns "your paste was thrown away" into "press ⌘V" — the one
// outcome that costs the user nothing.
func (a *App) catsClipPaste(e *catsEvent) {
	tab := a.activeTabPtr()
	if tab == nil || tab != e.clipTab || tab.EditRev != e.clipRev {
		a.clipBuf, a.clipKind = e.text, clipText
		a.flash("Host clipboard copied — the buffer moved, so press ⌘V to paste it")
		return
	}
	tab.InsertString(e.text)
	if e.truncated {
		a.flash("Pasted the host clipboard (truncated by the host)")
	}
}

// compile-time reminder that the clipboard path reports through the same
// notice channel as every other background cats call.
var _ func(tcell.Screen, string) = catsPostNotice
