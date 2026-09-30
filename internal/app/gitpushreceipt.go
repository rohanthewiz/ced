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
//	showPushReceipt ──► openGitReceipt("Pushed", "", head, lines)
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
// Declines an occupied screen for the commit receipt's reason: the panel
// paints below modals and the menu, so opening under one would spend its
// whole window invisible. The push's own flash has already said it
// worked, so declining loses nothing the user was not told.
func (a *App) showPushReceipt(head string, out []byte) {
	if a.modal != nil || a.menuOpen {
		return
	}
	a.openGitReceipt("Pushed", "", head, pushReceiptBody(out, commitReceiptWidth-4))
}

// pushReceiptBody turns `git push`'s combined output into panel rows:
// cleaned, trimmed of blank edges, wrapped to width and capped with the
// cut marked.
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
func pushReceiptBody(out []byte, width int) []string {
	if width < 1 {
		width = 1
	}
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

	var rows []string
	for _, ln := range src {
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
	return capLines(rows, commitReceiptMaxLines)
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
