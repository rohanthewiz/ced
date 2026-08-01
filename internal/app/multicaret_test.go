// =============================================================================
// File: internal/app/multicaret_test.go
// Author: Rohan Allison <rohanthewiz@gmail.com>
// Created: 2026-07-31
// Copyright: 2026 Rohan Allison. All rights reserved.
// =============================================================================

package app

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gdamore/tcell/v2"

	"github.com/rohanthewiz/ced/internal/editor"
)

// caretApp returns an app with one open tab holding the given text and
// the cursor at the origin — the fixture for the UI-level gestures.
func caretApp(t *testing.T, text string) (*App, *editor.Tab) {
	t.Helper()
	root := t.TempDir()
	path := filepath.Join(root, "main.go")
	if err := os.WriteFile(path, []byte(text), 0644); err != nil {
		t.Fatalf("seed file: %v", err)
	}
	a := newTestApp(t, root)
	a.openFile(path)
	tab := a.activeTabPtr()
	if tab == nil {
		t.Fatal("fixture should have opened a tab")
	}
	return a, tab
}

// TestMenuAddCaretBelow_AddsAndReports drives the ≡ row end to end: the
// caret lands, the menu closes, and the flash names the new total (the
// confirmation that matters most on the second caret).
func TestMenuAddCaretBelow_AddsAndReports(t *testing.T) {
	a, tab := caretApp(t, "one\ntwo\nthree")
	a.menuOpen = true

	a.menuAddCaretBelow()

	if a.menuOpen {
		t.Error("an action row should close the menu")
	}
	if got := tab.CaretCount(); got != 2 {
		t.Fatalf("caret count = %d, want 2", got)
	}
	if !strings.Contains(a.statusMsg, "2 carets") {
		t.Errorf("flash = %q, want it to name 2 carets", a.statusMsg)
	}
}

// TestMenuAddCaretBelow_FlashesAtBufferEdge pins the refusal path: a key
// press that can't do anything has to say so, not fail silently.
func TestMenuAddCaretBelow_FlashesAtBufferEdge(t *testing.T) {
	a, tab := caretApp(t, "only line")

	a.menuAddCaretBelow()

	if tab.HasCarets() {
		t.Error("no line below means no caret")
	}
	if !strings.Contains(a.statusMsg, "No line") {
		t.Errorf("flash = %q, want a refusal message", a.statusMsg)
	}
}

// TestMenuSelectAllOccurrences_ReportsCount covers the bulk row and its
// singular/plural wording.
func TestMenuSelectAllOccurrences_ReportsCount(t *testing.T) {
	a, tab := caretApp(t, "x := 1\ny := x\nz := x")
	tab.MoveCursorTo(editor.Position{Line: 0, Col: 0}, false)

	a.menuSelectAllOccurrences()

	if got := tab.CaretCount(); got != 3 {
		t.Fatalf("caret count = %d, want 3", got)
	}
	if !strings.Contains(a.statusMsg, "3 occurrences") {
		t.Errorf("flash = %q, want it to name 3 occurrences", a.statusMsg)
	}
}

// TestMenuSelectAllOccurrences_FlashesWithNoWord pins the empty case —
// the row stays enabled (see hasMultiCaretTarget), so the action itself
// owes the user an explanation.
func TestMenuSelectAllOccurrences_FlashesWithNoWord(t *testing.T) {
	a, tab := caretApp(t, "   ")
	tab.MoveCursorTo(editor.Position{Line: 0, Col: 1}, false)

	a.menuSelectAllOccurrences()

	if tab.HasCarets() {
		t.Error("nothing under the cursor should place no carets")
	}
	if !strings.Contains(a.statusMsg, "Nothing under the cursor") {
		t.Errorf("flash = %q, want the no-match message", a.statusMsg)
	}
}

// TestEscClearsCarets pins the escape hatch: Esc drops the caret column
// as a side effect WITHOUT consuming the keystroke, so the double-Esc
// menu and the leader table still work from multi-caret mode.
func TestEscClearsCarets(t *testing.T) {
	a, tab := caretApp(t, "a\nb\nc")
	tab.AddCaretLine(1)
	if !tab.HasCarets() {
		t.Fatal("fixture should have an extra caret")
	}

	a.handleKey(tcell.NewEventKey(tcell.KeyEsc, 0, tcell.ModNone))

	if tab.HasCarets() {
		t.Error("Esc should drop the extra carets")
	}
	if a.lastEscape.IsZero() {
		t.Error("Esc must still arm the leader window — the clear is a side effect")
	}
}

// TestEditorAltPress_TogglesCaret pins the mouse gesture at the app
// level, including that it never starts a drag (which would wipe the
// caret set on the next mouse wiggle).
func TestEditorAltPress_TogglesCaret(t *testing.T) {
	a, tab := caretApp(t, "alpha\nbravo\ncharlie")
	ex, ey, _, _ := a.editorRect()
	x := ex + 7 // one cell into the code column
	y := ey + 2

	ev := tcell.NewEventMouse(x, y, tcell.Button1, tcell.ModAlt)
	a.handleMouse(ev)

	if got := tab.CaretCount(); got != 2 {
		t.Fatalf("caret count after alt+click = %d, want 2", got)
	}
	if a.dragMode != "" {
		t.Errorf("alt+click started drag mode %q, want none", a.dragMode)
	}

	a.handleMouse(tcell.NewEventMouse(x, y, tcell.Button1, tcell.ModAlt))
	if tab.HasCarets() {
		t.Error("a second alt+click on the same spot should remove the caret")
	}
}

// TestPlainClickDropsCarets is the other half of the mouse contract: an
// unmodified click means "put the caret here", singular.
func TestPlainClickDropsCarets(t *testing.T) {
	a, tab := caretApp(t, "alpha\nbravo\ncharlie")
	tab.AddCaretLine(1)
	ex, ey, _, _ := a.editorRect()

	a.handleMouse(tcell.NewEventMouse(ex+8, ey+1, tcell.Button1, tcell.ModNone))

	if tab.HasCarets() {
		t.Error("a plain click must collapse back to one caret")
	}
}

// TestCaretStatusSuffix pins the status readout: silent in the ordinary
// single-caret case, and naming the count when the mode is live (the
// only cue once the extra carets scroll out of view).
func TestCaretStatusSuffix(t *testing.T) {
	a, tab := caretApp(t, "a\nb")
	if got := a.caretStatusSuffix(); got != "" {
		t.Errorf("single-caret suffix = %q, want empty", got)
	}
	tab.AddCaretLine(1)
	if got := a.caretStatusSuffix(); !strings.Contains(got, "2 carets") {
		t.Errorf("multi-caret suffix = %q, want it to name 2 carets", got)
	}
}

// TestTypingFansOutThroughHandleKey closes the loop from keystroke to
// buffer — the path a user actually takes, not just the editor API.
func TestTypingFansOutThroughHandleKey(t *testing.T) {
	a, tab := caretApp(t, "one\ntwo\nthree")
	tab.MoveCursorTo(editor.Position{Line: 0, Col: 0}, false)
	tab.AddCaretLine(1)
	tab.AddCaretLine(1)

	a.handleKey(tcell.NewEventKey(tcell.KeyRune, '/', tcell.ModNone))
	a.handleKey(tcell.NewEventKey(tcell.KeyRune, '/', tcell.ModNone))

	if got := tab.Buffer.String(); got != "//one\n//two\n//three" {
		t.Fatalf("buffer after typing at three carets:\n%q", got)
	}
}

// TestMenuClearCarets pins the keyboard-independent way back — the ≡ row
// that exists because macOS Terminal can swallow keys and because a user
// who got here by accident may already be in the menu looking for it.
func TestMenuClearCarets(t *testing.T) {
	a, tab := caretApp(t, "a\nb\nc")
	tab.AddCaretLine(1)
	a.menuOpen = true

	a.menuClearCarets()

	if tab.HasCarets() {
		t.Error("the Clear row should drop every extra caret")
	}
	if a.menuOpen {
		t.Error("an action row should close the menu")
	}
}

// TestHasCarets_GatesTheClearRow confirms the enable predicate tracks
// the live caret set, so the row dims when there's nothing to clear.
func TestHasCarets_GatesTheClearRow(t *testing.T) {
	a, tab := caretApp(t, "a\nb")
	if a.hasCarets() {
		t.Error("a fresh tab has no extra carets")
	}
	tab.AddCaretLine(1)
	if !a.hasCarets() {
		t.Error("predicate should see the added caret")
	}
}

// TestCaretBlink_ArmsOnlyWhileCaretsExist pins the lifecycle rule that
// keeps an idle single-caret editor from waking on a timer: the ticker
// is armed by the dispatch tail when carets appear and disarmed the
// moment they're gone.
func TestCaretBlink_ArmsOnlyWhileCaretsExist(t *testing.T) {
	a, tab := caretApp(t, "a\nb\nc")
	t.Cleanup(a.stopCaretBlink)

	a.caretBlinkAfterEvent()
	if a.caretBlinkStop != nil {
		t.Fatal("no carets — the blink must not be running")
	}

	tab.AddCaretLine(1)
	a.caretBlinkAfterEvent()
	if a.caretBlinkStop == nil {
		t.Fatal("carets exist — the blink should be armed")
	}

	tab.ClearCarets()
	a.caretBlinkAfterEvent()
	if a.caretBlinkStop != nil {
		t.Fatal("carets gone — the blink should be disarmed")
	}
}

// TestCaretBlink_TogglesPhaseOntoTabs covers the tick itself: the phase
// flips and reaches the Tab flag paintCarets reads.
func TestCaretBlink_TogglesPhaseOntoTabs(t *testing.T) {
	a, tab := caretApp(t, "a\nb\nc")
	tab.AddCaretLine(1)

	a.handleCaretBlink()
	if !a.caretBlinkOff || !tab.CaretsHidden {
		t.Fatal("first tick should enter the off phase and stamp the tab")
	}
	a.handleCaretBlink()
	if a.caretBlinkOff || tab.CaretsHidden {
		t.Fatal("second tick should return to the on phase")
	}
}

// TestCaretBlink_StopLeavesCaretsVisible pins the detail that would
// otherwise strand a caret invisible: disarming mid-blink has to restore
// the on phase, since nothing else is going to redraw it.
func TestCaretBlink_StopLeavesCaretsVisible(t *testing.T) {
	a, tab := caretApp(t, "a\nb\nc")
	tab.AddCaretLine(1)
	a.startCaretBlink()
	a.handleCaretBlink() // now in the off phase
	if !tab.CaretsHidden {
		t.Fatal("fixture should be mid-blink")
	}

	a.stopCaretBlink()

	if tab.CaretsHidden {
		t.Error("stopping the blink must leave the carets visible")
	}
	if a.caretBlinkStop != nil {
		t.Error("stop should clear the channel so a later start re-arms")
	}
}

// TestCaretBlink_HiddenPhaseDrawsNoCaret closes the loop at the render
// layer — the off phase shows the plain text underneath, which is what
// makes it read as a blink rather than a flicker of some other color.
func TestCaretBlink_HiddenPhaseDrawsNoCaret(t *testing.T) {
	a, tab := caretApp(t, "abc\ndef")
	tab.MoveCursorTo(editor.Position{Line: 0, Col: 0}, false)
	tab.AddCaretAt(editor.Position{Line: 1, Col: 1})
	th := a.theme

	scr := a.screen.(tcell.SimulationScreen)
	// The caret sits on row 1, column 1 of the code — one cell past the
	// gutter (6) and its mark column (1).
	caretCell := func() tcell.Color {
		cells, w, _ := scr.GetContents()
		_, bg, _ := cells[1*w+7+1].Style.Decompose()
		return bg
	}

	tab.CaretsHidden = true
	tab.Render(scr, th, 0, 0, 40, 10)
	scr.Show()
	if caretCell() == th.Accent {
		t.Fatal("the off phase should paint no caret cell")
	}

	tab.CaretsHidden = false
	tab.Render(scr, th, 0, 0, 40, 10)
	scr.Show()
	if caretCell() != th.Accent {
		t.Fatal("the on phase should paint the caret cell")
	}
}

// TestPlural covers the wording helper both caret messages route
// through — "1 caret", never "1 carets".
func TestPlural(t *testing.T) {
	if got := plural(1, "caret", "carets"); got != "1 caret" {
		t.Errorf("plural(1) = %q", got)
	}
	if got := plural(4, "caret", "carets"); got != "4 carets" {
		t.Errorf("plural(4) = %q", got)
	}
}

// TestMultiCaretLeaderBindings pins the four leader keys against the
// menu rows they mirror — the ≡ hint column claims "esc m", "esc M",
// "esc *" and "esc &", and a rebind that updated only one side would
// make it lie. The repeat flags are part of the contract: the three
// incremental gestures chain, the bulk one doesn't (it has nothing left
// to add, and re-arming would swallow the next keystroke).
func TestMultiCaretLeaderBindings(t *testing.T) {
	want := map[rune]bool{'m': true, 'M': true, '*': true, '&': false}
	for r, repeat := range want {
		b := leaderBindingFor(r)
		if b == nil || b.action == nil {
			t.Fatalf("no leader binding for %q", r)
		}
		if b.repeat != repeat {
			t.Errorf("binding %q repeat = %v, want %v", r, b.repeat, repeat)
		}
	}
}

// TestSelectAllOccurrencesLeaderFires drives Esc-& the way a user does —
// through handleKey, not by calling the action — so the leader window
// and the menu-row wiring are both covered.
func TestSelectAllOccurrencesLeaderFires(t *testing.T) {
	a, tab := caretApp(t, "x := 1\ny := x\nz := x")
	tab.MoveCursorTo(editor.Position{Line: 0, Col: 0}, false)

	a.handleKey(tcell.NewEventKey(tcell.KeyEsc, 0, tcell.ModNone))
	a.handleKey(tcell.NewEventKey(tcell.KeyRune, '&', tcell.ModNone))

	if got := tab.CaretCount(); got != 3 {
		t.Fatalf("caret count after Esc-& = %d, want 3", got)
	}
}
