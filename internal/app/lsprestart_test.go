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

// Every test here depends on newTestApp pinning lspLookPath at "never
// found": the restart verb ends in a real spawn when a binary resolves,
// and none of them may start the machine's own gopls.

// TestMenuRestartLSP_TearsDownAndSupersedes pins the teardown half: the
// live client is closed, the server's diagnostics go with it, and the
// slot's generation moves so the old process's late exit is stale.
func TestMenuRestartLSP_TearsDownAndSupersedes(t *testing.T) {
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

// TestMenuRestartLSP_FailedHandshakeExplainsItself pins N-112: a restart
// the user asked for flashes the server's OWN reason when its handshake
// fails, peeled of the wrapping layers. The read loop's bare exit report
// can land first, and must neither flash nor use up the flag.
func TestMenuRestartLSP_FailedHandshakeExplainsItself(t *testing.T) {
	a, _, goPath := newLSPTestApp(t)
	a.openFile(goPath)
	prev := lspLookPath
	lspLookPath = func(string) (string, error) { return "/bin/" + lspGoServerID, nil }
	t.Cleanup(func() { lspLookPath = prev })
	// lspEnsureStarted returns before spawning without a screen, so the
	// resolved binary above is never run.
	a.screen = nil

	a.menuRestartLSP()
	sv := a.lsp.server(lspGoServerID)
	if !sv.explainStart {
		t.Fatal("a restart with the binary present should arm explainStart")
	}

	a.flash("Restarting gopls…")
	a.handleLSPExit(&lspExitEvent{server: lspGoServerID, gen: sv.gen})
	if !sv.explainStart || !strings.Contains(a.statusMsg, "Restarting") {
		t.Errorf("a reasonless exit report flashed %q / cleared the flag", a.statusMsg)
	}

	a.handleLSPExit(&lspExitEvent{server: lspGoServerID, gen: sv.gen, err: errors.New(
		`lsp: initialize: Request initialize failed with message: provides no tsserver.js. Exiting. (code -32603)`)})
	if want := lspGoServerID + " failed to start: provides no tsserver.js. Exiting."; a.statusMsg != want {
		t.Errorf("flash = %q, want %q", a.statusMsg, want)
	}
	info, ok := a.modal.(*confirmModal)
	if !ok || !info.info || !strings.Contains(info.title, "failed to start") ||
		!strings.Contains(strings.Join(info.lines, " "), "provides no tsserver.js. Exiting.") {
		t.Errorf("modal = %#v, want the info modal carrying the whole reason", a.modal)
	}
	if sv.explainStart || !sv.dead {
		t.Errorf("after the explained failure: explainStart %v dead %v, want false / true", sv.explainStart, sv.dead)
	}
}

// TestLSPStartFailure_SilentWithoutTheRow is the other half: a start
// from opening a file (explainStart clear) fails without a word, the
// silent-degradation contract; and a handshake that SUCCEEDS after a
// restart clears the flag, so a later crash isn't misreported as a
// failed start.
func TestLSPStartFailure_SilentWithoutTheRow(t *testing.T) {
	a, _, _ := newLSPTestApp(t)
	sv := a.lsp.server(lspGoServerID)

	a.handleLSPExit(&lspExitEvent{server: lspGoServerID, gen: sv.gen, err: errors.New("lsp: connection closed")})
	if a.statusMsg != "" {
		t.Errorf("an unasked start's failure flashed %q", a.statusMsg)
	}

	sv.dead = false
	sv.explainStart = true
	a.handleLSPReady(&lspReadyEvent{server: lspGoServerID, client: &fakeLSPConn{}, gen: sv.gen})
	if sv.explainStart {
		t.Error("a successful handshake should clear explainStart")
	}
}

// TestLSPStartReason peels the client's and vscode-languageserver's
// wrappers, spells paths under the project root relative to it, and
// leaves anything else alone.
func TestLSPStartReason(t *testing.T) {
	root := filepath.Join(string(filepath.Separator)+"home", "me", "proj")
	for in, want := range map[string]string{
		"lsp: initialize: Request initialize failed with message: no tsserver.js (code -32603)": "no tsserver.js",
		"lsp: initialize: bad rootUri (code -32602)":                                            "bad rootUri",
		"lsp: connection closed":         "connection closed",
		"exec: \"x\": permission denied": "exec: \"x\": permission denied",
		`lsp: initialize: TypeScript at "` + filepath.Join(root, "node_modules", "typescript") + `" has none (code -1)`: `TypeScript at "` + filepath.Join("node_modules", "typescript") + `" has none`,
		`lsp: initialize: TypeScript at "` + filepath.Join(root+"2", "x") + `" (code -1)`:                               `TypeScript at "` + filepath.Join(root+"2", "x") + `"`,
	} {
		if got := lspStartReason(errors.New(in), root); got != want {
			t.Errorf("lspStartReason(%q) = %q, want %q", in, got, want)
		}
	}
}

// TestExplainLSPStartFailure_DeclinesABusyScreen: the answer arrives
// after the click, so with a modal or the ≡ menu up it must not take the
// screen; the flash is then the whole answer. On a free screen the
// reason is wrapped to the modal's width, never clipped.
func TestExplainLSPStartFailure_DeclinesABusyScreen(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	long := strings.Repeat("word ", 40) + "tsserver.js"

	a.menuOpen = true
	a.explainLSPStartFailure("tsls", long)
	if a.modal != nil || !strings.Contains(a.statusMsg, "tsls failed to start") {
		t.Errorf("open menu: modal %T flash %q, want no modal and the flash", a.modal, a.statusMsg)
	}
	a.menuOpen = false

	held := &confirmModal{title: "busy"}
	a.openModal(held)
	a.explainLSPStartFailure("tsls", long)
	if a.modal != modal(held) {
		t.Errorf("an occupied slot was replaced by %T", a.modal)
	}
	a.closeModal()

	a.explainLSPStartFailure("tsls", long)
	info, ok := a.modal.(*confirmModal)
	if !ok || len(info.lines) < 2 {
		t.Fatalf("free screen: modal %#v, want a wrapped info modal", a.modal)
	}
	for _, ln := range info.lines {
		if runeLen(ln) > lspStartReasonWidth {
			t.Errorf("line %q is %d cells, wider than %d", ln, runeLen(ln), lspStartReasonWidth)
		}
	}
	if !strings.HasSuffix(info.lines[len(info.lines)-1], "tsserver.js") {
		t.Errorf("last line %q lost the end of the reason", info.lines[len(info.lines)-1])
	}
}
