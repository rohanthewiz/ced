// =============================================================================
// File: internal/app/whichkey_test.go
// Author: Rohan Allison
// =============================================================================

// Tests for the which-key overlay: the hesitation tick, the
// hold-the-window-open contract, dismissal paths, clickable rows, chord
// re-rendering, and the every-binding-is-labeled invariant the overlay
// depends on.

package app

import (
	"testing"
	"time"

	"github.com/gdamore/tcell/v2"
)

// pressEsc / pressRune drive the real key router, so these tests cover
// the wiring in handleKey, not just the whichkey.go helpers.
func pressEsc(a *App) {
	a.handleKey(tcell.NewEventKey(tcell.KeyEsc, 0, tcell.ModNone))
}

func pressRune(a *App, r rune) {
	a.handleKey(tcell.NewEventKey(tcell.KeyRune, r, tcell.ModNone))
}

// tickWhichKey delivers the pending hesitation tick synchronously — the
// timer goroutine only posts an event, so tests can call the handler
// with the current generation directly.
func tickWhichKey(a *App) {
	a.handleWhichKeyTick(&whichKeyEvent{when: time.Now(), seq: a.whichKey.seq})
}

func TestWhichKeyOpensOnHesitation(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	pressEsc(a)
	if a.whichKey.open {
		t.Fatal("overlay must not open on the Esc itself")
	}
	tickWhichKey(a)
	if !a.whichKey.open {
		t.Fatal("overlay should open when the tick finds the leader still armed")
	}
}

func TestWhichKeyStaleTickIsDropped(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	pressEsc(a)
	staleSeq := a.whichKey.seq
	pressRune(a, 'Q') // unbound: disarms the leader, bumps the generation
	a.handleWhichKeyTick(&whichKeyEvent{when: time.Now(), seq: staleSeq})
	if a.whichKey.open {
		t.Fatal("a tick from a disarmed Esc must not open the overlay")
	}
}

func TestWhichKeyHoldsLeaderWindowOpen(t *testing.T) {
	root := t.TempDir()
	p := writeStatusTestFile(t, root, "wk.txt", "one\ntwo\n")
	a := newTestApp(t, root)
	a.openFile(p)

	pressEsc(a)
	tickWhichKey(a)
	// Simulate reading past the 500ms window.
	a.lastEscape = time.Now().Add(-2 * time.Second)
	pressRune(a, 'j') // Go to line
	if a.modal == nil {
		t.Fatal("leader key should still fire while the overlay is visible")
	}
	if a.whichKey.open {
		t.Fatal("firing a leader action should dismiss the overlay")
	}
}

func TestWhichKeyEscDismissesAndDoubleEscStillMenus(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	pressEsc(a)
	tickWhichKey(a)

	// Slow second Esc (outside doubleEscMs): dismiss, no menu.
	a.lastEscape = time.Now().Add(-time.Second)
	pressEsc(a)
	if a.whichKey.open || a.menuOpen {
		t.Fatal("slow Esc over the overlay should dismiss it, not open the menu")
	}

	// Fast double-Esc: the menu, exactly as without the overlay.
	pressEsc(a)
	pressEsc(a)
	if !a.menuOpen {
		t.Fatal("double-Esc should still open the menu")
	}
	if a.whichKey.open {
		t.Fatal("the menu and the overlay must not coexist")
	}
}

func TestWhichKeyTypingFallsThrough(t *testing.T) {
	root := t.TempDir()
	p := writeStatusTestFile(t, root, "type.txt", "\n")
	a := newTestApp(t, root)
	a.openFile(p)
	tab := a.activeTabPtr()

	pressEsc(a)
	tickWhichKey(a)
	pressRune(a, '1') // unbound rune: dismiss AND type
	if a.whichKey.open {
		t.Fatal("an unbound key should dismiss the overlay")
	}
	if got := tab.Buffer.LineRunes(0); len(got) == 0 || got[0] != '1' {
		t.Fatalf("the unbound key should still reach the buffer; line = %q", string(got))
	}
}

func TestWhichKeyRowsClickable(t *testing.T) {
	root := t.TempDir()
	p := writeStatusTestFile(t, root, "click.txt", "one\ntwo\n")
	a := newTestApp(t, root)
	a.openFile(p)

	pressEsc(a)
	tickWhichKey(a)
	a.drawWhichKey()
	if len(a.whichKey.rows) == 0 {
		t.Fatal("draw should stamp clickable rows")
	}
	// Find the "Go to line" row by resolving entries alongside rows —
	// same order by construction.
	_, entries := a.whichKeyEntries()
	idx := -1
	for i, e := range entries {
		if i >= len(a.whichKey.rows) {
			break
		}
		if e.label == "Go to line" {
			idx = i
			break
		}
	}
	if idx < 0 {
		t.Fatal("no Go to line entry in the overlay")
	}
	r := a.whichKey.rows[idx].rect
	if !a.whichKeyClick(r.x, r.y) {
		t.Fatal("click on a row should be consumed")
	}
	if a.modal == nil {
		t.Fatal("clicking the Go to line row should open its prompt")
	}
	if a.whichKey.open {
		t.Fatal("firing by click should dismiss the overlay")
	}
}

func TestWhichKeyClickOutsideDismissesAndFallsThrough(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	pressEsc(a)
	tickWhichKey(a)
	a.drawWhichKey()

	if a.whichKeyClick(2, 0) { // top row of the window — well above the band
		t.Fatal("a click outside the band should not be consumed")
	}
	if a.whichKey.open {
		t.Fatal("a click outside should dismiss the overlay")
	}
	if !a.lastEscape.IsZero() {
		t.Fatal("a click outside should disarm the leader")
	}
}

func TestWhichKeyChordRerenders(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	pressEsc(a)
	tickWhichKey(a)

	title, _ := a.whichKeyEntries()
	if title == "" || a.leaderChord != nil {
		t.Fatal("precondition: top-level table")
	}
	pressRune(a, 'a') // arm the AI namespace
	if a.leaderChord == nil {
		t.Fatal("Esc-a should arm the AI chord")
	}
	if !a.whichKey.open {
		t.Fatal("arming a chord must keep the visible overlay open")
	}
	title, entries := a.whichKeyEntries()
	if title[:2] != "AI" {
		t.Fatalf("overlay should re-render for the namespace; title %q", title)
	}
	found := false
	for _, e := range entries {
		if e.label == "Use skill" {
			found = true
		}
	}
	if !found {
		t.Fatal("namespace table should list its own entries")
	}
	// The chord window is held open while the overlay shows it.
	a.leaderChordAt = time.Now().Add(-time.Minute)
	if !a.handleChordKey(tcell.NewEventKey(tcell.KeyRune, 's', tcell.ModNone)) {
		t.Fatal("chord key should still resolve while the overlay is visible")
	}
	if a.whichKey.open {
		t.Fatal("resolving the chord should dismiss the overlay")
	}
}

func TestWhichKeyEveryLeaderBindingLabeled(t *testing.T) {
	// The overlay hides unlabeled bindings, so a label-less entry is a
	// silent documentation hole. Keep the table honest.
	for _, b := range leaderBindings() {
		if b.label == "" {
			t.Errorf("leader binding %q has no which-key label", string(b.key))
		}
	}
	for _, b := range aiLeaderBindings() {
		if b.label == "" {
			t.Errorf("AI binding %q has no which-key label", string(b.key))
		}
	}
}

func TestWhichKeyGeometryStaysOnScreen(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	a.width = 40 // narrow: forces multiple rows and the clamp
	pressEsc(a)
	tickWhichKey(a)
	a.drawWhichKey()

	for _, r := range a.whichKey.rows {
		if r.rect.x < 0 || r.rect.x+r.rect.w > a.width {
			t.Fatalf("row rect out of bounds: %+v", r.rect)
		}
		if r.rect.y >= a.height-1 {
			t.Fatal("rows must sit above the status bar")
		}
	}
	b := a.whichKey.band
	if b.h > a.height/2+2 {
		t.Fatalf("band too tall: %d of %d rows", b.h, a.height)
	}
}
