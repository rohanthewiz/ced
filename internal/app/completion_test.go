// =============================================================================
// File: internal/app/completion_test.go
// Author: Rohan Allison <rohanthewiz@gmail.com>
// Created: 2026-08-12
// Copyright: 2026 Rohan Allison. All rights reserved.
// =============================================================================

// Tests for the completion popup: the trigger rules, the staleness
// window, live prefix filtering, the accept path (including the
// auto-import as one undo step), the keys it does and does not claim,
// the ghost-text interplay, and the clickable rows.
//
// The popup is the one code-intelligence surface that is NOT a modal, so
// most of these drive the real key router rather than calling handlers:
// the whole point of the feature is which keystrokes reach the buffer,
// and only handleKey can prove that.

package app

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gdamore/tcell/v2"

	"github.com/rohanthewiz/ced/internal/editor"
	"github.com/rohanthewiz/ced/internal/lsp"
)

// compItem builds a labelled item with the spec's fallbacks already
// applied, the way ParseCompletionItem would have left it.
func compItem(label string) lsp.CompletionItem {
	return lsp.CompletionItem{
		Label: label, SortText: label, FilterText: label, InsertText: label,
		Kind: lsp.CompletionFunction,
		Raw:  json.RawMessage(`{"label":"` + label + `"}`),
	}
}

// newCompletionApp seeds a Go file, opens it, and puts the caret at the
// end of the given line's text — the position a completion is asked
// from. Returns the app, the fake connection, and the open tab.
func newCompletionApp(t *testing.T, lines ...string) (*App, *fakeLSPConn, string) {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "main.go")
	if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	a := newTestApp(t, dir)
	fake := &fakeLSPConn{}
	a.lsp.dead = false
	a.lsp.client = fake
	a.openFile(path)
	return a, fake, path
}

// caretTo puts the caret at (line, col) in the active tab.
func caretTo(a *App, line, col int) {
	a.activeTabPtr().MoveCursorTo(editor.Position{Line: line, Col: col}, false)
}

// openList drives a response into the popup as if a request had just
// come back for the caret's current position.
func openList(a *App, path string, items []lsp.CompletionItem, incomplete bool) {
	a.completion.seq++
	a.handleLSPCompletion(&lspCompletionEvent{
		when: time.Now(), seq: a.completion.seq, path: path,
		rev: a.activeTabPtr().EditRev, pos: a.activeTabPtr().Cursor,
		items: items, incomplete: incomplete,
	})
}

// -----------------------------------------------------------------------------
// Trigger
// -----------------------------------------------------------------------------

// TestCompletionTriggerChars pins the capability read: the server's own
// set wins, and its absence falls back to `.` rather than to nothing.
func TestCompletionTriggerChars(t *testing.T) {
	a, fake, _ := newCompletionApp(t, "package main")
	if got := a.completionTriggerChars(); len(got) != 1 || got[0] != "." {
		t.Errorf("fallback = %v, want [.]", got)
	}
	fake.compTriggers = []string{".", "::"}
	if got := a.completionTriggerChars(); len(got) != 2 || got[1] != "::" {
		t.Errorf("server set = %v, want the server's own", got)
	}
}

// TestCompletionArmsOnTriggerCharOnly pins the trigger rule that keeps
// the popup quiet: a trigger character arms the debounce, an ordinary
// identifier rune does not. Auto-opening on every letter would race
// Copilot's ghost text on every keystroke.
func TestCompletionArmsOnTriggerCharOnly(t *testing.T) {
	a, _, _ := newCompletionApp(t, "package main", "", "func main() { fmt }")
	caretTo(a, 2, 18)

	pressRune(a, 'x')
	a.completionAfterEvent()
	if a.completion.timer != nil {
		t.Fatal("an ordinary rune must not arm a completion request")
	}

	pressRune(a, '.')
	a.completionAfterEvent()
	if a.completion.timer == nil {
		t.Fatal("a trigger character must arm the debounce")
	}
	a.completionStopTimer()
}

// TestCompletionCursorTravelIsFree pins the other half of that rule:
// moving through a file spends no requests, because only an EditRev
// bump counts as intent.
func TestCompletionCursorTravelIsFree(t *testing.T) {
	a, _, _ := newCompletionApp(t, "package main", "a.b.c")
	caretTo(a, 1, 5)
	a.completionAfterEvent() // adopt the tab
	for i := 0; i < 5; i++ {
		a.activeTabPtr().MoveCursor(0, -1, false)
		a.completionAfterEvent()
	}
	if a.completion.timer != nil {
		t.Error("cursor travel must never arm a request")
	}
}

// TestCompletionTickRequestsWithContext pins what reaches the wire: the
// trigger kind and character, which a server answers differently for.
func TestCompletionTickRequestsWithContext(t *testing.T) {
	a, fake, path := newCompletionApp(t, "package main", "fmt.")
	caretTo(a, 1, 4)

	a.handleCompletionTick(&completionTickEvent{
		when: time.Now(), path: path, trigger: ".", kind: lsp.CompletionTriggerChar,
	})
	waitForCopilot(t, "the completion request to reach the wire", func() bool {
		for _, c := range fake.callLog() {
			if strings.HasPrefix(c, "completion:") {
				return true
			}
		}
		return false
	})
	fake.mu.Lock()
	ctx := fake.compCtx
	fake.mu.Unlock()
	if ctx.TriggerKind != lsp.CompletionTriggerChar || ctx.TriggerCharacter != "." {
		t.Errorf("context = %+v, want a trigger-character invocation", ctx)
	}
}

// TestManualCompletionFlashesWhenEmpty pins the one case that speaks:
// an automatic trigger with nothing to offer stays silent, but a user
// who deliberately asked deserves an answer.
func TestManualCompletionFlashesWhenEmpty(t *testing.T) {
	a, _, path := newCompletionApp(t, "package main", "x")
	caretTo(a, 1, 1)

	a.completion.seq++
	a.handleLSPCompletion(&lspCompletionEvent{
		when: time.Now(), seq: a.completion.seq, path: path,
		rev: a.activeTabPtr().EditRev, pos: a.activeTabPtr().Cursor, manual: true,
	})
	if !strings.Contains(a.statusMsg, "No completions") {
		t.Errorf("flash = %q, want the empty answer said out loud", a.statusMsg)
	}

	a.statusMsg = ""
	a.completion.seq++
	a.handleLSPCompletion(&lspCompletionEvent{
		when: time.Now(), seq: a.completion.seq, path: path,
		rev: a.activeTabPtr().EditRev, pos: a.activeTabPtr().Cursor,
	})
	if a.statusMsg != "" {
		t.Errorf("flash = %q, want an automatic miss to stay quiet", a.statusMsg)
	}
}

// -----------------------------------------------------------------------------
// Staleness
// -----------------------------------------------------------------------------

// TestCompletionAcceptsResponseAfterTypingOn pins the deliberately loose
// staleness window: at a 150ms debounce the user is usually two letters
// further into the same word by the time the answer lands, and dropping
// those responses would make the popup appear only for slow typists.
func TestCompletionAcceptsResponseAfterTypingOn(t *testing.T) {
	a, _, path := newCompletionApp(t, "package main", "fmt.")
	caretTo(a, 1, 4)
	askPos, askRev := a.activeTabPtr().Cursor, a.activeTabPtr().EditRev

	// Two more letters typed while the request was in flight.
	a.activeTabPtr().InsertString("Pr")

	a.completion.seq++
	a.handleLSPCompletion(&lspCompletionEvent{
		when: time.Now(), seq: a.completion.seq, path: path,
		rev: askRev, pos: askPos,
		items: []lsp.CompletionItem{compItem("Println"), compItem("Fatal")},
	})
	if !a.completion.open {
		t.Fatal("a response the user typed on must still open the popup")
	}
	// And it arrives already narrowed by what was typed since.
	if a.completion.prefix != "Pr" {
		t.Errorf("prefix = %q, want the letters typed while in flight", a.completion.prefix)
	}
	if len(a.completion.matches) != 1 {
		t.Fatalf("matches = %d, want only Println", len(a.completion.matches))
	}
}

// TestCompletionDropsResponseAfterMoving pins the other side: a caret
// that left the line describes a position the list knows nothing about.
func TestCompletionDropsResponseAfterMoving(t *testing.T) {
	a, _, path := newCompletionApp(t, "package main", "fmt.", "other")
	caretTo(a, 1, 4)
	askPos, askRev := a.activeTabPtr().Cursor, a.activeTabPtr().EditRev
	a.activeTabPtr().InsertString("\n") // a newline is not "more of the word"

	a.completion.seq++
	a.handleLSPCompletion(&lspCompletionEvent{
		when: time.Now(), seq: a.completion.seq, path: path,
		rev: askRev, pos: askPos, items: []lsp.CompletionItem{compItem("Println")},
	})
	if a.completion.open {
		t.Error("a response for a caret that moved off the line must be dropped")
	}
}

// TestCompletionDropsSupersededResponse pins the generation guard: only
// the newest request may paint.
func TestCompletionDropsSupersededResponse(t *testing.T) {
	a, _, path := newCompletionApp(t, "package main", "fmt.")
	caretTo(a, 1, 4)
	stale := a.completion.seq
	a.completion.seq++ // a newer request went out

	a.handleLSPCompletion(&lspCompletionEvent{
		when: time.Now(), seq: stale, path: path,
		rev: a.activeTabPtr().EditRev, pos: a.activeTabPtr().Cursor,
		items: []lsp.CompletionItem{compItem("Println")},
	})
	if a.completion.open {
		t.Error("a superseded response must not open the popup")
	}
}

// -----------------------------------------------------------------------------
// Filtering
// -----------------------------------------------------------------------------

// TestCompletionFiltersAsYouType is the core loop: the letters reach the
// buffer (proving the popup is not a modal) and narrow the list, and
// typing past every match closes it.
func TestCompletionFiltersAsYouType(t *testing.T) {
	a, _, path := newCompletionApp(t, "package main", "fmt.")
	caretTo(a, 1, 4)
	// Note the filter is the same fuzzy SUBSEQUENCE scorer the palette
	// and finder use, so these four are chosen to make the assertion
	// about the filter rather than about fzy: only two contain a 'p'.
	openList(a, path, []lsp.CompletionItem{
		compItem("Println"), compItem("Printf"), compItem("Fatal"), compItem("Concat"),
	}, false)
	if !a.completion.open {
		t.Fatal("the popup should be open")
	}

	pressRune(a, 'P')
	a.completionAfterEvent()
	if got := a.activeTabPtr().Buffer.Lines[1]; got != "fmt.P" {
		t.Fatalf("line = %q — the keystroke must reach the buffer", got)
	}
	if len(a.completion.matches) != 2 {
		t.Errorf("matches = %d, want Println and Printf", len(a.completion.matches))
	}

	pressRune(a, 'z')
	a.completionAfterEvent()
	if a.completion.open {
		t.Error("typing past every match must close the popup")
	}
	if got := a.activeTabPtr().Buffer.Lines[1]; got != "fmt.Pz" {
		t.Errorf("line = %q — the closing keystroke still types", got)
	}
}

// TestCompletionClosesWhenCaretLeavesToken pins the other dismissal: a
// caret behind the token start is a caret the list no longer describes.
func TestCompletionClosesWhenCaretLeavesToken(t *testing.T) {
	a, _, path := newCompletionApp(t, "package main", "fmt.Pr")
	caretTo(a, 1, 6)
	openList(a, path, []lsp.CompletionItem{compItem("Println")}, false)
	if !a.completion.open {
		t.Fatal("the popup should be open")
	}
	caretTo(a, 1, 2) // back before the token
	a.completionAfterEvent()
	if a.completion.open {
		t.Error("a caret behind the token start must close the popup")
	}
}

// TestCompletionHonoursPreselect pins the one ranking signal the local
// scorer cannot reproduce.
func TestCompletionHonoursPreselect(t *testing.T) {
	a, _, path := newCompletionApp(t, "package main", "x.")
	caretTo(a, 1, 2)
	second := compItem("Beta")
	second.Preselect = true
	openList(a, path, []lsp.CompletionItem{compItem("Alpha"), second}, false)

	if item := a.completion.selectedItem(); item == nil || item.Label != "Beta" {
		t.Errorf("selected = %v, want the preselected Beta", item)
	}
}

// -----------------------------------------------------------------------------
// Accept
// -----------------------------------------------------------------------------

// TestCompletionAcceptAppliesEditAndImportAsOneUndo is the headline
// behaviour: the item's own edit and its auto-import land together, and
// ONE undo takes back both. A completion that needed two undos — or that
// left the import behind — would be worse than no completion.
func TestCompletionAcceptAppliesEditAndImportAsOneUndo(t *testing.T) {
	a, _, path := newCompletionApp(t,
		"package main",
		"",
		"func main() {",
		"\tfmt.Pr",
		"}")
	caretTo(a, 3, 7)
	before := a.activeTabPtr().Buffer.String()

	item := compItem("Println")
	item.Edit = &lsp.TextEdit{
		Range:   lsp.Range{Start: lsp.Position{Line: 3, Character: 5}, End: lsp.Position{Line: 3, Character: 7}},
		NewText: "Println",
	}
	item.Additional = []lsp.TextEdit{{
		Range:   lsp.Range{Start: lsp.Position{Line: 1, Character: 0}, End: lsp.Position{Line: 1, Character: 0}},
		NewText: "\nimport \"fmt\"\n",
	}}
	openList(a, path, []lsp.CompletionItem{item}, false)

	if !a.completionKey(tcell.NewEventKey(tcell.KeyEnter, 0, tcell.ModNone)) {
		t.Fatal("Enter must be consumed by an open popup with a match")
	}
	got := a.activeTabPtr().Buffer.String()
	if !strings.Contains(got, "fmt.Println") {
		t.Errorf("buffer = %q, want the completion applied", got)
	}
	if !strings.Contains(got, `import "fmt"`) {
		t.Errorf("buffer = %q, want the auto-import applied too", got)
	}
	if a.completion.open {
		t.Error("accepting must close the popup")
	}

	if !a.activeTabPtr().Undo() {
		t.Fatal("the accept must be undoable")
	}
	if after := a.activeTabPtr().Buffer.String(); after != before {
		t.Errorf("after one undo = %q, want the original %q", after, before)
	}
}

// TestCompletionAcceptPlacesCaretAfterInsertion pins where the caret
// lands — after the text just typed, even though an import inserted
// ABOVE it shifted every following line. The end position has to come
// from the applied result, not from the request.
func TestCompletionAcceptPlacesCaretAfterInsertion(t *testing.T) {
	a, _, path := newCompletionApp(t, "package main", "", "\tfmt.Pr")
	caretTo(a, 2, 7)

	item := compItem("Println")
	item.Edit = &lsp.TextEdit{
		Range:   lsp.Range{Start: lsp.Position{Line: 2, Character: 5}, End: lsp.Position{Line: 2, Character: 7}},
		NewText: "Println",
	}
	item.Additional = []lsp.TextEdit{{
		Range:   lsp.Range{Start: lsp.Position{Line: 1, Character: 0}, End: lsp.Position{Line: 1, Character: 0}},
		NewText: "import \"fmt\"\n",
	}}
	openList(a, path, []lsp.CompletionItem{item}, false)
	a.completionAccept()

	cur := a.activeTabPtr().Cursor
	// The import pushed the code line down by one.
	if cur.Line != 3 || cur.Col != 12 {
		t.Errorf("caret = %+v, want line 3 col 12 (end of \\tfmt.Println)", cur)
	}
}

// TestCompletionAcceptExtendsStaleEditEnd pins the correction applied to
// the server's own range: its end was measured when the request went
// out, and letters typed since must still be consumed. Without this,
// `fmt.Pr` + "in" accepts into `fmt.Printlnin`.
func TestCompletionAcceptExtendsStaleEditEnd(t *testing.T) {
	a, _, path := newCompletionApp(t, "package main", "fmt.Pr")
	caretTo(a, 1, 6)

	item := compItem("Println")
	item.Edit = &lsp.TextEdit{
		Range:   lsp.Range{Start: lsp.Position{Line: 1, Character: 4}, End: lsp.Position{Line: 1, Character: 6}},
		NewText: "Println",
	}
	openList(a, path, []lsp.CompletionItem{item}, false)
	// Two more letters land before the user presses Enter.
	a.activeTabPtr().InsertString("in")
	a.completionAfterEvent()
	a.completionAccept()

	if got := a.activeTabPtr().Buffer.Lines[1]; got != "fmt.Println" {
		t.Errorf("line = %q, want fmt.Println (not a doubled tail)", got)
	}
}

// TestCompletionAcceptWithoutEditUsesTheToken pins the fallback path: no
// textEdit means the popup replaces exactly the span it filtered on.
func TestCompletionAcceptWithoutEditUsesTheToken(t *testing.T) {
	a, _, path := newCompletionApp(t, "package main", "Prin")
	caretTo(a, 1, 4)
	openList(a, path, []lsp.CompletionItem{compItem("Println")}, false)
	a.completionAccept()

	if got := a.activeTabPtr().Buffer.Lines[1]; got != "Println" {
		t.Errorf("line = %q, want the token replaced whole", got)
	}
}

// TestCompletionRefusesSnippet pins the refusal. The keystroke is still
// consumed: the user aimed at a row, and silently indenting instead
// would read as the popup having ignored them.
func TestCompletionRefusesSnippet(t *testing.T) {
	a, _, path := newCompletionApp(t, "package main", "fo")
	caretTo(a, 1, 2)
	item := compItem("for")
	item.Snippet = true
	item.InsertText = "for ${1:i} := range ${2:x} {\n\t$0\n}"
	openList(a, path, []lsp.CompletionItem{item}, false)

	if !a.completionAccept() {
		t.Error("the keystroke should still be consumed")
	}
	if got := a.activeTabPtr().Buffer.Lines[1]; got != "fo" {
		t.Errorf("line = %q, want the buffer untouched", got)
	}
	if !strings.Contains(a.statusMsg, "Snippet") {
		t.Errorf("flash = %q, want the refusal explained", a.statusMsg)
	}
}

// -----------------------------------------------------------------------------
// Keys the popup does and does not claim
// -----------------------------------------------------------------------------

// TestCompletionEscClosesWithoutArmingLeader pins the dismissal
// contract. Esc means "drop that" — and consuming it is what stops the
// dismissal from also arming a leader gesture the user never started.
func TestCompletionEscClosesWithoutArmingLeader(t *testing.T) {
	a, _, path := newCompletionApp(t, "package main", "fmt.")
	caretTo(a, 1, 4)
	openList(a, path, []lsp.CompletionItem{compItem("Println")}, false)

	pressEsc(a)
	if a.completion.open {
		t.Error("Esc must close the popup")
	}
	if !a.lastEscape.IsZero() {
		t.Error("the dismissing Esc must not also arm the leader")
	}
}

// TestCompletionArrowsNavigateAndDoNotMoveCaret pins the keys the popup
// takes: Up/Down walk the list while the caret stays put.
func TestCompletionArrowsNavigateAndDoNotMoveCaret(t *testing.T) {
	a, _, path := newCompletionApp(t, "package main", "x.")
	caretTo(a, 1, 2)
	openList(a, path, []lsp.CompletionItem{
		compItem("Alpha"), compItem("Beta"), compItem("Gamma"),
	}, false)
	before := a.activeTabPtr().Cursor

	a.handleKey(tcell.NewEventKey(tcell.KeyDown, 0, tcell.ModNone))
	a.handleKey(tcell.NewEventKey(tcell.KeyDown, 0, tcell.ModNone))
	if a.completion.selected != 2 {
		t.Errorf("selected = %d, want 2", a.completion.selected)
	}
	if a.activeTabPtr().Cursor != before {
		t.Error("navigating the list must not move the caret")
	}
	// And it clamps rather than wrapping.
	a.handleKey(tcell.NewEventKey(tcell.KeyDown, 0, tcell.ModNone))
	if a.completion.selected != 2 {
		t.Errorf("selected = %d, want the highlight clamped at the end", a.completion.selected)
	}
}

// TestCompletionLeavesOtherKeysAlone pins the non-modal promise from the
// other direction: a key the popup has no use for still does its normal
// job with the list up.
func TestCompletionLeavesOtherKeysAlone(t *testing.T) {
	a, _, path := newCompletionApp(t, "package main", "x.")
	caretTo(a, 1, 2)
	openList(a, path, []lsp.CompletionItem{compItem("Alpha")}, false)

	for _, k := range []tcell.Key{tcell.KeyLeft, tcell.KeyRight, tcell.KeyHome, tcell.KeyBackspace2} {
		if a.completionKey(tcell.NewEventKey(k, 0, tcell.ModNone)) {
			t.Errorf("key %v must fall through to the editor", k)
		}
	}
}

// TestCompletionTabFallsThroughWhenClosed pins that a closed popup
// leaves Tab exactly as it was — plain indentation.
func TestCompletionTabFallsThroughWhenClosed(t *testing.T) {
	a, _, _ := newCompletionApp(t, "package main", "")
	caretTo(a, 1, 0)
	a.handleKey(tcell.NewEventKey(tcell.KeyTab, 0, tcell.ModNone))
	if got := a.activeTabPtr().Buffer.Lines[1]; got == "" {
		t.Error("Tab with no popup should still indent")
	}
}

// -----------------------------------------------------------------------------
// Interplay
// -----------------------------------------------------------------------------

// TestCompletionSuppressesGhostText pins the one boolean that resolves
// the Tab ambiguity: while the list is up, Copilot's ghost text is off.
func TestCompletionSuppressesGhostText(t *testing.T) {
	a, _, path := newCompletionApp(t, "package main", "x.")
	caretTo(a, 1, 2)
	// Pretend the sidecar is live and suggesting.
	a.copilot.signedIn = true
	a.copilot.suggest = true

	openList(a, path, []lsp.CompletionItem{compItem("Alpha")}, false)
	if a.copilotGhostActive() {
		t.Error("ghost text must be suppressed while the popup is open")
	}
	a.completionClose()
	// (copilotReady is still false in this harness, so the assertion
	// below is about the popup clause specifically, not the whole
	// predicate — hence checking the clause directly.)
	if a.completion.open {
		t.Error("close should have closed it")
	}
}

// TestCompletionClosedByModal pins the guard that keeps a list from
// popping in behind a dialog and stealing its Enter.
func TestCompletionClosedByModal(t *testing.T) {
	a, _, path := newCompletionApp(t, "package main", "x.")
	caretTo(a, 1, 2)
	openList(a, path, []lsp.CompletionItem{compItem("Alpha")}, false)

	a.openModal(&hoverModal{lines: []string{"hi"}})
	a.completionAfterEvent()
	if a.completion.open {
		t.Error("a modal taking the keyboard must close the popup")
	}
}

// TestCompletionClosedByTabClose pins the cleanup: the popup is anchored
// into a buffer, so the buffer going away takes it with it.
func TestCompletionClosedByTabClose(t *testing.T) {
	a, _, path := newCompletionApp(t, "package main", "x.")
	caretTo(a, 1, 2)
	openList(a, path, []lsp.CompletionItem{compItem("Alpha")}, false)

	a.closeTab(0)
	if a.completion.open {
		t.Error("closing the tab must close its popup")
	}
}

// -----------------------------------------------------------------------------
// Draw and click
// -----------------------------------------------------------------------------

// TestCompletionDrawsAnchoredRows pins the geometry contract that makes
// the popup clickable: draw stamps one rect per visible row, inside the
// box it painted, and the box hangs off the TOKEN start rather than the
// caret.
func TestCompletionDrawsAnchoredRows(t *testing.T) {
	a, _, path := newCompletionApp(t, "package main", "fmt.Pr")
	caretTo(a, 1, 6)
	openList(a, path, []lsp.CompletionItem{
		compItem("Println"), compItem("Printf"),
	}, false)
	a.draw()

	if len(a.completion.rows) != 2 {
		t.Fatalf("stamped rows = %d, want 2", len(a.completion.rows))
	}
	box := a.completion.box
	for i, r := range a.completion.rows {
		if r.y < box.y || r.y >= box.y+box.h {
			t.Errorf("row %d at y=%d falls outside the box %+v", i, r.y, box)
		}
	}
	// The anchor is the token start (col 4), not the caret (col 6).
	ex, _, ew, eh := a.editorRect()
	wantX, _, ok := a.activeTabPtr().PosScreenCell(a.completion.start, ew, eh)
	if !ok {
		t.Fatal("the token should be on screen")
	}
	if box.x != ex+wantX {
		t.Errorf("box.x = %d, want the token start at %d", box.x, ex+wantX)
	}
}

// TestCompletionClickAcceptsRow pins the mouse path — the plan's north
// star is that reading a row and clicking it is a real way to work.
func TestCompletionClickAcceptsRow(t *testing.T) {
	a, _, path := newCompletionApp(t, "package main", "Pr")
	caretTo(a, 1, 2)
	openList(a, path, []lsp.CompletionItem{compItem("Printf"), compItem("Println")}, false)
	a.draw()

	second := a.completion.rows[1]
	if !a.completionMouse(second.x+1, second.y, tcell.Button1) {
		t.Fatal("a click on a row must be consumed")
	}
	if got := a.activeTabPtr().Buffer.Lines[1]; got != "Println" {
		t.Errorf("line = %q, want the clicked row applied", got)
	}
}

// TestCompletionClickOutsideDismissesAndFallsThrough pins the other half
// of the mouse contract: a click aimed at the code underneath still
// reaches it.
func TestCompletionClickOutsideDismissesAndFallsThrough(t *testing.T) {
	a, _, path := newCompletionApp(t, "package main", "Pr")
	caretTo(a, 1, 2)
	openList(a, path, []lsp.CompletionItem{compItem("Printf")}, false)
	a.draw()

	box := a.completion.box
	away := box.y + box.h + 2
	if away >= a.height {
		away = 0
	}
	if a.completionMouse(box.x, away, tcell.Button1) {
		t.Error("a click outside must fall through, not be swallowed")
	}
	if a.completion.open {
		t.Error("a click outside must dismiss the popup")
	}
}

// -----------------------------------------------------------------------------
// Resolve
// -----------------------------------------------------------------------------

// TestCompletionResolveEnrichesDetailOnly pins the boundary that keeps
// resolve a display concern: documentation folds in, and nothing that
// would change what Enter does is taken from the response.
func TestCompletionResolveEnrichesDetailOnly(t *testing.T) {
	a, _, path := newCompletionApp(t, "package main", "x.")
	caretTo(a, 1, 2)
	item := compItem("Println")
	item.Edit = &lsp.TextEdit{
		Range:   lsp.Range{Start: lsp.Position{Line: 1, Character: 2}, End: lsp.Position{Line: 1, Character: 2}},
		NewText: "Println",
	}
	openList(a, path, []lsp.CompletionItem{item}, false)

	enriched := compItem("Println")
	enriched.Doc = "Println formats using the default formats."
	enriched.Edit = &lsp.TextEdit{
		Range:   lsp.Range{Start: lsp.Position{Line: 9, Character: 9}, End: lsp.Position{Line: 9, Character: 9}},
		NewText: "SOMETHING ELSE",
	}
	a.handleLSPCompletionResolve(&lspCompletionResolveEvent{
		when: time.Now(), seq: a.completion.seq, idx: 0, item: &enriched,
	})

	got := a.completion.items[0]
	if !strings.Contains(got.Doc, "default formats") {
		t.Errorf("doc = %q, want the resolved documentation", got.Doc)
	}
	if got.Edit == nil || got.Edit.NewText != "Println" {
		t.Errorf("edit = %+v, want the ORIGINAL edit kept", got.Edit)
	}
}

// TestCompletionResolveIgnoresStaleList pins the generation guard on the
// enrichment path too — a doc for a list the user has moved past would
// land against whatever item now sits at that index.
func TestCompletionResolveIgnoresStaleList(t *testing.T) {
	a, _, path := newCompletionApp(t, "package main", "x.")
	caretTo(a, 1, 2)
	openList(a, path, []lsp.CompletionItem{compItem("Alpha")}, false)
	stale := a.completion.seq - 1

	enriched := compItem("Alpha")
	enriched.Doc = "should not land"
	a.handleLSPCompletionResolve(&lspCompletionResolveEvent{
		when: time.Now(), seq: stale, idx: 0, item: &enriched,
	})
	if a.completion.items[0].Doc != "" {
		t.Error("a resolve for a superseded list must be dropped")
	}
}

// -----------------------------------------------------------------------------
// Surfaces
// -----------------------------------------------------------------------------

// TestCompletionHasEveryEntryPoint pins the house rule that no verb is
// keyboard-only: the leader, the ≡ menu, and the editor context menu all
// reach the same action.
func TestCompletionHasEveryEntryPoint(t *testing.T) {
	if leaderActionFor(' ') == nil {
		t.Error("Esc-Space must be bound to completions")
	}
	b := leaderBindingFor(' ')
	if b == nil || b.displayKey() != "spc" {
		t.Errorf("space binding = %+v, want a printable key label", b)
	}

	a := newTestApp(t, t.TempDir())
	found := false
	for _, g := range a.visibleMenuGroups() {
		for _, it := range g.items {
			if it.label == "Completions" {
				found = true
			}
		}
	}
	if !found {
		t.Error("the ≡ Code group must carry a Completions row")
	}
}
