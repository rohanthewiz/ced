// =============================================================================
// File: internal/app/whichkey.go
// Author: Rohan Allison
// =============================================================================

// The which-key overlay: the Esc-leader table, drawn when you hesitate.
//
// The leader is ced's entire command layer, and until now it was
// documented only in the ≡ menu's shortcut column — which you can't see
// while you're mid-gesture wondering what the second key was. This file
// makes the leader self-documenting: press Esc, pause ~350ms, and a
// bottom-anchored band lists every `key → label` pair; arm a namespace
// (Esc-a, Esc-x) and the band re-renders with that namespace's table.
// Fast hands never see it — the delay is longer than any practiced
// Esc-s — so the overlay costs an expert nothing.
//
// Three deliberate properties:
//
//   - PASSIVE. It does not claim the modal slot: no key is consumed by
//     the overlay itself. Leader keys fire exactly as they would have,
//     any other key dismisses the band and then does what it always
//     did, so muscle memory is never punished for the overlay existing.
//   - CLICKABLE. Rows are buttons (stamped-rect pattern, statusbar.go):
//     reading the list and clicking the row IS the discoverable path
//     the plan's north star asks for — see the label once, click it,
//     learn the key for next time.
//   - PATIENT. While the band is visible the leader window stops
//     expiring: the 500ms window exists to keep a stray Esc harmless,
//     but once the editor has SHOWN you the table, "I'm reading" must
//     not time out mid-read. Dismissal is explicit (Esc, any unbound
//     key, a click elsewhere) — and the double-Esc menu gesture is
//     checked first, so Esc-Esc means what it always meant.
//
// Redraw mechanics: ced's loop is event-driven, so the pause is a
// one-shot time.AfterFunc that posts a whichKeyEvent (the events-only
// rule — the goroutine never touches App state). A generation counter
// stamped into the event invalidates stale ticks: every disarm bumps
// it, so a tick scheduled by an Esc the user has since typed through
// arrives dead.

package app

import (
	"time"

	"github.com/gdamore/tcell/v2"
)

// whichKeyDelay is the hesitation that summons the overlay. Longer than
// a practiced leader gesture, shorter than "I've forgotten the key".
// Well under doubleEscMs matters too: the overlay must appear while the
// leader window is still live, or it would document a dead state.
const whichKeyDelay = 350 * time.Millisecond

// whichKeyEvent is the posted timer tick. seq pins it to the arm that
// scheduled it — see whichKeyState.seq.
type whichKeyEvent struct {
	when time.Time
	seq  int
}

// When satisfies the tcell.Event interface.
func (e *whichKeyEvent) When() time.Time { return e.when }

// Compile-time check that whichKeyEvent really is a tcell.Event.
var _ tcell.Event = (*whichKeyEvent)(nil)

// whichKeyRow is one stamped clickable row of the drawn overlay.
type whichKeyRow struct {
	rect btnRect
	fire func(*App)
}

// whichKeyState is the overlay's whole state: whether it's up, the arm
// generation, and the rects stamped by the last draw.
type whichKeyState struct {
	open bool
	// seq counts arms. A scheduled tick carries the seq at schedule
	// time and is dropped on arrival unless it still matches, which is
	// how "user typed something in the meantime" cancels a pending
	// overlay without tracking timers.
	seq  int
	rows []whichKeyRow
	// band is the overlay's full rectangle from the last draw, so a
	// click inside the band but between rows is swallowed rather than
	// falling through to whatever sits under the overlay.
	band struct{ x, y, w, h int }
}

// armWhichKey schedules the overlay to appear after whichKeyDelay if the
// leader (or a chord) is still armed when the tick lands.
func (a *App) armWhichKey() {
	if a.screen == nil {
		return
	}
	a.whichKey.seq++
	seq := a.whichKey.seq
	scr := a.screen
	time.AfterFunc(whichKeyDelay, func() {
		// Goroutine territory: post, never mutate (the iron rule).
		_ = scr.PostEvent(&whichKeyEvent{when: time.Now(), seq: seq})
	})
}

// handleWhichKeyTick opens the overlay when the tick that scheduled it
// is still current and something is still armed to document.
func (a *App) handleWhichKeyTick(e *whichKeyEvent) {
	if e.seq != a.whichKey.seq || a.whichKey.open {
		return
	}
	if a.modal != nil || a.menuOpen {
		return
	}
	armed := !a.lastEscape.IsZero() || a.leaderChord != nil
	if !armed {
		return
	}
	a.whichKey.open = true
}

// closeWhichKey hides the overlay and invalidates any tick in flight.
// Safe (and cheap) to call when it isn't open — the seq bump is also
// how ordinary typing cancels a pending overlay.
func (a *App) closeWhichKey() {
	a.whichKey.open = false
	a.whichKey.rows = nil
	a.whichKey.seq++
}

// whichKeyEntry is one key → label pair before layout.
type whichKeyEntry struct {
	key   string
	label string
	fire  func(*App)
}

// whichKeyEntries resolves what the overlay documents right now: the
// armed chord's sub-table when one is pending, the full leader table
// otherwise. Unlabeled bindings are hidden rather than shown blank —
// they stay bound, just undocumented, which is what they were before
// the overlay existed.
func (a *App) whichKeyEntries() (string, []whichKeyEntry) {
	if a.leaderChord != nil {
		name := a.leaderChordName
		if name == "" {
			name = "leader"
		}
		entries := make([]whichKeyEntry, 0, len(a.leaderChord))
		for i := range a.leaderChord {
			b := a.leaderChord[i] // copy: the closure must not share the loop slot
			if b.label == "" || b.action == nil {
				continue
			}
			entries = append(entries, whichKeyEntry{
				key:   string(b.key),
				label: b.label,
				fire: func(app *App) {
					app.clearChord()
					app.closeWhichKey()
					b.action(app)
				},
			})
		}
		return name + " — type a key or click · esc dismisses", entries
	}

	entries := make([]whichKeyEntry, 0, 48)
	for _, b := range leaderBindings() {
		b := b
		if b.label == "" {
			continue
		}
		entries = append(entries, whichKeyEntry{
			key:   string(b.key),
			label: b.label,
			// fireLeader, not b.action: a prefix row must arm its
			// namespace (and re-render this overlay), and a repeatable
			// row must chain, exactly as the key itself would.
			fire: func(app *App) { app.fireLeader(&b) },
		})
	}
	return "esc leader — type a key or click · esc esc opens the menu", entries
}

// drawWhichKey paints the bottom-anchored band and stamps the row rects
// (draw is the one geometry source; whichKeyClick only reads it).
// Entries flow column-major — read down, then across, the order
// which-key users expect.
func (a *App) drawWhichKey() {
	title, entries := a.whichKeyEntries()
	a.whichKey.rows = a.whichKey.rows[:0]
	if len(entries) == 0 {
		return
	}

	keyW, labelW := 1, 0
	for _, e := range entries {
		if w := runeLen(e.key); w > keyW {
			keyW = w
		}
		if w := runeLen(e.label); w > labelW {
			labelW = w
		}
	}
	colW := keyW + 1 + labelW + 3 // key, gap, label, inter-column air
	usable := a.width - 2
	cols := usable / colW
	if cols < 1 {
		cols = 1
	}
	nrows := (len(entries) + cols - 1) / cols
	// The band may take at most half the window: the text being edited
	// stays the main subject even while the cheat sheet is up.
	maxRows := (a.height - 2) / 2
	if maxRows < 1 {
		maxRows = 1
	}
	truncated := false
	if nrows > maxRows {
		nrows = maxRows
		truncated = true
		if len(entries) > nrows*cols {
			entries = entries[:nrows*cols]
		}
	}

	h := nrows + 1 // +1 title row
	y0 := a.height - 1 - h
	a.whichKey.band = struct{ x, y, w, h int }{0, y0, a.width, h}

	bg := a.theme.LineHL
	bgStyle := tcell.StyleDefault.Background(bg).Foreground(a.theme.Text)
	titleStyle := tcell.StyleDefault.Background(bg).Foreground(a.theme.Muted)
	keyStyle := tcell.StyleDefault.Background(bg).Foreground(a.theme.Accent).Bold(true)

	fillRect(a.screen, 0, y0, a.width, h, bgStyle)
	t := title
	if truncated {
		t += " · list clipped — ≡ shows everything"
	}
	drawStatusText(a.screen, 1, y0, a.width-2, t, titleStyle)

	for i, e := range entries {
		c, r := i/nrows, i%nrows
		x := 1 + c*colW
		y := y0 + 1 + r
		if x+colW > a.width {
			break
		}
		// Key right-aligned in its column so single chars and shifted
		// pairs line up; label starts one cell after.
		drawAt(a.screen, x+keyW-runeLen(e.key), y, e.key, keyStyle)
		drawStatusText(a.screen, x+keyW+1, y, labelW, e.label, bgStyle)
		a.whichKey.rows = append(a.whichKey.rows, whichKeyRow{
			rect: btnRect{x: x, y: y, w: colW - 1},
			fire: e.fire,
		})
	}
}

// whichKeyClick routes a left press while the overlay is up. A row
// fires; a press elsewhere in the band is swallowed (the overlay is
// visually opaque, so clicking "through" it would be a lie); a press
// outside dismisses the overlay, disarms the leader, and reports
// unconsumed so the click still does whatever it was aimed at.
func (a *App) whichKeyClick(x, y int) bool {
	if !a.whichKey.open {
		return false
	}
	for _, row := range a.whichKey.rows {
		if row.rect.contains(x, y) {
			row.fire(a)
			return true
		}
	}
	b := a.whichKey.band
	if x >= b.x && x < b.x+b.w && y >= b.y && y < b.y+b.h {
		return true
	}
	a.closeWhichKey()
	a.clearChord()
	a.lastEscape = time.Time{}
	return false
}
