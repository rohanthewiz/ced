// =============================================================================
// File: internal/app/textpaste_test.go
// Author: Rohan Allison <rohanthewiz@gmail.com>
// Created: 2026-07-15
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

// TestPaste_IntoFocusedTerminalPrompt pins real-shell paste semantics in
// the panel: complete lines run IN ORDER, one at a time (never stacked —
// Eval is async), the unterminated tail is left at the prompt, and none of
// it touches the file behind the panel. The old keystroke-replay path got
// the first part accidentally right and everything else wrong.
func TestPaste_IntoFocusedTerminalPrompt(t *testing.T) {
	a := openBlankTab(t)
	f := openTestTerm(t, a)

	feedPaste(a, "cd /tmp\nls -la\necho done")

	// Line 1 runs; the rest wait their turn (Eval is async, so they must
	// NOT all be submitted at once).
	if evals := f.waitEvals(t, 1); evals[0] != "cd /tmp" {
		t.Fatalf("first Eval = %q, want %q", evals[0], "cd /tmp")
	}
	if got := a.term.input.String(); got != "ls -la" {
		t.Errorf("input while line 1 runs = %q, want the next line parked", got)
	}
	if got := len(a.term.pasteQueue); got != 1 {
		t.Errorf("pasteQueue = %d, want the final line still queued", got)
	}

	// Each completion walks the batch forward one command.
	a.handleTermDone(&termDoneEvent{when: time.Now()})
	if evals := f.waitEvals(t, 2); evals[1] != "ls -la" {
		t.Fatalf("second Eval = %q, want %q", evals[1], "ls -la")
	}
	a.handleTermDone(&termDoneEvent{when: time.Now()})

	// No trailing newline, so the last line is left at the prompt rather
	// than run — real-shell paste semantics.
	if got := a.term.input.String(); got != "echo done" {
		t.Errorf("input = %q, want the unterminated tail parked", got)
	}
	f.mu.Lock()
	n := len(f.evals)
	f.mu.Unlock()
	if n != 2 {
		t.Errorf("evals = %d, want 2 (the tail needs an Enter)", n)
	}
	if len(a.term.pasteQueue) != 0 {
		t.Errorf("pasteQueue = %v, want drained", a.term.pasteQueue)
	}
	if got := a.activeTabPtr().Buffer.String(); got != "" {
		t.Errorf("paste leaked into the editor buffer: %q", got)
	}
}

// TestPaste_TerminalTrailingNewlineRuns pins the difference a trailing
// break makes: with one, the last line runs too and the prompt is left
// empty. This is also the one-line case — pasting "go test ./...\n" runs
// it, exactly as pressing Enter on it would.
func TestPaste_TerminalTrailingNewlineRuns(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	f := openTestTerm(t, a)

	feedPaste(a, "go test ./...\n")

	if evals := f.waitEvals(t, 1); evals[0] != "go test ./..." {
		t.Errorf("Eval = %q, want %q", evals[0], "go test ./...")
	}
	if got := a.term.input.String(); got != "" {
		t.Errorf("input = %q, want empty after the line ran", got)
	}
}

// TestPaste_TerminalNoBreakDoesNotRun pins the other half of the rule: a
// paste carrying no line break is just text arriving at the caret, and
// joins whatever is already there. Nothing runs without a break or an
// Enter.
func TestPaste_TerminalNoBreakDoesNotRun(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	f := openTestTerm(t, a)
	typeTermLine(a, "echo ")

	feedPaste(a, "hello")

	if got := a.term.input.String(); got != "echo hello" {
		t.Errorf("input = %q, want the paste appended at the caret", got)
	}
	f.mu.Lock()
	n := len(f.evals)
	f.mu.Unlock()
	if n != 0 {
		t.Errorf("evals = %d, want 0 — a break-free paste runs nothing", n)
	}
}

// TestPaste_TerminalFirstLineJoinsInput pins that the paste's first line
// continues the command already being typed, the way a real terminal
// paste does: "echo " + "hello\n" submits "echo hello", not two pieces.
func TestPaste_TerminalFirstLineJoinsInput(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	f := openTestTerm(t, a)
	typeTermLine(a, "echo ")

	feedPaste(a, "hello\nls")

	if evals := f.waitEvals(t, 1); evals[0] != "echo hello" {
		t.Errorf("Eval = %q, want %q", evals[0], "echo hello")
	}
	if got := a.term.input.String(); got != "ls" {
		t.Errorf("input = %q, want %q", got, "ls")
	}
}

// TestPaste_TerminalBlockFeedsContinuation pins that a pasted multi-line
// block goes through grsh's NeedsMore loop exactly like a typed one: the
// lines accumulate and evaluate as ONE unit, not three broken commands.
func TestPaste_TerminalBlockFeedsContinuation(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	f := openTestTerm(t, a)
	f.needsMore = func(src string) bool { return !strings.Contains(src, "}") }

	feedPaste(a, "if true {\n\techo hi\n}\n")

	evals := f.waitEvals(t, 1)
	if want := "if true {\n echo hi\n}"; evals[0] != want {
		t.Errorf("Eval = %q, want the block as one unit %q", evals[0], want)
	}
	if a.term.pending != nil {
		t.Errorf("pending = %v, want cleared once the unit evaluated", a.term.pending)
	}
}

// TestPaste_TerminalInterruptDropsQueue is the safety pin: ⏹ has to mean
// the REST of a pasted batch never runs. Without it, stopping line 1 of a
// three-line paste would just hand the shell line 2.
func TestPaste_TerminalInterruptDropsQueue(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	f := openTestTerm(t, a)
	f.interruptOK = true

	feedPaste(a, "sleep 30\nrm -rf important\necho gone\n")
	f.waitEvals(t, 1)
	if len(a.term.pasteQueue) == 0 {
		t.Fatal("expected the rest of the batch to be queued")
	}

	a.termInterrupt()
	if len(a.term.pasteQueue) != 0 {
		t.Fatalf("⏹ left %v queued", a.term.pasteQueue)
	}

	// The completion that follows an interrupt must not resurrect it.
	a.handleTermDone(&termDoneEvent{when: time.Now()})
	f.mu.Lock()
	n := len(f.evals)
	f.mu.Unlock()
	if n != 1 {
		t.Errorf("evals = %d, want 1 — the interrupted batch resumed", n)
	}
}

// TestPaste_TerminalExitDropsQueue pins the same rule for a batch whose
// shell ends under it: the remaining lines were meant for THAT session,
// and a fresh one is not where they should land.
func TestPaste_TerminalExitDropsQueue(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	f := openTestTerm(t, a)

	prev := termExitCode
	termExitCode = func(error) (int, bool) { return 0, true }
	t.Cleanup(func() { termExitCode = prev })

	feedPaste(a, "exit\necho after\n")
	f.waitEvals(t, 1)

	a.handleTermDone(&termDoneEvent{when: time.Now(), err: errors.New("exit 0")})
	if len(a.term.pasteQueue) != 0 {
		t.Errorf("exit left %v queued", a.term.pasteQueue)
	}
	if a.term.sess != nil {
		t.Error("exit should drop the session")
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

// TestTermPasteClip_CmdV drives Cmd+V through the real key path to pin
// gesture equivalence: the two paste gestures share one entry point, so
// Cmd+V runs a complete pasted line exactly as a terminal paste does. The
// old path dropped breaks instead, gluing the tail of one line onto the
// head of the next ("cd foo" + "ls" read as "cd fools").
func TestTermPasteClip_CmdV(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	f := openTestTerm(t, a)
	a.clipBuf, a.clipKind = "cd foo\nls", clipText

	a.handleKey(tcell.NewEventKey(tcell.KeyRune, 'v', tcell.ModMeta))

	if evals := f.waitEvals(t, 1); evals[0] != "cd foo" {
		t.Errorf("Eval = %q, want %q", evals[0], "cd foo")
	}
	if got, want := a.term.input.String(), "ls"; got != want {
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

// TestPaste_TerminalCRLFAndTabs pins the normalization the terminal path
// does before it runs anything: CRLF is one break (not two, which would
// submit a blank line between every command), a lone CR is a break too,
// and a tab inside a line survives as a space rather than as a rune the
// field can't draw.
func TestPaste_TerminalCRLFAndTabs(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	f := openTestTerm(t, a)

	feedPaste(a, "echo\tone\r\necho two\r")

	evals := f.waitEvals(t, 1)
	if evals[0] != "echo one" {
		t.Errorf("first Eval = %q, want the tab as a space", evals[0])
	}
	a.handleTermDone(&termDoneEvent{when: time.Now()})
	if evals = f.waitEvals(t, 2); evals[1] != "echo two" {
		t.Errorf("second Eval = %q, want the lone CR to end line 2", evals[1])
	}
	// Two lines, two Evals — a CRLF must not have submitted a blank
	// third one in between.
	if len(evals) != 2 {
		t.Errorf("evals = %v, want exactly two", evals)
	}
}

// TestPaste_TerminalStopWithNothingRunning covers the queue's other exit:
// a paste whose first line needs no Eval (a blank line, or a block grsh is
// still accumulating) leaves the rest waiting with `running` false. ⏹ has
// to drop it there too, and say so rather than claiming an interrupt.
func TestPaste_TerminalStopWithNothingRunning(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	f := openTestTerm(t, a)
	f.needsMore = func(string) bool { return true } // never complete

	feedPaste(a, "if true {\n\techo hi\n}\n")
	if a.term.running {
		t.Fatal("an incomplete unit should not be running")
	}
	if len(a.term.pending) == 0 {
		t.Fatal("expected the pasted block to be accumulating")
	}

	a.termInterrupt()
	if len(a.term.pasteQueue) != 0 {
		t.Errorf("⏹ left %v queued", a.term.pasteQueue)
	}
	if strings.Contains(a.statusMsg, "interrupt sent") {
		t.Errorf("statusMsg = %q, want no claim of an interrupt", a.statusMsg)
	}
}
