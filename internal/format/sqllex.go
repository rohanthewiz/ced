// =============================================================================
// File: internal/format/sqllex.go
// Author: Rohan Allison <rohanthewiz@gmail.com>
// Created: 2026-10-16
// Copyright: 2026 Rohan Allison. All rights reserved.
// =============================================================================

// sqllex.go splits PostgreSQL source into tokens for the in-process SQL
// formatter (sqlfmt.go). It is a LEXER ONLY — it knows what a string,
// a comment or an operator is, never what a statement means — and it
// follows the server's own scanner (src/backend/parser/scan.l) wherever
// the two could disagree about where one token ends and the next begins.
//
// # Why token boundaries are the whole safety story
//
// The formatter is allowed to change exactly two things: the whitespace
// BETWEEN tokens and the letter case of keywords. Everything inside a
// token — a string's bytes, a dollar-quoted function body, a comment,
// a COPY data block — is copied through verbatim. So the formatter can
// only corrupt SQL by disagreeing with Postgres about a boundary:
//
//	'it''s'        one string, not two          ('' is an escaped quote)
//	$fn$ … $fn$    one token, however many lines and quotes it holds
//	a<-1           `<` then `-1`, not an operator `<-`   (scan.l's trim rule)
//	--             a comment opener, even in the middle of an operator run
//
// sqlfmt.go re-lexes its own output and refuses to write anything whose
// token stream differs from the input's, so a mistake here costs a
// refused format, never a damaged file — but only if this lexer and
// scan.l agree. The rules below are transcribed from it, not invented.
//
// # psql, not just the server
//
// .sql files are overwhelmingly fed to psql (migrations, pg_dump output),
// so psql's additions are recognised too: backslash meta-commands run to
// the end of their line, `:name` / `:'name'` / `:"name"` interpolate
// variables, and a `COPY … FROM stdin;` (or `\copy … from stdin`) is
// followed by raw data lines up to a `\.` terminator. pg_dump writes
// exactly that, and tab-separated data reflowed by a formatter would be
// silently corrupted on the next restore.
//
// # What is refused
//
// A backtick or a brace is not PostgreSQL: the first is MySQL's identifier
// quote, the second a template (`{{ ref('x') }}` in a dbt model, Go/Jinja
// placeholders). Both would lex as separate punctuation here, and
// re-spacing them would break the file in ITS language, so the lexer stops
// with a positioned error and the formatter leaves the file alone.

package format

import (
	"fmt"
	"strings"
)

// sqlTokKind classifies a token. The layout pass keys almost everything
// off sqlWord and sqlPunct; the remaining kinds exist so that their
// contents are never inspected or re-spaced.
type sqlTokKind int

const (
	sqlWord         sqlTokKind = iota // unquoted identifier or keyword
	sqlQuotedIdent                    // "name", U&"name"
	sqlString                         // 'x', E'x', B'01', X'ff', N'x', U&'x'
	sqlDollar                         // $tag$ … $tag$ (function bodies, DO blocks)
	sqlNumber                         // 42, 1.5e3, 0xff, 1_000
	sqlParam                          // $1
	sqlPsqlVar                        // :name, :'name', :"name"
	sqlOp                             // an operator run: =, <>, ->>, ||, *, …
	sqlPunct                          // ( ) [ ] , ; . :: : :=
	sqlLineComment                    // -- to end of line (newline excluded)
	sqlBlockComment                   // /* … */, nested
	sqlMeta                           // psql backslash command, to end of line
	sqlCopyData                       // COPY … FROM stdin data, through the \. line
)

// sqlToken is one lexeme plus the whitespace facts the layout pass needs
// about what preceded it in the SOURCE.
type sqlToken struct {
	kind sqlTokKind
	text string
	off  int // byte offset of the token in the source, for error positions
	// nlBefore counts newlines in the whitespace before the token, so the
	// layout can keep a blank line between statements and keep an
	// own-line comment on its own line.
	nlBefore int
	// spaceBefore reports ANY whitespace before the token. Used where the
	// author's adjacency is the only reliable signal — `varchar(10)` vs
	// `INSERT INTO t (a)`, `a[lo:hi]` vs `:var`.
	spaceBefore bool
}

// sqlLexError is a lexing failure at a byte offset. The formatter turns
// the offset into a line and column for the flash message.
type sqlLexError struct {
	off int
	msg string
}

// Error satisfies the error interface; the position is added by the
// caller, which has the source to count lines in.
func (e *sqlLexError) Error() string { return e.msg }

// sqlOpChars are the characters PostgreSQL allows in an operator name
// (scan.l `op_chars`). The backtick is one of them in the server's
// grammar, but it is refused before operator scanning — see the file
// comment.
const sqlOpChars = "+-*/<>=~!@#%^&|`?"

// sqlOpSpecial are the characters whose presence lets a multi-character
// operator END in + or - (scan.l: "SQL allows only + and - as the last
// char of an op if it also contains one of these"). Without one, a
// trailing +/- belongs to the next token: `a<-1` is `<` and `-1`.
const sqlOpSpecial = "~!@#%^&|`?"

// sqlLexer holds the scan position plus the little statement state that
// COPY detection needs.
type sqlLexer struct {
	src  string
	pos  int
	toks []sqlToken

	// stmtWords is how many words the current statement has produced;
	// only the first matters (COPY), and the FROM-STDIN pair is tracked
	// with prevWord.
	stmtWords int
	stmtCopy  bool
	prevWord  string
	fromStdin bool
	// copyPending is set by `COPY … FROM stdin;` (or \copy … from stdin)
	// and makes the lexer read the next line onwards as raw data.
	copyPending bool
}

// lexSQL tokenizes src. The error, when non-nil, is a *sqlLexError, and
// the tokens returned with it are everything lexed before the failure —
// enough for style detection on a sample cut mid-string, never enough to
// format from.
func lexSQL(src string) ([]sqlToken, error) {
	lx := &sqlLexer{src: src}
	for {
		nl, space := lx.skipSpace()
		if lx.pos >= len(lx.src) {
			return lx.toks, nil
		}
		if lx.copyPending && nl > 0 {
			// The COPY command's line is over: everything from here to the
			// terminator is data. Checked AFTER whitespace so a trailing
			// comment on the COPY line is still lexed as a comment.
			lx.lexCopyData(nl)
			continue
		}
		start := lx.pos
		kind, err := lx.next()
		if err != nil {
			return lx.toks, err
		}
		lx.toks = append(lx.toks, sqlToken{
			kind:        kind,
			text:        lx.src[start:lx.pos],
			off:         start,
			nlBefore:    nl,
			spaceBefore: space,
		})
		lx.track(kind, lx.src[start:lx.pos])
	}
}

// skipSpace consumes whitespace, reporting how many newlines it crossed
// and whether there was any whitespace at all.
func (lx *sqlLexer) skipSpace() (nl int, any bool) {
	for lx.pos < len(lx.src) {
		switch lx.src[lx.pos] {
		case '\n':
			nl++
		case ' ', '\t', '\r', '\f', '\v':
		default:
			return nl, any
		}
		any = true
		lx.pos++
	}
	return nl, any
}

// track updates the COPY-detection state after each token. Only the
// statement's first word, the FROM STDIN pair and the terminating `;`
// matter; everything else just resets prevWord.
func (lx *sqlLexer) track(kind sqlTokKind, text string) {
	switch kind {
	case sqlWord:
		up := strings.ToUpper(text)
		if lx.stmtWords == 0 && up == "COPY" {
			lx.stmtCopy = true
		}
		if lx.prevWord == "FROM" && up == "STDIN" {
			lx.fromStdin = true
		}
		lx.prevWord = up
		lx.stmtWords++
	case sqlPunct:
		if text == ";" {
			// COPY … TO stdout has no data block in the INPUT; only the
			// FROM stdin form is followed by rows.
			lx.copyPending = lx.stmtCopy && lx.fromStdin
			lx.stmtWords, lx.stmtCopy, lx.fromStdin, lx.prevWord = 0, false, false, ""
			return
		}
		lx.prevWord = ""
	case sqlMeta:
		// psql's \copy reads its rows the same way, and a meta-command
		// also ends whatever statement was being accumulated.
		low := strings.ToLower(text)
		lx.copyPending = strings.HasPrefix(low, `\copy`) && strings.Contains(low, "from stdin")
		lx.stmtWords, lx.stmtCopy, lx.fromStdin, lx.prevWord = 0, false, false, ""
	case sqlLineComment, sqlBlockComment:
		// Comments are invisible to the statement.
	default:
		lx.prevWord = ""
	}
}

// lexCopyData consumes raw COPY rows through the `\.` terminator line
// (or to end of input — psql treats EOF as the end of data too). The
// token's text excludes the newline after `\.`, so the writer can end
// the line itself like any other token.
func (lx *sqlLexer) lexCopyData(nl int) {
	lx.copyPending = false
	// skipSpace has already eaten the newline AND whatever whitespace
	// followed it — which may be a data row's leading tab, or whole empty
	// rows. Data is byte-exact, so restart from the first newline after
	// the last real token: the row after it is the first data row.
	prevEnd := 0
	if n := len(lx.toks); n > 0 {
		prevEnd = lx.toks[n-1].off + len(lx.toks[n-1].text)
	}
	start := prevEnd + strings.IndexByte(lx.src[prevEnd:], '\n') + 1
	nl = 1
	end := len(lx.src)
	for p := start; p < len(lx.src); {
		eol := strings.IndexByte(lx.src[p:], '\n')
		line := lx.src[p:]
		if eol >= 0 {
			line = lx.src[p : p+eol]
		}
		if strings.TrimRight(line, "\r") == `\.` {
			end = p + len(line)
			break
		}
		if eol < 0 {
			break
		}
		p += eol + 1
	}
	lx.toks = append(lx.toks, sqlToken{kind: sqlCopyData, text: lx.src[start:end], off: start, nlBefore: nl, spaceBefore: true})
	lx.pos = end
}

// next scans one token starting at lx.pos, which is not whitespace.
func (lx *sqlLexer) next() (sqlTokKind, error) {
	s, p := lx.src, lx.pos
	c := s[p]
	switch {
	case c == '-' && p+1 < len(s) && s[p+1] == '-':
		end := strings.IndexByte(s[p:], '\n')
		if end < 0 {
			lx.pos = len(s)
		} else {
			lx.pos = p + end
		}
		// A CR before the newline is line-ending, not comment text.
		lx.pos = p + len(strings.TrimRight(s[p:lx.pos], "\r"))
		return sqlLineComment, nil
	case c == '/' && p+1 < len(s) && s[p+1] == '*':
		return sqlBlockComment, lx.blockComment()
	case c == '\'':
		return sqlString, lx.quoted('\'', false)
	case c == '"':
		return sqlQuotedIdent, lx.quoted('"', false)
	case c == '$':
		return lx.dollar()
	case c == '\\':
		end := strings.IndexByte(s[p:], '\n')
		if end < 0 {
			lx.pos = len(s)
		} else {
			lx.pos = p + end
		}
		lx.pos = p + len(strings.TrimRight(s[p:lx.pos], "\r"))
		return sqlMeta, nil
	case c == '`':
		return 0, &sqlLexError{p, "backtick quoting is MySQL, not PostgreSQL"}
	case c == '{' || c == '}':
		return 0, &sqlLexError{p, fmt.Sprintf("'%c' is template syntax, not PostgreSQL", c)}
	case isSQLDigit(c) || (c == '.' && p+1 < len(s) && isSQLDigit(s[p+1])):
		lx.number()
		return sqlNumber, nil
	case isSQLIdentStart(c):
		return lx.word()
	case c == ':':
		return lx.colon()
	case strings.IndexByte("()[],;.", c) >= 0:
		lx.pos++
		return sqlPunct, nil
	case strings.IndexByte(sqlOpChars, c) >= 0:
		lx.operator()
		return sqlOp, nil
	default:
		return 0, &sqlLexError{p, fmt.Sprintf("unexpected character %q", c)}
	}
}

// blockComment consumes a /* … */ comment. PostgreSQL comments NEST, so
// `/* a /* b */ c */` is one comment — counting depth is what keeps the
// `c */` from being lexed as code.
func (lx *sqlLexer) blockComment() error {
	start := lx.pos
	depth := 0
	for p := lx.pos; p+1 < len(lx.src); {
		switch {
		case lx.src[p] == '/' && lx.src[p+1] == '*':
			depth++
			p += 2
		case lx.src[p] == '*' && lx.src[p+1] == '/':
			depth--
			p += 2
			if depth == 0 {
				lx.pos = p
				return nil
			}
		default:
			p++
		}
	}
	return &sqlLexError{start, "unterminated /* comment"}
}

// quoted consumes a quote-delimited token starting at lx.pos (on the
// opening quote). A doubled quote is an escaped quote in both strings
// and identifiers; backslash escapes apply only to E” strings, whose
// prefix the caller has already consumed.
func (lx *sqlLexer) quoted(q byte, backslash bool) error {
	start := lx.pos
	for p := lx.pos + 1; p < len(lx.src); p++ {
		switch lx.src[p] {
		case '\\':
			if backslash {
				p++
			}
		case q:
			if p+1 < len(lx.src) && lx.src[p+1] == q {
				p++
				continue
			}
			lx.pos = p + 1
			return nil
		}
	}
	if q == '"' {
		return &sqlLexError{start, `unterminated quoted identifier`}
	}
	return &sqlLexError{start, "unterminated string"}
}

// dollar handles everything that starts with `$`: a positional parameter
// ($1) or a dollar-quoted string ($$…$$, $tag$…$tag$). The tag follows
// identifier rules minus `$` and may not start with a digit, which is
// exactly what tells `$1` and `$a$` apart.
func (lx *sqlLexer) dollar() (sqlTokKind, error) {
	s, p := lx.src, lx.pos
	if p+1 < len(s) && isSQLDigit(s[p+1]) {
		q := p + 1
		for q < len(s) && isSQLDigit(s[q]) {
			q++
		}
		lx.pos = q
		return sqlParam, nil
	}
	q := p + 1
	if q < len(s) && isSQLIdentStart(s[q]) {
		q++
		for q < len(s) && isSQLIdentPart(s[q]) && s[q] != '$' {
			q++
		}
	}
	if q >= len(s) || s[q] != '$' {
		return 0, &sqlLexError{p, "stray '$'"}
	}
	delim := s[p : q+1]
	end := strings.Index(s[q+1:], delim)
	if end < 0 {
		return 0, &sqlLexError{p, "unterminated " + delim + " string"}
	}
	lx.pos = q + 1 + end + len(delim)
	return sqlDollar, nil
}

// number consumes a numeric literal. Beyond the obvious, it accepts the
// PostgreSQL 16 forms (0x/0o/0b prefixes, `_` digit separators) and then
// swallows any identifier characters glued on: the server rejects
// `123abc` as "trailing junk", and keeping it one token here means the
// formatter can never insert a space that would turn that error into
// `123 AS abc`-style valid-but-different SQL.
func (lx *sqlLexer) number() {
	s, p := lx.src, lx.pos
	if s[p] == '0' && p+1 < len(s) && strings.IndexByte("xXoObB", s[p+1]) >= 0 {
		p += 2
	} else {
		for p < len(s) && (isSQLDigit(s[p]) || s[p] == '_') {
			p++
		}
		// A fraction, but not the `..` range operator PL/pgSQL uses.
		if p < len(s) && s[p] == '.' && !(p+1 < len(s) && s[p+1] == '.') {
			p++
			for p < len(s) && (isSQLDigit(s[p]) || s[p] == '_') {
				p++
			}
		}
		if p < len(s) && (s[p] == 'e' || s[p] == 'E') {
			q := p + 1
			if q < len(s) && (s[q] == '+' || s[q] == '-') {
				q++
			}
			if q < len(s) && isSQLDigit(s[q]) {
				p = q
				for p < len(s) && isSQLDigit(s[p]) {
					p++
				}
			}
		}
	}
	for p < len(s) && isSQLIdentPart(s[p]) {
		p++
	}
	lx.pos = p
}

// word consumes an identifier or keyword — or, when a one-letter prefix
// is glued to a quote, the prefixed string it introduces (E'…', B'…',
// X'…', N'…', U&'…', U&"…").
func (lx *sqlLexer) word() (sqlTokKind, error) {
	s, p := lx.src, lx.pos
	if p+1 < len(s) && s[p+1] == '\'' && strings.IndexByte("eEbBxXnN", s[p]) >= 0 {
		lx.pos = p + 1
		return sqlString, lx.quoted('\'', s[p] == 'e' || s[p] == 'E')
	}
	if p+2 < len(s) && (s[p] == 'u' || s[p] == 'U') && s[p+1] == '&' && (s[p+2] == '\'' || s[p+2] == '"') {
		lx.pos = p + 2
		if s[p+2] == '"' {
			return sqlQuotedIdent, lx.quoted('"', false)
		}
		return sqlString, lx.quoted('\'', false)
	}
	q := p + 1
	for q < len(s) && isSQLIdentPart(s[q]) {
		q++
	}
	lx.pos = q
	return sqlWord, nil
}

// colon separates the three things a colon can start: the `::` cast, the
// `:=` assignment, and a psql variable (`:name`, `:'name'`, `:"name"`).
// Anything else is a bare `:` (array slices, `a[1:2]`).
func (lx *sqlLexer) colon() (sqlTokKind, error) {
	s, p := lx.src, lx.pos
	if p+1 < len(s) {
		switch n := s[p+1]; {
		case n == ':' || n == '=':
			lx.pos = p + 2
			return sqlPunct, nil
		case n == '\'' || n == '"':
			lx.pos = p + 1
			if err := lx.quoted(n, false); err != nil {
				return 0, err
			}
			return sqlPsqlVar, nil
		case isSQLIdentStart(n):
			q := p + 2
			for q < len(s) && isSQLIdentPart(s[q]) && s[q] != '$' {
				q++
			}
			lx.pos = q
			return sqlPsqlVar, nil
		}
	}
	lx.pos = p + 1
	return sqlPunct, nil
}

// operator consumes an operator run under scan.l's two rules: a run
// stops before `--` or `/*` (those always open comments), and a
// multi-character run may not END in + or - unless it contains one of
// sqlOpSpecial — so `=-` is `=` followed by unary minus.
func (lx *sqlLexer) operator() {
	s, p := lx.src, lx.pos
	q := p
	for q < len(s) && strings.IndexByte(sqlOpChars, s[q]) >= 0 {
		if q > p && ((s[q] == '-' && s[q-1] == '-') || (s[q] == '*' && s[q-1] == '/')) {
			// The previous char opened a comment: back off so it is not
			// part of this operator. (q > p keeps a lone `-` or `/` here
			// from looping: `--`/`/*` at p itself is caught by next().)
			q--
			break
		}
		q++
	}
	op := s[p:q]
	if len(op) > 1 && !strings.ContainsAny(op, sqlOpSpecial) {
		for len(op) > 1 && (op[len(op)-1] == '+' || op[len(op)-1] == '-') {
			op = op[:len(op)-1]
		}
	}
	lx.pos = p + len(op)
}

// isSQLDigit reports an ASCII digit.
func isSQLDigit(c byte) bool { return c >= '0' && c <= '9' }

// isSQLIdentStart reports a byte that may begin an unquoted identifier.
// Any byte >= 0x80 qualifies, as in scan.l: identifiers may contain any
// non-ASCII character, so a UTF-8 sequence is consumed byte by byte
// without ever being split.
func isSQLIdentStart(c byte) bool {
	return c == '_' || (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || c >= 0x80
}

// isSQLIdentPart reports a byte that may continue an unquoted
// identifier: a start byte, a digit, or `$`.
func isSQLIdentPart(c byte) bool {
	return isSQLIdentStart(c) || isSQLDigit(c) || c == '$'
}
