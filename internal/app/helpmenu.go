// =============================================================================
// File: internal/app/helpmenu.go
// Author: Rohan Allison <rohanthewiz@gmail.com>
// Created: 2026-11-17
// Copyright: 2026 Rohan Allison. All rights reserved.
// =============================================================================

// helpmenu.go is the ≡ Tools ▸ Help section: three doors to things ced
// already knew but only offered in passing (N-134, N-135).
//
//   - Keyboard reference — the which-key band, until now reachable only
//     by already knowing `Esc ?`.
//   - Messages… — the last flashes. A flash is one slot shown for three
//     seconds and overwritten by the next, and many carry the ONLY
//     explanation of a refusal ("a row that is unavailable explains
//     itself"). Missing it meant never learning why. The log keeps the
//     last flashLogMax with their times; the picker lists them newest
//     first and a pick copies the text.
//   - Open config.json — the ~25 hand-edited keys had no in-app door.
//     The row creates the file as `{}` when it is missing (so the door
//     always opens somewhere) and opens it as a plain tab. A config that
//     failed to load names its problem IN THE LABEL — the "startup errors
//     are HELD for a ≡ label" rule; before, it was only flashed once at
//     startup, under whatever else was starting.
package app

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/rohanthewiz/ced/internal/clipboard"
	"github.com/rohanthewiz/ced/internal/userconfig"
)

// flashLogMax caps the message log. Fifty is several minutes of an
// active session — enough to find the refusal you just missed — and the
// picker's title announces the cap.
const flashLogMax = 50

// flashEntry is one logged flash. A message repeated back to back is one
// entry with a count, so a flash fired on every tick can't push the rest
// of the log out.
type flashEntry struct {
	at    time.Time
	msg   string
	count int
}

// configPathFn is where Open config.json looks — a package var so tests
// point it into a temp dir (newTestApp does) instead of at the
// developer's real ~/.config/ced/config.json.
var configPathFn = userconfig.DefaultPath

// logFlash records msg in the message log (flash calls it). Empty
// messages are not news; a repeat of the newest entry bumps its count and
// time instead of adding a row.
func (a *App) logFlash(msg string, now time.Time) {
	if msg == "" {
		return
	}
	if n := len(a.flashLog); n > 0 && a.flashLog[n-1].msg == msg {
		a.flashLog[n-1].at = now
		a.flashLog[n-1].count++
		return
	}
	a.flashLog = append(a.flashLog, flashEntry{at: now, msg: msg, count: 1})
	if len(a.flashLog) > flashLogMax {
		// Copy down rather than reslice, so the backing array doesn't grow
		// for the life of the session.
		a.flashLog = append(a.flashLog[:0], a.flashLog[len(a.flashLog)-flashLogMax:]...)
	}
}

// menuKeyboardReference opens the which-key band (the `Esc ?` overlay)
// from the menu, which has to close first — the band refuses to open
// over it.
func (a *App) menuKeyboardReference() {
	a.closeMenu()
	a.menuWhichKey()
}

// menuMessages lists the logged flashes, newest first, in the picker. A
// pick copies the message (OSC 52), the one thing a user wants from a
// message they have now read in full.
func (a *App) menuMessages() {
	a.closeMenu()
	if len(a.flashLog) == 0 {
		a.flash("No messages yet")
		return
	}
	items := make([]paletteItem, 0, len(a.flashLog))
	for i := len(a.flashLog) - 1; i >= 0; i-- {
		e := a.flashLog[i]
		label := e.at.Format("15:04:05") + "  " + e.msg
		if e.count > 1 {
			label += fmt.Sprintf("  ×%d", e.count)
		}
		msg := e.msg
		items = append(items, paletteItem{label: label, run: func(app *App) {
			if err := clipboard.CopyToSystem(msg); err != nil {
				app.flash("Copy failed: " + err.Error())
				return
			}
			app.flash("Message copied")
		}})
	}
	a.openPicker(fmt.Sprintf("Messages — newest first (the last %d kept)", flashLogMax), items)
}

// menuOpenConfig opens ~/.config/ced/config.json as a tab, creating it as
// an empty object first when it doesn't exist. A config that failed to
// load also flashes its problem, so the user arrives knowing what to fix.
func (a *App) menuOpenConfig() {
	a.closeMenu()
	path := configPathFn()
	if _, err := os.Stat(path); os.IsNotExist(err) {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			a.flash("config.json: " + err.Error())
			return
		}
		if err := os.WriteFile(path, []byte("{}\n"), 0o644); err != nil {
			a.flash("config.json: " + err.Error())
			return
		}
	}
	a.openFile(path)
	if a.configLoadErr != "" {
		a.flash("config: " + a.configLoadErr)
	}
}

// openConfigLabel names the row, and carries the config's load problem
// when there was one — held here instead of being lost to a startup
// flash. The problem text is shortened to keep the row one line.
func (a *App) openConfigLabel() string {
	if a.configLoadErr == "" {
		return "Open config.json"
	}
	msg := a.configLoadErr
	if i := strings.IndexByte(msg, '\n'); i >= 0 {
		msg = msg[:i]
	}
	if r := []rune(msg); len(r) > 40 {
		msg = string(r[:39]) + "…"
	}
	return "Open config.json (problem: " + msg + ")"
}
