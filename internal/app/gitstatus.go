// =============================================================================
// File: internal/app/gitstatus.go
// Author: Rohan Allison <rohanthewiz@gmail.com>
// Created: 2026-04-30
// Copyright: 2026 Rohan Allison. All rights reserved.
// Portions copyright 2026 Cloudmanic, LLC. Original author: Spicer Matthews.
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
// We never spam errors at the user and never retry on failure.
//
// TWO FORKS, NOT SIX (N-127). The snapshot used to ask six questions in
// six processes — toplevel + git dir, `status --porcelain`, the upstream,
// the ahead/behind counts, the branch name, the stash ref — and each git
// process costs 35–50 ms to START on a macOS box before it does any
// work, so a snapshot cost ~270 ms whatever the repo's size. Now:
//
//	rev-parse --show-toplevel --absolute-git-dir --glob=refs/stas[h]
//	    → toplevel, git dir, and (a third line) the stash ref if any
//	status --porcelain=v2 --branch -z
//	    → every changed path, plus "# branch.head / .upstream / .ab"
//	      headers that answer branch, upstream, ahead and behind
//
// Conditional forks remain only for states that are rare or that already
// had them: a detached HEAD (its short SHA), a branch with no upstream
// (does any remote exist?), and unmerged files (their marker sizes).
//
// Why the stash rides rev-parse rather than `status --show-stash`:
// porcelain v2 prints its "# stash N" header only from git 2.35, and
// Ubuntu 22.04 ships 2.34 — there the header would be silently absent and
// "Pop stash" would never light up. `--glob` lists refs on every version.
// The `[h]` is load-bearing: a --glob pattern with no glob character is
// anchored with "/*" (it would ask for refs UNDER refs/stash/), so the
// one-character class is what makes the pattern match refs/stash itself
// and nothing else.
//
// Why -z: porcelain v2 without -z still C-quotes "unusual" paths
// (non-ASCII, control characters), and a rename separates its two paths
// with a TAB that a filename may also contain. With -z every path is
// printed raw and NUL-terminated, so there is nothing to unquote and no
// separator a path can forge.

package app

import (
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
)

// gitStashGlob is the rev-parse --glob pattern that matches exactly
// refs/stash (see the file header for why the bracket is required).
const gitStashGlob = "refs/stas[h]"

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
	// StagedFiles holds the absolute paths whose index side (the X of
	// the XY pair) carries a staged change. It gates the "Unstage file"
	// menu row and lets the git panel draw per-file stage checkboxes
	// without forking git per row. Renames mark both paths, matching
	// DirtyFiles.
	StagedFiles map[string]bool
	// HasStash reports whether refs/stash exists — i.e. `git stash pop`
	// has something to pop. Gates the "Pop stash" menu row.
	HasStash bool
	// Upstream is HEAD's tracked remote-tracking branch in short form
	// ("origin/main"), or "" when the branch has no upstream, the
	// upstream ref no longer exists, HEAD is detached, or we couldn't
	// tell. Ahead / Behind count the commits HEAD has that the upstream
	// doesn't and vice versa; both are 0 whenever Upstream is empty,
	// because there is nothing to measure against. They feed the status
	// bar's push indicator and seed the push dialog (gitpush.go).
	Upstream      string
	Ahead, Behind int
	// HasRemote reports whether the repo has at least one remote
	// configured — the difference between "this branch isn't pushed
	// yet" and "there is nowhere to push". Gates the push entry points
	// so a purely local repo never offers a dialog whose first dropdown
	// would be empty.
	HasRemote bool
	// ConflictedFiles holds the absolute paths git reports as UNMERGED —
	// a cherry-pick, revert, merge or rebase stopped on them and neither
	// side has been chosen yet. Read out of the status output the
	// snapshot already has, so knowing "is this repo mid-conflict?" costs
	// no extra fork; it gates the Resolve conflicts row (gitconflict.go).
	// The live list the conflict picker acts on is re-read at open time —
	// a snapshot up to ten seconds old is fine for enabling a row and far
	// too stale to run `git add` against.
	ConflictedFiles map[string]bool
	// ConflictMarkerSizes is the `conflict-marker-size` gitattribute of
	// each UNMERGED file whose markers are not git's default 7, keyed by
	// absolute path (gitopstate.go's loadConflictMarkerSizes). Asked only
	// while something is unmerged, so the 10-second tick in a repo that is
	// not parked pays no extra fork.
	ConflictMarkerSizes map[string]int
	// GitDir is the repository's absolute git directory (.git, or the
	// worktree's private dir), and InProgress the operation parked in it
	// — "cherry-pick", "merge", "revert", "rebase", or "" for none. Both
	// ride the rev-parse the snapshot already runs (one fork answers
	// toplevel AND git dir), and InProgress is a handful of stats against
	// it, so "is the repo parked?" is free on the 10-second tick. It
	// feeds the status bar's ⚠ segment and the Conflicts panel's header
	// — the answer to "nothing on screen says the repo is stopped".
	GitDir     string
	InProgress string
	// PanelFiles is the git panel's row list — the changed entries under
	// the project root, in status order, with their XY codes — built
	// from the same status output. The panel used to fork its own
	// `git status` every time the snapshot landed; reading the list off
	// the snapshot keeps that fork off the main loop (gitpanel.go's
	// setGitPanelFiles).
	PanelFiles []gitPanelFile
}

// loadGitStatus inspects rootDir and returns the snapshot described on
// gitStatus. A non-git directory yields the zero value (IsRepo=false, no
// dirty paths). Any failure of the underlying commands degrades the same
// way — we'd rather lose the dirty highlight than crash the editor over a
// transient git issue.
//
// Safe to call off the main loop: it reads nothing but its argument and
// the disk, and returns a value. gitsnapshot.go runs it on a goroutine.
func loadGitStatus(rootDir string) gitStatus {
	if rootDir == "" {
		return gitStatus{}
	}

	// rev-parse --show-toplevel does double duty: it tells us whether
	// we're in a git work tree at all (non-zero exit otherwise) and
	// gives us the absolute path of the repo root, which is the prefix
	// every status path is reported relative to.
	//
	// --absolute-git-dir rides the same fork (rev-parse prints one answer
	// per line in argument order), and so does the stash probe: --glob
	// prints the stash's SHA on a third line when refs/stash exists and
	// nothing at all when it doesn't — never an error, which is why it
	// can share a fork whose failure means "not a repo".
	revParse, err := exec.Command("git", "-C", rootDir, "rev-parse",
		"--show-toplevel", "--absolute-git-dir", "--glob="+gitStashGlob).Output()
	if err != nil {
		return gitStatus{}
	}
	toplevel, gitDir := splitRevParseDirs(revParse)
	if toplevel == "" {
		return gitStatus{}
	}
	hasStash := revParseHasStash(revParse)

	out, err := exec.Command("git", "-C", rootDir, "status", "--porcelain=v2", "--branch", "-z").Output()
	if err != nil {
		// We *are* in a repo (rev-parse succeeded) but couldn't read
		// status. Mark the result as a repo with no known dirty files
		// so the caller at least knows we tried.
		return gitStatus{IsRepo: true, DirtyFiles: map[string]bool{}, Branch: loadGitBranch(rootDir),
			HasStash: hasStash, GitDir: gitDir, InProgress: gitOpFromGitDir(gitDir)}
	}

	p := parsePorcelainV2(out)
	dirty, staged, conflicted := p.pathSets(toplevel)
	var markerSizes map[string]int
	if len(conflicted) > 0 {
		paths := make([]string, 0, len(conflicted))
		for p := range conflicted {
			paths = append(paths, p)
		}
		markerSizes = loadConflictMarkerSizes(toplevel, paths)
	}
	// The branch header names the branch, or says "(detached)". A
	// detached HEAD (a rebase stop, a bisect, a checked-out tag) is the
	// one case that pays a fork for its label, so the short SHA keeps
	// git's own abbreviation length (core.abbrev grows it in big repos)
	// rather than a guessed seven characters.
	branch := p.Head
	switch {
	case p.Detached:
		branch = loadGitShortHead(rootDir)
	case branch == "":
		branch = loadGitBranch(rootDir) // no header at all: ask the old way
	}
	return gitStatus{
		IsRepo:      true,
		DirtyFiles:  dirty,
		Branch:      branch,
		HasStaged:   len(staged) > 0,
		StagedFiles: staged,
		HasStash:    hasStash,
		Upstream:    p.Upstream,
		Ahead:       p.Ahead,
		Behind:      p.Behind,
		// Only an UNTRACKED branch has to ask whether a remote exists at
		// all: an upstream is itself proof of one. The steady state (a
		// tracked branch) therefore pays nothing for this answer, which
		// is what keeps the extra fork off the 10-second tick for every
		// repo anyone actually works in.
		HasRemote:           p.Upstream != "" || len(loadGitRemotes(rootDir)) > 0,
		ConflictedFiles:     conflicted,
		ConflictMarkerSizes: markerSizes,
		GitDir:              gitDir,
		InProgress:          gitOpFromGitDir(gitDir),
		PanelFiles:          gitPanelFilesFrom(p.Entries, toplevel, rootDir),
	}
}

// splitRevParseDirs reads `rev-parse --show-toplevel --absolute-git-dir`
// output: the toplevel on the first line, the git dir on the second.
// Either may come back empty (a bare repo has no toplevel), which the
// caller reads as "not a work tree" for the first and "nothing parked"
// for the second.
func splitRevParseDirs(out []byte) (toplevel, gitDir string) {
	lines := strings.Split(strings.TrimRight(string(out), "\n\r"), "\n")
	if len(lines) > 0 {
		toplevel = strings.TrimRight(lines[0], "\r")
	}
	if len(lines) > 1 {
		gitDir = strings.TrimRight(lines[1], "\r")
	}
	return toplevel, gitDir
}

// revParseHasStash reads the snapshot's rev-parse output for the stash
// probe: --glob prints refs/stash's SHA on the line after the git dir
// when the ref exists, and nothing when it doesn't. Split out so the
// line arithmetic is pinned without forking git.
func revParseHasStash(out []byte) bool {
	lines := strings.Split(strings.TrimRight(string(out), "\n\r"), "\n")
	return len(lines) > 2 && strings.TrimSpace(lines[2]) != ""
}

// isPorcelainConflict reports whether an XY status pair marks an
// unmerged entry. Porcelain spells "unmerged" in seven XY pairs rather
// than one flag: either side may be 'U' (UU both modified, AU / UA added
// by one side, DU / UD deleted by one side), plus the two same-letter
// pairs git defines as conflicts even though neither column says U — AA
// (both added) and DD (both deleted). Testing only for 'U' would silently
// miss the add/add case, which is the common one when two branches create
// the same file.
//
// The snapshot no longer needs it — porcelain v2 gives unmerged entries a
// line type of their own ('u') — but the conflict picker's live read
// (gitopstate.go's parseConflictStatusZ) still speaks v1 and does.
func isPorcelainConflict(x, y byte) bool {
	return x == 'U' || y == 'U' || (x == 'A' && y == 'A') || (x == 'D' && y == 'D')
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
//
// The snapshot reads the branch off status's "# branch.head" header now,
// so this is only its fallback — and the direct answer for callers that
// want the label without a whole snapshot.
func loadGitBranch(rootDir string) string {
	if rootDir == "" {
		return ""
	}
	if out, err := exec.Command("git", "-C", rootDir, "symbolic-ref", "--short", "HEAD").Output(); err == nil {
		return strings.TrimRight(string(out), "\n\r")
	}
	return loadGitShortHead(rootDir)
}

// loadGitShortHead returns HEAD's abbreviated commit SHA, "" on any
// failure (an unborn branch has no commit to name). The label for a
// detached HEAD, shared by loadGitBranch and the snapshot.
func loadGitShortHead(rootDir string) string {
	if rootDir == "" {
		return ""
	}
	if out, err := exec.Command("git", "-C", rootDir, "rev-parse", "--short", "HEAD").Output(); err == nil {
		return strings.TrimRight(string(out), "\n\r")
	}
	return ""
}

// porcelainEntry is one changed path from `git status --porcelain=v2 -z`.
type porcelainEntry struct {
	// XY is the status pair in porcelain v1's SPELLING — ' ' where v2
	// writes '.' for "unmodified", and "??" for an untracked path —
	// because the git panel draws the code verbatim and every reader of
	// XY codes in this package (gitStageState, the conflict kinds, the
	// panel's actions) speaks v1. The letters themselves are the same in
	// both versions.
	XY string
	// Path is relative to the repository's top level. Orig is the source
	// path of a rename or copy, "" otherwise.
	Path, Orig string
	// Unmerged marks a 'u' line: the path is in conflict.
	Unmerged bool
}

// porcelainStatus is what parsePorcelainV2 reads out of one status run.
type porcelainStatus struct {
	// Head is the "# branch.head" value — the branch name, including an
	// unborn branch's — or "" when HEAD is detached or the header was
	// absent. Detached records the "(detached)" spelling.
	Head     string
	Detached bool
	// Upstream is "# branch.upstream", kept ONLY when "# branch.ab"
	// followed it. git prints the upstream's name whenever one is
	// configured but the ab counts only when that ref actually exists;
	// a configured-but-gone upstream (the remote branch was deleted)
	// therefore reads as no upstream, which is what the old
	// `rev-parse @{upstream}` answer said and what the push dialog needs.
	Upstream      string
	Ahead, Behind int
	Entries       []porcelainEntry
}

// parsePorcelainV2 reads `git status --porcelain=v2 --branch -z` output.
// Pure: split out from loadGitStatus so the format is pinned without
// forking git.
//
// The -z stream is NUL-separated tokens (diagram of one of each kind):
//
//	# branch.head main                      header: "# <key> <value>"
//	1 .M N... 100644 100644 100644 <hH> <hI> <path>          ordinary
//	2 R. N... 100644 100644 100644 <hH> <hI> R100 <path>     renamed …
//	<origPath>                                                … its source
//	u UU N... <m1> <m2> <m3> <mW> <h1> <h2> <h3> <path>      unmerged
//	? <path>                                                 untracked
//
// The fields before the path never contain spaces, so splitting the
// first N fields off leaves the path whole even when it has spaces.
func parsePorcelainV2(out []byte) porcelainStatus {
	var p porcelainStatus
	upstream, haveAB := "", false
	tokens := strings.Split(string(out), "\x00")
	for i := 0; i < len(tokens); i++ {
		tok := tokens[i]
		if len(tok) < 3 {
			continue // the trailing empty token, or junk
		}
		switch tok[0] {
		case '#':
			key, val, _ := strings.Cut(strings.TrimPrefix(tok, "# "), " ")
			switch key {
			case "branch.head":
				if val == "(detached)" {
					p.Detached = true
				} else {
					p.Head = val
				}
			case "branch.upstream":
				upstream = val
			case "branch.ab":
				haveAB = true
				p.Ahead, p.Behind = parseBranchAB(val)
			}
		case '1':
			if f := strings.SplitN(tok, " ", 9); len(f) == 9 {
				p.Entries = append(p.Entries, porcelainEntry{XY: v1XY(f[1]), Path: f[8]})
			}
		case '2':
			// The source path is the NEXT token. It is consumed even
			// when this line turns out malformed: left in the stream, a
			// source named "1 a b …" or "? x" would be read as an entry
			// of its own.
			orig := ""
			if i+1 < len(tokens) {
				i++
				orig = tokens[i]
			}
			if f := strings.SplitN(tok, " ", 10); len(f) == 10 {
				p.Entries = append(p.Entries, porcelainEntry{XY: v1XY(f[1]), Path: f[9], Orig: orig})
			}
		case 'u':
			if f := strings.SplitN(tok, " ", 11); len(f) == 11 {
				p.Entries = append(p.Entries, porcelainEntry{XY: v1XY(f[1]), Path: f[10], Unmerged: true})
			}
		case '?':
			p.Entries = append(p.Entries, porcelainEntry{XY: "??", Path: tok[2:]})
		}
		// '!' (ignored) lines appear only with --ignored, which the
		// snapshot never passes; anything else is a newer git's line
		// type and is skipped rather than guessed at.
	}
	if haveAB {
		p.Upstream = upstream
	}
	return p
}

// v1XY respells a v2 XY pair in v1's alphabet: v2 writes '.' for an
// unmodified side where v1 writes a space.
func v1XY(xy string) string {
	return strings.ReplaceAll(xy, ".", " ")
}

// parseBranchAB decodes the "# branch.ab" value, "+<ahead> -<behind>".
// The signs are part of the spelling, not of the numbers (behind is
// printed "-3" and means three commits behind). Malformed input reads as
// level, the same degrade the rest of the snapshot uses.
func parseBranchAB(val string) (ahead, behind int) {
	f := strings.Fields(val)
	if len(f) != 2 {
		return 0, 0
	}
	ahead, _ = strconv.Atoi(strings.TrimPrefix(f[0], "+"))
	behind, _ = strconv.Atoi(strings.TrimPrefix(f[1], "-"))
	return ahead, behind
}

// pathSets turns the entries into the snapshot's three absolute-path
// sets.
//
//   - dirty: every entry, and both sides of a rename, so the user sees
//     both tree rows tinted.
//   - staged: entries whose index side (X) is not ' ' (unmodified) or
//     '?' (untracked). Unmerged entries count, because their X is a
//     letter: v1's reading, kept so "Commit staged" and "Unstage file"
//     light up during a conflict exactly as they always have.
//   - conflicted: the 'u' entries.
//
// All three are non-nil, empty when nothing matches, so callers can
// treat absence of a key as "no" without a nil check.
func (p porcelainStatus) pathSets(toplevel string) (dirty, staged, conflicted map[string]bool) {
	dirty, staged, conflicted = map[string]bool{}, map[string]bool{}, map[string]bool{}
	for _, e := range p.Entries {
		paths := []string{e.Path}
		if e.Orig != "" {
			paths = append(paths, e.Orig)
		}
		isStaged := len(e.XY) == 2 && e.XY[0] != ' ' && e.XY[0] != '?'
		for _, rel := range paths {
			if rel == "" {
				continue
			}
			abs := filepath.Join(toplevel, rel)
			dirty[abs] = true
			if isStaged {
				staged[abs] = true
			}
		}
		if e.Unmerged && e.Path != "" {
			conflicted[filepath.Join(toplevel, e.Path)] = true
		}
	}
	return dirty, staged, conflicted
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
	return !relEscapes(rel)
}

// relEscapes reports whether rel — a filepath.Rel answer — climbs OUT of
// its base: exactly "..", or a ".." segment followed by a separator.
//
// The tempting `strings.HasPrefix(rel, "..")` is wrong for a child whose
// NAME starts with two dots: "..notes" (or "..notes/a.txt") is inside the
// base, but the bare prefix test calls it outside. filepath.Rel only ever
// emits ".." as a whole leading segment, so checking the segment is exact.
// Every "is this outside?" test on a Rel answer goes through here.
func relEscapes(rel string) bool {
	return rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator))
}
