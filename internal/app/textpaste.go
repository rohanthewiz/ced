// =============================================================================
// File: internal/app/textpaste.go
// Author: Rohan Allison <rohanthewiz@gmail.com>
// Created: 2026-07-15
// Copyright: 2026 Rohan Allison. All rights reserved.
// =============================================================================

// textpaste.go implements bracketed-paste handling for the editor buffer.
// The screen enables paste mode (see App.New's scr.EnablePaste), so a
// terminal paste arrives wrapped in an EventPaste{start} … content-as-
// key-events … EventPaste{end} sequence instead of a raw stream of
// keystrokes. When an editable tab is the paste target we accumulate the
// content verbatim and splice it in as ONE InsertString.
//
// That single verbatim insert is the whole point. Replaying a paste as
// keystrokes ran each pasted tab through handleKey's `KeyTab →
// tab.IndentUnit` branch (so pasted code lost its exact indentation when
// the buffer's unit was spaces) and pushed every rune through the
// leader/shortcut machinery. Inserting the accumulated string once keeps
// tabs and newlines byte-for-byte and collapses the paste into a single
// undo step.
//
// Only the editor is gated. When a modal, the find bar, the menu, or a
// focused terminal owns the keyboard, `pasting` stays false and the
// content flows through handleKey as ordinary key events — exactly the
// pre-bracketed-paste behavior, so those single-line inputs are
// unaffected and there's no regression. Terminals that don't understand
// the enable sequence never send the markers, so the paste also arrives
// as raw keys there: the fix degrades silently, same contract as the
// formatter and LSP layers.
//
// All handling runs on the main loop; there is no locking here.

package app

import (
	"strings"

	"github.com/gdamore/tcell/v2"
	"github.com/rohanthewiz/ced/internal/editor"
)

// handlePaste processes a bracketed-paste boundary event. A start marker
// arms accumulation only when a surface that wants the content whole is
// the focus target — an editable tab, the focused chat composer, or the
// focused terminal input; otherwise it leaves `pasting` false so the
// content flows as normal key events. An end marker flushes whatever was
// accumulated into that surface as a single insert.
func (a *App) handlePaste(ev *tcell.EventPaste) {
	if ev.Start() {
		a.pasting = a.chatPasteTarget() || a.termPasteTarget() ||
			a.editorPasteTarget() != nil
		a.pasteBuf = a.pasteBuf[:0]
		return
	}
	if !a.pasting {
		return
	}
	a.pasting = false
	if len(a.pasteBuf) == 0 {
		return
	}
	// Re-resolve the target rather than trust the one seen at start:
	// nothing can steal focus mid-paste (no events run between the
	// markers), but a nil-safe re-check means a flush never panics if
	// that assumption ever changes. Chat is checked before the terminal,
	// matching handleKey's focus tiebreak; all three predicates exclude
	// each other anyway, so the order is only a formality.
	switch {
	case a.chatPasteTarget():
		a.chatInsertPaste(string(a.pasteBuf))
	case a.termPasteTarget():
		a.termInsertPaste(string(a.pasteBuf))
	default:
		if tab := a.editorPasteTarget(); tab != nil {
			tab.InsertString(string(a.pasteBuf))
		}
	}
	a.pasteBuf = a.pasteBuf[:0]
}

// accumulatePaste appends one key event's literal content to the paste
// buffer while a paste is in flight. Enter becomes a newline and Tab a
// literal tab — the exact characters raw handleKey would otherwise
// destroy (IndentUnit substitution, leader routing). Non-text keys inside
// a paste (arrows, control chars, a stray Esc from the parser) carry no
// insertable content and are dropped.
func (a *App) accumulatePaste(ev *tcell.EventKey) {
	switch ev.Key() {
	case tcell.KeyRune:
		a.pasteBuf = append(a.pasteBuf, ev.Rune())
	case tcell.KeyEnter:
		a.pasteBuf = append(a.pasteBuf, '\n')
	case tcell.KeyTab:
		a.pasteBuf = append(a.pasteBuf, '\t')
	}
}

// chatPasteTarget reports whether the focused chat composer should
// receive a bracketed paste. It is the single source of truth for "is
// the chat prompt the paste target", used both to arm accumulation and
// to route the flush.
//
// This gate is the whole reason pasting into the prompt used to fail:
// the panel owns the keyboard while focused, but a paste still resolved
// to the active tab and silently landed in the file BEHIND the panel.
// The keyboard-owning surfaces (modal, find bar, menu) outrank the panel
// exactly as they do for the editor, so the same suppressions apply.
func (a *App) chatPasteTarget() bool {
	if a.modal != nil || a.findOpen || a.menuOpen {
		return false
	}
	return a.chat.open && a.chat.focused
}

// termPasteTarget reports whether the focused terminal input should
// receive a bracketed paste — the termPasteClip gate, hoisted so the
// bracketed-paste path can share it. Same story as chatPasteTarget: the
// panel owns the keyboard while focused, so the paste belongs to its
// input line and not to the file behind it.
//
// Before this gate a paste here flowed through as raw key events, which
// meant every Enter inside a multi-line paste RAN the line before it —
// pasting a three-line snippet executed two commands nobody typed.
// Accumulating and flattening instead is what makes that impossible.
func (a *App) termPasteTarget() bool {
	if a.modal != nil || a.findOpen || a.menuOpen {
		return false
	}
	if a.chat.open && a.chat.focused {
		return false // chat wins the tiebreak, as in handleKey
	}
	return a.term.open && a.term.focused
}

// flattenPaste folds multi-line pasted text into a single line: line
// breaks and tabs each become one space (a CRLF pair counts once, so
// Windows-flavored text doesn't double-space), and the remaining control
// runes — which a single-line field can't render and no consumer wants —
// are dropped.
//
// Both single-line inputs (the chat composer, the terminal command line)
// share this policy so the two panels can't drift apart. Flattening is
// the conservative reading of a multi-line paste: it keeps the whole
// text visible and editable, where the alternatives silently drop
// everything after the first line or — in the terminal's case — invent
// separators the user never typed.
func flattenPaste(text string) string {
	var b strings.Builder
	b.Grow(len(text))
	prevBreak := false
	for _, r := range text {
		switch {
		case r == '\r' || r == '\n':
			if !prevBreak {
				b.WriteRune(' ')
			}
			prevBreak = true
			continue
		case r == '\t':
			b.WriteRune(' ')
		case r < 0x20 || r == 0x7f:
			// Unrenderable in a single-line field; drop it.
		default:
			b.WriteRune(r)
		}
		prevBreak = false
	}
	return b.String()
}

// pasteLineCount counts the lines a paste actually carried, so a caller
// can say so when flattening changed the shape of the text. Trailing
// breaks don't count (a copied line usually brings one along, and
// calling that "2 lines" would be a lie), and a CRLF pair counts once.
func pasteLineCount(text string) int {
	trimmed := strings.TrimRight(text, "\r\n")
	if trimmed == "" {
		return 0
	}
	n, prevBreak := 1, false
	for _, r := range trimmed {
		if r == '\r' || r == '\n' {
			if !prevBreak {
				n++
			}
			prevBreak = true
			continue
		}
		prevBreak = false
	}
	return n
}

// editorPasteTarget returns the tab a paste should land in, or nil when
// some other surface (modal, find bar, menu, or a focused terminal or
// chat panel) owns the keyboard or the active tab can't accept text
// (none open, or an image preview). It is the single source of truth for
// "is the editor the paste target", used both to arm accumulation and to
// flush it, so the two can never disagree.
func (a *App) editorPasteTarget() *editor.Tab {
	if a.modal != nil || a.findOpen || a.menuOpen {
		return nil
	}
	if a.term.open && a.term.focused {
		return nil
	}
	if a.chat.open && a.chat.focused {
		return nil
	}
	tab := a.activeTabPtr()
	if tab == nil || tab.IsImage() {
		return nil
	}
	return tab
}
