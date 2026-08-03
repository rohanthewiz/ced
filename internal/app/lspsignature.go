// =============================================================================
// File: internal/app/lspsignature.go
// Author: Rohan Allison <rohanthewiz@gmail.com>
// Created: 2026-08-03
// Copyright: 2026 Rohan Allison. All rights reserved.
// =============================================================================

// lspsignature.go is "Signature help" — the parameter list of the call
// the cursor sits inside, with the parameter you are currently typing
// picked out.
//
// It is the fifth code-intelligence verb and the closest sibling of
// hover: same request shape, same caret-anchored tooltip, same
// drop-a-response-for-a-tab-you-left rule. The difference is the whole
// feature — hover tells you what a symbol IS, this tells you where you
// ARE in a call — and that difference lives entirely in the emphasis.
// Without it this would be hover on the enclosing function, which the
// user could already get by moving the cursor.
//
//	Esc I / ≡ Code ──► menuSignatureHelp ──goroutine──► signatureHelp
//	                                                          │
//	           hoverModal{lines, emph} ◄── lspSignatureEvent ◄─┘
//
// IT IS MANUAL, AND THAT IS NOT AN OVERSIGHT. Every other editor pops
// this automatically on '(' and ',', and ced structurally cannot: a
// modal here OWNS THE KEYBOARD (the single-slot modal interface), so a
// tooltip that appeared while you typed would swallow the next
// keystroke — the very keystroke it exists to help you write. The
// automatic version of this feature needs a non-modal overlay, which is
// what ghost text is (editor/ghost.go); if signature help ever goes
// live-while-typing, that is the layer it goes through, not this one.

package app

import (
	"fmt"
	"strings"
	"time"

	"github.com/rohanthewiz/ced/internal/lsp"
)

// sigMaxDocLines caps the documentation tail so a well-commented
// function can't grow the tooltip into a wall. The signature itself is
// never capped — it is the answer.
const sigMaxDocLines = 6

// lspSignatureEvent carries a signatureHelp response back to the main
// loop. path lets a response for a document the user has already left be
// dropped, exactly as hover does.
type lspSignatureEvent struct {
	when time.Time
	path string
	sig  *lsp.Signature
	err  error
}

// When satisfies the tcell.Event interface.
func (e *lspSignatureEvent) When() time.Time { return e.when }

// menuSignatureHelp fires an async signatureHelp request for the cursor
// position; the result lands as an lspSignatureEvent.
func (a *App) menuSignatureHelp() {
	a.closeMenu()
	t := a.activeTabPtr()
	if t == nil || !a.hasLSPActions() {
		return
	}
	client, scr := a.lsp.client, a.screen
	if scr == nil {
		return
	}
	path := t.Path
	pos := lspPosFor(t, t.Cursor)
	// The flush matters more here than for any other verb: the answer is
	// a function of the half-typed call the user is standing in, and a
	// server that hasn't been told about the '(' they just typed will
	// report that they are not inside a call at all.
	a.lspFlushChange(t)
	go func() {
		sig, err := client.SignatureHelpAt(path, pos)
		_ = scr.PostEvent(&lspSignatureEvent{when: time.Now(), path: path, sig: sig, err: err})
	}()
}

// handleLSPSignature lands a signatureHelp response in the tooltip. A
// response for a tab the user has already left is dropped — a parameter
// list anchored to a caret in a different file describes nothing.
func (a *App) handleLSPSignature(e *lspSignatureEvent) {
	t := a.activeTabPtr()
	if t == nil || t.Path != e.path {
		return
	}
	if e.err != nil || e.sig == nil || strings.TrimSpace(e.sig.Label) == "" {
		// "here" is load-bearing: the usual reason is a cursor outside any
		// call, which is a fact about the position rather than about the
		// server, and a bare "no signature help" reads as the latter.
		a.flash("No signature help here")
		return
	}
	lines, emph := signatureTipLines(*e.sig, hoverModalTextWidth)
	a.openModal(&hoverModal{lines: lines, emph: emph})
}

// signatureTipLines renders one signature into tooltip lines plus the
// emphasis runs marking its active parameter.
//
// The label is HARD-wrapped and the prose is WORD-wrapped — the same
// split the chat transcript makes between fenced code and text, and here
// it is load-bearing twice over. A signature is code, so collapsing its
// runs of whitespace would misrepresent it; and hard wrapping is what
// keeps the mapping from a rune offset to a (line, column) exact
// division, which is how the emphasis survives the wrap at all. Word
// wrapping drops and merges spaces, so every offset past the first break
// would be a guess.
func signatureTipLines(sig lsp.Signature, width int) ([]string, []hoverEmph) {
	if width < 1 {
		width = 1
	}
	lines := hardWrap(sig.Label, width)

	var emph []hoverEmph
	if sig.ParamStart >= 0 && sig.ParamEnd > sig.ParamStart {
		for off := sig.ParamStart; off < sig.ParamEnd; {
			row, col := off/width, off%width
			if row >= len(lines) {
				break
			}
			// One run per wrapped row: a parameter straddling a break gets
			// both halves lit, which is what the user needs to see.
			end := min(sig.ParamEnd-off+col, width)
			emph = append(emph, hoverEmph{line: row, start: col, end: end})
			off += end - col
		}
	}

	// Overload position, when there is a set to be positioned in. Go has
	// no overloads, so this line is absent for the language ced ships a
	// server for — but a one-of-one counter would be noise anywhere.
	if sig.Count > 1 {
		lines = append(lines, fmt.Sprintf("(%d of %d)", sig.Index+1, sig.Count))
	}

	// The active parameter's own documentation comes BEFORE the
	// signature's: it is the answer to the question that made the user
	// ask, and the tooltip is capped, so it must not be what gets cut.
	if doc := strings.TrimSpace(sig.ParamDoc); doc != "" {
		lines = append(lines, "")
		lines = append(lines, capLines(wrapChatText(doc, width), sigMaxDocLines)...)
	}
	if doc := strings.TrimSpace(firstParagraph(sig.Doc)); doc != "" {
		lines = append(lines, "")
		lines = append(lines, capLines(wrapChatText(doc, width), sigMaxDocLines)...)
	}
	return lines, emph
}

// hardWrap splits s into chunks of exactly w runes, preserving every
// rune including whitespace — see signatureTipLines for why the exactness
// is what the emphasis mapping rests on. An empty string yields one empty
// line so the tooltip always has a body row.
func hardWrap(s string, w int) []string {
	runes := []rune(s)
	if len(runes) == 0 {
		return []string{""}
	}
	var out []string
	for i := 0; i < len(runes); i += w {
		out = append(out, string(runes[i:min(i+w, len(runes))]))
	}
	return out
}

// firstParagraph keeps a doc comment's opening paragraph. Go doc
// comments lead with the sentence that says what the function does and
// then go into detail nobody reads from a tooltip; taking the first
// paragraph beats truncating mid-sentence at a line count.
func firstParagraph(doc string) string {
	doc = strings.TrimSpace(doc)
	if i := strings.Index(doc, "\n\n"); i >= 0 {
		return doc[:i]
	}
	return doc
}

// capLines truncates a rendered block, marking the cut. An unmarked
// truncation reads as the server having said less than it did.
func capLines(lines []string, maxLines int) []string {
	if len(lines) <= maxLines {
		return lines
	}
	return append(lines[:maxLines:maxLines], "…")
}
