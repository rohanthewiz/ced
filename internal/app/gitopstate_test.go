// =============================================================================
// File: internal/app/gitopstate_test.go
// Author: Rohan Allison <rohanthewiz@gmail.com>
// Created: 2026-10-03
// Copyright: 2026 Rohan Allison. All rights reserved.
// =============================================================================

package app

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// cherryPickConflictRepo builds a repository parked mid-cherry-pick: main
// and side both rewrite the middle line of f.txt, and side's commit is
// cherry-picked onto main. Returns the (resolved) repo path.
func cherryPickConflictRepo(t *testing.T) string {
	t.Helper()
	requireGit(t)
	repo := initRepo(t)
	writeCommit(t, repo, "f.txt", "one\ntwo\nthree\n", "base")
	gitRun(t, repo, "checkout", "-q", "-b", "side")
	writeCommit(t, repo, "f.txt", "one\nSIDE\nthree\n", "side edits the middle")
	gitRun(t, repo, "checkout", "-q", "main")
	writeCommit(t, repo, "f.txt", "one\nMAIN\nthree\n", "main edits the middle")
	gitRunAllowFail(t, repo, "cherry-pick", "side")
	return repo
}

// TestParseConflictStatusZ pins the -z parse: unmerged entries kept with
// their XY codes, ordinary entries dropped, and a rename's second token
// (its original path) never mistaken for an entry of its own.
func TestParseConflictStatusZ(t *testing.T) {
	out := []byte("UU a.go\x00R  new.go\x00UD looks-like-an-entry\x00M  b.go\x00AA c.go\x00DU d.go\x00")
	got := parseConflictStatusZ(out, "/repo")
	want := []conflictFile{
		{rel: "a.go", abs: "/repo/a.go", xy: "UU"},
		{rel: "c.go", abs: "/repo/c.go", xy: "AA"},
		{rel: "d.go", abs: "/repo/d.go", xy: "DU"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %+v\nwant %+v", got, want)
	}
}

// TestConflictKindLabel pins the positional vocabulary for every unmerged
// code, and that only the presence codes count as presence conflicts.
func TestConflictKindLabel(t *testing.T) {
	cases := map[string]struct {
		label    string
		presence bool
	}{
		"UU": {"both modified", false},
		"AA": {"both added", false},
		"DD": {"both deleted", true},
		"DU": {"deleted in current", true},
		"UD": {"deleted in incoming", true},
		"AU": {"added in current only", true},
		"UA": {"added in incoming only", true},
	}
	for xy, c := range cases {
		if got := conflictKindLabel(xy); got != c.label {
			t.Errorf("%s: label %q, want %q", xy, got, c.label)
		}
		if got := isPresenceConflict(xy); got != c.presence {
			t.Errorf("%s: presence %v, want %v", xy, got, c.presence)
		}
	}
}

// TestReadConflictScanLines pins the CR strip (a CRLF file's separator
// must still parse) and the fail-safe answers for missing and oversized
// files.
func TestReadConflictScanLines(t *testing.T) {
	dir := t.TempDir()
	crlf := filepath.Join(dir, "crlf.txt")
	writeFileT(t, crlf, "<<<<<<< HEAD\r\na\r\n=======\r\nb\r\n>>>>>>> x\r\n")
	lines, ok := readConflictScanLines(crlf)
	if !ok || lines[2] != "=======" {
		t.Fatalf("lines = %q ok=%v, want CR stripped", lines, ok)
	}
	if _, ok := readConflictScanLines(filepath.Join(dir, "missing")); ok {
		t.Error("a missing file read as scannable")
	}
	big := filepath.Join(dir, "big.bin")
	if err := os.WriteFile(big, make([]byte, gitConflictScanMax+1), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, ok := readConflictScanLines(big); ok {
		t.Error("an over-cap file read as scannable")
	}
}

// TestSequencerProgress_NoSequence pins the single-commit answer: no todo
// means no "n of m" at all.
func TestSequencerProgress_NoSequence(t *testing.T) {
	if s, n := sequencerProgress(t.TempDir(), "", ""); s != 0 || n != 0 {
		t.Errorf("got %d of %d, want 0 of 0", s, n)
	}
}

// TestLoadGitOpInfo_CherryPick reads a real stopped single-commit pick:
// the stopped commit's hash and subject, and no sequence position.
func TestLoadGitOpInfo_CherryPick(t *testing.T) {
	repo := cherryPickConflictRepo(t)
	info := loadGitOpInfo(repo, filepath.Join(repo, ".git"), "cherry-pick")
	if info.op != "cherry-pick" || info.subject != "side edits the middle" || len(info.short) < 7 {
		t.Errorf("info = %+v", info)
	}
	if info.step != 0 || info.total != 0 {
		t.Errorf("single pick reported %d of %d", info.step, info.total)
	}
}

// TestLoadGitOpInfo_Sequence reads a real multi-commit pick stopped on
// its first commit, then on its last after a continue — the position is
// computed from the sequencer, which records no "done" count of its own.
func TestLoadGitOpInfo_Sequence(t *testing.T) {
	requireGit(t)
	repo := initRepo(t)
	writeCommit(t, repo, "f.txt", "one\ntwo\nthree\n", "base")
	gitRun(t, repo, "checkout", "-q", "-b", "side")
	writeCommit(t, repo, "f.txt", "one\nSIDE\nthree\n", "s1 conflicts")
	writeCommit(t, repo, "g.txt", "g\n", "s2 clean")
	writeCommit(t, repo, "f.txt", "one\nSIDE2\nthree\n", "s3 conflicts again")
	gitRun(t, repo, "checkout", "-q", "main")
	writeCommit(t, repo, "f.txt", "one\nMAIN\nthree\n", "main")
	gitRunAllowFail(t, repo, "cherry-pick", "side~2", "side~1", "side")

	gitDir := filepath.Join(repo, ".git")
	if info := loadGitOpInfo(repo, gitDir, "cherry-pick"); info.step != 1 || info.total != 3 {
		t.Fatalf("first stop: %d of %d, want 1 of 3", info.step, info.total)
	}
	writeFileT(t, filepath.Join(repo, "f.txt"), "one\nBOTH\nthree\n")
	gitRun(t, repo, "add", "f.txt")
	cont := gitCmdT(t, repo, gitConflictContinueArgs("cherry-pick")...)
	cont.Env = gitNoEditorEnv()
	_, _ = cont.CombinedOutput() // stops again on s3: a non-zero exit is the fixture
	info := loadGitOpInfo(repo, gitDir, "cherry-pick")
	if info.step != 3 || info.total != 3 || info.subject != "s3 conflicts again" {
		t.Errorf("second stop: %+v, want 3 of 3 on s3", info)
	}
}

// TestLoadGitOpInfo_Merge pins the merge header: MERGE_MSG's first line.
func TestLoadGitOpInfo_Merge(t *testing.T) {
	requireGit(t)
	repo := initRepo(t)
	writeCommit(t, repo, "f.txt", "one\ntwo\n", "base")
	gitRun(t, repo, "checkout", "-q", "-b", "topic")
	writeCommit(t, repo, "f.txt", "one\nTOPIC\n", "topic")
	gitRun(t, repo, "checkout", "-q", "main")
	writeCommit(t, repo, "f.txt", "one\nMAIN\n", "main")
	gitRunAllowFail(t, repo, "merge", "topic")

	info := loadGitOpInfo(repo, filepath.Join(repo, ".git"), "merge")
	if !strings.Contains(info.mergeMsg, "Merge branch 'topic'") {
		t.Errorf("mergeMsg = %q", info.mergeMsg)
	}
}

// TestLoadGitOpInfo_RebaseFromFiles pins the rebase-merge reads against a
// hand-built git dir: counters, the branch with refs/heads/ trimmed, and
// onto shortened.
func TestLoadGitOpInfo_RebaseFromFiles(t *testing.T) {
	gitDir := t.TempDir()
	rm := filepath.Join(gitDir, "rebase-merge")
	if err := os.Mkdir(rm, 0o755); err != nil {
		t.Fatal(err)
	}
	for name, body := range map[string]string{
		"msgnum": "3\n", "end": "7\n", "head-name": "refs/heads/feature\n",
		"onto": "0123456789abcdef\n",
	} {
		writeFileT(t, filepath.Join(rm, name), body)
	}
	info := loadGitOpInfo(t.TempDir(), gitDir, "rebase")
	if info.step != 3 || info.total != 7 || info.headName != "feature" || info.onto != "0123456" {
		t.Errorf("info = %+v", info)
	}
}

// TestLoadConflictFiles_RealRepo pins the end of the pipe against git:
// one UU path, toplevel-relative, with an absolute twin.
func TestLoadConflictFiles_RealRepo(t *testing.T) {
	repo := cherryPickConflictRepo(t)
	top, files := loadConflictFiles(repo)
	if top != repo || len(files) != 1 || files[0].rel != "f.txt" || files[0].xy != "UU" ||
		files[0].abs != filepath.Join(repo, "f.txt") {
		t.Fatalf("top=%q files=%+v", top, files)
	}
}

// TestGitIndexHasChanges pins both answers on a real repo: a clean index
// says no, a staged edit says yes.
func TestGitIndexHasChanges(t *testing.T) {
	requireGit(t)
	repo := initRepo(t)
	writeCommit(t, repo, "f.txt", "one\n", "base")
	if gitIndexHasChanges(repo) {
		t.Error("clean index reported changes")
	}
	writeFileT(t, filepath.Join(repo, "f.txt"), "two\n")
	gitRun(t, repo, "add", "f.txt")
	if !gitIndexHasChanges(repo) {
		t.Error("staged edit not reported")
	}
}
