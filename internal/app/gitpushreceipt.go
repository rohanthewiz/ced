// =============================================================================
// File: internal/app/gitpushreceipt.go
// Author: Rohan Allison <rohanthewiz@gmail.com>
// Created: 2026-09-30
// Copyright: 2026 Rohan Allison. All rights reserved.
// =============================================================================

// gitpushreceipt.go shows git's own output for a few seconds after a push
// from the push dialog succeeds — the commit receipt's twin, drawn in the
// same panel (gitcommitreceipt.go).
//
// Why git's output rather than a summary ced composes: the flash already
// says "Push main → origin/main — done", and what the user wants next is
// PROOF — which remote URL it went to, the old..new range that moved
// ("ad6f060..9b1c2e4  main -> main"), a `[new branch]` marker, "Everything
// up-to-date" when there was nothing to send, and whatever the remote
// said (GitHub's "Create a pull request for …" link). All of that exists
// only in what `git push` printed; none of it can be re-read from the
// repository afterwards, which is why this rides runGitCmdOKOutput rather
// than re-querying git the way the commit receipt does.
//
//	push dialog submit
//	      │ runGitCmdOKOutput(label, pushReceiptHook(head), push …)
//	      ▼
//	goroutine: git push (CombinedOutput) ──► gitCmdDoneEvent{output}
//	      ▼ main loop
//	handleGitCmdDone: flash "… — done", refresh, onOKOutput(a, output)
//	      ▼
//	showPushReceipt ──► openReceiptContent{"Pushed", head, lines, link, width}
//
// ALL of it, because the part a user most needs is often the last thing
// the remote says: a first push of a branch to GitHub ends with
//
//	remote: Create a pull request for 'feat' on GitHub by visiting:
//	remote:      https://github.com/owner/repo/pull/new/feat
//
// and that link is the next step. So the receipt (unlike the commit
// receipt's 12-line body) is capped only by the WINDOW, it widens to its
// longest line so the URL stays on one row (a URL hard-wrapped across two
// rows is one the terminal cannot detect and a reader cannot select), and
// a link the remote printed gets "[ Open link ]  [ Copy link ]" buttons
// and the longer window a receipt with a button earns. The output is also
// remembered (App.lastPush) so ≡ Git "Show last push output" can bring
// the receipt back after it has gone — it is dismissed by any key, and
// the link is still wanted after the user has typed something.
//
// Failures never get here: a nonzero push already opens the info modal
// with the same output, which is the louder answer it deserves. The
// passive rules are the commit receipt's, inherited through the shared
// panel — never the modal slot, dismissed by anything without consuming
// the key, suppressed while a modal or the menu owns the screen.

package app

import (
	"strings"
	"unicode"
)

// receiptOpenLinkLabel and receiptCopyLinkLabel are the push receipt's
// link buttons. Constants because the geometry (commitReceiptLinkBtns)
// measures them and the draw paints them — two reads of one string.
const (
	receiptOpenLinkLabel = "[ Open link ]"
	receiptCopyLinkLabel = "[ Copy link ]"
)

// lastPushState remembers the most recent successful push's output for
// the ≡ Git "Show last push output" row. Session-only and replaced by
// the next push: it answers "what did that push just say", not a history.
type lastPushState struct {
	head string
	out  []byte
}

// pushReceiptHook builds the success hook the push dialog hands to
// runGitCmdOKOutput. head is the "main → origin/main" line the panel
// shows under its title; it is captured at submit time because the
// dialog that knew it has closed by the time git exits.
func pushReceiptHook(head string) func(*App, []byte) {
	return func(a *App, out []byte) { a.showPushReceipt(head, out) }
}

// showPushReceipt opens the shared receipt panel with a push's output.
// Main loop only.
//
// The output is remembered FIRST, unconditionally, so the ≡ row can show
// it even when the panel itself declines below.
//
// Declines an occupied screen for the commit receipt's reason: the panel
// paints below modals and the menu, so opening under one would spend its
// whole window invisible. The push's own flash has already said it
// worked, and the remembered output is one ≡ row away.
func (a *App) showPushReceipt(head string, out []byte) {
	a.lastPush = lastPushState{head: head, out: out}
	if a.modal != nil || a.menuOpen {
		return
	}
	a.openPushReceipt(head, out)
}

// openPushReceipt fills the receipt panel from a push's output: the
// whole output, as wide as its longest line allows and as tall as the
// window allows, with the remote's link (if any) behind buttons.
func (a *App) openPushReceipt(head string, out []byte) {
	src := pushReceiptLines(out)
	width := pushReceiptWidth(src, a.width)
	a.openReceiptContent(receiptContent{
		title: "Pushed",
		head:  head,
		lines: pushReceiptBody(out, width-4, pushReceiptMaxLines(a.height)),
		link:  pushReceiptLink(src),
		width: width,
	})
}

// pushReceiptWidth is the panel width that keeps every line of the
// output on one row: the longest line plus the border and the two-cell
// insets, never narrower than the commit receipt (a short "Everything
// up-to-date" should not get a cramped box) and never wider than the
// window (commitReceiptRect clamps too, but the BODY must be wrapped to
// the width actually drawn, so the clamp has to happen here first).
func pushReceiptWidth(lines []string, winW int) int {
	w := commitReceiptWidth
	for _, ln := range lines {
		if n := runeLen(ln) + 4; n > w {
			w = n
		}
	}
	if winW > 0 && w > winW {
		w = winW
	}
	return w
}

// pushReceiptMaxLines is how many body rows fit the window: the height
// less the panel's own 8 rows of frame (borders, title, divider, head,
// two blanks, button row). Floored at 3 so a tiny window still shows
// the start of the output, with the cut marked.
func pushReceiptMaxLines(winH int) int {
	if n := winH - 8; n > 3 {
		return n
	}
	return 3
}

// pushReceiptLink returns the URL the remote printed for the user to
// visit, or "" when it printed none.
//
// Only "remote:" lines are searched: those are the server talking to the
// user, which is where GitHub's pull-request link, GitLab's merge-request
// link and Bitbucket's / Gitea's equivalents all appear. The "To <url>"
// line is git naming the remote itself — a clone URL, not a page to
// open — so it must never win just by coming first. The FIRST link wins
// because every host prints the one action link before anything else.
func pushReceiptLink(lines []string) string {
	for _, ln := range lines {
		rest, ok := strings.CutPrefix(ln, "remote:")
		if !ok {
			continue
		}
		for _, f := range strings.Fields(rest) {
			if strings.HasPrefix(f, "https://") || strings.HasPrefix(f, "http://") {
				// Prose punctuation that follows a link ("…visit
				// https://x/y.") is not part of it.
				return strings.TrimRight(f, ".,;:)>'\"")
			}
		}
	}
	return ""
}

// commitReceiptLinkBtns is the ONE geometry for the push receipt's two
// link buttons (the btnRect house rule: draw and the press handler both
// read it). Right-aligned on the button row, Open first. ok is false
// when the receipt carries no link, or the window is too narrow for both
// labels inside the border.
func (a *App) commitReceiptLinkBtns() (open, cp btnRect, ok bool) {
	if !a.commitReceiptOpen() || a.commitReceipt.link == "" {
		return btnRect{}, btnRect{}, false
	}
	mx, my, mw, mh := a.commitReceiptRect()
	ow, cw := runeLen(receiptOpenLinkLabel), runeLen(receiptCopyLinkLabel)
	if ow+2+cw > mw-4 {
		return btnRect{}, btnRect{}, false
	}
	y := my + mh - 2
	cp = btnRect{x: mx + mw - 2 - cw, y: y, w: cw}
	open = btnRect{x: cp.x - 2 - ow, y: y, w: ow}
	return open, cp, true
}

// commitReceiptLinkHit names the link button under a screen cell:
// "open", "copy", or "" for neither (or no buttons drawn).
func (a *App) commitReceiptLinkHit(x, y int) string {
	open, cp, ok := a.commitReceiptLinkBtns()
	switch {
	case !ok:
		return ""
	case open.contains(x, y):
		return "open"
	case cp.contains(x, y):
		return "copy"
	}
	return ""
}

// openReceiptLink opens url in the desktop browser through the host-exec
// seam (hostopen.go), with that file's rules: no desktop on Linux is a
// flashed reason (pointing at Copy link, which works over SSH via OSC 52),
// and macOS over SSH says where the window went.
func (a *App) openReceiptLink(url string) {
	argv, reason := browserArgv(url)
	if reason != "" {
		a.flash(reason)
		return
	}
	a.hostRunAsync(a.rootDir, argv, "Open link")
	if hostGOOS == "darwin" && hostEnv("SSH_CONNECTION") != "" {
		a.flash("Opened the link in the browser on this Mac's own screen (you're over SSH)")
		return
	}
	a.flash("Opened " + url)
}

// browserArgv composes the command that opens url in the desktop
// browser, or a reason it cannot run here — fileManagerArgv's shape, so
// both platforms are testable from either.
func browserArgv(url string) (argv []string, reason string) {
	if hostGOOS == "darwin" {
		return []string{"open", url}, ""
	}
	if hostEnv("DISPLAY") == "" && hostEnv("WAYLAND_DISPLAY") == "" {
		return nil, "No desktop session here ($DISPLAY is unset) — use Copy link instead"
	}
	return []string{"xdg-open", url}, ""
}

// hasLastPush gates ≡ Git "Show last push output": a push has succeeded
// this session.
func (a *App) hasLastPush() bool { return a.lastPush.head != "" }

// menuGitLastPush reopens the push receipt from the remembered output —
// the way back to a PR link after the passive panel has gone. The menu
// closes first, so the panel's "occupied screen" rule is satisfied.
func (a *App) menuGitLastPush() {
	a.closeMenu()
	if !a.hasLastPush() {
		a.flash("No push yet this session")
		return
	}
	a.openPushReceipt(a.lastPush.head, a.lastPush.out)
}

// pushReceiptLines is git push's combined output cleaned into display
// lines, before any wrapping: one source for the body, the width and the
// link, so all three see the same text.
//
// Cleaning matters because the output is not written for a grid. Remote
// messages and progress meters redraw themselves with a bare CR — the
// last frame after the final CR is the one a terminal would have left
// standing, so that is the one kept. Any remaining control bytes (a
// remote's ANSI colors, a stray bell) are dropped rather than handed to
// the screen, where they would corrupt the cells around them.
//
// Blank lines INSIDE the output survive, for the same reason the commit
// body keeps its paragraphs: git separates its own report from the
// remote's messages that way, and flowing them together would misreport
// who said what.
func pushReceiptLines(out []byte) []string {
	var src []string
	for _, ln := range strings.Split(string(out), "\n") {
		// CRLF first, so a Windows-style line end is not mistaken for a
		// redraw that wiped the line.
		ln = strings.TrimRight(ln, "\r")
		if i := strings.LastIndexByte(ln, '\r'); i >= 0 {
			ln = ln[i+1:]
		}
		src = append(src, strings.TrimRightFunc(stripReceiptControl(ln), unicode.IsSpace))
	}
	// Blank edges cost rows and say nothing; git ends with a newline and
	// remote banners are often padded.
	for len(src) > 0 && src[0] == "" {
		src = src[1:]
	}
	for len(src) > 0 && src[len(src)-1] == "" {
		src = src[:len(src)-1]
	}
	return src
}

// pushReceiptBody turns `git push`'s combined output into panel rows:
// cleaned (pushReceiptLines), wrapped to width and capped at maxLines
// with the cut marked. The caller sizes width to the longest line where
// the window allows, so wrapping only happens on a narrow window.
func pushReceiptBody(out []byte, width, maxLines int) []string {
	if width < 1 {
		width = 1
	}
	var rows []string
	for _, ln := range pushReceiptLines(out) {
		// A line that fits is kept verbatim: the word wrapper collapses
		// runs of spaces, and git aligns its ref table ("ad6f060..9b1c2e4
		// ␣␣main -> main") with exactly those runs.
		if runeLen(ln) <= width {
			rows = append(rows, ln)
			continue
		}
		rows = append(rows, wrapChatText(ln, width)...)
	}
	if len(rows) == 0 {
		// A successful push always prints something today, but a quiet
		// config (or a future git) could change that. Say so rather than
		// drawing an empty box that reads as a broken receipt.
		rows = []string{"(git printed nothing)"}
	}
	return capLines(rows, maxLines)
}

// stripReceiptControl removes ANSI escape sequences and every other
// control character from one line. Tabs become a space: the panel has no
// tab stops, and git's ref table aligns with spaces anyway.
//
// The ESC handling covers CSI (ESC [ … final byte in @–~), which is what
// color codes are; any other ESC is dropped on its own, which at worst
// leaves a harmless printable byte behind.
func stripReceiptControl(s string) string {
	var b strings.Builder
	rs := []rune(s)
	for i := 0; i < len(rs); i++ {
		r := rs[i]
		switch {
		case r == '\x1b':
			if i+1 < len(rs) && rs[i+1] == '[' {
				i += 2
				for i < len(rs) && (rs[i] < '@' || rs[i] > '~') {
					i++
				}
			}
		case r == '\t':
			b.WriteByte(' ')
		case unicode.IsControl(r):
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}
