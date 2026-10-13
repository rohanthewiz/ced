// =============================================================================
// File: internal/app/gitlogticks.go
// Author: Rohan Allison <rohanthewiz@gmail.com>
// Created: 2026-10-11
// Copyright: 2026 Rohan Allison. All rights reserved.
// =============================================================================

// gitlogticks.go is the git log panel's multi-commit selection: tick
// several commits, then cherry-pick them onto the current branch in ONE
// `git cherry-pick`. It is the log-side twin of the cherry-pick dialog
// (cherrypick.go), which only lists one branch's commits against HEAD;
// the log lists every ref (--all), so a set can mix branches.
//
// House patterns in play:
//
//   - **The tree marks' tick, one cell wide** (treemarks.go): the tick
//     borrows the blank leading cell every commit row already has, and
//     the click zone is that one column. Clicking it toggles the tick and
//     leaves the highlight alone.
//   - **Every surface has a keyboard twin**: the Actions ▾ picker (and so
//     the ≡ Git row and the palette) carries "Tick <hash>" / "Untick
//     <hash>" for the highlighted commit, and the set's own rows.
//   - **Ticks are keyed by hash and hold their row**, so a search that
//     hides a ticked commit does not drop it, and the confirm can still
//     name it. That is why the set is never pruned against the visible
//     list: under a filter, the visible list is a search result.
//   - **Oldest first, one command** — the cherry-pick dialog's rule. The
//     order is asked of git (`rev-list --topo-order`, which never lists
//     a parent before its child), not taken from tick order or list
//     position: a set ticked newest first, or partly hidden by a search,
//     must still apply in history order.
//   - **Confirm, listing the set** (treemarks.go's blast-radius rule):
//     ticked rows may be scrolled off or filtered away, so the confirm
//     spells them out, capped and announced.
//   - **Refused while an operation is parked** (menuCherryPickFromBranch's
//     rule): a second cherry-pick cannot start until the first is
//     settled, and the Conflicts panel is put up with the refusal.
//   - **A merge in the set adds `-m 1` to the whole command** (N-064,
//     the dialog's rule via cherryPickArgs' mainline): git refuses a
//     merge without it, and since git 2.21 the ordinary commits beside it
//     are picked unchanged under it. A merge-free set keeps its old argv;
//     a set holding one says so in the confirm.

package app

import (
	"bufio"
	"fmt"
	"os/exec"
	"strings"
)

// gitLogTickGlyph is the tick a ticked commit row wears in its leading
// cell — the tree marks' glyph, so the two multi-selections read alike.
const gitLogTickGlyph = '✓'

// gitLogTickConfirmList is how many commits the confirm names before
// "…and N more" — treeMarkConfirmList's number, for its reason.
const gitLogTickConfirmList = treeMarkConfirmList

// gitLogToggleTick ticks c, or unticks it if already ticked.
func (a *App) gitLogToggleTick(c gitLogCommit) {
	if _, ok := a.gitLog.ticked[c.Hash]; ok {
		delete(a.gitLog.ticked, c.Hash)
		return
	}
	if a.gitLog.ticked == nil {
		a.gitLog.ticked = make(map[string]gitLogCommit)
	}
	a.gitLog.ticked[c.Hash] = c
}

// gitLogTickPress handles a press on commit idx's tick cell. ok=false
// when idx is not a commit row, so the caller can treat the press as an
// ordinary row click.
func (a *App) gitLogTickPress(idx int) bool {
	if idx < 0 || idx >= len(a.gitLog.commits) {
		return false
	}
	a.gitLogToggleTick(a.gitLog.commits[idx])
	return true
}

// gitLogClearTicks empties the set.
func (a *App) gitLogClearTicks() {
	a.gitLog.ticked = nil
}

// gitLogTickItem is the per-commit picker row: the keyboard door to the
// tick the leading cell toggles by mouse.
func (a *App) gitLogTickItem(c gitLogCommit) paletteItem {
	label := "Tick " + c.Short + " for a multi-commit cherry-pick"
	if _, ok := a.gitLog.ticked[c.Hash]; ok {
		label = "Untick " + c.Short
	}
	return paletteItem{label: label, run: func(app *App) {
		app.gitLogToggleTick(c)
		n := len(app.gitLog.ticked)
		app.flash(plural(n, "commit", "commits") + " ticked")
	}}
}

// gitLogTickedItems is the set's rows, leading the Actions picker while
// anything is ticked: pick them all, or clear the set. Empty with no
// ticks — rows that would no-op are omitted, not dimmed.
func (a *App) gitLogTickedItems(branch string) []paletteItem {
	n := len(a.gitLog.ticked)
	if n == 0 {
		return nil
	}
	return []paletteItem{
		{label: "Cherry-pick " + plural(n, "ticked commit", "ticked commits") + " onto " + branch + "…",
			run: func(app *App) { app.confirmGitLogCherryPickTicked() }},
		{label: "Clear ticks (" + itoa(n) + ")", run: func(app *App) {
			app.gitLogClearTicks()
			app.flash("Ticks cleared")
		}},
	}
}

// confirmGitLogCherryPickTicked orders the set oldest first, confirms
// with the list, and runs one `git cherry-pick` over it with the conflict
// hook armed. The ticks are cleared only when git succeeds — a conflict
// stop leaves them, so the user can see what the sequence was.
func (a *App) confirmGitLogCherryPickTicked() {
	if op := gitInProgressOp(a.rootDir); op != "" {
		a.flash("A " + op + " is in progress — finish or abort it first")
		a.showTool(toolConflicts)
		return
	}
	commits, err := a.gitLogTickedOldestFirst()
	if err != nil {
		a.flash("Can't order the ticked commits: " + err.Error())
		return
	}
	if len(commits) == 0 {
		a.flash("No commits ticked")
		return
	}
	branch := a.gitLogBranchLabel()
	n := len(commits)
	lines := make([]string, 0, gitLogTickConfirmList+2)
	for i, c := range commits {
		if i == gitLogTickConfirmList && n > gitLogTickConfirmList+1 {
			lines = append(lines, fmt.Sprintf("…and %d more", n-i))
			break
		}
		lines = append(lines, c.Short+" "+elide(c.Subject, gitLogSubjectMax))
	}
	hashes := make([]string, n)
	merge := false
	for i, c := range commits {
		hashes[i] = c.Hash
		merge = merge || c.Merge
	}
	if merge {
		lines = append(lines, gitLogMergeNote("Cherry-pick"))
	}
	lines = append(lines, "onto "+branch+", oldest first?")
	label := "Cherry-pick " + plural(n, "commit", "commits")
	a.openConfirmLines(label, lines, func(app *App) {
		app.runGitCmdFull(label, gitNoEditorEnv(), gitConflictFailHook, func(app *App) {
			app.gitLogClearTicks()
			app.flash("Cherry-picked " + plural(n, "commit", "commits") + " onto " + branch)
		}, nil, cherryPickArgs(hashes, false, false, merge)...)
	})
}

// gitLogTickedOldestFirst returns the ticked commits in the order git
// should apply them: every commit after its ancestors, oldest first.
//
// The order comes from `git rev-list --topo-order` over the ticked hashes,
// which never lists a parent before its child. A plain walk does NOT
// promise that here — every ticked hash is a start point, so a parent
// sits in the date-ordered queue from the beginning and wins a tie with
// a same-second child (TestGitLogTicks_CherryPickEndToEnd caught exactly
// that) — and neither does a date sort (`log --no-walk=sorted`).
// The walk is streamed and stopped as soon as every ticked hash has been
// seen. --topo-order may have to read the history up front where the
// repository has no commit-graph; on an explicit gesture, once, that is
// an acceptable price for never applying a child before its parent.
//
// A ticked hash git no longer knows — rewritten away by a rebase since it
// was ticked — makes rev-list fail, which is reported rather than silently
// dropping the commit from the set.
func (a *App) gitLogTickedOldestFirst() ([]gitLogCommit, error) {
	if len(a.gitLog.ticked) == 0 {
		return nil, nil
	}
	args := []string{"-C", a.rootDir, "rev-list", "--topo-order"}
	for h := range a.gitLog.ticked {
		args = append(args, h)
	}
	cmd := exec.Command("git", args...)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("git not available")
	}
	var found []string
	seen := make(map[string]bool, len(a.gitLog.ticked))
	sc := bufio.NewScanner(stdout)
	for sc.Scan() {
		h := strings.TrimSpace(sc.Text())
		if _, ok := a.gitLog.ticked[h]; ok && !seen[h] {
			seen[h] = true
			found = append(found, h)
			if len(found) == len(a.gitLog.ticked) {
				break
			}
		}
	}
	// Stop the walk early when everything was found; Wait then reports
	// the kill, which is not a failure here.
	complete := len(found) == len(a.gitLog.ticked)
	if complete {
		_ = cmd.Process.Kill()
	}
	waitErr := cmd.Wait()
	if !complete && waitErr != nil {
		return nil, fmt.Errorf("git rev-list failed (was a ticked commit rewritten?)")
	}
	return orderTickedCommits(found, a.gitLog.ticked)
}

// orderTickedCommits maps the walk's ticked hashes (newest first) onto the
// ticked rows, reversed to oldest first. Every ticked hash must come back
// exactly once; anything else is an error, because cherry-picking a set
// with a member silently missing is worse than not starting.
func orderTickedCommits(newestFirst []string, ticked map[string]gitLogCommit) ([]gitLogCommit, error) {
	if len(newestFirst) != len(ticked) {
		return nil, fmt.Errorf("git found %d of %d ticked commits", len(newestFirst), len(ticked))
	}
	commits := make([]gitLogCommit, len(newestFirst))
	for i, h := range newestFirst {
		c, ok := ticked[h]
		if !ok {
			return nil, fmt.Errorf("git listed an unticked commit %.7s", h)
		}
		commits[len(newestFirst)-1-i] = c
	}
	return commits, nil
}
