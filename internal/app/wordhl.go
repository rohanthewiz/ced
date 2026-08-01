// =============================================================================
// File: internal/app/wordhl.go
// Author: Rohan Allison <rohanthewiz@gmail.com>
// Created: 2026-07-31
// Copyright: 2026 Rohan Allison. All rights reserved.
// =============================================================================

// wordhl.go holds the ≡ toggle for the matching-word highlight and the
// one place that pushes the setting onto tabs. The matcher and the
// decoration source live in internal/editor/wordhl.go.
//
// The flag lives on each Tab rather than on App because decoration
// sources are asked per-tab and have no route back here — the same
// reason Tab carries IndentUnit and the find query. App.wordHLEnabled is
// the authoritative copy (it's what gets persisted and what a newly
// opened tab inherits); applyWordHighlight is the single write path that
// keeps the tabs in step with it.
package app

import "github.com/rohanthewiz/ced/internal/userconfig"

// menuToggleWordHighlight flips the matching-word highlight for every
// open tab and persists the choice, following the exec-marks / auto-save
// toggle pattern.
func (a *App) menuToggleWordHighlight() {
	a.closeMenu()
	a.setWordHighlight(!a.wordHLEnabled)
}

// setWordHighlight is the single write path for the preference: update
// the App copy, push it to every open tab, flash, persist. Splitting the
// menu row from this means a test (or a future palette entry) can set
// the state without going through the menu.
func (a *App) setWordHighlight(on bool) {
	a.wordHLEnabled = on
	a.applyWordHighlight()
	if on {
		a.flash("Matching word highlight on")
	} else {
		a.flash("Matching word highlight off")
	}
	if err := userconfig.SaveWordHL(userconfig.DefaultPath(), on); err != nil {
		a.flash("config: " + err.Error())
	}
}

// applyWordHighlight stamps the current preference onto every open tab.
// Called when the preference changes; newly opened tabs pick it up in
// openFile instead, so a tab can never be missed.
func (a *App) applyWordHighlight() {
	for _, t := range a.tabs {
		t.WordHighlight = a.wordHLEnabled
	}
}

// wordHighlightToggleLabel names the action the row will perform, not the
// current state — the same toggle-in-place convention every other ≡
// toggle uses.
func (a *App) wordHighlightToggleLabel() string {
	if a.wordHLEnabled {
		return "Hide matching word highlight"
	}
	return "Show matching word highlight"
}
