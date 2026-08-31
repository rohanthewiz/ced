// =============================================================================
// File: internal/editor/highlight.go
// Author: Spicer Matthews <spicer@cloudmanic.com>
// Created: 2026-04-29
// Copyright: 2026 Cloudmanic, LLC. All rights reserved.
// =============================================================================

package editor

import (
	"strings"

	"github.com/alecthomas/chroma/v2"
	"github.com/alecthomas/chroma/v2/lexers"
	"github.com/gdamore/tcell/v2"

	"github.com/rohanthewiz/ced/internal/theme"
)

// Highlight tokenises src using a Chroma lexer chosen by filename (falling
// back to content-based detection, then to a plain-text lexer) and returns a
// per-line slice of styles parallel to the buffer's lines: styles[i][j] is
// the style for rune j on line i.
//
// Returning a per-rune style grid keeps the renderer simple — it just looks
// up the style for each cell it draws — at the cost of some memory.
// For files small enough to comfortably review, that's a fine trade.
func Highlight(filename, src string, t theme.Theme) [][]tcell.Style {
	lexer := lexers.Match(filename)
	if lexer == nil {
		lexer = lexers.Analyse(src)
	}
	if lexer == nil {
		lexer = lexers.Fallback
	}
	// Coalesce merges adjacent same-type tokens; cheaper to scan in render.
	lexer = chroma.Coalesce(lexer)

	base := tcell.StyleDefault.Background(t.BG).Foreground(t.Text)

	// Pre-allocate a styles grid sized to the source. We seed every cell
	// with the base style so untokenised runes still render readably.
	lines := strings.Split(src, "\n")
	styles := make([][]tcell.Style, len(lines))
	for i, ln := range lines {
		runes := []rune(ln)
		row := make([]tcell.Style, len(runes))
		for j := range row {
			row[j] = base
		}
		styles[i] = row
	}

	iter, err := lexer.Tokenise(nil, src)
	if err != nil {
		return styles
	}

	line, col := 0, 0
	for tok := iter(); tok != chroma.EOF; tok = iter() {
		st := styleForToken(tok.Type, t, base)
		for _, r := range tok.Value {
			if r == '\n' {
				line++
				col = 0
				continue
			}
			if line < len(styles) && col < len(styles[line]) {
				styles[line][col] = st
			}
			col++
		}
	}
	return styles
}

// styleForToken maps a Chroma token type to a tcell.Style using the active
// theme. We match by CATEGORY first (Keyword, Name, Comment, …) so the
// mapping stays tight across the dozens of language-specific subtypes —
// with one documented exception, the Literal family, where the category
// is too coarse to tell a string from a number. See the arm below.
func styleForToken(tt chroma.TokenType, t theme.Theme, base tcell.Style) tcell.Style {
	switch tt.Category() {
	case chroma.Keyword:
		return base.Foreground(t.SynKeyword)
	case chroma.Literal:
		// Chroma numbers its token types so that Category() is the
		// THOUSAND block and SubCategory() the hundred: strings are
		// 3100 and numbers 3200, both of which divide down to Literal
		// (3000). So a `case chroma.LiteralString` beside the other
		// category arms can never be reached — which is exactly what
		// used to happen here, and it painted every string and every
		// number in the editor with the constant color. The Literal
		// family is the one place the sub-category has to be asked.
		switch tt.SubCategory() {
		case chroma.LiteralString:
			return base.Foreground(t.SynString)
		case chroma.LiteralNumber:
			return base.Foreground(t.SynNumber)
		}
		return base.Foreground(t.SynConstant)
	case chroma.Comment:
		return base.Foreground(t.SynComment).Italic(true)
	case chroma.Operator:
		return base.Foreground(t.SynOperator)
	case chroma.Punctuation:
		return base.Foreground(t.SynPunct)
	case chroma.Name:
		switch tt {
		case chroma.NameFunction, chroma.NameFunctionMagic:
			return base.Foreground(t.SynFunction)
		case chroma.NameClass, chroma.NameNamespace:
			return base.Foreground(t.SynType)
		case chroma.NameBuiltin, chroma.NameBuiltinPseudo:
			return base.Foreground(t.SynBuiltin)
		case chroma.NameConstant:
			return base.Foreground(t.SynConstant)
		case chroma.NameVariable, chroma.NameVariableInstance,
			chroma.NameVariableClass, chroma.NameVariableGlobal,
			chroma.NameVariableAnonymous:
			return base.Foreground(t.SynVariable)
		case chroma.NameTag:
			return base.Foreground(t.SynType)
		case chroma.NameAttribute:
			return base.Foreground(t.SynVariable)
		}
		return base
	}
	return base
}
