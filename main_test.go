// =============================================================================
// File: main_test.go
// Author: Spicer Matthews <spicer@cloudmanic.com>
// Created: 2026-04-30
// Copyright: 2026 Cloudmanic, LLC. All rights reserved.
// =============================================================================

package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/rohanthewiz/ced/internal/session"
	"github.com/rohanthewiz/ced/internal/userconfig"
)

// TestResolveArgs_NoArgsRootsCurrentDir keeps the no-arg path simple:
// "." as rootDir, no file to open, action = edit.
func TestResolveArgs_NoArgsRootsCurrentDir(t *testing.T) {
	got := resolveArgs(nil)
	if got.Action != actionEdit {
		t.Fatalf("action: got %q, want edit", got.Action)
	}
	if got.RootDir != "." {
		t.Fatalf("rootDir: got %q, want .", got.RootDir)
	}
	if got.OpenFile != "" {
		t.Fatalf("OpenFile should be empty, got %q", got.OpenFile)
	}
}

// TestResolveArgs_DirectoryArgUsesAsRoot pins the existing behaviour:
// passing a directory uses it as the editor's root.
func TestResolveArgs_DirectoryArgUsesAsRoot(t *testing.T) {
	dir := t.TempDir()
	got := resolveArgs([]string{dir})
	if got.Action != actionEdit {
		t.Fatalf("action: got %q", got.Action)
	}
	if got.RootDir != dir {
		t.Fatalf("rootDir: got %q, want %q", got.RootDir, dir)
	}
	if got.OpenFile != "" {
		t.Fatalf("OpenFile should be empty, got %q", got.OpenFile)
	}
}

// TestResolveArgs_FileArgRootsParent is the regression test for the
// "ced main.go" bug: a file argument should root the editor at
// the file's parent and seed an OpenFile so the user's tab is ready.
func TestResolveArgs_FileArgRootsParent(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "main.go")
	if err := os.WriteFile(target, []byte("package main"), 0644); err != nil {
		t.Fatalf("seed: %v", err)
	}

	got := resolveArgs([]string{target})
	if got.Action != actionEdit {
		t.Fatalf("action: got %q", got.Action)
	}
	if got.RootDir != dir {
		t.Fatalf("rootDir: got %q, want %q", got.RootDir, dir)
	}
	if got.OpenFile != target {
		t.Fatalf("OpenFile: got %q, want %q", got.OpenFile, target)
	}
}

// TestResolveArgs_BarefilenameRootsCwd covers the common "ced
// foo.go" form where the path has no directory component. The
// filepath.Dir of "foo.go" is "." — without the empty-string guard
// we'd hand the editor an empty rootDir and filetree.New would fail.
func TestResolveArgs_BarefilenameRootsCwd(t *testing.T) {
	// Use a real bare filename in a temp cwd so the stat path covers
	// the existing-file branch.
	dir := t.TempDir()
	cwd, _ := os.Getwd()
	t.Cleanup(func() { _ = os.Chdir(cwd) })
	if err := os.Chdir(dir); err != nil {
		t.Fatalf("chdir: %v", err)
	}
	if err := os.WriteFile("bare.txt", []byte("x"), 0644); err != nil {
		t.Fatalf("seed: %v", err)
	}

	got := resolveArgs([]string{"bare.txt"})
	if got.RootDir != "." {
		t.Fatalf("rootDir: got %q, want .", got.RootDir)
	}
	if got.OpenFile != "bare.txt" {
		t.Fatalf("OpenFile: got %q, want bare.txt", got.OpenFile)
	}
}

// TestResolveArgs_MissingFileTreatsAsNew mirrors `vim foo.go` on a
// non-existent path: open the editor at the parent dir with the file
// queued for editing — first save creates it.
func TestResolveArgs_MissingFileTreatsAsNew(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "new.go")

	got := resolveArgs([]string{target})
	if got.Err != nil {
		t.Fatalf("missing file should not be an error, got %v", got.Err)
	}
	if got.RootDir != dir {
		t.Fatalf("rootDir: got %q, want %q", got.RootDir, dir)
	}
	if got.OpenFile != target {
		t.Fatalf("OpenFile: got %q, want %q", got.OpenFile, target)
	}
}

// TestResolveArgs_VersionFlag covers every flavour of --version we
// accept. Failing here would mean a user typing `--version` lands in
// the editor instead of seeing a printed version.
func TestResolveArgs_VersionFlag(t *testing.T) {
	for _, flag := range []string{"--version", "-v", "-V", "version"} {
		got := resolveArgs([]string{flag})
		if got.Action != actionVersion {
			t.Errorf("flag %q: action = %q, want version", flag, got.Action)
		}
	}
}

// TestResolveArgs_HelpFlag is the equivalent for --help. Like version,
// the multi-spelling list keeps the CLI forgiving.
func TestResolveArgs_HelpFlag(t *testing.T) {
	for _, flag := range []string{"--help", "-h", "help"} {
		got := resolveArgs([]string{flag})
		if got.Action != actionHelp {
			t.Errorf("flag %q: action = %q, want help", flag, got.Action)
		}
	}
}

// TestResolveArgs_LastFlag covers every spelling of --last: the action
// is still edit and RootDir stays the safe "." fallback, with Last set
// so main can look the folder up. Resolution happens in main, not here,
// because resolveArgs is deliberately IO-free beyond os.Stat.
func TestResolveArgs_LastFlag(t *testing.T) {
	for _, flag := range []string{"--last", "-l", "last"} {
		got := resolveArgs([]string{flag})
		if got.Action != actionEdit {
			t.Errorf("flag %q: action = %q, want edit", flag, got.Action)
		}
		if !got.Last {
			t.Errorf("flag %q: Last = false, want true", flag)
		}
		if got.RootDir != "." {
			t.Errorf("flag %q: RootDir = %q, want the . fallback", flag, got.RootDir)
		}
	}
}

// TestResolveArgs_BareCedDoesNotRestoreLast is the pin on the deliberate
// choice behind --last existing at all: bare `ced` opens the CURRENT
// directory. `cd myproj && ced` is the gesture this editor is launched
// with, and silently landing in a different project would make that
// reflex a lie — the folder's TABS come back instead (session restore).
func TestResolveArgs_BareCedDoesNotRestoreLast(t *testing.T) {
	got := resolveArgs(nil)
	if got.Last {
		t.Fatal("bare ced must not resolve to the last folder")
	}
	if got.RootDir != "." {
		t.Fatalf("RootDir = %q, want .", got.RootDir)
	}
}

// TestLastFolder_ReadsTheStateFile pins the --last lookup end to end
// against a real state file, and its two silent fallbacks: nothing
// recorded, and an unreadable file. Both must yield "" so main opens the
// current directory rather than refusing to start over a convenience
// file.
func TestLastFolder_ReadsTheStateFile(t *testing.T) {
	cfg := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", cfg)

	if got := lastFolder(); got != "" {
		t.Fatalf("lastFolder() with no state file = %q, want empty", got)
	}

	st := &session.Store{}
	st.Touch(filepath.Join(cfg, "older"))
	st.Touch(filepath.Join(cfg, "newest"))
	if err := st.Save(userconfig.StatePath()); err != nil {
		t.Fatalf("Save: %v", err)
	}
	if got, want := lastFolder(), filepath.Join(cfg, "newest"); got != want {
		t.Fatalf("lastFolder() = %q, want %q", got, want)
	}

	if err := os.WriteFile(userconfig.StatePath(), []byte("{{{"), 0644); err != nil {
		t.Fatalf("corrupt: %v", err)
	}
	if got := lastFolder(); got != "" {
		t.Fatalf("lastFolder() on a broken state file = %q, want empty", got)
	}
}
