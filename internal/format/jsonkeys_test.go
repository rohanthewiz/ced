// =============================================================================
// File: internal/format/jsonkeys_test.go
// Author: Rohan Allison <rohanthewiz@gmail.com>
// Created: 2026-11-23
// Copyright: 2026 Rohan Allison. All rights reserved.
// =============================================================================

package format

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

// benchLogLine is a SYNTHETIC structured-log record in the shape the
// feature was built for (a logrus-style JSON formatter: sorted keys,
// fifteen-odd of them, three worth reading). Never a real log.
const benchLogLine = `{"app":"orders","file":"/src/orders/writer.go","func":"orders.(*Writer).flush","level":"info","line":42,"msg":"flushed 500 rows to orders.line_items","pid":4242,"time":"2026-01-02T03:04:05Z","ts":"2026-01-02T03:04:05.123456789Z"}`

// keepSet builds a pick-list selection from names.
func keepSet(names ...string) map[string]bool {
	m := map[string]bool{}
	for _, n := range names {
		m[n] = true
	}
	return m
}

// allKeys ticks every key the parse found.
func allKeys(r *JSONRecords) map[string]bool {
	m := map[string]bool{}
	for _, k := range r.Keys {
		m[k.Name] = true
	}
	return m
}

// mustParse parses src or fails the test.
func mustParse(t *testing.T, src string) *JSONRecords {
	t.Helper()
	r, err := ParseJSONRecords([]byte(src))
	if err != nil {
		t.Fatalf("ParseJSONRecords(%q): %v", src, err)
	}
	return r
}

// TestParseJSONRecords_LinesKeysFirstSeenWithCounts: a JSON-lines log is
// read one record per line, and the key list is the union of every
// line's keys in first-seen order, each counted once per record that
// carries it — the count is what exposes a key only some lines have.
func TestParseJSONRecords_LinesKeysFirstSeenWithCounts(t *testing.T) {
	src := `{"level":"info","msg":"a","time":"t1"}
{"start":"s","level":"warn","msg":"b","time":"t2"}
`
	r := mustParse(t, src)
	if r.Shape != JSONLines {
		t.Fatalf("shape = %v, want JSON lines", r.Shape)
	}
	if r.Records != 2 || r.Others != 0 {
		t.Fatalf("records/others = %d/%d, want 2/0", r.Records, r.Others)
	}
	want := []JSONKey{{"level", 2}, {"msg", 2}, {"time", 2}, {"start", 1}}
	if len(r.Keys) != len(want) {
		t.Fatalf("keys = %v, want %v", r.Keys, want)
	}
	for i, k := range want {
		if r.Keys[i] != k {
			t.Errorf("keys[%d] = %v, want %v", i, r.Keys[i], k)
		}
	}
	if r.Pretty {
		t.Error("a JSON-lines log should start compact")
	}
}

// TestParseJSONRecords_DuplicateKeyCountsOncePerRecord: JSON allows a
// key twice in one object; the count is RECORDS carrying it, so a
// record repeating a key still counts once.
func TestParseJSONRecords_DuplicateKeyCountsOncePerRecord(t *testing.T) {
	r := mustParse(t, "{\"a\":1,\"a\":2}\n{\"a\":3}\n")
	if len(r.Keys) != 1 || r.Keys[0].Count != 2 {
		t.Fatalf("keys = %v, want [{a 2}]", r.Keys)
	}
	out, _ := r.Project(keepSet("a"), false, false)
	if string(out) != "{\"a\":1,\"a\":2}\n{\"a\":3}\n" {
		t.Fatalf("both occurrences should be kept, got %q", out)
	}
}

// TestProject_KeepsOnlyTickedKeysInRecordOrder is the feature: each
// record keeps exactly the ticked keys, in its own order, values
// untouched; a record lacking a ticked key simply goes without.
func TestProject_KeepsOnlyTickedKeysInRecordOrder(t *testing.T) {
	src := `{"time":"t1","file":"x.go","level":"info","msg":"hello"}
{"level":"error","msg":"boom","file":"y.go"}
`
	r := mustParse(t, src)
	out, kept := r.Project(keepSet("time", "msg"), false, false)
	want := `{"time":"t1","msg":"hello"}
{"msg":"boom"}
`
	if string(out) != want {
		t.Fatalf("out =\n%s\nwant\n%s", out, want)
	}
	if kept != 2 {
		t.Fatalf("kept = %d, want 2", kept)
	}
}

// TestProject_AllKeysIsCompact pins the file comment's invariant:
// keeping every key, unpretty, is exactly json.Compact of each record —
// projection changes whitespace and drops fields, nothing else (number
// spellings, escapes and nested key order included).
func TestProject_AllKeysIsCompact(t *testing.T) {
	src := `{ "n": 1e3, "big": 12345678901234567890, "s": "a<b\"c", "nested": {"z": 1, "a": [1, 2, {"y": null}]} }`
	r := mustParse(t, src)
	out, _ := r.Project(allKeys(r), false, false)
	var want bytes.Buffer
	if err := json.Compact(&want, []byte(src)); err != nil {
		t.Fatal(err)
	}
	if string(out) != want.String() {
		t.Fatalf("out  = %s\nwant = %s", out, want.String())
	}
}

// TestProject_DropsEscapedKeyByItsName: a key written with an escape is
// matched by its decoded NAME, and a kept one is written back with its
// original escape.
func TestProject_DropsEscapedKeyByItsName(t *testing.T) {
	r := mustParse(t, `{"ab":1,"c":2}`)
	if r.Keys[0].Name != "ab" {
		t.Fatalf("decoded name = %q, want ab", r.Keys[0].Name)
	}
	out, _ := r.Project(keepSet("ab"), false, false)
	if string(out) != `{"ab":1}` {
		t.Fatalf("out = %s", out)
	}
}

// TestParseJSONRecords_LinesKeepPassengers: a log line that is not a
// whole JSON object (a stack trace, a banner) and blank lines are
// passengers, written back byte for byte, and counted (blank ones not).
func TestParseJSONRecords_LinesKeepPassengers(t *testing.T) {
	src := "{\"a\":1,\"b\":2}\npanic: boom\n\tgoroutine 1 [running]:\n\n{\"a\":3,\"b\":4}"
	r := mustParse(t, src)
	if r.Shape != JSONLines || r.Records != 2 || r.Others != 2 {
		t.Fatalf("shape %v records %d others %d, want lines/2/2", r.Shape, r.Records, r.Others)
	}
	out, _ := r.Project(keepSet("b"), false, false)
	want := "{\"b\":2}\npanic: boom\n\tgoroutine 1 [running]:\n\n{\"b\":4}"
	if string(out) != want {
		t.Fatalf("out = %q\nwant  %q", out, want)
	}
}

// TestProject_SkipEmptyDropsOnlyRecordsThatLostEverything: "hide
// records left empty" drops a record that had keys and kept none, never
// one that was {} to begin with, and never a passenger.
func TestProject_SkipEmptyDropsOnlyRecordsThatLostEverything(t *testing.T) {
	src := "{\"err\":\"x\"}\n{\"msg\":\"y\"}\n{}\nnot json\n"
	r := mustParse(t, src)
	out, kept := r.Project(keepSet("err"), false, true)
	want := "{\"err\":\"x\"}\n{}\nnot json\n"
	if string(out) != want {
		t.Fatalf("out = %q, want %q", out, want)
	}
	if kept != 2 {
		t.Fatalf("kept = %d, want 2", kept)
	}
	// Off: the emptied record stays as {}.
	out, kept = r.Project(keepSet("err"), false, false)
	if string(out) != "{\"err\":\"x\"}\n{}\n{}\nnot json\n" || kept != 3 {
		t.Fatalf("skip off: out = %q kept %d", out, kept)
	}
}

// TestProject_PrettyLinesIndentsEachRecord: pretty on a JSON-lines text
// indents every record on its own; the default unit is two spaces.
func TestProject_PrettyLinesIndentsEachRecord(t *testing.T) {
	r := mustParse(t, "{\"a\":1,\"b\":[1,2]}\n{\"a\":2}\n")
	out, _ := r.Project(keepSet("a", "b"), true, false)
	want := "{\n  \"a\": 1,\n  \"b\": [\n    1,\n    2\n  ]\n}\n{\n  \"a\": 2\n}\n"
	if string(out) != want {
		t.Fatalf("out =\n%s\nwant\n%s", out, want)
	}
}

// TestParseJSONRecords_ArrayRoundTripsItsOwnLayout: a pretty-printed
// array (four-space, as written) reformatted with every key ticked
// comes back byte for byte — the dialog opening on a file and applying
// with nothing changed must not churn it — and dropping a key keeps the
// layout and the trailing newline.
func TestParseJSONRecords_ArrayRoundTripsItsOwnLayout(t *testing.T) {
	src := "[\n    {\n        \"id\": 1,\n        \"name\": \"a\"\n    },\n    {\n        \"id\": 2,\n        \"name\": \"b\"\n    }\n]\n"
	r := mustParse(t, src)
	if r.Shape != JSONArray || !r.Pretty || r.Records != 2 {
		t.Fatalf("shape %v pretty %v records %d", r.Shape, r.Pretty, r.Records)
	}
	out, _ := r.Project(allKeys(r), r.Pretty, false)
	if string(out) != src {
		t.Fatalf("all keys should round-trip:\n%s", out)
	}
	out, _ = r.Project(keepSet("name"), true, false)
	want := "[\n    {\n        \"name\": \"a\"\n    },\n    {\n        \"name\": \"b\"\n    }\n]\n"
	if string(out) != want {
		t.Fatalf("out =\n%s\nwant\n%s", out, want)
	}
}

// TestProject_ArrayKeepsScalarElementsAndSkipsEmpties: an array's non-
// object elements are passengers that stay in place; skipEmpty drops
// only emptied objects.
func TestProject_ArrayKeepsScalarElementsAndSkipsEmpties(t *testing.T) {
	r := mustParse(t, `[{"a":1},2,{"b":3}]`)
	if r.Records != 2 || r.Others != 1 {
		t.Fatalf("records/others = %d/%d", r.Records, r.Others)
	}
	out, _ := r.Project(keepSet("a"), false, true)
	if string(out) != `[{"a":1},2]` {
		t.Fatalf("out = %s", out)
	}
}

// TestParseJSONRecords_SingleObjectKeepsSurroundingWhitespace: one
// object is one record; its surrounding whitespace (the final newline)
// survives, and skipEmpty never deletes the only record.
func TestParseJSONRecords_SingleObjectKeepsSurroundingWhitespace(t *testing.T) {
	r := mustParse(t, "{\n  \"a\": 1,\n  \"b\": 2\n}\n")
	if r.Shape != JSONObject {
		t.Fatalf("shape = %v", r.Shape)
	}
	out, kept := r.Project(keepSet("b"), true, true)
	if string(out) != "{\n  \"b\": 2\n}\n" || kept != 1 {
		t.Fatalf("out = %q kept %d", out, kept)
	}
	out, _ = r.Project(keepSet(), true, true)
	if string(out) != "{}\n" {
		t.Fatalf("nothing kept = %q, want {}", out)
	}
}

// TestParseJSONRecords_EmptyObjectIsARecord: `{}` is one record with
// NO keys — accepted (it is an object), so every consumer of Keys must
// cope with an empty list. Found by FuzzParseJSONRecords.
func TestParseJSONRecords_EmptyObjectIsARecord(t *testing.T) {
	r := mustParse(t, "{}")
	if r.Shape != JSONObject || r.Records != 1 || len(r.Keys) != 0 {
		t.Fatalf("shape %v records %d keys %v", r.Shape, r.Records, r.Keys)
	}
	if out, kept := r.Project(keepSet(), false, true); string(out) != "{}" || kept != 1 {
		t.Fatalf("out %q kept %d", out, kept)
	}
}

// TestParseJSONRecords_StreamOfPrettyObjects: pretty-printed objects one
// after another are a stream, not lines — its lines are fragments.
func TestParseJSONRecords_StreamOfPrettyObjects(t *testing.T) {
	src := "{\n  \"a\": 1,\n  \"b\": 2\n}\n{\n  \"a\": 3,\n  \"b\": 4\n}\n"
	r := mustParse(t, src)
	if r.Shape != JSONStream || r.Records != 2 || !r.Pretty {
		t.Fatalf("shape %v records %d pretty %v", r.Shape, r.Records, r.Pretty)
	}
	out, _ := r.Project(keepSet("b"), false, false)
	if string(out) != "{\"b\":2}\n{\"b\":4}\n" {
		t.Fatalf("out = %q", out)
	}
}

// TestParseJSONRecords_TwoObjectsOnOneLineStayCompact: a stream whose
// values share one line starts compact.
func TestParseJSONRecords_TwoObjectsOnOneLineStayCompact(t *testing.T) {
	r := mustParse(t, `{"a":1} {"a":2}`)
	if r.Shape != JSONStream || r.Pretty {
		t.Fatalf("shape %v pretty %v, want a compact stream", r.Shape, r.Pretty)
	}
}

// TestParseJSONRecords_ExplainsWhyThereIsNothingToPick: every refusal
// says why in words — empty, a scalar, an array of scalars, a broken
// document (with the validator's line and column), plain text.
func TestParseJSONRecords_ExplainsWhyThereIsNothingToPick(t *testing.T) {
	cases := []struct{ src, want string }{
		{"  \n", "empty"},
		{"42", "single number"},
		{`"s"`, "single string"},
		{"[1,2]", "holds no objects"},
		{"{\n  \"a\": 1,\n}\n", "line 2, col 9"},
		{"hello world\n", "no JSON objects"},
	}
	for _, c := range cases {
		_, err := ParseJSONRecords([]byte(c.src))
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("ParseJSONRecords(%q) err = %v, want it to mention %q", c.src, err, c.want)
		}
	}
}

// TestParseJSONRecords_NonBreakingSpaceIsNotWhitespace: JSON whitespace
// is four bytes; a U+00A0 around a document is not trimmed into
// validity (the reason the file never uses bytes.TrimSpace).
func TestParseJSONRecords_NonBreakingSpaceIsNotWhitespace(t *testing.T) {
	if _, err := ParseJSONRecords([]byte(" {\"a\":1}")); err == nil {
		t.Fatal("a leading U+00A0 should not parse as a JSON object")
	}
}

// TestJSONKeyName_DecodesEscapesOnlyWhenPresent: a plain key is its
// bytes between the quotes; an escaped one is decoded.
func TestJSONKeyName_DecodesEscapesOnlyWhenPresent(t *testing.T) {
	if got := jsonKeyName([]byte(`"plain"`)); got != "plain" {
		t.Errorf("plain = %q", got)
	}
	if got := jsonKeyName([]byte(`"a\"b"`)); got != `a"b` {
		t.Errorf("escaped = %q", got)
	}
}

// TestJSONStreamValues_RefusesAStrayCloser: a cut that makes no progress
// ends the stream reading instead of looping forever.
func TestJSONStreamValues_RefusesAStrayCloser(t *testing.T) {
	if _, ok := jsonStreamValues([]byte(`{"a":1}}`)); ok {
		t.Fatal("a stray closer should not read as a stream")
	}
	vals, ok := jsonStreamValues([]byte(`1 "x" {"a":[1]}`))
	if !ok || len(vals) != 3 {
		t.Fatalf("vals = %q ok %v, want three values", vals, ok)
	}
}

// TestJSONShape_String is a smoke test of the summary names.
func TestJSONShape_String(t *testing.T) {
	for s, want := range map[JSONShape]string{JSONLines: "JSON lines", JSONStream: "JSON values",
		JSONArray: "array", JSONObject: "object", JSONShape(99): "JSON"} {
		if s.String() != want {
			t.Errorf("%d.String() = %q, want %q", s, s.String(), want)
		}
	}
}

// benchLog is n synthetic log lines.
func benchLog(n int) []byte {
	return []byte(strings.Repeat(benchLogLine+"\n", n))
}

// BenchmarkParseJSONRecords_Log measures the open of a 10k-line log —
// paid once when the dialog opens.
func BenchmarkParseJSONRecords_Log(b *testing.B) {
	src := benchLog(10000)
	b.SetBytes(int64(len(src)))
	for b.Loop() {
		if _, err := ParseJSONRecords(src); err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkProject_Log measures one re-projection of a 10k-line log —
// paid on every tick in the dialog.
func BenchmarkProject_Log(b *testing.B) {
	src := benchLog(10000)
	r, err := ParseJSONRecords(src)
	if err != nil {
		b.Fatal(err)
	}
	keep := keepSet("time", "level", "msg")
	b.SetBytes(int64(len(src)))
	for b.Loop() {
		r.Project(keep, false, false)
	}
}

// TestProject_TickStaysInteractiveOnALargeLog is a LOOSE budget beside
// the benchmarks: re-projecting a 100k-line (~26MB) log — about the
// largest file ced opens — stays well under a second, so a tick in the
// dialog never reads as a hang. Loose on purpose (race detector, slow
// CI): it catches an accidental quadratic, not a 20% regression.
func TestProject_TickStaysInteractiveOnALargeLog(t *testing.T) {
	if testing.Short() {
		t.Skip("large-input budget")
	}
	src := benchLog(100000)
	r, err := ParseJSONRecords(src)
	if err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	r.Project(keepSet("time", "level", "msg"), false, false)
	if d := time.Since(start); d > 5*time.Second {
		t.Fatalf("projecting 100k lines took %v", d)
	}
}

// FuzzParseJSONRecords feeds the parser — and the hand scanner behind
// it — arbitrary bytes (N-138). No panic; and when it accepts the text,
// keeping every key unpretty is exactly json.Compact of each record
// (the file comment's invariant), and keeping none still emits valid
// JSON for every record.
func FuzzParseJSONRecords(f *testing.F) {
	f.Add([]byte(benchLogLine + "\n" + benchLogLine + "\n"))
	f.Add([]byte("{\"a\":1}\npanic: x\n{\"b\":\"\\\"}\"}\n"))
	f.Add([]byte(`[{"a":{"b":[1,{"c":"}"}]}},3]`))
	f.Add([]byte("{\n \"a\": 1\n}\n{\n \"a\": 2\n}"))
	f.Add([]byte(`{"a":1e3}`))
	f.Fuzz(func(t *testing.T, src []byte) {
		r, err := ParseJSONRecords(src)
		if err != nil {
			return
		}
		all := allKeys(r)
		for _, it := range r.items {
			if !it.obj {
				continue
			}
			var got, want bytes.Buffer
			projectFields(&got, it.fields, all)
			body := bytes.Trim(it.raw, jsonSpace)
			if err := json.Compact(&want, body); err != nil {
				t.Fatalf("record %q is not valid JSON: %v", body, err)
			}
			if got.String() != want.String() {
				t.Fatalf("all keys = %s, want compact %s", got.String(), want.String())
			}
			var none bytes.Buffer
			projectFields(&none, it.fields, nil)
			if none.String() != "{}" {
				t.Fatalf("no keys = %s, want {}", none.String())
			}
		}
		// A whole-document shape stays ONE valid document whatever is
		// ticked, laid out either way.
		if r.Shape == JSONArray || r.Shape == JSONObject {
			// One key ticked when there is one; `{}` has none (found by
			// this target — see TestParseJSONRecords_EmptyObjectIsARecord).
			some := keepSet()
			if len(r.Keys) > 0 {
				some = keepSet(r.Keys[0].Name)
			}
			for _, pretty := range []bool{false, true} {
				out, _ := r.Project(some, pretty, true)
				if !json.Valid(bytes.Trim(out, jsonSpace)) {
					t.Fatalf("%v output (pretty %v) is not valid JSON: %q", r.Shape, pretty, out)
				}
			}
		}
	})
}
