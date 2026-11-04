// =============================================================================
// File: internal/format/sqlvalidate_test.go
// Author: Rohan Allison <rohanthewiz@gmail.com>
// Created: 2026-11-03
// Copyright: 2026 Rohan Allison. All rights reserved.
// =============================================================================

package format

import (
	"fmt"
	"strings"
	"testing"
)

// TestValidateSQL_AcceptsValidPostgres pins the quiet case on the shapes
// that make PostgreSQL lexing interesting: doubled quotes, E'' escapes,
// a $$ body full of quotes and parens, nested comments, a psql variable
// and meta-command, and a COPY data block whose rows are not SQL at all.
func TestValidateSQL_AcceptsValidPostgres(t *testing.T) {
	src := "-- setup\n" +
		"\\set ON_ERROR_STOP on\n" +
		"select 'it''s', E'a\\'b', :'name', x::int from t where (a = 1);\n" +
		"/* outer /* inner */ still comment */\n" +
		"create function f() returns int as $$ begin return (')'); end $$ language plpgsql;\n" +
		"copy t (a, b) from stdin;\n" +
		"1\t'unclosed (\n" +
		"\\.\n"
	if got := validateSQL([]byte(src)); got != nil {
		t.Errorf("validateSQL(valid) = %v, want nil", got)
	}
}

// TestValidateSQL_EmptyIsNotBroken pins the shared mid-thought rule: a
// new or emptied file is not an error while the user types.
func TestValidateSQL_EmptyIsNotBroken(t *testing.T) {
	for _, src := range []string{"", "  \n\t\n"} {
		if got := validateSQL([]byte(src)); got != nil {
			t.Errorf("validateSQL(%q) = %v, want nil", src, got)
		}
	}
}

// TestValidateSQL_MarksTheCause pins the positions: each mistake is
// marked where it STARTED (the opening quote, the unclosed paren, the
// stray character), never at the end of the file where scanning gave up.
func TestValidateSQL_MarksTheCause(t *testing.T) {
	cases := []struct {
		name      string
		src       string
		line, col int
		msg       string
	}{
		{"unterminated string", "select 1;\nselect 'oops from t;\nselect 2;\n", 1, 7, "unterminated string"},
		{"unterminated comment", "select 1; /* never\nclosed\n", 0, 10, "unterminated /* comment"},
		{"unterminated dollar body", "do $fn$ begin\nend\n", 0, 3, "unterminated $fn$ string"},
		{"unclosed paren at statement end", "select (a, b;\nselect 1;\n", 0, 7, "unclosed '('"},
		{"unmatched close paren", "select a)\n", 0, 8, "unmatched ')'"},
		{"backtick", "select `id` from t;\n", 0, 7, "backtick"},
		{"template brace", "select * from {{ ref('x') }};\n", 0, 14, "template"},
	}
	for _, tc := range cases {
		got := validateSQL([]byte(tc.src))
		if len(got) != 1 {
			t.Errorf("%s: got %d problems (%v), want 1", tc.name, len(got), got)
			continue
		}
		p := got[0]
		if p.Line != tc.line || p.Col != tc.col || !strings.Contains(p.Message, tc.msg) {
			t.Errorf("%s: got %d:%d %q, want %d:%d containing %q",
				tc.name, p.Line, p.Col, p.Message, tc.line, tc.col, tc.msg)
		}
	}
}

// TestValidateSQL_ColumnsAreRunes pins the rune-column rule every
// validator shares: non-ASCII text before the mistake must not push the
// mark to the right.
func TestValidateSQL_ColumnsAreRunes(t *testing.T) {
	// "café" is 5 bytes, 4 runes; the stray quote sits after it.
	got := validateSQL([]byte("select café, 'x\n"))
	if len(got) != 1 || got[0].Line != 0 || got[0].Col != 13 {
		t.Errorf("got %v, want one problem at 0:13", got)
	}
}

// TestValidateSQL_LexErrorHidesParenCheck pins the "one honest problem"
// rule: an unterminated string after an open paren reports the string,
// not an invented unclosed paren layered over it.
func TestValidateSQL_LexErrorHidesParenCheck(t *testing.T) {
	got := validateSQL([]byte("select (a, 'b\n"))
	if len(got) != 1 || !strings.Contains(got[0].Message, "unterminated string") {
		t.Errorf("got %v, want the unterminated string alone", got)
	}
}

// TestValidateSQL_AgreesWithTheFormatter pins the reason the check
// reuses formatSQL's front half: a file the validator passes is one the
// formatter accepts, and a file it marks is one the formatter refuses
// with the SAME message at the SAME (1-based) position.
func TestValidateSQL_AgreesWithTheFormatter(t *testing.T) {
	for _, src := range []string{
		"select 1;\n",
		"select 'oops\n",
		"select (a;\n",
		"select `x`;\n",
		"insert into t values (1, 'a''b');\n",
	} {
		probs := validateSQL([]byte(src))
		_, ferr := formatSQL([]byte(src))
		if (probs != nil) != (ferr != nil) {
			t.Errorf("%q: validator problems=%v, formatter err=%v — they must agree", src, probs, ferr)
			continue
		}
		if probs == nil {
			continue
		}
		// The formatter's refusal is sqlPosError's "line L, col C: msg",
		// 1-based; rebuild it from the zero-based problem.
		p := probs[0]
		want := fmt.Sprintf("line %d, col %d: %s", p.Line+1, p.Col+1, p.Message)
		if ferr.Error() != want {
			t.Errorf("%q: formatter says %q, validator implies %q", src, ferr, want)
		}
	}
}
