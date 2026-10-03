// =============================================================================
// File: internal/app/gitrestore_test.go
// Author: Rohan Allison <rohanthewiz@gmail.com>
// Created: 2026-10-03
// Copyright: 2026 Rohan Allison. All rights reserved.
// =============================================================================

package app

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rohanthewiz/ced/internal/filetree"
)

// restoreRepo builds a repo with f.txt committed as "one\n" and returns
// the (symlink-resolved) root and the file's path. Skips without git.
func restoreRepo(t *testing.T) (string, string) {
	t.Helper()
	if !gitAvailable() {
		t.Skip("git not on PATH")
	}
	repo := initRepo(t)
	file := filepath.Join(repo, "f.txt")
	writeFileT(t, file, "one\n")
	gitRun(t, repo, "add", ".")
	gitRun(t, repo, "commit", "-q", "-m", "init")
	return repo, file
}

// readFileT returns path's content, failing the test on error.
func readFileT(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(b)
}

// TestParseNumstat reads line counts, git's binary marker, and degrades
// garbage to zeros.
func TestParseNumstat(t *testing.T) {
	if a, d, bin := parseNumstat("12\t3\tf.txt\n"); a != 12 || d != 3 || bin {
		t.Fatalf("got %d %d %v, want 12 3 false", a, d, bin)
	}
	if _, _, bin := parseNumstat("-\t-\timg.png\n"); !bin {
		t.Fatal("'-\\t-' must read as binary")
	}
	if a, d, bin := parseNumstat(""); a != 0 || d != 0 || bin {
		t.Fatalf("empty output: got %d %d %v", a, d, bin)
	}
}

// TestProbeGitRestore classifies a modified, an untracked and a clean
// file — the three answers restoreFile branches on.
func TestProbeGitRestore(t *testing.T) {
	repo, file := restoreRepo(t)
	if p := probeGitRestore(file); p.Code != "" || !p.InHead {
		t.Fatalf("clean file probe = %+v, want no code, in HEAD", p)
	}

	writeFileT(t, file, "one\ntwo\nthree\n")
	p := probeGitRestore(file)
	if p.Code != " M" || !p.InHead || p.Added != 2 || p.Deleted != 0 {
		t.Fatalf("modified probe = %+v, want \" M\", in HEAD, +2 −0", p)
	}

	fresh := filepath.Join(repo, "new.txt")
	writeFileT(t, fresh, "x\n")
	if p := probeGitRestore(fresh); p.Code != "??" || p.InHead {
		t.Fatalf("untracked probe = %+v, want \"??\", not in HEAD", p)
	}
}

// TestRestoreFile_RevertsDiskAndOpenTab is the end-to-end path: a
// modified file with a dirty open tab is confirmed, git rewrites the
// file, the tab reloads clean — and one Undo brings the user's text back.
func TestRestoreFile_RevertsDiskAndOpenTab(t *testing.T) {
	repo, file := restoreRepo(t)
	writeFileT(t, file, "one\ntwo\n")

	a := newTestApp(t, repo)
	a.refreshGitStatus()
	a.openFile(file)
	tab := a.activeTabPtr()
	tab.InsertString("X")
	if !tab.Dirty {
		t.Fatal("setup: tab should be dirty")
	}
	before := tab.Buffer.String()

	a.restoreFile(file)
	m := confirmOf(a)
	if m == nil {
		t.Fatal("restore should confirm first")
	}
	body := strings.Join(m.lines, "\n")
	for _, want := range []string{"+1 −0", "unsaved edits", "One Undo"} {
		if !strings.Contains(body, want) {
			t.Errorf("confirm body missing %q:\n%s", want, body)
		}
	}
	if readFileT(t, file) != "one\ntwo\n" {
		t.Fatal("nothing may change before Yes")
	}

	m.yes(a)
	pumpAppEvents(t, a, func() bool { return strings.Contains(a.statusMsg, "Restored f.txt") })

	if got := readFileT(t, file); got != "one\n" {
		t.Fatalf("disk = %q, want the committed %q", got, "one\n")
	}
	if tab.Dirty || tab.Buffer.String() != "one\n" {
		t.Fatalf("tab = %q dirty=%v, want the committed text, clean", tab.Buffer.String(), tab.Dirty)
	}
	if a.formatRunning(file) {
		t.Fatal("the reconcile hold must be released on success")
	}
	if !tab.Undo() || tab.Buffer.String() != before {
		t.Fatalf("undo should bring back %q, got %q", before, tab.Buffer.String())
	}
}

// TestRestoreFile_ResetsStagedChange checks restore clears the index
// side too, and that the confirm says staged work is lost.
func TestRestoreFile_ResetsStagedChange(t *testing.T) {
	repo, file := restoreRepo(t)
	writeFileT(t, file, "staged\n")
	gitRun(t, repo, "add", "f.txt")

	a := newTestApp(t, repo)
	a.refreshGitStatus()
	a.restoreFile(file)
	m := confirmOf(a)
	if m == nil {
		t.Fatal("restore should confirm")
	}
	if !strings.Contains(strings.Join(m.lines, "\n"), "Staged") {
		t.Fatalf("confirm should name the staged loss: %v", m.lines)
	}
	if strings.Contains(strings.Join(m.lines, "\n"), "Undo") {
		t.Fatalf("no tab is open, so no Undo may be promised: %v", m.lines)
	}
	m.yes(a)
	pumpAppEvents(t, a, func() bool { return strings.Contains(a.statusMsg, "Restored f.txt") })

	if staged := gitOut(t, repo, "diff", "--cached", "--name-only"); staged != "" {
		t.Fatalf("index still carries %q", staged)
	}
	if got := readFileT(t, file); got != "one\n" {
		t.Fatalf("disk = %q, want %q", got, "one\n")
	}
}

// TestRestoreFile_NothingToRestore pins the flashes that answer instead
// of confirming: clean file, untracked file, not a repo, untitled buffer.
func TestRestoreFile_NothingToRestore(t *testing.T) {
	repo, file := restoreRepo(t)
	a := newTestApp(t, repo)
	a.refreshGitStatus()

	a.restoreFile(file)
	if a.modal != nil || !strings.Contains(a.statusMsg, "No uncommitted changes in f.txt") {
		t.Fatalf("clean file: modal=%v flash=%q", a.modal, a.statusMsg)
	}

	fresh := filepath.Join(repo, "new.txt")
	writeFileT(t, fresh, "x\n")
	a.restoreFile(fresh)
	if a.modal != nil || !strings.Contains(a.statusMsg, "new.txt is new") {
		t.Fatalf("untracked file: modal=%v flash=%q", a.modal, a.statusMsg)
	}
	if _, err := os.Stat(fresh); err != nil {
		t.Fatal("an untracked file must never be touched")
	}

	a.restoreFile("")
	if !strings.Contains(a.statusMsg, "never been saved") {
		t.Fatalf("untitled: flash=%q", a.statusMsg)
	}

	b := newTestApp(t, t.TempDir())
	b.restoreFile(filepath.Join(b.rootDir, "x"))
	if !strings.Contains(b.statusMsg, "Not a git repository") {
		t.Fatalf("non-repo: flash=%q", b.statusMsg)
	}
}

// TestRestoreFile_DirtyBufferOnCleanFile covers the buffer-only path:
// disk already matches HEAD, so Yes reverts the tab without running git,
// still undoably.
func TestRestoreFile_DirtyBufferOnCleanFile(t *testing.T) {
	repo, file := restoreRepo(t)
	a := newTestApp(t, repo)
	a.refreshGitStatus()
	a.openFile(file)
	tab := a.activeTabPtr()
	tab.InsertString("X")

	a.restoreFile(file)
	m := confirmOf(a)
	if m == nil || !strings.Contains(m.lines[0], "Discard unsaved edits") {
		t.Fatalf("want the unsaved-edits confirm, got %+v", m)
	}
	m.yes(a)
	if tab.Dirty || tab.Buffer.String() != "one\n" {
		t.Fatalf("tab = %q dirty=%v, want reverted and clean", tab.Buffer.String(), tab.Dirty)
	}
	if !tab.Undo() || !strings.Contains(tab.Buffer.String(), "X") {
		t.Fatal("undo should bring the edit back")
	}
}

// TestRunGitRestore_ReleasesHoldOnFailure checks a failing git run
// releases the reconcile hold and reports through the error modal — a
// leaked hold would stop the tick watching the file for good.
func TestRunGitRestore_ReleasesHoldOnFailure(t *testing.T) {
	repo, _ := restoreRepo(t)
	a := newTestApp(t, repo)
	bogus := filepath.Join(repo, "never-committed.txt")
	a.runGitRestore(bogus)
	if !a.formatRunning(bogus) {
		t.Fatal("the hold should be taken while git runs")
	}
	pumpAppEvents(t, a, func() bool { return !a.formatRunning(bogus) })
	if m := confirmOf(a); m == nil || !m.info {
		t.Fatalf("a git failure should open the info modal, got %T", a.modal)
	}
}

// TestRestoreRows_AllThreeDoors pins the tree row (files in a repo only),
// the tab-menu row, and the ≡ Git twin, and that each lands in
// restoreFile.
func TestRestoreRows_AllThreeDoors(t *testing.T) {
	repo, file := restoreRepo(t)
	writeFileT(t, file, "changed\n")
	a := newTestApp(t, repo)
	a.refreshGitStatus()
	a.tree.Refresh()

	var node, dirNode *filetree.Node
	if err := os.Mkdir(filepath.Join(repo, "sub"), 0755); err != nil {
		t.Fatal(err)
	}
	a.tree.Refresh()
	for _, c := range a.tree.Root.Children {
		switch c.Name {
		case "f.txt":
			node = c
		case "sub":
			dirNode = c
		}
	}
	if node == nil || dirNode == nil {
		t.Fatal("setup: tree nodes missing")
	}

	// Tree: present on the file, absent on a folder.
	hasRow := func(items []contextItem) *contextItem {
		for i := range items {
			if items[i].label == "Git restore…" {
				return &items[i]
			}
		}
		return nil
	}
	a.openTreeContext(dirNode, 5, 5)
	if hasRow(contextOf(a).items) != nil {
		t.Fatal("a folder must not offer Git restore")
	}
	a.closeModal()
	a.openTreeContext(node, 5, 5)
	row := hasRow(contextOf(a).items)
	if row == nil {
		t.Fatal("a repo file should offer Git restore")
	}
	a.closeModal()
	row.action(a, node)
	if confirmOf(a) == nil {
		t.Fatal("the tree row should reach the restore confirm")
	}
	a.closeModal()

	// Tab menu row.
	a.openFile(file)
	var found bool
	for _, it := range a.tabContextItems(a.activeTabPtr()) {
		if it.label == "Restore (discard changes)…" {
			found = true
			if !it.enabled(a) {
				t.Fatal("tab row should be enabled in a repo")
			}
			it.action(a)
			if confirmOf(a) == nil {
				t.Fatal("the tab row should reach the restore confirm")
			}
			a.closeModal()
		}
	}
	if !found {
		t.Fatal("tab menu lacks the Restore row")
	}

	// ≡ Git twin.
	it := menuItemByLabel(t, a, "Restore file (discard changes)…")
	if !it.enabled(a) {
		t.Fatal("≡ row should be enabled with a repo file in front")
	}
	it.action(a)
	if confirmOf(a) == nil {
		t.Fatal("the ≡ row should reach the restore confirm")
	}
}

// TestOpenTreeContext_NoGitRestoreOutsideRepo keeps the tree row out of
// a project that is not a repository.
func TestOpenTreeContext_NoGitRestoreOutsideRepo(t *testing.T) {
	dir := t.TempDir()
	writeFileT(t, filepath.Join(dir, "f.txt"), "x")
	a := newTestApp(t, dir)
	for _, c := range a.tree.Root.Children {
		if c.Name == "f.txt" {
			a.openTreeContext(c, 5, 5)
		}
	}
	for _, it := range contextOf(a).items {
		if it.label == "Git restore…" {
			t.Fatal("Git restore offered outside a repository")
		}
	}
}
