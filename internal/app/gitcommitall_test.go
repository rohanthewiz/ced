// =============================================================================
// File: internal/app/gitcommitall_test.go
// Author: Rohan Allison <rohanthewiz@gmail.com>
// Created: 2026-11-16
// Copyright: 2026 Rohan Allison. All rights reserved.
// =============================================================================

package app

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// commitAllPrompt returns the open commit prompt, failing the test when
// the slot holds anything else — every happy path below goes through it.
func commitAllPrompt(t *testing.T, a *App) *promptModal {
	t.Helper()
	pm, ok := a.modal.(*promptModal)
	if !ok {
		t.Fatalf("modal = %T (flash %q), want the commit prompt", a.modal, a.statusMsg)
	}
	return pm
}

// submitCommitAll types msg into the open commit-all prompt, presses
// Enter, and waits for the commit to land in the log.
func submitCommitAll(t *testing.T, a *App, repo, msg string) {
	t.Helper()
	pm := commitAllPrompt(t, a)
	pm.field.insertString(msg)
	pm.submit(a)
	pumpAppEvents(t, a, func() bool {
		return gitOut(t, repo, "log", "-1", "--pretty=%s") == msg
	})
}

// TestMenuGitCommitAll_CommitsEveryKindOfChange pins the verb's promise
// end to end: an edit, a brand-new file, a deletion and a STAGED RENAME
// all land in one commit, and the tree is clean afterwards. The rename is
// the case a per-file commit gets wrong (the panel lists it under the new
// path only), which is why the commit is pathspec'd by the folder.
func TestMenuGitCommitAll_CommitsEveryKindOfChange(t *testing.T) {
	if !gitAvailable() {
		t.Skip("git not on PATH")
	}
	repo, file := panelRepo(t)
	writeFileT(t, filepath.Join(repo, "gone.txt"), "doomed\n")
	writeFileT(t, filepath.Join(repo, "old.txt"), "moving\n")
	gitRun(t, repo, "add", ".")
	gitRun(t, repo, "commit", "-q", "-m", "more")

	writeFileT(t, file, "one\nEDITED\n")
	writeFileT(t, filepath.Join(repo, "new.txt"), "fresh\n")
	if err := os.Remove(filepath.Join(repo, "gone.txt")); err != nil {
		t.Fatal(err)
	}
	gitRun(t, repo, "mv", "old.txt", "renamed.txt")

	a := newTestApp(t, repo)
	a.refreshGitStatus()
	a.menuGitCommitAll()
	if pm := commitAllPrompt(t, a); !strings.Contains(pm.title, "all changes (4 files)") {
		t.Errorf("title = %q, want the change count", pm.title)
	}
	submitCommitAll(t, a, repo, "Commit the lot")

	if st := gitOut(t, repo, "status", "--porcelain"); st != "" {
		t.Errorf("tree not clean after commit all:\n%s", st)
	}
	names := gitOut(t, repo, "show", "--name-status", "--pretty=", "-M", "HEAD")
	for _, want := range []string{"f.txt", "new.txt", "gone.txt", "renamed.txt", "old.txt"} {
		if !strings.Contains(names, want) {
			t.Errorf("commit missing %s:\n%s", want, names)
		}
	}
}

// TestMenuGitCommitAll_SavesUnsavedTabsFirst pins the "what you see is
// what is committed" rule: a tab with edits auto-save has not reached
// yet is saved when the prompt opens, and those edits are in the commit
// — even when the file on disk had been clean, so the change existed
// ONLY in the buffer.
func TestMenuGitCommitAll_SavesUnsavedTabsFirst(t *testing.T) {
	if !gitAvailable() {
		t.Skip("git not on PATH")
	}
	repo, file := panelRepo(t)
	a := newTestApp(t, repo)
	a.refreshGitStatus()
	tab := openTabAtPath(t, a, file)
	tab.InsertString("typed ")
	if !tab.Dirty {
		t.Fatal("fixture: the edit should leave the tab dirty")
	}

	a.menuGitCommitAll()
	if tab.Dirty {
		t.Fatal("commit all should have saved the tab before prompting")
	}
	submitCommitAll(t, a, repo, "Commit the buffer")

	if got := gitOut(t, repo, "show", "HEAD:f.txt"); !strings.HasPrefix(got, "typed one") {
		t.Errorf("committed f.txt = %q, want the buffer's edit", got)
	}
}

// TestMenuGitCommitAll_DiskConflictStopsBeforeThePrompt pins the save
// guard's place in the gesture: a dirty tab whose file changed on disk
// raises the clobber question INSTEAD of the commit prompt (no message
// typed yet, so nothing to lose), and "Keep mine" carries on into the
// prompt the user asked for rather than stranding them with one save.
func TestMenuGitCommitAll_DiskConflictStopsBeforeThePrompt(t *testing.T) {
	if !gitAvailable() {
		t.Skip("git not on PATH")
	}
	repo, file := panelRepo(t)
	a := newTestApp(t, repo)
	a.refreshGitStatus()
	tab := openTabAtPath(t, a, file)
	tab.InsertString("mine ")
	externalWrite(t, file, "theirs\n", 60)

	a.menuGitCommitAll()
	if conflictOf(a) == nil {
		t.Fatalf("modal = %T, want the disk-conflict question", a.modal)
	}
	c := a.conflictFor(tab)
	if c == nil {
		t.Fatal("expected a recorded conflict")
	}
	a.closeAllModals()
	a.conflictKeepMine(c)

	pm := commitAllPrompt(t, a)
	if !strings.Contains(pm.title, "all changes") {
		t.Errorf("Keep mine resumed into %q, want the commit-all prompt", pm.title)
	}
	if tab.Dirty {
		t.Error("Keep mine should have saved the tab on the way to the prompt")
	}
}

// TestMenuGitCommitAll_NothingToCommit pins the empty answer: a clean
// tree flashes instead of opening a prompt whose Enter would only fail.
func TestMenuGitCommitAll_NothingToCommit(t *testing.T) {
	if !gitAvailable() {
		t.Skip("git not on PATH")
	}
	repo, _ := panelRepo(t)
	a := newTestApp(t, repo)
	a.refreshGitStatus()

	a.menuGitCommitAll()
	if a.modal != nil {
		t.Fatalf("clean tree opened %T", a.modal)
	}
	if a.statusMsg != "Nothing to commit" {
		t.Errorf("flash = %q", a.statusMsg)
	}
}

// TestMenuGitCommitAll_RefusesOutsideARepo pins the non-repo answer: the
// leader can fire anywhere, so the verb explains itself instead of
// running git in a folder git knows nothing about.
func TestMenuGitCommitAll_RefusesOutsideARepo(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	a.gitIsRepo = false

	a.menuGitCommitAll()
	if a.modal != nil {
		t.Fatalf("non-repo opened %T", a.modal)
	}
	if !strings.Contains(a.statusMsg, "Not a git repository") {
		t.Errorf("flash = %q", a.statusMsg)
	}
}

// TestMenuGitCommitAll_RefusesWhileAnOperationIsParked pins the parked-op
// refusal: mid-merge a pathspec'd commit is refused by git, and the way
// out is the Conflicts panel's Continue — so the panel comes up with the
// reason, and no prompt opens.
func TestMenuGitCommitAll_RefusesWhileAnOperationIsParked(t *testing.T) {
	if !gitAvailable() {
		t.Skip("git not on PATH")
	}
	repo, file := panelRepo(t)
	writeFileT(t, file, "one\nEDITED\n")
	// gitInProgressOp is stat-based: the marker alone parks a merge.
	writeFileT(t, filepath.Join(repo, ".git", "MERGE_HEAD"), gitOut(t, repo, "rev-parse", "HEAD")+"\n")
	a := newTestApp(t, repo)
	a.refreshGitStatus()

	a.menuGitCommitAll()
	if _, ok := a.modal.(*promptModal); ok {
		t.Fatal("a parked merge still opened the commit prompt")
	}
	if !strings.Contains(a.statusMsg, "merge is in progress") {
		t.Errorf("flash = %q", a.statusMsg)
	}
	if !a.toolOpen(toolConflicts) {
		t.Error("the Conflicts panel should come up with the refusal")
	}
}

// TestMenuGitCommitAll_StaysInsideTheProjectFolder pins the folder scope
// on a project opened at a SUBFOLDER of its repository: changes outside
// the folder — edited or already staged — are neither committed nor
// unstaged, the way the git panel never lists them either.
func TestMenuGitCommitAll_StaysInsideTheProjectFolder(t *testing.T) {
	if !gitAvailable() {
		t.Skip("git not on PATH")
	}
	repo, file := panelRepo(t)
	sub := filepath.Join(repo, "proj")
	inside := filepath.Join(sub, "in.txt")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	writeFileT(t, inside, "in\n")
	gitRun(t, repo, "add", ".")
	gitRun(t, repo, "commit", "-q", "-m", "sub")

	writeFileT(t, inside, "in\nEDITED\n")
	writeFileT(t, file, "one\nOUTSIDE\n")
	staged := filepath.Join(repo, "staged.txt")
	writeFileT(t, staged, "staged outside\n")
	gitRun(t, repo, "add", "staged.txt")

	a := newTestApp(t, sub)
	a.refreshGitStatus()
	a.menuGitCommitAll()
	if pm := commitAllPrompt(t, a); !strings.Contains(pm.title, "(1 file)") {
		t.Errorf("title = %q, want only the folder's one change counted", pm.title)
	}
	submitCommitAll(t, a, repo, "Commit the folder")

	if names := gitOut(t, repo, "show", "--name-only", "--pretty=", "HEAD"); names != "proj/in.txt" {
		t.Errorf("commit touched %q, want just proj/in.txt", names)
	}
	if got := gitOut(t, repo, "diff", "--cached", "--name-only"); got != "staged.txt" {
		t.Errorf("index after commit = %q, want staged.txt still staged", got)
	}
	if got := gitOut(t, repo, "diff", "--name-only"); got != "f.txt" {
		t.Errorf("work tree after commit = %q, want f.txt still modified", got)
	}
}

// TestSaveTabsForCommitAll_SkipsTabsOutsideTheFolder pins which tabs the
// gesture writes: dirty tabs inside the project are saved and counted,
// a dirty tab on a file outside it is left exactly as it was — the
// commit could not include it, so saving it would be a side effect with
// nothing to show for it.
func TestSaveTabsForCommitAll_SkipsTabsOutsideTheFolder(t *testing.T) {
	root := t.TempDir()
	a := newTestApp(t, root)
	in := filepath.Join(root, "in.txt")
	writeFileT(t, in, "in\n")
	outside := filepath.Join(t.TempDir(), "out.txt")
	writeFileT(t, outside, "out\n")
	inTab := openTabAtPath(t, a, in)
	outTab := openTabAtPath(t, a, outside)
	inTab.InsertString("x")
	outTab.InsertString("y")

	saved, ok := a.saveTabsForCommitAll()
	if !ok || saved != 1 {
		t.Fatalf("saveTabsForCommitAll = (%d, %v), want (1, true)", saved, ok)
	}
	if inTab.Dirty {
		t.Error("the tab inside the folder should be saved")
	}
	if !outTab.Dirty {
		t.Error("the tab outside the folder must be left unsaved")
	}
	if got, _ := os.ReadFile(outside); string(got) != "out\n" {
		t.Errorf("outside file rewritten: %q", got)
	}
}
