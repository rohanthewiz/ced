// =============================================================================
// File: internal/app/spellfix_test.go
// Author: Rohan Allison <rohanthewiz@gmail.com>
// Created: 2026-11-16
// Copyright: 2026 Rohan Allison. All rights reserved.
// =============================================================================

package app

import (
	"os"
	"strings"
	"sync"
	"testing"

	"github.com/rohanthewiz/ced/internal/editor"
	"github.com/rohanthewiz/ced/internal/spell"
)

// realSpellDict is the embedded word list, loaded once for the tests that
// need real corrections (the stub dictionaries have nothing to suggest).
var realSpellDict = sync.OnceValue(func() *spell.Dictionary {
	d, err := spell.Load()
	if err != nil {
		panic(err)
	}
	return d
})

// spellPicker returns the open picker, failing when something else holds
// the modal slot.
func spellPicker(t *testing.T, a *App) *paletteModal {
	t.Helper()
	pm, ok := a.modal.(*paletteModal)
	if !ok {
		t.Fatalf("no picker open (modal %T, flash %q)", a.modal, a.statusMsg)
	}
	return pm
}

// pickerRow runs the picker row whose label is label.
func pickerRow(t *testing.T, a *App, pm *paletteModal, label string) {
	t.Helper()
	for _, it := range pm.items {
		if it.label == label {
			a.closeAllModals()
			it.run(a)
			return
		}
	}
	var labels []string
	for _, it := range pm.items {
		labels = append(labels, it.label)
	}
	t.Fatalf("no row %q in %q", label, labels)
}

// line3 is the fixture's tag line as the buffer holds it now.
func line3(tab *editor.Tab) string { return string(tab.Buffer.LineRunes(3)) }

// TestFixSpellingAtCaret_OffersCorrectionsAsTagNames: the ≡ row opens the
// picker on the word at the caret, its rows are the corrected NAMES (in
// the word's case) followed by the two dictionaries and the off switch,
// and the title names the word.
func TestFixSpellingAtCaret_OffersCorrectionsAsTagNames(t *testing.T) {
	a, tab := spellTestApp(t, "t.go", spellFixture)
	a.spell.dict = realSpellDict()
	tab.Cursor = editor.Position{Line: 3, Col: 22}
	a.menuFixSpellingAtCaret()
	pm := spellPicker(t, a)
	if !strings.HasPrefix(pm.title, "Spelling · Nmae") {
		t.Errorf("title = %q, want it to name the word", pm.title)
	}
	if len(pm.items) < 4 || pm.items[0].label != "userName" {
		t.Fatalf("first row = %q, want the corrected name %q", pm.items[0].label, "userName")
	}
	n := len(pm.items)
	tail := []string{pm.items[n-3].label, pm.items[n-2].label, pm.items[n-1].label}
	want := []string{`Add "nmae" to my dictionary`, `Add "nmae" to project dictionary`, spellOffLabel}
	if strings.Join(tail, "|") != strings.Join(want, "|") {
		t.Errorf("last rows = %q, want %q", tail, want)
	}
}

// TestSpellReplace_FixesTheWordInOneUndoStep: a picked correction replaces
// only the flagged piece, the underline goes, the caret lands after the
// word, and one Undo puts the typo back.
func TestSpellReplace_FixesTheWordInOneUndoStep(t *testing.T) {
	a, tab := spellTestApp(t, "t.go", spellFixture)
	a.spell.dict = realSpellDict()
	before := line3(tab)
	tab.Cursor = editor.Position{Line: 3, Col: 22}
	a.menuFixSpellingAtCaret()
	pickerRow(t, a, spellPicker(t, a), "userName")

	if got := line3(tab); !strings.Contains(got, `json:"userName"`) || !strings.Contains(got, `db:"adress"`) {
		t.Fatalf("after the fix: %q", got)
	}
	if tab.Cursor != (editor.Position{Line: 3, Col: 25}) {
		t.Errorf("caret = %+v, want just after the corrected word (3,25)", tab.Cursor)
	}
	if _, ok := a.spellHitAt(tab, editor.Position{Line: 3, Col: 22}); ok {
		t.Error("the corrected word is still flagged")
	}
	tab.Undo()
	if got := line3(tab); got != before {
		t.Errorf("one Undo gave %q, want the original %q", got, before)
	}
}

// TestSpellReplace_RefusesWhenTheTagChanged: the pick re-reads the word's
// columns; if the text there moved (a reload, a workspace edit) the
// correction is refused rather than written over whatever is there now.
func TestSpellReplace_RefusesWhenTheTagChanged(t *testing.T) {
	a, tab := spellTestApp(t, "t.go", spellFixture)
	h, ok := a.spellHitAt(tab, editor.Position{Line: 3, Col: 22})
	if !ok {
		t.Fatal("fixture word not flagged")
	}
	tab.Buffer.Lines[3] = "\tN string `json:\"xyzzyNmae\"`" // shifted under the picker
	a.spellReplace(tab, h, "Nmae", "Name")
	if got := line3(tab); got != "\tN string `json:\"xyzzyNmae\"`" {
		t.Errorf("a stale correction was written: %q", got)
	}
	if !strings.Contains(a.statusMsg, "changed") {
		t.Errorf("flash = %q, want the reason", a.statusMsg)
	}
}

// TestSpellAddWord_RemembersTheWord: both Add rows write the lowercased
// word to their file and teach the loaded list at once, so the underline
// goes without a reload.
func TestSpellAddWord_RemembersTheWord(t *testing.T) {
	a, tab := spellTestApp(t, "t.go", spellFixture)
	tab.Cursor = editor.Position{Line: 3, Col: 22}
	a.menuFixSpellingAtCaret()
	pickerRow(t, a, spellPicker(t, a), `Add "nmae" to project dictionary`)
	data, err := os.ReadFile(a.spellProjectDictPath())
	if err != nil || !strings.HasSuffix(string(data), "\nnmae\n") {
		t.Fatalf("project dictionary: %q (err %v)", data, err)
	}
	if _, ok := a.spellHitAt(tab, editor.Position{Line: 3, Col: 22}); ok {
		t.Error("an added word is still flagged")
	}

	tab.Cursor = editor.Position{Line: 3, Col: 38} // on "adress"
	a.menuFixSpellingAtCaret()
	pickerRow(t, a, spellPicker(t, a), `Add "adress" to my dictionary`)
	if data, err := os.ReadFile(spellDictPathFn()); err != nil || !strings.Contains(string(data), "adress") {
		t.Errorf("user dictionary: %q (err %v)", data, err)
	}
	if got := spellSpanTexts(t, a, tab); len(got) != 0 {
		t.Errorf("still underlined after both adds: %q", got)
	}
}

// TestSpellPicker_OffRowTurnsTheCheckOff: the last row is the same
// switch as the ≡ View row.
func TestSpellPicker_OffRowTurnsTheCheckOff(t *testing.T) {
	a, tab := spellTestApp(t, "t.go", spellFixture)
	tab.Cursor = editor.Position{Line: 3, Col: 22}
	a.menuFixSpellingAtCaret()
	pickerRow(t, a, spellPicker(t, a), spellOffLabel)
	if a.spell.on {
		t.Error("the picker's off row left the check on")
	}
}

// TestFixSpellingAtCaret_ExplainsWhyNot: every reason the ≡ row cannot
// open the picker is flashed, never a silent no-op.
func TestFixSpellingAtCaret_ExplainsWhyNot(t *testing.T) {
	a, tab := spellTestApp(t, "t.txt", spellFixture)
	tab.Cursor = editor.Position{Line: 3, Col: 22}
	a.menuFixSpellingAtCaret()
	if a.modal != nil || !strings.Contains(a.statusMsg, ".go") {
		t.Errorf("non-Go file: modal %T, flash %q", a.modal, a.statusMsg)
	}

	a, tab = spellTestApp(t, "t.go", spellFixture)
	a.spell.on = false
	a.menuFixSpellingAtCaret()
	if a.modal != nil || !strings.Contains(a.statusMsg, spellOnLabel) {
		t.Errorf("check off: modal %T, flash %q (want the way to turn it on)", a.modal, a.statusMsg)
	}

	a.spell.on = true
	a.spell.dict = nil
	a.menuFixSpellingAtCaret()
	if a.modal != nil || !strings.Contains(a.statusMsg, "loading") {
		t.Errorf("no word list yet: modal %T, flash %q", a.modal, a.statusMsg)
	}

	a.spell.dict = spell.New("user")
	tab.Cursor = editor.Position{Line: 0}
	a.menuFixSpellingAtCaret()
	if a.modal != nil || !strings.Contains(a.statusMsg, "No misspelled") {
		t.Errorf("no word on the line: modal %T, flash %q", a.modal, a.statusMsg)
	}
}

// TestSpellContextRow_OnlyOnAFlaggedWord: the right-click row leads the
// menu when the click lands on a flagged word — including the one just
// typed, which carries no underline — and is absent anywhere else.
func TestSpellContextRow_OnlyOnAFlaggedWord(t *testing.T) {
	a, tab := spellTestApp(t, "t.go", spellFixture)
	tab.Cursor = editor.Position{Line: 3, Col: 25} // end of "Nmae": typing position
	items := a.editorContextItems(tab)
	if items[0].label != spellContextLabel("Nmae") || !items[1].split {
		t.Errorf("menu starts %q, %v; want the spelling row then a split", items[0].label, items[1].split)
	}

	tab.Cursor = editor.Position{Line: 3, Col: 18} // in "user", a known piece
	for _, it := range a.editorContextItems(tab) {
		if strings.HasPrefix(it.label, "Fix spelling") {
			t.Errorf("a known word got the spelling row %q", it.label)
		}
	}
}
