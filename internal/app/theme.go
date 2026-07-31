// =============================================================================
// File: internal/app/theme.go
// Author: Rohan Allison <rohanthewiz@gmail.com>
// Created: 2026-07-31
// Copyright: 2026 Rohan Allison. All rights reserved.
// =============================================================================

// theme.go wires the named-theme registry (internal/theme) into the
// editor: loading it at startup, switching live from the ≡ menu, writing
// a starter file for "Customize theme…", and re-reading a theme file the
// moment the user saves it.
//
// House rules:
//
//   - **A theme switch is a live restyle, never a restart.** setTheme
//     assigns App.theme, repaints the screen's default style, and marks
//     every open tab's cached syntax styles stale (Tab.Styles is the ONE
//     thing in the editor that caches theme-derived colors — every other
//     surface derives its styles inside its own draw call). Anything
//     that starts caching colors must join restyleTabs or it will keep
//     painting the old palette until the file is edited.
//   - **Same silent-degradation contract as the LSP and formatters.** A
//     broken theme file, an unknown saved name, an unwritable config —
//     all of it flashes once and leaves the editor running on the
//     shipped default. There is no path where a color preference can
//     stop ced from opening.
//   - **Editing a theme file is the customization UI.** ced has no
//     settings modal and isn't getting one (see CLAUDE.md), so
//     "Customize theme…" writes the current palette out in full,
//     opens it as a tab, and themeAfterSave re-reads it on every save.
//     That save-to-preview loop is the feature; don't replace it with a
//     color-picker modal.
//   - **Every surface goes through the palette picker** (the openPicker
//     house rule) rather than a bespoke list modal.
package app

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/gdamore/tcell/v2"
	"github.com/rohanthewiz/ced/internal/theme"
	"github.com/rohanthewiz/ced/internal/userconfig"
)

// themeDirFn resolves the user-themes directory. A package var, not a
// direct call, so tests can point the whole feature at a t.TempDir()
// instead of the developer's real ~/.config/ced/themes — newTestApp pins
// it to "", which means "built-ins only".
var themeDirFn = userconfig.ThemesDir

// themeConfigPathFn resolves config.json for persistence, stubbed in
// tests for the same reason: picking a theme must never rewrite the
// developer's own config.
var themeConfigPathFn = userconfig.DefaultPath

// loadThemes rebuilds the theme registry from the built-ins plus the
// user's themes directory. Broken files are reported (first one only —
// a flash is one line) and the rest still load, so one stray comma
// doesn't cost the user their other themes.
func (a *App) loadThemes() {
	specs, errs := theme.Registry(themeDirFn())
	a.themeSpecs = specs
	if len(errs) > 0 {
		a.flash("theme: " + errs[0].Error())
	}
}

// applyThemeName resolves name through the registry and installs it. An
// unknown name or an unresolvable palette falls back to the shipped
// default and flashes why — the editor is always left readable.
//
// persist writes the choice to config.json. It's false during startup
// (we're reading the config, not writing it) and on the theme-file save
// hook (the active theme's name hasn't changed, only its contents).
func (a *App) applyThemeName(name string, persist bool) {
	if strings.TrimSpace(name) == "" {
		name = theme.DefaultName
	}
	th, spec, err := theme.Resolve(a.themeSpecs, name)
	if err != nil {
		a.flash("theme: " + err.Error())
	}
	a.setTheme(th, spec.Name)
	if persist {
		if err := userconfig.SaveTheme(themeConfigPathFn(), spec.Name); err != nil {
			a.flash("theme: " + err.Error())
		}
	}
}

// setTheme installs a resolved palette. Everything a live switch has to
// touch is here, and it's deliberately short: the editor draws from
// App.theme on every frame, so the only stale state in the whole program
// is the per-tab syntax-highlight cache and the screen's default style
// (which fills cells no draw call reaches).
func (a *App) setTheme(th theme.Theme, name string) {
	a.theme = th
	a.themeName = name
	if a.screen != nil {
		a.screen.SetStyle(tcell.StyleDefault.Background(th.BG).Foreground(th.Text))
		a.screen.Clear()
	}
	a.restyleTabs()
}

// restyleTabs invalidates every open tab's cached syntax styles so the
// next render re-runs Chroma against the new palette. Without this a
// theme switch repaints the chrome but leaves the code in the old
// colors until the buffer is edited — the single most obvious way this
// feature could look broken.
func (a *App) restyleTabs() {
	for _, t := range a.tabs {
		t.StyleStale = true
	}
}

// activeThemeSpec returns the spec for the theme currently installed,
// falling back to the default's spec when the registry no longer has it
// (a user deleted the file mid-session). Used for labels and as the
// starting point for "Customize theme…".
func (a *App) activeThemeSpec() theme.Spec {
	if s, ok := theme.Find(a.themeSpecs, a.themeName); ok {
		return s
	}
	s, _ := theme.Find(theme.Builtins(), theme.DefaultName)
	return s
}

// themeMenuLabel is the ≡ row's dynamic label — it names the theme in
// force so the menu answers "which theme am I on?" without opening the
// picker.
func (a *App) themeMenuLabel() string {
	return "Theme: " + a.activeThemeSpec().Label + "…"
}

// menuTheme opens the registry as a fuzzy picker. Unlike the chat-model
// picker, the current theme is KEPT in the list (annotated) rather than
// excluded: re-picking it is how a user reverts after previewing
// something else, and a row vanishing from a list they're scanning reads
// as a bug. Light themes are marked — picking one by accident in a dark
// room is the one theme switch that genuinely hurts.
func (a *App) menuTheme() {
	a.closeMenu()
	a.loadThemes() // pick up files added since startup
	items := make([]paletteItem, 0, len(a.themeSpecs))
	for _, s := range a.themeSpecs {
		items = append(items, paletteItem{
			label: themePickerLabel(s, s.Name == a.themeName),
			run:   func(app *App) { app.applyThemeName(s.Name, true) },
		})
	}
	a.openPicker("Theme", items)
}

// themePickerLabel renders one registry row: the human label, a "(light)"
// marker, a "(custom)" marker for user themes, and "(current)" on the
// installed one. The markers are suffixes rather than columns because
// the palette's fuzzy scorer searches the whole label — "light" and
// "custom" then work as search terms for free.
func themePickerLabel(s theme.Spec, current bool) string {
	label := s.Label
	if !s.Dark {
		label += " (light)"
	}
	if s.Source == theme.SourceUser {
		label += " (custom)"
	}
	if current {
		label += " — current"
	}
	return label
}

// menuThemeCustomize writes the active theme out to
// ~/.config/ced/themes/<name>-custom.json — fully expanded, every
// derived key spelled out — and opens it in a tab.
//
// Expanded rather than sparse on purpose: a user customizing a theme
// wants to see the whole board, including the twenty-seven colors they'd
// otherwise never learn the names of. The sparse form is for themes
// people write from scratch; this is for themes people tweak.
//
// The "-custom" suffix keeps the copy from shadowing the built-in it was
// cloned from, which would make the original unreachable from the picker
// — surprising, and hard to undo without knowing where the file lives.
//
// Already on a user theme? Nothing is written: the row just opens the
// file. Re-running "Customize" must never rewrite an author's hand-kept
// sparse file into thirty-five expanded lines they didn't ask for.
func (a *App) menuThemeCustomize() {
	a.closeMenu()
	dir := themeDirFn()
	if dir == "" {
		a.flash("theme: no config directory — cannot write a theme file")
		return
	}
	base := a.activeThemeSpec()
	if base.Source == theme.SourceUser && base.Path != "" {
		a.openFile(base.Path)
		a.flash("Editing " + filepath.Base(base.Path) + " — save to preview")
		return
	}
	spec := base
	spec.Source = theme.SourceUser
	spec.Name = base.Name + "-custom"
	spec.Label = base.Label + " (custom)"
	path, err := theme.Save(dir, spec, true)
	if err != nil {
		a.flash("theme: " + err.Error())
		return
	}
	a.loadThemes()
	// Switch to the copy immediately: an editing session on a theme file
	// that isn't the one on screen is a guessing game. Saving the file
	// then repaints live (themeAfterSave).
	a.applyThemeName(spec.Name, true)
	a.openFile(path)
	a.flash("Editing " + filepath.Base(path) + " — save to preview")
}

// menuThemeReload re-reads the themes directory and re-applies the
// active theme. The save hook covers the common case (editing a theme
// inside ced), so this is for the rest: a theme file written by another
// program, dropped in by hand, or deleted.
func (a *App) menuThemeReload() {
	a.closeMenu()
	a.loadThemes()
	a.applyThemeName(a.themeName, false)
	a.flash(fmt.Sprintf("Reloaded themes (%d available)", len(a.themeSpecs)))
}

// themeAfterSave re-reads the registry when the file just saved is a
// theme file, and re-applies the active theme if that's the one that
// changed. This is what makes editing a theme in ced a live preview
// loop: change a hex, Save, watch the editor repaint.
//
// Called from saveTabAt after a successful write. Cheap enough to run on
// every save — it's a path-prefix check before it's anything else.
func (a *App) themeAfterSave(path string) {
	dir := themeDirFn()
	if dir == "" || !strings.EqualFold(filepath.Ext(path), theme.ThemeFileExt) {
		return
	}
	if abs, err := filepath.Abs(path); err == nil {
		path = abs
	}
	if absDir, err := filepath.Abs(dir); err == nil {
		dir = absDir
	}
	if filepath.Dir(path) != dir {
		return
	}
	a.loadThemes()
	// Re-apply unconditionally: the saved file may BE the active theme,
	// or may have started shadowing it (a new file named tokyo-night.json
	// takes over that name), and the difference isn't worth detecting.
	a.applyThemeName(a.themeName, false)
}
