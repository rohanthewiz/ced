// =============================================================================
// File: internal/app/catstheme.go
// Author: Rohan Allison <rohanthewiz@gmail.com>
// Created: 2026-08-12
// Copyright: 2026 Rohan Allison. All rights reserved.
// =============================================================================

// Theme unity: the editor wears the colors of the terminal it is running
// inside. When ced is a pane in cats, config.get hands back the host's
// EFFECTIVE palette (the named theme with the user's per-key overrides
// already folded in), and this file turns it into a real ced theme —
// "Cats (host)" — that sits in the theme picker beside the built-ins and is
// auto-selected for anyone who has never pinned a theme of their own.
//
// WHY THIS IS A SYNTHESIS AND NOT A COPY
//
// The two palettes are the same size but not the same vocabulary. cats is a
// terminal multiplexer: its ~33 keys describe chrome — panels, scrollbar
// thumbs, hover washes, a selection fill — and it has no opinion whatsoever
// about how a keyword differs from a string. ced needs eleven syntax colors.
//
// What makes the bridge cheap is that BOTH palettes are built on the same
// eight required keys — bg, fg, muted, line, accent, ok, warn, err. So the
// synthesis is: take those eight (plus the two host surfaces that have an
// honest ced counterpart), and let ced's own derivation table (palette.go)
// invent the other twenty-seven exactly the way it does for a hand-written
// eight-line theme file. The editor ends up in the host's hues without ced
// pretending cats knows anything about syntax highlighting.
//
//	cats                     ced
//	────────────────────     ─────────────────────────────────────
//	bg fg muted line     ┐
//	accent ok warn err   ┴──► the same eight core keys, verbatim
//	panel                ───► sidebar-bg   (the second surface)
//	panel2               ───► line-hl      (the raised surface)
//	  (everything else)  ───► derived by ced (palette.go)
//
// THREE RULES THIS FILE KEEPS
//
//  1. **Hex only.** cats accepts any CSS color expression, and its own
//     derivation table emits rgba() for the translucent keys. ced's palette
//     is hex. Non-hex values are dropped rather than approximated; if that
//     costs a CORE key, the whole synthesis is abandoned (see catsHostSpec)
//     — a theme built from five of eight keys would be a stranger wearing
//     the host's name.
//
//  2. **Auto-select is for the UNPINNED only.** A user who has picked a
//     theme has answered this question already, and silently overriding
//     that answer because they opened a terminal is the kind of "smart"
//     that gets a feature switched off. The row is still in the picker for
//     them; it is just not forced on them.
//
//  3. **Never persisted.** The host theme is applied with persist=false, so
//     config.json keeps saying "no preference" and a ced started in a plain
//     terminal tomorrow is still the shipped default rather than a frozen
//     copy of one night's cats palette.
//
// REFRESH: the host PUSHES its palette now. cats' theme_changed event carries
// the resolved appearance, so a theme switch reaches ced as one frame with the
// answer already in it — no round trip, and no delay while some unrelated event
// happens to arrive.
//
// The focus_changed poll it replaces is still here, and deliberately: an older
// cats never sends theme_changed, and a client that dropped the poll on faith
// would simply stop following that host's colors, silently and forever. So the
// poll stays armed until the first event PROVES the host speaks it, and retires
// for the session at that moment (catsThemeEvented). Nothing is probed at
// startup, because there is nothing to probe — the event vocabulary is not
// enumerable over the wire — and the first push is the only evidence that
// exists.

package app

import (
	"strconv"
	"strings"
	"time"

	"github.com/rohanthewiz/ced/internal/cats"
	"github.com/rohanthewiz/ced/internal/theme"
)

const (
	// catsHostThemeName is the registry id of the synthesized theme. It is
	// a normal name in a normal registry, so everything that already works
	// on themes — the picker, "Customize theme…", a user file that shadows
	// it — works on this one with no special case.
	catsHostThemeName = "cats-host"

	// catsThemePollMin is the floor between two config.get reads. Focus
	// moves between panes many times a minute; a theme changes a few times
	// a year. The poll is a cheap local socket round trip, but it is not
	// free, and doing it on every focus flip would be spending the user's
	// battery to learn nothing.
	catsThemePollMin = 3 * time.Second
)

// catsHostPalette maps the host's key onto ced's, for the keys where the
// two vocabularies genuinely agree. The eight core keys are identity
// mappings — deliberately spelled out rather than assumed, so this table is
// the single place to look when either side renames one.
//
// Only two non-core keys are taken, and both are surfaces:
//
//   - panel is the host's second surface (its sidebar / pane list), which is
//     precisely what ced's sidebar-bg is for.
//   - panel2 is the host's raised surface, which is the same STEP ced's
//     active-line highlight takes away from the editor background.
//
// Notably NOT taken: chrome → status-bg. ced paints its status bar as
// background-colored text ON status-bg, which only reads if status-bg is a
// loud color; cats' chrome is a quiet dark surface, so importing it would
// produce a dark-on-dark status bar. ced's own derivation (status-bg =
// accent) keeps that bar legible in the host's accent, which is the part
// that carries the resemblance anyway.
var catsHostPalette = []struct{ host, ced string }{
	{"bg", "bg"},
	{"fg", "fg"},
	{"muted", "muted"},
	{"line", "line"},
	{"accent", "accent"},
	{"ok", "ok"},
	{"warn", "warn"},
	{"err", "err"},
	{"panel", "sidebar-bg"},
	{"panel2", "line-hl"},
}

// catsHostSpec synthesizes the "Cats (host)" theme from a host palette.
// hostName is the host's own theme name, used only for the label — knowing
// you are looking at "Cats (host: cats-green)" is what tells a user whether
// the row is stale.
//
// ok is false when the palette cannot produce a ced theme: a missing or
// non-hex core key, or a palette that will not normalize. Returning false
// rather than a partial theme is the point — see rule 1 in the file comment.
func catsHostSpec(hostName string, colors map[string]string) (theme.Spec, bool) {
	if len(colors) == 0 {
		return theme.Spec{}, false
	}
	p := make(theme.Palette, len(catsHostPalette))
	for _, m := range catsHostPalette {
		v := strings.TrimSpace(colors[m.host])
		if !catsHexColor(v) {
			continue // rgba(), a CSS name, var(…) — not ced's currency
		}
		p[m.ced] = v
	}
	// The core eight are the floor. ced's Normalize would say the same,
	// but checking here keeps the reason reportable and stops a
	// half-translated palette from ever reaching a Spec.
	for _, k := range theme.CoreKeys() {
		if p[k] == "" {
			return theme.Spec{}, false
		}
	}
	label := "Cats (host)"
	if n := strings.TrimSpace(hostName); n != "" {
		label = "Cats (host: " + n + ")"
	}
	spec := theme.Spec{
		Name:   catsHostThemeName,
		Label:  label,
		Dark:   theme.IsDark(p["bg"]),
		Colors: p,
		Source: theme.SourceHost,
	}
	// Resolve once as a validation pass: a spec that cannot resolve must
	// never reach the picker, because picking it would fall back to the
	// default and flash an error the user cannot act on.
	if _, err := spec.Resolve(); err != nil {
		return theme.Spec{}, false
	}
	return spec, true
}

// catsHexColor reports whether v is a color ced's palette can carry:
// "#rgb" or "#rrggbb". Everything else the host may legitimately hold —
// rgba() washes, CSS color names, var() references — is a value ced has no
// way to render into a tcell.Color, so it is declined rather than guessed at.
func catsHexColor(v string) bool {
	if !strings.HasPrefix(v, "#") {
		return false
	}
	body := v[1:]
	if len(body) != 3 && len(body) != 6 {
		return false
	}
	_, err := strconv.ParseUint(body, 16, 32)
	return err == nil
}

// catsPollTheme asks the host for its palette, off the main loop. Safe to
// call from any main-loop site: it declines below Tier 1 and inside the
// rate-limit window, so the call sites can just say "something happened
// that might have changed the theme" without tracking when they last asked.
func (a *App) catsPollTheme() {
	if !a.catsTier1() || a.screen == nil {
		return
	}
	if a.cats.themeEvented {
		// The host has pushed a theme_changed at least once, so it will push
		// the next one too. Asking anyway would be spending a round trip to
		// re-learn something already on its way.
		return
	}
	now := time.Now()
	if !a.cats.themePolledAt.IsZero() && now.Sub(a.cats.themePolledAt) < catsThemePollMin {
		return
	}
	a.cats.themePolledAt = now
	client, scr := a.cats.client, a.screen
	go func() {
		res, err := client.ConfigGet()
		if err != nil {
			return // a host that will not answer keeps whatever theme we have
		}
		_ = scr.PostEvent(&catsEvent{when: time.Now(), kind: catsKindTheme, theme: res.Theme})
	}()
}

// catsThemeArrived installs a freshly-read host palette: rebuild the
// synthesized spec, put it in the registry, and re-apply it if it is the
// theme on screen (or should become it).
//
// A palette identical to the one already synthesized returns immediately.
// That check is what makes the focus_changed poll free in the normal case —
// every repaint it would otherwise trigger invalidates every open tab's
// syntax cache (restyleTabs), which is real work to redo a theme nobody
// changed.
func (a *App) catsThemeArrived(ct cats.ConfigTheme) {
	spec, ok := catsHostSpec(ct.Name, ct.Colors)
	if !ok {
		return
	}
	if a.cats.hostThemeOK && spec.Label == a.cats.hostTheme.Label &&
		catsSamePalette(spec.Colors, a.cats.hostTheme.Colors) {
		return
	}
	a.cats.hostTheme, a.cats.hostThemeOK = spec, true
	a.loadThemes() // the registry rebuild is what puts the row in the picker
	// Apply when nobody has claimed the choice, or when the host theme IS
	// the current choice and its colors just moved under it.
	if a.themePinned && a.themeName != catsHostThemeName {
		return
	}
	a.applyThemeName(catsHostThemeName, false)
}

// catsSamePalette reports whether two palettes state the same colors. A
// plain map compare — both sides are the sparse, mapped form built by
// catsHostSpec, so there is nothing to normalize first.
func catsSamePalette(a, b theme.Palette) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if b[k] != v {
			return false
		}
	}
	return true
}

// catsHostThemeSpecs returns the synthesized theme as a one-element slice
// (or nothing), for loadThemes to prepend to the registry. It goes FIRST,
// ahead of the built-in default: inside cats it is the theme of the room
// you are sitting in, which is the one a user scanning the picker is most
// likely to want.
//
// A user file named cats-host.json wins and this returns nothing — the same
// shadow-in-place rule user themes have over built-ins (load.go), for the
// same reason: two rows with one name is a bug report.
func (a *App) catsHostThemeSpecs(registry []theme.Spec) []theme.Spec {
	if !a.cats.hostThemeOK {
		return nil
	}
	for _, s := range registry {
		if s.Name == catsHostThemeName {
			return nil
		}
	}
	return []theme.Spec{a.cats.hostTheme}
}
