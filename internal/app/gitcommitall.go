// =============================================================================
// File: internal/app/gitcommitall.go
// Author: Rohan Allison <rohanthewiz@gmail.com>
// Created: 2026-11-16
// Copyright: 2026 Rohan Allison. All rights reserved.
// =============================================================================

// gitcommitall.go is "commit everything I changed" as ONE gesture:
// Esc-G, the ≡ Git row "Commit all changes…", or the palette.
//
// Before it, committing a working session was three surfaces deep — open
// the git panel, tick every row (or stage file by file), then pick the
// commit row — while the commonest commit of all is simply "everything
// in this project, with this message".
//
// The gesture, in order:
//
//	Esc-G ──► refuse? (not a repo · an operation is parked)
//	      ──► save every unsaved tab in the project   (saveTabAt each)
//	      ──► list the changes now on disk            (one git status fork)
//	      ──► nothing?  flash "Nothing to commit"
//	      ──► the ordinary commit prompt, scope = all (✦ draft included)
//	Enter ──► gitCommitAll: add -A + commit, both limited to the folder
//
// Design decisions:
//
//   - The prompt is the confirmation. Nothing is committed without a
//     message typed (or an agent draft accepted) and Enter — Esc at the
//     prompt backs out with the repository untouched. That is why a
//     leader key may open it at all: the leader table refuses verbs that
//     do damage in one keystroke, and this one does not.
//   - Unsaved tabs are SAVED FIRST, because "all changes" read from the
//     editor means what the user is looking at. A commit that silently
//     left out the last edits — the ones auto-save had not reached yet —
//     would record something other than what was on screen. The saves
//     are the plain explicit save (format-on-save included) and happen
//     when the prompt OPENS, not on Enter: a Go file's formatter then
//     has the whole message-typing time to land before `git add` runs,
//     and a save the guard refuses (the file changed on disk) stops the
//     gesture before the user has typed a message they would lose.
//   - The change list is read FRESH (loadGitStatusFiles, one fork, the
//     Switch branch picker's budget) rather than from the 10s snapshot:
//     the saves above just changed what git would say, and in a cats
//     pane beside an agent the snapshot can be seconds behind the files.
//     The count it gives is for the title and for the ✦ draft's diff;
//     the commit itself is pathspec'd by the FOLDER (gitCommitAll says
//     why).
//   - Refused while a merge / rebase / cherry-pick / revert is parked:
//     a pathspec'd commit is refused by git mid-merge, and a fresh commit
//     in the middle of a rebase stop is not what "commit my changes"
//     means there. The Conflicts panel's Continue is, so it is raised
//     with the refusal (the cherry-pick dialog's rule).
//   - No ⌘ chord. The two the ecosystem spells — ⌘K (JetBrains) is cats'
//     palette (metaReserved), ⌘Enter (VS Code's SCM box) is not a rune
//     chord the table can hold — and the ⌘ layer only binds chords hands
//     already know.

package app

// menuGitCommitAll is the verb behind Esc-G and the ≡ Git row. It
// gathers and saves, then opens the commit prompt in "all" scope; the
// commit runs only on the prompt's Enter.
func (a *App) menuGitCommitAll() {
	a.closeMenu()
	if !a.gitIsRepo || a.rootDir == "" {
		a.flash("Not a git repository — nothing to commit")
		return
	}
	if op := gitInProgressOp(a.rootDir); op != "" {
		a.flash("A " + op + " is in progress — finish or abort it first")
		a.showTool(toolConflicts)
		return
	}
	saved, ok := a.saveTabsForCommitAll()
	if !ok {
		return // saveTabAt / the save guard already said why
	}
	files := loadGitStatusFiles(a.rootDir)
	if len(files) == 0 {
		a.flash("Nothing to commit")
		return
	}
	if saved > 1 {
		// saveTabAt flashes each file by name, so one save already reads
		// right; several would show only the last name.
		a.flash("Saved " + plural(saved, "tab", "tabs") + " for the commit")
	}
	a.openCommitPromptWith(commitScope{files: files, all: true}, "", false, a.commitTrailer)
}

// saveTabsForCommitAll saves every dirty tab whose file lies inside the
// project folder, and reports how many it wrote and whether the gesture
// may go on. Tabs outside the folder are left alone: the commit is
// pathspec'd by the folder, so saving them would write a file for no
// commit. Untitled tabs have nowhere to go and nothing to commit.
//
// The guard is asked HERE first, with the whole gesture as its resume
// (menuSaveAndClose's rule): when a file changed on disk underneath its
// tab, the user's "Keep mine" carries on into the commit prompt they
// asked for, instead of leaving them with one saved file and nothing
// else. saveTabAt then asks again — answered from the same stamp, so the
// second ask is a stat, not a second prompt.
//
// The first failure stops the walk (saveAllDirty's reasoning): the user
// has to answer that one before anything should be committed.
func (a *App) saveTabsForCommitAll() (saved int, ok bool) {
	for i, t := range a.tabs {
		if !t.Dirty || t.Path == "" || !pathInside(t.Path, a.rootDir) {
			continue
		}
		if !a.saveGuard(t, (*App).menuGitCommitAll) {
			return saved, false
		}
		if !a.saveTabAt(i) {
			return saved, false
		}
		saved++
	}
	return saved, true
}
