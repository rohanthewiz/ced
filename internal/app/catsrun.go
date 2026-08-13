// =============================================================================
// File: internal/app/catsrun.go
// Author: Rohan Allison <rohanthewiz@gmail.com>
// Created: 2026-08-13
// Copyright: 2026 Rohan Allison. All rights reserved.
// =============================================================================

// Real terminal panes: inside cats, "give me a shell" and "run this file"
// stop being a REPL strip inside the editor and become a REAL pty beside it.
//
// WHY THIS EXISTS WHEN ced ALREADY HAS A TERMINAL
//
// The built-in panel (terminal.go) is a grsh REPL: compiled in, plain text,
// works over ssh, and deliberately cannot host a full-screen child. That is
// the right Tier-0 answer and it is not going away. But `go test ./...`,
// `npm run dev`, a build that draws a progress bar — those want a pty, and
// inside cats there is one available for the cost of a control call. So the
// two coexist under the ladder's rule: the FEATURE (run something, watch it)
// exists at Tier 0 in the panel, and at Tier 1 it upgrades to a real pane.
// The Tier-0 fallback here is not a refusal — "Run file" outside Tier 1
// opens the grsh panel with the command already typed in.
//
// HOW A RUN KNOWS IT FINISHED
//
// A pane is a screen, not a process handle: nothing in the control API says
// "the command you typed exited with 2". So the run prints its own answer and
// the host watches for it:
//
//	sh -c 'cd <root> && ( <cmd> ); printf "\n[ced run 41.3] exit:%s\n" "$?"'
//	                                        └──────── the marker ────────┘
//	pane.wait_for_output  pattern = \[ced run 41\.3\] exit:[0-9]+  (regex)
//
// Three details make that safe rather than clever:
//
//   - THE TYPED LINE MUST NOT MATCH THE PATTERN. wait_for_output is seeded
//     with what is already on screen, and the shell ECHOES the command — so
//     a naive marker would match its own invocation the instant it was typed.
//     The format string carries `%s` where the pattern needs `[0-9]+`, so the
//     echo cannot match and only the printed result can.
//   - THE MARKER IS UNIQUE PER RUN (pid + sequence), so a marker left in the
//     scrollback by an earlier run — or by another ced in another pane —
//     cannot satisfy this run's wait.
//   - THE COMMAND RUNS UNDER `sh -c`, not under the user's login shell. The
//     pane belongs to whatever shell they chose, and `$?` is not fish's
//     spelling of the exit status; wrapping in sh makes the exit-code
//     protocol independent of the shell the user happens to like.
//
// THE HOOK IS THE POINT OF THE WAIT. While a run is in flight ced reports
// `working` for its own pane, and cats turns the working→idle edge into its
// "finished" notification — toast, badge, or phone push, per the user's own
// cats config. That is why the wait exists at all: not to print an exit code
// in a status bar the user is looking at, but to reach them when they are
// looking at something else. The exit code itself lands in ced's status bar,
// because the hook's vocabulary is state-only and cannot carry it.
//
// A failed run is NOT reported as `blocked`. Blocked means the editor is
// asking a question the user did not ask for (cats_glue.go); a build that
// failed is an answer, not a question, and a channel that pages you for both
// is a channel you mute.

package app

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/gdamore/tcell/v2"

	"github.com/rohanthewiz/ced/internal/cats"
)

// catsRunWait bounds one run's wait. It is cats' own per-wait cap
// (MaxWaitTimeout): the server clamps anything larger, so asking for more
// would only make this side's arithmetic wrong. A run still going at the cap
// is not killed — the wait gives up, the pane keeps running, and the notice
// says exactly that.
const catsRunWait = cats.MaxWaitTimeout

// catsRunScrollGuard is how long the run pane's own shell gets to appear
// before the command is typed at it. pane.split answers as soon as the pane
// exists; the shell inside it is a process that has to start, and input
// arriving before it has drained its first read is input the terminal driver
// keeps but the shell may echo strangely. A short sleep is the honest fix —
// there is no "shell is ready" event to wait on.
//
// A package var rather than a constant so tests can collapse it, the same way
// cats.ProbeTimeout is one: a quarter second per run is invisible to a user
// and is pure dead time in a test suite.
var catsRunScrollGuard = 250 * time.Millisecond

// -----------------------------------------------------------------------------
// A real terminal pane
// -----------------------------------------------------------------------------

// menuCatsTerminal splits a pty sibling running the user's own shell, rooted
// at this project.
//
// The split inherits the source pane's cwd (cats resolves it before the
// split, the same way a new tab inherits its neighbour's), which is where ced
// was STARTED — usually but not always the project root. So the shell is sent
// a `cd` when the two differ, and nothing at all when they do not: a pane
// that opens with a pointless `cd` in its scrollback looks like the editor
// does not know where it is.
func (a *App) menuCatsTerminal() {
	a.closeMenu()
	if !a.catsTier1() {
		// The Tier-0 terminal is a real feature, not a consolation: say which
		// one the user is getting and open it.
		a.flash("No cats control socket — opening ced's own terminal instead")
		if !a.term.open {
			a.menuToggleTerminal()
		}
		return
	}
	client, scr := a.cats.client, a.screen
	self := a.catsSelfPane()
	// Two spellings of "start there", for the two vintages of host. Cwd rides
	// the split itself when the host takes one, so the shell simply STARTS in
	// the root and its scrollback opens clean; the `cd` line is the older
	// host's only way to say the same thing, and is still sent only when the
	// directories actually differ.
	spawn := catsSpawn{}
	if cwd := a.catsPaneCwd(a.cats.self); cwd != a.rootDir {
		spawn.Cwd = a.rootDir
		spawn.Line = "cd " + catsShellQuote(a.rootDir) + "\n"
	}
	go func() {
		if _, err := catsSpawnSibling(client, self, cats.SplitVertical, spawn); err != nil {
			catsPostNotice(scr, "Terminal split failed: "+err.Error())
			return
		}
		catsPostNotice(scr, "Opened a terminal pane below")
	}()
}

// catsSelfPane is our own pane as an optional argument: the resolved id when
// we have it, nil ("split whichever pane is focused") when we do not. Every
// self-addressing spawn wants exactly this, and nil is the right fallback
// because the pane the user is typing in IS the focused one.
func (a *App) catsSelfPane() *uint32 {
	if !a.cats.selfOK {
		return nil
	}
	id := a.cats.self
	return &id
}

// catsPaneCwd reads a pane's live cwd out of the cached pane list ("" when
// unknown). Cache-only, because every caller is on the main loop.
func (a *App) catsPaneCwd(pane uint32) string {
	for _, p := range a.cats.panes {
		if p.Pane == pane {
			return p.Cwd
		}
	}
	return ""
}

// hasCatsTerminal gates the terminal row. It is enabled below Tier 1 too —
// the row still does something there (it opens ced's own terminal), and a
// dimmed row would hide a working feature behind an unexplained gap.
func (a *App) hasCatsTerminal() bool { return true }

// -----------------------------------------------------------------------------
// Run file
// -----------------------------------------------------------------------------

// menuCatsRunFile asks what to run, then runs it. The command is a PROMPT
// with a guess in it rather than a table lookup that fires immediately:
// the guess is right often enough to be worth making and wrong often enough
// that running it unseen would be a nasty surprise (`go run main.go` in a
// package with three files, `python3` on a machine that spells it `python`).
func (a *App) menuCatsRunFile() {
	a.closeMenu()
	tab := a.activeTabPtr()
	if tab == nil {
		a.flash("Nothing to run — open a file first")
		return
	}
	if a.cats.runActive > 0 {
		// One run at a time, because the run pane is reused: a second command
		// typed at a pane already running something goes into that program's
		// stdin, not to a shell.
		a.flash("A run is already in flight — wait for it or watch its pane")
		return
	}
	a.openPrompt("Run in a cats pane", "in "+abbrevHomePath(a.rootDir), a.catsRunInitial(tab.Path),
		func(app *App, cmd string) { app.catsRun(cmd) })
}

// catsRunInitial is what the prompt starts with: the last command the user
// ran, but only while they are still on the file it was run for.
//
// The rule is the useful half of both behaviours. Re-running `go test ./...`
// on the file you are iterating on is the common case and wants no retyping;
// switching to a Python file and being offered `go test` is the case where
// the memory becomes a trap.
func (a *App) catsRunInitial(path string) string {
	if a.cats.lastRunCmd != "" && a.cats.lastRunFile == path {
		return a.cats.lastRunCmd
	}
	return catsRunGuess(a.catsRelPath(path))
}

// catsRunGuess maps a file to the command that most likely runs it. The table
// is deliberately short: an interpreter invocation is a guess anyone can
// correct at a glance, whereas a build system's incantation guessed wrong
// wastes the user's time proving it wrong.
//
// The path handed in is already project-relative (the run's cwd is the
// project root), so the command reads the way the user would have typed it.
func catsRunGuess(rel string) string {
	if rel == "" {
		return ""
	}
	q := catsShellQuote(rel)
	switch strings.ToLower(filepath.Ext(rel)) {
	case ".go":
		// Not `go run .`: the file the user is looking at is the subject, and
		// a package with a main in it accepts the file form.
		return "go run " + q
	case ".py":
		return "python3 " + q
	case ".js", ".mjs", ".cjs":
		return "node " + q
	case ".sh", ".bash":
		return "sh " + q
	case ".rb":
		return "ruby " + q
	case ".pl":
		return "perl " + q
	case ".lua":
		return "lua " + q
	case ".ts":
		// tsx over tsc: the question "run this file" has one answer in a
		// TypeScript project and it is not "compile it somewhere".
		return "npx tsx " + q
	}
	switch strings.ToLower(filepath.Base(rel)) {
	case "makefile", "gnumakefile":
		return "make"
	case "cargo.toml":
		return "cargo run"
	case "package.json":
		return "npm start"
	}
	return ""
}

// catsRelPath renders a path the way a shell sitting in the project root
// would want it: relative when it is inside the project, absolute when it is
// not. The same rule the selection quote uses.
func (a *App) catsRelPath(path string) string {
	if path == "" {
		return ""
	}
	if rel, err := filepath.Rel(a.rootDir, path); err == nil && !strings.HasPrefix(rel, "..") {
		return rel
	}
	return path
}

// catsRun launches one command. Everything after this function is IO: the
// pane, the typing, and a wait that can legitimately last minutes.
func (a *App) catsRun(cmd string) {
	if cmd == "" {
		return
	}
	if tab := a.activeTabPtr(); tab != nil {
		a.cats.lastRunCmd, a.cats.lastRunFile = cmd, tab.Path
	}
	if !a.catsTier1() {
		// The Tier-0 half of "run this": ced's own terminal, with the command
		// typed in but NOT submitted. Same rule as sending a selection to an
		// agent — the editor may compose a command, the user presses Enter.
		a.catsRunInPanel(cmd)
		return
	}
	a.cats.runSeq++
	marker := fmt.Sprintf("[ced run %d.%d] exit:", os.Getpid(), a.cats.runSeq)
	script := catsRunScript(a.rootDir, cmd, marker)

	// The hook badge goes up HERE, on the main loop, before any IO: the
	// after-event pump runs at the end of this same event, so the pane is
	// marked working from the moment the user pressed OK rather than from
	// whenever the split happens to complete.
	a.cats.runActive++
	a.cats.runLabel = cmd

	client, scr := a.cats.client, a.screen
	self := a.catsSelfPane()
	reuse, reuseOK := a.cats.runPane, a.cats.runPaneOK
	go func() {
		pane, err := catsRunPane(client, self, reuse, reuseOK)
		if err != nil {
			catsPostRunDone(scr, "Run failed: "+err.Error(), false, 0, false)
			return
		}
		// The pane exists but its shell may not have read its first byte yet.
		time.Sleep(catsRunScrollGuard)
		if err := client.PaneSendInput(pane, "sh -c "+catsShellQuote(script)+"\n", true); err != nil {
			catsPostRunDone(scr, "Run failed: "+err.Error(), false, pane, true)
			return
		}
		matched, text, err := client.WaitForOutput(pane, regexp.QuoteMeta(marker)+"[0-9]+", true, catsRunWait)
		catsPostRunDone(scr, catsRunOutcome(cmd, marker, matched, text, err), matched && catsRunOK(text, marker), pane, true)
	}()
}

// catsRunInPanel is the Tier-0 fallback: open ced's own terminal and stage
// the command on its input line.
func (a *App) catsRunInPanel(cmd string) {
	if !a.term.open {
		a.menuToggleTerminal()
	}
	a.term.focused = true
	a.term.input = newTextField(cmd)
	a.flash("Staged in ced's terminal — press Enter to run it")
}

// catsRunScript is the sh program the pane is asked to run. See the file
// comment for why the marker's format string is what keeps the echoed
// command from satisfying the wait.
//
// `cd … && cmd` rather than two statements: a cd that fails must not run the
// command somewhere unexpected, and its own failure is then the exit code the
// user is told about.
//
// The command runs in a SUBSHELL — the parentheses — so that a command which
// ends its own shell cannot take the reporting line with it. `exit 3`,
// `exec something`, a script that sources an rc ending in exit: all of those
// would otherwise skip the printf entirely, and the run would look like it
// never finished rather than like it failed.
func catsRunScript(root, cmd, marker string) string {
	return fmt.Sprintf("cd %s && ( %s ); printf '\\n%s%%s\\n' \"$?\"",
		catsShellQuote(root), cmd, marker)
}

// catsRunPane resolves which pane the command runs in: the one the last run
// used when it is still there and still unclaimed, otherwise a fresh split
// below.
//
// Reuse is what keeps a session of twelve runs from ending in twelve panes.
// It is conditional on the remembered pane still being LISTED (it may have
// been closed) and on it having acquired no agent since (an agent in there
// means something else took the pane over, and typing a build command at a
// coding agent is exactly the mistake the self-check in catsagents.go exists
// to prevent).
func catsRunPane(client *cats.Client, self *uint32, reuse uint32, reuseOK bool) (uint32, error) {
	if reuseOK {
		panes, err := client.PaneList()
		if err != nil {
			return 0, err
		}
		for _, p := range panes {
			if p.Pane == reuse && p.Agent == "" {
				return reuse, nil
			}
		}
	}
	return catsSpawnSibling(client, self, cats.SplitVertical, catsSpawn{})
}

// catsRunOK reports whether the matched marker line says exit 0.
func catsRunOK(text, marker string) bool {
	code, ok := catsRunExit(text, marker)
	return ok && code == 0
}

// catsRunExit pulls the exit code out of the line the wait matched. The
// LAST occurrence of the marker is the one read: the same line can carry the
// echoed command ahead of the printed result when a shell wraps its echo, and
// the result is always the rightmost.
func catsRunExit(text, marker string) (int, bool) {
	i := strings.LastIndex(text, marker)
	if i < 0 {
		return 0, false
	}
	rest := text[i+len(marker):]
	end := 0
	for end < len(rest) && rest[end] >= '0' && rest[end] <= '9' {
		end++
	}
	if end == 0 {
		return 0, false
	}
	code, err := strconv.Atoi(rest[:end])
	if err != nil {
		return 0, false
	}
	return code, true
}

// catsRunOutcome phrases the run's result for the status bar. Four outcomes,
// each said differently on purpose — "failed" for a non-zero exit is a
// different fact from "we stopped watching", and a user who is told the wrong
// one goes looking in the wrong place.
func catsRunOutcome(cmd, marker string, matched bool, text string, err error) string {
	label := catsRunLabel(cmd)
	switch {
	case err != nil:
		return label + ": lost track of the run — " + err.Error()
	case !matched:
		// The wait timed out or the pane exited: the command may still be
		// running perfectly well. Point at the pane rather than guessing.
		return label + ": still running after " + catsRunWait.String() + " — watch its pane"
	}
	code, ok := catsRunExit(text, marker)
	switch {
	case !ok:
		return label + ": finished"
	case code == 0:
		return label + ": finished ✓"
	default:
		return fmt.Sprintf("%s: failed ✗ exit %d", label, code)
	}
}

// catsRunLabel shortens a command for a status line. A run is often a long
// line ("go test ./internal/... -run TestThing -race -count=1") and the
// status bar is one row shared with everything else ced has to say.
func catsRunLabel(cmd string) string {
	const max = 40
	cmd = strings.TrimSpace(cmd)
	if len(cmd) <= max {
		return cmd
	}
	return cmd[:max-1] + "…"
}

// catsPostRunDone hands a finished run back to the main loop: the phrase, the
// verdict, and which pane it used (so the next run can reuse it).
func catsPostRunDone(scr tcell.Screen, notice string, ok bool, pane uint32, paneOK bool) {
	if scr == nil {
		return
	}
	_ = scr.PostEvent(&catsEvent{
		when: time.Now(), kind: catsKindRunDone,
		notice: notice, runOK: ok, pane: pane, paneOK: paneOK,
	})
}

// catsRunDone is the main-loop side: drop the hook badge, remember the pane,
// and say what happened.
func (a *App) catsRunDone(e *catsEvent) {
	if a.cats.runActive > 0 {
		a.cats.runActive--
	}
	if a.cats.runActive == 0 {
		a.cats.runLabel = ""
	}
	if e.paneOK {
		a.cats.runPane, a.cats.runPaneOK = e.pane, true
	}
	if e.notice != "" {
		a.flash(e.notice)
	}
}

// hasCatsRun gates the run row: something has to be open to run. Like the
// terminal row it stays enabled below Tier 1, where it stages the command in
// ced's own terminal instead.
func (a *App) hasCatsRun() bool { return a.activeTabPtr() != nil }
