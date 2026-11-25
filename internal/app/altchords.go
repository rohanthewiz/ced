// =============================================================================
// File: internal/app/altchords.go
// Author: Rohan Allison <rohanthewiz@gmail.com>
// Created: 2026-11-06
// Copyright: 2026 Rohan Allison. All rights reserved.
// =============================================================================

// altchords.go is the one shape for the Alt+<letter> chords that live
// INSIDE a keyboard-owning surface: a prompt's extras and its × clear,
// the find bar's Aa / |W| / .* / All, the cherry-pick dialog's buttons,
// the change view's Revert / Prev / Next / Side by side, and a picker
// heading's ⧉ copy.
//
// Those surfaces own the keyboard, so the ≡ menu (the keyboard twin of
// every other button) can't be reached from inside them, and each button
// owes an in-surface chord (the keyboard-owning-surface rule). Alt is
// safe there because the surface consumes the key before handleKey's
// Alt+rune leader branch can read it as a tmux-folded "Esc <rune>".
//
// Each surface used to answer its chords from a switch of its own, which
// left nothing to enumerate: the README could list the chords, but
// nothing could check the list (N-110). Now each surface's altChords
// method returns its table and its handleKey dispatches THROUGH that
// table, so the list a test reads is, by construction, the list a
// keystroke reaches:
//
//	Alt+<r> ──► surface.handleKey ──► fireAltChord(surface.altChords(a), r)
//	                                                  │ same table
//	README pin ──► surface.altChords(a) ──────────────┘
//
// The pin (TestREADME_AltChordTableListsEveryChord) opens every surface
// in a fixture and compares its table with README.md's
// "#### Alt chords inside dialogs", both ways. A NEW surface with Alt
// chords gets an altChords method here-shaped and a fixture entry there.
//
// The table carries no description. A button's own label is often its
// STATE ("[trailer: on]"), not what the chord does, and a second string
// per chord would be one more thing to keep in step for no runtime use;
// the README's "Does" column is the description, and the pin only asks
// that it says something.
//
// The tables are built on every Alt keystroke. They are a handful of
// closures on a surface that is already handling a key, so the cost is
// nothing next to the frame the key will draw, and building them fresh
// means a table can never go stale against state it closes over
// (prompt extras captured at open, a picker pane switched since).
//
// Alt+Enter is the one Alt chord that is NOT a rune, so it has no place
// in a table: the find bar answers it in its own Enter case ("close the
// bar and stay on the hit", stayFind) and the chat composer reads it as
// a line break. Both must see ONE spelling of it whatever the terminal
// sent, which foldAltEnter below provides.

package app

import "github.com/gdamore/tcell/v2"

// altChord is one in-surface chord: Alt+key runs run. run may be nil
// (the chord is then answered and does nothing), which keeps a key
// reserved without inventing a no-op closure.
type altChord struct {
	key rune
	run func(*App)
}

// altChordRune reports the rune of an Alt+<rune> keystroke; ok is false
// for anything else (a plain rune, an Alt+arrow, a bare Esc). Every
// surface asks this one question before consulting its table, so "is
// this an Alt chord?" can't drift between them.
func altChordRune(ev *tcell.EventKey) (rune, bool) {
	if ev.Key() != tcell.KeyRune || ev.Modifiers()&tcell.ModAlt == 0 {
		return 0, false
	}
	return ev.Rune(), true
}

// fireAltChord runs the chord bound to r and reports whether there was
// one. The FIRST match wins, so a table's order is its precedence: the
// prompt lists its extras before its clear chord, so a prompt that ever
// binds x to an extra keeps it.
func fireAltChord(a *App, chords []altChord, r rune) bool {
	for _, c := range chords {
		if c.key != r {
			continue
		}
		if c.run != nil {
			c.run(a)
		}
		return true
	}
	return false
}

// foldAltEnter rewrites the legacy spelling of Alt+Enter into the one a
// CSI-u terminal delivers (KeyEnter + ModAlt) and returns every other
// event unchanged.
//
// Alt+Enter never reaches tcell as itself on a legacy terminal (tmux
// included): the emulator writes ESC CR, and tcell's fold for
// ESC + control-char reports it as the RUNE 'm' carrying ModAlt|ModCtrl
// ('j' for the rare emulator that sends LF). Left alone, that spelling
// reads as Alt+m everywhere: a bound leader rune in handleKey (it fired
// multicaret on the buffer behind the surface the user was typing into)
// and a printable 'm' in any textField (it typed an "m" into the find
// bar's query).
//
// Two callers, on purpose not one hoisted to the top of handleKey:
// handleKey folds it just before its leader branches, and the find bar
// folds it for itself because it is dispatched ahead of that point. The
// modals stay outside the fold. Inside tmux a quick "Esc, Enter" arrives
// as this very ESC CR, and folding it for a dialog would turn "cancel,
// then a newline" into a SUBMIT of whatever the dialog defaults to; the
// bar's answer to the same accident is only "close and stay", which
// loses nothing.
func foldAltEnter(ev *tcell.EventKey) *tcell.EventKey {
	if ev.Key() == tcell.KeyRune &&
		ev.Modifiers()&tcell.ModAlt != 0 && ev.Modifiers()&tcell.ModCtrl != 0 &&
		(ev.Rune() == 'm' || ev.Rune() == 'j') {
		return tcell.NewEventKey(tcell.KeyEnter, 0, tcell.ModAlt)
	}
	return ev
}
