// =============================================================================
// File: internal/app/runexec.go
// Author: Rohan Allison <rohanthewiz@gmail.com>
// Created: 2026-08-19
// Copyright: 2026 Rohan Allison. All rights reserved.
// =============================================================================

// Run an executable file in the terminal panel.
//
// The tree already tells you which files are runnable — that is what the
// ls -F style '*' on `mac-install.sh*` means (execmarks.go) — and the
// editor already owns a shell (terminal.go). This file is the row that
// joins them: right-click an executable in the tree (or use the ≡ File
// row), pick where it should run, and the command lands on the panel's
// input line ready for arguments.
//
// IT STAGES, IT DOES NOT SUBMIT. Same rule as the Tier-0 half of "Run
// file in a cats pane" (catsrun.go) and as handing a selection to an
// agent: the editor may COMPOSE a command, the user presses Enter. Here
// that is not merely a safety habit — it is the only place arguments can
// come from. A file's execute bit says how to start it and nothing about
// what to pass it, so a row that fired immediately would be a row you
// could never use on a script that takes a flag. Re-running is the
// panel's own history (Up arrow), which keeps the edited line, arguments
// and all — hence no per-file memory here.
//
// THE WORKING DIRECTORY IS A CHOICE, AND IT IS PART OF THE STAGED LINE.
// grsh has no `( … )` subshell to scope a `cd` inside (v1 language: "there
// is no subshell to run them in"), and its `cd` builtin chdirs the WHOLE
// editor process by design. So a scoped-cd wrapper is not available, and
// inventing one with `sh -c '…'` would bury the command inside quotes
// where arguments cannot be typed. What is left is the honest version:
// the `cd` is the first thing on the line the user is about to submit,
// visible and editable, and the panel header shows the cwd it lands in.
//
// The candidates are ordered by how specific they are to what was
// clicked — the file's own directory, the project root, wherever the
// shell currently is — and then widen into the same frecency history the
// "Open project" picker uses (catsfrecency.go): ced's own recent folders
// plus the host's cdx-ranked list. That is the cdx-style directory search
// this verb wanted, and reusing it means the two surfaces can never
// disagree about which directories exist. One candidate is not a choice,
// so a project with nothing else to offer stages straight away.
//
// The command itself is written RELATIVE to the chosen directory when it
// sits inside it (`./tool.sh`, `./scripts/build.sh`) and absolute when it
// does not — catsRelPath's rule, and what makes the staged line read like
// something a person would have typed. The `./` prefix is not decoration:
// a bare `tool.sh` is a PATH lookup, not the file in the current
// directory.
//
// The execute bit is re-checked against the live file at the moment the
// row fires, not trusted from the tree node: the node's bit is stamped at
// the last reload, and a `chmod -x` (or a delete) in between must refuse
// rather than stage something that will only fail oddly.

package app

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/rohanthewiz/ced/internal/filetree"
	"github.com/rohanthewiz/ced/internal/session"
)

// runDirsMax bounds the directory picker. The first three rows are the
// specific ones; the rest is history, and catsRecentsMax already argues
// why a fuzzy list of two hundred shell directories is a worse instrument
// than one with a dozen good ones.
const runDirsMax = 24

// runDir is one candidate working directory: where it is, and why it is
// being offered. The note becomes both the row's explanation and a fuzzy
// search term ("root", "lives", "cats"), the trick catsRecentLabel plays.
type runDir struct {
	path string
	note string
}

// menuRunExecutable is the ≡ File row: stage the active tab's file. The
// keyboard twin of the tree's right-click row, per the house rule that
// every file action stays reachable from the main menu — macOS Terminal
// under tmux swallows right-click often enough that a tree-only path is
// no path at all.
func (a *App) menuRunExecutable() {
	a.closeMenu()
	tab := a.activeTabPtr()
	if tab == nil || tab.Path == "" {
		a.flash("Nothing to run — open an executable file first")
		return
	}
	a.runExecutable(tab.Path)
}

// ctxRunExecutable is the tree-context counterpart: stage the file the
// user right-clicked. Appended to the popup only for executables (see
// openTreeContext), so it never appears on a file it would refuse.
func ctxRunExecutable(a *App, n *filetree.Node) {
	a.runExecutable(n.Path)
}

// runExecutable is the verb's single entry point: validate, then ask
// where. The refusals happen BEFORE the picker — being asked to choose a
// directory for a file that turns out not to be runnable is a question
// with no useful answer.
func (a *App) runExecutable(path string) {
	if path == "" {
		return
	}
	abs := absolutePathFor(path)
	info, err := os.Stat(abs)
	if err != nil {
		a.flash("Run: " + err.Error())
		return
	}
	if !filetree.IsExecFile(info) {
		a.flash("Not executable: " + filepath.Base(abs))
		return
	}
	dirs := a.runDirCandidates(abs)
	if len(dirs) <= 1 {
		dir := filepath.Dir(abs)
		if len(dirs) == 1 {
			dir = dirs[0].path
		}
		a.stageRun(abs, dir)
		return
	}
	items := make([]paletteItem, 0, len(dirs))
	for _, d := range dirs {
		d := d // captured per row
		items = append(items, paletteItem{
			label: runDirLabel(d),
			run:   func(app *App) { app.stageRun(abs, d.path) },
		})
	}
	a.openPicker("Run "+filepath.Base(abs)+" from…", items)
}

// runDirCandidates builds the picker's list, most specific first and
// deduped on session.Normalize — the same key ced's own folder store uses,
// so the merge here and the merge in "Recent folders…" agree about what
// "the same folder" means. Directories that no longer exist are pruned
// rather than dimmed (the recent-list rule): a row you cannot run in is
// worse than a shorter list.
func (a *App) runDirCandidates(abs string) []runDir {
	out := make([]runDir, 0, runDirsMax)
	seen := make(map[string]bool, runDirsMax)
	add := func(path, note string) {
		if path == "" || len(out) >= runDirsMax {
			return
		}
		key := session.Normalize(path)
		if key == "" || seen[key] {
			return
		}
		if info, err := os.Stat(path); err != nil || !info.IsDir() {
			return
		}
		seen[key] = true
		out = append(out, runDir{path: path, note: note})
	}

	add(filepath.Dir(abs), "where the file lives")
	add(a.rootDir, "project root")
	// Where the shell already is. Offered only when it is somewhere else,
	// which the dedup above handles for free — and worth offering at all
	// because a user who has cd'd somewhere in the panel has stated an
	// interest in that directory more recently than any history has.
	add(a.termCwd(), "terminal")
	if a.sessionStore != nil {
		for _, root := range a.sessionStore.Recent() {
			add(root, "recent")
		}
	}
	// The host's cdx-ranked history, marked so a row that turns out to be
	// a downloads directory is explained rather than surprising. `have` is
	// the dedup set the shared helper expects, in its own key space.
	have := make(map[string]bool, len(seen))
	for k := range seen {
		have[k] = true
	}
	for _, dir := range a.catsRecentFolders(have) {
		add(dir, "cats")
	}
	return out
}

// runDirLabel renders one candidate. Two spaces and a middle dot set the
// note off from the path, matching catsRecentLabel exactly — these rows
// sit in the same kind of picker and should not look like two features.
func runDirLabel(d runDir) string {
	if d.note == "" {
		return displayPath(d.path)
	}
	return displayPath(d.path) + "  · " + d.note
}

// stageRun claims the terminal panel and puts the command on its input
// line, unsubmitted. Everything after this is the panel's own machinery,
// which is the point: what the user presses Enter on is an ordinary typed
// command, with an ordinary history entry and an ordinary ⏹ button.
func (a *App) stageRun(abs, dir string) {
	if !a.term.open {
		// menuToggleTerminal is a strict toggle, hence the guard: it would
		// CLOSE an already-open panel. It also handles the single-occupancy
		// bottom strip and the session lifecycle, which is why this doesn't
		// open the panel by hand.
		a.menuToggleTerminal()
	}
	a.ensureTermSession()
	a.term.focused = true
	a.term.input = newTextField(a.runCommandLine(abs, dir))
	a.flash("Staged in the terminal — add arguments, then press Enter")
}

// runCommandLine composes the staged line. The `cd` is omitted when the
// shell is already there: a panel that opens with a pointless cd in its
// scrollback looks like the editor does not know where it is
// (menuCatsTerminal's rule), and the omission is also what keeps a second
// run from stacking a redundant one.
//
// `&&` rather than `;` so a cd that fails cannot run the command
// somewhere unexpected — catsRunScript's argument, and it matters more
// here, where the whole point of the line is which directory it runs in.
func (a *App) runCommandLine(abs, dir string) string {
	cmd := runExecArg(abs, dir)
	if session.Normalize(dir) == session.Normalize(a.termCwd()) {
		return cmd
	}
	return "cd " + shellArg(dir) + " && " + cmd
}

// runExecArg spells the executable the way a shell sitting in dir would:
// `./tool.sh` or `./scripts/build.sh` inside it, the absolute path outside
// it. The `./` is load-bearing — a bare name is a PATH lookup.
func runExecArg(abs, dir string) string {
	rel, err := filepath.Rel(dir, abs)
	if err != nil || rel == "" || strings.HasPrefix(rel, "..") {
		return shellArg(abs)
	}
	return shellArg("./" + filepath.ToSlash(rel))
}

// shellArg quotes only what needs quoting. The staged line is something
// the user READS and EDITS, so blanketing every path in single quotes
// (catsShellQuote's unconditional form, right for a line nobody sees)
// would make the common case harder to work with for no gain.
func shellArg(s string) string {
	if s == "" {
		return "''"
	}
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
		case r == '/', r == '.', r == '_', r == '-', r == '+', r == ',', r == ':', r == '@', r == '%', r == '=':
		default:
			return catsShellQuote(s)
		}
	}
	return s
}

// termCwd is where the panel's shell is, or would be. The session is
// built lazily, so before the first open the honest answer is the editor
// process's own cwd — which is exactly what a fresh grsh session inherits.
func (a *App) termCwd() string {
	if a.term.sess != nil {
		return a.term.sess.Cwd()
	}
	if wd, err := os.Getwd(); err == nil {
		return wd
	}
	return ""
}

// hasRunnableTab gates the ≡ row: an open file that is executable right
// now. A stat per layout pass is affordable here — menuLayout only runs
// while the menu is on screen — and it is the honest question, unlike a
// cached tree bit that goes stale the moment the user runs `chmod +x` in
// the very panel this row feeds.
func (a *App) hasRunnableTab() bool {
	tab := a.activeTabPtr()
	if tab == nil || tab.Path == "" {
		return false
	}
	info, err := os.Stat(absolutePathFor(tab.Path))
	return err == nil && filetree.IsExecFile(info)
}

// runExecutableLabel names the file the row would run, so the menu says
// what will happen before it happens — the zipFolderLabel pattern. The
// bare label is kept for the disabled case, where there is no file to
// name and "Run file in terminal" still explains what the row is for.
func (a *App) runExecutableLabel() string {
	if tab := a.activeTabPtr(); tab != nil && tab.Path != "" && a.hasRunnableTab() {
		return "Run " + filepath.Base(tab.Path) + " in terminal…"
	}
	return "Run file in terminal…"
}
