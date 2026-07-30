// =============================================================================
// File: internal/app/gitlogactions.go
// Author: Rohan Allison <rohanthewiz@gmail.com>
// Created: 2026-07-29
// Copyright: 2026 Rohan Allison. All rights reserved.
// =============================================================================

// gitlogactions.go is the git log panel's verb surface: the "Actions ▾"
// header button (and its ≡ menu twin) offers everything the selected
// commit can be subjected to — cherry-pick, revert, reset (soft / mixed
// / hard via a second picker), detached checkout, branch and tag
// creation, and the two copies. The split of labour mirrors the changes
// panel's pair: gitlog.go owns state, geometry, and pixels; this file
// owns what selecting a commit is FOR.
//
// House patterns in play:
//
//   - Picker, not a bespoke dropdown (modal.go's rule): openPicker for
//     the verb list AND for the reset-mode choice — nested pickers are
//     fine because the first closes before the second opens.
//   - Async git (gitcmd.go's rule): every write goes through runGitCmd,
//     so cherry-pick conflicts, revert refusals on merges, and reset
//     errors all land in the info modal with git's own words, and the
//     log refreshes off the done-event's pipeline — never optimistically.
//   - Destructive verbs confirm first: reset --hard is the one verb
//     here that throws away uncommitted work, so it alone confirms.
//     Cherry-pick and revert CREATE commits — recoverable, no gate.

package app

import (
	"fmt"
	"os/exec"
	"strings"

	"github.com/rohanthewiz/ced/internal/clipboard"
)

// openGitLogActions opens the verb picker for the selected commit.
// Shared by the header button and the ≡ menu row, so both surfaces
// offer exactly the same verbs against exactly the same commit.
func (a *App) openGitLogActions() {
	c, ok := a.gitLogSelectedCommit()
	if !ok {
		a.flash("No commit selected")
		return
	}
	a.openPicker("Commit "+c.Short, a.gitLogActionItems(c))
}

// menuGitLogActions is the ≡ menu / command-palette entry point — the
// keyboard path to the header button, since macOS Terminal can swallow
// clicks (the same rationale as the changes panel's twin row).
func (a *App) menuGitLogActions() {
	a.closeMenu()
	a.openGitLogActions()
}

// hasGitLogOpen gates the menu row: the verbs operate on the panel's
// selection, which only exists while the panel is showing.
func (a *App) hasGitLogOpen() bool {
	return a.gitLog.open && a.gitIsRepo
}

// gitLogActionItems builds the picker rows for one commit. Order is
// deliberate: the two "apply this commit elsewhere" verbs first (the
// log's bread and butter), then the branch-motion verbs, then creation,
// then the read-only copies.
func (a *App) gitLogActionItems(c gitLogCommit) []paletteItem {
	branch := a.gitLogBranchLabel()
	return []paletteItem{
		{label: "Cherry-pick " + c.Short + " onto " + branch, run: func(app *App) {
			app.runGitCmd("Cherry-pick "+c.Short, "cherry-pick", c.Hash)
		}},
		{label: "Revert " + c.Short, run: func(app *App) {
			app.runGitCmd("Revert "+c.Short, "revert", "--no-edit", c.Hash)
		}},
		{label: "Reset " + branch + " to " + c.Short + "…", run: func(app *App) {
			app.openGitLogResetPicker(c)
		}},
		{label: "Checkout " + c.Short + " (detached HEAD)", run: func(app *App) {
			app.runGitCmd("Checkout "+c.Short, "switch", "--detach", c.Hash)
		}},
		{label: "Create branch at " + c.Short + "…", run: func(app *App) {
			app.openPrompt("Create branch at "+c.Short, "branch name", "", func(app *App, name string) {
				app.runGitCmd("Create branch "+name, "branch", name, c.Hash)
			})
		}},
		{label: "Create tag at " + c.Short + "…", run: func(app *App) {
			app.openPrompt("Create tag at "+c.Short, "tag name", "", func(app *App, name string) {
				app.runGitCmd("Tag "+name, "tag", name, c.Hash)
			})
		}},
		{label: "Copy hash", run: func(app *App) { app.gitLogCopyHash(c) }},
		{label: "Copy message", run: func(app *App) { app.gitLogCopyMessage(c) }},
	}
}

// gitLogBranchLabel names the ref that reset / cherry-pick will move —
// the current branch, or "HEAD" when detached. Spelled into the labels
// so the picker says exactly what a verb is about to touch.
func (a *App) gitLogBranchLabel() string {
	if a.gitBranch == "" {
		return "HEAD"
	}
	return a.gitBranch
}

// openGitLogResetPicker offers the three reset modes for c as a second
// picker — the choose-one-from-a-list house rule again, standing in for
// JetBrains' mode radio buttons. Soft and mixed only move refs (a
// reflog entry away from undone), so they run directly; hard discards
// uncommitted work, so it alone routes through a confirm whose body
// spells out the blast radius.
func (a *App) openGitLogResetPicker(c gitLogCommit) {
	branch := a.gitLogBranchLabel()
	a.openPicker("Reset "+branch+" to "+c.Short, []paletteItem{
		{label: "Soft — keep index and files", run: func(app *App) {
			app.runGitCmd("Reset (soft) to "+c.Short, "reset", "--soft", c.Hash)
		}},
		{label: "Mixed — unstage, keep files", run: func(app *App) {
			app.runGitCmd("Reset (mixed) to "+c.Short, "reset", "--mixed", c.Hash)
		}},
		{label: "Hard — discard uncommitted changes…", run: func(app *App) {
			app.openConfirm(
				"Reset --hard",
				"Reset "+branch+" to "+c.Short+" and discard ALL uncommitted changes? This cannot be undone.",
				func(app *App) {
					app.runGitCmd("Reset (hard) to "+c.Short, "reset", "--hard", c.Hash)
				},
			)
		}},
	})
}

// gitLogCopyHash puts the commit's FULL hash on the host clipboard via
// OSC 52 — the full form because that's what scripts and `git` itself
// want pasted; the flash echoes the short form the user recognizes.
func (a *App) gitLogCopyHash(c gitLogCommit) {
	if err := clipboard.CopyToSystem(c.Hash); err != nil {
		a.flash(fmt.Sprintf("Copy failed: %v", err))
		return
	}
	a.flash("Copied hash " + c.Short)
}

// gitLogCopyMessage copies the commit's full message body (subject +
// description), fetched inline — one fork on an explicit user gesture,
// the same budget as the branch pickers. The list only carries the
// subject, and copying half a message would be worse than the fork.
func (a *App) gitLogCopyMessage(c gitLogCommit) {
	out, err := exec.Command("git", "-C", a.rootDir, "show", "-s", "--format=%B", c.Hash).Output()
	if err != nil {
		a.flash("Copy failed: cannot read " + c.Short)
		return
	}
	if err := clipboard.CopyToSystem(strings.TrimRight(string(out), "\n")); err != nil {
		a.flash(fmt.Sprintf("Copy failed: %v", err))
		return
	}
	a.flash("Copied message of " + c.Short)
}
