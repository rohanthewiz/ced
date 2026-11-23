// =============================================================================
// File: internal/format/jsonkeys.go
// Author: Rohan Allison <rohanthewiz@gmail.com>
// Created: 2026-11-23
// Copyright: 2026 Rohan Allison. All rights reserved.
// =============================================================================

// jsonkeys.go is the engine behind ≡ File "Reformat JSON (pick keys)…"
// (app/jsonkeys.go): read a run of JSON RECORDS, list every top-level key
// they carry, and re-emit them keeping only the keys the user ticked.
//
// The motivating text is a structured log — one JSON object per line,
// fifteen keys each, and the three you care about (time, level, msg)
// buried among file / func / line / job_name. But the same question is
// asked of an API response saved to disk (an array of objects) and of a
// single config object, so the parser recognises four SHAPES and the
// projection preserves whichever one it was handed:
//
//	shape       what it looks like                 what a record is
//	─────────   ────────────────────────────────   ───────────────────────────
//	JSONObject  the whole text is one {…}          that object
//	JSONArray   the whole text is one […]          each object element
//	JSONLines   one value per line, other lines    each line that is a whole
//	            allowed (a log with stack traces)  object on its own
//	JSONStream  several values back to back, some  each object value
//	            spanning lines (pretty-printed)
//
// Detection order matters and is: one whole value → (pure lines) → a
// stream of values → lines with passengers. A single document is checked
// first because its lines are fragments that must never be read as
// records; pure NDJSON (every non-blank line an object) is the common
// log case and is taken without running the stream splitter; a stream is
// tried next so a pretty-printed sequence of objects is not misread line
// by line; and lines mode is the fallback that tolerates junk between
// records — a panic's stack trace in the middle of a log is still a log.
//
// # Bytes, never Marshal
//
// The same rule as inprocess.go: nothing here decodes a value into Go
// types and re-encodes it. A kept field is written as its ORIGINAL key
// bytes, a colon, and its original value bytes passed through
// json.Compact — a pure whitespace pass — so number spellings (`1e3`,
// 64-bit ids), string escapes and key order inside nested values survive
// exactly. The only decode is of the key NAME, for matching and for the
// pick-list, and it is skipped when the key holds no escape.
//
// The invariant that pins this (TestProject_AllKeysIsCompact, and the
// fuzz target): keeping EVERY key, unpretty, yields exactly json.Compact
// of each record. Projection is therefore "compact, minus the dropped
// fields" and nothing else.
//
// # Scanning without a second parser
//
// encoding/json does not expose an ordered object walk with raw bytes,
// and json.Decoder.Token re-escapes nothing but also hands back no
// offsets for keys. So the field walk (jsonObjectFields) is a small
// hand scanner — but it only ever runs on bytes json.Valid has already
// accepted, which is what lets it stay a handful of lines: it never has
// to decide whether something is JSON, only where the pieces of known-
// good JSON start and end. The one place it meets unvalidated bytes is
// the stream splitter, which re-validates every piece it cuts and
// refuses a cut that makes no progress.

package format

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
)

// JSONShape names how the records sit in the text (see the file comment).
type JSONShape int

const (
	// JSONLines is one record per line; lines that are not a whole JSON
	// object are kept verbatim.
	JSONLines JSONShape = iota
	// JSONStream is several JSON values back to back, at least one of
	// them spanning lines.
	JSONStream
	// JSONArray is one top-level array; its object elements are records.
	JSONArray
	// JSONObject is one top-level object — a single record.
	JSONObject
)

// String names the shape for the dialog's summary line.
func (s JSONShape) String() string {
	switch s {
	case JSONLines:
		return "JSON lines"
	case JSONStream:
		return "JSON values"
	case JSONArray:
		return "array"
	case JSONObject:
		return "object"
	}
	return "JSON"
}

// JSONKey is one entry in the pick-list: a top-level key name and how
// many records carry it. Count is what makes a heterogeneous log
// readable at a glance — "Start Time 1" in a list of "2"s is the key
// only one kind of line has.
type JSONKey struct {
	Name  string
	Count int
}

// JSONRecords is a parsed text, ready to be projected any number of
// times. It holds sub-slices of the source, so the source must not be
// mutated while it is in use (the dialog owns its copy).
type JSONRecords struct {
	Shape JSONShape
	// Keys is the union of every record's top-level keys, in the order
	// they were first seen — for a log, the order of its first line.
	Keys []JSONKey
	// Records counts the objects; Others counts non-blank items that are
	// not objects (a log's stray lines, an array's scalars), which are
	// kept as they are.
	Records int
	Others  int
	// Pretty is the source's own layout: true when a whole-document
	// shape spans lines. The dialog starts with this, so reformatting
	// with every key ticked changes as little as possible.
	Pretty bool

	// unit is one level of indentation for pretty output — the source's
	// own when it has one (indent.go), else jsonIndent.
	unit string
	// lead and trail are the whitespace around a whole-document shape,
	// re-emitted untouched so a file keeps its final newline.
	lead, trail []byte
	items       []jsonItem
}

// jsonItem is one record or one passenger, in source order.
type jsonItem struct {
	// raw is the item as written: for JSONLines the whole line (so a
	// passenger comes back byte for byte), otherwise the value's bytes.
	raw []byte
	// obj marks a record; fields are its top-level members in order.
	obj    bool
	fields []jsonField
}

// jsonField is one top-level member of a record.
type jsonField struct {
	name string // decoded, for matching against the pick-list
	key  []byte // the key as written, quotes included
	val  []byte // the value as written
}

// jsonSpace is JSON's own whitespace. bytes.TrimSpace is NOT used on
// JSON text anywhere in this file: it also strips Unicode spaces such as
// U+00A0, which JSON rejects, and trimming one would make text that
// json.Valid refuses look like text it accepts.
const jsonSpace = " \t\r\n"

// ParseJSONRecords reads src as JSON records. The error explains, in
// words fit for a flash, why there is nothing to pick keys from: empty
// text, a document that does not parse (with the line and column of the
// mistake, via the validator's explainer), or JSON that holds no object.
func ParseJSONRecords(src []byte) (*JSONRecords, error) {
	trimmed := bytes.Trim(src, jsonSpace)
	if len(trimmed) == 0 {
		return nil, errors.New("there is no JSON here — the text is empty")
	}
	r := &JSONRecords{unit: DetectIndent(src).Unit(jsonIndent)}
	lead := len(src) - len(bytes.TrimLeft(src, jsonSpace))
	r.lead, r.trail = src[:lead], src[lead+len(trimmed):]

	// 1. One whole document. Its lines are fragments, so this must be
	//    decided before anything reads the text line by line.
	if json.Valid(trimmed) {
		r.Pretty = bytes.IndexByte(trimmed, '\n') >= 0
		switch trimmed[0] {
		case '{':
			r.Shape = JSONObject
			r.items = []jsonItem{objectItem(trimmed)}
		case '[':
			r.Shape = JSONArray
			for _, e := range jsonArrayElems(trimmed) {
				r.items = append(r.items, valueItem(e))
			}
		default:
			return nil, fmt.Errorf("this JSON is a single %s — there are no keys to pick", jsonScalarKind(trimmed[0]))
		}
		r.tally()
		if r.Records == 0 {
			return nil, errors.New("this array holds no objects — there are no keys to pick")
		}
		return r, nil
	}

	// 2. Line by line. When every non-blank line is a whole object this
	//    is plain NDJSON and the answer; otherwise it is kept as the
	//    fallback while a stream is tried.
	lines, objLines, nonBlank := jsonLineItems(src)

	// 3. A stream of values, at least one spanning lines — pretty-printed
	//    objects one after another. Only worth trying when lines mode
	//    left something unexplained.
	if objLines < nonBlank {
		if vals, ok := jsonStreamValues(trimmed); ok && len(vals) > 1 {
			r.Shape = JSONStream
			for _, v := range vals {
				r.items = append(r.items, valueItem(v))
				// Usually multi-line (why lines mode fell short), but two
				// objects sharing ONE line also land here and should stay
				// compact.
				r.Pretty = r.Pretty || bytes.IndexByte(v, '\n') >= 0
			}
			r.tally()
			if r.Records > 0 {
				return r, nil
			}
			return nil, errors.New("these JSON values hold no objects — there are no keys to pick")
		}
	}

	// 4. Lines with passengers, if any line is a record at all.
	if objLines == 0 {
		return nil, noJSONRecordsError(src, trimmed)
	}
	r.Shape = JSONLines
	r.lead, r.trail = nil, nil // a line keeps its own whitespace
	r.items = lines
	r.tally()
	return r, nil
}

// jsonLineItems splits src into one item per line: a line that is a
// whole JSON object (after trimming JSON whitespace) is a record, any
// other line a passenger kept byte for byte. It also counts the object
// lines and the non-blank lines, which decide whether this reading
// explains the whole text.
func jsonLineItems(src []byte) (items []jsonItem, objLines, nonBlank int) {
	for line := range bytes.SplitSeq(src, []byte{'\n'}) {
		t := bytes.Trim(line, jsonSpace)
		if len(t) == 0 {
			items = append(items, jsonItem{raw: line})
			continue
		}
		nonBlank++
		if t[0] == '{' && json.Valid(t) {
			objLines++
			it := objectItem(t)
			// A record line's raw is the LINE: nothing reads it back
			// today, but a record and a passenger then hold the same
			// thing, which keeps the item one shape for both.
			it.raw = line
			items = append(items, it)
			continue
		}
		items = append(items, jsonItem{raw: line})
	}
	return items, objLines, nonBlank
}

// noJSONRecordsError explains a text no shape could read. Text that
// starts like a document gets the validator's own explanation — the line
// and column of the mistake, moved onto the character to fix
// (jsonexplain.go) — since "not JSON" alone would send the user hunting.
func noJSONRecordsError(src, trimmed []byte) error {
	if trimmed[0] == '{' || trimmed[0] == '[' {
		if probs := validateJSON(src); len(probs) > 0 {
			p := probs[0]
			return fmt.Errorf("not valid JSON — line %d, col %d: %s", p.Line+1, p.Col+1, p.Message)
		}
	}
	return errors.New("no JSON objects here — the text is not a JSON document, and no line holds a whole JSON object")
}

// jsonScalarKind names a top-level scalar by its first byte, for the
// "nothing to pick" error.
func jsonScalarKind(b byte) string {
	switch b {
	case '"':
		return "string"
	case 't', 'f':
		return "boolean"
	case 'n':
		return "null"
	}
	return "number"
}

// objectItem builds a record from bytes json.Valid accepted as an object.
func objectItem(obj []byte) jsonItem {
	return jsonItem{raw: obj, obj: true, fields: jsonObjectFields(obj)}
}

// valueItem builds an item from one valid JSON value: a record when it
// is an object, a passenger otherwise.
func valueItem(v []byte) jsonItem {
	if len(v) > 0 && v[0] == '{' {
		return objectItem(v)
	}
	return jsonItem{raw: v}
}

// tally counts records and passengers and builds the key list: first-
// seen order, each key counted once per record however often a record
// repeats it (duplicate keys are legal JSON, if unwise).
func (r *JSONRecords) tally() {
	index := map[string]int{}
	// lastRec[k] is the record number that last counted key k — a
	// per-record "seen" set without allocating a map per record, which on
	// a 100k-line log is the difference between a pause and none.
	var lastRec []int
	for _, it := range r.items {
		if !it.obj {
			if len(bytes.Trim(it.raw, jsonSpace)) > 0 {
				r.Others++
			}
			continue
		}
		r.Records++
		for _, f := range it.fields {
			k, ok := index[f.name]
			if !ok {
				k = len(r.Keys)
				index[f.name] = k
				r.Keys = append(r.Keys, JSONKey{Name: f.name})
				lastRec = append(lastRec, 0)
			}
			if lastRec[k] != r.Records {
				lastRec[k] = r.Records
				r.Keys[k].Count++
			}
		}
	}
}

// Project re-emits the records keeping only the keys keep marks true,
// in the shape they came in. pretty indents each record (an array or a
// single object as one indented document) with the source's unit;
// otherwise records are compact. skipEmpty drops a record that HAD keys
// but kept none — on a log, the lines that lack every ticked key — and
// never drops a record that was `{}` to begin with, nor the lone record
// of a JSONObject. kept is how many records were written.
//
// Passengers survive: a JSONLines passenger is written back byte for
// byte; a stream's or an array's non-object value is compacted (or
// indented with the rest) — it is JSON, and the document's layout is
// being chosen as a whole.
func (r *JSONRecords) Project(keep map[string]bool, pretty, skipEmpty bool) (out []byte, kept int) {
	var b, rec bytes.Buffer
	// record projects one item into rec (compact) and reports whether it
	// should be written at all.
	record := func(it jsonItem) bool {
		rec.Reset()
		n := projectFields(&rec, it.fields, keep)
		if skipEmpty && n == 0 && len(it.fields) > 0 && r.Shape != JSONObject {
			return false
		}
		kept++
		return true
	}
	switch r.Shape {
	case JSONLines:
		b.Grow(len(r.items) * 64)
		first := true
		for _, it := range r.items {
			if it.obj && !record(it) {
				continue
			}
			if !first {
				b.WriteByte('\n')
			}
			first = false
			if !it.obj {
				b.Write(it.raw)
				continue
			}
			writeLayout(&b, rec.Bytes(), pretty, r.unit)
		}
	case JSONStream:
		b.Write(r.lead)
		first := true
		for _, it := range r.items {
			if it.obj && !record(it) {
				continue
			}
			if !first {
				b.WriteByte('\n')
			}
			first = false
			if it.obj {
				writeLayout(&b, rec.Bytes(), pretty, r.unit)
			} else {
				writeLayout(&b, it.raw, pretty, r.unit)
			}
		}
		b.Write(r.trail)
	case JSONArray:
		// Built compact, then indented as ONE document, so the array's
		// brackets and commas are laid out by json.Indent like every
		// other level rather than by hand here.
		var arr bytes.Buffer
		arr.WriteByte('[')
		n := 0
		for _, it := range r.items {
			if it.obj && !record(it) {
				continue
			}
			if n > 0 {
				arr.WriteByte(',')
			}
			n++
			if it.obj {
				arr.Write(rec.Bytes())
			} else {
				compactInto(&arr, it.raw)
			}
		}
		arr.WriteByte(']')
		b.Write(r.lead)
		writeLayout(&b, arr.Bytes(), pretty, r.unit)
		b.Write(r.trail)
	case JSONObject:
		record(r.items[0])
		b.Write(r.lead)
		writeLayout(&b, rec.Bytes(), pretty, r.unit)
		b.Write(r.trail)
	}
	return b.Bytes(), kept
}

// projectFields writes `{` + the kept fields + `}` compactly into buf and
// returns how many fields it kept. Each field is its original key bytes,
// a colon, and its original value compacted — see the file comment.
func projectFields(buf *bytes.Buffer, fields []jsonField, keep map[string]bool) int {
	buf.WriteByte('{')
	n := 0
	for _, f := range fields {
		if !keep[f.name] {
			continue
		}
		if n > 0 {
			buf.WriteByte(',')
		}
		n++
		buf.Write(f.key)
		buf.WriteByte(':')
		compactInto(buf, f.val)
	}
	buf.WriteByte('}')
	return n
}

// compactInto appends v compacted. v was validated, so Compact cannot
// fail; if it ever did, the original bytes go out rather than nothing —
// a layout miss is recoverable, a dropped value is not.
func compactInto(buf *bytes.Buffer, v []byte) {
	if err := json.Compact(buf, v); err != nil {
		buf.Write(v)
	}
}

// writeLayout appends compact JSON v, indented with unit when pretty.
// Same fallback as compactInto.
func writeLayout(buf *bytes.Buffer, v []byte, pretty bool, unit string) {
	if !pretty {
		buf.Write(v)
		return
	}
	if err := json.Indent(buf, v, "", unit); err != nil {
		buf.Write(v)
	}
}

// -----------------------------------------------------------------------------
// The scanner. Everything below runs on bytes json.Valid accepted, except
// jsonStreamValues, which validates each piece it cuts.
// -----------------------------------------------------------------------------

// isJSONSpace reports whether c is JSON whitespace.
func isJSONSpace(c byte) bool {
	return c == ' ' || c == '\t' || c == '\r' || c == '\n'
}

// skipJSONSpace returns the first index at or after i that is not JSON
// whitespace.
func skipJSONSpace(b []byte, i int) int {
	for i < len(b) && isJSONSpace(b[i]) {
		i++
	}
	return i
}

// skipJSONString returns the index just past the string whose opening
// quote is at b[i]. A backslash always escapes exactly the next byte as
// far as FINDING the end is concerned (\uXXXX's hex digits are never a
// quote), so the scan needs no escape table.
func skipJSONString(b []byte, i int) int {
	for i++; i < len(b); i++ {
		switch b[i] {
		case '\\':
			i++
		case '"':
			return i + 1
		}
	}
	return len(b)
}

// skipJSONValue returns the index just past the value starting at b[i]:
// a string, a bracketed container (depth-counted, strings skipped so a
// "}" inside one is text), or a literal / number running to the next
// delimiter.
func skipJSONValue(b []byte, i int) int {
	if i >= len(b) {
		return i
	}
	switch b[i] {
	case '"':
		return skipJSONString(b, i)
	case '{', '[':
		depth := 0
		for i < len(b) {
			switch b[i] {
			case '"':
				i = skipJSONString(b, i)
				continue
			case '{', '[':
				depth++
			case '}', ']':
				depth--
				if depth == 0 {
					return i + 1
				}
			}
			i++
		}
		return len(b)
	}
	for i < len(b) && !isJSONSpace(b[i]) && b[i] != ',' && b[i] != '}' && b[i] != ']' && b[i] != '"' {
		i++
	}
	return i
}

// jsonObjectFields walks a VALID object's top-level members in order.
// The bounds checks are belt and braces: on validated input none of
// them can trip, and if one ever did the walk would end early rather
// than index out of range.
func jsonObjectFields(obj []byte) []jsonField {
	var out []jsonField
	i := skipJSONSpace(obj, 0) + 1 // past '{'
	for {
		i = skipJSONSpace(obj, i)
		if i >= len(obj) || obj[i] != '"' {
			return out // '}' — the end of the members
		}
		ks := i
		i = skipJSONString(obj, i)
		key := obj[ks:i]
		i = skipJSONSpace(obj, i)
		if i >= len(obj) || obj[i] != ':' {
			return out
		}
		vs := skipJSONSpace(obj, i+1)
		i = skipJSONValue(obj, vs)
		if i <= vs {
			return out
		}
		out = append(out, jsonField{name: jsonKeyName(key), key: key, val: obj[vs:i]})
		i = skipJSONSpace(obj, i)
		if i < len(obj) && obj[i] == ',' {
			i++
			continue
		}
		return out
	}
}

// jsonArrayElems walks a VALID array's elements in order, returning each
// as a sub-slice (no copies — a large saved API response is not doubled
// in memory to be read).
func jsonArrayElems(arr []byte) [][]byte {
	var out [][]byte
	i := skipJSONSpace(arr, 0) + 1 // past '['
	for {
		i = skipJSONSpace(arr, i)
		if i >= len(arr) || arr[i] == ']' {
			return out
		}
		vs := i
		i = skipJSONValue(arr, vs)
		if i <= vs {
			return out
		}
		out = append(out, arr[vs:i])
		i = skipJSONSpace(arr, i)
		if i < len(arr) && arr[i] == ',' {
			i++
			continue
		}
		return out
	}
}

// jsonStreamValues cuts b into whole JSON values separated by optional
// whitespace, validating each cut. ok is false the moment a piece is not
// valid JSON or a cut makes no progress (a stray closer), so text that
// is not a clean stream is left to lines mode.
func jsonStreamValues(b []byte) (vals [][]byte, ok bool) {
	i := 0
	for {
		i = skipJSONSpace(b, i)
		if i >= len(b) {
			return vals, true
		}
		end := skipJSONValue(b, i)
		if end <= i || !json.Valid(b[i:end]) {
			return nil, false
		}
		vals = append(vals, b[i:end])
		i = end
	}
}

// jsonKeyName decodes a key as written (quotes included) into its name.
// The common key has no escape, and is just the bytes between the
// quotes — no decoder, no allocation beyond the string itself.
func jsonKeyName(raw []byte) string {
	if len(raw) < 2 {
		return string(raw)
	}
	if bytes.IndexByte(raw, '\\') < 0 {
		return string(raw[1 : len(raw)-1])
	}
	var s string
	if err := json.Unmarshal(raw, &s); err != nil {
		return string(raw[1 : len(raw)-1])
	}
	return s
}
