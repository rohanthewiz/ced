// =============================================================================
// File: internal/lsp/workspacesymbol.go
// Author: Rohan Allison <rohanthewiz@gmail.com>
// Created: 2026-09-21
// Copyright: 2026 Rohan Allison. All rights reserved.
// =============================================================================

// workspacesymbol.go is workspace/symbol — "find a declaration by name,
// anywhere in the project". It is documentSymbol's query-driven sibling:
// that one takes a file and returns its outline, this one takes a STRING
// and returns whatever the server's own index matches it against.

package lsp

import (
	"encoding/json"
	"sort"
)

// WorkspaceSymbol is one hit: a declaration and where it lives. Unlike
// Symbol it carries a PATH, because the whole point is that the answer
// is usually in a file that isn't open.
type WorkspaceSymbol struct {
	Name      string
	Kind      int
	Container string // enclosing package / type, as the server spells it
	Path      string
	Pos       Position // the declaration's start
}

// workspaceSymbolWire decodes both spec shapes at once. SymbolInformation
// and the 3.17 WorkspaceSymbol differ only in whether location.range is
// guaranteed present; a range-less location means the server expects a
// workspaceSymbol/resolve round trip, which this client never declared
// (no resolveSupport) and so a conforming server never sends. One that
// does anyway lands on line 0 of the right file rather than nowhere.
type workspaceSymbolWire struct {
	Name          string `json:"name"`
	Kind          int    `json:"kind"`
	ContainerName string `json:"containerName"`
	Location      struct {
		URI   string `json:"uri"`
		Range Range  `json:"range"`
	} `json:"location"`
}

// ParseWorkspaceSymbols normalises a workspace/symbol response. Hits
// with no plain-file URI are dropped — the editor could not open them.
// Order is the SERVER's: it ranked these against the query, and a
// re-sort here would throw that ranking away. The sort is stable and
// only breaks exact ties, so a server that returns an unordered set
// still produces the same list twice.
func ParseWorkspaceSymbols(raw json.RawMessage) []WorkspaceSymbol {
	if len(raw) == 0 || string(raw) == "null" {
		return nil
	}
	var wire []workspaceSymbolWire
	if err := json.Unmarshal(raw, &wire); err != nil {
		return nil
	}
	out := make([]WorkspaceSymbol, 0, len(wire))
	for _, w := range wire {
		path := URIToPath(w.Location.URI)
		if path == "" || w.Name == "" {
			continue
		}
		out = append(out, WorkspaceSymbol{
			Name: w.Name, Kind: w.Kind, Container: w.ContainerName,
			Path: path, Pos: w.Location.Range.Start,
		})
	}
	return out
}

// SortWorkspaceSymbols orders hits by name, then path, then line. It is
// for callers that MERGE several servers' answers, where no single
// ranking survives; a single server's list should be left as it came.
func SortWorkspaceSymbols(syms []WorkspaceSymbol) {
	sort.SliceStable(syms, func(i, j int) bool {
		if syms[i].Name != syms[j].Name {
			return syms[i].Name < syms[j].Name
		}
		if syms[i].Path != syms[j].Path {
			return syms[i].Path < syms[j].Path
		}
		return syms[i].Pos.Line < syms[j].Pos.Line
	})
}

// WorkspaceSymbols asks the server for declarations matching query. The
// budget is references' 30s: this is a project-wide index lookup, and on
// a cold server it waits for the workspace to finish loading.
func (c *Client) WorkspaceSymbols(query string) ([]WorkspaceSymbol, error) {
	var raw json.RawMessage
	params := map[string]any{"query": query}
	if err := c.CallWithTimeout("workspace/symbol", params, &raw, referencesTimeout); err != nil {
		return nil, err
	}
	return ParseWorkspaceSymbols(raw), nil
}
