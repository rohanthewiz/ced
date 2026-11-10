// =============================================================================
// File: internal/editor/diskstamp.go
// Author: Rohan Allison <rohanthewiz@gmail.com>
// Created: 2026-11-10
// Copyright: 2026 Rohan Allison. All rights reserved.
// =============================================================================

// diskstamp.go answers one question for every layer that guards a write
// or watches for an external one: "is the file on disk still the version
// this tab last read or wrote?"
//
// It used to be answered by `disk mtime is AFTER tab.Mtime`, and that
// misses every external write whose timestamp is not newer (N-117):
//
//   - `cp -p`, `rsync -t`, `tar x`, a restored backup — they carry the
//     SOURCE's mtime, often older than ours;
//   - a write inside the same timestamp granule (NFS, some sshfs, a
//     coarse-clock filesystem) — the mtime does not move at all.
//
// Each was invisible to the watcher, then silently overwritten by the
// next save or auto-save.
//
// Two layers now:
//
//  1. A STAMP — (mtime, size, inode) — recorded whenever the tab reads or
//     writes the file. ANY difference counts, in either direction; an
//     older mtime is just as much "someone else's file" as a newer one.
//     The inode catches the rename-over-the-top writers (most editors,
//     git checkout) even when size and mtime collide.
//
//  2. A CONTENT SUM (sha256 of the raw bytes, as read or as written),
//     consulted only when the stamp differs. A `touch`, a `git checkout`
//     that rewrote identical bytes, or an NFS server that adjusted the
//     mtime on close all change the stamp without changing the file, and
//     raising a clobber conflict for those would teach people to click
//     through the prompt. When the bytes match, the new stamp is adopted
//     quietly and nothing happened.
//
//	stat ──► stamp same? ──yes──► unchanged
//	            │ no
//	            ▼
//	         sum known? ──no──► CHANGED
//	            │ yes
//	            ▼
//	         read + hash ──same──► adopt stamp, unchanged
//	            │ differs
//	            ▼
//	         CHANGED
//
// The read in the middle happens only on a stamp mismatch, which is rare,
// and the file is already bounded by MaxOpenBytes.
//
// One case slips past both layers as drawn: a SAME-SIZE rewrite in place
// (same inode) inside the same timestamp granule as our own read or write
// — the coarse-clock NFS / sshfs case — leaves the stamp identical. git
// calls such an index entry "racily clean" and answers it the same way
// this file does: while the recorded mtime is within racyGranule of the
// moment we recorded it, a matching stamp proves nothing, so the content
// is hashed anyway. Each passing check re-dates the record, and once the
// mtime is a granule older than that, any later write must move the mtime
// and the stamp alone suffices again — so the extra reads stop after the
// first tick or two.

package editor

import (
	"crypto/sha256"
	"io/fs"
	"os"
	"reflect"
	"time"
)

// DiskStamp identifies one version of a file by what a stat reports
// about it. The zero value means "no version recorded" — the tab never
// read or wrote the file (a new path), or the record was deliberately
// dropped (ForgetDiskStamp).
type DiskStamp struct {
	ModTime time.Time
	Size    int64
	// Ino is the inode number, or 0 where the platform's stat has none
	// (Same then ignores it rather than calling every file changed).
	Ino uint64
}

// StampOf records the identifying fields of info.
func StampOf(info fs.FileInfo) DiskStamp {
	if info == nil {
		return DiskStamp{}
	}
	return DiskStamp{ModTime: info.ModTime(), Size: info.Size(), Ino: inodeOf(info)}
}

// IsZero reports whether no version is recorded.
func (s DiskStamp) IsZero() bool {
	return s.ModTime.IsZero() && s.Size == 0 && s.Ino == 0
}

// Same reports whether s and o describe the same file version. Equal, not
// After: an mtime that moved BACKWARDS is a different file too. The inode
// is compared only when both sides have one.
func (s DiskStamp) Same(o DiskStamp) bool {
	if !s.ModTime.Equal(o.ModTime) || s.Size != o.Size {
		return false
	}
	if s.Ino != 0 && o.Ino != 0 && s.Ino != o.Ino {
		return false
	}
	return true
}

// inodeOf reads the inode number out of info.Sys() without naming
// syscall.Stat_t, so this package still compiles where that type does not
// exist. Reflection rather than build-tagged files: it is one field, read
// once per stat, and a platform without an `Ino` field simply answers 0,
// which Same treats as "not compared".
func inodeOf(info fs.FileInfo) uint64 {
	sys := info.Sys()
	if sys == nil {
		return 0
	}
	v := reflect.ValueOf(sys)
	if v.Kind() == reflect.Pointer {
		if v.IsNil() {
			return 0
		}
		v = v.Elem()
	}
	if v.Kind() != reflect.Struct {
		return 0
	}
	f := v.FieldByName("Ino")
	if !f.IsValid() || !f.CanUint() {
		return 0
	}
	return f.Uint()
}

// racyGranule is the widest timestamp granularity ced expects to meet:
// FAT keeps 2s, NFSv3 and HFS+ 1s. A recorded mtime closer than this to
// the moment it was recorded can hide a second write in the same tick of
// the filesystem clock.
const racyGranule = 2 * time.Second

// diskNow and diskReadFile are the clock and the file reader behind the
// checks, package vars so tests can pin time and count reads. Nothing
// outside tests may reassign them.
var (
	diskNow      = time.Now
	diskReadFile = os.ReadFile
)

// sumOf is the content fingerprint: sha256 over the raw bytes exactly as
// they sit on disk (BOM and CRLF included), so the sum of a read and the
// sum of a write of the same file agree.
func sumOf(data []byte) [32]byte {
	return sha256.Sum256(data)
}

// recordDisk notes the version of the file this tab now agrees with: the
// stamp from a stat and the sum of the raw bytes read or written. Mtime is
// kept as the exported mirror of the stamp's time for its many readers;
// nothing outside this file should write it.
func (t *Tab) recordDisk(stamp DiskStamp, sum [32]byte) {
	t.diskStamp = stamp
	t.Mtime = stamp.ModTime
	t.diskSum = sum
	t.diskSumOK = true
	t.diskSeenAt = diskNow()
}

// racy reports whether the recorded stamp is too young to vouch for the
// content: the file's mtime is within racyGranule of when the tab last
// confirmed it, so a second write in the same filesystem tick would look
// identical. See the file comment.
func (t *Tab) racy() bool {
	return !t.diskStamp.ModTime.Before(t.diskSeenAt.Add(-racyGranule))
}

// recordDiskStampOnly notes a stamp with no content sum (image tabs, which
// are decoded rather than read as text). A later mismatch then counts as
// changed without a second look — the old mtime-only behaviour, which is
// harmless for a read-only preview.
func (t *Tab) recordDiskStampOnly(stamp DiskStamp) {
	t.diskStamp = stamp
	t.Mtime = stamp.ModTime
	t.diskSumOK = false
}

// DiskStamp returns the recorded version of the file, zero when none.
func (t *Tab) DiskStamp() DiskStamp {
	return t.diskStamp
}

// ForgetDiskStamp drops the recorded version so the next check reports the
// file as changed. The reconcile tick uses it when a deleted file comes
// back: whatever reappeared is news, even if it happens to match.
func (t *Tab) ForgetDiskStamp() {
	t.diskStamp = DiskStamp{}
	t.Mtime = time.Time{}
	t.diskSumOK = false
}

// DiskChanged reports whether the file described by info is a different
// version from the one this tab last read or wrote. See the file comment
// for the two layers. A zero recorded stamp answers true — there is no
// version to be "the same as" — and callers that treat a never-read file
// as "nothing to clobber" (the save guards) check DiskStamp().IsZero()
// first.
//
// When the stamp differs but the bytes hash the same, the new stamp is
// adopted before returning false, so the next tick is a plain stat again
// instead of a re-read per tick. A read error counts as changed: if the
// file cannot be read, it cannot be proven the same.
func (t *Tab) DiskChanged(info fs.FileInfo) bool {
	now := StampOf(info)
	if t.diskStamp.IsZero() {
		return true
	}
	if t.diskStamp.Same(now) {
		if !t.racy() || !t.diskSumOK || t.Path == "" {
			return false
		}
		// Racily clean: the stamp matches but is too young to prove the
		// bytes do. A failed read leaves the stamp's answer standing —
		// it said "same", and the next tick asks again.
		data, err := diskReadFile(t.Path)
		if err != nil {
			return false
		}
		if sumOf(data) != t.diskSum {
			return true
		}
		t.diskSeenAt = diskNow() // confirmed now; ages out of racy.
		return false
	}
	if !t.diskSumOK || t.Path == "" {
		return true
	}
	data, err := diskReadFile(t.Path)
	if err != nil || sumOf(data) != t.diskSum {
		return true
	}
	t.recordDisk(now, t.diskSum)
	return false
}

// AdoptDisk records the file as it stands now as the version this tab
// agrees with, WITHOUT touching the buffer. It is for the moments ced
// knowingly accepts a disk state it did not load: its own formatter wrote
// under a dirty buffer, the user answered "Keep mine" to someone else's
// write, or the file was renamed. Stat-then-read, as the open path does,
// so a write racing it reads as changed on the next tick, never as "seen".
func (t *Tab) AdoptDisk() error {
	if t.Path == "" {
		return nil
	}
	info, err := os.Stat(t.Path)
	if err != nil {
		return err
	}
	if t.IsImage() {
		t.recordDiskStampOnly(StampOf(info))
		return nil
	}
	data, err := diskReadFile(t.Path)
	if err != nil {
		return err
	}
	t.recordDisk(StampOf(info), sumOf(data))
	return nil
}
