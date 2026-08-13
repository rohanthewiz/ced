// =============================================================================
// File: internal/app/gitpanelwalk.go
// Author: Rohan Allison <rohanthewiz@gmail.com>
// Created: 2026-08-12
// Copyright: 2026 Rohan Allison. All rights reserved.
// =============================================================================

// gitpanelwalk.go is the pre-commit SURVEY: one gesture that walks
// every changed file's diff in order, marks each one reviewed as it
// goes, and ends on the commit-message field. The panel already had all
// the parts — a file list, a diff pane, a commit prompt — and no thread
// running through them; this is that thread.
//
// The design in one paragraph. Walk mode is not a second selection: it
// steps the panel's EXISTING selection, so "the file being reviewed" and
// "the file whose diff is on screen" are the same variable and cannot
// disagree. What walk mode adds is the keyboard — while it is on, the
// panel owns n / p / space / Enter / q the way the terminal and chat
// panels own theirs — and the rule that leaving a file marks it
// reviewed. Reviewed marks are keyed by path and OUTLIVE the walk, so
// the survey can be interrupted (click into the editor, fix the thing
// you spotted) and resumed from the header button.
//
// Reviewed is deliberately NOT the same set as the checkboxes:
//
//	[ ] / [x]   selection — what the Actions button operates on
//	✓           reviewed  — what you have actually read
//
// They answer different questions, and collapsing them would mean
// reading a file implied acting on it. The commit row shows
// "(5/7 reviewed)" as a NUDGE — it never blocks the commit, because the
// editor does not get to decide when someone has looked hard enough.
//
// House patterns in play:
//
//   - Mouse-first focus (app.go's rule): a press outside the panel ends
//     the walk and hands the keyboard back, exactly as it drops the
//     terminal's and the chat composer's focus. The reviewed marks stay,
//     so resuming is one click.
//   - Identity-preserving state (gitpanel.go's rule): the reviewed set
//     is keyed by absolute path and pruned on refresh, like `checked` —
//     a mark for a file that has left the change list would silently
//     widen the next commit.
//   - No animation (the roadmap's §6): the survey advances only when the
//     user does something.

package app

import "github.com/gdamore/tcell/v2"

// gitPanelReviewGlyph is the mark in the list's leftmost column. The
// column is one cell wide and carries the whole review story: a caret
// for the file being surveyed now, a check for one already read, blank
// for one not yet reached. It sits LEFT of the "[x]" box rather than
// replacing it because the two mean different things (see the file
// comment) and a reviewer needs to see both at once.
const (
	gitPanelReviewGlyph  = '✓'
	gitPanelWalkAtGlyph  = '▸'
	gitPanelReviewBlank  = ' '
	gitPanelReviewColumn = 0 // offset from the list's left edge
)

// -----------------------------------------------------------------------------
// The reviewed set
// -----------------------------------------------------------------------------

// gitPanelIsReviewed reports whether path carries a review mark.
// Reading a nil map is legal in Go, so a zero-value panel needs no
// initialisation — same contract as gitPanelIsChecked.
func (a *App) gitPanelIsReviewed(path string) bool {
	return a.gitPanel.reviewed[path]
}

// gitPanelMarkReviewed records that a file has been read. Idempotent —
// the walk calls it every time it leaves a file, including files the
// user had already ticked off by hand.
func (a *App) gitPanelMarkReviewed(path string) {
	if path == "" {
		return
	}
	if a.gitPanel.reviewed == nil {
		a.gitPanel.reviewed = make(map[string]bool)
	}
	a.gitPanel.reviewed[path] = true
}

// gitPanelToggleReviewed flips one file's mark — the manual path, for
// the reviewer who read a file in the editor rather than in the strip,
// or who wants to un-say it. Unticking deletes the key rather than
// storing false so len() stays the count.
func (a *App) gitPanelToggleReviewed(path string) {
	if a.gitPanel.reviewed[path] {
		delete(a.gitPanel.reviewed, path)
		return
	}
	a.gitPanelMarkReviewed(path)
}

// gitPanelReviewedCount is the numerator of the "5/7 reviewed" nudge.
func (a *App) gitPanelReviewedCount() int {
	return len(a.gitPanel.reviewed)
}

// gitPanelPruneReviewed drops marks for files that have left the change
// list — committed, discarded, or reverted since they were read. The
// twin of gitPanelPruneChecked, and it matters more here: the walk's
// terminal commit targets the reviewed set, so a stale mark would put a
// path git no longer lists into a commit's pathspec.
func (a *App) gitPanelPruneReviewed() {
	if len(a.gitPanel.reviewed) == 0 {
		return
	}
	live := make(map[string]bool, len(a.gitPanel.files))
	for _, f := range a.gitPanel.files {
		live[f.Path] = true
	}
	for p := range a.gitPanel.reviewed {
		if !live[p] {
			delete(a.gitPanel.reviewed, p)
		}
	}
}

// gitPanelReviewedFiles returns the reviewed entries in list order, so
// a commit's pathspec and any flash read top-down like the panel does.
func (a *App) gitPanelReviewedFiles() []gitPanelFile {
	return gitPanelFilter(a.gitPanel.files, func(f gitPanelFile) bool {
		return a.gitPanel.reviewed[f.Path]
	})
}

// gitPanelReviewNudge is the "(5/7 reviewed)" suffix the commit rows
// carry, or "" when there is nothing to nudge about — no review has
// started, or every file has been read and the sentence would only be
// congratulating the user. A nudge that is always on stops being read.
func (a *App) gitPanelReviewNudge() string {
	n := a.gitPanelReviewedCount()
	total := len(a.gitPanel.files)
	if n == 0 || total == 0 || n >= total {
		return ""
	}
	return " (" + itoa(n) + "/" + itoa(total) + " reviewed)"
}

// -----------------------------------------------------------------------------
// The header button
// -----------------------------------------------------------------------------

// gitPanelReviewLabel is the survey button's text, and therefore its
// width — one source for the drawer and the hit-test. It states what
// the click will DO, which is three different things:
//
//	" Review all ▶ "  nothing read yet — start at the top
//	" Resume 3/7 ▶ "  a survey was interrupted — pick it back up
//	" Next 3/7 ▶ "    the survey is running — this is the mouse's `n`
func (a *App) gitPanelReviewLabel() string {
	total := len(a.gitPanel.files)
	pos := a.gitPanel.selected + 1
	if pos < 1 {
		pos = 1
	}
	switch {
	case a.gitPanel.walk:
		return " Next " + itoa(pos) + "/" + itoa(total) + " ▶ "
	case a.gitPanelReviewedCount() > 0:
		return " Resume " + itoa(a.gitPanelReviewedCount()) + "/" + itoa(total) + " ▶ "
	}
	return " Review all ▶ "
}

// gitPanelReviewRect is the survey button's rectangle, immediately
// right of "Actions ▾". A zero width means "this panel is too narrow to
// carry the button" — btnRect.contains is false for w == 0, so the
// drawer and the hit-test drop out together and the ≡ row remains the
// way in. The gap kept before the ✕ is the same restraint the title and
// the tick count already show.
func (a *App) gitPanelReviewRect() btnRect {
	if !a.gitPanel.open || len(a.gitPanel.files) == 0 {
		return btnRect{}
	}
	actions := a.gitPanelActionsRect()
	label := a.gitPanelReviewLabel()
	r := btnRect{x: actions.x + actions.w + 1, y: actions.y, w: runeLen(label)}
	if r.x+r.w > a.gitPanelCloseRect().x-1 {
		return btnRect{}
	}
	return r
}

// -----------------------------------------------------------------------------
// Walking
// -----------------------------------------------------------------------------

// startGitPanelWalk begins (or resumes) the survey and hands the panel
// the keyboard. It starts at the first UNREVIEWED file, which is what
// makes the button say "Resume": a survey interrupted at file 4 should
// not restart at file 1, and one that is complete should be re-walkable
// from the top rather than refusing.
func (a *App) startGitPanelWalk() {
	if !a.gitPanel.open || len(a.gitPanel.files) == 0 {
		return
	}
	// Single-owner keyboard, same as every other focusable surface: the
	// click handlers keep these mutually exclusive, this is the path in
	// that has no click.
	a.term.focused = false
	a.chat.focused = false
	a.treeFocus = false
	a.gitPanel.walk = true
	a.gitPanelSelect(a.gitPanelFirstUnreviewed())
	a.flash("Reviewing " + plural(len(a.gitPanel.files), "file", "files") +
		" — n/p to step, space to page, Enter to commit, q to stop")
}

// gitPanelFirstUnreviewed is where a resumed survey picks up: the first
// file with no mark, or the top of the list when every file has one.
func (a *App) gitPanelFirstUnreviewed() int {
	for i, f := range a.gitPanel.files {
		if !a.gitPanel.reviewed[f.Path] {
			return i
		}
	}
	return 0
}

// stopGitPanelWalk ends the survey and gives the keyboard back. The
// reviewed marks stay — they are the record of what was read, not a
// property of the mode that recorded them.
func (a *App) stopGitPanelWalk() {
	a.gitPanel.walk = false
}

// gitPanelSelect moves the highlight to idx and fetches that file's
// diff. The one path both the walk and the mouse take, so a click and
// an `n` land in exactly the same state.
func (a *App) gitPanelSelect(idx int) {
	if idx < 0 || idx >= len(a.gitPanel.files) {
		return
	}
	a.gitPanel.selected = idx
	a.gitPanel.diffScroll = 0
	a.gitPanelEnsureSelectedVisible()
	a.requestGitPanelDiff(a.gitPanel.files[idx])
}

// gitPanelWalkStep moves the survey by dir files, marking the file it
// LEAVES as reviewed — the mark means "you have been shown this", and
// stepping away is the moment that becomes true.
//
// It deliberately does not wrap and does not end the survey: walking off
// the top stays put, and walking off the bottom returns false so the
// caller decides what "done" means. That split is what lets `n` finish
// on the commit prompt while the wheel — which nobody aims precisely —
// simply stops at the end instead of throwing up a modal.
func (a *App) gitPanelWalkStep(dir int) bool {
	f, ok := a.gitPanelSelectedFile()
	if !ok {
		return false
	}
	next := a.gitPanel.selected + dir
	if next < 0 || next >= len(a.gitPanel.files) {
		return false
	}
	a.gitPanelMarkReviewed(f.Path)
	a.gitPanelSelect(next)
	return true
}

// gitPanelWalkNext is `n` and the header button: step forward, or —
// having run out of files — mark the last one read and end the survey
// on the commit-message field. That terminal state is the whole point
// of the feature: a survey that ends by dumping you back where you
// started is a viewer, not a workflow.
func (a *App) gitPanelWalkNext() {
	if a.gitPanelWalkStep(1) {
		return
	}
	if f, ok := a.gitPanelSelectedFile(); ok {
		a.gitPanelMarkReviewed(f.Path)
	}
	a.gitPanelWalkFinish()
}

// gitPanelWalkFinish ends the survey on the commit prompt. Enter does
// this from anywhere in the walk — a reviewer who has seen enough at
// file 3 of 7 should not have to page through four more to reach the
// message field.
func (a *App) gitPanelWalkFinish() {
	a.stopGitPanelWalk()
	targets := a.gitPanelWalkCommitTargets()
	if len(targets) == 0 {
		a.flash("Nothing to commit")
		return
	}
	a.openCommitPrompt(targets, "")
}

// gitPanelWalkCommitTargets is what the survey's terminal commit
// includes:
//
//	ticked files   an explicit selection always outranks an inferred
//	               one — the user said these, in as many words.
//	reviewed files otherwise, the survey commits what the survey read.
//	               This is why the reviewed set is pruned on refresh.
//	the selection  and if neither exists (Enter pressed before anything
//	               was read), the plain panel target, unchanged.
func (a *App) gitPanelWalkCommitTargets() []gitPanelFile {
	if a.gitPanelCheckedCount() > 0 {
		return a.gitPanelTargets()
	}
	if reviewed := a.gitPanelReviewedFiles(); len(reviewed) > 0 {
		return reviewed
	}
	return a.gitPanelTargets()
}

// gitPanelWalkPage scrolls the diff by a screenful in direction dir and
// reports whether it moved. False means the pane was already at that
// end — which is the wheel's and space's cue to change file.
func (a *App) gitPanelWalkPage(dir int) bool {
	_, _, _, ph := a.gitPanelRect()
	page := ph - 2
	if page < 1 {
		page = 1
	}
	before := a.gitPanel.diffScroll
	a.gitPanel.diffScroll += dir * page
	a.gitPanelClampScrolls()
	return a.gitPanel.diffScroll != before
}

// gitPanelWalkAtEnd / gitPanelWalkAtTop report whether the diff pane is
// scrolled hard against an edge. The wheel consults them to decide
// whether one more notch means "keep reading" or "next file" — the
// detent is free: the notch that REACHES the edge clamps and does
// nothing else, so changing file always takes a deliberate extra push.
func (a *App) gitPanelWalkAtEnd() bool {
	_, _, _, ph := a.gitPanelRect()
	max := len(a.gitPanel.diffLines) - (ph - 1)
	if max < 0 {
		max = 0
	}
	return a.gitPanel.diffScroll >= max
}

// gitPanelWalkAtTop is gitPanelWalkAtEnd's mirror; see there.
func (a *App) gitPanelWalkAtTop() bool {
	return a.gitPanel.diffScroll <= 0
}

// -----------------------------------------------------------------------------
// The keyboard
// -----------------------------------------------------------------------------

// handleGitPanelWalkKey is the survey's key router, installed in
// handleKey beside the terminal's and the chat composer's — AFTER the
// Esc / leader / menu blocks, so every global gesture keeps working
// from inside the walk and only plain editing keys are claimed.
//
// The claimed set is a pager's, which is what a survey is:
//
//	n / p        next / previous file (p never leaves the top)
//	space        page the diff, then step at the bottom — the one-key
//	             "keep going" that reads a long diff and a short one
//	             with the same gesture
//	↑ ↓ PgUp PgDn scroll this file's diff
//	Enter        stop here and commit what has been reviewed
//	h            hunk actions for this file (the Tier-0 route to the
//	             chips, which a click-swallowing terminal can't reach)
//	a            the panel's Actions list, unchanged
//	r            toggle this file's review mark by hand
//	q            leave the survey; the marks stay
//
// Everything else is swallowed rather than passed through: a keystroke
// aimed at a panel that owns the keyboard must never leak into the
// buffer (the same contract the tree branch states).
func (a *App) handleGitPanelWalkKey(ev *tcell.EventKey) {
	switch ev.Key() {
	case tcell.KeyEnter:
		a.gitPanelWalkFinish()
		return
	case tcell.KeyUp:
		a.gitPanel.diffScroll--
		a.gitPanelClampScrolls()
		return
	case tcell.KeyDown:
		a.gitPanel.diffScroll++
		a.gitPanelClampScrolls()
		return
	case tcell.KeyPgUp:
		a.gitPanelWalkPage(-1)
		return
	case tcell.KeyPgDn:
		a.gitPanelWalkPage(1)
		return
	}
	if ev.Key() != tcell.KeyRune {
		return
	}
	switch ev.Rune() {
	case 'n':
		a.gitPanelWalkNext()
	case 'p':
		a.gitPanelWalkStep(-1)
	case ' ':
		// Page first, change file only when the page had nowhere to go.
		if !a.gitPanelWalkPage(1) {
			a.gitPanelWalkNext()
		}
	case 'h':
		a.openGitPanelHunks()
	case 'a':
		a.openGitPanelActions()
	case 'r':
		if f, ok := a.gitPanelSelectedFile(); ok {
			a.gitPanelToggleReviewed(f.Path)
		}
	case 'q':
		a.stopGitPanelWalk()
		a.flash("Survey paused — " + itoa(a.gitPanelReviewedCount()) + " reviewed")
	}
}

// -----------------------------------------------------------------------------
// Entry points
// -----------------------------------------------------------------------------

// menuGitPanelReview is the ≡ / command-palette entry: open the panel
// if it is shut and start the survey. One thought, not two — the same
// argument that lets "Search history…" open the log panel it filters.
func (a *App) menuGitPanelReview() {
	a.closeMenu()
	if !a.gitIsRepo {
		return
	}
	if !a.gitPanel.open {
		a.menuToggleGitPanel() // refreshes the file list on the way in
	}
	if len(a.gitPanel.files) == 0 {
		a.flash("Nothing to review — the work tree is clean")
		return
	}
	a.startGitPanelWalk()
}

// hasGitPanelReview gates that row: a repo with something to look at.
// The file list is only populated while the panel is open, so a shut
// panel falls back to the tree's dirty set — the same snapshot the
// stash row reads, and the reason the row isn't dimmed on a dirty tree
// just because the panel hasn't been opened yet.
func (a *App) hasGitPanelReview() bool {
	if !a.gitIsRepo {
		return false
	}
	if a.gitPanel.open {
		return len(a.gitPanel.files) > 0
	}
	return a.tree != nil && len(a.tree.DirtyFiles) > 0
}
