// =============================================================================
// File: internal/app/gitstatus_test.go
// Author: Rohan Allison <rohanthewiz@gmail.com>
// Created: 2026-04-30
// Copyright: 2026 Rohan Allison. All rights reserved.
// Portions copyright 2026 Cloudmanic, LLC. Original author: Spicer Matthews.
// =============================================================================

// Tests for gitstatus.go. The byte-parsing helpers (parsePorcelainV2,
// pathSets, dirtyFolderSet, pathInside) are exercised in isolation
// with synthetic input — no subprocess needed. The shell-out flow
// (loadGitStatus end-to-end) is exercised against a real `git init`'d
// repo in a t.TempDir, and skipped when git isn't on PATH so the test
// suite still runs in a stripped-down container.

package app

import (
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// TestLoadGitStatus_NotARepo verifies that pointing the loader at a
// directory that isn't tracked by git returns the zero-value gitStatus —
// the editor should silently skip its dirty highlight rather than
// erroring out when run inside a plain folder.
func TestLoadGitStatus_NotARepo(t *testing.T) {
	dir := t.TempDir()
	st := loadGitStatus(dir)
	if st.IsRepo {
		t.Fatalf("plain dir should not report as repo, got %+v", st)
	}
	if st.DirtyFiles != nil {
		t.Fatalf("plain dir should have nil DirtyFiles, got %v", st.DirtyFiles)
	}
}

// TestLoadGitStatus_EmptyRoot guards the "" early-return so a fresh App
// (rootDir not yet set) can call refreshGitStatus without spawning git.
func TestLoadGitStatus_EmptyRoot(t *testing.T) {
	if st := loadGitStatus(""); st.IsRepo {
		t.Fatalf("empty rootDir should not report as repo, got %+v", st)
	}
}

// TestLoadGitStatus_CleanRepo runs the full pipeline against a freshly
// initialised, fully committed repo and confirms IsRepo flips on but the
// dirty set comes back empty — the renderer should treat clean files
// like any other, no Modified-color highlight. Also pins down that the
// branch name comes through populated.
func TestLoadGitStatus_CleanRepo(t *testing.T) {
	requireGit(t)
	repo := initRepo(t)
	writeFileT(t, filepath.Join(repo, "a.txt"), "hello")
	gitRun(t, repo, "add", "a.txt")
	gitRun(t, repo, "commit", "-m", "init")

	st := loadGitStatus(repo)
	if !st.IsRepo {
		t.Fatal("expected IsRepo=true on a real git repo")
	}
	if len(st.DirtyFiles) != 0 {
		t.Fatalf("expected no dirty files, got %v", st.DirtyFiles)
	}
	if st.Branch != "main" {
		t.Fatalf("expected Branch=main, got %q", st.Branch)
	}
}

// TestLoadGitBranch_NotARepo confirms the helper degrades quietly when
// the directory isn't a git work tree — empty string, no panic, no
// stderr noise reaching the editor.
func TestLoadGitBranch_NotARepo(t *testing.T) {
	if got := loadGitBranch(t.TempDir()); got != "" {
		t.Fatalf("non-repo branch = %q, want empty", got)
	}
	if got := loadGitBranch(""); got != "" {
		t.Fatalf("empty rootDir branch = %q, want empty", got)
	}
}

// TestLoadGitBranch_OnBranch checks the happy path — a fresh repo
// checked out on `main` returns "main".
func TestLoadGitBranch_OnBranch(t *testing.T) {
	requireGit(t)
	repo := initRepo(t)
	if got := loadGitBranch(repo); got != "main" {
		t.Fatalf("branch = %q, want main", got)
	}
}

// TestLoadGitBranch_TracksRename confirms a rename of the current
// branch is reflected on the next call — this is the whole point of
// the 10s tick: the user's checkout state is allowed to change behind
// the editor's back.
func TestLoadGitBranch_TracksRename(t *testing.T) {
	requireGit(t)
	repo := initRepo(t)
	writeFileT(t, filepath.Join(repo, "a.txt"), "x")
	gitRun(t, repo, "add", "a.txt")
	gitRun(t, repo, "commit", "-m", "init")
	gitRun(t, repo, "branch", "-m", "main", "feat/something")
	if got := loadGitBranch(repo); got != "feat/something" {
		t.Fatalf("after rename branch = %q, want feat/something", got)
	}
}

// TestLoadGitBranch_DetachedHEAD asserts the symbolic-ref fallback
// kicks in: when HEAD is detached at a commit, the helper returns a
// short SHA instead of an empty string, so the status bar still shows
// *something* useful instead of vanishing mid-rebase.
func TestLoadGitBranch_DetachedHEAD(t *testing.T) {
	requireGit(t)
	repo := initRepo(t)
	writeFileT(t, filepath.Join(repo, "a.txt"), "x")
	gitRun(t, repo, "add", "a.txt")
	gitRun(t, repo, "commit", "-m", "init")
	gitRun(t, repo, "checkout", "-q", "--detach", "HEAD")

	got := loadGitBranch(repo)
	if got == "" {
		t.Fatal("detached HEAD branch came back empty; expected a short SHA")
	}
	if got == "main" {
		t.Fatalf("detached HEAD reported branch name %q; expected SHA", got)
	}
	if len(got) > 12 || len(got) < 4 {
		t.Fatalf("detached HEAD output %q doesn't look like a short SHA", got)
	}
}

// TestLoadGitStatus_FindsModifiedAndUntracked seeds a repo with one
// committed file (later modified), one brand-new untracked file, and
// one staged-but-uncommitted file. All three should show up as dirty,
// indexed by absolute path so the file tree's path-keyed lookup hits.
func TestLoadGitStatus_FindsModifiedAndUntracked(t *testing.T) {
	requireGit(t)
	repo := initRepo(t)

	writeFileT(t, filepath.Join(repo, "tracked.txt"), "v1")
	gitRun(t, repo, "add", "tracked.txt")
	gitRun(t, repo, "commit", "-m", "init")

	// Modify the tracked file (worktree change).
	writeFileT(t, filepath.Join(repo, "tracked.txt"), "v2")
	// Brand-new untracked file.
	writeFileT(t, filepath.Join(repo, "untracked.txt"), "fresh")
	// Staged-but-uncommitted.
	writeFileT(t, filepath.Join(repo, "staged.txt"), "added")
	gitRun(t, repo, "add", "staged.txt")

	st := loadGitStatus(repo)
	if !st.IsRepo {
		t.Fatal("expected IsRepo=true")
	}
	for _, want := range []string{"tracked.txt", "untracked.txt", "staged.txt"} {
		abs := filepath.Join(repo, want)
		if !st.DirtyFiles[abs] {
			t.Errorf("expected %s to be dirty; got %v", want, sortedKeys(st.DirtyFiles))
		}
	}
}

// TestLoadGitStatus_FromSubdirectory makes sure the loader works when
// the editor was launched against a subdirectory of the repo, not the
// repo root. rev-parse --show-toplevel resolves the real top, and dirty
// paths still come back as absolute — even files outside the working
// rootDir but inside the repo.
func TestLoadGitStatus_FromSubdirectory(t *testing.T) {
	requireGit(t)
	repo := initRepo(t)
	sub := filepath.Join(repo, "deep", "dir")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	writeFileT(t, filepath.Join(sub, "inside.txt"), "x")
	writeFileT(t, filepath.Join(repo, "outside.txt"), "y")
	gitRun(t, repo, "add", ".")
	gitRun(t, repo, "commit", "-m", "init")

	// Mutate both files so they appear dirty.
	writeFileT(t, filepath.Join(sub, "inside.txt"), "x2")
	writeFileT(t, filepath.Join(repo, "outside.txt"), "y2")

	st := loadGitStatus(sub)
	if !st.IsRepo {
		t.Fatal("subdirectory of a repo should still register as a repo")
	}
	for _, want := range []string{
		filepath.Join(sub, "inside.txt"),
		filepath.Join(repo, "outside.txt"),
	} {
		if !st.DirtyFiles[want] {
			t.Errorf("expected %s to be dirty; got %v", want, sortedKeys(st.DirtyFiles))
		}
	}
}

// TestDirtyFolderSet_RollsUpToRoot verifies that each dirty file paints
// every ancestor folder up to (and including) the project root, so a
// collapsed branch still shows the user there's a change inside.
func TestDirtyFolderSet_RollsUpToRoot(t *testing.T) {
	root := "/proj"
	dirty := map[string]bool{
		"/proj/a/b/c/leaf.txt": true,
		"/proj/x/y.txt":        true,
	}
	got := dirtyFolderSet(dirty, root)

	want := []string{
		"/proj",
		"/proj/a",
		"/proj/a/b",
		"/proj/a/b/c",
		"/proj/x",
	}
	for _, w := range want {
		if !got[w] {
			t.Errorf("expected %q to be marked dirty; got %v", w, sortedKeys(got))
		}
	}
	// The leaf file path itself isn't a folder, must not appear here.
	if got["/proj/a/b/c/leaf.txt"] {
		t.Error("dirtyFolderSet should not contain file paths")
	}
}

// TestDirtyFolderSet_StopsAtRoot proves the walk stops at root rather
// than continuing all the way to "/", so a sibling project directory
// or the user's home directory can't be marked dirty by us.
func TestDirtyFolderSet_StopsAtRoot(t *testing.T) {
	root := "/proj/inner"
	dirty := map[string]bool{
		"/proj/inner/a/b.txt": true,
	}
	got := dirtyFolderSet(dirty, root)
	for _, ancestor := range []string{"/proj", "/", "/home"} {
		if got[ancestor] {
			t.Errorf("walk escaped root: %q should not be marked", ancestor)
		}
	}
	if !got["/proj/inner"] {
		t.Error("root itself should be marked when something inside is dirty")
	}
	if !got["/proj/inner/a"] {
		t.Error("intermediate folder should be marked")
	}
}

// TestDirtyFolderSet_EmptyInput returns an empty (non-nil) map so
// callers can safely range over the result without nil-checking.
func TestDirtyFolderSet_EmptyInput(t *testing.T) {
	got := dirtyFolderSet(nil, "/anywhere")
	if got == nil {
		t.Fatal("expected non-nil empty map")
	}
	if len(got) != 0 {
		t.Fatalf("expected empty map, got %v", got)
	}
}

// TestPathInside covers the core ancestry check used by dirtyFolderSet.
// Beyond the obvious matches, the prefix-trick trap ("/foo/bar" is NOT
// inside "/foo/ba") is the regression we care most about.
func TestPathInside(t *testing.T) {
	cases := []struct {
		candidate, root string
		want            bool
	}{
		{"/foo", "/foo", true},
		{"/foo/bar", "/foo", true},
		{"/foo/bar/baz", "/foo", true},
		{"/foo/ba", "/foo/bar", false},
		{"/foo/bar", "/foo/ba", false}, // string-prefix would lie here
		{"/sibling", "/foo", false},
		{"/", "/foo", false},
		// A child whose NAME starts with two dots is inside (N-109): the
		// bare HasPrefix(rel, "..") test called "..x" outside.
		{"/foo/..x", "/foo", true},
		{"/foo/..x/y", "/foo", true},
		{"/..foo", "/foo", false}, // a sibling of /foo that merely starts with ..
	}
	for _, tc := range cases {
		if got := pathInside(tc.candidate, tc.root); got != tc.want {
			t.Errorf("pathInside(%q, %q) = %v, want %v", tc.candidate, tc.root, got, tc.want)
		}
	}
}

// TestRelEscapes pins the one "does this Rel answer leave its base?" test
// every call site shares: only a whole leading ".." segment escapes; a
// name that merely starts with two dots stays inside.
func TestRelEscapes(t *testing.T) {
	sep := string(filepath.Separator)
	cases := []struct {
		rel  string
		want bool
	}{
		{"..", true},
		{".." + sep + "x", true},
		{".." + sep + ".." + sep + "x", true},
		{"..x", false},
		{"..x" + sep + "y", false},
		{"...", false},
		{".", false},
		{"a" + sep + "..b", false},
		{"", false},
	}
	for _, tc := range cases {
		if got := relEscapes(tc.rel); got != tc.want {
			t.Errorf("relEscapes(%q) = %v, want %v", tc.rel, got, tc.want)
		}
	}
}

// -----------------------------------------------------------------------------
// helpers
// -----------------------------------------------------------------------------

// requireGit skips the calling test when git isn't on PATH. The encoding
// helpers don't need it; only the end-to-end flow does.
func requireGit(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available on PATH")
	}
}

// initRepo creates a fresh git repo in t.TempDir and configures a local
// committer identity so commits in the test don't depend on the host's
// global git config.
func initRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	gitRun(t, dir, "init", "-q")
	gitRun(t, dir, "config", "user.email", "test@example.com")
	gitRun(t, dir, "config", "user.name", "Test User")
	gitRun(t, dir, "config", "commit.gpgsign", "false")
	// macOS 'git init' may print a default-branch hint; force a stable name
	// so the tests work the same on every host.
	gitRun(t, dir, "checkout", "-q", "-b", "main")
	// On macOS the temp dir lives under /var, which is a symlink to
	// /private/var. git resolves the real path; rev-parse --show-toplevel
	// will report /private/var/... — tests use the same dir variable so
	// they compare the *resolved* path to itself. Force resolution here.
	resolved, err := filepath.EvalSymlinks(dir)
	if err != nil {
		t.Fatalf("evalsymlinks: %v", err)
	}
	return resolved
}

// TestLoadGitStatus_HasStaged exercises the flag end-to-end against a
// real repo through the untracked → staged → committed lifecycle.
func TestLoadGitStatus_HasStaged(t *testing.T) {
	if !gitAvailable() {
		t.Skip("git not on PATH")
	}
	repo := initRepo(t)
	writeFileT(t, filepath.Join(repo, "f.txt"), "x\n")

	if st := loadGitStatus(repo); st.HasStaged {
		t.Fatal("untracked file alone must not read as staged")
	}
	gitRun(t, repo, "add", "f.txt")
	if st := loadGitStatus(repo); !st.HasStaged {
		t.Fatal("added file should read as staged")
	}
	gitRun(t, repo, "commit", "-q", "-m", "init")
	if st := loadGitStatus(repo); st.HasStaged {
		t.Fatal("clean tree after commit must not read as staged")
	}
}

// gitRun invokes git in cwd. Fails the test on non-zero exit so a broken
// fixture doesn't masquerade as a code bug.
func gitRun(t *testing.T, cwd string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = cwd
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v in %s: %v\n%s", args, cwd, err, out)
	}
}

// writeFileT writes content to path with sensible perms, failing the test
// on any IO error. (Named writeFileT to avoid colliding with the helper
// of the same name in modals_test.go.)
func writeFileT(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

// sortedKeys returns the keys of m in lexicographic order — handy when
// printing diff context inside test failures.
func sortedKeys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// TestIsPorcelainConflict pins the seven-code unmerged table. The two
// same-letter pairs are the ones a "does either column say U?" test
// quietly loses: AA is what two branches creating the same file looks
// like, and it is the common case, not an exotic one.
func TestIsPorcelainConflict(t *testing.T) {
	conflicts := []string{"UU", "AU", "UA", "DU", "UD", "AA", "DD"}
	for _, xy := range conflicts {
		if !isPorcelainConflict(xy[0], xy[1]) {
			t.Errorf("%s should read as unmerged", xy)
		}
	}
	clean := []string{"M ", " M", "MM", "A ", "R ", "??", "!!", "D ", " D", "AM"}
	for _, xy := range clean {
		if isPorcelainConflict(xy[0], xy[1]) {
			t.Errorf("%q should NOT read as unmerged", xy)
		}
	}
}

// TestLoadGitStatus_Conflicted is the end-to-end half: a real conflicted
// repo must come back through the snapshot, because that field is what
// gates the menu row on every draw.
func TestLoadGitStatus_Conflicted(t *testing.T) {
	requireGit(t)
	repo := initRepo(t)
	writeCommit(t, repo, "f.txt", "one\ntwo\nthree\n", "base")
	gitRun(t, repo, "checkout", "-q", "-b", "side")
	writeCommit(t, repo, "f.txt", "one\nSIDE\nthree\n", "side")
	gitRun(t, repo, "checkout", "-q", "main")
	writeCommit(t, repo, "f.txt", "one\nMAIN\nthree\n", "main")
	gitRunAllowFail(t, repo, "merge", "side")

	st := loadGitStatus(repo)
	if !st.ConflictedFiles[filepath.Join(repo, "f.txt")] {
		t.Errorf("conflicted set = %v, want f.txt", sortedKeys(st.ConflictedFiles))
	}

	gitRun(t, repo, "merge", "--abort")
	if n := len(loadGitStatus(repo).ConflictedFiles); n != 0 {
		t.Errorf("after abort: %d conflicted files, want 0", n)
	}
}

// TestSplitRevParseDirs pins the two-answer parse, including the bare
// repository shape (no toplevel line content) and trailing CRs.
func TestSplitRevParseDirs(t *testing.T) {
	top, dir := splitRevParseDirs([]byte("/repo\r\n/repo/.git\r\n"))
	if top != "/repo" || dir != "/repo/.git" {
		t.Errorf("got %q %q", top, dir)
	}
	if top, dir := splitRevParseDirs([]byte("/repo\n")); top != "/repo" || dir != "" {
		t.Errorf("one line: %q %q", top, dir)
	}
}

// TestLoadGitStatus_InProgressOperation pins the free detection: the
// snapshot carries the git dir and names a parked cherry-pick, and a clean
// repo names none.
func TestLoadGitStatus_InProgressOperation(t *testing.T) {
	repo := cherryPickConflictRepo(t)
	st := loadGitStatus(repo)
	if st.InProgress != "cherry-pick" || st.GitDir != filepath.Join(repo, ".git") {
		t.Errorf("InProgress=%q GitDir=%q", st.InProgress, st.GitDir)
	}
	gitRun(t, repo, "cherry-pick", "--abort")
	if st := loadGitStatus(repo); st.InProgress != "" {
		t.Errorf("after abort: InProgress=%q", st.InProgress)
	}
}

// -----------------------------------------------------------------------------
// porcelain v2 (N-127)
// -----------------------------------------------------------------------------

// TestParsePorcelainV2_Entries pins the -z entry grammar: an ordinary
// change, a rename whose SOURCE rides in the next token, an unmerged
// path, and untracked paths with a space and a non-ASCII rune (raw under
// -z, never quoted). XY codes come back in v1's spelling.
func TestParsePorcelainV2_Entries(t *testing.T) {
	out := "# branch.oid abc\x00# branch.head main\x00" +
		"1 .M N... 100644 100644 100644 h1 h2 sub/a.txt\x00" +
		"1 MM N... 100644 100644 100644 h1 h2 b c.txt\x00" +
		"2 R. N... 100644 100644 100644 h1 h2 R100 new name.txt\x00old.txt\x00" +
		"u UU N... 100644 100644 100644 100644 h1 h2 h3 f.txt\x00" +
		"? sub/new file.txt\x00" +
		"? café.txt\x00"
	got := parsePorcelainV2([]byte(out)).Entries
	want := []porcelainEntry{
		{XY: " M", Path: "sub/a.txt"},
		{XY: "MM", Path: "b c.txt"},
		{XY: "R ", Path: "new name.txt", Orig: "old.txt"},
		{XY: "UU", Path: "f.txt", Unmerged: true},
		{XY: "??", Path: "sub/new file.txt"},
		{XY: "??", Path: "café.txt"},
	}
	if len(got) != len(want) {
		t.Fatalf("entries = %+v, want %+v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("entry %d = %+v, want %+v", i, got[i], want[i])
		}
	}
}

// TestParsePorcelainV2_MalformedRenameStillConsumesItsSource: a rename
// line too short to split must still swallow its source token, or a
// source path that happens to look like an entry ("? x") would be listed
// as a change of its own.
func TestParsePorcelainV2_MalformedRenameStillConsumesItsSource(t *testing.T) {
	got := parsePorcelainV2([]byte("2 R. truncated\x00? x\x00? real.txt\x00")).Entries
	if len(got) != 1 || got[0].Path != "real.txt" {
		t.Fatalf("entries = %+v, want only real.txt", got)
	}
}

// TestParsePorcelainV2_JunkAndEmpty: empty output, a bare NUL and an
// unknown line type parse to nothing rather than a guess.
func TestParsePorcelainV2_JunkAndEmpty(t *testing.T) {
	for _, in := range []string{"", "\x00", "1\x00", "Z something new\x00", "1 .M too few\x00"} {
		if got := parsePorcelainV2([]byte(in)).Entries; len(got) != 0 {
			t.Errorf("%q parsed to %+v, want nothing", in, got)
		}
	}
}

// TestParsePorcelainV2_BranchHeaders pins the header half: the branch
// name, a detached HEAD, and the upstream rule — kept only when
// "# branch.ab" follows, because git names a configured upstream even
// after its ref is gone, and the old @{upstream} read called that "no
// upstream".
func TestParsePorcelainV2_BranchHeaders(t *testing.T) {
	cases := []struct {
		name          string
		out           string
		head          string
		detached      bool
		upstream      string
		ahead, behind int
	}{
		{"tracked, ahead and behind",
			"# branch.oid abc\x00# branch.head main\x00# branch.upstream origin/main\x00# branch.ab +3 -5\x00",
			"main", false, "origin/main", 3, 5},
		{"tracked, level",
			"# branch.head topic\x00# branch.upstream origin/topic\x00# branch.ab +0 -0\x00",
			"topic", false, "origin/topic", 0, 0},
		{"upstream configured but gone",
			"# branch.head gone\x00# branch.upstream origin/nosuch\x00",
			"gone", false, "", 0, 0},
		{"no upstream", "# branch.head local\x00", "local", false, "", 0, 0},
		{"detached", "# branch.oid abc\x00# branch.head (detached)\x00", "", true, "", 0, 0},
		{"unborn", "# branch.oid (initial)\x00# branch.head fresh\x00", "fresh", false, "", 0, 0},
	}
	for _, tc := range cases {
		p := parsePorcelainV2([]byte(tc.out))
		if p.Head != tc.head || p.Detached != tc.detached || p.Upstream != tc.upstream ||
			p.Ahead != tc.ahead || p.Behind != tc.behind {
			t.Errorf("%s: got head=%q detached=%v upstream=%q ahead=%d behind=%d",
				tc.name, p.Head, p.Detached, p.Upstream, p.Ahead, p.Behind)
		}
	}
}

// TestParseBranchAB pins the "+ahead -behind" spelling (the minus is
// spelling, not sign) and the malformed degrade to level.
func TestParseBranchAB(t *testing.T) {
	if a, b := parseBranchAB("+2 -7"); a != 2 || b != 7 {
		t.Errorf("got ahead=%d behind=%d, want 2/7", a, b)
	}
	if a, b := parseBranchAB("garbage"); a != 0 || b != 0 {
		t.Errorf("malformed: got %d/%d, want 0/0", a, b)
	}
}

// TestPathSets pins the three sets: every entry (both sides of a rename)
// is dirty; staged means an index-side letter — work-tree-only and
// untracked entries are not, unmerged ones ARE (v1's reading, which
// lights Commit staged / Unstage during a conflict); only 'u' entries
// are conflicted. All three come back non-nil.
func TestPathSets(t *testing.T) {
	p := porcelainStatus{Entries: []porcelainEntry{
		{XY: "M ", Path: "staged.txt"},
		{XY: " M", Path: "worktree.txt"},
		{XY: "??", Path: "untracked.txt"},
		{XY: "MM", Path: "both.txt"},
		{XY: "R ", Path: "new.txt", Orig: "old.txt"},
		{XY: "D ", Path: "gone.txt"},
		{XY: "AA", Path: "twice.txt", Unmerged: true},
	}}
	dirty, staged, conflicted := p.pathSets("/repo")
	j := func(n ...string) []string {
		out := make([]string, len(n))
		for i, s := range n {
			out[i] = filepath.Join("/repo", s)
		}
		sort.Strings(out)
		return out
	}
	check := func(name string, got map[string]bool, want []string) {
		t.Helper()
		if g := sortedKeys(got); strings.Join(g, ",") != strings.Join(want, ",") {
			t.Errorf("%s = %v, want %v", name, g, want)
		}
	}
	check("dirty", dirty, j("staged.txt", "worktree.txt", "untracked.txt", "both.txt", "new.txt", "old.txt", "gone.txt", "twice.txt"))
	check("staged", staged, j("staged.txt", "both.txt", "new.txt", "old.txt", "gone.txt", "twice.txt"))
	check("conflicted", conflicted, j("twice.txt"))

	d, s, c := porcelainStatus{}.pathSets("/repo")
	if d == nil || s == nil || c == nil || len(d)+len(s)+len(c) != 0 {
		t.Errorf("no entries: want three empty non-nil sets, got %v %v %v", d, s, c)
	}
}

// TestRevParseHasStash pins the line arithmetic of the folded stash
// probe: a third line is refs/stash's SHA, two lines mean no stash.
func TestRevParseHasStash(t *testing.T) {
	if revParseHasStash([]byte("/repo\n/repo/.git\n")) {
		t.Error("two lines read as a stash")
	}
	if !revParseHasStash([]byte("/repo\n/repo/.git\n7fbf4aa7e1cec7647c4aee5b490ed621e409627f\n")) {
		t.Error("third line not read as a stash")
	}
	if revParseHasStash([]byte("/repo\n/repo/.git\n\n")) {
		t.Error("blank third line read as a stash")
	}
}

// TestLoadGitStatus_HasStash is the end-to-end stash probe (ported from
// the retired loadGitHasStash): none in a fresh repo, one after a stash,
// and a BRANCH named "stash" never counts — the glob is anchored to
// refs/stash exactly.
func TestLoadGitStatus_HasStash(t *testing.T) {
	requireGit(t)
	repo := initRepo(t)
	writeFileT(t, filepath.Join(repo, "f.txt"), "x\n")
	gitRun(t, repo, "add", ".")
	gitRun(t, repo, "commit", "-q", "-m", "init")
	gitRun(t, repo, "branch", "stash")
	if loadGitStatus(repo).HasStash {
		t.Fatal("a branch named stash read as a stash entry")
	}
	writeFileT(t, filepath.Join(repo, "f.txt"), "y\n")
	gitRun(t, repo, "stash", "push", "-q")
	if !loadGitStatus(repo).HasStash {
		t.Fatal("stash entry not detected")
	}
}

// TestLoadGitStatus_DetachedHeadNamesShortSHA: the branch header says
// "(detached)", and the label is git's own abbreviation of HEAD.
func TestLoadGitStatus_DetachedHeadNamesShortSHA(t *testing.T) {
	requireGit(t)
	repo := initRepo(t)
	writeCommit(t, repo, "f.txt", "x\n", "init")
	gitRun(t, repo, "checkout", "-q", "--detach")
	want := gitOut(t, repo, "rev-parse", "--short", "HEAD")
	if got := loadGitStatus(repo).Branch; got != want {
		t.Fatalf("detached branch label = %q, want %q", got, want)
	}
}

// TestLoadGitStatus_PanelCodesMatchPorcelainV1 is the parity pin for the
// v1 → v2 switch: on a real repo holding every kind of change, the
// snapshot's panel rows (path + XY) are exactly what `git status
// --porcelain -z` (v1) reports, in the same order — the panel draws the
// code verbatim and its verbs key off it — and the panel's own read
// (loadGitStatusFiles) agrees with the snapshot's.
func TestLoadGitStatus_PanelCodesMatchPorcelainV1(t *testing.T) {
	requireGit(t)
	repo := initRepo(t)
	for _, f := range []string{"mod.txt", "both.txt", "ren.txt", "del.txt", "staged.txt"} {
		writeFileT(t, filepath.Join(repo, f), f+"\n")
	}
	gitRun(t, repo, "add", ".")
	gitRun(t, repo, "commit", "-q", "-m", "init")
	writeFileT(t, filepath.Join(repo, "mod.txt"), "changed\n")           // " M"
	writeFileT(t, filepath.Join(repo, "both.txt"), "staged\n")           //
	gitRun(t, repo, "add", "both.txt")                                   //
	writeFileT(t, filepath.Join(repo, "both.txt"), "staged then more\n") // "MM"
	gitRun(t, repo, "mv", "ren.txt", "renamed file.txt")                 // "R "
	gitRun(t, repo, "rm", "-q", "del.txt")                               // "D "
	writeFileT(t, filepath.Join(repo, "staged.txt"), "s2\n")             //
	gitRun(t, repo, "add", "staged.txt")                                 // "M "
	writeFileT(t, filepath.Join(repo, "new file.txt"), "n\n")            // "??"
	writeFileT(t, filepath.Join(repo, "café.txt"), "c\n")                // "??", raw under -z

	// v1 -z: "XY path", a rename's source in the next token.
	var want []gitPanelFile
	toks := strings.Split(gitOutRaw(t, repo, "status", "--porcelain", "-z"), "\x00")
	for i := 0; i < len(toks); i++ {
		tok := toks[i]
		if len(tok) < 4 {
			continue
		}
		if tok[0] == 'R' || tok[0] == 'C' {
			i++
		}
		want = append(want, gitPanelFile{Path: filepath.Join(repo, tok[3:]), Rel: tok[3:], Code: tok[:2]})
	}
	if len(want) != 7 {
		t.Fatalf("fixture produced %d v1 entries, want 7: %+v", len(want), want)
	}

	got := loadGitStatus(repo).PanelFiles
	if len(got) != len(want) {
		t.Fatalf("panel rows = %+v\nwant %+v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("row %d = %+v, want %+v", i, got[i], want[i])
		}
	}
	own := loadGitStatusFiles(repo)
	if len(own) != len(got) {
		t.Fatalf("loadGitStatusFiles = %+v, snapshot = %+v", own, got)
	}
	for i := range own {
		if own[i] != got[i] {
			t.Errorf("loadGitStatusFiles row %d = %+v, snapshot has %+v", i, own[i], got[i])
		}
	}
}

// gitOutRaw runs git in cwd and returns stdout untrimmed — for -z output,
// whose NULs and trailing separator matter.
func gitOutRaw(t *testing.T, cwd string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = cwd
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("git %v in %s: %v", args, cwd, err)
	}
	return string(out)
}
