// =============================================================================
// File: internal/spell/spell_test.go
// Author: Rohan Allison <rohanthewiz@gmail.com>
// Created: 2026-11-16
// Copyright: 2026 Rohan Allison. All rights reserved.
// =============================================================================

package spell

import (
	"os"
	"path/filepath"
	"testing"
)

// dict loads the embedded lists once for the whole package's tests — a
// 90k-word gunzip per test would dominate the suite for no coverage.
var dict = func() *Dictionary {
	d, err := Load()
	if err != nil {
		panic(err)
	}
	return d
}()

// TestLoad_EmbeddedListsAreWhole is the smoke test for the data: the
// SCOWL list is all there, and both curated lists made it in — extra.txt
// as plain words, ident.txt as trusted ones.
func TestLoad_EmbeddedListsAreWhole(t *testing.T) {
	if n := dict.Len(); n < 80_000 {
		t.Fatalf("dictionary has %d words; the embedded list looks truncated", n)
	}
	if !dict.Known("goroutine") {
		t.Error("extra.txt's words are missing")
	}
	if !dict.Known("omitempty") || !dict.isTrusted("omitempty") {
		t.Error("ident.txt's words are missing or not trusted")
	}
	if dict.isTrusted("goroutine") {
		t.Error("an extra.txt word is trusted; only ident.txt and user words should be")
	}
}

// TestKnown_IsCaseLenient: the identifier conventions capitalize words
// the list holds lowercase, and lowercase names the list holds
// capitalized — neither is a spelling mistake.
func TestKnown_IsCaseLenient(t *testing.T) {
	for _, w := range []string{"name", "Name", "english", "monday", "json", "don't", "editor's"} {
		if !dict.Known(w) {
			t.Errorf("Known(%q) = false, want true", w)
		}
	}
	for _, w := range []string{"", "nmae", "recieve", "adress"} {
		if dict.Known(w) {
			t.Errorf("Known(%q) = true, want false", w)
		}
	}
}

// TestNew_HoldsExactlyItsWords: the test constructor knows what it was
// given and nothing from the embedded lists, and trusts none of it.
func TestNew_HoldsExactlyItsWords(t *testing.T) {
	d := New("user", " name ", "")
	if !d.Known("user") || !d.Known("Name") {
		t.Error("New lost a word it was given")
	}
	if d.Known("the") {
		t.Error("New loaded the embedded lists")
	}
	if d.Len() != 2 {
		t.Errorf("Len = %d, want 2 (blank skipped)", d.Len())
	}
	if d.isTrusted("user") {
		t.Error("New's words should be plain list words, not trusted")
	}
}

// TestLoad_UserFilesAreKnownAndTrusted: a user dictionary's words are
// known and trusted (so they can build run-together names); a missing
// file is the fresh-install normal and no error.
func TestLoad_UserFilesAreKnownAndTrusted(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "dictionary.txt")
	if err := os.WriteFile(path, []byte("# mine\n\nacme\n  Zorbulate  \n"), 0o644); err != nil {
		t.Fatal(err)
	}
	d, err := Load(path, filepath.Join(dir, "missing.txt"), "")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	for _, w := range []string{"acme", "Zorbulate", "zorbulate"} {
		if !d.Known(w) {
			t.Errorf("Known(%q) = false after loading the user file", w)
		}
	}
	if !d.isTrusted("acme") {
		t.Error("a user word is not trusted")
	}
}

// TestLoad_UnreadableUserFileDegrades is where ced parts from cats-todo:
// a user file that exists but cannot be read costs only its own words.
// The dictionary still comes back, with the error alongside.
func TestLoad_UnreadableUserFileDegrades(t *testing.T) {
	dir := t.TempDir() // a directory: opens, but cannot be read as lines
	d, err := Load(dir)
	if err == nil {
		t.Error("Load(directory) returned no error, want one")
	}
	if d == nil || !d.Known("the") {
		t.Fatal("an unreadable user file cost the embedded lists")
	}
}
