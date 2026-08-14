// =============================================================================
// File: internal/app/lspreferences_test.go
// Author: Rohan Allison <rohanthewiz@gmail.com>
// Created: 2026-08-03
// Copyright: 2026 Rohan Allison. All rights reserved.
// =============================================================================

package app

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/rohanthewiz/ced/internal/editor"
	"github.com/rohanthewiz/ced/internal/lsp"
)

// refLocAt builds a single-line reference location for path.
func refLocAt(path string, line, startChar, endChar int) lsp.Location {
	return lsp.Location{
		URI: lsp.PathToURI(path),
		Range: lsp.Range{
			Start: lsp.Position{Line: line, Character: startChar},
			End:   lsp.Position{Line: line, Character: endChar},
		},
	}
}

// TestCursorWord pins what the panel is titled and tinted with: the
// identifier under the cursor, a single-line selection when there is one,
// and nothing at all from punctuation or a multi-line blob.
func TestCursorWord(t *testing.T) {
	mk := func(text string, cur editor.Position) *editor.Tab {
		tab := &editor.Tab{Buffer: editor.NewBuffer(text)}
		tab.Cursor, tab.Anchor = cur, cur
		return tab
	}

	tab := mk("total := count + 1\n", editor.Position{Line: 0, Col: 11})
	if got := cursorWord(tab); got != "count" {
		t.Errorf("word under cursor = %q, want %q", got, "count")
	}

	// Punctuation is not a symbol — refusing beats asking gopls about a
	// plus sign and reporting "no references".
	tab = mk("total := count + 1\n", editor.Position{Line: 0, Col: 15})
	if got := cursorWord(tab); got != "" {
		t.Errorf("word on punctuation = %q, want empty", got)
	}

	// A single-line selection is the narrower statement of intent.
	tab = mk("alpha beta\n", editor.Position{Line: 0, Col: 0})
	tab.Anchor = editor.Position{Line: 0, Col: 0}
	tab.Cursor = editor.Position{Line: 0, Col: 5}
	if got := cursorWord(tab); got != "alpha" {
		t.Errorf("selection = %q, want %q", got, "alpha")
	}

	// A multi-line selection falls through to the word under the cursor
	// instead of titling the panel with a blob containing newlines.
	tab = mk("alpha\nbeta\n", editor.Position{Line: 0, Col: 0})
	tab.Anchor = editor.Position{Line: 0, Col: 0}
	tab.Cursor = editor.Position{Line: 1, Col: 2}
	if got := cursorWord(tab); got != "beta" {
		t.Errorf("multi-line selection = %q, want the word under the cursor %q", got, "beta")
	}
}

// TestCollectRefLines_OrdersCapsAndReads pins the goroutine half: hits
// come back sorted by path/line/column, the on-disk line rides along as
// context, and a location whose URI names no file is dropped rather than
// producing a row nothing can open.
func TestCollectRefLines_OrdersCapsAndReads(t *testing.T) {
	dir := t.TempDir()
	aPath := filepath.Join(dir, "a.go")
	bPath := filepath.Join(dir, "b.go")
	if err := os.WriteFile(aPath, []byte("one\ntwo count\nthree\n"), 0644); err != nil {
		t.Fatalf("seed a: %v", err)
	}
	if err := os.WriteFile(bPath, []byte("count here\n"), 0644); err != nil {
		t.Fatalf("seed b: %v", err)
	}

	locs := []lsp.Location{
		refLocAt(bPath, 0, 0, 5),
		refLocAt(aPath, 1, 4, 9),
		refLocAt(aPath, 0, 0, 3),
		{URI: "jdt://contents/rt.jar", Range: lsp.Range{}}, // not a file
	}
	refs, truncated := collectRefLines(locs)
	if truncated {
		t.Error("four hits should not truncate")
	}
	if len(refs) != 3 {
		t.Fatalf("got %d refs, want 3 (the non-file URI dropped)", len(refs))
	}
	// Sorted by path, then line.
	want := []struct {
		path string
		line int
		text string
	}{
		{aPath, 0, "one"},
		{aPath, 1, "two count"},
		{bPath, 0, "count here"},
	}
	for i, w := range want {
		if refs[i].path != w.path || refs[i].line != w.line || refs[i].text != w.text {
			t.Errorf("refs[%d] = %s:%d %q, want %s:%d %q",
				i, filepath.Base(refs[i].path), refs[i].line, refs[i].text,
				filepath.Base(w.path), w.line, w.text)
		}
	}
}

// TestCollectRefLines_Truncates pins the cap and the flag that reports
// it: a silently short list reads as "that's all of them", which is the
// one wrong answer a results panel can give.
func TestCollectRefLines_Truncates(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "big.go")
	var b strings.Builder
	for i := range maxReferences + 10 {
		fmt.Fprintf(&b, "count %d\n", i)
	}
	if err := os.WriteFile(path, []byte(b.String()), 0644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	locs := make([]lsp.Location, 0, maxReferences+10)
	for i := range maxReferences + 10 {
		locs = append(locs, refLocAt(path, i, 0, 5))
	}
	refs, truncated := collectRefLines(locs)
	if !truncated {
		t.Error("over the cap should report truncated")
	}
	if len(refs) != maxReferences {
		t.Errorf("got %d refs, want the cap %d", len(refs), maxReferences)
	}
	// The cap keeps a PREFIX of the sorted order, so the list the user
	// reads starts where the file starts.
	if refs[0].line != 0 {
		t.Errorf("first kept ref is line %d, want 0", refs[0].line)
	}
}

// TestCollectRefLines_MissingFileKeepsRow pins the degradation: a file
// that can't be read costs its rows their context, not their existence.
// The location is the answer; the line is decoration.
func TestCollectRefLines_MissingFileKeepsRow(t *testing.T) {
	gone := filepath.Join(t.TempDir(), "vanished.go")
	refs, _ := collectRefLines([]lsp.Location{refLocAt(gone, 3, 0, 4)})
	if len(refs) != 1 {
		t.Fatalf("got %d refs, want the hit kept", len(refs))
	}
	if refs[0].text != "" {
		t.Errorf("text = %q, want empty for an unreadable file", refs[0].text)
	}
}

// TestReferenceHits_BufferBeatsDisk pins the reconciliation that is the
// whole reason row building runs on the main loop: gopls answered from
// the text ced synced to it, so an open tab's unsaved buffer — not the
// stale disk copy — is what those coordinates were measured against.
func TestReferenceHits_BufferBeatsDisk(t *testing.T) {
	a, _, goPath := newLSPTestApp(t)
	a.openFile(goPath)
	tab := a.activeTabPtr()
	tab.Buffer = editor.NewBuffer("package main\nvar counter = 1\n")

	hits := a.referenceHits([]refLoc{
		{path: goPath, line: 1, startChar: 4, endChar: 11, text: "STALE DISK LINE"},
	})
	if len(hits) != 1 {
		t.Fatalf("got %d hits, want 1", len(hits))
	}
	if hits[0].Text != "var counter = 1" {
		t.Errorf("text = %q, want the live buffer's line", hits[0].Text)
	}
	if hits[0].Col != 4 || hits[0].Width != 7 {
		t.Errorf("col/width = %d/%d, want 4/7", hits[0].Col, hits[0].Width)
	}
}

// TestReferenceHits_UTF16Columns pins the coordinate conversion: LSP
// counts UTF-16 code units, the editor counts runes, and a non-BMP rune
// on the line is where the two disagree. Getting this wrong lights up
// the wrong cells one column at a time.
func TestReferenceHits_UTF16Columns(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	// "🙂" is one rune, two UTF-16 units, so "count" starts at rune 2 but
	// UTF-16 column 3.
	line := "a🙂count"
	hits := a.referenceHits([]refLoc{
		{path: "/nowhere/x.go", line: 0, startChar: 3, endChar: 8, text: line},
	})
	if hits[0].Col != 2 || hits[0].Width != 5 {
		t.Errorf("col/width = %d/%d, want 2/5", hits[0].Col, hits[0].Width)
	}
}

// TestReferenceHits_MultiLineRangeClampsToLineEnd pins the sentinel: a
// range that spans lines reports refEndOfLine for its end, which RuneCol
// clamps against whichever text the row finally shows.
func TestReferenceHits_MultiLineRangeClampsToLineEnd(t *testing.T) {
	locs := []lsp.Location{{
		URI: lsp.PathToURI("/nowhere/x.go"),
		Range: lsp.Range{
			Start: lsp.Position{Line: 0, Character: 2},
			End:   lsp.Position{Line: 4, Character: 1},
		},
	}}
	refs, _ := collectRefLines(locs)
	if refs[0].endChar != refEndOfLine {
		t.Fatalf("endChar = %d, want the end-of-line sentinel", refs[0].endChar)
	}
	a := newTestApp(t, t.TempDir())
	refs[0].text = "0123456789"
	hits := a.referenceHits(refs)
	if hits[0].Col != 2 || hits[0].Width != 8 {
		t.Errorf("col/width = %d/%d, want 2/8 (to end of line)", hits[0].Col, hits[0].Width)
	}
}

// TestMenuFindReferences_FlushesAndRequests pins the request side: a
// pending edit is flushed before the question is asked (a symbol renamed
// a keystroke ago would otherwise report the OLD name's uses under the
// new name's title), and the declaration is always included.
func TestMenuFindReferences_FlushesAndRequests(t *testing.T) {
	a, fake, goPath := newLSPTestApp(t)
	a.openFile(goPath)
	tab := a.activeTabPtr()
	tab.Buffer = editor.NewBuffer("package main\nvar counter = 1\n")
	tab.EditRev++
	tab.MoveCursorTo(editor.Position{Line: 1, Col: 5}, false)

	a.menuFindReferences()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		calls := fake.callLog()
		if len(calls) > 0 && calls[len(calls)-1] == "references:main.go:true" {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}

	calls := fake.callLog()
	if len(calls) < 3 {
		t.Fatalf("calls = %v, want didOpen + didChange + references", calls)
	}
	if calls[1] != "didChange:main.go:2" {
		t.Errorf("calls[1] = %q, want the pre-request flush", calls[1])
	}
	if calls[2] != "references:main.go:true" {
		t.Errorf("calls[2] = %q, want references with includeDeclaration", calls[2])
	}
}

// TestMenuFindReferences_NoSymbolRefuses pins the guard: a cursor that
// isn't on an identifier says so instead of spending a round trip that
// is guaranteed to come back empty.
func TestMenuFindReferences_NoSymbolRefuses(t *testing.T) {
	a, fake, goPath := newLSPTestApp(t)
	a.openFile(goPath)
	tab := a.activeTabPtr()
	tab.Buffer = editor.NewBuffer("package main\n   \n")
	tab.MoveCursorTo(editor.Position{Line: 1, Col: 1}, false)

	before := len(fake.callLog())
	a.menuFindReferences()
	if got := len(fake.callLog()); got != before {
		t.Errorf("calls went from %d to %d, want no request", before, got)
	}
	if !strings.Contains(a.statusMsg, "cursor on a symbol") {
		t.Errorf("status = %q, want the no-symbol explanation", a.statusMsg)
	}
}

// TestFindReferences_MenuAndLeaderAgree pins the two surfaces against
// each other. The ≡ hint column is display-only — dispatch lives in the
// leader table — so a rebind that updated one and not the other would
// leave the menu telling the user a key that does something else.
func TestFindReferences_MenuAndLeaderAgree(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	if sc := menuItemByLabel(t, a, "Find references…").shortcut; sc != "esc R" {
		t.Errorf("menu shortcut = %q, want %q", sc, "esc R")
	}
	found := false
	for _, b := range leaderBindings() {
		if b.key == 'R' {
			found = true
			if b.sub != nil || b.subFor != nil {
				t.Error("'R' must be an action, not a namespace prefix")
			}
		}
	}
	if !found {
		t.Error("no leader binding on 'R'")
	}
}

// TestHandleLSPReferences_OpensPanel pins the landing: the result opens
// the Find-all panel in PROJECT mode (rows carry paths, walking them
// previews nothing) under a title naming the symbol.
func TestHandleLSPReferences_OpensPanel(t *testing.T) {
	a, _, goPath := newLSPTestApp(t)
	a.lsp.refSeq = 1
	a.handleLSPReferences(&lspReferencesEvent{
		when: time.Now(), seq: 1, query: "counter",
		locs: []refLoc{{path: goPath, line: 1, startChar: 4, endChar: 11, text: "var counter = 1"}},
	})
	m, ok := a.modal.(*findAllModal)
	if !ok {
		t.Fatalf("modal = %T, want *findAllModal", a.modal)
	}
	if !m.project {
		t.Error("references list must run in project mode — rows carry paths")
	}
	if got := m.titleText(); got != `References to "counter"` {
		t.Errorf("title = %q, want References to \"counter\"", got)
	}
	if len(m.rows) != 1 || m.rows[0].path != goPath {
		t.Fatalf("rows = %+v, want one row naming the file", m.rows)
	}
	if m.rows[0].label != "main.go:2" {
		t.Errorf("label = %q, want main.go:2", m.rows[0].label)
	}
	// The display order is derived from the rows, not implied by them: a
	// panel that skipped rebuildView would open saying every row had been
	// dismissed.
	if len(m.view) != len(m.rows) {
		t.Errorf("view = %d rows, want all %d showing", len(m.view), len(m.rows))
	}
	// And the filter box seeds with the symbol, like the two search lists —
	// inert until edited, so it hides nothing it just found.
	if got := m.filter.String(); got != "counter" {
		t.Errorf("filter = %q, want the symbol pre-filled", got)
	}
}

// TestHandleLSPReferences_TitleReportsTruncation pins the honesty
// clause the heading must not displace.
func TestHandleLSPReferences_TitleReportsTruncation(t *testing.T) {
	m := &findAllModal{
		query: "counter", project: true, heading: "References to", truncated: true,
		rows: make([]findAllRow, 3),
	}
	if got := m.titleText(); got != `References to "counter" — first 3` {
		t.Errorf("title = %q, want the truncation notice", got)
	}
}

// TestHandleLSPReferences_DropsStale pins the generation guard. This is
// the only code-intelligence verb whose answer OPENS a panel, so a reply
// to a question the user has moved on from must not land on top of what
// they are doing now.
func TestHandleLSPReferences_DropsStale(t *testing.T) {
	a, _, goPath := newLSPTestApp(t)
	a.lsp.refSeq = 2
	a.handleLSPReferences(&lspReferencesEvent{
		when: time.Now(), seq: 1, query: "old",
		locs: []refLoc{{path: goPath, line: 0, text: "x"}},
	})
	if a.modal != nil {
		t.Errorf("stale generation opened %T", a.modal)
	}
}

// TestHandleLSPReferences_EmptyAndError pin the two quiet answers: both
// flash and neither opens an empty panel.
func TestHandleLSPReferences_EmptyAndError(t *testing.T) {
	a, _, _ := newLSPTestApp(t)
	a.lsp.refSeq = 1

	a.handleLSPReferences(&lspReferencesEvent{when: time.Now(), seq: 1, query: "ghost"})
	if a.modal != nil {
		t.Fatalf("empty result opened %T", a.modal)
	}
	if !strings.Contains(a.statusMsg, "No references") {
		t.Errorf("status = %q, want the no-results message", a.statusMsg)
	}

	a.handleLSPReferences(&lspReferencesEvent{
		when: time.Now(), seq: 1, query: "x", err: fmt.Errorf("server busy"),
	})
	if a.modal != nil {
		t.Fatalf("error result opened %T", a.modal)
	}
	if !strings.Contains(a.statusMsg, "server busy") {
		t.Errorf("status = %q, want the server's reason", a.statusMsg)
	}
}

// TestHandleLSPReferences_YieldsTheModalSlot pins the politeness rule
// project search already keeps: a result arriving while a modal owns the
// screen reports its count rather than stealing the slot from something
// the user is typing into.
func TestHandleLSPReferences_YieldsTheModalSlot(t *testing.T) {
	a, _, goPath := newLSPTestApp(t)
	a.lsp.refSeq = 1
	a.openPrompt("Busy", "", "", func(*App, string) {})
	prompt := a.modal

	a.handleLSPReferences(&lspReferencesEvent{
		when: time.Now(), seq: 1, query: "counter",
		locs: []refLoc{{path: goPath, line: 0, text: "x"}},
	})
	if a.modal != prompt {
		t.Errorf("modal = %T, want the prompt left alone", a.modal)
	}
	if !strings.Contains(a.statusMsg, "run it again") {
		t.Errorf("status = %q, want the retry hint", a.statusMsg)
	}
}
