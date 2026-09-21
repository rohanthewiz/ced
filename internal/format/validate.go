// =============================================================================
// File: internal/format/validate.go
// Author: Rohan Allison <rohanthewiz@gmail.com>
// Created: 2026-09-21
// Copyright: 2026 Rohan Allison. All rights reserved.
// =============================================================================

// validate.go is the syntax-checking half of the built-in data-format
// support: it turns "this file does not parse" into a POSITION the
// editor can point at, which is the only form of that answer worth
// having. A message with no location sends the reader hunting; a
// location is what lets the gutter, the underline and the Problems
// panel all say the same thing about the same line.
//
// House rules:
//
//   - **It reports, it never rewrites.** Validation runs on every edit
//     (debounced, see app/validate.go); formatting runs on save. Keeping
//     them separate is what lets ced complain about a half-typed file
//     without touching it — a validator that also fixed things would be
//     reformatting under the user's cursor.
//   - **Positions are ZERO-based**, the editor's convention, converted
//     here exactly once. Every caller downstream paints with them
//     directly, so a one-based leak would be an off-by-one in the
//     underline that only shows up on the second line of a file.
//   - **Columns are counted in RUNES, not bytes.** encoding/json reports
//     a byte offset, and the editor's Span/Position columns are rune
//     indices. A file with a non-ASCII string above the error — a name,
//     an emoji, any UTF-8 at all — would otherwise underline a cell or
//     three to the right of the real problem, which is a confident wrong
//     answer rather than a missing one.
//   - **One problem, not a list.** encoding/json stops at the first
//     syntax error and cannot meaningfully resume: everything after a
//     missing brace is unparseable in a way that says nothing about the
//     text. Reporting one honest position beats inventing a cascade.

package format

import (
	"bytes"
	"encoding/json"
	"errors"
	"unicode/utf8"
)

// Problem is one syntax error located in a buffer. Line and Col are
// ZERO-based; Col is a RUNE index into the line.
//
// Deliberately a type of this package's own rather than a reuse of
// plugins.Diagnostic: this package has no business importing the plugin
// manifest loader, and the app converts once at the seam where every
// other diagnostic producer is already converted. There is no Severity
// field because a syntax error has only one volume — a file that does
// not parse is broken, not "worth considering".
type Problem struct {
	Line    int
	Col     int
	Message string
}

// Validates reports whether ced has a built-in syntax check for this
// file. Callers use it to decide whether to bother reading the buffer
// at all — the common case is a file ced has nothing to say about, and
// that case should cost one extension comparison.
func Validates(filePath string) bool {
	switch kindFor(filePath) {
	case kindJSON:
		return true
	default:
		// Go is deliberately absent. gopls already reports parse errors
		// with far better messages than a bare go/parser pass, and two
		// producers underlining the same broken line would just argue
		// with each other in the one gutter cell.
		return false
	}
}

// Validate syntax-checks src as whatever language filePath names,
// returning nil when the file is fine — or when ced has no checker for
// it, which is the same answer from the caller's point of view: nothing
// to paint.
//
// src is the BUFFER's bytes, not the file's. Validation follows what is
// on screen, so an unsaved edit is checked as typed; a checker reading
// the disk copy would underline text the user has already fixed.
func Validate(filePath string, src []byte) []Problem {
	switch kindFor(filePath) {
	case kindJSON:
		return validateJSON(src)
	default:
		return nil
	}
}

// validateJSON parses src as strict JSON and locates the first syntax
// error.
//
// The target is json.RawMessage rather than any/interface{} on purpose:
// Unmarshal validates the ENTIRE document before it decodes anything,
// so a RawMessage target gets the full syntax check while allocating
// nothing for the contents. Decoding into a map would additionally cost
// a whole parse tree ced immediately throws away — on every keystroke,
// for a file that might be a megabyte of fixture data.
func validateJSON(src []byte) []Problem {
	// An empty or whitespace-only file is NOT an error here, though
	// encoding/json calls it "unexpected end of JSON input". A file
	// someone has just created, or has selected-all and deleted on the
	// way to retyping, is mid-thought rather than broken — and this runs
	// while they type. Formatting agrees (see formatJSON), so the two
	// halves treat an empty file identically.
	if len(bytes.TrimSpace(src)) == 0 {
		return nil
	}

	var raw json.RawMessage
	err := json.Unmarshal(src, &raw)
	if err == nil {
		return nil
	}

	// Offset is the only positional information encoding/json exposes,
	// and only SyntaxError carries it. A non-syntax error against a
	// RawMessage target should be unreachable, but reporting it at the
	// top of the file beats dropping it: a validator that silently
	// declines to explain a rejection is worse than one pointing at the
	// wrong line, because the user cannot tell it ran at all.
	var syn *json.SyntaxError
	if !errors.As(err, &syn) {
		return []Problem{{Line: 0, Col: 0, Message: err.Error()}}
	}

	// Offset points at the byte just AFTER the offending one — the
	// parser consumed the bad character and then complained — so the
	// character to underline is at Offset-1. Verified across every
	// error shape encoding/json produces: a stray token, a character
	// in the wrong place, and the unterminated-document case, where
	// Offset is len(src) and Offset-1 is the last byte read.
	//
	// This is the difference between underlining the broken character
	// and underlining the blameless one after it, which on a one-cell
	// mark is the whole value of the feature.
	line, col := offsetToLineCol(src, int(syn.Offset)-1)
	return []Problem{{Line: line, Col: col, Message: syn.Error()}}
}

// offsetToLineCol converts a BYTE offset into src to a zero-based line
// and a zero-based RUNE column, clamping an out-of-range offset rather
// than reporting a position outside the buffer.
func offsetToLineCol(src []byte, off int) (line, col int) {
	if off > len(src) {
		off = len(src)
	}
	if off < 0 {
		off = 0
	}
	// The line is how many newlines precede the offset; the column is
	// the rune count of whatever follows the last of them.
	nl := bytes.LastIndexByte(src[:off], '\n')
	line = bytes.Count(src[:off], []byte{'\n'})
	return line, utf8.RuneCount(src[nl+1 : off])
}
