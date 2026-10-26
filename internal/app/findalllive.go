// =============================================================================
// File: internal/app/findalllive.go
// Author: Rohan Allison <rohanthewiz@gmail.com>
// Created: 2026-10-26
// Copyright: 2026 Rohan Allison. All rights reserved.
// =============================================================================

// findalllive.go makes the "Find all in file" prompt LIVE: the search
// runs from the moment the prompt opens (on its seed) and again on every
// edit, so the editor behind it tints every occurrence, the caret previews
// the hit the list would open on, and the row under the field counts them
// ("3 of 12 · enter lists them"). Enter still opens the full list; Esc
// puts the view back exactly as it was; a click on a line of code
// outside the box closes the prompt and puts the caret on that line.
//
//	  41 │ x := needle(a)                      ← code behind: tinted, the
//	  42 │ return needle                         current hit previewed
//	┌ Find all in file ─────────────── esc ┐
//	│ lists every match · alt+c …          │
//	│ [ needle                       × ▾ ] │   ← prompt rests on the
//	│ 3 of 12 · enter lists them           │     editor's bottom rows;
//	│ [ Cancel ]  [  OK  ]   [case: off] … │     status row under the field
//	└──────────────────────────────────────┘
//
// Why the prompt rather than skipping straight to the list: the list is a
// worklist over ONE answer (filter, dismissals, replace and the stale
// check all assume the query is fixed), so re-running it per keystroke
// would reset everything it is for. The prompt is where the question is
// still being asked, which is exactly where an answer per keystroke
// belongs — the find bar's grammar, ahead of the list's.
//
// In-file only. Project / folder search walks every file in the finder
// index; per keystroke that is a cost the user didn't sign up for, and
// its prompt stays inert.
//
// The contract the live part keeps with the list it leads to:
//
//   - Same question. The query is trimmed as submit trims it and matched
//     under a.findOptions() (the chips flip the App's toggles, and a flip
//     re-asks through promptModal.edited), so the count is the list's
//     length and the previewed hit is the row it opens on.
//   - Same starting row. The preview picks FirstMatchAtOrAfter the ORIGIN
//     cursor — never the previewed one, or each keystroke would walk the
//     caret further down the file — which is showFindAll's own rule.
//   - Nothing borrowed survives the prompt. Every exit restores the
//     tab's find state, and every exit but one the view too: Esc /
//     Cancel / a click outside on no line of code via onCancel, Enter in
//     the submit callback BEFORE showFindAll, so the list captures the
//     true origin (its ↺ revert loc and Esc go back to where the user
//     was, not to the prompt's last preview) and the find state the tab
//     really had. The one exception is a click ON a line of code
//     (clickAway): that line is where the user chose to be, so the view
//     they clicked in is kept and only the caret moves.
//   - A prompt opened over the find bar inherits the BAR's origin
//     (openFindAll), so its Esc goes back to where the user was before
//     the search started, not to the bar's last hit.
//
// The prompt rests on the BOTTOM of the editor (promptModal.bottom), where
// the find bar sits: a centered box covers the middle of the window, which
// is precisely where CenterOnCursor lands an off-screen hit, and a top box
// would hide hits near the top of the file that no scroll can lift (line 0
// is the ceiling). A hit still under the box counts as off-screen
// (hiddenByPrompt) and is centered above it.

package app

import (
	"fmt"

	"github.com/rohanthewiz/ced/internal/editor"
)

// findAllLive is one live prompt's borrowed state: the view and find
// state it found the tab in (put back on every exit) and the answer it
// is showing now.
type findAllLive struct {
	// tab is the tab searched, held by pointer and re-checked with
	// tabIndexOf before any write — nothing should close a tab while a
	// prompt owns the keyboard, but a nil-safe check keeps that from
	// being load-bearing.
	tab *editor.Tab

	origin        editor.Position
	originAnchor  editor.Position
	originScrollY int
	originScrollX int

	priorQuery   string
	priorMatches []editor.Match
	priorIndex   int
	priorOpts    editor.FindOptions

	// moved records that a preview moved the caret, so restore has a view
	// to put back (and doesn't fight a reload that clamped it otherwise).
	moved bool
	// note is the status row's text; "" draws nothing.
	note string
}

// newFindAllLive captures the active tab's view and find state. Called
// AFTER the prompt opens: openModal's closeAllModals closes the find bar,
// which clears the tab's find state, and the list opened on Enter would
// see the cleared one too — capturing earlier would "restore" a tint the
// plain prompt never left behind.
func newFindAllLive(a *App) *findAllLive {
	l := &findAllLive{}
	tab := a.activeTabPtr()
	if tab == nil || tab.Buffer == nil || tab.IsImage() {
		return l
	}
	l.tab = tab
	l.origin, l.originAnchor = tab.Cursor, tab.Anchor
	l.originScrollY, l.originScrollX = tab.ScrollY, tab.ScrollX
	l.priorQuery, l.priorMatches = tab.FindQuery, tab.FindMatches
	l.priorIndex, l.priorOpts = tab.FindIndex, tab.FindOpts
	return l
}

// liveTab returns the searched tab while it is still open, else nil.
func (l *findAllLive) liveTab(a *App) *editor.Tab {
	if l.tab == nil || a.tabIndexOf(l.tab) < 0 {
		return nil
	}
	return l.tab
}

// update re-asks the question for the prompt's current value: the
// promptModal.onEdit hook, also run once on the seed at open.
//
// Every non-answer — an empty field, a pattern that doesn't compile, no
// hits — puts the view back at the origin and drops the tint: a caret
// left on the previous query's hit would read as an answer to this one.
func (l *findAllLive) update(a *App, value string) {
	tab := l.liveTab(a)
	if tab == nil {
		return
	}
	query := trimSpace(value) // what submit will hand showFindAll
	opts := a.findOptions()
	if query == "" {
		l.settle(a, tab, "")
		return
	}
	if err := editor.FindQueryError(query, opts); err != nil {
		l.settle(a, tab, fmt.Sprintf("bad regex: %v", err))
		return
	}
	matches := editor.FindAllOpts(tab.Buffer, query, opts)
	if len(matches) == 0 {
		l.settle(a, tab, "no matches"+findOptionsNote(opts))
		return
	}
	// The list's starting row (showFindAll): the first hit at or after
	// where the user WAS. FirstMatchAtOrAfter wraps to 0 past the last.
	idx := max(editor.FirstMatchAtOrAfter(matches, l.origin), 0)
	// Installed the way showFindAll installs them — options first, so the
	// tint and idx index the same match list.
	tab.SetFindOptions(opts)
	tab.SetFindQuery(query)
	tab.FindIndex = idx
	l.preview(a, tab, matches[idx])
	l.note = fmt.Sprintf("%d of %d · enter lists them", idx+1, len(matches))
}

// settle is update's "no answer" exit: view back to the origin, tint off,
// note set (possibly empty).
func (l *findAllLive) settle(a *App, tab *editor.Tab, note string) {
	l.restoreView(tab)
	tab.ClearFind()
	l.note = note
}

// preview moves the caret onto hit with the list's own reveal policy
// (findAllModal.preview): a hit already in view is left where it is, an
// off-screen one is centered — and "in view" excludes the rows the prompt
// itself covers, since a hit painted under the box is not one the user
// can see.
func (l *findAllLive) preview(a *App, tab *editor.Tab, hit editor.Match) {
	tab.MoveCursorTo(editor.MatchPosition(hit), false)
	tab.MarkJump()
	_, _, ew, eh := a.editorRect()
	if !tab.CursorLineVisible(eh) || l.hiddenByPrompt(a, tab, ew, eh) {
		tab.CenterOnCursor(ew, eh)
	}
	l.moved = true
}

// hiddenByPrompt reports whether the caret's screen row falls under the
// prompt box. Only the vertical overlap matters: the box spans most of an
// editor's width, and a hit beside it on the same row is still hard to
// read. A caret scrolled out HORIZONTALLY reports not hidden — Render's
// EnsureVisible brings the column in, and the row is what this decides.
func (l *findAllLive) hiddenByPrompt(a *App, tab *editor.Tab, ew, eh int) bool {
	pm, ok := a.modal.(*promptModal)
	if !ok {
		return false
	}
	_, ey, _, _ := a.editorRect()
	_, py, _, ph := pm.rect(a)
	_, dy, vis := tab.PosScreenCell(tab.Cursor, ew, eh)
	if !vis {
		return false
	}
	row := ey + dy
	return row >= py && row < py+ph
}

// restoreView puts the caret and scroll back where the prompt found them,
// if a preview moved them. RestoreView (not MoveCursorTo) so the restored
// scroll is not second-guessed by the next Render — findAllModal.abort's
// reasoning.
func (l *findAllLive) restoreView(tab *editor.Tab) {
	if !l.moved {
		return
	}
	tab.RestoreView(l.origin, l.originAnchor, l.originScrollY, l.originScrollX)
	l.moved = false
}

// restore undoes everything the live prompt borrowed: the view, and the
// tab's find state. The onCancel hook, and the first thing the submit
// callback does.
func (l *findAllLive) restore(a *App) {
	l.note = ""
	tab := l.liveTab(a)
	if tab == nil {
		return
	}
	l.restoreView(tab)
	l.restoreFind(tab)
}

// restoreFind hands back the tab's find state verbatim (plain fields, as
// findAllModal.restoreFind does — the setter would re-scan a list
// produced under priorOpts).
func (l *findAllLive) restoreFind(tab *editor.Tab) {
	tab.FindQuery = l.priorQuery
	tab.FindMatches = l.priorMatches
	tab.FindIndex = l.priorIndex
	tab.FindOpts = l.priorOpts
}

// clickAway is the promptModal.onClickAway hook: a press outside the box
// that lands on a line of code closes the prompt and puts the caret on
// that line. The tint goes (the question was abandoned), but the VIEW
// the user clicked in stays — restoring the origin's scroll first would
// slide the line they aimed at out from under the pointer. Any other
// outside press (tree, tab bar, status bar, past the last line) answers
// false and gets the ordinary cancel, view and all.
//
// The prompt floats over the editor rather than taking rows out of it,
// so the clicked cell means the same line before and after the close;
// it is resolved first anyway, while the screen is the one aimed at.
func (l *findAllLive) clickAway(a *App, x, y int) bool {
	tab := l.liveTab(a)
	if tab == nil || a.activeTabPtr() != tab {
		return false
	}
	pos, ok := a.editorLineAt(x, y)
	if !ok {
		return false
	}
	a.closeModal()
	l.note = ""
	l.moved = false
	l.restoreFind(tab)
	tab.MoveCursorTo(pos, false)
	return true
}

// inherit swaps the captured origin for an earlier one — the find bar's,
// when the prompt opens over it (openFindAll) — so Esc goes back to where
// the user was before the search began. moved is set because the caret
// may already sit away from that origin (on the bar's hit) with no
// preview of this prompt's own to account for it.
func (l *findAllLive) inherit(from viewMark) {
	if l.tab == nil || from.tab != l.tab {
		return
	}
	l.origin, l.originAnchor = from.cursor, from.anchor
	l.originScrollY, l.originScrollX = from.scrollY, from.scrollX
	l.moved = true
}

// status is the prompt's status-row text (promptModal.status).
func (l *findAllLive) status(*App) string { return l.note }
