// =============================================================================
// File: internal/app/gitconflict_test.go
// Author: Rohan Allison <rohanthewiz@gmail.com>
// Created: 2026-08-12
// Copyright: 2026 Rohan Allison. All rights reserved.
// =============================================================================

// Tests for the conflict resolver: the operation-detection table (pure
// stats against a fake git dir), the marker scan and its fail-safe
// answers, the picker's row shapes for every state a conflict can be in,
// and one end-to-end pass over a real cherry-pick conflict — the part
// where git's actual behaviour, not our model of it, is the thing under
// test.

package app

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestGitOpFromGitDir pins the detection table, including the ordering
// that matters: an interactive rebase paused on a cherry-pick leaves
// BOTH markers, and answering "cherry-pick" there would offer a
// --continue that advances one commit and strands the rebase.
func TestGitOpFromGitDir(t *testing.T) {
	cases := []struct {
		name    string
		markers []string
		dirs    []string
		want    string
	}{
		{name: "clean", want: ""},
		{name: "cherry-pick", markers: []string{"CHERRY_PICK_HEAD"}, want: "cherry-pick"},
		{name: "revert", markers: []string{"REVERT_HEAD"}, want: "revert"},
		{name: "merge", markers: []string{"MERGE_HEAD"}, want: "merge"},
		{name: "rebase (merge backend)", dirs: []string{"rebase-merge"}, want: "rebase"},
		{name: "rebase (apply backend)", dirs: []string{"rebase-apply"}, want: "rebase"},
		{
			name:    "rebase paused on a cherry-pick",
			markers: []string{"CHERRY_PICK_HEAD"},
			dirs:    []string{"rebase-merge"},
			want:    "rebase",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			for _, m := range tc.markers {
				writeFileT(t, filepath.Join(dir, m), "ref\n")
			}
			for _, d := range tc.dirs {
				if err := os.Mkdir(filepath.Join(dir, d), 0o755); err != nil {
					t.Fatalf("mkdir %s: %v", d, err)
				}
			}
			if got := gitOpFromGitDir(dir); got != tc.want {
				t.Errorf("op = %q, want %q", got, tc.want)
			}
		})
	}
	if got := gitOpFromGitDir(""); got != "" {
		t.Errorf("empty git dir = %q, want empty", got)
	}
}

// TestGitConflictContinueArgs pins the argv AND the absence of the flag
// everyone reaches for: `--continue --no-edit` is a usage error in git,
// so the editor problem has to be solved in the environment instead.
func TestGitConflictContinueArgs(t *testing.T) {
	for _, op := range []string{"cherry-pick", "revert", "merge", "rebase"} {
		got := gitConflictContinueArgs(op)
		want := []string{op, "--continue"}
		if len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
			t.Errorf("%s argv = %v, want %v", op, got, want)
		}
	}
}

// TestGitNoEditorEnv pins both halves of the override: the variable git
// actually consults, and the inherited environment it must be layered
// ON TOP OF — a bare []string{"GIT_EDITOR=true"} would run git with no
// PATH and no HOME.
func TestGitNoEditorEnv(t *testing.T) {
	t.Setenv("CED_CONFLICT_ENV_PROBE", "kept")
	env := gitNoEditorEnv()
	var sawEditor, sawInherited bool
	for _, kv := range env {
		switch kv {
		case "GIT_EDITOR=true":
			sawEditor = true
		case "CED_CONFLICT_ENV_PROBE=kept":
			sawInherited = true
		}
	}
	if !sawEditor {
		t.Error("GIT_EDITOR=true missing — git would try to open $EDITOR over the TUI")
	}
	if !sawInherited {
		t.Error("inherited environment dropped — git would run without PATH/HOME")
	}
}

// TestFileHasConflictMarkers pins the scan and, more importantly, its
// fail-safe direction: anything it cannot read answers "still has
// markers", because the caller uses this to decide what is safe to
// `git add` and a wrong "no" commits <<<<<<< into the history.
func TestFileHasConflictMarkers(t *testing.T) {
	dir := t.TempDir()

	clean := filepath.Join(dir, "clean.txt")
	writeFileT(t, clean, "one\ntwo\nthree\n")
	if fileHasConflictMarkers(clean) {
		t.Error("a resolved file reported as still conflicted")
	}

	marked := filepath.Join(dir, "marked.txt")
	writeFileT(t, marked, "one\n<<<<<<< HEAD\nmine\n=======\ntheirs\n>>>>>>> abc\n")
	if !fileHasConflictMarkers(marked) {
		t.Error("a file full of markers reported as resolved")
	}

	// The marker must start the line — "<<<<<<<" quoted mid-sentence in
	// prose or a string literal is not a conflict.
	inline := filepath.Join(dir, "inline.txt")
	writeFileT(t, inline, "the marker is \"<<<<<<<\" and it starts a line\n")
	if fileHasConflictMarkers(inline) {
		t.Error("a mid-line marker was read as a conflict")
	}

	if !fileHasConflictMarkers(filepath.Join(dir, "gone.txt")) {
		t.Error("an unreadable file must answer conservatively (true)")
	}
}

// TestGitConflictReady pins the split the stage row is built from.
func TestGitConflictReady(t *testing.T) {
	root := t.TempDir()
	writeFileT(t, filepath.Join(root, "done.txt"), "resolved\n")
	writeFileT(t, filepath.Join(root, "todo.txt"), "<<<<<<< HEAD\n")

	ready, pending := gitConflictReady(root, []string{"done.txt", "todo.txt"})
	if len(ready) != 1 || ready[0] != "done.txt" {
		t.Errorf("ready = %v, want [done.txt]", ready)
	}
	if len(pending) != 1 || pending[0] != "todo.txt" {
		t.Errorf("pending = %v, want [todo.txt]", pending)
	}
}

// TestGitConflictTitle pins the state the picker announces before any
// row is read.
func TestGitConflictTitle(t *testing.T) {
	cases := []struct {
		op   string
		n    int
		want string
	}{
		{"cherry-pick", 2, "Cherry-pick conflict · 2 files"},
		{"cherry-pick", 1, "Cherry-pick conflict · 1 file"},
		{"rebase", 0, "Rebase conflict · nothing unmerged"},
		{"", 3, "Conflict · 3 files"},
	}
	for _, tc := range cases {
		if got := gitConflictTitle(tc.op, tc.n); got != tc.want {
			t.Errorf("title(%q, %d) = %q, want %q", tc.op, tc.n, got, tc.want)
		}
	}
}

// TestGitConflictItems_SafetyGradient is the ordering contract: the row
// under the default highlight can only ever OPEN something, and the
// irreversible verb is last. A picker that opened on "Abort" would turn
// a reflex Enter into thrown-away work.
func TestGitConflictItems_SafetyGradient(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	root := t.TempDir()
	writeFileT(t, filepath.Join(root, "a.txt"), "<<<<<<< HEAD\n")
	writeFileT(t, filepath.Join(root, "b.txt"), "<<<<<<< HEAD\n")

	items := a.gitConflictItems("cherry-pick", root, []string{"a.txt", "b.txt"})
	if len(items) == 0 {
		t.Fatal("no rows built")
	}
	if !strings.HasPrefix(items[0].label, "Open ") {
		t.Errorf("first row = %q, want an Open row", items[0].label)
	}
	last := items[len(items)-1].label
	if !strings.HasPrefix(last, "Abort ") {
		t.Errorf("last row = %q, want the Abort row", last)
	}
	// Every file gets its own row, so the picker answers "which files?".
	if !hasRowLabel(items, "Open a.txt") || !hasRowLabel(items, "Open b.txt") {
		t.Errorf("per-file rows missing: %v", rowLabels(items))
	}
	// Nothing is stageable while both still carry markers, and there is
	// nothing to continue while both are unmerged.
	for _, label := range rowLabels(items) {
		if strings.HasPrefix(label, "Stage ") || strings.HasPrefix(label, "Mark ") {
			t.Errorf("offered %q with no resolved file", label)
		}
		if strings.HasPrefix(label, "Continue ") {
			t.Errorf("offered %q with unmerged paths — git would only error", label)
		}
	}
}

// TestGitConflictItems_ResolutionRows walks the three resolution states:
// partly resolved gets a plain stage row that says what is holding it
// up, fully resolved collapses staging and continuing into one gesture,
// and a clean index gets the bare Continue.
func TestGitConflictItems_ResolutionRows(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	root := t.TempDir()
	writeFileT(t, filepath.Join(root, "done.txt"), "resolved\n")
	writeFileT(t, filepath.Join(root, "todo.txt"), "<<<<<<< HEAD\n")

	partly := rowLabels(a.gitConflictItems("cherry-pick", root, []string{"done.txt", "todo.txt"}))
	if !hasLabel(partly, "Stage 1 resolved file (1 file still has markers)") {
		t.Errorf("partly-resolved rows = %v", partly)
	}

	all := rowLabels(a.gitConflictItems("cherry-pick", root, []string{"done.txt"}))
	if !hasLabel(all, "Mark all resolved and continue the cherry-pick") {
		t.Errorf("fully-resolved rows = %v", all)
	}

	clean := rowLabels(a.gitConflictItems("merge", root, nil))
	if !hasLabel(clean, "Continue the merge") || !hasLabel(clean, "Abort the merge…") {
		t.Errorf("clean-index rows = %v", clean)
	}
	for _, l := range clean {
		if strings.HasPrefix(l, "Open ") {
			t.Errorf("offered %q with nothing unmerged", l)
		}
	}
}

// TestGitConflictItems_NoOperation covers the conflict ced did not
// start — a `stash pop` leaves unmerged paths with no operation to
// continue or abort, and offering either would offer an error.
func TestGitConflictItems_NoOperation(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	root := t.TempDir()
	writeFileT(t, filepath.Join(root, "a.txt"), "resolved\n")

	labels := rowLabels(a.gitConflictItems("", root, []string{"a.txt"}))
	if !hasLabel(labels, "Open a.txt") {
		t.Errorf("rows = %v, want the open row", labels)
	}
	if !hasLabel(labels, "Stage 1 resolved file") {
		t.Errorf("rows = %v, want the stage row (resolving IS the whole job here)", labels)
	}
	for _, l := range labels {
		if strings.HasPrefix(l, "Continue ") || strings.HasPrefix(l, "Abort ") ||
			strings.HasPrefix(l, "Mark all ") {
			t.Errorf("offered %q with no operation in progress", l)
		}
	}
}

// TestGitConflictItems_CapIsAnnounced pins the no-silent-caps rule: past
// gitConflictListMax the per-file rows stop, and the bulk row SAYS it is
// only opening a prefix rather than passing a short list off as all of
// them.
func TestGitConflictItems_CapIsAnnounced(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	root := t.TempDir()
	rels := make([]string, gitConflictListMax+5)
	for i := range rels {
		rels[i] = "f" + itoa(i) + ".txt"
		writeFileT(t, filepath.Join(root, rels[i]), "<<<<<<< HEAD\n")
	}

	items := a.gitConflictItems("rebase", root, rels)
	openRows := 0
	for _, l := range rowLabels(items) {
		if strings.HasPrefix(l, "Open f") {
			openRows++
		}
	}
	if openRows != gitConflictListMax {
		t.Errorf("%d per-file rows, want the cap of %d", openRows, gitConflictListMax)
	}
	if !strings.Contains(items[0].label, "first "+itoa(gitConflictListMax)) ||
		!strings.Contains(items[0].label, itoa(len(rels))) {
		t.Errorf("bulk row = %q, must name both the cap and the true total", items[0].label)
	}
}

// TestGitConflictAddArgs_AbsolutePaths pins the fix for the case that
// breaks silently: ced runs git with `-C rootDir`, and rootDir is
// whatever folder was opened — often a SUBDIRECTORY of the repo. git
// reports unmerged paths relative to the work-tree root, so passing
// them through verbatim would resolve them against the wrong base.
func TestGitConflictAddArgs_AbsolutePaths(t *testing.T) {
	args := gitConflictAddArgs("/repo", []string{"internal/app/f.go", "-weird.go"})
	if args[0] != "add" || args[1] != "--" {
		t.Fatalf("argv starts %v, want add then the -- fence", args[:2])
	}
	// The fence is what keeps a path beginning with a dash a path.
	if len(args) != 4 {
		t.Fatalf("argv = %v, want two paths behind the fence", args)
	}
	for _, p := range args[2:] {
		if !filepath.IsAbs(p) {
			t.Errorf("path %q is relative — it would resolve against rootDir, not the work tree", p)
		}
	}
	if args[2] != filepath.Join("/repo", "internal/app/f.go") {
		t.Errorf("path = %q, want it rooted at the toplevel", args[2])
	}
}

// TestGitConflictStage_FromRepoSubdirectory is the same fix end to end:
// with the project root one level down, marking a conflict resolved must
// still reach the right file.
func TestGitConflictStage_FromRepoSubdirectory(t *testing.T) {
	requireGit(t)
	repo := initRepo(t)
	if err := os.Mkdir(filepath.Join(repo, "sub"), 0o755); err != nil {
		t.Fatalf("mkdir sub: %v", err)
	}
	writeCommit(t, repo, "sub/f.txt", "one\ntwo\nthree\n", "base")
	gitRun(t, repo, "checkout", "-q", "-b", "side")
	writeCommit(t, repo, "sub/f.txt", "one\nSIDE\nthree\n", "side")
	gitRun(t, repo, "checkout", "-q", "main")
	writeCommit(t, repo, "sub/f.txt", "one\nMAIN\nthree\n", "main")
	gitRunAllowFail(t, repo, "cherry-pick", "side")

	// The opened folder is the SUBDIRECTORY, which is what every git
	// command below will run with -C.
	sub := filepath.Join(repo, "sub")
	root, rels := gitConflictedPaths(sub)
	if len(rels) != 1 || rels[0] != "sub/f.txt" {
		t.Fatalf("unmerged = %v, want [sub/f.txt] (relative to the work tree, not the cwd)", rels)
	}

	writeFileT(t, filepath.Join(repo, "sub", "f.txt"), "one\nBOTH\nthree\n")
	cmd := gitCmdT(t, sub, gitConflictAddArgs(root, rels)...)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git add from a subdirectory failed: %v\n%s", err, out)
	}
	if _, rels = gitConflictedPaths(sub); len(rels) != 0 {
		t.Errorf("the file is still unmerged after staging: %v", rels)
	}
}

// TestGitConflictAbort_Confirms pins the gate on the one row here that
// destroys work, and that the confirm names the operation it will
// abandon.
func TestGitConflictAbort_Confirms(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	a.gitConflictAbort("cherry-pick")
	m, ok := a.modal.(*confirmModal)
	if !ok {
		t.Fatalf("abort opened %T, want a confirm", a.modal)
	}
	body := strings.Join(m.lines, " ")
	if !strings.Contains(body, "cherry-pick") {
		t.Errorf("confirm body = %q, should name the operation", body)
	}
	if !strings.Contains(body, "lost") {
		t.Errorf("confirm body = %q, should say resolutions are lost", body)
	}
	// Both lines must survive the 50-cell body slot — a warning cut off
	// mid-sentence is the failure mode this whole dialog exists to avoid.
	for _, line := range m.lines {
		if runeLen(line) > confirmModalWidth-4 {
			t.Errorf("confirm line %q is %d cells, over the %d-cell body slot",
				line, runeLen(line), confirmModalWidth-4)
		}
	}
}

// TestHasGitConflict gates the ≡ row off the snapshot's unmerged set —
// never lit for a clean repo, never lit outside one.
func TestHasGitConflict(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	if a.hasGitConflict() {
		t.Error("non-repo should not enable the row")
	}
	a.gitIsRepo = true
	if a.hasGitConflict() {
		t.Error("clean repo should not enable the row")
	}
	a.gitConflicted = map[string]bool{"/tmp/f.txt": true}
	if !a.hasGitConflict() {
		t.Error("unmerged paths should enable the row")
	}
}

// TestOpenGitConflictPicker_NothingToResolve pins the empty case: a
// clean repo flashes instead of opening a picker with no rows.
func TestOpenGitConflictPicker_NothingToResolve(t *testing.T) {
	requireGit(t)
	repo := initRepo(t)
	writeCommit(t, repo, "f.txt", "one\n", "first")

	a := newTestApp(t, repo)
	a.rootDir = repo
	a.openGitConflictPicker()
	if a.modal != nil {
		t.Fatalf("picker opened on a clean repo: %T", a.modal)
	}
	if !strings.Contains(a.statusMsg, "No conflicted") {
		t.Errorf("flash = %q, want a no-conflict notice", a.statusMsg)
	}
}

// TestGitConflictFailHook_DeclinesWhenNothingParked pins the hook's
// half of the bargain: it claims a failure ONLY when the repo is left
// mid-operation, so "bad revision" and "your local changes would be
// overwritten" still reach the error modal with git's own words.
func TestGitConflictFailHook_DeclinesWhenNothingParked(t *testing.T) {
	requireGit(t)
	repo := initRepo(t)
	writeCommit(t, repo, "f.txt", "one\n", "first")

	a := newTestApp(t, repo)
	a.rootDir = repo
	if gitConflictFailHook(a, &gitCmdDoneEvent{label: "Cherry-pick abc"}) {
		t.Error("hook claimed a failure that left no operation in progress")
	}
	if a.modal != nil {
		t.Errorf("hook opened %T instead of standing down", a.modal)
	}
}

// TestGitConflictFailHook_OpensPickerOnRealConflict is the automatic
// path, against a real stopped cherry-pick: the hook claims the failure,
// the picker names the operation and the file, and the row it offers
// first actually opens that file as a tab — the point of the whole
// feature being that the conflict is visible in the editor rather than
// described in a modal.
func TestGitConflictFailHook_OpensPickerOnRealConflict(t *testing.T) {
	requireGit(t)
	repo := initRepo(t)
	writeCommit(t, repo, "f.txt", "one\ntwo\nthree\n", "base")
	gitRun(t, repo, "checkout", "-q", "-b", "side")
	writeCommit(t, repo, "f.txt", "one\nSIDE\nthree\n", "side edits the middle")
	gitRun(t, repo, "checkout", "-q", "main")
	writeCommit(t, repo, "f.txt", "one\nMAIN\nthree\n", "main edits the middle")
	gitRunAllowFail(t, repo, "cherry-pick", "side")

	a := newTestApp(t, repo)
	a.rootDir = repo
	if !gitConflictFailHook(a, &gitCmdDoneEvent{label: "Cherry-pick side"}) {
		t.Fatal("hook declined a failure that left a cherry-pick parked")
	}
	m, ok := a.modal.(*paletteModal)
	if !ok {
		t.Fatalf("hook opened %T, want the conflict picker", a.modal)
	}
	if !strings.Contains(m.title, "Cherry-pick conflict") || !strings.Contains(m.title, "1 file") {
		t.Errorf("picker title = %q, want the operation and the count", m.title)
	}
	if !hasRowLabel(m.items, "Open f.txt") {
		t.Errorf("rows = %v, want an Open row for the conflicted file", rowLabels(m.items))
	}
	// The dirty colors and gutters describe a work tree git has just
	// rewritten, so the failure path must refresh rather than return.
	if !a.gitConflicted[filepath.Join(repo, "f.txt")] {
		t.Errorf("snapshot not refreshed after the conflict: %v", a.gitConflicted)
	}

	m.items[0].run(a)
	tab := a.activeTabPtr()
	if tab == nil || tab.Path != filepath.Join(repo, "f.txt") {
		t.Fatalf("open row left the active tab at %v, want f.txt", tab)
	}
	if !strings.Contains(strings.Join(tab.Buffer.Lines, "\n"), "<<<<<<<") {
		t.Error("the opened tab does not show git's conflict markers")
	}
}

// TestGitConflict_EndToEnd is the one test that lets git be the
// authority. It builds a real cherry-pick conflict and walks the whole
// path: detection, the unmerged list, the marker scan flipping once the
// file is fixed, and finally the argv+environment pair actually
// finishing the cherry-pick — which is where a wrong --continue
// spelling or a lost GIT_EDITOR would surface.
func TestGitConflict_EndToEnd(t *testing.T) {
	requireGit(t)
	repo := initRepo(t)
	writeCommit(t, repo, "f.txt", "one\ntwo\nthree\n", "base")
	gitRun(t, repo, "checkout", "-q", "-b", "side")
	writeCommit(t, repo, "f.txt", "one\nSIDE\nthree\n", "side edits the middle")
	gitRun(t, repo, "checkout", "-q", "main")
	writeCommit(t, repo, "f.txt", "one\nMAIN\nthree\n", "main edits the middle")

	// The cherry-pick is expected to FAIL — that is the fixture.
	gitRunAllowFail(t, repo, "cherry-pick", "side")

	if op := gitInProgressOp(repo); op != "cherry-pick" {
		t.Fatalf("in-progress op = %q, want cherry-pick", op)
	}
	root, rels := gitConflictedPaths(repo)
	if root != repo {
		t.Errorf("toplevel = %q, want %q", root, repo)
	}
	if len(rels) != 1 || rels[0] != "f.txt" {
		t.Fatalf("unmerged = %v, want [f.txt]", rels)
	}
	if _, pending := gitConflictReady(root, rels); len(pending) != 1 {
		t.Errorf("git's own conflict markers were not detected: pending = %v", pending)
	}

	// Resolve, and watch the scan flip.
	writeFileT(t, filepath.Join(repo, "f.txt"), "one\nBOTH\nthree\n")
	ready, pending := gitConflictReady(root, rels)
	if len(ready) != 1 || len(pending) != 0 {
		t.Fatalf("after resolving: ready=%v pending=%v", ready, pending)
	}

	// Stage-then-continue, run exactly as gitConflictStageAndContinue
	// spells it — argv from the helper, environment from the helper.
	gitRun(t, repo, "add", "--", "f.txt")
	cmd := gitCmdT(t, repo, gitConflictContinueArgs("cherry-pick")...)
	cmd.Env = gitNoEditorEnv()
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("cherry-pick --continue failed: %v\n%s", err, out)
	}

	if op := gitInProgressOp(repo); op != "" {
		t.Errorf("operation still in progress after --continue: %q", op)
	}
	if _, rels = gitConflictedPaths(repo); len(rels) != 0 {
		t.Errorf("unmerged paths survive the continue: %v", rels)
	}
}

// -----------------------------------------------------------------------------
// Helpers
// -----------------------------------------------------------------------------

// rowLabels lifts the labels out of a picker row list for assertions
// that care about the SET of rows rather than their closures.
func rowLabels(items []paletteItem) []string {
	out := make([]string, 0, len(items))
	for _, it := range items {
		out = append(out, it.label)
	}
	return out
}

// hasLabel reports whether any label starts with want — prefix rather
// than equality so a test can pin the part of a row that matters
// without restating a parenthetical it doesn't care about.
func hasLabel(labels []string, want string) bool {
	for _, l := range labels {
		if strings.HasPrefix(l, want) {
			return true
		}
	}
	return false
}

// hasRowLabel is hasLabel over rows, for exact-match cases.
func hasRowLabel(items []paletteItem, want string) bool {
	for _, it := range items {
		if it.label == want {
			return true
		}
	}
	return false
}

// gitRunAllowFail is gitRun for the one fixture that NEEDS git to fail:
// the conflicting cherry-pick this file is all about. gitRun would call
// the expected non-zero exit a broken fixture and stop the test.
func gitRunAllowFail(t *testing.T, cwd string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = cwd
	_, _ = cmd.CombinedOutput()
}

// gitCmdT builds an unexecuted git command against repo, so a test can
// set the environment before running it — the whole point being to run
// --continue exactly the way gitConflictStageAndContinue does.
func gitCmdT(t *testing.T, repo string, args ...string) *exec.Cmd {
	t.Helper()
	return exec.Command("git", append([]string{"-C", repo}, args...)...)
}
