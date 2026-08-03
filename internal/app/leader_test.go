// =============================================================================
// File: internal/app/leader_test.go
// Author: Rohan Allison <rohanthewiz@gmail.com>
// Created: 2026-04-30
// Copyright: 2026 Rohan Allison. All rights reserved.
// Portions copyright 2026 Cloudmanic, LLC. Original author: Spicer Matthews.
// =============================================================================

package app

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gdamore/tcell/v2"
)

// TestLeaderActionFor_AllBindingsResolve walks the binding table and
// verifies every entry resolves to something that can fire: an action,
// or a prefix carrying a populated sub-table and the hint that makes it
// discoverable. Catches accidentally dropping a method reference when
// the table is reshuffled.
func TestLeaderActionFor_AllBindingsResolve(t *testing.T) {
	for _, b := range leaderBindings() {
		if b.sub != nil || b.subFor != nil {
			if b.action != nil {
				t.Errorf("prefix %q also carries an action — it can only do one", b.key)
			}
			if b.name == "" {
				t.Errorf("prefix %q has no name — a missed chord can't say which namespace it missed", b.key)
			}
			// A DYNAMIC prefix (Esc-x, the plugin namespace) resolves
			// its table from App state, so there is nothing to count
			// here — legitimately empty on a machine with no plugins.
			// It must still carry a resolver for both halves, or the
			// namespace arms with no bindings and no way to see that.
			if b.subFor != nil {
				if b.hintFor == nil {
					t.Errorf("dynamic prefix %q has no hintFor — its namespace would be undiscoverable", b.key)
				}
				continue
			}
			if len(b.sub) == 0 || b.hint == "" {
				t.Errorf("prefix %q has %d sub-bindings and hint %q", b.key, len(b.sub), b.hint)
			}
			continue
		}
		if leaderActionFor(b.key) == nil {
			t.Errorf("binding %q resolved to nil", b.key)
		}
	}
	for _, b := range aiLeaderBindings() {
		if b.action == nil {
			t.Errorf("AI binding %q resolved to nil", b.key)
		}
		if b.sub != nil {
			t.Errorf("AI binding %q nests another namespace — one level is the limit", b.key)
		}
		if b.repeat {
			t.Errorf("AI binding %q is repeatable; these all open a panel or picker", b.key)
		}
	}
}

// TestAILeaderHint_ListsEveryBinding keeps the flashed hint honest: it's
// the namespace's only discovery surface from the keyboard, so a binding
// missing from it is a binding nobody finds.
func TestAILeaderHint_ListsEveryBinding(t *testing.T) {
	var prefix *leaderBinding
	for _, b := range leaderBindings() {
		if b.key == 'a' {
			b := b
			prefix = &b
		}
	}
	if prefix == nil {
		t.Fatal("Esc-a is no longer the AI prefix")
	}
	for _, sub := range prefix.sub {
		if !strings.Contains(prefix.hint, string(sub.key)+" ") {
			t.Errorf("hint %q omits binding %q", prefix.hint, sub.key)
		}
	}
}

// TestLeaderChord_FiresSubBinding drives the two-key gesture end to end:
// Esc-a arms (flashing the hint, running nothing), and the next rune
// dispatches from the AI table.
func TestLeaderChord_FiresSubBinding(t *testing.T) {
	a := newTestApp(t, t.TempDir())

	a.handleKey(keyEv(tcell.KeyEsc, 0))
	a.handleKey(keyEv(tcell.KeyRune, 'a'))
	if a.leaderChord == nil {
		t.Fatal("Esc-a should arm the chord, not act")
	}
	if a.modal != nil {
		t.Fatalf("Esc-a opened %T — the prefix must run no action", a.modal)
	}
	if !strings.Contains(a.statusMsg, "chat") {
		t.Errorf("arming flash = %q, want the namespace hint", a.statusMsg)
	}

	a.handleKey(keyEv(tcell.KeyRune, 't')) // tools → MCP servers
	if a.leaderChord != nil {
		t.Error("the chord should disarm once its second rune lands")
	}
	if a.modal == nil {
		t.Fatal("Esc-a-t should have opened the MCP surface")
	}
}

// TestLeaderChord_TmuxAltPath pins the namespace inside tmux, the
// editor's primary habitat: tmux folds "Esc a" into one Alt+a event, so
// the prefix must arm from that path too, with the second rune arriving
// bare.
func TestLeaderChord_TmuxAltPath(t *testing.T) {
	a := newTestApp(t, t.TempDir())

	a.handleKey(tcell.NewEventKey(tcell.KeyRune, 'a', tcell.ModAlt))
	if a.leaderChord == nil {
		t.Fatal("Alt+a should arm the chord (tmux-folded Esc-a)")
	}
	a.handleKey(keyEv(tcell.KeyRune, 't'))
	if a.modal == nil {
		t.Fatal("Alt+a then t should have opened the MCP surface")
	}
}

// TestLeaderChord_UnboundRuneIsSwallowed pins the one place chords
// deliberately differ from the flat table: a miss inside a live chord
// flashes instead of falling through, because "Esc a" is two deliberate
// keys and dropping a stray character into the user's code is the worse
// reading of a typo.
func TestLeaderChord_UnboundRuneIsSwallowed(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "t.txt")
	if err := os.WriteFile(target, []byte(""), 0644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	a := newTestApp(t, dir)
	a.openFile(target)

	a.handleKey(keyEv(tcell.KeyEsc, 0))
	a.handleKey(keyEv(tcell.KeyRune, 'a'))
	a.handleKey(keyEv(tcell.KeyRune, 'j')) // not in the AI table

	if got := a.activeTabPtr().Buffer.Lines[0]; got != "" {
		t.Errorf("buffer = %q, want the miss swallowed", got)
	}
	if !strings.Contains(a.statusMsg, "No AI action") {
		t.Errorf("flash = %q, want the miss explained", a.statusMsg)
	}
	// And the next keystroke types normally — a miss disarms.
	a.handleKey(keyEv(tcell.KeyRune, 'j'))
	if got := a.activeTabPtr().Buffer.Lines[0]; got != "j" {
		t.Errorf("buffer = %q, want j typed after the chord disarmed", got)
	}
}

// TestLeaderChord_ExpiresAndEscCancels pins both exits from a half-typed
// chord: the window times out, and Esc drops it the way Esc drops
// everything else in this editor.
func TestLeaderChord_ExpiresAndEscCancels(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "t.txt")
	if err := os.WriteFile(target, []byte(""), 0644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	a := newTestApp(t, dir)
	a.openFile(target)

	// Expired: the rune types normally instead of being claimed.
	a.leaderChord = aiLeaderBindings()
	a.leaderChordAt = time.Now().Add(-2 * leaderChordFor)
	a.handleKey(keyEv(tcell.KeyRune, 't'))
	if a.modal != nil {
		t.Fatalf("an expired chord fired %T", a.modal)
	}
	if got := a.activeTabPtr().Buffer.Lines[0]; got != "t" {
		t.Errorf("buffer = %q, want the rune typed after expiry", got)
	}

	// Esc cancels a live chord and still arms the ordinary leader, so
	// "Esc a Esc s" saves rather than doing nothing.
	a.handleKey(keyEv(tcell.KeyEsc, 0))
	a.handleKey(keyEv(tcell.KeyRune, 'a'))
	if a.leaderChord == nil {
		t.Fatal("chord did not arm")
	}
	a.handleKey(keyEv(tcell.KeyEsc, 0))
	if a.leaderChord != nil {
		t.Error("Esc should drop a pending chord")
	}
	a.handleKey(keyEv(tcell.KeyRune, 's'))
	if a.activeTabPtr().Dirty {
		t.Error("Esc-a Esc-s should have saved")
	}
}

// TestLeaderPalette_KeepsEscK pins the binding the AI namespace took
// 'a' from: the palette still answers to Esc-k.
func TestLeaderPalette_KeepsEscK(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	a.handleKey(keyEv(tcell.KeyEsc, 0))
	a.handleKey(keyEv(tcell.KeyRune, 'k'))
	pm, ok := a.modal.(*paletteModal)
	if !ok || pm.title != paletteMenuLabel {
		t.Fatalf("Esc-k opened %T, want the command palette", a.modal)
	}
}

// TestLeaderActionFor_UnboundReturnsNil pins down the contract that
// leaderActionFor reports a miss with nil so handleKey can distinguish
// "leader fired" from "key was unbound — fall through".
func TestLeaderActionFor_UnboundReturnsNil(t *testing.T) {
	if leaderActionFor('y') != nil {
		t.Fatal("'y' should not be a leader binding (no editor action mapped)")
	}
}

// TestHandleKey_LeaderSave saves the active tab via Esc, s. The buffer
// is dirtied before the leader fires so the assertion is meaningful:
// a successful save flips the dirty flag back to false.
func TestHandleKey_LeaderSave(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "t.txt")
	if err := os.WriteFile(target, []byte(""), 0644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	a := newTestApp(t, dir)
	a.openFile(target)
	a.handleKey(keyEv(tcell.KeyRune, 'x')) // dirty the buffer
	if !a.activeTabPtr().Dirty {
		t.Fatal("expected dirty buffer before save")
	}

	a.handleKey(keyEv(tcell.KeyEsc, 0))
	a.handleKey(keyEv(tcell.KeyRune, 's'))

	if a.activeTabPtr().Dirty {
		t.Fatal("Esc-s should have saved the buffer (dirty still true)")
	}
}

// TestHandleKey_LeaderUndoRedo round-trips an edit through Esc-u and
// Esc-r. Pins down both bindings at once and the fact that the leader
// state resets between sequences (we re-arm Esc each time).
func TestHandleKey_LeaderUndoRedo(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "t.txt")
	if err := os.WriteFile(target, []byte(""), 0644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	a := newTestApp(t, dir)
	a.openFile(target)
	a.handleKey(keyEv(tcell.KeyRune, 'a'))

	a.handleKey(keyEv(tcell.KeyEsc, 0))
	a.handleKey(keyEv(tcell.KeyRune, 'u'))
	if a.activeTabPtr().Buffer.Lines[0] != "" {
		t.Fatalf("Esc-u should have undone the insert, got %q", a.activeTabPtr().Buffer.Lines[0])
	}

	a.handleKey(keyEv(tcell.KeyEsc, 0))
	a.handleKey(keyEv(tcell.KeyRune, 'r'))
	if a.activeTabPtr().Buffer.Lines[0] != "a" {
		t.Fatalf("Esc-r should have redone the insert, got %q", a.activeTabPtr().Buffer.Lines[0])
	}
}

// TestHandleKey_LeaderUndoRedoAliases round-trips the same edit through
// Esc-z and Esc-Z — the Cmd+Z muscle-memory aliases for undo/redo. A
// separate test from the u/r pair so dropping either alias fails loudly
// on its own, same as the palette-aliases test.
func TestHandleKey_LeaderUndoRedoAliases(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "t.txt")
	if err := os.WriteFile(target, []byte(""), 0644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	a := newTestApp(t, dir)
	a.openFile(target)
	a.handleKey(keyEv(tcell.KeyRune, 'a'))

	a.handleKey(keyEv(tcell.KeyEsc, 0))
	a.handleKey(keyEv(tcell.KeyRune, 'z'))
	if a.activeTabPtr().Buffer.Lines[0] != "" {
		t.Fatalf("Esc-z should have undone the insert, got %q", a.activeTabPtr().Buffer.Lines[0])
	}

	a.handleKey(keyEv(tcell.KeyEsc, 0))
	a.handleKey(keyEv(tcell.KeyRune, 'Z'))
	if a.activeTabPtr().Buffer.Lines[0] != "a" {
		t.Fatalf("Esc-Z should have redone the insert, got %q", a.activeTabPtr().Buffer.Lines[0])
	}
}

// TestHandleKey_LeaderUndoChainRepeats pins down leader chaining: after
// a repeatable leader fires, further repeatable runes inside the window
// keep firing without a fresh Esc. The regression this guards: "Esc z z"
// with an exhausted undo stack used to type a literal 'z' into the
// buffer, because only the first z was consumed by the leader.
func TestHandleKey_LeaderUndoChainRepeats(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "t.txt")
	if err := os.WriteFile(target, []byte(""), 0644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	a := newTestApp(t, dir)
	a.openFile(target)
	a.handleKey(keyEv(tcell.KeyRune, 'a'))

	a.handleKey(keyEv(tcell.KeyEsc, 0))
	a.handleKey(keyEv(tcell.KeyRune, 'z')) // undo — buffer back to ""
	a.handleKey(keyEv(tcell.KeyRune, 'z')) // chained: stack empty, must NOT insert
	if got := a.activeTabPtr().Buffer.Lines[0]; got != "" {
		t.Fatalf("chained Esc-z z with empty stack should be consumed, got %q", got)
	}
	a.handleKey(keyEv(tcell.KeyRune, 'Z')) // chained redo — no fresh Esc needed
	if got := a.activeTabPtr().Buffer.Lines[0]; got != "a" {
		t.Fatalf("chained Z should redo the insert, got %q", got)
	}
}

// TestHandleKey_LeaderChainAdmitsOnlyRepeatable verifies chain mode is
// narrower than a real Esc: a non-repeatable binding rune typed right
// after a chained action must insert literally instead of firing (so
// quick typing after an undo can't trigger, say, a save via 's').
func TestHandleKey_LeaderChainAdmitsOnlyRepeatable(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "t.txt")
	if err := os.WriteFile(target, []byte(""), 0644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	a := newTestApp(t, dir)
	a.openFile(target)
	a.handleKey(keyEv(tcell.KeyRune, 'a'))

	a.handleKey(keyEv(tcell.KeyEsc, 0))
	a.handleKey(keyEv(tcell.KeyRune, 'z')) // undo — arms chain mode
	a.handleKey(keyEv(tcell.KeyRune, 's')) // not repeatable — plain keystroke
	if got := a.activeTabPtr().Buffer.Lines[0]; got != "s" {
		t.Fatalf("'s' after a chained undo should insert literally, got %q", got)
	}
}

// TestHandleKey_EscAfterChainReopensFullTable makes sure a real Esc
// clears chain mode: Esc pressed mid-chain must re-arm the FULL leader
// table, so a non-repeatable binding like Esc-t still fires.
func TestHandleKey_EscAfterChainReopensFullTable(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "t.txt")
	if err := os.WriteFile(target, []byte(""), 0644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	a := newTestApp(t, dir)
	a.openFile(target)
	a.handleKey(keyEv(tcell.KeyRune, 'a'))

	a.handleKey(keyEv(tcell.KeyEsc, 0))
	a.handleKey(keyEv(tcell.KeyRune, 'z')) // undo — arms chain mode
	before := a.sidebarShown
	a.handleKey(keyEv(tcell.KeyEsc, 0))
	a.handleKey(keyEv(tcell.KeyRune, 't'))
	if a.sidebarShown == before {
		t.Fatal("Esc-t after a chained undo should still toggle the sidebar")
	}
}

// TestHandleKey_CmdZUndoRedo covers the Cmd+Z / Cmd+Shift+Z path used by
// hosts that forward Cmd chords via the kitty keyboard protocol (the
// cats mac app, kitty/Ghostty/WezTerm). tcell reports Cmd as ModMeta.
func TestHandleKey_CmdZUndoRedo(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "t.txt")
	if err := os.WriteFile(target, []byte(""), 0644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	a := newTestApp(t, dir)
	a.openFile(target)
	a.handleKey(keyEv(tcell.KeyRune, 'a'))

	a.handleKey(tcell.NewEventKey(tcell.KeyRune, 'z', tcell.ModMeta))
	if got := a.activeTabPtr().Buffer.Lines[0]; got != "" {
		t.Fatalf("Cmd+Z should undo the insert, got %q", got)
	}
	// Shifted redo may arrive as 'Z' or as 'z'+ModShift depending on the
	// emitter — exercise both encodings.
	a.handleKey(tcell.NewEventKey(tcell.KeyRune, 'Z', tcell.ModMeta|tcell.ModShift))
	if got := a.activeTabPtr().Buffer.Lines[0]; got != "a" {
		t.Fatalf("Cmd+Shift+Z (rune Z) should redo the insert, got %q", got)
	}
	a.handleKey(tcell.NewEventKey(tcell.KeyRune, 'z', tcell.ModMeta))
	a.handleKey(tcell.NewEventKey(tcell.KeyRune, 'z', tcell.ModMeta|tcell.ModShift))
	if got := a.activeTabPtr().Buffer.Lines[0]; got != "a" {
		t.Fatalf("Cmd+Shift+Z (rune z + shift) should redo the insert, got %q", got)
	}
}

// TestHandleKey_LeaderToggleSidebar flips sidebarShown via Esc-t. The
// toggle is the simplest leader action with no preconditions, so it's
// the most stable smoke test that the dispatch wiring is intact.
func TestHandleKey_LeaderToggleSidebar(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	before := a.sidebarShown
	a.handleKey(keyEv(tcell.KeyEsc, 0))
	a.handleKey(keyEv(tcell.KeyRune, 't'))
	if a.sidebarShown == before {
		t.Fatalf("Esc-t should toggle sidebar (still %v)", a.sidebarShown)
	}
}

// TestHandleKey_LeaderToggleLineComment binds Esc-/ to the same action menu
// path, giving keyboard users a fast toggle without adding Ctrl shortcuts.
func TestHandleKey_LeaderToggleLineComment(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "main.go")
	if err := os.WriteFile(target, []byte("one\ntwo"), 0644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	a := newTestApp(t, dir)
	a.openFile(target)

	a.handleKey(keyEv(tcell.KeyEsc, 0))
	a.handleKey(keyEv(tcell.KeyRune, '/'))

	if got := a.activeTabPtr().Buffer.String(); got != "// one\ntwo" {
		t.Fatalf("Esc-/ should comment the cursor line, got %q", got)
	}
}

// TestHandleKey_LeaderPalette pins Esc-k as the palette's leader. It
// used to share the binding with Esc-a ("actions"); 'a' now opens the AI
// namespace, so this also guards the split — Esc-a must NOT open the
// palette, or the chord it arms would be unreachable.
func TestHandleKey_LeaderPalette(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	a.handleKey(keyEv(tcell.KeyEsc, 0))
	a.handleKey(keyEv(tcell.KeyRune, 'k'))
	if _, ok := a.modal.(*paletteModal); !ok {
		t.Errorf("Esc-k should open the command palette, modal is %T", a.modal)
	}

	b := newTestApp(t, t.TempDir())
	b.handleKey(keyEv(tcell.KeyEsc, 0))
	b.handleKey(keyEv(tcell.KeyRune, 'a'))
	if b.modal != nil {
		t.Errorf("Esc-a opened %T — it is the AI prefix now, not the palette", b.modal)
	}
}

// TestHandleKey_LeaderQuit sets a.quit via Esc-q. We test this directly
// rather than through Run() so we don't have to drive the event loop —
// the quit flag is what Run() polls each tick.
func TestHandleKey_LeaderQuit(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	a.handleKey(keyEv(tcell.KeyEsc, 0))
	a.handleKey(keyEv(tcell.KeyRune, 'q'))
	if !a.quit {
		t.Fatal("Esc-q should set a.quit = true")
	}
}

// TestHandleKey_LeaderUnboundFallsThrough is the regression test for the
// "stray Esc shouldn't swallow the next keystroke" property: pressing
// Esc and then an unbound letter must still deliver that letter to the
// active tab. Without the fall-through, an accidental Esc tap would
// silently eat the user's next character.
func TestHandleKey_LeaderUnboundFallsThrough(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "t.txt")
	if err := os.WriteFile(target, []byte(""), 0644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	a := newTestApp(t, dir)
	a.openFile(target)

	a.handleKey(keyEv(tcell.KeyEsc, 0))
	a.handleKey(keyEv(tcell.KeyRune, 'y'))

	if got := a.activeTabPtr().Buffer.Lines[0]; got != "y" {
		t.Fatalf("unbound key after Esc should reach the editor, got %q", got)
	}
}

// TestHandleKey_LeaderTimesOut verifies the leader window expires:
// after doubleEscMs has passed since the last Esc, a bound letter must
// reach the editor as a normal keystroke instead of firing the action.
func TestHandleKey_LeaderTimesOut(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "t.txt")
	if err := os.WriteFile(target, []byte(""), 0644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	a := newTestApp(t, dir)
	a.openFile(target)

	a.handleKey(keyEv(tcell.KeyEsc, 0))
	// Backdate the Esc timestamp past the leader window so the next 's'
	// is treated as a plain keystroke rather than Save.
	a.lastEscape = time.Now().Add(-2 * doubleEscMs)
	a.handleKey(keyEv(tcell.KeyRune, 's'))

	if got := a.activeTabPtr().Buffer.Lines[0]; got != "s" {
		t.Fatalf("expired leader window: 's' should insert literally, got %q", got)
	}
}

// TestHandleKey_EscDoubleTapStillOpensMenu makes sure adding the leader
// table didn't break the existing double-Esc-opens-menu gesture. The
// second Esc inside the leader window must still be interpreted as
// "open the menu," not as an unbound leader keystroke.
func TestHandleKey_EscDoubleTapStillOpensMenu(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	a.handleKey(keyEv(tcell.KeyEsc, 0))
	a.handleKey(keyEv(tcell.KeyEsc, 0))
	if !a.menuOpen {
		t.Fatal("double-Esc should still open the menu after leader was added")
	}
}

// TestPluginChord_ArmsAndFires pins the Esc-x namespace end to end: the
// prefix arms from the user's installed plugins, and the second rune
// runs the command that claimed it.
func TestPluginChord_ArmsAndFires(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	sh := (&fakeShell{stdout: "done"}).install(t)
	installPlugin(t, a, "tools", `{"commands":[
		{"label":"Build","leader":"b","command":"make","output":"flash"}
	]}`)

	a.handleKey(keyEv(tcell.KeyEsc, 0))
	a.handleKey(keyEv(tcell.KeyRune, 'x'))
	if a.leaderChord == nil {
		t.Fatal("Esc-x should arm the plugin namespace")
	}
	if a.leaderChordName != "Plugin" {
		t.Errorf("chord name = %q, want Plugin", a.leaderChordName)
	}
	a.handleKey(keyEv(tcell.KeyRune, 'b'))
	pumpAppEvents(t, a, func() bool { return sh.count() > 0 })

	if got := sh.last(t).command; got != "make" {
		t.Errorf("Esc-x b ran %q, want make", got)
	}
}

// TestPluginChord_EmptyNamespaceArmsNothing pins the dynamic prefix's
// one real difference from a static one. With no plugin bound there is
// nothing for a second rune to resolve against, so the namespace must
// NOT arm — otherwise "Esc x" would swallow the next keystroke on the
// overwhelmingly common machine that has no plugins installed.
func TestPluginChord_EmptyNamespaceArmsNothing(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "t.txt")
	if err := os.WriteFile(target, []byte(""), 0644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	a := newTestApp(t, dir)
	a.openFile(target)

	a.handleKey(keyEv(tcell.KeyEsc, 0))
	a.handleKey(keyEv(tcell.KeyRune, 'x'))
	if a.leaderChord != nil {
		t.Fatal("an empty plugin namespace must not arm a chord")
	}
	if !strings.Contains(a.statusMsg, "no leader keys bound") {
		t.Errorf("flash = %q, want it to say the namespace is empty", a.statusMsg)
	}

	// And the next keystroke must reach the buffer, not vanish.
	a.handleKey(keyEv(tcell.KeyRune, 'z'))
	if got := a.activeTabPtr().Buffer.Lines[0]; got != "z" {
		t.Errorf("buffer = %q, want the swallowed-nothing 'z'", got)
	}
}

// TestPluginChord_TmuxAltPath pins the namespace inside tmux, where
// "Esc x" arrives folded as one Alt+x event. Both entry paths funnel
// through fireLeader, so this is what proves the dynamic prefix didn't
// break that.
func TestPluginChord_TmuxAltPath(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	sh := (&fakeShell{stdout: "done"}).install(t)
	installPlugin(t, a, "tools", `{"commands":[
		{"label":"Build","leader":"b","command":"make","output":"flash"}
	]}`)

	a.handleKey(tcell.NewEventKey(tcell.KeyRune, 'x', tcell.ModAlt))
	if a.leaderChord == nil {
		t.Fatal("Alt+x should arm the plugin namespace (tmux-folded Esc-x)")
	}
	a.handleKey(keyEv(tcell.KeyRune, 'b'))
	pumpAppEvents(t, a, func() bool { return sh.count() > 0 })
	if got := sh.last(t).command; got != "make" {
		t.Errorf("Alt+x then b ran %q, want make", got)
	}
}

// TestChordMissNamesItsNamespace pins the generalized miss message. With
// two namespaces the old hardcoded "No AI action bound to…" would lie
// about which one the user was in.
func TestChordMissNamesItsNamespace(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	installPlugin(t, a, "tools", `{"commands":[
		{"label":"Build","leader":"b","command":"make"}
	]}`)

	a.handleKey(keyEv(tcell.KeyEsc, 0))
	a.handleKey(keyEv(tcell.KeyRune, 'x'))
	a.handleKey(keyEv(tcell.KeyRune, 'j')) // not bound by any plugin
	if !strings.Contains(a.statusMsg, "No Plugin action") {
		t.Errorf("flash = %q, want it to name the Plugin namespace", a.statusMsg)
	}
	if !strings.Contains(a.statusMsg, "esc x") {
		t.Errorf("flash = %q, want it to name the prefix to press again", a.statusMsg)
	}

	// The AI namespace keeps its own wording.
	a.handleKey(keyEv(tcell.KeyEsc, 0))
	a.handleKey(keyEv(tcell.KeyRune, 'a'))
	a.handleKey(keyEv(tcell.KeyRune, 'j'))
	if !strings.Contains(a.statusMsg, "No AI action") {
		t.Errorf("flash = %q, want the AI namespace named", a.statusMsg)
	}
}
