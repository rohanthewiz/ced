// =============================================================================
// File: internal/format/csvvalidate_test.go
// Author: Rohan Allison <rohanthewiz@gmail.com>
// Created: 2026-10-13
// Copyright: 2026 Rohan Allison. All rights reserved.
// =============================================================================

package format

import (
	"strings"
	"testing"
)

// csvOne runs the CSV validator and insists on exactly one problem —
// the one-problem rule is part of every expectation below.
func csvOne(t *testing.T, src string) Problem {
	t.Helper()
	got := Validate("/proj/data.csv", []byte(src))
	if len(got) != 1 {
		t.Fatalf("Validate(%q) = %v, want exactly one problem", src, got)
	}
	return got[0]
}

// TestValidateCSV_AcceptsValidFiles pins the quiet case, including the
// RFC 4180 features a naive checker trips on: quoted commas, escaped
// quotes, a newline inside a quoted field, blank lines, and a missing
// final newline.
func TestValidateCSV_AcceptsValidFiles(t *testing.T) {
	for _, src := range []string{
		"a,b,c\n1,2,3\n",
		"a,b,c\n1,2,3",
		"name,note\nann,\"hello, world\"\n",
		"name,note\nann,\"she said \"\"hi\"\"\"\n",
		"name,note\nann,\"two\nlines\"\nbob,x\n",
		"a,b\n\n1,2\n",
		"single\nx\ny\n",
		"a,b\n,\n",
		"café,naïve\n☕,ü\n",
	} {
		if got := Validate("/proj/data.csv", []byte(src)); got != nil {
			t.Errorf("Validate(%q) = %v, want nil", src, got)
		}
	}
}

// TestValidateCSV_EmptyFileIsNotBroken pins the mid-thought case shared
// with JSON: a just-created or just-cleared file is not underlined.
func TestValidateCSV_EmptyFileIsNotBroken(t *testing.T) {
	for _, src := range []string{"", "  ", "\n\n"} {
		if got := Validate("/proj/data.csv", []byte(src)); got != nil {
			t.Errorf("Validate(%q) = %v, want nil", src, got)
		}
	}
}

// TestValidateCSV_TooManyFieldsMarksTheExtraField pins the commonest
// hand-made CSV mistake: one extra comma shifts every later column, and
// the mark must land where the shift starts.
func TestValidateCSV_TooManyFieldsMarksTheExtraField(t *testing.T) {
	p := csvOne(t, "a,b,c\n1,2,3\n4,5,6,7\n")
	if p.Line != 2 || p.Col != 6 {
		t.Errorf("problem at %d:%d, want 2:6 (the '7')", p.Line, p.Col)
	}
	if !strings.Contains(p.Message, "row has 4 fields, but the first row (line 1) has 3") {
		t.Errorf("message = %q", p.Message)
	}
}

// TestValidateCSV_TooFewFieldsMarksTheRowEnd pins the other shape: there
// is no missing field to point at, so the mark sits on the row's last
// character rather than blaming its (fine) first field.
func TestValidateCSV_TooFewFieldsMarksTheRowEnd(t *testing.T) {
	p := csvOne(t, "a,b,c\n1,22\n")
	if p.Line != 1 || p.Col != 3 {
		t.Errorf("problem at %d:%d, want 1:3 (the last '2')", p.Line, p.Col)
	}
	if !strings.Contains(p.Message, "row has 2 fields") {
		t.Errorf("message = %q", p.Message)
	}
}

// TestValidateCSV_TooFewAtEOFWithoutNewline pins the row-end back-up
// when the file has no final newline: InputOffset is then len(src), and
// stepping back must not skip a real character.
func TestValidateCSV_TooFewAtEOFWithoutNewline(t *testing.T) {
	p := csvOne(t, "a,b,c\n1,é")
	if p.Line != 1 || p.Col != 2 {
		t.Errorf("problem at %d:%d, want 1:2 (the 'é')", p.Line, p.Col)
	}
}

// TestValidateCSV_FirstRowIsCountedAfterBlankLines pins the "line N" in
// the shape message: encoding/csv skips leading blank lines, so the
// first row is not always line 1, and saying so would send the reader
// to the wrong place.
func TestValidateCSV_FirstRowIsCountedAfterBlankLines(t *testing.T) {
	p := csvOne(t, "\n\na,b\n1,2,3\n")
	if !strings.Contains(p.Message, "first row (line 3) has 2") {
		t.Errorf("message = %q", p.Message)
	}
}

// TestValidateCSV_BareQuoteMarksTheQuote pins the stray-quote case and
// the byte→rune conversion: encoding/csv reports a BYTE column, and the
// 'é' before the quote must not push the mark one cell right.
func TestValidateCSV_BareQuoteMarksTheQuote(t *testing.T) {
	p := csvOne(t, "a,b\né\"x,2\n")
	if p.Line != 1 || p.Col != 1 {
		t.Errorf("problem at %d:%d, want 1:1 (rune index of the quote)", p.Line, p.Col)
	}
	if !strings.Contains(p.Message, `stray "`) {
		t.Errorf("message = %q", p.Message)
	}
}

// TestValidateCSV_TextAfterClosingQuoteOnSameLine pins the same-line
// ErrQuote: the closing quote is where the field went wrong (either it
// was meant as a literal "" or what follows it is the stray).
func TestValidateCSV_TextAfterClosingQuoteOnSameLine(t *testing.T) {
	p := csvOne(t, "a,b\n\"2\"x,3\n")
	if p.Line != 1 || p.Col != 2 {
		t.Errorf("problem at %d:%d, want 1:2 (the closing quote)", p.Line, p.Col)
	}
	if !strings.Contains(p.Message, "text after a closing") {
		t.Errorf("message = %q", p.Message)
	}
}

// TestValidateCSV_UnclosedQuoteMarksTheOpener is the case the re-scan
// exists for. encoding/csv reports the END of the file; the mistake is
// the quote that never closed, several lines up.
func TestValidateCSV_UnclosedQuoteMarksTheOpener(t *testing.T) {
	p := csvOne(t, "a,b,c\n1,\"oops,3\n4,5,6\n7,8,9\n")
	if p.Line != 1 || p.Col != 2 {
		t.Errorf("problem at %d:%d, want 1:2 (the opening quote)", p.Line, p.Col)
	}
	if !strings.Contains(p.Message, "unclosed") {
		t.Errorf("message = %q", p.Message)
	}
}

// TestValidateCSV_QuoteThatEatsALineMarksTheOpener pins the subtler
// form: the unclosed quote finds a LATER quote to close on, and
// encoding/csv complains about the text after that one. The opener is
// still the mistake, and the message names the line the field ran to.
func TestValidateCSV_QuoteThatEatsALineMarksTheOpener(t *testing.T) {
	p := csvOne(t, "a,b,c\n1,\"oops,3\n4,\"x\",6\n")
	if p.Line != 1 || p.Col != 2 {
		t.Errorf("problem at %d:%d, want 1:2 (the opening quote)", p.Line, p.Col)
	}
	if !strings.Contains(p.Message, "runs on to line 3") {
		t.Errorf("message = %q", p.Message)
	}
}

// TestValidateCSV_ReportsOneProblem pins the one-problem rule on a file
// broken several times over.
func TestValidateCSV_ReportsOneProblem(t *testing.T) {
	got := Validate("/proj/data.csv", []byte("a,b\n1,2,3\n4\n5,6,7,8\n"))
	if len(got) != 1 {
		t.Fatalf("Validate returned %d problems, want 1", len(got))
	}
	if got[0].Line != 1 {
		t.Errorf("problem on line %d, want 1 (the first bad row)", got[0].Line)
	}
}

// TestQuotedFieldAt_DeclinesWhenNothingIsWrong pins the scanner's
// ok=false answer: on a record with no bad quoted field it must say so,
// leaving encoding/csv's position in charge.
func TestQuotedFieldAt_DeclinesWhenNothingIsWrong(t *testing.T) {
	for _, src := range []string{"a,\"b\",c\n", "\"a\"\"b\"\n", "plain\n", ""} {
		if _, _, ok := quotedFieldAt([]byte(src), 0); ok {
			t.Errorf("quotedFieldAt(%q) ok = true, want false", src)
		}
	}
}

// TestCSVOffset_ClampsToTheBuffer pins the clamp: a position past the
// last line or before the first byte lands inside the buffer.
func TestCSVOffset_ClampsToTheBuffer(t *testing.T) {
	src := []byte("ab\ncd\n")
	if got := csvOffset(src, 2, 2); got != 4 {
		t.Errorf("csvOffset(2,2) = %d, want 4", got)
	}
	if got := csvOffset(src, 9, 1); got != len(src) {
		t.Errorf("csvOffset past EOF = %d, want %d", got, len(src))
	}
	if got := csvOffset(src, 1, 0); got != 0 {
		t.Errorf("csvOffset(1,0) = %d, want 0", got)
	}
}

// TestValidateCSV_TSVIsLeftAlone pins the deliberate exclusion: TSV has
// no quoting convention, so a literal quote in it is not an error.
func TestValidateCSV_TSVIsLeftAlone(t *testing.T) {
	if Validates("/proj/data.tsv") {
		t.Error("Validates(data.tsv) = true; TSV quotes are literal")
	}
}
