// =============================================================================
// File: internal/app/gitdiff.go
// Author: Rohan Allison <rohanthewiz@gmail.com>
// Created: 2026-07-08
// Copyright: 2026 Rohan Allison. All rights reserved.
// =============================================================================

// gitdiff.go implements the diff gutter: per-open-tab `git diff -U0`
// runs parsed into added / modified / deleted hunks, rendered as
// colored marks in the editor's gutter mark column via the decoration
// layer, plus next/previous-hunk navigation.
//
// The moving parts, following the two house patterns they build on:
//
//   - Best-effort git (gitstatus.go's rule): any git failure — not a
//     repo, no git on PATH, file untracked — yields no hunks and the
//     editor renders normally. Never block, never error at the user.
//   - Custom tcell events for goroutine → main-loop messaging:
//     requestFileDiff shells out on a goroutine and posts a
//     gitDiffEvent; only the main loop touches App.fileDiffs.
//
// Diffs refresh on file open, after every save, and on the existing
// 10-second tick (refreshTreeNow), so external changes (a git commit
// in another pane clearing the gutter) appear within a tick.
//
// THE TICK SKIPS WHAT CANNOT HAVE CHANGED (N-127 part d). `git diff`
// with no revision compares the WORK TREE with the INDEX, so its answer
// for a path is a function of two files: the path itself and the
// repository's index. Each diff records both files' disk stamps
// (diffStamp: mtime, size, inode) as they stood BEFORE git ran, and the
// tick skips a path whose two stamps still match. A commit, a checkout,
// a `git add`, a stash, an edit from another pane — every one rewrites
// the file or the index, so every one still re-diffs. HEAD moving alone
// (`reset --soft`, a fetch) changes nothing this diff reads, so it
// rightly doesn't.
//
// A stamp younger than editor.RacyGranule is never recorded, git's own
// "racily clean" rule: a second write inside the filesystem clock's tick
// can keep the mtime AND the size, and the skip would then hide it. Only
// the TICK consults the stamps; open, save and git-command refreshes
// always ask git, and record what they asked about.

package app

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	"github.com/rohanthewiz/ced/internal/editor"
	"github.com/rohanthewiz/ced/internal/theme"
)

// diffKind classifies one hunk of working-tree change.
type diffKind uint8

const (
	diffAdded    diffKind = iota // lines that don't exist upstream
	diffModified                 // lines changed in place
	diffDeleted                  // lines removed — marked on the boundary line
)

// diffHunk is one contiguous changed region in the working file.
// Start/End are 0-based line indexes, inclusive, in the file as it is
// NOW (the "+" side of the diff) — which is the coordinate space the
// editor renders, so no translation happens at draw time. A deleted
// hunk has Start == End pointing at the line the content was removed
// after; there's no cell for text that no longer exists, so the
// boundary line carries the mark.
//
// Body keeps the hunk's own `-` / `+` lines (and git's "\ No newline"
// notes) verbatim, prefix included, for the change popup (hunktip.go):
// the gutter answers WHERE a file changed, and Body is what lets
// hovering the bar answer WHAT changed without a second git run. With
// -U0 there are no context lines, so Body is exactly the change.
//
// Top marks a deletion git reported at "+0,0": the removed lines were
// the very first ones, which the clamped boundary (line 0) can't say on
// its own — line 0 is also "after the first line" for a gap below it.
type diffHunk struct {
	Start, End int
	Kind       diffKind
	Top        bool
	Body       []string
}

// parseUnifiedDiff extracts hunks from `git diff -U0` output. With
// zero context lines every @@ header describes exactly one changed
// region, so we only read the headers and ignore the +/- body lines:
//
//	@@ -oldStart[,oldCount] +newStart[,newCount] @@
//
//	oldCount == 0 → pure insertion  → added,   newStart..+newCount-1
//	newCount == 0 → pure deletion   → deleted, boundary line newStart-1
//	both > 0      → replacement     → modified, newStart..+newCount-1
//
// Counts default to 1 when the ",count" suffix is omitted (git's
// shorthand for single-line hunks). Malformed lines are skipped —
// best-effort all the way down.
//
// Body lines are collected only AFTER a header has been accepted: the
// file header's "--- a/f" / "+++ b/f" come before the first @@ and must
// not be mistaken for a removed / added line, and a malformed header
// (skipped) must not lend its body to the hunk before it.
func parseUnifiedDiff(out []byte) []diffHunk {
	var hunks []diffHunk
	inHunk := false
	for _, raw := range bytes.Split(out, []byte{'\n'}) {
		line := string(raw)
		if len(line) < 4 || line[0] != '@' || line[1] != '@' {
			if inHunk && line != "" {
				switch line[0] {
				case '-', '+', '\\':
					h := &hunks[len(hunks)-1]
					h.Body = append(h.Body, line)
				}
			}
			continue
		}
		inHunk = false
		_, oldCount, newStart, newCount, ok := parseHunkHeader(line)
		if !ok {
			continue
		}
		inHunk = true
		switch {
		case newCount == 0:
			// git reports the 1-based line BEFORE the gap on the new
			// side; 0 means the deletion is at the very top of the file.
			boundary := newStart - 1
			if boundary < 0 {
				boundary = 0
			}
			hunks = append(hunks, diffHunk{Start: boundary, End: boundary, Kind: diffDeleted, Top: newStart == 0})
		case oldCount == 0:
			hunks = append(hunks, diffHunk{Start: newStart - 1, End: newStart - 1 + newCount - 1, Kind: diffAdded})
		default:
			hunks = append(hunks, diffHunk{Start: newStart - 1, End: newStart - 1 + newCount - 1, Kind: diffModified})
		}
	}
	return hunks
}

// parseHunkHeader pulls the four numbers out of one "@@ -a,b +c,d @@"
// line. Hand-rolled rather than regexp because this runs for every
// hunk of every open tab on every tick — and the format is rigid
// enough that a scanner is both faster and no harder to read.
func parseHunkHeader(line string) (oldStart, oldCount, newStart, newCount int, ok bool) {
	i := 2 // skip "@@"
	skipSpaces := func() {
		for i < len(line) && line[i] == ' ' {
			i++
		}
	}
	readInt := func() (int, bool) {
		start := i
		n := 0
		for i < len(line) && line[i] >= '0' && line[i] <= '9' {
			n = n*10 + int(line[i]-'0')
			i++
		}
		return n, i > start
	}
	readPair := func(sigil byte) (int, int, bool) {
		skipSpaces()
		if i >= len(line) || line[i] != sigil {
			return 0, 0, false
		}
		i++
		start, okStart := readInt()
		if !okStart {
			return 0, 0, false
		}
		count := 1 // git omits ",1"
		if i < len(line) && line[i] == ',' {
			i++
			var okCount bool
			count, okCount = readInt()
			if !okCount {
				return 0, 0, false
			}
		}
		return start, count, true
	}
	oldStart, oldCount, okOld := readPair('-')
	newStart, newCount, okNew := readPair('+')
	if !okOld || !okNew {
		return 0, 0, 0, 0, false
	}
	return oldStart, oldCount, newStart, newCount, true
}

// loadFileDiff runs `git diff -U0` for one file and returns its hunks.
// nil means "no changes or no answer" — untracked files, non-repos,
// and git errors all land there, and the gutter simply stays blank.
func loadFileDiff(rootDir, path string) []diffHunk {
	if rootDir == "" || path == "" {
		return nil
	}
	out, err := exec.Command("git", "-C", rootDir, "diff", "-U0", "--no-color", "--", path).Output()
	if err != nil {
		return nil
	}
	return parseUnifiedDiff(out)
}

// gitDiffEvent carries one file's freshly-computed hunks from the
// background goroutine to the main loop — the same messaging pattern
// every other background job here uses (never mutate App off-loop).
// stamp / stamped carry the disk versions the diff was asked about (see
// the file header); stamped=false means "don't trust a skip on this".
type gitDiffEvent struct {
	when    time.Time
	path    string
	hunks   []diffHunk
	stamp   diffStamp
	stamped bool
}

// When satisfies the tcell.Event interface.
func (e *gitDiffEvent) When() time.Time { return e.when }

// diffStamp is what a path's gutter diff was computed against: the
// file's own disk version and the repository index's. Equal stamps mean
// `git diff -- path` would print the same thing again.
type diffStamp struct {
	file, index editor.DiskStamp
}

// same reports whether two stamps describe the same pair of versions.
func (s diffStamp) same(o diffStamp) bool {
	return s.file.Same(o.file) && s.index.Same(o.index)
}

// diffStampFor stats path and the repository index and returns their
// stamps, ok=false when a skip could not be trusted: no git dir known
// yet (the first snapshot hasn't landed), either stat failed (an
// untracked path's diff is empty anyway, and a missing index means git
// has nothing to compare), or either mtime is younger than the racy
// window. Main loop: it reads a.gitDir.
func (a *App) diffStampFor(path string, now time.Time) (diffStamp, bool) {
	if a.gitDir == "" || path == "" {
		return diffStamp{}, false
	}
	fi, err := os.Stat(path)
	if err != nil {
		return diffStamp{}, false
	}
	ii, err := os.Stat(filepath.Join(a.gitDir, "index"))
	if err != nil {
		return diffStamp{}, false
	}
	cutoff := now.Add(-editor.RacyGranule)
	if !fi.ModTime().Before(cutoff) || !ii.ModTime().Before(cutoff) {
		return diffStamp{}, false
	}
	return diffStamp{file: editor.StampOf(fi), index: editor.StampOf(ii)}, true
}

// requestFileDiff computes path's diff on a goroutine and posts the
// result. Fire-and-forget: a dropped event (screen shutting down)
// just means the gutter waits for the next tick. Always asks git; the
// stamps it takes first are recorded when the answer lands, so the next
// tick can skip this path if nothing moves.
func (a *App) requestFileDiff(path string) {
	if path == "" || a.screen == nil {
		return
	}
	stamp, ok := a.diffStampFor(path, time.Now())
	a.requestFileDiffStamped(path, stamp, ok)
}

// requestFileDiffStamped launches the diff with stamps already taken —
// taken BEFORE git runs, so a write that lands while it runs leaves a
// stamp that no longer matches and the next tick asks again.
func (a *App) requestFileDiffStamped(path string, stamp diffStamp, stamped bool) {
	if path == "" || a.screen == nil {
		return
	}
	scr := a.screen
	root := a.rootDir
	go func() {
		hunks := loadFileDiff(root, path)
		_ = scr.PostEvent(&gitDiffEvent{when: time.Now(), path: path, hunks: hunks, stamp: stamp, stamped: stamped})
	}()
}

// requestOpenTabDiffs refreshes the diff of every open file tab.
// Called from the 10-second tick so gutters track external git
// activity (commits, checkouts in another pane) without a save. A tab
// whose file and index stamps match the ones its current hunks were
// computed against is skipped (the file header's rule); stamps of tabs
// no longer open are dropped on the way.
func (a *App) requestOpenTabDiffs() {
	now := time.Now()
	open := map[string]bool{}
	for _, t := range a.tabs {
		if t.Path == "" || t.IsImage() {
			continue
		}
		open[t.Path] = true
		stamp, ok := a.diffStampFor(t.Path, now)
		if ok {
			if prev, had := a.fileDiffStamps[t.Path]; had && prev.same(stamp) {
				continue
			}
		}
		a.requestFileDiffStamped(t.Path, stamp, ok)
	}
	for p := range a.fileDiffStamps {
		if !open[p] {
			delete(a.fileDiffStamps, p)
		}
	}
}

// handleGitDiff stores a completed diff, keyed by path. Runs on the
// main loop only. The map is lazily created so tests that assemble an
// App by hand don't need extra setup.
func (a *App) handleGitDiff(e *gitDiffEvent) {
	if a.fileDiffs == nil {
		a.fileDiffs = map[string][]diffHunk{}
	}
	// Record what this answer was computed against — or forget, when
	// the stamps couldn't be trusted, so the next tick asks again.
	if e.stamped {
		if a.fileDiffStamps == nil {
			a.fileDiffStamps = map[string]diffStamp{}
		}
		a.fileDiffStamps[e.path] = e.stamp
	} else {
		delete(a.fileDiffStamps, e.path)
	}
	// A disk diff replaces whatever a hunk revert had made exact for the
	// buffer (hunkview.go): these hunks are measured on the saved file.
	delete(a.fileDiffsRev, e.path)
	if len(e.hunks) == 0 {
		delete(a.fileDiffs, e.path)
		return
	}
	// git hands back the file's BYTES; the buffer holds text. Decoded
	// once here, on arrival, so the popup, the change view and above all
	// a revert (which writes the "-" lines into the buffer) all read the
	// file the way its tab does (encoding.go's decodeDiffLine).
	enc := ""
	if t := a.tabByPath(e.path); t != nil {
		enc = t.Encoding
	}
	decodeHunkBodies(enc, e.hunks)
	a.fileDiffs[e.path] = e.hunks
}

// -----------------------------------------------------------------------------
// Decoration source — the render-side half
// -----------------------------------------------------------------------------

// Gutter mark glyphs. A vertical bar for lines that exist (added /
// modified) and a low underscore-bar for the deletion boundary — "the
// missing lines were below here".
const (
	glyphChanged = '▎'
	glyphDeleted = '▁'
)

// gitDiffSource adapts App.fileDiffs into decoration gutter marks.
// One instance per tab, registered at open; it holds the App pointer
// because diffs are app state (they're keyed by path and outlive tab
// switches), while the decoration layer only knows tabs.
type gitDiffSource struct {
	app *App
}

// Decorations emits one gutter mark per changed line in the visible
// window. No spans — the diff gutter deliberately never recolors code.
func (s gitDiffSource) Decorations(t *editor.Tab, th theme.Theme, firstLine, lastLine int) ([]editor.Span, []editor.GutterMark) {
	hunks := s.app.fileDiffs[t.Path]
	if len(hunks) == 0 {
		return nil, nil
	}
	var marks []editor.GutterMark
	for _, h := range hunks {
		glyph, fg := glyphChanged, th.GitModified
		switch h.Kind {
		case diffAdded:
			fg = th.GitAdded
		case diffDeleted:
			glyph, fg = glyphDeleted, th.GitDeleted
		}
		for line := max(h.Start, firstLine); line <= h.End && line <= lastLine; line++ {
			marks = append(marks, editor.GutterMark{Line: line, Glyph: glyph, FG: fg})
		}
	}
	return nil, marks
}

// -----------------------------------------------------------------------------
// Hunk navigation — menu actions + Esc-h / Esc-H leaders
// -----------------------------------------------------------------------------

// hasDiffHunks is the menu predicate for the hunk-navigation rows:
// enabled only when the active tab has known changes to jump between.
func (a *App) hasDiffHunks() bool {
	t := a.activeTabPtr()
	return t != nil && len(a.fileDiffs[t.Path]) > 0
}

// menuNextHunk jumps the cursor to the start of the next changed
// region after the cursor line, wrapping to the first hunk past the
// end of the file — same wrap contract as find's next-match.
func (a *App) menuNextHunk() {
	a.closeMenu()
	a.jumpHunk(+1)
}

// menuPrevHunk jumps to the previous changed region, wrapping to the
// last hunk when the cursor sits above every change.
func (a *App) menuPrevHunk() {
	a.closeMenu()
	a.jumpHunk(-1)
}

// jumpHunk moves the cursor to the nearest hunk start in direction
// dir (+1 next / -1 previous), wrapping around the file. Hunks are
// already in document order because git emits them that way and the
// parser preserves it. Cursor lands at column 0 of the hunk's first
// line — the change is what the eye should catch, not a column.
func (a *App) jumpHunk(dir int) {
	t := a.activeTabPtr()
	if t == nil {
		return
	}
	hunks := a.fileDiffs[t.Path]
	if len(hunks) == 0 {
		return
	}
	cur := t.Cursor.Line
	target := -1
	if dir > 0 {
		for _, h := range hunks {
			if h.Start > cur {
				target = h.Start
				break
			}
		}
		if target < 0 {
			target = hunks[0].Start // wrap to top
		}
	} else {
		for i := len(hunks) - 1; i >= 0; i-- {
			if hunks[i].Start < cur {
				target = hunks[i].Start
				break
			}
		}
		if target < 0 {
			target = hunks[len(hunks)-1].Start // wrap to bottom
		}
	}
	t.MoveCursorTo(editor.Position{Line: target, Col: 0}, false)
}
