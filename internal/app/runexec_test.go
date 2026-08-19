// =============================================================================
// File: internal/app/runexec_test.go
// Author: Rohan Allison <rohanthewiz@gmail.com>
// Created: 2026-08-19
// Copyright: 2026 Rohan Allison. All rights reserved.
// =============================================================================

// Tests for the "Run in terminal…" verb. The assertions are on the
// STAGED LINE — the string left on the panel's input field — because
// that, plus the directory picker in front of it, is everything this
// file owns; the submit that follows is terminal.go's contract and is
// tested there. Nothing here can execute a program: newTestApp's stubbed
// evaluator never sees an Eval, since the verb deliberately doesn't
// submit one.

package app

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rohanthewiz/ced/internal/filetree"
)

// writeExec drops a runnable file into dir and returns its path.
func writeExec(t *testing.T, dir, name string) string {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(p, []byte("#!/bin/sh\necho hi\n"), 0o755); err != nil {
		t.Fatalf("write exec: %v", err)
	}
	return p
}

// stagedLine returns what the terminal's input field is holding.
func stagedLine(a *App) string { return a.term.input.String() }

// TestStageRun_SameDirNeedsNoCd pins the quiet case: with the shell
// already in the target directory the line is just the command, spelled
// relative with the ./ prefix that keeps it from becoming a PATH lookup.
func TestStageRun_SameDirNeedsNoCd(t *testing.T) {
	root := t.TempDir()
	a := newTestApp(t, root)
	exe := writeExec(t, root, "tool.sh")
	f := openTestTerm(t, a)
	f.cwd = a.rootDir

	a.stageRun(absolutePathFor(exe), a.rootDir)

	if got := stagedLine(a); got != "./tool.sh" {
		t.Errorf("staged %q, want %q", got, "./tool.sh")
	}
	if !a.term.open || !a.term.focused {
		t.Errorf("panel should be open and focused, got open=%v focused=%v", a.term.open, a.term.focused)
	}
	if len(f.evals) != 0 {
		t.Errorf("staging must not submit; got evals %v", f.evals)
	}
	if !strings.Contains(a.statusMsg, "press Enter") {
		t.Errorf("statusMsg = %q, want the staged hint", a.statusMsg)
	}
}

// TestStageRun_OtherDirLeadsWithCd pins the working-directory half: the
// cd is the FIRST thing on the line (so typed arguments land after the
// command, where they belong) and is joined with && so a failed cd can't
// run the command somewhere unexpected.
func TestStageRun_OtherDirLeadsWithCd(t *testing.T) {
	root := t.TempDir()
	a := newTestApp(t, root)
	exe := writeExec(t, filepath.Join(root, "scripts"), "build.sh")
	f := openTestTerm(t, a)
	f.cwd = "/"

	a.stageRun(absolutePathFor(exe), a.rootDir)

	want := "cd " + shellArg(a.rootDir) + " && ./scripts/build.sh"
	if got := stagedLine(a); got != want {
		t.Errorf("staged %q, want %q", got, want)
	}
}

// TestRunCommandLine_OutsideDirGoesAbsolute pins the fallback: an
// executable that isn't under the chosen directory can't be spelled
// relative to it without ../.. noise, so it goes absolute.
func TestRunCommandLine_OutsideDirGoesAbsolute(t *testing.T) {
	root := t.TempDir()
	other := t.TempDir()
	a := newTestApp(t, root)
	exe := absolutePathFor(writeExec(t, other, "tool.sh"))

	got := a.runCommandLine(exe, a.rootDir)
	want := "cd " + shellArg(a.rootDir) + " && " + shellArg(exe)
	if got != want {
		t.Errorf("runCommandLine = %q, want %q", got, want)
	}
	if strings.Contains(got, "..") {
		t.Errorf("runCommandLine = %q, should not walk out with ..", got)
	}
}

// TestShellArg pins the quoting rule: ordinary paths stay bare (the line
// is read and edited by a person), anything with a space or a shell
// metacharacter is single-quoted.
func TestShellArg(t *testing.T) {
	cases := map[string]string{
		"./tool.sh":            "./tool.sh",
		"/usr/local/bin/x-1_2": "/usr/local/bin/x-1_2",
		"./my tool.sh":         "'./my tool.sh'",
		"./a;rm":               "'./a;rm'",
		"./it's":               `'./it'\''s'`,
		"":                     "''",
	}
	for in, want := range cases {
		if got := shellArg(in); got != want {
			t.Errorf("shellArg(%q) = %q, want %q", in, got, want)
		}
	}
}

// TestRunExecutable_OpensDirPicker pins the choice: the file's own
// directory leads, the project root follows, and picking a row stages
// against that directory.
func TestRunExecutable_OpensDirPicker(t *testing.T) {
	root := t.TempDir()
	a := newTestApp(t, root)
	exe := writeExec(t, filepath.Join(root, "scripts"), "build.sh")

	a.runExecutable(exe)

	m := paletteOf(a)
	if m == nil {
		t.Fatalf("expected a picker, got %T", a.modal)
	}
	if !strings.Contains(m.title, "build.sh") {
		t.Errorf("title = %q, want it to name the file", m.title)
	}
	if len(m.items) < 2 {
		t.Fatalf("want at least the file's dir and the root, got %d rows", len(m.items))
	}
	if !strings.Contains(m.items[0].label, "where the file lives") ||
		!strings.Contains(m.items[0].label, "scripts") {
		t.Errorf("row 0 = %q, want the file's own directory first", m.items[0].label)
	}
	if !strings.Contains(m.items[1].label, "project root") {
		t.Errorf("row 1 = %q, want the project root second", m.items[1].label)
	}

	m.runSelected(a) // row 0 — run it from where it lives
	if got, want := stagedLine(a), "cd "+shellArg(filepath.Join(a.rootDir, "scripts"))+" && ./build.sh"; got != want {
		t.Errorf("staged %q, want %q", got, want)
	}
}

// TestRunDirCandidates_DedupesAndPrunes pins the list's two hygiene
// rules: one row per directory however many sources name it, and no row
// for a directory that has gone away.
func TestRunDirCandidates_DedupesAndPrunes(t *testing.T) {
	root := t.TempDir()
	a := newTestApp(t, root)
	exe := absolutePathFor(writeExec(t, root, "tool.sh"))
	gone := filepath.Join(root, "gone")
	if err := os.MkdirAll(gone, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.RemoveAll(gone); err != nil {
		t.Fatalf("rm: %v", err)
	}

	dirs := a.runDirCandidates(exe)

	// The file sits in the root, so "where the file lives" and "project
	// root" are the same directory and must collapse to one row.
	seen := map[string]int{}
	for _, d := range dirs {
		seen[d.path]++
	}
	for p, n := range seen {
		if n > 1 {
			t.Errorf("directory %s listed %d times", p, n)
		}
	}
	for _, d := range dirs {
		if d.path == gone {
			t.Error("a deleted directory should be pruned, not offered")
		}
	}
	if len(dirs) == 0 || dirs[0].note != "where the file lives" {
		t.Fatalf("candidates = %+v, want the file's own directory first", dirs)
	}
}

// TestRunExecutable_SingleCandidateStagesDirectly pins the shortcut: a
// list of one is not a choice, so a workspace with nothing else to offer
// skips the picker entirely.
func TestRunExecutable_SingleCandidateStagesDirectly(t *testing.T) {
	root := t.TempDir()
	a := newTestApp(t, root)
	a.sessionStore = nil // no recent folders to widen the list with
	exe := writeExec(t, root, "tool.sh")
	// The shell already sits in the root, so "where the file lives", the
	// project root and the terminal's cwd are all one directory.
	f := openTestTerm(t, a)
	f.cwd = a.rootDir

	a.runExecutable(exe)

	if paletteOf(a) != nil {
		t.Fatalf("one candidate should not open a picker")
	}
	if !a.term.open {
		t.Fatal("the panel should have been staged into")
	}
	if got := stagedLine(a); !strings.HasSuffix(got, "./tool.sh") {
		t.Errorf("staged %q, want it to end in ./tool.sh", got)
	}
}

// TestRunExecutable_RefusesNonExecutable pins the live re-check: a plain
// file is refused with a message naming it, before any question about
// directories is asked. This is the guard that covers a chmod -x between
// the tree's last reload and the click.
func TestRunExecutable_RefusesNonExecutable(t *testing.T) {
	root := t.TempDir()
	a := newTestApp(t, root)
	plain := filepath.Join(root, "notes.txt")
	if err := os.WriteFile(plain, []byte("hi\n"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}

	a.runExecutable(plain)

	if a.modal != nil {
		t.Errorf("a refusal must not open a picker, got %T", a.modal)
	}
	if a.term.open {
		t.Error("a refusal must not claim the bottom strip")
	}
	if !strings.Contains(a.statusMsg, "Not executable") {
		t.Errorf("statusMsg = %q, want a not-executable refusal", a.statusMsg)
	}
}

// TestRunExecutable_MissingFileRefuses covers the other stat failure —
// the file went away — which must read as an error, not as a run.
func TestRunExecutable_MissingFileRefuses(t *testing.T) {
	root := t.TempDir()
	a := newTestApp(t, root)

	a.runExecutable(filepath.Join(root, "gone.sh"))

	if a.term.open {
		t.Error("a refusal must not open the panel")
	}
	if !strings.HasPrefix(a.statusMsg, "Run: ") {
		t.Errorf("statusMsg = %q, want a Run: error", a.statusMsg)
	}
}

// TestStageRun_OpenPanelIsNotToggledShut guards the strict-toggle trap:
// menuToggleTerminal closes an open panel, so staging a second run while
// the panel is up must leave it up.
func TestStageRun_OpenPanelIsNotToggledShut(t *testing.T) {
	root := t.TempDir()
	a := newTestApp(t, root)
	exe := absolutePathFor(writeExec(t, root, "tool.sh"))
	openTestTerm(t, a)

	a.stageRun(exe, a.rootDir)

	if !a.term.open {
		t.Fatal("an already-open panel must stay open")
	}
	if !strings.Contains(stagedLine(a), "tool.sh") {
		t.Errorf("staged %q, want the executable", stagedLine(a))
	}
}

// TestStageRun_BusyTerminalStillStages pins the deliberate non-guard: a
// running command blocks SUBMISSION (terminal.go says so, with its own
// message), and staging a line to press Enter on when it finishes is a
// reasonable thing to do meanwhile.
func TestStageRun_BusyTerminalStillStages(t *testing.T) {
	root := t.TempDir()
	a := newTestApp(t, root)
	exe := absolutePathFor(writeExec(t, root, "tool.sh"))
	openTestTerm(t, a)
	a.term.running = true

	a.stageRun(exe, a.rootDir)

	if !strings.Contains(stagedLine(a), "tool.sh") {
		t.Errorf("staged %q, want the executable", stagedLine(a))
	}
}

// TestRunExecutableMenuRow pins the ≡ row: enabled only for an open file
// that is executable right now, with a label naming it, and a refusal
// that explains itself when there is nothing to run.
func TestRunExecutableMenuRow(t *testing.T) {
	root := t.TempDir()
	a := newTestApp(t, root)

	if a.hasRunnableTab() {
		t.Error("no tab open — the row should be disabled")
	}
	if got := a.runExecutableLabel(); got != "Run file in terminal…" {
		t.Errorf("label = %q, want the generic form", got)
	}
	a.menuRunExecutable()
	if !strings.Contains(a.statusMsg, "Nothing to run") {
		t.Errorf("statusMsg = %q, want a nothing-to-run refusal", a.statusMsg)
	}

	plain := filepath.Join(root, "notes.txt")
	if err := os.WriteFile(plain, []byte("hi\n"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	a.openFile(plain)
	if a.hasRunnableTab() {
		t.Error("a plain file must not enable the row")
	}

	exe := writeExec(t, root, "tool.sh")
	a.openFile(exe)
	if !a.hasRunnableTab() {
		t.Fatal("an executable file should enable the row")
	}
	if got := a.runExecutableLabel(); got != "Run tool.sh in terminal…" {
		t.Errorf("label = %q, want the file named", got)
	}
	a.menuRunExecutable()
	if a.modal == nil && !a.term.open {
		t.Error("the ≡ row should reach the same picker-or-stage path as the tree row")
	}
}

// TestTreeContextRunRowOnlyForExecutables pins where the row appears:
// appended last (a conditional row, not part of the fixed vocabulary)
// and only for a file the tree marked executable.
func TestTreeContextRunRowOnlyForExecutables(t *testing.T) {
	root := t.TempDir()
	writeExec(t, root, "tool.sh")
	if err := os.WriteFile(filepath.Join(root, "notes.txt"), []byte("hi\n"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	a := newTestApp(t, root)
	a.refreshTreeNow()

	exeNode, plainNode := findTreeChild(t, a, "tool.sh"), findTreeChild(t, a, "notes.txt")

	a.openTreeContext(exeNode, 5, 5)
	m, ok := a.modal.(*contextModal)
	if !ok {
		t.Fatalf("expected a contextModal, got %T", a.modal)
	}
	if last := m.items[len(m.items)-1]; last.label != "Run in terminal…" {
		t.Errorf("last row = %q, want Run in terminal…", last.label)
	}
	a.closeModal()

	a.openTreeContext(plainNode, 5, 5)
	m, ok = a.modal.(*contextModal)
	if !ok {
		t.Fatalf("expected a contextModal, got %T", a.modal)
	}
	for _, it := range m.items {
		if strings.HasPrefix(it.label, "Run in terminal") {
			t.Error("a plain file must not offer the Run row")
		}
	}
}

// findTreeChild returns the root's child named name, failing the test
// when the tree hasn't got one.
func findTreeChild(t *testing.T, a *App, name string) *filetree.Node {
	t.Helper()
	for _, c := range a.tree.Root.Children {
		if c.Name == name {
			return c
		}
	}
	t.Fatalf("tree has no child %q", name)
	return nil
}
