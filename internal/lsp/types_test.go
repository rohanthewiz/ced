// =============================================================================
// File: internal/lsp/types_test.go
// Author: Rohan Allison <rohanthewiz@gmail.com>
// Created: 2026-07-09
// Copyright: 2026 Rohan Allison. All rights reserved.
// =============================================================================

package lsp

import (
	"encoding/json"
	"testing"
)

// TestPathURIRoundTrip pins the URI conversion both ways, including a
// path with a space — the case that breaks naive "file://" + path
// concatenation and that some servers reject outright.
func TestPathURIRoundTrip(t *testing.T) {
	cases := []string{
		"/Users/ro/projs/go/spice-edit/main.go",
		"/tmp/dir with space/file.go",
		"/a/b/c.go",
	}
	for _, path := range cases {
		uri := PathToURI(path)
		if got := URIToPath(uri); got != path {
			t.Errorf("round trip %q → %q → %q", path, uri, got)
		}
	}
	if uri := PathToURI("/tmp/dir with space/file.go"); uri != "file:///tmp/dir%20with%20space/file.go" {
		t.Errorf("space not escaped: %q", uri)
	}
}

// TestURIToPathRejectsNonFile pins the "" return for schemes the editor
// can't open as plain files, so a definition into a zip-scheme stdlib
// archive degrades to a no-op instead of opening a garbage path.
func TestURIToPathRejectsNonFile(t *testing.T) {
	for _, uri := range []string{"https://example.com/x.go", "zipfile:///a.zip", "::bad::"} {
		if got := URIToPath(uri); got != "" {
			t.Errorf("URIToPath(%q) = %q, want empty", uri, got)
		}
	}
}

// TestUTF16ColBMP pins that plain ASCII/BMP text converts 1:1 — the
// overwhelmingly common case must be an identity mapping.
func TestUTF16ColBMP(t *testing.T) {
	line := []rune("hello, wörld")
	for i := 0; i <= len(line); i++ {
		if got := UTF16Col(line, i); got != i {
			t.Errorf("UTF16Col(BMP, %d) = %d, want identity", i, got)
		}
		if got := RuneCol(line, i); got != i {
			t.Errorf("RuneCol(BMP, %d) = %d, want identity", i, got)
		}
	}
}

// TestUTF16ColSurrogates pins the two-code-unit accounting for non-BMP
// runes — the whole reason these helpers exist. "a🙂b": the emoji is
// one rune but two UTF-16 units, so 'b' sits at rune 2 / UTF-16 3.
func TestUTF16ColSurrogates(t *testing.T) {
	line := []rune("a🙂b")
	if got := UTF16Col(line, 2); got != 3 {
		t.Errorf("UTF16Col rune 2 = %d, want 3", got)
	}
	if got := RuneCol(line, 3); got != 2 {
		t.Errorf("RuneCol utf16 3 = %d, want rune 2", got)
	}
	// Column landing mid-surrogate resolves to the emoji's own index.
	if got := RuneCol(line, 2); got != 1 {
		t.Errorf("RuneCol mid-surrogate = %d, want 1", got)
	}
	// Past-the-end clamps on both sides.
	if got := UTF16Col(line, 99); got != 4 {
		t.Errorf("UTF16Col overflow = %d, want 4", got)
	}
	if got := RuneCol(line, 99); got != 3 {
		t.Errorf("RuneCol overflow = %d, want 3", got)
	}
}

// TestHoverText pins the three wire shapes servers actually send for
// hover contents, plus the empty/unrecognised → "" fallback.
func TestHoverText(t *testing.T) {
	cases := []struct {
		name, contents, want string
	}{
		{"markup content", `{"kind":"plaintext","value":"func Foo()"}`, "func Foo()"},
		{"bare string", `"just text"`, "just text"},
		{"array of strings", `["one","two"]`, "one\ntwo"},
		{"array of language pairs", `[{"language":"go","value":"var x int"}]`, "var x int"},
		{"empty object", `{}`, ""},
		{"null-ish", ``, ""},
	}
	for _, tc := range cases {
		h := Hover{Contents: json.RawMessage(tc.contents)}
		if got := h.HoverText(); got != tc.want {
			t.Errorf("%s: HoverText() = %q, want %q", tc.name, got, tc.want)
		}
	}
}

// TestParseDocumentSymbols_Hierarchical pins the modern shape: the tree
// flattens in document order with Depth recording the nesting, and the
// jump position comes from selectionRange (the NAME) rather than range
// (the whole declaration) — landing on a 200-line function's opening
// brace is exactly what this is meant to avoid.
func TestParseDocumentSymbols_Hierarchical(t *testing.T) {
	raw := json.RawMessage(`[
		{"name":"Tab","kind":23,
		 "range":{"start":{"line":10,"character":0},"end":{"line":40,"character":1}},
		 "selectionRange":{"start":{"line":10,"character":5},"end":{"line":10,"character":8}},
		 "children":[
			{"name":"Render","kind":6,"detail":"func()",
			 "range":{"start":{"line":20,"character":0},"end":{"line":30,"character":1}},
			 "selectionRange":{"start":{"line":20,"character":15},"end":{"line":20,"character":21}}}
		 ]},
		{"name":"NewTab","kind":12,
		 "range":{"start":{"line":50,"character":0},"end":{"line":55,"character":1}},
		 "selectionRange":{"start":{"line":50,"character":5},"end":{"line":50,"character":11}}}
	]`)
	got := ParseDocumentSymbols(raw)
	if len(got) != 3 {
		t.Fatalf("parsed %d symbols, want 3: %+v", len(got), got)
	}
	want := []Symbol{
		{Name: "Tab", Kind: 23, Depth: 0, Pos: Position{Line: 10, Character: 5}},
		{Name: "Render", Detail: "func()", Kind: 6, Depth: 1, Pos: Position{Line: 20, Character: 15}},
		{Name: "NewTab", Kind: 12, Depth: 0, Pos: Position{Line: 50, Character: 5}},
	}
	for i, w := range want {
		if got[i] != w {
			t.Errorf("symbol %d = %+v, want %+v", i, got[i], w)
		}
	}
}

// TestParseDocumentSymbols_Flat pins the legacy shape and the reason the
// two are told apart by a "location" key rather than by a failed
// unmarshal: SymbolInformation decodes cleanly into DocumentSymbol, so a
// shape sniff based on error would silently report every symbol at 0:0.
func TestParseDocumentSymbols_Flat(t *testing.T) {
	raw := json.RawMessage(`[
		{"name":"Helper","kind":12,"containerName":"pkg",
		 "location":{"uri":"file:///a/b.go",
		   "range":{"start":{"line":7,"character":5},"end":{"line":7,"character":11}}}}
	]`)
	got := ParseDocumentSymbols(raw)
	if len(got) != 1 {
		t.Fatalf("parsed %d symbols, want 1", len(got))
	}
	want := Symbol{Name: "Helper", Detail: "pkg", Kind: 12, Depth: 0,
		Pos: Position{Line: 7, Character: 5}}
	if got[0] != want {
		t.Errorf("symbol = %+v, want %+v", got[0], want)
	}
}

// TestParseDocumentSymbols_Degenerate pins the quiet failures: an empty
// or null payload and unparseable JSON all mean "this document declares
// nothing", never a panic. A hierarchical entry with no selectionRange
// falls back to the declaration's start rather than the file origin.
func TestParseDocumentSymbols_Degenerate(t *testing.T) {
	for _, raw := range []string{``, `null`, `{"not":"an array"}`, `[[]]`} {
		if got := ParseDocumentSymbols(json.RawMessage(raw)); got != nil {
			t.Errorf("ParseDocumentSymbols(%q) = %+v, want nil", raw, got)
		}
	}
	fallback := ParseDocumentSymbols(json.RawMessage(
		`[{"name":"X","kind":12,"range":{"start":{"line":4,"character":2},"end":{"line":9,"character":0}}}]`))
	if len(fallback) != 1 || fallback[0].Pos != (Position{Line: 4, Character: 2}) {
		t.Errorf("missing selectionRange should fall back to range.start, got %+v", fallback)
	}
}

// TestSymbolKindName pins the display table's edges: the spec's numbers
// are dense and 1-based, and a kind this client hasn't heard of costs a
// blank column rather than a wrong word or a panic.
func TestSymbolKindName(t *testing.T) {
	cases := map[int]string{
		1: "file", 6: "method", 12: "function", 23: "struct", 26: "type parameter",
		0: "", 27: "", -1: "", 999: "",
	}
	for kind, want := range cases {
		if got := SymbolKindName(kind); got != want {
			t.Errorf("SymbolKindName(%d) = %q, want %q", kind, got, want)
		}
	}
}
