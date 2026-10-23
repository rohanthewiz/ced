// =============================================================================
// File: internal/app/comparerevert.go
// Author: Rohan Allison <rohanthewiz@gmail.com>
// Created: 2026-10-22
// Copyright: 2026 Rohan Allison. All rights reserved.
// =============================================================================

// comparerevert.go lets the compare panel put one hunk's OLD side back
// into the buffer it was compared from — "undo just this bit of what I
// changed since the save", or "take the pasted version of this block".
//
//	@@ -12,4 +12,5 @@                         [↺]  ← chip on the header row
//	 ctx                                     (same row in either layout)
//	-old line
//	+new line
//	+another
//	 ctx
//
//	buffer lines 11..15  ==  " ctx" "+new line" "+another" " ctx"  ← verified
//	        replaced by      " ctx" "-old line" " ctx"            ← old side
//
// Choices, all borrowed from the change view (hunkview.go) so the two
// reverts behave alike:
//
//   - THE BUFFER, NOT THE DISK, one undo step (ApplyMultiEdit), unsaved,
//     no confirm — Undo is the way back, and nothing on disk is lost.
//   - VERIFY, NEVER GUESS. The hunk's "+" side (context included) must
//     still be the buffer's text at the hunk's lines. A buffer typed in
//     since the diff is refused with the way out (⟳), never patched by
//     searching for where the text went.
//   - ONLY A WHOLE OPEN BUFFER can take a revert. A selection compare's
//     new side is a snapshot with no home, and a conflict compare's two
//     sides are resolved by the conflict verbs, not by overwriting one
//     with the other. Those refuse with the reason.
//   - THE COMPARISON RE-RUNS after a revert, keeping its scroll: every
//     other hunk's header numbers shift with the edit, so a second chip
//     must not act on the first one's stale coordinates.
//
// Under "ignore whitespace" the hunk's context lines carry the NEW
// side's spelling (diff.DiffWith), so the verification still matches
// the buffer and a revert leaves whitespace-only differences alone —
// which is what ignoring them asked for.

package app

import (
	"strings"

	"github.com/gdamore/tcell/v2"
	"github.com/rohanthewiz/ced/internal/editor"
)

// compareStaleReason is the refusal when the buffer no longer holds a
// hunk's "+" side where the diff saw it.
const compareStaleReason = "The buffer has changed since this comparison — ⟳ re-diffs it"

// compareRevertBlocked says why the current comparison can't take a
// revert, or "" when it can.
func (a *App) compareRevertBlocked() string {
	switch {
	case len(a.compare.lines) == 0:
		return "Nothing to revert — no differences"
	case a.compare.conflict != nil:
		return "Conflict sides are resolved with the conflict buttons, not reverted"
	case a.compare.newPath == "":
		return "Nothing to revert into — this comparison's new side isn't an open file"
	case a.compareTabIndex() < 0:
		return "Open " + a.relativePathFor(a.compare.newPath) + " to revert into it"
	}
	return ""
}

// compareTabIndex is the open tab holding the comparison's new side, or
// -1.
func (a *App) compareTabIndex() int {
	if a.compare.newPath == "" {
		return -1
	}
	for i, t := range a.tabs {
		if t.Path == a.compare.newPath && t.Buffer != nil && !t.IsImage() {
			return i
		}
	}
	return -1
}

// compareRevertEdit builds the single edit that turns a hunk's "+" side
// back into its "-" side, or says why it can't. lines is the compare
// panel's unified text; header indexes the hunk's "@@" line.
//
// Buffer geometry (lines are 0-based): the buffer is bare-LF lines, and
// a file ending in "\n" has an extra empty last line that is not a line
// of the diff (diff.SplitLines drops it). The replaced region runs from
// the start of the hunk's first line to the start of the line after
// its last; only when the hunk reaches the last line of a buffer WITHOUT
// a final newline does it end at that line's end instead, there being
// no next line to stop at.
func compareRevertEdit(t *editor.Tab, lines []string, header int) (editor.Edit, string) {
	if t == nil || t.Buffer == nil {
		return editor.Edit{}, "The file is no longer open"
	}
	if header < 0 || header >= len(lines) || !strings.HasPrefix(lines[header], "@@") {
		return editor.Edit{}, "That row isn't a change"
	}
	_, _, newStart, newCount, ok := parseHunkHeader(lines[header])
	if !ok {
		return editor.Edit{}, "That change's header can't be read"
	}
	var oldSide, newSide []string
	for i := header + 1; i < len(lines) && !strings.HasPrefix(lines[i], "@@"); i++ {
		ln := lines[i]
		if ln == "" {
			continue
		}
		switch ln[0] {
		case ' ':
			oldSide = append(oldSide, ln[1:])
			newSide = append(newSide, ln[1:])
		case '-':
			oldSide = append(oldSide, ln[1:])
		case '+':
			newSide = append(newSide, ln[1:])
		}
	}
	if len(newSide) != newCount {
		return editor.Edit{}, "That change's lines don't add up — ⟳ re-diffs it"
	}

	buf := t.Buffer.Lines
	n := len(buf)
	m := n // lines the diff saw: the empty line after a final "\n" isn't one
	if n > 0 && buf[n-1] == "" {
		m = n - 1
	}
	// internal/diff numbers a hunk with no new-side lines from 0 (the new
	// text is empty), so the region starts at the top either way.
	s := max(newStart-1, 0)
	if s+newCount > m {
		return editor.Edit{}, compareStaleReason
	}
	for k, want := range newSide {
		if buf[s+k] != want {
			return editor.Edit{}, compareStaleReason
		}
	}

	text := strings.Join(oldSide, "\n")
	start := editor.Position{Line: s}
	if s+newCount < n {
		if len(oldSide) > 0 {
			text += "\n"
		}
		return editor.Edit{Start: start, End: editor.Position{Line: s + newCount}, NewText: text}, ""
	}
	last := n - 1
	return editor.Edit{Start: start, End: editor.Position{Line: last, Col: len([]rune(buf[last]))}, NewText: text}, ""
}

// compareRevertHunk reverts the hunk whose "@@" line is header: the
// chip's and the picker's shared verb. The tab becomes the active one
// first — the revert is a buffer write, and the user should see it land
// (tabcontext.go's onTab rule for rows that write a buffer).
func (a *App) compareRevertHunk(header int) {
	if why := a.compareRevertBlocked(); why != "" {
		a.flash(why)
		return
	}
	i := a.compareTabIndex()
	t := a.tabs[i]
	e, why := compareRevertEdit(t, a.compare.lines, header)
	if why != "" {
		a.flash(why)
		return
	}
	if a.activeTab != i {
		a.switchToTab(i)
	}
	if _, ok := t.ApplyMultiEdit([]editor.Edit{e}); !ok {
		a.flash("The change could not be reverted")
		return
	}
	t.MoveCursorTo(editor.Position{Line: e.Start.Line}, false)
	t.MarkJump()

	scroll := a.compare.scroll
	a.compareRefresh()
	a.compare.scroll = scroll
	a.compareClampScroll()
	if a.compare.identical {
		a.flash("Reverted — no differences left · Undo brings the change back")
		return
	}
	a.flash("Reverted the change — Undo brings it back")
}

// compareRevertChipAt is the [↺] chip on display row row, ok=false when
// that row carries none: not a hunk header, scrolled out of the body, a
// comparison that can't take a revert, or a pane too narrow to keep a
// readable stub of the header beside the chip. Draw and the click router
// both ask here (the btnRect rule).
func (a *App) compareRevertChipAt(row int) (btnRect, bool) {
	if !a.compare.open || a.compareRevertBlocked() != "" {
		return btnRect{}, false
	}
	rows := a.compareRows()
	if row < 0 || row >= len(rows) || !rows[row].full {
		return btnRect{}, false
	}
	if !strings.HasPrefix(a.compare.lines[rows[row].left], "@@") {
		return btnRect{}, false
	}
	px, py, pw, ph := a.comparePanelRect()
	vis := row - a.compare.scroll
	if vis < 0 || vis >= ph-1 || pw-3 < 3+8 {
		return btnRect{}, false
	}
	// Right-aligned inside the text area (which stops a column short of
	// the panel's edge, where the overflow markers live).
	return btnRect{x: px + pw - 1 - 3, y: py + 1 + vis, w: 3}, true
}

// compareRevertChipTips labels the visible chips for buttontip.go.
func (a *App) compareRevertChipTips() []buttonTip {
	if !a.compare.open {
		return nil
	}
	_, _, _, ph := a.comparePanelRect()
	var tips []buttonTip
	for r := 0; r < ph-1; r++ {
		row := a.compare.scroll + r
		if chip, ok := a.compareRevertChipAt(row); ok {
			name := gitPanelHunkName(a.compare.lines, gitHunkSpan{header: a.compare.pane.lineAt(row)})
			tips = append(tips, buttonTip{rect: chip,
				text: []string{"Revert " + name + " to the old side", "(edits the buffer — Undo brings it back)"}})
		}
	}
	return tips
}

// menuCompareRevertPick is the ≡ Compare row: a picker of the hunks in
// the comparison, the keyboard's door to the chips (and the one that
// survives a terminal that eats clicks).
func (a *App) menuCompareRevertPick() {
	a.closeMenu()
	if why := a.compareRevertBlocked(); why != "" {
		a.flash(why)
		return
	}
	var items []paletteItem
	for i, ln := range a.compare.lines {
		if !strings.HasPrefix(ln, "@@") {
			continue
		}
		header := i
		items = append(items, paletteItem{
			label: "Revert " + gitPanelHunkName(a.compare.lines, gitHunkSpan{header: header}),
			run:   func(app *App) { app.compareRevertHunk(header) },
		})
	}
	if len(items) == 0 {
		a.flash("Nothing to revert — no differences")
		return
	}
	a.openPicker("Revert a change · "+a.relativePathFor(a.compare.newPath), items)
}

// compareRevertChipStyle is the chip's paint, the git panel's revert
// chip exactly — the same verb wears the same colour on both panels.
func (a *App) compareRevertChipStyle() tcell.Style {
	return tcell.StyleDefault.Background(a.theme.Selection).
		Foreground(gitHunkChipColor(hunkRevert, a.theme)).Bold(true)
}
