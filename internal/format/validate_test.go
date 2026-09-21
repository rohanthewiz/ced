// =============================================================================
// File: internal/format/validate_test.go
// Author: Rohan Allison <rohanthewiz@gmail.com>
// Created: 2026-09-21
// Copyright: 2026 Rohan Allison. All rights reserved.
// =============================================================================

package format

import (
	"strings"
	"testing"
)

// TestValidate_AcceptsValidJSON pins the quiet case, which is the one
// that runs on almost every keystroke: a file that parses produces no
// problems at all, so nothing is painted and nothing is allocated.
func TestValidate_AcceptsValidJSON(t *testing.T) {
	for _, src := range []string{
		`{}`,
		`[]`,
		`{"a": 1, "b": [true, null, "x"]}`,
		"{\n  \"nested\": {\n    \"deep\": [1, 2, 3]\n  }\n}\n",
		`"a bare string is a valid JSON document"`,
		`42`,
		`null`,
	} {
		if got := Validate("/proj/data.json", []byte(src)); got != nil {
			t.Errorf("Validate(%q) = %v, want nil", src, got)
		}
	}
}

// TestValidate_EmptyFileIsNotBroken pins the mid-thought case. This
// runs while the user types, so a file they just created — or just
// selected-all and deleted on the way to retyping — must not be
// underlined. encoding/json calls that "unexpected end of JSON input";
// the editor calls it "not yet".
func TestValidate_EmptyFileIsNotBroken(t *testing.T) {
	for _, src := range []string{"", "   ", "\n\n", "\t \n"} {
		if got := Validate("/proj/data.json", []byte(src)); got != nil {
			t.Errorf("Validate(%q) = %v, want nil (empty is mid-thought)", src, got)
		}
	}
}

// TestValidate_LocatesTheBrokenCharacter is the core of the feature:
// the problem must land ON the offending character, not one cell past
// it. encoding/json reports the byte AFTER the bad one, so this pins
// the correction — without it every underline sits one column right.
func TestValidate_LocatesTheBrokenCharacter(t *testing.T) {
	// A trailing comma. The '}' is what the parser chokes on, and it
	// sits on line 2 (zero-based) at column 0.
	src := "{\n  \"a\": 1,\n}\n"
	got := Validate("/proj/data.json", []byte(src))
	if len(got) != 1 {
		t.Fatalf("Validate = %v, want exactly one problem", got)
	}
	if got[0].Line != 2 || got[0].Col != 0 {
		t.Errorf("problem at line %d col %d, want line 2 col 0 (the '}')",
			got[0].Line, got[0].Col)
	}
	if got[0].Message == "" {
		t.Error("problem carries no message; the underline would be mute")
	}
}

// TestValidate_ColumnsAreRunesNotBytes is the bug this file exists to
// prevent. encoding/json counts BYTES; the editor's Span columns are
// RUNE indices. Any non-ASCII above the error — a name, an accent, an
// emoji — would otherwise push the underline right by one cell per
// extra byte, which is a confident wrong answer rather than a missing
// one.
func TestValidate_ColumnsAreRunesNotBytes(t *testing.T) {
	// "café" is 5 bytes but 4 runes, so the byte and rune columns of
	// the 'x' differ by exactly one.
	src := "{\n  \"café\": x\n}\n"
	got := Validate("/proj/data.json", []byte(src))
	if len(got) != 1 {
		t.Fatalf("Validate = %v, want exactly one problem", got)
	}
	// Line 1 is `  "café": x` — the 'x' is the 11th rune, index 10.
	if got[0].Line != 1 {
		t.Fatalf("line = %d, want 1", got[0].Line)
	}
	if got[0].Col != 10 {
		t.Errorf("col = %d, want 10 (rune index of 'x'); 11 means bytes were counted", got[0].Col)
	}
}

// TestValidate_UnterminatedDocument pins the other error shape
// encoding/json produces. Offset there is len(src), so the clamp and
// the Offset-1 correction have to cooperate: the position must stay
// inside the buffer rather than landing one past its end.
func TestValidate_UnterminatedDocument(t *testing.T) {
	got := Validate("/proj/data.json", []byte("{"))
	if len(got) != 1 {
		t.Fatalf("Validate = %v, want exactly one problem", got)
	}
	if got[0].Line != 0 || got[0].Col != 0 {
		t.Errorf("problem at line %d col %d, want line 0 col 0 (the unclosed '{')",
			got[0].Line, got[0].Col)
	}
	if !strings.Contains(got[0].Message, "unexpected end") {
		t.Errorf("message = %q, want the parser's own wording", got[0].Message)
	}
}

// TestValidate_ReportsOneProblemNotACascade pins the deliberate choice
// to stop at the first error. Everything after a missing brace is
// unparseable in a way that says nothing about the text, so a list of
// invented follow-on errors would be noise pointing at correct code.
func TestValidate_ReportsOneProblemNotACascade(t *testing.T) {
	src := "{\n  \"a\" 1\n  \"b\" 2\n  \"c\" 3\n}\n"
	got := Validate("/proj/data.json", []byte(src))
	if len(got) != 1 {
		t.Fatalf("Validate returned %d problems, want exactly 1", len(got))
	}
}

// TestValidate_IgnoresFilesItDoesNotOwn pins silence on everything
// else. A validator that had an opinion about every file would be
// painting marks on languages it cannot parse.
func TestValidate_IgnoresFilesItDoesNotOwn(t *testing.T) {
	// Deliberately broken JSON, under names ced must not judge.
	broken := []byte("{ this is not json at all")
	for _, p := range []string{
		"/proj/notes.txt",
		"/proj/main.go",
		"/proj/tsconfig.json",
		"/proj/.vscode/settings.json",
		"/proj/data.jsonc",
	} {
		if got := Validate(p, broken); got != nil {
			t.Errorf("Validate(%q) = %v, want nil", p, got)
		}
	}
}

// TestValidates_MatchesValidate pins the cheap predicate against the
// real thing. Validates exists so callers can skip reading a buffer
// they'd learn nothing from; if it ever disagreed with Validate, a file
// would either be checked twice or never checked at all.
func TestValidates_MatchesValidate(t *testing.T) {
	broken := []byte("{,}")
	for _, p := range []string{
		"/proj/data.json", "/proj/main.go", "/proj/notes.txt",
		"/proj/tsconfig.json", "/proj/data.jsonc",
	} {
		claimed := Validates(p)
		actual := Validate(p, broken) != nil
		if claimed != actual {
			t.Errorf("Validates(%q) = %v but Validate produced problems = %v", p, claimed, actual)
		}
	}
}

// TestValidate_GoIsLeftToGopls pins the deliberate absence. gopls
// reports Go parse errors with far better messages than a bare
// go/parser pass, and two producers underlining the same broken line
// would only argue with each other in the one gutter cell.
func TestValidate_GoIsLeftToGopls(t *testing.T) {
	if Validates("/proj/main.go") {
		t.Error("Validates(main.go) = true; Go syntax belongs to gopls")
	}
}
