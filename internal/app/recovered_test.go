// =============================================================================
// File: internal/app/recovered_test.go
// Author: Rohan Allison <rohanthewiz@gmail.com>
// Created: 2026-11-18
// Copyright: 2026 Rohan Allison. All rights reserved.
// =============================================================================

package app

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/rohanthewiz/ced/internal/crash"
)

// rescueFor writes one rescue of path (text) for a's project, as
// crashsafety.go would after a crash.
func rescueFor(t *testing.T, a *App, path, text string) {
	t.Helper()
	if _, _, err := crash.Rescue(recoverDirFn(), a.rootDir, "SIGTERM", time.Now(),
		[]crash.Buffer{{Path: path, Data: []byte(text)}}); err != nil {
		t.Fatal(err)
	}
}

// TestRecoveredEditsLabel_CountsThisProjectsRescues pins N-144's notice:
// the ≡ row carries the number of rescued files when there are any, and
// is the plain label otherwise.
func TestRecoveredEditsLabel_CountsThisProjectsRescues(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	if got := a.recoveredEditsLabel(); got != "Recovered edits…" {
		t.Errorf("none: %q", got)
	}
	rescueFor(t, a, filepath.Join(a.rootDir, "a.go"), "rescued\n")
	a.recovered = recoveredCache{} // drop the cached "none"
	if got := a.recoveredEditsLabel(); got != "Recovered edits (1)…" {
		t.Errorf("one rescue: %q", got)
	}
}

// TestMenuRecoveredEdits_ListsAndCompares: the picker lists the rescued
// file; its Compare action opens the ORIGINAL and compares it with the
// rescued copy. With no rescues the row explains itself.
func TestMenuRecoveredEdits_ListsAndCompares(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	a.menuRecoveredEdits()
	if !strings.Contains(a.statusMsg, "No recovered edits") {
		t.Errorf("none: flash = %q", a.statusMsg)
	}

	orig := filepath.Join(a.rootDir, "a.go")
	if err := os.WriteFile(orig, []byte("package a\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	rescueFor(t, a, orig, "package a\n\nfunc lost() {}\n")
	a.menuRecoveredEdits()
	m := paletteOf(a)
	if m == nil || len(m.items) != 1 || !strings.Contains(m.items[0].label, "a.go") || !strings.Contains(m.items[0].label, "SIGTERM") {
		t.Fatalf("picker = %+v", m)
	}
	m.items[0].run(a)
	actions := paletteOf(a)
	if actions == nil || len(actions.items) != 3 {
		t.Fatalf("actions = %+v, want open / compare / index", actions)
	}
	actions.items[1].run(a) // Compare with a.go
	if tab := a.activeTabPtr(); tab == nil || tab.Path != orig {
		t.Fatal("compare did not open the original")
	}
	if !a.compare.open || a.compare.identical {
		t.Errorf("compare panel: open=%v identical=%v, want the difference shown", a.compare.open, a.compare.identical)
	}
}
