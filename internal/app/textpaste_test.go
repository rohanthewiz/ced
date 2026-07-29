// =============================================================================
// File: internal/app/textpaste_test.go
// Author: Rohan Allison <rohanthewiz@gmail.com>
// Created: 2026-07-15
// Copyright: 2026 Rohan Allison. All rights reserved.
// =============================================================================

package app

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gdamore/tcell/v2"
)

// feedPaste drives a bracketed paste through the real event path: the
// start marker, one key event per rune (newlines as KeyEnter, tabs as
// KeyTab — exactly what tcell reports for pasted content), then the end
// marker. It goes through handleKey so the `pasting` gate is exercised,
// not bypassed.
func feedPaste(a *App, text string) {
	a.handlePaste(tcell.NewEventPaste(true))
	for _, r := range text {
		switch r {
		case '\n':
			a.handleKey(tcell.NewEventKey(tcell.KeyEnter, '\r', tcell.ModNone))
		case '\t':
			a.handleKey(tcell.NewEventKey(tcell.KeyTab, '\t', tcell.ModNone))
		default:
			a.handleKey(tcell.NewEventKey(tcell.KeyRune, r, tcell.ModNone))
		}
	}
	a.handlePaste(tcell.NewEventPaste(false))
}

// openBlankTab seeds and opens an empty text file, returning the app with
// that tab active — the common fixture for the paste tests.
func openBlankTab(t *testing.T) *App {
	t.Helper()
	dir := t.TempDir()
	target := filepath.Join(dir, "paste.txt")
	if err := os.WriteFile(target, nil, 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	a := newTestApp(t, dir)
	a.openFile(target)
	if a.activeTabPtr() == nil {
		t.Fatal("expected an active tab after openFile")
	}
	return a
}

// TestPaste_PreservesLineFormatting is the regression pin for the report
// that pasted text lost its line formatting: a multi-line, indented blob
// must land in the buffer byte-for-byte, newlines and leading tabs intact.
func TestPaste_PreservesLineFormatting(t *testing.T) {
	a := openBlankTab(t)
	src := "func main() {\n\tfmt.Println(\"hi\")\n\tif x {\n\t\ty()\n\t}\n}"

	feedPaste(a, src)

	if got := a.activeTabPtr().Buffer.String(); got != src {
		t.Fatalf("paste mangled formatting:\n got %q\nwant %q", got, src)
	}
}

// TestPaste_TabStaysLiteral pins the core bug: a pasted tab must be
// inserted verbatim, NOT expanded to the buffer's IndentUnit. The
// contrast keystroke shows the old, non-paste path still expands a Tab to
// IndentUnit — so the difference is genuinely the paste gate, not a
// coincidence.
func TestPaste_TabStaysLiteral(t *testing.T) {
	a := openBlankTab(t)
	a.activeTabPtr().IndentUnit = "    " // 4 spaces

	feedPaste(a, "\tx")
	if got := a.activeTabPtr().Buffer.String(); got != "\tx" {
		t.Fatalf("pasted tab should stay literal; got %q, want %q", got, "\tx")
	}

	// A Tab typed OUTSIDE a paste still expands to IndentUnit — proves the
	// literal-tab behavior above is the paste path doing its job.
	a.handleKey(tcell.NewEventKey(tcell.KeyTab, '\t', tcell.ModNone))
	if got := a.activeTabPtr().Buffer.String(); got != "\tx    " {
		t.Fatalf("typed Tab should expand to IndentUnit; got %q", got)
	}
}

// TestPaste_SingleUndoStep verifies a whole paste collapses into one undo
// step (one InsertString), not one per character — undoing once must clear
// the entire paste.
func TestPaste_SingleUndoStep(t *testing.T) {
	a := openBlankTab(t)

	feedPaste(a, "line one\nline two\nline three")
	if a.activeTabPtr().Buffer.String() == "" {
		t.Fatal("paste inserted nothing")
	}

	a.activeTabPtr().Undo()
	if got := a.activeTabPtr().Buffer.String(); got != "" {
		t.Fatalf("one Undo should clear the whole paste; got %q", got)
	}
}

// TestPaste_EmptyPasteNoop confirms a paste that carries no content
// leaves the buffer and paste state untouched.
func TestPaste_EmptyPasteNoop(t *testing.T) {
	a := openBlankTab(t)
	a.activeTabPtr().InsertString("seed")

	a.handlePaste(tcell.NewEventPaste(true))
	a.handlePaste(tcell.NewEventPaste(false))

	if a.pasting {
		t.Fatal("pasting flag should be cleared after an empty paste")
	}
	if got := a.activeTabPtr().Buffer.String(); got != "seed" {
		t.Fatalf("empty paste changed the buffer: %q", got)
	}
}

// TestEditorPasteTarget_Gating checks that the paste target resolves to
// the active tab only when the editor truly owns the keyboard, and to nil
// when a modal, a focused terminal, or an empty workspace should swallow
// the paste instead.
func TestEditorPasteTarget_Gating(t *testing.T) {
	a := openBlankTab(t)
	tab := a.activeTabPtr()

	if a.editorPasteTarget() != tab {
		t.Fatal("editor should be the paste target with a plain active tab")
	}

	// A focused terminal owns the keyboard — paste must not divert here.
	a.term.open, a.term.focused = true, true
	if a.editorPasteTarget() != nil {
		t.Fatal("focused terminal should suppress the editor paste target")
	}
	a.term.open, a.term.focused = false, false

	// A modal owns the keyboard too.
	a.modal = &confirmModal{}
	if a.editorPasteTarget() != nil {
		t.Fatal("open modal should suppress the editor paste target")
	}
	a.modal = nil

	// No open tab: nothing to paste into.
	a.tabs = nil
	a.activeTab = -1
	if a.editorPasteTarget() != nil {
		t.Fatal("no active tab should yield a nil paste target")
	}
}

// TestPaste_NotGatedWhenModalOpen verifies that starting a paste while a
// modal owns the keyboard leaves `pasting` false, so the content is NOT
// diverted into the editor buffer (it flows to the modal as normal keys).
func TestPaste_NotGatedWhenModalOpen(t *testing.T) {
	a := openBlankTab(t)
	a.modal = &confirmModal{}

	a.handlePaste(tcell.NewEventPaste(true))
	if a.pasting {
		t.Fatal("paste must not arm accumulation while a modal is open")
	}
}

// TestPaste_IntoFocusedChatPrompt is the regression pin for "can't paste
// into the chat prompt": with the panel focused a terminal paste must
// land in the composer, flattened to one line, and must NOT touch the
// file sitting behind the panel.
func TestPaste_IntoFocusedChatPrompt(t *testing.T) {
	a := openBlankTab(t)
	a.chat.open, a.chat.focused = true, true

	feedPaste(a, "func main() {\n\tfmt.Println(\"hi\")\n}")

	want := "func main() {  fmt.Println(\"hi\") }"
	if got := a.chat.input.String(); got != want {
		t.Errorf("chat prompt = %q, want %q", got, want)
	}
	if got := a.activeTabPtr().Buffer.String(); got != "" {
		t.Errorf("paste leaked into the editor buffer: %q", got)
	}
}

// TestPaste_ChatPromptInsertsAtCaret confirms a paste splices in at the
// caret rather than appending — the composer behaves like any other text
// field under paste.
func TestPaste_ChatPromptInsertsAtCaret(t *testing.T) {
	a := openBlankTab(t)
	a.chat.open, a.chat.focused = true, true
	a.chat.input = newTextField("explain:  <- here")
	a.chat.input.cursor = len("explain: ")

	feedPaste(a, "the bug")

	want := "explain: the bug <- here"
	if got := a.chat.input.String(); got != want {
		t.Errorf("chat prompt = %q, want %q", got, want)
	}
}

// TestPaste_ChatTargetGating checks the focus rules for the chat paste
// target: only a focused open panel claims a paste, and the
// keyboard-owning surfaces outrank it exactly as they do the editor.
// The editor target must stay mutually exclusive with it, or one paste
// could land in two places.
func TestPaste_ChatTargetGating(t *testing.T) {
	a := openBlankTab(t)

	if a.chatPasteTarget() {
		t.Error("closed chat must not be the paste target")
	}
	a.chat.open = true
	if a.chatPasteTarget() {
		t.Error("an open but unfocused chat must not claim the paste")
	}
	a.chat.focused = true
	if !a.chatPasteTarget() {
		t.Error("focused chat should be the paste target")
	}
	if a.editorPasteTarget() != nil {
		t.Error("focused chat must suppress the editor paste target")
	}

	a.modal = &confirmModal{}
	if a.chatPasteTarget() {
		t.Error("an open modal outranks the chat paste target")
	}
	a.modal = nil

	a.findOpen = true
	if a.chatPasteTarget() {
		t.Error("the find bar outranks the chat paste target")
	}
	a.findOpen = false

	a.menuOpen = true
	if a.chatPasteTarget() {
		t.Error("an open menu outranks the chat paste target")
	}
}

// TestPaste_ChatPromptWithNoTabOpen pins that the chat paste path does
// not depend on the editor having anything open — the earlier code
// resolved a paste through the active tab, so an empty workspace was a
// second way to lose the text.
func TestPaste_ChatPromptWithNoTabOpen(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	a.chat.open, a.chat.focused = true, true

	feedPaste(a, "no tabs here")

	if got := a.chat.input.String(); got != "no tabs here" {
		t.Errorf("chat prompt = %q, want %q", got, "no tabs here")
	}
}

// TestPaste_IntoFocusedTerminalPrompt is the regression pin for the old
// terminal behavior: a paste replayed as keystrokes meant every Enter it
// carried RAN the line before it. A multi-line paste must now land in the
// command line, flattened to one line, with nothing submitted.
func TestPaste_IntoFocusedTerminalPrompt(t *testing.T) {
	a := openBlankTab(t)
	a.term.open, a.term.focused = true, true

	feedPaste(a, "cd /tmp\nls -la\necho done")

	want := "cd /tmp ls -la echo done"
	if got := a.term.input.String(); got != want {
		t.Errorf("terminal input = %q, want %q", got, want)
	}
	if len(a.term.history) != 0 {
		t.Errorf("paste submitted commands: history = %v", a.term.history)
	}
	if a.term.running {
		t.Error("paste started a command; nothing should run without Enter")
	}
	if got := a.activeTabPtr().Buffer.String(); got != "" {
		t.Errorf("paste leaked into the editor buffer: %q", got)
	}
	// The panel says the shape changed — an unreviewed Enter here would
	// run something the user never typed.
	if !strings.Contains(a.statusMsg, "3 lines") {
		t.Errorf("statusMsg = %q, want a multi-line paste notice", a.statusMsg)
	}
}

// TestPaste_TerminalSingleLineNoFlash pins that the notice is reserved
// for pastes that actually got flattened: the common case (one command,
// with or without its trailing newline) must stay quiet.
func TestPaste_TerminalSingleLineNoFlash(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	a.term.open, a.term.focused = true, true

	feedPaste(a, "go test ./...\n")

	if got := a.term.input.String(); got != "go test ./... " {
		t.Errorf("terminal input = %q", got)
	}
	if a.statusMsg != "" {
		t.Errorf("statusMsg = %q, want no notice for a one-line paste", a.statusMsg)
	}
}

// TestPaste_TerminalTargetGating checks the terminal's focus rules and,
// more importantly, that the three paste targets are mutually exclusive:
// a focused chat panel outranks the terminal (handleKey's tiebreak), and
// the keyboard-owning surfaces outrank both.
func TestPaste_TerminalTargetGating(t *testing.T) {
	a := openBlankTab(t)

	if a.termPasteTarget() {
		t.Error("closed terminal must not be the paste target")
	}
	a.term.open = true
	if a.termPasteTarget() {
		t.Error("an open but unfocused terminal must not claim the paste")
	}
	a.term.focused = true
	if !a.termPasteTarget() {
		t.Error("focused terminal should be the paste target")
	}
	if a.editorPasteTarget() != nil {
		t.Error("focused terminal must suppress the editor paste target")
	}

	// Both panels focused at once shouldn't happen (the click handlers
	// keep the flags exclusive), but the tiebreak must still be one
	// target, not two.
	a.chat.open, a.chat.focused = true, true
	if a.termPasteTarget() {
		t.Error("chat should win the tiebreak over the terminal")
	}
	if !a.chatPasteTarget() {
		t.Error("chat should be the paste target when both are focused")
	}
	a.chat.open, a.chat.focused = false, false

	a.modal = &confirmModal{}
	if a.termPasteTarget() {
		t.Error("an open modal outranks the terminal paste target")
	}
	a.modal = nil

	a.findOpen = true
	if a.termPasteTarget() {
		t.Error("the find bar outranks the terminal paste target")
	}
	a.findOpen = false

	a.menuOpen = true
	if a.termPasteTarget() {
		t.Error("an open menu outranks the terminal paste target")
	}
}

// TestTermPasteClip_CmdV drives Cmd+V through the real key path and pins
// the mangling it used to do: dropping newlines outright glued the last
// word of one line onto the first of the next ("cd foo" + "ls" read as
// "cd fools"). Breaks must become spaces.
func TestTermPasteClip_CmdV(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	a.term.open, a.term.focused = true, true
	a.clipBuf, a.clipKind = "cd foo\nls", clipText

	a.handleKey(tcell.NewEventKey(tcell.KeyRune, 'v', tcell.ModMeta))

	if got, want := a.term.input.String(), "cd foo ls"; got != want {
		t.Errorf("terminal input = %q, want %q", got, want)
	}
}

// TestFlattenPaste covers the single-line paste policy both panels share:
// breaks and tabs collapse to one space each, a CRLF pair counts once,
// and other control runes are dropped.
func TestFlattenPaste(t *testing.T) {
	cases := []struct{ in, want string }{
		{"", ""},
		{"plain text", "plain text"},
		{"a\nb", "a b"},
		{"a\r\nb", "a b"},
		{"a\rb", "a b"},
		{"if x {\n\ty()\n}", "if x {  y() }"},
		{"bell\a and nul\x00", "bell and nul"},
		{"trailing\n", "trailing "},
	}
	for _, c := range cases {
		if got := flattenPaste(c.in); got != c.want {
			t.Errorf("flattenPaste(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

// TestPasteLineCount pins the count behind the terminal's notice: a
// trailing break doesn't invent a line, and CRLF counts once.
func TestPasteLineCount(t *testing.T) {
	cases := []struct {
		in   string
		want int
	}{
		{"", 0},
		{"\n", 0},
		{"one line", 1},
		{"one line\n", 1},
		{"a\nb", 2},
		{"a\r\nb\r\n", 2},
		{"a\nb\nc", 3},
	}
	for _, c := range cases {
		if got := pasteLineCount(c.in); got != c.want {
			t.Errorf("pasteLineCount(%q) = %d, want %d", c.in, got, c.want)
		}
	}
}
