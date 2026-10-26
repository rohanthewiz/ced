// =============================================================================
// File: internal/app/projectsearch_test.go
// Author: Rohan Allison <rohanthewiz@gmail.com>
// Created: 2026-08-03
// Copyright: 2026 Rohan Allison. All rights reserved.
// =============================================================================

package app

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gdamore/tcell/v2"

	"github.com/rohanthewiz/ced/internal/editor"
	"github.com/rohanthewiz/ced/internal/filetree"
	"github.com/rohanthewiz/ced/internal/finder"
	"github.com/rohanthewiz/ced/internal/search"
)

// TestFindInProject_AlwaysAsksAndSeedsFromTheSelection pins the seeding
// rule: Find in project ALWAYS opens the prompt (a whole-tree walk is
// worth one Enter to confirm), and the box is pre-filled with the
// selection when there is one, else the word under the cursor.
func TestFindInProject_AlwaysAsksAndSeedsFromTheSelection(t *testing.T) {
	a, root := projectSearchApp(t)
	a.openFile(filepath.Join(root, "alpha.go"))
	tab := a.activeTabPtr()
	if tab == nil {
		t.Fatal("fixture did not open")
	}

	// A bare cursor inside "needle" pre-fills the word.
	tab.MoveCursorTo(editor.Position{Line: 2, Col: 7}, false)
	a.menuFindInProject()
	m, ok := a.modal.(*promptModal)
	if !ok {
		t.Fatalf("modal = %T, want a promptModal asking for the query", a.modal)
	}
	if got := m.field.String(); got != "needle" {
		t.Errorf("prompt pre-fill = %q, want the word under the cursor %q", got, "needle")
	}
	a.closeModal()

	// A single-line selection seeds the prompt verbatim — including a
	// span that is not a whole word, which the cursor rule could never
	// have produced — and still waits for Enter.
	tab.Anchor = editor.Position{Line: 2, Col: 6}
	tab.Cursor = editor.Position{Line: 2, Col: 13}
	a.menuFindInProject()
	m, ok = a.modal.(*promptModal)
	if !ok {
		t.Fatalf("with a selection, modal = %T, want a promptModal seeded with it", a.modal)
	}
	if got := m.field.String(); got != "eedle()" {
		t.Errorf("prompt pre-fill from selection = %q, want %q", got, "eedle()")
	}
	a.closeModal()

	// A multi-line selection is not a search term, so the seed falls
	// back to the word under the cursor (the selection's end).
	tab.Anchor = editor.Position{Line: 0, Col: 0}
	tab.Cursor = editor.Position{Line: 2, Col: 7}
	if got := a.projectSearchSeed(); got != "needle" {
		t.Errorf("seed with a multi-line selection = %q, want the cursor word %q", got, "needle")
	}
}

// projectSearchApp seeds a small project with hits in two files and
// returns the app plus the root.
func projectSearchApp(t *testing.T) (*App, string) {
	t.Helper()
	dir := t.TempDir()
	files := map[string]string{
		"alpha.go":        "package alpha\n\nfunc needle() {}\n",
		"sub/bravo.go":    "\tif needle != nil {\n\t\treturn needle\n\t}\n",
		"sub/charlie.txt": "nothing to see\n",
	}
	for name, body := range files {
		full := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return newTestApp(t, dir), dir
}

// runProjectSearch performs the search synchronously and delivers it the
// way the goroutine would, so the tests exercise the real handler without
// waiting on the index or the event queue.
func runProjectSearch(t *testing.T, a *App, root, query string, paths []string) {
	t.Helper()
	runProjectSearchOpts(t, a, root, query, paths, editor.FindOptions{})
}

// runProjectSearchOpts is runProjectSearch under explicit match options,
// carried on the event the way startProjectSearch carries them.
func runProjectSearchOpts(t *testing.T, a *App, root, query string, paths []string, opts editor.FindOptions) {
	t.Helper()
	hits, truncated := search.Project(root, paths, search.Options{Query: query, FindOptions: opts})
	a.projectSearchSeq++
	a.handleProjectSearch(&projectSearchEvent{
		when: time.Now(), seq: a.projectSearchSeq,
		query: query, opts: opts, hits: hits, truncated: truncated,
	})
}

// TestProjectSearch_OpensTheFindAllPanelInProjectMode is the point of the
// stage: cross-file results reuse the in-file list rather than growing a
// second list surface, and arrive labelled with their paths.
func TestProjectSearch_OpensTheFindAllPanelInProjectMode(t *testing.T) {
	a, root := projectSearchApp(t)
	runProjectSearch(t, a, root, "needle", []string{"alpha.go", "sub/bravo.go", "sub/charlie.txt"})

	m, ok := a.modal.(*findAllModal)
	if !ok {
		t.Fatalf("expected the find-all panel, got %T", a.modal)
	}
	if !m.project {
		t.Fatal("the panel must be in project mode")
	}
	if len(m.rows) != 3 {
		t.Fatalf("expected 3 hits, got %d", len(m.rows))
	}
	if m.rows[0].label != "alpha.go:3" {
		t.Fatalf("row label should be path:line, got %q", m.rows[0].label)
	}
	if !strings.Contains(m.rows[1].label, "sub/bravo.go") {
		t.Fatalf("row should carry its relative path, got %q", m.rows[1].label)
	}
	// Compaction is shared with the in-file list: indentation off, so the
	// hit column still maps onto the display text.
	r := m.rows[1]
	if strings.HasPrefix(r.text, "\t") {
		t.Fatalf("row text should be compacted, got %q", r.text)
	}
	if got := string([]rune(r.text)[r.hit : r.hit+r.hitW]); !strings.EqualFold(got, "needle") {
		t.Fatalf("the lit range should cover the match, got %q in %q", got, r.text)
	}
}

// TestProjectSearch_FilterSeedsWithTheQuery pins the pre-fill at the
// wider scope: the box arrives holding the expression, narrowing nothing
// until it is edited. It earns its place here twice over — narrowing the
// answer in the box costs nothing, while restating the question costs a
// whole-tree walk.
func TestProjectSearch_FilterSeedsWithTheQuery(t *testing.T) {
	a, root := projectSearchApp(t)
	runProjectSearch(t, a, root, "needle", []string{"alpha.go", "sub/bravo.go"})

	m, ok := a.modal.(*findAllModal)
	if !ok {
		t.Fatalf("expected the find-all panel, got %T", a.modal)
	}
	if got := m.filter.String(); got != "needle" {
		t.Fatalf("filter = %q, want the query pre-filled", got)
	}
	if len(m.view) != len(m.rows) {
		t.Fatalf("the seed must narrow nothing: view %d of %d rows", len(m.view), len(m.rows))
	}
	// A replaced filter narrows to the file whose path carries it — rows
	// match on their label as well as their text.
	m.filter = newTextField("bravo")
	m.rebuildView()
	if len(m.view) == 0 || len(m.view) >= len(m.rows) {
		t.Fatalf("an edited filter should narrow: %d of %d", len(m.view), len(m.rows))
	}
}

// TestProjectSearch_WalkingRowsOpensNothing pins the deliberate departure
// from the in-file list. Previewing across files would open a file per
// keystroke — firing didOpen, plugin hooks and a syntax pass each time,
// and leaving a tab behind for every row merely scrolled past.
func TestProjectSearch_WalkingRowsOpensNothing(t *testing.T) {
	a, root := projectSearchApp(t)
	runProjectSearch(t, a, root, "needle", []string{"alpha.go", "sub/bravo.go"})
	m := a.modal.(*findAllModal)

	m.moveSelection(a, 1)
	m.moveSelection(a, 1)
	if len(a.tabs) != 0 {
		t.Fatalf("walking the list must not open files, got %d tabs", len(a.tabs))
	}
	if m.selected == 0 {
		t.Fatal("the highlight should still have moved")
	}
}

// TestProjectSearch_AcceptOpensTheFileAtTheHit is the verb behind Enter.
func TestProjectSearch_AcceptOpensTheFileAtTheHit(t *testing.T) {
	a, root := projectSearchApp(t)
	runProjectSearch(t, a, root, "needle", []string{"alpha.go", "sub/bravo.go"})
	m := a.modal.(*findAllModal)
	m.selectRow(a, 2) // the second hit inside sub/bravo.go

	want := m.rows[2]
	m.accept(a)

	if a.modal != nil {
		t.Fatalf("accept must close the list, got %T", a.modal)
	}
	tab := a.activeTabPtr()
	if tab == nil {
		t.Fatal("accept should have opened a tab")
	}
	if tab.Path != want.path {
		t.Fatalf("opened %q, want %q", tab.Path, want.path)
	}
	if tab.Cursor.Line != want.line || tab.Cursor.Col != want.col {
		t.Fatalf("cursor at %+v, want line %d col %d", tab.Cursor, want.line, want.col)
	}
	if tab.FindQuery != "needle" {
		t.Fatalf("the hit should be lit in the opened file, FindQuery = %q", tab.FindQuery)
	}
}

// TestProjectSearch_ClickOpensTheFileAndKeepsTheList pins the mouse
// contract for every list of cross-file results: a single click on a row
// navigates to that file and line the way the in-file list's click
// previews, and the list STAYS so the next row is one more click away.
// A double-click is still the accept, which closes it.
func TestProjectSearch_ClickOpensTheFileAndKeepsTheList(t *testing.T) {
	a, root := projectSearchApp(t)
	runProjectSearch(t, a, root, "needle", []string{"alpha.go", "sub/bravo.go"})
	m := a.modal.(*findAllModal)
	mx, my, _, _ := m.rect(a)
	x, y := mx+8, my+4+2 // the third visible row: the second hit in sub/bravo.go
	want := m.rows[2]

	m.handleMouse(a, x, y, tcell.Button1)

	if a.modal != m {
		t.Fatalf("a single click must keep the list open, modal = %T", a.modal)
	}
	tab := a.activeTabPtr()
	if tab == nil || tab.Path != want.path {
		t.Fatalf("click opened %v, want %q", tab, want.path)
	}
	if tab.Cursor.Line != want.line || tab.Cursor.Col != want.col {
		t.Fatalf("cursor at %+v, want line %d col %d", tab.Cursor, want.line, want.col)
	}
	if tab.FindQuery != "needle" {
		t.Fatalf("the hit should be lit in the opened file, FindQuery = %q", tab.FindQuery)
	}

	// A second click on the same row inside the double-click window is
	// the accept: the jump is already made, and the list goes.
	m.handleMouse(a, x, y, tcell.Button1)
	if a.modal != nil {
		t.Fatalf("a double-click must close the list, got %T", a.modal)
	}
	if len(a.tabs) != 1 {
		t.Fatalf("clicking one row twice must open one tab, got %d", len(a.tabs))
	}
}

// TestProjectSearch_EscRestoresNothingBecauseNothingMoved: the in-file
// list restores the cursor and scroll on Esc, and project mode must NOT
// try to — it has no tab behind it, and writing find state would clobber
// whatever the active tab legitimately holds.
func TestProjectSearch_EscRestoresNothingBecauseNothingMoved(t *testing.T) {
	a, root := projectSearchApp(t)
	a.openFile(filepath.Join(root, "alpha.go"))
	tab := a.activeTabPtr()
	tab.SetFindQuery("alpha")
	before := tab.Cursor

	runProjectSearch(t, a, root, "needle", []string{"alpha.go", "sub/bravo.go"})
	m := a.modal.(*findAllModal)
	m.moveSelection(a, 1)
	m.abort(a)

	if a.modal != nil {
		t.Fatalf("esc must close the list, got %T", a.modal)
	}
	if tab.Cursor != before {
		t.Fatalf("cursor moved during a project search: %+v -> %+v", before, tab.Cursor)
	}
	if tab.FindQuery != "alpha" {
		t.Fatalf("project mode clobbered the tab's own find state: %q", tab.FindQuery)
	}
}

// TestProjectSearch_StaleResultIsDropped pins the generation check: a
// search the user has already moved on from must not open a list over
// whatever they are doing now.
func TestProjectSearch_StaleResultIsDropped(t *testing.T) {
	a, root := projectSearchApp(t)
	hits, _ := search.Project(root, []string{"alpha.go"}, search.Options{Query: "needle"})
	a.projectSearchSeq = 7

	a.handleProjectSearch(&projectSearchEvent{
		when: time.Now(), seq: 6, query: "needle", hits: hits,
	})
	if a.modal != nil {
		t.Fatalf("a stale result must be dropped, got %T", a.modal)
	}
}

// TestProjectSearch_NoHitsFlashesInsteadOfOpening — an empty box makes
// the user dismiss something to learn "no", the same rule the in-file
// list follows.
func TestProjectSearch_NoHitsFlashesInsteadOfOpening(t *testing.T) {
	a, root := projectSearchApp(t)
	runProjectSearch(t, a, root, "definitely-absent", []string{"alpha.go"})
	if a.modal != nil {
		t.Fatalf("no hits must not open a list, got %T", a.modal)
	}
	if !strings.Contains(a.statusMsg, "no occurrences") {
		t.Fatalf("expected a no-results flash, got %q", a.statusMsg)
	}
}

// TestProjectSearch_TruncationShowsInTheTitle — a capped list that looks
// complete is the one wrong answer a search can give.
func TestProjectSearch_TruncationShowsInTheTitle(t *testing.T) {
	a, _ := projectSearchApp(t)
	m := &findAllModal{query: "needle", project: true, truncated: true,
		rows: make([]findAllRow, 10)}
	if !strings.Contains(m.titleText(), "first 10") {
		t.Fatalf("a truncated search must say so: %q", m.titleText())
	}
	m.truncated = false
	if strings.Contains(m.titleText(), "first") {
		t.Fatalf("a complete search must not claim truncation: %q", m.titleText())
	}
	_ = a
}

// TestProjectSearchLabel_TruncatesFromTheFront: the distinguishing part
// of a path is its tail, so twenty rows reading "internal/app/…" would
// say nothing at all.
func TestProjectSearchLabel_TruncatesFromTheFront(t *testing.T) {
	got := rowLabelText("internal/app/copilot_chat_context.go:412", 20)
	if !strings.HasPrefix(got, "…") {
		t.Fatalf("a truncated label should be cut from the front, got %q", got)
	}
	if !strings.HasSuffix(got, ":412") {
		t.Fatalf("the line number must survive truncation, got %q", got)
	}
	if len([]rune(got)) != 20 {
		t.Fatalf("label should fill its column exactly, got %d runes", len([]rune(got)))
	}
	if short := rowLabelText("a.go:1", 20); short != "a.go:1" {
		t.Fatalf("a short label must be left alone, got %q", short)
	}
}

// TestStartProjectSearch_WithoutAnIndexSaysSo — the index builds
// asynchronously, and "no results" would be a lie while it is empty.
func TestStartProjectSearch_WithoutAnIndexSaysSo(t *testing.T) {
	a, _ := projectSearchApp(t)
	a.finder = nil
	a.startProjectSearch("needle", editor.FindOptions{})
	if a.modal != nil {
		t.Fatalf("expected no list, got %T", a.modal)
	}
	if !strings.Contains(a.statusMsg, "index") {
		t.Fatalf("expected an indexing flash, got %q", a.statusMsg)
	}
}

// TestProjectSearch_DismissTakesBackTheTintItScattered pins the bug fix:
// every row a click opened was tinted with the query, and project mode's
// restoreFind used to return early, so the highlights outlived the list.
// Esc on the list must now clear them — and restore, not blank, a tab
// that held its own query before the list lit it.
func TestProjectSearch_DismissTakesBackTheTintItScattered(t *testing.T) {
	a, root := projectSearchApp(t)
	a.openFile(filepath.Join(root, "alpha.go"))
	alpha := a.activeTabPtr()
	alpha.SetFindQuery("alpha")

	runProjectSearch(t, a, root, "needle", []string{"alpha.go", "sub/bravo.go"})
	m := a.modal.(*findAllModal)
	m.selectRow(a, 0)
	m.jumpToSelected(a) // alpha.go, overwriting its own "alpha"
	m.selectRow(a, 2)
	m.jumpToSelected(a) // sub/bravo.go
	bravo := a.activeTabPtr()
	if alpha.FindQuery != "needle" || bravo.FindQuery != "needle" {
		t.Fatalf("clicks should tint: alpha %q, bravo %q", alpha.FindQuery, bravo.FindQuery)
	}

	m.abort(a)

	if bravo.FindQuery != "" || len(bravo.FindMatches) != 0 {
		t.Fatalf("dismissal left the tint in bravo: %q (%d matches)", bravo.FindQuery, len(bravo.FindMatches))
	}
	if alpha.FindQuery != "alpha" {
		t.Fatalf("dismissal should restore alpha's own query, got %q", alpha.FindQuery)
	}
}

// TestProjectSearch_EscClearsTheAcceptedTint: accepting a row keeps the
// hit lit on arrival (the list is gone by then), and a plain Esc in the
// editor is how that tint leaves — before the fix nothing ever cleared it.
func TestProjectSearch_EscClearsTheAcceptedTint(t *testing.T) {
	a, root := projectSearchApp(t)
	runProjectSearch(t, a, root, "needle", []string{"alpha.go", "sub/bravo.go"})
	m := a.modal.(*findAllModal)
	m.selectRow(a, 2)
	m.accept(a)
	tab := a.activeTabPtr()
	if tab == nil || tab.FindQuery != "needle" {
		t.Fatal("accept should leave the hit lit in the opened file")
	}

	a.handleKey(tcell.NewEventKey(tcell.KeyEsc, 0, tcell.ModNone))

	if tab.FindQuery != "" || len(tab.FindMatches) != 0 {
		t.Fatalf("Esc left the project tint: %q (%d matches)", tab.FindQuery, len(tab.FindMatches))
	}
}

// TestProjectSearch_TintClearLeavesAUsersNewSearchAlone: a tab whose
// query changed after the list lit it belongs to the user now, and the
// ledger must not blank a search they started themselves.
func TestProjectSearch_TintClearLeavesAUsersNewSearchAlone(t *testing.T) {
	a, root := projectSearchApp(t)
	runProjectSearch(t, a, root, "needle", []string{"alpha.go"})
	m := a.modal.(*findAllModal)
	m.selectRow(a, 0)
	m.accept(a)
	tab := a.activeTabPtr()
	tab.SetFindQuery("package")

	a.clearProjectFindTints()

	if tab.FindQuery != "package" {
		t.Fatalf("a user's own query was clobbered: %q", tab.FindQuery)
	}
}

// TestProjectSearch_CarriesTheOptionsThrough pins N-035 at project
// scope: a match-case search lists only exact-case hits, says so in the
// title, tints the opened file under the same options (a tint lighting
// every case variant would contradict the row just clicked), and hands
// the tab its own options back when the list goes.
func TestProjectSearch_CarriesTheOptionsThrough(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "mixed.go")
	if err := os.WriteFile(target, []byte("Needle needle\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	a := newTestApp(t, dir)
	runProjectSearchOpts(t, a, dir, "needle", []string{"mixed.go"}, editor.FindOptions{CaseSensitive: true})

	m, ok := a.modal.(*findAllModal)
	if !ok {
		t.Fatalf("expected the find-all panel, got %T", a.modal)
	}
	if len(m.rows) != 1 || m.rows[0].col != 7 {
		t.Fatalf("rows = %+v, want only the lowercase hit at col 7", m.rows)
	}
	if got := m.titleText(); !strings.Contains(got, "(match case)") {
		t.Errorf("title %q should name the option", got)
	}

	m.selectRow(a, 0)
	m.jumpToSelected(a)
	tab := a.activeTabPtr()
	if tab == nil || len(tab.FindMatches) != 1 || !tab.FindOpts.CaseSensitive {
		t.Fatalf("tint should be the list's case-sensitive set, got %+v", tab)
	}

	m.abort(a)
	if tab.FindOpts != (editor.FindOptions{}) {
		t.Errorf("tab options after the list = %+v, want its prior zero value", tab.FindOpts)
	}
}

// TestProjectSearch_ReferencesTitleHasNoOptionsNote: a heading producer
// is not a text match, so even if options were somehow set on it the
// title must not claim it searched "(match case)".
func TestProjectSearch_ReferencesTitleHasNoOptionsNote(t *testing.T) {
	m := &findAllModal{
		query: "needle", project: true, heading: "References to",
		opts: editor.FindOptions{CaseSensitive: true},
	}
	if got := m.titleText(); got != `References to "needle"` {
		t.Errorf("title = %q, want no options note", got)
	}
}

// folderSearchApp is projectSearchApp with a READY finder index, plus a
// decoy folder whose name extends "sub" — the prefix trap
// scopeSearchPaths must not fall into.
func folderSearchApp(t *testing.T) (*App, string) {
	t.Helper()
	a, root := projectSearchApp(t)
	decoy := filepath.Join(root, "subway", "delta.go")
	if err := os.MkdirAll(filepath.Dir(decoy), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(decoy, []byte("needle\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	a.finder = finder.New(a.rootDir)
	a.finder.Rebuild(nil)
	waitForFinderReady(t, a)
	return a, root
}

// awaitProjectSearch drains the screen's queue until the search posts
// its result, then hands it to the real handler — the main loop's job.
func awaitProjectSearch(t *testing.T, a *App) *projectSearchEvent {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		ev := a.screen.PollEvent()
		if ev == nil {
			break
		}
		if e, ok := ev.(*projectSearchEvent); ok {
			a.handleProjectSearch(e)
			return e
		}
	}
	t.Fatal("the folder search never posted its result")
	return nil
}

// TestScopeSearchPaths_KeepsOnlyTheFolder pins the scope filter: entries
// under the folder survive, a sibling whose name merely STARTS with the
// folder's ("subway" vs "sub") does not, the root is everything, and a
// folder outside the project is nothing.
func TestScopeSearchPaths_KeepsOnlyTheFolder(t *testing.T) {
	a, root := projectSearchApp(t)
	paths := []string{"alpha.go", "sub/bravo.go", "sub/deep/x.go", "subway/delta.go"}

	got := a.scopeSearchPaths(paths, filepath.Join(root, "sub"))
	if strings.Join(got, ",") != "sub/bravo.go,sub/deep/x.go" {
		t.Errorf("scoped to sub/ = %v, want only sub/'s files", got)
	}
	if got := a.scopeSearchPaths(paths, root); len(got) != len(paths) {
		t.Errorf("scoped to the root = %v, want every path", got)
	}
	if got := a.scopeSearchPaths(paths, filepath.Dir(root)); got != nil {
		t.Errorf("a folder outside the project = %v, want nil", got)
	}
}

// TestFindInFolder_SearchesOnlyThatFolder is the feature end to end: the
// scoped search walks only the folder's indexed files, the list's title
// names the folder, and ⟳ re-asks the SAME scoped question.
func TestFindInFolder_SearchesOnlyThatFolder(t *testing.T) {
	a, root := folderSearchApp(t)
	sub := filepath.Join(root, "sub")

	a.startFolderSearch("needle", editor.FindOptions{}, sub)
	e := awaitProjectSearch(t, a)
	if e.dir != sub {
		t.Fatalf("event scope = %q, want %q", e.dir, sub)
	}
	m, ok := a.modal.(*findAllModal)
	if !ok {
		t.Fatalf("expected the find-all panel, got %T (flash %q)", a.modal, a.statusMsg)
	}
	if len(m.rows) != 2 {
		t.Fatalf("rows = %d, want the two hits in sub/bravo.go", len(m.rows))
	}
	for _, r := range m.rows {
		if filepath.Dir(r.path) != sub {
			t.Errorf("row %q is outside the folder", r.path)
		}
	}
	if got := m.titleText(); !strings.HasPrefix(got, "Find in sub"+string(filepath.Separator)+" ") {
		t.Errorf("title %q should name the folder", got)
	}

	// ⟳ re-runs under the list's scope, not the whole project.
	m.rerun(a)
	if e := awaitProjectSearch(t, a); e.dir != sub {
		t.Errorf("⟳ scope = %q, want the list's folder %q", e.dir, sub)
	}
}

// TestFindInFolder_RootIsAProjectSearch: the tree root's row hands over
// the root, which must read as Find in project — no scope on the event,
// so the title and ⟳ behave exactly like the ≡ row's search.
func TestFindInFolder_RootIsAProjectSearch(t *testing.T) {
	a, root := folderSearchApp(t)
	a.startFolderSearch("needle", editor.FindOptions{}, root)
	if e := awaitProjectSearch(t, a); e.dir != "" {
		t.Fatalf("root search scope = %q, want \"\" (the project)", e.dir)
	}
	m, ok := a.modal.(*findAllModal)
	if !ok {
		t.Fatalf("expected the find-all panel, got %T", a.modal)
	}
	if got := m.titleText(); !strings.HasPrefix(got, "Find in project ") {
		t.Errorf("title = %q, want the project title", got)
	}
}

// TestFindInFolder_IgnoredFolderSaysSo: a folder with nothing indexed
// (the project ignores it) is refused with a reason, not reported as
// "no occurrences" — nothing was searched.
func TestFindInFolder_IgnoredFolderSaysSo(t *testing.T) {
	a, root := folderSearchApp(t)
	empty := filepath.Join(root, "empty")
	if err := os.MkdirAll(empty, 0o755); err != nil {
		t.Fatal(err)
	}
	a.startFolderSearch("needle", editor.FindOptions{}, empty)
	if a.modal != nil {
		t.Fatalf("expected no list, got %T", a.modal)
	}
	if !strings.Contains(a.statusMsg, "no indexed files") {
		t.Errorf("flash = %q, want the nothing-indexed reason", a.statusMsg)
	}
}

// TestTreeContext_FindAllInFolderOnFoldersOnly pins the tree row: offered
// on a folder ("Find all in folder…") and on the root, where it is
// labelled as the project search; never on a file. Activating it opens
// the query prompt scoped to the clicked folder.
func TestTreeContext_FindAllInFolderOnFoldersOnly(t *testing.T) {
	a, root := projectSearchApp(t)
	find := func(items []contextItem, label string) *contextItem {
		for i := range items {
			if items[i].label == label {
				return &items[i]
			}
		}
		return nil
	}
	subNode, ok := a.tree.Reveal(filepath.Join(root, "sub"))
	if !ok {
		t.Fatal("setup: sub/ not in the tree")
	}
	fileNode, ok := a.tree.Reveal(filepath.Join(root, "alpha.go"))
	if !ok {
		t.Fatal("setup: alpha.go not in the tree")
	}

	a.openTreeContext(fileNode, 5, 5)
	if find(contextOf(a).items, "Find all in folder…") != nil {
		t.Error("a file must not offer Find all in folder")
	}
	a.closeModal()

	a.openTreeContext(a.tree.Root, 5, 5)
	if find(contextOf(a).items, "Find in project…") == nil {
		t.Error("the root row should offer the project search")
	}
	a.closeModal()

	a.openTreeContext(subNode, 5, 5)
	row := find(contextOf(a).items, "Find all in folder…")
	if row == nil {
		t.Fatal("a folder should offer Find all in folder")
	}
	a.closeModal()
	row.action(a, subNode)
	p, ok := a.modal.(*promptModal)
	if !ok {
		t.Fatalf("modal = %T, want the query prompt", a.modal)
	}
	if p.title != "Find all in folder" || !strings.Contains(p.hint, "sub") {
		t.Errorf("prompt = %q / %q, want the folder prompt naming sub/", p.title, p.hint)
	}
}

// TestMenuFindInFolder_NamesTheActiveFolderOrExplains pins the ≡ twin:
// with a subfolder active the label names it and the row prompts; with
// none it stays clickable and flashes how to pick one.
func TestMenuFindInFolder_NamesTheActiveFolderOrExplains(t *testing.T) {
	a, root := projectSearchApp(t)

	a.activeFolder = ""
	if got := a.findInFolderLabel(); got != "Find all in folder" {
		t.Errorf("label with no folder = %q", got)
	}
	a.menuFindInFolder()
	if a.modal != nil {
		t.Fatalf("with no folder, modal = %T, want a flash", a.modal)
	}
	if !strings.Contains(a.statusMsg, "folder in the tree") {
		t.Errorf("flash = %q, want the how-to", a.statusMsg)
	}

	a.activeFolder = filepath.Join(root, "sub")
	if got := a.findInFolderLabel(); got != "Find all in folder (sub"+string(filepath.Separator)+")" {
		t.Errorf("label = %q, want it to name sub/", got)
	}
	a.menuFindInFolder()
	if _, ok := a.modal.(*promptModal); !ok {
		t.Fatalf("modal = %T, want the query prompt", a.modal)
	}
}

// TestCtxFindInFolder_IgnoresFiles: the action is only ever offered on a
// directory, and a file node reaching it anyway does nothing.
func TestCtxFindInFolder_IgnoresFiles(t *testing.T) {
	a, root := projectSearchApp(t)
	ctxFindInFolder(a, &filetree.Node{Path: filepath.Join(root, "alpha.go")})
	if a.modal != nil {
		t.Errorf("a file node opened %T", a.modal)
	}
}

// TestStartProjectSearch_BadRegexFlashesBeforeTheWalk: a pattern that
// doesn't compile is explained up front; no scan is started.
func TestStartProjectSearch_BadRegexFlashesBeforeTheWalk(t *testing.T) {
	a, _ := projectSearchApp(t)
	before := a.projectSearchSeq
	a.startProjectSearch("needle(", editor.FindOptions{Regex: true})
	if a.projectSearchSeq != before || a.projectSearchActive != 0 {
		t.Fatal("a bad pattern started a walk")
	}
	if !strings.Contains(a.statusMsg, "bad regex") {
		t.Fatalf("flash = %q, want the regex error", a.statusMsg)
	}
}

// TestFindInProject_PromptCarriesTheRegexChip: the prompt owns the
// keyboard, so it carries its own door onto the regex toggle — alt+r
// flips the App's option (the one the search reads when it runs).
func TestFindInProject_PromptCarriesTheRegexChip(t *testing.T) {
	a, root := projectSearchApp(t)
	a.openFile(filepath.Join(root, "alpha.go"))
	a.menuFindInProject()
	m, ok := a.modal.(*promptModal)
	if !ok {
		t.Fatalf("modal = %T, want the prompt", a.modal)
	}
	if len(m.extras) != 3 || m.extras[0].label(a) != "[regex: off]" {
		t.Fatalf("extras = %+v, want the regex chip rightmost of three", m.extras)
	}
	field := m.field.String()
	m.handleKey(a, tcell.NewEventKey(tcell.KeyRune, 'r', tcell.ModAlt))
	if !a.findRegex || m.extras[0].label(a) != "[regex: on]" {
		t.Fatal("alt+r did not flip the regex option")
	}
	if m.field.String() != field {
		t.Fatalf("alt+r leaked into the field: %q → %q", field, m.field.String())
	}
}

// TestProjectSearch_RevertLocReturnsToTheOriginTab: a project list's
// clicks open OTHER files, so ↺ revert loc must switch back to the tab
// the list was opened from and restore its view — while Esc after the
// same click keeps the user in the file the click opened.
func TestProjectSearch_RevertLocReturnsToTheOriginTab(t *testing.T) {
	a, root := projectSearchApp(t)
	start := filepath.Join(root, "sub/charlie.txt")
	a.openFile(start)
	home := a.activeTabPtr()
	if home == nil {
		t.Fatal("setup: origin file did not open")
	}
	runProjectSearch(t, a, root, "needle", []string{"alpha.go", "sub/bravo.go", "sub/charlie.txt"})
	m, ok := a.modal.(*findAllModal)
	if !ok {
		t.Fatalf("expected the find-all panel, got %T", a.modal)
	}
	if m.originTab != home {
		t.Fatal("the project list should capture the tab it was opened from")
	}

	m.selected = 0
	m.jumpToSelected(a) // what a single click on alpha.go does
	if a.activeTabPtr() == home {
		t.Fatal("setup: the jump should have opened another file")
	}
	m.revertLocation(a)

	if a.modal != nil {
		t.Error("revert loc should close an unpinned list")
	}
	if a.activeTabPtr() != home {
		t.Errorf("active tab = %v, want the origin %s", a.activeTabPtr().Path, start)
	}
}

// TestProjectSearch_EscAfterAJumpStaysInTheJumpedFile pins project mode's
// side of the click rule: Esc never drags the user back across files —
// only ↺ revert loc does.
func TestProjectSearch_EscAfterAJumpStaysInTheJumpedFile(t *testing.T) {
	a, root := projectSearchApp(t)
	a.openFile(filepath.Join(root, "sub/charlie.txt"))
	runProjectSearch(t, a, root, "needle", []string{"alpha.go", "sub/bravo.go", "sub/charlie.txt"})
	m := a.modal.(*findAllModal)
	m.selected = 0
	m.jumpToSelected(a)
	m.picked = true // as a row click would leave it
	m.handleKey(a, tcell.NewEventKey(tcell.KeyEsc, 0, tcell.ModNone))

	if a.modal != nil {
		t.Fatal("Esc should close the list")
	}
	if tab := a.activeTabPtr(); tab == nil || filepath.Base(tab.Path) != "alpha.go" {
		t.Errorf("Esc after a jump should stay in alpha.go, active = %v", tab)
	}
}
