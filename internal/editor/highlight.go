// =============================================================================
// File: internal/editor/highlight.go
// Author: Rohan Allison <rohanthewiz@gmail.com>
// Created: 2026-04-29
// Copyright: 2026 Rohan Allison. All rights reserved.
// Portions copyright 2026 Cloudmanic, LLC. Original author: Spicer Matthews.
// =============================================================================

// highlight.go turns source text into a syntax grid: one CLASS per rune,
// resolved to a colour only when a cell is painted (N-129).
//
// The grid used to hold a resolved tcell.Style per rune — 72 bytes each.
// app.go's 261KB of text kept a 19.1MB grid resident (75× the text) to
// describe nine distinct styles, and every lex allocated ~59MB. A class is
// one byte:
//
//	Chroma token ──classForToken──► SynClass (uint8)  ─┐  per rune, in Tab.Classes
//	                                                    │
//	theme + base ──NewSynPalette──► SynPalette ─────────┴─► style, at paint time
//
// Two things follow from keeping the theme OUT of the grid:
//
//   - A theme switch no longer needs a re-lex: the next frame builds a new
//     palette (12 styles) and paints the same grid in the new colours.
//   - "Is this rune inside a string or a comment?" (brace matching) is a
//     class comparison, not a colour comparison that broke down whenever a
//     theme gave syn-string the plain text colour.
//
// Highlight / HighlightLang still return resolved styles, for callers that
// cache a few rows of their own (markdown code fences) and for tests.

package editor

import (
	"strings"
	"unicode/utf8"

	"github.com/alecthomas/chroma/v2"
	"github.com/alecthomas/chroma/v2/lexers"
	"github.com/gdamore/tcell/v2"

	"github.com/rohanthewiz/ced/internal/theme"
)

// SynClass is one rune's syntax role, an index into a SynPalette. The
// zero value is SynPlain, so a freshly made row reads as unstyled text.
type SynClass uint8

// The classes, one per distinct style the theme gives syntax. The set is
// the theme's syn-* keys; adding a class means adding its palette entry.
const (
	SynPlain SynClass = iota
	SynKeyword
	SynString
	SynNumber
	SynConstant
	SynComment
	SynOperator
	SynPunct
	SynFunction
	SynType
	SynBuiltin
	SynVariable
	synClassCount
)

// SynPalette resolves every class to the style it paints with, for one
// theme and one base style (what unstyled runes keep: the editor's
// background, or a markdown code slab's).
type SynPalette [synClassCount]tcell.Style

// NewSynPalette builds the palette for a theme over base. Twelve style
// constructions — cheap enough to build per frame, which is what lets a
// theme switch skip the re-lex.
func NewSynPalette(t theme.Theme, base tcell.Style) SynPalette {
	var p SynPalette
	p[SynPlain] = base
	p[SynKeyword] = base.Foreground(t.SynKeyword)
	p[SynString] = base.Foreground(t.SynString)
	p[SynNumber] = base.Foreground(t.SynNumber)
	p[SynConstant] = base.Foreground(t.SynConstant)
	p[SynComment] = base.Foreground(t.SynComment).Italic(true)
	p[SynOperator] = base.Foreground(t.SynOperator)
	p[SynPunct] = base.Foreground(t.SynPunct)
	p[SynFunction] = base.Foreground(t.SynFunction)
	p[SynType] = base.Foreground(t.SynType)
	p[SynBuiltin] = base.Foreground(t.SynBuiltin)
	p[SynVariable] = base.Foreground(t.SynVariable)
	return p
}

// EditorBase is the style an unstyled rune in the editor pane keeps: the
// theme's text on its background.
func EditorBase(t theme.Theme) tcell.Style {
	return tcell.StyleDefault.Background(t.BG).Foreground(t.Text)
}

// HighlightClasses tokenises src with a Chroma lexer chosen by filename
// (falling back to content-based detection, then to a plain-text lexer)
// and returns the class grid, parallel to the buffer's lines: grid[i][j]
// is the class of rune j on line i. Theme-free by design — see the file
// comment.
func HighlightClasses(filename, src string) [][]SynClass {
	return classesWith(lexerForFile(filename, src), src)
}

// Highlight is HighlightClasses resolved against a theme over the editor
// base: a per-line slice of styles parallel to the buffer's lines.
func Highlight(filename, src string, t theme.Theme) [][]tcell.Style {
	return ResolveClasses(HighlightClasses(filename, src), NewSynPalette(t, EditorBase(t)))
}

// HighlightLang is Highlight for source that has no filename to match on —
// the body of a fenced code block, whose language arrives as the fence's
// info string ("go", "bash", "json") rather than as an extension. It
// resolves the lexer by NAME first (Chroma's registry is alias-aware, so
// "golang", "sh" and "js" all land) and falls back to content analysis and
// then plain text, exactly as Highlight does for an unknown extension.
//
// base is the style unstyled runes keep. The markdown viewer paints code
// blocks on their own background, so it passes a style whose background is
// the block's — a base of tcell.StyleDefault would punch the editor's
// background through every space between two tokens.
func HighlightLang(lang, src string, t theme.Theme, base tcell.Style) [][]tcell.Style {
	var lexer chroma.Lexer
	if lang != "" {
		lexer = lexers.Get(lang)
	}
	if lexer == nil {
		lexer = lexers.Analyse(src)
	}
	if lexer == nil {
		lexer = lexers.Fallback
	}
	return ResolveClasses(classesWith(lexer, src), NewSynPalette(t, base))
}

// ResolveClasses maps a class grid through a palette into styles.
func ResolveClasses(grid [][]SynClass, p SynPalette) [][]tcell.Style {
	out := make([][]tcell.Style, len(grid))
	for i, row := range grid {
		styles := make([]tcell.Style, len(row))
		for j, c := range row {
			styles[j] = p[c]
		}
		out[i] = styles
	}
	return out
}

// lexerForFile picks the lexer Highlight uses: by file name, then by
// content, then plain text.
func lexerForFile(filename, src string) chroma.Lexer {
	lexer := lexers.Match(filename)
	if lexer == nil {
		lexer = lexers.Analyse(src)
	}
	if lexer == nil {
		lexer = lexers.Fallback
	}
	return lexer
}

// classesWith is the shared tokenise-and-fill pass behind every entry
// point. One token walk, so the grid brace matching reads to decide
// "inside a string?" is the same grid the renderer paints.
func classesWith(lexer chroma.Lexer, src string) [][]SynClass {
	// Coalesce merges adjacent same-type tokens; fewer, longer tokens.
	lexer = chroma.Coalesce(lexer)

	// Rows sized to each line's rune count up front. make() zeroes them,
	// and zero is SynPlain — untokenised runes paint as plain text.
	lines := strings.Split(src, "\n")
	grid := make([][]SynClass, len(lines))
	for i, ln := range lines {
		grid[i] = make([]SynClass, utf8.RuneCountInString(ln))
	}

	iter, err := lexer.Tokenise(nil, src)
	if err != nil {
		return grid
	}

	line, col := 0, 0
	for tok := iter(); tok != chroma.EOF; tok = iter() {
		c := classForToken(tok.Type)
		for _, r := range tok.Value {
			if r == '\n' {
				line++
				col = 0
				continue
			}
			if line < len(grid) && col < len(grid[line]) {
				grid[line][col] = c
			}
			col++
		}
	}
	return grid
}

// classForToken maps a Chroma token type to a class. We match by CATEGORY
// first (Keyword, Name, Comment, …) so the mapping stays tight across the
// dozens of language-specific subtypes — with one documented exception,
// the Literal family, where the category is too coarse to tell a string
// from a number. See the arm below.
func classForToken(tt chroma.TokenType) SynClass {
	switch tt.Category() {
	case chroma.Keyword:
		return SynKeyword
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
			return SynString
		case chroma.LiteralNumber:
			return SynNumber
		}
		return SynConstant
	case chroma.Comment:
		return SynComment
	case chroma.Operator:
		return SynOperator
	case chroma.Punctuation:
		return SynPunct
	case chroma.Name:
		switch tt {
		case chroma.NameFunction, chroma.NameFunctionMagic:
			return SynFunction
		case chroma.NameClass, chroma.NameNamespace:
			return SynType
		case chroma.NameBuiltin, chroma.NameBuiltinPseudo:
			return SynBuiltin
		case chroma.NameConstant:
			return SynConstant
		case chroma.NameVariable, chroma.NameVariableInstance,
			chroma.NameVariableClass, chroma.NameVariableGlobal,
			chroma.NameVariableAnonymous:
			return SynVariable
		case chroma.NameTag:
			return SynType
		case chroma.NameAttribute:
			return SynVariable
		}
		return SynPlain
	}
	return SynPlain
}

// styleForToken resolves one token type to its style for a theme over
// base — classForToken through a palette. Kept for the callers (and the
// tests) that think in styles rather than classes.
func styleForToken(tt chroma.TokenType, t theme.Theme, base tcell.Style) tcell.Style {
	return NewSynPalette(t, base)[classForToken(tt)]
}
