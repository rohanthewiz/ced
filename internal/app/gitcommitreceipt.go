// =============================================================================
// File: internal/app/gitcommitreceipt.go
// Author: Rohan Allison <rohanthewiz@gmail.com>
// Created: 2026-08-24
// Copyright: 2026 Rohan Allison. All rights reserved.
// =============================================================================

// gitcommitreceipt.go is the panel that appears for a few seconds after
// a commit lands, naming the hash git just minted and the whole message
// that went with it.
//
// Why anything beyond the status flash: a commit is the one write in
// this editor whose RESULT the user wants to read back. The flash says
// "Commit — done", which answers whether the command worked and nothing
// about what it produced — and the two facts people actually reach for
// next (the hash, to cherry-pick or reference it; the message, to check
// that the trailer landed, that the drafted subject reads right, that
// the body survived the single-line prompt) are precisely the two the
// flash has no room for. Both exist only AFTER git exits, which is why
// this hangs off runGitCmdOK rather than off the prompt that composed
// the message.
//
// Four properties, and each is a house rule this feature inherits
// rather than invents:
//
//   - PASSIVE. It never takes the modal slot. Nobody asked a question,
//     so nothing here may own the keyboard — the hoverdwell argument,
//     and the same reason the arriving `git status` report DECLINES an
//     occupied slot instead of replacing whatever is in it. A modal
//     here would eat the user's next keystroke to make them dismiss a
//     receipt for something they already know happened.
//   - TEMPORARY, AND DISMISSED BY ANYTHING. One-shot timer (the
//     events-only rule: the goroutine posts, it never touches App), and
//     any key or any click closes it early. The keystroke is NOT
//     consumed — same contract as clearing the ghost text, because the
//     panel is chrome the user did not ask for and must never cost them
//     a character.
//   - REPORTED, NEVER RE-DERIVED. The hash and message come from `git
//     log -1` on the repository, not from the message string ced sent:
//     hooks rewrite messages, `commit.template` and cleanup rules trim
//     them, and the hash cannot be known any other way. What is on
//     screen is what the repository now holds.
//   - SILENT WHEN IT CANNOT SPEAK. A failed or unreadable `git log`
//     costs the receipt, not the commit — which already succeeded and
//     already flashed. Same silent-degradation contract as the LSP and
//     the formatters.

package app

import (
	"os/exec"
	"strings"
	"time"

	"github.com/gdamore/tcell/v2"
)

// commitReceiptFor is how long the panel stays up unattended. Long
// enough to read a hash and a subject line without hurrying, short
// enough that a user who looked away does not come back to a box
// sitting over their code — and every gesture they might make on
// returning closes it anyway.
const commitReceiptFor = 7 * time.Second

// commitReceiptWidth is the panel's preferred width, clamped to the
// window at draw time. Sized for git's own convention: a 40-character
// hash plus padding, and a message body wrapped near the 72 columns
// commit messages are written to.
const commitReceiptWidth = 72

// commitReceiptMaxLines caps the message body. A commit with a long
// body is exactly the one whose subject and hash still fit at the top,
// so cutting the tail costs the least; the cut is MARKED (capLines),
// because a silently short message reads as a message that was silently
// truncated on the way in — the one wrong thing a receipt could say.
const commitReceiptMaxLines = 12

// commitReceiptFormat asks for the two facts the panel exists to show,
// separated by a newline: the full hash, then the raw message. %B rather
// than %s%n%b so the subject and body arrive exactly as the repository
// stores them, blank separator line included.
const commitReceiptFormat = "%H%n%B"

// gitCommitReceiptEvent carries one finished `git log -1` from the
// background goroutine to the main loop. A non-nil err (or output that
// names no hash) means no panel — see the file header on degrading
// silently.
type gitCommitReceiptEvent struct {
	when time.Time
	out  []byte
	err  error
}

// When satisfies the tcell.Event interface.
func (e *gitCommitReceiptEvent) When() time.Time { return e.when }

// commitReceiptExpireEvent is the one-shot dismissal tick. seq pins it
// to the receipt that scheduled it, so a second commit inside the window
// is not closed early by the first one's timer.
type commitReceiptExpireEvent struct {
	when time.Time
	seq  int
}

// When satisfies the tcell.Event interface.
func (e *commitReceiptExpireEvent) When() time.Time { return e.when }

// Compile-time checks that both really are tcell.Events.
var (
	_ tcell.Event = (*gitCommitReceiptEvent)(nil)
	_ tcell.Event = (*commitReceiptExpireEvent)(nil)
)

// commitReceiptState is the whole feature's state. Empty hash means
// closed; there is no separate flag to keep in step with it.
type commitReceiptState struct {
	// hash is the full 40-character object name of the commit being
	// reported, and doubles as the open/closed flag.
	hash string
	// lines is the wrapped, capped message body, ready to draw.
	lines []string
	// seq counts receipts. A scheduled expiry carries the seq current
	// when it was armed and is ignored unless it still matches — the
	// hoverdwell generation trick, and what makes "commit twice quickly"
	// leave the SECOND receipt up for its full window.
	seq int
	// box is the rectangle the last draw stamped, so a press inside the
	// panel is swallowed rather than landing in code the user cannot
	// see (the dwell tooltip's contract, for the same reason).
	box struct{ x, y, w, h int }
}

// requestCommitReceipt forks `git log -1` and posts what it finds. It is
// the runGitCmdOK hook armed by gitCommitFiles — a method expression, so
// the plumbing carries a plain func(*App) and nothing about the commit
// path has to know what a receipt is.
//
// Fire-and-forget like every other background git job: a dropped event
// (the screen is shutting down) just means the receipt never appears.
func (a *App) requestCommitReceipt() {
	if a.screen == nil || a.rootDir == "" {
		return
	}
	scr := a.screen
	root := a.rootDir
	go func() {
		out, err := exec.Command("git", "-C", root,
			"log", "-1", "--no-color", "--format="+commitReceiptFormat).Output()
		_ = scr.PostEvent(&gitCommitReceiptEvent{when: time.Now(), out: out, err: err})
	}()
}

// handleGitCommitReceipt opens the panel for an arrived report. Main
// loop only.
//
// A modal or the open menu suppresses it entirely rather than being
// drawn under: this layer paints BELOW the overlay layer, so a receipt
// opened now would be invisible for the whole of its window and then
// expire unseen. Suppressing costs nothing the user has not already
// been told — the commit's own flash already said it worked.
func (a *App) handleGitCommitReceipt(e *gitCommitReceiptEvent) {
	if e.err != nil || a.modal != nil || a.menuOpen {
		return
	}
	hash, msg := parseCommitReceipt(string(e.out))
	if hash == "" {
		return
	}
	a.commitReceipt.hash = hash
	a.commitReceipt.lines = commitReceiptBody(msg, commitReceiptWidth-4)
	a.armCommitReceiptExpiry()
}

// parseCommitReceipt splits `git log -1 --format=%H%n%B` output into the
// hash and the raw message. A body's own trailing newlines are trimmed
// (git always emits at least one) so the panel does not end in blank
// rows it would then have to pay for in height.
func parseCommitReceipt(out string) (hash, msg string) {
	out = strings.TrimLeft(out, "\n")
	hash, msg, _ = strings.Cut(out, "\n")
	hash = strings.TrimSpace(hash)
	// Guard against a format that came back as something other than a
	// hash — an alias in the user's config, a future git. Nothing else
	// downstream would notice, and the panel would confidently show it.
	if !isHexHash(hash) {
		return "", ""
	}
	return hash, strings.TrimRight(msg, "\n")
}

// isHexHash reports whether s looks like an object name: non-empty and
// hex all the way through. Deliberately not length-pinned — git's hash
// is 40 hex characters today and 64 under SHA-256, and a receipt has no
// reason to have an opinion about which repository format it is reading.
func isHexHash(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		switch {
		case r >= '0' && r <= '9', r >= 'a' && r <= 'f', r >= 'A' && r <= 'F':
		default:
			return false
		}
	}
	return true
}

// commitReceiptBody wraps a raw commit message to the panel's usable
// width and caps it.
//
// Wrapping runs PER SOURCE LINE, and blank lines survive as blanks: a
// commit message's line structure is authored (the subject stands
// alone, paragraphs and bullets are separated on purpose), so flowing
// the whole thing as one blob would run the subject into the body and
// report a message the repository does not hold.
func commitReceiptBody(msg string, width int) []string {
	if width < 1 {
		width = 1
	}
	var out []string
	for _, ln := range strings.Split(msg, "\n") {
		if strings.TrimSpace(ln) == "" {
			// Never lead with a blank: a message whose body begins after
			// the subject already supplies the separator, and a leading
			// one would just be a wasted row.
			if len(out) > 0 {
				out = append(out, "")
			}
			continue
		}
		out = append(out, wrapChatText(ln, width)...)
	}
	if len(out) == 0 {
		out = []string{"(no message)"}
	}
	return capLines(out, commitReceiptMaxLines)
}

// armCommitReceiptExpiry schedules the automatic dismissal. One-shot
// timer, no goroutine of its own until it fires, and it POSTS rather
// than mutating — the iron rule for anything running off the main loop.
func (a *App) armCommitReceiptExpiry() {
	if a.screen == nil {
		return
	}
	a.commitReceipt.seq++
	seq := a.commitReceipt.seq
	scr := a.screen
	time.AfterFunc(commitReceiptFor, func() {
		_ = scr.PostEvent(&commitReceiptExpireEvent{when: time.Now(), seq: seq})
	})
}

// handleCommitReceiptExpire closes the panel when the tick that fired
// still belongs to the receipt on screen.
func (a *App) handleCommitReceiptExpire(e *commitReceiptExpireEvent) {
	if e.seq == a.commitReceipt.seq {
		a.closeCommitReceipt()
	}
}

// closeCommitReceipt hides the panel and invalidates any pending
// expiry. Cheap and safe when nothing is open, which is what lets the
// keyboard and mouse paths call it unconditionally.
func (a *App) closeCommitReceipt() {
	a.commitReceipt.hash = ""
	a.commitReceipt.lines = nil
	a.commitReceipt.box = struct{ x, y, w, h int }{}
	a.commitReceipt.seq++
}

// commitReceiptOpen reports whether the panel is up.
func (a *App) commitReceiptOpen() bool { return a.commitReceipt.hash != "" }

// commitReceiptContains reports whether a screen cell is inside the
// drawn panel, using the rect the last draw stamped.
func (a *App) commitReceiptContains(x, y int) bool {
	b := a.commitReceipt.box
	return b.w > 0 && b.h > 0 && x >= b.x && x < b.x+b.w && y >= b.y && y < b.y+b.h
}

// commitReceiptRect measures and centers the panel. Rows:
//
//	0        top border
//	1        title — "Committed            any key"
//	2        divider
//	3        the hash
//	4        blank
//	5..5+n-1 the message
//	5+n      blank
//	6+n      bottom border
func (a *App) commitReceiptRect() (x, y, w, h int) {
	w = commitReceiptWidth
	if w > a.width {
		w = a.width
	}
	return a.centeredRect(w, len(a.commitReceipt.lines)+7)
}

// drawCommitReceipt paints the panel. Called unconditionally from draw,
// because this is also what clears the stamped rect when nothing is
// showing — a box left over from the last frame would swallow presses
// over a region that no longer has a panel in it.
func (a *App) drawCommitReceipt() {
	if !a.commitReceiptOpen() {
		a.commitReceipt.box = struct{ x, y, w, h int }{}
		return
	}
	mx, my, mw, mh := a.commitReceiptRect()
	a.commitReceipt.box = struct{ x, y, w, h int }{mx, my, mw, mh}

	c := a.chrome()
	fillRect(a.screen, mx, my, mw, mh, c.bgSt)
	drawBorder(a.screen, mx, my, mw, mh, c.border)
	drawHDivider(a.screen, mx, my+2, mw, c.border)
	drawAt(a.screen, mx+1, my+1, " Committed", c.title)
	// The hint says what dismisses this, not "esc" — the frame every
	// MODAL draws promises a keystroke you must spend, and this one
	// costs nothing: whatever you press next both closes the panel and
	// does what it always did.
	hint := "any key "
	drawAt(a.screen, mx+mw-1-runeLen(hint), my+1, hint, c.muted)

	// The hash gets the accent, because it is the fact that exists
	// nowhere else on screen and the one a reader is most likely to be
	// copying by eye.
	drawAt(a.screen, mx+2, my+3, elide(a.commitReceipt.hash, mw-4), c.title)
	for i, ln := range a.commitReceipt.lines {
		drawAt(a.screen, mx+2, my+5+i, elide(ln, mw-4), c.body)
	}
}
