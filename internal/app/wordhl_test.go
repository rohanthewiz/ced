// =============================================================================
// File: internal/app/wordhl_test.go
// Author: Rohan Allison <rohanthewiz@gmail.com>
// Created: 2026-07-31
// Copyright: 2026 Rohan Allison. All rights reserved.
// =============================================================================

package app

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/rohanthewiz/ced/internal/userconfig"
)

// TestMenuToggleWordHighlight_PersistsAndPreservesConfig drives the ≡
// row end to end: every open tab flips, the choice lands in config.json,
// and a hand-set key the toggle doesn't own survives the rewrite.
func TestMenuToggleWordHighlight_PersistsAndPreservesConfig(t *testing.T) {
	cfgDir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", cfgDir)
	cfgPath := filepath.Join(cfgDir, "ced", "config.json")
	if err := os.MkdirAll(filepath.Dir(cfgPath), 0755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(cfgPath, []byte(`{"icons":"off"}`+"\n"), 0644); err != nil {
		t.Fatalf("seed config: %v", err)
	}
	root := t.TempDir()
	path := filepath.Join(root, "a.go")
	if err := os.WriteFile(path, []byte("x := x\n"), 0644); err != nil {
		t.Fatalf("seed file: %v", err)
	}
	a := newTestApp(t, root)
	a.wordHLEnabled = true
	a.openFile(path)
	tab := a.activeTabPtr()
	if tab == nil || !tab.WordHighlight {
		t.Fatal("a tab opened with the preference on should carry the flag")
	}

	a.menuToggleWordHighlight()

	if tab.WordHighlight {
		t.Error("the toggle should clear the flag on the open tab")
	}
	cfg, err := userconfig.Load(cfgPath)
	if err != nil {
		t.Fatalf("reload config: %v", err)
	}
	if cfg.WordHL {
		t.Error("persisted config should say wordhl off")
	}
	if cfg.Icons != userconfig.IconsOff {
		t.Fatalf("icons setting lost in rewrite: got %q", cfg.Icons)
	}
}

// TestMenuToggleWordHighlight_RoundTrips confirms the flag isn't a
// one-way latch and that a second flip reaches the open tabs again.
func TestMenuToggleWordHighlight_RoundTrips(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	root := t.TempDir()
	path := filepath.Join(root, "a.go")
	if err := os.WriteFile(path, []byte("y := y\n"), 0644); err != nil {
		t.Fatalf("seed file: %v", err)
	}
	a := newTestApp(t, root)
	a.openFile(path)
	tab := a.activeTabPtr()

	a.menuToggleWordHighlight() // off → on
	if !tab.WordHighlight {
		t.Fatal("first toggle should switch the highlight on")
	}
	a.menuToggleWordHighlight() // on → off
	if tab.WordHighlight {
		t.Fatal("second toggle should switch it back off")
	}
}

// TestWordHighlightToggleLabel pins the naming convention every ≡ toggle
// follows: the label says what the click will DO, not what is true now.
func TestWordHighlightToggleLabel(t *testing.T) {
	a := newTestApp(t, t.TempDir())

	a.wordHLEnabled = true
	if got := a.wordHighlightToggleLabel(); got != "Hide matching word highlight" {
		t.Errorf("label with highlight on = %q", got)
	}
	a.wordHLEnabled = false
	if got := a.wordHighlightToggleLabel(); got != "Show matching word highlight" {
		t.Errorf("label with highlight off = %q", got)
	}
}

// TestOpenFile_InheritsWordHighlight pins the per-tab plumbing: the flag
// lives on the Tab (that's what the decoration source can see), so a
// newly opened file has to pick up the App's copy.
func TestOpenFile_InheritsWordHighlight(t *testing.T) {
	root := t.TempDir()
	first := filepath.Join(root, "one.go")
	second := filepath.Join(root, "two.go")
	for _, p := range []string{first, second} {
		if err := os.WriteFile(p, []byte("z := z\n"), 0644); err != nil {
			t.Fatalf("seed file: %v", err)
		}
	}
	a := newTestApp(t, root)
	a.wordHLEnabled = true

	a.openFile(first)
	a.openFile(second)

	for _, tab := range a.tabs {
		if !tab.WordHighlight {
			t.Errorf("tab %s opened without the highlight flag", tab.Path)
		}
	}
}

// TestLoadUserConfig_AppliesWordHL pins the startup path: config.json's
// preference reaches the App copy AND every already-open tab, which is
// what makes a mid-session config reload behave.
func TestLoadUserConfig_AppliesWordHL(t *testing.T) {
	cfgDir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", cfgDir)
	cfgPath := filepath.Join(cfgDir, "ced", "config.json")
	if err := os.MkdirAll(filepath.Dir(cfgPath), 0755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	// icons:"off" keeps Resolve from shelling out to font detection.
	if err := os.WriteFile(cfgPath, []byte(`{"icons":"off","wordhl":"off"}`+"\n"), 0644); err != nil {
		t.Fatalf("seed config: %v", err)
	}
	root := t.TempDir()
	path := filepath.Join(root, "a.go")
	if err := os.WriteFile(path, []byte("q := q\n"), 0644); err != nil {
		t.Fatalf("seed file: %v", err)
	}
	a := newTestApp(t, root)
	a.wordHLEnabled = true
	a.openFile(path)

	a.loadUserConfig()

	if a.wordHLEnabled {
		t.Error("loadUserConfig should apply wordhl:off")
	}
	if a.activeTabPtr().WordHighlight {
		t.Error("the open tab should have been re-stamped from the config")
	}
}
