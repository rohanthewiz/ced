// =============================================================================
// File: internal/app/gitlogactions_test.go
// Author: Rohan Allison <rohanthewiz@gmail.com>
// Created: 2026-07-29
// Copyright: 2026 Rohan Allison. All rights reserved.
// =============================================================================

// Tests for the git log panel's verb surface: the picker inventory, the
// destructive-verb gating (only reset --hard confirms), the menu-row
// predicate, and the copy feedback contract.

package app

import (
	"strings"
	"testing"
)

// TestGitLogActionItems_Inventory pins the verb list and its order for
// one commit: apply-elsewhere verbs first, branch motion, creation,
// then the copies — and the labels name the branch and hash they'll
// touch, so the picker says exactly what Enter is about to do.
func TestGitLogActionItems_Inventory(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	a.gitBranch = "main"
	c := gitLogCommit{Hash: strings.Repeat("a", 40), Short: "abc1234"}

	items := a.gitLogActionItems(c)
	want := []string{
		"Cherry-pick abc1234 onto main",
		"Revert abc1234",
		"Reset main to abc1234…",
		"Checkout abc1234 (detached HEAD)",
		"Create branch at abc1234…",
		"Create tag at abc1234…",
		"Copy hash",
		"Copy message",
	}
	if len(items) != len(want) {
		t.Fatalf("%d verbs, want %d", len(items), len(want))
	}
	for i, w := range want {
		if items[i].label != w {
			t.Errorf("verb %d = %q, want %q", i, items[i].label, w)
		}
	}
}

// TestGitLogBranchLabel pins the detached-HEAD fallback: with no branch
// name the labels must still say what will move.
func TestGitLogBranchLabel(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	if got := a.gitLogBranchLabel(); got != "HEAD" {
		t.Errorf("detached label = %q, want HEAD", got)
	}
	a.gitBranch = "dev"
	if got := a.gitLogBranchLabel(); got != "dev" {
		t.Errorf("branch label = %q, want dev", got)
	}
}

// TestOpenGitLogActions_NoCommit verifies the empty-list path flashes
// instead of opening a picker with nothing to pick.
func TestOpenGitLogActions_NoCommit(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	a.gitLog.open = true
	a.openGitLogActions()
	if a.modal != nil {
		t.Fatal("picker opened with no commit selected")
	}
	if !strings.Contains(a.statusMsg, "No commit") {
		t.Errorf("flash = %q, want a No commit notice", a.statusMsg)
	}
}

// TestGitLogResetPicker_OnlyHardConfirms pins the destructive-verb
// gate: the reset-mode picker offers all three modes, and picking Hard
// opens a confirm (nothing runs yet) while Soft and Mixed carry no
// confirm ellipsis — they're a reflog entry away from undone.
func TestGitLogResetPicker_OnlyHardConfirms(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	a.gitBranch = "main"
	c := gitLogCommit{Hash: strings.Repeat("b", 40), Short: "bcd2345"}

	a.openGitLogResetPicker(c)
	m, ok := a.modal.(*paletteModal)
	if !ok {
		t.Fatalf("reset picker is %T, want paletteModal", a.modal)
	}
	if len(m.items) != 3 {
		t.Fatalf("%d reset modes, want 3", len(m.items))
	}
	for i, prefix := range []string{"Soft", "Mixed", "Hard"} {
		if !strings.HasPrefix(m.items[i].label, prefix) {
			t.Errorf("mode %d = %q, want %s…", i, m.items[i].label, prefix)
		}
	}
	// Picking Hard must interpose the confirm modal, not run git.
	a.closeModal()
	m.items[2].run(a)
	if _, ok := a.modal.(*confirmModal); !ok {
		t.Fatalf("hard reset opened %T, want confirmModal", a.modal)
	}
}

// TestHasGitLogOpen gates the ≡ "Git log actions" row: panel open AND
// inside a repo, nothing else.
func TestHasGitLogOpen(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	if a.hasGitLogOpen() {
		t.Error("closed panel should not enable the row")
	}
	a.gitLog.open = true
	if a.hasGitLogOpen() {
		t.Error("non-repo should not enable the row")
	}
	a.gitIsRepo = true
	if !a.hasGitLogOpen() {
		t.Error("open panel in a repo should enable the row")
	}
}

// TestGitLogCopyHash_Flash pins the feedback contract shared with the
// changes panel's copy: whatever the host tty allows, the user hears
// SOMETHING — the confirmation names the short hash, or the failure
// says copy failed. (OSC 52 may not work in the simulation screen's
// environment, so both outcomes are legal here.)
func TestGitLogCopyHash_Flash(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	a.gitLogCopyHash(gitLogCommit{Hash: strings.Repeat("c", 40), Short: "cde3456"})
	if a.statusMsg == "" {
		t.Fatal("copy produced no status feedback")
	}
	if !strings.Contains(a.statusMsg, "cde3456") && !strings.Contains(a.statusMsg, "Copy failed") {
		t.Errorf("flash = %q, want the short hash or a failure notice", a.statusMsg)
	}
}
