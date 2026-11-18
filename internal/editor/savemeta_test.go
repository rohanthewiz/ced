// =============================================================================
// File: internal/editor/savemeta_test.go
// Author: Rohan Allison <rohanthewiz@gmail.com>
// Created: 2026-11-17
// Copyright: 2026 Rohan Allison. All rights reserved.
// =============================================================================

package editor

import (
	"errors"
	"os"
	"path/filepath"
	"syscall"
	"testing"

	"golang.org/x/sys/unix"
)

// saveText opens path in a tab, replaces its text and saves it.
func saveText(t *testing.T, path, text string) error {
	t.Helper()
	tab, err := NewTab(path)
	if err != nil {
		t.Fatal(err)
	}
	tab.Buffer = NewBuffer(text)
	return tab.Save()
}

// inode returns path's inode number.
func inode(t *testing.T, path string) uint64 {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	return uint64(info.Sys().(*syscall.Stat_t).Ino)
}

// TestSave_RefusesAReadOnlyFile is N-121(a)'s loudest case: a 0444 file
// in a writable directory was overwritten silently (the temp + rename
// needs only the directory's permission). Now the save refuses, says
// why, and leaves the file as it was.
func TestSave_RefusesAReadOnlyFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "ro.txt")
	if err := os.WriteFile(path, []byte("keep\n"), 0o444); err != nil {
		t.Fatal(err)
	}
	err := saveText(t, path, "changed\n")
	if !IsReadOnlyError(err) {
		t.Fatalf("err = %v, want a *ReadOnlyError", err)
	}
	if got, _ := os.ReadFile(path); string(got) != "keep\n" {
		t.Errorf("read-only file was written: %q", got)
	}
}

// TestSave_KeepsHardLinks: a file with a second name is written IN PLACE,
// so both names see the save. A rename gave the saved name a new inode
// and left the other on the old content.
func TestSave_KeepsHardLinks(t *testing.T) {
	dir := t.TempDir()
	path, other := filepath.Join(dir, "a.txt"), filepath.Join(dir, "b.txt")
	if err := os.WriteFile(path, []byte("old\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Link(path, other); err != nil {
		t.Skipf("no hard links here: %v", err)
	}
	before := inode(t, path)
	if err := saveText(t, path, "new\n"); err != nil {
		t.Fatal(err)
	}
	if inode(t, path) != before {
		t.Error("the save replaced a hard-linked file's inode")
	}
	if got, _ := os.ReadFile(other); string(got) != "new\n" {
		t.Errorf("the other name reads %q, want the saved text", got)
	}
}

// TestSave_KeepsSetgidAndAtomicity: the full mode survives (setgid used
// to be dropped — only the nine permission bits were copied), and an
// ordinary file is still saved atomically (a NEW inode: temp + rename).
func TestSave_KeepsSetgidAndAtomicity(t *testing.T) {
	path := filepath.Join(t.TempDir(), "s.sh")
	if err := os.WriteFile(path, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, 0o755|os.ModeSetgid); err != nil {
		t.Fatal(err)
	}
	info, _ := os.Stat(path)
	if info.Mode()&os.ModeSetgid == 0 {
		t.Skip("this filesystem drops setgid on chmod")
	}
	before := inode(t, path)
	if err := saveText(t, path, "#!/bin/sh\necho hi\n"); err != nil {
		t.Fatal(err)
	}
	info, _ = os.Stat(path)
	if info.Mode()&keptModeBits != 0o755|os.ModeSetgid {
		t.Errorf("mode after save = %v, want %v", info.Mode()&keptModeBits, 0o755|os.ModeSetgid)
	}
	if inode(t, path) == before {
		t.Error("an ordinary save was not atomic (same inode — written in place)")
	}
}

// TestSave_KeepsExtendedAttributes: xattrs ride over to the new inode.
func TestSave_KeepsExtendedAttributes(t *testing.T) {
	path := filepath.Join(t.TempDir(), "x.txt")
	if err := os.WriteFile(path, []byte("x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	name := "user.ced.test" // the Linux user namespace; any name works on macOS
	if err := unix.Setxattr(path, name, []byte("kept"), 0); err != nil {
		if errors.Is(err, unix.ENOTSUP) || errors.Is(err, unix.EOPNOTSUPP) || errors.Is(err, unix.EPERM) {
			t.Skipf("no user xattrs on this filesystem: %v", err)
		}
		t.Fatal(err)
	}
	if err := saveText(t, path, "y\n"); err != nil {
		t.Fatal(err)
	}
	val, err := getXattr(path, name)
	if err != nil || string(val) != "kept" {
		t.Errorf("xattr after save = %q, %v", val, err)
	}
}

// TestReadTargetMeta_MissingFileIsNew: a path that doesn't exist yet is
// no error — the save creates it with the defaults.
func TestReadTargetMeta_MissingFileIsNew(t *testing.T) {
	m, err := readTargetMeta(filepath.Join(t.TempDir(), "nope"))
	if err != nil || m.exists {
		t.Errorf("missing file: %+v, %v", m, err)
	}
}
