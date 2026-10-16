// =============================================================================
// File: internal/format/sqlfmt_test.go
// Author: Rohan Allison <rohanthewiz@gmail.com>
// Created: 2026-10-16
// Copyright: 2026 Rohan Allison. All rights reserved.
// =============================================================================

package format

import (
	"strings"
	"testing"
)

// mustFormatSQL formats src, failing the test on an error, and checks the
// result is a fixed point — every layout test is an idempotence test too,
// since a formatter that moves its own output fights the file.
func mustFormatSQL(t *testing.T, src string) string {
	t.Helper()
	out, err := formatSQL([]byte(src))
	if err != nil {
		t.Fatalf("formatSQL: %v", err)
	}
	again, err := formatSQL(out)
	if err != nil {
		t.Fatalf("formatSQL(own output): %v", err)
	}
	if string(again) != string(out) {
		t.Errorf("not idempotent:\nonce:\n%s\ntwice:\n%s", out, again)
	}
	return string(out)
}

// checkSQL compares formatted output against want, printing both whole
// on a mismatch (a layout diff is unreadable as a quoted string).
func checkSQL(t *testing.T, got, want string) {
	t.Helper()
	if got != want {
		t.Errorf("got:\n%s\nwant:\n%s", got, want)
	}
}

// TestFormatSQL_ClauseLayout pins the core query layout: clause keywords
// on their own lines, bodies one level in with an item per line, joins
// under FROM with their ANDs a level further, WHERE's ANDs/ORs breaking
// but BETWEEN's AND not, a subquery as an indented block, and the short
// clauses (LIMIT) keeping their body on their line.
func TestFormatSQL_ClauseLayout(t *testing.T) {
	got := mustFormatSQL(t, "SELECT a, count(*) AS n FROM t JOIN u ON u.id = t.uid AND u.ok "+
		"WHERE x BETWEEN 1 AND 2 OR z IN (SELECT id FROM w) GROUP BY a ORDER BY a DESC LIMIT 10;")
	checkSQL(t, got, `SELECT
    a,
    count(*) AS n
FROM
    t
    JOIN u ON u.id = t.uid
        AND u.ok
WHERE
    x BETWEEN 1 AND 2
    OR z IN (
        SELECT
            id
        FROM
            w
    )
GROUP BY
    a
ORDER BY
    a DESC
LIMIT 10;
`)
}

// TestFormatSQL_DMLAndCTEs pins INSERT … VALUES rows one per line,
// ON CONFLICT with DO UPDATE SET read as one verb, and CTEs separated at
// the statement's own depth.
func TestFormatSQL_DMLAndCTEs(t *testing.T) {
	got := mustFormatSQL(t, "INSERT INTO t (a, b) VALUES (1, 2), (3, 4) ON CONFLICT (a) DO UPDATE SET b = EXCLUDED.b RETURNING id;")
	checkSQL(t, got, `INSERT INTO t (a, b)
VALUES
    (1, 2),
    (3, 4)
ON CONFLICT (a) DO UPDATE SET
    b = EXCLUDED.b
RETURNING
    id;
`)
	got = mustFormatSQL(t, "WITH a AS (SELECT 1), b AS (SELECT 2) SELECT * FROM a, b;")
	checkSQL(t, got, `WITH a AS (
    SELECT
        1
),
b AS (
    SELECT
        2
)
SELECT
    *
FROM
    a,
    b;
`)
}

// TestFormatSQL_InlineGroupsStayInline pins the frames that suppress
// clause layout: ORDER BY in an aggregate, PARTITION BY in OVER, FROM in
// extract(), WHERE in FILTER, and a CASE inside a call all stay on one
// line — and a CASE at a body's own level breaks into WHEN lines.
func TestFormatSQL_InlineGroupsStayInline(t *testing.T) {
	got := mustFormatSQL(t, "SELECT array_agg(x ORDER BY y) FILTER (WHERE y > 0), extract(year FROM d), "+
		"row_number() OVER (PARTITION BY a ORDER BY b), coalesce(CASE WHEN p THEN 1 END, 0), "+
		"CASE WHEN a > 0 THEN 'pos' ELSE 'neg' END AS sign FROM t;")
	checkSQL(t, got, `SELECT
    array_agg(x ORDER BY y) FILTER (WHERE y > 0),
    extract(year FROM d),
    row_number() OVER (PARTITION BY a ORDER BY b),
    coalesce(CASE WHEN p THEN 1 END, 0),
    CASE
        WHEN a > 0 THEN 'pos'
        ELSE 'neg'
    END AS sign
FROM
    t;
`)
}

// TestFormatSQL_CreateTableColumnList pins the one DDL shape that is laid
// out: one column per line, constraints' own parentheses inline.
func TestFormatSQL_CreateTableColumnList(t *testing.T) {
	got := mustFormatSQL(t, "CREATE TABLE IF NOT EXISTS public.users (id bigserial PRIMARY KEY, email text NOT NULL, CHECK (length(email) > 3));")
	checkSQL(t, got, `CREATE TABLE IF NOT EXISTS public.users (
    id bigserial PRIMARY KEY,
    email text NOT NULL,
    CHECK (length(email) > 3)
);
`)
}

// TestFormatSQL_OtherStatementsKeepTheirLines pins the non-query rule:
// DDL keeps the author's line breaks (re-indented one level), a GRANT
// whose SELECT is a privilege is not broken like a query, and a dollar
// body is copied verbatim.
func TestFormatSQL_OtherStatementsKeepTheirLines(t *testing.T) {
	src := "CREATE FUNCTION add(a int)\nRETURNS int\nLANGUAGE plpgsql\nAS $$\nBEGIN\n  RETURN a+1;\nEND;\n$$;\n" +
		"GRANT SELECT, INSERT ON ALL TABLES IN SCHEMA public TO app;"
	got := mustFormatSQL(t, src)
	checkSQL(t, got, `CREATE FUNCTION add(a int)
  RETURNS int
  LANGUAGE plpgsql
  AS $$
BEGIN
  RETURN a+1;
END;
$$;
GRANT SELECT, INSERT ON ALL TABLES IN SCHEMA public TO app;
`)
}

// TestFormatSQL_ViewQueryGetsLayout pins the switch from a DDL statement
// to query layout at `AS SELECT`.
func TestFormatSQL_ViewQueryGetsLayout(t *testing.T) {
	got := mustFormatSQL(t, "CREATE VIEW v AS SELECT a FROM t;")
	checkSQL(t, got, "CREATE VIEW v AS\nSELECT\n    a\nFROM\n    t;\n")
}

// TestFormatSQL_KeywordCaseFollowsTheFile pins the vote: a lower-case
// file stays lower-case, an upper-case one upper, and identifiers,
// function names and column-ish words (key, type) are never re-cased.
func TestFormatSQL_KeywordCaseFollowsTheFile(t *testing.T) {
	got := mustFormatSQL(t, "select key, type, Count(*) from kv where a is NOT null;")
	checkSQL(t, got, "select\n    key,\n    type,\n    Count(*)\nfrom\n    kv\nwhere\n    a is not null;\n")
	got = mustFormatSQL(t, "SELECT a FROM t where b;")
	if !strings.Contains(got, "WHERE") {
		t.Errorf("upper-case majority not applied:\n%s", got)
	}
}

// TestFormatSQL_KeepsIndentUnit pins indent.go's rule for SQL: a tab-
// indented file stays tabs.
func TestFormatSQL_KeepsIndentUnit(t *testing.T) {
	got := mustFormatSQL(t, "SELECT\n\ta\nFROM\n\tt;")
	checkSQL(t, got, "SELECT\n\ta\nFROM\n\tt;\n")
}

// TestFormatSQL_CommentsStayWhereTheyWere pins comment placement: a
// trailing comment stays trailing (even after a comma the layout breaks
// at), an own-line comment keeps its line, and a blank line between
// statements survives.
func TestFormatSQL_CommentsStayWhereTheyWere(t *testing.T) {
	got := mustFormatSQL(t, "-- header\n\nSELECT a, -- first\n    /* own */\n    b FROM t; -- done\n\n\n\nSELECT 2;")
	checkSQL(t, got, `-- header

SELECT
    a, -- first
    /* own */
    b
FROM
    t; -- done

SELECT
    2;
`)
}

// TestFormatSQL_SignsAndOperators pins operator spacing, the unary-minus
// judgement, and that a sign is never glued into a `--` comment opener.
func TestFormatSQL_SignsAndOperators(t *testing.T) {
	got := mustFormatSQL(t, "SELECT a-1, a - -1, x<-1, t.*, arr[1:2], data->>'k', $1::int FROM t WHERE a=-1;")
	checkSQL(t, got, `SELECT
    a - 1,
    a - -1,
    x < -1,
    t.*,
    arr[1:2],
    data ->> 'k',
    $1::int
FROM
    t
WHERE
    a = -1;
`)
}

// TestFormatSQL_StringContinuationKeepsItsNewline pins the one place
// whitespace is grammar: adjacent literals are concatenated only across
// a newline, so they must never be joined onto one line.
func TestFormatSQL_StringContinuationKeepsItsNewline(t *testing.T) {
	got := mustFormatSQL(t, "SELECT 'a'\n'b';")
	if strings.Contains(got, "'a' 'b'") {
		t.Errorf("continued literal joined onto one line:\n%s", got)
	}
}

// TestFormatSQL_CopyDataVerbatim pins pg_dump safety: COPY rows are
// written back byte for byte, tabs and quotes included.
func TestFormatSQL_CopyDataVerbatim(t *testing.T) {
	src := "COPY public.t (a, b) FROM stdin;\n1\tit's \"x\"\n2\t\\N\n\\.\n\nSELECT 1;\n"
	got := mustFormatSQL(t, src)
	if !strings.Contains(got, "FROM stdin;\n1\tit's \"x\"\n2\t\\N\n\\.\n") {
		t.Errorf("COPY data changed:\n%q", got)
	}
}

// TestFormatSQL_RefusesWithPosition pins the failure contract: SQL that
// does not lex or balance is refused with a 1-based line and column.
func TestFormatSQL_RefusesWithPosition(t *testing.T) {
	cases := map[string]string{
		"SELECT 1;\nSELECT 'oops":  "line 2, col 8: unterminated string",
		"SELECT (a FROM t;":        "line 1, col 8: unclosed '('",
		"SELECT a) FROM t;":        "line 1, col 9: unmatched ')'",
		"SELECT {{ col }} FROM t;": "line 1, col 8:",
		"SELECT `col` FROM t;":     "MySQL",
	}
	for src, want := range cases {
		_, err := formatSQL([]byte(src))
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("formatSQL(%q) err = %v, want containing %q", src, err, want)
		}
	}
}

// TestFormatSQL_EmptyFileUnchanged pins the empty-file rule shared with
// the JSON pass: a file someone just created formats to itself.
func TestFormatSQL_EmptyFileUnchanged(t *testing.T) {
	for _, src := range []string{"", "  \n"} {
		got, err := formatSQL([]byte(src))
		if err != nil || string(got) != src {
			t.Errorf("formatSQL(%q) = %q, %v; want unchanged", src, got, err)
		}
	}
}

// TestFormatSQL_KeepsCRLFAndBOM pins line-ending and BOM round trips: a
// CRLF file comes back CRLF throughout, with its BOM.
func TestFormatSQL_KeepsCRLFAndBOM(t *testing.T) {
	got, err := formatSQL([]byte("\xef\xbb\xbfSELECT a\r\nFROM t;\r\n"))
	if err != nil {
		t.Fatal(err)
	}
	want := "\xef\xbb\xbfSELECT\r\n    a\r\nFROM\r\n    t;\r\n"
	if string(got) != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

// TestSameSQLTokens pins the safety net's comparison: keyword case is
// not a difference, anything else is.
func TestSameSQLTokens(t *testing.T) {
	a, _ := lexSQL("select 'X' from T")
	b, _ := lexSQL("SELECT\n  'X'\nFROM t")
	if !sameSQLTokens(a, b) {
		t.Error("case/whitespace difference reported as a change")
	}
	c, _ := lexSQL("SELECT 'x' FROM t")
	if sameSQLTokens(a, c) {
		t.Error("string content difference not reported")
	}
	d, _ := lexSQL("select 'X' from T, u")
	if sameSQLTokens(a, d) {
		t.Error("extra tokens not reported")
	}
}

// TestDetectSQLStyle pins the convention reader the external tools are
// fed from: the indent unit, and the keyword-case vote with no evidence
// reported as unknown.
func TestDetectSQLStyle(t *testing.T) {
	st := detectSQLStyle([]byte("select\n\ta\nfrom t;"))
	if !st.indent.Tabs || !st.caseKnown || st.upper {
		t.Errorf("style = %+v, want tabs, known lower case", st)
	}
	st = detectSQLStyle([]byte("foo(bar);"))
	if st.caseKnown {
		t.Errorf("style = %+v, want case unknown", st)
	}
	// A sample cut mid-string still votes with what it lexed.
	st = detectSQLStyle([]byte("SELECT a FROM t WHERE b = 'cut"))
	if !st.caseKnown || !st.upper {
		t.Errorf("truncated sample style = %+v, want known upper", st)
	}
}

// TestSQLIsKeyword_Context pins the identifier guards: words that are
// keywords only beside the right neighbour, function-like keywords
// followed by `(`, and names after a dot or FUNCTION.
func TestSQLIsKeyword_Context(t *testing.T) {
	cases := []struct {
		src  string
		idx  int
		want bool
	}{
		{"PRIMARY KEY", 1, true},
		{"SELECT key", 1, false},
		{"left(s, 3)", 0, false},
		{"LEFT JOIN", 0, true},
		{"t.end", 2, false},
		{"CREATE FUNCTION add(", 2, false},
		{"LANGUAGE plpgsql", 0, true},
		{"SELECT language FROM t", 1, false},
	}
	for _, tc := range cases {
		toks, _ := lexSQL(tc.src)
		if got := sqlIsKeyword(toks, tc.idx); got != tc.want {
			t.Errorf("sqlIsKeyword(%q, %d) = %v, want %v", tc.src, tc.idx, got, tc.want)
		}
	}
}
