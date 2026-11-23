// =============================================================================
// File: internal/app/jsonkeys.go
// Author: Rohan Allison <rohanthewiz@gmail.com>
// Created: 2026-11-23
// Copyright: 2026 Rohan Allison. All rights reserved.
// =============================================================================

// jsonkeys.go is the REFORMAT JSON dialog (≡ File "Reformat JSON (pick
// keys)…"): every top-level key the JSON carries in a tick list, and the
// text rewritten with only the ticked keys in a live preview beside it.
//
//	┌ Reformat JSON · app.log ──────────────────────────────────────── esc ┐
//	│ JSON lines · 2,431 records · 3 of 14 keys kept       lines 1–38 of 2431 │
//	│ [ ] file         2431 │{"level":"info","msg":"flushed 500 rows","tim… │
//	│ [x] level        2431 │  e":"2026-01-02T03:04:05Z"}                    │
//	│ [x] msg          2431 │{"level":"warn","msg":"retrying","time":"20…    │
//	│ [ ] start           1 │                                                │
//	│ [x] time         2431 │                                                │
//	│ [x] pretty-print  [ ] hide records left empty      space ticks · type…  │
//	│ [ Select none ] [ Invert ]        [ Cancel ]  [ Copy ]  [ Apply to file ] │
//	└────────────────────────────────────────────────────────────────────────┘
//
// The engine — the four shapes it reads (one object, an array, JSON
// lines, a stream), the byte-preserving projection — lives in
// internal/format/jsonkeys.go. This file is the dialog around it, and
// its decisions are:
//
//   - A DIALOG WITH A PREVIEW, NOT A VIEW MODE. Reading a log through a
//     key filter is the common case, and the preview IS that reading —
//     a scrollable pane the size of the window, re-projected on every
//     tick, so "dynamic" costs nothing to the buffer. Changing the
//     editor's own rendering instead (a view flag like the markdown
//     preview) would have threaded a second text through every
//     hit-test, caret and decoration path for a filter the user only
//     wants while they look.
//   - TICKS MEAN KEEP. The key list is the union of every record's keys,
//     so "keep the ticked" and "drop the unticked" are the same set — one
//     list does both jobs. It opens with every key ticked (the text as it
//     is), so EXCLUDING is unticking the noise, and INCLUDING is Select
//     none and ticking the few. Select all / none and Invert act on the
//     VISIBLE rows only (the tree marks' bulk rule), so a filter plus
//     Select none drops exactly what it shows.
//   - TWO WAYS OUT: Apply and Copy. Apply rewrites what was read — the
//     selection, else the whole buffer — as ONE undo step through
//     ApplyMultiEdit, unsaved like any edit (auto-save then treats it as
//     one). A selection is re-selected afterwards, so reopening the
//     dialog reformats the same text again. Copy puts the result on the
//     clipboard and leaves the buffer and the dialog alone — the door for
//     "I only wanted to read / paste three fields". No "open in new tab":
//     an untitled tab cannot be saved yet, so it would be Copy with extra
//     steps.
//   - APPLY IS GENERATION-CHECKED. The dialog read the text at open;
//     the tick-time reconcile may reload the tab underneath it (a log
//     being appended to is exactly that file). Apply compares EditRev
//     and refuses a moved one, rather than writing a reformat of text
//     the buffer no longer holds over text the user never saw.
//   - THE PREVIEW WRAPS. A log record is one long line, and a filter is
//     chosen by reading the values — so rows wrap at the pane edge with
//     a two-cell hanging indent (a record's continuation is visibly not
//     a new record), rather than clipping at the column the msg starts
//     in. Wrapping is computed once per (output, width) over BYTE
//     offsets, so drawing a row of a 30 MB minified line never walks the
//     line from its start.
//   - SYNTAX COLOUR FROM THE EDITOR'S OWN LEXER, per visible line,
//     cached in a bounded window, and only for lines short enough to lex
//     in a frame and that look like JSON — a passenger (a stack-trace
//     line in a log) is plain text, not a wash of error tokens.
//   - TYPE TO FILTER the key list, the cherry-pick dialog's model:
//     printable letters narrow it (Space stays the tick), Backspace
//     edits, Esc clears the filter before a second Esc closes.

package app

import (
	"path/filepath"
	"strings"
	"time"
	"unicode"

	"github.com/gdamore/tcell/v2"

	"github.com/rohanthewiz/ced/internal/clipboard"
	"github.com/rohanthewiz/ced/internal/editor"
	"github.com/rohanthewiz/ced/internal/format"
)

const (
	// Dialog size bounds. Wide and tall: the preview is the point, and a
	// log record wants every column it can get.
	jsonKeysMinW = 60
	jsonKeysMaxW = 180
	jsonKeysMinH = 14

	// jsonKeysListMinW floors the key column (box, a short name, a
	// count); its ceiling is two fifths of the dialog, so the preview
	// always keeps the larger share.
	jsonKeysListMinW = 24

	// jsonWrapIndent is the hanging indent of a wrapped preview row.
	jsonWrapIndent = 2

	// jsonKeysHighlightMax is the longest output line the preview lexes
	// for colour (bytes). A log line is a few hundred; a minified
	// document on one line can be megabytes, and Chroma over that would
	// cost a visible pause on the frame that first shows it.
	jsonKeysHighlightMax = 8 << 10

	// jsonKeysStyleCacheMax bounds the per-line colour cache. Scrolling
	// a 100k-record log end to end would otherwise keep every line's
	// style slice alive; past this many the cache starts over, which
	// only costs re-lexing what is on screen.
	jsonKeysStyleCacheMax = 512
)

// jsonKeysCopyFn is the clipboard door (a package var so tests never
// write an OSC 52 sequence to the developer's terminal).
var jsonKeysCopyFn = clipboard.CopyToSystem

// jsonPreviewRow is one display row of the preview: a span of one output
// line, as BYTE offsets [b0, b1) plus the rune index r0 of its first
// rune (the colour cache is indexed by rune). cont marks a wrapped
// continuation, drawn with the hanging indent.
type jsonPreviewRow struct {
	line   int
	b0, b1 int
	r0     int
	cont   bool
}

// jsonKeysModal is the dialog's state.
type jsonKeysModal struct {
	// What was read, and from where: the tab, its EditRev at open (the
	// Apply guard), the range (selection or whole buffer) and its text.
	tab       *editor.Tab
	rev       int
	from, to  editor.Position
	selection bool
	scope     string // the title's "app.log" / "selection (12 lines)"
	src       string

	recs       *format.JSONRecords
	longestKey int // cells of the widest displayed key name, for the column width

	keep      map[string]bool
	pretty    bool
	skipEmpty bool

	// The key list: cursor indexes recs.Keys (-1 when a filter leaves
	// nothing visible); scroll is a VIEW position; view lists the key
	// indices the filter keeps.
	cursor    int
	scroll    int
	filter    string
	view      []int
	viewBuilt bool

	// The projection, rebuilt lazily after any change (built false).
	built bool
	out   []byte
	lines []string // out split for display, a trailing empty line dropped
	kept  int

	// The preview: scroll in display rows, the wrap cache (rowsW is the
	// width it was built for; nil rows = stale) and the colour cache.
	pscroll int
	rows    []jsonPreviewRow
	rowsW   int
	styles  map[int][]tcell.Style

	// Edge-triggered presses and double-click on a key row (the
	// cherry-pick dialog's model).
	lastBtn      tcell.ButtonMask
	lastClickRow int
	lastClickAt  time.Time
}

// -----------------------------------------------------------------------------
// Entry points
// -----------------------------------------------------------------------------

// menuReformatJSON is the ≡ File row.
func (a *App) menuReformatJSON() {
	a.closeMenu()
	a.openJSONKeys()
}

// openJSONKeys reads the active tab's selection (else its whole buffer)
// as JSON records and opens the dialog on them. Text with nothing to
// pick from is refused with the parser's reason — the row stays
// clickable on any text tab, so the flash is how a user learns WHY a
// file isn't one it can reformat (and where a broken document breaks).
func (a *App) openJSONKeys() {
	t := a.activeTabPtr()
	if t == nil || t.IsImage() {
		a.flash("Open a JSON file, or select some JSON, to reformat it")
		return
	}
	m := &jsonKeysModal{tab: t, rev: t.EditRev, lastClickRow: -1}
	// The PRIMARY selection only: secondary carets each hold a piece,
	// and a reformat of several disjoint pieces as one text would join
	// them. ApplyMultiEdit drops the carets anyway.
	if t.HasSelection() {
		m.from, m.to = editor.PosOrdered(t.Buffer.Clamp(t.Anchor), t.Buffer.Clamp(t.Cursor))
		m.selection = true
	} else {
		m.to = t.Buffer.EndPos()
	}
	m.src = t.Buffer.Substring(m.from, m.to)
	recs, err := format.ParseJSONRecords([]byte(m.src))
	if err != nil {
		what := "this file"
		if m.selection {
			what = "the selection"
		}
		a.flash("Can't reformat " + what + ": " + err.Error())
		return
	}
	m.recs = recs
	m.scope = jsonKeysScope(t, m)
	m.pretty = recs.Pretty
	m.keep = make(map[string]bool, len(recs.Keys))
	for _, k := range recs.Keys {
		m.keep[k.Name] = true
		m.longestKey = max(m.longestKey, runeLen(displayJSONKey(k.Name)))
	}
	if len(recs.Keys) == 0 {
		m.cursor = -1
	}
	a.openModal(m)
}

// jsonKeysScope names what the dialog read, for its title.
func jsonKeysScope(t *editor.Tab, m *jsonKeysModal) string {
	if m.selection {
		return "selection (" + plural(m.to.Line-m.from.Line+1, "line", "lines") + ")"
	}
	if t.Path == "" {
		return "untitled"
	}
	return filepath.Base(t.Path)
}

// displayJSONKey makes a key name safe to paint: a key may legally hold
// a newline or a control byte (decoded from an escape), which would
// break the row; those become the editor's one-cell stand-in. An empty
// key is shown as its quotes, since a blank row reads as no key at all.
func displayJSONKey(name string) string {
	if name == "" {
		return `""`
	}
	return strings.Map(displayJSONRune, name)
}

// displayJSONRune maps a rune to what one preview / list cell shows: a
// tab is one blank (rows are measured one cell per rune), any other
// control rune the stand-in.
func displayJSONRune(r rune) rune {
	switch {
	case r == '\t':
		return ' '
	case r < 0x20 || r == 0x7f || (r >= 0x80 && r < 0xa0):
		return '▯'
	}
	return r
}

// -----------------------------------------------------------------------------
// The projection
// -----------------------------------------------------------------------------

// invalidate marks the projection stale after a tick or an option.
func (m *jsonKeysModal) invalidate() {
	m.built = false
}

// output brings the projection up to date and returns it.
func (m *jsonKeysModal) output() []byte {
	if m.built {
		return m.out
	}
	m.out, m.kept = m.recs.Project(m.keep, m.pretty, m.skipEmpty)
	m.lines = strings.Split(string(m.out), "\n")
	// A final newline would show as a blank last row — noise in a
	// preview, though Apply and Copy keep it.
	if n := len(m.lines); n > 1 && m.lines[n-1] == "" {
		m.lines = m.lines[:n-1]
	}
	m.rows, m.styles = nil, nil
	m.built = true
	return m.out
}

// keptKeys counts the ticked keys.
func (m *jsonKeysModal) keptKeys() int {
	n := 0
	for _, k := range m.recs.Keys {
		if m.keep[k.Name] {
			n++
		}
	}
	return n
}

// toggle flips key k's tick.
func (m *jsonKeysModal) toggle(k int) {
	if k < 0 || k >= len(m.recs.Keys) {
		return
	}
	name := m.recs.Keys[k].Name
	m.keep[name] = !m.keep[name]
	m.invalidate()
}

// allVisibleKept reports whether every visible key is ticked (and there
// is one) — the state in which Select all becomes Select none.
func (m *jsonKeysModal) allVisibleKept() bool {
	vis := m.vis()
	for _, k := range vis {
		if !m.keep[m.recs.Keys[k].Name] {
			return false
		}
	}
	return len(vis) > 0
}

// toggleAll ticks every visible key, or unticks them when they already
// all are — one button labelled by what it will do.
func (m *jsonKeysModal) toggleAll() {
	on := !m.allVisibleKept()
	for _, k := range m.vis() {
		m.keep[m.recs.Keys[k].Name] = on
	}
	m.invalidate()
}

// invert flips every visible key — "keep everything except these" in
// one press after ticking the few to drop.
func (m *jsonKeysModal) invert() {
	for _, k := range m.vis() {
		name := m.recs.Keys[k].Name
		m.keep[name] = !m.keep[name]
	}
	m.invalidate()
}

// togglePretty / toggleSkipEmpty flip the two options.
func (m *jsonKeysModal) togglePretty() {
	m.pretty = !m.pretty
	m.invalidate()
}

// toggleSkipEmpty flips "hide records left empty".
func (m *jsonKeysModal) toggleSkipEmpty() {
	m.skipEmpty = !m.skipEmpty
	m.invalidate()
}

// -----------------------------------------------------------------------------
// The key list's filter (cherrypick.go's view model)
// -----------------------------------------------------------------------------

// refilter rebuilds the view and keeps the highlight on a visible key:
// the current one if it survived, else the first visible one, else none.
func (m *jsonKeysModal) refilter() {
	m.viewBuilt = true
	m.view = m.view[:0]
	q := strings.ToLower(m.filter)
	for i, k := range m.recs.Keys {
		if q == "" || strings.Contains(strings.ToLower(k.Name), q) {
			m.view = append(m.view, i)
		}
	}
	if m.viewPos(m.cursor) >= 0 {
		return
	}
	m.cursor = -1
	if len(m.view) > 0 {
		m.cursor = m.view[0]
	}
}

// vis is the view, built on first use.
func (m *jsonKeysModal) vis() []int {
	if !m.viewBuilt {
		m.refilter()
	}
	return m.view
}

// viewPos is key idx's position in the view, -1 when hidden.
func (m *jsonKeysModal) viewPos(idx int) int {
	for p, i := range m.vis() {
		if i == idx {
			return p
		}
	}
	return -1
}

// setFilter replaces the filter and brings the highlight back on screen.
func (m *jsonKeysModal) setFilter(a *App, f string) {
	m.filter = f
	m.refilter()
	m.scroll = 0
	m.ensureCursorVisible(a)
}

// -----------------------------------------------------------------------------
// Geometry — ONE method feeds draw and hit-test (the btnRect rule).
// -----------------------------------------------------------------------------

// jsonKeysGeom is the dialog's layout for the current window.
type jsonKeysGeom struct {
	x, y, w, h int
	summaryY   int
	// The two panes share rows [top, top+rows). Key rows span
	// [listX, divX); divX is the divider column; the preview's
	// background runs from divX+1 to the frame, its text over
	// [prevX, prevEnd).
	top, rows       int
	listX, divX     int
	prevX, prevEnd  int
	optY, btnY      int
	pretty, empty   btnRect
	all, invert     btnRect
	cancel, copyBtn btnRect
	apply           btnRect
}

// rect sizes the dialog: nearly the whole window, within bounds.
func (m *jsonKeysModal) rect(a *App) (x, y, w, h int) {
	w = min(max(a.width-4, jsonKeysMinW), jsonKeysMaxW)
	h = max(a.height-2, jsonKeysMinH)
	return a.centeredRect(min(w, a.width), min(h, a.height))
}

// geom lays the dialog out. Rows: frame + title + divider (3), the
// summary, the two panes, the options row, the button row, the border.
func (m *jsonKeysModal) geom(a *App) jsonKeysGeom {
	x, y, w, h := m.rect(a)
	g := jsonKeysGeom{x: x, y: y, w: w, h: h, summaryY: y + 3, top: y + 4, optY: y + h - 3, btnY: y + h - 2}
	g.rows = max(g.optY-g.top, 1)
	g.listX = x + 1
	g.divX = g.listX + m.listWidth(w)
	g.prevX = g.divX + 2
	g.prevEnd = max(x+w-2, g.prevX+1)

	pl := runeLen(m.prettyLabel())
	g.pretty = btnRect{x: x + 2, y: g.optY, w: pl}
	g.empty = btnRect{x: x + 2 + pl + 3, y: g.optY, w: runeLen(m.emptyLabel())}

	// Select all / none changes width with its label, so Invert sits at
	// a fixed offset past the WIDER label and never slides.
	g.all = btnRect{x: x + 2, y: g.btnY, w: runeLen(m.allLabel())}
	g.invert = btnRect{x: x + 2 + runeLen("[ Select none ]") + 1, y: g.btnY, w: runeLen("[ Invert ]")}
	// The right-hand three are anchored by the RIGHT edge (gitpush.go's
	// rule), primary last.
	al := runeLen(m.applyLabel())
	g.apply = btnRect{x: x + w - 3 - al, y: g.btnY, w: al}
	g.copyBtn = btnRect{x: g.apply.x - 2 - 8, y: g.btnY, w: 8}
	g.cancel = btnRect{x: g.copyBtn.x - 2 - 10, y: g.btnY, w: 10}
	return g
}

// listWidth is the key column's width in cells: pad, box, the longest
// name, a gap and the count column — clamped so the preview keeps the
// larger share.
func (m *jsonKeysModal) listWidth(w int) int {
	want := 1 + 4 + m.longestKey + 2 + 1
	if m.recs.Records > 1 {
		want += len(itoa(m.recs.Records))
	}
	return min(max(want, jsonKeysListMinW), max(w*2/5, jsonKeysListMinW))
}

// ensureCursorVisible scrolls the key list just enough to show the
// highlighted key (both sides VIEW positions).
func (m *jsonKeysModal) ensureCursorVisible(a *App) {
	rows := m.geom(a).rows
	if p := m.viewPos(m.cursor); p >= 0 {
		if p < m.scroll {
			m.scroll = p
		}
		if p >= m.scroll+rows {
			m.scroll = p - rows + 1
		}
	}
	m.clampScroll(a)
}

// clampScroll keeps the key list's scroll in range.
func (m *jsonKeysModal) clampScroll(a *App) {
	m.scroll = min(max(m.scroll, 0), max(len(m.vis())-m.geom(a).rows, 0))
}

// moveCursor steps the highlight through the view, clamped.
func (m *jsonKeysModal) moveCursor(a *App, delta int) {
	vis := m.vis()
	if len(vis) == 0 {
		return
	}
	p := max(m.viewPos(m.cursor), 0)
	m.cursor = vis[min(max(p+delta, 0), len(vis)-1)]
	m.ensureCursorVisible(a)
}

// previewRows wraps the projection at width w (cells) and caches the
// rows for that width. The first row of a line takes w runes, each
// continuation w-jsonWrapIndent. One walk over every line, by rune
// start, recording byte offsets — see the file comment for why bytes.
// Ranging over a string decodes an invalid byte as one U+FFFD of one
// byte, so the offsets stay exact on text that isn't valid UTF-8 too.
func (m *jsonKeysModal) previewRows(w int) []jsonPreviewRow {
	m.output()
	if m.rows != nil && m.rowsW == w {
		return m.rows
	}
	w = max(w, 1)
	contW := max(w-jsonWrapIndent, 1)
	rows := make([]jsonPreviewRow, 0, len(m.lines))
	for i, l := range m.lines {
		row := jsonPreviewRow{line: i}
		limit, col, r := w, 0, 0
		for bi := range l {
			if col == limit {
				row.b1 = bi
				rows = append(rows, row)
				row = jsonPreviewRow{line: i, b0: bi, r0: r, cont: true}
				limit, col = contW, 0
			}
			col++
			r++
		}
		row.b1 = len(l)
		rows = append(rows, row)
	}
	m.rows, m.rowsW = rows, w
	return rows
}

// previewWidth is the preview's text width for a layout.
func (g jsonKeysGeom) previewWidth() int {
	return max(g.prevEnd-g.prevX, 1)
}

// clampPreview keeps the preview's scroll in range for n display rows.
func (m *jsonKeysModal) clampPreview(rows, n int) {
	m.pscroll = min(max(m.pscroll, 0), max(n-rows, 0))
}

// scrollPreview moves the preview by delta display rows.
func (m *jsonKeysModal) scrollPreview(a *App, delta int) {
	g := m.geom(a)
	m.pscroll += delta
	m.clampPreview(g.rows, len(m.previewRows(g.previewWidth())))
}

// -----------------------------------------------------------------------------
// Labels
// -----------------------------------------------------------------------------

// prettyLabel / emptyLabel are the option checkboxes' text.
func (m *jsonKeysModal) prettyLabel() string { return checkbox(m.pretty) + " pretty-print" }

// emptyLabel describes skipEmpty in the words of what it does.
func (m *jsonKeysModal) emptyLabel() string {
	return checkbox(m.skipEmpty) + " hide records left empty"
}

// allLabel names the bulk button by what it will do.
func (m *jsonKeysModal) allLabel() string {
	if m.allVisibleKept() {
		return "[ Select none ]"
	}
	return "[ Select all ]"
}

// applyLabel says what Apply rewrites.
func (m *jsonKeysModal) applyLabel() string {
	if m.selection {
		return "[ Apply to selection ]"
	}
	return "[ Apply to file ]"
}

// summaryText is the line under the title: the filter's state while one
// is typed, else the shape, the counts, and what the options are hiding.
func (m *jsonKeysModal) summaryText() string {
	if m.filter != "" {
		return "filter “" + m.filter + "”: " + itoa(len(m.vis())) + " of " +
			plural(len(m.recs.Keys), "key", "keys") + " · Esc clears"
	}
	r := m.recs
	s := r.Shape.String() + " · " + plural(r.Records, "record", "records") + " · " +
		itoa(m.keptKeys()) + " of " + plural(len(r.Keys), "key", "keys") + " kept"
	if r.Others > 0 {
		one, many := "other value", "other values"
		if r.Shape == format.JSONLines {
			one, many = "other line", "other lines"
		}
		s += " · " + plural(r.Others, one, many) + " kept as-is"
	}
	m.output()
	if hidden := r.Records - m.kept; hidden > 0 {
		s += " · " + plural(hidden, "emptied record", "emptied records") + " hidden"
	}
	return s
}

// -----------------------------------------------------------------------------
// Drawing
// -----------------------------------------------------------------------------

// draw paints the dialog.
func (m *jsonKeysModal) draw(a *App) {
	g := m.geom(a)
	c := a.chrome()
	th := a.theme
	c.drawFrame(a.screen, g.x, g.y, g.w, g.h, "Reformat JSON · "+m.scope)
	a.screen.HideCursor()

	// Summary, with the preview's position right-aligned when it fits.
	rows := m.previewRows(g.previewWidth())
	m.clampPreview(g.rows, len(rows))
	inner := g.w - 4
	pos := m.previewPosText(g, rows)
	sum := m.summaryText()
	if px := g.x + g.w - 2 - runeLen(pos); pos != "" && px > g.x+2+runeLen(sum)+2 {
		drawAt(a.screen, px, g.summaryY, pos, c.muted)
	}
	drawStatusText(a.screen, g.x+2, g.summaryY, inner, clipRunes(sum, inner), c.muted)

	// The key list.
	vis := m.vis()
	for i := 0; i < g.rows; i++ {
		p := m.scroll + i
		if p >= len(vis) {
			break
		}
		m.drawKeyRow(a, g, vis[p], g.top+i)
	}
	if len(m.recs.Keys) == 0 {
		drawAt(a.screen, g.listX+1, g.top, clipRunes("(no keys)", g.divX-g.listX-1), c.muted)
	}
	for y := g.top; y < g.top+g.rows; y++ {
		a.screen.SetContent(g.divX, y, '│', nil, c.border)
	}

	m.drawPreview(a, g, rows)

	// Options, with the keyboard's reminder at the right when it fits.
	drawAt(a.screen, g.pretty.x, g.pretty.y, m.prettyLabel(), c.body)
	drawAt(a.screen, g.empty.x, g.empty.y, m.emptyLabel(), c.body)
	hint := "space ticks · type filters · alt+↑↓ scrolls preview"
	if hx := g.x + g.w - 2 - runeLen(hint); hx > g.empty.x+g.empty.w+2 {
		drawAt(a.screen, hx, g.optY, hint, c.muted)
	}

	drawButton(a.screen, g.all.x, g.all.y, m.allLabel(), c.bg, th.Text, false)
	drawButton(a.screen, g.invert.x, g.invert.y, "[ Invert ]", c.bg, th.Text, false)
	drawButton(a.screen, g.cancel.x, g.cancel.y, "[ Cancel ]", c.bg, th.Text, false)
	drawButton(a.screen, g.copyBtn.x, g.copyBtn.y, "[ Copy ]", c.bg, th.Text, false)
	drawButton(a.screen, g.apply.x, g.apply.y, m.applyLabel(), c.bg, th.Accent, true)
}

// previewPosText is "lines a–b of n" for the preview — in OUTPUT lines,
// which is what a user compares with the file, not wrapped rows.
func (m *jsonKeysModal) previewPosText(g jsonKeysGeom, rows []jsonPreviewRow) string {
	if len(rows) == 0 || len(rows) <= g.rows {
		return ""
	}
	first := rows[m.pscroll].line + 1
	last := rows[min(m.pscroll+g.rows, len(rows))-1].line + 1
	return "lines " + itoa(first) + "–" + itoa(last) + " of " + itoa(len(m.lines))
}

// drawKeyRow paints one key: box, name (muted when dropped), and how
// many records carry it (only meaningful with more than one record).
func (m *jsonKeysModal) drawKeyRow(a *App, g jsonKeysGeom, k, y int) {
	th := a.theme
	bg := a.chrome().bg
	if k == m.cursor {
		bg = th.Selection
	}
	for x := g.listX; x < g.divX; x++ {
		a.screen.SetContent(x, y, ' ', nil, tcell.StyleDefault.Background(bg))
	}
	st := func(fg tcell.Color) tcell.Style { return tcell.StyleDefault.Background(bg).Foreground(fg) }
	key := m.recs.Keys[k]
	on := m.keep[key.Name]
	x := g.listX + 1
	drawAt(a.screen, x, y, checkbox(on), st(th.Accent))
	x += 4
	end := g.divX - 1
	if m.recs.Records > 1 {
		cnt := itoa(key.Count)
		if cx := end - len(cnt); cx > x+3 {
			drawAt(a.screen, cx, y, cnt, st(th.Muted))
			end = cx - 1
		}
	}
	fg := th.Text
	if !on {
		fg = th.Muted
	}
	if w := end - x; w > 0 {
		drawAt(a.screen, x, y, clipRunes(displayJSONKey(key.Name), w), st(fg))
	}
}

// drawPreview paints the projection's visible rows on the editor's own
// background — it reads as code, and the syntax palette was chosen
// against that background, not the dialog's.
func (m *jsonKeysModal) drawPreview(a *App, g jsonKeysGeom, rows []jsonPreviewRow) {
	base := editor.EditorBase(a.theme)
	fillRect(a.screen, g.divX+1, g.top, g.x+g.w-1-(g.divX+1), g.rows, base)
	for i := 0; i < g.rows; i++ {
		ri := m.pscroll + i
		if ri >= len(rows) {
			break
		}
		r := rows[ri]
		line := m.lines[r.line]
		styles := m.lineStyles(a, r.line)
		x := g.prevX
		if r.cont {
			x += jsonWrapIndent
		}
		ci := r.r0
		for _, ch := range line[r.b0:r.b1] {
			st := base
			if ci < len(styles) {
				st = styles[ci]
			}
			a.screen.SetContent(x, g.top+i, displayJSONRune(ch), nil, st)
			x++
			ci++
		}
	}
}

// lineStyles is output line i's colours (nil = plain), lexed on first
// sight and cached in a bounded window.
func (m *jsonKeysModal) lineStyles(a *App, i int) []tcell.Style {
	if st, ok := m.styles[i]; ok {
		return st
	}
	if m.styles == nil || len(m.styles) >= jsonKeysStyleCacheMax {
		m.styles = map[int][]tcell.Style{}
	}
	var st []tcell.Style
	l := m.lines[i]
	if len(l) <= jsonKeysHighlightMax && looksLikeJSONLine(l) {
		if grid := editor.HighlightLang("json", l, a.theme, editor.EditorBase(a.theme)); len(grid) > 0 {
			st = grid[0]
		}
	}
	m.styles[i] = st
	return st
}

// looksLikeJSONLine reports whether an output line is JSON worth
// colouring: it starts (past indentation) like a JSON line of either
// layout — a brace, a bracket, a key, a number. A log's passenger (a
// stack trace, "panic: …") does not, and would lex as error tokens.
func looksLikeJSONLine(l string) bool {
	t := strings.TrimLeft(l, " \t")
	if t == "" {
		return false
	}
	switch c := t[0]; {
	case strings.IndexByte(`{}[]"-`, c) >= 0:
		return true
	case c >= '0' && c <= '9':
		return true
	}
	return false
}

// -----------------------------------------------------------------------------
// Keyboard
// -----------------------------------------------------------------------------

// altChords is the dialog's Alt table (altchords.go): the keyboard
// twins of its buttons and options. Apply's twin is Enter and Cancel's
// is Esc, as in every dialog.
func (m *jsonKeysModal) altChords(*App) []altChord {
	return []altChord{
		{key: 'a', run: func(*App) { m.toggleAll() }},
		{key: 'i', run: func(*App) { m.invert() }},
		{key: 'p', run: func(*App) { m.togglePretty() }},
		{key: 'e', run: func(*App) { m.toggleSkipEmpty() }},
		{key: 'c', run: m.copyOut},
	}
}

// handleKey: arrows and paging walk the key list, Space ticks, Enter
// applies, Esc clears the filter then closes, printable runes filter,
// Alt/Shift + arrows and paging scroll the preview, and the Alt chords
// are the buttons' twins.
func (m *jsonKeysModal) handleKey(a *App, ev *tcell.EventKey) {
	// Every Alt rune is the dialog's, bound or not, so an unbound one is
	// swallowed rather than typed into the filter.
	if r, ok := altChordRune(ev); ok {
		fireAltChord(a, m.altChords(a), r)
		return
	}
	if ev.Modifiers()&(tcell.ModAlt|tcell.ModShift) != 0 && m.scrollPreviewKey(a, ev.Key()) {
		return
	}
	switch ev.Key() {
	case tcell.KeyEsc:
		if m.filter != "" {
			m.setFilter(a, "")
			return
		}
		a.closeModal()
	case tcell.KeyBackspace, tcell.KeyBackspace2:
		if r := []rune(m.filter); len(r) > 0 {
			m.setFilter(a, string(r[:len(r)-1]))
		}
	case tcell.KeyUp:
		m.moveCursor(a, -1)
	case tcell.KeyDown:
		m.moveCursor(a, 1)
	case tcell.KeyPgUp:
		m.moveCursor(a, -m.geom(a).rows)
	case tcell.KeyPgDn:
		m.moveCursor(a, m.geom(a).rows)
	case tcell.KeyHome:
		m.moveCursor(a, -len(m.vis()))
	case tcell.KeyEnd:
		m.moveCursor(a, len(m.vis()))
	case tcell.KeyEnter:
		m.apply(a)
	case tcell.KeyRune:
		switch r := ev.Rune(); {
		case r == ' ':
			m.toggle(m.cursor)
		case ev.Modifiers()&tcell.ModCtrl == 0 && unicode.IsPrint(r):
			m.setFilter(a, m.filter+string(r))
		}
	}
}

// scrollPreviewKey scrolls the preview for an Alt/Shift-modified motion
// key and reports whether it was one.
func (m *jsonKeysModal) scrollPreviewKey(a *App, k tcell.Key) bool {
	page := max(m.geom(a).rows-1, 1)
	switch k {
	case tcell.KeyUp:
		m.scrollPreview(a, -1)
	case tcell.KeyDown:
		m.scrollPreview(a, 1)
	case tcell.KeyPgUp:
		m.scrollPreview(a, -page)
	case tcell.KeyPgDn:
		m.scrollPreview(a, page)
	case tcell.KeyHome:
		m.pscroll = 0
	case tcell.KeyEnd:
		m.scrollPreview(a, len(m.previewRows(m.geom(a).previewWidth())))
	default:
		return false
	}
	return true
}

// -----------------------------------------------------------------------------
// Mouse
// -----------------------------------------------------------------------------

// handleMouse: the wheel scrolls whichever pane it is over; a PRESS
// (edge-triggered) on a button or option does what it says, on a key's
// box ticks it, on the rest of the row highlights it (a second press
// ticks — double-click). A press outside the dialog dismisses it.
func (m *jsonKeysModal) handleMouse(a *App, x, y int, btn tcell.ButtonMask) {
	pressed := btn&tcell.Button1 != 0 && m.lastBtn&tcell.Button1 == 0
	m.lastBtn = btn
	g := m.geom(a)
	if btn&(tcell.WheelUp|tcell.WheelDown) != 0 {
		d := wheelLines
		if btn&tcell.WheelUp != 0 {
			d = -d
		}
		if x > g.divX {
			m.scrollPreview(a, d)
		} else {
			m.scroll += d
			m.clampScroll(a)
		}
		return
	}
	if !pressed {
		return
	}
	if x < g.x || x >= g.x+g.w || y < g.y || y >= g.y+g.h {
		a.closeModal()
		return
	}
	switch {
	case g.pretty.contains(x, y):
		m.togglePretty()
		return
	case g.empty.contains(x, y):
		m.toggleSkipEmpty()
		return
	case g.all.contains(x, y):
		m.toggleAll()
		return
	case g.invert.contains(x, y):
		m.invert()
		return
	case g.cancel.contains(x, y):
		a.closeModal()
		return
	case g.copyBtn.contains(x, y):
		m.copyOut(a)
		return
	case g.apply.contains(x, y):
		m.apply(a)
		return
	}
	row := y - g.top
	if x >= g.divX || row < 0 || row >= g.rows || m.scroll+row >= len(m.vis()) {
		return
	}
	k := m.vis()[m.scroll+row]
	now := time.Now()
	again := m.lastClickRow == k && now.Sub(m.lastClickAt) < doubleClickMs
	m.cursor, m.lastClickRow, m.lastClickAt = k, k, now
	if x < g.listX+1+4 || again { // the [x] box, or a double-click on the row
		m.toggle(k)
		m.lastClickRow = -1
	}
}

// -----------------------------------------------------------------------------
// Apply / Copy
// -----------------------------------------------------------------------------

// apply rewrites what the dialog read with the projection, as one undo
// step, and closes. Refused (with the reason) when the tab is gone or
// its text moved since the dialog read it — see the file comment.
func (m *jsonKeysModal) apply(a *App) {
	if a.tabIndexOf(m.tab) < 0 {
		a.closeModal()
		a.flash("That tab was closed — nothing to reformat")
		return
	}
	if m.tab.EditRev != m.rev {
		a.closeModal()
		a.flash("The text changed while the dialog was open (reloaded?) — reopen it to reformat what's there now")
		return
	}
	text := string(m.output())
	n, keys, total := m.kept, m.keptKeys(), len(m.recs.Keys)
	a.closeModal()
	if text == m.src {
		a.flash("Nothing to change — every record already reads that way")
		return
	}
	res, ok := m.tab.ApplyMultiEdit([]editor.Edit{{Start: m.from, End: m.to, NewText: text}})
	if !ok || len(res) == 0 {
		a.flash("Couldn't apply the reformat")
		return
	}
	// A selection is selected again (the reformatted text), so the
	// dialog reopens on it; a whole file leaves the caret at the top of
	// what changed.
	m.tab.MoveCursorTo(res[0].Start, false)
	if m.selection {
		m.tab.MoveCursorTo(res[0].End, true)
	}
	a.flash("Reformatted " + plural(n, "record", "records") + ", keeping " + itoa(keys) + " of " +
		plural(total, "key", "keys") + " — Undo puts it back")
}

// copyOut puts the projection on the clipboard — ced's own and the
// system's — and leaves the dialog up, so a user can copy, change a
// tick, and copy again.
func (m *jsonKeysModal) copyOut(a *App) {
	text := string(m.output())
	a.clipBuf, a.clipKind = text, clipText
	what := plural(m.kept, "record", "records")
	if err := jsonKeysCopyFn(text); err != nil {
		a.flash("Copied " + what + " (system clipboard unavailable)")
		return
	}
	// A big copy says how big: OSC 52 is how the text leaves, and many
	// terminals cap it, so "Copied" with nothing pasted has a lead.
	if len(text) >= 1<<10 {
		what += " (" + humanSize(len(text)) + ")"
	}
	a.flash("Copied " + what)
}
