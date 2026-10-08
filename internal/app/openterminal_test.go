// =============================================================================
// File: internal/app/openterminal_test.go
// Author: Rohan Allison <rohanthewiz@gmail.com>
// Created: 2026-10-08
// Copyright: 2026 Rohan Allison. All rights reserved.
// =============================================================================

package app

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/rohanthewiz/ced/internal/userconfig"
)

// termEvals reads what the fake grsh session was asked to run.
func termEvals(t *testing.T, a *App) []string {
	t.Helper()
	f, ok := a.term.sess.(*fakeTermEval)
	if !ok {
		t.Fatalf("terminal session is %T, want the fake", a.term.sess)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.evals...)
}

// waitTermIdle waits for the panel's in-flight Eval to report back, so a
// test can read evals without racing the goroutine.
func waitTermIdle(t *testing.T, a *App) {
	t.Helper()
	for a.term.running {
		ev := a.screen.PollEvent()
		if done, ok := ev.(*termDoneEvent); ok {
			a.handleTermDone(done)
		}
	}
}

// TestResolveTerminal_AutoLadder pins auto's ladder below Tier 1 (the
// test App has no cats client): tmux when $TMUX is set, else the panel.
func TestResolveTerminal_AutoLadder(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	if k, note := a.resolveTerminal(); k != termKindPanel || note != "" {
		t.Fatalf("no tmux: kind=%v note=%q, want the panel", k, note)
	}
	withHost(t, hostGOOS, map[string]string{"TMUX": "/tmp/tmux-1/default,1,0"})
	if k, _ := a.resolveTerminal(); k != termKindTmux {
		t.Fatalf("inside tmux: kind=%v, want tmux", k)
	}
	a.terminalPref = userconfig.TerminalAuto
	if k, _ := a.resolveTerminal(); k != termKindTmux {
		t.Fatalf("explicit auto: kind=%v, want tmux", k)
	}
}

// TestResolveTerminal_NamedChoiceFallsBackWithAReason: "tmux" outside
// tmux and "cats" outside cats still get a terminal, and say why it is
// not the configured one.
func TestResolveTerminal_NamedChoiceFallsBackWithAReason(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	for _, pref := range []string{userconfig.TerminalTmux, userconfig.TerminalCats} {
		a.terminalPref = pref
		k, note := a.resolveTerminal()
		if k != termKindPanel || note == "" {
			t.Fatalf("%s: kind=%v note=%q, want the panel with a reason", pref, k, note)
		}
	}
	// "ced" is the panel even inside tmux.
	withHost(t, hostGOOS, map[string]string{"TMUX": "x"})
	a.terminalPref = userconfig.TerminalCed
	if k, _ := a.resolveTerminal(); k != termKindPanel {
		t.Fatalf("ced inside tmux: kind=%v", k)
	}
	// Anything else is a command line.
	a.terminalPref = "open -a Ghostty {{DIR}}"
	if k, _ := a.resolveTerminal(); k != termKindCommand {
		t.Fatalf("command: kind=%v", k)
	}
}

// TestTerminalDirFor_FileMeansItsFolder pins the target rule.
func TestTerminalDirFor_FileMeansItsFolder(t *testing.T) {
	root := t.TempDir()
	file := filepath.Join(root, "a.go")
	if err := os.WriteFile(file, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if d, ok := terminalDirFor(file); !ok || d != root {
		t.Fatalf("file: dir=%q ok=%v, want %q", d, ok, root)
	}
	if d, ok := terminalDirFor(root); !ok || d != root {
		t.Fatalf("folder: dir=%q ok=%v", d, ok)
	}
	if _, ok := terminalDirFor(filepath.Join(root, "gone")); ok {
		t.Fatal("missing path should not resolve")
	}
}

// TestOpenTerminalAt_PanelSubmitsTheCd is the Tier-0 default end to end:
// the panel opens, focuses, and RUNS `cd <folder>` — the cd is the whole
// request, so it is not left staged.
func TestOpenTerminalAt_PanelSubmitsTheCd(t *testing.T) {
	root := t.TempDir()
	sub := filepath.Join(root, "pkg")
	if err := os.Mkdir(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(sub, "a.go")
	if err := os.WriteFile(file, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	a := newTestApp(t, root)

	a.openTerminalAt(file)
	waitTermIdle(t, a)

	if !a.term.open || !a.term.focused {
		t.Fatal("the panel should be open and focused")
	}
	want := "cd " + shellArg(sub)
	if got := termEvals(t, a); len(got) != 1 || got[0] != want {
		t.Fatalf("evals = %q, want [%q]", got, want)
	}
	if a.term.input.String() != "" {
		t.Fatalf("input = %q, want it submitted", a.term.input.String())
	}
}

// TestOpenTerminalAt_BusyPanelStagesTheCd: a running command would refuse
// the submit, so the cd is staged and the flash says why.
func TestOpenTerminalAt_BusyPanelStagesTheCd(t *testing.T) {
	root := t.TempDir()
	sub := filepath.Join(root, "pkg")
	if err := os.Mkdir(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	a := newTestApp(t, root)
	a.focusTermPanel()
	a.term.running = true

	a.openTerminalAt(sub)

	if got := a.term.input.String(); got != "cd "+shellArg(sub) {
		t.Fatalf("input = %q, want the cd staged", got)
	}
	if !strings.Contains(a.statusMsg, "busy") {
		t.Fatalf("statusMsg = %q", a.statusMsg)
	}
}

// TestOpenTerminalAt_NamedFallbackFlashesTheReason: "tmux" outside tmux
// opens the panel and the flash names the configured terminal's absence.
func TestOpenTerminalAt_NamedFallbackFlashesTheReason(t *testing.T) {
	root := t.TempDir()
	a := newTestApp(t, root)
	a.terminalPref = userconfig.TerminalTmux

	a.openTerminalAt(root)
	waitTermIdle(t, a)

	if !a.term.open {
		t.Fatal("the panel should open as the fallback")
	}
	if !strings.Contains(a.statusMsg, "Not inside tmux") {
		t.Fatalf("statusMsg = %q, want the fallback reason", a.statusMsg)
	}
}

// TestOpenTerminalAt_TmuxSplitsWithTheFolderAsCwd pins the tmux argv.
func TestOpenTerminalAt_TmuxSplitsWithTheFolderAsCwd(t *testing.T) {
	root := t.TempDir()
	a := newTestApp(t, root)
	withHost(t, hostGOOS, map[string]string{"TMUX": "x"})
	calls := recordHostRun(t, "", nil)

	a.openTerminalAt(root)

	c := nextHostCall(t, calls)
	want := []string{"tmux", "split-window", "-v", "-c", a.rootDir}
	if !reflect.DeepEqual(c.argv, want) {
		t.Fatalf("argv = %v, want %v", c.argv, want)
	}
	if a.term.open {
		t.Fatal("ced's panel should stay closed when tmux takes the request")
	}
}

// TestOpenTerminalAt_CommandRunsUnderShWithDirExpanded pins the custom
// command: sh -c, {{DIR}} replaced by the quoted folder, run from it.
func TestOpenTerminalAt_CommandRunsUnderShWithDirExpanded(t *testing.T) {
	root := t.TempDir()
	sub := filepath.Join(root, "my dir")
	if err := os.Mkdir(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	a := newTestApp(t, root)
	a.terminalPref = "open -a Ghostty {{DIR}}"
	calls := recordHostRun(t, "", nil)

	a.openTerminalAt(sub)

	c := nextHostCall(t, calls)
	want := []string{"sh", "-c", "open -a Ghostty " + catsShellQuote(sub)}
	if !reflect.DeepEqual(c.argv, want) {
		t.Fatalf("argv = %q, want %q", c.argv, want)
	}
	if c.dir != sub {
		t.Fatalf("cwd = %q, want %q", c.dir, sub)
	}
}

// TestOpenTerminalAt_MissingPathRefuses: nothing opens for a vanished
// node.
func TestOpenTerminalAt_MissingPathRefuses(t *testing.T) {
	root := t.TempDir()
	a := newTestApp(t, root)

	a.openTerminalAt(filepath.Join(root, "gone"))

	if a.term.open || !strings.Contains(a.statusMsg, "no longer exists") {
		t.Fatalf("open=%v statusMsg=%q", a.term.open, a.statusMsg)
	}
}
