// =============================================================================
// File: internal/lsp/progress.go
// Author: Rohan Allison <rohanthewiz@gmail.com>
// Created: 2026-09-21
// Copyright: 2026 Rohan Allison. All rights reserved.
// =============================================================================

// progress.go decodes the two notifications a server uses to talk to the
// PERSON rather than to the editor: $/progress (work-done reports —
// "Loading packages 12/40") and window/showMessage.
//
// Neither changes what the client does next. They matter because a cold
// server answers every question with "nothing" for its first several
// seconds, and without these the only thing the user sees is a verb that
// appears not to work.

package lsp

import "encoding/json"

// Progress kinds, as the spec spells them in WorkDoneProgress*.kind.
const (
	ProgressBegin  = "begin"
	ProgressReport = "report"
	ProgressEnd    = "end"
)

// Progress is one work-done report, normalised.
type Progress struct {
	// Token identifies the piece of work. The wire allows an integer or a
	// string; both are kept as the raw JSON text, which is all a map key
	// needs to be.
	Token string
	Kind  string
	// Title arrives on begin only; Message and Percent may arrive on
	// begin and on any report. Percent is -1 when the server gave none.
	Title   string
	Message string
	Percent int
}

// ParseProgress decodes $/progress params. ok=false covers both a
// malformed payload and a progress value that is not WORK-DONE progress:
// the same notification carries partial results for requests that asked
// for them, which have no `kind` and are nobody's business here.
func ParseProgress(params json.RawMessage) (Progress, bool) {
	var wire struct {
		Token json.RawMessage `json:"token"`
		Value struct {
			Kind       string `json:"kind"`
			Title      string `json:"title"`
			Message    string `json:"message"`
			Percentage *int   `json:"percentage"`
		} `json:"value"`
	}
	if err := json.Unmarshal(params, &wire); err != nil || len(wire.Token) == 0 {
		return Progress{}, false
	}
	switch wire.Value.Kind {
	case ProgressBegin, ProgressReport, ProgressEnd:
	default:
		return Progress{}, false
	}
	p := Progress{
		Token: string(wire.Token), Kind: wire.Value.Kind,
		Title: wire.Value.Title, Message: wire.Value.Message, Percent: -1,
	}
	if wire.Value.Percentage != nil {
		p.Percent = *wire.Value.Percentage
	}
	return p, true
}

// MessageType values for window/showMessage.
const (
	MessageError   = 1
	MessageWarning = 2
	MessageInfo    = 3
	MessageLog     = 4
)

// ParseShowMessage decodes window/showMessage params.
func ParseShowMessage(params json.RawMessage) (typ int, text string, ok bool) {
	var wire struct {
		Type    int    `json:"type"`
		Message string `json:"message"`
	}
	if err := json.Unmarshal(params, &wire); err != nil || wire.Message == "" {
		return 0, "", false
	}
	return wire.Type, wire.Message, true
}
