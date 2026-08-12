// =============================================================================
// File: internal/app/gitstatus.go
// Author: Spicer Matthews <spicer@cloudmanic.com>
// Created: 2026-04-30
// Copyright: 2026 Cloudmanic, LLC. All rights reserved.
// =============================================================================

// gitstatus.go shells out to `git` to figure out which files inside the
// project root have uncommitted changes. The result feeds the file tree's
// "dirty" highlight: changed files render in the theme's Modified color,
// and any folder containing a dirty file picks up the same color so the
// signal isn't hidden behind a collapsed branch.
//
// Everything in here is best-effort — if the project isn't a git
// repo, or `git` isn't on PATH, or the command fails for any reason,
// loadGitStatus returns an empty result and the editor renders normally.
// We never block the UI on git, never spam errors at the user, and never
// retry on failure.

package app

import (
	"bytes"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
)

// gitStatus is the snapshot of a single git status run. IsRepo distinguishes
// "not a git repo" (don't bother trying again) from "git error" (we tried
// and bailed). DirtyFiles holds absolute paths to changed entries; callers
// should treat absence-of-key as "clean" rather than as "unknown". Branch
// is the human-readable current branch name, or a short SHA when HEAD is
// detached, or "" when we aren't in a repo.
type gitStatus struct {
	IsRepo     bool
	DirtyFiles map[string]bool
	Branch     string
	// HasStaged reports whether anything sits in the index waiting to
	// be committed. It gates the "Commit staged" menu row — a commit
	// with an empty index only produces a git error, so the row stays
	// dimmed until a stage actually happened.
	HasStaged bool
	// StagedFiles holds the absolute paths whose index side (porcelain X
	// column) carries a staged change. It gates the "Unstage file" menu
	// row and lets the git panel draw per-file stage checkboxes without
	// forking git per row. Renames mark both paths, matching DirtyFiles.
	StagedFiles map[string]bool
	// HasStash reports whether refs/stash exists — i.e. `git stash pop`
	// has something to pop. Gates the "Pop stash" menu row.
	HasStash bool
	// Upstream is HEAD's tracked remote-tracking branch in short form
	// ("origin/main"), or "" when the branch has no upstream, HEAD is
	// detached, or we couldn't tell. Ahead / Behind count the commits
	// HEAD has that the upstream doesn't and vice versa; both are 0
	// whenever Upstream is empty, because there is nothing to measure
	// against. They feed the status bar's push indicator and seed the
	// push dialog (gitpush.go).
	Upstream      string
	Ahead, Behind int
	// HasRemote reports whether the repo has at least one remote
	// configured — the difference between "this branch isn't pushed
	// yet" and "there is nowhere to push". Gates the push entry points
	// so a purely local repo never offers a dialog whose first dropdown
	// would be empty.
	HasRemote bool
}

// loadGitStatus inspects rootDir and returns the set of dirty file paths
// reported by `git status --porcelain`. A non-git directory yields the
// zero value (IsRepo=false, no dirty paths). Any failure of the underlying
// commands degrades the same way — we'd rather lose the dirty highlight
// than crash the editor over a transient git issue.
func loadGitStatus(rootDir string) gitStatus {
	if rootDir == "" {
		return gitStatus{}
	}

	// rev-parse --show-toplevel does double duty: it tells us whether
	// we're in a git work tree at all (non-zero exit otherwise) and
	// gives us the absolute path of the repo root, which is the prefix
	// every porcelain path is reported relative to.
	topBytes, err := exec.Command("git", "-C", rootDir, "rev-parse", "--show-toplevel").Output()
	if err != nil {
		return gitStatus{}
	}
	toplevel := strings.TrimRight(string(topBytes), "\n\r")
	if toplevel == "" {
		return gitStatus{}
	}

	out, err := exec.Command("git", "-C", rootDir, "status", "--porcelain").Output()
	if err != nil {
		// We *are* in a repo (rev-parse succeeded) but couldn't read
		// status. Mark the result as a repo with no known dirty files
		// so the caller at least knows we tried.
		return gitStatus{IsRepo: true, DirtyFiles: map[string]bool{}, Branch: loadGitBranch(rootDir)}
	}

	upstream, ahead, behind := loadGitTracking(rootDir)
	dirty := parsePorcelain(out, toplevel)
	return gitStatus{
		IsRepo:      true,
		DirtyFiles:  dirty,
		Branch:      loadGitBranch(rootDir),
		HasStaged:   hasStagedPorcelain(out),
		StagedFiles: stagedPorcelainSet(out, toplevel),
		HasStash:    loadGitHasStash(rootDir),
		Upstream:    upstream,
		Ahead:       ahead,
		Behind:      behind,
		// Only an UNTRACKED branch has to ask whether a remote exists at
		// all: an upstream is itself proof of one. The steady state (a
		// tracked branch) therefore pays nothing for this answer, which
		// is what keeps the extra fork off the 10-second tick for every
		// repo anyone actually works in.
		HasRemote: upstream != "" || len(loadGitRemotes(rootDir)) > 0,
	}
}

// loadGitTracking reports HEAD's upstream in short form plus how far
// ahead of / behind it the local branch sits. Best-effort like
// everything else here: no upstream, a detached HEAD, or any git failure
// yields ("", 0, 0), which callers read as "nothing to say about the
// remote".
//
// Two forks rather than one because each has an unambiguous machine
// format. The one-fork alternative — `for-each-ref` with
// %(upstream:track) — answers both questions at once but does it in
// English ("[ahead 3, behind 1]"), which is a parser waiting to break.
// The second fork is skipped entirely when there is no upstream, so the
// case that would pay for a lookup that can only fail doesn't.
func loadGitTracking(rootDir string) (upstream string, ahead, behind int) {
	if rootDir == "" {
		return "", 0, 0
	}
	out, err := exec.Command("git", "-C", rootDir, "rev-parse",
		"--abbrev-ref", "--symbolic-full-name", "@{upstream}").Output()
	if err != nil {
		return "", 0, 0
	}
	upstream = strings.TrimRight(string(out), "\n\r")
	if upstream == "" {
		return "", 0, 0
	}
	cnt, err := exec.Command("git", "-C", rootDir, "rev-list",
		"--count", "--left-right", "@{upstream}...HEAD").Output()
	if err != nil {
		return upstream, 0, 0
	}
	// --left-right counts the two halves of the symmetric difference in
	// the order the revisions were named: with "@{upstream}...HEAD" the
	// left column is the upstream's exclusive commits (we are BEHIND by
	// that many) and the right column is ours (AHEAD).
	ahead, behind = parseTrackingCounts(cnt)
	return upstream, ahead, behind
}

// parseTrackingCounts decodes rev-list's "<behind>\t<ahead>" pair.
// Split out so the column order — the one thing easy to get backwards —
// can be pinned by a test without forking git.
func parseTrackingCounts(out []byte) (ahead, behind int) {
	fields := strings.Fields(string(out))
	if len(fields) != 2 {
		return 0, 0
	}
	behind, _ = strconv.Atoi(fields[0])
	ahead, _ = strconv.Atoi(fields[1])
	return ahead, behind
}

// hasStagedPorcelain reports whether any porcelain v1 line carries an
// index-side (staged) change. The X column — the first byte of each
// line — is the index status: anything except ' ' (unmodified),
// '?' (untracked) and '!' (ignored) means `git add` has already
// touched that entry. Reuses the same ≥4-byte line guard as
// parsePorcelain so malformed tails are skipped, not misread.
func hasStagedPorcelain(out []byte) bool {
	for _, raw := range bytes.Split(out, []byte{'\n'}) {
		if len(raw) < 4 {
			continue
		}
		switch raw[0] {
		case ' ', '?', '!':
			continue
		}
		return true
	}
	return false
}

// stagedPorcelainSet returns the absolute paths of entries whose X
// column reports a staged change — the per-file refinement of
// hasStagedPorcelain, reusing parsePorcelain's path handling by feeding
// it only the staged lines. Renames therefore mark both the old and the
// new path: the staged rename involves both, and marking both keeps the
// "is anything about this path staged?" question answerable either way.
func stagedPorcelainSet(out []byte, toplevel string) map[string]bool {
	var stagedLines []byte
	for _, raw := range bytes.Split(out, []byte{'\n'}) {
		if len(raw) < 4 {
			continue
		}
		switch raw[0] {
		case ' ', '?', '!':
			continue
		}
		stagedLines = append(stagedLines, raw...)
		stagedLines = append(stagedLines, '\n')
	}
	return parsePorcelain(stagedLines, toplevel)
}

// loadGitHasStash reports whether the repo has at least one stash entry.
// rev-parse --verify on refs/stash is the cheapest possible probe (no
// list formatting, exits non-zero when the ref is absent) and shares the
// best-effort contract: any failure reads as "no stash".
func loadGitHasStash(rootDir string) bool {
	if rootDir == "" {
		return false
	}
	err := exec.Command("git", "-C", rootDir, "rev-parse", "--verify", "--quiet", "refs/stash").Run()
	return err == nil
}

// loadGitBranch returns the current branch name for rootDir, or a short
// commit SHA when HEAD is detached (rebase / bisect / a manual checkout
// of a tag). Returns "" for non-repos and any other failure mode — the
// caller treats that as "no branch label to show" and the status bar
// just doesn't render one.
//
// We try `symbolic-ref --short HEAD` first because it's the cheapest way
// to distinguish "on a branch" from "detached"; the fallback to
// `rev-parse --short HEAD` only fires when symbolic-ref's non-zero exit
// tells us we're detached.
func loadGitBranch(rootDir string) string {
	if rootDir == "" {
		return ""
	}
	if out, err := exec.Command("git", "-C", rootDir, "symbolic-ref", "--short", "HEAD").Output(); err == nil {
		return strings.TrimRight(string(out), "\n\r")
	}
	if out, err := exec.Command("git", "-C", rootDir, "rev-parse", "--short", "HEAD").Output(); err == nil {
		return strings.TrimRight(string(out), "\n\r")
	}
	return ""
}

// parsePorcelain converts the bytes returned by `git status --porcelain`
// into a set of absolute file paths. Split out from loadGitStatus so it
// can be exercised by tests without spawning a subprocess.
//
// The porcelain v1 format (without -z) is:
//
//	XY <path>
//	XY <oldpath> -> <newpath>      (renames / copies)
//	XY "quoted path with spaces"   (when core.quotePath is on, the default)
//
// We treat any line as dirty regardless of the X/Y status codes; for renames
// we mark both the old and new paths so the user sees both rows tinted.
func parsePorcelain(out []byte, toplevel string) map[string]bool {
	dirty := map[string]bool{}
	for _, raw := range bytes.Split(out, []byte{'\n'}) {
		line := string(raw)
		if len(line) < 4 {
			continue
		}
		// Drop the two status chars + the separating space.
		body := line[3:]

		if idx := strings.Index(body, " -> "); idx >= 0 {
			oldPath := unquotePath(body[:idx])
			newPath := unquotePath(body[idx+len(" -> "):])
			if oldPath != "" {
				dirty[filepath.Join(toplevel, oldPath)] = true
			}
			if newPath != "" {
				dirty[filepath.Join(toplevel, newPath)] = true
			}
			continue
		}

		path := unquotePath(body)
		if path == "" {
			continue
		}
		dirty[filepath.Join(toplevel, path)] = true
	}
	return dirty
}

// unquotePath undoes git's C-style quoting (enabled by default via
// core.quotePath) so paths with spaces, unicode, or control chars come
// back as a normal Go string. Falls back to the raw input on any parse
// error — that's safer than dropping a path the user might want flagged.
func unquotePath(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return ""
	}
	if !strings.HasPrefix(s, `"`) {
		return s
	}
	if unq, err := strconv.Unquote(s); err == nil {
		return unq
	}
	return s
}

// dirtyFolderSet rolls a set of dirty file paths up to every ancestor
// folder under root. A folder is "dirty" if any of its descendants are
// dirty, so collapsed branches still signal that there's something
// changed inside.
func dirtyFolderSet(dirtyFiles map[string]bool, root string) map[string]bool {
	folders := map[string]bool{}
	if len(dirtyFiles) == 0 {
		return folders
	}
	root = filepath.Clean(root)
	for path := range dirtyFiles {
		// Walk up from each dirty file's parent toward the root,
		// marking every ancestor inside the project. The walk halts
		// the moment we step outside root so a file outside the
		// editor's scope can't paint folders we don't render.
		for p := filepath.Dir(path); p != "" && p != "."; p = filepath.Dir(p) {
			if !pathInside(p, root) {
				break
			}
			if folders[p] {
				break // already marked by a sibling — skip the rest.
			}
			folders[p] = true
			if p == root {
				break
			}
		}
	}
	return folders
}

// pathInside reports whether candidate is root or a descendant of root.
// Uses filepath.Rel rather than string-prefix matching so '/foo/bar'
// isn't considered inside '/foo/ba'.
func pathInside(candidate, root string) bool {
	rel, err := filepath.Rel(root, candidate)
	if err != nil {
		return false
	}
	if rel == "." {
		return true
	}
	return !strings.HasPrefix(rel, "..")
}
