// =============================================================================
// File: internal/lsp/completion.go
// Author: Rohan Allison <rohanthewiz@gmail.com>
// Created: 2026-08-12
// Copyright: 2026 Rohan Allison. All rights reserved.
// =============================================================================

// completion.go is the wire half of textDocument/completion — the one
// code-intelligence verb whose answer is a LIST THE USER TYPES INTO
// rather than a single fact to display. That difference is why the
// parsing here is fussier than hover's or definition's: every field
// this file keeps is a field the popup needs to rank, draw, or apply,
// and every field it drops is one the popup would have had to guess at.
//
// Three shape unions the protocol allows, all normalised away here so
// the app layer sees exactly one form:
//
//  1. The response is either a bare `CompletionItem[]` or a
//     `CompletionList {isIncomplete, items}`. The list form's flag is
//     load bearing — it means "this answer was computed for the prefix
//     you had, ask again when it changes" — so it survives as a return
//     value rather than being flattened away.
//  2. `documentation` is `string | MarkupContent`, the same union hover
//     and signature help already flatten through markupText.
//  3. `textEdit` is `TextEdit | InsertReplaceEdit`. The second shape
//     carries TWO ranges (insert = up to the caret, replace = over the
//     whole token under it) and ced always takes `replace`: completing
//     `fmt.Prin|tln` should produce `fmt.Println`, not `fmt.Printlntln`.
//
// What is deliberately NOT modelled: snippets. The client declares
// snippetSupport:false (see Initialize), so a conforming server sends
// literal text and there are no `${1:placeholder}` tab stops to expand.
// InsertTextFormat is still parsed, because a server that ignores the
// declaration would otherwise write its placeholder syntax into the
// user's buffer — Snippet items are dropped at the app layer instead.

package lsp

import (
	"encoding/json"
	"strings"
)

// Completion item kinds, from the protocol's CompletionItemKind enum.
// Only the ones the glyph table distinguishes are named; the rest are
// numbers that fall through to the default icon, which is the honest
// outcome for a kind ced has no opinion about.
const (
	CompletionText          = 1
	CompletionMethod        = 2
	CompletionFunction      = 3
	CompletionConstructor   = 4
	CompletionField         = 5
	CompletionVariable      = 6
	CompletionClass         = 7
	CompletionInterface     = 8
	CompletionModule        = 9
	CompletionProperty      = 10
	CompletionUnit          = 11
	CompletionValue         = 12
	CompletionEnum          = 13
	CompletionKeyword       = 14
	CompletionSnippetKind   = 15
	CompletionColor         = 16
	CompletionFile          = 17
	CompletionReference     = 18
	CompletionFolder        = 19
	CompletionEnumMember    = 20
	CompletionConstant      = 21
	CompletionStruct        = 22
	CompletionEvent         = 23
	CompletionOperator      = 24
	CompletionTypeParameter = 25
)

// insertFormatSnippet is InsertTextFormat's "this text contains tab
// stops" value. See the file comment for why it is parsed but never
// honoured.
const insertFormatSnippet = 2

// completionTagDeprecated is the CompletionItemTag the protocol defines
// for "this exists but don't use it".
const completionTagDeprecated = 1

// CompletionKindName gives a short lowercase word for a kind, for the
// popup's kind column and for tests that would otherwise assert on bare
// integers. Unknown kinds return "" rather than a placeholder: a column
// with nothing to say is better left blank than filled with "unknown".
func CompletionKindName(kind int) string {
	switch kind {
	case CompletionText:
		return "text"
	case CompletionMethod:
		return "method"
	case CompletionFunction:
		return "func"
	case CompletionConstructor:
		return "ctor"
	case CompletionField:
		return "field"
	case CompletionVariable:
		return "var"
	case CompletionClass:
		return "class"
	case CompletionInterface:
		return "iface"
	case CompletionModule:
		return "module"
	case CompletionProperty:
		return "prop"
	case CompletionUnit:
		return "unit"
	case CompletionValue:
		return "value"
	case CompletionEnum:
		return "enum"
	case CompletionKeyword:
		return "keyword"
	case CompletionSnippetKind:
		return "snippet"
	case CompletionColor:
		return "color"
	case CompletionFile:
		return "file"
	case CompletionReference:
		return "ref"
	case CompletionFolder:
		return "folder"
	case CompletionEnumMember:
		return "member"
	case CompletionConstant:
		return "const"
	case CompletionStruct:
		return "struct"
	case CompletionEvent:
		return "event"
	case CompletionOperator:
		return "op"
	case CompletionTypeParameter:
		return "typaram"
	}
	return ""
}

// CompletionItem is the slice of one completion the editor consumes.
//
// Raw is kept alongside the parsed fields for one reason:
// completionItem/resolve is defined as "send the item BACK and receive
// it enriched". Re-marshalling this struct would send a lossy copy —
// the server's own correlation fields (`data`, and anything else it
// tucked in that this client doesn't model) would be gone, and gopls
// answers a resolve without `data` by declining it.
type CompletionItem struct {
	// Label is what the list shows and, absent an edit, what gets typed.
	Label string
	// Kind is the CompletionItemKind enum value; 0 when unstated.
	Kind int
	// Detail is the one-line signature or type the server attaches —
	// `func(format string, a ...any) (n int, err error)`. Shown in the
	// popup's detail row, never inserted.
	Detail string
	// Doc is the flattened documentation, when the server sent any up
	// front. Often empty until a resolve fills it in.
	Doc string
	// SortText is the server's own ordering key, which encodes relevance
	// the client cannot recompute (gopls puts locals and
	// already-imported symbols ahead of everything else). Falls back to
	// Label when unstated, per the spec.
	SortText string
	// FilterText is what the user's typing is matched against when it
	// differs from the label — a server may present `Println` while
	// wanting `fmt.Println` to match. Falls back to Label.
	FilterText string
	// InsertText is the literal text to type when there is no Edit.
	// Falls back to Label.
	InsertText string
	// Snippet marks an item whose text carries tab stops. Kept so the
	// app layer can refuse it rather than write `${1:}` into the buffer.
	Snippet bool
	// Edit is the server's own replacement range, which is authoritative
	// when present: only the server knows how much of what the user
	// typed its item is meant to consume.
	Edit *TextEdit
	// Additional are the edits that must land WITH the item and
	// elsewhere in the file — for Go, this is the auto-import. They are
	// what makes accepting a completion a workspace edit rather than an
	// insertion.
	Additional []TextEdit
	// Preselect marks the item the server thinks should start selected.
	Preselect bool
	// Deprecated marks an item that exists but shouldn't be used, from
	// either the tag array or the legacy boolean.
	Deprecated bool
	// Raw is the item exactly as it arrived — see the type comment.
	Raw json.RawMessage
}

// completionItemWire is the on-the-wire shape. It exists separately from
// CompletionItem because three of its fields are unions that have to be
// decoded by hand, and because the parsed form applies the spec's
// fallback rules (SortText/FilterText/InsertText default to Label) once,
// here, rather than at every read site.
type completionItemWire struct {
	Label               string          `json:"label"`
	Kind                int             `json:"kind"`
	Detail              string          `json:"detail"`
	Documentation       json.RawMessage `json:"documentation"`
	SortText            string          `json:"sortText"`
	FilterText          string          `json:"filterText"`
	InsertText          string          `json:"insertText"`
	InsertTextFormat    int             `json:"insertTextFormat"`
	TextEdit            json.RawMessage `json:"textEdit"`
	AdditionalTextEdits []TextEdit      `json:"additionalTextEdits"`
	Preselect           bool            `json:"preselect"`
	Tags                []int           `json:"tags"`
	Deprecated          bool            `json:"deprecated"`
}

// insertReplaceEdit is the two-range form of textEdit. See the file
// comment for why `replace` wins.
type insertReplaceEdit struct {
	NewText string `json:"newText"`
	Insert  *Range `json:"insert"`
	Replace *Range `json:"replace"`
}

// completionList is the {isIncomplete, items} response envelope.
type completionList struct {
	IsIncomplete bool              `json:"isIncomplete"`
	Items        []json.RawMessage `json:"items"`
}

// ParseCompletion normalises a textDocument/completion result into the
// item slice and the "ask me again" flag.
//
// A null or unrecognised payload yields no items and incomplete=false,
// which every caller reads as "nothing to offer" — the same
// silent-degradation contract the rest of this package keeps. Individual
// items that fail to decode are skipped rather than failing the batch:
// one malformed entry should cost the user that entry, not the list.
func ParseCompletion(raw json.RawMessage) (items []CompletionItem, incomplete bool) {
	if len(raw) == 0 || string(raw) == "null" {
		return nil, false
	}

	// The list form is tried first because it is the one gopls sends and
	// because its `items` member makes the discrimination unambiguous: a
	// bare array cannot decode into a struct, and an object without
	// `items` yields an empty list either way.
	var list completionList
	if err := json.Unmarshal(raw, &list); err == nil && list.Items != nil {
		return parseCompletionItems(list.Items), list.IsIncomplete
	}

	var arr []json.RawMessage
	if err := json.Unmarshal(raw, &arr); err == nil {
		return parseCompletionItems(arr), false
	}
	return nil, false
}

// parseCompletionItems decodes each raw item, dropping the ones that
// don't decode or carry no label (an unlabelled item has nothing to show
// and nothing to type).
func parseCompletionItems(raws []json.RawMessage) []CompletionItem {
	out := make([]CompletionItem, 0, len(raws))
	for _, raw := range raws {
		item, ok := ParseCompletionItem(raw)
		if !ok {
			continue
		}
		out = append(out, item)
	}
	return out
}

// ParseCompletionItem decodes one item. Exported because
// completionItem/resolve answers with exactly this shape, and the
// resolve path must apply the same union handling and fallback rules the
// initial parse did.
func ParseCompletionItem(raw json.RawMessage) (CompletionItem, bool) {
	var w completionItemWire
	if err := json.Unmarshal(raw, &w); err != nil {
		return CompletionItem{}, false
	}
	if strings.TrimSpace(w.Label) == "" {
		return CompletionItem{}, false
	}

	item := CompletionItem{
		Label:      w.Label,
		Kind:       w.Kind,
		Detail:     strings.TrimSpace(w.Detail),
		Doc:        markupText(w.Documentation),
		SortText:   w.SortText,
		FilterText: w.FilterText,
		InsertText: w.InsertText,
		Snippet:    w.InsertTextFormat == insertFormatSnippet,
		Edit:       parseCompletionEdit(w.TextEdit),
		Additional: w.AdditionalTextEdits,
		Preselect:  w.Preselect,
		Deprecated: w.Deprecated,
		Raw:        raw,
	}
	// The spec's fallback chain, applied once so no read site has to
	// remember it. Each of these three means "same as the label" when
	// absent, and a caller that forgot would filter against "" (matching
	// everything) or insert "" (typing nothing).
	if item.SortText == "" {
		item.SortText = item.Label
	}
	if item.FilterText == "" {
		item.FilterText = item.Label
	}
	if item.InsertText == "" {
		item.InsertText = item.Label
	}
	for _, tag := range w.Tags {
		if tag == completionTagDeprecated {
			item.Deprecated = true
		}
	}
	return item, true
}

// parseCompletionEdit resolves the `TextEdit | InsertReplaceEdit` union.
// Nil means the item carries no edit, and the caller synthesises a range
// from the token under the caret.
func parseCompletionEdit(raw json.RawMessage) *TextEdit {
	if len(raw) == 0 || string(raw) == "null" {
		return nil
	}
	// InsertReplaceEdit is tried FIRST, because a plain TextEdit decode
	// of one would succeed with a zero Range — the two shapes differ only
	// in which members are present, and encoding/json ignores the ones it
	// doesn't know. Checking for the members unique to the two-range form
	// is the only reliable discrimination.
	var ir insertReplaceEdit
	if err := json.Unmarshal(raw, &ir); err == nil && (ir.Replace != nil || ir.Insert != nil) {
		rng := ir.Replace
		if rng == nil {
			rng = ir.Insert
		}
		return &TextEdit{Range: *rng, NewText: ir.NewText}
	}
	var te TextEdit
	if err := json.Unmarshal(raw, &te); err == nil {
		return &te
	}
	return nil
}

// -----------------------------------------------------------------------------
// Requests
// -----------------------------------------------------------------------------

// CompletionContext says why the request was made. The protocol's
// triggerKind values: 1 invoked (a deliberate gesture), 2 a trigger
// character, 3 re-request for an incomplete list. Servers use this —
// gopls offers a different set after `.` than after a bare invocation —
// so getting it right is not bookkeeping.
type CompletionContext struct {
	TriggerKind      int    `json:"triggerKind"`
	TriggerCharacter string `json:"triggerCharacter,omitempty"`
}

// Trigger kinds, named so call sites read as intent rather than as
// magic numbers.
const (
	CompletionInvoked        = 1
	CompletionTriggerChar    = 2
	CompletionTriggerRefresh = 3
)

// completionParams is TextDocumentPositionParams plus the context.
type completionParams struct {
	TextDocument TextDocumentIdentifier `json:"textDocument"`
	Position     Position               `json:"position"`
	Context      CompletionContext      `json:"context"`
}

// Completion asks what could be typed at pos. The second return is the
// server's isIncomplete flag — true means the answer was computed for
// the prefix that existed at request time and must be re-asked as the
// user keeps typing, rather than filtered locally.
func (c *Client) Completion(path string, pos Position, ctx CompletionContext) ([]CompletionItem, bool, error) {
	params := completionParams{
		TextDocument: TextDocumentIdentifier{URI: PathToURI(path)},
		Position:     pos,
		Context:      ctx,
	}
	var raw json.RawMessage
	if err := c.Call("textDocument/completion", params, &raw); err != nil {
		return nil, false, err
	}
	items, incomplete := ParseCompletion(raw)
	return items, incomplete, nil
}

// ResolveCompletion enriches one item — documentation, mostly — by
// sending it back verbatim.
//
// Correctness never depends on this call: the client does not declare
// resolveSupport, so a conforming server computes edits (including the
// auto-import) up front and an accepted item is complete without ever
// resolving. This exists purely to fill the popup's detail pane for the
// row the user is looking at, which is why a failure returns an error
// the caller is expected to swallow rather than report.
func (c *Client) ResolveCompletion(raw json.RawMessage) (*CompletionItem, error) {
	var out json.RawMessage
	if err := c.Call("completionItem/resolve", raw, &out); err != nil {
		return nil, err
	}
	item, ok := ParseCompletionItem(out)
	if !ok {
		return nil, nil
	}
	return &item, nil
}
