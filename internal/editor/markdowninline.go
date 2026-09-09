// =============================================================================
// File: internal/editor/markdowninline.go
// Author: Rohan Allison
// Created: 2026-09-09
// Copyright: 2026 Cloudmanic, LLC. All rights reserved.
// =============================================================================

// markdowninline.go is the inline half of the markdown viewer: one line
// of markdown text in, a list of styled spans out. Block structure is
// markdownblocks.go's problem; everything here happens INSIDE a
// paragraph, a heading, a list item or a table cell.
//
// It is a hand-written scanner rather than a parser generator or a
// dependency, for the reason the frontmatter parser in internal/skills
// gives: the whole surface is a dozen delimiters, and the promise is one
// static binary with no CGO. A CommonMark library would bring a document
// model this viewer has no use for — it never round-trips, never emits
// HTML, and answers exactly one question per rune, "what color".
//
// Where it deliberately diverges from the spec:
//
//   - Emphasis is matched by SCANNING FORWARD for a closing delimiter on
//     the same line, not by CommonMark's left/right-flanking run
//     algorithm. On real prose the two agree; on a line of stray
//     asterisks the spec is righter and this is cheaper, and the cost of
//     being wrong is a few cells of italic.
//   - A link renders its TEXT, not its URL. In a terminal there is
//     nothing to click, and repeating every href inline would double the
//     length of a link-dense document to say what the source already
//     says one keystroke away. Autolinks and bare URLs are their own
//     text, so those are not hidden.
//   - Raw HTML is passed through as muted literal text. Hiding it would
//     lose content the author wrote; rendering it would need an HTML
//     parser, which is the dependency this file exists to avoid.

package editor

import (
	"strings"

	"github.com/gdamore/tcell/v2"
)

// parseInline turns one line of markdown into styled spans, starting
// from base. depth bounds the recursion emphasis nesting can cause.
func parseInline(s string, base tcell.Style, p mdPalette, depth int) []mdSpan {
	var out []mdSpan
	var lit strings.Builder

	// flush moves whatever plain text has accumulated into a span. Every
	// branch below calls it before emitting its own span, which is what
	// keeps the output in source order.
	flush := func() {
		if lit.Len() > 0 {
			out = append(out, mdSpan{text: lit.String(), style: base})
			lit.Reset()
		}
	}
	emit := func(spans []mdSpan) {
		flush()
		out = append(out, spans...)
	}

	rs := []rune(s)
	for i := 0; i < len(rs); {
		r := rs[i]

		// A backslash escapes the next punctuation rune. Checked first,
		// so "\*not em\*" is literal asterisks and nothing below sees a
		// delimiter at all.
		if r == '\\' && i+1 < len(rs) && isMDPunct(rs[i+1]) {
			lit.WriteRune(rs[i+1])
			i += 2
			continue
		}

		// Code spans win over every other delimiter, per the spec and
		// for the obvious practical reason: "`**`" is a literal pair of
		// asterisks, and a reader looking at a document ABOUT markdown
		// is the person most likely to notice if it isn't.
		if r == '`' {
			run := runLen(rs, i, '`')
			if end := findRun(rs, i+run, '`', run); end >= 0 {
				body := strings.TrimSpace(string(rs[i+run : end]))
				emit([]mdSpan{{text: body, style: p.inline}})
				i = end + run
				continue
			}
		}

		// An image is a link with a bang, and has to be tested first or
		// the '[' branch would claim it and leave a stray '!' behind.
		if r == '!' && i+1 < len(rs) && rs[i+1] == '[' {
			if alt, _, next, ok := parseLinkAt(rs, i+1); ok {
				label := strings.TrimSpace(alt)
				if label == "" {
					label = "image"
				}
				// ▣ marks it as a thing that is not here: the viewer
				// cannot show the image (that is the image TAB's job,
				// and the file may not even be local), so saying so is
				// more honest than printing the alt text alone as if it
				// were prose.
				emit([]mdSpan{{text: "▣ " + label, style: p.image}})
				i = next
				continue
			}
		}

		if r == '[' {
			if text, _, next, ok := parseLinkAt(rs, i); ok {
				// The label is parsed as inline too — "[**bold** link]"
				// is ordinary — but forced onto the link style, so a
				// link always reads as one whatever it contains.
				inner := parseInline(text, p.link, p, depth+1)
				if depth >= mdMaxInlineDepth {
					inner = []mdSpan{{text: text, style: p.link}}
				}
				emit(inner)
				i = next
				continue
			}
		}

		// <https://example.com> — the text IS the URL, so nothing is
		// hidden by rendering it.
		if r == '<' {
			if end := indexRuneFrom(rs, i+1, '>'); end >= 0 {
				body := string(rs[i+1 : end])
				if isMDAutolink(body) {
					emit([]mdSpan{{text: body, style: p.link}})
					i = end + 1
					continue
				}
			}
		}

		// A bare URL, the form people actually paste. Only at a word
		// boundary, or "see:https://" style text would light up mid-word.
		if (r == 'h' || r == 'H') && (i == 0 || isMDBoundary(rs[i-1])) {
			if n := bareURLLen(rs, i); n > 0 {
				emit([]mdSpan{{text: string(rs[i : i+n]), style: p.link}})
				i += n
				continue
			}
		}

		if r == '~' && runLen(rs, i, '~') >= 2 {
			if end := findRun(rs, i+2, '~', 2); end >= 0 {
				emit(parseNested(string(rs[i+2:end]), base.StrikeThrough(true), p, depth))
				i = end + 2
				continue
			}
		}

		if r == '*' || r == '_' {
			run := runLen(rs, i, r)
			// An intraword underscore is not emphasis — snake_case_names
			// are far more common in a code-adjacent document than
			// mid-word italics, and treating them as delimiters is the
			// single most visible way this renderer could be wrong.
			if r == '_' && i > 0 && !isMDBoundary(rs[i-1]) {
				lit.WriteRune(r)
				i++
				continue
			}
			// Three is bold+italic, two bold, one italic. Longer runs
			// fall back to the top of that ladder rather than being
			// treated as literal, which is what a decorative "*****"
			// separator would otherwise become.
			want := run
			if want > 3 {
				want = 3
			}
			for ; want >= 1; want-- {
				end := findRun(rs, i+want, r, want)
				if end < 0 {
					continue
				}
				st := base
				switch want {
				case 3:
					st = st.Bold(true).Italic(true)
				case 2:
					st = st.Bold(true)
				default:
					st = st.Italic(true)
				}
				emit(parseNested(string(rs[i+want:end]), st, p, depth))
				i = end + want
				break
			}
			if want >= 1 {
				continue
			}
		}

		lit.WriteRune(r)
		i++
	}
	flush()
	return out
}

// parseNested recurses into an emphasis body, or gives up and emits the
// body flat once the depth bound is reached.
func parseNested(body string, st tcell.Style, p mdPalette, depth int) []mdSpan {
	if depth >= mdMaxInlineDepth {
		return []mdSpan{{text: body, style: st}}
	}
	return parseInline(body, st, p, depth+1)
}

// parseLinkAt reads "[text](dest)" starting at the '[' in rs[i], and
// reports the label, the destination, and the index just past the
// closing paren. Bracket nesting inside the label is tracked so
// "[see [1]](x)" resolves to the outer pair; a reference-style link
// ("[text][ref]") is deliberately NOT matched, since resolving it needs
// a link-definition table this reader does not build — it falls through
// and renders as the literal brackets the source contains.
func parseLinkAt(rs []rune, i int) (text, dest string, next int, ok bool) {
	if i >= len(rs) || rs[i] != '[' {
		return "", "", 0, false
	}
	depth := 0
	close := -1
	for j := i; j < len(rs); j++ {
		switch rs[j] {
		case '\\':
			j++
		case '[':
			depth++
		case ']':
			depth--
			if depth == 0 {
				close = j
			}
		}
		if close >= 0 {
			break
		}
	}
	if close < 0 || close+1 >= len(rs) || rs[close+1] != '(' {
		return "", "", 0, false
	}
	pdepth := 0
	end := -1
	for j := close + 1; j < len(rs); j++ {
		switch rs[j] {
		case '\\':
			j++
		case '(':
			pdepth++
		case ')':
			pdepth--
			if pdepth == 0 {
				end = j
			}
		}
		if end >= 0 {
			break
		}
	}
	if end < 0 {
		return "", "", 0, false
	}
	return string(rs[i+1 : close]), string(rs[close+2 : end]), end + 1, true
}

// runLen counts how many consecutive copies of r start at rs[i].
func runLen(rs []rune, i int, r rune) int {
	n := 0
	for i+n < len(rs) && rs[i+n] == r {
		n++
	}
	return n
}

// findRun locates the start of the next run of EXACTLY n copies of r at
// or after index from, skipping backslash-escaped runes. "Exactly" is
// what stops the closing "**" of a bold span being found inside a
// literal "***" further down the line.
func findRun(rs []rune, from int, r rune, n int) int {
	for j := from; j+n <= len(rs); j++ {
		if rs[j] == '\\' {
			j++
			continue
		}
		if rs[j] != r {
			continue
		}
		if runLen(rs, j, r) != n {
			// Skip the whole run, not one rune of it, or a "***" would
			// be re-examined at each of its three offsets.
			j += runLen(rs, j, r) - 1
			continue
		}
		if j == from {
			// An empty span ("**" immediately followed by "**") is not
			// emphasis; keep looking.
			continue
		}
		return j
	}
	return -1
}

// indexRuneFrom is strings.IndexRune over a rune slice, from an offset.
func indexRuneFrom(rs []rune, from int, r rune) int {
	for j := from; j < len(rs); j++ {
		if rs[j] == r {
			return j
		}
	}
	return -1
}

// isMDPunct reports whether r is a rune markdown lets a backslash
// escape. Deliberately the ASCII punctuation set the spec names rather
// than unicode.IsPunct: escaping a Unicode dash is not a thing markdown
// does, and treating it as one would eat the backslash.
func isMDPunct(r rune) bool {
	return strings.ContainsRune("!\"#$%&'()*+,-./:;<=>?@[\\]^_`{|}~", r)
}

// isMDBoundary reports whether r ends a word — used to keep underscores
// inside identifiers from opening emphasis, and to keep a bare URL from
// being recognised mid-word.
func isMDBoundary(r rune) bool {
	return r == ' ' || r == '\t' || strings.ContainsRune("([{<\"'*_~-,;:!?", r)
}

// isMDAutolink reports whether the body of a <…> is a URL rather than an
// HTML tag. Scheme-prefixed only, so "<div>" stays literal text.
func isMDAutolink(s string) bool {
	low := strings.ToLower(s)
	return strings.HasPrefix(low, "http://") || strings.HasPrefix(low, "https://") ||
		strings.HasPrefix(low, "mailto:") || strings.HasPrefix(low, "ftp://")
}

// bareURLLen reports the length in runes of an unmarked http(s) URL
// starting at rs[i], or 0 if there isn't one. Trailing punctuation is
// excluded so "see https://x.com." doesn't underline the sentence's
// full stop.
func bareURLLen(rs []rune, i int) int {
	low := strings.ToLower(string(rs[i:min(i+8, len(rs))]))
	var n int
	switch {
	case strings.HasPrefix(low, "https://"):
		n = 8
	case strings.HasPrefix(low, "http://"):
		n = 7
	default:
		return 0
	}
	for i+n < len(rs) && !isMDURLEnd(rs[i+n]) {
		n++
	}
	for n > 0 && strings.ContainsRune(".,;:!?)]}'\"", rs[i+n-1]) {
		n--
	}
	if n <= 8 {
		return 0
	}
	return n
}

// isMDURLEnd reports whether r terminates a bare URL.
func isMDURLEnd(r rune) bool {
	return r == ' ' || r == '\t' || r == '<' || r == '>' || r == '`' || r == '"'
}
