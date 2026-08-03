// =============================================================================
// File: internal/editor/fileio_test.go
// Author: Rohan Allison <rohanthewiz@gmail.com>
// Created: 2026-08-03
// Copyright: 2026 Rohan Allison. All rights reserved.
// =============================================================================

package editor

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeTemp drops content at a fresh path under t.TempDir and returns it.
func writeTemp(t *testing.T, name string, content []byte) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, content, 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

// TestNewTab_RefusesOversizeFile pins the open guard. The point is that
// the refusal happens on the STAT, before the bytes are in memory — a
// limit checked after the read would already have paid for the damage.
func TestNewTab_RefusesOversizeFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "huge.log")
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	// Sparse: the guard reads the size, so this costs no disk.
	if err := f.Truncate(MaxOpenBytes + 1); err != nil {
		t.Fatal(err)
	}
	f.Close()

	if _, err := NewTab(path); err == nil {
		t.Fatal("a file over MaxOpenBytes must be refused")
	} else if !strings.Contains(err.Error(), "too large") {
		t.Fatalf("error should say why: %v", err)
	}
}

// TestNewTab_RefusesBinary keeps a misclick in the file tree from loading
// an executable into a string-per-line buffer.
func TestNewTab_RefusesBinary(t *testing.T) {
	path := writeTemp(t, "a.bin", []byte("\x7fELF\x02\x01\x01\x00\x00\x00\x00\x00binary"))
	if _, err := NewTab(path); err == nil {
		t.Fatal("a file with NUL bytes in its head must be refused")
	} else if !strings.Contains(err.Error(), "binary") {
		t.Fatalf("error should say why: %v", err)
	}
}

// TestNewTab_NulPastTheSniffWindowStillOpens pins the limit of the sniff:
// only the head is examined, so a large text file that happens to contain
// a NUL deep inside still opens. Scanning the whole file would make the
// guard cost proportional to the file it is meant to protect us from.
func TestNewTab_NulPastTheSniffWindowStillOpens(t *testing.T) {
	body := append([]byte(strings.Repeat("plain text line\n", 2000)), 0)
	path := writeTemp(t, "long.txt", body)
	if _, err := NewTab(path); err != nil {
		t.Fatalf("text file with a late NUL should still open: %v", err)
	}
}

// TestNewTab_MissingFileOpensEmpty pins that the guards did not break the
// "open a path that doesn't exist yet" contract.
func TestNewTab_MissingFileOpensEmpty(t *testing.T) {
	path := filepath.Join(t.TempDir(), "new.go")
	tab, err := NewTab(path)
	if err != nil {
		t.Fatalf("a nonexistent path must open as an empty buffer: %v", err)
	}
	if got := tab.Buffer.String(); got != "" {
		t.Fatalf("expected an empty buffer, got %q", got)
	}
	if tab.LineEnding != lineEndingLF {
		t.Fatalf("a new file should default to LF, got %q", tab.LineEnding)
	}
}

// TestCRLF_StrippedFromTheBuffer is the display half of the CRLF bug: a
// carriage return left on the end of every line renders as a stray cell
// and puts End one column past the text.
func TestCRLF_StrippedFromTheBuffer(t *testing.T) {
	path := writeTemp(t, "win.go", []byte("package main\r\n\r\nfunc main() {}\r\n"))
	tab, err := NewTab(path)
	if err != nil {
		t.Fatal(err)
	}
	for i, line := range tab.Buffer.Lines {
		if strings.Contains(line, "\r") {
			t.Fatalf("line %d still carries a CR: %q", i, line)
		}
	}
	if tab.LineEnding != lineEndingCRLF {
		t.Fatalf("the tab should remember it was CRLF, got %q", tab.LineEnding)
	}
}

// TestCRLF_SurvivesASave is the data half, and the reason this matters
// more than the stray glyph: before, a save wrote the untouched lines
// back with CRLF and every newly typed line with a bare LF, quietly
// converting the file to mixed endings and lighting up the whole file in
// git.
func TestCRLF_SurvivesASave(t *testing.T) {
	path := writeTemp(t, "win.go", []byte("package main\r\n\r\nfunc main() {}\r\n"))
	tab, err := NewTab(path)
	if err != nil {
		t.Fatal(err)
	}
	tab.Cursor = Position{Line: 2, Col: 0}
	tab.Anchor = tab.Cursor
	tab.InsertString("// added\n")
	if err := tab.Save(); err != nil {
		t.Fatal(err)
	}

	out, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	text := string(out)
	if strings.Contains(strings.ReplaceAll(text, "\r\n", ""), "\n") {
		t.Fatalf("save introduced a bare LF into a CRLF file: %q", text)
	}
	if !strings.Contains(text, "// added\r\n") {
		t.Fatalf("the newly typed line should have been written CRLF: %q", text)
	}
}

// TestLF_StaysLF guards the other direction — the common case must not
// acquire carriage returns.
func TestLF_StaysLF(t *testing.T) {
	path := writeTemp(t, "unix.go", []byte("package main\n\nfunc main() {}\n"))
	tab, err := NewTab(path)
	if err != nil {
		t.Fatal(err)
	}
	tab.InsertString("// added\n")
	if err := tab.Save(); err != nil {
		t.Fatal(err)
	}
	out, _ := os.ReadFile(path)
	if strings.Contains(string(out), "\r") {
		t.Fatalf("an LF file must not gain carriage returns: %q", out)
	}
}

// TestDetectLineEnding_MixedFilePicksTheDominantEnding pins the
// half-the-newlines rule: a mostly-CRLF file with a few stray LF lines
// round-trips as CRLF, which is what its author and its VCS expect.
func TestDetectLineEnding_MixedFilePicksTheDominantEnding(t *testing.T) {
	cases := []struct {
		name string
		data string
		want string
	}{
		{"all crlf", "a\r\nb\r\nc\r\n", lineEndingCRLF},
		{"all lf", "a\nb\nc\n", lineEndingLF},
		{"mostly crlf", "a\r\nb\r\nc\r\nd\n", lineEndingCRLF},
		{"mostly lf", "a\nb\nc\nd\r\n", lineEndingLF},
		{"no newline at all", "single line", lineEndingLF},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := detectLineEnding([]byte(tc.data)); got != tc.want {
				t.Fatalf("got %q want %q", got, tc.want)
			}
		})
	}
}

// TestBOM_RoundTrips: a UTF-8 byte-order mark is not text. Left in the
// buffer it draws as a stray glyph on line 1; dropped on save it changes
// a file some Windows toolchains then refuse to read.
func TestBOM_RoundTrips(t *testing.T) {
	path := writeTemp(t, "bom.txt", append(append([]byte(nil), utf8BOM...), []byte("hello\n")...))
	tab, err := NewTab(path)
	if err != nil {
		t.Fatal(err)
	}
	if tab.Buffer.Lines[0] != "hello" {
		t.Fatalf("BOM should not reach the buffer, got %q", tab.Buffer.Lines[0])
	}
	if !tab.BOM {
		t.Fatal("the tab should remember the file had a BOM")
	}
	tab.Cursor = Position{Line: 0, Col: 5}
	tab.Anchor = tab.Cursor
	tab.InsertRune('!')
	if err := tab.Save(); err != nil {
		t.Fatal(err)
	}
	out, _ := os.ReadFile(path)
	if string(out) != string(utf8BOM)+"hello!\n" {
		t.Fatalf("BOM lost on save: %q", out)
	}
}

// TestSave_PreservesFileMode is the regression the atomic write could
// easily have introduced: os.WriteFile's perm argument only applies when
// it CREATES the file, so the old in-place write preserved mode for free.
// A fresh temp file does not, and silently dropping the +x from a script
// the user just edited is a genuinely bad surprise.
func TestSave_PreservesFileMode(t *testing.T) {
	path := writeTemp(t, "run.sh", []byte("#!/bin/sh\necho hi\n"))
	if err := os.Chmod(path, 0o755); err != nil {
		t.Fatal(err)
	}
	tab, err := NewTab(path)
	if err != nil {
		t.Fatal(err)
	}
	tab.InsertRune('#')
	if err := tab.Save(); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != 0o755 {
		t.Fatalf("mode not preserved across save: got %v want 0755", got)
	}
}

// TestSave_WritesThroughASymlink pins the symlink resolution. Renaming
// onto a symlink would replace the LINK with a regular file, which
// quietly breaks the dotfile layouts this editor's users keep.
func TestSave_WritesThroughASymlink(t *testing.T) {
	dir := t.TempDir()
	real := filepath.Join(dir, "real.conf")
	link := filepath.Join(dir, "link.conf")
	if err := os.WriteFile(real, []byte("value = 1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(real, link); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}

	tab, err := NewTab(link)
	if err != nil {
		t.Fatal(err)
	}
	tab.Cursor = Position{Line: 0, Col: 9}
	tab.Anchor = tab.Cursor
	tab.InsertRune('2')
	if err := tab.Save(); err != nil {
		t.Fatal(err)
	}

	info, err := os.Lstat(link)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode()&os.ModeSymlink == 0 {
		t.Fatal("save replaced the symlink with a regular file")
	}
	out, _ := os.ReadFile(real)
	if !strings.Contains(string(out), "value = 12") {
		t.Fatalf("the edit should have landed on the link target, got %q", out)
	}
}

// TestSave_LeavesNoTempFileBehind: the temp file lives in the target's
// directory (rename is only atomic within a filesystem), so a leak would
// litter the user's project with dotfiles.
func TestSave_LeavesNoTempFileBehind(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "a.go")
	if err := os.WriteFile(path, []byte("package a\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	tab, err := NewTab(path)
	if err != nil {
		t.Fatal(err)
	}
	tab.InsertRune('/')
	if err := tab.Save(); err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Name() != "a.go" {
		var names []string
		for _, e := range entries {
			names = append(names, e.Name())
		}
		t.Fatalf("save left extra files behind: %v", names)
	}
}

// TestWriteFileAtomic_FallsBackInAReadOnlyDirectory: a writable file
// inside a read-only directory is a real configuration, and refusing to
// save there would be a worse failure than losing atomicity for it.
func TestWriteFileAtomic_FallsBackInAReadOnlyDirectory(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root ignores directory permissions")
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "locked.txt")
	if err := os.WriteFile(path, []byte("before\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(dir, 0o555); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(dir, 0o755) })

	if err := writeFileAtomic(path, []byte("after\n")); err != nil {
		t.Fatalf("should have fallen back to an in-place write: %v", err)
	}
	out, _ := os.ReadFile(path)
	if string(out) != "after\n" {
		t.Fatalf("fallback write did not land: %q", out)
	}
}

// TestHumanBytes covers the sizes an error message actually reports.
func TestHumanBytes(t *testing.T) {
	cases := map[int64]string{
		512:              "512 B",
		2048:             "2.0 KB",
		5 << 20:          "5.0 MB",
		MaxOpenBytes:     "32.0 MB",
		3 * (1 << 30):    "3.0 GB",
		1536 * (1 << 20): "1.5 GB",
	}
	for in, want := range cases {
		if got := humanBytes(in); got != want {
			t.Fatalf("humanBytes(%d) = %q, want %q", in, got, want)
		}
	}
}
