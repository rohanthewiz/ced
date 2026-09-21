// =============================================================================
// File: internal/app/lspservers_test.go
// Author: Rohan Allison <rohanthewiz@gmail.com>
// Created: 2026-09-21
// Copyright: 2026 Rohan Allison. All rights reserved.
// =============================================================================

package app

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/rohanthewiz/ced/internal/lsp"
)

// TestLSPServers_ExtensionsAreDisjoint pins the registry's one structural
// promise: a path names exactly one server. Every per-document map on
// lspState is keyed by path alone on the strength of it.
func TestLSPServers_ExtensionsAreDisjoint(t *testing.T) {
	seen := map[string]string{}
	ids := map[string]bool{}
	for _, def := range lspServers {
		if def.id == "" || len(def.commands) == 0 || len(def.exts) == 0 {
			t.Errorf("incomplete definition: %+v", def)
		}
		if ids[def.id] {
			t.Errorf("duplicate server id %q", def.id)
		}
		ids[def.id] = true
		for _, ext := range def.exts {
			if prev, dup := seen[ext]; dup {
				t.Errorf("%s claimed by both %s and %s", ext, prev, def.id)
			}
			seen[ext] = def.id
		}
	}
	if lspServerByID(lspGoServerID) == nil {
		t.Errorf("no %q entry — the test harness installs its fake there", lspGoServerID)
	}
}

// TestLSPServerFor pins the lookup and the languageId that goes with it —
// "typescriptreact" vs "typescript" is what decides whether JSX parses.
func TestLSPServerFor(t *testing.T) {
	for path, want := range map[string][2]string{
		"/p/main.go":  {"gopls", "go"},
		"/p/App.TSX":  {"typescript-language-server", "typescriptreact"},
		"/p/index.js": {"typescript-language-server", "javascript"},
		"/p/lib.rs":   {"rust-analyzer", "rust"},
		"/p/tool.py":  {"python", "python"},
		"/p/x.cpp":    {"clangd", "cpp"},
	} {
		def := lspServerFor(path)
		if def == nil || def.id != want[0] {
			t.Errorf("lspServerFor(%q) = %v, want %s", path, def, want[0])
			continue
		}
		if got := languageIDFor(path); got != want[1] {
			t.Errorf("languageIDFor(%q) = %q, want %q", path, got, want[1])
		}
	}
	if def := lspServerFor("/p/notes.txt"); def != nil {
		t.Errorf("notes.txt claimed by %s", def.id)
	}
}

// TestResolveCommand_FirstInstalledWins pins the alternates rule: the
// first command whose binary is on PATH runs, with its own arguments, and
// a machine with none of them resolves to nothing.
func TestResolveCommand_FirstInstalledWins(t *testing.T) {
	prev := lspLookPath
	t.Cleanup(func() { lspLookPath = prev })

	lspLookPath = func(bin string) (string, error) {
		if bin == "pylsp" {
			return "/usr/bin/pylsp", nil
		}
		return "", errors.New("not found")
	}
	bin, args, ok := lspServerByID("python").resolveCommand()
	if !ok || bin != "pylsp" || len(args) != 0 {
		t.Errorf("resolve = %q %v %v, want pylsp with no args", bin, args, ok)
	}

	lspLookPath = func(string) (string, error) { return "", errors.New("not found") }
	if _, _, ok := lspServerByID("python").resolveCommand(); ok {
		t.Error("nothing installed must resolve to nothing")
	}
}

// TestLSPEnsureStarted_MissingBinaryKillsOnlyThatServer pins per-server
// degradation: a language whose server isn't installed goes quiet without
// touching a server that is up.
func TestLSPEnsureStarted_MissingBinaryKillsOnlyThatServer(t *testing.T) {
	a, _, goPath := newLSPTestApp(t)
	prev := lspLookPath
	lspLookPath = func(string) (string, error) { return "", errors.New("not found") }
	t.Cleanup(func() { lspLookPath = prev })

	rsPath := filepath.Join(a.rootDir, "lib.rs")
	if err := os.WriteFile(rsPath, []byte("fn main() {}\n"), 0644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	a.openFile(rsPath)

	if !a.lsp.server("rust-analyzer").dead {
		t.Error("a server with no binary should be marked dead")
	}
	if a.lspReadyFor(rsPath) {
		t.Error("a dead server must not read as ready")
	}
	if !a.lspReadyFor(goPath) {
		t.Error("gopls must be unaffected by rust-analyzer being missing")
	}
}

// TestHandleLSPExit_LeavesOtherServersAlone pins the crash cleanup's
// scope: one server's exit clears ITS diagnostics and bookkeeping, and
// another server's squiggles survive.
func TestHandleLSPExit_LeavesOtherServersAlone(t *testing.T) {
	a, _, goPath := newLSPTestApp(t)
	rs := &fakeLSPConn{}
	a.lspInstall("rust-analyzer", rs)
	rsPath := filepath.Join(a.rootDir, "lib.rs")
	a.lsp.diags = map[string][]lsp.Diagnostic{
		goPath: {{Message: "go problem"}},
		rsPath: {{Message: "rust problem"}},
	}

	a.handleLSPExit(&lspExitEvent{server: "rust-analyzer"})

	if _, ok := a.lsp.diags[rsPath]; ok {
		t.Error("the crashed server's diagnostics should be cleared")
	}
	if _, ok := a.lsp.diags[goPath]; !ok {
		t.Error("another server's diagnostics must survive")
	}
	if !rs.closed || !a.lspReadyFor(goPath) {
		t.Error("exit should close that connection and leave gopls ready")
	}
}

// TestLSPClientFor_RoutesByPath pins the routing every verb depends on:
// each file talks to its own server's connection.
func TestLSPClientFor_RoutesByPath(t *testing.T) {
	a, goFake, goPath := newLSPTestApp(t)
	ts := &fakeLSPConn{}
	a.lspInstall("typescript-language-server", ts)

	if got := a.lspClientFor(goPath); got != lspConn(goFake) {
		t.Error("a Go file should route to gopls")
	}
	if got := a.lspClientFor("/p/app.ts"); got != lspConn(ts) {
		t.Error("a TypeScript file should route to its own server")
	}
	if a.lspClientFor("/p/notes.txt") != nil {
		t.Error("an unhandled file has no client")
	}
	a.lsp.dead = true
	if a.lspClientFor(goPath) != nil {
		t.Error("the integration-wide switch outranks every server")
	}
}
