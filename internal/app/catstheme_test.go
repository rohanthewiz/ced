// =============================================================================
// File: internal/app/catstheme_test.go
// Author: Rohan Allison <rohanthewiz@gmail.com>
// Created: 2026-08-12
// Copyright: 2026 Rohan Allison. All rights reserved.
// =============================================================================

package app

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/gdamore/tcell/v2"

	"github.com/rohanthewiz/ced/internal/cats"
	"github.com/rohanthewiz/ced/internal/theme"
)

// hostPalette is a stand-in for what cats' config.get hands back: the eight
// required keys, the two surfaces ced borrows, and — crucially — the
// translucent rgba() keys cats' own derivation table emits, which ced cannot
// render and must decline.
func hostPalette() map[string]string {
	return map[string]string{
		"bg": "#1f2420", "fg": "#d6ddd6", "muted": "#9db0a2", "line": "#38403a",
		"accent": "#4db380", "ok": "#6ac47a", "warn": "#e0b64e", "err": "#e57373",
		"panel": "#242a25", "panel2": "#2b322c",
		"chrome": "#2b322c", "heading": "#6cbf8d",
		"sel-fill": "rgba(88,204,140,0.30)", "hover": "rgba(255,255,255,.16)",
	}
}

// The synthesis takes the ten keys the two palettes agree on and lets ced's
// own derivation table invent the rest — including the whole syntax palette,
// which the host has no opinion about at all. That is the difference between
// wearing the host's colors and pretending it is an editor.
func TestCatsHostSpecSynthesizesFromTheCoreEight(t *testing.T) {
	spec, ok := catsHostSpec("cats-green", hostPalette())
	if !ok {
		t.Fatal("a complete host palette should synthesize")
	}
	if spec.Name != catsHostThemeName || spec.Source != theme.SourceHost {
		t.Fatalf("identity = %q / %q", spec.Name, spec.Source)
	}
	if spec.Label != "Cats (host: cats-green)" {
		t.Fatalf("label = %q — it must name the host theme, so a stale row is visible", spec.Label)
	}
	if !spec.Dark {
		t.Fatal("a #1f2420 background is dark")
	}
	// The sparse form carries exactly the mapped keys, nothing invented.
	if got, want := len(spec.Colors), len(catsHostPalette); got != want {
		t.Fatalf("mapped %d keys, want %d: %v", got, want, spec.Colors)
	}
	if spec.Colors["sidebar-bg"] != "#242a25" || spec.Colors["line-hl"] != "#2b322c" {
		t.Fatalf("the two surfaces did not cross over: %v", spec.Colors)
	}
	// And it resolves to a full editor palette, syntax colors included.
	th, err := spec.Resolve()
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if th.SynKeyword == th.SynString {
		t.Fatal("derived syntax colors collapsed together")
	}
	if th.Accent == th.BG {
		t.Fatal("accent and background resolved to the same color")
	}
}

// cats accepts any CSS color expression; ced's palette is hex. Non-hex
// values are dropped rather than approximated — and if that costs a CORE
// key, the whole synthesis is abandoned, because a theme built from five of
// eight keys is a stranger wearing the host's name.
func TestCatsHostSpecRejectsIncompletePalettes(t *testing.T) {
	if _, ok := catsHostSpec("", nil); ok {
		t.Fatal("an empty palette synthesized something")
	}
	noAccent := hostPalette()
	delete(noAccent, "accent")
	if _, ok := catsHostSpec("x", noAccent); ok {
		t.Fatal("a missing core key must abandon the synthesis")
	}
	cssAccent := hostPalette()
	cssAccent["accent"] = "rgba(77,179,128,0.9)"
	if _, ok := catsHostSpec("x", cssAccent); ok {
		t.Fatal("a non-hex core key must abandon the synthesis")
	}
	// A non-hex NON-core key is merely skipped: the derivation table has
	// an answer for it.
	softPanel := hostPalette()
	softPanel["panel"] = "color-mix(in srgb, black 20%, green)"
	spec, ok := catsHostSpec("x", softPanel)
	if !ok {
		t.Fatal("a non-hex derived key must not sink the theme")
	}
	if _, stated := spec.Colors["sidebar-bg"]; stated {
		t.Fatal("an unrenderable value was carried through anyway")
	}
}

// catsHexColor is the whole filter, so it is pinned directly: cats' own
// values arrive in all of these forms.
func TestCatsHexColor(t *testing.T) {
	for _, ok := range []string{"#abc", "#AABBCC", "#1f2420"} {
		if !catsHexColor(ok) {
			t.Errorf("catsHexColor(%q) = false", ok)
		}
	}
	for _, bad := range []string{"", "abc", "#ab", "#abcd", "rgba(1,2,3,.5)", "#gggggg", "var(--x)"} {
		if catsHexColor(bad) {
			t.Errorf("catsHexColor(%q) = true", bad)
		}
	}
}

// The unpinned editor follows its host. This is the feature: open ced in a
// cats pane and it is already wearing the right colors.
func TestCatsThemeAutoSelectedWhenUnpinned(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	a.themePinned = false

	a.catsThemeArrived(cats.ConfigTheme{Name: "cats-green", Colors: hostPalette()})

	if a.themeName != catsHostThemeName {
		t.Fatalf("themeName = %q, want the host theme", a.themeName)
	}
	if got, want := a.theme.BG, mustHostColor(t, "#1f2420"); got != want {
		t.Fatalf("background = %v, want the host's %v", got, want)
	}
	// It is in the picker's registry too, at the top — inside cats it is
	// the theme of the room you are sitting in.
	if len(a.themeSpecs) == 0 || a.themeSpecs[0].Name != catsHostThemeName {
		t.Fatal("the host theme should lead the registry")
	}
	// And the choice was NOT written down: a ced started in a plain
	// terminal tomorrow is still the shipped default.
	if _, err := os.Stat(themeConfigPathFn()); err == nil {
		t.Fatal("the auto-selected host theme must never be persisted")
	}
	if a.themePinned {
		t.Fatal("auto-selecting must not count as the user choosing")
	}
}

// A user who has picked a theme has answered this question already.
// Overriding that because they opened a terminal is how a feature gets
// switched off.
func TestCatsThemeLeavesAPinnedChoiceAlone(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	a.applyThemeName("darcula", true) // as if picked from the ≡ picker
	if !a.themePinned {
		t.Fatal("picking a theme should pin it")
	}

	a.catsThemeArrived(cats.ConfigTheme{Name: "cats-green", Colors: hostPalette()})

	if a.themeName != "darcula" {
		t.Fatalf("themeName = %q — a pinned choice was overruled", a.themeName)
	}
	// The row is still OFFERED, though: not being forced on the user is
	// different from being hidden from them.
	if _, ok := theme.Find(a.themeSpecs, catsHostThemeName); !ok {
		t.Fatal("the host theme should still be in the picker")
	}
}

// Pinned TO the host theme is the one pinned case that still follows: the
// user asked for "whatever cats is wearing", so a host switch has to land.
func TestCatsThemeFollowsHostWhenPinnedToIt(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	a.catsThemeArrived(cats.ConfigTheme{Name: "cats-green", Colors: hostPalette()})
	a.themePinned = true // as if the user picked the host row explicitly

	switched := hostPalette()
	switched["bg"] = "#101820"
	a.catsThemeArrived(cats.ConfigTheme{Name: "midnight", Colors: switched})

	if got, want := a.theme.BG, mustHostColor(t, "#101820"); got != want {
		t.Fatalf("background = %v, want %v — the host switched theme under us", got, want)
	}
	if a.activeThemeSpec().Label != "Cats (host: midnight)" {
		t.Fatalf("label = %q", a.activeThemeSpec().Label)
	}
}

// An identical palette is dropped before it can cost anything. The
// focus_changed poll fires often, and every re-apply invalidates every open
// tab's syntax cache — real work to redo a theme nobody changed.
func TestCatsThemeIgnoresAnUnchangedPalette(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	a.catsThemeArrived(cats.ConfigTheme{Name: "cats-green", Colors: hostPalette()})
	before := a.themeSpecs

	a.catsThemeArrived(cats.ConfigTheme{Name: "cats-green", Colors: hostPalette()})

	// The registry is rebuilt only when something actually changed, so a
	// no-op poll leaves the very same slice in place.
	if len(before) == 0 || &before[0] != &a.themeSpecs[0] {
		t.Fatal("an unchanged palette rebuilt the registry")
	}
}

// A user file named cats-host.json wins, the same shadow-in-place rule user
// themes have over built-ins. Two rows with one name is a bug report.
func TestCatsThemeYieldsToAUserFileOfTheSameName(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	dir := themeDirFn()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	file := `{"name":"cats-host","label":"Mine","colors":{
	  "bg":"#000000","fg":"#ffffff","muted":"#888888","line":"#444444",
	  "accent":"#00ff00","ok":"#00ff00","warn":"#ffff00","err":"#ff0000"}}`
	if err := os.WriteFile(filepath.Join(dir, "cats-host.json"), []byte(file), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}

	a.catsThemeArrived(cats.ConfigTheme{Name: "cats-green", Colors: hostPalette()})

	n := 0
	for _, s := range a.themeSpecs {
		if s.Name == catsHostThemeName {
			n++
			if s.Label != "Mine" {
				t.Fatalf("label = %q, want the user's file to win", s.Label)
			}
		}
	}
	if n != 1 {
		t.Fatalf("%d rows named %q", n, catsHostThemeName)
	}
}

// Below Tier 1 the poll is a single check — the path every editor in every
// other terminal takes.
func TestCatsPollThemeIsANoopAtTier0(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	a.catsPollTheme() // must not dial, must not panic
	if !a.cats.themePolledAt.IsZero() {
		t.Fatal("Tier 0 should not even start the rate-limit clock")
	}
	// And a host palette that never arrives leaves no row behind.
	if _, ok := theme.Find(a.themeSpecs, catsHostThemeName); ok {
		t.Fatal("a host theme appeared without a host")
	}
}

// mustHostColor resolves a hex string the way the theme package would, so a
// test can compare against the exact tcell color a palette produces.
func mustHostColor(t *testing.T, hex string) tcell.Color {
	t.Helper()
	spec := theme.Spec{Colors: theme.Palette{
		"bg": hex, "fg": "#ffffff", "muted": "#888888", "line": "#444444",
		"accent": "#00ff00", "ok": "#00ff00", "warn": "#ffff00", "err": "#ff0000",
	}}
	th, err := spec.Resolve()
	if err != nil {
		t.Fatalf("resolve %s: %v", hex, err)
	}
	return th.BG
}
