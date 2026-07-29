// =============================================================================
// File: internal/app/leader.go
// Author: Rohan Allison <rohanthewiz@gmail.com>
// Created: 2026-04-30
// Copyright: 2026 Rohan Allison. All rights reserved.
// Portions copyright 2026 Cloudmanic, LLC. Original author: Spicer Matthews.
// =============================================================================

// leader.go defines the editor's Esc-leader hotkey table. Esc-Esc still opens
// the action menu (handled in handleKey); the bindings here handle the
// "Esc, then one rune within doubleEscMs" sequences for common
// actions. We deliberately avoid Ctrl-key shortcuts because they fight
// tmux/zellij prefixes and the terminal's own bindings — Esc is the only
// modifier we trust over SSH.

package app

import "time"

// leaderBinding is one Esc-leader entry: the trigger rune and the App method
// that fires when the user presses Esc, <rune> in quick succession. Each method
// already handles its own preconditions — calling menuUndo with no active tab,
// for example, is a safe no-op — so the leader dispatch doesn't need to
// re-check enable predicates.
type leaderBinding struct {
	key    rune
	action func(*App)
	// repeat marks actions that make sense in rapid succession (undo,
	// hunk-walking, panel resize). Firing one keeps the leader window
	// armed in "chain" mode: the next repeatable rune within
	// doubleEscMs fires without a fresh Esc, so "Esc z z z" undoes
	// three times instead of undoing once and typing "zz". Chain mode
	// only admits repeatable bindings — a non-repeatable rune falls
	// through to normal typing, so "Esc z" followed quickly by the
	// word "so" doesn't trigger a save.
	repeat bool
}

// leaderBindings is the editor's full Esc-leader table. The order is purely
// presentational: tests iterate it to assert every binding fires, and a
// future help screen can render the table directly. Letter bindings are
// chosen to be mnemonic and avoid collisions; punctuation bindings mirror
// familiar editor gestures where they make sense.
//
// Intentionally not bound:
//   - c / x / v (clipboard) — the host terminal's Cmd+C/V already covers
//     that path; adding a third channel just adds confusion.
//   - rename / delete / revert — destructive enough that we want the
//     menu's confirm dialog to gate the action as a deliberate gesture.
func leaderBindings() []leaderBinding {
	return []leaderBinding{
		{key: 's', action: (*App).menuSave},
		{key: 'u', action: (*App).menuUndo, repeat: true},
		{key: 'r', action: (*App).menuRedo, repeat: true},
		// 'z' is the Cmd+Z muscle-memory alias for undo (same spirit as
		// 'k' for the palette); the shifted variant redoes, mirroring
		// the h/H and o/O pair convention.
		{key: 'z', action: (*App).menuUndo, repeat: true},
		{key: 'Z', action: (*App).menuRedo, repeat: true},
		{key: 'w', action: (*App).menuClose},
		{key: 'q', action: (*App).menuQuit},
		{key: 'n', action: (*App).menuNewFile},
		{key: 't', action: (*App).menuToggleSidebar},
		{key: '/', action: (*App).menuToggleLineComment},
		{key: 'f', action: (*App).openFind},
		{key: 'p', action: (*App).openFinder},
		// 'a' for "actions" — the palette is the searchable twin of the
		// ≡ action menu, so it borrows the menu's vocabulary. 'k' is an
		// alias for Cmd+K muscle memory (VS Code/Slack); the real Cmd+K
		// never reaches a terminal app, so Esc-k is the closest stand-in.
		{key: 'a', action: (*App).openPalette},
		{key: 'k', action: (*App).openPalette},
		// 'h' for "hunk" — jump between git-changed regions. Shifted
		// variant walks backwards, mirroring find's Enter/Shift-Enter.
		{key: 'h', action: (*App).menuNextHunk, repeat: true},
		{key: 'H', action: (*App).menuPrevHunk, repeat: true},
		// 'g' for "git" — collapse/expand the diff review panel.
		// '=' / '-' resize whichever bottom panel is open (grow/shrink,
		// borrowing the browser-zoom mnemonic); silent no-ops while
		// both are collapsed. No menu rows for these two: resize's
		// primary surface is dragging the panel header, same as the
		// sidebar splitter.
		{key: 'g', action: (*App).menuToggleGitPanel},
		{key: '=', action: (*App).growBottomPanel, repeat: true},
		{key: '-', action: (*App).shrinkBottomPanel, repeat: true},
		// '`' for the terminal — the key VS Code binds, minus the Ctrl.
		// An open-but-unfocused panel grabs focus first, so the leader
		// doubles as "jump back into the shell".
		{key: '`', action: (*App).leaderTerminal},
		// LSP pair: 'd' definition, 'i' info (hover).
		{key: 'd', action: (*App).menuGoToDefinition},
		{key: 'i', action: (*App).menuHoverInfo},
		// Navigation history: 'o' back "out" of a jump — 'b' was
		// tempting but reads as "buffer" to vim hands. Shifted variant
		// walks forward again, mirroring the h/H hunk convention.
		// Alt+Left / Alt+Right (handleKey) are the arrow twins.
		{key: 'o', action: (*App).menuNavBack, repeat: true},
		{key: 'O', action: (*App).menuNavForward, repeat: true},
	}
}

// leaderBindingFor looks up the leader table entry bound to r, or returns
// nil when r isn't bound. Returning nil rather than a no-op lets the
// caller distinguish "leader fired" from "key was unbound — fall through
// to normal handling", which matters for typing flow: pressing Esc then a
// non-leader letter must still let that letter reach the editor's normal
// key handler.
func leaderBindingFor(r rune) *leaderBinding {
	for _, b := range leaderBindings() {
		if b.key == r {
			return &b
		}
	}
	return nil
}

// leaderActionFor is the action-only view of leaderBindingFor, kept for
// callers (and tests) that only care whether a rune is bound.
func leaderActionFor(r rune) func(*App) {
	if b := leaderBindingFor(r); b != nil {
		return b.action
	}
	return nil
}

// fireLeader runs a leader binding and settles the window state after it:
// a repeatable action re-arms the window in chain mode so the next
// repeatable rune fires without a fresh Esc; anything else closes the
// window outright.
func (a *App) fireLeader(b *leaderBinding) {
	if b.repeat {
		a.lastEscape = time.Now()
		a.leaderChained = true
	} else {
		a.lastEscape = time.Time{}
		a.leaderChained = false
	}
	b.action(a)
}
