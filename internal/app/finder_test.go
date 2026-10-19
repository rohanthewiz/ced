// =============================================================================
// File: internal/app/finder_test.go
// Author: Spicer Matthews <spicer@cloudmanic.com>
// Created: 2026-04-30
// Copyright: 2026 Cloudmanic, LLC. All rights reserved.
// =============================================================================

package app

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gdamore/tcell/v2"
	"github.com/rohanthewiz/ced/internal/filetree"
	"github.com/rohanthewiz/ced/internal/finder"
)

// waitForFinderReady spins until the App's finder reports
// StateReady or the timeout expires. Pulled out so each test
// can read as the scenario it's pinning down rather than the
// goroutine-sync boilerplate.
func waitForFinderReady(t *testing.T, a *App) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if a.finder != nil && a.finder.State() == finder.StateReady {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("finder did not reach StateReady within 2s")
}

// withFinder wires up an App + an indexed Finder rooted at a
// tempdir we seed with a few files. Tests use it as the entry
// point so they don't repeat the setup chain.
func withFinder(t *testing.T) (*App, string) {
	t.Helper()
	dir := t.TempDir()
	files := []string{
		"main.go",
		"internal/app/app.go",
		"internal/app/tab.go",
		"internal/finder/score.go",
		"README.md",
	}
	for _, f := range files {
		abs := filepath.Join(dir, f)
		if err := os.MkdirAll(filepath.Dir(abs), 0755); err != nil {
			t.Fatalf("mkdir: %v", err)
		}
		if err := os.WriteFile(abs, []byte("x"), 0644); err != nil {
			t.Fatalf("write: %v", err)
		}
	}
	a := newTestApp(t, dir)
	a.finder = finder.New(a.rootDir)
	a.finder.Rebuild(nil)
	waitForFinderReady(t, a)
	return a, dir
}

// TestOpenFinder_PopulatesResults pins the central wiring: opening
// the finder with a warm index immediately fills finderResults so
// the first frame shows real paths, not "Indexing…". Without this
// the user would see an empty list and worry the feature broke.
func TestOpenFinder_PopulatesResults(t *testing.T) {
	a, _ := withFinder(t)
	a.openFinder()

	if finderOf(a) == nil {
		t.Fatal("finderOpen should be true after openFinder")
	}
	if len(finderOf(a).results) == 0 {
		t.Fatal("expected initial result list (empty query → alphabetical)")
	}
}

// TestFinderKey_TypingFiltersResults walks the keystroke loop:
// typing "tab" narrows the list to paths matching that query, and
// the highlighted match should bubble tab.go to the top.
func TestFinderKey_TypingFiltersResults(t *testing.T) {
	a, _ := withFinder(t)
	a.openFinder()
	for _, r := range "tab" {
		finderOf(a).handleKey(a, tcell.NewEventKey(tcell.KeyRune, r, tcell.ModNone))
	}

	if len(finderOf(a).results) == 0 {
		t.Fatal("expected results after typing query")
	}
	if !endsWith(finderOf(a).results[0].Path, "tab.go") {
		t.Fatalf("top result: got %q, want ends-with tab.go", finderOf(a).results[0].Path)
	}
}

// TestFinderKey_BackspaceShrinksQuery checks the inverse: deleting
// characters re-broadens the result set. Catches a regression
// where backspace edits the query but forgets to rerun the search.
func TestFinderKey_BackspaceShrinksQuery(t *testing.T) {
	a, _ := withFinder(t)
	a.openFinder()
	for _, r := range "score" {
		finderOf(a).handleKey(a, tcell.NewEventKey(tcell.KeyRune, r, tcell.ModNone))
	}
	narrow := len(finderOf(a).results)

	// Backspace twice — the query becomes "sco", broader match.
	finderOf(a).handleKey(a, tcell.NewEventKey(tcell.KeyBackspace, 0, tcell.ModNone))
	finderOf(a).handleKey(a, tcell.NewEventKey(tcell.KeyBackspace, 0, tcell.ModNone))

	if len(finderOf(a).results) < narrow {
		t.Fatalf("backspace should not shrink results: was %d, now %d", narrow, len(finderOf(a).results))
	}
	if got := string(finderOf(a).field.value); got != "sco" {
		t.Fatalf("query: got %q, want %q", got, "sco")
	}
}

// TestFinderKey_ArrowsMoveSelection pins the navigation contract:
// ↓ moves to the next row, ↑ moves back, neither runs off the end.
// A regression here would let Enter open the wrong file.
func TestFinderKey_ArrowsMoveSelection(t *testing.T) {
	a, _ := withFinder(t)
	a.openFinder()

	finderOf(a).handleKey(a, tcell.NewEventKey(tcell.KeyDown, 0, tcell.ModNone))
	if finderOf(a).selected != 1 {
		t.Fatalf("selected after ↓: got %d, want 1", finderOf(a).selected)
	}
	finderOf(a).handleKey(a, tcell.NewEventKey(tcell.KeyUp, 0, tcell.ModNone))
	if finderOf(a).selected != 0 {
		t.Fatalf("selected after ↑: got %d, want 0", finderOf(a).selected)
	}
	// ↑ at the top stays put.
	finderOf(a).handleKey(a, tcell.NewEventKey(tcell.KeyUp, 0, tcell.ModNone))
	if finderOf(a).selected != 0 {
		t.Fatalf("selected at top after ↑: got %d, want 0", finderOf(a).selected)
	}
}

// TestFinderKey_EnterOpensFile is the headline outcome: pressing
// Enter on a result actually opens that file as a tab and
// dismisses the modal. Without this the whole feature is nothing
// but a viewer.
func TestFinderKey_EnterOpensFile(t *testing.T) {
	a, dir := withFinder(t)
	a.openFinder()
	for _, r := range "score" {
		finderOf(a).handleKey(a, tcell.NewEventKey(tcell.KeyRune, r, tcell.ModNone))
	}
	if len(finderOf(a).results) == 0 {
		t.Fatal("expected score results")
	}
	want := filepath.Join(dir, finderOf(a).results[0].Path)

	finderOf(a).handleKey(a, tcell.NewEventKey(tcell.KeyEnter, 0, tcell.ModNone))

	if finderOf(a) != nil {
		t.Fatal("modal should close after Enter")
	}
	tab := a.activeTabPtr()
	if tab == nil {
		t.Fatal("expected an active tab after opening result")
	}
	if tab.Path != want {
		t.Fatalf("opened path: got %q, want %q", tab.Path, want)
	}
}

// TestFinderKey_EscClosesModal pins the cancel path: Esc must
// dismiss without opening anything, and clear transient state so
// reopening starts fresh (no stale query, no stale selection).
func TestFinderKey_EscClosesModal(t *testing.T) {
	a, _ := withFinder(t)
	a.openFinder()
	finderOf(a).handleKey(a, tcell.NewEventKey(tcell.KeyRune, 'x', tcell.ModNone))
	finderOf(a).handleKey(a, tcell.NewEventKey(tcell.KeyEsc, 0, tcell.ModNone))

	if finderOf(a) != nil {
		t.Fatal("Esc should close the finder")
	}
	// Reopening must start fresh — the closed modal (and its query /
	// selection) is discarded wholesale, never resurrected.
	a.openFinder()
	if got := string(finderOf(a).field.value); got != "" {
		t.Fatalf("reopened finder should start with an empty query, got %q", got)
	}
	if finderOf(a).selected != 0 {
		t.Fatalf("reopened finder should start at selection 0, got %d", finderOf(a).selected)
	}
}

// TestFinderMouse_ClickOpensRow walks the click path: clicking on
// a result row both selects it and opens the file. We use the
// modal rect from the same helper the renderer uses so the row
// math stays in sync if either side changes.
func TestFinderMouse_ClickOpensRow(t *testing.T) {
	a, dir := withFinder(t)
	a.openFinder()
	if len(finderOf(a).results) < 2 {
		t.Fatalf("need at least 2 results for click test, got %d", len(finderOf(a).results))
	}
	mx, my, _, _ := finderOf(a).rect(a)
	target := filepath.Join(dir, finderOf(a).results[1].Path)

	finderOf(a).handleMouse(a, mx+5, my+4+1, tcell.Button1)

	if finderOf(a) != nil {
		t.Fatal("modal should close after click-open")
	}
	if got := a.activeTabPtr().Path; got != target {
		t.Fatalf("opened path: got %q, want %q", got, target)
	}
}

// TestFinderMouse_ClickOutsideCloses guards the dismiss path: a
// click that lands outside the modal should close it without
// opening anything. Otherwise a stray click could leave a tab
// open the user didn't ask for.
func TestFinderMouse_ClickOutsideCloses(t *testing.T) {
	a, _ := withFinder(t)
	a.openFinder()
	tabsBefore := len(a.tabs)

	finderOf(a).handleMouse(a, 0, 0, tcell.Button1)

	if finderOf(a) != nil {
		t.Fatal("modal should close on outside click")
	}
	if len(a.tabs) != tabsBefore {
		t.Fatalf("tab count changed unexpectedly: %d → %d", tabsBefore, len(a.tabs))
	}
}

// TestLeader_PFiresFinder pins the Esc-p binding so a future
// refactor of leader.go can't quietly drop it.
func TestLeader_PFiresFinder(t *testing.T) {
	a, _ := withFinder(t)
	if action := leaderActionFor('p'); action == nil {
		t.Fatal("Esc-p has no leader binding")
	} else {
		action(a)
	}
	if finderOf(a) == nil {
		t.Fatal("Esc-p should open the finder")
	}
}

// endsWith is a tiny string suffix check pulled in so the result-
// path assertions in this file read as the rule they're enforcing.
func endsWith(s, suffix string) bool {
	if len(suffix) > len(s) {
		return false
	}
	return s[len(s)-len(suffix):] == suffix
}

// TestOpenFinderIn_ScopesToTheFolder pins the folder-scoped finder: only
// files under the folder are listed, each relative to it, and the count
// tail's total is the folder's.
func TestOpenFinderIn_ScopesToTheFolder(t *testing.T) {
	a, dir := withFinder(t)
	a.openFinderIn(filepath.Join(dir, "internal"))

	m := finderOf(a)
	if m == nil {
		t.Fatal("openFinderIn should open the finder")
	}
	if m.scope != "internal" {
		t.Fatalf("scope = %q, want internal", m.scope)
	}
	if m.scopeTotal != 3 || len(m.results) != 3 {
		t.Fatalf("total %d / results %d, want the 3 files under internal/", m.scopeTotal, len(m.results))
	}
	for _, r := range m.results {
		if strings.HasPrefix(r.Path, "internal/") {
			t.Errorf("result %q should be relative to the folder", r.Path)
		}
		if r.Path == "main.go" || r.Path == "README.md" {
			t.Errorf("result %q is outside the folder", r.Path)
		}
	}
}

// TestFinderIn_PathQueryOpensTheRightFile walks the typed-path case: a
// folder-relative path narrows to one file, and Enter opens it at its
// real location under the project root.
func TestFinderIn_PathQueryOpensTheRightFile(t *testing.T) {
	a, dir := withFinder(t)
	a.openFinderIn(filepath.Join(dir, "internal"))
	for _, r := range "app/tab" {
		finderOf(a).handleKey(a, tcell.NewEventKey(tcell.KeyRune, r, tcell.ModNone))
	}
	if got := finderOf(a).results; len(got) == 0 || got[0].Path != "app/tab.go" {
		t.Fatalf("results = %+v, want app/tab.go first", got)
	}
	finderOf(a).handleKey(a, tcell.NewEventKey(tcell.KeyEnter, 0, tcell.ModNone))
	if finderOf(a) != nil {
		t.Fatal("Enter should close the finder")
	}
	want := filepath.Join(dir, "internal", "app", "tab.go")
	if tab := a.activeTabPtr(); tab == nil || tab.Path != want {
		t.Fatalf("active tab = %v, want %s", tab, want)
	}
}

// TestOpenFinderIn_RootIsThePlainFinder: the tree root's row hands over
// the project root, which must open the ordinary, unscoped finder.
func TestOpenFinderIn_RootIsThePlainFinder(t *testing.T) {
	a, dir := withFinder(t)
	a.openFinderIn(dir)
	m := finderOf(a)
	if m == nil || m.scope != "" {
		t.Fatalf("root should open the unscoped finder, got %+v", m)
	}
}

// TestOpenFinderIn_OutsideTheProjectFlashes: a folder outside the root
// (a stale node) is refused with a reason rather than an empty modal.
func TestOpenFinderIn_OutsideTheProjectFlashes(t *testing.T) {
	a, _ := withFinder(t)
	a.openFinderIn(t.TempDir())
	if a.modal != nil {
		t.Fatalf("modal = %T, want a flash", a.modal)
	}
	if !strings.Contains(a.statusMsg, "outside the project") {
		t.Errorf("flash = %q, want the outside-the-project reason", a.statusMsg)
	}
}

// TestFinderIn_DrawNamesTheFolderAndExplainsAnEmptyOne checks the paint:
// the title names the folder, and a folder with no indexed files says so
// on the first row instead of showing a blank list.
func TestFinderIn_DrawNamesTheFolderAndExplainsAnEmptyOne(t *testing.T) {
	a, dir := withFinder(t)
	empty := filepath.Join(dir, "empty")
	if err := os.MkdirAll(empty, 0o755); err != nil {
		t.Fatal(err)
	}
	a.openFinderIn(empty)
	m := finderOf(a)
	if m == nil {
		t.Fatal("finder should open on an empty folder")
	}
	m.draw(a)
	a.screen.Show()
	text := screenText(a)
	if !strings.Contains(text, "Find file in empty"+string(filepath.Separator)) {
		t.Errorf("title should name the folder:\n%s", text)
	}
	if !strings.Contains(text, "No indexed files here") {
		t.Errorf("an empty folder should explain itself:\n%s", text)
	}
}

// TestFinderDraw_QueryLinesUpWithRows pins the input row's alignment:
// the typed query starts in the same column as the result paths below
// it (mx+2), matching the palette.
func TestFinderDraw_QueryLinesUpWithRows(t *testing.T) {
	a, _ := withFinder(t)
	a.openFinder()
	for _, r := range "main" {
		finderOf(a).handleKey(a, tcell.NewEventKey(tcell.KeyRune, r, tcell.ModNone))
	}
	m := finderOf(a)
	m.draw(a)
	a.screen.Show()

	mx, my, _, _ := m.rect(a)
	if got := screenRuneAt(t, a, mx+2, my+3); got != 'm' {
		t.Errorf("query should start at the rows' column (mx+2); got %q there", got)
	}
	if got := screenRuneAt(t, a, mx+2, my+4); got == ' ' {
		t.Error("first result row should start at mx+2")
	}
}

// TestTreeContext_FindFilesInFolderOnFoldersOnly pins the tree row:
// offered on a folder and on the root (labelled as the project finder),
// never on a file; activating it opens the finder scoped to the folder.
func TestTreeContext_FindFilesInFolderOnFoldersOnly(t *testing.T) {
	a, dir := withFinder(t)
	find := func(items []contextItem, label string) *contextItem {
		for i := range items {
			if items[i].label == label {
				return &items[i]
			}
		}
		return nil
	}
	folder, ok := a.tree.Reveal(filepath.Join(dir, "internal"))
	if !ok {
		t.Fatal("setup: internal/ not in the tree")
	}
	file, ok := a.tree.Reveal(filepath.Join(dir, "main.go"))
	if !ok {
		t.Fatal("setup: main.go not in the tree")
	}

	a.openTreeContext(file, 5, 5)
	if find(contextOf(a).items, "Find files in folder…") != nil {
		t.Error("a file must not offer Find files in folder")
	}
	a.closeModal()

	a.openTreeContext(a.tree.Root, 5, 5)
	if find(contextOf(a).items, "Find file in project…") == nil {
		t.Error("the root row should offer the project finder")
	}
	a.closeModal()

	a.openTreeContext(folder, 5, 5)
	row := find(contextOf(a).items, "Find files in folder…")
	if row == nil {
		t.Fatal("a folder should offer Find files in folder")
	}
	a.closeModal()
	row.action(a, folder)
	if m := finderOf(a); m == nil || m.scope != "internal" {
		t.Fatalf("modal = %T, want the finder scoped to internal/", a.modal)
	}
}

// TestCtxFindFilesInFolder_IgnoresFiles: a file node reaching the action
// anyway opens nothing.
func TestCtxFindFilesInFolder_IgnoresFiles(t *testing.T) {
	a, dir := withFinder(t)
	ctxFindFilesInFolder(a, &filetree.Node{Path: filepath.Join(dir, "main.go")})
	if a.modal != nil {
		t.Errorf("a file node opened %T", a.modal)
	}
}

// TestMenuFindFilesInFolder_NamesTheActiveFolderOrExplains pins the ≡
// twin: with a subfolder active the label names it and the row opens the
// scoped finder; with none it stays clickable and flashes how to pick
// one.
func TestMenuFindFilesInFolder_NamesTheActiveFolderOrExplains(t *testing.T) {
	a, dir := withFinder(t)

	a.activeFolder = ""
	if got := a.findFilesInFolderLabel(); got != "Find files in folder" {
		t.Errorf("label with no folder = %q", got)
	}
	a.menuFindFilesInFolder()
	if a.modal != nil {
		t.Fatalf("with no folder, modal = %T, want a flash", a.modal)
	}
	if !strings.Contains(a.statusMsg, "folder in the tree") {
		t.Errorf("flash = %q, want the how-to", a.statusMsg)
	}

	a.activeFolder = filepath.Join(dir, "internal", "app")
	want := "Find files in folder (internal" + string(filepath.Separator) + "app" + string(filepath.Separator) + ")"
	if got := a.findFilesInFolderLabel(); got != want {
		t.Errorf("label = %q, want %q", got, want)
	}
	a.menuFindFilesInFolder()
	if m := finderOf(a); m == nil || m.scope != "internal/app" {
		t.Fatalf("modal = %T, want the finder scoped to internal/app", a.modal)
	}
}
