// =============================================================================
// File: internal/editor/diskstamp_test.go
// Author: Rohan Allison <rohanthewiz@gmail.com>
// Created: 2026-11-10
// Copyright: 2026 Rohan Allison. All rights reserved.
// =============================================================================

// Tests for diskstamp.go — the (mtime, size, inode) stamp, the content-sum
// fallback that keeps a touch from reading as a change, and the racily
// clean rule for same-granule rewrites. Every external write here is one
// the old `mtime.After` test missed or misjudged (N-117).

package editor

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// statOf stats path, failing the test on error.
func statOf(t *testing.T, path string) os.FileInfo {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	return info
}

// openStamped writes body, opens it with NewTab, and pins the clock the
// racy rule reads to an hour past the file's mtime, so the stamp is old
// enough to vouch for the content unless a test says otherwise.
func openStamped(t *testing.T, body string) (*Tab, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "f.txt")
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-2 * time.Hour)
	if err := os.Chtimes(path, old, old); err != nil {
		t.Fatal(err)
	}
	tab, err := NewTab(path)
	if err != nil {
		t.Fatal(err)
	}
	return tab, path
}

// TestDiskStamp_SameIsEqualityNotAfter: an OLDER mtime is a different
// version too; so is a different size or inode.
func TestDiskStamp_SameIsEqualityNotAfter(t *testing.T) {
	base := DiskStamp{ModTime: time.Unix(1000, 0), Size: 10, Ino: 7}
	if !base.Same(base) {
		t.Fatal("a stamp must equal itself")
	}
	older := base
	older.ModTime = time.Unix(500, 0)
	if base.Same(older) {
		t.Fatal("an older mtime read as the same version")
	}
	bigger := base
	bigger.Size = 11
	if base.Same(bigger) {
		t.Fatal("a different size read as the same version")
	}
	moved := base
	moved.Ino = 8
	if base.Same(moved) {
		t.Fatal("a different inode read as the same version")
	}
	noIno := base
	noIno.Ino = 0
	if !base.Same(noIno) {
		t.Fatal("a missing inode must not count as a difference")
	}
	if !(DiskStamp{}).IsZero() || base.IsZero() {
		t.Fatal("IsZero wrong")
	}
}

// TestStampOf_ReadsTheInode: on the platforms ced ships for, the stat
// carries an inode and inodeOf finds it.
func TestStampOf_ReadsTheInode(t *testing.T) {
	path := filepath.Join(t.TempDir(), "i.txt")
	if err := os.WriteFile(path, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	s := StampOf(statOf(t, path))
	if s.Ino == 0 {
		t.Skip("this platform's stat reports no inode")
	}
	if s.Size != 1 || s.ModTime.IsZero() {
		t.Fatalf("stamp = %+v", s)
	}
	if StampOf(nil) != (DiskStamp{}) {
		t.Fatal("StampOf(nil) should be zero")
	}
}

// TestDiskChanged_OlderMtimeIsSeen is the `cp -p` / restored-backup case:
// new content carrying an mtime OLDER than the one the tab recorded. The
// old After test called that unchanged and the next save overwrote it.
func TestDiskChanged_OlderMtimeIsSeen(t *testing.T) {
	tab, path := openStamped(t, "mine\n")
	if err := os.WriteFile(path, []byte("their backup\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	older := tab.Mtime.Add(-48 * time.Hour)
	if err := os.Chtimes(path, older, older); err != nil {
		t.Fatal(err)
	}
	if !tab.DiskChanged(statOf(t, path)) {
		t.Fatal("an external write with an older mtime went unseen")
	}
}

// TestDiskChanged_TouchIsNotAChange: the stamp moves but the bytes hash the
// same, so nothing changed — and the new stamp is adopted, so the next
// check is a plain stat again.
func TestDiskChanged_TouchIsNotAChange(t *testing.T) {
	tab, path := openStamped(t, "same\n")
	later := tab.Mtime.Add(time.Hour)
	if err := os.Chtimes(path, later, later); err != nil {
		t.Fatal(err)
	}
	info := statOf(t, path)
	if tab.DiskChanged(info) {
		t.Fatal("a touch read as an external edit")
	}
	if !tab.DiskStamp().Same(StampOf(info)) || !tab.Mtime.Equal(later) {
		t.Fatal("the touched stamp was not adopted")
	}
}

// TestDiskChanged_SameGranuleRewriteIsSeen is the coarse-clock case: a
// same-size, in-place rewrite whose mtime lands in the same tick as our
// read, so (mtime, size, inode) all match. The stamp is "racily clean",
// and the content check catches it.
func TestDiskChanged_SameGranuleRewriteIsSeen(t *testing.T) {
	path := filepath.Join(t.TempDir(), "g.txt")
	if err := os.WriteFile(path, []byte("aaaa\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	tab, err := NewTab(path) // recorded just now: the stamp is racy
	if err != nil {
		t.Fatal(err)
	}
	mtime := tab.Mtime
	f, err := os.OpenFile(path, os.O_WRONLY, 0) // in place: same inode
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteAt([]byte("bbbb\n"), 0); err != nil {
		t.Fatal(err)
	}
	f.Close()
	if err := os.Chtimes(path, mtime, mtime); err != nil { // same granule
		t.Fatal(err)
	}
	info := statOf(t, path)
	if !tab.DiskStamp().Same(StampOf(info)) {
		t.Skip("filesystem changed the stamp anyway; nothing racy to test")
	}
	if !tab.DiskChanged(info) {
		t.Fatal("a same-granule rewrite went unseen")
	}
}

// TestDiskChanged_OldStampSkipsTheRead: once the stamp is a granule older
// than the moment it was confirmed, a matching stamp is trusted without
// reading the file — the racy check must not turn every tick into a read.
func TestDiskChanged_OldStampSkipsTheRead(t *testing.T) {
	tab, path := openStamped(t, "quiet\n")
	reads := 0
	orig := diskReadFile
	diskReadFile = func(p string) ([]byte, error) { reads++; return orig(p) }
	t.Cleanup(func() { diskReadFile = orig })

	if tab.DiskChanged(statOf(t, path)) {
		t.Fatal("unchanged file read as changed")
	}
	if reads != 0 {
		t.Fatalf("an old matching stamp still read the file %d times", reads)
	}
}

// TestDiskChanged_RacyConfirmationAgesOut: a racy stamp is read once,
// re-dated on success, and trusted afterwards once the clock moves past
// the granule.
func TestDiskChanged_RacyConfirmationAgesOut(t *testing.T) {
	path := filepath.Join(t.TempDir(), "r.txt")
	if err := os.WriteFile(path, []byte("r\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	tab, err := NewTab(path)
	if err != nil {
		t.Fatal(err)
	}
	reads := 0
	origRead, origNow := diskReadFile, diskNow
	diskReadFile = func(p string) ([]byte, error) { reads++; return origRead(p) }
	diskNow = func() time.Time { return tab.Mtime.Add(time.Minute) }
	t.Cleanup(func() { diskReadFile, diskNow = origRead, origNow })

	info := statOf(t, path)
	if tab.DiskChanged(info) || reads != 1 {
		t.Fatalf("racy check: changed or reads=%d, want unchanged after 1 read", reads)
	}
	if tab.DiskChanged(info) || reads != 1 {
		t.Fatalf("confirmed stamp read again: reads=%d", reads)
	}
}

// TestNewTab_MissingFileRecordsNoStamp: a path that doesn't exist yet has
// no version to compare against.
func TestNewTab_MissingFileRecordsNoStamp(t *testing.T) {
	tab, err := NewTab(filepath.Join(t.TempDir(), "new.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if !tab.DiskStamp().IsZero() {
		t.Fatalf("missing file recorded %+v", tab.DiskStamp())
	}
}

// TestSave_RecordsTheWrittenVersion: after a save the file on disk is the
// recorded version, so the next check reports no change.
func TestSave_RecordsTheWrittenVersion(t *testing.T) {
	tab, path := openStamped(t, "a\n")
	tab.InsertString("b")
	if err := tab.Save(); err != nil {
		t.Fatal(err)
	}
	if tab.DiskChanged(statOf(t, path)) {
		t.Fatal("our own save read as an external change")
	}
}

// TestForgetDiskStamp_NextCheckIsAChange: a forgotten stamp makes whatever
// is on disk news (a deleted file that came back).
func TestForgetDiskStamp_NextCheckIsAChange(t *testing.T) {
	tab, path := openStamped(t, "x\n")
	tab.ForgetDiskStamp()
	if !tab.Mtime.IsZero() || !tab.DiskStamp().IsZero() {
		t.Fatal("stamp not forgotten")
	}
	if !tab.DiskChanged(statOf(t, path)) {
		t.Fatal("a forgotten stamp must read as changed")
	}
}

// TestAdoptDisk_AcceptsTheCurrentVersion: after AdoptDisk the file as it
// stands is the recorded version, and the buffer is untouched.
func TestAdoptDisk_AcceptsTheCurrentVersion(t *testing.T) {
	tab, path := openStamped(t, "mine\n")
	if err := os.WriteFile(path, []byte("theirs\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := tab.AdoptDisk(); err != nil {
		t.Fatal(err)
	}
	if tab.DiskChanged(statOf(t, path)) {
		t.Fatal("adopted version still reads as changed")
	}
	if tab.Buffer.String() != "mine\n" {
		t.Fatal("AdoptDisk touched the buffer")
	}
	gone := &Tab{Path: filepath.Join(t.TempDir(), "gone")}
	if err := gone.AdoptDisk(); err == nil {
		t.Fatal("adopting a missing file should report the stat error")
	}
}
