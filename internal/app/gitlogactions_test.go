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
// touch, so the picker says exactly what Enter is about to do. The
// trailing ellipsis on a row is the house promise that it opens
// something rather than firing; cherry-pick and revert carry it because
// both now confirm first.
func TestGitLogActionItems_Inventory(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	a.gitBranch = "main"
	c := gitLogCommit{Hash: strings.Repeat("a", 40), Short: "abc1234"}

	items := a.gitLogActionItems(c)
	want := []string{
		"Cherry-pick abc1234 onto main…",
		"Revert abc1234…",
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

// TestConfirmGitLogApply_NamesCommitAndTarget pins the new gate in front
// of cherry-pick and revert: nothing runs until a confirm has said which
// commit and onto what. The subject is in the body because a seven-char
// hash is not something anyone recognises, and the branch is there
// because the log lists commits from --all — the row under the pointer
// routinely belongs to a branch you are not standing on.
func TestConfirmGitLogApply_NamesCommitAndTarget(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	a.gitBranch = "main"
	c := gitLogCommit{
		Hash:    strings.Repeat("d", 40),
		Short:   "def4567",
		Subject: "fix: the widget alignment",
	}

	for i, verb := range []string{"Cherry-pick", "Revert"} {
		a.closeModal()
		a.gitLogActionItems(c)[i].run(a)
		m, ok := a.modal.(*confirmModal)
		if !ok {
			t.Fatalf("%s opened %T, want a confirm before anything ran", verb, a.modal)
		}
		if !strings.Contains(m.title, "def4567") {
			t.Errorf("%s confirm title = %q, should name the commit", verb, m.title)
		}
		body := strings.Join(m.lines, " ")
		if !strings.Contains(body, "fix: the widget alignment") {
			t.Errorf("%s confirm body = %q, should quote the subject", verb, body)
		}
		if !strings.Contains(body, "main") {
			t.Errorf("%s confirm body = %q, should name the target branch", verb, body)
		}
		// The default focus is No — the shared confirm contract, which is
		// the reason a reflex Enter here is harmless.
		if m.hover != 0 {
			t.Errorf("%s confirm opened focused on button %d, want No", verb, m.hover)
		}
	}
}

// TestConfirmGitLogApply_ElidesLongSubject keeps a rambling commit
// subject from pushing the closing quote — and the rest of the sentence
// — off the 54-cell dialog.
func TestConfirmGitLogApply_ElidesLongSubject(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	a.gitBranch = "main"
	c := gitLogCommit{
		Hash:    strings.Repeat("e", 40),
		Short:   "efa5678",
		Subject: strings.Repeat("very long subject ", 10),
	}
	a.confirmGitLogApply(c, "Cherry-pick", "onto main", "cherry-pick", c.Hash)
	m := a.modal.(*confirmModal)
	if got := runeLen(m.lines[0]); got > gitLogSubjectMax+2 { // +2 for the quotes
		t.Errorf("subject line is %d cells, want at most %d", got, gitLogSubjectMax+2)
	}
	if !strings.Contains(m.lines[0], "…") {
		t.Errorf("a cut subject must say so: %q", m.lines[0])
	}
}

// TestTryGitLogContextClick_SelectsRowUnderPointer is the contract every
// context menu must honour: right-clicking row three acts on row three,
// not on whatever was highlighted before.
func TestTryGitLogContextClick_SelectsRowUnderPointer(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	a.gitIsRepo = true
	a.gitBranch = "main"
	a.gitLog.open = true
	a.gitLog.commits = []gitLogCommit{
		{Hash: strings.Repeat("a", 40), Short: "aaa1111", Subject: "first"},
		{Hash: strings.Repeat("b", 40), Short: "bbb2222", Subject: "second"},
		{Hash: strings.Repeat("c", 40), Short: "ccc3333", Subject: "third"},
	}

	px, _, _, _ := a.gitLogRect()
	if !a.tryGitLogContextClick(px+2, a.gitLogBodyTop()+2) {
		t.Fatal("a right-click on a commit row was not consumed")
	}
	if a.gitLog.selected != 2 {
		t.Errorf("selected = %d, want the row under the pointer (2)", a.gitLog.selected)
	}
	m, ok := a.modal.(*editorContextModal)
	if !ok {
		t.Fatalf("opened %T, want the anchored context menu", a.modal)
	}
	// Same verbs as the Tier-0 picker, in the same order — one source.
	want := a.gitLogActionItems(a.gitLog.commits[2])
	if len(m.items) != len(want) {
		t.Fatalf("%d rows, want %d", len(m.items), len(want))
	}
	for i := range want {
		if m.items[i].label != want[i].label {
			t.Errorf("row %d = %q, want %q", i, m.items[i].label, want[i].label)
		}
	}
	if !strings.Contains(m.items[0].label, "ccc3333") {
		t.Errorf("menu aimed at %q, want the clicked commit", m.items[0].label)
	}
}

// TestTryGitLogContextClick_Boundaries pins what the handler does NOT
// claim (a closed panel, a point outside it — both fall through to the
// ≡ menu) and what it swallows without acting (the header, which is
// inside the panel: escalating there would answer a click on the log
// with a menu about the editor).
func TestTryGitLogContextClick_Boundaries(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	a.gitIsRepo = true
	a.gitLog.commits = []gitLogCommit{{Hash: strings.Repeat("a", 40), Short: "aaa1111"}}

	px, py, _, _ := a.gitLogRect()
	if a.tryGitLogContextClick(px+2, py+2) {
		t.Error("a closed panel must not claim the gesture")
	}

	a.gitLog.open = true
	if a.tryGitLogContextClick(px+2, py-1) {
		t.Error("a point above the panel must not claim the gesture")
	}
	if !a.tryGitLogContextClick(px+2, py) {
		t.Error("the header should swallow the gesture rather than escalate")
	}
	if a.modal != nil {
		t.Errorf("the header opened %T, want nothing", a.modal)
	}
}

// TestTryGitLogContextClick_DetailPaneUsesSelection covers the other
// half of the panel: the detail pane belongs to the selection, so a
// right-click there opens the menu for it without re-aiming.
func TestTryGitLogContextClick_DetailPaneUsesSelection(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	a.gitIsRepo = true
	a.gitLog.open = true
	a.gitLog.commits = []gitLogCommit{
		{Hash: strings.Repeat("a", 40), Short: "aaa1111"},
		{Hash: strings.Repeat("b", 40), Short: "bbb2222"},
	}
	a.gitLog.selected = 1

	px, _, pw, _ := a.gitLogRect()
	if !a.tryGitLogContextClick(px+a.gitLogListW(pw)+2, a.gitLogBodyTop()) {
		t.Fatal("a right-click in the detail pane was not consumed")
	}
	if a.gitLog.selected != 1 {
		t.Errorf("selection moved to %d — the detail pane has no row to re-aim at", a.gitLog.selected)
	}
	m, ok := a.modal.(*editorContextModal)
	if !ok {
		t.Fatalf("opened %T, want the anchored context menu", a.modal)
	}
	if !strings.Contains(m.items[0].label, "bbb2222") {
		t.Errorf("menu aimed at %q, want the selected commit", m.items[0].label)
	}
}

// TestContextItemsFromPalette pins the adapter: labels and closures
// survive, and every row arrives enabled — a picker has no way to
// express a disabled row, so it never holds one.
func TestContextItemsFromPalette(t *testing.T) {
	fired := ""
	items := contextItemsFromPalette([]paletteItem{
		{label: "one", run: func(*App) { fired = "one" }},
		{label: "two", run: func(*App) { fired = "two" }},
	})
	if len(items) != 2 || items[0].label != "one" || items[1].label != "two" {
		t.Fatalf("adapted rows = %+v", items)
	}
	for i, it := range items {
		if !it.enabled(nil) {
			t.Errorf("row %d arrived disabled", i)
		}
	}
	items[1].action(nil)
	if fired != "two" {
		t.Errorf("closure fired = %q, want two", fired)
	}
}
