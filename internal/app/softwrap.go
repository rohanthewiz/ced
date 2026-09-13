// =============================================================================
// File: internal/app/softwrap.go
// Author: Rohan Allison <rohanthewiz@gmail.com>
// Created: 2026-09-13
// Copyright: 2026 Rohan Allison. All rights reserved.
// =============================================================================

// softwrap.go is the UI half of soft wrap: the toggle and its four
// surfaces. The layout itself is internal/editor/softwrap.go; nothing here
// measures a row.
//
// The surfaces, and why each earns its place:
//
//   - The FILE TREE's right-click row is the primary door. Wrap is a
//     question about a FILE ("this README reads badly at one row per
//     paragraph"), and the tree is where a file is pointed at before it is
//     even open — so the row opens the file already wrapped rather than
//     making the user open it, find the text, and then ask.
//   - The EDITOR's right-click row is the same verb for the file in front
//     of you, which is where the long line was noticed.
//   - The ≡ View row is the path that survives a terminal which swallows
//     right-click (the house rule: every file action also lives in ≡), and
//     it gives the command palette the verb for free.
//   - The status bar's " · wrap" segment is not a door so much as a
//     disclosure: a wrapped view changes what Up and Down do, and a mode
//     that changes the keys has to be visible. Clicking it unwraps.
//
// House rules:
//
//   - PER TAB, NOT A PREFERENCE, and remembered with the tab. The markdown
//     preview's argument: the same repository holds files that want it and
//     files that don't, so a global switch would be wrong for half of them
//     every time. The session DOES carry it (session.TabState.Wrap),
//     because unlike a preview — a glance you take — wrap is how you have
//     chosen to keep reading a file, and losing it on every restart would
//     be a chore the user repeats forever.
//   - ONE ROW, LABELLED BY THE OUTCOME. Each menu carries exactly one of
//     "Soft Wrap" / "Stop Soft Wrap", named by the state the click
//     PRODUCES and computed from the same tab the action writes — the
//     Preview / Stop Preview pair, spelled the same way so the two view
//     verbs read as one vocabulary side by side.
//   - NO LEADER KEY. The flat table is out of mnemonic letters, and the ≡
//     row already reaches the palette.

package app

import (
	"github.com/rohanthewiz/ced/internal/editor"
	"github.com/rohanthewiz/ced/internal/filetree"
)

// softWrapOnFlash is what turning wrap on says. It names the one behaviour
// that changed under the user's fingers — Up/Down now step by screen row —
// because that is the only part of the mode they could otherwise mistake
// for a bug.
const softWrapOnFlash = "Soft wrap on — ↑/↓ move by screen row"

// softWrapTab returns the active tab when it can be wrapped (a text tab),
// else nil. The single predicate the ≡ row, the editor menu and the verb
// ask, so "can this be wrapped" can never mean two things.
func (a *App) softWrapTab() *editor.Tab {
	t := a.activeTabPtr()
	if t == nil || t.IsImage() || t.Buffer == nil {
		return nil
	}
	return t
}

// hasSoftWrapTarget gates the ≡ row: dimmed with no text tab in front,
// where there is nothing to say beyond "there is no text here".
func (a *App) hasSoftWrapTarget() bool {
	return a.softWrapTab() != nil
}

// softWrapToggleLabel is the ≡ row's label: the state the row switches TO,
// in the menu's sentence case.
func (a *App) softWrapToggleLabel() string {
	if t := a.softWrapTab(); t != nil && t.IsSoftWrap() {
		return "Stop soft wrap"
	}
	return "Soft wrap"
}

// softWrapContextLabel is the context menus' spelling of the same toggle,
// title-cased to sit beside "Preview" / "Stop Preview".
func softWrapContextLabel(wrapped bool) string {
	if wrapped {
		return "Stop Soft Wrap"
	}
	return "Soft Wrap"
}

// menuToggleSoftWrap is the ≡ row (and so the palette entry).
func (a *App) menuToggleSoftWrap() {
	a.closeMenu()
	a.toggleSoftWrap()
}

// toggleSoftWrap flips the active tab's wrap. The single write path for
// the file in front of you — the ≡ row, the editor's right-click row and
// the status segment all land here, so none of them can drift.
func (a *App) toggleSoftWrap() {
	t := a.softWrapTab()
	if t == nil {
		a.flash("No text file open")
		return
	}
	a.setSoftWrap(t, !t.IsSoftWrap())
}

// setSoftWrap applies a wrap state to t and says so. Kept apart from the
// toggle because the tree row FORCES a state (its label already promised
// one) rather than flipping whatever the tab happens to hold.
func (a *App) setSoftWrap(t *editor.Tab, on bool) {
	t.SetSoftWrap(on)
	if on {
		a.flash(softWrapOnFlash)
		return
	}
	a.flash("Soft wrap off")
}

// wrappingPath reports whether path is open in a tab that is currently
// wrapped. The tree row asks about the CLICKED file, which is usually not
// the active one — the previewingPath argument.
func (a *App) wrappingPath(path string) bool {
	t := a.tabForPath(absolutePathFor(path))
	return t != nil && t.IsSoftWrap()
}

// ctxSoftWrapOffered reports whether the tree offers the row on n: files
// only (a folder has no lines), and not images, which open as pictures.
func ctxSoftWrapOffered(n *filetree.Node) bool {
	return !n.IsDir && !editor.IsImagePath(n.Path)
}

// ctxToggleSoftWrap is the tree's row. It opens the clicked file — a wrap
// asked for from the tree is a request to READ it wrapped — and then sets
// the state the label named, computed from the clicked file's own tab.
//
// The path is re-checked against the tab that ended up active because
// openFile can refuse (too big, binary, unreadable) and flashes its own
// reason; without the check a refusal would silently wrap whatever file was
// in front of the user instead. The ctxPreviewMarkdown rule.
func ctxToggleSoftWrap(a *App, n *filetree.Node) {
	path := absolutePathFor(n.Path)
	want := !a.wrappingPath(path)
	a.openFile(path)
	t := a.softWrapTab()
	if t == nil || t.Path != path {
		return
	}
	a.setSoftWrap(t, want)
}
