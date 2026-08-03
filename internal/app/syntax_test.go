// =============================================================================
// File: internal/app/syntax_test.go
// Author: Rohan Allison <rohanthewiz@gmail.com>
// Created: 2026-08-03
// Copyright: 2026 Rohan Allison. All rights reserved.
// =============================================================================

package app

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rohanthewiz/ced/internal/editor"
)

// syntaxTestApp opens a small Go file in a throwaway root and returns the
// app plus its active tab, with the tab's style grid already built — the
// deferral only engages once there is a grid to defer against.
func syntaxTestApp(t *testing.T) (*App, *editor.Tab) {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "main.go")
	if err := os.WriteFile(path, []byte("package main\n\nfunc main() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	a := newTestApp(t, dir)
	a.openFile(path)
	tab := a.activeTabPtr()
	if tab == nil {
		t.Fatal("expected an open tab")
	}
	a.draw() // builds the initial style grid through Tab.Render
	return a, tab
}

// TestSyntaxAfterEvent_ArmsOnlyForDeferredWork pins the timer contract:
// the settle wake-up is armed while a typing burst is waiting on it and
// at no other time. An event-driven editor that holds an idle timer wakes
// the machine for nothing — the same constraint the caret blink has.
func TestSyntaxAfterEvent_ArmsOnlyForDeferredWork(t *testing.T) {
	a, tab := syntaxTestApp(t)
	defer a.stopSyntaxSettle()

	a.syntaxAfterEvent()
	if a.syntaxTimer != nil {
		t.Fatal("a quiet editor must not arm a settle timer")
	}

	tab.InsertRune('X')
	a.syntaxAfterEvent()
	if a.syntaxTimer == nil {
		t.Fatal("an intra-line edit must arm the settle timer")
	}

	// A structural edit re-lexes on the next render instead, so there is
	// nothing left for a timer to wake up for.
	a.stopSyntaxSettle()
	tab.InsertString("\n")
	a.syntaxAfterEvent()
	if a.syntaxTimer != nil {
		t.Fatal("a structural edit re-lexes immediately and needs no timer")
	}
}

// TestSyntaxSettle_ColorsLandAfterTheBurst is the end-to-end guarantee
// the timer exists for: once the pause elapses, the next draw re-lexes
// and the staleness is gone. Without the wake-up the loop would sit on
// PollEvent showing the pre-edit colors indefinitely.
func TestSyntaxSettle_ColorsLandAfterTheBurst(t *testing.T) {
	a, tab := syntaxTestApp(t)
	defer a.stopSyntaxSettle()

	tab.InsertRune('X')
	a.draw()
	if !tab.StyleStale {
		t.Fatal("the burst should still be deferred immediately after the edit")
	}

	// Stand in for the pause elapsing. Collapsing the window beats
	// sleeping through it: the handler does no work of its own, and the
	// redraw after it is what re-lexes.
	prev := editor.SyntaxSettle
	editor.SyntaxSettle = 0
	t.Cleanup(func() { editor.SyntaxSettle = prev })

	a.handleSyntaxSettle()
	a.draw()
	if tab.StyleStale {
		t.Fatal("the settle redraw must have re-lexed the buffer")
	}
}

// TestSyntaxStatusSuffix_NamesTheSizeDecision pins that a large file says
// why it has no colors. Silence would read as a broken lexer.
func TestSyntaxStatusSuffix_NamesTheSizeDecision(t *testing.T) {
	a, tab := syntaxTestApp(t)
	if got := a.syntaxStatusSuffix(); got != "" {
		t.Fatalf("an ordinary file should add no status note, got %q", got)
	}
	tab.SyntaxOff = true
	if got := a.syntaxStatusSuffix(); !strings.Contains(got, "large file") {
		t.Fatalf("a syntax-off tab should explain itself, got %q", got)
	}
}
