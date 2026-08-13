// =============================================================================
// File: internal/app/gitlog_test.go
// Author: Rohan Allison <rohanthewiz@gmail.com>
// Created: 2026-07-29
// Copyright: 2026 Rohan Allison. All rights reserved.
// =============================================================================

// Tests for the git log panel: the format parser, the geometry clamps,
// the single-occupancy bottom-strip contract, detail-row jump mapping,
// and the identity-preserving refresh — the pure logic against fixtures,
// the loaders against a real `git init`'d repo (skipped when git is
// absent, same policy as the other git tests).

package app

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gdamore/tcell/v2"
)

// TestParseGitLogCommits pins the tab-separated decode: all six fields
// land, a tab INSIDE the subject survives (the subject is the trailing
// SplitN field), decorations pass through, and malformed lines are
// skipped rather than erroring — the best-effort read contract.
func TestParseGitLogCommits(t *testing.T) {
	out := []byte(strings.Join([]string{
		"aaaa\ta1\tAlice\t2 hours ago\tHEAD -> main, tag: v1\tfix: the thing",
		"bbbb\tb2\tBob\t3 days ago\t\tsubject with\ttab inside",
		"not-a-commit-line",
		"",
	}, "\n"))
	commits := parseGitLogCommits(out)
	if len(commits) != 2 {
		t.Fatalf("parsed %d commits, want 2", len(commits))
	}
	first := commits[0]
	if first.Hash != "aaaa" || first.Short != "a1" || first.Author != "Alice" ||
		first.When != "2 hours ago" || first.Refs != "HEAD -> main, tag: v1" ||
		first.Subject != "fix: the thing" {
		t.Errorf("first commit fields wrong: %+v", first)
	}
	if commits[1].Subject != "subject with\ttab inside" {
		t.Errorf("tab inside subject not preserved: %q", commits[1].Subject)
	}
	if commits[1].Refs != "" {
		t.Errorf("undecorated commit should have empty Refs, got %q", commits[1].Refs)
	}
}

// TestGitLogListWidth pins the commit-list column clamps: auto mode is
// two fifths of the panel, explicit choices clamp to the min/max band,
// and nothing may push past three fifths of the panel — the detail pane
// keeps its share.
func TestGitLogListWidth(t *testing.T) {
	if got := gitLogListWidth(100, 0); got != 40 {
		t.Errorf("auto on 100 cols = %d, want 40 (two fifths)", got)
	}
	if got := gitLogListWidth(100, 5); got != gitLogMinListW {
		t.Errorf("tiny desired = %d, want floor %d", got, gitLogMinListW)
	}
	if got := gitLogListWidth(200, 150); got != gitLogMaxListW {
		t.Errorf("huge desired = %d, want cap %d", got, gitLogMaxListW)
	}
	// On a narrow panel the three-fifths cap wins over the band.
	if got := gitLogListWidth(60, 50); got != 36 {
		t.Errorf("narrow-panel desired 50 = %d, want 36 (three fifths of 60)", got)
	}
}

// TestMenuToggleGitLog_SingleOccupancy verifies the bottom strip stays
// single-occupancy in every direction: opening the log evicts the
// changes panel and a bottom-docked terminal, opening either of those
// evicts the log, and a non-repo project makes the toggle a silent
// no-op (the leader contract).
func TestMenuToggleGitLog_SingleOccupancy(t *testing.T) {
	a := newTestApp(t, t.TempDir())

	// Non-repo: the toggle must not open the panel.
	a.gitIsRepo = false
	a.menuToggleGitLog()
	if a.gitLog.open {
		t.Fatal("git log opened outside a repo")
	}

	a.gitIsRepo = true
	a.gitPanel.open = true
	a.term.open = true // bottom-docked (termDockLeft false)
	a.menuToggleGitLog()
	if !a.gitLog.open {
		t.Fatal("git log did not open in a repo")
	}
	if a.gitPanel.open || a.term.open {
		t.Errorf("bottom strip not single-occupancy: gitPanel=%v term=%v",
			a.gitPanel.open, a.term.open)
	}

	// The changes panel reclaims the strip from the log.
	a.menuToggleGitPanel()
	if a.gitLog.open || !a.gitPanel.open {
		t.Errorf("changes panel should evict the log: log=%v panel=%v",
			a.gitLog.open, a.gitPanel.open)
	}

	// A left-docked terminal does NOT compete for the bottom.
	a.gitPanel.open = false
	a.termDockLeft = true
	a.term.open = true
	a.menuToggleGitLog()
	if !a.gitLog.open || !a.term.open {
		t.Errorf("left-docked terminal should coexist: log=%v term=%v",
			a.gitLog.open, a.term.open)
	}
}

// TestGitLogToggleLabel pins the action-not-state label convention the
// menu row shares with every other toggle.
func TestGitLogToggleLabel(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	if got := a.gitLogToggleLabel(); got != "Show git log" {
		t.Errorf("closed label = %q, want Show git log", got)
	}
	a.gitLog.open = true
	if got := a.gitLogToggleLabel(); got != "Hide git log" {
		t.Errorf("open label = %q, want Hide git log", got)
	}
}

// TestLoadGitLogCommits_RealRepo exercises the loader end-to-end:
// newest first, fields populated, HEAD's commit decorated, and no
// truncation flag on a short history. Non-repos yield nil (the
// best-effort contract).
func TestLoadGitLogCommits_RealRepo(t *testing.T) {
	if !gitAvailable() {
		t.Skip("git not on PATH")
	}
	repo := initRepo(t)
	writeCommit(t, repo, "f.txt", "one\n", "first commit")
	writeCommit(t, repo, "f.txt", "one\ntwo\n", "second commit")

	commits, truncated := loadGitLogCommits(repo)
	if truncated {
		t.Error("two commits reported as truncated")
	}
	if len(commits) != 2 {
		t.Fatalf("loaded %d commits, want 2", len(commits))
	}
	if commits[0].Subject != "second commit" || commits[1].Subject != "first commit" {
		t.Errorf("order wrong: %q then %q", commits[0].Subject, commits[1].Subject)
	}
	if commits[0].Refs == "" || !strings.Contains(commits[0].Refs, "main") {
		t.Errorf("HEAD commit should be decorated with main, got %q", commits[0].Refs)
	}
	if commits[0].Author != "Test User" {
		t.Errorf("author = %q, want Test User", commits[0].Author)
	}
	if len(commits[0].Hash) != 40 || commits[0].Short == "" {
		t.Errorf("hash fields wrong: %q / %q", commits[0].Hash, commits[0].Short)
	}

	if got, _ := loadGitLogCommits(t.TempDir()); got != nil {
		t.Errorf("non-repo should yield nil, got %d commits", len(got))
	}
}

// TestRefreshGitLogCommits_PreservesSelectionByHash pins the
// identity-preserving refresh: a new commit arriving on top must not
// steal the highlight from the commit the user is reading — the same
// rule the file tree and the changes panel follow.
func TestRefreshGitLogCommits_PreservesSelectionByHash(t *testing.T) {
	if !gitAvailable() {
		t.Skip("git not on PATH")
	}
	repo := initRepo(t)
	writeCommit(t, repo, "f.txt", "one\n", "first commit")
	writeCommit(t, repo, "f.txt", "one\ntwo\n", "second commit")

	a := newTestApp(t, repo)
	a.gitIsRepo = true
	a.gitLog.open = true
	a.refreshGitLogCommits()
	if len(a.gitLog.commits) != 2 {
		t.Fatalf("loaded %d commits, want 2", len(a.gitLog.commits))
	}
	a.gitLog.selected = 1 // "first commit"
	watched := a.gitLog.commits[1].Hash

	writeCommit(t, repo, "f.txt", "one\ntwo\nthree\n", "third commit")
	a.refreshGitLogCommits()
	if len(a.gitLog.commits) != 3 {
		t.Fatalf("after new commit: %d commits, want 3", len(a.gitLog.commits))
	}
	if got := a.gitLog.commits[a.gitLog.selected].Hash; got != watched {
		t.Errorf("selection moved to %q, want it pinned on %q", got, watched)
	}
}

// TestLoadGitLogDetail_RealRepo verifies one `git show` fetch carries
// all three layers the detail pane promises: the metadata header
// (fuller pretty), the per-file stat, and the patch itself.
func TestLoadGitLogDetail_RealRepo(t *testing.T) {
	if !gitAvailable() {
		t.Skip("git not on PATH")
	}
	repo := initRepo(t)
	writeCommit(t, repo, "f.txt", "one\n", "first commit")
	commits, _ := loadGitLogCommits(repo)
	if len(commits) != 1 {
		t.Fatalf("setup: %d commits", len(commits))
	}

	lines := loadGitLogDetail(repo, commits[0].Hash)
	joined := strings.Join(lines, "\n")
	for _, want := range []string{"commit ", "Author:", "CommitDate:", "f.txt | 1 +", "+one"} {
		if !strings.Contains(joined, want) {
			t.Errorf("detail missing %q:\n%s", want, joined)
		}
	}
}

// TestHandleGitLogShow_DropsStale pins the async staleness rule: a
// detail fetch landing after the user clicked to another commit is
// discarded instead of painting the wrong commit's patch.
func TestHandleGitLogShow_DropsStale(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	a.gitLog.open = true
	a.gitLog.commits = []gitLogCommit{{Hash: "current"}, {Hash: "stale"}}
	a.gitLog.selected = 0

	a.handleGitLogShow(&gitLogShowEvent{when: time.Now(), hash: "stale", lines: []string{"old"}})
	if a.gitLog.detailLines != nil {
		t.Error("stale detail was stored")
	}
	a.handleGitLogShow(&gitLogShowEvent{when: time.Now(), hash: "current", lines: []string{"new"}})
	if len(a.gitLog.detailLines) != 1 || a.gitLog.detailHash != "current" {
		t.Errorf("fresh detail not stored: hash=%q lines=%v",
			a.gitLog.detailHash, a.gitLog.detailLines)
	}
}

// TestGitLogDetailTarget maps detail-pane rows to (file, line) across a
// multi-file `git show` document: metadata and stat rows have no
// target, hunk rows track the new-side counter, and a second file's
// "+++ b/" header retargets the walk — the behavior double-click
// jumping is built on.
func TestGitLogDetailTarget(t *testing.T) {
	lines := []string{
		"commit abcd (HEAD -> main)", // 0
		"Author:     Test User",      // 1
		"",                           // 2
		"    the subject",            // 3
		"",                           // 4
		" a.txt | 2 +-",              // 5
		"diff --git a/a.txt b/a.txt", // 6
		"--- a/a.txt",                // 7
		"+++ b/a.txt",                // 8
		"@@ -1,2 +1,3 @@",            // 9
		" ctx1",                      // 10
		"+added",                     // 11
		" ctx2",                      // 12
		"diff --git a/b.txt b/b.txt", // 13
		"--- a/b.txt",                // 14
		"+++ b/b.txt",                // 15
		"@@ -10,2 +20,2 @@",          // 16
		" c1",                        // 17
		"-del",                       // 18
		"+add2",                      // 19
	}
	for _, idx := range []int{0, 1, 3, 5, 6, 7, 8, 14, 15} {
		if _, _, ok := gitLogDetailTarget(lines, idx); ok {
			t.Errorf("row %d (%q) should have no target", idx, lines[idx])
		}
	}
	cases := []struct {
		idx  int
		file string
		line int
	}{
		{9, "a.txt", 0},   // hunk header → hunk start
		{10, "a.txt", 0},  // context line
		{11, "a.txt", 1},  // the added line
		{12, "a.txt", 2},  // context after the add
		{16, "b.txt", 19}, // second file's hunk at +20
		{17, "b.txt", 19},
		{19, "b.txt", 20}, // add after a deletion: deletion held the counter
	}
	for _, c := range cases {
		file, line, ok := gitLogDetailTarget(lines, c.idx)
		if !ok || file != c.file || line != c.line {
			t.Errorf("row %d (%q): got (%q, %d, %v), want (%q, %d, true)",
				c.idx, lines[c.idx], file, line, ok, c.file, c.line)
		}
	}
	if _, _, ok := gitLogDetailTarget(lines, 99); ok {
		t.Error("out-of-range row should have no target")
	}
}

// TestGitLogPress_ButtonsAndDrags drives the header hit-testing: the
// rule outside the buttons starts the height drag, the divider column
// starts the width drag, Actions opens the verb picker, and ✕
// collapses — all through the shared btnRect geometry.
func TestGitLogPress_ButtonsAndDrags(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	a.gitIsRepo = true
	a.gitLog.open = true
	a.gitLog.commits = []gitLogCommit{{Hash: "aaaa", Short: "a1", Subject: "s"}}

	// The height-drag handle is the header rule OUTSIDE the buttons —
	// the left-anchored chain (Actions ▾ / ↑ push / ⌕ search) now runs
	// past px+30, so the sample point sits in the title band.
	px, py, _, _ := a.gitLogRect()
	if got := a.gitLogPress(px+40, py); got != "gitlog" {
		t.Errorf("header rule press = %q, want gitlog drag", got)
	}
	if got := a.gitLogPress(a.gitLogDividerX(), py+2); got != "gitlogdiv" {
		t.Errorf("divider press = %q, want gitlogdiv drag", got)
	}

	actions := a.gitLogActionsRect()
	if got := a.gitLogPress(actions.x+1, actions.y); got != "" {
		t.Errorf("actions press should not start a drag, got %q", got)
	}
	if m, ok := a.modal.(*paletteModal); !ok || !strings.Contains(m.title, "a1") {
		t.Fatalf("actions press did not open the commit picker: %T", a.modal)
	}
	a.closeModal()

	closeBtn := a.gitLogCloseRect()
	a.gitLogPress(closeBtn.x+1, closeBtn.y)
	if a.gitLog.open {
		t.Error("✕ press did not collapse the panel")
	}
}

// TestGitLogClick_SelectsCommit verifies a list-row click moves the
// highlight (and only in-range rows count), while a detail-pane single
// click stays inert — the double-click jump is the only detail gesture.
func TestGitLogClick_SelectsCommit(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	a.gitLog.open = true
	a.gitLog.commits = []gitLogCommit{
		{Hash: "aaaa", Short: "a1"}, {Hash: "bbbb", Short: "b2"},
	}
	px, py, pw, _ := a.gitLogRect()
	a.gitLogClick(px+2, py+2) // second visible row
	if a.gitLog.selected != 1 {
		t.Errorf("selected = %d, want 1", a.gitLog.selected)
	}
	a.gitLogClick(px+2, py+10) // past the list
	if a.gitLog.selected != 1 {
		t.Errorf("out-of-range click moved selection to %d", a.gitLog.selected)
	}
	a.gitLogClick(px+a.gitLogListW(pw)+2, py+1) // detail pane, single click
	if a.gitLog.selected != 1 {
		t.Errorf("detail click moved selection to %d", a.gitLog.selected)
	}
}

// TestDrawGitLog_Smoke renders the whole panel to the simulation screen
// and checks the header (title, Actions ▾, ⧉ hash, ⟳, ✕) and a commit
// row landed — the full paint path at once, per the UI-test convention.
func TestDrawGitLog_Smoke(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	a.gitIsRepo = true
	a.gitLog.open = true
	a.gitLog.commits = []gitLogCommit{
		{Hash: "aaaa", Short: "abc1234", Author: "Alice", When: "2 hours ago",
			Refs: "HEAD -> main", Subject: "fix: the thing"},
	}
	a.gitLog.detailLines = []string{"commit aaaa", "+added line"}
	a.draw()
	scr := a.screen.(tcell.SimulationScreen)
	scr.Show()

	cells, w, _ := scr.GetContents()
	rowText := func(y int) string {
		var b strings.Builder
		for x := 0; x < w; x++ {
			c := cells[y*w+x]
			if len(c.Runes) > 0 {
				b.WriteRune(c.Runes[0])
			}
		}
		return b.String()
	}
	_, py, _, _ := a.gitLogRect()
	header := rowText(py)
	for _, want := range []string{"Actions ▾", "Git log · 1 commit", "⧉ hash", "⟳", "✕"} {
		if !strings.Contains(header, want) {
			t.Errorf("header row = %q, missing %q", header, want)
		}
	}
	body := rowText(py + 1)
	for _, want := range []string{"●", "abc1234", "fix: the thing", "commit aaaa"} {
		if !strings.Contains(body, want) {
			t.Errorf("first body row = %q, missing %q", body, want)
		}
	}
}

// writeCommit writes content to name under repo and commits it — the
// two-line fixture helper every real-repo test here shares.
func writeCommit(t *testing.T, repo, name, content, msg string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(repo, name), []byte(content), 0644); err != nil {
		t.Fatalf("write %s: %v", name, err)
	}
	gitRun(t, repo, "add", ".")
	gitRun(t, repo, "commit", "-q", "-m", msg)
}
