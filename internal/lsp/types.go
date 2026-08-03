// =============================================================================
// File: internal/lsp/types.go
// Author: Rohan Allison <rohanthewiz@gmail.com>
// Created: 2026-07-09
// Copyright: 2026 Rohan Allison. All rights reserved.
// =============================================================================

// Package lsp is a minimal Language Server Protocol client, hand-rolled
// over JSON-RPC 2.0 on stdio with nothing beyond the standard library.
// The editor needs a tiny protocol subset — initialize, document sync,
// publishDiagnostics, definition, references, hover, signature help,
// document symbols — so a dependency-free client is
// both smaller and easier to reason about than pulling in a full LSP
// framework (which would also fight the project's no-CGO / few-deps
// philosophy).
//
// types.go holds the wire structs for that subset plus the two
// coordinate systems the protocol forces on us:
//
//   - URIs: LSP identifies documents by file:// URI; the editor thinks
//     in absolute paths. PathToURI / URIToPath convert.
//   - UTF-16 columns: LSP character offsets count UTF-16 code units
//     (a JavaScript inheritance); the editor's Buffer counts runes.
//     UTF16Col / RuneCol convert per line. Getting this wrong shows up
//     as off-by-N diagnostics on any line containing non-BMP characters
//     (emoji in a string literal is the classic case).
package lsp

import (
	"encoding/json"
	"net/url"
	"strings"
	"unicode/utf16"
	"unicode/utf8"
)

// Position is an LSP text position: zero-based line, zero-based
// character offset measured in UTF-16 code units.
type Position struct {
	Line      int `json:"line"`
	Character int `json:"character"`
}

// Range is a half-open [Start, End) span of an LSP document.
type Range struct {
	Start Position `json:"start"`
	End   Position `json:"end"`
}

// Location names a range inside a document — the payload of a
// definition response.
type Location struct {
	URI   string `json:"uri"`
	Range Range  `json:"range"`
}

// Diagnostic severities, per the LSP spec. Zero means the server
// omitted the field; the spec says to treat that as an error.
const (
	SeverityError   = 1
	SeverityWarning = 2
	SeverityInfo    = 3
	SeverityHint    = 4
)

// Diagnostic is one server-reported problem in a document.
type Diagnostic struct {
	Range    Range  `json:"range"`
	Severity int    `json:"severity,omitempty"`
	Source   string `json:"source,omitempty"`
	Message  string `json:"message"`
}

// PublishDiagnosticsParams is the payload of the
// textDocument/publishDiagnostics notification.
type PublishDiagnosticsParams struct {
	URI         string       `json:"uri"`
	Diagnostics []Diagnostic `json:"diagnostics"`
}

// TextDocumentItem describes a document being opened.
type TextDocumentItem struct {
	URI        string `json:"uri"`
	LanguageID string `json:"languageId"`
	Version    int    `json:"version"`
	Text       string `json:"text"`
}

// TextDocumentIdentifier names an already-open document.
type TextDocumentIdentifier struct {
	URI string `json:"uri"`
}

// VersionedTextDocumentIdentifier names a document plus the client's
// version counter — required by didChange so the server can order
// edits.
type VersionedTextDocumentIdentifier struct {
	URI     string `json:"uri"`
	Version int    `json:"version"`
}

// DidOpenParams is the payload of textDocument/didOpen.
type DidOpenParams struct {
	TextDocument TextDocumentItem `json:"textDocument"`
}

// DidChangeParams is the payload of textDocument/didChange. The editor
// always sends one change event carrying the full document text (a
// change with no range means "replace everything" per the spec), which
// sidesteps incremental-edit bookkeeping entirely — the files this
// editor handles are small enough that full sync is cheap.
type DidChangeParams struct {
	TextDocument   VersionedTextDocumentIdentifier `json:"textDocument"`
	ContentChanges []ContentChange                 `json:"contentChanges"`
}

// ContentChange is one edit in a didChange. With Range omitted it
// replaces the whole document.
type ContentChange struct {
	Text string `json:"text"`
}

// DidSaveParams is the payload of textDocument/didSave.
type DidSaveParams struct {
	TextDocument TextDocumentIdentifier `json:"textDocument"`
}

// DidCloseParams is the payload of textDocument/didClose.
type DidCloseParams struct {
	TextDocument TextDocumentIdentifier `json:"textDocument"`
}

// TextDocumentPositionParams is the shared request payload of
// definition and hover — a document plus a position in it.
type TextDocumentPositionParams struct {
	TextDocument TextDocumentIdentifier `json:"textDocument"`
	Position     Position               `json:"position"`
}

// ReferenceContext is the one option textDocument/references takes.
// IncludeDeclaration decides whether the declaration itself counts as a
// reference; the editor always asks for it, because a "who uses this?"
// list that omits the thing being used reads as a hole.
type ReferenceContext struct {
	IncludeDeclaration bool `json:"includeDeclaration"`
}

// ReferenceParams is the payload of textDocument/references — a position
// (like definition and hover) plus that context object. It can't reuse
// TextDocumentPositionParams because the context field is required: a
// server given no context is entitled to refuse the request.
type ReferenceParams struct {
	TextDocument TextDocumentIdentifier `json:"textDocument"`
	Position     Position               `json:"position"`
	Context      ReferenceContext       `json:"context"`
}

// RenameParams is the payload of textDocument/rename — the position of the
// symbol to rename plus what to call it. Like ReferenceParams it can't
// reuse TextDocumentPositionParams, because newName is required and sits
// beside the position rather than inside a nested options object.
//
// Note there is no old name on the wire, and that is not an omission: the
// POSITION is the identity of the thing being renamed. The server resolves
// it to a symbol and rewrites every binding of that symbol, which is
// exactly what makes this different from a textual replace-all — and why
// the answer can reach files the user has never opened.
type RenameParams struct {
	TextDocument TextDocumentIdentifier `json:"textDocument"`
	Position     Position               `json:"position"`
	NewName      string                 `json:"newName"`
}

// Hover is the response payload of textDocument/hover. Contents is
// left raw because servers are allowed to send several shapes
// (MarkupContent, MarkedString, arrays of either); HoverText flattens
// whichever arrives into plain lines.
type Hover struct {
	Contents json.RawMessage `json:"contents"`
	Range    *Range          `json:"range,omitempty"`
}

// markupContent is the modern documentation payload: {kind, value}. The
// protocol uses it for hover, signature documentation and parameter
// documentation alike, each of which also allows a bare string.
type markupContent struct {
	Kind  string `json:"kind"`
	Value string `json:"value"`
}

// markupText flattens the `string | MarkupContent` union the protocol
// uses everywhere it attaches documentation to something. "" means the
// payload was empty or a shape this client doesn't model, which every
// caller treats as "nothing to show".
func markupText(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var mc markupContent
	if err := json.Unmarshal(raw, &mc); err == nil && mc.Value != "" {
		return mc.Value
	}
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		return s
	}
	return ""
}

// HoverText flattens a hover's Contents into its text value. It
// tolerates the three shapes real servers send — MarkupContent
// ({kind, value}), a bare string, and an array of strings /
// {language, value} objects — and returns "" when the payload is
// empty or unrecognised, which callers treat as "nothing to show".
func (h Hover) HoverText() string {
	if len(h.Contents) == 0 {
		return ""
	}
	// MarkupContent (what gopls sends when the client advertises it), or
	// a bare MarkedString — the shared union both signature help and
	// parameter documentation also use.
	if s := markupText(h.Contents); s != "" {
		return s
	}
	// Array of MarkedStrings (each a string or {language, value}) — the
	// one shape unique to hover, so it stays here.
	var arr []json.RawMessage
	if err := json.Unmarshal(h.Contents, &arr); err == nil {
		out := ""
		for _, el := range arr {
			var es string
			if json.Unmarshal(el, &es) == nil {
				if out != "" {
					out += "\n"
				}
				out += es
				continue
			}
			var emc markupContent
			if json.Unmarshal(el, &emc) == nil && emc.Value != "" {
				if out != "" {
					out += "\n"
				}
				out += emc.Value
			}
		}
		return out
	}
	return ""
}

// -----------------------------------------------------------------------------
// Signature help
// -----------------------------------------------------------------------------

// SignatureHelp is the response payload of textDocument/signatureHelp:
// the callable's overloads plus which one, and which of its parameters,
// applies at the requested position.
//
// Both active* fields are POINTERS because the protocol distinguishes
// "absent" from zero, and zero is the first signature and the first
// parameter — the overwhelmingly common answer. A plain int would make a
// server that omitted the field indistinguishable from one saying "the
// first parameter", which is the difference between an accurate hint and
// a confidently wrong one.
type SignatureHelp struct {
	Signatures      []SignatureInformation `json:"signatures"`
	ActiveSignature *int                   `json:"activeSignature"`
	ActiveParameter *int                   `json:"activeParameter"`
}

// SignatureInformation is one overload. Its own ActiveParameter, when
// present, overrides the enclosing help's — the spec's precedence, and
// the only way a server can say different things about different
// overloads in one response.
type SignatureInformation struct {
	Label           string                 `json:"label"`
	Documentation   json.RawMessage        `json:"documentation"`
	Parameters      []ParameterInformation `json:"parameters"`
	ActiveParameter *int                   `json:"activeParameter"`
}

// ParameterInformation names one parameter inside its signature's label.
// Label is raw because the protocol allows two shapes for it (see
// paramRange): a substring of the signature label, or a [start, end)
// pair of UTF-16 offsets into it.
type ParameterInformation struct {
	Label         json.RawMessage `json:"label"`
	Documentation json.RawMessage `json:"documentation"`
}

// Signature is the editor-facing normal form the whole response collapses
// into: ONE signature (the active one), its documentation, and where the
// active parameter sits inside its label as RUNE offsets.
//
// Flat and pre-resolved for the same reason Symbol is: the consumer is a
// tooltip, so the protocol's unions, pointer-optionals, precedence rules
// and UTF-16 offsets are all resolved once here rather than in the app
// layer, which never learns that any of them existed.
type Signature struct {
	Label string
	Doc   string
	// ParamStart / ParamEnd bound the active parameter within Label, in
	// runes, half-open. Both are -1 when no parameter is active or the
	// server's label shape couldn't be resolved — the tooltip then shows
	// the signature with nothing emphasised, which is still useful.
	ParamStart int
	ParamEnd   int
	ParamDoc   string
	// Index / Count describe the overload set, so a tooltip can say
	// "2 of 3". Count is 1 for a language like Go that has no overloads.
	Index int
	Count int
}

// ParseSignatureHelp normalises a signatureHelp response, returning nil
// when the server has nothing to say — a legitimate answer for a cursor
// that isn't inside a call.
func ParseSignatureHelp(raw json.RawMessage) *Signature {
	if len(raw) == 0 || string(raw) == "null" {
		return nil
	}
	var help SignatureHelp
	if err := json.Unmarshal(raw, &help); err != nil {
		return nil
	}
	if len(help.Signatures) == 0 {
		return nil
	}
	idx := 0
	if help.ActiveSignature != nil {
		idx = *help.ActiveSignature
	}
	// Clamped rather than trusted: an out-of-range index is a server bug
	// that would otherwise panic, and showing the first overload beats
	// showing nothing.
	if idx < 0 || idx >= len(help.Signatures) {
		idx = 0
	}
	si := help.Signatures[idx]

	out := &Signature{
		Label:      si.Label,
		Doc:        markupText(si.Documentation),
		ParamStart: -1,
		ParamEnd:   -1,
		Index:      idx,
		Count:      len(help.Signatures),
	}

	// Signature-level active parameter wins over the help-level one.
	active := -1
	if help.ActiveParameter != nil {
		active = *help.ActiveParameter
	}
	if si.ActiveParameter != nil {
		active = *si.ActiveParameter
	}
	// LSP 3.17 lets a server send -1 to mean "no parameter is active"
	// explicitly, which falls out of the same bounds check.
	if active < 0 || active >= len(si.Parameters) {
		return out
	}
	p := si.Parameters[active]
	out.ParamDoc = markupText(p.Documentation)
	out.ParamStart, out.ParamEnd = paramRange(si.Label, p.Label)
	return out
}

// paramRange resolves a ParameterInformation label to rune offsets into
// its signature's label, returning (-1, -1) when it can't.
//
// The offset form is tried FIRST and is the one to trust: it is exact.
// The string form is the protocol's older, looser option — "a substring
// of its containing signature label" — and resolving it means searching,
// which can land on the wrong occurrence when a parameter's text repeats
// (a signature of `f(int, int)` has no way to tell the two apart). Real
// labels are usually `name type`, so first-occurrence is right in
// practice; when it isn't, the emphasis is misplaced by a few columns
// rather than the tooltip being wrong.
func paramRange(label string, raw json.RawMessage) (start, end int) {
	if len(raw) == 0 {
		return -1, -1
	}
	labelRunes := []rune(label)
	var pair []int
	if err := json.Unmarshal(raw, &pair); err == nil && len(pair) == 2 {
		s := RuneCol(labelRunes, pair[0])
		e := RuneCol(labelRunes, pair[1])
		if s < 0 || e < s {
			return -1, -1
		}
		return s, e
	}
	var text string
	if err := json.Unmarshal(raw, &text); err != nil || text == "" {
		return -1, -1
	}
	i := strings.Index(label, text)
	if i < 0 {
		return -1, -1
	}
	s := utf8.RuneCountInString(label[:i])
	return s, s + utf8.RuneCountInString(text)
}

// -----------------------------------------------------------------------------
// Document symbols
// -----------------------------------------------------------------------------

// DocumentSymbolParams is the payload of textDocument/documentSymbol.
type DocumentSymbolParams struct {
	TextDocument TextDocumentIdentifier `json:"textDocument"`
}

// DocumentSymbol is the modern, HIERARCHICAL response element: a symbol
// plus the symbols declared inside it (a struct's methods, a class's
// members). Range covers the whole declaration; SelectionRange covers
// just the name, which is where a "go to symbol" jump wants to land —
// the top of a 200-line function tells you less than its signature does.
type DocumentSymbol struct {
	Name           string           `json:"name"`
	Detail         string           `json:"detail,omitempty"`
	Kind           int              `json:"kind"`
	Range          Range            `json:"range"`
	SelectionRange Range            `json:"selectionRange"`
	Children       []DocumentSymbol `json:"children,omitempty"`
}

// SymbolInformation is the LEGACY flat response element, still what a
// server sends when it doesn't advertise hierarchical support. It
// carries no children; nesting is implied by ContainerName, which is a
// string rather than a link and so cannot be re-assembled reliably.
type SymbolInformation struct {
	Name          string   `json:"name"`
	Kind          int      `json:"kind"`
	Location      Location `json:"location"`
	ContainerName string   `json:"containerName,omitempty"`
}

// Symbol is the editor-facing normal form both response shapes collapse
// into: a flat, document-ordered list where Depth records how deep the
// entry sat in the tree. Flat because the consumer is a fuzzy picker —
// a tree would have to be flattened for display anyway, and doing it
// once here means the app layer never learns that the protocol has two
// answers to the same question.
type Symbol struct {
	Name   string
	Detail string
	Kind   int
	Depth  int
	// Pos is where the cursor lands: the name's start, not the
	// declaration's.
	Pos Position
}

// symbolKindNames maps the spec's SymbolKind numbers (1-based) to the
// short word the picker shows beside a name. Kept as a slice rather
// than a map because the numbering is dense and spec-fixed; index 0 is
// the unused zero value, so a server that omits Kind gets "".
var symbolKindNames = []string{
	"", "file", "module", "namespace", "package", "class", "method",
	"property", "field", "constructor", "enum", "interface", "function",
	"variable", "constant", "string", "number", "boolean", "array",
	"object", "key", "null", "enum member", "struct", "event", "operator",
	"type parameter",
}

// SymbolKindName returns the display word for an LSP SymbolKind, or ""
// for a kind this client doesn't know. A future spec revision adding
// kind 27 costs a blank column, not a wrong label.
func SymbolKindName(kind int) string {
	if kind < 0 || kind >= len(symbolKindNames) {
		return ""
	}
	return symbolKindNames[kind]
}

// ParseDocumentSymbols normalises a documentSymbol response into the
// flat Symbol list, tolerating both shapes the protocol allows.
//
// The two are told apart by CONTENT, not by trying one unmarshal and
// falling back on error: both shapes decode cleanly into either struct
// (JSON ignores unknown fields and leaves missing ones zero), so a
// SymbolInformation read as a DocumentSymbol would parse "successfully"
// with every position at 0:0 — every symbol jumping to line 1. A
// "location" key is the discriminator: only the flat form has one.
func ParseDocumentSymbols(raw json.RawMessage) []Symbol {
	if len(raw) == 0 || string(raw) == "null" {
		return nil
	}
	var probe []struct {
		Location *json.RawMessage `json:"location"`
	}
	if err := json.Unmarshal(raw, &probe); err != nil {
		return nil
	}
	flat := false
	for _, p := range probe {
		if p.Location != nil {
			flat = true
			break
		}
	}
	if flat {
		var infos []SymbolInformation
		if err := json.Unmarshal(raw, &infos); err != nil {
			return nil
		}
		out := make([]Symbol, 0, len(infos))
		for _, si := range infos {
			// ContainerName is the only nesting hint the flat form
			// offers; it becomes Detail rather than a Depth, because
			// mapping names back to parents is guesswork on any
			// document with two same-named containers.
			out = append(out, Symbol{
				Name:   si.Name,
				Detail: si.ContainerName,
				Kind:   si.Kind,
				Pos:    si.Location.Range.Start,
			})
		}
		return out
	}
	var tree []DocumentSymbol
	if err := json.Unmarshal(raw, &tree); err != nil {
		return nil
	}
	var out []Symbol
	flattenSymbols(tree, 0, &out)
	return out
}

// flattenSymbols walks the hierarchy depth-first, preserving the
// server's ordering so the picker reads top-to-bottom like the file.
// A zero SelectionRange (a server that sent only Range) falls back to
// the declaration's start rather than pointing at the document origin.
func flattenSymbols(syms []DocumentSymbol, depth int, out *[]Symbol) {
	for _, s := range syms {
		pos := s.SelectionRange.Start
		if s.SelectionRange == (Range{}) {
			pos = s.Range.Start
		}
		*out = append(*out, Symbol{
			Name:   s.Name,
			Detail: s.Detail,
			Kind:   s.Kind,
			Depth:  depth,
			Pos:    pos,
		})
		flattenSymbols(s.Children, depth+1, out)
	}
}

// -----------------------------------------------------------------------------
// URI conversion
// -----------------------------------------------------------------------------

// PathToURI converts an absolute filesystem path to a file:// URI.
// Built via net/url so paths with spaces or other reserved characters
// escape correctly instead of producing URIs some servers reject.
func PathToURI(path string) string {
	u := url.URL{Scheme: "file", Path: path}
	return u.String()
}

// URIToPath converts a file:// URI back to a filesystem path. Non-file
// or unparseable URIs return "" — the caller treats that as "not a
// document we can open" (e.g. a definition inside a zipped stdlib
// archive some servers report with custom schemes).
func URIToPath(uri string) string {
	u, err := url.Parse(uri)
	if err != nil || u.Scheme != "file" {
		return ""
	}
	return u.Path
}

// -----------------------------------------------------------------------------
// UTF-16 ↔ rune column conversion
// -----------------------------------------------------------------------------

// UTF16Col converts a rune column in line to the UTF-16 code-unit
// column LSP expects. Runes outside the Basic Multilingual Plane
// (emoji, some CJK extensions) occupy two code units; everything else
// one. A runeCol past the end of the line clamps to the line's total
// UTF-16 length, matching the spec's "clamp to line length" rule.
func UTF16Col(line []rune, runeCol int) int {
	col := 0
	for i, r := range line {
		if i >= runeCol {
			break
		}
		col += utf16.RuneLen(r)
	}
	return col
}

// RuneCol converts a UTF-16 code-unit column from the server back to a
// rune index into line. Columns past the end clamp to len(line); a
// column landing on the second unit of a surrogate pair resolves to
// that rune's index (you can't address half a rune).
func RuneCol(line []rune, utf16Col int) int {
	col := 0
	for i, r := range line {
		// A target anywhere inside this rune's code units — including
		// the second half of a surrogate pair — addresses this rune.
		if utf16Col < col+utf16.RuneLen(r) {
			return i
		}
		col += utf16.RuneLen(r)
	}
	return len(line)
}
