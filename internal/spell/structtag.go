// =============================================================================
// File: internal/spell/structtag.go
// Author: Rohan Allison <rohanthewiz@gmail.com>
// Created: 2026-11-16
// Copyright: 2026 Rohan Allison. All rights reserved.
// =============================================================================

// structtag.go finds the NAMES inside Go struct tags on one line of
// source — the only text the struct-tag spell check reads.
//
//	Name string `json:"usrName,omitempty" db:"user_name"`
//	             ├──┘ ├─────┘             ├┘ ├───────┘
//	             key   name (checked)     key name (checked)
//
// # Why a line scanner and not go/scanner
//
// The check runs per frame over the visible lines (a DecorationSource),
// and a tag in gofmt'd code always sits on one line. go/scanner would be
// exact about strings and comments, but only from the top of the file,
// which turns a 40-line paint into a whole-file scan on every keystroke.
// The line scanner's one weakness — a multi-line raw string closing on
// this line shifts the backtick pairing — is covered by strictness: a span
// counts as a tag only if ALL of it parses as `key:"value"` pairs, which
// stray text between two misaligned backticks (" + ", a SQL tail) never
// does. Tag-shaped text in a comment IS read, and a typo there is still a
// typo.
//
// # What counts as the name
//
// Every key is read, not just json — yaml, xml, toml, db, bson, form,
// mapstructure and the rest all name a field in someone's wire format. The
// name is the value up to its first comma (the encoding/json convention
// the others follow), and it is checked only when it is IDENTIFIER-SHAPED:
// ASCII letters, digits and _ - . with at least one letter. That one rule
// is what keeps the check off values that are not names at all:
//
//	json:"-"                         no letter
//	json:",omitempty"                empty name
//	gorm:"column:id;type:varchar(9)" ':' ';' '(' — a mini-language
//	validate:"gte=0,lte=130"         '='
//	description:"The user's name"    spaces — prose, not a name
//	pattern:"^[a-z]+$"               punctuation
package spell

// TagName is one checkable name inside a struct tag: the tag key it
// belongs to ("json") and the rune columns [Start, End) of the name on the
// line it came from.
type TagName struct {
	Key        string
	Start, End int
}

// TagNames returns every checkable name in the struct tags on one line, in
// order. line is the line's runes; the columns returned index into it.
//
// Backticks are paired left to right; each pair whose content is a
// well-formed tag (see parseTag) contributes its identifier-shaped names.
// An unpaired last backtick — a raw string opening or closing on another
// line — contributes nothing.
func TagNames(line []rune) []TagName {
	var ticks []int
	for i, r := range line {
		if r == '`' {
			ticks = append(ticks, i)
		}
	}
	var out []TagName
	for k := 0; k+1 < len(ticks); k += 2 {
		open, close := ticks[k], ticks[k+1]
		pairs, ok := parseTag(line, open+1, close)
		if !ok {
			continue
		}
		for _, p := range pairs {
			if tn, ok := tagNameOf(line, p); ok {
				out = append(out, tn)
			}
		}
	}
	return out
}

// tagPair is one key:"value" of a tag: the key text and the rune columns
// of the value BETWEEN its quotes.
type tagPair struct {
	key              string
	valStart, valEnd int
}

// parseTag parses line[lo:hi] as a struct tag the way reflect.StructTag's
// Lookup does — space-separated key:"value" pairs, a key being anything
// above a space but ':' '"' and DEL — and reports ok only if the WHOLE
// span parses and holds at least one pair. Strictness is the point (see
// the file comment): it is the guard against misaligned backticks.
func parseTag(line []rune, lo, hi int) (pairs []tagPair, ok bool) {
	i := lo
	for {
		for i < hi && line[i] == ' ' {
			i++
		}
		if i == hi {
			return pairs, len(pairs) > 0
		}
		ks := i
		for i < hi && line[i] > ' ' && line[i] != ':' && line[i] != '"' && line[i] != 0x7f {
			i++
		}
		if i == ks || i+1 >= hi || line[i] != ':' || line[i+1] != '"' {
			return nil, false
		}
		key := string(line[ks:i])
		i += 2 // past `:"`
		vs := i
		for i < hi && line[i] != '"' {
			if line[i] == '\\' {
				i++ // an escaped character, possibly a quote
			}
			i++
		}
		if i >= hi {
			return nil, false // the value's closing quote is missing
		}
		pairs = append(pairs, tagPair{key: key, valStart: vs, valEnd: i})
		i++ // past the closing quote
	}
}

// tagNameOf extracts the checkable name from one pair, if it has one: the
// value up to its first comma, identifier-shaped. A value holding a
// backslash is skipped — its escapes would shift every column after them,
// and no real tag name needs one.
func tagNameOf(line []rune, p tagPair) (TagName, bool) {
	end := p.valStart
	for end < p.valEnd && line[end] != ',' {
		if line[end] == '\\' {
			return TagName{}, false
		}
		end++
	}
	name := line[p.valStart:end]
	if !identShaped(name) {
		return TagName{}, false
	}
	return TagName{Key: p.key, Start: p.valStart, End: end}, true
}

// identShaped reports whether name is ASCII letters, digits, '_', '-' and
// '.' only, with at least one letter.
func identShaped(name []rune) bool {
	letter := false
	for _, r := range name {
		switch {
		case isASCIILetter(r):
			letter = true
		case r >= '0' && r <= '9', r == '_', r == '-', r == '.':
		default:
			return false
		}
	}
	return letter
}
