// =============================================================================
// File: internal/app/gitcommitreceipt_test.go
// Author: Rohan Allison <rohanthewiz@gmail.com>
// Created: 2026-08-24
// Copyright: 2026 Rohan Allison. All rights reserved.
// =============================================================================

// Tests for the post-commit receipt. Like the dwell tooltip, this is a
// box that appears over the user's code without being asked, so most of
// what is pinned here is the ways it must go away — and the one thing it
// must never do, which is cost the keystroke that dismissed it.

package app

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gdamore/tcell/v2"
)

// errReceiptFake stands in for a `git log` that exited nonzero.
var errReceiptFake = errors.New("git log failed")

// receiptEvent is the shape `git log -1 --format=%H%n%B` produces, built
// by hand so no test here has to fork git.
func receiptEvent(hash, msg string) *gitCommitReceiptEvent {
	return &gitCommitReceiptEvent{out: []byte(hash + "\n" + msg + "\n")}
}

const receiptHash = "0123456789abcdef0123456789abcdef01234567"

// TestCommitReceipt_OpensAndDraws pins the happy path: an arrived report
// opens the panel, and both facts it exists to show — the hash and the
// whole message, subject and body — are actually on screen.
func TestCommitReceipt_OpensAndDraws(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	a.handleGitCommitReceipt(receiptEvent(receiptHash, "feat: add a thing\n\nBecause the body matters too."))
	if !a.commitReceiptOpen() {
		t.Fatal("receipt should be open after a report arrives")
	}
	a.draw()
	a.screen.Show()
	txt := screenText(a)
	for _, want := range []string{"Committed", receiptHash, "feat: add a thing", "Because the body matters too."} {
		if !strings.Contains(txt, want) {
			t.Errorf("receipt panel missing %q\n%s", want, txt)
		}
	}
}

// TestCommitReceipt_BadReportStaysQuiet pins the silent-degradation
// contract from both directions: a failed `git log` and output that
// names no hash each cost the receipt and nothing else. The commit
// already succeeded and already flashed.
func TestCommitReceipt_BadReportStaysQuiet(t *testing.T) {
	a := newTestApp(t, t.TempDir())

	a.handleGitCommitReceipt(&gitCommitReceiptEvent{err: errReceiptFake, out: []byte("boom")})
	if a.commitReceiptOpen() {
		t.Error("a failed git log must not open a receipt")
	}
	a.handleGitCommitReceipt(receiptEvent("not-a-hash", "subject"))
	if a.commitReceiptOpen() {
		t.Error("output whose first line is not an object name must not open a receipt")
	}
}

// TestCommitReceipt_DeclinesOccupiedScreen pins the passive rule: this
// layer paints below the menu and modals, so a receipt opened under one
// would be invisible for its whole window and then expire unread.
func TestCommitReceipt_DeclinesOccupiedScreen(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	a.openInfo("Something", []string{"already on screen"})
	a.handleGitCommitReceipt(receiptEvent(receiptHash, "subject"))
	if a.commitReceiptOpen() {
		t.Error("receipt must decline the screen while a modal owns it")
	}

	a.closeModal()
	a.menuOpen = true
	a.handleGitCommitReceipt(receiptEvent(receiptHash, "subject"))
	if a.commitReceiptOpen() {
		t.Error("receipt must decline the screen while the menu is open")
	}
}

// TestCommitReceipt_KeyDismissesWithoutEatingIt is the contract that
// makes the panel safe to show unbidden: the next keystroke closes it
// AND still does what it always did — the ghost-text rule.
func TestCommitReceipt_KeyDismissesWithoutEatingIt(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "note.txt")
	if err := os.WriteFile(path, []byte("\n"), 0644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	a := newTestApp(t, dir)
	a.openFile(path)
	a.handleGitCommitReceipt(receiptEvent(receiptHash, "subject"))

	a.handleKey(tcell.NewEventKey(tcell.KeyRune, 'x', tcell.ModNone))
	if a.commitReceiptOpen() {
		t.Error("a keystroke should dismiss the receipt")
	}
	tab := a.activeTabPtr()
	if got := tab.Buffer.Lines[0]; got != "x" {
		t.Errorf("the dismissing keystroke was swallowed: buffer line = %q, want %q", got, "x")
	}
}

// TestCommitReceipt_PressInsideIsSwallowed pins the other half of the
// mouse contract: any press closes the panel, but one that landed on the
// panel itself must not also move the caret in code the user could not
// see. A press outside falls through as usual.
func TestCommitReceipt_PressInsideIsSwallowed(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	a.handleGitCommitReceipt(receiptEvent(receiptHash, "subject"))
	a.drawCommitReceipt()

	b := a.commitReceipt.box
	if b.w == 0 || b.h == 0 {
		t.Fatal("draw should stamp the panel rect")
	}
	if !a.commitReceiptContains(b.x+1, b.y+1) {
		t.Error("a cell inside the drawn panel should test as inside")
	}
	if a.commitReceiptContains(b.x-1, b.y-1) {
		t.Error("a cell outside the drawn panel should test as outside")
	}

	a.handleMouse(tcell.NewEventMouse(b.x+1, b.y+1, tcell.Button1, tcell.ModNone))
	if a.commitReceiptOpen() {
		t.Error("a press should dismiss the receipt")
	}

	// And once closed, the stamped rect must go with it — a leftover box
	// would swallow presses over a region that has no panel in it.
	a.drawCommitReceipt()
	if a.commitReceipt.box.w != 0 {
		t.Error("a closed receipt must clear its stamped rect")
	}
}

// TestCommitReceipt_ExpiryIsGenerationChecked pins why the expiry tick
// carries a seq: a second commit inside the window must not be closed
// early by the first one's timer.
func TestCommitReceipt_ExpiryIsGenerationChecked(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	a.handleGitCommitReceipt(receiptEvent(receiptHash, "first"))
	stale := a.commitReceipt.seq

	a.handleGitCommitReceipt(receiptEvent(receiptHash, "second"))
	a.handleCommitReceiptExpire(&commitReceiptExpireEvent{seq: stale})
	if !a.commitReceiptOpen() {
		t.Fatal("a stale expiry tick must not close the current receipt")
	}

	a.handleCommitReceiptExpire(&commitReceiptExpireEvent{seq: a.commitReceipt.seq})
	if a.commitReceiptOpen() {
		t.Error("the current expiry tick should close the receipt")
	}
}

// TestParseCommitReceipt covers the split and its refusals — the hash
// must look like an object name, and git's trailing newlines must not
// become blank rows the panel pays height for.
func TestParseCommitReceipt(t *testing.T) {
	hash, msg := parseCommitReceipt(receiptHash + "\nsubject\n\nbody\n\n\n")
	if hash != receiptHash {
		t.Errorf("hash = %q, want %q", hash, receiptHash)
	}
	if msg != "subject\n\nbody" {
		t.Errorf("msg = %q, want %q", msg, "subject\n\nbody")
	}
	if h, _ := parseCommitReceipt("HEAD -> main\nsubject\n"); h != "" {
		t.Errorf("a non-hash first line should refuse, got %q", h)
	}
	if h, _ := parseCommitReceipt(""); h != "" {
		t.Errorf("empty output should refuse, got %q", h)
	}
}

// TestCommitReceiptBody pins the wrapping rules: authored line structure
// survives (the subject never runs into the body), a long line wraps
// rather than being cut, a leading blank is dropped, and an over-long
// message is capped with the cut MARKED.
func TestCommitReceiptBody(t *testing.T) {
	lines := commitReceiptBody("subject here\n\na body line", 20)
	want := []string{"subject here", "", "a body line"}
	if len(lines) != len(want) {
		t.Fatalf("lines = %q, want %q", lines, want)
	}
	for i := range want {
		if lines[i] != want[i] {
			t.Errorf("line %d = %q, want %q", i, lines[i], want[i])
		}
	}

	long := commitReceiptBody(strings.Repeat("word ", 10), 20)
	if len(long) < 2 {
		t.Errorf("a long line should wrap, got %q", long)
	}
	for _, ln := range long {
		if runeLen(ln) > 20 {
			t.Errorf("wrapped line %q exceeds the width", ln)
		}
	}

	if got := commitReceiptBody("\n\nsubject", 20); len(got) != 1 || got[0] != "subject" {
		t.Errorf("a leading blank should be dropped, got %q", got)
	}

	huge := commitReceiptBody(strings.Repeat("line\n", commitReceiptMaxLines*2), 20)
	if len(huge) != commitReceiptMaxLines+1 {
		t.Fatalf("capped body = %d lines, want %d", len(huge), commitReceiptMaxLines+1)
	}
	if huge[len(huge)-1] != "…" {
		t.Errorf("the cut must be marked, last line = %q", huge[len(huge)-1])
	}

	if got := commitReceiptBody("", 20); len(got) != 1 || got[0] != "(no message)" {
		t.Errorf("an empty message should still fill a row, got %q", got)
	}
}

// TestGitCmdDone_OnOKHook pins the plumbing the receipt hangs off: the
// success hook runs on a clean exit and never on a failure, where the
// error modal is the whole answer.
func TestGitCmdDone_OnOKHook(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	ran := 0
	hook := func(*App) { ran++ }

	a.handleGitCmdDone(&gitCmdDoneEvent{label: "Commit", onOK: hook})
	if ran != 1 {
		t.Errorf("onOK ran %d times on success, want 1", ran)
	}

	a.handleGitCmdDone(&gitCmdDoneEvent{label: "Commit", err: errReceiptFake, output: []byte("nope"), onOK: hook})
	if ran != 1 {
		t.Errorf("onOK ran on a FAILED command (%d times)", ran)
	}
	a.closeModal()
}

// TestCommitReceipt_EndToEnd drives the whole chain against a real
// repository: gitCommitFiles → the sequence runner → the success hook →
// `git log -1` → the panel. It is the one test here that forks git, and
// it is what pins the two facts nothing else can check — that the hook
// survives the plumbing, and that the hash on screen is the commit git
// actually wrote.
func TestCommitReceipt_EndToEnd(t *testing.T) {
	if !gitAvailable() {
		t.Skip("git not on PATH")
	}
	repo := initRepo(t)
	if err := os.WriteFile(filepath.Join(repo, "f.txt"), []byte("x\n"), 0644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	gitRun(t, repo, "add", ".")

	a := newTestApp(t, repo)
	a.rootDir = repo
	a.gitCommitFiles(nil, "feat: the receipt\n\nAnd a body line.")
	pumpAppEvents(t, a, func() bool { return a.commitReceiptOpen() })

	if want := gitOut(t, repo, "rev-parse", "HEAD"); a.commitReceipt.hash != want {
		t.Errorf("receipt hash = %q, want %q", a.commitReceipt.hash, want)
	}
	body := strings.Join(a.commitReceipt.lines, "\n")
	if !strings.Contains(body, "feat: the receipt") || !strings.Contains(body, "And a body line.") {
		t.Errorf("receipt body = %q, want the whole message", body)
	}
}

// TestCommitReceipt_PushButtonOnlyWithATarget pins when the receipt
// offers a push: exactly when the git panel's Actions picker would list
// its "Push <branch>…" row (a remote and a branch). Without one the
// panel keeps its old height and draws no button, and a push receipt
// replacing a commit receipt must not inherit the commit's button.
func TestCommitReceipt_PushButtonOnlyWithATarget(t *testing.T) {
	a := newTestApp(t, t.TempDir())

	// No remote: no button, and the rect is the button-less height.
	a.gitIsRepo, a.gitBranch = true, "main"
	a.handleGitCommitReceipt(receiptEvent(receiptHash, "subject"))
	if _, ok := a.commitReceiptPushBtn(); ok {
		t.Error("no remote, yet the receipt offers a push")
	}
	_, _, _, plainH := a.commitReceiptRect()

	// Remote + branch: the button is offered, drawn, and costs one row.
	a.gitHasRemote = true
	a.handleGitCommitReceipt(receiptEvent(receiptHash, "subject"))
	b, ok := a.commitReceiptPushBtn()
	if !ok {
		t.Fatal("a repo with a remote should offer the push button")
	}
	if _, _, _, h := a.commitReceiptRect(); h != plainH+1 {
		t.Errorf("height with button = %d, want %d", h, plainH+1)
	}
	a.draw()
	a.screen.Show()
	if !strings.Contains(screenText(a), "[ Push main… ]") {
		t.Errorf("push button not drawn\n%s", screenText(a))
	}
	if !a.commitReceiptPushHit(b.x, b.y) || a.commitReceiptPushHit(b.x-1, b.y) {
		t.Error("hit-test disagrees with the button's geometry")
	}

	// Detached HEAD (no branch): nothing to name, so no button.
	a.gitBranch = ""
	a.handleGitCommitReceipt(receiptEvent(receiptHash, "subject"))
	if _, ok := a.commitReceiptPushBtn(); ok {
		t.Error("detached HEAD, yet the receipt offers a push")
	}

	// A push receipt replaces a commit receipt whole — button included.
	a.gitBranch = "main"
	a.handleGitCommitReceipt(receiptEvent(receiptHash, "subject"))
	a.showPushReceipt("main → origin/main", []byte("Everything up-to-date\n"))
	if _, ok := a.commitReceiptPushBtn(); ok {
		t.Error("the push receipt inherited the commit receipt's push button")
	}
}

// TestCommitReceipt_PushOfferGetsTheLongerWindow pins the window length:
// a commit receipt offering "[ Push <branch>… ]" stays up for
// commitReceiptPushFor so the push isn't a race, while a receipt without
// the button (no remote, or a push receipt replacing it) keeps the plain
// commitReceiptFor.
func TestCommitReceipt_PushOfferGetsTheLongerWindow(t *testing.T) {
	if commitReceiptPushFor <= commitReceiptFor {
		t.Fatalf("push window %v should be longer than the plain %v",
			commitReceiptPushFor, commitReceiptFor)
	}
	a := newTestApp(t, t.TempDir())

	a.gitIsRepo, a.gitBranch = true, "main"
	a.handleGitCommitReceipt(receiptEvent(receiptHash, "subject"))
	if got := a.commitReceiptWindow(); got != commitReceiptFor {
		t.Errorf("no push offered: window = %v, want %v", got, commitReceiptFor)
	}

	a.gitHasRemote = true
	a.handleGitCommitReceipt(receiptEvent(receiptHash, "subject"))
	if got := a.commitReceiptWindow(); got != commitReceiptPushFor {
		t.Errorf("push offered: window = %v, want %v", got, commitReceiptPushFor)
	}

	a.showPushReceipt("main → origin/main", []byte("Everything up-to-date\n"))
	if got := a.commitReceiptWindow(); got != commitReceiptFor {
		t.Errorf("push receipt: window = %v, want %v", got, commitReceiptFor)
	}
}

// TestCommitReceipt_PushButtonOpensPushDialog pins the button's action
// end to end: a left click on it closes the receipt and raises the same
// push dialog the git panel's "Push <branch>…" row opens, while a
// right-click on it only dismisses (the receipt's ordinary contract).
func TestCommitReceipt_PushButtonOpensPushDialog(t *testing.T) {
	if !gitAvailable() {
		t.Skip("git not on PATH")
	}
	dir := initRepoWithRemote(t)
	a := newTestApp(t, dir)
	a.refreshGitStatus()

	// Right-click on the button: dismisses, opens nothing. Drawn first,
	// as the real loop always is, so the press-swallowing box is stamped.
	a.handleGitCommitReceipt(receiptEvent(receiptHash, "subject"))
	a.drawCommitReceipt()
	b, ok := a.commitReceiptPushBtn()
	if !ok {
		t.Fatal("receipt in a repo with a remote should offer a push")
	}
	a.handleMouse(tcell.NewEventMouse(b.x, b.y, tcell.ButtonSecondary, tcell.ModNone))
	// The release a terminal sends, so the left press below reads as a
	// fresh press rather than a change of held buttons.
	a.handleMouse(tcell.NewEventMouse(b.x, b.y, tcell.ButtonNone, tcell.ModNone))
	if a.commitReceiptOpen() || a.modal != nil {
		t.Fatalf("right-click: receipt open=%v, modal=%T; want closed, none", a.commitReceiptOpen(), a.modal)
	}

	// Left click on the button: the push dialog, for the current branch.
	a.handleGitCommitReceipt(receiptEvent(receiptHash, "subject"))
	a.drawCommitReceipt()
	b, _ = a.commitReceiptPushBtn()
	a.handleMouse(tcell.NewEventMouse(b.x+1, b.y, tcell.Button1, tcell.ModNone))
	if a.commitReceiptOpen() {
		t.Error("the receipt should close when its push button is clicked")
	}
	m, ok := a.modal.(*gitPushModal)
	if !ok {
		t.Fatalf("modal = %T, want *gitPushModal", a.modal)
	}
	if m.local != "main" {
		t.Errorf("push dialog for %q, want main", m.local)
	}
}

// TestCommitReceipt_PointerInsideHoldsExpiry pins N-061: the window is
// held while the pointer rests on the panel (so a slow reach for the
// push button cannot lose the race) and starts over when it leaves.
// Motion is never claimed — the receipt only watches it.
func TestCommitReceipt_PointerInsideHoldsExpiry(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	a.handleGitCommitReceipt(receiptEvent(receiptHash, "subject"))
	a.drawCommitReceipt()
	armed := a.commitReceipt.seq
	b := a.commitReceipt.box

	// Motion outside before ever entering: no hold, no re-arm — the
	// timer that is running stays the live one.
	a.handleMouse(tcell.NewEventMouse(b.x-1, b.y-1, tcell.ButtonNone, tcell.ModNone))
	if a.commitReceipt.held || a.commitReceipt.seq != armed {
		t.Fatalf("motion outside changed the window: held=%v seq=%d, want false %d",
			a.commitReceipt.held, a.commitReceipt.seq, armed)
	}

	// Enter: the tick armed at open goes stale.
	a.handleMouse(tcell.NewEventMouse(b.x+1, b.y+1, tcell.ButtonNone, tcell.ModNone))
	if !a.commitReceipt.held {
		t.Fatal("pointer inside the panel should hold its expiry")
	}
	a.handleCommitReceiptExpire(&commitReceiptExpireEvent{seq: armed})
	if !a.commitReceiptOpen() {
		t.Fatal("the expiry fired while the pointer rested on the panel")
	}

	// Moving about inside (and the wheel) keeps the hold without
	// re-arming anything.
	held := a.commitReceipt.seq
	a.handleMouse(tcell.NewEventMouse(b.x+2, b.y+2, tcell.ButtonNone, tcell.ModNone))
	a.handleMouse(tcell.NewEventMouse(b.x+2, b.y+2, tcell.WheelDown, tcell.ModNone))
	if !a.commitReceiptOpen() || !a.commitReceipt.held || a.commitReceipt.seq != held {
		t.Fatalf("motion inside disturbed the hold: open=%v held=%v seq=%d want %d",
			a.commitReceiptOpen(), a.commitReceipt.held, a.commitReceipt.seq, held)
	}

	// Leave: a fresh window is armed, and its tick (only its tick)
	// closes the panel.
	a.handleMouse(tcell.NewEventMouse(b.x-1, b.y-1, tcell.ButtonNone, tcell.ModNone))
	if a.commitReceipt.held {
		t.Fatal("leaving the panel should release the hold")
	}
	if a.commitReceipt.seq == held {
		t.Fatal("leaving the panel should arm a new expiry")
	}
	a.handleCommitReceiptExpire(&commitReceiptExpireEvent{seq: held})
	if !a.commitReceiptOpen() {
		t.Fatal("a tick from before the hold must not close the re-armed receipt")
	}
	a.handleCommitReceiptExpire(&commitReceiptExpireEvent{seq: a.commitReceipt.seq})
	if a.commitReceiptOpen() {
		t.Error("the re-armed expiry should close the receipt")
	}
}

// TestCommitReceipt_ReplacementIsNotHeld pins that a receipt replacing
// a held one starts with its own live window: the hold belonged to the
// old box, and the pointer has not entered the new one yet.
func TestCommitReceipt_ReplacementIsNotHeld(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	a.handleGitCommitReceipt(receiptEvent(receiptHash, "first"))
	a.drawCommitReceipt()
	b := a.commitReceipt.box
	a.handleMouse(tcell.NewEventMouse(b.x+1, b.y+1, tcell.ButtonNone, tcell.ModNone))
	if !a.commitReceipt.held {
		t.Fatal("setup: pointer inside should hold")
	}

	a.showPushReceipt("main → origin/main", []byte("Everything up-to-date\n"))
	if a.commitReceipt.held {
		t.Fatal("a replacement receipt inherited the old one's hold")
	}
	a.handleCommitReceiptExpire(&commitReceiptExpireEvent{seq: a.commitReceipt.seq})
	if a.commitReceiptOpen() {
		t.Error("the replacement's own expiry should close it")
	}
}
