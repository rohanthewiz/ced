// =============================================================================
// File: internal/app/hoverdwell_test.go
// Author: Rohan Allison <rohanthewiz@gmail.com>
// Created: 2026-08-13
// Copyright: 2026 Rohan Allison. All rights reserved.
// =============================================================================

// Tests for hover on mouse dwell. The tooltip's whole risk profile is
// "a box appears over the user's code without being asked", so most of
// what is pinned here is the REFUSALS: where it must not ask, when an
// answer must not paint, and what makes an open tooltip go away.

package app

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gdamore/tcell/v2"

	"github.com/rohanthewiz/ced/internal/cats"
	"github.com/rohanthewiz/ced/internal/editor"
	"github.com/rohanthewiz/ced/internal/lsp"
)

// dwellSource is the seeded file every test here hovers over. Line 2's
// `alpha` is the identifier under test; line 3 is deliberately short so
// there are cells to the right of it that are past end-of-line.
var dwellSource = []string{
	"package main",
	"",
	"func alpha() int { return 1 }",
	"var x = 2",
}

// newDwellApp builds a Tier-1 app with a fake LSP connection and the
// seeded file open, the caret parked at the very top. The caret's
// position matters: every test that asserts what reached the wire is
// really asserting "the pointer, not that".
func newDwellApp(t *testing.T) (*App, *fakeLSPConn, string) {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "main.go")
	if err := os.WriteFile(path, []byte(strings.Join(dwellSource, "\n")+"\n"), 0644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	a := newTestApp(t, dir)
	fake := &fakeLSPConn{hoverRes: hoverAnswer("func alpha() int")}
	a.lsp.dead = false
	a.lsp.client = fake
	a.openFile(path)
	caretTo(a, 0, 0)
	dwellTier1(a)
	return a, fake, path
}

// dwellTier1 puts the app in the state hoverDwellArmed asks about: a
// live control client and capabilities that say Tier 1.
func dwellTier1(a *App) {
	a.cats.caps = cats.Caps{InCats: true, Control: true, ControlSocket: "/x.sock", PaneHandle: "p_1"}
	a.cats.client = cats.NewClient("/x.sock")
}

// hoverAnswer wraps plain text in the MarkupContent shape a server sends.
func hoverAnswer(text string) *lsp.Hover {
	b, _ := json.Marshal(map[string]string{"kind": "plaintext", "value": text})
	return &lsp.Hover{Contents: json.RawMessage(b)}
}

// cellFor returns the screen cell the renderer puts (line, col) in.
// Tests locate the pointer through the editor's own arithmetic rather
// than hard-coding a gutter width that is not theirs to know.
func cellFor(t *testing.T, a *App, line, col int) (int, int) {
	t.Helper()
	ex, ey, ew, eh := a.editorRect()
	dx, dy, ok := a.activeTabPtr().PosScreenCell(editor.Position{Line: line, Col: col}, ew, eh)
	if !ok {
		t.Fatalf("position %d:%d is not on screen", line, col)
	}
	return ex + dx, ey + dy
}

// dwellFire runs the whole request half for the cell the pointer is on:
// the tick the timer would have posted, then a wait for the background
// request to reach the fake. It returns the seq the request went out
// under, which is what a response has to carry to be believed.
func dwellFire(t *testing.T, a *App, fake *fakeLSPConn) int {
	t.Helper()
	before := func() int { _, n := fake.hoverAsked(); return n }()
	seq := a.hoverDwell.seq
	a.handleHoverDwellTick(&hoverDwellEvent{when: time.Now(), seq: seq})
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if _, n := fake.hoverAsked(); n > before {
			return seq
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("no hover request reached the server")
	return 0
}

// dwellAnswer delivers a response as the request goroutine would have.
func dwellAnswer(a *App, path string, seq, ax, ay int, text string) {
	a.handleLSPHover(&lspHoverEvent{
		when: time.Now(), path: path, text: text,
		dwell: true, seq: seq, ax: ax, ay: ay,
	})
}

// -----------------------------------------------------------------------------
// The feature working
// -----------------------------------------------------------------------------

// The load-bearing claim: the question asked is about the cell under the
// POINTER, not the position of the caret — which is what separates this
// from the hover verb ced already had.
func TestHoverDwellAsksAboutThePointerNotTheCaret(t *testing.T) {
	a, fake, path := newDwellApp(t)
	x, y := cellFor(t, a, 2, 7) // inside `alpha`

	if a.notePointer(x, y, tcell.ButtonNone) {
		t.Fatal("a motion event must never be consumed")
	}
	seq := dwellFire(t, a, fake)

	pos, _ := fake.hoverAsked()
	if pos.Line != 2 || pos.Character != 7 {
		t.Fatalf("asked about %d:%d, want 2:7 (the caret is at 0:0)", pos.Line, pos.Character)
	}

	dwellAnswer(a, path, seq, x, y, "func alpha() int")
	if !a.hoverDwellVisible() {
		t.Fatal("a live answer with text must open the tooltip")
	}
	if a.hoverDwell.ax != x || a.hoverDwell.ay != y {
		t.Fatalf("anchored at %d,%d, want the pointer's cell %d,%d",
			a.hoverDwell.ax, a.hoverDwell.ay, x, y)
	}
	// The modal slot must be untouched: this surface is ambient, and a
	// tooltip that took the slot would block the next dialog the user
	// opened and swallow their keyboard on the way.
	if a.modal != nil {
		t.Fatal("the dwell tooltip must not claim the modal slot")
	}
}

// The tooltip hangs off the cell it describes and never covers it —
// below by default, flipped above when the window would clip it.
func TestHoverDwellBoxNeverCoversItsAnchor(t *testing.T) {
	a, fake, path := newDwellApp(t)

	x, y := cellFor(t, a, 2, 7)
	a.notePointer(x, y, tcell.ButtonNone)
	seq := dwellFire(t, a, fake)
	dwellAnswer(a, path, seq, x, y, "func alpha() int")
	a.drawHoverDwell()
	if b := a.hoverDwell.box; b.w == 0 || b.h == 0 {
		t.Fatal("draw stamped no rect")
	}
	if a.hoverDwellContains(x, y) {
		t.Fatal("the box covers its own anchor cell")
	}
	a.closeHoverDwell()

	// Anchored on the last usable row, the box has to flip above rather
	// than run off the bottom (or over the status bar).
	a.hoverDwell.open = true
	a.hoverDwell.lines = []string{"func alpha() int"}
	a.hoverDwell.ax, a.hoverDwell.ay = 10, a.height-2
	a.drawHoverDwell()
	if b := a.hoverDwell.box; b.y+b.h > a.height-1 {
		t.Fatalf("box bottom %d runs past the last free row %d", b.y+b.h, a.height-1)
	}
}

// -----------------------------------------------------------------------------
// Where it must not ask
// -----------------------------------------------------------------------------

// Tier 0 is silent: no arming, so no tick, so no request. The explicit
// hover verb is still there — this layer is a bonus, never the only path
// to the capability.
func TestHoverDwellIsTier1Only(t *testing.T) {
	a, fake, _ := newDwellApp(t)
	a.cats.client = nil // back to Tier 0
	if a.hoverDwellArmed() {
		t.Fatal("Tier 0 must not arm the dwell layer")
	}

	x, y := cellFor(t, a, 2, 7)
	seq := a.hoverDwell.seq
	a.notePointer(x, y, tcell.ButtonNone)
	if a.hoverDwell.seq != seq {
		t.Fatal("Tier 0 motion must not schedule anything")
	}
	// Even a tick delivered by hand (a timer from before the link
	// dropped) must not reach the wire.
	a.handleHoverDwellTick(&hoverDwellEvent{when: time.Now(), seq: a.hoverDwell.seq})
	if _, n := fake.hoverAsked(); n != 0 {
		t.Fatalf("%d hover requests at Tier 0, want 0", n)
	}
}

// The pointer must be ON an identifier rune. Everything else — the
// gutter, whitespace, the space past end-of-line, a row outside the
// editor — is a cell the user is not pointing at anything in, and
// HitTest's "nearest column" answer would invent a symbol for it.
func TestHoverDwellPosRefusesEverythingButIdentifiers(t *testing.T) {
	a, _, _ := newDwellApp(t)
	tab := a.activeTabPtr()
	ex, ey, _, _ := a.editorRect()

	onAlpha := func() (int, int) { return cellFor(t, a, 2, 7) }
	ax, ay := onAlpha()

	// The control case: the same row, on the identifier, is accepted.
	if pos, ok := a.hoverDwellPos(tab, ax, ay); !ok || pos.Line != 2 || pos.Col != 7 {
		t.Fatalf("on `alpha`: got %v ok=%v, want 2:7 accepted", pos, ok)
	}

	spaceX, spaceY := cellFor(t, a, 2, 4) // the space in "func alpha"
	eolX, eolY := cellFor(t, a, 3, len(dwellSource[3])-1)
	parenX, parenY := cellFor(t, a, 2, 10) // the '(' after alpha

	for _, tc := range []struct {
		name string
		x, y int
	}{
		{"the line-number gutter", ex, ay},
		{"whitespace between tokens", spaceX, spaceY},
		{"punctuation", parenX, parenY},
		{"past end of line", eolX + 4, eolY},
		{"a blank line", ex + 8, ey + 1},
		{"below the last line", ex + 8, ey + 20},
		{"left of the editor", ex - 1, ay},
	} {
		if pos, ok := a.hoverDwellPos(tab, tc.x, tc.y); ok {
			t.Errorf("%s (%d,%d): accepted as %v, want refused", tc.name, tc.x, tc.y, pos)
		}
	}
}

// Surfaces that mean "the user is mid-gesture" suppress the tooltip.
// Each of these is a deliberate action in flight, and a box appearing
// under the pointer during one is an interruption, not a hint.
func TestHoverDwellStandsDownForDeliberateSurfaces(t *testing.T) {
	a, fake, _ := newDwellApp(t)
	x, y := cellFor(t, a, 2, 7)

	for _, tc := range []struct {
		name string
		on   func()
		off  func()
	}{
		{"the ≡ menu", func() { a.menuOpen = true }, func() { a.menuOpen = false }},
		{"a modal", func() { a.modal = &hoverModal{lines: []string{"x"}} }, func() { a.modal = nil }},
		{"the find bar", func() { a.findOpen = true }, func() { a.findOpen = false }},
		{"the completion popup", func() { a.completion.open = true }, func() { a.completion.open = false }},
		{"the which-key band", func() { a.whichKey.open = true }, func() { a.whichKey.open = false }},
		{"a drag in progress", func() { a.dragMode = "editor" }, func() { a.dragMode = "" }},
	} {
		tc.on()
		a.hoverDwell.x, a.hoverDwell.y = x, y
		a.handleHoverDwellTick(&hoverDwellEvent{when: time.Now(), seq: a.hoverDwell.seq})
		if _, n := fake.hoverAsked(); n != 0 {
			t.Fatalf("%s: asked the server anyway", tc.name)
		}
		tc.off()
	}
}

// -----------------------------------------------------------------------------
// When an answer must not paint
// -----------------------------------------------------------------------------

// The staleness claim, and the reason the seq rides the request as well
// as the tick: the pointer moves during the round trip, and an answer
// about where it USED to be would describe the wrong symbol while
// pointing at a new one.
func TestHoverDwellMotionDropsAnAnswerInFlight(t *testing.T) {
	a, fake, path := newDwellApp(t)
	x, y := cellFor(t, a, 2, 7)
	a.notePointer(x, y, tcell.ButtonNone)
	seq := dwellFire(t, a, fake)

	// The mouse moves on before the server answers.
	nx, ny := cellFor(t, a, 2, 8)
	a.notePointer(nx, ny, tcell.ButtonNone)

	dwellAnswer(a, path, seq, x, y, "func alpha() int")
	if a.hoverDwell.open {
		t.Fatal("an answer about a cell the pointer has left must not paint")
	}
}

// A server with nothing to say produces nothing at all. menuHoverInfo
// flashes "No hover info" because a person asked; here nobody did, and a
// status line that blinked whenever the mouse came to rest would be
// noise the user cannot turn off.
func TestHoverDwellSaysNothingWhenThereIsNothing(t *testing.T) {
	a, fake, path := newDwellApp(t)
	fake.hoverRes = hoverAnswer("")
	x, y := cellFor(t, a, 2, 7)
	a.notePointer(x, y, tcell.ButtonNone)
	seq := dwellFire(t, a, fake)

	a.statusMsg = ""
	dwellAnswer(a, path, seq, x, y, "")
	if a.hoverDwell.open {
		t.Fatal("an empty answer must not open an empty box")
	}
	if a.statusMsg != "" {
		t.Fatalf("flashed %q; the dwell layer must be silent", a.statusMsg)
	}
}

// An answer for a document the user has left is dropped — the rule every
// async LSP surface here follows.
func TestHoverDwellDropsAnswerForAnotherFile(t *testing.T) {
	a, fake, _ := newDwellApp(t)
	x, y := cellFor(t, a, 2, 7)
	a.notePointer(x, y, tcell.ButtonNone)
	seq := dwellFire(t, a, fake)

	dwellAnswer(a, "/somewhere/else.go", seq, x, y, "func other()")
	if a.hoverDwell.open {
		t.Fatal("an answer about another file must not paint")
	}
}

// -----------------------------------------------------------------------------
// What makes it go away
// -----------------------------------------------------------------------------

// The lifetime rule in one test: the tooltip belongs to the cell it
// describes, so the pointer leaving that cell ends it — while resting on
// the same cell (hosts do re-send motion for it) leaves it alone.
func TestHoverDwellFollowsThePointer(t *testing.T) {
	a, fake, path := newDwellApp(t)
	x, y := cellFor(t, a, 2, 7)
	a.notePointer(x, y, tcell.ButtonNone)
	seq := dwellFire(t, a, fake)
	dwellAnswer(a, path, seq, x, y, "func alpha() int")

	if a.notePointer(x, y, tcell.ButtonNone); !a.hoverDwell.open {
		t.Fatal("motion within the same cell must not close the tooltip")
	}
	if a.notePointer(x+1, y, tcell.ButtonNone); a.hoverDwell.open {
		t.Fatal("the pointer left the cell — the tooltip must go with it")
	}
}

// Typing dismisses it and cancels anything in flight: attention has
// moved to the keyboard, and a tooltip that popped up mid-keystroke
// because the mouse happened to rest on an identifier is the behaviour
// that makes people turn hover off in other editors.
func TestHoverDwellTypingDismissesAndCancels(t *testing.T) {
	a, fake, path := newDwellApp(t)
	x, y := cellFor(t, a, 2, 7)
	a.notePointer(x, y, tcell.ButtonNone)
	seq := dwellFire(t, a, fake)
	dwellAnswer(a, path, seq, x, y, "func alpha() int")

	a.handleKey(tcell.NewEventKey(tcell.KeyRune, 'q', tcell.ModNone))
	if a.hoverDwell.open {
		t.Fatal("a keystroke must dismiss the tooltip")
	}
	// And the answer that was already on its way must not resurrect it.
	dwellAnswer(a, path, seq, x, y, "func alpha() int")
	if a.hoverDwell.open {
		t.Fatal("a keystroke must also cancel the request behind it")
	}
}

// A press inside the box is swallowed. The tooltip covers code, so a
// click on it must not move the caret into text the user cannot see —
// the contract the completion popup's box already has.
func TestHoverDwellSwallowsPressesOnItself(t *testing.T) {
	a, fake, path := newDwellApp(t)
	x, y := cellFor(t, a, 2, 7)
	a.notePointer(x, y, tcell.ButtonNone)
	seq := dwellFire(t, a, fake)
	dwellAnswer(a, path, seq, x, y, "func alpha() int")
	a.drawHoverDwell()

	b := a.hoverDwell.box
	if !a.notePointer(b.x+1, b.y+1, tcell.Button1) {
		t.Fatal("a press inside the tooltip must be consumed, not passed through")
	}
	if a.hoverDwell.open {
		t.Fatal("that press must also dismiss it")
	}

	// A press anywhere else is not this surface's business.
	a.hoverDwell.open, a.hoverDwell.lines = true, []string{"x"}
	a.hoverDwell.box = struct{ x, y, w, h int }{b.x, b.y, b.w, b.h}
	if a.notePointer(b.x-3, b.y, tcell.Button1) {
		t.Fatal("a press outside the box must fall through to normal routing")
	}
}

// A modal opening — from anywhere, including code that has never heard
// of this file — takes the tooltip with it, and the visibility check is
// belt and braces for anything that sets a.modal directly.
func TestHoverDwellYieldsToOverlays(t *testing.T) {
	a, fake, path := newDwellApp(t)
	x, y := cellFor(t, a, 2, 7)
	a.notePointer(x, y, tcell.ButtonNone)
	seq := dwellFire(t, a, fake)
	dwellAnswer(a, path, seq, x, y, "func alpha() int")

	a.modal = &hoverModal{lines: []string{"x"}} // set behind its back
	if a.hoverDwellVisible() {
		t.Fatal("a tooltip must not draw under an open modal")
	}
	a.modal = nil

	a.openModal(&hoverModal{lines: []string{"x"}}) // the funnel
	if a.hoverDwell.open {
		t.Fatal("closeAllModals must dismiss the tooltip")
	}
}

// A hidden tooltip stamps no rect. A box left over from the last draw
// would swallow presses over a region that no longer has anything in it.
func TestHoverDwellClearsItsRectWhenHidden(t *testing.T) {
	a, fake, path := newDwellApp(t)
	x, y := cellFor(t, a, 2, 7)
	a.notePointer(x, y, tcell.ButtonNone)
	seq := dwellFire(t, a, fake)
	dwellAnswer(a, path, seq, x, y, "func alpha() int")
	a.drawHoverDwell()
	if a.hoverDwell.box.w == 0 {
		t.Fatal("setup: expected a stamped rect")
	}

	a.closeHoverDwell()
	a.drawHoverDwell()
	if a.hoverDwellContains(x, y+1) || a.hoverDwell.box.w != 0 {
		t.Fatal("a hidden tooltip must leave no clickable rect behind")
	}
}
