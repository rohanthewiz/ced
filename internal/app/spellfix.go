// =============================================================================
// File: internal/app/spellfix.go
// Author: Rohan Allison <rohanthewiz@gmail.com>
// Created: 2026-11-16
// Copyright: 2026 Rohan Allison. All rights reserved.
// =============================================================================

// spellfix.go is the struct-tag spell check's active half: where an
// underline (spellcheck.go) turns into a decision. One picker offers the
// three answers there are to a flagged word, as cats-todo's Spelling
// panel does:
//
//	┌ Spelling · Nmae ─────────────────────────┐
//	│ userName                                 │  it was a typo: the corrected
//	│ userMae                                  │  NAME, best first
//	│ Add "nmae" to my dictionary              │  it was a word: remember it
//	│ Add "nmae" to project dictionary         │  (yours, or the repo's)
//	│ Stop spell-checking struct tags          │  the marks are in the way
//	└──────────────────────────────────────────┘
//
// Rows are labelled with the whole corrected tag name rather than the
// bare word: "userName" is what the reader recognises and what will be
// written, where "Name" alone says nothing about where it lands.
//
// It is a picker (openPicker), the sanctioned choose-one-from-a-list
// surface: it filters as you type and needs no new chassis. Two doors
// open it, both aimed at a word:
//
//   - the editor's right-click, when the click lands on a flagged word —
//     the gesture every editor has taught for a squiggle;
//   - ≡ Code "Fix spelling at caret…", the keyboard's way (and the twin
//     every right-click row needs, since tmux often eats the click).
//
// A correction is VERIFIED before it is written: the picker captured the
// word's columns when it opened, and the pick re-reads them. Nothing can
// normally edit the buffer under a modal, but a reload from disk or a
// workspace edit can, and a correction written into whatever now sits at
// those columns would be a new typo made by the spell checker.

package app

import (
	"strings"

	"github.com/rohanthewiz/ced/internal/editor"
	"github.com/rohanthewiz/ced/internal/spell"
)

// spellSuggestions is how many corrections the picker offers — the
// package's own cap (spell's suggestMax): past the first handful the
// candidates are all at the far edge of the distance, and a list that has
// to be read rather than glanced at is not a suggestion any more.
const spellSuggestions = 8

// menuFixSpellingAtCaret is the ≡ Code row: open the picker on the
// flagged word at the caret, or the nearest one on the caret's line. Every
// reason it cannot is flashed, never a dimmed row — each is something the
// user can act on (move to a Go file, turn the check on, wait a moment).
func (a *App) menuFixSpellingAtCaret() {
	a.closeMenu()
	t := a.activeTabPtr()
	switch {
	case t == nil || t.Buffer == nil:
		a.flash("No file is open")
		return
	case !isGoPath(t.Path):
		a.flash("Spelling is checked in Go struct tags — this is not a .go file")
		return
	case !a.spell.on:
		a.flash("Struct-tag spell check is off — ≡ View → " + spellOnLabel)
		return
	case a.spell.dict == nil:
		// spellAfterEvent will have launched the load by the time the
		// user tries again; this event is what triggers it if not.
		a.flash("The word list is still loading — try again in a moment")
		return
	}
	h, ok := a.spellHitNear(t, t.Cursor)
	if !ok {
		a.flash("No misspelled struct-tag word on this line")
		return
	}
	a.openSpellingPicker(t, h)
}

// spellContextRow is the editor right-click row for a click that landed on
// a flagged word (the click already moved the caret there). ok is false
// anywhere else, and the menu shows no row: a spelling row on every
// right-click would be a permanently dead row in all other text.
func (a *App) spellContextRow(t *editor.Tab) (editorContextItem, bool) {
	h, ok := a.spellHitAt(t, t.Cursor)
	if !ok {
		return editorContextItem{}, false
	}
	return editorContextItem{
		label:   spellContextLabel(h.word(t)),
		action:  func(app *App) { app.openSpellingPicker(t, h) },
		enabled: alwaysTrue,
	}, true
}

// spellContextLabel is the right-click row's label for word.
func spellContextLabel(word string) string {
	return "Fix spelling of \"" + word + "\"…"
}

// openSpellingPicker opens the picker for one flagged piece. Rows are
// built now and capture h; the pick verifies it (spellReplace).
//
// A held load error — a user dictionary that could not be read — is
// flashed here, when the user has asked about spelling, rather than at
// load time when they had not (the "startup errors are held" rule).
func (a *App) openSpellingPicker(t *editor.Tab, h spellHit) {
	if a.spell.dict == nil {
		return
	}
	word := h.word(t)
	rs := t.Buffer.LineRunes(h.line)
	head := string(rs[h.nameStart:h.start])
	tail := string(rs[h.end:h.nameEnd])

	suggestions := a.spell.dict.SuggestPiece(word, spellSuggestions)
	var items []paletteItem
	for _, s := range suggestions {
		items = append(items, paletteItem{
			label: head + s + tail,
			run:   func(app *App) { app.spellReplace(t, h, word, s) },
		})
	}
	// The word goes into a dictionary lowercased: its capital is the
	// camelCase convention's ("Acme" in userAcme), not the word's, and
	// lookup folds case anyway.
	entry := strings.ToLower(word)
	if p := spellDictPathFn(); p != "" {
		items = append(items, paletteItem{
			label: "Add \"" + entry + "\" to my dictionary",
			run:   func(app *App) { app.spellAddWord(entry, p, "your dictionary") },
		})
	}
	if p := a.spellProjectDictPath(); p != "" {
		items = append(items, paletteItem{
			label: "Add \"" + entry + "\" to project dictionary",
			run:   func(app *App) { app.spellAddWord(entry, p, "the project dictionary") },
		})
	}
	items = append(items, paletteItem{label: spellOffLabel, run: func(app *App) { app.setSpellcheck(false) }})

	// The title names the word (the picker is centred, away from the
	// code), and announces the cap or the empty list — a short list
	// should never read as "that's all" when it is "that's all I'll show".
	title := "Spelling · " + word
	switch len(suggestions) {
	case 0:
		title += " — no close words"
	case spellSuggestions:
		title += " — closest " + itoa(spellSuggestions)
	}
	a.openPicker(title, items)
	if a.spell.loadErr != nil {
		a.flash("Word list skipped: " + a.spell.loadErr.Error())
	}
}

// spellReplace writes a picked correction over the flagged piece, as one
// undo step, after verifying the piece is still where the picker found it.
// The caret lands at the end of the corrected word.
func (a *App) spellReplace(t *editor.Tab, h spellHit, word, fixed string) {
	if a.tabIndexOf(t) < 0 {
		a.flash("That file was closed")
		return
	}
	rs := t.Buffer.LineRunes(h.line)
	if h.end > len(rs) || string(rs[h.start:h.end]) != word {
		a.flash("The tag changed — ask about the word again")
		return
	}
	edit := editor.Edit{
		Start:   editor.Position{Line: h.line, Col: h.start},
		End:     editor.Position{Line: h.line, Col: h.end},
		NewText: fixed,
	}
	// ApplyMultiEdit is the one-undo-step write path for app code: it
	// pushes the undo checkpoint, bumps EditRev and restyles, so the LSP
	// sync, auto-save and the underline all see the change like a
	// keystroke.
	if _, ok := t.ApplyMultiEdit([]editor.Edit{edit}); !ok {
		a.flash("The correction could not be applied")
		return
	}
	t.MoveCursorTo(editor.Position{Line: h.line, Col: h.start + runeLen(fixed)}, false)
}

// spellAddWord writes word to the dictionary file at path and teaches the
// loaded dictionary the same word — in that order, so a failed write
// teaches nothing and the file and the session never disagree about what
// was accepted. where names the file in the confirmation.
func (a *App) spellAddWord(word, path, where string) {
	if a.spell.dict == nil {
		return
	}
	if err := spell.AppendWord(path, word); err != nil {
		a.flash("Could not add \"" + word + "\": " + err.Error())
		return
	}
	a.spell.dict.Add(word)
	a.flash("Added \"" + word + "\" to " + where)
}
