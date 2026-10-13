// =============================================================================
// File: internal/app/cherrypickdiff.go
// Author: Rohan Allison <rohanthewiz@gmail.com>
// Created: 2026-10-13
// Copyright: 2026 Rohan Allison. All rights reserved.
// =============================================================================

// cherrypickdiff.go is the cherry-pick dialog's DIFF PREVIEW (N-051): a
// pane under the list showing the highlighted commit's message, stat and
// patch, so "is this the fix I want?" is answered without leaving the
// dialog.
//
//	┌ Cherry-pick onto main ───────────────────────────────────────── esc ┐
//	│ From  ‹ feature/parser ›                               alt+b change │
//	│ 5 commits not on main · 1 already applied · applied oldest first    │
//	│ [x] 7c8f3a7    Handle empty input              rohan   2 days ago   │
//	│ [ ] bff5bbb ⚠  Rework the lexer                rohan   2 days ago   │
//	│ touches lexer.go, parse.go · ⚠ parse.go also…          ▾ diff alt+d │
//	├─ bff5bbb ──────────────────────────────── 1–14 of 52 · alt+↑↓ ──────┤
//	│ Rework the lexer                                                    │
//	│ ---                                                                 │
//	│  lexer.go | 40 +++++-----                                           │
//	│ @@ -12,7 +12,9 @@ func lex(s string) []tok {                        │
//	│ …                                                                   │
//	│ [ ] -x  note the origin in each message   [ ] stage only, don't c…  │
//	│ [ Select all ]                          [ Cancel ]  [ Cherry-pick ] │
//	└─────────────────────────────────────────────────────────────────────┘
//
// Decisions:
//
//   - INSIDE THE DIALOG, NOT A SECOND SURFACE. The modal slot is single,
//     and the point of a preview is walking the list with the arrows and
//     watching the diff follow; a viewer you open and close per commit
//     would make that a round trip. So the dialog grows to the window
//     (rect) and splits: the list keeps a third of the room, at least
//     three rows, and the pane takes the rest.
//   - THE DIFF IS WHAT THE PICK WOULD APPLY. `git show --diff-merges=
//     first-parent`: for a merge that is its diff against the first
//     parent, exactly what `-m 1` replays (cherrypick.go). git older than
//     2.31 rejects the flag; the show is retried without it and a merge
//     then shows git's combined diff — still a preview, just not the
//     precise one.
//   - LOADED OFF THE LOOP, CACHED BY HASH. A hash names an immutable
//     commit, so a cached diff never goes stale — not across a change of
//     source branch, not ever — and walking back up the list is instant.
//     The cache is bounded (cherryPickDiffCacheMax entries, dropped whole
//     when full: simpler than an LRU, and a refill is one fork per row).
//     The answer event carries the dialog it belongs to, so a diff that
//     lands while the branch picker has the slot still fills the dialog
//     that picker returns to.
//   - THE LOAD IS ASKED FOR BY THE DRAW. Every way the highlight moves
//     (keys, clicks, a filter, a load landing) ends in a frame, so the
//     pane's draw is the one place that sees each new highlight; asking
//     there instead of at every mover means no mover can forget.
//   - SCROLLED WITH ALT/SHIFT + ARROWS AND THE WHEEL. The bare arrows keep
//     walking the list — that is what makes the preview follow — so the
//     pane takes the modified ones (Alt is safe inside a modal, and Shift
//     covers terminals where tmux eats Alt+arrow), and the wheel over the
//     pane scrolls it instead of the list. The scroll resets when the
//     highlight moves to another commit.
//   - CAPPED AND SAID SO. A diff longer than cherryPickDiffMaxLines ends
//     with a line naming the cut: a silently short diff reads as the
//     whole change.

package app

import (
	"os/exec"
	"strings"
	"time"

	"github.com/gdamore/tcell/v2"
)

const (
	// cherryPickDiffMaxW widens the dialog while the preview is open —
	// patches carry long lines — still short of a whole ultra-wide window,
	// where a centred box keeps the list near the middle of the eye line.
	cherryPickDiffMaxW = 160

	// cherryPickDiffMaxLines caps one commit's preview; the cut is
	// announced on the last line.
	cherryPickDiffMaxLines = 4000

	// cherryPickDiffCacheMax bounds the per-dialog cache of diffs.
	cherryPickDiffCacheMax = 64
)

// cherryPickDiffState is the preview pane's part of the dialog.
type cherryPickDiffState struct {
	open bool

	// cache holds each loaded diff by full hash; pending marks the hashes
	// whose load is in flight, so a redraw does not fork a second one.
	cache   map[string][]string
	pending map[string]bool

	// hash is the commit scroll belongs to: when the highlight moves to
	// another commit the scroll starts again from the top.
	hash   string
	scroll int
}

// cherryPickDiffEvent carries one loaded diff to the main loop, addressed
// to the dialog that asked for it.
type cherryPickDiffEvent struct {
	when  time.Time
	m     *cherryPickModal
	hash  string
	lines []string
}

// When satisfies tcell.Event.
func (e *cherryPickDiffEvent) When() time.Time { return e.when }

// handleCherryPickDiff lands a loaded diff in its dialog's cache. Runs on
// the main loop, which owns the dialog's state whether or not it is the
// one on screen; a dialog since closed simply holds it until collected.
func (a *App) handleCherryPickDiff(e *cherryPickDiffEvent) {
	d := &e.m.diff
	delete(d.pending, e.hash)
	if d.cache == nil {
		d.cache = map[string][]string{}
	}
	d.cache[e.hash] = e.lines
}

// toggleDiff opens or closes the pane — the alt+d chord and the detail
// row's toggle. The list's height changes with it, so the highlight is
// brought back into view.
func (m *cherryPickModal) toggleDiff(a *App) {
	m.diff.open = !m.diff.open
	m.ensureCursorVisible(a)
}

// cherryPickSplitRows divides avail rows between the list and the pane
// while the preview is open: the list gets what its n commits need, but no
// more than a third of the room (and never less than three rows while
// that fits); the pane keeps at least its header and one body row.
func cherryPickSplitRows(avail, n int) int {
	list := min(max(n, 1), max(avail/3, 3))
	return max(min(list, avail-2), 1)
}

// diffPaneRows locates the pane: its header rule, the first body row and
// how many body rows there are (zero when the window is too short).
func (m *cherryPickModal) diffPaneRows(a *App) (header, top, n int) {
	_, _, _, detail, options, _, _ := m.rowY(a)
	header, top = detail+1, detail+2
	return header, top, max(options-top, 0)
}

// diffPaneContains reports whether (x, y) is on the pane's body — where
// the wheel scrolls the diff rather than the list.
func (m *cherryPickModal) diffPaneContains(a *App, x, y int) bool {
	if !m.diff.open {
		return false
	}
	mx, _, mw, _ := m.rect(a)
	_, top, n := m.diffPaneRows(a)
	return x >= mx && x < mx+mw && y >= top && y < top+n
}

// diffToggleLabel names the toggle by its state, with its chord.
func (m *cherryPickModal) diffToggleLabel() string {
	if m.diff.open {
		return "▾ diff alt+d"
	}
	return "▸ diff alt+d"
}

// diffToggleRect is the toggle's clickable span, at the detail row's
// right end. One rect for draw and hit-test.
func (m *cherryPickModal) diffToggleRect(a *App) btnRect {
	mx, _, mw, _ := m.rect(a)
	_, _, _, dy, _, _, _ := m.rowY(a)
	w := runeLen(m.diffToggleLabel())
	return btnRect{x: mx + mw - 2 - w, y: dy, w: w}
}

// highlighted returns the highlighted commit, if any.
func (m *cherryPickModal) highlighted() (cherryPickCommit, bool) {
	if m.cursor < 0 || m.cursor >= len(m.commits) {
		return cherryPickCommit{}, false
	}
	return m.commits[m.cursor], true
}

// ensureDiff starts loading the highlighted commit's diff unless it is
// cached or already on its way. Called from the pane's draw (see the file
// comment).
func (m *cherryPickModal) ensureDiff(a *App) {
	c, ok := m.highlighted()
	if !ok || a.screen == nil {
		return
	}
	d := &m.diff
	if _, ok := d.cache[c.hash]; ok || d.pending[c.hash] {
		return
	}
	if len(d.cache) >= cherryPickDiffCacheMax {
		d.cache = nil
	}
	if d.pending == nil {
		d.pending = map[string]bool{}
	}
	d.pending[c.hash] = true
	scr, root, hash := a.screen, a.rootDir, c.hash
	go func() {
		lines := loadCherryPickDiff(root, hash)
		_ = scr.PostEvent(&cherryPickDiffEvent{when: time.Now(), m: m, hash: hash, lines: lines})
	}()
}

// loadCherryPickDiff returns one commit's preview: message, stat, patch.
// Runs off the main loop. See the file comment for the merge flag and its
// fallback; a show that fails both ways is reported in the pane.
func loadCherryPickDiff(rootDir, hash string) []string {
	args := []string{"-C", rootDir, "show", "--no-color", "--format=%B", "--stat", "--patch"}
	out, err := exec.Command("git", append(args, "--diff-merges=first-parent", hash, "--")...).Output()
	if err != nil {
		out, err = exec.Command("git", append(args, hash, "--")...).Output()
	}
	if err != nil {
		return []string{"(can't show " + hash + ": " + firstLine(err.Error()) + ")"}
	}
	lines := splitDiffLines(out)
	if len(lines) == 0 {
		return []string{"(no changes)"}
	}
	return capCherryPickDiff(lines, cherryPickDiffMaxLines)
}

// capCherryPickDiff keeps the first max lines and replaces the rest with
// a line that counts them.
func capCherryPickDiff(lines []string, max int) []string {
	if len(lines) <= max {
		return lines
	}
	cut := len(lines) - max
	return append(lines[:max:max], "… "+plural(cut, "more line", "more lines")+
		" not shown (preview capped at "+itoa(max)+")")
}

// diffLines is the highlighted commit's loaded diff, if it has arrived.
func (m *cherryPickModal) diffLines() ([]string, bool) {
	c, ok := m.highlighted()
	if !ok {
		return nil, false
	}
	lines, ok := m.diff.cache[c.hash]
	return lines, ok
}

// syncDiffScroll resets the scroll when the highlight has moved to
// another commit, then clamps it to the loaded diff.
func (m *cherryPickModal) syncDiffScroll(a *App) {
	c, _ := m.highlighted()
	if c.hash != m.diff.hash {
		m.diff.hash, m.diff.scroll = c.hash, 0
	}
	lines, _ := m.diffLines()
	_, _, n := m.diffPaneRows(a)
	m.diff.scroll = min(max(m.diff.scroll, 0), max(len(lines)-n, 0))
}

// scrollDiffBy moves the pane's scroll by delta lines, clamped.
func (m *cherryPickModal) scrollDiffBy(a *App, delta int) {
	m.syncDiffScroll(a)
	m.diff.scroll += delta
	m.syncDiffScroll(a)
}

// scrollDiffKey scrolls the pane for a modified navigation key and reports
// whether the key was one of them (Up/Down a line, PgUp/PgDn a page,
// Home/End the ends).
func (m *cherryPickModal) scrollDiffKey(a *App, k tcell.Key) bool {
	_, _, n := m.diffPaneRows(a)
	switch k {
	case tcell.KeyUp:
		m.scrollDiffBy(a, -1)
	case tcell.KeyDown:
		m.scrollDiffBy(a, 1)
	case tcell.KeyPgUp:
		m.scrollDiffBy(a, -max(n-1, 1))
	case tcell.KeyPgDn:
		m.scrollDiffBy(a, max(n-1, 1))
	case tcell.KeyHome:
		m.scrollDiffBy(a, -cherryPickDiffMaxLines-1)
	case tcell.KeyEnd:
		m.scrollDiffBy(a, cherryPickDiffMaxLines+1)
	default:
		return false
	}
	return true
}

// scrollDiffWheel scrolls the pane for a wheel notch over it.
func (m *cherryPickModal) scrollDiffWheel(a *App, btn tcell.ButtonMask) {
	if btn&tcell.WheelUp != 0 {
		m.scrollDiffBy(a, -wheelLines)
	} else {
		m.scrollDiffBy(a, wheelLines)
	}
}

// drawDiffPane paints the header rule and the body. The body sits on the
// EDITOR background, a well inside the dialog's chrome, which is also the
// background the shared diff palette (gitPanelDiffStyle) is tuned for.
func (m *cherryPickModal) drawDiffPane(a *App) {
	m.ensureDiff(a)
	m.syncDiffScroll(a)
	mx, _, mw, _ := m.rect(a)
	c := a.chrome()
	th := a.theme
	hy, top, n := m.diffPaneRows(a)
	drawHDivider(a.screen, mx, hy, mw, c.border)

	lines, loaded := m.diffLines()
	commit, ok := m.highlighted()
	if ok {
		left := " " + commit.short + " "
		if commit.merge {
			left = " " + commit.short + " vs its first parent "
		}
		drawAt(a.screen, mx+2, hy, left, c.title)
		if loaded && len(lines) > 0 {
			right := " " + itoa(m.diff.scroll+1) + "–" + itoa(min(m.diff.scroll+n, len(lines))) +
				" of " + itoa(len(lines))
			if len(lines) > n {
				right += " · alt+↑↓"
			}
			right += " "
			if rx := mx + mw - 3 - runeLen(right); rx > mx+2+runeLen(left) {
				drawAt(a.screen, rx, hy, right, c.muted)
			}
		}
	}

	well := tcell.StyleDefault.Background(th.BG)
	fillRect(a.screen, mx+1, top, mw-2, n, well)
	w := mw - 4
	switch {
	case !ok:
		drawAt(a.screen, mx+2, top, "No commit highlighted", well.Foreground(th.Muted))
		return
	case !loaded:
		drawAt(a.screen, mx+2, top, "Loading diff…", well.Foreground(th.Muted))
		return
	}
	for row := 0; row < n; row++ {
		idx := m.diff.scroll + row
		if idx >= len(lines) {
			break
		}
		line := lines[idx]
		st := gitPanelDiffStyle(line, th)
		// Tabs expanded before the clip, at the hunk's own prefix width
		// (the git log detail's rule — a combined diff from old git has
		// one prefix column per parent).
		if strings.ContainsRune(line, '\t') {
			line = diffDisplayLineCols(line, diffPrefixColsAt(lines, idx))
		} else {
			line = diffDisplayLine(line)
		}
		drawAt(a.screen, mx+2, top+row, clipRunes(line, w), st)
	}
}
