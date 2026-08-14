// =============================================================================
// File: internal/app/autosave.go
// Author: Rohan Allison <rohanthewiz@gmail.com>
// Created: 2026-07-09
// Copyright: 2026 Rohan Allison. All rights reserved.
// =============================================================================

// Auto-save: dirty buffers are written to disk after the user pauses
// typing for autoSaveDelay. The mechanism mirrors the LSP didChange
// debounce — the only piece that leaves the main loop is a
// time.AfterFunc that posts an autoSaveEvent back onto the tcell
// queue, so all tab state is still mutated from event dispatch only:
//
//	edit → EditRev bump → autoSaveAfterEvent re-arms timer
//	                       └─ idle autoSaveInterval ─► autoSaveEvent → save dirty tabs
//
// The timer is not the only trigger. Leaving — the terminal window losing
// focus, or switching to another tab — flushes what is pending straight
// away (autoSaveOnFocusChange, autoSaveDepartingTab), which is what lets
// the idle window be long enough to stay out of the user's way.
//
// Design decisions worth knowing before touching this:
//
//   - Auto-saves are SILENT. No "Saved main.go" flash — on an idle
//     trigger the status bar would flicker constantly. The dirty dot
//     disappearing from the tab is the feedback.
//   - Auto-saves still run format-on-save, but in quiet mode: the
//     builtin goimports/gofmt pass keeps working (that's the point
//     of having both features), while trust prompts, install offers,
//     and error flashes are suppressed — a modal popping open or an
//     error flashing because the code is mid-thought would make the
//     feature feel hostile.
//   - A tab whose file changed on disk after we loaded it is skipped:
//     blindly writing would clobber the external edit before the
//     reconcile tick had a chance to warn. Explicit Save remains the
//     "I know, overwrite it" path. Same for DiskGone tabs — auto-save
//     silently resurrecting a file someone just deleted is surprising.
//   - The toggle lives in the ≡ menu (house rule: every action is
//     reachable there) and persists to ~/.config/ced/config.json so
//     it survives restarts.
package app

import (
	"os"
	"time"

	"github.com/gdamore/tcell/v2"

	"github.com/rohanthewiz/ced/internal/editor"
	"github.com/rohanthewiz/ced/internal/userconfig"
)

// defaultAutoSaveDelay is how long the user must be idle (no buffer
// mutations anywhere) before dirty tabs are written out, when nothing
// else has been resolved onto the App. Five seconds matches the "I
// finished a thought and looked away" rhythm; the user's own value comes
// from the "autosavedelay" config key, and the canonical default and its
// clamp live in userconfig so the shipped number is stated exactly once.
//
// Named default…, NOT autoSaveDelay: App carries a field by that name,
// and a package const sharing it would have every unqualified mention
// inside a method silently resolve to the const — the field would be
// written and never read.
const defaultAutoSaveDelay = userconfig.DefaultAutoSaveDelay

// autoSaveInterval is the resolved idle window: the user's configured
// value, or the shipped default when nothing has been resolved onto this
// App yet (tests build App as a struct literal, where the zero value
// would arm a timer that fires immediately). Split out from armAutoSave
// so the fallback is testable without waiting on a timer.
func (a *App) autoSaveInterval() time.Duration {
	if a.autoSaveDelay > 0 {
		return a.autoSaveDelay
	}
	return defaultAutoSaveDelay
}

// autoSaveEvent is posted by the debounce timer when the idle window
// elapses. Carries no payload — the handler re-derives which tabs
// need saving from live state, so a stale event (timer fired just as
// the user resumed typing) degrades to a cheap no-op or a re-arm.
type autoSaveEvent struct {
	when time.Time
}

// When satisfies the tcell.Event interface.
func (e *autoSaveEvent) When() time.Time { return e.when }

// autoSaveAfterEvent runs after every event dispatch (same slot as
// lspAfterEvent) and re-arms the debounce timer whenever any buffer
// mutated since the last check. The EditRev sum is the cheapest
// possible "did anything change?" signature: every mutation path
// already bumps a tab's EditRev, so summing them detects edits from
// keys, paste, modals, and reloads without hooking each path.
func (a *App) autoSaveAfterEvent() {
	if !a.autoSaveEnabled {
		return
	}
	sig := 0
	dirty := false
	for _, t := range a.tabs {
		sig += t.EditRev
		if autoSavable(t) {
			dirty = true
		}
	}
	if sig == a.autoSaveSig {
		return
	}
	a.autoSaveSig = sig
	if dirty {
		a.armAutoSave()
	}
}

// armAutoSave (re)starts the idle countdown. Restarting on every
// further edit is what makes it a debounce — the save only fires
// after a genuine pause, never mid-burst.
func (a *App) armAutoSave() {
	if a.autoSaveTimer != nil {
		a.autoSaveTimer.Stop()
	}
	scr := a.screen
	a.autoSaveTimer = time.AfterFunc(a.autoSaveInterval(), func() {
		_ = scr.PostEvent(&autoSaveEvent{when: time.Now()})
	})
}

// stopAutoSave cancels any pending idle countdown. Safe to call with
// no timer armed; used by the menu toggle and Close.
func (a *App) stopAutoSave() {
	if a.autoSaveTimer != nil {
		a.autoSaveTimer.Stop()
		a.autoSaveTimer = nil
	}
}

// handleAutoSave is the debounce firing on the main loop: write every
// tab that is still dirty and safe to write. While any modal or the
// action menu is open we defer instead — a save could invalidate the
// very question a modal is asking (the dirty-close dialog's "save or
// discard?" becomes nonsense if auto-save lands mid-decision).
func (a *App) handleAutoSave() {
	if !a.autoSaveEnabled {
		return
	}
	if a.modal != nil || a.menuOpen {
		a.armAutoSave()
		return
	}
	saved := false
	for i := range a.tabs {
		if a.autoSaveTabIfEligible(i) {
			saved = true
		}
	}
	// One status refresh for the whole batch — refreshGitStatus forks
	// git, so per-tab calls would multiply that cost for no benefit.
	if saved {
		a.refreshGitStatus()
	}
}

// autoSaveTabIfEligible writes the tab at idx if it is dirty, writable
// and unconflicted, and reports whether it actually wrote.
//
// It is the shared body of the idle flush and both focus flushes, so
// there is still exactly ONE answer to "is this tab safe to write in the
// background" — three copies of that predicate pair would be one refactor
// away from disagreeing.
//
// autoSaveGuard is diskChangedSinceLoad's louder successor: the same
// measurement, but a positive answer RECORDS a conflict (reconcile.go)
// instead of merely skipping. The record suspends auto-save for that tab,
// marks it ⚠ in the strip, and queues the prompt for the user's next
// visit to the file — the background save stays silent, but the question
// stops waiting on a reconcile tick to be asked.
func (a *App) autoSaveTabIfEligible(idx int) bool {
	if idx < 0 || idx >= len(a.tabs) {
		return false
	}
	t := a.tabs[idx]
	if !autoSavable(t) || !a.autoSaveGuard(t) {
		return false
	}
	return a.autoSaveTab(idx)
}

// autoSaveOnFocusChange flushes pending auto-saves when the terminal
// window loses focus. Leaving the editor is the clearest "I'm done for
// now" a terminal can report, and acting on it is what lets the idle
// window be long enough to stay out of the user's way.
//
// It deliberately owns no save logic: handleAutoSave already knows every
// rule that applies (a modal or the menu open means defer, autoSaveGuard
// means an unresolved disk conflict, autoSavable means the tab is worth
// and safe to writing), and a second write path would be one refactor
// away from disagreeing with the first about which of those still hold.
//
// Gated on the ≡ toggle, because a focus-out write IS an auto-save and
// "off" means don't write behind my back — which losing focus does not
// make less true.
//
// Regaining focus does nothing at all. The countdown is armed by EDITS,
// not by attention, so there is nothing pending that coming back would
// change. (Reconciling with disk on focus-IN is tempting — it would catch
// "I came back from the shell where I ran git checkout" instantly — but
// it can raise a conflict modal the moment you look at the window, which
// is its own decision to make.)
func (a *App) autoSaveOnFocusChange(focused bool) {
	if focused || !a.autoSaveEnabled {
		return
	}
	// Stop first, then flush: this IS the countdown's payload arriving
	// early. The order matters — handleAutoSave re-arms the timer when a
	// modal makes it defer, and stopping afterwards would cancel that
	// fresh one and strand the work until the next edit.
	a.stopAutoSave()
	a.handleAutoSave()
}

// autoSaveDepartingTab flushes the tab the user is leaving. The
// in-editor twin of losing window focus: a file you can no longer see
// should not still own a countdown. Same guards as every other
// auto-save, including the modal deferral — which matters here
// specifically because focusConflictTab (reconcile.go) calls
// switchToTab on its way to opening the conflict prompt.
func (a *App) autoSaveDepartingTab(idx int) {
	if !a.autoSaveEnabled || a.modal != nil || a.menuOpen {
		return
	}
	if a.autoSaveTabIfEligible(idx) {
		a.refreshGitStatus()
	}
}

// autoSaveTab writes a single tab to disk and runs the quiet
// follow-ups (diff refresh, LSP didSave, quiet format-on-save).
// Deliberately not routed through saveTabAt: that path flashes
// "Saved …" and may open formatter prompts, both wrong for a
// background save. Write errors DO flash — silently failing to save
// is the one thing an auto-save feature can never do, because the
// user has stopped thinking about saving at all.
func (a *App) autoSaveTab(idx int) bool {
	tab := a.tabs[idx]
	if err := tab.Save(); err != nil {
		a.flash("Auto-save failed: " + err.Error())
		return false
	}
	a.requestFileDiff(tab.Path)
	a.lspDidSave(tab)
	a.runFormatOnSave(idx, true)
	return true
}

// autoSavable reports whether a tab is eligible for background
// saving: it has unsaved edits, a real path to write to, and isn't a
// read-only image view or a buffer whose backing file was deleted
// externally (resurrection should be an explicit choice).
func autoSavable(t *editor.Tab) bool {
	return t.Dirty && t.Path != "" && !t.DiskGone && !t.IsImage()
}

// diskChangedSinceLoad reports whether the file on disk is newer than
// the content this tab was loaded from — i.e. someone else wrote it
// while we hold unsaved edits. Auto-save must not win that race
// silently; the reconcile tick will surface the conflict and the
// user can explicitly Save to overwrite. Stat errors report true
// (skip the save) — when we can't see the file's state, guessing
// "it's fine, overwrite" is the wrong default.
//
// The auto-save loop now asks autoSaveGuard (reconcile.go) instead,
// which makes this same call and additionally records the conflict.
// This predicate is kept as the plain, side-effect-free question —
// "is the disk ahead of this tab?" — for callers that want to ask
// without changing anything.
func diskChangedSinceLoad(t *editor.Tab) bool {
	info, err := os.Stat(t.Path)
	if err != nil {
		return true
	}
	return !t.Mtime.IsZero() && info.ModTime().After(t.Mtime)
}

// menuToggleAutoSave flips auto-save on/off from the ≡ menu and
// persists the choice to the user config so it sticks across
// sessions. Turning it ON arms the timer immediately when something
// is already dirty — the user's intent is "start keeping my work
// saved", not "start after my next keystroke".
func (a *App) menuToggleAutoSave() {
	a.closeMenu()
	a.autoSaveEnabled = !a.autoSaveEnabled
	if a.autoSaveEnabled {
		a.flash("Auto-save on")
		for _, t := range a.tabs {
			if autoSavable(t) {
				a.armAutoSave()
				break
			}
		}
	} else {
		a.stopAutoSave()
		a.flash("Auto-save off")
	}
	if err := userconfig.SaveAutoSave(userconfig.DefaultPath(), a.autoSaveEnabled); err != nil {
		a.flash("config: " + err.Error())
	}
}

// autoSaveToggleLabel is the dynamic menu label for the auto-save
// row — same toggle-in-place pattern as the sidebar row, so the menu
// always names the action it will perform, not the current state.
func (a *App) autoSaveToggleLabel() string {
	if a.autoSaveEnabled {
		return "Disable auto-save"
	}
	return "Enable auto-save"
}

// Compile-time check that autoSaveEvent really is a tcell.Event.
var _ tcell.Event = (*autoSaveEvent)(nil)
