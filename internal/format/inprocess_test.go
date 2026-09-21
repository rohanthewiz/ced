// =============================================================================
// File: internal/format/inprocess_test.go
// Author: Rohan Allison <rohanthewiz@gmail.com>
// Created: 2026-09-21
// Copyright: 2026 Rohan Allison. All rights reserved.
// =============================================================================

package format

import (
	"strings"
	"testing"
)

// TestInProcessFormat_IndentsJSON pins the basic pass: a minified
// document comes back laid out two spaces per level, with a trailing
// newline so the file plays nicely with every POSIX text tool and
// doesn't show up in git as "\ No newline at end of file".
func TestInProcessFormat_IndentsJSON(t *testing.T) {
	got, ok, err := InProcessFormat("/proj/data.json", []byte(`{"a":1,"b":[2,3]}`))
	if err != nil {
		t.Fatalf("InProcessFormat: %v", err)
	}
	if !ok {
		t.Fatal("ok = false, want true for a .json file")
	}
	want := "{\n  \"a\": 1,\n  \"b\": [\n    2,\n    3\n  ]\n}\n"
	if string(got) != want {
		t.Errorf("got:\n%q\nwant:\n%q", got, want)
	}
}

// TestInProcessFormat_IsIdempotent is the property that decides whether
// a formatter can be trusted to run on every save. Formatting twice
// must equal formatting once — otherwise each save adds another level
// of indentation and the tool is fighting the file.
func TestInProcessFormat_IsIdempotent(t *testing.T) {
	src := []byte("{\"a\":1,\"b\":{\"c\":[1,2]}}")
	once, _, err := InProcessFormat("/proj/data.json", src)
	if err != nil {
		t.Fatalf("first pass: %v", err)
	}
	twice, _, err := InProcessFormat("/proj/data.json", once)
	if err != nil {
		t.Fatalf("second pass: %v", err)
	}
	if string(once) != string(twice) {
		t.Errorf("not idempotent:\nonce:  %q\ntwice: %q", once, twice)
	}
}

// TestInProcessFormat_PreservesKeyOrder is the whole reason this uses
// json.Indent rather than a Marshal round trip. Go randomises map
// iteration, so a Marshal-based formatter would reorder every object
// and two saves of one file could produce two different orderings —
// turning a no-op save into a whole-file diff.
func TestInProcessFormat_PreservesKeyOrder(t *testing.T) {
	src := []byte(`{"zebra":1,"apple":2,"mango":3,"beta":4}`)
	got, _, err := InProcessFormat("/proj/data.json", src)
	if err != nil {
		t.Fatalf("InProcessFormat: %v", err)
	}
	zebra := strings.Index(string(got), "zebra")
	apple := strings.Index(string(got), "apple")
	mango := strings.Index(string(got), "mango")
	beta := strings.Index(string(got), "beta")
	if !(zebra < apple && apple < mango && mango < beta) {
		t.Errorf("key order changed; got:\n%s", got)
	}
}

// TestInProcessFormat_PreservesNumbersAndEscapes pins the other half of
// the json.Indent choice. A Marshal round trip pushes every number
// through a float64, which rewrites 1e3 as 1000 and can lose precision
// on large integers; it also re-escapes strings to its own taste. A
// whitespace-only pass changes the layout and nothing else.
func TestInProcessFormat_PreservesNumbersAndEscapes(t *testing.T) {
	// Built with an explicit \u escape sequence in the JSON TEXT (six
	// characters: backslash, u, 0, 0, e, 9) rather than a literal é, so
	// the test really does pin escape preservation. A Marshal round trip
	// would decode it to é and re-emit the decoded form.
	const uEscape = `é`
	src := []byte(`{"big":123456789012345678901234567890,"exp":1e3,` +
		`"esc":"a` + uEscape + `b\/c","lit":"café é","neg":-0.0}`)
	got, _, err := InProcessFormat("/proj/data.json", src)
	if err != nil {
		t.Fatalf("InProcessFormat: %v", err)
	}
	for _, token := range []string{
		"123456789012345678901234567890", // not rounded through a float
		"1e3",                            // not expanded to 1000
		`a` + uEscape + `b\/c`,           // \u and \/ left exactly as written
		"é",                              // and a literal non-ASCII rune survives too
		"-0.0",                           // not normalised to 0
	} {
		if !strings.Contains(string(got), token) {
			t.Errorf("token %q did not survive formatting; got:\n%s", token, got)
		}
	}
}

// TestInProcessFormat_EmptyFileIsLeftAlone mirrors the validator's
// mid-thought rule. A file someone just created must not produce a
// syntax complaint on save, and the two halves have to agree about it
// or ced would refuse to format a file it also refuses to call broken.
func TestInProcessFormat_EmptyFileIsLeftAlone(t *testing.T) {
	for _, src := range []string{"", "   ", "\n"} {
		got, ok, err := InProcessFormat("/proj/data.json", []byte(src))
		if err != nil {
			t.Errorf("InProcessFormat(%q) errored: %v", src, err)
		}
		if !ok {
			t.Errorf("InProcessFormat(%q) ok = false, want true", src)
		}
		if string(got) != src {
			t.Errorf("InProcessFormat(%q) = %q, want it unchanged", src, got)
		}
	}
}

// TestInProcessFormat_RefusesBrokenJSON pins the contract the caller
// depends on: a file that does not parse comes back as an error and
// NOTHING is returned to write. Rewriting unparseable text is how a
// formatter destroys work, and the validator has already underlined
// the reason.
func TestInProcessFormat_RefusesBrokenJSON(t *testing.T) {
	got, ok, err := InProcessFormat("/proj/data.json", []byte(`{"a": 1,}`))
	if err == nil {
		t.Fatal("err = nil, want a parse error for a trailing comma")
	}
	if !ok {
		t.Error("ok = false; the file IS one ced formats, it just doesn't parse")
	}
	if got != nil {
		t.Errorf("got = %q, want nil so the caller writes nothing", got)
	}
}

// TestInProcessFormat_DeclinesOtherFiles pins the ok=false branch —
// "not my job", which the caller must not confuse with a failure.
func TestInProcessFormat_DeclinesOtherFiles(t *testing.T) {
	for _, p := range []string{
		"/proj/main.go", // Go has no pure-Go rung and shouldn't grow one
		"/proj/notes.txt",
		"/proj/tsconfig.json",
		"/proj/.vscode/settings.json",
	} {
		got, ok, err := InProcessFormat(p, []byte(`{"a":1}`))
		if ok || got != nil || err != nil {
			t.Errorf("InProcessFormat(%q) = (%q, %v, %v), want (nil, false, nil)", p, got, ok, err)
		}
	}
}

// TestInProcessFormat_DoesNotAliasItsInput pins that the result is a
// fresh allocation. Callers hold the buffer's bytes and the formatted
// bytes at the same time; a window onto the input would let a later
// write through one of them corrupt the other.
func TestInProcessFormat_DoesNotAliasItsInput(t *testing.T) {
	src := []byte(`{"a":1}`)
	got, _, err := InProcessFormat("/proj/data.json", src)
	if err != nil {
		t.Fatalf("InProcessFormat: %v", err)
	}
	before := string(got)
	for i := range src {
		src[i] = 'X'
	}
	if string(got) != before {
		t.Error("formatted output changed when the input was overwritten; it aliases src")
	}
}
