// =============================================================================
// File: internal/app/plugincmd.go
// Author: Rohan Allison <rohanthewiz@gmail.com>
// Created: 2026-08-02
// Copyright: 2026 Rohan Allison. All rights reserved.
// =============================================================================

// plugincmd.go runs a plugin's invocable commands — the rows in ≡ →
// Plugins, their palette entries, and the Esc-x leader namespace — and
// applies whatever they print. It also owns the one shell-out helper
// the hook and decoration paths share (plugindeco.go).
//
// The interesting half is OUTPUT. actions.json could already run a
// command; what it could never do is put the result back in the file,
// and that's the whole reason this layer exists. Four rules hold it
// together:
//
//   - **stdout is the answer, stderr is the complaint.** They are
//     captured separately and never merged on this path. A formatter
//     that prints a deprecation warning to stderr must not have that
//     warning spliced into the user's source code — which is exactly
//     what a CombinedOutput here would do.
//   - **Nothing is written back over a buffer that moved.** Every run
//     records the target path and its EditRev; if the buffer changed
//     while the command was out, the output is DISCARDED with a flash
//     rather than applied to text it was never computed from. The same
//     staleness discipline the ghost text and the chat results use.
//   - **One undo step.** A replace lands as a single selection-delete
//     plus insert, which the editor's own primitives already merge into
//     one entry — so undoing a plugin is one Esc-u, not two.
//   - **A failure is loud, a success is quiet.** Non-zero exit opens the
//     info modal with the command's stderr (the same treatment custom
//     actions get); success flashes at most one line.

package app

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/rohanthewiz/ced/internal/editor"
	"github.com/rohanthewiz/ced/internal/plugins"
)

// pluginRunTimeout caps every plugin shell-out. A command that hangs
// would otherwise strand a goroutine per invocation — and since hooks
// fire on save and on every idle pause, one hanging linter would pile
// up processes for as long as the session lasts. Generous enough that a
// real formatter on a large file finishes comfortably.
const pluginRunTimeout = 30 * time.Second

// pluginOutputLimit caps what we keep from a command. Protects the
// editor from a plugin that cats a gigabyte into the buffer; the cut is
// announced wherever the output is used.
const pluginOutputLimit = 4 << 20 // 4 MiB

// pluginCmdDoneEvent carries a finished command back to the main loop.
// It holds the whole application decision — where the output goes and
// what it may safely overwrite — because by the time it lands the
// active tab, the selection, and the cursor may all be somewhere else.
type pluginCmdDoneEvent struct {
	when   time.Time
	label  string
	output plugins.OutputMode

	// path / rev identify the buffer the run was computed against.
	// Empty path means "no buffer involved" (an output mode that
	// doesn't write into one).
	path string
	rev  int

	// selStart / selEnd is the range OutputReplace overwrites when the
	// run took its input from a selection. whole says to replace the
	// entire buffer instead.
	selStart, selEnd editor.Position
	whole            bool

	// input is what was fed to stdin, kept only to decide whether the
	// command's trailing newline was one the user already had.
	input string

	stdout []byte
	stderr []byte
	err    error
}

// When implements tcell.Event.
func (e *pluginCmdDoneEvent) When() time.Time { return e.when }

// pluginShell is the single shell-out used by every plugin path. A
// package var so tests can observe and script plugin runs without ever
// spawning a real process — newTestApp points it at a stub that fails,
// so no test can execute a developer's actual plugin by accident.
//
// stdout and stderr come back separately: only the command paths care
// about the distinction, but a helper that merged them would make it
// impossible to add a path that does.
var pluginShell = runPluginShell

// runPluginShell executes command through `sh -c` in dir with env,
// feeding stdin and returning what it printed. `sh -c` rather than an
// argv split is deliberate and matches custom actions: the whole point
// is that a user writes the one-liner they'd type, pipes and all, and
// re-implementing shell quoting in Go to avoid a shell nobody asked to
// avoid would only mean supporting a worse shell.
//
// The trust position is the same as actions.json's: this file is the
// user's own, in their own config directory, and a command in it can do
// no more than a command they type. That is exactly why the inventory
// is user-scoped — see the note on project plugins in CLAUDE.md.
func runPluginShell(dir, command string, env []string, stdin string) (stdout, stderr []byte, err error) {
	ctx, cancel := context.WithTimeout(context.Background(), pluginRunTimeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, "sh", "-c", command)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), env...)
	if stdin != "" {
		cmd.Stdin = strings.NewReader(stdin)
	}
	var outBuf, errBuf bytes.Buffer
	cmd.Stdout = &outBuf
	cmd.Stderr = &errBuf
	err = cmd.Run()
	if ctx.Err() == context.DeadlineExceeded {
		err = fmt.Errorf("timed out after %s", pluginRunTimeout)
	}
	return capBytes(outBuf.Bytes()), capBytes(errBuf.Bytes()), err
}

// capBytes truncates a command's output to pluginOutputLimit.
func capBytes(b []byte) []byte {
	if len(b) <= pluginOutputLimit {
		return b
	}
	return b[:pluginOutputLimit]
}

// runPluginCommand is the entry point every command surface funnels
// through — the ≡ row, the palette entry, and the Esc-x leader. When
// the command declares prompts the form modal opens first and the shell
// runs only on submit, exactly as custom actions do.
func (a *App) runPluginCommand(p plugins.Plugin, c plugins.Command) {
	a.closeMenu()
	if !a.plugins.enabled {
		a.flash("Plugins are disabled — ≡ → Plugins → Enable plugins")
		return
	}
	// The one precondition worth enforcing: a selection filter with no
	// selection has nothing to read and nothing to write back, so it
	// would run the command over empty input and then replace an empty
	// range. Every other precondition belongs to the user's shell.
	if c.Input == plugins.InputSelection && !a.hasSelection() {
		a.flash(c.Label + ": select some text first")
		return
	}
	// The other precondition with no honest failure mode: a mode that
	// writes into a buffer, with no buffer. Left as a flash rather than
	// a dimmed menu row so the reason is visible — the row is enabled
	// because it may become runnable the moment a file opens.
	if a.activeTabPtr() == nil &&
		(c.Output == plugins.OutputInsert || c.Output == plugins.OutputReplace ||
			c.Output == plugins.OutputReload) {
		a.flash(c.Label + ": open a file first")
		return
	}
	if len(c.Prompts) == 0 {
		a.execPluginCommand(p, c, nil)
		return
	}
	a.openForm(c.Label, c.Prompts, func(app *App, values map[string]string) {
		app.execPluginCommand(p, c, values)
	})
}

// execPluginCommand collects the input, freezes what the result may
// overwrite, and runs the command off the main loop.
//
// Everything the done-handler needs is captured HERE, before the
// goroutine starts: the tab may be closed, the selection dropped and
// the active tab switched by the time the command exits, and a handler
// that re-derived any of that from live state would write the output
// into whatever the user happened to be looking at instead.
func (a *App) execPluginCommand(p plugins.Plugin, c plugins.Command, promptValues map[string]string) {
	tab := a.activeTabPtr()
	ev := &pluginCmdDoneEvent{label: c.Label, output: c.Output}

	var stdin string
	switch c.Input {
	case plugins.InputSelection:
		if tab == nil || !tab.HasSelection() {
			a.flash(c.Label + ": select some text first")
			return
		}
		start, end := editor.PosOrdered(tab.Anchor, tab.Cursor)
		stdin = tab.Buffer.Substring(start, end)
		ev.path, ev.rev = tab.Path, tab.EditRev
		ev.selStart, ev.selEnd = start, end
	case plugins.InputFile:
		if tab == nil {
			a.flash(c.Label + ": no file open")
			return
		}
		stdin = tab.Buffer.String()
		ev.path, ev.rev = tab.Path, tab.EditRev
		ev.whole = true
	}
	// Modes that write into the buffer without taking input from it
	// still need a target to check for staleness.
	if ev.path == "" && tab != nil &&
		(c.Output == plugins.OutputInsert || c.Output == plugins.OutputReload) {
		ev.path, ev.rev = tab.Path, tab.EditRev
	}
	ev.input = stdin

	vars := a.pluginVars(tab)
	env := pluginEnvFor(p, vars)
	env = append(env, promptValuesEnv(c.Prompts, promptValues)...)

	a.flash(c.Label + "…")
	scr := a.screen
	dir := a.rootDir
	go func() {
		stdout, stderr, err := pluginShell(dir, c.Command, env, stdin)
		ev.when = time.Now()
		ev.stdout, ev.stderr, ev.err = stdout, stderr, err
		_ = scr.PostEvent(ev)
	}()
}

// handlePluginCmdDone applies a finished command's output on the main
// loop. Failure short-circuits into the info modal before any output
// mode is considered — a command that exited non-zero has not produced
// a result to apply, whatever it managed to print first.
func (a *App) handlePluginCmdDone(e *pluginCmdDoneEvent) {
	if e == nil {
		return
	}
	if e.err != nil {
		// stderr rather than stdout: this is the "what went wrong"
		// channel, and errorBodyLines already formats it the way the
		// custom-action failure modal does.
		a.openInfo(e.label+" failed", errorBodyLines(e.err, e.stderr, ""))
		return
	}

	out := string(e.stdout)
	switch e.output {
	case plugins.OutputInfo:
		lines := strings.Split(strings.TrimRight(out, "\n"), "\n")
		if len(lines) == 1 && lines[0] == "" {
			lines = []string{"(no output)"}
		}
		a.openInfo(e.label, lines)
	case plugins.OutputFlash:
		a.flash(e.label + ": " + firstLineOf(out))
	case plugins.OutputReplace, plugins.OutputInsert:
		a.applyPluginEdit(e, out)
	case plugins.OutputReload:
		a.reloadPluginTarget(e.path, e.label)
	default:
		a.flash(e.label + " ✓")
	}
}

// applyPluginEdit writes a command's stdout into the buffer it was
// computed from — the staleness check, the range restore, and the
// single undo step all live here.
func (a *App) applyPluginEdit(e *pluginCmdDoneEvent, out string) {
	tab := a.tabForPath(e.path)
	if tab == nil {
		// Closed while the command ran. Silent: the user moved on.
		return
	}
	if tab.EditRev != e.rev {
		a.flash(e.label + ": buffer changed while it ran — output discarded")
		return
	}
	text := matchTrailingNewline(e.input, out)

	if e.output == plugins.OutputInsert {
		tab.InsertString(text)
		a.flash(e.label + " ✓")
		return
	}

	if e.whole {
		// Whole-buffer replace. SelectAll + InsertString is one undo
		// step (the insert's own selection-delete records it), but it
		// also parks the cursor at the very end of the file — so the
		// view is captured first and restored after. RestoreView is the
		// right primitive precisely because it does NOT set cursorMoved:
		// a scroll position being put back must not be undone by the
		// next render scrolling to the cursor.
		cursor, anchor := tab.Cursor, tab.Anchor
		scrollY, scrollX := tab.ScrollY, tab.ScrollX
		tab.SelectAll()
		tab.InsertString(text)
		tab.RestoreView(tab.Buffer.Clamp(cursor), tab.Buffer.Clamp(anchor), scrollY, scrollX)
	} else {
		// Re-establish the exact range the input came from, then let
		// InsertString's replace-the-selection path do the work.
		tab.MoveCursorTo(e.selStart, false)
		tab.MoveCursorTo(e.selEnd, true)
		tab.InsertString(text)
	}
	a.flash(e.label + " ✓")
}

// reloadPluginTarget re-reads a file a plugin rewrote in place. Refuses
// to trample a dirty buffer, the same call format-on-save makes: the
// user's unsaved edits outrank a plugin's opinion about the file.
func (a *App) reloadPluginTarget(path, label string) {
	tab := a.tabForPath(path)
	if tab == nil {
		return
	}
	if tab.Dirty {
		a.flash(label + " ran — kept your edits (file on disk changed)")
		return
	}
	if err := tab.Reload(); err != nil {
		a.flash(fmt.Sprintf("%s ran but reload failed: %v", label, err))
		return
	}
	a.flash(label + " ✓")
}

// tabForPath finds an open tab by absolute path, or nil.
func (a *App) tabForPath(path string) *editor.Tab {
	if path == "" {
		return nil
	}
	for _, t := range a.tabs {
		if t.Path == path {
			return t
		}
	}
	return nil
}

// matchTrailingNewline strips ONE trailing newline the command added
// that the input didn't have.
//
// Almost every line-oriented tool terminates its last line — `sort`,
// `jq`, `tr`, `fmt` all do — while a selection dragged across three
// lines usually stops at the end of the third with no newline after it.
// Without this rule, "sort the selection" would silently grow a blank
// line every time it ran, and running it twice would grow two. Only one
// newline is removed, so a command that genuinely wants to add a
// trailing blank line can still do it by emitting two.
func matchTrailingNewline(in, out string) string {
	if strings.HasSuffix(in, "\n") || !strings.HasSuffix(out, "\n") {
		return out
	}
	return strings.TrimSuffix(out, "\n")
}

// firstLineOf reduces command output to a single status-bar line.
func firstLineOf(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return "(no output)"
	}
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}
