// =============================================================================
// File: internal/app/lspworkspacesymbols_test.go
// Author: Rohan Allison <rohanthewiz@gmail.com>
// Created: 2026-09-21
// Copyright: 2026 Rohan Allison. All rights reserved.
// =============================================================================

package app

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rohanthewiz/ced/internal/editor"
	"github.com/rohanthewiz/ced/internal/lsp"
)

// TestWorkspaceSymbols_PromptThenPicker drives the whole verb: the prompt
// is seeded with the cursor word, the query reaches the server, and the
// answer opens a picker whose rows lead with the name and end with where
// it lives.
func TestWorkspaceSymbols_PromptThenPicker(t *testing.T) {
	a, fake, goPath := newLSPTestApp(t)
	other := filepath.Join(a.rootDir, "pkg", "util.go")
	if err := os.MkdirAll(filepath.Dir(other), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(other, []byte("package pkg\n\nfunc Helper() {}\n"), 0644); err != nil {
		t.Fatal(err)
	}
	fake.wsSymbols = []lsp.WorkspaceSymbol{{
		Name: "Helper", Kind: 12, Container: "pkg", Path: other, Pos: lsp.Position{Line: 2, Character: 5},
	}}
	a.openFile(goPath)
	a.activeTabPtr().MoveCursorTo(editor.Position{Line: 2, Col: 6}, false)

	a.menuGoToWorkspaceSymbol()
	pm, ok := a.modal.(*promptModal)
	if !ok {
		t.Fatalf("modal = %T, want the query prompt", a.modal)
	}
	if got := pm.field.String(); got != "main" {
		t.Errorf("prompt seed = %q, want the cursor word", got)
	}

	a.closeAllModals()
	a.startWorkspaceSymbols("Help")
	pumpAppEvents(t, a, func() bool { return a.modal != nil })

	if fake.wsQuery != "Help" {
		t.Errorf("query on the wire = %q", fake.wsQuery)
	}
	pal, ok := a.modal.(*paletteModal)
	if !ok || len(pal.items) != 1 {
		t.Fatalf("modal = %T, want a one-row picker", a.modal)
	}
	label := pal.items[0].label
	if !strings.HasPrefix(label, "Helper") || !strings.HasSuffix(label, filepath.Join("pkg", "util.go")+":3") {
		t.Errorf("label = %q, want name first and path:line last", label)
	}

	pal.items[0].run(a)
	if tab := a.activeTabPtr(); tab.Path != other || tab.Cursor != (editor.Position{Line: 2, Col: 5}) {
		t.Errorf("landed at %s %+v", tab.Path, tab.Cursor)
	}
	if !a.hasNavBack() {
		t.Error("the jump should be retraceable")
	}
}

// TestWorkspaceSymbols_AsksEveryReadyServer pins the project-wide scope:
// two running servers are both asked and their answers merged in name
// order, since no single ranking survives a merge.
func TestWorkspaceSymbols_AsksEveryReadyServer(t *testing.T) {
	a, goFake, _ := newLSPTestApp(t)
	tsFake := &fakeLSPConn{}
	a.lspInstall("typescript-language-server", tsFake)
	goFake.wsSymbols = []lsp.WorkspaceSymbol{{Name: "Zeta", Path: "/p/z.go"}}
	tsFake.wsSymbols = []lsp.WorkspaceSymbol{{Name: "Alpha", Path: "/p/a.ts"}}

	a.startWorkspaceSymbols("a")
	pumpAppEvents(t, a, func() bool { return a.modal != nil })

	pal := a.modal.(*paletteModal)
	if len(pal.items) != 2 || !strings.HasPrefix(pal.items[0].label, "Alpha") {
		t.Errorf("rows = %d, first = %q; want both servers' hits, name-sorted", len(pal.items), pal.items[0].label)
	}
}

// TestWorkspaceSymbols_NoServer pins the refusal: with nothing running
// the row explains itself and opens no prompt.
func TestWorkspaceSymbols_NoServer(t *testing.T) {
	a, _, _ := newLSPTestApp(t)
	a.lspInstall(lspGoServerID, nil)
	if a.hasWorkspaceSymbols() {
		t.Error("predicate should be false with no server")
	}
	a.menuGoToWorkspaceSymbol()
	if a.modal != nil {
		t.Error("no prompt should open without a server")
	}
}

// TestHandleLSPWorkspaceSymbols_Guards pins the two quiet exits: a stale
// generation is dropped and an empty answer flashes instead of opening a
// blank picker.
func TestHandleLSPWorkspaceSymbols_Guards(t *testing.T) {
	a, _, _ := newLSPTestApp(t)
	a.lsp.symSeq = 3
	a.handleLSPWorkspaceSymbols(&lspWorkspaceSymbolsEvent{seq: 2,
		syms: []lsp.WorkspaceSymbol{{Name: "X", Path: "/p/x.go"}}})
	if a.modal != nil {
		t.Error("a stale answer must not open a picker")
	}
	a.handleLSPWorkspaceSymbols(&lspWorkspaceSymbolsEvent{seq: 3, query: "nope"})
	if a.modal != nil || !strings.Contains(a.statusMsg, "nope") {
		t.Errorf("empty answer: modal=%v flash=%q", a.modal, a.statusMsg)
	}
}
