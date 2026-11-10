// =============================================================================
// File: internal/editor/fileio.go
// Author: Rohan Allison <rohanthewiz@gmail.com>
// Created: 2026-08-03
// Copyright: 2026 Rohan Allison. All rights reserved.
// =============================================================================

// fileio.go is everything that happens at the two edges of a Tab's life:
// the bytes coming off disk and the bytes going back. Three concerns,
// grouped because they're the same question asked twice — "is this file
// something we can faithfully round-trip, and did we round-trip it?"
//
//  1. WHAT WE AGREE TO OPEN. NewTab used to os.ReadFile anything the
//     user clicked. In a mouse-first file tree that means one misclick on
//     a vendored .so or a 300MB log read the whole thing into a
//     string-per-line buffer, handed it to Chroma, and then kept up to
//     500 undo snapshots of it. Both limits below refuse instead, with a
//     message that says which limit and how big the file was.
//
//  2. LINE ENDINGS. NewBuffer splits on "\n" only, so every line of a
//     CRLF file used to keep a trailing "\r": a stray cell at end of
//     line, End landing one column past the text, and — worst — a save
//     that wrote the old lines back with CRLF and every newly typed line
//     with a bare LF, silently converting the file to mixed endings.
//     The ending (and a UTF-8 BOM, the same problem in miniature) is now
//     detected on load, stripped from the buffer, and restored on write.
//
//  3. HOW WE WRITE. os.WriteFile truncates in place. A full disk, a
//     killed process, or a stalled network mount partway through left a
//     truncated file — and the undo history that could have rebuilt it
//     died with the process. Writes now land on a temp file in the same
//     directory and are renamed over the target, which is atomic on every
//     filesystem ced runs on: readers see either the old file or the new
//     one, never a half-written one.
package editor

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"syscall"
)

// MaxOpenBytes is the largest file NewTab will read. The ceiling isn't
// the read — it's everything downstream: one Go string per line, a
// per-rune style grid, and an undo stack that snapshots the whole line
// slice. 32MB of source is already far past what this editor is for, and
// refusing with a clear message beats wedging on a file nobody meant to
// open.
const MaxOpenBytes = 32 << 20

// binarySniffBytes is how much of the head is searched for a NUL. Every
// format worth refusing puts one well inside its header, and a text file
// legitimately containing one is not something this editor could
// round-trip anyway.
const binarySniffBytes = 8 << 10

// lineEndingCRLF / lineEndingLF are the two endings a tab can carry.
// Classic Mac CR-only files are deliberately not detected: they've been
// gone for two decades, and guessing wrong would join a whole file into
// one line.
const (
	lineEndingLF   = "\n"
	lineEndingCRLF = "\r\n"
)

// utf8BOM is the byte-order mark Windows tools prepend. It is not text —
// left in the buffer it renders as a stray glyph on line 1 and moves
// every column on that line by one.
var utf8BOM = []byte{0xEF, 0xBB, 0xBF}

// diskRead is what one guarded read of a file reports: the decoded text,
// whether it carried a BOM, the dominant line ending, and the identity of
// the version read — the stamp from the stat taken BEFORE the read and the
// sum of the raw bytes (diskstamp.go). exists is false for a missing path,
// which NewTab treats as a new, empty file.
type diskRead struct {
	text   string
	bom    bool
	ending string
	stamp  DiskStamp
	sum    [32]byte
	exists bool
}

// readTextFile reads path and reports the decoded text, whether it
// carried a BOM, which line ending dominated, and the stamp + sum that
// identify the version read. Errors from the guards are phrased for a
// status-bar flash, since that's where they land.
//
// A missing file is NOT an error: NewTab's contract is that opening a
// path that doesn't exist yet gives you an empty buffer to save into.
//
// The stat comes FIRST, for two reasons: the size guard has to refuse
// before the bytes are in memory, and the stamp it yields must not be
// NEWER than the bytes read — if a write races the read, the recorded
// stamp is then the older one and the next check sees a change.
func readTextFile(path string) (diskRead, error) {
	none := diskRead{ending: lineEndingLF}
	info, statErr := os.Stat(path)
	if statErr != nil {
		if os.IsNotExist(statErr) {
			return none, nil
		}
		return none, statErr
	}
	// Checked BEFORE the read, which is the entire point — by the time a
	// 300MB file is in memory the damage is done.
	if info.Size() > MaxOpenBytes {
		return none, fmt.Errorf("%s is %s — too large to open (limit %s)",
			filepath.Base(path), humanBytes(info.Size()), humanBytes(MaxOpenBytes))
	}

	data, readErr := os.ReadFile(path)
	if readErr != nil {
		if os.IsNotExist(readErr) {
			return none, nil
		}
		return none, readErr
	}
	if looksBinary(data) {
		return none, fmt.Errorf("%s looks like a binary file", filepath.Base(path))
	}
	r := diskRead{stamp: StampOf(info), sum: sumOf(data), exists: true}

	if bytes.HasPrefix(data, utf8BOM) {
		data, r.bom = data[len(utf8BOM):], true
	}
	r.ending = detectLineEnding(data)
	if r.ending == lineEndingCRLF {
		// Normalised for the WHOLE buffer, not just the majority of lines:
		// a file's ending is a property of the file, and re-emitting it
		// uniformly on save is what stops a mixed-ending file from getting
		// more mixed every time it's edited.
		data = bytes.ReplaceAll(data, []byte(lineEndingCRLF), []byte(lineEndingLF))
	}
	r.text = string(data)
	return r, nil
}

// looksBinary reports whether data's head contains a NUL byte. That
// single test catches executables, archives, images, and UTF-16 (whose
// ASCII range is half NULs) without a content-type table — and UTF-16 is
// a file ced genuinely cannot round-trip, so refusing it is right rather
// than merely convenient.
func looksBinary(data []byte) bool {
	return bytes.IndexByte(data[:min(len(data), binarySniffBytes)], 0) >= 0
}

// detectLineEnding returns the ending that dominates data. The comparison
// is against HALF the newlines rather than a strict majority so a file
// that is CRLF apart from a couple of stray LF lines still round-trips as
// CRLF, which is what its author and its VCS expect.
func detectLineEnding(data []byte) string {
	total := bytes.Count(data, []byte(lineEndingLF))
	if total == 0 {
		return lineEndingLF
	}
	if crlf := bytes.Count(data, []byte(lineEndingCRLF)); crlf*2 >= total {
		return lineEndingCRLF
	}
	return lineEndingLF
}

// encode serialises the buffer back into the byte form the file had when
// it was opened: BOM if it had one, and its own line ending. Without this
// every save of a CRLF file would rewrite it as LF and show the whole
// file as changed in git.
func (t *Tab) encode() []byte {
	text := t.Buffer.String()
	if t.LineEnding == lineEndingCRLF {
		text = strings.ReplaceAll(text, lineEndingLF, lineEndingCRLF)
	}
	if !t.BOM {
		return []byte(text)
	}
	return append(append([]byte(nil), utf8BOM...), text...)
}

// EncodedBytes is the buffer exactly as a save would write it — encode
// with the file's own BOM and line ending — WITHOUT writing it.
//
// Exported for the app's file copy (app/copypaste.go): copying a file
// whose tab holds unsaved edits copies what the user is looking at, and
// a second serialiser out there would be the one that forgot CRLF. The
// slice is freshly allocated, so it can be handed to another goroutine
// while the buffer keeps changing.
func (t *Tab) EncodedBytes() []byte {
	return t.encode()
}

// WriteFileAtomic writes data to path via a temp file in the same
// directory plus a rename, so a crash or a full disk can never leave a
// half-written file where the user's work used to be.
//
// Exported because Tab.Save is no longer the only writer that owes the
// user these guarantees: the in-process formatter (app/format.go)
// rewrites a file ced has just saved, and a second implementation of
// this would be the one that quietly forgot the symlink resolution or
// the mode copy. One writer, several callers — the wireTab rule.
//
// Three details that are load-bearing:
//
//   - The temp file is created in the TARGET's directory. Rename is only
//     atomic within a filesystem, so /tmp is not an option.
//   - Symlinks are resolved first. Renaming onto a symlink would replace
//     the LINK with a regular file, which quietly breaks the dotfile
//     layout half this editor's users have.
//   - Mode is copied from the existing file. os.WriteFile's perm argument
//     only applies at creation, so the old in-place write preserved mode
//     for free; a fresh temp file does not, and losing the +x on a script
//     you just edited is a nasty surprise to hand someone.
//
// If the directory won't take a temp file BECAUSE OF PERMISSIONS (a
// writable file inside a read-only directory is the usual cause), it
// falls back to an in-place write: degrading to the old behavior beats
// refusing to save. ONLY then — any other CreateTemp failure (a full disk,
// out of inodes, an I/O error) is returned with the original untouched.
// The fallback used to fire on ANY error and truncate first, so a full
// disk turned "can't make a temp file" into an EMPTY target, on every
// dirty tab auto-save touched (N-115). See writeInPlace for how the
// fallback itself avoids the truncate-first trap.
//
// Not preserved: hard links (rename breaks them) and ownership when
// running as root. Both are the standard trade for atomicity, and both
// are what vim does by default.
func WriteFileAtomic(path string, data []byte) error {
	_, err := writeFileAtomic(path, data)
	return err
}

// writeFileAtomic is WriteFileAtomic that also reports the written file's
// stat, taken on the open handle after the data is synced — so it describes
// OUR bytes, and Tab.Save can record it as the version on disk without a
// second os.Stat that another writer could slip in front of. A rename
// keeps mtime, size and inode, so the temp file's stat is the target's.
func writeFileAtomic(path string, data []byte) (os.FileInfo, error) {
	if resolved, err := filepath.EvalSymlinks(path); err == nil {
		path = resolved
	}
	perm := os.FileMode(0o644)
	if info, err := os.Stat(path); err == nil {
		perm = info.Mode().Perm()
	}

	dir, base := filepath.Dir(path), filepath.Base(path)
	tmp, err := createTemp(dir, "."+base+".ced-*")
	if err != nil {
		if tempRefusedByPermission(err) {
			return writeInPlace(path, data, perm)
		}
		return nil, err
	}
	tmpName := tmp.Name()
	// Any failure from here on leaves the ORIGINAL untouched, which is the
	// whole point; the temp file is what gets cleaned up.
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		os.Remove(tmpName)
		return nil, err
	}
	// Flushed before the rename so a power loss can't publish a name that
	// points at unwritten blocks.
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		os.Remove(tmpName)
		return nil, err
	}
	info, err := tmp.Stat()
	if err != nil {
		tmp.Close()
		os.Remove(tmpName)
		return nil, err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpName)
		return nil, err
	}
	if err := os.Chmod(tmpName, perm); err != nil {
		os.Remove(tmpName)
		return nil, err
	}
	if err := os.Rename(tmpName, path); err != nil {
		os.Remove(tmpName)
		return nil, err
	}
	return info, nil
}

// createTemp is os.CreateTemp behind a package var so a test can make the
// temp file fail the way a full disk does (ENOSPC) without filling a real
// volume. Nothing outside tests may reassign it.
var createTemp = os.CreateTemp

// tempRefusedByPermission reports whether a CreateTemp failure means "this
// DIRECTORY won't let us add a name" — the one case an in-place write can
// still serve, because writing an existing file needs only the FILE's
// permission. EACCES / EPERM arrive as fs.ErrPermission; EROFS is listed
// for completeness (an in-place write fails there too, with the honest
// error). Everything else, ENOSPC first among them, means the disk itself
// is in trouble, and an in-place write is the one thing that can make it
// worse.
func tempRefusedByPermission(err error) bool {
	return errors.Is(err, fs.ErrPermission) || errors.Is(err, syscall.EROFS)
}

// writeInPlace is the read-only-directory fallback: it overwrites path
// WITHOUT truncating first, then cuts the file to the new length only
// after every byte landed. O_TRUNC-then-write was the bug — if the write
// fails (the disk filled up in between), the file is already empty. In
// this order a write that fails partway leaves the old content past the
// failure point instead of nothing, and the error reaches the caller, so
// the tab stays dirty with the full text still in the buffer.
//
// Not atomic, and it can't be: atomicity needs a second name in the
// directory, which is exactly what the directory just refused.
func writeInPlace(path string, data []byte, perm os.FileMode) (os.FileInfo, error) {
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE, perm)
	if err != nil {
		return nil, err
	}
	if _, err := f.WriteAt(data, 0); err != nil {
		f.Close()
		return nil, err
	}
	if err := f.Truncate(int64(len(data))); err != nil {
		f.Close()
		return nil, err
	}
	if err := f.Sync(); err != nil {
		f.Close()
		return nil, err
	}
	// Same reason as the atomic path: describe OUR bytes, from the handle.
	info, err := f.Stat()
	if err != nil {
		f.Close()
		return nil, err
	}
	return info, f.Close()
}

// humanBytes renders a size the way an error message should read.
func humanBytes(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := int64(unit), 0
	for v := n / unit; v >= unit; v /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %cB", float64(n)/float64(div), "KMGT"[exp])
}
