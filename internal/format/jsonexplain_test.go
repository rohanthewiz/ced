// =============================================================================
// File: internal/format/jsonexplain_test.go
// Author: Rohan Allison <rohanthewiz@gmail.com>
// Created: 2026-09-30
// Copyright: 2026 Rohan Allison. All rights reserved.
// =============================================================================

package format

import (
	"strings"
	"testing"
)

// TestExplainJSON_PointsAtTheMistake walks the shapes people actually
// type and pins, for each, both halves of the answer: the message names
// the mistake, and the mark lands on the character to fix — which for a
// trailing or missing comma is NOT the one the parser rejected.
func TestExplainJSON_PointsAtTheMistake(t *testing.T) {
	cases := []struct {
		name      string
		src       string
		line, col int    // zero-based, runes
		want      string // substring of the message
	}{
		{"trailing comma in array", `{"tags": ["a", "b",]}`, 0, 18, "trailing comma"},
		{"trailing comma in object", "{\n  \"a\": 1,\n}\n", 1, 8, "trailing comma"},
		{"missing comma between members", "{\n  \"a\": 1\n  \"b\": 2\n}\n", 1, 7, "missing ','"},
		{"missing comma in array", `[1 2]`, 0, 1, "missing ','"},
		{"line comment", "{\n  // note\n  \"a\": 1\n}", 1, 2, "comments"},
		{"single-quoted string", `{"a": 'x'}`, 0, 6, "double quotes"},
		{"single-quoted key", `{'a': 1}`, 0, 1, "keys need double quotes"},
		{"unquoted key", `{a: 1}`, 0, 1, "keys must be in double quotes"},
		{"unquoted word", `{"a": NaN}`, 0, 6, "unquoted word"},
		{"missing colon", `{"a" 1}`, 0, 5, "missing ':'"},
		{"mismatched closer", "{\n  \"a\": [1, 2}\n", 1, 12, "opened on line 2"},
		{"double comma", `[1,,2]`, 0, 3, "extra ','"},
		{"text after document", `{} x`, 0, 3, "extra text"},
		{"stray closer after document", `{}}`, 0, 2, "no matching opener"},
		{"line break in string", "{\"a\": \"abc\n\"}", 0, 9, "line break inside a string"},
		{"bad escape", `{"a": "\q"}`, 0, 8, "unknown escape"},
	}
	for _, c := range cases {
		got := Validate("/proj/data.json", []byte(c.src))
		if len(got) != 1 {
			t.Errorf("%s: Validate = %v, want one problem", c.name, got)
			continue
		}
		p := got[0]
		if p.Line != c.line || p.Col != c.col {
			t.Errorf("%s: at line %d col %d, want line %d col %d", c.name, p.Line, p.Col, c.line, c.col)
		}
		if !strings.Contains(p.Message, c.want) {
			t.Errorf("%s: message %q, want it to contain %q", c.name, p.Message, c.want)
		}
	}
}

// TestExplainJSON_UnclosedNamesTheOpener pins the end-of-file case: the
// mark moves from the last byte of the file to the bracket (or quote)
// that was never closed, because "the file ended" says nothing about
// which of many braces lost its partner.
func TestExplainJSON_UnclosedNamesTheOpener(t *testing.T) {
	src := "{\n  \"a\": [\n    1,\n    2\n}"
	// Parsing stops at the '}' that does not match '[', which is the
	// mismatch shape, not EOF — so make a true EOF: drop the '}'.
	src = strings.TrimSuffix(src, "}")
	got := Validate("/proj/data.json", []byte(src))
	if len(got) != 1 {
		t.Fatalf("Validate = %v, want one problem", got)
	}
	if got[0].Line != 1 || got[0].Col != 7 {
		t.Errorf("at line %d col %d, want line 1 col 7 (the unclosed '[')", got[0].Line, got[0].Col)
	}
	if !strings.Contains(got[0].Message, "never closed") || !strings.Contains(got[0].Message, "']'") {
		t.Errorf("message %q, want the unclosed '[' and its missing ']' named", got[0].Message)
	}

	// An unterminated string points at its opening quote.
	got = Validate("/proj/data.json", []byte(`{"a": "abc`))
	if len(got) != 1 || got[0].Col != 6 || !strings.Contains(got[0].Message, "string is never closed") {
		t.Errorf("unterminated string: %v, want col 6 and 'string is never closed'", got)
	}
}

// TestExplainJSON_BracesInsideStringsAreText pins the scanner's string
// awareness: a '{' inside a value must not be counted as an unclosed
// bracket, or the EOF answer would point into the middle of a string.
func TestExplainJSON_BracesInsideStringsAreText(t *testing.T) {
	stack, str := jsonOpeners([]byte(`[ "a {b} \" [", {`), len(`[ "a {b} \" [", {`))
	if str != -1 {
		t.Fatalf("strStart = %d, want -1 (every string closed)", str)
	}
	if len(stack) != 2 || stack[0] != 0 || stack[1] != 16 {
		t.Errorf("stack = %v, want [0 16] (the '[' and the final '{' only)", stack)
	}
}

// TestExplainJSON_UnknownShapesKeepTheParserWording pins the fallback:
// an error this file has no story for keeps encoding/json's message and
// position — a vague true answer beats a confident wrong one.
func TestExplainJSON_UnknownShapesKeepTheParserWording(t *testing.T) {
	got := Validate("/proj/data.json", []byte(`[1.e5]`))
	if len(got) != 1 {
		t.Fatalf("Validate = %v, want one problem", got)
	}
	if !strings.Contains(got[0].Message, "invalid character") {
		t.Errorf("message %q, want the parser's own wording", got[0].Message)
	}
	if got[0].Col != 3 {
		t.Errorf("col = %d, want 3 (the 'e' the parser rejected)", got[0].Col)
	}
}

// TestPrevNonBlank pins the helper's edges: blanks of every kind are
// skipped, and running off the front is -1 rather than a panic.
func TestPrevNonBlank(t *testing.T) {
	src := []byte("a \t\r\n b")
	if got := prevNonBlank(src, 6); got != 0 {
		t.Errorf("prevNonBlank = %d, want 0", got)
	}
	if got := prevNonBlank([]byte("   x"), 3); got != -1 {
		t.Errorf("prevNonBlank over all-blank prefix = %d, want -1", got)
	}
}
