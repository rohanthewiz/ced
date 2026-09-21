// =============================================================================
// File: internal/app/lspgoto_test.go
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

// newGoToTestApp opens a Go file with the cursor on `main` and seeds a
// second file for locations to point into.
func newGoToTestApp(t *testing.T) (*App, *fakeLSPConn, string, string) {
	t.Helper()
	a, fake, goPath := newLSPTestApp(t)
	other := filepath.Join(a.rootDir, "impl.go")
	if err := os.WriteFile(other, []byte("package main\n\ntype impl struct{}\n\nfunc (impl) main() {}\n"), 0644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	a.openFile(goPath)
	a.activeTabPtr().MoveCursorTo(editor.Position{Line: 2, Col: 6}, false)
	return a, fake, goPath, other
}

// TestGoToImplementation_SingleAnswerJumps pins the singular half: one
// location is a jump, asked under the implementation method, with the
// origin recorded so Go back returns.
func TestGoToImplementation_SingleAnswerJumps(t *testing.T) {
	a, fake, goPath, other := newGoToTestApp(t)
	fake.locLocs = []lsp.Location{refLocAt(other, 2, 5, 9)}

	a.menuGoToImplementation()
	pumpAppEvents(t, a, func() bool { return a.activeTabPtr().Path == other })

	if fake.locMethod != lsp.MethodImplementation {
		t.Errorf("method = %q, want %q", fake.locMethod, lsp.MethodImplementation)
	}
	if got := a.activeTabPtr().Cursor; got != (editor.Position{Line: 2, Col: 5}) {
		t.Errorf("cursor = %+v, want the location's start", got)
	}
	if !a.hasNavBack() {
		t.Error("the jump should record where it came from")
	}
	_ = goPath
}

// TestGoToImplementation_SeveralAnswersList pins the plural half: more
// than one location opens the project-mode list under its own heading
// instead of guessing which one the user meant.
func TestGoToImplementation_SeveralAnswersList(t *testing.T) {
	a, fake, goPath, other := newGoToTestApp(t)
	fake.locLocs = []lsp.Location{refLocAt(other, 2, 5, 9), refLocAt(other, 4, 12, 16)}

	a.menuGoToImplementation()
	pumpAppEvents(t, a, func() bool { return a.modal != nil })

	m, ok := a.modal.(*findAllModal)
	if !ok {
		t.Fatalf("modal = %T, want the Find-all panel", a.modal)
	}
	if !m.project || m.heading != "Implementations of" || len(m.rows) != 2 {
		t.Errorf("panel = project:%v heading:%q rows:%d", m.project, m.heading, len(m.rows))
	}
	if a.activeTabPtr().Path != goPath {
		t.Error("a list must not move the editor")
	}
}

// TestGoToTypeDefinition_UsesItsOwnMethod pins the second row's wire
// method, and that an empty answer flashes rather than doing nothing.
func TestGoToTypeDefinition_UsesItsOwnMethod(t *testing.T) {
	a, fake, _, _ := newGoToTestApp(t)

	a.menuGoToTypeDefinition()
	pumpAppEvents(t, a, func() bool { return strings.HasPrefix(a.statusMsg, "No type definition") })

	if fake.locMethod != lsp.MethodTypeDefinition {
		t.Errorf("method = %q, want %q", fake.locMethod, lsp.MethodTypeDefinition)
	}
	if a.modal != nil {
		t.Error("an empty answer must not open a panel")
	}
}

// TestGoTo_RefusesOffASymbol pins the pre-flight: a cursor on nothing
// spends no round trip.
func TestGoTo_RefusesOffASymbol(t *testing.T) {
	a, fake, _, _ := newGoToTestApp(t)
	a.activeTabPtr().MoveCursorTo(editor.Position{Line: 1, Col: 0}, false) // blank line

	a.menuGoToImplementation()

	if fake.locMethod != "" {
		t.Error("no request should be sent from a blank line")
	}
}

// TestHandleLSPLocations_DropsStaleGeneration pins the guard shared with
// references: an answer superseded by a newer question opens nothing.
func TestHandleLSPLocations_DropsStaleGeneration(t *testing.T) {
	a, _, goPath, other := newGoToTestApp(t)
	a.lsp.refSeq = 5
	a.handleLSPLocations(&lspLocationsEvent{
		seq: 4, fromPath: goPath, noun: "implementation",
		locs: []lsp.Location{refLocAt(other, 2, 5, 9)},
	})
	if a.activeTabPtr().Path != goPath {
		t.Error("a stale answer must not jump")
	}
}

// TestIncomingCalls_AlwaysLists pins the third verb's one difference: a
// single call site still opens the list. "Who calls this?" answered by a
// silent jump would hide that the answer was "only one place".
func TestIncomingCalls_AlwaysLists(t *testing.T) {
	a, fake, goPath, other := newGoToTestApp(t)
	fake.callLocs = []lsp.Location{refLocAt(other, 4, 12, 16)}

	a.menuIncomingCalls()
	pumpAppEvents(t, a, func() bool { return a.modal != nil })

	m, ok := a.modal.(*findAllModal)
	if !ok || m.heading != "Calls to" || len(m.rows) != 1 {
		t.Fatalf("modal = %T %+v, want a one-row Calls-to list", a.modal, m)
	}
	if a.activeTabPtr().Path != goPath {
		t.Error("the list must not move the editor")
	}
}

// TestIncomingCalls_NothingCallable pins the empty answer's wording.
func TestIncomingCalls_NothingCallable(t *testing.T) {
	a, _, _, _ := newGoToTestApp(t)
	a.menuIncomingCalls()
	pumpAppEvents(t, a, func() bool { return strings.HasPrefix(a.statusMsg, "No incoming call") })
}
