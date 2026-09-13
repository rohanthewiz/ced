// =============================================================================
// File: internal/app/softwrap_test.go
// Author: Rohan Allison <rohanthewiz@gmail.com>
// Created: 2026-09-13
// Copyright: 2026 Rohan Allison. All rights reserved.
// =============================================================================

// Tests for soft wrap's UI half: the tree and editor right-click rows, the
// ≡ row's label and gate, the status segment, and the session round trip.

package app

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/rohanthewiz/ced/internal/filetree"
	"github.com/rohanthewiz/ced/internal/session"
)

// treeContextRow returns the index of the row labelled exactly label in
// the open tree context menu, or -1.
func treeContextRow(a *App, label string) int {
	for i, it := range contextOf(a).items {
		if it.label == label {
			return i
		}
	}
	return -1
}

// TestTreeContext_SoftWrapRowOpensWrappedAndBack pins the primary door:
// the row on an unopened file opens it already wrapped, and the same
// file's menu then offers the way back out, which unwraps it.
func TestTreeContext_SoftWrapRowOpensWrappedAndBack(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "long.txt"), []byte(strings.Repeat("word ", 40)), 0o644); err != nil {
		t.Fatal(err)
	}
	a := newTestApp(t, root)
	node := findTreeChild(t, a, "long.txt")

	a.openTreeContext(node, 5, 5)
	i := treeContextRow(a, "Soft Wrap")
	if i < 0 {
		t.Fatalf("no Soft Wrap row on a text file")
	}
	m := contextOf(a)
	m.hover = i
	m.activate(a)

	tab := a.activeTabPtr()
	if tab == nil || tab.Path != absolutePathFor(node.Path) {
		t.Fatal("the row should open the clicked file")
	}
	if !tab.IsSoftWrap() {
		t.Fatal("the row should open the file wrapped")
	}

	a.openTreeContext(node, 5, 5)
	if treeContextRow(a, "Soft Wrap") >= 0 {
		t.Error("a wrapped file must not offer Soft Wrap again")
	}
	i = treeContextRow(a, "Stop Soft Wrap")
	if i < 0 {
		t.Fatal("a wrapped file should offer Stop Soft Wrap")
	}
	m = contextOf(a)
	m.hover = i
	m.activate(a)
	if tab.IsSoftWrap() {
		t.Error("Stop Soft Wrap left the tab wrapped")
	}
}

// TestTreeContext_SoftWrapRowOnlyOnTextFiles pins where the row is NOT
// offered: a folder has no lines, and an image opens as a picture.
func TestTreeContext_SoftWrapRowOnlyOnTextFiles(t *testing.T) {
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, "sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "pic.png"), []byte("not really"), 0o644); err != nil {
		t.Fatal(err)
	}
	a := newTestApp(t, root)

	for _, n := range []*filetree.Node{findTreeChild(t, a, "sub"), findTreeChild(t, a, "pic.png"), a.tree.Root} {
		a.openTreeContext(n, 5, 5)
		if treeContextRow(a, "Soft Wrap") >= 0 {
			t.Errorf("%s offers a Soft Wrap row", n.Name)
		}
		a.closeModal()
	}
}

// TestEditorContext_SoftWrapRowToggles pins the editor body's door: the
// row wraps the file in front of you, and the reopened popup names the
// way back.
func TestEditorContext_SoftWrapRowToggles(t *testing.T) {
	root := t.TempDir()
	p := writeStatusTestFile(t, root, "code.go", "package main\n")
	a := newTestApp(t, root)
	a.openFile(p)

	m := openEditorContextAt(t, a, 1, 0)
	i := contextRowIndex(m, "Soft Wrap")
	if i < 0 {
		t.Fatalf("no Soft Wrap row: %v", labelsOf(m))
	}
	m.hover = i
	m.activate(a)
	if !a.activeTabPtr().IsSoftWrap() {
		t.Fatal("the row did not wrap the file")
	}

	m = openEditorContextAt(t, a, 1, 0)
	if contextRowIndex(m, "Stop Soft Wrap") < 0 {
		t.Fatalf("a wrapped file should offer Stop Soft Wrap: %v", labelsOf(m))
	}
}

// TestSoftWrapMenuRowAndStatusSegment pins the ≡ twin and the disclosure:
// the row dims with no text tab, its label names the state it switches
// to, and a wrapped tab says so in the status bar.
func TestSoftWrapMenuRowAndStatusSegment(t *testing.T) {
	root := t.TempDir()
	p := writeStatusTestFile(t, root, "notes.txt", "hello\n")
	a := newTestApp(t, root)

	if a.hasSoftWrapTarget() {
		t.Fatal("the ≡ row should dim with no tab open")
	}
	a.toggleSoftWrap() // refuses with a flash, must not panic

	a.openFile(p)
	if !a.hasSoftWrapTarget() || a.softWrapToggleLabel() != "Soft wrap" {
		t.Fatalf("unwrapped label = %q", a.softWrapToggleLabel())
	}
	hasWrapSeg := func() bool {
		for _, s := range a.statusLeftSegments() {
			if s.text == " · wrap" {
				return true
			}
		}
		return false
	}
	if hasWrapSeg() {
		t.Fatal("an unwrapped tab shows the wrap segment")
	}

	a.menuToggleSoftWrap()
	if a.softWrapToggleLabel() != "Stop soft wrap" {
		t.Fatalf("wrapped label = %q", a.softWrapToggleLabel())
	}
	// The toggle's flash owns the whole left side while it lasts; expire
	// it so the ambient segments are what gets read.
	a.statusUntil = time.Time{}
	if !hasWrapSeg() {
		t.Fatal("a wrapped tab must say so in the status bar")
	}
}

// TestSession_SoftWrapRoundTrips pins that wrap is remembered with the
// tab: recorded on the way out, and put back on restore.
func TestSession_SoftWrapRoundTrips(t *testing.T) {
	root := t.TempDir()
	p := filepath.Join(root, "doc.txt")
	if err := os.WriteFile(p, []byte("a\nb\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	a := newTestApp(t, root)
	seedSessionStore(a)
	a.openFile(p)
	a.activeTabPtr().SetSoftWrap(true)
	a.recordSession()

	e, ok := a.sessionStore.Find(root)
	if !ok || len(e.Tabs) != 1 || !e.Tabs[0].Wrap {
		t.Fatalf("wrap not recorded: %+v", e.Tabs)
	}

	store := &session.Store{}
	store.Record(session.Entry{Root: root, Tabs: []session.TabState{{Path: p, Wrap: true}}})
	b := newTestApp(t, root)
	b.sessionStore = store
	b.restoreSession()
	if len(b.tabs) != 1 || !b.tabs[0].IsSoftWrap() {
		t.Fatal("a restored tab lost its soft wrap")
	}
}
