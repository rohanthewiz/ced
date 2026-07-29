// =============================================================================
// File: internal/userconfig/userconfig_test.go
// Author: Rohan Allison <rohanthewiz@gmail.com>
// Created: 2026-04-30
// Copyright: 2026 Rohan Allison. All rights reserved.
// Portions copyright 2026 Cloudmanic, LLC. Original author: Spicer Matthews.
// =============================================================================

package userconfig

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestDefaults pins the documented default — icons mode "auto" — so a
// future refactor of the Defaults helper can't silently flip user-
// visible behaviour for everyone who has no config file.
func TestDefaults(t *testing.T) {
	got := Defaults()
	if got.Icons != IconsAuto {
		t.Fatalf("Defaults().Icons = %q, want %q", got.Icons, IconsAuto)
	}
}

// TestLoadEmptyPath verifies that calling Load with no path resolves
// to defaults rather than an error — the editor uses this when
// XDG_CONFIG_HOME is unset and the user has no home directory (CI,
// containers without HOME, etc.).
func TestLoadEmptyPath(t *testing.T) {
	cfg, err := Load("")
	if err != nil {
		t.Fatalf("Load(\"\"): unexpected error: %v", err)
	}
	if cfg.Icons != IconsAuto {
		t.Fatalf("Load(\"\").Icons = %q, want %q", cfg.Icons, IconsAuto)
	}
}

// TestLoadMissingFile verifies a non-existent config file is treated
// as "no preferences set" — the common case for fresh installs.
func TestLoadMissingFile(t *testing.T) {
	dir := t.TempDir()
	cfg, err := Load(filepath.Join(dir, "does-not-exist.json"))
	if err != nil {
		t.Fatalf("Load(missing): unexpected error: %v", err)
	}
	if cfg.Icons != IconsAuto {
		t.Fatalf("missing file should yield default IconsAuto, got %q", cfg.Icons)
	}
}

// TestLoadEmptyFile covers the "user touched the file but didn't
// write anything" edge case — should be indistinguishable from no
// file at all.
func TestLoadEmptyFile(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "config.json")
	if err := os.WriteFile(p, nil, 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	cfg, err := Load(p)
	if err != nil {
		t.Fatalf("Load(empty): %v", err)
	}
	if cfg.Icons != IconsAuto {
		t.Fatalf("empty file should yield default, got %q", cfg.Icons)
	}
}

// TestLoadHappyValues exercises every recognised icons mode exactly
// once so a typo in the switch arms shows up immediately.
func TestLoadHappyValues(t *testing.T) {
	cases := map[string]IconsMode{
		`{"icons":"auto"}`: IconsAuto,
		`{"icons":"on"}`:   IconsOn,
		`{"icons":"off"}`:  IconsOff,
		`{"icons":"AUTO"}`: IconsAuto, // case-insensitive
		`{"icons":" On "}`: IconsOn,   // whitespace-tolerant
		`{}`:               IconsAuto, // omitted field uses default
	}
	for body, want := range cases {
		t.Run(body, func(t *testing.T) {
			dir := t.TempDir()
			p := filepath.Join(dir, "config.json")
			if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
				t.Fatalf("seed: %v", err)
			}
			cfg, err := Load(p)
			if err != nil {
				t.Fatalf("Load(%s): %v", body, err)
			}
			if cfg.Icons != want {
				t.Fatalf("Load(%s).Icons = %q, want %q", body, cfg.Icons, want)
			}
		})
	}
}

// TestLoadUnknownValue verifies a typo in the icons field surfaces as
// a clear error rather than silently reverting to defaults — that's
// the bug we want users to notice and fix in their config file.
func TestLoadUnknownValue(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "config.json")
	if err := os.WriteFile(p, []byte(`{"icons":"yes-please"}`), 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	cfg, err := Load(p)
	if err == nil {
		t.Fatalf("expected error for unknown value, got nil")
	}
	if cfg.Icons != IconsAuto {
		t.Fatalf("on error should still return safe defaults, got %q", cfg.Icons)
	}
}

// TestLoadMalformedJSON verifies a syntactically broken config doesn't
// crash the editor — the user gets an error and the editor uses
// defaults until they fix the file.
func TestLoadMalformedJSON(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "config.json")
	if err := os.WriteFile(p, []byte(`{not json`), 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	if _, err := Load(p); err == nil {
		t.Fatalf("expected error for malformed JSON, got nil")
	}
}

// TestLoadForwardCompat verifies the loader ignores top-level fields
// it doesn't recognise — so a future config.json with new keys keeps
// working on older binaries instead of erroring out.
func TestLoadForwardCompat(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "config.json")
	body := `{"icons":"on","theme":"future-feature","unknown_block":{"a":1}}`
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	cfg, err := Load(p)
	if err != nil {
		t.Fatalf("forward-compat config should not error, got %v", err)
	}
	if cfg.Icons != IconsOn {
		t.Fatalf("recognised field still expected: got %q", cfg.Icons)
	}
}

// TestDefaultPathHonoursXDG verifies XDG_CONFIG_HOME wins over the
// fallback when set — important for nix-style setups that move every
// dotfile out of $HOME.
func TestDefaultPathHonoursXDG(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", "/tmp/xdg-test")
	got := DefaultPath()
	want := filepath.Join("/tmp/xdg-test", "ced", "config.json")
	if got != want {
		t.Fatalf("DefaultPath() = %q, want %q", got, want)
	}
}

// TestDefaultPathFallsBackToHome verifies the ~/.config fallback when
// XDG_CONFIG_HOME isn't set — the common path on macOS/Linux without
// XDG configured.
func TestDefaultPathFallsBackToHome(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", "")
	t.Setenv("HOME", "/tmp/home-test")
	got := DefaultPath()
	want := filepath.Join("/tmp/home-test", ".config", "ced", "config.json")
	if got != want {
		t.Fatalf("DefaultPath() = %q, want %q", got, want)
	}
}

// TestRcPathHonoursXDG mirrors the DefaultPath XDG test for the grsh rc
// file: the two must resolve to the same directory (rc.grsh sits next to
// config.json), so config and shell customizations never split up.
func TestRcPathHonoursXDG(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", "/tmp/xdg-test")
	got := RcPath()
	want := filepath.Join("/tmp/xdg-test", "ced", "rc.grsh")
	if got != want {
		t.Fatalf("RcPath() = %q, want %q", got, want)
	}
	// Same directory as the config file — the shared-helper guarantee.
	if filepath.Dir(got) != filepath.Dir(DefaultPath()) {
		t.Fatalf("RcPath dir %q != DefaultPath dir %q", filepath.Dir(got), filepath.Dir(DefaultPath()))
	}
}

// TestRcPathFallsBackToHome verifies the ~/.config/ced/rc.grsh fallback
// when XDG_CONFIG_HOME isn't set.
func TestRcPathFallsBackToHome(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", "")
	t.Setenv("HOME", "/tmp/home-test")
	got := RcPath()
	want := filepath.Join("/tmp/home-test", ".config", "ced", "rc.grsh")
	if got != want {
		t.Fatalf("RcPath() = %q, want %q", got, want)
	}
}

// TestDefaultsAutoSaveOn pins the documented auto-save default: on.
// Flipping this silently would change save semantics for every user
// with no config file, so it gets its own guard.
func TestDefaultsAutoSaveOn(t *testing.T) {
	if !Defaults().AutoSave {
		t.Fatal("Defaults().AutoSave = false, want true")
	}
}

// TestLoadAutoSaveValues exercises the recognised autosave values and
// the absent-field default, mirroring the icons table test.
func TestLoadAutoSaveValues(t *testing.T) {
	cases := map[string]bool{
		`{"autosave":"on"}`:    true,
		`{"autosave":"off"}`:   false,
		`{"autosave":" OFF "}`: false, // case/whitespace tolerant, like icons
		`{}`:                   true,  // omitted field keeps the default
	}
	dir := t.TempDir()
	for body, want := range cases {
		p := filepath.Join(dir, "config.json")
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatalf("seed: %v", err)
		}
		cfg, err := Load(p)
		if err != nil {
			t.Fatalf("Load(%s): %v", body, err)
		}
		if cfg.AutoSave != want {
			t.Errorf("Load(%s).AutoSave = %v, want %v", body, cfg.AutoSave, want)
		}
	}
}

// TestLoadAutoSaveInvalid mirrors the icons rule: a typo'd value is
// an error the caller can flash, not a silent fallback that hides the
// user's mistake.
func TestLoadAutoSaveInvalid(t *testing.T) {
	p := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(p, []byte(`{"autosave":"maybe"}`), 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	if _, err := Load(p); err == nil {
		t.Fatal("invalid autosave value should error")
	}
}

// TestSaveAutoSave_CreatesFile covers the fresh-install path: no
// config file (or even config dir) exists yet, and persisting the
// toggle must create both.
func TestSaveAutoSave_CreatesFile(t *testing.T) {
	p := filepath.Join(t.TempDir(), "nested", "config.json")
	if err := SaveAutoSave(p, false); err != nil {
		t.Fatalf("SaveAutoSave: %v", err)
	}
	cfg, err := Load(p)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if cfg.AutoSave {
		t.Fatal("persisted off, loaded on")
	}
}

// TestSaveAutoSave_PreservesUnknownKeys is the forward-compat
// contract: the read-modify-write must round-trip keys this version
// of the binary doesn't know about, so toggling auto-save from an
// old ced can't strip settings written by a newer one.
func TestSaveAutoSave_PreservesUnknownKeys(t *testing.T) {
	p := filepath.Join(t.TempDir(), "config.json")
	seed := `{"icons":"on","future_setting":{"nested":true}}`
	if err := os.WriteFile(p, []byte(seed), 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	if err := SaveAutoSave(p, true); err != nil {
		t.Fatalf("SaveAutoSave: %v", err)
	}
	data, _ := os.ReadFile(p)
	for _, want := range []string{`"icons"`, `"future_setting"`, `"nested"`, `"autosave"`} {
		if !strings.Contains(string(data), want) {
			t.Errorf("rewritten config lost %s: %s", want, data)
		}
	}
}

// TestSaveAutoSave_RefusesMalformedConfig pins the do-no-harm rule: a
// config the user hand-broke must be left alone, not replaced with a
// minimal file that eats their (fixable) settings.
func TestSaveAutoSave_RefusesMalformedConfig(t *testing.T) {
	p := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(p, []byte(`{not json`), 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	if err := SaveAutoSave(p, true); err == nil {
		t.Fatal("malformed config should refuse the write")
	}
	data, _ := os.ReadFile(p)
	if string(data) != `{not json` {
		t.Fatalf("malformed config was modified: %q", data)
	}
}

// TestDefaultsTermDockBottom pins the default layout: the terminal is
// a bottom strip unless the user opted into the left dock.
func TestDefaultsTermDockBottom(t *testing.T) {
	if Defaults().TermDock != TermDockBottom {
		t.Fatalf("default termdock = %q, want %q", Defaults().TermDock, TermDockBottom)
	}
}

// TestLoadTermDockValues covers both accepted values plus the
// keep-the-default omission case.
func TestLoadTermDockValues(t *testing.T) {
	cases := []struct {
		json string
		want TermDock
	}{
		{`{"termdock": "left"}`, TermDockLeft},
		{`{"termdock": "bottom"}`, TermDockBottom},
		{`{"icons": "off"}`, TermDockBottom}, // omitted → default
	}
	for _, tc := range cases {
		path := filepath.Join(t.TempDir(), "config.json")
		if err := os.WriteFile(path, []byte(tc.json), 0644); err != nil {
			t.Fatalf("seed: %v", err)
		}
		cfg, err := Load(path)
		if err != nil {
			t.Fatalf("Load(%s): %v", tc.json, err)
		}
		if cfg.TermDock != tc.want {
			t.Errorf("Load(%s).TermDock = %q, want %q", tc.json, cfg.TermDock, tc.want)
		}
	}
}

// TestLoadTermDockInvalid surfaces a typo as an error rather than
// silently snapping to the default — same contract as icons/autosave.
func TestLoadTermDockInvalid(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(path, []byte(`{"termdock": "sideways"}`), 0644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	if _, err := Load(path); err == nil {
		t.Fatal("expected an error for an unknown termdock value")
	}
}

// TestSaveTermDock_RoundTripsAndPreserves saves the preference into a
// config that already has hand-set keys and verifies both survive —
// the same unknown-key guarantee SaveAutoSave makes.
func TestSaveTermDock_RoundTripsAndPreserves(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	seed := "{\n  \"icons\": \"on\",\n  \"future-key\": 42\n}\n"
	if err := os.WriteFile(path, []byte(seed), 0644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	if err := SaveTermDock(path, TermDockLeft); err != nil {
		t.Fatalf("SaveTermDock: %v", err)
	}
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load after save: %v", err)
	}
	if cfg.TermDock != TermDockLeft || cfg.Icons != IconsOn {
		t.Fatalf("round trip lost values: termdock=%q icons=%q", cfg.TermDock, cfg.Icons)
	}
	data, _ := os.ReadFile(path)
	if !strings.Contains(string(data), "future-key") {
		t.Fatal("unknown key was dropped by the save round-trip")
	}
}

// TestDefaultsExecMarksOn pins the documented default: the executable
// '*' marker is shown unless the user opts out. Flipping this silently
// would change the file tree's look for everyone with no config file.
func TestDefaultsExecMarksOn(t *testing.T) {
	if !Defaults().ExecMarks {
		t.Fatal("Defaults().ExecMarks = false, want true")
	}
}

// TestLoadExecMarksValues exercises the recognised execmarks values and
// the absent-field default, mirroring the icons/autosave tables.
func TestLoadExecMarksValues(t *testing.T) {
	cases := map[string]bool{
		`{"execmarks":"on"}`:    true,
		`{"execmarks":"off"}`:   false,
		`{"execmarks":" OFF "}`: false, // case/whitespace tolerant
		`{}`:                    true,  // omitted field keeps the default
	}
	for body, want := range cases {
		p := filepath.Join(t.TempDir(), "config.json")
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatalf("seed: %v", err)
		}
		cfg, err := Load(p)
		if err != nil {
			t.Fatalf("Load(%s): %v", body, err)
		}
		if cfg.ExecMarks != want {
			t.Errorf("Load(%s).ExecMarks = %v, want %v", body, cfg.ExecMarks, want)
		}
	}
}

// TestLoadExecMarksInvalid mirrors the icons/autosave rule: a typo'd
// value is an error the caller can flash, not a silent fallback.
func TestLoadExecMarksInvalid(t *testing.T) {
	p := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(p, []byte(`{"execmarks":"sometimes"}`), 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	if _, err := Load(p); err == nil {
		t.Fatal("invalid execmarks value should error")
	}
}

// TestSaveExecMarks_RoundTripsAndPreserves saves the preference into a
// config that already has hand-set keys and verifies both survive — the
// same unknown-key guarantee SaveAutoSave makes.
func TestSaveExecMarks_RoundTripsAndPreserves(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	seed := "{\n  \"icons\": \"on\",\n  \"future-key\": 42\n}\n"
	if err := os.WriteFile(path, []byte(seed), 0644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	if err := SaveExecMarks(path, false); err != nil {
		t.Fatalf("SaveExecMarks: %v", err)
	}
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load after save: %v", err)
	}
	if cfg.ExecMarks || cfg.Icons != IconsOn {
		t.Fatalf("round trip lost values: execmarks=%v icons=%q", cfg.ExecMarks, cfg.Icons)
	}
	data, _ := os.ReadFile(path)
	if !strings.Contains(string(data), "future-key") {
		t.Fatal("unknown key was dropped by the save round-trip")
	}
}

// TestDefaultsCopilotOn pins the Copilot default: on, because the
// sidecar only ever spawns when the user installed its binary — PATH
// presence is the real opt-in, this key is just the opt-out.
func TestDefaultsCopilotOn(t *testing.T) {
	if !Defaults().Copilot {
		t.Fatal("Defaults().Copilot = false, want true")
	}
}

// TestLoadCopilotValues exercises the recognised copilot values and the
// absent-field default, mirroring the icons/autosave/execmarks tables.
func TestLoadCopilotValues(t *testing.T) {
	cases := map[string]bool{
		`{"copilot":"on"}`:    true,
		`{"copilot":"off"}`:   false,
		`{"copilot":" OFF "}`: false, // case/whitespace tolerant
		`{}`:                  true,  // omitted field keeps the default
	}
	for body, want := range cases {
		p := filepath.Join(t.TempDir(), "config.json")
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatalf("seed: %v", err)
		}
		cfg, err := Load(p)
		if err != nil {
			t.Fatalf("Load(%s): %v", body, err)
		}
		if cfg.Copilot != want {
			t.Errorf("Load(%s).Copilot = %v, want %v", body, cfg.Copilot, want)
		}
	}
}

// TestLoadCopilotInvalid mirrors the house rule for every key: a typo'd
// value is an error the caller can flash, not a silent fallback.
func TestLoadCopilotInvalid(t *testing.T) {
	p := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(p, []byte(`{"copilot":"maybe"}`), 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	if _, err := Load(p); err == nil {
		t.Fatal("invalid copilot value should error")
	}
}

// TestSaveCopilot_RoundTripsAndPreserves saves the preference into a
// config that already has hand-set keys and verifies both survive — the
// same unknown-key guarantee every SaveX makes.
func TestSaveCopilot_RoundTripsAndPreserves(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	seed := "{\n  \"icons\": \"on\",\n  \"future-key\": 42\n}\n"
	if err := os.WriteFile(path, []byte(seed), 0644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	if err := SaveCopilot(path, false); err != nil {
		t.Fatalf("SaveCopilot: %v", err)
	}
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load after save: %v", err)
	}
	if cfg.Copilot || cfg.Icons != IconsOn {
		t.Fatalf("round trip lost values: copilot=%v icons=%q", cfg.Copilot, cfg.Icons)
	}
	data, _ := os.ReadFile(path)
	if !strings.Contains(string(data), "future-key") {
		t.Fatal("unknown key was dropped by the save round-trip")
	}
}

// TestDefaultsSuggestionsOn pins the ghost-text default: on. The key
// exists so a user can keep the sidecar for sign-in/chat while opting
// out of just the inline completions.
func TestDefaultsSuggestionsOn(t *testing.T) {
	if !Defaults().Suggestions {
		t.Fatal("Defaults().Suggestions = false, want true")
	}
}

// TestLoadSuggestionsValues exercises the recognised suggestions values
// and the absent-field default, mirroring the copilot table.
func TestLoadSuggestionsValues(t *testing.T) {
	cases := map[string]bool{
		`{"suggestions":"on"}`:    true,
		`{"suggestions":"off"}`:   false,
		`{"suggestions":" OFF "}`: false, // case/whitespace tolerant
		`{}`:                      true,  // omitted field keeps the default
	}
	for body, want := range cases {
		p := filepath.Join(t.TempDir(), "config.json")
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatalf("seed: %v", err)
		}
		cfg, err := Load(p)
		if err != nil {
			t.Fatalf("Load(%s): %v", body, err)
		}
		if cfg.Suggestions != want {
			t.Errorf("Load(%s).Suggestions = %v, want %v", body, cfg.Suggestions, want)
		}
	}
}

// TestLoadSuggestionsInvalid mirrors the house rule for every key: a
// typo'd value is an error the caller can flash, not a silent fallback.
func TestLoadSuggestionsInvalid(t *testing.T) {
	p := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(p, []byte(`{"suggestions":"sometimes"}`), 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	if _, err := Load(p); err == nil {
		t.Fatal("invalid suggestions value should error")
	}
}

// TestSaveSuggestions_RoundTripsAndPreserves saves the preference into
// a config that already has hand-set keys and verifies both survive —
// the same unknown-key guarantee every SaveX makes.
func TestSaveSuggestions_RoundTripsAndPreserves(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	seed := "{\n  \"icons\": \"on\",\n  \"future-key\": 42\n}\n"
	if err := os.WriteFile(path, []byte(seed), 0644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	if err := SaveSuggestions(path, false); err != nil {
		t.Fatalf("SaveSuggestions: %v", err)
	}
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load after save: %v", err)
	}
	if cfg.Suggestions || cfg.Icons != IconsOn {
		t.Fatalf("round trip lost values: suggestions=%v icons=%q", cfg.Suggestions, cfg.Icons)
	}
	data, _ := os.ReadFile(path)
	if !strings.Contains(string(data), "future-key") {
		t.Fatal("unknown key was dropped by the save round-trip")
	}
}

// TestChatContext_Defaults pins the auto-attach default: on. A chat
// panel inside an editor exists to answer questions about the file
// you're looking at, so it has to see it without being asked.
func TestChatContext_Defaults(t *testing.T) {
	if !Defaults().ChatContext {
		t.Fatal("Defaults().ChatContext = false, want true")
	}
}

// TestLoadChatContextValues exercises the recognised chatcontext values
// and the absent-field default, mirroring the suggestions table.
func TestLoadChatContextValues(t *testing.T) {
	cases := map[string]bool{
		`{"chatcontext":"on"}`:    true,
		`{"chatcontext":"off"}`:   false,
		`{"chatcontext":" OFF "}`: false, // case/whitespace tolerant
		`{}`:                      true,  // omitted field keeps the default
	}
	for body, want := range cases {
		p := filepath.Join(t.TempDir(), "config.json")
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatalf("seed: %v", err)
		}
		cfg, err := Load(p)
		if err != nil {
			t.Fatalf("Load(%s): %v", body, err)
		}
		if cfg.ChatContext != want {
			t.Errorf("Load(%s).ChatContext = %v, want %v", body, cfg.ChatContext, want)
		}
	}
}

// TestLoadChatContextInvalid mirrors the house rule for every key: a
// typo'd value is an error the caller can flash, not a silent fallback.
func TestLoadChatContextInvalid(t *testing.T) {
	p := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(p, []byte(`{"chatcontext":"maybe"}`), 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	if _, err := Load(p); err == nil {
		t.Fatal("invalid chatcontext value should error")
	}
}

// TestSaveChatContext_RoundTripsAndPreserves saves the preference into
// a config that already has hand-set keys and verifies both survive —
// the same unknown-key guarantee every SaveX makes.
func TestSaveChatContext_RoundTripsAndPreserves(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	seed := "{\n  \"icons\": \"on\",\n  \"future-key\": 42\n}\n"
	if err := os.WriteFile(path, []byte(seed), 0644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	if err := SaveChatContext(path, false); err != nil {
		t.Fatalf("SaveChatContext: %v", err)
	}
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load after save: %v", err)
	}
	if cfg.ChatContext || cfg.Icons != IconsOn {
		t.Fatalf("round trip lost values: chatcontext=%v icons=%q", cfg.ChatContext, cfg.Icons)
	}
	data, _ := os.ReadFile(path)
	if !strings.Contains(string(data), "future-key") {
		t.Fatal("unknown key was dropped by the save round-trip")
	}
}

// TestChatModel_LoadAndSave pins the chatmodel key: absent means ""
// (agent default), any non-blank id loads trimmed and un-validated
// (the roster is server-defined), and SaveChatModel round-trips
// alongside hand-set keys like every other SaveX.
func TestChatModel_LoadAndSave(t *testing.T) {
	if got := Defaults().ChatModel; got != "" {
		t.Fatalf("default ChatModel = %q, want empty", got)
	}
	path := filepath.Join(t.TempDir(), "config.json")
	seed := "{\n  \"chatmodel\": \"  claude-sonnet-4.6  \",\n  \"future-key\": 42\n}\n"
	if err := os.WriteFile(path, []byte(seed), 0644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.ChatModel != "claude-sonnet-4.6" {
		t.Fatalf("ChatModel = %q, want trimmed id", cfg.ChatModel)
	}
	if err := SaveChatModel(path, "gpt-5.5"); err != nil {
		t.Fatalf("SaveChatModel: %v", err)
	}
	cfg, err = Load(path)
	if err != nil {
		t.Fatalf("Load after save: %v", err)
	}
	if cfg.ChatModel != "gpt-5.5" {
		t.Fatalf("round trip: ChatModel = %q", cfg.ChatModel)
	}
	data, _ := os.ReadFile(path)
	if !strings.Contains(string(data), "future-key") {
		t.Fatal("unknown key was dropped by the save round-trip")
	}
}

// TestChatAgent_LoadAndSave pins the chatagent key: absent means ""
// (default backend), any non-blank id loads trimmed and lowercased but
// un-validated (the registry is app-layer knowledge, and an id from a
// newer or older ced must not break config loading), and SaveChatAgent
// round-trips alongside hand-set keys like every other SaveX.
func TestChatAgent_LoadAndSave(t *testing.T) {
	if got := Defaults().ChatAgent; got != "" {
		t.Fatalf("default ChatAgent = %q, want empty", got)
	}
	path := filepath.Join(t.TempDir(), "config.json")
	seed := "{\n  \"chatagent\": \"  Claude  \",\n  \"future-key\": 42\n}\n"
	if err := os.WriteFile(path, []byte(seed), 0644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.ChatAgent != "claude" {
		t.Fatalf("ChatAgent = %q, want trimmed lowercased id", cfg.ChatAgent)
	}
	if err := SaveChatAgent(path, "copilot"); err != nil {
		t.Fatalf("SaveChatAgent: %v", err)
	}
	cfg, err = Load(path)
	if err != nil {
		t.Fatalf("Load after save: %v", err)
	}
	if cfg.ChatAgent != "copilot" {
		t.Fatalf("round trip: ChatAgent = %q", cfg.ChatAgent)
	}
	data, _ := os.ReadFile(path)
	if !strings.Contains(string(data), "future-key") {
		t.Fatal("unknown key was dropped by the save round-trip")
	}
}

// TestChatWrite_LoadAndSave pins the chatwrite key end to end: it
// defaults ON (the shipped posture — every change still asks permission
// first), the recognised values load case-insensitively, an omitted
// field keeps the default, a typo errors rather than silently choosing a
// trust level for the user, and SaveChatWrite round-trips beside
// hand-set keys.
func TestChatWrite_LoadAndSave(t *testing.T) {
	if !Defaults().ChatWrite {
		t.Fatal("Defaults().ChatWrite = false, want true")
	}
	cases := map[string]bool{
		`{"chatwrite":"on"}`:    true,
		`{"chatwrite":"off"}`:   false,
		`{"chatwrite":" OFF "}`: false,
		`{}`:                    true,
	}
	for body, want := range cases {
		p := filepath.Join(t.TempDir(), "config.json")
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatalf("seed: %v", err)
		}
		cfg, err := Load(p)
		if err != nil {
			t.Fatalf("Load(%s): %v", body, err)
		}
		if cfg.ChatWrite != want {
			t.Errorf("Load(%s).ChatWrite = %v, want %v", body, cfg.ChatWrite, want)
		}
	}

	p := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(p, []byte(`{"chatwrite":"sometimes"}`), 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	if _, err := Load(p); err == nil {
		t.Fatal("invalid chatwrite value should error")
	}

	path := filepath.Join(t.TempDir(), "config.json")
	seed := "{\n  \"icons\": \"on\",\n  \"future-key\": 42\n}\n"
	if err := os.WriteFile(path, []byte(seed), 0644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	if err := SaveChatWrite(path, false); err != nil {
		t.Fatalf("SaveChatWrite: %v", err)
	}
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load after save: %v", err)
	}
	if cfg.ChatWrite || cfg.Icons != IconsOn {
		t.Fatalf("round trip lost values: chatwrite=%v icons=%q", cfg.ChatWrite, cfg.Icons)
	}
	data, _ := os.ReadFile(path)
	if !strings.Contains(string(data), "future-key") {
		t.Fatal("unknown key was dropped by the save round-trip")
	}
}
