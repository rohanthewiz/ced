// =============================================================================
// File: internal/app/theme_test.go
// Author: Rohan Allison <rohanthewiz@gmail.com>
// Created: 2026-07-31
// Copyright: 2026 Rohan Allison. All rights reserved.
// =============================================================================

// Tests for the editor's side of theming: switching live, invalidating
// the one cache that holds theme-derived colors, degrading quietly when
// a preference can't be honored, and the save-to-preview loop that
// stands in for a settings modal.
//
// newTestApp points themeDirFn/themeConfigPathFn at throwaway
// directories, so these tests can write real theme files without going
// anywhere near the developer's ~/.config/ced.

package app

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gdamore/tcell/v2"
	"github.com/rohanthewiz/ced/internal/editor"
	"github.com/rohanthewiz/ced/internal/theme"
	"github.com/rohanthewiz/ced/internal/userconfig"
)

// writeUserTheme drops a theme file into the throwaway themes directory
// newTestApp pointed themeDirFn at, and returns its path.
func writeUserTheme(t *testing.T, name, body string) string {
	t.Helper()
	dir := themeDirFn()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatalf("write theme: %v", err)
	}
	return path
}

// testThemeBody is a minimal, obviously-distinguishable palette: a pure
// red background nothing else in the registry has, so "did the theme
// actually take?" is a single comparison.
const testThemeBody = `{
  "label": "Test Red",
  "colors": {
    "bg": "#ff0000", "fg": "#ffffff", "muted": "#aa8888", "line": "#cc4444",
    "accent": "#ffcc00", "ok": "#00ff00", "warn": "#ffaa00", "err": "#ff00ff"
  }
}`

// TestApplyThemeName_SwitchesAndPersists is the happy path: picking a
// theme installs its palette and writes the choice to config.json so it
// survives a restart.
func TestApplyThemeName_SwitchesAndPersists(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	a.applyThemeName("solarized-light", true)

	if a.themeName != "solarized-light" {
		t.Errorf("themeName = %q, want solarized-light", a.themeName)
	}
	if a.theme == theme.Default() {
		t.Error("the palette did not change")
	}
	cfg, err := userconfig.Load(themeConfigPathFn())
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	if cfg.Theme != "solarized-light" {
		t.Errorf("persisted theme = %q, want solarized-light", cfg.Theme)
	}
}

// TestApplyThemeName_MarksTabsForRehighlight pins the one cache a theme
// switch has to invalidate. Tab.Styles holds Chroma output colored by
// the OLD palette; leaving it valid repaints the chrome but leaves the
// code in the previous theme's colors until the buffer is next edited —
// the most obvious way this feature could look broken.
func TestApplyThemeName_MarksTabsForRehighlight(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "main.go")
	if err := os.WriteFile(src, []byte("package main\n"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	a := newTestApp(t, dir)
	a.openFile(src)
	if len(a.tabs) != 1 {
		t.Fatalf("expected the file to open, got %d tabs", len(a.tabs))
	}
	// Render once so the cache is populated and the flag clears.
	a.tabs[0].Render(a.screen, a.theme, 0, 0, 80, 20)
	if a.tabs[0].StyleStale {
		t.Fatal("precondition: rendering should have cleared StyleStale")
	}

	a.applyThemeName("gruvbox-dark", false)
	if !a.tabs[0].StyleStale {
		t.Error("a theme switch must invalidate every tab's cached syntax styles")
	}
}

// TestApplyThemeName_UnknownFallsBackAndFlashes covers the silent-
// degradation contract: a saved preference naming a theme that no longer
// exists (deleted file, older binary) leaves the editor on the shipped
// default with an explanation, never on a broken palette.
func TestApplyThemeName_UnknownFallsBackAndFlashes(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	a.applyThemeName("theme-that-never-was", false)

	if a.theme != theme.Default() {
		t.Error("an unknown theme should leave the default palette installed")
	}
	if a.themeName != theme.DefaultName {
		t.Errorf("themeName = %q, want the default", a.themeName)
	}
	if !strings.Contains(a.statusMsg, "theme-that-never-was") {
		t.Errorf("status = %q, want it to name the missing theme", a.statusMsg)
	}
}

// TestApplyThemeName_EmptyMeansDefault pins the startup path: a config
// with no "theme" key resolves to the shipped default rather than to an
// error about the empty string.
func TestApplyThemeName_EmptyMeansDefault(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	a.applyThemeName("darcula", false)
	a.applyThemeName("", false)

	if a.themeName != theme.DefaultName || a.theme != theme.Default() {
		t.Errorf("themeName = %q, want the default with its palette", a.themeName)
	}
	if strings.Contains(a.statusMsg, "theme:") {
		t.Errorf("status = %q, want no complaint about an absent preference", a.statusMsg)
	}
}

// TestLoadThemes_PicksUpUserFiles checks that the registry the picker
// draws from includes ~/.config/ced/themes/*.json, and that a user theme
// resolves through the normal apply path.
func TestLoadThemes_PicksUpUserFiles(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	writeUserTheme(t, "test-red.json", testThemeBody)
	a.loadThemes()

	spec, ok := theme.Find(a.themeSpecs, "test-red")
	if !ok {
		t.Fatal("user theme did not reach the registry")
	}
	if spec.Source != theme.SourceUser {
		t.Errorf("Source = %q, want user", spec.Source)
	}
	a.applyThemeName("test-red", false)
	r, g, b := a.theme.BG.RGB()
	if r != 0xff || g != 0 || b != 0 {
		t.Errorf("BG = (%d,%d,%d), want the user theme's pure red", r, g, b)
	}
}

// TestLoadThemes_BrokenFileWarnsButKeepsTheRest pins per-file
// degradation: one unparseable theme costs the user that theme, not
// their whole registry, and it says so once.
func TestLoadThemes_BrokenFileWarnsButKeepsTheRest(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	writeUserTheme(t, "test-red.json", testThemeBody)
	writeUserTheme(t, "wrecked.json", `{"colors": {`)
	a.loadThemes()

	if !strings.Contains(a.statusMsg, "wrecked.json") {
		t.Errorf("status = %q, want it to name the broken file", a.statusMsg)
	}
	if _, ok := theme.Find(a.themeSpecs, "test-red"); !ok {
		t.Error("the good user theme was lost to its broken neighbour")
	}
	if _, ok := theme.Find(a.themeSpecs, theme.DefaultName); !ok {
		t.Error("the built-ins were lost to a broken user theme")
	}
}

// TestMenuTheme_OpensPickerWithEveryTheme pins the picker surface: it
// reuses the palette (the modal house rule), lists the whole registry,
// and — unlike the chat-model picker — KEEPS the current theme in the
// list, annotated, because re-picking it is how a user reverts.
func TestMenuTheme_OpensPickerWithEveryTheme(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	writeUserTheme(t, "test-red.json", testThemeBody)
	a.menuTheme()

	pm, ok := a.modal.(*paletteModal)
	if !ok {
		t.Fatalf("modal = %T, want *paletteModal", a.modal)
	}
	if len(pm.items) != len(a.themeSpecs) {
		t.Errorf("picker has %d rows, want %d (the whole registry)", len(pm.items), len(a.themeSpecs))
	}
	var joined string
	for _, it := range pm.items {
		joined += it.label + "\n"
	}
	for _, want := range []string{
		"Tokyo Night — current", // the active theme stays, annotated
		"Solarized Light (light)",
		"Test Red (custom)",
		"Darcula",
	} {
		if !strings.Contains(joined, want) {
			t.Errorf("picker rows missing %q:\n%s", want, joined)
		}
	}
}

// TestMenuTheme_PickApplies drives a picker row's action to confirm the
// wiring between the list and the switch — the rows are closures over
// per-theme state, which is exactly the kind of thing a loop-variable
// mistake silently breaks (every row picking the last theme).
func TestMenuTheme_PickApplies(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	a.menuTheme()
	pm := a.modal.(*paletteModal)

	var picked bool
	for _, it := range pm.items {
		if strings.HasPrefix(it.label, "Dark City") {
			it.run(a)
			picked = true
			break
		}
	}
	if !picked {
		t.Fatal("Dark City row not found")
	}
	if a.themeName != "dark-city" {
		t.Errorf("themeName = %q, want dark-city — each row must close over its OWN theme", a.themeName)
	}
}

// TestMenuThemeCustomize_WritesExpandedCopyAndOpensIt pins the flow that
// stands in for a settings modal: clone the active theme under a new
// name, expand every derived color so the author can see the whole
// board, switch to it, and open the file for editing.
func TestMenuThemeCustomize_WritesExpandedCopyAndOpensIt(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	a.menuThemeCustomize()

	want := filepath.Join(themeDirFn(), theme.DefaultName+"-custom.json")
	data, err := os.ReadFile(want)
	if err != nil {
		t.Fatalf("expected a theme file at %s: %v", want, err)
	}
	var ff theme.FileFormat
	if err := json.Unmarshal(data, &ff); err != nil {
		t.Fatalf("written theme is not valid JSON: %v", err)
	}
	if len(ff.Colors) != len(theme.Keys()) {
		t.Errorf("wrote %d colors, want all %d expanded", len(ff.Colors), len(theme.Keys()))
	}
	// The copy must not shadow the built-in it was cloned from —
	// that would make the original unreachable from the picker.
	if ff.Name == theme.DefaultName {
		t.Error("the custom copy took the built-in's name and shadowed it")
	}
	if a.themeName != theme.DefaultName+"-custom" {
		t.Errorf("themeName = %q, want the new copy to be active", a.themeName)
	}
	// It renders identically to what it was cloned from — a "customize"
	// that changes the colors before you've edited anything is a bug.
	if a.theme != theme.Default() {
		t.Error("the clone does not render identically to its source")
	}
	if len(a.tabs) != 1 || a.tabs[0].Path != want {
		t.Errorf("expected the theme file open in a tab, got %d tabs", len(a.tabs))
	}
}

// TestMenuThemeCustomize_OnAUserThemeJustOpensIt pins the no-rewrite
// rule: running Customize while already on a user theme must open the
// author's file, not expand their hand-kept sparse palette into
// thirty-five lines they never asked for.
func TestMenuThemeCustomize_OnAUserThemeJustOpensIt(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	path := writeUserTheme(t, "test-red.json", testThemeBody)
	a.loadThemes()
	a.applyThemeName("test-red", false)

	a.menuThemeCustomize()

	onDisk, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if string(onDisk) != testThemeBody {
		t.Errorf("the user's theme file was rewritten:\n%s", onDisk)
	}
	if a.themeName != "test-red" {
		t.Errorf("themeName = %q, want test-red — no copy should have been made", a.themeName)
	}
	if len(a.tabs) != 1 || a.tabs[0].Path != path {
		t.Errorf("expected the user's own theme file open in a tab, got %d tabs", len(a.tabs))
	}
}

// TestThemeAfterSave_RepaintsLive is the save-to-preview loop: edit a
// hex in the active theme's file, save, and the editor repaints. This is
// the feature that makes "no settings modal" a defensible position.
func TestThemeAfterSave_RepaintsLive(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	path := writeUserTheme(t, "test-red.json", testThemeBody)
	a.loadThemes()
	a.applyThemeName("test-red", false)

	// Edit the file the way the user would — through a tab — then save.
	a.openFile(path)
	if len(a.tabs) != 1 {
		t.Fatalf("expected the theme file to open, got %d tabs", len(a.tabs))
	}
	body := strings.Replace(testThemeBody, `"bg": "#ff0000"`, `"bg": "#0000ff"`, 1)
	buf := a.tabs[0].Buffer
	buf.DeleteRange(editor.Position{}, buf.EndPos())
	buf.InsertString(editor.Position{}, body)
	if !a.saveTabAt(0) {
		t.Fatalf("save failed: %s", a.statusMsg)
	}

	r, g, b := a.theme.BG.RGB()
	if r != 0 || g != 0 || b != 0xff {
		t.Errorf("BG = (%d,%d,%d), want the saved pure blue — saving a theme file must repaint", r, g, b)
	}
}

// TestThemeAfterSave_IgnoresOtherFiles pins the cheap-guard half: the
// hook runs on EVERY save, so a source file that merely happens to be
// JSON, or one that lives elsewhere, must not trigger a registry reload.
func TestThemeAfterSave_IgnoresOtherFiles(t *testing.T) {
	dir := t.TempDir()
	a := newTestApp(t, dir)
	a.applyThemeName("dark-game", false)
	before := a.theme

	other := filepath.Join(dir, "package.json")
	if err := os.WriteFile(other, []byte("{}\n"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	a.themeAfterSave(other)

	if a.theme != before || a.themeName != "dark-game" {
		t.Error("saving an unrelated .json file changed the theme")
	}
}

// TestMenuThemeReload_RereadsDirectory covers the escape hatch for
// changes ced didn't make: a theme file dropped in (or edited) by
// another program shows up without a restart.
func TestMenuThemeReload_RereadsDirectory(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	if _, ok := theme.Find(a.themeSpecs, "test-red"); ok {
		t.Fatal("precondition: test-red should not exist yet")
	}
	writeUserTheme(t, "test-red.json", testThemeBody)
	a.menuThemeReload()

	if _, ok := theme.Find(a.themeSpecs, "test-red"); !ok {
		t.Error("Reload themes did not pick up the new file")
	}
	if !strings.Contains(a.statusMsg, "Reloaded themes") {
		t.Errorf("status = %q, want a reload confirmation", a.statusMsg)
	}
}

// TestDraw_UnderEveryBuiltinTheme paints a full frame under each shipped
// theme. The palette is consumed by a dozen render paths that each pick
// their own fields, so this is the cheapest guard that a theme with an
// unusual palette (a light one, say) can't panic or blank a surface: the
// simulation screen must come back with the editor's background actually
// applied and real content on it.
func TestDraw_UnderEveryBuiltinTheme(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "main.go")
	if err := os.WriteFile(src, []byte("package main\n\nfunc main() {}\n"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	a := newTestApp(t, dir)
	a.openFile(src)
	scr := a.screen.(tcell.SimulationScreen)

	for _, spec := range theme.Builtins() {
		a.applyThemeName(spec.Name, false)
		a.draw()
		scr.Show() // SimulationScreen serves GetContents from the front buffer.
		cells, w, h := scr.GetContents()
		if w == 0 || h == 0 || len(cells) == 0 {
			t.Fatalf("theme %q: nothing was drawn", spec.Name)
		}
		// The editor body's background must be the theme's, not tcell's
		// default — a mid-screen cell is well inside the code pane.
		_, bg, _ := cells[(h/2)*w+w/2].Style.Decompose()
		if bg != a.theme.BG {
			t.Errorf("theme %q: editor cell background = %v, want %v", spec.Name, bg, a.theme.BG)
		}
	}
}

// TestThemeMenuLabel_NamesTheActiveTheme pins the ≡ row's dynamic label.
// It's how the menu answers "which theme am I on?" without being
// clicked, so it has to track the switch.
func TestThemeMenuLabel_NamesTheActiveTheme(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	if got := a.themeMenuLabel(); !strings.Contains(got, "Tokyo Night") {
		t.Errorf("label = %q, want it to name Tokyo Night", got)
	}
	a.applyThemeName("corporate", false)
	if got := a.themeMenuLabel(); !strings.Contains(got, "Corporate") {
		t.Errorf("label = %q, want it to name Corporate", got)
	}
}

// TestActiveThemeSpec_SurvivesADeletedThemeFile covers the mid-session
// rug-pull: the user deletes the theme file they're running on. The
// label and the customize flow both go through activeThemeSpec, so it
// has to hand back something usable rather than a zero Spec whose empty
// label would render as a blank menu row.
func TestActiveThemeSpec_SurvivesADeletedThemeFile(t *testing.T) {
	a := newTestApp(t, t.TempDir())
	path := writeUserTheme(t, "test-red.json", testThemeBody)
	a.loadThemes()
	a.applyThemeName("test-red", false)

	if err := os.Remove(path); err != nil {
		t.Fatalf("remove: %v", err)
	}
	a.loadThemes()

	spec := a.activeThemeSpec()
	if spec.Label == "" {
		t.Error("activeThemeSpec returned a blank spec — the menu row would render empty")
	}
	if spec.Name != theme.DefaultName {
		t.Errorf("Name = %q, want a fallback to the default", spec.Name)
	}
}
