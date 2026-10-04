// =============================================================================
// File: internal/app/gitopstate.go
// Author: Rohan Allison <rohanthewiz@gmail.com>
// Created: 2026-10-03
// Copyright: 2026 Rohan Allison. All rights reserved.
// =============================================================================

// gitopstate.go reads WHAT a parked repository is in the middle of, in
// enough detail to explain it: which commit a cherry-pick stopped on and
// how far through the sequence it is, what a merge is merging, which
// files are unmerged and in what way. The Conflicts panel
// (conflictpanel.go) is the consumer; gitconflict.go's table decides
// WHICH operation is running, and this file only fills in the rest.
//
// Everything is re-derived from the git directory on each read — the
// gitconflict.go rule ("never remembered from the command that failed"),
// for its reason: the repo can be parked by a terminal beside the
// editor, by a `stash pop`, or by a ced command an hour ago.
//
// Where git keeps the facts (verified against git 2.50):
//
//	CHERRY_PICK_HEAD / REVERT_HEAD   the commit being applied right now
//	sequencer/todo                   the REMAINING picks, the stopped one first
//	sequencer/head                   HEAD before the sequence began
//	MERGE_HEAD, MERGE_MSG            what is being merged, and git's message
//	rebase-merge/{msgnum,end}        "step 3 of 7"
//	rebase-merge/stopped-sha         the commit a rebase stopped on
//	rebase-merge/{head-name,onto}    the branch being rebased, and onto what
//	rebase-apply/{next,last}         the same counters for the apply backend
//
// A multi-commit cherry-pick records no "done" count, so the position is
// computed: commits since sequencer/head, plus one for the stopped one.
// That is one `rev-list --count` — paid only while an operation is
// actually running, never on a clean repo.

package app

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
)

// gitOpInfo describes the operation a repo is parked in. The zero value
// is "nothing in progress".
type gitOpInfo struct {
	op string // "cherry-pick", "revert", "merge", "rebase", or ""

	// commit is the full hash of the commit being applied (cherry-pick,
	// revert, rebase) or merged (merge); short and subject describe it.
	// All three are "" when git left no record — a rebase stopped by an
	// `edit` line, say — and the header simply says less.
	commit  string
	short   string
	subject string

	// mergeMsg is the first line of MERGE_MSG ("Merge branch 'topic'"),
	// which names the merge better than the hash of its tip does.
	mergeMsg string

	// step / total locate the stopped commit in a multi-commit sequence
	// ("2 of 4"). Both 0 for a single-commit operation or a merge.
	step, total int

	// headName / onto are the rebase's two refs: the branch being rebased
	// ("feature") and the commit it is being replayed onto (short hash).
	headName string
	onto     string
}

// loadGitOpInfo fills a gitOpInfo for op from the git directory. Best
// effort per field: a missing file leaves its field empty, and the
// header composes from whatever was found.
func loadGitOpInfo(rootDir, gitDir, op string) gitOpInfo {
	info := gitOpInfo{op: op}
	if op == "" || gitDir == "" {
		return info
	}
	read := func(name string) string {
		b, err := os.ReadFile(filepath.Join(gitDir, name))
		if err != nil {
			return ""
		}
		return strings.TrimSpace(string(b))
	}
	switch op {
	case "cherry-pick", "revert":
		head := "CHERRY_PICK_HEAD"
		if op == "revert" {
			head = "REVERT_HEAD"
		}
		info.commit = firstLine(read(head))
		info.step, info.total = sequencerProgress(rootDir, read("sequencer/todo"), read("sequencer/head"))
	case "merge":
		info.commit = firstLine(read("MERGE_HEAD"))
		info.mergeMsg = firstLine(read("MERGE_MSG"))
	case "rebase":
		dir := "rebase-merge"
		if _, err := os.Stat(filepath.Join(gitDir, dir)); err != nil {
			dir = "rebase-apply"
		}
		if dir == "rebase-merge" {
			info.commit = read(dir + "/stopped-sha")
			info.step, _ = strconv.Atoi(read(dir + "/msgnum"))
			info.total, _ = strconv.Atoi(read(dir + "/end"))
		} else {
			info.commit = read(dir + "/original-commit")
			info.step, _ = strconv.Atoi(read(dir + "/next"))
			info.total, _ = strconv.Atoi(read(dir + "/last"))
		}
		info.headName = strings.TrimPrefix(read(dir+"/head-name"), "refs/heads/")
		if onto := read(dir + "/onto"); len(onto) >= 7 {
			info.onto = onto[:7]
		}
	}
	if info.commit != "" {
		info.short, info.subject = gitCommitSummary(rootDir, info.commit)
	}
	return info
}

// sequencerProgress turns a cherry-pick / revert sequencer's state into
// "step of total". todo lists the picks still to do with the STOPPED one
// first; head is HEAD before the sequence began, so the commits since it
// are the ones already applied. A single-commit pick leaves no sequencer
// at all, which answers (0, 0) — there is no sequence to be part of.
func sequencerProgress(rootDir, todo, head string) (step, total int) {
	remaining := 0
	for _, line := range strings.Split(todo, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		remaining++
	}
	if remaining == 0 {
		return 0, 0
	}
	done := 0
	if head != "" {
		out, err := exec.Command("git", "-C", rootDir, "rev-list", "--count", head+"..HEAD").Output()
		if err == nil {
			done, _ = strconv.Atoi(strings.TrimSpace(string(out)))
		}
	}
	return done + 1, done + remaining
}

// gitCommitSummary returns a commit's short hash and subject — one fork,
// asked only while an operation is parked.
func gitCommitSummary(rootDir, commit string) (short, subject string) {
	out, err := exec.Command("git", "-C", rootDir, "log", "-1", "--format=%h%x1f%s", commit).Output()
	if err != nil {
		if len(commit) >= 7 {
			return commit[:7], ""
		}
		return commit, ""
	}
	parts := strings.SplitN(strings.TrimRight(string(out), "\n"), "\x1f", 2)
	if len(parts) == 2 {
		return parts[0], parts[1]
	}
	return parts[0], ""
}

// gitIndexHasChanges reports whether anything is staged relative to HEAD.
// The Conflicts panel asks it in exactly one state — an operation parked
// with nothing unmerged — to tell "resolved, ready to continue" from
// "this commit turned out empty" (its changes were already on the
// branch), where Continue fails and Skip is the way forward. Unknown
// reads as "has changes", the answer that keeps Continue offered and lets
// git's own message explain.
func gitIndexHasChanges(rootDir string) bool {
	return exec.Command("git", "-C", rootDir, "diff", "--cached", "--quiet", "HEAD").Run() != nil
}

// conflictFile is one unmerged path: where it is, and HOW it conflicts —
// the porcelain XY pair, which separates a content conflict (markers in
// the file) from a modify/delete one (no markers; the question is
// whether the file should exist at all).
type conflictFile struct {
	rel string // work-tree-root relative, as git reports it
	abs string
	xy  string // "UU", "AA", "DU", "UD", "AU", "UA", "DD"
}

// loadConflictFiles lists the repo's unmerged paths with their XY codes,
// in path order. One `git status --porcelain -z` — the -z form because it
// neither quotes nor escapes paths (spaces, non-ASCII), and status rather
// than `diff --diff-filter=U` because only status says which KIND of
// conflict each path is.
func loadConflictFiles(rootDir string) (toplevel string, files []conflictFile) {
	toplevel = gitToplevel(rootDir)
	if toplevel == "" {
		return "", nil
	}
	out, err := exec.Command("git", "-C", rootDir, "status", "--porcelain", "-z", "--untracked-files=no").Output()
	if err != nil {
		return toplevel, nil
	}
	return toplevel, parseConflictStatusZ(out, toplevel)
}

// parseConflictStatusZ extracts the unmerged entries from `status
// --porcelain -z` output. A rename or copy entry carries its ORIGINAL path
// as a second NUL-terminated token, which is skipped so it is never read
// as an entry of its own (unmerged entries are never renames, but the
// other entries share the stream).
func parseConflictStatusZ(out []byte, toplevel string) []conflictFile {
	var files []conflictFile
	tokens := bytes.Split(out, []byte{0})
	for i := 0; i < len(tokens); i++ {
		tok := tokens[i]
		if len(tok) < 4 {
			continue
		}
		x, y := tok[0], tok[1]
		if x == 'R' || x == 'C' {
			i++ // the original path rides in the next token
			continue
		}
		if !isPorcelainConflict(x, y) {
			continue
		}
		rel := string(tok[3:])
		files = append(files, conflictFile{
			rel: rel,
			abs: filepath.Join(toplevel, rel),
			xy:  string([]byte{x, y}),
		})
	}
	return files
}

// conflictKindLabel describes an XY pair in POSITIONAL words. git's own
// "deleted by us / by them" flips meaning in a rebase (where "us" is the
// upstream), and this editor names the sides current / incoming
// everywhere (editor/conflict.go) so one vocabulary holds in every
// operation.
func conflictKindLabel(xy string) string {
	switch xy {
	case "UU":
		return "both modified"
	case "AA":
		return "both added"
	case "DD":
		return "both deleted"
	case "DU":
		return "deleted in current"
	case "UD":
		return "deleted in incoming"
	case "AU":
		return "added in current only"
	case "UA":
		return "added in incoming only"
	}
	return "unmerged"
}

// isPresenceConflict reports whether a conflict is about the file's
// EXISTENCE rather than its content: one side deleted or never had it.
// Such a file has no markers to resolve, and staging it is a decision
// (keep vs delete) the editor must not take on the user's behalf — so
// these never count as "ready" for the stage-everything verbs.
func isPresenceConflict(xy string) bool {
	switch xy {
	case "DU", "UD", "AU", "UA", "DD":
		return true
	}
	return false
}

// readConflictScanLines reads a file for the marker scan, as LINES the
// way the editor's buffer holds them (CR stripped, so a CRLF file's
// `=======\r` still reads as a separator). ok=false for a file that is
// missing, unreadable or over gitConflictScanMax — callers treat that as
// "unknown", never as "clean".
func readConflictScanLines(path string) ([]string, bool) {
	st, err := os.Stat(path)
	if err != nil || st.IsDir() || st.Size() > gitConflictScanMax {
		return nil, false
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, false
	}
	lines := strings.Split(string(data), "\n")
	for i, l := range lines {
		lines[i] = strings.TrimSuffix(l, "\r")
	}
	return lines, true
}
