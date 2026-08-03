// =============================================================================
// File: internal/plugins/diag_test.go
// Author: Rohan Allison <rohanthewiz@gmail.com>
// Created: 2026-08-02
// Copyright: 2026 Rohan Allison. All rights reserved.
// =============================================================================

package plugins

import "testing"

// TestParseDiagnostics_RealToolShapes is the test that matters: the
// format was chosen so real tools work unmodified, so it is pinned
// against actual output shapes rather than invented ones. If any of
// these regress, the "a provider is a one-liner" promise is broken.
func TestParseDiagnostics_RealToolShapes(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want Diagnostic
	}{
		{
			name: "go vet / gcc style with severity",
			in:   `main.go:12:6: error: undefined: foo`,
			want: Diagnostic{Path: "main.go", Line: 11, Col: 5, Severity: SevError, Message: "undefined: foo"},
		},
		{
			name: "go vet without severity word",
			in:   `internal/app/app.go:40:2: declared and not used: x`,
			want: Diagnostic{Path: "internal/app/app.go", Line: 39, Col: 1, Severity: SevInfo, Message: "declared and not used: x"},
		},
		{
			name: "eslint unix format",
			in:   `src/a.ts:3:11: Missing semicolon [Error/semi]`,
			want: Diagnostic{Path: "src/a.ts", Line: 2, Col: 10, Severity: SevInfo, Message: "Missing semicolon [Error/semi]"},
		},
		{
			name: "shellcheck gcc format warning",
			in:   `run.sh:7:5: warning: Quote this to prevent word splitting`,
			want: Diagnostic{Path: "run.sh", Line: 6, Col: 4, Severity: SevWarn, Message: "Quote this to prevent word splitting"},
		},
		{
			name: "grep -n, no column, no path",
			in:   `42:	// TODO: rework this`,
			want: Diagnostic{Path: "", Line: 41, Col: -1, Severity: SevInfo, Message: "// TODO: rework this"},
		},
		{
			name: "path and line, no column",
			in:   `notes.md:5: heading too long`,
			want: Diagnostic{Path: "notes.md", Line: 4, Col: -1, Severity: SevInfo, Message: "heading too long"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := ParseDiagnostics(tc.in)
			if len(got) != 1 {
				t.Fatalf("parsed %d diagnostics, want 1: %+v", len(got), got)
			}
			if got[0] != tc.want {
				t.Errorf("got  %+v\nwant %+v", got[0], tc.want)
			}
		})
	}
}

// TestParseDiagnostics_KeepsColonsInMessages is the reason the parser
// is incremental rather than a split on ':'. A grep over Go source hits
// `:=` constantly, and a fixed field count would eat the code the user
// is being shown.
func TestParseDiagnostics_KeepsColonsInMessages(t *testing.T) {
	got := ParseDiagnostics("12:\tfoo := bar // TODO: fix")
	if len(got) != 1 {
		t.Fatalf("parsed %d, want 1", len(got))
	}
	if got[0].Line != 11 {
		t.Errorf("Line = %d, want 11", got[0].Line)
	}
	if got[0].Col != -1 {
		t.Errorf("Col = %d, want -1 — \"\\tfoo \" is not a column", got[0].Col)
	}
	if got[0].Message != "foo := bar // TODO: fix" {
		t.Errorf("Message = %q, the := must survive", got[0].Message)
	}
}

// TestParseDiagnostics_DropsNoise pins the silent-drop rule. Tools
// interleave summaries, progress, and blank lines with their findings;
// if those became errors every provider would need a `| grep` wrapper.
func TestParseDiagnostics_DropsNoise(t *testing.T) {
	in := "Checking 3 files...\n" +
		"\n" +
		"main.go:2:1: error: bad\n" +
		"3 problems found\n" +
		"vet: cannot load package\n"
	got := ParseDiagnostics(in)
	if len(got) != 1 {
		t.Fatalf("parsed %d, want just the one real finding: %+v", len(got), got)
	}
	if got[0].Message != "bad" {
		t.Errorf("Message = %q, want %q", got[0].Message, "bad")
	}
}

// TestParseDiagnostics_EmptyIsNil pins that "nothing to say" and "no
// output" are the same value, so callers need only one check.
func TestParseDiagnostics_EmptyIsNil(t *testing.T) {
	if got := ParseDiagnostics(""); got != nil {
		t.Errorf("ParseDiagnostics(\"\") = %+v, want nil", got)
	}
	if got := ParseDiagnostics("nothing parseable here\n"); got != nil {
		t.Errorf("got %+v, want nil", got)
	}
}

// TestParseDiagnostics_ClampsOutOfRangeLines pins the one-based to
// zero-based conversion at its edge. Some tools print 0 for
// "whole file"; that has to land on the first line, never outside the
// buffer.
func TestParseDiagnostics_ClampsOutOfRangeLines(t *testing.T) {
	got := ParseDiagnostics("cfg.toml:0:0: warning: whole-file problem")
	if len(got) != 1 {
		t.Fatalf("parsed %d, want 1", len(got))
	}
	if got[0].Line != 0 || got[0].Col != 0 {
		t.Errorf("Line/Col = %d/%d, want 0/0", got[0].Line, got[0].Col)
	}
	if got[0].Severity != SevWarn {
		t.Errorf("Severity = %v, want warning", got[0].Severity)
	}
}

// TestParseDiagnostics_SeveritySynonyms pins the synonym folding — a
// plugin author shouldn't have to know which spelling ced implemented.
func TestParseDiagnostics_SeveritySynonyms(t *testing.T) {
	cases := map[string]Severity{
		"a:1: error: x":   SevError,
		"a:1: fatal: x":   SevError,
		"a:1: warn: x":    SevWarn,
		"a:1: warning: x": SevWarn,
		"a:1: note: x":    SevInfo,
		"a:1: hint: x":    SevInfo,
		// Case-insensitive, because tools disagree about that too.
		"a:1: ERROR: x": SevError,
		// A word that isn't a severity stays in the message.
		"a:1: banana: x": SevInfo,
	}
	for in, want := range cases {
		got := ParseDiagnostics(in)
		if len(got) != 1 {
			t.Fatalf("%q parsed %d, want 1", in, len(got))
		}
		if got[0].Severity != want {
			t.Errorf("%q severity = %v, want %v", in, got[0].Severity, want)
		}
		if in == "a:1: banana: x" && got[0].Message != "banana: x" {
			t.Errorf("unknown severity word must stay in the message, got %q", got[0].Message)
		}
	}
}

// TestSeverityString covers the label used in status messages.
func TestSeverityString(t *testing.T) {
	if SevError.String() != "error" || SevWarn.String() != "warning" || SevInfo.String() != "info" {
		t.Errorf("severity labels drifted: %v/%v/%v", SevError, SevWarn, SevInfo)
	}
}

// TestParseDiagnostic_SingleLine pins the exported one-line entry point
// the terminal panel scans scrollback with: it answers exactly what the
// whole-output parser would for that line, and reports false for prose
// so a caller holding lines never has to guess.
func TestParseDiagnostic_SingleLine(t *testing.T) {
	d, ok := ParseDiagnostic("internal/app/lsp.go:314:22: undefined: foo")
	if !ok {
		t.Fatal("a compiler line should parse")
	}
	if d.Path != "internal/app/lsp.go" || d.Line != 313 || d.Col != 21 {
		t.Errorf("parsed %+v, want lsp.go at 0-based 313:21", d)
	}
	if d.Message != "undefined: foo" {
		t.Errorf("message = %q, want %q", d.Message, "undefined: foo")
	}
	if _, ok := ParseDiagnostic("go: downloading example.com/mod"); ok {
		t.Error("a progress line has no location and must not parse")
	}
	// Same answer either way — two implementations of this format would
	// drift, and the user could not tell which one decided.
	if whole := ParseDiagnostics("internal/app/lsp.go:314:22: undefined: foo"); len(whole) != 1 || whole[0] != d {
		t.Errorf("ParseDiagnostics disagreed with ParseDiagnostic: %+v vs %+v", whole, d)
	}
}
