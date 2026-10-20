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

	"github.com/gdamore/tcell/v2"
)

// samplePushOutput is what `git push` prints to a non-terminal for an
// ordinary fast-forward: the destination, then the ref table.
const samplePushOutput = "To github.com:rohanthewiz/ced.git\n   ad6f060..9b1c2e4  main -> main\n"

// samplePRPushOutput is what GitHub prints for the first push of a
// branch: the remote's pull-request link, then git's own report. The URL
// line is longer than the commit receipt's 72-column body on purpose.
const samplePRPushOutput = "remote: \n" +
	"remote: Create a pull request for 'feature/new-branch-menu' on GitHub by visiting:        \n" +
	"remote:      https://github.com/rohanthewiz/ced/pull/new/feature/new-branch-menu        \n" +
	"remote: \n" +
	"To https://github.com/rohanthewiz/ced.git\n" +
	" * [new branch]      feature/new-branch-menu -> feature/new-branch-menu\n" +
	"branch 'feature/new-branch-menu' set up to track 'origin/feature/new-branch-menu'.\n"

// samplePRLink is the link samplePRPushOutput carries.
const samplePRLink = "https://github.com/rohanthewiz/ced/pull/new/feature/new-branch-menu"

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
	got := pushReceiptBody([]byte(out), 68, 50)
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
	if got := pushReceiptBody([]byte("\n \n"), 68, commitReceiptMaxLines); len(got) != 1 || got[0] != "(git printed nothing)" {
		t.Errorf("empty output body = %q", got)
	}
	long := strings.Repeat("remote: line\n", commitReceiptMaxLines+5)
	got := pushReceiptBody([]byte(long), 68, commitReceiptMaxLines)
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

// TestPushReceiptLink pins which URL counts as the remote's link: the
// first one on a "remote:" line, trailing prose punctuation trimmed —
// and never the "To <url>" line, which names the remote itself.
func TestPushReceiptLink(t *testing.T) {
	cases := []struct {
		name, out, want string
	}{
		{"github PR", samplePRPushOutput, samplePRLink},
		{"gitlab MR", "remote: To create a merge request for x, visit:\nremote:   https://gitlab.com/o/r/-/merge_requests/new?merge_request%5Bsource_branch%5D=x\nTo gitlab.com:o/r.git\n",
			"https://gitlab.com/o/r/-/merge_requests/new?merge_request%5Bsource_branch%5D=x"},
		{"punctuation", "remote: See https://example.com/x.\n", "https://example.com/x"},
		{"clone url only", "To https://github.com/o/r.git\n   a..b  main -> main\n", ""},
		{"no link", samplePushOutput, ""},
	}
	for _, c := range cases {
		if got := pushReceiptLink(pushReceiptLines([]byte(c.out))); got != c.want {
			t.Errorf("%s: link = %q, want %q", c.name, got, c.want)
		}
	}
}

// TestPushReceiptWidth pins the sizing rule: never under the commit
// receipt's width, wide enough for the longest line plus frame, never
// past the window.
func TestPushReceiptWidth(t *testing.T) {
	if got := pushReceiptWidth([]string{"short"}, 200); got != commitReceiptWidth {
		t.Errorf("short output width = %d, want %d", got, commitReceiptWidth)
	}
	long := strings.Repeat("x", 100)
	if got := pushReceiptWidth([]string{"a", long}, 200); got != 104 {
		t.Errorf("long line width = %d, want 104", got)
	}
	if got := pushReceiptWidth([]string{long}, 90); got != 90 {
		t.Errorf("clamped width = %d, want the window's 90", got)
	}
}

// TestPushReceiptMaxLines pins the window-derived cap and its floor.
func TestPushReceiptMaxLines(t *testing.T) {
	if got := pushReceiptMaxLines(40); got != 32 {
		t.Errorf("40-row window: %d rows, want 32", got)
	}
	if got := pushReceiptMaxLines(5); got != 3 {
		t.Errorf("tiny window: %d rows, want the floor of 3", got)
	}
}

// TestPushReceipt_ShowsEverythingAndKeepsTheLinkWhole pins the point of
// the push receipt: output longer than the commit receipt's 12-line cap
// is shown in full when the window has room, and the PR URL is drawn on
// ONE row (the panel widens rather than hard-wrapping it).
func TestPushReceipt_ShowsEverythingAndKeepsTheLinkWhole(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	var b strings.Builder
	for i := 0; i < commitReceiptMaxLines+4; i++ {
		b.WriteString("remote: hook says line " + itoa(i) + "\n")
	}
	b.WriteString(samplePRPushOutput)
	a.showPushReceipt("feature/new-branch-menu → origin/feature/new-branch-menu", []byte(b.String()))

	body := strings.Join(a.commitReceipt.lines, "\n")
	if strings.Contains(body, "…") {
		t.Errorf("output was capped in a window with room for it:\n%s", body)
	}
	if !strings.Contains(body, "hook says line 0") || !strings.Contains(body, "set up to track") {
		t.Errorf("first or last line missing:\n%s", body)
	}
	a.draw()
	a.screen.Show()
	if txt := screenText(a); !strings.Contains(txt, samplePRLink) {
		t.Errorf("PR link not drawn whole on one row\n%s", txt)
	}
}

// TestPushReceipt_LinkButtons pins the two buttons end to end through
// handleMouse: offered only when the remote printed a link, Copy puts
// the link on the clipboard (the flash names it), Open hands it to the
// desktop opener — and either click closes the receipt. A receipt with
// buttons gets the longer window.
func TestPushReceipt_LinkButtons(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	a.showPushReceipt("main → origin/main", []byte(samplePushOutput))
	if _, _, ok := a.commitReceiptLinkBtns(); ok {
		t.Fatal("a push without a link offers link buttons")
	}

	a.showPushReceipt("x → origin/x", []byte(samplePRPushOutput))
	if got := a.commitReceiptWindow(); got != commitReceiptPushFor {
		t.Errorf("link receipt window = %v, want %v", got, commitReceiptPushFor)
	}
	a.draw()
	a.screen.Show()
	txt := screenText(a)
	if !strings.Contains(txt, receiptOpenLinkLabel) || !strings.Contains(txt, receiptCopyLinkLabel) {
		t.Fatalf("link buttons not drawn\n%s", txt)
	}
	_, cp, _ := a.commitReceiptLinkBtns()
	a.handleMouse(tcell.NewEventMouse(cp.x+1, cp.y, tcell.Button1, tcell.ModNone))
	a.handleMouse(tcell.NewEventMouse(cp.x+1, cp.y, tcell.ButtonNone, tcell.ModNone))
	if a.commitReceiptOpen() {
		t.Error("the receipt should close when Copy link is clicked")
	}
	// OSC 52 needs /dev/tty, which a test run may not have; either the
	// success flash naming the link or the copy helper's own failure
	// proves the Copy path ran (fileops_test.go takes the same view).
	if !strings.Contains(a.statusMsg, samplePRLink) && !strings.HasPrefix(a.statusMsg, "Copy failed") {
		t.Errorf("statusMsg = %q, want the copy flash", a.statusMsg)
	}

	calls := recordHostRun(t, "", nil)
	withHost(t, "darwin", nil)
	a.showPushReceipt("x → origin/x", []byte(samplePRPushOutput))
	a.draw()
	open, _, _ := a.commitReceiptLinkBtns()
	a.handleMouse(tcell.NewEventMouse(open.x+1, open.y, tcell.Button1, tcell.ModNone))
	c := nextHostCall(t, calls)
	if strings.Join(c.argv, " ") != "open "+samplePRLink {
		t.Errorf("argv = %q, want open <link>", c.argv)
	}
	if a.commitReceiptOpen() {
		t.Error("the receipt should close when Open link is clicked")
	}
}

// TestBrowserArgv covers the opener on both platforms and the no-desktop
// refusal, which must point at the button that does work over SSH.
func TestBrowserArgv(t *testing.T) {
	withHost(t, "darwin", nil)
	if argv, _ := browserArgv("https://x"); strings.Join(argv, " ") != "open https://x" {
		t.Errorf("darwin argv = %q", argv)
	}
	withHost(t, "linux", nil)
	if _, reason := browserArgv("https://x"); !strings.Contains(reason, "Copy link") {
		t.Errorf("no-desktop reason = %q, want it to point at Copy link", reason)
	}
	withHost(t, "linux", map[string]string{"DISPLAY": ":0"})
	if argv, _ := browserArgv("https://x"); strings.Join(argv, " ") != "xdg-open https://x" {
		t.Errorf("linux argv = %q", argv)
	}
}

// TestMenuGitLastPush pins the way back to a receipt: the row is dim
// until a push succeeds, the output is remembered even when the panel
// declined an occupied screen, and the row reopens the whole receipt —
// link buttons included.
func TestMenuGitLastPush(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	if a.hasLastPush() {
		t.Fatal("no push yet, but the row is enabled")
	}
	a.openInfo("Something", []string{"already on screen"})
	a.showPushReceipt("x → origin/x", []byte(samplePRPushOutput))
	if a.commitReceiptOpen() {
		t.Fatal("the receipt should still decline an occupied screen")
	}
	a.closeModal()
	if !a.hasLastPush() {
		t.Fatal("a declined receipt's output should still be remembered")
	}
	a.menuGitLastPush()
	if !a.commitReceiptOpen() || a.commitReceipt.title != "Pushed" {
		t.Fatalf("receipt open=%v title=%q, want the push receipt", a.commitReceiptOpen(), a.commitReceipt.title)
	}
	if a.commitReceipt.link != samplePRLink {
		t.Errorf("reopened link = %q, want %q", a.commitReceipt.link, samplePRLink)
	}
}
