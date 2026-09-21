// =============================================================================
// File: internal/app/lspinlay_test.go
// Author: Rohan Allison <rohanthewiz@gmail.com>
// Created: 2026-09-21
// Copyright: 2026 Rohan Allison. All rights reserved.
// =============================================================================

package app

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/rohanthewiz/ced/internal/lsp"
)

// hintAt builds one hint on line 0.
func hintAt(char int, label string, kind int) lsp.InlayHint {
	return lsp.InlayHint{Pos: lsp.Position{Line: 0, Character: char}, Label: label, Kind: kind}
}

// TestInlayNote_RestatesTheAnchor pins the one piece of real logic: a
// hint moved to the end of the line is given back the thing it was
// written against — the variable for a type, the argument for a
// parameter name — in source order.
func TestInlayNote_RestatesTheAnchor(t *testing.T) {
	line := []rune("total := compute(cfg, 3, g(a, b))")
	got := inlayNote(line, []lsp.InlayHint{
		hintAt(25, "inner:", lsp.InlayParameter), // before g(a, b)
		hintAt(5, ": int", lsp.InlayType),        // after `total`
		hintAt(22, "level:", lsp.InlayParameter), // before 3
	})
	want := "total: int · level: 3 · inner: g(a, b)"
	if got != want {
		t.Errorf("note = %q, want %q", got, want)
	}
}

// TestInlayNote_EdgeCases pins the fallbacks: an unknown kind keeps its
// label as written, a long argument is cut, and a repeated remark is
// said once.
func TestInlayNote_EdgeCases(t *testing.T) {
	if got := inlayNote([]rune("}"), []lsp.InlayHint{hintAt(1, "fn main", 0)}); got != "fn main" {
		t.Errorf("unknown kind = %q", got)
	}
	long := []rune("f(aVeryLongArgumentExpressionIndeed)")
	got := inlayNote(long, []lsp.InlayHint{hintAt(2, "x:", lsp.InlayParameter)})
	if r := []rune(got); len(r) != len("x: ")+inlayArgMaxRunes || r[len(r)-1] != '…' {
		t.Errorf("long argument = %q, want it cut at %d runes", got, inlayArgMaxRunes)
	}
	// gopls sends the type bare, rust-analyzer with its own colon; both
	// must read the same.
	if got := inlayNote([]rune("x := f()"), []lsp.InlayHint{hintAt(1, "float64", lsp.InlayType)}); got != "x: float64" {
		t.Errorf("bare type label = %q", got)
	}
	// `for _, v := range` — the discarded half says nothing worth a note.
	blank := inlayNote([]rune("for _, v := range xs {"), []lsp.InlayHint{
		hintAt(5, "int", lsp.InlayType), hintAt(8, "string", lsp.InlayType)})
	if blank != "v: string" {
		t.Errorf("blank identifier = %q, want it skipped", blank)
	}
	dup := inlayNote([]rune("f(1, 1)"), []lsp.InlayHint{
		hintAt(2, "n:", lsp.InlayParameter), hintAt(5, "n:", lsp.InlayParameter)})
	if dup != "n: 1" {
		t.Errorf("duplicate remarks = %q, want one", dup)
	}
}

// newInlayTestApp opens the Go fixture with hints enabled and the
// document in sync, which is the state the dispatch tail waits for.
func newInlayTestApp(t *testing.T) (*App, *fakeLSPConn, string) {
	t.Helper()
	a, fake, goPath := newLSPTestApp(t)
	a.inlayEnabled = true
	a.openFile(goPath)
	return a, fake, goPath
}

// TestInlay_AsksOncePerRevision pins the refresh policy: one request when
// the tab is in sync, none on the events that follow, and none at all
// while an edit is still waiting on the sync debounce.
func TestInlay_AsksOncePerRevision(t *testing.T) {
	a, fake, _ := newInlayTestApp(t)
	fake.inlay = []lsp.InlayHint{{Pos: lsp.Position{Line: 2, Character: 9}, Label: "fn main", Kind: 0}}

	a.inlayAfterEvent()
	a.inlayAfterEvent()
	tab := a.activeTabPtr()
	pumpAppEvents(t, a, func() bool { return tab.LiveLineNotes() != nil })
	if fake.inlayAsked() != 1 {
		t.Errorf("requests = %d, want 1 per revision", fake.inlayAsked())
	}
	if tab.LiveLineNotes()[2] != "fn main" {
		t.Errorf("notes = %v", tab.LiveLineNotes())
	}

	tab.InsertRune('x') // unsynced: the server has not seen this text
	a.inlayAfterEvent()
	if fake.inlayAsked() != 1 {
		t.Error("an unsynced buffer must not be asked about")
	}
	a.lspFlushChange(tab)
	a.inlayAfterEvent()
	pumpAppEvents(t, a, func() bool { return fake.inlayAsked() == 2 })
}

// TestInlay_OffMeansNoRequestsAndNoNotes pins the toggle at both ends.
func TestInlay_OffMeansNoRequestsAndNoNotes(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	a, fake, _ := newInlayTestApp(t)
	tab := a.activeTabPtr()
	tab.SetLineNotes(map[int]string{0: "x: int"})

	a.setInlayHints(false)
	if tab.LiveLineNotes() != nil {
		t.Error("turning hints off should clear them now")
	}
	a.inlayAfterEvent()
	if fake.inlayAsked() != 0 {
		t.Error("no requests while off")
	}
	if a.inlayHintsToggleLabel() != "Show inlay hints" {
		t.Errorf("label = %q", a.inlayHintsToggleLabel())
	}
}

// TestHandleLSPInlay_Guards pins the two drops: an answer for a buffer
// that moved installs nothing, and a server that answers with an error
// is not asked again.
func TestHandleLSPInlay_Guards(t *testing.T) {
	a, fake, goPath := newInlayTestApp(t)
	tab := a.activeTabPtr()

	a.handleLSPInlay(&lspInlayEvent{path: goPath, rev: tab.EditRev - 1,
		hints: []lsp.InlayHint{hintAt(0, "x", 0)}})
	if tab.LiveLineNotes() != nil {
		t.Error("a stale answer must not install")
	}

	a.handleLSPInlay(&lspInlayEvent{path: goPath, rev: tab.EditRev, err: errors.New("method not found")})
	a.inlayAfterEvent()
	if fake.inlayAsked() != 0 {
		t.Error("a server that refused the method must not be asked again")
	}
}

// TestInlay_ReasksWhenTheServerFinishesLoading pins the cold-start fix: a
// loading server's empty answer is not its real one.
func TestInlay_ReasksWhenTheServerFinishesLoading(t *testing.T) {
	a, fake, _ := newInlayTestApp(t)
	a.inlayAfterEvent()
	pumpAppEvents(t, a, func() bool { return fake.inlayAsked() == 1 })

	a.handleLSPServerNote(progressNote("1", lsp.ProgressBegin, "Loading", "", -1))
	a.handleLSPServerNote(progressNote("1", lsp.ProgressEnd, "", "", -1))
	a.inlayAfterEvent()
	pumpAppEvents(t, a, func() bool { return fake.inlayAsked() == 2 })
}

// TestInlay_EndToEndWithRealGopls is the one test here that trusts
// nothing: gopls ships with every hint OFF, so a note only appears if the
// initializationOptions in the registry name settings gopls really has.
// A typo there fails silently everywhere except in this test.
func TestInlay_EndToEndWithRealGopls(t *testing.T) {
	if _, err := exec.LookPath(lspGoServerID); err != nil {
		t.Skip("gopls not installed")
	}
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module e2e\n\ngo 1.21\n"), 0644); err != nil {
		t.Fatal(err)
	}
	src := filepath.Join(dir, "main.go")
	code := "package main\n\nfunc scale(n int, by float64) float64 { return float64(n) * by }\n\nfunc main() {\n\ttotal := scale(3, 1.5)\n\t_ = total\n}\n"
	if err := os.WriteFile(src, []byte(code), 0644); err != nil {
		t.Fatal(err)
	}

	a := newTestApp(t, dir)
	a.lsp.dead = false
	a.inlayEnabled = true
	t.Cleanup(a.lspShutdown)
	a.openFile(src)
	tab := a.activeTabPtr()

	deadline := time.Now().Add(60 * time.Second)
	for tab.LiveLineNotes()[5] == "" {
		if time.Now().After(deadline) {
			t.Fatalf("no inlay note on line 6 within 60s; notes = %v", tab.LiveLineNotes())
		}
		if !a.screen.HasPendingEvent() {
			time.Sleep(5 * time.Millisecond)
			// The dispatch tail is what asks; with no events arriving it
			// has to be run by hand, as the real loop's next event would.
			a.inlayAfterEvent()
			continue
		}
		a.handleEvent(a.screen.PollEvent())
	}
	note := tab.LiveLineNotes()[5]
	for _, want := range []string{"total: float64", "n: 3", "by: 1.5"} {
		if !strings.Contains(note, want) {
			t.Errorf("note = %q, want it to contain %q", note, want)
		}
	}
}
