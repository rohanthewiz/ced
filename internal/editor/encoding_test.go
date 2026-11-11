// =============================================================================
// File: internal/editor/encoding_test.go
// Author: Rohan Allison <rohanthewiz@gmail.com>
// Created: 2026-11-10
// Copyright: 2026 Rohan Allison. All rights reserved.
// =============================================================================

package editor

import (
	"bytes"
	"errors"
	"os"
	"strings"
	"testing"
)

// openTemp writes content to a temp file and opens it as a tab.
func openTemp(t *testing.T, name string, content []byte) (*Tab, string) {
	t.Helper()
	path := writeTemp(t, name, content)
	tab, err := NewTab(path)
	if err != nil {
		t.Fatal(err)
	}
	return tab, path
}

// typeAtEndOfLine moves the caret to the end of line and types s.
func typeAtEndOfLine(tab *Tab, line int, s string) {
	tab.Cursor = Position{Line: line, Col: len([]rune(tab.Buffer.Lines[line]))}
	tab.Anchor = tab.Cursor
	tab.InsertString(s)
}

// TestCP1252_EveryByteRoundTrips pins the property the whole fallback
// rests on: all 256 byte values decode to DIFFERENT runes and encode back
// to themselves, so decode-then-encode is the identity on any bytes.
func TestCP1252_EveryByteRoundTrips(t *testing.T) {
	all := make([]byte, 256)
	for i := range all {
		all[i] = byte(i)
	}
	text := decodeCP1252(all)
	seen := map[rune]bool{}
	for _, r := range text {
		if seen[r] {
			t.Fatalf("rune U+%04X decoded from two different bytes", r)
		}
		seen[r] = true
	}
	if len(seen) != 256 {
		t.Fatalf("decoded %d distinct runes, want 256", len(seen))
	}
	back, err := encodeCP1252(text)
	if err != nil {
		t.Fatalf("encode refused its own decode: %v", err)
	}
	if !bytes.Equal(back, all) {
		t.Fatal("decode → encode is not the identity")
	}
}

// TestCP1252Byte_TableSpotChecks pins the bytes that differ from Latin-1
// and the trap in the C1 range: U+0080 is NOT byte 0x80 (that's €), while
// the five undefined bytes are their own C1 runes.
func TestCP1252Byte_TableSpotChecks(t *testing.T) {
	cases := []struct {
		r    rune
		b    byte
		want bool
	}{
		{'€', 0x80, true},
		{'—', 0x97, true},
		{'é', 0xE9, true},
		{'ÿ', 0xFF, true},
		{0x81, 0x81, true},
		{0x9D, 0x9D, true},
		{0x80, 0, false},
		{0x82, 0, false},
		{'→', 0, false},
		{'世', 0, false},
	}
	for _, c := range cases {
		b, ok := cp1252Byte(c.r)
		if ok != c.want || (ok && b != c.b) {
			t.Errorf("cp1252Byte(U+%04X) = %#x, %v; want %#x, %v", c.r, b, ok, c.b, c.want)
		}
	}
}

// TestNewTab_Latin1OpensAsCP1252 pins the open path: bytes that are not
// UTF-8 come back as their Windows-1252 text, and the tab remembers it.
func TestNewTab_Latin1OpensAsCP1252(t *testing.T) {
	tab, _ := openTemp(t, "fr.txt", []byte("caf\xe9 one\n"))
	if tab.Encoding != EncodingCP1252 {
		t.Fatalf("Encoding = %q, want cp1252", tab.Encoding)
	}
	if got := tab.Buffer.Lines[0]; got != "café one" {
		t.Fatalf("line 0 = %q", got)
	}
}

// TestNewTab_ValidUTF8StaysUTF8 pins the common case untouched: a UTF-8
// file — including one with multibyte runes — keeps an empty Encoding.
func TestNewTab_ValidUTF8StaysUTF8(t *testing.T) {
	tab, _ := openTemp(t, "u.txt", []byte("café → 世\n"))
	if tab.Encoding != "" {
		t.Fatalf("Encoding = %q, want UTF-8", tab.Encoding)
	}
	if tab.Buffer.Lines[0] != "café → 世" {
		t.Fatalf("line 0 = %q", tab.Buffer.Lines[0])
	}
}

// TestSave_EditedLatin1LineKeepsItsBytes is the N-114 reproduction:
// typing on a Latin-1 line used to rewrite its é as EF BF BD.
func TestSave_EditedLatin1LineKeepsItsBytes(t *testing.T) {
	tab, path := openTemp(t, "fr.txt", []byte("caf\xe9 one\n"))
	typeAtEndOfLine(tab, 0, "X")
	if err := tab.Save(); err != nil {
		t.Fatal(err)
	}
	if disk, _ := os.ReadFile(path); string(disk) != "caf\xe9 oneX\n" {
		t.Fatalf("disk = %q, want %q", disk, "caf\xe9 oneX\n")
	}
}

// TestSave_AnyBytesRoundTripAroundAnEdit pins the guarantee for files
// that are NOT Windows-1252 (Shift-JIS, stray high bytes): they show as
// mojibake, but an edit on another line leaves their bytes exactly.
func TestSave_AnyBytesRoundTripAroundAnEdit(t *testing.T) {
	// "日本語" in Shift-JIS, then every non-NUL, non-newline byte.
	var salad []byte
	for b := 1; b < 256; b++ {
		if b != '\n' && b != '\r' {
			salad = append(salad, byte(b))
		}
	}
	orig := append([]byte("\x93\xfa\x96{\x8c\xea\n"), salad...)
	orig = append(orig, "\nascii\n"...)
	tab, path := openTemp(t, "sjis.txt", orig)
	if tab.Encoding != EncodingCP1252 {
		t.Fatalf("Encoding = %q, want cp1252", tab.Encoding)
	}
	typeAtEndOfLine(tab, 2, "!")
	if err := tab.Save(); err != nil {
		t.Fatal(err)
	}
	want := bytes.Replace(orig, []byte("ascii\n"), []byte("ascii!\n"), 1)
	if disk, _ := os.ReadFile(path); !bytes.Equal(disk, want) {
		t.Fatalf("bytes changed:\n got %q\nwant %q", disk, want)
	}
}

// TestSave_CP1252WithBOMAndCRLFRoundTrips pins the layering: the BOM and
// CRLF steps are ASCII and wrap the encoding step, so a file carrying all
// three comes back byte for byte.
func TestSave_CP1252WithBOMAndCRLFRoundTrips(t *testing.T) {
	orig := append(append([]byte(nil), utf8BOM...), "na\xefve\r\nok\r\n"...)
	tab, path := openTemp(t, "w.txt", orig)
	if !tab.BOM || tab.LineEnding != lineEndingCRLF || tab.Encoding != EncodingCP1252 {
		t.Fatalf("BOM %v ending %q encoding %q", tab.BOM, tab.LineEnding, tab.Encoding)
	}
	typeAtEndOfLine(tab, 1, "?")
	if err := tab.Save(); err != nil {
		t.Fatal(err)
	}
	want := string(utf8BOM) + "na\xefve\r\nok?\r\n"
	if disk, _ := os.ReadFile(path); string(disk) != want {
		t.Fatalf("disk = %q, want %q", disk, want)
	}
}

// TestSave_UnencodableRuneRefusesAndWritesNothing pins the one way to
// lose: a rune the code page can't hold refuses the save with its
// position, leaves the disk alone and the tab dirty.
func TestSave_UnencodableRuneRefusesAndWritesNothing(t *testing.T) {
	orig := []byte("caf\xe9\r\nnext\r\n")
	tab, path := openTemp(t, "fr.txt", orig)
	typeAtEndOfLine(tab, 1, " → done")
	err := tab.Save()
	var uerr *UnencodableError
	if !errors.As(err, &uerr) {
		t.Fatalf("Save err = %v, want *UnencodableError", err)
	}
	// Counted in the buffer the user sees (LF text), not the CRLF bytes.
	if uerr.Rune != '→' || uerr.Line != 1 || uerr.Col != 5 {
		t.Fatalf("refusal = %+v, want → at line 1 col 5", *uerr)
	}
	if disk, _ := os.ReadFile(path); !bytes.Equal(disk, orig) {
		t.Fatalf("refused save touched the file: %q", disk)
	}
	if !tab.Dirty {
		t.Fatal("a refused save must leave the tab dirty")
	}
	if _, err := tab.EncodedBytes(); !errors.As(err, &uerr) {
		t.Fatalf("EncodedBytes err = %v, want the same refusal", err)
	}
}

// TestUnencodableError_Message pins the flash wording: 1-based line, the
// rune and its code point, the encoding by name, and the way out.
func TestUnencodableError_Message(t *testing.T) {
	e := &UnencodableError{Encoding: EncodingCP1252, Rune: '→', Line: 11, Col: 3}
	want := "line 12 has '→' (U+2192), which Windows-1252 can't hold — convert the file to UTF-8 (≡ File) to save it"
	if got := e.Error(); got != want {
		t.Fatalf("Error() = %q\nwant      %q", got, want)
	}
}

// TestRuneLabel_ControlsShowOnlyTheCodePoint pins that a glyph that would
// not show (a control, U+FFFD) is named by code point alone.
func TestRuneLabel_ControlsShowOnlyTheCodePoint(t *testing.T) {
	cases := map[rune]string{'é': "'é' (U+00E9)", 0x1B: "U+001B", 0x90: "U+0090", 0xFFFD: "U+FFFD"}
	for r, want := range cases {
		if got := RuneLabel(r); got != want {
			t.Errorf("RuneLabel(U+%04X) = %q, want %q", r, got, want)
		}
	}
}

// TestConvertToUTF8_NextSaveWritesUTF8 pins the way out: after the
// conversion the refused text saves, as UTF-8, and the tab reports it.
func TestConvertToUTF8_NextSaveWritesUTF8(t *testing.T) {
	tab, path := openTemp(t, "fr.txt", []byte("caf\xe9\n"))
	typeAtEndOfLine(tab, 0, " →")
	if !tab.ConvertToUTF8() {
		t.Fatal("ConvertToUTF8 reported nothing to do")
	}
	if tab.Encoding != "" || !tab.Dirty {
		t.Fatalf("after convert: encoding %q dirty %v", tab.Encoding, tab.Dirty)
	}
	if err := tab.Save(); err != nil {
		t.Fatal(err)
	}
	if disk, _ := os.ReadFile(path); string(disk) != "café →\n" {
		t.Fatalf("disk = %q", disk)
	}
	if tab.ConvertToUTF8() {
		t.Fatal("a UTF-8 tab has nothing to convert")
	}
}

// TestConvertToUTF8_NoUndoStateReadsClean pins the baseline rule: with
// the disk still in Windows-1252, undoing back to the opened text must
// not mark the tab clean — its next save would rewrite the file.
func TestConvertToUTF8_NoUndoStateReadsClean(t *testing.T) {
	tab, _ := openTemp(t, "fr.txt", []byte("caf\xe9\n"))
	tab.ConvertToUTF8()
	typeAtEndOfLine(tab, 0, "x")
	tab.Undo()
	if tab.Buffer.Lines[0] != "café" {
		t.Fatalf("undo left %q", tab.Buffer.Lines[0])
	}
	if !tab.Dirty {
		t.Fatal("undo after a conversion read clean while the disk holds other bytes")
	}
}

// TestConvertToUTF8_CleanTabGoesDirty pins that converting a tab with no
// edits still marks it dirty: its bytes on disk are not what a save writes.
func TestConvertToUTF8_CleanTabGoesDirty(t *testing.T) {
	tab, _ := openTemp(t, "fr.txt", []byte("caf\xe9\n"))
	if tab.Dirty {
		t.Fatal("fresh tab dirty")
	}
	tab.ConvertToUTF8()
	if !tab.Dirty {
		t.Fatal("converted tab still clean")
	}
}

// TestUnencodable_FollowsTheRevision pins the memo: nil on a clean
// Windows-1252 tab, the first offender after an edit, nil again after the
// undo — and always nil for UTF-8.
func TestUnencodable_FollowsTheRevision(t *testing.T) {
	tab, _ := openTemp(t, "fr.txt", []byte("caf\xe9\nb\n"))
	if e := tab.Unencodable(); e != nil {
		t.Fatalf("clean tab: %v", e)
	}
	typeAtEndOfLine(tab, 1, "✓")
	e := tab.Unencodable()
	if e == nil || e.Rune != '✓' || e.Line != 1 || e.Col != 1 {
		t.Fatalf("after edit: %+v", e)
	}
	tab.Undo()
	if e := tab.Unencodable(); e != nil {
		t.Fatalf("after undo: %v", e)
	}
	u, _ := openTemp(t, "u.txt", []byte("→\n"))
	if e := u.Unencodable(); e != nil {
		t.Fatalf("UTF-8 tab: %v", e)
	}
}

// TestReload_RedetectsTheEncoding pins that the disk is the authority on
// a reload: a file converted to UTF-8 behind the tab stops being written
// back as Windows-1252.
func TestReload_RedetectsTheEncoding(t *testing.T) {
	tab, path := openTemp(t, "fr.txt", []byte("caf\xe9\n"))
	if err := os.WriteFile(path, []byte("café\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := tab.Reload(); err != nil {
		t.Fatal(err)
	}
	if tab.Encoding != "" {
		t.Fatalf("Encoding = %q after reloading a UTF-8 file", tab.Encoding)
	}
}

// TestReloadAsEdit_AdoptsTheEncodingWhenTextMatches pins the unchanged
// branch: same text, new bytes (a tool rewrote it as UTF-8) — no undo
// step, but the encoding follows the disk.
func TestReloadAsEdit_AdoptsTheEncodingWhenTextMatches(t *testing.T) {
	tab, path := openTemp(t, "fr.txt", []byte("caf\xe9\n"))
	if err := os.WriteFile(path, []byte("café\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	changed, err := tab.ReloadAsEdit()
	if err != nil || changed {
		t.Fatalf("ReloadAsEdit = %v, %v; want unchanged", changed, err)
	}
	if tab.Encoding != "" {
		t.Fatalf("Encoding = %q, want UTF-8", tab.Encoding)
	}
}

// TestDecodeBytes_AndDecodeAs pins the two exported decoders: detection
// for a fresh read, and a KNOWN encoding for git's lines — where valid
// UTF-8 bytes still mean their Windows-1252 text.
func TestDecodeBytes_AndDecodeAs(t *testing.T) {
	if text, enc := DecodeBytes([]byte("café")); text != "café" || enc != "" {
		t.Fatalf("DecodeBytes(utf8) = %q, %q", text, enc)
	}
	if text, enc := DecodeBytes([]byte("caf\xe9")); text != "café" || enc != EncodingCP1252 {
		t.Fatalf("DecodeBytes(latin1) = %q, %q", text, enc)
	}
	if got := DecodeAs(EncodingCP1252, []byte("caf\xc3\xa9")); got != "cafÃ©" {
		t.Fatalf("DecodeAs(cp1252) = %q", got)
	}
	if got := DecodeAs("", []byte("x\xe9")); got != "x\xe9" {
		t.Fatalf("DecodeAs(utf8) altered the bytes: %q", got)
	}
}

// TestEncodeAs_AndEncodeLossy pins the two exported encoders: EncodeAs
// refuses with the position, EncodeLossy (git's eyes only) substitutes.
func TestEncodeAs_AndEncodeLossy(t *testing.T) {
	if b, err := EncodeAs(EncodingCP1252, "é€"); err != nil || string(b) != "\xe9\x80" {
		t.Fatalf("EncodeAs = %q, %v", b, err)
	}
	if b, err := EncodeAs("", "→"); err != nil || string(b) != "→" {
		t.Fatalf("EncodeAs(utf8) = %q, %v", b, err)
	}
	_, err := EncodeAs(EncodingCP1252, "a\nb→")
	var uerr *UnencodableError
	if !errors.As(err, &uerr) || uerr.Line != 1 || uerr.Col != 1 {
		t.Fatalf("EncodeAs refusal = %v", err)
	}
	if got := EncodeLossy(EncodingCP1252, "é→x"); string(got) != "\xe9?x" {
		t.Fatalf("EncodeLossy = %q", got)
	}
}

// TestEncodingLabel pins the names the status bar and messages use.
func TestEncodingLabel(t *testing.T) {
	if EncodingLabel(EncodingCP1252) != "Windows-1252" || EncodingLabel("") != "UTF-8" {
		t.Fatal("labels changed")
	}
}

// TestRender_ControlRunesGetAStandIn pins the paint: a control rune is
// never sent to the terminal raw. ESC shows its control picture; the C1
// runes the Windows-1252 table produces (U+0090 is DCS) show ▯ — each in
// one cell, so the text after them stays where the layout put it.
func TestRender_ControlRunesGetAStandIn(t *testing.T) {
	tab := &Tab{Buffer: NewBuffer("a\x1bb" + string(rune(0x90)) + "c\x7fd\n")}
	scr := noteScreen(t, tab, 40, 3)
	row := noteRow(scr, 0, 40)
	if !strings.Contains(row, "a␛b▯c▯d") {
		t.Fatalf("row 0 = %q", row)
	}
}

// TestIsControlRune pins the set the stand-in covers: C0 but not tab,
// DEL, and C1 — and nothing printable.
func TestIsControlRune(t *testing.T) {
	for _, r := range []rune{0, 0x1B, '\r', 0x7F, 0x81, 0x9F} {
		if !IsControlRune(r) {
			t.Errorf("U+%04X should be a control", r)
		}
	}
	for _, r := range []rune{'\t', ' ', 'a', 0xA0, 'é', '€'} {
		if IsControlRune(r) {
			t.Errorf("U+%04X is not a control", r)
		}
	}
}
