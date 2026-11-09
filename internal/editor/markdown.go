// =============================================================================
// File: internal/editor/markdown.go
// Author: Rohan Allison
// Created: 2026-09-09
// Copyright: 2026 Cloudmanic, LLC. All rights reserved.
// =============================================================================

// markdown.go gives a text Tab a second way to be LOOKED at: the same
// buffer, rendered as a formatted document instead of as source. It is a
// VIEW, not a mode change — the buffer, the undo history, the dirty flag,
// the cursor and the find state are all untouched underneath, so toggling
// back lands the user exactly where they were.
//
// That is the whole reason this is a flag on Tab rather than a second
// Mode like the image viewer's. An image tab has no text to go back to,
// so imageMode short-circuits every mutating method and Save refuses;
// a markdown tab is an ordinary editable file that happens to be drawn
// prettily for a moment. Nothing here may write to the buffer.
//
// Layout is DERIVED, never stored: RenderMarkdown turns the buffer's
// lines into display rows for a given width, and the rows are cached
// against (EditRev, width) so a resize re-flows for free and a keystroke
// in edit mode invalidates it. Same shape as the chat transcript's
// chatRows — the model is the text, the rows are a function of it.
//
// Scrolling gets its own counter (MDScroll) rather than borrowing
// ScrollY. A rendered row is not a buffer line (a paragraph becomes
// several, a heading gains a rule, a fence loses its markers), so
// sharing the field would mean the edit view's scroll position was
// silently rewritten by every scroll of the preview — and restoring the
// view the user left is the point of a toggle.
//
// What this renderer covers: ATX and setext headings, fenced and
// indented code (syntax-highlighted through Chroma when the fence names
// a language), block quotes to any depth, ordered / unordered / task
// lists, GFM pipe tables, thematic breaks, YAML front matter, and the
// inline run — emphasis, strong, strikethrough, code spans, links,
// images and autolinks. It is a READER, so nothing here is round-
// trippable and nothing needs to be: the source is one keystroke away.

package editor

import (
	"math"
	"path/filepath"
	"strings"

	"github.com/gdamore/tcell/v2"

	"github.com/rohanthewiz/ced/internal/theme"
)

// mdMaxInlineDepth bounds the inline parser's recursion. Emphasis nests
// legitimately ("**bold with *em* inside**") but only a few levels deep
// in real prose; a bound keeps a pathological line of asterisks from
// costing a stack rather than a paragraph.
const mdMaxInlineDepth = 6

// mdMaxQuoteDepth bounds block-quote recursion for the same reason. A
// deeper quote still renders — it just stops gaining bars.
const mdMaxQuoteDepth = 6

// mdMinContent is the narrowest window the viewer will try to lay a
// document out in. Below it the rows would be one word each, so the
// view refuses and the app keeps showing source (see MarkdownRows).
const mdMinContent = 20

// MDRow is one rendered display row: the runes to paint and a parallel
// style slice, exactly the shape Tab.Render already draws for code.
// Src is the 0-based buffer line the row came from, or -1 for a row the
// renderer synthesized (a heading's underline rule, a table border, the
// blank between two paragraphs). It is what makes the preview a
// NAVIGATION surface — clicking a row puts the caret on the line that
// produced it — so every row that has an honest answer carries one.
type MDRow struct {
	Runes  []rune
	Styles []tcell.Style
	Src    int
}

// mdSpan is a run of text sharing one style, the inline parser's output.
type mdSpan struct {
	text  string
	style tcell.Style
}

// mdCell is one rune with its style. Word wrapping happens at this
// granularity because a single word can carry several styles ("**bo**ld")
// and breaking a line must not lose them.
type mdCell struct {
	r  rune
	st tcell.Style
}

// mdPalette is the set of styles the renderer paints with, derived from
// the theme once per render rather than per row.
//
// The choices worth defending: code — both fenced blocks and inline
// spans — takes MDCodeBG, a derived surface of its own (see
// theme/palette.go). It is NOT LineHL: that color is a few units off the
// background by design, which is right for a one-row cursor wash and
// invisible across a twenty-row block — the mistake a "this is ambient,
// keep it quiet" choice makes. Headings take Accent and lean on BOLD
// as well as hue, since a terminal's contrast is not ours to assume and
// a heading that differs from body text by hue alone disappears on a
// washed-out profile. Links are underlined for the same reason.
//
// Every TEXT role is lifted to inkFloor against the surface it actually
// sits on (see mdPaletteFor). The preview is a reading surface, so it
// holds itself to more than the 4.5:1 the rest of the editor settles for:
// body text aims at mdBodyContrast and everything else at mdInkContrast.
// Pure decoration — hr, table rules, the quote bar — is left alone: a
// line needs to be seen, not read.
//
// Prose roles deliberately take NO colour from the syntax family.
// accent-soft and syn-constant are tuned to mark keywords and constants
// in code; in most palettes that means purple, so borrowing them tinted
// every inline span, h2 rule, h3 and bullet magenta (and olive on a green
// accent, where accent-soft is a blend toward err). The slab alone says
// "this is code", so inline spans wear neutral ink; the single accent
// carries the whole heading/bullet/quote-bar structure. Fenced blocks are
// the exception that keeps its hues: there the colours are real syntax.
type mdPalette struct {
	base    tcell.Style
	h1, h2  tcell.Style
	h3, hN  tcell.Style
	rule1   tcell.Style
	rule2   tcell.Style
	hr      tcell.Style
	code    tcell.Style // fenced/indented block background
	inline  tcell.Style // `code span`
	quote   tcell.Style // quoted text
	bar     tcell.Style // the ▎ quote bar
	bullet  tcell.Style
	link    tcell.Style
	image   tcell.Style
	meta    tcell.Style // front matter, raw HTML
	tblRule tcell.Style
	tblHead tcell.Style
	check   tcell.Style
}

// mdPaletteFor derives the render styles from the active theme.
//
// Each text colour goes through ink, which lifts it (same hue, paler on
// a dark theme, deeper on a light one) until it reads at inkFloor on
// its own background. Colours that already clear the floor come back
// untouched, so a high-contrast theme keeps its exact inks.
//
// A heading's rule and a list's bullet wear the LIFTED accent, so the
// underline never drifts a shade away from the word above it. The quote
// bar stays raw accent: decoration.
func mdPaletteFor(th theme.Theme) mdPalette {
	floor := inkFloor(th)
	ink := func(fg, bg tcell.Color) tcell.Color { return theme.LiftToContrast(fg, bg, floor) }

	accent := ink(th.Accent, th.BG)
	muted := ink(th.Muted, th.BG)

	base := tcell.StyleDefault.Background(th.BG).Foreground(mdBodyInk(th))
	code := tcell.StyleDefault.Background(th.MDCodeBG).Foreground(ink(th.Text, th.MDCodeBG))
	return mdPalette{
		base: base,
		h1:   base.Foreground(accent).Bold(true),
		h2:   base.Foreground(accent).Bold(true),
		// h3 shares the accent with h1/h2. The tiers stay apart by their
		// rules (heavy, light, none), which survive any palette; a second
		// hue here is what used to bring the keyword colour into prose.
		h3:      base.Foreground(accent).Bold(true),
		hN:      base.Bold(true),
		rule1:   base.Foreground(accent),
		rule2:   base.Foreground(accent),
		hr:      base.Foreground(th.Subtle),
		code:    code,
		inline:  code, // the slab marks it; a hue on top only adds noise
		quote:   base.Foreground(muted).Italic(true),
		bar:     base.Foreground(th.Accent),
		bullet:  base.Foreground(accent),
		link:    base.Foreground(accent).Underline(true),
		image:   base.Foreground(muted),
		meta:    base.Foreground(muted),
		tblRule: base.Foreground(th.Subtle),
		tblHead: base.Foreground(accent).Bold(true),
		check:   base.Foreground(ink(th.GitAdded, th.BG)).Bold(true),
	}
}

// The preview's contrast targets. Between WCAG AA (4.5) and what a dark
// theme's fg usually reaches (11+), so strong palettes are left alone and
// only the dim ones move. Body text sits a tier above the other roles on
// purpose: it is what the user came to read, and keeping it brightest
// means a lifted quote or heading can never out-shout the prose.
const (
	mdBodyContrast = 9.0
	mdInkContrast  = 7.0
)

// mdBodyInk is the colour of the document's prose: the theme's fg, lifted
// to mdBodyContrast on the page background.
func mdBodyInk(th theme.Theme) tcell.Color {
	return theme.LiftToContrast(th.Text, th.BG, mdBodyContrast)
}

// inkFloor is the contrast every non-body text role is lifted to:
// mdInkContrast, or what the body text actually achieves when that is
// LOWER. The second half only bites on a mid-tone background, where even
// white or black cannot reach the target; aiming headings and quotes past
// the prose there would invert the hierarchy.
//
// The floor is the TARGET; LiftToContrast moves in tenth steps and can
// land up to one step past it. Close enough that hue and italics still
// carry the hierarchy, and it keeps one lift shared by every surface.
func inkFloor(th theme.Theme) float64 {
	return math.Min(mdInkContrast, theme.ContrastRatio(mdBodyInk(th), th.BG))
}

// slabInk lifts a fenced block's syntax colour so the code slab costs it
// nothing: the target is the contrast the colour has on the PAGE
// background (what the editor shows), capped at inkFloor.
//
// Deliberately not inkFloor outright. Syntax colours are the theme's
// own tuning against bg, and their spread is information — Darcula's
// #808080 comments sit at 3.6:1 so they recede behind the code. Lifting
// every token to 4.5 would pull comments up level with the code and
// flatten that. The slab is ced's addition, so ced gives back exactly
// what it took and no more.
//
// cache is keyed by the raw foreground; a block uses a handful of
// colours, and a long document repaints every block on each re-flow.
func slabInk(th theme.Theme, floor float64, st tcell.Style, cache map[tcell.Color]tcell.Color) tcell.Style {
	fg, _, _ := st.Decompose()
	if fg == tcell.ColorDefault {
		return st
	}
	lifted, ok := cache[fg]
	if !ok {
		target := math.Min(floor, theme.ContrastRatio(fg, th.BG))
		lifted = theme.LiftToContrast(fg, th.MDCodeBG, target)
		cache[fg] = lifted
	}
	return st.Foreground(lifted)
}

// IsMarkdownPath reports whether path is a file the viewer knows how to
// render. Extension-based and case-insensitive, the isImageExt rule:
// content sniffing would have to guess, and a document that opens as a
// preview when the user expected source is a worse failure than one that
// does not offer the preview at all.
func IsMarkdownPath(path string) bool {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".md", ".markdown", ".mdown", ".mkd", ".mkdn", ".mdwn":
		return true
	}
	return false
}

// MarkdownCapable reports whether this tab can be previewed: a real text
// buffer whose path looks like markdown. Image tabs and untitled
// scratch buffers are out — the first has no text, the second has no
// extension to judge by.
func (t *Tab) MarkdownCapable() bool {
	return t != nil && t.Buffer != nil && !t.IsImage() && IsMarkdownPath(t.Path)
}

// IsMarkdownView reports whether the tab is currently being DRAWN as a
// rendered document. Callers use it the way they use IsImage: to skip
// caret work and editing on a surface that has neither.
func (t *Tab) IsMarkdownView() bool {
	return t != nil && t.mdView
}

// SetMarkdownView turns the preview on or off. Turning it ON parks the
// scroll at the row that best matches the cursor's line, so the toggle
// shows the passage the user was editing rather than the top of the
// file; turning it OFF leaves the edit view exactly as it was, which is
// the whole reason MDScroll is a separate counter.
//
// The cached rows are dropped on the way out rather than kept warm: a
// preview the user closed may not be reopened for the rest of the
// session, and a long document's rows are the largest thing this feature
// allocates.
func (t *Tab) SetMarkdownView(on bool) {
	if t == nil || t.mdView == on {
		return
	}
	t.mdView = on
	if !on {
		t.InvalidateMarkdown()
		return
	}
	t.MDScroll = 0
	t.mdPendingSync = true
}

// InvalidateMarkdown drops the cached rows so the next draw re-renders.
// Called by the theme switch (the colors are baked into the rows) and
// whenever the view is closed. Edits invalidate through the EditRev half
// of the cache key and need no call here.
func (t *Tab) InvalidateMarkdown() {
	if t == nil {
		return
	}
	t.mdRows = nil
	t.mdValid = false
}

// MarkdownRows returns the document's display rows for a content width,
// rendering and caching on a miss.
//
// It is exported because DRAW AND HIT-TESTING MUST SHARE ONE ANSWER —
// the btnRect rule. The app scrolls by rows, maps a click to a row, and
// counts off-screen rows for the overflow markers, all outside the draw;
// a second layout pass for those would drift from the one on screen the
// moment a wrap boundary moved.
//
// A window too narrow to lay out returns nil, and the caller falls back
// to the source view. Wrapping prose into a six-column column is not a
// degraded preview, it is an unreadable one.
func (t *Tab) MarkdownRows(th theme.Theme, width int) []MDRow {
	if t == nil || t.Buffer == nil || width < mdMinContent {
		return nil
	}
	if t.mdValid && t.mdWidth == width && t.mdRev == t.EditRev {
		return t.mdRows
	}
	t.mdRows = RenderMarkdown(t.Buffer.Lines, width, th)
	t.mdWidth = width
	t.mdRev = t.EditRev
	t.mdValid = true
	return t.mdRows
}

// MDContentWidth is the layout width a pane of paneW cells gives the
// document: one column of left margin so text never starts hard against
// the pane edge, and one on the right so a full-width row still leaves
// the overflow marker's cell legible. Exported because the app has to
// ask MarkdownRows the same question the draw does — a hit-test laid out
// against a different width would map clicks to the wrong row.
func MDContentWidth(paneW int) int {
	return paneW - 2
}

// MDMaxScroll is MaxScroll for the preview: the largest MDScroll that
// still leaves something on screen, with the same overscroll pad the
// source view gets so the end of a document can be pulled up to the
// middle of the viewport.
func (t *Tab) MDMaxScroll(total, viewH int) int {
	overscroll := viewH / 2
	if overscroll < 3 {
		overscroll = 3
	}
	max := total - viewH + overscroll
	if max < 0 {
		max = 0
	}
	return max
}

// MDScrollBy moves the preview viewport by delta rows, clamped. Kept
// here beside Scroll so the app's wheel dispatcher can treat the two
// views symmetrically.
func (t *Tab) MDScrollBy(delta, total, viewH int) {
	if t == nil {
		return
	}
	t.MDScroll += delta
	if t.MDScroll < 0 {
		t.MDScroll = 0
	}
	if max := t.MDMaxScroll(total, viewH); t.MDScroll > max {
		t.MDScroll = max
	}
}

// MDRowForLine finds the display row that best represents a buffer line:
// the first row carrying that Src, or failing that the last row from an
// EARLIER line. The fallback is what makes the answer useful for a line
// that produced no row of its own — a fence marker, a blank inside a
// paragraph — where "just above where that text ended up" is the honest
// place to land.
func MDRowForLine(rows []MDRow, line int) int {
	best := 0
	for i, r := range rows {
		if r.Src == line {
			return i
		}
		if r.Src >= 0 && r.Src < line {
			best = i
		}
	}
	return best
}

// MDLineForRow is the inverse: which buffer line a display row came
// from. Synthesized rows have no line of their own, so the search walks
// BACKWARDS to the nearest row that does — a click on a heading's
// underline rule means the heading.
func MDLineForRow(rows []MDRow, row int) int {
	if row < 0 || row >= len(rows) {
		return -1
	}
	for i := row; i >= 0; i-- {
		if rows[i].Src >= 0 {
			return rows[i].Src
		}
	}
	return 0
}

// renderMarkdown paints the preview into (x, y, w, h). Called from
// Tab.Render when the view is on and the rows laid out; the caller has
// already decided the window is wide enough.
//
// The cursor is hidden for the image viewer's reason: there is no caret
// on this surface, and a hardware cursor parked wherever the edit view
// left it would be a blinking claim that typing does something.
func (t *Tab) renderMarkdown(scr tcell.Screen, th theme.Theme, x, y, w, h int, rows []MDRow) {
	bgStyle := tcell.StyleDefault.Background(th.BG).Foreground(th.Text)
	for cy := y; cy < y+h; cy++ {
		for cx := x; cx < x+w; cx++ {
			scr.SetContent(cx, cy, ' ', nil, bgStyle)
		}
	}

	// A pending sync is the toggle asking to open on the passage the
	// cursor was in. It is resolved HERE rather than in SetMarkdownView
	// because the rows only exist once a width is known, and the width
	// is the draw's to know.
	if t.mdPendingSync {
		t.mdPendingSync = false
		row := MDRowForLine(rows, t.Cursor.Line)
		// Park the target a third of the way down rather than at the
		// top: CenterOnCursor's argument — a line pinned to the first
		// row has no context above it, which is exactly what a reader
		// arriving at a passage wants.
		t.MDScroll = row - h/3
		if t.MDScroll < 0 {
			t.MDScroll = 0
		}
	}
	if max := t.MDMaxScroll(len(rows), h); t.MDScroll > max {
		t.MDScroll = max
	}
	if t.MDScroll < 0 {
		t.MDScroll = 0
	}

	// A one-cell left margin, so text never starts hard against the
	// pane edge and a code block's background reads as a block rather
	// than as a stripe running off the side.
	for i := 0; i < h; i++ {
		idx := t.MDScroll + i
		if idx < 0 || idx >= len(rows) {
			break
		}
		row := rows[idx]
		for j, r := range row.Runes {
			cx := x + 1 + j
			if cx >= x+w {
				break
			}
			st := bgStyle
			if j < len(row.Styles) {
				st = row.Styles[j]
			}
			scr.SetContent(cx, y+i, r, nil, st)
		}
	}
	scr.HideCursor()
}
