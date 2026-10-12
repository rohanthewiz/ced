// =============================================================================
// File: internal/app/gitlogticks_test.go
// Author: Rohan Allison <rohanthewiz@gmail.com>
// Created: 2026-10-11
// Copyright: 2026 Rohan Allison. All rights reserved.
// =============================================================================

package app

import (
	"strings"
	"testing"
)

// tickRepo builds main plus a side branch with three clean commits made
// in quick succession (so they can share a committer second — the case
// a date-only order gets wrong), checks out main, and returns an App on
// it with the log loaded.
func tickRepo(t *testing.T) (*App, string) {
	t.Helper()
	requireGit(t)
	repo := initRepo(t)
	writeCommit(t, repo, "base.txt", "base\n", "base")
	gitRun(t, repo, "checkout", "-q", "-b", "side")
	writeCommit(t, repo, "s1.txt", "s1\n", "s1 first")
	writeCommit(t, repo, "s2.txt", "s2\n", "s2 second")
	writeCommit(t, repo, "s3.txt", "s3\n", "s3 third")
	gitRun(t, repo, "checkout", "-q", "main")

	a := newTestApp(t, repo)
	a.rootDir = repo
	a.refreshGitStatus()
	commits, _ := loadGitLogCommits(repo)
	a.gitLog.commits = commits
	return a, repo
}

// logCommit returns the loaded log row whose subject is subj.
func logCommit(t *testing.T, a *App, subj string) gitLogCommit {
	t.Helper()
	for _, c := range a.gitLog.commits {
		if c.Subject == subj {
			return c
		}
	}
	t.Fatalf("no commit %q in the log", subj)
	return gitLogCommit{}
}

// TestGitLogToggleTick pins the set's basic contract: toggle adds and
// removes by hash, the tick-cell press reports whether it hit a row, and
// clearing empties the set.
func TestGitLogToggleTick(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	a.gitLog.commits = []gitLogCommit{{Hash: "aaa", Short: "aaa"}, {Hash: "bbb", Short: "bbb"}}
	a.gitLogToggleTick(a.gitLog.commits[0])
	if _, ok := a.gitLog.ticked["aaa"]; !ok || len(a.gitLog.ticked) != 1 {
		t.Fatalf("ticked = %v, want aaa", a.gitLog.ticked)
	}
	if !a.gitLogTickPress(1) || len(a.gitLog.ticked) != 2 {
		t.Errorf("tick press on row 1: ticked = %v", a.gitLog.ticked)
	}
	if a.gitLogTickPress(5) {
		t.Error("a press past the last row claimed a tick")
	}
	a.gitLogToggleTick(a.gitLog.commits[0])
	if _, ok := a.gitLog.ticked["aaa"]; ok {
		t.Error("second toggle did not untick")
	}
	a.gitLogClearTicks()
	if len(a.gitLog.ticked) != 0 {
		t.Errorf("clear left %v", a.gitLog.ticked)
	}
}

// TestGitLogActionItems_TickRows pins where the tick rows sit: the
// per-commit Tick row after the apply pair (its label flipping to Untick),
// and the set's two rows leading the list only while something is ticked.
func TestGitLogActionItems_TickRows(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	a.gitBranch = "main"
	c := gitLogCommit{Hash: strings.Repeat("a", 40), Short: "abc1234"}

	items := a.gitLogActionItems(c)
	if got := items[2].label; got != "Tick abc1234 for a multi-commit cherry-pick" {
		t.Errorf("row 2 = %q, want the Tick row", got)
	}
	items[2].run(a)
	items = a.gitLogActionItems(c)
	if items[0].label != "Cherry-pick 1 ticked commit onto main…" || items[1].label != "Clear ticks (1)" {
		t.Errorf("set rows = %q, %q", items[0].label, items[1].label)
	}
	if got := items[4].label; got != "Untick abc1234" {
		t.Errorf("tick row after ticking = %q, want Untick", got)
	}
	items[1].run(a)
	if len(a.gitLog.ticked) != 0 {
		t.Error("Clear ticks left the set non-empty")
	}
}

// TestOrderTickedCommits pins the reversal to oldest first and that a
// walk returning the wrong set is an error, never a partial pick.
func TestOrderTickedCommits(t *testing.T) {
	ticked := map[string]gitLogCommit{
		"h1": {Hash: "h1", Subject: "old"},
		"h2": {Hash: "h2", Subject: "new"},
	}
	got, err := orderTickedCommits([]string{"h2", "h1"}, ticked)
	if err != nil || len(got) != 2 || got[0].Subject != "old" || got[1].Subject != "new" {
		t.Errorf("order = %+v, %v; want old then new", got, err)
	}
	if _, err := orderTickedCommits([]string{"h2"}, ticked); err == nil {
		t.Error("a missing commit was not an error")
	}
	if _, err := orderTickedCommits([]string{"h2", "zz"}, ticked); err == nil {
		t.Error("an unticked hash was not an error")
	}
}

// TestGitLogTicks_CherryPickEndToEnd ticks two of side's commits NEWEST
// first, confirms, and checks git: both landed on main oldest first, and
// the ticks were cleared on success. s2 is left out so the set is not
// simply "the branch".
func TestGitLogTicks_CherryPickEndToEnd(t *testing.T) {
	a, repo := tickRepo(t)
	a.gitBranch = "main"
	a.gitLogToggleTick(logCommit(t, a, "s3 third"))
	a.gitLogToggleTick(logCommit(t, a, "s1 first"))

	ordered, err := a.gitLogTickedOldestFirst()
	if err != nil || len(ordered) != 2 || ordered[0].Subject != "s1 first" {
		t.Fatalf("oldest first = %+v, %v", ordered, err)
	}

	a.confirmGitLogCherryPickTicked()
	m, ok := a.modal.(*confirmModal)
	if !ok {
		t.Fatalf("opened %T, want a confirm", a.modal)
	}
	body := strings.Join(m.lines, "\n")
	if !strings.Contains(body, "s1 first") || !strings.Contains(body, "s3 third") || !strings.Contains(body, "onto main") {
		t.Errorf("confirm body does not list the set and target:\n%s", body)
	}
	if strings.Index(body, "s1 first") > strings.Index(body, "s3 third") {
		t.Errorf("confirm lists newest first:\n%s", body)
	}
	m.callback(a)
	a.closeModal()
	pumpAppEvents(t, a, func() bool { return strings.HasPrefix(a.statusMsg, "Cherry-picked") })
	if got := gitOut(t, repo, "log", "-2", "--format=%s"); got != "s3 third\ns1 first" {
		t.Errorf("main log = %q, want s1 then s3 applied", got)
	}
	if len(a.gitLog.ticked) != 0 {
		t.Errorf("ticks survived a successful pick: %v", a.gitLog.ticked)
	}
}

// TestGitLogTicks_RefusedWhileParked pins the parked-operation gate: with
// a cherry-pick stopped on conflicts, picking the set flashes and raises
// the Conflicts panel instead of confirming.
func TestGitLogTicks_RefusedWhileParked(t *testing.T) {
	repo := cherryPickConflictRepo(t)
	a := newTestApp(t, repo)
	a.rootDir = repo
	a.refreshGitStatus()
	a.gitLog.ticked = map[string]gitLogCommit{"x": {Hash: "x", Short: "x"}}
	a.confirmGitLogCherryPickTicked()
	if _, ok := a.modal.(*confirmModal); ok {
		t.Error("a confirm opened while a cherry-pick is parked")
	}
	if !strings.Contains(a.statusMsg, "in progress") {
		t.Errorf("flash = %q, want the in-progress refusal", a.statusMsg)
	}
}

// TestDrawGitLog_TickGlyphAndTitle pins the paint: a ticked row wears the
// tick in its leading cell, an unticked one keeps it blank, and the title
// counts the set.
func TestDrawGitLog_TickGlyphAndTitle(t *testing.T) {
	a, _ := tickRepo(t)
	a.showTool(toolGitLog)
	a.gitLog.commits = a.gitLog.commits[:3]
	a.gitLogToggleTick(a.gitLog.commits[1])
	a.drawGitLog()

	px, py, pw, _ := a.gitLogRect()
	top := a.gitLogBodyTop()
	if r, _, _, _ := a.screen.GetContent(px, top+1); r != gitLogTickGlyph {
		t.Errorf("ticked row's leading cell = %q, want %q", r, gitLogTickGlyph)
	}
	if r, _, _, _ := a.screen.GetContent(px, top); r == gitLogTickGlyph {
		t.Error("unticked row wears a tick")
	}
	var header strings.Builder
	for x := px; x < px+pw; x++ {
		r, _, _, _ := a.screen.GetContent(x, py)
		header.WriteRune(r)
	}
	if !strings.Contains(header.String(), "1 ticked") {
		t.Errorf("header %q does not count the ticks", header.String())
	}
}
