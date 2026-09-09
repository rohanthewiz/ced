// =============================================================================
// File: internal/app/markdown.go
// Author: Rohan Allison
// Created: 2026-09-09
// Copyright: 2026 Cloudmanic, LLC. All rights reserved.
// =============================================================================

// markdown.go is the UI half of the markdown viewer: the toggle, its
// surfaces, and the four things the editor pane does differently while a
// preview is up — scroll by rows, jump to source on a double-click,
// swallow editing keys, and count off-screen ROWS for the overflow
// markers. The rendering itself is internal/editor (markdown.go and its
// two halves); this file never lays anything out.
//
// House rules the shape follows:
//
//   - IT IS PER TAB, NOT A PREFERENCE. There is no config key and no
//     "open .md in preview by default": the toggle answers "how do I
//     want to look at THIS file right now", and the same file is a
//     document one minute and something you are editing the next. A
//     persisted default would also have to decide what happens when you
//     type into a preview, and the honest answer to that is that you
//     cannot — which is a bad thing to arrive at by surprise on a file
//     you opened to fix a typo in.
//
//   - THE BUFFER IS NEVER TOUCHED. Save, auto-save, the LSP sync, the
//     git gutter and the disk reconciler all keep running on a
//     previewed tab, because it is still an open file with unsaved
//     edits in it. Only the DRAWING changed.
//
//   - Keys are swallowed, the image tab's rule, with navigation carved
//     out. Dropping every key would make the preview unscrollable from
//     a keyboard; letting them through would put characters into a file
//     the user cannot see the caret in. Leaders are dispatched well
//     above this, so Esc-v toggles back and the whole ≡ menu stays
//     reachable from inside a preview.

package app

import (
	"time"

	"github.com/gdamore/tcell/v2"

	"github.com/rohanthewiz/ced/internal/editor"
)

// mdKeyScrollRows is how far the arrow keys move the preview. Three
// rows rather than one: a display row is often a wrapped fragment of a
// sentence, so a one-row step reads as the text twitching.
const mdKeyScrollRows = 3

// markdownTab returns the active tab when it is currently being drawn as
// a preview, else nil. The single predicate every branch in this file
// (and the ones in app.go and overflow.go) asks, so "is a preview up"
// can never mean two things.
func (a *App) markdownTab() *editor.Tab {
	t := a.activeTabPtr()
	if t == nil || !t.IsMarkdownView() {
		return nil
	}
	return t
}

// hasMarkdownPreview reports whether the ≡ row should be live: a text
// tab whose name says markdown. The row is DIMMED rather than hidden on
// other files, unlike the AI rows that flash a reason — there is no
// reason to give beyond "this is not a markdown file", which the label
// and the filename already say between them.
func (a *App) hasMarkdownPreview() bool {
	t := a.activeTabPtr()
	return t != nil && t.MarkdownCapable()
}

// markdownToggleLabel names the state the row will switch TO, and says
// which state is in force by naming the other one — the sidebar and
// terminal toggles' convention.
func (a *App) markdownToggleLabel() string {
	if a.markdownTab() != nil {
		return "Show markdown source"
	}
	return "Preview markdown"
}

// menuToggleMarkdownView is the ≡ row and the Esc-v leader.
func (a *App) menuToggleMarkdownView() {
	a.closeMenu()
	a.toggleMarkdownView()
}

// toggleMarkdownView flips the active tab's preview. The single write
// path (the setWordHighlight shape), so the leader, the menu row and the
// palette entry can never drift apart.
//
// A refusal names the file rather than the rule: "not a markdown file"
// is actionable, "unavailable" is not.
func (a *App) toggleMarkdownView() {
	t := a.activeTabPtr()
	if t == nil {
		a.flash("No file open")
		return
	}
	if !t.MarkdownCapable() {
		a.flash(t.DisplayName() + " is not a markdown file")
		return
	}
	on := !t.IsMarkdownView()
	t.SetMarkdownView(on)
	if on {
		a.flash("Markdown preview — esc v for source, double-click a line to edit it")
		return
	}
	a.flash("Markdown source")
}

// leaveMarkdownView drops the preview and puts the caret on line, the
// path a double-click and the Enter key share. Landing the caret is what
// makes the preview a navigation surface rather than a dead end: you
// read until something is wrong, point at it, and you are editing it.
func (a *App) leaveMarkdownView(t *editor.Tab, line int) {
	if t == nil {
		return
	}
	t.SetMarkdownView(false)
	if line >= 0 && t.Buffer != nil {
		if max := t.Buffer.LineCount() - 1; line > max {
			line = max
		}
		// Through MoveCursorTo, so the jump drops any multi-caret set and
		// the next render scrolls the line into view — the same contract
		// every other explicit jump in the editor has.
		t.MoveCursorTo(editor.Position{Line: line, Col: 0}, false)
		ex, ey, ew, eh := a.editorRect()
		_, _ = ex, ey
		t.CenterOnCursor(ew, eh)
	}
}

// markdownRows lays the active preview out at the current pane width.
// Every consumer outside the draw — scrolling, hit-testing, the overflow
// counts — asks through here, so all four agree with what is on screen.
func (a *App) markdownRows(t *editor.Tab) []editor.MDRow {
	if t == nil {
		return nil
	}
	_, _, ew, _ := a.editorRect()
	return t.MarkdownRows(a.theme, editor.MDContentWidth(ew))
}

// markdownScroll moves a preview's viewport, the wheel's entry point.
func (a *App) markdownScroll(t *editor.Tab, delta int) {
	_, _, _, eh := a.editorRect()
	t.MDScrollBy(delta, len(a.markdownRows(t)), eh)
}

// handleMarkdownKey routes a keystroke while a preview owns the editor
// pane. It reports whether the key was consumed; everything it does not
// claim is DROPPED by the caller rather than passed through, because the
// alternative is typing into a buffer with no visible caret.
func (a *App) handleMarkdownKey(t *editor.Tab, ev *tcell.EventKey) bool {
	_, _, _, eh := a.editorRect()
	total := len(a.markdownRows(t))
	page := eh - 2
	if page < 1 {
		page = 1
	}
	switch ev.Key() {
	case tcell.KeyUp:
		t.MDScrollBy(-mdKeyScrollRows, total, eh)
	case tcell.KeyDown:
		t.MDScrollBy(mdKeyScrollRows, total, eh)
	case tcell.KeyPgUp:
		t.MDScrollBy(-page, total, eh)
	case tcell.KeyPgDn:
		t.MDScrollBy(page, total, eh)
	case tcell.KeyHome:
		t.MDScroll = 0
	case tcell.KeyEnd:
		t.MDScroll = t.MDMaxScroll(total, eh)
	case tcell.KeyEnter:
		// Enter is the keyboard twin of the double-click: leave the
		// preview at whatever the reader was looking at, which is the
		// row at the TOP of the viewport — the one they scrolled to.
		a.leaveMarkdownView(t, editor.MDLineForRow(a.markdownRows(t), t.MDScroll))
	case tcell.KeyRune:
		switch ev.Rune() {
		case ' ':
			t.MDScrollBy(page, total, eh)
		case 'g':
			t.MDScroll = 0
		case 'G':
			t.MDScroll = t.MDMaxScroll(total, eh)
		default:
			return false
		}
	default:
		return false
	}
	return true
}

// markdownPress handles a click inside a preview. A single click does
// nothing — there is no caret to place and no selection to start, so a
// press that moved something invisible would be a mystery — while a
// DOUBLE click leaves the preview on that line. Same gesture the git
// panels use to go from a row to the code it describes.
func (a *App) markdownPress(t *editor.Tab, x, y int) {
	ex, ey, ew, eh := a.editorRect()
	if x < ex || x >= ex+ew || y < ey || y >= ey+eh {
		return
	}
	now := time.Now()
	if a.lastClick.x == x && a.lastClick.y == y && now.Sub(a.lastClick.when) < doubleClickMs {
		a.lastClick = clickRecord{}
		rows := a.markdownRows(t)
		a.leaveMarkdownView(t, editor.MDLineForRow(rows, t.MDScroll+(y-ey)))
		return
	}
	a.lastClick = clickRecord{x: x, y: y, when: now}
}
