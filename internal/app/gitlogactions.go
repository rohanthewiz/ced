// =============================================================================
// File: internal/app/gitlogactions.go
// Author: Rohan Allison <rohanthewiz@gmail.com>
// Created: 2026-07-29
// Copyright: 2026 Rohan Allison. All rights reserved.
// =============================================================================

// gitlogactions.go is the git log panel's verb surface: the "Actions ▾"
// header button, its ≡ menu twin, and a right-click on any commit row
// offer everything the selected commit can be subjected to —
// cherry-pick, revert, reset (soft / mixed / hard via a second picker),
// detached checkout, branch and tag creation, and the two copies. The
// split of labour mirrors the changes panel's pair: gitlog.go owns
// state, geometry, and pixels; this file owns what selecting a commit
// is FOR.
//
// House patterns in play:
//
//   - Picker, not a bespoke dropdown (modal.go's rule): openPicker for
//     the verb list AND for the reset-mode choice — nested pickers are
//     fine because the first closes before the second opens. The
//     right-click surface is the SAME list through the anchored chassis
//     (editorContextModal), built by the same function, so the two doors
//     cannot come to offer different verbs.
//   - Async git (gitcmd.go's rule): every write goes through runGitCmd,
//     so revert refusals on merges and reset errors land in the info
//     modal with git's own words, and the log refreshes off the
//     done-event's pipeline — never optimistically. The one failure with
//     a better answer than a modal is a conflict, which cherry-pick and
//     revert route to the picker in gitconflict.go.
//   - Destructive verbs confirm first. Reset --hard throws away
//     uncommitted work. Cherry-pick and revert are recoverable in the
//     sense that they only ADD commits — but they add them to the branch
//     you are standing on, which is not always the branch you think, and
//     a mis-aimed one can park the whole repo in a conflict. So they
//     confirm too, and their confirms spell out the two facts that
//     decide the answer: which commit, and onto what.

package app

import (
	"fmt"
	"os/exec"
	"strings"

	"github.com/rohanthewiz/ced/internal/clipboard"
)

// gitLogSubjectMax bounds a commit subject quoted into a confirm dialog.
// The confirm body is 50 cells wide and the surrounding words claim
// some; 40 leaves the quoted subject recognisable without pushing the
// quote marks off the end. Longer subjects are elided, which SAYS they
// were cut — the dialog's job is to be recognised, not to be complete.
const gitLogSubjectMax = 40

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
		{label: "Cherry-pick " + c.Short + " onto " + branch + "…", run: func(app *App) {
			app.confirmGitLogApply(c, "Cherry-pick", "onto "+branch,
				"cherry-pick", c.Hash)
		}},
		{label: "Revert " + c.Short + "…", run: func(app *App) {
			app.confirmGitLogApply(c, "Revert", "on "+branch+" — a new commit undoing it",
				"revert", "--no-edit", c.Hash)
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

// confirmGitLogApply is the gate in front of cherry-pick and revert: a
// confirm naming the commit (hash AND subject — a seven-character hash
// is not something anyone recognises) and where the new commit will
// land, then the command itself with the conflict hook armed.
//
// Both verbs share it because both answer the same question badly when
// answered wrong: they write a commit onto the CURRENT branch, and the
// log panel lists commits from --all, so the row under the pointer is
// routinely from somewhere else. "Cherry-pick this" is a sentence with a
// hidden second half, and this dialog is where it gets said out loud.
func (a *App) confirmGitLogApply(c gitLogCommit, verb, where string, args ...string) {
	a.openConfirmLines(verb+" "+c.Short, []string{
		"“" + elide(c.Subject, gitLogSubjectMax) + "”",
		where + "?",
	}, func(app *App) {
		app.runGitCmdHook(verb+" "+c.Short, gitConflictFailHook, args...)
	})
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
			// Two lines because one did not fit: this body used to be a
			// single 80-character sentence in a 50-cell slot, so the
			// dialog guarding the most destructive verb in the panel was
			// the one being cut off mid-warning.
			app.openConfirmLines("Reset --hard", []string{
				"Reset " + branch + " to " + c.Short + "?",
				"ALL uncommitted changes are discarded — no undo.",
			}, func(app *App) {
				app.runGitCmd("Reset (hard) to "+c.Short, "reset", "--hard", c.Hash)
			})
		}},
	})
}

// -----------------------------------------------------------------------------
// Right-click: the same verbs, at the pointer
// -----------------------------------------------------------------------------

// tryGitLogContextClick opens the commit verbs AT THE POINTER, reporting
// whether it consumed the event (the tree / problems / editor menus'
// contract).
//
// It reuses editorContextModal rather than the fuzzy picker for the same
// reason the problems panel does: a right-click has already named its
// subject by where it landed, so the picker's query field would be a
// text box asking a question the gesture just answered. The picker
// remains the Tier-0 fallback under "Actions ▾" and the ≡ row, because
// macOS Terminal and tmux both eat Button3 and a verb reachable only by
// right-click is a verb some users do not have.
//
// A right-click on a commit row SELECTS it first — right-clicking row
// three and acting on row one is the bug every context menu must not
// have. In the detail pane there is nothing to re-aim at (the pane
// belongs to the selection), so the menu opens against the selection as
// it stands.
func (a *App) tryGitLogContextClick(x, y int) bool {
	if !a.gitLog.open || !a.gitLogContains(x, y) {
		return false
	}
	// The header and the search bar are swallowed rather than escalated
	// to the ≡ menu: they are inside the panel, so falling through would
	// answer a click on the log with a menu about the editor.
	row := y - a.gitLogBodyTop()
	if row < 0 {
		return true
	}
	px, _, pw, _ := a.gitLogRect()
	if x < px+a.gitLogListW(pw) {
		a.gitLogSelect(a.gitLog.listScroll + row)
	}
	c, ok := a.gitLogSelectedCommit()
	if !ok {
		a.flash("No commit selected")
		return true
	}

	items := contextItemsFromPalette(a.gitLogActionItems(c))
	w := contextMenuWidth
	for _, it := range items {
		if lw := runeLen(it.label) + 6; lw > w { // border + chevron + padding
			w = lw
		}
	}
	if w > a.width {
		w = a.width
	}
	cx, cy := a.placeContextSized(x, y, len(items), w)
	a.openModal(&editorContextModal{x: cx, y: cy, w: w, items: items})
	return true
}

// contextItemsFromPalette adapts picker rows to the anchored menu's row
// type. The adapter exists so a verb list is written ONCE and can be
// shown through either chassis; every row comes across enabled, since a
// picker has no way to express a disabled row and so never holds one.
func contextItemsFromPalette(items []paletteItem) []editorContextItem {
	out := make([]editorContextItem, 0, len(items))
	for _, it := range items {
		out = append(out, editorContextItem{
			label: it.label, action: it.run, enabled: alwaysTrue,
		})
	}
	return out
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
