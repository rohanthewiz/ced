// =============================================================================
// File: internal/editor/find.go
// Author: Rohan Allison <rohanthewiz@gmail.com>
// Created: 2026-04-30
// Copyright: 2026 Rohan Allison. All rights reserved.
// Portions copyright 2026 Cloudmanic, LLC. Original author: Spicer Matthews.
// =============================================================================

// find.go implements the editor's in-file search primitives. Matching runs
// on rune-decoded lines so multi-byte characters behave as one column each
// (consistent with how the cursor / selection already treat columns
// elsewhere), and the default is a case-INSENSITIVE substring: find is a
// reading tool, so "buffer" should turn up every spelling of it.
//
// FindOptions makes that default a choice rather than a law. The two
// toggles it carries (match case, whole word) are exactly the two the
// word-highlight scanner already needed for its own reasons — see
// wordhl.go's header for why THAT feature is case-sensitive and
// whole-word by default — so both features now share one scanner
// (matchCols). Two implementations of "what counts as a hit" would drift,
// and the user would have no way to tell which one answered.
//
// REGEX is the third toggle (FindOptions.Regex), and it brings the three
// things that kept it out for so long: its own input grammar, an error
// surface for a half-typed pattern, and a replacement language ($1).
// They are all answered in one place, the Matcher below, so no caller
// has to know which kind of query it holds:
//
//	query + FindOptions ──NewMatcher──► *Matcher ──┬─ All / Line   (scan)
//	                          │                    ├─ At           (still a hit?)
//	                          └─ error (bad regex) └─ Expand       ($1 replacement)
//
//   - Grammar: Go's RE2 (regexp), matched one LINE at a time. The buffer
//     is a []string of lines and every hit is a (line, col, width)
//     triple, so a pattern can never span a newline — `\n` simply finds
//     nothing. That is the same "find matches within a line" contract
//     the literal search always had, not a regex-specific limitation.
//   - Errors: NewMatcher returns the compile error and every scan entry
//     point treats a bad pattern as "no hits". The UI asks
//     FindQueryError for the reason, so a half-typed `(foo` paints an
//     explanation instead of an alarming "no results".
//   - Replacement: in regex mode the replacement is a TEMPLATE expanded
//     per hit ($1, ${name}, $$ for a literal dollar — regexp.Expand's
//     language). In literal mode it is inserted verbatim, so a "$" typed
//     into a plain replace still means a dollar sign.
//
// The two other toggles compose with it rather than being re-implemented
// in pattern syntax. Case-insensitive is the `(?i)` flag (RE2's folding
// is per rune, so columns stay exact). Whole word is the SAME
// wordBounded post-filter the literal scan uses, not `\b`: RE2's `\b`
// is ASCII-only and would disagree with IsWordRune on "café", and one
// definition of "a word" across both modes beats a faster wrong one.
//
// Zero-width hits (`^`, `x*` on a line without x) are DROPPED. A hit
// paints, counts and jumps as a run of cells; a zero-width one has no
// cells to paint, would inflate "1 of N" with invisible stops, and
// would make Replace all an insert-everywhere. A pattern that only ever
// matches emptily therefore reports no results.

package editor

import (
	"errors"
	"regexp"
	"regexp/syntax"
	"unicode"
	"unicode/utf8"
)

// Match describes one find hit. Line and Col follow the same rune-indexed
// convention as Position; Width is the rune count of the query so the
// renderer can paint the right number of cells without re-running the
// matcher.
type Match struct {
	Line  int
	Col   int
	Width int
}

// FindOptions carries the search modifiers a query is matched under. The
// zero value is the historical default (case-insensitive, substring),
// which is what keeps every caller that doesn't care about options — and
// every test written before they existed — working unchanged.
type FindOptions struct {
	// CaseSensitive matches "Buffer" against "Buffer" but not "buffer".
	CaseSensitive bool
	// WholeWord rejects a hit that sits inside a longer identifier, so
	// "err" stops matching "errors". Boundaries use IsWordRune — the
	// programmer's definition, shared with double-click word select.
	WholeWord bool
	// Regex reads the query as an RE2 pattern (see the file header for
	// how it composes with the two toggles above and with replace).
	Regex bool
}

// Matcher is a query compiled under a set of FindOptions — the one
// object every scan, staleness check and replacement goes through, so
// the literal and regex paths can't drift apart per caller. Build it
// once per search (NewMatcher) and reuse it: a project search shares
// one across every file and worker goroutine, which is safe because
// both of its forms are read-only after construction (*regexp.Regexp
// is documented safe for concurrent use).
//
// A nil *Matcher is valid and matches nothing — what NewMatcher returns
// for an empty query — so callers never need a separate "is there a
// search?" branch.
type Matcher struct {
	opts FindOptions
	// needle is the folded literal query (literal mode only).
	needle []rune
	// re is the compiled pattern (regex mode only).
	re *regexp.Regexp
}

// NewMatcher compiles query under opts. An empty query yields a nil
// Matcher (matches nothing) and no error; a pattern RE2 rejects yields
// the compile error, which FindQueryError shortens for display.
func NewMatcher(query string, opts FindOptions) (*Matcher, error) {
	if query == "" {
		return nil, nil
	}
	if !opts.Regex {
		needle := foldRunes([]rune(query), opts.CaseSensitive)
		if len(needle) == 0 {
			return nil, nil
		}
		return &Matcher{opts: opts, needle: needle}, nil
	}
	pattern := query
	if !opts.CaseSensitive {
		// A flag group rather than lowercasing the pattern: folding the
		// pattern text would corrupt escapes (`\S` → `\s`).
		pattern = "(?i)" + pattern
	}
	re, err := regexp.Compile(pattern)
	if err != nil {
		return nil, err
	}
	return &Matcher{opts: opts, re: re}, nil
}

// FindQueryError reports why query can't be searched under opts, or nil
// when it can. Only a regex can fail. The message is the parser's own
// reason without its "error parsing regexp:" preamble — "missing closing
// )" — because it is shown in tight places (the find bar's counter, a
// flash) where the preamble would crowd out the part that helps.
func FindQueryError(query string, opts FindOptions) error {
	if !opts.Regex {
		return nil
	}
	_, err := NewMatcher(query, opts)
	if err == nil {
		return nil
	}
	var se *syntax.Error
	if errors.As(err, &se) {
		return errors.New(string(se.Code))
	}
	return err
}

// IsRegex reports whether m reads its query as a pattern — the question
// a replace asks before treating its replacement as a template.
func (m *Matcher) IsRegex() bool {
	return m != nil && m.re != nil
}

// All returns every hit in buf, in document order.
func (m *Matcher) All(buf *Buffer) []Match {
	if m == nil || buf == nil {
		return nil
	}
	var out []Match
	for lineIdx, raw := range buf.Lines {
		out = append(out, m.Line(lineIdx, raw)...)
	}
	return out
}

// Line returns the hits on one line (raw is its text, lineIdx goes into
// each Match), left to right and non-overlapping.
func (m *Matcher) Line(lineIdx int, raw string) []Match {
	if m == nil {
		return nil
	}
	if m.re == nil {
		hay := foldRunes([]rune(raw), m.opts.CaseSensitive)
		var out []Match
		for _, col := range matchCols(hay, m.needle, m.opts.WholeWord) {
			out = append(out, Match{Line: lineIdx, Col: col, Width: len(m.needle)})
		}
		return out
	}
	var out []Match
	for _, h := range m.regexHits(raw) {
		out = append(out, Match{Line: lineIdx, Col: h.col, Width: h.width})
	}
	return out
}

// regexHit is one regex match in rune coordinates, plus the byte-offset
// submatch slice Expand needs to fill in $1.
type regexHit struct {
	col, width int
	sub        []int
}

// regexHits runs the pattern over one line and converts each match from
// byte offsets (what regexp speaks) to rune columns (what Position
// speaks), dropping zero-width matches and, under WholeWord, matches
// that sit inside a longer word (see the file header for both rules).
//
// The byte→rune conversion walks the line once, incrementally: each
// match's start is counted from the previous match's start, so a line
// with many hits costs O(len) rather than O(len × hits).
func (m *Matcher) regexHits(raw string) []regexHit {
	idx := m.re.FindAllStringSubmatchIndex(raw, -1)
	if len(idx) == 0 {
		return nil
	}
	var runes []rune // decoded lazily: only WholeWord needs the neighbours
	if m.opts.WholeWord {
		runes = []rune(raw)
	}
	var out []regexHit
	prevByte, prevCol := 0, 0
	for _, sub := range idx {
		start, end := sub[0], sub[1]
		col := prevCol + utf8.RuneCountInString(raw[prevByte:start])
		prevByte, prevCol = start, col
		width := utf8.RuneCountInString(raw[start:end])
		if width == 0 {
			continue
		}
		if runes != nil && !wordBounded(runes, col, width) {
			continue
		}
		out = append(out, regexHit{col: col, width: width, sub: sub})
	}
	return out
}

// At reports whether line holds a hit starting at col and exactly width
// runes wide — the one-range "is this still a match?" question
// (MatchesAt's contract, below).
//
// Regex mode re-scans the whole line rather than trying the pattern at
// col alone: whether a pattern matches at a column depends on what
// precedes it (`^`, and RE2's leftmost-first choice among overlapping
// candidates), so only the full-line scan agrees with All exactly.
func (m *Matcher) At(line []rune, col, width int) bool {
	if m == nil || col < 0 {
		return false
	}
	if m.re == nil {
		n := len(m.needle)
		if width != n || col+n > len(line) {
			return false
		}
		if !runesEqual(foldRunes(line[col:col+n], m.opts.CaseSensitive), m.needle) {
			return false
		}
		return !m.opts.WholeWord || wordBounded(line, col, n)
	}
	for _, h := range m.regexHits(string(line)) {
		if h.col == col && h.width == width {
			return true
		}
	}
	return false
}

// Expand returns the text that replaces the hit at (col, width) on line.
// Literal mode returns template verbatim. Regex mode expands $1 /
// ${name} / $$ against that hit's submatches; a hit that is no longer
// there (the caller should have checked At) also falls back to the
// template verbatim rather than inventing captures.
func (m *Matcher) Expand(line []rune, col, width int, template string) string {
	if !m.IsRegex() {
		return template
	}
	raw := string(line)
	for _, h := range m.regexHits(raw) {
		if h.col == col && h.width == width {
			return string(m.re.ExpandString(nil, template, raw, h.sub))
		}
	}
	return template
}

// FindAll returns every case-insensitive substring match of query inside
// buf, in document order — FindAllOpts under the default options. Kept as
// its own entry point for callers that genuinely want the plain
// reading-tool behavior (tests, one-off scans) and shouldn't have to name
// a zero struct to say so. Anything the find bar's Aa / |W| toggles
// should reach — the Find-all list, project search — calls FindAllOpts.
func FindAll(buf *Buffer, query string) []Match {
	return FindAllOpts(buf, query, FindOptions{})
}

// FindAllOpts returns every match of query inside buf under opts, in
// document order. An empty query returns nil — the caller is expected to
// clear its UI rather than show "0 of 0" results. Matches do not overlap:
// after a hit the scanner advances past the matched run, so "aaaa" with
// query "aa" yields two matches at columns 0 and 2.
//
// A regex that doesn't compile also returns nil — a half-typed pattern
// is "no hits yet", not a failure; FindQueryError says why.
func FindAllOpts(buf *Buffer, query string, opts FindOptions) []Match {
	m, err := NewMatcher(query, opts)
	if err != nil {
		return nil
	}
	return m.All(buf)
}

// matchCols returns the starting columns of every non-overlapping hit of
// needle inside hay. It is the ONE place the editor decides what counts
// as a match: FindAllOpts (whole buffer, optionally case-folded) and
// MatchOccurrences (a line window, always case-sensitive) both come
// through here, so an in-file search, a project search and the word
// highlight can never disagree about a hit.
//
// Case folding is the CALLER's job — it must fold the needle and the
// haystack the same way, and folding a whole line once beats folding a
// slice of it per candidate column.
func matchCols(hay, needle []rune, wholeWord bool) []int {
	if len(needle) == 0 || len(needle) > len(hay) {
		return nil
	}
	var cols []int
	for col := 0; col+len(needle) <= len(hay); col++ {
		if !runesEqual(hay[col:col+len(needle)], needle) {
			continue
		}
		if wholeWord && !wordBounded(hay, col, len(needle)) {
			continue
		}
		cols = append(cols, col)
		col += len(needle) - 1 // the loop's ++ carries us past the hit
	}
	return cols
}

// wordBounded reports whether the run hay[col:col+n] stands alone as a
// word: no word rune immediately before or after it. Split out of
// matchCols so MatchesAt applies the SAME boundary rule to one range
// that the scanner applies to every candidate.
func wordBounded(hay []rune, col, n int) bool {
	if col > 0 && IsWordRune(hay[col-1]) {
		return false
	}
	if end := col + n; end < len(hay) && IsWordRune(hay[end]) {
		return false
	}
	return true
}

// MatchesAt reports whether line holds a hit of query starting at col,
// width runes wide, under opts — the one-range question "is this still a
// match?", asked by
// callers that hold a hit's coordinates from an earlier scan (the
// Find-all list's stale-row check and its replace plan) and need to know
// whether the text under them has moved.
//
// It must agree with FindAllOpts exactly: a check stricter than the scan
// (say, a byte compare after a case-insensitive scan) would call a fresh
// "Count" hit for "count" stale, dim it, and skip it on replace. So it
// folds and bounds through the same helpers matchCols uses. In literal
// mode the one thing it does not re-check is overlap with an earlier hit
// on the line; a caller asking about a single range has no earlier hit
// to overlap. width matters because a regex's hits differ in width: a
// row recorded as "foo123" must not stay live when the line now reads
// "foo1".
//
// Callers checking many ranges against one query should build a Matcher
// once and call At — this compiles per call.
func MatchesAt(line []rune, col, width int, query string, opts FindOptions) bool {
	m, err := NewMatcher(query, opts)
	if err != nil {
		return false
	}
	return m.At(line, col, width)
}

// foldRunes lowercases rs for a case-insensitive compare, or returns it
// untouched when the search is case-sensitive.
//
// Per-rune unicode.ToLower rather than strings.ToLower on purpose: a few
// runes (U+0130 LATIN CAPITAL I WITH DOT ABOVE is the classic) lowercase
// to MORE than one rune, and a fold that changes the rune count would
// shift every column reported after it — the match would paint over the
// wrong cells. Per-rune folding is length-preserving by construction, so
// a column in the folded line is the same column in the real one.
func foldRunes(rs []rune, caseSensitive bool) []rune {
	if caseSensitive {
		return rs
	}
	out := make([]rune, len(rs))
	for i, r := range rs {
		out[i] = unicode.ToLower(r)
	}
	return out
}

// runesEqual returns true when two equal-length rune slices match
// element-for-element. Inlined so the hot inner loop of FindAll doesn't
// pay for a generic slices.Equal call (which exists in 1.21+ but pulls in
// the slices package).
func runesEqual(a, b []rune) bool {
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// FirstMatchAtOrAfter returns the index into matches of the first hit at
// or after cursor, or 0 when cursor sits past the last match (we wrap
// around to the top — that's what the user expects after typing a query
// at the bottom of a file).
//
// Returns -1 when matches is empty so callers can short-circuit without
// re-checking the length.
func FirstMatchAtOrAfter(matches []Match, cursor Position) int {
	if len(matches) == 0 {
		return -1
	}
	for i, m := range matches {
		if m.Line > cursor.Line || (m.Line == cursor.Line && m.Col >= cursor.Col) {
			return i
		}
	}
	// Cursor is past every match — wrap to the top.
	return 0
}

// MatchPosition returns the cursor-friendly Position at the start of m.
// Trivial helper, but it keeps callers from constructing Position literals
// by hand (which loses the "rune-indexed" intent at the call site).
func MatchPosition(m Match) Position {
	return Position{Line: m.Line, Col: m.Col}
}

// MatchEndPosition returns the position one past the end of m — useful
// when the caller wants to set a selection that covers the match.
func MatchEndPosition(m Match) Position {
	return Position{Line: m.Line, Col: m.Col + m.Width}
}

// SetFindQuery installs a new search query on the tab, recomputes the
// match list against the current buffer, and points FindIndex at the
// first match at or after the cursor (so the user lands on the nearest
// hit, not always the first hit in the file). An empty query clears all
// find state — symmetrical with closing the bar via Esc.
//
// The cursor is left where it is; SetFindQuery only updates state. It is
// the caller's job to call FocusCurrentMatch when they want the cursor
// to actually move (which is what happens on the first non-empty query
// and on every Enter / Shift-Enter press).
func (t *Tab) SetFindQuery(query string) {
	t.FindQuery = query
	if query == "" {
		t.FindMatches = nil
		t.FindIndex = -1
		return
	}
	t.FindMatches = FindAllOpts(t.Buffer, query, t.FindOpts)
	t.FindIndex = FirstMatchAtOrAfter(t.FindMatches, t.Cursor)
}

// SetFindOptions installs new search modifiers and re-runs the current
// query under them. Going through one method (rather than letting callers
// poke t.FindOpts) is what guarantees the match list on screen was
// produced by the options the toggles are showing — a stale list under a
// flipped toggle is a lie the user can't detect.
func (t *Tab) SetFindOptions(opts FindOptions) {
	t.FindOpts = opts
	if t.FindQuery != "" {
		t.SetFindQuery(t.FindQuery)
	}
}

// FocusCurrentMatch moves the cursor (and anchor — we don't want a
// dangling selection from an earlier action) to the start of the
// currently-pointed match. No-op when FindIndex is out of range, so
// callers don't have to re-check it themselves.
func (t *Tab) FocusCurrentMatch() {
	if t.FindIndex < 0 || t.FindIndex >= len(t.FindMatches) {
		return
	}
	m := t.FindMatches[t.FindIndex]
	t.Cursor = MatchPosition(m)
	t.Anchor = t.Cursor
	// Jumping to a hit is an explicit navigation, so it drops secondary
	// carets for the same reason MoveCursorTo does.
	t.Carets = nil
	// A hit is a jump: it lands with context around it rather than on
	// the view's edge row (jumpmargin.go).
	t.MarkJump()
}

// FindNext advances FindIndex by one (wrapping at the end) and moves
// the cursor onto the new match. No-op when there are no matches. Used
// by Enter inside the find bar and by the Esc-g "again" leader.
func (t *Tab) FindNext() {
	if len(t.FindMatches) == 0 {
		return
	}
	t.FindIndex = (t.FindIndex + 1) % len(t.FindMatches)
	t.FocusCurrentMatch()
}

// FindPrev moves FindIndex backwards by one (wrapping at the start) and
// moves the cursor onto the new match. Used by Shift-Enter inside the
// find bar.
func (t *Tab) FindPrev() {
	if len(t.FindMatches) == 0 {
		return
	}
	t.FindIndex--
	if t.FindIndex < 0 {
		t.FindIndex = len(t.FindMatches) - 1
	}
	t.FocusCurrentMatch()
}

// ClearFind drops every piece of find state. The app calls this when the
// buffer has been edited enough that the cached match list is stale and
// can't safely be re-used; the user will re-type their query.
func (t *Tab) ClearFind() {
	t.FindQuery = ""
	t.FindMatches = nil
	t.FindIndex = -1
}
