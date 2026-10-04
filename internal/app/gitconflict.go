// =============================================================================
// File: internal/app/gitconflict.go
// Author: Rohan Allison <rohanthewiz@gmail.com>
// Created: 2026-08-12
// Copyright: 2026 Rohan Allison. All rights reserved.
// =============================================================================

// gitconflict.go is the way out of a stopped git operation. A
// cherry-pick, revert, merge or rebase that hits a conflict leaves the
// repository parked: files carry <<<<<<< markers, the index holds
// unmerged entries, and until someone finishes or abandons the operation
// almost every other git verb refuses to run. Before this file ced
// reported that state as a wall of stderr in the info modal and left the
// user to reach for a terminal.
//
// The surface is one picker (gitLogActionItems' house rule — choose one
// from a list) whose rows are the four things anyone actually does next:
//
//	Open 2 conflicted files            ← first, and the default row
//	  Open internal/app/gitlog.go        (per-file, so the picker also
//	  Open internal/app/gitpanel.go       ANSWERS "which files?")
//	Mark all resolved and continue…    ← the one-gesture happy path
//	Abort the cherry-pick…             ← last, behind a confirm
//
// Four decisions worth keeping:
//
//   - The operation is RE-DERIVED from the repo on every open, never
//     remembered from the command that failed. The picker is reachable
//     long after that command (the ≡ Git row, a second right-click), and
//     a remembered verb would eventually offer `cherry-pick --abort` for
//     a rebase someone started in a terminal.
//   - "Resolved" means the index says so, not that the markers are gone.
//     git refuses --continue while ANY path is unmerged, so a marker-free
//     but unstaged file is still a conflict; the marker scan only decides
//     which files are SAFE to stage, since `git add` on a file that still
//     contains <<<<<<< is how conflict markers get committed.
//   - Staging and continuing are one row when every file is ready, via
//     runGitCmdSeq — "I'm done" is one thought, and the two-call spelling
//     would race.
//   - --continue runs with GIT_EDITOR=true in the child environment,
//     because git opens the commit-message editor there and a TUI editor
//     cannot hand its terminal to $EDITOR mid-frame. The environment is
//     the only spelling that holds — see gitNoEditorEnv for the two that
//     look right and aren't.

package app

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

const (
	// gitConflictListMax caps both the per-file picker rows and how many
	// tabs "Open conflicted files" will open at once. A conflicted rebase
	// of a long branch can list dozens of paths; opening dozens of tabs is
	// not help, it is a mess someone then has to close. The cap is
	// announced when it bites (flash + row label) — a silently shortened
	// list would read as the repo having fewer conflicts than it has.
	gitConflictListMax = 12

	// gitConflictScanMax bounds the marker scan per file. Conflicted
	// source files are kilobytes; anything past this is a generated blob
	// or binary, where a byte scan costs more than the answer is worth.
	// Over-cap files are reported as "still has markers", the safe answer:
	// it withholds them from the stage row rather than staging something
	// unexamined.
	gitConflictScanMax = 4 << 20
)

// gitConflictOp names an operation that can be parked mid-conflict, and
// carries the two things that differ between them: the state file git
// leaves behind while it is in progress, and the subcommand that resumes
// or abandons it (identical to the op name for all four, but spelled out
// so the table reads as the contract it is).
type gitConflictOp struct {
	name string
	// marker is a path under the git dir whose existence means "this
	// operation is in progress". Rebase leaves a DIRECTORY (two of them,
	// depending on which backend ran), which is why this is a plain stat
	// rather than a file read.
	marker string
}

// gitConflictOps is the detection table, most-specific first. Order
// matters for exactly one pair: an interactive rebase that stops to
// cherry-pick a commit leaves CHERRY_PICK_HEAD *and* rebase-merge/, and
// the right verb there is `rebase --continue` — `cherry-pick --continue`
// would advance one commit and then leave the rebase stranded.
var gitConflictOps = []gitConflictOp{
	{name: "rebase", marker: "rebase-merge"},
	{name: "rebase", marker: "rebase-apply"},
	{name: "cherry-pick", marker: "CHERRY_PICK_HEAD"},
	{name: "revert", marker: "REVERT_HEAD"},
	{name: "merge", marker: "MERGE_HEAD"},
}

// gitInProgressOp reports which operation the repo is parked in the
// middle of, or "" for none. Best-effort like every git read here: a
// failed rev-parse reads as "nothing in progress", which downgrades the
// picker to its open-and-stage rows rather than offering an --abort that
// would only produce an error.
func gitInProgressOp(rootDir string) string {
	if rootDir == "" {
		return ""
	}
	out, err := exec.Command("git", "-C", rootDir, "rev-parse", "--absolute-git-dir").Output()
	if err != nil {
		return ""
	}
	return gitOpFromGitDir(strings.TrimRight(string(out), "\n\r"))
}

// gitOpFromGitDir is the stat half of gitInProgressOp, split out so the
// table's order and its markers can be pinned by a test that builds a
// fake git dir out of plain files — no repository, no forks.
func gitOpFromGitDir(gitDir string) string {
	if gitDir == "" {
		return ""
	}
	for _, op := range gitConflictOps {
		if _, err := os.Stat(filepath.Join(gitDir, op.marker)); err == nil {
			return op.name
		}
	}
	return ""
}

// gitConflictedPaths returns the repo-relative paths git currently
// reports as unmerged, plus the work-tree root they hang off. `git diff
// --diff-filter=U` is the direct question ("which paths are unmerged?")
// where porcelain's XY table is an inference, and -z removes the quoting
// git otherwise applies to paths with spaces or non-ASCII.
//
// Read fresh on every picker open rather than off the status snapshot:
// the snapshot is up to ten seconds old, and a `git add` aimed at a
// ten-second-old list is a write against a repository that has moved.
func gitConflictedPaths(rootDir string) (root string, rels []string) {
	root = gitToplevel(rootDir)
	if root == "" {
		return "", nil
	}
	out, err := exec.Command("git", "-C", rootDir, "diff",
		"--name-only", "--diff-filter=U", "-z").Output()
	if err != nil {
		return root, nil
	}
	for _, raw := range bytes.Split(out, []byte{0}) {
		if p := string(raw); p != "" {
			rels = append(rels, p)
		}
	}
	return root, rels
}

// fileHasConflictMarkers reports whether path still contains a conflict
// marker line. Unreadable and over-large files answer true — the callers
// use this to decide what is safe to `git add`, and "I could not check"
// must never come out as "go ahead".
//
// Only the "<<<<<<<" opener is looked for: git writes all three markers
// together, the opener is the one that cannot appear as ordinary content
// at the start of a line in any language ced is likely to hold, and
// scanning for one is a third of the work.
func fileHasConflictMarkers(path string) bool {
	st, err := os.Stat(path)
	if err != nil || st.Size() > gitConflictScanMax {
		return true
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return true
	}
	for _, line := range bytes.Split(data, []byte{'\n'}) {
		if bytes.HasPrefix(line, []byte("<<<<<<<")) {
			return true
		}
	}
	return false
}

// gitConflictReady splits conflicted paths into the ones whose markers
// are gone (safe to stage) and the ones still carrying them.
func gitConflictReady(root string, rels []string) (ready, pending []string) {
	for _, rel := range rels {
		if fileHasConflictMarkers(filepath.Join(root, rel)) {
			pending = append(pending, rel)
		} else {
			ready = append(ready, rel)
		}
	}
	return ready, pending
}

// -----------------------------------------------------------------------------
// The picker
// -----------------------------------------------------------------------------

// openGitConflictPicker shows the way out of the current conflict. It is
// the handler a failed cherry-pick / revert routes to, and the ≡ Git
// row's target for coming back to a conflict that was dismissed.
func (a *App) openGitConflictPicker() {
	op := gitInProgressOp(a.rootDir)
	root, rels := gitConflictedPaths(a.rootDir)
	if op == "" && len(rels) == 0 {
		a.flash("No conflicted files")
		return
	}
	a.openPicker(gitConflictTitle(op, len(rels)), a.gitConflictItems(op, root, rels))
	// A stopped cherry-pick/revert is the definition of an unasked-for
	// question: the operation was supposed to finish on its own and now
	// waits on a human. Marked even when reached from ≡ — the repo is
	// mid-operation either way, and that is what the badge reports. See
	// cats_glue.go.
	reason := "conflicts need resolving" // no in-progress op, just conflicted files
	if op != "" {
		reason = op + " stopped on conflicts"
	}
	a.catsAsking(reason)
}

// gitConflictTitle names the state the picker is answering for. The
// count is in the title because "how bad is it?" is the question asked
// before any of the rows are read.
func gitConflictTitle(op string, n int) string {
	what := "Conflict"
	if op != "" {
		what = strings.ToUpper(op[:1]) + op[1:] + " conflict"
	}
	if n == 0 {
		return what + " · nothing unmerged"
	}
	return what + " · " + plural(n, "file", "files")
}

// gitConflictItems builds the rows. The order is a safety gradient:
// looking at the damage first, resolving in the middle, and the two
// irreversible verbs last, so the Enter that lands on the default row can
// only ever open files.
func (a *App) gitConflictItems(op, root string, rels []string) []paletteItem {
	ready, pending := gitConflictReady(root, rels)
	// A modify/delete conflict has no markers, so the scan calls it
	// ready — and staging it would silently decide whether the file
	// exists. Those leave the stage rows and get Keep / Delete rows of
	// their own below (gitopstate.go's isPresenceConflict).
	presence := a.presenceConflicts()
	ready, pending = splitPresence(ready, pending, presence)
	items := make([]paletteItem, 0, len(rels)+8)

	// Open — all of them, then one row per file so the picker doubles as
	// the list of what conflicted (and its fuzzy field as a way to find
	// one path among many).
	if len(rels) == 1 {
		rel := rels[0]
		items = append(items, paletteItem{
			label: "Open " + rel,
			run:   func(app *App) { app.gitConflictOpenFiles(root, []string{rel}) },
		})
	} else if len(rels) > 1 {
		label := "Open " + itoa(len(rels)) + " conflicted files"
		if len(rels) > gitConflictListMax {
			label = "Open the first " + itoa(gitConflictListMax) + " of " +
				itoa(len(rels)) + " conflicted files"
		}
		items = append(items, paletteItem{
			label: label,
			run:   func(app *App) { app.gitConflictOpenFiles(root, rels) },
		})
		for i, rel := range rels {
			if i >= gitConflictListMax {
				break
			}
			rel := rel // capture per-iteration for the closure
			items = append(items, paletteItem{
				label: "Open " + rel,
				run:   func(app *App) { app.gitConflictOpenFiles(root, []string{rel}) },
			})
		}
	}

	// Resolve. Staging every file at once is offered only when every file
	// is marker-free, and then it continues in the same gesture; a partial
	// set gets the plain stage row and says what is still holding it up.
	switch {
	case len(ready) > 0 && len(pending) == 0 && len(presence) == 0 && op != "":
		items = append(items, paletteItem{
			label: "Mark all resolved and continue the " + op,
			run:   func(app *App) { app.gitConflictStageAndContinue(op, root, ready) },
		})
	case len(ready) > 0:
		label := "Stage " + plural(len(ready), "resolved file", "resolved files")
		if len(pending) > 0 {
			label += " (" + plural(len(pending), "file still has", "files still have") + " markers)"
		} else if len(presence) > 0 {
			label += " (" + plural(len(presence), "file needs", "files need") + " keep or delete)"
		}
		items = append(items, paletteItem{
			label: label,
			run:   func(app *App) { app.gitConflictStage(root, ready) },
		})
	}

	// Keep / Delete for each presence conflict, in the list's order and
	// under its cap, labelled with which side removed the file.
	shown := 0
	for _, rel := range rels {
		xy, ok := presence[rel]
		if !ok || shown >= gitConflictListMax {
			continue
		}
		shown++
		f := conflictFile{rel: rel, abs: filepath.Join(root, rel), xy: xy}
		if xy != "DD" {
			items = append(items, paletteItem{
				label: "Keep " + rel + " (" + conflictKindLabel(xy) + ")",
				run:   func(app *App) { app.conflictKeepFile(f) },
			})
		}
		items = append(items, paletteItem{
			label: "Delete " + rel + " (" + conflictKindLabel(xy) + ")",
			run:   func(app *App) { app.conflictDeleteFile(f) },
		})
	}

	// The panel is the mouse-side home of everything here; this row is
	// the keyboard's way to put it up (or take it down).
	items = append(items, paletteItem{
		label: a.conflictPanelToggleLabel(),
		run:   func(app *App) { app.toggleTool(toolConflicts) },
	})

	if op == "" {
		// A conflict with no operation in progress — a stash pop is the
		// usual way in. There is nothing to continue or abort; resolving
		// and staging IS the whole job.
		return items
	}

	// Continue on its own is the state where the index is already clean —
	// everything staged, git just waiting to be told to go on. Offering it
	// alongside unmerged paths would be offering an error.
	if len(rels) == 0 {
		items = append(items, paletteItem{
			label: "Continue the " + op,
			run:   func(app *App) { app.gitConflictContinue(op) },
		})
	}

	// Skip sits with Abort, behind a confirm, after everything that keeps
	// work: it drops the stopped commit from the operation. A merge has no
	// "next commit" to skip to, so it never offers one.
	if op != "merge" {
		items = append(items, paletteItem{
			label: "Skip this commit…",
			run: func(app *App) {
				app.refreshConflictPanelInfo()
				app.confirmConflictSkip()
			},
		})
	}
	items = append(items, paletteItem{
		label: "Abort the " + op + "…",
		run:   func(app *App) { app.gitConflictAbort(op) },
	})
	return items
}

// presenceConflicts maps the toplevel-relative path of every
// modify/delete-style unmerged file to its XY code. One fork, on the
// picker's open; empty outside a repository.
func (a *App) presenceConflicts() map[string]string {
	_, files := loadConflictFiles(a.rootDir)
	out := make(map[string]string)
	for _, f := range files {
		if isPresenceConflict(f.xy) {
			out[f.rel] = f.xy
		}
	}
	return out
}

// splitPresence moves presence conflicts out of the ready list (they need
// a Keep/Delete decision, not a stage) and out of pending too (they have
// no markers to finish), leaving both lists about content conflicts only.
func splitPresence(ready, pending []string, presence map[string]string) (r, p []string) {
	for _, rel := range ready {
		if _, ok := presence[rel]; !ok {
			r = append(r, rel)
		}
	}
	for _, rel := range pending {
		if _, ok := presence[rel]; !ok {
			p = append(p, rel)
		}
	}
	return r, p
}

// gitConflictOpenFiles opens conflicted files as tabs — the git gutter
// then marks the conflicting regions in place, which is the whole reason
// this beats printing paths at the user.
//
// The first file is re-opened after the loop so it ends up ACTIVE:
// openFile switches to what it opens, so a plain loop would leave the
// user staring at the last path in the list.
func (a *App) gitConflictOpenFiles(root string, rels []string) {
	if root == "" || len(rels) == 0 {
		return
	}
	first, opened := "", 0
	for _, rel := range rels {
		if opened >= gitConflictListMax {
			break
		}
		abs := filepath.Join(root, rel)
		if !pathInside(abs, a.rootDir) {
			continue // a conflict outside the open project — not ours to show
		}
		a.openFile(abs)
		if first == "" {
			first = abs
		}
		opened++
	}
	if opened == 0 {
		a.flash("No conflicted files inside this project")
		return
	}
	// The FIRST one opened, not rels[0] — a path skipped for sitting
	// outside the project would otherwise be re-opened here, past the
	// check that just declined it.
	a.openFile(first)
	if len(rels) > opened {
		a.flash("Opened " + itoa(opened) + " of " + itoa(len(rels)) + " conflicted files")
	}
}

// gitConflictAbsPaths turns toplevel-relative paths into absolute ones
// for a git command.
//
// This is not cosmetic. git reports unmerged paths relative to the WORK
// TREE ROOT, but ced's git commands run with `-C rootDir`, and rootDir
// is whatever folder the user opened — routinely a subdirectory of the
// repo. Passing the relative path straight through would resolve it
// against the wrong base and stage nothing, or worse, stage a
// same-named file one level down. Absolute paths are what every other
// write in ced hands git (stageFilePath's contract), for this reason.
func gitConflictAbsPaths(root string, rels []string) []string {
	out := make([]string, 0, len(rels))
	for _, rel := range rels {
		out = append(out, filepath.Join(root, rel))
	}
	return out
}

// gitConflictStage marks resolved files as such. Plain `git add` — the
// verb git itself documents for "I have settled this conflict".
func (a *App) gitConflictStage(root string, rels []string) {
	if len(rels) == 0 || !a.saveConflictTabs(root, rels) {
		return
	}
	what := plural(len(rels), "file", "files")
	a.runGitCmd("Mark "+what+" resolved", gitConflictAddArgs(root, rels)...)
}

// gitConflictStageAndContinue is the happy path: stage everything, then
// finish the operation, as ONE sequence so the continue sees the staging
// (two runGitCmds would race) and one flash reports one gesture.
//
// Both outcomes are claimed. A continue in a multi-commit cherry-pick
// applies the NEXT commit, which can stop on conflicts of its own — that
// is the conflict hook's case, not an error modal's — and a continue that
// finishes the operation closes the Conflicts panel (gitConflictAfterStep).
func (a *App) gitConflictStageAndContinue(op, root string, rels []string) {
	if !a.saveConflictTabs(root, rels) {
		return
	}
	a.runGitCmdSeqHook("Continue "+op, gitNoEditorEnv(), gitConflictFailHook,
		gitConflictAfterStep(op), [][]string{
			gitConflictAddArgs(root, rels),
			gitConflictContinueArgs(op),
		})
}

// saveConflictTabs saves any DIRTY open tab among the files about to be
// staged, reporting false (with saveTabAt's own flash) when a save fails.
// `git add` reads the disk; staging the file while its resolution sits
// unsaved in a buffer would commit the version the user already fixed.
func (a *App) saveConflictTabs(root string, rels []string) bool {
	for _, rel := range rels {
		t := a.tabForPath(filepath.Join(root, rel))
		if t == nil || !t.Dirty {
			continue
		}
		// Not saveTabAt: format-on-save would race the add (conflictpanel.go's
		// saveForStaging says how).
		if !a.saveForStaging(t) {
			return false
		}
	}
	return true
}

// gitConflictAddArgs is the argv that marks files resolved — split out
// for the pushArgs rule, and because the `--` fence is what keeps a path
// beginning with a dash a path.
func gitConflictAddArgs(root string, rels []string) []string {
	return append([]string{"add", "--"}, gitConflictAbsPaths(root, rels)...)
}

// gitConflictContinue resumes an operation whose index is already clean.
func (a *App) gitConflictContinue(op string) {
	a.runGitCmdFull("Continue "+op, gitNoEditorEnv(), gitConflictFailHook,
		gitConflictAfterStep(op), nil, gitConflictContinueArgs(op)...)
}

// gitConflictSkip drops the stopped commit and moves on to the next one
// (cherry-pick, revert, rebase). The caller confirms first — skip is the
// verb that quietly loses a commit. The next commit can stop on its own
// conflicts, so the hooks are continue's.
func (a *App) gitConflictSkip(op string) {
	a.runGitCmdFull("Skip in "+op, gitNoEditorEnv(), gitConflictFailHook,
		gitConflictAfterStep(op), nil, op, "--skip")
}

// gitConflictAfterStep is the success hook for continue / skip / abort:
// once the operation is over (nothing parked, nothing unmerged — the
// done-event's refresh has just re-read both), the Conflicts panel has
// nothing left to say and is put away, and the flash says the operation
// finished rather than the generic "— done". Still parked (a skip landed
// on the next stopped commit) just refreshes the panel, which
// refreshGitStatus has already done.
func gitConflictAfterStep(op string) func(*App) {
	return func(app *App) {
		if app.gitOp != "" || len(app.gitConflicted) > 0 {
			return
		}
		if app.conflictPanel.open {
			app.hideTool(toolConflicts)
		}
		app.flash(strings.ToUpper(op[:1]) + op[1:] + " finished")
	}
}

// gitConflictContinueArgs is the argv for resuming op — split out for
// the pushArgs rule (the argv is the thing with consequences, so it is
// testable without forking git). Deliberately bare: the editor problem
// that dogs --continue is solved in the ENVIRONMENT, not here, because
// git rejects `--continue --no-edit` as conflicting options. See
// gitNoEditorEnv, which every caller of this pairs with.
func gitConflictContinueArgs(op string) []string {
	return []string{op, "--continue"}
}

// gitConflictAbort throws the operation away, behind a confirm. It is the
// one row here that destroys work: --abort restores the pre-operation
// state, taking every conflict resolution typed since with it.
func (a *App) gitConflictAbort(op string) {
	a.openConfirmLines("Abort "+op, []string{
		"Abandon the " + op + " in progress?",
		"Conflict resolutions you have made are lost.",
	}, func(app *App) {
		app.runGitCmdOK("Abort "+op, func(app *App) {
			// The abort's own flash, not "finished": nothing was applied.
			if app.gitOp == "" && app.conflictPanel.open {
				app.hideTool(toolConflicts)
			}
		}, op, "--abort")
	})
}

// -----------------------------------------------------------------------------
// Entry points
// -----------------------------------------------------------------------------

// gitConflictFailHook is what a cherry-pick / revert hands runGitCmdHook.
// It claims the failure only when the repo is actually left mid-conflict;
// every other way those commands can fail ("bad revision", a dirty tree
// refusing the operation) falls through to the usual error modal, which
// is the right surface for git's own words.
//
// The tree refresh is not decoration: the failure path used to return
// without one, so the gutters and dirty colors still described the repo
// as it was BEFORE the conflict wrote markers into the work tree.
//
// A stop with conflicts now raises the Conflicts panel and opens the
// first conflicted file at its first block (conflictpanel.go's
// showConflictStop) instead of the picker: the picker was a modal in
// front of the very file the user had to go and read. The picker stays
// as the keyboard door (≡ Git "Resolve conflicts…"). A stop with NOTHING
// unmerged — a pick that came out empty, a dirty tree refusing the next
// commit — still puts the panel up (it carries Skip and Abort) but
// declines the event, so git's own explanation lands in the modal on top.
func gitConflictFailHook(app *App, _ *gitCmdDoneEvent) bool {
	if gitInProgressOp(app.rootDir) == "" {
		return false
	}
	app.refreshTreeNow()
	return app.showConflictStop()
}

// menuGitResolveConflicts is the ≡ Git row (and therefore the command
// palette's), the way back to a picker that was dismissed — or into one
// for a conflict ced never started, from a `stash pop` or a rebase run in
// the terminal beside it.
func (a *App) menuGitResolveConflicts() {
	a.closeMenu()
	a.openGitConflictPicker()
}

// hasGitConflict gates that row off the status snapshot's unmerged set —
// a field read, because enabled() runs on every menu draw.
//
// It deliberately asks about unmerged FILES rather than about an
// operation in progress: detecting the latter costs a fork, and a
// stopped-but-fully-staged operation is rare enough to reach through the
// palette. What must never happen is the row lighting up for a clean
// repo, and DirtyFiles' contract — absence means clean — gives that.
func (a *App) hasGitConflict() bool {
	return a.gitIsRepo && len(a.gitConflicted) > 0
}
