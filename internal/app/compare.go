// =============================================================================
// File: internal/app/compare.go
// Author: Rohan Allison <rohanthewiz@gmail.com>
// Created: 2026-08-03
// Copyright: 2026 Rohan Allison. All rights reserved.
// =============================================================================

// compare.go is the diff viewer: a unified diff of the file you're
// editing against something else — another file, its own saved copy, or
// a block of text you just pasted. It is the fourth occupant of the
// bottom strip, under the same single-occupancy rule as the git panels
// and a bottom-docked terminal.
//
// House rules:
//
//   - THE ACTIVE BUFFER IS THE NEW SIDE. Everything else is "old". That
//     ordering is not cosmetic: it makes the "+" lines the ones that
//     exist in the open file, so `diffTargetLine` — written for the git
//     panel — maps a display row straight to a line in the tab, and a
//     double-click jumps there for free. It also reads the way the
//     question is asked ("what have I got that the saved copy hasn't?").
//   - THE LEFT SIDE COMES FROM THE BUFFER, not the disk copy, whenever
//     the file is open. Comparing the stale on-disk text of the file you
//     just edited is the one answer that would be quietly wrong — the
//     same rule the chat attachments follow.
//   - PASTE IS A REAL SOURCE, because ced cannot READ the system
//     clipboard (clipboard.go is OSC 52 write-only, and that is correct
//     for an SSH-first editor). "Compare with pasted text" arms the panel
//     and the next bracketed paste — or Cmd+V from the internal
//     clipboard — becomes the old side. That covers text from anywhere,
//     which is strictly more useful than a clipboard read would have
//     been.
//   - DIFFING IS PURE GO (internal/diff), not `git diff --no-index`. The
//     sources here are buffers, so shelling out would mean temp files,
//     and neither git nor the repository it wants is guaranteed to be
//     there. Same argument the project search made against ripgrep.
//
// State lives on App.compare and is mutated only on the main loop.

package app

import (
	"os"
	"path/filepath"
	"time"

	"github.com/gdamore/tcell/v2"

	"github.com/rohanthewiz/ced/internal/diff"
)

const (
	// Same vertical band as the other bottom panels — they swap in one
	// strip, so sharing the floor and cap keeps the swap seamless.
	comparePanelMinHeight = 6
	comparePanelMaxHeight = 20

	// The editor stays primary, and resize steps match the neighbours.
	comparePanelMinEditorRows = 5
	comparePanelResizeStep    = 2

	// compareContext is how many unchanged lines frame each hunk. Three
	// is git's default and the number every reader's eye is trained on.
	compareContext = 3

	// compareMaxBytes caps a side. The differ is O(anchors) on ordinary
	// text but a comparison is an interactive gesture, and a user who
	// picks a 40MB log from the picker deserves a sentence rather than a
	// frozen editor. Matches the "guard before the read" rule fileio
	// applies at open time.
	compareMaxBytes = 8 << 20
)

// compareState is the panel's whole state.
type compareState struct {
	open bool
	// height is the user-chosen row count from a drag or the resize
	// leaders; 0 means auto. Session-only, like every other panel size.
	height int

	// oldLabel / newLabel name the two sides in the header and in the
	// diff's --- / +++ lines.
	oldLabel string
	newLabel string
	// newPath is the absolute path of the tab the "+" side came from, so
	// a double-click on a row can open it and jump. Empty for an
	// untitled buffer, which simply makes the jump a no-op.
	newPath string
	// oldPath is where the old side came FROM, so ⟳ can re-read it —
	// empty when the old side was pasted, which is the case that has
	// nothing to re-read and re-diffs the held lines instead. Keeping
	// the source here rather than reconstructing it from oldLabel is
	// what stops a refresh from guessing: the label is prose (it carries
	// "(saved)"), and a path outside the project root doesn't survive
	// the round trip through a relative rendering.
	oldPath  string
	oldLines []string

	lines  []string
	scroll int
	added  int
	remove int

	// identical records that the comparison RAN and found no
	// differences. Distinct from an empty lines slice, which is also
	// what "nothing has been compared yet" looks like — and telling the
	// user "no differences" is the entire result in that case.
	identical bool

	// awaitPaste is set between "Compare with pasted text" and the paste
	// that answers it. While it's set the panel shows the instruction and
	// comparePasteTarget claims the next bracketed paste.
	awaitPaste bool
}

// -----------------------------------------------------------------------------
// Entry points
// -----------------------------------------------------------------------------

// menuCompareFile asks which file to compare the active buffer against.
// A palette picker over the finder's index, per the house rule that
// every choose-one-from-a-list UI reuses the palette.
func (a *App) menuCompareFile() {
	a.closeMenu()
	if !a.hasComparable() {
		a.flash("Nothing to compare — open a file first")
		return
	}
	if a.finder == nil {
		a.flash("File index unavailable")
		return
	}
	paths := a.finder.Paths()
	if len(paths) == 0 {
		a.flash("File index is still building — try again in a moment")
		return
	}
	items := make([]paletteItem, 0, len(paths))
	for _, rel := range paths {
		rel := rel // capture per-iteration for the closure
		items = append(items, paletteItem{
			label: rel,
			run: func(app *App) {
				app.compareWithFile(filepath.Join(app.rootDir, rel))
			},
		})
	}
	a.openPicker("Compare current file with", items)
}

// menuCompareSaved diffs the buffer against its own file on disk — "what
// have I changed since the last save?". It is the same code path as
// comparing with any other file, with the path already known, which is
// why it costs a row rather than a feature.
func (a *App) menuCompareSaved() {
	a.closeMenu()
	tab := a.activeTabPtr()
	if tab == nil || tab.Path == "" {
		a.flash("This buffer has never been saved")
		return
	}
	a.compareWithFile(tab.Path)
}

// menuComparePaste arms the panel for a paste. It opens the panel first,
// showing the instruction: a mode you can't see is a mode nobody knows
// they're in, and the next paste going somewhere surprising is exactly
// the failure to avoid.
func (a *App) menuComparePaste() {
	a.closeMenu()
	if !a.hasComparable() {
		a.flash("Nothing to compare — open a file first")
		return
	}
	a.openComparePanel()
	a.compare.awaitPaste = true
	a.compare.lines = nil
	a.compare.identical = false
	a.compare.oldLabel = "pasted text"
	a.compare.newLabel = a.compareNewLabel()
	a.flash("Paste now to compare (⌘V or a terminal paste) · Esc cancels")
}

// menuToggleCompare is the show/hide row. Hiding keeps the comparison,
// so re-showing puts the user back on the diff they were reading — the
// same contract the git panels' collapse has.
func (a *App) menuToggleCompare() {
	a.closeMenu()
	if a.compare.open {
		a.closeComparePanel()
		return
	}
	a.openComparePanel()
}

// compareToggleLabel reads as the action the row performs.
func (a *App) compareToggleLabel() string {
	if a.compare.open {
		return "Hide compare panel"
	}
	return "Show compare panel"
}

// hasComparable gates the compare rows: there has to be a text buffer to
// be the new side.
func (a *App) hasComparable() bool {
	t := a.activeTabPtr()
	return t != nil && t.Buffer != nil && !t.IsImage()
}

// hasCompareResult reports whether a comparison has been computed, so
// the Hide row can dim when there's nothing behind it.
func (a *App) hasCompareResult() bool {
	return a.compare.open || len(a.compare.lines) > 0 || a.compare.identical
}

// -----------------------------------------------------------------------------
// Running a comparison
// -----------------------------------------------------------------------------

// compareWithFile diffs the active buffer against path. When path is a
// file that's already open, its BUFFER is used rather than the disk copy
// — you compare what you're looking at, including unsaved edits.
func (a *App) compareWithFile(path string) {
	tab := a.activeTabPtr()
	if tab == nil || tab.Buffer == nil {
		return
	}
	abs := absolutePathFor(path)
	text, err := a.compareSideText(abs)
	if err != nil {
		a.flash("Compare: " + err.Error())
		return
	}
	label := a.relativePathFor(abs)
	if abs == tab.Path {
		// Comparing a file with itself only makes sense against the SAVED
		// copy, so this side deliberately reads from disk — and says so,
		// since "t.txt ↔ t.txt" would look like a bug.
		disk, derr := readCompareFile(abs)
		if derr != nil {
			a.flash("Compare: " + derr.Error())
			return
		}
		text = disk
		label += " (saved)"
	}
	a.runCompare(label, abs, diff.SplitLines(text))
}

// compareWithText diffs the active buffer against a literal block of
// text — the paste path. No source path: there is nothing to re-read.
func (a *App) compareWithText(label, text string) {
	a.runCompare(label, "", diff.SplitLines(text))
}

// runCompare is the single place a comparison is computed and installed,
// so every source produces the same shape of result and the same header.
func (a *App) runCompare(oldLabel, oldPath string, oldLines []string) {
	tab := a.activeTabPtr()
	if tab == nil || tab.Buffer == nil {
		return
	}
	newLines := diff.SplitLines(tab.Buffer.String())
	edits := diff.Diff(oldLines, newLines)
	added, removed := diff.Stats(edits)

	a.compare.awaitPaste = false
	a.compare.oldLabel = oldLabel
	a.compare.oldPath = oldPath
	a.compare.oldLines = oldLines
	a.compare.newLabel = a.compareNewLabel()
	a.compare.newPath = tab.Path
	a.compare.lines = diff.Unified(edits, oldLabel, a.compare.newLabel, compareContext)
	a.compare.identical = len(a.compare.lines) == 0
	a.compare.added, a.compare.remove = added, removed
	a.compare.scroll = 0
	a.openComparePanel()
	if a.compare.identical {
		a.flash("No differences")
	}
}

// compareNewLabel names the buffer side: its project-relative path, or
// the tab's display name for an untitled buffer, marked when it carries
// unsaved edits — which is the difference the diff is often about.
func (a *App) compareNewLabel() string {
	tab := a.activeTabPtr()
	if tab == nil {
		return "(nothing)"
	}
	name := tab.DisplayName()
	if tab.Path != "" {
		name = a.relativePathFor(tab.Path)
	}
	if tab.Dirty {
		name += " (unsaved)"
	}
	return name
}

// compareSideText returns the text to diff against for a path: the open
// tab's buffer when there is one, the file on disk otherwise.
func (a *App) compareSideText(abs string) (string, error) {
	for i := range a.tabs {
		if a.tabs[i].Path == abs && a.tabs[i].Buffer != nil && !a.tabs[i].IsImage() {
			return a.tabs[i].Buffer.String(), nil
		}
	}
	return readCompareFile(abs)
}

// readCompareFile reads a file for the old side, refusing what the
// editor itself would refuse to open. The guard runs on the STAT, before
// the read — a limit checked afterwards has already paid for the damage,
// which is the rule fileio.go established for opening files.
func readCompareFile(abs string) (string, error) {
	st, err := os.Stat(abs)
	if err != nil {
		return "", err
	}
	if st.IsDir() {
		return "", errCompare(filepath.Base(abs) + " is a directory")
	}
	if st.Size() > compareMaxBytes {
		return "", errCompare(filepath.Base(abs) + " is too large to compare")
	}
	b, err := os.ReadFile(abs)
	if err != nil {
		return "", err
	}
	// Same one-NUL-in-the-first-8KB sniff the open path uses: a binary
	// file has no lines to compare, and rendering its bytes into the
	// panel would be noise at best.
	head := b
	if len(head) > 8192 {
		head = head[:8192]
	}
	for _, c := range head {
		if c == 0 {
			return "", errCompare(filepath.Base(abs) + " looks like a binary file")
		}
	}
	return string(b), nil
}

// errCompare is a tiny error type so the messages above read as
// sentences at the call site without importing a formatting helper.
type errCompare string

// Error satisfies the error interface.
func (e errCompare) Error() string { return string(e) }

// -----------------------------------------------------------------------------
// Paste plumbing
// -----------------------------------------------------------------------------

// comparePasteTarget reports whether an armed compare panel should claim
// the next bracketed paste. It outranks the editor, the chat composer
// and the terminal because arming it was the user's most recent
// deliberate act — and it can only be armed by that act, so it never
// steals a paste nobody redirected.
func (a *App) comparePasteTarget() bool {
	if a.modal != nil || a.menuOpen {
		return false
	}
	return a.compare.open && a.compare.awaitPaste
}

// compareInsertPaste turns a pasted block into the old side. Unlike the
// chat composer and the terminal, nothing is flattened or split: the
// point of the gesture is to compare the text EXACTLY as it arrived.
func (a *App) compareInsertPaste(text string) {
	a.compareWithText("pasted text", text)
}

// comparePasteClip is the Cmd+V twin, using the internal clipboard —
// the only clipboard ced can read, since OSC 52 is write-only.
func (a *App) comparePasteClip() {
	if a.clipBuf == "" {
		a.flash("Clipboard is empty")
		return
	}
	a.compareWithText("clipboard", a.clipBuf)
}

// -----------------------------------------------------------------------------
// Open / close
// -----------------------------------------------------------------------------

// openComparePanel claims the bottom strip. Single occupancy: the git
// panels and a bottom-docked terminal yield, exactly as they do to each
// other.
func (a *App) openComparePanel() {
	a.compare.open = true
	a.gitPanel.open = false
	a.gitLog.open = false
	if !a.termDockLeft {
		a.term.open = false
		a.term.focused = false
	}
}

// closeComparePanel collapses the panel and disarms any pending paste —
// a mode the user can no longer see must not still be claiming pastes.
func (a *App) closeComparePanel() {
	a.compare.open = false
	a.compare.awaitPaste = false
}

// -----------------------------------------------------------------------------
// Geometry — one source for draw AND mouse routing
// -----------------------------------------------------------------------------

// comparePanelHeight returns the panel's row count for the current
// window; mirrors gitPanelHeight.
func (a *App) comparePanelHeight() int {
	h := a.compare.height
	if h == 0 {
		h = a.height / 3
		if h > comparePanelMaxHeight {
			h = comparePanelMaxHeight
		}
	}
	if h < comparePanelMinHeight {
		h = comparePanelMinHeight
	}
	if max := a.maxComparePanelHeight(); h > max {
		h = max
	}
	return h
}

// maxComparePanelHeight is the tallest the panel may grow while leaving
// the editor its minimum working rows.
func (a *App) maxComparePanelHeight() int {
	max := a.height - 2 - comparePanelMinEditorRows
	max -= a.findBarRows()
	if max < comparePanelMinHeight {
		max = comparePanelMinHeight
	}
	return max
}

// resizeComparePanel records a user-chosen height, clamped to the legal
// band, and re-clamps the scroll offset against the new viewport.
func (a *App) resizeComparePanel(target int) {
	if target < comparePanelMinHeight {
		target = comparePanelMinHeight
	}
	if max := a.maxComparePanelHeight(); target > max {
		target = max
	}
	a.compare.height = target
	a.compareClampScroll()
}

// dragComparePanelTo resizes the panel so its header rule tracks the
// mouse row during a drag.
func (a *App) dragComparePanelTo(y int) {
	bottom := a.height - 1
	bottom -= a.findBarRows()
	a.resizeComparePanel(bottom - y)
}

// growComparePanel / shrinkComparePanel are the Esc-= / Esc-- targets
// while the compare panel owns the strip. Silent no-ops while collapsed,
// per the leader contract; single occupancy guarantees at most one of
// the bottom panels acts.
func (a *App) growComparePanel() {
	if !a.compare.open {
		return
	}
	a.resizeComparePanel(a.comparePanelHeight() + comparePanelResizeStep)
}

// shrinkComparePanel steps the panel shorter; see growComparePanel.
func (a *App) shrinkComparePanel() {
	if !a.compare.open {
		return
	}
	a.resizeComparePanel(a.comparePanelHeight() - comparePanelResizeStep)
}

// comparePanelRect returns the panel's on-screen rectangle — the same
// slot the git panels occupy.
func (a *App) comparePanelRect() (x, y, w, h int) {
	lw := a.leftBlockW()
	h = a.comparePanelHeight()
	y = a.height - 1 - h
	y -= a.findBarRows()
	return lw, y, a.width - lw - a.rightBlockW(), h
}

// comparePanelContains reports whether (x, y) falls inside the panel.
func (a *App) comparePanelContains(x, y int) bool {
	if !a.compare.open {
		return false
	}
	px, py, pw, ph := a.comparePanelRect()
	return x >= px && x < px+pw && y >= py && y < py+ph
}

// compareCloseRect is the ✕ collapse button (btnRect house rule: one
// source for draw and hit-test).
func (a *App) compareCloseRect() btnRect {
	px, py, pw, _ := a.comparePanelRect()
	return btnRect{x: px + pw - 4, y: py, w: 3}
}

// compareRefreshRect is the ⟳ button — recompute against the buffer as
// it is NOW. A diff is a snapshot, and the buffer moves under it with
// every keystroke; this is how the user says "again".
func (a *App) compareRefreshRect() btnRect {
	c := a.compareCloseRect()
	return btnRect{x: c.x - 4, y: c.y, w: 3}
}

// compareClampScroll pins the scroll offset into range. Hard clamp, no
// overscroll — it's a viewer, same as the git panels.
func (a *App) compareClampScroll() {
	_, _, _, ph := a.comparePanelRect()
	visible := ph - 1 // header row
	max := len(a.compare.lines) - visible
	if max < 0 {
		max = 0
	}
	if a.compare.scroll > max {
		a.compare.scroll = max
	}
	if a.compare.scroll < 0 {
		a.compare.scroll = 0
	}
}

// comparePanelScroll wheels the diff text.
func (a *App) comparePanelScroll(delta int) {
	a.compare.scroll += delta
	a.compareClampScroll()
}

// -----------------------------------------------------------------------------
// Mouse
// -----------------------------------------------------------------------------

// comparePanelPress routes an initial left press and reports the drag it
// started, if any. The header rule outside the buttons is the height
// handle; a double-click on a diff row jumps the editor to that line.
func (a *App) comparePanelPress(x, y int) (dragMode string) {
	_, py, _, _ := a.comparePanelRect()
	if y == py {
		if a.compareCloseRect().contains(x, y) {
			a.closeComparePanel()
			return ""
		}
		if a.compareRefreshRect().contains(x, y) {
			a.compareRefresh()
			return ""
		}
		return "comparepanel"
	}
	// Body: single clicks are inert, a double-click jumps — the same
	// gesture (and the same lastClick record) as the git panel's diff.
	now := time.Now()
	if a.lastClick.x == x && a.lastClick.y == y && now.Sub(a.lastClick.when) < doubleClickMs {
		a.lastClick = clickRecord{}
		a.compareJumpToRow(a.compare.scroll + (y - py - 1))
		return ""
	}
	a.lastClick = clickRecord{x: x, y: y, when: now}
	return ""
}

// compareRefresh re-runs the comparison against the buffer as it is
// NOW — a diff is a snapshot and the buffer moves under it with every
// keystroke. A file-backed old side is RE-READ (it may have moved too);
// a pasted one re-diffs the lines it's holding, since there is nothing
// to read it from again.
func (a *App) compareRefresh() {
	switch {
	case a.compare.oldPath != "":
		a.compareWithFile(a.compare.oldPath)
	case a.compare.oldLines != nil:
		a.runCompare(a.compare.oldLabel, "", a.compare.oldLines)
	}
}

// compareJumpToRow opens the "+" side's file and moves the cursor to the
// buffer line behind diff row idx. Best-effort, exactly like the git
// panel's: rows with no line mapping (headers, an empty pane) do
// nothing, and a line past EOF clamps.
func (a *App) compareJumpToRow(idx int) {
	if a.compare.newPath == "" {
		return
	}
	line, ok := diffTargetLine(a.compare.lines, idx)
	if !ok {
		return
	}
	a.openFile(a.compare.newPath)
	t := a.activeTabPtr()
	if t == nil || t.Path != a.compare.newPath {
		return // open failed — openFile flashed why
	}
	if lc := t.Buffer.LineCount(); line >= lc {
		line = lc - 1
	}
	a.goToLine(line, 0)
}

// -----------------------------------------------------------------------------
// Drawing
// -----------------------------------------------------------------------------

// drawComparePanel paints the header rule (labels, +/− stats, ⟳ and ✕)
// and the unified diff below it, colored by the same function the git
// panels use.
func (a *App) drawComparePanel() {
	px, py, pw, ph := a.comparePanelRect()
	th := a.theme

	headerSt := tcell.StyleDefault.Background(th.SidebarBG).Foreground(th.Subtle)
	titleSt := tcell.StyleDefault.Background(th.SidebarBG).Foreground(th.Accent).Bold(true)
	bodyBG := tcell.StyleDefault.Background(th.BG)

	ruleSt := headerSt
	if a.dragMode == "comparepanel" {
		ruleSt = tcell.StyleDefault.Background(th.SidebarBG).Foreground(th.Accent)
	}
	for cx := px; cx < px+pw; cx++ {
		a.screen.SetContent(cx, py, '─', nil, ruleSt)
	}
	closeBtn := a.compareCloseRect()
	drawAt(a.screen, closeBtn.x, closeBtn.y, " ✕ ", titleSt)
	refresh := a.compareRefreshRect()
	drawAt(a.screen, refresh.x, refresh.y, " ⟳ ", titleSt)

	// Header: "Compare · old ↔ new" then the +/− counts, dropped in that
	// order when the panel is too narrow — the labels are what identify
	// the comparison, the counts are a summary of what's already drawn.
	title := " Compare · " + a.compare.oldLabel + " ↔ " + a.compare.newLabel + " "
	rightEdge := refresh.x
	if a.compare.added > 0 || a.compare.remove > 0 {
		stats := "+" + itoa(a.compare.added) + " −" + itoa(a.compare.remove) + " "
		if cx := rightEdge - runeLen(stats) - 1; cx > px+runeLen(title) {
			drawAt(a.screen, cx, py, stats,
				tcell.StyleDefault.Background(th.SidebarBG).Foreground(th.GitModified))
			rightEdge = cx
		}
	}
	if px+1+runeLen(title) <= rightEdge {
		drawAt(a.screen, px+1, py, title, titleSt)
	}

	// Body.
	for row := 0; row < ph-1; row++ {
		ry := py + 1 + row
		for cx := px; cx < px+pw; cx++ {
			a.screen.SetContent(cx, ry, ' ', nil, bodyBG)
		}
	}
	if msg := a.compareEmptyMessage(); msg != "" {
		drawAt(a.screen, px+2, py+1, msg,
			tcell.StyleDefault.Background(th.BG).Foreground(th.Muted))
		return
	}
	for row := 0; row < ph-1; row++ {
		idx := a.compare.scroll + row
		if idx < 0 || idx >= len(a.compare.lines) {
			break
		}
		line := a.compare.lines[idx]
		st := gitPanelDiffStyle(line, th)
		if runeLen(line) > pw-3 {
			line = string([]rune(line)[:pw-4]) + "…"
		}
		drawAt(a.screen, px+2, py+1+row, line, st)
	}
}

// compareEmptyMessage is what the body says when there's no diff to
// draw: the armed-for-paste instruction, the "no differences" result, or
// the nothing-compared-yet prompt. Returns "" when there ARE lines.
func (a *App) compareEmptyMessage() string {
	switch {
	case len(a.compare.lines) > 0:
		return ""
	case a.compare.awaitPaste:
		return "Paste now to compare against " + a.compare.newLabel +
			"  (⌘V, or your terminal's paste) · Esc cancels"
	case a.compare.identical:
		return "No differences"
	default:
		return "Nothing compared yet — use ≡ → Compare"
	}
}

// compareCancelPaste disarms a pending "paste to compare", reporting
// whether it had anything to do. Wired into the Esc branch as a side
// effect (like clearing the ghost or the chat highlight): a mode that
// silently claims the next paste has to be escapable, and Esc must
// still fall through to the menu / leader handling underneath.
func (a *App) compareCancelPaste() bool {
	if !a.compare.awaitPaste {
		return false
	}
	a.compare.awaitPaste = false
	a.flash("Compare with pasted text: cancelled")
	return true
}

// hasSavedCopy gates "Compare with saved copy": there has to be a file
// on disk for the buffer to be compared against.
func (a *App) hasSavedCopy() bool {
	t := a.activeTabPtr()
	return t != nil && t.Path != "" && t.Buffer != nil && !t.IsImage()
}
