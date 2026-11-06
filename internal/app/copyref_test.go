// =============================================================================
// File: internal/app/copyref_test.go
// Author: Rohan Allison <rohanthewiz@gmail.com>
// Created: 2026-11-06
// Copyright: 2026 Rohan Allison. All rights reserved.
// =============================================================================

package app

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/rohanthewiz/ced/internal/editor"
)

// assertCopiedReference checks the flash a reference copy leaves. A test
// environment without a usable /dev/tty answers "Copy failed" instead,
// which still proves the user got feedback.
func assertCopiedReference(t *testing.T, a *App, want string) {
	t.Helper()
	if strings.HasPrefix(a.statusMsg, "Copy failed") {
		return
	}
	if a.statusMsg != "Copied reference: "+want {
		t.Fatalf("flash = %q, want %q", a.statusMsg, "Copied reference: "+want)
	}
}

// TestFileReference_AbsolutePathAndOneBasedLine pins the format: the
// absolute path, a colon, the 1-based line — and a relative input path
// comes out absolute, a negative line clamps to 1.
func TestFileReference_AbsolutePathAndOneBasedLine(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "a.go")
	if got, want := fileReference(p, 41), p+":42"; got != want {
		t.Errorf("fileReference = %q, want %q", got, want)
	}
	if got := fileReference(p, -3); got != p+":1" {
		t.Errorf("negative line = %q, want %q", got, p+":1")
	}
	got := fileReference(filepath.Join("rel", "b.go"), 0)
	if !filepath.IsAbs(strings.TrimSuffix(got, ":1")) || !strings.HasSuffix(got, filepath.Join("rel", "b.go")+":1") {
		t.Errorf("relative input = %q, want an absolute path ending rel/b.go:1", got)
	}
}

// TestMenuCopyReference copies the active tab's caret line, closes the
// menu, and is a silent no-op with no tab open (keyboard activation can
// race the row's dimming, like the copy-path rows).
func TestMenuCopyReference(t *testing.T) {
	dir := t.TempDir()
	a := newTestApp(t, dir)
	a.menuOpen = true
	a.menuCopyReference()
	if a.statusMsg != "" {
		t.Fatalf("no tab: flash = %q, want none", a.statusMsg)
	}

	p := writeStatusTestFile(t, dir, "ref.go", "package x\n\nfunc f() {}\n")
	a.openFile(p)
	a.activeTabPtr().Cursor = editor.Position{Line: 2, Col: 3}
	a.menuOpen = true
	a.menuCopyReference()
	if a.menuOpen {
		t.Fatal("the row should close the menu")
	}
	assertCopiedReference(t, a, p+":3")
}

// TestCopyReferenceFor_UntitledTabIsSilent: a buffer with no path has
// nothing to name, so the verb neither copies nor flashes.
func TestCopyReferenceFor_UntitledTabIsSilent(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	a.copyReferenceFor(&editor.Tab{})
	a.copyReferenceFor(nil)
	if a.statusMsg != "" {
		t.Fatalf("flash = %q, want none", a.statusMsg)
	}
}

// TestEditorContext_CopyReferenceUsesTheClickedLine: the right-click
// moves the caret to the clicked line first, so the row copies THAT
// line's reference, not wherever the caret was parked.
func TestEditorContext_CopyReferenceUsesTheClickedLine(t *testing.T) {
	root := t.TempDir()
	p := writeStatusTestFile(t, root, "ctx.go", "package main\n\nfunc hello() {}\n")
	a := newTestApp(t, root)
	a.openFile(p)
	a.activeTabPtr().Cursor = editor.Position{Line: 0}

	m := openEditorContextAt(t, a, 8, 2) // inside "func hello"
	i := contextRowIndex(m, copyReferenceLabel)
	if i < 0 {
		t.Fatalf("no reference row: %v", labelsOf(m))
	}
	m.hover = i
	m.activate(a)
	assertCopiedReference(t, a, p+":3")
}

// TestTabContext_CopyReferenceActsInPlace: the tab row copies the CLICKED
// background tab's reference at that tab's own caret line, and leaves the
// active tab in front, like the copy-path rows beside it.
func TestTabContext_CopyReferenceActsInPlace(t *testing.T) {
	a, paths := tabMenuTestApp(t)
	a.tabByPath(paths[1]).Cursor = editor.Position{Line: 0, Col: 1}
	_, sy, _ := a.tabStripRect()
	rightClick(a, tabRectFor(t, a, paths[1]).X+2, sy)
	runTabMenuRow(t, a, copyReferenceLabel)

	if a.activeTabPtr().Path != paths[0] {
		t.Errorf("active = %s, want a.go still", a.activeTabPtr().Path)
	}
	assertCopiedReference(t, a, paths[1]+":1")
}
