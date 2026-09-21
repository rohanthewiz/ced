// =============================================================================
// File: internal/lsp/inlayhint.go
// Author: Rohan Allison <rohanthewiz@gmail.com>
// Created: 2026-09-21
// Copyright: 2026 Rohan Allison. All rights reserved.
// =============================================================================

// inlayhint.go is textDocument/inlayHint — the annotations a server would
// write into the code if it could: the type `x := f()` inferred, the
// parameter name a bare `true` is being passed as.

package lsp

import (
	"encoding/json"
	"strings"
)

// InlayHintKind values.
const (
	InlayType      = 1
	InlayParameter = 2
)

// InlayHint is one hint, normalised: the label's two wire shapes (a plain
// string, or an array of parts each carrying a `value`) are already
// joined, because the parts exist to hang tooltips and click targets on
// and this client renders text.
type InlayHint struct {
	Pos   Position
	Label string
	Kind  int // 0 when the server did not say
}

// ParseInlayHints normalises an inlayHint response. Hints with an empty
// label are dropped — there would be nothing to draw.
func ParseInlayHints(raw json.RawMessage) []InlayHint {
	if len(raw) == 0 || string(raw) == "null" {
		return nil
	}
	var wire []struct {
		Position Position        `json:"position"`
		Label    json.RawMessage `json:"label"`
		Kind     int             `json:"kind"`
	}
	if err := json.Unmarshal(raw, &wire); err != nil {
		return nil
	}
	out := make([]InlayHint, 0, len(wire))
	for _, w := range wire {
		label := inlayLabel(w.Label)
		if label == "" {
			continue
		}
		out = append(out, InlayHint{Pos: w.Position, Label: label, Kind: w.Kind})
	}
	return out
}

// inlayLabel joins a label's wire form into text. The discriminator is
// the JSON TYPE (string vs array), never a failed unmarshal — the house
// rule for every union in this package.
func inlayLabel(raw json.RawMessage) string {
	trimmed := strings.TrimSpace(string(raw))
	if trimmed == "" {
		return ""
	}
	if trimmed[0] == '"' {
		var s string
		_ = json.Unmarshal(raw, &s)
		return strings.TrimSpace(s)
	}
	var parts []struct {
		Value string `json:"value"`
	}
	if err := json.Unmarshal(raw, &parts); err != nil {
		return ""
	}
	var b strings.Builder
	for _, p := range parts {
		b.WriteString(p.Value)
	}
	return strings.TrimSpace(b.String())
}

// InlayHints asks for the hints inside rng.
func (c *Client) InlayHints(path string, rng Range) ([]InlayHint, error) {
	params := map[string]any{
		"textDocument": TextDocumentIdentifier{URI: PathToURI(path)},
		"range":        rng,
	}
	var raw json.RawMessage
	if err := c.Call("textDocument/inlayHint", params, &raw); err != nil {
		return nil, err
	}
	return ParseInlayHints(raw), nil
}
