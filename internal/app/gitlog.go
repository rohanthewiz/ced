// =============================================================================
// File: internal/app/gitlog.go
// Author: Rohan Allison <rohanthewiz@gmail.com>
// Created: 2026-07-29
// Copyright: 2026 Rohan Allison. All rights reserved.
// =============================================================================

// gitlog.go implements the git log panel — a JetBrains-style history
// browser sharing the bottom strip with the git changes panel: commits
// on the left (hash, subject, author, relative age, with a ● marker on
// ref-decorated commits), the selected commit's full detail on the
// right (metadata, changed-file stat, and the patch, via `git show`).
// Esc-L toggles it; the header carries the commit verbs behind an
// "Actions ▾" button (cherry-pick, revert, reset, …) plus one-click
// ⧉ hash copy, ⟳ refresh, and ✕ collapse buttons.
//
// It deliberately mirrors gitpanel.go's shape rather than sharing code
// with it: the two panels evolve independently (the changes panel grew
// checkboxes; this one grows commit verbs), and the house patterns are
// the shared part — best-effort reads, async detail fetches posted as
// gitLogShowEvents, one geometry source for draw and hit-testing, and
// the single-occupancy bottom strip (log, changes, and a bottom-docked
// terminal swap, never stack).

package app

import (
	"bytes"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/gdamore/tcell/v2"
	"github.com/rohanthewiz/ced/internal/editor"
	"github.com/rohanthewiz/ced/internal/theme"
)

const (
	// Same vertical band as the git changes panel — the two swap in the
	// same strip, so inheriting its floor/cap keeps the swap seamless.
	gitLogMinHeight = 6
	gitLogMaxHeight = 18

	// Commit-list column bounds. Commits need more width than file paths
	// (hash + subject + author + age), so the band sits wider than the
	// changes panel's: 30 fits "● abc1234 subject…", 70 admits the
	// author · age tail on wide terminals without starving the detail pane.
	gitLogMinListW = 30
	gitLogMaxListW = 70

	// gitLogMinEditorRows / gitLogResizeStep mirror the changes panel's
	// resize contract — the strip is a viewer, the editor stays primary.
	gitLogMinEditorRows = 5
	gitLogResizeStep    = 2

	// gitLogMaxCommits caps the history read. 400 commits cover weeks of
	// scrollback in one cheap fork; the title shows "400+" when the cap
	// truncated, so a capped list never masquerades as the whole history.
	gitLogMaxCommits = 400
)

// gitLogCommit is one row of the commit list: the full hash (what every
// git verb wants), the abbreviated hash the row renders, author, the
// relative age ("3 days ago"), the ref decorations ("HEAD -> main,
// tag: v1.0" — empty for most commits), and the subject line.
type gitLogCommit struct {
	Hash    string
	Short   string
	Author  string
	When    string
	Refs    string
	Subject string
}

// gitLogState is the panel's whole state, mutated only on the main
// loop. Selection and scroll survive a collapse, same as the changes
// panel — reopening puts the user back on the commit they were reading.
type gitLogState struct {
	open bool
	// height / listWidth are the user-chosen dimensions from drags or
	// the resize leaders; 0 means "auto". Session-only, like the changes
	// panel's — ced deliberately has no layout config.
	height    int
	listWidth int
	commits   []gitLogCommit
	// truncated marks a list cut at gitLogMaxCommits, so the title can
	// say "400+" instead of passing the cap off as the full history.
	truncated bool
	// toplevel is the repo root `git show` paths are relative to —
	// captured at refresh so detail-pane jumps can absolutize them.
	toplevel   string
	selected   int
	listScroll int
	// detailHash is the commit detailLines belong to. It lags selection
	// while a `git show` is in flight and is what makes stale async
	// results detectable (see handleGitLogShow).
	detailHash   string
	detailLines  []string
	detailScroll int
}

// gitLogShowEvent carries one commit's freshly-fetched `git show` text
// from the background goroutine to the main loop.
type gitLogShowEvent struct {
	when  time.Time
	hash  string
	lines []string
}

// When satisfies the tcell.Event interface.
func (e *gitLogShowEvent) When() time.Time { return e.when }

// -----------------------------------------------------------------------------
// Toggle + refresh
// -----------------------------------------------------------------------------

// menuToggleGitLog is the ≡ menu / Esc-L entry point. Opening claims the
// bottom strip (the changes panel and a bottom-docked terminal yield —
// single occupancy, same rule they enforce against each other) and
// refreshes immediately so the list reflects this instant.
func (a *App) menuToggleGitLog() {
	a.closeMenu()
	if !a.gitLog.open && !a.gitIsRepo {
		return
	}
	a.gitLog.open = !a.gitLog.open
	if a.gitLog.open {
		a.gitPanel.open = false
		a.closeComparePanel()
		if !a.termDockLeft {
			a.term.open = false
			a.term.focused = false
		}
		a.refreshGitLogCommits()
	}
}

// gitLogToggleLabel is the dynamic menu label — reads as the action it
// will perform, the same convention as every other toggle row.
func (a *App) gitLogToggleLabel() string {
	if a.gitLog.open {
		return "Hide git log"
	}
	return "Show git log"
}

// refreshGitLogCommits reloads the commit list, preserving the selection
// by hash (the identity-preserving-refresh idea — a fetch or commit
// reorders rows; hashes are stable). No-op while collapsed so the 10s
// tick doesn't fork git log for a hidden surface.
func (a *App) refreshGitLogCommits() {
	if !a.gitLog.open {
		return
	}
	prevHash := ""
	if c, ok := a.gitLogSelectedCommit(); ok {
		prevHash = c.Hash
	}
	a.gitLog.commits, a.gitLog.truncated = loadGitLogCommits(a.rootDir)
	a.gitLog.toplevel = gitToplevel(a.rootDir)
	a.gitLog.selected = 0
	for i, c := range a.gitLog.commits {
		if c.Hash == prevHash {
			a.gitLog.selected = i
			break
		}
	}
	a.gitLogClampScrolls()
	a.gitLogEnsureSelectedVisible()
	if c, ok := a.gitLogSelectedCommit(); ok {
		// A commit's content is immutable, so a detail pane already
		// showing this hash needs no refetch — unlike the changes
		// panel's diffs, which go stale with every keystroke.
		if a.gitLog.detailHash != c.Hash {
			a.requestGitLogDetail(c)
		}
	} else {
		a.gitLog.detailHash = ""
		a.gitLog.detailLines = nil
		a.gitLog.detailScroll = 0
	}
}

// gitLogSelectedCommit returns the highlighted commit, ok=false when the
// list is empty.
func (a *App) gitLogSelectedCommit() (gitLogCommit, bool) {
	s := a.gitLog.selected
	if s < 0 || s >= len(a.gitLog.commits) {
		return gitLogCommit{}, false
	}
	return a.gitLog.commits[s], true
}

// gitLogFormat is the tab-separated --format spec the loader requests:
// full hash, short hash, author, relative date, decorations, subject.
// The subject is deliberately LAST — it's the only field that can
// itself contain a tab, and SplitN keeps a trailing field intact.
const gitLogFormat = "%H%x09%h%x09%an%x09%ad%x09%D%x09%s"

// loadGitLogCommits returns up to gitLogMaxCommits commits across all
// refs (--all, so a cherry-pick can reach commits on other branches),
// newest first, plus whether the cap truncated the history. Best-effort
// like every git read: non-repos, unborn branches, and missing git all
// yield nil and the panel renders "(no commits)".
func loadGitLogCommits(rootDir string) ([]gitLogCommit, bool) {
	if rootDir == "" {
		return nil, false
	}
	// Ask for one past the cap purely to detect truncation.
	out, err := exec.Command("git", "-C", rootDir, "log", "--all",
		"--date=relative", "-n", itoa(gitLogMaxCommits+1),
		"--format="+gitLogFormat).Output()
	if err != nil {
		return nil, false
	}
	commits := parseGitLogCommits(out)
	if len(commits) > gitLogMaxCommits {
		return commits[:gitLogMaxCommits], true
	}
	return commits, false
}

// parseGitLogCommits decodes the loader's tab-separated format, one
// commit per line. Split out so the parser is testable without a repo.
// Malformed lines are skipped — best-effort, not an error path.
func parseGitLogCommits(out []byte) []gitLogCommit {
	var commits []gitLogCommit
	for _, raw := range bytes.Split(out, []byte{'\n'}) {
		parts := strings.SplitN(string(raw), "\t", 6)
		if len(parts) < 6 || parts[0] == "" {
			continue
		}
		commits = append(commits, gitLogCommit{
			Hash:    parts[0],
			Short:   parts[1],
			Author:  parts[2],
			When:    parts[3],
			Refs:    parts[4],
			Subject: parts[5],
		})
	}
	return commits
}

// gitToplevel returns the repo's worktree root, "" on any failure —
// the base `git show` diff paths resolve against.
func gitToplevel(rootDir string) string {
	if rootDir == "" {
		return ""
	}
	out, err := exec.Command("git", "-C", rootDir, "rev-parse", "--show-toplevel").Output()
	if err != nil {
		return ""
	}
	return strings.TrimRight(string(out), "\n\r")
}

// -----------------------------------------------------------------------------
// Detail fetching — goroutine side
// -----------------------------------------------------------------------------

// requestGitLogDetail fetches c's `git show` on a goroutine and posts
// the result — the log twin of requestGitPanelDiff.
func (a *App) requestGitLogDetail(c gitLogCommit) {
	if a.screen == nil || a.rootDir == "" {
		return
	}
	scr := a.screen
	root := a.rootDir
	go func() {
		lines := loadGitLogDetail(root, c.Hash)
		_ = scr.PostEvent(&gitLogShowEvent{when: time.Now(), hash: c.Hash, lines: lines})
	}()
}

// loadGitLogDetail produces the detail pane's text: `git show` with the
// fuller pretty (author AND committer with both dates — the metadata a
// history browser is for), ref decorations, the per-file stat, and the
// patch. One command so metadata, changed files, and the diff arrive as
// a single scrollable document, the way JetBrains stacks them.
func loadGitLogDetail(rootDir, hash string) []string {
	out, err := exec.Command("git", "-C", rootDir, "show", "--decorate",
		"--pretty=fuller", "--stat", "--patch", "--no-color", hash).Output()
	if err != nil {
		return []string{"(unable to load commit " + hash + ")"}
	}
	return splitDiffLines(out)
}

// handleGitLogShow stores a completed detail fetch. Runs on the main
// loop only. Results for a commit the user has already clicked away
// from are dropped — same staleness rule as the changes panel's diffs.
func (a *App) handleGitLogShow(e *gitLogShowEvent) {
	if !a.gitLog.open {
		return
	}
	c, ok := a.gitLogSelectedCommit()
	if !ok || c.Hash != e.hash {
		return
	}
	if a.gitLog.detailHash != e.hash {
		a.gitLog.detailScroll = 0
	}
	a.gitLog.detailHash = e.hash
	a.gitLog.detailLines = e.lines
	a.gitLogClampScrolls()
}

// -----------------------------------------------------------------------------
// Geometry — one source for draw AND mouse routing
// -----------------------------------------------------------------------------

// gitLogHeight returns the panel's row count for the current window —
// user choice wins, auto mode takes a third of the screen, both
// re-clamped against the live window. Mirrors gitPanelHeight.
func (a *App) gitLogHeight() int {
	h := a.gitLog.height
	if h == 0 {
		h = a.height / 3
		if h > gitLogMaxHeight {
			h = gitLogMaxHeight
		}
	}
	if h < gitLogMinHeight {
		h = gitLogMinHeight
	}
	if max := a.maxGitLogHeight(); h > max {
		h = max
	}
	return h
}

// maxGitLogHeight is the tallest the panel may grow while leaving the
// editor its minimum working rows — the hard limit even explicit drags
// respect.
func (a *App) maxGitLogHeight() int {
	max := a.height - 2 - gitLogMinEditorRows
	max -= a.findBarRows()
	if max < gitLogMinHeight {
		max = gitLogMinHeight
	}
	return max
}

// resizeGitLog records a user-chosen height, clamped to the legal band,
// and re-clamps the scroll offsets against the new viewport.
func (a *App) resizeGitLog(target int) {
	if target < gitLogMinHeight {
		target = gitLogMinHeight
	}
	if max := a.maxGitLogHeight(); target > max {
		target = max
	}
	a.gitLog.height = target
	a.gitLogClampScrolls()
}

// dragGitLogTo resizes the panel so its header rule tracks the mouse
// row during a drag.
func (a *App) dragGitLogTo(y int) {
	bottom := a.height - 1
	bottom -= a.findBarRows()
	a.resizeGitLog(bottom - y)
}

// growGitLog / shrinkGitLog are the Esc-= / Esc-- targets while the log
// owns the bottom strip. Silent no-ops while collapsed, per the leader
// contract; single occupancy guarantees at most one panel acts.
func (a *App) growGitLog() {
	if !a.gitLog.open {
		return
	}
	a.resizeGitLog(a.gitLogHeight() + gitLogResizeStep)
}

// shrinkGitLog steps the panel shorter; see growGitLog.
func (a *App) shrinkGitLog() {
	if !a.gitLog.open {
		return
	}
	a.resizeGitLog(a.gitLogHeight() - gitLogResizeStep)
}

// gitLogRect returns the panel's on-screen rectangle — the same slot the
// changes panel occupies (they swap, never stack).
func (a *App) gitLogRect() (x, y, w, h int) {
	lw := a.leftBlockW()
	h = a.gitLogHeight()
	y = a.height - 1 - h
	y -= a.findBarRows()
	return lw, y, a.width - lw - a.rightBlockW(), h
}

// gitLogListWidth clamps a desired commit-list column width for a panel
// of total width w: at least gitLogMinListW, at most gitLogMaxListW, and
// never past 3/5 of the panel — commits carry more text than file paths,
// so the list gets a bigger ceiling than the changes panel's half, but
// the detail pane keeps a readable share. Desired <= 0 means "auto" —
// two fifths of the panel.
func gitLogListWidth(w, desired int) int {
	lw := desired
	if lw <= 0 {
		lw = w * 2 / 5
	}
	if lw < gitLogMinListW {
		lw = gitLogMinListW
	}
	if lw > gitLogMaxListW {
		lw = gitLogMaxListW
	}
	if max := w * 3 / 5; lw > max {
		lw = max
	}
	return lw
}

// gitLogListW returns the effective commit-list column width for a
// panel of total width pw — the single source draw, hit-test, and
// resize all consult.
func (a *App) gitLogListW(pw int) int {
	return gitLogListWidth(pw, a.gitLog.listWidth)
}

// gitLogDividerX returns the screen column of the list/detail divider —
// the draggable width handle — or -1 when the panel is closed.
func (a *App) gitLogDividerX() int {
	if !a.gitLog.open {
		return -1
	}
	px, _, pw, _ := a.gitLogRect()
	return px + a.gitLogListW(pw)
}

// dragGitLogDivTo resizes the commit-list column so the divider tracks
// the mouse x during a drag.
func (a *App) dragGitLogDivTo(x int) {
	px, _, pw, _ := a.gitLogRect()
	a.gitLog.listWidth = gitLogListWidth(pw, x-px)
}

// Header button labels. All glyphs are single-width on purpose (the
// runeLen house rule — a double-width emoji would skew every rect to
// its right): ▾ marks "opens a list", ⧉ copies, ⟳ refreshes, ✕ closes.
const (
	gitLogActionsLabel = " Actions ▾ "
	gitLogCopyLabel    = " ⧉ hash "
)

// gitLogCloseRect returns the ✕ collapse button's rectangle in the
// header row (btnRect house rule: one source for draw and hit-test).
func (a *App) gitLogCloseRect() btnRect {
	px, py, pw, _ := a.gitLogRect()
	return btnRect{x: px + pw - 4, y: py, w: 3}
}

// gitLogRefreshRect returns the ⟳ button — a manual reload for the
// moment right after an outside-the-editor push/fetch, ahead of the
// 10s tick.
func (a *App) gitLogRefreshRect() btnRect {
	c := a.gitLogCloseRect()
	return btnRect{x: c.x - 4, y: c.y, w: 3}
}

// gitLogCopyRect returns the ⧉ hash button — one-click copy of the
// selected commit's full hash, the single thing a history browser is
// asked for most.
func (a *App) gitLogCopyRect() btnRect {
	r := a.gitLogRefreshRect()
	return btnRect{x: r.x - runeLen(gitLogCopyLabel) - 1, y: r.y, w: runeLen(gitLogCopyLabel)}
}

// gitLogActionsRect returns the Actions ▾ button, pinned at the header's
// left like the changes panel's — the header doubles as the height-drag
// handle, so gitLogPress carves these rects out before starting a drag.
func (a *App) gitLogActionsRect() btnRect {
	px, py, _, _ := a.gitLogRect()
	return btnRect{x: px + 1, y: py, w: runeLen(gitLogActionsLabel)}
}

// gitLogContains reports whether (x, y) falls inside the open panel.
func (a *App) gitLogContains(x, y int) bool {
	px, py, pw, ph := a.gitLogRect()
	return x >= px && x < px+pw && y >= py && y < py+ph
}

// gitLogClampScrolls pins both scroll offsets into range after any
// mutation. Hard clamp, no overscroll — it's a viewer, same rationale
// as the changes panel.
func (a *App) gitLogClampScrolls() {
	_, _, _, ph := a.gitLogRect()
	visible := ph - 1 // header row
	clamp := func(scroll, total int) int {
		max := total - visible
		if max < 0 {
			max = 0
		}
		if scroll > max {
			scroll = max
		}
		if scroll < 0 {
			scroll = 0
		}
		return scroll
	}
	a.gitLog.listScroll = clamp(a.gitLog.listScroll, len(a.gitLog.commits))
	a.gitLog.detailScroll = clamp(a.gitLog.detailScroll, len(a.gitLog.detailLines))
}

// gitLogEnsureSelectedVisible snaps the list scroll so the selected row
// is on-screen. Called only when the selection itself moves — never
// from generic clamping (the cursorMoved lesson).
func (a *App) gitLogEnsureSelectedVisible() {
	_, _, _, ph := a.gitLogRect()
	visible := ph - 1
	if a.gitLog.selected < a.gitLog.listScroll {
		a.gitLog.listScroll = a.gitLog.selected
	}
	if visible > 0 && a.gitLog.selected >= a.gitLog.listScroll+visible {
		a.gitLog.listScroll = a.gitLog.selected - visible + 1
	}
}

// -----------------------------------------------------------------------------
// Mouse
// -----------------------------------------------------------------------------

// gitLogPress routes an initial left press inside the panel and reports
// the drag it started ("" for none): header rule outside the buttons is
// the height handle ("gitlog"), the list/detail divider is the width
// handle ("gitlogdiv"), the four header buttons do their thing, and
// everything else is a plain click.
func (a *App) gitLogPress(x, y int) (dragMode string) {
	_, py, _, _ := a.gitLogRect()
	if y == py {
		switch {
		case a.gitLogActionsRect().contains(x, y):
			a.openGitLogActions()
		case a.gitLogCopyRect().contains(x, y):
			if c, ok := a.gitLogSelectedCommit(); ok {
				a.gitLogCopyHash(c)
			}
		case a.gitLogRefreshRect().contains(x, y):
			a.refreshGitLogCommits()
			a.flash("Git log refreshed")
		case a.gitLogCloseRect().contains(x, y):
			a.gitLog.open = false
		default:
			return "gitlog"
		}
		return ""
	}
	if x == a.gitLogDividerX() {
		return "gitlogdiv"
	}
	a.gitLogClick(x, y)
	return ""
}

// gitLogClick routes a body click: a commit row selects it and fetches
// its detail; a double-click on a detail row jumps the editor to the
// file/line that diff row touched — "where did this change land" is the
// history browser's version of the changes panel's jump.
func (a *App) gitLogClick(x, y int) {
	px, py, pw, _ := a.gitLogRect()
	if x >= px+a.gitLogListW(pw) {
		now := time.Now()
		if a.lastClick.x == x && a.lastClick.y == y && now.Sub(a.lastClick.when) < doubleClickMs {
			a.lastClick = clickRecord{}
			a.gitLogJumpToDetailRow(a.gitLog.detailScroll + (y - py - 1))
			return
		}
		a.lastClick = clickRecord{x: x, y: y, when: now}
		return
	}
	idx := a.gitLog.listScroll + (y - py - 1)
	if idx < 0 || idx >= len(a.gitLog.commits) {
		return
	}
	if idx == a.gitLog.selected {
		return // re-click on the same commit: nothing to refetch
	}
	a.gitLog.selected = idx
	a.gitLog.detailScroll = 0
	a.requestGitLogDetail(a.gitLog.commits[idx])
}

// gitLogJumpToDetailRow opens the file behind detail row idx at that
// row's new-side line. Best-effort: rows with no mapping (metadata,
// stat, deleted files) do nothing; a line past today's EOF clamps —
// history moved on, landing near is still better than not landing.
func (a *App) gitLogJumpToDetailRow(idx int) {
	rel, line, ok := gitLogDetailTarget(a.gitLog.detailLines, idx)
	if !ok || a.gitLog.toplevel == "" {
		return
	}
	abs := filepath.Join(a.gitLog.toplevel, rel)
	if !pathInside(abs, a.rootDir) {
		return
	}
	a.openFile(abs)
	t := a.activeTabPtr()
	if t == nil || t.Path != abs {
		return // open failed (file gone since that commit) — openFile flashed why
	}
	if lc := t.Buffer.LineCount(); line >= lc {
		line = lc - 1
	}
	t.MoveCursorTo(editor.Position{Line: line, Col: 0}, false)
}

// gitLogDetailTarget maps a detail-pane row to (repo-relative path,
// 0-based new-side line). It walks the `git show` document tracking two
// cursors: the current file (from "+++ b/…" headers; reset on the next
// "diff --git" so stat/metadata rows never inherit a file) and the
// new-side line counter (from hunk headers, advanced by every row that
// exists on the new side — the diffTargetLine logic generalized to a
// multi-file patch).
func gitLogDetailTarget(lines []string, idx int) (rel string, line int, ok bool) {
	if idx < 0 || idx >= len(lines) {
		return "", 0, false
	}
	cur := ""
	newLine := 0 // 1-based; 0 = not inside a hunk
	for i := 0; i <= idx; i++ {
		ln := lines[i]
		switch {
		case strings.HasPrefix(ln, "diff --git"):
			cur = ""
			newLine = 0
		case strings.HasPrefix(ln, "+++ b/"):
			cur = ln[len("+++ b/"):]
			newLine = 0
		case strings.HasPrefix(ln, "+++ "):
			cur = "" // "+++ /dev/null": the file was deleted, nothing to open
			newLine = 0
		case strings.HasPrefix(ln, "@@"):
			if _, _, c, _, hok := parseHunkHeader(ln); hok {
				newLine = c
				if newLine < 1 {
					newLine = 1 // "+0,0": deletion-only hunk
				}
			}
		}
		if i == idx {
			if cur == "" || newLine == 0 {
				return "", 0, false
			}
			return cur, newLine - 1, true
		}
		// Inside a hunk, every row except old-side deletions and the
		// header itself occupies a new-side line.
		if cur != "" && newLine > 0 &&
			!strings.HasPrefix(ln, "@@") && !strings.HasPrefix(ln, "-") {
			newLine++
		}
	}
	return "", 0, false
}

// gitLogScroll wheels whichever half of the panel the cursor is over:
// commit list on the left, detail text on the right.
func (a *App) gitLogScroll(x, _, delta int) {
	px, _, pw, _ := a.gitLogRect()
	if x < px+a.gitLogListW(pw) {
		a.gitLog.listScroll += delta
	} else {
		a.gitLog.detailScroll += delta
	}
	a.gitLogClampScrolls()
}

// -----------------------------------------------------------------------------
// Drawing
// -----------------------------------------------------------------------------

// drawGitLog paints the whole panel: header rule with the Actions,
// ⧉ hash, ⟳, and ✕ buttons plus the title; the commit list; a vertical
// divider; the commit detail with unified-diff coloring.
func (a *App) drawGitLog() {
	px, py, pw, ph := a.gitLogRect()
	listW := a.gitLogListW(pw)
	th := a.theme

	headerSt := tcell.StyleDefault.Background(th.SidebarBG).Foreground(th.Subtle)
	titleSt := tcell.StyleDefault.Background(th.SidebarBG).Foreground(th.Accent).Bold(true)
	btnSt := tcell.StyleDefault.Background(th.Selection).Foreground(th.Accent).Bold(true)
	listBG := tcell.StyleDefault.Background(th.SidebarBG)
	detailBG := tcell.StyleDefault.Background(th.BG)

	// Grab handles brighten while seized — the sidebar splitter's
	// grab-handle language.
	ruleSt := headerSt
	if a.dragMode == "gitlog" {
		ruleSt = tcell.StyleDefault.Background(th.SidebarBG).Foreground(th.Accent)
	}
	divSt := headerSt
	if a.dragMode == "gitlogdiv" {
		divSt = tcell.StyleDefault.Background(th.SidebarBG).Foreground(th.Accent)
	}

	// Header rule + buttons. Buttons are mandatory; the title drops out
	// on narrow panels rather than overlapping them — a control you
	// can't click is worse than a label you can't read.
	for cx := px; cx < px+pw; cx++ {
		a.screen.SetContent(cx, py, '─', nil, ruleSt)
	}
	closeBtn := a.gitLogCloseRect()
	drawAt(a.screen, closeBtn.x, closeBtn.y, " ✕ ", titleSt)
	refreshBtn := a.gitLogRefreshRect()
	drawAt(a.screen, refreshBtn.x, refreshBtn.y, " ⟳ ", titleSt)
	copyBtn := a.gitLogCopyRect()
	drawAt(a.screen, copyBtn.x, copyBtn.y, gitLogCopyLabel, btnSt)
	actions := a.gitLogActionsRect()
	drawAt(a.screen, actions.x, actions.y, gitLogActionsLabel, btnSt)

	title := " Git log · " + itoa(len(a.gitLog.commits))
	if a.gitLog.truncated {
		title += "+"
	}
	title += " commits "
	if tx := actions.x + actions.w; tx+runeLen(title) <= copyBtn.x {
		drawAt(a.screen, tx, py, title, titleSt)
	}

	// Body rows.
	for row := 0; row < ph-1; row++ {
		ry := py + 1 + row
		for cx := px; cx < px+listW; cx++ {
			a.screen.SetContent(cx, ry, ' ', nil, listBG)
		}
		a.drawGitLogListRow(row, px, ry, listW)
		a.screen.SetContent(px+listW, ry, '│', nil, divSt)
		for cx := px + listW + 1; cx < px+pw; cx++ {
			a.screen.SetContent(cx, ry, ' ', nil, detailBG)
		}
		a.drawGitLogDetailRow(row, px+listW+2, ry, pw-listW-3)
	}
}

// drawGitLogListRow paints one commit row: a ● marker on ref-decorated
// commits (branch heads and tags stand out the way JetBrains' labels
// do), the short hash in accent, the subject, and — when the column is
// wide enough — a muted "author · age" tail right-aligned.
func (a *App) drawGitLogListRow(row, px, ry, listW int) {
	th := a.theme
	idx := a.gitLog.listScroll + row
	if len(a.gitLog.commits) == 0 {
		if row == 0 {
			drawAt(a.screen, px+1, ry, "(no commits)",
				tcell.StyleDefault.Background(th.SidebarBG).Foreground(th.Muted))
		}
		return
	}
	if idx < 0 || idx >= len(a.gitLog.commits) {
		return
	}
	c := a.gitLog.commits[idx]
	bg := th.SidebarBG
	if idx == a.gitLog.selected {
		bg = th.Selection
		for cx := px; cx < px+listW; cx++ {
			a.screen.SetContent(cx, ry, ' ', nil,
				tcell.StyleDefault.Background(bg))
		}
	}
	textSt := tcell.StyleDefault.Background(bg).Foreground(th.Text)
	mutedSt := tcell.StyleDefault.Background(bg).Foreground(th.Muted)
	hashSt := tcell.StyleDefault.Background(bg).Foreground(th.AccentSoft)

	// " ● abc1234 " — the marker cell stays reserved (space when
	// undecorated) so hashes align into a scannable column.
	mark := " "
	if c.Refs != "" {
		mark = "●"
	}
	drawAt(a.screen, px, ry, " "+mark+" ", tcell.StyleDefault.Background(bg).Foreground(th.GitAdded))
	drawAt(a.screen, px+3, ry, c.Short, hashSt)
	subjX := px + 3 + runeLen(c.Short) + 1

	// The author · age tail claims the right edge only when the subject
	// keeps a readable share (16 cols) — otherwise the subject wins.
	tail := c.Author + " · " + c.When + " "
	tailW := runeLen(tail)
	subjEnd := px + listW - 1
	if listW >= 46 && subjX+16+tailW < px+listW {
		drawAt(a.screen, px+listW-tailW, ry, tail, mutedSt)
		subjEnd = px + listW - tailW - 1
	}
	subj := c.Subject
	if avail := subjEnd - subjX; runeLen(subj) > avail {
		if avail < 1 {
			return
		}
		subj = string([]rune(subj)[:avail-1]) + "…"
	}
	drawAt(a.screen, subjX, ry, subj, textSt)
}

// drawGitLogDetailRow paints one detail line, truncated to the pane
// width — same no-horizontal-scroll tradeoff as the changes panel.
func (a *App) drawGitLogDetailRow(row, x, ry, w int) {
	if w <= 0 {
		return
	}
	idx := a.gitLog.detailScroll + row
	if idx < 0 || idx >= len(a.gitLog.detailLines) {
		return
	}
	line := a.gitLog.detailLines[idx]
	st := gitLogDetailStyle(line, a.theme)
	if runeLen(line) > w {
		line = string([]rune(line)[:w-1]) + "…"
	}
	drawAt(a.screen, x, ry, line, st)
}

// gitLogDetailStyle colors one `git show` line: the commit header line
// accented (it carries the hash and decorations), the metadata keys
// subtle, and everything else through the shared unified-diff palette —
// message and stat lines land on its default text case.
func gitLogDetailStyle(line string, th theme.Theme) tcell.Style {
	base := tcell.StyleDefault.Background(th.BG)
	switch {
	case strings.HasPrefix(line, "commit "):
		return base.Foreground(th.Accent).Bold(true)
	case strings.HasPrefix(line, "Author:"), strings.HasPrefix(line, "AuthorDate:"),
		strings.HasPrefix(line, "Commit:"), strings.HasPrefix(line, "CommitDate:"),
		strings.HasPrefix(line, "Merge:"), strings.HasPrefix(line, "Date:"):
		return base.Foreground(th.Subtle)
	default:
		return gitPanelDiffStyle(line, th)
	}
}
