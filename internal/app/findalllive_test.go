// =============================================================================
// File: internal/app/findalllive_test.go
// Author: Rohan Allison <rohanthewiz@gmail.com>
// Created: 2026-10-26
// Copyright: 2026 Rohan Allison. All rights reserved.
// =============================================================================

package app

import (
	"strings"
	"testing"

	"github.com/gdamore/tcell/v2"

	"github.com/rohanthewiz/ced/internal/editor"
)

// openLiveFindAll opens the Find-all prompt with the caret at origin and
// returns it, failing when the prompt didn't come up or isn't live.
func openLiveFindAll(t *testing.T, a *App, tab *editor.Tab, origin editor.Position) *promptModal {
	t.Helper()
	tab.MoveCursorTo(origin, false)
	a.openFindAll()
	m, ok := a.modal.(*promptModal)
	if !ok || m.onEdit == nil {
		t.Fatalf("modal = %T, want the live Find-all prompt", a.modal)
	}
	return m
}

// retype clears the prompt's field and types s through its own key
// handler, so each rune is one live re-search, as at the keyboard.
func retype(a *App, m *promptModal, s string) {
	m.handleKey(a, keyEv(tcell.KeyEnd, 0))
	for range m.field.value {
		m.handleKey(a, keyEv(tcell.KeyBackspace2, 0))
	}
	for _, r := range s {
		m.handleKey(a, keyEv(tcell.KeyRune, r))
	}
}

// liveStatus is the prompt's status-row text.
func liveStatus(a *App, m *promptModal) string { return m.status(a) }

// TestFindAllLive_SeedSearchesFromTheStart: the prompt answers before a
// key is pressed — the seed (word under the caret) tints every hit,
// previews the one the list would open on, and the status counts them.
func TestFindAllLive_SeedSearchesFromTheStart(t *testing.T) {
	a, tab := seedFindAllApp(t)
	m := openLiveFindAll(t, a, tab, editor.Position{Line: 2, Col: 5}) // on "count"
	if m.field.String() != "count" {
		t.Fatalf("seed = %q, want count", m.field.String())
	}
	if len(tab.FindMatches) != 4 || tab.FindQuery != "count" {
		t.Fatalf("tint: query %q, %d matches; want count ×4", tab.FindQuery, len(tab.FindMatches))
	}
	if got := liveStatus(a, m); got != "1 of 4 · enter lists them" {
		t.Errorf("status = %q", got)
	}
	if !m.bottom {
		t.Error("live prompt should rest on the bottom of the editor")
	}
}

// TestFindAllLive_TypingReSearchesFromTheOrigin: every keystroke re-runs
// the search, and the preview is always the first hit at or after where
// the user STARTED — narrowing past a hit and widening again comes back
// to it rather than walking on from the last preview.
func TestFindAllLive_TypingReSearchesFromTheOrigin(t *testing.T) {
	a, tab := seedFindAllApp(t)
	origin := editor.Position{Line: 3, Col: 0}
	m := openLiveFindAll(t, a, tab, origin)

	retype(a, m, "count")
	if tab.Cursor != (editor.Position{Line: 3, Col: 1}) || liveStatus(a, m) != "2 of 4 · enter lists them" {
		t.Fatalf("count: cursor %v status %q, want (3,1) and 2 of 4", tab.Cursor, liveStatus(a, m))
	}
	m.handleKey(a, keyEv(tcell.KeyRune, '+'))
	if tab.Cursor != (editor.Position{Line: 5, Col: 2}) || liveStatus(a, m) != "1 of 1 · enter lists them" {
		t.Fatalf("count+: cursor %v status %q, want (5,2) and 1 of 1", tab.Cursor, liveStatus(a, m))
	}
	m.handleKey(a, keyEv(tcell.KeyBackspace2, 0))
	if tab.Cursor != (editor.Position{Line: 3, Col: 1}) {
		t.Fatalf("back to count: cursor %v, want (3,1) — measured from the origin", tab.Cursor)
	}
}

// TestFindAllLive_NoAnswerPutsTheViewBack: a miss, a bad pattern and an
// empty field each return the caret to the origin and drop the tint, so
// the previous query's hit can't pass for this one's.
func TestFindAllLive_NoAnswerPutsTheViewBack(t *testing.T) {
	a, tab := seedFindAllApp(t)
	origin := editor.Position{Line: 3, Col: 0}
	m := openLiveFindAll(t, a, tab, origin)

	cases := []struct {
		text, status string
		regex        bool
	}{
		{"zzz", "no matches", false},
		{"", "", false},
		{"(co", "bad regex", true},
	}
	for _, c := range cases {
		a.findRegex = c.regex
		retype(a, m, "count") // move off the origin first
		retype(a, m, c.text)
		if tab.Cursor != origin || len(tab.FindMatches) != 0 {
			t.Errorf("%q: cursor %v, %d tinted; want the origin and no tint", c.text, tab.Cursor, len(tab.FindMatches))
		}
		if got := liveStatus(a, m); !strings.HasPrefix(got, c.status) || (c.status == "" && got != "") {
			t.Errorf("%q: status %q, want prefix %q", c.text, got, c.status)
		}
	}
}

// TestFindAllLive_ChipReSearches: flipping an option chip (Alt+c) re-asks
// the same text under the new options.
func TestFindAllLive_ChipReSearches(t *testing.T) {
	a, tab := seedFindAllApp(t)
	m := openLiveFindAll(t, a, tab, editor.Position{Line: 3, Col: 0})
	retype(a, m, "COUNT")
	if len(tab.FindMatches) != 4 {
		t.Fatalf("case off: %d matches, want 4", len(tab.FindMatches))
	}
	m.handleKey(a, tcell.NewEventKey(tcell.KeyRune, 'c', tcell.ModAlt))
	if !a.findCase || len(tab.FindMatches) != 0 || !strings.HasPrefix(liveStatus(a, m), "no matches") {
		t.Fatalf("case on: findCase=%v, %d matches, status %q", a.findCase, len(tab.FindMatches), liveStatus(a, m))
	}
}

// TestFindAllLive_EscRestoresViewAndFindState: Esc hands back the caret,
// the scroll and the tab's find state exactly as the prompt found them.
func TestFindAllLive_EscRestoresViewAndFindState(t *testing.T) {
	a, tab := seedFindAllApp(t)
	tab.SetFindQuery("int")
	prior := len(tab.FindMatches)
	origin := editor.Position{Line: 3, Col: 0}
	m := openLiveFindAll(t, a, tab, origin)
	scrollY := tab.ScrollY

	retype(a, m, "count")
	m.handleKey(a, keyEv(tcell.KeyEsc, 0))
	if a.modal != nil {
		t.Fatalf("Esc left %T open", a.modal)
	}
	if tab.Cursor != origin || tab.ScrollY != scrollY {
		t.Errorf("view: cursor %v scroll %d, want %v / %d", tab.Cursor, tab.ScrollY, origin, scrollY)
	}
	if tab.FindQuery != "int" || len(tab.FindMatches) != prior {
		t.Errorf("find state: %q ×%d, want int ×%d", tab.FindQuery, len(tab.FindMatches), prior)
	}
}

// TestFindAllLive_EnterOpensTheListFromTheTrueOrigin: Enter opens the
// list on the previewed hit, but the list's origin (what its Esc and ↺
// revert loc go back to) and borrowed find state are the ones the user
// had BEFORE the prompt, not the prompt's last preview.
func TestFindAllLive_EnterOpensTheListFromTheTrueOrigin(t *testing.T) {
	a, tab := seedFindAllApp(t)
	tab.SetFindQuery("int")
	origin := editor.Position{Line: 3, Col: 0}
	m := openLiveFindAll(t, a, tab, origin)
	retype(a, m, "count")
	m.handleKey(a, keyEv(tcell.KeyEnter, 0))

	list, ok := a.modal.(*findAllModal)
	if !ok {
		t.Fatalf("Enter opened %T, want the list", a.modal)
	}
	if list.origin != origin || list.priorQuery != "int" {
		t.Errorf("list origin %v prior %q, want %v and int", list.origin, list.priorQuery, origin)
	}
	if list.selected != 1 || tab.Cursor != (editor.Position{Line: 3, Col: 1}) {
		t.Errorf("list on row %d cursor %v, want the previewed hit (row 1, (3,1))", list.selected, tab.Cursor)
	}
}

// TestFindAllLive_HitUnderThePromptIsCentered: a hit on a row the prompt
// covers counts as off-screen and is scrolled out from under the box,
// while a hit already clear of it leaves the scroll alone.
func TestFindAllLive_HitUnderThePromptIsCentered(t *testing.T) {
	a, tab := seedFindAllLongApp(t)
	_, ey, ew, eh := a.editorRect()
	m := openLiveFindAll(t, a, tab, editor.Position{Line: 1, Col: 0})
	_, py, _, ph := m.rect(a)

	retype(a, m, "count") // line 4: clear of the box, nothing to scroll
	if tab.Cursor.Line != 4 || tab.ScrollY != 0 {
		t.Fatalf("visible hit: line %d scroll %d, want 4 and 0", tab.Cursor.Line, tab.ScrollY)
	}

	// Restart from a line whose next hit sits on a row the box covers.
	m.handleKey(a, keyEv(tcell.KeyEsc, 0))
	covered := py - ey // first editor row under the box
	line := covered + (4-covered%4)%4 // first hit line (every 4th) at or below it
	if line >= py-ey+ph {
		t.Fatalf("fixture: window %dx%d puts no hit under the prompt", a.width, a.height)
	}
	m = openLiveFindAll(t, a, tab, editor.Position{Line: line - 1, Col: 0})
	retype(a, m, "count")
	if tab.Cursor.Line != line {
		t.Fatalf("preview on line %d, want %d", tab.Cursor.Line, line)
	}
	_, dy, vis := tab.PosScreenCell(tab.Cursor, ew, eh)
	if row := ey + dy; !vis || (row >= py && row < py+ph) {
		t.Fatalf("hit on screen row %d (visible %v), prompt covers %d..%d", row, vis, py, py+ph-1)
	}
}

// TestFindAllLive_ProjectPromptStaysInert: Find in project walks every
// file, so its prompt is not live.
func TestFindAllLive_ProjectPromptStaysInert(t *testing.T) {
	a, _ := seedFindAllApp(t)
	a.menuFindInProject()
	m, ok := a.modal.(*promptModal)
	if !ok {
		t.Fatalf("modal = %T", a.modal)
	}
	if m.onEdit != nil || m.bottom {
		t.Error("project prompt should stay inert and centered")
	}
}
