// =============================================================================
// File: internal/plugins/diag.go
// Author: Rohan Allison <rohanthewiz@gmail.com>
// Created: 2026-08-02
// Copyright: 2026 Rohan Allison. All rights reserved.
// =============================================================================

// diag.go parses a decoration provider's stdout into positions the
// editor can paint. The format is the compiler/grep convention every
// command-line tool already speaks:
//
//	path:line:col: severity: message
//	path:line: message
//	line:col: message
//	line: message
//
// That choice is the whole reason decorations are worth having here.
// It means a useful provider is a one-liner the user already knows how
// to write — `grep -n TODO "$FILE"`, `go vet "$FILE"`, `shellcheck
// -f gcc`, `eslint -f unix`, `golangci-lint run --out-format=line-number`
// — instead of a JSON envelope nobody's linter emits and everybody has
// to write a wrapper script for. A format ced invented would have made
// this feature theoretical.
//
// Unparseable lines are DROPPED, not reported. Real tools interleave
// summary lines ("3 problems found"), progress, and blank separators
// with their findings; treating those as errors would mean every
// provider needs a `| grep` to stay quiet, and a provider that prints
// nothing parseable is already saying so by producing no marks.

package plugins

import (
	"strconv"
	"strings"
)

// Severity ranks a diagnostic for coloring and for which one wins the
// single gutter cell when several land on one line.
type Severity int

const (
	// SevInfo is the default for a line that named no severity — a
	// TODO scanner's hits, a grep. Ambient, quietly colored.
	SevInfo Severity = iota
	SevWarn
	SevError
)

// String renders the severity for status messages and tests.
func (s Severity) String() string {
	switch s {
	case SevError:
		return "error"
	case SevWarn:
		return "warning"
	default:
		return "info"
	}
}

// Diagnostic is one parsed finding. Line and Col are ZERO-based (the
// editor's convention) even though the wire format is one-based — the
// conversion happens here, once, so no consumer has to remember it.
// Col is -1 when the line carried no column, which the painter reads as
// "mark the whole line" rather than "mark the first character".
type Diagnostic struct {
	// Path is whatever the tool printed, or "" when it printed none.
	// Left exactly as written: resolving it against the project root
	// needs a root, which this package deliberately doesn't have.
	Path     string
	Line     int
	Col      int
	Severity Severity
	Message  string
}

// severityWords maps the leading token tools use to label a finding.
// "note" and "hint" fold into info: they're the same volume, and a
// plugin author shouldn't have to know which synonym ced happened to
// implement.
var severityWords = map[string]Severity{
	"error":   SevError,
	"err":     SevError,
	"fatal":   SevError,
	"warning": SevWarn,
	"warn":    SevWarn,
	"info":    SevInfo,
	"note":    SevInfo,
	"hint":    SevInfo,
}

// ParseDiagnostics parses every line of a provider's output, dropping
// the ones that don't carry a location. Returns nil rather than an
// empty slice when nothing parsed, so callers can treat "no output" and
// "nothing recognisable" identically — both mean "this provider has
// nothing to say about this file right now".
func ParseDiagnostics(out string) []Diagnostic {
	var diags []Diagnostic
	for _, raw := range strings.Split(out, "\n") {
		if d, ok := ParseDiagnostic(raw); ok {
			diags = append(diags, d)
		}
	}
	return diags
}

// ParseDiagnostic parses ONE output line, for callers holding lines
// already — the terminal panel's scrollback asks row by row, since it
// never has the whole of a command's output as one string and only ever
// needs to answer "is this row a place I could jump to?". Same parser
// either way: two implementations of this format would drift, and the
// user would have no way to tell which one decided.
func ParseDiagnostic(raw string) (Diagnostic, bool) {
	return parseDiagLine(raw)
}

// parseDiagLine parses one output line. Reports false for anything
// without a recognisable leading location.
//
// The parse is deliberately incremental rather than a split-on-colon,
// because the MESSAGE routinely contains colons ("expected ':'", a Go
// `:=`, a URL) and a fixed field count would eat them. Each field is
// taken only if it looks like what that position wants: the first is a
// path unless it parses as an integer, and the column is claimed only
// when the next field is an integer. So `12:\tfoo := bar` (grep -n over
// Go source) keeps its `:=` — "\tfoo " is not an integer, so the column
// slot goes unclaimed and the whole remainder is the message.
func parseDiagLine(raw string) (Diagnostic, bool) {
	s := strings.TrimRight(raw, "\r")
	if strings.TrimSpace(s) == "" {
		return Diagnostic{}, false
	}
	d := Diagnostic{Col: -1}

	field, rest, ok := nextField(s)
	if !ok {
		return Diagnostic{}, false
	}
	if n, err := strconv.Atoi(strings.TrimSpace(field)); err == nil {
		d.Line = n
	} else {
		// First field wasn't a number, so it's a path and the SECOND
		// field has to be the line — a location with neither is not a
		// diagnostic, it's prose.
		d.Path = strings.TrimSpace(field)
		var lineField string
		lineField, rest, ok = nextField(rest)
		if !ok {
			return Diagnostic{}, false
		}
		n, err := strconv.Atoi(strings.TrimSpace(lineField))
		if err != nil {
			return Diagnostic{}, false
		}
		d.Line = n
	}

	// Optional column: claimed only if the next field is an integer.
	if field, after, found := nextField(rest); found {
		if n, err := strconv.Atoi(strings.TrimSpace(field)); err == nil {
			d.Col = n - 1
			if d.Col < 0 {
				d.Col = 0
			}
			rest = after
		}
	}

	// Optional severity word, same claim-only-if-it-matches rule.
	msg := strings.TrimSpace(rest)
	if field, after, found := nextField(msg); found {
		if sev, known := severityWords[strings.ToLower(strings.TrimSpace(field))]; known {
			d.Severity = sev
			msg = strings.TrimSpace(after)
		}
	}
	d.Message = msg

	// One-based on the wire, zero-based in the editor. Tools that print
	// a 0 (or a negative, which some emit for "whole file") clamp to the
	// first line rather than landing outside the buffer.
	d.Line--
	if d.Line < 0 {
		d.Line = 0
	}
	return d, true
}

// nextField splits s at its first colon, returning the text before it,
// the text after it, and whether there was one at all.
func nextField(s string) (field, rest string, ok bool) {
	i := strings.IndexByte(s, ':')
	if i < 0 {
		return "", s, false
	}
	return s[:i], s[i+1:], true
}
