// =============================================================================
// File: internal/app/gitpushreceipt_test.go
// Author: Rohan Allison <rohanthewiz@gmail.com>
// Created: 2026-09-30
// Copyright: 2026 Rohan Allison. All rights reserved.
// =============================================================================

// Tests for the post-push receipt: git's output reaches the shared
// receipt panel on success and only on success, the output is cleaned
// before it is drawn, and a push receipt never inherits a commit's hash.
// The dismissal contract itself is pinned in gitcommitreceipt_test.go —
// it is the same panel.

package app

import (
	"path/filepath"
	"strings"
	"testing"
)

// samplePushOutput is what `git push` prints to a non-terminal for an
// ordinary fast-forward: the destination, then the ref table.
const samplePushOutput = "To github.com:rohanthewiz/ced.git\n   ad6f060..9b1c2e4  main -> main\n"

// TestPushReceipt_OpensAndDraws pins the happy path end to end through
// the done-event: a successful push hands its output to the hook, and
// the title, the branch line and git's own ref table are all on screen.
func TestPushReceipt_OpensAndDraws(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	a.handleGitCmdDone(&gitCmdDoneEvent{
		label: "Push main → origin/main", output: []byte(samplePushOutput),
		onOKOutput: pushReceiptHook("main → origin/main"),
	})
	if !a.commitReceiptOpen() {
		t.Fatal("a successful push should open the receipt")
	}
	a.draw()
	a.screen.Show()
	txt := screenText(a)
	for _, want := range []string{"Pushed", "main → origin/main", "To github.com:rohanthewiz/ced.git", "ad6f060..9b1c2e4  main -> main"} {
		if !strings.Contains(txt, want) {
			t.Errorf("push receipt missing %q\n%s", want, txt)
		}
	}
	if strings.Contains(txt, "Committed") {
		t.Error("push receipt drew the commit receipt's title")
	}
}

// TestPushReceipt_FailureKeepsTheModal pins that a failed push never
// opens the passive panel — the info modal with git's error is the whole
// answer, and a receipt would read as "it worked".
func TestPushReceipt_FailureKeepsTheModal(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	a.handleGitCmdDone(&gitCmdDoneEvent{
		label: "Push main → origin/main", err: errReceiptFake,
		output:     []byte("! [rejected]        main -> main (fetch first)"),
		onOKOutput: pushReceiptHook("main → origin/main"),
	})
	if a.commitReceiptOpen() {
		t.Error("a failed push must not open the receipt")
	}
	if a.modal == nil {
		t.Error("a failed push should still open the error modal")
	}
	a.closeModal()
}

// TestPushReceipt_DeclinesOccupiedScreen pins the passive rule shared
// with the commit receipt: under a modal the panel would expire unseen.
func TestPushReceipt_DeclinesOccupiedScreen(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	a.openInfo("Something", []string{"already on screen"})
	a.showPushReceipt("main → origin/main", []byte(samplePushOutput))
	if a.commitReceiptOpen() {
		t.Error("push receipt must decline the screen while a modal owns it")
	}
	a.closeModal()
}

// TestPushReceipt_ReplacesACommitReceipt pins the commit-then-push
// sequence: the push receipt takes the panel over whole, and the commit
// hash does not survive as its headline.
func TestPushReceipt_ReplacesACommitReceipt(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	a.handleGitCommitReceipt(receiptEvent(receiptHash, "feat: a thing"))
	a.showPushReceipt("main → origin/main", []byte(samplePushOutput))
	if a.commitReceipt.hash != "" {
		t.Errorf("push receipt kept the commit hash %q", a.commitReceipt.hash)
	}
	if got := a.commitReceipt.headline(); got != "main → origin/main" {
		t.Errorf("headline = %q, want the push target", got)
	}
	a.closeCommitReceipt()
	if a.commitReceiptOpen() || a.commitReceipt.title != "" {
		t.Error("closing should clear the push receipt's title and head too")
	}
}

// TestPushReceiptBody_Cleans pins the cleaning rules: CR redraws keep
// their last frame, CRLF is not a redraw, ANSI color and tabs are gone,
// blank edges are trimmed and interior blanks survive.
func TestPushReceiptBody_Cleans(t *testing.T) {
	out := "\n\nremote: Resolving deltas: 10%\rremote: Resolving deltas: 100% (3/3), done.\r\n" +
		"remote:\n" +
		"remote: \x1b[32mCreate a pull request\x1b[0m\tfor 'x'\n" +
		"\n" +
		"To origin\n\n"
	got := pushReceiptBody([]byte(out), 68)
	want := []string{
		"remote: Resolving deltas: 100% (3/3), done.",
		"remote:",
		"remote: Create a pull request for 'x'",
		"",
		"To origin",
	}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Errorf("body = %q\nwant   %q", got, want)
	}
}

// TestPushReceiptBody_EmptyAndCapped pins the two edges: silent output
// still fills a row that says so, and a long remote banner is capped
// with the cut marked.
func TestPushReceiptBody_EmptyAndCapped(t *testing.T) {
	if got := pushReceiptBody([]byte("\n \n"), 68); len(got) != 1 || got[0] != "(git printed nothing)" {
		t.Errorf("empty output body = %q", got)
	}
	long := strings.Repeat("remote: line\n", commitReceiptMaxLines+5)
	got := pushReceiptBody([]byte(long), 68)
	if len(got) != commitReceiptMaxLines+1 || got[len(got)-1] != "…" {
		t.Errorf("long output: %d rows ending %q, want %d ending with …", len(got), got[len(got)-1], commitReceiptMaxLines+1)
	}
}

// TestStripReceiptControl pins the per-line sanitiser on its own:
// CSI sequences vanish whole, other controls vanish, text is untouched.
func TestStripReceiptControl(t *testing.T) {
	cases := map[string]string{
		"plain → text":           "plain → text",
		"\x1b[1;31mred\x1b[m":    "red",
		"bell\a here":            "bell here",
		"a\tb":                   "a b",
		"lone \x1bescape stays?": "lone escape stays?",
	}
	for in, want := range cases {
		if got := stripReceiptControl(in); got != want {
			t.Errorf("stripReceiptControl(%q) = %q, want %q", in, got, want)
		}
	}
}

// TestPushReceipt_EndToEnd drives the real chain against a LOCAL bare
// remote in a temp dir (initRepoWithRemote), never the network: the
// output-carrying runner → the done-event → the hook → the panel. It
// pins what no fixture can — that git's real push output survives the
// plumbing and names the ref that moved. It calls the runner directly
// rather than the dialog's submit, whose remote is the user's own.
func TestPushReceipt_EndToEnd(t *testing.T) {
	if !gitAvailable() {
		t.Skip("git not on PATH")
	}
	dir := initRepoWithRemote(t)
	writeFileT(t, filepath.Join(dir, "f.txt"), "two\n")
	gitRun(t, dir, "commit", "-q", "-am", "second")

	a := newTestApp(t, dir)
	a.rootDir = dir
	a.runGitCmdOKOutput("Push main → origin/main", pushReceiptHook("main → origin/main"),
		"push", "origin", "main:main")
	pumpAppEvents(t, a, func() bool { return a.commitReceiptOpen() })

	body := strings.Join(a.commitReceipt.lines, "\n")
	if !strings.Contains(body, "main -> main") {
		t.Errorf("receipt body = %q, want git's ref line", body)
	}
	if a.commitReceipt.title != "Pushed" {
		t.Errorf("title = %q, want Pushed", a.commitReceipt.title)
	}
}
