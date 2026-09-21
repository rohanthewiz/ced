// =============================================================================
// File: internal/lsp/callhierarchy.go
// Author: Rohan Allison <rohanthewiz@gmail.com>
// Created: 2026-09-21
// Copyright: 2026 Rohan Allison. All rights reserved.
// =============================================================================

// callhierarchy.go is "who calls this?" — the call hierarchy's incoming
// half. It differs from references in what it leaves OUT: references
// lists every mention of a function (its declaration, a value passed as a
// callback, a doc link), while this lists the places it is actually
// CALLED.
//
// The protocol makes it two requests:
//
//	prepareCallHierarchy(position)  ──► CallHierarchyItem[]   "what is here?"
//	callHierarchy/incomingCalls(item) ─► [{from, fromRanges}]  "who calls it?"
//
// and the item from the first is handed back to the second VERBATIM. It
// carries a server-private `data` field that identifies the symbol, which
// is exactly the field this client has no reason to model — the same
// argument Diagnostic and CompletionItem make for echoing raw JSON.

package lsp

import "encoding/json"

// IncomingCalls returns every call site of the function at pos, as plain
// Locations so the caller can list them exactly as it lists references.
//
// A call site is a (caller file, range) pair: each incoming call names
// the CALLING function (`from`) and the ranges inside it where the call
// happens (`fromRanges`), which are relative to the caller's document.
// A caller with no ranges still contributes its own name's position —
// the server knows it calls us but not where, and a row that lands on
// the right function beats no row.
//
// nil, nil means the position is not on something callable.
func (c *Client) IncomingCalls(path string, pos Position) ([]Location, error) {
	prep := TextDocumentPositionParams{
		TextDocument: TextDocumentIdentifier{URI: PathToURI(path)},
		Position:     pos,
	}
	var items []json.RawMessage
	if err := c.CallWithTimeout("textDocument/prepareCallHierarchy", prep, &items, referencesTimeout); err != nil {
		return nil, err
	}
	var out []Location
	for _, item := range items {
		var raw json.RawMessage
		params := map[string]any{"item": item}
		if err := c.CallWithTimeout("callHierarchy/incomingCalls", params, &raw, referencesTimeout); err != nil {
			return nil, err
		}
		out = append(out, ParseIncomingCalls(raw)...)
	}
	return out, nil
}

// ParseIncomingCalls flattens a callHierarchy/incomingCalls response into
// call-site Locations.
func ParseIncomingCalls(raw json.RawMessage) []Location {
	if len(raw) == 0 || string(raw) == "null" {
		return nil
	}
	var calls []struct {
		From struct {
			URI            string `json:"uri"`
			SelectionRange Range  `json:"selectionRange"`
		} `json:"from"`
		FromRanges []Range `json:"fromRanges"`
	}
	if err := json.Unmarshal(raw, &calls); err != nil {
		return nil
	}
	var out []Location
	for _, call := range calls {
		if call.From.URI == "" {
			continue
		}
		if len(call.FromRanges) == 0 {
			out = append(out, Location{URI: call.From.URI, Range: call.From.SelectionRange})
			continue
		}
		for _, r := range call.FromRanges {
			out = append(out, Location{URI: call.From.URI, Range: r})
		}
	}
	return out
}
