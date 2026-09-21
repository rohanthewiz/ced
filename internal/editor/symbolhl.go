// =============================================================================
// File: internal/editor/symbolhl.go
// Author: Rohan Allison <rohanthewiz@gmail.com>
// Created: 2026-09-21
// Copyright: 2026 Rohan Allison. All rights reserved.
// =============================================================================

// symbolhl.go is the SEMANTIC twin of the matching-word highlight: the
// uses of one specific symbol, as a language server resolved them, rather
// than every run of characters that happens to spell its name.
//
// wordhl.go answers "what else is called this?" by scanning text, which
// is instant and works in any file — and is wrong in exactly the cases
// that matter when reading code: two `err`s in sibling scopes, a field
// and a local sharing a name, the word appearing in a comment. A server
// knows which of those are the SAME BINDING, and it knows one more thing
// text never can: which uses WRITE the symbol and which only read it.
//
// The editor package has no server, so this file is only the display
// half — a set of ranges pinned to the buffer revision they were measured
// against, and the built-in source that paints them:
//
//	app (lsphighlight.go) ──SetSymbolUses──► Tab.symbolUses @ EditRev
//	                                              │
//	    wordHighlightSource stands down  ◄────────┤  while the set is live
//	    symbolUseSource paints reads + underlined writes
//
// The decisions worth spelling out:
//
//   - IT REPLACES THE WORD HIGHLIGHT WHILE LIVE, it does not stack on it.
//     Both answer the same question, and two washes over the same cells —
//     one of them a superset of the other — would leave the user unable
//     to tell which cells the server actually vouched for. Same stand-down
//     rule multi-caret mode uses.
//   - THE SET DIES WITH THE REVISION. The ranges are coordinates into the
//     text the server saw; one keystroke shifts every column after it.
//     Patching them through edits is the syntax grid's trick and is not
//     worth it here — the verb is one key away, and a stale box on the
//     wrong word is the one thing this feature must never show.
//   - Same fill as the word highlight (it IS that highlight, better
//     informed), with writes UNDERLINED. Difference in kind rather than
//     hue, the bracket matcher's rule: read/write has to survive a
//     terminal whose contrast ced cannot vouch for.

package editor

import "github.com/rohanthewiz/ced/internal/theme"

// SymbolUse is one server-resolved use of a symbol, in buffer (rune)
// coordinates. End is exclusive.
type SymbolUse struct {
	Start, End Position
	// Write marks an assignment / declaration rather than a read.
	Write bool
}

// SetSymbolUses installs a semantic highlight set, stamped with the
// current EditRev — the revision the caller's request was answered for.
// An empty set clears.
func (t *Tab) SetSymbolUses(uses []SymbolUse) {
	t.symbolUses = uses
	t.symbolUsesRev = t.EditRev
}

// ClearSymbolUses drops the set, reporting whether there was one — the
// Esc side effect uses the answer to stay silent when it did nothing.
func (t *Tab) ClearSymbolUses() bool {
	had := len(t.symbolUses) > 0
	t.symbolUses = nil
	return had
}

// LiveSymbolUses returns the set while it still describes the buffer,
// or nil once an edit has moved the text out from under it.
func (t *Tab) LiveSymbolUses() []SymbolUse {
	if t == nil || len(t.symbolUses) == 0 || t.symbolUsesRev != t.EditRev {
		return nil
	}
	return t.symbolUses
}

// symbolUseSource paints the live semantic set. A built-in rather than an
// app-registered source because it has to coordinate with the word
// highlight (which stands down for it), and built-ins are where that
// ordering is decided.
type symbolUseSource struct{}

// Decorations returns one span per visible use.
func (symbolUseSource) Decorations(t *Tab, th theme.Theme, firstLine, lastLine int) ([]Span, []GutterMark) {
	uses := t.LiveSymbolUses()
	if len(uses) == 0 || t.IsImage() {
		return nil, nil
	}
	var spans []Span
	for _, u := range uses {
		if u.End.Line < firstLine || u.Start.Line > lastLine {
			continue
		}
		spans = append(spans, Span{
			Start: u.Start,
			End:   u.End,
			Delta: StyleDelta{SetBG: true, BG: th.WordHL, Bold: true, Underline: u.Write},
		})
	}
	return spans, nil
}
