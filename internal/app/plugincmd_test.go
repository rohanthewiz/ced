// =============================================================================
// File: internal/app/plugincmd_test.go
// Author: Rohan Allison <rohanthewiz@gmail.com>
// Created: 2026-08-02
// Copyright: 2026 Rohan Allison. All rights reserved.
// =============================================================================

package app

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/rohanthewiz/ced/internal/customactions"
	"github.com/rohanthewiz/ced/internal/editor"
	"github.com/rohanthewiz/ced/internal/plugins"
)

// fakeShell records what plugin commands were asked to run and replays
// a scripted answer, so every test in this file exercises the real
// capture / apply wiring without a process ever starting.
type fakeShell struct {
	mu    sync.Mutex
	calls []fakeShellCall

	stdout string
	stderr string
	err    error
}

// fakeShellCall is one recorded invocation.
type fakeShellCall struct {
	dir     string
	command string
	env     []string
	stdin   string
}

// install swaps the fake in for the duration of the test.
func (f *fakeShell) install(t *testing.T) *fakeShell {
	t.Helper()
	prev := pluginShell
	pluginShell = func(dir, command string, env []string, stdin string) ([]byte, []byte, error) {
		f.mu.Lock()
		f.calls = append(f.calls, fakeShellCall{dir: dir, command: command, env: env, stdin: stdin})
		f.mu.Unlock()
		return []byte(f.stdout), []byte(f.stderr), f.err
	}
	t.Cleanup(func() { pluginShell = prev })
	return f
}

// last returns the most recent recorded call.
func (f *fakeShell) last(t *testing.T) fakeShellCall {
	t.Helper()
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.calls) == 0 {
		t.Fatal("no plugin command ran")
	}
	return f.calls[len(f.calls)-1]
}

// count returns how many commands have run.
func (f *fakeShell) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.calls)
}

// openScratch opens a file with the given contents in a tab.
func openScratch(t *testing.T, a *App, name, body string) *editor.Tab {
	t.Helper()
	path := filepath.Join(a.rootDir, name)
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	a.openFile(path)
	tab := a.activeTabPtr()
	if tab == nil {
		t.Fatal("no tab after open")
	}
	return tab
}

// selectRange sets the tab's selection to [start, end).
func selectRange(tab *editor.Tab, start, end editor.Position) {
	tab.MoveCursorTo(start, false)
	tab.MoveCursorTo(end, true)
}

// TestPluginCommand_ReplaceSelection is the headline behaviour: pipe the
// selection through a command and put stdout back, as ONE undo step.
func TestPluginCommand_ReplaceSelection(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	sh := (&fakeShell{stdout: "apple\nbanana\ncherry\n"}).install(t)
	tab := openScratch(t, a, "list.txt", "cherry\nbanana\napple\ntail\n")
	selectRange(tab, editor.Position{Line: 0, Col: 0}, editor.Position{Line: 3, Col: 0})

	before := tab.Buffer.String()
	cmd := plugins.Command{
		ID: "sort", Label: "Sort", Command: "sort",
		Input: plugins.InputSelection, Output: plugins.OutputReplace,
	}
	a.execPluginCommand(plugins.Plugin{Name: "p"}, cmd, nil)
	pumpAppEvents(t, a, func() bool { return tab.Buffer.String() != before })

	if got := sh.last(t).stdin; got != "cherry\nbanana\napple\n" {
		t.Errorf("stdin = %q, want the selected range", got)
	}
	if got := tab.Buffer.String(); got != "apple\nbanana\ncherry\ntail\n" {
		t.Errorf("buffer = %q", got)
	}
	// One undo step: a plugin edit is one thing the user did.
	tab.Undo()
	if got := tab.Buffer.String(); got != before {
		t.Errorf("after one undo = %q, want the original %q", got, before)
	}
}

// TestPluginCommand_StderrNeverReachesTheBuffer pins the rule that makes
// separate capture necessary. A formatter printing a deprecation notice
// to stderr must not have that notice spliced into the user's source.
func TestPluginCommand_StderrNeverReachesTheBuffer(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	(&fakeShell{stdout: "clean\n", stderr: "warning: --foo is deprecated\n"}).install(t)
	tab := openScratch(t, a, "x.txt", "dirty\n")
	selectRange(tab, editor.Position{Line: 0, Col: 0}, editor.Position{Line: 1, Col: 0})

	cmd := plugins.Command{
		ID: "f", Label: "Filter", Command: "filter",
		Input: plugins.InputSelection, Output: plugins.OutputReplace,
	}
	a.execPluginCommand(plugins.Plugin{Name: "p"}, cmd, nil)
	pumpAppEvents(t, a, func() bool { return !strings.Contains(tab.Buffer.String(), "dirty") })

	if strings.Contains(tab.Buffer.String(), "deprecated") {
		t.Errorf("stderr leaked into the buffer: %q", tab.Buffer.String())
	}
}

// TestPluginCommand_TrailingNewlineRule pins the difference between a
// newline the command added and one the user already had. Without it,
// "sort the selection" grows a blank line every time it runs.
func TestPluginCommand_TrailingNewlineRule(t *testing.T) {
	cases := []struct {
		name     string
		in, out  string
		want     string
		whyBrief string
	}{
		{"command added one", "a\nb", "b\na\n", "b\na", "selection had no trailing newline"},
		{"user already had one", "a\nb\n", "b\na\n", "b\na\n", "preserve what was there"},
		{"command added none", "a\nb", "b\na", "b\na", "nothing to strip"},
		{"only one is stripped", "a", "a\n\n", "a\n", "a deliberate blank line survives"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := matchTrailingNewline(tc.in, tc.out); got != tc.want {
				t.Errorf("got %q, want %q (%s)", got, tc.want, tc.whyBrief)
			}
		})
	}
}

// TestPluginCommand_ReplaceWholeFileKeepsTheView pins that a whole-file
// filter doesn't fling the user to the end of the buffer. SelectAll +
// InsertString parks the cursor there, so the view is captured and put
// back — through RestoreView, which deliberately does NOT re-scroll.
func TestPluginCommand_ReplaceWholeFileKeepsTheView(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	var body strings.Builder
	for i := 0; i < 200; i++ {
		body.WriteString("line\n")
	}
	(&fakeShell{stdout: body.String()}).install(t)
	tab := openScratch(t, a, "big.txt", body.String())
	tab.MoveCursorTo(editor.Position{Line: 100, Col: 2}, false)
	tab.ScrollY = 90
	wantCursor, wantScroll := tab.Cursor, tab.ScrollY

	cmd := plugins.Command{
		ID: "fmt", Label: "Format", Command: "fmt",
		Input: plugins.InputFile, Output: plugins.OutputReplace,
	}
	rev := tab.EditRev
	a.execPluginCommand(plugins.Plugin{Name: "p"}, cmd, nil)
	pumpAppEvents(t, a, func() bool { return tab.EditRev != rev })

	if tab.Cursor != wantCursor {
		t.Errorf("cursor = %+v, want it restored to %+v", tab.Cursor, wantCursor)
	}
	if tab.ScrollY != wantScroll {
		t.Errorf("scrollY = %d, want it restored to %d", tab.ScrollY, wantScroll)
	}
}

// TestPluginCommand_StaleOutputIsDiscarded pins the staleness rule. The
// output was computed from text that no longer exists; applying it would
// overwrite the user's newer edit with a stale answer.
func TestPluginCommand_StaleOutputIsDiscarded(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	tab := openScratch(t, a, "x.txt", "original\n")
	selectRange(tab, editor.Position{Line: 0, Col: 0}, editor.Position{Line: 0, Col: 8})

	ev := &pluginCmdDoneEvent{
		label: "Filter", output: plugins.OutputReplace,
		path: tab.Path, rev: tab.EditRev - 1, // as if the buffer moved on
		selStart: editor.Position{Line: 0, Col: 0},
		selEnd:   editor.Position{Line: 0, Col: 8},
		stdout:   []byte("clobbered"),
	}
	a.handlePluginCmdDone(ev)

	if strings.Contains(tab.Buffer.String(), "clobbered") {
		t.Errorf("stale output was applied: %q", tab.Buffer.String())
	}
	if !strings.Contains(a.statusMsg, "buffer changed") {
		t.Errorf("flash = %q, want it to explain the discard", a.statusMsg)
	}
}

// TestPluginCommand_FailureOpensInfoModal pins that a non-zero exit is
// loud and shows stderr — the same treatment custom actions get — and
// that no output mode runs on a command that failed.
func TestPluginCommand_FailureOpensInfoModal(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	tab := openScratch(t, a, "x.txt", "keep me\n")
	selectRange(tab, editor.Position{Line: 0, Col: 0}, editor.Position{Line: 0, Col: 7})

	a.handlePluginCmdDone(&pluginCmdDoneEvent{
		label: "Filter", output: plugins.OutputReplace,
		path: tab.Path, rev: tab.EditRev,
		selStart: editor.Position{Line: 0, Col: 0},
		selEnd:   editor.Position{Line: 0, Col: 7},
		stdout:   []byte("half an answer"),
		stderr:   []byte("filter: bad flag"),
		err:      errors.New("exit status 2"),
	})

	m, ok := a.modal.(*confirmModal)
	if !ok || !m.info {
		t.Fatalf("modal = %T, want the info modal", a.modal)
	}
	if !strings.Contains(strings.Join(m.lines, "\n"), "bad flag") {
		t.Errorf("modal should carry stderr, got %v", m.lines)
	}
	if strings.Contains(tab.Buffer.String(), "half an answer") {
		t.Errorf("a failed command's output was applied: %q", tab.Buffer.String())
	}
}

// TestPluginCommand_OutputModes walks the modes that don't replace a
// range, since each routes somewhere different.
func TestPluginCommand_OutputModes(t *testing.T) {
	t.Run("insert at cursor", func(t *testing.T) {
		a := newTestApp(t, t.TempDir())
		tab := openScratch(t, a, "x.txt", "before after\n")
		tab.MoveCursorTo(editor.Position{Line: 0, Col: 7}, false)

		a.handlePluginCmdDone(&pluginCmdDoneEvent{
			label: "Stamp", output: plugins.OutputInsert,
			path: tab.Path, rev: tab.EditRev,
			// A trailing newline the command added is stripped: nobody
			// wants `date` to also break their line.
			stdout: []byte("MIDDLE\n"),
		})
		if got := tab.Buffer.String(); got != "before MIDDLEafter\n" {
			t.Errorf("buffer = %q", got)
		}
	})

	t.Run("info modal", func(t *testing.T) {
		a := newTestApp(t, t.TempDir())
		a.handlePluginCmdDone(&pluginCmdDoneEvent{
			label: "Explain", output: plugins.OutputInfo,
			stdout: []byte("line one\nline two\n"),
		})
		m, ok := a.modal.(*confirmModal)
		if !ok || !m.info {
			t.Fatalf("modal = %T, want the info modal", a.modal)
		}
		if len(m.lines) != 2 {
			t.Errorf("lines = %v, want two (trailing newline trimmed)", m.lines)
		}
	})

	t.Run("flash takes the first line only", func(t *testing.T) {
		a := newTestApp(t, t.TempDir())
		a.handlePluginCmdDone(&pluginCmdDoneEvent{
			label: "Count", output: plugins.OutputFlash,
			stdout: []byte("42 things\nand more\n"),
		})
		if !strings.Contains(a.statusMsg, "42 things") {
			t.Errorf("flash = %q", a.statusMsg)
		}
		if strings.Contains(a.statusMsg, "and more") {
			t.Errorf("flash spilled a second line: %q", a.statusMsg)
		}
	})
}

// TestPluginCommand_ReloadRespectsDirtyBuffer pins the same call
// format-on-save makes: the user's unsaved edits outrank a plugin's
// opinion about what's on disk.
func TestPluginCommand_ReloadRespectsDirtyBuffer(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	tab := openScratch(t, a, "x.txt", "on disk\n")
	// Rewrite the file behind the editor, then dirty the buffer.
	if err := os.WriteFile(tab.Path, []byte("rewritten by plugin\n"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	tab.InsertString("edited ")

	a.reloadPluginTarget(tab.Path, "Fixer")

	if strings.Contains(tab.Buffer.String(), "rewritten by plugin") {
		t.Errorf("dirty buffer was trampled: %q", tab.Buffer.String())
	}
	if !strings.Contains(a.statusMsg, "kept your edits") {
		t.Errorf("flash = %q, want it to say the edits were kept", a.statusMsg)
	}

	// Clean buffer: the reload lands.
	tab.Dirty = false
	a.reloadPluginTarget(tab.Path, "Fixer")
	if !strings.Contains(tab.Buffer.String(), "rewritten by plugin") {
		t.Errorf("clean buffer should have reloaded, got %q", tab.Buffer.String())
	}
}

// TestPluginCommand_ReloadPreservesUndoHistory pins that adopting a
// plugin's in-place rewrite costs one undo step, not the whole stack.
// OutputReload was the one plugin output mode that ate your history —
// OutputReplace has always been a single undoable edit, and these are
// the same promise from the user's side.
func TestPluginCommand_ReloadPreservesUndoHistory(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	tab := openScratch(t, a, "x.txt", "on disk\n")
	tab.InsertString("mine ")
	mine := tab.Buffer.String()
	tab.Dirty = false // the plugin only reloads a clean buffer

	if err := os.WriteFile(tab.Path, []byte("rewritten by plugin\n"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	a.reloadPluginTarget(tab.Path, "Fixer")

	if !strings.Contains(tab.Buffer.String(), "rewritten by plugin") {
		t.Fatalf("buffer = %q, want the plugin's rewrite", tab.Buffer.String())
	}
	if !tab.Undo() || tab.Buffer.String() != mine {
		t.Fatalf("one undo should take the rewrite back, got %q", tab.Buffer.String())
	}
}

// TestRunPluginCommand_SelectionGuard pins the one precondition worth
// enforcing: a selection filter with nothing selected would run over
// empty input and then replace an empty range.
func TestRunPluginCommand_SelectionGuard(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	sh := (&fakeShell{stdout: "x"}).install(t)
	openScratch(t, a, "x.txt", "nothing selected\n")

	a.runPluginCommand(plugins.Plugin{Name: "p"}, plugins.Command{
		ID: "s", Label: "Sort", Command: "sort",
		Input: plugins.InputSelection, Output: plugins.OutputReplace,
	})

	if sh.count() != 0 {
		t.Error("command ran with no selection")
	}
	if !strings.Contains(a.statusMsg, "select some text") {
		t.Errorf("flash = %q, want the selection hint", a.statusMsg)
	}
}

// TestRunPluginCommand_KillSwitch pins that a disabled plugin can't be
// run from a stale surface (a palette entry collected before the toggle,
// say) — "off" has to mean nothing of the user's runs.
func TestRunPluginCommand_KillSwitch(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	sh := (&fakeShell{}).install(t)
	a.plugins.enabled = false

	a.runPluginCommand(plugins.Plugin{Name: "p"}, plugins.Command{
		ID: "b", Label: "Build", Command: "make",
	})
	if sh.count() != 0 {
		t.Error("a disabled plugin ran")
	}
	if !strings.Contains(a.statusMsg, "disabled") {
		t.Errorf("flash = %q, want it to say plugins are off", a.statusMsg)
	}
}

// TestRunPluginCommand_PromptsOpenTheForm pins the reuse of the custom
// action form modal — the whole point of importing the Prompt type
// rather than inventing a second dialect.
func TestRunPluginCommand_PromptsOpenTheForm(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	sh := (&fakeShell{}).install(t)

	a.runPluginCommand(plugins.Plugin{Name: "p"}, plugins.Command{
		ID: "d", Label: "Deploy", Command: "deploy $TARGET",
		Prompts: []customactions.Prompt{{Key: "TARGET", Label: "Target", Type: "text"}},
	})

	if _, ok := a.modal.(*formModal); !ok {
		t.Fatalf("modal = %T, want the form modal", a.modal)
	}
	if sh.count() != 0 {
		t.Error("command ran before the form was submitted")
	}
}

// TestExecPluginCommand_RunsInProjectRoot pins the working directory. A
// plugin's relative paths have to mean the same thing as the ones its
// diagnostics print, and both are resolved against the project root.
func TestExecPluginCommand_RunsInProjectRoot(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	sh := (&fakeShell{stdout: "ok"}).install(t)
	openScratch(t, a, "x.txt", "hi\n")

	a.execPluginCommand(plugins.Plugin{Name: "p"}, plugins.Command{
		ID: "b", Label: "Build", Command: "make", Output: plugins.OutputFlash,
	}, nil)
	pumpAppEvents(t, a, func() bool { return sh.count() > 0 })

	if got := sh.last(t).dir; got != a.rootDir {
		t.Errorf("working dir = %q, want the project root %q", got, a.rootDir)
	}
}

// TestFirstLineOf covers the status-bar reducer, including the empty
// case that would otherwise flash a bare label with nothing after it.
func TestFirstLineOf(t *testing.T) {
	cases := map[string]string{
		"one\ntwo\n": "one",
		"  spaced  ": "spaced",
		"":           "(no output)",
		"\n\n":       "(no output)",
	}
	for in, want := range cases {
		if got := firstLineOf(in); got != want {
			t.Errorf("firstLineOf(%q) = %q, want %q", in, got, want)
		}
	}
}

// TestRunPluginShell_RealShell is the one test here that starts an
// actual process, because every other test in this file replaces
// pluginShell and would therefore pass even if the real one were
// broken. Restricted to `cat` and `echo` — the same restraint
// TestTermRealGrshIntegration keeps.
//
// It pins the three properties the callers depend on: stdin reaches the
// command, stdout and stderr come back SEPARATELY (the rule that keeps a
// formatter's warnings out of the user's source), and the environment
// entries are visible to the shell.
func TestRunPluginShell_RealShell(t *testing.T) {
	dir := t.TempDir()

	stdout, stderr, err := runPluginShell(dir, "cat", nil, "piped in\n")
	if err != nil {
		t.Fatalf("cat: %v", err)
	}
	if string(stdout) != "piped in\n" {
		t.Errorf("stdout = %q, want the piped input back", stdout)
	}
	if len(stderr) != 0 {
		t.Errorf("stderr = %q, want empty", stderr)
	}

	// Separate streams, and a non-zero exit reported as an error.
	stdout, stderr, err = runPluginShell(dir, "echo out; echo err 1>&2; exit 3", nil, "")
	if err == nil {
		t.Error("a non-zero exit should be reported as an error")
	}
	if strings.TrimSpace(string(stdout)) != "out" {
		t.Errorf("stdout = %q, want just \"out\"", stdout)
	}
	if strings.TrimSpace(string(stderr)) != "err" {
		t.Errorf("stderr = %q, want just \"err\"", stderr)
	}

	// The env slice reaches the shell — this is how $FILE, $PLUGIN_DIR
	// and every prompt answer get there.
	stdout, _, err = runPluginShell(dir, `echo "$PLUGIN_NAME"`, []string{"PLUGIN_NAME=demo"}, "")
	if err != nil {
		t.Fatalf("echo: %v", err)
	}
	if strings.TrimSpace(string(stdout)) != "demo" {
		t.Errorf("stdout = %q, want the env value", stdout)
	}
}

// TestCapBytes pins the output cap that keeps a runaway plugin from
// pushing a gigabyte through the editor.
func TestCapBytes(t *testing.T) {
	if got := capBytes([]byte("short")); string(got) != "short" {
		t.Errorf("small output was altered: %q", got)
	}
	big := make([]byte, pluginOutputLimit+100)
	if got := capBytes(big); len(got) != pluginOutputLimit {
		t.Errorf("capped length = %d, want %d", len(got), pluginOutputLimit)
	}
}

// TestRunPluginCommand_NoBufferGuard pins the second precondition: a
// mode that writes into a buffer has no honest failure mode without
// one, so it says so rather than running the command and dropping the
// result on the floor.
func TestRunPluginCommand_NoBufferGuard(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	sh := (&fakeShell{stdout: "x"}).install(t)

	a.runPluginCommand(plugins.Plugin{Name: "p"}, plugins.Command{
		ID: "s", Label: "Stamp", Command: "date", Output: plugins.OutputInsert,
	})
	if sh.count() != 0 {
		t.Error("an insert with no open file ran anyway")
	}
	if !strings.Contains(a.statusMsg, "open a file first") {
		t.Errorf("flash = %q, want the reason", a.statusMsg)
	}

	// A command that doesn't write into a buffer is unaffected.
	a.runPluginCommand(plugins.Plugin{Name: "p"}, plugins.Command{
		ID: "b", Label: "Build", Command: "make", Output: plugins.OutputFlash,
	})
	pumpAppEvents(t, a, func() bool { return sh.count() > 0 })
}
