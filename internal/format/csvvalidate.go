// =============================================================================
// File: internal/format/csvvalidate.go
// Author: Rohan Allison <rohanthewiz@gmail.com>
// Created: 2026-10-13
// Copyright: 2026 Rohan Allison. All rights reserved.
// =============================================================================

// csvvalidate.go is the CSV half of ced's in-process syntax check. It
// answers the same question validateJSON does — "does this parse, and
// if not, WHERE is the mistake?" — for RFC 4180 comma-separated files.
//
// # What counts as broken
//
// Two families, both things a downstream reader (a spreadsheet import,
// pandas, encoding/csv itself) rejects or silently mis-reads:
//
//   - QUOTING. A `"` inside an unquoted field, text after a closing
//     quote, or a quote that never closes. The last one is the nasty
//     case: an unclosed quote swallows every following line into one
//     field, so the file "parses" into garbage rather than failing.
//   - SHAPE. A row whose field count differs from the first row's. RFC
//     4180 asks every record to have the same number of fields, and an
//     extra comma in one row is the most common way a CSV goes wrong by
//     hand — the columns after it all shift one to the right.
//
// # Why encoding/csv plus a small re-scan
//
// encoding/csv is the strict reference parser (LazyQuotes off), so the
// accept/reject decision is the standard library's, not a hand-rolled
// opinion. What it does NOT do is point at the mistake for every shape:
//
//	1,"abc,3          ← the mistake: the quote never closes
//	4,5,6
//	<EOF>             ← where encoding/csv reports it
//
// So, like jsonexplain.go, the position is MOVED onto the character that
// needs fixing. For quote errors a tiny RFC 4180 scanner (quotedFieldAt)
// re-walks just the failing record from its first line to find the quote
// that opened the bad field. It never decides validity — it only runs
// after encoding/csv has already said no.
//
// # Why only .csv, not .tsv
//
// TSV has no quoting convention (the IANA definition simply forbids tabs
// inside fields), and real TSV files carry literal `"` characters freely.
// Running this checker with a tab delimiter would underline every one of
// them, which is a validator lying about a file that is fine.

package format

import (
	"bytes"
	"encoding/csv"
	"errors"
	"fmt"
	"io"
	"unicode/utf8"
)

// validateCSV parses src as strict RFC 4180 CSV and locates the first
// problem, or returns nil when the file is fine.
//
// One problem, not a list — the JSON rule, for the same reason: after
// an unclosed quote everything below is mis-tokenised, and after a
// shape error every later row is measured against a count that may
// itself be the mistake.
func validateCSV(src []byte) []Problem {
	// Empty or whitespace-only is mid-thought, not broken — the same
	// call validateJSON makes, since this too runs while the user types.
	if len(bytes.TrimSpace(src)) == 0 {
		return nil
	}

	r := csv.NewReader(bytes.NewReader(src))
	// Strict on purpose: LazyQuotes would accept exactly the stray
	// quotes this checker exists to catch.
	r.LazyQuotes = false
	// 0 = "the first record sets the count", which is the RFC's rule
	// and what every importer infers from a header row.
	r.FieldsPerRecord = 0
	// The fields themselves are never kept, so one backing array for
	// every row keeps a large fixture from allocating per line on each
	// debounced re-parse.
	r.ReuseRecord = true

	// firstLine is the 1-based line the first record started on, kept
	// for the shape message: blank lines are skipped by encoding/csv,
	// so "the first row" is not necessarily line 1.
	firstLine, firstCount := 0, 0
	for {
		rec, err := r.Read()
		if err == io.EOF {
			return nil
		}
		if err == nil {
			// Only a record that parsed whole sets the reference: a
			// rejected one may come back partial.
			if firstLine == 0 && len(rec) > 0 {
				firstLine, _ = r.FieldPos(0)
				firstCount = len(rec)
			}
			continue
		}

		var pe *csv.ParseError
		if !errors.As(err, &pe) {
			// Unreachable over a bytes.Reader, but a rejection reported
			// at the top of the file beats one silently dropped — the
			// validateJSON rule.
			return []Problem{{Message: err.Error()}}
		}
		switch {
		case errors.Is(pe.Err, csv.ErrFieldCount):
			return []Problem{csvShapeProblem(src, r, rec, firstLine, firstCount)}
		case errors.Is(pe.Err, csv.ErrBareQuote):
			// encoding/csv's position is the stray quote itself, which
			// IS the mistake — no move needed, only words.
			line, col := offsetToLineCol(src, csvOffset(src, pe.Line, pe.Column))
			return []Problem{{Line: line, Col: col,
				Message: `stray " in an unquoted field — quote the whole field and write the quote as ""`}}
		case errors.Is(pe.Err, csv.ErrQuote):
			return []Problem{csvQuoteProblem(src, pe)}
		default:
			line, col := offsetToLineCol(src, csvOffset(src, pe.Line, pe.Column))
			return []Problem{{Line: line, Col: col, Message: pe.Err.Error()}}
		}
	}
}

// csvShapeProblem phrases and places a field-count mismatch. rec is the
// offending row (encoding/csv returns it alongside ErrFieldCount, and
// FieldPos is valid for it).
//
// Too MANY fields: the mark lands on the first extra field, which is
// where the columns start to shift. Too FEW: there is no missing field
// to point at, so the mark lands on the row's last character — "this is
// where the row ran out" — rather than its first, which would read as
// blaming a field that is fine.
func csvShapeProblem(src []byte, r *csv.Reader, rec []string, firstLine, firstCount int) Problem {
	msg := fmt.Sprintf("row has %d fields, but the first row (line %d) has %d",
		len(rec), firstLine, firstCount)
	var off int
	if len(rec) > firstCount {
		l, c := r.FieldPos(firstCount)
		off = csvOffset(src, l, c)
		msg += " — the extra field starts here"
	} else {
		// InputOffset is just past the row's terminating newline (or
		// at EOF); back up over the newline to the last real byte, then
		// to the START of that rune so a multi-byte final character is
		// not split.
		off = int(r.InputOffset())
		if off > 0 && off <= len(src) && src[off-1] == '\n' {
			off--
		}
		if off > 0 {
			_, size := utf8.DecodeLastRune(src[:off])
			off -= size
		}
	}
	line, col := offsetToLineCol(src, off)
	return Problem{Line: line, Col: col, Message: msg}
}

// csvQuoteProblem places an ErrQuote — encoding/csv's one error for two
// very different mistakes — by re-walking the failing record to find
// the quote that opened the bad field:
//
//	the field never closes          → mark the OPENING quote
//	it closes on a LATER line, then
//	  stray text follows            → mark the opening quote too: a
//	                                  quote that silently ate a newline
//	                                  is almost always a missing close
//	it closes on the SAME line, then
//	  stray text follows            → keep encoding/csv's mark (the
//	                                  closing quote): either it was
//	                                  meant as a literal "" or the text
//	                                  after it is the stray
func csvQuoteProblem(src []byte, pe *csv.ParseError) Problem {
	start := csvOffset(src, pe.StartLine, 1)
	open, eof, ok := quotedFieldAt(src, start)
	if ok {
		line, col := offsetToLineCol(src, open)
		if eof {
			return Problem{Line: line, Col: col,
				Message: `unclosed " — this field runs to the end of the file`}
		}
		if pe.Line > line+1 {
			return Problem{Line: line, Col: col, Message: fmt.Sprintf(
				`the field opened by this " runs on to line %d — missing closing "?`, pe.Line)}
		}
	}
	line, col := offsetToLineCol(src, csvOffset(src, pe.Line, pe.Column))
	return Problem{Line: line, Col: col,
		Message: `text after a closing " — inside a quoted field, write a quote as ""`}
}

// quotedFieldAt walks ONE record of src from byte offset start and
// reports the opening quote of the quoted field that went wrong: either
// one still open at EOF (eof=true) or one whose closing quote is
// followed by something other than a comma or line end. ok=false means
// the walk found no such field, and the caller keeps encoding/csv's own
// position.
//
// Only ever called on a record encoding/csv already rejected with
// ErrQuote, so it implements just enough of RFC 4180 to retrace that
// rejection — it never judges a file on its own.
func quotedFieldAt(src []byte, start int) (open int, eof bool, ok bool) {
	i := start
	for i < len(src) {
		if src[i] != '"' {
			// Unquoted field: skip to the next separator. A record end
			// here means the walk passed the failing field without
			// seeing it, which should not happen; say so with ok=false.
			for i < len(src) && src[i] != ',' && src[i] != '\n' {
				i++
			}
			if i >= len(src) || src[i] == '\n' {
				return 0, false, false
			}
			i++ // past the comma
			continue
		}

		// Quoted field: "" is an escaped quote, a lone " closes.
		open = i
		i++
		for {
			if i >= len(src) {
				return open, true, true
			}
			if src[i] == '"' {
				if i+1 < len(src) && src[i+1] == '"' {
					i += 2
					continue
				}
				break
			}
			i++
		}
		i++ // past the closing quote
		switch {
		case i >= len(src) || src[i] == '\n':
			return 0, false, false
		case src[i] == ',':
			i++
		default:
			return open, false, true
		}
	}
	return 0, false, false
}

// csvOffset converts encoding/csv's 1-based (line, BYTE column) to a
// byte offset into src, clamped to the buffer. offsetToLineCol then
// turns it into the editor's zero-based RUNE column — the same seam the
// JSON path goes through, so the two can never disagree on non-ASCII.
func csvOffset(src []byte, line, col int) int {
	off := 0
	for l := 1; l < line; l++ {
		nl := bytes.IndexByte(src[off:], '\n')
		if nl < 0 {
			return len(src)
		}
		off += nl + 1
	}
	off += col - 1
	if off < 0 {
		return 0
	}
	if off > len(src) {
		return len(src)
	}
	return off
}
