// =============================================================================
// File: internal/app/altchords_test.go
// Author: Rohan Allison <rohanthewiz@gmail.com>
// Created: 2026-11-06
// Copyright: 2026 Rohan Allison. All rights reserved.
// =============================================================================

package app

import (
	"sort"
	"strings"
	"testing"

	"github.com/gdamore/tcell/v2"
	"github.com/rohanthewiz/ced/internal/favorites"
	"github.com/rohanthewiz/ced/internal/readme"
)

// TestAltChordRune_OnlyAltRunes pins the one "is this an Alt chord?"
// question every surface asks: Alt+<rune> answers its rune, while a
// plain rune, an Alt+arrow and a bare Esc are not chords.
func TestAltChordRune_OnlyAltRunes(t *testing.T) {
	if r, ok := altChordRune(tcell.NewEventKey(tcell.KeyRune, 'c', tcell.ModAlt)); !ok || r != 'c' {
		t.Errorf("Alt+c = %q %v, want 'c' true", r, ok)
	}
	for name, ev := range map[string]*tcell.EventKey{
		"plain c":  tcell.NewEventKey(tcell.KeyRune, 'c', tcell.ModNone),
		"Alt+Up":   tcell.NewEventKey(tcell.KeyUp, 0, tcell.ModAlt),
		"Alt+Esc":  tcell.NewEventKey(tcell.KeyEsc, 0, tcell.ModAlt),
		"Shift+C":  tcell.NewEventKey(tcell.KeyRune, 'C', tcell.ModShift),
		"Ctrl+Alt": tcell.NewEventKey(tcell.KeyEnter, 0, tcell.ModAlt|tcell.ModCtrl),
	} {
		if _, ok := altChordRune(ev); ok {
			t.Errorf("%s read as an Alt chord", name)
		}
	}
}

// TestFireAltChord_FirstMatchWinsAndMissesReport pins the dispatch rules
// the tables rely on: the first entry for a key runs (so a table's order
// is its precedence), a nil run still consumes the key, and an unbound
// key reports false so the surface can let it through.
func TestFireAltChord_FirstMatchWinsAndMissesReport(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	var ran []string
	chords := []altChord{
		{key: 'x', run: func(*App) { ran = append(ran, "first") }},
		{key: 'x', run: func(*App) { ran = append(ran, "second") }},
		{key: 'n'},
	}
	if !fireAltChord(a, chords, 'x') || len(ran) != 1 || ran[0] != "first" {
		t.Errorf("x: ran %v, want only the first entry", ran)
	}
	if !fireAltChord(a, chords, 'n') {
		t.Error("a nil-run chord must still consume its key")
	}
	if fireAltChord(a, chords, 'q') {
		t.Error("an unbound key reported as fired")
	}
}

// TestPromptAltChords_ExtrasThenClear pins the prompt's table order:
// extras first, then the clear chord, so an extra bound to x would keep
// it. It also pins that the clear chord really empties the field and an
// extra's chord re-asks a live prompt (onEdit) as a typed edit does.
func TestPromptAltChords_ExtrasThenClear(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	var edits []string
	toggled := false
	m := &promptModal{
		field:     newTextField("seed"),
		clearable: true,
		extras: []promptExtra{
			{key: 'x', run: func(*App) { toggled = !toggled }},
			{key: 0, run: func(*App) { t.Error("a keyless extra must have no chord") }},
		},
		onEdit: func(_ *App, v string) { edits = append(edits, v) },
	}
	a.openModal(m)

	keys := altChordKeys(m.altChords(a))
	if keys != "x x" {
		t.Fatalf("prompt table keys = %q, want the extra's x before clear's x", keys)
	}
	m.handleKey(a, tcell.NewEventKey(tcell.KeyRune, 'x', tcell.ModAlt))
	if !toggled || m.field.String() != "seed" {
		t.Errorf("Alt+x ran toggled=%v field=%q, want the extra (field kept)", toggled, m.field.String())
	}
	if len(edits) != 1 || edits[0] != "seed" {
		t.Errorf("onEdit heard %v, want one re-ask with the unchanged text", edits)
	}

	m.extras = nil
	m.handleKey(a, tcell.NewEventKey(tcell.KeyRune, 'x', tcell.ModAlt))
	if m.field.String() != "" {
		t.Errorf("with no extra on x, Alt+x left %q, want the field cleared", m.field.String())
	}
}

// altChordKeys spells a table's keys in order, space-separated — a
// compact thing to compare in a failure message.
func altChordKeys(cs []altChord) string {
	keys := make([]string, len(cs))
	for i, c := range cs {
		keys[i] = string(c.key)
	}
	return strings.Join(keys, " ")
}

// altChordSurfaces opens every keyboard-owning surface that answers Alt
// chords, each in the state that shows ALL its chords (the commit prompt
// on a drafted message, so the trailer chip is there; a NAMED bookmark,
// so Clear name is), and returns its table under the name README.md's
// "Where" column uses.
//
// This is the fixture the README pin rests on, the same way the
// right-click pins rest on theirs: a surface not listed here is
// unpinned. A new surface with Alt chords gets an entry, and a README
// row naming a surface or chord this fixture doesn't reach fails the pin.
func altChordSurfaces(t *testing.T) map[string][]altChord {
	t.Helper()
	out := map[string][]altChord{}
	prompt := func(a *App, where string) {
		t.Helper()
		pm, ok := a.modal.(*promptModal)
		if !ok {
			t.Fatalf("%s: modal = %T, want a prompt", where, a.modal)
		}
		out[where] = pm.altChords(a)
		a.closeModal()
	}

	a := newTestApp(t, t.TempDir())
	out["Find bar"] = a.findBarAltChords()

	a.openFindPrompt("Find all in file", "lists every match", "", func(*App, string) {})
	prompt(a, "Find prompts")

	a.gitIsRepo = true
	wireChat(a)
	a.openCommitPromptDraft([]gitPanelFile{{Path: "/p/a.go", Rel: "a.go", Code: " M"}}, "Add the thing", true)
	prompt(a, "Commit message")

	a.openNotePrompt(chatAttach{}, "", false, false)
	prompt(a, "GoNotes title")

	bm, _, _ := bmTestApp(t)
	bmCaretTo(bm, 2)
	bm.menuLabelBookmark()
	bmSubmitPrompt(t, bm, "named")
	bm.menuLabelBookmark()
	prompt(bm, "Label bookmark")

	_, fav := manageProject(t)
	fav.promptFavoriteName("ai_docs/plans", favorites.ScopeProject, nil)
	prompt(fav, "Add to favorites")

	// The two dialogs' tables are fixed (they close over the modal, not
	// over its state), so a zero modal shows every chord.
	out["Cherry-pick"] = (&cherryPickModal{}).altChords(a)
	out["Change view"] = (&hunkViewModal{}).altChords(a)

	// Recent folders' Local pane and Recent locations share these
	// sections (locationSections); both headings show when both lists
	// have a folder.
	pal := &paletteModal{items: a.locationSections("", []string{"internal"}, []string{"docs"})}
	out["Recent folders / locations"] = pal.altChords(a)
	return out
}

// TestAltChords_NoSurfaceBindsAKeyTwice: within one surface a key may be
// bound once. fireAltChord's first match wins, so a second entry for the
// same key would be dead — a button whose keyboard twin silently runs
// some other button.
func TestAltChords_NoSurfaceBindsAKeyTwice(t *testing.T) {
	for where, cs := range altChordSurfaces(t) {
		seen := map[rune]bool{}
		for _, c := range cs {
			if seen[c.key] {
				t.Errorf("%s binds alt+%c twice; the second can never fire", where, c.key)
			}
			seen[c.key] = true
		}
	}
}

// TestREADME_AltChordTableListsEveryChord pins README.md's "#### Alt
// chords inside dialogs" table to the surfaces' altChords tables, both
// ways (N-110): every chord a surface answers has a row naming that
// surface in "Where", and every row names a surface the fixture opens
// and a chord that surface answers. "Does" must say something. A row
// may list several chords (`alt+n` / `alt+p`).
func TestREADME_AltChordTableListsEveryChord(t *testing.T) {
	doc, err := readme.LoadRepo()
	if err != nil {
		t.Fatalf("load README: %v", err)
	}
	sec, ok := doc.Section(4, "Alt chords inside dialogs")
	if !ok {
		t.Fatal(`README has no "#### Alt chords inside dialogs" section`)
	}
	tables := sec.Tables()
	if len(tables) != 1 {
		t.Fatalf("README line %d: the Alt chord section has %d tables, want 1", sec.Line, len(tables))
	}

	// answered[where][spelling] — what the code binds.
	answered := map[string]map[string]bool{}
	for where, cs := range altChordSurfaces(t) {
		answered[where] = map[string]bool{}
		for _, c := range cs {
			answered[where]["alt+"+string(c.key)] = true
		}
	}

	listed := map[string]map[string]bool{}
	for _, r := range tables[0].Rows {
		where := strings.TrimSpace(r.Cell(0))
		chords := readme.CodeSpans(r.Cell(1))
		if len(chords) == 0 {
			t.Errorf("README line %d: Chord cell %q names no chord in backticks", r.Line, r.Cell(1))
		}
		if strings.TrimSpace(r.Cell(2)) == "" {
			t.Errorf("README line %d: %s %v says nothing in Does", r.Line, where, chords)
		}
		binds, known := answered[where]
		if !known {
			t.Errorf("README line %d: Where %q is no surface the fixture opens (renamed? add it to altChordSurfaces)", r.Line, where)
			continue
		}
		if listed[where] == nil {
			listed[where] = map[string]bool{}
		}
		for _, c := range chords {
			if !binds[c] {
				t.Errorf("README line %d: %s does not answer %s (removed or rebound?)", r.Line, where, c)
			}
			listed[where][c] = true
		}
	}

	var missing []string
	for where, binds := range answered {
		for c := range binds {
			if !listed[where][c] {
				missing = append(missing, where+" "+c)
			}
		}
	}
	sort.Strings(missing)
	for _, m := range missing {
		t.Errorf("%s is bound, but the README Alt chord table has no row for it", m)
	}
}
