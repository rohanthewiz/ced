// =============================================================================
// File: internal/format/jsonexplain.go
// Author: Rohan Allison <rohanthewiz@gmail.com>
// Created: 2026-09-30
// Copyright: 2026 Rohan Allison. All rights reserved.
// =============================================================================

// jsonexplain.go turns encoding/json's syntax errors into an answer a
// reader can act on, pointed at the character that is actually WRONG.
//
// The parser reports where it gave up, which is often one token after
// the mistake, in the grammar's vocabulary:
//
//	"tags": ["a", "b",],
//	                  ^ invalid character ']' looking for beginning of value
//
// The ']' is blameless; the comma before it is the error. An underline
// on the ']' reads as "the editor is confused", and the message does not
// help the reader decide otherwise. So for the shapes people actually
// type — trailing commas, missing commas, comments, single quotes,
// unquoted keys, mismatched or unclosed brackets, line breaks inside a
// string — this file names the mistake and moves the mark onto it:
//
//	"tags": ["a", "b",],
//	                 ^ trailing comma: JSON allows no ',' before ']'
//
// # House rules
//
//   - **Only rephrase what the parser proved.** Every rewrite is keyed by
//     the parser's own error class plus the byte it choked on and the
//     nearest non-blank byte before it; nothing re-parses or guesses at
//     intent beyond that. Anything unrecognised keeps the parser's own
//     wording, because a vague true message beats a confident wrong one.
//   - **Offsets in, offsets out.** The caller still owns the byte→rune
//     conversion (offsetToLineCol), so the runes-not-bytes rule lives in
//     one place.
//   - **Costs nothing on a valid file.** This runs only after Unmarshal
//     has already failed; the bracket scan is linear and allocates a
//     stack only as deep as the nesting.

package format

import (
	"encoding/json"
	"fmt"
	"strings"
)

// explainJSONError returns a reader-facing message for a syntax error in
// src and the BYTE offset of the character it describes.
func explainJSONError(src []byte, syn *json.SyntaxError) (string, int) {
	raw := syn.Error()
	// Offset is one past the byte the parser consumed and rejected; see
	// validateJSON. Clamped so an EOF error still names a real byte.
	bad := min(max(int(syn.Offset)-1, 0), len(src)-1)

	if strings.HasPrefix(raw, "unexpected end") {
		return explainJSONEOF(src, bad)
	}

	c := src[bad]
	prev := prevNonBlank(src, bad)
	var pc byte
	if prev >= 0 {
		pc = src[prev]
	}

	switch {
	case strings.HasSuffix(raw, "looking for beginning of value"),
		strings.HasSuffix(raw, "looking for beginning of object key string"):
		key := strings.HasSuffix(raw, "object key string")
		switch {
		case c == ']' || c == '}':
			if msg, at, ok := mismatchedCloser(src, bad); ok {
				return msg, at
			}
			if pc == ',' {
				// The parser stops on the closer; the mistake is the comma,
				// so that is what gets the mark.
				return fmt.Sprintf("trailing comma: JSON allows no ',' before '%c'", c), prev
			}
		case c == '/':
			return "comments are not allowed in JSON", bad
		case c == '\'':
			if key {
				return "object keys need double quotes, not single", bad
			}
			return "strings need double quotes, not single", bad
		case c == ',':
			if pc == ',' {
				return "extra ',': nothing between the two commas", bad
			}
			return "missing value before ','", bad
		case key && isJSONWordByte(c):
			return "object keys must be in double quotes", bad
		case !key && isJSONWordByte(c):
			// t/f/n start the literals, so they never reach this branch —
			// a word that does is NaN, undefined, True, or an unquoted
			// string.
			return "unquoted word: strings need double quotes; the only bare words are true, false and null", bad
		}

	case strings.HasSuffix(raw, "after object key:value pair"),
		strings.HasSuffix(raw, "after array element"):
		switch {
		case c == ']' || c == '}':
			if msg, at, ok := mismatchedCloser(src, bad); ok {
				return msg, at
			}
		case c == '/':
			return "comments are not allowed in JSON", bad
		case prev >= 0 && startsJSONValue(c):
			// Another value where a ',' or closer belongs: the comma after
			// the PREVIOUS value is what's missing, so the mark goes on the
			// end of that value — that is where the fix is typed.
			return "missing ',' after this value", prev
		}

	// The key itself parsed; the ':' after it did not arrive.
	case strings.HasSuffix(raw, "after object key"):
		return "missing ':' between the key and its value", bad

	case strings.HasSuffix(raw, "after top-level value"):
		if c == ']' || c == '}' {
			return fmt.Sprintf("'%c' has no matching opener: the document already ended", c), bad
		}
		return "extra text after the end of the document", bad

	case strings.HasSuffix(raw, "in string literal"):
		// A control character inside a string. A raw line break is by far
		// the common one: the closing quote is missing, and it belongs at
		// the end of the line the string started on.
		switch c {
		case '\n', '\r':
			return "line break inside a string: the closing '\"' is missing (or write \\n)", max(bad-1, 0)
		case '\t':
			return "raw tab inside a string: write \\t", bad
		}
		return "control character inside a string", bad

	case strings.HasSuffix(raw, "in string escape code"):
		return fmt.Sprintf("unknown escape '\\%c': JSON allows \\\" \\\\ \\/ \\b \\f \\n \\r \\t \\uXXXX", c), bad
	}
	return raw, bad
}

// explainJSONEOF handles "unexpected end of JSON input": the file ran
// out while something was still open. The useful answer is WHAT was left
// open and WHERE it began — pointing at the last byte of the file says
// nothing about which of a dozen braces lost its partner.
//
// The phrase "unexpected end" is kept as the lead so the message still
// reads as the parser's verdict, with the explanation after it.
func explainJSONEOF(src []byte, bad int) (string, int) {
	stack, strStart := jsonOpeners(src, len(src))
	if strStart >= 0 {
		return "unexpected end of file: this string is never closed", strStart
	}
	if n := len(stack); n > 0 {
		open := stack[n-1]
		return fmt.Sprintf("unexpected end of file: this '%c' is never closed (a '%c' is missing)",
			src[open], closerFor(src[open])), open
	}
	return "unexpected end of file: the last value is incomplete", bad
}

// mismatchedCloser reports a closer that does not match the innermost
// open bracket — `[1, 2}` — naming where that bracket was opened. ok is
// false when the closer DOES match (the caller has a better story, such
// as a trailing comma) or nothing is open.
func mismatchedCloser(src []byte, at int) (string, int, bool) {
	stack, _ := jsonOpeners(src, at)
	if len(stack) == 0 {
		return "", 0, false
	}
	open := stack[len(stack)-1]
	if closerFor(src[open]) == src[at] {
		return "", 0, false
	}
	line := 1 + strings.Count(string(src[:open]), "\n")
	return fmt.Sprintf("'%c' does not match the '%c' opened on line %d (expected '%c')",
		src[at], src[open], line, closerFor(src[open])), at, true
}

// jsonOpeners scans src[:end] and returns the offsets of the brackets
// still open there, innermost last, plus the offset of the opening quote
// of a string still open at end (-1 when none).
//
// String-aware, so a brace inside "a {b}" is text, not structure, and
// escape-aware, so "\"" does not end a string. Closers pop regardless
// of kind: the parser has already accepted everything before the error,
// so the prefix is well-nested and the kind check is never needed here.
func jsonOpeners(src []byte, end int) (stack []int, strStart int) {
	strStart = -1
	for i := 0; i < end; i++ {
		b := src[i]
		if strStart >= 0 {
			switch b {
			case '\\':
				i++ // skip the escaped byte, whatever it is
			case '"':
				strStart = -1
			}
			continue
		}
		switch b {
		case '"':
			strStart = i
		case '{', '[':
			stack = append(stack, i)
		case '}', ']':
			if len(stack) > 0 {
				stack = stack[:len(stack)-1]
			}
		}
	}
	return stack, strStart
}

// prevNonBlank returns the offset of the last non-whitespace byte before
// i, or -1.
func prevNonBlank(src []byte, i int) int {
	for j := i - 1; j >= 0; j-- {
		switch src[j] {
		case ' ', '\t', '\n', '\r':
			continue
		}
		return j
	}
	return -1
}

// closerFor maps an opening bracket to its partner.
func closerFor(open byte) byte {
	if open == '[' {
		return ']'
	}
	return '}'
}

// startsJSONValue reports whether b can begin a JSON value — what the
// parser found where a ',' belonged, in the missing-comma shape. Letters
// count too: an unquoted word after a value is still "a value with no
// comma before it" to the reader.
func startsJSONValue(b byte) bool {
	return b == '"' || b == '{' || b == '[' || b == '-' || (b >= '0' && b <= '9') || isJSONWordByte(b)
}

// isJSONWordByte reports an ASCII letter, '_' or '$' — the start of a
// bare identifier, which is what an unquoted key or value looks like.
func isJSONWordByte(b byte) bool {
	return (b >= 'a' && b <= 'z') || (b >= 'A' && b <= 'Z') || b == '_' || b == '$'
}
