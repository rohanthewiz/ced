// =============================================================================
// File: internal/app/hunkview.go
// Author: Rohan Allison <rohanthewiz@gmail.com>
// Created: 2026-10-21
// Copyright: 2026 Rohan Allison. All rights reserved.
// =============================================================================

// hunkview.go is the change popup's (hunktip.go) big sibling: a modal
// that shows ONE hunk's diff, then the original text as it read in place
// with a little of the surrounding code, and offers to put it back.
//
//	┌ Changed · lines 41–42  −1 +2                     esc ┐
//	├──────────────────────────────────────────────────────┤
//	│ Diff                                                 │
//	│ -	return parseAll(src, opts)                        │
//	│ +	return parse(src)                                 │
//	│ +	// opts moved to the caller                       │
//	│                                                      │
//	│ Original — what Revert puts back                     │
//	│   func load(src string) error {        ← context     │
//	│   	return parseAll(src, opts)          ← restored   │
//	│   }                                    ← context     │
//	│                                                      │
//	│       [ Revert change ] alt+r   [ Close ]            │
//	└──────────────────────────────────────────────────────┘
//
// Doors (every one reaches openHunkView with a tab and a hunk):
//
//   - ≡ Git "Show change at caret…" — the keyboard door.
//   - Editor right-click "Show change…" when the clicked line is part of
//     a change (contextmenu.go).
//   - A click inside the change popup (hunktip.go): the popup is passive
//     and can't own a button, so clicking it opens this view.
//
// Design choices:
//
//   - **The hunk comes from the gutter's own diff** (gitdiff.go:
//     `git diff -U0`, working tree vs the INDEX). So "original" means
//     what git has staged, which is HEAD unless the file has staged
//     changes. That is the same baseline the bar is drawn against, so
//     the view, the bar and Revert can never disagree about what the
//     change is.
//   - **Revert edits the BUFFER, not the disk**, as ONE undo step
//     (ApplyMultiEdit), and does not save — the workspace-edit rule for
//     open files. Undo brings the change back, so no confirm: the
//     destructive git verbs confirm because they lose work on disk.
//   - **The diff was measured on DISK, the edit lands in the BUFFER**,
//     and the two drift apart when the buffer has unsaved edits (or
//     just after a save, before the next diff arrives). So the hunk's
//     position is VERIFIED by content before anything is written: the
//     buffer must still hold the hunk's "+" lines at the hunk's lines. A
//     pure removal has no "+" lines to check, so it needs the hunks to
//     be known-exact for the buffer (hunksMatchBuffer): a clean buffer,
//     or one whose only edits since are ced's own reverts. A mismatch is
//     refused with the reason, never "fixed up" by guessing — writing
//     the original text into the wrong place would be worse than not
//     reverting.
//   - **End-of-file newlines are exact.** git marks a side without a
//     final newline with "\ No newline at end of file"; the edit is
//     built so a revert restores that state too, not just the lines.
//   - **The gutter catches up at once.** After a revert the hunk is
//     dropped from App.fileDiffs and later hunks shift by the line
//     delta, so the bar goes away immediately. The next disk diff (after
//     the save that auto-save or the user makes) confirms it. When the
//     hunks were exact before the revert they are exact after it, and
//     App.fileDiffsRev stamps that for the new EditRev — which is what
//     lets "revert this, then revert the removal below it" work without
//     a save in between.
//   - **One scrolling body** for both sections, with the change popup's
//     ▴/▾ and "a–b of n" in the border, because a large hunk would
//     otherwise push the buttons off the screen.
//   - **Close is the default button**, as in every confirm: Enter on a
//     view that just opened should not edit the file.

package app

import (
	"fmt"
	"strings"

	"github.com/gdamore/tcell/v2"

	"github.com/rohanthewiz/ced/internal/editor"
)

const (
	// hunkViewMaxWidth caps the modal. Code is clipped, not wrapped, so
	// the cap is the change popup's: every cell given back is a cell of
	// the line shown.
	hunkViewMaxWidth = 100

	// hunkViewMinWidth keeps the button row and the section headings
	// readable for a hunk of very short lines.
	hunkViewMinWidth = 50

	// hunkViewContext is how many unchanged lines frame the original on
	// each side: enough to see where it sits, few enough that the change
	// stays the subject.
	hunkViewContext = 2

	// hunkViewChrome is the rows around the body: top border, title,
	// divider, the blank above the buttons, the button row, bottom border.
	hunkViewChrome = 6

	// Button labels. The alt+r hint is drawn beside Revert (not inside
	// the brackets) so the click target stays the label alone.
	hunkViewRevertLabel = "[ Revert change ]"
	hunkViewRevertHint  = " alt+r"
	hunkViewCloseLabel  = "[ Close ]"
	hunkViewButtonGap   = 3
)

// hunkRowKind decides how one body row is painted.
type hunkRowKind uint8

const (
	hunkRowHeading  hunkRowKind = iota // "Diff", "Original — …"
	hunkRowRemoved                     // "-" diff line
	hunkRowAdded                       // "+" diff line
	hunkRowMeta                        // "\ No newline…", placeholder notes
	hunkRowContext                     // unchanged line around the original
	hunkRowOriginal                    // a line Revert puts back
	hunkRowBlank                       // section spacer
)

// hunkViewRow is one painted body row, tabs already expanded.
type hunkViewRow struct {
	text string
	kind hunkRowKind
}

// hunkViewModal is the modal's whole state. It holds the TAB, not a
// path: Revert edits that tab's buffer, and a pointer survives a rename
// that a path would not (tabIndexOf re-checks it is still open).
type hunkViewModal struct {
	tab    *editor.Tab
	hunk   diffHunk
	title  string
	rows   []hunkViewRow
	scroll int // first body row shown
	hover  int // 0 = Close (the default), 1 = Revert
}

// -----------------------------------------------------------------------------
// Doors
// -----------------------------------------------------------------------------

// menuShowHunkAtCaret is the ≡ Git door: the hunk on the caret's line.
// Enabled whenever the file has changes (hasDiffHunks, shared with Next
// / Previous change), so a caret that is NOT on one gets the reason and
// the way to one instead of a dimmed row.
func (a *App) menuShowHunkAtCaret() {
	a.closeMenu()
	t := a.activeTabPtr()
	if t == nil {
		return
	}
	h, ok := a.hunkAtLine(t, t.Cursor.Line)
	if !ok {
		a.flash("No change on this line — Next change jumps to one")
		return
	}
	a.openHunkView(t, h)
}

// hasHunkAtCaret gates the editor right-click row: the clicked line (the
// click already moved the caret there) is part of a change.
func (a *App) hasHunkAtCaret() bool {
	t := a.activeTabPtr()
	if t == nil {
		return false
	}
	_, ok := a.hunkAtLine(t, t.Cursor.Line)
	return ok
}

// hunkAtLine finds the hunk whose bar is drawn on a buffer line. A
// removal's bar sits on its boundary line (Start == End), so the same
// inclusive test covers all three kinds.
func (a *App) hunkAtLine(t *editor.Tab, line int) (diffHunk, bool) {
	if t == nil || t.Path == "" || t.IsImage() {
		return diffHunk{}, false
	}
	for _, h := range a.fileDiffs[t.Path] {
		if line >= h.Start && line <= h.End {
			return h, true
		}
	}
	return diffHunk{}, false
}

// openHunkView shows the modal for one hunk of one tab. The rows are
// built once, here: the modal owns the screen while it is open, so the
// buffer cannot change under it (Revert re-verifies anyway).
func (a *App) openHunkView(t *editor.Tab, h diffHunk) {
	if len(h.Body) == 0 {
		a.flash("git recorded no lines for this change")
		return
	}
	a.closeHunkTip()
	a.openModal(&hunkViewModal{
		tab:   t,
		hunk:  h,
		title: hunkTipTitle(h),
		rows:  a.hunkViewRows(t, h),
	})
}

// -----------------------------------------------------------------------------
// The two sides of a hunk, and the revert edit
// -----------------------------------------------------------------------------

// hunkSides is a hunk body split into what git has (old) and what the
// file has now (new), prefixes stripped. The NoEOL flags record git's
// "\ No newline at end of file" note, which applies to the side whose
// line it FOLLOWS.
type hunkSides struct {
	old, new           []string
	oldNoEOL, newNoEOL bool
}

// splitHunkSides parses a hunk body. Carriage returns are dropped: the
// buffer is always bare LF (fileio.go re-emits CRLF on write), so a
// CRLF file's diff lines must lose their "\r" to compare or restore.
func splitHunkSides(h diffHunk) hunkSides {
	var s hunkSides
	last := byte(0) // prefix of the previous -/+ line, for the "\" note
	for _, ln := range h.Body {
		if ln == "" {
			continue
		}
		switch ln[0] {
		case '-':
			s.old = append(s.old, strings.TrimRight(ln[1:], "\r"))
			last = '-'
		case '+':
			s.new = append(s.new, strings.TrimRight(ln[1:], "\r"))
			last = '+'
		case '\\':
			switch last {
			case '-':
				s.oldNoEOL = true
			case '+':
				s.newNoEOL = true
			}
		}
	}
	return s
}

// hunkStaleReason is the refusal when the buffer no longer holds the
// change where git saw it. Saving re-runs the diff, which is the fix.
const hunkStaleReason = "This change has moved since git looked — save the file and try again"

// hunkRevertEdit builds the single buffer edit that puts a hunk's old
// side back, or says why it can't. Nothing is written here; the caller
// applies the edit, so the verification and the edit share one buffer
// read.
//
// The edit's shape, by kind (lines are 0-based buffer indexes):
//
//	added / modified   replace lines Start..End (the "+" side, verified)
//	                   with the "-" side (empty for an addition)
//	removed            insert the "-" side after line Start (at 0 when
//	                   Top); needs exact hunks — nothing to verify
//
// The region normally ends at the start of the line after End, and the
// replacement carries its own trailing "\n". The two EOF notes bend
// that: a new side without a final newline ends at the END of line End
// (there is no next line), and an old side without one drops the
// trailing "\n" so the restored file ends where the original did.
//
// exact says the hunks are known to match the buffer (hunksMatchBuffer);
// only a removal needs it, the other kinds verify themselves.
func hunkRevertEdit(t *editor.Tab, h diffHunk, exact bool) (editor.Edit, string) {
	if t == nil || t.Buffer == nil {
		return editor.Edit{}, "The file is no longer open"
	}
	s := splitHunkSides(h)
	if len(s.old) == 0 && len(s.new) == 0 {
		return editor.Edit{}, "git recorded no lines for this change"
	}
	lines := t.Buffer.Lines
	n := len(lines)

	text := strings.Join(s.old, "\n")
	if len(s.old) > 0 && !s.oldNoEOL {
		text += "\n"
	}

	if h.Kind == diffDeleted {
		if !exact {
			return editor.Edit{}, "Save the file first — a removal can only be placed against the saved file"
		}
		at := editor.Position{Line: 0}
		if !h.Top {
			if h.Start+1 >= n {
				return editor.Edit{}, hunkStaleReason
			}
			at = editor.Position{Line: h.Start + 1}
		}
		return editor.Edit{Start: at, End: at, NewText: text}, ""
	}

	// Added / modified: the buffer must still hold the "+" side exactly
	// at Start..End.
	if h.Start < 0 || h.End >= n || h.End-h.Start+1 != len(s.new) {
		return editor.Edit{}, hunkStaleReason
	}
	for i, want := range s.new {
		if lines[h.Start+i] != want {
			return editor.Edit{}, hunkStaleReason
		}
	}
	start := editor.Position{Line: h.Start}
	var end editor.Position
	switch {
	case s.newNoEOL:
		// The file ends on line End with no newline: the region runs to
		// the end of that line, there being no next line to stop at.
		if h.End != n-1 {
			return editor.Edit{}, hunkStaleReason
		}
		end = editor.Position{Line: h.End, Col: len([]rune(lines[h.End]))}
	default:
		if h.End+1 >= n {
			return editor.Edit{}, hunkStaleReason
		}
		// An old side without a final newline reached the old EOF, so
		// the new side must reach the new one: only the empty line after
		// the file's last "\n" may follow.
		if s.oldNoEOL && (h.End+1 != n-1 || lines[n-1] != "") {
			return editor.Edit{}, hunkStaleReason
		}
		end = editor.Position{Line: h.End + 1}
	}
	return editor.Edit{Start: start, End: end, NewText: text}, ""
}

// revertHunk applies the edit for one hunk of one tab, reporting why it
// didn't when it didn't. On success the caret lands on the restored
// text (a jump, so it comes with context) and the gutter drops the bar.
func (a *App) revertHunk(t *editor.Tab, h diffHunk) string {
	if a.tabIndexOf(t) < 0 {
		return "The file is no longer open"
	}
	exact := a.hunksMatchBuffer(t)
	e, why := hunkRevertEdit(t, h, exact)
	if why != "" {
		return why
	}
	if _, ok := t.ApplyMultiEdit([]editor.Edit{e}); !ok {
		return "The change could not be reverted"
	}
	t.MoveCursorTo(editor.Position{Line: e.Start.Line}, false)
	t.MarkJump()
	a.dropRevertedHunk(t.Path, h)
	// Exact in, exact out: the shift in dropRevertedHunk is the revert's
	// own line delta, so hunks that matched the buffer before still do.
	// Hunks that were only trusted line-by-line stay untrusted — pinned
	// with a rev no buffer has (-1) rather than by deleting the stamp,
	// because without one an Undo back to clean would read as exact.
	if a.fileDiffsRev == nil {
		a.fileDiffsRev = map[string]int{}
	}
	a.fileDiffsRev[t.Path] = -1
	if exact {
		a.fileDiffsRev[t.Path] = t.EditRev
	}
	return ""
}

// hunksMatchBuffer reports whether the gutter's hunks for a tab are in
// the BUFFER's coordinates: every edit since the last disk diff was a
// hunk revert made on exact hunks (the fileDiffsRev stamp still names
// the current EditRev), or there has been no revert and the buffer is
// unedited (so it is the file git diffed).
//
// The stamp is checked FIRST, and a stale one means "unknown" even on a
// clean buffer: Undo of a revert returns the buffer to the saved text,
// but the hunks were already shifted for the revert, so the clean flag
// alone would trust coordinates that are now off. The next disk diff
// (every tick, every save) clears the stamp and the doubt with it.
func (a *App) hunksMatchBuffer(t *editor.Tab) bool {
	if rev, ok := a.fileDiffsRev[t.Path]; ok {
		return rev == t.EditRev
	}
	return !t.Dirty
}

// dropRevertedHunk removes a reverted hunk from the gutter's diff and
// shifts the hunks below it by the line delta the revert made, so the
// bars stay on their lines until the next disk diff replaces them all.
// A hunk that is no longer in the map (a diff landed meanwhile) leaves
// the map alone: whatever is there is newer than what was reverted.
func (a *App) dropRevertedHunk(path string, h diffHunk) {
	hunks := a.fileDiffs[path]
	idx := -1
	for i, o := range hunks {
		if o.Start == h.Start && o.End == h.End && o.Kind == h.Kind && o.Top == h.Top {
			idx = i
			break
		}
	}
	if idx < 0 {
		return
	}
	s := splitHunkSides(h)
	delta := len(s.old) - len(s.new)
	out := make([]diffHunk, 0, len(hunks)-1)
	out = append(out, hunks[:idx]...)
	for _, o := range hunks[idx+1:] {
		o.Start += delta
		o.End += delta
		out = append(out, o)
	}
	if len(out) == 0 {
		delete(a.fileDiffs, path)
		return
	}
	a.fileDiffs[path] = out
}

// -----------------------------------------------------------------------------
// Rows
// -----------------------------------------------------------------------------

// hunkViewRows builds the body: the diff as git shows it, then the
// original in place. Context lines come from the BUFFER, and only when
// the hunk's position checks out — around a stale hunk they would frame
// the original with the wrong code, so the original is shown bare.
func (a *App) hunkViewRows(t *editor.Tab, h diffHunk) []hunkViewRow {
	rows := []hunkViewRow{{text: "Diff", kind: hunkRowHeading}}
	for _, ln := range hunkTipLines(h) {
		kind := hunkRowMeta
		switch {
		case strings.HasPrefix(ln, "-"):
			kind = hunkRowRemoved
		case strings.HasPrefix(ln, "+"):
			kind = hunkRowAdded
		}
		rows = append(rows, hunkViewRow{text: ln, kind: kind})
	}

	rows = append(rows,
		hunkViewRow{kind: hunkRowBlank},
		hunkViewRow{text: "Original — what Revert puts back", kind: hunkRowHeading},
	)
	_, why := hunkRevertEdit(t, h, a.hunksMatchBuffer(t))
	placed := why == ""
	above, below := hunkContextRange(t, h)
	tab := strings.Repeat(" ", hunkTipTabWidth)
	ctx := func(from, to int) {
		if !placed {
			return
		}
		for i := from; i <= to; i++ {
			rows = append(rows, hunkViewRow{
				text: "  " + strings.ReplaceAll(t.Buffer.Lines[i], "\t", tab),
				kind: hunkRowContext,
			})
		}
	}

	ctx(above[0], above[1])
	s := splitHunkSides(h)
	if len(s.old) == 0 {
		rows = append(rows, hunkViewRow{text: "  (nothing — these lines were added)", kind: hunkRowMeta})
	}
	for _, ln := range s.old {
		rows = append(rows, hunkViewRow{text: "  " + strings.ReplaceAll(ln, "\t", tab), kind: hunkRowOriginal})
	}
	ctx(below[0], below[1])
	if !placed {
		rows = append(rows, hunkViewRow{kind: hunkRowBlank}, hunkViewRow{text: why, kind: hunkRowMeta})
	}
	return rows
}

// hunkContextRange returns the inclusive buffer-line ranges of the
// unchanged lines just above and below where the original sits. An
// empty range has from > to. The empty line after a file's final "\n"
// is never context: it is not a line of the file, just where the caret
// can go.
func hunkContextRange(t *editor.Tab, h diffHunk) (above, below [2]int) {
	n := len(t.Buffer.Lines)
	last := n - 1
	if last > 0 && t.Buffer.Lines[last] == "" {
		last--
	}
	// The original replaces Start..End (added / modified) or goes in
	// after Start (removed; before line 0 when Top).
	topEnd, botStart := h.Start-1, h.End+1
	if h.Kind == diffDeleted {
		topEnd, botStart = h.Start, h.Start+1
		if h.Top {
			topEnd, botStart = -1, 0
		}
	}
	above = [2]int{max(topEnd-hunkViewContext+1, 0), topEnd}
	below = [2]int{botStart, min(botStart+hunkViewContext-1, last)}
	return above, below
}

// -----------------------------------------------------------------------------
// The modal
// -----------------------------------------------------------------------------

// rect sizes the modal to its content, capped by the window: as wide as
// the longest row (or the title, or the button row), as tall as the
// rows plus chrome.
func (m *hunkViewModal) rect(a *App) (x, y, w, h int) {
	w = max(runeLen(m.title)+10, hunkViewMinWidth)
	for _, r := range m.rows {
		w = max(w, runeLen(r.text)+4)
	}
	w = min(w, hunkViewMaxWidth, a.width)
	h = min(len(m.rows)+hunkViewChrome, max(a.height, hunkViewChrome+1))
	return a.centeredRect(w, h)
}

// bodyRows is how many body rows the current geometry shows.
func (m *hunkViewModal) bodyRows(a *App) int {
	_, _, _, h := m.rect(a)
	return max(h-hunkViewChrome, 1)
}

// buttons returns the Revert and Close rects: the one geometry draw and
// hit-testing share. The row is centered as a whole, hint included.
func (m *hunkViewModal) buttons(a *App) (revert, closeBtn btnRect) {
	mx, my, mw, mh := m.rect(a)
	rw, hw, cw := runeLen(hunkViewRevertLabel), runeLen(hunkViewRevertHint), runeLen(hunkViewCloseLabel)
	total := rw + hw + hunkViewButtonGap + cw
	bx := mx + max((mw-total)/2, 1)
	by := my + mh - 2
	return btnRect{x: bx, y: by, w: rw}, btnRect{x: bx + rw + hw + hunkViewButtonGap, y: by, w: cw}
}

// scrollBy moves the body, clamped to the content.
func (m *hunkViewModal) scrollBy(a *App, delta int) {
	m.scroll = clampHunkTipScroll(m.scroll+delta, len(m.rows), m.bodyRows(a))
}

// revert runs the revert. Success closes the modal and says how to get
// the change back; a refusal keeps the view open and flashes why, so
// the user can still read what they were about to undo.
func (m *hunkViewModal) revert(a *App) {
	if why := a.revertHunk(m.tab, m.hunk); why != "" {
		a.flash(why)
		return
	}
	a.closeModal()
	a.flash("Reverted the change — Undo brings it back")
}

// handleKey: Esc closes; alt+r reverts (the button's in-surface twin);
// Tab / ←/→ move the button focus and Enter presses it; the arrows,
// PgUp/PgDn and Home/End scroll the body.
func (m *hunkViewModal) handleKey(a *App, ev *tcell.EventKey) {
	if ev.Key() == tcell.KeyRune && ev.Modifiers()&tcell.ModAlt != 0 && ev.Rune() == 'r' {
		m.revert(a)
		return
	}
	page := max(m.bodyRows(a)-1, 1)
	switch ev.Key() {
	case tcell.KeyEsc:
		a.closeModal()
	case tcell.KeyEnter:
		if m.hover == 1 {
			m.revert(a)
		} else {
			a.closeModal()
		}
	case tcell.KeyTab, tcell.KeyBacktab:
		m.hover = 1 - m.hover
	case tcell.KeyLeft:
		m.hover = 1 // Revert is the left button
	case tcell.KeyRight:
		m.hover = 0
	case tcell.KeyUp:
		m.scrollBy(a, -1)
	case tcell.KeyDown:
		m.scrollBy(a, 1)
	case tcell.KeyPgUp:
		m.scrollBy(a, -page)
	case tcell.KeyPgDn:
		m.scrollBy(a, page)
	case tcell.KeyHome:
		m.scroll = 0
	case tcell.KeyEnd:
		m.scrollBy(a, len(m.rows))
	}
}

// handleMouse: hover tracks the buttons, a click presses one, the wheel
// scrolls the body, and a click outside the modal closes it.
func (m *hunkViewModal) handleMouse(a *App, x, y int, btn tcell.ButtonMask) {
	mx, my, mw, mh := m.rect(a)
	inside := x >= mx && x < mx+mw && y >= my && y < my+mh
	if btn&(tcell.WheelUp|tcell.WheelDown) != 0 {
		if inside {
			if btn&tcell.WheelUp != 0 {
				m.scrollBy(a, -wheelLines)
			} else {
				m.scrollBy(a, wheelLines)
			}
		}
		return
	}
	revert, closeBtn := m.buttons(a)
	switch {
	case revert.contains(x, y):
		m.hover = 1
	case closeBtn.contains(x, y):
		m.hover = 0
	}
	if btn&tcell.Button1 == 0 {
		return
	}
	switch {
	case !inside:
		a.closeModal()
	case revert.contains(x, y):
		m.revert(a)
	case closeBtn.contains(x, y):
		a.closeModal()
	}
}

// hunkViewRowStyle paints one body row by kind, on the modal background.
func (a *App) hunkViewRowStyle(k hunkRowKind, c modalChrome) tcell.Style {
	switch k {
	case hunkRowHeading:
		return c.body.Bold(true)
	case hunkRowRemoved:
		return c.body.Foreground(a.theme.GitDeleted)
	case hunkRowAdded:
		return c.body.Foreground(a.theme.GitAdded)
	case hunkRowMeta, hunkRowContext:
		return c.muted
	default:
		return c.body
	}
}

// draw paints the frame, the scrolled body (with the change popup's
// ▴/▾ and "a–b of n" when it scrolls) and the button row.
func (m *hunkViewModal) draw(a *App) {
	mx, my, mw, mh := m.rect(a)
	c := a.chrome()
	c.drawFrame(a.screen, mx, my, mw, mh, clipRunes(m.title, max(mw-8, 1)))

	body := m.bodyRows(a)
	n := len(m.rows)
	m.scroll = clampHunkTipScroll(m.scroll, n, body)
	textW := mw - 4
	for i := 0; i < body; i++ {
		idx := m.scroll + i
		if idx >= n {
			break
		}
		r := m.rows[idx]
		drawAt(a.screen, mx+2, my+3+i, clipRunes(r.text, textW), a.hunkViewRowStyle(r.kind, c))
	}
	if n > body {
		if m.scroll > 0 {
			a.screen.SetContent(mx+mw-1, my+3, overflowUpRune, nil, c.title)
		}
		if m.scroll+body < n {
			a.screen.SetContent(mx+mw-1, my+2+body, overflowDownRune, nil, c.title)
		}
		pos := fmt.Sprintf(" %d–%d of %d ", m.scroll+1, m.scroll+body, n)
		if px := mx + mw - 1 - runeLen(pos); px > mx {
			drawAt(a.screen, px, my+mh-1, pos, c.muted)
		}
	}

	revert, closeBtn := m.buttons(a)
	drawButton(a.screen, revert.x, revert.y, hunkViewRevertLabel, c.bg, a.theme.Error, m.hover == 1)
	drawAt(a.screen, revert.x+revert.w, revert.y, hunkViewRevertHint, c.muted)
	drawButton(a.screen, closeBtn.x, closeBtn.y, hunkViewCloseLabel, c.bg, a.theme.Text, m.hover == 0)
	a.screen.HideCursor()
}
