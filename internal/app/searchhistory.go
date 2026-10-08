// =============================================================================
// File: internal/app/searchhistory.go
// Author: Rohan Allison <rohanthewiz@gmail.com>
// Created: 2026-09-29
// Copyright: 2026 Rohan Allison. All rights reserved.
// =============================================================================

// searchhistory.go is the history dropdown every find input carries: the
// find bar's Find and Repl rows, the Find all in file, Find in project
// and Go to symbol in project prompts, and the Find-all list's replace
// box. It lists what was searched here before, most recent first, and
// picking a row puts it in the field. FILTER boxes (the Find-all list's,
// the git log's, the references list's) have none: they narrow results
// already in hand rather than asking a question worth recalling.
//
// WHERE THE LISTS COME FROM. The repository's history database
// (internal/history/searches.go) — one list per kind of question, so the
// three text finds share theirs and a replacement never shows up as a
// search. A search is recorded where it RUNS (showFindAll,
// startProjectSearch, startWorkspaceSymbols, the find bar's Enter and
// close), not in the prompt, so it is remembered whichever door it came
// through — a selection-seeded Esc-F included.
//
// WHY NOT openPicker. The house rule sends every choose-one-from-a-list
// UI through the palette, but the palette takes the single modal slot,
// and every one of these inputs is either the find bar (which the slot
// closes) or a prompt that IS the slot. Picking a history entry through a
// picker would tear down the very field the entry is meant to fill. A
// dropdown drawn over the field's own surface is the one shape that keeps
// the field alive underneath — the completion popup's argument, one layer
// up. It is the second exception to the rule, beside the Find-all list.
//
// THE GESTURES. Up opens it (Down already means "list every hit" in the
// find bar, and a shell's Up is "what did I type before"); so does a
// click on the ▾. Arrows move VISUALLY, so in a list that opened above
// the field Up walks to older entries and Down back toward the field;
// moving past the newest entry closes the list and hands the keys back.
// Enter or Tab or a click fills the field and closes — it does NOT submit,
// because a remembered query is as often the start of the next one as the
// thing itself, and Enter again is the confirmation. Delete (or the row's
// ×) forgets an entry. Any other key closes the list and is typed.
//
//	        ┌ recent searches ────────────┐
//	        │ handleKey                 × │   ← oldest shown
//	        │ openFind                  × │
//	        │ findBarRows               × │   ← newest, nearest the field
//	        └──────────── ⏎ use · ⌦ forget┘
//	 Find▾  findBar█            Aa  |W|
//
// Geometry is ONE method (histDropGeom) that draw, keys and the mouse all
// take, so the row a click lands on is the row that was painted there.

package app

import (
	"fmt"

	"github.com/gdamore/tcell/v2"

	"github.com/rohanthewiz/ced/internal/history"
)

const (
	// histDropRows caps the visible rows. Fewer than the palette's ten:
	// the list opens over the code (or the prompt) the user is working
	// on, and the entries that matter are the last handful.
	histDropRows = 8

	// histDropMaxWidth caps how wide a long entry may stretch the box;
	// past it entries elide. The box is never narrower than its field.
	histDropMaxWidth = 64

	// histDropForgetW is the cells the row's × claims at its right end —
	// three rather than one so the target is hittable without precision.
	histDropForgetW = 3
)

// histDrop is one input's dropdown state. The owner (App for the find
// bar, promptModal for a prompt) holds it by value; the zero value is a
// closed dropdown.
type histDrop struct {
	open bool
	// kind names the history list shown (history.SearchFind, …).
	kind string
	// preferUp opens the list ABOVE its field when there is room — the
	// find bar sits at the bottom of the editor, so down would run into
	// the status bar or cover a docked panel.
	preferUp bool
	// items is the list as it stood when the dropdown opened, most
	// recent first. A snapshot, so a search recorded meanwhile cannot
	// shift the rows under the pointer.
	items []string
	// sel is the highlighted item index; scroll is the index of the item
	// shown NEAREST the field (the first one past the border on that side).
	sel    int
	scroll int
}

// histGeom is the dropdown's resolved geometry: the outer box (border
// included), how many item rows fit, and which side of the field it
// opened on.
type histGeom struct {
	x, y, w, h int
	rows       int
	up         bool
}

// ok reports whether there is a box to draw or hit at all.
func (g histGeom) ok() bool { return g.rows > 0 }

// contains reports whether (x, y) falls inside the box.
func (g histGeom) contains(x, y int) bool {
	return g.ok() && x >= g.x && x < g.x+g.w && y >= g.y && y < g.y+g.h
}

// histNoun is what a kind's list is called in the title and the flash.
func histNoun(kind string) string {
	switch kind {
	case history.SearchReplace:
		return "replacements"
	case history.SearchSymbol:
		return "symbol searches"
	case history.CopyDestinations:
		// Copy to…'s folder list (copyto.go) — the one kind that is not a
		// search, so "recent searches" would misname it.
		return "destinations"
	case history.EntryCommands:
		// Shell command…'s templates (entrycmd.go).
		return "commands"
	}
	return "searches"
}

// searchHistory returns kind's list for this repository, most recent
// first.
func (a *App) searchHistory(kind string) []string {
	return a.repoHistory().Searches(kind)
}

// recordSearch remembers text in kind's list. Memory only — the database
// is written on Close with the rest of the repository's history, so a
// search costs no IO.
func (a *App) recordSearch(kind, text string) {
	a.repoHistory().RecordSearch(kind, text)
}

// openHistDrop opens d on kind's list, highlighting current when it is
// already in the list (so Up from a recalled query carries on to the one
// before it). An empty list flashes and stays closed rather than opening
// an empty box — the button still answers, per the rule that an
// unavailable control explains itself.
func (a *App) openHistDrop(d *histDrop, kind, current string, preferUp bool) bool {
	items := a.searchHistory(kind)
	if len(items) == 0 {
		a.flash("No recent " + histNoun(kind) + " in this project yet")
		*d = histDrop{}
		return false
	}
	*d = histDrop{open: true, kind: kind, preferUp: preferUp, items: items}
	for i, s := range items {
		if s == current {
			d.sel = i
			break
		}
	}
	return true
}

// histDropGeom lays the dropdown out against its field: anchor is the
// field's row and columns. The box is at least the field's width (plus
// its border) and grows for long entries up to histDropMaxWidth. It opens
// on the preferred side and flips when that side cannot hold it but the
// other has more room; when neither holds every row it keeps the roomier
// side and shows fewer. The status bar row is never covered.
func (a *App) histDropGeom(d *histDrop, anchor btnRect) histGeom {
	if !d.open || len(d.items) == 0 {
		return histGeom{}
	}
	n := min(len(d.items), histDropRows)

	w := anchor.w + 2
	for _, s := range d.items {
		// Two cells of left pad, the × column, and the border.
		w = max(w, min(runeLen(s)+histDropForgetW+4, histDropMaxWidth))
	}
	w = min(w, a.width)

	above := anchor.y                        // rows 0 … anchor.y-1
	below := (a.height - 1) - (anchor.y + 1) // down to the row above the status bar
	up := d.preferUp
	if up && above < n+2 && below > above {
		up = false
	}
	if !up && below < n+2 && above > below {
		up = true
	}
	room := below
	if up {
		room = above
	}
	if room < n+2 {
		n = room - 2
	}
	if n < 1 {
		return histGeom{}
	}
	h := n + 2

	x := anchor.x - 1
	if x+w > a.width {
		x = a.width - w
	}
	x = max(x, 0)
	y := anchor.y + 1
	if up {
		y = anchor.y - h
	}
	d.clampScroll(n)
	return histGeom{x: x, y: y, w: w, h: h, rows: n, up: up}
}

// clampScroll keeps the highlighted item inside the rows shown.
func (d *histDrop) clampScroll(rows int) {
	if d.sel < d.scroll {
		d.scroll = d.sel
	}
	if d.sel >= d.scroll+rows {
		d.scroll = d.sel - rows + 1
	}
	d.scroll = max(0, min(d.scroll, len(d.items)-rows))
}

// itemAtRow maps visual row r (0 = first row inside the top border) to an
// item index: newest nearest the field, so an upward list counts from its
// bottom.
func (d *histDrop) itemAtRow(g histGeom, r int) int {
	if g.up {
		return d.scroll + (g.rows - 1 - r)
	}
	return d.scroll + r
}

// step moves the highlight toward older entries (delta > 0) or back
// toward the field (delta < 0). Past the newest entry it closes: the
// arrow that walked into the list walks back out of it.
func (d *histDrop) step(g histGeom, delta int) {
	next := d.sel + delta
	if next < 0 {
		*d = histDrop{}
		return
	}
	d.sel = min(next, len(d.items)-1)
	d.clampScroll(g.rows)
}

// forget drops item i from the list and from the repository's history.
// The last one going closes the dropdown — an empty box is a list of
// nothing.
func (a *App) histDropForget(d *histDrop, g histGeom, i int) {
	if i < 0 || i >= len(d.items) {
		return
	}
	a.repoHistory().ForgetSearch(d.kind, d.items[i])
	d.items = append(d.items[:i:i], d.items[i+1:]...)
	if len(d.items) == 0 {
		*d = histDrop{}
		return
	}
	d.sel = min(d.sel, len(d.items)-1)
	d.clampScroll(min(g.rows, len(d.items)))
}

// histDropKey routes a keystroke while d is open. consumed=false means
// the dropdown closed itself and the key belongs to the field; picked
// carries the chosen entry, already closed.
func (a *App) histDropKey(d *histDrop, g histGeom, ev *tcell.EventKey) (consumed bool, pick string, picked bool) {
	if !g.ok() {
		*d = histDrop{}
		return false, "", false
	}
	// Arrows move on screen: in a list above its field, Up is away from
	// the field (older); below it, Down is.
	older := tcell.KeyDown
	if g.up {
		older = tcell.KeyUp
	}
	switch ev.Key() {
	case tcell.KeyEsc:
		// Esc drops the list and nothing else — the field (and the bar or
		// prompt around it) stays exactly as it was.
		*d = histDrop{}
		return true, "", false
	case tcell.KeyEnter, tcell.KeyTab:
		v := d.items[d.sel]
		*d = histDrop{}
		return true, v, true
	case tcell.KeyUp, tcell.KeyDown:
		if ev.Key() == older {
			d.step(g, 1)
		} else {
			d.step(g, -1)
		}
		return true, "", false
	case tcell.KeyPgUp, tcell.KeyPgDn:
		if (ev.Key() == tcell.KeyPgUp) == g.up {
			d.step(g, g.rows)
		} else if d.sel > 0 {
			d.step(g, -min(g.rows, d.sel))
		}
		return true, "", false
	case tcell.KeyDelete:
		a.histDropForget(d, g, d.sel)
		return true, "", false
	}
	*d = histDrop{}
	return false, "", false
}

// histDropMouse routes a mouse event while d is open: hover tracks the
// pointer, the wheel walks the list, a click on a row picks it and a click
// on its × forgets it, a press elsewhere inside is swallowed, and a press
// outside closes the list and falls through (consumed=false) to whatever
// it was aimed at — the completion popup's contract.
//
// toggle is the owner's button that opened the list (the find bar's
// Find▾ / Repl▾ label, a prompt's ▾, the Find-all replace box's ⇄▾). A
// press there closes the list and is CONSUMED: handed back as an ordinary
// outside press it would reach that same button with the list already
// closed, and the button would open it again — a dropdown its own button
// could never put away. Zero-width means none.
func (a *App) histDropMouse(d *histDrop, g histGeom, toggle btnRect, x, y int, btn tcell.ButtonMask) (consumed bool, pick string, picked bool) {
	if !g.ok() {
		return false, "", false
	}
	if btn&tcell.Button1 != 0 && toggle.w > 0 && toggle.contains(x, y) {
		*d = histDrop{}
		return true, "", false
	}
	inside := g.contains(x, y)
	if btn&(tcell.WheelUp|tcell.WheelDown) != 0 {
		if !inside {
			return false, "", false
		}
		// Wheel toward the top of the screen shows what is up there.
		if (btn&tcell.WheelUp != 0) == g.up {
			d.step(g, 1)
		} else if d.sel > 0 {
			d.step(g, -1)
		}
		return true, "", false
	}
	if r := y - g.y - 1; inside && r >= 0 && r < g.rows && x > g.x && x < g.x+g.w-1 {
		i := d.itemAtRow(g, r)
		if i >= len(d.items) {
			return true, "", false
		}
		d.sel = i
		if btn&tcell.Button1 == 0 {
			return true, "", false // hover
		}
		if x >= g.x+g.w-1-histDropForgetW {
			a.histDropForget(d, g, i)
			return true, "", false
		}
		v := d.items[i]
		*d = histDrop{}
		return true, v, true
	}
	if btn&(tcell.Button1|tcell.Button2|tcell.Button3) == 0 {
		return inside, "", false
	}
	if inside {
		return true, "", false
	}
	*d = histDrop{}
	return false, "", false
}

// drawHistDrop paints the dropdown: bordered box in the modal chrome, the
// list's name on the top border, the highlighted row on the editor
// background (the palette's block highlight), a muted × at each row's
// end, and on the bottom border the position when the list scrolls plus
// the two key hints when they fit.
func (a *App) drawHistDrop(d *histDrop, g histGeom) {
	if !g.ok() {
		return
	}
	c := a.chrome()
	fillRect(a.screen, g.x, g.y, g.w, g.h, c.bgSt)
	drawBorder(a.screen, g.x, g.y, g.w, g.h, c.border)
	if title := " recent " + histNoun(d.kind) + " "; runeLen(title) < g.w-2 {
		drawAt(a.screen, g.x+2, g.y, title, c.muted)
	}

	textW := g.w - 3 - histDropForgetW // left pad + border, then the × column
	for r := 0; r < g.rows; r++ {
		i := d.itemAtRow(g, r)
		if i >= len(d.items) {
			continue
		}
		ry := g.y + 1 + r
		rowSt, xSt := c.body, c.muted
		if i == d.sel {
			rowSt = tcell.StyleDefault.Background(a.theme.BG).Foreground(a.theme.Text).Bold(true)
			xSt = tcell.StyleDefault.Background(a.theme.BG).Foreground(a.theme.Muted)
			for cx := g.x + 1; cx < g.x+g.w-1; cx++ {
				a.screen.SetContent(cx, ry, ' ', nil, rowSt)
			}
		}
		drawAt(a.screen, g.x+2, ry, elide(d.items[i], textW), rowSt)
		drawAt(a.screen, g.x+g.w-1-histDropForgetW+1, ry, "×", xSt)
	}

	bottom := g.y + g.h - 1
	left := g.x + 1
	if len(d.items) > g.rows {
		pos := fmt.Sprintf(" %d/%d ", d.sel+1, len(d.items))
		if runeLen(pos) < g.w-2 {
			drawAt(a.screen, left, bottom, pos, c.muted)
			left += runeLen(pos)
		}
	}
	if hint := " ⏎ use · ⌦ forget "; left+runeLen(hint) <= g.x+g.w-1 {
		drawAt(a.screen, g.x+g.w-1-runeLen(hint), bottom, hint, c.muted)
	}
}
