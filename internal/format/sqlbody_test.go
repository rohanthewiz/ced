// =============================================================================
// File: internal/format/sqlbody_test.go
// Author: Rohan Allison <rohanthewiz@gmail.com>
// Created: 2026-11-13
// Copyright: 2026 Rohan Allison. All rights reserved.
// =============================================================================

package format

import (
	"strings"
	"testing"
)

// TestFormatSQL_PLpgSQLBlocks pins the PL/pgSQL block layout end to end:
// DECLARE and BEGIN at the body's depth, IF / ELSIF / ELSE arms, a
// SELECT … INTO STRICT and a RETURNING … INTO as clauses, EXCEPTION with
// its WHEN handlers one level in and their statements two, `%TYPE`
// glued, and the whole body at column 0 under `AS $$`.
func TestFormatSQL_PLpgSQLBlocks(t *testing.T) {
	got := mustFormatSQL(t, "CREATE FUNCTION bump(p_id users.id % TYPE) RETURNS int LANGUAGE plpgsql AS $$\n"+
		"DECLARE n int := 0; BEGIN\n"+
		"  SELECT count(*) INTO STRICT n FROM users WHERE id = p_id;\n"+
		"  IF n = 0 THEN RAISE NOTICE 'none %', p_id; RETURN -1;\n"+
		"  ELSIF n > 1 THEN RETURN 0;\n"+
		"  ELSE UPDATE users SET n = n + 1 WHERE id = p_id RETURNING n INTO n;\n"+
		"  END IF;\n"+
		"  RETURN n;\n"+
		"EXCEPTION WHEN unique_violation OR foreign_key_violation THEN RETURN NULL; WHEN OTHERS THEN RAISE;\n"+
		"END;\n$$;")
	checkSQL(t, got, `CREATE FUNCTION bump(p_id users.id%TYPE) RETURNS int LANGUAGE plpgsql AS $$
DECLARE
  n int := 0;
BEGIN
  SELECT
    count(*)
  INTO STRICT
    n
  FROM
    users
  WHERE
    id = p_id;
  IF n = 0 THEN
    RAISE NOTICE 'none %', p_id;
    RETURN -1;
  ELSIF n > 1 THEN
    RETURN 0;
  ELSE
    UPDATE users
    SET
      n = n + 1
    WHERE
      id = p_id
    RETURNING
      n
    INTO
      n;
  END IF;
  RETURN n;
EXCEPTION
  WHEN unique_violation OR foreign_key_violation THEN
    RETURN NULL;
  WHEN OTHERS THEN
    RAISE;
END;
$$;
`)
}

// TestFormatSQL_PLpgSQLLoopsAndLabels pins loops and the statement CASE: a
// `<<label>>` on its own line and glued, FOR … IN's query laid out one
// level in with LOOP back at the FOR's depth, a one-line WHILE opened up,
// CASE arms one level in and their statements two, RETURN QUERY's query
// one level in, and a nested DECLARE block. A DO body with no LANGUAGE is
// PL/pgSQL.
func TestFormatSQL_PLpgSQLLoopsAndLabels(t *testing.T) {
	got := mustFormatSQL(t, "DO $$\nBEGIN\n<< outer >>\nFOR r IN SELECT id FROM t WHERE ok LOOP\n"+
		"  CONTINUE outer WHEN r.id % 2 = 0;\n"+
		"  CASE r.id WHEN 1 THEN PERFORM a(); ELSE NULL; END CASE;\n"+
		"END LOOP outer;\n"+
		"WHILE x > 0 LOOP x := x - 1; END LOOP;\n"+
		"DECLARE y int; BEGIN y := 1; END;\n"+
		"RETURN QUERY SELECT a FROM t;\n"+
		"END $$;")
	checkSQL(t, got, `DO $$
BEGIN
  <<outer>>
  FOR r IN
    SELECT
      id
    FROM
      t
    WHERE
      ok
  LOOP
    CONTINUE outer WHEN r.id % 2 = 0;
    CASE r.id
      WHEN 1 THEN
        PERFORM a();
      ELSE
        NULL;
    END CASE;
  END LOOP outer;
  WHILE x > 0 LOOP
    x := x - 1;
  END LOOP;
  DECLARE
    y int;
  BEGIN
    y := 1;
  END;
  RETURN QUERY
    SELECT
      a
    FROM
      t;
END
$$;
`)
}

// TestFormatSQL_PLpgSQLKeepsAuthorBreaks pins the header rules: a THEN
// the author put on its own line lines up with its IF, a broken
// condition continues one level in, and blank lines and own-line
// comments between statements are kept.
func TestFormatSQL_PLpgSQLKeepsAuthorBreaks(t *testing.T) {
	got := mustFormatSQL(t, "DO $$\nBEGIN\n  -- check\n  IF a\n  AND b\n  THEN\n    NULL;\n  END IF;\n\n\n  PERFORM 1;\nEND $$;")
	checkSQL(t, got, `DO $$
BEGIN
  -- check
  IF a
    AND b
  THEN
    NULL;
  END IF;

  PERFORM 1;
END
$$;
`)
}

// TestFormatSQL_SQLBody pins a LANGUAGE sql body: laid out by the plain
// SQL layout under its `AS $$`, the LANGUAGE clause after the body found
// all the same, and the closing delimiter on its own line.
func TestFormatSQL_SQLBody(t *testing.T) {
	got := mustFormatSQL(t, "CREATE FUNCTION add(a int, b int) RETURNS int AS $$ SELECT a + b $$ LANGUAGE sql IMMUTABLE;")
	checkSQL(t, got, "CREATE FUNCTION add(a int, b int) RETURNS int AS $$\nSELECT\n    a + b\n$$ LANGUAGE sql IMMUTABLE;\n")
}

// TestFormatSQL_BodiesKeptVerbatim pins the bodies that are left exactly
// as written while the file around them is still formatted: other
// languages, a string handed to EXECUTE, a non-body dollar string, PL/pgSQL
// whose blocks do not pair up, a `#` compiler option, and an empty body.
func TestFormatSQL_BodiesKeptVerbatim(t *testing.T) {
	for _, body := range []string{
		"CREATE FUNCTION f() RETURNS int AS $$ my  $x = 1; $$ LANGUAGE plperl;",
		"SELECT $q$  keep   this $q$;",
		"DO $$ BEGIN IF true THEN PERFORM 1; END $$;",
		"DO $$ BEGIN PERFORM 1; $$;",
		"DO $$\n#variable_conflict use_column\nBEGIN PERFORM 1; END $$;",
		"CREATE FUNCTION f() RETURNS void AS $$  $$ LANGUAGE sql;",
	} {
		out, err := formatSQL([]byte(body))
		if err != nil {
			t.Errorf("%q: %v", body, err)
			continue
		}
		dollar := body[strings.IndexByte(body, '$'):strings.LastIndexByte(body, '$')]
		if !strings.Contains(string(out), dollar) {
			t.Errorf("body re-laid, want verbatim:\n%s", out)
		}
	}

	// The string inside a laid-out body is still a string.
	got := mustFormatSQL(t, "DO $$BEGIN EXECUTE $q$SELECT   1$q$; END$$;")
	if !strings.Contains(got, "EXECUTE $q$SELECT   1$q$;") {
		t.Errorf("EXECUTE string re-spaced:\n%s", got)
	}
}

// TestFormatSQL_BodyCaseJoinsTheVote pins that a body's keywords vote on
// the file's keyword case: an upper-case header over a lower-case body
// comes out lower-case throughout.
func TestFormatSQL_BodyCaseJoinsTheVote(t *testing.T) {
	got := mustFormatSQL(t, "CREATE FUNCTION f() RETURNS void LANGUAGE plpgsql AS $$\nbegin\n  if true then raise notice 'x'; end if;\nend;\n$$;")
	checkSQL(t, got, `create function f() returns void language plpgsql as $$
begin
  if true then
    raise notice 'x';
  end if;
end;
$$;
`)
}

// TestFormatSQL_BodyKeepsCRLF pins that a consistently CRLF file with a
// body comes back CRLF throughout — the body's new lines included.
func TestFormatSQL_BodyKeepsCRLF(t *testing.T) {
	got := mustFormatSQL(t, "DO $$\r\nBEGIN PERFORM 1; END\r\n$$;\r\n")
	if got != "DO $$\r\nBEGIN\r\n    PERFORM 1;\r\nEND\r\n$$;\r\n" {
		t.Errorf("got %q", got)
	}
}

// TestRoutineBodyLang pins which dollar strings are bodies: the AS body
// of a CREATE FUNCTION / PROCEDURE in sql or plpgsql (LANGUAGE before or
// after, as a word or a string), a DO block (default plpgsql); never a
// parameter named language, another language, or a plain dollar string.
func TestRoutineBodyLang(t *testing.T) {
	cases := []struct {
		src, want string
	}{
		{"CREATE OR REPLACE FUNCTION f() RETURNS int LANGUAGE sql AS $$x$$;", "sql"},
		{"CREATE PROCEDURE p() AS $b$x$b$ LANGUAGE 'plpgsql';", "plpgsql"},
		{"CREATE FUNCTION f(language text) RETURNS int AS $$x$$ LANGUAGE sql;", "sql"},
		{"CREATE FUNCTION f(language plpgsql) RETURNS int AS $$x$$;", ""},
		{"CREATE FUNCTION f() RETURNS int AS $$x$$ LANGUAGE plperl;", ""},
		{"DO $$x$$;", "plpgsql"},
		{"DO LANGUAGE sql $$x$$;", "sql"},
		{"DO $$x$$ LANGUAGE plv8;", ""},
		{"SELECT $$x$$;", ""},
		{"CREATE FUNCTION f() RETURNS text LANGUAGE sql RETURN $$x$$;", ""},
	}
	for _, c := range cases {
		toks, err := lexSQL(c.src)
		if err != nil {
			t.Fatalf("%q: %v", c.src, err)
		}
		got := "none"
		for i, tk := range toks {
			if tk.kind == sqlDollar {
				got = routineBodyLang(toks, i)
				break
			}
		}
		if got != c.want {
			t.Errorf("%q: got %q, want %q", c.src, got, c.want)
		}
	}
}

// TestSplitDollar pins the delimiter split, plain and tagged, and the
// refusal of text that is not a dollar string.
func TestSplitDollar(t *testing.T) {
	tag, body, ok := splitDollar("$fn$ a $fn$")
	if !ok || tag != "$fn$" || body != " a " {
		t.Errorf("tagged: %q %q %v", tag, body, ok)
	}
	tag, body, ok = splitDollar("$$$$")
	if !ok || tag != "$$" || body != "" {
		t.Errorf("empty: %q %q %v", tag, body, ok)
	}
	if _, _, ok := splitDollar("'x'"); ok {
		t.Error("a quoted string split as a dollar string")
	}
	if _, _, ok := splitDollar("$a$ x $b$"); ok {
		t.Error("mismatched delimiters accepted")
	}
}

// TestSameRoutineBody pins the body comparison: layout and keyword case
// are not differences; a changed token or a different delimiter is.
func TestSameRoutineBody(t *testing.T) {
	if !sameRoutineBody("$$BEGIN NULL; END$$", "$$\nbegin\n  null;\nend\n$$") {
		t.Error("layout difference reported")
	}
	if sameRoutineBody("$$SELECT 1$$", "$$SELECT 2$$") {
		t.Error("changed token not reported")
	}
	if sameRoutineBody("$$SELECT 1$$", "$a$SELECT 1$a$") {
		t.Error("changed delimiter not reported")
	}
	if sameRoutineBody("$$SELECT 'x$$", "$$SELECT 'x$$") {
		t.Error("an unlexable body compared equal by its tokens")
	}
}

// TestPLIsKeyword pins the PL/pgSQL words and their context rules: RAISE
// levels only after RAISE, QUERY only after RETURN, GET only before
// DIAGNOSTICS, TYPE only after %, and never a qualified name's part.
func TestPLIsKeyword(t *testing.T) {
	toks, _ := lexSQL("RAISE NOTICE x; notice; RETURN QUERY q; query; GET DIAGNOSTICS; get; r.loop; LOOP; a%TYPE")
	want := map[string]bool{"RAISE": true, "NOTICE": true, "RETURN": true, "QUERY": true, "GET": true,
		"DIAGNOSTICS": true, "LOOP": true, "TYPE": true}
	for i, tk := range toks {
		if tk.kind != sqlWord {
			continue
		}
		if got := plIsKeyword(toks, i); got != want[tk.text] {
			t.Errorf("%q at %d: got %v", tk.text, i, got)
		}
	}
}

// TestSQLIsTypeSuffix pins the words a `%` glues to.
func TestSQLIsTypeSuffix(t *testing.T) {
	if !sqlIsTypeSuffix(sqlToken{kind: sqlWord, text: "rowtype"}) || !sqlIsTypeSuffix(sqlToken{kind: sqlWord, text: "TYPE"}) {
		t.Error("TYPE / ROWTYPE not recognised")
	}
	if sqlIsTypeSuffix(sqlToken{kind: sqlQuotedIdent, text: `"type"`}) || sqlIsTypeSuffix(sqlToken{kind: sqlWord, text: "types"}) {
		t.Error("a quoted name or another word recognised")
	}
}

// TestSQLStmtStop pins the tokens that bound a routine's statement.
func TestSQLStmtStop(t *testing.T) {
	if !sqlStmtStop(sqlToken{kind: sqlPunct, text: ";"}) || !sqlStmtStop(sqlToken{kind: sqlMeta, text: `\i x`}) {
		t.Error("; or a meta-command not a stop")
	}
	if sqlStmtStop(sqlToken{kind: sqlPunct, text: ","}) {
		t.Error("a comma ends a statement")
	}
}

// TestFormatRoutineBody_Direct pins the body formatter's own answer: a
// laid-out body at the depth asked for reports true, a refused one hands
// the token back untouched with false.
func TestFormatRoutineBody_Direct(t *testing.T) {
	out, ok := formatRoutineBody("$$SELECT 1$$", "sql", "  ", true, 1)
	if !ok || out != "$$\n  SELECT\n    1\n  $$" {
		t.Errorf("got %q %v", out, ok)
	}
	out, ok = formatRoutineBody("$$END IF;$$", "plpgsql", "  ", true, 0)
	if ok || out != "$$END IF;$$" {
		t.Errorf("unpaired END: got %q %v", out, ok)
	}
}

// TestPLIsLabel pins where a word is a block label (kept as written even
// when it spells a keyword): after `<<`, EXIT / CONTINUE, END and END
// LOOP at a statement's end — but not END IF's IF, EXIT's WHEN, or a word
// after an expression CASE's END.
func TestPLIsLabel(t *testing.T) {
	toks, _ := lexSQL("<<outer>> EXIT outer WHEN a; CONTINUE outer; END LOOP outer; END IF; EXIT WHEN b; END blk; SELECT CASE WHEN c THEN 1 END AS x;")
	want := map[int]bool{}
	var labels []string
	for i, tk := range toks {
		if tk.kind == sqlWord && (tk.text == "outer" || tk.text == "blk") {
			want[i] = true
		}
	}
	for i, tk := range toks {
		got := plIsLabel(toks, i)
		if got != want[i] {
			t.Errorf("%q at %d: got %v", tk.text, i, got)
		}
		if got {
			labels = append(labels, tk.text)
		}
	}
	if len(labels) != 5 {
		t.Errorf("labels found: %v", labels)
	}
}
