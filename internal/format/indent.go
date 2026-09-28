// =============================================================================
// File: internal/format/indent.go
// Author: Rohan Allison <rohanthewiz@gmail.com>
// Created: 2026-09-28
// Copyright: 2026 Rohan Allison. All rights reserved.
// =============================================================================

// indent.go reads a document's EXISTING indentation so ced's formatters
// can keep it.
//
// # Why formatters ask the file first
//
// A data file's indentation is a decision somebody already made — a
// repo whose package.json uses tabs, a config file hand-kept at four
// spaces. Reformatting such a file to ced's house style turns a
// one-character edit into a whole-file diff, which is exactly the churn
// a formatter is supposed to prevent. So the formatter's job on a
// document is narrowed to LAYOUT UNDER THE FILE'S OWN RULES: fix the
// structure, keep the indent unit.
//
// Only when the file has no opinion — minified onto one line, or empty —
// does the formatter's default (two spaces) apply.
//
// # The detection rule
//
//	for every line with leading whitespace AND content after it:
//	    starts with a tab    → one vote for tabs
//	    starts with a space  → one vote for spaces; remember its width
//	tabs win outright        → Indent{Tabs: true}
//	otherwise                → Indent{Width: narrowest space indent}
//
// The narrowest non-zero space run is the unit because every indented
// line in a structured document sits a whole number of units deep, and
// the shallowest one is exactly one unit deep. A GCD over all widths
// would say the same thing on a clean file and collapse to 1 on a file
// with a single stray line, which is the worse failure: a 1-space
// reindent of a 4-space file.
//
// Blank and whitespace-only lines are ignored: editors leave trailing
// indentation on empty lines and it says nothing about the unit.

package format

import (
	"bytes"
	"strconv"
	"strings"
)

// maxIndentWidth caps a detected space unit. Anything wider is almost
// certainly alignment (a hand-lined-up value column) rather than
// nesting, and handing a formatter a 12-space unit would push every
// nested document off the right edge of the screen.
const maxIndentWidth = 8

// Indent describes a document's indentation unit. The zero value means
// "not detected" — the caller keeps its own default.
type Indent struct {
	// Tabs reports one tab per nesting level. Width is meaningless
	// when set.
	Tabs bool
	// Width is the number of spaces per nesting level; 0 when the
	// file offered no evidence.
	Width int
}

// Known reports whether detection found an indentation unit at all.
func (i Indent) Known() bool {
	return i.Tabs || i.Width > 0
}

// Unit returns the literal text of one indentation level, or fallback
// when nothing was detected.
func (i Indent) Unit(fallback string) string {
	switch {
	case i.Tabs:
		return "\t"
	case i.Width > 0:
		return strings.Repeat(" ", i.Width)
	default:
		return fallback
	}
}

// String renders the unit for status messages and test failures:
// "tabs", "4 spaces", or "unknown".
func (i Indent) String() string {
	switch {
	case i.Tabs:
		return "tabs"
	case i.Width > 0:
		return strconv.Itoa(i.Width) + " spaces"
	default:
		return "unknown"
	}
}

// DetectIndent inspects src and reports its indentation unit. See the
// file comment for the rule. It never fails: a document with no
// indented lines returns the zero Indent.
func DetectIndent(src []byte) Indent {
	var tabVotes, spaceVotes int
	narrowest := 0
	for len(src) > 0 {
		// Walk line by line without allocating: formatting runs on every
		// explicit Format, and a large JSON file can be many MB.
		line := src
		if nl := bytes.IndexByte(src, '\n'); nl >= 0 {
			line, src = src[:nl], src[nl+1:]
		} else {
			src = nil
		}
		if len(line) == 0 || (line[0] != ' ' && line[0] != '\t') {
			continue
		}
		// Measure the leading run of the SAME character the line
		// started with; a mixed run (tab then spaces) is counted by its
		// first character, which is how editors decide it too.
		lead := line[0]
		n := 0
		for n < len(line) && line[n] == lead {
			n++
		}
		rest := bytes.TrimSpace(line[n:])
		if len(rest) == 0 {
			// Whitespace-only line — carries no signal.
			continue
		}
		if lead == '\t' {
			tabVotes++
			continue
		}
		spaceVotes++
		if narrowest == 0 || n < narrowest {
			narrowest = n
		}
	}
	switch {
	case tabVotes == 0 && spaceVotes == 0:
		return Indent{}
	case tabVotes > spaceVotes:
		return Indent{Tabs: true}
	case narrowest > maxIndentWidth:
		// Evidence of indentation, but only of alignment-sized runs:
		// better to say nothing than to impose an absurd unit.
		return Indent{}
	default:
		return Indent{Width: narrowest}
	}
}
