// =============================================================================
// File: internal/app/metakeys_test.go
// Author: Rohan Allison <rohanthewiz@gmail.com>
// Created: 2026-08-13
// Copyright: 2026 Rohan Allison. All rights reserved.
// =============================================================================

package app

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/gdamore/tcell/v2"
)

// armMetaHost makes the ⌘ layer live for one test by claiming to be
// kitty, and clears the other emulators' markers so a developer running
// the suite from inside Ghostty or WezTerm doesn't get a different gate
// than CI does.
func armMetaHost(t *testing.T) {
	t.Helper()
	t.Setenv("TERM", "xterm-kitty")
	t.Setenv("TERM_PROGRAM", "")
	t.Setenv("KITTY_WINDOW_ID", "")
	t.Setenv("GHOSTTY_RESOURCES_DIR", "")
	t.Setenv("WEZTERM_PANE", "")
}

// disarmMetaHost is its opposite: a plain terminal that speaks no
// keyboard protocol at all.
func disarmMetaHost(t *testing.T) {
	t.Helper()
	t.Setenv("TERM", "xterm-256color")
	t.Setenv("TERM_PROGRAM", "Apple_Terminal")
	t.Setenv("KITTY_WINDOW_ID", "")
	t.Setenv("GHOSTTY_RESOURCES_DIR", "")
	t.Setenv("WEZTERM_PANE", "")
}

// metaKeyEv builds the event a kitty-protocol host sends for ⌘<rune>:
// the UNSHIFTED codepoint plus the super bit. This is the encoding the
// wire actually carries (CSI <cp>;9u), so the common case is tested in
// the spelling the common case arrives in.
func metaKeyEv(r rune, shift bool) *tcell.EventKey {
	mods := tcell.ModMeta
	if shift {
		mods |= tcell.ModShift
	}
	return tcell.NewEventKey(tcell.KeyRune, r, mods)
}

// ⌘S saves, which is the whole promise of the layer in one keystroke:
// a hand trained on any other editor lands on the right verb.
func TestMetaAccelSaves(t *testing.T) {
	armMetaHost(t)
	dir := t.TempDir()
	target := filepath.Join(dir, "t.txt")
	if err := os.WriteFile(target, []byte(""), 0644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	a := newTestApp(t, dir)
	a.openFile(target)
	a.handleKey(keyEv(tcell.KeyRune, 'a'))
	if !a.activeTabPtr().Dirty {
		t.Fatal("precondition: the tab should be dirty after typing")
	}

	a.handleKey(metaKeyEv('s', false))

	if a.activeTabPtr().Dirty {
		t.Fatal("Cmd+S did not save")
	}
	got, err := os.ReadFile(target)
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	if string(got) != "a" {
		t.Fatalf("file on disk = %q, want %q", got, "a")
	}
}

// ⌘E is the newest row and the only one with a picker built for it: the
// chord must open the recent-files list holding the file visited before
// this one, which is the gesture (⌘E, Enter) the row exists for. The ring
// itself is tested in recentfiles_test.go; this pins that the chord
// reaches that picker rather than one of the editor's several others.
func TestMetaAccelOpensRecentFiles(t *testing.T) {
	armMetaHost(t)
	dir := t.TempDir()
	first := filepath.Join(dir, "first.txt")
	second := filepath.Join(dir, "second.txt")
	for _, p := range []string{first, second} {
		if err := os.WriteFile(p, []byte("x\n"), 0644); err != nil {
			t.Fatalf("seed: %v", err)
		}
	}
	a := newTestApp(t, dir)
	a.openFile(first)
	a.openFile(second)

	a.handleKey(metaKeyEv('e', false))

	m, ok := a.modal.(*paletteModal)
	if !ok {
		t.Fatalf("Cmd+E opened %T, want the recent-files picker", a.modal)
	}
	if len(m.items) != 1 || !strings.Contains(m.items[0].label, "first.txt") {
		t.Fatalf("first row should be the previously visited file, got %d rows", len(m.items))
	}
}

// The shift half of the table, in BOTH spellings a host may send it in:
// kitty reports the unshifted codepoint with the shift bit (⌘⇧P =
// 'p'+Shift+Meta), while an emitter reporting the produced character
// sends 'P'. They are the same keycap and must reach the same verb —
// and neither may be confused with unshifted ⌘P, which is a different
// picker.
func TestMetaAccelShiftBothEncodings(t *testing.T) {
	armMetaHost(t)
	dir := t.TempDir()

	// kitty's spelling.
	a := newTestApp(t, dir)
	a.handleKey(metaKeyEv('p', true))
	if _, ok := a.modal.(*paletteModal); !ok {
		t.Fatalf("Cmd+Shift+P ('p' + shift) opened %T, want the command palette", a.modal)
	}

	// The shifted-rune spelling.
	a = newTestApp(t, dir)
	a.handleKey(tcell.NewEventKey(tcell.KeyRune, 'P', tcell.ModMeta))
	if _, ok := a.modal.(*paletteModal); !ok {
		t.Fatalf("Cmd+Shift+P (rune 'P') opened %T, want the command palette", a.modal)
	}

	// Unshifted is the OTHER picker — a shift flag that leaked would
	// silently swap two chords that live one keycap apart.
	a = newTestApp(t, dir)
	a.handleKey(metaKeyEv('p', false))
	if _, ok := a.modal.(*finderModal); !ok {
		t.Fatalf("Cmd+P opened %T, want the file finder", a.modal)
	}
}

// The gate. In a terminal that cannot be trusted to distinguish Command
// from Meta the table must not fire — an Option-as-Meta misfire would
// turn a stray keystroke into a save or a duplicated line.
func TestMetaAccelSilentOnUnknownHost(t *testing.T) {
	disarmMetaHost(t)
	dir := t.TempDir()
	target := filepath.Join(dir, "t.txt")
	if err := os.WriteFile(target, []byte(""), 0644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	a := newTestApp(t, dir)
	a.openFile(target)
	a.handleKey(keyEv(tcell.KeyRune, 'a'))

	a.handleKey(metaKeyEv('s', false))

	if !a.activeTabPtr().Dirty {
		t.Fatal("Cmd+S fired outside a kitty-protocol host — the gate is open")
	}
	// ...and the refused chord must not become TEXT either.
	if got := a.activeTabPtr().Buffer.Lines[0]; got != "a" {
		t.Fatalf("the refused Cmd+S typed into the buffer: %q", got)
	}
}

// A Command chord this build doesn't bind is swallowed, not inserted.
// ⌘T is the case that matters: a kitty-protocol host that doesn't claim
// it for a new tab forwards it to us, and "my shortcut didn't work" must
// never mean "my shortcut typed a letter into my code".
func TestMetaUnboundChordIsNotText(t *testing.T) {
	armMetaHost(t)
	dir := t.TempDir()
	target := filepath.Join(dir, "t.txt")
	if err := os.WriteFile(target, []byte(""), 0644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	a := newTestApp(t, dir)
	a.openFile(target)

	a.handleKey(metaKeyEv('t', false))
	a.handleKey(metaKeyEv('n', false))

	if got := a.activeTabPtr().Buffer.Lines[0]; got != "" {
		t.Fatalf("unbound Cmd chords typed %q into the buffer", got)
	}
}

// Cmd+C / Cmd+V / Cmd+Z predate this table and are context-sensitive
// (the compare panel, the chat composer and the terminal each claim
// their own paste), which is why they stay in handleKey. The table must
// never shadow them.
func TestMetaAccelDoesNotShadowClipboardOrUndo(t *testing.T) {
	armMetaHost(t)
	dir := t.TempDir()
	target := filepath.Join(dir, "t.txt")
	if err := os.WriteFile(target, []byte(""), 0644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	a := newTestApp(t, dir)
	a.openFile(target)
	a.handleKey(keyEv(tcell.KeyRune, 'a'))

	a.handleKey(metaKeyEv('z', false))

	if got := a.activeTabPtr().Buffer.Lines[0]; got != "" {
		t.Fatalf("Cmd+Z should still undo, buffer = %q", got)
	}
}

// The reserved list is the contract with the host: cats owns ⌘K, ⌘B,
// ⌘V and the font chords, ced's own handleKey owns ⌘C/⌘V/⌘Z. Claiming
// one here would produce a chord that does two things at once — one of
// them invisible, in another program.
func TestMetaAccelsAvoidReserved(t *testing.T) {
	reserved := map[rune]bool{}
	for _, r := range metaReserved() {
		reserved[r] = true
	}
	for _, m := range metaAccels() {
		if reserved[m.key] {
			t.Errorf("⌘%c (%s) is on the reserved list — the host claims it first", m.key, m.label)
		}
	}
}

// No duplicate chords: two entries with the same (rune, shift) would
// make the table's ORDER decide which verb runs, which is not a thing a
// reader would expect to have to check.
func TestMetaAccelsAreUnique(t *testing.T) {
	type chord struct {
		r     rune
		shift bool
	}
	seen := map[chord]string{}
	for _, m := range metaAccels() {
		c := chord{m.key, m.shift}
		if prev, dup := seen[c]; dup {
			t.Errorf("⌘%c (shift=%v) is bound twice: %s and %s", m.key, m.shift, prev, m.label)
		}
		seen[c] = m.label
	}
}

// THE HOUSE RULE, MADE BINDING: never bind anything ⌘-only. Esc-leader
// works over SSH, in tmux and in every terminal ever shipped; ⌘ works in
// a handful of emulators. Every accelerator must therefore be a SECOND
// door onto a verb that already has an Esc key or a ≡ row, so a user who
// never learns this table loses nothing at all.
//
// Function values aren't comparable in Go, so the identity check goes
// through reflect's code pointer — which is exactly the question being
// asked: is this the same function the other surface calls?
func TestMetaAccelsAreNeverTheOnlyPath(t *testing.T) {
	fnKey := func(f func(*App)) uintptr { return reflect.ValueOf(f).Pointer() }

	reachable := map[uintptr]bool{}
	var walkLeader func(bs []leaderBinding)
	walkLeader = func(bs []leaderBinding) {
		for _, b := range bs {
			if b.action != nil {
				reachable[fnKey(b.action)] = true
			}
			walkLeader(b.sub)
		}
	}
	walkLeader(leaderBindings())

	// The ≡ menu is the other guaranteed surface — Duplicate line lives
	// there and not in the leader table, and that is a complete answer.
	a := newTestApp(t, t.TempDir())
	for _, g := range a.visibleMenuGroups() {
		for _, it := range g.items {
			if it.action != nil {
				reachable[fnKey(it.action)] = true
			}
		}
	}

	for _, m := range metaAccels() {
		if !reachable[fnKey(m.action)] {
			t.Errorf("⌘%c (%s) has no Esc-leader key and no menu row — "+
				"a ⌘-only binding is invisible to every user not on a kitty-protocol host", m.key, m.label)
		}
	}
}

// metaChord folds the two wire spellings of a shifted chord into one
// pair, and leaves an unshifted chord alone. Punctuation has no case, so
// ⌘/ must survive the fold untouched.
func TestMetaChordNormalizes(t *testing.T) {
	cases := []struct {
		name      string
		ev        *tcell.EventKey
		wantRune  rune
		wantShift bool
	}{
		{"kitty unshifted", tcell.NewEventKey(tcell.KeyRune, 'p', tcell.ModMeta), 'p', false},
		{"kitty shifted", tcell.NewEventKey(tcell.KeyRune, 'p', tcell.ModMeta|tcell.ModShift), 'p', true},
		{"shifted rune", tcell.NewEventKey(tcell.KeyRune, 'P', tcell.ModMeta), 'p', true},
		{"shifted rune + flag", tcell.NewEventKey(tcell.KeyRune, 'P', tcell.ModMeta|tcell.ModShift), 'p', true},
		{"punctuation", tcell.NewEventKey(tcell.KeyRune, '/', tcell.ModMeta), '/', false},
	}
	for _, tc := range cases {
		r, shift := metaChord(tc.ev)
		if r != tc.wantRune || shift != tc.wantShift {
			t.Errorf("%s: metaChord = (%q, %v), want (%q, %v)", tc.name, r, shift, tc.wantRune, tc.wantShift)
		}
	}
}

// The host sniff, one emulator per marker. Each of these is a terminal
// that reports Command as kitty's super bit; Terminal.app and iTerm2 are
// not (iTerm2 deliberately — see metaAccelArmed).
func TestMetaKittyHostDetection(t *testing.T) {
	cases := []struct {
		name string
		env  map[string]string
		want bool
	}{
		{"kitty by TERM", map[string]string{"TERM": "xterm-kitty"}, true},
		{"kitty by window id", map[string]string{"TERM": "xterm-256color", "KITTY_WINDOW_ID": "3"}, true},
		{"ghostty by TERM", map[string]string{"TERM": "xterm-ghostty"}, true},
		{"ghostty by program", map[string]string{"TERM": "xterm-256color", "TERM_PROGRAM": "ghostty"}, true},
		{"wezterm", map[string]string{"TERM": "xterm-256color", "TERM_PROGRAM": "WezTerm"}, true},
		{"apple terminal", map[string]string{"TERM": "xterm-256color", "TERM_PROGRAM": "Apple_Terminal"}, false},
		{"iterm2", map[string]string{"TERM": "xterm-256color", "TERM_PROGRAM": "iTerm.app"}, false},
		{"bare tmux", map[string]string{"TERM": "screen-256color"}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			for _, k := range []string{"TERM", "TERM_PROGRAM", "KITTY_WINDOW_ID", "GHOSTTY_RESOURCES_DIR", "WEZTERM_PANE"} {
				t.Setenv(k, tc.env[k])
			}
			if got := metaKittyHost(); got != tc.want {
				t.Fatalf("metaKittyHost() = %v, want %v", got, tc.want)
			}
		})
	}
}

// Tier 1 arms the layer on its own, even in a host whose env says
// nothing: inside cats the wire is the kitty protocol by construction,
// and the chords go live the day cats' front end stops holding them
// back (see the file comment). A gate that waited for that day would
// need a ced release to open it.
func TestMetaAccelArmedByTier1(t *testing.T) {
	disarmMetaHost(t)
	a := newTestApp(t, t.TempDir())
	if a.metaAccelArmed() {
		t.Fatal("precondition: a plain terminal must not arm the layer")
	}

	withCtlSpy(t, a)

	if !a.metaAccelArmed() {
		t.Fatal("Tier 1 should arm the ⌘ layer")
	}
}
