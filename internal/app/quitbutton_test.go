// =============================================================================
// File: internal/app/quitbutton_test.go
// Author: Rohan Allison <rohanthewiz@gmail.com>
// Created: 2026-10-16
// Copyright: 2026 Rohan Allison. All rights reserved.
// =============================================================================

package app

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gdamore/tcell/v2"
)

// TestQuitButtonRect_SitsLeftOfTheMenuButton pins the layout: ✕ takes
// the first cells of the tab bar, ≡ starts right after it, and the tab
// strip starts after both — with the sidebar shown and hidden.
func TestQuitButtonRect_SitsLeftOfTheMenuButton(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	for _, shown := range []bool{true, false} {
		a.sidebarShown = shown
		qx, _, qw, _ := a.quitButtonRect()
		tx, _, _, _ := a.tabBarRect()
		mx, _, mw, _ := a.menuButtonRect()
		sx, _, _ := a.tabStripRect()
		if qx != tx || qw != quitButtonWidth {
			t.Errorf("sidebar %v: quit rect x=%d w=%d, want x=%d w=%d", shown, qx, qw, tx, quitButtonWidth)
		}
		if mx != qx+qw {
			t.Errorf("sidebar %v: ≡ at %d, want right after ✕ at %d", shown, mx, qx+qw)
		}
		if sx != mx+mw {
			t.Errorf("sidebar %v: tab strip at %d, want right after ≡ at %d", shown, sx, mx+mw)
		}
	}
}

// TestDrawTabBar_PaintsQuitButton checks the paint matches the rect: the
// ✕ glyph is in the middle of the quit button and ≡ is still drawn.
func TestDrawTabBar_PaintsQuitButton(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	a.drawTabBar()
	a.screen.Show()
	qx, qy, qw, _ := a.quitButtonRect()
	if r, _, _ := a.screen.Get(qx+qw/2, qy); r != "✕" {
		t.Errorf("quit button cell = %q, want ✕", r)
	}
	mx, my, mw, _ := a.menuButtonRect()
	if r, _, _ := a.screen.Get(mx+mw/2, my); r != "≡" {
		t.Errorf("menu button cell = %q, want ≡", r)
	}
}

// TestQuitButton_CleanTabsConfirmFirst: with nothing unsaved a click asks
// a Yes/No question (Yes focused) and does NOT quit; No leaves the editor
// running, Yes quits.
func TestQuitButton_CleanTabsConfirmFirst(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	qx, _, _, _ := a.quitButtonRect()

	a.tabBarClick(qx, 0)
	m := confirmOf(a)
	if m == nil {
		t.Fatalf("modal = %T, want the quit confirmation", a.modal)
	}
	if a.quit {
		t.Fatal("the click alone must not quit")
	}
	if m.hover != 1 {
		t.Error("the default button should be Yes — nothing unsaved is at stake")
	}
	m.cancel(a)
	if a.quit || a.modal != nil {
		t.Fatalf("No should close the dialog and keep running (quit=%v, modal=%T)", a.quit, a.modal)
	}

	a.tabBarClick(qx+quitButtonWidth-1, 0) // any cell of the button
	confirmOf(a).yes(a)
	if !a.quit {
		t.Fatal("Yes should quit")
	}
}

// TestQuitButton_DirtyTabsGoStraightToSaveDiscard: with unsaved work the
// click opens the Save / Discard / Cancel modal directly — one question,
// not a Yes/No in front of it — and still does not quit on its own.
func TestQuitButton_DirtyTabsGoStraightToSaveDiscard(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "a.txt")
	if err := os.WriteFile(path, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	a := newTestApp(t, dir)
	a.openFile(path)
	a.activeTabPtr().Dirty = true

	a.requestQuitFromButton()
	m, ok := a.modal.(*dirtyModal)
	if !ok {
		t.Fatalf("modal = %T, want the unsaved-changes modal", a.modal)
	}
	if !strings.Contains(m.message, "a.txt") {
		t.Errorf("message = %q, want it to name the dirty file", m.message)
	}
	if a.quit {
		t.Fatal("the click alone must not quit")
	}
}

// TestQuitButton_EnterConfirmsQuit: the clean-tabs confirm opens on Yes,
// so click + Enter quits without reaching for the mouse again.
func TestQuitButton_EnterConfirmsQuit(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	a.requestQuitFromButton()
	m := confirmOf(a)
	if m == nil {
		t.Fatalf("modal = %T, want the quit confirmation", a.modal)
	}
	m.handleKey(a, tcell.NewEventKey(tcell.KeyEnter, 0, tcell.ModNone))
	if !a.quit {
		t.Fatal("Enter on the quit confirm should quit")
	}
}

// TestOpenConfirm_DefaultStaysNo guards the shared default: only the quit
// button flips to Yes, every other confirm (Delete, Discard…) keeps No.
func TestOpenConfirm_DefaultStaysNo(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	if m := a.openConfirm("Delete", "Delete x?", nil); m.hover != 0 {
		t.Errorf("openConfirm hover = %d, want 0 (No)", m.hover)
	}
}

// TestDrawQuitButton_RedIsDesaturated: the ✕ is drawn in a duller red
// than the theme's DiagError — same red cast, less chroma — and still
// reads against the tab bar's ground.
func TestDrawQuitButton_RedIsDesaturated(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	a.draw()
	qx, qy, qw, _ := a.quitButtonRect()
	_, _, style, _ := a.screen.GetContent(qx+qw/2, qy)
	fg, _, _ := style.Decompose()
	if fg == a.theme.DiagError {
		t.Fatal("✕ still painted in the full DiagError red")
	}
	chroma := func(c tcell.Color) int32 {
		r, g, b := c.RGB()
		return max(r, g, b) - min(r, g, b)
	}
	if chroma(fg) >= chroma(a.theme.DiagError) {
		t.Errorf("✕ chroma %d not below DiagError's %d", chroma(fg), chroma(a.theme.DiagError))
	}
	if r, g, b := fg.RGB(); r <= g || r <= b {
		t.Errorf("✕ colour %d,%d,%d lost its red cast", r, g, b)
	}
	if contrastRatio(fg, a.theme.SidebarBG) < 4.5 {
		t.Errorf("✕ contrast %.2f under 4.5:1", contrastRatio(fg, a.theme.SidebarBG))
	}
}

// TestDesaturateColor covers the helper's ends and its non-RGB pass-through.
func TestDesaturateColor(t *testing.T) {
	red := tcell.NewRGBColor(0xf7, 0x76, 0x8e)
	if got := desaturateColor(red, 0); got != red {
		t.Errorf("t=0 changed the colour: %v", got)
	}
	// ±1: blendColor's +0.5 rounds a downward step toward the source.
	if r, g, b := desaturateColor(red, 1).RGB(); max(r, g, b)-min(r, g, b) > 1 {
		t.Errorf("t=1 should be grey, got %d,%d,%d", r, g, b)
	}
	if got := desaturateColor(tcell.ColorDefault, 0.5); got != tcell.ColorDefault {
		t.Errorf("non-RGB colour should pass through, got %v", got)
	}
}
