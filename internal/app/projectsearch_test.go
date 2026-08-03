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

	"github.com/rohanthewiz/ced/internal/search"
)

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
	hits, truncated := search.Project(root, paths, search.Options{Query: query})
	a.projectSearchSeq++
	a.handleProjectSearch(&projectSearchEvent{
		when: time.Now(), seq: a.projectSearchSeq,
		query: query, hits: hits, truncated: truncated,
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
	a.startProjectSearch("needle")
	if a.modal != nil {
		t.Fatalf("expected no list, got %T", a.modal)
	}
	if !strings.Contains(a.statusMsg, "index") {
		t.Fatalf("expected an indexing flash, got %q", a.statusMsg)
	}
}
