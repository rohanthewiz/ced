// =============================================================================
// File: internal/app/buttontip_test.go
// Author: Rohan Allison <rohanthewiz@gmail.com>
// Created: 2026-10-17
// Copyright: 2026 Rohan Allison. All rights reserved.
// =============================================================================

package app

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gdamore/tcell/v2"
	"github.com/rohanthewiz/ced/internal/lsp"
)

// buttonTipApp is a drawn editor with one open file, so the tab strip
// and status bar have stamped their rects.
func buttonTipApp(t *testing.T) (*App, string) {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "sub", "main.go")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("package main\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	a := newTestApp(t, dir)
	a.openFile(path)
	// A flash owns the status bar's left side; clear any the open left
	// so the ambient segments (⧉ among them) are what gets drawn.
	a.statusMsg = ""
	a.draw()
	return a, path
}

// hoverButton rests the pointer on (x, y) through the real mouse router
// and fires the dwell tick the arm scheduled, then draws.
func hoverButton(t *testing.T, a *App, x, y int) {
	t.Helper()
	a.handleMouse(tcell.NewEventMouse(x, y, tcell.ButtonNone, 0))
	a.handleButtonTipTick(&buttonTipEvent{seq: a.buttonTip.seq})
	a.draw()
	a.screen.Show() // the simulation screen's contents update on Show
}

// TestLeaderKeysFor_ResolvesMethodExpressions pins the key lookup: a verb
// bound at the top level, one bound inside a namespace, and a closure
// (never a bound verb) that must resolve to nothing.
func TestLeaderKeysFor_ResolvesMethodExpressions(t *testing.T) {
	if got := leaderKeysFor((*App).menuQuit); got != "Esc q" {
		t.Errorf("menuQuit keys = %q, want \"Esc q\"", got)
	}
	if got := leaderKeysFor((*App).leaderChat); got != "Esc a c" {
		t.Errorf("leaderChat keys = %q, want \"Esc a c\"", got)
	}
	if got := leaderKeysFor(func(*App) {}); got != "" {
		t.Errorf("closure keys = %q, want none", got)
	}
	if got := leaderKeysFor(nil); got != "" {
		t.Errorf("nil keys = %q, want none", got)
	}
}

// TestButtonTip_QuitButtonLabel is the headline case: resting on the red
// ✕ shows what it does and its key, and the label never covers the
// button it describes.
func TestButtonTip_QuitButtonLabel(t *testing.T) {
	a, _ := buttonTipApp(t)
	qx, qy, qw, _ := a.quitButtonRect()
	hoverButton(t, a, qx+qw/2, qy)
	if !a.buttonTipVisible() {
		t.Fatalf("no label after the dwell tick: %+v", a.buttonTip)
	}
	txt := screenText(a)
	if !strings.Contains(txt, "Quit ced — asks first") || !strings.Contains(txt, "Esc q") {
		t.Errorf("label missing from screen:\n%s", txt)
	}
	b := a.buttonTip.box
	if qy >= b.y && qy < b.y+b.h {
		t.Errorf("label box %+v covers the quit button row %d", b, qy)
	}
}

// TestButtonTip_QuitLabelCountsUnsavedTabs: with unsaved work the label
// says how much and that the click will ask what to do with it.
func TestButtonTip_QuitLabelCountsUnsavedTabs(t *testing.T) {
	a, _ := buttonTipApp(t)
	a.activeTabPtr().Dirty = true
	qx, qy, _, _ := a.quitButtonRect()
	tip, ok := a.buttonTipAt(qx, qy)
	if !ok {
		t.Fatal("no tip on the quit button")
	}
	if got := tip.text[0]; got != "Quit ced — 1 unsaved tab, asks what to do" {
		t.Errorf("dirty quit label = %q", got)
	}
}

// TestButtonTip_AnchoredToTheButton: motion inside a multi-cell button
// keeps the label open; leaving the button closes it.
func TestButtonTip_AnchoredToTheButton(t *testing.T) {
	a, _ := buttonTipApp(t)
	qx, qy, qw, _ := a.quitButtonRect()
	hoverButton(t, a, qx, qy)
	if !a.buttonTip.open {
		t.Fatal("label did not open")
	}
	a.handleMouse(tcell.NewEventMouse(qx+qw-1, qy, tcell.ButtonNone, 0))
	if !a.buttonTip.open {
		t.Error("moving within the same button closed the label")
	}
	ex, ey, _, _ := a.editorRect()
	a.handleMouse(tcell.NewEventMouse(ex+3, ey+3, tcell.ButtonNone, 0))
	if a.buttonTip.open {
		t.Error("leaving the button left the label open")
	}
}

// TestButtonTip_DismissedByPressKeyAndStaleTick: a press and a key close
// the label without being consumed, and a tick armed for a pointer that
// has since moved opens nothing.
func TestButtonTip_DismissedByPressKeyAndStaleTick(t *testing.T) {
	a, _ := buttonTipApp(t)
	mx, my, _, _ := a.menuButtonRect()
	hoverButton(t, a, mx, my)
	if !a.buttonTip.open {
		t.Fatal("label did not open on ≡")
	}
	a.noteButtonTipPointer(mx, my, tcell.Button1)
	if a.buttonTip.open {
		t.Error("a press left the label open")
	}

	hoverButton(t, a, mx+1, my)
	a.handleEvent(tcell.NewEventKey(tcell.KeyRight, 0, 0))
	if a.buttonTip.open {
		t.Error("a key left the label open")
	}

	qx, qy, _, _ := a.quitButtonRect()
	a.handleMouse(tcell.NewEventMouse(qx, qy, tcell.ButtonNone, 0))
	stale := a.buttonTip.seq
	ex, ey, _, _ := a.editorRect()
	a.handleMouse(tcell.NewEventMouse(ex+3, ey+3, tcell.ButtonNone, 0))
	a.handleButtonTipTick(&buttonTipEvent{seq: stale})
	if a.buttonTip.open {
		t.Error("a stale tick opened a label nobody is pointing at")
	}
}

// TestButtonTip_PlainCellArmsNothing: crossing code schedules no tick.
func TestButtonTip_PlainCellArmsNothing(t *testing.T) {
	a, _ := buttonTipApp(t)
	ex, ey, _, _ := a.editorRect()
	a.noteButtonTipPointer(ex+2, ey+2, tcell.ButtonNone)
	if a.buttonTip.armed.w != 0 {
		t.Errorf("a pointer over code armed a button label: %+v", a.buttonTip.armed)
	}
}

// TestButtonTip_TabPartsBeforeTabBody: the × and the tab body are both
// labelled, and the × wins on its own cell (first match in the list).
func TestButtonTip_TabPartsBeforeTabBody(t *testing.T) {
	a, path := buttonTipApp(t)
	r := a.lastTabRects[0]
	_, ty, _, _ := a.tabBarRect()
	close, ok := a.buttonTipAt(r.CloseX, ty)
	if !ok || close.text[0] != "Close tab" {
		t.Errorf("× label = %+v, want \"Close tab\"", close)
	}
	if close.tipLinesKeys() != "Esc w" {
		t.Errorf("× keys = %q, want \"Esc w\"", close.tipLinesKeys())
	}
	body, ok := a.buttonTipAt(r.X+3, ty)
	if !ok || body.text[0] != a.relativePathFor(path) {
		t.Errorf("tab body label = %+v, want the relative path", body)
	}
}

// tipLinesKeys returns the key row a label would draw, or "".
func (t buttonTip) tipLinesKeys() string {
	lines, emph := t.tipLines()
	if len(emph) == 0 {
		return ""
	}
	return lines[len(lines)-1]
}

// TestButtonTip_StatusCopyNamesThePath: the status bar's ⧉ says what it
// copies, read from the segment rect the last draw stamped.
func TestButtonTip_StatusCopyNamesThePath(t *testing.T) {
	a, path := buttonTipApp(t)
	var found bool
	for _, seg := range a.statusSegs {
		if strings.TrimSpace(seg.text) != statusCopyGlyph {
			continue
		}
		found = true
		tip, ok := a.buttonTipAt(seg.rect.x+seg.rect.w-1, seg.rect.y)
		if !ok || !strings.Contains(tip.text[0], absolutePathFor(path)) {
			t.Errorf("⧉ label = %+v, want the absolute path", tip)
		}
	}
	if !found {
		t.Fatal("status bar drew no ⧉ segment")
	}
}

// TestButtonTip_StatusLabelOpensAboveTheBar: the bottom row has no room
// below, so the box flips above and stays on screen.
func TestButtonTip_StatusLabelOpensAboveTheBar(t *testing.T) {
	a, _ := buttonTipApp(t)
	var seg statusSegment
	for _, s := range a.statusSegs {
		if s.text == "≡ " {
			seg = s // the ≡ in the corner
		}
	}
	if seg.rect.w == 0 {
		t.Fatal("status bar drew no ≡")
	}
	hoverButton(t, a, seg.rect.x, seg.rect.y)
	b := a.buttonTip.box
	if b.w == 0 || b.y+b.h > seg.rect.y {
		t.Errorf("status label box %+v, want it wholly above row %d", b, seg.rect.y)
	}
	if !strings.Contains(screenText(a), "Action menu") {
		t.Error("≡ label not drawn")
	}
}

// TestButtonTip_SilentUnderMenuAndOtherModals: the ≡ menu and ordinary
// modals cover the chrome, so nothing is labelled under them.
func TestButtonTip_SilentUnderMenuAndOtherModals(t *testing.T) {
	a, _ := buttonTipApp(t)
	a.openMenu()
	if tips := a.buttonTips(); len(tips) != 0 {
		t.Errorf("menu open: %d tips, want none", len(tips))
	}
	a.closeMenu()
	a.openConfirm("Quit ced", "Close the editor?", (*App).menuQuit)
	if tips := a.buttonTips(); len(tips) != 0 {
		t.Errorf("confirm modal open: %d tips, want none", len(tips))
	}
}

// TestButtonTip_FindAllModalButtonsAreLabelled: the one modal the layer
// speaks over — its ⟳ ◇ ◨ buttons get labels, and the hook runs ahead of
// the modal's own mouse dispatch so resting there arms the clock.
func TestButtonTip_FindAllModalButtonsAreLabelled(t *testing.T) {
	a, _ := seedFindAllLongApp(t)
	m := openFindAllT(t, a, "count")
	a.draw()
	pin := m.pinRect(a)
	hoverButton(t, a, pin.x+1, pin.y)
	if !a.buttonTipVisible() {
		t.Fatalf("no label on the pin button: %+v", a.buttonTip)
	}
	if !strings.Contains(screenText(a), "Pin: keep the list open") {
		t.Error("pin label not drawn over the modal")
	}
	if tip, ok := a.buttonTipAt(m.rerunRect(a).x, m.rerunRect(a).y); !ok || tip.text[0] != "Run the search again" {
		t.Errorf("rerun label = %+v", tip)
	}
}

// TestButtonTip_FindBarToggleTracksState: the Aa label names the current
// state and the in-bar Alt chord.
func TestButtonTip_FindBarToggleTracksState(t *testing.T) {
	a, _ := buttonTipApp(t)
	a.openFind()
	a.draw()
	r := a.findCaseRect()
	tip, ok := a.buttonTipAt(r.x, r.y)
	if !ok || tip.text[0] != "Match case: off" || tip.keys != "Alt+c" {
		t.Errorf("Aa label = %+v", tip)
	}
	a.toggleFindCase()
	tip, _ = a.buttonTipAt(r.x, r.y)
	if tip.text[0] != "Match case: on" {
		t.Errorf("Aa label after toggle = %q", tip.text[0])
	}
}

// TestButtonTip_ExplorerHeaderClose: the tree's generic-header ✕ says it
// hides the explorer, with its leader.
func TestButtonTip_ExplorerHeaderClose(t *testing.T) {
	a, _ := buttonTipApp(t)
	r := a.toolHeaderCloseRect(toolProject)
	if r.w == 0 {
		t.Skip("explorer wears no generic header in this layout")
	}
	tip, ok := a.buttonTipAt(r.x, r.y)
	if !ok || tip.text[0] != "Hide Explorer" || tip.tipLinesKeys() != "Esc t" {
		t.Errorf("explorer ✕ label = %+v keys %q", tip, tip.tipLinesKeys())
	}
}

// TestButtonTips_AllRectsHaveText is the enumerator's smoke test: with
// several panels up, every listed button carries words and a real rect.
func TestButtonTips_AllRectsHaveText(t *testing.T) {
	a, _ := buttonTipApp(t)
	a.problems.open = true
	a.draw()
	tips := a.buttonTips()
	if len(tips) < 6 {
		t.Fatalf("only %d tips enumerated", len(tips))
	}
	for _, tip := range tips {
		if len(tip.text) == 0 || strings.TrimSpace(tip.text[0]) == "" {
			t.Errorf("tip at %+v has no text", tip.rect)
		}
	}
}

// TestProblemsChipTip names the click's effect for each chip state.
func TestProblemsChipTip(t *testing.T) {
	cases := []struct {
		c    problemsChip
		want string
	}{
		{problemsChip{kind: lsp.SeverityError, on: true}, "Hide errors"},
		{problemsChip{kind: lsp.SeverityWarning, on: false}, "Show warnings"},
		{problemsChip{kind: problemsChipScope, on: true}, "Showing this file — click for all files"},
		{problemsChip{kind: problemsChipScope, on: false}, "Showing all files — click for this file only"},
	}
	for _, c := range cases {
		if got := problemsChipTip(c.c); got != c.want {
			t.Errorf("problemsChipTip(%+v) = %q, want %q", c.c, got, c.want)
		}
	}
}

// TestGitLogChipTip covers every chip in the table, so a new search mode
// cannot ship with the generic fallback label.
func TestGitLogChipTip(t *testing.T) {
	for _, c := range gitLogModeChips {
		if got := gitLogChipTip(c.mode); got == "Search mode" {
			t.Errorf("chip %q has no label of its own", c.label)
		}
	}
}
