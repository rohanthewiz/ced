// =============================================================================
// File: internal/format/sqlfmt.go
// Author: Rohan Allison <rohanthewiz@gmail.com>
// Created: 2026-10-16
// Copyright: 2026 Rohan Allison. All rights reserved.
// =============================================================================

// sqlfmt.go is ced's own PostgreSQL formatter — the in-process floor of
// the SQL ladder, reached only when the project has no format.json entry
// and no pg_format / sql-formatter is installed (builtin.go). It exists
// for the reason the JSON pass does: no SQL formatter ships with a Go
// toolchain, and a Format verb that is inert on most machines is a verb
// nobody can observe.
//
// # The contract: whitespace and keyword case, nothing else
//
// The pass is token-preserving. It decides where lines break and how deep
// they indent, and it writes keywords in one case; every other byte —
// identifiers, strings, numbers, comments, dollar-quoted function bodies,
// COPY data — is copied from the source. That contract is ENFORCED, not
// hoped for: the output is re-lexed and compared token-for-token with the
// input (words case-insensitively, which is exactly PostgreSQL's own rule
// for unquoted names), and any difference refuses the format with the
// file untouched. A layout bug can therefore produce an ugly line, never
// different SQL.
//
// Keyword case is the only non-whitespace change, and it is safe because
// unquoted words fold to lower case in PostgreSQL anyway. Function names
// and identifiers are left as written — only the structural words in
// sqlKeywords are re-cased, so `SELECT count(*)` stays lower-case `count`.
//
// # The layout
//
//	SELECT                       clause keyword on its own line,
//	    a,                       its body one level in, one item per
//	    b                        line at top-level commas
//	FROM
//	    t
//	    JOIN u ON u.id = t.id    joins one level in, under FROM
//	        AND u.ok             their AND/OR one level further
//	WHERE
//	    x = 1
//	    AND y BETWEEN 1 AND 2    BETWEEN's AND never breaks
//	    AND z IN (
//	        SELECT               a parenthesised query is a block of
//	            id               its own, closed at the opener's depth
//	        FROM
//	            v
//	    );
//
// Only DML (and the query of a CREATE VIEW … AS / EXPLAIN / PREPARE …
// AS) gets clause layout. Every other statement — DDL, GRANT, SET, DO —
// keeps the author's own line breaks, re-indented one level for
// continuation lines, because the clause vocabulary there is open-ended
// (`GRANT SELECT ON …`, `CREATE TRIGGER … BEFORE INSERT OR UPDATE`) and a
// rule that breaks before SELECT would wreck it. CREATE TABLE's column
// list is the one DDL shape laid out (one column per line), since that is
// the layout every Postgres codebase converges on.
//
// What the pass deliberately does NOT do: wrap long lines, align, or
// look inside a $$ function body. Wrapping needs a width policy the file
// cannot express; a PL/pgSQL body is a second language whose blocks the
// author has already laid out, and it is a string to the SQL grammar.
//
// # The file keeps its conventions
//
// Like the JSON pass, the file's own indentation unit is kept (indent.go;
// four spaces when it has none — pg_format's default). Keyword case is
// decided the same way, by VOTE: a file written in lower-case keywords
// stays lower-case, so a one-line fix in a lower-case migration is not a
// whole-file diff. No evidence means upper case, the documentation's
// spelling and pg_format's default.

package format

import (
	"bytes"
	"errors"
	"fmt"
	"strings"
)

// sqlIndent is the indentation unit for a file that offers no evidence
// of its own: pg_format's default, so a project that later installs it
// sees no churn.
const sqlIndent = "    "

// sqlKeywords are the words the formatter re-cases. All of PostgreSQL's
// reserved words (which cannot be bare column names, so re-casing them
// can never touch an identifier the author spelled on purpose) plus the
// unreserved words that do structural work in everyday SQL and are rare
// as column names. Words that are common column names — name, type,
// value, key, data, language, action, … — are absent, or appear in
// sqlContextKeywords with the neighbour that proves they are keywords.
var sqlKeywords = setOf(
	// Reserved.
	"ALL", "ANALYSE", "ANALYZE", "AND", "ANY", "ARRAY", "AS", "ASC", "ASYMMETRIC",
	"BOTH", "CASE", "CAST", "CHECK", "COLLATE", "COLUMN", "CONSTRAINT", "CREATE",
	"CURRENT_CATALOG", "CURRENT_DATE", "CURRENT_ROLE", "CURRENT_TIME",
	"CURRENT_TIMESTAMP", "CURRENT_USER", "DEFAULT", "DEFERRABLE", "DESC",
	"DISTINCT", "DO", "ELSE", "END", "EXCEPT", "FALSE", "FETCH", "FOR", "FOREIGN",
	"FROM", "GRANT", "GROUP", "HAVING", "IN", "INITIALLY", "INTERSECT", "INTO",
	"LATERAL", "LEADING", "LIMIT", "LOCALTIME", "LOCALTIMESTAMP", "NOT", "NULL",
	"OFFSET", "ON", "ONLY", "OR", "ORDER", "PLACING", "PRIMARY", "REFERENCES",
	"RETURNING", "SELECT", "SESSION_USER", "SOME", "SYMMETRIC", "SYSTEM_USER",
	"TABLE", "THEN", "TO", "TRAILING", "TRUE", "UNION", "UNIQUE", "USER", "USING",
	"VARIADIC", "WHEN", "WHERE", "WINDOW", "WITH",
	// Reserved, but allowed as function or type names.
	"AUTHORIZATION", "BINARY", "COLLATION", "CONCURRENTLY", "CROSS",
	"CURRENT_SCHEMA", "FREEZE", "FULL", "ILIKE", "INNER", "IS", "ISNULL", "JOIN",
	"LEFT", "LIKE", "NATURAL", "NOTNULL", "OUTER", "OVERLAPS", "RIGHT", "SIMILAR",
	"TABLESAMPLE", "VERBOSE",
	// Unreserved, structural, and rare as column names.
	"ADD", "AFTER", "ALTER", "ALWAYS", "BEFORE", "BEGIN", "BETWEEN", "BY", "CALL",
	"CASCADE", "COMMIT", "CONFLICT", "COPY", "CURSOR", "DEALLOCATE", "DECLARE",
	"DEFINER", "DELETE", "DISABLE", "DROP", "EACH", "ENABLE", "ESCAPE", "EXECUTE",
	"EXISTS", "EXPLAIN", "EXTENSION", "FILTER", "FIRST", "FOLLOWING", "FUNCTION",
	"GENERATED", "IDENTITY", "IF", "IMMUTABLE", "INHERITS", "INOUT", "INSERT",
	"INSTEAD", "INVOKER", "LAST", "LISTEN", "LOCK", "LOCKED", "MATCHED",
	"MATERIALIZED", "MERGE", "NEXT", "NO", "NOTHING", "NOTIFY", "NOWAIT", "NULLS",
	"OF", "OPTION", "OUT", "OVER", "PARTITION", "POLICY", "PRECEDING", "PREPARE",
	"PRIVILEGES", "PROCEDURE", "RECURSIVE", "RELEASE", "RENAME", "REPLACE",
	"RESTRICT", "RETURNS", "REVOKE", "ROLLBACK", "ROW", "ROWS", "SAVEPOINT",
	"SECURITY", "SET", "SETOF", "SHARE", "SKIP", "STABLE", "STORED", "STRICT",
	"TEMPORARY", "TRANSACTION", "TRIGGER", "TRUNCATE", "UNBOUNDED", "UNLOGGED",
	"UPDATE", "VACUUM", "VALUES", "VIEW", "VOLATILE", "WITHIN",
)

// sqlContextKeywords are words that are keywords only after a particular
// neighbour — `PRIMARY KEY` but a `key` column, `CREATE TYPE` but a
// `type` column. Keyed by the word, valued by the previous words that
// prove it.
var sqlContextKeywords = map[string]map[string]bool{
	"KEY":      setOf("PRIMARY", "FOREIGN"),
	"TYPE":     setOf("CREATE", "DROP", "ALTER", "DATA"),
	"INDEX":    setOf("CREATE", "DROP", "ALTER", "UNIQUE"),
	"SCHEMA":   setOf("CREATE", "DROP", "ALTER"),
	"SEQUENCE": setOf("CREATE", "DROP", "ALTER"),
	"ROLE":     setOf("CREATE", "DROP", "ALTER"),
}

// sqlLanguages prove a preceding LANGUAGE is the clause of CREATE
// FUNCTION / DO rather than a column named language.
var sqlLanguages = setOf("SQL", "PLPGSQL", "C", "INTERNAL", "PLPERL", "PLPERLU",
	"PLPYTHON3U", "PLTCL", "PLTCLU", "PLV8")

// sqlFuncLike are keywords that double as ordinary function names
// (`left(s, 3)`, `replace(s, 'a', 'b')`). Followed by `(` they are calls,
// and are left exactly as written.
var sqlFuncLike = setOf("LEFT", "RIGHT", "REPLACE", "ADD")

// sqlCallable are keywords conventionally glued to their parenthesis
// (`CAST(x AS int)`, `ARRAY(SELECT …)`, `= ANY(arr)`). The author's
// spacing is kept for these; every other keyword gets a space before `(`
// (`IN (`, `VALUES (`, `OVER (`).
var sqlCallable = setOf("CAST", "ANY", "SOME", "ALL", "ARRAY", "ROW", "EXISTS",
	"CURRENT_TIME", "CURRENT_TIMESTAMP", "LOCALTIME", "LOCALTIMESTAMP")

// sqlValueWords are keywords that END an operand, so a following +/- is
// binary (`END - 1`, `NULL + x`). After any other keyword it is unary
// (`THEN -1`, `SELECT -x`).
var sqlValueWords = setOf("NULL", "TRUE", "FALSE", "END", "CURRENT_DATE",
	"CURRENT_TIME", "CURRENT_TIMESTAMP", "LOCALTIME", "LOCALTIMESTAMP",
	"CURRENT_USER", "SESSION_USER", "USER", "CURRENT_ROLE", "CURRENT_CATALOG",
	"CURRENT_SCHEMA", "SYSTEM_USER")

// sqlQueryStarters open a statement that gets clause layout.
var sqlQueryStarters = setOf("SELECT", "WITH", "VALUES", "INSERT", "UPDATE", "DELETE", "MERGE")

// sqlBodyClauses take a body on the lines below them, broken at
// top-level commas. GROUP and ORDER are listed by their first word; the
// BY is found by lookahead.
var sqlBodyClauses = setOf("SELECT", "FROM", "WHERE", "HAVING", "RETURNING", "WINDOW", "SET", "VALUES", "GROUP", "ORDER")

// sqlLineClauses start their own line but keep their (short) body on it.
var sqlLineClauses = setOf("LIMIT", "OFFSET", "FETCH", "FOR")

// sqlJoinWords can open a join (`LEFT OUTER JOIN`, `CROSS JOIN`, …).
var sqlJoinWords = setOf("JOIN", "INNER", "LEFT", "RIGHT", "FULL", "OUTER", "CROSS", "NATURAL")

// sqlCreateTablePrefix are the words allowed between CREATE and TABLE
// for the statement to count as a table definition.
var sqlCreateTablePrefix = setOf("OR", "REPLACE", "GLOBAL", "LOCAL", "TEMP", "TEMPORARY", "UNLOGGED", "FOREIGN")

// setOf builds a membership set.
func setOf(words ...string) map[string]bool {
	m := make(map[string]bool, len(words))
	for _, w := range words {
		m[w] = true
	}
	return m
}

// formatSQL lays out src (see the file comment). An error means the file
// is not formattable — it does not lex, its parentheses do not balance,
// or the layout would have changed a token — and the caller must leave it
// untouched. Errors carry a 1-based line and column.
func formatSQL(src []byte) ([]byte, error) {
	if len(bytes.TrimSpace(src)) == 0 {
		// An empty file is the one someone just created; formatting it to
		// itself agrees with the JSON pass.
		return append([]byte(nil), src...), nil
	}

	// A BOM would lex as identifier bytes glued to the first keyword.
	// It is set aside and put back, as the editor's own save path does.
	bom := []byte("\xef\xbb\xbf")
	hasBOM := bytes.HasPrefix(src, bom)
	text := string(bytes.TrimPrefix(src, bom))

	// A consistently CRLF file is laid out in LF and converted back, so
	// the new line breaks match the old. A MIXED file is left in its
	// mixture: converting would also rewrite the line endings inside
	// multi-line strings, which are data.
	crlf := strings.Count(text, "\r\n") > 0 && strings.Count(text, "\r\n") == strings.Count(text, "\n")
	if crlf {
		text = strings.ReplaceAll(text, "\r\n", "\n")
	}

	toks, err := lexSQL(text)
	if err != nil {
		return nil, sqlPosError(text, err)
	}
	match, err := matchSQLParens(toks)
	if err != nil {
		return nil, sqlPosError(text, err)
	}

	style := detectSQLStyleTokens(text, toks)
	unit := style.indent.Unit(sqlIndent)
	out := layoutSQL(toks, match, unit, !style.caseKnown || style.upper)

	// The safety net — see the file comment. A failure here is a bug in
	// the layout, reported as such rather than written.
	after, err := lexSQL(out)
	if err != nil || !sameSQLTokens(toks, after) {
		return nil, errors.New("internal error: the layout would change the SQL; file left as is")
	}

	if crlf {
		out = strings.ReplaceAll(out, "\n", "\r\n")
	}
	res := []byte(out)
	if hasBOM {
		res = append(append([]byte(nil), bom...), res...)
	}
	return res, nil
}

// sqlPosError prefixes a lexing/balancing error with its 1-based line
// and column, the form a status-bar flash can point a user at.
func sqlPosError(text string, err error) error {
	var le *sqlLexError
	if !errors.As(err, &le) {
		return err
	}
	line, col := offsetToLineCol([]byte(text), le.off)
	return fmt.Errorf("line %d, col %d: %s", line+1, col+1, le.msg)
}

// matchSQLParens pairs every `(` with its `)`, returning the partner
// index for each paren token (-1 elsewhere). The layout needs it to look
// past a parenthesised group (`SELECT DISTINCT ON (a) …`), and an
// unbalanced file is refused here: parentheses never span a `;`, so an
// unclosed one at a statement end is a real error, not a layout puzzle.
func matchSQLParens(toks []sqlToken) ([]int, error) {
	match := make([]int, len(toks))
	var open []int
	for i, t := range toks {
		match[i] = -1
		if t.kind == sqlMeta || t.kind == sqlCopyData || (t.kind == sqlPunct && t.text == ";") {
			if len(open) > 0 {
				return nil, &sqlLexError{toks[open[len(open)-1]].off, "unclosed '('"}
			}
			continue
		}
		if t.kind != sqlPunct {
			continue
		}
		switch t.text {
		case "(":
			open = append(open, i)
		case ")":
			if len(open) == 0 {
				return nil, &sqlLexError{t.off, "unmatched ')'"}
			}
			o := open[len(open)-1]
			open = open[:len(open)-1]
			match[o], match[i] = i, o
		}
	}
	if len(open) > 0 {
		return nil, &sqlLexError{toks[open[len(open)-1]].off, "unclosed '('"}
	}
	return match, nil
}

// sameSQLTokens reports whether two token streams are the same SQL:
// same kinds, same text — words compared case-insensitively, everything
// else byte-for-byte.
func sameSQLTokens(a, b []sqlToken) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i].kind != b[i].kind {
			return false
		}
		if a[i].kind == sqlWord {
			if !strings.EqualFold(a[i].text, b[i].text) {
				return false
			}
		} else if a[i].text != b[i].text {
			return false
		}
	}
	return true
}

// sqlStyle is a file's own conventions, read before formatting so the
// formatter — ced's own or an external tool, via flags — can keep them.
type sqlStyle struct {
	indent Indent
	// caseKnown reports whether the file had any keyword to vote with;
	// upper is the winner (ties go to upper).
	caseKnown bool
	upper     bool
}

// detectSQLStyle reads src's indentation unit and keyword case. It never
// fails: a sample that ends mid-string votes with what it lexed.
func detectSQLStyle(src []byte) sqlStyle {
	text := string(src)
	toks, _ := lexSQL(text)
	return detectSQLStyleTokens(text, toks)
}

// detectSQLStyleTokens is detectSQLStyle for an already-lexed source.
func detectSQLStyleTokens(text string, toks []sqlToken) sqlStyle {
	st := sqlStyle{indent: DetectIndent([]byte(text))}
	upper, lower := 0, 0
	for i, t := range toks {
		if !sqlIsKeyword(toks, i) {
			continue
		}
		// Mixed-case spellings (`Select`) are no evidence either way.
		switch t.text {
		case strings.ToUpper(t.text):
			upper++
		case strings.ToLower(t.text):
			lower++
		}
	}
	st.caseKnown = upper+lower > 0
	st.upper = upper >= lower
	return st
}

// sqlIsKeyword reports whether toks[i] is a word the formatter re-cases
// (see sqlKeywords, sqlContextKeywords, sqlFuncLike).
func sqlIsKeyword(toks []sqlToken, i int) bool {
	t := toks[i]
	if t.kind != sqlWord {
		return false
	}
	up := strings.ToUpper(t.text)
	if sqlKeywords[up] {
		if sqlFuncLike[up] {
			if j := sqlNextSig(toks, i); j >= 0 && toks[j].kind == sqlPunct && toks[j].text == "(" {
				return false
			}
		}
		// A qualified name's parts are identifiers whatever they spell
		// (`t.end`), and so is whatever follows FUNCTION / PROCEDURE:
		// `CREATE FUNCTION add(…)` names a function, it is not ADD.
		if p := sqlPrevSig(toks, i); p >= 0 {
			pt := toks[p]
			if pt.kind == sqlPunct && (pt.text == "." || pt.text == "::") {
				return false
			}
			if pt.kind == sqlWord && (strings.EqualFold(pt.text, "FUNCTION") || strings.EqualFold(pt.text, "PROCEDURE")) {
				return false
			}
		}
		return true
	}
	if up == "LANGUAGE" {
		j := sqlNextSig(toks, i)
		return j >= 0 && (toks[j].kind == sqlString || (toks[j].kind == sqlWord && sqlLanguages[strings.ToUpper(toks[j].text)]))
	}
	if prev, ok := sqlContextKeywords[up]; ok {
		if p := sqlPrevSig(toks, i); p >= 0 && toks[p].kind == sqlWord {
			return prev[strings.ToUpper(toks[p].text)]
		}
	}
	return false
}

// sqlNextSig returns the index of the next non-comment token after i, or
// -1.
func sqlNextSig(toks []sqlToken, i int) int {
	for j := i + 1; j < len(toks); j++ {
		if toks[j].kind != sqlLineComment && toks[j].kind != sqlBlockComment {
			return j
		}
	}
	return -1
}

// sqlPrevSig returns the index of the previous non-comment token before
// i, or -1.
func sqlPrevSig(toks []sqlToken, i int) int {
	for j := i - 1; j >= 0; j-- {
		if toks[j].kind != sqlLineComment && toks[j].kind != sqlBlockComment {
			return j
		}
	}
	return -1
}

// sqlFrameKind names what a nesting level is.
type sqlFrameKind int

const (
	frameStmt     sqlFrameKind = iota // a whole statement
	frameSubquery                     // ( SELECT … ) — broken, clause layout
	frameList                         // CREATE TABLE t ( … ) — one item per line
	frameInline                       // any other ( … ) — never broken
	frameCase                         // CASE … END
)

// sqlFrame is one nesting level of the layout. The stack of them is the
// whole of the formatter's grammar knowledge: which clause the tokens
// belong to, and how deep its lines indent.
//
//	stack, bottom → top               while laying out
//	  stmt(query, clause=WHERE)        WHERE
//	  inline                             x IN (
//	  subquery(clause=SELECT)              SELECT …
//
// Clause layout reacts only to tokens whose TOP frame is a query frame:
// an ORDER BY inside `array_agg(x ORDER BY y)` or a FROM inside
// `extract(year FROM d)` sits under an inline frame and is left alone.
type sqlFrame struct {
	kind sqlFrameKind
	// base is the indent level of the frame's own lines (clause keywords;
	// a list's items; a CASE's WHEN/ELSE). close is where its closing
	// `)` or END goes.
	base, close int
	// query turns clause layout on. Statements decide at their first word
	// (or switch at `AS SELECT`); subqueries are always queries.
	query bool
	// broken is false only for a CASE nested in an inline group, which
	// stays on one line like the rest of the group.
	broken bool

	clause      string // current clause keyword ("" before the first)
	commaBreak  bool   // top-level commas end a line in this clause
	commaIndent int    // …and the next item starts here
	between     bool   // a BETWEEN is waiting for its AND
	inOn        bool   // inside a JOIN's ON condition

	// breakAt is a token index at which a new line starts at breakIndent:
	// the clause body after `SELECT DISTINCT ON (…)`, `ORDER BY`, `UNION
	// ALL`. Lookahead resolved once, applied when the loop gets there.
	breakAt, breakIndent int

	first       bool   // no significant token yet
	firstWord   string // the statement's first word
	prevWord    string // last significant word at this level, upper-cased
	prevSig     int    // index of the last significant token at this level
	explain     bool   // EXPLAIN … : the query starts at the first starter
	createHead  bool   // only CREATE [prefix words] so far
	createTable bool   // CREATE … TABLE: the next `name (` is a column list
}

// sqlWriter accumulates output lines. Line breaks and indentation are
// both written LAZILY — owed, then paid with the next token — for two
// reasons: a requested line that never gets a token leaves no trailing
// whitespace, and a trailing comment (`a, -- why`) can still be written
// onto the line the break was requested after.
type sqlWriter struct {
	b      strings.Builder
	unit   string
	nl     int // newlines owed before the next token: 0, 1, or 2 (a blank line)
	indent int // indent of the next line to be started
	empty  bool
}

// atStart reports whether the next token begins a line.
func (w *sqlWriter) atStart() bool { return w.empty || w.nl > 0 }

// line requests a new line at indent. Repeated requests collapse, and
// the last indent asked for wins, so a clause break after a forced break
// never doubles up.
func (w *sqlWriter) line(indent int) {
	if !w.empty && w.nl == 0 {
		w.nl = 1
	}
	w.indent = indent
}

// blank requests one empty line before the next token. Never at the very
// top of the output.
func (w *sqlWriter) blank() {
	if !w.empty {
		w.nl = 2
	}
}

// put writes text: after the owed newlines and the indent when it starts
// a line, else after one space when space is set.
func (w *sqlWriter) put(text string, space bool) {
	switch {
	case w.empty:
	case w.nl > 0:
		w.b.WriteString(strings.Repeat("\n", w.nl))
		w.b.WriteString(strings.Repeat(w.unit, w.indent))
	case space:
		w.b.WriteByte(' ')
	}
	w.b.WriteString(text)
	w.nl, w.empty = 0, false
}

// trail writes a trailing comment at the end of the line already written,
// leaving any owed line break owed.
func (w *sqlWriter) trail(text string) {
	if w.empty {
		w.put(text, false)
		return
	}
	w.b.WriteString(" " + text)
}

// sqlLayout is the formatter's state while walking the tokens.
type sqlLayout struct {
	toks  []sqlToken
	match []int
	upper bool
	w     *sqlWriter
	stack []*sqlFrame

	prev      int  // index of the last token written, -1 at the start
	prevUnary bool // the last token was a unary +/-: glue the operand
	// mustBreak: the next token starts a new line (after a line comment,
	// an own-line block comment, a statement end).
	mustBreak bool
	// stmtStart: the next token begins a statement; blank lines before
	// it are kept (collapsed to one).
	stmtStart bool
}

// layoutSQL runs the layout over toks and returns the text, ending in
// exactly one newline.
func layoutSQL(toks []sqlToken, match []int, unit string, upper bool) string {
	l := &sqlLayout{
		toks: toks, match: match, upper: upper,
		w:    &sqlWriter{unit: unit, empty: true},
		prev: -1, stmtStart: true,
	}
	l.resetStmt()
	for i := range toks {
		switch toks[i].kind {
		case sqlLineComment, sqlBlockComment:
			l.comment(i)
		case sqlCopyData:
			// Byte-exact rows: always at column 0, never re-spaced.
			l.w.line(0)
			l.w.put(toks[i].text, false)
			l.endStmt(i)
		case sqlMeta:
			l.meta(i)
		default:
			l.code(i)
		}
	}
	return l.w.b.String() + "\n"
}

// top returns the innermost frame.
func (l *sqlLayout) top() *sqlFrame { return l.stack[len(l.stack)-1] }

// resetStmt starts a fresh statement frame.
func (l *sqlLayout) resetStmt() {
	l.stack = []*sqlFrame{{kind: frameStmt, broken: true, first: true, breakAt: -1, prevSig: -1}}
}

// endStmt closes the statement after token i was written.
func (l *sqlLayout) endStmt(i int) {
	l.resetStmt()
	l.prev = i
	l.prevUnary = false
	l.mustBreak = true
	l.stmtStart = true
}

// meta writes a psql backslash command. One that started its own line
// stays at column 0; one trailing a query (`SELECT 1 \gset`) stays on
// it. Either way it ends the statement, as it does in psql.
func (l *sqlLayout) meta(i int) {
	t := l.toks[i]
	if t.nlBefore > 0 || l.w.atStart() || l.stmtStart {
		l.boundary(t)
		l.w.line(0)
		l.w.put(t.text, false)
	} else {
		l.w.put(t.text, true)
	}
	l.endStmt(i)
}

// boundary handles the gap before a statement's first token: a new line,
// plus one blank line if the source had any.
func (l *sqlLayout) boundary(t sqlToken) {
	if l.w.empty {
		return
	}
	l.w.line(0)
	if t.nlBefore >= 2 {
		l.w.blank()
	}
}

// comment places a comment. Own-line comments keep their own line, and a
// blank line above them; trailing comments stay trailing. Either kind
// can be followed by code only on a new line when it is a `--` comment
// (or an own-line block comment, whose author put the code below it).
func (l *sqlLayout) comment(i int) {
	t := l.toks[i]
	ownLine := t.nlBefore > 0 || l.w.empty
	switch {
	case !ownLine:
		// Trailing in the source, trailing in the output — even when the
		// layout has just asked for a break after the token it trails.
		l.w.trail(t.text)
	case l.stmtStart:
		l.boundary(t)
		l.w.put(t.text, false)
	default:
		l.w.line(l.commentIndent(i))
		if t.nlBefore >= 2 {
			l.w.blank()
		}
		l.w.put(t.text, false)
	}
	if t.kind == sqlLineComment || (ownLine && i+1 < len(l.toks) && l.toks[i+1].nlBefore > 0) {
		l.mustBreak = true
	}
	l.prev = i
	l.prevUnary = false
}

// commentIndent is the depth for an own-line comment: that of the code it
// precedes. A comment above a clause keyword sits at the clause's depth,
// one above a body item at the body's.
func (l *sqlLayout) commentIndent(i int) int {
	fr := l.top()
	if j := sqlNextSig(l.toks, i); j >= 0 && fr.query && l.toks[j].kind == sqlWord {
		up := strings.ToUpper(l.toks[j].text)
		if sqlBodyClauses[up] || sqlLineClauses[up] || up == "UNION" || up == "INTERSECT" || up == "EXCEPT" {
			return fr.base
		}
	}
	return l.contIndent()
}

// contIndent is where a line forced open mid-construct (after a `--`
// comment) continues: one level into the current frame's body.
func (l *sqlLayout) contIndent() int {
	fr := l.top()
	switch {
	case fr.kind == frameList || fr.kind == frameInline || fr.kind == frameCase:
		return fr.base
	case fr.first:
		return fr.base
	default:
		return fr.base + 1
	}
}

// code lays out one significant (non-comment) token: decide whether it
// starts a line, write it, then update the frames. The decision and the
// update are split around the write because a token can need both — a
// CASE starts a line as a body item AND opens a frame whose depth is
// measured from that line.
func (l *sqlLayout) code(i int) {
	t := l.toks[i]
	w := l.w
	brk := -1 // indent to start a new line at; -1 = stay on this line

	if l.stmtStart {
		l.boundary(t)
		l.stmtStart, l.mustBreak = false, false
	}
	fr := l.top()
	up := ""
	if t.kind == sqlWord {
		up = strings.ToUpper(t.text)
	}

	// A statement decides at its first significant token whether it is a
	// query; a non-query statement switches at `AS SELECT` / EXPLAIN's
	// query (see the file comment).
	if fr.kind == frameStmt {
		if fr.first {
			fr.firstWord = up
			fr.query = sqlQueryStarters[up]
			fr.explain = up == "EXPLAIN"
			fr.createHead = up == "CREATE"
		} else if !fr.query && sqlQueryStarters[up] && (fr.explain || l.prevIsWord(fr, "AS")) {
			fr.query, fr.first = true, true
			brk = fr.base
		}
	}

	if fr.breakAt == i {
		fr.breakAt = -1
		if !(t.kind == sqlPunct && (t.text == ";" || t.text == ")")) {
			brk = fr.breakIndent
		}
	}

	switch {
	case t.kind == sqlPunct && t.text == ")":
		// Pop to the matching paren frame (a CASE left open inside it is
		// malformed SQL, closed with it rather than leaking out).
		for len(l.stack) > 1 {
			f := l.top()
			l.stack = l.stack[:len(l.stack)-1]
			if f.kind == frameSubquery || f.kind == frameList {
				brk = f.close
				break
			}
			if f.kind == frameInline {
				break
			}
		}
		fr = l.top()
	case fr.kind == frameCase && fr.broken && (up == "WHEN" || up == "ELSE"):
		brk = fr.base
	case fr.kind == frameCase && up == "END":
		if fr.broken {
			brk = fr.close
		}
	case fr.query && t.kind == sqlWord && (fr.kind == frameStmt || fr.kind == frameSubquery):
		if b := l.clauseWord(i, fr, up); b >= 0 {
			brk = b
		}
	case fr.kind == frameStmt && !fr.query && t.nlBefore > 0 && !fr.first:
		// A non-query statement keeps the author's line breaks.
		brk = fr.base + 1
	}

	// Adjacent string literals are one literal in PostgreSQL ONLY when a
	// newline separates them ('a'\n'b'); on one line they are a syntax
	// error. The re-lex check cannot see this (the tokens are identical),
	// so it is guarded here.
	if brk < 0 && t.kind == sqlString && l.prev >= 0 && l.toks[l.prev].kind == sqlString {
		brk = l.contIndent()
	}
	if brk < 0 && l.mustBreak {
		brk = l.contIndent()
	}
	l.mustBreak = false
	if brk >= 0 {
		w.line(brk)
	}

	text := t.text
	if sqlIsKeyword(l.toks, i) {
		if l.upper {
			text = strings.ToUpper(text)
		} else {
			text = strings.ToLower(text)
		}
	}
	space := !w.atStart() && l.needSpace(i)
	unaryNow := t.kind == sqlOp && (t.text == "-" || t.text == "+") && l.unaryPosition()
	w.put(text, space)
	l.prev = i
	l.prevUnary = unaryNow

	l.after(i, fr, up)
}

// clauseWord applies clause layout to a word at a query frame's own
// level, returning the indent to break at (-1 for none) and updating the
// frame's clause state.
func (l *sqlLayout) clauseWord(i int, fr *sqlFrame, up string) int {
	toks := l.toks
	next := sqlNextSig(toks, i)
	nextUp := ""
	if next >= 0 && toks[next].kind == sqlWord {
		nextUp = strings.ToUpper(toks[next].text)
	}
	startBody := func(clause string, bodyAt int) int {
		fr.clause, fr.commaBreak, fr.commaIndent = clause, true, fr.base+1
		fr.between, fr.inOn = false, false
		fr.breakAt, fr.breakIndent = bodyAt, fr.base+1
		return fr.base
	}

	switch {
	case up == "WITH" && fr.first:
		// CTEs: `WITH a AS (…),` then the next CTE at the statement's own
		// depth, not a body depth.
		fr.clause, fr.commaBreak, fr.commaIndent = "WITH", true, fr.base
		return -1
	case (up == "INSERT" || up == "UPDATE" || up == "DELETE" || up == "MERGE") && fr.clause == "WITH":
		fr.clause, fr.commaBreak = "", false
		return fr.base
	case up == "SELECT":
		// The body starts after SELECT's modifiers: DISTINCT, DISTINCT ON
		// (…), ALL.
		j := next
		if j >= 0 && toks[j].kind == sqlWord {
			switch strings.ToUpper(toks[j].text) {
			case "DISTINCT":
				j = sqlNextSig(toks, j)
				if j >= 0 && toks[j].kind == sqlWord && strings.EqualFold(toks[j].text, "ON") {
					if p := sqlNextSig(toks, j); p >= 0 && toks[p].text == "(" && l.match[p] >= 0 {
						j = sqlNextSig(toks, l.match[p])
					}
				}
			case "ALL":
				j = sqlNextSig(toks, j)
			}
		}
		return startBody("SELECT", j)
	case up == "FROM":
		// DELETE FROM t and IS DISTINCT FROM are not the FROM clause.
		if fr.prevWord == "DELETE" || fr.prevWord == "DISTINCT" {
			return -1
		}
		return startBody("FROM", next)
	case (up == "GROUP" || up == "ORDER") && nextUp == "BY":
		return startBody(up+" BY", sqlNextSig(toks, next))
	case sqlBodyClauses[up]:
		// SET included: at a query's own level it only ever opens an
		// assignment list (UPDATE, ON CONFLICT DO UPDATE, MERGE). The
		// `SET search_path` statement is not a query, so never gets here.
		// Directly after `DO UPDATE` / `THEN UPDATE` it stays on that
		// line — those three words read as one verb.
		b := startBody(up, next)
		if up == "SET" && fr.prevWord == "UPDATE" {
			return -1
		}
		return b
	case sqlLineClauses[up] || (up == "ON" && nextUp == "CONFLICT") || (up == "WHEN" && fr.firstWord == "MERGE"):
		fr.clause, fr.commaBreak, fr.between, fr.inOn = up, false, false, false
		return fr.base
	case up == "UNION" || up == "INTERSECT" || up == "EXCEPT":
		fr.clause, fr.commaBreak = "", false
		j := next
		if nextUp == "ALL" || nextUp == "DISTINCT" {
			j = sqlNextSig(toks, next)
		}
		fr.breakAt, fr.breakIndent = j, fr.base
		return fr.base
	case fr.clause == "FROM" && sqlJoinWords[up] && !sqlJoinWords[fr.prevWord] && l.joinAhead(i):
		fr.inOn = false
		return fr.base + 1
	case up == "ON" && fr.clause == "FROM":
		fr.inOn = true
	case up == "BETWEEN":
		fr.between = true
	case up == "AND" && fr.between:
		fr.between = false
	case up == "AND" || up == "OR":
		switch {
		case fr.clause == "WHERE" || fr.clause == "HAVING":
			return fr.base + 1
		case fr.clause == "FROM" && fr.inOn:
			return fr.base + 2
		}
	}
	return -1
}

// joinAhead reports whether the words from i on spell a join
// (`LEFT OUTER JOIN`), as opposed to `left(…)` or a column named full.
func (l *sqlLayout) joinAhead(i int) bool {
	for j, n := i, 0; j >= 0 && n < 4; j, n = sqlNextSig(l.toks, j), n+1 {
		t := l.toks[j]
		if t.kind != sqlWord {
			return false
		}
		up := strings.ToUpper(t.text)
		if up == "JOIN" {
			return true
		}
		if !sqlJoinWords[up] {
			return false
		}
	}
	return false
}

// after updates the frames once token i is written: opening and closing
// frames, and the per-frame memory (previous word, CREATE TABLE state).
func (l *sqlLayout) after(i int, fr *sqlFrame, up string) {
	t := l.toks[i]
	w := l.w
	switch {
	case t.kind == sqlPunct && t.text == ";":
		l.endStmt(i)
		return
	case t.kind == sqlPunct && t.text == "(":
		kind := l.parenKind(i, fr)
		nf := &sqlFrame{kind: kind, base: w.indent + 1, close: w.indent, broken: true, breakAt: -1, prevSig: -1, first: true}
		if kind == frameSubquery {
			nf.query = true
		}
		if kind == frameList {
			fr.createTable = false
			nf.commaBreak, nf.commaIndent = true, nf.base
		}
		l.stack = append(l.stack, nf)
		if kind == frameSubquery || kind == frameList {
			w.line(nf.base)
		}
	case t.kind == sqlPunct && t.text == ",":
		if fr.commaBreak && (fr.kind == frameList || fr.clause != "") {
			w.line(fr.commaIndent)
		}
	case up == "CASE":
		broken := fr.kind != frameInline && (fr.kind != frameCase || fr.broken)
		l.stack = append(l.stack, &sqlFrame{kind: frameCase, broken: broken, base: w.indent + 1, close: w.indent, breakAt: -1, prevSig: -1})
	case up == "END" && fr.kind == frameCase:
		l.stack = l.stack[:len(l.stack)-1]
		fr = l.top()
	}

	// Per-frame memory, on the frame the token belongs to. A closing `)`
	// belongs to the frame it returns to, as the end of an operand.
	if t.kind == sqlPunct && t.text == ")" {
		fr = l.top()
	}
	if fr.createHead && up != "CREATE" {
		switch {
		case up == "TABLE":
			fr.createTable, fr.createHead = true, false
		case !sqlCreateTablePrefix[up]:
			fr.createHead = false
		}
	}
	fr.first = false
	fr.prevSig = i
	fr.prevWord = up
}

// parenKind classifies the `(` at i: a subquery when a query starts
// inside it, a CREATE TABLE column list right after the table's name,
// otherwise an inline group.
func (l *sqlLayout) parenKind(i int, fr *sqlFrame) sqlFrameKind {
	if j := sqlNextSig(l.toks, i); j >= 0 && l.toks[j].kind == sqlWord && sqlQueryStarters[strings.ToUpper(l.toks[j].text)] {
		return frameSubquery
	}
	if fr.kind == frameStmt && fr.createTable && fr.prevSig >= 0 {
		p := l.toks[fr.prevSig]
		if p.kind == sqlQuotedIdent || (p.kind == sqlWord && !sqlIsKeyword(l.toks, fr.prevSig)) {
			return frameList
		}
	}
	return frameInline
}

// prevIsWord reports whether the frame's previous significant token is
// the keyword kw.
func (l *sqlLayout) prevIsWord(fr *sqlFrame, kw string) bool {
	return fr.prevSig >= 0 && l.toks[fr.prevSig].kind == sqlWord && fr.prevWord == kw
}

// unaryPosition reports whether a +/- written now is a sign rather than
// a binary operator: nothing before it on the line, or an operator, an
// opening bracket, a comma, or a keyword that does not end an operand.
func (l *sqlLayout) unaryPosition() bool {
	if l.prev < 0 || l.w.atStart() {
		return true
	}
	p := l.toks[l.prev]
	switch p.kind {
	case sqlOp:
		return true
	case sqlPunct:
		return p.text == "(" || p.text == "," || p.text == "[" || p.text == ":="
	case sqlWord:
		return sqlIsKeyword(l.toks, l.prev) && !sqlValueWords[strings.ToUpper(p.text)]
	}
	return false
}

// needSpace decides the gap between the previous token and toks[i] on
// the same line. Gluing is only ever done where the two tokens cannot
// merge into one (the re-lex check backs this up): never word to word,
// never operator to operator.
func (l *sqlLayout) needSpace(i int) bool {
	if l.prev < 0 {
		return false
	}
	p, t := l.toks[l.prev], l.toks[i]
	if l.prevUnary {
		return t.kind == sqlOp
	}
	if p.kind == sqlLineComment || p.kind == sqlBlockComment {
		return true
	}
	if t.kind == sqlPunct {
		switch t.text {
		case ",", ";", ")", "]", ".", "::", ":":
			return false
		case "[":
			return !(p.kind == sqlWord || p.kind == sqlQuotedIdent || p.kind == sqlParam ||
				p.kind == sqlPsqlVar || (p.kind == sqlPunct && (p.text == ")" || p.text == "]")))
		}
	}
	if p.kind == sqlPunct {
		switch p.text {
		case "(", "[", ".", "::", ":":
			return false
		}
	}
	if t.kind == sqlPsqlVar {
		return t.spaceBefore
	}
	if t.kind == sqlPunct && t.text == "(" {
		switch {
		case p.kind == sqlWord && sqlIsKeyword(l.toks, l.prev) && !sqlCallable[strings.ToUpper(p.text)]:
			return true
		case p.kind == sqlWord, p.kind == sqlQuotedIdent, p.kind == sqlPunct && (p.text == ")" || p.text == "]"):
			// `varchar(10)` vs `INSERT INTO t (a)`: only the author knows
			// which a name followed by a paren is.
			return t.spaceBefore
		}
	}
	return true
}
