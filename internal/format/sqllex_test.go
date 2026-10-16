// =============================================================================
// File: internal/format/sqllex_test.go
// Author: Rohan Allison <rohanthewiz@gmail.com>
// Created: 2026-10-16
// Copyright: 2026 Rohan Allison. All rights reserved.
// =============================================================================

package format

import (
	"errors"
	"strings"
	"testing"
)

// lexTexts lexes src and returns the token texts, failing the test on a
// lexing error.
func lexTexts(t *testing.T, src string) []string {
	t.Helper()
	toks, err := lexSQL(src)
	if err != nil {
		t.Fatalf("lexSQL(%q): %v", src, err)
	}
	out := make([]string, len(toks))
	for i, tk := range toks {
		out[i] = tk.text
	}
	return out
}

// TestLexSQL_SplitsAStatement pins the happy path: words, punctuation,
// operators, a cast and a parameter come out as the separate tokens the
// server's scanner would produce.
func TestLexSQL_SplitsAStatement(t *testing.T) {
	got := lexTexts(t, "SELECT a.b, x::int FROM t WHERE id = $1;")
	want := []string{"SELECT", "a", ".", "b", ",", "x", "::", "int", "FROM", "t", "WHERE", "id", "=", "$1", ";"}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Errorf("got  %q\nwant %q", got, want)
	}
}

// TestLexSQL_QuotedTokensStayWhole pins every quoting form that can hold
// characters which would otherwise end the token: a doubled quote, an
// E-string backslash escape, prefixed strings, quoted identifiers and a
// dollar-quoted body containing quotes, semicolons and newlines.
func TestLexSQL_QuotedTokensStayWhole(t *testing.T) {
	cases := []struct {
		src  string
		kind sqlTokKind
	}{
		{`'it''s'`, sqlString},
		{`E'it\'s'`, sqlString},
		{`B'0101'`, sqlString},
		{`X'ff'`, sqlString},
		{`U&'d\0061t'`, sqlString},
		{`"Mixed ""Case"""`, sqlQuotedIdent},
		{`U&"d\0061t"`, sqlQuotedIdent},
		{"$$ it's; done\n $$", sqlDollar},
		{"$fn$ a $$ b $fn$", sqlDollar},
	}
	for _, tc := range cases {
		toks, err := lexSQL(tc.src)
		if err != nil {
			t.Errorf("lexSQL(%q): %v", tc.src, err)
			continue
		}
		if len(toks) != 1 || toks[0].kind != tc.kind || toks[0].text != tc.src {
			t.Errorf("lexSQL(%q) = %+v, want one token of kind %v", tc.src, toks, tc.kind)
		}
	}
}

// TestLexSQL_CommentsNestAndInterruptOperators pins two scan.l rules: a
// block comment nests, and `--` / `/*` open a comment even in the middle
// of an operator run, so `a+--x` is `a`, `+`, comment.
func TestLexSQL_CommentsNestAndInterruptOperators(t *testing.T) {
	got := lexTexts(t, "/* a /* b */ c */ x")
	if len(got) != 2 || got[0] != "/* a /* b */ c */" {
		t.Errorf("nested comment lexed as %q", got)
	}
	got = lexTexts(t, "a+--note\nb")
	want := []string{"a", "+", "--note", "b"}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Errorf("got %q, want %q", got, want)
	}
}

// TestLexSQL_OperatorTrailingSignTrimmed pins scan.l's rule that a
// multi-character operator may not end in + or - unless it contains a
// special character: `a<-1` is `<` then `-` `1`, while `@-@` stays whole.
func TestLexSQL_OperatorTrailingSignTrimmed(t *testing.T) {
	got := lexTexts(t, "a<-1")
	want := []string{"a", "<", "-", "1"}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Errorf("got %q, want %q", got, want)
	}
	got = lexTexts(t, "a @-@ b")
	if len(got) != 3 || got[1] != "@-@" {
		t.Errorf("got %q, want @-@ kept whole", got)
	}
}

// TestLexSQL_PsqlForms pins psql's additions: variables in their three
// spellings, and a backslash meta-command running to the end of its line.
func TestLexSQL_PsqlForms(t *testing.T) {
	toks, err := lexSQL("\\set x 1\nSELECT :x, :'x', :\"x\", a[1:2];")
	if err != nil {
		t.Fatal(err)
	}
	if toks[0].kind != sqlMeta || toks[0].text != `\set x 1` {
		t.Errorf("meta token = %+v", toks[0])
	}
	var vars []string
	for _, tk := range toks {
		if tk.kind == sqlPsqlVar {
			vars = append(vars, tk.text)
		}
	}
	if strings.Join(vars, " ") != `:x :'x' :"x"` {
		t.Errorf("psql vars = %q", vars)
	}
}

// TestLexSQL_CopyDataIsOneToken pins the pg_dump case: rows after `COPY
// … FROM stdin;` — tabs, quotes and all — are one verbatim token through
// the `\.` line, and lexing resumes after it.
func TestLexSQL_CopyDataIsOneToken(t *testing.T) {
	src := "COPY t (a) FROM stdin;\n\tlead tab\nit's\n\\.\nSELECT 1;"
	toks, err := lexSQL(src)
	if err != nil {
		t.Fatal(err)
	}
	var data *sqlToken
	for i := range toks {
		if toks[i].kind == sqlCopyData {
			data = &toks[i]
		}
	}
	if data == nil || data.text != "\tlead tab\nit's\n\\." {
		t.Fatalf("copy data = %+v", data)
	}
	if last := toks[len(toks)-1]; last.text != ";" || toks[len(toks)-2].text != "1" {
		t.Errorf("lexing did not resume after the data: %+v", toks[len(toks)-3:])
	}
}

// TestLexSQL_CopyToStdoutHasNoData pins the other direction: COPY … TO
// stdout reads nothing from the script, so the next line is SQL.
func TestLexSQL_CopyToStdoutHasNoData(t *testing.T) {
	toks, err := lexSQL("COPY t TO stdout;\nSELECT 1;")
	if err != nil {
		t.Fatal(err)
	}
	for _, tk := range toks {
		if tk.kind == sqlCopyData {
			t.Fatalf("COPY TO produced a data token: %+v", tk)
		}
	}
}

// TestLexSQL_ErrorsArePositioned pins the refusals: each carries the byte
// offset of the construct that is broken (the OPENER of an unterminated
// one), and the tokens lexed before it are still returned for sampling.
func TestLexSQL_ErrorsArePositioned(t *testing.T) {
	cases := []struct {
		src  string
		off  int
		want string
	}{
		{"SELECT 'abc", 7, "unterminated string"},
		{`SELECT "abc`, 7, "unterminated quoted identifier"},
		{"SELECT /* x", 7, "unterminated /* comment"},
		{"SELECT $q$ x", 7, "unterminated $q$ string"},
		{"SELECT `a`", 7, "MySQL"},
		{"SELECT {{ x }}", 7, "template"},
		{"SELECT $ 1", 7, "stray"},
	}
	for _, tc := range cases {
		toks, err := lexSQL(tc.src)
		var le *sqlLexError
		if !errors.As(err, &le) {
			t.Errorf("lexSQL(%q) err = %v, want *sqlLexError", tc.src, err)
			continue
		}
		if le.off != tc.off || !strings.Contains(le.msg, tc.want) {
			t.Errorf("lexSQL(%q) = off %d %q, want off %d containing %q", tc.src, le.off, le.msg, tc.off, tc.want)
		}
		if len(toks) != 1 || toks[0].text != "SELECT" {
			t.Errorf("lexSQL(%q) partial tokens = %+v, want [SELECT]", tc.src, toks)
		}
	}
}

// TestLexSQL_RecordsSourceWhitespace pins the facts the layout reads off
// each token: newlines before it and whether it touched its neighbour.
func TestLexSQL_RecordsSourceWhitespace(t *testing.T) {
	toks, err := lexSQL("a\n\nb(c")
	if err != nil {
		t.Fatal(err)
	}
	if toks[1].nlBefore != 2 || !toks[1].spaceBefore {
		t.Errorf("b: nlBefore=%d spaceBefore=%v, want 2/true", toks[1].nlBefore, toks[1].spaceBefore)
	}
	if toks[2].spaceBefore {
		t.Error("( glued to b reported spaceBefore")
	}
}

// TestLexSQL_NumbersSwallowTrailingJunk pins the number forms, including
// PostgreSQL 16's prefixes and separators, and that glued identifier
// characters stay in the token so no space can ever be put inside it.
func TestLexSQL_NumbersSwallowTrailingJunk(t *testing.T) {
	for _, src := range []string{"42", "1.5e-3", ".5", "0xff", "1_000", "123abc"} {
		toks, err := lexSQL(src)
		if err != nil || len(toks) != 1 || toks[0].kind != sqlNumber {
			t.Errorf("lexSQL(%q) = %+v, %v; want one number", src, toks, err)
		}
	}
}
