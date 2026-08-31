// =============================================================================
// File: internal/theme/theme.go
// Author: Spicer Matthews <spicer@cloudmanic.com>
// Created: 2026-04-29
// Copyright: 2026 Cloudmanic, LLC. All rights reserved.
// =============================================================================

// Package theme owns every color the editor renders.
//
// It has three layers, one per file:
//
//   - theme.go (here) — the Theme struct the renderers consume, plus
//     Default(), the Tokyo Night palette ced has always shipped with.
//   - palette.go — the color model: a flat map of canonical keys, of
//     which only eight are required, and a derivation table that fills
//     the other twenty-nine.
//   - builtin.go / load.go — ten named themes, and the registry that
//     merges them with the user's own ~/.config/ced/themes/*.json.
//
// The layering is what keeps the rest of the editor unaware that themes
// exist at all: every renderer still takes a plain Theme by value, the
// same as before named themes were added. Switching themes is nothing
// more than assigning a different Theme to App.theme and re-rendering.
//
// Default() is deliberately still a hand-written literal rather than a
// call into the registry. It is the floor the editor falls back to when
// a theme file is broken or a saved preference names something that no
// longer exists, so it must not be able to fail — and a test pins that
// resolving the "tokyo-night" built-in reproduces it exactly.
package theme

import "github.com/gdamore/tcell/v2"

// Theme bundles every color the editor renders. UI surfaces, accents, and
// syntax-highlight colors all live in one struct so that adjusting one
// element of the palette can be balanced against the others.
type Theme struct {
	// --- Surfaces ---
	BG        tcell.Color // Editor background.
	SidebarBG tcell.Color // File tree / inactive tab background, slightly darker than BG.
	StatusBG  tcell.Color // Status bar background.
	LineHL    tcell.Color // Active line highlight.

	// --- Foregrounds & accents ---
	Text       tcell.Color // Primary editor text.
	Muted      tcell.Color // Line numbers, inactive tabs, secondary UI text.
	Subtle     tcell.Color // Even more subtle (separators, hints).
	Accent     tcell.Color // Active tab accent, root label, important UI.
	AccentSoft tcell.Color // Softer accent (active line number).
	Selection  tcell.Color // Selection background.
	Modified   tcell.Color // Dirty indicator (unsaved changes).
	Error      tcell.Color // Error messages.

	// FindMatch / FindCurrent paint search hits in the editor body.
	// FindMatch is a soft tint applied to every match in the viewport;
	// FindCurrent is the louder color drawn under the "active" match
	// (the one Enter/Esc-g will jump past) so the user can find their
	// place at a glance.
	FindMatch   tcell.Color
	FindCurrent tcell.Color

	// WordHL boxes every other instance of the word under the cursor
	// (wordhl.go). Deliberately NEUTRAL rather than accent-tinted:
	// Selection is the editor body's only blue fill, so a highlight can
	// never be mistaken for something the user selected. It still loses
	// every overlap to Selection — that's decoration precedence, not
	// color weight.
	WordHL tcell.Color

	// BracketMatch fills the two cells of the bracket pair the caret is
	// on; BracketUnmatched tints the one bracket that has no partner.
	// Both are set by bracket.go's decoration source. They are a fill
	// and a foreground respectively so the two answers differ in kind,
	// not just in color — see palette.go's derivation for why.
	BracketMatch     tcell.Color
	BracketUnmatched tcell.Color

	// Git gutter marks (the mark column between line numbers and code).
	// Follows the near-universal editor convention: green = added,
	// blue = modified, red = deleted — users read these without a key.
	GitAdded    tcell.Color
	GitModified tcell.Color
	GitDeleted  tcell.Color

	// LSP diagnostics — underline tint + gutter mark per severity.
	// Errors reuse the red family, warnings amber, info/hint the calm
	// blue, so severity reads at a glance without a legend. DiagError
	// is separate from Error (the UI-failure color) so the two can be
	// tuned independently even though they start out close.
	DiagError   tcell.Color
	DiagWarning tcell.Color
	DiagInfo    tcell.Color

	// --- File tree ---
	FolderColor tcell.Color
	FileColor   tcell.Color

	// --- Syntax highlighting ---
	SynKeyword  tcell.Color
	SynString   tcell.Color
	SynNumber   tcell.Color
	SynComment  tcell.Color
	SynFunction tcell.Color
	SynType     tcell.Color
	SynBuiltin  tcell.Color
	SynVariable tcell.Color
	SynOperator tcell.Color
	SynPunct    tcell.Color
	SynConstant tcell.Color
}

// Default returns the editor's original curated dark theme — Tokyo
// Night. It is the fallback used when no preference is set and the
// safety net when a preference can't be honored (unknown name, broken
// user theme file), which is why it stays a literal that cannot fail
// rather than a lookup through the registry. The named-theme equivalent
// is Builtins()[0]; TestBuiltin_TokyoNightMatchesDefault pins the two
// together so this palette and that one can never drift.
func Default() Theme {
	return Theme{
		// Surfaces.
		BG:        tcell.NewRGBColor(0x1a, 0x1b, 0x26),
		SidebarBG: tcell.NewRGBColor(0x16, 0x16, 0x1e),
		StatusBG:  tcell.NewRGBColor(0x7a, 0xa2, 0xf7),
		LineHL:    tcell.NewRGBColor(0x1f, 0x20, 0x2e),

		// Foregrounds & accents.
		Text:       tcell.NewRGBColor(0xc0, 0xca, 0xf5),
		Muted:      tcell.NewRGBColor(0x56, 0x5f, 0x89),
		Subtle:     tcell.NewRGBColor(0x32, 0x34, 0x4a),
		Accent:     tcell.NewRGBColor(0x7a, 0xa2, 0xf7),
		AccentSoft: tcell.NewRGBColor(0xbb, 0x9a, 0xf7),
		Selection:  tcell.NewRGBColor(0x33, 0x46, 0x7c),
		Modified:   tcell.NewRGBColor(0xe8, 0x98, 0x38),
		Error:      tcell.NewRGBColor(0xf7, 0x76, 0x8e),

		// Find. FindMatch is a desaturated amber so it reads as "all
		// hits" without competing with the syntax palette. FindCurrent
		// is full amber — the same shade the dirty indicator uses —
		// so the active match jumps off the page.
		FindMatch:   tcell.NewRGBColor(0x6f, 0x52, 0x1f),
		FindCurrent: tcell.NewRGBColor(0xe8, 0x98, 0x38),

		// Word highlight — a NEUTRAL box (26% text over background), not
		// an accent wash. That's what keeps it visible without looking
		// like the (blue) selection. See palette.go's derivation.
		WordHL: tcell.NewRGBColor(0x45, 0x49, 0x5c),

		// Bracket pair — the same neutral idea one step louder (42%
		// text over background), because it only ever paints two cells.
		// The unmatched tint is the theme's plain error red.
		BracketMatch:     tcell.NewRGBColor(0x60, 0x65, 0x7d),
		BracketUnmatched: tcell.NewRGBColor(0xf7, 0x76, 0x8e),

		// Git gutter — the standard Tokyo Night green / blue / red.
		GitAdded:    tcell.NewRGBColor(0x9e, 0xce, 0x6a),
		GitModified: tcell.NewRGBColor(0x7a, 0xa2, 0xf7),
		GitDeleted:  tcell.NewRGBColor(0xf7, 0x76, 0x8e),

		// Diagnostics — Tokyo Night red / amber / cyan-blue.
		DiagError:   tcell.NewRGBColor(0xf7, 0x76, 0x8e),
		DiagWarning: tcell.NewRGBColor(0xe0, 0xaf, 0x68),
		DiagInfo:    tcell.NewRGBColor(0x7a, 0xa2, 0xf7),

		// Tree.
		FolderColor: tcell.NewRGBColor(0x7a, 0xa2, 0xf7),
		FileColor:   tcell.NewRGBColor(0xa9, 0xb1, 0xd6),

		// Syntax — Tokyo Night-ish.
		SynKeyword:  tcell.NewRGBColor(0xbb, 0x9a, 0xf7), // purple
		SynString:   tcell.NewRGBColor(0x9e, 0xce, 0x6a), // green
		SynNumber:   tcell.NewRGBColor(0xff, 0x9e, 0x64), // orange
		SynComment:  tcell.NewRGBColor(0x56, 0x5f, 0x89), // muted slate
		SynFunction: tcell.NewRGBColor(0x7a, 0xa2, 0xf7), // blue
		SynType:     tcell.NewRGBColor(0x2a, 0xc3, 0xde), // cyan
		SynBuiltin:  tcell.NewRGBColor(0xf7, 0x76, 0x8e), // red
		SynVariable: tcell.NewRGBColor(0xc0, 0xca, 0xf5), // text-like
		SynOperator: tcell.NewRGBColor(0x89, 0xdd, 0xff), // light cyan
		SynPunct:    tcell.NewRGBColor(0xa9, 0xb1, 0xd6), // soft text
		SynConstant: tcell.NewRGBColor(0xff, 0x9e, 0x64), // orange
	}
}
