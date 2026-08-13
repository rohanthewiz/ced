// =============================================================================
// File: internal/app/gitpanelactions.go
// Author: Rohan Allison <rohanthewiz@gmail.com>
// Created: 2026-07-28
// Copyright: 2026 Rohan Allison. All rights reserved.
// =============================================================================

// gitpanelactions.go is the git panel's verb surface: the "Actions ▾"
// button in the panel header (and the matching ≡ menu row) gathers the
// ticked files and offers everything that can be done to them — stage,
// unstage, discard, delete, open, copy paths — plus the commit rows
// (see gitcommitmsg.go) and the two selection helpers.
//
// The split of labour with gitpanel.go: that file owns the panel's
// state, geometry, and pixels; this one owns what the checkboxes are
// FOR. The checkbox used to be a stage toggle, which capped the panel
// at exactly one verb — moving stage/unstage in here alongside the rest
// is what let it become a general multi-selection.
//
// House patterns in play:
//
//   - Picker, not a bespoke dropdown (modal.go's rule): the button opens
//     openPicker, so the action list is fuzzy-searchable, keyboard-
//     driven, and free of new hit-testing. Rows the current selection
//     can't use are omitted rather than dimmed — a palette that offers
//     "Unstage" with nothing staged just teaches the user that Enter
//     sometimes does nothing.
//   - Async git (gitcmd.go's rule): every write goes through runGitCmd,
//     so failures land in the info modal with git's own words and the
//     panel redraws off the done-event's refresh — never optimistically.
//   - Destructive verbs confirm first (fileops.go's rule), with the
//     blast radius spelled out in the body text.

package app

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/rohanthewiz/ced/internal/clipboard"
)

// -----------------------------------------------------------------------------
// Targets
// -----------------------------------------------------------------------------

// gitPanelTargets returns the files an action will operate on: every
// ticked file, or — when nothing is ticked — the highlighted row alone.
// The fallback is what keeps the button useful on the very first click;
// demanding a tick before any action would turn the common single-file
// case into two gestures. Iterating files (not the map) keeps the result
// in list order, so confirm bodies and flashes read top-down.
func (a *App) gitPanelTargets() []gitPanelFile {
	var out []gitPanelFile
	for _, f := range a.gitPanel.files {
		if a.gitPanel.checked[f.Path] {
			out = append(out, f)
		}
	}
	if len(out) > 0 {
		return out
	}
	if f, ok := a.gitPanelSelectedFile(); ok {
		return []gitPanelFile{f}
	}
	return nil
}

// gitPanelTargetLabel names a target set for a picker row, a confirm
// body, or a flash: the basename when it's a single file, a count
// otherwise. One helper so "Stage main.go" and "Discard 3 files" can't
// drift into two different phrasings.
func gitPanelTargetLabel(files []gitPanelFile) string {
	if len(files) == 1 {
		return filepath.Base(files[0].Path)
	}
	return itoa(len(files)) + " files"
}

// gitPanelPaths projects a target set onto the absolute paths git wants
// after a `--` separator.
func gitPanelPaths(files []gitPanelFile) []string {
	paths := make([]string, 0, len(files))
	for _, f := range files {
		paths = append(paths, f.Path)
	}
	return paths
}

// gitPanelFilter returns the subset of files satisfying keep — the
// shared spine of the "which rows are even offered" question.
func gitPanelFilter(files []gitPanelFile, keep func(gitPanelFile) bool) []gitPanelFile {
	var out []gitPanelFile
	for _, f := range files {
		if keep(f) {
			out = append(out, f)
		}
	}
	return out
}

// gitPanelTracked reports whether f is known to git. Untracked entries
// ("??") have no HEAD version to discard back to, so they're filtered
// out of Discard — `git checkout` on one only errors with "pathspec did
// not match", and Delete is the verb that actually applies to them.
func gitPanelTracked(f gitPanelFile) bool {
	return !strings.HasPrefix(f.Code, "?")
}

// gitPanelOnDisk reports whether f still exists on disk. Deletions
// (staged or not) stay listed in the panel — you review them like any
// other change — but there's nothing to open or copy content from, so
// the Open row skips them instead of spraying "no such file" flashes.
func gitPanelOnDisk(f gitPanelFile) bool {
	st, err := os.Stat(f.Path)
	return err == nil && !st.IsDir()
}

// -----------------------------------------------------------------------------
// The picker
// -----------------------------------------------------------------------------

// openGitPanelActions gathers the current targets and opens the action
// list. Shared by the header button and the ≡ menu row, so both surfaces
// offer exactly the same verbs against exactly the same selection.
func (a *App) openGitPanelActions() {
	items := a.gitPanelActionItems(a.gitPanelTargets())
	if len(items) == 0 {
		a.flash("No git actions available")
		return
	}
	a.openPicker("Git panel actions", items)
}

// menuGitPanelActions is the ≡ menu / command-palette entry point — the
// keyboard path to the header button, since the panel itself is
// deliberately mouse-driven and macOS Terminal can swallow clicks.
func (a *App) menuGitPanelActions() {
	a.closeMenu()
	a.openGitPanelActions()
}

// hasGitPanelOpen gates the menu row: the actions operate on the panel's
// selection, which only exists while the panel is showing.
func (a *App) hasGitPanelOpen() bool {
	return a.gitPanel.open && a.gitIsRepo
}

// gitPanelActionItems builds the picker rows for a target set. Rows are
// offered only when they'd do something: Stage needs an unstaged change
// in the set, Unstage a staged one, Discard a tracked file, Commit a
// non-empty index. Order is deliberate — the two staging verbs first
// (the panel's bread and butter), then the read-only conveniences, then
// the destructive pair, then selection management.
func (a *App) gitPanelActionItems(targets []gitPanelFile) []paletteItem {
	var items []paletteItem
	add := func(label string, run func(*App)) {
		items = append(items, paletteItem{label: label, run: run})
	}

	if len(targets) > 0 {
		what := gitPanelTargetLabel(targets)
		paths := gitPanelPaths(targets)

		if len(gitPanelFilter(targets, func(f gitPanelFile) bool {
			return gitPanelStageState(f.Code) != stageFull
		})) > 0 {
			add("Stage "+what, func(app *App) {
				app.runGitCmd("Stage "+what, append([]string{"add", "--"}, paths...)...)
			})
		}
		if len(gitPanelFilter(targets, func(f gitPanelFile) bool {
			return gitPanelStageState(f.Code) != stageNone
		})) > 0 {
			add("Unstage "+what, func(app *App) {
				app.runGitCmd("Unstage "+what, append([]string{"reset", "-q", "--"}, paths...)...)
			})
		}
		if openable := gitPanelFilter(targets, gitPanelOnDisk); len(openable) > 0 {
			add("Open "+gitPanelTargetLabel(openable), func(app *App) {
				app.gitPanelOpenFiles(openable)
			})
		}
		add("Copy path of "+what, func(app *App) { app.gitPanelCopyPaths(targets) })
		if tracked := gitPanelFilter(targets, gitPanelTracked); len(tracked) > 0 {
			add("Discard changes in "+gitPanelTargetLabel(tracked)+"…", func(app *App) {
				app.gitPanelDiscard(tracked)
			})
		}
		add("Delete "+what+" from disk…", func(app *App) { app.gitPanelDelete(targets) })
		// The nudge rides on the commit row itself, where the decision
		// is being made — "(5/7 reviewed)" is a fact about the change,
		// not a gate on it, so the row still runs. It goes quiet once
		// every file has been read (gitPanelReviewNudge).
		add("Commit "+what+"…"+a.gitPanelReviewNudge(), func(app *App) {
			app.openCommitPrompt(targets, "")
		})
	}

	// The survey and its hunk verbs — the keyboard twins of the header
	// button and the diff pane's chips, for the same reason every other
	// row here has one: the panel is mouse-driven, and macOS Terminal
	// can swallow clicks.
	if len(a.gitPanel.files) > 0 {
		if a.gitPanel.walk {
			add("Stop reviewing ("+itoa(a.gitPanelReviewedCount())+"/"+itoa(len(a.gitPanel.files))+" read)",
				(*App).stopGitPanelWalk)
		} else if a.gitPanelReviewedCount() > 0 {
			add("Resume review ("+itoa(a.gitPanelReviewedCount())+"/"+itoa(len(a.gitPanel.files))+" read)",
				(*App).startGitPanelWalk)
		} else {
			add("Review all "+itoa(len(a.gitPanel.files))+" files ▶", (*App).startGitPanelWalk)
		}
	}
	if len(a.gitPanelHunkItems()) > 0 {
		add("Hunk actions…", (*App).openGitPanelHunks)
	}

	// The suggestion row sits next to the commit rows because that's
	// the sequence it belongs to — draft, then edit, then commit. It
	// targets the ticked set when there is one and the index otherwise,
	// so it always describes the same change the neighbouring Commit
	// row would make.
	if a.canSuggestCommitMsg() && (len(targets) > 0 || a.gitHasStaged) {
		suggestFor := targets
		add("Suggest commit message for "+gitCommitLabel(suggestFor)+" ("+a.chatAgent().name+")", func(app *App) {
			app.gitPanelSuggestCommit(suggestFor)
		})
	}
	if a.gitHasStaged {
		add("Commit staged…"+a.gitPanelReviewNudge(), (*App).menuGitCommit)
	}
	// Push closes the sequence the commit rows above start. It names the
	// branch rather than saying a bare "Push" because this picker is
	// fuzzy-searchable: typing the branch name should find the row that
	// sends it.
	if a.hasGitPushTarget() && a.gitBranch != "" {
		add("Push "+a.gitBranch+"…", (*App).openGitPush)
	}
	if n := a.gitPanelCheckedCount(); n < len(a.gitPanel.files) {
		add("Select all ("+itoa(len(a.gitPanel.files))+")", (*App).gitPanelSelectAll)
	}
	if n := a.gitPanelCheckedCount(); n > 0 {
		add("Clear selection ("+itoa(n)+")", (*App).gitPanelClearChecked)
	}
	return items
}

// -----------------------------------------------------------------------------
// The verbs
// -----------------------------------------------------------------------------

// gitPanelOpenFiles opens each target as a tab, leaving the last one
// active — the same end state as clicking them one by one in the tree,
// which is what "open these" should feel like.
func (a *App) gitPanelOpenFiles(files []gitPanelFile) {
	for _, f := range files {
		a.openFile(f.Path)
	}
}

// gitPanelCopyPaths puts the targets' project-relative paths on the host
// clipboard via OSC 52, one per line — the shape you paste straight into
// a shell (`go test ./…`, `git add …`). Relative, not absolute, because
// the panel already speaks in project-relative terms and that's the form
// that survives being pasted on the other end of an SSH session.
func (a *App) gitPanelCopyPaths(files []gitPanelFile) {
	if len(files) == 0 {
		return
	}
	rels := make([]string, 0, len(files))
	for _, f := range files {
		rels = append(rels, f.Rel)
	}
	if err := clipboard.CopyToSystem(strings.Join(rels, "\n")); err != nil {
		a.flash(fmt.Sprintf("Copy failed: %v", err))
		return
	}
	if len(files) == 1 {
		a.flash("Copied path: " + rels[0])
		return
	}
	a.flash(fmt.Sprintf("Copied %d paths", len(files)))
}

// gitPanelDiscard confirms, then resets the targets to HEAD
// (`git checkout HEAD --`), which clears the work-tree edit AND any
// staged version of it — "make this file look like the last commit",
// which is what a reviewer means by discard. Callers pass the tracked
// subset only. The work is unrecoverable, so the confirm body says so
// and openConfirm's focus starts on No.
func (a *App) gitPanelDiscard(files []gitPanelFile) {
	if len(files) == 0 {
		return
	}
	what := gitPanelTargetLabel(files)
	paths := gitPanelPaths(files)
	a.openConfirm(
		"Discard changes",
		"Reset "+what+" to HEAD? Uncommitted work is lost.",
		func(app *App) {
			app.runGitCmd("Discard "+what, append([]string{"checkout", "HEAD", "--"}, paths...)...)
		},
	)
}

// gitPanelDelete confirms, then removes the targets from disk. Plain
// os.Remove rather than `git rm`: the set can mix tracked and untracked
// files and git rm refuses the latter, whereas a disk delete is uniform
// — tracked entries simply reappear in the panel as deletions, which is
// exactly the reviewable state.
func (a *App) gitPanelDelete(files []gitPanelFile) {
	if len(files) == 0 {
		return
	}
	what := gitPanelTargetLabel(files)
	paths := gitPanelPaths(files)
	a.openConfirm(
		"Delete from disk",
		"Permanently delete "+what+"?",
		func(app *App) { app.gitPanelDoDelete(paths, what) },
	)
}

// gitPanelDoDelete removes each path, closing any tab it backed, then
// refreshes the workspace once. A failure mid-run does NOT abort the
// rest: the user asked for the whole set, and stopping halfway leaves a
// state that's harder to reason about than "these N went, that one
// didn't". The first error is reported in the flash.
func (a *App) gitPanelDoDelete(paths []string, what string) {
	deleted, failed := 0, 0
	var firstErr error
	for _, p := range paths {
		if err := deletePath(p); err != nil {
			if firstErr == nil {
				firstErr = err
			}
			failed++
			continue
		}
		deleted++
		for i := len(a.tabs) - 1; i >= 0; i-- {
			if tabPathRemoved(a.tabs[i].Path, p) {
				a.closeTab(i)
			}
		}
	}
	a.workspaceChanged()
	if failed > 0 {
		a.flash(fmt.Sprintf("Deleted %d, %d failed: %v", deleted, failed, firstErr))
		return
	}
	a.flash("Deleted " + what)
}
