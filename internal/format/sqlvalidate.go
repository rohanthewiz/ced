// =============================================================================
// File: internal/format/sqlvalidate.go
// Author: Rohan Allison <rohanthewiz@gmail.com>
// Created: 2026-11-03
// Copyright: 2026 Rohan Allison. All rights reserved.
// =============================================================================

// sqlvalidate.go is the PostgreSQL half of ced's in-process syntax
// check. It answers validateJSON's question — "does this parse, and if
// not, WHERE?" — for .pgsql / .psql files, and ONLY those: a .sql file
// may be MySQL or SQLite, whose perfectly good `'it\'s'` and `# note`
// lex as unterminated PostgreSQL strings, and an underline that appears
// unasked on a working file is a lie (see sqlDialectKnown, kinds.go).
//
// # Why the formatter's own front half, and nothing more
//
// The check is exactly the two steps formatSQL runs before it lays
// anything out:
//
//	lexSQL           strings, quoted identifiers, $tag$ bodies, nested
//	                 comments close; no backtick / brace / stray byte
//	matchSQLParens   every ( closes before its statement's `;`
//
// Sharing them is the point, not a convenience. The live underline and
// the Format verb's refusal then say the same thing at the same place —
// a file the validator passes is one the formatter will accept, and a
// file it marks is one Format would refuse with the identical message.
// Two scanners would eventually disagree, and the user would see a
// clean file that "fails to format", or a marked one that formats fine.
//
// It is deliberately NOT a parser. `SELEC 1` lexes; whether it is a
// statement is the server's call (and a language server's, if one is
// installed — its diagnostics merge alongside this one). What a lexer
// CAN know — a quote or comment that never closes swallows the rest of
// the file — is precisely the mistake that is hardest to spot by eye,
// because the damage shows up far below the cause.
//
// # Positions
//
// Both steps report a byte offset at the CAUSE, not where scanning gave
// up: an unterminated string is marked on its opening quote, an unclosed
// paren on the `(` itself. That is the CSV validator's rule (mark the
// opener of the field that ran away) and needs no moving here.

package format

import (
	"bytes"
	"errors"
)

// validateSQL lexes src as PostgreSQL (with psql's additions) and checks
// its parentheses, returning the first problem or nil.
//
// src is the editor BUFFER: always bare LF and BOM-free (fileio.go
// strips both on read and re-emits them on write), so unlike formatSQL
// there is no line-ending or BOM bookkeeping — and doing it anyway would
// shift the columns of a buffer that never had them.
func validateSQL(src []byte) []Problem {
	// Empty is mid-thought, not broken — the rule every validator here
	// shares, since this runs while the user types.
	if len(bytes.TrimSpace(src)) == 0 {
		return nil
	}

	toks, err := lexSQL(string(src))
	if err == nil {
		// Only a file that lexes whole is worth balancing: on a lex
		// error the token list stops at the failure, and an "unclosed
		// (" before a cut-off string would be a second, invented
		// problem layered over the real one.
		_, err = matchSQLParens(toks)
	}
	if err == nil {
		return nil
	}

	var le *sqlLexError
	if !errors.As(err, &le) {
		// Both steps only ever return *sqlLexError; a rejection reported
		// at the top of the file still beats one silently dropped (the
		// validateJSON rule).
		return []Problem{{Message: err.Error()}}
	}
	line, col := offsetToLineCol(src, le.off)
	return []Problem{{Line: line, Col: col, Message: le.msg}}
}
