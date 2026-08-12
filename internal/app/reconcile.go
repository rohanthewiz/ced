// =============================================================================
// File: internal/app/reconcile.go
// Author: Rohan Allison <rohanthewiz@gmail.com>
// Created: 2026-08-12
// Copyright: 2026 Rohan Allison. All rights reserved.
// =============================================================================

// reconcile.go is the editor's answer to "never clobber, never be
// clobbered": everything that happens when the file on disk and the
// buffer in front of you stop agreeing.
//
// Two interception points, and the pair is the whole design — one for
// each direction the damage can flow:
//
//	A. the watcher tick  — disk changed under an open tab   (they clobber us)
//	B. the save guard    — disk is newer than what we loaded (we clobber them)
//
// A: reconcileOpenTabsWithDisk runs on the background refresh tick and
// applies this matrix:
//
//	 disk      buffer   behaviour
//	 -------   ------   -------------------------------------------------
//	 newer     clean    reload — but UNDOABLY, and say so
//	 newer     dirty    record a conflict; ⚠ marker; raise the prompt
//	 deleted   any      keep the buffer, ⊘ marker; a save recreates it
//
// B: saveGuard stats before every write. If the file grew a newer mtime
// than the one the tab loaded, the write is ABORTED and the same prompt
// is raised, with the blocked save armed as `resume` so answering "Keep
// mine" completes the gesture the user actually asked for. Before this
// existed ced warned about the conflict and then wrote anyway, which is
// the one behaviour a save guard may not have.
//
// The conflict record is the state that ties the two together. While it
// stands, the tab wears ⚠, auto-save is suspended for it (a background
// save must never be the thing that resolves a question the user hasn't
// been asked yet), and every save path keeps refusing. It clears only
// through an explicit answer:
//
//	Compare…     look first — buffer vs. the current disk copy
//	Keep mine    adopt the disk mtime; the next save knowingly overwrites
//	Take disk    ReloadUndoable — reversible with one Undo
//	Decide later marker stays, saves stay guarded
//
// Deferral rule: the prompt is modal and modals are single-slot, so a
// conflict on a background tab is recorded silently and raised by
// conflictAfterEvent the moment that tab is frontmost and the slot is
// free. A conflict never steals the screen from the file you're looking
// at.
//
// Tier 1 (Phase 5) hangs one more consumer off this file: an unresolved
// conflict is exactly the `blocked` hook state — the editor asking for
// you by badge, toast, and phone push. The record above is the signal it
// will read; nothing here needs to change for it.
package app

import (
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/gdamore/tcell/v2"

	"github.com/rohanthewiz/ced/internal/editor"
)

// Tab-strip markers. Both are single-width on purpose (the runeLen
// house rule — a double-width glyph would shove the filename out of the
// slot the layout measured), and both occupy the same cell as the dirty
// dot, because they are strictly worse news than "unsaved": a tab that
// is in conflict is also dirty, and saying so twice costs a column the
// strip does not have.
const (
	conflictMarker = '⚠' // disk and buffer disagree, awaiting a decision.
	diskGoneMarker = '⊘' // the backing file was deleted out from under us.
)

// diskConflict is one unresolved "the file changed under you". One per
// tab at most: further disk churn updates the record rather than
// stacking, since the user is answering about the file's current state,
// not about each write that happened while they thought.
type diskConflict struct {
	tab  *editor.Tab
	path string // captured at detection so the record survives a rename.

	// mtime is the disk timestamp that triggered the conflict. Kept so a
	// LATER external write can be detected as new news (see noteConflict)
	// and re-raise a prompt the user had deferred.
	mtime time.Time

	// resume is the save this conflict blocked, re-run verbatim if the
	// user answers "Keep mine". nil when the conflict came from the
	// watcher tick — there was no save in flight to finish.
	resume func(*App)

	// prompted records that the modal has already been shown for the
	// current state of the file, so a deferred decision stays deferred
	// instead of re-popping on every event dispatch.
	prompted bool
}

// -----------------------------------------------------------------------------
// A. The watcher tick
// -----------------------------------------------------------------------------

// reconcileOpenTabsWithDisk runs once per background tick. For every
// open tab with a real path it stats the file, compares the on-disk
// mtime to what the tab last knew, and applies the matrix in this
// file's header comment.
//
// Untitled tabs (Path == "") are skipped because there's no disk file to
// reconcile against.
func (a *App) reconcileOpenTabsWithDisk() {
	for _, tab := range a.tabs {
		if tab.Path == "" {
			continue
		}
		info, err := os.Stat(tab.Path)
		if os.IsNotExist(err) {
			if !tab.DiskGone {
				tab.DiskGone = true
				// Dirty because the buffer is now the only copy that
				// exists: marking it clean would let the editor quit
				// without a murmur and take the file's last remains
				// with it. The ⊘ marker says which kind of dirty.
				tab.Dirty = true
				a.flash(fmt.Sprintf("%s deleted on disk — save to recreate it",
					filepath.Base(tab.Path)))
			}
			continue
		}
		if err != nil {
			// Permission denied or some other transient stat error — leave
			// the tab as-is rather than spamming the user with a flash.
			continue
		}
		if tab.DiskGone {
			// File reappeared. Force the mtime check below to fire so we
			// either reload or warn about a dirty conflict.
			tab.DiskGone = false
			tab.Mtime = time.Time{}
		}
		if !info.ModTime().After(tab.Mtime) {
			continue // unchanged on disk.
		}
		if tab.Dirty {
			// Both sides have edits. Record it and let the prompt come to
			// the user (now, or when this tab is next frontmost).
			//
			// Deliberately NOT adopting info.ModTime() into tab.Mtime the
			// way the pre-conflict version did to silence repeat flashes:
			// tab.Mtime is what the save guard measures against, so
			// adopting it here would quietly mean "the user accepted the
			// disk version" and re-open the clobber the guard exists to
			// prevent. The conflict record is what suppresses the repeats
			// instead.
			a.noteConflict(tab, info.ModTime(), nil)
			continue
		}
		// Clean buffer: the disk copy is strictly newer information, so
		// take it — but undoably, because "the editor changed my text
		// while I was reading it" needs a way back even when the text was
		// saved. A stale conflict record (deleted-then-restored, say) is
		// answered by this reload.
		if err := tab.ReloadUndoable(); err != nil {
			a.flash(fmt.Sprintf("%s reload failed: %v", filepath.Base(tab.Path), err))
			continue
		}
		a.clearConflict(tab)
		a.flash(fmt.Sprintf("%s reloaded from disk ↺ — undo to get yours back",
			filepath.Base(tab.Path)))
	}
}

// -----------------------------------------------------------------------------
// Conflict bookkeeping
// -----------------------------------------------------------------------------

// conflictFor returns the unresolved conflict record for tab, or nil.
func (a *App) conflictFor(tab *editor.Tab) *diskConflict {
	if tab == nil || a.conflicts == nil {
		return nil
	}
	return a.conflicts[tab]
}

// noteConflict records (or refreshes) the conflict for tab and returns
// the live record.
//
// Refresh semantics matter more than they look: if the file changed
// AGAIN since the record was made, the user's earlier "decide later" was
// a decision about older news, so `prompted` is cleared and the prompt
// gets to come back. If nothing new happened, the record is left alone —
// that is what stops the reconcile tick from re-flashing and re-popping
// once per tick.
func (a *App) noteConflict(tab *editor.Tab, diskMtime time.Time, resume func(*App)) *diskConflict {
	if a.conflicts == nil {
		a.conflicts = make(map[*editor.Tab]*diskConflict)
	}
	c := a.conflicts[tab]
	if c == nil {
		c = &diskConflict{tab: tab, path: tab.Path, mtime: diskMtime}
		a.conflicts[tab] = c
		a.flash(fmt.Sprintf("%s changed on disk — ⚠ your edits are not saved",
			filepath.Base(tab.Path)))
	} else if diskMtime.After(c.mtime) {
		c.mtime = diskMtime
		c.prompted = false // new news deserves a new question.
	}
	// A save that arrives while a conflict stands re-arms the resume: the
	// most recent save gesture is the one the user is waiting on.
	if resume != nil {
		c.resume = resume
	}
	return c
}

// clearConflict resolves the conflict for tab, if any. Every resolution
// path funnels through here so "the marker is gone" and "the guard will
// let a write through" can never disagree.
func (a *App) clearConflict(tab *editor.Tab) {
	if a.conflicts == nil {
		return
	}
	delete(a.conflicts, tab)
}

// reconcileForgetTab drops a closed tab's conflict record. Called from
// closeTab beside the other per-tab teardown: a record keyed on a tab
// nobody can reach would keep auto-save suspended for a buffer that no
// longer exists, and would leak the tab.
func (a *App) reconcileForgetTab(tab *editor.Tab) {
	a.clearConflict(tab)
}

// -----------------------------------------------------------------------------
// Raising the prompt
// -----------------------------------------------------------------------------

// conflictAfterEvent is the deferred-prompt pump, run in the
// after-event slot beside autoSaveAfterEvent. It raises the frontmost
// tab's unanswered conflict as soon as the modal slot is free.
//
// This is the whole "deferred to next activation" rule: detection can
// happen on a background tick for a tab three positions away, and the
// prompt still shows up exactly when the user turns to that file, with
// nothing having interrupted whatever they were doing in the meantime.
func (a *App) conflictAfterEvent() {
	if len(a.conflicts) == 0 || a.modal != nil || a.menuOpen {
		return
	}
	c := a.conflictFor(a.activeTabPtr())
	if c == nil || c.prompted {
		return
	}
	a.openConflictModal(c)
}

// openConflictModal raises the four-choice prompt for c and marks the
// record prompted, so dismissing it (Esc, click-away, "Decide later")
// leaves the user alone until the file changes again.
func (a *App) openConflictModal(c *diskConflict) {
	c.prompted = true
	a.openModal(&conflictModal{c: c})
}

// focusConflictTab brings c's tab to the front. The compare panel and
// every other resolution act on the ACTIVE tab, so a prompt raised for a
// background tab (a save-all during quit, say) has to move the user
// there first — resolving a conflict you can't see is how the wrong
// button gets pressed.
func (a *App) focusConflictTab(c *diskConflict) {
	for i, t := range a.tabs {
		if t == c.tab {
			a.switchToTab(i)
			return
		}
	}
}

// -----------------------------------------------------------------------------
// Resolutions
// -----------------------------------------------------------------------------

// conflictCompare opens the compare panel with the buffer on one side
// and the current disk copy on the other. It resolves nothing: the
// record and its marker stand, so the other three choices are still
// waiting when the user has finished reading — reachable from the tab's
// ⚠ marker or the next save.
func (a *App) conflictCompare(c *diskConflict) {
	a.focusConflictTab(c)
	// compareWithFile against the tab's own path is precisely
	// "buffer vs. saved copy" — it re-reads from disk rather than reusing
	// the open buffer for exactly this case (compare.go).
	a.compareWithFile(c.tab.Path)
}

// conflictKeepMine adopts the disk's mtime without touching the buffer:
// the tab now claims to be based on the file as it stands, so the guard
// stops firing and the next write knowingly overwrites. The blocked save
// (if this conflict interrupted one) is then re-run, because "keep mine"
// in answer to a save means "yes, write it".
//
// The mtime is re-stat'ed rather than taken from the record: the user may
// have spent a minute reading the compare panel, and adopting a
// timestamp older than the file's current one would leave the guard
// armed and the save bouncing a second time.
func (a *App) conflictKeepMine(c *diskConflict) {
	if info, err := os.Stat(c.tab.Path); err == nil {
		c.tab.Mtime = info.ModTime()
	} else {
		c.tab.Mtime = c.mtime
	}
	resume := c.resume
	a.clearConflict(c.tab)
	if resume != nil {
		resume(a)
		return
	}
	a.flash(fmt.Sprintf("Keeping your %s — the next save overwrites the disk copy",
		filepath.Base(c.tab.Path)))
}

// conflictTakeDisk reloads the file, discarding the buffer's edits —
// reversibly. ReloadUndoable leaves the pre-reload text one Undo away,
// which is what makes this button safe to press while still curious.
//
// Any blocked save is dropped rather than resumed: the user just chose
// the disk's version, and writing it straight back would be a no-op that
// only muddies mtimes.
func (a *App) conflictTakeDisk(c *diskConflict) {
	if err := c.tab.ReloadUndoable(); err != nil {
		a.flash(fmt.Sprintf("Reload failed: %v", err))
		return
	}
	a.clearConflict(c.tab)
	a.flash(fmt.Sprintf("Took the disk copy of %s — undo restores yours",
		filepath.Base(c.tab.Path)))
}

// conflictLater leaves everything as it is: buffer untouched, marker in
// the strip, saves still guarded. The blocked save is dropped — the
// user declined to answer the question that save asked.
func (a *App) conflictLater(c *diskConflict) {
	c.resume = nil
	a.flash(fmt.Sprintf("%s still conflicts — ⚠ in the tab strip; saves stay blocked",
		filepath.Base(c.tab.Path)))
}

// -----------------------------------------------------------------------------
// B. The save guard
// -----------------------------------------------------------------------------

// saveGuard is the gate every explicit write goes through. It reports
// whether it is safe to write tab now; when it isn't, it has already
// recorded the conflict and raised the prompt, and resume is what runs
// if the user answers "Keep mine".
//
// Stat failures deliberately pass: a missing file means the save is a
// RECREATE (the ⊘ row of the matrix, and the reason a deleted file's tab
// stays dirty), and any other stat error is better reported by the write
// itself than guessed at here.
func (a *App) saveGuard(tab *editor.Tab, resume func(*App)) bool {
	if tab == nil || tab.Path == "" {
		return true
	}
	diskMtime, newer := diskNewerThanTab(tab)
	if !newer {
		return true
	}
	c := a.noteConflict(tab, diskMtime, resume)
	// Unlike the deferred watcher path this always raises, even on a
	// record the user already deferred: they just asked to save, and the
	// save cannot proceed until they answer.
	a.focusConflictTab(c)
	a.openConflictModal(c)
	a.flash(fmt.Sprintf("Save blocked — %s changed on disk", filepath.Base(tab.Path)))
	return false
}

// autoSaveGuard is the same gate for background writes, minus the
// voice. An auto-save fires while the user is mid-thought, so it may
// never open a modal (autosave.go's standing rule); on conflict it
// records the state, which suspends auto-save for that tab, and the
// question surfaces through conflictAfterEvent the next time the user
// looks at the file.
//
// Stat errors return false here rather than true: when we cannot see
// the file's state, a background write is exactly the wrong thing to
// guess at, and an explicit Save remains available to say otherwise.
func (a *App) autoSaveGuard(tab *editor.Tab) bool {
	if tab == nil || tab.Path == "" {
		return false
	}
	if a.conflictFor(tab) != nil {
		return false // suspended until the user answers.
	}
	if _, err := os.Stat(tab.Path); err != nil {
		return false
	}
	diskMtime, newer := diskNewerThanTab(tab)
	if !newer {
		return true
	}
	a.noteConflict(tab, diskMtime, nil)
	return false
}

// diskNewerThanTab reports the file's mtime and whether it is newer than
// the copy the tab loaded — the single measurement both guards and the
// watcher agree on. A zero tab mtime (never read or written) is treated
// as "no conflict": there is no prior state to have been clobbered.
func diskNewerThanTab(tab *editor.Tab) (time.Time, bool) {
	info, err := os.Stat(tab.Path)
	if err != nil {
		return time.Time{}, false
	}
	if tab.Mtime.IsZero() || !info.ModTime().After(tab.Mtime) {
		return info.ModTime(), false
	}
	return info.ModTime(), true
}

// -----------------------------------------------------------------------------
// The tab-strip marker
// -----------------------------------------------------------------------------

// tabMarker is the glyph for the strip's leading slot on this tab, in
// descending order of "how much does the user need to know": the file is
// gone, the file disagrees with the buffer, the buffer is unsaved. ok is
// false for a clean tab, whose slot stays blank.
func (a *App) tabMarker(tab *editor.Tab) (rune, tcell.Color, bool) {
	switch {
	case tab.DiskGone:
		return diskGoneMarker, a.theme.Error, true
	case a.conflictFor(tab) != nil:
		return conflictMarker, a.theme.DiagWarning, true
	case tab.Dirty:
		return '●', a.theme.Modified, true
	}
	return 0, a.theme.Modified, false
}

// -----------------------------------------------------------------------------
// The prompt
// -----------------------------------------------------------------------------

// Geometry of the conflict prompt. Wider than the dirty-close modal
// because it carries four buttons plus a sentence of explanation, and
// one row taller for the hover hint under them.
const (
	conflictModalWidth  = 72
	conflictModalHeight = 11
)

// conflictModal is the four-choice disk-conflict dialog. hover indexes
// the button row: 0 = Compare (the default — it is the only choice that
// changes nothing, and looking before choosing is the point), 1 = Keep
// mine, 2 = Take disk, 3 = Later.
type conflictModal struct {
	c     *diskConflict
	hover int
}

// conflictChoices is the button row: label, the hint shown under it
// while hovered, and the resolution it runs. One table so draw,
// hit-testing, and behaviour cannot drift — the btnRect discipline
// applied to a whole row.
var conflictChoices = []struct {
	label string
	hint  string
	run   func(a *App, c *diskConflict)
}{
	{"[ Compare ]", "See your buffer against the file on disk — decide after",
		(*App).conflictCompare},
	{"[ Keep mine ]", "Your version wins; the next save overwrites the disk copy",
		(*App).conflictKeepMine},
	{"[ Take disk ]", "Reload the file — one Undo brings your version back",
		(*App).conflictTakeDisk},
	{"[ Later ]", "Leave the ⚠ marker; saves stay blocked until you choose",
		(*App).conflictLater},
}

// activate runs the focused choice and closes the modal first, so a
// resolution is free to open something of its own (Compare opens the
// compare panel) without this modal stomping it on the way out.
func (m *conflictModal) activate(a *App) {
	if m.hover < 0 || m.hover >= len(conflictChoices) {
		return
	}
	choice := conflictChoices[m.hover]
	a.closeModal()
	choice.run(a, m.c)
}

// handleKey drives the row from the keyboard: Left/Right and Tab move
// across the choices, Enter activates, Esc means "decide later" — the
// same as the button, because dismissing a question is deferring it, not
// answering it.
func (m *conflictModal) handleKey(a *App, ev *tcell.EventKey) {
	switch ev.Key() {
	case tcell.KeyEsc:
		a.closeModal()
		a.conflictLater(m.c)
	case tcell.KeyEnter:
		m.activate(a)
	case tcell.KeyLeft:
		if m.hover > 0 {
			m.hover--
		}
	case tcell.KeyRight:
		if m.hover < len(conflictChoices)-1 {
			m.hover++
		}
	case tcell.KeyTab:
		m.hover = (m.hover + 1) % len(conflictChoices)
	}
}

// buttons returns the four button rects — the single geometry source
// for draw and hit-testing. Laid out centred: four labels plus three
// four-cell gaps, with the leftover split evenly as margins.
func (m *conflictModal) buttons(a *App) []btnRect {
	mx, my, mw, _ := m.rect(a)
	const gap = 4
	total := 0
	for _, ch := range conflictChoices {
		total += runeLen(ch.label)
	}
	total += gap * (len(conflictChoices) - 1)
	x := mx + (mw-total)/2
	rects := make([]btnRect, 0, len(conflictChoices))
	for _, ch := range conflictChoices {
		w := runeLen(ch.label)
		rects = append(rects, btnRect{x: x, y: my + 7, w: w})
		x += w + gap
	}
	return rects
}

// buttonAt maps a screen cell to a choice index, or -1 on a miss.
func (m *conflictModal) buttonAt(a *App, x, y int) int {
	for i, b := range m.buttons(a) {
		if b.contains(x, y) {
			return i
		}
	}
	return -1
}

// handleMouse hovers on move and activates on click. A click outside
// the modal defers, matching Esc.
func (m *conflictModal) handleMouse(a *App, x, y int, btn tcell.ButtonMask) {
	if idx := m.buttonAt(a, x, y); idx >= 0 {
		m.hover = idx
	}
	if btn&tcell.Button1 == 0 {
		return
	}
	mx, my, mw, mh := m.rect(a)
	if x < mx || x >= mx+mw || y < my || y >= my+mh {
		a.closeModal()
		a.conflictLater(m.c)
		return
	}
	if idx := m.buttonAt(a, x, y); idx >= 0 {
		m.hover = idx
		m.activate(a)
	}
}

// rect centres the prompt in the window.
func (m *conflictModal) rect(a *App) (x, y, w, h int) {
	return a.centeredRect(conflictModalWidth, conflictModalHeight)
}

// draw renders the prompt.
//
// Rows (relY):
//
//	0   top border
//	1   title — "File changed on disk   esc"
//	2   divider
//	3   blank
//	4   "<name> changed on disk while you were editing it."
//	5   "Your version has unsaved edits."
//	6   blank
//	7   buttons  [ Compare ]  [ Keep mine ]  [ Take disk ]  [ Later ]
//	8   blank
//	9   hint for the hovered button (muted)
//	10  bottom border
func (m *conflictModal) draw(a *App) {
	mx, my, mw, mh := m.rect(a)
	c := a.chrome()
	c.drawFrame(a.screen, mx, my, mw, mh, "File changed on disk")

	name := filepath.Base(m.c.path)
	lines := []string{
		name + " changed on disk while you were editing it.",
		"Your version has unsaved edits.",
	}
	for i, ln := range lines {
		if runeLen(ln) > mw-4 {
			ln = string([]rune(ln)[:mw-4])
		}
		drawAt(a.screen, mx+(mw-runeLen(ln))/2, my+4+i, ln, c.body)
	}

	// Buttons. Compare is neutral, Take disk is the destructive-looking
	// one (it replaces your text — reversibly, which is what the hint
	// says), Keep mine carries the accent as the "proceed with my work"
	// answer.
	rects := m.buttons(a)
	for i, ch := range conflictChoices {
		fg := a.theme.Text
		switch i {
		case 1:
			fg = a.theme.Accent
		case 2:
			fg = a.theme.Error
		}
		drawButton(a.screen, rects[i].x, rects[i].y, ch.label, c.bg, fg, m.hover == i)
	}

	// Hover hint — the discoverability half of a four-way choice, where
	// the labels alone can't say what each one costs.
	if m.hover >= 0 && m.hover < len(conflictChoices) {
		hint := conflictChoices[m.hover].hint
		if runeLen(hint) > mw-4 {
			hint = string([]rune(hint)[:mw-4])
		}
		st := tcell.StyleDefault.Background(c.bg).Foreground(a.theme.Muted)
		drawAt(a.screen, mx+(mw-runeLen(hint))/2, my+9, hint, st)
	}

	a.screen.HideCursor()
}

// Compile-time check that conflictModal really satisfies the modal contract.
var _ modal = (*conflictModal)(nil)
