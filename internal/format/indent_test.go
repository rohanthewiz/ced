// =============================================================================
// File: internal/format/indent_test.go
// Author: Rohan Allison <rohanthewiz@gmail.com>
// Created: 2026-09-28
// Copyright: 2026 Rohan Allison. All rights reserved.
// =============================================================================

package format

import "testing"

// TestDetectIndent covers each branch of the detection rule: spaces of
// several widths, tabs, a minified file with no evidence, blank lines
// carrying trailing indentation, and alignment-sized runs.
func TestDetectIndent(t *testing.T) {
	cases := []struct {
		name string
		src  string
		want Indent
	}{
		{"two spaces", "{\n  \"a\": {\n    \"b\": 1\n  }\n}\n", Indent{Width: 2}},
		{"four spaces", "{\n    \"a\": [\n        1\n    ]\n}\n", Indent{Width: 4}},
		{"tabs", "{\n\t\"a\": {\n\t\t\"b\": 1\n\t}\n}\n", Indent{Tabs: true}},
		{"minified", `{"a":1,"b":[2,3]}`, Indent{}},
		{"empty", "", Indent{}},
		{"no trailing newline", "{\n   \"a\": 1}", Indent{Width: 3}},
		// A blank line holding two spaces must not outvote the real unit.
		{"whitespace-only lines ignored", "{\n  \n    \"a\": 1\n}\n", Indent{Width: 4}},
		// Tabs outnumber spaces: one stray space-indented line should not
		// flip a tab-indented file to spaces.
		{"tabs win the vote", "{\n\t\"a\": 1,\n\t\"b\": 2,\n  \"c\": 3\n}\n", Indent{Tabs: true}},
		{"alignment-only is unknown", "{\n            \"a\": 1\n}\n", Indent{}},
		// CRLF files: the \r rides at the END of the line, so the leading
		// run is measured exactly as for LF.
		{"crlf", "{\r\n    \"a\": 1\r\n}\r\n", Indent{Width: 4}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := DetectIndent([]byte(tc.src)); got != tc.want {
				t.Fatalf("DetectIndent = %v, want %v", got, tc.want)
			}
		})
	}
}

// TestIndent_UnitAndString pins the three renderings of an Indent —
// tabs, spaces, and the zero value falling back to the caller's default.
func TestIndent_UnitAndString(t *testing.T) {
	cases := []struct {
		in       Indent
		unit     string
		str      string
		wantKnow bool
	}{
		{Indent{Tabs: true}, "\t", "tabs", true},
		{Indent{Width: 4}, "    ", "4 spaces", true},
		{Indent{}, "  ", "unknown", false},
	}
	for _, tc := range cases {
		if got := tc.in.Unit("  "); got != tc.unit {
			t.Errorf("%v.Unit = %q, want %q", tc.in, got, tc.unit)
		}
		if got := tc.in.String(); got != tc.str {
			t.Errorf("String = %q, want %q", got, tc.str)
		}
		if got := tc.in.Known(); got != tc.wantKnow {
			t.Errorf("%v.Known = %v, want %v", tc.in, got, tc.wantKnow)
		}
	}
}
