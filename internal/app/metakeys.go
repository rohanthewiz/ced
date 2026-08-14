// =============================================================================
// File: internal/app/metakeys.go
// Author: Rohan Allison <rohanthewiz@gmail.com>
// Created: 2026-08-13
// Copyright: 2026 Rohan Allison. All rights reserved.
// =============================================================================

// The ⌘ accelerator layer (ai_docs/cats-native-plan.md Phase 5.2): the
// GoLand/VS Code muscle memory — ⌘S, ⌘P, ⌘F — for the hosts that can
// actually deliver a Command chord to a terminal program.
//
// IT IS A BONUS LAYER AND NOTHING MAY LIVE HERE ALONE. Esc-leader is the
// editor's identity: it survives SSH, tmux and every terminal ever
// shipped, and it is what the which-key overlay, the ≡ shortcut column
// and the docs speak. Every row below is a second door onto a verb that
// already has an Esc key or a menu row — pinned by
// TestMetaAccelsAreNeverTheOnlyPath, which fails the build if someone
// adds a ⌘-only action here. A user who never learns this table loses
// nothing.
//
// HOW A ⌘ CHORD REACHES A TERMINAL PROGRAM AT ALL
//
// Only through the kitty keyboard protocol. tcell asks for it at startup
// (`CSI > 1 u`, tscreen.go) on any xterm-like terminal, and a terminal
// that honours it reports ⌘S as `CSI 115;9u` — codepoint 115, modifier
// param 9 = 1 + 8, where 8 is kitty's "super" bit. tcell's calcModifier
// turns that into ModMeta. In a classic terminal the chord never leaves
// the emulator, so the layer is silently absent — the same degradation
// story as Tier 0.
//
// That encoding is not asserted by a test here, and it cannot be: the
// only way to feed raw bytes to tcell's parser from a test is
// SimulationScreen.InjectKeyBytes, which in v2.13.9 does NOT run the CSI
// parser at all — it maps bytes one at a time, and its control-byte
// branch never advances the slice, so an ESC hangs the caller in an
// infinite post loop. The claim rests instead on reading both ends:
// tcell's calcModifier (input.go — bit 8 "kitty calls this Super" →
// ModMeta) and cats' own encoder test, which pins `\x1b[97;9u` for ⌘A.
// The tests below start from the event those produce.
//
// Two consequences shape this file:
//
//   - THE RUNE ARRIVES UNSHIFTED. The CSI-u parameter is kitty's
//     unicode-key-code, i.e. the key's lowercase codepoint, with Shift in
//     the modifier bits — so ⌘⇧P is ('p', ModMeta|ModShift), not ('P',
//     ModMeta). Other emitters (the cats mac app's own path, terminals
//     using xterm's modifyOtherKeys) send the shifted rune instead.
//     metaChord folds both spellings into one (rune, shift) pair, which
//     is the same both-encodings rule the Cmd+Z branch in handleKey
//     already follows.
//
//   - ⌘+CLICK IS NOT AVAILABLE, AND CANNOT BE. The plan asked for
//     ⌘click → go to definition; the wire has no room for it. SGR mouse
//     reports carry exactly three modifier bits — shift 4, meta/alt 8,
//     ctrl 16 — with no super bit, so cats' encoder drops the Command
//     modifier before the report is written (verified in cats'
//     internal/inputenc: the mouse test matrix has shift and ctrl+alt
//     cases and no super case) and tcell's own SGR decoder has no ModMeta
//     path to decode into. The gesture would need a mouse protocol nobody
//     implements. Go-to-definition therefore stays Esc-d / the editor's
//     right-click menu, and ced's one modified click stays Alt+click for
//     multicaret — which is bit 8, the bit ⌘ would have had to borrow.
//
// WHERE THE LAYER IS LIVE TODAY
//
// In kitty, Ghostty and WezTerm, where ced is talking to the emulator
// directly — and, since cats 77285f9, in browser-cats for the chords on
// its CMD_TO_PANE allowlist (⌘S ⌘P ⌘⇧P ⌘E ⌘F ⌘⇧F ⌘D ⌘G ⌘/), forwarded
// only to a pane whose kitty flags are non-zero. Before that commit its
// front end forwarded ⌘C and ⌘Z and nothing else, so this whole table was
// armed and dark; the table did not change when the gate opened, which is
// the point of arming it early. ⌘E reached that allowlist a few hours
// later (cats ed4962c) — the one row here bound after the gate had
// already opened, and it needed no change on this side either.
//
// What is still the HOST's to decide, per chord: ⌘W ⌘T ⌘L ⌘R ⌘N ⌘Q stay
// the browser's on purpose. The cats mac app was confirmed by hand on
// 2026-08-14 to pass this layer through — Cocoa resolves ⌘ menu key
// equivalents before the WebView, and catapp's menus claim none of these
// chords, so they reach the same handler the browser front end uses.
//
// A browser, however, DOES claim some of them for its own menus, and
// resolves those before the page: ⌘E is on cats' allowlist and still
// never arrives, because the browser takes it first and cats cannot
// preventDefault an event it is not offered. So the layer is live in the
// mac app and only partly live in a browser, per chord, and which chords
// is the host's answer to give — not ced's and not cats'. Either way a
// chord this table binds and a host declines to forward is a chord that
// does nothing, never a chord that does the wrong thing.

package app

import (
	"os"
	"strings"
	"unicode"

	"github.com/gdamore/tcell/v2"
)

// metaAccel is one ⌘ accelerator. key is always the UNSHIFTED rune and
// shift says whether Command+Shift is the chord, so the table reads the
// way the keycap does regardless of which spelling the host emitted.
type metaAccel struct {
	key    rune
	shift  bool
	action func(*App)
	// label names the verb for the guard tests' failure messages. It is
	// deliberately not drawn anywhere: the menu's shortcut column speaks
	// Esc, and printing two shortcuts per row would teach the one that
	// works in fewer places.
	label string
}

// metaAccels is the table. Every entry is a verb that already has an
// Esc-leader key or a ≡ row (enforced by test), and every chord is one
// the surrounding ecosystem already spells the same way — the point of
// the layer is that hands trained elsewhere work here without being
// retrained.
//
// Preconditions are each action's own business, exactly as in the leader
// table: menuSave with no tab open is a no-op, not a crash.
func metaAccels() []metaAccel {
	return []metaAccel{
		{key: 's', action: (*App).menuSave, label: "Save"},
		// ⌘P is the fuzzy file open and ⌘⇧P the command palette — the
		// VS Code pair, in that order. ced's own Esc table spells them
		// p/k rather than p/P, because Esc-k predates the palette's ⌘
		// convention; the shift pair is right here because ⌘⇧P is what
		// every other editor's user will press first.
		{key: 'p', action: (*App).openFinder, label: "Find file"},
		{key: 'p', shift: true, action: (*App).openPalette, label: "Command palette"},
		// Find and its wider scope, matching the f/F leader pair.
		{key: 'f', action: (*App).openFind, label: "Find"},
		{key: 'f', shift: true, action: (*App).menuFindInProject, label: "Find in project"},
		// ⌘W is bound for completeness and is expected to be DEAD in
		// most hosts: kitty, Ghostty and every browser claim it for
		// "close the window/tab" before an application sees it. It is
		// here so that a host which does forward it does the obvious
		// thing, and it is documented as dead so nobody debugs it twice.
		{key: 'w', action: (*App).menuClose, label: "Close tab"},
		// ⌘D duplicates the line (the ≡ row's own shortcut column says
		// ctrl+d, which is the JetBrains spelling ced cannot claim —
		// Ctrl is banned outright). Not "add next occurrence": that is
		// VS Code's ⌘D, but ced's multicaret gesture is Esc-* and
		// rebinding a MUTATING verb's letter to a selection verb by
		// modifier would make ⌘D and Esc-d mean different things.
		{key: 'd', action: (*App).menuDuplicateLines, label: "Duplicate line"},
		{key: '/', action: (*App).menuToggleLineComment, label: "Toggle comment"},
		{key: 'g', action: (*App).menuGoToLine, label: "Go to line"},
		// ⌘E is the recent-files ring (recentfiles.go), added once 3.4
		// gave it a picker to open — the VS Code / GoLand spelling, where
		// the gesture people actually perform is ⌘E-Enter to flip back to
		// the previous file. Its Esc twin is Esc-B. It was bound here after
		// cats' allowlist had already closed, and cats added KeyE to it the
		// same afternoon (ed4962c), so it is now live wherever the rest of
		// this table is: kitty, Ghostty, WezTerm, and browser-cats over a
		// pane that asked for the kitty protocol.
		{key: 'e', action: (*App).menuRecentFiles, label: "Recent files"},
		// Deliberately NOT bound:
		//   ⌘C / ⌘V / ⌘Z — handled directly in handleKey, and older than
		//     this table. They stay there because they are context-
		//     sensitive (the compare panel, the chat composer and the
		//     terminal each claim their own paste), which is exactly the
		//     shape a flat table cannot express.
		//   everything in metaReserved, below.
	}
}

// metaReserved are the Command chords ced must never claim, with the
// owner that claims them first. Nothing consults this at runtime —
// dispatch simply finds no entry — but the knowledge belongs in the
// source next to the table it constrains, and TestMetaAccelsAvoidReserved
// makes it binding.
//
//	k, b        cats' command palette and sidebar toggle (index.html)
//	v           cats' paste — it reads the host clipboard for us
//	=, +, -, 0  font size, in cats' browser front end AND in the mac
//	            app's View menu, which resolves them in Cocoa before the
//	            WebView is offered them at all
//	c, z        ced's own, in handleKey — see the note in metaAccels
//
// F12 is on the same list conceptually (devtools, and cats returns early
// on it) but it is not a rune chord, so it cannot appear in this table.
func metaReserved() []rune { return []rune{'k', 'b', 'v', '=', '+', '-', '0', 'c', 'z'} }

// metaChord normalizes a ModMeta key event into the (rune, shift) pair
// the table is written in. Two hosts spell ⌘⇧P two different ways —
// kitty's CSI-u sends the unshifted codepoint with the shift bit set,
// while an emitter that reports the produced character sends 'P' — and
// both mean the same keycap. Returning the lowercase rune plus a shift
// flag lets the table say each chord once.
func metaChord(ev *tcell.EventKey) (rune, bool) {
	r := ev.Rune()
	shift := ev.Modifiers()&tcell.ModShift != 0
	if unicode.IsUpper(r) {
		shift = true
		r = unicode.ToLower(r)
	}
	return r, shift
}

// metaAccelArmed reports whether the ⌘ layer may fire in this process.
//
// The gate exists for one failure mode: a terminal that folds Option
// into Meta would turn a stray Alt-keystroke into a save, a close, or a
// duplicated line. Rather than guess from the keystroke, we require the
// host to be one that is known to report Command DISTINCTLY —
//
//   - Tier 1: we are in a cats pane whose control socket answered. cats
//     encodes Command as kitty's super bit and nothing else; whether its
//     front end forwards a given chord today is cats' business (see the
//     file comment), and arming a chord that cannot arrive is free.
//   - a terminal that identifies itself as a kitty-protocol emulator.
//
// iTerm2 is deliberately NOT on the list: it speaks the protocol in
// recent versions but also ships the "left Option as Meta" setting this
// gate is here to defend against, and no env var distinguishes the two
// configurations. Losing an accelerator there costs a user nothing (the
// Esc key still works); a phantom save does.
//
// Read from the environment on each ⌘ keystroke rather than cached at
// startup: it is a handful of getenvs on a keypress that only happens
// when the user held Command, and a var would need a seam for tests
// anyway.
func (a *App) metaAccelArmed() bool {
	return a.catsTier1() || metaKittyHost()
}

// metaKittyHost sniffs the terminal identity for an emulator that
// implements the kitty keyboard protocol and reports Command as super.
// Env only — no query/response handshake, because a terminal that fails
// to answer one would leave the editor deciding a keybinding table on a
// timeout.
func metaKittyHost() bool {
	term := os.Getenv("TERM")
	prog := os.Getenv("TERM_PROGRAM")
	switch {
	case term == "xterm-kitty" || os.Getenv("KITTY_WINDOW_ID") != "":
		return true
	case term == "xterm-ghostty" || strings.EqualFold(prog, "ghostty") ||
		os.Getenv("GHOSTTY_RESOURCES_DIR") != "":
		return true
	case strings.EqualFold(prog, "WezTerm") || os.Getenv("WEZTERM_PANE") != "":
		return true
	}
	return false
}

// metaAccelFire dispatches a ModMeta key event through the table,
// reporting whether it was consumed. False means "not ours" and the
// caller carries on — which is what keeps ⌘C/⌘V/⌘Z, and any chord the
// host wants back, working exactly as they did before this file existed.
func (a *App) metaAccelFire(ev *tcell.EventKey) bool {
	if ev.Key() != tcell.KeyRune || ev.Modifiers()&tcell.ModMeta == 0 {
		return false
	}
	// An open ≡ menu owns the keyboard — every rune typed into it
	// narrows its fuzzy search — so the bonus layer stands down rather
	// than firing a verb out from under the row the user is reading.
	// The chord is still not TEXT: handleKey swallows what this refuses,
	// so the search seed doesn't gain a stray letter either.
	if a.menuOpen {
		return false
	}
	if !a.metaAccelArmed() {
		return false
	}
	r, shift := metaChord(ev)
	for _, m := range metaAccels() {
		if m.key == r && m.shift == shift {
			m.action(a)
			return true
		}
	}
	return false
}
