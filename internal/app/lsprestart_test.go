// =============================================================================
// File: internal/app/lsprestart_test.go
// Author: Rohan Allison <rohanthewiz@gmail.com>
// Created: 2026-09-21
// Copyright: 2026 Rohan Allison. All rights reserved.
// =============================================================================

package app

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rohanthewiz/ced/internal/lsp"
)

// pinLSPLookPathMissing makes every server binary "not installed" for the
// test. The restart verb ends in a real spawn when a binary resolves, so
// every test here pins this — none may start the machine's own gopls.
func pinLSPLookPathMissing(t *testing.T) {
	t.Helper()
	prev := lspLookPath
	lspLookPath = func(string) (string, error) { return "", errors.New("not found") }
	t.Cleanup(func() { lspLookPath = prev })
}

// TestMenuRestartLSP_TearsDownAndSupersedes pins the teardown half: the
// live client is closed, the server's diagnostics go with it, and the
// slot's generation moves so the old process's late exit is stale.
func TestMenuRestartLSP_TearsDownAndSupersedes(t *testing.T) {
	pinLSPLookPathMissing(t)
	a, fake, goPath := newLSPTestApp(t)
	a.openFile(goPath)
	a.lsp.diags = map[string][]lsp.Diagnostic{goPath: {{Message: "stale"}}}
	sv := a.lsp.server(lspGoServerID)
	sv.noInlay = true
	before := sv.gen

	a.menuRestartLSP()

	if !fake.closed {
		t.Error("the old client must be closed, not leaked")
	}
	if sv.client != nil {
		t.Error("the slot should be empty after the teardown")
	}
	if _, ok := a.lsp.diags[goPath]; ok {
		t.Error("the old server's diagnostics should be cleared")
	}
	if sv.gen != before+1 {
		t.Errorf("gen = %d, want %d", sv.gen, before+1)
	}
	if sv.noInlay {
		t.Error("noInlay describes the old process and should be cleared")
	}
}

// TestMenuRestartLSP_MissingBinaryNamesIt pins the refusal that makes the
// row worth having: a server that still isn't installed is marked dead
// again and the flash names every binary that would have been accepted.
func TestMenuRestartLSP_MissingBinaryNamesIt(t *testing.T) {
	pinLSPLookPathMissing(t)
	a, _, _ := newLSPTestApp(t)
	pyPath := filepath.Join(a.rootDir, "tool.py")
	if err := os.WriteFile(pyPath, []byte("x = 1\n"), 0644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	a.openFile(pyPath)

	a.menuRestartLSP()

	def := lspServerFor(pyPath)
	if sv := a.lsp.server(def.id); !sv.dead || sv.starting {
		t.Errorf("slot = dead %v starting %v, want dead and idle", sv.dead, sv.starting)
	}
	for _, argv := range def.commands {
		if !strings.Contains(a.statusMsg, argv[0]) {
			t.Errorf("flash = %q, want it to name %q", a.statusMsg, argv[0])
		}
	}
}

// TestMenuRestartLSP_StaleExitCannotKillTheSuccessor is the regression
// the generation exists for: the exit event of the process a restart
// replaced must not touch the slot its successor now owns.
func TestMenuRestartLSP_StaleExitCannotKillTheSuccessor(t *testing.T) {
	a, _, _ := newLSPTestApp(t)
	sv := a.lsp.server(lspGoServerID)
	sv.gen = 3
	next := &fakeLSPConn{}
	a.lspInstall(lspGoServerID, next)

	a.handleLSPExit(&lspExitEvent{server: lspGoServerID, gen: 2})

	if sv.dead || sv.client == nil || next.closed {
		t.Error("a superseded process's exit must leave the live server alone")
	}

	// And the mirror: a handshake from before the restart is closed,
	// never installed over the current connection.
	late := &fakeLSPConn{}
	a.handleLSPReady(&lspReadyEvent{server: lspGoServerID, client: late, gen: 2})
	if !late.closed {
		t.Error("a superseded handshake's client must be closed")
	}
	if sv.client != lspConn(next) {
		t.Error("a superseded handshake must not replace the live client")
	}
}

// TestMenuRestartLSP_Refusals pins the three states in which the row does
// nothing but say why: no handling server, a handshake in flight, and an
// integration the editor shut down (whose switch is not this row's).
func TestMenuRestartLSP_Refusals(t *testing.T) {
	pinLSPLookPathMissing(t)
	a, fake, goPath := newLSPTestApp(t)

	txt := filepath.Join(a.rootDir, "notes.txt")
	if err := os.WriteFile(txt, []byte("hi\n"), 0644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	a.openFile(txt)
	a.menuRestartLSP()
	if !strings.Contains(a.statusMsg, "No language server") {
		t.Errorf("flash = %q, want the no-server reason", a.statusMsg)
	}
	if got := a.lspRestartLabel(); got != "Restart language server" {
		t.Errorf("label = %q, want the bare form on an unhandled file", got)
	}

	a.openFile(goPath)
	if got := a.lspRestartLabel(); !strings.Contains(got, lspGoServerID) {
		t.Errorf("label = %q, want it to name the server", got)
	}
	sv := a.lsp.server(lspGoServerID)
	sv.starting = true
	a.menuRestartLSP()
	if !strings.Contains(a.statusMsg, "already starting") || sv.gen != 0 {
		t.Errorf("flash = %q gen = %d, want a refusal that changes nothing", a.statusMsg, sv.gen)
	}
	sv.starting = false

	a.lsp.dead = true
	a.menuRestartLSP()
	if a.lsp.dead != true || fake.closed {
		t.Error("the integration-wide switch is not the row's to clear")
	}
}
