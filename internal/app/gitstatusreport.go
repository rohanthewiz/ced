// =============================================================================
// File: internal/app/gitstatusreport.go
// Author: Rohan Allison <rohanthewiz@gmail.com>
// Created: 2026-08-17
// Copyright: 2026 Rohan Allison. All rights reserved.
// =============================================================================

// gitstatusreport.go implements the ≡ Git group's "Git status…" row:
// git's OWN status report, read on demand and shown in the info modal.
//
// Why a row for something ced appears to compute already: the porcelain
// snapshot behind the tree colors, the changes panel and the status bar
// (gitstatus.go) answers two questions and deliberately no others —
// which files changed, and how far HEAD sits from its upstream.
// `git status` answers the ones those surfaces drop: what the sequencer
// is in the MIDDLE of (a rebase, a cherry-pick that stopped, an
// unfinished merge and the paths still unmerged), what HEAD detached
// from, whether the branch has an upstream at all, and what a bare
// `git commit` would actually record. Re-deriving that would mean
// parsing four more porcelain surfaces; reading it costs one fork and
// arrives in git's own words, which is the form a user can act on.
//
// Shape follows the house patterns it builds on:
//
//   - Events only (gitcmd.go's rule): the fork runs on a goroutine and
//     posts a gitStatusReportEvent; only the main loop touches App or
//     opens the modal. A repo with a cold index can take a moment, and
//     forking inline would freeze the frame.
//   - The user asked for this, so a FAILURE is surfaced (the write-side
//     contract) rather than swallowed the way the background snapshot's
//     failures are.

package app

import (
	"os/exec"
	"strings"
	"time"
)

// gitStatusReportBodyWidth caps a report line at the info modal's usable
// width — the modal is 84 cells wide with a two-cell frame and a cell of
// padding either side. Longer lines are ellipsized rather than wrapped:
// a status line's information is front-loaded (the XY code and the path
// prefix), and a wrapped tail would double the row count the cap below
// then has to spend.
const gitStatusReportBodyWidth = 78

// gitStatusReportMinLines is the floor for the body cap. On a window too
// short for even this the modal overflows, which is the same trade every
// other info modal makes — showing three lines of the answer beats
// computing that there is no room for any.
const gitStatusReportMinLines = 3

// gitStatusReportArgs is the command behind the row, with two `-c`
// overrides that are both about the surface it lands on:
//
//   - color.status=false: git's default is `auto`, which already emits
//     nothing to a pipe — but a user with `color.status = always` in
//     their config would have raw SGR escapes drawn as text in a modal
//     that does no ANSI parsing.
//   - advice.statusHints=false: the "(use "git restore …" to discard)"
//     lines name shell commands for verbs that are ROWS IN THIS VERY
//     MENU, so in ced they point away from the thing the user just
//     used — and the info modal doesn't scroll, so every line spent on
//     advice is a line of the actual report that gets cut.
//
// Long format, not `--short`: the short form is the porcelain list the
// changes panel already draws, so a row producing it would say nothing
// new. The narrative header is the half worth reading.
var gitStatusReportArgs = []string{
	"-c", "color.status=false",
	"-c", "advice.statusHints=false",
	"status",
}

// gitStatusReportEvent carries one finished `git status` from the
// background goroutine to the main loop. A non-nil err means the command
// exited nonzero and out holds git's complaint — captured combined in
// that case, since a failure's explanation can land on either stream.
type gitStatusReportEvent struct {
	when time.Time
	out  []byte
	err  error
}

// When satisfies the tcell.Event interface.
func (e *gitStatusReportEvent) When() time.Time { return e.when }

// menuGitStatus forks `git status` against the project root and posts
// the result. Fire-and-forget like every other background git job here:
// a dropped event (screen shutting down) just means the report is never
// shown.
func (a *App) menuGitStatus() {
	a.closeMenu()
	if a.screen == nil || a.rootDir == "" {
		return
	}
	scr := a.screen
	root := a.rootDir
	go func() {
		out, err := exec.Command("git", append([]string{"-C", root}, gitStatusReportArgs...)...).CombinedOutput()
		_ = scr.PostEvent(&gitStatusReportEvent{when: time.Now(), out: out, err: err})
	}()
}

// handleGitStatusReport shows a finished report. Main loop only.
//
// It DECLINES the modal slot rather than taking it when something else
// already owns the screen. The round trip is milliseconds, so the case
// this guards is not a user who opened a dialog meanwhile — it is one
// that arrived unprompted (a chat permission request, a disk-conflict
// warning), and openModal replaces rather than refuses, so stealing the
// slot would silently drop that modal's pending reply. A report is
// re-runnable; a dropped permission answer leaves an agent stuck.
func (a *App) handleGitStatusReport(e *gitStatusReportEvent) {
	if a.modal != nil || a.menuOpen {
		a.flash("Git status ready — close the dialog and re-run")
		return
	}
	if e.err != nil {
		a.openInfo("Git status failed", errorBodyLines(e.err, e.out, "… (truncated)"))
		return
	}
	a.openInfo("Git status", a.gitStatusReportBody(e.out))
}

// gitStatusReportBody turns git's report into the info modal's body:
// per-line CR trim and ellipsis at the modal's width, then a cap at what
// the window can actually show, because the info modal does not scroll
// and draws every line it is handed (rows past the bottom would be
// painted off-screen and lost without a trace).
//
// The cap is REPORTED, naming the remainder — a silently short status is
// the one wrong answer a status can give, since "nothing further" and
// "twelve more untracked files" read identically once the note is gone.
// Same rule the project-search title follows.
func (a *App) gitStatusReportBody(out []byte) []string {
	text := strings.TrimRight(string(out), "\n")
	if text == "" {
		return nil // openInfo substitutes its own "(no output captured)"
	}

	raw := strings.Split(text, "\n")
	lines := make([]string, 0, len(raw))
	for _, ln := range raw {
		ln = strings.TrimRight(ln, "\r")
		if runeLen(ln) > gitStatusReportBodyWidth {
			ln = string([]rune(ln)[:gitStatusReportBodyWidth-1]) + "…"
		}
		lines = append(lines, ln)
	}

	// confirmModal's info flavour is bodyRows+7 tall (frame, title,
	// divider, the button row and its padding), so the body may claim
	// everything the window has beyond that overhead.
	max := a.height - 7
	if max < gitStatusReportMinLines {
		max = gitStatusReportMinLines
	}
	if len(lines) > max {
		// One of the surviving rows pays for the note, so the body still
		// fits: keep max-1 lines and spend the last on the count.
		hidden := len(lines) - (max - 1)
		lines = append(lines[:max-1], "… "+plural(hidden, "more line", "more lines")+" (run `git status` in the terminal for all of it)")
	}
	return lines
}

// hasGitStatusReport gates the row: any repository. Reads the flag
// refreshGitStatus stamps rather than forking git, like every other menu
// predicate here — enabled() runs on every menu draw.
//
// Deliberately NOT gated on there being changes: "nothing to commit,
// working tree clean" is a real answer, and it is the answer a user
// suspicious of the tree's colors is asking for.
func (a *App) hasGitStatusReport() bool { return a.gitIsRepo }
