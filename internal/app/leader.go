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

import (
	"time"

	"github.com/gdamore/tcell/v2"
)

// leaderChordFor is how long a prefix stays armed waiting for its second
// rune. Deliberately longer than doubleEscMs (the double-Esc window): a
// chord is a two-stage gesture the user is composing, not a double-tap
// reflex, and 500ms is not enough time to think "which letter was the
// model picker again?". Short enough that a forgotten prefix can't
// swallow a keystroke seconds later.
const leaderChordFor = 2 * time.Second

// leaderBinding is one Esc-leader entry: the trigger rune and the App method
// that fires when the user presses Esc, <rune> in quick succession. Each method
// already handles its own preconditions — calling menuUndo with no active tab,
// for example, is a safe no-op — so the leader dispatch doesn't need to
// re-check enable predicates.
type leaderBinding struct {
	key    rune
	action func(*App)

	// sub makes this entry a PREFIX rather than an action: firing it
	// arms a second window in which one more rune dispatches from this
	// table (see fireLeader / handleChordKey). One namespace exists
	// today — Esc-a for the AI surface — and it exists because that
	// surface is fifteen actions deep while the flat table has run out
	// of mnemonic letters. Don't add a second namespace without the
	// same justification: a chord is a real cost, paid by everyone who
	// has to remember which letters are prefixes.
	sub []leaderBinding
	// subFor is the DYNAMIC form of sub, for a namespace whose contents
	// aren't known until they're asked for. Exactly one namespace needs
	// it — Esc-x, whose sub-table is built from the user's installed
	// plugins and changes the moment they hit Reload. Resolved on every
	// arm rather than cached, because a table baked at startup would go
	// stale exactly when the user was iterating on a manifest. When both
	// are set subFor wins; a nil result means "nothing bound", which
	// arms nothing (see fireLeader).
	subFor func(*App) []leaderBinding
	// hint is the one-line menu of a prefix's sub-bindings, flashed the
	// moment the prefix arms. A namespace nobody can enumerate from the
	// keyboard is a namespace nobody uses; this is the flat table's ≡
	// hint column, compressed into a status line.
	hint string
	// hintFor is hint's dynamic twin, for the same reason subFor exists.
	hintFor func(*App) string
	// name labels the namespace in the "no action bound to x" message a
	// missed chord flashes. Empty reads as a generic "leader".
	name string
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
		// 'F' lists every occurrence instead of walking them one Enter at
		// a time — the shifted variant of the same verb, following the
		// h/H and o/O convention. It seeds itself from the find bar, the
		// selection, or the word under the cursor, so it's a one-key
		// gesture from anywhere in the file.
		{key: 'F', action: (*App).menuFindAll},
		// 'e' for rEplace — the letter the verb's own name offers once
		// 'r' is spoken for (redo, with 'Z' as its shifted twin). It
		// deliberately isn't 'F's neighbour on a shift: replace is a
		// MUTATING verb, not another way to read, so pairing it with
		// find under the shift convention would have said the wrong
		// thing about what pressing it does.
		{key: 'e', action: (*App).menuReplace},
		// 'j' for Jump to line. Not 'l' — a lone 'l' is one stray Esc
		// away from every word with an L in it, which is the same
		// argument that put the git Log on a shifted 'L'.
		{key: 'j', action: (*App).menuGoToLine},
		{key: 'p', action: (*App).openFinder},
		// 'P' searches the project's CONTENTS rather than its filenames —
		// the shifted variant of the same verb, following the f/F and o/O
		// convention. Results land in the Find-all panel, so the two
		// scopes of "find" read as one instrument (projectsearch.go).
		{key: 'P', action: (*App).menuFindInProject},
		// Tab switching. ',' / '.' rather than the '[' / ']' every editor
		// with buffers uses, because those two CANNOT work here: "\x1b["
		// is the CSI introducer and "\x1b]" is OSC, so the terminal (and
		// tcell's parser) swallows the pair before it can ever reach the
		// leader table. ',' and '.' carry '<' and '>' on the same keys,
		// which is the same left/right mnemonic, and neither introduces
		// anything. 'b' opens the switcher as a picker — "buffer", and
		// the surface that still works once the strip is scrolled and
		// the tab you want isn't drawn at all. See tabbar.go.
		{key: ',', action: (*App).menuPrevTab, repeat: true},
		{key: '.', action: (*App).menuNextTab, repeat: true},
		{key: 'b', action: (*App).menuSwitchTab},
		// 'k' for the palette — the Cmd+K muscle memory every editor and
		// chat app teaches (the real Cmd+K never reaches a terminal app,
		// so Esc-k is the closest stand-in). It used to share the binding
		// with 'a' for "actions"; 'a' now opens the AI namespace below,
		// which is the higher-traffic use of the letter. The palette is
		// still the ≡ menu's pinned headline row, so it keeps three ways
		// in without that alias.
		{key: 'k', action: (*App).openPalette},
		// 'a' for AI — a PREFIX, not an action. Everything the chat agent
		// touches lives one rune deeper: the panel itself, context, the
		// model and backend pickers, skills, and MCP tools.
		{key: 'a', sub: aiLeaderBindings(), name: "AI",
			hint: "AI  c chat · s skills · a attach · f file · m model · b backend · t tools"},
		// 'x' for eXtensions — the plugin namespace, and the codebase's
		// only DYNAMIC prefix: its sub-table is whatever leader keys the
		// user's installed plugins asked for (plugins.go).
		//
		// It clears the same bar the AI namespace did, from the other
		// direction. That one existed because a fixed surface outgrew
		// the flat table; this one exists because plugin commands are
		// UNBOUNDED and belong to the user, so they can never go in the
		// flat table at all — every letter they took would be one ced
		// could no longer bind, and any letter ced later bound would
		// silently break somebody's plugin. A namespace is the only
		// place a user-owned key can live without fighting the editor's.
		{key: 'x', subFor: pluginLeaderBindings, hintFor: pluginLeaderHint, name: "Plugin"},
		// Multi-line editing (multicaret.go). 'm' grows the caret column
		// downward, 'M' upward — the same "shift means the other
		// direction" convention as h/H and o/O. Both repeat, so
		// "Esc m m m" builds a four-line column in one gesture.
		{key: 'm', action: (*App).menuAddCaretBelow, repeat: true},
		{key: 'M', action: (*App).menuAddCaretAbove, repeat: true},
		// '*' is vim's word-under-cursor key, which is the muscle memory
		// this actually competes with — VS Code's Cmd+D never reaches a
		// terminal app. Repeats, so "Esc * * *" claims three occurrences.
		{key: '*', action: (*App).menuAddNextOccurrence, repeat: true},
		// '&' is the bulk form of the same idea, one shift-key over on
		// the row — "all of them" next to "the next one". Deliberately
		// NOT repeatable: it already claimed every occurrence in the
		// file, so a second press has nothing to add and re-arming the
		// window would only swallow the next keystroke.
		{key: '&', action: (*App).menuSelectAllOccurrences},
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
		// 'L' for the git Log — shifted because lowercase 'l' is too
		// close to the common typing flow after a stray Esc, and the
		// h/H, o/O pairs already teach that shift means "the other git
		// surface".
		{key: 'L', action: (*App).menuToggleGitLog},
		{key: '=', action: (*App).growBottomPanel, repeat: true},
		{key: '-', action: (*App).shrinkBottomPanel, repeat: true},
		// '`' for the terminal — the key VS Code binds, minus the Ctrl.
		// An open-but-unfocused panel grabs focus first, so the leader
		// doubles as "jump back into the shell".
		{key: '`', action: (*App).leaderTerminal},
		// '~' is the shifted twin of the terminal's own key, and it means
		// "the file locations in what the terminal printed" — the
		// keyboard path into the clickable-output list (termdiag.go).
		// The panel is mouse-driven, but macOS Terminal swallows clicks,
		// so its verbs need a keyboard route; the shift convention (f/F,
		// p/P, d/D) makes this one guessable from the key beside it.
		{key: '~', action: (*App).menuTermLocations},
		// LSP trio: 'd' definition, 'i' info (hover), 'D' the file's whole
		// symbol outline as a picker. The shifted pair follows the same
		// convention f/F and p/P do — same verb, wider scope: 'd' goes to
		// the definition of the symbol under the cursor, 'D' lists every
		// definition in the file and lets you pick one.
		{key: 'd', action: (*App).menuGoToDefinition},
		{key: 'i', action: (*App).menuHoverInfo},
		{key: 'D', action: (*App).menuGoToSymbol},
		// 'R' for References — the letter the verb's own name offers once
		// 'r' is spoken for by redo, which is the same argument that put
		// rEplace on 'e'. It is deliberately NOT a shifted twin of redo:
		// redo's twin is 'Z' (beside its own 'z' alias), so the pair
		// convention was already spent and 'R' is free to mean what it
		// says. Results open in the Find-all panel (lspreferences.go).
		{key: 'R', action: (*App).menuFindReferences},
		// Navigation history: 'o' back "out" of a jump — 'b' was
		// tempting but reads as "buffer" to vim hands. Shifted variant
		// walks forward again, mirroring the h/H hunk convention.
		// Alt+Left / Alt+Right (handleKey) are the arrow twins.
		{key: 'o', action: (*App).menuNavBack, repeat: true},
		{key: 'O', action: (*App).menuNavForward, repeat: true},
	}
}

// aiLeaderBindings is the Esc-a namespace: every keyboard path into the
// chat agent's surface. It exists because that surface outgrew the flat
// table — fifteen menu rows competing for the letters a single-rune
// table had left, which is how skills ended up on a shifted 'S' before
// this namespace replaced it.
//
// Letters are mnemonic within the namespace and may collide with the
// top-level table on purpose ('a', 'f', 'm', 't' all mean something else
// after a bare Esc). That's the point of a namespace: 'f' can be "find
// file" out here and "attach file" in there, because the prefix already
// said which world you're in.
//
// Nothing here is repeatable — these all open a panel or a picker, and
// re-arming the window after one would only swallow the next keystroke.
func aiLeaderBindings() []leaderBinding {
	return []leaderBinding{
		// 'c' for chat — focus-or-toggle, the same gesture Esc-` uses for
		// the terminal, so an open-but-unfocused panel is one key away.
		{key: 'c', action: (*App).leaderChat},
		{key: 's', action: (*App).menuUseSkill},
		// 'a' doubles the prefix: Esc-a-a is "attach what I'm looking
		// at", the namespace's most-reached-for verb, and a doubled
		// prefix is the standard way to spell "the obvious one".
		{key: 'a', action: (*App).menuChatAttachCurrent},
		{key: 'f', action: (*App).menuChatAttachFile},
		{key: 'm', action: (*App).menuChatModel},
		// 'b' for backend — 'a' was taken by attach, and "backend" is
		// the word the ≡ row's help text uses for the agent registry.
		{key: 'b', action: (*App).menuChatAgent},
		// 't' for tools — what MCP servers actually are from the chat
		// panel's side, and the word the README leads with.
		{key: 't', action: (*App).menuMCPServers},
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
//
// A PREFIX binding runs no action at all — it arms the chord window and
// flashes its hint, and the next rune resolves against its sub-table
// (handleChordKey). Both leader entry paths funnel through here, so the
// namespace works identically inside tmux (where "Esc a" arrives folded
// as one Alt+a event) and outside it.
func (a *App) fireLeader(b *leaderBinding) {
	if b.sub != nil || b.subFor != nil {
		sub, hint := b.sub, b.hint
		if b.subFor != nil {
			sub = b.subFor(a)
		}
		if b.hintFor != nil {
			hint = b.hintFor(a)
		}
		// An empty dynamic namespace arms NOTHING: with no plugin
		// bound, "Esc x" then a letter should leave that letter alone
		// rather than swallow it into a namespace that has no answers.
		// The hint still flashes — it's what tells the user the
		// namespace exists and is currently empty.
		if len(sub) == 0 {
			a.clearChord()
			a.lastEscape = time.Time{}
			a.leaderChained = false
			a.flash(hint)
			return
		}
		a.leaderChord = sub
		a.leaderChordName = b.name
		a.leaderChordAt = time.Now()
		a.lastEscape = time.Time{}
		a.leaderChained = false
		a.flash(hint)
		return
	}
	a.clearChord()
	if b.repeat {
		a.lastEscape = time.Now()
		a.leaderChained = true
	} else {
		a.lastEscape = time.Time{}
		a.leaderChained = false
	}
	b.action(a)
}

// handleChordKey resolves the second rune of a chord, reporting whether
// it consumed the keystroke. Returns false when no chord is pending or
// the window has expired, so the caller carries on as if nothing was
// armed.
//
// An unbound rune inside a live chord is SWALLOWED with a flash, which
// is deliberately unlike the top-level table's fall-through. A lone Esc
// can be a stray tap — the flat table stays harmless to mash for exactly
// that reason — but "Esc a" is two deliberate keys, so the likelier
// reading of a miss is a mistyped chord, not text. Falling through would
// answer that by dropping a stray character into the user's code.
func (a *App) handleChordKey(ev *tcell.EventKey) bool {
	if a.leaderChord == nil {
		return false
	}
	if time.Since(a.leaderChordAt) >= leaderChordFor {
		a.clearChord()
		return false
	}
	// Esc out of a chord: the universal "drop that" gesture. Handled by
	// the caller's Esc branch, which clears the chord before running.
	if ev.Key() != tcell.KeyRune {
		a.clearChord()
		return false
	}
	sub := a.leaderChord
	name := a.leaderChordName
	if name == "" {
		name = "leader"
	}
	prefix := chordPrefixFor(a.leaderChordName)
	a.clearChord()
	for i := range sub {
		if sub[i].key == ev.Rune() {
			sub[i].action(a)
			return true
		}
	}
	a.flash("No " + name + " action bound to " + string(ev.Rune()) +
		" — esc " + string(prefix) + " again for the list")
	return true
}

// clearChord disarms a pending chord.
func (a *App) clearChord() {
	a.leaderChord = nil
	a.leaderChordName = ""
	a.leaderChordAt = time.Time{}
}

// chordPrefixFor finds the leader rune that arms the namespace called
// name, so the miss message can tell the user which prefix to press
// again. Falls back to 'a' — the only namespace that predates the
// lookup — rather than printing an empty key.
func chordPrefixFor(name string) rune {
	for _, b := range leaderBindings() {
		if (b.sub != nil || b.subFor != nil) && b.name == name {
			return b.key
		}
	}
	return 'a'
}
