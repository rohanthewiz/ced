// =============================================================================
// File: internal/editor/encoding.go
// Author: Rohan Allison <rohanthewiz@gmail.com>
// Created: 2026-11-10
// Copyright: 2026 Rohan Allison. All rights reserved.
// =============================================================================

// encoding.go is how ced holds a file whose bytes are not UTF-8 without
// damaging it (N-114).
//
// THE BUG. The buffer primitives work in []rune (buffer.go), and Go turns
// every byte that is not part of a valid UTF-8 sequence into U+FFFD on
// that conversion. So a Latin-1 file opened as-is looked fine until the
// first keystroke on a line, which rewrote every high byte on THAT line
// as the three bytes EF BF BD — and auto-save persisted it five seconds
// later. Untouched lines round-tripped, which is what kept it hidden.
//
// THE RULE. readTextFile asks utf8.Valid once. Valid → the bytes ARE the
// text, exactly as before. Not valid → the file is decoded as
// Windows-1252 and Tab.Encoding remembers it, so every save re-encodes.
// The buffer therefore always holds valid UTF-8, and nothing above this
// layer — the rune primitives, Chroma, LSP, find — ever sees a stray byte.
// This is Vim's model (`fileencodings=…,latin1`).
//
// WHY WINDOWS-1252 IS SAFE FOR ANY BYTES. Its decode table (WHATWG's,
// below) maps all 256 byte values to 256 DIFFERENT runes — the five bytes
// the code page leaves undefined become the C1 controls of the same
// number. A one-to-one table means decode-then-encode is the identity on
// every byte string, so the guarantee does not depend on guessing the
// encoding right: Shift-JIS, BOM-less UTF-16 without a NUL in the head,
// or a mostly-UTF-8 file with one stray Latin-1 byte all display as
// mojibake, but none of them can be changed by opening, editing other
// lines, and saving. Latin-1 and Windows-1252 — the files that actually
// turn up in repositories (old source comments, Java .properties,
// spreadsheet exports) — display correctly.
//
//	bytes ──utf8.Valid?──yes──► text as-is ─────────────► Encoding ""
//	           │
//	           no──► decodeCP1252 (1 byte → 1 rune) ──────► Encoding "cp1252"
//
//	save: Encoding "" → []byte(text)
//	      "cp1252"    → encodeCP1252 ─ every rune in the table? ─ yes → bytes
//	                                                            └ no → *UnencodableError
//
// THE ONE WAY TO LOSE. Typing a rune the code page has no byte for (→,
// CJK, emoji). encode refuses with an *UnencodableError naming the rune
// and its line, Save returns it untouched, and the app turns it into the
// "Convert to UTF-8 and save?" question (app/encoding.go). Nothing is
// ever written as a substitute character.
//
// What this file does NOT do: guess other encodings. A "Reopen with
// encoding…" picker (Shift-JIS, UTF-16, …) would sit on top of the same
// Tab.Encoding field; it is not built.
package editor

import (
	"fmt"
	"strings"
	"unicode/utf8"
)

// EncodingCP1252 is Tab.Encoding for a file read as Windows-1252 because
// its bytes were not valid UTF-8. The empty string means UTF-8, the
// default and the only encoding ced writes for a file it creates.
const EncodingCP1252 = "cp1252"

// cp1252High is the WHATWG index for bytes 0x80–0x9F, the only range
// where Windows-1252 differs from Latin-1 (0xA0–0xFF are U+00A0–U+00FF,
// and 0x00–0x7F are ASCII). The five undefined bytes — 0x81, 0x8D,
// 0x8F, 0x90, 0x9D — map to the C1 control of the same number, which is
// what keeps the table one-to-one and therefore lossless.
var cp1252High = [32]rune{
	0x20AC, 0x0081, 0x201A, 0x0192, 0x201E, 0x2026, 0x2020, 0x2021, // 80–87
	0x02C6, 0x2030, 0x0160, 0x2039, 0x0152, 0x008D, 0x017D, 0x008F, // 88–8F
	0x0090, 0x2018, 0x2019, 0x201C, 0x201D, 0x2022, 0x2013, 0x2014, // 90–97
	0x02DC, 0x2122, 0x0161, 0x203A, 0x0153, 0x009D, 0x017E, 0x0178, // 98–9F
}

// cp1252Back is cp1252High inverted: the rune → byte map encode needs for
// the 32 runes that do not sit at their own code point. Built once from
// the table so the two directions can never disagree.
var cp1252Back = func() map[rune]byte {
	m := make(map[rune]byte, len(cp1252High))
	for i, r := range cp1252High {
		m[r] = byte(0x80 + i)
	}
	return m
}()

// cp1252Byte returns the Windows-1252 byte for r, and false when the code
// page has none.
//
// U+0080–U+009F need care: only the five C1 runes the table itself uses
// are encodable. U+0080 is NOT byte 0x80 — that byte is €.
func cp1252Byte(r rune) (byte, bool) {
	switch {
	case r < 0x80:
		return byte(r), true
	case r >= 0xA0 && r <= 0xFF:
		return byte(r), true
	}
	b, ok := cp1252Back[r]
	return b, ok
}

// decodeCP1252 turns Windows-1252 bytes into text, one rune per byte.
// Total: there is no input it refuses.
func decodeCP1252(data []byte) string {
	var sb strings.Builder
	// Most bytes of a mostly-ASCII file stay one byte; the high ones grow
	// to two or three. An eighth extra is a guess that avoids most regrows.
	sb.Grow(len(data) + len(data)/8)
	for _, c := range data {
		switch {
		case c < 0x80:
			sb.WriteByte(c)
		case c < 0xA0:
			sb.WriteRune(cp1252High[c-0x80])
		default:
			sb.WriteRune(rune(c))
		}
	}
	return sb.String()
}

// encodeCP1252 is decodeCP1252's inverse. The first rune the code page
// cannot hold stops it with an *UnencodableError carrying the rune's
// line and column (0-based, rune columns), counted here because the
// caller hands over the whole text and the position is what makes the
// refusal actionable.
//
// Invalid UTF-8 in s (which a buffer should never hold, but a raw byte
// pushed in from outside could) ranges as U+FFFD, which the code page
// has no byte for — so it is refused rather than written as a guess.
func encodeCP1252(s string) ([]byte, *UnencodableError) {
	out := make([]byte, 0, len(s))
	line, col := 0, 0
	for _, r := range s {
		b, ok := cp1252Byte(r)
		if !ok {
			return nil, &UnencodableError{Encoding: EncodingCP1252, Rune: r, Line: line, Col: col}
		}
		out = append(out, b)
		if r == '\n' {
			line, col = line+1, 0
		} else {
			col++
		}
	}
	return out, nil
}

// UnencodableError is a save refused because the buffer holds a rune the
// file's encoding has no bytes for. Line and Col are 0-based (Col in
// runes); Error spells them 1-based for the flash.
type UnencodableError struct {
	Encoding string
	Rune     rune
	Line     int
	Col      int
}

// Error is phrased for a status-bar flash, because every caller that is
// not the explicit save (the formatter's re-save, staging, a workspace
// edit's detached save) lands it there — so it names the way out too.
func (e *UnencodableError) Error() string {
	return fmt.Sprintf("line %d has %s, which %s can't hold — convert the file to UTF-8 (≡ File) to save it",
		e.Line+1, RuneLabel(e.Rune), EncodingLabel(e.Encoding))
}

// EncodingLabel is the name a person knows an encoding by.
func EncodingLabel(enc string) string {
	switch enc {
	case EncodingCP1252:
		return "Windows-1252"
	case "":
		return "UTF-8"
	}
	return enc
}

// RuneLabel spells a rune for a message: the glyph in quotes plus its code
// point, or the code point alone when the glyph would not show (controls,
// U+FFFD, which reads as "corrupt" rather than as itself).
func RuneLabel(r rune) string {
	if IsControlRune(r) || r == utf8.RuneError {
		return fmt.Sprintf("U+%04X", r)
	}
	return fmt.Sprintf("'%c' (U+%04X)", r, r)
}

// DecodeBytes is the open path's decision, exported for the app's other
// readers of files a tab could hold (the compare panel's disk side, the
// agent's reads, reference context): the text ced shows for these bytes
// and the encoding it read them in. One rule, so a file compared against
// its own tab never differs on every non-ASCII line.
//
// It does NOT strip a BOM or normalise line endings — readTextFile does
// those first and then asks this; the other readers never did either.
func DecodeBytes(data []byte) (text, enc string) {
	if utf8.Valid(data) {
		return string(data), ""
	}
	return decodeCP1252(data), EncodingCP1252
}

// DecodeAs decodes bytes known to be in enc — for text that comes from
// git about a file whose tab already decided (the gutter diff's hunk
// lines). Re-detecting per line would be wrong: in a Windows-1252 file
// the bytes C3 A9 are "Ã©", even though they happen to be valid UTF-8.
func DecodeAs(enc string, data []byte) string {
	if enc == EncodingCP1252 {
		return decodeCP1252(data)
	}
	return string(data)
}

// EncodeAs encodes text into enc. The error, when there is one, is an
// *UnencodableError.
func EncodeAs(enc, text string) ([]byte, error) {
	if enc != EncodingCP1252 {
		return []byte(text), nil
	}
	b, uerr := encodeCP1252(text)
	if uerr != nil {
		// Returned as a plain nil error interface on success — a typed nil
		// *UnencodableError in an error would read as non-nil.
		return nil, uerr
	}
	return b, nil
}

// EncodeLossy encodes text into enc with '?' for every rune the encoding
// cannot hold. ONLY for text handed to a tool that compares lines (git
// blame's --contents): such a line differs from history anyway, and a
// refusal would cost the annotations of every OTHER line. Never for a
// write.
func EncodeLossy(enc, text string) []byte {
	if enc != EncodingCP1252 {
		return []byte(text)
	}
	out := make([]byte, 0, len(text))
	for _, r := range text {
		b, ok := cp1252Byte(r)
		if !ok {
			b = '?'
		}
		out = append(out, b)
	}
	return out
}

// firstUnencodable scans lines for the first rune enc cannot hold. Lines
// rather than one joined string so the scan allocates nothing — it backs
// Tab.Unencodable, which the status bar asks every frame.
func firstUnencodable(enc string, lines []string) *UnencodableError {
	if enc != EncodingCP1252 {
		return nil
	}
	for li, ln := range lines {
		col := 0
		for _, r := range ln {
			if _, ok := cp1252Byte(r); !ok {
				return &UnencodableError{Encoding: enc, Rune: r, Line: li, Col: col}
			}
			col++
		}
	}
	return nil
}

// Unencodable reports the first rune in the buffer the file's encoding
// cannot hold, or nil. Always nil for UTF-8. Memoised on (EditRev,
// Encoding): the status bar asks every frame, and the answer only moves
// when the text or the encoding does.
func (t *Tab) Unencodable() *UnencodableError {
	if t.Encoding == "" || t.Buffer == nil {
		return nil
	}
	if t.unencKnown && t.unencRev == t.EditRev && t.unencEnc == t.Encoding {
		return t.unencErr
	}
	t.unencErr = firstUnencodable(t.Encoding, t.Buffer.Lines)
	t.unencRev, t.unencEnc, t.unencKnown = t.EditRev, t.Encoding, true
	return t.unencErr
}

// ConvertToUTF8 switches the tab to writing UTF-8. The TEXT does not
// change — only the bytes the next save writes — so it is not an undo
// step and bumps no EditRev. Reports false when the tab already writes
// UTF-8.
//
// The tab goes dirty and its saved baseline is forgotten
// (ForgetSavedText): the disk still holds Windows-1252 bytes, so no
// buffer state — including one Undo reaches — is "what's on disk" until
// the next save writes the UTF-8 form. Going back is a reload from disk.
func (t *Tab) ConvertToUTF8() bool {
	if t.Encoding == "" || t.IsImage() {
		return false
	}
	t.Encoding = ""
	t.Dirty = true
	t.ForgetSavedText()
	return true
}

// IsControlRune reports whether r is a C0 control other than tab, DEL,
// or a C1 control — the runes the renderer draws a stand-in for.
func IsControlRune(r rune) bool {
	return (r < 0x20 && r != '\t') || (r >= 0x7F && r <= 0x9F)
}

// controlStandIn is the glyph Render paints for a control rune, in its one
// cell (the layout already gives every non-tab rune one cell, indent.go).
//
// Painting the raw rune was never right: tcell measures a control as zero
// cells, and several of the five C1 runes the Windows-1252 table produces
// are terminal introducers in their 8-bit form (U+0090 DCS, U+009D OSC).
// C0 gets its Unicode Control Picture (␛ for ESC, ␍ for a stray CR,
// ␌ for a form feed) — the name is the useful part. DEL and C1 have no
// pictures, so they share ▯ (U+25AF): East-Asian-width Neutral, so one
// cell in every terminal, and not itself a Windows-1252 character, so it
// can't be mistaken for one in exactly the files that produce it.
func controlStandIn(r rune) rune {
	if r < 0x20 {
		return 0x2400 + r
	}
	return '▯'
}
