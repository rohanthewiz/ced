// =============================================================================
// File: internal/theme/load_test.go
// Author: Rohan Allison <rohanthewiz@gmail.com>
// Created: 2026-07-31
// Copyright: 2026 Rohan Allison. All rights reserved.
// =============================================================================

// Tests for the theme registry: reading user files, shadowing built-ins
// in place, surviving broken files, and the write/read round-trip that
// the editor's "Customize theme…" flow depends on.

package theme

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeTheme drops a theme file into dir and returns its path.
func writeTheme(t *testing.T, dir, name, body string) string {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
	return path
}

// minimalFile is the smallest theme file that loads: a colors block with
// the eight core keys and nothing else. It doubles as the documentation
// example — if this stops working, the format's whole promise is broken.
const minimalFile = `{
  "colors": {
    "bg": "#101018", "fg": "#e0e0f0", "muted": "#707088", "line": "#303040",
    "accent": "#78a8f0", "ok": "#88cc70", "warn": "#e0b060", "err": "#f07080"
  }
}`

// TestLoadFile_MinimalTakesNameFromFilename pins the "name the theme by
// naming the file" shortcut: a colors block alone is a complete theme,
// and its id and label come from the filename.
func TestLoadFile_MinimalTakesNameFromFilename(t *testing.T) {
	path := writeTheme(t, t.TempDir(), "Midnight.json", minimalFile)
	spec, err := LoadFile(path)
	if err != nil {
		t.Fatalf("LoadFile: %v", err)
	}
	if spec.Name != "midnight" {
		t.Errorf("Name = %q, want %q (lowercased filename stem)", spec.Name, "midnight")
	}
	if spec.Label != "midnight" {
		t.Errorf("Label = %q, want the name as a fallback", spec.Label)
	}
	if spec.Source != SourceUser || spec.Path != path {
		t.Errorf("Source/Path = %q/%q, want user/%q", spec.Source, spec.Path, path)
	}
	// dark: absent → inferred from the background.
	if !spec.Dark {
		t.Error("a #101018 background should be inferred as dark")
	}
	// The spec must still hold the SPARSE palette — see Encode/Save.
	if len(spec.Colors) != len(CoreKeys()) {
		t.Errorf("Colors has %d keys, want the %d that were authored", len(spec.Colors), len(CoreKeys()))
	}
	if _, err := spec.Resolve(); err != nil {
		t.Errorf("Resolve: %v", err)
	}
}

// TestLoadFile_ExplicitMetadataWins covers the other direction: a file
// that states name/label/dark keeps them, including a "dark": false that
// contradicts the luminance guess (a deliberately washed-out dark theme
// is the author's call, not the detector's).
func TestLoadFile_ExplicitMetadataWins(t *testing.T) {
	body := `{"name":"Custom-ID","label":"My Theme","dark":false,"colors":` +
		strings.SplitN(minimalFile, `"colors":`, 2)[1]
	path := writeTheme(t, t.TempDir(), "ignored-name.json", body)
	spec, err := LoadFile(path)
	if err != nil {
		t.Fatalf("LoadFile: %v", err)
	}
	if spec.Name != "custom-id" {
		t.Errorf("Name = %q, want the file's own (lowercased) name", spec.Name)
	}
	if spec.Label != "My Theme" {
		t.Errorf("Label = %q, want %q", spec.Label, "My Theme")
	}
	if spec.Dark {
		t.Error("an explicit \"dark\": false must beat the luminance guess")
	}
}

// TestLoadFile_Rejects covers the failure modes a hand-edited file hits:
// unparseable JSON, no colors block, and a palette that can't normalize.
// Each must return an error naming the file — the editor flashes it, and
// "theme: bad" with no filename is useless when you have six themes.
func TestLoadFile_Rejects(t *testing.T) {
	dir := t.TempDir()
	cases := []struct{ name, body string }{
		{"broken.json", `{"colors": {`},
		{"empty.json", `{"label": "no colors here"}`},
		{"incomplete.json", `{"colors": {"bg": "#000000"}}`},
	}
	for _, c := range cases {
		path := writeTheme(t, dir, c.name, c.body)
		_, err := LoadFile(path)
		if err == nil {
			t.Errorf("LoadFile(%s) should have failed", c.name)
			continue
		}
		if !strings.Contains(err.Error(), c.name) {
			t.Errorf("error %q does not name the file", err)
		}
	}
}

// TestRegistry_UserThemeShadowsBuiltinInPlace is the house rule that
// keeps the picker honest: overriding a built-in replaces it at its
// original position rather than appending a second row with the same
// name. Two identically-named rows in a chooser is a bug report.
func TestRegistry_UserThemeShadowsBuiltinInPlace(t *testing.T) {
	dir := t.TempDir()
	writeTheme(t, dir, DefaultName+".json", minimalFile)

	specs, errs := Registry(dir)
	if len(errs) != 0 {
		t.Fatalf("unexpected errors: %v", errs)
	}
	if len(specs) != len(Builtins()) {
		t.Errorf("registry has %d themes, want %d — the override should replace, not append",
			len(specs), len(Builtins()))
	}
	if specs[0].Name != DefaultName {
		t.Fatalf("specs[0] = %q, want the shadowed built-in still first", specs[0].Name)
	}
	if specs[0].Source != SourceUser {
		t.Errorf("specs[0].Source = %q, want the user file to have won", specs[0].Source)
	}
	// And the palette really is the user's, not the built-in's.
	th, err := specs[0].Resolve()
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if th.BG == Default().BG {
		t.Error("the shadowed theme still resolves to the built-in background")
	}
}

// TestRegistry_NewThemesAppendAndBrokenOnesWarn pins the two remaining
// registry behaviors: an unrecognised name is appended after the
// built-ins, and a broken file is reported WITHOUT taking its neighbours
// down with it. Losing nine good themes to one stray comma would be a
// terrible trade.
func TestRegistry_NewThemesAppendAndBrokenOnesWarn(t *testing.T) {
	dir := t.TempDir()
	writeTheme(t, dir, "midnight.json", minimalFile)
	writeTheme(t, dir, "wrecked.json", `{"colors": {`)
	// Non-JSON files in the directory are ignored, not errors — users
	// keep notes and backups next to their configs.
	writeTheme(t, dir, "notes.txt", "just some notes")

	specs, errs := Registry(dir)
	if len(errs) != 1 {
		t.Fatalf("errs = %v, want exactly the one broken file", errs)
	}
	if !strings.Contains(errs[0].Error(), "wrecked.json") {
		t.Errorf("error %q does not name the broken file", errs[0])
	}
	if len(specs) != len(Builtins())+1 {
		t.Errorf("registry has %d themes, want built-ins + midnight", len(specs))
	}
	if _, ok := Find(specs, "midnight"); !ok {
		t.Error("the good user theme did not survive its broken neighbour")
	}
	if _, ok := Find(specs, DefaultName); !ok {
		t.Error("the built-ins did not survive a broken user theme")
	}
}

// TestRegistry_MissingDirectoryIsSilent — having no themes directory is
// the overwhelmingly common case, so it must not produce a warning the
// user sees on every start.
func TestRegistry_MissingDirectoryIsSilent(t *testing.T) {
	specs, errs := Registry(filepath.Join(t.TempDir(), "nope"))
	if len(errs) != 0 {
		t.Errorf("errs = %v, want none for a missing directory", errs)
	}
	if len(specs) != len(Builtins()) {
		t.Errorf("got %d themes, want the built-ins", len(specs))
	}
	// And no config location at all behaves the same way.
	if specs, errs := Registry(""); len(errs) != 0 || len(specs) != len(Builtins()) {
		t.Errorf("Registry(\"\") = %d themes, %v; want built-ins and no errors", len(specs), errs)
	}
}

// TestResolve_FallsBackToDefault pins the safety net: neither an unknown
// name nor an unresolvable theme may leave the editor without a palette.
// The caller gets the default plus a reason to flash.
func TestResolve_FallsBackToDefault(t *testing.T) {
	specs := Builtins()

	th, spec, err := Resolve(specs, "no-such-theme")
	if err == nil {
		t.Error("resolving an unknown theme should report why")
	}
	if th != Default() || spec.Name != DefaultName {
		t.Error("an unknown theme must fall back to the shipped default")
	}

	// A registry entry that won't normalize takes the same path.
	specs = append(specs, Spec{Name: "broken", Label: "Broken", Colors: Palette{"bg": "#000000"}})
	th, spec, err = Resolve(specs, "broken")
	if err == nil {
		t.Error("resolving a broken theme should report why")
	}
	if th != Default() || spec.Name != DefaultName {
		t.Error("a broken theme must fall back to the shipped default")
	}
}

// TestEncode_RoundTrips checks that what Save writes, LoadFile reads
// back identically. This is the contract behind "Customize theme…":
// the editor writes a file, the user edits a hex, and the save hook
// re-reads it — a lossy encoder would silently drop their edits' context.
func TestEncode_RoundTrips(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "themes")
	src, _ := Find(Builtins(), "darcula")
	src.Name, src.Label, src.Source = "darcula-custom", "Darcula (custom)", SourceUser

	path, err := Save(dir, src, true)
	if err != nil {
		t.Fatalf("Save: %v", err)
	}
	if filepath.Base(path) != "darcula-custom.json" {
		t.Errorf("wrote %s, want darcula-custom.json", filepath.Base(path))
	}

	back, err := LoadFile(path)
	if err != nil {
		t.Fatalf("LoadFile: %v", err)
	}
	if back.Name != src.Name || back.Label != src.Label || back.Dark != src.Dark {
		t.Errorf("metadata drifted: %+v vs %+v", back, src)
	}
	// full=true expands every derived key — that's the point of the
	// customize flow: show the author the whole board.
	if len(back.Colors) != len(Keys()) {
		t.Errorf("expanded file has %d colors, want all %d", len(back.Colors), len(Keys()))
	}
	// And it still renders as the theme it was cloned from.
	wantTheme, _ := src.Resolve()
	gotTheme, err := back.Resolve()
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if gotTheme != wantTheme {
		t.Error("round-tripped theme does not render identically to its source")
	}
}

// TestEncode_SparseStaysSparse pins the other half of Encode's contract:
// full=false writes back exactly what the author stated, so re-saving an
// eight-line theme doesn't balloon it to thirty-five.
func TestEncode_SparseStaysSparse(t *testing.T) {
	spec := Spec{Name: "tiny", Label: "Tiny", Dark: true, Colors: minimal()}
	out, err := Encode(spec, false)
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}
	var ff FileFormat
	if err := json.Unmarshal(out, &ff); err != nil {
		t.Fatalf("Encode produced invalid JSON: %v\n%s", err, out)
	}
	if len(ff.Colors) != len(CoreKeys()) {
		t.Errorf("sparse encode wrote %d colors, want %d", len(ff.Colors), len(CoreKeys()))
	}
}

// TestEncode_CanonicalKeyOrder pins that generated files read core-first
// rather than in Go's map order, which is what makes a written theme
// file navigable by a human: the eight keys they'll actually edit are at
// the top, above the twenty-seven they mostly won't.
func TestEncode_CanonicalKeyOrder(t *testing.T) {
	spec, _ := Find(Builtins(), DefaultName)
	out, err := Encode(spec, true)
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}
	text := string(out)
	prev := -1
	for _, k := range Keys() {
		i := strings.Index(text, `"`+k+`"`)
		if i < 0 {
			t.Fatalf("key %q missing from encoded output", k)
		}
		if i < prev {
			t.Errorf("key %q appears out of canonical order", k)
		}
		prev = i
	}
}

// TestSave_NoConfigDirectory covers the "" path — no resolvable config
// location. It must be a readable error, not a file written to the
// process's working directory.
func TestSave_NoConfigDirectory(t *testing.T) {
	spec, _ := Find(Builtins(), DefaultName)
	if _, err := Save("", spec, true); err == nil {
		t.Fatal("Save(\"\") should have failed")
	}
}
