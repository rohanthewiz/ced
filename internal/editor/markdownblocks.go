// =============================================================================
// File: internal/editor/markdownblocks.go
// Author: Rohan Allison
// Created: 2026-09-09
// Copyright: 2026 Cloudmanic, LLC. All rights reserved.
// =============================================================================

// markdownblocks.go is the block half of the markdown viewer: buffer
// lines in, display rows out. Inline styling within a block is
// markdowninline.go's job.
//
// The pass is a single forward walk with lookahead — no document tree.
// A tree would buy nesting this renderer does not need: the only
// construct that genuinely nests is the block quote, which recurses into
// a fresh renderer over its own stripped lines and prefixes the rows
// that come back. Everything else is flat, and a flat walk is what makes
// EVERY ROW ABLE TO NAME ITS SOURCE LINE — the property that turns the
// preview from a picture into a place you can click.
//
// Two rules hold throughout:
//
//   - Prose WORD-wraps, code HARD-wraps. The chat transcript's split,
//     for its reason: a code line's columns are meaningful, so
//     re-flowing one misrepresents it, while a paragraph's are not.
//   - A row is padded out to the block's width ONLY when the block has
//     its own background (code, table header). Padding everything would
//     make a selection-less viewer look like it had a selection.

package editor

import (
	"strconv"
	"strings"

	"github.com/gdamore/tcell/v2"

	"github.com/rohanthewiz/ced/internal/theme"
)

// mdBulletRunes are the unordered-list markers, indexed by nesting
// depth. Single-width glyphs per the marker rule — a double-width
// bullet would shift every wrapped continuation line off its hanging
// indent, since the wrapper counts runes as columns.
var mdBulletRunes = []string{"•", "◦", "▪", "‣"}

// RenderMarkdown lays a document out for a given content width. It is
// pure: same lines, width and theme in, same rows out, nothing cached
// and nothing on the Tab touched — which is what lets the tests drive it
// directly and the cache above it stay a plain memo.
func RenderMarkdown(lines []string, width int, th theme.Theme) []MDRow {
	r := &mdRenderer{
		lines: lines,
		width: width,
		pal:   mdPaletteFor(th),
		th:    th,
	}
	r.run()
	return r.out
}

// mdRenderer carries the walk's state. srcBase offsets Src for a nested
// render (a block quote's inner lines are a sub-slice, so their indices
// have to be shifted back into the real buffer's numbering).
type mdRenderer struct {
	lines   []string
	width   int
	pal     mdPalette
	th      theme.Theme
	out     []MDRow
	srcBase int
	depth   int // quote nesting, bounded by mdMaxQuoteDepth
}

// run is the block walk.
func (r *mdRenderer) run() {
	i := 0
	// YAML front matter, and only at the very top — a "---" anywhere
	// else is a thematic break, and treating one as metadata would
	// silently swallow the rest of the document.
	if len(r.lines) > 0 && strings.TrimSpace(r.lines[0]) == "---" {
		i = r.frontMatter()
	}
	for i < len(r.lines) {
		i = r.block(i)
	}
	r.trimTrailingBlanks()
}

// block dispatches one block starting at line i and returns the index of
// the first line after it. Order is the grammar: code fences swallow
// their contents whole (so a "# " inside one stays code), tables need
// two lines to be recognised at all, and the paragraph consumer is last
// because it is what everything falls through to.
func (r *mdRenderer) block(i int) int {
	ln := r.lines[i]
	if strings.TrimSpace(ln) == "" {
		r.blank()
		return i + 1
	}
	if fence, indent, lang, ok := mdFenceOpen(ln); ok {
		return r.fencedCode(i, fence, indent, lang)
	}
	if lvl, text, ok := mdATXHeading(ln); ok {
		r.heading(i, lvl, text)
		return i + 1
	}
	if mdThematicBreak(ln) {
		r.thematicBreak(i)
		return i + 1
	}
	if mdQuoteDepth(ln) > 0 {
		return r.blockQuote(i)
	}
	if cols, aligns, ok := r.tableAt(i); ok {
		return r.table(i, cols, aligns)
	}
	if _, _, _, ok := mdListItem(ln); ok {
		return r.listItem(i)
	}
	if mdIndentedCode(ln) {
		return r.indentedCode(i)
	}
	return r.paragraph(i)
}

// ---------------------------------------------------------------------
// Emitters
// ---------------------------------------------------------------------

// emit appends one row built from cells, recording its source line.
func (r *mdRenderer) emit(src int, cells []mdCell) {
	if src >= 0 {
		src += r.srcBase
	}
	runes := make([]rune, len(cells))
	styles := make([]tcell.Style, len(cells))
	for i, c := range cells {
		runes[i] = c.r
		styles[i] = c.st
	}
	r.out = append(r.out, MDRow{Runes: runes, Styles: styles, Src: src})
}

// blank emits one empty row, collapsing a run of them. Markdown treats
// any number of blank lines as one paragraph break, and a document
// written with generous spacing would otherwise render as mostly air.
func (r *mdRenderer) blank() {
	if n := len(r.out); n > 0 && len(r.out[n-1].Runes) == 0 {
		return
	}
	r.out = append(r.out, MDRow{Src: -1})
}

// trimTrailingBlanks drops the blank rows a document's trailing newlines
// produce, so the overflow marker's "N rows below" is not counting air.
func (r *mdRenderer) trimTrailingBlanks() {
	for len(r.out) > 0 && len(r.out[len(r.out)-1].Runes) == 0 {
		r.out = r.out[:len(r.out)-1]
	}
}

// emitWrapped word-wraps spans into rows, prefixing the first row with
// first and every continuation with cont. The two prefixes are what give
// a list item its hanging indent and a block quote its bar on every row.
func (r *mdRenderer) emitWrapped(src int, first, cont []mdCell, spans []mdSpan) {
	body := spansToCells(spans)
	avail := r.width - len(first)
	if avail < 4 {
		avail = 4
	}
	chunks := wrapCells(body, avail)
	if len(chunks) == 0 {
		r.emit(src, first)
		return
	}
	for n, chunk := range chunks {
		prefix := cont
		if n == 0 {
			prefix = first
		}
		row := make([]mdCell, 0, len(prefix)+len(chunk))
		row = append(row, prefix...)
		row = append(row, chunk...)
		// Only the first row of a wrapped block carries the source line:
		// a continuation is the same line's text, so claiming it again
		// would make MDRowForLine land on the middle of a paragraph.
		s := src
		if n > 0 {
			s = -1
		}
		r.emit(s, row)
	}
}

// ---------------------------------------------------------------------
// Blocks
// ---------------------------------------------------------------------

// frontMatter renders a leading YAML block as muted metadata, fenced
// above and below by rules. It is shown rather than hidden because it is
// content the author wrote and often the only place a document's title
// lives; it is muted because it is not prose.
func (r *mdRenderer) frontMatter() int {
	end := len(r.lines)
	for j := 1; j < len(r.lines); j++ {
		t := strings.TrimSpace(r.lines[j])
		if t == "---" || t == "..." {
			end = j
			break
		}
	}
	if end == len(r.lines) {
		// No closing marker: it was a thematic break after all, so hand
		// the line back to the ordinary walk rather than eating the file.
		return 0
	}
	for j := 1; j < end; j++ {
		r.emit(j, styleCells(strings.TrimRight(r.lines[j], " \t"), r.pal.meta))
	}
	r.blank()
	return end + 1
}

// heading renders an ATX or setext heading, with a rule beneath the top
// two levels. The rule is what carries the hierarchy on a terminal that
// renders neither bold nor color faithfully — the one cue that survives
// every profile.
func (r *mdRenderer) heading(src, lvl int, text string) {
	// A blank above, so a heading is never glued to the paragraph it
	// interrupts. Skipped at the very top of a document.
	if len(r.out) > 0 {
		r.blank()
	}
	st := r.pal.hN
	switch lvl {
	case 1:
		st = r.pal.h1
	case 2:
		st = r.pal.h2
	case 3:
		st = r.pal.h3
	}
	spans := parseInline(text, st, r.pal, 0)
	r.emitWrapped(src, nil, nil, spans)
	switch lvl {
	case 1:
		r.emit(-1, repeatCells('━', r.width, r.pal.rule1))
	case 2:
		r.emit(-1, repeatCells('─', r.width, r.pal.rule2))
	}
}

// thematicBreak renders "---" / "***" / "___" as a full-width rule.
func (r *mdRenderer) thematicBreak(src int) {
	r.blank()
	r.emit(src, repeatCells('─', r.width, r.pal.hr))
	r.blank()
}

// fencedCode renders a ``` / ~~~ block. The body is handed to Chroma
// when the fence names a language, which is why this viewer bothers to
// live in package editor at all — the highlighter is right here, and a
// code block rendered as flat text is the single biggest thing a
// markdown reader for PROGRAMMERS could get wrong.
//
// The block paints its own background across the full width, so a fence
// reads as a slab rather than as differently-colored prose. Rows hard-
// wrap: a code line's columns mean something.
func (r *mdRenderer) fencedCode(i int, fence string, indent int, lang string) int {
	body := make([]string, 0, 8)
	srcs := make([]int, 0, 8)
	j := i + 1
	for ; j < len(r.lines); j++ {
		if mdFenceCloses(r.lines[j], fence) {
			break
		}
		body = append(body, mdStripIndent(r.lines[j], indent))
		srcs = append(srcs, j)
	}
	r.codeRows(body, srcs, lang)
	if j < len(r.lines) {
		return j + 1 // consume the closing fence
	}
	return j // unterminated fence: the rest of the file was the block
}

// indentedCode renders a four-space-indented block, which has no
// language to name and so is never highlighted.
func (r *mdRenderer) indentedCode(i int) int {
	body := make([]string, 0, 8)
	srcs := make([]int, 0, 8)
	j := i
	for ; j < len(r.lines); j++ {
		if strings.TrimSpace(r.lines[j]) == "" {
			// A blank inside an indented block only continues it if more
			// indented code follows; otherwise the block has ended.
			if k := j + 1; k < len(r.lines) && mdIndentedCode(r.lines[k]) {
				body = append(body, "")
				srcs = append(srcs, j)
				continue
			}
			break
		}
		if !mdIndentedCode(r.lines[j]) {
			break
		}
		body = append(body, mdStripIndent(r.lines[j], 4))
		srcs = append(srcs, j)
	}
	r.codeRows(body, srcs, "")
	return j
}

// codeRows paints a code block: an optional language chip, then the
// highlighted body on the block background, hard-wrapped.
func (r *mdRenderer) codeRows(body []string, srcs []int, lang string) {
	if len(body) == 0 {
		return
	}
	r.blank()
	if lang != "" {
		// The language is worth a row: it is what the highlighting is
		// keyed on, so showing it makes a wrongly-tagged fence
		// diagnosable instead of merely odd-looking.
		r.emit(-1, styleCells(lang, r.pal.meta))
	}
	// One Chroma pass for the whole block, not one per line — the lexer
	// is stateful (a string literal spans lines), and per-line calls
	// would restart it on every row and mis-color every multi-line
	// construct in the file.
	grid := HighlightLang(lang, strings.Join(body, "\n"), r.th, r.pal.code)
	for n, line := range body {
		var styles []tcell.Style
		if n < len(grid) {
			styles = grid[n]
		}
		cells := codeCells(line, styles, r.pal.code)
		for k, chunk := range hardWrap(cells, r.width-1) {
			src := srcs[n]
			if k > 0 {
				src = -1
			}
			row := append([]mdCell{{r: ' ', st: r.pal.code}}, chunk...)
			r.emit(src, padCells(row, r.width, r.pal.code))
		}
	}
	r.blank()
}

// blockQuote strips one level of "> " off a run of quoted lines, renders
// what is left with a FRESH renderer, and prefixes every row that comes
// back with a bar. Recursing is what gets nested quotes, and lists and
// code inside quotes, for free rather than as special cases.
func (r *mdRenderer) blockQuote(i int) int {
	inner := make([]string, 0, 8)
	j := i
	for ; j < len(r.lines); j++ {
		ln := r.lines[j]
		if mdQuoteDepth(ln) == 0 {
			// A lazy continuation — an unmarked line directly under a
			// quoted one — belongs to the quote, per the spec and per
			// how people actually wrap quoted paragraphs. A blank ends
			// the quote.
			if strings.TrimSpace(ln) == "" || len(inner) == 0 {
				break
			}
			inner = append(inner, ln)
			continue
		}
		inner = append(inner, mdStripQuote(ln))
	}
	bar := []mdCell{{r: '▎', st: r.pal.bar}, {r: ' ', st: r.pal.base}}
	if r.depth >= mdMaxQuoteDepth {
		// Out of bars: render the remaining depth flat rather than
		// recursing forever on a pathological document.
		for k, ln := range inner {
			r.emitWrapped(i+k, bar, bar, parseInline(ln, r.pal.quote, r.pal, 0))
		}
		return j
	}
	sub := &mdRenderer{
		lines:   inner,
		width:   r.width - len(bar),
		pal:     r.pal,
		th:      r.th,
		srcBase: r.srcBase + i,
		depth:   r.depth + 1,
	}
	if sub.width < 8 {
		sub.width = 8
	}
	sub.run()
	for _, row := range sub.out {
		cells := append([]mdCell{}, bar...)
		for k, ru := range row.Runes {
			st := r.pal.base
			if k < len(row.Styles) {
				st = row.Styles[k]
			}
			cells = append(cells, mdCell{r: ru, st: st})
		}
		// The sub-renderer already offset Src into buffer numbering, so
		// this row is emitted with srcBase temporarily neutralised.
		src := row.Src
		saved := r.srcBase
		r.srcBase = 0
		r.emit(src, cells)
		r.srcBase = saved
	}
	return j
}

// listItem renders one item and every line that continues it. Nesting
// comes from the source indent rather than from a recursive parse: the
// marker's column IS the level, which is how the format is written and
// how a reader reads it.
func (r *mdRenderer) listItem(i int) int {
	indent, marker, rest, _ := mdListItem(r.lines[i])
	level := indent / 2
	if level > 6 {
		level = 6
	}
	// Continuation lines: anything non-blank that is more indented than
	// the marker and is not itself a new item.
	j := i + 1
	text := []string{rest}
	for ; j < len(r.lines); j++ {
		ln := r.lines[j]
		if strings.TrimSpace(ln) == "" {
			break
		}
		if _, _, _, ok := mdListItem(ln); ok {
			break
		}
		if mdIndentOf(ln) <= indent {
			break
		}
		text = append(text, strings.TrimSpace(ln))
	}

	pad := strings.Repeat("  ", level)
	var head []mdCell
	head = append(head, styleCells(pad, r.pal.base)...)
	if marker == "" {
		head = append(head, styleCells(mdBulletRunes[level%len(mdBulletRunes)]+" ", r.pal.bullet)...)
	} else {
		head = append(head, styleCells(marker+" ", r.pal.bullet)...)
	}
	body := strings.Join(text, " ")

	// A task box replaces the "[ ]" the source spells out. Three cells
	// either way, so a mixed list stays aligned.
	if box, remainder, ok := mdTaskBox(body); ok {
		st := r.pal.base
		mark := " "
		if box {
			st = r.pal.check
			mark = "✓"
		}
		head = append(head, mdCell{r: '[', st: r.pal.meta}, mdCell{r: []rune(mark)[0], st: st},
			mdCell{r: ']', st: r.pal.meta}, mdCell{r: ' ', st: r.pal.base})
		body = remainder
	}
	cont := repeatCells(' ', len(head), r.pal.base)
	r.emitWrapped(i, head, cont, parseInline(body, r.pal.base, r.pal, 0))
	return j
}

// paragraph consumes a run of prose, joining its lines the way markdown
// does (a single newline is a space) and word-wrapping the result.
//
// It also owns SETEXT headings, because they can only be recognised from
// inside a paragraph: "Title" followed by "===" is a heading, and the
// same "===" after a blank line is nothing at all.
func (r *mdRenderer) paragraph(i int) int {
	var parts []string
	j := i
	for ; j < len(r.lines); j++ {
		ln := r.lines[j]
		if strings.TrimSpace(ln) == "" {
			break
		}
		if lvl, ok := mdSetextRule(ln); ok && len(parts) > 0 {
			r.heading(i, lvl, strings.Join(parts, " "))
			return j + 1
		}
		// A block that can interrupt a paragraph ends it. A thematic
		// break is NOT in this list when it could be a setext rule —
		// the branch above already claimed that case.
		if j > i {
			if _, _, _, ok := mdFenceOpen(ln); ok {
				break
			}
			if _, _, ok := mdATXHeading(ln); ok {
				break
			}
			if mdQuoteDepth(ln) > 0 {
				break
			}
			if _, _, _, ok := mdListItem(ln); ok {
				break
			}
			if mdThematicBreak(ln) {
				break
			}
		}
		parts = append(parts, strings.TrimSpace(ln))
	}
	if len(parts) == 0 {
		return j + 1
	}
	r.emitWrapped(i, nil, nil, parseInline(strings.Join(parts, " "), r.pal.base, r.pal, 0))
	return j
}

// ---------------------------------------------------------------------
// Tables
// ---------------------------------------------------------------------

// tableAt reports whether a GFM pipe table starts at line i. It needs
// TWO lines to say yes — a header and a delimiter row — because a single
// line with a pipe in it is overwhelmingly likely to be prose or a
// shell command.
func (r *mdRenderer) tableAt(i int) (cols int, aligns []int, ok bool) {
	if i+1 >= len(r.lines) || !strings.Contains(r.lines[i], "|") {
		return 0, nil, false
	}
	aligns, ok = mdDelimiterRow(r.lines[i+1])
	if !ok {
		return 0, nil, false
	}
	head := mdSplitRow(r.lines[i])
	if len(head) == 0 || len(aligns) != len(head) {
		return 0, nil, false
	}
	return len(head), aligns, true
}

// table renders the whole block: header, a rule, then the body rows,
// laid out in columns sized to their content and shrunk proportionally
// when the window cannot hold them.
func (r *mdRenderer) table(i, cols int, aligns []int) int {
	type row struct {
		cells []string
		src   int
	}
	rows := []row{{cells: mdSplitRow(r.lines[i]), src: i}}
	j := i + 2
	for ; j < len(r.lines); j++ {
		if !strings.Contains(r.lines[j], "|") || strings.TrimSpace(r.lines[j]) == "" {
			break
		}
		rows = append(rows, row{cells: mdSplitRow(r.lines[j]), src: j})
	}

	// Width per column is the widest RENDERED cell — markup stripped,
	// since "**yes**" occupies three columns, not seven.
	widths := make([]int, cols)
	for _, rw := range rows {
		for c := 0; c < cols && c < len(rw.cells); c++ {
			if n := mdVisibleLen(rw.cells[c], r.pal); n > widths[c] {
				widths[c] = n
			}
		}
	}
	// " x │ " per column: one pad each side plus a separator between.
	frame := 3*cols + 1
	total := frame
	for _, w := range widths {
		total += w
	}
	if total > r.width {
		shrinkColumns(widths, r.width-frame)
	}

	r.blank()
	for n, rw := range rows {
		st := r.pal.base
		if n == 0 {
			st = r.pal.tblHead
		}
		var cells []mdCell
		for c := 0; c < cols; c++ {
			cells = append(cells, mdCell{r: '│', st: r.pal.tblRule}, mdCell{r: ' ', st: r.pal.base})
			var txt string
			if c < len(rw.cells) {
				txt = rw.cells[c]
			}
			spans := parseInline(txt, st, r.pal, 0)
			cells = append(cells, alignCells(spansToCells(spans), widths[c], aligns[c], r.pal.base)...)
			cells = append(cells, mdCell{r: ' ', st: r.pal.base})
		}
		cells = append(cells, mdCell{r: '│', st: r.pal.tblRule})
		r.emit(rw.src, cells)
		if n == 0 {
			// The rule after the header uses the DELIMITER line as its
			// source, so clicking it lands on the row that defines the
			// table's alignment — the line a user editing the table
			// actually wants.
			var rule []mdCell
			for c := 0; c < cols; c++ {
				// The corner glyph names the junction it actually is —
				// a rule that used ┼ at both ends would draw a table
				// with four stubs poking out of its sides.
				j := '┼'
				if c == 0 {
					j = '├'
				}
				rule = append(rule, mdCell{r: j, st: r.pal.tblRule})
				rule = append(rule, repeatCells('─', widths[c]+2, r.pal.tblRule)...)
			}
			rule = append(rule, mdCell{r: '┤', st: r.pal.tblRule})
			r.emit(i+1, rule)
		}
	}
	r.blank()
	return j
}

// shrinkColumns reduces the widest columns one cell at a time until the
// set fits in avail. Taking from the widest is what keeps a table of one
// long prose column and three short ones from crushing the short ones
// into nothing.
func shrinkColumns(widths []int, avail int) {
	if avail < len(widths) {
		avail = len(widths)
	}
	total := 0
	for _, w := range widths {
		total += w
	}
	for total > avail {
		worst, wi := 0, -1
		for i, w := range widths {
			if w > worst {
				worst, wi = w, i
			}
		}
		if wi < 0 || worst <= 1 {
			return
		}
		widths[wi]--
		total--
	}
}

// ---------------------------------------------------------------------
// Cell helpers
// ---------------------------------------------------------------------

// spansToCells flattens styled spans into per-rune cells, the form the
// wrapper works in — a single word can carry several styles, so wrapping
// at span granularity would lose them at every break.
func spansToCells(spans []mdSpan) []mdCell {
	var out []mdCell
	for _, s := range spans {
		for _, r := range s.text {
			out = append(out, mdCell{r: r, st: s.style})
		}
	}
	return out
}

// styleCells renders a plain string in one style.
func styleCells(s string, st tcell.Style) []mdCell {
	out := make([]mdCell, 0, len(s))
	for _, r := range s {
		out = append(out, mdCell{r: r, st: st})
	}
	return out
}

// repeatCells builds n copies of r in one style.
func repeatCells(r rune, n int, st tcell.Style) []mdCell {
	if n < 0 {
		n = 0
	}
	out := make([]mdCell, n)
	for i := range out {
		out[i] = mdCell{r: r, st: st}
	}
	return out
}

// padCells extends cells to width with spaces in st. Used only by blocks
// that paint their own background.
func padCells(cells []mdCell, width int, st tcell.Style) []mdCell {
	for len(cells) < width {
		cells = append(cells, mdCell{r: ' ', st: st})
	}
	return cells
}

// codeCells pairs a code line's runes with the styles Chroma produced
// for it, falling back to the block's base where the grid is short (a
// trailing token the lexer never emitted a style for).
//
// Tabs are EXPANDED here rather than passed through. The source view
// draws a tab by advancing to the next stop and painting spaces
// (Tab.Render, via RuneVisualWidth); this row has no such pass — every
// cell it emits is drawn literally — so an unexpanded tab would paint
// as one blank cell and collapse a Go file's indentation to nothing.
// Same TabStop, so a fence and the file it was copied from line up.
func codeCells(line string, styles []tcell.Style, base tcell.Style) []mdCell {
	rs := []rune(line)
	out := make([]mdCell, 0, len(rs))
	for i, r := range rs {
		st := base
		if i < len(styles) {
			st = styles[i]
		}
		if r == '\t' {
			out = append(out, repeatCells(' ', RuneVisualWidth(r, len(out)), st)...)
			continue
		}
		out = append(out, mdCell{r: r, st: st})
	}
	return out
}

// alignCells pads cells to width per a GFM alignment code (0 left,
// 1 center, 2 right), truncating with an ellipsis when the column was
// shrunk below the content.
func alignCells(cells []mdCell, width, align int, pad tcell.Style) []mdCell {
	if len(cells) > width {
		if width <= 1 {
			return repeatCells('…', width, pad)
		}
		cut := append([]mdCell{}, cells[:width-1]...)
		return append(cut, mdCell{r: '…', st: pad})
	}
	gap := width - len(cells)
	switch align {
	case 1:
		left := gap / 2
		out := repeatCells(' ', left, pad)
		out = append(out, cells...)
		return append(out, repeatCells(' ', gap-left, pad)...)
	case 2:
		return append(repeatCells(' ', gap, pad), cells...)
	default:
		return append(append([]mdCell{}, cells...), repeatCells(' ', gap, pad)...)
	}
}

// wrapCells word-wraps cells to width, breaking on spaces and hard-
// breaking any single word longer than the line (a URL, a long
// identifier) rather than letting it run off the pane.
func wrapCells(cells []mdCell, width int) [][]mdCell {
	if width < 1 {
		width = 1
	}
	var out [][]mdCell
	var line []mdCell
	var word []mdCell

	flushWord := func() {
		if len(word) == 0 {
			return
		}
		// The word does not fit after what is already on the line: break
		// the line first. A word longer than a whole line is hard-broken
		// below instead.
		if len(line)+len(word) > width && len(line) > 0 {
			out = append(out, line)
			line = nil
		}
		for len(word) > width {
			out = append(out, append(line, word[:width-len(line)]...))
			word = word[width-len(line):]
			line = nil
		}
		line = append(line, word...)
		word = nil
	}

	for _, c := range cells {
		if c.r == ' ' || c.r == '\t' {
			flushWord()
			// A space at a wrap boundary is dropped rather than carried
			// to the next row, where it would look like a stray indent.
			if len(line) > 0 && len(line) < width {
				line = append(line, mdCell{r: ' ', st: c.st})
			}
			continue
		}
		word = append(word, c)
	}
	flushWord()
	if len(line) > 0 {
		out = append(out, line)
	}
	// Trailing spaces are an artifact of the loop above, never content.
	for i := range out {
		for len(out[i]) > 0 && out[i][len(out[i])-1].r == ' ' {
			out[i] = out[i][:len(out[i])-1]
		}
	}
	return out
}

// hardWrap splits cells into fixed-width chunks without looking for word
// boundaries — the code path, where a column means something.
func hardWrap(cells []mdCell, width int) [][]mdCell {
	if width < 1 {
		width = 1
	}
	if len(cells) == 0 {
		return [][]mdCell{nil}
	}
	var out [][]mdCell
	for len(cells) > width {
		out = append(out, cells[:width])
		cells = cells[width:]
	}
	return append(out, cells)
}

// mdVisibleLen is how many columns a cell's text will occupy once its
// markup is gone — what the table layout has to measure.
func mdVisibleLen(s string, p mdPalette) int {
	n := 0
	for _, sp := range parseInline(s, p.base, p, 0) {
		n += len([]rune(sp.text))
	}
	return n
}

// ---------------------------------------------------------------------
// Line classifiers
// ---------------------------------------------------------------------

// mdIndentOf counts a line's leading indent in columns, a tab being four.
func mdIndentOf(s string) int {
	n := 0
	for _, r := range s {
		switch r {
		case ' ':
			n++
		case '\t':
			n += 4
		default:
			return n
		}
	}
	return n
}

// mdStripIndent removes up to n columns of leading whitespace.
func mdStripIndent(s string, n int) string {
	for n > 0 && s != "" {
		switch s[0] {
		case ' ':
			s = s[1:]
			n--
		case '\t':
			s = s[1:]
			n -= 4
		default:
			return s
		}
	}
	return s
}

// mdIndentedCode reports whether a line is a four-space code line.
func mdIndentedCode(s string) bool {
	return mdIndentOf(s) >= 4 && strings.TrimSpace(s) != ""
}

// mdFenceOpen recognises an opening code fence and its info string. Only
// the first word of the info string is the language ("```go run" is Go);
// the rest is a directive for some other tool.
func mdFenceOpen(s string) (fence string, indent int, lang string, ok bool) {
	indent = mdIndentOf(s)
	if indent > 3 {
		return "", 0, "", false
	}
	t := strings.TrimLeft(s, " \t")
	for _, ch := range []string{"```", "~~~"} {
		if !strings.HasPrefix(t, ch) {
			continue
		}
		n := 0
		for n < len(t) && t[n] == ch[0] {
			n++
		}
		info := strings.TrimSpace(t[n:])
		// A backtick inside a backtick fence's info string is illegal —
		// that line is inline code in a paragraph, not a fence.
		if ch == "```" && strings.Contains(info, "`") {
			return "", 0, "", false
		}
		if f := strings.Fields(info); len(f) > 0 {
			lang = strings.ToLower(strings.TrimPrefix(f[0], "."))
		}
		return t[:n], indent, lang, true
	}
	return "", 0, "", false
}

// mdFenceCloses reports whether a line closes the given fence: the same
// character, at least as long, and nothing else on the line.
func mdFenceCloses(s, fence string) bool {
	t := strings.TrimSpace(s)
	if !strings.HasPrefix(t, fence) {
		return false
	}
	return strings.Trim(t, string(fence[0])) == ""
}

// mdATXHeading recognises "### Title", returning the level and the text
// with any closing run of hashes removed.
func mdATXHeading(s string) (level int, text string, ok bool) {
	if mdIndentOf(s) > 3 {
		return 0, "", false
	}
	t := strings.TrimLeft(s, " \t")
	n := 0
	for n < len(t) && t[n] == '#' {
		n++
	}
	if n == 0 || n > 6 {
		return 0, "", false
	}
	rest := t[n:]
	// "#hashtag" is not a heading — the spec requires a space, and
	// without that rule every tag in a document becomes an H1.
	if rest != "" && rest[0] != ' ' && rest[0] != '\t' {
		return 0, "", false
	}
	rest = strings.TrimSpace(rest)
	rest = strings.TrimRight(rest, "#")
	return n, strings.TrimSpace(rest), true
}

// mdSetextRule recognises the "===" / "---" underline that turns the
// paragraph above it into a heading.
func mdSetextRule(s string) (level int, ok bool) {
	t := strings.TrimSpace(s)
	if t == "" || mdIndentOf(s) > 3 {
		return 0, false
	}
	if strings.Trim(t, "=") == "" {
		return 1, true
	}
	if strings.Trim(t, "-") == "" {
		return 2, true
	}
	return 0, false
}

// mdThematicBreak recognises "---" / "***" / "___" with optional spaces.
func mdThematicBreak(s string) bool {
	if mdIndentOf(s) > 3 {
		return false
	}
	t := strings.ReplaceAll(strings.TrimSpace(s), " ", "")
	if len(t) < 3 {
		return false
	}
	for _, ch := range "-*_" {
		if strings.Trim(t, string(ch)) == "" {
			return true
		}
	}
	return false
}

// mdQuoteDepth counts the leading ">" markers on a line.
func mdQuoteDepth(s string) int {
	n := 0
	i := 0
	for i < len(s) {
		switch s[i] {
		case ' ', '\t':
			i++
		case '>':
			n++
			i++
			if i < len(s) && s[i] == ' ' {
				i++
			}
		default:
			return n
		}
	}
	return n
}

// mdStripQuote removes one leading ">" marker and the space after it.
func mdStripQuote(s string) string {
	t := strings.TrimLeft(s, " \t")
	if !strings.HasPrefix(t, ">") {
		return s
	}
	t = t[1:]
	return strings.TrimPrefix(t, " ")
}

// mdListItem recognises a list marker, returning the indent, the ordered
// marker ("" for a bullet), and the text after it.
func mdListItem(s string) (indent int, marker, rest string, ok bool) {
	indent = mdIndentOf(s)
	t := strings.TrimLeft(s, " \t")
	if t == "" {
		return 0, "", "", false
	}
	if c := t[0]; c == '-' || c == '*' || c == '+' {
		if len(t) > 1 && (t[1] == ' ' || t[1] == '\t') {
			return indent, "", strings.TrimSpace(t[1:]), true
		}
		return 0, "", "", false
	}
	n := 0
	for n < len(t) && t[n] >= '0' && t[n] <= '9' {
		n++
	}
	// Nine digits is already an absurd list; the bound is there so a
	// line of digits can never be mistaken for a marker.
	if n == 0 || n > 9 || n >= len(t) {
		return 0, "", "", false
	}
	if t[n] != '.' && t[n] != ')' {
		return 0, "", "", false
	}
	if n+1 >= len(t) || (t[n+1] != ' ' && t[n+1] != '\t') {
		return 0, "", "", false
	}
	num, err := strconv.Atoi(t[:n])
	if err != nil {
		return 0, "", "", false
	}
	return indent, strconv.Itoa(num) + string(t[n]), strings.TrimSpace(t[n+1:]), true
}

// mdTaskBox recognises a "[ ]" / "[x]" prefix on a list item's text.
func mdTaskBox(s string) (checked bool, rest string, ok bool) {
	if len(s) < 3 || s[0] != '[' || s[2] != ']' {
		return false, s, false
	}
	switch s[1] {
	case ' ':
		return false, strings.TrimSpace(s[3:]), true
	case 'x', 'X':
		return true, strings.TrimSpace(s[3:]), true
	}
	return false, s, false
}

// mdSplitRow splits a pipe-table row into cells, honouring backslash-
// escaped pipes and tolerating the optional leading/trailing pipes.
func mdSplitRow(s string) []string {
	t := strings.TrimSpace(s)
	t = strings.TrimPrefix(t, "|")
	t = strings.TrimSuffix(t, "|")
	var cells []string
	var cur strings.Builder
	rs := []rune(t)
	for i := 0; i < len(rs); i++ {
		if rs[i] == '\\' && i+1 < len(rs) && rs[i+1] == '|' {
			cur.WriteRune('|')
			i++
			continue
		}
		if rs[i] == '|' {
			cells = append(cells, strings.TrimSpace(cur.String()))
			cur.Reset()
			continue
		}
		cur.WriteRune(rs[i])
	}
	cells = append(cells, strings.TrimSpace(cur.String()))
	return cells
}

// mdDelimiterRow recognises a table's "| --- | :-: |" line and returns
// one alignment code per column (0 left, 1 center, 2 right).
func mdDelimiterRow(s string) ([]int, bool) {
	if !strings.Contains(s, "-") || !strings.Contains(s, "|") {
		return nil, false
	}
	cells := mdSplitRow(s)
	aligns := make([]int, 0, len(cells))
	for _, c := range cells {
		c = strings.TrimSpace(c)
		left := strings.HasPrefix(c, ":")
		right := strings.HasSuffix(c, ":")
		body := strings.Trim(c, ":")
		if body == "" || strings.Trim(body, "-") != "" {
			return nil, false
		}
		switch {
		case left && right:
			aligns = append(aligns, 1)
		case right:
			aligns = append(aligns, 2)
		default:
			aligns = append(aligns, 0)
		}
	}
	return aligns, true
}
