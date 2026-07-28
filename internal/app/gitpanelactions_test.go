// =============================================================================
// File: internal/app/gitpanelactions_test.go
// Author: Rohan Allison <rohanthewiz@gmail.com>
// Created: 2026-07-28
// Copyright: 2026 Rohan Allison. All rights reserved.
// =============================================================================

package app

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// actionItem finds the picker row whose label starts with prefix — the
// tests drive verbs the way a user does, by picking a labelled row.
func actionItem(t *testing.T, items []paletteItem, prefix string) paletteItem {
	t.Helper()
	for _, it := range items {
		if strings.HasPrefix(it.label, prefix) {
			return it
		}
	}
	labels := make([]string, 0, len(items))
	for _, it := range items {
		labels = append(labels, it.label)
	}
	t.Fatalf("no action row starting with %q; have %v", prefix, labels)
	return paletteItem{}
}

// actionLabels flattens a picker's rows for presence/absence assertions.
func actionLabels(items []paletteItem) string {
	labels := make([]string, 0, len(items))
	for _, it := range items {
		labels = append(labels, it.label)
	}
	return strings.Join(labels, " | ")
}

// TestGitPanelTargets_TicksBeatHighlight pins the targeting rule the
// whole surface rests on: ticked files win, and with nothing ticked the
// highlighted row stands in so the very first click on Actions is
// already useful.
func TestGitPanelTargets_TicksBeatHighlight(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	a.gitPanel.files = []gitPanelFile{
		{Path: "/p/a.go", Rel: "a.go", Code: " M"},
		{Path: "/p/b.go", Rel: "b.go", Code: "M "},
		{Path: "/p/c.go", Rel: "c.go", Code: "??"},
	}
	a.gitPanel.selected = 1

	if got := a.gitPanelTargets(); len(got) != 1 || got[0].Path != "/p/b.go" {
		t.Fatalf("no ticks → targets = %+v, want the highlighted b.go", got)
	}

	// Ticked in reverse order; the result must still follow list order so
	// confirm bodies and flashes read top-down.
	a.gitPanelToggleChecked("/p/c.go")
	a.gitPanelToggleChecked("/p/a.go")
	got := a.gitPanelTargets()
	if len(got) != 2 || got[0].Path != "/p/a.go" || got[1].Path != "/p/c.go" {
		t.Fatalf("ticked targets = %+v, want a.go then c.go", got)
	}

	a.gitPanel.files = nil
	a.gitPanelClearChecked()
	if got := a.gitPanelTargets(); got != nil {
		t.Fatalf("empty panel targets = %+v, want nil", got)
	}
}

// TestGitPanelTargetLabel pins the naming both the picker rows and the
// confirm bodies share: a basename for one file, a count for many.
func TestGitPanelTargetLabel(t *testing.T) {
	one := []gitPanelFile{{Path: "/p/sub/a.go", Rel: "sub/a.go"}}
	if got := gitPanelTargetLabel(one); got != "a.go" {
		t.Errorf("single label = %q, want a.go", got)
	}
	two := append(one, gitPanelFile{Path: "/p/b.go", Rel: "b.go"})
	if got := gitPanelTargetLabel(two); got != "2 files" {
		t.Errorf("multi label = %q, want 2 files", got)
	}
}

// TestGitPanelTracked pins the Discard filter: untracked entries have no
// HEAD version to reset to, so they must never reach `git checkout`.
func TestGitPanelTracked(t *testing.T) {
	for code, want := range map[string]bool{
		" M": true, "M ": true, "MM": true, "A ": true, "D ": true, "??": false,
	} {
		if got := gitPanelTracked(gitPanelFile{Code: code}); got != want {
			t.Errorf("gitPanelTracked(%q) = %v, want %v", code, got, want)
		}
	}
}

// TestGitPanelOnDisk pins the Open filter: a listed deletion has nothing
// to open, and only real files qualify.
func TestGitPanelOnDisk(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "here.go")
	writeFileT(t, file, "package p\n")

	if !gitPanelOnDisk(gitPanelFile{Path: file}) {
		t.Error("an existing file must be openable")
	}
	if gitPanelOnDisk(gitPanelFile{Path: filepath.Join(dir, "gone.go")}) {
		t.Error("a deleted file must be filtered out of Open")
	}
	if gitPanelOnDisk(gitPanelFile{Path: dir}) {
		t.Error("a directory is not an openable file")
	}
}

// TestGitPanelActionItems_OffersOnlyApplicableVerbs pins the gating: a
// row appears only when it would do something. Offering "Unstage" with
// nothing staged just teaches the user that Enter sometimes does
// nothing — the same rule the command palette follows.
func TestGitPanelActionItems_OffersOnlyApplicableVerbs(t *testing.T) {
	a := newTestApp(t, t.TempDir())

	// A work-tree-only modification: stageable, not unstageable, tracked.
	unstaged := []gitPanelFile{{Path: "/p/a.go", Rel: "a.go", Code: " M"}}
	a.gitPanel.files = unstaged
	got := actionLabels(a.gitPanelActionItems(unstaged))
	for _, want := range []string{"Stage a.go", "Copy path of a.go", "Discard changes in a.go", "Delete a.go from disk"} {
		if !strings.Contains(got, want) {
			t.Errorf("unstaged rows missing %q: %s", want, got)
		}
	}
	if strings.Contains(got, "Unstage") {
		t.Errorf("nothing staged, yet Unstage offered: %s", got)
	}
	if strings.Contains(got, "Commit staged") {
		t.Errorf("empty index, yet Commit offered: %s", got)
	}

	// A fully staged file: unstageable, and Stage would be a no-op.
	staged := []gitPanelFile{{Path: "/p/a.go", Rel: "a.go", Code: "M "}}
	a.gitPanel.files = staged
	a.gitHasStaged = true
	got = actionLabels(a.gitPanelActionItems(staged))
	if !strings.Contains(got, "Unstage a.go") {
		t.Errorf("staged rows missing Unstage: %s", got)
	}
	if strings.Contains(got, "Stage a.go") {
		t.Errorf("already staged, yet Stage offered: %s", got)
	}
	if !strings.Contains(got, "Commit staged") {
		t.Errorf("index non-empty, yet Commit missing: %s", got)
	}

	// Partially staged ("MM") is both — that's the whole point of the
	// state existing.
	partial := []gitPanelFile{{Path: "/p/a.go", Rel: "a.go", Code: "MM"}}
	got = actionLabels(a.gitPanelActionItems(partial))
	if !strings.Contains(got, "Stage a.go") || !strings.Contains(got, "Unstage a.go") {
		t.Errorf("partial rows should offer both verbs: %s", got)
	}

	// Untracked: no HEAD to discard back to, Delete is the verb instead.
	untracked := []gitPanelFile{{Path: "/p/new.go", Rel: "new.go", Code: "??"}}
	got = actionLabels(a.gitPanelActionItems(untracked))
	if strings.Contains(got, "Discard") {
		t.Errorf("untracked file must not offer Discard: %s", got)
	}
	if !strings.Contains(got, "Delete new.go from disk") {
		t.Errorf("untracked rows missing Delete: %s", got)
	}
}

// TestGitPanelActionItems_SelectionRows pins the two bulk-selection
// rows: Select all hides once everything is ticked, Clear selection
// appears only when something is.
func TestGitPanelActionItems_SelectionRows(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	a.gitPanel.files = []gitPanelFile{
		{Path: "/p/a.go", Rel: "a.go", Code: " M"},
		{Path: "/p/b.go", Rel: "b.go", Code: " M"},
	}

	got := actionLabels(a.gitPanelActionItems(a.gitPanelTargets()))
	if !strings.Contains(got, "Select all (2)") {
		t.Errorf("nothing ticked, yet Select all missing: %s", got)
	}
	if strings.Contains(got, "Clear selection") {
		t.Errorf("nothing ticked, yet Clear offered: %s", got)
	}

	a.gitPanelSelectAll()
	got = actionLabels(a.gitPanelActionItems(a.gitPanelTargets()))
	if strings.Contains(got, "Select all") {
		t.Errorf("everything ticked, yet Select all offered: %s", got)
	}
	if !strings.Contains(got, "Clear selection (2)") {
		t.Errorf("ticks present, yet Clear missing: %s", got)
	}
}

// TestOpenGitPanelActions_EmptyPanelFlashes pins the degenerate case: a
// clean repo has no files, no index, and nothing to select, so the
// button must say so rather than opening an empty picker.
func TestOpenGitPanelActions_EmptyPanelFlashes(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	a.gitPanel.open = true

	a.openGitPanelActions()
	if a.modal != nil {
		t.Fatalf("empty panel opened %T, want no modal", a.modal)
	}
	if a.statusMsg == "" {
		t.Fatal("empty panel must flash why nothing happened")
	}
}

// TestHasGitPanelOpen gates the ≡ menu row: the actions operate on the
// panel's selection, which only exists while the panel is showing.
func TestHasGitPanelOpen(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	a.gitIsRepo = true
	if a.hasGitPanelOpen() {
		t.Error("collapsed panel must not enable the menu row")
	}
	a.gitPanel.open = true
	if !a.hasGitPanelOpen() {
		t.Error("open panel in a repo must enable the menu row")
	}
	a.gitIsRepo = false
	if a.hasGitPanelOpen() {
		t.Error("outside a repo the row must stay disabled")
	}
}

// TestGitPanelActions_StageAndUnstageTicked is the bulk-staging e2e:
// ticking two files and picking Stage runs one `git add` over both, and
// Unstage takes them back out without touching the work tree.
func TestGitPanelActions_StageAndUnstageTicked(t *testing.T) {
	if !gitAvailable() {
		t.Skip("git not on PATH")
	}
	repo, file := panelRepo(t)
	writeFileT(t, file, "one\nCHANGED\n")
	second := filepath.Join(repo, "g.txt")
	writeFileT(t, second, "brand new\n")

	a := newTestApp(t, repo)
	a.refreshGitStatus()
	a.menuToggleGitPanel()
	if len(a.gitPanel.files) != 2 {
		t.Fatalf("panel files = %+v, want f.txt and g.txt", a.gitPanel.files)
	}
	a.gitPanelSelectAll()

	// Pump on the PANEL's view of the world, not git's: the command
	// finishes before the done-event's refresh lands, and the next set of
	// action rows is built from the refreshed codes.
	allStaged := func(want gitStageState) func() bool {
		return func() bool {
			for _, f := range a.gitPanel.files {
				if gitPanelStageState(f.Code) != want {
					return false
				}
			}
			return len(a.gitPanel.files) == 2
		}
	}

	actionItem(t, a.gitPanelActionItems(a.gitPanelTargets()), "Stage 2 files").run(a)
	pumpAppEvents(t, a, allStaged(stageFull))
	if staged := gitOut(t, repo, "diff", "--cached", "--name-only"); staged != "f.txt\ng.txt" {
		t.Fatalf("staged files = %q, want both", staged)
	}

	actionItem(t, a.gitPanelActionItems(a.gitPanelTargets()), "Unstage 2 files").run(a)
	pumpAppEvents(t, a, allStaged(stageNone))
	if staged := gitOut(t, repo, "diff", "--cached", "--name-only"); staged != "" {
		t.Fatalf("staged files after unstage = %q, want none", staged)
	}
	content, err := os.ReadFile(file)
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	if string(content) != "one\nCHANGED\n" {
		t.Fatalf("work-tree content = %q — unstage must not revert the edit", content)
	}
}

// TestGitPanelActions_DiscardResetsToHead pins the destructive verb: it
// confirms first, then puts the file back to its committed content —
// clearing the staged copy as well as the work-tree edit, which is what
// a reviewer means by "discard".
func TestGitPanelActions_DiscardResetsToHead(t *testing.T) {
	if !gitAvailable() {
		t.Skip("git not on PATH")
	}
	repo, file := panelRepo(t)
	writeFileT(t, file, "one\nCHANGED\n")
	gitRun(t, repo, "add", ".")

	a := newTestApp(t, repo)
	a.refreshGitStatus()
	a.menuToggleGitPanel()

	actionItem(t, a.gitPanelActionItems(a.gitPanelTargets()), "Discard changes in f.txt").run(a)
	m := confirmOf(a)
	if m == nil {
		t.Fatalf("Discard opened %T, want a confirm modal", a.modal)
	}
	if !strings.Contains(m.message, "f.txt") {
		t.Errorf("confirm body doesn't name the target: %q", m.message)
	}
	m.yes(a)

	pumpAppEvents(t, a, func() bool {
		got, err := os.ReadFile(file)
		return err == nil && string(got) == "one\ntwo\n"
	})
	if staged := gitOut(t, repo, "diff", "--cached", "--name-only"); staged != "" {
		t.Fatalf("staged after discard = %q, want the index clean too", staged)
	}
}

// TestGitPanelActions_DeleteRemovesAndClosesTabs pins the delete verb:
// every ticked file leaves disk in one pass and any tab backed by one
// closes with it, so no buffer is left pointing at nothing.
func TestGitPanelActions_DeleteRemovesAndClosesTabs(t *testing.T) {
	dir := t.TempDir()
	one := filepath.Join(dir, "one.txt")
	two := filepath.Join(dir, "two.txt")
	writeFileT(t, one, "1\n")
	writeFileT(t, two, "2\n")

	a := newTestApp(t, dir)
	a.openFile(one)
	a.gitPanel.open = true
	a.gitPanel.files = []gitPanelFile{
		{Path: one, Rel: "one.txt", Code: "??"},
		{Path: two, Rel: "two.txt", Code: "??"},
	}
	a.gitPanelSelectAll()

	actionItem(t, a.gitPanelActionItems(a.gitPanelTargets()), "Delete 2 files").run(a)
	m := confirmOf(a)
	if m == nil {
		t.Fatalf("Delete opened %T, want a confirm modal", a.modal)
	}
	m.yes(a)

	for _, p := range []string{one, two} {
		if _, err := os.Stat(p); !os.IsNotExist(err) {
			t.Errorf("%s still on disk after delete", filepath.Base(p))
		}
	}
	if len(a.tabs) != 0 {
		t.Fatalf("tabs = %d, want the deleted file's tab closed", len(a.tabs))
	}
	if !strings.Contains(a.statusMsg, "Deleted") {
		t.Fatalf("status flash = %q, want a Deleted confirmation", a.statusMsg)
	}
}

// TestGitPanelDoDelete_ReportsPartialFailure pins the "finish the set"
// rule: one unremovable path doesn't abort the rest, and the flash says
// how many made it.
func TestGitPanelDoDelete_ReportsPartialFailure(t *testing.T) {
	dir := t.TempDir()
	good := filepath.Join(dir, "good.txt")
	writeFileT(t, good, "x\n")
	missing := filepath.Join(dir, "nested", "missing.txt")

	a := newTestApp(t, dir)
	a.gitPanelDoDelete([]string{missing, good}, "2 files")

	if _, err := os.Stat(good); !os.IsNotExist(err) {
		t.Error("a failure on the first path must not skip the rest")
	}
	if !strings.Contains(a.statusMsg, "1 failed") {
		t.Fatalf("status flash = %q, want the failure count", a.statusMsg)
	}
}

// TestGitPanelOpenFiles_OpensTabs pins the Open verb: each target lands
// as a tab, the last one active — the same end state as clicking them
// one by one in the tree.
func TestGitPanelOpenFiles_OpensTabs(t *testing.T) {
	dir := t.TempDir()
	one := filepath.Join(dir, "one.txt")
	two := filepath.Join(dir, "two.txt")
	writeFileT(t, one, "1\n")
	writeFileT(t, two, "2\n")

	a := newTestApp(t, dir)
	a.gitPanelOpenFiles([]gitPanelFile{
		{Path: one, Rel: "one.txt"},
		{Path: two, Rel: "two.txt"},
	})
	if len(a.tabs) != 2 {
		t.Fatalf("tabs = %d, want 2", len(a.tabs))
	}
	if tab := a.activeTabPtr(); tab == nil || tab.Path != two {
		t.Fatalf("active tab = %+v, want the last opened file", tab)
	}
}

// TestGitPanelCopyPaths_Flash pins the feedback contract shared with the
// other clipboard actions: OSC 52 is invisible from inside the TUI, so
// the user must always get a status line — success or failure.
func TestGitPanelCopyPaths_Flash(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	a.gitPanelCopyPaths([]gitPanelFile{{Path: "/p/a.go", Rel: "a.go"}})
	// Either outcome is fine here — CI may have no usable /dev/tty. The
	// contract is "the user hears about it", and that the flash names
	// the path when it worked.
	if a.statusMsg == "" {
		t.Fatal("expected a status flash after copy")
	}
	if !strings.Contains(a.statusMsg, "a.go") && !strings.Contains(a.statusMsg, "Copy failed") {
		t.Fatalf("status flash = %q, want the path or an error", a.statusMsg)
	}

	a.statusMsg = ""
	a.gitPanelCopyPaths([]gitPanelFile{
		{Path: "/p/a.go", Rel: "a.go"},
		{Path: "/p/b.go", Rel: "b.go"},
	})
	if !strings.Contains(a.statusMsg, "2 paths") && !strings.Contains(a.statusMsg, "Copy failed") {
		t.Fatalf("multi-copy flash = %q, want a count or an error", a.statusMsg)
	}

	// A no-op set must stay silent rather than flashing "Copied 0 paths".
	a.statusMsg = ""
	a.gitPanelCopyPaths(nil)
	if a.statusMsg != "" {
		t.Fatalf("empty copy flashed %q, want silence", a.statusMsg)
	}
}
