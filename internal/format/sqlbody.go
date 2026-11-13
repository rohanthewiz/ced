// =============================================================================
// File: internal/format/sqlbody.go
// Author: Rohan Allison <rohanthewiz@gmail.com>
// Created: 2026-11-13
// Copyright: 2026 Rohan Allison. All rights reserved.
// =============================================================================

// sqlbody.go lays out routine bodies — the `$$ … $$` of a CREATE
// FUNCTION / PROCEDURE or a DO block — for the in-process SQL formatter
// (sqlfmt.go). To the SQL grammar such a body is one string token, so the
// file-level pass copies it whole; this file is what looks inside.
//
// # Which dollar strings are bodies
//
// Only one whose language is known and is SQL-shaped (routineBodyLang):
//
//	CREATE [OR REPLACE] FUNCTION|PROCEDURE … AS $$ … $$ … LANGUAGE sql|plpgsql
//	DO [LANGUAGE plpgsql] $$ … $$ [LANGUAGE …]      (no LANGUAGE = plpgsql)
//
// The LANGUAGE clause may sit before or after the body, so the whole
// statement is scanned for it (outside parentheses: a parameter can be
// called `language`). Everything else — `LANGUAGE c`, plperl, plv8, a
// `$q$` passed to EXECUTE or format() — is data and is never touched.
//
// # Same contract, one level down
//
// A body is lexed with the same lexer (PL/pgSQL reuses the server's core
// scanner, so the token boundaries are the same), laid out, re-lexed and
// compared token-for-token. sameSQLTokens recurses into bodies, so the
// file-level safety net checks them too. A body that will not lex, holds
// a psql meta-command, or whose PL/pgSQL blocks do not pair up is kept
// VERBATIM — a body is a best effort, never a reason to refuse the file.
//
// # The output shape
//
// The body starts on the line after its opening delimiter and the closing
// delimiter gets its own line, both at the STATEMENT's depth (column 0
// for a top-level CREATE), which is how pg_dump and pg_format write it:
//
//	CREATE FUNCTION f(a int)
//	    RETURNS int
//	    LANGUAGE plpgsql
//	    AS $$
//	DECLARE
//	    n int := 0;
//	BEGIN
//	    IF a > 0 THEN
//	        n := a;
//	    END IF;
//	    RETURN n;
//	END;
//	$$;
//
// # PL/pgSQL: a block stack over the SQL layout
//
// A PL/pgSQL body is a sequence of statements ended by `;`, nested in
// blocks. The statements themselves are SQL (or SQL expressions), so they
// go through the ordinary layout — a SELECT … INTO gets clause layout, an
// assignment keeps its line. What PL/pgSQL adds is only the nesting,
// recognised by the word a statement STARTS with (mid-statement, `END`,
// `LOOP` or `EXCEPTION` mean what they mean in SQL):
//
//	word at start      line at         then
//	-------------      -------         ----
//	DECLARE            depth d         open declare   (body d+1)
//	BEGIN              d (DECLARE's)   open begin     (body d+1)
//	EXCEPTION          begin's d       begin → exception: WHEN d+1, body d+2
//	IF … THEN          d               open if        (body d+1)
//	ELSIF … THEN       if's d          —
//	ELSE               if's d / case's d+1
//	CASE [x]           d               open case: WHEN d+1, body d+2
//	WHEN … THEN        case's / exception's d+1
//	[FOR|WHILE …] LOOP d               open loop      (body d+1)
//	END …              the block's d   close it
//	<<label>>          d               own line, glued
//
// A header (IF … THEN, FOR … LOOP) is one statement whose end is the
// word THEN or LOOP rather than `;`; that word stays on the header's last
// line unless the author broke before it (or the header held a laid-out
// query), in which case it lines up with the header's first word.
package format

import (
	"strings"
)

// plKind names an open PL/pgSQL block.
type plKind int

const (
	plNone      plKind = iota // no block (a header that opens nothing)
	plDeclare                 // DECLARE … — becomes plBegin at its BEGIN
	plBegin                   // BEGIN … END
	plException               // BEGIN … EXCEPTION … END, after EXCEPTION
	plIf                      // IF … END IF
	plCase                    // CASE … END CASE (the statement, not the expression)
	plLoop                    // [FOR|WHILE|FOREACH …] LOOP … END LOOP
)

// plBlock is one open block: its kind and the depth of its opening line.
type plBlock struct {
	kind  plKind
	depth int
}

// plKeywords are the PL/pgSQL words re-cased inside a PL/pgSQL body, on
// top of sqlKeywords. None is a likely variable name, and re-casing an
// unquoted name is harmless anyway (PL/pgSQL folds names like SQL).
var plKeywords = setOf("ELSIF", "ELSEIF", "LOOP", "WHILE", "FOREACH", "REVERSE",
	"EXIT", "CONTINUE", "RETURN", "RAISE", "PERFORM", "EXCEPTION", "ASSERT",
	"CONSTANT", "ALIAS", "DIAGNOSTICS", "STACKED")

// plRaiseLevels are RAISE's level words — keywords only right after RAISE
// (`notice` and `log` are fine variable names elsewhere).
var plRaiseLevels = setOf("DEBUG", "LOG", "INFO", "NOTICE", "WARNING")

// plIsKeyword reports whether toks[i] is a PL/pgSQL word the formatter
// re-cases inside a PL/pgSQL body (see plKeywords; QUERY, the RAISE
// levels, GET and %TYPE / %ROWTYPE need their neighbour to prove it).
func plIsKeyword(toks []sqlToken, i int) bool {
	t := toks[i]
	if t.kind != sqlWord {
		return false
	}
	up := strings.ToUpper(t.text)
	p := sqlPrevSig(toks, i)
	if p >= 0 && toks[p].kind == sqlPunct && (toks[p].text == "." || toks[p].text == "::") {
		return false // a qualified name's part: `rec.loop` is a field
	}
	if plKeywords[up] {
		return true
	}
	prevUp := ""
	if p >= 0 && toks[p].kind == sqlWord {
		prevUp = strings.ToUpper(toks[p].text)
	}
	switch {
	case up == "QUERY":
		return prevUp == "RETURN"
	case plRaiseLevels[up]:
		return prevUp == "RAISE"
	case up == "GET":
		n := sqlNextSig(toks, i)
		return n >= 0 && toks[n].kind == sqlWord &&
			(strings.EqualFold(toks[n].text, "DIAGNOSTICS") || strings.EqualFold(toks[n].text, "STACKED"))
	case up == "TYPE" || up == "ROWTYPE":
		return p >= 0 && toks[p].kind == sqlOp && toks[p].text == "%"
	}
	return false
}

// sqlIsTypeSuffix reports the TYPE / ROWTYPE of a `%TYPE` spelling.
func sqlIsTypeSuffix(t sqlToken) bool {
	return t.kind == sqlWord && (strings.EqualFold(t.text, "TYPE") || strings.EqualFold(t.text, "ROWTYPE"))
}

// plIsLabel reports whether the word at toks[i] names a block label —
// `<<outer>>`, `EXIT outer WHEN …`, `CONTINUE outer;`, `END LOOP outer;`,
// `END blk;`. A label is an identifier, so it keeps its spelling even
// when it spells a keyword (`outer` is a join word).
func plIsLabel(toks []sqlToken, i int) bool {
	t := toks[i]
	if t.kind != sqlWord {
		return false
	}
	switch strings.ToUpper(t.text) {
	case "WHEN", "IF", "LOOP", "CASE":
		return false
	}
	p := sqlPrevSig(toks, i)
	if p < 0 {
		return false
	}
	if toks[p].kind == sqlOp && toks[p].text == "<<" {
		return true
	}
	if toks[p].kind != sqlWord {
		return false
	}
	// Only where the label is the statement's last word (or EXIT's /
	// CONTINUE's WHEN follows), so `CASE … END AS x` keeps AS a keyword.
	n := sqlNextSig(toks, i)
	ends := n < 0 || (toks[n].kind == sqlPunct && toks[n].text == ";") ||
		(toks[n].kind == sqlWord && strings.EqualFold(toks[n].text, "WHEN"))
	if !ends {
		return false
	}
	switch strings.ToUpper(toks[p].text) {
	case "EXIT", "CONTINUE", "END":
		return true
	case "LOOP":
		pp := sqlPrevSig(toks, p)
		return pp >= 0 && toks[pp].kind == sqlWord && strings.EqualFold(toks[pp].text, "END")
	}
	return false
}

// isKeyword reports whether toks[i] is re-cased by this layout: the SQL
// keywords everywhere, the PL/pgSQL ones only inside a PL/pgSQL body,
// where a block label is never one.
func (l *sqlLayout) isKeyword(i int) bool {
	if l.pl && plIsLabel(l.toks, i) {
		return false
	}
	return sqlIsKeyword(l.toks, i) || (l.pl && plIsKeyword(l.toks, i))
}

// spell returns token i as it is written out: keywords in the file's
// case, a routine body laid out, everything else as in the source.
func (l *sqlLayout) spell(i int) string {
	t := l.toks[i]
	if t.kind == sqlDollar {
		return l.dollarText(i)
	}
	if l.isKeyword(i) {
		if l.upper {
			return strings.ToUpper(t.text)
		}
		return strings.ToLower(t.text)
	}
	return t.text
}

// sqlStmtStop reports a token that ends a statement for the purpose of
// finding a routine body's statement.
func sqlStmtStop(t sqlToken) bool {
	return t.kind == sqlMeta || t.kind == sqlCopyData || (t.kind == sqlPunct && t.text == ";")
}

// routineBodyLang returns "sql" or "plpgsql" when the dollar-quoted token
// at toks[i] is the body of a routine in that language (see the file
// comment), and "" for every other dollar string. It is a pure function
// of the token stream, so the layout and the safety check agree on it.
func routineBodyLang(toks []sqlToken, i int) string {
	if toks[i].kind != sqlDollar {
		return ""
	}
	s := i
	for s > 0 && !sqlStmtStop(toks[s-1]) {
		s--
	}
	f := s
	for f < i && (toks[f].kind == sqlLineComment || toks[f].kind == sqlBlockComment) {
		f++
	}
	if f >= i || toks[f].kind != sqlWord {
		return ""
	}
	first := strings.ToUpper(toks[f].text)
	p := sqlPrevSig(toks, i)
	switch first {
	case "CREATE":
		// CREATE [OR REPLACE] FUNCTION|PROCEDURE, and the dollar is AS's.
		j := sqlNextSig(toks, f)
		for j >= 0 && j < i && toks[j].kind == sqlWord &&
			(strings.EqualFold(toks[j].text, "OR") || strings.EqualFold(toks[j].text, "REPLACE")) {
			j = sqlNextSig(toks, j)
		}
		if j < 0 || j >= i || toks[j].kind != sqlWord ||
			!(strings.EqualFold(toks[j].text, "FUNCTION") || strings.EqualFold(toks[j].text, "PROCEDURE")) {
			return ""
		}
		if p < 0 || toks[p].kind != sqlWord || !strings.EqualFold(toks[p].text, "AS") {
			return ""
		}
	case "DO":
		// DO's grammar is DO [LANGUAGE name] code [LANGUAGE name]: the
		// dollar follows DO itself or DO LANGUAGE name.
		if p != f {
			pp := sqlPrevSig(toks, p)
			if pp < 0 || toks[pp].kind != sqlWord || !strings.EqualFold(toks[pp].text, "LANGUAGE") || sqlPrevSig(toks, pp) != f {
				return ""
			}
		}
	default:
		return ""
	}

	// The LANGUAGE clause, anywhere in the statement but outside
	// parentheses; and the body itself must be outside them too.
	lang := ""
	depth := 0
	for k := s; k < len(toks) && !sqlStmtStop(toks[k]); k++ {
		t := toks[k]
		switch {
		case t.kind == sqlPunct && t.text == "(":
			depth++
		case t.kind == sqlPunct && t.text == ")":
			depth--
		case k == i && depth != 0:
			return ""
		case depth == 0 && t.kind == sqlWord && strings.EqualFold(t.text, "LANGUAGE"):
			if n := sqlNextSig(toks, k); n >= 0 {
				switch toks[n].kind {
				case sqlWord:
					lang = strings.ToLower(toks[n].text)
				case sqlString:
					lang = strings.ToLower(strings.Trim(toks[n].text, "'"))
				}
			}
		}
	}
	if lang == "" && first == "DO" {
		lang = "plpgsql" // DO's documented default
	}
	if lang == "sql" || lang == "plpgsql" {
		return lang
	}
	return ""
}

// splitDollar splits a dollar-quoted token into its delimiter ($$ or
// $tag$) and the text between the two delimiters.
func splitDollar(text string) (tag, body string, ok bool) {
	if len(text) < 2 || text[0] != '$' {
		return "", "", false
	}
	j := strings.IndexByte(text[1:], '$')
	if j < 0 {
		return "", "", false
	}
	tag = text[:j+2]
	if len(text) < 2*len(tag) || !strings.HasSuffix(text, tag) {
		return "", "", false
	}
	return tag, text[len(tag) : len(text)-len(tag)], true
}

// dollarText returns token i's text as written out: a routine body laid
// out at the depth of the statement holding it, anything else verbatim.
func (l *sqlLayout) dollarText(i int) string {
	t := l.toks[i]
	lang := routineBodyLang(l.toks, i)
	if lang == "" {
		return t.text
	}
	out, _ := formatRoutineBody(t.text, lang, l.w.unit, l.upper, l.stack[0].base)
	return out
}

// formatRoutineBody lays out a routine body token in lang at depth base,
// returning the new token text and true — or the token unchanged and
// false when the body is kept verbatim (see the file comment for why a
// body is never an error).
func formatRoutineBody(tok, lang, unit string, upper bool, base int) (string, bool) {
	tag, body, ok := splitDollar(tok)
	// A body with a CR in it comes from a MIXED line-ending file (a
	// consistent CRLF file was converted before lexing): laying it out
	// would rewrite its line endings, so it is left as it is.
	if !ok || strings.TrimSpace(body) == "" || strings.Contains(body, "\r") {
		return tok, false
	}
	toks, err := lexSQL(body)
	if err != nil {
		return tok, false
	}
	for _, t := range toks {
		// psql does not read inside a dollar string: a backslash line or
		// a COPY there is not what the lexer would take it for.
		if t.kind == sqlMeta || t.kind == sqlCopyData {
			return tok, false
		}
	}
	match, err := matchSQLParens(toks)
	if err != nil {
		return tok, false
	}
	l := newSQLLayout(toks, match, unit, upper, base, lang == "plpgsql")
	out := l.run()
	if l.plBad || len(l.blocks) > 0 || l.plHead != "" || l.plLabel != 0 {
		return tok, false
	}
	// The same net as the file's. The tag check is belt and braces: the
	// source body cannot contain its own delimiter, and re-spacing cannot
	// glue one together, but a body that did would end the string early.
	after, err := lexSQL(out)
	if err != nil || !sameSQLTokens(toks, after) || strings.Contains(out, tag) {
		return tok, false
	}
	return tag + "\n" + out + strings.Repeat(unit, base) + tag, true
}

// sameRoutineBody reports whether two spellings of a routine body are the
// same code: same delimiter, and bodies that are the same token stream
// (by sameSQLTokens, so nested bodies are compared the same way).
func sameRoutineBody(a, b string) bool {
	ta, ba, ok := splitDollar(a)
	if !ok {
		return false
	}
	tb, bb, ok := splitDollar(b)
	if !ok || ta != tb {
		return false
	}
	xa, err := lexSQL(ba)
	if err != nil {
		return false
	}
	xb, err := lexSQL(bb)
	if err != nil {
		return false
	}
	return sameSQLTokens(xa, xb)
}

// plDepth is the depth a statement starts at: the body's base with no
// block open, else the innermost block's body depth (arms of a CASE and
// handlers of an EXCEPTION sit one level in, their statements two).
func (l *sqlLayout) plDepth() int {
	if len(l.blocks) == 0 {
		return l.base
	}
	b := l.blocks[len(l.blocks)-1]
	if b.kind == plException || b.kind == plCase {
		return b.depth + 2
	}
	return b.depth + 1
}

// plTop returns the innermost open block, or nil.
func (l *sqlLayout) plTop() *plBlock {
	if len(l.blocks) == 0 {
		return nil
	}
	return &l.blocks[len(l.blocks)-1]
}

// plQueryStarts reports whether a query starter at this point of a
// PL/pgSQL statement begins an embedded query that gets clause layout:
// RETURN QUERY SELECT, FOR r IN SELECT … LOOP, OPEN c FOR SELECT and a
// DECLAREd `c CURSOR FOR SELECT`.
func (l *sqlLayout) plQueryStarts(fr *sqlFrame) bool {
	if !l.pl || fr.prevSig < 0 || l.toks[fr.prevSig].kind != sqlWord {
		return false
	}
	switch fr.prevWord {
	case "QUERY":
		return fr.firstWord == "RETURN"
	case "IN":
		return l.plHead == "LOOP"
	case "FOR":
		return fr.firstWord != "FOR"
	}
	return false
}

// plCode places token i when the PL/pgSQL block layout owns it — a block
// word at a statement's start, a header's closing THEN / LOOP, a label —
// and reports whether it did. Tokens inside parentheses or an expression
// CASE are never block words, so only the statement frame is considered.
func (l *sqlLayout) plCode(i int) bool {
	if len(l.stack) != 1 {
		return false
	}
	t := l.toks[i]
	fr := l.stack[0]
	up := ""
	if t.kind == sqlWord {
		up = strings.ToUpper(t.text)
	}

	// <<label>>: written glued, then the labelled LOOP / BEGIN / DECLARE
	// starts its own line.
	switch l.plLabel {
	case 1:
		l.plLabel = 0
		if t.kind != sqlWord && t.kind != sqlQuotedIdent {
			l.plBad = true
			return false
		}
		l.plPut(i)
		l.plLabel = 2
		return true
	case 2:
		l.plLabel = 0
		if t.kind != sqlOp || t.text != ">>" {
			l.plBad = true
			return false
		}
		l.plPut(i)
		l.endStmt(i)
		return true
	}

	if fr.first {
		return l.plStatementStart(i, t, up)
	}

	if l.plHead == "" {
		return false
	}
	if t.kind == sqlPunct && t.text == ";" {
		// IF without THEN, FOR without LOOP: not PL/pgSQL we can follow.
		l.plBad, l.plHead = true, ""
		return false
	}
	if up != l.plHead {
		return false
	}
	if up == "WHEN" {
		// A statement CASE's header (`CASE x`) ends where its first arm
		// begins; the WHEN is then placed as the arm's own start.
		l.plHead = ""
		l.endStmt(l.prev)
		l.code(i)
		return true
	}
	l.plHeaderEnd(i)
	return true
}

// plStatementStart places a block word that starts a statement (see the
// table in the file comment), reporting false for any other word so the
// SQL layout takes it.
func (l *sqlLayout) plStatementStart(i int, t sqlToken, up string) bool {
	top := l.plTop()
	if t.kind == sqlOp {
		switch t.text {
		case "<<":
			l.plWrite(i, l.plDepth())
			l.plLabel = 1
			return true
		case "#":
			// `#variable_conflict use_column` and friends: compiler
			// options read in the author's own lines. Rare enough that the
			// body is simply kept as written.
			l.plBad = true
		}
		return false
	}
	switch up {
	case "DECLARE":
		d := l.plDepth()
		l.plWrite(i, d)
		l.blocks = append(l.blocks, plBlock{plDeclare, d})
		l.endStmt(i)
	case "BEGIN":
		d := l.plDepth()
		if top != nil && top.kind == plDeclare {
			// The block's BEGIN lines up with its DECLARE.
			d = top.depth
			l.blocks = l.blocks[:len(l.blocks)-1]
		}
		l.plWrite(i, d)
		l.blocks = append(l.blocks, plBlock{plBegin, d})
		l.endStmt(i)
	case "EXCEPTION":
		if top == nil || top.kind != plBegin {
			return false
		}
		top.kind = plException
		l.plWrite(i, top.depth)
		l.endStmt(i)
	case "END":
		if top == nil || top.kind == plDeclare {
			l.plBad = true
			return false
		}
		b := *top
		l.blocks = l.blocks[:len(l.blocks)-1]
		// The rest — IF / LOOP / CASE / a label, then `;` — follows on
		// this line as an ordinary statement.
		l.plWrite(i, b.depth)
	case "IF":
		l.plHeader(i, l.plDepth(), "THEN", plIf)
	case "ELSIF", "ELSEIF":
		if top == nil || top.kind != plIf {
			return false
		}
		l.plHeader(i, top.depth, "THEN", plNone)
	case "ELSE":
		switch {
		case top != nil && top.kind == plIf:
			l.plWrite(i, top.depth)
		case top != nil && top.kind == plCase:
			l.plWrite(i, top.depth+1)
		default:
			return false
		}
		l.endStmt(i)
	case "CASE":
		d := l.plDepth()
		l.plHeader(i, d, "WHEN", plNone)
		l.blocks = append(l.blocks, plBlock{plCase, d})
	case "WHEN":
		if top == nil || (top.kind != plCase && top.kind != plException) {
			return false
		}
		l.plHeader(i, top.depth+1, "THEN", plNone)
	case "LOOP":
		d := l.plDepth()
		l.plWrite(i, d)
		l.blocks = append(l.blocks, plBlock{plLoop, d})
		l.endStmt(i)
	case "FOR", "FOREACH", "WHILE":
		l.plHeader(i, l.plDepth(), "LOOP", plLoop)
	default:
		return false
	}
	return true
}

// plHeader writes the first word of a block header at depth d and waits
// for the word that ends it, which opens block open (plNone: none).
func (l *sqlLayout) plHeader(i, d int, end string, open plKind) {
	l.plWrite(i, d)
	l.plHead, l.plHeadDepth, l.plHeadOpen = end, d, open
}

// plHeaderEnd writes the THEN / LOOP that ends a header, opens the
// header's block, and ends the header like a `;` ends a statement.
func (l *sqlLayout) plHeaderEnd(i int) {
	t := l.toks[i]
	w := l.w
	// On the header's last line, unless the author put it on a line of
	// its own (`IF a\n   AND b\nTHEN`) or the header held a laid-out
	// query — after a WHERE body a trailing LOOP would read as SQL.
	if t.nlBefore > 0 || l.stack[0].query || l.mustBreak {
		w.line(l.plHeadDepth)
	}
	l.mustBreak = false
	w.put(l.spell(i), !w.atStart() && l.needSpace(i))
	if l.plHeadOpen != plNone {
		l.blocks = append(l.blocks, plBlock{l.plHeadOpen, l.plHeadDepth})
	}
	l.plHead, l.plHeadOpen = "", plNone
	l.endStmt(i)
}

// plWrite writes block word i at the start of a line at depth d and
// makes d the statement frame's depth, so a header's continuation lines
// indent from where the header actually stands.
func (l *sqlLayout) plWrite(i, d int) {
	fr := l.stack[0]
	l.w.line(d)
	fr.base = d
	l.w.put(l.spell(i), false)
	l.prev, l.prevUnary, l.mustBreak = i, false, false
	up := strings.ToUpper(l.toks[i].text)
	if fr.first {
		fr.firstWord = up
	}
	fr.first, fr.prevSig, fr.prevWord = false, i, up
}

// plPut writes a label's name or closing `>>` glued to what precedes it.
func (l *sqlLayout) plPut(i int) {
	l.w.put(l.spell(i), false)
	l.prev, l.prevUnary = i, false
	fr := l.stack[0]
	fr.prevSig, fr.prevWord = i, strings.ToUpper(l.toks[i].text)
}
