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
// a Yes/No question and does NOT quit; No leaves the editor running, Yes
// quits.
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
	if m.hover != 0 {
		t.Error("the default button should be No, so a stray Enter is safe")
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
