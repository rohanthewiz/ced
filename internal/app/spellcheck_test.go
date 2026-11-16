// =============================================================================
// File: internal/app/spellcheck_test.go
// Author: Rohan Allison <rohanthewiz@gmail.com>
// Created: 2026-11-16
// Copyright: 2026 Rohan Allison. All rights reserved.
// =============================================================================

package app

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gdamore/tcell/v2"

	"github.com/rohanthewiz/ced/internal/editor"
	"github.com/rohanthewiz/ced/internal/lsp"
	"github.com/rohanthewiz/ced/internal/spell"
)

// spellFixture is the struct every spelling test reads: line 3 holds two
// tags, one misspelled piece in each ("Nmae" at cols 21–25 and "adress"
// at cols 35–41).
const spellFixture = "package x\n\ntype T struct {\n\tN string `json:\"userNmae\" db:\"adress\"`\n}\n"

// spellTestApp opens name (holding content) with the check on and a
// small dictionary that knows "user" — so exactly the fixture's two
// pieces are flagged, with no 90k-word load.
func spellTestApp(t *testing.T, name, content string) (*App, *editor.Tab) {
	t.Helper()
	a := newTestApp(t, t.TempDir())
	a.spell.on = true
	a.spell.dict = spell.New("user", "name", "address")
	path := filepath.Join(a.rootDir, name)
	writeFileT(t, path, content)
	a.openFile(path)
	tab := a.activeTabPtr()
	if tab == nil || tab.Path != path {
		t.Fatalf("%s did not open", name)
	}
	tab.Cursor = editor.Position{} // away from any tag word
	return a, tab
}

// spellSpanTexts runs the source over the whole buffer and returns the
// underlined text of each span, failing on anything but the spelling
// style (underline, DiagWarning ink) or a gutter mark.
func spellSpanTexts(t *testing.T, a *App, tab *editor.Tab) []string {
	t.Helper()
	spans, marks := spellSource{app: a}.Decorations(tab, a.theme, 0, tab.Buffer.LineCount()-1)
	if len(marks) != 0 {
		t.Errorf("spelling claimed %d gutter marks; the mark cell is for broken code", len(marks))
	}
	var out []string
	for _, sp := range spans {
		d := sp.Delta
		if !d.Underline || !d.SetFG || d.FG != a.theme.DiagWarning || d.SetBG {
			t.Errorf("span %+v: want underline in DiagWarning ink only", sp)
		}
		out = append(out, string(tab.Buffer.LineRunes(sp.Start.Line)[sp.Start.Col:sp.End.Col]))
	}
	return out
}

// TestSpellSource_UnderlinesMisspelledTagPieces is the feature: the
// misspelled piece of each tag name is underlined, the known ones are not.
func TestSpellSource_UnderlinesMisspelledTagPieces(t *testing.T) {
	a, tab := spellTestApp(t, "t.go", spellFixture)
	got := spellSpanTexts(t, a, tab)
	if strings.Join(got, ",") != "Nmae,adress" {
		t.Fatalf("underlined %q, want [Nmae adress]", got)
	}
}

// TestSpellSource_QuietWhereItDoesNotApply: a non-Go file, the switch
// off, and a dictionary not yet loaded all paint nothing.
func TestSpellSource_QuietWhereItDoesNotApply(t *testing.T) {
	a, tab := spellTestApp(t, "t.txt", spellFixture)
	if got := spellSpanTexts(t, a, tab); len(got) != 0 {
		t.Errorf("a .txt file was spell-checked: %q", got)
	}

	a, tab = spellTestApp(t, "t.go", spellFixture)
	a.spell.on = false
	if got := spellSpanTexts(t, a, tab); len(got) != 0 {
		t.Errorf("the check painted while off: %q", got)
	}
	a.spell.on = true
	a.spell.dict = nil
	if got := spellSpanTexts(t, a, tab); len(got) != 0 {
		t.Errorf("the check painted with no dictionary: %q", got)
	}
}

// TestSpellSource_LeavesTheWordBeingTyped: a piece with the caret at its
// end is the word being typed and is not marked; a caret INSIDE a word
// (a click) keeps the mark.
func TestSpellSource_LeavesTheWordBeingTyped(t *testing.T) {
	a, tab := spellTestApp(t, "t.go", spellFixture)
	tab.Cursor = editor.Position{Line: 3, Col: 25} // just after "Nmae"
	if got := spellSpanTexts(t, a, tab); strings.Join(got, ",") != "adress" {
		t.Errorf("caret at the end of Nmae: underlined %q, want only adress", got)
	}
	tab.Cursor = editor.Position{Line: 3, Col: 23} // inside "Nmae"
	if got := spellSpanTexts(t, a, tab); strings.Join(got, ",") != "Nmae,adress" {
		t.Errorf("caret inside Nmae: underlined %q, want both", got)
	}
}

// spellCellAt returns the drawn cell holding the first occurrence of
// needle on screen, offset by off runes into it.
func spellCellAt(t *testing.T, a *App, needle string, off int) tcell.SimCell {
	t.Helper()
	a.draw()
	a.screen.Show()
	scr := a.screen.(tcell.SimulationScreen)
	cells, w, h := scr.GetContents()
	for y := range h {
		row := make([]rune, w)
		for x := range w {
			row[x] = ' '
			if rs := cells[y*w+x].Runes; len(rs) > 0 {
				row[x] = rs[0]
			}
		}
		if i := strings.Index(string(row), needle); i >= 0 {
			return cells[y*w+len([]rune(string(row)[:i]))+off]
		}
	}
	t.Fatalf("%q is not on screen:\n%s", needle, screenText(a))
	return tcell.SimCell{}
}

// TestSpellSource_DrawsAndYieldsToDiagnostics renders the real frame: the
// misspelled piece is underlined in warning ink, its neighbours are not,
// and a language-server diagnostic over the same cells wins the colour
// (spell is registered below every diagnostic producer).
func TestSpellSource_DrawsAndYieldsToDiagnostics(t *testing.T) {
	a, tab := spellTestApp(t, "t.go", spellFixture)
	c := spellCellAt(t, a, "userNmae", 4) // the N
	fg, _, attr := c.Style.Decompose()
	if attr&tcell.AttrUnderline == 0 || fg != a.theme.DiagWarning {
		t.Errorf("the N of Nmae: fg=%v underline=%v, want DiagWarning + underline", fg, attr&tcell.AttrUnderline != 0)
	}
	if _, _, attr := spellCellAt(t, a, "userNmae", 0).Style.Decompose(); attr&tcell.AttrUnderline != 0 {
		t.Error("the u of user is underlined; only the misspelled piece should be")
	}

	a.lsp.diags = map[string][]lsp.Diagnostic{tab.Path: {{
		Range: lsp.Range{
			Start: lsp.Position{Line: 3, Character: 21},
			End:   lsp.Position{Line: 3, Character: 25},
		},
		Severity: lsp.SeverityError,
		Message:  "boom",
	}}}
	if fg, _, _ := spellCellAt(t, a, "userNmae", 4).Style.Decompose(); fg != a.theme.DiagError {
		t.Errorf("under an LSP error the N is %v, want the diagnostic's DiagError", fg)
	}
}

// TestSpellHitNear_FindsTheWordForTheMenuRow: the ≡ row aims at the word
// under the caret, else the nearest on the caret's line.
func TestSpellHitNear_FindsTheWordForTheMenuRow(t *testing.T) {
	a, tab := spellTestApp(t, "t.go", spellFixture)
	for _, tc := range []struct {
		col  int
		want string
	}{
		{22, "Nmae"},   // inside
		{25, "Nmae"},   // just after its end
		{0, "Nmae"},    // line start: Nmae is nearer than adress
		{40, "adress"}, // inside the second
		{44, "adress"}, // past the end of the line's tags
	} {
		h, ok := a.spellHitNear(tab, editor.Position{Line: 3, Col: tc.col})
		if !ok || h.word(tab) != tc.want {
			t.Errorf("col %d: got %q (ok=%v), want %q", tc.col, h.word(tab), ok, tc.want)
		}
	}
	if _, ok := a.spellHitNear(tab, editor.Position{Line: 0}); ok {
		t.Error("a line with no tag found a word")
	}
	if _, ok := a.spellHitAt(tab, editor.Position{Line: 3, Col: 18}); ok {
		t.Error("spellHitAt answered for a known piece (user)")
	}
}

// stubSpellLoad swaps the loader for one that counts calls and returns
// d, err.
func stubSpellLoad(t *testing.T, d *spell.Dictionary, err error) *int {
	t.Helper()
	calls := 0
	prev := spellLoadFn
	spellLoadFn = func(...string) (*spell.Dictionary, error) {
		calls++
		return d, err
	}
	t.Cleanup(func() { spellLoadFn = prev })
	return &calls
}

// waitSpellDict pumps the simulation screen until the loader's event
// arrives, and hands it to the main-loop handler.
func waitSpellDict(t *testing.T, a *App) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if e, ok := a.screen.PollEvent().(*spellDictEvent); ok {
			a.handleEvent(e)
			return
		}
	}
	t.Fatal("the dictionary load never answered")
}

// TestSpellAfterEvent_LoadsLazilyForGoTabs: nothing loads while the check
// is off or the active tab is not Go; the first active Go tab starts ONE
// async load, and its answer installs the dictionary.
func TestSpellAfterEvent_LoadsLazilyForGoTabs(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	want := spell.New("user")
	calls := stubSpellLoad(t, want, nil)

	txt := filepath.Join(a.rootDir, "notes.txt")
	writeFileT(t, txt, "hello\n")
	a.openFile(txt)
	a.spell.on = true
	a.spellAfterEvent()
	if *calls != 0 || a.spell.loading {
		t.Fatal("a .txt tab started a load")
	}

	goFile := filepath.Join(a.rootDir, "t.go")
	writeFileT(t, goFile, spellFixture)
	a.openFile(goFile)
	a.spell.on = false
	a.spellAfterEvent()
	if a.spell.loading {
		t.Fatal("the check is off, yet a load started")
	}

	a.spell.on = true
	a.spellAfterEvent()
	a.spellAfterEvent() // in flight: no second launch
	if !a.spell.loading {
		t.Fatal("an active Go tab did not start the load")
	}
	waitSpellDict(t, a)
	if a.spell.dict != want || a.spell.loading || *calls != 1 {
		t.Errorf("after the answer: dict=%p want %p, loading=%v, calls=%d", a.spell.dict, want, a.spell.loading, *calls)
	}
}

// TestHandleSpellDict_DropsStaleAnswers: only the latest launch's answer
// applies — a reload's slower predecessor must not overwrite it.
func TestHandleSpellDict_DropsStaleAnswers(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	a.spell.seq = 2
	a.spell.loading = true
	a.handleSpellDict(&spellDictEvent{seq: 1, dict: spell.New("old")})
	if a.spell.dict != nil || !a.spell.loading {
		t.Fatal("a stale answer was applied")
	}
	fresh := spell.New("new")
	a.handleSpellDict(&spellDictEvent{seq: 2, dict: fresh})
	if a.spell.dict != fresh || a.spell.loading {
		t.Fatal("the latest answer was not applied")
	}
}

// TestHandleSpellDict_HoldsErrorsAndLatchesAFailure: an unreadable user
// list is held beside a working dictionary; NO dictionary latches dead so
// the after-event hook stops relaunching, and the ≡ row clears it.
func TestHandleSpellDict_HoldsErrorsAndLatchesAFailure(t *testing.T) {
	a, _ := spellTestApp(t, "t.go", spellFixture)
	a.spell.dict = nil
	a.spell.seq = 1
	a.handleSpellDict(&spellDictEvent{seq: 1, dict: spell.New("x"), err: errors.New("bad file")})
	if a.spell.dict == nil || a.spell.loadErr == nil || a.spell.dead {
		t.Fatalf("an unreadable user list: dict=%v err=%v dead=%v", a.spell.dict, a.spell.loadErr, a.spell.dead)
	}

	a.spell.dict = nil
	calls := stubSpellLoad(t, nil, errors.New("corrupt"))
	a.handleSpellDict(&spellDictEvent{seq: 1, err: errors.New("corrupt")})
	if !a.spell.dead {
		t.Fatal("a load with no dictionary did not latch dead")
	}
	a.spellAfterEvent()
	if *calls != 0 {
		t.Fatal("a dead check relaunched on the next event")
	}
	a.setSpellcheck(true)
	if a.spell.dead {
		t.Error("re-picking the ≡ row did not clear the latch")
	}
}

// TestSpellAfterSave_ReloadsTheWordLists: saving one of the dictionary
// files re-reads them (the hand-edit loop); saving anything else does
// not; with the check off the loaded copy is dropped instead.
func TestSpellAfterSave_ReloadsTheWordLists(t *testing.T) {
	a, tab := spellTestApp(t, "t.go", spellFixture)
	calls := stubSpellLoad(t, spell.New("user"), nil)

	a.spellAfterSave(tab.Path)
	if *calls != 0 || a.spell.loading {
		t.Fatal("saving a Go file reloaded the word lists")
	}
	a.spellAfterSave(a.spellProjectDictPath())
	waitSpellDict(t, a)
	if *calls != 1 {
		t.Fatalf("saving the project dictionary: %d loads, want 1", *calls)
	}
	a.spell.on = false
	a.spellAfterSave(spellDictPathFn())
	if a.spell.dict != nil || *calls != 1 {
		t.Errorf("with the check off a save should drop the list, not reload it (calls=%d)", *calls)
	}
}

// TestSetSpellcheck_PersistsAndRelabels: the toggle is the single write
// path — it flips the state, the ≡ label follows, and config.json (the
// seam's throwaway copy) records it.
func TestSetSpellcheck_PersistsAndRelabels(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	a.spell.on = true
	if a.spellcheckToggleLabel() != spellOffLabel {
		t.Errorf("label while on = %q", a.spellcheckToggleLabel())
	}
	a.menuToggleSpellcheck()
	if a.spell.on || a.spellcheckToggleLabel() != spellOnLabel {
		t.Fatalf("after toggling off: on=%v label=%q", a.spell.on, a.spellcheckToggleLabel())
	}
	data, err := os.ReadFile(spellConfigPathFn())
	if err != nil || !strings.Contains(string(data), `"spellcheck"`) || !strings.Contains(string(data), "off") {
		t.Errorf("config.json after toggling off: %s (err %v)", data, err)
	}
}

// TestLoadUserConfig_ReadsSpellcheck: the switch comes from config.json,
// on by default.
func TestLoadUserConfig_ReadsSpellcheck(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	a := newTestApp(t, t.TempDir())
	a.loadUserConfig()
	if !a.spell.on {
		t.Error("spellcheck is off with no config file; the default is on")
	}
}
