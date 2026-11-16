// =============================================================================
// File: internal/spell/userdict_test.go
// Author: Rohan Allison <rohanthewiz@gmail.com>
// Created: 2026-11-16
// Copyright: 2026 Rohan Allison. All rights reserved.
// Copied from cats-todo's internal/spell tests (same author).
// =============================================================================

package spell

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestAddTeachesALoadedDictionary: a word added to the running dictionary is
// known at once, without the 90k-word reload that learning it from a file
// would otherwise cost.
func TestAddTeachesALoadedDictionary(t *testing.T) {
	d, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if d.Known("zorbulate") {
		t.Fatal("the test word is somehow already in the list")
	}
	d.Add("zorbulate")
	if !d.Known("zorbulate") {
		t.Error("Add did not teach the dictionary the word")
	}
	// An added word is trusted, so it can build run-together names.
	if !d.isTrusted("zorbulate") || d.CheckIdent("zorbulateid") != nil {
		t.Error("an added word is not trusted as a compound part")
	}
	// Lookup stays as lenient for an added word as for a listed one.
	if !d.Known("Zorbulate") {
		t.Error("an added word is not known capitalized")
	}
	if n := d.Len(); n == 0 {
		t.Error("Len went to zero")
	}
	// Blank input is not a word and must not become one — an empty entry would
	// match nothing and sit in the file forever.
	before := d.Len()
	d.Add("   ")
	if d.Len() != before {
		t.Error("Add stored a blank word")
	}
}

// TestAppendWordCreatesTheFile: the first word written to a dictionary that
// does not exist yet creates it, and its directory, with a header explaining
// what the file is — it is meant to be opened by hand.
func TestAppendWordCreatesTheFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nested", "dictionary.txt")
	if err := AppendWord(path, "zorbulate"); err != nil {
		t.Fatalf("AppendWord: %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(string(data), "#") {
		t.Errorf("a created dictionary has no header:\n%s", data)
	}
	if !strings.HasSuffix(string(data), "zorbulate\n") {
		t.Errorf("the word is not the last line:\n%s", data)
	}
	// And Load reads it back, which is the only reason the file is written.
	d, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if !d.Known("zorbulate") {
		t.Error("the word written to the file is not known after a reload")
	}
}

// TestAppendWordDoesNotRepeatItself: a word the file already lists is left
// alone, whatever case it was listed in, and a hand-edited file that ends
// mid-line gets its newline before the new word rather than losing both.
func TestAppendWordDoesNotRepeatItself(t *testing.T) {
	path := filepath.Join(t.TempDir(), "dictionary.txt")
	if err := os.WriteFile(path, []byte("# mine\nKubernetes\ncatctl"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, w := range []string{"kubernetes", "Kubernetes", "KUBERNETES"} {
		if err := AppendWord(path, w); err != nil {
			t.Fatalf("AppendWord(%q): %v", w, err)
		}
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Count(strings.ToLower(string(data)), "kubernetes"); got != 1 {
		t.Errorf("the word was written %d times:\n%s", got, data)
	}
	// The last line had no newline; the next word must not be joined onto it.
	if err := AppendWord(path, "bytdb"); err != nil {
		t.Fatal(err)
	}
	data, _ = os.ReadFile(path)
	if strings.Contains(string(data), "catctlbytdb") {
		t.Errorf("the new word was joined onto an unfinished line:\n%s", data)
	}
	if !strings.HasSuffix(string(data), "bytdb\n") {
		t.Errorf("the new word is not the last line:\n%s", data)
	}
	// A file that already existed is not given a second header.
	if got := strings.Count(string(data), "# mine"); got != 1 {
		t.Errorf("the existing header was disturbed:\n%s", data)
	}
}

// TestAppendWordRefusesWhatCannotBeRead: a line with a space in it is read back
// whole and can then never match a token, so it would be a word that silently
// does nothing. Better to say so than to write it.
func TestAppendWordRefusesWhatCannotBeRead(t *testing.T) {
	path := filepath.Join(t.TempDir(), "dictionary.txt")
	for _, w := range []string{"", "   ", "two words", "with\ttab"} {
		if err := AppendWord(path, w); err == nil {
			t.Errorf("AppendWord(%q) succeeded, want an error", w)
		}
	}
	if err := AppendWord("", "word"); err == nil {
		t.Error("AppendWord with no path succeeded, want an error")
	}
	if _, err := os.Stat(path); err == nil {
		t.Error("a refused word still created the file")
	}
	// An unreadable file is an error rather than a silent overwrite: the words
	// already in it are the user's, and appending blind would be a way to lose
	// them.
	dir := filepath.Join(t.TempDir(), "dictionary.txt")
	if err := os.Mkdir(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := AppendWord(dir, "word"); err == nil {
		t.Error("AppendWord into a directory succeeded, want an error")
	}
}
