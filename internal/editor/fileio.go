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
	"fmt"
	"os"
	"path/filepath"
	"strings"
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

// readTextFile reads path and reports the decoded text, whether it
// carried a BOM, and which line ending dominated. Errors from the guards
// are phrased for a status-bar flash, since that's where they land.
//
// A missing file is NOT an error: NewTab's contract is that opening a
// path that doesn't exist yet gives you an empty buffer to save into.
func readTextFile(path string) (text string, bom bool, ending string, err error) {
	info, statErr := os.Stat(path)
	if statErr != nil {
		if os.IsNotExist(statErr) {
			return "", false, lineEndingLF, nil
		}
		return "", false, lineEndingLF, statErr
	}
	// Checked BEFORE the read, which is the entire point — by the time a
	// 300MB file is in memory the damage is done.
	if info.Size() > MaxOpenBytes {
		return "", false, lineEndingLF, fmt.Errorf("%s is %s — too large to open (limit %s)",
			filepath.Base(path), humanBytes(info.Size()), humanBytes(MaxOpenBytes))
	}

	data, readErr := os.ReadFile(path)
	if readErr != nil {
		if os.IsNotExist(readErr) {
			return "", false, lineEndingLF, nil
		}
		return "", false, lineEndingLF, readErr
	}
	if looksBinary(data) {
		return "", false, lineEndingLF, fmt.Errorf("%s looks like a binary file", filepath.Base(path))
	}

	if bytes.HasPrefix(data, utf8BOM) {
		data, bom = data[len(utf8BOM):], true
	}
	ending = detectLineEnding(data)
	if ending == lineEndingCRLF {
		// Normalised for the WHOLE buffer, not just the majority of lines:
		// a file's ending is a property of the file, and re-emitting it
		// uniformly on save is what stops a mixed-ending file from getting
		// more mixed every time it's edited.
		data = bytes.ReplaceAll(data, []byte(lineEndingCRLF), []byte(lineEndingLF))
	}
	return string(data), bom, ending, nil
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

// writeFileAtomic writes data to path via a temp file in the same
// directory plus a rename, so a crash or a full disk can never leave a
// half-written file where the user's work used to be.
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
// If the directory won't take a temp file (a writable file inside a
// read-only directory is the usual cause), it falls back to a plain
// in-place write: degrading to the old behavior beats refusing to save.
//
// Not preserved: hard links (rename breaks them) and ownership when
// running as root. Both are the standard trade for atomicity, and both
// are what vim does by default.
func writeFileAtomic(path string, data []byte) error {
	if resolved, err := filepath.EvalSymlinks(path); err == nil {
		path = resolved
	}
	perm := os.FileMode(0o644)
	if info, err := os.Stat(path); err == nil {
		perm = info.Mode().Perm()
	}

	dir, base := filepath.Dir(path), filepath.Base(path)
	tmp, err := os.CreateTemp(dir, "."+base+".ced-*")
	if err != nil {
		return os.WriteFile(path, data, perm)
	}
	tmpName := tmp.Name()
	// Any failure from here on leaves the ORIGINAL untouched, which is the
	// whole point; the temp file is what gets cleaned up.
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		os.Remove(tmpName)
		return err
	}
	// Flushed before the rename so a power loss can't publish a name that
	// points at unwritten blocks.
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		os.Remove(tmpName)
		return err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpName)
		return err
	}
	if err := os.Chmod(tmpName, perm); err != nil {
		os.Remove(tmpName)
		return err
	}
	if err := os.Rename(tmpName, path); err != nil {
		os.Remove(tmpName)
		return err
	}
	return nil
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
