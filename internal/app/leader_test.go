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
	"testing"
	"time"

	"github.com/gdamore/tcell/v2"
)

// TestLeaderActionFor_AllBindingsResolve walks the binding table and
// verifies every entry returns a non-nil action. Catches accidentally
// dropping a method reference when the table is reshuffled.
func TestLeaderActionFor_AllBindingsResolve(t *testing.T) {
	for _, b := range leaderBindings() {
		if leaderActionFor(b.key) == nil {
			t.Errorf("binding %q resolved to nil", b.key)
		}
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

// TestHandleKey_LeaderPaletteAliases pins down that BOTH palette leaders
// — Esc-a (actions) and Esc-k (the Cmd+K muscle-memory alias) — open the
// command palette modal. Guards against one alias being dropped when the
// table is reshuffled.
func TestHandleKey_LeaderPaletteAliases(t *testing.T) {
	for _, key := range []rune{'a', 'k'} {
		a := newTestApp(t, t.TempDir())
		a.handleKey(keyEv(tcell.KeyEsc, 0))
		a.handleKey(keyEv(tcell.KeyRune, key))
		if _, ok := a.modal.(*paletteModal); !ok {
			t.Errorf("Esc-%c should open the command palette, modal is %T", key, a.modal)
		}
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
