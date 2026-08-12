// =============================================================================
// File: internal/lsp/completion_test.go
// Author: Rohan Allison <rohanthewiz@gmail.com>
// Created: 2026-08-12
// Copyright: 2026 Rohan Allison. All rights reserved.
// =============================================================================

// Tests for the completion wire layer: the three shape unions the
// protocol allows, the spec's fallback chain, and the two flags that
// change what the editor does with an item (snippet, deprecated).

package lsp

import (
	"encoding/json"
	"testing"
)

// TestParseCompletion_ListShape pins the envelope gopls sends: a
// CompletionList whose isIncomplete flag must survive parsing, because
// it is the difference between filtering locally and re-asking.
func TestParseCompletion_ListShape(t *testing.T) {
	raw := json.RawMessage(`{
		"isIncomplete": true,
		"items": [
			{"label": "Println", "kind": 3, "detail": "func(a ...any)"},
			{"label": "Printf",  "kind": 3}
		]
	}`)
	items, incomplete := ParseCompletion(raw)
	if !incomplete {
		t.Error("isIncomplete must survive the parse")
	}
	if len(items) != 2 {
		t.Fatalf("items = %d, want 2", len(items))
	}
	if items[0].Label != "Println" || items[0].Detail != "func(a ...any)" {
		t.Errorf("item[0] = %+v, want the labelled/detailed Println", items[0])
	}
	if items[0].Kind != CompletionFunction {
		t.Errorf("kind = %d, want %d", items[0].Kind, CompletionFunction)
	}
}

// TestParseCompletion_ArrayShape pins the older bare-array response.
// isIncomplete has no home in that shape, so it must read as false —
// which means "filter locally", the safe answer.
func TestParseCompletion_ArrayShape(t *testing.T) {
	items, incomplete := ParseCompletion(json.RawMessage(`[{"label":"x"}]`))
	if incomplete {
		t.Error("a bare array cannot be incomplete")
	}
	if len(items) != 1 || items[0].Label != "x" {
		t.Fatalf("items = %+v, want one item labelled x", items)
	}
}

// TestParseCompletion_EmptyShapes pins the silent-degradation contract:
// every payload with nothing in it yields no items and no error path.
func TestParseCompletion_EmptyShapes(t *testing.T) {
	for _, raw := range []string{"", "null", "[]", `{"items":[]}`, `"nonsense"`} {
		items, incomplete := ParseCompletion(json.RawMessage(raw))
		if len(items) != 0 || incomplete {
			t.Errorf("ParseCompletion(%q) = %d items / incomplete=%v, want nothing",
				raw, len(items), incomplete)
		}
	}
}

// TestParseCompletion_SkipsUnusable pins the per-item tolerance: a
// malformed or unlabelled entry costs the user that entry, never the
// whole list.
func TestParseCompletion_SkipsUnusable(t *testing.T) {
	raw := json.RawMessage(`{"items":[
		{"label": "  "},
		{"label": 42},
		{"label": "good"}
	]}`)
	items, _ := ParseCompletion(raw)
	if len(items) != 1 || items[0].Label != "good" {
		t.Fatalf("items = %+v, want only the usable one", items)
	}
}

// TestParseCompletionItem_Fallbacks pins the spec's defaulting rules.
// Getting these wrong is quiet and nasty: an empty FilterText matches
// every prefix, and an empty InsertText types nothing.
func TestParseCompletionItem_Fallbacks(t *testing.T) {
	item, ok := ParseCompletionItem(json.RawMessage(`{"label":"Println"}`))
	if !ok {
		t.Fatal("a bare labelled item must parse")
	}
	if item.SortText != "Println" || item.FilterText != "Println" || item.InsertText != "Println" {
		t.Errorf("fallbacks = sort %q filter %q insert %q, want all Println",
			item.SortText, item.FilterText, item.InsertText)
	}
}

// TestParseCompletionItem_ExplicitFieldsWin confirms the fallbacks only
// fill gaps — a server that distinguishes label from filter/insert text
// must be obeyed.
func TestParseCompletionItem_ExplicitFieldsWin(t *testing.T) {
	item, _ := ParseCompletionItem(json.RawMessage(
		`{"label":"Println","filterText":"fmt.Println","insertText":"Println(","sortText":"00"}`))
	if item.FilterText != "fmt.Println" || item.InsertText != "Println(" || item.SortText != "00" {
		t.Errorf("item = %+v, want the server's own values", item)
	}
}

// TestParseCompletionItem_InsertReplaceEdit pins the union that matters
// most for correctness: the two-range form must yield the REPLACE range,
// or completing in the middle of a word doubles its tail.
func TestParseCompletionItem_InsertReplaceEdit(t *testing.T) {
	item, _ := ParseCompletionItem(json.RawMessage(`{
		"label": "Println",
		"textEdit": {
			"newText": "Println",
			"insert":  {"start":{"line":3,"character":4},"end":{"line":3,"character":8}},
			"replace": {"start":{"line":3,"character":4},"end":{"line":3,"character":12}}
		}
	}`))
	if item.Edit == nil {
		t.Fatal("the two-range form must still produce an edit")
	}
	if item.Edit.Range.End.Character != 12 {
		t.Errorf("edit end = %d, want the replace range's 12 (not insert's 8)",
			item.Edit.Range.End.Character)
	}
	if item.Edit.NewText != "Println" {
		t.Errorf("newText = %q", item.Edit.NewText)
	}
}

// TestParseCompletionItem_PlainTextEdit pins the ordinary single-range
// form — the shape a client that never declared insertReplaceSupport
// actually receives.
func TestParseCompletionItem_PlainTextEdit(t *testing.T) {
	item, _ := ParseCompletionItem(json.RawMessage(`{
		"label": "Println",
		"textEdit": {
			"newText": "Println",
			"range": {"start":{"line":1,"character":2},"end":{"line":1,"character":5}}
		},
		"additionalTextEdits": [
			{"newText": "\t\"fmt\"\n", "range": {"start":{"line":0,"character":0},"end":{"line":0,"character":0}}}
		]
	}`))
	if item.Edit == nil || item.Edit.Range.Start.Character != 2 {
		t.Fatalf("edit = %+v, want the single range", item.Edit)
	}
	if len(item.Additional) != 1 {
		t.Fatalf("additional = %d, want the auto-import edit kept", len(item.Additional))
	}
}

// TestParseCompletionItem_NoEdit confirms an item without a textEdit
// parses cleanly with a nil Edit — the caller synthesises the range from
// the token under the caret in that case.
func TestParseCompletionItem_NoEdit(t *testing.T) {
	item, _ := ParseCompletionItem(json.RawMessage(`{"label":"x","textEdit":null}`))
	if item.Edit != nil {
		t.Errorf("edit = %+v, want nil", item.Edit)
	}
}

// TestParseCompletionItem_SnippetFlagged pins the flag the app layer
// refuses on. Parsing it is the only reason InsertTextFormat is modelled
// at all: the client declares snippetSupport:false, so an item arriving
// as a snippet is a server ignoring the declaration, and writing its
// `${1:}` syntax into the buffer would be the worst outcome.
func TestParseCompletionItem_SnippetFlagged(t *testing.T) {
	item, _ := ParseCompletionItem(json.RawMessage(
		`{"label":"for","insertText":"for ${1:i} := range ${2:x}","insertTextFormat":2}`))
	if !item.Snippet {
		t.Error("insertTextFormat 2 must set Snippet")
	}
	plain, _ := ParseCompletionItem(json.RawMessage(`{"label":"for","insertTextFormat":1}`))
	if plain.Snippet {
		t.Error("insertTextFormat 1 is plain text")
	}
}

// TestParseCompletionItem_Deprecated pins both spellings — the modern
// tag array and the legacy boolean — because servers in the wild still
// send either.
func TestParseCompletionItem_Deprecated(t *testing.T) {
	tagged, _ := ParseCompletionItem(json.RawMessage(`{"label":"old","tags":[1]}`))
	if !tagged.Deprecated {
		t.Error("tag 1 must mark the item deprecated")
	}
	legacy, _ := ParseCompletionItem(json.RawMessage(`{"label":"old","deprecated":true}`))
	if !legacy.Deprecated {
		t.Error("the legacy boolean must still be honoured")
	}
}

// TestParseCompletionItem_DocumentationUnion pins the string |
// MarkupContent union, flattened through the same helper hover uses.
func TestParseCompletionItem_DocumentationUnion(t *testing.T) {
	markup, _ := ParseCompletionItem(json.RawMessage(
		`{"label":"x","documentation":{"kind":"markdown","value":"the doc"}}`))
	if markup.Doc != "the doc" {
		t.Errorf("markup doc = %q", markup.Doc)
	}
	bare, _ := ParseCompletionItem(json.RawMessage(`{"label":"x","documentation":"the doc"}`))
	if bare.Doc != "the doc" {
		t.Errorf("bare doc = %q", bare.Doc)
	}
}

// TestParseCompletionItem_KeepsRaw pins the field the resolve round trip
// depends on: the item must go back to the server byte-for-byte, since
// re-marshalling the parsed struct would drop the server's own `data`
// correlation field and gopls declines a resolve without it.
func TestParseCompletionItem_KeepsRaw(t *testing.T) {
	raw := json.RawMessage(`{"label":"x","data":{"uri":"file:///a.go","tok":7}}`)
	item, _ := ParseCompletionItem(raw)
	if string(item.Raw) != string(raw) {
		t.Errorf("Raw = %s, want the bytes verbatim", item.Raw)
	}
}

// TestCompletionKindName pins the few kinds the detail pane names, and
// the deliberate blank for one it has no word for.
func TestCompletionKindName(t *testing.T) {
	for kind, want := range map[int]string{
		CompletionFunction: "func",
		CompletionVariable: "var",
		CompletionStruct:   "struct",
		CompletionKeyword:  "keyword",
		0:                  "",
		999:                "",
	} {
		if got := CompletionKindName(kind); got != want {
			t.Errorf("CompletionKindName(%d) = %q, want %q", kind, got, want)
		}
	}
}
