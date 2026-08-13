// =============================================================================
// File: internal/app/gitpanelwalk_test.go
// Author: Rohan Allison <rohanthewiz@gmail.com>
// Created: 2026-08-12
// Copyright: 2026 Rohan Allison. All rights reserved.
// =============================================================================

package app

import (
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gdamore/tcell/v2"
)

// walkTestApp builds an open panel over three synthetic changed files —
// enough to have a middle, which is where every off-by-one in a walk
// lives. No git needed: the walk is state, and the diff pane's contents
// are somebody else's test.
func walkTestApp(t *testing.T) *App {
	t.Helper()
	a := newTestApp(t, t.TempDir())
	a.gitIsRepo = true
	a.gitPanel.open = true
	a.gitPanel.files = []gitPanelFile{
		{Path: "/p/a.go", Rel: "a.go", Code: " M"},
		{Path: "/p/b.go", Rel: "b.go", Code: " M"},
		{Path: "/p/c.go", Rel: "c.go", Code: " M"},
	}
	return a
}

// TestGitPanelWalk_StepsAndMarks pins the survey's core promise: the
// file you LEAVE is the file that gets marked read, stepping is linear
// (no wrap in either direction), and the marks are keyed by path so a
// list rebuild can't shuffle them onto the wrong rows.
func TestGitPanelWalk_StepsAndMarks(t *testing.T) {
	a := walkTestApp(t)
	a.startGitPanelWalk()
	if !a.gitPanel.walk || a.gitPanel.selected != 0 {
		t.Fatalf("walk = %v selected = %d, want on at 0", a.gitPanel.walk, a.gitPanel.selected)
	}
	if a.gitPanelReviewedCount() != 0 {
		t.Fatal("starting a walk must not mark anything read")
	}

	a.gitPanelWalkStep(1)
	if a.gitPanel.selected != 1 || !a.gitPanelIsReviewed("/p/a.go") {
		t.Fatalf("after one step: selected %d, a.go reviewed %v",
			a.gitPanel.selected, a.gitPanelIsReviewed("/p/a.go"))
	}
	if a.gitPanelIsReviewed("/p/b.go") {
		t.Fatal("the file you arrive at is not yet read")
	}

	// Backwards never leaves the list, and never marks past the end.
	a.gitPanelWalkStep(-1)
	a.gitPanelWalkStep(-1)
	if a.gitPanel.selected != 0 {
		t.Fatalf("selected = %d, want pinned at 0", a.gitPanel.selected)
	}
	if got := a.gitPanelWalkStep(-1); got {
		t.Fatal("stepping off the top should report no movement")
	}

	// A resumed walk picks up at the first UNREAD file, not the top.
	a.stopGitPanelWalk()
	a.gitPanelMarkReviewed("/p/a.go")
	a.gitPanelMarkReviewed("/p/b.go")
	a.startGitPanelWalk()
	if a.gitPanel.selected != 2 {
		t.Fatalf("resumed at %d, want the first unread (2)", a.gitPanel.selected)
	}
}

// TestGitPanelWalk_ReviewedIsNotChecked pins the distinction the whole
// feature rests on: reading a file is not selecting it, the two sets
// move independently, and both are pruned when a file leaves the list.
func TestGitPanelWalk_ReviewedIsNotChecked(t *testing.T) {
	a := walkTestApp(t)
	a.gitPanelMarkReviewed("/p/a.go")
	a.gitPanelToggleChecked("/p/b.go")

	if a.gitPanelIsChecked("/p/a.go") || a.gitPanelIsReviewed("/p/b.go") {
		t.Fatal("the two sets must not leak into each other")
	}
	if a.gitPanelReviewedCount() != 1 || a.gitPanelCheckedCount() != 1 {
		t.Fatalf("counts = %d reviewed / %d checked", a.gitPanelReviewedCount(), a.gitPanelCheckedCount())
	}

	// a.go is committed away; its mark must go with it, or the survey's
	// terminal commit would carry a path git no longer lists.
	a.gitPanel.files = a.gitPanel.files[1:]
	a.gitPanelPruneReviewed()
	if a.gitPanelIsReviewed("/p/a.go") {
		t.Fatal("a mark outlived the file it described")
	}

	// The manual toggle is a toggle, both ways.
	a.gitPanelToggleReviewed("/p/c.go")
	a.gitPanelToggleReviewed("/p/c.go")
	if a.gitPanelIsReviewed("/p/c.go") {
		t.Fatal("second toggle should clear the mark")
	}
}

// TestGitPanelReviewNudge pins the nudge's manners: silent before a
// survey starts and silent once everything has been read, so the one
// state it does describe still registers.
func TestGitPanelReviewNudge(t *testing.T) {
	a := walkTestApp(t)
	if got := a.gitPanelReviewNudge(); got != "" {
		t.Fatalf("nudge with nothing read = %q, want silence", got)
	}
	a.gitPanelMarkReviewed("/p/a.go")
	if got := a.gitPanelReviewNudge(); got != " (1/3 reviewed)" {
		t.Fatalf("nudge = %q", got)
	}
	a.gitPanelMarkReviewed("/p/b.go")
	a.gitPanelMarkReviewed("/p/c.go")
	if got := a.gitPanelReviewNudge(); got != "" {
		t.Fatalf("nudge at 3/3 = %q, want silence", got)
	}
}

// TestGitPanelWalk_EndsOnCommitPrompt pins the terminal state — the
// reason the feature exists. Walking off the last file marks it, ends
// the walk, and opens the commit prompt over what the survey READ.
func TestGitPanelWalk_EndsOnCommitPrompt(t *testing.T) {
	a := walkTestApp(t)
	a.startGitPanelWalk()
	a.gitPanelWalkNext() // a → b
	a.gitPanelWalkNext() // b → c
	if a.modal != nil {
		t.Fatalf("modal appeared mid-walk: %T", a.modal)
	}
	a.gitPanelWalkNext() // off the end

	if a.gitPanel.walk {
		t.Fatal("the walk should be over")
	}
	if a.gitPanelReviewedCount() != 3 {
		t.Fatalf("reviewed = %d, want all 3 (including the last file)", a.gitPanelReviewedCount())
	}
	pm, ok := a.modal.(*promptModal)
	if !ok {
		t.Fatalf("walk ended on %T, want the commit prompt", a.modal)
	}
	if !strings.Contains(pm.title, "3 files") {
		t.Fatalf("prompt title = %q, want the reviewed set", pm.title)
	}

	// An explicit tick outranks the inferred set — the user said these,
	// in as many words.
	a.closeModal()
	a.gitPanelToggleChecked("/p/b.go")
	if got := a.gitPanelWalkCommitTargets(); len(got) != 1 || got[0].Path != "/p/b.go" {
		t.Fatalf("targets with a tick = %+v, want just b.go", got)
	}
}

// TestGitPanelWalk_KeysAndEscape drives the survey through the real key
// router: n / p step, the keystrokes never reach the buffer, and Esc
// hands the keyboard back without losing what was read.
func TestGitPanelWalk_KeysAndEscape(t *testing.T) {
	a := walkTestApp(t)
	target := filepath.Join(a.rootDir, "typing.txt")
	writeFileT(t, target, "seed")
	a.openFile(target)
	before := a.activeTabPtr().Buffer.String()

	a.startGitPanelWalk()
	a.handleKey(tcell.NewEventKey(tcell.KeyRune, 'n', tcell.ModNone))
	if a.gitPanel.selected != 1 {
		t.Fatalf("n moved to %d, want 1", a.gitPanel.selected)
	}
	a.handleKey(tcell.NewEventKey(tcell.KeyRune, 'p', tcell.ModNone))
	if a.gitPanel.selected != 0 {
		t.Fatalf("p moved to %d, want 0", a.gitPanel.selected)
	}
	if got := a.activeTabPtr().Buffer.String(); got != before {
		t.Fatalf("survey keys leaked into the buffer: %q", got)
	}

	// Esc is the universal "drop that": it ends the survey (via the Esc
	// block's side effects) but keeps the marks.
	a.handleKey(tcell.NewEventKey(tcell.KeyEsc, 0, tcell.ModNone))
	if a.gitPanel.walk {
		t.Fatal("Esc should end the survey")
	}
	if a.gitPanelReviewedCount() == 0 {
		t.Fatal("Esc must not discard what was already read")
	}
	// …and the keyboard is the editor's again. (The Esc above armed the
	// leader, and Esc-n is New file — disarm it, or this asserts on the
	// leader table instead of on focus.)
	a.lastEscape = time.Time{}
	a.handleKey(tcell.NewEventKey(tcell.KeyRune, 'n', tcell.ModNone))
	if got := a.activeTabPtr().Buffer.String(); got == before {
		t.Fatalf("typing after the survey = %q, want the rune inserted", got)
	}
}

// TestGitPanelWalk_ClickOutsideReleases pins the mouse-first focus rule:
// clicking into the editor to go fix what you spotted ends the survey,
// so a walk still holding n / p can't type its verbs into the fix.
func TestGitPanelWalk_ClickOutsideReleases(t *testing.T) {
	a := walkTestApp(t)
	a.startGitPanelWalk()
	a.gitPanelWalkNext() // one file read before the interruption

	ex, ey, _, _ := a.editorRect()
	a.handleMouse(tcell.NewEventMouse(ex+1, ey+1, tcell.Button1, 0))
	if a.gitPanel.walk {
		t.Fatal("a press outside the panel should end the survey")
	}
	if a.gitPanelReviewedCount() == 0 {
		t.Fatal("the marks are the record, not part of the mode")
	}
}

// TestGitPanelReviewButton pins the header control: its label states
// what the click does, the rect is where it's drawn, and clicking it
// starts the survey and then advances it.
func TestGitPanelReviewButton(t *testing.T) {
	a := walkTestApp(t)
	if got := a.gitPanelReviewLabel(); !strings.Contains(got, "Review all") {
		t.Fatalf("idle label = %q", got)
	}

	btn := a.gitPanelReviewRect()
	if btn.w == 0 {
		t.Fatal("a 120-column panel has room for the button")
	}
	a.handleMouse(tcell.NewEventMouse(btn.x+1, btn.y, tcell.Button1, 0))
	if !a.gitPanel.walk {
		t.Fatal("the button should start the survey")
	}
	if got := a.gitPanelReviewLabel(); !strings.Contains(got, "Next 1/3") {
		t.Fatalf("walking label = %q", got)
	}

	btn = a.gitPanelReviewRect()
	a.handleMouse(tcell.NewEventMouse(btn.x+1, btn.y, tcell.Button1, 0))
	if a.gitPanel.selected != 1 {
		t.Fatalf("second click selected %d, want 1 (the button is the mouse's n)", a.gitPanel.selected)
	}

	// Paused, it offers to resume from where the marks stop.
	a.stopGitPanelWalk()
	if got := a.gitPanelReviewLabel(); !strings.Contains(got, "Resume 1/3") {
		t.Fatalf("paused label = %q", got)
	}
	// And it vanishes rather than overlapping the ✕ on a narrow panel.
	a.screen.SetSize(34, 40)
	a.width, a.height = 34, 40
	if got := a.gitPanelReviewRect(); got.w != 0 {
		t.Fatalf("narrow panel button = %+v, want withheld", got)
	}
}

// TestGitPanelHeader_TitleYieldsToTheButton pins the header's growth
// rule, and a bug it already had: the title used to be drawn from the
// end of "Actions ▾", straight over the survey button beside it. A
// label is optional and a control is not, so the label starts after
// the last button — or, on a panel too narrow for both, not at all.
func TestGitPanelHeader_TitleYieldsToTheButton(t *testing.T) {
	a := walkTestApp(t)
	a.gitPanel.walk = true
	px, py, pw, _ := a.gitPanelRect()
	a.drawGitPanel()

	btn := a.gitPanelReviewRect()
	if btn.w == 0 {
		t.Fatal("expected the button on a 120-column panel")
	}
	if got := screenRow(t, a, py, btn.x, btn.w); got != a.gitPanelReviewLabel() {
		t.Fatalf("button row = %q, want %q (the title painted over it)", got, a.gitPanelReviewLabel())
	}
	if got := screenRow(t, a, py, px, pw); !strings.Contains(got, "Git changes") {
		t.Fatalf("the title should still fit beside it: %q", got)
	}
}

// TestGitPanelReviewColumn pins the list's leftmost cell: it draws the
// walk's position and the read marks, and clicking it toggles a mark
// without moving the highlight (the checkbox's own rule).
func TestGitPanelReviewColumn(t *testing.T) {
	a := walkTestApp(t)
	a.gitPanelMarkReviewed("/p/b.go")
	a.gitPanel.walk = true
	a.gitPanel.selected = 0

	px, py, _, _ := a.gitPanelRect()
	a.drawGitPanel()
	if got := screenRow(t, a, py+1, px, 1); got != string(gitPanelWalkAtGlyph) {
		t.Fatalf("current row mark = %q, want %q", got, string(gitPanelWalkAtGlyph))
	}
	if got := screenRow(t, a, py+2, px, 1); got != string(gitPanelReviewGlyph) {
		t.Fatalf("reviewed row mark = %q, want %q", got, string(gitPanelReviewGlyph))
	}
	if got := screenRow(t, a, py+3, px, 1); got != " " {
		t.Fatalf("unread row mark = %q, want blank", got)
	}

	// A click in that column marks the row without selecting it.
	a.handleMouse(tcell.NewEventMouse(px, py+3, tcell.Button1, 0))
	if !a.gitPanelIsReviewed("/p/c.go") {
		t.Fatal("clicking the review column should mark the row")
	}
	if a.gitPanel.selected != 0 {
		t.Fatalf("selection moved to %d; the column is not a row click", a.gitPanel.selected)
	}
}

// TestGitPanelWalk_WheelStepsAtTheEdge pins the survey's wheel: it
// scrolls the diff until the pane runs out, then changes file — and
// never ends the survey, because a commit modal should not appear in
// answer to a scroll.
func TestGitPanelWalk_WheelStepsAtTheEdge(t *testing.T) {
	a := walkTestApp(t)
	a.gitPanel.walk = true
	px, _, pw, ph := a.gitPanelRect()
	diffX := px + a.gitPanelListW(pw) + 2
	// A diff exactly one screenful longer than the pane: one notch to
	// the bottom, and the notch after that is the one that steps.
	for i := 0; i < ph; i++ {
		a.gitPanel.diffLines = append(a.gitPanel.diffLines, "+line")
	}

	a.gitPanelScroll(diffX, 0, 1)
	if a.gitPanel.selected != 0 {
		t.Fatal("the notch that reaches the bottom must not also change file")
	}
	a.gitPanelScroll(diffX, 0, 1)
	if a.gitPanel.selected != 1 || !a.gitPanelIsReviewed("/p/a.go") {
		t.Fatalf("wheel at the end: selected %d, a.go read %v",
			a.gitPanel.selected, a.gitPanelIsReviewed("/p/a.go"))
	}

	// Off the last file, the wheel stops rather than opening a modal.
	a.gitPanel.selected = 2
	a.gitPanel.diffLines = nil
	a.gitPanelScroll(diffX, 0, 1)
	if a.modal != nil {
		t.Fatalf("the wheel opened %T; only n / Enter may finish the survey", a.modal)
	}
	if a.gitPanel.walk != true {
		t.Fatal("the wheel must not end the survey")
	}
}

// TestCommitPromptAIButton pins the survey's landing spot: the commit
// prompt carries the ✦ AI button whenever an agent could draft the
// message, and it fills the field rather than submitting.
func TestCommitPromptAIButton(t *testing.T) {
	a := walkTestApp(t)
	a.copilot.enabled = true
	a.chat.dead = false
	a.openCommitPrompt(a.gitPanel.files, "")
	pm, ok := a.modal.(*promptModal)
	if !ok {
		t.Fatalf("modal = %T, want the commit prompt", a.modal)
	}
	if pm.extraLabel == "" || pm.extraRun == nil {
		t.Fatal("an available agent should put the ✦ AI button on the row")
	}
	if extra := pm.extraButton(a); extra.w == 0 {
		t.Fatal("the button has to fit the standard prompt")
	} else if _, ok := pm.buttons(a); extra.x <= ok.x {
		t.Fatal("the extra button belongs right of OK")
	}

	// With no agent the prompt is exactly what it always was.
	a.closeModal()
	a.chat.dead = true
	a.openCommitPrompt(a.gitPanel.files, "")
	pm = a.modal.(*promptModal)
	if pm.extraLabel != "" || pm.extraButton(a).w != 0 {
		t.Fatal("no agent, no button")
	}
}

// TestMenuGitPanelReview pins the ≡ route: it opens the panel on the
// way in, because walking the changes and showing them are one thought.
func TestMenuGitPanelReview(t *testing.T) {
	if !gitAvailable() {
		t.Skip("git not on PATH")
	}
	repo, file := panelRepo(t)
	writeFileT(t, file, "one\nCHANGED\n")

	a := newTestApp(t, repo)
	a.refreshGitStatus()
	if !a.hasGitPanelReview() {
		t.Fatal("a dirty tree should offer the row with the panel still shut")
	}
	a.menuGitPanelReview()
	if !a.gitPanel.open || !a.gitPanel.walk {
		t.Fatalf("open = %v walk = %v, want both", a.gitPanel.open, a.gitPanel.walk)
	}
}
